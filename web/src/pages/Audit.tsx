import * as React from "react";
import { ChevronLeft, ChevronRight, RefreshCw, ScrollText } from "lucide-react";
import { api, type AuditEvent, type Role } from "../lib/api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { EmptyState, ErrorBanner, Loading, PageHeader } from "../components/common";
import { formatBeijing } from "../lib/time";
import { roleLabels } from "../lib/roles";

const categories = [
  ["", "全部类别"],
  ["auth", "认证"],
  ["member", "成员"],
  ["account", "账号"],
  ["tool", "工具"],
  ["security", "安全"],
  ["config", "配置"]
] as const;

const initialFilters = { category: "", actor: "" } as const;

function resultVariant(success: boolean) {
  return success ? "success" as const : "destructive" as const;
}

function categoryLabel(value: string) {
  return categories.find(([code]) => code === value)?.[1] ?? value;
}

export function Audit() {
  const [items, setItems] = React.useState<AuditEvent[]>([]);
  const [total, setTotal] = React.useState(0);
  const [offset, setOffset] = React.useState(0);
  const [category, setCategory] = React.useState("");
  const [actor, setActor] = React.useState("");
  const [applied, setApplied] = React.useState<{ category: string; actor: string }>(initialFilters);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const requestSeq = React.useRef(0);
  const limit = 50;

  const load = React.useCallback(async (nextOffset: number, filters: { category: string; actor: string }) => {
    const seq = ++requestSeq.current;
    setLoading(true);
    setError("");
    try {
      const result = await api.audit({
        limit,
        offset: nextOffset,
        category: filters.category || undefined,
        actor: filters.actor || undefined
      });
      if (seq !== requestSeq.current) return;
      setItems(result.items);
      setTotal(result.total);
      setOffset(nextOffset);
    } catch (err) {
      if (seq === requestSeq.current) setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (seq === requestSeq.current) setLoading(false);
    }
  }, [limit]);

  React.useEffect(() => { void load(0, initialFilters); }, [load]);

  function applyFilters(event: React.FormEvent) {
    event.preventDefault();
    const next = { category, actor: actor.trim() };
    setApplied(next);
    void load(0, next);
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title="控制面审计"
        meta={<span>保留 180 天</span>}
        actions={
        <form className="flex flex-wrap items-end gap-2" onSubmit={applyFilters}>
          <div className="space-y-1">
            <Label htmlFor="audit-category" className="text-[11px]">类别</Label>
            <Select id="audit-category" className="w-[120px]" value={category} onChange={(event) => setCategory(event.target.value)}>
              {categories.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </Select>
          </div>
          <div className="space-y-1">
            <Label htmlFor="audit-actor" className="text-[11px]">操作者</Label>
            <Input id="audit-actor" className="w-[120px]" value={actor} onChange={(event) => setActor(event.target.value)} placeholder="username" maxLength={32} />
          </div>
          <Button size="sm" type="submit" variant="outline" disabled={loading}><RefreshCw className="h-4 w-4" /> <span className="hidden sm:inline">查询</span></Button>
        </form>
        }
      />
      <ErrorBanner error={error} />

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><ScrollText className="h-4 w-4 text-primary" /> 最近事件</CardTitle>
        </CardHeader>
        <CardContent>
          {loading ? <Loading label="正在读取审计日志" /> : error ? null : items.length === 0 ? (
            <EmptyState bare title="没有匹配的审计事件" />
          ) : (
            <>
            <div className="space-y-2 lg:hidden">
              {items.map((item) => (
                <article key={item.id} className="rounded-control border border-border bg-card p-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <Badge variant="secondary">{categoryLabel(item.category)}</Badge>
                        <span className="truncate font-mono text-[11px]">{item.action}</span>
                      </div>
                      <div className="mt-1 text-[11px] text-muted-foreground">
                        {formatBeijing(item.created_at)} · {item.actor_username || "—"}
                      </div>
                    </div>
                    <Badge variant={resultVariant(item.success)} className="shrink-0">
                      {item.success ? "成功" : "失败"}
                    </Badge>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
                    {item.account_key && <span>账号 {item.account_key}</span>}
                    {item.target && <span className="max-w-full truncate" title={item.target}>目标 {item.target}</span>}
                    {item.ip && <span className="font-mono">{item.ip}</span>}
                  </div>
                  {item.details && Object.keys(item.details).length > 0 && (
                    <details className="mt-2 border-t border-border pt-2">
                      <summary className="cursor-pointer text-[11px] text-muted-foreground">详情</summary>
                      <pre className="mt-1 max-h-52 overflow-auto rounded-control bg-muted/45 p-2 text-[11px] leading-relaxed">
                        {JSON.stringify(item.details, null, 2)}
                      </pre>
                    </details>
                  )}
                </article>
              ))}
            </div>
            <div className="hidden lg:block">
            <Table label="控制面审计" className="min-w-[760px]">
                <TableHeader>
                  <TableRow>
                    <TableHead>时间</TableHead>
                    <TableHead>操作者</TableHead>
                    <TableHead>类别 / 动作</TableHead>
                    <TableHead>账号</TableHead>
                    <TableHead>目标</TableHead>
                    <TableHead>结果</TableHead>
                    <TableHead>来源</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((item) => (
                    <TableRow key={item.id}>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatBeijing(item.created_at)}</TableCell>
                      <TableCell>
                        <div className="text-sm">{item.actor_username || "—"}</div>
                        <div className="text-[11px] text-muted-foreground">{roleLabels[item.actor_role as Role] ?? item.actor_role ?? "anonymous"}</div>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-1.5">
                          <Badge variant="secondary">{categoryLabel(item.category)}</Badge>
                          <span className="font-mono text-[11px]">{item.action}</span>
                        </div>
                        {item.details && Object.keys(item.details).length > 0 && (
                          <details className="mt-1">
                            <summary className="cursor-pointer text-[11px] text-muted-foreground">详情</summary>
                            <pre className="mt-1 max-w-72 overflow-auto rounded-control bg-muted/45 p-2 text-[11px] leading-relaxed">{JSON.stringify(item.details, null, 2)}</pre>
                          </details>
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{item.account_key || "—"}</TableCell>
                      <TableCell className="max-w-44 truncate text-xs" title={item.target}>{item.target || "—"}</TableCell>
                      <TableCell><Badge variant={resultVariant(item.success)}>{item.success ? "成功" : "拒绝/失败"}</Badge></TableCell>
                      <TableCell>
                        <div className="font-mono text-[11px]">{item.ip || "—"}</div>
                        {item.request_id && <div className="max-w-28 truncate font-mono text-[11px] text-muted-foreground" title={item.request_id}>{item.request_id}</div>}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
            </Table>
            </div>
            </>
          )}
          {!loading && !error && items.length > 0 && <div className="mt-4 flex items-center justify-between gap-2">
            <div className="text-xs text-muted-foreground">第 {Math.floor(offset / limit) + 1} 页</div>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" disabled={loading || offset === 0} onClick={() => void load(Math.max(0, offset - limit), applied)}>
                <ChevronLeft className="h-4 w-4" /> 上一页
              </Button>
              <Button size="sm" variant="outline" disabled={loading || offset + limit >= total} onClick={() => void load(offset + limit, applied)}>
                下一页 <ChevronRight className="h-4 w-4" />
              </Button>
            </div>
          </div>}
        </CardContent>
      </Card>
    </div>
  );
}
