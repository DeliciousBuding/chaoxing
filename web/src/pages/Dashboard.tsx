import * as React from "react";
import {
  Activity,
  Database,
  Globe2,
  GripVertical,
  HeartPulse,
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
import { api, type ToolDefinition } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { ErrorBanner, JsonBlock, Loading, MetaSummary } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { useUIStore, type DashboardCardId } from "../lib/store";
import { cn } from "../lib/utils";

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
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const dashboardOrder = useUIStore((state) => state.dashboardOrder);
  const setDashboardOrder = useUIStore((state) => state.setDashboardOrder);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor)
  );

  const load = React.useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      const [nextStatus, nextConnection, nextCapabilities] = await Promise.all([
        api.status(),
        api.call("get_connection_status"),
        api.call("get_capabilities")
      ]);
      setStatus(nextStatus);
      setConnection(nextConnection.data);
      setCapabilities(nextCapabilities.data);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const runtime = status?.runtime ?? {};
  const egress: any[] = runtime.egress ?? [];
  const accounts: any[] = status?.accounts ?? [];
  const capabilityCount = Array.isArray(capabilities?.items) ? capabilities.items.length : null;
  const watcherRows: Array<Record<string, any> & { account: string }> =
    Object.entries(runtime.watchers ?? {}).map(([account, value]) => ({
      account,
      ...(value as Record<string, any>)
    }));
  const watcherPanics = watcherRows.reduce((sum, item) => sum + (Number(item.consecutive_panics) || 0), 0);

  const metrics = React.useMemo<Record<DashboardCardId, Metric>>(() => ({
    security: {
      id: "security", title: "安全会话", description: "HttpOnly Cookie + Strict SameSite + CSRF 双提交", icon: ShieldCheck,
      value: "Active", tone: "success"
    },
    egress: {
      id: "egress", title: "出口路径", description: "账号粘性与健康冷却已启用", icon: Globe2,
      value: egress.length, tone: "primary"
    },
    upstream: {
      id: "upstream", title: "上游连接", description: "每次均为新鲜读取，不做缓存伪造", icon: Activity,
      value: connection?.meta?.status === "complete" ? "正常" : "待确认", tone: connection?.meta?.status === "complete" ? "success" : "warning"
    },
    watchers: {
      id: "watchers", title: "Watcher 监督", description: `panic 已隔离并可观测；当前连续 panic ${watcherPanics} 次`, icon: HeartPulse,
      value: watcherRows.length, tone: watcherPanics > 0 ? "warning" : "success"
    },
    capabilities: {
      id: "capabilities", title: "能力域", description: "以 capabilities 契约为准，不猜测接口", icon: Database,
      value: capabilityCount ?? "—", tone: "primary"
    }
  }), [capabilityCount, connection?.meta?.status, egress.length]);

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
                    <TableCell className="text-xs">{item.last_tick_at ? new Date(item.last_tick_at).toLocaleString() : "—"}</TableCell>
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
