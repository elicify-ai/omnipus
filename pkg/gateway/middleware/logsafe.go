// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package middleware — fix10 (CodeQL go/log-injection, CWE-117): middleware
// needs its own log-safe wrapper because it cannot import package gateway
// (import cycle) and had none of its own. Same contract as
// pkg/gateway/logsafe.go::safeLogString: newlines and carriage returns are
// escaped so a request-derived value cannot forge a log record ending.
package middleware

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

// logsafeWarn is the Warn-level entry point; the middleware package's only
// user-driven log sink today. Extend level by level as sites convert.
func logsafeWarn(msg string, args ...any) { slog.Warn(msg, safeLogArgs(args)...) }

// safeLogArgs escapes string and error values, keeping other kinds
// machine-readable.
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
