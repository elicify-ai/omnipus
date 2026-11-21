// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_settings_writers_merge_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order row 12 (FR-021, C-6 "Section writers merge under
// the lock"; BDD "Two tabs saving different Limits fields keep both
// changes", same trace as order 11, for the SEVEN remaining Save-bar
// writers): every writer the Save bar uses MUST apply only the fields
// present in the request to the stored values read INSIDE the config write
// lock. Each subtest launches two disjoint-field writes back-to-back in
// goroutines (the order-11 pattern) and asserts both new values are stored
// in config.json — in EITHER interleaving.
//
// The disjoint pairs come from the spec (C-4/C-6 writer list) and the
// handlers' request schemas in contracts/ (PerformanceSettingsUpdate,
// MemorySettings, SandboxConfigUpdate, PromptGuardUpdateRequest — a
// single-field body, so its partner is the rate-limits field that shares the
// sandbox section — RateLimitsUpdateRequest, DefaultModelUpdateRequest — a
// single (provider, model) pair, so its partner is the memory recap_model
// field that shares agents.defaults — RetentionSettingsUpdate).
//
// RED today: /settings/retention is not implemented (FR-041/FR-043), so its
// subtest answers 404 and fails loudly naming the spec; the other six
// subtests pin the merge property (they pass on writers that already merge
// under the lock and must stay green — the C-6 record for each writer).
//
// Oracles: the spec's FR-021 and C-6 facts. Expected values from the spec
// and contracts, never from today's answers.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// seedWriterBase merges a JSON object into config.json on disk (the base
// state every subtest's two fields start from).
func seedWriterBase(t *testing.T, api *restAPI, path []string, value any) {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	m := doc
	for i, key := range path {
		if i == len(path)-1 {
			m[key] = value
			break
		}
		next, ok := m[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[key] = next
		}
		m = next
	}
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))
}

// storedWriterDoc loads config.json fresh (the state a restart would see).
func storedWriterDoc(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc
}

func digStored(t *testing.T, doc map[string]any, path ...string) any {
	t.Helper()
	m := doc
	for i, key := range path {
		v, ok := m[key]
		require.True(t, ok, "config.json must hold %v", path)
		if i == len(path)-1 {
			return v
		}
		m, ok = v.(map[string]any)
		require.True(t, ok, "%v must be an object", path[:i+1])
	}
	return nil
}

// putVia calls a handler with a raw JSON PUT body and returns the recorder.
func putVia(handler func(http.ResponseWriter, *http.Request), path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func TestSaveBarWriters_MergeUnderLock(t *testing.T) {
	// runPair forces the overlap at the read/lock seam with the FR-021 test
	// barrier (spec :1597; F10, CHECK — the plain back-to-back launch let
	// both writes run serially, so a stale pre-lock snapshot could pass): it
	// holds the config write lock, launches both writes, and gives them a
	// bounded window to finish every pre-lock step — an early-copy writer
	// takes its whole-section snapshot HERE, parked on the held lock — then
	// releases and lets the merges serialise. GREEN's inside-the-lock merge
	// reads the stored state under its own lock acquisition and passes under
	// ANY window; the window bounds arrival only and is never the oracle.
	runPair := func(api *restAPI, w1, w2 func()) {
		api.configMu.Lock()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); w1() }()
		go func() { defer wg.Done(); w2() }()
		arrival := time.Now()
		for time.Since(arrival) < 200*time.Millisecond {
			time.Sleep(5 * time.Millisecond)
		}
		api.configMu.Unlock()
		wg.Wait()
	}

	t.Run("PUT /settings/context is order 11 (covered by TestContextSettings_ConcurrentDisjointEditsBothSurvive)", func(t *testing.T) {
		t.Log("the verified early-copy writer keeps its own RED test (order 11)")
	})

	t.Run("PUT /performance merges under the lock", func(t *testing.T) {
		withEdition(t, config.EditionCore) // local mode: requireReAuth checks the minted token
		api := newTestRestAPIWithHome(t)
		seedWriterBase(t, api, []string{"planning"}, map[string]any{
			"goal_max_rounds":             6,
			"goal_reminder_quiet_seconds": 60,
		})
		runPair(api,
			func() {
				r := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(`{"goal_max_rounds":8}`))
				r.Header.Set("Content-Type", "application/json")
				r = withReAuthAdmin(t, api, r)
				w := httptest.NewRecorder()
				api.HandlePerformance(w, r)
			},
			func() {
				r := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(`{"goal_reminder_quiet_seconds":90}`))
				r.Header.Set("Content-Type", "application/json")
				r = withReAuthAdmin(t, api, r)
				w := httptest.NewRecorder()
				api.HandlePerformance(w, r)
			},
		)
		doc := storedWriterDoc(t, api)
		require.Equal(t, float64(8), digStored(t, doc, "planning", "goal_max_rounds"),
			"RED (FR-021, order 12): the quiet-window write lost goal_max_rounds — "+
				"/performance must apply only the fields present in the request to the stored values read inside the lock")
		require.Equal(t, float64(90), digStored(t, doc, "planning", "goal_reminder_quiet_seconds"),
			"RED (FR-021, order 12): the tries-per-goal write lost goal_reminder_quiet_seconds")
	})

	t.Run("PUT /settings/memory merges under the lock", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		seedWriterBase(t, api, []string{"agents", "defaults"}, map[string]any{
			"idle_timeout_minutes": 30,
			"recap_model":          "",
		})
		runPair(api,
			func() { putVia(api.HandleMemorySettings, "/api/v1/settings/memory", `{"idle_timeout_minutes":45}`) },
			func() { putVia(api.HandleMemorySettings, "/api/v1/settings/memory", `{"recap_model":"recap-a"}`) },
		)
		doc := storedWriterDoc(t, api)
		require.Equal(t, float64(45), digStored(t, doc, "agents", "defaults", "idle_timeout_minutes"),
			"RED (FR-021, order 12): the recap-model write lost idle_timeout_minutes")
		require.Equal(t, "recap-a", digStored(t, doc, "agents", "defaults", "recap_model"),
			"RED (FR-021, order 12): the idle-timeout write lost recap_model")
	})

	t.Run("PUT /security/sandbox-config merges under the lock", func(t *testing.T) {
		withEdition(t, config.EditionCore)
		api := newTestRestAPIWithHome(t)
		seedWriterBase(t, api, []string{"sandbox"}, map[string]any{
			"mode":             "enforce",
			"filesystem_model": "confined",
		})
		runPair(api,
			func() {
				r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config", strings.NewReader(`{"mode":"permissive"}`))
				r.Header.Set("Content-Type", "application/json")
				r = withReAuthAdmin(t, api, r)
				w := httptest.NewRecorder()
				api.HandleSandboxConfig(w, r)
			},
			func() {
				r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config", strings.NewReader(`{"filesystem_model":"open"}`))
				r.Header.Set("Content-Type", "application/json")
				r = withReAuthAdmin(t, api, r)
				w := httptest.NewRecorder()
				api.HandleSandboxConfig(w, r)
			},
		)
		doc := storedWriterDoc(t, api)
		require.Equal(t, "permissive", digStored(t, doc, "sandbox", "mode"),
			"RED (FR-021, order 12): the filesystem-model write lost sandbox.mode")
		require.Equal(t, "open", digStored(t, doc, "sandbox", "filesystem_model"),
			"RED (FR-021, order 12): the mode write lost sandbox.filesystem_model (D-4 row 2: the B2 key)")
	})

	t.Run("PUT /security/prompt-guard shares the sandbox section without losing a sibling field", func(t *testing.T) {
		// PromptGuardUpdateRequest has ONE field, so the disjoint partner is
		// the rate-limits field that lives under the same sandbox section: a
		// writer that replaced the whole section from a stale copy would lose
		// its sibling (C-4 table; contracts/components/schemas).
		api := newTestRestAPIWithHome(t)
		seedWriterBase(t, api, []string{"sandbox"}, map[string]any{
			"prompt_injection_level": "medium",
			"rate_limits":            map[string]any{"max_agent_llm_calls_per_hour": 100, "max_agent_tool_calls_per_minute": 50},
		})
		runPair(api,
			func() { putVia(api.HandlePromptGuard, "/api/v1/security/prompt-guard", `{"level":"high"}`) },
			func() {
				r := httptest.NewRequest(http.MethodPut, "/api/v1/security/rate-limits", strings.NewReader(`{"max_agent_tool_calls_per_minute":75}`))
				r.Header.Set("Content-Type", "application/json")
				r = withAdminRole(r)
				w := httptest.NewRecorder()
				api.HandleRateLimits(w, r)
			},
		)
		doc := storedWriterDoc(t, api)
		require.Equal(t, "high", digStored(t, doc, "sandbox", "prompt_injection_level"),
			"RED (FR-021, order 12): the rate-limits write lost sandbox.prompt_injection_level")
		require.Equal(t, float64(75), digStored(t, doc, "sandbox", "rate_limits", "max_agent_tool_calls_per_minute"),
			"RED (FR-021, order 12): the prompt-guard write lost the sibling rate-limit field")
	})

	t.Run("PUT /security/rate-limits merges under the lock", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		seedWriterBase(t, api, []string{"sandbox", "rate_limits"}, map[string]any{
			"max_agent_llm_calls_per_hour":    100,
			"max_agent_tool_calls_per_minute": 50,
		})
		runPair(api,
			func() {
				r := httptest.NewRequest(http.MethodPut, "/api/v1/security/rate-limits", strings.NewReader(`{"max_agent_llm_calls_per_hour":120}`))
				r.Header.Set("Content-Type", "application/json")
				r = withAdminRole(r)
				w := httptest.NewRecorder()
				api.HandleRateLimits(w, r)
			},
			func() {
				r := httptest.NewRequest(http.MethodPut, "/api/v1/security/rate-limits", strings.NewReader(`{"max_agent_tool_calls_per_minute":75}`))
				r.Header.Set("Content-Type", "application/json")
				r = withAdminRole(r)
				w := httptest.NewRecorder()
				api.HandleRateLimits(w, r)
			},
		)
		doc := storedWriterDoc(t, api)
		require.Equal(t, float64(120), digStored(t, doc, "sandbox", "rate_limits", "max_agent_llm_calls_per_hour"),
			"RED (FR-021, order 12): the tool-calls write lost max_agent_llm_calls_per_hour")
		require.Equal(t, float64(75), digStored(t, doc, "sandbox", "rate_limits", "max_agent_tool_calls_per_minute"),
			"RED (FR-021, order 12): the llm-calls write lost max_agent_tool_calls_per_minute")
	})

	t.Run("PUT /providers/default-model shares agents.defaults without losing a sibling field", func(t *testing.T) {
		// DefaultModelUpdateRequest is exactly the (provider, model) pair, so
		// the disjoint partner is the memory recap_model field that lives
		// under the same agents.defaults section (C-4 table; contracts).
		rows := []*config.ModelConfig{{
			Name: "mockgw", Provider: "mockgw", Model: "default",
			Models:  []string{"mock/model-a"},
			APIBase: "https://mock.example/v1", Protocol: "openai-compatible",
		}}
		api, _, _ := newDefaultModelAPI(t, config.DefaultModel{}, rows, &restMockProvider{})
		seedWriterBase(t, api, []string{"agents", "defaults"}, map[string]any{
			"default_model": map[string]any{"provider": "mockgw", "model": "default"},
			"recap_model":   "",
		})
		runPair(api,
			func() {
				putVia(api.HandleDefaultModel, "/api/v1/providers/default-model", `{"provider":"mockgw","model":"mock/model-a"}`)
			},
			func() { putVia(api.HandleMemorySettings, "/api/v1/settings/memory", `{"recap_model":"recap-a"}`) },
		)
		doc := storedWriterDoc(t, api)
		require.Equal(t, "mock/model-a", digStored(t, doc, "agents", "defaults", "default_model", "model"),
			"RED (FR-021, order 12): the memory write lost the default-model pair")
		require.Equal(t, "recap-a", digStored(t, doc, "agents", "defaults", "recap_model"),
			"RED (FR-021, order 12): the default-model write lost the sibling recap_model field")
	})

	t.Run("PUT /settings/retention merges under the lock", func(t *testing.T) {
		// The new endpoint (FR-041/FR-043) driven through the REAL production
		// route registration. RED today: 404 — the loud per-test statement
		// that /settings/retention is not implemented as a Save-bar writer.
		h := newRetentionSectionAPI(t, false)
		seedWriterBase(t, h.api, []string{"storage", "retention"}, map[string]any{
			"session_days":       90,
			"memory_retros_days": 180,
		})
		var code1, code2 int
		runPair(h.api,
			func() { code1 = h.do(http.MethodPut, "/api/v1/settings/retention", `{"session_days":30}`).Code },
			func() { code2 = h.do(http.MethodPut, "/api/v1/settings/retention", `{"memory_retros_days":400}`).Code },
		)
		require.Less(t, code1, http.StatusBadRequest,
			"BLOCKED RED (FR-041/FR-043, order 12): PUT /api/v1/settings/retention is not implemented — "+
				"required as the one retention write path (C-3); got %d", code1)
		require.Less(t, code2, http.StatusBadRequest,
			"BLOCKED RED (FR-041/FR-043, order 12): PUT /api/v1/settings/retention is not implemented; got %d", code2)
		doc := storedWriterDoc(t, h.api)
		require.Equal(t, float64(30), digStored(t, doc, "storage", "retention", "session_days"),
			"RED (FR-021, order 12): the memory-days write lost session_days")
		require.Equal(t, float64(400), digStored(t, doc, "storage", "retention", "memory_retros_days"),
			"RED (FR-021, order 12): the session-days write lost memory_retros_days")
	})
}
