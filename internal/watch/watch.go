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
	// Key is the stable identity used by per-schedule location bindings.
	// Operators should set it when two entries can share the same time slot
	// (for example a regular class and a room-changed guidance class).
	Key   string   `json:"key,omitempty"`
	Days  []string `json:"days"`
	Start string   `json:"start"`
	End   string   `json:"end"`
	// Weeks restricts the entry to teaching weeks ("1-6", "7,13", "1-16").
	// Empty keeps the legacy behaviour of every week.
	Weeks string `json:"weeks,omitempty"`
	// Room is display metadata and is also returned by the schedule
	// projection. Sign-in coordinates always come from the location binding.
	Room string `json:"room,omitempty"`
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
	// Lead is the legacy pre-class padding. PollBefore/PollAfter supersede it.
	Lead       time.Duration
	PollBefore time.Duration
	PollAfter  time.Duration
	// GroupScanInterval bounds the optional Easemob course-group fallback.
	// The class feed remains the primary, every-tick source.
	GroupScanInterval time.Duration
	// TermStart is the first day of teaching week 1; required once any
	// schedule carries a Weeks restriction.
	TermStart            time.Time
	AutoSign             bool
	AutoAnswer           bool
	ClassroomAgent       *classroomagent.Agent
	Classes              []Class
	StatePath            string
	Capture              bool
	CaptureAccount       string
	CaptureRetention     time.Duration
	CaptureActivityPages bool
	CaptureStore         capture.Store
	LocationResolver     *location.Resolver
	ActivityPageSource   ActivityPageSource
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
	RetryAt     time.Time `json:"retry_at,omitempty"`
	RetryCount  int       `json:"retry_count,omitempty"`
	Count       int       `json:"count"`
	Status      string    `json:"status,omitempty"`
	Method      string    `json:"method,omitempty"`
	Code        string    `json:"code,omitempty"`
}

// pendingAlert is a notification that could not be delivered after the
// upstream action already succeeded or failed. Keeping it separate from the
// activity entry means a retry never re-runs the sign-in itself.
type pendingAlert struct {
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Priority  int       `json:"priority"`
	Attempts  int       `json:"attempts"`
	NextAt    time.Time `json:"next_at"`
	LastError string    `json:"last_error,omitempty"`
}

type stateFile struct {
	Version int                     `json:"version"`
	Entries map[string]entry        `json:"entries"`
	Alerts  map[string]pendingAlert `json:"alerts,omitempty"`
}

// Metrics is safe to expose from the public health endpoint: it contains only
// timing and counters, never course identities or upstream response bodies.
type Metrics struct {
	StartedAt         time.Time `json:"started_at"`
	LastTickStartedAt time.Time `json:"last_tick_started_at,omitempty"`
	LastTickAt        time.Time `json:"last_tick_at"`
	LastPanicAt       time.Time `json:"last_panic_at,omitempty"`
	ConsecutivePanics int       `json:"consecutive_panics"`
	LastPanicKind     string    `json:"last_panic_kind,omitempty"`
	Ticks             uint64    `json:"ticks"`
	ActiveClassPolls  uint64    `json:"active_class_polls"`
	StateDegraded     bool      `json:"state_degraded,omitempty"`
}

type Runner struct {
	cfg              Config
	source           ActivitySource
	signer           Signer
	answerer         AnswerActivitySource
	sender           notify.Sender
	alertSender      notify.Sender
	state            map[string]entry
	alerts           map[string]pendingAlert
	panicAlerts      chan notify.Message
	signDelayLeft    time.Duration
	now              func() time.Time
	random           func() float64
	wait             func(context.Context, time.Duration) bool
	lastCapturePrune time.Time
	lastGroupScan    time.Time
	metrics          Metrics
	metricsMu        sync.RWMutex
	ticks            atomic.Uint64
	activePolls      atomic.Uint64
	tickMu           sync.Mutex
	runStarted       atomic.Bool
}

var shanghai = time.FixedZone("CST", 8*60*60)

const (
	defaultPollPadding = 20 * time.Minute
	maxPollPadding     = 30 * time.Minute
	maxWatchStateBytes = 2 << 20
	// A tick has three independently valuable phases: discovery, sign-in and
	// best-effort evidence collection. Keep an overall supervision budget, but
	// give discovery its own shorter budget so a slow class feed cannot consume
	// the time reserved for signing.
	tickBudget        = 150 * time.Second
	pollBudget        = 60 * time.Second
	groupScanBudget   = 30 * time.Second
	maxSignAttempts   = 4
	maxAlertAttempts  = 6
	alertRetryBase    = 30 * time.Second
	alertRetryMax     = 5 * time.Minute
	maxAlertsPerFlush = 2
	// A pending alert retry must never delay the discovery/sign-in path for
	// long; a black-holed push endpoint is bounded by this shared budget.
	alertFlushBudget = 6 * time.Second
	// A sign-in burst must not spend the whole tick budget sleeping. The
	// jitter stays, but all automatic sign-ins in one tick share this cap.
	maxSignDelayPerTick = 30 * time.Second
	signActionTimeout   = 30 * time.Second
)

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
	if cfg.GroupScanInterval == 0 {
		cfg.GroupScanInterval = 5 * time.Minute
	}
	if cfg.GroupScanInterval < time.Minute || cfg.GroupScanInterval > 30*time.Minute {
		return nil, errors.New("watch group scan interval must be between 1 and 30 minutes")
	}
	before, after, err := Padding(cfg.PollBefore, cfg.PollAfter, cfg.Lead)
	if err != nil {
		return nil, err
	}
	cfg.PollBefore, cfg.PollAfter = before, after
	if cfg.TermStart.IsZero() {
		for _, class := range cfg.Classes {
			for _, schedule := range class.Schedule {
				if strings.TrimSpace(schedule.Weeks) != "" {
					return nil, errors.New("watch term_start is required when a schedule restricts weeks")
				}
			}
		}
	} else {
		local := cfg.TermStart.In(shanghai)
		cfg.TermStart = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, shanghai)
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
		state:       map[string]entry{}, alerts: map[string]pendingAlert{},
		panicAlerts: make(chan notify.Message, 8),
		now:         time.Now, random: rand.Float64, wait: waitContext,
		metrics: Metrics{StartedAt: time.Now().UTC()},
	}
	if loadErr := r.load(); loadErr != nil {
		return nil, loadErr
	}
	return r, nil
}

// Padding resolves the polling window around a class. PollBefore/PollAfter
// are the current configuration; Lead remains accepted so an old deployment
// can be upgraded without silently changing its semantics.
func Padding(before, after, legacy time.Duration) (time.Duration, time.Duration, error) {
	if legacy < -time.Second {
		return 0, 0, errors.New("watch lead seconds must be -1 or between 0 and 1800")
	}
	if before == 0 && after == 0 {
		switch {
		case legacy < 0:
			before, after = 0, 0
		case legacy > 0:
			before, after = legacy, 0
		default:
			before, after = defaultPollPadding, defaultPollPadding
		}
	} else {
		if before == 0 {
			before = defaultPollPadding
		}
		if after == 0 {
			after = defaultPollPadding
		}
	}
	if before < 0 || after < 0 || before > maxPollPadding || after > maxPollPadding {
		return 0, 0, errors.New("watch poll padding must be between 0 and 30 minutes")
	}
	return before, after, nil
}

func validateClasses(classes []Class) error {
	for _, class := range classes {
		if strings.TrimSpace(class.CourseID) == "" || strings.TrimSpace(class.ClassID) == "" {
			return errors.New("watch classes require course_id and class_id")
		}
		if class.Location != nil && (class.Location.Latitude < -90 || class.Location.Latitude > 90 || class.Location.Longitude < -180 || class.Location.Longitude > 180) {
			return errors.New("watch class location is outside range")
		}
		keys := map[string]bool{}
		for _, schedule := range class.Schedule {
			if len(schedule.Days) == 0 {
				return errors.New("watch class schedule requires days")
			}
			for _, day := range schedule.Days {
				if !validDay(day) {
					return errors.New("watch class schedule day is invalid")
				}
			}
			start, err := minuteOfDay(schedule.Start)
			if err != nil {
				return err
			}
			end, err := minuteOfDay(schedule.End)
			if err != nil {
				return err
			}
			if start == end {
				return errors.New("watch class schedule start and end must differ")
			}
			if _, err := NormalizeWeeks(schedule.Weeks); err != nil {
				return err
			}
			if strings.TrimSpace(schedule.Key) != "" {
				if err := ValidateScheduleKey(schedule.Key); err != nil {
					return err
				}
			}
			if len([]rune(strings.TrimSpace(schedule.Room))) > 64 {
				return errors.New("watch class schedule room is too long")
			}
			key := schedule.EffectiveKey()
			if keys[key] {
				return fmt.Errorf("watch class schedule key %q is duplicated", key)
			}
			keys[key] = true
		}
	}
	return nil
}

// ValidateScheduleKey constrains the identity used by location bindings.
func ValidateScheduleKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 128 {
		return errors.New("watch schedule key must be between 1 and 128 bytes")
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':':
		default:
			return errors.New("watch schedule key may contain only letters, digits, dot, dash, underscore and colon")
		}
	}
	return nil
}

// EffectiveKey returns the stable schedule identity used by location
// bindings. Explicit keys win; generated keys include the teaching-week
// restriction so a regular Tuesday slot and a Tuesday guidance slot do not
// collide.
func (s Schedule) EffectiveKey() string {
	if key := strings.TrimSpace(s.Key); key != "" {
		return key
	}
	days := make([]string, 0, len(s.Days))
	for _, day := range s.Days {
		days = append(days, strings.TrimSpace(day))
	}
	sort.Slice(days, func(i, j int) bool {
		ni, _ := dayNumber(days[i])
		nj, _ := dayNumber(days[j])
		return ni < nj
	})
	key := strings.Join(days, ",") + "-" + strings.TrimSpace(s.Start) + "-" + strings.TrimSpace(s.End)
	if weeks, err := NormalizeWeeks(s.Weeks); err == nil && weeks != "" {
		key += "-W" + strings.ReplaceAll(weeks, ",", ".")
	}
	return key
}

// NormalizeWeeks validates and canonicalizes a teaching-week restriction such
// as "1-6", "7,13" or "13-16". Empty means every week.
func NormalizeWeeks(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return "", nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[[2]int]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", errors.New("watch weeks contains an empty range")
		}
		bounds := strings.Split(part, "-")
		if len(bounds) > 2 {
			return "", errors.New("watch weeks must look like 1-6 or 7,13")
		}
		start, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
		if err != nil || start < 1 || start > 60 {
			return "", errors.New("watch weeks must be between 1 and 60")
		}
		end := start
		if len(bounds) == 2 {
			end, err = strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil || end < start || end > 60 {
				return "", errors.New("watch week range is invalid")
			}
		}
		pair := [2]int{start, end}
		if seen[pair] {
			continue
		}
		seen[pair] = true
		if start == end {
			out = append(out, strconv.Itoa(start))
		} else {
			out = append(out, strconv.Itoa(start)+"-"+strconv.Itoa(end))
		}
	}
	if len(out) == 0 {
		return "", errors.New("watch weeks is empty")
	}
	return strings.Join(out, ","), nil
}

func (r *Runner) Run(ctx context.Context) {
	if !r.runStarted.CompareAndSwap(false, true) {
		log.Print("activity watch already running; duplicate Run ignored")
		return
	}
	defer r.runStarted.Store(false)
	log.Printf("activity watch started: %d classes, interval %s (+/-%d%%), auto_sign=%t, auto_answer=%t", len(r.cfg.Classes), r.cfg.Interval, r.cfg.IntervalJitterPercent, r.cfg.AutoSign, r.cfg.AutoAnswer)
	go r.runPanicAlerts(ctx)
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

// runPanicAlerts drains panic notifications off the polling path. A slow push
// endpoint previously delayed the next tick by up to the alert timeout.
func (r *Runner) runPanicAlerts(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-r.panicAlerts:
			r.sendPanicAlert(message)
		}
	}
}

func (r *Runner) sendPanicAlert(message notify.Message) {
	notifyCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := r.alertSender.Send(notifyCtx, message); err != nil {
		log.Printf("activity watch panic alert failed: %v", err)
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
	started := r.now().UTC()
	r.metricsMu.Lock()
	r.metrics.LastTickStartedAt = started
	r.metricsMu.Unlock()
	defer func() {
		if value := recover(); value != nil {
			r.recordPanic(value)
		}
		// LastTickAt is a completion timestamp. Updating it at tick start
		// makes a wedged loop look healthy to readiness checks.
		r.metricsMu.Lock()
		r.metrics.LastTickAt = r.now().UTC()
		r.metricsMu.Unlock()
	}()
	r.ticks.Add(1)
	tickCtx, cancel := context.WithTimeout(ctx, tickBudget)
	defer cancel()
	r.Tick(tickCtx)
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

	body := fmt.Sprintf(
		"连续 panic：%d\n最近类型：%s\nlast_tick：%s\nTick 已恢复，进程未退出；如持续增加请立即检查。",
		count, kind, now.Format(time.RFC3339),
	)
	message := notify.Message{Title: "🚨 学习通 Watcher panic 已恢复", Body: body, Priority: 9}
	if r.runStarted.Load() {
		select {
		case r.panicAlerts <- message:
		default:
			log.Print("activity watch: panic alert queue full; dropping alert")
		}
		return
	}
	// Tests and direct Tick callers do not start the worker, so keep the
	// synchronous path for them.
	r.sendPanicAlert(message)
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
		if classActive(now, class, r.cfg.PollBefore, r.cfg.PollAfter, r.cfg.TermStart) {
			return r.jitterInterval()
		}
	}
	next := nextClassStart(now, r.cfg.Classes, r.cfg.PollBefore, r.cfg.TermStart)
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

func nextClassStart(t time.Time, classes []Class, before time.Duration, termStart time.Time) time.Duration {
	local := t.In(shanghai)
	var best time.Time
	for _, class := range classes {
		if len(class.Schedule) == 0 {
			continue
		}
		for day := 0; day <= 7; day++ {
			dayStart := time.Date(local.Year(), local.Month(), local.Day()+day, 0, 0, 0, 0, shanghai)
			for _, schedule := range class.Schedule {
				if !scheduleDayActive(dayStart, schedule.Days) {
					continue
				}
				if !scheduleWeekActive(dayStart, schedule.Weeks, termStart) {
					continue
				}
				start, err := minuteOfDay(schedule.Start)
				if err != nil {
					continue
				}
				candidate := dayStart.Add(time.Duration(start)*time.Minute - before)
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

// queueAlert records a notification for bounded retry. It is used only after
// the upstream action has already been persisted, so retrying delivery can
// never repeat a successful sign-in or answer.
func (r *Runner) queueAlert(k string, msg notify.Message, now time.Time, cause error) {
	if k == "" {
		return
	}
	if len(r.alerts) >= 64 {
		log.Printf("activity watch: pending alert queue full; dropping %q", msg.Title)
		return
	}
	lastError := ""
	if cause != nil {
		lastError = truncateAlertError(cause.Error())
	}
	r.alerts[k] = pendingAlert{
		Title: msg.Title, Body: msg.Body, Priority: msg.Priority,
		NextAt: now.Add(alertRetryBase), LastError: lastError,
	}
	r.persistState()
}

// flushAlerts retries due notifications with exponential backoff. It is
// bounded per tick so a slow push endpoint cannot consume the sign-in budget.
func (r *Runner) flushAlerts(ctx context.Context, now time.Time) {
	if len(r.alerts) == 0 {
		return
	}
	keys := make([]string, 0, len(r.alerts))
	for k := range r.alerts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	attempted := 0
	changed := false
	flushCtx, cancel := context.WithTimeout(ctx, alertFlushBudget)
	defer cancel()
	for _, k := range keys {
		if attempted >= maxAlertsPerFlush {
			break
		}
		alert := r.alerts[k]
		if now.Before(alert.NextAt) {
			continue
		}
		attempted++
		err := r.notify(flushCtx, notify.Message{Title: alert.Title, Body: alert.Body, Priority: alert.Priority})
		if err == nil {
			delete(r.alerts, k)
			// A delivered retry counts as this activity's notification for
			// rate-limiting purposes, so the next tick does not send a
			// duplicate reminder.
			if e, ok := r.state[k]; ok {
				e.LastSent, e.Count = now, e.Count+1
				r.state[k] = e
			}
			changed = true
			log.Printf("activity watch: pending alert delivered: %s", alert.Title)
			continue
		}
		alert.Attempts++
		alert.LastError = truncateAlertError(err.Error())
		if alert.Attempts >= maxAlertAttempts {
			delete(r.alerts, k)
			log.Printf("activity watch: pending alert dropped after %d attempts: %s: %s", alert.Attempts, alert.Title, alert.LastError)
			changed = true
			continue
		}
		delay := alertRetryBase << (alert.Attempts - 1)
		if delay > alertRetryMax || delay <= 0 {
			delay = alertRetryMax
		}
		alert.NextAt = now.Add(delay)
		r.alerts[k] = alert
		changed = true
	}
	if changed {
		r.persistState()
	}
}

func truncateAlertError(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

// Tick polls only classes whose course schedule is active, attempts each new
// signable activity at most once, and falls back to alerts for manual types.
func (r *Runner) Tick(ctx context.Context) {
	r.tickMu.Lock()
	defer r.tickMu.Unlock()
	now := r.now()
	r.flushAlerts(ctx, now)
	r.signDelayLeft = maxSignDelayPerTick
	if !inWindow(now, r.cfg.Window) {
		return
	}
	active := map[string]upstream.Activity{}
	classes := map[string]Class{}
	byCourseClass := map[string]Class{}
	captured := []upstream.Activity{}
	polledClasses := 0
	pollCtx, cancelPoll := context.WithTimeout(ctx, pollBudget)
	defer cancelPoll()
	for _, class := range r.cfg.Classes {
		byCourseClass[class.CourseID+":"+class.ClassID] = class
		if !classActive(now, class, r.cfg.PollBefore, r.cfg.PollAfter, r.cfg.TermStart) {
			continue
		}
		if polledClasses > 0 && !r.wait(pollCtx, r.classPollDelay()) {
			break
		}
		polledClasses++
		r.activePolls.Add(1)
		items, err := r.source.ListClassActivities(pollCtx, class.CourseID, class.ClassID)
		if err != nil {
			log.Printf("activity watch: class %s: %s", class.ClassID, upstream.SafeFault(err).Code)
			continue
		}
		captured = append(captured, items...)
		for _, item := range items {
			if item.Signable || item.Answerable || answerReviewable(item) {
				k := key(item)
				active[k] = item
				classes[k] = class
			}
		}
	}
	// Act on the primary class feed before any evidence capture or fallback
	// group sweep. A slow page fetch or group scan must never delay a sign-in
	// that has already been discovered.
	changed := r.processActivities(ctx, active, classes, now)
	if groupSource, ok := r.source.(GroupActivitySource); ok && polledClasses > 0 {
		// Course-group history is a fallback for cards missing from the class
		// feed. Scan it on a slower cadence, and only while a watched class is
		// active, so the primary sign-in loop stays fast and predictable.
		if r.lastGroupScan.IsZero() || now.Sub(r.lastGroupScan) >= r.cfg.GroupScanInterval {
			groupCtx, cancelGroup := context.WithTimeout(ctx, groupScanBudget)
			if r.wait(groupCtx, r.classPollDelay()) {
				groupRows, err := groupSource.ListGroupActivities(groupCtx)
				if err != nil {
					log.Printf("activity watch: group feed: %s", upstream.SafeFault(err).Code)
					r.lastGroupScan = now.Add(-r.cfg.GroupScanInterval + 30*time.Second)
				} else {
					r.lastGroupScan = now
					for _, item := range groupRows {
						class, ok := byCourseClass[item.CourseID+":"+item.ClassID]
						if !ok || !classActive(now, class, r.cfg.PollBefore, r.cfg.PollAfter, r.cfg.TermStart) || !item.Signable {
							continue
						}
						k := key(item)
						if _, exists := active[k]; !exists {
							active[k] = item
							classes[k] = class
						}
					}
					changed = r.processActivities(ctx, active, classes, now) || changed
					captured = append(captured, groupRows...)
				}
			}
			cancelGroup()
		}
	}
	// Page evidence is best-effort and deliberately runs after every sign-in
	// decision, including the group fallback. A slow or oversized H5 page must
	// not delay a sign-in that the group feed discovered.
	if r.cfg.Capture {
		r.captureActivities(ctx, captured, now)
	}
	changed = r.pruneInactive(active, now) || changed
	if r.cfg.Capture && (r.lastCapturePrune.IsZero() || now.Sub(r.lastCapturePrune) >= time.Hour) {
		if err := r.cfg.CaptureStore.PruneActivityCaptures(ctx, r.cfg.CaptureAccount, now.Add(-r.cfg.CaptureRetention)); err != nil {
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

// processActivities performs the time-critical sign-in/answer decisions for
// every activity currently known to this tick. It is intentionally separate
// from pruning and persistence so it can run as soon as discovery produces
// actionable rows, before slower capture work.
func (r *Runner) processActivities(ctx context.Context, active map[string]upstream.Activity, classes map[string]Class, now time.Time) bool {
	changed := false
	for k, item := range active {
		e, seen := r.state[k]
		if answerReviewable(item) {
			if seen && (e.Count >= r.cfg.MaxRepeats || now.Sub(e.LastSent) < r.cfg.RepeatEvery) {
				continue
			}
			if _, pending := r.alerts[k]; pending {
				continue
			}
			body := formatPracticeAlertBody(item)
			message := notify.Message{Title: answerAlertTitle(item.Kind), Body: body, Priority: 6}
			if err := r.notify(ctx, message); err != nil {
				log.Printf("activity watch: notify failed: %v", err)
				r.queueAlert(k, message, now, err)
				continue
			}
			r.state[k] = entry{
				LastSent: now, Count: e.Count + 1, Status: "review", Method: "watch",
				Code: "REVIEW_REQUIRED", LastAttempt: e.LastAttempt,
				RetryAt: e.RetryAt, RetryCount: e.RetryCount,
			}
			changed = true
			log.Printf("activity watch: classroom item %s notified: %s", item.ID, item.Kind)
			continue
		}
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
			title, body := answerAlertTitle(item.Kind), formatAnswerAlertBody(item)
			if seen && e.Status == "failed" {
				title, body = "🚨 学习通自动抢答失败", formatFailureBody(item, e.Code)
			}
			if _, pending := r.alerts[k]; pending {
				continue
			}
			priority := 6
			if seen && e.Status == "failed" {
				priority = 8
			}
			message := notify.Message{Title: title, Body: body, Priority: priority}
			if err := r.notify(ctx, message); err != nil {
				log.Printf("activity watch: notify failed: %v", err)
				r.queueAlert(k, message, now, err)
				continue
			}
			r.state[k] = entry{
				LastSent: now, Count: e.Count + 1, Status: e.Status, Method: e.Method, Code: e.Code,
				LastAttempt: e.LastAttempt, RetryAt: e.RetryAt, RetryCount: e.RetryCount,
			}
			changed = true
			continue
		}
		if r.cfg.AutoSign && autoSignType(item, classes[k]) && signAttemptDue(seen, e, now) {
			scheduleKey, ambiguous := activeScheduleKeyInfo(now, classes[k], r.cfg.PollBefore, r.cfg.PollAfter, r.cfg.TermStart)
			// An overlapping schedule only blocks the automatic choice when a
			// per-schedule location binding is actually possible. A normal
			// sign-in without a location resolver must not be skipped merely
			// because two timetable rows share the same padding window.
			if !ambiguous || r.cfg.LocationResolver == nil {
				changed = r.autoSign(ctx, classes[k], item, e, scheduleKey) || changed
				continue
			}
			log.Printf("activity watch: overlapping schedules for class %s; refusing automatic location selection", item.ClassID)
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
		if _, pending := r.alerts[k]; pending {
			continue
		}
		priority := 6
		if seen && e.Status == "failed" {
			priority = 8
			if e.Count >= 1 {
				priority = 9
			}
		}
		message := notify.Message{Title: title, Body: body, Priority: priority}
		if err := r.notify(ctx, message); err != nil {
			log.Printf("activity watch: notify failed: %v", err)
			r.queueAlert(k, message, now, err)
			continue
		}
		r.state[k] = entry{
			LastSent: now, Count: e.Count + 1, Status: e.Status, Method: e.Method, Code: e.Code,
			LastAttempt: e.LastAttempt, RetryAt: e.RetryAt, RetryCount: e.RetryCount,
		}
		changed = true
		log.Printf("activity watch: alerted %s (attempt %d)", item.ID, e.Count+1)
	}
	return changed
}

func (r *Runner) pruneInactive(active map[string]upstream.Activity, now time.Time) bool {
	changed := false
	for k, e := range r.state {
		last := maxTime(e.LastSent, e.LastAttempt, e.RetryAt)
		// Drop rows that disappeared from the feed after six hours, and keep a
		// hard 24-hour ceiling for activities that somehow remain in the feed.
		// Without the second bound a permanently returned row would keep the
		// private state file growing until its write limit is reached.
		if _, still := active[k]; (!still && now.Sub(last) > 6*time.Hour) || now.Sub(last) > 24*time.Hour {
			delete(r.state, k)
			delete(r.alerts, k)
			changed = true
		}
	}
	return changed
}

// signAttemptDue centralizes automatic sign-in retry state. LastAttempt being
// present is not enough to suppress a retry: transient transport failures get
// a bounded backoff instead of a tight, repeated loop.
func signAttemptDue(seen bool, e entry, now time.Time) bool {
	if !seen {
		return true
	}
	if e.Status == "signed" {
		return false
	}
	if e.Status == "retry" {
		if e.RetryCount >= maxSignAttempts {
			return false
		}
		return e.RetryAt.IsZero() || !now.Before(e.RetryAt)
	}
	return e.LastAttempt.IsZero()
}

func signRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	case 3:
		return 5 * time.Minute
	default:
		return 10 * time.Minute
	}
}

func (r *Runner) classPollDelay() time.Duration {
	return randomDuration(r.cfg.ClassPollDelayMin, r.cfg.ClassPollDelayMax, r.random)
}

func (r *Runner) autoSignDelay() time.Duration {
	delay := randomDuration(r.cfg.AutoSignDelayMin, r.cfg.AutoSignDelayMax, r.random)
	if delay <= 0 || r.signDelayLeft <= 0 {
		return 0
	}
	if delay > r.signDelayLeft {
		delay = r.signDelayLeft
	}
	r.signDelayLeft -= delay
	return delay
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
	pageCaptured := false
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
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
		if !r.cfg.CaptureActivityPages || pageCaptured || (item.Kind != "practice" && item.Kind != "homework") || saved.HadPage {
			continue
		}
		// At most one page fetch per tick. A 1 MiB HTML page is already a
		// meaningful heap spike on the router; fan-out across one class's
		// activities would starve the next polling cycle.
		pageCaptured = true
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
		if retryableActivityError(err) {
			next.LastAttempt = time.Time{}
			next.Status = "retry"
		}
		title, body = "🚨 学习通自动抢答失败", formatFailureBody(item, safe.Code)
	} else {
		next.Status, next.Method, next.Code = "answered", result.Method, ""
	}
	priority := 6
	if err != nil {
		priority = 8
	}
	k := key(item)
	// Persist the upstream outcome before the notification call: a crash while
	// pushing must never make the next tick answer the same activity again.
	r.state[k] = next
	r.persistState()
	message := notify.Message{Title: title, Body: body, Priority: priority}
	if sendErr := r.notify(ctx, message); sendErr != nil {
		log.Printf("activity watch: notify failed: %v", sendErr)
		r.queueAlert(k, message, now, sendErr)
	} else {
		next.LastSent, next.Count = now, e.Count+1
		r.state[k] = next
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
	message := notify.Message{Title: title, Body: body, Priority: 7}
	if err := r.notify(ctx, message); err != nil {
		log.Printf("activity watch: notify failed: %v", err)
		r.queueAlert(key(item), message, now, err)
	} else {
		next.LastSent, next.Count = now, e.Count+1
	}
	r.state[key(item)] = next
	log.Printf("activity watch: classroom agent review %s: %s", item.ID, decision.Reason)
	return true
}

func (r *Runner) autoSign(ctx context.Context, class Class, item upstream.Activity, e entry, scheduleKey string) bool {
	if delay := r.autoSignDelay(); delay > 0 && !r.wait(ctx, delay) {
		return false
	}
	actionCtx, cancel := context.WithTimeout(ctx, signActionTimeout)
	defer cancel()
	req := upstream.SignRequest{CourseID: item.CourseID, ClassID: item.ClassID, ActivityID: item.ID}
	if r.cfg.LocationResolver != nil {
		loc, resolveErr := r.cfg.LocationResolver.Resolve(actionCtx, item.CourseID, item.ClassID, scheduleKey)
		if resolveErr != nil {
			now := r.now()
			next := entry{
				LastAttempt: now, RetryAt: e.RetryAt, RetryCount: e.RetryCount,
				Count: e.Count, LastSent: e.LastSent, Status: "failed", Code: "LOCATION_RESOLVE_FAILED",
			}
			r.state[key(item)] = next
			r.persistState()
			message := notify.Message{Title: "🚨 学习通位置解析失败", Body: formatFailureBody(item, "LOCATION_RESOLVE_FAILED"), Priority: 8}
			if sendErr := r.notify(ctx, message); sendErr != nil {
				log.Printf("activity watch: notify failed: %v", sendErr)
				r.queueAlert(key(item), message, now, sendErr)
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
	result, err := r.signer.SignActivity(actionCtx, req)
	now := r.now()
	k := key(item)
	next := entry{
		LastAttempt: now, RetryAt: e.RetryAt, RetryCount: e.RetryCount,
		Count: e.Count, LastSent: e.LastSent, Status: e.Status, Method: e.Method, Code: e.Code,
	}
	title, body := "✅ 学习通自动签到成功", formatSuccessBody(item, result)
	if err != nil {
		safe := upstream.SafeFault(err)
		next.Status, next.Code = "failed", safe.Code
		if retryableActivityError(err) {
			next.RetryCount = e.RetryCount + 1
			if next.RetryCount < maxSignAttempts {
				next.Status = "retry"
				next.RetryAt = now.Add(signRetryDelay(next.RetryCount))
			} else {
				next.RetryAt = time.Time{}
			}
		}
		title, body = "🚨 学习通自动签到失败", formatFailureBody(item, safe.Code)
		if result.Address != "" {
			body += "\n地址：" + result.Address
		}
		if result.EgressIP != "" {
			body += "\n出口 IP：" + result.EgressIP
		}
	} else {
		next.Status, next.Method, next.Code = "signed", result.Method, ""
		next.RetryAt, next.RetryCount = time.Time{}, 0
	}
	// Persist the verified sign-in (or retry decision) before the notification
	// call. A notification timeout must never make the next tick repeat a
	// successful upstream sign-in.
	r.state[k] = next
	r.persistState()
	priority := 6
	if err != nil {
		priority = 8
	}
	message := notify.Message{Title: title, Body: body, Priority: priority}
	if sendErr := r.notify(ctx, message); sendErr != nil {
		log.Printf("activity watch: notify failed: %v", sendErr)
		r.queueAlert(k, message, now, sendErr)
	} else {
		next.LastSent, next.Count = now, e.Count+1
		r.state[k] = next
		r.persistState()
	}
	log.Printf("activity watch: auto sign %s: status=%s", item.ID, next.Status)
	return true
}

func (r *Runner) persistState() {
	if err := r.save(); err != nil {
		log.Printf("activity watch: state write failed: %v", err)
	}
}

func (r *Runner) notify(ctx context.Context, message notify.Message) error {
	notifyCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return r.sender.Send(notifyCtx, message)
}

func retryableActivityError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch upstream.SafeFault(err).Code {
	case "UPSTREAM_UNAVAILABLE", "RATE_LIMITED", "AUTH_COOLDOWN":
		return true
	default:
		return false
	}
}

func answerReviewable(item upstream.Activity) bool {
	if item.State != "active" || item.UserState != "unsigned" {
		return false
	}
	return item.Kind == "practice" || item.Kind == "homework"
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

func activeScheduleKey(t time.Time, class Class, before, after time.Duration, termStart time.Time) string {
	key, _ := activeScheduleKeyInfo(t, class, before, after, termStart)
	return key
}

func activeScheduleKeyInfo(t time.Time, class Class, before, after time.Duration, termStart time.Time) (string, bool) {
	keys := map[string]struct{}{}
	for _, schedule := range class.Schedule {
		if scheduleActiveAt(t, schedule, before, after, termStart) {
			keys[schedule.EffectiveKey()] = struct{}{}
		}
	}
	if len(keys) == 1 {
		for key := range keys {
			return key, false
		}
	}
	return "", len(keys) > 1
}

func classActive(t time.Time, class Class, before, after time.Duration, termStart time.Time) bool {
	if len(class.Schedule) == 0 {
		return true
	}
	for _, schedule := range class.Schedule {
		if scheduleActiveAt(t, schedule, before, after, termStart) {
			return true
		}
	}
	return false
}

func scheduleActiveAt(t time.Time, schedule Schedule, before, after time.Duration, termStart time.Time) bool {
	if _, _, ok := scheduleIntervalAt(t, schedule, before, after, termStart); ok {
		return true
	}
	return false
}

// scheduleIntervalAt evaluates a schedule against the local Beijing day. It
// checks adjacent dates as well so pre-class padding and overnight sessions
// that cross midnight remain visible.
func scheduleIntervalAt(t time.Time, schedule Schedule, before, after time.Duration, termStart time.Time) (time.Time, time.Time, bool) {
	local := t.In(shanghai)
	day := localDate(local)
	for offset := -1; offset <= 1; offset++ {
		startDay := day.AddDate(0, 0, offset)
		if !scheduleDayActive(startDay, schedule.Days) {
			continue
		}
		if !scheduleWeekActive(startDay, schedule.Weeks, termStart) {
			continue
		}
		start, end, ok := scheduleBounds(startDay, schedule)
		if !ok {
			continue
		}
		activeStart, activeEnd := start.Add(-before), end.Add(after)
		if !t.Before(activeStart) && t.Before(activeEnd) {
			return start, end, true
		}
	}
	return time.Time{}, time.Time{}, false
}

func scheduleBounds(dayStart time.Time, schedule Schedule) (time.Time, time.Time, bool) {
	startMinute, startErr := minuteOfDay(schedule.Start)
	endMinute, endErr := minuteOfDay(schedule.End)
	if startErr != nil || endErr != nil || startMinute == endMinute {
		return time.Time{}, time.Time{}, false
	}
	start := dayStart.Add(time.Duration(startMinute) * time.Minute)
	end := dayStart.Add(time.Duration(endMinute) * time.Minute)
	if endMinute < startMinute {
		end = end.Add(24 * time.Hour)
	}
	return start, end, true
}

func localDate(t time.Time) time.Time {
	local := t.In(shanghai)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, shanghai)
}

// teachingWeek returns the one-based teaching week, or zero before the term.
// Both values are interpreted as Beijing calendar dates.
func teachingWeek(now, termStart time.Time) int {
	if termStart.IsZero() {
		return 0
	}
	start := localDate(termStart.In(shanghai))
	current := localDate(now.In(shanghai))
	days := int(current.Sub(start).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days/7 + 1
}

func scheduleWeekActive(now time.Time, weeks string, termStart time.Time) bool {
	normalized, err := NormalizeWeeks(weeks)
	if err != nil {
		return false
	}
	if normalized == "" {
		return true
	}
	week := teachingWeek(now, termStart)
	if week < 1 {
		return false
	}
	for _, part := range strings.Split(normalized, ",") {
		bounds := strings.Split(part, "-")
		start, _ := strconv.Atoi(bounds[0])
		end := start
		if len(bounds) == 2 {
			end, _ = strconv.Atoi(bounds[1])
		}
		if week >= start && week <= end {
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

func formatAnswerAlertBody(a upstream.Activity) string {
	lines := []string{
		"课程：" + firstNonBlank(a.Course, a.CourseID),
		"活动：" + firstNonBlank(a.Title, a.ID),
		"类型：课堂答题",
		"状态：答题进行中，尚未作答",
		"系统未自动抢答，请打开学习通或控制台处理。",
	}
	if a.StartsAt != nil {
		lines = append(lines, "开始："+a.StartsAt.In(shanghai).Format("15:04"))
	}
	return strings.Join(lines, "\n")
}

func formatPracticeAlertBody(a upstream.Activity) string {
	kind := "随堂练习"
	if a.Kind == "homework" {
		kind = "课堂作业"
	}
	lines := []string{
		"课程：" + firstNonBlank(a.Course, a.CourseID),
		"活动：" + firstNonBlank(a.Title, a.ID),
		"类型：" + kind,
		"状态：题目已发布，尚未提交",
		"系统未自动提交，请打开控制台课堂活动页查看题面与页面证据。",
	}
	if a.StartsAt != nil {
		lines = append(lines, "开始："+a.StartsAt.In(shanghai).Format("15:04"))
	}
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
	if result.Address != "" {
		lines = append(lines, "地址："+result.Address)
	}
	if result.EgressIP != "" {
		lines = append(lines, "出口 IP："+result.EgressIP)
	}
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
	if len(b) > maxWatchStateBytes {
		return r.recoverState(errors.New("watch state exceeds size limit"))
	}
	var s stateFile
	if json.Unmarshal(b, &s) != nil || s.Entries == nil || (s.Version != 1 && s.Version != 2) {
		return r.recoverState(errors.New("watch state is invalid or unsupported"))
	}
	if s.Version == 1 {
		for k, e := range s.Entries {
			e.LastAttempt = time.Time{}
			s.Entries[k] = e
		}
	}
	r.state = s.Entries
	r.alerts = s.Alerts
	if r.alerts == nil {
		r.alerts = map[string]pendingAlert{}
	}
	return nil
}

func (r *Runner) recoverState(cause error) error {
	backup := r.cfg.StatePath + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(r.cfg.StatePath, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("watch state is unusable and could not be preserved: %w", cause)
	}
	log.Printf("activity watch: state set aside at %s: %v", backup, cause)
	r.state = map[string]entry{}
	r.metrics.StateDegraded = true
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
	alerts := make(map[string]pendingAlert, len(r.alerts))
	for k, a := range r.alerts {
		alerts[k] = a
	}
	if len(alerts) == 0 {
		alerts = nil
	}
	b, err := json.Marshal(stateFile{Version: 2, Entries: entries, Alerts: alerts})
	if err != nil {
		return err
	}
	if len(b) > maxWatchStateBytes {
		// Pending alert bodies are the only unbounded, reconstructable part of
		// the state. Dropping them is safe; letting the file cross the load
		// limit would quarantine the whole file on the next restart and lose
		// sign-in suppression for every activity.
		r.alerts = map[string]pendingAlert{}
		b, err = json.Marshal(stateFile{Version: 2, Entries: entries})
		if err != nil {
			return err
		}
		if len(b) > maxWatchStateBytes {
			return errors.New("watch state exceeds size limit")
		}
	}
	return persist.Write(r.cfg.StatePath, b, 0o600)
}

// Window is one contiguous interval in which a class is considered active,
// including the configured lead time. It exists so operators can compare the
// configured schedule with the real timetable without reading runner state.
type Window struct {
	CourseID    string    `json:"course_id"`
	ClassID     string    `json:"class_id"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	ScheduleKey string    `json:"schedule_key,omitempty"`
	Weeks       string    `json:"weeks,omitempty"`
	Room        string    `json:"room,omitempty"`
}

// Windows projects the active intervals for every class between from and
// until using the same predicate as the polling loop. Minutes are the smallest
// granularity the schedule format supports.
func Windows(classes []Class, before, after time.Duration, termStart, from, until time.Time) []Window {
	from, until = from.In(shanghai).Truncate(time.Minute), until.In(shanghai).Truncate(time.Minute)
	if until.Before(from) {
		return nil
	}
	out := []Window{}
	for _, class := range classes {
		if len(class.Schedule) == 0 {
			if !until.Before(from) {
				out = append(out, Window{CourseID: class.CourseID, ClassID: class.ClassID, Start: from, End: until})
			}
			continue
		}
		firstDay := localDate(from).AddDate(0, 0, -1)
		lastDay := localDate(until).AddDate(0, 0, 1)
		for day := firstDay; !day.After(lastDay); day = day.AddDate(0, 0, 1) {
			for _, schedule := range class.Schedule {
				if !scheduleDayActive(day, schedule.Days) || !scheduleWeekActive(day, schedule.Weeks, termStart) {
					continue
				}
				start, end, ok := scheduleBounds(day, schedule)
				if !ok {
					continue
				}
				start, end = start.Add(-before), end.Add(after)
				if start.Before(from) {
					start = from
				}
				if end.After(until) {
					end = until
				}
				if !end.After(start) {
					continue
				}
				out = append(out, Window{
					CourseID: class.CourseID, ClassID: class.ClassID,
					Start: start, End: end,
					ScheduleKey: schedule.EffectiveKey(), Weeks: schedule.Weeks, Room: schedule.Room,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start.Equal(out[j].Start) {
			return out[i].CourseID+out[i].ClassID < out[j].CourseID+out[j].ClassID
		}
		return out[i].Start.Before(out[j].Start)
	})
	merged := make([]Window, 0, len(out))
	for _, window := range out {
		if len(merged) == 0 {
			merged = append(merged, window)
			continue
		}
		last := &merged[len(merged)-1]
		if last.CourseID == window.CourseID && last.ClassID == window.ClassID &&
			last.ScheduleKey == window.ScheduleKey && !window.Start.After(last.End) {
			if window.End.After(last.End) {
				last.End = window.End
			}
			continue
		}
		merged = append(merged, window)
	}
	return merged
}
