// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_timeout_adr084_test.go — ADR-084 D9, JUDGE-FR-049 / FR-050: the
// judge turn timeout's default/hard-ceiling clamp (EffectiveTimeoutSec) and
// its ADDITIONAL clamp against the goal/plan judge-round bound it runs
// inside ("a per-turn bound above the per-round bound can never fire"),
// applied loudly at config load (applyJudgeTimeoutRoundClamp).
package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestJudgeTimeout_EffectiveDefaultAndCeiling is JUDGE-FR-049: unset becomes
// the 420 s default, and anything above the 900 s hard ceiling is clamped
// down to it — the exact values ADR-084's own text fixes.
func TestJudgeTimeout_EffectiveDefaultAndCeiling(t *testing.T) {
	cases := []struct {
		name      string
		configure int
		want      int
	}{
		{"unset defaults to 420s", 0, 420},
		{"negative treated as unset", -1, 420},
		{"an in-range value is honoured exactly", 500, 500},
		{"exactly the ceiling is untouched", 900, 900},
		{"above the ceiling is clamped down to it", 1200, 900},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jc := JudgeConfig{TimeoutSec: tc.configure}
			if got := jc.EffectiveTimeoutSec(); got != tc.want {
				t.Fatalf("EffectiveTimeoutSec() with TimeoutSec=%d = %d, want %d", tc.configure, got, tc.want)
			}
		})
	}
}

// TestJudgeTimeout_RoundClampMirrorsGoalJudgeRoundTimeout pins the mirrored
// constant's value. pkg/config cannot import pkg/agent (pkg/agent already
// imports pkg/config — importing back would be a cycle), so
// judgeTimeoutRoundCeilingSec is a SEPARATE constant kept in step with
// pkg/agent/goal_loop.go's goalJudgeRoundTimeout (itself
// pkg/agent/plan_engine.go's planJudgeRoundTimeout, 10 minutes) by
// convention, not by the compiler. If this test ever needs to change, the
// pkg/agent constant changed first and this one must follow — never the
// reverse.
func TestJudgeTimeout_RoundClampMirrorsGoalJudgeRoundTimeout(t *testing.T) {
	const tenMinutesInSeconds = 600
	if judgeTimeoutRoundCeilingSec != tenMinutesInSeconds {
		t.Fatalf("judgeTimeoutRoundCeilingSec = %d, want %d (10 minutes) to mirror pkg/agent/goal_loop.go's goalJudgeRoundTimeout",
			judgeTimeoutRoundCeilingSec, tenMinutesInSeconds)
	}
}

// TestJudgeTimeout_RoundClamp_AppliesWithWarn is JUDGE-FR-050 itself: a
// configured judge timeout above the goal/plan judge-round bound it runs
// inside is clamped down to that bound, LOUDLY — this project's established
// convention (ClampLeaseWait, lease_wait_clamp.go) is clamp + WARN over
// reject, and a silent clamp is the ADR-037 anti-pattern this project bans
// (an operator who configured 700s and sees nothing believes it took
// effect).
func TestJudgeTimeout_RoundClamp_AppliesWithWarn(t *testing.T) {
	logs := captureWarnings(t)

	cfg := &Config{Judge: JudgeConfig{TimeoutSec: 700}}
	applyJudgeTimeoutRoundClamp(cfg)

	if cfg.Judge.TimeoutSec != judgeTimeoutRoundCeilingSec {
		t.Fatalf("judge.timeout_sec=700 (above the %ds round ceiling) resolved to %d, want %d",
			judgeTimeoutRoundCeilingSec, cfg.Judge.TimeoutSec, judgeTimeoutRoundCeilingSec)
	}
	out := logs.String()
	for _, needle := range []string{"timeout_sec", "700", "600"} {
		if !strings.Contains(out, needle) {
			t.Errorf("the round clamp WARN does not contain %q — it must name both the configured and the applied value, or an operator cannot tell what changed.\nCaptured log:\n%s", needle, out)
		}
	}
}

// TestJudgeTimeout_RoundClamp_SilentWhenWithinBound is the negative
// companion TestJudgeTimeout_RoundClamp_AppliesWithWarn needs: a value
// already at or under the round ceiling must be left untouched AND must not
// warn — a clamp that fires on every default install is not a clamp, it
// is a second default nobody asked for, and a WARN that fires when nothing
// changed is how a real WARN stops being read.
func TestJudgeTimeout_RoundClamp_SilentWhenWithinBound(t *testing.T) {
	logs := captureWarnings(t)

	cfg := &Config{Judge: JudgeConfig{TimeoutSec: 300}}
	applyJudgeTimeoutRoundClamp(cfg)

	if cfg.Judge.TimeoutSec != 300 {
		t.Fatalf("judge.timeout_sec=300 (well under the round ceiling) was mutated to %d, want 300 unchanged", cfg.Judge.TimeoutSec)
	}
	if strings.Contains(logs.String(), "judge.timeout_sec is configured above") {
		t.Fatalf("the round clamp warned about a value it did not change.\nCaptured log:\n%s", logs.String())
	}
}

// TestJudgeTimeout_LoadConfigAppliesRoundClamp proves FR-050 fires exactly
// where the requirement says it must: "Config load MUST reject (or clamp
// with a WARN)" — not merely as a standalone function nobody calls. A
// config.json written with judge.timeout_sec above the round ceiling must
// come back from LoadConfig already clamped.
func TestJudgeTimeout_LoadConfigAppliesRoundClamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	cfg.Judge.TimeoutSec = 700
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Judge.TimeoutSec != judgeTimeoutRoundCeilingSec {
		t.Fatalf("LoadConfig with judge.timeout_sec=700 on disk returned Judge.TimeoutSec=%d, want %d (JUDGE-FR-050 must fire at config load)",
			loaded.Judge.TimeoutSec, judgeTimeoutRoundCeilingSec)
	}
}

// TestControlIdleReleaseSec_EffectiveDefaultAndDisableSentinel is
// BROWSER-FR-031a's config-field half (wave E2's other deliverable in this
// file's package, landed in the same commit as the judge timeout keys per
// the ADR-084/085/086 joint delivery plan §5's config.go row): unset
// defaults to 900s, and — since a plain Go int cannot tell an omitted key
// apart from an explicit 0 — a NEGATIVE value is this accessor's "disable
// expiry" sentinel rather than the spec's literal "0 disables" wording. See
// ControlIdleReleaseSec's own doc comment in config.go for why.
func TestControlIdleReleaseSec_EffectiveDefaultAndDisableSentinel(t *testing.T) {
	cases := []struct {
		name      string
		configure int
		want      int
	}{
		{"unset defaults to 900s", 0, 900},
		{"an in-range value is honoured exactly, no ceiling", 60, 60},
		{"a value well above the default is honoured exactly, no ceiling", 3600, 3600},
		{"negative is the disable-expiry sentinel, resolved to 0", -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bc := BrowserToolConfig{ControlIdleReleaseSec: tc.configure}
			if got := bc.EffectiveControlIdleReleaseSec(); got != tc.want {
				t.Fatalf("EffectiveControlIdleReleaseSec() with ControlIdleReleaseSec=%d = %d, want %d", tc.configure, got, tc.want)
			}
		})
	}
}
