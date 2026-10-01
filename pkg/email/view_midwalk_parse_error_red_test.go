package email

// RED (round-2 delta review, item 4 — code-reviewer, I7). Oracle: FR-018 /
// the decodeBody house style (round-8 F3) already followed elsewhere in
// this package: a genuine mid-stream MIME parse error must degrade LOUDLY
// (a visible marker, plus a logged warning), never silently, and must never
// be treated the same as the ordinary end-of-parts signal (io.EOF).
//
// pkg/email/view.go::viewFromRaw's leaf-walk loop currently does:
//
//	part, perr := reader.NextPart(); if perr != nil || part == nil { break }
//
// which breaks silently on ANY non-nil error, io.EOF or not — a genuine
// parse failure mid-walk renders exactly like a normal, clean end of parts.
//
// Trigger: a multipart message whose closing boundary is missing after its
// second part. go-message's mail.Reader.NextPart() (vendored
// go-message@v0.18.2) returns both parts successfully, then returns a
// genuine, NON-io.EOF error ("multipart: NextPart: EOF") on the next call —
// confirmed by construction probe against the vendored package — because
// textproto.MultipartReader.NextPart wraps the underlying read failure with
// fmt.Errorf instead of returning the io.EOF sentinel, so errors.Is(perr,
// io.EOF) is false for it.
//
// ORACLE PROVENANCE: expected values are FR-018 and decodeBody's own
// already-shipped marker wording ("[body incomplete: MIME parse error]",
// the same wording view.go's own top-level unparseable-MIME branch already
// uses) — never read off viewFromRaw's current (silent) behavior.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestViewFromRaw_MidWalkParseErrorIsMarked(t *testing.T) {
	const firstPartBody = "first part body, must survive intact"
	raw := "From: a@b.test\r\nTo: c@d.test\r\nSubject: x\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <midparse@b.test>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=bnd\r\n\r\n" +
		"--bnd\r\nContent-Type: text/plain\r\n\r\n" + firstPartBody + "\r\n" +
		"--bnd\r\nContent-Type: text/html\r\n\r\n<p>second part, html, never overwrites TextBody</p>"
	// Deliberately NO closing "--bnd--" boundary line — this is what
	// triggers the genuine mid-stream parse error on the call after the
	// second part, per the construction probe described above.

	view := ParseViewRaw([]byte(raw))

	require.Contains(t, view.TextBody, firstPartBody,
		"the first (successfully parsed) part's text must survive intact despite the later mid-walk parse error")
	require.Contains(t, view.TextBody, "MIME parse error",
		"FR-018: a genuine mid-stream MIME parse error must carry the house '[body incomplete: MIME parse error]' marker, exactly like the top-level unparseable-MIME case already does — a real parse failure must never be silently treated as the normal end of parts (io.EOF)")
}
