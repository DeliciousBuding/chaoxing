package upstream

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestObservedSignDoesNotRediscoverDuringListCooldown(t *testing.T) {
	discoveries, preSigns, submissions := 0, 0, 0
	c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			discoveries++
			return response(r, signActivityResponse(0, "")), nil
		case "/v2/apis/active/getPPTActiveInfo":
			return response(r, `{"result":1,"data":{"otherId":0,"ifphoto":0}}`), nil
		case "/apis/login/userLogin4Uname.do":
			return response(r, `{"status":true,"msg":{"name":"Synthetic","uid":"10001","puid":"10001","fid":"20002"}}`), nil
		case "/newsign/preSign":
			preSigns++
			return response(r, `<script>var state={"primaryAttend":{"status":1}}</script>`), nil
		case "/v2/apis/sign/getAttendInfo":
			return response(r, `{"result":1,"data":{"status":1}}`), nil
		case "/pptSign/stuSignajax":
			submissions++
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return response(r, ""), nil
	}))
	rows, err := c.ListClassActivities(context.Background(), "course-1", "class-1")
	if err != nil {
		t.Fatal(err)
	}
	// Neither a short-lived cache nor an in-memory previous process is needed.
	c.classActivities = nil
	u, _ := url.Parse(activityAPI)
	c.deferEndpoint(u, "600")
	result, err := c.SignObservedActivity(context.Background(), SignRequest{CourseID: "course-1", ClassID: "class-1", ActivityID: "9001"}, rows[0], time.Now().Add(-time.Minute))
	if err != nil || !result.Verified || result.Submitted || discoveries != 1 || preSigns != 1 || submissions != 0 {
		t.Fatalf("result=%+v err=%v discovery=%d presign=%d submit=%d", result, err, discoveries, preSigns, submissions)
	}
	// Discovery still respects its own cooldown after the independent read.
	if _, err := c.ListClassActivities(context.Background(), "course-1", "class-1"); SafeFault(err).Code != "UPSTREAM_RATE_LIMITED" || discoveries != 1 {
		t.Fatal("observed action cleared or bypassed list cooldown")
	}
}

func TestObservedSignRejectsMismatchedExpiredAndUnownedCards(t *testing.T) {
	calls := 0
	c := academicClient(t, []Course{{ID: "c", ClassID: "k"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected network request %s", r.URL.Path)
		return nil, nil
	}))
	end := time.Now().Add(time.Minute)
	base := Activity{ID: "a", CourseID: "c", ClassID: "k", Kind: "signin", State: "active", Signable: true, EndsAt: &end}
	for _, name := range []string{"mismatch", "expired", "old", "unowned"} {
		t.Run(name, func(t *testing.T) {
			item, at := base, time.Now()
			req := SignRequest{CourseID: "c", ClassID: "k", ActivityID: "a"}
			switch name {
			case "mismatch":
				item.ID = "different"
			case "expired":
				ended := time.Now().Add(-time.Second)
				item.EndsAt = &ended
			case "old":
				at = time.Now().Add(-25 * time.Hour)
			case "unowned":
				item.CourseID, req.CourseID = "other", "other"
			}
			if _, err := c.SignObservedActivity(context.Background(), req, item, at); err == nil {
				t.Fatal("unsafe observed card accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatal("invalid observation reached upstream")
	}
}
