package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func newTestJSONLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestRedactSlogArgs_SecretKeyRedacted(t *testing.T) {
	out := RedactSlogArgs([]any{"token", "sk-abcdef0123456789", "user", "alice"})
	// Even-indexed: keys. Odd-indexed: redacted values.
	if out[0] != "token" {
		t.Errorf("expected key=token, got: %v", out[0])
	}
	if out[1] != "[REDACTED]" {
		t.Errorf("expected token value [REDACTED], got: %v", out[1])
	}
	if out[2] != "user" {
		t.Errorf("expected key=user, got: %v", out[2])
	}
	if out[3] != "alice" {
		t.Errorf("expected user value alice, got: %v", out[3])
	}
}

func TestRedactSlogArgs_NonSensitiveKeyPassesThrough(t *testing.T) {
	out := RedactSlogArgs([]any{"host", "example.com", "count", 7})
	if out[1] != "example.com" {
		t.Errorf("expected host=example.com, got: %v", out[1])
	}
	if out[3] != 7 {
		t.Errorf("expected count=7, got: %v", out[3])
	}
}

func TestRedactSlogArgs_EmptyAndOddLength(t *testing.T) {
	if got := RedactSlogArgs(nil); len(got) != 0 {
		t.Errorf("expected empty for nil, got: %v", got)
	}
	// Odd length: trailing key without a value is preserved.
	out := RedactSlogArgs([]any{"trailing"})
	if len(out) != 1 || out[0] != "trailing" {
		t.Errorf("expected [trailing], got: %v", out)
	}
}

func TestRedactSlogArgs_BearerValuePattern(t *testing.T) {
	// The "note" key is not in the sensitive-field set, so the value falls
	// through to the value-pattern layer. The Bearer pattern inside the
	// string should be replaced with [REDACTED].
	out := RedactSlogArgs([]any{"note", "the header is Bearer abcdef1234567890xyz"})
	got, ok := out[1].(string)
	if !ok {
		t.Fatalf("expected string, got: %T", out[1])
	}
	if strings.Contains(got, "Bearer abcdef1234567890xyz") {
		t.Errorf("expected Bearer pattern redacted, got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected [REDACTED] sentinel in output, got: %q", got)
	}
}

func TestSlogInfo_RedactsArgsBeforeLogging(t *testing.T) {
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(newTestJSONLogger(buf))
	defer slog.SetDefault(prev)

	SlogInfo("user login", "token", "sk-abcdef0123456789", "user_id", "u-1234")
	out := buf.String()
	if strings.Contains(out, "sk-abcdef0123456789") {
		t.Errorf("expected token value to be redacted, got: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in output, got: %s", out)
	}
	if !strings.Contains(out, "u-1234") {
		t.Errorf("expected user_id to pass through, got: %s", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
}

func TestSlogWarn_OutputIsValid(t *testing.T) {
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(newTestJSONLogger(buf))
	defer slog.SetDefault(prev)

	SlogWarn("warn msg", "session_id", "sess-abc", "apiKey", "key-abcdef0123456789")
	out := buf.String()
	if strings.Contains(out, "key-abcdef0123456789") {
		t.Errorf("expected apiKey redacted, got: %s", out)
	}
}

func TestRedactLogString_StripsCRLF(t *testing.T) {
	if got := RedactLogString("hello\r\nworld"); got != "helloworld" {
		t.Errorf("expected CR/LF stripped, got: %q", got)
	}
}

func TestRedactLogString_StripsAnsi(t *testing.T) {
	if got := RedactLogString("\x1b[31mred\x1b[0m text"); got != "red text" {
		t.Errorf("expected ANSI stripped, got: %q", got)
	}
}

func TestRedactLogString_Truncates(t *testing.T) {
	long := strings.Repeat("a", maxLogStringLen+50)
	got := RedactLogString(long)
	if len(got) > maxLogStringLen {
		t.Errorf("expected ≤ %d chars, got %d", maxLogStringLen, len(got))
	}
}

func TestSlogLogString_PreservesUnicode(t *testing.T) {
	if got := SlogLogString("héllo 🌍"); got != "héllo 🌍" {
		t.Errorf("expected unicode preserved, got: %q", got)
	}
}

func TestSlogLogString_StripsNewlinesBeforeSanitize(t *testing.T) {
	if got := SlogLogString("a\nb\rc"); got != "abc" {
		t.Errorf("expected CR/LF stripped first, got: %q", got)
	}
}
