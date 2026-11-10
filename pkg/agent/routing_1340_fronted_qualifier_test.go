package agent

import (
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Round-5 R5-1 (issue #1340) over the REAL recovery conductor: a fronted
// request-shape qualifier ("With attachments, ...", "For image input, ...",
// "When tools are enabled, ...") makes the availability sentence conditional.
// With an attachment present, such a rejection must reach attachment recovery
// in exactly two provider calls and the first error must translate to the
// format catalogue copy — never terminal bad_model. Neutral fronted phrases
// keep the terminal bad_model verdict. Only the provider HTTP edge is scripted
// (the shared routing1340RecoveryClient recorder).
func TestRouting1340_FrontedQualifierReachesAttachmentRecovery(t *testing.T) {
	t.Run("qualified_sentence_recovers", func(t *testing.T) {
		for _, tc := range []struct {
			name, message string
			pdf           bool
		}{
			{"with_attachments_image", "With attachments, model m is not available.", false},
			{"with_attachments_pdf", "With attachments, model m is not available.", true},
			{"for_image_input_unquoted", "For image input, model m is not available.", false},
			{"when_tools_enabled_generic", "When tools are enabled, the model is not available.", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				first := &common.ProviderError{Status: 400, Body: routing1340SubjectAgentBody(t, "", tc.message)}
				client, f := runRouting1340SubjectTurn(t, first, routing1340SubjectMedia(tc.pdf))
				if len(client.seen) != 2 {
					t.Fatalf("provider requests=%d, want 2 (qualified availability recovers the attachment); err=%v", len(client.seen), f.rf.err)
				}
				if f.rf.err != nil || f.rf.response == nil || f.rf.response.Content != "recovered-answer" {
					t.Errorf("attachment recovery failed: response=%+v err=%v", f.rf.response, f.rf.err)
				}
				if tc.pdf {
					if !f.rt.ts.mediaRetryDone.Load() || f.rt.ts.imageRetryDone.Load() {
						t.Errorf("PDF downgrade guards mediaRetryDone=%t imageRetryDone=%t, want true/false", f.rt.ts.mediaRetryDone.Load(), f.rt.ts.imageRetryDone.Load())
					}
				} else if !f.rt.ts.imageRetryDone.Load() || f.rt.ts.mediaRetryDone.Load() {
					t.Errorf("image strip guards imageRetryDone=%t mediaRetryDone=%t, want true/false", f.rt.ts.imageRetryDone.Load(), f.rt.ts.mediaRetryDone.Load())
				}
				llm := TranslateTurnError(first)
				if llm.Code != CodeProviderRequestRejected {
					t.Errorf("first error code=%q, want provider_request_rejected (never bad_model) for %q", llm.Code, tc.message)
				}
				if want := UserMessageForCode(CodeProviderRequestRejected); llm.Message != want {
					t.Errorf("first error message=%q, want catalogue copy %q", llm.Message, want)
				}
			})
		}
	})

	t.Run("neutral_fronted_phrase_stays_terminal", func(t *testing.T) {
		wantBadModelSentence := routing1340SubjectContractSentence(routing1340SubjectBadModelTemplate, "m")
		for _, tc := range []struct{ name, message string }{
			{"sorry_comma", "Sorry, model m is not available."},
			{"error_colon", "Error: model m is not available."},
		} {
			t.Run(tc.name, func(t *testing.T) {
				first := &common.ProviderError{Status: 400, Body: routing1340SubjectAgentBody(t, "", tc.message)}
				client, f := runRouting1340SubjectTurn(t, first, routing1340SubjectMedia(false))
				if len(client.seen) != 1 {
					t.Errorf("provider requests=%d, want 1 (unconditional model absence never recovers); err=%v", len(client.seen), f.rf.err)
				}
				if f.rf.err == nil || !errors.Is(f.rf.err, first) {
					t.Errorf("original model-absence error replaced: got=%v want=%v", f.rf.err, first)
				}
				if f.rt.ts.imageRetryDone.Load() || f.rt.ts.mediaRetryDone.Load() {
					t.Error("unconditional model absence consumed an attachment recovery guard")
				}
				if llm := TranslateTurnError(f.rf.err); llm.Code != CodeBadModel || llm.Message != wantBadModelSentence {
					t.Errorf("translated=%+v, want bad_model sentence %q", llm, wantBadModelSentence)
				}
			})
		}
	})
}
