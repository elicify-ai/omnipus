// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestProcessSystemMessage_DeletedOriginAgent_IsNotRehomed is UAT E-3's
// routing half. A goal-keeper push addressed to an agent that had just been
// deleted fell back to the default agent ("named async origin agent not
// found; falling back to default agent"), which then worked — and parked — a
// goal that was never its own. A named origin that no longer resolves must be
// discarded with a visible note, and no other agent may run a turn for it.
func TestProcessSystemMessage_DeletedOriginAgent_IsNotRehomed(t *testing.T) {
	al, msgBus, _, defaultAgent := newAsyncResultTestLoop(t, &mockProvider{})
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", defaultAgent.ID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	const deletedAgentID = "4b4594d1-deleted-agent"
	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct",
		AgentID:             deletedAgentID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "goal_continue_push",
		SenderCanonicalID:   goalLoopFollowUpSenderID,
		Content:             "Keep working on the goal.",
	})
	require.Equal(t, deletedAgentID, msg.AsyncOriginAgentID, "precondition: the message names the deleted agent")

	resp, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Empty(t, resp, "no turn may run for a deleted origin agent")

	assert.Empty(t, readAssistantTranscript(t, store, meta.ID),
		"a background result for a deleted agent must not be answered by the default agent (%q)", defaultAgent.ID)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var notes int
	for _, e := range entries {
		assert.NotEqual(t, defaultAgent.ID, e.AgentID, "no transcript entry may be attributed to the default agent")
		if e.Role == "system" && strings.Contains(e.Content, deletedAgentID) && strings.Contains(e.Content, "no longer exists") {
			notes++
		}
	}
	assert.Equal(t, 1, notes, "exactly one system note must tell the user the update was discarded and why")
}
