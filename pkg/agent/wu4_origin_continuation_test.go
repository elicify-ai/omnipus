package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent oracle: system-agent spec §6.18 / FR-240. These are actual tool
// contexts after real queue consumption, not source assertions or card claims.
func wu4OriginRequireObservation(t *testing.T, probe *wu4OriginProbe, kind string, sameTurn bool) {
	t.Helper()
	observations, arrivalErr := probe.snapshot()
	require.NoError(t, arrivalErr, "instrument: input must reach the real checked queue")
	require.Len(t, observations, 2, "both workload tools must really execute")
	for _, observation := range observations {
		require.True(t, observation.originPresent, "instrument: the actual tool context must carry a TurnOrigin, including the web control")
		require.NotEmpty(t, observation.turnID, "instrument: execution must carry a real turn id")
	}
	require.Empty(t, observations[0].label, "instrument: original web input starts unlabelled")
	require.False(t, observations[0].channelTriggered, "instrument: original web input is not channel-triggered")
	if sameTurn {
		require.Equal(t, observations[0].turnID, observations[1].turnID, "active control must consume within the same real turn")
	} else {
		require.NotEqual(t, observations[0].turnID, observations[1].turnID, "post-turn continuation must run a fresh real turn")
	}
	wantLabel := map[string]string{
		"channel": "while answering Anna on telegram-ops", "peer": "asked by noah", "web": "",
	}[kind]
	got := observations[1]
	t.Logf("actual executed tool origin: kind=%s channel_triggered=%v label=%q", kind, got.channelTriggered, got.label)
	assert.Equal(t, kind == "channel", got.channelTriggered, "FR-240: consuming a channel person's input makes THIS new turn channel-triggered")
	assert.Equal(t, wantLabel, got.label, "§6.18: consumed server-derived person/connection or peer attribution must reach the real tool context")
}

func wu4OriginRequireInjection(t *testing.T, p *wu4OriginProvider, input bus.InboundMessage, midTurn bool) {
	t.Helper()
	requests := p.snapshot()
	wantCalls, inputCall := 4, 2 // Two real turns, each one tool call and a final reply.
	if midTurn {
		wantCalls, inputCall = 3, 1 // One turn, tool before and after consumption, then final.
	}
	require.Len(t, requests, wantCalls, "instrument: exact real provider calls, not an empty selection or fabricated turn")
	copies := 0
	for _, msg := range requests[inputCall] {
		if msg.Role == "user" && msg.Content == input.Content {
			copies++
		}
	}
	require.Equal(t, 1, copies, "the late input must actually be consumed exactly once by the observed turn")
}

func wu4OriginOrdinaryContinue(t *testing.T, kind string) {
	t.Helper()
	f, provider, probe := wu4OriginHarness(t, false)
	initial := bus.InboundMessage{Channel: "webchat", ChatID: "origin-chat", SessionID: f.root,
		Content: "P0-d original web instruction", GatewayUserID: "qa-secret-wake-owner"}
	target, err := f.al.buildContinuationTarget(initial)
	require.NoError(t, err)
	// Retain the real ordinary episode through Continue, exactly as the session
	// worker does. No fake record, alternate executor or private origin stamp.
	ctx, entry := ordinaryExecutionContext(context.Background())
	defer func() { require.NoError(t, f.al.finishExecutionDisposition(entry.disposition)) }()
	answer, _, err := f.al.processMessage(ctx, initial)
	require.NoError(t, err)
	require.Equal(t, "origin original answer", answer)
	require.Nil(t, f.al.GetActiveTurnBySession(target.SessionKey), "closing-window premise: original turn has retired before dequeue")
	input := wu4OriginInput(t, f, f.root, kind)
	require.NoError(t, f.al.enqueueSteeringFromMessage(input))
	require.Equal(t, 1, f.al.pendingSteeringCountForScope(target.SessionKey))
	require.Nil(t, f.al.GetActiveTurnBySession(target.SessionKey), "no active turn may consume/record this origin before actual Continue")
	answer, err = f.al.Continue(ctx, target.SessionKey, target.Channel, target.ChatID, target.WorkspaceID)
	require.NoError(t, err)
	require.Equal(t, "origin continuation answer", answer)
	require.Zero(t, f.al.pendingSteeringCountForScope(target.SessionKey), "actual Continue must drain the input")
	wu4OriginRequireInjection(t, provider, input, false)
	wu4OriginRequireObservation(t, probe, kind, false)
}

func wu4OriginSteeredDrain(t *testing.T, kind string) {
	t.Helper()
	f, provider, probe := wu4OriginHarness(t, false)
	child := f.launch(t, "p0-d-"+kind)
	input := wu4OriginInput(t, f, child.SessionID, kind)
	observer := &wu4OriginClosingObserver{al: f.al, childID: child.SessionID, input: input}
	classifier := NewSteerRecordClassifier(f.al.GetSessionLifecycleStore(), f.al.GetSessionStore())
	f.al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), observer, NewSteerUpwardDeliverer())
	result, err := NewSteerLauncher(f.al).Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, result.State)
	joinGoalFixtureRuns(t, f.al)
	calls, observerErr := observer.result()
	require.NoError(t, observerErr, "instrument: enqueue must happen after the original active turn retired, before the production drain")
	require.Equal(t, 1, calls, "the real post-turn closing boundary must be reached exactly once")
	require.Zero(t, f.al.pendingSteeringCountForScope(child.SessionID))
	wu4OriginRequireInjection(t, provider, input, false)
	observations, err := probe.snapshot()
	require.NoError(t, err)
	require.Len(t, observations, 2)
	for _, observation := range observations {
		require.Equal(t, child.SessionID, observation.sessionID, "instrument: tools run in the real reconstructed helper's transcript context")
	}
	wu4OriginRequireObservation(t, probe, kind, false)
}

func TestWU4P0DOrdinaryContinuePreservesChannelOrigin(t *testing.T) {
	wu4OriginOrdinaryContinue(t, "channel")
}
func TestWU4P0DOrdinaryContinuePreservesPeerOrigin(t *testing.T) {
	wu4OriginOrdinaryContinue(t, "peer")
}
func TestWU4P0DOrdinaryContinueWebOriginStaysUnlabelled(t *testing.T) {
	wu4OriginOrdinaryContinue(t, "web")
}
func TestWU4P0DSteeredDrainPreservesChannelOrigin(t *testing.T) {
	wu4OriginSteeredDrain(t, "channel")
}
func TestWU4P0DSteeredDrainPreservesPeerOrigin(t *testing.T) {
	wu4OriginSteeredDrain(t, "peer")
}
func TestWU4P0DSteeredDrainWebOriginStaysUnlabelled(t *testing.T) {
	wu4OriginSteeredDrain(t, "web")
}

func TestWU4P0DMidTurnConsumptionPreservesInputOrigin(t *testing.T) {
	for _, kind := range []string{"channel", "peer", "web"} {
		t.Run(kind, func(t *testing.T) {
			f, provider, probe := wu4OriginHarness(t, true)
			initial := bus.InboundMessage{Channel: "webchat", ChatID: "origin-chat", SessionID: f.root,
				Content: "P0-d original web instruction", GatewayUserID: "qa-secret-wake-owner"}
			target, err := f.al.buildContinuationTarget(initial)
			require.NoError(t, err)
			input := wu4OriginInput(t, f, f.root, kind)
			probe.afterFirst = func() error {
				if f.al.GetActiveTurnBySession(target.SessionKey) == nil {
					return fmt.Errorf("instrument: mid-turn arrival has no real active turn")
				}
				return f.al.enqueueSteeringFromMessage(input)
			}
			answer, _, err := f.al.processMessage(context.Background(), initial)
			require.NoError(t, err)
			require.Equal(t, "origin continuation answer", answer)
			require.Zero(t, f.al.pendingSteeringCountForScope(target.SessionKey))
			wu4OriginRequireInjection(t, provider, input, true)
			wu4OriginRequireObservation(t, probe, kind, true)
		})
	}
}
