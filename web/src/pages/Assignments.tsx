import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { EmptyState, ErrorBanner, JsonBlock, Loading, MetaSummary, PageHeader } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export function Assignments() {
  const [actionItems, setActionItems] = React.useState<any>(null);
  const [assignments, setAssignments] = React.useState<any>(null);
  const [detail, setDetail] = React.useState<any>(null);
  const [drafts, setDrafts] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

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
        description="“没有查到”不等于“没有任务”；请始终关注覆盖状态，并从作业与考试清单取数。"
        meta={<><Badge variant="secondary">{actions.length} 个待办</Badge><Badge variant="secondary">{rows.length} 个作业</Badge></>}
        actions={<Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>刷新待办</Button>}
      />
      <ErrorBanner error={error} />

      <Card>
        <CardHeader>
          <CardTitle>待处理事项</CardTitle>
          <CardDescription>来源：作业与考试清单，不从通知文本推断新任务。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {actionItems && <MetaSummary data={actionItems} />}
          {busy && !actionItems ? <Loading /> : actions.length === 0 ? <EmptyState title="当前没有明确待办" /> : (
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              {actions.map((item, index) => (
                <div key={index} className="rounded-control border border-border bg-muted/25 p-4">
                  <div className="flex items-center justify-between gap-3">
                    <Badge variant={item.kind === "assignment" ? "warning" : "secondary"}>{item.kind}</Badge>
                    <span className="text-xs text-muted-foreground">{item.action || "pending"}</span>
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
        <CardHeader><CardTitle>全部作业</CardTitle><CardDescription>最多显示 100 条，可进入作业答题页处理具体作业。</CardDescription></CardHeader>
        <CardContent>
          {assignments && <MetaSummary data={assignments} />}
          {busy && !assignments ? <Loading /> : rows.length === 0 ? <EmptyState title="没有作业数据" /> : (
            <Table>
              <TableHeader>
                <TableRow><TableHead>作业</TableHead><TableHead>课程</TableHead><TableHead>状态</TableHead><TableHead>提交</TableHead><TableHead>可用性</TableHead><TableHead>ID</TableHead><TableHead>操作</TableHead></TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row, index) => (
                  <TableRow key={row.id ?? index}>
                    <TableCell className="font-medium">{row.title}</TableCell>
                    <TableCell>{row.course}</TableCell>
                    <TableCell><Badge>{row.status ?? "unknown"}</Badge></TableCell>
                    <TableCell><Badge variant={row.submission_state === "pending" ? "warning" : row.submission_state === "submitted" ? "success" : "secondary"}>{row.submission_state}</Badge></TableCell>
                    <TableCell><Badge variant={row.availability_state === "open" ? "success" : "secondary"}>{row.availability_state}</Badge></TableCell>
                    <TableCell className="font-mono text-xs">{row.id}</TableCell>
                  <TableCell><Button size="sm" variant="outline" onClick={() => void loadQuestions(row)} disabled={busy}>读取题目</Button></TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {detail && (
        <Card>
          <CardHeader>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <CardTitle>作业题目</CardTitle>
                <CardDescription>读取 → AI 草稿；保存 / 最终提交请进入“作业答题”页。</CardDescription>
              </div>
              <Button size="sm" disabled={busy} onClick={() => void generateDraft()}>AI 草稿</Button>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            <MetaSummary data={detail} />
            <Table>
              <TableHeader><TableRow><TableHead>题号</TableHead><TableHead>题目</TableHead><TableHead>类型</TableHead><TableHead>选项</TableHead></TableRow></TableHeader>
              <TableBody>
                {(detail.questions ?? []).map((question: any, index: number) => (
                  <TableRow key={question.id ?? index}>
                    <TableCell className="font-mono text-xs">{question.id}</TableCell>
                    <TableCell className="max-w-2xl whitespace-pre-wrap">{question.title}</TableCell>
                    <TableCell><Badge>{question.type}</Badge></TableCell>
                    <TableCell className="text-xs text-muted-foreground">{(question.options ?? []).join(" / ") || "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
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
