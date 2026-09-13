// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_store_fault_test.go supplies the storage-fault ARRANGE used by the
// silent-failure guards in goal_review_fixes_test.go, plus the self-tests
// that keep that arrange honest.
//
// Why this file exists, and what the arrange it replaces got wrong.
//
// The guards originally induced a storage fault the obvious way: chmod the
// goal entity directory (or a transcript file) unwritable and let the real
// write fail with EACCES. That works on a developer laptop and does NOT work
// in CI, because the CI worker runs as ROOT — and root ignores permission
// bits entirely (`mkdir d; chmod 000 d; touch d/x` succeeds as uid 0). The
// simulated fault never happened, the guarded code took its SUCCESS path,
// and every one of those guards failed in its own arrange step.
//
// Worse, one of them failed silently in the other direction: the old
// freezeGoalStore probed its own arrange with an Update against the id
// "no-such-goal", which returns ErrNotFound whether the store is frozen or
// not. That probe passed as root while the store was fully writable — a
// green precondition protecting nothing, exactly the shape
// docs/internal/false-green-patterns.md exists to name.
//
// What these faults do instead.
//
// Neither helper below relies on a permission bit, so neither has a
// privileged and an unprivileged behaviour — both fail the same way for uid
// 0 and for everyone else:
//
//   - failGoalRecordWrites replaces ONE record's pkg/entity sidecar lockfile
//     (<goal-id>.lock) with a DIRECTORY. Every write path in
//     pkg/entity.Store — Create, Update and Delete alike — runs its whole
//     read-modify-write inside fileutil.WithFlock(lockPath), which opens that
//     path O_RDWR|O_CREATE; opening a directory for writing is EISDIR for
//     every uid, root included. The READ paths (Get/List, and therefore
//     goal.Store's ListActive/GetActiveByOwner) deliberately take no flock —
//     see pkg/entity/store.go's load() doc comment — so reads keep working,
//     which is precisely the read-succeeds/write-fails shape the guards need.
//     Directory entries are skipped by scanIDs' `.json` filter, so the
//     poisoned lock path is invisible to listings.
//
//   - failTranscriptReads replaces a session's transcript.jsonl with a
//     DIRECTORY. read(2) against a directory fd is EISDIR on Linux and
//     macOS alike, for every uid, so UnifiedStore.ReadTranscript returns a
//     real error. Deleting the file would NOT do: ReadTranscript maps
//     os.IsNotExist to "no entries, no error", which is a clean empty read
//     rather than the failure being guarded.
//
// BOTH HELPERS ASSERT THEIR OWN FAULT BEFORE RETURNING. That property is
// load-bearing and must survive any future rework of this file: a guard
// whose arrange quietly stops biting goes GREEN while testing nothing, which
// is a worse outcome than the guard not existing. If pkg/entity ever changes
// its lockfile scheme, or UnifiedStore ever caches transcripts, these
// helpers fail loudly in arrange and name the reason — they do not degrade.
package agent

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// goalRecordLockPath is the pkg/entity sidecar lockfile for one goal record
// under the CURRENT $OMNIPUS_HOME (read fresh, exactly as
// resolveGoalRecordStore does).
func goalRecordLockPath(goalID string) string {
	return filepath.Join(config.OmnipusHomeDir(), "entities", "goals", goalID+".lock")
}

// failGoalRecordWrites makes every WRITE against the goal record goalID fail
// for the rest of the test, while every READ of it — and every write to
// every OTHER record — keeps working. Restored on cleanup.
//
// This is the storage-fault arrange for SF-5/SF-6/SF-7/SF-9 and for finding
// 9's failure branch: the shape a full disk or a permissions fault produces
// in the field, and the shape under which each of those findings rendered a
// failure as a success.
func failGoalRecordWrites(t *testing.T, goalID string) {
	t.Helper()
	if goalID == "" {
		t.Fatal("failGoalRecordWrites: empty goal id — the fault would target no record at all")
	}
	store := goal.NewStore(config.OmnipusHomeDir())
	before, err := store.Get(goalID)
	if err != nil {
		t.Fatalf("failGoalRecordWrites: %q must exist before its writes can be faulted: %v", goalID, err)
	}

	lock := goalRecordLockPath(goalID)
	// The sidecar is created lazily by fileutil.WithFlock's O_CREATE, so it
	// may or may not exist yet; either way it must become a directory.
	if rmErr := os.RemoveAll(lock); rmErr != nil {
		t.Fatalf("failGoalRecordWrites: clear %q: %v", lock, rmErr)
	}
	if mkErr := os.Mkdir(lock, 0o700); mkErr != nil {
		t.Fatalf("failGoalRecordWrites: poison %q: %v", lock, mkErr)
	}
	t.Cleanup(func() { _ = os.RemoveAll(lock) })

	// Prove the arrange bites, against the record the test actually uses —
	// never against a synthetic id, whose ErrNotFound would pass here with
	// the store fully writable.
	_, uerr := store.Update(goalID, func(*goal.Goal) error { return nil })
	if uerr == nil {
		t.Fatalf("failGoalRecordWrites: a no-op Update on %q still SUCCEEDED — the storage-fault arrange "+
			"did not take, so every assertion after it would pass for the wrong reason. pkg/entity's "+
			"write paths must run inside fileutil.WithFlock(%q)", goalID, lock)
	}
	// ...and that it bites for the RIGHT reason. EISDIR is a kind-of-file
	// refusal, identical for uid 0 and everyone else; EACCES/EPERM would mean
	// the fault had become a permission check, which CI's root user ignores.
	if !errors.Is(uerr, syscall.EISDIR) {
		t.Fatalf("failGoalRecordWrites: the write fault on %q reported %v, want EISDIR. Running as uid %d. "+
			"A permission-shaped fault does not fire for root and would make this arrange a no-op on CI",
			goalID, uerr, os.Geteuid())
	}

	// ...and prove reads are untouched: a fault that blinded the readers too
	// would be a different scenario from the one under guard.
	after, gerr := store.Get(goalID)
	if gerr != nil {
		t.Fatalf("failGoalRecordWrites: reads of %q must still succeed under the write fault: %v", goalID, gerr)
	}
	if after.State != before.State {
		t.Fatalf("failGoalRecordWrites: the probe changed %q's state (%q -> %q); the arrange must observe, "+
			"never mutate", goalID, before.State, after.State)
	}
}

// failTranscriptReads makes UnifiedStore.ReadTranscript fail for sessionID
// until the returned restore func is called (also registered as cleanup, and
// safe to call twice). The transcript's exact bytes are preserved and put
// back by restore, so a test can fail one turn's read and then let storage
// recover with the same content the agent actually wrote.
func failTranscriptReads(t *testing.T, store *session.UnifiedStore, sessionID string) (restore func()) {
	t.Helper()
	path := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failTranscriptReads: transcript not readable where expected (%q): %v", path, err)
	}
	if rmErr := os.Remove(path); rmErr != nil {
		t.Fatalf("failTranscriptReads: remove %q: %v", path, rmErr)
	}
	if mkErr := os.Mkdir(path, 0o700); mkErr != nil {
		t.Fatalf("failTranscriptReads: poison %q: %v", path, mkErr)
	}

	done := false
	restore = func() {
		if done {
			return
		}
		done = true
		if rmErr := os.RemoveAll(path); rmErr != nil {
			t.Fatalf("failTranscriptReads: un-poison %q: %v", path, rmErr)
		}
		if wErr := os.WriteFile(path, saved, 0o600); wErr != nil {
			t.Fatalf("failTranscriptReads: restore %q: %v", path, wErr)
		}
	}
	t.Cleanup(restore)

	// Prove the arrange bites: the read must ERROR, not merely come back
	// empty. An empty-but-successful read is a different code path
	// ("scanned cleanly, nothing there") and is not what SF-3 guards.
	_, rerr := store.ReadTranscript(sessionID)
	if rerr == nil {
		t.Fatalf("failTranscriptReads: ReadTranscript(%q) still succeeded — the read-fault arrange did not "+
			"take, so any claim-scan assertion after it would pass for the wrong reason", sessionID)
	}
	if !errors.Is(rerr, syscall.EISDIR) {
		t.Fatalf("failTranscriptReads: the read fault on %q reported %v, want EISDIR. Running as uid %d. "+
			"A permission-shaped fault does not fire for root and would make this arrange a no-op on CI",
			sessionID, rerr, os.Geteuid())
	}
	return restore
}

// newTestGoalRecordForFaultSuite persists one ACTIVE session-owned goal
// record in store and returns it. Deliberately minimal — these self-tests
// care about the store's write/read behaviour under fault, not about goal
// semantics.
func newTestGoalRecordForFaultSuite(t *testing.T, store *goal.Store) *goal.Goal {
	t.Helper()
	sid := newGoalID()
	g, err := goal.New(generated.GoalOwnerKindSession, sid, generated.TaskExplicit,
		"a goal for the fault suite", "", recordedGoalCriteria("a goal for the fault suite"),
		newFloorDoD(), armedGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("newTestGoalRecordForFaultSuite: goal.New: %v", err)
	}
	g.GoalID = newGoalID()
	if aerr := g.Activate(sid, time.Now().UTC()); aerr != nil {
		t.Fatalf("newTestGoalRecordForFaultSuite: Activate: %v", aerr)
	}
	if cerr := store.Create(g); cerr != nil {
		t.Fatalf("newTestGoalRecordForFaultSuite: Create: %v", cerr)
	}
	return g
}

// --- self-tests: the arrange must bite, as root and as anyone else --------

// TestGoalRecordWriteFaultBitesRegardlessOfUID is the guard on the guards'
// arrange.
//
// Given a real goal record under a real store
// When failGoalRecordWrites poisons it
// Then writes fail and reads still succeed — for the uid this suite happens
// to run as, whatever it is.
//
// The uid-independence is asserted MECHANICALLY, not by running this suite
// twice: the write must fail with EISDIR, which is not a discretionary
// access-control check at all. open(2) refuses O_RDWR on a directory for
// every caller — CAP_DAC_OVERRIDE/root bypasses permission BITS, and there
// is no bit here to bypass. An EACCES/EPERM failure instead would mean the
// fault had silently reverted to the permission-based arrange that CI's root
// user walked straight through, so this test rejects that outcome by name.
func TestGoalRecordWriteFaultBitesRegardlessOfUID(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	store := goal.NewStore(home)

	g := newTestGoalRecordForFaultSuite(t, store)

	failGoalRecordWrites(t, g.GoalID)

	_, err := store.Update(g.GoalID, func(cur *goal.Goal) error {
		cur.LatestReason = "this must never be persisted"
		return nil
	})
	if err == nil {
		t.Fatalf("running as uid %d: an Update against a write-faulted record SUCCEEDED. The whole "+
			"silent-failure guard suite depends on this fault being uid-independent", os.Geteuid())
	}
	if !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("running as uid %d: the write failed with %v, which is not EISDIR. The fault must be a "+
			"kind-of-file failure, never a permission failure: a permission-based fault does not fire for "+
			"root, which is exactly how these guards came to test nothing on CI", os.Geteuid(), err)
	}
	if errors.Is(err, os.ErrPermission) {
		t.Fatalf("running as uid %d: the write failed a PERMISSION check (%v) — root ignores permission "+
			"bits, so this arrange would not fire on the CI worker", os.Geteuid(), err)
	}

	got, err := store.Get(g.GoalID)
	if err != nil {
		t.Fatalf("running as uid %d: the record must stay readable under the write fault: %v", os.Geteuid(), err)
	}
	if got.LatestReason == "this must never be persisted" {
		t.Fatal("the refused Update was persisted anyway — the fault leaked a write through")
	}

	// The poisoned sidecar must be invisible to listings: a `.lock`
	// directory that leaked into scanIDs would break every reader.
	active, lerr := store.ListActive()
	if lerr != nil {
		t.Fatalf("ListActive under the write fault: %v", lerr)
	}
	if len(active) != 1 || active[0].GoalID != g.GoalID {
		t.Fatalf("ListActive returned %d record(s) under the write fault, want exactly the faulted one", len(active))
	}
}

// TestGoalRecordWriteFaultIsScopedToOneRecord states the fault's blast
// radius: freezing one goal must not freeze the store. Several guards run a
// production sweep that legitimately writes OTHER records while the record
// under test is faulted.
func TestGoalRecordWriteFaultIsScopedToOneRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	store := goal.NewStore(home)

	faulted := newTestGoalRecordForFaultSuite(t, store)
	neighbour := newTestGoalRecordForFaultSuite(t, store)

	failGoalRecordWrites(t, faulted.GoalID)

	if _, err := store.Update(neighbour.GoalID, func(cur *goal.Goal) error {
		cur.LatestReason = "the neighbour is still writable"
		return nil
	}); err != nil {
		t.Fatalf("a write to an UNfaulted record failed: %v — the fault must be scoped to one record", err)
	}
	got, gerr := store.Get(neighbour.GoalID)
	if gerr != nil {
		t.Fatalf("re-read the unfaulted neighbour: %v", gerr)
	}
	if got.LatestReason != "the neighbour is still writable" {
		t.Fatalf("the neighbour's write did not persist: LatestReason = %q", got.LatestReason)
	}
}

// TestTranscriptReadFaultBitesAndRestores is the same guard for SF-3's read
// fault: the read must ERROR while poisoned (not return an empty transcript)
// and the exact bytes must come back on restore.
func TestTranscriptReadFaultBitesAndRestores(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	if err := store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "fault-probe-1", Type: session.EntryTypeMessage, Role: "assistant",
		Content: "the bytes that must survive the fault",
	}); err != nil {
		t.Fatalf("AppendTranscriptStrict: %v", err)
	}

	restore := failTranscriptReads(t, store, sid)

	entries, err := store.ReadTranscript(sid)
	if err == nil {
		t.Fatalf("running as uid %d: ReadTranscript SUCCEEDED under the read fault (%d entries). SF-3's "+
			"guard depends on this fault being uid-independent", os.Geteuid(), len(entries))
	}
	// Same uid-independence contract as the write fault: read(2) against a
	// directory is EISDIR for every caller, root included — never EACCES.
	if !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("running as uid %d: the transcript read failed with %v, which is not EISDIR. A "+
			"permission-based read fault does not fire for root", os.Geteuid(), err)
	}
	if errors.Is(err, os.ErrPermission) {
		t.Fatalf("running as uid %d: the transcript read failed a PERMISSION check (%v) — root ignores "+
			"permission bits, so this arrange would not fire on the CI worker", os.Geteuid(), err)
	}

	restore()
	recovered, rerr := store.ReadTranscript(sid)
	if rerr != nil {
		t.Fatalf("ReadTranscript after restore: %v", rerr)
	}
	var found bool
	for _, e := range recovered {
		if e.Content == "the bytes that must survive the fault" {
			found = true
		}
	}
	if !found {
		t.Fatalf("restore did not put the transcript's own bytes back: %d entries recovered", len(recovered))
	}
}
