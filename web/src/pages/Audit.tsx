import * as React from "react";
import { ChevronLeft, ChevronRight, RefreshCw, ScrollText } from "lucide-react";
import { api, type AuditEvent } from "../lib/api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { EmptyState, ErrorBanner, Loading } from "../components/common";
import { formatBeijing } from "../lib/time";

const categories = [
  ["", "全部类别"],
  ["auth", "认证"],
  ["member", "成员"],
  ["account", "账号"],
  ["tool", "工具"],
  ["security", "安全"],
  ["config", "配置"]
] as const;

function resultVariant(success: boolean) {
  return success ? "success" as const : "destructive" as const;
}

export function Audit() {
  const [items, setItems] = React.useState<AuditEvent[]>([]);
  const [total, setTotal] = React.useState(0);
  const [offset, setOffset] = React.useState(0);
  const [category, setCategory] = React.useState("");
  const [actor, setActor] = React.useState("");
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const limit = 50;

  const load = React.useCallback(async (nextOffset = offset) => {
    setLoading(true);
    setError("");
    try {
      const result = await api.audit({ limit, offset: nextOffset, category: category || undefined, actor: actor.trim() || undefined });
      setItems(result.items);
      setTotal(result.total);
      setOffset(nextOffset);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [actor, category, offset]);

  React.useEffect(() => { void load(0); }, [category]); // eslint-disable-line react-hooks/exhaustive-deps

  function applyFilters(event: React.FormEvent) {
    event.preventDefault();
    void load(0);
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 className="text-base font-semibold">控制面审计</h2>
          <p className="text-xs text-muted-foreground">登录、成员、权限、账号切换和工具调用轨迹；不保存密码、token、题目正文或页面原文。</p>
        </div>
        <form className="flex flex-wrap items-end gap-2" onSubmit={applyFilters}>
          <div className="space-y-1">
            <Label htmlFor="audit-category" className="text-[11px]">类别</Label>
            <Select id="audit-category" className="h-9 w-32" value={category} onChange={(event) => setCategory(event.target.value)}>
              {categories.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </Select>
          </div>
          <div className="space-y-1">
            <Label htmlFor="audit-actor" className="text-[11px]">操作者</Label>
            <Input id="audit-actor" className="h-9 w-36" value={actor} onChange={(event) => setActor(event.target.value)} placeholder="username" maxLength={32} />
          </div>
          <Button size="sm" type="submit" variant="outline" disabled={loading}><RefreshCw className="h-4 w-4" /> 查询</Button>
        </form>
      </div>
      <ErrorBanner error={error} />

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><ScrollText className="h-4 w-4 text-primary" /> 最近事件</CardTitle>
          <CardDescription>共 {total} 条；单页最多 {limit} 条。签到坐标与出口 IP 请查看对应账号的签到审计。</CardDescription>
        </CardHeader>
        <CardContent>
          {loading ? <Loading label="正在读取审计日志" /> : items.length === 0 ? (
            <EmptyState title="没有匹配的审计事件" description="调整类别或操作者过滤条件后重试。" />
          ) : (
            <div className="overflow-x-auto">
              <Table>
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
                        <div className="text-[11px] text-muted-foreground">{item.actor_role || "anonymous"}</div>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-1.5">
                          <Badge variant="secondary">{item.category}</Badge>
                          <span className="font-mono text-[11px]">{item.action}</span>
                        </div>
                        {item.details && Object.keys(item.details).length > 0 && (
                          <details className="mt-1">
                            <summary className="cursor-pointer text-[11px] text-muted-foreground">详情</summary>
                            <pre className="mt-1 max-w-72 overflow-auto rounded-lg bg-muted/45 p-2 text-[10px] leading-relaxed">{JSON.stringify(item.details, null, 2)}</pre>
                          </details>
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{item.account_key || "—"}</TableCell>
                      <TableCell className="max-w-44 truncate text-xs" title={item.target}>{item.target || "—"}</TableCell>
                      <TableCell><Badge variant={resultVariant(item.success)}>{item.success ? "成功" : "拒绝/失败"}</Badge></TableCell>
                      <TableCell>
                        <div className="font-mono text-[11px]">{item.ip || "—"}</div>
                        {item.request_id && <div className="max-w-28 truncate font-mono text-[10px] text-muted-foreground" title={item.request_id}>{item.request_id}</div>}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          <div className="mt-4 flex items-center justify-between gap-2">
            <div className="text-xs text-muted-foreground">偏移 {offset} / {total}</div>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" disabled={loading || offset === 0} onClick={() => void load(Math.max(0, offset - limit))}>
                <ChevronLeft className="h-4 w-4" /> 上一页
              </Button>
              <Button size="sm" variant="outline" disabled={loading || offset + limit >= total} onClick={() => void load(offset + limit)}>
                下一页 <ChevronRight className="h-4 w-4" />
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
