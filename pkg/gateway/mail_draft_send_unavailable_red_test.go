package gateway

// Round-6 RED — F2, the default carry-all path's silent empty attachment
// (security-lead round-5 side note, pre-existing; squad-lead ruling in the
// qa-lead dispatch). handleMailDraftSendInner with keep_attachment_parts
// OMITTED (the contract's default carry-ALL) carries a listed-but-unavailable
// part (pkg/email/view.go::MailPart.DataUnavailable — over the 25 MiB
// per-part fetch cap, maxViewPartBytes, or failed to decode) as an attachment
// with NIL bytes: the recipient receives an EMPTY attachment. The explicit
// keep path refuses the same part with a 400 and the static message
// "keep_attachment_parts names an unavailable part".
//
// Required behavior (oracle — the squad-lead ruling, never the current code):
// the default path refuses the same way — 400, a static message in the same
// "unavailable" family as the explicit path, PRE-DIAL (no SMTP connection is
// opened), nothing transmitted — never an empty attachment.
//
// Fixtures exercise BOTH DataUnavailable routes from viewFromRaw:
//   - a part one byte over maxViewPartBytes (25 MiB + 1): listed, bytes not
//     fetched (boundary pair: exactly 25 MiB stays AVAILABLE — max — and
//     must ride; 25 MiB + 1 is unavailable — max+1 — and must refuse);
//   - a part whose Content-Transfer-Encoding: base64 body is illegal, whose
//     read fails ("failed to decode"), paired with an AVAILABLE sibling: a
//     fix that "strips the bad part and continues" fails here — the send
//     must refuse the whole draft, not silently drop the bad part.
//
// Mutations this pack must kill (CHECK's job, deferred per RED scope):
// remove the unavailable check from the default branch; replace the refusal
// with a skip-and-continue; refuse AFTER the SMTP dial; echo part data in
// the error; move the explicit-keep refusal off the same family.

import (
	"fmt"
	"mime"
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	gomail "github.com/emersion/go-message/mail"
	"github.com/stretchr/testify/require"
)

// The per-part fetch cap the view parser enforces (view.go::maxViewPartBytes).
const unvPartCap = 25 << 20

// unvCapToken is the unique payload marker embedded in the over-cap part's
// body: a substring probe identifies which parts rode the send (the SMTP
// sink transmits 7-bit-clean payloads verbatim).
const unvCapToken = "unv-cap-probe-t4k9z payload first line marker"

// unvSmallData is the AVAILABLE sibling attachment's unique bytes.
const unvSmallData = "small available sibling payload r8w2m"

const (
	unvOverName  = "overcap-part.bin"
	unvSmallName = "small-part.txt"
)

// unvFillerPayload returns a CRLF-line-wrapped payload whose total byte
// length is EXACTLY want bytes (instrument-checked below): the unique token
// as the first line's head, padded so (want % 1000) holds, then full
// 998-char filler lines. Lines stay far under the sink's scanner bound and
// carry no leading dots, so the payload rides SMTP verbatim.
func unvFillerPayload(t *testing.T, want int) string {
	t.Helper()
	pad := ((want % 1000) - len(unvCapToken) + 1000) % 1000
	first := unvCapToken + strings.Repeat("a", pad)
	var b strings.Builder
	b.WriteString(first)
	written := len(first)
	for want-written >= 1000 {
		b.WriteString("\r\n")
		b.WriteString(strings.Repeat("a", 998))
		written += 1000
	}
	require.Equal(t, want, written, "instrument: the payload builder must land exactly on the wanted size")
	require.Equal(t, want, len(b.String()), "instrument: payload length")
	return b.String()
}

// unvRawAtt is one attachment leaf of the fixture draft.
type unvRawAtt struct {
	Name string
	// Body is the raw (wire) payload; Base64Body marks it
	// Content-Transfer-Encoding: base64 (used with an ILLEGAL base64 body to
	// force the view parser's "failed to decode" route).
	Body       string
	Base64Body bool
}

// unvForeignDraftRaw builds a foreign (owner-authored) draft — multipart/mixed
// with a plain body leaf and the given attachment leaves — under a
// subtest-unique Message-ID (the panel-send replay map is package-level state
// keyed on Message-ID).
func unvForeignDraftRaw(tag string, atts []unvRawAtt) string {
	var b strings.Builder
	b.WriteString("From: mailbox@test.local\r\nTo: a@b.test\r\nSubject: unavailable fixture draft\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <unv-" + tag + "@example.test>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=unvbnd\r\n\r\n" +
		"--unvbnd\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" +
		"unavailable fixture body plain\r\n")
	for _, at := range atts {
		b.WriteString("--unvbnd\r\nContent-Type: text/plain; charset=UTF-8\r\n")
		if at.Base64Body {
			b.WriteString("Content-Transfer-Encoding: base64\r\n")
		}
		b.WriteString("Content-Disposition: attachment; filename=\"" + at.Name + "\"\r\n\r\n")
		b.WriteString(at.Body)
		b.WriteString("\r\n")
	}
	b.WriteString("--unvbnd--\r\n")
	return b.String()
}

// unvAttLeafIndex derives one attachment leaf's stable part index from the
// raw fixture this test authored (walk-order leaf index; the body plain leaf
// is leaf 0) — the same derivation scheme the index pack cross-validates
// against the download route. A fixture without the named leaf is an
// instrument failure and Fatals.
func unvAttLeafIndex(t *testing.T, raw, filename string) int {
	t.Helper()
	r, err := gomail.CreateReader(strings.NewReader(raw))
	require.NoError(t, err, "instrument: fixture must parse as MIME")
	leaf := -1
	for {
		p, perr := r.NextPart()
		if perr != nil || p == nil {
			break
		}
		leaf++
		_, ctParams, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		_, dispParams, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		name := dispParams["filename"]
		if name == "" {
			name = ctParams["name"]
		}
		if name == filename {
			return leaf
		}
	}
	t.Fatalf("instrument: fixture carries no attachment leaf named %q; the stable index cannot be derived from the raw MIME", filename)
	return -1
}

func TestMailDraftSend_DefaultCarryAll_RefusesUnavailablePart(t *testing.T) {
	t.Run("an available part at exactly the per-part fetch cap rides the default carry-all send", func(t *testing.T) {
		// Boundary max (positive control): exactly 25 MiB is AVAILABLE
		// (viewFromRaw reads at most maxViewPartBytes; equal is not over) and
		// the default carry-all send must carry it. Proves the refusal keys
		// on DataUnavailable, not on part size per se.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		payload := unvFillerPayload(t, unvPartCap)
		raw := unvForeignDraftRaw("capmax", []unvRawAtt{
			{Name: unvOverName, Body: payload},
		})
		appendRaw(t, cl, "Drafts", []byte(raw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)

		sendLeakDraft(t, env, uv, 1, nil)
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		require.Contains(t, bodies[0], unvCapToken,
			"the available at-cap part must ride the default carry-all send verbatim")
	})

	t.Run("the default carry-all send refuses a part one byte over the fetch cap", func(t *testing.T) {
		// Boundary max+1 (RED): 25 MiB + 1 is listed but DataUnavailable.
		// Required: 400, static "unavailable"-family message, pre-dial, and
		// NOTHING transmitted. Today the part rides as an EMPTY attachment.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, smtpAccepts := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		payload := unvFillerPayload(t, unvPartCap+1)
		raw := unvForeignDraftRaw("capover", []unvRawAtt{
			{Name: unvOverName, Body: payload},
			{Name: unvSmallName, Body: unvSmallData},
		})
		appendRaw(t, cl, "Drafts", []byte(raw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)

		reqBody := fmt.Sprintf(`{"uid":1,"uidvalidity":%d,"to":["a@b.test"],"subject":"final send","body_markdown":"final body"}`, uv)
		rec := mailDo(env.mux, http.MethodPost, draftRefPath(uv, 1)+"/send", nextMailIP(), true, reqBody)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"the default carry-all path must refuse a DataUnavailable part exactly like the explicit keep path (400, pre-dial, nothing sent) - an empty attachment must never be transmitted; body: %s", rec.Body.String())
		er := decodeMailErr(t, rec)
		require.Contains(t, er.Error, "unavailable",
			"the refusal must stay in the explicit path's static message family (\"… unavailable …\"); body: "+rec.Body.String())
		require.NotContains(t, er.Error, unvSmallData,
			"the static refusal must not echo part data")
		require.Equal(t, int32(0), smtpAccepts.Load(),
			"the refusal must happen PRE-DIAL: no SMTP connection may be opened")
	})

	t.Run("the default carry-all send refuses when any carried part failed to decode", func(t *testing.T) {
		// RED, strip-and-continue discriminator: a part whose bytes FAILED TO
		// DECODE (illegal base64 body) is DataUnavailable too. Paired with an
		// AVAILABLE sibling: the whole send must refuse (400, pre-dial,
		// nothing sent) — a fix that silently drops the bad part and sends
		// the good one fails here, and today's empty-attachment send fails
		// on the status.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, smtpAccepts := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		raw := unvForeignDraftRaw("corrupt", []unvRawAtt{
			{Name: "corrupt-part.bin", Body: "@@@@ not base64 @@@@", Base64Body: true},
			{Name: unvSmallName, Body: unvSmallData},
		})
		appendRaw(t, cl, "Drafts", []byte(raw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)

		reqBody := fmt.Sprintf(`{"uid":1,"uidvalidity":%d,"to":["a@b.test"],"subject":"final send","body_markdown":"final body"}`, uv)
		rec := mailDo(env.mux, http.MethodPost, draftRefPath(uv, 1)+"/send", nextMailIP(), true, reqBody)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"the default carry-all path must refuse the draft (400): one carried part failed to decode; body: %s", rec.Body.String())
		er := decodeMailErr(t, rec)
		require.Contains(t, er.Error, "unavailable",
			"the refusal must stay in the static \"unavailable\" family; body: "+rec.Body.String())
		require.Equal(t, int32(0), smtpAccepts.Load(),
			"nothing may be transmitted and no SMTP connection may be opened")
	})

	t.Run("the explicit keep path still refuses the unavailable part in the same message family", func(t *testing.T) {
		// Parity guard (green today, must stay): the explicit keep path
		// refuses the over-cap part with 400 and a static "unavailable"
		// message — the family the default path must join. Nothing sent.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, smtpAccepts := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		payload := unvFillerPayload(t, unvPartCap+1)
		raw := unvForeignDraftRaw("explkeep", []unvRawAtt{
			{Name: unvOverName, Body: payload},
			{Name: unvSmallName, Body: unvSmallData},
		})
		appendRaw(t, cl, "Drafts", []byte(raw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)
		overIdx := unvAttLeafIndex(t, raw, unvOverName)

		rec := idxSend(t, env, uv, 1, []int{overIdx})
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"the explicit keep path must keep refusing the unavailable part; body: "+rec.Body.String())
		er := decodeMailErr(t, rec)
		require.Contains(t, er.Error, "unavailable",
			"the explicit refusal's static family; body: "+rec.Body.String())
		require.Equal(t, int32(0), smtpAccepts.Load(), "nothing may be transmitted")
	})

	t.Run("the explicit keep of an available sibling still sends", func(t *testing.T) {
		// Positive control (green today, must stay): explicitly keeping only
		// the AVAILABLE sibling must still send - the draft is not poisoned
		// by carrying an unavailable part it does not name.
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		payload := unvFillerPayload(t, unvPartCap+1)
		raw := unvForeignDraftRaw("keepsibling", []unvRawAtt{
			{Name: unvOverName, Body: payload},
			{Name: unvSmallName, Body: unvSmallData},
		})
		appendRaw(t, cl, "Drafts", []byte(raw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)
		smallIdx := unvAttLeafIndex(t, raw, unvSmallName)

		sendLeakDraft(t, env, uv, 1, []int{smallIdx})
		n, bodies := sink.acceptedBodies()
		require.Equal(t, 1, n, "exactly one DATA body may be transmitted")
		require.Len(t, bodies, 1)
		require.Contains(t, bodies[0], unvSmallData, "the named available sibling must ride")
		require.NotContains(t, bodies[0], unvCapToken, "the un-named over-cap part must not ride")
	})
}
