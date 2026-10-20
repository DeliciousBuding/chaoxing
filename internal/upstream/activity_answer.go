package upstream

import (
	"context"
	"strings"
	"time"
)

const (
	taskActivityListAPI = "https://mobilelearn.chaoxing.com/ppt/activeAPI/taskactivelist"
	studentAnswerAPI    = "https://mobilelearn.chaoxing.com/pptAnswer/stuAnswer"
)

func (c *Client) AnswerActivity(ctx context.Context, courseID, classID, activityID string) (SignResult, error) {
	if err := c.lock(ctx); err != nil {
		return SignResult{}, err
	}
	defer c.queries.Release()
	return c.answerActivity(ctx, courseID, classID, activityID)
}

func (c *Client) answerActivity(ctx context.Context, courseID, classID, activityID string) (SignResult, error) {
	if strings.TrimSpace(courseID) == "" || strings.TrimSpace(classID) == "" || strings.TrimSpace(activityID) == "" {
		return SignResult{}, fault("INVALID_ARGUMENT", "course, class and activity IDs are required")
	}
	courses, err := c.fetchCourses(ctx)
	if err != nil {
		return SignResult{}, err
	}
	course, ok := findCourseClass(courses, courseID, classID)
	if !ok {
		return SignResult{}, fault("NOT_FOUND", "course/class is not in the accessible list")
	}
	rows, err := c.fetchActivities(ctx, course)
	if err != nil {
		return SignResult{}, err
	}
	activity, ok := findActivity(rows, activityID)
	if !ok || activity.Kind != "answer" {
		return SignResult{}, fault("NOT_FOUND", "activity is not an answer activity in the current class feed")
	}
	if activity.State != "active" || !activity.Answerable {
		if activity.UserState == "signed" {
			return SignResult{ActivityID: activity.ID, CourseID: activity.CourseID, ClassID: activity.ClassID, Method: "none", State: activity.UserState, Verified: true, FetchedAt: time.Now().UTC()}, nil
		}
		return SignResult{}, fault("UNSUPPORTED", "answer activity is not currently claimable")
	}
	identity, err := c.fetchSignIdentity(ctx)
	if err != nil {
		return SignResult{}, err
	}
	q := orderedQuery(
		"answerId", activity.ID, "classId", activity.ClassID, "role", "",
		"courseId", activity.CourseID, "general", "", "appType", "15",
		"stuMiddlePage", "1", "stuName", identity.Name,
	)
	b, err := c.get(ctx, studentAnswerAPI+"?"+q)
	if err != nil {
		return SignResult{}, err
	}
	state := answerResponseState(string(b))
	if state == "" {
		return SignResult{}, fault("ANSWER_REJECTED", "rapid-answer participation was not accepted")
	}
	return SignResult{
		ActivityID: activity.ID, CourseID: activity.CourseID, ClassID: activity.ClassID,
		Method: "answer", State: state, Verified: true, FetchedAt: time.Now().UTC(),
	}, nil
}

func answerResponseState(body string) string {
	value := strings.TrimSpace(body)
	switch {
	case value == "success", value == "true", strings.Contains(value, `"status":true`), strings.Contains(value, "您已抢答"):
		return "answered"
	case strings.Contains(value, "已经结束"), strings.Contains(value, "已结束"):
		return ""
	default:
		return ""
	}
}
