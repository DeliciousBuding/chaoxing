package upstream

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountsProgressIndependentlyAndCapabilitiesBypassQueue(t *testing.T) {
	a, b := testClient(t), testClient(t)
	entered, release := make(chan struct{}), make(chan struct{})
	a.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return response(r, `{"result":1,"channelList":[]}`), nil
	})
	b.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		return response(r, `{"result":1,"channelList":[]}`), nil
	})
	done := make(chan error, 1)
	go func() { _, err := a.Call(context.Background(), "list_courses", nil); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := b.Call(ctx, "list_courses", nil); err != nil {
		t.Error(err)
	}
	if _, err := a.Call(ctx, "get_capabilities", nil); err != nil {
		t.Error(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSameAccountBurstReusesCache(t *testing.T) {
	c := testClient(t)
	var calls atomic.Int32
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(r, `{"result":1,"channelList":[]}`), nil
	})
	done := make(chan error, 8)
	for range 8 {
		go func() { _, err := c.Call(context.Background(), "list_courses", nil); done <- err }()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls=%d", calls.Load())
	}
}
