import * as React from "react";
import {
  Activity,
  ArrowUpRight,
  CalendarDays,
  CheckCircle2,
  CircleAlert,
  Clock3,
  Database,
  Globe2,
  HeartPulse,
  MapPin,
  RefreshCw,
  ServerCog,
  ShieldCheck,
  Wifi
} from "lucide-react";
import {
  api,
  type AdminAccount,
  type Course,
  type Profile,
  type SignAudit,
  type ToolDefinition,
  type WatchSchedule
} from "../lib/api";
import { Avatar } from "../components/avatar";
import { CourseCard } from "../components/course-card";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { ErrorBanner, JsonBlock, MetaSummary, PageHeader } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { useUIStore } from "../lib/store";
import { roleLabels } from "../lib/roles";
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

function pulseDot(tone: Tone) {
  return {
    ok: "bg-success",
    warn: "bg-warning",
    danger: "bg-destructive",
    muted: "bg-muted-foreground/45"
  }[tone];
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

function clockLabel(value: number) {
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false
  }).format(new Date(value));
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

const PulseItem = React.memo(function PulseItem({
  icon: Icon,
  label,
  value,
  detail,
  tone
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  value: string;
  detail: string;
  tone: Tone;
}) {
  return (
    <div className="flex min-w-0 items-start gap-2.5 px-3 py-3 sm:px-4">
      <Icon className={cn("mt-0.5 h-3.5 w-3.5 shrink-0", toneClass(tone))} />
      <div className="min-w-0">
        <div className="text-[10px] font-medium uppercase tracking-[0.09em] text-muted-foreground">{label}</div>
        <div className="mt-0.5 flex min-w-0 items-center gap-1.5">
          <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", pulseDot(tone))} />
          <span className="truncate text-[12px] font-medium leading-4 text-foreground">{value}</span>
        </div>
        <div className="mt-0.5 truncate text-[10px] leading-4 text-muted-foreground">{detail}</div>
      </div>
    </div>
  );
});

export function Dashboard({ tools }: { tools: ToolDefinition[] }) {
  const session = useUIStore((state) => state.session);
  const account = useUIStore((state) => state.account);
  const setPage = useUIStore((state) => state.setPage);
  const [status, setStatus] = React.useState<any>(null);
  const [connection, setConnection] = React.useState<any>(null);
  const [capabilities, setCapabilities] = React.useState<any>(null);
  const [profile, setProfile] = React.useState<Profile | null>(null);
  const [accountPolicy, setAccountPolicy] = React.useState<AdminAccount | null>(null);
  const [courses, setCourses] = React.useState<Course[]>([]);
  const [schedules, setSchedules] = React.useState<WatchSchedule[]>([]);
  const [signAudits, setSignAudits] = React.useState<SignAudit[]>([]);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [auditsLoading, setAuditsLoading] = React.useState(true);
  const [auditsError, setAuditsError] = React.useState("");
  const requestEpoch = React.useRef(0);
  const canReadAudits = Boolean(session?.capabilities?.sensitive_read);
  const canManageAccounts = Boolean(session?.capabilities?.manage_accounts);

  const load = React.useCallback(async () => {
    const epoch = ++requestEpoch.current;
    setBusy(true);
    setError("");
    setAuditsLoading(canReadAudits);
    setAuditsError("");
    const [statusResult, connectionResult, capabilitiesResult, profileResult, coursesResult, schedulesResult, policyResult, auditsResult] =
      await Promise.allSettled([
        api.status(),
        api.call("get_connection_status"),
        api.call("get_capabilities"),
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
    if (connectionResult.status === "fulfilled") setConnection(connectionResult.value.data);
    else setConnection(null);
    if (capabilitiesResult.status === "fulfilled") setCapabilities(capabilitiesResult.value.data);
    else setCapabilities(null);
    if (profileResult.status === "fulfilled") setProfile(profileResult.value);
    else setProfile(null);
    if (coursesResult.status === "fulfilled") {
      setCourses((coursesResult.value.data.result as Course[] | undefined) ?? []);
    } else {
      setCourses([]);
      failures.push(`课程目录：${errorMessage(coursesResult.reason)}`);
    }
    if (schedulesResult.status === "fulfilled") setSchedules(schedulesResult.value.items ?? []);
    else setSchedules([]);
    if (policyResult.status === "fulfilled" && policyResult.value) {
      setAccountPolicy(
        policyResult.value.items.find((item) => item.id === (account?.current ?? session?.account ?? "")) ?? null
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
  }, [account?.current, canManageAccounts, canReadAudits, session?.account]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const runtime = status?.runtime ?? {};
  const egress: any[] = runtime.egress ?? [];
  const serverNow = runtime.now ? new Date(runtime.now) : new Date();
  const clock = beijingClock(Number.isNaN(serverNow.getTime()) ? new Date() : serverNow);
  const staleMs = 20 * 60 * 1000;

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
  const latestTick = watcherRows
    .map((item) => (item.last_tick_at ? new Date(item.last_tick_at).getTime() : 0))
    .filter((value) => Number.isFinite(value) && value > 0)
    .sort((a, b) => b - a)[0];
  const process: Record<string, any> = runtime.process ?? {};
  const processCrashes = Number(process.consecutive_crashes) || 0;
  const processDegraded = Boolean(process.degraded);
  const capabilityCount = Array.isArray(capabilities?.items) ? capabilities.items.length : null;
  const coolingEgress = egress.filter((item) => item.cooldown).length;
  const connectionHealthy = connection?.meta?.status === "complete";
  const statusPending = busy && !status;
  const statusUnknown = !status;
  const accountID = account?.current ?? session?.account ?? "";
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
      .filter((row) => row.days?.includes(clock.day))
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
  }, [clock.day, clock.minutes, courseLookup, schedules]);
  const nextSchedule = todaySchedules.find((row) => row.state === "active") ?? todaySchedules.find((row) => row.state === "upcoming");
  const recentSigns = signAudits.slice(0, 5);

  const pulse = [
    {
      icon: Wifi,
      label: "上游",
      value: statusPending ? "读取中" : statusUnknown ? "未确认" : connectionHealthy ? "正常" : "待确认",
      detail: "每次均为新鲜课程读取",
      tone: (statusPending || statusUnknown ? "warn" : connectionHealthy ? "ok" : "warn") as Tone
    },
    {
      icon: HeartPulse,
      label: "课堂轮询",
      value: watcherRows.length ? `${watcherRows.length} 个账号` : "未启用",
      detail: watchersStale ? "存在停滞 watcher" : latestTick ? `最近 ${clockLabel(latestTick)}` : "等待首次 tick",
      tone: (watchersStale || watcherPanics > 0 ? "warn" : "ok") as Tone
    },
    {
      icon: ServerCog,
      label: "进程监督",
      value: processDegraded ? "降级" : processCrashes > 0 ? "需关注" : "稳定",
      detail: processDegraded ? "状态写入降级" : processCrashes > 0 ? `连续异常 ${processCrashes} 次` : "巡检计数已持久化",
      tone: (processDegraded || processCrashes > 0 ? "warn" : "ok") as Tone
    },
    {
      icon: Globe2,
      label: "出口",
      value: egress.length ? `${egress.length} 条路径` : "直连",
      detail: coolingEgress ? `${coolingEgress} 条冷却中` : "路径健康",
      tone: (coolingEgress > 0 ? "warn" : "ok") as Tone
    },
    {
      icon: ShieldCheck,
      label: "会话",
      value: "已加固",
      detail: "HttpOnly · SameSite · CSRF",
      tone: "ok" as Tone
    },
    {
      icon: Database,
      label: "能力边界",
      value: capabilityCount === null ? "读取中" : `${capabilityCount} 项工具`,
      detail: "后端 allowlist 约束",
      tone: (capabilityCount === null ? "muted" : "ok") as Tone
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
      ) : (
        <>
          <Badge variant="success">会话已加固</Badge>
          <Badge variant="secondary">实时读取</Badge>
        </>
      )}
    </>
  );

  return (
    <div className="space-y-5">
      <PageHeader
        title="总览"
        description="今天的课堂、签到与课程放在同一视线内；状态按需展开，不把运行数字堆在首屏。"
        meta={
          <>
            <span>{clock.date}</span>
            <span aria-hidden="true">·</span>
            <span>北京时间 {clock.time}</span>
            <span aria-hidden="true">·</span>
            <span>{tools.length} 个受控工具</span>
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
        <div className="flex items-start gap-3 p-4 sm:items-center sm:p-5">
          <Avatar
            src={profile?.avatar_url}
            name={displayName}
            className="h-11 w-11 border-border-strong bg-secondary"
          />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center gap-2">
              <h2 className="truncate text-[15px] font-medium tracking-[-0.02em]">{displayName}</h2>
              <Badge variant="secondary">{roleLabel}</Badge>
            </div>
            {accountName && (
              <div className="mt-1 truncate text-[11px] text-muted-foreground">账号：{accountName}</div>
            )}
            <div className="mt-1 text-[10px] text-muted-foreground/85">私有管理面 · 数据不出租户</div>
            <div className="mt-2 flex flex-wrap items-center gap-1.5 sm:hidden">{accountBadges}</div>
          </div>
          <div className="hidden shrink-0 flex-wrap items-center gap-1.5 sm:flex">{accountBadges}</div>
        </div>
        <div className="grid grid-cols-2 gap-px border-t border-border bg-border sm:grid-cols-3 xl:grid-cols-6">
          {pulse.map((item) => (
            <div key={item.label} className="bg-card">
              <PulseItem {...item} />
            </div>
          ))}
        </div>
      </section>

      <div className="grid min-w-0 gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(300px,1fr)]">
        <section className="min-w-0 overflow-hidden rounded-card border border-border bg-card">
          <div className="flex flex-wrap items-start justify-between gap-2 border-b border-border px-4 py-3.5 sm:px-5">
            <div>
              <div className="flex items-center gap-2">
                <CalendarDays className="h-3.5 w-3.5 text-muted-foreground" />
                <h2 className="text-[13px] font-medium">今日课堂</h2>
              </div>
              <p className="mt-1 text-[11px] text-muted-foreground">
                {dayLabels[clock.day]} · 按已配置时段展示
                {nextSchedule ? ` · 下一段 ${nextSchedule.course?.name || nextSchedule.course_id} ${nextSchedule.start}` : " · 今天没有待开始时段"}
              </p>
            </div>
            <Button size="sm" variant="ghost" onClick={() => setPage("study")}>
              完整课表
              <ArrowUpRight className="h-3.5 w-3.5" />
            </Button>
          </div>
          {todaySchedules.length === 0 ? (
            <div className="flex items-start gap-2 px-4 py-6 text-[12px] text-muted-foreground sm:px-5">
              <Clock3 className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>今天没有已配置的课堂时段。调课或临时课堂会在活动列表里出现。</span>
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
                      <div className="text-[10px] opacity-65">{row.end}</div>
                    </div>
                    <div className="min-w-0">
                      <div className="flex min-w-0 items-center gap-2">
                        <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", row.state === "active" ? "bg-primary" : row.state === "done" ? "bg-muted-foreground/45" : "bg-success")} />
                        <span className="truncate text-[13px] font-medium">{row.course?.name || row.course_id}</span>
                      </div>
                      <div className="mt-1 flex min-w-0 items-center gap-1.5 text-[10px] text-muted-foreground">
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
                        <span className="text-[10px] text-muted-foreground">已结束</span>
                      ) : (
                        <span className="text-[10px] text-muted-foreground">待开始</span>
                      )}
                    </div>
                  </li>
                );
              })}
            </ol>
          )}
          <div className="border-t border-border px-4 py-2.5 text-[10px] leading-4 text-muted-foreground sm:px-5">
            课堂开始前后按配置轮询；自动签到只使用已绑定位置，不从浏览器坐标推断。
          </div>
        </section>

        <section className="min-w-0 overflow-hidden rounded-card border border-border bg-card">
          <div className="flex items-start justify-between gap-2 border-b border-border px-4 py-3.5 sm:px-5">
            <div>
              <div className="flex items-center gap-2">
                <CheckCircle2 className="h-3.5 w-3.5 text-muted-foreground" />
                <h2 className="text-[13px] font-medium">最近签到</h2>
              </div>
              <p className="mt-1 text-[11px] text-muted-foreground">出口 IP、地点与上游确认结果</p>
            </div>
            {canReadAudits && recentSigns.length > 0 && (
              <span className="text-[10px] text-muted-foreground">{recentSigns.length} 条</span>
            )}
          </div>
          {!canReadAudits ? (
            <div className="px-4 py-5 text-[11px] leading-5 text-muted-foreground sm:px-5">
              当前角色不读取签到出口与地点审计。
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
              还没有签到记录。成功或失败尝试都会写入本地审计。
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
                      <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[10px] text-muted-foreground">
                        <span>{methodLabel(item.method)}</span>
                        <span aria-hidden="true">·</span>
                        <span>{formatBeijing(item.created_at)}</span>
                      </div>
                      <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[10px] text-muted-foreground/85">
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
              <h2 className="text-[13px] font-medium">我的课程</h2>
            </div>
            <p className="mt-1 text-[11px] text-muted-foreground">课程封面、教师与班级；内部 ID 收进卡片详情。</p>
          </div>
          <Button size="sm" variant="ghost" onClick={() => setPage("courses")}>
            全部课程
            <ArrowUpRight className="h-3.5 w-3.5" />
          </Button>
        </div>
        {statusPending && courses.length === 0 ? (
          <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4">
            {[0, 1, 2, 3].map((item) => (
              <div key={item} className="h-44 animate-pulse rounded-card border border-border bg-muted/35" />
            ))}
          </div>
        ) : courses.length === 0 ? (
          <div className="rounded-card border border-border bg-card px-4 py-5 text-[11px] leading-5 text-muted-foreground">
            课程目录尚未加载。真实课程只在后端读取成功后显示，不用空列表伪装完成。
          </div>
        ) : (
          <div className="grid min-w-0 grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4">
            {courses.map((course) => (
              <CourseCard key={courseKey(course.course_id, course.class_id)} course={course} />
            ))}
          </div>
        )}
      </section>

      <details className="group rounded-card border border-border bg-card">
        <summary className="flex cursor-pointer list-none items-center justify-between gap-2 px-4 py-3.5 text-[12px] font-medium text-muted-foreground transition-colors hover:text-foreground sm:px-5">
          <span>运行诊断与原始数据</span>
          <span className="text-[10px] font-normal text-muted-foreground">出口、watcher、连接检查与能力边界</span>
        </summary>
        <div className="grid gap-4 border-t border-border p-4 lg:grid-cols-2 sm:p-5">
          <div className="min-w-0">
            <div className="mb-2 flex items-center gap-2 text-[12px] font-medium">
              <Wifi className="h-3.5 w-3.5 text-muted-foreground" />
              出口健康
            </div>
            {egress.length === 0 ? (
              <p className="text-[11px] text-muted-foreground">当前没有配置代理路径，使用直连出口。</p>
            ) : (
              <Table label="出口健康" className="min-w-[520px]">
                <TableHeader>
                  <TableRow>
                    <TableHead>Endpoint</TableHead>
                    <TableHead>类型</TableHead>
                    <TableHead>失败</TableHead>
                    <TableHead>状态</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {egress.map((item) => (
                    <TableRow key={item.key}>
                      <TableCell className="font-mono text-xs">{item.key}</TableCell>
                      <TableCell>{item.kind}</TableCell>
                      <TableCell>{item.failures}</TableCell>
                      <TableCell>
                        <Badge variant={item.cooldown ? "destructive" : "success"}>{item.cooldown ? "冷却" : "健康"}</Badge>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
          <div className="min-w-0">
            <div className="mb-2 flex items-center gap-2 text-[12px] font-medium">
              <HeartPulse className="h-3.5 w-3.5 text-muted-foreground" />
              Watcher 健康
            </div>
            {watcherRows.length === 0 ? (
              <p className="text-[11px] text-muted-foreground">当前配置没有活跃 watcher。</p>
            ) : (
              <Table label="Watcher 健康" className="min-w-[620px]">
                <TableHeader>
                  <TableRow>
                    <TableHead>账号</TableHead>
                    <TableHead>最近 Tick</TableHead>
                    <TableHead>连续 panic</TableHead>
                    <TableHead>课堂轮询</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {watcherRows.map((item) => (
                    <TableRow key={String(item.account)}>
                      <TableCell className="font-mono text-xs">{String(item.account)}</TableCell>
                      <TableCell className="text-xs">
                        <span className="inline-flex items-center gap-2">
                          {item.last_tick_at ? formatBeijing(item.last_tick_at) : "—"}
                          {isStale(item) && <Badge variant="destructive">停滞</Badge>}
                        </span>
                      </TableCell>
                      <TableCell>
                        <Badge variant={Number(item.consecutive_panics) > 0 ? "destructive" : "success"}>{Number(item.consecutive_panics) || 0}</Badge>
                      </TableCell>
                      <TableCell>{Number(item.active_class_polls) || 0}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
          <div className="min-w-0">
            <div className="mb-2 flex items-center gap-2 text-[12px] font-medium">
              <Wifi className="h-3.5 w-3.5 text-muted-foreground" />
              连接检查
            </div>
            {connection ? <><MetaSummary data={connection} /><JsonBlock value={connection} /></> : <p className="text-[11px] text-muted-foreground">尚未读取连接状态。</p>}
          </div>
          <div className="min-w-0">
            <div className="mb-2 flex items-center gap-2 text-[12px] font-medium">
              <Database className="h-3.5 w-3.5 text-muted-foreground" />
              能力边界
            </div>
            {capabilities ? <JsonBlock value={capabilities} /> : <p className="text-[11px] text-muted-foreground">尚未读取能力契约。</p>}
          </div>
        </div>
      </details>
    </div>
  );
}

function errorMessage(value: unknown) {
  return value instanceof Error ? value.message : String(value);
}
