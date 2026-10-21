import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { EmptyState, ErrorBanner, JsonBlock, Loading, PageHeader } from "../components/common";
import { formatBeijing } from "../lib/time";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export function Captures() {
  const [rows, setRows] = React.useState<any[]>([]);
  const [selected, setSelected] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try { setRows((await api.captures(50)).items); }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  async function view(row: any) {
    setBusy(true);
    setError("");
    try {
      const result = await api.captures(1, { includePage: true, account: row.account, activityKey: row.activity_key });
      setSelected(result.items[0] ?? row);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  React.useEffect(() => { void load(); }, []);

  return (
    <div className="space-y-5">
      <PageHeader
        title="生产捕获"
        meta={<span>保留 7 天</span>}
        actions={<Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>刷新捕获</Button>}
      />
      <ErrorBanner error={error} />
      <Card>
        <CardHeader><CardTitle>活动捕获</CardTitle></CardHeader>
        <CardContent>
          {busy && rows.length === 0 ? <Loading /> : rows.length === 0 ? <EmptyState title="暂无捕获" /> : (
            <>
            <div className="space-y-3 lg:hidden">
              {rows.map((row, index) => (
                <article key={`${row.activity_key}-${index}`} className="rounded-card border border-border bg-card p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="truncate font-mono text-[11px] text-muted-foreground">{row.account} · {row.course_id}</div>
                      <h2 className="mt-1 break-all font-mono text-[13px] font-medium">{row.activity_id}</h2>
                    </div>
                    <Badge>{row.kind}</Badge>
                  </div>
                  <div className="mt-3 flex flex-wrap gap-1.5">
                    <Badge variant={row.state === "active" ? "warning" : "secondary"}>{row.state}</Badge>
                    <Badge variant={row.has_page ? "success" : "secondary"}>{row.has_page ? "已捕获页面" : "无页面证据"}</Badge>
                  </div>
                  <div className="mt-2 text-[11px] text-muted-foreground">{formatBeijing(row.last_seen_at)}</div>
                  <Button size="sm" variant="outline" className="mt-3 w-full" onClick={() => void view(row)} disabled={busy}>查看捕获</Button>
                </article>
              ))}
            </div>
            <div className="hidden lg:block">
            <Table label="生产捕获">
              <TableHeader><TableRow><TableHead>账号</TableHead><TableHead>课程</TableHead><TableHead>活动</TableHead><TableHead>类型</TableHead><TableHead>状态</TableHead><TableHead>页面</TableHead><TableHead>最近出现</TableHead><TableHead /></TableRow></TableHeader>
              <TableBody>
                {rows.map((row, index) => (
                  <TableRow key={`${row.activity_key}-${index}`}>
                    <TableCell className="font-mono text-xs">{row.account}</TableCell>
                    <TableCell className="font-mono text-xs">{row.course_id}</TableCell>
                    <TableCell className="font-mono text-xs">{row.activity_id}</TableCell>
                    <TableCell><Badge>{row.kind}</Badge></TableCell>
                    <TableCell><Badge variant={row.state === "active" ? "warning" : "secondary"}>{row.state}</Badge></TableCell>
                    <TableCell><Badge variant={row.has_page ? "success" : "secondary"}>{row.has_page ? "已捕获" : "未捕获"}</Badge></TableCell>
                    <TableCell>{formatBeijing(row.last_seen_at)}</TableCell>
                    <TableCell className="sticky right-0 z-10 border-l border-border bg-card lg:static lg:border-l-0 lg:bg-transparent"><Button size="sm" variant="outline" onClick={() => void view(row)} disabled={busy}>查看</Button></TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            </div>
            </>
          )}
        </CardContent>
      </Card>
      {selected && (
        <Card>
          <CardHeader><CardTitle>捕获详情</CardTitle></CardHeader>
          <CardContent><JsonBlock value={selected} /></CardContent>
        </Card>
      )}
    </div>
  );
}
