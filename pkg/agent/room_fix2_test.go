package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestRoomsFix2_HandoverOwnsPrePollStops(t *testing.T) {
	for _, hard := range []bool{false, true} {
		t.Run(map[bool]string{false: "hourly_limit", true: "hard_stop"}[hard], func(t *testing.T) {
			h := newD2bRoot(t)
			h.seedState(t, session.LifecycleStopped)
			rt := &fakeRoomRuntime{allow: true}
			h.al.SetRoomRuntime(rt)
			c := addressing.Capture{RequestID: "fix2-held", ReceiverSessionID: h.id,
				Receiver: addressing.Pair{WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: testDefaultAgentID},
				Sender:   addressing.Sender{Principal: "user"}, Source: addressing.Source{Kind: addressing.SourceRoom, RoomID: "room-fix2", RoomName: "Launch"},
				AdmittedAt: time.Now().UTC(), Text: "HELD_PRE_POLL", Held: true}
			require.NoError(t, h.al.RequestLedger().Put(c))
			inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
			require.True(t, ok)
			if hard {
				h.al.SetEventSyncTap(func(e Event) {
					if e.Kind == EventKindTurnStart {
						raw, registered := h.al.activeTurnStates.Load("agent:" + testDefaultAgentID + ":session:" + h.id)
						require.True(t, registered)
						ts, isTurn := raw.(*turnState)
						require.True(t, isTurn)
						ts.requestHardAbort()
					}
				})
			} else {
				inst.AgentType = "custom"
				h.al.GetConfig().Sandbox.RateLimits.MaxAgentLLMCallsPerHour = 1
				window := h.al.rateLimiter.GetOrCreate("agent:"+testDefaultAgentID+":llm_call", 1, time.Hour, security.ScopeAgent, testDefaultAgentID, "llm_call")
				require.True(t, window.Allow().Allowed, "instrument: consume the only slot before the reviving turn")
			}
			_, _ = h.humanTurn(t, "REVIVE_WITH_HELD")
			require.Equal(t, []string{c.RequestID}, rt.ended, "ownership is installed before hard-stop/hourly-limit gates")
			require.Empty(t, h.provider.calls(), "instrument: failure was before the first provider call")
			require.Zero(t, h.al.steering.len(), "settled input must not execute through a later continuation")
			capture, found, err := h.al.RequestLedger().Get(h.id, c.RequestID)
			require.NoError(t, err)
			require.True(t, found && capture.Discarded, "settled work is not offered by a reminder or another turn")
		})
	}
}

type fix2TurnProvider struct {
	err   error
	calls int
}

func (p *fix2TurnProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return &providers.LLMResponse{Content: "done", FinishReason: "stop", Usage: &providers.UsageInfo{PromptTokens: 1, TotalTokens: 1}}, nil
}
func (*fix2TurnProvider) GetDefaultModel() string { return "test-model" }

type fix2CostRuntime struct {
	fakeRoomRuntime
	costs []RoomTurnCost
}

func (f *fix2CostRuntime) RoomTurnCost(c RoomTurnCost) { f.costs = append(f.costs, c) }

func TestRoomsFix2_ContinuationOwnsInitialInputs(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "priced", true: "failed"}[fail], func(t *testing.T) {
			f := newAddrFixture(t)
			rt := &fix2CostRuntime{fakeRoomRuntime: fakeRoomRuntime{allow: true}}
			f.al.SetRoomRuntime(rt)
			f.al.roomRequests.Store("fix2-initial", "room-1")
			p := &fix2TurnProvider{}
			if fail {
				p.err = errors.New("provider failure")
			}
			inst, ok := f.al.GetRegistry().GetAgent(addrReceiver)
			require.True(t, ok)
			inst.Provider = p
			inst.StoreProviderPool(map[string]providers.LLMProvider{})
			f.al.SetRoomCallPricer(func(string, providers.UsageInfo) float64 { return .03 })
			_, err := f.al.continueWithSteeringMessages(context.Background(), inst, "fix2-continuation", "cli", "direct", "",
				[]providers.Message{{Role: "user", Content: "room input"}}, []string{roomSteerCorrelation("fix2-initial")})
			require.Positive(t, p.calls, "instrument: actual continuation body ran")
			if fail {
				require.Error(t, err)
				require.Equal(t, []string{"fix2-initial"}, rt.ended)
			} else {
				require.NoError(t, err)
				require.Len(t, rt.costs, 1)
				require.Equal(t, "room-1", rt.costs[0].RoomID)
				require.Equal(t, .03, rt.costs[0].CostUSD)
				require.Equal(t, 1, rt.costs[0].Inputs, "initial steering is the only new input, not an extra phantom root message")
				require.Equal(t, 1, rt.costs[0].RoomInputs)
			}
		})
	}
}

func TestRoomsFix2_ContinueNeverRestoresFailedRoomInput(t *testing.T) {
	f := newAddrFixture(t)
	rt := &fenceRuntime{fakeRoomRuntime: fakeRoomRuntime{allow: true}, decision: RoomConsumeRun}
	f.al.SetRoomRuntime(rt)
	_, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission())
	require.NoError(t, err)
	wake, ok := addrDrainInbound(t, f.bus)
	require.True(t, ok)
	c, found, err := f.al.RequestLedger().Get(wake.SessionID, roomTestDelivery)
	require.NoError(t, err)
	require.True(t, found)
	f.al.deliverHeldRoomRequest(wake.SessionID, c)
	inst, ok := f.al.GetRegistry().GetAgent(addrReceiver)
	require.True(t, ok)
	inst.Provider = &fix2TurnProvider{err: errors.New("continuation provider failure")}
	inst.StoreProviderPool(map[string]providers.LLMProvider{})
	_, err = f.al.Continue(context.Background(), "agent:"+addrReceiver+":session:"+wake.SessionID, "cli", "direct", addrWS)
	require.Error(t, err)
	require.Equal(t, []string{roomTestDelivery}, rt.ended, "the consuming continuation failed its room input")
	require.Zero(t, f.al.steering.len(), "a failed continuation must not restore work it already settled")
}

func TestRoomsFix2_FailedHandoffReleasesBeforeReportingQueued(t *testing.T) {
	f := newAddrFixture(t)
	rt := &fenceRuntime{fakeRoomRuntime: fakeRoomRuntime{allow: true}, decision: RoomConsumeRun}
	f.al.SetRoomRuntime(rt)
	_, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission())
	require.NoError(t, err)
	wake, ok := addrDrainInbound(t, f.bus)
	require.True(t, ok)
	c, found, err := f.al.RequestLedger().Get(wake.SessionID, roomTestDelivery)
	require.NoError(t, err)
	require.True(t, found)
	f.al.steering.SetAggregateCap(1)
	f.al.deliverHeldRoomRequest(wake.SessionID, c)
	require.Equal(t, []string{roomTestDelivery}, rt.released, "instrument: failed aggregate handoff invokes checked undo")
	c, _, err = f.al.RequestLedger().Get(wake.SessionID, roomTestDelivery)
	require.NoError(t, err)
	require.False(t, c.Delivered)
	require.False(t, c.Discarded)
	require.Zero(t, f.al.steering.len())
	f.al.steering.SetAggregateCap(config.DefaultSMSteerAggregateBodyBytes)
	f.al.deliverHeldRoomRequest(wake.SessionID, c)
	c, _, err = f.al.RequestLedger().Get(wake.SessionID, roomTestDelivery)
	require.NoError(t, err)
	require.True(t, c.Delivered, "healthy retry hands over the same capture")
	require.Equal(t, 1, f.al.steering.len())
}
