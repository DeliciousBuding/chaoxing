// Package watch owns course-time polling and the local automatic sign-in
// decision. Protocol details remain in internal/upstream.
package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/persist"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

type Class struct {
	CourseID string     `json:"course_id"`
	ClassID  string     `json:"class_id"`
	Schedule []Schedule `json:"schedule,omitempty"`
	Location *Location  `json:"location,omitempty"`
	SignCode *SignCode  `json:"sign_code,omitempty"`
}

type Schedule struct {
	Days  []string `json:"days"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Address   string  `json:"address,omitempty"`
}

// SignCode holds an operator-provided password or gesture code. It stays in
// private configuration and is never copied into watch state or notifications.
type SignCode struct {
	Code string `json:"code"`
}

type Config struct {
	Interval              time.Duration
	IntervalJitterPercent int
	RepeatEvery           time.Duration
	MaxRepeats            int
	Window                string
	Lead                  time.Duration
	AutoSign              bool
	Classes               []Class
	StatePath             string
}

type ActivitySource interface {
	ListClassActivities(ctx context.Context, courseID, classID string) ([]upstream.Activity, error)
}

// GroupActivitySource is optional. It exposes course-group message cards
// without teaching the watcher any IM protocol details.
type GroupActivitySource interface {
	ListGroupActivities(ctx context.Context) ([]upstream.Activity, error)
}

type Signer interface {
	SignActivity(ctx context.Context, req upstream.SignRequest) (upstream.SignResult, error)
}

type entry struct {
	LastSent    time.Time `json:"last_sent"`
	LastAttempt time.Time `json:"last_attempt,omitempty"`
	Count       int       `json:"count"`
	Status      string    `json:"status,omitempty"`
	Method      string    `json:"method,omitempty"`
	Code        string    `json:"code,omitempty"`
}

type stateFile struct {
	Version int              `json:"version"`
	Entries map[string]entry `json:"entries"`
}

type Runner struct {
	cfg    Config
	source ActivitySource
	signer Signer
	sender notify.Sender
	state  map[string]entry
	now    func() time.Time
	random func() float64
}

var shanghai = time.FixedZone("CST", 8*60*60)

func New(cfg Config, source ActivitySource, sender notify.Sender, signer Signer) (*Runner, error) {
	if source == nil || sender == nil {
		return nil, errors.New("watch requires an activity source and a notifier")
	}
	if cfg.AutoSign && signer == nil {
		return nil, errors.New("auto sign requires a signer")
	}
	if cfg.Interval < 30*time.Second {
		return nil, errors.New("watch interval must be at least 30 seconds")
	}
	if cfg.IntervalJitterPercent < 0 {
		return nil, errors.New("watch interval jitter must not be negative")
	}
	if cfg.IntervalJitterPercent == 0 {
		cfg.IntervalJitterPercent = 10
	}
	if cfg.IntervalJitterPercent > 25 {
		return nil, errors.New("watch interval jitter must be at most 25 percent")
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
	if cfg.Lead < -time.Second {
		return nil, errors.New("watch lead seconds must be -1 or between 0 and 600")
	}
	if cfg.Lead < 0 {
		cfg.Lead = 0
	} else if cfg.Lead == 0 {
		cfg.Lead = 2 * time.Minute
	}
	if cfg.Lead > 10*time.Minute {
		cfg.Lead = 10 * time.Minute
	}
	if len(cfg.Classes) == 0 {
		return nil, errors.New("watch requires at least one class")
	}
	if len(cfg.Classes) > 8 {
		return nil, errors.New("watch supports at most eight classes")
	}
	if _, err := parseWindow(cfg.Window); err != nil {
		return nil, err
	}
	if err := validateClasses(cfg.Classes); err != nil {
		return nil, err
	}
	r := &Runner{
		cfg: cfg, source: source, signer: signer, sender: sender,
		state: map[string]entry{}, now: time.Now, random: rand.Float64,
	}
	if loadErr := r.load(); loadErr != nil {
		return nil, loadErr
	}
	return r, nil
}

func validateClasses(classes []Class) error {
	for _, class := range classes {
		if strings.TrimSpace(class.CourseID) == "" || strings.TrimSpace(class.ClassID) == "" {
			return errors.New("watch classes require course_id and class_id")
		}
		if class.Location != nil && (class.Location.Latitude < -90 || class.Location.Latitude > 90 || class.Location.Longitude < -180 || class.Location.Longitude > 180) {
			return errors.New("watch class location is outside range")
		}
		for _, schedule := range class.Schedule {
			if len(schedule.Days) == 0 {
				return errors.New("watch class schedule requires days")
			}
			for _, day := range schedule.Days {
				if !validDay(day) {
					return errors.New("watch class schedule day is invalid")
				}
			}
			if _, err := minuteOfDay(schedule.Start); err != nil {
				return err
			}
			if _, err := minuteOfDay(schedule.End); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runner) Run(ctx context.Context) {
	log.Printf("activity watch started: %d classes, interval %s (+/-%d%%), auto_sign=%t", len(r.cfg.Classes), r.cfg.Interval, r.cfg.IntervalJitterPercent, r.cfg.AutoSign)
	for {
		delay := r.jitterInterval()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			log.Print("activity watch stopped")
			return
		case <-timer.C:
			r.Tick(ctx)
		}
	}
}

func (r *Runner) jitterInterval() time.Duration {
	spread := time.Duration(float64(r.cfg.Interval) * float64(r.cfg.IntervalJitterPercent) / 100)
	if spread <= 0 {
		return r.cfg.Interval
	}
	return r.cfg.Interval - spread + time.Duration(r.random()*float64(2*spread))
}

// Tick polls only classes whose course schedule is active, attempts each new
// signable activity at most once, and falls back to alerts for manual types.
func (r *Runner) Tick(ctx context.Context) {
	now := r.now()
	if !inWindow(now, r.cfg.Window) {
		return
	}
	active := map[string]upstream.Activity{}
	classes := map[string]Class{}
	byCourseClass := map[string]Class{}
	for _, class := range r.cfg.Classes {
		byCourseClass[class.CourseID+":"+class.ClassID] = class
		if !classActive(now, class, r.cfg.Lead) {
			continue
		}
		items, err := r.source.ListClassActivities(ctx, class.CourseID, class.ClassID)
		if err != nil {
			log.Printf("activity watch: class %s: %s", class.ClassID, upstream.SafeFault(err).Code)
			continue
		}
		for _, item := range items {
			if item.Signable {
				k := key(item)
				active[k] = item
				classes[k] = class
			}
		}
	}
	if groupSource, ok := r.source.(GroupActivitySource); ok {
		groupRows, err := groupSource.ListGroupActivities(ctx)
		if err != nil {
			log.Printf("activity watch: group feed: %s", upstream.SafeFault(err).Code)
		}
		for _, item := range groupRows {
			class, ok := byCourseClass[item.CourseID+":"+item.ClassID]
			if !ok || !classActive(now, class, r.cfg.Lead) || !item.Signable {
				continue
			}
			k := key(item)
			if _, exists := active[k]; !exists {
				active[k] = item
				classes[k] = class
			}
		}
	}
	changed := false
	for k, item := range active {
		e, seen := r.state[k]
		if r.cfg.AutoSign && autoSignType(item, classes[k]) && (!seen || e.LastAttempt.IsZero()) {
			changed = r.autoSign(ctx, classes[k], item, e) || changed
			continue
		}
		if seen && e.Status == "signed" {
			continue
		}
		if seen && e.Count >= r.cfg.MaxRepeats {
			continue
		}
		if seen && now.Sub(e.LastSent) < r.cfg.RepeatEvery {
			continue
		}
		title, body := "🔔 学习通签到提醒", formatAlertBody(item)
		if seen && e.Status == "failed" {
			title, body = "🚨 学习通自动签到失败", formatFailureBody(item, e.Code)
		}
		if err := r.sender.Send(ctx, notify.Message{Title: title, Body: body}); err != nil {
			log.Printf("activity watch: notify failed: %v", err)
			continue
		}
		r.state[k] = entry{LastSent: now, Count: e.Count + 1, Status: e.Status, Method: e.Method, Code: e.Code, LastAttempt: e.LastAttempt}
		changed = true
		log.Printf("activity watch: alerted %s (attempt %d)", item.ID, e.Count+1)
	}
	for k, e := range r.state {
		if _, still := active[k]; !still && now.Sub(maxTime(e.LastSent, e.LastAttempt)) > 6*time.Hour {
			delete(r.state, k)
			changed = true
		}
		if now.Sub(maxTime(e.LastSent, e.LastAttempt)) > 24*time.Hour {
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

func (r *Runner) autoSign(ctx context.Context, class Class, item upstream.Activity, e entry) bool {
	req := upstream.SignRequest{CourseID: item.CourseID, ClassID: item.ClassID, ActivityID: item.ID}
	if class.Location != nil {
		lat, lng := class.Location.Latitude, class.Location.Longitude
		req.Latitude, req.Longitude, req.Address = &lat, &lng, class.Location.Address
	}
	if class.SignCode != nil {
		req.SignCode = class.SignCode.Code
	}
	result, err := r.signer.SignActivity(ctx, req)
	now := r.now()
	next := entry{LastAttempt: now, Count: e.Count, LastSent: e.LastSent, Status: e.Status, Method: e.Method, Code: e.Code}
	title, body := "✅ 学习通自动签到成功", formatSuccessBody(item, result)
	if err != nil {
		safe := upstream.SafeFault(err)
		next.Status, next.Code = "failed", safe.Code
		title, body = "🚨 学习通自动签到失败", formatFailureBody(item, safe.Code)
	} else {
		next.Status, next.Method, next.Code = "signed", result.Method, ""
	}
	r.state[key(item)] = next
	if sendErr := r.sender.Send(ctx, notify.Message{Title: title, Body: body}); sendErr != nil {
		log.Printf("activity watch: notify failed: %v", sendErr)
	} else {
		next.LastSent, next.Count = now, e.Count+1
	}
	log.Printf("activity watch: auto sign %s: status=%s", item.ID, next.Status)
	return true
}

func autoSignType(item upstream.Activity, class Class) bool {
	switch item.SignType {
	case "normal_or_photo", "location":
		return true
	case "gesture", "password":
		return class.SignCode != nil && strings.TrimSpace(class.SignCode.Code) != ""
	default:
		return false
	}
}

func classActive(t time.Time, class Class, lead time.Duration) bool {
	if len(class.Schedule) == 0 {
		return true
	}
	local := t.In(shanghai)
	nowSecond := local.Hour()*3600 + local.Minute()*60 + local.Second()
	for _, schedule := range class.Schedule {
		if !scheduleDayActive(local, schedule.Days) {
			continue
		}
		start, _ := minuteOfDay(schedule.Start)
		end, _ := minuteOfDay(schedule.End)
		if start == end {
			return true
		}
		startSecond, endSecond := start*60, end*60
		if start < end && nowSecond >= startSecond-int(lead.Seconds()) && nowSecond < endSecond {
			return true
		}
		if start > end && (nowSecond >= startSecond-int(lead.Seconds()) || nowSecond < endSecond) {
			return true
		}
	}
	return false
}

func scheduleDayActive(t time.Time, days []string) bool {
	current := map[int]bool{int(t.Weekday()): true}
	if t.Weekday() == time.Sunday {
		current[7] = true
	}
	for _, day := range days {
		n, ok := dayNumber(day)
		if ok && current[n] {
			return true
		}
	}
	return false
}

func dayNumber(day string) (int, bool) {
	if n, err := strconv.Atoi(strings.TrimSpace(day)); err == nil {
		return n, true
	}
	switch strings.ToLower(strings.TrimSpace(day)) {
	case "sun":
		return 0, true
	case "mon":
		return 1, true
	case "tue":
		return 2, true
	case "wed":
		return 3, true
	case "thu":
		return 4, true
	case "fri":
		return 5, true
	case "sat":
		return 6, true
	default:
		return 0, false
	}
}

func validDay(day string) bool {
	if n, err := strconv.Atoi(strings.TrimSpace(day)); err == nil {
		return n >= 0 && n <= 7
	}
	switch strings.ToLower(strings.TrimSpace(day)) {
	case "mon", "tue", "wed", "thu", "fri", "sat", "sun":
		return true
	default:
		return false
	}
}

func formatAlertBody(a upstream.Activity) string {
	lines := []string{"课程：" + firstNonBlank(a.Course, a.CourseID), "活动：" + firstNonBlank(a.Title, a.ID)}
	if a.Source == "group" && a.GroupName != "" {
		lines = append(lines, "来源：课程群 "+a.GroupName)
	}
	if a.SignType == "gesture" || a.SignType == "password" {
		kind := "密码签到"
		if a.SignType == "gesture" {
			kind = "手势签到"
		}
		lines = append(lines, "类型："+kind+"，需要签到码")
	}
	if a.StartsAt != nil {
		lines = append(lines, "开始："+a.StartsAt.In(shanghai).Format("15:04"))
	}
	lines = append(lines, "状态：进行中，尚未签到", "请立即打开学习通。")
	return strings.Join(lines, "\n")
}

func formatSuccessBody(a upstream.Activity, result upstream.SignResult) string {
	lines := []string{
		"课程：" + firstNonBlank(a.Course, a.CourseID),
		"活动：" + firstNonBlank(a.Title, a.ID),
	}
	if a.Source == "group" && a.GroupName != "" {
		lines = append(lines, "来源：课程群 "+a.GroupName)
	}
	lines = append(lines,
		"方式："+result.Method,
		"状态：已签到并通过上游状态复核",
	)
	return strings.Join(lines, "\n")
}

func formatFailureBody(a upstream.Activity, code string) string {
	lines := []string{
		"课程：" + firstNonBlank(a.Course, a.CourseID),
		"活动：" + firstNonBlank(a.Title, a.ID),
	}
	if a.Source == "group" && a.GroupName != "" {
		lines = append(lines, "来源：课程群 "+a.GroupName)
	}
	lines = append(lines,
		"错误："+code,
		"系统不会自动重试该活动，请立即人工处理。",
	)
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

func maxTime(values ...time.Time) time.Time {
	var out time.Time
	for _, v := range values {
		if v.After(out) {
			out = v
		}
	}
	return out
}

func (r *Runner) load() error {
	if strings.TrimSpace(r.cfg.StatePath) == "" {
		return nil
	}
	b, err := os.ReadFile(r.cfg.StatePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var s stateFile
	if json.Unmarshal(b, &s) != nil || s.Entries == nil || (s.Version != 1 && s.Version != 2) {
		return errors.New("watch state is invalid")
	}
	if s.Version == 1 {
		for k, e := range s.Entries {
			e.LastAttempt = time.Time{}
			s.Entries[k] = e
		}
	}
	r.state = s.Entries
	return nil
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
	b, err := json.Marshal(stateFile{Version: 2, Entries: entries})
	if err != nil {
		return err
	}
	return persist.Write(r.cfg.StatePath, b, 0o600)
}
