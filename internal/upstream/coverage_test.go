package upstream

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDetailDoesNotDependOnCacheCapacity(t *testing.T) {
	c := testClient(t)
	c.courses = []Course{{ID: "1", ClassID: "2", CPI: "4", Name: "Example"}}
	c.coursesAt = time.Now()
	c.assignments = []Assignment{}
	c.assignmentsAt = time.Now()
	c.extraAssignments = map[string]Assignment{}
	for i := 0; i < 2000; i++ {
		c.extraAssignments[fmt.Sprint(i)] = Assignment{}
	}
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/visit/stucoursemiddle":
			return response(r, `<input id="workEnc" value="test"><input id="enc" value="test">`), nil
		case "/mooc-ans/mooc2/work/list":
			return response(r, `<span>0/1</span><li onclick="goTask(this);" data="https://mooc1-api.chaoxing.com/task?courseId=1&amp;classId=2&amp;workId=3"><p>Example work</p><span class="status">未交</span></li>`), nil
		case "/task":
			return response(r, `<p>开始时间：10-01 08:00 截止时间：10-10 20:00</p>`), nil
		default:
			t.Fatal("unexpected upstream path", r.URL.Path)
			return nil, nil
		}
	})
	got, e := c.Call(context.Background(), "get_assignment", map[string]any{"id": "1:2:3"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(fmt.Sprint(got), "10-10 20:00") {
		t.Fatal(got)
	}
}
func TestReloginInvalidatesSignedLinks(t *testing.T) {
	c := testClient(t)
	c.extraAssignments = map[string]Assignment{"1": {DetailURL: "https://mooc1.chaoxing.com/old"}}
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) { return response(r, `{"status":true}`), nil })
	if e := c.ForceLogin(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(c.extraAssignments) != 0 {
		t.Fatal("old signed links retained")
	}
}
