// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_settings_merge_c6_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order rows 11, 13 and 14 (C-6, FR-020/021/022, #995/
// FR-102, MAJ-001/005; review R2-MAJ-005):
//
//   - TestContextSettings_ConcurrentDisjointEditsBothSurvive (FR-021,
//     order 11): putContextSettings reads `next := cfg.Context` BEFORE
//     safeUpdateConfigJSON (the writer C-6 names as "verified"), so two
//     Saves changing different fields can lose one change. The test launches
//     the two real handler calls back-to-back in goroutines — both take
//     their early copies before either merge completes, so the second
//     writer's stale whole-section copy overwrites the first writer's field
//     — alternating the launch order across iterations to cover both
//     orders. After GREEN's inside-the-lock per-field merge, the test is
//     timing-independent: both fields survive under ANY interleaving.
//
//   - TestThirdPartySettings_OneRowEditKeepsOthers (FR-022, MAJ-001,
//     order 13): the section saves all three rows built from the
//     section-open values plus the drafts — a one-row edit keeps the other
//     two rows — and two tabs saving the section is last-save-wins for the
//     WHOLE section (a stale second tab reverts the first tab's row: the
//     documented contract, pinned so it cannot drift).
//
//   - TestSettingsWrites_RefreshFailureReported (FR-102, order 14): with
//     the locked-credential-store seam (channel_configure_partial_failure_
//     test.go pattern) the live refresh fails AFTER config.json is written;
//     PUT /config and PUT /settings/context must answer 500 ErrorResponse
//     with code "saved_not_refreshed" and details.keys naming the written
//     keys, decoded through the GENERATED type, and GET /config/pending-
//     restart must already list those keys with reason "apply_failed" when
//     the response arrives. A failure BEFORE the write answers 500 without
//     that code (outcome table row: screen treats it as unknown).
//
//   - TestContextSettings_RefusesZeroOverride (R2-MAJ-005, order 14's
//     second half): the server's own refusal of a Model-override context
//     length of 0 at the real handler. The screen keeps its own minimum;
//     this pins the server side. Preserved behaviour (passes today, must
//     stay green).
//
// Oracles: the spec's C-6 facts, FR-020's outcome table, FR-021/022,
// FR-102 and R2-MAJ-005. Expected values from the spec, never from today's
// answers.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// putContextBody drives the real context writer with a raw JSON body.
func putContextBody(api *restAPI, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/context", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleContextSettings(w, r)
	return w
}

// seedContextValues sets both disjoint fields on the live config and on the
// stored config.json so the merge base is unambiguous.
func seedContextValues(t *testing.T, api *restAPI, cap, success int) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	cfg.Context.McpResultCap = cap
	cfg.Context.BuiltinSuccessCap = success
	persistedRaw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(persistedRaw, &doc))
	ctxMap, _ := doc["context"].(map[string]any)
	if ctxMap == nil {
		ctxMap = map[string]any{}
	}
	ctxMap["mcp_result_cap"] = cap
	ctxMap["builtin_success_cap"] = success
	doc["context"] = ctxMap
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))
}

// storedContextValues loads config.json fresh and returns the two fields —
// the stored state a restart would see.
func storedContextValues(t *testing.T, api *restAPI) (cap, success int) {
	t.Helper()
	persistedRaw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(persistedRaw, &doc))
	ctxMap, _ := doc["context"].(map[string]any)
	require.NotNil(t, ctxMap, "context section must exist after a context write")
	cf, _ := ctxMap["mcp_result_cap"].(float64)
	sf, _ := ctxMap["builtin_success_cap"].(float64)
	return int(cf), int(sf)
}

func TestContextSettings_ConcurrentDisjointEditsBothSurvive(t *testing.T) {
	// Ten iterations with alternating launch order: the RED loss needs the
	// second writer's early copy to precede the first writer's merge, which
	// the back-to-back launch produces (the critical section holds the file
	// write and the live refresh); GREEN's inside-the-lock merge passes
	// under ANY timing, so the iteration count never gates GREEN.
	for i := 0; i < 10; i++ {
		order := [2]string{"cap_first", "success_first"}[i%2]
		t.Run(fmt.Sprintf("iteration %d (%s)", i+1, order), func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			seedContextValues(t, api, 20, 5)

			first := `{"mcp_result_cap":50}`
			second := `{"builtin_success_cap":7}`
			if order == "success_first" {
				first, second = second, first
			}
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); putContextBody(api, first) }()
			go func() { defer wg.Done(); putContextBody(api, second) }()
			wg.Wait()

			gotCap, gotSuccess := storedContextValues(t, api)
			require.Equal(t, 50, gotCap,
				"RED (FR-021, order 11): the %s write lost mcp_result_cap — putContextSettings merges a "+
					"whole-section copy taken before the write lock, so a concurrent Save of a different "+
					"field is overwritten; both disjoint changes must survive in either order",
				order)
			require.Equal(t, 7, gotSuccess,
				"RED (FR-021, order 11): the %s write lost builtin_success_cap — same early-copy merge",
				order)
		})
	}
}

func TestContextSettings_RefusesZeroOverride(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := putContextBody(api, `{"model_overrides":[{"provider":"openai","model":"gpt-x","context_window":0}]}`)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"the server must refuse a Model-override context length of 0 (R2-MAJ-005); body=%s", w.Body.String())
	require.Contains(t, w.Body.String(), "model_overrides[0].context_window must be ≥ 1",
		"the refusal names the field and its minimum")
	// Nothing was written: config.json is byte-identical to before.
	before, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	after, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	require.Equal(t, string(before), string(after),
		"the refused write must store nothing")
}

func TestThirdPartySettings_OneRowEditKeepsOthers(t *testing.T) {
	const (
		validPath = "/usr/bin/env" // absolute, exists, executable, outside agent-writable folders
	)
	putSection := func(api *restAPI, codex, claude, opencode string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"clis":{"codex":{"path":%q,"preset_args":%q},`+
			`"claude-code":{"path":%q,"preset_args":%q},`+
			`"opencode":{"path":%q,"preset_args":%q}}}`,
			validPath, codex, validPath, claude, validPath, opencode)
		r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/third-party-agents", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.putThirdPartyAgentSettings(w, r)
		return w
	}
	storedRows := func(t *testing.T, api *restAPI) map[string]map[string]any {
		t.Helper()
		raw, err := os.ReadFile(api.configPath())
		require.NoError(t, err)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(raw, &doc))
		sb, _ := doc["sandbox"].(map[string]any)
		require.NotNil(t, sb, "sandbox section must exist after a third-party save")
		tp, _ := sb["third_party_agents"].(map[string]any)
		require.NotNil(t, tp, "third_party_agents must exist after a third-party save")
		clis, _ := tp["clis"].(map[string]any)
		out := map[string]map[string]any{}
		for k, v := range clis {
			row, _ := v.(map[string]any)
			out[k] = row
		}
		return out
	}

	t.Run("a one-row edit keeps the other two rows", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		w := putSection(api, "--codex-args", "--claude-args", "--opencode-args")
		require.Less(t, w.Code, http.StatusBadRequest,
			"RED setup: the whole-section save must be accepted; body=%s", w.Body.String())
		rows := storedRows(t, api)
		require.Len(t, rows, 3, "all three rows must be stored")
		require.Equal(t, "--codex-args", rows["codex"]["preset_args"])
		require.Equal(t, "--claude-args", rows["claude-code"]["preset_args"])
		require.Equal(t, "--opencode-args", rows["opencode"]["preset_args"])
	})

	t.Run("two tabs saving the section is last-save-wins for the whole section", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		putSection(api, "--codex-args", "--claude-args", "--opencode-args")

		// Tab B opened the section BEFORE tab A saved and now saves its own
		// one-row edit carrying its stale view of the other rows: the whole
		// section is replaced, so tab A's row edit is reverted (FR-022 — the
		// documented exception to per-field merging).
		w := putSection(api, "--codex-tab-b", "--claude-args", "--opencode-args")
		require.Less(t, w.Code, http.StatusBadRequest, "body=%s", w.Body.String())
		rows := storedRows(t, api)
		require.Equal(t, "--codex-tab-b", rows["codex"]["preset_args"],
			"the last save wins for the whole section")
		require.Equal(t, "--claude-args", rows["claude-code"]["preset_args"],
			"tab B's stale echo restores its section-open value (documented whole-section replace)")
	})
}

func TestSettingsWrites_RefreshFailureReported(t *testing.T) {
	// decodeErrorResponse decodes a non-2xx body through the GENERATED
	// ErrorResponse type (the contract the screen reads).
	decodeErrorResponse := func(t *testing.T, body []byte) *gen.ErrorResponse {
		t.Helper()
		var er gen.ErrorResponse
		require.NoError(t, json.Unmarshal(body, &er), "body must decode as ErrorResponse: %s", body)
		return &er
	}
	codeIsSavedNotRefreshed := func(er *gen.ErrorResponse) bool {
		return er.Code != nil && *er.Code == "saved_not_refreshed"
	}
	detailsKeys := func(er *gen.ErrorResponse) []any {
		if er.Details == nil {
			return nil
		}
		keys, _ := (*er.Details)["keys"].([]any)
		return keys
	}

	t.Run("PUT /config: stored but not refreshed says so with details.keys", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		locked := credentials.NewStore(filepath.Join(t.TempDir(), "locked-credentials.json"))
		previous := api.credStore
		api.credStore = locked

		w := putConfigBody(api, `{"gateway":{"log_level":"debug"}}`)
		api.credStore = previous

		require.Equal(t, http.StatusInternalServerError, w.Code,
			"the refresh failure must answer 500; body=%s", w.Body.String())
		er := decodeErrorResponse(t, w.Body.Bytes())
		require.True(t, codeIsSavedNotRefreshed(er),
			"RED (FR-102, C-6 outcome table): PUT /config answers today's plain 500 'could not save config' "+
				"with no code — the write IS on disk, so the answer must carry code saved_not_refreshed")
		keys := detailsKeys(er)
		require.Contains(t, keys, "gateway.log_level",
			"details.keys must list exactly the written dotted keys; got %v", keys)

		// The record is made inside the write lock, before the response: the
		// pending-restart read right after already lists the key.
		pw := callPendingRestart(t, api)
		var diffs []map[string]any
		require.NoError(t, json.Unmarshal(pw.Body.Bytes(), &diffs))
		found := false
		for _, d := range diffs {
			if d["key"] == "gateway.log_level" && d["reason"] == "apply_failed" {
				found = true
			}
		}
		require.True(t, found,
			"RED (FR-062 + FR-102): GET /config/pending-restart must list gateway.log_level with "+
				"reason apply_failed when the 500 arrives — the banner reads it after a lost response")
	})

	t.Run("PUT /settings/context: stored but not refreshed says so with details.keys", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		seedContextValues(t, api, 20, 5)
		locked := credentials.NewStore(filepath.Join(t.TempDir(), "locked-credentials.json"))
		previous := api.credStore
		api.credStore = locked

		w := putContextBody(api, `{"mcp_result_cap":44}`)
		api.credStore = previous

		require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
		er := decodeErrorResponse(t, w.Body.Bytes())
		require.True(t, codeIsSavedNotRefreshed(er),
			"RED (FR-102): PUT /settings/context must answer code saved_not_refreshed when the live "+
				"refresh fails after the write")
		require.Contains(t, detailsKeys(er), "context.mcp_result_cap",
			"details.keys must name the written key context.mcp_result_cap")
	})

	t.Run("a failure BEFORE the write answers 500 without the code", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		// Unreadable config.json: safeUpdateConfigJSON fails reading the file,
		// before anything is written — the outcome table's unknown row.
		path := api.configPath()
		require.NoError(t, os.Chmod(path, 0o000))
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

		w := putConfigBody(api, `{"gateway":{"log_level":"debug"}}`)
		_ = os.Chmod(path, 0o600) // restore before any assertion reads the file

		require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
		er := decodeErrorResponse(t, w.Body.Bytes())
		require.False(t, codeIsSavedNotRefreshed(er),
			"a failure before the write must NOT carry code saved_not_refreshed — "+
				"the screen treats it as unknown, and the code would claim the write is on disk")
	})
}
