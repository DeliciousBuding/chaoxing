package watch

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/upstream"
	"github.com/DeliciousBuding/chaoxing/internal/vault"
)

type chainTransport func(*http.Request) (*http.Response, error)

func (f chainTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// Keep assignment/group work out of this test so every outbound call belongs
// to discovery, signing or confirmation and an unexpected call fails it.
type chainActivitySource struct{ client *upstream.Client }

func (s chainActivitySource) ListClassActivities(ctx context.Context, course, class string) ([]upstream.Activity, error) {
	return s.client.ListClassActivities(ctx, course, class)
}

func TestAcceptedSignSurvivesRestartFeedDisappearanceAndPushFailure(t *testing.T) {
	dir := t.TempDir()
	cfg := upstream.Config{CredentialsPath: filepath.Join(dir, "credentials"), SessionPath: filepath.Join(dir, "session"), KeyPath: filepath.Join(dir, "key")}
	if err := vault.Seal(cfg.CredentialsPath, cfg.KeyPath, vault.Credentials{Phone: "synthetic", Password: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	submissions, discoveries, confirmations := 0, 0, 0
	afterRestart := false
	cfg.Transport = chainTransport(func(req *http.Request) (*http.Response, error) {
		status, body := 200, ""
		switch req.URL.Path {
		case "/mycourse/backclazzdata":
			body = `{"result":1,"channelList":[{"content":{"id":"class-k","cpi":"synthetic-cpi","name":"Synthetic class","course":{"data":[{"id":"course-c","name":"Synthetic course"}]}}}]}`
		case "/v2/apis/active/student/activelist":
			if afterRestart {
				t.Fatal("verification tried to rediscover an ended activity")
			}
			discoveries++
			body = `{"result":1,"data":{"activeList":[{"id":"9001","type":2,"otherId":0,"status":1,"userStatus":1,"nameOne":"Synthetic sign"}],"ext":{}}}`
		case "/v2/apis/active/getPPTActiveInfo":
			body = `{"result":1,"data":{"otherId":0,"ifphoto":0}}`
		case "/apis/login/userLogin4Uname.do":
			body = `{"status":true,"msg":{"name":"Synthetic","uid":"10001","puid":"10001","fid":"20002"}}`
		case "/newsign/preSign":
			if afterRestart {
				t.Fatal("verification reentered submission preflight")
			}
			body = `<script>var state={"primaryAttend":{"status":0}}</script>`
		case "/pptSign/analysis":
			body = `code='+'a1b2c3'`
		case "/pptSign/analysis2":
		case "/pptSign/stuSignajax":
			submissions++
			if submissions > 1 {
				t.Fatal("accepted submission was repeated")
			}
			body = "success"
		case "/v2/apis/sign/getAttendInfo":
			confirmations++
			if confirmations == 1 {
				status, body = 503, "temporarily unavailable"
			} else {
				body = `{"result":1,"data":{"status":1}}`
			}
		default:
			t.Fatalf("unexpected upstream request %s", req.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	watchCfg := Config{Interval: time.Minute, Window: "07:30-22:30", AutoSign: true, StatePath: filepath.Join(dir, "watch.json"), Classes: []Class{{CourseID: "course-c", ClassID: "class-k"}}}
	now := time.Now()
	watchCfg.Window = ""
	newRunner := func(sender *fakeSender) *Runner {
		client, err := upstream.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, err := New(watchCfg, chainActivitySource{client}, sender, client)
		if err != nil {
			t.Fatal(err)
		}
		r.now = func() time.Time { return now }
		return r
	}
	r := newRunner(&fakeSender{fail: true})
	r.Tick(context.Background())
	k := "course-c:class-k:9001"
	if e := r.state[k]; e.Status != "verify" || !e.Submitted || e.PendingResult == nil || discoveries != 1 || submissions != 1 {
		t.Fatalf("accepted stage lost: %+v discoveries=%d submissions=%d", e, discoveries, submissions)
	}
	afterRestart = true
	watchCfg.Window = "00:00-00:01"
	r = newRunner(&fakeSender{fail: true})
	if delay := r.nextTickDelay(now.Add(15 * time.Second)); delay != 15*time.Second {
		t.Fatalf("verification ignored outside-window deadline: %s", delay)
	}
	now = now.Add(31 * time.Second)
	r.Tick(context.Background())
	if e := r.state[k]; e.Status != "signed" || !e.AttendanceVerified || !e.Submitted || e.PendingActivity != nil || confirmations != 2 {
		t.Fatalf("verification failed after restart: %+v confirmations=%d", e, confirmations)
	}
	if alert := r.alerts[k]; alert.Title != "✅ 学习通自动签到成功" {
		t.Fatalf("waiting alert replaced final result: %+v", alert)
	}
	sender := &fakeSender{}
	r = newRunner(sender)
	now = now.Add(31 * time.Second)
	r.Tick(context.Background())
	if submissions != 1 || discoveries != 1 || confirmations != 2 || len(sender.sent) != 1 || sender.sent[0].Title != "✅ 学习通自动签到成功" {
		t.Fatalf("chain repeated work: submit=%d discovery=%d verify=%d alerts=%+v", submissions, discoveries, confirmations, sender.sent)
	}
}

func TestCooldownDoesNotExhaustSubmissionAttempts(t *testing.T) {
	src := &fakeSource{items: []upstream.Activity{signIn("c", "k", "1", true)}}
	signer := &fakeSigner{err: &upstream.Fault{Code: "UPSTREAM_RATE_LIMITED", RetryAfterSeconds: 300}}
	r, err := New(Config{Interval: time.Minute, AutoSign: true, Classes: []Class{{CourseID: "c", ClassID: "k"}}}, src, &fakeSender{}, signer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, shanghai)
	r.now = func() time.Time { return now }
	for i := 0; i < 6; i++ {
		r.Tick(context.Background())
		if e := r.state[key(src.items[0])]; e.Status != "retry" || e.RetryCount != 0 {
			t.Fatalf("cooldown consumed attempt %d: %+v", i, e)
		}
		now = now.Add(301 * time.Second)
	}
	signer.err = nil
	signer.result = upstream.SignResult{Method: "normal", State: "signed", Verified: true, Submitted: true}
	r.Tick(context.Background())
	if r.state[key(src.items[0])].Status != "signed" || len(signer.requests) != 7 {
		t.Fatal("recovered activity was permanently abandoned")
	}
}

type verificationStub struct {
	fakeSigner
	calls int
}

func (s *verificationStub) VerifySignActivity(_ context.Context, req upstream.SignRequest) (upstream.SignResult, error) {
	s.calls++
	return upstream.SignResult{ActivityID: req.ActivityID, CourseID: req.CourseID, ClassID: req.ClassID, Verified: true, StateSource: "attendance"}, nil
}

func TestVerificationRejectsMismatchedAndExpiredReceiptWithoutRequests(t *testing.T) {
	for _, invalidScope := range []bool{false, true} {
		signer := &verificationStub{}
		r, err := New(Config{Interval: time.Minute, Window: "07:30-22:30", AutoSign: true, StatePath: filepath.Join(t.TempDir(), "state"), Classes: []Class{{CourseID: "c", ClassID: "k"}}}, &fakeSource{}, &fakeSender{}, signer)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 10, 20, 23, 0, 0, 0, shanghai)
		r.now = func() time.Time { return now }
		item := signIn("c", "k", "9001", true)
		result := upstream.SignResult{ActivityID: item.ID, CourseID: "c", ClassID: "k", State: "signed", Submitted: true, FetchedAt: now.Add(-25 * time.Hour)}
		if invalidScope {
			result.FetchedAt, result.CourseID = now, "other"
		}
		r.state[key(item)] = entry{Status: "verify", PendingActivity: &item, PendingResult: &result, Submitted: true}
		r.Tick(context.Background())
		if e := r.state[key(item)]; e.Status != "failed" || e.AttendanceVerified || signer.calls != 0 || len(signer.requests) != 0 {
			t.Fatalf("bad receipt authorized action: %+v verifier=%d submits=%d", e, signer.calls, len(signer.requests))
		}
	}
}
