// T2.12 + T2.13: Health endpoint audit-degraded field tests.
//
// T2.12: When auditLoggerAvailableFn returns false, /health must report
//   audit_logger == "unavailable" and audit_degraded == true.
//
// T2.13: When the audit logger is present (fn returns true) but
//   audit.SnapshotSkipped().Total > 0, /health must report audit_degraded == true.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/health"
)

// TestHealth_FlipsAuditDegraded_OnLoggerUnavailable (T2.12) boots a
// health.Server with auditLoggerAvailableFn = func() bool { return false },
// hits /health, and verifies:
//   - audit_logger == "unavailable"
//   - audit_degraded == true
func TestHealth_FlipsAuditDegraded_OnLoggerUnavailable(t *testing.T) {
	s := health.NewServer("127.0.0.1", 0)
	s.SetAuditLoggerAvailableFunc(func() bool { return false })
	// Operator configured audit (sandbox.audit_log=true) but the logger is
	// broken — this IS degraded. The deliberately-off case is covered by
	// the separate TestHealth_AuditNotDegraded_WhenDeliberatelyDisabled test.
	s.SetAuditLoggerConfiguredFunc(func() bool { return true })

	// Use RegisterOnMux so we can hit /health on an httptest server.
	mux := http.NewServeMux()
	s.RegisterOnMux(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close() // errcheck rationale (out of errcheck scope; kept as documentation): test HTTP response body close; inconsequential once the body has been read

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health response: %v", err)
	}

	auditLogger, _ := body["audit_logger"].(string)
	if auditLogger != "unavailable" {
		t.Errorf("audit_logger = %q; want %q (T2.12)", auditLogger, "unavailable")
	}

	auditDegraded, _ := body["audit_degraded"].(bool)
	if !auditDegraded {
		t.Errorf("audit_degraded = %v; want true when logger unavailable (T2.12)", auditDegraded)
	}
}

// TestHealth_FlipsAuditDegraded_OnSkipCount (T2.13) sets the audit logger
// as available (fn returns true) but increments the skipped counter so
// audit.SnapshotSkipped().Total > 0. Verifies /health reports audit_degraded == true.
func TestHealth_FlipsAuditDegraded_OnSkipCount(t *testing.T) {
	// Reset the global counter before and after this test so it does not
	// pollute other tests sharing the same process.
	audit.ResetSkippedForTest()
	t.Cleanup(audit.ResetSkippedForTest)

	// Bump the counter.
	audit.IncSkipped("serve_web", audit.DecisionDeny)

	s := health.NewServer("127.0.0.1", 0)
	// Logger IS available — degraded must come from the skip count alone.
	s.SetAuditLoggerAvailableFunc(func() bool { return true })

	mux := http.NewServeMux()
	s.RegisterOnMux(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close() // errcheck rationale (out of errcheck scope; kept as documentation): test HTTP response body close; inconsequential once the body has been read

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health response: %v", err)
	}

	auditLogger, _ := body["audit_logger"].(string)
	if auditLogger != "ok" {
		t.Errorf("audit_logger = %q; want %q (T2.13: logger is available)", auditLogger, "ok")
	}

	auditDegraded, _ := body["audit_degraded"].(bool)
	if !auditDegraded {
		t.Errorf("audit_degraded = %v; want true when skip count > 0 (T2.13)", auditDegraded)
	}
}

// TestHealth_NotDegraded_WhenLoggerOkAndNoSkips (T2.12/T2.13 sanity) confirms
// that audit_degraded is false when the logger is available AND skip count is 0.
func TestHealth_NotDegraded_WhenLoggerOkAndNoSkips(t *testing.T) {
	audit.ResetSkippedForTest()
	t.Cleanup(audit.ResetSkippedForTest)

	s := health.NewServer("127.0.0.1", 0)
	s.SetAuditLoggerAvailableFunc(func() bool { return true })

	mux := http.NewServeMux()
	s.RegisterOnMux(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close() // errcheck rationale (out of errcheck scope; kept as documentation): test HTTP response body close; inconsequential once the body has been read

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health response: %v", err)
	}

	auditLogger, _ := body["audit_logger"].(string)
	if auditLogger != "ok" {
		t.Errorf("audit_logger = %q; want %q", auditLogger, "ok")
	}
	auditDegraded, _ := body["audit_degraded"].(bool)
	if auditDegraded {
		t.Errorf("audit_degraded = true; want false when logger is ok and no skips")
	}
}

// TestHealth_AuditNotDegraded_WhenDeliberatelyDisabled — operator did NOT
// configure audit (cfg.Sandbox.AuditLog=false). audit_logger reads as
// "unavailable" but audit_degraded must stay false: it's an intentional
// off-state, not a broken-and-supposed-to-work state.
func TestHealth_AuditNotDegraded_WhenDeliberatelyDisabled(t *testing.T) {
	audit.ResetSkippedForTest()
	t.Cleanup(audit.ResetSkippedForTest)

	s := health.NewServer("127.0.0.1", 0)
	s.SetAuditLoggerAvailableFunc(func() bool { return false })
	s.SetAuditLoggerConfiguredFunc(func() bool { return false })

	mux := http.NewServeMux()
	s.RegisterOnMux(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close() // errcheck rationale (out of errcheck scope; kept as documentation): test HTTP response body close; inconsequential once the body has been read

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health response: %v", err)
	}

	if logger, _ := body["audit_logger"].(string); logger != "unavailable" {
		t.Errorf("audit_logger = %q; want %q", logger, "unavailable")
	}
	if degraded, _ := body["audit_degraded"].(bool); degraded {
		t.Errorf("audit_degraded = true; want false when operator did not configure audit")
	}
}
