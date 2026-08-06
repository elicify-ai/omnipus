package logger

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// captureSlogBridge enables file logging to a temp file at DEBUG level and
// returns a func that reads the file's current contents back as a string.
// Mirrors the EnableFileLogging test-capture pattern already established in
// pkg/gateway/capability_catalog_refresh_test.go and
// pkg/gateway/rest_executor_smoketest_rawlog_test.go — pkg/logger's console
// logger has no exported io.Writer sink, so the file logger is the only
// capturable output.
func captureSlogBridge(t *testing.T) func() string {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "slog-bridge-test.log")
	prevLevel := GetLevel()
	DisableConsole()
	SetLevel(DEBUG)
	if err := EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		DisableFileLogging()
		SetLevel(prevLevel)
	})
	return func() string {
		data, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		return string(data)
	}
}

func TestSlogHandler_Handle_MessageAndFieldsSurvive(t *testing.T) {
	readLog := captureSlogBridge(t)

	l := slog.New(NewSlogHandler())
	l.Warn("bridge test message", "user_id", "u-123", "count", 42)

	logged := readLog()
	if !contains(logged, "bridge test message") {
		t.Errorf("message missing from log output: %q", logged)
	}
	if !contains(logged, `"user_id":"u-123"`) {
		t.Errorf("string field missing/malformed: %q", logged)
	}
	if !contains(logged, `"count":42`) {
		t.Errorf("int field missing/malformed: %q", logged)
	}
	if !contains(logged, `"level":"warn"`) {
		t.Errorf("expected warn level, got: %q", logged)
	}
}

func TestSlogHandler_Handle_ErrorAttrUsesErrorString(t *testing.T) {
	readLog := captureSlogBridge(t)

	l := slog.New(NewSlogHandler())
	l.Error("bridge test error", "error", errors.New("boom-slog-bridge"))

	logged := readLog()
	if !contains(logged, "boom-slog-bridge") {
		t.Errorf("error attribute's message missing: %q", logged)
	}
	if !contains(logged, `"level":"error"`) {
		t.Errorf("expected error level, got: %q", logged)
	}
}

// TestSlogHandler_LevelMapping proves the four-way DEBUG/INFO/WARN/ERROR
// mapping, including that a level ABOVE slog.LevelError (a custom/offset
// level, e.g. what some libraries use for a "fatal"-flavored slog call)
// still caps at pkg/logger's ERROR and never reaches FATAL (which would
// os.Exit — asserting that indirectly by confirming the process is still
// running to make this assertion at all, plus the logged level string).
func TestSlogHandler_LevelMapping(t *testing.T) {
	readLog := captureSlogBridge(t)
	l := slog.New(NewSlogHandler())

	l.Debug("lvl-debug-marker")
	l.Info("lvl-info-marker")
	l.Warn("lvl-warn-marker")
	l.Error("lvl-error-marker")
	l.Log(context.Background(), slog.LevelError+4, "lvl-superror-marker") // custom level above Error

	logged := readLog()
	cases := []struct {
		marker string
		level  string
	}{
		{"lvl-debug-marker", `"level":"debug"`},
		{"lvl-info-marker", `"level":"info"`},
		{"lvl-warn-marker", `"level":"warn"`},
		{"lvl-error-marker", `"level":"error"`},
		{"lvl-superror-marker", `"level":"error"`}, // capped at ERROR, never FATAL
	}
	for _, c := range cases {
		idx := indexOf(logged, c.marker)
		if idx < 0 {
			t.Errorf("marker %q not found in log output", c.marker)
			continue
		}
		// The level field precedes the message on the same JSON line; just
		// confirm the expected level string appears somewhere on that line.
		lineStart := lastIndexOfNewlineBefore(logged, idx)
		lineEnd := indexOfNewlineAfter(logged, idx)
		line := logged[lineStart:lineEnd]
		if !contains(line, c.level) {
			t.Errorf("marker %q: expected level %q on its line, got: %q", c.marker, c.level, line)
		}
	}
}

// TestSlogHandler_Enabled_RespectsLoggerLevel proves Enabled defers to
// pkg/logger's own currentLevelAtomic threshold rather than always
// returning true — raising the level to WARN must suppress Debug/Info.
func TestSlogHandler_Enabled_RespectsLoggerLevel(t *testing.T) {
	readLog := captureSlogBridge(t)
	SetLevel(WARN)

	l := slog.New(NewSlogHandler())
	l.Debug("should-not-appear-debug")
	l.Info("should-not-appear-info")
	l.Warn("should-appear-warn")

	logged := readLog()
	if contains(logged, "should-not-appear-debug") {
		t.Errorf("debug line should have been suppressed at WARN threshold: %q", logged)
	}
	if contains(logged, "should-not-appear-info") {
		t.Errorf("info line should have been suppressed at WARN threshold: %q", logged)
	}
	if !contains(logged, "should-appear-warn") {
		t.Errorf("warn line should have passed at WARN threshold: %q", logged)
	}
}

// TestSlogHandler_WithAttrs_BoundFieldsSurvive proves attrs bound via
// Logger.With (Handler.WithAttrs) are carried into every subsequent Handle
// call from that derived logger, without mutating the parent.
func TestSlogHandler_WithAttrs_BoundFieldsSurvive(t *testing.T) {
	readLog := captureSlogBridge(t)

	base := slog.New(NewSlogHandler())
	child := base.With("request_id", "req-789")
	child.Info("child logger message")
	base.Info("base logger message")

	logged := readLog()
	if !contains(logged, `"request_id":"req-789"`) {
		t.Errorf("bound attr from With() missing: %q", logged)
	}
	// The base logger (parent) must NOT have picked up the child's bound attr.
	baseIdx := indexOf(logged, "base logger message")
	if baseIdx < 0 {
		t.Fatalf("base logger message missing entirely: %q", logged)
	}
	lineStart := lastIndexOfNewlineBefore(logged, baseIdx)
	lineEnd := indexOfNewlineAfter(logged, baseIdx)
	baseLine := logged[lineStart:lineEnd]
	if contains(baseLine, "req-789") {
		t.Errorf("parent logger must not be mutated by a child's With(): %q", baseLine)
	}
}

// TestSlogHandler_WithGroup_PrefixesKeys proves WithGroup dot-prefixes both
// pre-bound attrs added after the group and the record's own attrs.
func TestSlogHandler_WithGroup_PrefixesKeys(t *testing.T) {
	readLog := captureSlogBridge(t)

	l := slog.New(NewSlogHandler()).WithGroup("http").With("method", "GET")
	l.Info("grouped request", "status", 200)

	logged := readLog()
	if !contains(logged, `"http.method":"GET"`) {
		t.Errorf("group-prefixed bound attr missing: %q", logged)
	}
	if !contains(logged, `"http.status":200`) {
		t.Errorf("group-prefixed record attr missing: %q", logged)
	}
}

// TestSlogHandler_NestedGroups_DotJoined proves slog.Group values (both
// inline via a logging call and via nested WithGroup) flatten to dot-joined
// keys rather than being dropped or serialized as an opaque struct.
func TestSlogHandler_NestedGroups_DotJoined(t *testing.T) {
	readLog := captureSlogBridge(t)

	l := slog.New(NewSlogHandler())
	l.Info("nested group test",
		slog.Group("request",
			slog.String("id", "req-1"),
			slog.Group("client", slog.String("ip", "10.0.0.1")),
		),
	)

	logged := readLog()
	if !contains(logged, `"request.id":"req-1"`) {
		t.Errorf("first-level group key missing: %q", logged)
	}
	if !contains(logged, `"request.client.ip":"10.0.0.1"`) {
		t.Errorf("nested group key missing: %q", logged)
	}
}

// TestSlogHandler_AnonymousGroup_Inlines proves a group with an empty key
// (slog.Group("", ...)) inlines its attrs without adding a key prefix, per
// slog's own documented Handler convention.
func TestSlogHandler_AnonymousGroup_Inlines(t *testing.T) {
	readLog := captureSlogBridge(t)

	l := slog.New(NewSlogHandler())
	l.Info("anonymous group test", slog.Group("", slog.String("inlined_key", "v1")))

	logged := readLog()
	if !contains(logged, `"inlined_key":"v1"`) {
		t.Errorf("anonymous group attr should inline without a prefix: %q", logged)
	}
	if contains(logged, `..inlined_key`) || contains(logged, `".inlined_key"`) {
		t.Errorf("anonymous group must not add a leading-dot prefix: %q", logged)
	}
}

// TestSlogHandler_EmptyGroup_Omitted proves a group with zero attributes is
// omitted entirely (matches slog's own documented behavior), rather than
// emitting an empty/garbage field.
func TestSlogHandler_EmptyGroup_Omitted(t *testing.T) {
	readLog := captureSlogBridge(t)

	l := slog.New(NewSlogHandler())
	l.Info("empty group test", slog.Group("empty"), slog.String("real_key", "v2"))

	logged := readLog()
	if !contains(logged, `"real_key":"v2"`) {
		t.Errorf("sibling real attr should still be present: %q", logged)
	}
	if contains(logged, "empty.") {
		t.Errorf("an empty group must not emit any field: %q", logged)
	}
}

// TestSlogHandler_LogValuer_Resolved proves a slog.LogValuer value (e.g. a
// redacting wrapper type some libraries use for secrets) is resolved to its
// underlying value rather than logged as an opaque struct dump.
func TestSlogHandler_LogValuer_Resolved(t *testing.T) {
	readLog := captureSlogBridge(t)

	l := slog.New(NewSlogHandler())
	l.Info("logvaluer test", slog.Any("secret", redactedValuer{plain: "resolved-value"}))

	logged := readLog()
	if !contains(logged, `"secret":"resolved-value"`) {
		t.Errorf("LogValuer must be resolved before logging: %q", logged)
	}
}

type redactedValuer struct{ plain string }

func (r redactedValuer) LogValue() slog.Value { return slog.StringValue(r.plain) }

// TestSlogHandler_TimeAndDurationKinds proves slog.Time/slog.Duration
// values convert to their native Go types (time.Time/time.Duration) rather
// than an unhelpful string dump, so pkg/logger's Interface fallback
// serializes them properly.
func TestSlogHandler_TimeAndDurationKinds(t *testing.T) {
	readLog := captureSlogBridge(t)

	fixedTime := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	l := slog.New(NewSlogHandler())
	l.Info("time/duration test",
		slog.Time("started_at", fixedTime),
		slog.Duration("elapsed", 2*time.Second),
	)

	logged := readLog()
	if !contains(logged, "2026-07-25T12:00:00Z") {
		t.Errorf("time.Time value not preserved: %q", logged)
	}
	if !contains(logged, "2000000000") && !contains(logged, "2s") {
		t.Errorf("duration value not preserved in some recognizable form: %q", logged)
	}
}

// --- tiny string helpers (avoid importing strings just for Contains/Index
// in a file that otherwise has no other need for it, keeping the test
// self-contained and easy to audit) ---

func contains(haystack, needle string) bool {
	return indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	n := len(needle)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(haystack); i++ {
		if haystack[i:i+n] == needle {
			return i
		}
	}
	return -1
}

func lastIndexOfNewlineBefore(s string, idx int) int {
	for i := idx; i > 0; i-- {
		if s[i-1] == '\n' {
			return i
		}
	}
	return 0
}

func indexOfNewlineAfter(s string, idx int) int {
	for i := idx; i < len(s); i++ {
		if s[i] == '\n' {
			return i
		}
	}
	return len(s)
}
