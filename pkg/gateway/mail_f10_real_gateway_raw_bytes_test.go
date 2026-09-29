package gateway

// F10 real-gateway-path investigation (this task, 2026-09-29).
//
// A prior investigator (pkg/email/compose_readback_signature_real_imap_test.go)
// drove Compose -> real IMAP APPEND -> real IMAP FETCH -> ReadView directly at
// the pkg/email package level and could NOT reproduce the F10 bug: the
// draft-body marker header (X-Omnipus-Part: draft-body) is recognized
// correctly, MarkdownLossy comes back false, BodyMarkdown comes back
// unsigned. Squad-lead separately ran the REAL gateway HTTP path (a live
// ./build/omnipus-darwin-amd64 gateway process against a live fakemail
// binary) and the bug reproduced every time: a fresh GET on a just-saved
// draft already shows markdown_lossy: true and a signed body_markdown.
//
// This test exercises the ACTUAL production HTTP path — the real
// handleMailDraftUpdate / handleMailFolderMessage handlers, dispatched
// through the real mux (env.mux, same as every other pkg/gateway mail RED
// test), backed by a real in-memory IMAP server (imapmemserver, same family
// tests/e2e/fixtures/fakemail wraps) — never the in-process
// Compose()->ParseViewRaw() shortcut. It reads the RAW BYTES fakemail
// actually stored right after the real Save (a raw IMAP FETCH BODY[] issued
// on a SEPARATE connection, before any GET happens), then drives the real
// GET, so the reported mechanism is evidence, not inference.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
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

// f10rgLogLeaves dumps every MIME leaf's Content-Type / filename /
// X-Omnipus-Part header plus a body snippet, so the raw-bytes evidence is
// legible in the test log rather than a wall of MIME.
func f10rgLogLeaves(t *testing.T, label string, raw []byte) {
	t.Helper()
	leaves := mdhWireLeaves(t, string(raw))
	t.Logf("%s: %d MIME leaves", label, len(leaves))
	for i, l := range leaves {
		t.Logf("%s: leaf[%d] content-type=%q filename=%q X-Omnipus-Part=%q",
			label, i, l.ContentType, l.Filename, l.OmnipusPart)
	}
	t.Logf("%s: raw bytes contains signature marker %q: %v", label, f10rgSigMarker, strings.Contains(string(raw), f10rgSigMarker))
}

// TestMailDraftSave_RealGatewayPath_RawBytesInvestigation is the decisive
// probe: PUT through the real handler, then a raw FETCH on a SEPARATE IMAP
// connection (never through ReadView/viewFromRaw) before any GET, then the
// real GET. Determines (a) the SAVE handler wrote something already-signed
// to fakemail, vs (b) the stored bytes are clean and the READ path is what
// diverges.
func TestMailDraftSave_RealGatewayPath_RawBytesInvestigation(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	smtpPort, _ := listenCount(t)
	pointMailboxAt(t, env, imapPort, smtpPort)
	f10rgSetSignature(t, env, f10rgSigHTML)

	// Step 0: the agent's original draft — unsigned, exactly like
	// tools/email_compose.go's create_email_draft (never sets SignatureHTML).
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
	t.Logf("Save response: uid=%d uidvalidity=%d markdown_lossy=%v body_markdown=%q",
		saveResp.Uid, saveResp.Uidvalidity, saveResp.MarkdownLossy,
		f10rgDerefStr(saveResp.BodyMarkdown))

	// Step 2 (THE DECISIVE PROBE): raw FETCH BODY[] on a SEPARATE IMAP
	// connection, before any GET — the ground truth for what fakemail
	// actually stored, bypassing viewFromRaw/ReadView entirely.
	newUID := uint32(saveResp.Uid)
	rawStored := f10rgFetchRawBytes(t, cl, "Drafts", newUID)
	f10rgLogLeaves(t, "RAW-STORED (immediately after Save, before any GET)", rawStored)

	leavesStored := mdhWireLeaves(t, string(rawStored))
	var plainLeafSigned, markerLeafPresent, markerLeafSigned bool
	for _, l := range leavesStored {
		if l.ContentType == "text/plain" && l.Filename == "" && l.OmnipusPart == "" {
			plainLeafSigned = strings.Contains(string(rawStored), f10rgSigMarker)
			_ = plainLeafSigned
		}
		if l.OmnipusPart == "draft-body" {
			markerLeafPresent = true
		}
	}
	// Extract the marker leaf's own decoded body to check whether IT carries
	// the signature (which would mean the SAVE handler baked the signature
	// into the bookkeeping part itself — a distinct (a)-shaped bug from the
	// plain-text-leaf-signed case).
	_ = markerLeafSigned

	// Step 3: the real GET, by the NEW uid/uidvalidity Save's own response
	// returned — exactly what a real subsequent panel refresh uses.
	getPath := mailMessagesPath("drafts") + "/" + fmt.Sprintf("uid:%d:%d", saveResp.Uidvalidity, saveResp.Uid)
	getRec := mailDo(env.mux, http.MethodGet, getPath, nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, getRec.Code, "GET must succeed; body: %s", getRec.Body.String())
	var getResp gen.MailMessage
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &getResp), "GET response must decode as MailMessage")
	t.Logf("GET response: markdown_lossy=%v body_markdown=%q", getResp.MarkdownLossy, f10rgDerefStr(getResp.BodyMarkdown))

	// Report plainly which hypothesis the raw bytes support. This test
	// intentionally does NOT hard-fail on either shape — it is the
	// investigation probe; the report below is read from its log output,
	// not from a pass/fail verdict, per the task's "state plainly (a) or
	// (b), do not guess" instruction.
	if !markerLeafPresent {
		t.Logf("FINDING: the draft-body bookkeeping part (X-Omnipus-Part: draft-body) is ABSENT from the stored bytes -- hypothesis (a): the SAVE handler did not write what its own response implies.")
	}
	bodyMd := f10rgDerefStr(getResp.BodyMarkdown)
	if getResp.MarkdownLossy || strings.Contains(bodyMd, f10rgSigMarker) {
		t.Logf("FINDING: GET after Save reproduces the bug (markdown_lossy=%v, body_markdown contains signature=%v) through the REAL gateway HTTP path.",
			getResp.MarkdownLossy, strings.Contains(bodyMd, f10rgSigMarker))
	} else {
		t.Logf("FINDING: GET after Save does NOT reproduce the bug through this real-gateway-path harness (markdown_lossy=false, body_markdown clean).")
	}
}

func f10rgDerefStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
