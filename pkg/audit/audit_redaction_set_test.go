// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #914 fix round. These tests pin the audit logger's credential
// pattern set: every default credential pattern except the email one, plus
// a URL-userinfo password pattern, with key prefixes anchored so a slug such
// as "project-task-…" is not mangled. Samples are the formats the docs list.

func newAuditRedactorForTest(t *testing.T) *Redactor {
	t.Helper()
	r, err := newAuditRedactor(nil)
	require.NoError(t, err)
	return r
}

func TestAuditCredentialPatterns_EveryDefaultExceptEmailPlusUserinfo(t *testing.T) {
	// One default pattern (email) is dropped, one (URL userinfo password)
	// is added — so the audit set has exactly as many patterns as the
	// default table.
	require.Len(t, auditCredentialPatterns(), len(defaultPatterns),
		"audit set = defaultPatterns minus email plus the URL-userinfo pattern")
	for i, label := range defaultPatternLabels {
		if label == emailPatternLabel {
			continue
		}
		assert.Contains(t, strings.Join(auditCredentialPatterns(), "\n"), defaultPatterns[i],
			"credential pattern %q must stay in the audit set", label)
	}
}

func TestAuditRedactor_RedactsEveryCredentialFormat(t *testing.T) {
	r := newAuditRedactorForTest(t)
	samples := []struct {
		name   string
		secret string
	}{
		{"sk- key", "sk-or-v1-0123456789abcdef0123456789abcdef"},
		{"key- key", "key-0123456789abcdefABCDEF"},
		{"Bearer token", "Bearer abcDEF123456.ghiJKL789"},
		{"GitHub PAT", "ghp_" + strings.Repeat("a", 36)},
		{"GitHub OAuth", "gho_" + strings.Repeat("b", 36)},
		{"Slack bot", "xoxb-1234567890-abcdefABCDEF"},
		{"Slack user", "xoxp-1234567890-abcdefABCDEF"},
		{"AWS AKIA", "AKIA" + "ABCDEFGHIJKLMNOP"},
		{"AWS ASIA", "ASIA" + "ABCDEFGHIJKLMNOP"},
		{"JWT", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl"},
		{"Google OAuth", "ya29.a0AfH6SMBx-abc_def"},
	}
	for _, s := range samples {
		t.Run(s.name, func(t *testing.T) {
			got := r.Redact("value: " + s.secret + " end")
			assert.NotContains(t, got, s.secret)
			assert.Equal(t, "value: [REDACTED] end", got)
		})
	}
}

func TestAuditRedactor_KeepsEmailAndMessageID(t *testing.T) {
	r := newAuditRedactorForTest(t)
	for _, s := range []string{"a@b.example", "<x@y.example>", "mail a@b.example and c.d@e.example now"} {
		assert.Equal(t, s, r.Redact(s))
	}
}

func TestAuditRedactor_KeyPrefixNeedsBoundary(t *testing.T) {
	r := newAuditRedactorForTest(t)
	for _, s := range []string{
		"docs/project-task-management-level1-spec.md",
		"risk-assessment-framework",
		"/home/u/desk-organiser-plans-2026-final.txt",
		"monkey-0123456789abcdefABCDEF",
		"XAKIAABCDEFGHIJKLMNOP",
	} {
		assert.Equal(t, s, r.Redact(s), "a letter/digit before the key prefix must block the match")
	}
	// A non-alphanumeric character before the prefix still matches, and is
	// kept in the output.
	assert.Equal(t, "OPENAI_API_KEY=[REDACTED]", r.Redact("OPENAI_API_KEY=sk-or-v1-0123456789abcdef0123456789"))
	assert.Equal(t, "[REDACTED] x [REDACTED]",
		r.Redact("sk-aaaaaaaaaaaaaaaaaaaaaaaa x sk-bbbbbbbbbbbbbbbbbbbbbbbb"))

	// Encoded separators count as a boundary too (round-3 finding S1): a
	// percent-encoded byte, a JSON \uXXXX escape, or a literal backslash
	// escape before the key must not hide it. The encoded prefix is kept.
	const key = "sk-or-v1-0123456789abcdef0123456789"
	encoded := map[string]string{
		"https://x.test/?q%3D" + key:     "https://x.test/?q%3D[REDACTED]",
		"a%20" + key:                     "a%20[REDACTED]",
		`\u0022` + key:                   `\u0022[REDACTED]`,
		`line1\n` + key:                  `line1\n[REDACTED]`,
		"Authorization: Bearer%20" + key: "Authorization: Bearer%20[REDACTED]",
	}
	for in, want := range encoded {
		assert.Equal(t, want, r.Redact(in), "encoded boundary before the key: %q", in)
	}
}

func TestAuditRedactor_BearerIsCaseInsensitive(t *testing.T) {
	r := newAuditRedactorForTest(t)
	for _, s := range []string{"bearer abcDEF123456.ghiJKL789", "BEARER abcDEF123456.ghiJKL789", "Bearer abcDEF123456.ghiJKL789"} {
		assert.Equal(t, "authorization: [REDACTED]", r.Redact("authorization: "+s), "input %q", s)
	}
	// The shared default set is unchanged: NewRedactor stays case-sensitive
	// for its other callers.
	full, err := NewRedactor(nil)
	require.NoError(t, err)
	assert.Equal(t, "bearer abcDEF123456.ghiJKL789", full.Redact("bearer abcDEF123456.ghiJKL789"))
}

func TestAuditRedactor_URLUserinfoPassword(t *testing.T) {
	r := newAuditRedactorForTest(t)
	cases := map[string]string{
		"psql postgres://admin:S3cretPass@db.internal:5432/app":            "psql postgres://admin:[REDACTED]@db.internal:5432/app",
		"git clone https://oauth2:glpat-AbCdEf123456@gitlab.example/x.git": "git clone https://oauth2:[REDACTED]@gitlab.example/x.git",
		// empty user, password only (round-3 finding S2)
		"redis-cli -u redis://:S3cretPass@cache.internal:6379/0": "redis-cli -u redis://:[REDACTED]@cache.internal:6379/0",
		// user only, no password: nothing to redact
		"https://alice@example.test/path": "https://alice@example.test/path",
	}
	for in, want := range cases {
		assert.Equal(t, want, r.Redact(in))
	}
}

// readAuditFile returns the whole audit.jsonl in dir.
func readAuditFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	return string(data)
}

func newRedactingLoggerForTest(t *testing.T) (*Logger, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := NewLogger(LoggerConfig{Dir: dir, RetentionDays: 90, RedactEnabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	return l, dir
}

type redactTestStruct struct {
	Cmd string `json:"cmd"`
	N   int    `json:"n"`
}

func TestLoggerRedaction_WalksTypedContainers(t *testing.T) {
	l, dir := newRedactingLoggerForTest(t)
	const secret = "sk-or-v1-0123456789abcdef0123456789abcdef"
	require.NoError(t, l.Log(&Entry{
		Event:    EventToolCall,
		Decision: DecisionAllow,
		Tool:     "typed",
		Parameters: map[string]any{
			"strings":    []string{"curl -H x " + secret},
			"str_map":    map[string]string{"header": "X-Key " + secret, "password": "hunter2"},
			"map_list":   []map[string]any{{"cmd": "run " + secret}},
			"struct":     redactTestStruct{Cmd: "run " + secret, N: 3},
			"struct_ptr": &redactTestStruct{Cmd: "run " + secret, N: 4},
			"count":      7,
			"flag":       true,
		},
	}))
	raw := readAuditFile(t, dir)
	assert.NotContains(t, raw, "sk-or-v1-", "no typed container may carry the key through")
	assert.NotContains(t, raw, "hunter2", "a secret-named key inside map[string]string must be redacted")
	assert.Contains(t, raw, `"count":7`, "numbers pass through unchanged")
	assert.Contains(t, raw, `"flag":true`, "booleans pass through unchanged")
	assert.Contains(t, raw, `"n":3`, "struct fields that are not secrets are kept")
}

func TestEmit_RedactsRecordFields(t *testing.T) {
	l, dir := newRedactingLoggerForTest(t)
	Emit(context.Background(), l, EventToolPolicyDenyAttempted, SeverityWarn, map[string]any{
		"command": "curl -H 'Authorization: Bearer abcDEF123456.ghiJKL789'",
		"token":   "plain-token-value",
		"nested":  map[string]any{"url": "postgres://admin:S3cretPass@db/app"},
	})
	raw := readAuditFile(t, dir)
	assert.NotContains(t, raw, "abcDEF123456.ghiJKL789")
	assert.NotContains(t, raw, "plain-token-value")
	assert.NotContains(t, raw, "S3cretPass")
	assert.Contains(t, raw, "[REDACTED]")
}

func TestEmitSecuritySettingChange_AppliesValuePatterns(t *testing.T) {
	l, dir := newRedactingLoggerForTest(t)
	require.NoError(t, EmitSecuritySettingChange(context.Background(), l, "sandbox.note",
		map[string]any{"note": "old"},
		map[string]any{"note": "use sk-or-v1-0123456789abcdef0123456789abcdef", "api_key": "k"}))
	raw := readAuditFile(t, dir)
	assert.NotContains(t, raw, "sk-or-v1-", "a credential in a non-secret-named field must be caught by value pattern")
	assert.Contains(t, raw, redactedSentinel, "name-based redaction still applies")

	res, err := l.Verify(context.Background())
	require.NoError(t, err)
	assert.True(t, res.Valid)
}
