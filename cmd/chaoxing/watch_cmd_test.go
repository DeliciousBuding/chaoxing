package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTestConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestWatchClassesAddPreservesOtherFields(t *testing.T) {
	path := writeTestConfig(t, `{"state_dir":"/s","local_token":"synthetic","watch":{"auto_sign":false,"classes":[]}}`)
	if err := watchCommand(path, []string{
		"classes", "add", "--course", "c1", "--class", "k1", "--days", "Tue,Thu", "--start", "08:00", "--end", "09:50",
	}); err != nil {
		t.Fatal(err)
	}
	if err := watchCommand(path, []string{
		"classes", "add", "--course", "c1", "--class", "k1", "--days", "Tue", "--start", "08:00", "--end", "09:50",
	}); err != nil {
		t.Fatal(err)
	}
	root := readTestConfig(t, path)
	if root["state_dir"] != "/s" || root["local_token"] != "synthetic" {
		t.Fatal("unrelated configuration fields were lost")
	}
	watch := root["watch"].(map[string]any)
	if watch["auto_sign"] != false {
		t.Fatal("watch flags changed")
	}
	classes := watch["classes"].([]any)
	if len(classes) != 1 {
		t.Fatalf("classes=%v", classes)
	}
	schedules := classes[0].(map[string]any)["schedule"].([]any)
	if len(schedules) != 2 {
		t.Fatalf("schedules=%v", schedules)
	}
}

func TestWatchClassesRemoveScheduleAndClass(t *testing.T) {
	path := writeTestConfig(t, `{"watch":{"classes":[{"course_id":"c1","class_id":"k1","schedule":[
		{"days":["Tue","Thu"],"start":"08:00","end":"09:50"},
		{"days":["Fri"],"start":"16:10","end":"18:00"}]}]}}`)
	if err := watchCommand(path, []string{
		"classes", "remove", "--course", "c1", "--class", "k1", "--days", "Thu,Tue", "--start", "08:00", "--end", "09:50",
	}); err != nil {
		t.Fatal(err)
	}
	classes := readTestConfig(t, path)["watch"].(map[string]any)["classes"].([]any)
	schedules := classes[0].(map[string]any)["schedule"].([]any)
	if len(schedules) != 1 {
		t.Fatalf("schedules=%v", schedules)
	}
	if err := watchCommand(path, []string{"classes", "remove", "--course", "c1", "--class", "k1"}); err != nil {
		t.Fatal(err)
	}
	classes = readTestConfig(t, path)["watch"].(map[string]any)["classes"].([]any)
	if len(classes) != 0 {
		t.Fatalf("classes=%v", classes)
	}
}

func TestWatchClassesRejectsBadInput(t *testing.T) {
	path := writeTestConfig(t, `{"watch":{"classes":[]}}`)
	for _, args := range [][]string{
		{"classes", "add", "--course", "c", "--class", "k", "--days", "Funday", "--start", "08:00", "--end", "09:00"},
		{"classes", "add", "--course", "c", "--class", "k", "--days", "Tue", "--start", "9:00", "--end", "10:00"},
		{"classes", "add", "--course", "c", "--class", "k", "--days", "Tue", "--start", "10:00", "--end", "10:00"},
		{"classes", "add", "--course", "c", "--class", "k", "--days", "Tue", "--start", "10:00", "--end", "11:00", "--weeks", "7-6"},
		{"classes", "add", "--course", "c", "--class", "k", "--days", "Tue", "--start", "10:00", "--end", "11:00", "--key", "bad key"},
		{"classes", "remove", "--course", "c", "--class", "missing"},
	} {
		if err := watchCommand(path, args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestWatchClassesAddIndependentGuidanceSchedule(t *testing.T) {
	path := writeTestConfig(t, `{"watch":{"classes":[{"course_id":"c1","class_id":"k1","schedule":[]}]}}`)
	if err := watchCommand(path, []string{
		"classes", "add", "--course", "c1", "--class", "k1", "--days", "Tue",
		"--start", "08:00", "--end", "09:40", "--weeks", "7,13",
		"--key", "arch-tue-guidance", "--room", "中303",
	}); err != nil {
		t.Fatal(err)
	}
	schedule := readTestConfig(t, path)["watch"].(map[string]any)["classes"].([]any)[0].(map[string]any)["schedule"].([]any)[0].(map[string]any)
	if schedule["weeks"] != "7,13" || schedule["key"] != "arch-tue-guidance" || schedule["room"] != "中303" {
		t.Fatalf("schedule=%v", schedule)
	}
}

func TestWatchClassesAllowCrossMidnight(t *testing.T) {
	path := writeTestConfig(t, `{"watch":{"classes":[]}}`)
	if err := watchCommand(path, []string{
		"classes", "add", "--course", "c", "--class", "k", "--days", "Mon",
		"--start", "23:50", "--end", "00:30",
	}); err != nil {
		t.Fatal(err)
	}
}
