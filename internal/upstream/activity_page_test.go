package upstream

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestActivityPageCollectsPracticeStructureWithoutValues(t *testing.T) {
	now := time.Now().UnixMilli()
	row := fmt.Sprintf(`{"id":9301,"nameOne":"Synthetic practice","type":42,"status":1,"userStatus":0,"startTime":%d,"endTime":%d,"url":"https://mobilelearn.chaoxing.com/practice/page?activityId=9301"}`, now-1000, now+600000)
	c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, activityResponse(row)), nil
		case "/practice/page":
			return response(r, `<html><head><title>Synthetic practice</title><script src="/practice/app.js"></script></head><body>
<iframe src="https://mooc1.chaoxing.com/practice/inner"></iframe>
<a href="/practice/submit">Submit</a>
<form action="/practice/submit" method="POST"><input name="enc" value="secret"><input name="answer" value=""></form>
<p>Classroom practice evidence</p></body></html>`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}))
	v, err := c.Call(context.Background(), "get_activity_page", map[string]any{
		"course_id": "course-1", "class_id": "class-1", "activity_id": "9301",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := v.(map[string]any)
	page := out["activity_page"].(map[string]any)
	if page["title"] != "Synthetic practice" || page["final_url"] != "https://mobilelearn.chaoxing.com/practice/page?activityId=9301" {
		t.Fatal(page)
	}
	if len(page["iframes"].([]string)) != 1 || len(page["scripts"].([]string)) != 1 || len(page["links"].([]activityPageLink)) != 1 {
		t.Fatal(page)
	}
	forms := page["forms"].([]activityPageForm)
	if len(forms) != 1 || forms[0].Action != "/practice/submit" || forms[0].Method != "post" || len(forms[0].Fields) != 2 {
		t.Fatal(forms)
	}
	if forms[0].Fields[0].Name != "enc" || !forms[0].Fields[0].ValuePresent || forms[0].Fields[0].Type != "input" {
		t.Fatal(forms[0].Fields[0])
	}
	if len(page["body_sha256"].(string)) != 64 || !strings.Contains(page["text"].(string), "Classroom practice evidence") {
		t.Fatal(page)
	}
}
