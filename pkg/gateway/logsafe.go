package gateway

import (
	"log/slog"
	"strings"
)

// safeLogString removes line breaks that could forge new structured-log
// records while preserving their escaped diagnostic content.
func safeLogString(value string) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	return strings.ReplaceAll(value, "\r", "\\r")
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

func logsafeDebug(msg string, args ...any) { slog.Debug(msg, safeLogArgs(args)...) }
func logsafeInfo(msg string, args ...any)  { slog.Info(msg, safeLogArgs(args)...) }
func logsafeWarn(msg string, args ...any)  { slog.Warn(msg, safeLogArgs(args)...) }
func logsafeError(msg string, args ...any) { slog.Error(msg, safeLogArgs(args)...) }
