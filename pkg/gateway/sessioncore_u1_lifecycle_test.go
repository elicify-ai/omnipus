// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U1: main lifecycle over membership and heartbeat
// changes. Spec: FR-002 (protection independent of heartbeat), FR-003 (identity
// and hiding part only); BDD-01.2, BDD-01.3 (hide / re-add / retained identity).
// The "child settles with its captured recipient, no hidden-main wake" half of
// BDD-01.3 belongs to unit U6 and is deliberately not tested here.

package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// BDD-01.2: disabling an enabled heartbeat on the pinned/protected main leaves
// the SAME main, still protected and navigable, with no replacement and no
// stored heartbeat address.
func TestSessionCoreU1_DisablingHeartbeatKeepsTheSameProtectedMain(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JU1HBWS000000000000000A"
	env.seedWorkspace(t, ws, false, "jim")
	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim", "mia").Code, "control: add Mia")
	mainID := u1MainID(ws, "mia")

	assertSameMain := func(step string) {
		t.Helper()
		sess, code := env.getSession(t, mainID)
		require.Equal(t, http.StatusOK, code, "%s: main navigable", step)
		assert.Equal(t, "main", sess["type"], "%s", step)
		assert.Equal(t, true, sess["protected"], "%s: protected regardless of heartbeat state", step)
		assert.Equal(t, http.StatusConflict, env.deleteSession(t, mainID), "%s: protected main refuses delete (409)", step)
		assert.Equal(t, []string{u1MainID(ws, "jim"), mainID}, u1Sorted(env.storedOfType(t, "main")),
			"%s: no replacement main", step)
		assert.Empty(t, env.storedOfType(t, "heartbeat"), "%s: no stored heartbeat session address", step)
		mc := u1MemberConfig(env.getWorkspace(t, ws), "mia")
		require.NotNil(t, mc, "%s", step)
		assert.Equal(t, mainID, mc["main_session_id"], "%s", step)
		if hb, _ := mc["heartbeat"].(map[string]any); hb != nil {
			_, has := hb["session_id"]
			assert.False(t, has, "%s: C-MAIN deletes heartbeat.session_id from the wire", step)
		}
	}

	assertSameMain("heartbeat off")

	hb := func(enabled bool) map[string]any {
		return map[string]any{"member_configs": map[string]any{"mia": map[string]any{
			"heartbeat": map[string]any{"enabled": enabled, "interval_minutes": 10, "body": "Check tasks."},
		}}}
	}
	require.Equal(t, http.StatusOK, env.put(t, ws, hb(true)).Code, "control: enable heartbeat")
	assertSameMain("heartbeat enabled")

	require.Equal(t, http.StatusOK, env.put(t, ws, hb(false)).Code, "control: disable heartbeat")
	assertSameMain("heartbeat disabled again")
}

// BDD-01.3 (identity / hide / re-add): removing the member hides the main from
// the session list and keeps the stored identity untouched; re-adding the same
// pair reveals the SAME retained identity, with no replacement.
func TestSessionCoreU1_RemovedMemberMainIsHiddenAndReAddRevealsTheSameRetainedID(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JU1REMOVEWS00000000000B"
	env.seedWorkspace(t, ws, false, "jim")
	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim", "mia").Code, "control: add Mia")
	mainID := u1MainID(ws, "mia")

	before, code := env.getSession(t, mainID)
	require.Equal(t, http.StatusOK, code, "main exists while Mia is a member")
	require.NotNil(t, u1Row(env.listSessions(t, ""), mainID), "visible while a member")
	stored := env.storedOfType(t, "main")[mainID]
	require.NotNil(t, stored.doc, "persisted")
	metaPath := filepath.Join(stored.dir, "meta.json")
	rawBefore, err := os.ReadFile(metaPath)
	require.NoError(t, err)

	// Remove Mia from the team.
	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim").Code, "control: remove Mia")

	assert.Nil(t, u1Row(env.listSessions(t, ""), mainID), "FR-003: hidden from the UI list after removal")
	assert.Nil(t, u1Row(env.listSessions(t, "flat=true"), mainID), "FR-003: hidden from the flat list too")
	if mc := u1MemberConfig(env.getWorkspace(t, ws), "mia"); mc != nil {
		assert.Empty(t, mc["main_session_id"], "no main address exposed for a non-member")
	}
	retained, ok := env.storedOfType(t, "main")[mainID]
	require.True(t, ok, "FR-003: identity retained on disk until normal retention, not deleted")
	rawMid, err := os.ReadFile(filepath.Join(retained.dir, "meta.json"))
	require.NoError(t, err)
	assert.Equal(t, string(rawBefore), string(rawMid), "retained identity is untouched by removal")

	// Re-add the same pair.
	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim", "mia").Code, "control: re-add Mia")
	after, code := env.getSession(t, mainID)
	require.Equal(t, http.StatusOK, code, "re-add reveals the retained identity")
	assert.Equal(t, before["id"], after["id"])
	assert.Equal(t, before["created_at"], after["created_at"], "same identity, not a replacement")
	assert.NotNil(t, u1Row(env.listSessions(t, ""), mainID), "visible again")
	assert.Equal(t, []string{u1MainID(ws, "jim"), mainID}, u1Sorted(env.storedOfType(t, "main")),
		"exactly one main per member pair after re-add; no replacement")
}

// C-MAIN immutability over the public store seam: a main's workspace tag cannot
// be re-pointed (agent_id has no patch field at all).
func TestSessionCoreU1_MainWorkspaceTagCannotBeRepointed(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JU1IMMUTWS00000000000C"
	env.seedWorkspace(t, ws, false, "jim")
	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim", "mia").Code, "control: add Mia")
	mainID := u1MainID(ws, "mia")
	_, code := env.getSession(t, mainID)
	require.Equal(t, http.StatusOK, code, "precondition: the main exists, so a SetMeta error below is about immutability, not absence")

	other := "01JU1OTHERTARGET0000000D"
	err := env.store(t).SetMeta(mainID, session.MetaPatch{WorkspaceID: &other})
	require.Error(t, err, "C-MAIN: Session.workspace_id is immutable")
	got, gerr := env.store(t).GetMeta(mainID)
	require.NoError(t, gerr)
	assert.Equal(t, ws, got.WorkspaceID)
	assert.Equal(t, "mia", got.AgentID)
}
