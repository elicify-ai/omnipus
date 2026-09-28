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
//
// Round-5 note (contract 1aef5e376): keep_attachment_parts values are each
// attachment's STABLE part index — the same value the download route's
// {partIndex} expects and the listing's part_index reports — never a listing
// position. Every keep list in this file is derived per that contract: from
// the download route (the contract-designated addressing authority —
// leakFreshDraftKeep, via the index pack's idxProbeStablePartIndex), from
// the response listing's own part_index fields (allPartIndexes,
// realAttachmentKeepIndex), or from the appended fixture's raw MIME leaf
// walk (draftBodyMarkerStableIndex). No keep value is hard-coded.

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
// the fixture Drafts folder, exactly what a panel edit would act on. Returns
// the raw wire bytes so callers can derive part indexes from the fixture's
// own MIME shape.
//
// email.Compose with Draft: true renders a fixed walk-order LEAF sequence for
// this input: the multipart/alternative text/plain + text/html body leaves
// (0 and 1), the message.md body bookkeeping leaf (2), then the user
// attachments (3..). Under the contract (part_index = the download route's
// {partIndex}) the fixed attachment listing shows only the real attachment —
// at its stable leaf index — never the bookkeeping leaf.
func appendLeakDraft(t *testing.T, cl *imapclient.Client) string {
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
	return string(out.Transmitted)
}

// editLeakDraft issues one panel edit (PUT) on the draft copy the ref names,
// carrying forward exactly the keep list given (stable part indexes — the
// values the contract makes identical between keep_attachment_parts and the
// download route's {partIndex}), with a fresh subject and body. The update
// path has NO default carry-all: a nil list omits the field and keeps
// NOTHING, so every edit names its parts explicitly. Returns the decoded
// MailMessage response: the new copy's uid and the attachment listing the
// panel sees after the edit.
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
// a non-nil list (stable part indexes, same scheme as editLeakDraft) is sent
// verbatim. Returns the decoded MailSendResponse and
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

// allPartIndexes returns every entry's own part_index from one attachment
// listing — the keep-everything list under the round-5 contract: keep
// values ARE the listing's part_index fields (the download route's
// {partIndex}), never listing positions.
func allPartIndexes(msg gen.MailMessage) []int {
	out := make([]int, 0, len(msg.Attachments))
	for _, at := range msg.Attachments {
		out = append(out, at.PartIndex)
	}
	return out
}

// leakFreshDraftKeep derives the keep list for the FIRST edit of a fresh
// draft copy — the panel's keep-everything input when no listing exists yet
// (the PUT update response is the only listing surface, and it describes the
// copy AFTER the edit). The contract's addressing authority is the download
// route, so the fresh copy's real attachment is located there by its unique
// bytes — reusing the index pack's probe (idxProbeStablePartIndex,
// mail_draft_attachment_index_red_test.go) — and its stable part index
// becomes the keep value. Note the update path has NO default carry-all:
// an omitted keep_attachment_parts keeps NOTHING, so the first edit must
// name the part explicitly.
func leakFreshDraftKeep(t *testing.T, env *mailRedEnv, uv uint32) []int {
	t.Helper()
	return []int{idxProbeStablePartIndex(t, env, draftRefPath(uv, 1), leakAttachData, leakAttachName)}
}

// realAttachmentKeepIndex derives the real user attachment's keep value from
// a listing: the part_index the listing reports for the fixture's notes.txt
// entry, matched by NAME — never a position. A listing without the real
// attachment is an instrument failure (the derivations below would be
// meaningless) and Fatals; it can never silently pass.
func realAttachmentKeepIndex(t *testing.T, msg gen.MailMessage) int {
	t.Helper()
	for _, at := range msg.Attachments {
		if at.Filename == leakAttachName {
			return at.PartIndex
		}
	}
	t.Fatalf("instrument: the listing carries no %q entry; the keep-value derivation has nothing to derive from - listing: %s",
		leakAttachName, listingString(msg))
	return -1
}

// draftBodyMarkerStableIndex derives the draft-body marker's stable part
// index from the appended fixture's own raw MIME — its position in the
// walk-order leaf sequence — never from any implementation surface. The
// fixed listing no longer shows the marker, so no response can supply this
// value; the leaf walk of the bytes this test authored is the independent
// source. The walk-order scheme this relies on is the same one the index
// pack cross-validates against the download route
// (mail_draft_attachment_index_red_test.go: the Compose draft whose
// attachment probes at stable index 3, after leaves 0,1 body and leaf 2
// message.md), and every edit of a Compose draft re-renders the same leaf
// order (alternative leaves 0/1, marker leaf 2, carried attachments 3..), so
// the value derived from the appended copy names the marker in every later
// copy too. A fixture without the marker leaf is an instrument failure and
// Fatals — it can never silently return a wrong index.
func draftBodyMarkerStableIndex(t *testing.T, rawDraft string) int {
	t.Helper()
	r, err := gomail.CreateReader(strings.NewReader(rawDraft))
	require.NoError(t, err, "instrument: the appended fixture must parse as MIME")
	leaf := -1
	for {
		p, perr := r.NextPart()
		if perr != nil || p == nil {
			break
		}
		leaf++
		ct, ctParams, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		_, dispParams, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		name := dispParams["filename"]
		if name == "" {
			name = ctParams["name"]
		}
		if ct == "text/markdown" && name == "message.md" {
			return leaf
		}
	}
	t.Fatalf("instrument: the fixture draft carries no message.md/text-markdown leaf; the marker's stable index cannot be derived from the raw MIME")
	return -1
}

func TestMailDraftPanelSend_NeverLeaksDraftBodyPart(t *testing.T) {
	// Oracle: the draft's own message.md bookkeeping part is Omnipus's
	// bookkeeping, never a user attachment (dispatch item 1; the audit path
	// already excludes it via mailAuditDraftBodyPart). Every subtest edits at
	// least twice (the accumulation shape) before sending, keeping everything
	// the contract lets a panel address: the first edit keeps every
	// attachment of the fresh copy (located on the download route — the
	// contract's addressing authority — because no listing exists before it:
	// the PUT update response is the only listing surface and it describes
	// the copy AFTER the edit), later edits keep everything the listing
	// shows by each entry's own part_index field.
	t.Run("keep-all edits then default-carry-all send transmit zero marker parts", func(t *testing.T) {
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		appendLeakDraft(t, cl)
		uv := draftUIDValidity(t, cl)

		m1 := editLeakDraft(t, env, uv, 1, leakFreshDraftKeep(t, env, uv), "edit one", leakEdit1Body)
		m2 := editLeakDraft(t, env, uv, uint32(m1.Uid), allPartIndexes(m1), "edit two", leakEdit2Body)

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
		rawDraft := appendLeakDraft(t, cl)
		uv := draftUIDValidity(t, cl)

		m1 := editLeakDraft(t, env, uv, 1, leakFreshDraftKeep(t, env, uv), "edit one", leakEdit1Body)
		m2 := editLeakDraft(t, env, uv, uint32(m1.Uid), allPartIndexes(m1), "edit two", leakEdit2Body)

		// The keep list names EVERY part the panel can address: everything
		// the listing shows (each entry's own part_index — the contract's
		// keep values) PLUS the marker's own stable index, derived from the
		// appended fixture's raw MIME leaf walk (the listing never shows the
		// marker, so no response can supply its index — see
		// draftBodyMarkerStableIndex). The oracle: even a keep list that
		// NAMES the draft-body marker's own stable index must be ACCEPTED
		// and still transmit zero marker parts (the marker is skipped
		// silently, never a rejection — it is Omnipus bookkeeping, not a
		// user attachment).
		markerIdx := draftBodyMarkerStableIndex(t, rawDraft)
		keep := append(allPartIndexes(m2), markerIdx)
		require.NotContains(t, allPartIndexes(m2), markerIdx,
			"instrument: the derived marker index collides with a listed attachment's index, so the keep list would not actually name the marker")
		resp := sendLeakDraft(t, env, uv, uint32(m2.Uid), keep)
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		requireZeroMarkerParts(t, bodies[0])
		require.Contains(t, bodies[0], leakAttachData,
			"the real user attachment must still ride the keep-all-by-index send")
		_ = resp
	})

	t.Run("explicit keep list naming only the real attachment keeps it and only it", func(t *testing.T) {
		// The keep value is the real attachment's own part_index from the
		// listing (matched by NAME, never a position — see
		// realAttachmentKeepIndex). Honoring the explicit list must carry
		// exactly that part — and never a marker part (kills an
		// over-stripping fix).
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		appendLeakDraft(t, cl)
		uv := draftUIDValidity(t, cl)

		m1 := editLeakDraft(t, env, uv, 1, leakFreshDraftKeep(t, env, uv), "edit one", leakEdit1Body)
		m2 := editLeakDraft(t, env, uv, uint32(m1.Uid), allPartIndexes(m1), "edit two", leakEdit2Body)
		keepIdx := realAttachmentKeepIndex(t, m2)

		resp := sendLeakDraft(t, env, uv, uint32(m2.Uid), []int{keepIdx})
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

	m1 := editLeakDraft(t, env, uv, 1, leakFreshDraftKeep(t, env, uv), "edit one", leakEdit1Body)
	m2 := editLeakDraft(t, env, uv, uint32(m1.Uid), allPartIndexes(m1), "edit two", leakEdit2Body)

	for name, m := range map[string]gen.MailMessage{"edit one response": m1, "edit two response": m2} {
		require.False(t, containsMarkerListing(m),
			"%s: the draft's own body bookkeeping part (message.md / text/markdown) must never be listed as an attachment - listing: %s",
			name, listingString(m))
		require.True(t, listsRealAttachment(m),
			"%s: the real user attachment must still be listed; listing: %s", name, listingString(m))
		require.NotEmpty(t, m.Attachments, "%s: the draft carries one real attachment; the listing may not be empty", name)
	}
}

// leakFetchCopyRaw fetches one draft copy's full raw MIME bytes from the
// fixture (BODY.PEEK[] — MC-25: fetching never sets \Seen) so the oracle can
// count the marker parts the NEW copy actually carries. No response surface
// can supply this: the listing never shows the marker.
func leakFetchCopyRaw(t *testing.T, cl *imapclient.Client, uid uint32) string {
	t.Helper()
	opts := &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}}
	bufs, err := cl.Fetch(imap.UIDSetNum(imap.UID(uid)), opts).Collect()
	require.NoError(t, err, "fixture fetch of the edited draft copy")
	require.Len(t, bufs, 1, "fixture fetch: edited copy uid %d not found in Drafts", uid)
	var raw []byte
	for _, bs := range bufs[0].BodySection {
		raw = bs.Bytes
	}
	require.NotEmpty(t, raw, "fixture fetch: no BODY[] bytes for the edited copy")
	return string(raw)
}

func TestMailDraftPanelUpdate_KeepNamingMarkerPartIsAcceptedAndSkipped(t *testing.T) {
	// Round 7 — CHECK round-5/6 finding 2: the PUT update path's
	// keep_attachment_parts must ACCEPT a list that names the draft-body
	// marker part's OWN stable index (derived independently from the appended
	// fixture's raw MIME leaf walk — draftBodyMarkerStableIndex, never from
	// any response surface) and SKIP the marker: the new copy carries exactly
	// ONE marker part (its own, freshly rendered), never two, and the edit is
	// never a 400 — "keep_attachment_parts names no such part" would mean the
	// carry path forgot the marker is Omnipus bookkeeping, not a user
	// attachment.
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	rawDraft := appendLeakDraft(t, cl)
	uv := draftUIDValidity(t, cl)

	realIdx := leakFreshDraftKeep(t, env, uv)            // download-route probe (contract addressing authority)
	markerIdx := draftBodyMarkerStableIndex(t, rawDraft) // raw-MIME leaf walk (independent)
	require.NotContains(t, realIdx, markerIdx,
		"instrument: the derived marker index collides with the real attachment's index — the keep list would not actually name the marker")

	m := editLeakDraft(t, env, uv, 1, append(realIdx, markerIdx), "marker named edit", leakEdit1Body)

	require.False(t, containsMarkerListing(m),
		"the carried listing must never show the marker; listing: %s", listingString(m))
	require.True(t, listsRealAttachment(m),
		"the real user attachment must still ride the keep list; listing: %s", listingString(m))
	require.NotContains(t, allPartIndexes(m), markerIdx,
		"the new copy's listing must not expose the marker as an attachment; listing: %s", listingString(m))

	newRaw := leakFetchCopyRaw(t, cl, uint32(m.Uid))
	markers := draftBodyMarkerParts(newRaw)
	require.Len(t, markers, 1,
		"the new copy must carry exactly ONE draft-body marker part (its own) — a keep list naming the marker's own stable index must be skipped, never carried:\n%s\nfull new copy:\n%s",
		strings.Join(markers, "\n"), newRaw)
}
