package main

import (
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
	"net/http"
	"testing"
)

func TestDaemonHTTPFailuresAreClassified(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
	}{
		{429, "", "RATE_LIMITED"}, {401, "", "AUTH_REQUIRED"},
		{403, "<html>denied</html>", "AUTH_REQUIRED"},
		{502, "private diagnostic", "UPSTREAM_UNAVAILABLE"},
		{429, `{"ok":false,"error":{"code":"RATE_LIMITED","message":"private"}}`, "RATE_LIMITED"},
		{http.StatusOK, "invalid", "INTERNAL"},
	} {
		e := emitDaemonResponse("list_courses", tc.status, []byte(tc.body))
		if e == nil || upstream.SafeFault(e).Code != tc.code {
			t.Fatalf("status=%d fault=%+v", tc.status, upstream.SafeFault(e))
		}
	}
}
