package watch

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/notify"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

func (r *Runner) verificationDelay(now time.Time, delay time.Duration) time.Duration {
	r.tickMu.Lock()
	defer r.tickMu.Unlock()
	delay = r.notificationDelay(now, delay)
	if !r.cfg.AutoSign {
		return delay
	}
	for _, e := range r.state {
		if e.Status == "verify" && e.PendingActivity != nil && e.PendingResult != nil {
			delay = min(delay, max(time.Second, e.RetryAt.Sub(now)))
		}
	}
	return delay
}

// Only persisted accepted attempts are eligible here. Discovery windows and
// feed disappearance cannot cancel their read-only confirmation; a receipt
// never authorizes a second submission.
func (r *Runner) resumeVerifications(ctx context.Context, now time.Time) bool {
	verifier, ok := r.signer.(SignVerifier)
	if !r.cfg.AutoSign || !ok {
		return false
	}
	keys := []string{}
	for k, e := range r.state {
		if e.Status == "verify" && e.PendingActivity != nil && e.PendingResult != nil && !now.Before(e.RetryAt) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	changed := false
	for _, k := range keys[:min(len(keys), 2)] {
		e := r.state[k]
		item := *e.PendingActivity
		var proof upstream.SignResult
		var err error
		if k != key(item) || item.Kind != "signin" || e.PendingResult.ActivityID != item.ID || e.PendingResult.CourseID != item.CourseID || e.PendingResult.ClassID != item.ClassID {
			err = &upstream.Fault{Code: "STATE_INVALID"}
		} else if now.Sub(e.PendingResult.FetchedAt) >= 24*time.Hour {
			err = &upstream.Fault{Code: "SIGN_REJECTED"}
		} else {
			verifyCtx, cancel := context.WithTimeout(ctx, signActionTimeout)
			proof, err = verifier.VerifySignActivity(verifyCtx, upstream.SignRequest{CourseID: item.CourseID, ClassID: item.ClassID, ActivityID: item.ID})
			cancel()
		}
		if err == nil && (!proof.Verified || proof.ActivityID != item.ID || proof.CourseID != item.CourseID || proof.ClassID != item.ClassID || proof.StateSource != "attendance") {
			err = &upstream.Fault{Code: "SIGN_VERIFICATION_PENDING"}
		}
		e.LastAttempt = r.now()
		if err != nil {
			f := upstream.SafeFault(err)
			e.Code = f.Code
			if !cooldownActivityCode(f.Code) {
				e.RetryCount++
			}
			if (retryableActivityError(err) || f.Code == "SIGN_VERIFICATION_PENDING") && e.RetryCount < maxSignAttempts {
				e.RetryAt = e.LastAttempt.Add(max(signRetryDelay(max(e.RetryCount, 1)), time.Duration(f.RetryAfterSeconds)*time.Second))
				r.state[k] = e
				r.persistState()
				log.Printf("activity watch: attendance verification activity=%s status=waiting code=%s retry_count=%d", item.ID, e.Code, e.RetryCount)
				changed = true
				continue
			}
		}
		result := *e.PendingResult
		message := notify.Message{Priority: 8, Title: "🚨 学习通考勤未确认", Body: formatFailureBody(item, e.Code)}
		if err == nil {
			result.Verified, result.StateSource, result.FetchedAt = true, "attendance", proof.FetchedAt
			e.Status, e.AttendanceVerified, e.Code = "signed", true, ""
			message = notify.Message{Priority: 6, Title: "✅ 学习通自动签到成功", Body: formatSuccessBody(item, result)}
			if !result.Submitted || result.State == "already_signed" {
				message.Title = "✅ 学习通已签（非本次提交）"
			}
		} else {
			e.Status = "failed"
		}
		e.PendingActivity, e.PendingResult, e.RetryAt = nil, nil, time.Time{}
		delete(r.alerts, k)
		r.state[k] = e
		r.persistState()
		if sendErr := r.notify(ctx, message); sendErr != nil {
			r.queueAlert(k, message, e.LastAttempt, sendErr)
		} else {
			e.LastSent, e.Count = e.LastAttempt, e.Count+1
			r.state[k] = e
		}
		r.persistState()
		log.Printf("activity watch: attendance verification activity=%s status=%s code=%s submitted=%t verified=%t", item.ID, e.Status, e.Code, e.Submitted, e.AttendanceVerified)
		changed = true
	}
	return changed
}
