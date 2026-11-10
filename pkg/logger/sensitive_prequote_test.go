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

// ORACLE: a registered credential never reaches a log sink in plain form or
// in an escaped form. The gateway quotes a value with Go %q (safeLogValue)
// before the logger scrubs it, so the scrubber receives values that are
// already in the quoted spelling. The tables here feed the scrubber such
// values and read the real file sink. The replacer holds only the
// credential's plain text, so the scrubber's own handling is what is tested;
// the registration path the product uses is covered in
// sensitive_registration_test.go.
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
	// A replacer holding only the plain-text pair: the scrubber's own
	// handling of the quoted field is what the rows exercise.
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

// --- Composed fields -------------------------------------------------------
//
// An error composed from several messages carries quoted fragments inside a
// longer text. The rows below feed the scrubber field shapes like the ones
// the gateway's error chains produce, and the oracle is the rule itself: no
// reversible Go spelling of the credential may reach the sink, and the mask
// token must stand in for it.

type scrubberComposedRow struct {
	name  string
	field func(ws string) string
}

// scrubberComposedRows builds each row's field from ws, the text that
// carries the credential. fragment_middle follows the message composition of
// the media store's workspace-reference error, nested_quote nests one quoted
// string inside another, and the escape_* rows write the same kind of
// fragment with other valid Go escapes.
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

// scrubberUnescapeGo decodes the Go escape sequences in arbitrary text with
// strconv.UnquoteChar, the standard library's own per-character decoder, so
// the oracle reads an escape the way Go does. A sequence that is not a valid
// escape is left as written. The oracle's independence from the product lies
// in the closure built around this decoder (scrubberDecodeClosure), not in
// the decoder itself.
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
// repeatedly unquoting whole strings and decoding escape sequences. Its
// bound is deeper than the product's own decoding limit, so a field the
// product leaves encoded can still be unwound here. It models a log reader
// undoing whatever escaping sits over the credential.
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

// --- Decoding limit, whole-field replacement, byte-escaped echoes ----------
//
// Rule: a registered credential never reaches a log sink in plain form or in
// any form a reader can decode back to it. A field the scrubber cannot fully
// decode within its limit must not reach the sink as it came in; a field it
// can fully decode that holds no credential still reaches the sink
// byte-for-byte. Text that external diagnostics write with byte escapes for a
// credential's non-ASCII characters must be masked too.

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

// TestScrubber_DeeplyEncodedCredentialNeverReachesFileSink checks the
// scrubber at and beyond its decoding limit. At the limit the credential must
// be masked; one layer beyond, and well beyond, the field must not reach the
// sink as it came in: no decoding path of the logged field may recover the
// credential, and the mask token must be present.
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

// TestScrubber_DeepCredentialFreeFieldStaysByteIdentical is the control for
// the decoding-limit test: a deeply encoded field that holds no credential
// and can be fully decoded within the limit must reach the sink
// byte-for-byte; the scrubber may not rewrite text it does not mask.
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

// TestScrubber_QuoteBoundaryFailSafeMasksWholeField covers a registered value
// that begins with a double quote. A field that holds that value in an
// escaped spelling must reach the sink without any decodable copy of it. The
// assertions are on the whole logged field (no decoding path recovers the
// value, the record is control-free, the mask token is present) and do not
// depend on which step of the scrubber decides the outcome.
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

// TestScrubber_Latin1ByteEchoCredentialNeverReachesFileSink covers a
// credential with a Latin-1 character that an external system echoes with
// byte escapes. Text from an external system can reach the log as it was
// sent, so the echo must be masked. The second row carries the same echo
// under more quoting layers than the scrubber's decoding limit.
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

// TestScrubber_EscapeGrammarOctalEchoIsDecoded checks that a three-digit
// octal escape of the quote character inside a composed field is read and
// masked like any other Go escape.
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
