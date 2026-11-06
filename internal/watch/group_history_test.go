package watch

import (
	"context"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

func TestGroupHistoryCannotReactivateFinishedClassCard(t *testing.T) {
	item := signIn("c", "k", "old-card", false)
	item.State, item.Source = "finished", "class"
	group := item
	group.State, group.Signable, group.Source = "active", true, "group"
	group.EndsAt = nil
	signer := &fakeSigner{}
	src := &fakeGroupSource{
		fakeSource: fakeSource{items: []upstream.Activity{item}},
		groupItems: []upstream.Activity{group},
	}
	r, err := New(Config{
		Interval: time.Minute, AutoSign: true,
		Classes: []Class{{CourseID: "c", ClassID: "k"}}, StatePath: t.TempDir() + "/state.json",
	}, src, &fakeSender{}, signer)
	if err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background())
	if src.groupCalls != 1 || len(signer.requests) != 0 {
		t.Fatalf("groupCalls=%d sign requests=%d", src.groupCalls, len(signer.requests))
	}
	if e := r.state[key(item)]; e.KnownActivity != nil || !e.DiscoveryNotified {
		t.Fatalf("historical discovery overwritten: %+v", e)
	}
}
