// turn_exit_notice_1775_test.go: #1775 — a stopped turn is saved in the order
// the user saw it live: the interrupted partial answer, then the stop notice,
// then the turn_canceled marker. Before the fix typedTurnExit wrote the notice
// first, so a reload showed notice, partial.
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// stoppedTurnFixture builds a loop, a real session and an in-flight root turn
// with a user Stop already fired through the real RequestCancel path.
func stoppedTurnFixture(t *testing.T) (*AgentLoop, *turnState, *session.UnifiedStore, string) {
	t.Helper()
	workspaceDir := t.TempDir() + "/workspace"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "stop-order-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	ts := &turnState{
		agent:               &AgentInstance{ID: "mia"},
		turnID:              "turn-1775",
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	al.activeTurnStates.Store(meta.ID, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(meta.ID) })
	outcome, err := al.RequestCancel(context.Background(), CancelScope{SessionID: meta.ID},
		CancelCanceller{UserID: "u", Channel: "web"}, CancelHooks{})
	require.NoError(t, err)
	require.True(t, outcome.Fired)
	return al, ts, store, meta.ID
}

// savedKinds reads the transcript back as a short, ordered list of kinds.
func savedKinds(t *testing.T, store *session.UnifiedStore, sessionID string) []string {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	kinds := make([]string, 0, len(entries))
	for _, e := range entries {
		switch {
		case e.Type == session.EntryTypeSystem:
			kinds = append(kinds, "notice")
		case e.Type == session.EntryTypeTurnCancelled:
			kinds = append(kinds, "turn_canceled")
		case e.Role == "assistant":
			kinds = append(kinds, "assistant")
		default:
			kinds = append(kinds, string(e.Type))
		}
	}
	return kinds
}

// The partial is written by the answer-preserving defer (the non-streamed
// path writes it directly), the marker by Finish. The notice must land between.
func TestStoppedTurn_SavedOrderIsPartialNoticeMarker(t *testing.T) {
	t.Parallel()
	al, ts, store, sessionID := stoppedTurnFixture(t)

	_, _, err := al.typedTurnExit(ts, 1, "stop-order-model", context.Canceled)
	require.Error(t, err)
	// runTurn's deferred chain: the interrupted answer, then Finish.
	ts.appendAssistantTranscriptTruncatedAs("", "The three sentences are above, and", "cancelled")
	ts.finishAfterExitNotice()

	require.Equal(t, []string{"assistant", "notice", "turn_canceled"}, savedKinds(t, store, sessionID))
}

// doneBoundaryStreamer mimics pkg/gateway wsStreamer.Finalize: save the
// answer, run the before-done callback, then "publish done" — recording what a
// reload taken at that instant would read.
type doneBoundaryStreamer struct {
	ts         *turnState
	beforeDone func()
	atDone     []string
	store      *session.UnifiedStore
	sessionID  string
	t          *testing.T
}

func (s *doneBoundaryStreamer) Update(context.Context, string) error { return nil }
func (s *doneBoundaryStreamer) Cancel(context.Context)               {}
func (s *doneBoundaryStreamer) SetBeforeDone(fn func())              { s.beforeDone = fn }
func (s *doneBoundaryStreamer) Finalize(_ context.Context, content string) error {
	s.ts.appendAssistantTranscriptTruncatedAs("", content, "cancelled")
	if s.beforeDone != nil {
		s.beforeDone()
	}
	s.atDone = savedKinds(s.t, s.store, s.sessionID)
	return nil
}

// The streamed (webchat) path: the real finalizeStreamer hands the streamer
// the before-done write. At the done boundary a reload must already read
// partial, notice, marker, and the deferred finish adds nothing after it.
func TestStoppedTurn_StreamedPathNoticeIsSavedBeforeDone(t *testing.T) {
	t.Parallel()
	al, ts, store, sessionID := stoppedTurnFixture(t)
	st := &doneBoundaryStreamer{ts: ts, store: store, sessionID: sessionID, t: t}
	ts.setLastStreamer(st)
	ts.SetFinalContent("The three sentences are above, and")

	_, _, err := al.typedTurnExit(ts, 1, "stop-order-model", context.Canceled)
	require.Error(t, err)
	require.NoError(t, ts.finalizeStreamer(context.Background()))
	require.Equal(t, []string{"assistant", "notice", "turn_canceled"}, st.atDone, "what a reload at done reads")

	ts.finishAfterExitNotice()
	require.Equal(t, []string{"assistant", "notice", "turn_canceled"}, savedKinds(t, store, sessionID), "no duplicate after the deferred finish")
}

// No partial (Stop before the first token): the notice is still saved, once.
func TestStoppedTurn_NoticeSavedOnceWithoutPartial(t *testing.T) {
	t.Parallel()
	al, ts, store, sessionID := stoppedTurnFixture(t)

	_, _, err := al.typedTurnExit(ts, 1, "stop-order-model", context.Canceled)
	require.Error(t, err)
	ts.finishAfterExitNotice()
	ts.flushExitNotice() // a second flush must not write a duplicate

	require.Equal(t, []string{"notice", "turn_canceled"}, savedKinds(t, store, sessionID))
}

// A reader holding the turn lock until the forced stop (3 s later) must not
// delay the Stop: queueing the notice never waits for ts.mu. Seen on a pod as
// the notice and marker saved 3 s after the partial answer.
func TestStoppedTurn_QueueingNoticeDoesNotWaitForTurnLock(t *testing.T) {
	t.Parallel()
	al, ts, _, _ := stoppedTurnFixture(t)
	_ = al

	ts.mu.RLock()
	defer ts.mu.RUnlock()
	done := make(chan struct{})
	go func() {
		ts.queueExitNotice("error", "runTurn", LLMError{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("queueExitNotice blocked on ts.mu held by a reader")
	}
}

// Positive control for the instrument: the old direct write puts the notice
// first, and this reader sees it. If this ever stops holding, the two tests
// above could no longer tell the old order from the new one.
func TestStoppedTurn_ControlOldDirectWriteIsNoticeFirst(t *testing.T) {
	t.Parallel()
	al, ts, store, sessionID := stoppedTurnFixture(t)

	al.emitTurnErrorFrame(ts, ts.eventMeta("runTurn", "turn.error"), "llm", "runTurn", TranslateTurnError(context.Canceled))
	ts.appendAssistantTranscriptTruncatedAs("", "partial", "cancelled")
	ts.finishAfterExitNotice()

	require.Equal(t, []string{"notice", "assistant", "turn_canceled"}, savedKinds(t, store, sessionID))
}
