package upstream

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	ID                string `json:"id"`
	CourseID          string `json:"course_id"`
	ClassID           string `json:"class_id"`
	Title             string `json:"title"`
	Course            string `json:"course"`
	Status            string `json:"status"`
	Remaining         string `json:"remaining,omitempty"`
	DetailURL         string `json:"-"`
	SubmissionState   string `json:"submission_state"`
	AvailabilityState string `json:"availability_state"`
	RawStatus         string `json:"raw_status"`
}

// Only explicit evidence establishes submission; closed is not submitted.
func academicStates(raw string) (string, string) {
	submission, availability := "unknown", "unknown"
	switch strings.TrimSpace(raw) {
	case "未交", "未提交":
		submission = "pending"
	case "已交", "已提交", "已批阅":
		submission = "submitted"
	}
	switch strings.TrimSpace(raw) {
	case "未开始", "未开放":
		availability = "not_open"
	case "已截止", "已结束", "已关闭":
		availability = "closed"
	case "进行中", "可提交":
		availability = "open"
	}
	return submission, availability
}

type AcademicCoverage struct {
	CourseID string `json:"course_id"`
	ClassID  string `json:"class_id"`
	Complete bool   `json:"complete"`
	Code     string `json:"code,omitempty"`
}

func academicCode(e error) string {
	var f *Fault
	if errors.As(e, &f) {
		return f.Code
	}
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return "BUDGET_EXHAUSTED"
	}
	return "UPSTREAM_UNAVAILABLE"
}

func academicSelection(courses []Course, a map[string]any) ([]Course, error) {
	selected := []Course{}
	seen := map[string]bool{}
	for _, co := range courses {
		if s := str(a, "course"); s != "" && !strings.Contains(co.Name, s) {
			continue
		}
		if s := str(a, "course_id"); s != "" && co.ID != s {
			continue
		}
		if s := str(a, "class_id"); s != "" && co.ClassID != s {
			continue
		}
		key := co.ID + ":" + co.ClassID
		if seen[key] {
			continue
		}
		seen[key] = true
		selected = append(selected, co)
		if len(selected) > 4 {
			return nil, fault("AMBIGUOUS_COURSE", "query matches >4 classes; specify course_id/class_id or a more specific name")
		}
	}
	if len(selected) == 0 {
		return nil, fault("NOT_FOUND", "no enrolled course matches query")
	}
	return selected, nil
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
		submission, availability := academicStates(status)
		rows = append(rows, Assignment{ID: cid + ":" + cl + ":" + workID, CourseID: cid, ClassID: cl, Title: title, Status: status, RawStatus: status, SubmissionState: submission, AvailabilityState: availability, Remaining: rem, DetailURL: raw})
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
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	rows := []Assignment{}
	course, status := str(a, "course"), str(a, "status")
	if status != "" && status != "all" && status != "pending" && status != "submitted" && status != "unknown" {
		return nil, fault("INVALID_ARGUMENT", "status must be all, pending, submitted, or unknown")
	}
	lim, off := number(a, "limit", 50), number(a, "offset", 0)
	if lim < 1 || lim > 100 || off < 0 {
		return nil, fault("INVALID_ARGUMENT", "limit 1..100; offset >=0")
	}
	source := c.assignments
	complete := false
	coverage := c.coverage
	fetchedAt := c.assignmentsAt.UTC()
	details := []AcademicCoverage{}
	warnings := []string{}
	if course != "" || str(a, "course_id") != "" || str(a, "class_id") != "" {
		courses, e := c.fetchCourses(ctx)
		if e != nil {
			return nil, e
		}
		source = []Assignment{}
		selected, e := academicSelection(courses, a)
		if e != nil {
			return nil, e
		}
		complete = true
		for _, co := range selected {
			rr, ok, e := c.scanCourse(ctx, co)
			d := AcademicCoverage{CourseID: co.ID, ClassID: co.ClassID, Complete: ok && e == nil}
			if e != nil {
				d.Code = academicCode(e)
			} else if !ok {
				d.Code = "INCOMPLETE_SCAN"
			}
			details = append(details, d)
			if d.Code != "" {
				warnings = append(warnings, "assignment class scan incomplete: "+d.Code)
			}
			source = append(source, rr...)
			complete = complete && d.Complete
		}
		coverage = "matched_course_classes; completeness requires upstream total-count agreement"
		fetchedAt = time.Now().UTC()
	} else {
		if e := c.fetchAssignments(ctx); e != nil {
			return nil, e
		}
		source, coverage, fetchedAt = c.assignments, c.coverage, c.assignmentsAt.UTC()
		warnings = append(warnings, "global assignment feed is not full account coverage")
	}
	for _, r := range source {
		if course != "" && !strings.Contains(r.Course, course) {
			continue
		}
		r.RawStatus = r.Status
		r.SubmissionState, r.AvailabilityState = academicStates(r.Status)
		if status != "" && status != "all" && r.SubmissionState != status {
			continue
		}
		rows = append(rows, r)
	}
	total := len(rows)
	if off > total {
		off = total
	}
	end := off + lim
	if end > total {
		end = total
	}
	return map[string]any{"items": rows[off:end], "total_in_feed": total, "offset": off, "has_more": end < total, "coverage": coverage, "coverage_details": academicCoverageDetails(details), "warnings": warnings, "complete": complete, "fetched_at": fetchedAt, "timezone": "Asia/Shanghai"}, nil
}

func academicCoverageDetails(details []AcademicCoverage) map[string]any {
	checked, failed := 0, 0
	for _, d := range details {
		if d.Code != "" && d.Code != "INCOMPLETE_SCAN" {
			failed++
		} else {
			checked++
		}
	}
	return map[string]any{"classes": details, "sources_checked": checked, "sources_failed": failed}
}

func (c *Client) scanCourse(ctx context.Context, co Course) ([]Assignment, bool, error) {
	if e := ctx.Err(); e != nil {
		return []Assignment{}, false, e
	}
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
	consistentTotal := true
	for page := 1; page <= 20; page++ {
		if e := ctx.Err(); e != nil {
			return rows, false, e
		}
		q.Set("pageNum", strconv.Itoa(page))
		b, e = c.get(ctx, "https://mooc1.chaoxing.com/mooc-ans/mooc2/work/list?"+q.Encode())
		if e != nil {
			return rows, false, e
		}
		dd, _ := html.Parse(bytes.NewReader(b))
		ratio := regexp.MustCompile(`(?:^|\s)([0-9]+)/([0-9]+)(?:\s|$)`).FindStringSubmatch(text(dd))
		if len(ratio) == 3 {
			total, err := strconv.Atoi(ratio[2])
			if err == nil {
				if expected >= 0 && total != expected {
					consistentTotal = false
				}
				expected = total
			}
		}
		rr, e := parseAssignments(b)
		if e != nil {
			if expected == 0 && len(rows) == 0 && consistentTotal {
				return []Assignment{}, true, nil
			}
			return rows, false, e
		}
		added := 0
		for _, r := range rr {
			if r.CourseID != co.ID || r.ClassID != co.ClassID {
				return rows, false, fault("UPSTREAM_SCHEMA", "assignment row does not match requested course/class")
			}
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
			return rows, len(rows) == expected && consistentTotal, nil
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

type Exam struct {
	Title             string   `json:"title"`
	Course            string   `json:"course"`
	SubmissionState   string   `json:"submission_state"`
	AvailabilityState string   `json:"availability_state"`
	RawStatus         string   `json:"raw_status"`
	RawColumns        []string `json:"raw_columns"`
	Provenance        string   `json:"provenance"`
}

func parseExams(b []byte) (any, error) {
	doc, e := html.Parse(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	rows := [][]string{}
	headers := []string{}
	walk(doc, func(n *html.Node) {
		if n.Data == "th" {
			headers = append(headers, text(n))
		}
	})
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
	exams := []Exam{}
	for _, columns := range rows {
		exam := Exam{RawColumns: columns, SubmissionState: "unknown", AvailabilityState: "unknown", Provenance: "exam_list_table"}
		// Header evidence only: table positions are not an upstream contract.
		if len(columns) == len(headers) {
			for i, header := range headers {
				switch header {
				case "考试名称", "考试标题":
					exam.Title = columns[i]
				case "课程名称", "所属课程":
					exam.Course = columns[i]
				case "状态", "考试状态":
					exam.RawStatus = columns[i]
				}
			}
		}
		exam.SubmissionState, exam.AvailabilityState = academicStates(exam.RawStatus)
		exams = append(exams, exam)
	}
	return map[string]any{"items": rows, "exams": exams, "raw_headers": headers, "complete": false, "coverage": "exam_list_page_only; account coverage and pagination not established", "warnings": []string{"exam coverage incomplete; unmapped columns remain raw"}, "fetched_at": time.Now().UTC()}, nil
}

func (c *Client) examList(ctx context.Context) (any, error) {
	b, e := c.get(ctx, api+"/exam-ans/exam/test/examcode/examlist")
	if e != nil {
		return nil, e
	}
	return parseExams(b)
}

type Notice struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Content      string         `json:"content"`
	SendTime     string         `json:"send_time"`
	InsertTime   string         `json:"insert_time"`
	SourceType   string         `json:"source_type"`
	RawSource    any            `json:"raw_source,omitempty"`
	RawReadState any            `json:"raw_read_state,omitempty"`
	Raw          map[string]any `json:"raw,omitempty"`
	Provenance   string         `json:"provenance"`
}

type noticePageCursor struct {
	Upstream string `json:"u"`
	Offset   int    `json:"o"`
}

func decodeNoticeCursor(cursor string) (noticePageCursor, error) {
	if len(cursor) > 256 {
		return noticePageCursor{}, fault("INVALID_ARGUMENT", "cursor must be <=256 bytes")
	}
	if !strings.HasPrefix(cursor, "page:") {
		return noticePageCursor{Upstream: cursor}, nil
	}
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, "page:"))
	var p noticePageCursor
	if e != nil || json.Unmarshal(b, &p) != nil || p.Offset < 0 || len(p.Upstream) > 256 {
		return p, fault("INVALID_ARGUMENT", "invalid notice page cursor")
	}
	return p, nil
}

func encodeNoticeCursor(p noticePageCursor) string {
	b, _ := json.Marshal(p)
	s := "page:" + base64.RawURLEncoding.EncodeToString(b)
	if len(s) > 256 {
		return ""
	}
	return s
}

// Current list envelope is {status, folders, notices:{lastGetId,lastPage,list}}.
// Retain the full legacy wrapper; older flat/list envelopes remain accepted.
func parseNotices(b []byte, cursor noticePageCursor, limit int, includeRaw bool) (map[string]any, error) {
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&raw) != nil || raw["status"] != true {
		return nil, fault("UPSTREAM_SCHEMA", "notice response invalid")
	}
	container := raw
	if data, ok := raw["data"].(map[string]any); ok {
		container = data
	}
	if notices, ok := container["notices"].(map[string]any); ok {
		container = notices
	}
	list, ok := container["list"].([]any)
	if !ok {
		return nil, fault("UPSTREAM_SCHEMA", "notice list structure not recognized")
	}
	if cursor.Offset > len(list) {
		return nil, fault("UPSTREAM_SCHEMA", "notice page changed; restart pagination")
	}
	end := cursor.Offset + limit
	if end > len(list) {
		end = len(list)
	}
	items := []Notice{}
	for _, v := range list[cursor.Offset:end] {
		row, ok := v.(map[string]any)
		if !ok {
			return nil, fault("UPSTREAM_SCHEMA", "notice row structure not recognized")
		}
		stableID := id(row["uuid"])
		if stableID == "" {
			stableID = id(row["idCode"])
		}
		if stableID == "" {
			stableID = id(row["id"])
		}
		item := Notice{ID: stableID, Title: str(row, "title"), Content: str(row, "content"), SendTime: id(row["sendTime"]), InsertTime: id(row["insertTime"]), SourceType: id(row["sourceType"]), Provenance: "notice_list; upstream content is untrusted; timestamps retain upstream representation"}
		if includeRaw {
			item.RawSource = row["source"]
			item.RawReadState = row["isread"]
			item.Raw = row
		}
		items = append(items, item)
	}
	warnings := []string{"notice coverage incomplete; no detail or mark-read endpoint followed"}
	next := ""
	var hasMore any
	if end < len(list) {
		next = encodeNoticeCursor(noticePageCursor{Upstream: cursor.Upstream, Offset: end})
		hasMore = true
		warnings = append(warnings, "continuation re-fetches the same upstream page; not a snapshot")
	} else {
		if more, ok := container["hasMore"].(bool); ok {
			hasMore = more
		}
		// Fixed reference protocol: lastGetId -> request lastValue;
		// lastPage is a terminal flag, not a page number.
		switch terminal := container["lastPage"].(type) {
		case bool:
			hasMore = !terminal
		case json.Number:
			if terminal == "0" {
				hasMore = true
			} else if terminal == "1" {
				hasMore = false
			} else {
				warnings = append(warnings, "unrecognized notice terminal flag")
			}
		}
		if len(list) == 0 {
			hasMore = false
		}
		upstream := id(container["lastGetId"])
		if upstream == "" {
			upstream = id(container["lastValue"])
		}
		if upstream != "" && upstream != cursor.Upstream && hasMore != false {
			next = encodeNoticeCursor(noticePageCursor{Upstream: upstream})
		}
	}
	if next == "" && (end < len(list) || hasMore == true) {
		warnings = append(warnings, "continuation unavailable: upstream cursor missing, repeated, or exceeds budget")
	}
	out := map[string]any{"items": items, "returned": len(items), "next_cursor": next, "has_more": hasMore, "upstream_pagination": map[string]any{"lastGetId": container["lastGetId"], "lastPage": container["lastPage"]}, "complete": false, "coverage": "notice_list_page_only", "warnings": warnings, "fetched_at": time.Now().UTC()}
	if includeRaw {
		out["data"] = raw
	}
	return out, nil
}

func (c *Client) notices(ctx context.Context, a map[string]any) (any, error) {
	limit := number(a, "limit", 50)
	if limit < 1 || limit > 100 {
		return nil, fault("INVALID_ARGUMENT", "limit must be 1..100")
	}
	cursor, e := decodeNoticeCursor(str(a, "cursor"))
	if e != nil {
		return nil, e
	}
	// Establish session with a read first; only POST the documented read endpoint.
	if _, e := c.fetchCourses(ctx); e != nil {
		return nil, e
	}
	b, e := c.authenticatedRead(ctx, "POST", "https://notice.chaoxing.com/pc/notice/getNoticeList", url.Values{"type": {"0"}, "notice_type": {"0"}, "lastValue": {cursor.Upstream}, "sort": {"0"}})
	if e != nil {
		return nil, e
	}
	return parseNotices(b, cursor, limit, boolArg(a, "include_raw"))
}

// Overview is deliberately assignment-only until other sources have verified
// course identity/coverage. No implicit semester and no notice-derived tasks.
func (c *Client) studyOverview(ctx context.Context, a map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	courses, e := c.fetchCourses(ctx)
	if e != nil {
		return nil, e
	}
	selected, e := academicSelection(courses, a)
	if e != nil {
		return nil, e
	}
	return c.academicOverviewSelected(ctx, selected, number(a, "limit", 50))
}

func (c *Client) academicOverviewSelected(ctx context.Context, selected []Course, limit int) (any, error) {
	if limit < 1 || limit > 100 {
		return nil, fault("INVALID_ARGUMENT", "limit must be 1..100")
	}
	items := []Assignment{}
	details := []AcademicCoverage{}
	warnings := []string{"overview covers assignments only; exams/task points/notices not established"}
	complete := true
	total := 0
	for _, co := range selected {
		rows, ok, e := c.scanCourse(ctx, co)
		d := AcademicCoverage{CourseID: co.ID, ClassID: co.ClassID, Complete: ok && e == nil}
		if e != nil {
			d.Code = academicCode(e)
		} else if !ok {
			d.Code = "INCOMPLETE_SCAN"
		}
		if d.Code != "" {
			warnings = append(warnings, "assignment class scan incomplete: "+d.Code)
		}
		details = append(details, d)
		complete = complete && d.Complete
		total += len(rows)
		for _, row := range rows {
			if len(items) < limit {
				items = append(items, row)
			}
		}
	}
	return map[string]any{"items": items, "courses": selected, "complete": false, "assignments_complete": complete, "total_in_feed": total, "has_more": total > len(items), "coverage": "selected_course_assignment_lists_only", "coverage_details": academicCoverageDetails(details), "warnings": warnings, "provenance": "course_assignment_lists; no inferred tasks", "unsupported_sources": []string{"course_exams", "task_points", "notice_action_extraction"}, "fetched_at": time.Now().UTC()}, nil
}

func (c *Client) actionItems(ctx context.Context, a map[string]any) (any, error) {
	// Filter before the result limit, so submitted work cannot hide pending work.
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	courses, e := c.fetchCourses(ctx)
	if e != nil {
		return nil, e
	}
	selected, e := academicSelection(courses, a)
	if e != nil {
		return nil, e
	}
	limit := number(a, "limit", 50)
	if limit < 1 || limit > 100 {
		return nil, fault("INVALID_ARGUMENT", "limit must be 1..100")
	}
	items := []map[string]any{}
	details := []AcademicCoverage{}
	warnings := []string{"action items cover assignments only; unknown states require review, not presumed submitted"}
	total := 0
	for _, co := range selected {
		rows, ok, e := c.scanCourse(ctx, co)
		d := AcademicCoverage{CourseID: co.ID, ClassID: co.ClassID, Complete: ok && e == nil}
		if e != nil {
			d.Code = academicCode(e)
		} else if !ok {
			d.Code = "INCOMPLETE_SCAN"
		}
		if d.Code != "" {
			warnings = append(warnings, "assignment class scan incomplete: "+d.Code)
		}
		details = append(details, d)
		for _, row := range rows {
			if row.SubmissionState == "submitted" {
				continue
			}
			total++
			if len(items) >= limit {
				continue
			}
			action := "review_state"
			if row.SubmissionState == "pending" {
				action = "review_assignment"
			}
			items = append(items, map[string]any{"kind": "assignment", "action": action, "assignment": row, "inferred": false, "provenance": map[string]string{"source": "course_assignment_list", "course_id": row.CourseID, "class_id": row.ClassID, "id": row.ID}})
		}
	}
	return map[string]any{"items": items, "total_in_feed": total, "has_more": total > len(items), "complete": false, "coverage": "selected_course_assignment_lists_only; other action sources unsupported", "coverage_details": academicCoverageDetails(details), "warnings": warnings, "fetched_at": time.Now().UTC()}, nil
}
func assignmentAction(row Assignment) map[string]any {
	action := "review_state"
	if row.SubmissionState == "pending" {
		action = "review_assignment"
	}
	return map[string]any{"kind": "assignment", "action": action, "assignment": row, "inferred": false, "provenance": map[string]string{"source": "course_assignment_list", "course_id": row.CourseID, "class_id": row.ClassID, "id": row.ID}}
}

func assignmentActionItems(rows []Assignment, limit int) ([]map[string]any, int) {
	items := []map[string]any{}
	total := 0
	for _, row := range rows {
		if row.SubmissionState == "submitted" {
			continue
		}
		total++
		if len(items) < limit {
			items = append(items, assignmentAction(row))
		}
	}
	return items, total
}

func digestExamItem(exam Exam) map[string]any {
	return map[string]any{"kind": "exam", "action": "review_exam", "exam": exam, "inferred": false, "provenance": map[string]string{"source": "exam_list_table", "title": exam.Title, "course": exam.Course}}
}

func digestNoticeItem(notice Notice) map[string]any {
	content := notice.Content
	truncated := false
	runes := []rune(content)
	if len(runes) > 240 {
		content = string(runes[:240]) + "…"
		truncated = true
	}
	return map[string]any{"kind": "notice", "action": "read_notice", "notice": map[string]any{"id": notice.ID, "title": notice.Title, "content": content, "content_truncated": truncated, "send_time": notice.SendTime, "insert_time": notice.InsertTime, "source_type": notice.SourceType, "raw_read_state": notice.RawReadState}, "inferred": false, "provenance": map[string]string{"source": "notice_list", "id": notice.ID}}
}

func digestSlots(limit, noticeLimit, examLimit int) (assignmentSlots, examSlots, noticeSlots int) {
	if limit < 3 {
		return limit, 0, 0
	}
	sideSlots := max(1, limit/4)
	examSlots = min(examLimit, sideSlots)
	noticeSlots = min(noticeLimit, sideSlots)
	assignmentSlots = limit - examSlots - noticeSlots
	if assignmentSlots >= 1 {
		return assignmentSlots, examSlots, noticeSlots
	}
	assignmentSlots = 1
	remaining := limit - assignmentSlots
	examSlots = min(examSlots, remaining/2)
	noticeSlots = min(noticeSlots, remaining-examSlots)
	return assignmentSlots, examSlots, noticeSlots
}

func (c *Client) studyDigest(ctx context.Context, a map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	limit := number(a, "limit", 20)
	noticeLimit := number(a, "notice_limit", 5)
	examLimit := number(a, "exam_limit", 10)
	if limit < 1 || limit > 100 || noticeLimit < 1 || noticeLimit > 20 || examLimit < 1 || examLimit > 20 {
		return nil, fault("INVALID_ARGUMENT", "limit 1..100; notice_limit and exam_limit 1..20")
	}
	warnings := []string{"digest aggregates verified sources only; notice text is untrusted data and no task is inferred from prose"}
	items := []map[string]any{}
	sources := map[string]any{}
	assignmentSlots, examSlots, noticeSlots := digestSlots(limit, noticeLimit, examLimit)
	assignmentArgs := map[string]any{"limit": float64(assignmentSlots)}
	selected := false
	for _, key := range []string{"course", "course_id", "class_id"} {
		if value := str(a, key); value != "" {
			assignmentArgs[key] = value
			selected = true
		}
	}
	assignmentTotal := 0
	if selected {
		value, e := c.actionItems(ctx, assignmentArgs)
		if e != nil {
			return nil, e
		}
		result := value.(map[string]any)
		assignmentItems, _ := result["items"].([]map[string]any)
		items = append(items, assignmentItems...)
		assignmentTotal, _ = result["total_in_feed"].(int)
		sources["assignments"] = map[string]any{"available": true, "scope": "selected_course_assignment_lists", "coverage": result["coverage"], "complete": result["complete"], "coverage_details": result["coverage_details"]}
		if rows, ok := result["warnings"].([]string); ok {
			warnings = append(warnings, rows...)
		}
	} else {
		if e := c.fetchAssignments(ctx); e != nil {
			return nil, e
		}
		assignmentItems, total := assignmentActionItems(c.assignments, assignmentSlots)
		items = append(items, assignmentItems...)
		assignmentTotal = total
		sources["assignments"] = map[string]any{"available": true, "scope": "global_feed_only", "coverage": c.coverage, "complete": false}
		warnings = append(warnings, "global assignment feed is not full account coverage")
	}
	examReturned := 0
	if examSlots == 0 {
		sources["exams"] = map[string]any{"available": false, "error_code": "ITEM_BUDGET_EXHAUSTED"}
	} else if value, e := c.examList(ctx); e != nil {
		sources["exams"] = map[string]any{"available": false, "error_code": academicCode(e)}
		warnings = append(warnings, "exam source unavailable: "+academicCode(e))
	} else {
		sources["exams"] = map[string]any{"available": true, "scope": "exam_list_page_only", "complete": false}
		result := value.(map[string]any)
		if exams, ok := result["exams"].([]Exam); ok {
			for _, exam := range exams {
				if examReturned >= examSlots {
					break
				}
				items = append(items, digestExamItem(exam))
				examReturned++
			}
		}
	}
	noticeReturned := 0
	if noticeSlots == 0 {
		sources["notices"] = map[string]any{"available": false, "error_code": "ITEM_BUDGET_EXHAUSTED"}
	} else if value, e := c.notices(ctx, map[string]any{"limit": float64(noticeSlots)}); e != nil {
		sources["notices"] = map[string]any{"available": false, "error_code": academicCode(e)}
		warnings = append(warnings, "notice source unavailable: "+academicCode(e))
	} else {
		sources["notices"] = map[string]any{"available": true, "scope": "notice_list_first_page", "complete": false}
		result := value.(map[string]any)
		if notices, ok := result["items"].([]Notice); ok {
			for _, notice := range notices {
				items = append(items, digestNoticeItem(notice))
				noticeReturned++
			}
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return map[string]any{"items": items, "counts": map[string]any{"assignment_actions": assignmentTotal, "exams_returned": examReturned, "notices_returned": noticeReturned, "items_returned": len(items)}, "sources": sources, "complete": false, "coverage": "source_backed_digest; assignment/exam/notice coverage remains independent and partial", "warnings": warnings, "fetched_at": time.Now().UTC()}, nil
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
	if err := c.lock(ctx); err != nil {
		return err
	}
	defer c.queries.Release()
	return c.login(ctx)
}
func (c *Client) String() string { return fmt.Sprintf("Chaoxing read-only client") }
