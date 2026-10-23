import * as React from "react";
import { createPortal } from "react-dom";
import {
  ChevronRight,
  MoreHorizontal,
  X
} from "lucide-react";
import { canAccessPage, pageGroups, pages, visiblePages, type PageId } from "../lib/layout";
import { useUIStore } from "../lib/store";
import { cn } from "../lib/utils";
import type { SessionInfo } from "../lib/api";

const primaryPages: PageId[] = ["dashboard", "courses", "activities", "todo"];
const primarySet = new Set<PageId>(primaryPages);

const focusableSelector = [
  "button:not([disabled])",
  "[href]",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex='-1'])"
].join(",");

export function MobileBottomNav({ capabilities }: { capabilities?: SessionInfo["capabilities"] }) {
  const page = useUIStore((state) => state.page);
  const setPage = useUIStore((state) => state.setPage);
  const [open, setOpen] = React.useState(false);
  const moreButtonRef = React.useRef<HTMLButtonElement | null>(null);
  const sheetRef = React.useRef<HTMLDivElement | null>(null);

  const accessiblePages = React.useMemo(
    () => visiblePages().filter((item) => canAccessPage(item.id as PageId, capabilities)),
    [capabilities]
  );
  const primaryItems = primaryPages
    .map((id) => accessiblePages.find((item) => item.id === id))
    .filter((item): item is (typeof pages)[number] => Boolean(item));
  const pageIsPrimary = primarySet.has(page) && primaryItems.some((item) => item.id === page);

  React.useEffect(() => {
    setOpen(false);
  }, [page]);

  React.useEffect(() => {
    if (!open) return;
    const trigger = moreButtonRef.current;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const timer = window.setTimeout(() => {
      const first = sheetRef.current?.querySelector<HTMLElement>("[data-sheet-focus]");
      (first ?? sheetRef.current)?.focus({ preventScroll: true });
    }, 28);

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setOpen(false);
        return;
      }
      if (event.key !== "Tab" || !sheetRef.current) return;
      const focusable = Array.from(
        sheetRef.current.querySelectorAll<HTMLElement>(focusableSelector)
      ).filter((node) => node.offsetParent !== null);
      if (focusable.length === 0) {
        event.preventDefault();
        sheetRef.current.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener("keydown", onKeyDown);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener("keydown", onKeyDown);
      document.body.style.overflow = previousOverflow;
      trigger?.focus({ preventScroll: true });
    };
  }, [open]);

  function selectPage(next: PageId) {
    setPage(next);
    setOpen(false);
  }

  return (
    <>
      <nav
        aria-label="移动端主导航"
        className="fixed inset-x-0 bottom-0 z-30 border-t border-border bg-sidebar pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] lg:hidden"
      >
        <div className="grid h-[58px] grid-cols-5">
          {primaryItems.map((item) => {
            const active = page === item.id;
            const Icon = item.icon;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => selectPage(item.id as PageId)}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "relative flex min-w-0 flex-col items-center justify-center gap-1 px-1 text-[11px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/70",
                  active ? "text-foreground" : "text-muted-foreground hover:text-foreground"
                )}
              >
                {active && (
                  <span className="absolute inset-x-2 inset-y-1.5 rounded-control bg-accent" />
                )}
                <span className="relative flex min-w-0 flex-col items-center gap-1">
                  <Icon className="h-[18px] w-[18px]" />
                  <span className="max-w-full truncate">{item.title}</span>
                </span>
              </button>
            );
          })}
          <button
            ref={moreButtonRef}
            type="button"
            onClick={() => setOpen(true)}
            aria-expanded={open}
            aria-controls="mobile-page-sheet"
            className={cn(
              "relative flex min-w-0 flex-col items-center justify-center gap-1 px-1 text-[11px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/70",
              !pageIsPrimary ? "text-foreground" : "text-muted-foreground hover:text-foreground"
            )}
          >
            {!pageIsPrimary && (
              <span className="absolute inset-x-2 inset-y-1.5 rounded-control bg-accent" />
            )}
            <span className="relative flex min-w-0 flex-col items-center gap-1">
              <MoreHorizontal className="h-[18px] w-[18px]" />
              <span>更多</span>
            </span>
          </button>
        </div>
      </nav>

      {typeof document !== "undefined" && open && createPortal(
        <>
          {open && (
            <div
              key="mobile-sheet-overlay"
              className="fixed inset-0 z-40 bg-black/55 lg:hidden"
              onClick={() => setOpen(false)}
              aria-hidden="true"
            />
          )}
          {open && (
            <div
              key="mobile-sheet"
              id="mobile-page-sheet"
              ref={sheetRef}
              role="dialog"
              aria-modal="true"
              aria-labelledby="mobile-page-sheet-title"
              tabIndex={-1}
              className="fixed inset-x-0 bottom-0 z-50 max-h-[82dvh] overflow-hidden rounded-t-panel border-t border-border bg-card pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] lg:hidden"
              onClick={(event) => event.stopPropagation()}
            >
              <div className="flex items-center justify-between border-b border-border px-4 py-3">
                <div>
                  <h2 id="mobile-page-sheet-title" className="text-[14px] font-medium">全部页面</h2>
                </div>
                <button
                  type="button"
                  onClick={() => setOpen(false)}
                  aria-label="关闭页面选择"
                  className="flex h-11 w-11 items-center justify-center rounded-control text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
                >
                  <X className="h-4 w-4" />
                </button>
              </div>

              <div
                data-slot="scroll-area"
                className="max-h-[calc(82dvh-73px)] touch-scroll-y overflow-y-auto overscroll-contain px-3 py-3"
              >
                {pageGroups.map((group) => {
                  const items = accessiblePages.filter((item) => item.group === group.id);
                  if (items.length === 0) return null;
                  return (
                    <section key={group.id} className="mb-3 last:mb-0" aria-labelledby={`mobile-group-${group.id}`}>
                      <h3
                        id={`mobile-group-${group.id}`}
                        className="px-2 pb-1.5 text-[11px] font-medium uppercase tracking-[0.12em] text-muted-foreground"
                      >
                        {group.title}
                      </h3>
                      <div className="overflow-hidden rounded-control border border-border">
                        {items.map((item, index) => {
                          const Icon = item.icon;
                          const active = item.id === page;
                          return (
                            <button
                              key={item.id}
                              type="button"
                              data-sheet-focus={index === 0 && group.id === pageGroups[0].id ? true : undefined}
                              onClick={() => selectPage(item.id as PageId)}
                              aria-current={active ? "page" : undefined}
                              className={cn(
                                "flex min-h-12 w-full items-center gap-3 border-b border-border px-3 text-left text-[13px] transition-colors last:border-b-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60",
                                active ? "bg-accent text-foreground" : "bg-card text-foreground hover:bg-muted"
                              )}
                            >
                              <span className={cn(
                                "flex h-8 w-8 shrink-0 items-center justify-center rounded-control border border-border",
                                active ? "border-primary/35 bg-primary/10 text-primary" : "bg-muted/45 text-muted-foreground"
                              )}>
                                <Icon className="h-4 w-4" />
                              </span>
                              <span className="min-w-0 flex-1 truncate">{item.title}</span>
                              {active && <span className="text-[11px] text-muted-foreground">当前</span>}
                              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                            </button>
                          );
                        })}
                      </div>
                    </section>
                  );
                })}
              </div>
            </div>
          )}
        </>,
        document.body
      )}
    </>
  );
}
