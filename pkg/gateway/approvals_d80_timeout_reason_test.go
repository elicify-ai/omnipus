// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for UAT 2026-09-13 D-80: an approval that expired
// unanswered reached the agent as `"reason":"user"` ("The user denied this
// tool call") — a human blamed for a decision nobody made. The lane's own
// evidence was contaminated by D-90 (pressing Close on the modal posted a
// deny, which IS reason "user"); this file pins the paths that must hold now
// that Close no longer decides anything:
//
//  1. The registry's expiry outcome is "timeout", and a deny that arrives
//     AFTER expiry (a late click, a throttled tab's stale countdown) is
//     reported gone (410) and never re-delivers or rewrites the outcome.
//  2. The agent-facing classification of "timeout" names a timeout —
//     nobody answered — and never the user.
package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

func TestApprovalRegistry_D80_LateDenyAfterExpiryDoesNotBecomeUserDenial(t *testing.T) {
	reg := newApprovalRegistryV2(64, 50*time.Millisecond)
	reg.terminalRetention = time.Minute // keep the entry so the late deny finds it

	e, accepted := reg.requestApproval(
		"tc-d80", "knowledge_edit",
		map[string]any{"op": "set_property"},
		"agent-d80", "sess-d80", "turn-d80",
	)
	require.True(t, accepted)

	var outcome ApprovalOutcome
	select {
	case outcome = <-e.resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout outcome not delivered within 2 s")
	}
	assert.False(t, outcome.Approved)
	assert.Equal(t, "timeout", outcome.Reason, "an unanswered approval is a timeout")

	// A deny that lands after expiry is a stale click, not a decision.
	ok, gone := reg.resolve(e.ApprovalID, ApprovalActionDeny, false)
	assert.False(t, ok, "a terminal entry must not transition again")
	assert.True(t, gone, "the HTTP layer must answer 410, not 200")

	select {
	case second := <-e.resultCh:
		t.Fatalf("a second outcome %+v was delivered — the agent would now see reason %q", second, second.Reason)
	case <-time.After(100 * time.Millisecond):
	}

	reg.mu.Lock()
	state := reg.entries[e.ApprovalID].state
	reg.mu.Unlock()
	assert.Equal(t, ApprovalStateDeniedTimeout, state, "the recorded state stays denied_timeout, never denied_user")
}

func TestClassifyDenial_D80_TimeoutIsNotAUserDecision(t *testing.T) {
	cls, known := agent.ClassifyDenial("timeout")
	require.True(t, known)
	assert.Equal(t, "timeout", cls.Reason)
	assert.True(t, cls.Permanent)
	assert.Contains(t, cls.ModelMessage, "expired with no answer")
	assert.NotContains(t, cls.ModelMessage, "user denied", "a timeout must never be attributed to the user")

	user, known := agent.ClassifyDenial("user")
	require.True(t, known)
	assert.NotEqual(t, user.ModelMessage, cls.ModelMessage, "the two reasons must read differently to the agent")
}
