package gateway

// mail_send_body_limit_test.go — feature-gate round 1, security-lead
// LOW-MEDIUM + FUNCTIONAL BUG finding: the Mail send/draft routes are
// dispatched from HandleWorkspaces (rest_workspaces.go::HandleWorkspaces ->
// handleWorkspaceMail -> handleMailSend), and HandleWorkspaces is
// registered under the generic `withAuth` middleware (rest.go's route
// table: `rae.a.withAuth(withRateLimit(configLimiter, rae.a.HandleWorkspaces))`)
// — the same 1 MiB request-body cap (`withAuthAndBodyLimit(handler, 1<<20)`)
// every other simple JSON endpoint gets (rest.go::withAuth,
// withAuthAndBodyLimit). Mail's OWN attachment budget (MC-32,
// mailMaxAttachmentBytes = 25<<20, rest_mail_send.go) is checked inside
// handleMailSendInner, AFTER decodeMailJSON has already tried to read the
// body — but decodeMailJSON reads from r.Body, which withAuth has already
// wrapped in an http.MaxBytesReader(w, r.Body, 1<<20). A message with a
// real attachment over 1 MiB therefore fails at the generic reader before
// mail's own 25 MiB budget check ever runs. There is already a precedent
// for a route-specific larger limit — withUploadAuth, 1 GB, for
// /api/v1/library — Mail's mutation routes have none.
//
// Expected behaviour, derived from the wire contract (MailSendRequest /
// MC-32 / contracts/components/schemas/MailAttachmentInput.yaml) and the
// withUploadAuth precedent — NOT from what the handler chain currently
// does: a message with a ~2 MiB attachment (well under the 25 MiB MC-32
// budget) sent through the REAL registered HTTP handler chain must reach
// mail's own attachment-budget logic and succeed, exactly as the small
// attachment in TestMailSend_SentCopyLandsInSent
// (mail_send_smtp_sink_test.go) already does end to end over the same
// D36 fake-server strategy.
//
// RED today: the generic 1 MiB reader rejects the request before mail's
// own logic runs.

import (
	"crypto/rand"
	"encoding/json"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// mailSendBodyLimitAttachmentBytes is a ~2 MiB decoded attachment — well
// under MC-32's 25 MiB cap (mailMaxAttachmentBytes, rest_mail_send.go) but,
// base64-encoded on the wire, comfortably over withAuth's 1 MiB body cap
// (rest.go::withAuth). 2 MiB is the brief's own example figure for the
// finding; any value inside the "over 1 MiB, under 25 MiB" window proves
// the same gap.
const mailSendBodyLimitAttachmentBytes = 2 * 1024 * 1024

// TestMailSend_AttachmentOver1MiBReachesMailBudgetLogic drives a real send
// through the actual registered HTTP handler chain (env.mux, built by
// api.registerAdditionalEndpoints — the same production route table
// rest.go wires up, withAuth included) with an attachment inside Mail's
// own 25 MiB budget but over the generic 1 MiB body-size floor. A fixed
// implementation reaches handleMailSendInner's attachment-budget check,
// finds the attachment within budget, and completes the send against the
// fake IMAP/SMTP fixtures — 200 with a decodable MailSendResponse. The
// buggy implementation never gets that far.
func TestMailSend_AttachmentOver1MiBReachesMailBudgetLogic(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, _ := startPlainIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))

	raw := make([]byte, mailSendBodyLimitAttachmentBytes)
	_, err := rand.Read(raw)
	require.NoError(t, err)

	req := gen.MailSendRequest{
		To:           []string{"human@example.test"},
		Subject:      "N4 body limit over 1 MiB attachment",
		BodyMarkdown: "attachment is over 1 MiB but under the 25 MiB MC-32 budget",
		Attachments: &[]struct {
			ContentType string `json:"content_type"`
			DataBase64  []byte `json:"data_base64"`
			Filename    string `json:"filename"`
		}{
			{ContentType: "application/octet-stream", DataBase64: raw, Filename: "big.bin"},
		},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	require.Greater(t, len(body), 1<<20,
		"test setup: the wire body must exceed withAuth's 1 MiB cap to exercise the bug")
	require.Less(t, len(body), 25<<20,
		"test setup: the wire body must stay under MC-32's 25 MiB attachment cap")

	rec := mailDo(env.mux, "POST", "/api/v1/workspaces/"+mailRedWS+"/mail/"+mailRedAgent+"/messages",
		nextMailIP(), true, string(body))

	if rec.Code != 200 {
		t.Fatalf("a %d-byte wire body (2 MiB decoded attachment, under the 25 MiB MC-32 budget) "+
			"was rejected before reaching mail's own attachment-budget logic: status=%d body=%s "+
			"— the generic withAuth 1 MiB body-size reader (rest.go::withAuth -> "+
			"withAuthAndBodyLimit) blocks mail's send route, which has no larger, "+
			"route-specific limit of its own (compare withUploadAuth's 1 GB for /api/v1/library)",
			len(body), rec.Code, rec.Body.String())
	}
	var resp gen.MailSendResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp),
		"200 body must decode as MailSendResponse: %s", rec.Body.String())
	require.NotEmpty(t, resp.MessageId, "a successful send must report a message id")
}
