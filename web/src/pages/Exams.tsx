import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { EmptyState, ErrorBanner, Loading, MetaSummary, PageHeader } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export function Exams() {
  const [data, setData] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try { setData((await api.call("list_exams")).data); }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  React.useEffect(() => { void load(); }, []);
  const rows: any[] = data?.exams ?? [];

  return (
    <div className="space-y-5">
      <PageHeader
        title="考试安排"
        description="只读取考试列表，不进入考试、不做自动作答。"
        meta={<><Badge variant="secondary">{rows.length} 场考试</Badge><span>只读</span></>}
        actions={<Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>刷新</Button>}
      />
      <ErrorBanner error={error} />
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3 space-y-0">
          <div><CardTitle>考试清单</CardTitle><CardDescription>提交状态和可用状态各自独立，不根据标题猜测考试是否开放。</CardDescription></div>
          {data && <MetaSummary data={data} />}
        </CardHeader>
        <CardContent>
          {busy && !data ? <Loading /> : rows.length === 0 ? <EmptyState title="没有考试数据" /> : (
            <Table label="考试清单">
              <TableHeader><TableRow><TableHead>考试</TableHead><TableHead>课程</TableHead><TableHead>提交状态</TableHead><TableHead>可用状态</TableHead><TableHead>原始状态</TableHead></TableRow></TableHeader>
              <TableBody>
                {rows.map((row, index) => (
                  <TableRow key={index}>
                    <TableCell className="font-medium">{row.title}</TableCell>
                    <TableCell>{row.course}</TableCell>
                    <TableCell><Badge variant={row.submission_state === "pending" ? "warning" : "secondary"}>{row.submission_state}</Badge></TableCell>
                    <TableCell><Badge variant={row.availability_state === "open" ? "success" : "secondary"}>{row.availability_state}</Badge></TableCell>
                    <TableCell className="text-xs">{row.raw_status || "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
