import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { EmptyState, ErrorBanner, Loading, MetaSummary, PageHeader } from "../components/common";
import { formatBeijing } from "../lib/time";

export function Notices() {
  const [data, setData] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try { setData((await api.call("list_notices", { limit: 50, include_raw: false })).data); }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  React.useEffect(() => { void load(); }, []);
  const rows: any[] = data?.items ?? [];

  return (
    <div className="space-y-5">
      <PageHeader
        title="课程通知"
        description="通知正文按不可信数据处理，不会自动转成待办，也不会触发写入。"
        meta={<><Badge variant="secondary">{rows.length} 条</Badge><span>只读投影</span></>}
        actions={<Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>刷新</Button>}
      />
      <ErrorBanner error={error} />
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3 space-y-0">
          <div><CardTitle>通知流</CardTitle><CardDescription>按上游返回顺序展示，未读取的原始 HTML 不会进入浏览器。</CardDescription></div>
          {data && <MetaSummary data={data} />}
        </CardHeader>
        <CardContent>
          {busy && !data ? <Loading /> : rows.length === 0 ? <EmptyState title="没有通知" /> : (
            <div className="divide-y divide-border">
              {rows.map((row, index) => (
                <article key={row.id ?? index} className="py-4 first:pt-1 last:pb-1">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <h2 className="text-[13px] font-medium">{row.title || "无标题"}</h2>
                    <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                      <Badge variant="secondary">{row.source_type || "notice"}</Badge>
                      {row.send_time && <span>{formatBeijing(row.send_time)}</span>}
                    </div>
                  </div>
                  <div className="mt-2 whitespace-pre-wrap text-[12px] leading-5 text-muted-foreground">{row.content || "—"}</div>
                </article>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
