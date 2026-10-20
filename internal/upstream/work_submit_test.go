package upstream

import (
	"context"
	"net/http"
	"testing"
)

func TestSubmitWorkPayloadPostsReferenceForm(t *testing.T) {
	c := academicClient(t, nil, roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != workSubmitAPI || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.PostForm.Get("pyFlag") != "" || r.PostForm.Get("answerq1") != "B" || r.PostForm.Get("answertypeq1") != "0" {
			t.Fatalf("form=%v", r.PostForm)
		}
		return response(r, `{"status":true,"msg":"ok"}`), nil
	}))
	result, err := c.SubmitWorkPayload(context.Background(), "submit", map[string]string{
		"jobid": "work-1", "answerq1": "B", "answertypeq1": "0", "pyFlag": "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.JobID != "work-1" || result.Action != "submit" || result.State != "accepted" || !result.Verified {
		t.Fatalf("%+v", result)
	}
}

func TestSubmitWorkPayloadRejectsUpstreamFailure(t *testing.T) {
	c := academicClient(t, nil, roundtrip(func(r *http.Request) (*http.Response, error) {
		return response(r, `{"status":false,"msg":"作业已过期"}`), nil
	}))
	_, err := c.SubmitWorkPayload(context.Background(), "save", map[string]string{"jobid": "work-1"})
	if err == nil || SafeFault(err).Code != "WORK_UNAVAILABLE" {
		t.Fatalf("err=%v", err)
	}
}
