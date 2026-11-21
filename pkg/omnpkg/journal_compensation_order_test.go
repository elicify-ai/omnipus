package omnpkg_test

// RED T-2 — TestJournal_CompensationOrder (spec TDD plan order 14, [r7
// CHANGED]). Spec: Rollback contract revision 7 ("Compensation steps and their
// failure rules") and FR-037, with fake stores:
//   - C1 (membership removal, then delegation edges) runs ONLY when the
//     membership write was attempted; C2 (DiscardPending: the pending record,
//     then the agent folder) ALWAYS follows;
//   - a failed C1 — membership removal fails, or a partial C1 after the
//     membership was removed — does NOT skip C2 (revision 7 replaced the
//     revision-6 stop rule; the reload steps C3/C4 are retired and no rollback
//     answer carries an activation);
//   - failed_step is "C1" or "C2"; leftovers are named in both failure cases;
//   - the journal states are started → committed → done, or rolled_back /
//     rollback_incomplete (asserted through the terminal-state sweep cases in
//     TestStagingStateMachine).
// BDD (revision 7): "Failure before the commit point is rolled back",
// "A failed membership removal does not stop the pending-record discard",
// "Incomplete rollback is reported and kept".

import (
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// recorder records which compensation ran, in order, and returns configured
// errors.
type recorder struct {
	order []string
	fail  map[string]error
}

func newRecorder(fail map[string]error) *recorder {
	return &recorder{order: []string{}, fail: fail}
}

func (r *recorder) step(name string) error {
	r.order = append(r.order, name)
	if err, ok := r.fail[name]; ok {
		return err
	}
	return nil
}

func (r *recorder) ran(names ...string) bool {
	if len(r.order) != len(names) {
		return false
	}
	for i := range names {
		if r.order[i] != names[i] {
			return false
		}
	}
	return true
}

// planFor returns a RollbackPlan wired to the recorder. attempted mirrors the
// MembershipAttempted flag: C1 runs only when the membership write happened.
func planFor(r *recorder, attempted bool) omnpkg.RollbackPlan {
	return omnpkg.RollbackPlan{
		MembershipAttempted:  attempted,
		RemoveMembership:     func() error { return r.step("C1.membership") },
		RemoveEdges:          func() error { return r.step("C1.edges") },
		DiscardPendingRecord: func() error { return r.step("C2.record") },
		DiscardPendingFolder: func() error { return r.step("C2.folder") },
	}
}

func TestJournal_CompensationOrder(t *testing.T) {
	t.Run("complete rollback: C1 then C2, nothing else", func(t *testing.T) {
		r := newRecorder(nil)
		res := omnpkg.RunRollback(planFor(r, true))
		require.True(t, res.Complete, "a rollback with no failures is complete")
		require.Empty(t, res.FailedStep)
		require.True(t, r.ran("C1.membership", "C1.edges", "C2.record", "C2.folder"),
			"order is C1 membership → C1 edges → C2 record → C2 folder; got %v", r.order)
	})

	t.Run("failure before the membership write skips C1 entirely", func(t *testing.T) {
		r := newRecorder(nil)
		res := omnpkg.RunRollback(planFor(r, false))
		require.True(t, res.Complete, "no C1 step ran, nothing failed")
		require.True(t, r.ran("C2.record", "C2.folder"),
			"C1 must not run when the membership write was never attempted; got %v", r.order)
	})

	// Revision-7 headline: a C1 failure does NOT skip C2 — the pending state is
	// still discarded, so the failed import leaves nothing behind.
	t.Run("C1 membership failure still discards the pending state", func(t *testing.T) {
		r := newRecorder(map[string]error{"C1.membership": errBoom})
		res := omnpkg.RunRollback(planFor(r, true))
		require.False(t, res.Complete)
		require.Equal(t, "C1", res.FailedStep, "details.failed_step is C1")
		require.True(t, r.ran("C1.membership", "C2.record", "C2.folder"),
			"after a failed membership removal, C1 stops (edges not reached) but C2 MUST still run; got %v", r.order)
		require.NotEmpty(t, res.Leftovers, "the leftovers are named (membership/edges remnants)")
	})

	t.Run("partial C1 (membership removed, edges fail) still discards the pending state", func(t *testing.T) {
		r := newRecorder(map[string]error{"C1.edges": errBoom})
		res := omnpkg.RunRollback(planFor(r, true))
		require.False(t, res.Complete)
		require.Equal(t, "C1", res.FailedStep)
		require.True(t, r.ran("C1.membership", "C1.edges", "C2.record", "C2.folder"),
			"C1 ran its two places, then C2 ran anyway (revision 7); got %v", r.order)
		require.NotEmpty(t, res.Leftovers)
	})

	t.Run("C2 pending-record failure stops the folder discard and reports C2", func(t *testing.T) {
		r := newRecorder(map[string]error{"C2.record": errBoom})
		res := omnpkg.RunRollback(planFor(r, true))
		require.False(t, res.Complete)
		require.Equal(t, "C2", res.FailedStep, "details.failed_step is C2")
		require.True(t, r.ran("C1.membership", "C1.edges", "C2.record"),
			"the folder discard must not run after the pending-record discard failed; got %v", r.order)
		require.NotEmpty(t, res.Leftovers, "the leftovers are named (pending record and folder remain)")
	})

	t.Run("partial C2 (record discarded, folder fails) reports failed_step C2", func(t *testing.T) {
		r := newRecorder(map[string]error{"C2.folder": errBoom})
		res := omnpkg.RunRollback(planFor(r, true))
		require.False(t, res.Complete)
		require.Equal(t, "C2", res.FailedStep, "the failing step is C2 (folder leftover)")
		require.True(t, r.ran("C1.membership", "C1.edges", "C2.record", "C2.folder"))
		require.NotEmpty(t, res.Leftovers, "the remaining folder is named")
	})

	// Revision 7 retired the reload: no step beyond C2 exists, and the result
	// carries no activation — asserted structurally: exactly four steps are
	// offered to the engine, and Complete stays false across both failure
	// points without any reload/activation observable.
	t.Run("no reload step exists after C2 (revision 7)", func(t *testing.T) {
		r := newRecorder(nil)
		res := omnpkg.RunRollback(planFor(r, true))
		require.Len(t, r.order, 4, "C1's two places and C2's two places, nothing more; got %v", r.order)
		require.True(t, res.Complete)
	})
}

var errBoom = errors.New("injected fault")
