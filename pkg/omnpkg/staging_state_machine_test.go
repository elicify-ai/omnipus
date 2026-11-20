package omnpkg_test

// RED T-2 — TestStaging_StateMachine (spec TDD plan order 15).
// Spec: FR-022 / FR-039 (staging lifecycle: states receiving → ready →
// applying → done | needs-cleanup; max 5 slots reserved before decoding; the
// 30-minute clock starts at ready, expired at exactly 30:00; applying and
// needs-cleanup never expire; a refused apply returns to ready with the clock
// unchanged; retirement only by deleting the folder), FR-038 (one apply at a
// time, 409 import_in_progress), FR-044 (an orphan with an unfinished journal
// is kept, holds a slot and names the agent for the warning).
// Datasets: D9 rows 1–9 (fake clock), D13 rows 1–6 (engine-level races).
// All ordering is by channels and a fake clock — no sleeps.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// fakeClock is a settable clock for the registry.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)} }

// writeJournal writes the staging journal the way the engine will (the JSON
// shape is the shared JournalFile contract; the writer itself is GREEN's).
func writeJournal(t *testing.T, dir string, state omnpkg.JournalState, agentID string) {
	t.Helper()
	raw, err := json.Marshal(omnpkg.JournalFile{State: state, AgentID: agentID})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "journal.json"), raw, 0o644))
}

func TestStaging_StateMachine(t *testing.T) {
	t.Run("lifecycle: receiving → ready → applying → done", stagingLifecycleToDone)
	t.Run("D9-1 ready at 29:59 is still usable", stagingD9ReadyAt2959)
	t.Run("D9-2/D9-3 ready at exactly 30:00 or later is expired", stagingD9ExpiredAtOrAfter30)
	t.Run("D9-4 a staging that never existed is not found", stagingD9MissingID)
	t.Run("D9-5 a consumed (done) staging is not found", stagingD9ConsumedDone)
	t.Run("D9-6 a needs-cleanup staging is not usable", stagingD9NeedsCleanupUnusable)
	t.Run("D9-7 a path-traversal staging id touches no files", stagingD9PathTraversal)
	t.Run("D9-9 a refused apply returns to ready with the clock unchanged", stagingD9RefusedApplyKeepsClock)
	t.Run("applying stagings never expire (D13-1)", stagingD13ApplyingNeverExpires)
	t.Run("D13-2 one apply at a time: second apply refused as import_in_progress", stagingD13OneApplyAtATime)
	t.Run("D13-3 two uploads compete for the last free slot", stagingD13LastSlotRace)
	t.Run("D13-4 a failed upload releases its slot", stagingD13FailedUploadReleasesSlot)
	t.Run("D13-5 orphans: terminal journals deleted, unfinished kept with agent id", stagingD13OrphanJournals)
	t.Run("D13-5b an orphan with no journal is deleted on the next upload", stagingD13OrphanWithoutJournal)
	t.Run("D13-6 a retired needs-cleanup staging frees its slot", stagingD13RetiredNeedsCleanup)
	t.Run("FR-022 expired ready stagings are deleted by the next upload's sweep", stagingFR022ExpiredReadyDeleted)
}

func stagingLifecycleToDone(t *testing.T) {
	clk := newClock()
	reg := omnpkg.NewStagingRegistry(t.TempDir(), clk.Now)
	id, err := reg.Reserve()
	require.NoError(t, err)
	st, err := reg.State(id)
	require.NoError(t, err)
	require.Equal(t, omnpkg.StagingReceiving, st)
	require.NoError(t, reg.MarkReady(id))
	st, _ = reg.State(id)
	require.Equal(t, omnpkg.StagingReady, st)
	require.NoError(t, reg.BeginApply(id))
	st, _ = reg.State(id)
	require.Equal(t, omnpkg.StagingApplying, st)
	require.NoError(t, reg.FinishApply(id))
	st, _ = reg.State(id)
	require.Equal(t, omnpkg.StagingDone, st)
}

func stagingD9ReadyAt2959(t *testing.T) {
	clk := newClock()
	reg := omnpkg.NewStagingRegistry(t.TempDir(), clk.Now)
	id, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, reg.MarkReady(id))
	clk.Set(clk.Now().Add(29*time.Minute + 59*time.Second))
	require.NoError(t, reg.BeginApply(id), "29:59 is accepted (S-09.1)")
}

func stagingD9ExpiredAtOrAfter30(t *testing.T) {
	for _, elapsed := range []time.Duration{30 * time.Minute, 30*time.Minute + time.Second} {
		clk := newClock()
		reg := omnpkg.NewStagingRegistry(t.TempDir(), clk.Now)
		id, err := reg.Reserve()
		require.NoError(t, err)
		require.NoError(t, reg.MarkReady(id))
		clk.Set(clk.Now().Add(elapsed))
		err = reg.BeginApply(id)
		wantCode(t, err, omnpkg.CodeStagingNotFound)
	}
}

func stagingD9MissingID(t *testing.T) {
	reg := omnpkg.NewStagingRegistry(t.TempDir(), newClock().Now)
	err := reg.BeginApply("no-such-staging")
	wantCode(t, err, omnpkg.CodeStagingNotFound)
}

func stagingD9ConsumedDone(t *testing.T) {
	reg := omnpkg.NewStagingRegistry(t.TempDir(), newClock().Now)
	id, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, reg.MarkReady(id))
	require.NoError(t, reg.BeginApply(id))
	require.NoError(t, reg.FinishApply(id))
	wantCode(t, reg.BeginApply(id), omnpkg.CodeStagingNotFound)
}

func stagingD9NeedsCleanupUnusable(t *testing.T) {
	reg := omnpkg.NewStagingRegistry(t.TempDir(), newClock().Now)
	id, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, reg.MarkReady(id))
	require.NoError(t, reg.NeedsCleanup(id))
	wantCode(t, reg.BeginApply(id), omnpkg.CodeStagingNotFound)
}

func stagingD9PathTraversal(t *testing.T) {
	base := t.TempDir()
	reg := omnpkg.NewStagingRegistry(base, newClock().Now)
	err := reg.BeginApply("..%2F..%2Fentities")
	wantCode(t, err, omnpkg.CodeStagingNotFound)
	entries, readErr := os.ReadDir(base)
	require.NoError(t, readErr)
	require.Empty(t, entries, "no folder was created or read under the staging root")
}

func stagingD9RefusedApplyKeepsClock(t *testing.T) {
	clk := newClock()
	reg := omnpkg.NewStagingRegistry(t.TempDir(), clk.Now)
	id, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, reg.MarkReady(id))
	require.NoError(t, reg.BeginApply(id))
	require.NoError(t, reg.AbortApply(id)) // e.g. 404 workspace, nothing written
	st, err := reg.State(id)
	require.NoError(t, err)
	require.Equal(t, omnpkg.StagingReady, st)
	// The clock is unchanged: 29:59 after ready still applies, 30:00 not.
	clk.Set(clk.Now().Add(29*time.Minute + 59*time.Second))
	require.NoError(t, reg.BeginApply(id))
	require.NoError(t, reg.AbortApply(id))
	clk.Set(clk.Now().Add(time.Second)) // exactly 30:00
	wantCode(t, reg.BeginApply(id), omnpkg.CodeStagingNotFound)
}

func stagingD13ApplyingNeverExpires(t *testing.T) {
	clk := newClock()
	base := t.TempDir()
	reg := omnpkg.NewStagingRegistry(base, clk.Now)
	id, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(base, id), 0o755))
	require.NoError(t, reg.MarkReady(id))
	require.NoError(t, reg.BeginApply(id))
	clk.Set(clk.Now().Add(30*time.Minute + time.Second))
	reg.Sweep() // an upload's maintenance at 30:01
	st, err := reg.State(id)
	require.NoError(t, err)
	require.Equal(t, omnpkg.StagingApplying, st, "applying is pinned against expiry")
	_, statErr := os.Stat(filepath.Join(base, id))
	require.NoError(t, statErr, "the applying staging's folder is not deleted")
}

func stagingD13OneApplyAtATime(t *testing.T) {
	clk := newClock()
	reg := omnpkg.NewStagingRegistry(t.TempDir(), clk.Now)
	a, err := reg.Reserve()
	require.NoError(t, err)
	b, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, reg.MarkReady(a))
	require.NoError(t, reg.MarkReady(b))
	require.NoError(t, reg.BeginApply(a))
	// Another staging while A applies → 409 (FR-038); the same staging
	// twice may also answer 404 (D13 2 allows either).
	wantCode(t, reg.BeginApply(b), omnpkg.CodeImportInProgress)
	err = reg.BeginApply(a)
	require.True(t,
		omnpkg.CodeOf(err) == omnpkg.CodeImportInProgress || omnpkg.CodeOf(err) == omnpkg.CodeStagingNotFound,
		"D13-2: the second apply of the same staging is 409 or 404, got %v", err)
}

// D13-3: exactly one of two concurrent uploads wins the last free slot.
// Defect fixed: the waiter no longer wg.Add(1)s itself and then Wait()s before Done, which deadlocked the race before either Reserve returned.
func stagingD13LastSlotRace(t *testing.T) {
	clk := newClock()
	reg := omnpkg.NewStagingRegistry(t.TempDir(), clk.Now)
	for i := 0; i < omnpkg.MaxStagedImports-1; i++ {
		_, err := reg.Reserve()
		require.NoError(t, err)
	}
	start := make(chan struct{})
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id, err := reg.Reserve()
			results <- result{id: id, err: err}
		}()
	}
	close(start)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(raceTimeout):
		require.FailNow(t, "the slot race deadlocked")
	}
	ok, refused := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil {
			ok++
		} else {
			wantCode(t, r.err, omnpkg.CodeTooManyStagedImports)
			refused++
		}
	}
	require.Equal(t, 1, ok, "exactly one upload wins the last slot")
	require.Equal(t, 1, refused, "the other is refused as too_many_staged_imports")
}

func stagingD13FailedUploadReleasesSlot(t *testing.T) {
	reg := omnpkg.NewStagingRegistry(t.TempDir(), newClock().Now)
	id, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, reg.Release(id)) // decode failed
	_, err = reg.Reserve()
	require.NoError(t, err, "the released slot is immediately available")
}

func stagingD13OrphanJournals(t *testing.T) {
	clk := newClock()
	base := t.TempDir()
	done := filepath.Join(base, "stg-done")
	rolledBack := filepath.Join(base, "stg-rolled-back")
	unfinished := filepath.Join(base, "stg-unfinished")
	committed := filepath.Join(base, "stg-committed")
	for _, d := range []string{done, rolledBack, unfinished, committed} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	// Revision 7 journal states: done and rolled_back are terminal (the
	// import finished either way); started/committed are unfinished.
	writeJournal(t, done, omnpkg.JournalDone, testSourceID)
	writeJournal(t, rolledBack, omnpkg.JournalRolledBack, testSourceID)
	writeJournal(t, unfinished, omnpkg.JournalStarted, testSourceID)
	writeJournal(t, committed, omnpkg.JournalCommitted, testSourceID2)

	reg := omnpkg.NewStagingRegistry(base, clk.Now)
	kept := reg.Sweep() // the next upload's maintenance
	require.Len(t, kept, 2, "only the unfinished-journal orphans are kept")
	byStaging := map[string]string{}
	for _, u := range kept {
		byStaging[u.StagingID] = u.AgentID
	}
	require.Equal(t, testSourceID, byStaging["stg-unfinished"], "FR-044: the warning names the agent ID")
	require.Equal(t, testSourceID2, byStaging["stg-committed"], "a committed journal is still unfinished")
	for _, gone := range []string{done, rolledBack} {
		_, err := os.Stat(gone)
		require.ErrorIs(t, err, os.ErrNotExist, "the terminal-journal orphan folder is deleted")
	}
	for _, stay := range []string{unfinished, committed} {
		_, err := os.Stat(stay)
		require.NoError(t, err, "the unfinished orphan is kept for recovery")
	}

	// The kept orphans take slots: MaxStagedImports-2 more are admitted.
	for i := 0; i < omnpkg.MaxStagedImports-2; i++ {
		_, err := reg.Reserve()
		require.NoError(t, err)
	}
	_, err := reg.Reserve()
	wantCode(t, err, omnpkg.CodeTooManyStagedImports)
}

func stagingD13OrphanWithoutJournal(t *testing.T) {
	base := t.TempDir()
	orphan := filepath.Join(base, "stg-orphan")
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	reg := omnpkg.NewStagingRegistry(base, newClock().Now)
	kept := reg.Sweep()
	require.Empty(t, kept)
	_, err := os.Stat(orphan)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func stagingD13RetiredNeedsCleanup(t *testing.T) {
	clk := newClock()
	base := t.TempDir()
	reg := omnpkg.NewStagingRegistry(base, clk.Now)
	id, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(base, id), 0o755))
	require.NoError(t, reg.MarkReady(id))
	require.NoError(t, reg.NeedsCleanup(id)) // incomplete rollback kept it

	// The owner retires it by deleting the folder (Manual recovery step 3).
	require.NoError(t, os.RemoveAll(filepath.Join(base, id)))
	require.Empty(t, reg.Sweep(), "the dropped entry is not reported as unfinished")

	// The slot is free again without a restart.
	for i := 0; i < omnpkg.MaxStagedImports; i++ {
		_, err := reg.Reserve()
		require.NoError(t, err, "all 5 slots are available after retirement")
	}
}

func stagingFR022ExpiredReadyDeleted(t *testing.T) {
	clk := newClock()
	base := t.TempDir()
	reg := omnpkg.NewStagingRegistry(base, clk.Now)
	id, err := reg.Reserve()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(base, id), 0o755))
	require.NoError(t, reg.MarkReady(id))
	clk.Set(clk.Now().Add(30 * time.Minute)) // exactly 30:00 → expired
	require.Empty(t, reg.Sweep())
	_, err = os.Stat(filepath.Join(base, id))
	require.ErrorIs(t, err, os.ErrNotExist, "the expired staging folder is gone")
	wantCode(t, reg.BeginApply(id), omnpkg.CodeStagingNotFound)
}
