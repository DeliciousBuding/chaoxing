import * as React from "react";
import { api, type LocationBinding, type LocationPreset, type MapConfig, type WatchSchedule } from "../lib/api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { EmptyState, ErrorBanner, Loading, PageHeader } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

type CourseRow = {
  course_id: string;
  class_id: string;
  name: string;
  class?: string;
  teacher?: string;
};

const emptyPreset = (): LocationPreset => ({
  label: "",
  address: "",
  latitude: 0,
  longitude: 0,
  coordinate_system: "bd09"
});

export function Locations() {
  const [config, setConfig] = React.useState<MapConfig>({ enabled: false });
  const [presets, setPresets] = React.useState<LocationPreset[]>([]);
  const [bindings, setBindings] = React.useState<LocationBinding[]>([]);
  const [courses, setCourses] = React.useState<CourseRow[]>([]);
  const [schedules, setSchedules] = React.useState<WatchSchedule[]>([]);
  const [form, setForm] = React.useState<LocationPreset>(emptyPreset);
  const [editingId, setEditingId] = React.useState<number | null>(null);
  const [selectedPreset, setSelectedPreset] = React.useState<LocationPreset | null>(null);
  const [selectedCourse, setSelectedCourse] = React.useState<CourseRow | null>(null);
  const [scheduleKey, setScheduleKey] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const iframeRef = React.useRef<HTMLIFrameElement | null>(null);
  const frameReadyRef = React.useRef(false);
  const mapInitializedRef = React.useRef(false);

  const postMap = React.useCallback((message: Record<string, unknown>) => {
    iframeRef.current?.contentWindow?.postMessage({ source: "chaoxing-parent", ...message }, "*");
  }, []);

  const sendMapInit = React.useCallback(() => {
    if (!config.enabled || !config.ak || mapInitializedRef.current) return;
    const initial = Number.isFinite(form.latitude) && Number.isFinite(form.longitude) && (form.latitude !== 0 || form.longitude !== 0)
      ? { latitude: form.latitude, longitude: form.longitude }
      : {};
    postMap({
      type: "init",
      ak: config.ak,
      center_latitude: config.center_latitude,
      center_longitude: config.center_longitude,
      zoom: config.zoom,
      ...initial
    });
    mapInitializedRef.current = true;
  }, [config.ak, config.center_latitude, config.center_longitude, config.enabled, config.zoom, form.latitude, form.longitude, postMap]);

  async function reload() {
    setBusy(true);
    setError("");
    try {
      const [mapValue, presetValue, bindingValue, courseValue, scheduleValue] = await Promise.all([
        api.mapConfig(),
        api.locations(),
        api.locationBindings(),
        api.call("list_courses"),
        api.watchSchedules()
      ]);
      setConfig(mapValue);
      setPresets(presetValue.items ?? []);
      setBindings(bindingValue.items ?? []);
      setSchedules(scheduleValue.items ?? []);
      const rows = ((courseValue.data as any)?.result ?? []) as CourseRow[];
      setCourses(rows);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  React.useEffect(() => { void reload(); }, []);

  React.useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      if (event.source !== iframeRef.current?.contentWindow) return;
      const message = event.data;
      if (!message || message.source !== "chaoxing-map") return;
      if (message.type === "frame-ready") {
        frameReadyRef.current = true;
        sendMapInit();
        return;
      }
      if (message.type === "point") {
        const latitude = Number(message.latitude);
        const longitude = Number(message.longitude);
        if (!Number.isFinite(latitude) || !Number.isFinite(longitude)) return;
        setForm((current) => ({ ...current, latitude, longitude, coordinate_system: "bd09" }));
        return;
      }
      if (message.type === "error") {
        setError(typeof message.message === "string" ? message.message : "百度地图加载失败");
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [sendMapInit]);

  React.useEffect(() => {
    if (!frameReadyRef.current) return;
    sendMapInit();
  }, [sendMapInit]);

  React.useEffect(() => {
    if (!frameReadyRef.current || !Number.isFinite(form.latitude) || !Number.isFinite(form.longitude)) return;
    if (form.latitude === 0 && form.longitude === 0) return;
    postMap({ type: "point", latitude: form.latitude, longitude: form.longitude });
  }, [form.latitude, form.longitude, postMap]);

  async function reverseGeocode() {
    if (!Number.isFinite(form.latitude) || !Number.isFinite(form.longitude)) {
      setError("请先选择或输入有效经纬度");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api.reverseGeocode(form.latitude, form.longitude, form.coordinate_system ?? "bd09");
      setForm((current) => ({ ...current, address: result.address }));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }

  async function geocode() {
    const address = search.trim();
    if (!address) {
      setError("请输入地址");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api.geocode(address);
      setForm((current) => ({ ...current, latitude: result.latitude, longitude: result.longitude, address, coordinate_system: "bd09" }));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }

  async function savePreset() {
    if (!form.label.trim()) {
      setError("位置名称不能为空");
      return;
    }
    if (!Number.isFinite(form.latitude) || !Number.isFinite(form.longitude)) {
      setError("经纬度无效");
      return;
    }
    if (form.latitude === 0 && form.longitude === 0) {
      setError("请选择有效坐标，不能保存 0,0");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const saved = await api.saveLocation({ ...form, id: editingId ?? undefined });
      setForm({ ...saved });
      setEditingId(saved.id ?? null);
      setSelectedPreset(saved);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }

  async function removePreset(preset: LocationPreset) {
    if (!preset.id) return;
    if (!window.confirm(`删除位置“${preset.label}”？绑定了它的课程也会一起失效。`)) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteLocation(preset.id);
      if (selectedPreset?.id === preset.id) setSelectedPreset(null);
      if (editingId === preset.id) startNewPreset();
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }

  function startNewPreset() {
    setForm(emptyPreset());
    setEditingId(null);
    setSelectedPreset(null);
    setError("");
  }

  function editPreset(preset: LocationPreset) {
    setForm({ ...preset });
    setEditingId(preset.id ?? null);
    setSelectedPreset(preset);
    setError("");
  }

  async function bindCourse() {
    if (!selectedPreset?.id || !selectedCourse) {
      setError("请先选择位置和课程");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.saveLocationBinding({
        course_id: selectedCourse.course_id,
        class_id: selectedCourse.class_id,
        preset_id: selectedPreset.id,
        schedule_key: scheduleKey.trim()
      });
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }

  async function unbind(binding: LocationBinding) {
    if (!window.confirm(`解除 ${binding.course_id} / ${binding.class_id} 的位置绑定？`)) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteLocationBinding(binding);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }

  const bindingRows = bindings.map((binding) => {
    const preset = presets.find((item) => item.id === binding.preset_id);
    const course = courses.find((item) => item.course_id === binding.course_id && item.class_id === binding.class_id);
    const schedule = schedules.find((item) =>
      item.course_id === binding.course_id &&
      item.class_id === binding.class_id &&
      item.key === binding.schedule_key
    );
    return { binding, preset, course, schedule };
  });
  const availableSchedules = selectedCourse
    ? schedules.filter((item) => item.course_id === selectedCourse.course_id && item.class_id === selectedCourse.class_id)
    : [];
  const selectedSchedule = availableSchedules.find((item) => item.key === scheduleKey);
  const scheduleLabel = (item: WatchSchedule) => {
    const weeks = item.weeks ? ` · 第${item.weeks}周` : " · 每周";
    const room = item.room ? ` · ${item.room}` : "";
    return `${item.days.join("/")} ${item.start}-${item.end}${weeks}${room}`;
  };

  return (
    <div className="space-y-5">
      <PageHeader
        title="位置预设"
        description="位置预设只保存在生产 SQLite；百度地图在隔离沙箱 iframe 中加载，不能读取登录 Cookie 或 CSRF token。"
        meta={<><Badge variant="secondary">{presets.length} 个位置</Badge><Badge variant="secondary">{bindings.length} 个课程/时段绑定</Badge><span>坐标不出浏览器凭据范围</span></>}
        actions={<Button size="sm" variant="outline" onClick={() => void reload()} disabled={busy}>刷新</Button>}
      />
      <ErrorBanner error={error} />
      <div className="grid gap-4 xl:grid-cols-[380px_1fr]">
        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>地图选点</CardTitle>
              <CardDescription>地图点击与拖拽使用 BD-09；手输支持 BD-09 / GCJ-02 / WGS-84，保存时统一归一化为 BD-09。</CardDescription>
              <Badge variant={editingId ? "warning" : "secondary"}>
                {editingId ? `编辑中：${form.label || "未命名位置"}` : "新建位置"}
              </Badge>
            </CardHeader>
            <CardContent className="space-y-4">
              {config.enabled ? (
                <div className="space-y-3">
                  <div className="flex gap-2">
                    <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="输入地址搜索" />
                    <Button size="sm" onClick={() => void geocode()} disabled={busy}>搜索</Button>
                  </div>
                  <iframe
                    ref={iframeRef}
                    title="地图选点"
                    src="/map"
                    sandbox="allow-scripts"
                    referrerPolicy="origin"
                    onLoad={() => { frameReadyRef.current = true; mapInitializedRef.current = false; sendMapInit(); }}
                    className="h-[360px] w-full overflow-hidden rounded-control border border-border bg-muted"
                  />
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                      <Label>纬度</Label>
                      <Input type="number" inputMode="decimal" min={-90} max={90} step="any" value={form.latitude} onChange={(event) => setForm({ ...form, latitude: Number(event.target.value) })} />
                    </div>
                    <div className="space-y-2">
                      <Label>经度</Label>
                      <Input type="number" inputMode="decimal" min={-180} max={180} step="any" value={form.longitude} onChange={(event) => setForm({ ...form, longitude: Number(event.target.value) })} />
                    </div>
                  </div>
                  <div className="space-y-2">
                    <Label>输入坐标系</Label>
                    <Select value={form.coordinate_system ?? "bd09"} onChange={(event) => setForm({ ...form, coordinate_system: event.target.value })}>
                      <option value="bd09">BD-09（百度地图）</option>
                      <option value="gcj02">GCJ-02（高德/火星）</option>
                      <option value="wgs84">WGS-84（GPS）</option>
                    </Select>
                  </div>
                  <div className="space-y-2">
                    <Label>位置名称</Label>
                    <Input value={form.label} onChange={(event) => setForm({ ...form, label: event.target.value })} />
                  </div>
                  <div className="space-y-2">
                    <Label>地址</Label>
                    <Input value={form.address} onChange={(event) => setForm({ ...form, address: event.target.value })} />
                  </div>
                  <div className="flex gap-2">
                    <Button size="sm" onClick={() => void reverseGeocode()} disabled={busy}>反查地址</Button>
                    <Button size="sm" onClick={() => void savePreset()} disabled={busy}>{editingId ? "更新位置" : "保存位置"}</Button>
                  </div>
                </div>
              ) : (
                <div className="space-y-3">
                  <EmptyState title="地图未启用" description="在私有配置里启用 location 并填入百度 AK 后可使用地图选点。" />
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                      <Label>纬度</Label>
                      <Input type="number" inputMode="decimal" min={-90} max={90} step="any" value={form.latitude} onChange={(event) => setForm({ ...form, latitude: Number(event.target.value) })} />
                    </div>
                    <div className="space-y-2">
                      <Label>经度</Label>
                      <Input type="number" inputMode="decimal" min={-180} max={180} step="any" value={form.longitude} onChange={(event) => setForm({ ...form, longitude: Number(event.target.value) })} />
                    </div>
                  </div>
                  <div className="space-y-2">
                    <Label>输入坐标系</Label>
                    <Select value={form.coordinate_system ?? "bd09"} onChange={(event) => setForm({ ...form, coordinate_system: event.target.value })}>
                      <option value="bd09">BD-09（百度地图）</option>
                      <option value="gcj02">GCJ-02（高德/火星）</option>
                      <option value="wgs84">WGS-84（GPS）</option>
                    </Select>
                  </div>
                  <div className="space-y-2">
                    <Label>位置名称</Label>
                    <Input value={form.label} onChange={(event) => setForm({ ...form, label: event.target.value })} />
                  </div>
                  <div className="space-y-2">
                    <Label>地址</Label>
                    <Input value={form.address} onChange={(event) => setForm({ ...form, address: event.target.value })} />
                  </div>
                  <Button size="sm" onClick={() => void savePreset()} disabled={busy}>{editingId ? "更新位置" : "保存位置"}</Button>
                </div>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>绑定课程</CardTitle>
              <CardDescription>选中的位置会用于该课程的定位签到；可选时段绑定优先于课程级绑定。</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="space-y-2">
                <Label>当前位置</Label>
                <Select value={selectedPreset?.id ? String(selectedPreset.id) : ""} onChange={(event) => {
                  const preset = presets.find((item) => item.id === Number(event.target.value)) ?? null;
                  setSelectedPreset(preset);
                }}>
                  <option value="">请选择</option>
                  {presets.map((preset) => <option key={preset.id} value={preset.id}>{preset.label}</option>)}
                </Select>
              </div>
              <div className="space-y-2">
                <Label>课程</Label>
                <Select value={selectedCourse ? String(courses.findIndex((item) => item.course_id === selectedCourse.course_id && item.class_id === selectedCourse.class_id)) : ""} onChange={(event) => {
                  const index = Number(event.target.value);
                  setSelectedCourse(Number.isInteger(index) && index >= 0 ? courses[index] ?? null : null);
                  setScheduleKey("");
                }}>
                  <option value="">请选择</option>
                  {courses.map((course, index) => <option key={`${course.course_id}:${course.class_id}`} value={String(index)}>{course.name} / {course.class ?? course.class_id}</option>)}
                </Select>
              </div>
              <div className="space-y-2">
                <Label>独立时段（可选）</Label>
                {availableSchedules.length > 0 ? (
                  <Select value={scheduleKey} onChange={(event) => setScheduleKey(event.target.value)}>
                    <option value="">课程默认（未单独绑定的时段）</option>
                    {availableSchedules.map((item) => <option key={item.key} value={item.key}>{scheduleLabel(item)}</option>)}
                    {scheduleKey && !selectedSchedule ? <option value={scheduleKey}>{scheduleKey}（历史配置）</option> : null}
                  </Select>
                ) : (
                  <Input value={scheduleKey} onChange={(event) => setScheduleKey(event.target.value)} placeholder="例如 arch-tue-guidance" />
                )}
                <div className="text-xs text-muted-foreground">
                  每个时段可以绑定不同位置。例如体系结构周二普通课用复临舍，第 7/13 周指导课独立绑定中楼 303。
                </div>
                {selectedSchedule && (
                  <div className="rounded-control border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
                    稳定 key：<span className="font-mono text-foreground">{selectedSchedule.key}</span>
                    {selectedSchedule.room ? ` · 教室 ${selectedSchedule.room}` : ""}
                    {selectedSchedule.weeks ? ` · 第 ${selectedSchedule.weeks} 周` : " · 每周"}
                  </div>
                )}
              </div>
              <Button size="sm" onClick={() => void bindCourse()} disabled={busy || !selectedPreset || !selectedCourse}>绑定</Button>
            </CardContent>
          </Card>
        </div>

        <div className="space-y-4">
          <Card>
            <CardHeader>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <CardTitle>位置预设</CardTitle>
                <Button size="sm" variant="outline" onClick={startNewPreset} disabled={busy}>新建位置</Button>
              </div>
              <CardDescription>保存在生产数据库，不进入仓库。</CardDescription>
            </CardHeader>
            <CardContent>
              {busy && presets.length === 0 ? <Loading /> : presets.length === 0 ? <EmptyState title="暂无位置" /> : (
                <Table>
                  <TableHeader>
                    <TableRow><TableHead>名称</TableHead><TableHead>地址</TableHead><TableHead>纬度</TableHead><TableHead>经度</TableHead><TableHead>坐标系</TableHead><TableHead>操作</TableHead></TableRow>
                  </TableHeader>
                  <TableBody>
                    {presets.map((preset) => (
                      <TableRow key={preset.id}>
                        <TableCell className="font-medium">{preset.label}</TableCell>
                        <TableCell>{preset.address || "—"}</TableCell>
                        <TableCell className="font-mono text-xs">{preset.latitude.toFixed(6)}</TableCell>
                        <TableCell className="font-mono text-xs">{preset.longitude.toFixed(6)}</TableCell>
                          <TableCell><Badge variant="secondary">{preset.coordinate_system === "bd09" ? "BD-09" : preset.coordinate_system ?? "BD-09"}</Badge></TableCell>
                        <TableCell>
                          <div className="flex gap-2">
                            <Button size="sm" variant="outline" onClick={() => editPreset(preset)}>编辑</Button>
                            <Button size="sm" variant="outline" onClick={() => void removePreset(preset)}>删除</Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>课程绑定</CardTitle>
              <CardDescription>自动签到时按课程解析位置。</CardDescription>
            </CardHeader>
            <CardContent>
              {busy && bindingRows.length === 0 ? <Loading /> : bindingRows.length === 0 ? <EmptyState title="暂无绑定" /> : (
                <Table>
                  <TableHeader>
                    <TableRow><TableHead>课程</TableHead><TableHead>班级</TableHead><TableHead>时段</TableHead><TableHead>位置</TableHead><TableHead>操作</TableHead></TableRow>
                  </TableHeader>
                  <TableBody>
                    {bindingRows.map(({ binding, preset, course, schedule }) => (
                      <TableRow key={`${binding.course_id}:${binding.class_id}:${binding.schedule_key ?? ""}`}>
                        <TableCell>{course?.name ?? binding.course_id}</TableCell>
                        <TableCell>{course?.class ?? binding.class_id}</TableCell>
                        <TableCell className="text-xs">
                          <div className="font-mono">{binding.schedule_key || "课程默认"}</div>
                          {schedule && <div className="mt-1 text-muted-foreground">{scheduleLabel(schedule)}</div>}
                        </TableCell>
                        <TableCell><Badge>{preset?.label ?? "已删除"}</Badge></TableCell>
                        <TableCell><Button size="sm" variant="outline" onClick={() => void unbind(binding)}>解除</Button></TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
