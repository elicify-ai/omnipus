package email

// RED (round 8, silent-failure-hunter F3 — MEDIUM). Oracle: FR-018 (upstream
// mail failures are explicit, never silent in the panel or the logs) and the
// decodeBody house style the tool path already follows: every way a body read
// can degrade is surfaced LOUDLY with a marker from the house wording family
// ("could not be fully decoded", "[truncated N bytes]", "[body incomplete:
// MIME parse error]"). The finding: pkg/email/view.go::readViewText and
// ::viewFromRaw degrade silently on three paths — a mid-decode error renders
// as an EMPTY body (indistinguishable from a genuinely empty mail), a body
// over the 4 MiB view cap renders silently truncated, and a message whose
// MIME cannot be parsed renders as a near-empty view.
//
// Trigger inputs verified by construction probe (inputs only; the markers
// expected below are house-style wording per the dispatch ruling, not
// observed output): headerless bytes, a corrupt base64 text/plain part, a
// >4 MiB text/plain body.

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestViewFromRaw_TextBodyDecodeFailureIsMarked(t *testing.T) {
	// A corrupt base64 text/plain part errors mid-read; readViewText returns
	// "" on HEAD and the mail renders empty. Post-fix it must carry a marker
	// from the house degrade family and keep the decoded prefix.
	good := base64.StdEncoding.EncodeToString([]byte("PARTIAL DECODED SENTINEL DATA PROBE OK"))
	corrupt := good[:16] + "!!!" + good[16:]
	view := ParseViewRaw([]byte(buildMIME(map[string]string{
		"Content-Type":              "text/plain; charset=utf-8",
		"Content-Transfer-Encoding": "base64",
	}, corrupt)))
	require.NotEmpty(t, view.TextBody, "FR-018: a mid-decode read error must NOT render as an empty body")
	require.Contains(t, view.TextBody, "could not be fully decoded",
		"FR-018: the degrade must carry the house 'could not be fully decoded' marker")
	require.Contains(t, view.TextBody, "PARTIAL DEC",
		"the decoded prefix must survive (decodeBody best-effort contract, mirrored on the view path)")
}

func TestViewFromRaw_TextBodyOver4MiBCapIsMarked(t *testing.T) {
	// A body over the 4 MiB view cap may be truncated, but never silently:
	// the view must carry an explicit truncation marker with the byte count
	// (the capBody wording family).
	const sent = "OVER_CAP_SENTINEL"
	unit := sent + "-"
	big := strings.Repeat(unit, (4<<20)/len(unit))
	big += strings.Repeat("A", 1<<20)
	view := ParseViewRaw([]byte(buildMIME(map[string]string{
		"Content-Type": "text/plain; charset=utf-8",
	}, big)))
	require.NotEmpty(t, view.TextBody)
	require.Regexp(t, `truncated \d+ bytes`, view.TextBody,
		"FR-018: an over-cap text body must carry an explicit truncation marker with the byte count, not cut mid-sentence silently")
	require.Contains(t, view.TextBody, sent,
		"the readable prefix must survive the cap")
}

func TestViewFromRaw_UnparseableMIMEIsMarked(t *testing.T) {
	// Bytes no MIME parser accepts: on HEAD viewFromRaw returns a near-empty
	// view with no error and no log; ReadView serves it as a normal message —
	// the unparseable mail renders EMPTY. Post-fix the view must carry the
	// house MIME-parse-error marker.
	raw := []byte("this is not a mime message VIEW_SENTINEL just text, no header")
	view := ParseViewRaw(raw)
	require.NotEmpty(t, view.TextBody, "FR-018: unparseable MIME must not render as an empty body")
	require.Contains(t, view.TextBody, "MIME parse error",
		"FR-018: unparseable MIME must carry the house '[body incomplete: MIME parse error]' marker")
}
