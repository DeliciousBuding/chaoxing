import * as React from "react";
import { UserRound } from "lucide-react";
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
        <div className="flex h-full w-full items-center justify-center bg-muted/40">
          <span className="text-[22px] font-medium tracking-[-0.04em] text-foreground/25">{initials}</span>
        </div>
      )}
    </div>
  );
}

export function CourseCard({
  course,
  className
}: {
  course: Course;
  className?: string;
}) {
  return (
    <article
      className={cn(
        "group flex min-w-0 overflow-hidden rounded-card border border-border bg-card sm:block",
        className
      )}
    >
      <CourseCover
        course={course}
        className="h-16 w-16 shrink-0 border-r border-border sm:aspect-[16/6] sm:h-auto sm:w-auto sm:border-b sm:border-r-0"
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
      </div>
    </article>
  );
}
