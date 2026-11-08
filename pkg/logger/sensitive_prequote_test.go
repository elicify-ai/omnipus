package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// CHECK finding 1, at the scrubber. ORACLE: a registered credential never
// reaches a log sink in plain form or in an escaped form. Production
// registers only the original plaintext (pkg/config/security.go::
// sensitiveReplacementPairs: each value longer than 3 bytes maps to
// [FILTERED], with no escaped variant). safeLogValue quotes with Go %q
// BEFORE logMessage scrubs. This table feeds the scrubber a value that is
// already in that quoted spelling and reads the real file sink.
//
// Synthetic values only. Not real credentials. Not parallel: the file sink
// and the process-wide replacer are process-wide.

const scrubberMaskToken = "[FILTERED]"

func scrubberShapes() []struct{ name, value string } {
	return []struct{ name, value string }{
		{name: "quote", value: "qa\"needle-7Q"},
		{name: "backslash", value: "back\\slash-9Z"},
		{name: "control", value: "tab\tsecret-3K"},
		{name: "non_ascii", value: "na\u2028dle-4N"},
	}
}

func scrubberForms(value string) (plain, quotedInner, unicodeEscaped string) {
	plain = value
	quoted := strconv.Quote(value)
	quotedInner = quoted[1 : len(quoted)-1]
	var b strings.Builder
	for _, r := range value {
		if r < 0x80 && strconv.IsPrint(r) && r != '"' && r != '\\' {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, `\u%04x`, r)
	}
	return plain, quotedInner, b.String()
}

func scrubberDecoded(t *testing.T, raw []byte) string {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("fixture: file sink line is not JSON: %v", err)
		}
		for _, v := range event {
			if s, ok := v.(string); ok {
				b.WriteString(s)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func scrubberAssertMasked(t *testing.T, raw []byte, shape, cred string) {
	t.Helper()
	decoded := scrubberDecoded(t, raw)
	plain, quotedInner, unicodeEscaped := scrubberForms(cred)
	for _, form := range []struct{ kind, form string }{
		{"plaintext", plain},
		{"percent-q inner", quotedInner},
		{"unicode-u", unicodeEscaped},
	} {
		if strings.Contains(string(raw), form.form) || strings.Contains(decoded, form.form) {
			t.Errorf("shape %s: file sink still contains the %s form of the registered credential; want %s and none of the plain, percent-q or \\u forms",
				shape, form.kind, scrubberMaskToken)
		}
	}
	if !strings.Contains(decoded, scrubberMaskToken) {
		t.Errorf("shape %s: file sink does not contain the mask token %s", shape, scrubberMaskToken)
	}
}

func scrubberLogPreQuoted(t *testing.T, cred string) []byte {
	t.Helper()
	// The production pair is one original plaintext to the mask token
	// (sensitiveReplacementPairs). No escaped variant is registered.
	restore := SetSensitiveValueReplacer(strings.NewReplacer(cred, scrubberMaskToken))
	t.Cleanup(restore)
	path := filepath.Join(t.TempDir(), "gateway.log")
	if err := EnableFileLogging(path); err != nil {
		t.Fatalf("fixture: EnableFileLogging: %v", err)
	}
	t.Cleanup(DisableFileLogging)
	previous := GetLevel()
	SetLevel(DEBUG)
	t.Cleanup(func() { SetLevel(previous) })
	// The field is already in safeLogValue's spelling: %q of a cause that
	// contains the credential. The scrubber must still mask it.
	ErrorCF("test", "provider refused", map[string]any{
		"error": strconv.Quote("provider echoed " + cred),
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture: read file sink: %v", err)
	}
	return raw
}

func TestScrubber_PreQuotedCredentialNeverReachesFileSink(t *testing.T) {
	for _, shape := range scrubberShapes() {
		t.Run(shape.name, func(t *testing.T) {
			raw := scrubberLogPreQuoted(t, shape.value)
			scrubberAssertMasked(t, raw, shape.name, shape.value)
		})
	}
}

func TestScrubber_PlainCredentialIsStillMasked(t *testing.T) {
	const cred = "plain-needle-4K"
	raw := scrubberLogPreQuoted(t, cred)
	scrubberAssertMasked(t, raw, "plain", cred)
}

// --- Round 3, CHECK R2-1: composed fields ---------------------------------
//
// A composed error carries %q fragments INSIDE a longer text, so the
// credential's escaping sits at depth 2+: one whole-field decode cannot
// reach it. The rows below feed the scrubber the exact field shapes the
// production chains produce, and the oracle is the rule itself — no
// reversible Go spelling of the credential at any depth may survive, and
// [FILTERED] must stand in.

type scrubberComposedRow struct {
	name  string
	field func(ws string) string
}

// scrubberComposedRows builds each shape from ws = the credential carrier.
// fragment_middle is pkg/media/store.go::resolveWorkspaceRef's composition
// (`media store: workspace library %q unavailable: %w`) around
// gateway_boot.go:589's provider error (`workspace library unavailable for
// %q`) — both fragments are %q of the same workspace id. nested_quote wraps
// one quoted string inside another. escape_unicode / escape_hex spell the
// non-plain runes with alternative VALID Go escapes — the spellings another
// encoding layer could emit, all decodable by a log reader.
func scrubberComposedRows() []scrubberComposedRow {
	quote := strconv.Quote
	return []scrubberComposedRow{
		{"fragment_middle", func(ws string) string {
			return quote("media store: workspace library " + quote(ws) + " unavailable: workspace library unavailable for " + quote(ws))
		}},
		{"nested_quote", func(ws string) string {
			return quote("outer for " + quote("inner "+quote(ws)))
		}},
		{"escape_unicode", func(ws string) string {
			return quote("composed for " + scrubberQuoteFragment(ws, scrubberUnicodeSpell))
		}},
		{"escape_hex", func(ws string) string {
			return quote("composed for " + scrubberQuoteFragment(ws, scrubberHexSpell))
		}},
	}
}

// scrubberQuoteFragment renders s as a valid Go quoted string whose
// non-plain runes use the given escape spelling.
func scrubberQuoteFragment(s string, spell func(rune) string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r < 0x80 && strconv.IsPrint(r) && r != '"' && r != '\\' {
			b.WriteRune(r)
			continue
		}
		b.WriteString(spell(r))
	}
	b.WriteByte('"')
	return b.String()
}

func scrubberUnicodeSpell(r rune) string {
	return fmt.Sprintf(`\u%04x`, r)
}

func scrubberHexSpell(r rune) string {
	if r < 0x100 {
		return fmt.Sprintf(`\x%02x`, r)
	}
	return fmt.Sprintf(`\u%04x`, r)
}

// scrubberUnescapeGo decodes Go escape sequences in arbitrary text, following
// Go's grammar exactly by delegating to strconv.UnquoteChar (the standard
// library's own per-character decoder): \xHH is a BYTE (two hex digits),
// octal is exactly three digits, \uNNNN four and \UNNNNNNNN eight, and an
// invalid sequence is left as written — Go's behavior for text that is not a
// valid literal. Round-4 decision D4: the grammar authority is strconv; the
// oracle's independence lives in the closure around it (per-field
// application, both decodings at every layer, dedup, and a depth beyond the
// product's).
func scrubberUnescapeGo(s string) (string, bool) {
	if !strings.Contains(s, `\`) {
		return s, false
	}
	var b strings.Builder
	changed := false
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			value, multibyte, tail, err := strconv.UnquoteChar(s[i:], '"')
			if err != nil {
				// \' is valid only with a single-quote delimiter.
				value, multibyte, tail, err = strconv.UnquoteChar(s[i:], '\'')
			}
			if err == nil {
				if multibyte {
					b.WriteRune(value)
				} else {
					b.WriteByte(byte(value))
				}
				i = len(s) - len(tail)
				changed = true
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), changed
}

// scrubberUnquoteWhole adapts strconv.Unquote to the closure's (string, ok)
// shape: only a successfully decoded whole quoted string counts.
func scrubberUnquoteWhole(s string) (string, bool) {
	decoded, err := strconv.Unquote(s)
	return decoded, err == nil
}

// scrubberDecodeClosure returns every string reachable from seed by
// repeatedly unquoting whole strings and decoding escape sequences. The
// bound is 16 levels — twice the product's decoding limit — so the oracle
// can price the boundary: anything the product leaves encoded at eight
// layers must still be decodable here (round-4 decision D4).
func scrubberDecodeClosure(seed string) []string {
	seen := map[string]bool{seed: true}
	frontier := []string{seed}
	for level := 0; level < 16 && len(frontier) > 0; level++ {
		var next []string
		for _, current := range frontier {
			for _, decode := range []func(string) (string, bool){
				scrubberUnquoteWhole,
				scrubberUnescapeGo,
			} {
				if variant, ok := decode(current); ok && variant != current && !seen[variant] {
					seen[variant] = true
					next = append(next, variant)
				}
			}
		}
		frontier = next
	}
	out := make([]string, 0, len(seen))
	for variant := range seen {
		out = append(out, variant)
	}
	return out
}

// scrubberLogRawField logs one field through the real ErrorCF -> file sink
// path with the replacer the caller installed.
func scrubberLogRawField(t *testing.T, field string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.log")
	if err := EnableFileLogging(path); err != nil {
		t.Fatalf("fixture: EnableFileLogging: %v", err)
	}
	t.Cleanup(DisableFileLogging)
	previous := GetLevel()
	SetLevel(DEBUG)
	t.Cleanup(func() { SetLevel(previous) })
	ErrorCF("test", "provider refused", map[string]any{"error": field})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture: read file sink: %v", err)
	}
	return raw
}

func scrubberFileStringValues(t *testing.T, raw []byte) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event),
			"fixture: the file sink must emit one JSON object per line")
		for key, value := range event {
			if key == "error" {
				if s, ok := value.(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

func scrubberAssertComposedMasked(t *testing.T, raw []byte, row, cred string) {
	t.Helper()
	values := scrubberFileStringValues(t, raw)
	require.NotEmpty(t, values, "fixture: the file sink must hold the error field")
	for _, value := range values {
		for _, variant := range scrubberDecodeClosure(value) {
			require.NotContains(t, variant, cred,
				"row %s: a reversible spelling of the registered credential survives the file sink (decoding variant of field %q); want %s",
				row, value, scrubberMaskToken)
		}
	}
	require.Contains(t, strings.Join(values, "\n"), scrubberMaskToken,
		"row %s: the mask token %s must stand in for the credential", row, scrubberMaskToken)
}

func TestScrubber_ComposedQuotedFragmentCredentialNeverReachesFileSink(t *testing.T) {
	const cred = "qa\"needle-7Q"
	for _, row := range scrubberComposedRows() {
		t.Run(row.name, func(t *testing.T) {
			restore := SetSensitiveValueReplacer(strings.NewReplacer(cred, scrubberMaskToken))
			t.Cleanup(restore)
			raw := scrubberLogRawField(t, row.field("ws-1666"+cred+"-ws"))
			scrubberAssertComposedMasked(t, raw, row.name, cred)
		})
	}
}

// TestScrubber_ComposedNoCredentialStaysByteIdentical is the control: with
// a replacer active but no credential anywhere in the field, masking must
// leave the field byte-for-byte as it arrived — it may not rewrite text it
// does not mask (an operator compares these lines against real files).
func TestScrubber_ComposedNoCredentialStaysByteIdentical(t *testing.T) {
	restore := SetSensitiveValueReplacer(strings.NewReplacer("plain-needle-4K", scrubberMaskToken))
	t.Cleanup(restore)
	field := scrubberComposedRows()[0].field("ws-1666-plain-ws")
	raw := scrubberLogRawField(t, field)
	values := scrubberFileStringValues(t, raw)
	require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
	require.Equal(t, field, values[0],
		"a composed field with no registered credential must reach the sink byte-for-byte")
}

// --- Round 4: depth limit, fail-safe branch, byte escapes (decisions D1-D4)
//
// Rule: a registered credential never reaches a log sink in plain form or in
// any form a reader can decode back to it. D1 adds: a field the scrubber
// cannot fully decode within its limit must not survive as it came in; byte
// identity stays required for fields it CAN fully decode and that hold no
// credential. D2 adds: the final whole-field defense gets its own
// regression. D3 adds: a one-byte \xHH echo of a Latin-1 credential must be
// masked, because external diagnostics carry such text into logs.

// scrubberDeepQuote wraps s in the given number of whole Go quoting layers —
// the encoding depth a composed diagnostic chain can reach.
func scrubberDeepQuote(s string, layers int) string {
	for i := 0; i < layers; i++ {
		s = strconv.Quote(s)
	}
	return s
}

// scrubberAssertControlFree applies the #1666 record rule to one decoded
// field value: no C0, DEL, C1 (incl. U+0085), U+2028, U+2029, and valid
// UTF-8.
func scrubberAssertControlFree(t *testing.T, s string) {
	t.Helper()
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029 {
			t.Errorf("field value contains control U+%04X; want a control-free record", r)
		}
	}
	if !utf8.ValidString(s) {
		t.Errorf("field value is not valid UTF-8; want a control-free record")
	}
}

// TestScrubber_DeeplyEncodedCredentialNeverReachesFileSink is decision D1.
// The oracle boundary is the product's own decode limit: at the limit the
// credential must be masked (control row, green before and after); one layer
// beyond, and well beyond, the field must NOT come out as it came in — no
// decoding path may recover the credential and the mask must stand in.
func TestScrubber_DeeplyEncodedCredentialNeverReachesFileSink(t *testing.T) {
	const cred = "qa\"needle-7Q"
	for _, row := range []struct {
		name   string
		layers int
	}{
		{name: "depth_at_limit", layers: 8},
		{name: "depth_first_beyond_limit", layers: 9},
		{name: "depth_well_beyond_limit", layers: 12},
	} {
		t.Run(row.name, func(t *testing.T) {
			restore := SetSensitiveValueReplacer(strings.NewReplacer(cred, scrubberMaskToken))
			t.Cleanup(restore)
			field := scrubberDeepQuote("provider refused: "+cred, row.layers)
			raw := scrubberLogRawField(t, field)
			values := scrubberFileStringValues(t, raw)
			require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
			for _, variant := range scrubberDecodeClosure(values[0]) {
				require.NotContains(t, variant, cred,
					"row %s: a reversible spelling of the registered credential survives the file sink (decoding variant of the field); want %s",
					row.name, scrubberMaskToken)
			}
			require.Contains(t, values[0], scrubberMaskToken,
				"row %s: the mask token %s must stand in for the credential", row.name, scrubberMaskToken)
		})
	}
}

// TestScrubber_DeepCredentialFreeFieldStaysByteIdentical is D1's preservation
// clause: a deeply encoded field the scrubber can fully decode within its
// limit, holding no credential, must reach the sink byte-for-byte — the
// depth machinery may not rewrite text it does not mask.
func TestScrubber_DeepCredentialFreeFieldStaysByteIdentical(t *testing.T) {
	restore := SetSensitiveValueReplacer(strings.NewReplacer("plain-needle-4K", scrubberMaskToken))
	t.Cleanup(restore)
	field := scrubberDeepQuote("provider refused: nothing sensitive here", 8)
	raw := scrubberLogRawField(t, field)
	values := scrubberFileStringValues(t, raw)
	require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
	require.Equal(t, field, values[0],
		"a credential-free field fully decodable within the limit must reach the sink byte-for-byte")
}

// TestScrubber_QuoteBoundaryFailSafeMasksWholeField is decision D2: the
// final whole-field defense gets its own regression, separate from the depth
// test. The witness: a credential whose first character is a double quote,
// a field starting with the credential's tail and carrying an escaped copy
// later — the decoded copy forces masking, and the re-quoted output would
// match the credential across the opening quote boundary, so only the whole
// field replacement satisfies the rule. The assertions are the full output
// (every decoding path clean, control-free, mask present), not a marker.
func TestScrubber_QuoteBoundaryFailSafeMasksWholeField(t *testing.T) {
	const cred = "\"edge-9Z"
	restore := SetSensitiveValueReplacer(strings.NewReplacer(cred, scrubberMaskToken))
	t.Cleanup(restore)
	field := "edge-9Z retry tail \\x22edge-9Z"
	raw := scrubberLogRawField(t, field)
	values := scrubberFileStringValues(t, raw)
	require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
	for _, variant := range scrubberDecodeClosure(values[0]) {
		require.NotContains(t, variant, cred,
			"a reversible spelling of the registered credential survives the file sink (decoding variant of the field); want %s",
			scrubberMaskToken)
	}
	scrubberAssertControlFree(t, values[0])
	require.Contains(t, values[0], scrubberMaskToken,
		"the mask token %s must stand in for the credential", scrubberMaskToken)
}

// TestScrubber_Latin1ByteEchoCredentialNeverReachesFileSink is decision D3.
// A credential with a Latin-1 character, echoed by an external system with
// one-byte \xHH spellings of its UTF-8 bytes, must be masked: provider error
// previews and onboarding raw detail log external text verbatim. The
// within-depth row is the byte-grammar regression (green before and after;
// proven real by the byte-to-rune mutant); the beyond-depth row is the same
// carrier through the depth rule, and must fail while the limit returns
// undecoded fields unchanged.
func TestScrubber_Latin1ByteEchoCredentialNeverReachesFileSink(t *testing.T) {
	const cred = "token-é-9W"
	const echo = `provider echoed token-\xc3\xa9-9W at retry`
	for _, row := range []struct {
		name   string
		layers int
	}{
		{name: "echo_within_depth", layers: 0},
		{name: "echo_beyond_depth", layers: 10},
	} {
		t.Run(row.name, func(t *testing.T) {
			restore := SetSensitiveValueReplacer(strings.NewReplacer(cred, scrubberMaskToken))
			t.Cleanup(restore)
			field := echo
			if row.layers > 0 {
				field = scrubberDeepQuote(echo, row.layers)
			}
			raw := scrubberLogRawField(t, field)
			values := scrubberFileStringValues(t, raw)
			require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
			for _, variant := range scrubberDecodeClosure(values[0]) {
				require.NotContains(t, variant, cred,
					"row %s: a reversible spelling of the registered credential survives the file sink (decoding variant of the field); want %s",
					row.name, scrubberMaskToken)
			}
			require.Contains(t, values[0], scrubberMaskToken,
				"row %s: the mask token %s must stand in for the credential", row.name, scrubberMaskToken)
		})
	}
}

// TestScrubber_EscapeGrammarOctalEchoIsDecoded prices the octal part of the
// grammar (decision D4): a three-digit octal spelling of the quote character
// inside a composed field must decode and mask like any other Go escape.
// Green before and after; it fails if the oracle or the scrubber stops
// reading exact-three-digit octal.
func TestScrubber_EscapeGrammarOctalEchoIsDecoded(t *testing.T) {
	const cred = "qa\"needle-7Q"
	restore := SetSensitiveValueReplacer(strings.NewReplacer(cred, scrubberMaskToken))
	t.Cleanup(restore)
	field := strconv.Quote(`composed for ` + strconv.Quote("ws-1666qa\\042needle-7Q-ws"))
	raw := scrubberLogRawField(t, field)
	values := scrubberFileStringValues(t, raw)
	require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
	for _, variant := range scrubberDecodeClosure(values[0]) {
		require.NotContains(t, variant, cred,
			"a reversible spelling of the registered credential survives the file sink (decoding variant of the field); want %s",
			scrubberMaskToken)
	}
	require.Contains(t, values[0], scrubberMaskToken,
		"the mask token %s must stand in for the credential", scrubberMaskToken)
}

// scrubberSingleByteSpellings returns the one-escaped-byte spelling (\xHH)
// of every code point U+00A0..U+00FF in s — the spelling a diagnostic source
// outside this process may use for an accented character, writing its code
// point instead of the character's UTF-8 bytes.
func scrubberSingleByteSpellings(s string) []string {
	var out []string
	for _, r := range s {
		if r >= 0xa0 && r <= 0xff {
			out = append(out, fmt.Sprintf(`\x%02x`, r))
		}
	}
	return out
}

// scrubberByteSpelled spells every byte of an ASCII value as \xHH, for the
// control row.
func scrubberByteSpelled(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, `\x%02x`, s[i])
	}
	return b.String()
}

// TestScrubber_SingleByteCodeEchoCredentialNeverReachesFileSink extends the
// echo regressions to the one-byte code-point reading (round-5 decision on
// the developer's round-4 open question): a diagnostic source outside this
// process may spell an accented character of a registered value as ONE
// escaped byte of its code point (U+00A0..U+00FF), inside a quoted field,
// within the decode limit. The rule is unchanged — the value must not reach
// the sink in that or any other recoverable spelling, and the mask must
// stand in. The ASCII row shows the existing byte-spelling masking is
// unchanged: an ASCII value spelled byte-for-byte is masked today and must
// stay masked.
func TestScrubber_SingleByteCodeEchoCredentialNeverReachesFileSink(t *testing.T) {
	t.Run("code_point_within_depth", func(t *testing.T) {
		const cred = "token-é-9W"
		restore := SetSensitiveValueReplacer(strings.NewReplacer(cred, scrubberMaskToken))
		t.Cleanup(restore)
		field := strconv.Quote(`provider echoed token-\xe9-9W at retry`)
		raw := scrubberLogRawField(t, field)
		values := scrubberFileStringValues(t, raw)
		require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
		for _, variant := range scrubberDecodeClosure(values[0]) {
			require.NotContains(t, variant, cred,
				"a recoverable spelling of the registered credential survives the file sink (decoding variant of the field); want %s",
				scrubberMaskToken)
		}
		for _, spelling := range scrubberSingleByteSpellings(cred) {
			require.NotContains(t, values[0], spelling,
				"the one-byte escaped spelling %s of the registered character survives the file sink; want %s",
				spelling, scrubberMaskToken)
		}
		require.Contains(t, values[0], scrubberMaskToken,
			"the mask token %s must stand in for the credential", scrubberMaskToken)
	})

	t.Run("ascii_bytes_control", func(t *testing.T) {
		const cred = "token-9W"
		restore := SetSensitiveValueReplacer(strings.NewReplacer(cred, scrubberMaskToken))
		t.Cleanup(restore)
		field := strconv.Quote("provider echoed " + scrubberByteSpelled(cred) + " at retry")
		raw := scrubberLogRawField(t, field)
		values := scrubberFileStringValues(t, raw)
		require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
		for _, variant := range scrubberDecodeClosure(values[0]) {
			require.NotContains(t, variant, cred,
				"a recoverable spelling of the registered credential survives the file sink (decoding variant of the field); want %s",
				scrubberMaskToken)
		}
		require.Contains(t, values[0], scrubberMaskToken,
			"the mask token %s must stand in for the credential", scrubberMaskToken)
	})
}
