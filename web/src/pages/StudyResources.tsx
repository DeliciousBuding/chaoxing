import * as React from "react";
import { CalendarDays, LibraryBig } from "lucide-react";
import { api } from "../lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { EmptyState, ErrorBanner, JsonBlock, Loading, MetaSummary, PageHeader, statusVariant } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { labelFor } from "../lib/labels";
import { teachingWeek } from "../lib/teaching-week";

const lessonDays = ["周一", "周二", "周三", "周四", "周五", "周六", "周日"];

function lessonRows(raw: any): { rows: any[]; recognized: boolean } {
  const candidates = [raw?.data?.data?.lessonArray, raw?.data?.lessonArray, raw?.lessonArray];
  for (const candidate of candidates) {
    if (Array.isArray(candidate)) return { rows: candidate, recognized: true };
  }
  return { rows: [], recognized: false };
}

function lessonPeriod(row: any) {
  const start = Number(row?.beginNumber);
  const length = Number(row?.length);
  if (!Number.isFinite(start) || start <= 0) return "—";
  if (!Number.isFinite(length) || length <= 1) return `第 ${start} 节`;
  return `第 ${start}–${start + length - 1} 节`;
}

function lessonDay(row: any) {
  const day = Number(row?.dayOfWeek);
  return Number.isFinite(day) && day >= 1 && day <= 7 ? lessonDays[day - 1] : "—";
}

function sortedLessons(rows: any[]) {
  return [...rows].sort((a, b) => {
    const dayDiff = (Number(a?.dayOfWeek) || 0) - (Number(b?.dayOfWeek) || 0);
    if (dayDiff !== 0) return dayDiff;
    return (Number(a?.beginNumber) || 0) - (Number(b?.beginNumber) || 0);
  });
}

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
    let active = true;
    Promise.all([
      api.call("list_courses"),
      api.watchSchedules().catch(() => null)
    ])
      .then(([courseResult, scheduleResult]) => {
        if (!active) return;
        setCourses(courseResult.data.result ?? []);
        const weekNumber = teachingWeek(new Date(), scheduleResult?.term_start);
        if (weekNumber >= 1 && weekNumber <= 30) setWeek(String(weekNumber));
      })
      .catch((err) => {
        if (active) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      active = false;
    };
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
    const failures: string[] = [];
    try {
      try {
        const chapterResult = await api.call("list_chapters", {
          course_id: selectedCourse.course_id,
          class_id: selectedCourse.class_id,
          cpi: selectedCourse.cpi,
          limit: 100
        });
        setChapters(chapterResult.data);
      } catch (err) {
        failures.push(`章节：${err instanceof Error ? err.message : String(err)}`);
      }
      try {
        const materialResult = await api.call("list_materials", {
          course_id: selectedCourse.course_id,
          class_id: selectedCourse.class_id,
          cpi: selectedCourse.cpi
        });
        setMaterials(materialResult.data);
      } catch (err) {
        failures.push(`资料：${err instanceof Error ? err.message : String(err)}`);
      }
      try {
        const scheduleResult = await api.call("get_schedule", { week: weekNumber });
        setSchedule(scheduleResult.data);
      } catch (err) {
        failures.push(`课表：${err instanceof Error ? err.message : String(err)}`);
      }
      if (failures.length > 0) setError(failures.join("；"));
    } finally {
      setBusy(false);
    }
  }

  const chapterRows: any[] = chapters?.items ?? [];
  const scheduleInfo = lessonRows(schedule);
  const lessons = sortedLessons(scheduleInfo.rows);

  return (
    <div className="space-y-5">
      <PageHeader title="课表与资料" />
      <ErrorBanner error={error} />
      <Card>
        <CardHeader>
          <CardTitle>读取范围</CardTitle>
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
              <CalendarDays className="h-4 w-4" /> {busy ? "读取中…" : "读取"}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>章节任务</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {chapters && <MetaSummary data={chapters} />}
          {busy && !chapters ? <Loading /> : !chapters ? <EmptyState bare title="选择课程后读取章节" /> : chapterRows.length === 0 ? (
            <EmptyState bare title="没有章节数据" />
          ) : (
            <>
            <div className="space-y-2 md:hidden">
              {chapterRows.map((row, index) => (
                <article key={`${row.number}-${index}`} className="rounded-control border border-border bg-card p-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <div className="font-mono text-[11px] text-muted-foreground">{row.number}</div>
                      <div className="mt-1 text-[13px] font-medium">{row.title}</div>
                    </div>
                    <Badge variant={statusVariant(row.state)} title={row.state} className="shrink-0">
                      {labelFor("chapterState", row.state)}
                    </Badge>
                  </div>
                  {row.pending_tasks != null && (
                    <div className="mt-2 text-[11px] text-muted-foreground">待做任务点 {row.pending_tasks}</div>
                  )}
                </article>
              ))}
            </div>
            <div className="hidden md:block">
            <Table label="章节任务">
              <TableHeader>
                <TableRow><TableHead>章节</TableHead><TableHead>标题</TableHead><TableHead>状态</TableHead><TableHead>待做任务点</TableHead></TableRow>
              </TableHeader>
              <TableBody>
                {chapterRows.map((row, index) => (
                  <TableRow key={`${row.number}-${index}`}>
                    <TableCell className="font-mono text-xs">{row.number}</TableCell>
                    <TableCell className="font-medium">{row.title}</TableCell>
                    <TableCell><Badge variant={statusVariant(row.state)} title={row.state}>{labelFor("chapterState", row.state)}</Badge></TableCell>
                    <TableCell>{row.pending_tasks ?? "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            </div>
            </>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle><LibraryBig className="mr-2 inline h-4 w-4" />课程资料</CardTitle>
          </CardHeader>
          <CardContent>
            {materials ? (
              <pre className="max-h-96 overflow-auto rounded-control border border-border bg-muted/30 p-4 text-xs leading-relaxed">
                {materials.text || "暂无资料文本"}
              </pre>
            ) : (
              <EmptyState bare title="尚未读取资料" />
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>教学周课表</CardTitle>
          </CardHeader>
          <CardContent>
            {!schedule ? (
              <EmptyState bare title="尚未读取课表" />
            ) : !scheduleInfo.recognized ? (
              <details className="rounded-control border border-border bg-muted/20">
                <summary className="cursor-pointer px-3 py-2.5 text-xs font-medium text-muted-foreground">上游原始数据</summary>
                <div className="px-3 pb-3">
                  <JsonBlock value={schedule} className="max-h-80" />
                </div>
              </details>
            ) : lessons.length === 0 ? (
              <EmptyState bare title="本周没有课程" />
            ) : (
              <>
                <div className="space-y-2 lg:hidden">
                  {lessons.map((row, index) => (
                    <article key={index} className="rounded-control border border-border bg-muted/15 p-3">
                      <div className="flex items-start justify-between gap-2">
                        <span className="text-[13px] font-medium">{row.name || row.courseName || "未命名课程"}</span>
                        <Badge variant="secondary">{lessonDay(row)}</Badge>
                      </div>
                      <div className="mt-1 text-[11px] text-muted-foreground">
                        {lessonPeriod(row)}
                        <span aria-hidden="true"> · </span>
                        {row.location || "教室未标注"}
                        {row.teacherName ? ` · ${row.teacherName}` : ""}
                      </div>
                    </article>
                  ))}
                </div>
                <div className="hidden lg:block">
                  <Table label="教学周课表">
                    <TableHeader>
                      <TableRow><TableHead className="w-[52px]">星期</TableHead><TableHead className="w-[78px]">节次</TableHead><TableHead>课程</TableHead><TableHead>地点</TableHead></TableRow>
                    </TableHeader>
                    <TableBody>
                      {lessons.map((row, index) => (
                        <TableRow key={index}>
                          <TableCell className="whitespace-nowrap">{lessonDay(row)}</TableCell>
                          <TableCell className="whitespace-nowrap font-mono text-xs">{lessonPeriod(row)}</TableCell>
                          <TableCell>
                            <div className="font-medium">{row.name || row.courseName || "未命名课程"}</div>
                            {row.teacherName && <div className="text-[11px] text-muted-foreground">{row.teacherName}</div>}
                          </TableCell>
                          <TableCell className="whitespace-nowrap">{row.location || "—"}</TableCell>
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
    </div>
  );
}
