package gateway

// Round-6 RED — F1, the X-Omnipus-Part draft-body marker (squad-lead ruling;
// qa-lead pack). Security-lead round 5 found that the draft-body bookkeeping
// part is recognized by the filename/type pair message.md + text/markdown
// alone (rest_mail_audit_fields.go::mailViewDraftBodyPart and
// mailAuditDraftBodyPart), so a GENUINE user attachment with that pair is
// silently hidden from listings and dropped from carries and sends.
//
// Ruling: Omnipus marks its own bookkeeping part unambiguously with the
// dedicated MIME header X-Omnipus-Part: draft-body (written by
// pkg/email/compose.go::renderMarkdownPart), and every filter recognizes the
// bookkeeping part by that header — never by name/type. Required behavior:
//
//   (a) the draft composed by Omnipus carries the header on its bookkeeping
//       part and only there — pinned in pkg/email (see
//       mail_draft_marker_header_red_test.go in that package);
//   (b) a genuine user attachment named message.md (text/markdown) — inbound
//       (read path) or uploaded via draft update req.Attachments — is listed,
//       carried on update and send (default carry-all and explicit keep by
//       its stable part_index), and appears in the sent message;
//   (c) the header-marked bookkeeping part is still never listed, carried or
//       sent, even when a keep list names its own stable index;
//   (d) the read path's attachment listing never lists the header-marked
//       bookkeeping part either (rest_mail_read.go::handleMailFolderMessage's
//       listing loop has no filter today);
//   (e) the draft-sent audit stays count-consistent with what is sent.
//
// ROUTING FACT the (d) pin rests on (ambiguity flagged to squad-lead): the
// drafts route SHADOWS the generic GET-message route
// (rest_mail.go::handleWorkspaceMail — len-5 drafts case precedes the generic
// case; GET is answered 405 by handleMailDraftAction) and the contract
// (contracts/openapi.yaml, /workspaces/{id}/mail/{agentId}/folders/drafts/
// messages/{ref}) defines PUT, DELETE and the send POST only — no GET read
// exists for the Drafts folder on the wire. The read-path listing the
// dispatch names is therefore pinned on the generic route with a draft-marked
// message served from INBOX: handleMailFolderMessage's listing loop is shared
// for every folder, so a header-based filter there covers the Drafts surface
// should a drafts GET read ever be added.
//
// ORACLE PROVENANCE (never the current code): expected values derive from the
// ruling (header recognition), the contract (keep_attachment_parts values are
// the listing's stable part_index fields — the download route's {partIndex};
// draft update is full-replace; send with keep_attachment_parts omitted is
// the default carry-ALL), and the addressing authority — the download route,
// probed for each part's unique bytes (idxProbeStablePartIndex). No expected
// index, status or count was read off the current implementation.
//
// Mutations this pack must kill (CHECK's job, deferred per RED scope): swap
// any filter back to name/type; stamp the marker header on user attachments;
// drop the bookkeeping part without writing the header; make the keep paths
// reject the user part; count the bookkeeping part in the audit.

import (
	"encoding/base64"
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

// The genuine user attachment (scenario B's upload; scenario A's inbound).
const (
	mdhUserMdName = "message.md"
	mdhUserMdData = "user message.md upload payload q3v7 unique marker body\n"
	mdhUserMdCT   = "text/markdown"
)

// mdhUploadFlow sets up one subtest's mailbox: an Omnipus draft (real
// email.Compose output, no attachments) appended to the fixture Drafts
// folder, then ONE panel edit that uploads a genuine message.md
// (text/markdown) with keep_attachment_parts=[] (full-replace: keep nothing
// else). The appended copy therefore carries BOTH a bookkeeping part and a
// genuine user message.md — indistinguishable by name/type, distinguished
// only by the marker header after the fix. Returns the env, the SMTP sink,
// the folder uidvalidity, the new copy's uid and the update response.
func mdhUploadFlow(t *testing.T, tag string) (*mailRedEnv, *smtpSink, uint32, int64, gen.MailMessage) {
	t.Helper()
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))

	out, cerr := email.Compose(email.ComposeInput{
		From:      "mailbox@test.local",
		To:        []string{"a@b.test"},
		Subject:   "marker header flow fixture",
		Markdown:  "marker header flow fixture body",
		Draft:     true,
		MessageID: "<mdh-flow-" + tag + "@example.test>",
	})
	require.NoError(t, cerr)
	appendRaw(t, cl, "Drafts", out.Transmitted, []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)

	reqBody := fmt.Sprintf(
		`{"uid":1,"uidvalidity":%d,"to":["a@b.test"],"subject":"after upload","body_markdown":"body after upload edit",`+
			`"keep_attachment_parts":[],"attachments":[{"filename":%q,"content_type":%q,"data_base64":%q}]}`,
		uv, mdhUserMdName, mdhUserMdCT,
		base64.StdEncoding.EncodeToString([]byte(mdhUserMdData)))
	rec := mailDo(env.mux, http.MethodPut, draftRefPath(uv, 1), nextMailIP(), true, reqBody)
	require.Less(t, rec.Code, 300, "panel edit with the message.md upload must succeed; body: "+rec.Body.String())
	var msg gen.MailMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &msg), "update response must decode as MailMessage")
	return env, sink, uv, msg.Uid, msg
}

// mdhWireLeaves walks the MIME leaves of transmitted bytes and returns, per
// leaf, the content type, the filename and the X-Omnipus-Part value — the
// wire-level oracle for what a send actually carried.
func mdhWireLeaves(t *testing.T, raw string) []struct {
	ContentType string
	Filename    string
	OmnipusPart string
} {
	t.Helper()
	r, err := gomail.CreateReader(strings.NewReader(raw))
	require.NoError(t, err, "instrument: transmitted bytes must parse as MIME")
	var leaves []struct {
		ContentType string
		Filename    string
		OmnipusPart string
	}
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
		leaves = append(leaves, struct {
			ContentType string
			Filename    string
			OmnipusPart string
		}{
			ContentType: ct,
			Filename:    name,
			OmnipusPart: strings.TrimSpace(p.Header.Get("X-Omnipus-Part")),
		})
	}
	return leaves
}

// mdhMessageMdCount counts message.md/text-markdown leaves in transmitted
// bytes (the wire-visible name/type pair).
func mdhMessageMdCount(t *testing.T, raw string) int {
	t.Helper()
	n := 0
	for _, l := range mdhWireLeaves(t, raw) {
		if l.ContentType == mdhUserMdCT && l.Filename == mdhUserMdName {
			n++
		}
	}
	return n
}

// mdhMarkedCount counts X-Omnipus-Part-marked leaves in transmitted bytes —
// the post-fix definition of the bookkeeping part (c).
func mdhMarkedCount(t *testing.T, raw string) int {
	t.Helper()
	n := 0
	for _, l := range mdhWireLeaves(t, raw) {
		if l.OmnipusPart != "" {
			n++
		}
	}
	return n
}

// mdhRequireUserMdSent asserts the scenario-B send oracle on transmitted
// bytes: exactly one message.md leaf, it is the USER's (no marker header),
// its bytes ride verbatim, and no marker-header leaf rode at all.
func mdhRequireUserMdSent(t *testing.T, body string) {
	t.Helper()
	require.Equal(t, 1, mdhMessageMdCount(t, body),
		"the sent message must carry exactly one message.md/text-markdown leaf - the user's; zero means a name/type filter still eats the user attachment")
	require.Equal(t, 0, mdhMarkedCount(t, body),
		"the sent message must carry zero X-Omnipus-Part-marked leaves - the bookkeeping part is never sent (c)")
	require.Contains(t, body, mdhUserMdData,
		"the user's message.md bytes must ride the send verbatim")
	for _, l := range mdhWireLeaves(t, body) {
		if l.ContentType == mdhUserMdCT && l.Filename == mdhUserMdName {
			require.Equal(t, "", l.OmnipusPart,
				"the message.md leaf that rode the send must be the user's (no marker header), not the bookkeeping part")
		}
	}
}

// mdhDownloadProbeFor returns the stable part index the download route serves
// a part's unique bytes at — the contract's addressing authority, used to
// derive keep values and to cross-check listing part_index fields.
func mdhDownloadProbeFor(t *testing.T, env *mailRedEnv, refPath, marker, label string) int {
	t.Helper()
	return idxProbeStablePartIndex(t, env, refPath, marker, label)
}

func TestMailDraftUpload_UserMessageMd_RidesListingCarryAndSend(t *testing.T) {
	// (b) via the upload route (security-lead scenario B). Every subtest
	// drives the same flow (one panel edit uploading a genuine message.md)
	// and differs only in the surface it pins.
	t.Run("update response lists the uploaded message.md under its stable index", func(t *testing.T) {
		env, _, uv, newUID, msg := mdhUploadFlow(t, "listing")
		newRef := draftRefPath(uv, uint32(newUID))

		require.Len(t, msg.Attachments, 1,
			"the update response must list the user's message.md upload (recognition by the marker header, never by name/type - a genuine message.md is a real attachment); listing is empty today because the name/type filter hides it; response: %+v", msg)
		entry := msg.Attachments[0]
		require.Equal(t, mdhUserMdName, entry.Filename, "the listed entry must be the user's message.md")
		require.Equal(t, mdhUserMdCT, entry.ContentType, "the listed entry must be the user's text/markdown upload")

		// The listed part_index must address the upload on the download
		// route of the copy this response describes (contract:
		// MailAttachment.PartIndex is the download route's {partIndex}).
		stable := mdhDownloadProbeFor(t, env, newRef, mdhUserMdData, "uploaded message.md")
		require.Equal(t, stable, entry.PartIndex,
			"the listed part_index %d does not address the upload (download route serves it at %d)", entry.PartIndex, stable)
	})

	t.Run("default carry-all send transmits the uploaded message.md", func(t *testing.T) {
		env, sink, uv, newUID, _ := mdhUploadFlow(t, "defaultsend")
		_ = env

		resp := sendLeakDraft(t, env, uv, uint32(newUID), nil)
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		mdhRequireUserMdSent(t, bodies[0])
		require.NotEmpty(t, resp.MessageId)
	})

	t.Run("explicit keep naming the user part's stable index transmits it", func(t *testing.T) {
		env, sink, uv, newUID, _ := mdhUploadFlow(t, "explicitkeep")
		newRef := draftRefPath(uv, uint32(newUID))
		// The keep value derives from the download route (the addressing
		// authority), never from the (today-empty) listing.
		userIdx := mdhDownloadProbeFor(t, env, newRef, mdhUserMdData, "uploaded message.md")

		resp := sendLeakDraft(t, env, uv, uint32(newUID), []int{userIdx})
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		mdhRequireUserMdSent(t, bodies[0])
		require.NotEmpty(t, resp.MessageId)
	})

	t.Run("keep list naming both the bookkeeping part and the user part transmits only the user part", func(t *testing.T) {
		env, sink, uv, newUID, _ := mdhUploadFlow(t, "keepboth")
		newRef := draftRefPath(uv, uint32(newUID))
		userIdx := mdhDownloadProbeFor(t, env, newRef, mdhUserMdData, "uploaded message.md")
		// The bookkeeping part's own stable index, located by its unique
		// payload: the marker leaf carries the draft's body markdown
		// verbatim (renderMarkdownPart stores the original Markdown), so the
		// download route's index serving exactly those bytes IS the marker.
		markerIdx := mdhDownloadProbeFor(t, env, newRef, "body after upload edit", "draft body bookkeeping part")
		require.NotEqual(t, userIdx, markerIdx,
			"instrument: the marker and user part probes must name different parts")

		// Name the MARKER first: the skip must stay silent (the bookkeeping
		// part is never a rejection - it is bookkeeping), and only the user
		// part may ride.
		resp := sendLeakDraft(t, env, uv, uint32(newUID), []int{markerIdx, userIdx})
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		mdhRequireUserMdSent(t, bodies[0])
		require.NotEmpty(t, resp.MessageId)
	})
}

func TestMailDraftSendAudit_UserMessageMd_CountListAndSentAgree(t *testing.T) {
	// (e): the draft-sent audit's attachment_count and attachments list must
	// describe exactly what was sent. The flow carries the user's
	// message.md; the audit must count ONE attachment, list it by
	// filename+size (D33), and the sent bytes must hold exactly that one
	// message.md leaf - count, list and wire all agree.
	env, sink, uv, newUID, _ := mdhUploadFlow(t, "audit")
	// The auditor is wired before the send; only the draft-sent event is
	// captured (the flow's PUT already happened).
	auditDir := mailAuditLogger(t, env)
	resp := sendLeakDraft(t, env, uv, uint32(newUID), nil)
	n, bodies := sink.acceptedBodies()
	require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
	require.Len(t, bodies, 1)
	mdhRequireUserMdSent(t, bodies[0])
	require.NotEmpty(t, resp.MessageId)

	ev := findAuditEvent(t, auditDir, "mail.panel.draft_sent")
	require.NotNil(t, ev, "the draft-sent audit event must exist; lines:\n%s", auditDebugLines(t, auditDir))
	details, _ := ev["details"].(map[string]any)
	require.NotNil(t, details, "audit event must carry details")

	rawCount, ok := details["attachment_count"]
	require.True(t, ok, "audit must carry attachment_count; details: %v", details)
	count, ok := rawCount.(float64)
	require.True(t, ok, "attachment_count must be numeric, got %T", rawCount)
	require.Equal(t, float64(1), count,
		"attachment_count must be 1 (the user's message.md; the bookkeeping part is not an attachment and was never carried)")

	// The D33 per-attachment list: exactly the user's upload, filename+size.
	requireAttachments(t, details, map[string]int{mdhUserMdName: len(mdhUserMdData)})

	// (e) invariant: the audit list length equals the count and equals the
	// message.md leaves on the wire - all three say the same thing.
	rawList, ok := details["attachments"]
	require.True(t, ok, "audit must carry the attachments list; details: %v", details)
	list, ok := rawList.([]any)
	require.True(t, ok, "attachments must be a list, got %T", rawList)
	require.Equal(t, int(count), len(list), "attachment_count must equal the attachments list length (e)")
	require.Equal(t, 1, mdhMessageMdCount(t, bodies[0]),
		"the audit count/list must equal the attachment parts actually sent (e)")
}

func TestMailReadMessageDotMd_ReadPathSurface(t *testing.T) {
	// (d) and scenario A's read half, pinned on the generic read route
	// (rest_mail_read.go::handleMailFolderMessage) — see the routing fact in
	// the file header: no GET read exists for the Drafts folder on the wire,
	// and the listing loop pinned here is shared for every folder.
	t.Run("an inbound message's real message.md attachment is listed and downloadable", func(t *testing.T) {
		// Scenario A's read half (guard): the genuine user attachment is
		// listed with its stable part_index and the download route serves it
		// there. Must stay green through the fix — a fix that starts
		// filtering by name anywhere on the read path fails here.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		appendRaw(t, cl, "INBOX", []byte(mdhInboundRaw()), nil)
		uv := mdhFolderUIDValidity(t, cl, "INBOX")
		ref := mailMessagesPath("inbox") + "/uid:" + fmt.Sprint(uv) + ":1"

		rec := mailDo(env.mux, http.MethodGet, ref, nextMailIP(), true, "")
		require.Equal(t, http.StatusOK, rec.Code, "the read route must serve the inbound message; body: "+rec.Body.String())
		var msg gen.MailMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &msg))

		require.True(t, containsMarkerListing(msg),
			"the inbound message's genuine message.md attachment must be listed (scenario A: the user sees it); listing: %s", listingString(msg))
		require.True(t, listsRealAttachment(msg),
			"positive control: the fixture's second real attachment must be listed too; listing: %s", listingString(msg))
		stable := mdhDownloadProbeFor(t, env, ref, mdhUserMdData, "inbound message.md")
		for _, at := range msg.Attachments {
			if at.Filename == mdhUserMdName {
				require.Equal(t, stable, at.PartIndex,
					"the listed message.md part_index %d must address it on the download route (serves it at %d)", at.PartIndex, stable)
			}
		}
	})

	t.Run("the read path never lists the header-marked bookkeeping part of an Omnipus draft", func(t *testing.T) {
		// (d), RED: an Omnipus draft copy (real Compose output) served
		// through the generic read route lists its bookkeeping part today —
		// rest_mail_read.go's listing loop has no filter. The oracle: the
		// read listing never shows the bookkeeping part (recognized by the
		// marker header after the fix), while the draft's REAL attachment
		// stays listed.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		out, cerr := email.Compose(email.ComposeInput{
			From:      "mailbox@test.local",
			To:        []string{"a@b.test"},
			Subject:   "read surface fixture",
			Markdown:  "read surface fixture body",
			Draft:     true,
			MessageID: "<mdh-read-draft@example.test>",
			Attachments: []email.Attachment{
				{Name: leakAttachName, ContentType: "text/plain", Data: []byte(leakAttachData)},
			},
		})
		require.NoError(t, cerr)
		appendRaw(t, cl, "INBOX", out.Transmitted, nil)
		uv := mdhFolderUIDValidity(t, cl, "INBOX")
		ref := mailMessagesPath("inbox") + "/uid:" + fmt.Sprint(uv) + ":1"

		rec := mailDo(env.mux, http.MethodGet, ref, nextMailIP(), true, "")
		require.Equal(t, http.StatusOK, rec.Code, "body: "+rec.Body.String())
		var msg gen.MailMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &msg))

		require.False(t, containsMarkerListing(msg),
			"the read path must never list the header-marked bookkeeping part (message.md / text/markdown) of an Omnipus draft; listing: %s", listingString(msg))
		require.True(t, listsRealAttachment(msg),
			"positive control: the draft's real attachment must stay listed; listing: %s", listingString(msg))
		require.Len(t, msg.Attachments, 1,
			"exactly the real attachment may be listed; listing: %s", listingString(msg))
	})
}

// mdhInboundRaw builds scenario A's inbound message: multipart/mixed with a
// plain body, a GENUINE message.md attachment (text/markdown) and a second
// real attachment (positive control). The message.md bytes are the shared
// mdhUserMdData so the download-route probe identifies them.
func mdhInboundRaw() string {
	return "From: sender@b.test\r\nTo: mailbox@test.local\r\nSubject: inbound with real message.md\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <mdh-inbound@example.test>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=mdhbnd\r\n\r\n" +
		"--mdhbnd\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" +
		"inbound body plain\r\n" +
		"--mdhbnd\r\nContent-Type: text/markdown; charset=UTF-8\r\n" +
		"Content-Disposition: attachment; filename=\"message.md\"\r\n\r\n" +
		mdhUserMdData + "\r\n" +
		"--mdhbnd\r\nContent-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Disposition: attachment; filename=\"" + leakAttachName + "\"\r\n\r\n" +
		leakAttachData + "\r\n" +
		"--mdhbnd--\r\n"
}

// mdhFolderUIDValidity reads a folder's UIDVALIDITY (generic form of
// draftUIDValidity for the non-drafts folders the generic read route serves).
func mdhFolderUIDValidity(t *testing.T, cl *imapclient.Client, folder string) uint32 {
	t.Helper()
	data, err := cl.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	require.NoError(t, err)
	return data.UIDValidity
}
