import * as React from "react";
import { Activity, GraduationCap, LockKeyhole, MapPin, ShieldCheck } from "lucide-react";
import { api, ApiError, type SessionInfo } from "../lib/api";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Badge } from "../components/ui/badge";
import { ErrorBanner } from "../components/common";

const capabilities = [
  { icon: Activity, title: "课堂活动", description: "签到、测验、作业与通知统一进入一条可观测链路。" },
  { icon: MapPin, title: "位置策略", description: "按课表绑定坐标，保留出口 IP 与签到结果作为私有证据。" },
  { icon: ShieldCheck, title: "最小权限", description: "四档角色 + 账号授权；密钥、令牌与坐标不进入浏览器。" }
];

export function Login({ onLogin }: { onLogin: (session: SessionInfo) => void }) {
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const [bootError, setBootError] = React.useState("");

  React.useEffect(() => {
    api.session().catch((err) => setBootError(err instanceof Error ? err.message : String(err)));
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
    <div className="grid min-h-screen bg-background lg:grid-cols-[minmax(0,1.08fr)_minmax(420px,0.92fr)]">
      <section className="hidden min-h-screen flex-col justify-between border-r border-border bg-card p-10 lg:flex xl:p-14">
        <div>
          <div className="flex items-center gap-2.5">
            <div className="flex h-8 w-8 items-center justify-center rounded-control border border-border bg-muted">
              <GraduationCap className="h-4 w-4" />
            </div>
            <div>
              <div className="text-[13px] font-medium">学习通控制台</div>
              <div className="text-[11px] text-muted-foreground">Private classroom operations</div>
            </div>
          </div>

          <div className="mt-20 max-w-xl">
            <Badge variant="secondary">自托管 · 单实例 · 多租户</Badge>
            <h2 className="mt-5 text-[32px] font-medium leading-[1.18] tracking-[-0.035em] xl:text-[40px]">
              把课堂操作收进
              <br />
              一个安静的控制面
            </h2>
            <p className="mt-4 max-w-lg text-sm leading-6 text-muted-foreground">
              课程、签到、答题、作业、捕获与审计共享同一套身份和边界。没有第三方分析脚本，也没有把凭据交给浏览器。
            </p>
          </div>

          <div className="mt-12 grid max-w-2xl gap-px overflow-hidden rounded-card border border-border bg-border sm:grid-cols-3">
            {capabilities.map(({ icon: Icon, title, description }) => (
              <div key={title} className="bg-card p-4">
                <Icon className="h-4 w-4 text-muted-foreground" />
                <div className="mt-3 text-[13px] font-medium">{title}</div>
                <div className="mt-1 text-[11px] leading-5 text-muted-foreground">{description}</div>
              </div>
            ))}
          </div>
        </div>

        <div className="flex max-w-2xl items-center justify-between gap-4 border-t border-border pt-4 text-[11px] text-muted-foreground">
          <span>登录、成员与工具调用均写入控制面审计。</span>
          <span className="font-mono">HTTPS · SameSite=Strict</span>
        </div>
      </section>

      <section className="flex min-h-screen items-center justify-center p-5 sm:p-8">
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
              <CardDescription>使用平台成员账号。平台权限与 MCP OAuth 授权相互独立。</CardDescription>
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
                <ErrorBanner error={error || bootError} />
                <Button className="w-full" type="submit" disabled={busy || !username.trim() || !password}>
                  {busy ? "验证中…" : "登录控制台"}
                </Button>
                <div className="flex items-start gap-2 border-t border-border pt-4 text-[11px] leading-5 text-muted-foreground">
                  <ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  <span>登录按客户端网络限速；写请求校验 Origin 与 CSRF；会话绑定登录网络并支持一键失效。</span>
                </div>
              </form>
            </CardContent>
          </Card>
          <div className="mt-5 text-center text-[10px] text-muted-foreground">
            仅限授权成员 · 所有操作可追溯
          </div>
        </div>
      </section>
    </div>
  );
}
