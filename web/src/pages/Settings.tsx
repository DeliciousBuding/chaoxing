import * as React from "react";
import { api, type AccountInfo, type AdminAccount, type SessionInfo } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { JsonBlock } from "../components/common";
import { formatBeijing } from "../lib/time";
import { roleDescriptions, roleLabels } from "../lib/roles";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export function Settings({ account, session }: { account: AccountInfo | null; session: SessionInfo }) {
  const [status, setStatus] = React.useState<any>(null);
  const [accountConfigs, setAccountConfigs] = React.useState<AdminAccount[]>([]);
  React.useEffect(() => { api.status().then(setStatus).catch(() => undefined); }, []);
  React.useEffect(() => {
    if (session.capabilities?.manage_accounts) {
      api.adminAccounts().then((value) => setAccountConfigs(value.items)).catch(() => undefined);
    }
  }, [session.capabilities?.manage_accounts]);

  const role = session.user?.role ?? "owner";
  const capabilities = [
    session.capabilities?.manage_users && "成员管理",
    session.capabilities?.view_audit && "审计日志",
    session.capabilities?.manage_accounts && "账号配置",
    session.capabilities?.sensitive_read && "捕获/签到审计",
    ...(session.capabilities?.tool_scopes ?? [])
  ].filter(Boolean) as string[];

  const security = [
    ["密码", "PBKDF2-SHA256 / 600,000 iterations"],
    ["平台身份", "owner / admin / operator / viewer 四档角色 + 账号授权"],
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
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="text-muted-foreground">平台成员：</span>
              <Badge variant="secondary">{session.user?.display_name || session.user?.username || "owner"}</Badge>
              <Badge>{roleLabels[role]}</Badge>
            </div>
            <div className="text-xs leading-relaxed text-muted-foreground">{roleDescriptions[role]}</div>
            <div className="flex flex-wrap gap-1.5">
              {capabilities.map((item) => <Badge key={item} variant="secondary">{item}</Badge>)}
            </div>
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

      {session.capabilities?.manage_accounts && (
        <Card>
          <CardHeader>
            <CardTitle>账号运行策略</CardTitle>
            <CardDescription>脱敏只读摘要。上游凭据、代理、通知 token、位置坐标仍只存在于私有部署配置，不通过浏览器回显。</CardDescription>
          </CardHeader>
          <CardContent>
            {accountConfigs.length === 0 ? (
              <div className="text-sm text-muted-foreground">暂无账号策略摘要。</div>
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>账号</TableHead>
                      <TableHead>Watcher</TableHead>
                      <TableHead>自动签到</TableHead>
                      <TableHead>自动答题</TableHead>
                      <TableHead>课堂仅通知</TableHead>
                      <TableHead>位置</TableHead>
                      <TableHead>课程</TableHead>
                      <TableHead>显式成员</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {accountConfigs.map((item) => (
                      <TableRow key={item.id}>
                        <TableCell>
                          <div className="text-sm">{item.name}</div>
                          <div className="font-mono text-[11px] text-muted-foreground">{item.id}</div>
                        </TableCell>
                        <TableCell><Badge variant={item.watch_enabled ? "success" : "secondary"}>{item.watch_enabled ? "启用" : "关闭"}</Badge></TableCell>
                        <TableCell><Badge variant={item.auto_sign ? "warning" : "secondary"}>{item.auto_sign ? "启用" : "关闭"}</Badge></TableCell>
                        <TableCell><Badge variant={item.auto_answer ? "warning" : "secondary"}>{item.auto_answer ? "启用" : "关闭"}</Badge></TableCell>
                        <TableCell><Badge variant={item.notify_only ? "success" : "destructive"}>{item.notify_only ? "是" : "否"}</Badge></TableCell>
                        <TableCell>{item.location_enabled ? "已配置" : "未启用"}</TableCell>
                        <TableCell>{item.classes}</TableCell>
                        <TableCell>{item.explicit_members}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
            <p className="mt-3 text-xs leading-relaxed text-muted-foreground">
              修改这些运行策略仍走配置文件、CLI 和明确重启；本阶段不在浏览器里改写密钥或运行时 JSON，避免把配置回滚和密钥泄露风险带进 UI。
            </p>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader><CardTitle>运行状态</CardTitle><CardDescription>脱敏 runtime 状态，不包含代理 URL、token 或凭据。</CardDescription></CardHeader>
        <CardContent>{status ? <JsonBlock value={status} /> : <div className="text-sm text-muted-foreground">加载中…</div>}</CardContent>
      </Card>
    </div>
  );
}
