// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

// Gate wave-2 fix6 pins (PR #950, #798 ADR-094):
//
//   - SF-1 (silent-failure-hunter): a planted-cookie detection must be
//     visible SERVER-SIDE. The guard now slog.Warns and emits a best-effort
//     audit entry (auth.planted_cookie_detected) when the wired audit logger
//     is present. Cookie NAMES only and the REDACTED path; never cookie
//     values, never the raw Cookie header.
//
//   - TDA-1 (type-design-analyzer): the planted_cookie_cleared discriminant
//     is a duplicated Go<->TS literal with no contract binding (the value
//     appears nowhere in contracts/, ErrorResponse.code is a free string).
//     A one-side rename compiles clean everywhere and silently kills the
//     SPA's recovery toast. This tripwire reads src/lib/api/http.ts and
//     fails when the TS literal drifts from the Go constant.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// newFix6AuditLogger builds a temp-dir audit logger for one test and wires
// Close to run before the caller reads the JSONL back.
func newFix6AuditLogger(t *testing.T) (*audit.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	return logger, dir
}

// readFix6AuditEntries closes nothing and reads every audit*.jsonl entry in dir.
func readFix6AuditEntries(t *testing.T, dir string) []audit.Entry {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "audit*.jsonl"))
	if err != nil {
		t.Fatalf("glob audit dir: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no audit*.jsonl files in %s — the check could not have seen an entry (instrument failure, not a pass)", dir)
	}
	var entries []audit.Entry
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", m, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var e audit.Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("unmarshal audit entry %q: %v", line, err)
			}
			entries = append(entries, e)
		}
	}
	return entries
}

// runPlantedGuard drives the guard once with a duplicated omnipus-session
// cookie (marker value) on the given method+path and returns status + whether
// the inner handler ran. The request carries TWO pairs of the reserved name —
// what plantedReservedDuplicates detects.
func runPlantedGuard(t *testing.T, guardMW func(http.Handler) http.Handler, method, target string) (int, bool) {
	t.Helper()
	innerReached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerReached = true
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(guardMW(inner))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(method, srv.URL+target, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Cookie", "omnipus-session=SECRETVAL; omnipus-session=SECRETVAL2")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, innerReached
}

// TestPreviewFix6_PlantedCookieGuard_AuditsDetection pins SF-1: the detection
// is recorded — Warn-visible AND audited — with names + redacted path only.
func TestPreviewFix6_PlantedCookieGuard_AuditsDetection(t *testing.T) {
	logger, dir := newFix6AuditLogger(t)
	guard := PlantedCookieGuard(WithPlantedCookieAuditLog(logger))

	// State-changing: 403 from the guard, inner never runs.
	status, innerReached := runPlantedGuard(t, guard, http.MethodPost, "/api/v1/agents")
	if status != http.StatusForbidden || innerReached {
		t.Fatalf("POST: status=%d innerReached=%v, want 403/false", status, innerReached)
	}

	// Safe method: passes through (marked failed downstream), still audited.
	status, innerReached = runPlantedGuard(t, guard, http.MethodGet, "/preview/agent/tok123/index.html")
	if status != http.StatusOK || !innerReached {
		t.Fatalf("GET: status=%d innerReached=%v, want 200/true (safe method passes through)", status, innerReached)
	}

	entries := readFix6AuditEntries(t, dir)
	if len(entries) != 2 {
		t.Fatalf("audit entries = %d, want 2 (one per detection)", len(entries))
	}
	for _, e := range entries {
		if e.Event != "auth.planted_cookie_detected" {
			t.Fatalf("event = %q, want auth.planted_cookie_detected", e.Event)
		}
		if e.Decision != audit.DecisionDeny {
			t.Fatalf("decision = %q, want deny", e.Decision)
		}
		names, ok := e.Details["names"].([]any)
		if !ok || len(names) != 1 || names[0] != "omnipus-session" {
			t.Fatalf("details.names = %#v, want [omnipus-session]", e.Details["names"])
		}
		gotPath, _ := e.Details["path"].(string)
		if gotPath == "" {
			t.Fatalf("details.path missing: %#v", e.Details)
		}
		switch e.Details["method"] {
		case http.MethodPost:
			// /api/v1/agents carries no credential in its path — passthrough.
			if gotPath != "/api/v1/agents" {
				t.Fatalf("POST details.path = %q, want /api/v1/agents", gotPath)
			}
		case http.MethodGet:
			// Preview path: the token segment redacted, triage segments kept.
			if strings.Contains(gotPath, "tok123") {
				t.Fatalf("GET details.path %q leaks the preview token — FR-026 redaction broken", gotPath)
			}
			if gotPath != "/preview/agent/<redacted>/index.html" {
				t.Fatalf("GET details.path = %q, want /preview/agent/<redacted>/index.html", gotPath)
			}
		default:
			t.Fatalf("unexpected method in details: %#v", e.Details["method"])
		}
		if e.Details["state_changing"] == nil {
			t.Fatalf("details.state_changing missing: %#v", e.Details)
		}
	}
}

// TestPreviewFix6_PlantedCookieGuard_NoCookieValuesInAudit pins the value
// ban: the marker values SECRETVAL/SECRETVAL2 must appear NOWHERE in the
// audit file (names and path only, never cookie values).
func TestPreviewFix6_PlantedCookieGuard_NoCookieValuesInAudit(t *testing.T) {
	logger, dir := newFix6AuditLogger(t)
	guard := PlantedCookieGuard(WithPlantedCookieAuditLog(logger))
	status, _ := runPlantedGuard(t, guard, http.MethodPost, "/api/v1/agents")
	if status != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", status)
	}
	entries := readFix6AuditEntries(t, dir)
	if len(entries) == 0 {
		t.Fatal("no audit entries — detection not recorded")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "audit*.jsonl"))
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		for _, marker := range []string{"SECRETVAL", "SECRETVAL2", "omnipus-session="} {
			if strings.Contains(string(b), marker) {
				t.Fatalf("audit file %s contains cookie value marker %q", m, marker)
			}
		}
	}
}

// TestPreviewFix6_PlantedCookieCode_TS_DriftTripwire is the TDA-1 tripwire:
// the Go discriminant and the TS literal must never drift. Fail-closed — an
// unreadable TS file fails the test, never silently passes it.
func TestPreviewFix6_PlantedCookieCode_TS_DriftTripwire(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed — cannot locate the TS source")
	}
	// thisFile = .../pkg/gateway/middleware/planted_cookie_fix6_test.go
	tsPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "src", "lib", "api", "http.ts")
	b, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("cannot read %s (fail-closed: the tripwire must not silently pass): %v", tsPath, err)
	}
	re := regexp.MustCompile(`PLANTED_COOKIE_CODE\s*=\s*['"]([^'"]+)['"]`)
	m := re.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("PLANTED_COOKIE_CODE literal not found in %s — the TS constant was renamed or moved; update this tripwire", tsPath)
	}
	if m[1] != plantedCookieClearedCode {
		t.Fatalf("drift: Go plantedCookieClearedCode = %q but src/lib/api/http.ts PLANTED_COOKIE_CODE = %q — the SPA toast keys on err.code and would silently never fire", plantedCookieClearedCode, m[1])
	}
}
