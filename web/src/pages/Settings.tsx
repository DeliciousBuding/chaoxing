import * as React from "react";
import { api, type AccountInfo, type AdminAccount, type SessionInfo } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { ErrorBanner, JsonBlock, Loading, PageHeader } from "../components/common";
import { formatBeijing } from "../lib/time";
import { roleDescriptions, roleLabels } from "../lib/roles";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { ThemeSelect } from "../components/app-shell";

export function Settings({ account, session }: { account: AccountInfo | null; session: SessionInfo }) {
  const [status, setStatus] = React.useState<any>(null);
  const [statusError, setStatusError] = React.useState("");
  const [accountConfigs, setAccountConfigs] = React.useState<AdminAccount[]>([]);
  const [configError, setConfigError] = React.useState("");
  const [configLoading, setConfigLoading] = React.useState(false);
  React.useEffect(() => {
    api.status().then(setStatus).catch((err) => setStatusError(err instanceof Error ? err.message : String(err)));
  }, []);
  React.useEffect(() => {
    if (session.capabilities?.manage_accounts) {
      setConfigLoading(true);
      setConfigError("");
      api.adminAccounts()
        .then((value) => setAccountConfigs(value.items))
        .catch((err) => setConfigError(err instanceof Error ? err.message : String(err)))
        .finally(() => setConfigLoading(false));
    }
  }, [session.capabilities?.manage_accounts]);

  const role = session.user?.role;
  const controlHealth = status?.control ?? {};
  const auditFailures = Number(controlHealth.audit_write_failures ?? 0);
  const auditStreak = Number(controlHealth.audit_consecutive_write_failures ?? 0);
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
      <PageHeader
        title="安全与配置"
        description="当前身份、控制面健康与脱敏运行策略；密钥和上游凭据不会进入浏览器。"
        meta={<><span>角色权限</span><span>·</span><span>会话边界</span><span>·</span><span>审计健康</span></>}
      />
      <div className="grid gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader><CardTitle>当前会话</CardTitle><CardDescription>Web UI 会话与 MCP OAuth token 独立隔离。</CardDescription></CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center gap-2 text-sm"><span className="text-muted-foreground">当前账号：</span><Badge>{account ? (account.current || "无账号") : (session.account || "未加载")}</Badge></div>
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="text-muted-foreground">平台成员：</span>
              <Badge variant="secondary">{session.user?.display_name || session.user?.username || "未加载"}</Badge>
              <Badge>{role ? roleLabels[role] : "未加载"}</Badge>
            </div>
            {role && <div className="text-xs leading-relaxed text-muted-foreground">{roleDescriptions[role]}</div>}
            <div className="flex flex-wrap gap-1.5">
              {capabilities.map((item) => <Badge key={item} variant="secondary">{item}</Badge>)}
            </div>
            {session.expires_at && <div className="text-sm text-muted-foreground">过期时间：{formatBeijing(session.expires_at)}（北京时间）</div>}
            <div className="text-xs text-muted-foreground">版本：{session.version || "未知"}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>账号上下文</CardTitle><CardDescription>每个账号独立 state、凭据、cookie、watcher 与出口绑定。</CardDescription></CardHeader>
          <CardContent className="flex flex-wrap gap-2">
            {!account ? (
              <span className="text-sm text-muted-foreground">账号范围未加载。</span>
            ) : account.accounts.length === 0 ? (
              <span className="text-sm text-warning">尚无学习通账号授权。</span>
            ) : account.accounts.map((item) => <Badge key={item.id} variant={item.id === account.current ? "default" : "secondary"}>{item.name}</Badge>)}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>外观</CardTitle>
            <CardDescription>深色优先；主题只改变中性色与对比度，不改变权限边界。</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <ThemeSelect compact={false} />
            <p className="text-[11px] leading-5 text-muted-foreground">
              选择会保存在当前浏览器。高对比主题适合强光环境，仍使用同一套语义令牌。
            </p>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader><CardTitle>安全基线</CardTitle><CardDescription>代码内置的登录、CSRF、会话与请求边界；具体监听和部署参数以运行状态为准。</CardDescription></CardHeader>
        <CardContent>
          <div className="grid gap-3 md:grid-cols-2">
            {security.map(([name, value]) => (
              <div key={name} className="rounded-control border border-border bg-muted/20 p-4">
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
            {configLoading ? (
              <Loading label="正在读取账号策略" />
            ) : configError ? (
              <ErrorBanner error={configError} />
            ) : accountConfigs.length === 0 ? (
              <div className="text-sm text-muted-foreground">暂无账号策略摘要。</div>
            ) : (
              <Table label="账号运行策略" className="min-w-[820px]">
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
                        <TableCell><Badge variant={item.notify_only ? "success" : "warning"}>{item.notify_only ? "是" : "否"}</Badge></TableCell>
                        <TableCell>{item.location_enabled ? "已配置" : "未启用"}</TableCell>
                        <TableCell>{item.classes}</TableCell>
                        <TableCell>{item.explicit_members}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
              </Table>
            )}
            <p className="mt-3 text-xs leading-relaxed text-muted-foreground">
              修改这些运行策略仍走配置文件、CLI 和明确重启；本阶段不在浏览器里改写密钥或运行时 JSON，避免把配置回滚和密钥泄露风险带进 UI。
            </p>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader><CardTitle>运行状态</CardTitle><CardDescription>脱敏 runtime 状态，不包含代理 URL、token 或凭据。</CardDescription></CardHeader>
        <CardContent>
          {status ? (
            <div className="space-y-3">
              <div className="flex flex-wrap gap-2">
                <Badge variant={auditStreak > 0 ? "destructive" : "success"}>
                  审计写入{auditStreak > 0 ? `连续失败 ${auditStreak}` : "正常"}
                </Badge>
                <Badge variant="secondary">累计失败 {auditFailures}</Badge>
                {controlHealth.audit_last_write_failure_at && (
                  <Badge variant="warning">最近失败 {formatBeijing(controlHealth.audit_last_write_failure_at)}</Badge>
                )}
              </div>
              <details className="rounded-control border border-border bg-muted/20">
                <summary className="cursor-pointer px-4 py-3 text-xs font-medium text-muted-foreground">查看原始 runtime 数据</summary>
                <div className="px-3 pb-3"><JsonBlock value={status} /></div>
              </details>
            </div>
          ) : statusError ? <ErrorBanner error={statusError} /> : <div className="text-sm text-muted-foreground">加载中…</div>}
        </CardContent>
      </Card>
    </div>
  );
}
