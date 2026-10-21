package upstream

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func activityFixture(id int64, name string, kind int, status int, userStatus int, start, end int64) string {
	return fmt.Sprintf(`{"id":%d,"nameOne":%q,"nameFour":"","type":%d,"activeType":0,"status":%d,"userStatus":%d,"startTime":%d,"endTime":%d}`, id, name, kind, status, userStatus, start, end)
}

func activityResponse(rows ...string) string {
	return `{"result":1,"data":{"activeList":[` + strings.Join(rows, ",") + `],"ext":{}}}`
}

func TestActivityFeedNormalizesSignInState(t *testing.T) {
	now := time.Now().UnixMilli()
	courses := []Course{{ID: "synthetic-course", ClassID: "class-a", Name: "Synthetic"}}
	c := academicClient(t, courses, roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "mobilelearn.chaoxing.com" || r.URL.Path != "/v2/apis/active/student/activelist" {
			t.Fatalf("unexpected read %s", r.URL)
		}
		if r.URL.Query().Get("courseId") != "synthetic-course" || r.URL.Query().Get("classId") != "class-a" {
			t.Fatalf("missing class scope %s", r.URL.RawQuery)
		}
		return response(r, activityResponse(
			activityFixture(101, "Synthetic sign-in", 2, 1, 0, now-60_000, now+600_000),
			activityFixture(102, "Finished sign-in", 2, 2, 0, now-120_000, now-60_000),
			activityFixture(103, "Synthetic other sign-in", 74, 1, 1, now-30_000, now+600_000),
		)), nil
	}))
	v, err := c.Call(context.Background(), "list_activities", map[string]any{"course_id": "synthetic-course", "class_id": "class-a"})
	if err != nil {
		t.Fatal(err)
	}
	out := v.(map[string]any)
	items := out["items"].([]Activity)
	if len(items) != 3 || out["complete"] != true {
		t.Fatalf("%+v", out)
	}
	byID := map[string]Activity{}
	for _, item := range items {
		byID[item.ID] = item
	}
	unsigned := byID["101"]
	if unsigned.Course != "Synthetic" || unsigned.Kind != "signin" || unsigned.State != "active" || unsigned.UserState != "unsigned" || !unsigned.Signable {
		t.Fatalf("%+v", unsigned)
	}
	if unsigned.StartsAt == nil || unsigned.EndsAt == nil {
		t.Fatalf("timestamps lost: %+v", unsigned)
	}
	finished := byID["102"]
	if finished.State != "finished" || finished.Signable {
		t.Fatalf("%+v", finished)
	}
	signed := byID["103"]
	if signed.Kind != "signin" || signed.State != "active" || signed.UserState != "signed" || signed.Signable {
		t.Fatalf("%+v", signed)
	}
}

func TestActivityFeedPartialKeepsRows(t *testing.T) {
	now := time.Now().UnixMilli()
	courses := []Course{
		{ID: "synthetic-course", ClassID: "class-a", Name: "Synthetic"},
		{ID: "synthetic-course", ClassID: "class-b", Name: "Synthetic"},
	}
	c := academicClient(t, courses, roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("classId") == "class-b" {
			return response(r, `{"result":0,"errorMsg":"synthetic"}`), nil
		}
		return response(r, activityResponse(activityFixture(201, "Synthetic sign-in", 2, 1, 0, now-1000, now+60_000))), nil
	}))
	v, err := c.Call(context.Background(), "list_activities", map[string]any{"course_id": "synthetic-course"})
	if err != nil {
		t.Fatal(err)
	}
	out := v.(map[string]any)
	items := out["items"].([]Activity)
	if len(items) != 1 || out["complete"] != false {
		t.Fatalf("%+v", out)
	}
	details := out["coverage_details"].(map[string]any)
	if details["sources_failed"] != 1 {
		t.Fatalf("%+v", details)
	}
	warnings := out["warnings"].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "UPSTREAM_SCHEMA") {
		t.Fatalf("%+v", warnings)
	}
}

func TestActivityPublicURLDropsQueryAndFragment(t *testing.T) {
	got := publicActivityURL("https://mobilelearn.chaoxing.com/practice/page?enc=placeholder-secret#fragment")
	if got != "https://mobilelearn.chaoxing.com/practice/page" {
		t.Fatalf("url was not redacted: %q", got)
	}
	if publicActivityURL("javascript:alert(1)") != "" {
		t.Fatal("non-https URL accepted")
	}
	if publicActivityURL("//evil.example/path") != "" {
		t.Fatal("protocol-relative URL accepted")
	}
	if publicActivityURL("http://mobilelearn.chaoxing.com/path") != "" {
		t.Fatal("plain-http URL accepted")
	}
}

func TestActivityFeedAllClassesFailingReturnsError(t *testing.T) {
	courses := []Course{{ID: "synthetic-course", ClassID: "class-a", Name: "Synthetic"}}
	c := academicClient(t, courses, roundtrip(func(r *http.Request) (*http.Response, error) {
		return response(r, `<html>maintenance</html>`), nil
	}))
	if _, err := c.Call(context.Background(), "list_activities", map[string]any{"course_id": "synthetic-course"}); err == nil {
		t.Fatal("accepted unrecognized activity list")
	} else if f := SafeFault(err); f.Code != "UPSTREAM_SCHEMA" {
		t.Fatalf("%+v", f)
	}
}

func TestActivityFeedRejectsRowWithoutID(t *testing.T) {
	courses := []Course{{ID: "synthetic-course", ClassID: "class-a", Name: "Synthetic"}}
	c := academicClient(t, courses, roundtrip(func(r *http.Request) (*http.Response, error) {
		return response(r, activityResponse(`{"nameOne":"Synthetic","type":2,"status":1,"userStatus":0}`)), nil
	}))
	_, err := c.Call(context.Background(), "list_activities", map[string]any{"course_id": "synthetic-course"})
	if err == nil || SafeFault(err).Code != "UPSTREAM_SCHEMA" {
		t.Fatalf("%v", err)
	}
}
