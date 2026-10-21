import * as React from "react";
import { div as MotionDiv } from "motion/react-m";
import { AlertTriangle, CheckCircle2, Database, Inbox, Loader2 } from "lucide-react";
import { Badge } from "./ui/badge";
import { Card, CardContent, CardHeader } from "./ui/card";
import { Button } from "./ui/button";
import { Skeleton } from "./ui/skeleton";
import { cn } from "../lib/utils";
import { useReducedMotion } from "../lib/hooks";
import { formatBeijing } from "../lib/time";

export function ErrorBanner({ error }: { error: unknown }) {
  const reduced = useReducedMotion();
  if (!error) return null;
  const message = error instanceof Error ? error.message : String(error);
  return (
    <MotionDiv
      role="alert"
      initial={reduced ? false : { opacity: 0, y: -6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.16 }}
      className="flex items-start gap-3 rounded-2xl border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive backdrop-blur"
    >
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
      <div className="min-w-0">
        <div className="font-semibold">请求失败</div>
        <div className="break-all opacity-90">{message}</div>
      </div>
    </MotionDiv>
  );
}

type ErrorBoundaryProps = {
  children: React.ReactNode;
  resetKey?: string | number;
};

type ErrorBoundaryState = {
  error: Error | null;
};

export class PageErrorBoundary extends React.Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: unknown): ErrorBoundaryState {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  componentDidUpdate(previous: ErrorBoundaryProps) {
    if (previous.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null });
    }
  }

  render() {
    if (this.state.error) {
      return (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardHeader>
            <div className="flex items-center gap-2 text-destructive">
              <AlertTriangle className="h-4 w-4" />
              <span className="font-semibold">页面加载失败</span>
            </div>
            <div className="text-sm text-muted-foreground">
              当前页面渲染异常，管理服务本身可能仍然正常。可以重试，或切换到其他页面后返回。
            </div>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="break-all rounded-xl border border-destructive/30 bg-background/60 p-3 font-mono text-xs text-destructive">
              {this.state.error.message || "unknown render error"}
            </div>
            <Button size="sm" onClick={() => this.setState({ error: null })}>重试当前页面</Button>
          </CardContent>
        </Card>
      );
    }
    return this.props.children;
  }
}

export function EmptyState({ title, description }: { title: string; description?: string }) {
  return (
    <div className="rounded-2xl border border-dashed border-border/80 bg-card/40 p-8 text-center">
      <div className="mx-auto mb-3 flex h-11 w-11 items-center justify-center rounded-xl bg-primary/10 text-primary">
        <Inbox className="h-5 w-5" />
      </div>
      <div className="text-sm font-medium">{title}</div>
      {description && <div className="mx-auto mt-1 max-w-md text-xs leading-relaxed text-muted-foreground">{description}</div>}
    </div>
  );
}

export function Loading({ label = "加载中" }: { label?: string }) {
  return (
    <div role="status" aria-live="polite" className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
      <Loader2 className="h-4 w-4 animate-spin" />
      {label}
    </div>
  );
}

export function PageSkeleton() {
  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {[0, 1, 2, 3].map((item) => (
          <Skeleton key={item} className="h-32 rounded-2xl" />
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        {[0, 1].map((item) => (
          <Card key={item} className="bg-card/50">
            <CardHeader className="pb-3">
              <Skeleton className="h-5 w-40" />
              <Skeleton className="h-3 w-24" />
            </CardHeader>
            <CardContent>
              <Skeleton className="h-24 w-full" />
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}

export function MetaSummary({ data }: { data: any }) {
  const meta = data?.meta;
  if (!meta) return null;
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <Badge variant={meta.status === "complete" ? "success" : "warning"}>
        {meta.status === "complete" ? <CheckCircle2 className="mr-1 h-3 w-3" /> : null}
        {meta.status === "complete" ? "完整覆盖" : "部分覆盖"}
      </Badge>
      <span>范围：{meta.coverage?.scope ?? "unknown"}</span>
      {meta.freshness?.fetched_at && <span>抓取：{formatBeijing(meta.freshness.fetched_at)}（北京时间）</span>}
      {meta.trace_id && <span>Trace：{meta.trace_id}</span>}
    </div>
  );
}

export function JsonBlock({ value, className }: { value: any; className?: string }) {
  return (
    <div className={cn("overflow-hidden rounded-2xl border border-border/70 bg-muted/35", className)}>
      <div className="flex items-center gap-2 border-b border-border/70 px-4 py-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        <Database className="h-3.5 w-3.5" />
        JSON
      </div>
      <pre className="max-h-80 overflow-auto p-4 text-xs leading-relaxed">{JSON.stringify(value, null, 2)}</pre>
    </div>
  );
}

export function statusVariant(value: string) {
  if (["active", "open", "pending", "unsigned"].includes(value)) return "warning" as const;
  if (["signed", "submitted", "complete", "completed"].includes(value)) return "success" as const;
  if (["failed", "error", "closed"].includes(value)) return "destructive" as const;
  return "secondary" as const;
}
