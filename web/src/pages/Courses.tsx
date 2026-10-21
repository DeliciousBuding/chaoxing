import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { EmptyState, ErrorBanner, Loading, MetaSummary, PageHeader } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export function Courses() {
  const [data, setData] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try { setData((await api.call("list_courses")).data); }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  React.useEffect(() => { void load(); }, []);
  const rows: any[] = data?.result ?? [];

  return (
    <div className="space-y-5">
      <PageHeader
        title="课程与班级"
        description="所有后续工具都要求使用这里的精确 course_id / class_id / cpi。"
        meta={<><Badge variant="secondary">{rows.length} 条课程上下文</Badge><span>账号范围由右上角切换</span></>}
        actions={<Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>刷新</Button>}
      />
      <ErrorBanner error={error} />
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3 space-y-0">
          <div>
            <CardTitle>课程清单</CardTitle>
            <CardDescription>来自后端 list_courses；未知字段保持为空，不从前端推断。</CardDescription>
          </div>
          {data && <MetaSummary data={data} />}
        </CardHeader>
        <CardContent>
          {busy && !data ? <Loading /> : rows.length === 0 ? <EmptyState title="没有课程数据" description="检查上游会话或账号上下文。" /> : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>课程</TableHead><TableHead>教师</TableHead><TableHead>班级</TableHead>
                  <TableHead>course_id</TableHead><TableHead>class_id</TableHead><TableHead>cpi</TableHead><TableHead>状态</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row, index) => (
                  <TableRow key={`${row.course_id}-${row.class_id}-${index}`}>
                    <TableCell className="font-medium">{row.name}</TableCell>
                    <TableCell>{row.teacher || "—"}</TableCell>
                    <TableCell>{row.class || "—"}</TableCell>
                    <TableCell className="font-mono text-xs">{row.course_id}</TableCell>
                    <TableCell className="font-mono text-xs">{row.class_id}</TableCell>
                    <TableCell className="font-mono text-xs">{row.cpi}</TableCell>
                    <TableCell><Badge variant={row.retired ? "secondary" : "success"}>{row.retired ? "退课" : "在读"}</Badge></TableCell>
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
