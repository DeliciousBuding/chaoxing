import * as React from "react";
import {
  Activity,
  Database,
  Globe2,
  GripVertical,
  HeartPulse,
  ServerCog,
  ShieldCheck
} from "lucide-react";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent
} from "@dnd-kit/core";
import { SortableContext, arrayMove, rectSortingStrategy, useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { api, type SignAudit, type ToolDefinition } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { ErrorBanner, JsonBlock, Loading, MetaSummary } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { useUIStore, type DashboardCardId } from "../lib/store";
import { cn } from "../lib/utils";
import { formatBeijing } from "../lib/time";

type Metric = {
  id: DashboardCardId;
  title: string;
  description: string;
  icon: React.ComponentType<{ className?: string }>;
  value: string | number;
  tone: "primary" | "success" | "warning";
};

const MetricCard = React.memo(function MetricCard({ metric }: { metric: Metric }) {
  const { attributes, isDragging, listeners, setNodeRef, transform, transition } = useSortable({ id: metric.id });
  const Icon = metric.icon;
  const toneClass = {
    primary: "bg-primary/12 text-primary ring-primary/20",
    success: "bg-success/12 text-success ring-success/25",
    warning: "bg-warning/12 text-warning ring-warning/25"
  }[metric.tone];

  return (
    <div
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={cn(
        "group relative rounded-card border border-border/70 bg-card/85 shadow-lg shadow-black/5 backdrop-blur-xl transition-shadow",
        isDragging && "z-10 shadow-2xl ring-2 ring-primary/40"
      )}
    >
      <div className="flex flex-col p-5">
        <div className="flex items-start justify-between gap-3">
          <div className={cn("flex h-9 w-9 items-center justify-center rounded-xl ring-1", toneClass)}>
            <Icon className="h-4 w-4" />
          </div>
          <button
            type="button"
            className="cursor-grab touch-none rounded-lg p-1.5 text-muted-foreground opacity-0 transition focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring group-hover:opacity-100 active:cursor-grabbing"
            aria-label={`调整 ${metric.title} 顺序`}
            {...attributes}
            {...listeners}
          >
            <GripVertical className="h-4 w-4" />
          </button>
        </div>
        <div className="mt-4 text-2xl font-semibold tracking-tight">{metric.value}</div>
        <div className="text-sm font-medium">{metric.title}</div>
        <div className="mt-1 text-xs leading-relaxed text-muted-foreground">{metric.description}</div>
      </div>
    </div>
  );
});

export function Dashboard({ tools }: { tools: ToolDefinition[] }) {
  const [status, setStatus] = React.useState<any>(null);
  const [connection, setConnection] = React.useState<any>(null);
  const [capabilities, setCapabilities] = React.useState<any>(null);
  const [signAudits, setSignAudits] = React.useState<SignAudit[]>([]);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [auditsLoading, setAuditsLoading] = React.useState(true);
  const [auditsError, setAuditsError] = React.useState("");
  const requestEpoch = React.useRef(0);
  const dashboardOrder = useUIStore((state) => state.dashboardOrder);
  const setDashboardOrder = useUIStore((state) => state.setDashboardOrder);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor)
  );

  const load = React.useCallback(async () => {
    const epoch = ++requestEpoch.current;
    setBusy(true);
    setError("");
    setAuditsLoading(true);
    setAuditsError("");
    const [statusResult, connectionResult, capabilitiesResult, auditsResult] = await Promise.allSettled([
      api.status(),
      api.call("get_connection_status"),
      api.call("get_capabilities"),
      api.signAudits(20)
    ]);
    if (epoch !== requestEpoch.current) return;
    const failures: string[] = [];
    if (statusResult.status === "fulfilled") setStatus(statusResult.value);
    else failures.push(`状态：${errorMessage(statusResult.reason)}`);
    if (connectionResult.status === "fulfilled") setConnection(connectionResult.value.data);
    else failures.push(`连接：${errorMessage(connectionResult.reason)}`);
    if (capabilitiesResult.status === "fulfilled") setCapabilities(capabilitiesResult.value.data);
    else failures.push(`能力：${errorMessage(capabilitiesResult.reason)}`);
    if (auditsResult.status === "fulfilled") {
      setSignAudits(auditsResult.value.items ?? []);
    } else {
      setSignAudits([]);
      setAuditsError(errorMessage(auditsResult.reason));
    }
    setError(failures.join("；"));
    setAuditsLoading(false);
    setBusy(false);
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const runtime = status?.runtime ?? {};
  const egress: any[] = runtime.egress ?? [];
  const accounts: any[] = status?.accounts ?? [];
  const capabilityCount = Array.isArray(capabilities?.items) ? capabilities.items.length : null;
  const serverNow = runtime.now ? new Date(runtime.now).getTime() : Date.now();
  const STALE_MS = 20 * 60 * 1000;
  function isStale(row: Record<string, any>) {
    const reference = row.last_tick_at || row.started_at;
    if (!reference) return false;
    const at = new Date(reference).getTime();
    return Number.isFinite(at) && serverNow - at > STALE_MS;
  }
  const watcherRows: Array<Record<string, any> & { account: string }> =
    Object.entries(runtime.watchers ?? {}).map(([account, value]) => ({
      account,
      ...(value as Record<string, any>)
    }));
  const watcherPanics = watcherRows.reduce((sum, item) => sum + (Number(item.consecutive_panics) || 0), 0);
  const watchersStale = watcherRows.some((item) => isStale(item));
  const process: Record<string, any> = runtime.process ?? {};
  const processCrashes = Number(process.consecutive_crashes) || 0;
  const processDegraded = Boolean(process.degraded);
  const statusPending = busy && !status;
  const statusUnknown = !status;

  const metrics = React.useMemo<Record<DashboardCardId, Metric>>(() => ({
    security: {
      id: "security", title: "安全会话", description: "HttpOnly Cookie + Strict SameSite + CSRF 双提交", icon: ShieldCheck,
      value: statusPending ? "读取中" : statusUnknown ? "未确认" : "Active",
      tone: statusPending || statusUnknown ? "warning" : "success"
    },
    egress: {
      id: "egress", title: "出口路径", description: "账号粘性与健康冷却已启用", icon: Globe2,
      value: statusPending ? "…" : statusUnknown ? "—" : egress.length, tone: "primary"
    },
    upstream: {
      id: "upstream", title: "上游连接", description: "每次均为新鲜读取，不做缓存伪造", icon: Activity,
      value: connection?.meta?.status === "complete" ? "正常" : "待确认", tone: connection?.meta?.status === "complete" ? "success" : "warning"
    },
    watchers: {
      id: "watchers", title: "Watcher 监督",
      description: watchersStale
        ? `存在停滞 watcher；当前连续 panic ${watcherPanics} 次`
        : `panic 已隔离并可观测；当前连续 panic ${watcherPanics} 次`,
      icon: HeartPulse,
      value: watcherRows.length, tone: watcherPanics > 0 || watchersStale ? "warning" : "success"
    },
    supervisor: {
      id: "supervisor", title: "进程监督",
      description: statusPending || statusUnknown
        ? "等待进程监督状态"
        : processDegraded
        ? "状态文件写入降级；计数仅保存在内存"
        : `procd 异常退出计数已持久化；当前连续 ${processCrashes} 次`,
      icon: ServerCog,
      value: statusPending ? "读取中" : statusUnknown ? "未确认" : processCrashes > 0 ? processCrashes : "稳定",
      tone: statusPending || statusUnknown || processDegraded || processCrashes > 0 ? "warning" : "success"
    },
    capabilities: {
      id: "capabilities", title: "能力域", description: "以 capabilities 契约为准，不猜测接口", icon: Database,
      value: capabilityCount ?? "—", tone: "primary"
    }
  }), [capabilityCount, connection?.meta?.status, egress.length, processCrashes, processDegraded, statusPending, statusUnknown, watcherPanics, watchersStale]);

  const order = React.useMemo(() => {
    const valid = dashboardOrder.filter((id): id is DashboardCardId => id in metrics);
    const missing = (Object.keys(metrics) as DashboardCardId[]).filter((id) => !valid.includes(id));
    return [...valid, ...missing];
  }, [dashboardOrder, metrics]);

  function onDragEnd(event: DragEndEvent) {
    const active = event.active?.id;
    const over = event.over?.id;
    if (!active || !over || active === over) return;
    const oldIndex = order.indexOf(active as DashboardCardId);
    const newIndex = order.indexOf(over as DashboardCardId);
    if (oldIndex < 0 || newIndex < 0) return;
    setDashboardOrder(arrayMove(order, oldIndex, newIndex));
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap gap-2">
          <Badge variant="success">Web UI 已启用</Badge>
          <Badge>{tools.length} 个受控工具</Badge>
          <Badge variant="secondary">{accounts.length} 个租户上下文</Badge>
        </div>
        <Button size="sm" onClick={() => void load()} disabled={busy}>刷新状态</Button>
      </div>
      <ErrorBanner error={error} />

      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
        <SortableContext items={order} strategy={rectSortingStrategy}>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            {order.map((id) => <MetricCard key={id} metric={metrics[id]} />)}
          </div>
        </SortableContext>
      </DndContext>

      <Card>
        <CardHeader>
          <CardTitle>出口健康</CardTitle>
          <CardDescription>仅显示脱敏 endpoint key，不泄露代理 URL 或凭据。</CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Endpoint</TableHead>
                <TableHead>类型</TableHead>
                <TableHead>失败</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>持久化</TableHead>
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
                  <TableCell>
                    <Badge variant={item.persistence_degraded || item.assignment_degraded ? "warning" : "secondary"}>
                      {item.persistence_degraded || item.assignment_degraded ? "降级" : "正常"}
                    </Badge>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Watcher 健康</CardTitle>
          <CardDescription>只显示时间与计数，不暴露课程身份或上游响应体。</CardDescription>
        </CardHeader>
        <CardContent>
          {watcherRows.length === 0 ? (
            <p className="text-sm text-muted-foreground">当前配置没有活跃 watcher。</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>账号</TableHead>
                  <TableHead>最近 Tick</TableHead>
                  <TableHead>连续 panic</TableHead>
                  <TableHead>最近 panic 类型</TableHead>
                  <TableHead>课堂轮询</TableHead>
                  <TableHead>总 Tick</TableHead>
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
                    <TableCell className="text-xs">{item.last_panic_kind || "—"}</TableCell>
                    <TableCell>{Number(item.active_class_polls) || 0}</TableCell>
                    <TableCell>{Number(item.ticks) || 0}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>签到审计</CardTitle>
          <CardDescription>最近 20 次签到尝试的出口 IP、签到地址与结果；仅当前账号可见。</CardDescription>
        </CardHeader>
        <CardContent>
          {auditsLoading ? (
            <Loading label="正在读取签到审计" />
          ) : auditsError ? (
            <div role="alert" className="space-y-3 rounded-xl border border-destructive/35 bg-destructive/5 p-3 text-sm text-destructive">
              <div className="break-all">签到审计读取失败：{auditsError}</div>
              <Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>重试</Button>
            </div>
          ) : signAudits.length === 0 ? (
            <p className="text-sm text-muted-foreground">暂无签到审计记录。</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>时间</TableHead>
                  <TableHead>活动</TableHead>
                  <TableHead>方式</TableHead>
                  <TableHead>结果</TableHead>
                  <TableHead>出口 IP</TableHead>
                  <TableHead>签到地址</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {signAudits.map((item, index) => (
                  <TableRow key={`${item.created_at}:${item.activity_id}:${index}`}>
                    <TableCell className="text-xs">{formatBeijing(item.created_at)}</TableCell>
                    <TableCell className="font-mono text-xs">{item.course_id} / {item.class_id}</TableCell>
                    <TableCell>{item.method || "—"}</TableCell>
                    <TableCell>
                      <Badge variant={item.verified ? "success" : item.state === "failed" ? "destructive" : "warning"}>
                        {item.verified ? "已确认" : item.code || item.state || "未知"}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{item.egress_ip || "未获取"}</TableCell>
                    <TableCell className="max-w-56 truncate text-xs" title={item.address || ""}>{item.address || "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>连接检查</CardTitle><CardDescription>get_connection_status</CardDescription></CardHeader>
          <CardContent className="space-y-3">
            {connection ? <><MetaSummary data={connection} /><JsonBlock value={connection} /></> : <Loading />}
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>能力边界</CardTitle><CardDescription>get_capabilities</CardDescription></CardHeader>
          <CardContent>{capabilities ? <JsonBlock value={capabilities} /> : <Loading />}</CardContent>
        </Card>
      </div>
    </div>
  );
}

function errorMessage(value: unknown) {
  return value instanceof Error ? value.message : String(value);
}
