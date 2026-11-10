package gateway

import (
	"fmt"
	"log/slog"
	"strings"
)

// safeLogString removes line breaks that could forge new structured-log
// records while preserving their escaped diagnostic content.
func safeLogString(value string) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	return strings.ReplaceAll(value, "\r", "\\r")
}

// safeLogValue quotes value with Go %q so a slog attribute cannot contain a
// raw ASCII control (U+0000-U+001F or U+007F). Printable text stays readable
// inside the quotes. The result is for log records only: do not return it to
// a client, and do not wrap it into an error that is returned to a caller.
func safeLogValue(value string) string {
	return fmt.Sprintf("%q", value)
}

// logsafe makes request-derived strings and errors safe for structured logs.
// This preserves diagnostic content while preventing spoofed log endings and
// keeps non-string values (IDs, counts, booleans) machine-readable.
func safeLogArgs(args ...any) []any {
	safe := make([]any, 0, len(args))
	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			safe = append(safe, safeLogString(value))
		case error:
			safe = append(safe, safeLogString(value.Error()))
		default:
			safe = append(safe, value)
		}
	}
	return safe
}

func logsafeDebug(msg string, args ...any) { slog.Debug(msg, safeLogArgs(args...)...) }
func logsafeInfo(msg string, args ...any)  { slog.Info(msg, safeLogArgs(args...)...) }
func logsafeWarn(msg string, args ...any)  { slog.Warn(msg, safeLogArgs(args...)...) }
func logsafeError(msg string, args ...any) { slog.Error(msg, safeLogArgs(args...)...) }
