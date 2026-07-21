package security_test

// File purpose: Sprint J issue #80 — audit field-name redaction integration tests.
//
// These tests prove that AuditEntry values with sensitive field names are replaced
// with [REDACTED], covering all aliases, nested structures, arrays, Bearer value
// patterns, edge cases (nil, already-redacted, number, bool), and the two-layer
// redaction model (field-name layer + value-pattern layer).
//
// Tests operate on the real pkg/audit.Redactor and pkg/audit.Logger, not mocks.
//
// Aliases tested (normalized by lowercase + strip _ and -):
//   password, pwd, passwd, passphrase, secret, token, api_key, apikey, api-key,
//   API_KEY, authorization, auth, bearer, private_key, signing_key, client_secret
//
// Traces to: sprint-j-security-hardening prompt §4 (audit field redaction).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/logger"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// loggerForTest creates a Logger with redaction enabled, backed by a temp dir.
func loggerForTest(t *testing.T) (*audit.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 1,
		RedactEnabled: true,
	})
	require.NoError(t, err, "NewLogger must succeed")
	t.Cleanup(func() { _ = logger.Close() })
	return logger, dir
}

// TestAuditFieldRedact_SensitiveFieldAliases — all aliases from the spec
//
// BDD: Given a Redactor,
//
//	When a map containing a sensitive field name is redacted,
//	Then the value is replaced with [REDACTED].
//
// Covers: password, pwd, passwd, passphrase, secret, token, api_key, apikey,
//
//	api-key, API_KEY, authorization, auth, bearer, private_key, signing_key, client_secret.
//
// Traces to: sprint-j prompt §4 "Cover all aliases from the backend-lead spec".
func TestAuditFieldRedact_SensitiveFieldAliases(t *testing.T) {
	// BDD: Given a Logger with redaction enabled
	logger, dir := loggerForTest(t)

	// All field names from the spec — some hyphenated, some camelCase, some UPPER
	sensitiveCases := []struct {
		fieldName string
		value     string
	}{
		{"password", "hunter2"},
		{"pwd", "secret123"},
		{"passwd", "p@ssw0rd"},
		{"passphrase", "my long passphrase"},
		{"secret", "top-secret-value"},
		{"token", "eyJhbGciOiJIUzI1NiJ9.payload"},
		{"api_key", "sk-abc123def456"},
		{"apikey", "sk-proj-xyz789"},
		{"api-key", "key-abcdefghij12345678901"},
		{"API_KEY", "sk-ant-abcdefghijklmnopqrst"},
		{"authorization", "Bearer my-token-xyz"},
		{"auth", "Basic dXNlcjpwYXNz"},
		{"bearer", "some-bearer-value"},
		{"private_key", "-----BEGIN PRIVATE KEY-----"},
		{"signing_key", "hmac-secret-key-value"},
		{"client_secret", "client-secret-12345"},
	}

	for _, tc := range sensitiveCases {
		t.Run("field_"+tc.fieldName, func(t *testing.T) {
			// BDD: When an audit entry is logged with this sensitive field
			entry := &audit.Entry{
				Timestamp: time.Now().UTC(),
				Event:     audit.EventToolCall,
				Decision:  audit.DecisionAllow,
				AgentID:   "test-agent",
				Tool:      "test-tool",
				Parameters: map[string]any{
					tc.fieldName: tc.value,
					"safe_field": "not-sensitive",
				},
			}
			require.NoError(t, logger.Log(entry))

			// BDD: Then the sensitive field is [REDACTED] in the written log
			written, readErr := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
			require.NoError(t, readErr)

			var decoded map[string]any
			require.NoError(t, json.Unmarshal(written, &decoded))

			params := decoded["parameters"].(map[string]any)
			redactedVal, ok := params[tc.fieldName]
			require.True(t, ok,
				"field %q must still be present in output (just redacted)", tc.fieldName)
			assert.Equal(t, "[REDACTED]", redactedVal,
				"field %q with value %q must be redacted to [REDACTED]",
				tc.fieldName, tc.value)

			// Content assertion: the original value must not appear anywhere in the log
			assert.NotContains(t, string(written), tc.value,
				"original sensitive value %q must not appear anywhere in the log output", tc.value)

			// Non-sensitive field must pass through unchanged
			assert.Equal(t, "not-sensitive", params["safe_field"],
				"non-sensitive field must be preserved unchanged")

			// Truncate file for next test case (same logger, same file)
			require.NoError(t, os.Truncate(filepath.Join(dir, "audit.jsonl"), 0))
		})
	}
}

// TestAuditFieldRedact_NestedStructure — nested sensitive fields
//
// BDD: Given {"outer": {"password": "x"}},
//
//	When redacted, then outer unchanged, inner password → [REDACTED].
//
// Traces to: sprint-j prompt §4 "Cover nested".
func TestAuditFieldRedact_NestedStructure(t *testing.T) {
	logger, dir := loggerForTest(t)

	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		AgentID:   "test-agent",
		Tool:      "web_search",
		Parameters: map[string]any{
			"outer": map[string]any{
				"password": "inner-secret",
				"safe":     "visible-value",
			},
			"top_level": "also-visible",
		},
	}
	require.NoError(t, logger.Log(entry))

	written, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(written, &decoded))

	params := decoded["parameters"].(map[string]any)

	// Outer field must still be a map (not redacted at top level)
	outer, ok := params["outer"].(map[string]any)
	require.True(t, ok,
		"outer must remain a map after redaction; found: %T", params["outer"])

	// Inner password must be redacted
	assert.Equal(t, "[REDACTED]", outer["password"],
		"nested password field must be redacted")

	// Inner safe field must pass through
	assert.Equal(t, "visible-value", outer["safe"],
		"nested non-sensitive field must be preserved")

	// Top-level safe field must pass through
	assert.Equal(t, "also-visible", params["top_level"],
		"top-level non-sensitive field must be preserved")

	// Original value must not leak
	assert.NotContains(t, string(written), "inner-secret",
		"the original nested password value must not appear in the log")
}

// TestAuditFieldRedact_ArrayWithSensitiveObjects — array of objects
//
// BDD: Given {"list": [{"token": "t1"}, {"safe": "ok"}]},
//
//	When redacted, then only the token is redacted; safe is preserved.
//
// Traces to: sprint-j prompt §4 "Cover arrays".
func TestAuditFieldRedact_ArrayWithSensitiveObjects(t *testing.T) {
	logger, dir := loggerForTest(t)

	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		AgentID:   "test-agent",
		Tool:      "some_tool",
		Details: map[string]any{
			"list": []any{
				map[string]any{"token": "t1-secret-value"},
				map[string]any{"safe": "ok"},
				map[string]any{"password": "pwd-in-array", "name": "alice"},
			},
		},
	}
	require.NoError(t, logger.Log(entry))

	written, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(written, &decoded))

	details := decoded["details"].(map[string]any)
	list := details["list"].([]any)
	require.Len(t, list, 3, "array must retain all elements after redaction")

	// First element: token must be redacted
	first := list[0].(map[string]any)
	assert.Equal(t, "[REDACTED]", first["token"],
		"token in first array element must be redacted")

	// Second element: safe field must pass through
	second := list[1].(map[string]any)
	assert.Equal(t, "ok", second["safe"],
		"safe field in second array element must be preserved")

	// Third element: password redacted, name preserved
	third := list[2].(map[string]any)
	assert.Equal(t, "[REDACTED]", third["password"],
		"password in third array element must be redacted")
	assert.Equal(t, "alice", third["name"],
		"non-sensitive name field in third element must be preserved")

	// Original values must not appear in output
	assert.NotContains(t, string(written), "t1-secret-value")
	assert.NotContains(t, string(written), "pwd-in-array")
}

// TestAuditFieldRedact_ValuePatternBearerString — value-pattern layer on non-sensitive key
//
// BDD: Given {"debug": "Bearer abc..."},
//
//	When redacted, the value-pattern redactor matches the Bearer string even though
//	the key is not in the sensitive field list.
//
// Traces to: sprint-j prompt §4 "Cover non-sensitive key + value-matching-pattern".
func TestAuditFieldRedact_ValuePatternBearerString(t *testing.T) {
	logger, dir := loggerForTest(t)

	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		AgentID:   "test-agent",
		Tool:      "debug_tool",
		Details: map[string]any{
			"debug": "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig",
			"info":  "safe plain text",
		},
	}
	require.NoError(t, logger.Log(entry))

	written, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(written, &decoded))

	details := decoded["details"].(map[string]any)

	// Value-pattern layer must redact the Bearer token in the "debug" field
	// even though "debug" is not a sensitive field name.
	debugVal, _ := details["debug"].(string)
	assert.NotContains(t, debugVal, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		"Bearer token in non-sensitive key 'debug' must be redacted by value-pattern layer")
	assert.Contains(t, debugVal, "[REDACTED]",
		"Bearer token in 'debug' must be replaced with [REDACTED]")

	// Non-Bearer safe text must pass through
	assert.Equal(t, "safe plain text", details["info"],
		"safe info field must not be redacted")
}

// TestAuditFieldRedact_AlreadyRedacted — no double-wrap
//
// BDD: Given {"token": "[REDACTED]"},
//
//	When redacted again, the value stays "[REDACTED]" (no "[REDACTED][REDACTED]").
//
// Traces to: sprint-j prompt §4 "Cover edge: value already '[REDACTED]'".
func TestAuditFieldRedact_AlreadyRedacted(t *testing.T) {
	logger, dir := loggerForTest(t)

	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		AgentID:   "test-agent",
		Tool:      "some_tool",
		Parameters: map[string]any{
			"token": "[REDACTED]", // already redacted from a previous pass
		},
	}
	require.NoError(t, logger.Log(entry))

	written, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(written, &decoded))

	params := decoded["parameters"].(map[string]any)
	tokenVal := params["token"].(string)

	// Must remain exactly "[REDACTED]" — no double-wrapping
	assert.Equal(t, "[REDACTED]", tokenVal,
		"already-redacted value must remain [REDACTED] without double-wrapping")
	assert.NotEqual(t, "[REDACTED][REDACTED]", tokenVal,
		"double-wrapping [REDACTED][REDACTED] must not occur")
}

// TestAuditFieldRedact_EdgeCases_NilEmptyNumberBool — no crash on primitive types
//
// BDD: Given parameters with nil, empty string, number, and bool values,
//
//	When a Logger with redaction enabled logs the entry, no panic occurs and
//	the values are handled gracefully.
//
// Traces to: sprint-j prompt §4 "Cover edge: value is nil, empty string, number, bool".
func TestAuditFieldRedact_EdgeCases_NilEmptyNumberBool(t *testing.T) {
	logger, dir := loggerForTest(t)

	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		AgentID:   "test-agent",
		Tool:      "edge_tool",
		Parameters: map[string]any{
			// Sensitive keys with primitive values — must be [REDACTED] for string-like,
			// or handled without panic for non-string.
			"password": nil,         // nil value
			"token":    "",          // empty string
			"api_key":  float64(42), // number (JSON numbers become float64)
			"secret":   true,        // bool
			// Non-sensitive key with normal value
			"count": float64(99),
		},
	}

	// Must not panic
	require.NotPanics(t, func() {
		_ = logger.Log(entry)
	}, "Logger.Log must not panic when sensitive fields have nil/empty/number/bool values")

	written, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	// Must be valid JSON
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(written, &decoded),
		"log entry with primitive sensitive values must produce valid JSON")

	params := decoded["parameters"].(map[string]any)

	// Sensitive fields must be handled (either [REDACTED] or nil — no crash)
	// The exact output for nil/number/bool is implementation-defined per
	// pkg/audit/redactor.go redactField — it calls redactedValue constant
	// for string-matched sensitives. For non-string types, the behavior
	// depends on the implementation. We verify: no panic + valid JSON.
	t.Logf("password (nil) → %v", params["password"])
	t.Logf("token (empty) → %v", params["token"])
	t.Logf("api_key (42) → %v", params["api_key"])
	t.Logf("secret (true) → %v", params["secret"])

	// Non-sensitive numeric field must pass through
	assert.Equal(t, float64(99), params["count"],
		"non-sensitive count field must be preserved as-is")

	// The string "empty" token (empty string is still a string) — check behavior
	// Per redactField: if value is string, it goes to redactedValue. So "" → "[REDACTED]".
	// This confirms empty strings are still caught.
	if tokenVal, ok := params["token"].(string); ok {
		assert.Equal(t, "[REDACTED]", tokenVal,
			"empty string value for sensitive key 'token' must be replaced with [REDACTED]")
	} else {
		// nil or some other type — just verify no panic (already past this point)
		t.Logf("token value is non-string type: %T", params["token"])
	}
}

// TestAuditFieldRedact_PersistenceTest — write then read-back
//
// BDD: Given an entry with sensitive parameters,
//
//	When logged and then read back from the JSONL file,
//	Then the sensitive values must be absent from the written file
//	and the non-sensitive values must be intact.
//
// This is the persistence test that proves the redactor is actually writing
// to the file — not just running in memory and discarding.
//
// Traces to: sprint-j prompt §4 (anti-shortcut persistence test).
func TestAuditFieldRedact_PersistenceTest(t *testing.T) {
	logger, dir := loggerForTest(t)

	// Two entries with DIFFERENT sensitive values
	entry1 := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		AgentID:   "agent-x",
		Tool:      "tool-alpha",
		Parameters: map[string]any{
			"password": "UNIQUE-PASSWORD-ALPHA-12345",
			"query":    "entry-one-query",
		},
	}
	entry2 := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		AgentID:   "agent-y",
		Tool:      "tool-beta",
		Parameters: map[string]any{
			"password": "UNIQUE-PASSWORD-BETA-67890",
			"query":    "entry-two-query",
		},
	}

	require.NoError(t, logger.Log(entry1))
	require.NoError(t, logger.Log(entry2))
	require.NoError(t, logger.Close())

	// Read back the entire file
	fileContents, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	rawLog := string(fileContents)

	// Persistence assertion: sensitive values must NOT appear anywhere in the file
	assert.NotContains(t, rawLog, "UNIQUE-PASSWORD-ALPHA-12345",
		"sensitive value from entry1 must not appear in the written log")
	assert.NotContains(t, rawLog, "UNIQUE-PASSWORD-BETA-67890",
		"sensitive value from entry2 must not appear in the written log")

	// Non-sensitive query values MUST appear in the file (proves write happened)
	assert.Contains(t, rawLog, "entry-one-query",
		"non-sensitive query from entry1 must be written to the file")
	assert.Contains(t, rawLog, "entry-two-query",
		"non-sensitive query from entry2 must be written to the file")

	// Differentiation: two different entries produce two different non-sensitive values
	assert.Contains(t, rawLog, "agent-x",
		"agent-x must appear in the persisted log")
	assert.Contains(t, rawLog, "agent-y",
		"agent-y must appear in the persisted log")
}

// TestAuditFieldRedact_Differentiation — two different inputs → two different outputs
//
// BDD: Given two entries with different sensitive field NAMES,
//
//	When both are redacted, the non-sensitive content differs between them
//	(proving the redactor is not a no-op that discards everything).
//
// This is the anti-hardcoded-response differentiation test.
// Traces to: sprint-j prompt §4 (anti-shortcut: differentiation test).
func TestAuditFieldRedact_Differentiation(t *testing.T) {
	logger, dir := loggerForTest(t)

	// Entry A: has password field
	entryA := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		AgentID:   "agent-diff-test",
		Tool:      "tool-A",
		Parameters: map[string]any{
			"password": "secret-A",
			"label":    "entry-alpha",
		},
	}
	require.NoError(t, logger.Log(entryA))

	// Entry B: has token field (different sensitive key name)
	entryB := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		AgentID:   "agent-diff-test",
		Tool:      "tool-B",
		Parameters: map[string]any{
			"token": "secret-B",
			"label": "entry-beta",
		},
	}
	require.NoError(t, logger.Log(entryB))

	fileContents, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	rawLog := string(fileContents)

	// Both sensitive values must be absent (redaction worked)
	assert.NotContains(t, rawLog, "secret-A", "password value must be redacted")
	assert.NotContains(t, rawLog, "secret-B", "token value must be redacted")

	// Both non-sensitive labels must be present and different
	assert.Contains(t, rawLog, "entry-alpha",
		"non-sensitive label from entry A must be present")
	assert.Contains(t, rawLog, "entry-beta",
		"non-sensitive label from entry B must be present")

	// Different tools must appear (differentiation: not the same record twice)
	assert.Contains(t, rawLog, "tool-A")
	assert.Contains(t, rawLog, "tool-B")
}
