package watch

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

const assignmentScanInterval = 5 * time.Minute

type assignmentSeen struct {
	State    string `json:"state"`
	Notified bool   `json:"notified"`
}

type assignmentBaseline struct {
	Items map[string]assignmentSeen `json:"items"`
}

// FeedMetrics contains timing and counts only; course IDs stay private.
type FeedMetrics struct {
	Attempts            uint64    `json:"attempts"`
	Failures            uint64    `json:"failures"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastAttemptAt       time.Time `json:"last_attempt_at"`
	LastSuccessAt       time.Time `json:"last_success_at"`
}

func (r *Runner) recordFeed(key string, now time.Time, success bool) {
	r.metricsMu.Lock()
	defer r.metricsMu.Unlock()
	v := r.feeds[key]
	v.Attempts++
	v.LastAttemptAt = now.UTC()
	if success {
		v.ConsecutiveFailures = 0
		v.LastSuccessAt = now.UTC()
	} else {
		v.Failures++
		v.ConsecutiveFailures++
	}
	r.feeds[key] = v
}

func (r *Runner) resetInactiveFeedFailures(now time.Time) {
	r.metricsMu.Lock()
	defer r.metricsMu.Unlock()
	for _, class := range r.cfg.Classes {
		if !classActive(now, class, r.cfg.PollBefore, r.cfg.PollAfter, r.cfg.TermStart) {
			key := "activities:" + class.CourseID + ":" + class.ClassID
			v := r.feeds[key]
			v.ConsecutiveFailures = 0
			if v.Attempts > 0 {
				r.feeds[key] = v
			}
		}
	}
}

func (r *Runner) pollAssignments(ctx context.Context, now time.Time) bool {
	source, ok := r.source.(AssignmentSource)
	if !ok {
		return false
	}
	changed := false
	var selected *Class
	var oldest time.Time
	for i := range r.cfg.Classes {
		class := &r.cfg.Classes[i]
		if r.classroomActive(*class, now) {
			changed = r.pollClassAssignments(ctx, source, *class, now) || changed
			continue
		}
		last := r.lastAssignments[class.CourseID+":"+class.ClassID]
		if !last.IsZero() && now.Sub(last) < assignmentScanInterval {
			continue
		}
		if selected == nil || last.Before(oldest) {
			selected, oldest = class, last
		}
	}
	if selected == nil {
		return changed
	}
	return r.pollClassAssignments(ctx, source, *selected, now) || changed
}

func (r *Runner) pollClassAssignments(ctx context.Context, source AssignmentSource, class Class, now time.Time) bool {
	selected := &class
	key := selected.CourseID + ":" + selected.ClassID
	r.lastAssignments[key] = now
	scanCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	items, complete, err := source.ListClassAssignments(scanCtx, selected.CourseID, selected.ClassID)
	status := "ok"
	if err != nil {
		status = upstream.SafeFault(err).Code
	} else if !complete {
		status = "partial"
	}
	log.Printf("assignment watch: class_poll class=%s started_at=%s completed_at=%s status=%s items=%d", class.ClassID, now.UTC().Format(time.RFC3339Nano), r.now().UTC().Format(time.RFC3339Nano), status, len(items))
	valid := err == nil && complete && len(items) <= 1000
	ids := map[string]bool{}
	for _, item := range items {
		if item.ID == "" || ids[item.ID] || item.CourseID != selected.CourseID || item.ClassID != selected.ClassID {
			valid = false
		}
		ids[item.ID] = true
	}
	if previous := r.assignments[selected.CourseID+":"+selected.ClassID]; previous != nil {
		for id := range previous.Items {
			ids[id] = true
		}
		valid = valid && len(ids) <= 1000
	}
	r.recordFeed("assignments:"+key, now, valid)
	if !valid {
		log.Printf("assignment watch: incomplete or failed class scan")
		return false
	}
	previous := r.assignments[key]
	initial := previous == nil
	if initial {
		previous = &assignmentBaseline{Items: map[string]assignmentSeen{}}
	}
	if previous.Items == nil {
		previous.Items = map[string]assignmentSeen{}
	}
	next := &assignmentBaseline{Items: map[string]assignmentSeen{}}
	// Keep accepted IDs when a complete feed temporarily omits old work.
	// The bounded history prevents a later reappearance from ringing twice.
	for id, seen := range previous.Items {
		next.Items[id] = seen
	}
	var pending []upstream.Assignment
	for _, item := range items {
		old := previous.Items[item.ID]
		state := assignmentSeen{State: item.SubmissionState, Notified: old.Notified}
		if initial && item.SubmissionState == "pending" || item.SubmissionState == "submitted" {
			state.Notified = true
		}
		if !initial && item.SubmissionState == "pending" && item.AvailabilityState != "closed" && item.AvailabilityState != "not_open" && !state.Notified {
			pending = append(pending, item)
		}
		next.Items[item.ID] = state
	}
	r.assignments[key] = next
	// Save the baseline before any send. A crash on first discovery must not
	// turn all historical pending work into a notification burst.
	if err := r.save(); err != nil {
		if initial {
			delete(r.assignments, key)
		} else {
			r.assignments[key] = previous
		}
		log.Printf("assignment watch: baseline save failed")
		r.metricsMu.Lock()
		r.metrics.StateDegraded = true
		r.metricsMu.Unlock()
		return false
	}
	if len(pending) == 0 {
		return true
	}
	lines := []string{"课程：" + firstNonBlank(pending[0].Course, selected.CourseID), "发现新的未提交作业："}
	for i, item := range pending {
		if i == 10 {
			lines = append(lines, fmt.Sprintf("另有 %d 项，请查看作业列表。", len(pending)-10))
			break
		}
		lines = append(lines, "• "+item.Title)
	}
	lines = append(lines, "系统未自动提交，请打开学习通或控制台作业页处理。")
	priority := r.classroomPriority(class, now, 6)
	if err := r.notify(ctx, notify.Message{Title: "📝 学习通作业提醒", Body: strings.Join(lines, "\n"), Priority: priority}); err != nil {
		log.Printf("assignment watch: notify failed")
		return true // Pending IDs stay retryable on the next bounded scan.
	}
	for _, item := range pending {
		v := next.Items[item.ID]
		v.Notified = true
		next.Items[item.ID] = v
	}
	return true
}
