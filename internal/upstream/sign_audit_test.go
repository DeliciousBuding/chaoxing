package upstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/signaudit"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type auditStore struct {
	records []signaudit.Record
}

func (s *auditStore) SaveSignAudit(_ context.Context, record signaudit.Record) error {
	s.records = append(s.records, record)
	return nil
}

func TestRecordSignAuditCapturesEgressAndSubmittedLocation(t *testing.T) {
	store := &auditStore{}
	client := &Client{
		config: Config{SignAudit: store, EgressAccountKey: "root"},
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != egressProbeURL {
				return nil, fmt.Errorf("unexpected probe URL %s", req.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ip=203.0.113.7\n")),
				Request:    req,
			}, nil
		})},
	}
	lat, lng := 31.2304, 121.4737
	result := SignResult{Method: "location", State: "success", Verified: true}
	client.recordSignAudit(
		SignRequest{CourseID: "course-1", ClassID: "class-1", ActivityID: "activity-1"},
		&result,
		nil,
		&signAuditTrace{Address: "synthetic address", Latitude: &lat, Longitude: &lng, Submitted: true},
	)
	if len(store.records) != 1 {
		t.Fatalf("records=%d", len(store.records))
	}
	got := store.records[0]
	if got.EgressIP != "203.0.113.7" || got.EgressSource != "cloudflare-trace" ||
		got.Address != "synthetic address" || got.Latitude == nil || *got.Latitude != lat ||
		got.Longitude == nil || *got.Longitude != lng || !got.Verified {
		t.Fatalf("unexpected audit: %+v", got)
	}
	if result.EgressIP != "203.0.113.7" || result.EgressSource != "cloudflare-trace" {
		t.Fatalf("sign result did not expose the observed egress: %+v", result)
	}
}

func TestRecordSignAuditKeepsFailureCode(t *testing.T) {
	store := &auditStore{}
	client := &Client{
		config: Config{SignAudit: store, EgressAccountKey: "root"},
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("synthetic probe failure")
		})},
	}
	result := SignResult{}
	client.recordSignAudit(
		SignRequest{CourseID: "course-1", ClassID: "class-1", ActivityID: "activity-1", Address: "fallback address"},
		&result,
		&Fault{Code: "SIGN_REJECTED", Message: "synthetic"},
		nil,
	)
	if len(store.records) != 1 {
		t.Fatalf("records=%d", len(store.records))
	}
	got := store.records[0]
	if got.State != "failed" || got.Code != "SIGN_REJECTED" || got.Address != "" || got.EgressSource != "not_submitted" || got.Method != "none" {
		t.Fatalf("unexpected audit: %+v", got)
	}
	if got.CreatedAt.IsZero() || time.Since(got.CreatedAt) > time.Minute {
		t.Fatalf("unexpected timestamp: %v", got.CreatedAt)
	}
}
