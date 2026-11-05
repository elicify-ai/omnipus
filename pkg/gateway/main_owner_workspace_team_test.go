package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// F3: a workspace create (handleWorkspacePost) or team update
// (reconcileSavedWorkspace after handleWorkspacePut) creates the team members'
// mains on behalf of the signed-in person, so those mains are owned by that
// person, not by the operator.
//
// Oracle (founder decision 2026-10-28; config.Config.MainOwnerFor doc comment):
// the owner is the acting person's username. When the actor is the machine
// principal "cli" (not a person) the owner falls back to the operator, the
// first configured person account, here "op". Config users are [op, bob], so a
// wrong "pass no actor" wiring would stamp "op" for bob's request.

func ownerWorkspaceAccounts() []config.UserConfig {
	return []config.UserConfig{{Username: "op"}, {Username: "bob"}}
}

// ownerWorkspaceRequest drives the real workspace handlers as actor.
func ownerWorkspaceRequest(t *testing.T, env *u1Env, method, path, actor string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: actor}))
	w := httptest.NewRecorder()
	env.api.HandleWorkspaces(w, r)
	return w
}

func TestOwnerWiringF3_WorkspaceCreateAndUpdateStampTheActingPerson(t *testing.T) {
	for _, tc := range []struct {
		name, actor, want string
	}{
		{name: "person_bob_owns_not_operator", actor: "bob", want: "bob"},
		{name: "machine_principal_falls_back_to_operator", actor: config.MachinePrincipalUsername, want: "op"},
	} {
		t.Run("create/"+tc.name, func(t *testing.T) {
			env := u1NewEnv(t, false)
			env.api.agentLoop.GetConfig().Gateway.Users = ownerWorkspaceAccounts()

			w := ownerWorkspaceRequest(t, env, http.MethodPost, "/api/v1/workspaces", tc.actor,
				map[string]any{"name": "Owner create", "core_team": []string{"mia"}})
			require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
			var created struct {
				ID string `json:"id"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
			require.NotEmpty(t, created.ID)

			persisted := ownerWiringFreshMeta(t, env.store(t), u1MainID(created.ID, "mia"))
			require.Equal(t, tc.want, persisted.Owner)
		})
		t.Run("update/"+tc.name, func(t *testing.T) {
			env := u1NewEnv(t, false)
			env.api.agentLoop.GetConfig().Gateway.Users = ownerWorkspaceAccounts()
			env.seedWorkspace(t, "ws-owner", false, "jim")
			state, err := workspace.ReadState(env.home, "ws-owner")
			require.NoError(t, err)

			w := ownerWorkspaceRequest(t, env, http.MethodPut, "/api/v1/workspaces/ws-owner", tc.actor,
				map[string]any{"revision": state.Revision, "core_team": []string{"jim", "mia"}})
			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

			// Both members' mains are created by this one team update.
			for _, agentID := range []string{"jim", "mia"} {
				persisted := ownerWiringFreshMeta(t, env.store(t), u1MainID("ws-owner", agentID))
				require.Equal(t, tc.want, persisted.Owner, "main of %s", agentID)
			}
		})
	}
}
