import * as React from "react";
import { cn } from "../lib/utils";

export function Avatar({
  src,
  name,
  className
}: {
  src?: string;
  name: string;
  className?: string;
}) {
  const [failed, setFailed] = React.useState(false);
  const initial = (name.trim().slice(0, 1) || "学").toUpperCase();

  React.useEffect(() => {
    setFailed(false);
  }, [src]);

  return (
    <div
      className={cn(
        "flex shrink-0 items-center justify-center overflow-hidden rounded-full border border-border bg-muted text-foreground",
        className
      )}
      aria-hidden="true"
    >
      {src && !failed ? (
        <img
          src={src}
          alt=""
          className="h-full w-full object-cover"
          loading="eager"
          decoding="async"
          referrerPolicy="no-referrer"
          onError={() => setFailed(true)}
        />
      ) : (
        <span className="text-[13px] font-medium tracking-[-0.02em]">{initial}</span>
      )}
    </div>
  );
}
