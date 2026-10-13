// builtin_roster_test.go: unit coverage for the shared D8 rule both workspace
// writers (REST PUT and the update_workspace tool) enforce (founder ruling
// 2026-10-11, S1/S6).

package workspace

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefuseBuiltinRemoval(t *testing.T) {
	configured := []string{"mia", "jim", "ava", "worker", "admin", "ghost"}
	// The derived roster: coreagent.All() ∩ configured, Admin excluded.
	roster := DefaultWorkspaceTeamIDs(configured)
	require.Contains(t, roster, "mia")
	require.Contains(t, roster, "jim")
	require.Contains(t, roster, "worker")
	assert.NotContains(t, roster, "admin", "Admin is never a workspace member (ADR-090)")
	assert.NotContains(t, roster, "ghost", "unconfigured ids are never on the roster")

	// Removing a built-in from the default workspace is refused with the
	// founder-fixed message.
	err := RefuseBuiltinRemoval(true, []string{"jim", "mia"}, []string{"jim"}, configured)
	require.Error(t, err, "built-in removal from the default workspace must be refused")
	assert.Equal(t, BuiltinRemovalMessage, err.Error())

	// The same removal on a non-default workspace is allowed.
	assert.NoError(t, RefuseBuiltinRemoval(false, []string{"jim", "mia"}, []string{"jim"}, configured),
		"built-ins stay removable on non-default workspaces")

	// A default-workspace change that removes no built-in is allowed —
	// reorders, dedups, and non-built-in removals alike.
	assert.NoError(t, RefuseBuiltinRemoval(true, []string{"jim", "mia"}, []string{"mia", "jim"}, configured),
		"same-team PUT removes nobody")
	assert.NoError(t, RefuseBuiltinRemoval(true, []string{"jim", "mia", "ghost"}, []string{"jim", "mia"}, configured),
		"removing a non-built-in member from the default workspace stays allowed")
}

func TestRefuseBuiltinRemoval_EmptyRosterConfig(t *testing.T) {
	// No configured agents ⇒ empty roster ⇒ nothing can be a built-in removal.
	assert.NoError(t, RefuseBuiltinRemoval(true, []string{"mia"}, []string{}, nil),
		"a nil configured list derives an empty roster (nil-cfg safe)")
}

func TestBuiltinRemovalCode_IsStable(t *testing.T) {
	// The wire code is API surface the SPA maps — pin it.
	assert.Equal(t, "builtin_roster_default", BuiltinRemovalCode)
	assert.Equal(t, "Built-in agents can't be removed from this workspace", BuiltinRemovalMessage)
	assert.Equal(t, coreagent.IDMia, coreagent.CoreAgentID("mia"), "sanity: roster ids are lowercase core ids")
}
