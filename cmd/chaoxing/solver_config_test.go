package main

import (
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
