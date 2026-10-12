package upstream

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"
)

func TestKnownProgressRoutesRejectedBeforeFetch(t *testing.T) {
	for _, s := range []string{"https://mooc1.chaoxing.com/mooc-ans/mycourse/studentstudyAjax", "https://mooc1.chaoxing.com/ananas/job/document", "https://fystat-ans.chaoxing.com/log/setlog", "https://mooc1.chaoxing.com/mooc-ans/knowledge/cards"} {
		u, _ := url.Parse(s)
		if safeURL(u) {
			t.Fatal(s)
		}
	}
}

func TestCallWaitingForSessionLockHonorsBudget(t *testing.T) {
	c := testClient(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, e := c.Call(ctx, "list_courses", nil)
	var f *Fault
	if !errors.As(e, &f) || f.Code != "UPSTREAM_UNAVAILABLE" {
		t.Fatal(e)
	}
	if time.Since(started) > time.Second {
		t.Fatal("lock ignored context")
	}
}
func TestSafeFaultDoesNotExposeMessages(t *testing.T) {
	for _, e := range []error{errors.New("synthetic-secret"), &Fault{Code: "AUTH_REQUIRED", Message: "synthetic-secret"}, &Fault{Code: "untrusted-code", Message: "synthetic-secret"}} {
		f := SafeFault(e)
		if f.Message == "synthetic-secret" || f.Code == "untrusted-code" {
			t.Fatal(f)
		}
	}
}
