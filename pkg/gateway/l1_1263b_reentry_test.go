package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// R1/R5: persisted root origin decides even when the next turn has no
// dispatcher context. Every fixture first creates and RUNS a task or plan
// through its real start entry. No test stamps WithAutoDenyAsk itself.
func TestL11263B_ReentryKeepsTaskAndPlanUnattended(t *testing.T) {
	for _, route := range []string{"board", "plan"} {
		for _, entry := range []string{"human", "system"} {
			t.Run(route+"/"+entry, func(t *testing.T) {
				initial := l11263Script(l11263Claim())
				h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: initial})
				id := h.startRoute(t, route)
				final := h.awaitTask(t, id)
				h.requireTaskOrigin(t, final.SessionID)
				probe := l11263Script(l11263Write())
				inst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Agent)
				require.True(t, ok)
				inst.Provider = probe
				h.startPump(t)
				h.reenter(t, entry, final.SessionID, l11263Agent)
				h.awaitObservedWithoutPrompt(t, probe)
				h.awaitTurnSettled(t, final.SessionID)
				h.requireNoApprovals(t)
				h.requireRefusal(t, final.SessionID, probe)
			})
		}
	}
}

// The plan dispatch already marks a child unattended today; a human writing
// into that actual child's session must NOT clear it. This is the real
// plan-chain pending-approval regression, separate from the first turn's old
// scheduled refusal wording.
func TestL11263B_PlanHelperHumanReentryNeverPrompts(t *testing.T) {
	gate := newL11263Latch()
	root := l11263Script(l11263Delegate(l11263Leaf), l11263Claim())
	root.steps[1].wait = gate.ch
	leaf := l11263Script()
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: root, l11263Leaf: leaf})
	t.Cleanup(gate.release)
	member := h.startPlan(t)
	var childID string
	require.Eventually(t, func() bool {
		_, ok := root.result("delegate-" + l11263Leaf)
		return ok
	}, 15*time.Second, 10*time.Millisecond, "real plan member must launch its helper")
	childID = l11263DelegateSession(t, root, l11263Leaf)
	h.awaitHelperTerminal(t, childID)
	gate.release()
	final := h.awaitTask(t, member.Id)
	h.requireParent(t, childID, final.SessionID)
	probe := l11263Script(l11263Write())
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Leaf)
	require.True(t, ok)
	inst.Provider = probe
	h.startPump(t)
	h.reenter(t, "human", childID, l11263Leaf)
	h.awaitObservedWithoutPrompt(t, probe)
	h.awaitTurnSettled(t, childID)
	h.requireNoApprovals(t)
	h.requireRefusal(t, childID, probe)
}

func (h *l11263Harness) startPump(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.api.agentLoop.Run(ctx) }()
	t.Cleanup(func() {
		for _, entry := range h.reg.pendingApprovals() {
			h.reg.resolve(entry.ApprovalID, ApprovalActionCancel, false)
		}
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err, "real inbound pump shutdown")
		case <-time.After(15 * time.Second):
			t.Error("real inbound pump did not join during cleanup")
		}
	})
}

func (h *l11263Harness) reenter(t *testing.T, entry, sessionID, agentID string) {
	t.Helper()
	if entry == "human" {
		// Exact production WebSocket chat intake, with the acting session id.
		// A person's message cannot relabel a task/plan root (R5).
		h.ws.handleChatMessage(context.Background(), sessionID, sessionID, "Run the approval probe again", agentID,
			nil, "", testHarnessWorkspaceMembershipID, h.tab)
		return
	}
	// Exact async-origin envelope used by the bus's system-wake path; origin
	// is resolved from the persisted session rather than an attended ctx flag.
	require.NoError(t, h.ws.msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "system", ChatID: "webchat:" + sessionID, Content: "Run the approval probe again",
		AsyncOriginAgentID: agentID, AsyncTranscriptSessionID: sessionID,
	}))
}

func (h *l11263Harness) awaitTurnSettled(t *testing.T, id string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return h.api.agentLoop.GetActiveTurnHookForSession(id) == nil
	}, 15*time.Second, 10*time.Millisecond, "a positively observed re-entered turn must really settle")
}
