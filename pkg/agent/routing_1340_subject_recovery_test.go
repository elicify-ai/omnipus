package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Round-4 subject-kind recovery oracle (issue #1340), derived from the
// round-4 directive BEFORE the structured availability parser exists. The
// subject of the availability sentence decides the outcome at the REAL
// recovery conductor:
//
//   - model absence          → terminal: exactly one provider call, the
//     original error retained, neither attachment guard consumed, and
//     TranslateTurnError renders the contract's bad_model sentence;
//   - input-type capability  → real recovery in exactly two provider calls
//     (image strip or native-PDF downgrade with its own per-class guard),
//     and the FIRST error translates to the format catalogue copy — never
//     bad_model, never a fabricated live event;
//   - everything else        → terminal format: one call, provider_request_
//     rejected contract sentence, guards untouched, never bad_model;
//   - structured model_not_found stays authoritative over capability prose.
//
// Only the provider HTTP edge is scripted (the shared routing1340RecoveryClient
// recorder); selection, classification, recovery, extraction and translation
// stay real. Five literals (this / requested / a / quoted-requested / no-space
// colon prefix) are behavioral RED on the baseline regex: it misreads their
// model-absence subject as capability/format, so the conductor wrongly recovers
// in two calls.

// Contract literals for the fact-bearing templates
// (contracts/components/schemas/LLMError.yaml, entries bad_model and
// provider_request_rejected). Slots are filled from the fixture's own
// candidate identity — never from observed output.
const (
	routing1340SubjectBadModelTemplate = "Model {model} is not offered by provider {provider} (bad_model). Pick a new model in the agent's settings."
	routing1340SubjectFormatTemplate   = "Provider {provider} rejected the request for model {model} (HTTP 400: Bad Request; format). Open Verbose chat for technical details."
)

// routing1340SubjectContractSentence fills a contract template with the
// fixture's own failing-attempt facts: candidate model m and the provider's
// catalog display name.
func routing1340SubjectContractSentence(tmpl, model string) string {
	return strings.NewReplacer("{model}", model, "{provider}", providers.DisplayName("openrouter")).Replace(tmpl)
}

// routing1340SubjectAgentBody renders the upstream error envelope the
// provider-edge recorder returns on the first call.
func routing1340SubjectAgentBody(t *testing.T, code, message string) string {
	t.Helper()
	errObj := map[string]any{"message": message}
	if code != "" {
		errObj["code"] = code
	}
	body, err := json.Marshal(map[string]any{"error": errObj})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// runRouting1340SubjectTurn drives one real turn through provider selection
// and the recovery conductor with the recorder as the only scripted edge.
func runRouting1340SubjectTurn(t *testing.T, firstErr error, media string) (*routing1340RecoveryClient, *routing1340ReviewFixture) {
	t.Helper()
	client := &routing1340RecoveryClient{first: firstErr}
	a := &AgentInstance{ID: "routing1340-subject", Model: "m", Candidates: []providers.FallbackCandidate{{Provider: "openrouter", Model: "m"}}}
	a.StoreProviderPool(map[string]providers.LLMProvider{"openrouter": client})
	f := newRouting1340ReviewFixture(t, nil, a)
	f.selectProvider(t)
	message := providers.Message{Role: "user", Content: "inspect this"}
	if media != "" {
		message.Media = []string{media}
	}
	f.rf.callMessages = []providers.Message{message}
	f.ri.messages = f.rf.callMessages
	if flow := f.rr.callLLMWithRetries(); flow != agentLoopRunTurnResponseNext {
		t.Fatalf("provider-call flow=%v", flow)
	}
	return client, f
}

func routing1340SubjectMedia(pdf bool) string {
	if pdf {
		return pdfDataURL("ORIGINAL_PDF_TEXT")
	}
	return "data:image/png;base64,iVBORw0KGgo="
}

// TestRouting1340_AvailabilitySubjectRecoveryOracle is the round-4 oracle over
// the real recovery conductor and the safe-message translation.
func TestRouting1340_AvailabilitySubjectRecoveryOracle(t *testing.T) {
	const wantModel = "m"
	wantBadModelSentence := routing1340SubjectContractSentence(routing1340SubjectBadModelTemplate, wantModel)
	wantFormatSentence := routing1340SubjectContractSentence(routing1340SubjectFormatTemplate, wantModel)

	t.Run("model_absence_is_terminal", func(t *testing.T) {
		cases := []struct {
			name, message string
			pdf           bool
		}{
			{"bare_unquoted", "Model m is not available.", false},
			{"article_generic", "The model is not available.", false},
			{"this_determiner", "This model is not available.", false},               // RED on baseline regex
			{"requested_unquoted", "The requested model m is not available.", false}, // RED on baseline regex
			{"a_determiner_quoted", "A model 'm' is not available.", true},           // RED on baseline regex
			{"requested_quoted", "The requested model 'm' is not available.", false}, // RED on baseline regex
			{"offered_by_provider_unquoted", "Model m is not offered by this provider.", false},
			{"article_offered_by_provider", "The model 'm' is not offered by this provider.", true},
			{"prefix_colon_no_space", "Request rejected:model m is not available.", true}, // RED on baseline regex
			{"prefix_colon_quoted", `Request rejected: the model "m" is not available.`, false},
			{"prefix_colon_spaced", "Request rejected: model m is not available.", false},
			{"prefix_semicolon", "Request rejected; model 'm' is not available.", false},
			{"prefix_sentence", "Request rejected. The model 'm' is not available.", false},
			{"prefix_exclamation", "Request rejected! The model 'm' is not available.", false},
			{"prefix_question", "Request rejected? The model 'm' is not available.", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				first := &common.ProviderError{Status: 400, Body: routing1340SubjectAgentBody(t, "", tc.message)}
				client, f := runRouting1340SubjectTurn(t, first, routing1340SubjectMedia(tc.pdf))
				// Terminal: exactly one provider call, no recovery, no answer.
				if len(client.seen) != 1 {
					t.Errorf("provider requests=%d, want 1 (model absence never recovers); err=%v", len(client.seen), f.rf.err)
				}
				if f.rf.err == nil || !errors.Is(f.rf.err, first) {
					t.Errorf("original model-absence error replaced: got=%v want=%v", f.rf.err, first)
				}
				if f.rf.response != nil {
					t.Errorf("terminal model absence produced an answer: %+v", f.rf.response)
				}
				if f.rt.ts.imageRetryDone.Load() || f.rt.ts.mediaRetryDone.Load() {
					t.Error("model absence consumed an attachment recovery guard")
				}
				// Safe message: the contract's fact-bearing bad_model sentence,
				// non-retriable — never the vendor's raw prose.
				llm := TranslateTurnError(f.rf.err)
				if llm.Code != CodeBadModel {
					t.Errorf("translated code=%q, want bad_model for %q", llm.Code, tc.message)
				}
				if llm.Message != wantBadModelSentence {
					t.Errorf("translated message=%q, want contract sentence %q", llm.Message, wantBadModelSentence)
				}
				if llm.Retryable || !llm.ProviderMessage {
					t.Errorf("retriable=%t providerMessage=%t, want non-retriable fact-bearing sentence", llm.Retryable, llm.ProviderMessage)
				}
			})
		}
	})

	t.Run("capability_recovers_in_two_calls", func(t *testing.T) {
		cases := []struct {
			name, message string
			pdf           bool
		}{
			{"image_subject_bare", "Image input is not available.", false},
			{"image_colon_spaced", "Model m: image input is not available.", false},
			{"image_colon_no_space", "Model m:image input is not available.", false},
			{"image_prefix_no_space", "Request rejected:image input is not available.", false},
			{"image_input_first_unquoted", "Image input for model m is not available.", false},
			{"image_input_first_quoted", "Image input for model 'm' is not available.", false},
			{"image_input_first_this_model", "Image input for this model is not available.", false},
			{"image_model_qualifier", "Model m is not available for image input.", false},
			{"image_fronted_qualifier", "For image input, model 'm' is not available.", false},
			{"pdf_input_first_this_model", "PDF input for this model is not available.", true},
			{"pdf_input_first_quoted", "PDF input for model 'm' is not available.", true},
			{"pdf_colon_no_space", "Model m:PDF input is not available.", true},
			{"pdf_article_qualifier", "The model 'm' is not offered for PDF input.", true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				first := &common.ProviderError{Status: 400, Body: routing1340SubjectAgentBody(t, "", tc.message)}
				client, f := runRouting1340SubjectTurn(t, first, routing1340SubjectMedia(tc.pdf))
				// Real recovery: exactly two provider calls, media gone on the retry.
				if len(client.seen) != 2 {
					t.Fatalf("provider requests=%d, want 2 (capability really recovers); err=%v", len(client.seen), f.rf.err)
				}
				if len(client.seen[0][0].Media) != 1 || len(client.seen[1][0].Media) != 0 {
					t.Errorf("media blocks first=%d second=%d, want 1 then 0", len(client.seen[0][0].Media), len(client.seen[1][0].Media))
				}
				if f.rf.err != nil || f.rf.response == nil || f.rf.response.Content != "recovered-answer" {
					t.Errorf("capability recovery failed: response=%+v err=%v", f.rf.response, f.rf.err)
				}
				// Per-class guards: the fired class is set, the sibling stays clear.
				if tc.pdf {
					if !f.rt.ts.mediaRetryDone.Load() || f.rt.ts.imageRetryDone.Load() {
						t.Errorf("PDF downgrade guards mediaRetryDone=%t imageRetryDone=%t, want true/false", f.rt.ts.mediaRetryDone.Load(), f.rt.ts.imageRetryDone.Load())
					}
					if !strings.Contains(client.seen[0][0].Content, "inspect this") || !strings.Contains(client.seen[1][0].Content, "inspect this") || !strings.Contains(client.seen[1][0].Content, "ORIGINAL_PDF_TEXT") {
						t.Errorf("PDF retry lost the original prompt or the extracted document text: first=%q second=%q", client.seen[0][0].Content, client.seen[1][0].Content)
					}
				} else {
					if !f.rt.ts.imageRetryDone.Load() || f.rt.ts.mediaRetryDone.Load() {
						t.Errorf("image strip guards imageRetryDone=%t mediaRetryDone=%t, want true/false", f.rt.ts.imageRetryDone.Load(), f.rt.ts.mediaRetryDone.Load())
					}
					if f.rt.ts.outcomeRelabel != CodeMediaUnsupported {
						t.Errorf("recovered outcome=%q, want media_unsupported", f.rt.ts.outcomeRelabel)
					}
				}
				// The FIRST error's translated safe classification: catalogue
				// copy for the expected format code, never bad_model and never
				// a fabricated live event.
				llm := TranslateTurnError(first)
				if llm.Code != CodeProviderRequestRejected {
					t.Errorf("first error code=%q, want provider_request_rejected for %q", llm.Code, tc.message)
				}
				if llm.Code == CodeBadModel {
					t.Errorf("capability prose translated as bad_model for %q", tc.message)
				}
				if want := UserMessageForCode(CodeProviderRequestRejected); llm.Message != want {
					t.Errorf("first error message=%q, want catalogue copy %q", llm.Message, want)
				}
				if llm.Retryable || llm.ProviderMessage {
					t.Errorf("retriable=%t providerMessage=%t, want non-retriable facts-absent copy", llm.Retryable, llm.ProviderMessage)
				}
			})
		}
	})

	t.Run("everything_else_is_terminal_format", func(t *testing.T) {
		cases := []struct{ name, message string }{
			{"request_subject", "This request is not available."},
			{"requested_input_subject", "The requested input is not available."},
			{"model_response_not_found", "Model response not found."},
			{"model_input_not_found", "Model input not found."},
			{"unquoted_other_identifier", "The model other is not available."},
			{"available_without_negation", "Model m is available."},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				first := &common.ProviderError{Status: 400, Body: routing1340SubjectAgentBody(t, "", tc.message)}
				client, f := runRouting1340SubjectTurn(t, first, "")
				if len(client.seen) != 1 {
					t.Errorf("provider requests=%d, want 1 (terminal format); err=%v", len(client.seen), f.rf.err)
				}
				if f.rf.err == nil || !errors.Is(f.rf.err, first) {
					t.Errorf("original error replaced: got=%v want=%v", f.rf.err, first)
				}
				if f.rf.response != nil {
					t.Errorf("terminal format produced an answer: %+v", f.rf.response)
				}
				if f.rt.ts.imageRetryDone.Load() || f.rt.ts.mediaRetryDone.Load() {
					t.Error("a non-availability verdict consumed an attachment recovery guard")
				}
				llm := TranslateTurnError(f.rf.err)
				if llm.Code != CodeProviderRequestRejected {
					t.Errorf("translated code=%q, want provider_request_rejected (never bad_model) for %q", llm.Code, tc.message)
				}
				if llm.Message != wantFormatSentence {
					t.Errorf("translated message=%q, want contract sentence %q", llm.Message, wantFormatSentence)
				}
				if llm.Retryable || !llm.ProviderMessage {
					t.Errorf("retriable=%t providerMessage=%t, want non-retriable fact-bearing sentence", llm.Retryable, llm.ProviderMessage)
				}
			})
		}
	})

	t.Run("structured_model_not_found_stays_authoritative", func(t *testing.T) {
		cases := []struct {
			name, message string
			pdf           bool
		}{
			{"structured_image_prose", "Image input for this model is not available.", false},
			{"structured_pdf_no_space_colon", "Model m:PDF input is not available.", true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				first := &common.ProviderError{Status: 400, Body: routing1340SubjectAgentBody(t, "model_not_found", tc.message)}
				client, f := runRouting1340SubjectTurn(t, first, routing1340SubjectMedia(tc.pdf))
				if len(client.seen) != 1 {
					t.Errorf("provider requests=%d, want 1 (structured verdict, no recovery); err=%v", len(client.seen), f.rf.err)
				}
				if f.rf.err == nil || !errors.Is(f.rf.err, first) {
					t.Errorf("original structured error replaced: got=%v want=%v", f.rf.err, first)
				}
				if f.rt.ts.imageRetryDone.Load() || f.rt.ts.mediaRetryDone.Load() {
					t.Error("structured wrong-model verdict consumed an attachment recovery guard")
				}
				llm := TranslateTurnError(f.rf.err)
				if llm.Code != CodeBadModel || llm.Message != wantBadModelSentence || llm.Retryable || !llm.ProviderMessage {
					t.Errorf("structured verdict translation=%+v, want non-retriable bad_model sentence %q", llm, wantBadModelSentence)
				}
			})
		}
	})
}
