import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { EmptyState, ErrorBanner, JsonBlock, Loading, MetaSummary, PageHeader } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { labelFor } from "../lib/labels";

export function Assignments() {
  const [actionItems, setActionItems] = React.useState<any>(null);
  const [assignments, setAssignments] = React.useState<any>(null);
  const [detail, setDetail] = React.useState<any>(null);
  const [drafts, setDrafts] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const [actions, all] = await Promise.all([
        api.call("list_action_items"),
        api.call("list_assignments", { status: "all", limit: 100 })
      ]);
      setActionItems(actions.data);
      setAssignments(all.data);
      setLoaded(true);
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  React.useEffect(() => { void load(); }, []);
  const actions: any[] = actionItems?.items ?? [];
  const rows: any[] = assignments?.items ?? [];

  async function loadQuestions(row: any) {
    setBusy(true);
    setError("");
    setDetail(null);
    setDrafts(null);
    try {
      const result = await api.call("get_assignment_questions", { id: row.id });
      setDetail(result.data);
      const job = result.data.job ?? {};
      if (job.course_id && job.class_id && result.data.job_id) {
        const draftResult = await api.call("list_work_drafts", {
          course_id: job.course_id,
          class_id: job.class_id,
          job_id: result.data.job_id
        });
        setDrafts(draftResult.data);
      }
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  async function generateDraft() {
    if (!detail?.job_id) return;
    const job = detail.job ?? {};
    if (!job.course_id || !job.class_id) {
      setError("作业题目结果缺少 course_id / class_id");
      return;
    }
    if (!window.confirm("使用配置的题库 / LLM 生成本地草稿？不会提交上游。")) return;
    setBusy(true);
    setError("");
    try {
      const result = await api.call("answer_work", {
        course_id: job.course_id,
        class_id: job.class_id,
        job_id: detail.job_id
      });
      setDrafts(result.data);
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title="待办与作业"
        meta={
          <Badge variant={actionItems ? (actions.length > 0 ? "warning" : "secondary") : "secondary"}>
            {actionItems ? (actions.length > 0 ? "有待处理" : "暂无待办") : "读取中"}
          </Badge>
        }
        actions={<Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>刷新待办</Button>}
      />
      <ErrorBanner error={error} />

      <Card>
        <CardHeader>
          <CardTitle>待处理事项</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {actionItems && <MetaSummary data={actionItems} />}
          {busy && !actionItems ? <Loading /> : !loaded ? null : actions.length === 0 ? <EmptyState title="当前没有明确待办" /> : (
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              {actions.map((item, index) => (
                <div key={index} className="rounded-control border border-border bg-muted/25 p-4">
                  <div className="flex items-center justify-between gap-3">
                    <Badge variant={item.kind === "assignment" ? "warning" : "secondary"} title={item.kind}>
                      {labelFor("actionKind", item.kind)}
                    </Badge>
                    <span className="text-xs text-muted-foreground" title={item.action}>
                      {labelFor("action", item.action)}
                    </span>
                  </div>
                  <div className="mt-3 text-sm font-medium">{item.assignment?.title ?? item.exam?.title ?? item.notice?.title ?? "未命名"}</div>
                  <div className="mt-1 text-xs text-muted-foreground">{item.assignment?.course ?? item.exam?.course ?? "—"}</div>
                  {item.assignment?.remaining && <div className="mt-2 text-xs text-warning">{item.assignment.remaining}</div>}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>全部作业</CardTitle></CardHeader>
        <CardContent>
          {assignments && <MetaSummary data={assignments} />}
          {busy && !assignments ? <Loading /> : !loaded ? null : rows.length === 0 ? <EmptyState title="没有作业数据" /> : (
            <>
            <div className="space-y-3 lg:hidden">
              {rows.map((row, index) => (
                <article key={row.id ?? index} className="rounded-card border border-border bg-card p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="truncate text-[11px] text-muted-foreground">{row.course}</div>
                      <h2 className="mt-1 text-[14px] font-medium leading-5">{row.title}</h2>
                    </div>
                    <Badge
                      variant={row.submission_state === "pending" ? "warning" : row.submission_state === "submitted" ? "success" : "secondary"}
                      title={row.submission_state}
                    >
                      {labelFor("submission", row.submission_state)}
                    </Badge>
                  </div>
                  <div className="mt-3 flex flex-wrap items-center gap-1.5">
                    <Badge variant={row.availability_state === "open" ? "success" : "secondary"} title={row.availability_state}>
                      {labelFor("availability", row.availability_state)}
                    </Badge>
                  </div>
                  <div className="mt-2 break-all font-mono text-[11px] text-muted-foreground">{row.id}</div>
                  <Button size="sm" variant="outline" className="mt-3 w-full" onClick={() => void loadQuestions(row)} disabled={busy}>读取题目</Button>
                </article>
              ))}
            </div>
            <div className="hidden lg:block">
            <Table label="全部作业">
              <TableHeader>
                <TableRow><TableHead>作业</TableHead><TableHead>课程</TableHead><TableHead>提交</TableHead><TableHead>可用性</TableHead><TableHead>ID</TableHead><TableHead>操作</TableHead></TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row, index) => (
                  <TableRow key={row.id ?? index}>
                    <TableCell className="font-medium">{row.title}</TableCell>
                    <TableCell>{row.course}</TableCell>
                    <TableCell>
                      <Badge
                        variant={row.submission_state === "pending" ? "warning" : row.submission_state === "submitted" ? "success" : "secondary"}
                        title={row.submission_state}
                      >
                        {labelFor("submission", row.submission_state)}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <Badge variant={row.availability_state === "open" ? "success" : "secondary"} title={row.availability_state}>
                        {labelFor("availability", row.availability_state)}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{row.id}</TableCell>
                  <TableCell className="sticky right-0 z-10 border-l border-border bg-card lg:static lg:border-l-0 lg:bg-transparent"><Button size="sm" variant="outline" onClick={() => void loadQuestions(row)} disabled={busy}>读取题目</Button></TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            </div>
            </>
          )}
        </CardContent>
      </Card>

      {detail && (
        <Card>
          <CardHeader>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <CardTitle>作业题目</CardTitle>
              </div>
              <Button size="sm" disabled={busy} onClick={() => void generateDraft()}>AI 草稿</Button>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            <MetaSummary data={detail} />
            <div className="space-y-2 lg:hidden">
              {(detail.questions ?? []).map((question: any, index: number) => (
                <article key={question.id ?? index} className="rounded-control border border-border bg-muted/15 p-3">
                  <div className="flex items-start justify-between gap-2">
                    <span className="font-mono text-[11px] text-muted-foreground">{question.id}</span>
                    <Badge title={question.type}>{labelFor("questionType", question.type)}</Badge>
                  </div>
                  <div className="mt-2 whitespace-pre-wrap text-[13px] leading-5">{question.title}</div>
                  <div className="mt-2 whitespace-pre-wrap text-[11px] leading-5 text-muted-foreground">{(question.options ?? []).join(" / ") || "—"}</div>
                </article>
              ))}
            </div>
            <div className="hidden lg:block">
            <Table label="作业题目">
              <TableHeader><TableRow><TableHead>题号</TableHead><TableHead>题目</TableHead><TableHead>类型</TableHead><TableHead>选项</TableHead></TableRow></TableHeader>
              <TableBody>
                {(detail.questions ?? []).map((question: any, index: number) => (
                  <TableRow key={question.id ?? index}>
                    <TableCell className="font-mono text-xs">{question.id}</TableCell>
                    <TableCell className="max-w-2xl whitespace-pre-wrap">{question.title}</TableCell>
                    <TableCell><Badge title={question.type}>{labelFor("questionType", question.type)}</Badge></TableCell>
                    <TableCell className="text-xs text-muted-foreground">{(question.options ?? []).join(" / ") || "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            </div>
            {drafts && (
              <div className="space-y-2">
                <div className="text-sm font-medium">本地草稿</div>
                <JsonBlock value={drafts} />
              </div>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
