// rest_agents_max_tool_iterations_test.go — #904 RED: the per-agent tool
// iteration limit on the Agent REST surface (create / update / get / list).
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md (Status: Approved);
// founder decisions: docs/internal/specs/tool-iteration-limit-interview.md
// D1–D19. Every expected value below is taken from the spec's BDD scenarios,
// the Dataset tables ("Resolver", "Per-agent bounds") and the
// Machine-Verifiable Constraints — never from running the handlers.
//
// Wire shapes are read as raw JSON (map[string]any), not through the
// generated gen.Agent type: the new fields (max_tool_iterations_source,
// max_tool_iterations_override, max_tool_iterations_override_ignored) are
// being added to contracts/ by a parallel stream, and reading raw JSON keeps
// this file compiling against today's generated types so every test here
// fails on an ASSERTION, not a compile error.
//
// Oracles (spec "Contract Changes" row `Agent`):
//   - max_tool_iterations            = the EFFECTIVE value, 1–1000
//   - max_tool_iterations_source     = "global" | "agent"
//   - max_tool_iterations_override   = the stored own value; ABSENT when none
//   - max_tool_iterations_override_ignored = true iff own value > global (D1)

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// envMaxToolIterations is the retired env var (D5/D6). Every #904 harness
// unsets it so a developer's shell cannot leak a value into the global.
const envMaxToolIterations = "OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"

// Exact message texts from the spec's Machine-Verifiable Constraints.
const mtiBoundMsg = "max_tool_iterations must be between 1 and 1000"

func mtiAboveGlobalMsg(n, g int) string {
	return fmt.Sprintf("max_tool_iterations %d is above the global limit (%d); "+
		"lower it, or raise the global limit in Settings → Performance", n, g)
}

// unsetMTIEnv guarantees the retired env var is absent for the test (t.Setenv
// registers the restore; os.Unsetenv then removes it for the test's duration).
func mtiUnsetEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envMaxToolIterations, "")
	require.NoError(t, os.Unsetenv(envMaxToolIterations))
}

// mtiAgent is one seeded agent: own <= 0 means "no own value" (the store
// omits the key — AgentConfig.MaxToolIterations is `omitempty`).
type mtiAgent struct {
	id  string
	own int
}

// newMTIAPI builds a restAPI over a real temp-dir config.json + agent store.
// globalJSON is the raw JSON value written at agents.defaults.max_tool_iterations
// ("" = key absent, per Dataset "Saved global" row 2). The in-memory config is
// then loaded from disk through the production refresh path so it reflects
// whatever config.LoadConfig does with the saved value (D13).
func newMTIAPI(t *testing.T, globalJSON string, agents ...mtiAgent) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	mtiUnsetEnv(t)
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	defaults := `"workspace":` + mtiJSON(t, tmpDir) + `,"model_name":"test-model","max_tokens":4096`
	if globalJSON != "" {
		defaults += `,"max_tool_iterations":` + globalJSON
	}
	cfgJSON := `{"version":` + fmt.Sprint(config.CurrentVersion) +
		`,"agents":{"defaults":{` + defaults + `}},"providers":[]}`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(cfgJSON), 0o600))

	list := make([]config.AgentConfig, 0, len(agents))
	store := agentstore.New(tmpDir)
	for _, a := range agents {
		ac := config.AgentConfig{
			ID:    a.id,
			Name:  "Agent " + a.id,
			Type:  config.AgentTypeCustom,
			Tools: coreagent.NewCustomAgentToolsCfg(),
		}
		if a.own != 0 {
			ac.MaxToolIterations = a.own
		}
		rec := ac
		require.NoError(t, store.Create(a.id, &rec))
		list = append(list, ac)
	}

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: list,
		},
		// The agent loop's audit sink (<dir(home)>/system/audit.jsonl) — the
		// sink every existing security_setting_change emitter in pkg/gateway
		// uses (a.agentLoop.AuditLogger()). D11's audit assertions read it.
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	// Load the on-disk config through the production path (LoadConfig +
	// roster population from the agent store) so the live config is what a
	// real boot would hold.
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
	return api
}

func mtiJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// decodeWire decodes a JSON object body into a raw map.
func mtiDecode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m), "body is not a JSON object: %s", body)
	return m
}

// assertLimitFields asserts the four #904 Agent fields at full strength.
// wantOverride < 0 means the key must be ABSENT (no own value).
func mtiAssertLimitFields(t *testing.T, m map[string]any, wantEff int, wantSrc string, wantOverride int, wantIgnored bool) {
	t.Helper()
	assert.EqualValues(t, wantEff, m["max_tool_iterations"],
		"max_tool_iterations must be the EFFECTIVE limit (min(global, own))")
	assert.Equal(t, wantSrc, m["max_tool_iterations_source"],
		"max_tool_iterations_source must say where the effective limit comes from")
	ov, present := m["max_tool_iterations_override"]
	if wantOverride < 0 {
		assert.False(t, present, "max_tool_iterations_override must be absent when the agent has no own value; got %v", ov)
	} else {
		assert.True(t, present, "max_tool_iterations_override must carry the stored own value %d", wantOverride)
		assert.EqualValues(t, wantOverride, ov, "max_tool_iterations_override must be the stored own value, shown truthfully")
	}
	assert.Equal(t, wantIgnored, m["max_tool_iterations_override_ignored"],
		"max_tool_iterations_override_ignored must be true iff the own value is above the global (D1)")
}

func mtiGetAgent(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	api.getAgent(w, id)
	require.Equal(t, http.StatusOK, w.Code, "GET agent %s: %s", id, w.Body.String())
	return mtiDecode(t, w.Body.Bytes())
}

func mtiListAgent(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET agents: %s", w.Body.String())
	var arr []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &arr))
	for _, m := range arr {
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("agent %q not in GET /api/v1/agents list", id)
	return nil
}

// storedRecord reads the agent's persisted entity record as raw JSON.
func mtiStoredRecord(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(api.homePath, "entities", "agents", id+".json"))
	require.NoError(t, err)
	return mtiDecode(t, raw)
}

func mtiPostAgent(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	return w
}

func mtiErrorText(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	m := mtiDecode(t, w.Body.Bytes())
	s, _ := m["error"].(string)
	return s
}

// --- Dataset "Resolver" through the Agent API (FR-002, FR-003, SC-001) ---

// TestAgentResponses_ResolverDataset walks every row of the spec's Dataset
// "Resolver" (G = saved global, O = stored own value) through GET
// /api/v1/agents/{id}. Rows 4, 5, 9 (own above global) are the D1
// capped-and-flagged cases today's "per-agent wins" code gets wrong.
func TestAgentResponses_ResolverDataset(t *testing.T) {
	cases := []struct {
		row          int
		global, own  int // own 0 = none
		wantEff      int
		wantSrc      string
		wantOverride int // -1 = absent
		wantIgnored  bool
	}{
		{1, 200, 0, 200, "global", -1, false},
		{2, 200, 50, 50, "agent", 50, false},
		{3, 200, 200, 200, "agent", 200, false},
		{4, 200, 201, 200, "global", 201, true},
		{5, 200, 500, 200, "global", 500, true},
		{6, 600, 500, 500, "agent", 500, false},
		{7, 1, 0, 1, "global", -1, false},
		{8, 1000, 1000, 1000, "agent", 1000, false},
		{9, 200, 5000, 200, "global", 5000, true},
		{11, 200, -5, 200, "global", -1, false}, // hand-edited negative = no own value (D17)
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("row%d_G%d_O%d", tc.row, tc.global, tc.own), func(t *testing.T) {
			api := newMTIAPI(t, fmt.Sprint(tc.global), mtiAgent{id: "agent-a", own: tc.own})
			mtiAssertLimitFields(t, mtiGetAgent(t, api, "agent-a"), tc.wantEff, tc.wantSrc, tc.wantOverride, tc.wantIgnored)
		})
	}
}

// TestAgentResponses_ResolverDataset_ZeroStored is Dataset "Resolver" row 10
// written as a raw on-disk record: AgentConfig.MaxToolIterations is
// omitempty, so a literal 0 can only exist on disk by hand edit.
func TestAgentResponses_ResolverDataset_ZeroStored(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"})
	path := filepath.Join(api.homePath, "entities", "agents", "agent-a.json")
	rec := mtiStoredRecord(t, api, "agent-a")
	rec["max_tool_iterations"] = 0
	require.NoError(t, os.WriteFile(path, []byte(mtiJSON(t, rec)), 0o600))
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))

	mtiAssertLimitFields(t, mtiGetAgent(t, api, "agent-a"), 200, "global", -1, false)
}

// TestAgentResponses_ListAndGetAgree — FR-003: list and get return the same
// four fields for the capped-and-flagged agent (Scenario "Profile flags an
// ignored own value": override 500, ignored true, source global, effective 200).
func TestAgentResponses_ListAndGetAgree(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a", own: 500})
	mtiAssertLimitFields(t, mtiGetAgent(t, api, "agent-a"), 200, "global", 500, true)
	mtiAssertLimitFields(t, mtiListAgent(t, api, "agent-a"), 200, "global", 500, true)
}

// --- PUT /api/v1/agents/{id} (US-2, FR-006/007/008) ---

// Scenario "Operator lowers one agent".
func TestAgentUpdate_MaxToolIterations_LowerOne(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"})
	w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":50}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	mtiAssertLimitFields(t, mtiDecode(t, w.Body.Bytes()), 50, "agent", 50, false)
	assert.EqualValues(t, 50, mtiStoredRecord(t, api, "agent-a")["max_tool_iterations"])
	mtiAssertLimitFields(t, mtiGetAgent(t, api, "agent-a"), 50, "agent", 50, false)
}

// Scenario "Per-agent value equal to the global is accepted" (Edge Case).
func TestAgentUpdate_MaxToolIterations_EqualToGlobalAccepted(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"})
	w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":200}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	mtiAssertLimitFields(t, mtiDecode(t, w.Body.Bytes()), 200, "agent", 200, false)
}

// Scenario "Per-agent value above the global is refused" (D10, FR-007).
func TestAgentUpdate_MaxToolIterations_AboveGlobalRefused(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"})
	before := mtiStoredRecord(t, api, "agent-a")
	w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, mtiAboveGlobalMsg(300, 200), mtiErrorText(t, w))
	assert.Equal(t, before, mtiStoredRecord(t, api, "agent-a"), "a refused save must leave the stored record unchanged")
}

// Scenario "Per-agent value out of range is refused" — Dataset "Per-agent
// bounds" (global 1000), rows 1–5.
func TestAgentUpdate_MaxToolIterations_BoundsDataset(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		wantOK bool
	}{
		{"row1_min_1", "1", true},
		{"row2_max_1000", "1000", true},
		{"row3_zero", "0", false},
		{"row4_negative", "-3", false},
		{"row5_1001", "1001", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newMTIAPI(t, "1000", mtiAgent{id: "agent-a"})
			before := mtiStoredRecord(t, api, "agent-a")
			w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":`+tc.value+`}`)
			if tc.wantOK {
				require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
				want, convErr := strconv.Atoi(tc.value)
				require.NoError(t, convErr)
				// global 1000, own value in range → own applies (source agent).
				mtiAssertLimitFields(t, mtiDecode(t, w.Body.Bytes()), want, "agent", want, false)
				return
			}
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, mtiBoundMsg, mtiErrorText(t, w))
			assert.Equal(t, before, mtiStoredRecord(t, api, "agent-a"), "a refused save must leave the stored record unchanged")
		})
	}
}

// Dataset "Per-agent bounds" note: a non-integer (2.5) is 400 from request
// decoding/validation — no message oracle is specified for that path.
func TestAgentUpdate_MaxToolIterations_NonIntegerRefused(t *testing.T) {
	api := newMTIAPI(t, "1000", mtiAgent{id: "agent-a", own: 40})
	w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":2.5}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.EqualValues(t, 40, mtiStoredRecord(t, api, "agent-a")["max_tool_iterations"])
}

// TestAgentUpdate_MaxToolIterations_NullVsOmitted — test plan row 13b (grill
// F1). Scenarios "Use global limit clears the own value" and "Omitted field
// leaves the own value unchanged". RAW JSON bytes only: a marshalled
// generated struct with a nil pointer omits the key and would let an
// implementation that only checks `!= nil` pass for the wrong reason.
func TestAgentUpdate_MaxToolIterations_NullVsOmitted(t *testing.T) {
	t.Run("omitted_leaves_own_value", func(t *testing.T) {
		api := newMTIAPI(t, "200", mtiAgent{id: "agent-a", own: 50})
		w := putAgentJSON(t, api, "agent-a", `{"description":"x"}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		assert.EqualValues(t, 50, mtiStoredRecord(t, api, "agent-a")["max_tool_iterations"],
			"an omitted max_tool_iterations must leave the own value unchanged")
		mtiAssertLimitFields(t, mtiDecode(t, w.Body.Bytes()), 50, "agent", 50, false)
	})
	t.Run("explicit_null_clears", func(t *testing.T) {
		api := newMTIAPI(t, "200", mtiAgent{id: "agent-a", own: 50})
		w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":null}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		_, still := mtiStoredRecord(t, api, "agent-a")["max_tool_iterations"]
		assert.False(t, still, "an explicit null must remove the stored max_tool_iterations key")
		mtiAssertLimitFields(t, mtiDecode(t, w.Body.Bytes()), 200, "global", -1, false)
		mtiAssertLimitFields(t, mtiGetAgent(t, api, "agent-a"), 200, "global", -1, false)
	})
}

// Scenario "Unrelated autosave does not resend the limit" — server half:
// an unrelated PUT on a capped-and-flagged agent (stored 500, global 200)
// succeeds and leaves 500 on disk (D1: never silently rewritten).
func TestAgentUpdate_UnrelatedFieldOnCappedAgent_KeepsStoredValue(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a", own: 500})
	w := putAgentJSON(t, api, "agent-a", `{"description":"x"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.EqualValues(t, 500, mtiStoredRecord(t, api, "agent-a")["max_tool_iterations"])
	mtiAssertLimitFields(t, mtiDecode(t, w.Body.Bytes()), 200, "global", 500, true)
}

// Scenario "Worker PUT of the limit is accepted" (D14, FR-013).
func TestAgentUpdate_Worker_MaxToolIterationsAccepted(t *testing.T) {
	mtiUnsetEnv(t)
	api := buildExecutorTestAPI(t) // no saved global → effective global 200 (D13)
	id := createSubagent3p(t, api)
	w := putAgentJSON(t, api, id, `{"max_tool_iterations":60}`)
	require.Equal(t, http.StatusOK, w.Code, "subagent_3p PUT of max_tool_iterations must be accepted (D14); body: %s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "subagent_3p agents do not support")
	mtiAssertLimitFields(t, mtiDecode(t, w.Body.Bytes()), 60, "agent", 60, false)
}

// D14: the forbidden-field table no longer lists max_tool_iterations.
func TestFirstForbiddenSubagent3pField_MaxToolIterationsAllowed(t *testing.T) {
	n := 30
	field, forbidden := firstForbiddenSubagent3pField(&gen.AgentUpdateRequest{MaxToolIterations: &n})
	assert.False(t, forbidden, "max_tool_iterations must be allowed on subagent_3p (D14); got forbidden field %q", field)
}

// --- POST /api/v1/agents (US-3, FR-014) ---

// Scenario "Create keeps the requested own value" (guards the
// normalizeVariant drop, FR-014), plus the runtime leg: the registered
// instance runs with 40.
func TestAgentCreate_MaxToolIterations_Persists(t *testing.T) {
	for _, typ := range []string{"Main", "Subagent"} {
		t.Run(typ, func(t *testing.T) {
			api := newMTIAPI(t, "200")
			body := `{"name":"Lim ` + typ + `","type":"` + typ + `","description":"d","soul":"s","max_tool_iterations":40}`
			w := mtiPostAgent(t, api, body)
			require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
			m := mtiDecode(t, w.Body.Bytes())
			mtiAssertLimitFields(t, m, 40, "agent", 40, false)
			id, _ := m["id"].(string)
			require.NotEmpty(t, id)
			assert.EqualValues(t, 40, mtiStoredRecord(t, api, id)["max_tool_iterations"])
			mtiAssertLimitFields(t, mtiGetAgent(t, api, id), 40, "agent", 40, false)
			inst, ok := api.agentLoop.GetRegistry().GetAgent(id)
			require.True(t, ok)
			assert.Equal(t, 40, inst.MaxIterations, "the running instance must use the own value 40")
		})
	}
}

// Scenario "Create without a value rides the global".
func TestAgentCreate_MaxToolIterations_OmittedRidesGlobal(t *testing.T) {
	api := newMTIAPI(t, "200")
	w := mtiPostAgent(t, api, `{"name":"Rider","type":"Subagent","description":"d","soul":"s"}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	mtiAssertLimitFields(t, m, 200, "global", -1, false)
	id, _ := m["id"].(string)
	_, has := mtiStoredRecord(t, api, id)["max_tool_iterations"]
	assert.False(t, has, "a create without the field must store no own value")
}

// Scenario "Create above the global is refused" — and no record exists.
func TestAgentCreate_MaxToolIterations_AboveGlobalRefused(t *testing.T) {
	api := newMTIAPI(t, "200")
	w := mtiPostAgent(t, api, `{"name":"TooHigh","type":"Main","soul":"s","max_tool_iterations":500}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, mtiAboveGlobalMsg(500, 200), mtiErrorText(t, w))
	agents, _, err := agentstore.New(api.homePath).List()
	require.NoError(t, err)
	for _, a := range agents {
		assert.NotEqual(t, "TooHigh", a.Name, "a refused create must leave no agent record")
	}
}

// Dataset "Per-agent bounds" rows 3–5 on create (FR-006).
func TestAgentCreate_MaxToolIterations_OutOfRangeRefused(t *testing.T) {
	for _, v := range []string{"0", "-3", "1001"} {
		t.Run(v, func(t *testing.T) {
			api := newMTIAPI(t, "1000")
			w := mtiPostAgent(t, api, `{"name":"Bad`+v+`","type":"Main","soul":"s","max_tool_iterations":`+v+`}`)
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, mtiBoundMsg, mtiErrorText(t, w))
		})
	}
}

// Scenario "External-CLI worker created with its own limit" (D14): 201 with
// override 30, and the dispatch leg — the registered instance's
// MaxIterations is what prepareRunOptions passes as RunOptions.MaxTurns.
func TestAgentCreate_Worker_MaxToolIterations(t *testing.T) {
	mtiUnsetEnv(t)
	api := buildExecutorTestAPI(t)
	body := `{"name":"ExtLim","type":"subagent_3p","description":"external worker","soul":"s",` +
		`"executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"max_tool_iterations":30}`
	w := mtiPostAgent(t, api, body)
	require.Equal(t, http.StatusCreated, w.Code, "subagent_3p create with max_tool_iterations must be accepted (D14); body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	mtiAssertLimitFields(t, m, 30, "agent", 30, false)
	id, _ := m["id"].(string)
	inst, ok := api.agentLoop.GetRegistry().GetAgent(id)
	require.True(t, ok)
	assert.Equal(t, 30, inst.MaxIterations, "a real dispatch passes the instance's MaxIterations as the CLI turn cap")
}
