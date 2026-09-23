// Omnipus — run_doctor tool tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests exist because run_doctor is the tool an operator trusts to say
// "you are secure" — so each check must be proven to REPORT a problem when
// that problem exists, not merely to run. For every check the unhealthy state
// is constructed in a t.TempDir() and the specific finding asserted; the
// healthy state is then asserted to produce no finding.

package systools_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// doctorIssue mirrors one entry of run_doctor's issues array.
type doctorIssue struct {
	Severity       string `json:"severity"`
	Message        string `json:"message"`
	Recommendation string `json:"recommendation"`
}

// doctorReport mirrors the JSON run_doctor returns in ToolResult.ForLLM.
// Asserting against this typed shape (not log output) keeps the tests tied
// to the tool's real result contract.
type doctorReport struct {
	ChecksFailedPct int           `json:"checks_failed_pct"`
	Issues          []doctorIssue `json:"issues"`
	ChecksPassed    int           `json:"checks_passed"`
	ChecksFailed    int           `json:"checks_failed"`
	RunAt           string        `json:"run_at"`
}

// doctorSetup describes the install state a doctor test runs against. Mode 0
// means the file is absent; the audit flag toggles ~/.omnipus/system/.
type doctorSetup struct {
	enableProxy bool
	credMode    fs.FileMode
	configMode  fs.FileMode
	auditDir    bool
}

// healthyDoctorSetup is an install with nothing to report: exec proxy on,
// both sensitive files 0600, audit directory present. The exec-allowlist
// check itself is retired (ADR-091 D2/D5) — there is no allowedBinaries
// field left to set here.
func healthyDoctorSetup() doctorSetup {
	return doctorSetup{
		enableProxy: true,
		credMode:    0o600,
		configMode:  0o600,
		auditDir:    true,
	}
}

// newDoctorDeps materialises the described install under a fresh t.TempDir()
// Home and returns the Deps run_doctor reads (Home, ConfigPath, GetCfg).
// File modes are set with an explicit chmod so results do not depend on the
// process umask.
func newDoctorDeps(t *testing.T, s doctorSetup) *systools.Deps {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Tools.Exec.EnableProxy = s.enableProxy

	home := t.TempDir()
	writeFixedMode := func(name string, mode fs.FileMode) {
		t.Helper()
		p := filepath.Join(home, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatalf("chmod %s to %v: %v", name, mode, err)
		}
	}
	if s.credMode != 0 {
		writeFixedMode("credentials.json", s.credMode)
	}
	if s.configMode != 0 {
		writeFixedMode("config.json", s.configMode)
	}
	if s.auditDir {
		if err := os.Mkdir(filepath.Join(home, "system"), 0o700); err != nil {
			t.Fatalf("seed audit dir: %v", err)
		}
	}
	return &systools.Deps{
		Home:       home,
		ConfigPath: filepath.Join(home, "config.json"),
		GetCfg:     func() *config.Config { return cfg },
	}
}

// runDoctor executes the tool and decodes its report. The tool has no error
// result path by design: findings always ride in the issues array of a
// success result, so every call asserts a non-error ToolResult whose ForLLM
// parses as the report shape.
func runDoctor(t *testing.T, deps *systools.Deps) doctorReport {
	t.Helper()
	res := systools.NewDoctorRunTool(deps).Execute(context.Background(), map[string]any{})
	if res == nil {
		t.Fatal("run_doctor returned a nil result")
	}
	if res.IsError {
		t.Fatalf("run_doctor returned an error result; findings belong in issues: %s", res.ForLLM)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(res.ForLLM), &rep); err != nil {
		t.Fatalf("run_doctor result is not the report JSON: %v\nbody: %s", err, res.ForLLM)
	}
	return rep
}

// TestDoctorRun_HealthyInstallReportsNothing is the complement of every
// finding test: with nothing wrong, no issue may appear. A healthy-path-only
// test would pass against a broken doctor — it is meaningful only alongside
// the unhealthy-state tests below.
func TestDoctorRun_HealthyInstallReportsNothing(t *testing.T) {
	rep := runDoctor(t, newDoctorDeps(t, healthyDoctorSetup()))
	if len(rep.Issues) != 0 {
		t.Fatalf("healthy install produced issues: %+v", rep.Issues)
	}
	if rep.ChecksFailed != 0 {
		t.Errorf("checks_failed = %d, want 0", rep.ChecksFailed)
	}
	if rep.ChecksFailedPct != 0 {
		t.Errorf("checks_failed_pct = %d, want 0", rep.ChecksFailedPct)
	}
	// 3 pass counters: credentials, config, audit dir. A healthy exec-egress
	// check adds no pass counter (the loop only counts warnings), so a fully
	// healthy install reports 3, not 4.
	if rep.ChecksPassed != 3 {
		t.Errorf("checks_passed = %d, want 3 (credentials, config, audit dir)", rep.ChecksPassed)
	}
	if rep.RunAt == "" {
		t.Error("run_at is empty; the report must carry a timestamp")
	}
}

// TestDoctorRun_AllChecksFailTogether constructs the worst install — proxy
// off, both sensitive files world-readable, audit directory missing — and
// asserts every one of the four findings appears with its severity and a
// recommendation, plus the aggregate counters. (The exec-allowlist/SEC-05
// finding is retired alongside the allowlist itself, ADR-091 D2/D5 — down
// from five findings to four.)
func TestDoctorRun_AllChecksFailTogether(t *testing.T) {
	s := doctorSetup{enableProxy: false, credMode: 0o644, configMode: 0o644, auditDir: false}
	rep := runDoctor(t, newDoctorDeps(t, s))

	if len(rep.Issues) != 4 {
		t.Fatalf("got %d issues, want 4: %+v", len(rep.Issues), rep.Issues)
	}
	want := []struct {
		message     string // distinctive substring of the finding
		severity    string
		recContains string
	}{
		{"exec HTTP proxy", "high", "SEC-29"},
		{"credentials.json is world/group-readable", "high", "chmod 600 ~/.omnipus/credentials.json"},
		{"config.json is world/group-readable", "medium", "chmod 600 ~/.omnipus/config.json"},
		{"Audit log directory", "medium", "Restart Omnipus"},
	}
	for _, w := range want {
		iss := issueWithMessage(rep.Issues, w.message)
		if iss == nil {
			t.Errorf("no issue reports %q; issues: %+v", w.message, rep.Issues)
			continue
		}
		if iss.Severity != w.severity {
			t.Errorf("finding %q has severity %q, want %q", w.message, iss.Severity, w.severity)
		}
		if !strings.Contains(iss.Recommendation, w.recContains) {
			t.Errorf("finding %q recommends %q, want it to mention %q", w.message, iss.Recommendation, w.recContains)
		}
	}
	if rep.ChecksFailed != 4 {
		t.Errorf("checks_failed = %d, want 4", rep.ChecksFailed)
	}
	if rep.ChecksPassed != 0 {
		t.Errorf("checks_passed = %d, want 0", rep.ChecksPassed)
	}
	if rep.ChecksFailedPct != 100 {
		t.Errorf("checks_failed_pct = %d, want 100", rep.ChecksFailedPct)
	}
}

// TestDoctorRun_SingleFindingIsolation breaks ONE thing at a time on an
// otherwise healthy install. Each case asserts the specific finding appears
// AND that nothing else does — proving each check reports exactly the
// problem that exists, and that a healthy check stays quiet while a
// neighbouring one fires.
//
// The expected pass/pct values differ by case because the exec-egress check
// counts only when it FAILS (a healthy run adds no pass counter): an exec
// failure yields 1 failed + 3 passed = 4 counted checks (25%), while a file
// or audit-dir failure yields 1 failed + 2 passed = 3 counted checks (33%).
func TestDoctorRun_SingleFindingIsolation(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(*doctorSetup)
		message     string
		severity    string
		recContains string
		wantPassed  int
		wantPct     int
	}{
		{
			name:        "exec proxy disabled",
			mutate:      func(s *doctorSetup) { s.enableProxy = false },
			message:     "exec HTTP proxy",
			severity:    "high",
			recContains: "SEC-29",
			wantPassed:  3,
			wantPct:     25,
		},
		{
			name:        "credentials world-readable 0644",
			mutate:      func(s *doctorSetup) { s.credMode = 0o644 },
			message:     "credentials.json is world/group-readable",
			severity:    "high",
			recContains: "chmod 600 ~/.omnipus/credentials.json",
			wantPassed:  2,
			wantPct:     33,
		},
		{
			name:        "credentials group-readable 0640",
			mutate:      func(s *doctorSetup) { s.credMode = 0o640 },
			message:     "credentials.json is world/group-readable",
			severity:    "high",
			recContains: "chmod 600 ~/.omnipus/credentials.json",
			wantPassed:  2,
			wantPct:     33,
		},
		{
			name:        "config world-readable 0644",
			mutate:      func(s *doctorSetup) { s.configMode = 0o644 },
			message:     "config.json is world/group-readable",
			severity:    "medium",
			recContains: "chmod 600 ~/.omnipus/config.json",
			wantPassed:  2,
			wantPct:     33,
		},
		{
			name:        "audit directory missing",
			mutate:      func(s *doctorSetup) { s.auditDir = false },
			message:     "Audit log directory",
			severity:    "medium",
			recContains: "Restart Omnipus",
			wantPassed:  2,
			wantPct:     33,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := healthyDoctorSetup()
			tc.mutate(&s)
			rep := runDoctor(t, newDoctorDeps(t, s))

			if len(rep.Issues) != 1 {
				t.Fatalf("expected exactly the %q finding, got %d issues: %+v",
					tc.message, len(rep.Issues), rep.Issues)
			}
			iss := rep.Issues[0]
			if !strings.Contains(iss.Message, tc.message) {
				t.Errorf("issue message %q does not report %q", iss.Message, tc.message)
			}
			if iss.Severity != tc.severity {
				t.Errorf("severity = %q, want %q", iss.Severity, tc.severity)
			}
			if !strings.Contains(iss.Recommendation, tc.recContains) {
				t.Errorf("recommendation %q does not mention %q", iss.Recommendation, tc.recContains)
			}
			if rep.ChecksFailed != 1 || rep.ChecksPassed != tc.wantPassed {
				t.Errorf("counts failed=%d passed=%d, want failed=1 passed=%d",
					rep.ChecksFailed, rep.ChecksPassed, tc.wantPassed)
			}
			if rep.ChecksFailedPct != tc.wantPct {
				t.Errorf("checks_failed_pct = %d, want %d", rep.ChecksFailedPct, tc.wantPct)
			}
		})
	}
}

// TestDoctorRun_AbsentFilesSkippedNotFailed pins the deliberate semantics for
// files that do not exist: a fresh install before onboarding has no
// credentials.json/config.json, and absence is neither a pass nor a finding.
// Only the audit-directory existence check counts here.
func TestDoctorRun_AbsentFilesSkippedNotFailed(t *testing.T) {
	s := healthyDoctorSetup()
	s.credMode = 0
	s.configMode = 0
	rep := runDoctor(t, newDoctorDeps(t, s))

	if len(rep.Issues) != 0 {
		t.Fatalf("absent files must not be reported as findings: %+v", rep.Issues)
	}
	if rep.ChecksFailed != 0 {
		t.Errorf("checks_failed = %d, want 0", rep.ChecksFailed)
	}
	if rep.ChecksPassed != 1 {
		t.Errorf("checks_passed = %d, want 1 (only the audit-dir check counts)", rep.ChecksPassed)
	}
	if rep.ChecksFailedPct != 0 {
		t.Errorf("checks_failed_pct = %d, want 0", rep.ChecksFailedPct)
	}
}

// issueWithMessage returns the first issue whose Message contains substr, or
// nil when none matches.
func issueWithMessage(issues []doctorIssue, substr string) *doctorIssue {
	for i := range issues {
		if strings.Contains(issues[i].Message, substr) {
			return &issues[i]
		}
	}
	return nil
}
