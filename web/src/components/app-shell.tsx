import * as React from "react";
import { div as MotionDiv, span as MotionSpan } from "motion/react-m";
import { Group, Panel, Separator, type Layout } from "react-resizable-panels";
import {
  GraduationCap,
  LogOut,
  Palette
} from "lucide-react";
import { canAccessPage, pageGroups, pages, visiblePages, type PageId } from "../lib/layout";
import { useUIStore, themeLabels, themes, type Theme } from "../lib/store";
import { useMediaQuery, useReducedMotion } from "../lib/hooks";
import type { AccountInfo, SessionInfo } from "../lib/api";
import { roleLabels } from "../lib/roles";
import { Button } from "./ui/button";
import { Select } from "./ui/select";
import { Separator as Divider } from "./ui/separator";
import { MobileBottomNav } from "./mobile-nav";
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
  const reducedMotion = useReducedMotion();
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? "page" : undefined}
      className={cn(
        "group relative flex h-8 w-full items-center gap-2.5 rounded-control px-2.5 text-left text-[12px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
        active
          ? "bg-accent text-foreground"
          : "text-muted-foreground hover:bg-muted hover:text-foreground"
      )}
    >
      {active && (
        <MotionSpan
          layoutId={reducedMotion ? undefined : "sidebar-active"}
          transition={reducedMotion ? { duration: 0 } : undefined}
          className="absolute inset-y-1.5 left-0 w-[2px] rounded-full bg-primary"
        />
      )}
      <Icon className={cn("h-3.5 w-3.5 shrink-0", active ? "text-foreground" : "text-muted-foreground")} />
      <span className="truncate">{title}</span>
    </button>
  );
});

function SkipLink() {
  return (
    <a
      href="#main-content"
      className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:rounded-control focus:border focus:border-primary focus:bg-background focus:px-3 focus:py-2 focus:text-[12px] focus:text-foreground focus:outline-none focus:ring-2 focus:ring-ring/60"
    >
      跳到主要内容
    </a>
  );
}

function Sidebar({ session }: { session: SessionInfo }) {
  const page = useUIStore((state) => state.page);
  const setPage = useUIStore((state) => state.setPage);
  const role = session.user?.role;
  const roleLabel = role ? roleLabels[role] : "权限未加载";
  const displayName = session.user?.display_name || session.user?.username || "未命名成员";

  return (
    <aside className="flex h-full min-h-0 flex-col bg-sidebar">
      <div className="flex items-center gap-2.5 px-3 py-3">
        <div className="flex h-8 w-8 items-center justify-center rounded-control border border-border bg-card text-foreground">
          <GraduationCap className="h-4 w-4" />
        </div>
        <div className="min-w-0">
          <div className="truncate text-[12px] font-medium tracking-[-0.01em]">学习通控制台</div>
        </div>
      </div>
      <Divider />
      <nav aria-label="主导航" className="min-h-0 flex-1 space-y-3 overflow-y-auto px-2 py-3">
        {pageGroups.map((group) => {
          const items = visiblePages().filter(
            (item) => item.group === group.id && canAccessPage(item.id as PageId, session.capabilities)
          );
          if (items.length === 0) return null;
          return (
            <div key={group.id} className="space-y-0.5">
              <div className="px-2.5 pb-1 text-[11px] font-medium uppercase tracking-[0.12em] text-muted-foreground">
                {group.title}
              </div>
              {items.map((item) => (
                <SidebarButton
                  key={item.id}
                  active={page === item.id}
                  title={item.title}
                  icon={item.icon}
                  onClick={() => setPage(item.id as PageId)}
                />
              ))}
            </div>
          );
        })}
      </nav>
      <footer className="border-t border-border px-3 py-2.5">
        <div className="flex items-center gap-2">
          <span className="flex h-5 w-5 items-center justify-center rounded-full border border-border bg-muted text-[11px] font-medium text-muted-foreground">
            {displayName.slice(0, 1).toUpperCase()}
          </span>
          <div className="min-w-0 flex-1">
            <div className="truncate text-[11px] font-medium text-foreground">{displayName}</div>
            <div className="truncate text-[11px] text-muted-foreground">{roleLabel}</div>
          </div>
        </div>
      </footer>
    </aside>
  );
}

export function ThemeSelect({ compact = true }: { compact?: boolean }) {
  const theme = useUIStore((state) => state.theme);
  const setTheme = useUIStore((state) => state.setTheme);
  return (
    <div className="relative">
      {!compact && (
        <Palette className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
      )}
      <Select
        aria-label="主题"
        className={cn(
          "text-[11px]",
          compact ? "h-7 w-[104px]" : "h-8 w-full pl-8"
        )}
        value={theme}
        onChange={(event) => setTheme(event.target.value as Theme)}
      >
        {themes.map((item) => (
          <option key={item} value={item}>{themeLabels[item]}</option>
        ))}
      </Select>
    </div>
  );
}

function AccountSelect({
  session,
  account,
  switchingAccount,
  onSwitchAccount,
  className
}: {
  session: SessionInfo;
  account: AccountInfo | null;
  switchingAccount: boolean;
  onSwitchAccount: (id: string) => void;
  className?: string;
}) {
  const accounts = account?.accounts ?? [];
  const currentAccount = account?.current ?? session.account ?? "";
  return (
    <Select
      aria-label="当前账号"
      className={cn("h-11 w-[156px] text-[13px] sm:h-7 sm:text-[11px]", className)}
      value={currentAccount}
      onChange={(event) => onSwitchAccount(event.target.value)}
      disabled={accounts.length === 0 || switchingAccount}
    >
      {accounts.length === 0 ? (
        <option value="">无账号授权</option>
      ) : (
        accounts.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)
      )}
    </Select>
  );
}

function Header({
  session,
  account,
  switchingAccount,
  onSwitchAccount,
  onLogout
}: {
  session: SessionInfo;
  account: AccountInfo | null;
  switchingAccount: boolean;
  onSwitchAccount: (id: string) => void;
  onLogout: () => void;
}) {
  const page = useUIStore((state) => state.page);
  const meta = pages.find((item) => item.id === page) ?? pages[0];
  const group = pageGroups.find((item) => item.id === meta.group);

  return (
    <header className="sticky top-0 z-20 border-b border-border bg-background pt-[env(safe-area-inset-top)]">
      <div className="flex min-h-12 items-center gap-2 px-3 sm:min-h-11 sm:px-5">
        <nav aria-label="面包屑" className="flex min-w-0 flex-1 items-center gap-1.5 text-[11px] text-muted-foreground">
          <span className="truncate font-medium text-foreground sm:hidden">{meta.title}</span>
          <span className="hidden shrink-0 sm:inline">{group?.title ?? "控制台"}</span>
          <span aria-hidden="true" className="hidden sm:inline">/</span>
          <span className="hidden truncate text-foreground/80 sm:inline">{meta.title}</span>
        </nav>
        <div className="flex shrink-0 items-center gap-1.5">
          <div className="hidden 2xl:block">
            <ThemeSelect />
          </div>
          <div className="min-w-0">
            <AccountSelect
              session={session}
              account={account}
              switchingAccount={switchingAccount}
              onSwitchAccount={onSwitchAccount}
              className="w-[148px] sm:w-[156px]"
            />
          </div>
          <Button variant="ghost" size="icon" onClick={onLogout} aria-label="退出登录" title="退出登录">
            <LogOut className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    </header>
  );
}

export function AppShell({
  session,
  account,
  switchingAccount,
  onSwitchAccount,
  onLogout,
  children
}: {
  session: SessionInfo;
  account: AccountInfo | null;
  switchingAccount: boolean;
  onSwitchAccount: (id: string) => void;
  onLogout: () => void;
  children: React.ReactNode;
}) {
  const page = useUIStore((state) => state.page);
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const reducedMotion = useReducedMotion();
  const [sidebarSize, setSidebarSize] = React.useState(() => {
    const saved = Number(window.localStorage.getItem("chaoxing-linear-layout:sidebar"));
    return Number.isFinite(saved) && saved >= 14 && saved <= 24 ? saved : 18;
  });
  const mainRef = React.useRef<HTMLElement | null>(null);

  React.useEffect(() => {
    mainRef.current?.scrollTo({ top: 0 });
    mainRef.current?.focus({ preventScroll: true });
  }, [page]);

  const saveLayout = React.useCallback((layout: Layout, meta: { isUserInteraction: boolean }) => {
    const next = layout.sidebar;
    if (!meta.isUserInteraction || !Number.isFinite(next)) return;
    setSidebarSize(next);
    window.localStorage.setItem("chaoxing-linear-layout:sidebar", String(next));
  }, []);

  const transition = reducedMotion
    ? { initial: { opacity: 1 }, animate: { opacity: 1 }, exit: { opacity: 1 } }
    : { initial: { opacity: 0, y: 3 }, animate: { opacity: 1, y: 0 }, exit: { opacity: 0, y: -2 } };

  const content = (
    <>
      <Header
        session={session}
        account={account}
        switchingAccount={switchingAccount}
        onSwitchAccount={onSwitchAccount}
        onLogout={onLogout}
      />
      <main
        ref={mainRef}
        id="main-content"
        tabIndex={-1}
        className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto px-3 py-4 pb-[calc(5.75rem+env(safe-area-inset-bottom))] focus:outline-none sm:px-5 lg:px-6 lg:py-6 lg:pb-6"
      >
        <MotionDiv
          key={page}
          {...transition}
          transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}
          className="mx-auto w-full max-w-[1480px]"
        >
          {children}
        </MotionDiv>
      </main>
    </>
  );

  if (!isDesktop) {
    return (
      <div className="app-viewport flex flex-col bg-background pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)]">
        <SkipLink />
        {content}
        <MobileBottomNav capabilities={session.capabilities} />
      </div>
    );
  }

  return (
    <div className="app-viewport bg-background pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)]">
      <SkipLink />
      <Group
        orientation="horizontal"
        className="h-full"
        defaultLayout={{ sidebar: sidebarSize, main: 100 - sidebarSize }}
        onLayoutChanged={saveLayout}
      >
        <Panel id="sidebar" defaultSize={`${sidebarSize}%`} minSize="14%" maxSize="24%" className="min-w-0 border-r border-border">
          <Sidebar session={session} />
        </Panel>
        <Separator className="group relative w-[5px] bg-border/50 transition-colors hover:bg-primary/35 data-[separator-state=drag]:bg-primary/60">
          <div className="absolute left-1/2 top-1/2 h-8 w-px -translate-x-1/2 -translate-y-1/2 bg-border-strong transition-colors group-hover:bg-primary/60" />
        </Separator>
        <Panel id="main" className="flex min-w-0 flex-col">{content}</Panel>
      </Group>
    </div>
  );
}
