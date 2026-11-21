package omnpkg_test

// RED B1 — the staging cancel unit tests (spec TDD order 5 and 5a, issue
// #1967).
//
// Spec: docs/internal/specs/export-import-b1-spec.md revision 5 — the
// "Cancel lifecycle" table, FR-010, the BDD scenarios "Cancel refusals",
// "Folder removal fails", "Folder left after a restart is removed by the next
// upload", "A restart alone removes nothing", "Cancel and apply at the same
// moment" (checkpoint-based oracles; no exclusive-or assertion).
//
// The two B1 error codes the tests assert are the spec's wire codes
// ("staging_busy", "staging_cleanup_failed"), transcribed as literals so the
// oracle stays in the spec text. All ordering is by channels and the fake
// clock — no sleeps.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// noRecoveryOps is a RecoveryOps whose every method fails loudly if called:
// the restart rows of the cancel lifecycle must never reach the recovery
// decisions (a ready/cancelled staging has no journal).
type noRecoveryOps struct{ t *testing.T }

func (n noRecoveryOps) Published(string) (bool, error) { return false, nil }
func (n noRecoveryOps) Leave(string, string) error {
	n.t.Fatal("recovery must not touch workspaces for a journal-less staging")
	return nil
}
func (n noRecoveryOps) Join(string, string) error {
	n.t.Fatal("recovery must not join workspaces for a journal-less staging")
	return nil
}
func (n noRecoveryOps) DiscardPending(string) error {
	n.t.Fatal("recovery must not discard a pending agent for a journal-less staging")
	return nil
}
func (n noRecoveryOps) UnpublishAndDiscard(string) error {
	n.t.Fatal("recovery must not unpublish for a journal-less staging")
	return nil
}
func (n noRecoveryOps) Audit(string, string, string, string) {}
func (n noRecoveryOps) PendingIDs() ([]string, error)        { return nil, nil }

// cancelSeamedRegistry builds a registry whose folder removal can be made to
// fail once through the registry's RemoveDir seam (spec: "test seam makes the
// removal fail"). The seam forwards to os.RemoveAll from its second call on,
// so "the removal seam is cleared" and "a later attempt succeeds" are the same
// observable. Returns the registry, the seam's call counter and the staging
// root.
func cancelSeamedRegistry(t *testing.T, clk *fakeClock) (*omnpkg.StagingRegistry, *int, string) {
	t.Helper()
	base := t.TempDir()
	reg := omnpkg.NewStagingRegistry(base, clk.Now)
	calls := 0
	reg.RemoveDir = func(dir string) error {
		calls++
		if calls == 1 {
			return os.ErrPermission // the first removal fails, as the seam's fault
		}
		return os.RemoveAll(dir)
	}
	return reg, &calls, base
}

// stageWithFolder reserves a staging and creates its folder, as a completed
// upload would have, and moves it to ready.
func stageWithFolder(t *testing.T, reg *omnpkg.StagingRegistry, base string) string {
	t.Helper()
	id, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(base, id), 0o755))
	require.NoError(t, reg.MarkReady(id))
	return id
}

// TestStagingRegistry_CancelReadyOnly — spec order 5: the cancel lifecycle
// state table and the retention promises (FR-010).
func TestStagingRegistry_CancelReadyOnly(t *testing.T) {
	t.Run("a ready staging is cancelled: entry dropped, folder gone, ID unknown", func(t *testing.T) {
		clk := newClock()
		base := t.TempDir()
		reg := omnpkg.NewStagingRegistry(base, clk.Now)
		id := stageWithFolder(t, reg, base)

		require.NoError(t, reg.Cancel(id), "the cancel of a ready staging succeeds (204)")
		_, statErr := os.Stat(filepath.Join(base, id))
		require.ErrorIs(t, statErr, os.ErrNotExist, "the staging folder is gone")
		_, err := reg.State(id)
		wantCode(t, err, "staging_not_found")
	})

	t.Run("a ready staging past expiry, not yet swept, still cancels", func(t *testing.T) {
		clk := newClock()
		base := t.TempDir()
		reg := omnpkg.NewStagingRegistry(base, clk.Now)
		id := stageWithFolder(t, reg, base)
		clk.Set(clk.Now().Add(31 * time.Minute))
		require.NoError(t, reg.Cancel(id), "expiry does not stop a cancel (204)")
		_, statErr := os.Stat(filepath.Join(base, id))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})

	t.Run("receiving, applying and needs-cleanup refuse with nothing changed", func(t *testing.T) {
		for _, setup := range []struct {
			name  string
			state func(t *testing.T, reg *omnpkg.StagingRegistry, id string)
		}{
			{"receiving", func(t *testing.T, reg *omnpkg.StagingRegistry, id string) {
				// Reserve leaves the staging receiving (upload still streaming).
			}},
			{"applying", func(t *testing.T, reg *omnpkg.StagingRegistry, id string) {
				require.NoError(t, reg.MarkReady(id))
				require.NoError(t, reg.BeginApply(id))
			}},
			{"needs-cleanup", func(t *testing.T, reg *omnpkg.StagingRegistry, id string) {
				require.NoError(t, reg.MarkReady(id))
				require.NoError(t, reg.BeginApply(id))
				require.NoError(t, reg.NeedsCleanup(id))
			}},
		} {
			t.Run(setup.name, func(t *testing.T) {
				clk := newClock()
				base := t.TempDir()
				reg := omnpkg.NewStagingRegistry(base, clk.Now)
				id, err := reg.Reserve()
				require.NoError(t, err)
				dir := filepath.Join(base, id)
				require.NoError(t, os.MkdirAll(dir, 0o755))
				journal := filepath.Join(dir, "journal.json")
				require.NoError(t, os.WriteFile(journal, []byte(`{"state":"started"}`), 0o644))
				before, err := os.ReadFile(journal)
				require.NoError(t, err)
				setup.state(t, reg, id)
				stBefore, err := reg.State(id)
				require.NoError(t, err)

				err = reg.Cancel(id)
				wantCode(t, err, "staging_busy")
				st, err := reg.State(id)
				require.NoError(t, err)
				require.Equal(t, stBefore, st, "the state is unchanged by the refused cancel")
				_, statErr := os.Stat(dir)
				require.NoError(t, statErr, "the folder is unchanged")
				after, err := os.ReadFile(journal)
				require.NoError(t, err)
				require.Equal(t, before, after, "the journal file is byte-identical")
			})
		}
	})

	t.Run("done and unknown IDs answer staging_not_found", func(t *testing.T) {
		clk := newClock()
		base := t.TempDir()
		reg := omnpkg.NewStagingRegistry(base, clk.Now)
		id, err := reg.Reserve()
		require.NoError(t, err)
		require.NoError(t, reg.MarkReady(id))
		require.NoError(t, reg.BeginApply(id))
		require.NoError(t, reg.FinishApply(id)) // done; folder removed by FinishApply
		wantCode(t, reg.Cancel(id), "staging_not_found")
		wantCode(t, reg.Cancel("no-such-staging"), "staging_not_found")
	})

	t.Run("a failed folder removal: cancelled state, never applicable, not a slot", func(t *testing.T) {
		clk := newClock()
		reg, seamCalls, base := cancelSeamedRegistry(t, clk)
		id := stageWithFolder(t, reg, base)

		err := reg.Cancel(id)
		wantCode(t, err, "staging_cleanup_failed")
		require.Equal(t, 1, *seamCalls, "the seam failed exactly the first removal")

		// The staging can never be applied again.
		wantCode(t, reg.BeginApply(id), "staging_not_found")
		// It does not count as a slot: five more uploads are admitted.
		for i := 0; i < omnpkg.MaxStagedImports; i++ {
			_, err := reg.Reserve()
			require.NoError(t, err, "the cancelled staging holds no slot (reservation %d)", i+1)
		}
		// The state is cancelled and the folder is still there.
		st, err := reg.State(id)
		require.NoError(t, err)
		require.Equal(t, omnpkg.StagingCancelled, st, "the entry moved to cancelled")
		_, statErr := os.Stat(filepath.Join(base, id))
		require.NoError(t, statErr, "the removal failed, so the folder is still on disk")

		// A repeated cancel succeeds once removal works again (204).
		require.NoError(t, reg.Cancel(id))
		_, statErr = os.Stat(filepath.Join(base, id))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})

	t.Run("the next upload's Sweep removes a cancelled staging's folder", func(t *testing.T) {
		clk := newClock()
		reg, seamCalls, base := cancelSeamedRegistry(t, clk)
		id := stageWithFolder(t, reg, base)
		wantCode(t, reg.Cancel(id), "staging_cleanup_failed")
		require.Equal(t, 1, *seamCalls)

		// The next upload's maintenance (Sweep) attempts the removal again;
		// removal is an attempt that can fail while the fault lasts — here it
		// succeeds, and the entry is dropped.
		require.Empty(t, reg.Sweep(), "no unfinished-journal orphan is reported for a cancelled staging")
		_, statErr := os.Stat(filepath.Join(base, id))
		require.ErrorIs(t, statErr, os.ErrNotExist)
		_, err := reg.State(id)
		wantCode(t, err, "staging_not_found")
	})

	// CHECK W02 row 5: cleanup must also be exercised CONTINUING to fail —
	// successive upload sweeps each retry the removal while the disk problem
	// lasts, and the first success ends it.
	t.Run("a removal that keeps failing: every successive upload sweep retries; the first success ends it", func(t *testing.T) {
		clk := newClock()
		base := t.TempDir()
		reg := omnpkg.NewStagingRegistry(base, clk.Now)
		removals := 0
		reg.RemoveDir = func(dir string) error {
			removals++
			if removals <= 2 {
				return os.ErrPermission // the cancel's removal and the FIRST sweep's retry fail
			}
			return os.RemoveAll(dir)
		}
		id := stageWithFolder(t, reg, base)

		wantCode(t, reg.Cancel(id), "staging_cleanup_failed")
		require.Equal(t, 1, removals)
		dir := filepath.Join(base, id)

		require.Empty(t, reg.Sweep(), "the next upload's sweep retries the removal")
		require.Equal(t, 2, removals, "the sweep attempted the removal again")
		_, statErr := os.Stat(dir)
		require.NoError(t, statErr, "the folder survives a failed sweep attempt")
		st, err := reg.State(id)
		require.NoError(t, err)
		require.Equal(t, omnpkg.StagingCancelled, st, "the entry stays cancelled while the removal fails")

		require.Empty(t, reg.Sweep(), "the second upload's sweep retries once more")
		require.Equal(t, 3, removals)
		_, statErr = os.Stat(dir)
		require.ErrorIs(t, statErr, os.ErrNotExist, "the first successful attempt ends the clean-up")
		_, err = reg.State(id)
		wantCode(t, err, "staging_not_found")
	})

	t.Run("a restart alone removes nothing; the next upload's sweep does", func(t *testing.T) {
		clk := newClock()
		reg, _, base := cancelSeamedRegistry(t, clk)
		id := stageWithFolder(t, reg, base)
		wantCode(t, reg.Cancel(id), "staging_cleanup_failed")
		dir := filepath.Join(base, id)
		_, statErr := os.Stat(dir)
		require.NoError(t, statErr)

		// Gateway restart: a fresh registry over the same base (the in-memory
		// entry is gone), boot recovery runs, and the clock moves 31 minutes
		// with no upload (spec: "A restart alone removes nothing").
		fresh := omnpkg.NewStagingRegistry(base, clk.Now)
		omnpkg.RecoverImports(context.Background(), base, noRecoveryOps{t: t})
		clk.Set(clk.Now().Add(31 * time.Minute))
		_, statErr = os.Stat(dir)
		require.NoError(t, statErr, "no timer and no start-up clean-up removes the folder in B1")

		// The next upload's sweep runs the orphan scan: the journal-less
		// folder is removed (spec "Folder left after a restart…").
		require.Empty(t, fresh.Sweep())
		_, statErr = os.Stat(dir)
		require.ErrorIs(t, statErr, os.ErrNotExist, "the next upload's sweep removes the journal-less folder")
	})
}

// TestStagingRegistry_CancelApplyInterleavings — spec order 5a: the
// checkpoint-based cancel/apply interleavings (no exclusive-or assertion).
func TestStagingRegistry_CancelApplyInterleavings(t *testing.T) {
	t.Run("cancel first: the apply then answers staging_not_found", func(t *testing.T) {
		clk := newClock()
		base := t.TempDir()
		reg := omnpkg.NewStagingRegistry(base, clk.Now)
		id := stageWithFolder(t, reg, base)
		require.NoError(t, reg.Cancel(id))
		wantCode(t, reg.BeginApply(id), "staging_not_found")
	})

	t.Run("apply paused after BeginApply: cancel refused, nothing changed at that checkpoint; the apply then ends normally", func(t *testing.T) {
		clk := newClock()
		base := t.TempDir()
		reg := omnpkg.NewStagingRegistry(base, clk.Now)
		id := stageWithFolder(t, reg, base)
		dir := filepath.Join(base, id)
		journal := filepath.Join(dir, "journal.json")
		require.NoError(t, os.WriteFile(journal, []byte(`{"state":"started"}`), 0o644))
		before, err := os.ReadFile(journal)
		require.NoError(t, err)

		// The apply pauses right after BeginApply (seam, channel handshake).
		// Release and join are DEFERRED (CHECK W02 5a): every failure path —
		// a fatal assertion, a red cancel, a killed mutant — unblocks the
		// paused goroutine and waits for it, so no seam state outlives the
		// subtest.
		paused := make(chan struct{})
		release := make(chan struct{})
		reg.AfterBeginApply = func(string) {
			close(paused)
			<-release
		}
		applyDone := make(chan error, 1)
		go func() { applyDone <- reg.BeginApply(id) }()
		closeRelease := sync.OnceFunc(func() { close(release) })
		joined := false
		t.Cleanup(func() {
			// Every failure path (fatal assertion, red cancel, killed mutant)
			// unblocks the paused apply and waits for it before leaving.
			closeRelease()
			if !joined {
				<-applyDone
			}
		})
		<-paused

		wantCode(t, reg.Cancel(id), "staging_busy")
		st, err := reg.State(id)
		require.NoError(t, err)
		require.Equal(t, omnpkg.StagingApplying, st, "at the checkpoint the staging is applying")
		_, statErr := os.Stat(dir)
		require.NoError(t, statErr, "at the checkpoint the folder exists")
		after, err := os.ReadFile(journal)
		require.NoError(t, err)
		require.Equal(t, before, after, "at the checkpoint the journal is byte-identical")

		// The pause is released: the apply ends in its normal way — here the
		// unchanged lifecycle's success path (FinishApply removes the folder).
		closeRelease()
		applyErr := <-applyDone
		joined = true // consumed; the cleanup's join is a no-op now
		require.NoError(t, applyErr, "the refused cancel had no effect on the apply")
		require.NoError(t, reg.FinishApply(id))
		_, statErr = os.Stat(dir)
		require.ErrorIs(t, statErr, os.ErrNotExist, "FinishApply removes the folder, as today")
		wantCode(t, reg.Cancel(id), "staging_not_found")
	})

	t.Run("apply refused in preparation: AbortApply returns ready, then the cancel succeeds", func(t *testing.T) {
		clk := newClock()
		base := t.TempDir()
		reg := omnpkg.NewStagingRegistry(base, clk.Now)
		id := stageWithFolder(t, reg, base)
		require.NoError(t, reg.BeginApply(id))
		require.NoError(t, reg.AbortApply(id)) // e.g. 404 workspace_not_found, nothing written
		require.NoError(t, reg.Cancel(id), "the cancel after a preparation refusal succeeds (204)")
		_, statErr := os.Stat(filepath.Join(base, id))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})
}
