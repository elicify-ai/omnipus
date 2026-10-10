package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// U7 — boot-recovery deletions (FR-025; DEL-13; spec
// docs/internal/specs/session-core-spec.md §"U7" and DEL-13 row).
//
// DEL-13: "pkg/agent/boot_sweep.go::SteerBootRecovery.failLegacy **only** as
// the removed boot-recovery helper, with its exclusively dead
// callers/compatibility support" — DELETE. KEEP failInterrupted,
// boot_ordinary_recovery.go::recoverOrdinaryRoot, current Classify
// record/metadata validation and restart-stop settlement.
//
// Proving a deletion = K (controlled absence) AND B (obsolete behaviour absent;
// the canonical producer still exists). The absence instrument below carries
// both controls BDD-12.1 requires (known-present + injected-forbidden), so a
// negative is meaningful.

// sessionCoreU7AbsentFromPackageSources proves no non-test .go file in the
// current package directory mentions `symbol`. Modeled on the U15 instrument
// (pkg/tools/sessioncore_u15_leaf_deletions_test.go):
//
//   - known-present control: `presentControl` must appear in `presentControlFile`
//     — proving the reader reads real source (a miss is not a broken reader);
//   - injected-forbidden control: a temp fixture containing `symbol` must be
//     reported present by the same matcher — proving the matcher can detect a
//     real occurrence, so a negative here is meaningful.
func sessionCoreU7AbsentFromPackageSources(t *testing.T, symbol, presentControlFile, presentControl string) {
	t.Helper()

	ctrl, err := os.ReadFile(presentControlFile)
	if err != nil {
		t.Fatalf("U7 instrument: present-control read %s: %v", presentControlFile, err)
	}
	if !strings.Contains(string(ctrl), presentControl) {
		t.Fatalf("U7 instrument broken: present-control %q not found in %s", presentControl, presentControlFile)
	}

	injected := filepath.Join(t.TempDir(), "injected_fixture.txt")
	if err := os.WriteFile(injected, []byte(symbol), 0o600); err != nil {
		t.Fatalf("U7 instrument: write injected fixture: %v", err)
	}
	injectedData, err := os.ReadFile(injected)
	if err != nil {
		t.Fatalf("U7 instrument: read injected fixture: %v", err)
	}
	if !strings.Contains(string(injectedData), symbol) {
		t.Fatalf("U7 instrument broken: injected forbidden token %q not detected", symbol)
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("U7 instrument: glob: %v", err)
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
			t.Fatalf("U7 sweep: read %s: %v", f, err)
		}
		if strings.Contains(string(data), symbol) {
			t.Errorf("%s still references %q — DEL-13 requires the legacy boot-recovery helper (and its dead compatibility support) removed with a clean source sweep (FR-025)", f, symbol)
		}
		if f == presentControlFile {
			sawControlFile = true
		}
	}
	if !sawControlFile {
		t.Fatalf("U7 instrument broken: present-control file %s not among package sources", presentControlFile)
	}
}

// TestSessionCoreU7_FailLegacyRemoved is the DEL-13 K half. RED on the pre-cut
// code: boot_sweep.go still declares `func (r *SteerBootRecovery) failLegacy(...)`
// and Run() still calls it for ClassLegacyDelegate.
func TestSessionCoreU7_FailLegacyRemoved(t *testing.T) {
	sessionCoreU7AbsentFromPackageSources(t, "failLegacy", "boot_sweep.go", "failInterrupted")
}

// TestSessionCoreU7_LegacyFailReasonConstantRemoved covers DEL-13's "with its
// exclusively dead callers/compatibility support": the pre-ADR-091 failure
// reason constant is referenced in production ONLY by failLegacy, so once the
// helper goes the constant is dead compatibility support too. RED on the pre-cut
// code: boot_sweep.go still declares
// `const failedReasonPreADR091NotResumable = "pre-adr-091-not-resumable"`.
func TestSessionCoreU7_LegacyFailReasonConstantRemoved(t *testing.T) {
	sessionCoreU7AbsentFromPackageSources(t, "failedReasonPreADR091NotResumable", "boot_sweep.go", "failInterrupted")
}
