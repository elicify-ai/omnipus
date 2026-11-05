// stop_continuation_prefix_red2_test.go: #1534 RED round 2 — Q1 = A parity
// half for an auto-continued (ADR-087 D6) answer interrupted by a Stop: the
// already-shown prefix of the CURRENT continuation round must be saved
// together with the earlier rounds' accumulated text. Today the errored
// round's shown prefix is LOST: preserveTruncatedAccumulator
// (loop_truncation.go) sets the turn's final content to the accumulated
// text alone, and finalizeStreamer hands the streamer exactly that
// (turn_stream.go::finalizeStreamer → SetContinuationContent), so the
// gateway persists "A" while the screen showed "AB" (CHECK finding F3).
//
// The seam pinned here is the pkg/agent half of that handoff: the value
// finalizeStreamer hands the streamer must be accumulated + this round's
// shown prefix. The end-to-end form (real wsStreamer, reload) lives in
// pkg/gateway/chat_history_1534_stop_body_parity_test.go.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// stopContinuationStep is one scripted streaming response for
// stopContinuationStreamProvider.
type stopContinuationStep struct {
	stream       string // chunk delivered via onChunk before any waiting
	waitClaim    bool   // if true: signal `claim`, then block until `release` before returning
	content      string // response Content returned
	finishReason string
	err          error
}

// stopContinuationStreamProvider drives the real streaming call site
// (providers.StreamingProvider, engaged because the test installs a bus
// stream delegate) with a two-round truncated-continuation script. Round 2
// streams its prefix, then waits until the test has claimed the Stop through
// the real seam, and only then ends cancelled — the claim strictly precedes
// the round's completion.
type stopContinuationStreamProvider struct {
	mu      sync.Mutex
	steps   []stopContinuationStep
	calls   int
	claim   chan struct{}
	release chan struct{}
}

func (p *stopContinuationStreamProvider) next() (stopContinuationStep, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	var step stopContinuationStep
	have := idx < len(p.steps)
	if have {
		step = p.steps[idx]
	}
	p.mu.Unlock()
	if !have {
		return stopContinuationStep{}, context.Canceled
	}
	return step, nil
}

func (p *stopContinuationStreamProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	// The delegate makes the streaming branch the live one; a Chat call means
	// the test is not driving the path it claims to.
	return nil, context.Canceled
}

func (p *stopContinuationStreamProvider) ChatStream(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
	onChunk func(accumulated string), _ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	step, err := p.next()
	if err != nil {
		return nil, err
	}
	if step.stream != "" {
		onChunk(step.stream)
	}
	if step.waitClaim {
		close(p.claim)
		<-p.release
	}
	if step.err != nil {
		return nil, step.err
	}
	return &providers.LLMResponse{Content: step.content, FinishReason: step.finishReason}, nil
}

func (p *stopContinuationStreamProvider) GetDefaultModel() string { return "stop-continuation-model" }

func (p *stopContinuationStreamProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

var _ providers.StreamingProvider = (*stopContinuationStreamProvider)(nil)

// stopContinuationDelegate hands the loop the recording streamer, mirroring
// turn_stream_identity_stamp_wiring_test.go's identityStampStreamDelegate.
type stopContinuationDelegate struct {
	streamer bus.Streamer
}

func (d *stopContinuationDelegate) GetStreamer(_ context.Context, _, _, _ string) (bus.Streamer, bool) {
	return d.streamer, true
}

var _ bus.StreamDelegate = (*stopContinuationDelegate)(nil)

// stopContinuationRecordingStreamer observes the transport handoff, not
// persistence. The real wsStreamer/store/reload oracle is the D16 test.
// Retaining Update arguments proves both round prefixes reached this edge
// before the Stop; a no-op mock could not establish the "already shown" setup.
type stopContinuationRecordingStreamer struct {
	continuationCapturingStreamer
	updates []string
}

func (s *stopContinuationRecordingStreamer) Update(_ context.Context, content string) error {
	s.updates = append(s.updates, content)
	return nil
}

// newStopContinuationHarness mirrors newTruncationTestHarness (real loop,
// custom agent, real store) and additionally returns the bus so the test can
// install the stream delegate before the turn starts.
func newStopContinuationHarness(
	t *testing.T,
	provider providers.LLMProvider,
) (al *AgentLoop, msgBus *bus.MessageBus, agentInst *AgentInstance, store *session.UnifiedStore, sessionID string) {
	t.Helper()
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const agentID = "stop-continuation-agent"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "stop-continuation-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: agentID, Name: "Stop Continuation Agent", Type: config.AgentTypeCustom},
			},
		},
	}

	msgBus = bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al = mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	var ok bool
	agentInst, ok = al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "SETUP: custom agent must be registered")

	store = al.GetSessionStore()
	require.NotNil(t, store, "SETUP: shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", agentID)
	require.NoError(t, err)
	sessionID = meta.ID
	return al, msgBus, agentInst, store, sessionID
}

// TestStopContinuation_CanceledRoundKeepsShownPrefix pins the parity rule
// for an interrupted auto-continuation: round 1 contributes "A-SHOWN"
// (truncated, D6 arms); round 2 streams "B-SHOWN" — the screen shows
// "A-SHOWNB-SHOWN" — then the user's Stop is claimed and the round ends
// cancelled. The streamer must be handed "A-SHOWNB-SHOWN" (accumulated +
// this round's shown prefix), never the accumulated text alone.
//
// RED today: preserveTruncatedAccumulator sets the turn's final content to
// continuationAccumulated() ("A-SHOWN") — the errored round's shown prefix
// never joins — and finalizeStreamer hands that value to the streamer.
//
// BDD:
//
//	Given a two-round auto-continued answer whose second round has already
//	  streamed its prefix when the user presses Stop,
//	When the continuation round ends cancelled because of that Stop,
//	Then the streamer receives exactly the accumulated text plus the shown
//	  prefix of the cancelled round, so a reload shows what the screen showed.
func TestStopContinuation_CanceledRoundKeepsShownPrefix(t *testing.T) {
	const round1 = "A-SHOWN"
	const round2Shown = "B-SHOWN"

	provider := &stopContinuationStreamProvider{
		claim:   make(chan struct{}),
		release: make(chan struct{}),
	}
	provider.steps = []stopContinuationStep{
		{stream: round1, content: round1, finishReason: "truncated"},
		{stream: round2Shown, waitClaim: true, err: context.Canceled},
	}

	al, msgBus, inst, store, sessionID := newStopContinuationHarness(t, provider)
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(provider.release) }) }
	t.Cleanup(letGo)

	cs := &stopContinuationRecordingStreamer{}
	msgBus.SetStreamDelegate(&stopContinuationDelegate{streamer: cs})

	type result struct {
		answer string
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
			SessionKey:          "stop-continuation-session",
			Channel:             "web",
			ChatID:              sessionID,
			UserMessage:         "tell me a long story",
			DefaultResponse:     defaultResponse,
			SendResponse:        false,
			TranscriptSessionID: sessionID,
			TranscriptStore:     store,
		})
		resultCh <- result{answer: answer, err: err}
	}()

	select {
	case <-provider.claim:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for the continuation round to stream its prefix")
	}
	fired, _, err := al.StopSessionTurn(context.Background(), sessionID, "human-red2", "web")
	require.NoError(t, err, "StopSessionTurn must accept the live turn")
	require.True(t, fired, "the Stop must fire against the live turn")
	letGo()

	select {
	case res := <-resultCh:
		// Today's user-Stop exit surfaces the turn_canceled error with the
		// (unchanged) interrupted UX; whether the turn returns it is not this
		// round's rule — the saved-body pin below is. No assertion on res.err
		// here: pinning today's aborted exit would over-constrain the fix.
		_ = res
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for the stopped turn to end")
	}

	assert.Equal(t, 2, provider.CallCount(),
		"the truncated round and its continuation happened; the claimed Stop must not allow a third call")
	require.Equal(t, []string{round1, round2Shown}, cs.updates,
		"both prefixes must actually reach the transport edge before Stop; the fixture must not only return a final value")
	require.Equal(t, 1, cs.setCalls,
		"finalizeStreamer must hand the streamer the continuation answer exactly once")
	assert.Equal(t, round1+round2Shown, cs.lastFull,
		"the shown prefix of the cancelled continuation round must survive next to the accumulated text "+
			"(the reload must show what the screen showed); got %q, want %q", cs.lastFull, round1+round2Shown)
}
