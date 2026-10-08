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
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestSessionCoreU1_CorruptStoredMainIsRefusedAndNeverGuessed(t *testing.T) {
	cases := []struct {
		name string
		seed func(t *testing.T, env *u1Env, id string)
	}{
		{"stored pair has the wrong owner", func(t *testing.T, env *u1Env, id string) {
			_, err := env.store(t).GetOrCreateScheduledSession(id, "jim") // exact-ID create, owner jim
			require.NoError(t, err)
		}},
		{"stored pair has the wrong workspace", func(t *testing.T, env *u1Env, id string) {
			_, err := env.store(t).GetOrCreateScheduledSession(id, "mia")
			require.NoError(t, err)
			other := "some-other-workspace"
			require.NoError(t, env.store(t).SetMeta(id, session.MetaPatch{WorkspaceID: &other}))
		}},
		{"metadata unreadable", func(t *testing.T, env *u1Env, id string) {
			dir := filepath.Join(env.store(t).BaseDir(), id)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{not json"), 0o600))
		}},
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
			c.seed(t, env, badID)
			rawBefore, err := os.ReadFile(filepath.Join(env.store(t).BaseDir(), badID, "meta.json"))
			require.NoError(t, err)

			_ = env.putTeam(t, ws, "jim", "mia") // outcome (refuse vs accept) is an open question; effects are asserted

			if sess, status := env.getSession(t, badID); status == http.StatusOK {
				isValidMiaMain := sess["type"] == "main" && sess["agent_id"] == "mia" && sess["workspace_id"] == ws
				assert.False(t, isValidMiaMain, "a corrupt/mismatched record must never be served as Mia's main: %v", sess)
				assert.NotEqual(t, http.StatusOK, status, "BDD-01.4: lookup of a mismatched/corrupt stored main is a visible refusal")
			}

			rawAfter, err := os.ReadFile(filepath.Join(env.store(t).BaseDir(), badID, "meta.json"))
			require.NoError(t, err)
			assert.Equal(t, string(rawBefore), string(rawAfter), "bad record is not repaired or rewritten")

			for name, rec := range env.storedOfType(t, "main") {
				if name == u1MainID(ws, "jim") {
					continue
				}
				assert.Failf(t, "guessed/replacement identity", "unexpected stored main %q (%v)", name, rec.doc)
			}
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
