import * as React from "react";
import { GraduationCap, LockKeyhole } from "lucide-react";
import { api, ApiError, type SessionInfo } from "../lib/api";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { ErrorBanner } from "../components/common";
import { useMediaQuery } from "../lib/hooks";

export function Login({ onLogin }: { onLogin: (session: SessionInfo) => void }) {
  const desktop = useMediaQuery("(min-width: 1024px)");
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!username.trim() || !password) return;
    setBusy(true);
    setError("");
    try {
      await api.login(username.trim(), password);
      onLogin(await api.session());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "登录失败");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="min-app-viewport grid bg-background pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] pt-[env(safe-area-inset-top)] lg:grid-cols-[minmax(0,1.08fr)_minmax(420px,0.92fr)]">
      <section className="hidden min-app-viewport flex-col justify-between border-r border-border bg-card p-10 lg:flex xl:p-14">
        <div>
          <div className="flex items-center gap-2.5">
            <div className="flex h-8 w-8 items-center justify-center rounded-control border border-border bg-muted">
              <GraduationCap className="h-4 w-4" />
            </div>
            <div>
              <div className="text-[13px] font-medium">学习通控制台</div>
            </div>
          </div>

          <div className="mt-20 max-w-xl">
            <h2 className="text-[32px] font-medium leading-[1.18] tracking-[-0.035em] xl:text-[40px]">
              学习通控制台
            </h2>
            <p className="mt-3 text-sm text-muted-foreground">课程、签到、作业与审计</p>
          </div>
        </div>

      </section>

      <section className="flex min-app-viewport items-start justify-center px-5 py-8 pb-[calc(2rem+env(safe-area-inset-bottom))] sm:items-center sm:p-8">
        <div className="w-full max-w-[390px]">
          <div className="mb-8 flex items-center gap-2.5 lg:hidden">
            <div className="flex h-8 w-8 items-center justify-center rounded-control border border-border bg-card">
              <GraduationCap className="h-4 w-4" />
            </div>
            <div>
              <div className="text-[13px] font-medium">学习通控制台</div>
              <div className="text-[11px] text-muted-foreground">私有管理面</div>
            </div>
          </div>

          <Card>
            <CardHeader className="gap-1.5">
              <CardTitle as="h1" className="text-[20px] leading-7 tracking-[-0.025em]">登录工作区</CardTitle>
            </CardHeader>
            <CardContent>
              <form className="space-y-4" onSubmit={submit}>
                <div className="space-y-2">
                  <Label htmlFor="username">用户名</Label>
                  <Input
                    id="username"
                    name="username"
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
                    autoComplete="username"
                    autoCapitalize="none"
                    autoCorrect="off"
                    spellCheck={false}
                    autoFocus={desktop}
                    required
                    maxLength={32}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="password">密码</Label>
                  <div className="relative">
                    <LockKeyhole className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                    <Input
                      id="password"
                      type="password"
                      className="pl-9"
                      value={password}
                      onChange={(event) => setPassword(event.target.value)}
                      autoComplete="current-password"
                      required
                    />
                  </div>
                </div>
                <ErrorBanner error={error} />
                <Button className="w-full" type="submit" disabled={busy || !username.trim() || !password}>
                  {busy ? "验证中…" : "登录控制台"}
                </Button>
              </form>
            </CardContent>
          </Card>
        </div>
      </section>
    </div>
  );
}
