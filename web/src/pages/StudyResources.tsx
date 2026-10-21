import * as React from "react";
import { CalendarDays, LibraryBig } from "lucide-react";
import { api } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { EmptyState, ErrorBanner, JsonBlock, Loading, MetaSummary, PageHeader, statusVariant } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export function StudyResources() {
  const [courses, setCourses] = React.useState<any[]>([]);
  const [courseKey, setCourseKey] = React.useState("");
  const [week, setWeek] = React.useState("1");
  const [chapters, setChapters] = React.useState<any>(null);
  const [materials, setMaterials] = React.useState<any>(null);
  const [schedule, setSchedule] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const courseKeyFor = (course: any) => `${encodeURIComponent(course.course_id)}:${encodeURIComponent(course.class_id)}`;
  const selectedCourse = courses.find((course) => courseKeyFor(course) === courseKey);

  React.useEffect(() => {
    api.call("list_courses")
      .then((result) => setCourses(result.data.result ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, []);

  async function load() {
    const weekNumber = Number(week);
    if (!Number.isInteger(weekNumber) || weekNumber < 1 || weekNumber > 30) {
      setError("教学周必须是 1..30 的整数");
      return;
    }
    if (!selectedCourse) {
      setError("请选择课程");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const [chapterResult, materialResult, scheduleResult] = await Promise.all([
        api.call("list_chapters", {
          course_id: selectedCourse.course_id,
          class_id: selectedCourse.class_id,
          cpi: selectedCourse.cpi,
          limit: 100
        }),
        api.call("list_materials", {
          course_id: selectedCourse.course_id,
          class_id: selectedCourse.class_id,
          cpi: selectedCourse.cpi
        }),
        api.call("get_schedule", { week: weekNumber })
      ]);
      setChapters(chapterResult.data);
      setMaterials(materialResult.data);
      setSchedule(scheduleResult.data);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const chapterRows: any[] = chapters?.items ?? [];

  return (
    <div className="space-y-5">
      <PageHeader
        title="课表与资料"
        description="读取章节完成度、课程顶层资料文本和指定教学周课表；不做学习页任务点写入。"
        meta={<><Badge variant="secondary">{courses.length} 门课程</Badge><span>北京时间</span><span>·</span><span>只读</span></>}
        actions={<Button size="sm" variant="outline" onClick={() => void load()} disabled={!selectedCourse || busy}><CalendarDays className="h-3.5 w-3.5" /> 读取当前范围</Button>}
      />
      <ErrorBanner error={error} />
      <Card>
        <CardHeader>
          <CardTitle>读取范围</CardTitle>
          <CardDescription>课程与教学周共同决定章节、资料和课表查询。</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 lg:grid-cols-[1fr_180px_auto]">
          <div className="space-y-2">
            <Label htmlFor="study-course">课程</Label>
            <Select id="study-course" value={courseKey} onChange={(event) => setCourseKey(event.target.value)}>
              <option value="">选择课程</option>
              {courses.map((course) => (
                <option key={courseKeyFor(course)} value={courseKeyFor(course)}>
                  {course.name} / {course.class}
                </option>
              ))}
            </Select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="study-week">教学周</Label>
            <Input id="study-week" type="number" min={1} max={30} value={week} onChange={(event) => setWeek(event.target.value)} />
          </div>
          <div className="flex items-end">
            <Button disabled={!selectedCourse || busy} onClick={() => void load()}>
              <CalendarDays className="h-4 w-4" /> 读取
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>章节任务</CardTitle>
          <CardDescription>只显示章节摘要与显式上报的任务点数量，未知状态不会被推断为已完成。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {chapters && <MetaSummary data={chapters} />}
          {busy && !chapters ? <Loading /> : !chapters ? <EmptyState title="选择课程后读取章节" /> : chapterRows.length === 0 ? (
            <EmptyState title="没有章节数据" />
          ) : (
            <Table label="章节任务">
              <TableHeader>
                <TableRow><TableHead>章节</TableHead><TableHead>标题</TableHead><TableHead>状态</TableHead><TableHead>待做任务点</TableHead></TableRow>
              </TableHeader>
              <TableBody>
                {chapterRows.map((row, index) => (
                  <TableRow key={`${row.number}-${index}`}>
                    <TableCell className="font-mono text-xs">{row.number}</TableCell>
                    <TableCell className="font-medium">{row.title}</TableCell>
                    <TableCell><Badge variant={statusVariant(row.state)}>{row.state}</Badge></TableCell>
                    <TableCell>{row.pending_tasks ?? "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle><LibraryBig className="mr-2 inline h-4 w-4" />课程资料</CardTitle>
            <CardDescription>顶层文本投影；不支持文件夹遍历或附件下载。</CardDescription>
          </CardHeader>
          <CardContent>
            {materials ? (
              <pre className="max-h-96 overflow-auto rounded-control border border-border bg-muted/30 p-4 text-xs leading-relaxed">
                {materials.text || "暂无资料文本"}
              </pre>
            ) : (
              <EmptyState title="尚未读取资料" />
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>教学周课表</CardTitle>
            <CardDescription>原始结构仅做本地展示，不会自动生成待办。</CardDescription>
          </CardHeader>
          <CardContent>{schedule ? <JsonBlock value={schedule} className="max-h-96" /> : <EmptyState title="尚未读取课表" />}</CardContent>
        </Card>
      </div>
    </div>
  );
}
