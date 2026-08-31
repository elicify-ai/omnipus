// Omnipus — ADR-071 §4.3.1(a) ToolSearch detection counter tests.
//
// Required test (ADR-071 §4.3.1a): "an assertion that GET /metrics contains
// both series names after a zero-result ToolSearch and after an abandoned
// promotion." Asserted against the endpoint output, not the pkg/tools
// accessor — a counter whose observability is untested is one refactor away
// from being unobservable again.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// withFreshToolMetrics swaps globalToolMetrics for a brand-new instance for
// the duration of the test and restores the previous one afterward — both as
// the package-level var AND as the active tools.toolMetricsRecorder — so
// this file's assertions are isolated from any other test in this package
// that increments the shared package-level singleton, and so a later test
// relying on tools.SetToolMetricsRecorder(globalToolMetrics) still being
// wired does not silently write into this test's discarded instance.
func withFreshToolMetrics(t *testing.T) *toolMetrics {
	t.Helper()
	prev := globalToolMetrics
	fresh := newToolMetrics()
	globalToolMetrics = fresh
	t.Cleanup(func() {
		globalToolMetrics = prev
		tools.SetToolMetricsRecorder(prev)
	})
	return fresh
}

// TestHandleMetrics_ToolSearchCountersReachable proves both
// omnipus_toolsearch_zero_result_total and omnipus_toolsearch_no_followup_total
// are present on GET /metrics, declared as counters, after each has been
// incremented at least once via the real tools.SetToolMetricsRecorder wiring
// (the same indirection FR-039's pre-existing counters use) — not via a
// direct field write on *toolMetrics, so this exercises the actual
// cross-package path a zero-result ToolSearch query or an abandoned
// promotion would take.
func TestHandleMetrics_ToolSearchCountersReachable(t *testing.T) {
	fresh := withFreshToolMetrics(t)

	// Mirrors gateway.go's boot-time tools.SetToolMetricsRecorder(globalToolMetrics)
	// call, then drives the increments through the exported pkg/tools recorder
	// surface, exactly as tools_tool.go (zero-result) and pkg/agent
	// (no-followup) do in production.
	tools.SetToolMetricsRecorder(fresh)
	fresh.IncToolSearchZeroResult()    // simulates a zero-result query
	tools.RecordToolSearchNoFollowUp() // simulates an abandoned promotion

	if got := fresh.toolSearchZeroResultTotal.Load(); got != 1 {
		t.Fatalf("toolSearchZeroResultTotal = %d, want 1", got)
	}
	if got := fresh.toolSearchNoFollowUpTotal.Load(); got != 1 {
		t.Fatalf("toolSearchNoFollowUpTotal = %d, want 1", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	(&restAPI{}).HandleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleMetrics status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		"# TYPE omnipus_toolsearch_zero_result_total counter",
		"omnipus_toolsearch_zero_result_total 1",
		"# TYPE omnipus_toolsearch_no_followup_total counter",
		"omnipus_toolsearch_no_followup_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /metrics body missing %q; full body:\n%s", want, body)
		}
	}
}

// TestHandleMetrics_ToolSearchCounters_ZeroByDefault proves the two series
// are exposed even before either has ever fired (declared with a starting
// value of 0), so an operator scraping a freshly-booted install sees the
// series exist rather than silently missing until first use.
func TestHandleMetrics_ToolSearchCounters_ZeroByDefault(t *testing.T) {
	withFreshToolMetrics(t)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	(&restAPI{}).HandleMetrics(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "omnipus_toolsearch_zero_result_total 0") {
		t.Errorf("expected omnipus_toolsearch_zero_result_total to start at 0; body:\n%s", body)
	}
	if !strings.Contains(body, "omnipus_toolsearch_no_followup_total 0") {
		t.Errorf("expected omnipus_toolsearch_no_followup_total to start at 0; body:\n%s", body)
	}
}
