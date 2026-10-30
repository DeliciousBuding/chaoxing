package upstream

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// activityAPI is the class activity feed used by the Chaoxing mobile pages.
const activityAPI = "https://mobilelearn.chaoxing.com/v2/apis/active/student/activelist"

// A short success-only cache coalesces watcher, interactive and sign-preflight
// reads. Cached list state is still only a hint, never attendance proof.
const classActivityCacheTTL = 30 * time.Second

type cachedClassActivities struct {
	rows []Activity
	at   time.Time
}

// Activity is the normalized, read-only projection of one class activity.
type Activity struct {
	ID            string `json:"id"`
	CourseID      string `json:"course_id"`
	ClassID       string `json:"class_id"`
	Course        string `json:"course"`
	Title         string `json:"title"`
	Kind          string `json:"kind"`
	State         string `json:"state"`
	UserState     string `json:"user_state"`
	StateSource   string `json:"state_source,omitempty"`
	RawType       int    `json:"raw_type"`
	RawOtherID    int    `json:"raw_other_id"`
	RawStatus     int    `json:"raw_status"`
	RawUserStatus int    `json:"raw_user_status"`
	SignType      string `json:"sign_type"`
	Signable      bool   `json:"signable"`
	Answerable    bool   `json:"answerable"`
	// URL is safe to return to clients; RawURL stays internal because upstream
	// activity links may carry short-lived signature/query material.
	URL           string     `json:"url,omitempty"`
	RawURL        string     `json:"-"`
	StartsAt      *time.Time `json:"starts_at,omitempty"`
	EndsAt        *time.Time `json:"ends_at,omitempty"`
	StartsAtTitle string     `json:"starts_at_title,omitempty"`
	Ext           string     `json:"-"`
	Source        string     `json:"source,omitempty"`
	GroupName     string     `json:"group_name,omitempty"`
}

// flexInt accepts upstream numbers that are sometimes encoded as strings.
type flexInt struct {
	value int64
	ok    bool
}

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		f.value, f.ok = 0, false
		return nil
	}
	f.value, f.ok = v, true
	return nil
}

type rawActivity struct {
	ID         json.Number `json:"id"`
	Title      string      `json:"nameOne"`
	NameFour   string      `json:"nameFour"`
	Type       flexInt     `json:"type"`
	ActiveType flexInt     `json:"activeType"`
	OtherID    flexInt     `json:"otherId"`
	URL        string      `json:"url"`
	Status     flexInt     `json:"status"`
	UserStatus flexInt     `json:"userStatus"`
	StartTime  flexInt     `json:"startTime"`
	EndTime    flexInt     `json:"endTime"`
}

func (c *Client) activities(ctx context.Context, args map[string]any) (any, error) {
	limit := number(args, "limit", 50)
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	courses, err := c.fetchCourses(ctx)
	if err != nil {
		return nil, err
	}
	selected, err := academicSelection(courses, args)
	if err != nil {
		return nil, err
	}
	items := []Activity{}
	classes := []any{}
	warnings := []string{}
	failed := 0
	var firstErr error
	for _, co := range selected {
		cached := c.classActivities[co.ID+":"+co.ClassID]
		rows, err := c.fetchActivities(ctx, co)
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			code := academicCode(err)
			classes = append(classes, map[string]any{"course_id": co.ID, "class_id": co.ClassID, "complete": false, "code": code})
			warnings = append(warnings, "class "+co.ClassID+": "+code)
			continue
		}
		items = append(items, rows...)
		classes = append(classes, map[string]any{"course_id": co.ID, "class_id": co.ClassID, "complete": true, "cache_hit": !cached.at.IsZero() && time.Since(cached.at) < classActivityCacheTTL, "fetched_at": c.classActivities[co.ID+":"+co.ClassID].at.UTC()})
	}
	groupAvailable := !c.config.GroupActivities
	var groupErr error
	if c.config.GroupActivities {
		groups, err := c.fetchIMGroups(ctx)
		if err != nil {
			groupErr = err
		} else {
			groupRows, groupComplete, groupWarnings, err := c.groupActivities(ctx, groups)
			if err != nil {
				groupErr = err
			} else {
				groupAvailable = groupComplete
				warnings = append(warnings, groupWarnings...)
				known := map[string]bool{}
				for _, item := range items {
					known[item.CourseID+":"+item.ClassID+":"+item.ID] = true
				}
				for _, item := range groupRows {
					if !known[item.CourseID+":"+item.ClassID+":"+item.ID] {
						items = append(items, item)
					}
				}
			}
		}
	}
	if groupErr != nil {
		code := academicCode(groupErr)
		warnings = append(warnings, "group activity feed unavailable: "+code)
	}
	if failed == len(selected) && firstErr != nil {
		return nil, firstErr
	}
	items = dedupeActivities(items)
	total := len(items)
	hasMore := total > limit
	if hasMore {
		items = items[:limit]
	}
	return map[string]any{
		"items":            items,
		"returned":         len(items),
		"total_in_feed":    total,
		"limit":            limit,
		"has_more":         hasMore,
		"complete":         failed == 0 && groupAvailable,
		"coverage":         "class_activity_list",
		"coverage_details": map[string]any{"classes": classes, "sources_checked": len(selected), "sources_failed": failed},
		"warnings":         warnings,
		"fetched_at":       time.Now().UTC(),
	}, nil
}

func (c *Client) fetchActivities(ctx context.Context, co Course) ([]Activity, error) {
	key := co.ID + ":" + co.ClassID
	if cached, ok := c.classActivities[key]; ok && time.Since(cached.at) < classActivityCacheTTL {
		// Never hide a known cooldown behind cached data.
		u, _ := url.Parse(activityAPI)
		if err := c.endpointDeferred(u); err != nil {
			return nil, err
		}
		return append([]Activity{}, cached.rows...), nil
	}
	q := url.Values{
		"fid":                  {"0"},
		"showNotStartedActive": {"0"},
		"courseId":             {co.ID},
		"classId":              {co.ClassID},
		"_":                    {strconv.FormatInt(time.Now().UnixMilli(), 10)},
	}
	b, err := c.get(ctx, activityAPI+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var payload struct {
		Result int `json:"result"`
		Data   struct {
			ActiveList []rawActivity   `json:"activeList"`
			Ext        json.RawMessage `json:"ext"`
		} `json:"data"`
	}
	if err = json.Unmarshal(b, &payload); err != nil {
		return nil, fault("UPSTREAM_SCHEMA", "class activity list not recognized")
	}
	if payload.Result != 1 {
		return nil, fault("UPSTREAM_SCHEMA", "class activity list not recognized")
	}
	rows := make([]Activity, 0, len(payload.Data.ActiveList))
	for _, raw := range payload.Data.ActiveList {
		id := raw.ID.String()
		if id == "" || id == "0" {
			return nil, fault("UPSTREAM_SCHEMA", "activity row without a stable id")
		}
		rows = append(rows, normalizeActivity(raw, co, string(payload.Data.Ext)))
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].StartsAt == nil || rows[j].StartsAt == nil {
			return rows[i].StartsAt != nil
		}
		return rows[i].StartsAt.After(*rows[j].StartsAt)
	})
	if c.classActivities == nil {
		c.classActivities = make(map[string]cachedClassActivities)
	}
	for key, cached := range c.classActivities {
		if time.Since(cached.at) >= classActivityCacheTTL {
			delete(c.classActivities, key)
		}
	}
	c.classActivities[key] = cachedClassActivities{rows: append([]Activity{}, rows...), at: time.Now()}
	return rows, nil
}

func normalizeActivity(raw rawActivity, co Course, ext string) Activity {
	kind := activityKind(raw.Type.value)
	state := "unknown"
	switch raw.Status.value {
	case 1:
		state = "active"
	case 2:
		state = "finished"
	}
	userState := "unknown"
	switch raw.UserStatus.value {
	case 0:
		userState = "unsigned"
	case 1:
		userState = "signed"
	case 2:
		userState = "teacher_signed"
	case 4:
		userState = "leave"
	case 5:
		userState = "absence"
	case 9:
		userState = "late"
	case 10:
		userState = "leave_early"
	case 11:
		userState = "expired"
	}
	a := Activity{
		ID:            raw.ID.String(),
		CourseID:      co.ID,
		ClassID:       co.ClassID,
		Course:        co.Name,
		Title:         firstNonBlank(raw.Title, raw.NameFour),
		Kind:          kind,
		State:         state,
		UserState:     userState,
		RawType:       int(raw.Type.value),
		RawOtherID:    int(raw.OtherID.value),
		RawStatus:     int(raw.Status.value),
		RawUserStatus: int(raw.UserStatus.value),
		Ext:           strings.TrimSpace(ext),
		Source:        "class",
		URL:           publicActivityURL(raw.URL),
		RawURL:        strings.TrimSpace(raw.URL),
	}
	a.SignType = signType(raw.OtherID.value)
	// The list's userStatus is not an attendance receipt. In particular, an
	// active, not-yet-signed card can contain userStatus=1. Preserve the hint,
	// but let preSign and the independent attendance read decide completion.
	if kind == "signin" {
		a.UserState = "unknown"
		a.StateSource = "class_feed_unverified"
	}
	a.Signable = kind == "signin" && state == "active"
	a.Answerable = kind == "answer" && state == "active" && userState == "unsigned"
	a.StartsAt = msTime(raw.StartTime.value)
	a.EndsAt = msTime(raw.EndTime.value)
	return a
}

func publicActivityURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

func activityKind(rawType int64) string {
	switch rawType {
	case 2, 74:
		return "signin"
	case 4:
		return "answer"
	case 42:
		return "practice"
	case 19:
		return "homework"
	default:
		return "activity"
	}
}

func signType(otherID int64) string {
	switch otherID {
	case 0:
		return "normal_or_photo"
	case 2:
		return "qrcode"
	case 3:
		return "gesture"
	case 4:
		return "location"
	case 5:
		return "password"
	default:
		return "unknown"
	}
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func msTime(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

func dedupeActivities(rows []Activity) []Activity {
	seen := map[string]bool{}
	out := make([]Activity, 0, len(rows))
	for _, row := range rows {
		key := row.CourseID + ":" + row.ClassID + ":" + row.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartsAt == nil || out[j].StartsAt == nil {
			return out[i].StartsAt != nil
		}
		return out[i].StartsAt.After(*out[j].StartsAt)
	})
	return out
}
