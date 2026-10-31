// cancel_stop_cutoff_test.go: #1534 RED — the Stop claim must cut the turn's
// streamer off, and the tail-flush reconcile must respect that cut.
//
// Founder decision 2026-10-31 (option A): nothing delivered AFTER the server
// claimed the Stop may be saved. The production seam (diagnosis Q6 option 1)
// is pkg/agent/cancel_stop.go::claimCancel(stopOnly=true): after storing
// stopRequested, it type-asserts the optional capability
// `interface{ CutoffAtStop() }` on ts.lastStreamer — the file's established
// optional-interface pattern (stampStreamerTurnID, streamOwnershipReleaser) —
// and calls it exactly once. These tests drive the real claimCancel and the
// real callProviderOnce streaming path; the streamer is a recording probe
// that never implements the product's cutoff semantics itself.
package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cutoffRecordingStreamer is a bus.Streamer probe that records the ordered
// event sequence it observes ("update:<content>" per Update, "cutoff" per
// CutoffAtStop). It deliberately contains no cutoff semantics: the saved-body
// behaviour is pinned on the real wsStreamer in pkg/gateway; this double only
// proves WHEN pkg/agent invokes the capability.
type cutoffRecordingStreamer struct {
	mu     sync.Mutex
	events []string
	cutoff int
}

func (s *cutoffRecordingStreamer) Update(_ context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "update:"+content)
	return nil
}

func (s *cutoffRecordingStreamer) Finalize(_ context.Context, _ string) error { return nil }

func (s *cutoffRecordingStreamer) Cancel(_ context.Context) {}

// CutoffAtStop is the optional capability #1534 gives the Stop claim. It
// exists HERE (on the probe) before it exists on any production streamer so
// the seam test can pin the call site; production RED is the missing call.
func (s *cutoffRecordingStreamer) CutoffAtStop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutoff++
	s.events = append(s.events, "cutoff")
}

func (s *cutoffRecordingStreamer) cutoffCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cutoff
}

func (s *cutoffRecordingStreamer) snapshotEvents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

func (s *cutoffRecordingStreamer) indexOf(event string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.events {
		if e == event {
			return i
		}
	}
	return -1
}

// newCutoffTestTurnState builds the minimal bare turnState shape the existing
// cancel_test.go / cancel_race_test.go suites use to drive the claim paths
// without a full runTurn lifecycle (cf. cancel_prearm_test.go's fixture).
func newCutoffTestTurnState(streamer bus.Streamer) *turnState {
	return &turnState{
		turnID:       "turn-cutoff-seam",
		lastStreamer: streamer,
		finishedChan: make(chan struct{}),
	}
}

// TestClaimCancel_StopCallsCutoffAtStopExactlyOnce pins the #1534 seam: the
// first Stop claim (stopOnly=true) must invoke CutoffAtStop on the turn's
// lastStreamer exactly once, and a losing second claim must not re-invoke it.
// RED today: claimCancel stores flags only and never touches the streamer, so
// the probe records 0 calls.
func TestClaimCancel_StopCallsCutoffAtStopExactlyOnce(t *testing.T) {
	probe := &cutoffRecordingStreamer{}
	ts := newCutoffTestTurnState(probe)

	require.True(t, ts.claimCancel(true), "the first Stop claim must win (first-cancel-wins)")
	assert.Equal(t, 1, probe.cutoffCalls(),
		"claimCancel(stopOnly=true) must call CutoffAtStop exactly once on the turn's lastStreamer — #1534 cutoff seam (RED: the claim never touches the streamer today)")

	require.False(t, ts.claimCancel(true), "a second Stop claim must lose (first-cancel-wins)")
	assert.Equal(t, 1, probe.cutoffCalls(),
		"a losing second claim must not re-invoke CutoffAtStop")
}

// TestClaimCancel_AdministrativeCancelDoesNotCallCutoffAtStop pins the
// diagnosis's option-1 shape: only a user Stop (stopOnly=true) cuts the
// streamer. An administrative cancel (stopOnly=false) keeps today's
// semantics and must not invoke the capability.
func TestClaimCancel_AdministrativeCancelDoesNotCallCutoffAtStop(t *testing.T) {
	probe := &cutoffRecordingStreamer{}
	ts := newCutoffTestTurnState(probe)

	require.True(t, ts.claimCancel(false), "the first administrative cancel claim must win")
	assert.Equal(t, 0, probe.cutoffCalls(),
		"claimCancel(stopOnly=false) is not a user Stop and must not invoke CutoffAtStop")
}

// TestHardAbort_DoesNotCallCutoffAtStop is the non-regression guard the brief
// names: the hard-abort path (a stuck turn's PHASE-C detach,
// cancel_stop.go::interruptCancelTargets(hard=true) → requestHardAbort) is a
// different mechanism from the user Stop and must not invoke the capability.
func TestHardAbort_DoesNotCallCutoffAtStop(t *testing.T) {
	probe := &cutoffRecordingStreamer{}
	ts := newCutoffTestTurnState(probe)

	require.True(t, ts.requestHardAbort(), "the hard abort must fire on a fresh turn")
	assert.Equal(t, 0, probe.cutoffCalls(),
		"the hard-abort path must not invoke CutoffAtStop — the cutoff belongs to the Stop claim, not the stuck-turn detach")
	assert.True(t, ts.hardAbortRequested(), "the hard abort itself must still be recorded")
}

// cutMidStreamProvider streams one chunk, then claims the turn's Stop through
// the PRODUCTION claim path (exactly where a browser Stop click lands
// mid-stream), then returns a final response LONGER than what was streamed —
// which is what triggers the tail-flush reconcile in
// loop_run_turn_attempt.go. The provider carries no cutoff logic; it only
// places the claim at the right instant.
type cutMidStreamProvider struct {
	ts          *turnState
	stopClaimed bool
}

func (p *cutMidStreamProvider) GetDefaultModel() string { return "cutoff-model" }

func (p *cutMidStreamProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	return nil, context.Canceled // this fixture always streams
}

func (p *cutMidStreamProvider) ChatStream(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	onChunk("pre")
	p.stopClaimed = p.ts.claimCancel(true)
	return &providers.LLMResponse{Content: "preTAIL"}, nil
}

var _ providers.StreamingProvider = (*cutMidStreamProvider)(nil)

// stubStreamDelegate hands the streaming attempt the probe streamer, the way
// the gateway's StreamDelegate hands a turn its wsStreamer.
type stubStreamDelegate struct {
	streamer bus.Streamer
}

func (d *stubStreamDelegate) GetStreamer(_ context.Context, _, _, _ string) (bus.Streamer, bool) {
	return d.streamer, true
}

// TestTailFlushAfterStopClaim_CutoffPrecedesTailFlushUpdate pins the ordering
// the tail-flush reconcile depends on: when a Stop is claimed while the
// provider attempt is in flight, the streamer has been cut BEFORE the
// post-stream tail flush Update runs, and the flush itself still runs (frames
// are unchanged). RED today: the claim never signals the streamer, so no
// cutoff event exists in the probe's log.
//
// The saved-body half of this invariant is pinned on the real wsStreamer in
// pkg/gateway (websocket_streamer_cancel_parity_test.go): pkg/agent cannot
// import the gateway's streamer, and this probe deliberately implements no
// cutoff semantics of its own.
func TestTailFlushAfterStopClaim_CutoffPrecedesTailFlushUpdate(t *testing.T) {
	probe := &cutoffRecordingStreamer{}
	provider := &cutMidStreamProvider{}
	member := &AgentInstance{ID: "mia", Provider: provider}
	member.StoreProviderPool(map[string]providers.LLMProvider{"302ai": provider})
	messages := bus.NewMessageBus()
	messages.SetStreamDelegate(&stubStreamDelegate{streamer: probe})
	al := &AgentLoop{bus: messages, fallback: providers.NewFallbackChain(providers.NewCooldownTracker())}
	rt := &agentLoopRunTurn{
		al: al, ts: newTurnState(member, processOptions{}, turnEventScope{}), turnCtx: context.Background(), activeProvider: provider,
		activeCandidates: []providers.FallbackCandidate{{Provider: "302ai", Model: "primary-model"}},
		llmModel:         "primary-model",
	}
	provider.ts = rt.ts

	resp, err := rt.callProviderOnce([]providers.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err, "the streaming attempt must succeed")
	require.NotNil(t, resp, "the streaming attempt must return the provider response")
	assert.Equal(t, "preTAIL", resp.Content, "fixture: the provider's final text is longer than what streamed")
	assert.True(t, provider.stopClaimed, "fixture: the production Stop claim fired mid-stream")

	events := probe.snapshotEvents()
	assert.Contains(t, events, "update:pre", "the streamed chunk reached the streamer before the Stop")
	assert.Contains(t, events, "update:TAIL",
		"frames are unchanged by the fix: the tail-flush Update must still run after the Stop (the browser discards it)")
	assert.Equal(t, 1, probe.cutoffCalls(),
		"claimCancel(stopOnly=true) must cut the streamer before the attempt's tail flush runs — #1534 (RED: no cutoff signal today)")

	cutoffAt := probe.indexOf("cutoff")
	tailAt := probe.indexOf("update:TAIL")
	require.GreaterOrEqual(t, cutoffAt, 0, "the cutoff event must exist in the probe's log")
	require.GreaterOrEqual(t, tailAt, 0, "the tail-flush Update must exist in the probe's log")
	assert.Less(t, cutoffAt, tailAt,
		"the cutoff must be recorded before the tail-flush Update so the flush cannot re-append post-Stop text to the saved body; log=%v", events)
}
