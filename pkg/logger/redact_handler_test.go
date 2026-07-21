package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/logredact"
)

// newTestHandler builds a RedactingHandler wrapping a JSON handler that
// writes to buf, returning both the wrapped handler and the buffer so
// tests can assert on the rendered output.
func newTestHandler(t *testing.T, customPatterns []string) (*RedactingHandler, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	inner := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h, err := NewRedactingHandler(inner, customPatterns)
	if err != nil {
		t.Fatalf("NewRedactingHandler: %v", err)
	}
	return h, buf
}

// renderRecord calls h.Handle with a single record carrying the given attrs
// and returns the JSON-decoded map from the underlying buffer.
func renderRecord(t *testing.T, h *RedactingHandler, attrs ...slog.Attr) map[string]any {
	t.Helper()
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "test message", 0)
	rec.AddAttrs(attrs...)
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Each Handle writes one JSON line.
	line := strings.TrimSpace(h.inner.(interface{ Writer() *bytes.Buffer }).Writer().String())
	// Fallback: extract from the buffer indirectly via the test.
	_ = line
	return nil
}

func TestRedactingHandler_SecretFieldIsRedacted(t *testing.T) {
	h, buf := newTestHandler(t, nil)
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.String("token", "sk-abcdef0123456789abcdef"))
	rec.AddAttrs(slog.String("api_key", "key-abcdef0123456789abcd"))
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "sk-abcdef0123456789abcdef") {
		t.Errorf("expected token value to be redacted, got: %s", out)
	}
	if strings.Contains(out, "key-abcdef0123456789abcd") {
		t.Errorf("expected api_key value to be redacted, got: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] sentinel in output, got: %s", out)
	}
}

func TestRedactingHandler_NonSensitiveFieldPassesThrough(t *testing.T) {
	h, buf := newTestHandler(t, nil)
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.String("user_id", "u-1234"))
	rec.AddAttrs(slog.String("host", "example.com"))
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "u-1234") {
		t.Errorf("expected user_id to pass through, got: %s", out)
	}
	if !strings.Contains(out, "example.com") {
		t.Errorf("expected host to pass through, got: %s", out)
	}
}

func TestRedactingHandler_NestedGroupRedaction(t *testing.T) {
	h, buf := newTestHandler(t, nil)
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.Group("creds",
		slog.String("token", "ghp_abcdef0123456789abcdef0123456789abcd"),
		slog.String("username", "alice"),
	))
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "ghp_abcdef0123456789abcdef0123456789abcd") {
		t.Errorf("expected nested token to be redacted, got: %s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("expected username in nested group to pass through, got: %s", out)
	}
}

func TestRedactingHandler_BearerValuePattern(t *testing.T) {
	h, buf := newTestHandler(t, nil)
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.String("authorization", "Bearer abcdefghijklmnop"))
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	out := buf.String()
	// "authorization" is in the sensitive field-name set → redacted.
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected authorization to be redacted, got: %s", out)
	}
}

func TestRedactingHandler_CustomPattern(t *testing.T) {
	h, buf := newTestHandler(t, []string{`SECRET-[A-Z0-9]{6}`})
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.String("note", "the code is SECRET-ABC123 today"))
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "SECRET-ABC123") {
		t.Errorf("expected custom pattern to redact, got: %s", out)
	}
}

func TestRedactingHandler_NilInner(t *testing.T) {
	if _, err := NewRedactingHandler(nil, nil); err == nil {
		t.Errorf("expected error for nil inner handler")
	}
}

func TestRedactingHandler_DisabledRedactorPassesThrough(t *testing.T) {
	// DisabledRedactor should let everything through.
	buf := &bytes.Buffer{}
	inner := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	disabled := logredact.DisabledRedactor()
	h := &RedactingHandler{inner: inner, r: disabled}
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.String("token", "sk-abcdef0123456789abcdef"))
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(buf.String(), "sk-abcdef0123456789abcdef") {
		t.Errorf("expected disabled redactor to pass through, got: %s", buf.String())
	}
}

func TestRedactingHandler_WithAttrsPreRedacts(t *testing.T) {
	h, buf := newTestHandler(t, nil)
	pre := h.WithAttrs([]slog.Attr{slog.String("apiKey", "sk-abcdef0123456789")})
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.String("user", "alice"))
	if err := pre.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "sk-abcdef0123456789") {
		t.Errorf("expected pre-applied apiKey to be redacted, got: %s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("expected user to pass through, got: %s", out)
	}
}

func TestRedactingHandler_EnabledDelegatesToInner(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Errorf("expected handler to be enabled at Info")
	}
	disabledInner := slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError})
	hd, _ := NewRedactingHandler(disabledInner, nil)
	if hd.Enabled(context.Background(), slog.LevelInfo) {
		t.Errorf("expected handler to defer to disabled inner")
	}
}

func TestSanitizeForLog_StripsCRLF(t *testing.T) {
	if got := SanitizeForLog("hello\r\nworld"); got != "helloworld" {
		t.Errorf("expected CR/LF stripped, got: %q", got)
	}
}

func TestSanitizeForLog_StripsControlChars(t *testing.T) {
	in := "abc\x00\x01\x02def\x7f"
	want := "abcdef"
	if got := SanitizeForLog(in); got != want {
		t.Errorf("expected control chars stripped, got: %q", got)
	}
}

func TestSanitizeForLog_StripsAnsiEscapes(t *testing.T) {
	in := "\x1b[31mred\x1b[0m text"
	want := "red text"
	if got := SanitizeForLog(in); got != want {
		t.Errorf("expected ANSI stripped, got: %q", got)
	}
}

func TestSanitizeForLog_TruncatesLongString(t *testing.T) {
	long := strings.Repeat("a", maxLogStringLen+100)
	got := SanitizeForLog(long)
	if len(got) > maxLogStringLen {
		t.Errorf("expected length ≤ %d, got %d", maxLogStringLen, len(got))
	}
}

func TestSanitizeForLog_CleanStringPassesThrough(t *testing.T) {
	in := "normal text 123"
	if got := SanitizeForLog(in); got != in {
		t.Errorf("expected clean string to pass through unchanged, got: %q", got)
	}
}

func TestSanitizeForLog_EmptyString(t *testing.T) {
	if got := SanitizeForLog(""); got != "" {
		t.Errorf("expected empty string to pass through, got: %q", got)
	}
}

func TestSanitizeForLog_PreservesUnicode(t *testing.T) {
	in := "héllo 🌍"
	if got := SanitizeForLog(in); got != in {
		t.Errorf("expected unicode preserved, got: %q", got)
	}
}

// TestRedactingHandler_OutputIsValidJSON sanity-checks that the inner JSON
// handler still produces parseable JSON after redaction.
func TestRedactingHandler_OutputIsValidJSON(t *testing.T) {
	h, buf := newTestHandler(t, nil)
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.String("token", "sk-abc"))
	rec.AddAttrs(slog.Int("count", 7))
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}
	if m["token"] != "[REDACTED]" {
		t.Errorf("expected token=[REDACTED], got: %v", m["token"])
	}
	if m["count"] != float64(7) {
		t.Errorf("expected count=7, got: %v", m["count"])
	}
}
