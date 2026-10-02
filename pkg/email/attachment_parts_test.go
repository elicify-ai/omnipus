package email

// RED pack — the targeted single-part reader and the MIME attachment
// classifier (w4 spec §9.1 row 1's reader oracle; §2.1; DS-ATT-PARTS; §5.2;
// §9.3 M1/M10; US-3.AC-1/AC-4). The spec assigns this oracle's deep half to
// w2 (register R-4); no w2 pack exists, so it lives here next to the
// in-memory IMAP harness (startMemIMAP). The transfer service's seam tests
// stay in pkg/mailattachment/service_test.go.
//
// Expected values derive from the spec: the 25 MiB DECODED cap is the
// authority, never reported metadata (§5.2); only the selected part's bytes
// move (M10); structure listing fetches no bodies and writes no flags
// (US-3.AC-1); the reported size is the ENCODED transfer size, never
// substituted for a decoded count (§2.3); a stale reference is refused
// before any part data moves (grill I-03).

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// ---- MIME fixtures ----

const redBoundary = "w4red-boundary-42"

func redPart(contentType, disposition, extraHeaders, transferEncoding, body string) string {
	var b strings.Builder
	b.WriteString("--" + redBoundary + "\r\n")
	b.WriteString("Content-Type: " + contentType + "\r\n")
	if disposition != "" {
		b.WriteString("Content-Disposition: " + disposition + "\r\n")
	}
	if extraHeaders != "" {
		b.WriteString(extraHeaders)
	}
	if transferEncoding != "" {
		b.WriteString("Content-Transfer-Encoding: " + transferEncoding + "\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return b.String()
}

func redMultipart(subject string, parts ...string) []byte {
	var b strings.Builder
	b.WriteString("From: Sender <sender@example.test>\r\n")
	b.WriteString("To: mailbox@test.local\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("Date: Mon, 02 Mar 2026 10:00:00 +0000\r\n")
	b.WriteString("Message-ID: <" + subject + "@test.local>\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + redBoundary + "\"\r\n")
	b.WriteString("\r\n")
	b.WriteString("Lead-in body.\r\n")
	for _, p := range parts {
		b.WriteString(p)
	}
	b.WriteString("--" + redBoundary + "--\r\n")
	return []byte(b.String())
}

// ---- byte-counting dial seam (M10's instrument) ----

type redCountingConn struct {
	net.Conn
	n *int64
}

func (c redCountingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	*c.n += int64(n)
	return n, err
}

// redDialCounting replaces the imapDial seam with one that counts every byte
// the server sends on that connection.
func redDialCounting(t *testing.T, counter *int64) {
	t.Helper()
	prev := imapDial
	imapDial = func(ctx context.Context, addr string, _ *tls.Config) (*imapclient.Client, error) {
		d, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		return imapclient.New(&redCountingConn{Conn: d, n: counter}, nil), nil
	}
	t.Cleanup(func() { imapDial = prev })
}

// redInboxRef mints a valid reference from REAL protocol output — refs come
// from read output, never synthesized (US-3.AC-4's own discipline).
func redInboxRef(t *testing.T, cl *Client) string {
	t.Helper()
	rows, uv, _, err := cl.ReadFolderPage(context.Background(), FolderInbox, 10, 0)
	if err != nil {
		t.Fatalf("ReadFolderPage: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("inbox is empty — fixture did not append")
	}
	return fmt.Sprintf("uid:%d:%d", uv, rows[0].UID)
}

// ---- the classifier ----

func TestListAttachmentPartsClassifier(t *testing.T) {
	msg := redMultipart("classifier-fixture",
		redPart("text/plain; charset=utf-8", "", "", "", "Hello body."),
		redPart("application/pdf", `attachment; filename="q4 report.pdf"`, "", "base64", base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n"))),
		redPart("image/png", "inline", "Content-ID: <img1@inline.test>\r\n", "base64", base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G'})),
		redPart("application/octet-stream", "attachment", "", "base64", base64.StdEncoding.EncodeToString([]byte("noname"))),
	)
	cl := startMemIMAP(t, [][]byte{msg}, nil)
	ref := redInboxRef(t, cl)

	descs, err := cl.ListAttachmentParts(context.Background(), FolderInbox, ref)
	if err != nil {
		t.Fatalf("ListAttachmentParts: %v", err)
	}
	if len(descs) != 4 {
		t.Fatalf("classifier found %d leaves, want 4 (text leaf + named attachment + inline CID + noname attachment): %+v", len(descs), descs)
	}
	if !descs[3].IsAttachment {
		t.Fatalf("attachment disposition with no filename not classified as attachment: %+v (classifier rule: disposition attachment OR declared filename)", descs[3])
	}
	if descs[0].IsAttachment {
		t.Fatalf("the lead-in text leaf classified as an attachment: %+v (DS-ATT-PARTS: a genuine user body is not an attachment)", descs[0])
	}
	att := descs[1]
	if !att.IsAttachment {
		t.Fatalf("attachment leaf not classified as attachment: %+v (US-3.AC-1)", att)
	}
	if att.PartIndex != 1 {
		t.Fatalf("attachment leaf index = %d, want 1 (stable leaf order)", att.PartIndex)
	}
	if !strings.Contains(att.Filename, "q4") || !strings.Contains(att.Filename, "report") || !strings.Contains(att.Filename, ".pdf") {
		t.Fatalf("attachment filename = %q, want the (sanitized) declared name", att.Filename)
	}
	if att.ContentType != "application/pdf" {
		t.Fatalf("content type = %q, want application/pdf", att.ContentType)
	}
	if att.Disposition != "attachment" {
		t.Fatalf("disposition = %q, want attachment", att.Disposition)
	}
	if att.ReportedSizeBytes <= 0 {
		t.Fatalf("reported size = %d, want the server's positive transfer size for the part", att.ReportedSizeBytes)
	}
	inline := descs[2]
	if inline.IsAttachment {
		t.Fatalf("inline CID image with no filename classified as an attachment: %+v (classifier rule: disposition attachment OR declared filename)", inline)
	}
	if inline.ContentID != "img1@inline.test" {
		t.Fatalf("inline ContentID = %q, want img1@inline.test (angles stripped)", inline.ContentID)
	}

	again, err := cl.ListAttachmentParts(context.Background(), FolderInbox, ref)
	if err != nil {
		t.Fatalf("second ListAttachmentParts: %v", err)
	}
	if fmt.Sprint(again) != fmt.Sprint(descs) {
		t.Fatalf("descriptors not stable across listings:\nfirst:  %+v\nsecond: %+v", descs, again)
	}
}

func TestListAttachmentPartsPlainMessageHasNoAttachments(t *testing.T) {
	cl := startMemIMAP(t, [][]byte{mkMsg("plain", "a@b.test", "just text")}, nil)
	ref := redInboxRef(t, cl)
	descs, err := cl.ListAttachmentParts(context.Background(), FolderInbox, ref)
	if err != nil {
		t.Fatalf("ListAttachmentParts: %v", err)
	}
	for _, d := range descs {
		if d.IsAttachment {
			t.Fatalf("plain message leaf classified as attachment: %+v (US-3.AC-1: a message with no attachments lists none)", d)
		}
	}
}

func TestListAttachmentPartsDraftMarkerFlagged(t *testing.T) {
	msg := redMultipart("draft-marker-fixture",
		redPart("text/plain; charset=utf-8", "", "", "", "Hello."),
		redPart("text/markdown", `attachment; filename="message.md"`, "X-Omnipus-Part: draft-body\r\n", "", "draft bookkeeping"),
	)
	cl := startMemIMAP(t, [][]byte{msg}, nil)
	ref := redInboxRef(t, cl)
	descs, err := cl.ListAttachmentParts(context.Background(), FolderInbox, ref)
	if err != nil {
		t.Fatalf("ListAttachmentParts: %v", err)
	}
	found := false
	for _, d := range descs {
		if d.OmnipusDraftBody {
			found = true
		} else if d.ContentType == "text/markdown" && d.IsAttachment {
			t.Fatalf("markdown attachment with the draft-marker header NOT flagged: %+v (the X-Omnipus-Part header — never the name/type pair — decides)", d)
		}
	}
	if !found {
		t.Fatalf("no descriptor carries OmnipusDraftBody; consumers could not exclude the draft bookkeeping part: %+v", descs)
	}
}

// ---- the reported size is the ENCODED transfer size (§2.3 honesty) ----

func TestReportedSizeIsEncodedTransferSize(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("Hello"))
	msg := redMultipart("size-honesty",
		redPart("text/plain", `attachment; filename="hello.txt"`, "", "base64", encoded),
	)
	cl := startMemIMAP(t, [][]byte{msg}, nil)
	ref := redInboxRef(t, cl)
	descs, err := cl.ListAttachmentParts(context.Background(), FolderInbox, ref)
	if err != nil {
		t.Fatalf("ListAttachmentParts: %v", err)
	}
	var att *AttachmentPartDescriptor
	for i := range descs {
		if descs[i].IsAttachment {
			att = &descs[i]
		}
	}
	if att == nil {
		t.Fatalf("no attachment in fixture: %+v", descs)
	}
	if att.ReportedSizeBytes != int64(len(encoded)) {
		t.Fatalf("ReportedSizeBytes = %d, want %d (the ENCODED transfer size — §2.3 transfer-size honesty; the decoded count is 5 and must not be claimed)", att.ReportedSizeBytes, len(encoded))
	}
	part, err := cl.ReadAttachmentPart(context.Background(), FolderInbox, ref, att.PartIndex)
	if err != nil {
		t.Fatalf("ReadAttachmentPart: %v", err)
	}
	if string(part.Data) != "Hello" {
		t.Fatalf("decoded part = %q, want Hello", part.Data)
	}
}

// ---- unreported size: nothing positive is claimed ----

func TestUnreportedSizeClaimsNothing(t *testing.T) {
	zero := &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain", Encoding: "7bit", Size: 0}
	d := singlePartDescriptor(zero, 0)
	if d.ReportedSizeBytes >= 0 {
		t.Fatalf("unreported size produced a positive claim %d (the descriptor must claim nothing when the server reported none)", d.ReportedSizeBytes)
	}
	hundred := &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain", Encoding: "7bit", Size: 100}
	if got := singlePartDescriptor(hundred, 0).ReportedSizeBytes; got != 100 {
		t.Fatalf("reported size 100 produced %d", got)
	}
}

// ---- M10: only the selected part's bytes move ----

func TestReadAttachmentPartFetchesOnlySelectedPart(t *testing.T) {
	body1 := strings.Repeat("A", 30000)
	body2 := strings.Repeat("B", 30000)
	msg := redMultipart("targeted-fetch",
		redPart("text/plain", `attachment; filename="a1.txt"`, "", "", body1),
		redPart("text/plain", `attachment; filename="a2.txt"`, "", "", body2),
	)
	wholeLen := int64(len(msg))
	cl := startMemIMAP(t, [][]byte{msg}, nil)

	var read int64
	redDialCounting(t, &read)
	ref := redInboxRef(t, cl)
	part, err := cl.ReadAttachmentPart(context.Background(), FolderInbox, ref, 1)
	if err != nil {
		t.Fatalf("ReadAttachmentPart: %v", err)
	}
	if string(part.Data) != body2 {
		t.Fatalf("part 1 content = %d bytes starting %q, want the a2 body", len(part.Data), part.Data[:8])
	}
	// The second attachment's bytes must never have moved: everything the
	// server sent is bounded by the whole message minus a2's body (plus
	// protocol overhead).
	bound := wholeLen - int64(len(body2)) + 4096
	if read > bound {
		t.Fatalf("server sent %d bytes for a single-part fetch; bound is %d — the whole message (or the wrong part) moved (M10: byte counters, only the selected part)", read, bound)
	}
}

// ---- §5.2/M1: the decoded cap is the authority; the download role has none ----

// wrappedB64 emits canonical 76-column base64 (decoders skip the line
// breaks; a 26 MB single line would overflow line-buffered decoders).
func wrappedB64(b []byte) string {
	enc := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		out.WriteString(enc[i:end])
		out.WriteString("\r\n")
	}
	return out.String()
}

func TestCapAuthorityOnDecodedBytes(t *testing.T) {
	exact := bytes.Repeat([]byte("A"), 25<<20)     // 25 MiB exactly: within the cap
	over := bytes.Repeat([]byte("A"), (25<<20)+1) // 25 MiB + 1: refused
	msgExact := redMultipart("cap-exact",
		redPart("text/plain", `attachment; filename="exact.txt"`, "", "base64", wrappedB64(exact)),
	)
	msgOver := redMultipart("cap-over",
		redPart("text/plain", `attachment; filename="over.txt"`, "", "base64", wrappedB64(over)),
	)
	cl := startMemIMAP(t, [][]byte{msgExact, msgOver}, nil)

	rows, uv, _, err := cl.ReadFolderPage(context.Background(), FolderInbox, 10, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ReadFolderPage: %v rows=%d", err, len(rows))
	}
	// The page lists newest-first; identify the fixtures by UID, not position
	// (msgExact was appended first, so it holds the smaller UID).
	lo, hi := rows[0].UID, rows[1].UID
	if lo > hi {
		lo, hi = hi, lo
	}
	refExact := fmt.Sprintf("uid:%d:%d", uv, lo)
	refOver := fmt.Sprintf("uid:%d:%d", uv, hi)

	part, err := cl.ReadAttachmentPart(context.Background(), FolderInbox, refExact, 0)
	if err != nil {
		t.Fatalf("25 MiB exactly must pass the preview cap (DS-SIZE boundary): %v", err)
	}
	if len(part.Data) != 25<<20 {
		t.Fatalf("decoded length = %d, want exactly 25 MiB", len(part.Data))
	}

	if _, err := cl.ReadAttachmentPart(context.Background(), FolderInbox, refOver, 0); !errors.Is(err, ErrMailPartTooLarge) {
		t.Fatalf("25 MiB + 1 must refuse with the typed over-cap error, got %v (§5.2: actual decoded bytes are the cap authority — never reported metadata)", err)
	}

	// The same over-cap part streams to completion in the Download role —
	// two distinct byte resources (grill I-05), one cap, one uncapped.
	full, err := cl.ReadAttachmentPartFull(context.Background(), FolderInbox, refOver, 0)
	if err != nil {
		t.Fatalf("Download role must not apply the preview cap (I-05): %v", err)
	}
	if len(full.Data) != (25<<20)+1 {
		t.Fatalf("download role delivered %d bytes, want the complete %d", len(full.Data), (25<<20)+1)
	}
}

// ---- I-03: a stale reference is refused before any part data moves ----

func TestStaleReferenceRefusedBeforeAnyPartFetch(t *testing.T) {
	msg := redMultipart("stale-ref",
		redPart("text/plain", `attachment; filename="a.txt"`, "", "", strings.Repeat("A", 30000)),
	)
	cl := startMemIMAP(t, [][]byte{msg}, nil)
	rows, uv, _, err := cl.ReadFolderPage(context.Background(), FolderInbox, 10, 0)
	if err != nil || len(rows) == 0 {
		t.Fatalf("ReadFolderPage: %v rows=%d", err, len(rows))
	}
	goodRef := fmt.Sprintf("uid:%d:%d", uv, rows[0].UID)
	staleRef := fmt.Sprintf("uid:%d:%d", uv+1, rows[0].UID) // recreated-folder epoch

	var goodRead, staleRead int64
	redDialCounting(t, &goodRead)
	if _, err := cl.ReadAttachmentPart(context.Background(), FolderInbox, goodRef, 0); err != nil {
		t.Fatalf("good ref must fetch: %v", err)
	}
	redDialCounting(t, &staleRead)
	_, err = cl.ReadAttachmentPart(context.Background(), FolderInbox, staleRef, 0)
	if !errors.Is(err, ErrMailStaleReference) {
		t.Fatalf("old-epoch ref must refuse with the typed stale-reference error, got %v (I-03)", err)
	}
	if staleRead >= goodRead {
		t.Fatalf("stale-ref path moved %d bytes, at least the good fetch's %d — a part fetch happened before/despite the refusal (I-03: zero fetches)", staleRead, goodRead)
	}
}

func TestReadAttachmentPartOutOfRangeIndex(t *testing.T) {
	msg := redMultipart("range",
		redPart("text/plain", `attachment; filename="a.txt"`, "", "", "hi"),
	)
	cl := startMemIMAP(t, [][]byte{msg}, nil)
	ref := redInboxRef(t, cl)
	if _, err := cl.ReadAttachmentPart(context.Background(), FolderInbox, ref, 7); !errors.Is(err, ErrMailPartNotFound) {
		t.Fatalf("out-of-range part index must refuse with ErrMailPartNotFound, got %v", err)
	}
}

// ---- I-03: a message with no Message-ID needs no identity anywhere ----

func TestReadMessageMetaWithoutMessageID(t *testing.T) {
	raw := []byte("From: Sender <sender@example.test>\r\n" +
		"To: mailbox@test.local\r\n" +
		"Subject: no-mid\r\n" +
		"Date: Mon, 02 Mar 2026 10:00:00 +0000\r\n" +
		"\r\nbody\r\n")
	cl := startMemIMAP(t, [][]byte{raw}, nil)
	ref := redInboxRef(t, cl)
	meta, err := cl.ReadMessageMeta(context.Background(), FolderInbox, ref)
	if err != nil {
		t.Fatalf("ReadMessageMeta: %v", err)
	}
	if meta.MessageID != "" {
		t.Fatalf("MessageID = %q, want empty — no identity may be synthesized for a message that has none (grill I-03)", meta.MessageID)
	}
	if meta.Subject != "no-mid" {
		t.Fatalf("Subject = %q, want no-mid", meta.Subject)
	}
}
