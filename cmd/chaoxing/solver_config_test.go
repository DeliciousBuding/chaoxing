package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/captchasolver"
)

func TestCaptchaSolverConfig(t *testing.T) {
	var nilCfg *captchaSolverConfig
	if solver, err := nilCfg.solver(); err != nil || solver != nil {
		t.Fatalf("solver=%v err=%v", solver, err)
	}
	cfg := &captchaSolverConfig{Provider: "damagou", UserKey: "synthetic-user_key", TimeoutSeconds: 7}
	solver, err := cfg.solver()
	if err != nil {
		t.Fatal(err)
	}
	damagou, ok := solver.(*captchasolver.DamaGou)
	if !ok || damagou.UserKey != "synthetic-user_key" || damagou.Type != 1004 || damagou.Client.Timeout.Seconds() != 7 {
		t.Fatalf("solver=%+v", solver)
	}
	bad := &captchaSolverConfig{Provider: "unknown"}
	if _, err = bad.solver(); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("err=%v", err)
	}
}
func TestCaptchaSolverUserKeySources(t *testing.T) {
	direct := &captchaSolverConfig{Provider: "damagou", UserKey: "synthetic-user_key"}
	if key, err := direct.resolveUserKey(); err != nil || key != "synthetic-user_key" {
		t.Fatalf("key=%q err=%v", key, err)
	}

	path := filepath.Join(t.TempDir(), "damagou.key")
	if err := os.WriteFile(path, []byte(" file-key \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile := &captchaSolverConfig{Provider: "damagou", UserKeyFile: path}
	if key, err := fromFile.resolveUserKey(); err != nil || key != "file-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}

	t.Setenv("CHAOXING_TEST_DAMAGOU_KEY", "env-key")
	fromEnv := &captchaSolverConfig{Provider: "damagou", UserKeyEnv: "CHAOXING_TEST_DAMAGOU_KEY"}
	if key, err := fromEnv.resolveUserKey(); err != nil || key != "env-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}

	ambiguous := &captchaSolverConfig{Provider: "damagou", UserKey: "a", UserKeyEnv: "B"}
	if _, err := ambiguous.resolveUserKey(); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("err=%v", err)
	}
}
