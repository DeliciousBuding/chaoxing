import * as React from "react";
import { Check, Plus, RefreshCw, Save, ShieldCheck, UserRound } from "lucide-react";
import {
  api,
  type AdminAccount,
  type PlatformUser,
  type Role,
  type SessionInfo
} from "../lib/api";
import { assignableRoles, roleLabels } from "../lib/roles";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Checkbox } from "../components/ui/checkbox";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { EmptyState, ErrorBanner, Loading, PageHeader } from "../components/common";
import { formatBeijing } from "../lib/time";
import { cn } from "../lib/utils";

type FormState = {
  username: string;
  display_name: string;
  password: string;
  role: Role;
  status: "active" | "disabled";
  accounts: string[];
};

const emptyForm: FormState = {
  username: "",
  display_name: "",
  password: "",
  role: "operator",
  status: "active",
  accounts: []
};

function statusVariant(status: string) {
  return status === "active" ? "success" as const : "destructive" as const;
}

export function Members({ session }: { session: SessionInfo }) {
  const actorRole = session.user?.role;
  const [users, setUsers] = React.useState<PlatformUser[]>([]);
  const [accounts, setAccounts] = React.useState<AdminAccount[]>([]);
  const [selected, setSelected] = React.useState<PlatformUser | null>(null);
  const [creating, setCreating] = React.useState(false);
  const [form, setForm] = React.useState<FormState>(emptyForm);
  const [loading, setLoading] = React.useState(true);
  const [membersLoaded, setMembersLoaded] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const [notice, setNotice] = React.useState("");
  const formCardRef = React.useRef<HTMLDivElement | null>(null);
  const formTitleRef = React.useRef<HTMLHeadingElement | null>(null);

  const load = React.useCallback(async () => {
    setLoading(true);
    setError("");
    const [memberResult, accountResult] = await Promise.allSettled([api.members(), api.adminAccounts()]);
    const failures: string[] = [];
    if (memberResult.status === "fulfilled") {
      setUsers(memberResult.value.items);
      setMembersLoaded(true);
    } else {
      failures.push(`成员：${memberResult.reason instanceof Error ? memberResult.reason.message : String(memberResult.reason)}`);
    }
    if (accountResult.status === "fulfilled") {
      setAccounts(accountResult.value.items);
    } else {
      failures.push(`账号：${accountResult.reason instanceof Error ? accountResult.reason.message : String(accountResult.reason)}`);
    }
    if (failures.length > 0) setError(failures.join("；"));
    setLoading(false);
  }, []);

  React.useEffect(() => { void load(); }, [load]);

  function beginCreate() {
    setSelected(null);
    setCreating(true);
    setNotice("");
    setForm(emptyForm);
  }

  function beginEdit(user: PlatformUser) {
    setSelected(user);
    setCreating(false);
    setNotice("");
    setForm({
      username: user.username,
      display_name: user.display_name,
      password: "",
      role: user.role,
      status: user.status,
      accounts: user.accounts ?? []
    });
  }

  const canEdit = React.useCallback((user: PlatformUser) => {
    if (!actorRole) return false;
    if (actorRole === "owner") return true;
    if (actorRole !== "admin") return false;
    return user.id === session.user?.id || user.role === "operator" || user.role === "viewer";
  }, [actorRole, session.user?.id]);

  const roleOptions = actorRole ? assignableRoles[actorRole] : [];
  const targetIsSelf = selected?.id === session.user?.id;
  const targetIsOwner = selected?.role === "owner";
  const roleLocked = targetIsSelf || targetIsOwner || Boolean(selected && !roleOptions.includes(form.role));
  const grantsVisible = form.role === "operator" || form.role === "viewer";

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!creating && selected && !roleLocked && form.role !== selected.role &&
      !window.confirm(`将 ${selected.username} 的角色从“${roleLabels[selected.role]}”改为“${roleLabels[form.role]}”？该成员的现有会话会立即失效。`)) {
      return;
    }
    if (!creating && selected && !targetIsSelf && !targetIsOwner && form.status !== selected.status &&
      !window.confirm(`${form.status === "disabled" ? "禁用" : "启用"}成员 ${selected.username}？${form.status === "disabled" ? "其现有会话会立即失效。" : ""}`)) {
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      let saved: PlatformUser | null = null;
      if (creating) {
        await api.createMember({
          username: form.username.trim(),
          display_name: form.display_name.trim(),
          password: form.password,
          role: form.role,
          accounts: grantsVisible ? form.accounts : []
        });
        setNotice(`成员 ${form.username.trim()} 已创建`);
      } else if (selected) {
        const payload: Record<string, unknown> = {
          display_name: form.display_name.trim()
        };
        if (grantsVisible) {
          payload.accounts = form.accounts;
        } else if (selected.role !== form.role) {
          // A role change to admin/owner makes grants implicit. Clear stale
          // explicit grants so a later downgrade cannot silently restore them.
          payload.accounts = [];
        }
        if (form.password) payload.password = form.password;
        if (!roleLocked && form.role !== selected.role) payload.role = form.role;
        if (!targetIsSelf && !targetIsOwner && form.status !== selected.status) payload.status = form.status;
        saved = await api.updateMember(selected.id, payload);
        setNotice(`成员 ${selected.username} 已更新`);
      }
      await load();
      if (creating) {
        setCreating(false);
        setSelected(null);
        setForm(emptyForm);
      } else if (saved) {
        setSelected(saved);
        setForm({
          username: saved.username,
          display_name: saved.display_name,
          password: "",
          role: saved.role,
          status: saved.status,
          accounts: saved.accounts ?? []
        });
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  function toggleAccount(key: string) {
    setForm((current) => ({
      ...current,
      accounts: current.accounts.includes(key)
        ? current.accounts.filter((item) => item !== key)
        : [...current.accounts, key]
    }));
  }

  const accountName = React.useMemo(() => {
    const map = new Map(accounts.map((account) => [account.id, account.name]));
    return (key: string) => map.get(key) ?? key;
  }, [accounts]);

  const formOpen = creating || Boolean(selected);

  React.useEffect(() => {
    if (!formOpen) return;
    if (window.innerWidth < 1536) {
      formCardRef.current?.scrollIntoView({ block: "start" });
    }
    formTitleRef.current?.focus({ preventScroll: true });
  }, [formOpen, selected?.id, creating]);

  return (
    <div className="space-y-5">
      <PageHeader
        title="平台成员"
        meta={!actorRole ? <Badge variant="warning">权限未加载</Badge> : undefined}
        actions={
          <>
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading || busy}>
            <RefreshCw className="h-4 w-4" /> 刷新
          </Button>
          <Button size="sm" onClick={beginCreate} disabled={!actorRole || roleOptions.length === 0}>
            <Plus className="h-4 w-4" /> 新建成员
          </Button>
          </>
        }
      />
      <ErrorBanner error={error} />
      {notice && (
        <Alert variant="success">
          <Check className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <AlertDescription>{notice}</AlertDescription>
        </Alert>
      )}

      <div className={cn("grid min-w-0 gap-5", formOpen && "xl:grid-cols-[minmax(0,1fr)_390px]")}>
        <Card className="min-w-0">
          <CardHeader>
            <CardTitle>成员列表</CardTitle>
          </CardHeader>
          <CardContent>
            {loading ? <Loading label="正在读取成员" /> : !membersLoaded ? (
              <EmptyState bare title="成员读取失败" />
            ) : users.length === 0 ? (
              <EmptyState bare title="还没有平台成员" />
            ) : (
              <>
              <div className="space-y-2 lg:hidden">
                {users.map((user) => (
                  <article
                    key={user.id}
                    className={cn(
                      "rounded-control border border-border bg-card p-3",
                      selected?.id === user.id && "border-primary/45 bg-accent/70"
                    )}
                  >
                    <div className="flex items-start gap-2.5">
                      <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-primary/10 text-primary">
                        {user.role === "owner" ? <ShieldCheck className="h-4 w-4" /> : <UserRound className="h-4 w-4" />}
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-[13px] font-medium">{user.display_name || user.username}</div>
                        <div className="truncate font-mono text-[11px] text-muted-foreground">{user.username}</div>
                      </div>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => beginEdit(user)}
                        disabled={!canEdit(user)}
                        title={canEdit(user) ? undefined : "只有 owner 可以修改管理员"}
                      >
                        编辑
                      </Button>
                    </div>
                    <div className="mt-3 flex flex-wrap items-center gap-1.5">
                      <Badge variant={user.role === "owner" ? "default" : "secondary"}>{roleLabels[user.role]}</Badge>
                      <Badge variant={statusVariant(user.status)}>{user.status === "active" ? "启用" : "禁用"}</Badge>
                    </div>
                    <div className="mt-3 space-y-1.5 text-[11px] text-muted-foreground">
                      <div>
                        <span className="text-foreground/75">账号范围：</span>
                        {user.role === "owner" || user.role === "admin" ? (
                          <span>全部账号</span>
                        ) : (user.accounts?.length ?? 0) === 0 ? (
                          <span className="text-warning">未授权</span>
                        ) : (
                          <span className="inline-flex flex-wrap gap-1 align-middle">
                            {(user.accounts ?? []).map((key) => <Badge key={key} variant="secondary">{accountName(key)}</Badge>)}
                          </span>
                        )}
                      </div>
                      <div>最近登录：{user.last_login_at ? formatBeijing(user.last_login_at) : "从未登录"}</div>
                    </div>
                  </article>
                ))}
              </div>
              <div className="hidden min-w-0 lg:block">
              <Table label="平台成员" className="min-w-[760px]">
                  <TableHeader>
                    <TableRow>
                      <TableHead>成员</TableHead>
                      <TableHead>角色</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead>账号范围</TableHead>
                      <TableHead>最近登录</TableHead>
                      <TableHead className="text-right">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {users.map((user) => (
                      <TableRow key={user.id} className={selected?.id === user.id ? "bg-accent" : undefined}>
                        <TableCell>
                          <div className="flex items-center gap-2">
                            <div className="flex h-8 w-8 items-center justify-center rounded-control bg-primary/10 text-primary">
                              {user.role === "owner" ? <ShieldCheck className="h-4 w-4" /> : <UserRound className="h-4 w-4" />}
                            </div>
                            <div className="min-w-0">
                              <div className="truncate text-sm font-medium">{user.display_name || user.username}</div>
                              <div className="font-mono text-[11px] text-muted-foreground">{user.username}</div>
                            </div>
                          </div>
                        </TableCell>
                        <TableCell><Badge variant={user.role === "owner" ? "default" : "secondary"}>{roleLabels[user.role]}</Badge></TableCell>
                        <TableCell><Badge variant={statusVariant(user.status)}>{user.status === "active" ? "启用" : "禁用"}</Badge></TableCell>
                        <TableCell>
                          {user.role === "owner" || user.role === "admin" ? (
                            <Badge variant="secondary">全部账号</Badge>
                          ) : (user.accounts?.length ?? 0) === 0 ? (
                            <span className="text-xs text-warning">未授权</span>
                          ) : (
                            <div className="flex max-w-64 flex-wrap gap-1">
                              {(user.accounts ?? []).map((key) => <Badge key={key} variant="secondary">{accountName(key)}</Badge>)}
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground">{user.last_login_at ? formatBeijing(user.last_login_at) : "从未登录"}</TableCell>
                        <TableCell className="text-right">
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => beginEdit(user)}
                            disabled={!canEdit(user)}
                            title={canEdit(user) ? undefined : "只有 owner 可以修改管理员"}
                          >
                            编辑
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
              </Table>
              </div>
              </>
            )}
          </CardContent>
        </Card>

        {formOpen && (
          <Card ref={formCardRef} className="h-fit min-w-0 scroll-mt-24 sm:scroll-mt-16">
            <CardHeader>
              <CardTitle ref={formTitleRef} tabIndex={-1} className="focus:outline-none">{creating ? "新建成员" : `编辑 ${selected?.username}`}</CardTitle>
            </CardHeader>
            <CardContent>
              <form className="space-y-4" onSubmit={submit}>
                <div className="space-y-2">
                  <Label htmlFor="member-username">用户名</Label>
                  <Input id="member-username" aria-describedby="member-username-hint" value={form.username} disabled={!creating} onChange={(event) => setForm({ ...form, username: event.target.value })} required minLength={3} maxLength={32} pattern="[a-z0-9][a-z0-9_.-]{2,31}" autoComplete="off" autoCapitalize="none" autoCorrect="off" spellCheck={false} />
                  <p id="member-username-hint" className="text-[11px] text-muted-foreground">小写字母/数字开头，3–32 位，可用 _ . -</p>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="member-display">显示名</Label>
                  <Input id="member-display" value={form.display_name} onChange={(event) => setForm({ ...form, display_name: event.target.value })} maxLength={64} />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="member-password">{creating ? "初始密码" : "重置密码（留空不改）"}</Label>
                  <Input id="member-password" aria-describedby={targetIsOwner ? "member-password-hint" : undefined} type="password" value={form.password} onChange={(event) => setForm({ ...form, password: event.target.value })} minLength={12} maxLength={1024} required={creating} autoComplete="new-password" disabled={targetIsOwner} />
                  {targetIsOwner && <p id="member-password-hint" className="text-[11px] text-muted-foreground">该账号密码由系统管理员维护。</p>}
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <div className="space-y-2">
                    <Label htmlFor="member-role">角色</Label>
                    <Select id="member-role" value={form.role} disabled={roleLocked} onChange={(event) => setForm({ ...form, role: event.target.value as Role })}>
                      {[...new Set<Role>([form.role, ...roleOptions])].map((role) => <option key={role} value={role}>{roleLabels[role]}</option>)}
                    </Select>
                  </div>
                  {!creating && (
                    <div className="space-y-2">
                      <Label htmlFor="member-status">状态</Label>
                      <Select id="member-status" value={form.status} disabled={targetIsSelf || targetIsOwner} onChange={(event) => setForm({ ...form, status: event.target.value as "active" | "disabled" })}>
                        <option value="active">启用</option>
                        <option value="disabled">禁用</option>
                      </Select>
                    </div>
                  )}
                </div>

                {grantsVisible ? (
                  <fieldset className="space-y-2">
                    <legend className="text-sm font-medium leading-none">账号授权</legend>
                    <div className="space-y-1 rounded-control border border-border bg-muted/25 p-3">
                      {accounts.map((account) => {
                        const checked = form.accounts.includes(account.id);
                        return (
                          <label key={account.id} className="flex min-h-11 cursor-pointer items-center gap-2 rounded-control px-2 py-2.5 text-[13px] hover:bg-muted/60">
                            <Checkbox checked={checked} onChange={() => toggleAccount(account.id)} />
                            <span className="min-w-0 flex-1 truncate">{account.name}</span>
                            {checked && <Check className="h-3.5 w-3.5 text-success" />}
                          </label>
                        );
                      })}
                    </div>
                  </fieldset>
                ) : (
                  <div className="rounded-control border border-primary/20 bg-primary/5 p-3 text-xs text-muted-foreground">
                    管理员隐式拥有全部账号，不需要逐项授权。
                  </div>
                )}

                <div className="flex gap-2">
                  <Button type="submit" disabled={busy || (creating && form.password.length < 12)} className="flex-1">
                    <Save className="h-4 w-4" /> {busy ? "保存中…" : "保存"}
                  </Button>
                  <Button type="button" variant="outline" onClick={() => { setCreating(false); setSelected(null); setNotice(""); }} disabled={busy}>取消</Button>
                </div>
              </form>
            </CardContent>
          </Card>
        )}
      </div>
    </div>
  );
}
