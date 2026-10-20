import * as React from "react";
import { AlertTriangle, CheckCircle2, Loader2 } from "lucide-react";
import { Badge } from "./ui/badge";
import { cn } from "../lib/utils";

export function ErrorBanner({ error }: { error: unknown }) {
  if (!error) return null;
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div className="flex items-start gap-3 rounded-xl border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive">
      <AlertTriangle className="mt-0.5 h-4 w-4" />
      <div className="min-w-0">
        <div className="font-semibold">请求失败</div>
        <div className="break-all opacity-90">{message}</div>
      </div>
    </div>
  );
}

export function EmptyState({ title, description }: { title: string; description?: string }) {
  return (
    <div className="rounded-xl border border-dashed p-8 text-center">
      <div className="text-sm font-medium">{title}</div>
      {description && <div className="mt-1 text-xs text-muted-foreground">{description}</div>}
    </div>
  );
}

export function Loading({ label = "加载中" }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
      <Loader2 className="h-4 w-4 animate-spin" />
      {label}
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
      {meta.freshness?.fetched_at && <span>抓取：{new Date(meta.freshness.fetched_at).toLocaleString()}</span>}
      {meta.trace_id && <span>Trace：{meta.trace_id}</span>}
    </div>
  );
}

export function JsonBlock({ value, className }: { value: any; className?: string }) {
  return (
    <pre className={cn("max-h-80 overflow-auto rounded-xl border bg-black/40 p-4 text-xs leading-relaxed", className)}>
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

export function statusVariant(value: string) {
  if (["active", "open", "pending", "unsigned"].includes(value)) return "warning" as const;
  if (["signed", "submitted", "complete", "completed"].includes(value)) return "success" as const;
  if (["failed", "error", "closed"].includes(value)) return "destructive" as const;
  return "secondary" as const;
}
