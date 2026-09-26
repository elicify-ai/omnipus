// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
)

// Issue #914: the production audit logger (built by initializeAudit during
// NewAgentLoop) never enabled redaction, so credentials typed into a bash
// command were written verbatim to audit.jsonl and served by
// GET /api/v1/audit-log. Every existing redaction test built its own logger
// with RedactEnabled:true, so none of them could see the wiring gap. These
// tests go through the real boot path instead.
//
// The email half pins the founder's MC-19 ruling: the audit log must record
// mail recipients and Message-IDs in full, so the production logger redacts
// credentials only, never email addresses.

// bootAuditLoggerForRedactionTest boots a real AgentLoop against a temp home
// and returns its production audit logger and the audit.jsonl path.
func bootAuditLoggerForRedactionTest(t *testing.T) (*audit.Logger, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := auditBootTestConfig()
	require.True(t, cfg.Sandbox.AuditLog, "precondition: audit must be on by default")

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })

	auditLogger := al.AuditLogger()
	require.NotNil(t, auditLogger, "precondition: a fresh install must boot with an audit logger")
	return auditLogger, filepath.Join(home, "system", "audit.jsonl")
}

// lastAuditLine returns the last non-empty JSONL line of path, decoded.
func lastAuditLine(t *testing.T, path string) (string, map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	last := lines[len(lines)-1]
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(last), &parsed))
	return last, parsed
}

func TestProductionAuditLogger_RedactsCredentialsInBashCommand(t *testing.T) {
	auditLogger, auditPath := bootAuditLoggerForRedactionTest(t)

	const bearer = "Bearer abcDEF123456.ghiJKL789-token"
	const orKey = "sk-or-v1-0123456789abcdef0123456789abcdef"
	command := `curl -H "Authorization: ` + bearer + `" -H "X-Key: ` + orKey + `" https://api.example.test/v1`

	require.NoError(t, auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: audit.DecisionAllow,
		AgentID:  "general-purpose",
		Tool:     "bash",
		Command:  command,
	}))

	raw, parsed := lastAuditLine(t, auditPath)
	assert.NotContains(t, raw, "abcDEF123456.ghiJKL789-token",
		"the Bearer token must not reach audit.jsonl")
	assert.NotContains(t, raw, orKey,
		"the OpenRouter key must not reach audit.jsonl")
	assert.NotContains(t, raw, "sk-or-v1-",
		"no fragment of the OpenRouter key may reach audit.jsonl")

	got, ok := parsed["command"].(string)
	require.True(t, ok, "command must be a string")
	assert.Equal(t,
		`curl -H "Authorization: [REDACTED]" -H "X-Key: [REDACTED]" https://api.example.test/v1`,
		got, "only the credentials are replaced; the rest of the command stays readable")

	// The redacted file must still verify: redaction happens before the HMAC
	// is computed, so the chain covers the bytes actually on disk.
	res, err := auditLogger.Verify(context.Background())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Valid, "chain must verify on the redacted file: broken_at=%d reason=%q", res.BrokenAt, res.Reason)
	assert.GreaterOrEqual(t, res.EntriesScanned, 2, "startup entry plus the exec entry must be scanned")
}

func TestProductionAuditLogger_KeepsMailRecipientsAndMessageID(t *testing.T) {
	auditLogger, auditPath := bootAuditLoggerForRedactionTest(t)

	require.NoError(t, auditLogger.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionAllow,
		AgentID:  "general-purpose",
		Tool:     "email_send",
		Details: map[string]any{
			"recipients": []any{"a@b.example"},
			"message_id": "<x@y.example>",
		},
	}))

	_, parsed := lastAuditLine(t, auditPath)
	details, ok := parsed["details"].(map[string]any)
	require.True(t, ok, "details must be a JSON object")
	assert.Equal(t, []any{"a@b.example"}, details["recipients"],
		"MC-19: recipient addresses must be logged in full")
	assert.Equal(t, "<x@y.example>", details["message_id"],
		"MC-19: the Message-ID must be logged in full")

	res, err := auditLogger.Verify(context.Background())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Valid, "chain must verify: broken_at=%d reason=%q", res.BrokenAt, res.Reason)
}
