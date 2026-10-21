import * as React from "react";
import {
  Activity,
  ArrowUpRight,
  CalendarDays,
  CheckCircle2,
  CircleAlert,
  Clock3,
  Globe2,
  HeartPulse,
  MapPin,
  RefreshCw,
  ServerCog,
  Wifi
} from "lucide-react";
import {
  api,
  type AdminAccount,
  type Course,
  type Profile,
  type SignAudit,
  type WatchSchedule
} from "../lib/api";
import { Avatar } from "../components/avatar";
import { CourseCard } from "../components/course-card";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { ErrorBanner, PageHeader } from "../components/common";
import { useUIStore } from "../lib/store";
import { roleLabels } from "../lib/roles";
import { teachingWeek, weekLabel, weekMatches } from "../lib/teaching-week";
import { formatBeijing } from "../lib/time";
import { cn } from "../lib/utils";

type Tone = "ok" | "warn" | "danger" | "muted";

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

function toneClass(tone: Tone) {
  switch (tone) {
    case "ok":
      return "text-success";
    case "warn":
      return "text-warning";
    case "danger":
      return "text-destructive";
    default:
      return "text-muted-foreground";
  }
}

function beijingClock(value: Date) {
  const shifted = new Date(value.getTime() + 8 * 60 * 60 * 1000);
  const minutes = shifted.getUTCHours() * 60 + shifted.getUTCMinutes();
  return {
    day: dayCodes[shifted.getUTCDay()],
    minutes,
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

function statusText(item: SignAudit) {
  if (item.verified) return "已确认";
  if (item.state === "failed") return "失败";
  return item.code || "待确认";
}

export function Dashboard() {
  const session = useUIStore((state) => state.session);
  const account = useUIStore((state) => state.account);
  const setPage = useUIStore((state) => state.setPage);
  const [status, setStatus] = React.useState<any>(null);
  const [profile, setProfile] = React.useState<Profile | null>(null);
  const [accountPolicy, setAccountPolicy] = React.useState<AdminAccount | null>(null);
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
  const canManageAccounts = Boolean(session?.capabilities?.manage_accounts);
  const accountID = account?.current ?? session?.account ?? "";

  const load = React.useCallback(async () => {
    const epoch = ++requestEpoch.current;
    setBusy(true);
    setError("");
    setAuditsLoading(canReadAudits);
    setAuditsError("");
    const [statusResult, profileResult, coursesResult, schedulesResult, policyResult, auditsResult] =
      await Promise.allSettled([
        api.status(),
        api.profile(),
        api.call("list_courses"),
        api.watchSchedules(),
        canManageAccounts ? api.adminAccounts() : Promise.resolve(null),
        canReadAudits ? api.signAudits(12) : Promise.resolve(null)
      ]);
    if (epoch !== requestEpoch.current) return;
    const failures: string[] = [];
    if (statusResult.status === "fulfilled") setStatus(statusResult.value);
    else failures.push(`运行状态：${errorMessage(statusResult.reason)}`);
    if (profileResult.status === "fulfilled") setProfile(profileResult.value);
    else setProfile(null);
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
    }
    if (policyResult.status === "fulfilled" && policyResult.value) {
      setAccountPolicy(
        policyResult.value.items.find((item) => item.id === accountID) ?? null
      );
    } else {
      setAccountPolicy(null);
    }
    if (canReadAudits) {
      if (auditsResult.status === "fulfilled" && auditsResult.value) {
        setSignAudits(auditsResult.value.items ?? []);
      } else {
        setSignAudits([]);
        if (auditsResult.status === "rejected") setAuditsError(errorMessage(auditsResult.reason));
      }
    } else {
      setSignAudits([]);
    }
    setError(failures.join("；"));
    setAuditsLoading(false);
    setBusy(false);
  }, [accountID, canManageAccounts, canReadAudits]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const runtime = status?.runtime ?? {};
  const egress: any[] = runtime.egress ?? [];
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
  const processCrashes = Number(process.consecutive_crashes) || 0;
  const processDegraded = Boolean(process.degraded);
  const coolingEgress = egress.filter((item) => item.cooldown).length;
  const upstreamHealthy = coursesMeta?.status === "complete";
  const statusPending = busy && !status;
  const accountName = account?.accounts.find((item) => item.id === accountID)?.name ?? accountID;
  const displayName = profile?.display_name || accountName || session?.user?.display_name || "当前账号";
  const roleLabel = session?.user?.role ? roleLabels[session.user.role] : "成员";
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
        const state = start === null || end === null ? "unknown" : clock.minutes >= start && clock.minutes < end ? "active" : clock.minutes >= end ? "done" : "upcoming";
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
  const dashboardCourses = React.useMemo(
    () =>
      [...courses]
        .sort((a, b) => Number(Boolean(a.retired)) - Number(Boolean(b.retired)))
        .slice(0, 4),
    [courses]
  );

  const statusItems = [
    {
      icon: Wifi,
      label: "上游",
      value: statusPending ? "读取中" : upstreamHealthy ? "可用" : "待确认",
      tone: (statusPending || !upstreamHealthy ? "warn" : "ok") as Tone
    },
    {
      icon: HeartPulse,
      label: "轮询",
      value: watcherRows.length === 0 ? "未启用" : watchersStale ? "已停滞" : watcherPanics > 0 ? "需关注" : "运行中",
      tone: (watchersStale || watcherPanics > 0 ? "warn" : "ok") as Tone
    },
    {
      icon: ServerCog,
      label: "进程",
      value: processDegraded ? "降级" : processCrashes > 0 ? "需关注" : "稳定",
      tone: (processDegraded || processCrashes > 0 ? "warn" : "ok") as Tone
    },
    {
      icon: Globe2,
      label: "出口",
      value: egress.length === 0 ? "直连" : coolingEgress > 0 ? "部分冷却" : "代理就绪",
      tone: (coolingEgress > 0 ? "warn" : "ok") as Tone
    }
  ];

  const accountBadges = (
    <>
      {accountPolicy ? (
        <>
          <Badge variant={accountPolicy.auto_sign ? "success" : "warning"}>
            {accountPolicy.auto_sign ? "签到自动" : "签到待开启"}
          </Badge>
          <Badge variant={accountPolicy.auto_answer ? "warning" : "secondary"}>
            {accountPolicy.auto_answer ? "答题自动" : "答题关闭"}
          </Badge>
          {accountPolicy.notify_only && <Badge variant="secondary">通知优先</Badge>}
        </>
      ) : null}
    </>
  );

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
              课堂活动
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

      <section className="overflow-hidden rounded-card border border-border bg-card">
        <div className="flex items-center gap-3 px-4 py-3 sm:px-5">
          <Avatar
            src={profile?.avatar_url}
            name={displayName}
            className="h-9 w-9 border-border-strong bg-secondary"
          />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
              <h2 className="truncate text-[14px] font-medium tracking-[-0.02em]">{displayName}</h2>
              <span className="text-[11px] text-muted-foreground">{roleLabel}</span>
              {accountName && <span className="truncate text-[11px] text-muted-foreground">· {accountName}</span>}
              <span className="flex flex-wrap items-center gap-1.5 sm:ml-1">{accountBadges}</span>
            </div>
          </div>
        </div>
        <div className="grid grid-cols-2 gap-x-4 gap-y-1.5 border-t border-border px-4 py-2.5 sm:flex sm:flex-wrap sm:items-center sm:gap-x-5 sm:px-5">
          {statusItems.map((item) => (
            <span key={item.label} className="inline-flex min-w-0 items-center gap-1.5 text-[11px]">
              <item.icon className={cn("h-3 w-3 shrink-0", toneClass(item.tone))} />
              <span className="text-muted-foreground">{item.label}</span>
              <span className={cn("font-medium", toneClass(item.tone))}>{item.value}</span>
            </span>
          ))}
          <button
            type="button"
            onClick={() => setPage("settings")}
            className="ml-auto hidden text-[11px] text-muted-foreground transition-colors hover:text-foreground sm:inline"
          >
            运行设置
          </button>
        </div>
      </section>

      <div className="grid min-w-0 gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(300px,1fr)]">
        <section className="min-w-0 overflow-hidden rounded-card border border-border bg-card">
          <div className="border-b border-border px-4 py-3.5 sm:px-5">
            <div className="flex items-center justify-between gap-2">
              <div className="flex min-w-0 items-center gap-2">
                <CalendarDays className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <h2 className="truncate text-[13px] font-medium">今日课堂</h2>
              </div>
              <Button size="sm" variant="ghost" className="-my-1 shrink-0 px-1.5 text-[11px]" onClick={() => setPage("study")}>
                完整课表
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
                        {row.weeks && (
                          <>
                            <span aria-hidden="true">·</span>
                            <span className="shrink-0">{row.weeks.includes("-") ? `第 ${row.weeks} 周` : `周次 ${row.weeks}`}</span>
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
          <div className="flex items-start justify-between gap-2 border-b border-border px-4 py-3.5 sm:px-5">
            <div>
              <div className="flex items-center gap-2">
                <CheckCircle2 className="h-3.5 w-3.5 text-muted-foreground" />
                <h2 className="text-[13px] font-medium">最近签到</h2>
              </div>
            </div>
          </div>
          {!canReadAudits ? (
            <div className="px-4 py-5 text-[11px] leading-5 text-muted-foreground sm:px-5">
              当前角色无签到审计权限。
            </div>
          ) : auditsLoading ? (
            <div className="space-y-2 px-4 py-4 sm:px-5">
              {[0, 1, 2].map((item) => (
                <div key={item} className="h-12 animate-pulse rounded-control bg-muted/55" />
              ))}
            </div>
          ) : auditsError ? (
            <div className="flex items-start gap-2 px-4 py-5 text-[11px] leading-5 text-warning sm:px-5">
              <CircleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>签到记录暂不可用：{auditsError}</span>
            </div>
          ) : recentSigns.length === 0 ? (
            <div className="px-4 py-5 text-[11px] leading-5 text-muted-foreground sm:px-5">
              暂无签到记录。
            </div>
          ) : (
            <ul className="divide-y divide-border">
              {recentSigns.map((item, index) => {
                const course = courseLookup.get(courseKey(item.course_id, item.class_id));
                return (
                  <li key={`${item.created_at}:${item.activity_id}:${index}`} className="flex min-w-0 items-start gap-2.5 px-4 py-3 sm:px-5">
                    <span className={cn("mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full", item.verified ? "bg-success" : item.state === "failed" ? "bg-destructive" : "bg-warning")} />
                    <div className="min-w-0 flex-1">
                      <div className="flex min-w-0 items-center justify-between gap-2">
                        <span className="truncate text-[12px] font-medium">{course?.name || item.course_id}</span>
                        <Badge variant={auditVariant(item)} className="shrink-0">{statusText(item)}</Badge>
                      </div>
                      <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[11px] text-muted-foreground">
                        <span>{methodLabel(item.method)}</span>
                        <span aria-hidden="true">·</span>
                        <span>{formatBeijing(item.created_at)}</span>
                      </div>
                      <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[11px] text-muted-foreground">
                        {item.egress_ip && <span className="font-mono">{item.egress_ip}</span>}
                        {item.address && <span className="truncate" title={item.address}>{item.address}</span>}
                      </div>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      </div>

      <section className="min-w-0">
        <div className="mb-3 flex flex-wrap items-end justify-between gap-2">
          <div>
            <div className="flex items-center gap-2">
              <Activity className="h-3.5 w-3.5 text-muted-foreground" />
              <h2 className="text-[13px] font-medium">课程</h2>
            </div>
          </div>
          <Button size="sm" variant="ghost" onClick={() => setPage("courses")}>
            全部课程
            <ArrowUpRight className="h-3.5 w-3.5" />
          </Button>
        </div>
        {statusPending && courses.length === 0 ? (
          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 lg:grid-cols-4">
            {[0, 1, 2, 3].map((item) => (
              <div key={item} className="h-[86px] animate-pulse rounded-card border border-border bg-muted/35 sm:h-44" />
            ))}
          </div>
        ) : courses.length === 0 ? (
          <div className="rounded-card border border-border bg-card px-4 py-5 text-[11px] leading-5 text-muted-foreground">
            未读取到课程目录。
          </div>
        ) : (
          <div className="grid min-w-0 grid-cols-1 gap-2.5 sm:grid-cols-2 lg:grid-cols-4">
            {dashboardCourses.map((course) => (
              <CourseCard key={courseKey(course.course_id, course.class_id)} course={course} />
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function errorMessage(value: unknown) {
  return value instanceof Error ? value.message : String(value);
}
