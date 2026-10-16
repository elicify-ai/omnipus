package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

type routing1340RecoveryClient struct {
	first error
	seen  [][]providers.Message
}

func (p *routing1340RecoveryClient) GetDefaultModel() string { return "m" }
func (p *routing1340RecoveryClient) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	copyMessages := append([]providers.Message(nil), messages...)
	for i := range copyMessages {
		copyMessages[i].Media = append([]string(nil), messages[i].Media...)
	}
	p.seen = append(p.seen, copyMessages)
	if len(p.seen) == 1 {
		return nil, p.first
	}
	return &providers.LLMResponse{Content: "recovered-answer"}, nil
}

// CR F2/PTA F2 and SFH F4: actual recovery is allowed for an attachment
// restriction, never for an authoritative wrong-model configuration failure.
func TestRouting1340_ReviewRecoveryDistinguishesModelAndCapability(t *testing.T) {
	cases := []struct {
		name         string
		pdf          bool
		err          error
		wantCalls    int
		wantBadModel bool
	}{
		{"capability_image_strip_retry", false, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Model 'm' is not available for image input"}}`}, 2, false},
		{"colon_capability_subject", false, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Model 'm': image input is not available."}}`}, 2, false},
		{"colon_capability_no_period", false, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Model 'm': image input is not available"}}`}, 2, false},
		// CR R3-1 / SFH R3-N1: input is the subject; a model suffix cannot
		// suppress the real attachment retry, including PDF-to-text recovery.
		{"input_first_image_quoted", false, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Image input for model 'm' is not available."}}`}, 2, false},
		{"input_first_image_this_model", false, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Image input for this model is not available."}}`}, 2, false},
		{"prefixed_input_first_image", false, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Request rejected: image input for model 'm' is not available."}}`}, 2, false},
		{"input_first_pdf_quoted", true, &common.ProviderError{Status: 400, Body: `{"error":{"message":"PDF input for model 'm' is not available."}}`}, 2, false},
		{"input_first_pdf_this_model", true, &common.ProviderError{Status: 400, Body: `{"error":{"message":"PDF input for this model is not available."}}`}, 2, false},
		{"prefixed_input_first_pdf", true, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Request rejected: PDF input for this model is not offered by this provider."}}`}, 2, false},
		{"leading_article_pdf_absence", true, &common.ProviderError{Status: 400, Body: `{"error":{"message":"The model 'm' is not available."}}`}, 1, true},
		{"pdf_absence_offered_by_provider", true, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Request rejected: the model 'm' is not offered by this provider."}}`}, 1, true},
		{"structured_input_first_image", false, &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found","message":"Image input for this model is not available."}}`}, 1, true},
		{"structured_input_first_pdf", true, &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found","message":"PDF input for model 'm' is not available."}}`}, 1, true},
		{"leading_article_model_absent", false, &common.ProviderError{Status: 400, Body: `{"error":{"message":"The model 'm' is not available."}}`}, 1, true},
		{"model_not_offered_by_provider", false, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Model 'm' is not offered by this provider."}}`}, 1, true},
		{"leading_article_not_offered", false, &common.ProviderError{Status: 400, Body: `{"error":{"message":"The model 'm' is not offered."}}`}, 1, true},
		{"explicit_bad_model_image", false, &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found","message":"model not found"}}`}, 1, true},
		{"explicit_bad_model_pdf", true, &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found","message":"model not found"}}`}, 1, true},
		{"typed_bad_model_pdf_without_body", true, &providers.FailoverError{Reason: "bad_model", Provider: "openrouter", Model: "m", Status: 400}, 1, true},
		{"structured_model_beats_media_words", false, &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found","message":"image input not supported; requested model not found"}}`}, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &routing1340RecoveryClient{first: tc.err}
			a := &AgentInstance{ID: "routing1340", Model: "m", Candidates: []providers.FallbackCandidate{{Provider: "openrouter", Model: "m"}}}
			a.StoreProviderPool(map[string]providers.LLMProvider{"openrouter": client})
			f := newRouting1340ReviewFixture(t, nil, a)
			f.selectProvider(t)
			media := "data:image/png;base64,iVBORw0KGgo="
			if tc.pdf {
				media = pdfDataURL("ORIGINAL_PDF_TEXT")
			}
			f.rf.callMessages = []providers.Message{{Role: "user", Content: "inspect this", Media: []string{media}}}
			f.ri.messages = f.rf.callMessages
			if flow := f.rr.callLLMWithRetries(); flow != agentLoopRunTurnResponseNext {
				t.Fatalf("provider-call flow=%v", flow)
			}
			if len(client.seen) != tc.wantCalls {
				t.Errorf("provider requests=%d, want%d; error=%v", len(client.seen), tc.wantCalls, f.rf.err)
			}
			if tc.wantBadModel {
				if f.rf.err == nil || !errors.Is(f.rf.err, tc.err) {
					t.Errorf("permanent model error was replaced: got=%v want original=%v", f.rf.err, tc.err)
				}
				const wantMessage = "Model m is not offered by provider OpenRouter (bad_model). Pick a new model in the agent's settings."
				if llm := TranslateTurnError(f.rf.err); llm.Code != LLMErrorCode("bad_model") || llm.Message != wantMessage || llm.Retryable || !llm.ProviderMessage {
					t.Errorf("permanent model verdict lost: %+v; want non-retriable bad_model and safe message %q", llm, wantMessage)
				}
				if f.rt.ts.imageRetryDone.Load() || f.rt.ts.mediaRetryDone.Load() {
					t.Error("bad_model consumed attachment recovery budget")
				}
			} else {
				if f.rf.err != nil || f.rf.response == nil || f.rf.response.Content != "recovered-answer" {
					t.Errorf("capability recovery failed: response=%+v err=%v", f.rf.response, f.rf.err)
				}
				if tc.pdf {
					if len(client.seen) != 2 || len(client.seen[0][0].Media) != 1 || len(client.seen[1][0].Media) != 0 || !f.rt.ts.mediaRetryDone.Load() || f.rt.ts.imageRetryDone.Load() {
						t.Errorf("real PDF downgrade/retry not observed with separate per-class guards: %+v", client.seen)
					}
					if len(client.seen) == 2 && (client.seen[0][0].Content != "inspect this" || !strings.Contains(client.seen[1][0].Content, "inspect this") || !strings.Contains(client.seen[1][0].Content, "ORIGINAL_PDF_TEXT")) {
						t.Errorf("PDF retry lost the original request or extracted document content: %+v", client.seen)
					}
				} else {
					if len(client.seen) != 2 || len(client.seen[0][0].Media) != 1 || len(client.seen[1][0].Media) != 0 || !f.rt.ts.imageRetryDone.Load() {
						t.Errorf("real image strip/retry not observed: %+v", client.seen)
					}
					if f.rt.ts.outcomeRelabel != CodeMediaUnsupported {
						t.Errorf("successful recovery outcome=%q, want media_unsupported", f.rt.ts.outcomeRelabel)
					}
				}
			}
		})
	}
}
