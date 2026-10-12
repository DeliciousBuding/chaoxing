package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type Course struct {
	ID      string `json:"course_id"`
	ClassID string `json:"class_id"`
	CPI     string `json:"cpi"`
	Name    string `json:"name"`
	Teacher string `json:"teacher"`
	Class   string `json:"class"`
	Retired bool   `json:"retired"`
}
type Assignment struct {
	ID        string `json:"id"`
	CourseID  string `json:"course_id"`
	ClassID   string `json:"class_id"`
	Title     string `json:"title"`
	Course    string `json:"course"`
	Status    string `json:"status"`
	Remaining string `json:"remaining,omitempty"`
	DetailURL string `json:"-"`
}

func id(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', 0, 64)
	}
	return ""
}
func (c *Client) fetchCourses(ctx context.Context) ([]Course, error) {
	if c.courses != nil && time.Since(c.coursesAt) < 2*time.Minute {
		return c.courses, nil
	}
	b, e := c.get(ctx, api+"/mycourse/backclazzdata?view=json&rss=1")
	if e != nil {
		return nil, e
	}
	var v struct {
		Result      int `json:"result"`
		ChannelList []struct {
			CPI     any `json:"cpi"`
			Content struct {
				ID      any    `json:"id"`
				CPI     any    `json:"cpi"`
				Name    string `json:"name"`
				Retired int    `json:"isretire"`
				Course  struct {
					Data []struct {
						ID      any    `json:"id"`
						Name    string `json:"name"`
						Teacher string `json:"teacherfactor"`
					} `json:"data"`
				} `json:"course"`
			} `json:"content"`
		} `json:"channelList"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&v) != nil || v.Result != 1 {
		return nil, fault("UPSTREAM_SCHEMA", "course response invalid")
	}
	rows := []Course{}
	for _, ch := range v.ChannelList {
		for _, d := range ch.Content.Course.Data {
			p := id(ch.Content.CPI)
			if p == "" {
				p = id(ch.CPI)
			}
			rows = append(rows, Course{id(d.ID), id(ch.Content.ID), p, d.Name, d.Teacher, ch.Content.Name, ch.Content.Retired == 1})
		}
	}
	c.courses = rows
	c.coursesAt = time.Now()
	return rows, nil
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		b.WriteString(text(ch))
		b.WriteByte(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		walk(ch, fn)
	}
}
func first(n *html.Node, fn func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(n, func(v *html.Node) {
		if found == nil && fn(v) {
			found = v
		}
	})
	return found
}
func hasClass(n *html.Node, s string) bool {
	for _, v := range strings.Fields(attr(n, "class")) {
		if v == s {
			return true
		}
	}
	return false
}
func parseAssignments(b []byte) ([]Assignment, error) {
	doc, e := html.Parse(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	rows := []Assignment{}
	walk(doc, func(n *html.Node) {
		if n.Data != "li" || !strings.Contains(attr(n, "onclick"), "goTask") {
			return
		}
		raw := attr(n, "data")
		u, e := url.Parse(raw)
		if e != nil || !safeURL(u) {
			return
		}
		q := u.Query()
		workID := q.Get("workId")
		if workID == "" {
			workID = q.Get("taskrefId")
		}
		cid := q.Get("courseId")
		cl := q.Get("classId")
		if cl == "" {
			cl = q.Get("clazzId")
		}
		if workID == "" || cid == "" {
			return
		}
		title := text(first(n, func(v *html.Node) bool { return v.Data == "p" }))
		status := text(first(n, func(v *html.Node) bool { return hasClass(v, "status") }))
		rem := text(first(n, func(v *html.Node) bool { return hasClass(v, "fr") }))
		rows = append(rows, Assignment{ID: cid + ":" + cl + ":" + workID, CourseID: cid, ClassID: cl, Title: title, Status: status, Remaining: rem, DetailURL: raw})
	})
	if len(rows) == 0 && !strings.Contains(text(doc), "暂无") && !strings.Contains(text(doc), "没有") {
		return nil, fault("UPSTREAM_SCHEMA", "assignment list structure not recognized")
	}
	return rows, nil
}

// The global feed may be capped by upstream. Never claim full account coverage.
func (c *Client) fetchAssignments(ctx context.Context) error {
	if c.assignments != nil && time.Since(c.assignmentsAt) < time.Minute {
		return nil
	}
	b, e := c.get(ctx, api+"/work/stu-work")
	if e != nil {
		return e
	}
	rows, e := parseAssignments(b)
	if e != nil {
		return e
	}
	courses, e := c.fetchCourses(ctx)
	if e != nil {
		return e
	}
	for i := range rows {
		for _, co := range courses {
			if co.ID == rows[i].CourseID && co.ClassID == rows[i].ClassID {
				rows[i].Course = co.Name
				break
			}
		}
	}
	c.assignments = rows
	c.assignmentsAt = time.Now()
	c.coverage = "global_feed_only; upstream pagination and per-course coverage not established"
	return nil
}
func (c *Client) listAssignments(ctx context.Context, a map[string]any) (any, error) {
	if e := c.fetchAssignments(ctx); e != nil {
		return nil, e
	}
	rows := []Assignment{}
	course, status := str(a, "course"), str(a, "status")
	source := c.assignments
	complete := false
	coverage := c.coverage
	if course != "" {
		courses, e := c.fetchCourses(ctx)
		if e != nil {
			return nil, e
		}
		source = []Assignment{}
		matched := 0
		complete = true
		for _, co := range courses {
			if strings.Contains(co.Name, course) {
				matched++
				if matched > 4 {
					return nil, fault("AMBIGUOUS_COURSE", "course query matches >4 classes; use a more specific name")
				}
				rr, ok, e := c.scanCourse(ctx, co)
				if e != nil {
					return nil, e
				}
				source = append(source, rr...)
				complete = complete && ok
			}
		}
		if matched == 0 {
			return nil, fault("NOT_FOUND", "no enrolled course matches query")
		}
		coverage = "matched_course_classes; completeness requires upstream total-count agreement"
	}
	if status != "" && status != "all" && status != "pending" && status != "submitted" {
		return nil, fault("INVALID_ARGUMENT", "status must be all, pending, or submitted")
	}
	for _, r := range source {
		if course != "" && !strings.Contains(r.Course, course) {
			continue
		}
		pending := r.Status == "未交" || r.Status == "未提交"
		if status == "pending" && !pending || status == "submitted" && pending {
			continue
		}
		rows = append(rows, r)
	}
	lim, off := number(a, "limit", 50), number(a, "offset", 0)
	if lim < 1 || lim > 100 || off < 0 {
		return nil, fault("INVALID_ARGUMENT", "limit 1..100; offset >=0")
	}
	total := len(rows)
	if off > total {
		off = total
	}
	end := off + lim
	if end > total {
		end = total
	}
	return map[string]any{"items": rows[off:end], "total_in_feed": total, "offset": off, "has_more": end < total, "coverage": coverage, "complete": complete, "fetched_at": time.Now().UTC(), "timezone": "Asia/Shanghai"}, nil
}

func (c *Client) scanCourse(ctx context.Context, co Course) ([]Assignment, bool, error) {
	q := url.Values{"courseid": {co.ID}, "clazzid": {co.ClassID}, "cpi": {co.CPI}, "ismooc2": {"1"}, "v": {"2"}}
	b, e := c.get(ctx, "https://mooc1.chaoxing.com/visit/stucoursemiddle?"+q.Encode())
	if e != nil {
		return nil, false, e
	}
	doc, e := html.Parse(bytes.NewReader(b))
	if e != nil {
		return nil, false, e
	}
	inputs := map[string]string{}
	walk(doc, func(n *html.Node) {
		if n.Data == "input" {
			inputs[attr(n, "id")] = attr(n, "value")
		}
	})
	if inputs["workEnc"] == "" {
		return nil, false, fault("COURSE_ACCESS", "course did not supply a work signature; no zero-work assumption")
	}
	q = url.Values{"courseId": {co.ID}, "classId": {co.ClassID}, "cpi": {co.CPI}, "ut": {"s"}, "enc": {inputs["workEnc"]}, "stuenc": {inputs["enc"]}, "openc": {inputs["openc"]}, "t": {strconv.FormatInt(time.Now().UnixMilli(), 10)}, "isdisplaytable": {"2"}}
	rows := []Assignment{}
	seen := map[string]bool{}
	expected := -1
	for page := 1; page <= 20; page++ {
		q.Set("pageNum", strconv.Itoa(page))
		b, e = c.get(ctx, "https://mooc1.chaoxing.com/mooc-ans/mooc2/work/list?"+q.Encode())
		if e != nil {
			return nil, false, e
		}
		dd, _ := html.Parse(bytes.NewReader(b))
		ratio := regexp.MustCompile(`(?:^|\s)([0-9]+)/([0-9]+)(?:\s|$)`).FindStringSubmatch(text(dd))
		if len(ratio) == 3 {
			expected, _ = strconv.Atoi(ratio[2])
		}
		rr, e := parseAssignments(b)
		if e != nil {
			if expected == 0 {
				return []Assignment{}, true, nil
			}
			return nil, false, e
		}
		added := 0
		for _, r := range rr {
			if !seen[r.ID] {
				r.Course = co.Name
				seen[r.ID] = true
				rows = append(rows, r)
				added++
				if c.extraAssignments == nil {
					c.extraAssignments = map[string]Assignment{}
				}
				if len(c.extraAssignments) >= 2000 {
					c.extraAssignments = map[string]Assignment{}
				}
				if len(c.extraAssignments) < 2000 {
					c.extraAssignments[r.ID] = r
				}
			}
		}
		if expected >= 0 && len(rows) >= expected {
			return rows, len(rows) == expected, nil
		}
		if added == 0 {
			return rows, false, nil
		}
		if len(rows) > 1000 {
			return rows, false, nil
		}
	}
	return rows, false, nil
}

var timePattern = regexp.MustCompile(`(?:开始时间|截止时间|作答时间)\s*[:：]\s*([0-9-]+\s+[0-9:]+)`)

func (c *Client) detail(ctx context.Context, aid string) (any, error) {
	if e := c.fetchAssignments(ctx); e != nil {
		return nil, e
	}
	var hit *Assignment
	for i := range c.assignments {
		if c.assignments[i].ID == aid {
			hit = &c.assignments[i]
			break
		}
	}
	if hit == nil {
		if r, ok := c.extraAssignments[aid]; ok {
			hit = &r
		} else {
			parts := strings.Split(aid, ":")
			if len(parts) == 3 {
				courses, e := c.fetchCourses(ctx)
				if e != nil {
					return nil, e
				}
				for _, co := range courses {
					if co.ID == parts[0] && co.ClassID == parts[1] {
						var scanned []Assignment
						scanned, _, e = c.scanCourse(ctx, co)
						if e != nil {
							return nil, e
						}
						for _, row := range scanned {
							if row.ID == aid {
								r := row
								hit = &r
								break
							}
						}
						break
					}
				}
				if r, ok := c.extraAssignments[aid]; ok {
					hit = &r
				}
			}
		}
		if hit == nil {
			return nil, fault("NOT_FOUND", "assignment not found in accessible course feed")
		}
	}
	b, e := c.get(ctx, hit.DetailURL)
	if e != nil {
		return nil, e
	}
	doc, e := html.Parse(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	t := text(doc)
	// Read only the summary/task page. Do not follow 'start answering' links.
	if strings.Contains(t, "用户登录") {
		return nil, fault("AUTH_REQUIRED", "assignment needs additional authorization")
	}
	out := map[string]any{"assignment": *hit, "summary": t, "fetched_at": time.Now().UTC(), "timezone": "Asia/Shanghai", "start_time": nil, "deadline": nil, "date_precision": "upstream_month_day; year not inferred"}
	for _, m := range timePattern.FindAllStringSubmatch(t, -1) {
		if strings.Contains(m[0], "开始") {
			out["start_time"] = m[1]
		}
		if strings.Contains(m[0], "截止") {
			out["deadline"] = m[1]
		}
	}
	combined := regexp.MustCompile(`作答时间\s*[:：]\s*([0-9-]+\s+[0-9:]+)\s*至\s*([0-9-]+\s+[0-9:]+)`).FindStringSubmatch(t)
	if len(combined) == 3 {
		out["start_time"] = combined[1]
		out["deadline"] = combined[2]
	}
	if len(t) > 16000 {
		out["summary"] = t[:16000]
		out["summary_truncated"] = true
	}
	return out, nil
}
func parseExams(b []byte) (any, error) {
	doc, e := html.Parse(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	rows := [][]string{}
	walk(doc, func(n *html.Node) {
		if n.Data == "tr" && hasClass(n, "dataTr") {
			r := []string{}
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				if ch.Data == "td" {
					r = append(r, text(ch))
				}
			}
			rows = append(rows, r)
		}
	})
	if len(rows) == 0 && !strings.Contains(text(doc), "暂无") {
		return nil, fault("UPSTREAM_SCHEMA", "exam table not recognized")
	}
	return map[string]any{"items": rows, "fetched_at": time.Now().UTC()}, nil
}
func (c *Client) notices(ctx context.Context) (any, error) {
	// Establish session with a read first; only POST the documented read endpoint.
	if _, e := c.fetchCourses(ctx); e != nil {
		return nil, e
	}
	b, e := c.authenticatedRead(ctx, "POST", "https://notice.chaoxing.com/pc/notice/getNoticeList", url.Values{"type": {"0"}, "notice_type": {"0"}, "lastValue": {""}, "sort": {"0"}})
	if e != nil {
		return nil, e
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil || v["status"] != true {
		return nil, fault("UPSTREAM_SCHEMA", "notice response not JSON")
	}
	return map[string]any{"data": v, "complete": false, "fetched_at": time.Now().UTC()}, nil
}
func (c *Client) schedule(ctx context.Context, a map[string]any) (any, error) {
	if _, e := c.fetchCourses(ctx); e != nil {
		return nil, e
	}
	week := number(a, "week", 1)
	if week < 1 || week > 30 {
		return nil, fault("INVALID_ARGUMENT", "week must be 1..30")
	}
	b, e := c.authenticatedRead(ctx, "POST", "https://kb.chaoxing.com/pc/curriculum/getMyLessons", url.Values{"week": {strconv.Itoa(week)}})
	if e != nil {
		return nil, e
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil || v["result"] != float64(1) {
		return nil, fault("UPSTREAM_SCHEMA", "schedule response not JSON")
	}
	return map[string]any{"data": v, "week": week, "fetched_at": time.Now().UTC()}, nil
}
func (c *Client) materials(ctx context.Context, a map[string]any) (any, error) {
	cid, cl, cpi := str(a, "course_id"), str(a, "class_id"), str(a, "cpi")
	courses, e := c.fetchCourses(ctx)
	if e != nil {
		return nil, e
	}
	matched := false
	for _, co := range courses {
		if co.ID == cid && co.ClassID == cl && co.CPI == cpi {
			matched = true
		}
	}
	if !matched {
		return nil, fault("INVALID_ARGUMENT", "course triple must match list_courses")
	}
	if str(a, "folder_id") != "" {
		return nil, fault("UNSUPPORTED", "folder traversal is not implemented")
	}
	q := url.Values{"courseid": {cid}, "clazzid": {cl}, "cpi": {cpi}, "ut": {"s"}}
	b, e := c.get(ctx, "https://mooc2-ans.chaoxing.com/mooc2-ans/coursedata/stu-datalist?"+q.Encode())
	if e != nil {
		return nil, e
	}
	doc, e := html.Parse(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	return map[string]any{"text": text(doc), "complete": false, "fetched_at": time.Now().UTC()}, nil
}
func (c *Client) ForceLogin(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.login(ctx)
}
func (c *Client) String() string { return fmt.Sprintf("Chaoxing read-only client") }
