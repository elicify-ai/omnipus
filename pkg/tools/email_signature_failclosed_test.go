package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/email"
)

// signatureAppendProbe keeps the SMTP/IMAP edge observations local to each
// case, without using the package-wide fixtureAppends recorder.
type signatureAppendProbe struct {
	*signatureFake
	appends []fakeAppend
}

func (p *signatureAppendProbe) AppendMessage(_ context.Context, folder string, flags []string, raw []byte) (uint32, uint32, error) {
	p.appends = append(p.appends, fakeAppend{Folder: folder, Flags: flags, Raw: string(raw)})
	return fixtureUID, fixtureUIDValidity, nil
}

// Oracle: review fix round 1 requires all three tools to refuse a 16,385-char
// mailbox signature before sending or saving. email-mail-view-spec MC-1/DS-1
// sets the maximum at 16,384 chars; max-1/max are successful controls. The
// exact diagnostic below characterizes SanitizeSignatureHTML's only error
// path; IsError and zero SMTP sends / IMAP APPENDs are independent assertions.
func TestEmailTools_SignatureLimitFailsClosed(t *testing.T) {
	for _, toolName := range []string{"send_email", "reply", "create_email_draft"} {
		t.Run(toolName, func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				chars int
			}{
				{"below_limit", 16383},
				{"at_limit", 16384},
				{"over_limit", 16385},
			} {
				t.Run(tc.name, func(t *testing.T) {
					// ASCII makes character and byte counts equal; this is a
					// mailbox signature, not an oversized agent message body.
					signature := strings.Repeat("s", tc.chars)
					ft := &signatureAppendProbe{signatureFake: newSignatureFake(signature, email.Message{
						UID: 7, From: "alice@box.test", Subject: "Hello", MessageID: "<orig@box.test>",
					})}
					tool := toolByName(t, EmailTransports{"ws": ft}, toolName)
					res := tool.Execute(mailCtx(), map[string]any{
						"to": []any{"alice@box.test"}, "subject": "Hello",
						"body": sigAgentBody, "uid": float64(7),
					})
					if res == nil {
						t.Fatal("tool returned no result")
					}
					if tc.chars == 16385 {
						if !res.IsError {
							t.Errorf("16,385-char signature: IsError = false, want true (%s)", res.ForLLM)
						}
						wantError := toolName + ": compose: signature: signature is 16385 characters; the maximum is 16384"
						if res.ForLLM != wantError {
							t.Errorf("signature-limit diagnostic = %q, want %q", res.ForLLM, wantError)
						}
						if got := len(ft.sent); got != 0 {
							t.Errorf("16,385-char signature: SMTP sends = %d, want 0", got)
						}
						if got := len(ft.appends); got != 0 {
							t.Errorf("16,385-char signature: IMAP APPENDs = %d, want 0", got)
						}
						return
					}

					if res.IsError {
						t.Fatalf("allowed %d-char signature rejected: %s", tc.chars, res.ForLLM)
					}
					wantSends, wantFolder := 1, "Sent"
					if toolName == "create_email_draft" {
						wantSends, wantFolder = 0, "Drafts"
					}
					if got := len(ft.sent); got != wantSends {
						t.Errorf("allowed signature: SMTP sends = %d, want %d", got, wantSends)
					}
					if got := len(ft.appends); got != 1 {
						t.Fatalf("allowed signature: IMAP APPENDs = %d, want 1", got)
					}
					if got := ft.appends[0].Folder; got != wantFolder {
						t.Errorf("APPEND folder = %q, want %q", got, wantFolder)
					}
					view := email.ParseViewRaw([]byte(ft.appends[0].Raw))
					if got := strings.Count(view.TextBody, signature); got != 1 {
						t.Errorf("plain part carries the %d-char signature %d times, want 1", tc.chars, got)
					}
					if got := strings.Count(view.HTMLBody, signature); got != 1 {
						t.Errorf("HTML part carries the %d-char signature %d times, want 1", tc.chars, got)
					}
				})
			}
		})
	}
}
