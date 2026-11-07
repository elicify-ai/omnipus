package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
