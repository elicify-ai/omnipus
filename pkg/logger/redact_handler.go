// Package logger: runtime log redaction (SEC-16).
//
// The audit pipeline runs every audit entry through pkg/logredact.Redactor
// before it lands in the JSONL. The same Redactor is now also wired into
// the runtime slog pipeline via RedactingHandler, so secrets that accidentally
// reach a runtime log line (token, apiKey, secret, bearer, etc.) are
// replaced with [REDACTED] before the line is written.
//
// SanitizeForLog provides a small text-cleanup helper for user-controlled
// strings (hostnames, chat IDs, channel names, LLM-supplied commands).
// It strips newlines, control characters, and ANSI escapes, and truncates
// long values. Use it at the call site for the go/log-injection CodeQL rule;
// inline strings.ReplaceAll(s, "\n", "") / strings.ReplaceAll(s, "\r", "") at
// the call site is also recognized by the rule and is the canonical fix.
package logger

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/logredact"
)

// RedactingHandler is a slog.Handler that runs the canonical logredact
// Redactor over every record's attributes before delegating to an inner
// handler. The redaction is identical to the audit pipeline's: field-name
// match (case-insensitive, underscore/dash-stripped) replaces with
// [REDACTED]; value-pattern match (sk-, key-, Bearer, ghp_, gho_, xoxb-,
// xoxp-, AKIA, ASIA, JWTs, ya29., emails) replaces with [REDACTED] in any
// string value.
//
// Construct via NewRedactingHandler.
type RedactingHandler struct {
	inner slog.Handler
	r     *logredact.Redactor
}

// NewRedactingHandler wraps inner with the canonical Redactor. customPatterns
// adds extra value-level regex patterns on top of the defaults; pass nil to
// use the defaults only.
func NewRedactingHandler(inner slog.Handler, customPatterns []string) (*RedactingHandler, error) {
	if inner == nil {
		return nil, errNilInnerHandler
	}
	r, err := logredact.NewRedactor(customPatterns)
	if err != nil {
		return nil, err
	}
	return &RedactingHandler{inner: inner, r: r}, nil
}

// errNilInnerHandler is returned by NewRedactingHandler when inner is nil.
var errNilInnerHandler = &redactHandlerError{msg: "redact handler: inner handler is nil"}

type redactHandlerError struct{ msg string }

func (e *redactHandlerError) Error() string { return e.msg }

// Enabled reports whether the inner handler is enabled at level.
func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle redacts every attribute in record and forwards to the inner handler.
// record.Attrs is a method in Go 1.22+ (returns a callback to walk attrs);
// we collect them into a slice, redact, then build a new record to pass
// downstream. Time, Level, Message, and PC are preserved.
//
// The message itself is also run through the redactor: a secret-shaped
// substring in the message (e.g. "auth failed for sk-abc...") is replaced
// with [REDACTED].
func (h *RedactingHandler) Handle(ctx context.Context, record slog.Record) error {
	redacted := redactAttrsFromRecord(h.r, record)
	nr := slog.NewRecord(record.Time, record.Level, h.r.Redact(record.Message), record.PC)
	nr.AddAttrs(redacted...)
	return h.inner.Handle(ctx, nr)
}

// WithAttrs returns a handler with the given attrs pre-applied and redacted.
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RedactingHandler{
		inner: h.inner.WithAttrs(redactAttrs(h.r, attrs)),
		r:     h.r,
	}
}

// WithGroup returns a handler with the given group name.
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{
		inner: h.inner.WithGroup(name),
		r:     h.r,
	}
}

// redactAttrsFromRecord walks a slog.Record's attributes via its callback
// and returns a redacted slice.
func redactAttrsFromRecord(r *logredact.Redactor, record slog.Record) []slog.Attr {
	var out []slog.Attr
	record.Attrs(func(a slog.Attr) bool {
		out = append(out, redactAttr(r, a))
		return true
	})
	return out
}

// redactAttrs walks a []slog.Attr slice and returns a new slice with values
// passed through the Redactor.
func redactAttrs(r *logredact.Redactor, attrs []slog.Attr) []slog.Attr {
	if len(attrs) == 0 {
		return attrs
	}
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, redactAttr(r, a))
	}
	return out
}

// redactAttr redacts a single slog.Attr. Groups are recursed into so the
// key context is preserved at each level. The group key itself is also
// run through the field-name layer: a group named "token" or "apiKey"
// is sensitive regardless of its children.
func redactAttr(r *logredact.Redactor, a slog.Attr) slog.Attr {
	// Sensitive-key check on the group/attr key itself.
	if r.RedactField(a.Key, "__redact_marker__") == logredact.RedactedValue {
		return slog.Attr{Key: a.Key, Value: slog.AnyValue(logredact.RedactedValue)}
	}
	if a.Value.Kind() == slog.KindGroup {
		children := a.Value.Group()
		redactedChildren := redactAttrs(r, children)
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(redactedChildren...)}
	}
	redacted := r.RedactField(a.Key, a.Value.Any())
	return slog.Any(a.Key, redacted)
}

// maxLogStringLen is the longest string we let pass into a log line. Longer
// values are truncated. Chosen to keep a single log line below ~1 KB even
// with several long fields. Operators can find the full payload via the
// audit log when one exists.
const maxLogStringLen = 256

// SanitizeForLog cleans a user-controlled string for inclusion in a log line.
// It strips newlines, control characters, and ANSI escapes, and truncates
// the result to maxLogStringLen. If the input is already clean, the original
// string is returned (no allocation).
//
// Note: this helper is for cosmetic cleanup of user-controlled strings. The
// canonical fix for the go/log-injection CodeQL rule is inline
// `strings.ReplaceAll(s, "\n", "")` and `strings.ReplaceAll(s, "\r", "")`
// at the call site — that pattern is recognized by the rule without any
// QL model. SanitizeForLog handles the additional cleanup (control chars,
// ANSI, length cap) that the rule does not require.
func SanitizeForLog(s string) string {
	if s == "" {
		return s
	}
	needs := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			needs = true
			break
		}
		if r == 0x1b {
			needs = true
			break
		}
	}
	if !needs && len(s) <= maxLogStringLen {
		return s
	}
	// Replace common ANSI CSI sequences (color, cursor, etc.) with empty.
	// Matches ESC [ <digits/semicolons> <letter>. e.g. "\x1b[31m" or "\x1b[0m".
	s = ansiCSI.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= maxLogStringLen {
			break
		}
	}
	return b.String()
}

// ansiCSI matches ANSI CSI escape sequences: ESC [ <digits/semicolons>+ <letter>.
// Compiled once at package init for the cost of one allocation per call.
var ansiCSI = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// installOnce ensures InstallSlogRedactor is idempotent across the
// multiple init() sites (cmd/omnipus/main.go and cmd/omnipus/internal/gateway).
var installOnce sync.Once

// installErr captures the result of the first install; subsequent calls
// return the same handler.
var installedHandler *RedactingHandler
var installErr error

// InstallSlogRedactor installs a redacting slog.Handler as the process-wide
// default slog logger. After this call, every `slog.Info`, `slog.Warn`, etc.
// that does not pass a custom *slog.Logger will flow through the redaction
// wrapper. Returns the installed handler so callers can compose further.
//
// Idempotent: safe to call multiple times. The first call wins; later calls
// return the same handler and a nil error.
//
// Use this once at process startup, before any goroutine begins logging.
func InstallSlogRedactor(customPatterns []string) (*RedactingHandler, error) {
	installOnce.Do(func() {
		base := slog.Default()
		wrapped, err := NewRedactingHandler(base.Handler(), customPatterns)
		if err != nil {
			installErr = err
			return
		}
		slog.SetDefault(slog.New(wrapped))
		installedHandler = wrapped
	})
	return installedHandler, installErr
}
