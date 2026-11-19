package agent

// SV-19i / S-16, spec r8: at real runTurn done publication the finished turn
// is absent from activeTurnStates AND ActiveForegroundTurnInfo returns false.
// The successful path clears/finalizes inline; an early provider-error exit
// exercises runTurn's deferred finalization instead. Swapping BOTH clear
// defers behind finalizeStreamer (MU-37) must be checked on that error exit.
// The map assertion also prevents IsAlive from hiding a late removal.
// These are existing-behaviour guards, not RED proof. Production mutation and
// GREEN proof remain deferred to the independent CHECK instance.

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
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Only the provider network edge is scripted; the turn and all cleanup are real.
type doneOrderProvider struct {
	failure     error
	observeLive func()
}

func (*doneOrderProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return nil, errors.New("doneOrderProvider always streams")
}

func (*doneOrderProvider) GetDefaultModel() string { return "done-order-provider" }

func (p *doneOrderProvider) ChatStream(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	onChunk("done-order answer")
	p.observeLive()
	if p.failure != nil {
		return nil, p.failure
	}
	return &providers.LLMResponse{Content: "done-order answer"}, nil
}

var _ providers.StreamingProvider = (*doneOrderProvider)(nil)

type doneOrderObservation struct {
	sessionID  string
	active     bool
	registered int
}

type doneOrderProbe struct {
	mu    sync.Mutex
	live  []doneOrderObservation
	dones []doneOrderObservation
}

func (p *doneOrderProbe) record(al *AgentLoop, sessionID string, atDone bool) {
	_, _, _, active := al.ActiveForegroundTurnInfo(sessionID)
	sample := doneOrderObservation{sessionID: sessionID, active: active}
	al.activeTurnStates.Range(func(_, value any) bool {
		if ts, ok := value.(*turnState); ok && ts.transcriptSessionID == sessionID {
			sample.registered++
		}
		return true
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	if atDone {
		p.dones = append(p.dones, sample)
	} else {
		p.live = append(p.live, sample)
	}
}

func (p *doneOrderProbe) results() (live, dones []doneOrderObservation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]doneOrderObservation(nil), p.live...), append([]doneOrderObservation(nil), p.dones...)
}

// The bus StreamDelegate is the publication process edge. Sample INSIDE
// Finalize, not after processMessage returns (which would miss late removal).
type doneOrderStreamer struct {
	al        *AgentLoop
	probe     *doneOrderProbe
	sessionID string
}

func (*doneOrderStreamer) Update(context.Context, string) error { return nil }
func (*doneOrderStreamer) Cancel(context.Context)               {}
func (s *doneOrderStreamer) Finalize(context.Context, string) error {
	s.probe.record(s.al, s.sessionID, true)
	return nil
}

type doneOrderDelegate struct {
	al    *AgentLoop
	probe *doneOrderProbe
}

func (d *doneOrderDelegate) GetStreamer(_ context.Context, _, _, sessionID string) (bus.Streamer, bool) {
	return &doneOrderStreamer{al: d.al, probe: d.probe, sessionID: sessionID}, true
}

func TestTurnDoneOrder_DonePublicationSeesNoActiveForegroundTurn(t *testing.T) {
	// An HTTP authentication failure cannot be repaired by another model call.
	// A terminal streamed error reaches deferred finalization, not finalizeTurn.
	failure := &common.ProviderError{Status: 401, Body: `{"error":{"message":"fixture authentication refused"}}`}
	for _, tc := range []struct {
		name    string
		failure error
	}{
		{"successful inline finalization", nil},
		{"provider error deferred finalization MU37", failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &doneOrderProvider{failure: tc.failure}
			al, _ := newGoalLoopTestLoop(t, provider, func(cfg *config.Config) {
				cfg.Agents.List[0].Type = config.AgentTypeCustom
			})
			installTestSessionLauncher(t, al)
			probe := &doneOrderProbe{}
			al.bus.SetStreamDelegate(&doneOrderDelegate{al: al, probe: probe})
			t.Cleanup(func() { al.bus.SetStreamDelegate(nil) })

			const agentID = "native-agent"
			store := al.GetSessionStore()
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
			require.NoError(t, err)
			key := fmt.Sprintf("agent:%s:session:%s", agentID, meta.ID)
			provider.observeLive = func() { probe.record(al, meta.ID, false) }
			require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
				ID: "done-order-input", Role: "user", Content: "finish in one short answer",
				AgentID: agentID, Timestamp: time.Now().UTC(),
			}))
			_, _, err = al.processMessage(context.Background(), bus.InboundMessage{
				Channel: "webchat", ChatID: "done-order", SessionID: meta.ID, SessionKey: key,
				Content: "finish in one short answer", GatewayUserID: "done-order-user", OperatorPrompt: true,
				Metadata: map[string]string{"agent_id": agentID},
			})
			if tc.failure == nil {
				require.NoError(t, err, "the real successful streamed turn must complete")
			} else {
				require.ErrorIs(t, err, tc.failure, "instrument: the real turn must take the provider-error exit")
			}

			live, dones := probe.results()
			require.NotEmpty(t, live, "instrument: provider must actually stream through the real turn")
			for _, sample := range live {
				require.True(t, sample.active, "instrument: the accessor must detect this turn while it is live")
				require.Equal(t, 1, sample.registered, "instrument: the map observer must detect this registered turn")
			}
			require.Len(t, dones, 1, "instrument: exactly one done-publication hook must fire")
			require.Equal(t, meta.ID, dones[0].sessionID, "instrument: observe the turn's own transcript session")
			require.Zero(t, dones[0].registered,
				"S-16/MU-37: done was published before the finished turn left activeTurnStates")
			require.False(t, dones[0].active,
				"SV-19i: done publication observed ActiveForegroundTurnInfo(%s) = ok=true; clearActiveTurn must precede done", meta.ID)
			t.Logf("instrument: %d live observations (registered=1, active=true); done registered=0, active=false", len(live))
		})
	}
}
