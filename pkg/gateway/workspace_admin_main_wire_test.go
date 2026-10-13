// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// C-MAIN: Admin is not a team member, so his main id rides on the default
// Workspace as the read-only admin_main_session_id (never in member_configs).
func TestWorkspaceWire_AdminMainSessionID(t *testing.T) {
	resolver := func(ws storedWorkspace, agentID string) string {
		if agentID == "admin" {
			return "main-session-" + ws.ID + "+admin"
		}
		return ""
	}
	unresolved := func(storedWorkspace, string) string { return "" }
	base := storedWorkspace{ID: "ws1", Name: "W", Status: "active", CreatedAt: "2026-10-10T00:00:00Z", UpdatedAt: "2026-10-10T00:00:00Z"}

	t.Run("default workspace with a resolving main carries the id", func(t *testing.T) {
		ws := base
		ws.IsDefault = true
		wire := workspaceToWireFrom(t.TempDir(), ws, 0, nil, resolver)
		require.NotNil(t, wire.AdminMainSessionId)
		require.Equal(t, "main-session-ws1+admin", *wire.AdminMainSessionId)
		if wire.MemberConfigs != nil {
			require.NotContains(t, *wire.MemberConfigs, "admin", "no fake membership entry for Admin")
		}
	})
	t.Run("default workspace whose main does not resolve omits the field", func(t *testing.T) {
		ws := base
		ws.IsDefault = true
		wire := workspaceToWireFrom(t.TempDir(), ws, 0, nil, unresolved)
		require.Nil(t, wire.AdminMainSessionId)
	})
	t.Run("non-default workspace omits the field", func(t *testing.T) {
		wire := workspaceToWireFrom(t.TempDir(), base, 0, nil, resolver)
		require.Nil(t, wire.AdminMainSessionId)
	})
	t.Run("no resolver omits the field", func(t *testing.T) {
		ws := base
		ws.IsDefault = true
		wire := workspaceToWireFrom(t.TempDir(), ws, 0, nil, nil)
		require.Nil(t, wire.AdminMainSessionId)
	})
}
