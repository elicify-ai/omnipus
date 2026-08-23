// Package gateway — regression coverage for the runTurn defer-ordering bug
// found via LIVE verification (not by any unit test) during the Wave 3
// fix-pass re-verification: pkg/agent/loop.go's runTurn deferred
// finalizeStreamer (which writes the assistant transcript entry via
// wsStreamer.Finalize) BEFORE ts.Finish(false) (whose onCancelFinish
// callback writes the turn_canceled entry and calls
// MarkLastEntryTruncated) — Go's LIFO defer order made Finish's callback
// run FIRST, so on a real mid-stream cancel the assistant entry did not
// exist yet when MarkLastEntryTruncated ran (silently finding nothing to
// flag) and the on-disk order was user -> turn_canceled -> assistant
// instead of user -> assistant -> turn_canceled. The frontend's replay
// correlation (which processes frames in on-disk order) then always missed
// the assistant message, logging chatTurnCanceledNoMatch on every reload
// after a mid-stream cancel — live-reproduced with a real browser before
// the fix, confirmed absent after it.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// blockingStreamingProvider streams one chunk, signals startedStreaming
// exactly once, then blocks on ctx.Done() — simulating a slow LLM response
// that is still generating when a cancel arrives, so the test can
// deterministically cancel mid-stream instead of racing a real timing window.
type blockingStreamingProvider struct {
	startedStreaming chan struct{}
	once             sync.Once
}

func (p *blockingStreamingProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (p *blockingStreamingProvider) ChatStream(
	ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
	onChunk func(accumulated string),
	_ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	onChunk("Partial response before the cancel arrives...")
	p.once.Do(func() { close(p.startedStreaming) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingStreamingProvider) GetDefaultModel() string { return "blocking-stream-model" }

var _ providers.StreamingProvider = (*blockingStreamingProvider)(nil)

// TestRunTurn_CancelMidStream_TranscriptOrderAssistantBeforeTurnCanceled
// drives a REAL mid-stream cancel through the full production path (a real
// WebSocket connection, a real streaming turn, a real RequestCancel call)
// and asserts the ON-DISK PHYSICAL ORDER of transcript.jsonl: the assistant
// entry must be written before the turn_canceled entry, and the assistant
// entry must be flagged Truncated=true.
//
// BDD:
//
//	Given a turn that is actively streaming (the provider has emitted a
//	  chunk and is now blocked, simulating an in-flight LLM call),
//	When RequestCancel cancels the session,
//	Then transcript.jsonl's assistant entry appears BEFORE its turn_canceled
//	  entry (not after), and the assistant entry is Truncated=true.
//
// Negative-test discipline: this test was confirmed to FAIL (order:
// turn_canceled before assistant, Truncated left false) against the pre-fix
// runTurn (finalizeStreamer deferred before Finish, making it execute AFTER
// Finish's callback due to Go's LIFO defer order) before the fix was
// applied — see the delivery report for the revert/confirm/restore
// transcript, and for the live-browser reproduction that surfaced this bug
// in the first place.
func TestRunTurn_CancelMidStream_TranscriptOrderAssistantBeforeTurnCanceled(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "blocking-stream-model"},
				MaxTokens:    4096,
			},
			// An explicitly registered agent. There is no implicit "main" sentinel
			// to fall back on any more (ADR-064), and handleChatMessage now
			// REFUSES a chat frame it cannot resolve an agent for rather than
			// creating a session with an empty owner -- so a config with no
			// agents produces no session_started frame at all.
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &blockingStreamingProvider{startedStreaming: make(chan struct{})}
	al := mustAgentLoop(t, cfg, msgBus, provider)

	ctx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run exited: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runDone:
		case <-time.After(cancelTestTurnStartDeadline):
			// Log-only: never fails the test. Kept on the shared budget so a
			// slow host cannot surface this line inside an unrelated failure
			// block and read like the cause — which is exactly what it did in
			// the ci-omnipus-2 @670a8c0c run, where the real failure was the
			// "token" read below timing out.
			t.Logf("agent loop Run did not exit within %v of cancel", cancelTestTurnStartDeadline)
		}
	})
	time.Sleep(20 * time.Millisecond) // let Run start reading from the bus

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	msg := wsClientFrameTestHelper{Type: "message", Content: "please write a long essay"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", cancelTestTurnStartDeadline)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID, "session_started must carry a non-empty session_id")

	// Wait for the token frame confirming streaming has actually begun —
	// the provider is now blocked on ctx.Done(), simulating an in-flight
	// generation, exactly the window a real mid-stream cancel targets.
	// This read is the one that blew its 5s budget on ci-omnipus-2 @670a8c0c
	// ("read error: i/o timeout") while the turn was merely starting slowly.
	// See cancelTestTurnStartDeadline.
	tokenFrame := readFrameOfType(t, conn, "token", cancelTestTurnStartDeadline)
	require.NotEmpty(t, tokenFrame.Content, "token frame must carry the streamed chunk")

	// Cancel through the REAL production cancel path — the same
	// RequestCancel -> ClaimCancel -> InterruptSession -> SetOnCancelFinish
	// -> Finish(false) -> onCancelFinish chain a live /stop click drives.
	outcome, cancelErr := al.RequestCancel(
		context.Background(),
		agent.CancelScope{SessionID: sessionID},
		agent.CancelCanceller{UserID: "test-user", Channel: "web"},
		agent.CancelHooks{},
	)
	require.NoError(t, cancelErr)
	require.True(t, outcome.Fired, "RequestCancel must fire for the actively-streaming turn")

	// Wait for the turn to fully finish and emit its done frame.
	doneFrame := readFrameOfType(t, conn, "done", cancelTestTurnStartDeadline)
	assert.Equal(t, sessionID, doneFrame.SessionID)

	// Brief settle window for the deferred transcript writes (finalizeStreamer
	// + Finish's onCancelFinish callback) to land on disk.
	time.Sleep(200 * time.Millisecond)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")
	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	raw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err, "transcript.jsonl must exist after the turn finishes")

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	assistantIdx, cancelIdx := -1, -1
	var assistantTruncated bool
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry session.TranscriptEntry
		require.NoErrorf(t, json.Unmarshal([]byte(line), &entry), "line %d must be valid JSON: %q", i, line)
		if entry.Role == "assistant" && assistantIdx == -1 {
			assistantIdx = i
			assistantTruncated = entry.Truncated
		}
		if entry.Type == session.EntryTypeTurnCancelled && cancelIdx == -1 {
			cancelIdx = i
		}
	}

	require.GreaterOrEqualf(t, assistantIdx, 0, "an assistant entry must exist in transcript.jsonl; lines: %v", lines)
	require.GreaterOrEqualf(t, cancelIdx, 0, "a turn_canceled entry must exist in transcript.jsonl; lines: %v", lines)
	assert.Less(t, assistantIdx, cancelIdx,
		"the assistant entry must be written BEFORE the turn_canceled entry on disk — otherwise "+
			"MarkLastEntryTruncated cannot find it to flag (it hasn't been written yet when the "+
			"cancel callback runs), and the frontend's turn_canceled -> assistant-message replay "+
			"correlation (which processes frames in this same on-disk order) always misses, since "+
			"it sees the turn_canceled frame before the assistant message it needs to correlate")
	assert.True(t, assistantTruncated,
		"the assistant entry must be flagged Truncated=true by MarkLastEntryTruncated — this only "+
			"succeeds when the entry already exists in the transcript at the moment the cancel "+
			"callback runs, i.e. when the ordering above is correct")
}
