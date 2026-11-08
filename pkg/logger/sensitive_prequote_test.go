package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

// scrubberUnescapeGo decodes Go escape sequences in arbitrary text (n r t a
// b f v, \\, \", \', \xHH, \uNNNN, \UNNNNNNNN, octal); unknown sequences
// pass through. This is the reader side of "escaped form": whatever a Go
// formatter emitted, a log reader can decode.
func scrubberUnescapeGo(s string) (string, bool) {
	var b strings.Builder
	changed := false
	for i := 0; i < len(s); {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch e := s[i+1]; e {
		case 'a':
			b.WriteByte(7)
			i += 2
			changed = true
		case 'b':
			b.WriteByte(8)
			i += 2
			changed = true
		case 'f':
			b.WriteByte(12)
			i += 2
			changed = true
		case 'n':
			b.WriteByte('\n')
			i += 2
			changed = true
		case 'r':
			b.WriteByte('\r')
			i += 2
			changed = true
		case 't':
			b.WriteByte('\t')
			i += 2
			changed = true
		case 'v':
			b.WriteByte(11)
			i += 2
			changed = true
		case '\\', '"', '\'':
			b.WriteByte(e)
			i += 2
			changed = true
		case 'x', 'u', 'U':
			width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[e]
			if i+2+width <= len(s) && scrubberHexDigits(s[i+2:i+2+width]) {
				value, _ := strconv.ParseUint(s[i+2:i+2+width], 16, 32)
				b.WriteRune(rune(value))
				i += 2 + width
				changed = true
			} else {
				b.WriteByte(s[i])
				i++
			}
		case '0', '1', '2', '3', '4', '5', '6', '7':
			digits := 1
			for digits < 3 && i+1+digits < len(s) && s[i+1+digits] >= '0' && s[i+1+digits] <= '7' {
				digits++
			}
			value, _ := strconv.ParseUint(s[i+1:i+1+digits], 8, 8)
			b.WriteByte(byte(value))
			i += 1 + digits
			changed = true
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), changed
}

func scrubberHexDigits(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return len(s) > 0
}

// scrubberUnquoteWhole adapts strconv.Unquote to the closure's (string, ok)
// shape: only a successfully decoded whole quoted string counts.
func scrubberUnquoteWhole(s string) (string, bool) {
	decoded, err := strconv.Unquote(s)
	return decoded, err == nil
}

// scrubberDecodeClosure returns every string reachable from seed by
// repeatedly unquoting whole strings and decoding escape sequences, bounded
// at six levels (the composed fixtures nest at most four).
func scrubberDecodeClosure(seed string) []string {
	seen := map[string]bool{seed: true}
	frontier := []string{seed}
	for level := 0; level < 6 && len(frontier) > 0; level++ {
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
