package knowledge

import "testing"

// TestIndexProgress_UnsetPhaseIsIndistinguishableFromIdle pins the invariant
// that IndexPhase's zero value behaves as idle everywhere, not just wherever a
// caller remembered to guard.
//
// IndexPhase is a string, so an IndexProgress nobody populated carries Phase
// "". Before the fix that read as IN FLIGHT: an unpopulated progress reported a
// build running, Presentation returned "indexing" and a search report claimed
// its results were incomplete — a permanently wrong state that no later event
// could clear, because nothing was running to finish it.
//
// The assertion is deliberately "same as explicit idle" rather than a list of
// literal expectations. A future predicate added to IndexProgress and given a
// case here is then checked against idle's behaviour rather than against a
// hardcoded answer someone has to remember to keep true.
func TestIndexProgress_UnsetPhaseIsIndistinguishableFromIdle(t *testing.T) {
	var unset IndexProgress                      // nobody wired a progress source
	idle := IndexProgress{Phase: IndexPhaseIdle} // the explicit form

	if unset.Phase != "" {
		t.Fatalf("precondition: zero value Phase = %q, want empty — this test is pointless otherwise", unset.Phase)
	}
	if IndexPhaseIdle == "" {
		t.Skip("IndexPhaseIdle is now the zero value; the bug this pins is unrepresentable")
	}

	for _, c := range []struct {
		name      string
		got, want any
	}{
		{"InFlight", unset.InFlight(), idle.InFlight()},
		{"BannerVisible", unset.BannerVisible(), idle.BannerVisible()},
		{"Presentation", unset.Presentation(), idle.Presentation()},
	} {
		if c.got != c.want {
			t.Errorf("unset phase: %s() = %v, want %v (what an explicitly idle progress returns)", c.name, c.got, c.want)
		}
	}

	ud, ut, uok := unset.Ratio()
	id, it, iok := idle.Ratio()
	if ud != id || ut != it || uok != iok {
		t.Errorf("unset phase: Ratio() = (%d,%d,%v), want (%d,%d,%v)", ud, ut, uok, id, it, iok)
	}

	// The wire-facing consequence, asserted directly: a report built from an
	// unpopulated progress must not tell the caller its results are partial.
	if got := buildSearchReport(unset, 10, 10, false, false).Complete; !got {
		t.Errorf("buildSearchReport(unset).Complete = false; an unpopulated progress " +
			"must not report results as incomplete — nothing is running to complete")
	}
}
