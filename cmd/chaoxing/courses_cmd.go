package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

// coursesCommand explains why an expected course is or is not watchable.
// "discover" reads the mooc2 course-list page (the 课程 tab source) and marks
// which rows the legacy backclazzdata feed also returns. It never enters a
// course or starts an activity.
func coursesCommand(c config, args []string) error {
	if len(args) == 0 || args[0] != "discover" {
		return errors.New("courses subcommand required: discover")
	}
	fs := flag.NewFlagSet("courses discover", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Int("timeout-seconds", 45, "upstream timeout")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	known := map[string]bool{}
	if value, callErr := client.Call(ctx, "list_courses", map[string]any{}); callErr == nil {
		var listed struct {
			Result []struct {
				CourseID string `json:"course_id"`
				ClassID  string `json:"class_id"`
			} `json:"result"`
		}
		if raw, marshalErr := json.Marshal(value); marshalErr == nil {
			_ = json.Unmarshal(raw, &listed)
		}
		for _, row := range listed.Result {
			known[row.CourseID+":"+row.ClassID] = true
		}
	}
	rows, err := client.DiscoverCourses(ctx)
	if err != nil {
		return err
	}
	type item struct {
		upstream.DiscoveredCourse
		InBackclazzdata bool `json:"in_backclazzdata"`
		Watched         bool `json:"watched"`
	}
	watched := map[string]bool{}
	if c.Watch != nil {
		for _, class := range c.Watch.Classes {
			watched[class.CourseID+":"+class.ClassID] = true
		}
	}
	out := make([]item, 0, len(rows))
	for _, row := range rows {
		key := row.CourseID + ":" + row.ClassID
		out = append(out, item{DiscoveredCourse: row, InBackclazzdata: known[key], Watched: watched[key]})
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"ok": true, "source": "mooc2-ans/visit/courselistdata", "count": len(out), "items": out,
	})
}
