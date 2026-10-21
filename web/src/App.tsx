import * as React from "react";
import { api } from "./lib/api";
import { pageFromURL, useUIStore, writePageURL } from "./lib/store";
import { canAccessPage, pageMeta } from "./lib/layout";
import { AppShell } from "./components/app-shell";
import { ErrorBanner, PageErrorBoundary, PageSkeleton } from "./components/common";

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
    <div className="flex min-h-screen items-center justify-center p-6">
      <div className="w-full max-w-5xl space-y-4">
        <div className="h-10 w-56 animate-pulse rounded-xl bg-muted" />
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
          {[0, 1, 2, 3].map((item) => (
            <div key={item} className="h-32 animate-pulse rounded-2xl bg-muted/70" />
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
  const [initializing, setInitializing] = React.useState(true);
  const [accountEpoch, setAccountEpoch] = React.useState(0);
  const activeAccount = account?.current ?? session?.account ?? "root";

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
    document.title = `${pageMeta(page).title} · 学习通控制台`;
  }, [page]);

  React.useEffect(() => {
    if (session?.authenticated && !canAccessPage(page, session.capabilities)) {
      useUIStore.getState().setPage("dashboard");
    }
  }, [page, session?.authenticated, session?.capabilities]);

  React.useEffect(() => {
    document.body.dataset.theme = theme;
    return () => {
      delete document.body.dataset.theme;
    };
  }, [theme]);

  React.useEffect(() => {
    let active = true;
    api.session()
      .then((value) => {
        if (active) setSession(value);
      })
      .catch((err) => {
        if (!active) return;
        setSession({ authenticated: false, csrf: "", version: "" });
        setGlobalError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (active) setInitializing(false);
      });
    return () => {
      active = false;
    };
  }, [setGlobalError, setSession]);

  React.useEffect(() => {
    if (!session?.authenticated) return;
    let active = true;
    Promise.all([api.account(), api.tools()])
      .then(([nextAccount, nextTools]) => {
        if (!active) return;
        setAccount(nextAccount);
        setTools(nextTools.items);
      })
      .catch((err) => setGlobalError(err instanceof Error ? err.message : String(err)));
    return () => {
      active = false;
    };
  }, [session?.authenticated, session?.account, setAccount, setGlobalError, setTools]);

  async function switchAccount(id: string) {
    if (id === activeAccount) return;
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
    }
  }

  async function logout() {
    await api.logout().catch(() => undefined);
    resetIdentity();
  }

  let content: React.ReactNode;
  if (page === "dashboard") content = <Dashboard tools={tools} />;
  else if (page === "courses") content = <Courses />;
  else if (page === "todo") content = <Assignments />;
  else if (page === "study") content = <StudyResources />;
  else if (page === "activities") content = <Activities tools={tools} />;
  else if (page === "work") content = <Work tools={tools} />;
  else if (page === "notices") content = <Notices />;
  else if (page === "exams") content = <Exams />;
  else if (page === "captures") content = <Captures />;
  else if (page === "locations") content = <Locations />;
  else if (page === "tools") content = <Tools tools={tools} />;
  else if (page === "members") content = <Members session={session!} />;
  else if (page === "audit") content = <Audit />;
  else content = <Settings account={account} session={session!} />;

  return (
    <div data-theme={theme} className="min-h-screen bg-background text-foreground">
      <React.Suspense fallback={<Booting />}>
        {initializing ? (
          <Booting />
        ) : !session?.authenticated ? (
          <Login onLogin={setSession} />
        ) : (
          <AppShell
            session={session}
            account={account}
            onSwitchAccount={(id) => void switchAccount(id)}
            onLogout={() => void logout()}
            onRefresh={refresh}
          >
            <div className="space-y-4">
              <ErrorBanner error={globalError} />
              <PageErrorBoundary resetKey={`${page}:${refreshToken}:${accountEpoch}:${activeAccount}`}>
                <React.Suspense fallback={<PageSkeleton />}>
                  <React.Fragment key={`${refreshToken}:${accountEpoch}:${activeAccount}`}>{content}</React.Fragment>
                </React.Suspense>
              </PageErrorBoundary>
            </div>
          </AppShell>
        )}
      </React.Suspense>
    </div>
  );
}
