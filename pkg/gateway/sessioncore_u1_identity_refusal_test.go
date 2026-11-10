// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U1: BDD-01.4 / FR-002 "validate stored pair/owner,
// refuse corrupt/mismatched identity without guessing".
//
// The spec says "visible refusal/error, zero guessed/replacement identity, no
// wrong-owner attach". It does not say WHICH surface refuses (membership write
// vs lookup), so these tests assert only what holds on every reading: the bad
// stored record is never presented as a valid main, is never rewritten, no
// replacement identity is minted, and an unrelated healthy main keeps working.

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// u1SeedStoredMain writes a VALID main-type session record directly to the
// store under id, carrying agentID/workspaceID verbatim. It is the fixture seam
// for BDD-01.4's "stored main whose owner/workspace contradicts its computed
// pair": the record must PASS GetOrCreateMainSession's type guard (type ==
// "main") so the OWNER / WORKSPACE guard is the check under test. A
// scheduled-type fixture (GetOrCreateScheduledSession) is refused on its TYPE
// first, which let the owner/workspace guards be deleted with the test still
// green (C1 CHECK survivors m22/m23, 2026-10-08).
func u1SeedStoredMain(t *testing.T, env *u1Env, id, agentID, workspaceID string) {
	t.Helper()
	dir := filepath.Join(env.store(t).BaseDir(), id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	doc := map[string]any{
		"id":           id,
		"agent_id":     agentID,
		"status":       "active",
		"created_at":   "2026-01-01T00:00:00Z",
		"updated_at":   "2026-01-01T00:00:00Z",
		"channel":      "main",
		"partitions":   []string{},
		"type":         "main",
		"workspace_id": workspaceID,
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0o600))
}

func TestSessionCoreU1_CorruptStoredMainIsRefusedAndNeverGuessed(t *testing.T) {
	cases := []struct {
		name string
		// seed lays down the bad record at id (a computed main id for (ws, mia)).
		seed func(t *testing.T, env *u1Env, ws, id string)
		// wantMains is the exact persisted main set after the PUT: the healthy
		// control plus, when the fixture is a parseable main-type record, the
		// pre-existing bad record itself (which must survive untouched).
		// DRIVEN FROM SPEC (BDD-01.4): no replacement identity may be minted, so
		// no THIRD main may ever appear.
		wantMains func(ws string) []string
	}{
		{
			"stored main has the wrong owner",
			func(t *testing.T, env *u1Env, ws, id string) {
				// Main-type record (passes the type guard) whose stored agent_id
				// disagrees with the computed pair (ws, mia): the pair's OWNER
				// guard is the check this fixture must reach.
				u1SeedStoredMain(t, env, id, "jim", ws)
			},
			func(ws string) []string { return []string{u1MainID(ws, "jim"), u1MainID(ws, "mia")} },
		},
		{
			"stored main has the wrong workspace",
			func(t *testing.T, env *u1Env, ws, id string) {
				// Main-type record (passes the type guard, agent matches) whose
				// stored workspace_id disagrees with the computed pair: the
				// WORKSPACE guard is the check this fixture must reach.
				u1SeedStoredMain(t, env, id, "mia", "some-other-workspace")
			},
			func(ws string) []string { return []string{u1MainID(ws, "jim"), u1MainID(ws, "mia")} },
		},
		{
			"metadata unreadable",
			func(t *testing.T, env *u1Env, _, id string) {
				dir := filepath.Join(env.store(t).BaseDir(), id)
				require.NoError(t, os.MkdirAll(dir, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{not json"), 0o600))
			},
			// The unparseable record is not a main on disk, so only the control
			// is counted.
			func(ws string) []string { return []string{u1MainID(ws, "jim")} },
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := u1NewEnv(t, false)
			ws := []string{"01JU1CORRUPTWS0000000000E", "01JU1CORRUPTWS0000000000F", "01JU1CORRUPTWS0000000000G"}[i]
			env.seedWorkspace(t, ws, false)

			// Positive companion: a healthy member next to the bad one.
			require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim").Code, "control: add Jim")
			_, code := env.getSession(t, u1MainID(ws, "jim"))
			require.Equal(t, http.StatusOK, code, "Jim (healthy) has a main")

			badID := u1MainID(ws, "mia")
			c.seed(t, env, ws, badID)
			rawBefore, err := os.ReadFile(filepath.Join(env.store(t).BaseDir(), badID, "meta.json"))
			require.NoError(t, err)

			_ = env.putTeam(t, ws, "jim", "mia") // outcome (refuse vs accept) is an open question; effects are asserted

			// BDD-01.4: a stored main that contradicts its computed pair is a
			// visible refusal — it is NEVER served as Mia's main. Deleting the
			// owner guard (m22) or the workspace guard (m23) in
			// GetOrCreateMainSession makes this status 200 (the mismatched record
			// is adopted and returned) and fails here.
			sess, status := env.getSession(t, badID)
			assert.NotEqual(t, http.StatusOK, status,
				"BDD-01.4: lookup of a mismatched/corrupt stored main is a visible refusal; got %v", sess)

			rawAfter, err := os.ReadFile(filepath.Join(env.store(t).BaseDir(), badID, "meta.json"))
			require.NoError(t, err)
			assert.Equal(t, string(rawBefore), string(rawAfter), "bad record is not repaired or rewritten")

			// No replacement identity is minted: the persisted main set is exactly
			// the control plus the pre-existing bad record.
			assert.Equal(t, c.wantMains(ws), u1Sorted(env.storedOfType(t, "main")),
				"no guessed/replacement identity: only the control and the pre-existing bad record")

			if mc := u1MemberConfig(env.getWorkspace(t, ws), "mia"); mc != nil {
				assert.Empty(t, mc["main_session_id"], "no main address exposed for the refused pair")
			}
			assert.Equal(t, http.StatusOK, func() int { _, c := env.getSession(t, u1MainID(ws, "jim")); return c }(),
				"the healthy main keeps working")
		})
	}
}

// BDD-01.4 / FR-002: a lookup for a non-member, or in an unknown workspace,
// refuses and never creates a main. The positive companion (Mia is a member)
// stops the negative half from passing vacuously; the negative half itself
// already holds at baseline.
func TestSessionCoreU1_LookupNeverCreatesAMainForNonMemberOrUnknownWorkspace(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JU1LOOKUPWS000000000H"
	env.seedWorkspace(t, ws, false)
	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "mia").Code, "control: only Mia is a member")
	_, code := env.getSession(t, u1MainID(ws, "mia"))
	require.Equal(t, http.StatusOK, code, "positive companion: member has a main")

	for _, id := range []string{u1MainID(ws, "jim"), u1MainID("no-such-workspace", "mia")} {
		_, code := env.getSession(t, id)
		assert.Equal(t, http.StatusNotFound, code, "%s: refused", id)
		assert.Empty(t, u1Prefixed(env.storedSessions(t), id), "%s: nothing created by a lookup", id)
	}
	assert.Equal(t, []string{u1MainID(ws, "mia")}, u1Sorted(env.storedOfType(t, "main")))
}
