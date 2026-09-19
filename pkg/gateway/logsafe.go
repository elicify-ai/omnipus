package gateway

import (
	"fmt"
	"log/slog"
)

// logsafe makes request-derived strings and errors safe for structured logs by
// encoding them as quoted Go strings. This preserves diagnostic content while
// preventing spoofed log endings and keeps non-string values (IDs, counts,
// booleans) machine-readable.
func safeLogArgs(args ...any) []any {
	safe := make([]any, 0, len(args))
	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			safe = append(safe, fmt.Sprintf("%q", value))
		case error:
			safe = append(safe, fmt.Sprintf("%q", value.Error()))
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
