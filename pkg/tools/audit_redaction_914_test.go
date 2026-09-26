// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// Issue #914 fix round: credentials must not escape through the audit
// preview truncation or through the gateway log on audit-failure paths.

// TestAuditDenyPreview_RedactsBeforeTruncating places a key so that it
// straddles the 512-byte preview cut. Truncating first would leave a key
// fragment too short for the pattern to recognise.
func TestAuditDenyPreview_RedactsBeforeTruncating(t *testing.T) {
	const key = "sk-or-v1-0123456789abcdef0123456789abcdef"
	prefix := strings.Repeat("a ", (environmentSetupAuditDenyPreviewBytes-12)/2)
	require.Less(t, len(prefix), environmentSetupAuditDenyPreviewBytes)
	require.Greater(t, len(prefix)+len(key), environmentSetupAuditDenyPreviewBytes,
		"precondition: the key must cross the preview cut")
	in := prefix + key + strings.Repeat(" tail", 200)

	got := auditDenyPreview(in)
	assert.NotContains(t, got, "sk-or-v1", "no fragment of the key may survive the preview")
	assert.Contains(t, got, "[REDACTED]")
	assert.Contains(t, got, " [audit preview truncated; ", "the preview is still bounded")
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestBash_AuditFailClosed_GatewayLogRedactsCommand: when the audit write
// fails, the refusal is logged to the gateway log — that line must not
// carry the raw command's credentials.
func TestBash_AuditFailClosed_GatewayLogRedactsCommand(t *testing.T) {
	logBuf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	workspace := t.TempDir()
	tool, err := NewExecToolWithDeps(workspace, false, nil, ExecToolDeps{AuditFailClosed: true})
	require.NoError(t, err)
	// Closed like closedAuditLogger, but redacting like the production
	// logger, so the logger's own degraded-mode line is redacted and the
	// assertion isolates the bash refusal line.
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: t.TempDir(), RetentionDays: 1, RedactEnabled: true})
	require.NoError(t, err)
	require.NoError(t, auditLogger.Close())
	tool.SetAuditLogger(auditLogger)

	result := tool.Execute(bashCtx(t), map[string]any{
		"command": "curl -H 'Authorization: Bearer abcDEF123456.ghiJKL789' https://api.example.test",
	})
	require.True(t, result.IsError, "fail-closed must refuse")

	logged := logBuf.String()
	require.Contains(t, logged, "bash: audit logger degraded",
		"precondition: the fail-closed log line must have been written")
	assert.NotContains(t, logged, "abcDEF123456.ghiJKL789", "the gateway log must not carry the raw token")
}
