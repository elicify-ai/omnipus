// rest_max_tool_iterations_fix2_test.go — #904 gate ROUND 2 test gaps
// (gateway side), closed with spec-derived tests.
//
// Sources of every expected value:
//   - D21 (docs/internal/specs/tool-iteration-limit-interview.md): the generic
//     PUT /api/v1/config is closed for the global limit and its import marker;
//     the refusal contract for a blocked path is blocked_paths.go's
//     403 "<path> is a blocked path — use the dedicated endpoint".
//   - The blocked-path rule itself (blocked_paths.go::blockedPaths): a blocked
//     path is refused at any nesting depth and in any spelling that
//     encoding/json would bind to the same field. encoding/json matches object
//     keys to struct fields with Unicode simple case folding (bytes.EqualFold
//     semantics: U+017F LATIN SMALL LETTER LONG S folds to "s", U+212A KELVIN
//     SIGN folds to "k"), so such a spelling reaches the blocked field.
//   - Spec "API and Data" D11/D16 write order + "Reload fails after the global
//     is written" edge case: a committed save whose apply fails answers 500
//     performance_reload_failed and is audited like any committed change
//     (audit.EmitSecuritySettingChange for the global).
//   - Gate round 2 contract addition (dispatch brief, backend-lead branch
//     feature/904-fix2-be): the reload-failure body carries typed details
//     with `stage` = "refresh" (in-memory refresh failed after config.json
//     was written) or "reload" (registry reload failed).
//
// RED status on this branch (tests-only, cut from 4b1f71d7c):
//   - TestUpdateConfig_UnicodeFoldBypass_* — RED until backend-lead lands the
//     Unicode-fold fix in the blocked-path walker.
//   - the `stage` assertions — RED until backend-lead lands the typed details.
//   - everything else is a regression guard over shipped behaviour, proven
//     able to fail by a mutation probe (see the dispatch report).

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

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// fix2AssertRefusedUnchanged asserts a PUT /api/v1/config was refused and
// changed nothing: not a 2xx, config.json byte-identical, and — when the
// answer is the blocked-path 403 — the message names the canonical blocked
// path. 400 is accepted too: the brief lets the fix refuse a non-canonical
// spelling either through the blocked-path answer (403) or as a malformed
// request (400); both leave the side door closed.
func fix2AssertRefusedUnchanged(t *testing.T, api *restAPI, before map[string]string, w *httptest.ResponseRecorder, body, canonical string) {
	t.Helper()
	assert.Contains(t, []int{http.StatusForbidden, http.StatusBadRequest}, w.Code,
		"a body that folds onto the blocked path %s must be refused; got %d %s", canonical, w.Code, w.Body.String())
	if w.Code == http.StatusForbidden {
		assert.Contains(t, mtiErrorText(t, w), canonical, "the blocked-path answer names the canonical path")
	}
	assert.Equal(t, before, mtiSnapshotFiles(t, api), "a refused PUT leaves config.json byte-identical (body %s)", body)
}

// ---------------------------------------------------------------------------
// Item 2 — N1 security proof: Unicode case folding must not bypass a blocked
// path. RED until backend-lead's fold fix lands.
// ---------------------------------------------------------------------------

const (
	fix2LongS  = "ſ" // LATIN SMALL LETTER LONG S — folds to "s"
	fix2Kelvin = "K" // KELVIN SIGN — folds to "k"
)

// instrument: the premise of every fold test — encoding/json binds the
// folded spelling to the blocked field. If this ever stops holding, the fold
// tests below would pass for the wrong reason, so it is asserted, not assumed.
func TestUpdateConfig_UnicodeFold_InstrumentJSONBindsFoldedKeys(t *testing.T) {
	var d config.AgentDefaults
	require.NoError(t, json.Unmarshal([]byte(`{"max_tool_iteration`+fix2LongS+`":1000}`), &d))
	require.Equal(t, 1000, d.MaxToolIterations,
		"instrument: encoding/json folds U+017F onto max_tool_iterations — the bypass premise")
	var g config.GatewayConfig
	require.NoError(t, json.Unmarshal([]byte(`{"dev_mode_bypa`+fix2LongS+`s":true}`), &g))
	require.True(t, g.DevModeBypass, "instrument: U+017F folds onto gateway.dev_mode_bypass")
	var probe struct {
		Kind string `json:"kind"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"`+fix2Kelvin+`ind":"x"}`), &probe))
	require.Equal(t, "x", probe.Kind, "instrument: U+212A folds onto a key with a k")
}

func TestUpdateConfig_UnicodeFoldBypass_GlobalMaxToolIterations(t *testing.T) {
	const seeded = `250,"max_tool_iterations_env_imported":true`
	for _, tc := range []struct{ name, body, canonical string }{
		{"nested global, long s", `{"agents":{"defaults":{"max_tool_iteration` + fix2LongS + `":1000}}}`,
			string(config.AgentsDefaultsMaxToolIterations)},
		{"dotted global, long s", `{"agents.defaults.max_tool_iteration` + fix2LongS + `":1000}`,
			string(config.AgentsDefaultsMaxToolIterations)},
		{"nested marker, long s", `{"agents":{"defaults":{"max_tool_iteration` + fix2LongS + `_env_imported":false}}}`,
			string(config.AgentsDefaultsMaxToolIterationsEnvImported)},
		{"long s in an ancestor segment", `{"agent` + fix2LongS + `":{"defaults":{"max_tool_iterations":1000}}}`,
			string(config.AgentsDefaultsMaxToolIterations)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newMTIAPI(t, seeded)
			before := mtiSnapshotFiles(t, api)
			w := mtiPutConfig(t, api, tc.body)
			fix2AssertRefusedUnchanged(t, api, before, w, tc.body, tc.canonical)
			assert.EqualValues(t, 250, mtiGetPerf(t, api)["max_tool_iterations"],
				"the effective global is unchanged (D21: one write path, PUT /performance)")
			assert.True(t, api.agentLoop.GetConfig().Agents.Defaults.MaxToolIterationsEnvImported,
				"the in-memory import marker is unchanged")
		})
	}
}

func TestUpdateConfig_UnicodeFoldBypass_GatewayBlockedPaths(t *testing.T) {
	for _, tc := range []struct{ name, body, canonical string }{
		{"dev_mode_bypass, long s", `{"gateway":{"dev_mode_bypa` + fix2LongS + `s":true}}`, string(config.GatewayDevModeBypass)},
		{"users, long s", `{"gateway":{"u` + fix2LongS + `ers":[{"username":"mallory","role":"admin"}]}}`, string(config.GatewayUsers)},
		{"dotted users, long s", `{"gateway.u` + fix2LongS + `ers":[{"username":"mallory","role":"admin"}]}`, string(config.GatewayUsers)},
		{"sandbox, long s", `{"` + fix2LongS + `andbox":{"mode":"off"}}`, "sandbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newMTIAPI(t, "250")
			usersBefore := len(api.agentLoop.GetConfig().Gateway.Users)
			before := mtiSnapshotFiles(t, api)
			w := mtiPutConfig(t, api, tc.body)
			fix2AssertRefusedUnchanged(t, api, before, w, tc.body, tc.canonical)
			live := api.agentLoop.GetConfig()
			assert.False(t, live.Gateway.DevModeBypass, "dev_mode_bypass stays off in the running config")
			assert.Len(t, live.Gateway.Users, usersBefore, "no user was added to the running config")
		})
	}
}

// Kelvin sign: a blocked key containing a "k" needs a Kelvin-sign (U+212A)
// bypass twin in TestUpdateConfig_UnicodeFoldBypass_Refused, because U+212A
// folds to "k" under the JSON key folding matchBlockedPath applies.
// workspace_seed_defaults (session-core C-DELEGATE, FR-014/015) is the one
// blocked path carrying a "k"; its twin is pinned in
// TestUpdateConfig_UnicodeFoldBypass_Refused. This test fails loudly the day
// another k-bearing path is blocked without one.
func TestBlockedPaths_KelvinVariantPremise_KPathsHaveTwin(t *testing.T) {
	require.NotEmpty(t, blockedPaths, "instrument: the blocked list is readable")
	var kPaths []string
	for _, bp := range blockedPaths {
		if strings.Contains(strings.ToLower(string(bp)), "k") {
			kPaths = append(kPaths, string(bp))
		}
	}
	assert.Equal(t, []string{"workspace_seed_defaults"}, kPaths,
		"exactly one blocked path carries a k; a new k-bearing blocked path needs a Kelvin-sign (U+212A) twin in TestUpdateConfig_UnicodeFoldBypass_Refused")
}

// ---------------------------------------------------------------------------
// Item 3 — case-insensitive blocked paths for NON-#904 keys. encoding/json
// binds "Gateway"/"Users"/"AGENTS" to the same fields as the lower-case
// spelling, so each must be refused exactly like it (blocked_paths.go
// contract: 403 naming the canonical path, nothing written).
// ---------------------------------------------------------------------------

func TestUpdateConfig_BlockedPathsCaseInsensitive_NonMTIKeys(t *testing.T) {
	for _, tc := range []struct{ name, body, canonical string }{
		{"Gateway.Users nested", `{"Gateway":{"Users":[{"username":"mallory","role":"admin"}]}}`, string(config.GatewayUsers)},
		{"GATEWAY.users nested", `{"GATEWAY":{"users":[{"username":"mallory","role":"admin"}]}}`, string(config.GatewayUsers)},
		{"Gateway.Users dotted", `{"Gateway.Users":[{"username":"mallory","role":"admin"}]}`, string(config.GatewayUsers)},
		{"Gateway.Dev_Mode_Bypass", `{"Gateway":{"Dev_Mode_Bypass":true}}`, string(config.GatewayDevModeBypass)},
		{"Sandbox", `{"Sandbox":{"mode":"off"}}`, "sandbox"},
		{"SANDBOX", `{"SANDBOX":{"mode":"off"}}`, "sandbox"},
		{"Credentials", `{"Credentials":{"x":"y"}}`, "credentials"},
		{"AGENTS.list nested", `{"AGENTS":{"list":[{"id":"evil"}]}}`, string(config.AgentsList)},
		{"Agents.List dotted", `{"Agents.List":[{"id":"evil"}]}`, string(config.AgentsList)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newMTIAPI(t, "250")
			before := mtiSnapshotFiles(t, api)
			w := mtiPutConfig(t, api, tc.body)
			require.Equal(t, http.StatusForbidden, w.Code, "blocked path in another case must be refused; body: %s", w.Body.String())
			assert.Equal(t, tc.canonical+" is a blocked path — use the dedicated endpoint", mtiErrorText(t, w))
			assert.Equal(t, before, mtiSnapshotFiles(t, api), "a refused PUT leaves config.json byte-identical")
		})
	}
	t.Run("instrument: a non-blocked mixed-case sibling is still accepted", func(t *testing.T) {
		api := newMTIAPI(t, "250")
		w := mtiPutConfig(t, api, `{"agents":{"defaults":{"max_tokens":8192}}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	})
}

// ---------------------------------------------------------------------------
// Item 6 — configRefreshError on a GLOBAL-ONLY change (no agent lowered):
// config.json is written, the in-memory refresh fails → 500
// performance_reload_failed, details.stage "refresh", and the global change
// is audited (it is committed). Kills "drop the audit call on the
// notApplied path".
// ---------------------------------------------------------------------------

// fix2CorruptAgentRecords makes every agent record on disk unparseable. A
// RAISE of the global reads no agent in the handler (D20: the affected set
// is empty without a store read), so the only reader left is the in-memory
// refresh that follows the config.json write
// (populateAgentsListFromEntityStoreStrict refuses an all-unparseable store) —
// the real refresh-failure path, no test-only production hook.
func fix2CorruptAgentRecords(t *testing.T, api *restAPI, ids ...string) {
	t.Helper()
	for _, id := range ids {
		p := filepath.Join(api.homePath, "entities", "agents", id+".json")
		_, err := os.Stat(p)
		require.NoError(t, err, "instrument: the record to corrupt exists at %s", p)
		require.NoError(t, os.WriteFile(p, []byte("{not json"), 0o600))
	}
}

func TestPerformancePut_GlobalOnlyRefreshFailure_ReloadFailedStageRefreshAudited(t *testing.T) {
	// D20 fixture: global 200, agent rides the global (no own value) — a
	// raise to 300 lowers nobody, so this is the global-only change.
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"})
	extraAudit := attachTestAuditor(t, api)
	fix2CorruptAgentRecords(t, api, "agent-a")

	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)

	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	assert.Equal(t, "performance_reload_failed", m["code"], "a committed save whose apply failed is 'saved, not applied'")
	assert.NotContains(t, fmt.Sprint(m["error"]), "nothing was changed", "the save IS committed")
	assert.EqualValues(t, 300, mtiDiskGlobal(t, api), "config.json holds the saved global")

	recs := mtiSecurityChanges(t, api, extraAudit)
	require.Len(t, recs, 1, "exactly one audit record — the committed global change; got %v", recs)
	assert.Equal(t, "agents.defaults.max_tool_iterations", recs[0]["resource"])
	assert.EqualValues(t, 200, recs[0]["old_value"])
	assert.EqualValues(t, 300, recs[0]["new_value"])

	details, _ := m["details"].(map[string]any)
	require.NotNil(t, details, "RED until feature/904-fix2-be: the reload-failure body carries typed details; body: %s", w.Body.String())
	// contracts/components/schemas/PerformanceReloadFailedDetails.yaml:
	// stage, changed_fields (= the fields in the PUT body) and lowered_agents
	// (empty when none) are all required.
	assert.Equal(t, "refresh", details["stage"], "the in-memory refresh (not the registry reload) failed")
	assert.Equal(t, []any{"max_tool_iterations"}, details["changed_fields"], "changed_fields = the fields in the PUT body")
	assert.Equal(t, []any{}, details["lowered_agents"], "a raise lowers nobody (D20); the list is present and empty")
}

// Twin: when the REGISTRY reload is what failed (config.json written and
// refreshed), details.stage is "reload". RED until feature/904-fix2-be.
func TestPerformancePut_ReloadFailure_DetailsStageReload(t *testing.T) {
	api := newMTIAPI(t, "200")
	api.agentLoop.SetReloadFunc(func() error { return fmt.Errorf("injected reload failure") })
	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	assert.Equal(t, "performance_reload_failed", m["code"])
	details, _ := m["details"].(map[string]any)
	require.NotNil(t, details, "RED until feature/904-fix2-be: typed reload-failure details; body: %s", w.Body.String())
	assert.Equal(t, "reload", details["stage"])
	assert.Equal(t, []any{"max_tool_iterations"}, details["changed_fields"])
	assert.Equal(t, []any{}, details["lowered_agents"])
	assert.EqualValues(t, 300, mtiGetPerf(t, api)["max_tool_iterations"],
		"stage reload: the refresh succeeded, so GET already shows the saved value")
}

// ---------------------------------------------------------------------------
// Item 7 (backend half) — every per-agent limit refusal names the field in
// ErrorResponse.field (contracts/components/schemas/ErrorResponse.yaml:
// "names the request field the error is about"), so the SPA recognises the
// refusal by field, not by message wording. RED until feature/904-fix2-be.
// ---------------------------------------------------------------------------

func TestAgentLimitRefusals_SetErrorResponseField(t *testing.T) {
	for _, tc := range []struct {
		name string
		send func(t *testing.T, api *restAPI) *httptest.ResponseRecorder
	}{
		{"PUT above the global (D10)", func(t *testing.T, api *restAPI) *httptest.ResponseRecorder {
			return putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":300}`)
		}},
		{"PUT above the bound (FR-006)", func(t *testing.T, api *restAPI) *httptest.ResponseRecorder {
			return putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":1001}`)
		}},
		{"PUT below the bound (FR-006)", func(t *testing.T, api *restAPI) *httptest.ResponseRecorder {
			return putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":0}`)
		}},
		{"POST above the global (D10)", func(t *testing.T, api *restAPI) *httptest.ResponseRecorder {
			return mtiPostAgent(t, api, `{"name":"TooHigh","type":"Main","soul":"s","max_tool_iterations":300}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"})
			w := tc.send(t, api)
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			m := mtiDecode(t, w.Body.Bytes())
			assert.Contains(t, fmt.Sprint(m["error"]), "max_tool_iterations", "instrument: this IS the limit refusal")
			assert.Equal(t, "max_tool_iterations", m["field"], "the refusal names its field; body: %s", w.Body.String())
		})
	}
}

// ---------------------------------------------------------------------------
// CHECK round 2, finding A (BLOCK, surviving mutant M1b): the DECIDING check
// under configMu must compare the confirmed snapshot against a FRESH list.
// Spec "API and Data" PerformanceSettingsUpdate rule: "the server recomputes,
// at write time, the set of agents whose own value is above the new global;
// the PUT succeeds only if that set equals confirmed_lowering ... an extra
// agent ... is drift: nothing is written and the PUT answers 409". Here agent
// C GAINS an own value (260 > 200) after the pre-check List and before the
// deciding List, so only the deciding comparison
// (decideMaxToolIterationsLowering's matchesConfirmation) can see the extra
// agent. Kills M1b (`!upd.matchesConfirmation(live)` → `false`): the handler
// would then lower A and B and write the global, leaving C at 260 unconfirmed.
// ---------------------------------------------------------------------------

func TestPerformancePut_DecidingCheck_AgentGainsOwnValueBetweenLists_Drift409(t *testing.T) {
	api := newMTIAPI(t, "300",
		mtiAgent{id: "agent-a", own: 250}, mtiAgent{id: "agent-b", own: 280}, mtiAgent{id: "agent-c"})
	s := &mtiScriptStore{Store: agentstore.New(api.homePath)}
	api.limitAgentStore = s
	extraAudit := attachTestAuditor(t, api)
	s.onList = func(n int) error {
		if n == 2 { // 1 = pre-check (before the step-up token), 2 = the deciding list under configMu
			s.concurrentWrite(t, "agent-c", func(ac *config.AgentConfig) { ac.MaxToolIterations = 260 })
		}
		return nil
	}
	before := mtiSnapshotFiles(t, api, "agent-a", "agent-b")

	w := mtiPutPerf(t, api, mtiRollbackBody) // confirms exactly A(250) and B(280)

	require.GreaterOrEqual(t, s.listCalls, 2, "instrument: the deciding List ran, so the change landed between the two lists")
	require.Equal(t, http.StatusConflict, w.Code, "an agent that gained an own value above the target is drift; body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	assert.Equal(t, mtiDriftCode, m["code"])
	mtiAssertAgentChanges(t, mtiPreviewAgents(t, m), []mtiLowering{
		{"agent-a", "Agent agent-a", 250, 200},
		{"agent-b", "Agent agent-b", 280, 200},
		{"agent-c", "Agent agent-c", 260, 200},
	})
	assert.Equal(t, 0, s.mutates(), "nothing written: no lowering attempted")
	assert.Equal(t, before, mtiSnapshotFiles(t, api, "agent-a", "agent-b"), "config.json, A and B byte-identical")
	assert.EqualValues(t, 260, mtiStoredOwn(t, api, "agent-c"), "C keeps the value it gained")
	assert.Empty(t, mtiSecurityChanges(t, api, extraAudit), "no audit record for a refused lowering")
}
