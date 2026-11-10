package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// The default client is an external-boundary stand-in, not a routing mock.
// No public API, token source, or live data directory is touched by this proof.
type routing1340DefaultClient struct {
	calls  int
	models []string
}

func (p *routing1340DefaultClient) GetDefaultModel() string { return "gpt-6.1-sol" }
func (p *routing1340DefaultClient) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	p.models = append(p.models, model)
	return nil, errors.New(`codex API call: POST "https://chatgpt.com/backend-api/codex/responses": 400 Bad Request`)
}

// Oracle: #1340 Expected, and ADR-067 FR-040: the named provider serves
// its offered model, never an unrelated global-default account. Chain length
// and omission from a cloud row's manual Models list cannot change this.
func TestRouting1340_NamedProviderRequestUsesItsClient(t *testing.T) {
	previousCatalog := providers.ProviderCatalog()
	providers.SetCatalog(nil) // pinned HEAD's embedded catalogue; no live read/pull
	t.Cleanup(func() { providers.SetCatalog(previousCatalog) })

	cases := []struct {
		name     string
		model    string
		fallback bool
	}{
		{"single_unlisted_opus48", "anthropic/claude-opus-4.8", false},
		{"single_unlisted_opus46", "anthropic/claude-opus-4.6", false},
		{"single_already_listed_glm", "z-ai/glm-5.2", false},
		{"two_candidate_control_opus48", "anthropic/claude-opus-4.8", true},
		{"two_candidate_control_opus46", "anthropic/claude-opus-4.6", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			var mu sync.Mutex
			var sentModels []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected boundary request: %s %s", r.Method, r.URL.Path)
				}
				var body struct {
					Model string `json:"model"`
				} // not-wire-format: vendor request inspection
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode local provider request: %v", err)
				}
				mu.Lock()
				sentModels = append(sentModels, body.Model)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"proof","choices":[{"message":{"role":"assistant","content":"openrouter-local-answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			}))
			defer server.Close()
			cfg := &config.Config{
				Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
					DefaultModel: config.DefaultModel{Provider: "openai-chatgpt", Model: "gpt-6.1-sol"},
				}},
				Providers: []*config.ModelConfig{{
					Provider: "openrouter", Model: "z-ai/glm-5.2",
					Models:  []string{"~deepseek/deepseek-flash-latest", "z-ai/glm-5.2"},
					APIBase: server.URL + "/v1",
				}},
			}
			mc, err := ResolveModelPairCfg(cfg, "openrouter", tc.model, "")
			if err != nil {
				t.Fatalf("named pair must resolve from embedded catalogue: %v", err)
			}
			if mc.Provider != "openrouter" || mc.Model != tc.model {
				t.Fatalf("resolved pair = (%q,%q), want (openrouter,%q)", mc.Provider, mc.Model, tc.model)
			}
			var fallbacks []config.FallbackModel
			if tc.fallback {
				fallbacks = []config.FallbackModel{{Provider: "openrouter", Model: "z-ai/glm-5.2"}}
			}
			candidates := resolveAgentCandidatesWithPrimaryProvider(cfg, "openai-chatgpt", tc.model, "openrouter", fallbacks, nil)
			wantCandidates := []providers.FallbackCandidate{{Provider: "openrouter", Model: tc.model}}
			if tc.fallback {
				wantCandidates = append(wantCandidates, providers.FallbackCandidate{Provider: "openrouter", Model: "z-ai/glm-5.2"})
			}
			if !reflect.DeepEqual(candidates, wantCandidates) {
				t.Fatalf("candidates = %+v, want %+v", candidates, wantCandidates)
			}
			build := buildProviderPool(cfg, candidates, "routing1340-proof")
			if build.primaryUnknown {
				t.Fatal("configured OpenRouter was incorrectly marked unknown")
			}
			if _, ok := build.pool["openrouter"].(*providers.HTTPProvider); !ok {
				t.Fatalf("pool[openrouter] = %T, want actual HTTPProvider", build.pool["openrouter"])
			}
			defaultClient := &routing1340DefaultClient{}
			a := &AgentInstance{ID: "routing1340-proof", Model: tc.model, Provider: defaultClient, Candidates: candidates}
			a.StoreProviderPool(build.pool)
			al := &AgentLoop{cfg: cfg, fallback: providers.NewFallbackChain(providers.NewCooldownTracker())}
			ts := newTurnState(a, processOptions{NoHistory: true}, turnEventScope{})
			ts.userMessage = "hi"
			rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background()}
			rf := &agentLoopRunTurnFallbacks{rt: rt}
			ri := &agentLoopRunTurnIteration{rf: rf, messages: []providers.Message{{Role: "user", Content: "hi"}}}
			rq := &agentLoopRunTurnRequest{ri: ri}
			rr := &agentLoopRunTurnResponse{rq: rq}
			rx := &agentLoopRunTurnTools{rr: rr}
			rc := &agentLoopRunTurnConductor{rx: rx}
			rp := &agentLoopRunTurnPrepare{rc: rc}
			if flow := rp.selectTurnProvider(); flow != agentLoopRunTurnConductorNext {
				t.Fatalf("selectTurnProvider flow = %v, want next", flow)
			}
			rt.llmModel = ri.activeModel
			resp, callErr := rt.callProviderOnce(ri.messages, nil)
			mu.Lock()
			gotModels := append([]string(nil), sentModels...)
			mu.Unlock()
			t.Logf("pair=openrouter/%s manual_list_contains=%t chain_len=%d pool_client=%T selected_client=%T openrouter_calls=%d global_default_calls=%d default_models=%v error=%v", tc.model, tc.model == "z-ai/glm-5.2", len(candidates), build.pool["openrouter"], rt.activeProvider, calls.Load(), defaultClient.calls, defaultClient.models, callErr)
			if defaultClient.calls != 0 {
				t.Errorf("unrelated global-default calls = %d, want 0", defaultClient.calls)
			}
			if calls.Load() != 1 {
				t.Errorf("named OpenRouter calls = %d, want 1", calls.Load())
			}
			if !reflect.DeepEqual(gotModels, []string{tc.model}) {
				t.Errorf("outbound OpenRouter models = %v, want [%s]", gotModels, tc.model)
			}
			if callErr != nil {
				t.Errorf("offered named-provider request must succeed: %v", callErr)
			}
			if resp == nil || resp.Content != "openrouter-local-answer" {
				t.Errorf("response = %#v, want exact OpenRouter boundary answer", resp)
			}
		})
	}
}
