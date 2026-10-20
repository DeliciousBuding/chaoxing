import * as React from "react";
import { api } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Textarea } from "../components/ui/textarea";
import { EmptyState, ErrorBanner, JsonBlock, Loading, MetaSummary, statusVariant } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export function Activities({ tools }: { tools: any[] }) {
  const [data, setData] = React.useState<any>(null);
  const [detail, setDetail] = React.useState<any>(null);
  const [signTarget, setSignTarget] = React.useState<any>(null);
  const [signForm, setSignForm] = React.useState<Record<string, string>>({});
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try { setData((await api.call("list_activities")).data); }
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
    try {
      const result = await api.call("sign_activity", args);
      setDetail(result.data);
      setSignTarget(null);
      await load();
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
  }

  async function page(row: any) {
    try { setDetail((await api.call("get_activity_page", { course_id: row.course_id, class_id: row.class_id, activity_id: row.id })).data); }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
  }

  React.useEffect(() => { void load(); }, []);
  const rows: any[] = data?.items ?? [];

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <div className="text-sm text-muted-foreground">课堂活动包含签到、抢答、练习与作业入口；写操作均需显式确认。</div>
        <Button size="sm" onClick={() => void load()} disabled={busy}>刷新活动</Button>
      </div>
      <ErrorBanner error={error} />
      <Card>
        <CardHeader><CardTitle>课堂活动</CardTitle><CardDescription>定位、二维码、拍照、人脸等特殊签到需要额外参数，请使用工具面板。</CardDescription></CardHeader>
        <CardContent className="space-y-4">
          {data && <MetaSummary data={data} />}
          {busy && !data ? <Loading /> : rows.length === 0 ? <EmptyState title="当前没有活动" /> : (
            <Table>
              <TableHeader>
                <TableRow><TableHead>课程</TableHead><TableHead>活动</TableHead><TableHead>类型</TableHead><TableHead>签到类型</TableHead><TableHead>状态</TableHead><TableHead>我的状态</TableHead><TableHead>操作</TableHead></TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row, index) => (
                  <React.Fragment key={`${row.id}-${index}`}>
                    <TableRow>
                      <TableCell>{row.course}</TableCell>
                      <TableCell className="font-medium">{row.title}</TableCell>
                      <TableCell><Badge>{row.kind}</Badge></TableCell>
                      <TableCell><Badge variant={row.sign_type === "unknown" ? "secondary" : "warning"}>{row.sign_type}</Badge></TableCell>
                      <TableCell><Badge variant={statusVariant(row.state)}>{row.state}</Badge></TableCell>
                      <TableCell><Badge variant={statusVariant(row.user_state)}>{row.user_state}</Badge></TableCell>
                      <TableCell>
                        <div className="flex gap-2">
                          <Button size="sm" variant="outline" onClick={() => void page(row)}>页面证据</Button>
                          {row.signable && row.user_state === "unsigned" && ["normal_or_photo", "password", "gesture", "location", "qrcode", "photo"].includes(row.sign_type) && (
                            <Button size="sm" onClick={beginSign}>签到</Button>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                    {signTarget?.id === row.id && (
                      <TableRow>
                        <TableCell colSpan={7}>
                          <div className="grid gap-4 rounded-xl border bg-card/60 p-4 md:grid-cols-3">
                            {["password", "gesture"].includes(row.sign_type) && (
                              <div className="space-y-2">
                                <Label>sign_code *</Label>
                                <Input value={signForm.sign_code ?? ""} onChange={(event) => setSignForm({ ...signForm, sign_code: event.target.value })} />
                              </div>
                            )}
                            {row.sign_type === "location" && (
                              <>
                                <div className="space-y-2">
                                  <Label>latitude *</Label>
                                  <Input type="number" step="any" value={signForm.latitude ?? ""} onChange={(event) => setSignForm({ ...signForm, latitude: event.target.value })} />
                                </div>
                                <div className="space-y-2">
                                  <Label>longitude *</Label>
                                  <Input type="number" step="any" value={signForm.longitude ?? ""} onChange={(event) => setSignForm({ ...signForm, longitude: event.target.value })} />
                                </div>
                                <div className="space-y-2">
                                  <Label>address</Label>
                                  <Input value={signForm.address ?? ""} onChange={(event) => setSignForm({ ...signForm, address: event.target.value })} />
                                </div>
                              </>
                            )}
                            {row.sign_type === "qrcode" && (
                              <div className="space-y-2 md:col-span-3">
                                <Label>qr_payload *</Label>
                                <Textarea value={signForm.qr_payload ?? ""} onChange={(event) => setSignForm({ ...signForm, qr_payload: event.target.value })} />
                              </div>
                            )}
                            {(row.sign_type === "photo" || row.sign_type === "normal_or_photo") && (
                              <div className="space-y-2">
                                <Label>photo_object_id{row.sign_type === "photo" ? " *" : ""}</Label>
                                <Input value={signForm.photo_object_id ?? ""} onChange={(event) => setSignForm({ ...signForm, photo_object_id: event.target.value })} />
                              </div>
                            )}
                            <div className="flex items-end gap-2 md:col-span-3">
                              <Button size="sm" disabled={busy} onClick={() => void sign(row)}>确认签到</Button>
                              <Button size="sm" variant="outline" onClick={() => setSignTarget(null)}>取消</Button>
                            </div>
                          </div>
                        </TableCell>
                      </TableRow>
                    )}
                  </React.Fragment>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
      {detail && (
        <Card>
          <CardHeader><CardTitle>活动详情 / 页面证据</CardTitle><CardDescription>仅展示脱敏结构，不包含 hidden value、cookie 或原始 HTML。</CardDescription></CardHeader>
          <CardContent className="space-y-3"><MetaSummary data={detail} /><JsonBlock value={detail} /></CardContent>
        </Card>
      )}
    </div>
  );
}
