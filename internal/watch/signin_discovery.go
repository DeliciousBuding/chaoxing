package watch

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

const discoveryPrefix = "discovery:"

// discoveryFailuresBeforeAlert is how many consecutive class-feed failures are
// tolerated before the operator is warned. A single transient hiccup (for
// example a 20-second upstream timeout) recovers on the next tick and must not
// ring the phone; a sustained outage still warns while there is time to act.
const discoveryFailuresBeforeAlert = 2

// Discovery receipts are separate from action results: signed, finished,
// unknown and unsupported cards must not disappear behind auto-sign filters.
func (r *Runner) rememberSignIns(items []upstream.Activity, class Class, now time.Time) {
	for _, item := range items {
		if item.Kind != "signin" || item.ID == "" || item.CourseID != class.CourseID || item.ClassID != class.ClassID {
			continue
		}
		k := key(item)
		e := r.state[k]
		if e.LastObserved.IsZero() || now.Sub(e.LastObserved) >= time.Hour {
			e.LastObserved = now
		}
		if item.Signable && item.State == "active" {
			known := item
			// These fields may contain signed query material. A resumed action
			// uses fresh authoritative preSign, never a persisted signed URL.
			known.RawURL, known.Ext = "", ""
			e.KnownActivity, e.KnownObservedAt = &known, now
			if schedule, ambiguous := activeScheduleKeyInfo(now, class, r.cfg.PollBefore, r.cfg.PollAfter, r.cfg.TermStart); !ambiguous {
				e.KnownScheduleKey = schedule
			}
		} else {
			e.KnownActivity = nil
		}
		r.state[k] = e
		alertKey := discoveryPrefix + k
		if _, pending := r.alerts[alertKey]; e.DiscoveryNotified || pending {
			continue
		}
		priority := 6
		if item.State == "active" {
			priority = 9
		}
		body := fmt.Sprintf("课程：%s\n活动：%s\n活动状态：%s\n列表考勤状态：%s（提示，非独立考勤证明）\n类型：%s\n可自动处理：%t", firstNonBlank(item.Course, item.CourseID), firstNonBlank(item.Title, item.ID), item.State, item.UserState, item.SignType, r.cfg.AutoSign && item.Signable && autoSignType(item, class))
		if item.EndsAt != nil {
			body += "\n截止：" + item.EndsAt.In(shanghai).Format("01-02 15:04:05")
		}
		if item.State == "finished" {
			body += "\n这是已结束的历史签到，不会补交。"
		} else {
			body += "\n该提醒只表示已发现；实际提交与考勤结果以独立通知为准。"
		}
		r.queueAlert(alertKey, notify.Message{Title: "🔔 学习通签到发现提醒", Body: body, Priority: priority}, now, nil)
		if alert, ok := r.alerts[alertKey]; ok {
			alert.NextAt = now
			r.alerts[alertKey] = alert
		}
		log.Printf("activity watch: signin discovered activity=%s state=%s user_state=%s signable=%t", item.ID, item.State, item.UserState, item.Signable)
	}
	r.persistState()
}

// A discovered action keeps its own deadline and retry receipt. A later list
// failure or the end of the timetable window must not erase that work.
func (r *Runner) resumeSignIns(ctx context.Context, now time.Time) {
	if !r.cfg.AutoSign {
		return
	}
	classes := map[string]Class{}
	for _, class := range r.cfg.Classes {
		classes[class.CourseID+":"+class.ClassID] = class
	}
	keys := []string{}
	for k, e := range r.state {
		if e.KnownActivity != nil && (signAttemptDue(true, e, now) || (e.Status == "retry" && e.KnownActivity.EndsAt != nil && !now.Before(*e.KnownActivity.EndsAt))) {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := r.state[keys[i]].KnownActivity, r.state[keys[j]].KnownActivity
		if a.EndsAt != nil && b.EndsAt != nil && !a.EndsAt.Equal(*b.EndsAt) {
			return a.EndsAt.Before(*b.EndsAt)
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys[:min(len(keys), 2)] {
		e := r.state[k]
		item := *e.KnownActivity
		class, ok := classes[item.CourseID+":"+item.ClassID]
		if !ok || k != key(item) || !autoSignType(item, class) {
			continue
		}
		if item.EndsAt != nil && !now.Before(*item.EndsAt) {
			r.closeSignWindow(item, e, "SIGN_WINDOW_ENDED")
			continue
		}
		if now.Sub(e.KnownObservedAt) >= 24*time.Hour || (item.EndsAt == nil && now.Sub(e.KnownObservedAt) >= 10*time.Minute) {
			e.KnownActivity = nil
			r.state[k] = e
			r.persistState()
			continue
		}
		schedule, ambiguous := activeScheduleKeyInfo(now, class, r.cfg.PollBefore, r.cfg.PollAfter, r.cfg.TermStart)
		if e.KnownScheduleKey != "" {
			schedule, ambiguous = e.KnownScheduleKey, false
		}
		if ambiguous && r.cfg.LocationResolver != nil {
			continue
		}
		r.autoSign(ctx, class, item, e, schedule)
	}
}

func (r *Runner) closeSignWindow(item upstream.Activity, e entry, code string) bool {
	now := r.now()
	e.Status, e.Code, e.RetryAt, e.KnownActivity = "expired", code, time.Time{}, nil
	e.LastAttempt = now
	r.state[key(item)] = e
	r.persistState()
	r.queueAlert(key(item), notify.Message{Title: "🚨 学习通签到窗口已结束提醒", Body: formatFailureBody(item, code), Priority: r.windowPriority(now, 9, 5)}, now, nil)
	if alert, ok := r.alerts[key(item)]; ok {
		alert.NextAt = now
		r.alerts[key(item)] = alert
	}
	return true
}

// Warn only when a class feed has failed discovery for more than one
// consecutive tick, so a transient timeout does not raise a false alarm. One
// receipt per failure episode avoids repeating the warning every tick, and a
// recovery message closes the episode once the feed is healthy again.
//
// In-class failures raise priority 9 because a live sign-in could be missed.
// Off-schedule idle-scan failures stay quieter (priority 5), and a class that
// is already in a local rate-limit cooldown skips repeat idle probes until the
// cooldown expires so a daily per-class quota never floods alerts or retries.
func (r *Runner) reportDiscoveryFailure(class Class, now time.Time, err error, inClass bool) {
	k := "feed:activities:" + class.CourseID + ":" + class.ClassID
	if err == nil {
		prev, had := r.state[k]
		delete(r.state, k)
		delete(r.alerts, k)
		if had && prev.Count >= discoveryFailuresBeforeAlert {
			recoveryKey := k + ":recovered"
			r.queueAlert(recoveryKey, notify.Message{
				Title: "✅ 学习通数据源已恢复",
				Body:  fmt.Sprintf("班级：%s\n活动发现已恢复正常轮询，之前的发现异常已解除。", class.ClassID), Priority: 6,
			}, now, nil)
			if alert, ok := r.alerts[recoveryKey]; ok {
				alert.NextAt = now
				r.alerts[recoveryKey] = alert
			}
		}
		return
	}
	f := upstream.SafeFault(err)
	e := r.state[k]
	e.Status, e.Code, e.LastObserved = "feed_degraded", f.Code, now
	if f.Code == "UPSTREAM_RATE_LIMITED" && f.RetryAfterSeconds > 0 {
		e.RetryAt = now.Add(time.Duration(f.RetryAfterSeconds) * time.Second)
	} else {
		e.RetryAt = time.Time{}
	}
	e.Count++
	r.state[k] = e
	if e.Count != discoveryFailuresBeforeAlert {
		// Below the threshold: keep counting silently. Above it: the episode
		// was already warned, so stay quiet until it recovers.
		r.persistState()
		return
	}
	priority := 9
	title := "🚨 学习通签到发现异常提醒"
	body := fmt.Sprintf("班级：%s\n活动发现连续失败：%s\n新签到可能暂时无法自动发现，系统会持续重试；如持续请打开学习通核对。", class.ClassID, f.Code)
	if !inClass {
		priority = 5
		title = "⚠️ 学习通课外巡检异常"
		body = fmt.Sprintf("班级：%s\n课外巡检连续失败：%s\n当前不在正课时间窗，系统会在冷却后重试，不影响其他班级。", class.ClassID, f.Code)
	}
	if f.RetryAfterSeconds > 0 {
		body += fmt.Sprintf("\n下一次允许探测：%s\n等待来源：%s", now.Add(time.Duration(f.RetryAfterSeconds)*time.Second).In(shanghai).Format("15:04:05"), firstNonBlank(f.RetryAfterSource, "unspecified"))
	}
	r.queueAlert(k, notify.Message{Title: title, Body: body, Priority: priority}, now, nil)
	if alert, ok := r.alerts[k]; ok {
		alert.NextAt = now
		r.alerts[k] = alert
	}
	r.persistState()
}

// idlePollCooling reports whether a class is already known to be in an active
// upstream rate-limit cooldown so the off-schedule idle sweep can skip probing
// it until the cooldown expires.
func (r *Runner) idlePollCooling(class Class, now time.Time) bool {
	k := "feed:activities:" + class.CourseID + ":" + class.ClassID
	e, ok := r.state[k]
	return ok && e.Code == "UPSTREAM_RATE_LIMITED" && now.Before(e.RetryAt)
}

// Notification work shares the existing bounded outbox, not a new scheduler.
func (r *Runner) notificationDelay(now time.Time, delay time.Duration) time.Duration {
	// Maintenance wakeups must not turn the configured class polling cadence
	// into a faster loop. In class the normal <=66s tick services this outbox.
	if inWindow(now, r.cfg.Window) {
		for _, class := range r.cfg.Classes {
			if classActive(now, class, r.cfg.PollBefore, r.cfg.PollAfter, r.cfg.TermStart) {
				return delay
			}
		}
	}
	for _, alert := range r.alerts {
		delay = min(delay, max(time.Second, alert.NextAt.Sub(now)))
	}
	if r.cfg.AutoSign {
		for _, e := range r.state {
			if e.KnownActivity != nil && e.Status == "retry" && !e.RetryAt.IsZero() {
				due := e.RetryAt
				if e.KnownActivity.EndsAt != nil && e.KnownActivity.EndsAt.Before(due) {
					due = *e.KnownActivity.EndsAt
				}
				delay = min(delay, max(time.Second, due.Sub(now)))
			}
		}
	}
	return delay
}
