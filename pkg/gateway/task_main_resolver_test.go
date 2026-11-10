// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Oracle: session-core D-U6.1 — the resolver says a pair has a main only when
// it is an eligible member (the same rule the REST surface applies), returns
// the COMPUTED main id, and a missing workspace or a non-member is (false, nil),
// never an error and never a guessed id.
func TestTaskMainResolver_EligibilityFollowsMembership(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const wsID = "01JXRESOLVERWS000000001"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "WS", Status: "active", CoreTeam: []string{"mia"},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})
	r := taskMainResolver{api: api}

	id, ok, err := r.EligibleMain(wsID, "mia")
	require.NoError(t, err)
	require.True(t, ok, "a core-team member owns a main")
	want, werr := session.MainSessionID(wsID, "mia")
	require.NoError(t, werr)
	require.Equal(t, want, id, "the resolver returns the computed main id")
	_, gerr := api.agentLoop.GetSessionStore().GetMeta(id)
	require.NoError(t, gerr, "the main exists as a session once resolved")

	_, ok, err = r.EligibleMain(wsID, "not-a-member")
	require.NoError(t, err)
	require.False(t, ok, "a non-member has no main")

	_, ok, err = r.EligibleMain("01JXNOSUCHWS0000000000001", "mia")
	require.NoError(t, err, "a missing workspace is 'no main', not an error")
	require.False(t, ok)
}
