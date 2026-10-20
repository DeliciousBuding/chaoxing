import * as React from "react";
import { Activity, Database, Globe2, ShieldCheck } from "lucide-react";
import { api } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { ErrorBanner, JsonBlock, Loading, MetaSummary } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export function Dashboard({ tools }: { tools: any[] }) {
  const [status, setStatus] = React.useState<any>(null);
  const [connection, setConnection] = React.useState<any>(null);
  const [capabilities, setCapabilities] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const [nextStatus, nextConnection, nextCapabilities] = await Promise.all([
        api.status(),
        api.call("get_connection_status"),
        api.call("get_capabilities")
      ]);
      setStatus(nextStatus);
      setConnection(nextConnection.data);
      setCapabilities(nextCapabilities.data);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  React.useEffect(() => { void load(); }, []);

  const runtime = status?.runtime ?? {};
  const egress: any[] = runtime.egress ?? [];
  const accounts: any[] = status?.accounts ?? [];

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap gap-2">
          <Badge variant="success">Web UI 已启用</Badge>
          <Badge>{tools.length} 个受控工具</Badge>
          <Badge variant="secondary">{accounts.length} 个账号上下文</Badge>
        </div>
        <Button size="sm" onClick={() => void load()} disabled={busy}>刷新状态</Button>
      </div>
      <ErrorBanner error={error} />

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-center gap-2 text-primary"><ShieldCheck className="h-4 w-4" /> 安全会话</div>
            <CardTitle className="text-2xl">Active</CardTitle>
            <CardDescription>HttpOnly + Strict + CSRF</CardDescription>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-center gap-2 text-primary"><Globe2 className="h-4 w-4" /> 出口路径</div>
            <CardTitle className="text-2xl">{egress.length}</CardTitle>
            <CardDescription>账号粘性出口已启用</CardDescription>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-center gap-2 text-primary"><Activity className="h-4 w-4" /> 上游连接</div>
            <CardTitle className="text-2xl">{connection?.meta?.status === "complete" ? "正常" : "需检查"}</CardTitle>
            <CardDescription>每次均为新鲜读取</CardDescription>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-center gap-2 text-primary"><Database className="h-4 w-4" /> 能力域</div>
            <CardTitle className="text-2xl">{Array.isArray(capabilities?.items) ? capabilities.items.length : "—"}</CardTitle>
            <CardDescription>以 capabilities 为准，不猜测</CardDescription>
          </CardHeader>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>出口健康</CardTitle>
          <CardDescription>仅显示脱敏 endpoint key，不泄露代理 URL 或凭据。</CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Endpoint</TableHead>
                <TableHead>类型</TableHead>
                <TableHead>失败</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>持久化</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {egress.map((item) => (
                <TableRow key={item.key}>
                  <TableCell className="font-mono text-xs">{item.key}</TableCell>
                  <TableCell>{item.kind}</TableCell>
                  <TableCell>{item.failures}</TableCell>
                  <TableCell>
                    <Badge variant={item.cooldown ? "destructive" : "success"}>{item.cooldown ? "冷却" : "健康"}</Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={item.persistence_degraded || item.assignment_degraded ? "warning" : "secondary"}>
                      {item.persistence_degraded || item.assignment_degraded ? "降级" : "正常"}
                    </Badge>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>连接检查</CardTitle><CardDescription>get_connection_status</CardDescription></CardHeader>
          <CardContent className="space-y-3">
            {connection ? <><MetaSummary data={connection} /><JsonBlock value={connection} /></> : <Loading />}
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>能力边界</CardTitle><CardDescription>get_capabilities</CardDescription></CardHeader>
          <CardContent>{capabilities ? <JsonBlock value={capabilities} /> : <Loading />}</CardContent>
        </Card>
      </div>
    </div>
  );
}
