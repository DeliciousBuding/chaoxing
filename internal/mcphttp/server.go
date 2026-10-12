// Package mcphttp implements a stateless, JSON-only Streamable HTTP MCP endpoint.
// Mount behind access.Server.Protect; it has no administration or login methods.
package mcphttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxBody = 64 << 10
const maxResult = 2 << 20

type field struct {
	kind             string
	minimum, maximum int
	required         bool
}
type tool struct {
	Name            string           `json:"name"`
	Description     string           `json:"description"`
	InputSchema     map[string]any   `json:"inputSchema"`
	Annotations     map[string]any   `json:"annotations"`
	SecuritySchemes []map[string]any `json:"securitySchemes"`
	Meta            map[string]any   `json:"_meta"`
	fields          map[string]field
}

func makeTool(name, description string, fields map[string]field) tool {
	props := map[string]any{}
	required := []string{}
	// Stable order makes tool descriptions and schema snapshots reproducible.
	for _, key := range []string{"course", "status", "limit", "offset", "id", "week", "course_id", "class_id", "cpi", "folder_id"} {
		f, ok := fields[key]
		if !ok {
			continue
		}
		p := map[string]any{"type": f.kind}
		if f.kind == "integer" {
			p["minimum"] = f.minimum
			p["maximum"] = f.maximum
		} else {
			p["maxLength"] = 256
			if f.required {
				p["minLength"] = 1
			}
		}
		props[key] = p
		if key == "status" {
			p["enum"] = []string{"all", "pending", "submitted"}
		}
		if f.required {
			required = append(required, key)
		}
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	security := []map[string]any{{"type": "oauth2", "scopes": []string{"chaoxing.read"}}}
	return tool{Name: name, Description: description, InputSchema: schema, Annotations: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}, SecuritySchemes: security, Meta: map[string]any{"securitySchemes": security}, fields: fields}
}

var tools = []tool{
	makeTool("get_connection_status", "查询上游连接状态。", nil),
	makeTool("list_courses", "列出可访问的课程。", nil),
	makeTool("list_assignments", "查询作业；按上游返回的 complete 和 coverage 判断覆盖范围。", map[string]field{"course": {kind: "string"}, "status": {kind: "string"}, "limit": {kind: "integer", minimum: 1, maximum: 100}, "offset": {kind: "integer", minimum: 0, maximum: 100000}}),
	makeTool("get_assignment", "读取指定作业详情。", map[string]field{"id": {kind: "string", required: true}}),
	makeTool("list_exams", "列出考试；保留上游覆盖范围标记。", nil),
	makeTool("list_notices", "列出通知；保留上游覆盖范围标记。", nil),
	makeTool("get_schedule", "读取指定周课表。", map[string]field{"week": {kind: "integer", minimum: 1, maximum: 30}}),
	makeTool("list_materials", "读取课程资料目录，不执行下载或写操作。", map[string]field{"course_id": {kind: "string", required: true}, "class_id": {kind: "string", required: true}, "cpi": {kind: "string", required: true}, "folder_id": {kind: "string"}}),
}

type endpoint struct {
	call  func(context.Context, string, map[string]any) (any, error)
	slots chan struct{}
}

// New returns an HTTP handler, not a listener. Authentication is the caller's
// responsibility. The callback receives only allowlisted, validated tool calls.
func New(call func(context.Context, string, map[string]any) (any, error)) http.Handler {
	return &endpoint{call: call, slots: make(chan struct{}, 4)}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func respond(w http.ResponseWriter, status int, id json.RawMessage, result any, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	v := map[string]any{"jsonrpc": "2.0", "id": id}
	if code != 0 {
		v["error"] = map[string]any{"code": code, "message": message}
	} else {
		v["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func parseJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func objectJSON(b []byte) bool { b = bytes.TrimSpace(b); return len(b) > 0 && b[0] == '{' }
func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	if len(id) > 256 {
		return false
	}
	var v any
	if parseJSON(id, &v) != nil {
		return false
	}
	switch v.(type) {
	case string, json.Number:
		return true
	}
	return false
}
func protocol(v string) bool { return v == "2025-11-25" || v == "2025-06-18" || v == "2025-03-26" }
func (e *endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "JSON POST required", 405)
		return
	}
	// Remote browser origins must match the externally visible request host.
	// A trusted reverse proxy must preserve Host; absent Origin is a machine call.
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		// HTTPS may terminate at the trusted proxy. Never trust forwarded headers.
		if err != nil || u.Host != r.Host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(r.TLS == nil && u.Scheme == "http")) {
			http.Error(w, "origin not allowed", 403)
			return
		}
	}
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/json" {
		http.Error(w, "application/json required", 415)
		return
	}
	accept := r.Header.Get("Accept")
	if accept != "" && !strings.Contains(accept, "application/json") && !strings.Contains(accept, "*/*") {
		http.Error(w, "JSON responses required", 406)
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !protocol(v) {
		http.Error(w, "unsupported protocol version", 400)
		return
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "busy", 503)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large", 413)
		return
	}
	var req rpcRequest
	if parseJSON(b, &req) != nil {
		respond(w, 400, nil, nil, -32700, "Parse error")
		return
	}
	if !objectJSON(b) || req.JSONRPC != "2.0" || req.Method == "" || !validID(req.ID) {
		respond(w, 400, nil, nil, -32600, "Invalid Request")
		return
	}
	if len(req.ID) == 0 {
		if strings.HasPrefix(req.Method, "notifications/") {
			w.WriteHeader(202)
			return
		}
		w.WriteHeader(202)
		return
	}
	if len(req.Params) > 0 && !objectJSON(req.Params) {
		respond(w, 200, req.ID, nil, -32602, "Params must be an object")
		return
	}
	switch req.Method {
	case "initialize":
		var p struct {
			Version string `json:"protocolVersion"`
		}
		if len(req.Params) == 0 || parseJSON(req.Params, &p) != nil || p.Version == "" {
			respond(w, 200, req.ID, nil, -32602, "protocolVersion is required")
			return
		}
		version := p.Version
		if !protocol(version) {
			version = "2025-11-25"
		}
		respond(w, 200, req.ID, map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]string{"name": "chaoxing", "version": "1.0.0"}}, 0, "")
	case "ping":
		respond(w, 200, req.ID, map[string]any{}, 0, "")
	case "tools/list":
		if len(req.Params) > 0 {
			var p map[string]any
			_ = parseJSON(req.Params, &p)
			if len(p) > 0 {
				respond(w, 200, req.ID, nil, -32602, "No pagination cursor is supported")
				return
			}
		}
		respond(w, 200, req.ID, map[string]any{"tools": tools}, 0, "")
	case "tools/call":
		e.callTool(w, r, req)
	default:
		respond(w, 200, req.ID, nil, -32601, "Method not found")
	}
}
func validate(t tool, args map[string]any) bool {
	for key, f := range t.fields {
		v, ok := args[key]
		if !ok {
			if f.required {
				return false
			}
			continue
		}
		if f.kind == "string" {
			s, ok := v.(string)
			if !ok || len(s) > 256 || (f.required && strings.TrimSpace(s) == "") || strings.ContainsAny(s, "\x00\r\n") {
				return false
			}
			if key == "status" && s != "all" && s != "pending" && s != "submitted" {
				return false
			}
		} else {
			n, ok := v.(json.Number)
			if !ok {
				return false
			}
			i, err := n.Int64()
			if err != nil || i < int64(f.minimum) || i > int64(f.maximum) {
				return false
			}
			// Keep the normal encoding/json numeric convention used by Client.Call.
			args[key] = float64(i)
		}
	}
	for key := range args {
		if _, ok := t.fields[key]; !ok {
			return false
		}
	}
	return true
}
func (e *endpoint) callTool(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(req.Params) == 0 || parseJSON(req.Params, &p) != nil {
		respond(w, 200, req.ID, nil, -32602, "Invalid tool parameters")
		return
	}
	var selected *tool
	for i := range tools {
		if tools[i].Name == p.Name {
			selected = &tools[i]
			break
		}
	}
	if selected == nil {
		respond(w, 200, req.ID, nil, -32602, "Unknown tool")
		return
	}
	args := map[string]any{}
	if len(p.Arguments) > 0 && (!objectJSON(p.Arguments) || parseJSON(p.Arguments, &args) != nil) {
		respond(w, 200, req.ID, nil, -32602, "Arguments must be an object")
		return
	}
	if !validate(*selected, args) {
		respond(w, 200, req.ID, nil, -32602, "Invalid tool arguments")
		return
	}
	if e.call == nil {
		respond(w, 200, req.ID, nil, -32603, "Tool service unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	value, err := e.call(ctx, p.Name, args)
	if err != nil { // Upstream errors may contain credentials or private URLs: do not echo.
		respond(w, 200, req.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": "Upstream read failed."}}, "isError": true}, 0, "")
		return
	}
	b, err := json.Marshal(value)
	if err != nil || len(b) > maxResult {
		respond(w, 200, req.ID, nil, -32603, "Tool result unavailable or too large")
		return
	}
	var structured any
	_ = json.Unmarshal(b, &structured)
	if _, ok := structured.(map[string]any); !ok {
		structured = map[string]any{"result": structured}
	}
	respond(w, 200, req.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": string(b)}}, "structuredContent": structured, "isError": false}, 0, "")
}
