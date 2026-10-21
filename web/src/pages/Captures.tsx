import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { EmptyState, ErrorBanner, JsonBlock, Loading } from "../components/common";
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
      <div className="flex items-center justify-between">
        <div className="text-sm text-muted-foreground">生产捕获只保存脱敏结构，用于逆向随堂练习提交协议。</div>
        <Button size="sm" onClick={() => void load()} disabled={busy}>刷新捕获</Button>
      </div>
      <ErrorBanner error={error} />
      <Card>
        <CardHeader><CardTitle>活动捕获</CardTitle><CardDescription>默认保留 7 天；page evidence 不包含 hidden value、cookie 或原始 HTML。</CardDescription></CardHeader>
        <CardContent>
          {busy && rows.length === 0 ? <Loading /> : rows.length === 0 ? <EmptyState title="暂无捕获" description="开启 watch.capture 后等待课堂事件。" /> : (
            <Table>
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
                    <TableCell><Button size="sm" variant="outline" onClick={() => void view(row)} disabled={busy}>查看</Button></TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
      {selected && (
        <Card>
          <CardHeader><CardTitle>捕获详情</CardTitle><CardDescription>activity_json 是脱敏投影，可直接用于协议分析。</CardDescription></CardHeader>
          <CardContent><JsonBlock value={selected} /></CardContent>
        </Card>
      )}
    </div>
  );
}
