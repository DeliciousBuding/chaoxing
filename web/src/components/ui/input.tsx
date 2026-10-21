import * as React from "react";
import { cn } from "../../lib/utils";

const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, type, ...props }, ref) => (
    <input
      type={type}
      ref={ref}
      className={cn(
        "h-11 w-full rounded-control border border-input bg-background px-2.5 py-1.5 text-[13px] transition-colors placeholder:text-muted-foreground focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:cursor-not-allowed disabled:opacity-50 sm:h-8",
        className
      )}
      {...props}
    />
  )
);
Input.displayName = "Input";

export { Input };
