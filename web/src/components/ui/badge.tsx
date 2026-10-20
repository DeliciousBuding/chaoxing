import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

const badgeVariants = cva("inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium", {
  variants: {
    variant: {
      default: "border-primary/40 bg-primary/15 text-primary",
      secondary: "border-border bg-secondary text-secondary-foreground",
      success: "border-success/40 bg-success/15 text-success",
      warning: "border-warning/40 bg-warning/15 text-warning",
      destructive: "border-destructive/40 bg-destructive/15 text-destructive"
    }
  },
  defaultVariants: { variant: "default" }
});

export interface BadgeProps extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge };
