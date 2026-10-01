package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func plainHistoryContextDirectory(t *testing.T, agent *AgentInstance) string {
	t.Helper()
	store := agent.Sessions
	if fault, ok := store.(*plainHistoryCommitFailure); ok {
		store = fault.SessionStore
	}
	unified, ok := store.(*session.UnifiedStore)
	require.True(t, ok, "fixture: locate the actual unified context backend")
	return filepath.Join(unified.BaseDir(), ".context")
}

func plainHistoryArchiveBytes(t *testing.T, agent *AgentInstance, key string) []byte {
	t.Helper()
	filename := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(key) + ".jsonl"
	data, err := os.ReadFile(filepath.Join(plainHistoryContextDirectory(t, agent), filename))
	require.NoError(t, err, "fixture: read actual admitted archive bytes, not a synthesized message list")
	return data
}

func TestContextOverflowPlain_CommittedReliefPreservesArchiveAndReload(t *testing.T) {
	const key = "agent:mia:plain-reload"
	history := append(plainHistoryExchange(1), plainHistoryExchange(2)...)
	current := providers.Message{Role: "user", Content: "current media anchor", Media: []string{"media://original-user-media"}}
	al, agent, ts, messages := plainHistoryCheckpointFixture(t, key, history, current)
	store := plainHistoryStore(t, agent)
	before, err := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	archiveBytes := plainHistoryArchiveBytes(t, agent, key)
	candidate, changed, err := al.checkpointWindow(context.Background(), ts, messages, nil, true)
	require.NoError(t, err)
	require.True(t, changed, "FR-030/B-58: legally reducible plain source must be committed")
	require.Equal(t, append(plainHistoryExchange(2), current), plainHistoryNonSystem(candidate), "newest assistant and original user/media identity survive exactly")
	require.Contains(t, candidate[0].Content, messages[0].Content, "pinned instruction remains")
	require.Equal(t, archiveBytes, plainHistoryArchiveBytes(t, agent, key), "committed relief must not rewrite even one admitted archive byte")
	after, err := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, 2, after.State.Skip, "exact completed oldest plain prefix")
	require.Equal(t, before.State.Count, after.State.Count)
	require.Equal(t, before.Archive, after.Archive)

	// A separately constructed REAL store has no live backend cache or staged
	// message slice. Its metadata+archive projection must reproduce the view.
	reopened, err := memory.NewJSONLStore(plainHistoryContextDirectory(t, agent))
	require.NoError(t, err)
	reloadedBackend := session.NewJSONLBackend(reopened)
	reloaded, err := reloadedBackend.SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, after, reloaded, "fresh backend reads the exact committed cursor/projection/anchor and source")
	require.Equal(t, plainHistoryNonSystem(candidate), reloadedBackend.GetHistory(key), "reloaded reduced live view equals the committed candidate, including media")
}

func TestContextOverflowPlain_AbortRestoresActualTurnStart(t *testing.T) {
	const key = "agent:mia:plain-abort"
	p := plainHistoryRejectOnce(errors.New("context_length_exceeded"), "unused")
	al, agent, _ := newPlainHistoryLoop(t, p)
	history := append(plainHistoryExchange(1), plainHistoryExchange(2)...)
	seedPlainHistory(t, agent, key, history)
	store := plainHistoryStore(t, agent)
	start, err := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	startBytes := plainHistoryArchiveBytes(t, agent, key)
	ts := newTurnState(agent, processOptions{SessionKey: key, UserMessage: "turn appended user"}, turnEventScope{turnID: "plain-abort"})
	require.NotNil(t, ts.initialWindow)
	require.Equal(t, start.State, *ts.initialWindow, "real constructor captured the actual start before the append")
	ts.ctx = context.Background()
	require.NoError(t, ts.appendWindowMessage(providers.Message{Role: "user", Content: ts.userMessage, Media: []string{"media://abort-anchor"}}))
	admittedBytes := plainHistoryArchiveBytes(t, agent, key)
	messages := append([]providers.Message{{Role: "system", Content: "pinned"}}, agent.Sessions.GetHistory(key)...)
	_, changed, err := al.checkpointWindow(context.Background(), ts, messages, nil, true)
	require.NoError(t, err)
	require.True(t, changed, "must enter a genuinely committed intermediate relief state before testing abort")
	require.Equal(t, admittedBytes, plainHistoryArchiveBytes(t, agent, key), "relief itself does not rewrite admitted source")
	intermediate, err := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, 2, intermediate.State.Skip)
	require.Equal(t, start.State.Count+1, intermediate.State.Count)
	// Invoke the actual hard-abort exit, not assignment of fabricated metadata.
	result, err := al.abortTurn(ts, "plain-history-test", hardInterruptAbortReason)
	require.NoError(t, err)
	require.Equal(t, TurnEndStatusAborted, result.status)
	restored, err := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, start, restored, "B-58: restore actual start, NOT the last intermediate cursor or the post-append count")
	require.Equal(t, startBytes, plainHistoryArchiveBytes(t, agent, key), "abort removes only this turn's suffix and restores the original archive bytes")
	require.Equal(t, history, agent.Sessions.GetHistory(key))
}

// Commit failure is injected at the storage boundary only. Snapshot/append/
// projection/selection/serialization remain real. This proves caller behavior
// after a metadata transaction fails, NOT an operating-system write fault.
// not-wire-format: private test-only storage boundary adapter.
type plainHistoryCommitFailure struct {
	session.SessionStore
	session.ContextWindowStore
	failure error
	commits int
}

func (s *plainHistoryCommitFailure) CommitWindow(_ context.Context, _ string, _, _ memory.WindowState) error {
	s.commits++
	return s.failure
}

func TestContextOverflowPlain_MetadataCommitFailureDoesNotSendDivergentView(t *testing.T) {
	const key = "agent:mia:plain-write-failure"
	p := plainHistoryRejectOnce(errors.New("context_length_exceeded initial rejection"), "MUST NOT SEND divergent candidate")
	al, agent, _ := newPlainHistoryLoop(t, p)
	history := append(plainHistoryExchange(1), plainHistoryExchange(2)...)
	seedPlainHistory(t, agent, key, history)
	failure := errors.New("metadata transaction write failure injected at storage edge")
	fault := &plainHistoryCommitFailure{SessionStore: agent.Sessions, ContextWindowStore: plainHistoryStore(t, agent), failure: failure}
	agent.Sessions = fault
	var admitted memory.WindowSnapshot
	var admittedBytes []byte
	p.observe = func(call int, _ plainHistoryCapture) {
		if call == 1 {
			var err error
			admitted, err = fault.SnapshotWindow(context.Background(), key)
			require.NoError(t, err)
			admittedBytes = plainHistoryArchiveBytes(t, agent, key)
		}
	}
	reliefEvents := observePlainHistoryRelief(al)
	_, err := al.runAgentLoop(context.Background(), agent, processOptions{SessionKey: key, UserMessage: "current"})
	assertPlainHistoryFirstAttempt(t, agent, p, "ASSISTANT-SOURCE-1-END", "ASSISTANT-SOURCE-2-END")
	require.Equal(t, 1, fault.commits, "FR-030/B-58: legal plain relief must reach the commit edge before proving failed-write safety")
	require.ErrorIs(t, err, failure, "B-58: propagate storage failure, never turn failed metadata persistence into permission to retry")
	require.Len(t, p.recorded(), 1, "no provider attempt after persistence failed")
	require.Empty(t, reliefEvents(), "no committed-relief/retry event for failed persistence")
	after, snapshotErr := fault.SnapshotWindow(context.Background(), key)
	require.NoError(t, snapshotErr)
	require.Equal(t, admitted, after, "failed candidate cannot alter real persisted window")
	require.Equal(t, admittedBytes, plainHistoryArchiveBytes(t, agent, key))
}
