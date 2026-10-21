import * as React from "react";
import { div as MotionDiv, span as MotionSpan } from "motion/react-m";
import { Group, Panel, Separator, type Layout } from "react-resizable-panels";
import { GraduationCap, LogOut, Palette, RefreshCw } from "lucide-react";
import { pageGroups, pages, type PageId } from "../lib/layout";
import { useUIStore, themeLabels, themes, type Theme } from "../lib/store";
import { useMediaQuery, useReducedMotion } from "../lib/hooks";
import type { AccountInfo, SessionInfo } from "../lib/api";
import { Button } from "./ui/button";
import { Select } from "./ui/select";
import { cn } from "../lib/utils";

const SidebarButton = React.memo(function SidebarButton({
  active,
  title,
  icon: Icon,
  onClick
}: {
  active: boolean;
  title: string;
  icon: React.ComponentType<{ className?: string }>;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? "page" : undefined}
      className={cn(
        "group relative flex h-10 w-full items-center gap-3 rounded-xl px-3 text-left text-sm transition-all",
        active
          ? "bg-primary/14 text-primary shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]"
          : "text-muted-foreground hover:bg-muted/70 hover:text-foreground"
      )}
    >
      {active && <MotionSpan layoutId="sidebar-active" className="absolute left-0 top-2 bottom-2 w-[3px] rounded-full bg-primary" />}
      <Icon className="h-[17px] w-[17px] shrink-0" />
      <span className="truncate">{title}</span>
    </button>
  );
});

function Sidebar({ version }: { version: string }) {
  const page = useUIStore((state) => state.page);
  const setPage = useUIStore((state) => state.setPage);
  return (
    <aside className="flex h-full min-h-0 flex-col bg-card/80 p-4">
      <div className="flex items-center gap-3 px-2 py-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-2xl bg-primary/15 text-primary ring-1 ring-primary/20">
          <GraduationCap className="h-5 w-5" />
        </div>
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold">学习通控制台</div>
          <div className="truncate text-[11px] text-muted-foreground">Nova 管理平台</div>
        </div>
      </div>
      <div role="separator" className="my-3 h-px w-full shrink-0 bg-border" />
      <nav className="min-h-0 flex-1 space-y-5 overflow-y-auto pr-1">
        {pageGroups.map((group) => (
          <div key={group.id} className="space-y-1">
            <div className="px-3 pb-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground/70">{group.title}</div>
            {pages.filter((item) => item.group === group.id).map((item) => (
              <SidebarButton
                key={item.id}
                active={page === item.id}
                title={item.title}
                icon={item.icon}
                onClick={() => setPage(item.id as PageId)}
              />
            ))}
          </div>
        ))}
      </nav>
      <div role="separator" className="my-3 h-px w-full shrink-0 bg-border" />
      <div className="space-y-1 px-2 text-[11px] text-muted-foreground">
        <div>版本 {version || "dev"}</div>
        <div className="opacity-70">私有管理面 · 多租户</div>
      </div>
    </aside>
  );
}

function ThemeSelect() {
  const theme = useUIStore((state) => state.theme);
  const setTheme = useUIStore((state) => state.setTheme);
  return (
    <div className="relative">
      <Palette className="pointer-events-none absolute left-3 top-3 h-4 w-4 text-muted-foreground" />
      <Select className="h-9 w-[132px] pl-9" value={theme} onChange={(event) => setTheme(event.target.value as Theme)}>
        {themes.map((item) => (
          <option key={item} value={item}>{themeLabels[item]}</option>
        ))}
      </Select>
    </div>
  );
}

function Header({
  session,
  account,
  onSwitchAccount,
  onLogout,
  onRefresh
}: {
  session: SessionInfo;
  account: AccountInfo | null;
  onSwitchAccount: (id: string) => void;
  onLogout: () => void;
  onRefresh: () => void;
}) {
  const page = useUIStore((state) => state.page);
  const meta = pages.find((item) => item.id === page) ?? pages[0];
  const accounts = account?.accounts ?? [{ id: "root", name: "默认账号" }];
  return (
    <header className="sticky top-0 z-20 border-b border-border/70 bg-background/88 px-5 py-4 backdrop-blur-xl">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <h1 className="truncate text-lg font-semibold tracking-tight">{meta.title}</h1>
          <p className="truncate text-xs text-muted-foreground">课程、活动、作业、答题、捕获与位置统一治理</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <ThemeSelect />
          <Select className="h-9 w-44" value={account?.current ?? session.account ?? "root"} onChange={(event) => onSwitchAccount(event.target.value)}>
            {accounts.map((item) => (
              <option key={item.id} value={item.id}>{item.name}</option>
            ))}
          </Select>
          <Button variant="outline" size="sm" onClick={onRefresh}>
            <RefreshCw className="h-4 w-4" /> 刷新
          </Button>
          <Button variant="ghost" size="sm" onClick={onLogout}>
            <LogOut className="h-4 w-4" /> 退出
          </Button>
        </div>
      </div>
    </header>
  );
}

function MobileNav() {
  const page = useUIStore((state) => state.page);
  const setPage = useUIStore((state) => state.setPage);
  return (
    <div className="flex gap-2 overflow-x-auto border-b border-border/70 bg-card/60 px-4 py-3 lg:hidden">
      {pages.map((item) => {
        const Icon = item.icon;
        return (
          <button
            key={item.id}
            type="button"
            onClick={() => setPage(item.id as PageId)}
            className={cn(
              "flex shrink-0 items-center gap-2 rounded-full border px-3 py-1.5 text-xs transition",
              page === item.id ? "border-primary/50 bg-primary/15 text-primary" : "border-border bg-background/60 text-muted-foreground"
            )}
          >
            <Icon className="h-3.5 w-3.5" />
            {item.title}
          </button>
        );
      })}
    </div>
  );
}

export function AppShell({
  session,
  account,
  onSwitchAccount,
  onLogout,
  onRefresh,
  children
}: {
  session: SessionInfo;
  account: AccountInfo | null;
  onSwitchAccount: (id: string) => void;
  onLogout: () => void;
  onRefresh: () => void;
  children: React.ReactNode;
}) {
  const page = useUIStore((state) => state.page);
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const reducedMotion = useReducedMotion();
  const [sidebarSize, setSidebarSize] = React.useState(() => {
    const saved = Number(window.localStorage.getItem("chaoxing-nova-layout:sidebar"));
    return Number.isFinite(saved) && saved >= 15 && saved <= 27 ? saved : 18;
  });

  const saveLayout = React.useCallback((layout: Layout, meta: { isUserInteraction: boolean }) => {
    const next = layout.sidebar;
    if (!meta.isUserInteraction || !Number.isFinite(next)) return;
    setSidebarSize(next);
    window.localStorage.setItem("chaoxing-nova-layout:sidebar", String(next));
  }, []);

  const transition = reducedMotion
    ? { initial: { opacity: 1 }, animate: { opacity: 1 }, exit: { opacity: 1 } }
    : { initial: { opacity: 0, y: 8 }, animate: { opacity: 1, y: 0 }, exit: { opacity: 0, y: -6 } };

  const content = (
    <>
      <Header session={session} account={account} onSwitchAccount={onSwitchAccount} onLogout={onLogout} onRefresh={onRefresh} />
      {!isDesktop && <MobileNav />}
      <main className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto p-4 pb-12 sm:p-5 lg:p-6">
        <MotionDiv key={page} {...transition} transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}>
          {children}
        </MotionDiv>
      </main>
    </>
  );

  if (!isDesktop) {
    return <div className="flex min-h-screen flex-col bg-background">{content}</div>;
  }

  return (
    <Group
      orientation="horizontal"
      className="h-screen min-h-screen bg-background"
      defaultLayout={{ sidebar: sidebarSize, main: 100 - sidebarSize }}
      onLayoutChanged={saveLayout}
    >
      <Panel id="sidebar" defaultSize={`${sidebarSize}%`} minSize="15%" maxSize="27%" className="min-w-0 border-r border-border/70">
        <Sidebar version={session.version} />
      </Panel>
      <Separator className="group relative w-[7px] bg-border/35 transition-colors hover:bg-primary/35 data-[separator-state=drag]:bg-primary/70">
        <div className="absolute left-1/2 top-1/2 h-10 w-1 -translate-x-1/2 -translate-y-1/2 rounded-full bg-muted-foreground/25 transition-colors group-hover:bg-primary/70" />
      </Separator>
      <Panel id="main" className="flex min-w-0 flex-col">{content}</Panel>
    </Group>
  );
}
