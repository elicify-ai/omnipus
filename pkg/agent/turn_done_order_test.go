package agent

// turn_done_order_test.go — SV-19i guard (r5, S-16; #1428 step 2 option C,
// qa-lead). Spec §6.1: "A real `runTurn` whose `done` publication is observed
// by a hook: at the moment `done` is published, ActiveForegroundTurnInfo(
// session) returns ok=false. This guards the precondition F-rr; it is the
// test MU-37 must turn red."
//
// ORACLE (from the spec, not the implementation): the observation happens
// INSIDE the hook that publishes done — the bus StreamDelegate's Streamer.
// Finalize — which is exactly what runTurn's finalizeStreamer calls after
// clearActiveTurn has already run (loop.go's LIFO defer chain: clearActiveTurn
// is registered after finalizeStreamer, so it fires before it). If a mutation
// reorders those defers (clearActiveTurn after finalizeStreamer) or drops the
// liveness gate, the observation flips to ok=true and this test dies.
//
// RED/GUARD note: this test is GREEN by design today — it pins existing
// behaviour the spec names a mutation target for. Its failability proof is
// CHECK's mutation run (MU-37), not a RED run; the plan records it as GUARD.
//
// Fixture discipline: the turn is REAL (newGoalLoopTestLoop + processMessage,
// the same shape fallback_live_turn_test.go uses); the provider streams
// (StreamingProvider) so finalizeStreamer has a streamer to finalize; the
// stream delegate is the process edge, not a mock of anything under test.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// doneOrderProvider always streams: the Chat path is never taken.
type doneOrderProvider struct{}

func (p *doneOrderProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	return nil, errors.New("doneOrderProvider always streams")
}

func (p *doneOrderProvider) GetDefaultModel() string { return "done-order-provider" }

func (p *doneOrderProvider) ChatStream(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, onChunk func(accumulated string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	onChunk("done-order answer")
	return &providers.LLMResponse{Content: "done-order answer"}, nil
}

var _ providers.StreamingProvider = (*doneOrderProvider)(nil)

// doneOrderProbe is the observation the spec describes: ActiveForegroundTurnInfo
// sampled at the done publication itself.
type doneOrderProbe struct {
	mu    sync.Mutex
	oks   []bool
	first string // the first session_id the observation ran for (diagnostics)
}

func (p *doneOrderProbe) record(sessionID string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.oks) == 0 {
		p.first = sessionID
	}
	p.oks = append(p.oks, ok)
}

func (p *doneOrderProbe) results() (sessionID string, oks []bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.first, append([]bool(nil), p.oks...)
}

// doneOrderStreamer publishes the answer through and samples the active-turn
// table inside Finalize — the exact moment runTurn hands out "done".
type doneOrderStreamer struct {
	al        *AgentLoop
	probe     *doneOrderProbe
	sessionID string
}

func (s *doneOrderStreamer) Update(_ context.Context, _ string) error { return nil }
func (s *doneOrderStreamer) Cancel(_ context.Context)                 {}
func (s *doneOrderStreamer) Finalize(_ context.Context, _ string) error {
	_, _, _, ok := s.al.ActiveForegroundTurnInfo(s.sessionID)
	s.probe.record(s.sessionID, ok)
	return nil
}

// doneOrderDelegate is the bus StreamDelegate: the process edge every
// streaming channel sits behind (production: the channel Manager).
type doneOrderDelegate struct {
	al    *AgentLoop
	probe *doneOrderProbe
}

func (d *doneOrderDelegate) GetStreamer(_ context.Context, _, _, sessionID string) (bus.Streamer, bool) {
	return &doneOrderStreamer{al: d.al, probe: d.probe, sessionID: sessionID}, true
}

// TestTurnDoneOrder_DonePublicationSeesNoActiveForegroundTurn is SV-19i: a
// real runTurn whose done publication is observed by a hook; at the moment
// done is published, ActiveForegroundTurnInfo(session) returns ok=false.
func TestTurnDoneOrder_DonePublicationSeesNoActiveForegroundTurn(t *testing.T) {
	provider := &doneOrderProvider{}
	al, _ := newGoalLoopTestLoop(t, provider, func(cfg *config.Config) {
		// Ordinary chat addresses a colleague, never a delegation-only worker.
		cfg.Agents.List[0].Type = config.AgentTypeCustom
	})
	installTestSessionLauncher(t, al)

	probe := &doneOrderProbe{}
	al.bus.SetStreamDelegate(&doneOrderDelegate{al: al, probe: probe})
	t.Cleanup(func() { al.bus.SetStreamDelegate(nil) })

	agentID := "native-agent"
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	require.NoError(t, err)
	key := fmt.Sprintf("agent:%s:session:%s", agentID, meta.ID)

	// The gateway saves the user entry before submitting this inbound message;
	// do the same, without constructing a turnState by hand.
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
		ID: "done-order-input", Role: "user", Content: "finish in one short answer",
		AgentID: agentID, Timestamp: time.Now().UTC(),
	}))
	_, _, err = al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: "done-order", SessionID: meta.ID, SessionKey: key,
		Content: "finish in one short answer", GatewayUserID: "done-order-user", OperatorPrompt: true,
		Metadata: map[string]string{"agent_id": agentID},
	})
	require.NoError(t, err, "the real streamed turn must complete")

	// The hook must have fired (the turn streamed through the probe streamer).
	sessionID, oks := probe.results()
	require.NotEmpty(t, oks,
		"instrument: the done-publication hook never fired — the turn did not stream through the probe streamer, "+
			"so this run proves nothing about the ordering")
	require.Equal(t, meta.ID, sessionID,
		"instrument: the observation must run for the turn's own transcript session")

	// SV-19i: at the moment done is published, there is NO active foreground
	// turn for the session — clearActiveTurn has already run (F-rr).
	for i, ok := range oks {
		require.False(t, ok,
			"SV-19i violated: done publication %d observed ActiveForegroundTurnInfo(%s) = ok=true — "+
				"the turn was still reported in flight at the moment its done frame was published "+
				"(F-rr: clearActiveTurn must precede finalizeStreamer)", i, sessionID)
	}
}
