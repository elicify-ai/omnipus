package agent

// effort_red_request_rejection_test.go — RED tests for the rejection rule,
// per-candidate resolution, and the live per-turn effort record.
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-24, S-25, S-26, S-28, S-29 (T-P2, dataset D4); S-12, S-51,
// S-52, S-53 (T-S2); S-19, S-20, S-21 (T-R0 live rows); FR-057, FR-045,
// FR-021, FR-058, SC-004, SC-006.
//
// The internal effort option key is effortdialect.EffortOptionKey
// ("reasoning_effort") — the value GREEN's per-candidate resolver places in
// the request options and the adapters encode. Tests set it on the shared
// llmOpts seam so the rejection/encoding behaviour is observable from the
// provider boundary.
//
// RED reasons:
//   S-24/S-25/S-28 — the adapters never send the effort field, so the
//   scripted rejection condition is never produced: no retry, no rejection
//   note, no remembered rejection.
//   S-26 — no rejection ever happens, so nothing is remembered.
//   S-29 — one attempt is recorded where two are required.
//   S-12/S-51 — the agent-primary and default-model effort keys are dropped
//   by the config loader (fields do not exist), so the resolved value is
//   never sent.
//   S-53 — a fallback's own effort cannot be stored, so the fallback sends
//   nothing.
//   S-19 — pin (nothing is sent today; must stay so).
//   S-20 — the extra_body override reaches the body today (pin), but the
//   override record/note do not exist (RED).
//   S-21 — no effort records are produced, so neither turn carries one (RED).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/effortdialect"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bodyHasEffort reports whether a captured OpenAI-compatible body carries any
// effort key (the condition the S-24 provider rejects).
func bodyHasEffort(body map[string]any) bool {
	if _, ok := body[effortdialect.EffortOptionKey]; ok {
		return true
	}
	if r, ok := body["reasoning"].(map[string]any); ok {
		if _, ok := r["effort"]; ok {
			return true
		}
		if _, ok := r["enabled"]; ok {
			return true
		}
	}
	if _, ok := body["thinking"]; ok {
		return true
	}
	return false
}

// bodyEffortValues collects every effort value the body carries, in the S-17
// encodings: the flat OpenAI-compatible key and the nested OpenRouter levels
// form (reasoning.effort). Used to assert WHICH value a request carried, so
// absence of one candidate's value never contradicts presence of another's.
func bodyEffortValues(body map[string]any) []any {
	var out []any
	if v, ok := body[effortdialect.EffortOptionKey]; ok {
		out = append(out, v)
	}
	if r, ok := body["reasoning"].(map[string]any); ok {
		if v, ok := r["effort"]; ok {
			out = append(out, v)
		}
	}
	return out
}

// bodyCarriesEffortValue is a broad negative-leak detector, not a positive
// route-encoding oracle. A primary value is forbidden in either recognised
// carrier on a fallback. Positive OpenRouter checks use assertOpenRouterLevelBody.
func bodyCarriesEffortValue(body map[string]any, want string) bool {
	for _, v := range bodyEffortValues(body) {
		if s, ok := v.(string); ok && s == want {
			return true
		}
	}
	return false
}

// rejectionEndpoint answers status when the serialised body carries an effort
// field and 200 otherwise — the S-24 test provider.
type rejectionEndpoint struct {
	mu     sync.Mutex
	bodies []map[string]any
	// Statuses: the first failWithEffort requests that carry effort and the
	// first failWithout requests that do not get a 400; every other request
	// gets a 200. S-25 passes a large failWithout so both attempts fail
	// regardless of encoding.
	failWithEffort int
	failWithout    int
	// fail400s counts the 400s served so the ERROR BODY carries an
	// ORDER-BASED attempt identity: the first 400 is attempt 1's message,
	// every later 400 is attempt 2's. Effort-keyed markers cannot construct
	// "both failed" before the feature exists (no effort is sent at RED), so
	// identity rides on request order instead.
	fail400s int
}

func (e *rejectionEndpoint) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.bodies = append(e.bodies, body)
		with := bodyHasEffort(body)
		st := http.StatusOK
		switch {
		case with && e.failWithEffort > 0:
			e.failWithEffort--
			st = http.StatusBadRequest
		case !with && e.failWithout > 0:
			e.failWithout--
			st = http.StatusBadRequest
		}
		if st == http.StatusBadRequest {
			e.fail400s++
		}
		failedAttempt := e.fail400s
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		if st == http.StatusOK {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
		} else if st == http.StatusBadRequest {
			// Distinct provider error messages keyed by ORDER so the test can
			// pin WHICH attempt's failure surfaced (S-25: the original
			// attempt's error, never a later retry's).
			if failedAttempt == 1 {
				_, _ = w.Write([]byte(`{"error":{"message":"attempt-1-rejection-7731","code":"400"}}`))
			} else {
				_, _ = w.Write([]byte(`{"error":{"message":"attempt-2-rejection-9999","code":"400"}}`))
			}
		}
	}
}

func newRejectionEndpoint(t *testing.T, failWithEffort, failWithout int) (*rejectionEndpoint, string) {
	t.Helper()
	e := &rejectionEndpoint{failWithEffort: failWithEffort, failWithout: failWithout}
	srv := httptest.NewServer(e.handler(t))
	t.Cleanup(srv.Close)
	return e, srv.URL
}

func (e *rejectionEndpoint) snapshot() []map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]map[string]any(nil), e.bodies...)
}

// newRejectionFixture is the routing1340 review fixture bound to a primary
// endpoint row, with the effort option placed on the shared llmOpts seam.
func newRejectionFixture(t *testing.T, primaryURL, fallbackURL, fallbackModel string, effort string) *routing1340ReviewFixture {
	t.Helper()
	cfg := &config.Config{}
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{Provider: "openrouter", Model: "primary-m", APIBase: primaryURL})
	candidates := []providers.FallbackCandidate{{Provider: "openrouter", Model: "primary-m"}}
	if fallbackURL != "" {
		cfg.Providers = append(cfg.Providers, &config.ModelConfig{Provider: "openrouter", Model: fallbackModel, APIBase: fallbackURL})
		candidates = append(candidates, providers.FallbackCandidate{Provider: "openrouter", Model: fallbackModel})
	}
	a := &AgentInstance{ID: "effort-rej-agent", Model: "primary-m", Candidates: candidates}
	a.storeProviderPoolBuild(buildProviderPool(cfg, candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)
	if effort != "" {
		if f.rt.llmOpts == nil {
			f.rt.llmOpts = map[string]any{}
		}
		f.rt.llmOpts[effortdialect.EffortOptionKey] = effort
	}
	return f
}

// entryJSON marshals a transcript entry into a raw map for JSON-name
// assertions (the effort record reaches the answering entry as `effort`).
func entryJSON(t *testing.T, e session.TranscriptEntry) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func fullEffortRequestLoop(t *testing.T, url, effort string, extra map[string]any) (*AgentLoop, *session.UnifiedStore) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	var entity config.AgentConfig
	raw, err := json.Marshal(map[string]any{"id": testDefaultAgentID, "provider": "openrouter", "model": "primary-m", "reasoning_effort": effort})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &entity))
	window := 128000
	entity.ContextWindowOverride = &window
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.List = []config.AgentConfig{entity}
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	cfg.Providers = []*config.ModelConfig{{Provider: "openrouter", Model: "primary-m", APIBase: url, ExtraBody: extra}}
	al, store, _ := newContainmentLoop(t, cfg, &effortScriptedProvider{content: "UNEXPECTED-FALLBACK", finish: "stop"})
	return al, store
}
func fullEffortAnswer(t *testing.T, store *session.UnifiedStore) map[string]any {
	t.Helper()
	entries, err := store.ReadTranscript(containmentSessionID)
	require.NoError(t, err)
	var answer map[string]any
	for _, e := range entries {
		if e.Role == "assistant" && e.Content == "ok" {
			answer = entryJSON(t, e)
		}
	}
	require.NotNil(t, answer, "completed turn saved its actual answer")
	return answer
}

// TestT_P2_S24_RejectedEffortRetriedOnce pins FR-057: a 400 to a request
// carrying effort is retried once without it, the turn succeeds, and the two
// requests differ only in the effort fields.
// Traces to: S-24, D4 row 1, FR-057, SC-004.
func TestT_P2_S24_RejectedEffortRetriedOnce(t *testing.T) {
	captureFileLog(t)
	installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
	ep, url := newRejectionEndpoint(t, 1, 0)
	al, store := fullEffortRequestLoop(t, url, "high", nil)

	resp, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("answer"))
	require.NoError(t, err, "the resend without effort succeeds")
	assert.Equal(t, "ok", resp)

	bodies := ep.snapshot()
	require.Len(t, bodies, 2, "exactly two requests: the original and one resend (S-24)")
	assert.True(t, bodyHasEffort(bodies[0]), "the first request carries the effort field")
	assert.False(t, bodyHasEffort(bodies[1]), "the resend carries no effort field")
	// The two bodies differ ONLY in the effort fields.
	stripped := func(m map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			if k == effortdialect.EffortOptionKey || k == "reasoning" || k == "thinking" {
				continue
			}
			out[k] = v
		}
		return out
	}
	assert.Equal(t, stripped(bodies[0]), stripped(bodies[1]), "the resend differs only in the effort fields (S-24)")

	// The answering entry carries the effort record {chosen high, reason
	// rejected} and the rejection note text (FR-058); JSON-name assertion so
	// the wire/storage shape is pinned, not a struct field.
	record := fullEffortAnswer(t, store)
	eff, has := record["effort"]
	require.True(t, has, "the answering entry stores the effort record (S-24/FR-058)")
	em, ok := eff.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "high", em["chosen"])
	assert.Equal(t, "rejected", em["reason"])
	assert.NotContains(t, em, "sent", "nothing was sent after the successful resend (S-24)")
	assert.Equal(t, map[string]any{"chosen": "high", "reason": "rejected", "provider": "openrouter", "model": "primary-m"}, em)
	lines := []string{}
	for _, line := range strings.Split(slogBufForTest.String(), "\n") {
		if strings.Contains(line, "effort_rejected=true") {
			lines = append(lines, line)
		}
	}
	require.Len(t, lines, 1, "one content-free effort rejection diagnostic")
	for _, field := range []string{"400", "openrouter", "primary-m", "high"} {
		assert.Contains(t, lines[0], field)
	}
	assert.NotContains(t, lines[0], "messages")
	assert.NotContains(t, lines[0], "attempt-1-rejection-7731", "provider response body is not copied into the diagnostic")
}

// TestT_P2_S25_FailingRetrySurfacesOriginalError pins D4 row 3: both the
// effort request and the resend fail → the original attempt's error surfaces
// through the normal path, with no effort wording. The endpoint identifies
// its 400s by ORDER (attempt 1 vs attempt 2), not by effort encoding, so the
// identity control stays constructible before the feature exists.
// Traces to: S-25, D4 row 3, FR-057.
func TestT_P2_S25_FailingRetrySurfacesOriginalError(t *testing.T) {
	// A large failWithout keeps EVERY attempt failing, whatever encoding the
	// attempt carries — at GREEN both the effort request and the resend fail.
	ep, url := newRejectionEndpoint(t, 1, 1000)
	f := newRejectionFixture(t, url, "", "", "high")

	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.Error(t, err, "both requests failed")
	bodies := ep.snapshot()
	assert.Len(t, bodies, 2, "exactly two requests are made: the original and one resend (S-25)")
	assert.NotContains(t, err.Error(), "effort", "the surfaced error claims nothing about effort (S-25)")
	// Identity control: the two attempts carry distinct provider messages, so
	// surfacing attempt 2's error instead of attempt 1's is detectable.
	assert.Contains(t, err.Error(), "attempt-1-rejection-7731",
		"the ORIGINAL attempt's error surfaces (S-25)")
	assert.NotContains(t, err.Error(), "attempt-2-rejection-9999",
		"the resend's error never replaces the original (S-25)")
}

// TestT_P2_S26_RejectionRememberedUntilRestart pins FR-057: after a rejection
// whose resend succeeded, a later request to the same pair is sent without
// effort, once.
// Traces to: S-26, D4 rows 1 and 7, FR-057.
func TestT_P2_S26_RejectionRememberedUntilRestart(t *testing.T) {
	ep, url := newRejectionEndpoint(t, 1, 0)
	f := newRejectionFixture(t, url, "", "", "high")

	// First call: rejection + successful resend.
	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	require.Len(t, ep.snapshot(), 2)

	// A later request to the same pair: one request, no effort, reason
	// rejected_earlier.
	resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	bodies := ep.snapshot()
	require.Len(t, bodies, 3, "the remembered rejection sends exactly one request (S-26)")
	assert.False(t, bodyHasEffort(bodies[2]), "no effort field on the remembered rejection request")
}

// TestT_P2_S28_Only400And422TriggerRetry pins dataset D4: only 400 and 422
// to an effort-carrying request trigger the single resend.
// Traces to: S-28, D4 rows 2-6.
func TestT_P2_S28_Only400And422TriggerRetry(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantRetry bool
	}{
		{"400 retries once", http.StatusBadRequest, true},
		{"422 retries once", http.StatusUnprocessableEntity, true},
		{"401 no retry", http.StatusUnauthorized, false},
		{"403 no retry", http.StatusForbidden, false},
		{"429 no retry", http.StatusTooManyRequests, false},
		{"500 no retry", http.StatusInternalServerError, false},
		{"503 no retry", http.StatusServiceUnavailable, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ep := &rejectionEndpoint{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				ep.mu.Lock()
				ep.bodies = append(ep.bodies, body)
				st := http.StatusOK
				if bodyHasEffort(body) {
					st = tc.status
				}
				ep.mu.Unlock()
				w.WriteHeader(st)
				if st == http.StatusOK {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
				}
			}))
			t.Cleanup(srv.Close)
			f := newRejectionFixture(t, srv.URL, "", "", "high")
			_, _ = f.rt.callProviderOnce(f.rf.callMessages, nil)
			got := len(ep.snapshot())
			want := 1
			if tc.wantRetry {
				want = 2
			}
			assert.Equal(t, want, got, "status %d: %d extra request(s) (S-28)", tc.status, want-1)
		})
	}
}

// TestT_P2_S29_EachAttemptAccountedSeparately pins S-29: a rejection resend
// records two attempts, never one doubled and never one dropped.
// Traces to: S-29, FR-057, regression "per-attempt accounting".
func TestT_P2_S29_EachAttemptAccountedSeparately(t *testing.T) {
	installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
	_, url := newRejectionEndpoint(t, 1, 0)
	al, store := fullEffortRequestLoop(t, url, "high", nil)
	_, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("answer"))
	require.NoError(t, err)

	entries, err := store.ReadTranscript(containmentSessionID)
	require.NoError(t, err)
	attempts := 0
	for _, e := range entries {
		if e.Role == "assistant" && e.Type != session.EntryTypeToolCall {
			attempts++
		}
	}
	assert.Equal(t, 2, attempts, "two attempts are recorded, never one doubled and never one dropped (S-29)")
}

// TestT_S2_S52_UnsetFallbackSendsNothing pins FR-021 (regression pin at RED):
// with effort on the primary seam and an unset fallback, the fallback request
// carries no effort key once the primary fails over.
// Traces to: S-52, FR-021, SC-006.
func TestT_S2_S52_UnsetFallbackSendsNothing(t *testing.T) {
	// The primary fails unconditionally so the chain falls through to the
	// unset fallback regardless of effort support.
	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srvFail.Close)
	fb, url := newRejectionEndpoint(t, 0, 0)
	cfg := &config.Config{}
	cfg.Providers = append(cfg.Providers,
		&config.ModelConfig{Provider: "openrouter", Model: "primary-m", APIBase: srvFail.URL},
		&config.ModelConfig{Provider: "openrouter", Model: "fb-model", APIBase: url},
	)
	candidates := []providers.FallbackCandidate{
		{Provider: "openrouter", Model: "primary-m"},
		{Provider: "openrouter", Model: "fb-model"},
	}
	a := &AgentInstance{ID: "effort-s52", Model: "primary-m", Candidates: candidates}
	a.storeProviderPoolBuild(buildProviderPool(cfg, candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)
	if f.rt.llmOpts == nil {
		f.rt.llmOpts = map[string]any{}
	}
	// The primary's effort sits on the shared options seam (the S-52 mutation
	// target: sharing rt.llmOpts across candidates is exactly what must not
	// leak onto the fallback).
	f.rt.llmOpts[effortdialect.EffortOptionKey] = "max"

	resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	require.NotNil(t, resp)

	fbBodies := fb.snapshot()
	require.NotEmpty(t, fbBodies, "the fallback served the turn")
	assert.False(t, bodyHasEffort(fbBodies[0]),
		"the unset fallback sends no effort key regardless of the primary (S-52, FR-021)")
}

// TestT_S2_S53_FallbackUsesOwnSetting pins FR-021 via S-53: a fallback with a
// different level set uses only its own setting; the chat value never reaches
// it. The fallback's own effort cannot be stored at this head (the
// FallbackModel field does not exist), so the body assertion fails RED.
// Traces to: S-53, FR-021.
func TestT_S2_S53_FallbackUsesOwnSetting(t *testing.T) {
	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srvFail.Close)
	fb, furl := newRejectionEndpoint(t, 0, 0)
	cfg := &config.Config{}
	cfg.Providers = append(cfg.Providers,
		&config.ModelConfig{Provider: "openrouter", Model: "primary-m", APIBase: srvFail.URL},
		&config.ModelConfig{Provider: "openrouter", Model: "fb-model", APIBase: furl},
	)
	candidates := []providers.FallbackCandidate{
		{Provider: "openrouter", Model: "primary-m"},
		{Provider: "openrouter", Model: "fb-model"},
	}
	// Seed the agent fallback slot itself (not recap_fallback_models). Use the
	// real config decoder so today's missing field is the RED, not a missing
	// Given. NewAgentInstance captures this actual slot in sourceConfigJSON.
	var agentCfg config.AgentConfig
	require.NoError(t, json.Unmarshal([]byte(`{"id":"effort-s53","model":"primary-m","provider":"openrouter","fallback_models":[{"provider":"openrouter","model":"fb-model","reasoning_effort":"max"}]}`), &agentCfg))
	cfg.Agents.List = []config.AgentConfig{agentCfg}
	a := NewAgentInstance(&agentCfg, &cfg.Agents.Defaults, cfg, &effortScriptedProvider{content: "unused", finish: "stop"})
	require.NotNil(t, a)
	a.Candidates = candidates
	a.storeProviderPoolBuild(buildProviderPool(cfg, candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)
	if f.rt.llmOpts == nil {
		f.rt.llmOpts = map[string]any{}
	}
	// The primary's chat/primary value is Medium (S-53's given).
	f.rt.llmOpts[effortdialect.EffortOptionKey] = "medium"

	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	fbBodies := fb.snapshot()
	require.NotEmpty(t, fbBodies)
	// The chat/primary value (medium) never reaches the fallback — scoped to
	// the value, never a blanket absence, so it cannot contradict the next
	// assertion (CHECK-03).
	assert.False(t, bodyCarriesEffortValue(fbBodies[0], "medium"),
		"the chat/primary value (medium) never reaches the fallback (S-53, FR-021)")
	// The fallback's own Max was supplied above through its agent config
	// slot. It must reach OpenRouter as reasoning.effort, never inherit the
	// primary/chat Medium (CHECK-03).
	assertOpenRouterLevelBody(t, fbBodies[0], "max")
}

// TestT_S2_S51_DefaultModelEffortUsed pins S-51: an agent with no model of
// its own uses the default model's effort. The default-model effort key is
// dropped by today's loader (field missing), so the body assertion fails RED.
// Traces to: S-51, FR-045, US-10 AS4.
func TestT_S2_S51_DefaultModelEffortUsed(t *testing.T) {
	ep, url := newRejectionEndpoint(t, 0, 0)
	// Seed the default model's effort the only honest way available: a real
	// config file (version 1) that carries agents.defaults.default_model with
	// reasoning_effort medium, loaded through config.LoadConfig. Today's
	// loader silently drops that unknown key — the RED reason.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"providers": [{"provider": "openrouter", "model": "primary-m", "api_base": "` + url + `"}],
		"agents": {"defaults": {"default_model": {"provider":"openrouter", "model": "primary-m", "reasoning_effort": "medium"}}}
	}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(raw), 0o600))
	cfg, err := config.LoadConfig(cfgPath)
	require.NoError(t, err, "the seeded config loads")
	// The agent truly rides the default: its own config contains no model,
	// provider or effort. The production constructor resolves the default.
	agentCfg := &config.AgentConfig{ID: "effort-s51"}
	a := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &effortScriptedProvider{content: "unused", finish: "stop"})
	require.NotNil(t, a)
	require.Equal(t, "primary-m", a.Model)
	a.storeProviderPoolBuild(buildProviderPool(cfg, a.Candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)

	_, err = f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	bodies := ep.snapshot()
	require.NotEmpty(t, bodies)
	// S-17: the OpenRouter levels encoding is the nested reasoning.effort.
	assertOpenRouterLevelBody(t, bodies[0], "medium")
}

// TestT_R0_S19_ProviderDefaultSendsNothing pins S-19 (regression pin): with
// the provider_default value on the seam, no effort key reaches the body.
// Traces to: S-19, FR-037.
func TestT_R0_S19_ProviderDefaultSendsNothing(t *testing.T) {
	ep, url := newRejectionEndpoint(t, 0, 0)
	f := newRejectionFixture(t, url, "", "", "provider_default")
	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	bodies := ep.snapshot()
	require.NotEmpty(t, bodies)
	for _, key := range []string{"reasoning_effort", "reasoning", "thinking", "output_config"} {
		_, present := bodies[0][key]
		assert.False(t, present, "provider default sends no %s (S-19, FR-037)", key)
	}
}

// TestT_R0_S20_ExtraBodyOverrideRecorded pins S-20: the operator's extra_body
// effort reaches the final body today (pin); the override record and note do
// not exist yet (RED).
// Traces to: S-20, FR-037 (extra_body wins), FR-058.
func TestT_R0_S20_ExtraBodyOverrideRecorded(t *testing.T) {
	installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
	ep, url := newRejectionEndpoint(t, 0, 0)
	al, store := fullEffortRequestLoop(t, url, "low", map[string]any{"reasoning_effort": "high"})
	_, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("answer"))
	require.NoError(t, err)
	bodies := ep.snapshot()
	require.NotEmpty(t, bodies)
	// extra_body is merged last and wins on a key it sets (pin).
	assert.Equal(t, "high", bodies[0][effortdialect.EffortOptionKey], "operator extra_body wins on the serialized body")
	record := fullEffortAnswer(t, store)
	eff, has := record["effort"]
	require.True(t, has, "the override is recorded (S-20 — RED: no record)")
	em := eff.(map[string]any)
	assert.Equal(t, "low", em["chosen"])
	assert.Equal(t, "high", em["sent"])
	assert.Equal(t, "extra_body", em["reason"])
}
