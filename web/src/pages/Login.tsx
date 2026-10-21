import * as React from "react";
import { GraduationCap, LockKeyhole } from "lucide-react";
import { api, ApiError, type SessionInfo } from "../lib/api";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { ErrorBanner } from "../components/common";

export function Login({ onLogin }: { onLogin: (session: SessionInfo) => void }) {
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
    <div className="min-app-viewport flex flex-col bg-background pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] pt-[env(safe-area-inset-top)]">
      <section className="flex min-app-viewport flex-1 items-center justify-center px-5 py-10 pb-[calc(2.5rem+env(safe-area-inset-bottom))]">
        <div className="w-full max-w-[360px]">
          <div className="mb-6 flex items-center gap-2.5">
            <div className="flex h-8 w-8 items-center justify-center rounded-control border border-border bg-card">
              <GraduationCap className="h-4 w-4" />
            </div>
            <div className="text-[13px] font-medium">学习通控制台</div>
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
                    autoFocus
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
