package upstream

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// courseListPage is the mooc2 "my courses" fragment. It is the same source the
// web client renders in the 课程 tab and exposes courses that the legacy
// backclazzdata feed sometimes omits (not yet opened, archived, folder-scoped).
const courseListPage = "https://mooc2-ans.chaoxing.com/mooc2-ans/visit/courselistdata"

// DiscoveredCourse is one row from the mooc2 course list.
type DiscoveredCourse struct {
	CourseID string `json:"course_id"`
	ClassID  string `json:"class_id"`
	Name     string `json:"name"`
	Teacher  string `json:"teacher,omitempty"`
	Status   string `json:"status,omitempty"`
}

// DiscoverCourses reads the mooc2 course list. It is a read-only discovery
// probe used to explain why an expected course is missing from
// list_courses/backclazzdata; it never enters a course or starts an activity.
func (c *Client) DiscoverCourses(ctx context.Context) ([]DiscoveredCourse, error) {
	form := url.Values{
		"courseType":     {"1"},
		"courseFolderId": {"0"},
		"query":          {""},
		"superstarClass": {"0"},
	}
	b, err := c.authenticatedRead(ctx, http.MethodPost, courseListPage, form)
	if err != nil {
		return nil, err
	}
	rows := parseCourseListPage(string(b))
	out := make([]DiscoveredCourse, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		key := row.CourseID + ":" + row.ClassID
		if row.CourseID == "" || row.ClassID == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, row)
	}
	return out, nil
}

func parseCourseListPage(page string) []DiscoveredCourse {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil
	}
	var out []DiscoveredCourse
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "div" && nodeHasClass(node, "course") {
			if row, ok := decodeCourseNode(node); ok {
				out = append(out, row)
			}
			// Course nodes do not nest; skip the subtree to avoid duplicate hits.
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return out
}

func decodeCourseNode(course *html.Node) (DiscoveredCourse, bool) {
	row := DiscoveredCourse{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch {
			case node.Data == "input":
				switch {
				case nodeHasClass(node, "clazzId") || nodeHasClass(node, "clazzid"):
					if row.ClassID == "" {
						row.ClassID = inputValue(node, "value")
					}
				case nodeHasClass(node, "courseId") || nodeHasClass(node, "courseid"):
					if row.CourseID == "" {
						row.CourseID = inputValue(node, "value")
					}
				}
			case node.Data == "span" && nodeHasClass(node, "course-name"):
				row.Name = inputValue(node, "title")
				if row.Name == "" {
					row.Name = textContent(node)
				}
			case node.Data == "p" && nodeHasClass(node, "color3"):
				row.Teacher = inputValue(node, "title")
				if row.Teacher == "" {
					row.Teacher = textContent(node)
				}
			case nodeHasClass(node, "not-open-tip"):
				row.Status = textContent(node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(course)
	row.Name = strings.TrimSpace(row.Name)
	row.Teacher = strings.TrimSpace(row.Teacher)
	row.Status = strings.TrimSpace(row.Status)
	return row, row.CourseID != "" && row.ClassID != ""
}

func nodeHasClass(node *html.Node, class string) bool {
	for _, a := range node.Attr {
		if a.Key != "class" {
			continue
		}
		for _, field := range strings.Fields(a.Val) {
			if strings.EqualFold(field, class) {
				return true
			}
		}
	}
	return false
}

func inputValue(node *html.Node, key string) string {
	for _, a := range node.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func textContent(node *html.Node) string {
	if node.Type == html.TextNode {
		return node.Data
	}
	var b strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		b.WriteString(textContent(child))
	}
	return b.String()
}
