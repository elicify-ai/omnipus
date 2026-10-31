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
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// rejectionEndpoint answers status when the serialised body carries an effort
// field and 200 otherwise — the S-24 test provider.
type rejectionEndpoint struct {
	mu     sync.Mutex
	bodies []map[string]any
	// statuses: status returned while the request carries effort (first N),
	// then 200 with effort (for the S-25 both-fail row).
	failWithEffort int
	failWithout    int
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
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		if st == http.StatusOK {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
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

// TestT_P2_S24_RejectedEffortRetriedOnce pins FR-057: a 400 to a request
// carrying effort is retried once without it, the turn succeeds, and the two
// requests differ only in the effort fields.
// Traces to: S-24, D4 row 1, FR-057, SC-004.
func TestT_P2_S24_RejectedEffortRetriedOnce(t *testing.T) {
	ep, url := newRejectionEndpoint(t, 1, 0)
	f := newRejectionFixture(t, url, "", "", "high")

	resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err, "the resend without effort succeeds")
	require.NotNil(t, resp)
	assert.Equal(t, "ok", resp.Content)

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
	entries, err := f.store.ReadTranscript(f.sessionID)
	require.NoError(t, err)
	var record map[string]any
	for _, e := range entries {
		if e.Role == "assistant" && e.Type != session.EntryTypeToolCall {
			record = entryJSON(t, e)
		}
	}
	require.NotNil(t, record)
	eff, has := record["effort"]
	require.True(t, has, "the answering entry stores the effort record (S-24/FR-058)")
	em, ok := eff.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "high", em["chosen"])
	assert.Equal(t, "rejected", em["reason"])
	assert.Nil(t, em["sent"], "nothing was sent after the successful resend (S-24)")
}

// TestT_P2_S25_FailingRetrySurfacesOriginalError pins D4 row 3: both the
// effort request and the resend fail → the original error surfaces through
// the normal path, with no effort wording.
// Traces to: S-25, D4 row 3, FR-057.
func TestT_P2_S25_FailingRetrySurfacesOriginalError(t *testing.T) {
	ep, url := newRejectionEndpoint(t, 1, 1)
	f := newRejectionFixture(t, url, "", "", "high")

	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.Error(t, err, "both requests failed")
	bodies := ep.snapshot()
	assert.Len(t, bodies, 2, "exactly two requests are made (S-25)")
	assert.NotContains(t, err.Error(), "effort", "the surfaced error claims nothing about effort (S-25)")
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
	_, url := newRejectionEndpoint(t, 1, 0)
	f := newRejectionFixture(t, url, "", "", "high")
	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)

	entries, err := f.store.ReadTranscript(f.sessionID)
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
	// The fallback's own stored effort, hand-written the way the REST writer
	// will one day store it (dropped by today's loader — the RED reason).
	cfg.Providers = append(cfg.Providers,
		&config.ModelConfig{Provider: "openrouter", Model: "primary-m", APIBase: srvFail.URL},
		&config.ModelConfig{Provider: "openrouter", Model: "fb-model", APIBase: furl},
	)
	cfg.Agents.Defaults.RecapFallbackModels = config.FallbackModelSlice{
		{Provider: "openrouter", Model: "fb-model"},
	}
	candidates := []providers.FallbackCandidate{
		{Provider: "openrouter", Model: "primary-m"},
		{Provider: "openrouter", Model: "fb-model"},
	}
	a := &AgentInstance{ID: "effort-s53", Model: "primary-m", Candidates: candidates}
	a.storeProviderPoolBuild(buildProviderPool(cfg, candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)
	if f.rt.llmOpts == nil {
		f.rt.llmOpts = map[string]any{}
	}
	f.rt.llmOpts[effortdialect.EffortOptionKey] = "medium" // primary/chat value

	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	fbBodies := fb.snapshot()
	require.NotEmpty(t, fbBodies)
	assert.False(t, bodyHasEffort(fbBodies[0]),
		"the chat/primary value never reaches the fallback (S-53, FR-021)")
	// The fallback's OWN effort ("max") is missing at this head: the loader
	// drops fallback_models[].reasoning_effort, so nothing can carry it.
	eff, has := fbBodies[0][effortdialect.EffortOptionKey]
	require.True(t, has, "the fallback must send its own stored effort (S-53 — RED: the field cannot be stored)")
	assert.Equal(t, "max", eff)
}

// TestT_S2_S51_DefaultModelEffortUsed pins S-51: an agent with no model of
// its own uses the default model's effort. The default-model effort key is
// dropped by today's loader (field missing), so the body assertion fails RED.
// Traces to: S-51, FR-045, US-10 AS4.
func TestT_S2_S51_DefaultModelEffortUsed(t *testing.T) {
	ep, url := newRejectionEndpoint(t, 0, 0)
	cfg := &config.Config{}
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{Provider: "openrouter", Model: "primary-m", APIBase: url})
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "openrouter", Model: "primary-m"}
	a := &AgentInstance{ID: "effort-s51", Model: "primary-m", Candidates: []providers.FallbackCandidate{{Provider: "openrouter", Model: "primary-m"}}}
	a.storeProviderPoolBuild(buildProviderPool(cfg, a.Candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)

	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	bodies := ep.snapshot()
	require.NotEmpty(t, bodies)
	eff, present := bodies[0][effortdialect.EffortOptionKey]
	require.True(t, present, "the default model's effort (medium) is sent (S-51 — RED: the key cannot be stored)")
	assert.Equal(t, "medium", eff)
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
	ep, url := newRejectionEndpoint(t, 0, 0)
	cfg := &config.Config{}
	row := &config.ModelConfig{Provider: "openrouter", Model: "primary-m", APIBase: url}
	cfg.Providers = append(cfg.Providers, row)
	a := &AgentInstance{ID: "effort-s20", Model: "primary-m", Candidates: []providers.FallbackCandidate{{Provider: "openrouter", Model: "primary-m"}}}
	a.storeProviderPoolBuild(buildProviderPool(cfg, a.Candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)
	// The chat chose low…
	if f.rt.llmOpts == nil {
		f.rt.llmOpts = map[string]any{}
	}
	f.rt.llmOpts[effortdialect.EffortOptionKey] = "low"
	// …and the operator's row sets extra_body reasoning_effort=high. The
	// ModelConfig field that carries extra_body today is ExtraBody (string
	// map); set it the way the config loader does.
	row.ExtraBody = map[string]any{"reasoning_effort": "high"}

	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	bodies := ep.snapshot()
	require.NotEmpty(t, bodies)
	// extra_body is merged last and wins on a key it sets (pin).
	merged, has := bodies[0][effortdialect.EffortOptionKey]
	if has {
		assert.Equal(t, "high", merged, "extra_body wins over the chat value (S-20)")
	}

	entries, err := f.store.ReadTranscript(f.sessionID)
	require.NoError(t, err)
	var record map[string]any
	for _, e := range entries {
		if e.Role == "assistant" && e.Type != session.EntryTypeToolCall {
			record = entryJSON(t, e)
		}
	}
	require.NotNil(t, record)
	eff, has := record["effort"]
	require.True(t, has, "the override is recorded (S-20 — RED: no record)")
	em := eff.(map[string]any)
	assert.Equal(t, "low", em["chosen"])
	assert.Equal(t, "high", em["sent"])
	assert.Equal(t, "extra_body", em["reason"])
}
