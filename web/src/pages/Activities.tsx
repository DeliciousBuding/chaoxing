import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Textarea } from "../components/ui/textarea";
import { EmptyState, ErrorBanner, JsonBlock, Loading, MetaSummary, PageHeader, statusVariant } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";
import { labelFor } from "../lib/labels";

const signableTypes = ["normal_or_photo", "password", "gesture", "location", "qrcode", "photo"];

function canSignRow(row: any) {
  return Boolean(row.signable && row.user_state === "unsigned" && signableTypes.includes(row.sign_type));
}

function SignFields({
  row,
  values,
  onChange,
  busy,
  idPrefix,
  onConfirm,
  onCancel
}: {
  row: any;
  values: Record<string, string>;
  onChange: (next: Record<string, string>) => void;
  busy: boolean;
  idPrefix: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const update = (key: string, value: string) => onChange({ ...values, [key]: value });
  return (
    <>
      {["password", "gesture"].includes(row.sign_type) && (
        <div className="min-w-0 space-y-2">
          <Label htmlFor={`${idPrefix}-code`}>sign_code *</Label>
          <Input id={`${idPrefix}-code`} value={values.sign_code ?? ""} onChange={(event) => update("sign_code", event.target.value)} />
        </div>
      )}
      {row.sign_type === "location" && (
        <>
          <div className="min-w-0 space-y-2">
            <Label htmlFor={`${idPrefix}-latitude`}>latitude *</Label>
            <Input id={`${idPrefix}-latitude`} type="number" inputMode="decimal" step="any" value={values.latitude ?? ""} onChange={(event) => update("latitude", event.target.value)} />
          </div>
          <div className="min-w-0 space-y-2">
            <Label htmlFor={`${idPrefix}-longitude`}>longitude *</Label>
            <Input id={`${idPrefix}-longitude`} type="number" inputMode="decimal" step="any" value={values.longitude ?? ""} onChange={(event) => update("longitude", event.target.value)} />
          </div>
          <div className="min-w-0 space-y-2">
            <Label htmlFor={`${idPrefix}-address`}>address</Label>
            <Input id={`${idPrefix}-address`} value={values.address ?? ""} onChange={(event) => update("address", event.target.value)} />
          </div>
        </>
      )}
      {row.sign_type === "qrcode" && (
        <div className="min-w-0 space-y-2 md:col-span-3">
          <Label htmlFor={`${idPrefix}-qr`}>qr_payload *</Label>
          <Textarea id={`${idPrefix}-qr`} value={values.qr_payload ?? ""} onChange={(event) => update("qr_payload", event.target.value)} />
        </div>
      )}
      {(row.sign_type === "photo" || row.sign_type === "normal_or_photo") && (
        <div className="min-w-0 space-y-2">
          <Label htmlFor={`${idPrefix}-photo`}>photo_object_id{row.sign_type === "photo" ? " *" : ""}</Label>
          <Input id={`${idPrefix}-photo`} value={values.photo_object_id ?? ""} onChange={(event) => update("photo_object_id", event.target.value)} />
        </div>
      )}
      <div className="flex flex-wrap items-end gap-2 md:col-span-3">
        <Button size="sm" disabled={busy} onClick={onConfirm}>确认签到</Button>
        <Button size="sm" variant="outline" onClick={onCancel}>取消</Button>
      </div>
    </>
  );
}

export function Activities({ tools }: { tools: any[] }) {
  const [data, setData] = React.useState<any>(null);
  const [detail, setDetail] = React.useState<any>(null);
  const [signTarget, setSignTarget] = React.useState<any>(null);
  const [signForm, setSignForm] = React.useState<Record<string, string>>({});
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try {
      setData((await api.call("list_activities")).data);
      setLoaded(true);
    }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  function beginSign(row: any) {
    setSignTarget(row);
    setError("");
    setSignForm({ sign_code: "", latitude: "", longitude: "", address: "", qr_payload: "", photo_object_id: "" });
  }

  async function sign(row: any) {
    const args: Record<string, any> = {
      course_id: row.course_id,
      class_id: row.class_id,
      activity_id: row.id,
    };
    const code = signForm.sign_code?.trim() ?? "";
    const latitude = Number(signForm.latitude);
    const longitude = Number(signForm.longitude);
    const hasLatitude = signForm.latitude?.trim() !== "";
    const hasLongitude = signForm.longitude?.trim() !== "";
    const address = signForm.address?.trim() ?? "";
    const qrPayload = signForm.qr_payload?.trim() ?? "";
    const photoObjectID = signForm.photo_object_id?.trim() ?? "";

    if (["password", "gesture"].includes(row.sign_type) && !code) {
      setError("密码 / 手势签到必须填写 sign_code");
      return;
    }
    if (row.sign_type === "location" && (!hasLatitude || !hasLongitude || !Number.isFinite(latitude) || !Number.isFinite(longitude))) {
      setError("定位签到必须填写有效经纬度");
      return;
    }
    if (row.sign_type === "qrcode" && !qrPayload) {
      setError("二维码签到必须填写原始二维码内容");
      return;
    }
    if (row.sign_type === "photo" && !photoObjectID) {
      setError("拍照签到必须填写已上传文件的 object_id");
      return;
    }
    if (code) args.sign_code = code;
    if (row.sign_type === "location") {
      args.latitude = latitude;
      args.longitude = longitude;
      if (address) args.address = address;
    }
    if (qrPayload) args.qr_payload = qrPayload;
    if (photoObjectID) args.photo_object_id = photoObjectID;

    if (!window.confirm(`确认提交活动 ${row.id} 的签到？该操作会写入学习通。`)) return;
    setBusy(true);
    setError("");
    try {
      const result = await api.call("sign_activity", args);
      setDetail(result.data);
      setSignTarget(null);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function page(row: any) {
    try { setDetail((await api.call("get_activity_page", { course_id: row.course_id, class_id: row.class_id, activity_id: row.id })).data); }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
  }

  React.useEffect(() => { void load(); }, []);
  const rows: any[] = data?.items ?? [];

  return (
    <div className="space-y-5">
      <PageHeader
        title="课堂活动"
        actions={<Button size="sm" variant="outline" onClick={() => void load()} disabled={busy}>刷新活动</Button>}
      />
      <ErrorBanner error={error} />
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3 space-y-0">
          <CardTitle>活动流</CardTitle>
          {data && <MetaSummary data={data} />}
        </CardHeader>
        <CardContent className="space-y-4">
          {busy && !data ? <Loading /> : !loaded ? null : rows.length === 0 ? <EmptyState title="当前没有活动" /> : (
            <>
            <div className="space-y-3 lg:hidden">
              {rows.map((row, index) => (
                <article key={`${row.id}-${index}`} className="overflow-hidden rounded-card border border-border bg-card">
                  <div className="p-3">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="truncate text-[11px] text-muted-foreground">{row.course}</div>
                        <h2 className="mt-1 text-[14px] font-medium leading-5">{row.title}</h2>
                      </div>
                      <Badge variant={statusVariant(row.user_state)} title={row.user_state}>{labelFor("userState", row.user_state)}</Badge>
                    </div>
                    <div className="mt-3 flex flex-wrap items-center gap-1.5">
                      <Badge title={row.kind}>{labelFor("activityKind", row.kind)}</Badge>
                      <Badge variant={row.sign_type === "unknown" ? "secondary" : "warning"} title={row.sign_type}>
                        {labelFor("signType", row.sign_type)}
                      </Badge>
                      <Badge variant={statusVariant(row.state)} title={row.state}>{labelFor("activityState", row.state)}</Badge>
                    </div>
                    <div className="mt-3 flex gap-2">
                      <Button size="sm" variant="outline" className="flex-1" onClick={() => void page(row)}>页面证据</Button>
                      {canSignRow(row) && (
                        <Button size="sm" className="flex-1" onClick={() => beginSign(row)}>签到</Button>
                      )}
                    </div>
                  </div>
                  {signTarget?.id === row.id && (
                    <div className="border-t border-border bg-muted/15 p-3">
                      <div className="grid min-w-0 gap-3 sm:grid-cols-2 md:grid-cols-3">
                        <SignFields
                          row={row}
                          values={signForm}
                          onChange={setSignForm}
                          busy={busy}
                          idPrefix={`mobile-sign-${row.id}`}
                          onConfirm={() => void sign(row)}
                          onCancel={() => setSignTarget(null)}
                        />
                      </div>
                    </div>
                  )}
                </article>
              ))}
            </div>
            <div className="hidden lg:block">
            <Table label="课堂活动">
              <TableHeader>
                <TableRow><TableHead>课程</TableHead><TableHead>活动</TableHead><TableHead>类型</TableHead><TableHead>签到类型</TableHead><TableHead>状态</TableHead><TableHead>我的状态</TableHead><TableHead>操作</TableHead></TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row, index) => (
                  <React.Fragment key={`${row.id}-${index}`}>
                    <TableRow>
                      <TableCell>{row.course}</TableCell>
                      <TableCell className="font-medium">{row.title}</TableCell>
                      <TableCell><Badge title={row.kind}>{labelFor("activityKind", row.kind)}</Badge></TableCell>
                      <TableCell>
                        <Badge variant={row.sign_type === "unknown" ? "secondary" : "warning"} title={row.sign_type}>
                          {labelFor("signType", row.sign_type)}
                        </Badge>
                      </TableCell>
                      <TableCell><Badge variant={statusVariant(row.state)} title={row.state}>{labelFor("activityState", row.state)}</Badge></TableCell>
                      <TableCell><Badge variant={statusVariant(row.user_state)} title={row.user_state}>{labelFor("userState", row.user_state)}</Badge></TableCell>
                      <TableCell className="sticky right-0 z-10 border-l border-border bg-card lg:static lg:border-l-0 lg:bg-transparent">
                        <div className="flex gap-2">
                          <Button size="sm" variant="outline" onClick={() => void page(row)}>页面证据</Button>
                          {canSignRow(row) && (
                            <Button size="sm" onClick={() => beginSign(row)}>签到</Button>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                    {signTarget?.id === row.id && (
                      <TableRow>
                        <TableCell colSpan={7}>
                          <div className="grid min-w-0 gap-4 rounded-control border border-border bg-muted/20 p-4 md:grid-cols-3">
                            <SignFields
                              row={row}
                              values={signForm}
                              onChange={setSignForm}
                              busy={busy}
                              idPrefix={`desktop-sign-${row.id}`}
                              onConfirm={() => void sign(row)}
                              onCancel={() => setSignTarget(null)}
                            />
                          </div>
                        </TableCell>
                      </TableRow>
                    )}
                  </React.Fragment>
                ))}
              </TableBody>
            </Table>
            </div>
            </>
          )}
        </CardContent>
      </Card>
      {detail && (
        <Card>
          <CardHeader><CardTitle>活动详情</CardTitle></CardHeader>
          <CardContent className="space-y-3"><MetaSummary data={detail} /><JsonBlock value={detail} /></CardContent>
        </Card>
      )}
    </div>
  );
}
