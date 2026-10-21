package main

import "testing"

func TestSignOperationsRequireAuditedDaemon(t *testing.T) {
	for _, tool := range []string{"sign_activity", "sign_activity_with_face"} {
		if !requiresAuditedDaemon(tool) {
			t.Fatalf("%s bypasses daemon audit", tool)
		}
	}
	for _, tool := range []string{"list_courses", "get_sign_captcha", "answer_sign_captcha"} {
		if requiresAuditedDaemon(tool) {
			t.Fatalf("%s should remain available through query", tool)
		}
	}
}
