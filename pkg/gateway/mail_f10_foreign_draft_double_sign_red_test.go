package gateway

// F10 (foreign-draft half) RED — this task, 2026-09-29.
//
// The original F10 fix (6e9c4fbe2, landed, stays correct) makes viewFromRaw
// recognize an Omnipus draft's own text/markdown bookkeeping part by its
// X-Omnipus-Part: draft-body marker header. That fix is fine.
//
// The bug this test pins is narrower and different: it affects FOREIGN
// drafts only — a draft created in another mail client (Thunderbird,
// Outlook, Gmail web), never an Omnipus-created one. Per FR-030, a foreign
// draft intentionally gets NO bookkeeping part on Save — that keeps it
// looking normal in the owner's other mail client, and is correct, not the
// bug. The bug is that rest_mail_draft.go::handleMailDraftUpdate passes
// SignatureHTML: mb.SignatureHTML to email.Compose() UNCONDITIONALLY — even
// when the current copy is not an Omnipus draft (cur.IsOmnipusDraft ==
// false) — so the foreign draft's stored text/plain and text/html parts
// already carry the signature right after Save. Because there is no
// separate unsigned bookkeeping part for a foreign draft, a later read
// (pkg/email/view.go::viewFromRaw, the text/plain-lossy-fallback branch)
// derives BodyMarkdown FROM that already-signed body. When
// rest_mail_draft.go's send handler (the "cur.BodyMarkdown" fallback used
// whenever the send request omits body_markdown) feeds that
// already-signed text into email.Compose() a SECOND time, the signature is
// appended again, and its "--" separator line — sitting directly under the
// body's first line with no blank line between — becomes a valid CommonMark
// setext-heading underline, corrupting that line into an <h2> in the
// transmitted HTML part.
//
// Spec requirement this fix must not violate: FR-030 — a draft without
// X-Omnipus-Draft MUST still be fully editable and sendable (D24). The fix
// must stop the double signing without restricting foreign-draft editing.
//
// ORACLE PROVENANCE: expected values are the task's own root-cause diagnosis
// (task instructions, this task, 2026-09-29) plus FR-030 — never read off
// the current handler behavior. The stored-bytes assertion (step 4) and the
// transmitted-bytes assertions (step 5) are both expected to be RED against
// today's code: the diagnosis states plainly that today the stored parts DO
// carry the signature, and the final send carries it FOUR times (twice
// plain, twice HTML) with the first HTML line corrupted into <h2> — this
// test's failure messages must report that shape when it goes red.
//
// KNOWN LIMITATION, not addressed by this fix and not asserted here
// (informational only, per task instructions): if a foreign draft's stored
// text already contains a signature added by the OTHER mail client (e.g. a
// Thunderbird auto-signature the user typed before ever touching Omnipus),
// Omnipus's own signature still gets added on top of that at Send. There is
// no reliable way to detect and strip a foreign signature, and this test
// suite does not attempt it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	gomail "github.com/emersion/go-message/mail"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

const (
	f10fdFrom      = "mailbox@test.local"
	f10fdTo        = "alice@box.test"
	f10fdSigHTML   = "<p>Kind regards,<br><b>Mia</b></p>"
	f10fdSigMarker = "Kind regards,"

	// f10fdOrigBody is the foreign client's own original draft text — never
	// touched by Omnipus. It plays no further role once Save (step 3)
	// replaces it; it only has to be present so the seeded message is a
	// plausible foreign draft.
	f10fdOrigBody = "Original foreign draft text, never touched by Omnipus yet."

	// f10fdEditedBody is the "some new unsigned body text" the task's step 3
	// asks for — deliberately containing NO signature of its own, so any
	// occurrence of f10fdSigMarker found later can only have come from
	// Compose's own signature append(s), never from the edited text itself.
	f10fdEditedBody = "Edited body text for the foreign draft, never containing a signature of its own."
)

// f10fdForeignDraftRaw is a bare, hand-crafted MIME message simulating a
// draft saved by another mail client: multipart/alternative with only
// text/plain and text/html leaves, no X-Omnipus-Draft header anywhere. This
// is deliberately never built via pkg/email.Compose — a real foreign client
// never runs Omnipus's own composer.
func f10fdForeignDraftRaw() string {
	return "From: " + f10fdFrom + "\r\nTo: " + f10fdTo + "\r\nSubject: original foreign draft\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <f10fd-foreign@example.test>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=f10fdbnd\r\n\r\n" +
		"--f10fdbnd\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		f10fdOrigBody + "\r\n" +
		"--f10fdbnd\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>" + f10fdOrigBody + "</p>\r\n" +
		"--f10fdbnd--\r\n"
}

// f10fdLeaf is one MIME leaf's content type, filename (empty for a primary
// body part) and decoded body text.
type f10fdLeaf struct {
	ContentType string
	Filename    string
	Body        string
}

// f10fdParseLeaves walks raw RFC 5322 bytes and returns each MIME leaf's
// content type, filename and decoded body — the ground truth for what a set
// of bytes (stored or transmitted) actually carries, independent of any
// production view/compose code.
func f10fdParseLeaves(t *testing.T, raw []byte) []f10fdLeaf {
	t.Helper()
	r, err := gomail.CreateReader(bytes.NewReader(raw))
	require.NoError(t, err, "instrument: bytes under test must parse as MIME")
	var leaves []f10fdLeaf
	for {
		p, perr := r.NextPart()
		if perr != nil || p == nil {
			break
		}
		ct, ctParams, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		_, dispParams, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		name := dispParams["filename"]
		if name == "" {
			name = ctParams["name"]
		}
		body, _ := io.ReadAll(p.Body)
		leaves = append(leaves, f10fdLeaf{ContentType: ct, Filename: name, Body: string(body)})
	}
	return leaves
}

// f10fdPrimaryBody returns the decoded body of the one primary (no filename)
// leaf of the given content type, failing the test (instrument check, not
// the bug under test) if no such leaf exists.
func f10fdPrimaryBody(t *testing.T, leaves []f10fdLeaf, contentType, label string) string {
	t.Helper()
	for _, l := range leaves {
		if l.Filename == "" && l.ContentType == contentType {
			return l.Body
		}
	}
	t.Fatalf("instrument: %s has no primary %s leaf; cannot assert against it", label, contentType)
	return ""
}

// TestMailForeignDraftSave_SignatureNotBakedIntoStoredBody is step 3+4: a
// foreign draft (no X-Omnipus-Draft header) is Saved through the real PUT
// handler with a signature configured, then the RAW stored bytes (a direct
// IMAP FETCH BODY[] on a separate connection, never through ReadView) must
// show a clean, unsigned text/plain and text/html body — the signature
// belongs at Send, once, never baked into what gets saved.
func TestMailForeignDraftSave_SignatureNotBakedIntoStoredBody(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	smtpPort, _ := listenCount(t)
	pointMailboxAt(t, env, imapPort, smtpPort)
	f10rgSetSignature(t, env, f10fdSigHTML)

	// Step 1: seed the FOREIGN draft — a bare hand-crafted message, no
	// X-Omnipus-Draft header, appended with the \Draft flag exactly as a
	// real IMAP client would leave it.
	appendRaw(t, cl, "Drafts", []byte(f10fdForeignDraftRaw()), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)

	// Step 3: Save — the REAL PUT through the REAL handler
	// (handleMailDraftUpdate), with new unsigned body text.
	saveReqBody := fmt.Sprintf(
		`{"uid":1,"uidvalidity":%d,"to":[%q],"subject":"edited foreign draft","body_markdown":%q}`,
		uv, f10fdTo, f10fdEditedBody)
	rec := mailDo(env.mux, http.MethodPut, draftRefPath(uv, 1), nextMailIP(), true, saveReqBody)
	require.Equal(t, http.StatusOK, rec.Code, "Save PUT on a foreign draft must succeed (FR-030: foreign drafts stay fully editable); body: %s", rec.Body.String())
	var saveResp gen.MailMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saveResp), "Save response must decode as MailMessage")
	require.False(t, saveResp.IsOmnipusDraft,
		"instrument: the seeded draft (no X-Omnipus-Draft header) must be recognized as foreign, or this test proves nothing about the foreign-draft path")

	// Step 4 (THE DECISIVE PROBE): raw FETCH BODY[] on a SEPARATE IMAP
	// connection, for the NEW copy Save's own response names — the ground
	// truth for what fakemail actually stored, bypassing viewFromRaw
	// entirely.
	newUID := uint32(saveResp.Uid)
	rawStored := f10rgFetchRawBytes(t, cl, "Drafts", newUID)
	leaves := f10fdParseLeaves(t, rawStored)

	plainBody := f10fdPrimaryBody(t, leaves, "text/plain", "the stored foreign draft after Save")
	require.NotContains(t, plainBody, f10fdSigMarker,
		"F10 foreign-draft bug: the SAVED text/plain part already carries the account signature, even though a foreign draft's saved body must stay unsigned (the signature belongs at Send, once) — stored plain body: %q", plainBody)

	htmlBody := f10fdPrimaryBody(t, leaves, "text/html", "the stored foreign draft after Save")
	require.NotContains(t, htmlBody, f10fdSigMarker,
		"F10 foreign-draft bug: the SAVED text/html part already carries the account signature — stored html body: %q", htmlBody)
}

// TestMailForeignDraftSend_SignatureAppliedExactlyOnce is step 3+4+5: after
// the same Save as above, sending the saved draft (via the exact uid/
// uidvalidity ref Save's own response returned, with no body_markdown
// override — the send handler's own documented fallback to cur.BodyMarkdown,
// exactly what a panel refresh that never touched the body would submit)
// must transmit the account signature EXACTLY ONCE in each of the plain and
// HTML parts, and the body's first line must render as a paragraph, never a
// heading — the setext-heading corruption the double-signed "--" line
// produces today.
func TestMailForeignDraftSend_SignatureAppliedExactlyOnce(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
	f10rgSetSignature(t, env, f10fdSigHTML)

	// Steps 1+3: seed the foreign draft, then Save it with new unsigned body
	// text — identical setup to the stored-bytes test above (Save's own
	// double-signing bug is the precondition the send-side double-sign
	// builds on).
	appendRaw(t, cl, "Drafts", []byte(f10fdForeignDraftRaw()), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)
	saveReqBody := fmt.Sprintf(
		`{"uid":1,"uidvalidity":%d,"to":[%q],"subject":"edited foreign draft","body_markdown":%q}`,
		uv, f10fdTo, f10fdEditedBody)
	rec := mailDo(env.mux, http.MethodPut, draftRefPath(uv, 1), nextMailIP(), true, saveReqBody)
	require.Equal(t, http.StatusOK, rec.Code, "Save PUT must succeed; body: %s", rec.Body.String())
	var saveResp gen.MailMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saveResp), "Save response must decode as MailMessage")
	require.False(t, saveResp.IsOmnipusDraft, "instrument: seeded draft must be recognized as foreign")

	// Step 5: Send the SAVED copy, by the EXACT new ref Save's response
	// returned. body_markdown is deliberately omitted (empty) — the send
	// handler's own fallback ("markdown := cur.BodyMarkdown") is what a real
	// panel submits whenever the human sends without re-typing the body, and
	// that fallback is the second half of the double-sign mechanism this
	// test pins.
	sendPath := draftRefPath(uint32(saveResp.Uidvalidity), uint32(saveResp.Uid)) + "/send"
	sendReqBody := fmt.Sprintf(`{"uid":%d,"uidvalidity":%d,"to":[%q],"subject":"edited foreign draft","body_markdown":""}`,
		saveResp.Uid, saveResp.Uidvalidity, f10fdTo)
	sendRec := mailDo(env.mux, http.MethodPost, sendPath, nextMailIP(), true, sendReqBody)
	require.Equal(t, http.StatusOK, sendRec.Code, "Send POST must succeed against the loopback sink; body: %s", sendRec.Body.String())
	var sendResp gen.MailSendResponse
	require.NoError(t, json.Unmarshal(sendRec.Body.Bytes(), &sendResp), "Send response must decode as MailSendResponse")
	require.True(t, sendResp.SentSaved, "send must succeed cleanly; body: %s", sendRec.Body.String())

	n, bodies := sink.acceptedBodies()
	require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
	require.Len(t, bodies, 1)

	leaves := f10fdParseLeaves(t, []byte(bodies[0]))

	plainBody := f10fdPrimaryBody(t, leaves, "text/plain", "the transmitted message")
	plainCount := strings.Count(plainBody, f10fdSigMarker)
	require.Equal(t, 1, plainCount,
		"F10 foreign-draft double-sign bug: the transmitted text/plain part must carry the account signature EXACTLY ONCE; got %d occurrences — plain body: %q", plainCount, plainBody)

	htmlBody := f10fdPrimaryBody(t, leaves, "text/html", "the transmitted message")
	htmlCount := strings.Count(htmlBody, f10fdSigMarker)
	require.Equal(t, 1, htmlCount,
		"F10 foreign-draft double-sign bug: the transmitted text/html part must carry the account signature EXACTLY ONCE; got %d occurrences — html body: %q", htmlCount, htmlBody)

	for lvl := 1; lvl <= 6; lvl++ {
		tag := fmt.Sprintf("<h%d", lvl)
		require.NotContains(t, htmlBody, tag,
			"F10 foreign-draft double-sign bug: the double-appended signature's \"--\" separator corrupted the body's first line into a CommonMark setext heading (%s) — html body: %q", tag, htmlBody)
	}
}
