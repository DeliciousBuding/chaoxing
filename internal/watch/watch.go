// Package watch polls the class activity feed and raises operator alerts for
// new sign-in activities.
package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

type Class struct {
	CourseID string `json:"course_id"`
	ClassID  string `json:"class_id"`
}

type Config struct {
	Interval    time.Duration
	RepeatEvery time.Duration
	MaxRepeats  int
	Window      string
	Classes     []Class
	StatePath   string
}

type Source interface {
	Call(ctx context.Context, name string, args map[string]any) (any, error)
}

type entry struct {
	LastSent time.Time `json:"last_sent"`
	Count    int       `json:"count"`
}

type stateFile struct {
	Version int              `json:"version"`
	Entries map[string]entry `json:"entries"`
}

type Runner struct {
	cfg    Config
	source Source
	sender notify.Sender
	state  map[string]entry
	now    func() time.Time
}

var shanghai = time.FixedZone("CST", 8*60*60)

func New(cfg Config, source Source, sender notify.Sender) (*Runner, error) {
	if source == nil || sender == nil {
		return nil, errors.New("watch requires an activity source and a notifier")
	}
	if cfg.Interval < 30*time.Second {
		return nil, errors.New("watch interval must be at least 30 seconds")
	}
	if cfg.RepeatEvery < 30*time.Second {
		cfg.RepeatEvery = 60 * time.Second
	}
	if cfg.MaxRepeats < 1 {
		cfg.MaxRepeats = 5
	}
	if cfg.MaxRepeats > 20 {
		cfg.MaxRepeats = 20
	}
	if len(cfg.Classes) == 0 {
		return nil, errors.New("watch requires at least one class")
	}
	if len(cfg.Classes) > 8 {
		return nil, errors.New("watch supports at most eight classes")
	}
	for _, c := range cfg.Classes {
		if strings.TrimSpace(c.CourseID) == "" || strings.TrimSpace(c.ClassID) == "" {
			return nil, errors.New("watch classes require course_id and class_id")
		}
	}
	if _, err := parseWindow(cfg.Window); err != nil {
		return nil, err
	}
	r := &Runner{cfg: cfg, source: source, sender: sender, state: map[string]entry{}, now: time.Now}
	r.load()
	return r, nil
}

func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	log.Printf("activity watch started: %d classes, interval %s", len(r.cfg.Classes), r.cfg.Interval)
	for {
		select {
		case <-ctx.Done():
			log.Print("activity watch stopped")
			return
		case <-ticker.C:
			r.Tick(ctx)
		}
	}
}

// Tick polls every configured class once and emits due alerts.
func (r *Runner) Tick(ctx context.Context) {
	now := r.now()
	if !inWindow(now, r.cfg.Window) {
		return
	}
	active := map[string]upstream.Activity{}
	for _, class := range r.cfg.Classes {
		v, err := r.source.Call(ctx, "list_activities", map[string]any{
			"course_id": class.CourseID,
			"class_id":  class.ClassID,
			"limit":     float64(20),
		})
		if err != nil {
			log.Printf("activity watch: class %s: %s", class.ClassID, upstream.SafeFault(err).Code)
			continue
		}
		out, ok := v.(map[string]any)
		if !ok {
			continue
		}
		items, _ := out["items"].([]upstream.Activity)
		for _, item := range items {
			if item.Signable {
				active[key(item)] = item
			}
		}
	}
	changed := false
	for k, item := range active {
		e, seen := r.state[k]
		if seen && e.Count >= r.cfg.MaxRepeats {
			continue
		}
		if seen && now.Sub(e.LastSent) < r.cfg.RepeatEvery {
			continue
		}
		msg := notify.Message{Title: "🔔 学习通签到提醒", Body: formatBody(item)}
		if err := r.sender.Send(ctx, msg); err != nil {
			log.Printf("activity watch: notify failed: %v", err)
			continue
		}
		r.state[k] = entry{LastSent: now, Count: e.Count + 1}
		changed = true
		log.Printf("activity watch: alerted %s (attempt %d)", item.ID, e.Count+1)
	}
	// Drop alerts whose activity is no longer actionable and prune old state.
	for k, e := range r.state {
		if _, still := active[k]; !still && now.Sub(e.LastSent) > 6*time.Hour {
			delete(r.state, k)
			changed = true
		}
		if now.Sub(e.LastSent) > 24*time.Hour {
			delete(r.state, k)
			changed = true
		}
	}
	if changed {
		if err := r.save(); err != nil {
			log.Printf("activity watch: state write failed: %v", err)
		}
	}
}

func formatBody(a upstream.Activity) string {
	lines := []string{
		"课程：" + firstNonBlank(a.Course, a.CourseID),
		"活动：" + firstNonBlank(a.Title, a.ID),
	}
	if a.StartsAt != nil {
		lines = append(lines, "开始："+a.StartsAt.In(shanghai).Format("15:04"))
	}
	lines = append(lines, "状态：进行中，尚未签到", "请立即打开学习通。")
	return strings.Join(lines, "\n")
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func key(a upstream.Activity) string { return a.CourseID + ":" + a.ClassID + ":" + a.ID }

type window struct {
	enabled bool
	start   int
	end     int
}

func parseWindow(spec string) (window, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return window{}, nil
	}
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return window{}, fmt.Errorf("watch window must look like 07:30-22:30")
	}
	start, err := minuteOfDay(parts[0])
	if err != nil {
		return window{}, err
	}
	end, err := minuteOfDay(parts[1])
	if err != nil {
		return window{}, err
	}
	return window{enabled: true, start: start, end: end}, nil
}

func minuteOfDay(s string) (int, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("watch window time must look like 07:30")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("watch window time is invalid")
	}
	return h*60 + m, nil
}

func inWindow(t time.Time, spec string) bool {
	w, err := parseWindow(spec)
	if err != nil || !w.enabled {
		return true
	}
	local := t.In(shanghai)
	now := local.Hour()*60 + local.Minute()
	if w.start == w.end {
		return true
	}
	if w.start < w.end {
		return now >= w.start && now < w.end
	}
	return now >= w.start || now < w.end
}

func (r *Runner) load() {
	if strings.TrimSpace(r.cfg.StatePath) == "" {
		return
	}
	b, err := os.ReadFile(r.cfg.StatePath)
	if err != nil {
		return
	}
	var s stateFile
	if json.Unmarshal(b, &s) != nil || s.Entries == nil {
		return
	}
	r.state = s.Entries
}

func (r *Runner) save() error {
	if strings.TrimSpace(r.cfg.StatePath) == "" {
		return nil
	}
	keys := make([]string, 0, len(r.state))
	for k := range r.state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	entries := make(map[string]entry, len(keys))
	for _, k := range keys {
		entries[k] = r.state[k]
	}
	b, err := json.Marshal(stateFile{Version: 1, Entries: entries})
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.cfg.StatePath)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := r.cfg.StatePath + ".tmp"
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.cfg.StatePath)
}
