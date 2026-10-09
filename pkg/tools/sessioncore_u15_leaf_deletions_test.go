package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// U15 — leaf deletions (FR-038; BDD-12.1; T14, T23).
//
// DEL-26 (session-core-spec.md §"U15 — Leaf deletion inventory"):
//   "pkg/tools/session.go::StatusExited, ProcessSession.IsDone, statusPriority
//    and matching poll/read/test compatibility branches — DELETE legacy exited
//    value/branches and compatibility-only fixtures; preserve current
//    done/killed/timeout/canceled terminal behavior and reasons. Source sweep
//    must show no current writer before removal."
//
// Proving a deletion = K (controlled absence, plus the canonical producer still
// exists) AND B (obsolete behaviour absent, canonical positive still works).

// sessionCoreU15AbsentFromPackageSources proves that no non-test .go file in the
// current package directory mentions `symbol`. This is an absence instrument, so
// it carries the two controls BDD-12.1 requires:
//
//   - known-present control: `presentControl` must appear in `presentControlFile`
//     — proving the reader reads real source (a miss is not a broken reader);
//   - injected-forbidden control: a temp fixture containing `symbol` must be
//     reported present by the same matcher — proving the matcher can detect a
//     real occurrence, so a negative here is meaningful.
//
// A `symbol` absent from every source file (including a deleted file) passes.
func sessionCoreU15AbsentFromPackageSources(t *testing.T, symbol, presentControlFile, presentControl string) {
	t.Helper()

	// Known-present control.
	ctrl, err := os.ReadFile(presentControlFile)
	if err != nil {
		t.Fatalf("U15 instrument: present-control read %s: %v", presentControlFile, err)
	}
	if !strings.Contains(string(ctrl), presentControl) {
		t.Fatalf("U15 instrument broken: present-control %q not found in %s", presentControl, presentControlFile)
	}

	// Injected-forbidden control.
	injected := filepath.Join(t.TempDir(), "injected_fixture.txt")
	if err := os.WriteFile(injected, []byte(symbol), 0o600); err != nil {
		t.Fatalf("U15 instrument: write injected fixture: %v", err)
	}
	injectedData, err := os.ReadFile(injected)
	if err != nil {
		t.Fatalf("U15 instrument: read injected fixture: %v", err)
	}
	if !strings.Contains(string(injectedData), symbol) {
		t.Fatalf("U15 instrument broken: injected forbidden token %q not detected", symbol)
	}

	// Real sweep over non-test package sources.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("U15 instrument: glob: %v", err)
	}
	sawControlFile := false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("U15 sweep: read %s: %v", f, err)
		}
		if strings.Contains(string(data), symbol) {
			t.Errorf("%s still references %q — DEL-26 requires the value/branches removed with a clean source sweep (FR-038)", f, symbol)
		}
		if f == presentControlFile {
			sawControlFile = true
		}
	}
	if !sawControlFile {
		t.Fatalf("U15 instrument broken: present-control file %s not among package sources", presentControlFile)
	}
}

// TestSessionCoreU15_StatusExitedRemoved is the K half of DEL-26. RED on the
// pre-cut code: session.go still declares `StatusExited SessionStatus = "exited"`
// and both IsDone and statusPriority still branch on it.
func TestSessionCoreU15_StatusExitedRemoved(t *testing.T) {
	sessionCoreU15AbsentFromPackageSources(t, "StatusExited", "session.go", "StatusDone")
}

// TestSessionCoreU15_TerminalStatusesAndReasonsPreserved is the B half of
// DEL-26: the canonical positive control that the surviving terminal statuses
// and their reason ranking still work. GREEN by design on the pre-cut code; it
// must stay green after the legacy value is removed.
//
// The IsDone assertions derive from the delivered spec (§DEL-26: "preserve
// current done/killed/timeout/canceled terminal behavior"). The ranking
// assertions are properties, not copied constants: a specific terminal reason
// must outrank the generic done fallback, which must outrank non-terminal
// running — the invariant that stops a racing generic caller erasing a specific
// reason.
func TestSessionCoreU15_TerminalStatusesAndReasonsPreserved(t *testing.T) {
	terminal := []SessionStatus{StatusDone, StatusKilled, StatusTimeout, StatusCanceled}
	for _, st := range terminal {
		s := &ProcessSession{Status: st}
		if !s.IsDone() {
			t.Errorf("IsDone() = false for terminal status %q, want true (DEL-26 preserves current terminal behaviour)", st)
		}
	}
	if (&ProcessSession{Status: StatusRunning}).IsDone() {
		t.Error("IsDone() = true for StatusRunning, want false")
	}

	for _, st := range []SessionStatus{StatusKilled, StatusTimeout, StatusCanceled} {
		if statusPriority(st) <= statusPriority(StatusDone) {
			t.Errorf("statusPriority(%q)=%d must rank above generic StatusDone=%d (DEL-26: preserve terminal reasons)",
				st, statusPriority(st), statusPriority(StatusDone))
		}
	}
	if statusPriority(StatusDone) <= statusPriority(StatusRunning) {
		t.Errorf("statusPriority(StatusDone)=%d must rank above StatusRunning=%d",
			statusPriority(StatusDone), statusPriority(StatusRunning))
	}
}
