import * as React from "react";
import { api, type AccountInfo, type SessionInfo } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { JsonBlock } from "../components/common";
import { formatBeijing } from "../lib/time";

export function Settings({ account, session }: { account: AccountInfo | null; session: SessionInfo }) {
  const [status, setStatus] = React.useState<any>(null);
  React.useEffect(() => { api.status().then(setStatus).catch(() => undefined); }, []);

  const security = [
    ["所有者密码", "PBKDF2-SHA256 / 600,000 iterations"],
    ["会话 Cookie", "__Host- 前缀 / HttpOnly / Secure / SameSite=Strict"],
    ["会话绑定", "登录来源地址"],
    ["CSRF", "双提交 Header + Cookie，所有写请求强制校验"],
    ["登录限速", "每个客户端 1 分钟 5 次 / 15 分钟 20 次"],
    ["会话生命周期", "8 小时绝对过期 / 30 分钟空闲过期"],
    ["会话容量", "最多 32 个活跃会话，登录时清理过期会话"],
    ["请求边界", "128 KiB JSON / 并发闸门 / 90 秒工具超时"],
    ["安全头", "CSP、nosniff、no-referrer、frame-ancestors none"],
    ["隐私边界", "仅同源 API，不包含分析脚本或第三方 tracker"],
    ["工具边界", "后端 operation allowlist，不提供任意 URL / 文件 / 命令"]
  ];

  return (
    <div className="space-y-5">
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>当前会话</CardTitle><CardDescription>Web UI 会话与 MCP OAuth token 独立隔离。</CardDescription></CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center gap-2 text-sm"><span className="text-muted-foreground">当前账号：</span><Badge>{account?.current ?? session.account ?? "root"}</Badge></div>
            {session.expires_at && <div className="text-sm text-muted-foreground">过期时间：{formatBeijing(session.expires_at)}（北京时间）</div>}
            <div className="text-xs text-muted-foreground">版本：{session.version}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>账号上下文</CardTitle><CardDescription>每个账号独立 state、凭据、cookie、watcher 与出口绑定。</CardDescription></CardHeader>
          <CardContent className="flex flex-wrap gap-2">
            {(account?.accounts ?? [{ id: "root", name: "默认账号" }]).map((item) => <Badge key={item.id} variant={item.id === account?.current ? "default" : "secondary"}>{item.name}</Badge>)}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader><CardTitle>安全能力</CardTitle><CardDescription>登录、CSRF、会话、请求边界与工具边界均已内置。</CardDescription></CardHeader>
        <CardContent>
          <div className="grid gap-3 md:grid-cols-2">
            {security.map(([name, value]) => (
              <div key={name} className="rounded-xl border bg-card/70 p-4">
                <div className="text-sm font-medium">{name}</div>
                <div className="mt-1 text-xs text-muted-foreground">{value}</div>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>运行状态</CardTitle><CardDescription>脱敏 runtime 状态，不包含代理 URL、token 或凭据。</CardDescription></CardHeader>
        <CardContent>{status ? <JsonBlock value={status} /> : <div className="text-sm text-muted-foreground">加载中…</div>}</CardContent>
      </Card>
    </div>
  );
}
