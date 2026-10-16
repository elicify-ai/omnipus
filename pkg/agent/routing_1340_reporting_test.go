package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

type routing1340Transport func(*http.Request) (*http.Response, error)

func (f routing1340Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Only the network edge is fake. The SDK, chain, classifiers, turn failure
// handler and transcript store are real. Oracles: dispatch R3-R5 and ADR-051 RD5.
func routing1340SDKError(t *testing.T, body string) error {
	t.Helper()
	client := openai.NewClient(
		option.WithBaseURL("https://fixture.invalid/codex"),
		option.WithAPIKey("unused-in-memory-fixture"),
		option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{Transport: routing1340Transport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"routing-1340-request"}},
				Body:       io.NopCloser(strings.NewReader(body)), Request: req,
			}, nil
		})}),
	)
	stream := client.Responses.NewStreaming(context.Background(), responses.ResponseNewParams{Model: "gpt-6.1-sol"})
	for stream.Next() {
	}
	err := stream.Err()
	if closeErr := stream.Close(); closeErr != nil {
		t.Fatalf("SDK fixture close: %v", closeErr)
	}
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("fixture did not produce SDK HTTP400: %T %v", err, err)
	}
	return fmt.Errorf("codex API call: %w", err)
}

func TestRouting1340_Provider400Reporting(t *testing.T) {
	const model = "anthropic/claude-opus-4.6"
	const provider = "openrouter"
	t.Run("instrument_control_structured_401", func(t *testing.T) {
		got := TranslateTurnError(&providers.FailoverError{
			Reason: providers.FailoverAuth, Provider: provider, Model: model, Status: http.StatusUnauthorized,
			Wrapped: &common.ProviderError{Status: http.StatusUnauthorized},
		})
		if got.Code != CodeProviderAuthFailed {
			t.Fatalf("401 control code=%q, want provider_auth_failed", got.Code)
		}
	})
	cases := []struct {
		name   string
		err    error
		reason string
	}{
		{"sdk400_empty_body", routing1340SDKError(t, ""), "format"},
		{"structured400_empty_body", &common.ProviderError{Status: http.StatusBadRequest}, "format"},
		{"sdk400_bad_model", routing1340SDKError(t, `{"error":{"code":"model_not_found","message":"model unavailable sk-proj-routing1340canary"}}`), "bad_model"},
		{"structured400_bad_model", &common.ProviderError{Status: http.StatusBadRequest, Body: `{"error":{"code":"model_not_found","message":"model unavailable sk-proj-routing1340canary"}}`}, "bad_model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			_, chainErr := providers.NewFallbackChain(providers.NewCooldownTracker()).Execute(
				context.Background(), []providers.FallbackCandidate{{Provider: provider, Model: model}},
				func(context.Context, string, string) (*providers.LLMResponse, error) {
					calls++
					return nil, tc.err
				},
			)
			if chainErr == nil || calls != 1 {
				t.Fatalf("expected one failed request, calls=%d err=%v", calls, chainErr)
			}
			var classified *providers.FailoverError
			if !errors.As(chainErr, &classified) || string(classified.Reason) != tc.reason || classified.Status != 400 {
				t.Errorf("terminal classification=%+v, want %s/status400", classified, tc.reason)
			}
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
			rt := &agentLoopRunTurn{al: &AgentLoop{}, ts: ts, turnCtx: context.Background(), llmModel: model}
			rf := &agentLoopRunTurnFallbacks{rt: rt, err: chainErr}
			ri := &agentLoopRunTurnIteration{rf: rf}
			rr := &agentLoopRunTurnResponse{rq: &agentLoopRunTurnRequest{ri: ri}}
			if flow := rr.handleProviderResponse(); flow != agentLoopRunTurnResponseReturn || rr.ret1 == nil {
				t.Fatalf("failed turn flow=%v err=%v", flow, rr.ret1)
			}
			llm := TranslateTurnError(rr.ret1)
			pe := errorToProviderError(chainErr)
			wire := WireLLMError(ErrorPayload{Code: string(llm.Code), Message: llm.Message, ProviderError: pe})
			text := userVisibleTurnError(rr.ret1)
			if llm.Code == CodeUnknown || wire.Code != string(llm.Code) || wire.Message != text {
				t.Errorf("known400 not preserved at wire: llm=%+v wire=%+v", llm, wire)
			}
			for _, want := range []string{provider, model, tc.reason} {
				if !strings.Contains(strings.ToLower(text), want) {
					t.Errorf("safe terminal text lacks %q: %q", want, text)
				}
			}
			if tc.reason == "format" && (!strings.Contains(text, "400") || !strings.Contains(text, "Bad Request")) {
				t.Errorf("generic400 must name known HTTP rejection, not invent a model error: %q", text)
			}
			if wire.ProviderMessage == nil || !*wire.ProviderMessage {
				t.Error("wire must mark the safely assembled provider sentence for normal chat")
			}
			if wire.Facts == nil || wire.Facts.Provider == nil || *wire.Facts.Provider != provider || wire.Facts.Model == nil || *wire.Facts.Model != model {
				t.Errorf("missing failing attempt facts: %+v", wire.Facts)
			}
			if strings.Contains(text, "sk-proj") || wire.Detail == nil || strings.Contains(*wire.Detail, "sk-proj") || len(*wire.Detail) > 2048 {
				t.Errorf("provider body canary escaped or detail invalid: text=%q detail=%v", text, wire.Detail)
			}
			entries, err := store.ReadTranscript(meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("system error entries=%d, want1", len(entries))
			}
			entry := entries[0]
			if entry.Type != session.EntryTypeSystem || entry.Status != "error" || entry.Content != text || entry.ErrorCode != string(llm.Code) || !entry.ProviderMessage || entry.ErrorRetryable {
				t.Errorf("stored system error lost safe classification/identity: %+v; want %q", entry, text)
			}
			if strings.Contains(entry.Content, "sk-proj") {
				t.Fatal("raw provider canary persisted")
			}
			t.Logf("terminal code=%s text=%q stored=%q", llm.Code, text, entry.Content)
		})
	}
}
