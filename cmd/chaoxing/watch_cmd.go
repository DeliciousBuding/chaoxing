package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/persist"
	"github.com/DeliciousBuding/chaoxing/internal/watch"
)

const maxWatchClasses = 8

// watchCommand edits the static class list used by the watcher. It rewrites
// only watch.classes and preserves every other configuration field verbatim.
// The daemon reads configuration at startup, so callers must restart it.
func watchCommand(configPath string, args []string) error {
	if len(args) < 2 {
		return errors.New("watch subcommand required: classes list|add|remove, windows")
	}
	root, err := loadConfigMap(configPath)
	if err != nil {
		return err
	}
	if args[0] == "windows" {
		return watchWindows(configPath, args[1:])
	}
	if args[0] != "classes" {
		return errors.New("unknown watch subcommand")
	}
	switch args[1] {
	case "list":
		return emitWatchClasses(root)
	case "add":
		fs := flag.NewFlagSet("watch classes add", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		course := fs.String("course", "", "course id")
		class := fs.String("class", "", "class id")
		days := fs.String("days", "", "comma-separated days, e.g. Tue,Thu")
		start := fs.String("start", "", "start time HH:MM")
		end := fs.String("end", "", "end time HH:MM")
		weeks := fs.String("weeks", "", "optional teaching weeks, e.g. 1-6 or 7,13")
		key := fs.String("key", "", "optional stable schedule key for location bindings")
		room := fs.String("room", "", "optional room label, e.g. 中303")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		dayList, err := parseWatchDays(*days)
		if err != nil {
			return err
		}
		if err := validateClock(*start); err != nil {
			return err
		}
		if err := validateClock(*end); err != nil {
			return err
		}
		if *start == *end {
			return errors.New("start and end must differ")
		}
		normalizedWeeks, err := watch.NormalizeWeeks(*weeks)
		if err != nil {
			return err
		}
		if err := addWatchClass(root, *course, *class, dayList, *start, *end, normalizedWeeks, *key, *room); err != nil {
			return err
		}
	case "remove":
		fs := flag.NewFlagSet("watch classes remove", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		course := fs.String("course", "", "course id")
		class := fs.String("class", "", "class id")
		days := fs.String("days", "", "optional: remove only this schedule")
		start := fs.String("start", "", "optional: remove only this schedule")
		end := fs.String("end", "", "optional: remove only this schedule")
		weeks := fs.String("weeks", "", "optional: remove only this exact week range")
		key := fs.String("key", "", "optional: remove only this schedule key")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		var dayList []string
		if strings.TrimSpace(*days) != "" {
			if dayList, err = parseWatchDays(*days); err != nil {
				return err
			}
			if err := validateClock(*start); err != nil {
				return err
			}
			if err := validateClock(*end); err != nil {
				return err
			}
		}
		if err := removeWatchClass(root, *course, *class, dayList, *start, *end, *weeks, *key); err != nil {
			return err
		}
	default:
		return errors.New("unknown watch classes subcommand")
	}
	if err := saveConfigMap(configPath, root); err != nil {
		return err
	}
	if err := emitWatchClasses(root); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "restart the daemon to apply the new class list")
	return nil
}

// watchWindows prints the same active intervals the polling loop would use,
// including lead time, so an operator can compare configuration with the real
// timetable before relying on automatic sign-in.
func watchWindows(configPath string, args []string) error {
	c, err := load()
	if err != nil {
		return err
	}
	if c.Watch == nil {
		return errors.New("watch is not configured")
	}
	fs := flag.NewFlagSet("watch windows", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	days := fs.Int("days", 7, "number of days to project")
	fromRaw := fs.String("from", "", "optional RFC3339 start time (default now)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *days < 1 || *days > 30 {
		return errors.New("--days must be between 1 and 30")
	}
	from := time.Now()
	if strings.TrimSpace(*fromRaw) != "" {
		parsed, err := time.Parse(time.RFC3339, *fromRaw)
		if err != nil {
			return errors.New("--from must be RFC3339")
		}
		from = parsed
	}
	before, after, err := watch.Padding(
		time.Duration(c.Watch.PollBeforeMinutes)*time.Minute,
		time.Duration(c.Watch.PollAfterMinutes)*time.Minute,
		time.Duration(c.Watch.LeadSeconds)*time.Second,
	)
	if err != nil {
		return err
	}
	termStart, err := parseWatchTermStart(c.Watch.TermStart)
	if err != nil {
		return err
	}
	windows := watch.Windows(c.Watch.Classes, before, after, termStart, from, from.Add(time.Duration(*days)*24*time.Hour))
	type row struct {
		CourseID    string `json:"course_id"`
		ClassID     string `json:"class_id"`
		Start       string `json:"start"`
		End         string `json:"end"`
		ScheduleKey string `json:"schedule_key,omitempty"`
		Weeks       string `json:"weeks,omitempty"`
		Room        string `json:"room,omitempty"`
	}
	out := make([]row, 0, len(windows))
	for _, w := range windows {
		out = append(out, row{
			CourseID: w.CourseID, ClassID: w.ClassID,
			Start:       w.Start.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04 MST"),
			End:         w.End.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04 MST"),
			ScheduleKey: w.ScheduleKey,
			Weeks:       w.Weeks,
			Room:        w.Room,
		})
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"ok": true, "timezone": "Asia/Shanghai",
		"poll_before_seconds": int(before.Seconds()), "poll_after_seconds": int(after.Seconds()),
		"term_start": c.Watch.TermStart, "count": len(out), "windows": out,
	})
}

func loadConfigMap(path string) (map[string]any, error) {
	if err := checkPrivateFile(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("configuration unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, errors.New("configuration invalid")
	}
	return root, nil
}

func saveConfigMap(path string, root map[string]any) error {
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	// The marshal above must stay parseable before it replaces the live file.
	var check map[string]any
	if err := json.Unmarshal(b, &check); err != nil {
		return errors.New("configuration rewrite produced invalid JSON")
	}
	if old, err := os.ReadFile(path); err == nil {
		backup := path + ".bak-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return errors.New("configuration backup failed")
		}
	}
	return persist.Write(path, b, 0o600)
}

func watchClasses(root map[string]any) []any {
	watch, _ := root["watch"].(map[string]any)
	if watch == nil {
		return nil
	}
	classes, _ := watch["classes"].([]any)
	return classes
}

func addWatchClass(root map[string]any, course, class string, days []string, start, end, weeks, key, room string) error {
	course, class = strings.TrimSpace(course), strings.TrimSpace(class)
	if course == "" || class == "" {
		return errors.New("--course and --class are required")
	}
	key, room = strings.TrimSpace(key), strings.TrimSpace(room)
	if key != "" {
		if err := watch.ValidateScheduleKey(key); err != nil {
			return err
		}
	}
	if len([]rune(room)) > 64 {
		return errors.New("--room is too long")
	}
	watch, ok := root["watch"].(map[string]any)
	if !ok {
		watch = map[string]any{}
		root["watch"] = watch
	}
	classes, _ := watch["classes"].([]any)
	schedule := map[string]any{"days": stringSlice(days), "start": start, "end": end}
	if weeks != "" {
		schedule["weeks"] = weeks
	}
	if key != "" {
		schedule["key"] = key
	}
	if room != "" {
		schedule["room"] = room
	}
	for _, raw := range classes {
		item, ok := raw.(map[string]any)
		if !ok || stringField(item, "course_id") != course || stringField(item, "class_id") != class {
			continue
		}
		list, _ := item["schedule"].([]any)
		for _, existing := range list {
			if scheduleEqual(existing, schedule) {
				return nil
			}
		}
		item["schedule"] = append(list, schedule)
		return nil
	}
	if len(classes) >= maxWatchClasses {
		return fmt.Errorf("watch supports at most %d classes", maxWatchClasses)
	}
	watch["classes"] = append(classes, map[string]any{
		"course_id": course, "class_id": class, "schedule": []any{schedule},
	})
	return nil
}

func removeWatchClass(root map[string]any, course, class string, days []string, start, end, weeks, key string) error {
	course, class = strings.TrimSpace(course), strings.TrimSpace(class)
	if course == "" || class == "" {
		return errors.New("--course and --class are required")
	}
	watch, _ := root["watch"].(map[string]any)
	classes, _ := watch["classes"].([]any)
	kept := make([]any, 0, len(classes))
	found := false
	for _, raw := range classes {
		item, ok := raw.(map[string]any)
		if !ok || stringField(item, "course_id") != course || stringField(item, "class_id") != class {
			kept = append(kept, raw)
			continue
		}
		found = true
		if len(days) == 0 {
			continue
		}
		list, _ := item["schedule"].([]any)
		remaining := make([]any, 0, len(list))
		for _, existing := range list {
			schedule, _ := existing.(map[string]any)
			if schedule != nil && stringField(schedule, "start") == start && stringField(schedule, "end") == end &&
				daysEqual(schedule["days"], days) &&
				(weeks == "" || stringField(schedule, "weeks") == weeks) &&
				(key == "" || stringField(schedule, "key") == key) {
				continue
			}
			remaining = append(remaining, existing)
		}
		if len(remaining) == 0 {
			continue
		}
		item["schedule"] = remaining
		kept = append(kept, item)
	}
	if !found {
		return errors.New("watch class not found")
	}
	watch["classes"] = kept
	return nil
}

func emitWatchClasses(root map[string]any) error {
	classes := watchClasses(root)
	if classes == nil {
		classes = []any{}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "classes": classes, "count": len(classes)})
}

func parseWatchDays(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	if len(fields) == 0 {
		return nil, errors.New("--days is required")
	}
	valid := map[string]string{"mon": "Mon", "tue": "Tue", "wed": "Wed", "thu": "Thu", "fri": "Fri", "sat": "Sat", "sun": "Sun"}
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		day, ok := valid[strings.ToLower(strings.TrimSpace(field))]
		if !ok {
			return nil, fmt.Errorf("invalid day %q", field)
		}
		if seen[day] {
			continue
		}
		seen[day] = true
		out = append(out, day)
	}
	sort.Strings(out)
	return out, nil
}

func validateClock(value string) error {
	if len(value) != 5 || value[2] != ':' {
		return errors.New("time must use HH:MM")
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return errors.New("time must use HH:MM")
	}
	if parsed.Format("15:04") != value {
		return errors.New("time must use HH:MM")
	}
	return nil
}

func stringSlice(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func stringField(item map[string]any, key string) string {
	value, _ := item[key].(string)
	return value
}

func scheduleEqual(existing any, schedule map[string]any) bool {
	item, ok := existing.(map[string]any)
	if !ok {
		return false
	}
	return stringField(item, "start") == stringField(schedule, "start") &&
		stringField(item, "end") == stringField(schedule, "end") &&
		stringField(item, "weeks") == stringField(schedule, "weeks") &&
		stringField(item, "key") == stringField(schedule, "key") &&
		stringField(item, "room") == stringField(schedule, "room") &&
		daysEqual(item["days"], toStrings(schedule["days"]))
}

func daysEqual(raw any, days []string) bool {
	list, ok := raw.([]any)
	if !ok {
		return false
	}
	got := make([]string, 0, len(list))
	for _, value := range list {
		text, _ := value.(string)
		got = append(got, text)
	}
	sort.Strings(got)
	want := append([]string(nil), days...)
	sort.Strings(want)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func toStrings(raw any) []string {
	list, _ := raw.([]any)
	out := make([]string, 0, len(list))
	for _, value := range list {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
