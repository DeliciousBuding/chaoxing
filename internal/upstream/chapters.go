package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Protocol reference: DanMo661/Xuexitong-mcp client.py, revision
// ebce2cc735bc7acc45fac7ccf32bcefe1a6b2a54 (fetch_chapters).
// Only the middle page and chapter catalog are read, never study pages/cards.
func (c *Client) chapters(ctx context.Context, args map[string]any) (any, error) {
	limit, err := chapterInteger(args, "limit", 50, 1, 100)
	if err != nil {
		return nil, err
	}
	offset, err := chapterInteger(args, "offset", 0, 0, 100000)
	if err != nil {
		return nil, err
	}
	courseID, classID, cpi := str(args, "course_id"), str(args, "class_id"), str(args, "cpi")
	if courseID == "" || classID == "" || cpi == "" {
		return nil, fault("INVALID_ARGUMENT", "exact course_id, class_id and cpi required")
	}
	courses, err := c.fetchCourses(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, co := range courses {
		if co.ID == courseID && co.ClassID == classID && co.CPI == cpi {
			found = true
			break
		}
	}
	if !found {
		return nil, fault("COURSE_ACCESS", "course triple is not enrolled")
	}
	q := url.Values{"courseid": {courseID}, "clazzid": {classID}, "cpi": {cpi}, "ismooc2": {"1"}, "v": {"2"}}
	b, err := c.chapterGET(ctx, "https://mooc1.chaoxing.com/visit/stucoursemiddle?"+q.Encode())
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return nil, fault("UPSTREAM_SCHEMA", "invalid course middle page")
	}
	inputs := map[string]string{}
	walk(doc, func(n *html.Node) {
		if n.Data == "input" {
			inputs[attr(n, "id")] = attr(n, "value")
		}
	})
	if inputs["enc"] == "" || inputs["openc"] == "" {
		return nil, fault("COURSE_ACCESS", "chapter signatures unavailable")
	}
	q = url.Values{"courseid": {courseID}, "clazzid": {classID}, "cpi": {cpi}, "enc": {inputs["enc"]}, "openc": {inputs["openc"]}, "fromMiddle": {"1"}, "ut": {"s"}, "t": {strconv.FormatInt(time.Now().UnixMilli(), 10)}}
	b, err = c.chapterGET(ctx, "https://mooc2-ans.chaoxing.com/mooc2-ans/mycourse/studentcourse?"+q.Encode())
	if err != nil {
		return nil, err
	}
	result, err := parseChapterSummary(b, limit, offset)
	if err != nil {
		return nil, err
	}
	result["course_id"], result["class_id"], result["cpi"] = courseID, classID, cpi
	result["fetched_at"] = time.Now().UTC()
	return result, nil
}

func chapterInteger(args map[string]any, key string, def, min, max int) (int, error) {
	v, ok := args[key]
	if !ok {
		return def, nil
	}
	var n int
	switch x := v.(type) {
	case int:
		n = x
	case float64:
		if math.IsNaN(x) || x < float64(min) || x > float64(max) || math.Trunc(x) != x {
			return 0, fault("INVALID_ARGUMENT", "invalid chapter pagination")
		}
		n = int(x)
	case json.Number:
		i, err := strconv.Atoi(string(x))
		if err != nil {
			return 0, fault("INVALID_ARGUMENT", "invalid chapter pagination")
		}
		n = i
	default:
		return 0, fault("INVALID_ARGUMENT", "invalid chapter pagination")
	}
	if n < min || n > max {
		return 0, fault("INVALID_ARGUMENT", "invalid chapter pagination")
	}
	return n, nil
}

// A copy of the HTTP client prevents catalog redirects from reaching learning
// or logging endpoints without changing the shared client's redirect policy.
func (c *Client) chapterGET(ctx context.Context, endpoint string) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		b, err := c.chapterGETAttempt(ctx, endpoint)
		f, expired := err.(*Fault)
		if !expired || f.Code != "AUTH_REQUIRED" || attempt == 1 {
			return b, err
		}
		if err := c.login(ctx); err != nil {
			return nil, err
		}
	}
	return nil, fault("AUTH_REQUIRED", "chapter session rejected")
}

func (c *Client) chapterGETAttempt(ctx context.Context, endpoint string) ([]byte, error) {
	h := *c.http
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fault("INVALID_URL", "invalid chapter endpoint")
	}
	req.Header.Set("User-Agent", ua)
	resp, err := h.Do(req)
	if err != nil {
		return nil, fault("UPSTREAM_UNAVAILABLE", "chapter transport failed")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil {
		return nil, fault("UPSTREAM_UNAVAILABLE", "chapter read failed")
	}
	if len(b) > 4*1024*1024 {
		return nil, fault("RESPONSE_TOO_LARGE", "chapter response exceeds limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fault("UPSTREAM_HTTP", "chapter response rejected; redirects disabled")
	}
	if loginRequired(b, resp) {
		return nil, fault("AUTH_REQUIRED", "chapter session rejected")
	}
	if strings.Contains(string(b), "没有此页面访问权限") || strings.Contains(string(b), "无权限访问") {
		return nil, fault("COURSE_ACCESS", "chapter page access denied")
	}
	if err := c.save(resp); err != nil {
		return nil, fault("STATE_WRITE_FAILED", "cannot persist session")
	}
	return b, nil
}

type chapterSummaryItem struct {
	Number       string `json:"number"`
	Title        string `json:"title"`
	State        string `json:"state"`
	PendingTasks *int   `json:"pending_tasks"`
}

var chapterAggregatePattern = regexp.MustCompile(`已完成任务点\s*[:：]\s*([0-9]+)\s*/\s*([0-9]+)`)

func chapterShort(s string) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return string(r)
}

func parseChapterSummary(b []byte, limit, offset int) (map[string]any, error) {
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return nil, fault("UPSTREAM_SCHEMA", "invalid chapter catalog")
	}
	var done, total *int
	if m := chapterAggregatePattern.FindStringSubmatch(text(doc)); m != nil {
		d, e1 := strconv.Atoi(m[1])
		t, e2 := strconv.Atoi(m[2])
		if e1 != nil || e2 != nil || d > t {
			return nil, fault("UPSTREAM_SCHEMA", "invalid task aggregate")
		}
		done, total = &d, &t
	}
	items := []chapterSummaryItem{}
	walk(doc, func(n *html.Node) {
		if !hasClass(n, "chapter_item") {
			return
		}
		// Each section is bounded by its own li; never take a following section's status.
		container := n
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Data == "li" {
				container = p
				break
			}
			if hasClass(p, "chapter_item") {
				break
			}
		}
		num := first(n, func(v *html.Node) bool { return hasClass(v, "catalog_sbar") })
		if num == nil {
			return
		}
		title := attr(n, "title")
		if title == "" {
			t := first(n, func(v *html.Node) bool { return hasClass(v, "catalog_name") })
			if t != nil {
				titled := first(t, func(v *html.Node) bool { return attr(v, "title") != "" })
				if titled != nil {
					title = attr(titled, "title")
				} else {
					title = text(t)
				}
			}
		}
		item := chapterSummaryItem{Number: chapterShort(text(num)), Title: chapterShort(title), State: "unknown"}
		finished := first(container, func(v *html.Node) bool { return hasClass(v, "icon_yiwanc") }) != nil
		pending := first(container, func(v *html.Node) bool { return hasClass(v, "catalog_points_yi") })
		if pending != nil {
			if count, e := strconv.Atoi(strings.TrimSpace(text(pending))); e == nil && count >= 0 {
				item.PendingTasks = &count
				item.State = "pending"
			}
		}
		if finished && pending == nil {
			item.State = "completed"
		}
		if finished && pending != nil {
			item.State = "unknown"
		}
		items = append(items, item)
	})
	if total == nil && len(items) == 0 {
		return nil, fault("UPSTREAM_SCHEMA", "chapter catalog not recognized")
	}
	count := len(items)
	start := offset
	if start > count {
		start = count
	}
	end := start + limit
	if end > count {
		end = count
	}
	warnings := []string{"Chapter summaries only; individual task cards are not enumerated."}
	if total == nil {
		warnings = append(warnings, "Aggregate task counts are not explicitly reported.")
	}
	return map[string]any{"items": items[start:end], "total_in_feed": count, "returned": end - start, "offset": offset, "has_more": end < count, "completed_tasks": done, "total_tasks": total, "complete": false, "scope": "chapter_summary", "coverage": "chapter_summary_only; individual task cards not enumerated", "warnings": warnings}, nil
}
