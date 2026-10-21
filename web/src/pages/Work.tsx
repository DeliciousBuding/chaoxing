import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { EmptyState, ErrorBanner, JsonBlock, Loading, MetaSummary, PageHeader } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { labelFor } from "../lib/labels";

export function Work({ tools }: { tools: any[] }) {
  const [courses, setCourses] = React.useState<any[]>([]);
  const [courseKey, setCourseKey] = React.useState("");
  const [jobs, setJobs] = React.useState<any[]>([]);
  const [jobID, setJobID] = React.useState("");
  const [questions, setQuestions] = React.useState<any>(null);
  const [drafts, setDrafts] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const courseKeyFor = (course: any) => `${encodeURIComponent(course.course_id)}:${encodeURIComponent(course.class_id)}`;
  const selectedCourse = courses.find((course) => courseKeyFor(course) === courseKey);

  React.useEffect(() => {
    api.call("list_courses").then((result) => setCourses(result.data.result ?? [])).catch((err) => setError(err.message));
  }, []);

  async function loadJobs(nextCourseKey = courseKey) {
    const course = courses.find((item) => courseKeyFor(item) === nextCourseKey);
    if (!course) return;
    setBusy(true);
    setError("");
    try {
      const result = await api.call("list_work_jobs", { course_id: course.course_id, class_id: course.class_id, cpi: course.cpi });
      setJobs(result.data.items ?? []);
      setJobID("");
      setQuestions(null);
      setDrafts(null);
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  async function loadQuestions(nextJobID = jobID) {
    if (!selectedCourse || !nextJobID) return;
    setBusy(true);
    setError("");
    try {
      const result = await api.call("get_work_questions", { course_id: selectedCourse.course_id, class_id: selectedCourse.class_id, job_id: nextJobID });
      setQuestions(result.data);
      const draftResult = await api.call("list_work_drafts", { course_id: selectedCourse.course_id, class_id: selectedCourse.class_id, job_id: nextJobID });
      setDrafts(draftResult.data);
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  async function generate() {
    if (!selectedCourse || !jobID) return;
    if (!window.confirm("使用配置的题库 / LLM 生成草稿？不会自动提交上游。")) return;
    setBusy(true);
    setError("");
    try {
      await api.call("answer_work", { course_id: selectedCourse.course_id, class_id: selectedCourse.class_id, job_id: jobID });
      await loadQuestions();
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  async function submit(action: "save_work" | "submit_work") {
    if (!selectedCourse || !jobID) return;
    const final = action === "submit_work";
    const message = final ? "最终提交不可撤销，确认所有题都有草稿并提交？" : "保存到上游暂存区？";
    if (!window.confirm(message)) return;
    setBusy(true);
    try {
      const result = await api.call(action, { course_id: selectedCourse.course_id, class_id: selectedCourse.class_id, job_id: jobID });
      setDrafts(result.data);
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title="作业答题"
      />
      <ErrorBanner error={error} />
      <Card>
        <CardHeader><CardTitle>选择作业</CardTitle></CardHeader>
        <CardContent className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
          <div className="space-y-2">
            <Label htmlFor="work-course">课程</Label>
            <Select id="work-course" value={courseKey} onChange={(event) => { setCourseKey(event.target.value); void loadJobs(event.target.value); }}>
              <option value="">选择课程</option>
              {courses.map((course) => <option key={courseKeyFor(course)} value={courseKeyFor(course)}>{course.name} / {course.class}</option>)}
            </Select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="work-job">作业 / 知识点</Label>
            <Select id="work-job" value={jobID} onChange={(event) => { setJobID(event.target.value); void loadQuestions(event.target.value); }} disabled={!selectedCourse}>
              <option value="">选择作业</option>
              {jobs.map((job) => <option key={job.id} value={job.id}>{job.title}</option>)}
            </Select>
          </div>
          <div className="flex flex-wrap items-center gap-2 xl:col-span-2">
            <Button variant="outline" disabled={!jobID || busy} onClick={() => void loadQuestions()}>读取</Button>
            <Button disabled={!jobID || busy} onClick={() => void generate()}>AI 草稿</Button>
            <Button variant="secondary" disabled={!jobID || busy} onClick={() => void submit("save_work")}>暂存</Button>
            <Button variant="destructive" disabled={!jobID || busy} onClick={() => void submit("submit_work")}>最终提交</Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>题目</CardTitle></CardHeader>
        <CardContent className="space-y-4">
          {questions && <MetaSummary data={questions} />}
          {busy && !questions ? <Loading /> : !questions ? <EmptyState bare title="选择作业后读取题目" /> : (
            <>
              <div className="space-y-2 lg:hidden">
                {(questions.questions ?? []).map((question: any, index: number) => (
                  <article key={question.id ?? index} className="rounded-control border border-border bg-muted/15 p-3">
                    <div className="flex items-start justify-between gap-2">
                      <span className="font-mono text-[11px] text-muted-foreground">{question.id}</span>
                      <Badge title={question.type}>{labelFor("questionType", question.type)}</Badge>
                    </div>
                    <div className="mt-2 whitespace-pre-wrap text-[13px] leading-5">{question.title}</div>
                    <div className="mt-2 whitespace-pre-wrap text-[11px] leading-5 text-muted-foreground">
                      {(question.options ?? []).join(" / ") || "—"}
                    </div>
                  </article>
                ))}
              </div>
              <div className="hidden lg:block">
                <Table label="作业题目">
                  <TableHeader><TableRow><TableHead>题号</TableHead><TableHead>题目</TableHead><TableHead>类型</TableHead><TableHead>选项</TableHead></TableRow></TableHeader>
                  <TableBody>
                    {(questions.questions ?? []).map((question: any, index: number) => (
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
            </>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>本地草稿</CardTitle></CardHeader>
          <CardContent>{drafts ? <JsonBlock value={drafts} /> : <EmptyState bare title="暂无草稿" />}</CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>可执行工具</CardTitle></CardHeader>
          <CardContent>
            <div className="flex flex-wrap gap-2">
              {tools.filter((tool) => tool.id.startsWith("works.") || tool.id.startsWith("workdrafts.")).map((tool) => <Badge key={tool.id}>{tool.title}</Badge>)}
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
