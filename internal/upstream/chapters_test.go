package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

const chapterFixture = `<p>已完成任务点: <span>2</span>/5</p><ul>
<li><div class="chapter_item" title="Completed"><span class="catalog_sbar">1.1</span></div><span class="icon_yiwanc"></span></li>
<li><div class="chapter_item" title="Pending"><span class="catalog_sbar">1.2</span></div><span class="catalog_points_yi">3</span></li>
<li><div class="chapter_item" title="Unknown"><span class="catalog_sbar">1.3</span></div></li></ul>
<script src="https://fystat-ans.chaoxing.com/log/setlog?enc=secret"></script>
<iframe src="https://mooc1.chaoxing.com/mooc-ans/knowledge/cards"></iframe>
<a href="https://mooc1.chaoxing.com/mooc-ans/mycourse/studentstudyAjax">start</a>`

func TestChaptersReadGraph(t *testing.T) {
	c := testClient(t)
	c.courses = []Course{{ID: "11", ClassID: "22", CPI: "33"}, {ID: "11", ClassID: "44", CPI: "55"}}
	c.coursesAt = time.Now()
	args := map[string]any{"course_id": "11", "class_id": "22", "cpi": "33"}
	calls := 0
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.Body != nil {
			t.Fatal("non-read request")
		}
		q := r.URL.Query()
		if q.Get("courseid") != "11" || q.Get("clazzid") != "22" || q.Get("cpi") != "33" {
			t.Fatal("wrong triple")
		}
		switch r.URL.Host + r.URL.Path {
		case "mooc1.chaoxing.com/visit/stucoursemiddle":
			return response(r, `<input id="enc" value="synthetic-enc"><input id="openc" value="synthetic-openc">`), nil
		case "mooc2-ans.chaoxing.com/mooc2-ans/mycourse/studentcourse":
			if q.Get("enc") != "synthetic-enc" || q.Get("openc") != "synthetic-openc" || q.Get("ut") != "s" || q.Get("fromMiddle") != "1" {
				t.Fatal("bad catalog query")
			}
			return response(r, chapterFixture), nil
		default:
			t.Fatal("unexpected endpoint", r.URL.Path)
			return nil, nil
		}
	})
	v, err := c.chapters(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	out := v.(map[string]any)
	if calls != 2 || out["complete"] != false || out["scope"] != "chapter_summary" || *out["completed_tasks"].(*int) != 2 || *out["total_tasks"].(*int) != 5 {
		t.Fatal(out, calls)
	}
	items := out["items"].([]chapterSummaryItem)
	if len(items) != 3 || items[0].State != "completed" || items[1].State != "pending" || *items[1].PendingTasks != 3 || items[2].State != "unknown" || items[2].PendingTasks != nil {
		t.Fatal(items)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "synthetic-enc") || strings.Contains(string(b), "https://") || len(out["warnings"].([]string)) == 0 {
		t.Fatal("unsafe output")
	}
	args["class_id"] = "44"
	if _, err := c.chapters(context.Background(), args); err == nil || calls != 2 {
		t.Fatal("mixed triple accepted")
	}
}

func TestChaptersParseBoundsAndUnknown(t *testing.T) {
	out, err := parseChapterSummary([]byte(chapterFixture), 1, 1)
	if err != nil || out["returned"] != 1 || out["has_more"] != true || out["items"].([]chapterSummaryItem)[0].Number != "1.2" {
		t.Fatal(out, err)
	}
	out, err = parseChapterSummary([]byte(`<li><div class="chapter_item" title="`+strings.Repeat("章", 500)+`"><span class="catalog_sbar">1</span></div></li>`), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	item := out["items"].([]chapterSummaryItem)[0]
	if item.State != "unknown" || item.PendingTasks != nil || out["total_tasks"].(*int) != nil || len([]rune(item.Title)) != 241 {
		t.Fatal(out)
	}
	out, err = parseChapterSummary([]byte(chapterFixture), 100, 100000)
	if err != nil || out["returned"] != 0 {
		t.Fatal(out, err)
	}
	for _, body := range []string{`unrecognized`, `<p>已完成任务点: 9/3</p>`, `<script>已完成任务点: 0/0</script>`} {
		if _, err := parseChapterSummary([]byte(body), 50, 0); err == nil {
			t.Fatal("bad page accepted")
		}
	}
	for _, args := range []map[string]any{{"limit": 0}, {"limit": 101}, {"limit": 1.5}, {"offset": -1}} {
		c := testClient(t)
		if _, err := c.chapters(context.Background(), args); err == nil {
			t.Fatal("bad bounds accepted")
		}
	}
}

func TestChapterGETRedirectAndAuthentication(t *testing.T) {
	c := testClient(t)
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/visit/stucoursemiddle" {
			t.Fatal("followed redirect")
		}
		resp := response(r, "")
		resp.StatusCode = 302
		resp.Header.Set("Location", "https://mooc1.chaoxing.com/mooc-ans/mycourse/studentstudyAjax")
		return resp, nil
	})
	if _, err := c.chapterGET(context.Background(), "https://mooc1.chaoxing.com/visit/stucoursemiddle"); err == nil {
		t.Fatal("redirect accepted")
	}
	for _, rejected := range []bool{false, true} {
		c := testClient(t)
		gets, logins := 0, 0
		const endpoint = "https://mooc2-ans.chaoxing.com/mooc2-ans/mycourse/studentcourse?enc=synthetic"
		c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/fanyalogin" {
				logins++
				return response(r, `{"status":true}`), nil
			}
			if r.Method != "GET" || r.URL.String() != endpoint {
				t.Fatal("retry changed endpoint")
			}
			gets++
			if gets == 1 || rejected {
				return response(r, `请重新登录`), nil
			}
			return response(r, chapterFixture), nil
		})
		_, err := c.chapterGET(context.Background(), endpoint)
		if rejected {
			f, ok := err.(*Fault)
			if !ok || f.Code != "AUTH_REQUIRED" {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if gets != 2 || logins != 1 {
			t.Fatal(gets, logins)
		}
	}
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) { return response(r, `没有此页面访问权限`), nil })
	_, err := c.chapterGET(context.Background(), "https://mooc1.chaoxing.com/visit/stucoursemiddle")
	if f, ok := err.(*Fault); !ok || f.Code != "COURSE_ACCESS" {
		t.Fatal(err)
	}
}
