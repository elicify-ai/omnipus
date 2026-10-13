package logger

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Founder ruling R2-X-SEC-04: console escaping must also escape the Unicode
// bidirectional controls and the zero-width characters. They are invisible but a
// terminal (or a reader) acts on them: a bidi override reorders the visible text
// of a log line (the "Trojan Source" class), and a zero-width character hides a
// difference between two otherwise identical-looking values.
//
// ORACLE: the ruling's own code-point list, spelled out here as literals rather
// than derived from the implementation: U+202A-202E, U+2066-2069, U+200E/200F
// (bidirectional), U+200B-200D and U+FEFF (zero-width); plus, from R3-SEC-01,
// U+061C, U+2060-2064 and U+00AD.
var invisibleControlRunes = []rune{
	0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
	0x2066, 0x2067, 0x2068, 0x2069,
	0x200E, 0x200F,
	0x200B, 0x200C, 0x200D, 0xFEFF,
	// R3-SEC-01 (founder: fix now): the Arabic Letter Mark (a bidi control), the
	// word joiner and invisible operators U+2060-2064, and the soft hyphen.
	0x061C,
	0x2060, 0x2061, 0x2062, 0x2063, 0x2064,
	0x00AD,
}

func assertNoInvisibleControl(t *testing.T, where, got string) {
	t.Helper()
	for _, r := range got {
		for _, bad := range invisibleControlRunes {
			if r == bad {
				t.Errorf("%s: console output still carries the raw invisible control %U: %q", where, r, got)
				return
			}
		}
	}
}

func TestConsole_BidiAndZeroWidthControls_AreEscapedEverywhere(t *testing.T) {
	for _, r := range invisibleControlRunes {
		t.Run(fmt.Sprintf("U+%04X", r), func(t *testing.T) {
			raw := "a" + string(r) + "b"
			// The escaper writes \xNN below U+0100 (as it does for the C1 controls) and
			// \uNNNN above; the soft hyphen is the only member below U+0100.
			wantEscaped := fmt.Sprintf(`a\u%04xb`, r)
			if r < 0x100 {
				wantEscaped = fmt.Sprintf(`a\x%02xb`, r)
			}

			msg := captureConsole(t, func() { Warn("m:" + raw) })
			assertNoInvisibleControl(t, "message", msg)
			if !strings.Contains(msg, "m:"+wantEscaped) {
				t.Errorf("message must show the escape %q, not drop or keep the rune; got %q", wantEscaped, msg)
			}

			val := captureConsole(t, func() { InfoCF("test", "msg", map[string]any{"v": raw}) })
			assertNoInvisibleControl(t, "field value", val)
			if !strings.Contains(val, wantEscaped) {
				t.Errorf("field value must show the escape %q; got %q", wantEscaped, val)
			}

			er := captureConsole(t, func() { ErrorCF("test", "msg", map[string]any{"error": errors.New(raw)}) })
			assertNoInvisibleControl(t, "error value", er)

			key := captureConsole(t, func() { InfoCF("test", "msg", map[string]any{"k" + string(r): "x"}) })
			assertNoInvisibleControl(t, "field name", key)
		})
	}
}

// Instrument control: ordinary non-ASCII text next to the escaped set must not be
// touched - the escaping is a precise list, not "everything above U+007F".
func TestConsole_OrdinaryUnicodeTextIsUnchanged(t *testing.T) {
	const text = "zażółć gęślą jaźń - 日本語 - emoji \U0001F600 - ‐ dash"
	got := captureConsole(t, func() { Info(text) })
	if !strings.Contains(got, text) {
		t.Errorf("ordinary Unicode text must pass through unchanged; got %q", got)
	}
}
