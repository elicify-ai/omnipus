package gateway

// Round-4 RED — the message.md attachment leak (founder-ordered; confirmed by
// three independent reviewers this gate). An Omnipus draft carries its own
// rendered Markdown source as a bookkeeping MIME part (pkg/email/compose.go::
// renderMarkdownPart: Content-Type text/markdown + Content-Disposition
// attachment; filename="message.md"), which the view parser lists in
// MailView.Attachments (pkg/email/view.go::viewFromRaw). The draft panel's
// carry-forward paths treat that part as a keepable user attachment:
//
//   - pkg/gateway/rest_mail_draft.go::handleMailDraftSendInner, the absent
//     keep_attachment_parts case (the contract's default carry-ALL), and
//   - pkg/gateway/rest_mail_draft.go::handleMailDraftUpdate, whose explicit
//     keep_attachment_parts indices name it whenever the panel keeps
//     everything it sees.
//
// The required behavior (oracle — derived from the marker definition and the
// audit-path recognition precedent, never from the buggy code):
//
//   - the sent message contains ZERO parts matching the draft-body marker
//     (filename "message.md" AND text/markdown), regardless of how many
//     keep-all edits preceded the send;
//   - the panel's attachment listing for a draft — the PUT update response's
//     MailMessage.attachments, the only MailMessage surface for a draft (the
//     drafts route shadows the generic GET-message route, so no GET read
//     exists for drafts) — never lists a part matching the marker;
//   - real user attachments ride every path untouched.
//
// Recognition precedent reused as the oracle's match condition:
// pkg/gateway/rest_mail_audit_fields.go::mailAuditDraftBodyPart
// (at.Name == "message.md" && at.ContentType == "text/markdown").

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomail "github.com/emersion/go-message/mail"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

const (
	leakDraftBody  = "original draft body"
	leakEdit1Body  = "edit one body"
	leakEdit2Body  = "edit two body"
	leakSendBody   = "final send body"
	leakAttachName = "notes.txt"
	leakAttachData = "real attachment bytes - kept through two edits"
)

// appendLeakDraft appends a byte-for-byte realistic agent draft — real
// email.Compose output (Draft: true, one genuine text/plain attachment) — to
// the fixture Drafts folder, exactly what a panel edit would act on.
// email.Compose with Draft: true renders exactly two listed parts for this
// input: the body bookkeeping part (message.md, listed first) and the real
// attachment (listed second), so a fresh copy's keep-everything list is [0, 1].
func appendLeakDraft(t *testing.T, cl *imapclient.Client) {
	t.Helper()
	out, err := email.Compose(email.ComposeInput{
		From:      "mailbox@test.local",
		To:        []string{"a@b.test"},
		Subject:   "leak fixture draft",
		Markdown:  leakDraftBody,
		Draft:     true,
		MessageID: "<leak-draft@example.test>",
		Attachments: []email.Attachment{
			{Name: leakAttachName, ContentType: "text/plain", Data: []byte(leakAttachData)},
		},
	})
	require.NoError(t, err)
	appendRaw(t, cl, "Drafts", out.Transmitted, []imap.Flag{imap.FlagDraft})
}

// editLeakDraft issues one panel edit (PUT) on the draft copy the ref names,
// carrying forward exactly the keep list given (positions into the current
// listing — the handler's indexing), with a fresh subject and body. Returns
// the decoded MailMessage response: the new copy's uid and the attachment
// listing the panel sees after the edit.
func editLeakDraft(t *testing.T, env *mailRedEnv, uv, uid uint32, keep []int, subject, body string) gen.MailMessage {
	t.Helper()
	keepJSON := make([]string, 0, len(keep))
	for _, idx := range keep {
		keepJSON = append(keepJSON, fmt.Sprint(idx))
	}
	reqBody := fmt.Sprintf(
		`{"uid":%d,"uidvalidity":%d,"to":["a@b.test"],"subject":%q,"body_markdown":%q,"keep_attachment_parts":[%s]}`,
		uid, uv, subject, body, strings.Join(keepJSON, ","))
	rec := mailDo(env.mux, http.MethodPut, draftRefPath(uv, uid), nextMailIP(), true, reqBody)
	require.Less(t, rec.Code, 300, "panel edit must succeed; body: "+rec.Body.String())
	var msg gen.MailMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &msg), "update response must decode as MailMessage")
	return msg
}

// draftBodyMarkerParts walks the MIME leaves of a transmitted message and
// returns a description of every part matching the draft-body marker —
// text/markdown named "message.md", the exact pair renderMarkdownPart writes
// and mailAuditDraftBodyPart recognizes. An empty result is the spec-derived
// expectation for every sent message; a parse failure is reported as a
// finding so it can never masquerade as a pass.
func draftBodyMarkerParts(raw string) []string {
	var found []string
	r, err := gomail.CreateReader(strings.NewReader(raw))
	if err != nil {
		return []string{"PARSE-FAILURE: " + err.Error()}
	}
	for {
		p, perr := r.NextPart()
		if perr != nil || p == nil {
			break
		}
		ct, ctParams, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		disp, dispParams, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		name := dispParams["filename"]
		if name == "" {
			name = ctParams["name"]
		}
		if ct == "text/markdown" && name == "message.md" {
			found = append(found, fmt.Sprintf("part content_type=%s name=%s disposition=%q", ct, name, disp))
		}
	}
	return found
}

// sendLeakDraft POSTs the panel send for the copy the ref names. keepList nil
// omits keep_attachment_parts entirely (the contract's default carry-ALL);
// a non-nil list is sent verbatim. Returns the decoded MailSendResponse and
// requires the send to have succeeded (a 200 with sent_saved=true) — a send
// that did not happen can prove nothing.
func sendLeakDraft(t *testing.T, env *mailRedEnv, uv, uid uint32, keepList []int) gen.MailSendResponse {
	t.Helper()
	reqBody := fmt.Sprintf(`{"uid":%d,"uidvalidity":%d,"to":["a@b.test"],"subject":"final send","body_markdown":%q`,
		uid, uv, leakSendBody)
	if keepList != nil {
		parts := make([]string, 0, len(keepList))
		for _, idx := range keepList {
			parts = append(parts, fmt.Sprint(idx))
		}
		reqBody += `,"keep_attachment_parts":[` + strings.Join(parts, ",") + `]`
	}
	reqBody += `}`
	rec := mailDo(env.mux, http.MethodPost, draftRefPath(uv, uid)+"/send", nextMailIP(), true, reqBody)
	require.Equal(t, http.StatusOK, rec.Code, "panel send must succeed against the loopback sink; body: "+rec.Body.String())
	var resp gen.MailSendResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.SentSaved, "sent_saved must be true on a clean send; body: "+rec.Body.String())
	return resp
}

// requireZeroMarkerParts asserts the spec oracle: no transmitted part matches
// the draft-body marker. The failure names every offending part.
func requireZeroMarkerParts(t *testing.T, body string) {
	t.Helper()
	marker := draftBodyMarkerParts(body)
	require.Empty(t, marker,
		"the sent message leaked the draft's own body bookkeeping part (message.md / text/markdown) as a literal attachment - found:\n%s\nfull body:\n%s",
		strings.Join(marker, "\n"), body)
}

// listingString renders one MailMessage attachment listing for failure
// messages.
func listingString(msg gen.MailMessage) string {
	parts := make([]string, 0, len(msg.Attachments))
	for _, at := range msg.Attachments {
		parts = append(parts, fmt.Sprintf("filename=%q content_type=%q part_index=%d", at.Filename, at.ContentType, at.PartIndex))
	}
	return strings.Join(parts, "; ")
}

// containsMarkerListing reports whether a MailMessage attachment listing
// shows a text/markdown part named message.md — the draft-body marker.
func containsMarkerListing(msg gen.MailMessage) bool {
	for _, at := range msg.Attachments {
		if at.Filename == "message.md" && at.ContentType == "text/markdown" {
			return true
		}
	}
	return false
}

// listsRealAttachment reports whether a MailMessage attachment listing shows
// the fixture's real text/plain attachment.
func listsRealAttachment(msg gen.MailMessage) bool {
	for _, at := range msg.Attachments {
		if at.Filename == leakAttachName {
			return true
		}
	}
	return false
}

// allPositions returns 0..n-1 — the keep-everything index list for a listing
// of n entries.
func allPositions(n int) []int {
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, i)
	}
	return out
}

func TestMailDraftPanelSend_NeverLeaksDraftBodyPart(t *testing.T) {
	// Oracle: the draft's own message.md bookkeeping part is Omnipus's
	// bookkeeping, never a user attachment (dispatch item 1; the audit path
	// already excludes it via mailAuditDraftBodyPart). Every subtest edits at
	// least twice (the accumulation shape) before sending, keeping everything
	// the listing shows — the panel's natural flow.
	t.Run("two keep-all edits then default-carry-all send transmit zero marker parts", func(t *testing.T) {
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		appendLeakDraft(t, cl)
		uv := draftUIDValidity(t, cl)

		m1 := editLeakDraft(t, env, uv, 1, []int{0, 1}, "edit one", leakEdit1Body)
		m2 := editLeakDraft(t, env, uv, uint32(m1.Uid), allPositions(len(m1.Attachments)), "edit two", leakEdit2Body)

		resp := sendLeakDraft(t, env, uv, uint32(m2.Uid), nil)
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		requireZeroMarkerParts(t, bodies[0])
		require.Contains(t, bodies[0], leakAttachData,
			"the real user attachment must still ride the default carry-all send - a fix that strips real attachments fails here")
		require.NotEmpty(t, resp.MessageId)
	})

	t.Run("keep-everything-by-index send carries no marker part either", func(t *testing.T) {
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		appendLeakDraft(t, cl)
		uv := draftUIDValidity(t, cl)

		m1 := editLeakDraft(t, env, uv, 1, []int{0, 1}, "edit one", leakEdit1Body)
		m2 := editLeakDraft(t, env, uv, uint32(m1.Uid), allPositions(len(m1.Attachments)), "edit two", leakEdit2Body)

		// The panel keeps everything the listing shows, by index: every
		// position of the last response's attachment listing. Under the bug
		// the listing itself carries marker entries, so this explicit-list
		// send must still transmit zero marker parts (and keep the real
		// attachment) once fixed.
		resp := sendLeakDraft(t, env, uv, uint32(m2.Uid), allPositions(len(m2.Attachments)))
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		requireZeroMarkerParts(t, bodies[0])
		require.Contains(t, bodies[0], leakAttachData,
			"the real user attachment must still ride the keep-all-by-index send")
		_ = resp
	})

	t.Run("explicit keep list naming only the real attachment keeps it and only it", func(t *testing.T) {
		// The last position of the current listing is the real attachment
		// (marker entries, where the bug lists them, sit at earlier
		// positions). Honoring the explicit list must carry exactly that
		// part — and never a marker part (kills an over-stripping fix).
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		appendLeakDraft(t, cl)
		uv := draftUIDValidity(t, cl)

		m1 := editLeakDraft(t, env, uv, 1, []int{0, 1}, "edit one", leakEdit1Body)
		m2 := editLeakDraft(t, env, uv, uint32(m1.Uid), allPositions(len(m1.Attachments)), "edit two", leakEdit2Body)
		last := len(m2.Attachments) - 1

		resp := sendLeakDraft(t, env, uv, uint32(m2.Uid), []int{last})
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		requireZeroMarkerParts(t, bodies[0])
		require.Contains(t, bodies[0], leakAttachData, "the explicitly kept real attachment must ride the send")
		_ = resp
	})
}

func TestMailDraftRead_NeverListsDraftBodyPart(t *testing.T) {
	// Oracle: MailMessage.attachments describes the message's USER
	// attachments; the draft's own body bookkeeping part is not one (same
	// recognition pair as the audit path). Asserted on the PUT update
	// responses of the same twice-edited draft — the only MailMessage
	// surface for a draft (the drafts route shadows the generic GET-message
	// route) and the listing the panel carries forward from.
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	smtpPort, _ := listenCount(t)
	pointMailboxAt(t, env, imapPort, smtpPort)
	appendLeakDraft(t, cl)
	uv := draftUIDValidity(t, cl)

	m1 := editLeakDraft(t, env, uv, 1, []int{0, 1}, "edit one", leakEdit1Body)
	m2 := editLeakDraft(t, env, uv, uint32(m1.Uid), allPositions(len(m1.Attachments)), "edit two", leakEdit2Body)

	for name, m := range map[string]gen.MailMessage{"edit one response": m1, "edit two response": m2} {
		require.False(t, containsMarkerListing(m),
			"%s: the draft's own body bookkeeping part (message.md / text/markdown) must never be listed as an attachment - listing: %s",
			name, listingString(m))
		require.True(t, listsRealAttachment(m),
			"%s: the real user attachment must still be listed; listing: %s", name, listingString(m))
		require.NotEmpty(t, m.Attachments, "%s: the draft carries one real attachment; the listing may not be empty", name)
	}
}
