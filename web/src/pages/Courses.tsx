import * as React from "react";
import { Search } from "lucide-react";
import { api, type Course } from "../lib/api";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { CourseCard } from "../components/course-card";
import { EmptyState, ErrorBanner, PageHeader } from "../components/common";
import { cn } from "../lib/utils";

type Filter = "all" | "active" | "retired";

const filters: Array<{ id: Filter; label: string }> = [
  { id: "all", label: "全部" },
  { id: "active", label: "在读" },
  { id: "retired", label: "退课" }
];

export function Courses() {
  const [courses, setCourses] = React.useState<Course[]>([]);
  const [query, setQuery] = React.useState("");
  const [filter, setFilter] = React.useState<Filter>("all");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);

  const load = React.useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      const result = await api.call("list_courses");
      setCourses((result.data.result as Course[] | undefined) ?? []);
      setLoaded(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const visibleCourses = React.useMemo(() => {
    const needle = query.trim().toLowerCase();
    return courses
      .filter((course) => {
        if (filter === "active" && course.retired) return false;
        if (filter === "retired" && !course.retired) return false;
        if (!needle) return true;
        return [course.name, course.teacher, course.class, course.schools, course.course_id, course.class_id]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(needle));
      })
      .sort((a, b) => {
        if (Boolean(a.retired) !== Boolean(b.retired)) return a.retired ? 1 : -1;
        return a.name.localeCompare(b.name, "zh-CN");
      });
  }, [courses, filter, query]);

  return (
    <div className="space-y-5">
      <PageHeader
        title="课程"
        actions={
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>
            刷新
          </Button>
        }
      />
      <ErrorBanner error={error} />

      <div className="flex flex-col gap-2 rounded-control border border-border bg-card p-2 sm:flex-row sm:items-center">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="搜索课程"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索课程、教师、班级或学校"
            className="pl-8"
          />
        </div>
        <div className="flex shrink-0 items-center gap-0.5 rounded-control border border-border p-0.5" role="group" aria-label="课程状态筛选">
          {filters.map((item) => (
            <button
              key={item.id}
              type="button"
              aria-pressed={filter === item.id}
              onClick={() => setFilter(item.id)}
              className={cn(
                "h-10 rounded-[4px] px-3 text-[11px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 sm:h-8 sm:px-2.5",
                filter === item.id
                  ? "bg-accent text-foreground"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground"
              )}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>
      {busy && courses.length === 0 ? (
        <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {[0, 1, 2, 3, 4, 5].map((item) => (
            <div key={item} className="h-[86px] animate-pulse rounded-card border border-border bg-muted/35 sm:h-48" />
          ))}
        </div>
      ) : !loaded ? null : visibleCourses.length === 0 ? (
        <EmptyState
          title={courses.length === 0 ? "没有课程数据" : "没有匹配的课程"}
        />
      ) : (
        <div className="grid min-w-0 grid-cols-1 gap-2.5 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {visibleCourses.map((course) => (
            <CourseCard
              key={`${course.course_id}:${course.class_id}`}
              course={course}
              showDetails
            />
          ))}
        </div>
      )}
    </div>
  );
}
