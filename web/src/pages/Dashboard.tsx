import * as React from "react";
import {
  ArrowUpRight,
  CalendarDays,
  CheckCircle2,
  CircleAlert,
  Clock3,
  MapPin,
  RefreshCw
} from "lucide-react";
import { api, type Course, type SignAudit, type WatchSchedule } from "../lib/api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { ErrorBanner, PageHeader } from "../components/common";
import { useUIStore } from "../lib/store";
import { teachingWeek, weekLabel, weekMatches } from "../lib/teaching-week";
import { formatBeijing } from "../lib/time";
import { cn } from "../lib/utils";

const dayCodes = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const dayLabels: Record<string, string> = {
  Sun: "周日",
  Mon: "周一",
  Tue: "周二",
  Wed: "周三",
  Thu: "周四",
  Fri: "周五",
  Sat: "周六"
};

function beijingClock(value: Date) {
  const shifted = new Date(value.getTime() + 8 * 60 * 60 * 1000);
  return {
    day: dayCodes[shifted.getUTCDay()],
    minutes: shifted.getUTCHours() * 60 + shifted.getUTCMinutes(),
    time: `${String(shifted.getUTCHours()).padStart(2, "0")}:${String(shifted.getUTCMinutes()).padStart(2, "0")}`,
    date: new Intl.DateTimeFormat("zh-CN", {
      timeZone: "Asia/Shanghai",
      month: "long",
      day: "numeric",
      weekday: "long"
    }).format(value)
  };
}

function minuteOfDay(value?: string) {
  if (!value) return null;
  const [hour, minute] = value.split(":").map(Number);
  if (!Number.isFinite(hour) || !Number.isFinite(minute)) return null;
  return hour * 60 + minute;
}

function courseKey(courseID: string, classID: string) {
  return `${courseID}:${classID}`;
}

function methodLabel(value?: string) {
  switch (value) {
    case "location":
      return "定位签到";
    case "qr":
      return "二维码";
    case "gesture":
      return "手势";
    case "photo":
      return "拍照";
    case "face":
      return "人脸";
    case "normal":
      return "普通签到";
    default:
      return value || "签到";
  }
}

function auditVariant(item: SignAudit) {
  if (item.verified) return "success" as const;
  if (item.state === "failed" || item.code) return "destructive" as const;
  return "warning" as const;
}

function auditText(item: SignAudit) {
  if (item.verified) return "已确认";
  if (item.state === "failed") return "失败";
  return item.code || "待确认";
}

function errorMessage(value: unknown) {
  return value instanceof Error ? value.message : String(value);
}

export function Dashboard() {
  const session = useUIStore((state) => state.session);
  const account = useUIStore((state) => state.account);
  const setPage = useUIStore((state) => state.setPage);
  const [status, setStatus] = React.useState<any>(null);
  const [courses, setCourses] = React.useState<Course[]>([]);
  const [coursesMeta, setCoursesMeta] = React.useState<any>(null);
  const [schedules, setSchedules] = React.useState<WatchSchedule[]>([]);
  const [termStart, setTermStart] = React.useState("");
  const [signAudits, setSignAudits] = React.useState<SignAudit[]>([]);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [auditsLoading, setAuditsLoading] = React.useState(true);
  const [auditsError, setAuditsError] = React.useState("");
  const requestEpoch = React.useRef(0);
  const canReadAudits = Boolean(session?.capabilities?.sensitive_read);
  const accountID = account?.current ?? session?.account ?? "";

  const load = React.useCallback(async () => {
    const epoch = ++requestEpoch.current;
    setBusy(true);
    setError("");
    setAuditsLoading(canReadAudits);
    setAuditsError("");
    const [statusResult, coursesResult, schedulesResult, auditsResult] = await Promise.allSettled([
      api.status(),
      api.call("list_courses"),
      api.watchSchedules(),
      canReadAudits ? api.signAudits(6) : Promise.resolve(null)
    ]);
    if (epoch !== requestEpoch.current) return;

    const failures: string[] = [];
    if (statusResult.status === "fulfilled") setStatus(statusResult.value);
    else failures.push(`运行状态：${errorMessage(statusResult.reason)}`);

    if (coursesResult.status === "fulfilled") {
      setCourses((coursesResult.value.data.result as Course[] | undefined) ?? []);
      setCoursesMeta(coursesResult.value.data.meta ?? null);
    } else {
      setCourses([]);
      setCoursesMeta(null);
      failures.push(`课程目录：${errorMessage(coursesResult.reason)}`);
    }

    if (schedulesResult.status === "fulfilled") {
      setSchedules(schedulesResult.value.items ?? []);
      setTermStart(schedulesResult.value.term_start ?? "");
    } else {
      setSchedules([]);
      setTermStart("");
      failures.push(`课表：${errorMessage(schedulesResult.reason)}`);
    }

    if (!canReadAudits) {
      setSignAudits([]);
    } else if (auditsResult.status === "fulfilled" && auditsResult.value) {
      setSignAudits(auditsResult.value.items ?? []);
    } else {
      setSignAudits([]);
      if (auditsResult.status === "rejected") setAuditsError(errorMessage(auditsResult.reason));
    }

    setError(failures.join("；"));
    setAuditsLoading(false);
    setBusy(false);
  }, [accountID, canReadAudits]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const runtime = status?.runtime ?? {};
  const serverNow = runtime.now ? new Date(runtime.now) : new Date();
  const clock = beijingClock(Number.isNaN(serverNow.getTime()) ? new Date() : serverNow);
  const currentWeek = teachingWeek(Number.isNaN(serverNow.getTime()) ? new Date() : serverNow, termStart);
  const staleMs = 15 * 60 * 1000;

  function isStale(row: Record<string, any>) {
    const reference = row.last_tick_at || row.started_at;
    if (!reference) return false;
    const at = new Date(reference).getTime();
    return Number.isFinite(at) && serverNow.getTime() - at > staleMs;
  }

  const watcherRows: Array<Record<string, any> & { account: string }> = Object.entries(runtime.watchers ?? {}).map(
    ([key, value]) => ({ account: key, ...(value as Record<string, any>) })
  );
  const watcherPanics = watcherRows.reduce((sum, item) => sum + (Number(item.consecutive_panics) || 0), 0);
  const watchersStale = watcherRows.some((item) => isStale(item));
  const process: Record<string, any> = runtime.process ?? {};
  const processDegraded = Boolean(process.degraded) || Number(process.consecutive_crashes) > 0;
  const upstreamHealthy = coursesMeta?.status === "complete";
  const courseLookup = React.useMemo(
    () => new Map(courses.map((course) => [courseKey(course.course_id, course.class_id), course])),
    [courses]
  );
  const todaySchedules = React.useMemo(() => {
    const seen = new Set<string>();
    return schedules
      .filter((row) => row.days?.includes(clock.day) && weekMatches(currentWeek, row.weeks))
      .map((row) => {
        const course = courseLookup.get(courseKey(row.course_id, row.class_id));
        const start = minuteOfDay(row.start);
        const end = minuteOfDay(row.end);
        const state = start === null || end === null
          ? "unknown"
          : clock.minutes >= start && clock.minutes < end
            ? "active"
            : clock.minutes >= end
              ? "done"
              : "upcoming";
        return { ...row, course, state };
      })
      .filter((row) => {
        const key = `${row.course_id}:${row.class_id}:${row.key}:${row.start}:${row.end}`;
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      })
      .sort((a, b) => (minuteOfDay(a.start) ?? 0) - (minuteOfDay(b.start) ?? 0));
  }, [clock.day, clock.minutes, courseLookup, currentWeek, schedules]);
  const activeSchedule = todaySchedules.find((row) => row.state === "active");
  const nextSchedule = activeSchedule ?? todaySchedules.find((row) => row.state === "upcoming");
  const recentSigns = signAudits.slice(0, 3);
  const watcherState = watcherRows.length === 0
    ? { value: "未启用", className: "text-muted-foreground" }
    : watchersStale || watcherPanics > 0
      ? { value: watchersStale ? "已停滞" : "需关注", className: "text-warning" }
      : { value: "正常", className: "text-success" };
  const serviceState = processDegraded
    ? { value: "需关注", className: "text-warning" }
    : { value: "稳定", className: "text-success" };
  const upstreamState = busy && !status
    ? { value: "读取中", className: "text-muted-foreground" }
    : upstreamHealthy
      ? { value: "正常", className: "text-success" }
      : { value: "待确认", className: "text-warning" };

  return (
    <div className="space-y-4">
      <PageHeader
        title="总览"
        meta={
          <>
            <span>{clock.date}</span>
            <span aria-hidden="true">·</span>
            <span>北京时间 {clock.time}</span>
            {currentWeek > 0 && (
              <>
                <span aria-hidden="true">·</span>
                <span>{weekLabel(currentWeek)}</span>
              </>
            )}
          </>
        }
        actions={
          <>
            <Button size="sm" variant="outline" onClick={() => setPage("activities")}>
              课堂
              <ArrowUpRight className="h-3.5 w-3.5" />
            </Button>
            <Button size="sm" variant="ghost" onClick={() => void load()} disabled={busy}>
              <RefreshCw className={cn("h-3.5 w-3.5", busy && "animate-spin")} />
              刷新
            </Button>
          </>
        }
      />
      <ErrorBanner error={error} />

      <div className="grid min-w-0 gap-4 xl:grid-cols-[minmax(0,1.45fr)_minmax(300px,1fr)]">
        <section className="min-w-0 overflow-hidden rounded-card border border-border bg-card">
          <div className="border-b border-border px-4 py-3.5 sm:px-5">
            <div className="flex items-center justify-between gap-2">
              <div className="flex min-w-0 items-center gap-2">
                <CalendarDays className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <h2 className="text-[13px] font-medium">今日课堂</h2>
              </div>
              <Button size="sm" variant="ghost" className="shrink-0 px-1.5 text-[11px]" onClick={() => setPage("study")}>
                课表
                <ArrowUpRight className="h-3.5 w-3.5" />
              </Button>
            </div>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {dayLabels[clock.day]}
              {activeSchedule
                ? ` · 进行中 ${activeSchedule.course?.name || activeSchedule.course_id} 至 ${activeSchedule.end}`
                : nextSchedule
                  ? ` · 下一段 ${nextSchedule.course?.name || nextSchedule.course_id} ${nextSchedule.start}`
                  : " · 今天没有待开始时段"}
              {currentWeek > 0 ? ` · ${weekLabel(currentWeek)}` : ""}
            </p>
          </div>
          {todaySchedules.length === 0 ? (
            <div className="flex items-start gap-2 px-4 py-6 text-[12px] text-muted-foreground sm:px-5">
              <Clock3 className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>今天没有课堂时段。</span>
            </div>
          ) : (
            <ol className="divide-y divide-border">
              {todaySchedules.map((row) => {
                const active = row.state === "active";
                return (
                  <li
                    key={`${row.course_id}:${row.class_id}:${row.key}:${row.start}`}
                    className={cn(
                      "grid grid-cols-[58px_minmax(0,1fr)_auto] items-center gap-3 px-4 py-3 sm:px-5",
                      active && "bg-accent/55"
                    )}
                  >
                    <div className={cn("font-mono text-[11px]", active ? "text-foreground" : "text-muted-foreground")}>
                      <div>{row.start}</div>
                      <div className="text-[11px] text-muted-foreground">{row.end}</div>
                    </div>
                    <div className="min-w-0">
                      <div className="flex min-w-0 items-center gap-2">
                        <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", row.state === "active" ? "bg-primary" : row.state === "done" ? "bg-muted-foreground/45" : "bg-success")} />
                        <span className="truncate text-[13px] font-medium">{row.course?.name || row.course_id}</span>
                      </div>
                      <div className="mt-1 flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
                        <span className="truncate">{row.course?.teacher || "教师未标注"}</span>
                        {row.room && (
                          <>
                            <span aria-hidden="true">·</span>
                            <MapPin className="h-2.5 w-2.5 shrink-0" />
                            <span className="truncate">{row.room}</span>
                          </>
                        )}
                      </div>
                    </div>
                    <div className="text-right">
                      {row.state === "active" ? (
                        <Badge variant="default">进行中</Badge>
                      ) : row.state === "done" ? (
                        <span className="text-[11px] text-muted-foreground">已结束</span>
                      ) : (
                        <span className="text-[11px] text-muted-foreground">待开始</span>
                      )}
                    </div>
                  </li>
                );
              })}
            </ol>
          )}
        </section>

        <section className="min-w-0 overflow-hidden rounded-card border border-border bg-card">
          <div className="border-b border-border px-4 py-3.5 sm:px-5">
            <div className="flex items-center gap-2">
              <CheckCircle2 className="h-3.5 w-3.5 text-muted-foreground" />
              <h2 className="text-[13px] font-medium">签到与轮询</h2>
            </div>
            <div className="mt-3 grid grid-cols-3 gap-2 text-[11px]">
              <div>
                <div className="text-muted-foreground">轮询</div>
                <div className={cn("mt-0.5 font-medium", watcherState.className)}>{watcherState.value}</div>
              </div>
              <div>
                <div className="text-muted-foreground">上游</div>
                <div className={cn("mt-0.5 font-medium", upstreamState.className)}>{upstreamState.value}</div>
              </div>
              <div>
                <div className="text-muted-foreground">服务</div>
                <div className={cn("mt-0.5 font-medium", serviceState.className)}>{serviceState.value}</div>
              </div>
            </div>
          </div>
          <div className="px-4 py-3 sm:px-5">
            <div className="mb-2 text-[11px] font-medium text-muted-foreground">最近签到</div>
            {!canReadAudits ? (
              <div className="text-[11px] leading-5 text-muted-foreground">当前角色无签到审计权限。</div>
            ) : auditsLoading ? (
              <div className="space-y-2">
                {[0, 1, 2].map((item) => (
                  <div key={item} className="h-12 animate-pulse rounded-control bg-muted/55" />
                ))}
              </div>
            ) : auditsError ? (
              <div className="flex items-start gap-2 text-[11px] leading-5 text-warning">
                <CircleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span>签到记录暂不可用：{auditsError}</span>
              </div>
            ) : recentSigns.length === 0 ? (
              <div className="text-[11px] leading-5 text-muted-foreground">暂无签到记录。</div>
            ) : (
              <ul className="divide-y divide-border">
                {recentSigns.map((item, index) => {
                  const course = courseLookup.get(courseKey(item.course_id, item.class_id));
                  return (
                    <li key={`${item.created_at}:${item.activity_id}:${index}`} className="flex min-w-0 items-start gap-2.5 py-2.5 first:pt-0 last:pb-0">
                      <span className={cn("mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full", item.verified ? "bg-success" : item.state === "failed" ? "bg-destructive" : "bg-warning")} />
                      <div className="min-w-0 flex-1">
                        <div className="flex min-w-0 items-center justify-between gap-2">
                          <span className="truncate text-[12px] font-medium">{course?.name || item.course_id}</span>
                          <Badge variant={auditVariant(item)} className="shrink-0">{auditText(item)}</Badge>
                        </div>
                        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[11px] text-muted-foreground">
                          <span>{methodLabel(item.method)}</span>
                          <span aria-hidden="true">·</span>
                          <span>{formatBeijing(item.created_at)}</span>
                        </div>
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}
