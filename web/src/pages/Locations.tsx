import * as React from "react";
import { api, type LocationBinding, type LocationPreset, type MapConfig, type WatchSchedule } from "../lib/api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
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

const formatCoordinate = (value: number) =>
  Number.isFinite(value) && value !== 0 ? String(value) : "";

function parseCoordinate(text: string, min: number, max: number): number | null {
  if (!text.trim()) return null;
  const value = Number(text);
  return Number.isFinite(value) && value >= min && value <= max ? value : null;
}

function CoordinateField({
  id,
  label,
  value,
  onChange,
  onBlur
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  onBlur: (value: string) => void;
}) {
  return (
    <div className="min-w-0 space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="text"
        inputMode="decimal"
        autoComplete="off"
        spellCheck={false}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onBlur={() => onBlur(value)}
      />
    </div>
  );
}

export function Locations() {
  const [config, setConfig] = React.useState<MapConfig>({ enabled: false });
  const [presets, setPresets] = React.useState<LocationPreset[]>([]);
  const [presetsLoaded, setPresetsLoaded] = React.useState(false);
  const [bindings, setBindings] = React.useState<LocationBinding[]>([]);
  const [bindingsLoaded, setBindingsLoaded] = React.useState(false);
  const [courses, setCourses] = React.useState<CourseRow[]>([]);
  const [schedules, setSchedules] = React.useState<WatchSchedule[]>([]);
  const [form, setForm] = React.useState<LocationPreset>(emptyPreset);
  const [coordinateText, setCoordinateText] = React.useState({ latitude: "", longitude: "" });
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

  const updateCoordinates = React.useCallback((
    latitude: number,
    longitude: number,
    extra: Partial<LocationPreset> = {}
  ) => {
    setForm((current) => ({ ...current, ...extra, latitude, longitude }));
    setCoordinateText({
      latitude: formatCoordinate(latitude),
      longitude: formatCoordinate(longitude)
    });
  }, []);

  const commitCoordinate = React.useCallback((axis: "latitude" | "longitude", text: string) => {
    const parsed = axis === "latitude"
      ? parseCoordinate(text, -90, 90)
      : parseCoordinate(text, -180, 180);
    if (parsed === null) {
      setCoordinateText((current) => ({
        ...current,
        [axis]: formatCoordinate(form[axis])
      }));
      return;
    }
    setForm((current) => ({ ...current, [axis]: parsed }));
  }, [form.latitude, form.longitude]);

  async function reload() {
    setBusy(true);
    setError("");
    const [mapResult, presetResult, bindingResult, courseResult, scheduleResult] = await Promise.allSettled([
      api.mapConfig(),
      api.locations(),
      api.locationBindings(),
      api.call("list_courses"),
      api.watchSchedules()
    ]);
    const failures: string[] = [];
    if (mapResult.status === "fulfilled") setConfig(mapResult.value);
    else failures.push(`地图：${mapResult.reason instanceof Error ? mapResult.reason.message : String(mapResult.reason)}`);
    if (presetResult.status === "fulfilled") {
      setPresets(presetResult.value.items ?? []);
      setPresetsLoaded(true);
    } else {
      failures.push(`位置：${presetResult.reason instanceof Error ? presetResult.reason.message : String(presetResult.reason)}`);
    }
    if (bindingResult.status === "fulfilled") {
      setBindings(bindingResult.value.items ?? []);
      setBindingsLoaded(true);
    } else {
      failures.push(`绑定：${bindingResult.reason instanceof Error ? bindingResult.reason.message : String(bindingResult.reason)}`);
    }
    if (courseResult.status === "fulfilled") {
      setCourses(((courseResult.value.data as any)?.result ?? []) as CourseRow[]);
    } else {
      failures.push(`课程：${courseResult.reason instanceof Error ? courseResult.reason.message : String(courseResult.reason)}`);
    }
    if (scheduleResult.status === "fulfilled") {
      setSchedules(scheduleResult.value.items ?? []);
    } else {
      failures.push(`课表：${scheduleResult.reason instanceof Error ? scheduleResult.reason.message : String(scheduleResult.reason)}`);
    }
    if (failures.length > 0) setError(failures.join("；"));
    setBusy(false);
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
        updateCoordinates(latitude, longitude, { coordinate_system: "bd09" });
        return;
      }
      if (message.type === "error") {
        setError(typeof message.message === "string" ? message.message : "百度地图加载失败");
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [sendMapInit, updateCoordinates]);

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
    const latitude = parseCoordinate(coordinateText.latitude, -90, 90);
    const longitude = parseCoordinate(coordinateText.longitude, -180, 180);
    if (latitude === null || longitude === null) {
      setError("请先选择或输入有效经纬度");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api.reverseGeocode(latitude, longitude, form.coordinate_system ?? "bd09");
      updateCoordinates(latitude, longitude, { address: result.address });
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
      updateCoordinates(result.latitude, result.longitude, { address, coordinate_system: "bd09" });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }

  async function savePreset() {
    if (!form.label.trim()) {
      setError("位置名称不能为空");
      return;
    }
    const latitude = parseCoordinate(coordinateText.latitude, -90, 90);
    const longitude = parseCoordinate(coordinateText.longitude, -180, 180);
    if (latitude === null || longitude === null) {
      setError("纬度需在 -90..90、经度需在 -180..180，且不能为空");
      return;
    }
    if (latitude === 0 && longitude === 0) {
      setError("请选择有效坐标，不能保存 0,0");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const saved = await api.saveLocation({ ...form, latitude, longitude, id: editingId ?? undefined });
      setForm({ ...saved });
      setCoordinateText({
        latitude: formatCoordinate(saved.latitude),
        longitude: formatCoordinate(saved.longitude)
      });
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
    setCoordinateText({ latitude: "", longitude: "" });
    setEditingId(null);
    setSelectedPreset(null);
    setError("");
  }

  function editPreset(preset: LocationPreset) {
    setForm({ ...preset });
    setCoordinateText({
      latitude: formatCoordinate(preset.latitude),
      longitude: formatCoordinate(preset.longitude)
    });
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
        title="位置"
        actions={<Button size="sm" variant="outline" onClick={() => void reload()} disabled={busy}>刷新</Button>}
      />
      <ErrorBanner error={error} />
      <div className="grid min-w-0 gap-4 xl:grid-cols-[380px_1fr]">
        <div className="min-w-0 space-y-4">
          <Card className="min-w-0">
            <CardHeader>
              <CardTitle>地图选点</CardTitle>
              <Badge variant={editingId ? "warning" : "secondary"}>
                {editingId ? `编辑中：${form.label || "未命名位置"}` : "新建位置"}
              </Badge>
            </CardHeader>
            <CardContent className="space-y-4">
              {config.enabled ? (
                <div className="space-y-3">
                  <div className="flex gap-2">
                    <Input aria-label="地址搜索" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="输入地址搜索" />
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
                    <CoordinateField
                      id="location-latitude"
                      label="纬度"
                      value={coordinateText.latitude}
                      onChange={(value) => setCoordinateText((current) => ({ ...current, latitude: value }))}
                      onBlur={(value) => commitCoordinate("latitude", value)}
                    />
                    <CoordinateField
                      id="location-longitude"
                      label="经度"
                      value={coordinateText.longitude}
                      onChange={(value) => setCoordinateText((current) => ({ ...current, longitude: value }))}
                      onBlur={(value) => commitCoordinate("longitude", value)}
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="location-coordinate-system">输入坐标系</Label>
                    <Select id="location-coordinate-system" value={form.coordinate_system ?? "bd09"} onChange={(event) => setForm({ ...form, coordinate_system: event.target.value })}>
                      <option value="bd09">BD-09（百度地图）</option>
                      <option value="gcj02">GCJ-02（高德/火星）</option>
                      <option value="wgs84">WGS-84（GPS）</option>
                    </Select>
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="location-label">位置名称</Label>
                    <Input id="location-label" value={form.label} onChange={(event) => setForm({ ...form, label: event.target.value })} />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="location-address">地址</Label>
                    <Input id="location-address" value={form.address} onChange={(event) => setForm({ ...form, address: event.target.value })} />
                  </div>
                  <div className="flex gap-2">
                    <Button size="sm" onClick={() => void reverseGeocode()} disabled={busy}>反查地址</Button>
                    <Button size="sm" onClick={() => void savePreset()} disabled={busy}>{editingId ? "更新位置" : "保存位置"}</Button>
                  </div>
                </div>
              ) : (
                <div className="space-y-3">
                  <EmptyState bare title="地图未启用" />
                  <div className="grid grid-cols-2 gap-3">
                    <CoordinateField
                      id="location-latitude"
                      label="纬度"
                      value={coordinateText.latitude}
                      onChange={(value) => setCoordinateText((current) => ({ ...current, latitude: value }))}
                      onBlur={(value) => commitCoordinate("latitude", value)}
                    />
                    <CoordinateField
                      id="location-longitude"
                      label="经度"
                      value={coordinateText.longitude}
                      onChange={(value) => setCoordinateText((current) => ({ ...current, longitude: value }))}
                      onBlur={(value) => commitCoordinate("longitude", value)}
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="location-coordinate-system">输入坐标系</Label>
                    <Select id="location-coordinate-system" value={form.coordinate_system ?? "bd09"} onChange={(event) => setForm({ ...form, coordinate_system: event.target.value })}>
                      <option value="bd09">BD-09（百度地图）</option>
                      <option value="gcj02">GCJ-02（高德/火星）</option>
                      <option value="wgs84">WGS-84（GPS）</option>
                    </Select>
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="location-label">位置名称</Label>
                    <Input id="location-label" value={form.label} onChange={(event) => setForm({ ...form, label: event.target.value })} />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="location-address">地址</Label>
                    <Input id="location-address" value={form.address} onChange={(event) => setForm({ ...form, address: event.target.value })} />
                  </div>
                  <Button size="sm" onClick={() => void savePreset()} disabled={busy}>{editingId ? "更新位置" : "保存位置"}</Button>
                </div>
              )}
            </CardContent>
          </Card>

          <Card className="min-w-0">
            <CardHeader>
              <CardTitle>绑定课程</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="space-y-2">
                <Label htmlFor="location-bind-preset">当前位置</Label>
                <Select id="location-bind-preset" value={selectedPreset?.id ? String(selectedPreset.id) : ""} onChange={(event) => {
                  const preset = presets.find((item) => item.id === Number(event.target.value)) ?? null;
                  setSelectedPreset(preset);
                }}>
                  <option value="">请选择</option>
                  {presets.map((preset) => <option key={preset.id} value={preset.id}>{preset.label}</option>)}
                </Select>
              </div>
              <div className="space-y-2">
                <Label htmlFor="location-bind-course">课程</Label>
                <Select id="location-bind-course" value={selectedCourse ? String(courses.findIndex((item) => item.course_id === selectedCourse.course_id && item.class_id === selectedCourse.class_id)) : ""} onChange={(event) => {
                  const index = Number(event.target.value);
                  setSelectedCourse(Number.isInteger(index) && index >= 0 ? courses[index] ?? null : null);
                  setScheduleKey("");
                }}>
                  <option value="">请选择</option>
                  {courses.map((course, index) => <option key={`${course.course_id}:${course.class_id}`} value={String(index)}>{course.name} / {course.class ?? course.class_id}</option>)}
                </Select>
              </div>
              <div className="space-y-2">
                <Label htmlFor="location-bind-schedule">独立时段（可选）</Label>
                {availableSchedules.length > 0 ? (
                  <Select id="location-bind-schedule" value={scheduleKey} onChange={(event) => setScheduleKey(event.target.value)}>
                    <option value="">课程默认（未单独绑定的时段）</option>
                    {availableSchedules.map((item) => <option key={item.key} value={item.key}>{scheduleLabel(item)}</option>)}
                    {scheduleKey && !selectedSchedule ? <option value={scheduleKey}>{scheduleKey}（历史配置）</option> : null}
                  </Select>
                ) : (
                  <Input id="location-bind-schedule" value={scheduleKey} onChange={(event) => setScheduleKey(event.target.value)} placeholder="例如 arch-tue-guidance" />
                )}
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

        <div className="min-w-0 space-y-4">
          <Card className="min-w-0">
            <CardHeader>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <CardTitle>位置预设</CardTitle>
                <Button size="sm" variant="outline" onClick={startNewPreset} disabled={busy}>新建位置</Button>
              </div>
            </CardHeader>
            <CardContent>
              {busy && !presetsLoaded ? <Loading /> : !presetsLoaded ? <EmptyState bare title="位置读取失败" /> : presets.length === 0 ? <EmptyState bare title="暂无位置" /> : (
                <>
                  <div className="space-y-2 lg:hidden">
                    {presets.map((preset) => (
                      <article key={preset.id} className="rounded-control border border-border bg-muted/15 p-3">
                        <div className="flex items-start justify-between gap-2">
                          <div className="min-w-0">
                            <div className="font-medium">{preset.label}</div>
                            <div className="mt-1 truncate text-[11px] text-muted-foreground">{preset.address || "未填写地址"}</div>
                          </div>
                          <Badge variant="secondary">{preset.coordinate_system === "bd09" ? "BD-09" : preset.coordinate_system ?? "BD-09"}</Badge>
                        </div>
                        <div className="mt-2 font-mono text-[11px] text-muted-foreground">
                          {preset.latitude.toFixed(6)}, {preset.longitude.toFixed(6)}
                        </div>
                        <div className="mt-3 flex gap-2">
                          <Button size="sm" variant="outline" className="flex-1" onClick={() => editPreset(preset)}>编辑</Button>
                          <Button size="sm" variant="outline" className="flex-1" onClick={() => void removePreset(preset)}>删除</Button>
                        </div>
                      </article>
                    ))}
                  </div>
                  <div className="hidden lg:block">
                    <Table label="位置预设">
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
                            <TableCell className="sticky right-0 z-10 border-l border-border bg-card lg:static lg:border-l-0 lg:bg-transparent">
                              <div className="flex gap-2">
                                <Button size="sm" variant="outline" onClick={() => editPreset(preset)}>编辑</Button>
                                <Button size="sm" variant="outline" onClick={() => void removePreset(preset)}>删除</Button>
                              </div>
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

          <Card className="min-w-0">
            <CardHeader>
              <CardTitle>课程绑定</CardTitle>
            </CardHeader>
            <CardContent>
              {busy && !bindingsLoaded ? <Loading /> : !bindingsLoaded ? <EmptyState bare title="绑定读取失败" /> : bindingRows.length === 0 ? <EmptyState bare title="暂无绑定" /> : (
                <>
                  <div className="space-y-2 lg:hidden">
                    {bindingRows.map(({ binding, preset, course, schedule }) => (
                      <article key={`${binding.course_id}:${binding.class_id}:${binding.schedule_key ?? ""}`} className="rounded-control border border-border bg-muted/15 p-3">
                        <div className="flex items-start justify-between gap-2">
                          <div className="min-w-0">
                            <div className="font-medium">{course?.name ?? binding.course_id}</div>
                            <div className="mt-1 text-[11px] text-muted-foreground">{course?.class ?? binding.class_id}</div>
                          </div>
                          <Badge>{preset?.label ?? "已删除"}</Badge>
                        </div>
                        <div className="mt-2 font-mono text-[11px] text-muted-foreground">{binding.schedule_key || "课程默认"}</div>
                        {schedule && <div className="mt-1 text-[11px] text-muted-foreground">{scheduleLabel(schedule)}</div>}
                        <Button size="sm" variant="outline" className="mt-3 w-full" onClick={() => void unbind(binding)}>解除</Button>
                      </article>
                    ))}
                  </div>
                  <div className="hidden lg:block">
                    <Table label="课程位置绑定">
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
                            <TableCell className="sticky right-0 z-10 border-l border-border bg-card lg:static lg:border-l-0 lg:bg-transparent"><Button size="sm" variant="outline" onClick={() => void unbind(binding)}>解除</Button></TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                </>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
