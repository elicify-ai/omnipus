package email

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
)

// The decode tests feed REAL raw MIME bytes to the REAL decode function
// (decodeBody) — not a canned-plaintext fake. That fake is exactly the gap that
// let the "base64 blob reaches the agent" bug ship: the old bufferToMessage did
// string(bytes) with zero decoding, and no test ever exercised a real encoded
// body. Every expected value here is derived from the MIME spec (RFC 2045/2047),
// never read back from the implementation.

// buildMIME assembles a single-part message with the given headers + body.
func buildMIME(headers map[string]string, body string) string {
	var sb strings.Builder
	// Deterministic base headers so an Envelope exists; callers override via map.
	base := map[string]string{
		"From":         "Alice <alice@example.com>",
		"To":           "bob@example.com",
		"Subject":      "Test",
		"Date":         "Mon, 02 Jan 2006 15:04:05 -0700",
		"MIME-Version": "1.0",
	}
	for k, v := range headers {
		base[k] = v
	}
	for k, v := range base {
		sb.WriteString(k + ": " + v + "\r\n")
	}
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}

// TestDecodeBody_Base64TextPlain proves a base64 text/plain body is DECODED, not
// forwarded as the raw base64 blob (the shipped bug).
func TestDecodeBody_Base64TextPlain(t *testing.T) {
	// Oracle: a plaintext we choose. The wire body is its base64 encoding per
	// RFC 2045 §6.8 — encoded here by the stdlib, independent of decodeBody.
	const want = "Hello, World! Base64 bodies must be decoded, not shown raw."
	raw := buildMIME(map[string]string{
		"Content-Type":              "text/plain; charset=utf-8",
		"Content-Transfer-Encoding": "base64",
	}, base64.StdEncoding.EncodeToString([]byte(want)))

	got := decodeBody([]byte(raw))
	if got != want {
		t.Fatalf("base64 body not decoded.\n got: %q\nwant: %q", got, want)
	}
	// Guard against a no-op that just echoes the raw bytes.
	if strings.Contains(got, base64.StdEncoding.EncodeToString([]byte(want))) {
		t.Fatalf("decoded body still contains the raw base64 blob: %q", got)
	}
}

// TestDecodeBody_QuotedPrintable proves quoted-printable decoding: =XX hex
// escapes become their bytes and =<CRLF> soft breaks are removed (RFC 2045 §6.7).
func TestDecodeBody_QuotedPrintable(t *testing.T) {
	// "Café costs €5 per cup." — é = UTF-8 C3 A9, € = UTF-8 E2 82 AC.
	// "=\r\n" is a soft line break the decoder must drop.
	body := "Caf=C3=A9 costs =E2=82=AC5 =\r\nper cup."
	const want = "Café costs €5 per cup."
	raw := buildMIME(map[string]string{
		"Content-Type":              "text/plain; charset=utf-8",
		"Content-Transfer-Encoding": "quoted-printable",
	}, body)

	got := decodeBody([]byte(raw))
	if got != want {
		t.Fatalf("quoted-printable not decoded.\n got: %q\nwant: %q", got, want)
	}
}

// TestDecodeBody_MultipartAlternativePrefersPlain proves that for a
// multipart/alternative message the text/plain part is chosen over text/html.
func TestDecodeBody_MultipartAlternativePrefersPlain(t *testing.T) {
	const boundary = "BOUNDARY42"
	body := "--" + boundary + "\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"This is the PLAIN part.\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>This is the <b>HTML</b> part.</p>\r\n" +
		"--" + boundary + "--\r\n"
	raw := buildMIME(map[string]string{
		"Content-Type": "multipart/alternative; boundary=\"" + boundary + "\"",
	}, body)

	got := decodeBody([]byte(raw))
	if got != "This is the PLAIN part." {
		t.Fatalf("multipart/alternative must pick text/plain, got: %q", got)
	}
	if strings.Contains(got, "HTML part") || strings.Contains(got, "<p>") {
		t.Fatalf("text/plain preference leaked HTML content: %q", got)
	}
}

// TestDecodeBody_HTMLOnlyStripsTags proves an HTML-only body is flattened to
// readable text: tags removed, script/style dropped, entities decoded, blocks
// separated.
func TestDecodeBody_HTMLOnlyStripsTags(t *testing.T) {
	html := "<html><head><style>.x{color:red}</style></head><body>" +
		"<h1>Heading</h1><p>First&nbsp;paragraph.</p><p>Second paragraph.</p>" +
		"<script>alert('nope')</script></body></html>"
	raw := buildMIME(map[string]string{
		"Content-Type": "text/html; charset=utf-8",
	}, html)

	got := decodeBody([]byte(raw))
	for _, want := range []string{"Heading", "First paragraph.", "Second paragraph."} {
		if !strings.Contains(got, want) {
			t.Errorf("HTML→text missing %q; got: %q", want, got)
		}
	}
	for _, bad := range []string{"<h1>", "<p>", "color:red", "alert", "nope"} {
		if strings.Contains(got, bad) {
			t.Errorf("HTML→text leaked %q; got: %q", bad, got)
		}
	}
	// Block tags must separate paragraphs — they must not run together.
	if strings.Contains(got, "paragraph.Second") {
		t.Errorf("adjacent block elements were not separated: %q", got)
	}
}

// TestDecodeBody_NonUTF8Charset proves a non-UTF-8 (ISO-8859-1) body is decoded
// via the registered charset reader. Raw byte 0xE9 = 'é', 0xEF = 'ï' in Latin-1.
func TestDecodeBody_NonUTF8Charset(t *testing.T) {
	rawStr := buildMIME(map[string]string{
		"Content-Type":              "text/plain; charset=ISO-8859-1",
		"Content-Transfer-Encoding": "8bit",
	}, "PLACEHOLDER")
	// Inject the actual Latin-1 bytes for "Café naïve" in place of PLACEHOLDER.
	rawBytes := []byte(rawStr)
	latin1 := append([]byte("Caf"), 0xE9)     // Café
	latin1 = append(latin1, []byte(" na")...) // " na"
	latin1 = append(latin1, 0xEF)             // ï
	latin1 = append(latin1, []byte("ve")...)  // ve
	rawBytes = []byte(strings.Replace(string(rawBytes), "PLACEHOLDER", "", 1))
	rawBytes = append(rawBytes, latin1...)

	got := decodeBody(rawBytes)
	const want = "Café naïve"
	if got != want {
		t.Fatalf("ISO-8859-1 not decoded to UTF-8.\n got: %q (% x)\nwant: %q", got, got, want)
	}
}

// TestDecodeBody_SizeCap proves a very large decoded body is truncated with a
// byte-accurate marker instead of bloating the agent context unbounded.
func TestDecodeBody_SizeCap(t *testing.T) {
	bodyText := strings.Repeat("a", maxBodyBytes+50000) // all ASCII, no rune backoff
	raw := buildMIME(map[string]string{
		"Content-Type": "text/plain; charset=utf-8",
	}, bodyText)

	got := decodeBody([]byte(raw))
	if len(got) >= len(bodyText) {
		t.Fatalf("body was not truncated: got %d bytes, source %d", len(got), len(bodyText))
	}
	expectedRemoved := len(bodyText) - maxBodyBytes
	marker := fmt.Sprintf("\n…[truncated %d bytes]", expectedRemoved)
	if !strings.Contains(got, marker) {
		t.Fatalf("missing/incorrect truncation marker; want substring %q in %q…", marker, got[len(got)-64:])
	}
	if !strings.HasPrefix(got, "aaaa") {
		t.Fatalf("truncated body should retain the leading content")
	}
	// The kept content is exactly maxBodyBytes of body plus the marker.
	if len(got) != maxBodyBytes+len(marker) {
		t.Fatalf("kept-content length wrong: got %d, want %d", len(got), maxBodyBytes+len(marker))
	}
}

// TestDecodeBody_PlainUnencoded proves a plain 7bit body passes through intact
// (the simplest, most common case — no encoding, no MIME parts).
func TestDecodeBody_PlainUnencoded(t *testing.T) {
	const want = "Just a plain sentence."
	raw := buildMIME(map[string]string{
		"Content-Type": "text/plain; charset=utf-8",
	}, want)
	if got := decodeBody([]byte(raw)); got != want {
		t.Fatalf("plain body altered: got %q want %q", got, want)
	}
}

// TestDecodeBody_Differentiation rules out a constant/no-op decoder: two
// different messages must yield two different bodies.
func TestDecodeBody_Differentiation(t *testing.T) {
	a := decodeBody([]byte(buildMIME(map[string]string{"Content-Type": "text/plain"}, "alpha body")))
	b := decodeBody([]byte(buildMIME(map[string]string{"Content-Type": "text/plain"}, "beta body")))
	if a == b {
		t.Fatalf("decodeBody returned identical output for different messages: %q", a)
	}
}

// --- pure-helper unit tests ---

func TestHTMLToText_Table(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", "hello world"},
		{"strips inline tag", "a <b>bold</b> word", "a bold word"},
		{"entities decoded", "1 &lt; 2 &amp; 3 &gt; 0", "1 < 2 & 3 > 0"},
		{"drops script", "keep<script>drop()</script>", "keep"},
		{"drops style", "keep<style>.a{}</style>", "keep"},
		{"collapses spaces", "too      many     spaces", "too many spaces"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := htmlToText(tc.in); got != tc.want {
				t.Errorf("htmlToText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCapBody_ShortUnchanged(t *testing.T) {
	s := "short body"
	if got := capBody(s); got != s {
		t.Fatalf("capBody altered a short body: %q", got)
	}
}

func TestClampLimit_Table(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{0, defaultListLimit},
		{-5, defaultListLimit},
		{1, 1},
		{defaultListLimit, defaultListLimit},
		{50, 50},
		{maxListLimit, maxListLimit},
		{maxListLimit + 1, maxListLimit},
		{100000, maxListLimit},
	}
	for _, tc := range tests {
		if got := clampLimit(tc.in); got != tc.want {
			t.Errorf("clampLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// criteriaHasBody reports whether a (possibly nested) SEARCH criteria tree
// contains any BODY term — used to assert body search is opt-in.
func criteriaHasBody(c *imap.SearchCriteria) bool {
	if c == nil {
		return false
	}
	if len(c.Body) > 0 {
		return true
	}
	for _, or := range c.Or {
		if criteriaHasBody(&or[0]) || criteriaHasBody(&or[1]) {
			return true
		}
	}
	for i := range c.Not {
		if criteriaHasBody(&c.Not[i]) {
			return true
		}
	}
	return false
}

// TestBuildSearchCriteria_BodyOptIn proves the default search does NOT issue an
// expensive BODY scan, and that body=true does.
func TestBuildSearchCriteria_BodyOptIn(t *testing.T) {
	light := buildSearchCriteria("invoice", false, 0)
	if criteriaHasBody(light) {
		t.Error("default (body=false) search must not contain a BODY term")
	}
	heavy := buildSearchCriteria("invoice", true, 0)
	if !criteriaHasBody(heavy) {
		t.Error("body=true search must contain a BODY term")
	}
}

// TestBuildSearchCriteria_BeforeUIDCursor proves before_uid becomes a
// "UID < before_uid" range criterion.
func TestBuildSearchCriteria_BeforeUIDCursor(t *testing.T) {
	// No cursor → no UID criterion.
	if c := buildSearchCriteria("q", false, 0); len(c.UID) != 0 {
		t.Errorf("before_uid=0 must not set a UID criterion, got %+v", c.UID)
	}
	// before_uid=1 → nothing is strictly below 1; still no UID range added
	// (the transport short-circuits UID==1 in the read path, and the range
	// [1,0] would be nonsensical here).
	if c := buildSearchCriteria("q", false, 1); len(c.UID) != 0 {
		t.Errorf("before_uid=1 must not set a UID range, got %+v", c.UID)
	}
	// before_uid=10 → range [1,9].
	c := buildSearchCriteria("q", false, 10)
	if len(c.UID) != 1 {
		t.Fatalf("before_uid=10 must set exactly one UID set, got %+v", c.UID)
	}
	if !c.UID[0].Contains(imap.UID(9)) || c.UID[0].Contains(imap.UID(10)) {
		t.Errorf("UID range must include 9 and exclude 10, got %v", c.UID[0])
	}
}
