import * as React from "react";
import { BookOpen, UserRound } from "lucide-react";
import { api, type Course } from "../lib/api";
import { Badge } from "./ui/badge";
import { cn } from "../lib/utils";
import { useUIStore } from "../lib/store";

export function CourseCover({
  course,
  className
}: {
  course: Course;
  className?: string;
}) {
  const [failed, setFailed] = React.useState(false);
  const account = useUIStore((state) => state.account?.current ?? state.session?.account ?? "");
  const initials = React.useMemo(() => {
    const trimmed = course.name.trim();
    return trimmed ? trimmed.slice(0, 2) : "课程";
  }, [course.name]);

  React.useEffect(() => {
    setFailed(false);
  }, [account, course.course_id, course.class_id]);

  return (
    <div className={cn("relative isolate overflow-hidden bg-muted", className)}>
      {course.cover_available && !failed ? (
        <img
          src={api.courseCoverUrl(course.course_id, course.class_id, account)}
          alt=""
          className="h-full w-full object-cover"
          loading="lazy"
          decoding="async"
          referrerPolicy="no-referrer"
          onError={() => setFailed(true)}
        />
      ) : (
        <div className="flex h-full w-full items-center justify-between gap-2 px-3 text-muted-foreground">
          <div className="min-w-0">
            <div className="truncate text-[16px] font-medium tracking-[-0.03em] text-foreground/70">{initials}</div>
            <div className="mt-0.5 truncate text-[10px] uppercase tracking-[0.08em] opacity-55">课程封面</div>
          </div>
          <BookOpen className="h-4 w-4 shrink-0 opacity-45" />
        </div>
      )}
    </div>
  );
}

export function CourseCard({
  course,
  showDetails = false,
  className
}: {
  course: Course;
  showDetails?: boolean;
  className?: string;
}) {
  return (
    <article
      className={cn(
        "group flex min-w-0 overflow-hidden rounded-card border border-border bg-card transition-colors hover:border-border-strong sm:block",
        className
      )}
    >
      <CourseCover
        course={course}
        className="h-[74px] w-[86px] shrink-0 border-r border-border sm:aspect-[16/8.5] sm:h-auto sm:w-auto sm:border-b sm:border-r-0"
      />
      <div className="min-w-0 flex-1 p-3">
        <div className="flex min-w-0 items-start justify-between gap-2">
          <h3 className="min-w-0 truncate text-[13px] font-medium leading-5" title={course.name}>
            {course.name}
          </h3>
          <Badge variant={course.retired ? "secondary" : "success"} className="shrink-0">
            {course.retired ? "退课" : "在读"}
          </Badge>
        </div>
        <div className="mt-1 flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
          <UserRound className="h-3 w-3 shrink-0" />
          <span className="truncate">{course.teacher || "教师未标注"}</span>
          <span aria-hidden="true">·</span>
          <span className="truncate">{course.class || course.class_id}</span>
        </div>
        {course.schools && (
          <div className="mt-1 truncate text-[11px] text-muted-foreground">{course.schools}</div>
        )}
        {showDetails && (
          <details className="mt-2 hidden border-t border-border/70 pt-1 sm:block">
            <summary className="flex min-h-8 cursor-pointer select-none items-center text-[11px] text-muted-foreground transition-colors hover:text-foreground">
              内部标识
            </summary>
            <dl className="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-1 font-mono text-[11px] text-muted-foreground">
              <dt>course</dt>
              <dd className="truncate text-foreground/75">{course.course_id}</dd>
              <dt>class</dt>
              <dd className="truncate text-foreground/75">{course.class_id}</dd>
              <dt>cpi</dt>
              <dd className="truncate text-foreground/75">{course.cpi}</dd>
            </dl>
          </details>
        )}
      </div>
    </article>
  );
}
