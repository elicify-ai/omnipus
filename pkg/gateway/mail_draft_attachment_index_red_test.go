package gateway

// Round-5 RED — keep_attachment_parts and the draft attachment listing use
// TWO DIFFERENT, DISAGREEING index numbering schemes (team-lead ruling on the
// round-4 open question). The contract fixes ONE scheme for both:
//
//   - pkg/api/generated/openapi_types.gen.go::MailAttachment.PartIndex —
//     "Index of the MIME part within the message — the {partIndex} path
//     parameter of the download endpoint"; the download route resolves a
//     request by searching the freshly-read view for that value
//     (pkg/gateway/rest_mail_read.go::handleMailAttachment,
//     v.Attachments[i].PartIndex == idx).
//   - both KeepAttachmentParts fields take "Part indices from the current
//     copy's MailMessage.attachments" — the same values the listing reports.
//
// The stable index is assigned during the raw MIME walk
// (pkg/email/view.go::MailPart: "the stable walk-order leaf index"); EVERY
// leaf part consumes a walk index, including body leaves (the
// multipart/alternative text/plain + text/html pair every Compose message
// carries, pkg/email/compose.go::renderAlternative) that never enter
// MailView.Attachments. So a real attachment's stable PartIndex is >0 even
// when it is the FIRST entry of the filtered attachment list, and the two
// numberings diverge as soon as any non-attachment leaf precedes an
// attachment — which is EVERY Omnipus draft (alternative leaves 0 and 1
// precede everything) and every foreign draft with a plain body.
//
// The buggy sites (all in pkg/gateway/rest_mail_draft.go):
//   - handleMailDraftUpdate's response reports PartIndex: len(resp.Attachments)
//     — a recomputed sequential counter, not the part's own field;
//   - handleMailDraftUpdate and handleMailDraftSendInner both index
//     cur.Attachments[idx] by raw slice POSITION instead of matching
//     .PartIndex == idx.
//
// The harm: a panel that renders download links from the update response's
// part_index 404s on the download route, and a client feeding a listing
// index back as keep_attachment_parts keeps the WRONG part (a user asking to
// keep "attachment 2" silently gets a different file sent) or is falsely
// rejected with 400.
//
// Oracle (never read off the buggy code): expected indexes are derived from
// the contract-designated addressing authority — the download route itself —
// by probing which {partIndex} serves each attachment's unique bytes, plus
// the walk-order arithmetic over the raw fixture this test authored.
//
// Mutations this pack must kill (CHECK's job, deferred per RED scope): switch
// either keep lookup to .PartIndex search; replace the response counter with
// the part's real PartIndex; invert the positional range guard.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// Fixture: a FOREIGN draft (no X-Omnipus-Draft header — the panel edits any
// draft in the folder, IsOmnipusDraft only toggles marker re-rendering) whose
// raw MIME shape is multipart/mixed[
//
//	leaf 0: text/plain body      (consumed as body — no MailPart)
//	leaf 1: text/html body       (consumed as body — no MailPart)
//	leaf 2: alpha-report.txt     (real attachment — stable PartIndex 2, list position 0)
//	leaf 3: beta-sheet.csv       (real attachment — stable PartIndex 3, list position 1)
//	leaf 4: gamma-notes.txt      (real attachment — stable PartIndex 4, list position 2)
//
// ] — the minimal shape in which "the listing position" and "the stable part
// index" disagree by construction.
// idxForeignDraftRaw renders the fixture with a SUBTEST-UNIQUE Message-ID:
// the gateway's panel-send replay map (rest_mail_draft.go::draftSendRecords)
// is package-level state keyed on Message-ID with a 15-minute TTL — a shared
// ID would make every send after the first a replay instead of a send.
func idxForeignDraftRaw(tag string) string {
	return "From: mailbox@test.local\r\nTo: a@b.test\r\nSubject: index fixture draft\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <idx-draft-" + tag + "@example.test>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=idxbnd\r\n\r\n" +
		"--idxbnd\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" +
		"index fixture body plain\r\n" +
		"--idxbnd\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n" +
		"<p>index fixture body html</p>\r\n" +
		"--idxbnd\r\nContent-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Disposition: attachment; filename=\"alpha-report.txt\"\r\n\r\n" +
		idxAlphaData + "\r\n" +
		"--idxbnd\r\nContent-Type: text/csv; charset=UTF-8\r\n" +
		"Content-Disposition: attachment; filename=\"beta-sheet.csv\"\r\n\r\n" +
		idxBetaData + "\r\n" +
		"--idxbnd\r\nContent-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Disposition: attachment; filename=\"gamma-notes.txt\"\r\n\r\n" +
		idxGammaData + "\r\n" +
		"--idxbnd--\r\n"
}

// idxAppendForeignDraft appends the fixture draft to the fixture Drafts
// folder under a subtest-unique tag (fresh folder per subtest, so the
// appended copy is uid 1).
func idxAppendForeignDraft(t *testing.T, cl *imapclient.Client, tag string) {
	t.Helper()
	idxAppendRaw(t, cl, "Drafts", idxForeignDraftRaw(tag))
}

// Unique 7-bit-clean payload markers: the download route serves a part's
// decoded bytes, and the SMTP sink transmits 7-bit-clean attachment payloads
// verbatim, so a substring probe identifies exactly which part rode which
// surface.
const (
	idxAlphaData = "alpha report payload q7f3a first attachment"
	idxBetaData  = "beta sheet payload w91c4 second attachment"
	idxGammaData = "gamma notes payload e55de third attachment"
)

// idxProbeMax bounds the download probe. The fixture has at most 5 leaves;
// 12 leaves headroom is generous and a miss is a loud Fatalf naming the
// instrument, never a silent pass.
const idxProbeMax = 12

// idxProbeStablePartIndex derives one attachment's stable part index from the
// contract-designated addressing authority: the download route. It walks
// {partIndex} 0..idxProbeMax and returns the index whose response carries the
// attachment's unique bytes. A probe that finds nothing fails the test
// naming the instrument — the oracle must stand on a working download route.
func idxProbeStablePartIndex(t *testing.T, env *mailRedEnv, refPath, marker, label string) int {
	t.Helper()
	for idx := 0; idx <= idxProbeMax; idx++ {
		rec := mailDo(env.mux, http.MethodGet, refPath+"/attachments/"+strconv.Itoa(idx), nextMailIP(), true, "")
		if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), marker) {
			return idx
		}
	}
	t.Fatalf("download probe: no part index 0..%d on %s served %s bytes (%q) - the download route is the oracle of record and must serve each part by its stable index",
		idxProbeMax, refPath, label, marker)
	return -1
}

// idxAppendRaw appends raw bytes to the fixture Drafts mailbox with the
// \Draft flag (thin wrapper so the subtests read uniformly).
func idxAppendRaw(t *testing.T, cl *imapclient.Client, mailbox string, raw string) {
	t.Helper()
	appendRaw(t, cl, mailbox, []byte(raw), []imap.Flag{imap.FlagDraft})
}

// idxPut updates the draft copy the (uv, uid) pair names, carrying forward
// exactly the keep list given. Returns the raw recorder; callers assert the
// status THEY specify so a desired-4xx case can assert its own contract.
func idxPut(t *testing.T, env *mailRedEnv, uv, uid uint32, keep []int, subject, body string) *httptest.ResponseRecorder {
	t.Helper()
	parts := make([]string, 0, len(keep))
	for _, idx := range keep {
		parts = append(parts, strconv.Itoa(idx))
	}
	reqBody := fmt.Sprintf(
		`{"uid":%d,"uidvalidity":%d,"to":["a@b.test"],"subject":%q,"body_markdown":%q,"keep_attachment_parts":[%s]}`,
		uid, uv, subject, body, strings.Join(parts, ","))
	return mailDo(env.mux, http.MethodPut, draftRefPath(uv, uid), nextMailIP(), true, reqBody)
}

// idxSend POSTs the panel send with an explicit keep list. Returns the raw
// recorder for caller-side status assertions.
func idxSend(t *testing.T, env *mailRedEnv, uv, uid uint32, keep []int) *httptest.ResponseRecorder {
	t.Helper()
	parts := make([]string, 0, len(keep))
	for _, idx := range keep {
		parts = append(parts, strconv.Itoa(idx))
	}
	reqBody := fmt.Sprintf(
		`{"uid":%d,"uidvalidity":%d,"to":["a@b.test"],"subject":"final send","body_markdown":"final body","keep_attachment_parts":[%s]}`,
		uid, uv, strings.Join(parts, ","))
	return mailDo(env.mux, http.MethodPost, draftRefPath(uv, uid)+"/send", nextMailIP(), true, reqBody)
}

// idxDecodeMessage decodes a 2xx PUT response body as MailMessage.
func idxDecodeMessage(t *testing.T, body string) gen.MailMessage {
	t.Helper()
	var msg gen.MailMessage
	require.NoError(t, json.Unmarshal([]byte(body), &msg), "update response must decode as MailMessage")
	return msg
}

// idxListing renders one attachment listing for failure messages.
func idxListing(msg gen.MailMessage) string {
	parts := make([]string, 0, len(msg.Attachments))
	for _, at := range msg.Attachments {
		parts = append(parts, fmt.Sprintf("filename=%q part_index=%d", at.Filename, at.PartIndex))
	}
	return strings.Join(parts, "; ")
}

func TestMailDraftAttachment_PartIndexIsTheStableMIMEPartIndex(t *testing.T) {
	t.Run("download route establishes the stable part numbering on a draft", func(t *testing.T) {
		// Oracle derivation, not an assertion about the fix: the download
		// route must serve each fixture attachment at its walk-order leaf
		// index (alpha=2, beta=3, gamma=4 — two body leaves precede them).
		// These probed values are the expected numbers every later subtest
		// holds the draft-update response and keep handling to.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		idxAppendForeignDraft(t, cl, "probe")
		uv := draftUIDValidity(t, cl)
		ref := draftRefPath(uv, 1)

		alpha := idxProbeStablePartIndex(t, env, ref, idxAlphaData, "alpha-report.txt")
		beta := idxProbeStablePartIndex(t, env, ref, idxBetaData, "beta-sheet.csv")
		gamma := idxProbeStablePartIndex(t, env, ref, idxGammaData, "gamma-notes.txt")

		require.Equal(t, 2, alpha, "alpha-report.txt is leaf 2 (after the text/plain and text/html body leaves) — its stable index must be 2, not its list position 0")
		require.Equal(t, 3, beta, "beta-sheet.csv is leaf 3 — its stable index must be 3, not its list position 1")
		require.Equal(t, 4, gamma, "gamma-notes.txt is leaf 4 — its stable index must be 4, not its list position 2")
	})

	t.Run("update response reports every carried attachment under its stable index", func(t *testing.T) {
		// Contract (MailAttachment.PartIndex): the response's part_index is
		// the {partIndex} path parameter of the download endpoint. Keep-all
		// edit; every listed attachment's reported index must equal the
		// index the download route of the NEW copy actually serves it at.
		//
		// The keep list names every attachment by the index the download
		// route of the copy BEING EDITED serves it at — probed below, never
		// hard-coded: the contract makes keep_attachment_parts and the
		// download route one numbering scheme, so the probed values are the
		// contract-correct keep values.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		idxAppendForeignDraft(t, cl, "keepall")
		uv := draftUIDValidity(t, cl)
		ref := draftRefPath(uv, 1)
		keep := []int{
			idxProbeStablePartIndex(t, env, ref, idxAlphaData, "alpha-report.txt"),
			idxProbeStablePartIndex(t, env, ref, idxBetaData, "beta-sheet.csv"),
			idxProbeStablePartIndex(t, env, ref, idxGammaData, "gamma-notes.txt"),
		}

		rec := idxPut(t, env, uv, 1, keep, "keep all edit", "kept everything body")
		require.Less(t, rec.Code, 300, "keep-all edit must succeed; body: "+rec.Body.String())
		msg := idxDecodeMessage(t, rec.Body.String())
		require.Len(t, msg.Attachments, 3, "all three attachments carried; listing: "+idxListing(msg))

		newRef := draftRefPath(uv, uint32(msg.Uid))
		for _, at := range msg.Attachments {
			var marker, label string
			switch at.Filename {
			case "alpha-report.txt":
				marker, label = idxAlphaData, "alpha-report.txt"
			case "beta-sheet.csv":
				marker, label = idxBetaData, "beta-sheet.csv"
			case "gamma-notes.txt":
				marker, label = idxGammaData, "gamma-notes.txt"
			default:
				t.Fatalf("unexpected listing entry %q; listing: %s", at.Filename, idxListing(msg))
			}
			stable := idxProbeStablePartIndex(t, env, newRef, marker, label)
			require.Equal(t, stable, at.PartIndex,
				"%s: update response reports part_index %d but the download route of the copy this response describes serves it at %d - the reported index does not address the attachment it lists (contract: part_index is the download endpoint's {partIndex}); listing: %s",
				label, at.PartIndex, stable, idxListing(msg))
		}
	})

	t.Run("update keep list written in stable indexes keeps the named part", func(t *testing.T) {
		// keep=[2] names alpha-report.txt in the stable scheme (leaf 2).
		// The handler must match by .PartIndex, not slice position: today it
		// keeps cur.Attachments[2] — gamma-notes.txt, the third attachment.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		idxAppendForeignDraft(t, cl, "putkeep")
		uv := draftUIDValidity(t, cl)
		require.Equal(t, 2, idxProbeStablePartIndex(t, env, draftRefPath(uv, 1), idxAlphaData, "alpha-report.txt"),
			"instrument: alpha's stable index is 2 before the edit")

		rec := idxPut(t, env, uv, 1, []int{2}, "keep alpha edit", "kept alpha body")
		require.Less(t, rec.Code, 300, "keep=[2] (alpha's stable index) must be accepted; body: "+rec.Body.String())
		msg := idxDecodeMessage(t, rec.Body.String())
		require.Len(t, msg.Attachments, 1, "exactly the named part carried; listing: "+idxListing(msg))
		require.Equal(t, "alpha-report.txt", msg.Attachments[0].Filename,
			"keep_attachment_parts [2] names alpha-report.txt by stable index, but the handler kept the part at slice position 2; listing: %s", idxListing(msg))
		stable := idxProbeStablePartIndex(t, env, draftRefPath(uv, uint32(msg.Uid)), idxAlphaData, "alpha-report.txt")
		require.Equal(t, stable, msg.Attachments[0].PartIndex,
			"carried alpha reports part_index %d, download route serves it at %d; listing: %s",
			msg.Attachments[0].PartIndex, stable, idxListing(msg))
	})

	t.Run("update accepts a fresh Omnipus draft keep list written in stable indexes", func(t *testing.T) {
		// Realistic shape: a real email.Compose Omnipus draft. Its
		// multipart/alternative body leaves (0 and 1) and the message.md
		// bookkeeping leaf (2) all precede the user attachment, whose stable
		// index is therefore 3 — beyond the positional range of a listing
		// that shows two entries today. The panel that addresses the
		// attachment the way the contract says (the download route's index)
		// is falsely rejected today.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		out, cerr := email.Compose(email.ComposeInput{
			From:      "mailbox@test.local",
			To:        []string{"a@b.test"},
			Subject:   "omnibus fixture draft",
			Markdown:  "omnibus agenda body",
			Draft:     true,
			MessageID: "<idx-omnibus-draft@example.test>",
			Attachments: []email.Attachment{
				{Name: "agenda.pdf", ContentType: "application/pdf", Data: []byte("agenda pdf payload m44k9 omnibus")},
			},
		})
		require.NoError(t, cerr)
		idxAppendRaw(t, cl, "Drafts", string(out.Transmitted))
		uv := draftUIDValidity(t, cl)
		stable := idxProbeStablePartIndex(t, env, draftRefPath(uv, 1), "agenda pdf payload m44k9 omnibus", "agenda.pdf")
		require.Equal(t, 3, stable, "instrument: agenda.pdf's stable index is 3 (leaves 0,1 body + leaf 2 message.md precede it)")

		rec := idxPut(t, env, uv, 1, []int{stable}, "keep agenda edit", "kept agenda body")
		require.Less(t, rec.Code, 300,
			"keep=[%d] (agenda.pdf's stable index per the download route) must be accepted on the draft; body: %s", stable, rec.Body.String())
		msg := idxDecodeMessage(t, rec.Body.String())
		require.Len(t, msg.Attachments, 1, "exactly the named part carried; listing: "+idxListing(msg))
		require.Equal(t, "agenda.pdf", msg.Attachments[0].Filename, "listing: %s", idxListing(msg))
	})

	t.Run("send carries the part named by its stable index", func(t *testing.T) {
		// keep=[2] (alpha's stable index) must transmit ALPHA. Today the
		// positional lookup keeps slice position 2 = gamma-notes.txt: the
		// user asked to keep attachment alpha and a different file is sent.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		idxAppendForeignDraft(t, cl, "sendalpha")
		uv := draftUIDValidity(t, cl)

		rec := idxSend(t, env, uv, 1, []int{2})
		require.Equal(t, http.StatusOK, rec.Code, "send must accept alpha's stable index; body: "+rec.Body.String())
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		require.Contains(t, bodies[0], idxAlphaData,
			"keep_attachment_parts [2] names alpha-report.txt by stable index, but its bytes are not in the sent message - the part at slice position 2 rode instead")
		require.NotContains(t, bodies[0], idxBetaData, "the un-named part must not ride the send")
		require.NotContains(t, bodies[0], idxGammaData,
			"gamma-notes.txt sits at slice position 2 but was NOT named; its bytes in the sent message prove the positional misread")
	})

	t.Run("send accepts a stable index beyond the positional range", func(t *testing.T) {
		// gamma's stable index is 4; the listing has 3 entries, so the
		// positional bound check (idx >= len) rejects it with 400 today. The
		// contract says 4 IS gamma's part index — the send must carry it.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		idxAppendForeignDraft(t, cl, "sendgamma")
		uv := draftUIDValidity(t, cl)
		require.Equal(t, 4, idxProbeStablePartIndex(t, env, draftRefPath(uv, 1), idxGammaData, "gamma-notes.txt"),
			"instrument: gamma's stable index is 4 before the send")

		rec := idxSend(t, env, uv, 1, []int{4})
		require.Equal(t, http.StatusOK, rec.Code,
			"keep=[4] is gamma-notes.txt's stable index per the download route and must be accepted; body: "+rec.Body.String())
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		require.Contains(t, bodies[0], idxGammaData, "the named part must ride the send")
		require.NotContains(t, bodies[0], idxAlphaData, "un-named parts must not ride the send")
		require.NotContains(t, bodies[0], idxBetaData, "un-named parts must not ride the send")
	})

	t.Run("send rejects a value that names no attachment part", func(t *testing.T) {
		// Negative case: stable index 1 belongs to the text/html BODY leaf —
		// no ATTACHMENT carries it. Search-by-PartIndex must reject it with
		// the contract's "names no such part" 400. Today the positional read
		// keeps slice position 1 (beta-sheet.csv) and SENDS MAIL with it —
		// the harm is not a wrong listing, it is an unintended send.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		idxAppendForeignDraft(t, cl, "sendhole")
		uv := draftUIDValidity(t, cl)

		rec := idxSend(t, env, uv, 1, []int{1})
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"stable index 1 names no attachment part (it is a body leaf) and must be rejected; body: "+rec.Body.String())
		n, _ := sink.acceptedBodies()
		require.Equal(t, 0, n, "a rejected send must not transmit anything")
	})

	t.Run("send still rejects a value beyond every part index", func(t *testing.T) {
		// Guard: 9 matches no part under EITHER scheme — the 400 must
		// survive the fix (kills an overcorrection to accept-everything).
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		idxAppendForeignDraft(t, cl, "sendbeyond")
		uv := draftUIDValidity(t, cl)

		rec := idxSend(t, env, uv, 1, []int{9})
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"stable index 9 names no part at all; body: "+rec.Body.String())
		n, _ := sink.acceptedBodies()
		require.Equal(t, 0, n, "a rejected send must not transmit anything")
	})
}
