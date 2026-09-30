package email

// Round-2 delta review, item 5 (silent-failure-hunter): buildEmailBody's
// Compose-error fallback gave the caller no signal — the return value was
// indistinguishable from a normal plain-text send.
//
// This file used to pin the OLD fallback's own content bug (the plain
// shape pasted raw Markdown syntax instead of rendering it). That fallback
// path no longer exists: buildEmailBody now returns a visible error to the
// caller on a Compose failure (see build_email_body_red_test.go for the
// error-signal pin) instead of silently degrading to a plain shape, so
// there is no more fallback body for a "raw Markdown leaked" assertion to
// apply to.
//
// Investigation this file's own history recorded, still true and still the
// justification for returning an error rather than deleting this path as
// unreachable: Client.Send validates ONLY that `to` parses (parseRecipientList)
// before calling buildEmailBody(c.acct.Username, to, subject, req.Body,
// req.InReplyTo) — it never validates that the ACCOUNT's own Username is
// itself a parseable RFC 5322 address (NewClient only requires it
// non-empty). Inside buildEmailBody, fromHeaderAddress(from) falls back to
// a raw sanitized string when `from` doesn't parse, and Compose then fails
// with "invalid from address" on that same string. So any mailbox account
// whose configured Username is not itself a bare RFC 5322 address
// (plausible for enterprise IMAP/SMTP logins that differ from the
// mailbox's real address) hits this path on every Markdown-structured
// send — REACHABLE, not dead code, which is exactly why Option 1 (return a
// visible error) was chosen over deleting the branch as unreachable.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildEmailBody_ComposeFailureFromUnparseableUsername_ReturnsVisibleError(t *testing.T) {
	// from is not RFC 5322-parseable — mail.ParseAddress rejects any bare
	// string with no "@" ("missing '@' or angle-addr"), which is exactly
	// what fromHeaderAddress falls back to, so Compose's own
	// mail.ParseAddress(in.From) fails too.
	const from = "not-an-email-username"
	const heading = "# Heading"
	const bold = "**bold**"
	md := heading + "\n\nSome " + bold + " text."

	out, err := buildEmailBody(from, "to@x.test", "subj", md, "")

	require.Error(t, err,
		"a Compose failure (invalid From address) must be a VISIBLE error to the caller, never a silent plain-shape fallback the caller cannot distinguish from a normal send")
	require.Empty(t, out,
		"no body may be produced on a Compose failure — there must be nothing for a caller to accidentally transmit")
	require.Contains(t, err.Error(), "invalid from address",
		"the returned error must name the actual Compose failure, not a generic wrapper with no diagnostic value")
}
