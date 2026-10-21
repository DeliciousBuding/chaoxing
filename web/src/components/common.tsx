import * as React from "react";
import { div as MotionDiv } from "motion/react-m";
import { AlertTriangle, CheckCircle2, Database, Inbox, Loader2 } from "lucide-react";
import { Badge } from "./ui/badge";
import { Card, CardContent, CardHeader } from "./ui/card";
import { Button } from "./ui/button";
import { Skeleton } from "./ui/skeleton";
import { Alert, AlertDescription, AlertTitle } from "./ui/alert";
import { cn } from "../lib/utils";
import { useReducedMotion } from "../lib/hooks";
import { formatBeijing } from "../lib/time";

export function ErrorBanner({ error }: { error: unknown }) {
  const reduced = useReducedMotion();
  if (!error) return null;
  const message = error instanceof Error ? error.message : String(error);
  return (
    <MotionDiv
      initial={reduced ? false : { opacity: 0, y: -6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.16 }}
    >
      <Alert variant="destructive">
        <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
        <div className="min-w-0">
          <AlertTitle>请求失败</AlertTitle>
          <AlertDescription className="break-all">{message}</AlertDescription>
        </div>
      </Alert>
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
            <div className="break-all rounded-control border border-destructive/30 bg-background/60 p-3 font-mono text-xs text-destructive">
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

export function EmptyState({ title, description, bare = false }: { title: string; description?: string; bare?: boolean }) {
  return (
    <div className={cn("text-center", bare ? "py-8" : "rounded-card border border-border bg-card p-8")}>
      <div className="mx-auto mb-3 flex h-8 w-8 items-center justify-center rounded-control border border-border bg-muted text-muted-foreground">
        <Inbox className="h-4 w-4" />
      </div>
      <div className="text-[13px] font-medium">{title}</div>
      {description && <div className="mx-auto mt-1 max-w-md text-xs leading-relaxed text-muted-foreground">{description}</div>}
    </div>
  );
}

export function PageHeader({
  title,
  description,
  actions,
  meta
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  actions?: React.ReactNode;
  meta?: React.ReactNode;
}) {
  return (
    <header className={cn("border-b border-border pb-4", !actions && !description && !meta && "hidden sm:block")}>
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
        <h1 className="sr-only text-[19px] font-medium leading-6 tracking-[-0.025em] sm:not-sr-only">{title}</h1>
        {actions && <div className="ml-auto flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {description && <p className="mt-1.5 max-w-3xl text-[12px] leading-5 text-muted-foreground">{description}</p>}
      {meta && <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">{meta}</div>}
    </header>
  );
}

export function Toolbar({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-wrap items-center gap-2 rounded-control border border-border bg-card px-2.5 py-2", className)}>
      {children}
    </div>
  );
}

export function Loading({ label = "加载中" }: { label?: string }) {
  return (
    <div role="status" aria-live="polite" className="flex items-center gap-2 p-6 text-[13px] text-muted-foreground">
      <Loader2 className="h-4 w-4 animate-spin" />
      {label}
    </div>
  );
}

export function PageSkeleton() {
  return (
    <div className="space-y-4">
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        {[0, 1, 2, 3].map((item) => (
          <Skeleton key={item} className="h-24 rounded-card" />
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        {[0, 1].map((item) => (
          <Card key={item} className="bg-card">
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

export function MetaSummary({ data, compact = false }: { data: any; compact?: boolean }) {
  const meta = data?.meta;
  if (!meta) return null;
  if (meta.status === "complete") return null;
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
      <span className={cn("inline-flex items-center gap-1", meta.status === "complete" ? "text-success" : "text-warning")}>
        {meta.status === "complete" && <CheckCircle2 className="h-3 w-3" />}
        {meta.status === "complete" ? "完整覆盖" : "部分覆盖"}
      </span>
      {meta.freshness?.fetched_at && (
        <>
          <span aria-hidden="true">·</span>
          <span>{formatBeijing(meta.freshness.fetched_at)}</span>
        </>
      )}
      {!compact && meta.coverage?.scope && (
        <>
          <span aria-hidden="true">·</span>
          <span>范围 {meta.coverage.scope}</span>
        </>
      )}
    </div>
  );
}

export function JsonBlock({ value, className }: { value: any; className?: string }) {
  return (
    <div className={cn("min-w-0 max-w-full overflow-hidden rounded-control border border-border bg-muted/30", className)}>
      <div className="flex items-center gap-2 border-b border-border px-4 py-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
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
