package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type signTraceKey struct{}

func upstreamRateLimited(body []byte) bool {
	var envelope struct {
		Result  json.RawMessage `json:"result"`
		Error   string          `json:"errorMsg"`
		Message string          `json:"msg"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	result := strings.TrimSpace(string(envelope.Result))
	if result != "0" && result != "false" {
		return false
	}
	message := envelope.Error + " " + envelope.Message
	return strings.Contains(message, "请勿频繁操作") || strings.Contains(message, "操作过于频繁") || strings.Contains(strings.ToLower(message), "too many requests")
}

const evidenceSegmentBytes = 2 << 20
const evidenceSegments = 3

type evidenceWriter struct {
	mu   sync.Mutex
	path string
}

func newEvidenceWriter(sessionPath string) *evidenceWriter {
	return &evidenceWriter{path: filepath.Join(filepath.Dir(sessionPath), "protocol-evidence.jsonl")}
}

func evidenceRegular(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("evidence target must be a regular file")
	}
	return nil
}

func (w *evidenceWriter) write(body []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(body) > 8192 {
		return errors.New("evidence exceeds record limit")
	}
	if info, err := os.Lstat(filepath.Dir(w.path)); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return errors.New("evidence directory unavailable")
	}
	// Validate every exact target before rotating; never follow a symlink.
	for i := 0; i <= evidenceSegments; i++ {
		path := w.path
		if i > 0 {
			path += "." + strconv.Itoa(i)
		}
		if err := evidenceRegular(path); err != nil {
			return err
		}
	}
	if info, err := os.Stat(w.path); err == nil && (info.Size()+int64(len(body))+1 > evidenceSegmentBytes || time.Since(info.ModTime()) > 7*24*time.Hour) {
		if err := os.Remove(w.path + ".3"); err != nil && !os.IsNotExist(err) {
			return err
		}
		for i := evidenceSegments - 1; i >= 1; i-- {
			if err := os.Rename(w.path+"."+strconv.Itoa(i), w.path+"."+strconv.Itoa(i+1)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil {
			return err
		}
	}
	for i := 1; i <= evidenceSegments; i++ {
		path := w.path + "." + strconv.Itoa(i)
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > 7*24*time.Hour {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(append(body, '\n'))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func evidenceErrorCode(err error) string {
	if err == nil {
		return ""
	}
	return SafeFault(err).Code
}

func (c *Client) recordEvidence(event map[string]any) {
	event["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	body, err := json.Marshal(event)
	if err == nil && c.evidence != nil {
		err = c.evidence.write(body)
	}
	if err != nil {
		c.evidenceFailures.Add(1)
		c.evidenceConsecutive.Add(1)
		log.Printf("upstream evidence: write_failed code=STATE_WRITE_FAILED")
		return
	}
	c.evidenceConsecutive.Store(0)
	if event["event"] == "sign_result" {
		log.Printf("upstream evidence: %s", body)
	}
}

// EvidenceHealth exposes only counters, never private response content.
func (c *Client) EvidenceHealth() (uint64, uint64) {
	return c.evidenceFailures.Load(), c.evidenceConsecutive.Load()
}

func (c *Client) observeResponse(ctx context.Context, method string, u *url.URL, started time.Time, body []byte, response *http.Response, requestErr error) {
	switch u.Path {
	case "/v2/apis/active/student/activelist", "/v2/apis/active/getPPTActiveInfo", "/v2/apis/sign/getAttendInfo", "/newsign/preSign", "/pptSign/analysis", "/pptSign/analysis2", "/pptSign/stuSignajax":
	default:
		return
	}
	status := 0
	serverDate := ""
	if response != nil {
		status = response.StatusCode
		if date, err := http.ParseTime(response.Header.Get("Date")); err == nil {
			serverDate = date.UTC().Format(time.RFC3339)
		}
	}
	hash := sha256.Sum256(body)
	event := map[string]any{"event": "upstream_response", "endpoint": u.Path, "method": method, "http_status": status, "request_attempted": response != nil || (requestErr != nil && SafeFault(requestErr).Code != "UPSTREAM_RATE_LIMITED"), "started_at": started.UTC().Format(time.RFC3339Nano), "duration_ms": time.Since(started).Milliseconds(), "body_bytes": len(body), "body_sha256": hex.EncodeToString(hash[:]), "code": evidenceErrorCode(requestErr), "response": safeResponseEvidence(u.Path, body)}
	if serverDate != "" {
		event["server_date"] = serverDate
	}
	for _, key := range []string{"courseId", "classId", "activeId", "activePrimaryId", "aid"} {
		if value := u.Query().Get(key); value != "" && len(value) <= 96 {
			event[key] = value
		}
	}
	if requestErr != nil {
		event["retry_after_seconds"] = SafeFault(requestErr).RetryAfterSeconds
	}
	if trace, ok := ctx.Value(signTraceKey{}).(*signAuditTrace); ok && trace != nil {
		event["submission_attempted"] = trace.Submitted
		event["sign_attempt_id"] = trace.AttemptID
		event["activity_id"] = trace.ActivityID
	}
	c.recordEvidence(event)
}

func safeNumber(v any) any {
	switch value := v.(type) {
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return n
		}
	case string:
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return n
		}
	}
	return nil
}

// Only numeric protocol fields and canonical outcomes are retained. No raw
// body, URL query, cookie, user identity, coordinates, hidden token or arbitrary
// upstream error text can cross this allowlist.
func safeResponseEvidence(path string, body []byte) map[string]any {
	out := map[string]any{}
	var envelope map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if decoder.Decode(&envelope) == nil && envelope != nil {
		out["format"] = "json"
		if result, ok := envelope["result"]; ok {
			out["result"] = safeNumber(result)
		}
		if upstreamRateLimited(body) {
			out["message"] = "请勿频繁操作"
		}
		data, ok := envelope["data"].(map[string]any)
		out["data_object"] = ok
		if ok {
			for _, key := range []string{"status", "createtime", "updatetime", "otherId", "ifphoto", "ifNeedVCode", "openCheckFaceFlag"} {
				if value, present := data[key]; present {
					out[key] = safeNumber(value)
				}
			}
			if rows, ok := data["activeList"].([]any); ok {
				out["row_count"] = len(rows)
				projected := []any{}
				for _, value := range rows[:min(len(rows), 8)] {
					row, ok := value.(map[string]any)
					if !ok {
						continue
					}
					item := map[string]any{}
					for _, key := range []string{"id", "type", "activeType", "otherId", "status", "userStatus", "startTime", "endTime"} {
						if value, present := row[key]; present {
							item[key] = safeNumber(value)
						}
					}
					projected = append(projected, item)
				}
				out["rows"] = projected
			}
		}
		return out
	}
	out["format"] = "text"
	switch path {
	case "/newsign/preSign":
		if status, ok := primaryAttendanceStatus(string(body)); ok {
			out["primary_attend_status"] = status
		}
		out["expired"] = strings.Contains(string(body), "下次早点哦")
		out["access_rejected"] = strings.Contains(string(body), "校验失败，未查询到活动数据")
	case "/pptSign/stuSignajax":
		state, err := signResponseState(string(body))
		if err != nil {
			out["outcome"] = SafeFault(err).Code
		} else if strings.HasPrefix(state, "validate_") || strings.HasPrefix(state, "checkFace_") {
			out["outcome"] = "verification_required"
		} else {
			out["outcome"] = state
		}
	}
	return out
}

var attendancePatterns = []*regexp.Regexp{
	regexp.MustCompile(`"primaryAttend"\s*:\s*\{[^{}]*?"status"\s*:\s*(\d+)`),
	regexp.MustCompile(`signstatus\s*=\s*(\d+)`),
}

func primaryAttendanceStatus(body string) (int, bool) {
	for _, pattern := range attendancePatterns {
		if m := pattern.FindStringSubmatch(body); m != nil {
			n, err := strconv.Atoi(m[1])
			return n, err == nil
		}
	}
	return 0, false
}
