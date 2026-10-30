package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSignIgnoresListHintAndRequiresAttendanceProof(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		preStatus, attendStatus int
		submitted, verified     bool
	}{
		{"unsigned list hint one", 0, 1, true, true},
		{"already signed independently", 1, 1, false, true},
		{"absence is not success", 0, 5, true, false},
		{"unknown is not success", 0, 77, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			submissions := 0
			c := academicClient(t, []Course{{ID: "c", ClassID: "k"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v2/apis/active/student/activelist":
					return response(r, activityResponse(activityFixture(9001, "Synthetic", 2, 1, 1, time.Now().Add(-time.Minute).UnixMilli(), time.Now().Add(time.Minute).UnixMilli()))), nil
				case "/v2/apis/active/getPPTActiveInfo":
					return response(r, `{"result":1,"data":{"otherId":0,"ifphoto":0}}`), nil
				case "/newsign/preSign":
					return response(r, fmt.Sprintf(`<script>var state={"primaryAttend":{"status":%d}}</script>`, tc.preStatus)), nil
				case "/pptSign/analysis":
					return response(r, `code='+'a1b2c3'`), nil
				case "/pptSign/analysis2":
					return response(r, ``), nil
				case "/pptSign/stuSignajax":
					submissions++
					return response(r, "success"), nil
				case "/v2/apis/sign/getAttendInfo":
					return response(r, fmt.Sprintf(`{"result":1,"data":{"status":%d}}`, tc.attendStatus)), nil
				default:
					t.Fatalf("unexpected request %s", r.URL.Path)
					return nil, nil
				}
			}))
			c.identity = &signIdentity{UID: "10001", FID: "20002", Name: "Synthetic"}
			if err := os.Chmod(filepath.Dir(c.config.SessionPath), 0700); err != nil {
				t.Fatal(err)
			}
			got, err := c.SignActivity(context.Background(), SignRequest{CourseID: "c", ClassID: "k", ActivityID: "9001"})
			if tc.verified != (err == nil) || got.Verified != tc.verified || got.Submitted != tc.submitted {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if (submissions == 1) != tc.submitted || submissions > 1 {
				t.Fatalf("submissions=%d", submissions)
			}
			if tc.verified && !tc.submitted && (got.Method != "none" || got.StateSource != "attendance") {
				t.Fatalf("already signed misattributed: %+v", got)
			}
			data, err := os.ReadFile(c.evidence.path)
			if err != nil {
				t.Fatal(err)
			}
			var events []map[string]any
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var e map[string]any
				if err := json.Unmarshal([]byte(line), &e); err != nil {
					t.Fatal(err)
				}
				events = append(events, e)
			}
			last := events[len(events)-1]
			if last["event"] != "sign_result" || last["submitted"] != tc.submitted || last["verified"] != tc.verified || last["sign_attempt_id"] == "" {
				t.Fatalf("missing actual outcome: %+v", last)
			}
		})
	}
}

func TestUpstreamRateLimitDefersWithoutAnotherRequest(t *testing.T) {
	calls := 0
	c := academicClient(t, []Course{{ID: "c", ClassID: "k"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, `{"result":0,"data":null,"errorMsg":"请勿频繁操作"}`), nil
	}))
	if err := os.Chmod(filepath.Dir(c.config.SessionPath), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err := c.ListClassActivities(context.Background(), "c", "k")
		f := SafeFault(err)
		if f.Code != "UPSTREAM_RATE_LIMITED" || !f.Retryable || f.RetryAfterSeconds < 299 {
			t.Fatalf("%+v", f)
		}
	}
	if calls != 1 {
		t.Fatalf("cooldown still hit upstream: %d", calls)
	}
	data, err := os.ReadFile(c.evidence.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "请勿频繁操作") || !strings.Contains(string(data), `"request_attempted":false`) {
		t.Fatal("actual response/deferred read missing")
	}
}

func TestVerificationDoesNotRequireFeedAndChecksAccountScope(t *testing.T) {
	calls := 0
	c := academicClient(t, []Course{{ID: "c", ClassID: "k"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v2/apis/sign/getAttendInfo" {
			t.Fatalf("verification submitted or rediscovered: %s", r.URL.Path)
		}
		return response(r, `{"result":1,"data":{"status":1}}`), nil
	}))
	got, err := c.VerifySignActivity(context.Background(), SignRequest{CourseID: "c", ClassID: "k", ActivityID: "9001"})
	if err != nil || !got.Verified || got.Submitted || got.StateSource != "attendance" || calls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", got, err, calls)
	}
	if _, err := c.VerifySignActivity(context.Background(), SignRequest{CourseID: "other", ClassID: "k", ActivityID: "9001"}); SafeFault(err).Code != "NOT_FOUND" || calls != 1 {
		t.Fatal("verification escaped enrolled class scope")
	}
	value, err := c.Call(context.Background(), "get_sign_status", map[string]any{"course_id": "c", "class_id": "k", "activity_id": "9001"})
	if err != nil || !value.(map[string]any)["result"].(SignResult).Verified || calls != 2 {
		t.Fatalf("read-only adapter failed: value=%v err=%v calls=%d", value, err, calls)
	}
}

func TestGroupDiscoveredSignUsesOnlyFreshMatchingOwnedCard(t *testing.T) {
	for _, tc := range []struct {
		name, class string
		age         time.Duration
		cooldown    bool
		want        string
	}{
		{"fresh owned", "k", 25 * time.Second, false, ""},
		{"expired", "k", 2 * time.Minute, false, "NOT_FOUND"},
		{"other class", "other", time.Second, false, "NOT_FOUND"},
		{"cooldown does not cause rediscovery", "k", time.Second, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			submissions := 0
			c := academicClient(t, []Course{{ID: "c", ClassID: "k"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v2/apis/active/student/activelist":
					if tc.cooldown {
						t.Fatal("sign-in requested the throttled discovery endpoint")
					}
					return response(r, activityResponse()), nil
				case "/v2/apis/active/getPPTActiveInfo":
					return response(r, `{"result":1,"data":{"otherId":0,"ifphoto":0}}`), nil
				case "/newsign/preSign":
					return response(r, `<script>var x={"primaryAttend":{"status":0}}</script>`), nil
				case "/pptSign/analysis":
					return response(r, `code='+'a1b2c3'`), nil
				case "/pptSign/analysis2":
					return response(r, ""), nil
				case "/pptSign/stuSignajax":
					submissions++
					return response(r, "success"), nil
				case "/v2/apis/sign/getAttendInfo":
					return response(r, `{"result":1,"data":{"status":1}}`), nil
				default:
					t.Fatalf("unexpected request %s", r.URL.Path)
					return nil, nil
				}
			}))
			c.identity = &signIdentity{UID: "10001", FID: "20002", Name: "Synthetic"}
			c.config.GroupActivities = true
			c.imActivitiesAt = time.Now().Add(-tc.age)
			c.imActivities = []Activity{{ID: "9001", CourseID: "c", ClassID: tc.class, Kind: "signin", State: "active", Signable: true, Source: "group"}}
			if tc.cooldown {
				u, _ := url.Parse(activityAPI)
				c.deferEndpoint(u, "")
			}
			got, err := c.SignActivity(context.Background(), SignRequest{CourseID: "c", ClassID: "k", ActivityID: "9001"})
			if tc.want == "" {
				if err != nil || !got.Verified || submissions != 1 {
					t.Fatalf("result=%+v err=%v submissions=%d", got, err, submissions)
				}
			} else if SafeFault(err).Code != tc.want || submissions != 0 {
				t.Fatalf("err=%v submissions=%d", err, submissions)
			}
		})
	}
}

func TestExistingAttendanceResponseIsNotNewSign(t *testing.T) {
	state, err := signResponseState("您已签到过了")
	if err != nil || state != "already_signed" {
		t.Fatalf("prior attendance attributed to new sign: state=%s err=%v", state, err)
	}
}

func TestResponseEvidenceOmitsCredentialsAndIdentity(t *testing.T) {
	body := []byte(`{"result":1,"data":{"status":1,"username":"private-name","deviceCode":"private-device","activeList":[{"id":123,"userStatus":1,"status":1,"url":"https://example.com/?enc=private-token","nameOne":"private-name","latitude":12.34,"password":"private-password"}]},"errorMsg":"private-error"}`)
	encoded, err := json.Marshal(safeResponseEvidence("/v2/apis/active/student/activelist", body))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-", "latitude", "password", "deviceCode", "username", "enc="} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("evidence leaked %s", secret)
		}
	}
	if !strings.Contains(string(encoded), `"userStatus":1`) {
		t.Fatal("real protocol field missing")
	}
	encoded, _ = json.Marshal(safeResponseEvidence("/pptSign/stuSignajax", []byte("validate_private-secret")))
	if strings.Contains(string(encoded), "private-secret") || !strings.Contains(string(encoded), "verification_required") {
		t.Fatal(string(encoded))
	}
}

func TestEvidenceRotationBoundedAndRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	w := newEvidenceWriter(filepath.Join(dir, "session"))
	if err := os.WriteFile(w.path, make([]byte, evidenceSegmentBytes), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.write([]byte(`{"event":"synthetic"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.path + ".1"); err != nil {
		t.Fatal(err)
	}
	if err := w.write(make([]byte, 8193)); err == nil {
		t.Fatal("oversized record accepted")
	}
	if err := os.Remove(w.path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "unrelated")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, w.path); err != nil {
		t.Skip("symlink unavailable")
	}
	if err := w.write([]byte(`{}`)); err == nil {
		t.Fatal("symlink accepted")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "untouched" {
		t.Fatal("unrelated target changed")
	}
}
