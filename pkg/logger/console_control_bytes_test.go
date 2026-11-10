package logger

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// CodeQL triage (CODEQL-PR1260-triage.md, class A, residual gaps): a value that
// carries a carriage return or a terminal escape byte must not forge or colour a
// console line. ORACLE: the triage's wording - CR and ESC/control bytes are
// escaped in console field values AND in the message text (the file sink is
// zerolog JSON, which already escapes them). The assertions are on the bytes a
// terminal would act on, not on the formatter's internals.

// captureConsole runs fn with the global console logger writing to a buffer.
func captureConsole(t *testing.T, fn func()) string {
	t.Helper()
	var out bytes.Buffer
	w := newConsoleWriter(&out)
	w.NoColor = true
	w.TimeLocation = time.UTC

	mu.Lock()
	prev := logger
	logger = zerolog.New(w)
	mu.Unlock()
	prevLevel := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	t.Cleanup(func() {
		mu.Lock()
		logger = prev
		mu.Unlock()
		zerolog.SetGlobalLevel(prevLevel)
	})
	fn()
	return out.String()
}

// assertTerminalSafe fails when the output holds a byte a terminal would act on
// (anything below 0x20 except the single event-terminating newline, DEL, or a
// C1 control) or more than one line.
func assertTerminalSafe(t *testing.T, got string) {
	t.Helper()
	if n := strings.Count(got, "\n"); n != 1 || !strings.HasSuffix(got, "\n") {
		t.Errorf("one log call must be exactly one console line; got %d newlines: %q", n, got)
	}
	body := strings.TrimSuffix(got, "\n")
	for _, r := range body {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("console output contains control rune %U: %q", r, got)
			return
		}
	}
}

func TestConsole_FieldValueWithCRAndESCCannotForgeOrColour(t *testing.T) {
	// No spaces: the unquoted branch of formatFieldValue.
	got := captureConsole(t, func() {
		WarnCF("test", "request refused", map[string]any{"path": "/x\r12:00:00ERRforged\x1b[31mred"})
	})
	assertTerminalSafe(t, got)
	if !strings.Contains(got, `\r`) || !strings.Contains(got, `\x1b`) {
		t.Errorf("the escapes must stay visible to the reader; got %q", got)
	}
}

func TestConsole_FieldValueWithSpacesAndBracesCannotSmuggleControlBytes(t *testing.T) {
	// A brace-delimited value with a space took a raw-return branch.
	got := captureConsole(t, func() {
		WarnCF("test", "request refused", map[string]any{"body": "{not json \x1b[2J\rboom}"})
	})
	assertTerminalSafe(t, got)
}

func TestConsole_QuotedEscapeTextIsNotDecodedBackIntoARealEscape(t *testing.T) {
	// The attacker supplies the literal text "\x1b[31m" WITH quotes; the
	// formatter's strconv.Unquote would decode it into a real ESC byte.
	got := captureConsole(t, func() {
		WarnCF("test", "request refused", map[string]any{"ua": `"\x1b[31mred"`})
	})
	assertTerminalSafe(t, got)
}

func TestConsole_MultilineJSONFieldKeepsNewlinesButNoOtherControlBytes(t *testing.T) {
	// A valid multi-line JSON document may keep its newlines (readable output);
	// a CR used as JSON whitespace must still not reach the terminal.
	got := captureConsole(t, func() {
		WarnCF("test", "payload", map[string]any{"doc": "{\r\n\"a\": 1\r\n}"})
	})
	if strings.ContainsAny(got, "\r\x1b") {
		t.Errorf("CR/ESC reached the console: %q", got)
	}
}

func TestConsole_MessageTextWithCRAndESCCannotForgeOrColour(t *testing.T) {
	got := captureConsole(t, func() {
		Warn("bad request\r12:00:00 ERR forged line\x1b[31m red")
	})
	assertTerminalSafe(t, got)
	if !strings.Contains(got, `bad request\r12:00:00 ERR forged line\x1b[31m red`) {
		t.Errorf("the message must be shown escaped, not dropped; got %q", got)
	}
}

func TestConsole_ErrorFieldWithCRAndESCCannotForgeOrColour(t *testing.T) {
	got := captureConsole(t, func() {
		ErrorCF("test", "save failed", map[string]any{"error": errors.New("disk\rfull\x1b[0m")})
	})
	assertTerminalSafe(t, got)
}

func TestConsole_OrdinaryTextIsUnchanged(t *testing.T) {
	// Instrument control: harmless text, tabs excluded from the contract, must
	// not be altered by the escaping.
	got := captureConsole(t, func() {
		InfoCF("test", "hello world", map[string]any{"user": "alice", "note": "two words"})
	})
	want := "hello world"
	if !strings.Contains(got, want) || !strings.Contains(got, "user=alice") || !strings.Contains(got, `note="two words"`) {
		t.Errorf("ordinary text must be untouched; got %q", got)
	}
	assertTerminalSafe(t, got)
}

// The file sink is zerolog JSON; pin that it never carries a raw CR/ESC either.
func TestFileSink_NeverCarriesRawControlBytes(t *testing.T) {
	var out bytes.Buffer
	fl := zerolog.New(&out)
	writeEvent(fl, zerolog.WarnLevel, "test", map[string]any{"path": "/x\r\x1b[31m"}, 0, "msg\r\x1b")
	if strings.ContainsAny(out.String(), "\r\x1b") {
		t.Errorf("file sink carries raw control bytes: %q", out.String())
	}
}
