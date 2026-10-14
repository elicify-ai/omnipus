// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainSetupOfferInbound reads one published inbound, or reports none.
func drainSetupOfferInbound(t *testing.T, b *bus.MessageBus) (bus.InboundMessage, bool) {
	t.Helper()
	select {
	case m := <-b.InboundChan():
		return m, true
	case <-time.After(300 * time.Millisecond):
		return bus.InboundMessage{}, false
	}
}

// Oracle: omnipus-system-agent FR-047 / K9 and ADR-20261011 OSA-9 W8 + OSA-9.1;
// the guard classification in scripts/check-operator-prompt-sites.sh. The
// setup offer is ONE server-generated, non-operator owner wake into Omnipus's
// main: never OperatorPrompt, never UserInitiated, never a human turn; the
// turn stays in the main's own workspace and the new workspace is target data.
func TestWorkspaceSetupOffer_PublishesOneNonOperatorOwnerWakeIntoOmnipusMain(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	t.Cleanup(cleanup)
	offer := WorkspaceSetupOffer{
		MainSessionID:     "main-default-omnipus",
		MainWorkspaceID:   "ws-default",
		TargetWorkspaceID: "ws-research",
		TargetName:        "Research",
	}

	require.NoError(t, al.PostWorkspaceSetupOffer(context.Background(), offer))

	got, ok := drainSetupOfferInbound(t, msgBus)
	require.True(t, ok, "the offer must be published")
	assert.Equal(t, "webchat", got.Channel)
	assert.Equal(t, "main-default-omnipus", got.SessionID, "the offer goes into Omnipus's existing main")
	assert.False(t, got.OperatorPrompt, "a server-generated offer is never an operator prompt")
	assert.False(t, got.UserInitiated, "a server-generated offer is never user-initiated")
	assert.Equal(t, "1", got.Metadata["owner_wake"], "the non-human owner-wake marker")
	assert.False(t, reviveInboundIsHumanTurn(got), "the offer is no person's turn")
	assert.Equal(t, "omnipus", got.Metadata["agent_id"])
	assert.Equal(t, "ws-default", got.Metadata["workspace_id"], "OSA-9.1: the main's own workspace never changes")
	assert.Equal(t, "1", got.Metadata["workspace_setup_offer"])
	assert.Equal(t, "ws-research", got.Metadata["setup_target_workspace_id"], "the new workspace is target data")
	assert.Contains(t, got.Content, `"Research"`, "the offer names the workspace, quoted")
	assert.Contains(t, got.Content, "ws-research", "the offer names the workspace id")

	_, extra := drainSetupOfferInbound(t, msgBus)
	assert.False(t, extra, "exactly one offer is published")
}

func TestWorkspaceSetupOffer_RefusesAnIncompleteOfferWithoutPublishing(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	t.Cleanup(cleanup)
	full := WorkspaceSetupOffer{MainSessionID: "m", MainWorkspaceID: "d", TargetWorkspaceID: "t", TargetName: "T"}
	for _, row := range []struct {
		name  string
		offer WorkspaceSetupOffer
	}{
		{"no_main_session", WorkspaceSetupOffer{MainWorkspaceID: full.MainWorkspaceID, TargetWorkspaceID: full.TargetWorkspaceID, TargetName: full.TargetName}},
		{"no_main_workspace", WorkspaceSetupOffer{MainSessionID: full.MainSessionID, TargetWorkspaceID: full.TargetWorkspaceID, TargetName: full.TargetName}},
		{"no_target_workspace", WorkspaceSetupOffer{MainSessionID: full.MainSessionID, MainWorkspaceID: full.MainWorkspaceID, TargetName: full.TargetName}},
		{"blank_target_workspace", WorkspaceSetupOffer{MainSessionID: full.MainSessionID, MainWorkspaceID: full.MainWorkspaceID, TargetWorkspaceID: "  ", TargetName: full.TargetName}},
	} {
		t.Run(row.name, func(t *testing.T) {
			assert.Error(t, al.PostWorkspaceSetupOffer(context.Background(), row.offer))
			_, published := drainSetupOfferInbound(t, msgBus)
			assert.False(t, published, "a refused offer publishes nothing")
		})
	}
}
