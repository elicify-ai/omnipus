// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// C-MAIN after the Admin retirement (omnipus-system-agent FR-003, FR-008):
// Omnipus is an ordinary member of the default workspace, so its main id is an
// ordinary member_configs main_session_id; the retired admin_main_session_id
// is never filled any more.
func TestWorkspaceWire_OmnipusMainIsAnOrdinaryMemberMain(t *testing.T) {
	resolver := func(ws storedWorkspace, agentID string) (string, error) {
		return "main-session-" + ws.ID + "+" + agentID, nil
	}
	base := storedWorkspace{ID: "ws1", Name: "W", Status: "active", IsDefault: true,
		CoreTeam: []string{"mia", "omnipus"}, CreatedAt: "2026-10-10T00:00:00Z", UpdatedAt: "2026-10-10T00:00:00Z"}

	wire := workspaceToWireFrom(t.TempDir(), base, 0, nil, resolver)
	require.Nil(t, wire.AdminMainSessionId, "the retired admin_main_session_id is never filled")
	require.NotNil(t, wire.MemberConfigs)
	mc, ok := (*wire.MemberConfigs)["omnipus"]
	require.True(t, ok, "Omnipus, a default-workspace member, carries an ordinary member_configs entry")
	require.NotNil(t, mc.MainSessionId)
	require.Equal(t, "main-session-ws1+omnipus", *mc.MainSessionId)
}
