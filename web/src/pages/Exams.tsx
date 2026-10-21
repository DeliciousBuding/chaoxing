import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { EmptyState, ErrorBanner, Loading, MetaSummary, PageHeader } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { labelFor } from "../lib/labels";

export function Exams() {
  const [data, setData] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try {
      setData((await api.call("list_exams")).data);
      setLoaded(true);
    }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  React.useEffect(() => { void load(); }, []);
  const rows: any[] = data?.exams ?? [];

  return (
    <div className="space-y-5">
      <PageHeader
        title="考试安排"
        actions={<Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>刷新</Button>}
      />
      <ErrorBanner error={error} />
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3 space-y-0">
          <CardTitle>考试清单</CardTitle>
          {data && <MetaSummary data={data} />}
        </CardHeader>
        <CardContent>
          {busy && !data ? <Loading /> : !loaded ? null : rows.length === 0 ? <EmptyState bare title="没有考试数据" /> : (
            <>
              <div className="space-y-2 lg:hidden">
                {rows.map((row, index) => (
                  <article key={index} className="rounded-control border border-border bg-muted/15 p-3">
                    <p className="text-sm font-medium leading-5">{row.title}</p>
                    <p className="mt-1 text-xs text-muted-foreground">{row.course}</p>
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      <Badge variant={row.submission_state === "pending" ? "warning" : "secondary"} title={row.submission_state}>
                        {labelFor("submission", row.submission_state)}
                      </Badge>
                      <Badge variant={row.availability_state === "open" ? "success" : "secondary"} title={row.availability_state}>
                        {labelFor("availability", row.availability_state)}
                      </Badge>
                    </div>
                  </article>
                ))}
              </div>
              <div className="hidden lg:block">
                <Table label="考试清单">
                  <TableHeader><TableRow><TableHead>考试</TableHead><TableHead>课程</TableHead><TableHead>提交状态</TableHead><TableHead>可用状态</TableHead></TableRow></TableHeader>
                  <TableBody>
                    {rows.map((row, index) => (
                      <TableRow key={index}>
                        <TableCell className="font-medium">{row.title}</TableCell>
                        <TableCell>{row.course}</TableCell>
                        <TableCell>
                          <Badge variant={row.submission_state === "pending" ? "warning" : "secondary"} title={row.submission_state}>
                            {labelFor("submission", row.submission_state)}
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <Badge variant={row.availability_state === "open" ? "success" : "secondary"} title={row.availability_state}>
                            {labelFor("availability", row.availability_state)}
                          </Badge>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
