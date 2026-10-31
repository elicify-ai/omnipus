package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder 2026-10-29 q1 a / fix-round-3: derived history state is local to
// one chat, not a reason to fail retention for everyone. P3 is once per actual
// failure episode, not once per sweep (boot may already have reported it).
func TestRetentionHistory_IndexStateDoesNotFailCleanupAndWarnsPerEpisode(t *testing.T) {
	for _, reason := range []string{"catchup_limit", "index_inconsistent", "index_missing"} {
		t.Run(reason, func(t *testing.T) {
			logs := selfHealCaptureLogs(t)
			f := selfHealSeed(t)
			goals := retentionHistoryObserveGoals(t, f.store, f.sid)
			for episode := 1; episode <= 2; episode++ {
				repair := retentionHistoryBreak(t, f, reason, episode)
				before := historyTree(t, historyIndexDir(f.store, f.sid))
				canonical := partitionBytes(t, f.store, f.sid)
				healsBefore := len(selfHealEvents(t, logs, f.sid, selfHealOKLog))
				require.Len(t, selfHealEvents(t, logs, f.sid, healFixUnavailableLog), episode-1,
					"fixture must not pre-log the new failure and hide a silent sweep")
				for repeat := 1; repeat <= 3; repeat++ {
					aged := retentionHistoryAgedSession(t, f.store)
					removed, err := f.store.RetentionSweep(7)
					assert.NoError(t, err, "index-state %s must not fail the shared sweep", reason)
					require.Equal(t, 1, removed, "the other chat's aged transcript still expires")
					require.NoDirExists(t, aged)
					goals.requireSwept(t, (episode-1)*3+repeat)
					require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)), "retention must NOT heal, replace or trim this chat's derived index")
					require.Equal(t, canonical, partitionBytes(t, f.store, f.sid), "unavailable history must not lose accepted canonical content")
					retentionHistoryRequireWarnings(t, logs, f.sid, reason, episode)
					_, unavailable := f.store.reconcileChatHistoryIndex(context.Background(), f.sid)
					selfHealRequireUnavailable(t, unavailable, reason)
					retentionHistoryRequireWarnings(t, logs, f.sid, reason, episode)
					require.Len(t, selfHealEvents(t, logs, f.sid, selfHealOKLog), healsBefore, "only explicit demand recovery between episodes may heal")
				}
				if episode == 1 {
					repair()
					require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "actual healthy recovery resets P3, never direct logger-state changes")
				}
			}
		})
	}
}

func TestRetentionHistory_StorageErrorRemainsAnErrorButGoalCleanupRuns(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := selfHealSeed(t)
	retentionHistoryBreak(t, f, "storage_error", 1)
	goals := retentionHistoryObserveGoals(t, f.store, f.sid)
	before := historyTree(t, historyIndexDir(f.store, f.sid))
	for repeat := 1; repeat <= 3; repeat++ {
		aged := retentionHistoryAgedSession(t, f.store)
		removed, err := f.store.RetentionSweep(7)
		require.Error(t, err, "a real filesystem error must not be swallowed with index-state failures")
		require.ErrorIs(t, err, ErrChatHistoryUnavailable)
		var unavailable *ChatHistoryUnavailableError
		require.ErrorAs(t, err, &unavailable)
		require.Equal(t, "storage_error", unavailable.Reason)
		var pathError *os.PathError
		require.ErrorAs(t, err, &pathError, "fault is actual filesystem I/O, not a manufactured history error")
		require.Equal(t, filepath.Join(historyIndexDir(f.store, f.sid), "FORMAT"), pathError.Path)
		require.Equal(t, 1, removed)
		require.NoDirExists(t, aged)
		goals.requireSwept(t, repeat)
		require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)))
		retentionHistoryRequireWarnings(t, logs, f.sid, "storage_error", 1)
		require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
	}
}

func TestRetentionHistory_BootWarningIsNotRepeatedBySweeps(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := selfHealSeed(t)
	retentionHistoryBreak(t, f, "index_inconsistent", 1)
	require.Empty(t, selfHealEvents(t, logs, f.sid, healFixUnavailableLog))
	require.NoError(t, f.store.Close())
	reopened, err := NewUnifiedStore(f.store.BaseDir())
	require.NoError(t, err)
	reopened.stopStatsFlusher()
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	retentionHistoryRequireWarnings(t, logs, f.sid, "index_inconsistent", 1) // real constructor's boot pass
	before := historyTree(t, historyIndexDir(reopened, f.sid))
	for repeat := 0; repeat < 3; repeat++ {
		removed, sweepErr := reopened.RetentionSweep(7)
		assert.NoError(t, sweepErr, "an already-reported bad index must not poison later sweeps")
		require.Zero(t, removed)
		retentionHistoryRequireWarnings(t, logs, f.sid, "index_inconsistent", 1)
		require.Equal(t, before, historyTree(t, historyIndexDir(reopened, f.sid)))
	}
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
}

func TestRetentionHistory_DemandHealAllowsNextSweep(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := selfHealSeed(t)
	// Same #1470 shape: an empty earlier rolled partition and 213 real accepted,
	// unindexed records beyond the first saved record. Only demand may heal it.
	retentionHistoryGap(t, f, "renata", selfHealBacklog)
	_, err := f.store.reconcileChatHistoryIndex(context.Background(), f.sid)
	selfHealRequireUnavailable(t, err, "catchup_limit")
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
	healFixRequireAddresses(t, f.store, f.sid, f.addresses)
	require.Len(t, selfHealEvents(t, logs, f.sid, selfHealOKLog), 1, "the real demand call completed one rebuild")
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
	selfHealRequirePages(t, f)
	goals := retentionHistoryObserveGoals(t, f.store, f.sid)
	aged := retentionHistoryAgedSession(t, f.store)
	before := historyTree(t, historyIndexDir(f.store, f.sid))
	warnings := len(selfHealEvents(t, logs, f.sid, healFixUnavailableLog))
	removed, err := f.store.RetentionSweep(7)
	require.NoError(t, err, "a demand-healed chat must not leave a sticky retention failure")
	require.Equal(t, 1, removed)
	require.NoDirExists(t, aged)
	goals.requireSwept(t, 1)
	require.Len(t, selfHealEvents(t, logs, f.sid, healFixUnavailableLog), warnings, "healthy sweep must not report a stale episode")
	require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)))
	healFixRequireAddresses(t, f.store, f.sid, f.addresses)
}

func TestRetentionHistory_HealthySweepPreservesExistingBehavior(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := selfHealSeed(t)
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
	before := historyTree(t, historyIndexDir(f.store, f.sid))
	goals := retentionHistoryObserveGoals(t, f.store, f.sid)
	aged := retentionHistoryAgedSession(t, f.store)
	removed, err := f.store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	require.NoDirExists(t, aged)
	goals.requireSwept(t, 1)
	require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)))
	require.Empty(t, selfHealEvents(t, logs, f.sid, healFixUnavailableLog))
	selfHealRequireIndexed(t, f)
	selfHealRequirePages(t, f)
}

func retentionHistoryGap(t *testing.T, f *selfHealFixture, prefix string, count int) {
	t.Helper()
	before := historyCheckpointOf(t, f.store, f.sid)
	owner := f.store.historyOwner(f.sid)
	func() {
		owner.mu.Lock()
		defer owner.mu.Unlock()
		for i := 0; i < count; i++ {
			address, err := f.store.AppendTranscriptAddressed(f.sid, historyUserEntry(fmt.Sprintf("retention-%s-%03d", prefix, i), selfHealBody, len(f.addresses)))
			require.NoError(t, err)
			f.addresses = append(f.addresses, address)
		}
	}()
	require.Equal(t, before, historyCheckpointOf(t, f.store, f.sid), "positive control: real accepted suffix has not reached the index")
}

func retentionHistoryBreak(t *testing.T, f *selfHealFixture, reason string, episode int) func() {
	t.Helper()
	if reason == "catchup_limit" {
		retentionHistoryGap(t, f, fmt.Sprintf("episode-%d", episode), 65) // first refused fact after the 64-fact bound
		return func() { require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid)) }
	}
	path := filepath.Join(historyIndexDir(f.store, f.sid), "FORMAT")
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	switch reason {
	case "index_inconsistent":
		require.NoError(t, os.WriteFile(path, []byte("owned-test-incompatible-history-format\n"), 0o600))
	case "index_missing":
		require.NoError(t, os.Remove(path))
	case "storage_error":
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Mkdir(path, 0o700)) // real ReadFile failure even when tests run as root
	default:
		t.Fatalf("unsupported retention history fixture %q", reason)
	}
	return func() {
		if reason == "storage_error" {
			require.NoError(t, os.Remove(path))
		}
		require.NoError(t, os.WriteFile(path, saved, 0o600))
	}
}

func retentionHistoryAgedSession(t *testing.T, store *UnifiedStore) string {
	t.Helper()
	meta, err := store.NewSession(SessionTypeChat, "", "mia")
	require.NoError(t, err)
	_, err = store.AppendTranscriptAddressed(meta.ID, historyUserEntry("owned-aged-row", "owned retention fixture", 0))
	require.NoError(t, err)
	path := filepath.Join(store.BaseDir(), meta.ID, archiveCurrentFile)
	old := time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	require.FileExists(t, path, "positive control: real aged canonical session exists before sweeping")
	return filepath.Dir(path)
}

// These are test-only imports/wiring: production session still has no goal
// dependency. The existing cross-package hook invokes the REAL goal store and
// sweeper, and cleanup restores its exact prior value, not an assumed nil.
type retentionHistoryGoals struct {
	store   *goal.Store
	agedID  string
	fresh   *goal.Goal
	days    []int
	removed []int
	errors  []error
}

func retentionHistoryObserveGoals(t *testing.T, sessions *UnifiedStore, sid string) *retentionHistoryGoals {
	t.Helper()
	now := time.Now().UTC()
	g := &retentionHistoryGoals{store: goal.NewStore(t.TempDir())}
	for _, at := range []time.Time{now.Add(-30 * 24 * time.Hour), now} {
		criterion := task.AcceptanceCriterion{
			ID: "retention-done", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
			Text: "Deliver the owned fixture", Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "retention-fixture-user"},
		}
		dod := criterion
		dod.ID = "retention-safe"
		record, err := goal.New(generated.GoalOwnerKindSession, sid, generated.GoalSourceChatCompiled,
			"Deliver fixture", "Deliver fixture", []task.AcceptanceCriterion{criterion}, []task.AcceptanceCriterion{dod}, 1, at)
		require.NoError(t, err)
		require.NoError(t, record.Activate(sid, at))
		require.NoError(t, record.Terminate(generated.GoalStateMet, "owned fixture complete", at))
		require.NoError(t, g.store.Create(record))
		persisted, err := g.store.Get(record.GoalID)
		require.NoError(t, err, "positive control: real terminal goal persisted before retention")
		if at.Equal(now) {
			g.fresh = persisted
		} else {
			g.agedID = persisted.GoalID
		}
	}
	old := goalRetentionSweepFn
	goalRetentionSweepFn = func(days int) (int, error) {
		g.days = append(g.days, days)
		removed, _, err := goal.Sweep(g.store, days, func(id string) bool {
			_, err := sessions.GetMeta(id)
			return err == nil
		}, time.Now().UTC())
		g.removed = append(g.removed, removed)
		g.errors = append(g.errors, err)
		return removed, err
	}
	t.Cleanup(func() { goalRetentionSweepFn = old })
	return g
}

func (g *retentionHistoryGoals) requireSwept(t *testing.T, calls int) {
	t.Helper()
	wantDays, wantRemoved := make([]int, calls), make([]int, calls)
	for i := range wantDays {
		wantDays[i] = 7 // same configured seven-day window, not a goal-specific default
	}
	wantRemoved[0] = 1 // one aged terminal goal, zero further removals on repeats
	require.Equal(t, wantDays, g.days, "goal pass runs on EVERY sweep with the exact configured days")
	require.Equal(t, wantRemoved, g.removed, "real goal sweep deletes the aged record, not a fabricated callback count")
	for _, err := range g.errors {
		require.NoError(t, err)
	}
	_, err := g.store.Get(g.agedID)
	require.ErrorIs(t, err, entity.ErrNotFound)
	fresh, err := g.store.Get(g.fresh.GoalID)
	require.NoError(t, err)
	require.Equal(t, g.fresh, fresh, "fresh terminal goal remains intact")
}

func retentionHistoryRequireWarnings(t *testing.T, logs *selfHealLogs, sid, reason string, count int) {
	t.Helper()
	warnings := selfHealEvents(t, logs, sid, healFixUnavailableLog)
	require.Len(t, warnings, count, "one existing P3 warning per episode, including one already emitted at boot")
	for _, warning := range warnings {
		require.Equal(t, "WARN", warning["level"])
		require.Equal(t, sid, warning["session_id"])
		require.Equal(t, reason, warning["reason"])
		require.Contains(t, warning, "cause")
	}
	require.NotContains(t, logs.text(), selfHealBody)
	// A sweep must not attempt healing, even if the attempt would fail without
	// publishing. A prior explicit demand-heal may have one success warning.
	require.Empty(t, selfHealEvents(t, logs, sid, selfHealBadLog))
}
