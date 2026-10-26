package agent

// approval_stop_test.go reproduces the real-browser E2E finding (T24 B05 /
// T35 approval-Stop): pressing Stop while a tool approval is PENDING must end
// the server turn "stopped" — the pending-QUESTION variant already passes.
// The turn parks inside the interactive approval gate (CheckGrantOrRequestApproval
// -> PolicyApprover.RequestApproval, ctx-honouring exactly like the gateway's
// policyApproverAdapter), and the real Stop path (RequestCancel, polite then
// forced) must end that parked turn.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// approvalStopProvider scripts exactly one tool call; the turn is expected
// to be stopped while that call's approval is pending, so no second step.
type approvalStopProvider struct{}

func (p *approvalStopProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	fc := providers.FunctionCall{Name: "approval_stop_test_tool", Arguments: "{}"}
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{ID: "call_1", Type: "function", Name: "approval_stop_test_tool", Function: &fc}},
	}, nil
}

func (p *approvalStopProvider) GetDefaultModel() string { return "approval-stop-mock" }

// parkingApprover mirrors the gateway adapter's wait: block until released
// or ctx done, and record which branch fired.
type parkingApprover struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	ctxDone bool
}

func (p *parkingApprover) RequestApproval(ctx context.Context, _ PolicyApprovalReq) (bool, string, bool) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
		return false, "denied by test", false
	case <-ctx.Done():
		p.mu.Lock()
		p.ctxDone = true
		p.mu.Unlock()
		return false, "cancel", false
	}
}

func TestStop_WhileApprovalPending_EndsTheTurnStopped(t *testing.T) {
	tmpDir := t.TempDir()
	const testAgentID = "mia"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testAgentID, Name: "Mia"}},
		},
		// The ask-policy ceiling entry must exist at BOTH layers (the d16
		// harness pattern): with no ceiling entry the resolver fails closed
		// to deny before the interactive approver is ever consulted.
		Sandbox: config.OmnipusSandboxConfig{ToolPolicies: map[string]string{"approval_stop_test_tool": "ask"}},
	}

	busRef := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, busRef, &approvalStopProvider{})
	// Match gateway boot: durable Stop state requires the installed lifecycle
	// and inbox stores, plus the execution identity's genuine boot epoch.
	installTestSessionLauncher(t, al)
	t.Cleanup(al.Close)

	// The turn runs as a genuine user-initiated webchat turn (the gateway
	// publishes exactly this shape; ProcessScheduled would stamp headless
	// AutoDenyAsk and never reach the interactive approver).
	loopCtx, loopCancel := context.WithCancel(context.Background())
	t.Cleanup(loopCancel)
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(loopCtx) }()
	t.Cleanup(func() {
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
		}
	})

	al.RegisterTool(&interruptibleTool{name: "approval_stop_test_tool"})
	inst := al.registry.primaryChatAgentForTest()
	require.NotNil(t, inst, "expected default agent")
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"approval_stop_test_tool": "ask"},
	})

	approver := &parkingApprover{entered: make(chan struct{}), release: make(chan struct{})}
	al.SetToolApprover(approver)
	t.Cleanup(func() {
		select {
		case <-approver.release:
		default:
			close(approver.release)
		}
	})

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "test-approval-stop", testAgentID)
	require.NoError(t, err)
	sessionID := meta.ID

	events := al.SubscribeEvents(256)
	t.Cleanup(func() { al.UnsubscribeEvents(events.ID) })

	require.NoError(t, busRef.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:       "webchat",
		Sender:        bus.SenderInfo{CanonicalID: "webchat_user"},
		ChatID:        "chat1",
		Content:       "please run the ask tool",
		SessionID:     sessionID,
		UserInitiated: true,
		Metadata:      map[string]string{"agent_id": testAgentID},
	}))

	select {
	case <-approver.entered:
		// The turn is genuinely parked inside the interactive approval gate.
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the turn to park on the pending approval")
	}

	lifecycle := al.GetSessionLifecycleStore()
	require.NotNil(t, lifecycle)
	parkedRecord, parkedErr := lifecycle.Load(sessionID)
	require.NoError(t, parkedErr)
	require.NotNil(t, parkedRecord, "the approval must belong to an admitted execution")
	disposition := al.executionDispositionFor(al.executionClaimFor(parkedRecord))
	require.NotNil(t, disposition, "the parked turn must have its owning execution disposition")

	cancelOutcome, cancelErr := al.StopSession(
		context.Background(),
		StopRequest{
			SessionID: sessionID,
			By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "approval-stop-test-user"},
			Channel:   "web",
		},
	)
	require.NoError(t, cancelErr)
	require.True(t, cancelOutcome.Fired, "Stop must select the parked turn")

	// The turn must END. The real stop path is polite-then-forced (3s); a
	// parked approval waits on the turn context, so the turn must end no
	// later than the forced stage — never stay "working".
	turnEnded := false
	deadline := time.Now().Add(10 * time.Second)
	for !turnEnded && time.Now().Before(deadline) {
		select {
		case event, ok := <-events.C:
			if !ok {
				break
			}
			if event.Kind != EventKindTurnEnd {
				continue
			}
			if payload, ok := event.Payload.(TurnEndPayload); ok && payload.SessionID == sessionID {
				turnEnded = true
				t.Logf("turn ended after Stop with status %q", payload.Status)
				assert.Equal(t, TurnEndStatusAborted, payload.Status,
					"a Stop over a parked approval must end the turn aborted (stopped), not completed/error")
			}
		case <-time.After(time.Until(deadline)):
		}
	}
	require.True(t, turnEnded, "the turn stayed working after Stop while an approval was pending — REPRODUCED")
	assert.True(t, approver.ctxDoneLocked(), "the parked approval must observe the turn context cancelling")

	// turn_end describes compute, not durable settlement. The outer worker
	// still owns streamer/output tails and the Stop landing after runTurn
	// returns. Join this exact execution's existing completion barrier before
	// reading its final state; do not poll for the expected state itself.
	select {
	case <-disposition.done:
	case <-time.After(time.Until(deadline)):
		t.Fatal("the stopped turn's owning execution did not settle")
	}
	disposition.mu.Lock()
	settlementErr := disposition.result
	disposition.mu.Unlock()
	require.NoError(t, settlementErr, "the owning execution must settle Stop successfully")

	// Foreground truth: no active foreground turn may survive the Stop.
	turnID, _, _, ok := al.ActiveForegroundTurnInfo(sessionID)
	assert.False(t, ok, "no foreground turn may remain active after Stop (turn %q)", turnID)

	// Durable lifecycle truth: the session's lifecycle record must read
	// stopped after the Stop landed and the turn ended — this is the state
	// the SPA's session rows and fresh session_state frames show.
	rec, loadErr := lifecycle.Load(sessionID)
	require.NoError(t, loadErr)
	require.NotNil(t, rec)
	assert.Equal(t, session.LifecycleStopped, rec.State,
		"the server lifecycle must record stopped after Stop ends a turn parked on a pending approval")
	require.NotNil(t, rec.StopNote, "a settled Stop must retain its durable note")
	assert.Equal(t, session.StopCauseStop, rec.StopNote.Cause, "the note must record this explicit Stop")
	assert.Nil(t, rec.Stop, "the settled Stop must spend its in-flight fence")
	t.Logf("owning execution settled after Stop with lifecycle %q", rec.State)
}

func (p *parkingApprover) ctxDoneLocked() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ctxDone
}
