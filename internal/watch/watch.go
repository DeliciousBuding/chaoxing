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
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/capture"
	"github.com/DeliciousBuding/chaoxing/internal/classroomagent"
	"github.com/DeliciousBuding/chaoxing/internal/location"
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

type Location = location.Location

// SignCode holds an operator-provided password or gesture code. It stays in
// private configuration and is never copied into watch state or notifications.
type SignCode struct {
	Code string `json:"code"`
}

type Config struct {
	Interval              time.Duration
	IntervalJitterPercent int
	ClassPollDelayMin     time.Duration
	ClassPollDelayMax     time.Duration
	AutoSignDelayMin      time.Duration
	AutoSignDelayMax      time.Duration
	RepeatEvery           time.Duration
	MaxRepeats            int
	Window                string
	Lead                  time.Duration
	AutoSign              bool
	AutoAnswer            bool
	ClassroomAgent        *classroomagent.Agent
	Classes               []Class
	StatePath             string
	Capture               bool
	CaptureAccount        string
	CaptureRetention      time.Duration
	CaptureActivityPages  bool
	CaptureStore          capture.Store
	LocationResolver      *location.Resolver
	ActivityPageSource    ActivityPageSource
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

type AnswerActivitySource interface {
	AnswerActivity(ctx context.Context, courseID, classID, activityID string) (upstream.SignResult, error)
}

type ActivityPageSource interface {
	ActivityPage(ctx context.Context, args map[string]any) (any, error)
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

// Metrics is safe to expose from the public health endpoint: it contains only
// timing and counters, never course identities or upstream response bodies.
type Metrics struct {
	StartedAt         time.Time `json:"started_at"`
	LastTickAt        time.Time `json:"last_tick_at"`
	LastPanicAt       time.Time `json:"last_panic_at,omitempty"`
	ConsecutivePanics int       `json:"consecutive_panics"`
	LastPanicKind     string    `json:"last_panic_kind,omitempty"`
	Ticks             uint64    `json:"ticks"`
	ActiveClassPolls  uint64    `json:"active_class_polls"`
}

type Runner struct {
	cfg              Config
	source           ActivitySource
	signer           Signer
	answerer         AnswerActivitySource
	sender           notify.Sender
	alertSender      notify.Sender
	state            map[string]entry
	now              func() time.Time
	random           func() float64
	wait             func(context.Context, time.Duration) bool
	lastCapturePrune time.Time
	metrics          Metrics
	metricsMu        sync.RWMutex
	ticks            atomic.Uint64
	activePolls      atomic.Uint64
}

var shanghai = time.FixedZone("CST", 8*60*60)

func New(cfg Config, source ActivitySource, sender notify.Sender, signer Signer, answerer ...AnswerActivitySource) (*Runner, error) {
	if source == nil || sender == nil {
		return nil, errors.New("watch requires an activity source and a notifier")
	}
	if cfg.AutoSign && signer == nil {
		return nil, errors.New("auto sign requires a signer")
	}
	var answerSource AnswerActivitySource
	if len(answerer) > 0 {
		answerSource = answerer[0]
	}
	if cfg.AutoAnswer && answerSource == nil {
		return nil, errors.New("auto answer requires an answer activity source")
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
	if cfg.ClassPollDelayMin < 0 || cfg.ClassPollDelayMax < cfg.ClassPollDelayMin || cfg.ClassPollDelayMax > 10*time.Second {
		return nil, errors.New("class poll delay must be between 0 and 10 seconds")
	}
	if cfg.AutoSignDelayMin < 0 || cfg.AutoSignDelayMax < cfg.AutoSignDelayMin || cfg.AutoSignDelayMax > 2*time.Minute {
		return nil, errors.New("auto sign delay must be between 0 and 120 seconds")
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
	if cfg.Capture {
		if cfg.CaptureStore == nil || cfg.CaptureAccount == "" {
			return nil, errors.New("activity capture requires a store and account key")
		}
		if cfg.CaptureRetention < time.Hour || cfg.CaptureRetention > 30*24*time.Hour {
			return nil, errors.New("activity capture retention must be between 1 hour and 30 days")
		}
		if cfg.CaptureActivityPages && cfg.ActivityPageSource == nil {
			return nil, errors.New("activity page capture requires an activity page source")
		}
	}
	r := &Runner{
		cfg: cfg, source: source, signer: signer, sender: sender,
		// Panic alerts are retried; ordinary activity notifications keep one
		// attempt because the watcher already re-sends them on the next tick.
		alertSender: notify.Retry{Sender: sender, Attempts: 3, Delay: 2 * time.Second},
		answerer:    answerSource,
		state:       map[string]entry{}, now: time.Now, random: rand.Float64, wait: waitContext,
		metrics: Metrics{StartedAt: time.Now().UTC()},
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
	log.Printf("activity watch started: %d classes, interval %s (+/-%d%%), auto_sign=%t, auto_answer=%t", len(r.cfg.Classes), r.cfg.Interval, r.cfg.IntervalJitterPercent, r.cfg.AutoSign, r.cfg.AutoAnswer)
	// Poll once immediately: a restart during class must not wait for the idle
	// sleep cap before the first observation. Outside the window the tick is a
	// no-op, so this costs nothing on an idle deployment.
	select {
	case <-ctx.Done():
		return
	default:
		r.tick(ctx)
	}
	for {
		delay := r.nextDelay(r.now())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			log.Print("activity watch stopped")
			return
		case <-timer.C:
			r.tick(ctx)
		}
	}
}

// Metrics returns a safe snapshot for /healthz and the management dashboard.
func (r *Runner) Metrics() Metrics {
	r.metricsMu.RLock()
	defer r.metricsMu.RUnlock()
	out := r.metrics
	out.Ticks = r.ticks.Load()
	out.ActiveClassPolls = r.activePolls.Load()
	return out
}

// tick is the supervision boundary for one polling cycle. A panic is logged,
// counted and alerted, but must not terminate the long-running watcher.
func (r *Runner) tick(ctx context.Context) {
	defer func() {
		if value := recover(); value != nil {
			r.recordPanic(value)
		}
	}()
	now := r.now().UTC()
	r.metricsMu.Lock()
	r.metrics.LastTickAt = now
	r.metricsMu.Unlock()
	r.ticks.Add(1)
	r.Tick(ctx)
	r.metricsMu.Lock()
	r.metrics.ConsecutivePanics = 0
	r.metricsMu.Unlock()
}

func (r *Runner) recordPanic(value any) {
	now := r.now().UTC()
	kind := panicKind(value)
	r.metricsMu.Lock()
	r.metrics.LastPanicAt = now
	r.metrics.ConsecutivePanics++
	r.metrics.LastPanicKind = kind
	count := r.metrics.ConsecutivePanics
	r.metricsMu.Unlock()
	log.Printf("activity watch panic recovered (%d consecutive): %s\n%s", count, kind, debug.Stack())

	notifyCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	body := fmt.Sprintf(
		"连续 panic：%d\n最近类型：%s\nlast_tick：%s\nTick 已恢复，进程未退出；如持续增加请立即检查。",
		count, kind, now.Format(time.RFC3339),
	)
	if err := r.alertSender.Send(notifyCtx, notify.Message{Title: "🚨 学习通 Watcher panic 已恢复", Body: body, Priority: 9}); err != nil {
		log.Printf("activity watch panic alert failed: %v", err)
	}
}

func panicKind(value any) string {
	switch value.(type) {
	case runtime.Error:
		return "runtime"
	case string:
		return "explicit"
	default:
		return "unknown"
	}
}

func (r *Runner) jitterInterval() time.Duration {
	spread := time.Duration(float64(r.cfg.Interval) * float64(r.cfg.IntervalJitterPercent) / 100)
	if spread <= 0 {
		return r.cfg.Interval
	}
	return r.cfg.Interval - spread + time.Duration(r.random()*float64(2*spread))
}

// nextDelay keeps the normal jittered interval in class, but sleeps toward
// the next schedule boundary (capped at five minutes) when no class is active.
// This avoids a 24/7 one-minute wake-up while never entering a class late.
func (r *Runner) nextDelay(now time.Time) time.Duration {
	if !inWindow(now, r.cfg.Window) {
		if until := nextWindowOpen(now, r.cfg.Window); until > 0 && until < 5*time.Minute {
			return until
		}
		return 5 * time.Minute
	}
	for _, class := range r.cfg.Classes {
		if classActive(now, class, r.cfg.Lead) {
			return r.jitterInterval()
		}
	}
	next := nextClassStart(now, r.cfg.Classes, r.cfg.Lead)
	if next <= 0 {
		return 5 * time.Minute
	}
	if next < 5*time.Minute {
		return next
	}
	return 5 * time.Minute
}

func nextWindowOpen(t time.Time, spec string) time.Duration {
	w, err := parseWindow(spec)
	if err != nil || !w.enabled {
		return 0
	}
	local := t.In(shanghai)
	for day := 0; day <= 1; day++ {
		midnight := time.Date(local.Year(), local.Month(), local.Day()+day, 0, 0, 0, 0, shanghai)
		candidate := midnight.Add(time.Duration(w.start) * time.Minute)
		if candidate.After(local) {
			return candidate.Sub(local)
		}
	}
	return 0
}

func nextClassStart(t time.Time, classes []Class, lead time.Duration) time.Duration {
	local := t.In(shanghai)
	var best time.Time
	for _, class := range classes {
		if len(class.Schedule) == 0 {
			return 0
		}
		for day := 0; day <= 7; day++ {
			dayStart := time.Date(local.Year(), local.Month(), local.Day()+day, 0, 0, 0, 0, shanghai)
			for _, schedule := range class.Schedule {
				if !scheduleDayActive(dayStart, schedule.Days) {
					continue
				}
				start, err := minuteOfDay(schedule.Start)
				if err != nil {
					continue
				}
				candidate := dayStart.Add(time.Duration(start)*time.Minute - lead)
				if candidate.After(local) && (best.IsZero() || candidate.Before(best)) {
					best = candidate
				}
			}
		}
	}
	if best.IsZero() {
		return 0
	}
	return best.Sub(local)
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
	polledClasses := 0
	for _, class := range r.cfg.Classes {
		byCourseClass[class.CourseID+":"+class.ClassID] = class
		if !classActive(now, class, r.cfg.Lead) {
			continue
		}
		if polledClasses > 0 && !r.wait(ctx, r.classPollDelay()) {
			return
		}
		polledClasses++
		r.activePolls.Add(1)
		items, err := r.source.ListClassActivities(ctx, class.CourseID, class.ClassID)
		if err != nil {
			log.Printf("activity watch: class %s: %s", class.ClassID, upstream.SafeFault(err).Code)
			continue
		}
		if r.cfg.Capture {
			r.captureActivities(ctx, items, now)
		}
		for _, item := range items {
			if item.Signable || item.Answerable {
				k := key(item)
				active[k] = item
				classes[k] = class
			}
		}
	}
	if groupSource, ok := r.source.(GroupActivitySource); ok {
		// Course-group history is useful only while a watched class is active.
		// Scanning it all day wastes upstream quota and creates a fixed request pattern.
		if polledClasses == 0 {
			return
		}
		if !r.wait(ctx, r.classPollDelay()) {
			return
		}
		groupRows, err := groupSource.ListGroupActivities(ctx)
		if err != nil {
			log.Printf("activity watch: group feed: %s", upstream.SafeFault(err).Code)
		} else if r.cfg.Capture {
			r.captureActivities(ctx, groupRows, now)
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
		if item.Answerable {
			if r.cfg.AutoAnswer && (!seen || e.LastAttempt.IsZero()) {
				changed = r.autoAnswer(ctx, item, e) || changed
				continue
			}
			if seen && e.Status == "answered" {
				continue
			}
			if seen && (e.Count >= r.cfg.MaxRepeats || now.Sub(e.LastSent) < r.cfg.RepeatEvery) {
				continue
			}
			title, body := answerAlertTitle(item.Kind), formatAlertBody(item)
			if seen && e.Status == "failed" {
				title, body = "🚨 学习通自动抢答失败", formatFailureBody(item, e.Code)
			}
			priority := 6
			if seen && e.Status == "failed" {
				priority = 8
			}
			if err := r.sender.Send(ctx, notify.Message{Title: title, Body: body, Priority: priority}); err != nil {
				log.Printf("activity watch: notify failed: %v", err)
				continue
			}
			r.state[k] = entry{LastSent: now, Count: e.Count + 1, Status: e.Status, Method: e.Method, Code: e.Code, LastAttempt: e.LastAttempt}
			changed = true
			continue
		}
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
		priority := 6
		if seen && e.Status == "failed" {
			priority = 8
			if e.Count >= 1 {
				priority = 9
			}
		}
		if err := r.sender.Send(ctx, notify.Message{Title: title, Body: body, Priority: priority}); err != nil {
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
	if r.cfg.Capture && (r.lastCapturePrune.IsZero() || now.Sub(r.lastCapturePrune) >= time.Hour) {
		if err := r.cfg.CaptureStore.PruneActivityCaptures(ctx, now.Add(-r.cfg.CaptureRetention)); err != nil {
			log.Printf("activity capture prune failed: %v", err)
		} else {
			r.lastCapturePrune = now
		}
	}
	if changed {
		if err := r.save(); err != nil {
			log.Printf("activity watch: state write failed: %v", err)
		}
	}
}

func (r *Runner) classPollDelay() time.Duration {
	return randomDuration(r.cfg.ClassPollDelayMin, r.cfg.ClassPollDelayMax, r.random)
}

func (r *Runner) autoSignDelay() time.Duration {
	return randomDuration(r.cfg.AutoSignDelayMin, r.cfg.AutoSignDelayMax, r.random)
}

func randomDuration(min, max time.Duration, random func() float64) time.Duration {
	if min <= 0 || max <= min {
		return min
	}
	return min + time.Duration(random()*float64(max-min))
}

func waitContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (r *Runner) captureActivities(ctx context.Context, items []upstream.Activity, now time.Time) {
	for _, item := range items {
		payload, err := json.Marshal(item)
		if err != nil {
			continue
		}
		record := capture.Activity{
			AccountKey: r.cfg.CaptureAccount, ActivityKey: capture.Key(item.CourseID, item.ClassID, item.ID),
			CourseID: item.CourseID, ClassID: item.ClassID, ActivityID: item.ID, Kind: item.Kind,
			State: item.State, UserState: item.UserState, SignType: item.SignType, Source: item.Source,
			FirstSeenAt: now.UTC(), LastSeenAt: now.UTC(), ActivityJSON: payload,
		}
		saved, err := r.cfg.CaptureStore.SaveActivityCapture(ctx, record)
		if err != nil {
			log.Printf("activity capture save failed: class=%s code=%s", item.ClassID, upstream.SafeFault(err).Code)
			continue
		}
		if !r.cfg.CaptureActivityPages || (item.Kind != "practice" && item.Kind != "homework") || saved.HadPage {
			continue
		}
		page, err := r.cfg.ActivityPageSource.ActivityPage(ctx, map[string]any{
			"course_id": item.CourseID, "class_id": item.ClassID, "activity_id": item.ID,
		})
		var pageJSON []byte
		pageError := ""
		if err != nil {
			pageError = upstream.SafeFault(err).Code
		} else if pageJSON, err = json.Marshal(page); err != nil || len(pageJSON) > capture.MaxPageJSONBytes {
			pageJSON, pageError = nil, "CAPTURE_TOO_LARGE"
		}
		if err := r.cfg.CaptureStore.SaveActivityPage(ctx, record.AccountKey, record.ActivityKey, pageJSON, pageError); err != nil {
			log.Printf("activity page capture save failed: class=%s code=%s", item.ClassID, upstream.SafeFault(err).Code)
		}
	}
}

func (r *Runner) autoAnswer(ctx context.Context, item upstream.Activity, e entry) bool {
	if agent := r.cfg.ClassroomAgent; agent != nil {
		decision := agent.Decide(ctx, classroomagent.FromUpstream(item))
		switch decision.Action {
		case classroomagent.ActionSkip:
			log.Printf("activity watch: classroom agent skipped %s: %s", item.ID, decision.Reason)
			return false
		case classroomagent.ActionNotify:
			return r.recordAnswerReview(ctx, item, e, decision)
		}
	}
	result, err := r.answerer.AnswerActivity(ctx, item.CourseID, item.ClassID, item.ID)
	now := r.now()
	next := entry{LastAttempt: now, Count: e.Count, LastSent: e.LastSent, Status: e.Status, Method: e.Method, Code: e.Code}
	title, body := "✅ 学习通自动抢答成功", formatSuccessBody(item, result)
	if err != nil {
		safe := upstream.SafeFault(err)
		next.Status, next.Code = "failed", safe.Code
		title, body = "🚨 学习通自动抢答失败", formatFailureBody(item, safe.Code)
	} else {
		next.Status, next.Method, next.Code = "answered", result.Method, ""
	}
	r.state[key(item)] = next
	priority := 6
	if err != nil {
		priority = 8
	}
	if sendErr := r.sender.Send(ctx, notify.Message{Title: title, Body: body, Priority: priority}); sendErr != nil {
		log.Printf("activity watch: notify failed: %v", sendErr)
	} else {
		next.LastSent, next.Count = now, e.Count+1
	}
	log.Printf("activity watch: auto answer %s: status=%s", item.ID, next.Status)
	return true
}

// recordAnswerReview persists a classroom-agent decision that requires the
// operator instead of performing the upstream claim. Generated answers, when
// present, are included in the alert so the operator can act immediately.
func (r *Runner) recordAnswerReview(ctx context.Context, item upstream.Activity, e entry, decision classroomagent.Decision) bool {
	now := r.now()
	next := entry{LastAttempt: now, Count: e.Count, LastSent: e.LastSent, Status: "review", Method: "agent", Code: decision.Reason}
	title, body := "🧠 学习通课堂答题需要确认", formatReviewBody(item, decision)
	if err := r.sender.Send(ctx, notify.Message{Title: title, Body: body, Priority: 7}); err != nil {
		log.Printf("activity watch: notify failed: %v", err)
	} else {
		next.LastSent, next.Count = now, e.Count+1
	}
	r.state[key(item)] = next
	log.Printf("activity watch: classroom agent review %s: %s", item.ID, decision.Reason)
	return true
}

func (r *Runner) autoSign(ctx context.Context, class Class, item upstream.Activity, e entry) bool {
	if delay := r.autoSignDelay(); delay > 0 && !r.wait(ctx, delay) {
		return false
	}
	req := upstream.SignRequest{CourseID: item.CourseID, ClassID: item.ClassID, ActivityID: item.ID}
	if r.cfg.LocationResolver != nil {
		loc, resolveErr := r.cfg.LocationResolver.Resolve(ctx, item.CourseID, item.ClassID, activeScheduleKey(r.now(), class))
		if resolveErr != nil {
			now := r.now()
			next := entry{LastAttempt: now, Count: e.Count, LastSent: e.LastSent, Status: "failed", Code: "LOCATION_RESOLVE_FAILED"}
			r.state[key(item)] = next
			if sendErr := r.sender.Send(ctx, notify.Message{Title: "🚨 学习通位置解析失败", Body: formatFailureBody(item, "LOCATION_RESOLVE_FAILED"), Priority: 8}); sendErr != nil {
				log.Printf("activity watch: notify failed: %v", sendErr)
			}
			return true
		}
		if loc != nil {
			lat, lng := loc.Latitude, loc.Longitude
			req.Latitude, req.Longitude, req.Address = &lat, &lng, loc.Address
		}
	}
	if class.Location != nil && req.Latitude == nil {
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
	priority := 6
	if err != nil {
		priority = 8
	}
	if sendErr := r.sender.Send(ctx, notify.Message{Title: title, Body: body, Priority: priority}); sendErr != nil {
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

func activeScheduleKey(t time.Time, class Class) string {
	local := t.In(shanghai)
	for _, schedule := range class.Schedule {
		if !scheduleDayActive(local, schedule.Days) {
			continue
		}
		start, startErr := minuteOfDay(schedule.Start)
		end, endErr := minuteOfDay(schedule.End)
		if startErr != nil || endErr != nil {
			continue
		}
		if start == end || (local.Hour()*60+local.Minute() >= int(start) && local.Hour()*60+local.Minute() <= int(end)) {
			return strings.Join(schedule.Days, ",") + "-" + schedule.Start + "-" + schedule.End
		}
	}
	return ""
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

// answerAlertTitle distinguishes rapid answers from classroom practice and
// homework so an operator can triage the notification without opening it.
func answerAlertTitle(kind string) string {
	switch kind {
	case "practice":
		return "📝 学习通课堂练习提醒"
	case "homework":
		return "📝 学习通课堂作业提醒"
	default:
		return "🔔 学习通抢答提醒"
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

func formatReviewBody(a upstream.Activity, decision classroomagent.Decision) string {
	lines := []string{
		"课程：" + firstNonBlank(a.Course, a.CourseID),
		"活动：" + firstNonBlank(a.Title, a.ID),
	}
	if a.Source == "group" && a.GroupName != "" {
		lines = append(lines, "来源：课程群 "+a.GroupName)
	}
	detail := strings.TrimSpace(decision.Detail)
	if detail == "" {
		detail = decision.Reason
	}
	lines = append(lines, "原因："+detail)
	for _, ans := range decision.Answers {
		value := strings.ReplaceAll(ans.Text, "\n", " / ")
		if len(value) > 120 {
			value = value[:120] + "…"
		}
		lines = append(lines, "AI答案："+value+"（置信度 "+strconv.FormatFloat(ans.Confidence, 'f', 2, 64)+"）")
	}
	lines = append(lines, "系统不会自动提交，请人工确认。")
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
