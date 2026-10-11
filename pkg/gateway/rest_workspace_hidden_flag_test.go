// rest_workspace_hidden_flag_test.go: D5 "Hide from sidebar" + D8 built-in
// roster removal guard, over the real PUT /workspaces/{id} handler
// (ADR-hide-from-sidebar; DECISIONS.md D5/D8 of squad nav-agent-row-menu-20261011).
//
// Every expectation here is derived from the ADR/DECISIONS spec text, never
// from the implementation: assertions go through the wire JSON (PUT/GET
// bodies) and the raw on-disk record JSON, so they stay honest oracles for
// both sides of the mapping.

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hiddenFlagSeedWorkspace writes a workspace record directly, with an optional
// raw member_configs JSON (what an existing install's file already carries —
// the flag must round-trip through plain record storage, no migration).
func hiddenFlagSeedWorkspace(t *testing.T, home, id string, isDefault bool, team []string, memberConfigs json.RawMessage) {
	t.Helper()
	rec := map[string]any{
		"id": id, "name": "WS " + id, "status": "active",
		"is_default": isDefault, "core_team": team,
		"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
	}
	if memberConfigs != nil {
		rec["member_configs"] = memberConfigs
	}
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", id+".json"), data, 0o600))
}

// hiddenFlagWorkspaceDiskJSON reads the persisted workspace record as generic
// JSON (no typed struct: the assertion must not lean on the Go field existing).
func hiddenFlagWorkspaceDiskJSON(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(api.homePath, "workspaces", id+".json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// hiddenFlagPutRaw PUTs a raw JSON body (used where the test must control the
// revision verbatim, e.g. the stale-revision conflict case).
func hiddenFlagPutRaw(t *testing.T, api *restAPI, wsID, rawBody string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, strings.NewReader(rawBody))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + wsID
	w := httptest.NewRecorder()
	api.HandleWorkspaces(w, r)
	return w
}

// hiddenFlagDiskEntry returns workspace.member_configs[agentID] from the raw
// disk record (nil when absent).
func hiddenFlagDiskEntry(t *testing.T, api *restAPI, id, agentID string) map[string]any {
	t.Helper()
	mcs, _ := hiddenFlagWorkspaceDiskJSON(t, api, id)["member_configs"].(map[string]any)
	if mcs == nil {
		return nil
	}
	entry, _ := mcs[agentID].(map[string]any)
	return entry
}

// D5: hiding a member persists the flag and it survives a GET (and restart,
// because it lives in the workspace record itself).
func TestWorkspaceHiddenFlag_HidePersistsAndSurvivesGET(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGPERSIST000001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, nil)

	// One-click hide is a minimal legal body: revision + member_configs only.
	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"mia": map[string]any{"hidden_from_sidebar": true},
	}})
	require.Equal(t, http.StatusOK, w.Code, "hide PUT body=%s", w.Body.String())

	got := env.getWorkspace(t, ws)
	mc := u1MemberConfig(got, "mia")
	require.NotNil(t, mc, "member_configs[mia] must exist after the hide")
	assert.Equal(t, true, mc["hidden_from_sidebar"], "GET must report the stored flag")

	entry := hiddenFlagDiskEntry(t, env.api, ws, "mia")
	require.NotNil(t, entry, "the flag is stored in the workspace record")
	assert.Equal(t, true, entry["hidden_from_sidebar"], "disk record carries hidden_from_sidebar:true")
}

// D5, the one documented semantics exception: inside an incoming member
// config, hidden_from_sidebar absent => preserve the stored value — so the
// minimal hide body must NOT wipe the member's stored heartbeat.
func TestWorkspaceHiddenFlag_HideOnlyPutPreservesStoredHeartbeat(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGHBKEEP0000001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, json.RawMessage(`{
		"mia": {"heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Check tasks."}}
	}`))

	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"mia": map[string]any{"hidden_from_sidebar": true},
	}})
	require.Equal(t, http.StatusOK, w.Code, "hide PUT body=%s", w.Body.String())

	mc := u1MemberConfig(env.getWorkspace(t, ws), "mia")
	require.NotNil(t, mc)
	assert.Equal(t, true, mc["hidden_from_sidebar"], "hide stored")
	hb, _ := mc["heartbeat"].(map[string]any)
	require.NotNil(t, hb, "the minimal hide body must not wipe the stored heartbeat")
	assert.Equal(t, true, hb["enabled"], "heartbeat.enabled preserved")
	assert.Equal(t, float64(10), hb["interval_minutes"], "heartbeat.interval_minutes preserved")
	assert.Equal(t, "Check tasks.", hb["body"], "heartbeat.body preserved")
}

// The mirror direction: a heartbeat-only PUT keeps its documented
// wholesale-replace semantics for the heartbeat AND preserves the stored
// hidden flag (the only field with preserve-when-absent).
func TestWorkspaceHiddenFlag_HeartbeatOnlyPutPreservesStoredHiddenFlag(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGHBRPL0000001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, json.RawMessage(`{
		"mia": {"hidden_from_sidebar": true,
		        "heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Old body."}}
	}`))

	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"mia": map[string]any{"heartbeat": map[string]any{
			"enabled": true, "interval_minutes": 15, "body": "New body.",
		}},
	}})
	require.Equal(t, http.StatusOK, w.Code, "heartbeat PUT body=%s", w.Body.String())

	mc := u1MemberConfig(env.getWorkspace(t, ws), "mia")
	require.NotNil(t, mc)
	assert.Equal(t, true, mc["hidden_from_sidebar"], "stored hidden flag must survive a heartbeat-only PUT")
	hb, _ := mc["heartbeat"].(map[string]any)
	require.NotNil(t, hb)
	assert.Equal(t, float64(15), hb["interval_minutes"], "heartbeat is replaced, not merged")
	assert.Equal(t, "New body.", hb["body"], "heartbeat is replaced, not merged")
}

// A member_configs key that is not on the (effective) core team is refused
// 422, hidden flag or not.
func TestWorkspaceHiddenFlag_UnknownMemberRefused(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGGHOST0000001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, nil)

	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"ghost-agent": map[string]any{"hidden_from_sidebar": true},
	}})
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "unknown member body=%s", w.Body.String())
	assert.Nil(t, hiddenFlagDiskEntry(t, env.api, ws, "ghost-agent"),
		"a refused hide must not persist anything")
}

// ADR "Agent leaves later": the whole entry — flag included — is GC'd when the
// member leaves the team; re-adding the agent starts VISIBLE.
func TestWorkspaceHiddenFlag_RemoveGCsEntryAndReAddStartsVisible(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGREADD0000001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, json.RawMessage(`{
		"mia": {"hidden_from_sidebar": true}
	}`))

	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim").Code, "remove Mia (non-default workspace)")
	assert.Nil(t, u1MemberConfig(env.getWorkspace(t, ws), "mia"),
		"removal garbage-collects the member_configs entry, flag included")
	assert.Nil(t, hiddenFlagDiskEntry(t, env.api, ws, "mia"), "entry gone from disk")

	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim", "mia").Code, "re-add Mia")
	mc := u1MemberConfig(env.getWorkspace(t, ws), "mia")
	if mc != nil {
		assert.NotEqual(t, true, mc["hidden_from_sidebar"],
			"re-adding the agent starts visible — the GC'd flag must not resurrect")
	}
	assert.NotEqual(t, true, hiddenFlagDiskEntry(t, env.api, ws, "mia")["hidden_from_sidebar"],
		"re-add starts visible on disk too")
}

// ADR "Minor, recommended": hidden_from_sidebar:true for a non-main-eligible
// member (worker) is refused 422, mirroring the worker-heartbeat rule — the
// sidebar has no row for a worker, so the flag would be inert and dishonest.
func TestWorkspaceHiddenFlag_WorkerHideRefused(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGWORKER000001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia", "worker1"}, nil)

	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"worker1": map[string]any{"hidden_from_sidebar": true},
	}})
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "worker hide body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "worker", "the refusal must name the offending member")
	assert.NotEqual(t, true, hiddenFlagDiskEntry(t, env.api, ws, "worker1")["hidden_from_sidebar"],
		"a refused worker hide must not persist anything")
}

// D8: a core_team PUT that removes a built-in roster member from the DEFAULT
// workspace is refused 409 — ensureBuiltinRosterPresent would silently re-add
// it at the next boot, so a 200 would make the API lie about a removal.
// Removing a NON-built-in member from the default workspace stays allowed.
func TestWorkspaceHiddenFlag_BuiltinRemoveFromDefaultWorkspaceRefused(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGDEFWS0000001"
	// Mia is a built-in roster member (she ships on install); worker1 is a
	// fixture-local custom agent and is NOT on the built-in roster.
	hiddenFlagSeedWorkspace(t, env.home, ws, true, []string{"jim", "mia", "worker1"}, nil)

	// Control: dropping the non-built-in member from the default workspace is
	// a legitimate Team-tab save and must stay 200.
	w := env.putTeam(t, ws, "jim", "mia")
	require.Equal(t, http.StatusOK, w.Code, "control: remove non-built-in worker1; body=%s", w.Body.String())

	// Control: a PUT that removes nobody is unaffected by the guard.
	w = env.putTeam(t, ws, "jim", "mia")
	require.Equal(t, http.StatusOK, w.Code, "control: same-team PUT; body=%s", w.Body.String())

	// The guard: dropping built-in Mia from the default workspace is 409.
	w = env.putTeam(t, ws, "jim")
	require.Equal(t, http.StatusConflict, w.Code, "built-in removal from default ws body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "built-in", "the refusal must say why")
	assert.Contains(t, w.Body.String(), "default workspace", "the refusal must name the default-workspace rule")

	team, _ := hiddenFlagWorkspaceDiskJSON(t, env.api, ws)["core_team"].([]any)
	assert.Contains(t, team, "mia", "a refused removal must not persist: Mia stays on the team")
}

// D8: built-ins on NON-default workspaces stay removable — nothing re-adds
// them there.
func TestWorkspaceHiddenFlag_BuiltinRemoveFromNonDefaultWorkspaceAllowed(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGOTHERWS00001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, nil)

	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim").Code,
		"removing built-in Mia from a non-default workspace must be allowed")
	team, _ := hiddenFlagWorkspaceDiskJSON(t, env.api, ws)["core_team"].([]any)
	assert.NotContains(t, team, "mia", "the removal persists on the non-default workspace")
}

// The update path stays revision-guarded: a stale revision is 409, hidden
// flag or not.
func TestWorkspaceHiddenFlag_StaleRevisionConflicts(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGREVCONF00001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, nil)

	stale := fmt.Sprintf(`{"revision":"%s","member_configs":{"mia":{"hidden_from_sidebar":true}}}`,
		"0000000000000000000000000000000000000000000000000000000000000000")
	w := hiddenFlagPutRaw(t, env.api, ws, stale)
	require.Equal(t, http.StatusConflict, w.Code, "stale revision body=%s", w.Body.String())
	assert.Nil(t, hiddenFlagDiskEntry(t, env.api, ws, "mia"),
		"a revision conflict must not persist the hide")
}
