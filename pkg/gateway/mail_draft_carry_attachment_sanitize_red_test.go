package gateway

// Regression coverage — NOT a bug fix. Raised as a suspected same-defect-class
// finding as MC-32 item 9 (mail_send_attachment_sanitize_red_test.go), during
// that fix's regression testing: mailCarryAttachment (pkg/gateway/rest_mail_draft.go)
// builds the carried-forward attachment with Name: p.Filename — no sanitizing
// call at that site — unlike draft create/update's own NEW-upload path
// (handleMailDraftUpdate, which calls email.SanitizeAttachmentName(at.Filename)
// on bytes taken straight from the request body) and unlike the just-fixed
// manual send (rest_mail_send.go::handleMailSendInner, same reason: request-body
// input, never parsed).
//
// Investigated and confirmed NOT exploitable: p.Filename never carries a raw,
// unsanitized name in the first place. Every email.MailPart's Filename field is
// populated by pkg/email/view.go's MIME parser at Filename: sanitizeMailPartName(name)
// (view.go:661) — sanitization happens once, at parse time, for every attachment
// parsed off the wire, before mailCarryAttachment (or anything else) ever sees
// the value. Item 9 and the draft-update new-upload path both needed their own
// explicit SanitizeAttachmentName call because THEIR input (MailSendRequest /
// MailDraftUpdateRequest .Attachments[].Filename) comes straight from the
// request body — never through the MIME parser — so nothing upstream had
// sanitized it. mailCarryAttachment's input already has.
//
// This test passes on the current, unmodified code (confirmed: no fix applied).
// Kept as a regression test because it pins a real security invariant
// (path-traversal-safe carried-forward attachment names) that would be easy to
// break by, e.g., adding a p.RawFilename bypass or a new carry path that reads
// a name from somewhere other than the parsed MailPart.
//
// Oracle: email.SanitizeAttachmentName's own behavior (pkg/email/view.go,
// sanitizeMailPartName) — the expected sanitized name is derived by calling
// that function directly, never by hand-computing or reading off the current
// output.
//
// Exercises the draft-send explicit-keep path (idxSend), which calls
// mailCarryAttachment directly on a foreign draft's existing attachment part.

import (
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/require"
)

func TestMailDraftSend_CarriedAttachmentFilenameIsSanitized(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))

	const hostileName = "../../evil.txt"
	const attachmentMarker = "MC32-CARRY-ATTACH-MARKER"

	raw := unvForeignDraftRaw("carrysan", []unvRawAtt{
		{Name: hostileName, Body: attachmentMarker},
	})
	appendRaw(t, cl, "Drafts", []byte(raw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)
	leafIdx := unvAttLeafIndex(t, raw, hostileName)

	rec := idxSend(t, env, uv, 1, []int{leafIdx})
	require.Equal(t, http.StatusOK, rec.Code,
		"the explicit-keep send of a carried-forward attachment must succeed; body=%s", rec.Body.String())

	n, bodies := sink.acceptedBodies()
	require.Equal(t, 1, n, "instrument: the message must actually have been transmitted for the sanitization claim to mean anything")
	require.Len(t, bodies, 1)
	transmitted := bodies[0]

	// Instrument check: the carried attachment's bytes really rode the wire —
	// this is not a pre-dial rejection or a body that skipped the attachment
	// entirely.
	require.Contains(t, transmitted, attachmentMarker,
		"instrument: the carried attachment's bytes never reached the transmitted body — the test proves nothing about sanitization")

	// The actual finding: draft-update's NEW-upload sanitizer
	// (email.SanitizeAttachmentName) is never called on the EXISTING-part
	// carry-forward path, so the raw hostile name — and the path-traversal
	// sequence inside it — survives verbatim into both the Content-Type
	// "name=" parameter and the Content-Disposition "filename=" parameter of
	// the transmitted MIME part.
	require.NotContains(t, transmitted, hostileName,
		"MC-32: the raw hostile filename must never reach the wire on the carry-forward path — contract says "+
			`"Sanitized on serve and on attach (MC-32) — no path separators are ever stored or served"`)
	require.NotContains(t, transmitted, "../",
		"MC-32: no path-traversal sequence may survive into the transmitted MIME part on the carry-forward path")

	// Positive oracle check: the carried attachment must reach the wire under
	// exactly the name email.SanitizeAttachmentName itself produces for this
	// input — derived from the sanitizer, never guessed or read off the
	// buggy output.
	wantName := email.SanitizeAttachmentName(hostileName)
	require.Contains(t, transmitted, wantName,
		"the carried-forward attachment must reach the wire under its sanitized name (oracle: email.SanitizeAttachmentName)")
}
