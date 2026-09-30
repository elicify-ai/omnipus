package gateway

// F10 real-gateway-path — Omnipus-draft case (delta-review round 2, item 3;
// pr-test-analyzer).
//
// Delta-review finding: this file used to be
// TestMailDraftSave_RealGatewayPath_RawBytesInvestigation, an investigation
// probe that only t.Logf'd its findings — it could never fail, so it was not
// a test (repo rule: no test that can't fail). The live F10 bug it was
// written to investigate is a SEPARATE, narrower case — a FOREIGN draft (no
// X-Omnipus-Draft header) getting its account signature baked in at Save,
// and then double-signed at Send — already covered and fixed by
// mail_f10_foreign_draft_double_sign_red_test.go
// (TestMailForeignDraftSave_SignatureNotBakedIntoStoredBody /
// TestMailForeignDraftSend_SignatureAppliedExactlyOnce) and the F10 Option B
// fix (rest_mail_draft.go::handleMailDraftUpdate). This file is a SEPARATE
// test-quality fix, not a functional fix: it turns the old investigation
// probe into a real, asserting test for the OMNIPUS-draft case (a draft
// Omnipus itself composed, carrying X-Omnipus-Draft and the X-Omnipus-Part:
// draft-body bookkeeping part) through the same real gateway HTTP path,
// which is supposed to stay clean end to end — Save never bakes the
// signature into the stored copy, the marker part survives Save, a
// subsequent GET reports markdown_lossy=false, and Send applies the
// signature exactly once with no setext-heading corruption.
//
// ORACLE PROVENANCE: expected values come from the spec (FR-030's converse —
// an Omnipus-authored draft's bookkeeping part is recognized by
// X-Omnipus-Part: draft-body, per the F1 ruling in
// mail_draft_marker_header_red_test.go) and from the already-landed F10 fix
// (6e9c4fbe2) this test exercises through the real HTTP path rather than
// pkg/email's own unit-level tests — never read off this test's own first
// run.

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
	"github.com/emersion/go-imap/v2/imapclient"
	gomail "github.com/emersion/go-message/mail"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

const (
	f10rgFrom      = "mailbox@test.local"
	f10rgTo        = "alice@box.test"
	f10rgFirstLine = "Hello Alice, this is the F10 real-gateway re-verification body."
	f10rgSigHTML   = "<p>Kind regards,<br><b>Mia</b></p>"
	f10rgSigMarker = "Kind regards,"
)

// f10rgSetSignature stamps the mailbox's SignatureHTML directly on the
// config map entry the same way pointMailboxAt stamps host/port — the
// bug only manifests when a signature is configured (an unsigned Compose
// has nothing for a lossy fallback to duplicate).
func f10rgSetSignature(t *testing.T, env *mailRedEnv, sigHTML string) {
	t.Helper()
	cfg := env.api.agentLoop.GetConfig()
	mb := cfg.Mailboxes[mailRedAgent][mailRedWS]
	mb.SignatureHTML = sigHTML
	cfg.Mailboxes[mailRedAgent][mailRedWS] = mb
}

// f10rgFetchRawBytes issues a RAW IMAP FETCH BODY[] for one UID in one
// folder, on the given (already-authenticated) client connection — the
// same shape production's ReadView uses (imap.FetchItemBodySection{Peek:
// true}, whole-message zero value), but called directly against the
// server, bypassing viewFromRaw entirely. This is the ground truth for
// what fakemail actually stored.
func f10rgFetchRawBytes(t *testing.T, cl *imapclient.Client, folder string, uid uint32) []byte {
	t.Helper()
	_, err := cl.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	require.NoError(t, err, "SELECT %s", folder)
	opts := &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}}
	bufs, err := cl.Fetch(imap.UIDSetNum(imap.UID(uid)), opts).Collect()
	require.NoError(t, err, "FETCH UID %d in %s", uid, folder)
	require.Len(t, bufs, 1, "expected exactly one message at UID %d in %s", uid, folder)
	var raw []byte
	for _, sec := range bufs[0].BodySection {
		if len(sec.Bytes) > 0 {
			raw = sec.Bytes
		}
	}
	require.NotEmpty(t, raw, "FETCH BODY[] returned no bytes for UID %d in %s", uid, folder)
	return raw
}

// f10rgDerefStr returns the empty-safe form of a possibly-nil *string, for
// readable failure messages.
func f10rgDerefStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// f10rgLeaf is one MIME leaf's content type, filename (empty for a primary
// body part), the X-Omnipus-Part marker header value, and its decoded body —
// the ground truth for what a set of bytes (stored or transmitted) actually
// carries, independent of any production view/compose code.
type f10rgLeaf struct {
	ContentType string
	Filename    string
	OmnipusPart string
	Body        string
}

// f10rgParseLeaves walks raw RFC 5322 bytes and returns each MIME leaf's
// content type, filename, X-Omnipus-Part header value and decoded body.
func f10rgParseLeaves(t *testing.T, raw []byte) []f10rgLeaf {
	t.Helper()
	r, err := gomail.CreateReader(bytes.NewReader(raw))
	require.NoError(t, err, "instrument: bytes under test must parse as MIME")
	var leaves []f10rgLeaf
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
		leaves = append(leaves, f10rgLeaf{
			ContentType: ct,
			Filename:    name,
			OmnipusPart: strings.TrimSpace(p.Header.Get("X-Omnipus-Part")),
			Body:        string(body),
		})
	}
	return leaves
}

// TestMailOmnipusDraftSave_RealGatewayPath_StaysClean is the OMNIPUS-draft
// counterpart to the foreign-draft tests in
// mail_f10_foreign_draft_double_sign_red_test.go: PUT through the real
// handler (handleMailDraftUpdate), a raw FETCH on a SEPARATE IMAP connection
// (never through ReadView/viewFromRaw) to prove what was actually stored,
// the real GET, then the real Send — asserting the OMNIPUS-draft path stays
// clean throughout, per the F10 fix already landed for it.
func TestMailOmnipusDraftSave_RealGatewayPath_StaysClean(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
	f10rgSetSignature(t, env, f10rgSigHTML)

	// Step 0: the agent's original draft — unsigned, exactly like
	// tools/email_compose.go's create_email_draft (never sets SignatureHTML).
	// This is a REAL Omnipus draft: it carries X-Omnipus-Draft and the
	// X-Omnipus-Part: draft-body bookkeeping part.
	origOut, cerr := email.Compose(email.ComposeInput{
		From: f10rgFrom, To: []string{f10rgTo}, Subject: "F10 real gateway readback",
		Markdown: f10rgFirstLine, Draft: true,
	})
	require.NoError(t, cerr, "Compose(orig)")
	appendRaw(t, cl, "Drafts", origOut.Transmitted, []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)

	// Step 1: Save — the REAL PUT through the REAL handler
	// (handleMailDraftUpdate), same request shape a real panel edit sends.
	reqBody := fmt.Sprintf(
		`{"uid":1,"uidvalidity":%d,"to":[%q],"subject":"F10 real gateway readback","body_markdown":%q}`,
		uv, f10rgTo, f10rgFirstLine)
	rec := mailDo(env.mux, http.MethodPut, draftRefPath(uv, 1), nextMailIP(), true, reqBody)
	require.Equal(t, http.StatusOK, rec.Code, "Save PUT must succeed; body: %s", rec.Body.String())
	var saveResp gen.MailMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saveResp), "Save response must decode as MailMessage")
	require.True(t, saveResp.IsOmnipusDraft,
		"instrument: the seeded draft (composed by Omnipus with Draft:true) must be recognized as an Omnipus draft, or this test proves nothing about the Omnipus-draft path")

	// Step 2 (THE DECISIVE PROBE): raw FETCH BODY[] on a SEPARATE IMAP
	// connection, before any GET — the ground truth for what fakemail
	// actually stored, bypassing viewFromRaw/ReadView entirely. The
	// bookkeeping part (the unsigned Markdown source) must have survived
	// Save; its OWN body must stay unsigned — the signature belongs only in
	// the rendered plain/html display parts Compose already produces on
	// every save, never in the bookkeeping source itself.
	newUID := uint32(saveResp.Uid)
	rawStored := f10rgFetchRawBytes(t, cl, "Drafts", newUID)
	leavesStored := f10rgParseLeaves(t, rawStored)

	var markerLeafPresent bool
	var markerBody string
	for _, l := range leavesStored {
		if l.OmnipusPart == "draft-body" {
			markerLeafPresent = true
			markerBody = l.Body
		}
	}
	require.True(t, markerLeafPresent,
		"F10: the stored raw bytes after Save must still carry the X-Omnipus-Part: draft-body bookkeeping part — its absence is how a foreign-draft-shaped bug would manifest for an Omnipus draft too")
	require.NotContains(t, markerBody, f10rgSigMarker,
		"F10: the bookkeeping part's own Markdown source must stay unsigned — the signature belongs only in the rendered display parts, never baked into the editable source")

	// Step 3: the real GET, by the NEW uid/uidvalidity Save's own response
	// returned — exactly what a real subsequent panel refresh uses. The
	// Omnipus-draft path is supposed to stay clean: markdown_lossy must be
	// false (the marker part gives ReadView an exact, non-derived source).
	getPath := mailMessagesPath("drafts") + "/" + fmt.Sprintf("uid:%d:%d", saveResp.Uidvalidity, saveResp.Uid)
	getRec := mailDo(env.mux, http.MethodGet, getPath, nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, getRec.Code, "GET must succeed; body: %s", getRec.Body.String())
	var getResp gen.MailMessage
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &getResp), "GET response must decode as MailMessage")
	require.False(t, getResp.MarkdownLossy,
		"F10: an Omnipus draft's GET must report markdown_lossy=false — a marker-recognized draft has an exact editable source, never a server-derived, signature-carrying fallback")
	require.NotContains(t, f10rgDerefStr(getResp.BodyMarkdown), f10rgSigMarker,
		"F10: BodyMarkdown for an Omnipus draft must not already carry the account signature")

	// Step 4: Send the saved copy, by the exact new ref Save's response
	// returned, with body_markdown omitted — the send handler's own
	// documented fallback to cur.BodyMarkdown, exactly what a panel refresh
	// that never re-typed the body would submit. The signature must apply
	// exactly once in each part, with no setext-heading corruption — same
	// shape as TestMailForeignDraftSend_SignatureAppliedExactlyOnce's
	// send-side assertions, but for the OMNIPUS-draft case.
	sendPath := draftRefPath(uint32(saveResp.Uidvalidity), uint32(saveResp.Uid)) + "/send"
	sendReqBody := fmt.Sprintf(`{"uid":%d,"uidvalidity":%d,"to":[%q],"subject":"F10 real gateway readback","body_markdown":""}`,
		saveResp.Uid, saveResp.Uidvalidity, f10rgTo)
	sendRec := mailDo(env.mux, http.MethodPost, sendPath, nextMailIP(), true, sendReqBody)
	require.Equal(t, http.StatusOK, sendRec.Code, "Send POST must succeed against the loopback sink; body: %s", sendRec.Body.String())
	var sendResp gen.MailSendResponse
	require.NoError(t, json.Unmarshal(sendRec.Body.Bytes(), &sendResp), "Send response must decode as MailSendResponse")
	require.True(t, sendResp.SentSaved, "send must succeed cleanly; body: %s", sendRec.Body.String())

	n, bodies := sink.acceptedBodies()
	require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
	require.Len(t, bodies, 1)

	leavesSent := f10rgParseLeaves(t, []byte(bodies[0]))

	var plainBody, htmlBody string
	var markedCount int
	for _, l := range leavesSent {
		if l.OmnipusPart != "" {
			markedCount++
			continue // the bookkeeping part is never sent — excluded from the primary-body scan below
		}
		if l.Filename != "" {
			continue // an attachment, not the primary plain/html body
		}
		switch l.ContentType {
		case "text/plain":
			plainBody = l.Body
		case "text/html":
			htmlBody = l.Body
		}
	}
	require.Equal(t, 0, markedCount,
		"F10: the sent message must carry zero X-Omnipus-Part-marked leaves — the bookkeeping part is never sent")

	plainCount := strings.Count(plainBody, f10rgSigMarker)
	require.Equal(t, 1, plainCount,
		"F10: the transmitted text/plain part must carry the account signature EXACTLY ONCE; got %d occurrences — plain body: %q", plainCount, plainBody)

	htmlCount := strings.Count(htmlBody, f10rgSigMarker)
	require.Equal(t, 1, htmlCount,
		"F10: the transmitted text/html part must carry the account signature EXACTLY ONCE; got %d occurrences — html body: %q", htmlCount, htmlBody)

	for lvl := 1; lvl <= 6; lvl++ {
		tag := fmt.Sprintf("<h%d", lvl)
		require.NotContains(t, htmlBody, tag,
			"F10: a double-appended signature's \"--\" separator would corrupt the body's first line into a CommonMark setext heading (%s) — html body: %q", tag, htmlBody)
	}
}
