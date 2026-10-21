import * as React from "react";
import {
  Activity,
  BookOpen,
  CalendarDays,
  Brain,
  ClipboardList,
  GraduationCap,
  LayoutDashboard,
  LogOut,
  RefreshCw,
  Shield,
  Sparkles,
  MapPin,
  Wrench
} from "lucide-react";
import { api, type AccountInfo, type SessionInfo, type ToolDefinition } from "./lib/api";
import { Button } from "./components/ui/button";
import { Select } from "./components/ui/select";
import { ErrorBanner } from "./components/common";
import { Login } from "./pages/Login";
import { Locations } from "./pages/Locations";
import { Dashboard } from "./pages/Dashboard";
import { Courses } from "./pages/Courses";
import { Assignments } from "./pages/Assignments";
import { Activities } from "./pages/Activities";
import { Work } from "./pages/Work";
import { Notices } from "./pages/Notices";
import { Exams } from "./pages/Exams";
import { StudyResources } from "./pages/StudyResources";
import { Captures } from "./pages/Captures";
import { Tools } from "./pages/Tools";
import { Settings } from "./pages/Settings";

const pages = [
  { id: "dashboard", title: "总览", icon: LayoutDashboard },
  { id: "courses", title: "课程", icon: BookOpen },
  { id: "todo", title: "待办", icon: ClipboardList },
  { id: "study", title: "课表/章节/资料", icon: CalendarDays },
  { id: "activities", title: "课堂活动", icon: Activity },
  { id: "work", title: "作业答题", icon: Brain },
  { id: "notices", title: "通知", icon: GraduationCap },
  { id: "exams", title: "考试", icon: ClipboardList },
  { id: "captures", title: "捕获", icon: Sparkles },
  { id: "tools", title: "工具面板", icon: Wrench },
  { id: "locations", title: "位置", icon: MapPin },
  { id: "settings", title: "安全与配置", icon: Shield }
] as const;

export type PageId = (typeof pages)[number]["id"];

export default function App() {
  const [session, setSession] = React.useState<SessionInfo | null>(null);
  const [initializing, setInitializing] = React.useState(true);
  const [account, setAccount] = React.useState<AccountInfo | null>(null);
  const [tools, setTools] = React.useState<ToolDefinition[]>([]);
  const [page, setPage] = React.useState<PageId>("dashboard");
  const [globalError, setGlobalError] = React.useState("");

  React.useEffect(() => {
    api.session()
      .then(setSession)
      .catch((err) => {
        setSession({ authenticated: false, csrf: "", version: "" });
        setGlobalError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => setInitializing(false));
  }, []);

  React.useEffect(() => {
    if (!session?.authenticated) return;
    api.account().then(setAccount).catch(() => undefined);
    api.tools().then((value) => setTools(value.items)).catch(() => undefined);
  }, [session?.authenticated, session?.account]);

  async function switchAccount(id: string) {
    setGlobalError("");
    try {
      await api.selectAccount(id);
      const next = await api.account();
      setAccount(next);
      setSession({ ...session!, account: id });
    } catch (err) {
      setGlobalError(err instanceof Error ? err.message : String(err));
    }
  }

  async function logout() {
    await api.logout().catch(() => undefined);
    setSession({ authenticated: false, csrf: "", version: session?.version ?? "" });
    setAccount(null);
    setTools([]);
  }

  if (initializing) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background text-muted-foreground">
        <div className="flex items-center gap-3">
          <div className="h-10 w-10 animate-pulse rounded-xl bg-primary/30" />
          正在初始化管理平台…
        </div>
      </div>
    );
  }

  if (!session?.authenticated) {
    return <Login onLogin={setSession} />;
  }

  return (
    <div className="min-h-screen bg-background">
      <div className="mx-auto flex min-h-screen w-full max-w-[1600px]">
        <aside className="sticky top-0 hidden h-screen w-64 shrink-0 border-r bg-card/70 p-5 lg:block">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/15 text-primary">
              <GraduationCap className="h-5 w-5" />
            </div>
            <div>
              <div className="text-sm font-semibold">学习通控制台</div>
              <div className="text-xs text-muted-foreground">本地托管平台</div>
            </div>
          </div>
          <nav className="mt-8 space-y-1">
            {pages.map((item) => {
              const Icon = item.icon;
              return (
                <button
                  key={item.id}
                  onClick={() => setPage(item.id)}
                  className={`flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm transition ${
                    page === item.id ? "bg-primary/15 text-primary" : "text-muted-foreground hover:bg-muted hover:text-foreground"
                  }`}
                >
                  <Icon className="h-4 w-4" />
                  {item.title}
                </button>
              );
            })}
          </nav>
          <div className="absolute bottom-5 left-5 right-5 text-xs text-muted-foreground">
            <div>版本 {session.version}</div>
            <div className="mt-1 opacity-70">私有管理面 · 会话 8 小时</div>
          </div>
        </aside>

        <main className="min-w-0 flex-1">
          <header className="sticky top-0 z-20 border-b bg-background/85 px-5 py-4 backdrop-blur">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h1 className="text-xl font-semibold">{pages.find((item) => item.id === page)?.title}</h1>
                <p className="text-xs text-muted-foreground">统一管理课程、活动、作业、答题与生产捕获</p>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Select
                  className="h-9 w-44"
                  value={account?.current ?? session.account ?? "root"}
                  onChange={(event) => void switchAccount(event.target.value)}
                >
                  {(account?.accounts ?? [{ id: "root", name: "默认账号" }]).map((item) => (
                    <option key={item.id} value={item.id}>{item.name}</option>
                  ))}
                </Select>
                <Button variant="outline" size="sm" onClick={() => setPage("dashboard")}>
                  <RefreshCw className="h-4 w-4" /> 刷新
                </Button>
                <Button variant="ghost" size="sm" onClick={() => void logout()}>
                  <LogOut className="h-4 w-4" /> 退出
                </Button>
              </div>
            </div>
            <div className="mt-3 flex gap-2 overflow-x-auto lg:hidden">
              {pages.map((item) => (
                <button
                  key={item.id}
                  onClick={() => setPage(item.id)}
                  className={`rounded-full border px-3 py-1 text-xs ${page === item.id ? "border-primary/50 bg-primary/15 text-primary" : "text-muted-foreground"}`}
                >
                  {item.title}
                </button>
              ))}
            </div>
          </header>

          <div className="p-5 pb-16">
            <ErrorBanner error={globalError} />
            {page === "dashboard" && <Dashboard tools={tools} />}
            {page === "courses" && <Courses />}
            {page === "todo" && <Assignments />}
            {page === "study" && <StudyResources />}
            {page === "activities" && <Activities tools={tools} />}
            {page === "work" && <Work tools={tools} />}
            {page === "notices" && <Notices />}
            {page === "exams" && <Exams />}
            {page === "captures" && <Captures />}
            {page === "locations" && <Locations />}
            {page === "tools" && <Tools tools={tools} />}
            {page === "settings" && <Settings account={account} session={session} />}
          </div>
        </main>
      </div>
    </div>
  );
}
