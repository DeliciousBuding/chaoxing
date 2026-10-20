package upstream

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestAnswerActivityUsesRapidAnswerProtocol(t *testing.T) {
	now := time.Now().UnixMilli()
	c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, activityResponse(activityFixture(9101, "Synthetic rapid answer", 4, 1, 0, now-1000, now+600000))), nil
		case "/apis/login/userLogin4Uname.do":
			return response(r, `{"status":true,"msg":{"name":"Synthetic User","uid":"10001","puid":"10001","fid":"20002"}}`), nil
		case "/pptAnswer/stuAnswer":
			want := "answerId=9101&classId=class-1&role=&courseId=course-1&general=&appType=15&stuMiddlePage=1&stuName=Synthetic+User"
			if r.URL.RawQuery != want {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, "success"), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}))
	v, err := c.Call(context.Background(), "answer_activity", map[string]any{
		"course_id": "course-1", "class_id": "class-1", "activity_id": "9101",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := v.(map[string]any)["result"].(SignResult)
	if result.Method != "answer" || result.State != "answered" || !result.Verified {
		t.Fatalf("%+v", result)
	}
}

func TestActivityFeedRecognizesClassroomActivityTypes(t *testing.T) {
	now := time.Now().UnixMilli()
	c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		return response(r, activityResponse(
			activityFixture(9201, "Rapid", 4, 1, 0, now, now),
			activityFixture(9202, "Practice", 42, 1, 0, now, now),
			activityFixture(9203, "Homework", 19, 1, 0, now, now),
		)), nil
	}))
	v, err := c.Call(context.Background(), "list_activities", map[string]any{"course_id": "course-1", "class_id": "class-1"})
	if err != nil {
		t.Fatal(err)
	}
	items := v.(map[string]any)["items"].([]Activity)
	byID := map[string]Activity{}
	for _, item := range items {
		byID[item.ID] = item
	}
	if byID["9201"].Kind != "answer" || !byID["9201"].Answerable || byID["9201"].Signable {
		t.Fatalf("%+v", byID["9201"])
	}
	if byID["9202"].Kind != "practice" || byID["9202"].Answerable {
		t.Fatalf("%+v", byID["9202"])
	}
	if byID["9203"].Kind != "homework" {
		t.Fatalf("%+v", byID["9203"])
	}
}
