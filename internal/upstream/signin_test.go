package upstream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signActivityResponse(otherID int, extra string) string {
	now := time.Now().UnixMilli()
	return `{"result":1,"data":{"activeList":[{"id":"9001","nameOne":"Synthetic sign-in","nameFour":"","type":2,"activeType":2,"otherId":` + strconv.Itoa(otherID) + `,"status":1,"userStatus":0,"startTime":` + strconv.FormatInt(now-1000, 10) + `,"endTime":` + strconv.FormatInt(now+600000, 10) + `}],"ext":{"synthetic":true}}}`
}

func TestNormalSignUsesReferenceProtocolAndDeviceIdentity(t *testing.T) {
	var c *Client
	c = academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "ChaoXingStudy_3_7.0.4_android_phone_10995_") || !strings.Contains(ua, "(schild:") {
			t.Fatalf("user agent mismatch: %q", r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, signActivityResponse(0, "")), nil
		case "/v2/apis/active/getPPTActiveInfo":
			if r.URL.RawQuery != "activeId=9001" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `{"result":1,"data":{"otherId":0,"ifphoto":0,"ifNeedVCode":0,"openCheckFaceFlag":0}}`), nil
		case "/apis/login/userLogin4Uname.do":
			if r.Method != "POST" || r.FormValue("data") == "" {
				t.Fatalf("user info request %s %s", r.Method, r.URL)
			}
			return response(r, `{"status":true,"msg":{"name":"Synthetic User","uid":"10001","puid":"10001","fid":"20002"}}`), nil
		case "/newsign/preSign":
			if r.Method != "POST" || r.FormValue("ext") != `{"synthetic":true}` {
				t.Fatalf("preSign request %s ext=%q", r.Method, r.FormValue("ext"))
			}
			if !strings.HasPrefix(r.URL.RawQuery, "courseId=course-1&classId=class-1&activePrimaryId=9001&") {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `<html>ready</html>`), nil
		case "/pptSign/analysis":
			if r.URL.RawQuery != "vs=1&DB_STRATEGY=RANDOM&aid=9001" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `code='+'a1b2c3'`), nil
		case "/pptSign/analysis2":
			if r.URL.RawQuery != "DB_STRATEGY=RANDOM&code=a1b2c3" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, ``), nil
		case "/pptSign/stuSignajax":
			wantPrefix := "activeId=9001&courseId=course-1&uid=10001&clientip=&useragent=&latitude=-1&longitude=-1&appType=15&fid=20002&name=Synthetic+User&deviceCode="
			if !strings.HasPrefix(r.URL.RawQuery, wantPrefix) {
				t.Fatal(r.URL.RawQuery)
			}
			if r.URL.Query().Get("deviceCode") != c.device.DeviceCode {
				t.Fatal("device code missing")
			}
			return response(r, "success"), nil
		case "/v2/apis/sign/getAttendInfo":
			return response(r, `{"result":1,"data":{"status":1}}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}))
	v, err := c.Call(context.Background(), "sign_activity", map[string]any{"course_id": "course-1", "class_id": "class-1", "activity_id": "9001"})
	if err != nil {
		t.Fatal(err)
	}
	out := v.(map[string]any)
	result := out["result"].(SignResult)
	if result.Method != "normal" || result.State != "signed" || !result.Verified || out["complete"] != true {
		t.Fatalf("%+v", out)
	}
}

func TestLocationSignUsesPresetJitterAndSingleTightRetry(t *testing.T) {
	var c *Client
	signCalls := 0
	c = academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, signActivityResponse(4, "")), nil
		case "/v2/apis/active/getPPTActiveInfo":
			return response(r, `{"result":1,"data":{"otherId":4,"ifopenAddress":1,"ifNeedVCode":0,"openCheckFaceFlag":0,"locationLatitude":31.000000,"locationLongitude":121.000000,"locationRange":500}}`), nil
		case "/apis/login/userLogin4Uname.do":
			return response(r, `{"status":true,"msg":{"name":"Synthetic User","uid":"10001","puid":"10001","fid":"20002"}}`), nil
		case "/newsign/preSign":
			return response(r, `<html>ready</html>`), nil
		case "/pptSign/analysis":
			return response(r, `code='+'a1b2c3'`), nil
		case "/pptSign/analysis2":
			return response(r, ``), nil
		case "/pptSign/stuSignajax":
			signCalls++
			q := r.URL.Query()
			lat, lng := parseFloat(t, q.Get("latitude")), parseFloat(t, q.Get("longitude"))
			bound := 0.00006
			if signCalls == 2 {
				bound = 0.00002
			}
			if lat < 31-bound || lat > 31+bound || lng < 121-bound || lng > 121+bound {
				t.Fatalf("jitter outside reference bounds: %v %v", lat, lng)
			}
			if !strings.HasPrefix(r.URL.RawQuery, "name=Synthetic+User&address=Synthetic+room&activeId=9001&courseId=course-1&uid=10001&clientip=&latitude=") {
				t.Fatal(r.URL.RawQuery)
			}
			var location locationResultPayload
			if err := json.Unmarshal([]byte(q.Get("locationResult")), &location); err != nil || location.Result != 1 || location.Address != "Synthetic room" {
				t.Fatalf("locationResult=%q err=%v", q.Get("locationResult"), err)
			}
			for _, value := range []float64{location.Latitude, location.Longitude} {
				decimal := strings.Split(strconv.FormatFloat(value, 'f', -1, 64), ".")
				if len(decimal) == 2 && len(decimal[1]) > 6 {
					t.Fatalf("locationResult coordinate exceeds reference precision: %v", value)
				}
			}
			if r.URL.Query().Get("deviceCode") != c.device.DeviceCode {
				t.Fatal("device code missing")
			}
			if len(r.URL.Query()["deviceCode"]) == 0 {
				t.Fatal("no device code")
			}
			if signCalls == 1 {
				return response(r, "errorLocation_20"), nil
			}
			return response(r, "success"), nil
		case "/v2/apis/sign/getAttendInfo":
			return response(r, `{"result":1,"data":{"status":1}}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}))
	lat, lng := 31.0, 121.0
	v, err := c.Call(context.Background(), "sign_activity", map[string]any{
		"course_id": "course-1", "class_id": "class-1", "activity_id": "9001",
		"latitude": lat, "longitude": lng, "address": "Synthetic room",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := v.(map[string]any)["result"].(SignResult)
	if result.Method != "location" || !result.Verified {
		t.Fatalf("%+v", result)
	}
	if signCalls != 2 {
		t.Fatalf("sign calls=%d", signCalls)
	}
}

func TestPasswordSignChecksCodeAndUsesReferenceQuery(t *testing.T) {
	c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, signActivityResponse(5, "")), nil
		case "/v2/apis/active/getPPTActiveInfo":
			return response(r, `{"result":1,"data":{"otherId":5,"ifNeedVCode":0,"openCheckFaceFlag":0}}`), nil
		case "/apis/login/userLogin4Uname.do":
			return response(r, `{"status":true,"msg":{"name":"Synthetic User","uid":"10001","puid":"10001","fid":"20002"}}`), nil
		case "/newsign/preSign":
			return response(r, `<html>ready</html>`), nil
		case "/pptSign/analysis":
			return response(r, `code='+'a1b2c3'`), nil
		case "/pptSign/analysis2":
			return response(r, ``), nil
		case "/widget/sign/pcStuSignController/checkSignCode":
			if r.URL.RawQuery != "activeId=9001&signCode=1234" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `{"result":1}`), nil
		case "/pptSign/stuSignajax":
			want := "activeId=9001&courseId=course-1&uid=10001&clientip=&latitude=-1&longitude=-1&appType=15&fid=20002&name=Synthetic+User&signCode=1234&deviceCode="
			if !strings.HasPrefix(r.URL.RawQuery, want) {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, "success"), nil
		case "/v2/apis/sign/getAttendInfo":
			return response(r, `{"result":1,"data":{"status":1}}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}))
	v, err := c.Call(context.Background(), "sign_activity", map[string]any{
		"course_id": "course-1", "class_id": "class-1", "activity_id": "9001", "sign_code": "1234",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := v.(map[string]any)["result"].(SignResult)
	if result.Method != "password" || result.State != "signed" || !result.Verified {
		t.Fatalf("%+v", result)
	}
}

func parseFloat(t *testing.T, s string) float64 {
	t.Helper()
	var v float64
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(s, err)
	}
	return v
}

func TestDeviceStatePersistsStableIdentity(t *testing.T) {
	d := t.TempDir()
	cfg := Config{
		CredentialsPath: filepath.Join(d, "credentials"), KeyPath: filepath.Join(d, "key"),
		SessionPath: filepath.Join(d, "session"), DeviceStatePath: filepath.Join(d, "device-state.json"),
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	first := c.device
	if len(first.AndroidID) != 16 {
		t.Fatalf("android id shape: %q", first.AndroidID)
	}
	if decoded, err := base64.StdEncoding.Strict().DecodeString(first.MediaDrmID); err != nil || len(decoded) != 32 {
		t.Fatalf("media drm id shape: %q err=%v", first.MediaDrmID, err)
	}
	if decoded, err := base64.StdEncoding.Strict().DecodeString(first.DeviceCode); err != nil || len(decoded) != 64 {
		t.Fatalf("device code shape: %q err=%v", first.DeviceCode, err)
	}
	if first.DeviceUniqueID == "" {
		t.Fatalf("%+v", first)
	}
	if _, err = os.Stat(cfg.DeviceStatePath); err != nil {
		t.Fatal(err)
	}
	c2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c2.device != first {
		t.Fatalf("device identity changed: %+v %+v", first, c2.device)
	}
}

func TestDeviceConfigRejectsInvalidCode(t *testing.T) {
	d := t.TempDir()
	_, err := New(Config{
		CredentialsPath: filepath.Join(d, "credentials"), KeyPath: filepath.Join(d, "key"),
		SessionPath: filepath.Join(d, "session"), DeviceStatePath: filepath.Join(d, "device-state.json"),
		DeviceCode: "not-base64!",
	})
	if err == nil || !strings.Contains(err.Error(), "device code") {
		t.Fatal(err)
	}
}

func TestDeviceInvalidStateFailsClosed(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "device-state.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{
		CredentialsPath: filepath.Join(d, "credentials"), KeyPath: filepath.Join(d, "key"),
		SessionPath: filepath.Join(d, "session"), DeviceStatePath: path,
	})
	if err == nil {
		t.Fatal("invalid device state accepted")
	}
}

func TestDeviceConfigOverridesPersist(t *testing.T) {
	d := t.TempDir()
	cfg := Config{
		CredentialsPath: filepath.Join(d, "credentials"), KeyPath: filepath.Join(d, "key"),
		SessionPath: filepath.Join(d, "session"), DeviceStatePath: filepath.Join(d, "device-state.json"),
		DeviceCode: "c3ludGhldGljLWRldmljZS1jb2Rl", UserAgent: "Synthetic/1.0 Agent", PackageName: "com.example.synthetic",
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.device.DeviceCode != cfg.DeviceCode || c.device.UserAgent != cfg.UserAgent || c.device.PackageName != cfg.PackageName {
		t.Fatalf("%+v", c.device)
	}
	cfg.DeviceCode, cfg.UserAgent, cfg.PackageName = "", "", ""
	c2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c2.device != c.device {
		t.Fatalf("%+v %+v", c.device, c2.device)
	}
}
