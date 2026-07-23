package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTokenBudget_CorruptFile_SurfacesNotSilentlyZero proves silent-MAJOR-1 is
// closed: a corrupted token_budget.json no longer silently resets consumed to 0
// with a nil error (which defeated the spend brake for prior spend — the doc
// claimed "reconciled at boot", but Load returned {consumed:0}, nil so
// NewTokenBudget skipped reconcile and Exhausted() read false). The persister
// now surfaces the unmarshal failure; NewTokenBudget emits an ERROR-grade
// "brake baseline unreliable" advisory (asserted via the Load contract below:
// err != nil) instead of looking like a fresh-install info Warn.
func TestTokenBudget_CorruptFile_SurfacesNotSilentlyZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tb.json")
	// Write a corrupt (unparseable) budget file — present on disk but invalid JSON.
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	t.Run("persister_Load_surfaces_error", func(t *testing.T) {
		// Contract: Load returns a non-nil error for a corrupt file. It used to
		// swallow this as {0}, nil — silently zeroing the consumed counter and
		// defeating the cap the operator set to bound prior spend.
		st, err := NewTokenBudgetPersister(path).Load()
		if err == nil {
			t.Fatalf("Load must surface a corrupt state file as a non-nil error; "+
				"got nil err and state %+v (silent zero — brake defeated for prior spend)", st)
		}
		if st.Consumed != 0 {
			t.Errorf("on a corrupt file, Load must return a zero-valued state, got consumed=%d", st.Consumed)
		}
	})

	t.Run("fresh_install_missing_file_is_not_an_error", func(t *testing.T) {
		// Contrast: a genuinely missing file (fresh install) is still {0}, nil —
		// the corruption surface must not turn a normal first boot into an error.
		missing := filepath.Join(dir, "does-not-exist.json")
		st, err := NewTokenBudgetPersister(missing).Load()
		if err != nil {
			t.Fatalf("a missing file is a fresh install, not an error: %v", err)
		}
		if st.Consumed != 0 {
			t.Errorf("fresh install consumed=%d want 0", st.Consumed)
		}
	})

	t.Run("NewTokenBudget_resets_consumed_to_zero", func(t *testing.T) {
		// With a corrupt file and a non-zero cap, NewTokenBudget cannot trust
		// the prior counter, so consumed is reset to 0. The operator-visible
		// surface (ERROR advisory) is asserted by persister_Load_surfaces_error
		// above; here we pin the reset behavior so the brake semantics are
		// documented: Exhausted()==false (not protecting prior spend), which is
		// exactly why surfacing the failure matters.
		tb := NewTokenBudget(1000, NewTokenBudgetPersister(path))
		if got := tb.Consumed(); got != 0 {
			t.Fatalf("after a corrupt load, consumed=%d want 0 (counter reset; future debits accumulate from zero)", got)
		}
		if tb.Exhausted() {
			t.Error("a freshly-reset pool must not report exhausted (prior spend baseline lost, not carried over)")
		}
	})

	t.Run("valid_file_still_reconciles", func(t *testing.T) {
		// Guard against a regression where the corruption surface accidentally
		// breaks the normal reconcile path: a valid file with consumed=300 must
		// still reconcile consumed to 300.
		validPath := filepath.Join(dir, "tb-valid.json")
		tb1 := NewTokenBudget(1000, NewTokenBudgetPersister(validPath))
		tb1.Debit(300)
		tb2 := NewTokenBudget(1000, NewTokenBudgetPersister(validPath))
		if got := tb2.Consumed(); got != 300 {
			t.Fatalf("valid file must still reconcile: consumed=%d want 300", got)
		}
	})
}
