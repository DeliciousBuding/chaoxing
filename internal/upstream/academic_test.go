package upstream

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func academicFixture(cid, class, work, status string) string {
	return fmt.Sprintf(`<li onclick="goTask(this)" data="https://mooc1-api.chaoxing.com/task?courseId=%s&amp;classId=%s&amp;workId=%s"><p>Synthetic assignment</p><span class="status">%s</span></li>`, cid, class, work, status)
}

func academicClient(t *testing.T, courses []Course, handler roundtrip) *Client {
	t.Helper()
	c := testClient(t)
	c.courses, c.coursesAt = courses, time.Now()
	c.http.Transport = handler
	return c
}

func TestAcademicStatesConservative(t *testing.T) {
	for _, tc := range []struct{ raw, submission, availability string }{
		{"未提交", "pending", "unknown"}, {"已提交", "submitted", "unknown"},
		{"已批阅", "submitted", "unknown"}, {"已截止", "unknown", "closed"},
		{"未开始", "unknown", "not_open"}, {"新状态", "unknown", "unknown"},
		{"", "unknown", "unknown"}, {"已完成", "unknown", "unknown"},
	} {
		rows, e := parseAssignments([]byte(academicFixture("synthetic-course", "synthetic-class", "synthetic-work", tc.raw)))
		if e != nil || len(rows) != 1 {
			t.Fatalf("%v %v", rows, e)
		}
		r := rows[0]
		if r.RawStatus != tc.raw || r.Status != tc.raw || r.SubmissionState != tc.submission || r.AvailabilityState != tc.availability {
			t.Fatalf("%+v", r)
		}
	}
}

func TestAcademicExactSelectionAndPartialPreservation(t *testing.T) {
	courses := []Course{{ID: "synthetic-course", ClassID: "class-a", Name: "Synthetic"}, {ID: "synthetic-course", ClassID: "class-b", Name: "Synthetic"}, {ID: "other-course", ClassID: "class-c", Name: "Synthetic"}}
	requested := []string{}
	c := academicClient(t, courses, roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/visit/stucoursemiddle" {
			class := r.URL.Query().Get("clazzid")
			requested = append(requested, class)
			if class == "class-b" {
				return response(r, `<p>No signature</p>`), nil
			}
			return response(r, `<input id="workEnc" value="synthetic">`), nil
		}
		if r.URL.Path != "/mooc-ans/mooc2/work/list" {
			t.Fatalf("unexpected read %s", r.URL.Path)
		}
		return response(r, `<span>0/2</span>`+academicFixture("synthetic-course", "class-a", "a", "已提交")+academicFixture("synthetic-course", "class-a", "b", "已截止")), nil
	}))
	v, e := c.listAssignments(context.Background(), map[string]any{"course_id": "synthetic-course", "status": "submitted"})
	if e != nil {
		t.Fatal(e)
	}
	out := v.(map[string]any)
	items := out["items"].([]Assignment)
	if len(items) != 1 || items[0].SubmissionState != "submitted" || out["complete"] != false {
		t.Fatal(out)
	}
	if !reflect.DeepEqual(requested, []string{"class-a", "class-b"}) {
		t.Fatal(requested)
	}
	details := out["coverage_details"].(map[string]any)
	if details["sources_failed"] != 1 || len(out["warnings"].([]string)) != 1 {
		t.Fatal(out)
	}
	requested = nil
	v, e = c.listAssignments(context.Background(), map[string]any{"course_id": "synthetic-course", "class_id": "class-a", "status": "unknown"})
	if e != nil {
		t.Fatal(e)
	}
	if len(v.(map[string]any)["items"].([]Assignment)) != 1 || !reflect.DeepEqual(requested, []string{"class-a"}) {
		t.Fatal(v, requested)
	}
}

func TestAcademicSelectionPreflight(t *testing.T) {
	courses := []Course{}
	for i := 0; i < 5; i++ {
		courses = append(courses, Course{ID: "synthetic", ClassID: fmt.Sprint(i)})
	}
	c := academicClient(t, courses, roundtrip(func(r *http.Request) (*http.Response, error) { t.Fatal("preflight must not read"); return nil, nil }))
	if _, e := c.listAssignments(context.Background(), map[string]any{"course_id": "synthetic"}); e == nil {
		t.Fatal("unbounded selection")
	}
	if _, e := c.studyOverview(context.Background(), nil); e == nil {
		t.Fatal("unbounded overview")
	}
	if _, e := c.actionItems(context.Background(), nil); e == nil {
		t.Fatal("unbounded action selection")
	}
	if _, e := c.listAssignments(context.Background(), map[string]any{"status": "invented"}); e == nil {
		t.Fatal("invalid status")
	}
	if _, e := c.listAssignments(context.Background(), map[string]any{"limit": float64(101)}); e == nil {
		t.Fatal("invalid limit")
	}
}

func TestAcademicScanKeepsEarlierPages(t *testing.T) {
	co := Course{ID: "synthetic", ClassID: "class"}
	c := academicClient(t, []Course{co}, roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/visit/stucoursemiddle" {
			return response(r, `<input id="workEnc" value="synthetic">`), nil
		}
		if r.URL.Query().Get("pageNum") == "1" {
			return response(r, `<span>0/2</span>`+academicFixture(co.ID, co.ClassID, "a", "未交")), nil
		}
		return response(r, `<p>Maintenance</p>`), nil
	}))
	rows, complete, e := c.scanCourse(context.Background(), co)
	if len(rows) != 1 || complete || e == nil {
		t.Fatal(rows, complete, e)
	}
}

func TestAcademicScanBoundsCountsAndIdentity(t *testing.T) {
	for _, mode := range []string{"page-cap", "mismatch", "empty", "wrong-class", "repeat"} {
		t.Run(mode, func(t *testing.T) {
			co := Course{ID: "synthetic", ClassID: "class"}
			requests := 0
			c := academicClient(t, []Course{co}, roundtrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/visit/stucoursemiddle" {
					return response(r, `<input id="workEnc" value="synthetic">`), nil
				}
				requests++
				page := r.URL.Query().Get("pageNum")
				switch mode {
				case "empty":
					return response(r, `<p>暂无作业</p><span>0/0</span>`), nil
				case "wrong-class":
					return response(r, `<span>0/1</span>`+academicFixture(co.ID, "other-class", page, "未交")), nil
				case "mismatch":
					return response(r, `<span>0/1</span>`+academicFixture(co.ID, co.ClassID, "a", "未交")+academicFixture(co.ID, co.ClassID, "b", "未交")), nil
				case "repeat":
					return response(r, `<span>0/3</span>`+academicFixture(co.ID, co.ClassID, "a", "未交")), nil
				default:
					return response(r, `<span>0/99</span>`+academicFixture(co.ID, co.ClassID, page, "未交")), nil
				}
			}))
			rows, complete, e := c.scanCourse(context.Background(), co)
			if mode == "empty" {
				if e != nil || !complete || len(rows) != 0 {
					t.Fatal(rows, complete, e)
				}
				return
			}
			if complete {
				t.Fatal("false complete")
			}
			if mode == "page-cap" && (requests != 20 || len(rows) != 20) {
				t.Fatal(requests, len(rows))
			}
			if mode == "wrong-class" && (e == nil || len(rows) != 0) {
				t.Fatal(rows, e)
			}
		})
	}
}

func TestAcademicGlobalCacheFreshnessAndUnknown(t *testing.T) {
	c := academicClient(t, nil, roundtrip(func(r *http.Request) (*http.Response, error) { t.Fatal("cache should avoid reads"); return nil, nil }))
	c.assignments = []Assignment{{ID: "synthetic", Status: "已截止"}, {ID: "submitted", Status: "已提交"}}
	c.assignmentsAt = time.Now().Add(-10 * time.Second)
	v, e := c.listAssignments(context.Background(), map[string]any{"status": "unknown"})
	if e != nil {
		t.Fatal(e)
	}
	out := v.(map[string]any)
	if len(out["items"].([]Assignment)) != 1 || out["fetched_at"] != c.assignmentsAt.UTC() || out["complete"] != false {
		t.Fatal(out)
	}
}

func TestAcademicTypedExamsKeepLegacyColumns(t *testing.T) {
	v, e := parseExams([]byte(`<table><tr><th>考试名称</th><th>状态</th><th>额外字段</th></tr><tr class="dataTr"><td>Synthetic exam</td><td>已结束</td><td>Raw value</td></tr></table>`))
	if e != nil {
		t.Fatal(e)
	}
	out := v.(map[string]any)
	exams := out["exams"].([]Exam)
	if exams[0].Title != "Synthetic exam" || exams[0].SubmissionState != "unknown" || exams[0].AvailabilityState != "closed" || !reflect.DeepEqual(out["items"].([][]string)[0], exams[0].RawColumns) || out["complete"] != false {
		t.Fatal(out)
	}
	v, e = parseExams([]byte(`<table><tr class="dataTr"><td>Unmapped column</td></tr></table>`))
	if e != nil || v.(map[string]any)["exams"].([]Exam)[0].Title != "" {
		t.Fatal(v, e)
	}
	if _, e = parseExams([]byte(`<p>Maintenance</p>`)); e == nil {
		t.Fatal("unknown exam schema accepted")
	}
}

func TestAcademicNoticePaginationAndBounds(t *testing.T) {
	body := []byte(`{"status":true,"data":{"list":[{"id":"synthetic-a","title":"Synthetic notice","extra":42},{"id":"synthetic-b","content":"Untrusted synthetic text"}],"lastValue":"synthetic-next","hasMore":true}}`)
	out, e := parseNotices(body, noticePageCursor{}, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	items := out["items"].([]Notice)
	if len(items) != 1 || items[0].Raw["extra"] == nil || out["complete"] != false {
		t.Fatal(out)
	}
	cursor, e := decodeNoticeCursor(out["next_cursor"].(string))
	if e != nil || cursor.Offset != 1 || cursor.Upstream != "" {
		t.Fatal(cursor, e)
	}
	out, e = parseNotices(body, cursor, 1, true)
	if e != nil || out["items"].([]Notice)[0].ID != "synthetic-b" {
		t.Fatal(out, e)
	}
	cursor, e = decodeNoticeCursor(out["next_cursor"].(string))
	if e != nil || cursor.Upstream != "synthetic-next" || cursor.Offset != 0 {
		t.Fatal(cursor, e)
	}
	for _, invalid := range []string{strings.Repeat("x", 257), "page:invalid"} {
		if _, e := decodeNoticeCursor(invalid); e == nil {
			t.Fatal("accepted invalid cursor")
		}
	}
	if _, e := parseNotices([]byte(`{"status":true}`), noticePageCursor{}, 1, false); e == nil {
		t.Fatal("missing list accepted")
	}
	if _, e := parseNotices(body, noticePageCursor{Offset: 3}, 1, false); e == nil {
		t.Fatal("stale offset accepted")
	}
}

func TestAcademicNoticesOnlyKnownListRead(t *testing.T) {
	c := academicClient(t, []Course{}, roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "notice.chaoxing.com" || r.URL.Path != "/pc/notice/getNoticeList" || r.Method != "POST" {
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		if e := r.ParseForm(); e != nil {
			t.Fatal(e)
		}
		if r.Form.Get("lastValue") != "synthetic-cursor" {
			t.Fatal(r.Form)
		}
		// No guessed read-state flag: this is solely the verified list request.
		if len(r.Form) != 4 || r.Form.Get("type") != "0" || r.Form.Get("notice_type") != "0" || r.Form.Get("sort") != "0" {
			t.Fatal("unexpected list parameters", r.Form)
		}
		return response(r, `{"status":true,"list":[],"hasMore":false}`), nil
	}))
	if _, e := c.notices(context.Background(), map[string]any{"cursor": "synthetic-cursor", "limit": float64(1)}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.notices(context.Background(), map[string]any{"limit": float64(0)}); e == nil {
		t.Fatal("zero limit")
	}
}

func TestAcademicVerifiedNoticeWrapper(t *testing.T) {
	body := []byte(`{"status":true,"folders":[],"notices":{"lastGetId":"synthetic-next","lastPage":1,"list":[{"uuid":"synthetic-uuid","idCode":"synthetic-code","title":"Synthetic","content":"Untrusted","sendTime":"2026-01-01 10:00","insertTime":123,"sourceType":7,"source":{"synthetic":true},"isread":0,"attachment":[],"rft":"Raw rich content"},{"idCode":"synthetic-fallback"},{"id":"synthetic-legacy"}]}}`)
	out, e := parseNotices(body, noticePageCursor{}, 2, true)
	if e != nil {
		t.Fatal(e)
	}
	items := out["items"].([]Notice)
	if items[0].ID != "synthetic-uuid" || items[1].ID != "synthetic-fallback" || items[0].SendTime != "2026-01-01 10:00" || items[0].InsertTime != "123" || items[0].SourceType != "7" || items[0].RawSource == nil || items[0].Raw["rft"] != "Raw rich content" {
		t.Fatal(items)
	}
	if out["data"].(map[string]any)["folders"] == nil || out["complete"] != false {
		t.Fatal(out)
	}
	cursor, e := decodeNoticeCursor(out["next_cursor"].(string))
	if e != nil {
		t.Fatal(e)
	}
	out, e = parseNotices(body, cursor, 2, true)
	if e != nil || out["items"].([]Notice)[0].ID != "synthetic-legacy" || out["next_cursor"] != "" || out["has_more"] != false {
		t.Fatal(out, e)
	}
	if out["upstream_pagination"].(map[string]any)["lastGetId"] != "synthetic-next" {
		t.Fatal(out)
	}
}

func TestAcademicVerifiedNoticeCrossPageProtocol(t *testing.T) {
	requests := 0
	c := academicClient(t, []Course{}, roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Host != "notice.chaoxing.com" || r.URL.Path != "/pc/notice/getNoticeList" {
			t.Fatal("unexpected read")
		}
		if e := r.ParseForm(); e != nil {
			t.Fatal(e)
		}
		requests++
		if requests == 1 {
			if r.Form.Get("lastValue") != "" {
				t.Fatal(r.Form)
			}
			return response(r, `{"status":true,"notices":{"lastGetId":"synthetic-next","lastPage":0,"list":[{"uuid":"synthetic-first"}]}}`), nil
		}
		if r.Form.Get("lastValue") != "synthetic-next" {
			t.Fatal(r.Form)
		}
		return response(r, `{"status":true,"notices":{"lastGetId":"synthetic-final","lastPage":true,"list":[{"uuid":"synthetic-second"}]}}`), nil
	}))
	v, e := c.notices(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	out := v.(map[string]any)
	if out["has_more"] != true || out["next_cursor"] == "" || out["complete"] != false || out["data"] != nil {
		t.Fatal(out)
	}
	if out["items"].([]Notice)[0].Raw != nil {
		t.Fatal("raw notice exposed by default")
	}
	v, e = c.notices(context.Background(), map[string]any{"cursor": out["next_cursor"]})
	if e != nil {
		t.Fatal(e)
	}
	out = v.(map[string]any)
	if out["has_more"] != false || out["next_cursor"] != "" || out["items"].([]Notice)[0].ID != "synthetic-second" || out["complete"] != false {
		t.Fatal(out)
	}
	out, e = parseNotices([]byte(`{"status":true,"notices":{"lastGetId":"synthetic-repeat","lastPage":0,"list":[{"uuid":"synthetic"}]}}`), noticePageCursor{Upstream: "synthetic-repeat"}, 50, false)
	if e != nil || out["next_cursor"] != "" || out["has_more"] != true || len(out["warnings"].([]string)) < 2 {
		t.Fatal(out, e)
	}
}

func TestAcademicOverviewActionsBoundedAndSourceBacked(t *testing.T) {
	co := Course{ID: "synthetic", ClassID: "class"}
	c := academicClient(t, []Course{co}, roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/visit/stucoursemiddle" {
			return response(r, `<input id="workEnc" value="synthetic">`), nil
		}
		if r.URL.Path != "/mooc-ans/mooc2/work/list" {
			t.Fatal("unexpected non-assignment source")
		}
		return response(r, `<span>0/3</span>`+academicFixture(co.ID, co.ClassID, "submitted", "已提交")+academicFixture(co.ID, co.ClassID, "pending", "未交")+academicFixture(co.ID, co.ClassID, "unknown", "新状态")), nil
	}))
	v, e := c.studyOverview(context.Background(), map[string]any{"limit": float64(1)})
	if e != nil {
		t.Fatal(e)
	}
	out := v.(map[string]any)
	if len(out["items"].([]Assignment)) != 1 || out["complete"] != false || out["assignments_complete"] != true || out["has_more"] != true {
		t.Fatal(out)
	}
	v, e = c.actionItems(context.Background(), map[string]any{"limit": float64(1)})
	if e != nil {
		t.Fatal(e)
	}
	out = v.(map[string]any)
	items := out["items"].([]map[string]any)
	if len(items) != 1 || items[0]["inferred"] != false || items[0]["assignment"].(Assignment).SubmissionState != "pending" || out["total_in_feed"] != 2 || out["has_more"] != true {
		t.Fatal(out)
	}
}

func TestDigestSlotsPreserveVisibleSections(t *testing.T) {
	cases := []struct {
		limit, notice, exam int
		want                [3]int
	}{
		{1, 5, 10, [3]int{1, 0, 0}},
		{3, 5, 10, [3]int{1, 1, 1}},
		{5, 1, 1, [3]int{3, 1, 1}},
		{20, 5, 10, [3]int{10, 5, 5}},
		{100, 20, 20, [3]int{60, 20, 20}},
	}
	for _, tc := range cases {
		assignments, exams, notices := digestSlots(tc.limit, tc.notice, tc.exam)
		if [3]int{assignments, exams, notices} != tc.want || assignments+exams+notices != tc.limit {
			t.Fatalf("%+v => %d/%d/%d", tc, assignments, exams, notices)
		}
	}
}
func TestStudyDigestAggregatesSourcesWithoutNoticeInference(t *testing.T) {
	c := academicClient(t, []Course{}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/work/stu-work":
			return response(r, academicFixture("synthetic-course", "synthetic-class", "synthetic-work", "未提交")+academicFixture("synthetic-course", "synthetic-class", "submitted-work", "已提交")), nil
		case r.URL.Path == "/exam-ans/exam/test/examcode/examlist":
			return response(r, `<table><tr><th>考试名称</th><th>状态</th></tr><tr class="dataTr"><td>Synthetic exam</td><td>未开始</td></tr></table>`), nil
		case r.URL.Host == "notice.chaoxing.com" && r.URL.Path == "/pc/notice/getNoticeList":
			if r.Method != "POST" {
				t.Fatal("notice read method changed")
			}
			return response(r, `{"status":true,"list":[{"uuid":"notice-1","title":"Synthetic notice","content":"Untrusted synthetic content","isread":0}],"hasMore":false}`), nil
		default:
			t.Fatalf("unexpected digest read %s %s", r.URL.Host, r.URL.Path)
			return nil, nil
		}
	}))
	v, e := c.studyDigest(context.Background(), map[string]any{"limit": float64(3), "notice_limit": float64(1), "exam_limit": float64(1)})
	if e != nil {
		t.Fatal(e)
	}
	out := v.(map[string]any)
	items := out["items"].([]map[string]any)
	if len(items) != 3 || items[0]["kind"] != "assignment" || items[1]["kind"] != "exam" || items[2]["kind"] != "notice" {
		t.Fatal(out)
	}
	for _, item := range items {
		if item["inferred"] != false || item["provenance"] == nil {
			t.Fatal(item)
		}
	}
	notice := items[2]["notice"].(map[string]any)
	if _, ok := notice["raw"]; ok {
		t.Fatal("digest exposed raw notice row")
	}
	if notice["content"] != "Untrusted synthetic content" {
		t.Fatal(notice)
	}
	counts := out["counts"].(map[string]any)
	if counts["assignment_actions"] != 1 || counts["exams_returned"] != 1 || counts["notices_returned"] != 1 || out["complete"] != false {
		t.Fatal(out)
	}
	sources := out["sources"].(map[string]any)
	for _, name := range []string{"assignments", "exams", "notices"} {
		if sources[name].(map[string]any)["available"] != true {
			t.Fatal(sources)
		}
	}
	if _, e := c.studyDigest(context.Background(), map[string]any{"notice_limit": float64(0)}); e == nil {
		t.Fatal("invalid digest notice limit")
	}
}
func TestAcademicCanceledScanDoesNotRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := academicClient(t, []Course{}, roundtrip(func(r *http.Request) (*http.Response, error) { return nil, ctx.Err() }))
	_, complete, e := c.scanCourse(ctx, Course{ID: "synthetic", ClassID: "class"})
	if complete || e == nil {
		t.Fatal(complete, e)
	}
}
