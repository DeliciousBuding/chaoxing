import * as React from "react";
import { GraduationCap, LockKeyhole } from "lucide-react";
import { api, ApiError, type SessionInfo } from "../lib/api";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";

export function Login({ onLogin }: { onLogin: (session: SessionInfo) => void }) {
  const [password, setPassword] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  React.useEffect(() => {
    api.session().catch(() => undefined);
  }, []);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!password) return;
    setBusy(true);
    setError("");
    try {
      await api.login(password);
      onLogin(await api.session());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "登录失败");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden p-6">
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_20%_20%,rgba(34,211,238,0.12),transparent_40%),radial-gradient(circle_at_80%_10%,rgba(21,94,117,0.15),transparent_35%)]" />
      <Card className="w-full max-w-md border-border/80 bg-card/90 backdrop-blur">
        <CardHeader>
          <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/15 text-primary">
            <GraduationCap className="h-6 w-6" />
          </div>
          <CardTitle className="text-2xl">学习通管理平台</CardTitle>
          <CardDescription>输入所有者密码登录私有控制台。会话与 MCP OAuth 授权相互隔离。</CardDescription>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={submit}>
            <div className="space-y-2">
              <Label htmlFor="password">所有者密码</Label>
              <div className="relative">
                <LockKeyhole className="absolute left-3 top-3 h-4 w-4 text-muted-foreground" />
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
            {error && <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>}
            <Button className="w-full" type="submit" disabled={busy || !password}>
              {busy ? "验证中…" : "登录控制台"}
            </Button>
            <p className="text-xs leading-relaxed text-muted-foreground">
              登录使用 PBKDF2-SHA256 哈希验证，Cookie 为 HttpOnly + SameSite=Strict，所有写请求均要求 CSRF 双提交。
            </p>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
