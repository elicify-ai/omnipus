package email

// RED (combined feature-gate review, item 8, LOW). Investigation: is
// buildEmailBody's Compose-error fallback reachable in production, and if
// so, what does it actually send?
//
// Reachable: Client.Send (pkg/email/transport.go) validates ONLY that `to`
// parses (parseRecipientList) before calling
// buildEmailBody(c.acct.Username, to, subject, req.Body, req.InReplyTo) —
// it never validates that the ACCOUNT's own Username is itself a parseable
// RFC 5322 address (NewClient only requires it non-empty). Inside
// buildEmailBody, fromHeaderAddress(from) falls back to a raw sanitized
// string when `from` doesn't parse, and Compose then fails with "invalid
// from address" on that same string (verified: net/mail.ParseAddress
// rejects any bare, non-"user@domain" string with "missing '@' or
// angle-addr" — confirmed by direct probe). So any mailbox account whose
// configured Username is not itself a bare RFC 5322 address (plausible for
// enterprise IMAP/SMTP logins that differ from the mailbox's real address)
// takes this fallback on every Markdown-structured send. This is
// REACHABLE, not dead code.
//
// What it silently degrades: the fallback (buildEmailBody's plain-shape
// tail) writes the Markdown SOURCE TEXT verbatim into the text/plain body —
// it does not render it via markdownToPlain (the same helper Compose uses
// for its own text/plain part). A human recipient therefore sees raw
// Markdown syntax ("# Heading", "**bold**") instead of rendered plain
// text. Oracle: docs/internal/specs/email-mail-view-spec.md's own FR-029
// principle for drafts — "rendered, not raw Markdown" — states plainly
// that a human reader must never see raw Markdown syntax in a mail body;
// the round-8 fix already logs this fallback (build_email_body_red_test.go)
// but logging does not change what the RECIPIENT sees. This test is
// independent of that log-only fix: it targets the transmitted body's
// content, not the server log.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildEmailBody_ComposeFallbackDoesNotLeakRawMarkdownSyntax(t *testing.T) {
	// from is not RFC 5322-parseable — mail.ParseAddress rejects any bare
	// string with no "@" ("missing '@' or angle-addr"), which is exactly
	// what fromHeaderAddress falls back to, so Compose's own
	// mail.ParseAddress(in.From) fails too and the plain-shape fallback runs.
	const from = "not-an-email-username"
	const heading = "# Heading"
	const bold = "**bold**"
	md := heading + "\n\nSome " + bold + " text."

	out := buildEmailBody(from, "to@x.test", "subj", md, "")

	// Instrument: prove this exercised the FALLBACK, not a working Compose
	// multipart build (multipart/alternative bodies carry a boundary=).
	require.NotContains(t, out, "boundary=",
		"instrument: this probe must take the Compose-error fallback, not a successful multipart build")

	idx := strings.Index(out, "\r\n\r\n")
	require.GreaterOrEqual(t, idx, 0, "instrument: the fallback must produce a header/body-separated RFC 5322 message")
	body := out[idx+4:]

	// The finding: the fallback pastes the Markdown SOURCE verbatim instead
	// of rendering it — spec principle (FR-029, "rendered, not raw
	// Markdown") says a human reader must never see raw Markdown syntax in
	// a mail body.
	require.NotContains(t, body, heading,
		"the fallback's plain body still contains the raw Markdown heading marker %q — "+
			"a human recipient sees unrendered Markdown syntax instead of plain text", heading)
	require.NotContains(t, body, bold,
		"the fallback's plain body still contains the raw Markdown bold marker %q — "+
			"a human recipient sees unrendered Markdown syntax instead of plain text", bold)
}
