package gateway

// rest_mail_reply.go — the F5 reply-context operation (ADR-20261001
// "Reply / Reply all context (F5)"; w4 spec US-5): assemble the compose
// prefill for one message — recipients from the ONE shared recipient rule
// (pkg/email/reply.go::BuildReplyRecipients; the SPA implements no second
// algorithm), the subject with Re: applied exactly once, and the editable
// quoted original with escaped syntax. Current-compose state ONLY — never a
// server draft, never a body cache, never a send.

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// handleMailReplyContext implements POST .../messages/{ref}/reply-context.
func (a *restAPI) handleMailReplyContext(w http.ResponseWriter, r *http.Request, workspaceID, agentID, folder, ref string) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req gen.MailReplyContextRequest
	if !decodeMailJSON(a, w, r, "MailReplyContextRequest", &req) {
		return
	}
	if req.Mode != email.ReplyModeReply && req.Mode != email.ReplyModeReplyAll {
		jsonErr(w, http.StatusBadRequest, "mode must be reply or reply_all")
		return
	}
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	mv, handled := mailBudgetWrap(a, w, r, agentID, workspaceID, client, "getMailReplyContext",
		map[string]any{"folder": folder, "ref": ref, "mode": string(req.Mode)}, func(c context.Context) (*email.MailView, error) {
			return client.ReadView(c, folder, ref)
		})
	if handled {
		return
	}
	if mailViewHidden(mv) {
		jsonErr(w, http.StatusNotFound, "message not found")
		return
	}
	v := mv
	recips, err := email.BuildReplyRecipients(email.ReplyInput{
		From:       v.From,
		ReplyTo:    v.ReplyTo,
		To:         v.To,
		Cc:         v.Cc,
		Mode:       string(req.Mode),
		OwnAddress: client.Address(),
	})
	if err != nil {
		// An invalid supplied address is an actionable error, never a
		// silent omission (US-5.AC-3).
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := gen.MailReplyContextResponse{
		To:           mailAddressViews(recips.To),
		Cc:           mailAddressViews(recips.Cc),
		Bcc:          []string{},
		Subject:      mailReplySubject(v.Subject),
		BodyMarkdown: mailReplyQuote(v),
	}
	if v.MessageID != "" {
		mid := v.MessageID
		out.InReplyTo = &mid
	}
	jsonOK(w, out)
}

// mailAddressViews renders parsed addresses as bare-address strings (the
// generated response carries string arrays).
func mailAddressViews(addrs []mail.Address) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.Address)
	}
	return out
}

// mailReplySubject applies Re: exactly once — no duplicated prefix, no
// signature duplication.
func mailReplySubject(subject string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "re:") {
		return subject
	}
	return "Re: " + subject
}

// mailReplyQuote builds the editable quoted original: escaped attribution
// (sender/date, or "No date" per F6) plus the readable body with its
// Markdown image/embed syntax escaped so the quote cannot load remote
// images or resolve workspace embeds inside Compose. Never raw HTML/CSS;
// never the original's attachments.
func mailReplyQuote(v *email.MailView) string {
	sender := v.FromName
	if sender == "" {
		sender = v.From
	}
	when := email.FormatEffectiveDate(v.Date, time.Time{})
	var body string
	switch {
	case v.TextBody != "":
		body = v.TextBody
	case v.BodyMarkdown != "":
		body = v.BodyMarkdown
	case v.HasHTML:
		// HTML-only original: the readable projection is the sanitized
		// body's text is not carried on the view; state the honest
		// projection note rather than pasting raw HTML.
		body = "[the original message was HTML-only; its styled view is in the reading pane]"
	}
	if body == "" {
		body = "[no readable body]"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "> On %s, %s wrote:\n", when, sender)
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString("> ")
		b.WriteString(mailEscapeQuoteLine(line))
		b.WriteString("\n")
	}
	return b.String()
}

// mailEscapeQuoteLine escapes the syntax that must stay inert inside a
// compose quote: remote/embedded images ("!["), wikilinks/embeds ("[["),
// autolinks and raw HTML ("<"), and a leading heading marker. Ordinary text
// and plain links stay readable.
func mailEscapeQuoteLine(line string) string {
	out := strings.ReplaceAll(line, "\\", "\\\\")
	out = strings.ReplaceAll(out, "![", "\\![")
	out = strings.ReplaceAll(out, "[[", "\\[[")
	out = strings.ReplaceAll(out, "<", "\\<")
	if strings.HasPrefix(out, "#") {
		out = "\\" + out
	}
	return out
}
