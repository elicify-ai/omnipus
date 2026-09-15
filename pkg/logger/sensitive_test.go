package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logToTempFile points file logging at a temp file for the test and returns a
// reader for the last JSON line written.
func logToTempFile(t *testing.T) (readAll func() string, lastLine func() map[string]any) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.log")
	if err := EnableFileLogging(path); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(DisableFileLogging)
	readAll = func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(b)
	}
	lastLine = func() map[string]any {
		lines := bytes.Split(bytes.TrimSpace([]byte(readAll())), []byte("\n"))
		var got map[string]any
		if err := json.Unmarshal(lines[len(lines)-1], &got); err != nil {
			t.Fatalf("unmarshal log line %q: %v", lines[len(lines)-1], err)
		}
		return got
	}
	return readAll, lastLine
}

type scrubPayload struct {
	Body string `json:"body"`
}

// Given a registered credential
// When a log line carries it in its message and in every field shape log call
// sites pass
// Then the written line carries the scrub marker in its place, keeps every
// field's shape and every other value, and leaves the caller's map untouched.
func TestLogMessage_ScrubsRegisteredCredentialFromMessageAndFields(t *testing.T) {
	const secret = "sk-live-LOGGER-5Hq81z"
	t.Cleanup(SetSensitiveValueReplacer(strings.NewReplacer(secret, "[FILTERED]")))
	readAll, lastLine := logToTempFile(t)

	fields := map[string]any{
		"error":  errors.New("provider error: body=" + secret),
		"text":   "key " + secret,
		"list":   []string{"a", secret},
		"nested": map[string]any{"body": secret, "n": 3},
		"struct": scrubPayload{Body: "echo " + secret},
		"bytes":  []byte("raw " + secret),
		"count":  7,
	}
	ErrorCF("test", "call failed for "+secret, fields)

	if out := readAll(); strings.Contains(out, secret) {
		t.Fatalf("the log carries the registered credential:\n%s", out)
	}
	got := lastLine()
	want := map[string]any{
		"message": "call failed for [FILTERED]",
		"error":   "provider error: body=[FILTERED]",
		"text":    "key [FILTERED]",
		"bytes":   "raw [FILTERED]",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("field %q = %#v, want %#v", k, got[k], v)
		}
	}
	if list, ok := got["list"].([]any); !ok || len(list) != 2 || list[0] != "a" || list[1] != "[FILTERED]" {
		t.Errorf("list = %#v, want [a [FILTERED]]", got["list"])
	}
	if nested, ok := got["nested"].(map[string]any); !ok || nested["body"] != "[FILTERED]" || nested["n"] != float64(3) {
		t.Errorf("nested = %#v, want body scrubbed and n kept", got["nested"])
	}
	if st, ok := got["struct"].(map[string]any); !ok || st["body"] != "echo [FILTERED]" {
		t.Errorf("struct = %#v, want a JSON object with its body scrubbed", got["struct"])
	}
	if got["count"] != float64(7) {
		t.Errorf("count = %#v, want 7", got["count"])
	}
	if fields["text"] != "key "+secret {
		t.Errorf("the caller's map was modified: text = %#v", fields["text"])
	}
}

// Control: with no replacer installed the same line is written as given, so
// the absence above is the replacer's doing.
func TestLogMessage_NoRegisteredCredentialWritesTheLineAsGiven(t *testing.T) {
	const value = "sk-live-LOGGER-CONTROL-2w"
	t.Cleanup(SetSensitiveValueReplacer(nil))
	_, lastLine := logToTempFile(t)

	WarnCF("test", "call failed for "+value, map[string]any{"text": "key " + value})

	got := lastLine()
	if got["message"] != "call failed for "+value || got["text"] != "key "+value {
		t.Errorf("line = %#v, want message and text written as given", got)
	}
}

// TestScrubSensitiveValues_FollowsTheInstalledReplacer: nothing installed
// leaves text as given; an installed replacer scrubs at any length; restore
// puts back the replacer that was installed before.
func TestScrubSensitiveValues_FollowsTheInstalledReplacer(t *testing.T) {
	t.Cleanup(SetSensitiveValueReplacer(nil))
	if got := ScrubSensitiveValues("abcd"); got != "abcd" {
		t.Fatalf("with nothing installed got %q, want the text as given", got)
	}
	first := strings.NewReplacer("abcd", "[FILTERED]")
	SetSensitiveValueReplacer(first)
	restore := SetSensitiveValueReplacer(strings.NewReplacer("wxyz", "[FILTERED]"))
	if got := ScrubSensitiveValues("abcd wxyz"); got != "abcd [FILTERED]" {
		t.Errorf("got %q, want only the installed replacer's value scrubbed", got)
	}
	restore()
	if got := ScrubSensitiveValues("abcd wxyz"); got != "[FILTERED] wxyz" {
		t.Errorf("after restore got %q, want the previous replacer back", got)
	}
}
