import * as React from "react";
import { cn } from "../../lib/utils";

type TableProps = React.HTMLAttributes<HTMLTableElement> & {
  label?: string;
};

const Table = React.forwardRef<HTMLTableElement, TableProps>(
  ({ className, label = "数据表格", ...props }, forwardedRef) => {
    const scrollerRef = React.useRef<HTMLDivElement | null>(null);
    const tableRef = React.useRef<HTMLTableElement | null>(null);
    const [overflowing, setOverflowing] = React.useState(false);

    const setRefs = React.useCallback(
      (node: HTMLTableElement | null) => {
        tableRef.current = node;
        if (typeof forwardedRef === "function") forwardedRef(node);
        else if (forwardedRef) forwardedRef.current = node;
      },
      [forwardedRef]
    );

    React.useEffect(() => {
      const scroller = scrollerRef.current;
      const table = tableRef.current;
      if (!scroller || !table) return;

      const measure = () => {
        setOverflowing(scroller.scrollWidth > scroller.clientWidth + 1);
      };
      measure();

      const observer = typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(measure);
      observer?.observe(scroller);
      observer?.observe(table);
      window.addEventListener("resize", measure);
      return () => {
        observer?.disconnect();
        window.removeEventListener("resize", measure);
      };
    }, []);

    return (
      <div className="min-w-0 max-w-full space-y-1">
        <div
          ref={scrollerRef}
          data-slot="scroll-area"
          role={overflowing ? "region" : undefined}
          aria-label={overflowing ? `${label}，可横向滚动` : undefined}
          tabIndex={overflowing ? 0 : undefined}
          className="w-full max-w-full overflow-x-auto rounded-control focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
        >
          <table
            ref={setRefs}
            className={cn("w-full max-md:min-w-[720px] caption-bottom border-collapse text-[13px]", className)}
            {...props}
          />
        </div>
        {overflowing && (
          <p className="text-[10px] leading-4 text-muted-foreground lg:hidden" aria-hidden="true">
            左右滑动查看全部列
          </p>
        )}
      </div>
    );
  }
);
Table.displayName = "Table";

const TableHeader = React.forwardRef<HTMLTableSectionElement, React.HTMLAttributes<HTMLTableSectionElement>>(
  ({ className, ...props }, ref) => <thead ref={ref} className={cn("bg-muted/55 [&_tr]:border-b [&_tr]:border-border", className)} {...props} />
);
TableHeader.displayName = "TableHeader";

const TableBody = React.forwardRef<HTMLTableSectionElement, React.HTMLAttributes<HTMLTableSectionElement>>(
  ({ className, ...props }, ref) => <tbody ref={ref} className={cn("[&_tr:last-child]:border-0", className)} {...props} />
);
TableBody.displayName = "TableBody";

const TableRow = React.forwardRef<HTMLTableRowElement, React.HTMLAttributes<HTMLTableRowElement>>(
  ({ className, ...props }, ref) => (
    <tr ref={ref} className={cn("border-b border-border transition-colors last:border-0 hover:bg-muted/45", className)} {...props} />
  )
);
TableRow.displayName = "TableRow";

const TableHead = React.forwardRef<HTMLTableCellElement, React.ThHTMLAttributes<HTMLTableCellElement>>(
  ({ className, scope = "col", ...props }, ref) => (
    <th ref={ref} scope={scope} className={cn("h-8 whitespace-nowrap px-3 text-left align-middle text-[12px] font-medium text-muted-foreground first:rounded-l-control last:rounded-r-control", className)} {...props} />
  )
);
TableHead.displayName = "TableHead";

const TableCell = React.forwardRef<HTMLTableCellElement, React.TdHTMLAttributes<HTMLTableCellElement>>(
  ({ className, ...props }, ref) => <td ref={ref} className={cn("px-3 py-2 align-middle text-[13px]", className)} {...props} />
);
TableCell.displayName = "TableCell";

export { Table, TableHeader, TableBody, TableRow, TableHead, TableCell };
