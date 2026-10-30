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
	e.Status, e.Code, e.RetryAt, e.KnownActivity = "expired", code, time.Time{}, nil
	e.LastAttempt = r.now()
	r.state[key(item)] = e
	r.persistState()
	r.queueAlert(key(item), notify.Message{Title: "🚨 学习通签到窗口已结束提醒", Body: formatFailureBody(item, code), Priority: 9}, r.now(), nil)
	if alert, ok := r.alerts[key(item)]; ok {
		alert.NextAt = r.now()
		r.alerts[key(item)] = alert
	}
	return true
}

// Alert on the first failed discovery, not only after readiness has degraded.
// One receipt per failure episode avoids repeating the warning every tick.
func (r *Runner) reportDiscoveryFailure(class Class, now time.Time, err error) {
	k := "feed:activities:" + class.CourseID + ":" + class.ClassID
	if err == nil {
		delete(r.state, k)
		delete(r.alerts, k)
		return
	}
	if _, warned := r.state[k]; warned {
		return
	}
	f := upstream.SafeFault(err)
	r.state[k] = entry{Status: "feed_degraded", Code: f.Code, LastObserved: now}
	body := fmt.Sprintf("班级：%s\n活动发现失败：%s\n新签到可能暂时无法自动发现，请立即打开学习通检查。", class.ClassID, f.Code)
	if f.RetryAfterSeconds > 0 {
		body += fmt.Sprintf("\n下一次允许探测：%s\n等待来源：%s", now.Add(time.Duration(f.RetryAfterSeconds)*time.Second).In(shanghai).Format("15:04:05"), firstNonBlank(f.RetryAfterSource, "unspecified"))
	}
	r.queueAlert(k, notify.Message{Title: "🚨 学习通签到发现异常提醒", Body: body, Priority: 9}, now, nil)
	if alert, ok := r.alerts[k]; ok {
		alert.NextAt = now
		r.alerts[k] = alert
	}
	r.persistState()
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
