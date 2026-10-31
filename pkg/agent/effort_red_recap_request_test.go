package agent

// effort_red_recap_request_test.go — RED tests for the recap effort request.
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-71 (outline), S-78 (configured recap effort), S-97 (recap
// timing, FD-23), FR-070, FR-074 (timing rules), test order items T-C5 and
// T-L8. The ChatGPT-login output-limit half of S-71/S-89 is pinned at the
// adapter level in pkg/providers/effort_red_codex_recap_test.go.
//
// RED reasons:
//   S-71 — today's recap options carry extended_thinking and
//   extra_body.reasoning.enabled=false, and no configured effort exists; the
//   expected per-route Off encodings are never sent.
//   S-78 — recap_reasoning_effort cannot be configured (the config key does
//   not exist), so a configured effort never reaches the recap body.
//   S-97 — the injected timing seam is ignored: every try does NOT get
//   whatever remains of the overall limit (the old split+floor is still
//   there), retry pauses are unconditional sleeps, and calls start after the
//   deadline.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recapJSONResponse is a valid recap envelope so a served candidate succeeds.
const recapJSONResponse = `{"recap":"recap body","went_well":[],"needs_improvement":[],"worth_remembering":[]}`

// recapEndpoint records every serialised request body (raw JSON) with its
// arrival time. status lets a row force failures.
type recapEndpoint struct {
	mu     sync.Mutex
	bodies []map[string]any
	times  []time.Time
	status int
	srv    *httptest.Server
}

func newRecapEndpoint(t *testing.T, status int) *recapEndpoint {
	t.Helper()
	e := &recapEndpoint{status: status}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.bodies = append(e.bodies, body)
		e.times = append(e.times, time.Now())
		st := e.status
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		if st == http.StatusOK {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + jsonQuote(recapJSONResponse) + `},"finish_reason":"stop"}]}`))
		}
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (e *recapEndpoint) snapshot() ([]map[string]any, []time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]map[string]any(nil), e.bodies...), append([]time.Time(nil), e.times...)
}

func (e *recapEndpoint) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.bodies)
}

// buildEffortRecapLoop is buildRecapTestLoop with an explicit config so recap
// candidates can be pointed at recording endpoints. Returns the loop, the
// session id, the agent instance and the session store.
func buildEffortRecapLoop(t *testing.T, cfg *config.Config, prov providers.LLMProvider) (*AgentLoop, string, *AgentInstance, *session.UnifiedStore) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	if cfg.Agents.Defaults.Home == "" {
		cfg.Agents.Defaults.Home = home
	}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	if cfg.Agents.Defaults.RecapModel == "" {
		cfg.Agents.Defaults.RecapModel = "scripted-model"
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	require.NoError(t, err)
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "effort-recap-agent", Name: "EffortRecap"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, prov)
	require.NotNil(t, ag)
	ag.Home = filepath.Join(home, "agents", agentCfg.ID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentCfg.ID, agentCfg.Name)
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", agentCfg.ID)
	require.NoError(t, err)
	wu21ReaimStampWorkspace(t, store, meta.ID)
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "user", Content: "hello recap", Timestamp: time.Now().UTC(),
	}))
	return al, meta.ID, ag, store
}

// runRecapAndWait fires the idle recap and waits for the carry-forward write.
func runRecapAndWait(t *testing.T, al *AgentLoop, sessionID, home, agentID string) {
	t.Helper()
	al.CloseSession(sessionID, "idle")
	pairPath := filepath.Join(home, "workspaces", wu21ReaimWorkspace, ".omnipus", "carry-forward", agentID+".md")
	waitForCond(t, 10*time.Second, "carry-forward write", func() bool {
		data, err := os.ReadFile(pairPath)
		return err == nil && len(strings.TrimSpace(string(data))) > 0
	})
}

// effortRecapCfg builds a config whose recap primary and (optionally) fallback
// point at the given endpoint URLs.
func effortRecapCfg(primaryModel, primaryURL string, fallbackModel, fallbackURL string) *config.Config {
	cfg := &config.Config{}
	cfg.Agents.Defaults.RecapModel = primaryModel
	if primaryURL != "" {
		cfg.Providers = append(cfg.Providers, &config.ModelConfig{Provider: "openrouter", Model: primaryModel, APIBase: primaryURL})
	}
	if fallbackURL != "" {
		cfg.Providers = append(cfg.Providers, &config.ModelConfig{Provider: "openrouter", Model: fallbackModel, APIBase: fallbackURL})
		cfg.Agents.Defaults.RecapFallbackModels = config.FallbackModelSlice{{Provider: "openrouter", Model: fallbackModel}}
	}
	return cfg
}

// bodyFind walks a decoded JSON body for the first value at a dotted path.
func bodyFind(body map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = body
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// assertNoHardcodedRecapOff asserts the FR-070 removal: no extended_thinking
// option and no extra_body reasoning.enabled=false anywhere in the body.
func assertNoHardcodedRecapOff(t *testing.T, body map[string]any) {
	t.Helper()
	assert.NotContains(t, body, "extended_thinking", "the recap options no longer contain extended_thinking (S-71, FR-070)")
	// extra_body is merged into the body by the adapter; a hardcoded
	// reasoning.enabled=false would surface as a nested reasoning object.
	if rv, ok := bodyFind(body, "reasoning"); ok {
		rmap, isMap := rv.(map[string]any)
		if isMap {
			if en, has := rmap["enabled"]; has {
				assert.NotEqual(t, false, en, "the recap must not send a hardcoded reasoning.enabled=false (S-71, FR-070)")
			}
		}
	}
	if eb, ok := bodyFind(body, "extra_body"); ok {
		assert.Nil(t, eb, "the recap must not send a per-call extra_body (S-71, FR-070)")
	}
}

// TestT_C5_S71_RecapDefaultOffPerRoute drives the outline's OpenAI-compatible
// routes end to end (default Off, FD-19) and asserts the serialised bodies.
// The native-Anthropic and ChatGPT-login rows are covered at the adapter
// level (pkg/providers/effort_red_codex_recap_test.go and the anthropic wire
// tests); the "does not reason" row is a regression pin.
// Traces to: S-71 (all rows), US-1 AS7, FR-070, SC-014.
func TestT_C5_S71_RecapDefaultOffPerRoute(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		expectPath  string // dotted path expected in the serialised body
		expectValue any
		expectNone  string // dotted path that must be absent
	}{
		{
			name:        "openrouter levels incl none sends reasoning.effort none",
			model:       "deepseek/deepseek-v4.1-flash",
			expectPath:  "reasoning.effort",
			expectValue: "none",
			expectNone:  "reasoning.enabled",
		},
		{
			name:        "openrouter switch sends reasoning.enabled false",
			model:       "minimax/minimax-m3",
			expectPath:  "reasoning.enabled",
			expectValue: false,
			expectNone:  "reasoning.effort",
		},
		{
			name:       "openrouter levels without none sends no reasoning key (blocked Off, background call)",
			model:      "openai/gpt-6.1-sol",
			expectPath: "",
			expectNone: "reasoning",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ep := newRecapEndpoint(t, http.StatusOK)
			cfg := effortRecapCfg(tc.model, ep.srv.URL, "", "")
			prov := &effortScriptedProvider{content: "unused", finish: "stop"}
			al, sessionID, ag, _ := buildEffortRecapLoop(t, cfg, prov)
			runRecapAndWait(t, al, sessionID, wu21ReaimHome(ag), ag.ID)

			bodies, _ := ep.snapshot()
			require.NotEmpty(t, bodies, "the recap request must reach the candidate endpoint")
			body := bodies[0]
			assertNoHardcodedRecapOff(t, body)

			// Default output limit (L1 = recap_max_output_tokens 2048).
			mt, ok := bodyFind(body, "max_tokens")
			require.True(t, ok, "the recap body carries max_tokens (recap_max_output_tokens default)")
			assert.Equal(t, float64(2048), mt, "recap output limit default 2048 (S-71)")

			if tc.expectPath == "" {
				_, present := bodyFind(body, tc.expectNone)
				assert.False(t, present, "%s must be absent from the recap body", tc.expectNone)
				return
			}
			got, present := bodyFind(body, tc.expectPath)
			require.True(t, present, "the recap body carries %s (default Off per FD-19)", tc.expectPath)
			assert.Equal(t, tc.expectValue, got, "the recap Off encoding for %s", tc.model)
			if tc.expectNone != "" {
				_, present := bodyFind(body, tc.expectNone)
				assert.False(t, present, "%s must be absent", tc.expectNone)
			}
		})
	}
}

// TestT_C5_S71_RecapNoReasonDataSendsNothing pins the "does not reason" row:
// a model with no catalog reasoning data sends no effort key. This passes at
// RED (no reasoning data exists yet) and must keep passing.
// Traces to: S-71 row 7.
func TestT_C5_S71_RecapNoReasonDataSendsNothing(t *testing.T) {
	ep := newRecapEndpoint(t, http.StatusOK)
	cfg := effortRecapCfg("some-unknown-model", ep.srv.URL, "", "")
	prov := &effortScriptedProvider{content: "unused", finish: "stop"}
	al, sessionID, ag, _ := buildEffortRecapLoop(t, cfg, prov)
	runRecapAndWait(t, al, sessionID, wu21ReaimHome(ag), ag.ID)
	bodies, _ := ep.snapshot()
	require.NotEmpty(t, bodies)
	for _, key := range []string{"reasoning", "reasoning_effort", "thinking"} {
		_, present := bodyFind(bodies[0], key)
		assert.False(t, present, "%s must be absent when the model has no reasoning data (S-71 row 7)", key)
	}
}

// TestT_C5_S78_ConfiguredRecapEffortReachesBody pins S-78: a configured recap
// effort (recap_reasoning_effort high; fallback reasoning_effort on) is sent
// through the normal encoders. The effort is configured by hand-writing the
// key into config.json — at RED the loader drops the unknown key, so the body
// carries nothing and the test fails for the right reason (the setting does
// not exist yet).
// Traces to: S-78, FR-070 (L2 configured), FR-072, SC-014.
func TestT_C5_S78_ConfiguredRecapEffortReachesBody(t *testing.T) {
	ep := newRecapEndpoint(t, http.StatusOK)

	// Hand-write the L2 keys the Settings screen will one day save, plus the
	// provider rows pointing at the recording endpoint, then load through the
	// production loader the way the loop does at boot.
	omniHome := os.Getenv("OMNIPUS_HOME")
	require.NotEmpty(t, omniHome)
	cfgPath := filepath.Join(omniHome, "config.json")
	handWritten := map[string]any{
		"providers": []map[string]any{
			{"provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_base": ep.srv.URL},
			{"provider": "openrouter", "model": "minimax/minimax-m3", "api_base": ep.srv.URL},
		},
		"agents": map[string]any{
			"defaults": map[string]any{
				"recap_provider":         "openrouter",
				"recap_model":            "deepseek/deepseek-v4.1-flash",
				"recap_reasoning_effort": "high",
				"recap_fallback_models": []map[string]any{{
					"provider": "openrouter", "model": "minimax/minimax-m3", "reasoning_effort": "on",
				}},
			},
		},
	}
	raw, err := json.Marshal(handWritten)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))

	loaded, err := config.LoadConfig(cfgPath)
	require.NoError(t, err)

	prov := &effortScriptedProvider{content: "unused", finish: "stop"}
	al, sessionID, ag, _ := buildEffortRecapLoop(t, loaded, prov)
	runRecapAndWait(t, al, sessionID, wu21ReaimHome(ag), ag.ID)

	bodies, _ := ep.snapshot()
	require.NotEmpty(t, bodies)
	body := bodies[0]
	got, present := bodyFind(body, "reasoning.effort")
	require.True(t, present, "the configured recap effort reaches the recap body (S-78)")
	assert.Equal(t, "high", got)
	assertNoHardcodedRecapOff(t, body)
	mt, ok := bodyFind(body, "max_tokens")
	require.True(t, ok)
	assert.Equal(t, float64(2048), mt, "recap_max_output_tokens default still applies (S-78)")
}

// effortTimingProvider is the in-process primary for S-97: it records every
// received context deadline and can block until its context is cancelled
// before returning a transient stream reset.
type effortTimingProvider struct {
	mu           sync.Mutex
	calls        int
	deadlines    []time.Time
	start        time.Time
	blockN       int // block the first N calls until ctx is done, then return the transient error
	transientErr error
	blockMin     time.Duration // safety bound so RED cannot hang
}

func (p *effortTimingProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	if dl, ok := ctx.Deadline(); ok {
		p.deadlines = append(p.deadlines, dl)
	}
	p.start = time.Now()
	call := p.calls
	block := call <= p.blockN
	safety := p.blockMin
	p.mu.Unlock()

	if block {
		// Block until the caller's deadline cancels us (bounded so a RED run
		// without a deadline cannot hang the suite).
		timer := time.NewTimer(safety)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		return nil, p.transientErr
	}
	return nil, p.transientErr
}

func (p *effortTimingProvider) GetDefaultModel() string { return "timing-primary" }

func (p *effortTimingProvider) snapshot() (int, []time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]time.Time(nil), p.deadlines...)
}

// TestT_L8_S97_OneOverallTimeLimit pins the recap timing rules (FD-23): every
// try's context deadline is the recap deadline (whatever remains), a retry
// pause is cut at the deadline with no call after it.
// Traces to: S-97, FR-074 (timing), SC-020; mutation targets named in the
// spec's test hierarchy (unconditional time.Sleep, deadline checked only
// before calls, split+floor allocation restored).
func TestT_L8_S97_OneOverallTimeLimit(t *testing.T) {
	const (
		timeLimit  = 300 * time.Millisecond
		retries    = 1
		pause      = 200 * time.Millisecond
		tolerance  = 75 * time.Millisecond
		safetyWait = 2 * time.Second
	)
	transientErr := errors.New("streaming read error: http2: response body closed")

	t.Run("every try gets whatever remains and a pause that would cross the deadline is cut", func(t *testing.T) {
		fb := newRecapEndpoint(t, http.StatusOK)
		cfg := effortRecapCfg("timing-primary", "", "fb-model", fb.srv.URL)
		prov := &effortTimingProvider{
			blockN:       1, // first try blocks until its deadline, then returns the transient reset
			transientErr: transientErr,
			blockMin:     safetyWait,
		}
		al, _, ag, _ := buildEffortRecapLoop(t, cfg, prov)

		rr := &agentLoopRunRecap{
			al:        al,
			sessionID: "s97-session",
			trigger:   "test",
			agentInst: ag,
			userTurns: []string{"hello"},
		}
		start := time.Now()
		_ = rr.callCandidatesWithTiming(timeLimit, retries, pause)
		elapsed := time.Since(start)

		calls, deadlines := prov.snapshot()
		assert.LessOrEqual(t, len(deadlines), 1, "the injected time limit must be honoured; primary tries: %d", calls)
		if len(deadlines) > 0 {
			want := start.Add(timeLimit)
			assert.InDelta(t, want.UnixMilli(), deadlines[0].UnixMilli(), float64(tolerance.Milliseconds()),
				"the try's context deadline is the recap deadline (whatever remains, FD-23)")
		}
		assert.Equal(t, 1, calls, "the pause scheduled past the deadline is cut: primary called once (S-97)")
		assert.Less(t, elapsed, timeLimit+tolerance, "the recap reaches the stop by the deadline, not after the full pause schedule")
		fbBodies, _ := fb.snapshot()
		assert.Empty(t, fbBodies, "no provider call starts at or after the deadline: the fallback is never called (S-97)")
	})

	t.Run("a pause that fits before the deadline lets the retry run", func(t *testing.T) {
		fb := newRecapEndpoint(t, http.StatusOK)
		cfg := effortRecapCfg("timing-primary", "", "fb-model", fb.srv.URL)
		prov := &effortTimingProvider{
			blockN:       0, // every try returns the transient reset immediately
			transientErr: transientErr,
			blockMin:     safetyWait,
		}
		al, _, ag, _ := buildEffortRecapLoop(t, cfg, prov)
		rr := &agentLoopRunRecap{al: al, sessionID: "s97-session2", trigger: "test", agentInst: ag, userTurns: []string{"hello"}}
		_ = rr.callCandidatesWithTiming(timeLimit, retries, pause)
		calls, _ := prov.snapshot()
		assert.GreaterOrEqual(t, calls, 2, "a pause inside the deadline lets the retry run (S-97)")
	})

	t.Run("deadline is whatever remains under the real 60s setting (allocation-only)", func(t *testing.T) {
		// R4-MIN-002: with the real 60-second limit and immediate-return
		// stubs, the primary's recorded deadline must be start+60s — the
		// split-plus-floor allocation gives 30s and fails this assertion.
		fb := newRecapEndpoint(t, http.StatusOK)
		cfg := effortRecapCfg("timing-primary", "", "fb-model", fb.srv.URL)
		prov := &effortTimingProvider{blockN: 0, transientErr: transientErr, blockMin: safetyWait}
		al, _, ag, _ := buildEffortRecapLoop(t, cfg, prov)
		rr := &agentLoopRunRecap{al: al, sessionID: "s97-session3", trigger: "test", agentInst: ag, userTurns: []string{"hello"}}
		start := time.Now()
		_ = rr.callCandidatesWithTiming(60*time.Second, 0, 0)
		_, deadlines := prov.snapshot()
		require.NotEmpty(t, deadlines, "the primary receives a context deadline")
		assert.InDelta(t, start.Add(60*time.Second).UnixMilli(), deadlines[0].UnixMilli(), float64((1 * time.Second).Milliseconds()),
			"under the real 60s limit the primary's deadline is start+60s, not the old 30s split (S-97, R4-MIN-002)")
	})
}
