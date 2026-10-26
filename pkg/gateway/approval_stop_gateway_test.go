package gateway

// approval_stop_gateway_test.go reproduces the real-browser E2E finding
// (T24 B05 / T35 approval-Stop) against the REAL approval stack — the
// in-process approvalRegistryV2 plus the policyApproverAdapter the gateway
// wires at boot — with a genuine webchat turn through loop.Run: the turn
// parks on a pending "ask" approval, the user presses Stop, and the turn
// must end aborted with the pending card cancelled — never left "working".

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStop_WhilePendingApproval_RealStack_EndsAbortedAndCancelsCard(t *testing.T) {
	home, cfg := historyD16Config(t)
	// The d16 tool resolves to ASK and Auto-approve is OFF, so its call parks
	// in the real registry instead of executing. Both must be set BEFORE the
	// harness consumes the config (the loop snapshots it at construction).
	cfg.Sandbox.ToolPolicies[historyD16ToolName] = "ask"
	cfg.Sandbox.AutoApprove = false
	// The d16 tool is a lazy-manifest registration; the eager manifest keeps
	// it in the offered set so the call is not refused as tool_not_offered.
	cfg.Tools.Manifest.Compressed = false
	provider := historyD16CancelProvider()
	h := newHistoryD16Harness(t, home, cfg, provider, true)

	// The REAL approval stack, wired as gateway_boot does for every turn.
	reg := newApprovalRegistryV2(64, 120*time.Second)
	h.loop.SetToolApprover(newPolicyApproverAdapter(reg, h.handler))

	// The tool must actually be REGISTERED, or exec-time policy resolution
	// fails closed to deny before the approval gate is ever reached.
	h.loop.RegisterTool(newHistoryD16Tool(false))
	if inst, ok := h.loop.GetRegistry().GetAgent("mia"); ok {
		inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{historyD16ToolName: "ask"}})
	}

	events := h.loop.SubscribeEvents(256)
	t.Cleanup(func() { h.loop.UnsubscribeEvents(events.ID) })

	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	h.send(peer, sid, "approval-stop-client", "run the d16 tool")

	// Wait until the turn is genuinely parked on a pending approval card.
	parked := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, entry := range reg.pendingApprovals() {
			if entry.SessionID == sid {
				parked = true
			}
		}
		if parked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, parked, "the turn must park on a pending approval card for this session")

	// The user presses Stop: the real stop path, polite then forced.
	cancelOutcome, cancelErr := h.loop.StopSession(context.Background(), agent.StopRequest{
		SessionID: sid,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "approval-stop-gw-test-user"},
		Channel:   "webchat",
	})
	require.NoError(t, cancelErr)
	require.True(t, cancelOutcome.Fired, "Stop must select the parked turn")

	// The turn must END aborted — never stay "working".
	turnEnded := false
	endDeadline := time.Now().Add(12 * time.Second)
	for !turnEnded && time.Now().Before(endDeadline) {
		select {
		case event, ok := <-events.C:
			if !ok {
				break
			}
			if event.Kind != agent.EventKindTurnEnd {
				continue
			}
			if payload, ok := event.Payload.(agent.TurnEndPayload); ok && payload.SessionID == sid {
				turnEnded = true
				assert.Equal(t, agent.TurnEndStatusAborted, payload.Status,
					"a Stop over a pending approval must end the turn aborted")
			}
		case <-time.After(endDeadline.Sub(time.Now())):
		}
	}
	require.True(t, turnEnded, "the turn stayed working after Stop while an approval was pending — REPRODUCED")

	// No pending approval card may survive the Stop for this session.
	remaining := 0
	for _, entry := range reg.pendingApprovals() {
		if entry.SessionID == sid {
			remaining++
		}
	}
	assert.Zero(t, remaining, "the pending approval card must be cancelled by the Stop")

	// Foreground truth: the session must not report a running turn.
	_, _, _, active := h.loop.ActiveForegroundTurnInfo(sid)
	assert.False(t, active, "no foreground turn may remain after the Stop")
}
