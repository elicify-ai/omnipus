package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestRouting1340_TypedBadModelSurvivesWithoutBody(t *testing.T) {
	// Once the routing classifier made the decision, a later consumer must
	// not require the response body to classify the same typed failure.
	llm := TranslateTurnError(&providers.FailoverError{Reason: "bad_model", Provider: "openrouter", Model: "missing", Status: 400})
	const want = "Model missing is not offered by provider OpenRouter (bad_model). Pick a new model in the agent's settings."
	if llm.Code != LLMErrorCode("bad_model") || llm.Message != want || llm.Retryable || !llm.ProviderMessage {
		t.Fatalf("typed bad_model lost: %+v; want %q", llm, want)
	}
}

func TestRouting1340_TerminalMessageRequiresCompleteFacts(t *testing.T) {
	for _, tc := range []struct{ provider, model string }{{"", ""}, {"openrouter", ""}, {"", "missing"}} {
		llm := TranslateTurnError(&providers.FailoverError{
			Reason: providers.FailoverFormat, Status: 400, Provider: tc.provider, Model: tc.model,
			Wrapped: &common.ProviderError{Status: 400},
		})
		want := UserMessageForCode(CodeProviderRequestRejected)
		wire := WireLLMError(ErrorPayload{Code: string(llm.Code), Message: llm.Message, ProviderError: &ProviderError{Status: 400, Provider: tc.provider, Model: tc.model}})
		if llm.Code != LLMErrorCode("provider_request_rejected") || llm.Message != want || llm.ProviderMessage || (wire.ProviderMessage != nil && *wire.ProviderMessage) {
			t.Errorf("partial facts must keep catalogue message without trust flag: llm=%+v wire=%+v", llm, wire)
		}
	}
}

func TestRouting1340_Residual400PreservesMediaOutcomeRelabel(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	meta, err := store.NewSession(session.SessionTypeChat, "web", "routing1340")
	if err != nil {
		t.Fatal(err)
	}
	ts := newTurnState(&AgentInstance{ID: "routing1340"}, processOptions{}, turnEventScope{})
	ts.transcriptStore, ts.transcriptSessionID = store, meta.ID
	// The successful strip-retry's authoritative outcome stamp. A later
	// inconclusive400 must retain its old media label despite terminal copy.
	ts.setOutcomeRelabel(CodeMediaUnsupported)
	rt := &agentLoopRunTurn{al: &AgentLoop{}, ts: ts, turnCtx: context.Background()}
	rf := &agentLoopRunTurnFallbacks{rt: rt, err: &providers.FailoverError{
		Reason: providers.FailoverFormat, Status: 400, Provider: "openrouter", Model: "requested",
		Wrapped: &common.ProviderError{Status: 400},
	}}
	rr := &agentLoopRunTurnResponse{rq: &agentLoopRunTurnRequest{ri: &agentLoopRunTurnIteration{rf: rf}}}
	if rr.handleProviderResponse() != agentLoopRunTurnResponseReturn {
		t.Fatal("failed turn did not exit")
	}
	entries, err := store.ReadTranscript(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d, want1", len(entries))
	}
	entry := entries[0]
	if entry.ErrorCode != string(CodeMediaUnsupported) || entry.Content != UserMessageForCode(CodeMediaUnsupported) || entry.ProviderMessage {
		t.Fatalf("terminal400 replaced successful media-recovery label: %+v", entry)
	}
}

func TestRouting1340_DelegatedProviderFailureIsSanitized(t *testing.T) {
	err := &providers.FailoverError{
		Reason: "bad_model", Status: 400, Provider: "openrouter", Model: "missing",
		Wrapped: &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found","message":"sk-proj-routing1340canary"}}`},
	}
	outcome, state, reason := completionDisposition(turnResult{}, err, "")
	const want = "failed: Model missing is not offered by provider OpenRouter (bad_model). Pick a new model in the agent's settings."
	if outcome != steer.OutcomeFailed || state != session.LifecycleFailed || reason != want || strings.Contains(reason, "sk-proj") {
		t.Fatalf("delegate failure leaks raw provider data or loses classification: outcome=%s state=%s reason=%q; want %q", outcome, state, reason, want)
	}
}
