import * as React from "react";
import { api, type AccountInfo, type AdminAccount, type SessionInfo } from "../lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { ErrorBanner, JsonBlock, Loading, PageHeader } from "../components/common";
import { formatBeijing } from "../lib/time";
import { roleLabels } from "../lib/roles";
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

  return (
    <div className="space-y-5">
      <PageHeader
        title="安全与配置"
      />
      <div className="grid gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader><CardTitle>当前会话</CardTitle></CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center gap-2 text-sm"><span className="text-muted-foreground">当前账号：</span><Badge>{account ? (account.current || "无账号") : (session.account || "未加载")}</Badge></div>
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="text-muted-foreground">平台成员：</span>
              <Badge variant="secondary">{session.user?.display_name || session.user?.username || "未加载"}</Badge>
              <Badge>{role ? roleLabels[role] : "未加载"}</Badge>
            </div>
            <div className="flex flex-wrap gap-1.5">
              {capabilities.map((item) => <Badge key={item} variant="secondary">{item}</Badge>)}
            </div>
            {session.expires_at && <div className="text-sm text-muted-foreground">过期时间：{formatBeijing(session.expires_at)}（北京时间）</div>}
            <div className="text-xs text-muted-foreground">版本：{session.version || "未知"}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>账号上下文</CardTitle></CardHeader>
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
          </CardHeader>
          <CardContent className="space-y-3">
            <ThemeSelect compact={false} />
          </CardContent>
        </Card>
      </div>

      {session.capabilities?.manage_accounts && (
        <Card>
          <CardHeader>
            <CardTitle>账号运行策略</CardTitle>
          </CardHeader>
          <CardContent>
            {configLoading ? (
              <Loading label="正在读取账号策略" />
            ) : configError ? (
              <ErrorBanner error={configError} />
            ) : accountConfigs.length === 0 ? (
              <div className="text-sm text-muted-foreground">暂无账号策略摘要。</div>
            ) : (
              <>
              <div className="space-y-2 md:hidden">
                {accountConfigs.map((item) => (
                  <article key={item.id} className="rounded-control border border-border bg-card p-3">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="truncate text-[13px] font-medium">{item.name}</div>
                        <div className="truncate font-mono text-[11px] text-muted-foreground">{item.id}</div>
                      </div>
                      <Badge variant={item.watch_enabled ? "success" : "secondary"}>
                        {item.watch_enabled ? "轮询开启" : "轮询关闭"}
                      </Badge>
                    </div>
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      <Badge variant={item.auto_sign ? "warning" : "secondary"}>{item.auto_sign ? "自动签到" : "签到关闭"}</Badge>
                      <Badge variant={item.auto_answer ? "warning" : "secondary"}>{item.auto_answer ? "自动答题" : "答题关闭"}</Badge>
                      {item.notify_only && <Badge variant="success">通知优先</Badge>}
                      <Badge variant={item.location_enabled ? "success" : "secondary"}>
                        {item.location_enabled ? "位置已配置" : "位置未配置"}
                      </Badge>
                    </div>
                    <div className="mt-2 text-[11px] text-muted-foreground">
                      课程 {item.classes} · 显式成员 {item.explicit_members}
                    </div>
                  </article>
                ))}
              </div>
              <div className="hidden md:block">
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
              </div>
              </>
            )}
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader><CardTitle>运行状态</CardTitle></CardHeader>
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
