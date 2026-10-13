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

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/workspace"
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

	// The guard: dropping built-in Mia from the default workspace is 409 with
	// the founder-fixed message and machine-readable code (2026-10-11), so
	// the SPA can tell this permanent refusal apart from a retryable revision
	// conflict (which carries no code).
	w = env.putTeam(t, ws, "jim")
	require.Equal(t, http.StatusConflict, w.Code, "built-in removal from default ws body=%s", w.Body.String())
	var errBody map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errBody), "refusal body must be JSON: %s", w.Body.String())
	assert.Equal(t, "Built-in agents can't be removed from this workspace", errBody["error"],
		"the refusal must carry the founder-fixed message verbatim")
	assert.Equal(t, "builtin_roster_default", errBody["code"],
		"the refusal must carry the documented machine-readable code")

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

// ADR addendum 2026-10-11 (founder: "hiding is only hiding"): an entry with
// NEITHER field ({}) keeps the pre-feature replace behaviour — the stored
// heartbeat is cleared — while rule 1 still preserves the flag.
func TestWorkspaceHiddenFlag_EmptyEntryClearsStoredHeartbeat(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGEMPTY000001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, json.RawMessage(`{
		"mia": {"hidden_from_sidebar": true,
		        "heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Check tasks."}}
	}`))

	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"mia": map[string]any{},
	}})
	require.Equal(t, http.StatusOK, w.Code, "empty-entry PUT body=%s", w.Body.String())

	mc := u1MemberConfig(env.getWorkspace(t, ws), "mia")
	require.NotNil(t, mc)
	assert.Equal(t, true, mc["hidden_from_sidebar"],
		"rule 1: the flag is preserved even by an otherwise-empty entry")
	assert.Nil(t, mc["heartbeat"],
		"{} keeps the ordinary replace behaviour: the stored heartbeat is cleared")
	entry := hiddenFlagDiskEntry(t, env.api, ws, "mia")
	require.NotNil(t, entry)
	assert.Nil(t, entry["heartbeat"], "the cleared heartbeat is gone from disk too")
}

// A heartbeat the body DOES carry is replaced wholesale, never merged
// field-by-field: a partial heartbeat ({enabled:false} only) clears the
// fields it omits, exactly as the pre-feature replace semantics did.
func TestWorkspaceHiddenFlag_PartialHeartbeatReplacedNotMerged(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGPARTIAL0001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, json.RawMessage(`{
		"mia": {"heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Old body."}}
	}`))

	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"mia": map[string]any{"heartbeat": map[string]any{"enabled": false}},
	}})
	require.Equal(t, http.StatusOK, w.Code, "partial heartbeat PUT body=%s", w.Body.String())

	hb, _ := u1MemberConfig(env.getWorkspace(t, ws), "mia")["heartbeat"].(map[string]any)
	require.NotNil(t, hb)
	assert.Equal(t, false, hb["enabled"], "the carried field lands")
	_, hasInterval := hb["interval_minutes"]
	assert.False(t, hasInterval, "an omitted heartbeat field is CLEARED (replace), not merged")
	_, hasBody := hb["body"]
	assert.False(t, hasBody, "an omitted heartbeat field is CLEARED (replace), not merged")
}

// Rule 2, show direction: a pure show entry (hidden_from_sidebar:false,
// heartbeat absent) also leaves the stored heartbeat untouched.
func TestWorkspaceHiddenFlag_PureShowEntryPreservesHeartbeat(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGSHOW000001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, json.RawMessage(`{
		"mia": {"hidden_from_sidebar": true,
		        "heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Check tasks."}}
	}`))

	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"mia": map[string]any{"hidden_from_sidebar": false},
	}})
	require.Equal(t, http.StatusOK, w.Code, "pure show PUT body=%s", w.Body.String())

	mc := u1MemberConfig(env.getWorkspace(t, ws), "mia")
	require.NotNil(t, mc)
	assert.NotEqual(t, true, mc["hidden_from_sidebar"], "the show lands: the member is no longer hidden")
	assert.NotEqual(t, true, hiddenFlagDiskEntry(t, env.api, ws, "mia")["hidden_from_sidebar"],
		"disk agrees the member is visible again")
	hb, _ := mc["heartbeat"].(map[string]any)
	require.NotNil(t, hb, "a pure show must not change any other setting")
	assert.Equal(t, true, hb["enabled"], "stored heartbeat survives the pure show")
}

// Founder ruling 2026-10-11 (S2/F2): the built-in-removal refusal must run
// BEFORE any side effect. A valid-revision PUT on the default workspace that
// removes built-in Mia AND disables continuing member Jim's heartbeat is
// refused 409, and NOTHING changes: the workspace record (Jim's heartbeat
// stays enabled), the delegation store, and Jim's plan pause state.
func TestWorkspacePut_BuiltinRemovalRefusalPrecedesPlanEffects(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGREFUSR00001"
	hiddenFlagSeedWorkspace(t, env.home, ws, true, []string{"jim", "mia"}, json.RawMessage(`{
		"jim": {"heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Check."}},
		"mia": {"heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Mia."}}
	}`))

	// Wire the plan store + a real PlanEngine exactly as production boot does
	// (rest_plans_test.go::newTestRestAPIWithPlans) so the pause/resume loop in
	// prepareWorkspacePutMembers is live and a pre-refusal pause would be
	// observable on the store.
	planStore := plan.New(filepath.Join(env.home, "plans"))
	env.api.planStore = planStore
	pe := agent.NewPlanEngine(env.api.agentLoop, planStore, env.api.taskStore, env.api.taskExecutor)
	env.api.agentLoop.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)

	// A RUNNING plan owned by Jim with no pause reason.
	p := &plan.Plan{Title: "jim plan", WorkspaceID: ws, OwnerAgentID: "jim", State: plan.StateRunning}
	require.NoError(t, planStore.Create(p), "seed running plan")

	edgesBefore, hadEdgesBefore := workspace.LoadDelegation(env.home, ws)

	// Remove Mia (built-in, default workspace) AND disable Jim's heartbeat in
	// one request: the refusal must win before the heartbeat disable's pause
	// side effect fires.
	body := fmt.Sprintf(`{"revision":%q,"core_team":["jim"],"member_configs":{"jim":{"heartbeat":{"enabled":false,"interval_minutes":10,"body":"Check."}}}}`,
		currentRevision(env.home, ws))
	w := hiddenFlagPutRaw(t, env.api, ws, body)
	require.Equal(t, http.StatusConflict, w.Code, "refusal body=%s", w.Body.String())

	var errBody map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errBody))
	assert.Equal(t, "builtin_roster_default", errBody["code"], "the 409 carries the refusal code")
	assert.Equal(t, "Built-in agents can't be removed from this workspace", errBody["error"])

	got, err := planStore.Get(p.ID)
	require.NoError(t, err, "plan must still be readable")
	assert.Equal(t, plan.StateRunning, got.State, "plan state untouched by the refusal")
	assert.Equal(t, "", got.PausedReason,
		"the plan must NOT be paused: the refusal ran before any plan side effect")

	disk := hiddenFlagWorkspaceDiskJSON(t, env.api, ws)
	team, _ := disk["core_team"].([]any)
	assert.Contains(t, team, "mia", "the workspace record is unchanged: Mia stays on the team")
	jimEntry, _ := disk["member_configs"].(map[string]any)["jim"].(map[string]any)
	require.NotNil(t, jimEntry)
	jimHB, _ := jimEntry["heartbeat"].(map[string]any)
	require.NotNil(t, jimHB, "Jim's stored heartbeat is unchanged")
	assert.Equal(t, true, jimHB["enabled"], "Jim's heartbeat stays enabled — the disable never persisted")

	edgesAfter, hadEdgesAfter := workspace.LoadDelegation(env.home, ws)
	assert.Equal(t, hadEdgesBefore, hadEdgesAfter, "delegation store presence unchanged")
	assert.Equal(t, edgesBefore, edgesAfter, "delegation store contents unchanged")
}

// currentRevision reads the workspace's current revision for a raw PUT body.
func currentRevision(home, id string) string {
	state, err := workspace.ReadState(home, id)
	if err != nil {
		return strings.Repeat("0", 64)
	}
	return state.Revision
}

// S2-06: the PURE hide/show shortcut in prepareWorkspacePutMembers. A pure hide
// carries the display flag and nothing else, so the stored heartbeat survives
// unchanged — and with no enable→disable transition there is nothing to pause
// for. Without the shortcut the transition test reads wasEnabled=true (the
// stored heartbeat) against isEnabled=false (the entry omits the heartbeat) and
// pauses plans for a member whose heartbeat is still running.
//
// The companion test below is the control: a real heartbeat disable DOES pause,
// so this one cannot pass merely because the pause wiring is dead.
func TestWorkspaceHiddenFlag_PureHideDoesNotPausePlans(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGPAUSE00001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, json.RawMessage(`{
		"mia": {"heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Mia."}}
	}`))

	planStore := plan.New(filepath.Join(env.home, "plans"))
	env.api.planStore = planStore
	pe := agent.NewPlanEngine(env.api.agentLoop, planStore, env.api.taskStore, env.api.taskExecutor)
	env.api.agentLoop.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)

	p := &plan.Plan{Title: "mia plan", WorkspaceID: ws, OwnerAgentID: "mia", State: plan.StateRunning}
	require.NoError(t, planStore.Create(p), "seed running plan")

	// Pure hide: the flag is present, the heartbeat is absent.
	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"mia": map[string]any{"hidden_from_sidebar": true},
	}})
	require.Equal(t, http.StatusOK, w.Code, "pure hide PUT body=%s", w.Body.String())

	got, err := planStore.Get(p.ID)
	require.NoError(t, err, "the plan must still be readable")
	assert.Equal(t, plan.StateRunning, got.State, "a pure hide must not change the plan's state")
	assert.Empty(t, got.PausedReason,
		"S2-06: a pure hide leaves the member's heartbeat enabled, so there is no disable transition to pause for")

	// The shortcut's own premise, asserted rather than assumed: the stored
	// heartbeat really is unchanged by the pure hide.
	mc := u1MemberConfig(env.getWorkspace(t, ws), "mia")
	require.NotNil(t, mc)
	hb, _ := mc["heartbeat"].(map[string]any)
	require.NotNil(t, hb, "a pure hide must not clear the heartbeat")
	assert.Equal(t, true, hb["enabled"], "the stored heartbeat survives the pure hide")
}

// S2-06 control: an ORDINARY heartbeat disable (the heartbeat IS carried, and
// disabled) still pauses the owner's running plans. This is what makes the
// pure-hide assertion above meaningful.
func TestWorkspaceHiddenFlag_HeartbeatDisableStillPausesPlans(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JHIDDENFLAGPAUSEC00001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim", "mia"}, json.RawMessage(`{
		"mia": {"heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Mia."}}
	}`))

	planStore := plan.New(filepath.Join(env.home, "plans"))
	env.api.planStore = planStore
	pe := agent.NewPlanEngine(env.api.agentLoop, planStore, env.api.taskStore, env.api.taskExecutor)
	env.api.agentLoop.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)

	p := &plan.Plan{Title: "mia plan", WorkspaceID: ws, OwnerAgentID: "mia", State: plan.StateRunning}
	require.NoError(t, planStore.Create(p), "seed running plan")

	w := env.put(t, ws, map[string]any{"member_configs": map[string]any{
		"mia": map[string]any{"heartbeat": map[string]any{
			"enabled": false, "interval_minutes": 10, "body": "Mia."}},
	}})
	require.Equal(t, http.StatusOK, w.Code, "heartbeat-disable PUT body=%s", w.Body.String())

	got, err := planStore.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.PausedReasonOwnerDisabled, got.PausedReason,
		"instrument: a real heartbeat disable must pause the owner's running plans")
}

// Intended behaviour (founder decision A, 2026-10-13): member_configs on a
// workspace PUT cannot delete one off-team entry. A PUT that sends only on-team
// entries prunes EVERY off-team (stale) entry; a PUT that keeps any off-team
// entry is refused 422; on-team entries are never touched by the prune.
func TestWorkspaceMemberConfigs_PutPrunesAllOffTeamEntriesAndRefusesKeepingOne(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JMCPRUNEALLOFFTEAM00001"
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim"}, json.RawMessage(`{
		"jim": {"hidden_from_sidebar": true},
		"mia": {"hidden_from_sidebar": true},
		"ray": {"hidden_from_sidebar": true}
	}`))
	revision := func() any { return env.getWorkspace(t, ws)["revision"] }

	// Keeping one stale entry (ray) while omitting another (mia) is refused, and
	// nothing is written.
	w := env.put(t, ws, map[string]any{"revision": revision(), "member_configs": map[string]any{
		"ray": map[string]any{"hidden_from_sidebar": true},
	}})
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "an off-team key in the request is refused: %s", w.Body.String())
	for _, id := range []string{"jim", "mia", "ray"} {
		assert.NotNil(t, hiddenFlagDiskEntry(t, env.api, ws, id), "%s untouched by a refused PUT", id)
	}

	// An empty member_configs map prunes every off-team entry at once, the
	// on-team one stays.
	w = env.put(t, ws, map[string]any{"revision": revision(), "member_configs": map[string]any{}})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Nil(t, hiddenFlagDiskEntry(t, env.api, ws, "mia"), "stale mia pruned")
	assert.Nil(t, hiddenFlagDiskEntry(t, env.api, ws, "ray"), "stale ray pruned with it")
	assert.Equal(t, true, hiddenFlagDiskEntry(t, env.api, ws, "jim")["hidden_from_sidebar"], "on-team jim untouched")

	// Sending only an on-team entry prunes the stale ones the same way.
	hiddenFlagSeedWorkspace(t, env.home, ws, false, []string{"jim"}, json.RawMessage(`{
		"jim": {"hidden_from_sidebar": true}, "mia": {"hidden_from_sidebar": true}
	}`))
	w = env.put(t, ws, map[string]any{"revision": revision(), "member_configs": map[string]any{
		"jim": map[string]any{"hidden_from_sidebar": true},
	}})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Nil(t, hiddenFlagDiskEntry(t, env.api, ws, "mia"), "stale mia pruned by an on-team-only PUT")
	assert.NotNil(t, hiddenFlagDiskEntry(t, env.api, ws, "jim"), "on-team jim kept")
}
