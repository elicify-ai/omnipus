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

// plainHistoryOverflowError is the typed original provider rejection for the
// provenance oracle. ADR-051 §RD5 CRIT-001 and ADR-066 D7 require a known
// overflow cause to reach the terminal surfaces as itself, so the oracle must
// demand the exact cause object back through the provider stack's normal
// wrapping — a bare errors.New could only witness a substring. The message
// keeps the classifier's context-overflow marker so ClassifyError →
// FailoverContextOverflow triggers the forced-relief path exactly as a real
// provider rejection does.
// not-wire-format: test-only typed cause.
type plainHistoryOverflowError struct {
	detail string
}

func (e *plainHistoryOverflowError) Error() string { return "context_length_exceeded " + e.detail }

// metadataCommitFailureTurn wires the B-58 commit-failure scenario end to end
// and runs ONE real turn: typed context-overflow rejection, forced relief, and
// a CommitWindow fault at the storage boundary only (snapshot, append,
// projection, selection and serialization stay real). Each caller gets a fresh
// fixture — the scripted provider rejects exactly once per fixture — so every
// oracle group proves itself on its own independent run.
type metadataCommitFailureTurn struct {
	key           string
	al            *AgentLoop
	agent         *AgentInstance
	p             *plainHistoryProvider
	fault         *plainHistoryCommitFailure
	failure       error
	rejection     *plainHistoryOverflowError
	admitted      memory.WindowSnapshot
	admittedBytes []byte
	reliefEvents  func() []EventKind
	events        func() []Event
	err           error
}

func runMetadataCommitFailureTurn(t *testing.T) *metadataCommitFailureTurn {
	t.Helper()
	turn := &metadataCommitFailureTurn{key: "agent:mia:plain-write-failure"}
	// ADR-051 §RD5 CRIT-001: the original rejection is TYPED so the provenance
	// oracle can demand the exact cause back through the provider stack's
	// normal wrapping, not a message substring.
	turn.rejection = &plainHistoryOverflowError{detail: "initial rejection"}
	turn.p = plainHistoryRejectOnce(turn.rejection, "MUST NOT SEND divergent candidate")
	turn.al, turn.agent, _ = newPlainHistoryLoop(t, turn.p)
	history := append(plainHistoryExchange(1), plainHistoryExchange(2)...)
	seedPlainHistory(t, turn.agent, turn.key, history)
	turn.failure = errors.New("metadata transaction write failure injected at storage edge")
	turn.fault = &plainHistoryCommitFailure{SessionStore: turn.agent.Sessions, ContextWindowStore: plainHistoryStore(t, turn.agent), failure: turn.failure}
	turn.agent.Sessions = turn.fault
	turn.p.observe = func(call int, _ plainHistoryCapture) {
		if call == 1 {
			var err error
			turn.admitted, err = turn.fault.SnapshotWindow(context.Background(), turn.key)
			require.NoError(t, err)
			turn.admittedBytes = plainHistoryArchiveBytes(t, turn.agent, turn.key)
		}
	}
	sub := turn.al.SubscribeEvents(64)
	t.Cleanup(func() { turn.al.UnsubscribeEvents(sub.ID) })
	turn.reliefEvents = observePlainHistoryRelief(turn.al)
	_, turn.err = turn.al.runAgentLoop(context.Background(), turn.agent, processOptions{SessionKey: turn.key, UserMessage: "current"})
	turn.events = func() []Event { return collectEventStream(sub.C) }
	return turn
}

func TestContextOverflowPlain_MetadataCommitFailureDoesNotSendDivergentView(t *testing.T) {
	t.Run("B-58 divergence safety holds when forced relief commit fails", func(t *testing.T) {
		run := runMetadataCommitFailureTurn(t)
		assertPlainHistoryFirstAttempt(t, run.agent, run.p, "ASSISTANT-SOURCE-1-END", "ASSISTANT-SOURCE-2-END")
		require.Equal(t, 1, run.fault.commits, "FR-030/B-58: legal plain relief must reach the commit edge before proving failed-write safety")
		require.ErrorIs(t, run.err, run.failure, "B-58: propagate storage failure, never turn failed metadata persistence into permission to retry")
		require.Len(t, run.p.recorded(), 1, "no provider attempt after persistence failed")
		require.Empty(t, run.reliefEvents(), "no committed-relief/retry event for failed persistence")
		after, snapshotErr := run.fault.SnapshotWindow(context.Background(), run.key)
		require.NoError(t, snapshotErr)
		require.Equal(t, run.admitted, after, "failed candidate cannot alter real persisted window")
		require.Equal(t, run.admittedBytes, plainHistoryArchiveBytes(t, run.agent, run.key))
	})

	// N2 provenance (ADR-051 §RD5 CRIT-001 / ADR-066 D7): the storage failure
	// must surface WITHOUT erasing the known provider cause — the adjacent
	// notice-failure branch in loop_run_turn_response.go::retryContextOverflow
	// already joins both, and the two adjacent terminal overflow variants pin
	// context_too_long. Preserving both causes is the required shape.
	t.Run("returned chain preserves the typed original cause", func(t *testing.T) {
		run := runMetadataCommitFailureTurn(t)
		var preserved *plainHistoryOverflowError
		require.ErrorAs(t, run.err, &preserved,
			"N2 provenance: when forced relief also fails, the typed provider rejection must survive in the returned chain — a storage error must surface, not replace the known cause")
		require.Same(t, run.rejection, preserved,
			"N2 provenance: the exact original cause object must ride the returned chain, not a re-synthesized lookalike")
	})

	// N2 provenance: the live terminal error surface the SPA renders must keep
	// the original classification. TranslateTurnError reads the returned
	// chain — with the cause erased it can only answer unknown.
	t.Run("terminal error event classifies context_too_long", func(t *testing.T) {
		run := runMetadataCommitFailureTurn(t)
		var errPayloads []ErrorPayload
		for _, evt := range run.events() {
			if evt.Kind != EventKindError {
				continue
			}
			payload, ok := evt.Payload.(ErrorPayload)
			require.True(t, ok, "terminal error surface must carry ErrorPayload, got %T", evt.Payload)
			errPayloads = append(errPayloads, payload)
		}
		require.NotEmpty(t, errPayloads, "failed turn must emit the live EventKindError surface")
		for _, payload := range errPayloads {
			require.Equal(t, string(CodeContextTooLong), payload.Code,
				"N2 provenance (ADR-051 §RD5 CRIT-001): terminal error code must classify the known overflow cause (context_too_long), not degrade to unknown because relief failed")
		}
	})
}
