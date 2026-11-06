package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestOwnerWiringI3_RESTOpeningNewMainResolvesPersonOrOperator(t *testing.T) {
	for _, tc := range []struct {
		name, actor, want string
		accounts          bool
	}{
		{name: "signed_in_person", actor: "alice", accounts: true, want: "alice"},
		{name: "machine_actor", actor: "cli", accounts: true, want: "operator"},
		{name: "system_actor", actor: "", accounts: true, want: "operator"},
		{name: "no_accounts_system_actor", actor: "", accounts: false, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := u1NewEnv(t, false)
			env.seedWorkspace(t, "default", true, "mia")
			cfg := env.api.agentLoop.GetConfig()
			if tc.accounts {
				cfg.Gateway.Users = ownerWiringAccounts()
			} else {
				cfg.Gateway.Users = []config.UserConfig{}
			}
			id := u1MainID("default", "mia")
			store := env.store(t)
			before, err := store.ListSessionsFiltered(func(meta *session.UnifiedMeta) bool { return meta.ID == id })
			require.NoError(t, err)
			require.Empty(t, before, "fixture must not already have the main before REST creates it")

			response := ownerWiringRESTGet(t, env.api, "/api/v1/sessions/"+id, tc.actor)
			require.Equal(t, http.StatusOK, response.Code, "opening must succeed through the real REST handler: %s", response.Body.String())
			persisted := ownerWiringFreshMeta(t, store, id)
			require.Equal(t, tc.want, persisted.Owner, "REST must forward the acting person, or resolve the operator for system/machine calls")
			require.Equal(t, id, persisted.ID)
			require.Equal(t, session.SessionTypeMain, persisted.Type)
			require.Equal(t, "mia", persisted.AgentID)
			require.Equal(t, "default", persisted.WorkspaceID)
		})
	}
}

func TestOwnerWiringI3_OpeningAndListingExistingMainNeverAdopt(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, "default", true, "mia")
	env.api.agentLoop.GetConfig().Gateway.Users = ownerWiringAccounts()
	store := env.store(t)
	main, err := store.GetOrCreateMainSession("default", "mia")
	require.NoError(t, err)
	require.Equal(t, "", main.Owner)
	updated := ownerWiringHistoricalRecency(t, store, main.ID)
	before := ownerWiringIdentityBytes(t, store, main.ID)

	opened := ownerWiringRESTGet(t, env.api, "/api/v1/sessions/"+main.ID, "alice")
	require.Equal(t, http.StatusOK, opened.Code, "signed-in read must really succeed: %s", opened.Body.String())
	require.Equal(t, "", ownerWiringFreshMeta(t, store, main.ID).Owner, "open is a read, not a signed-in send")
	listed := ownerWiringRESTGet(t, env.api, "/api/v1/sessions", "alice")
	require.Equal(t, http.StatusOK, listed.Code, "signed-in list must really succeed: %s", listed.Body.String())
	var page sessionPageWire
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &page))
	require.NotNil(t, u1Row(page.Sessions, main.ID), "the ownerless main must actually be listed, not filtered out")
	persisted := ownerWiringFreshMeta(t, store, main.ID)
	require.Equal(t, "", persisted.Owner, "listing must not adopt on behalf of its reader")
	require.Equal(t, updated, persisted.UpdatedAt, "read paths do not change recency")
	require.Equal(t, before, ownerWiringIdentityBytes(t, store, main.ID), "opening and listing leave the old identity untouched")
}
