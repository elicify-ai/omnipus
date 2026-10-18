package tools

// Oracle: docs/internal/specs/email-mail-view-spec.md US-1 (any send from a
// mailbox — agent tool or manual — carries the signature once on the HTML part
// and the derived plain-text part; a mailbox with no signature stays bare),
// §5.1, D3 (the agent never writes the signature; Compose appends it), and D26
// (reply uses the same rendering and signature as send_email).
//
// Draft half: F10 in pkg/gateway/rest_mail_draft.go. An Omnipus draft's
// bookkeeping Markdown stays the unsigned source. Panel Save bakes the
// signature into the stored HTML and plain parts only when the draft is an
// Omnipus draft. Panel Send always recomposes from that unsigned Markdown and
// appends the signature once. A tool-created draft is an Omnipus draft, so its
// stored display parts carry the signature once and its Markdown does not —
// otherwise the later panel Send signs twice (and the "--" separator turns the
// sender's first line into a heading).

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/email"
)

const (
	// sigOnceToken is text that survives signature sanitizing and the
	// plain-text derivation. It is not part of any message body in this file.
	sigOnceToken = "SIG-ONCE-TOKEN"
	// sigEvilToken must not survive the signature allowlist (US-1 scenario 4).
	sigEvilToken = "EVIL-SCRIPT-TOKEN"
	// sigHTMLForTest is a saved mailbox signature: real text plus a script
	// the sanitizer must drop. Compose re-sanitizes; the tools must not
	// concatenate this string raw.
	sigHTMLForTest = "<p>Kind regards, " + sigOnceToken + "</p><script>" + sigEvilToken + "</script>"

	sigAgentBody = "Hello Alice, this is the unsigned message body."
)

// signatureFake is the in-memory transport plus the signature capability
// *email.Client exposes. A nil inner is never used.
type signatureFake struct {
	*fakeTransport
	signature string
}

func (s *signatureFake) SignatureHTML() string { return s.signature }

func newSignatureFake(sig string, msgs ...email.Message) *signatureFake {
	return &signatureFake{fakeTransport: newFakeTransport(msgs...), signature: sig}
}

func TestSendEmail_AppendsMailboxSignatureOnce(t *testing.T) {
	ft := newSignatureFake(sigHTMLForTest)
	before := len(fixtureAppends)
	res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
		"to":      []any{"alice@box.test"},
		"subject": "Hello",
		"body":    sigAgentBody,
	})
	if res.IsError || len(ft.sent) != 1 {
		t.Fatalf("US-1: send_email err=%v sends=%d, want one send (%s)", res.IsError, len(ft.sent), res.ForLLM)
	}
	assertSignatureOnce(t, ft.sent[0].Body, "send_email transmitted")
	appended := fixtureAppends[before:]
	if len(appended) != 1 || appended[0].Folder != "Sent" {
		t.Fatalf("US-2: Sent appends = %+v, want one append to Sent", appended)
	}
	assertSignatureOnce(t, appended[0].Raw, "send_email Sent copy")
}

func TestReply_AppendsMailboxSignatureOnce(t *testing.T) {
	ft := newSignatureFake(sigHTMLForTest, email.Message{
		UID:       7,
		From:      "alice@box.test",
		Subject:   "Hello",
		MessageID: "<orig@box.test>",
	})
	before := len(fixtureAppends)
	res := NewReplyTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
		"uid":  float64(7),
		"body": sigAgentBody,
	})
	if res.IsError || len(ft.sent) != 1 {
		t.Fatalf("US-1/D26: reply err=%v sends=%d, want one send (%s)", res.IsError, len(ft.sent), res.ForLLM)
	}
	assertSignatureOnce(t, ft.sent[0].Body, "reply transmitted")
	appended := fixtureAppends[before:]
	if len(appended) != 1 || appended[0].Folder != "Sent" {
		t.Fatalf("US-2: Sent appends = %+v, want one append to Sent", appended)
	}
	assertSignatureOnce(t, appended[0].Raw, "reply Sent copy")
}

func TestCreateEmailDraft_StoredSignature_PanelSendOnce(t *testing.T) {
	ft := newSignatureFake(sigHTMLForTest)
	before := len(fixtureAppends)
	res := NewCreateEmailDraftTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
		"to":      []any{"alice@box.test"},
		"subject": "Draft subject",
		"body":    sigAgentBody,
	})
	if res.IsError {
		t.Fatalf("create_email_draft: %s", res.ForLLM)
	}
	if len(ft.sent) != 0 {
		t.Fatalf("draft creation opened %d SMTP sends, want 0", len(ft.sent))
	}
	appended := fixtureAppends[before:]
	if len(appended) != 1 || appended[0].Folder != "Drafts" {
		t.Fatalf("draft appends = %+v, want one append to Drafts", appended)
	}
	saved := email.ParseViewRaw([]byte(appended[0].Raw))
	if !saved.IsOmnipusDraft {
		t.Fatal("tool draft must be an Omnipus draft (X-Omnipus-Draft) so panel Save keeps the F10 bookkeeping split")
	}
	if saved.BodyMarkdown != sigAgentBody {
		t.Fatalf("F10: bookkeeping Markdown = %q, want the agent's unsigned body %q", saved.BodyMarkdown, sigAgentBody)
	}
	if strings.Contains(saved.BodyMarkdown, sigOnceToken) {
		t.Fatalf("F10: bookkeeping Markdown contains the signature; panel Send would append it again")
	}
	assertSignatureOnce(t, appended[0].Raw, "stored Omnipus draft")

	// Panel Send (rest_mail_draft.go::handleMailDraftSendInner): recomposes
	// from the unsigned Markdown and always passes the mailbox signature.
	sendOut, err := email.Compose(email.ComposeInput{
		From:          "agent@example.test",
		To:            []string{"alice@box.test"},
		Subject:       "Draft subject",
		Markdown:      saved.BodyMarkdown,
		SignatureHTML: sigHTMLForTest,
	})
	if err != nil {
		t.Fatalf("panel send Compose: %v", err)
	}
	assertSignatureOnce(t, string(sendOut.Transmitted), "panel send after tool draft")
	final := email.ParseViewRaw(sendOut.Transmitted)
	wantParagraph := "<p>" + sigAgentBody + "</p>"
	if !strings.Contains(final.HTMLBody, wantParagraph) {
		t.Fatalf("panel send HTML does not keep the sender's line as a paragraph %q:\n%s", wantParagraph, final.HTMLBody)
	}
	if strings.Contains(final.HTMLBody, "<h2") {
		t.Fatalf("panel send turned the sender's line into a heading (double-signed separator):\n%s", final.HTMLBody)
	}
}

func TestSendEmail_NoSignatureWhenUnset(t *testing.T) {
	t.Run("transport has no signature capability", func(t *testing.T) {
		ft := newFakeTransport()
		res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
			"to": "alice@box.test", "subject": "Hello", "body": sigAgentBody,
		})
		if res.IsError || len(ft.sent) != 1 {
			t.Fatalf("send err=%v sends=%d (%s)", res.IsError, len(ft.sent), res.ForLLM)
		}
		assertNoSignature(t, ft.sent[0].Body)
	})
	t.Run("mailbox signature is empty", func(t *testing.T) {
		ft := newSignatureFake("")
		res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
			"to": "alice@box.test", "subject": "Hello", "body": sigAgentBody,
		})
		if res.IsError || len(ft.sent) != 1 {
			t.Fatalf("send err=%v sends=%d (%s)", res.IsError, len(ft.sent), res.ForLLM)
		}
		assertNoSignature(t, ft.sent[0].Body)
	})
	t.Run("reply with empty signature", func(t *testing.T) {
		ft := newSignatureFake("", email.Message{
			UID: 7, From: "alice@box.test", Subject: "Hello", MessageID: "<orig@box.test>",
		})
		res := NewReplyTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
			"uid": float64(7), "body": sigAgentBody,
		})
		if res.IsError || len(ft.sent) != 1 {
			t.Fatalf("reply err=%v sends=%d (%s)", res.IsError, len(ft.sent), res.ForLLM)
		}
		assertNoSignature(t, ft.sent[0].Body)
	})
	t.Run("draft with empty signature stays unsigned", func(t *testing.T) {
		ft := newSignatureFake("")
		before := len(fixtureAppends)
		res := NewCreateEmailDraftTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
			"to": []any{"alice@box.test"}, "subject": "Draft subject", "body": sigAgentBody,
		})
		if res.IsError {
			t.Fatalf("draft: %s", res.ForLLM)
		}
		appended := fixtureAppends[before:]
		if len(appended) != 1 {
			t.Fatalf("draft appends = %d, want 1", len(appended))
		}
		assertNoSignature(t, appended[0].Raw)
		sendOut, err := email.Compose(email.ComposeInput{
			From: "agent@example.test", To: []string{"alice@box.test"},
			Subject: "Draft subject", Markdown: sigAgentBody, SignatureHTML: "",
		})
		if err != nil {
			t.Fatalf("panel send: %v", err)
		}
		assertNoSignature(t, string(sendOut.Transmitted))
	})
}

func assertSignatureOnce(t *testing.T, raw, where string) {
	t.Helper()
	view := email.ParseViewRaw([]byte(raw))
	if got := strings.Count(view.TextBody, sigOnceToken); got != 1 {
		t.Fatalf("%s: plain part contains %q %d time(s), want exactly 1:\n%s", where, sigOnceToken, got, view.TextBody)
	}
	if got := strings.Count(view.HTMLBody, sigOnceToken); got != 1 {
		t.Fatalf("%s: HTML part contains %q %d time(s), want exactly 1:\n%s", where, sigOnceToken, got, view.HTMLBody)
	}
	if strings.Contains(view.HTMLBody, sigEvilToken) || strings.Contains(strings.ToLower(view.HTMLBody), "<script") {
		t.Fatalf("%s: signature sanitizer left hostile content in the HTML part:\n%s", where, view.HTMLBody)
	}
	if strings.Contains(view.TextBody, sigEvilToken) {
		t.Fatalf("%s: plain part contains the stripped script token:\n%s", where, view.TextBody)
	}
	if got := strings.Count(view.HTMLBody, "omnipus-signature"); got != 1 {
		t.Fatalf("%s: Compose signature wrapper count = %d, want 1:\n%s", where, got, view.HTMLBody)
	}
}

func assertNoSignature(t *testing.T, raw string) {
	t.Helper()
	view := email.ParseViewRaw([]byte(raw))
	if strings.Contains(view.TextBody, sigOnceToken) || strings.Contains(view.HTMLBody, sigOnceToken) {
		t.Fatalf("US-1: a mailbox with no signature still carried %q\nplain:\n%s\nhtml:\n%s", sigOnceToken, view.TextBody, view.HTMLBody)
	}
	if strings.Contains(view.HTMLBody, "omnipus-signature") {
		t.Fatalf("US-1: signature wrapper present with no signature configured:\n%s", view.HTMLBody)
	}
	if strings.Contains(view.TextBody, "\n--\n") {
		t.Fatalf("US-1: plain part has the signature separator with no signature configured:\n%s", view.TextBody)
	}
}
