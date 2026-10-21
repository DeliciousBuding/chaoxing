import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { EmptyState, ErrorBanner, Loading, MetaSummary } from "../components/common";
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
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <div><CardTitle>课程通知</CardTitle><CardDescription>通知正文按不可信数据处理，不会自动转成待办。</CardDescription></div>
        <Button size="sm" onClick={() => void load()} disabled={busy}>刷新</Button>
      </CardHeader>
      <CardContent className="space-y-4">
        <ErrorBanner error={error} />
        {data && <MetaSummary data={data} />}
        {busy && !data ? <Loading /> : rows.length === 0 ? <EmptyState title="没有通知" /> : (
          <div className="space-y-3">
            {rows.map((row, index) => (
              <div key={row.id ?? index} className="rounded-xl border bg-card/70 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="font-medium">{row.title || "无标题"}</div>
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <Badge variant="secondary">{row.source_type || "notice"}</Badge>
                    {row.send_time && <span>{formatBeijing(row.send_time)}</span>}
                  </div>
                </div>
                <div className="mt-2 whitespace-pre-wrap text-sm text-muted-foreground">{row.content || "—"}</div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
