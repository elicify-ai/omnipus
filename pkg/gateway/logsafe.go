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

// safeLogArgs makes request-derived values safe for structured logs: strings,
// errors, []string slices, and fmt.Stringer values all end in safeLogString —
// no user-supplied value can forge a log record ending (fix11, CodeQL
// go/log-injection) — while numbers and booleans stay machine-readable.
func safeLogArgs(args ...any) []any {
	safe := make([]any, 0, len(args))
	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			safe = append(safe, value)
		case string:
			safe = append(safe, safeLogString(value))
		case error:
			safe = append(safe, safeLogString(value.Error()))
		case []string:
			safe = append(safe, safeLogString(strings.Join(value, ", ")))
		case fmt.Stringer:
			safe = append(safe, safeLogString(value.String()))
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
