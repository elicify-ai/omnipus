// This file is the external test package (logger_test) because the rows
// register their synthetic value the way the product registers real
// credentials: pkg/config/security.go::(*Config).RegisterSensitiveValues
// publishes the process-wide credential replacer that the logger scrubs
// with. pkg/config imports pkg/logger, so only a test package outside
// pkg/logger itself can drive that registration.
//
// ORACLE: a registered credential never reaches a log sink in plain form or
// in an escaped form. Synthetic values only. Not parallel: the file sink and
// the process-wide replacer are process-wide, so the registration block
// captures the previous replacer for cleanup first.
package logger_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/require"
)

const scrubberMaskToken = "[FILTERED]"

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
// layers must still be decodable here.
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
// path with the replacer the production registration published.
func scrubberLogRawField(t *testing.T, field string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.log")
	if err := logger.EnableFileLogging(path); err != nil {
		t.Fatalf("fixture: EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)
	previous := logger.GetLevel()
	logger.SetLevel(logger.DEBUG)
	t.Cleanup(func() { logger.SetLevel(previous) })
	logger.ErrorCF("test", "provider refused", map[string]any{"error": field})
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

// scrubberRegisterCredential registers the value the way real credentials are
// registered: pkg/config/security.go::(*Config).RegisterSensitiveValues
// publishes the process-wide replacer the logger uses. The nil swap only
// captures whatever replacer ran before this test for cleanup.
func scrubberRegisterCredential(t *testing.T, cred string) {
	t.Helper()
	restore := logger.SetSensitiveValueReplacer(nil)
	t.Cleanup(restore)
	cfg := &config.Config{}
	cfg.RegisterSensitiveValues([]string{cred})
}

// TestScrubber_SingleByteCodeEchoCredentialNeverReachesFileSink extends the
// echo regressions to the one-byte code-point reading (round-5 decision on
// the developer's round-4 open question; round 5b re-homes the rows onto the
// production registration path): a diagnostic source outside this process
// may spell an accented character of a registered value as ONE escaped byte
// of its code point (U+00A0..U+00FF), inside a quoted field, within the
// decode limit. The rule is unchanged — the value must not reach the sink in
// that or any other recoverable spelling, and the mask must stand in. The
// ASCII row shows the existing byte-spelling masking is unchanged: an ASCII
// value spelled byte-for-byte is masked today and must stay masked.
func TestScrubber_SingleByteCodeEchoCredentialNeverReachesFileSink(t *testing.T) {
	t.Run("code_point_within_depth", func(t *testing.T) {
		const cred = "token-é-9W"
		scrubberRegisterCredential(t, cred)
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
		scrubberRegisterCredential(t, cred)
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

// scrubberCodePointSpellings returns the one-byte escaped spellings of a
// code point in U+0080..U+00FF in both hex cases — the forms a diagnostic
// source outside this process may use for one of a value's characters.
func scrubberCodePointSpellings(r rune) []string {
	return []string{
		fmt.Sprintf(`\x%02x`, r),
		fmt.Sprintf(`\x%02X`, r),
	}
}

// scrubberAssertSpellingEchoMasked runs the echo oracle for one spelling
// row: exactly one field in the sink, no decoding variant recovers the
// registered credential, no escaped spelling of the registered characters
// survives, and the mask stands in.
func scrubberAssertSpellingEchoMasked(t *testing.T, cred string, spellings []string, field string) {
	t.Helper()
	raw := scrubberLogRawField(t, field)
	values := scrubberFileStringValues(t, raw)
	require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
	for _, variant := range scrubberDecodeClosure(values[0]) {
		require.NotContains(t, variant, cred,
			"a recoverable spelling of the registered credential survives the file sink (decoding variant of the field); want %s",
			scrubberMaskToken)
	}
	for _, spelling := range spellings {
		require.NotContains(t, values[0], spelling,
			"the escaped spelling %s of the registered character survives the file sink; want %s",
			spelling, scrubberMaskToken)
	}
	require.Contains(t, values[0], scrubberMaskToken,
		"the mask token %s must stand in for the credential", scrubberMaskToken)
}

// TestScrubber_RegisteredSpellingEchoesNeverReachFileSink prices the echo
// variants of the spelling rule (round 6): the one-byte escaped spelling of
// a registered value's character may arrive with uppercase hex digits, may
// be mixed with literal characters of the same value, and may concern a
// code point below U+00A0 as well. In every variant the value must not
// reach the sink in any recoverable spelling, and the mask must stand in.
// The unicode_* rows mix a Unicode escape with a \xHH escape in one
// field; character_octal spells a character as a three-digit octal escape.
// The byte-identity controls below show credential-free content is
// untouched.
func TestScrubber_RegisteredSpellingEchoesNeverReachFileSink(t *testing.T) {
	t.Run("uppercase_hex", func(t *testing.T) {
		const cred = "token-é-9W"
		scrubberRegisterCredential(t, cred)
		field := strconv.Quote(`provider echoed token-\xE9-9W at retry`)
		scrubberAssertSpellingEchoMasked(t, cred, scrubberCodePointSpellings('é'), field)
	})

	t.Run("mixed_escape_second", func(t *testing.T) {
		const cred = "töken-éa-7M"
		scrubberRegisterCredential(t, cred)
		field := strconv.Quote(`provider echoed töken-\xe9a-7M at retry`)
		spellings := append(scrubberCodePointSpellings('ö'), scrubberCodePointSpellings('é')...)
		scrubberAssertSpellingEchoMasked(t, cred, spellings, field)
	})

	t.Run("mixed_escape_first", func(t *testing.T) {
		const cred = "töken-éa-7M"
		scrubberRegisterCredential(t, cred)
		field := strconv.Quote(`provider echoed t\xf6ken-éa-7M at retry`)
		spellings := append(scrubberCodePointSpellings('ö'), scrubberCodePointSpellings('é')...)
		scrubberAssertSpellingEchoMasked(t, cred, spellings, field)
	})

	t.Run("c1_code_point", func(t *testing.T) {
		const cred = "token-\u0085-8F"
		scrubberRegisterCredential(t, cred)
		field := strconv.Quote(`provider echoed token-\x85-8F at retry`)
		scrubberAssertSpellingEchoMasked(t, cred, scrubberCodePointSpellings('\u0085'), field)
	})

	t.Run("unicode_hex_composed", func(t *testing.T) {
		const cred = "töken-éa-7M"
		scrubberRegisterCredential(t, cred)
		field := strconv.Quote(`provider echoed t\u00f6ken-\xe9a-7M at retry`)
		// No spelling list: the mask assertion carries this row.
		scrubberAssertSpellingEchoMasked(t, cred, nil, field)
	})

	t.Run("unicode_long_hex_composed", func(t *testing.T) {
		const cred = "tüken-éb-3N"
		scrubberRegisterCredential(t, cred)
		field := strconv.Quote(`provider echoed t\U000000fcken-\xe9b-3N at retry`)
		scrubberAssertSpellingEchoMasked(t, cred, nil, field)
	})

	t.Run("character_octal", func(t *testing.T) {
		const cred = "token-é-9W"
		scrubberRegisterCredential(t, cred)
		field := strconv.Quote(`provider echoed token-\351-9W at retry`)
		scrubberAssertSpellingEchoMasked(t, cred, nil, field)
	})
}

// TestScrubber_SpellingEscapesCredentialFreeFieldStaysByteIdentical is the
// control for the spelling rows: escape text for characters nobody
// registered is diagnostic content like any other. A value is registered
// only so the scrubber runs with a real masking set; the field holds no
// credential and must reach the sink unchanged.
func TestScrubber_SpellingEscapesCredentialFreeFieldStaysByteIdentical(t *testing.T) {
	scrubberRegisterCredential(t, "plain-needle-4K")
	field := strconv.Quote(`provider echoed \xE9 and \x85 in an unrelated retry`)
	raw := scrubberLogRawField(t, field)
	values := scrubberFileStringValues(t, raw)
	require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
	require.Equal(t, field, values[0],
		"a credential-free field with escape text must reach the sink byte-for-byte")
}

// TestScrubber_AsciiCredentialUnrelatedFieldStaysByteIdentical is the second
// control: with an ASCII-only value registered, an unrelated field must
// still reach the sink byte-for-byte.
func TestScrubber_AsciiCredentialUnrelatedFieldStaysByteIdentical(t *testing.T) {
	scrubberRegisterCredential(t, "token-7Q")
	field := strconv.Quote("provider refused with an unrelated diagnostic tail")
	raw := scrubberLogRawField(t, field)
	values := scrubberFileStringValues(t, raw)
	require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
	require.Equal(t, field, values[0],
		"an unrelated field must reach the sink byte-for-byte")
}

// TestScrubber_ComposedEscapeKindsCredentialFreeFieldStaysByteIdentical
// extends the byte-identity controls to the remaining escape kinds: a
// credential-free field mixing short hex with a Unicode escape, and one
// carrying octal escapes, must both reach the sink unchanged.
func TestScrubber_ComposedEscapeKindsCredentialFreeFieldStaysByteIdentical(t *testing.T) {
	t.Run("hex_with_unicode", func(t *testing.T) {
		scrubberRegisterCredential(t, "plain-needle-4K")
		field := strconv.Quote(`provider echoed \xf6 and \u00e9 and \U000000e9 in an unrelated retry`)
		raw := scrubberLogRawField(t, field)
		values := scrubberFileStringValues(t, raw)
		require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
		require.Equal(t, field, values[0],
			"a credential-free field with composed escape text must reach the sink byte-for-byte")
	})

	t.Run("character_octal", func(t *testing.T) {
		scrubberRegisterCredential(t, "plain-needle-4K")
		field := strconv.Quote(`provider echoed \351 and \240 in an unrelated retry`)
		raw := scrubberLogRawField(t, field)
		values := scrubberFileStringValues(t, raw)
		require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
		require.Equal(t, field, values[0],
			"a credential-free field with octal escape text must reach the sink byte-for-byte")
	})
}

// TestScrubber_FinalCheckMasksWholeField is the regression for the scrubber's
// last check before a decoded field is written: a field whose written form
// would itself hold the registered value is replaced whole. It is separate
// from TestScrubber_QuoteBoundaryFailSafeMasksWholeField, whose input is
// handled earlier in the scrub.
func TestScrubber_FinalCheckMasksWholeField(t *testing.T) {
	const unit = "edge-é-9"
	const cred = unit + "\""
	scrubberRegisterCredential(t, cred)
	field := `retry edge-\xc3\xa9-9\" tail ` + unit
	raw := scrubberLogRawField(t, field)
	values := scrubberFileStringValues(t, raw)
	require.Len(t, values, 1, "fixture: exactly the one error field must be in the sink")
	for _, variant := range scrubberDecodeClosure(values[0]) {
		require.NotContains(t, variant, cred,
			"a recoverable spelling of the registered credential survives the file sink (decoding variant of the field); want %s",
			scrubberMaskToken)
	}
	require.Equal(t, scrubberMaskToken, values[0],
		"the whole field must be replaced by %s", scrubberMaskToken)
}
