import * as React from "react";
import { api, SESSION_EXPIRED_EVENT } from "./lib/api";
import { pageFromURL, useUIStore, writePageURL } from "./lib/store";
import { canAccessPage, pageMeta } from "./lib/layout";
import { AppShell } from "./components/app-shell";
import { EmptyState, ErrorBanner, PageErrorBoundary, PageSkeleton } from "./components/common";

const Login = React.lazy(() => import("./pages/Login").then((m) => ({ default: m.Login })));
const Dashboard = React.lazy(() => import("./pages/Dashboard").then((m) => ({ default: m.Dashboard })));
const Courses = React.lazy(() => import("./pages/Courses").then((m) => ({ default: m.Courses })));
const Assignments = React.lazy(() => import("./pages/Assignments").then((m) => ({ default: m.Assignments })));
const StudyResources = React.lazy(() => import("./pages/StudyResources").then((m) => ({ default: m.StudyResources })));
const Activities = React.lazy(() => import("./pages/Activities").then((m) => ({ default: m.Activities })));
const Work = React.lazy(() => import("./pages/Work").then((m) => ({ default: m.Work })));
const Notices = React.lazy(() => import("./pages/Notices").then((m) => ({ default: m.Notices })));
const Exams = React.lazy(() => import("./pages/Exams").then((m) => ({ default: m.Exams })));
const Captures = React.lazy(() => import("./pages/Captures").then((m) => ({ default: m.Captures })));
const Locations = React.lazy(() => import("./pages/Locations").then((m) => ({ default: m.Locations })));
const Tools = React.lazy(() => import("./pages/Tools").then((m) => ({ default: m.Tools })));
const Members = React.lazy(() => import("./pages/Members").then((m) => ({ default: m.Members })));
const Audit = React.lazy(() => import("./pages/Audit").then((m) => ({ default: m.Audit })));
const Settings = React.lazy(() => import("./pages/Settings").then((m) => ({ default: m.Settings })));

function Booting() {
  return (
    <div className="flex min-app-viewport items-center justify-center p-6">
      <div className="w-full max-w-5xl space-y-4">
        <div className="h-8 w-48 animate-pulse rounded-control bg-muted" />
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          {[0, 1, 2, 3].map((item) => (
            <div key={item} className="h-24 animate-pulse rounded-card bg-muted/70" />
          ))}
        </div>
      </div>
    </div>
  );
}

export default function App() {
  const session = useUIStore((state) => state.session);
  const account = useUIStore((state) => state.account);
  const tools = useUIStore((state) => state.tools);
  const page = useUIStore((state) => state.page);
  const theme = useUIStore((state) => state.theme);
  const refreshToken = useUIStore((state) => state.refreshToken);
  const globalError = useUIStore((state) => state.globalError);
  const setSession = useUIStore((state) => state.setSession);
  const setAccount = useUIStore((state) => state.setAccount);
  const setTools = useUIStore((state) => state.setTools);
  const setGlobalError = useUIStore((state) => state.setGlobalError);
  const resetIdentity = useUIStore((state) => state.resetIdentity);
  const refresh = useUIStore((state) => state.refresh);
  const effectivePage = canAccessPage(page, session?.capabilities) ? page : "dashboard";
  const [initializing, setInitializing] = React.useState(true);
  const [accountEpoch, setAccountEpoch] = React.useState(0);
  const [switchingAccount, setSwitchingAccount] = React.useState(false);
  const switchingAccountRef = React.useRef(false);
  const activeAccount = account?.current ?? session?.account ?? "";
  const hasNoAccountScope = Boolean(session?.authenticated && account && account.accounts.length === 0);

  React.useEffect(() => {
    const fromURL = pageFromURL();
    if (fromURL) useUIStore.setState({ page: fromURL });
    else writePageURL(useUIStore.getState().page, "replace");
    const onPopState = () => {
      const next = pageFromURL();
      if (next) useUIStore.setState({ page: next });
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  React.useEffect(() => {
    const onExpired = () => {
      resetIdentity();
    };
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired);
  }, [resetIdentity]);

  React.useEffect(() => {
    document.title = `${pageMeta(effectivePage).title} · 学习通控制台`;
  }, [effectivePage]);

  React.useEffect(() => {
    if (session?.authenticated && page !== effectivePage) {
      useUIStore.getState().setPage(effectivePage);
    }
  }, [effectivePage, page, session?.authenticated]);

  React.useEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = theme;
    root.classList.toggle("light", theme === "nova-light");
    root.classList.toggle("dark", theme !== "nova-light");
    root.style.colorScheme = theme === "nova-light" ? "light" : "dark";
    const themeColors: Record<typeof theme, string> = {
      "nova-dark": "#08090a",
      "nova-light": "#fcfcfc",
      midnight: "#0a0b0d",
      contrast: "#000000"
    };
    let themeColor = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
    if (!themeColor) {
      themeColor = document.createElement("meta");
      themeColor.name = "theme-color";
      document.head.appendChild(themeColor);
    }
    themeColor.content = themeColors[theme];
  }, [theme]);

  React.useEffect(() => {
    let active = true;
    api.session()
      .then((value) => {
        if (active) setSession(value);
      })
      .catch(() => {
        if (!active) return;
        setSession({ authenticated: false, csrf: "", version: "" });
      })
      .finally(() => {
        if (active) setInitializing(false);
      });
    return () => {
      active = false;
    };
  }, [setSession]);

  React.useEffect(() => {
    if (!session?.authenticated) return;
    let active = true;
    api.account()
      .then((nextAccount) => {
        if (active) setAccount(nextAccount);
      })
      .catch((err) => {
        if (active) setGlobalError(`账号范围加载失败：${err instanceof Error ? err.message : String(err)}`);
      });
    return () => {
      active = false;
    };
  }, [session?.authenticated, setAccount, setGlobalError]);

  React.useEffect(() => {
    if (!session?.authenticated) return;
    let active = true;
    api.tools()
      .then((nextTools) => {
        if (active) setTools(nextTools.items);
      })
      .catch((err) => {
        if (active) setGlobalError(`工具能力加载失败：${err instanceof Error ? err.message : String(err)}`);
      });
    return () => {
      active = false;
    };
  }, [session?.authenticated, setGlobalError, setTools]);

  async function switchAccount(id: string) {
    if (id === activeAccount || switchingAccountRef.current) return;
    switchingAccountRef.current = true;
    setSwitchingAccount(true);
    setGlobalError("");
    // Remount account-scoped pages before the network round trip so stale
    // audit rows and dashboard values cannot remain visible under the new
    // account while the switch is in flight.
    setAccountEpoch((value) => value + 1);
    try {
      await api.selectAccount(id);
      const next = await api.account();
      setAccount(next);
      if (session) setSession({ ...session, account: id });
    } catch (err) {
      setGlobalError(err instanceof Error ? err.message : String(err));
      const restored = await api.account().catch(() => null);
      if (restored) setAccount(restored);
    } finally {
      switchingAccountRef.current = false;
      setSwitchingAccount(false);
    }
  }

  async function logout() {
    try {
      await api.logout();
      resetIdentity();
    } catch (err) {
      if (useUIStore.getState().session?.authenticated) {
        setGlobalError(`退出失败，服务端会话可能仍然有效：${err instanceof Error ? err.message : String(err)}`);
      }
    }
  }

  let content: React.ReactNode;
  if (effectivePage === "dashboard") content = <Dashboard tools={tools} />;
  else if (effectivePage === "courses") content = <Courses />;
  else if (effectivePage === "todo") content = <Assignments />;
  else if (effectivePage === "study") content = <StudyResources />;
  else if (effectivePage === "activities") content = <Activities tools={tools} />;
  else if (effectivePage === "work") content = <Work tools={tools} />;
  else if (effectivePage === "notices") content = <Notices />;
  else if (effectivePage === "exams") content = <Exams />;
  else if (effectivePage === "captures") content = <Captures />;
  else if (effectivePage === "locations") content = <Locations />;
  else if (effectivePage === "tools") content = <Tools tools={tools} />;
  else if (effectivePage === "members") content = <Members session={session!} />;
  else if (effectivePage === "audit") content = <Audit />;
  else content = <Settings account={account} session={session!} />;

  return (
    <div className="min-app-viewport bg-background text-foreground">
      <React.Suspense fallback={<Booting />}>
        {initializing ? (
          <Booting />
        ) : !session?.authenticated ? (
          <Login onLogin={(value) => { setGlobalError(""); setSession(value); }} />
        ) : (
          <AppShell
            session={session}
            account={account}
            switchingAccount={switchingAccount}
            onSwitchAccount={(id) => void switchAccount(id)}
            onLogout={() => void logout()}
            onRefresh={refresh}
          >
            <div className="space-y-4">
              <ErrorBanner error={globalError} />
              <PageErrorBoundary resetKey={`${effectivePage}:${refreshToken}:${accountEpoch}:${activeAccount}`}>
                {hasNoAccountScope && effectivePage !== "settings" ? (
                  <EmptyState
                    title="尚未授权学习通账号"
                    description="管理员需要在“成员与权限”中为当前成员分配至少一个上游账号；在此之前，业务数据和工具调用不会发起。"
                  />
                ) : (
                  <React.Suspense fallback={<PageSkeleton />}>
                    <React.Fragment key={`${refreshToken}:${accountEpoch}:${activeAccount}`}>{content}</React.Fragment>
                  </React.Suspense>
                )}
              </PageErrorBoundary>
            </div>
          </AppShell>
        )}
      </React.Suspense>
    </div>
  );
}
