package gateway

// RED (combined feature-gate review, item 9, code-reviewer — IMPORTANT,
// MC-32). Oracle: the wire contract itself —
// pkg/api/generated/openapi_types.gen.go's MailSendRequest.Attachments[].
// Filename doc comment, generated from contracts/openapi.yaml (Constraint
// #8's single source of truth) — states plainly: "Sanitized on serve and on
// attach (MC-32) — no path separators are ever stored or served." Draft
// creation/update (pkg/gateway/rest_mail_draft.go::handleMailDraftUpdate)
// honors this: it calls email.SanitizeAttachmentName(at.Filename) before
// handing the name to email.Compose. Manual send
// (pkg/gateway/rest_mail_send.go::handleMailSendInner) does not: it passes
// at.Filename straight into email.Attachment{Name: at.Filename, ...} with no
// sanitizing call anywhere on that path. email.Compose's own
// renderAttachmentPart (pkg/email/compose.go) only strips CR/LF via
// sanitizeHeader — it does not strip path separators or control characters.
// A hostile attachment filename therefore reaches the transmitted MIME part
// verbatim on manual send, contradicting the contract's own promise.
//
// This mirrors the existing D36/N3 SMTP-sink pattern
// (mail_send_smtp_sink_test.go) so the assertion is against what actually
// left the process over SMTP, not against an intermediate struct.

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMailSend_AttachmentFilenameIsSanitizedLikeDraftUpdate(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, _ := startPlainIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))

	const hostileName = "../../evil.txt"
	const attachmentMarker = "MC32-SEND-ATTACH-MARKER"
	dataB64 := base64.StdEncoding.EncodeToString([]byte(attachmentMarker))

	body := fmt.Sprintf(
		`{"to":["human@example.test"],"subject":"MC-32 manual send attachment",`+
			`"body_markdown":"hi","attachments":[{"filename":%q,"content_type":"text/plain","data_base64":%q}]}`,
		hostileName, dataB64)

	rec := mailDo(env.mux, http.MethodPost,
		"/api/v1/workspaces/"+mailRedWS+"/mail/"+mailRedAgent+"/messages",
		nextMailIP(), true, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("MC-32: manual send with an attachment = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	n, bodies := sink.acceptedBodies()
	require.Equal(t, 1, n, "instrument: the message must actually have been transmitted for the sanitization claim to mean anything")
	require.Len(t, bodies, 1)
	transmitted := bodies[0]

	// Instrument check: the attachment itself really rode the wire — this is
	// not a pre-dial rejection or a body that skipped attachments entirely.
	require.Contains(t, transmitted, attachmentMarker,
		"instrument: the attachment bytes never reached the transmitted body — the test proves nothing about sanitization")

	// The actual finding: draft-update's sanitizer (email.SanitizeAttachmentName)
	// replaces '/', '\\' and ':' with '-' and drops control characters before
	// the name ever reaches Compose. Manual send skips that call, so the raw
	// hostile name — and the path-traversal sequence inside it — survive
	// verbatim into both the Content-Type "name=" parameter and the
	// Content-Disposition "filename=" parameter of the transmitted MIME part.
	require.NotContains(t, transmitted, hostileName,
		"MC-32: the raw hostile filename must never reach the wire — contract says "+
			`"Sanitized on serve and on attach (MC-32) — no path separators are ever stored or served"`)
	require.NotContains(t, transmitted, "../",
		"MC-32: no path-traversal sequence may survive into the transmitted MIME part (contract: no path separators are ever stored or served)")
}
