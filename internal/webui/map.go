package webui

import "net/http"

// mapFrameHTML is intentionally a separate, sandboxed document. The map SDK
// never runs in the authenticated application origin: it receives only an AK
// and a selected point over postMessage and cannot read cookies or the CSRF
// token from the parent document.
const mapFrameHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>地图选点</title>
<style>
html,body,#map{height:100%;margin:0;background:#0f172a}
body{font:13px system-ui,sans-serif;color:#e2e8f0}
.hint{position:absolute;z-index:2;left:10px;top:10px;padding:6px 9px;border-radius:8px;background:rgba(15,23,42,.78);pointer-events:none}
</style>
</head>
<body>
<div class="hint">点击或拖拽地图标记选择签到位置</div>
<div id="map"></div>
<script>
(() => {
  "use strict";
  const parentWindow = window.parent;
  let map = null;
  let marker = null;
  let circle = null;
  let point = null;
  let started = false;

  const send = (type, value = {}) => {
    try { parentWindow.postMessage(Object.assign({source: "chaoxing-map", type}, value), "*"); } catch (_) {}
  };

  const updateOverlays = (next) => {
    if (!map || !next || !Number.isFinite(next.latitude) || !Number.isFinite(next.longitude)) return;
    const BMapGL = window.BMapGL;
    if (!BMapGL) return;
    point = new BMapGL.Point(next.longitude, next.latitude);
    if (!marker) {
      marker = new BMapGL.Marker(point);
      marker.enableDragging();
      marker.addEventListener("dragend", (event) => {
        const dragged = event.latlng || event.point;
        if (!dragged || !Number.isFinite(dragged.lat) || !Number.isFinite(dragged.lng)) return;
        send("point", {latitude: dragged.lat, longitude: dragged.lng});
      });
      map.addOverlay(marker);
    } else {
      marker.setPosition(point);
    }
    if (!circle) {
      circle = new BMapGL.Circle(point, 5, {
        strokeColor: "#38bdf8", fillColor: "#38bdf8", fillOpacity: 0.18, strokeWeight: 1
      });
      map.addOverlay(circle);
    } else {
      circle.setCenter(point);
    }
  };

  const startMap = (init) => {
    if (started || !init || !init.ak) return;
    started = true;
    const script = document.createElement("script");
    script.src = "https://api.map.baidu.com/api?v=1.0&type=webgl&ak=" + encodeURIComponent(init.ak);
    script.async = true;
    script.onload = () => {
      const BMapGL = window.BMapGL;
      if (!BMapGL) return send("error", {message: "百度地图脚本未初始化"});
      const lat = Number(init.center_latitude) || 39.9042;
      const lng = Number(init.center_longitude) || 116.4074;
      map = new BMapGL.Map(document.getElementById("map"));
      map.centerAndZoom(new BMapGL.Point(lng, lat), Number(init.zoom) || 17);
      map.enableScrollWheelZoom(true);
      map.addEventListener("click", (event) => {
        const selected = event.latlng || event.point;
        if (!selected || !Number.isFinite(selected.lat) || !Number.isFinite(selected.lng)) return;
        updateOverlays({latitude: selected.lat, longitude: selected.lng});
        send("point", {latitude: selected.lat, longitude: selected.lng});
      });
      if (init.latitude && init.longitude) updateOverlays(init);
      send("ready");
    };
    script.onerror = () => send("error", {message: "百度地图脚本加载失败"});
    document.head.appendChild(script);
  };

  window.addEventListener("message", (event) => {
    if (event.source !== parentWindow) return;
    const message = event.data;
    if (!message || message.source !== "chaoxing-parent") return;
    if (message.type === "init") startMap(message);
    if (message.type === "point") updateOverlays(message);
    if (message.type === "resize" && map) map.resize();
  });

  send("frame-ready");
})();
</script>
</body>
</html>`

func (s *Server) mapFrame(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "origin")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval' https://api.map.baidu.com https://mapopen.baidu.com; style-src 'unsafe-inline' https://api.map.baidu.com; img-src data: blob: https://map.baidu.com https://*.bdimg.com https://*.bdstatic.com https://api.map.baidu.com; font-src data: https://*.bdstatic.com; connect-src https://api.map.baidu.com https://mapopen.baidu.com; worker-src blob: https://api.map.baidu.com; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	_, _ = w.Write([]byte(mapFrameHTML))
}
