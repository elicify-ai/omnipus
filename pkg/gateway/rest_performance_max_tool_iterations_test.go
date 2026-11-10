// rest_performance_max_tool_iterations_test.go — #904 RED: the GLOBAL tool
// iteration limit on Settings → Performance (GET/PUT /api/v1/performance),
// the lowering preview (GET /api/v1/performance/max-tool-iterations/preview)
// and the D11/D16 confirmed-lowering write path.
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md (Approved) —
// Contract Changes rows PerformanceSettings / PerformanceSettingsUpdate /
// MaxToolIterationsLoweringConflict / preview path; "API and Data" D11/D16
// write order; BDD scenarios of User Stories 1, 5, 6, 7; Datasets "Global
// bounds", "Saved global", "Confirmed lowering". Founder decisions D1–D19.
//
// Responses are read as raw JSON (the generated types for the new fields
// are being produced by a parallel contract stream), so these tests compile
// against today's tree and fail on assertions.
//
// The preview endpoint's Go handler name is NOT pinned by the spec (only
// its path and operationId), so the preview is exercised through the REAL
// route table (registerAdditionalEndpoints) — which also proves it is
// registered behind adminWrap (FR-018).

package gateway

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const (
	mtiPreviewPath   = "/api/v1/performance/max-tool-iterations/preview"
	mtiDriftCode     = "max_tool_iterations_lowering_drift"
	mtiEnvTokenValue = "mti-env-token"
)

func mtiDriftMsg(n int) string {
	return fmt.Sprintf("the agents affected by lowering the limit to %d changed since the preview; "+
		"review the updated list and confirm again", n)
}

// putPerf sends PUT /api/v1/performance with a raw JSON body and a freshly
// minted step-up token (withReAuthAdmin).
func mtiPutPerf(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	return w
}

// putPerfWithToken sends the PUT with an explicit, caller-held token so a
// test can prove whether that token was consumed.
func mtiPutPerfWithToken(t *testing.T, api *restAPI, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(reAuthHeader, token)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: reauthGateAdminUser}))
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	return w
}

func mtiGetPerf(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	api.HandlePerformance(w, httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil))
	require.Equal(t, http.StatusOK, w.Code, "GET /performance: %s", w.Body.String())
	return mtiDecode(t, w.Body.Bytes())
}

// previewViaMux issues GET preview through the production route table,
// authenticated by the legacy env bearer token (no accounts configured).
func mtiPreviewViaMux(t *testing.T, api *restAPI, query string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", mtiEnvTokenValue)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	req := httptest.NewRequest(http.MethodGet, mtiPreviewPath+query, nil)
	req.RemoteAddr = uniqueTestSourceIP()
	if authed {
		req.Header.Set("Authorization", "Bearer "+mtiEnvTokenValue)
	}
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func mtiDiskGlobal(t *testing.T, api *restAPI) any {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	m := mtiDecode(t, raw)
	agents, _ := m["agents"].(map[string]any)
	defaults, _ := agents["defaults"].(map[string]any)
	return defaults["max_tool_iterations"]
}

func mtiStoredOwn(t *testing.T, api *restAPI, id string) any {
	t.Helper()
	return mtiStoredRecord(t, api, id)["max_tool_iterations"]
}

// snapshotFiles captures config.json and every agent record byte-for-byte so
// a "nothing written" assertion compares real bytes, not parsed values.
func mtiSnapshotFiles(t *testing.T, api *restAPI, ids ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	out["config.json"] = string(raw)
	for _, id := range ids {
		rec, err := os.ReadFile(filepath.Join(api.homePath, "entities", "agents", id+".json"))
		require.NoError(t, err)
		out[id] = string(rec)
	}
	return out
}

// securityChanges returns every security_setting_change record from BOTH
// production audit sinks a handler can reach (the agent loop's logger and
// restAPI.auditor) — the spec pins the emitter (audit.EmitSecuritySettingChange),
// not which of the two loggers it is handed.
func mtiSecurityChanges(t *testing.T, api *restAPI, extraDirs ...string) []map[string]any {
	t.Helper()
	dirs := append([]string{filepath.Join(filepath.Dir(api.agentLoop.GetConfig().AgentHomeBasePath()), "system")}, extraDirs...)
	out := make([]map[string]any, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, readAuditEntries(t, d, "security_setting_change")...)
	}
	return out
}

// mtiLowering is one MaxToolIterationAgentChange as JSON (spec contract).
type mtiLowering struct {
	id, name   string
	oldV, newV int
}

// assertAgentChanges asserts an array of MaxToolIterationAgentChange as an
// order-independent exact set (all four fields, no extras).
func mtiAssertAgentChanges(t *testing.T, got any, want []mtiLowering) {
	t.Helper()
	arr, ok := got.([]any)
	require.True(t, ok, "expected a JSON array of agent changes, got %T (%v)", got, got)
	gotNorm := make([]string, 0, len(arr))
	wantNorm := make([]string, 0, len(want))
	for _, e := range arr {
		m, _ := e.(map[string]any)
		require.Len(t, m, 4, "MaxToolIterationAgentChange has exactly 4 fields (additionalProperties:false): %v", m)
		gotNorm = append(gotNorm, fmt.Sprintf("%v|%v|%v|%v", m["agent_id"], m["agent_name"], m["old_value"], m["new_value"]))
	}
	for _, w := range want {
		wantNorm = append(wantNorm, fmt.Sprintf("%s|%s|%d|%d", w.id, w.name, w.oldV, w.newV))
	}
	sort.Strings(gotNorm)
	sort.Strings(wantNorm)
	assert.Equal(t, wantNorm, gotNorm)
}

// mtiLogCapture captures BOTH log paths the implementation may use: the
// default slog logger and pkg/logger's zerolog file sink.
type mtiLogCapture struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	file string
}

func (c *mtiLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *mtiLogCapture) warnLines() []string {
	c.mu.Lock()
	all := c.buf.String()
	c.mu.Unlock()
	if raw, err := os.ReadFile(c.file); err == nil {
		all += "\n" + string(raw)
	}
	var out []string
	for _, l := range strings.Split(all, "\n") {
		if strings.Contains(l, "level=WARN") || strings.Contains(l, `"level":"warn"`) {
			out = append(out, l)
		}
	}
	return out
}

func captureMTILogs(t *testing.T) *mtiLogCapture {
	t.Helper()
	c := &mtiLogCapture{file: filepath.Join(t.TempDir(), "mti.log")}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(c, &slog.HandlerOptions{Level: slog.LevelDebug})))
	prevLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	require.NoError(t, logger.EnableFileLogging(c.file))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
		slog.SetDefault(prev)
	})
	return c
}

// ---------------------------------------------------------------------------
// GET /api/v1/performance (US-1 AS-1, US-7 AS-4/5, D13, FR-012)
// ---------------------------------------------------------------------------

// Scenario "Fresh install shows the shipped global in Settings".
func TestGetPerformance_MaxToolIterations_FreshInstall(t *testing.T) {
	api := newMTIAPI(t, "200")
	m := mtiGetPerf(t, api)
	assert.EqualValues(t, 200, m["max_tool_iterations"])
	assert.Equal(t, "ok", m["max_tool_iterations_saved_state"])
	_, hasRaw := m["max_tool_iterations_saved_raw"]
	assert.False(t, hasRaw, "max_tool_iterations_saved_raw is present only for below_min/above_max")
}

// TestEffectiveGlobal_SavedStates — Dataset "Saved global" rows 1–7 via the
// wire (test plan row 2). Scenario "Saved global out of range is corrected
// in memory only": effective + saved_state (+ raw), the file is NOT
// rewritten, and a WARN is logged for every non-ok row.
func TestEffectiveGlobal_SavedStates(t *testing.T) {
	cases := []struct {
		row       int
		saved     string // "" = key missing
		wantEff   int
		wantState string
		wantRaw   any // nil = absent
	}{
		{1, "200", 200, "ok", nil},
		{2, "", 200, "missing", nil},
		{3, "0", 200, "below_min", float64(0)},
		{4, "-4", 200, "below_min", float64(-4)},
		{5, "1000", 1000, "ok", nil},
		{6, "1001", 1000, "above_max", float64(1001)},
		{7, "5000", 1000, "above_max", float64(5000)},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("row%d_%s", tc.row, tc.saved), func(t *testing.T) {
			logs := captureMTILogs(t)
			api := newMTIAPI(t, tc.saved)
			before := mtiSnapshotFiles(t, api)
			m := mtiGetPerf(t, api)
			assert.EqualValues(t, tc.wantEff, m["max_tool_iterations"], "effective global in force")
			assert.Equal(t, tc.wantState, m["max_tool_iterations_saved_state"])
			raw, hasRaw := m["max_tool_iterations_saved_raw"]
			if tc.wantRaw == nil {
				assert.False(t, hasRaw, "saved_raw must be absent for state %s", tc.wantState)
			} else {
				assert.True(t, hasRaw, "saved_raw must carry the value found in config.json")
				assert.Equal(t, tc.wantRaw, raw)
			}
			assert.Equal(t, before, mtiSnapshotFiles(t, api), "config.json must never be rewritten by the correction (D13)")
			if tc.wantState != "ok" {
				warns := logs.warnLines()
				found := false
				for _, l := range warns {
					if strings.Contains(l, "max_tool_iterations") &&
						(tc.saved == "" || strings.Contains(l, tc.saved)) {
						found = true
					}
				}
				assert.True(t, found, "a WARN naming max_tool_iterations (and the saved value %q) must be logged; WARN lines: %v", tc.saved, warns)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PUT /api/v1/performance — the global itself (US-1, FR-001/005/006)
// ---------------------------------------------------------------------------

// Scenario "Admin raises the global and every non-overriding agent follows"
// (REST legs) + FR-005 reload.
func TestPerformancePut_MaxToolIterations_RaiseAndReload(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"}, mtiAgent{id: "agent-b"})
	var reloads atomic.Int32
	api.agentLoop.SetReloadFunc(func() error { reloads.Add(1); return nil })

	w := mtiPutPerf(t, api, `{"max_tool_iterations":350}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.EqualValues(t, 350, mtiDecode(t, w.Body.Bytes())["max_tool_iterations"])
	assert.EqualValues(t, 350, mtiDiskGlobal(t, api), "config.json agents.defaults.max_tool_iterations must hold 350")
	assert.Equal(t, int32(1), reloads.Load(), "a changed global must end with exactly one registry reload (FR-005)")
	for _, id := range []string{"agent-a", "agent-b"} {
		mtiAssertLimitFields(t, mtiGetAgent(t, api, id), 350, "global", -1, false)
	}
	assert.EqualValues(t, 350, mtiGetPerf(t, api)["max_tool_iterations"])
}

// Scenario "Global out of range is refused" — Dataset "Global bounds" rows
// 3–6; rows 1–2 are the valid edges.
func TestPerformancePut_MaxToolIterations_BoundsDataset(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantOK  bool
		wantMsg string // "" = status only (request-validation path)
	}{
		{"row1_min_1", "1", true, ""},
		{"row2_max_1000", "1000", true, ""},
		{"row3_zero", "0", false, "max_tool_iterations must be between 1 and 1000"},
		{"row4_negative", "-1", false, "max_tool_iterations must be between 1 and 1000"},
		{"row5_1001", "1001", false, "max_tool_iterations must be between 1 and 1000"},
		{"row6_non_integer", "2.5", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newMTIAPI(t, "200")
			before := mtiSnapshotFiles(t, api)
			w := mtiPutPerf(t, api, `{"max_tool_iterations":`+tc.value+`}`)
			if tc.wantOK {
				require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
				assert.Equal(t, tc.value, fmt.Sprint(mtiDiskGlobal(t, api)))
				return
			}
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			if tc.wantMsg != "" {
				assert.Equal(t, tc.wantMsg, mtiErrorText(t, w))
			}
			assert.Equal(t, before, mtiSnapshotFiles(t, api), "a refused PUT must leave config.json byte-identical (still 200)")
		})
	}
}

// Scenario "PUT without step-up token is refused".
func TestPerformancePut_MaxToolIterations_NoStepUp(t *testing.T) {
	withEdition(t, config.EditionCore)
	api := newMTIAPI(t, "200")
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(`{"max_tool_iterations":150}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.EqualValues(t, 200, mtiDiskGlobal(t, api))
}

// Regression table: a limit-only PUT leaves goal_max_rounds untouched, and
// a goal-only PUT leaves the limit untouched.
func TestPerformancePut_MaxToolIterations_PartialUpdates(t *testing.T) {
	api := newMTIAPI(t, "200")
	require.Equal(t, http.StatusOK, mtiPutPerf(t, api, `{"goal_max_rounds":7}`).Code)
	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	m := mtiGetPerf(t, api)
	assert.EqualValues(t, 7, m["goal_max_rounds"], "a limit-only PUT must not touch goal_max_rounds")
	assert.EqualValues(t, 300, m["max_tool_iterations"])
	require.Equal(t, http.StatusOK, mtiPutPerf(t, api, `{"goal_max_rounds":9}`).Code)
	assert.EqualValues(t, 300, mtiGetPerf(t, api)["max_tool_iterations"], "a goal-only PUT must not touch the limit")
}

// Edge case "Reload fails after the global is written → 500" (precedent
// rest_context_settings.go): the value IS written, the response says the
// reload failed.
func TestPerformancePut_MaxToolIterations_ReloadFailure500(t *testing.T) {
	api := newMTIAPI(t, "200")
	api.agentLoop.SetReloadFunc(func() error { return fmt.Errorf("injected reload failure") })
	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	assert.EqualValues(t, 300, mtiDiskGlobal(t, api), "the global is written before the reload")
	assert.Contains(t, strings.ToLower(mtiErrorText(t, w)), "reload")
}

// ---------------------------------------------------------------------------
// GET /api/v1/performance/max-tool-iterations/preview (US-6 AS-1, FR-018)
// ---------------------------------------------------------------------------

// Scenario "Lowering preview lists only agents above the new value".
func TestPerformancePreview_ListsOnlyAgentsAbove(t *testing.T) {
	api := newMTIAPI(t, "300",
		mtiAgent{id: "agent-a", own: 250}, mtiAgent{id: "agent-b", own: 100},
		mtiAgent{id: "agent-c", own: 200}, mtiAgent{id: "agent-d"})
	before := mtiSnapshotFiles(t, api, "agent-a", "agent-b", "agent-c", "agent-d")
	w := mtiPreviewViaMux(t, api, "?value=200", true)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	assert.EqualValues(t, 200, m["value"])
	mtiAssertAgentChanges(t, m["agents"], []mtiLowering{{"agent-a", "Agent agent-a", 250, 200}})
	assert.Equal(t, before, mtiSnapshotFiles(t, api, "agent-a", "agent-b", "agent-c", "agent-d"), "the preview is read-only")
}

// Preview with nothing affected returns an EMPTY array (required field).
func TestPerformancePreview_NoneAffected_EmptyList(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 100})
	w := mtiPreviewViaMux(t, api, "?value=150", true)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	arr, ok := m["agents"].([]any)
	require.True(t, ok, "agents is required and must be an array even when empty; got %v", m["agents"])
	assert.Empty(t, arr)
}

// Contract: preview value missing/out of range → 400 `value must be between 1 and 1000`.
func TestPerformancePreview_BadValue400(t *testing.T) {
	for _, q := range []string{"", "?value=0", "?value=1001", "?value=-1", "?value=abc"} {
		t.Run("q="+q, func(t *testing.T) {
			api := newMTIAPI(t, "300")
			w := mtiPreviewViaMux(t, api, q, true)
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, "value must be between 1 and 1000", mtiErrorText(t, w))
		})
	}
}

// Scenario "Preview is blocked under dev-mode bypass" (503, no agent data).
func TestPerformancePreview_Bypass503(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250})
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	bypassCfg := &config.Config{}
	bypassCfg.Gateway.DevModeBypass = true
	req := httptest.NewRequest(http.MethodGet, mtiPreviewPath+"?value=100", nil)
	req.Header.Set("Authorization", "Bearer mti-bypass-sentinel")
	req.RemoteAddr = uniqueTestSourceIP()
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, bypassCfg))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "agent-a", "no agent data may be returned under bypass")
}

// US-1 AS-5: unauthenticated preview → 401.
func TestPerformancePreview_Unauthenticated401(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250})
	w := mtiPreviewViaMux(t, api, "?value=100", false)
	require.Equal(t, http.StatusUnauthorized, w.Code, "body: %s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "agent-a")
}

// ---------------------------------------------------------------------------
// D11 confirmed lowering + D16 drift (US-6, FR-010)
// ---------------------------------------------------------------------------

// Scenario "Confirmed lowering rewrites and audits".
func TestPerformancePut_Lowering_ConfirmedRewritesAndAudits(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250}, mtiAgent{id: "agent-b", own: 100})
	extraAudit := attachTestAuditor(t, api)
	w := mtiPutPerf(t, api, `{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	assert.EqualValues(t, 200, mtiStoredOwn(t, api, "agent-a"), "A must be lowered to the new global")
	assert.EqualValues(t, 100, mtiStoredOwn(t, api, "agent-b"), "B (below) must be untouched")
	assert.EqualValues(t, 200, mtiDiskGlobal(t, api))
	mtiAssertAgentChanges(t, mtiDecode(t, w.Body.Bytes())["max_tool_iterations_lowered_agents"],
		[]mtiLowering{{"agent-a", "Agent agent-a", 250, 200}})

	recs := mtiSecurityChanges(t, api, extraAudit)
	require.Len(t, recs, 2, "exactly one audit record for A and one for the global; got %v", recs)
	var agentRec, globalRec map[string]any
	for _, r := range recs {
		if strings.Contains(fmt.Sprint(r["resource"]), "agent-a") {
			agentRec = r
		} else {
			globalRec = r
		}
	}
	require.NotNil(t, agentRec, "an audit record naming agent-a must exist; got %v", recs)
	require.NotNil(t, globalRec, "an audit record for the global must exist; got %v", recs)
	assert.EqualValues(t, 250, agentRec["old_value"])
	assert.EqualValues(t, 200, agentRec["new_value"])
	assert.Equal(t, reauthGateAdminUser, agentRec["actor"], "actor must be the admin who confirmed")
	assert.EqualValues(t, 300, globalRec["old_value"])
	assert.EqualValues(t, 200, globalRec["new_value"])
}

// TestPerformancePut_LoweringDrift_ConfirmedLoweringDataset — Dataset
// "Confirmed lowering" rows 1–7 (live A=250, B=280; global 300 → PUT 200).
func TestPerformancePut_LoweringDrift_ConfirmedLoweringDataset(t *testing.T) {
	wantPreview := []mtiLowering{{"agent-a", "Agent agent-a", 250, 200}, {"agent-b", "Agent agent-b", 280, 200}}
	cases := []struct {
		row       int
		confirmed string // raw JSON value of confirmed_lowering; "" = field absent
		wantCode  int
	}{
		{1, `[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":280}]`, http.StatusOK},
		{2, `[{"agent_id":"agent-b","old_value":280},{"agent_id":"agent-a","old_value":250}]`, http.StatusOK},
		{3, `[{"agent_id":"agent-a","old_value":250}]`, http.StatusConflict},
		{4, `[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":280},{"agent_id":"agent-c","old_value":260}]`, http.StatusConflict},
		{5, `[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":270}]`, http.StatusConflict},
		{6, "", http.StatusConflict},
		{7, `[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":280}]`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("row%d", tc.row), func(t *testing.T) {
			api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250}, mtiAgent{id: "agent-b", own: 280})
			extraAudit := attachTestAuditor(t, api)
			before := mtiSnapshotFiles(t, api, "agent-a", "agent-b")
			body := `{"max_tool_iterations":200`
			if tc.confirmed != "" {
				body += `,"confirmed_lowering":` + tc.confirmed
			}
			w := mtiPutPerf(t, api, body+`}`)
			require.Equal(t, tc.wantCode, w.Code, "body: %s", w.Body.String())
			switch tc.wantCode {
			case http.StatusOK:
				assert.EqualValues(t, 200, mtiStoredOwn(t, api, "agent-a"))
				assert.EqualValues(t, 200, mtiStoredOwn(t, api, "agent-b"))
				assert.EqualValues(t, 200, mtiDiskGlobal(t, api))
				mtiAssertAgentChanges(t, mtiDecode(t, w.Body.Bytes())["max_tool_iterations_lowered_agents"], wantPreview)
			case http.StatusConflict:
				m := mtiDecode(t, w.Body.Bytes())
				assert.Equal(t, mtiDriftCode, m["code"])
				assert.Equal(t, mtiDriftMsg(200), m["error"])
				preview, _ := m["preview"].(map[string]any)
				require.NotNil(t, preview, "409 must carry the fresh preview")
				assert.EqualValues(t, 200, preview["value"])
				mtiAssertAgentChanges(t, preview["agents"], wantPreview)
				assert.Equal(t, before, mtiSnapshotFiles(t, api, "agent-a", "agent-b"), "drift: nothing written")
				assert.Empty(t, mtiSecurityChanges(t, api, extraAudit), "drift: no audit record")
			case http.StatusBadRequest:
				assert.Equal(t, "confirmed_lowering lists agent agent-a more than once", mtiErrorText(t, w))
				assert.Equal(t, before, mtiSnapshotFiles(t, api, "agent-a", "agent-b"))
			}
		})
	}
}

// Scenario "Drift between preview and confirm refuses and returns the fresh list".
func TestPerformancePut_LoweringDrift_OtherAgentChanged(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250}, mtiAgent{id: "agent-b", own: 150})
	extraAudit := attachTestAuditor(t, api)
	// The admin previewed value 200 and saw only [A 250 → 200].
	pw := mtiPreviewViaMux(t, api, "?value=200", true)
	require.Equal(t, http.StatusOK, pw.Code, "body: %s", pw.Body.String())
	mtiAssertAgentChanges(t, mtiDecode(t, pw.Body.Bytes())["agents"], []mtiLowering{{"agent-a", "Agent agent-a", 250, 200}})
	// B's own value is then changed to 220 (an ordinary agent PUT, global 300).
	require.Equal(t, http.StatusOK, putAgentJSON(t, api, "agent-b", `{"max_tool_iterations":220}`).Code)
	before := mtiSnapshotFiles(t, api, "agent-a", "agent-b")
	auditBefore := len(mtiSecurityChanges(t, api, extraAudit))

	w := mtiPutPerf(t, api, `{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`)
	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	assert.Equal(t, mtiDriftCode, m["code"])
	preview, _ := m["preview"].(map[string]any)
	require.NotNil(t, preview)
	mtiAssertAgentChanges(t, preview["agents"], []mtiLowering{
		{"agent-a", "Agent agent-a", 250, 200}, {"agent-b", "Agent agent-b", 220, 200},
	})
	assert.Equal(t, before, mtiSnapshotFiles(t, api, "agent-a", "agent-b"), "global 300, A 250, B 220 all unchanged")
	assert.Len(t, mtiSecurityChanges(t, api, extraAudit), auditBefore, "no audit record for a refused lowering")
}

// Scenario "Changed old value is drift".
func TestPerformancePut_LoweringDrift_ChangedOldValue(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250})
	require.Equal(t, http.StatusOK, putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":260}`).Code)
	w := mtiPutPerf(t, api, `{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`)
	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	preview, _ := mtiDecode(t, w.Body.Bytes())["preview"].(map[string]any)
	require.NotNil(t, preview)
	mtiAssertAgentChanges(t, preview["agents"], []mtiLowering{{"agent-a", "Agent agent-a", 260, 200}})
	assert.EqualValues(t, 260, mtiStoredOwn(t, api, "agent-a"))
	assert.EqualValues(t, 300, mtiDiskGlobal(t, api))
}

// Scenario "Lowering without confirmation is refused" — the step-up token is
// NOT consumed by the pre-check (D11/D16 write order step 2): the SAME token
// then authorises the corrected, confirmed PUT.
func TestPerformancePut_LoweringDrift_PrecheckDoesNotConsumeToken(t *testing.T) {
	withEdition(t, config.EditionCore) // requireReAuth performs the real single-use token check
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250})
	token, err := api.reauthStoreOrInit().mint(reauthGateAdminUser)
	require.NoError(t, err)

	w1 := mtiPutPerfWithToken(t, api, `{"max_tool_iterations":200}`, token)
	require.Equal(t, http.StatusConflict, w1.Code, "body: %s", w1.Body.String())
	preview, _ := mtiDecode(t, w1.Body.Bytes())["preview"].(map[string]any)
	require.NotNil(t, preview)
	mtiAssertAgentChanges(t, preview["agents"], []mtiLowering{{"agent-a", "Agent agent-a", 250, 200}})
	assert.EqualValues(t, 250, mtiStoredOwn(t, api, "agent-a"))

	w2 := mtiPutPerfWithToken(t, api,
		`{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`, token)
	require.Equal(t, http.StatusOK, w2.Code,
		"the pre-check 409 must not have consumed the step-up token; body: %s", w2.Body.String())
	assert.EqualValues(t, 200, mtiStoredOwn(t, api, "agent-a"))
}

// Scenario "Lowering with no affected agents shows no dialog" (server half):
// no confirmation needed, only the global changes.
func TestPerformancePut_Lowering_NoAgentsAffected(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 150}, mtiAgent{id: "agent-b", own: 100})
	w := mtiPutPerf(t, api, `{"max_tool_iterations":150}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.EqualValues(t, 150, mtiDiskGlobal(t, api))
	assert.EqualValues(t, 150, mtiStoredOwn(t, api, "agent-a"), "own value EQUAL to the new global is not lowered")
	assert.EqualValues(t, 100, mtiStoredOwn(t, api, "agent-b"))
	_, has := mtiDecode(t, w.Body.Bytes())["max_tool_iterations_lowered_agents"]
	assert.False(t, has, "max_tool_iterations_lowered_agents is present only when agents were lowered")
}

// Scenario "Raising again does not restore lowered values" (D11).
func TestPerformancePut_Lowering_RaiseDoesNotRestore(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250})
	require.Equal(t, http.StatusOK,
		mtiPutPerf(t, api, `{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`).Code)
	require.Equal(t, http.StatusOK, mtiPutPerf(t, api, `{"max_tool_iterations":300}`).Code)
	assert.EqualValues(t, 200, mtiStoredOwn(t, api, "agent-a"), "raising the global must not restore the old 250")
}

// Scenario "Raising the global activates a previously ignored own value" (US-5 AS-3).
func TestPerformancePut_RaiseActivatesIgnoredOwnValue(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a", own: 500})
	mtiAssertLimitFields(t, mtiGetAgent(t, api, "agent-a"), 200, "global", 500, true)
	require.Equal(t, http.StatusOK, mtiPutPerf(t, api, `{"max_tool_iterations":600}`).Code)
	mtiAssertLimitFields(t, mtiGetAgent(t, api, "agent-a"), 500, "agent", 500, false)
	assert.EqualValues(t, 500, mtiStoredOwn(t, api, "agent-a"))
}

// ---------------------------------------------------------------------------
// FR-021 rollback (test plan row 13e) — fault injection through the
// restAPI.limitAgentStore seam (maxToolIterationsAgentStore)
// ---------------------------------------------------------------------------

// mtiFaultStore wraps the REAL agent store and fails chosen MutateState
// calls by their overall call number (1-based). Counting calls, not agent
// ids, keeps the tests independent of the order the handler lowers agents
// in: "the 2nd lowering write fails" always means "one agent was already
// lowered", whichever it was.
type mtiFaultStore struct {
	*agentstore.Store
	mu     sync.Mutex
	failAt map[int]bool
	calls  int
	wrote  []string // ids of successful MutateState calls, in order
}

func (f *mtiFaultStore) MutateState(id, rev string, mutate func(*config.AgentConfig) error, soul *string) (agentstore.MutationResult, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	if f.failAt[n] {
		return agentstore.MutationResult{}, fmt.Errorf("mti injected I/O failure on write %d (%s)", n, id)
	}
	res, err := f.Store.MutateState(id, rev, mutate, soul)
	if err == nil {
		f.mu.Lock()
		f.wrote = append(f.wrote, id)
		f.mu.Unlock()
	}
	return res, err
}

func (c *mtiLogCapture) errorLines() []string {
	c.mu.Lock()
	all := c.buf.String()
	c.mu.Unlock()
	if raw, err := os.ReadFile(c.file); err == nil {
		all += "\n" + string(raw)
	}
	var out []string
	for _, l := range strings.Split(all, "\n") {
		if strings.Contains(l, "level=ERROR") || strings.Contains(l, `"level":"error"`) {
			out = append(out, l)
		}
	}
	return out
}

const mtiRollbackBody = `{"max_tool_iterations":200,"confirmed_lowering":` +
	`[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":280}]}`

// newMTIRollbackAPI: global 300, A own 250, B own 280 (spec scenario setup),
// with the fault store installed and both audit sinks attached.
func newMTIRollbackAPI(t *testing.T, failAt ...int) (*restAPI, *mtiFaultStore, string) {
	t.Helper()
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250}, mtiAgent{id: "agent-b", own: 280})
	fs := &mtiFaultStore{Store: agentstore.New(api.homePath), failAt: map[int]bool{}}
	for _, n := range failAt {
		fs.failAt[n] = true
	}
	api.limitAgentStore = fs
	return api, fs, attachTestAuditor(t, api)
}

var mtiOld = map[string]int{"agent-a": 250, "agent-b": 280}

// Scenario "Mid-write failure rolls back already-lowered agents" (US-6 AS-7,
// FR-021): the 2nd lowering write fails with an I/O error after the 1st
// succeeded → 500 max_tool_iterations_lowering_failed saying nothing was
// changed; both agents back at their old values; global 300; audit = the
// first agent's lowering + its rollback, nothing for the global.
func TestPerformancePut_LoweringRollback_MidWriteFailure(t *testing.T) {
	api, fs, extraAudit := newMTIRollbackAPI(t, 2)
	w := mtiPutPerf(t, api, mtiRollbackBody)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	assert.Equal(t, "max_tool_iterations_lowering_failed", m["code"])
	msg, _ := m["error"].(string)
	assert.Contains(t, msg, "nothing was changed")

	require.GreaterOrEqual(t, len(fs.wrote), 1, "the first lowering write must have succeeded (fault is on write 2)")
	first := fs.wrote[0]
	failed := "agent-b"
	if first == "agent-b" {
		failed = "agent-a"
	}
	assert.True(t, strings.Contains(msg, failed) || strings.Contains(msg, "Agent "+failed),
		"the error must name the failing agent %s: %q", failed, msg)

	assert.EqualValues(t, 250, mtiStoredOwn(t, api, "agent-a"), "A restored / untouched")
	assert.EqualValues(t, 280, mtiStoredOwn(t, api, "agent-b"), "B restored / untouched")
	assert.EqualValues(t, 300, mtiDiskGlobal(t, api), "the global is written only after every agent succeeded")

	recs := mtiSecurityChanges(t, api, extraAudit)
	require.Len(t, recs, 2, "audit = %s lowering + %s rollback, nothing for the global; got %v", first, first, recs)
	var lowering, rollback map[string]any
	for _, r := range recs {
		require.Contains(t, fmt.Sprint(r["resource"]), first, "every audit record is about the rolled-back agent: %v", r)
		if fmt.Sprint(r["new_value"]) == "200" {
			lowering = r
		} else {
			rollback = r
		}
	}
	require.NotNil(t, lowering, "lowering record missing: %v", recs)
	require.NotNil(t, rollback, "rollback record missing: %v", recs)
	assert.EqualValues(t, mtiOld[first], lowering["old_value"])
	assert.EqualValues(t, 200, rollback["old_value"], "rollback audit: old = the new global value")
	assert.EqualValues(t, mtiOld[first], rollback["new_value"], "rollback audit: new = the restored old value")
}

// Scenario "Failed rollback is reported, not silent" (FR-021): write 2 fails
// AND the rollback of the first agent (write 3) fails → 500
// max_tool_iterations_rollback_incomplete naming that agent (now 200, was
// old); global 300; the agent stays at 200; one ERROR line names it with old
// and current value; its lowering audit stands and no rollback record exists.
func TestPerformancePut_LoweringRollback_RollbackFails(t *testing.T) {
	logs := captureMTILogs(t)
	api, fs, extraAudit := newMTIRollbackAPI(t, 2, 3)
	w := mtiPutPerf(t, api, mtiRollbackBody)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	require.Len(t, fs.wrote, 1, "exactly one agent was lowered before the failures")
	first := fs.wrote[0]
	m := mtiDecode(t, w.Body.Bytes())
	assert.Equal(t, "max_tool_iterations_rollback_incomplete", m["code"])
	msg, _ := m["error"].(string)
	// Spec text: `limit not changed; could not restore <agent> (now <new>, was <old>)[, …] —
	// set their limits again on each agent's profile`. <agent> may be the id or display name.
	okID := fmt.Sprintf("limit not changed; could not restore %s (now 200, was %d) — set their limits again on each agent's profile", first, mtiOld[first])
	okName := fmt.Sprintf("limit not changed; could not restore Agent %s (now 200, was %d) — set their limits again on each agent's profile", first, mtiOld[first])
	assert.True(t, msg == okID || msg == okName, "error = %q\nwant %q\n  or %q", msg, okID, okName)

	assert.EqualValues(t, 200, mtiStoredOwn(t, api, first), "the unrestorable agent is left at the new value")
	assert.EqualValues(t, 300, mtiDiskGlobal(t, api), "global unchanged")

	var errLines []string
	for _, l := range logs.errorLines() {
		if strings.Contains(l, first) {
			errLines = append(errLines, l)
		}
	}
	require.Len(t, errLines, 1, "exactly one ERROR line for %s; ERROR lines: %v", first, logs.errorLines())
	assert.Contains(t, errLines[0], fmt.Sprint(mtiOld[first]), "ERROR line names the old value")
	assert.Contains(t, errLines[0], "200", "ERROR line names the current value")

	recs := mtiSecurityChanges(t, api, extraAudit)
	require.Len(t, recs, 1, "only the lowering audit record stands (no rollback record, nothing for the global): %v", recs)
	assert.Contains(t, fmt.Sprint(recs[0]["resource"]), first)
	assert.EqualValues(t, mtiOld[first], recs[0]["old_value"])
	assert.EqualValues(t, 200, recs[0]["new_value"])
}

// TestUpgradeBootLog_ListsCappedAgents — test plan row 13, Scenario "Upgrade
// keeps a stored value above the global" (D1/D19): the boot roster step
// emits EXACTLY ONE WARN line naming every capped agent (A stored 500, B
// stored 300) with the global 200, never naming C (100); stored values stay.
func TestUpgradeBootLog_ListsCappedAgents(t *testing.T) {
	mtiUnsetEnv(t)
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	store := agentstore.New(home)
	for _, a := range []mtiAgent{{"cap-alpha", 500}, {"cap-beta", 300}, {"cap-gamma", 100}} {
		rec := config.AgentConfig{ID: a.id, Name: "Name " + a.id, Type: config.AgentTypeCustom,
			Tools: coreagent.NewCustomAgentToolsCfg(), MaxToolIterations: a.own}
		require.NoError(t, store.Create(a.id, &rec))
	}
	cfgPath := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte(`{"version":`+fmt.Sprint(config.CurrentVersion)+`,"agents":{"defaults":{"max_tool_iterations":200}},"providers":[]}`), 0o600))
	cfg, err := config.LoadConfig(cfgPath)
	require.NoError(t, err)

	logs := captureMTILogs(t)
	require.NoError(t, seedAndPersistAgentRoster(cfg, home, cfgPath))

	var capped []string
	for _, l := range logs.warnLines() {
		if strings.Contains(l, "cap-alpha") || strings.Contains(l, "cap-beta") || strings.Contains(l, "cap-gamma") {
			capped = append(capped, l)
		}
	}
	require.Len(t, capped, 1, "exactly one startup WARN line for capped agents (D19); got %v", capped)
	line := capped[0]
	for _, want := range []string{"cap-alpha", "500", "cap-beta", "300", "200"} {
		assert.Contains(t, line, want)
	}
	assert.NotContains(t, line, "cap-gamma", "an agent below the global is not capped and must not be listed")

	for id, own := range map[string]int{"cap-alpha": 500, "cap-beta": 300, "cap-gamma": 100} {
		got, getErr := store.Get(id)
		require.NoError(t, getErr)
		assert.Equal(t, own, got.MaxToolIterations, "boot must never rewrite a stored own value (D1)")
	}
}

// TestMTILogCapture_Instrument proves captureMTILogs sees a WARN from both
// log paths, so an empty WARN list above means nothing was logged.
func TestMTILogCapture_Instrument(t *testing.T) {
	logs := captureMTILogs(t)
	slog.Warn("mti-probe-slog")
	logger.WarnF("mti-probe-zerolog", map[string]any{"k": "v"})
	joined := strings.Join(logs.warnLines(), "\n")
	assert.Contains(t, joined, "mti-probe-slog")
	assert.Contains(t, joined, "mti-probe-zerolog")
}
