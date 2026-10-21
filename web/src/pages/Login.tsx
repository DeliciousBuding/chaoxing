import * as React from "react";
import { GraduationCap, LockKeyhole, ShieldCheck } from "lucide-react";
import { api, ApiError, type SessionInfo } from "../lib/api";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { ErrorBanner } from "../components/common";

export function Login({ onLogin }: { onLogin: (session: SessionInfo) => void }) {
  const [username, setUsername] = React.useState("owner");
  const [password, setPassword] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  React.useEffect(() => {
    api.session().catch(() => undefined);
  }, []);

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
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden p-5 sm:p-6">
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_18%_18%,color-mix(in_oklab,var(--color-primary)_16%,transparent),transparent_38%),radial-gradient(circle_at_82%_8%,color-mix(in_oklab,var(--color-accent)_16%,transparent),transparent_34%)]" />
      <div className="pointer-events-none absolute inset-x-0 bottom-0 h-40 bg-gradient-to-t from-primary/5 to-transparent" />
      <Card className="w-full max-w-md border-border/70 bg-card/90 shadow-2xl shadow-black/10">
        <CardHeader>
          <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/14 text-primary ring-1 ring-primary/20">
            <GraduationCap className="h-6 w-6" />
          </div>
          <CardTitle className="text-2xl font-semibold tracking-tight">学习通管理平台</CardTitle>
          <CardDescription>输入平台用户名和密码登录。成员权限与 MCP OAuth 授权相互隔离。</CardDescription>
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
                required
                maxLength={32}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">密码</Label>
              <div className="relative">
                <LockKeyhole className="pointer-events-none absolute left-3.5 top-3.5 h-4 w-4 text-muted-foreground" />
                <Input
                  id="password"
                  type="password"
                  className="pl-10"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  autoComplete="current-password"
                  required
                  minLength={12}
                />
              </div>
            </div>
            <ErrorBanner error={error} />
            <Button className="w-full" type="submit" disabled={busy || !username.trim() || !password}>
              {busy ? "验证中…" : "登录控制台"}
            </Button>
            <div className="flex items-start gap-2 rounded-xl border border-border/70 bg-muted/35 p-3 text-xs leading-relaxed text-muted-foreground">
              <ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0 text-success" />
              <span>公网路径要求 HTTPS 反代；登录按客户端网络限速，Cookie 为 HttpOnly + SameSite=Strict，写请求均校验 Origin 与 CSRF。</span>
            </div>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
