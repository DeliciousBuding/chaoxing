package upstream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type activityPageLink struct {
	Href string `json:"href"`
	Text string `json:"text,omitempty"`
}

type activityPageFormField struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	ValuePresent bool   `json:"value_present"`
}

type activityPageForm struct {
	Action string                  `json:"action,omitempty"`
	Method string                  `json:"method,omitempty"`
	Fields []activityPageFormField `json:"fields"`
}

// ActivityPage follows only a URL already supplied by the verified class
// activity feed. It returns structure useful for protocol reconstruction while
// withholding hidden input values and raw page bytes.
func (c *Client) ActivityPage(ctx context.Context, args map[string]any) (any, error) {
	if err := c.lock(ctx); err != nil {
		return nil, err
	}
	defer c.queries.Release()
	return c.activityPageLocked(ctx, args)
}

// activityPageLocked is the account-scoped implementation. The caller must
// hold the client query gate; this keeps the shared cookie/cache state
// serialized with Call and the sign-in/answer paths.
func (c *Client) activityPageLocked(ctx context.Context, args map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	courseID, classID, activityID := str(args, "course_id"), str(args, "class_id"), str(args, "activity_id")
	if strings.TrimSpace(courseID) == "" || strings.TrimSpace(classID) == "" || strings.TrimSpace(activityID) == "" {
		return nil, fault("INVALID_ARGUMENT", "course, class and activity IDs are required")
	}
	courses, err := c.fetchCourses(ctx)
	if err != nil {
		return nil, err
	}
	course, ok := findCourseClass(courses, courseID, classID)
	if !ok {
		return nil, fault("NOT_FOUND", "course/class is not in the accessible list")
	}
	rows, err := c.fetchActivities(ctx, course)
	if err != nil {
		return nil, err
	}
	activity, ok := findActivity(rows, activityID)
	if !ok {
		return nil, fault("NOT_FOUND", "activity is not in the current class feed")
	}
	entry, err := url.Parse(strings.TrimSpace(activity.URL))
	if err != nil || !safeURL(entry) {
		return nil, fault("UNSUPPORTED", "activity has no safe page URL")
	}
	body, resp, err := c.authenticatedReadResponse(ctx, "GET", entry.String(), nil)
	if err != nil {
		return nil, err
	}
	pageText := string(body)
	if strings.Contains(pageText, "用户登录") {
		return nil, fault("AUTH_REQUIRED", "activity page needs additional authorization")
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fault("UPSTREAM_SCHEMA", "activity page is not valid HTML")
	}
	title := ""
	if node := first(doc, func(n *html.Node) bool { return n.Data == "title" }); node != nil {
		title = chapterShort(text(node))
	}
	textValue := chapterShort(text(doc))
	warnings := []string{}
	if len(textValue) > 16000 {
		textValue = textValue[:16000]
		warnings = append(warnings, "activity page text truncated at 16000 characters")
	}
	iframes := []string{}
	scripts := []string{}
	links := []activityPageLink{}
	forms := []activityPageForm{}
	walk(doc, func(n *html.Node) {
		switch n.Data {
		case "iframe":
			if src := strings.TrimSpace(attr(n, "src")); src != "" {
				iframes = append(iframes, src)
			}
		case "script":
			if src := strings.TrimSpace(attr(n, "src")); src != "" {
				scripts = append(scripts, src)
			}
		case "a":
			href := strings.TrimSpace(attr(n, "href"))
			if href != "" {
				links = append(links, activityPageLink{Href: href, Text: chapterShort(text(n))})
			}
		case "form":
			form := activityPageForm{Action: strings.TrimSpace(attr(n, "action")), Method: strings.ToLower(strings.TrimSpace(attr(n, "method")))}
			walk(n, func(v *html.Node) {
				if v.Data != "input" && v.Data != "textarea" && v.Data != "select" {
					return
				}
				name := strings.TrimSpace(attr(v, "name"))
				if name == "" {
					return
				}
				form.Fields = append(form.Fields, activityPageFormField{
					Name: name, Type: strings.ToLower(firstNonBlank(attr(v, "type"), v.Data)),
					ValuePresent: strings.TrimSpace(attr(v, "value")) != "",
				})
			})
			forms = append(forms, form)
		}
	})
	digest := sha256.Sum256(body)
	page := map[string]any{
		"activity": activity, "entry_url": entry.String(), "final_url": resp.Request.URL.String(),
		"title": title, "text": textValue, "iframes": iframes, "scripts": scripts,
		"links": links, "forms": forms, "body_sha256": hex.EncodeToString(digest[:]),
		"fetched_at": time.Now().UTC(),
	}
	return map[string]any{
		"activity_page": page, "warnings": warnings, "complete": true,
		"coverage": "single_activity_page_structure", "fetched_at": time.Now().UTC(),
	}, nil
}
