import * as React from "react";
import { cn } from "../../lib/utils";

const Label = React.forwardRef<HTMLLabelElement, React.LabelHTMLAttributes<HTMLLabelElement>>(
  ({ className, ...props }, ref) => (
    <label ref={ref} className={cn("text-[12px] font-medium leading-none text-foreground", className)} {...props} />
  )
);
Label.displayName = "Label";

export { Label };
