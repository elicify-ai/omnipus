package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestRouting1340_ReviewUnknownCompletionSanitized(t *testing.T) {
	const canary = "sk-proj-review1340SECRET"
	for _, tc := range []struct {
		name  string
		cause error
	}{{"stream", &common.StreamProviderError{Code: "new_provider_code", Message: "request payload " + canary}}, {"plain", fmt.Errorf("provider returned request payload %s", canary)}} {
		t.Run(tc.name, func(t *testing.T) {
			_, cause := providers.NewFallbackChain(providers.NewCooldownTracker()).Execute(context.Background(), []providers.FallbackCandidate{{Provider: "openrouter", Model: "m"}}, func(context.Context, string, string) (*providers.LLMResponse, error) { return nil, tc.cause })
			outcome, state, reason := completionDisposition(turnResult{}, cause, "")
			want := "failed: " + UserMessageForCode(CodeUnknown)
			if outcome != steer.OutcomeFailed || state != session.LifecycleFailed || reason != want || strings.Contains(reason, canary) {
				t.Errorf("untrusted provider failure persisted raw: outcome=%s state=%s reason=%q, want %q", outcome, state, reason, want)
			}
			message, err := (&AgentLoop{}).completionMessage(&session.LifecycleRecord{SessionID: "child", AgentID: "mia", Generation: 1}, outcome, "", reason)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := message.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), canary) || strings.Contains(string(raw), "request payload") {
				t.Fatalf("raw provider message entered durable outbox: %s", raw)
			}
		})
	}
	curated := &curatedTurnError{text: "This Omnipus-authored refusal remains actionable."}
	_, _, got := completionDisposition(turnResult{}, curated, "")
	if got != "failed: "+curated.text {
		t.Fatalf("curated failure changed: %q", got)
	}
}

// PTA F5/SFH F6: observe the ACTUAL producer's event. Never re-create its
// Code/Message from a returned error as the test oracle.
func TestRouting1340_ReviewActualLiveAndEmptyRetryPersistence(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, badModel := range []bool{false, true} {
			name := fmt.Sprintf("empty_retry_%t_bad_model_%t", empty, badModel)
			t.Run(name, func(t *testing.T) {
				body := `{"error":{"message":"account header missing sk-proj-review1340SECRET"}}`
				code := "provider_request_rejected"
				want := "Provider OpenRouter rejected the request for model attempted (HTTP 400: Bad Request; format). Open Verbose chat for technical details."
				if badModel {
					body = `{"error":{"code":"model_not_found","message":"not found sk-proj-review1340SECRET"}}`
					code = "bad_model"
					want = "Model attempted is not offered by provider OpenRouter (bad_model). Pick a new model in the agent's settings."
				}
				cause := &common.ProviderError{Status: 400, Body: body, RequestID: "review-request"}
				err := providers.ClassifyError(cause, "openrouter", "attempted")
				f := newRouting1340ReviewFixture(t, nil, &AgentInstance{ID: "routing1340"})
				f.rf.err = err
				if empty {
					cn := &agentLoopRunTurnConductorRunIterations{rc: f.rc}
					if cn.surfaceEmptyRetryOutcome() != agentLoopRunTurnConductorRunIterationsReturn {
						t.Fatal("empty retry failure did not exit")
					}
				} else if f.rr.handleProviderResponse() != agentLoopRunTurnResponseReturn {
					t.Fatal("main failure did not exit")
				}
				p := f.errorPayload(t)
				if p.Code != code || p.Message != want || p.ProviderError == nil || p.ProviderError.Provider != "openrouter" || p.ProviderError.Model != "attempted" {
					t.Fatalf("actual live payload=%+v, want code=%s message=%q with attempt facts", p, code, want)
				}
				wire := WireLLMError(p)
				if wire.Code != code || wire.Message != want || wire.ProviderMessage == nil || !*wire.ProviderMessage || wire.Detail == nil || strings.Contains(*wire.Detail, "sk-proj") {
					t.Errorf("actual event wire loses safe sentence: %+v", wire)
				}
				entry := f.errorEntry(t)
				if entry.Content != want || entry.ErrorCode != code || !entry.ProviderMessage || entry.ErrorRetryable {
					t.Errorf("live/persisted failure diverged: %+v; want %q", entry, want)
				}
			})
		}
	}
}

func TestRouting1340_ReviewRealHookFailureIdentity(t *testing.T) {
	requests := &routing1340RequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		} // not-wire-format: vendor request inspection
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode vendor request: %v", err)
		}
		requests.mu.Lock()
		requests.models = append(requests.models, request.Model)
		requests.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"model not found"}}`))
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{Providers: []*config.ModelConfig{{Provider: "openrouter", Model: "selected-model", APIBase: srv.URL}}}
	global := &routing1340DefaultClient{}
	original := []providers.FallbackCandidate{{Provider: "openrouter", Model: "selected-model"}}
	a := &AgentInstance{ID: "routing1340", Provider: global, Model: "selected-model", Candidates: original}
	a.storeProviderPoolBuild(buildProviderPool(cfg, original, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.al.hooks = NewHookManager(nil)
	t.Cleanup(f.al.hooks.Close)
	if err := f.al.hooks.Mount(NamedHook("actual-before-llm-override", &llmObserverHook{})); err != nil {
		t.Fatal(err)
	}
	f.selectProvider(t)
	if f.rq.prepareLLMRequest() != agentLoopRunTurnRequestNext {
		t.Fatal("real hook preparation did not continue")
	}
	_, f.rf.err = f.rt.callProviderOnce(f.rf.callMessages, nil)
	sent := requests.snapshot()
	if !reflect.DeepEqual(sent, []string{"hook-model"}) || global.calls != 0 {
		t.Fatalf("actual requested model=%v global=%d", sent, global.calls)
	}
	if f.rr.handleProviderResponse() != agentLoopRunTurnResponseReturn {
		t.Fatal("failed hook-modified request did not exit")
	}
	const want = "Model hook-model is not offered by provider OpenRouter (bad_model). Pick a new model in the agent's settings."
	p := f.errorPayload(t)
	entry := f.errorEntry(t)
	if p.Message != want || p.ProviderError == nil || p.ProviderError.Model != "hook-model" || entry.Content != want || entry.ErrorCode != "bad_model" {
		t.Fatalf("actual hook attempt misattributed: payload=%+v entry=%+v", p, entry)
	}
	if !reflect.DeepEqual(original, []providers.FallbackCandidate{{Provider: "openrouter", Model: "selected-model"}}) {
		t.Fatalf("shared candidate slice was mutated: %+v", original)
	}
}

func TestRouting1340_ReviewRouteCauseIsLiveOnly(t *testing.T) {
	cause := &providers.ModelRouteUnavailableError{Provider: "openrouter", Model: "m", Cause: fmt.Errorf("invalid endpoint sk-proj-review1340SECRET")}
	f := newRouting1340ReviewFixture(t, nil, &AgentInstance{ID: "routing1340"})
	f.rf.err = cause
	if f.rr.handleProviderResponse() != agentLoopRunTurnResponseReturn {
		t.Fatal("route refusal did not end the turn")
	}
	const want = "OpenRouter cannot serve model m: no usable connection could be set up from its saved settings, so no request was sent. Check the provider's API key and endpoint settings."
	p, entry := f.errorPayload(t), f.errorEntry(t)
	wire := WireLLMError(p)
	if p.Code != "model_route_unavailable" || p.Message != want || p.ProviderError == nil || p.ProviderError.Status != 0 || entry.Content != want || entry.ErrorCode != "model_route_unavailable" || !entry.ProviderMessage {
		t.Fatalf("pre-request route facts lost or HTTP status fabricated: payload=%+v entry=%+v", p, entry)
	}
	if wire.Detail == nil || !strings.Contains(*wire.Detail, "invalid endpoint") || strings.Contains(*wire.Detail, "sk-proj") || strings.Contains(entry.Content, "invalid endpoint") || strings.Contains(entry.Content, "sk-proj") {
		t.Fatalf("constructor cause must remain scrubbed and live-only: wire=%+v entry=%+v", wire, entry)
	}
	code, fix := classifyOperatorOnlyTurnError(cause)
	_, judge, needs := judgeDispatchNeedsOperator(cause)
	text := (&TaskExecutor{}).taskOperatorFixReason("mia", cause, fix)
	if code != CodeModelRouteUnavailable || fix == operatorFixNone || !needs || !strings.Contains(judge, "openrouter") || !strings.Contains(judge, "m") || !strings.Contains(text, "openrouter") || !strings.Contains(text, "endpoint settings") {
		t.Fatalf("route refusal missing operator-only guidance: code=%s cause=%d Judge=%q task=%q", code, fix, judge, text)
	}
}

func TestRouting1340_ReviewTerminalEvidencePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       LLMErrorCode
		routing    providers.FailoverReason
	}{
		{"billing_over_ambiguous_prose", `{"error":{"message":"credit balance is too low; Model 'm' not found"}}`, CodeQuotaBilling, providers.FailoverBilling},
		{"structured_model_over_incidental_credit", `{"error":{"code":"model_not_found","message":"credit balance is too low for missing model"}}`, CodeBadModel, providers.FailoverBadModel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fe := providers.ClassifyError(&common.ProviderError{Status: 400, Body: tc.body}, "openrouter", "m")
			if fe == nil || fe.Reason != tc.routing {
				t.Fatalf("routing=%+v, want %s", fe, tc.routing)
			}
			if got := TranslateTurnError(fe); got.Code != tc.want {
				t.Fatalf("terminal=%+v, want %s", got, tc.want)
			}
		})
	}
}

func TestRouting1340_ReviewBadModelIsOperatorOnly(t *testing.T) {
	cause := &providers.FailoverError{Reason: "bad_model", Status: 400, Provider: "openrouter", Model: "actual-bad-model", Wrapped: &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found","message":"sk-proj-review1340SECRET"}}`}}
	code, fix := classifyOperatorOnlyTurnError(cause)
	if code != LLMErrorCode("bad_model") || fix == operatorFixNone {
		t.Errorf("bad_model not classified operator-only: code=%s fix=%d", code, fix)
	}
	_, judge, needs := judgeDispatchNeedsOperator(cause)
	if !needs || !strings.Contains(judge, "openrouter") || !strings.Contains(judge, "actual-bad-model") || strings.Contains(judge, "sk-proj") {
		t.Errorf("Judge must stop with named operator fix: needs=%t text=%q", needs, judge)
	}
	text := (&TaskExecutor{}).taskOperatorFixReason("mia", cause, fix)
	if !strings.Contains(text, "openrouter") || !strings.Contains(text, "actual-bad-model") || !strings.Contains(text, "settings") || strings.Contains(text, "sk-proj") {
		t.Errorf("task guidance lacks failing pair/fix: %q", text)
	}
}
