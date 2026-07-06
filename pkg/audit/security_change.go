// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

// EventSecuritySettingChange is the audit event emitted when an admin mutates a
// security-relevant config key.
const EventSecuritySettingChange = "security_setting_change"

// SecurityChangeRecord is the canonical wire shape for the
// security_setting_change audit event. Exported so tests and future
// downstream consumers can decode the JSONL into a typed value rather
// than map[string]any.
type SecurityChangeRecord struct {
	Timestamp string `json:"timestamp"`
	Event     string `json:"event"`
	Actor     string `json:"actor"`
	Resource  string `json:"resource"`
	OldValue  any    `json:"old_value"`
	NewValue  any    `json:"new_value"`
}

// redactedSentinel is the replacement value used for keys whose names match
// the sensitive-name filter. This is intentionally a different sentinel from
// the string-level `[REDACTED]` used elsewhere: this logs a distinct
// "***redacted***" string so that security_setting_change entries can be
// visually distinguished from other audit categories.
const redactedSentinel = "***redacted***"

// sensitiveSubstrings lists the lowercase substrings whose presence in any map
// key (case-insensitive) triggers unconditional redaction of that key's value.
// Substring semantics (not exact-match) mean "password_hash", "token_hash",
// "new_password", "my_api_key", and "client_secret" all redact.
var sensitiveSubstrings = []string{"password", "token", "api_key", "secret"}

// EmitSecuritySettingChange writes one JSONL audit record for a security config
// mutation when the audit logger is enabled. Fields: timestamp, event, actor,
// resource, old_value, new_value — with old_value and new_value recursively
// redacted for sensitive keys (password/token/api_key/secret, case-insensitive).
//
// Behavior contract:
//   - logger == nil → no-op, returns nil (audit log disabled).
//   - actor missing from ctx → actor="" and a slog.Warn is emitted, but the
//     audit entry IS still written (an audit trail of unauthenticated or
//     middleware-bypassed writes is strictly more valuable than none).
//   - audit-write error → slog.Error, return nil. Never blocks the caller.
//
// Redaction applies recursively to nested maps and to maps inside slices.
// Callers that audit user deletions should pass old_value as a plain
// map[string]any{"username": ..., "role": ...} — no hash field, no special
// casing; the recursive redactor handles any leakage defensively.
func EmitSecuritySettingChange(ctx context.Context, logger *Logger, resource string, oldValue, newValue any) error {
	if logger == nil {
		return nil
	}

	actor := extractActor(ctx)
	if actor == "" {
		slog.Warn("audit: security_setting_change without actor in context",
			"resource", resource)
	}

	record := SecurityChangeRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Event:     EventSecuritySettingChange,
		Actor:     actor,
		Resource:  resource,
		OldValue:  redactSensitive(oldValue),
		NewValue:  redactSensitive(newValue),
	}

	data, err := json.Marshal(record)
	if err != nil {
		slog.Error("audit: marshal security_setting_change failed",
			"error", err, "resource", resource)
		return nil
	}

	// CRIT-5: security_setting_change is a security-relevant configuration
	// mutation — fsync so an attacker who triggers a crash mid-change can't
	// hide the audit row that proves what they touched.
	if writeErr := logger.writeLine(data, true); writeErr != nil {
		slog.Error("audit: write security_setting_change failed",
			"error", writeErr, "resource", resource)
		return nil
	}
	return nil
}

// extractActor pulls the authenticated username off the context. Returns ""
// when the context has no user (e.g. system-initiated mutations or tests that
// forgot to inject the key). The retrieval uses ctxkey.UserContextKey which is
// the canonical storage type — gateway.UserContextKey is a type alias so both
// forms resolve to the same stored value.
func extractActor(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v := ctx.Value(ctxkey.UserContextKey{})
	if v == nil {
		return ""
	}
	if u, ok := v.(*config.UserConfig); ok && u != nil {
		return u.Username
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// redactSensitive returns a copy of v with any map value under a sensitive-named
// key replaced by the "***redacted***" sentinel. Recursion descends through
// map[string]any and []any; primitives pass through unchanged. The input is
// never mutated.
func redactSensitive(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, inner := range x {
			if isSensitiveKey(k) {
				out[k] = redactedSentinel
				continue
			}
			out[k] = redactSensitive(inner)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = redactSensitive(item)
		}
		return out
	default:
		return v
	}
}

// isSensitiveKey reports whether key (case-insensitive) contains any of the
// sensitive substrings. Substring semantics catch "password_hash",
// "token_hash", "new_password", "client_secret", "api_key_override", etc.
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}
