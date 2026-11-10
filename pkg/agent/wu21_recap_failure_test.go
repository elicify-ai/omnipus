package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// §3.2(3): a failed pair-summary write never files the recap marker. A
// healthy sibling workspace still succeeds, so "everything is broken" cannot
// satisfy this test. A directory at the target forces an atomic-publish error
// on every platform, including elevated test users (chmod alone would not).
func TestWU21_PairSummaryWriteFailureIsVisibleAndDoesNotFileRetro(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "success"
		if fallback {
			name = "fallback"
		}
		t.Run(name, func(t *testing.T) {
			f := wu21New(t)
			a := f.recap(t, "alpha", "WU21_BLOCKED_ALPHA")
			if err := os.MkdirAll(filepath.Join(f.path("alpha", "self"), "blocker"), 0o700); err != nil {
				t.Fatal(err)
			}
			if fallback {
				err := f.al.writeHeuristicFallbackRetroWithCount(a.sessionID, "idle", "llm_error", f.ag, 1, 0, a.carryForward)
				if err == nil {
					t.Error("§3.2(3): pair-summary write failed, but fallback returned success")
				}
				if outcome := lastRecapOutcome(t, f.auditDir); !strings.HasPrefix(outcome, "fallback_write_failed:llm_error:") || !strings.Contains(outcome, "last_session") {
					t.Errorf("§3.2(3): failed fallback audit=%q", outcome)
				}
			} else {
				a.persistResponse()
				if outcome := lastRecapOutcome(t, f.auditDir); outcome != "write_failed:last_session" {
					t.Errorf("§3.2(3): success-path write failure audit=%q want write_failed:last_session", outcome)
				}
			}
			if f.al.agentSessionHasRetro(f.ag, a.sessionID) {
				t.Error("§3.2(3): retro filed after summary write failed; future retry would be blocked")
			}
			b := f.recap(t, "beta", "WU21_HEALTHY_BETA")
			b.persistResponse()
			t.Run("healthy_sibling_positive", func(t *testing.T) {
				if outcome := lastRecapOutcome(t, f.auditDir); outcome != "success" {
					t.Fatalf("healthy sibling success audit=%q", outcome)
				}
				if !f.al.agentSessionHasRetro(f.ag, b.sessionID) {
					t.Fatal("healthy sibling did not file its real recap marker")
				}
			})
			content, err := os.ReadFile(f.path("beta", "self"))
			if err != nil || !strings.Contains(string(content), "WU21_HEALTHY_BETA") {
				t.Errorf("§3.2(2): healthy sibling pair summary missing: err=%v", err)
			}
		})
	}
}
