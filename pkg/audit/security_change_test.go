// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/logger"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
)

// ctxWithUser builds a context carrying a *config.UserConfig under the shared
// ctxkey.UserContextKey — the same key the gateway auth middleware uses.
func ctxWithUser(username string) context.Context {
	return context.WithValue(context.Background(), ctxkey.UserContextKey{},
		&config.UserConfig{Username: username})
}

// readLastAuditLine returns the last non-empty line of audit.jsonl as a
// decoded map. Fails the test on any I/O or JSON error.
func readLastAuditLine(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	var last []byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			last = line
		}
	}
	require.NotNil(t, last, "audit.jsonl must contain at least one record")
	var out map[string]any
	require.NoError(t, json.Unmarshal(last, &out))
	return out
}

// TestEmitSecuritySettingChange_WhenDisabled_NoOp — a nil Logger means the
// operator disabled sandbox.audit_log. The helper MUST silently no-op without
// side effects and MUST return nil.
func TestEmitSecuritySettingChange_WhenDisabled_NoOp(t *testing.T) {
	ctx := ctxWithUser("alice")
	err := audit.EmitSecuritySettingChange(ctx, nil, "sandbox.audit_log", false, true)
	assert.NoError(t, err, "nil logger path must return nil")
}

// TestEmitSecuritySettingChange_ShapeCorrect — with a live logger, the JSONL
// record MUST carry all six FR-020 fields with correct types (timestamp string
// in RFC3339Nano, event/actor/resource strings, old/new values preserved).
func TestEmitSecuritySettingChange_ShapeCorrect(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := ctxWithUser("alice")
	require.NoError(t, audit.EmitSecuritySettingChange(
		ctx, logger, "sandbox.audit_log", false, true))

	rec := readLastAuditLine(t, dir)

	for _, field := range []string{"timestamp", "event", "actor", "resource", "old_value", "new_value"} {
		assert.Contains(t, rec, field, "record must include field %q", field)
	}
	assert.Equal(t, audit.EventSecuritySettingChange, rec["event"])
	assert.Equal(t, "alice", rec["actor"])
	assert.Equal(t, "sandbox.audit_log", rec["resource"])
	assert.Equal(t, false, rec["old_value"])
	assert.Equal(t, true, rec["new_value"])

	ts, ok := rec["timestamp"].(string)
	require.True(t, ok, "timestamp must serialize as a string")
	_, parseErr := time.Parse(time.RFC3339Nano, ts)
	assert.NoError(t, parseErr, "timestamp must be RFC3339Nano-parseable")
}

// TestRedact_PasswordInNestedMap — the redactor MUST descend into nested
// map[string]any and substitute sensitive values at every level.
func TestRedact_PasswordInNestedMap(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := ctxWithUser("admin")
	newValue := map[string]any{
		"user": map[string]any{"password": "plaintext-pw"},
	}
	require.NoError(t, audit.EmitSecuritySettingChange(
		ctx, logger, "gateway.users.bob.password", nil, newValue))

	rec := readLastAuditLine(t, dir)
	nv, ok := rec["new_value"].(map[string]any)
	require.True(t, ok, "new_value must round-trip as an object")
	inner, ok := nv["user"].(map[string]any)
	require.True(t, ok, "nested user map must be preserved as an object")
	assert.Equal(t, "***redacted***", inner["password"],
		"nested password field must be replaced with the redaction sentinel")
}

// TestRedact_TokenHashRedacted — keys whose name contains "token" (substring,
// e.g. "token_hash") MUST be redacted.
func TestRedact_TokenHashRedacted(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := ctxWithUser("admin")
	oldValue := map[string]any{"token_hash": "$2a$10$abcdefg"}
	require.NoError(t, audit.EmitSecuritySettingChange(
		ctx, logger, "gateway.users.alice.token_hash", oldValue, nil))

	rec := readLastAuditLine(t, dir)
	ov, ok := rec["old_value"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "***redacted***", ov["token_hash"])
}

// TestRedact_ApiKeyRedacted — keys whose name contains "api_key" MUST be
// redacted even when the value does not match any regex pattern.
func TestRedact_ApiKeyRedacted(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := ctxWithUser("admin")
	newValue := map[string]any{"api_key": "sk-whatever-123"}
	require.NoError(t, audit.EmitSecuritySettingChange(
		ctx, logger, "providers.openai.api_key", nil, newValue))

	rec := readLastAuditLine(t, dir)
	nv, ok := rec["new_value"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "***redacted***", nv["api_key"])
}

// TestRedact_CaseInsensitive — uppercase and mixed-case variants of sensitive
// names (Password, PASSWORD, MyToken) MUST all trigger redaction.
func TestRedact_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := ctxWithUser("admin")

	cases := []struct {
		name string
		key  string
	}{
		{"titlecase Password", "Password"},
		{"uppercase PASSWORD", "PASSWORD"},
		{"mixed Secret", "Client_Secret"},
		{"mixed Token", "MyToken"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newValue := map[string]any{tc.key: "raw-value"}
			require.NoError(t, audit.EmitSecuritySettingChange(
				ctx, logger, "test."+tc.key, nil, newValue))

			rec := readLastAuditLine(t, dir)
			nv, ok := rec["new_value"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "***redacted***", nv[tc.key],
				"key %q must be redacted regardless of case", tc.key)
		})
	}
}

// TestRedact_UnrelatedFieldsPreserved — non-sensitive keys (username, role,
// plain data) MUST pass through unchanged. This is the "user-delete" path:
// callers pass {username, role} explicitly and the recursive redactor leaves
// both fields alone.
func TestRedact_UnrelatedFieldsPreserved(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := ctxWithUser("admin")
	oldValue := map[string]any{"username": "alice", "role": "admin"}
	require.NoError(t, audit.EmitSecuritySettingChange(
		ctx, logger, "gateway.users.alice", oldValue, nil))

	rec := readLastAuditLine(t, dir)
	ov, ok := rec["old_value"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "alice", ov["username"])
	assert.Equal(t, "admin", ov["role"])
}

// TestRedact_RecursiveIntoArrays — redaction MUST recurse into []any that
// contain maps (e.g. a list of user records each with a password_hash).
func TestRedact_RecursiveIntoArrays(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := ctxWithUser("admin")
	newValue := map[string]any{
		"users": []any{
			map[string]any{"username": "alice", "password": "pw1"},
			map[string]any{"username": "bob", "password": "pw2"},
		},
	}
	require.NoError(t, audit.EmitSecuritySettingChange(
		ctx, logger, "gateway.users", nil, newValue))

	rec := readLastAuditLine(t, dir)
	nv, ok := rec["new_value"].(map[string]any)
	require.True(t, ok)
	users, ok := nv["users"].([]any)
	require.True(t, ok, "users must round-trip as an array")
	require.Len(t, users, 2)
	for i, u := range users {
		m, ok := u.(map[string]any)
		require.True(t, ok, "element %d must be an object", i)
		assert.Equal(t, "***redacted***", m["password"],
			"password inside array element %d must be redacted", i)
		assert.NotEqual(t, "***redacted***", m["username"],
			"username inside array element %d must be preserved", i)
	}
}

// TestEmitSecuritySettingChange_ActorMissing_LogsWarn — when the context
// carries no user (unauthenticated or test forgot to inject), the helper MUST
// emit a slog.Warn AND still write the audit record with actor="". A silent
// skip would let an attacker who bypassed middleware evade the audit trail.
func TestEmitSecuritySettingChange_ActorMissing_LogsWarn(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	require.NoError(t, audit.EmitSecuritySettingChange(
		context.Background(), logger, "sandbox.audit_log", false, true))

	assert.True(t,
		strings.Contains(buf.String(), "security_setting_change without actor"),
		"slog.Warn must fire when actor is missing; got: %q", buf.String())

	rec := readLastAuditLine(t, dir)
	assert.Equal(t, "", rec["actor"], "actor field must be empty string when unavailable")
	assert.Equal(t, audit.EventSecuritySettingChange, rec["event"],
		"audit record must still be written when actor is missing")
}
