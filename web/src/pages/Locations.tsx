import * as React from "react";
import { api, type LocationBinding, type LocationPreset, type MapConfig } from "../lib/api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { EmptyState, ErrorBanner, Loading } from "../components/common";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

type CourseRow = {
  course_id: string;
  class_id: string;
  name: string;
  class?: string;
  teacher?: string;
};

function loadBaiduMap(ak: string): Promise<any> {
  return new Promise((resolve, reject) => {
    const globalWindow = window as any;
    if (globalWindow.BMapGL) {
      resolve(globalWindow.BMapGL);
      return;
    }
    const script = document.createElement("script");
    script.src = `https://api.map.baidu.com/api?v=1.0&type=webgl&ak=${encodeURIComponent(ak)}`;
    script.async = true;
    script.onload = () => {
      const started = Date.now();
      const timer = window.setInterval(() => {
        if (globalWindow.BMapGL) {
          window.clearInterval(timer);
          resolve(globalWindow.BMapGL);
        } else if (Date.now() - started > 10000) {
          window.clearInterval(timer);
          reject(new Error("百度地图脚本加载超时"));
        }
      }, 50);
    };
    script.onerror = () => reject(new Error("百度地图脚本加载失败"));
    document.head.appendChild(script);
  });
}

export function Locations() {
  const [config, setConfig] = React.useState<MapConfig>({ enabled: false });
  const [presets, setPresets] = React.useState<LocationPreset[]>([]);
  const [bindings, setBindings] = React.useState<LocationBinding[]>([]);
  const [courses, setCourses] = React.useState<CourseRow[]>([]);
  const [form, setForm] = React.useState<LocationPreset>({
    label: "",
    address: "",
    latitude: 0,
    longitude: 0,
    coordinate_system: "bd09"
  });
  const [selectedPreset, setSelectedPreset] = React.useState<LocationPreset | null>(null);
  const [selectedCourse, setSelectedCourse] = React.useState<CourseRow | null>(null);
  const [scheduleKey, setScheduleKey] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const containerRef = React.useRef<HTMLDivElement | null>(null);
  const mapRef = React.useRef<any>(null);
  const markerRef = React.useRef<any>(null);
  const circleRef = React.useRef<any>(null);

  async function reload() {
    setBusy(true);
    setError("");
    try {
      const [mapValue, presetValue, bindingValue, courseValue] = await Promise.all([
        api.mapConfig(),
        api.locations(),
        api.locationBindings(),
        api.call("list_courses")
      ]);
      setConfig(mapValue);
      setPresets(presetValue.items ?? []);
      setBindings(bindingValue.items ?? []);
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
    if (!config.enabled || !config.ak || !containerRef.current || mapRef.current) return;
    let cancelled = false;
    loadBaiduMap(config.ak).then((BMapGL) => {
      if (cancelled || !containerRef.current) return;
      const latitude = config.center_latitude || 39.9042;
      const longitude = config.center_longitude || 116.4074;
      const map = new BMapGL.Map(containerRef.current);
      map.centerAndZoom(new BMapGL.Point(longitude, latitude), config.zoom || 17);
      map.enableScrollWheelZoom(true);
      map.addEventListener("click", (event: any) => {
        const point = event.latlng ?? event.point;
        if (!point || typeof point.lat !== "number" || typeof point.lng !== "number") return;
        setForm((current) => ({ ...current, latitude: point.lat, longitude: point.lng }));
      });
      mapRef.current = map;
    }).catch((err) => {
      if (!cancelled) setError(err instanceof Error ? err.message : String(err));
    });
    return () => { cancelled = true; };
  }, [config.enabled, config.ak, config.center_latitude, config.center_longitude, config.zoom]);

  React.useEffect(() => {
    if (!mapRef.current || !Number.isFinite(form.latitude) || !Number.isFinite(form.longitude)) return;
    const BMapGL = (window as any).BMapGL;
    if (!BMapGL) return;
    const point = new BMapGL.Point(form.longitude, form.latitude);
    if (!markerRef.current) {
      markerRef.current = new BMapGL.Marker(point);
      markerRef.current.enableDragging();
      markerRef.current.addEventListener("dragend", (event: any) => {
        const dragged = event.latlng ?? event.point;
        if (!dragged) return;
        setForm((current) => ({ ...current, latitude: dragged.lat, longitude: dragged.lng }));
      });
      mapRef.current.addOverlay(markerRef.current);
    } else {
      markerRef.current.setPosition(point);
    }
    if (!circleRef.current) {
      circleRef.current = new BMapGL.Circle(point, 5, {
        strokeColor: "#38bdf8",
        fillColor: "#38bdf8",
        fillOpacity: 0.18,
        strokeWeight: 1
      });
      mapRef.current.addOverlay(circleRef.current);
    } else {
      circleRef.current.setCenter(point);
    }
  }, [form.latitude, form.longitude]);

  async function reverseGeocode() {
    if (!Number.isFinite(form.latitude) || !Number.isFinite(form.longitude)) {
      setError("请先选择或输入有效经纬度");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api.reverseGeocode(form.latitude, form.longitude);
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
      setForm((current) => ({ ...current, latitude: result.latitude, longitude: result.longitude, address }));
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
    setBusy(true);
    setError("");
    try {
      const saved = await api.saveLocation(form);
      setForm({ ...saved });
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
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
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
    return { binding, preset, course };
  });

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="text-sm text-muted-foreground">
          位置预设只保存在生产 SQLite；百度地图仅在你打开本页时加载。
        </div>
        <Button size="sm" onClick={() => void reload()} disabled={busy}>刷新</Button>
      </div>
      <ErrorBanner error={error} />
      <div className="grid gap-4 xl:grid-cols-[380px_1fr]">
        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>地图选点</CardTitle>
              <CardDescription>点击地图或拖拽 marker 选择位置，坐标按 BD-09 保存。</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              {config.enabled ? (
                <div className="space-y-3">
                  <div className="flex gap-2">
                    <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="输入地址搜索" />
                    <Button size="sm" onClick={() => void geocode()} disabled={busy}>搜索</Button>
                  </div>
                  <div ref={containerRef} className="h-[360px] w-full overflow-hidden rounded-xl border bg-muted" />
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                      <Label>纬度</Label>
                      <Input type="number" step="any" value={form.latitude} onChange={(event) => setForm({ ...form, latitude: Number(event.target.value) })} />
                    </div>
                    <div className="space-y-2">
                      <Label>经度</Label>
                      <Input type="number" step="any" value={form.longitude} onChange={(event) => setForm({ ...form, longitude: Number(event.target.value) })} />
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
                    <Button size="sm" onClick={() => void savePreset()} disabled={busy}>保存位置</Button>
                  </div>
                </div>
              ) : (
                <div className="space-y-3">
                  <EmptyState title="地图未启用" description="在私有配置里启用 location 并填入百度 AK 后可使用地图选点。" />
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                      <Label>纬度</Label>
                      <Input type="number" step="any" value={form.latitude} onChange={(event) => setForm({ ...form, latitude: Number(event.target.value) })} />
                    </div>
                    <div className="space-y-2">
                      <Label>经度</Label>
                      <Input type="number" step="any" value={form.longitude} onChange={(event) => setForm({ ...form, longitude: Number(event.target.value) })} />
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
                  <Button size="sm" onClick={() => void savePreset()} disabled={busy}>保存位置</Button>
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
                  if (preset) setForm({ ...preset });
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
                }}>
                  <option value="">请选择</option>
                  {courses.map((course, index) => <option key={`${course.course_id}:${course.class_id}`} value={String(index)}>{course.name} / {course.class ?? course.class_id}</option>)}
                </Select>
              </div>
              <div className="space-y-2">
                <Label>时段键（可选）</Label>
                <Input value={scheduleKey} onChange={(event) => setScheduleKey(event.target.value)} placeholder="例如 Tue-08:00-09:40" />
                <div className="text-xs text-muted-foreground">留空表示课程级默认位置；填写后仅在该 watch 时段使用此位置。</div>
              </div>
              <Button size="sm" onClick={() => void bindCourse()} disabled={busy || !selectedPreset || !selectedCourse}>绑定</Button>
            </CardContent>
          </Card>
        </div>

        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>位置预设</CardTitle>
              <CardDescription>保存在生产数据库，不进入仓库。</CardDescription>
            </CardHeader>
            <CardContent>
              {busy && presets.length === 0 ? <Loading /> : presets.length === 0 ? <EmptyState title="暂无位置" /> : (
                <Table>
                  <TableHeader>
                    <TableRow><TableHead>名称</TableHead><TableHead>地址</TableHead><TableHead>纬度</TableHead><TableHead>经度</TableHead><TableHead>操作</TableHead></TableRow>
                  </TableHeader>
                  <TableBody>
                    {presets.map((preset) => (
                      <TableRow key={preset.id}>
                        <TableCell className="font-medium">{preset.label}</TableCell>
                        <TableCell>{preset.address || "—"}</TableCell>
                        <TableCell className="font-mono text-xs">{preset.latitude.toFixed(6)}</TableCell>
                        <TableCell className="font-mono text-xs">{preset.longitude.toFixed(6)}</TableCell>
                        <TableCell>
                          <div className="flex gap-2">
                            <Button size="sm" variant="outline" onClick={() => { setSelectedPreset(preset); setForm({ ...preset }); }}>编辑</Button>
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
                    {bindingRows.map(({ binding, preset, course }) => (
                      <TableRow key={`${binding.course_id}:${binding.class_id}:${binding.schedule_key ?? ""}`}>
                        <TableCell>{course?.name ?? binding.course_id}</TableCell>
                        <TableCell>{course?.class ?? binding.class_id}</TableCell>
                        <TableCell className="font-mono text-xs">{binding.schedule_key || "课程默认"}</TableCell>
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
