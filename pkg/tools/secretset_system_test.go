// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// $OMNIPUS_HOME/system/ was not in any part of the secret set — adversarial
// review finding #1. It holds audit.jsonl (pkg/audit), that log's rotated
// audit-YYYY-MM-DD.jsonl siblings and audit-chain-checkpoint.json (the v0.2
// HMAC tamper-evidence chain's own anchor), token_budget.json, and
// state.json. The HMAC chain detects a sandboxed child MODIFYING a logged
// entry; it does nothing to stop the child from truncating or deleting the
// file outright — neither operation needs a read, and a deleted file has no
// chain left to verify.
//
// This file drives the APP-LAYER half of the fix (the same read_file/
// write_file/send_file resolver boundary secretset_backups_auth_test.go
// exercises for backups/ and auth.json): an agent cannot read, overwrite, or
// truncate system/audit.jsonl through the generic filesystem tools. The
// KERNEL half — a real child under sandbox-exec cannot truncate or delete the
// file at all, while the gateway itself still can — is
// pkg/sandbox/seatbelt_deny_darwin_test.go's
// TestSeatbelt_RealChildCannotTamperWithAuditLog.

// systemSecretHome lays out a $OMNIPUS_HOME with a live-shaped system/
// directory (audit.jsonl plus a rotated sibling) and an agent home to serve
// as the turn's work dir, and points config.OmnipusHomeDir at it.
//
// Returns the resolved home, the agent home, and the path to audit.jsonl.
// The home is symlink-resolved because on macOS t.TempDir() lives under
// /var -> /private/var, and the carve-out comparison is done on realpaths.
func systemSecretHome(t *testing.T) (home, agentHome, auditPath string) {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home = base

	agentHome = filepath.Join(home, "agents", "mia")
	require.NoError(t, os.MkdirAll(agentHome, 0o700))

	systemDir := filepath.Join(home, "system")
	require.NoError(t, os.MkdirAll(systemDir, 0o700))
	auditPath = filepath.Join(systemDir, "audit.jsonl")
	require.NoError(t, os.WriteFile(auditPath,
		[]byte(`{"event":"tool_call","decision":"allow"}`+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(systemDir, "audit-2026-08-01.jsonl"),
		[]byte(`{"event":"tool_call","decision":"deny"}`+"\n"), 0o600))

	// The controls: ordinary, non-secret files in the same parents. If these
	// stopped being reachable, the tests below would be proving "the home is
	// unreachable" rather than "system/ specifically is".
	require.NoError(t, os.WriteFile(filepath.Join(home, "notes.txt"), []byte("ORDINARY-HOME-FILE"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentHome, "own.txt"), []byte("OWN-WORKDIR-FILE"), 0o600))

	t.Setenv(config.EnvHome, home)
	return home, agentHome, auditPath
}

// TestSecretSet_ReadFileToolCannotReachAuditLog drives read_file end-to-end.
func TestSecretSet_ReadFileToolCannotReachAuditLog(t *testing.T) {
	home, agentHome, auditPath := systemSecretHome(t)

	// restrict=false is the worst case for this boundary: unrestricted scope
	// removes every path-based excuse for refusing, leaving only the
	// carve-out. If the deny holds here it holds under confined too.
	tool := NewReadFileTool(agentHome, false, 0)

	cases := []struct {
		name       string
		path       string
		wantDenied bool
		marker     string
	}{
		{"audit.jsonl", auditPath, true, "tool_call"},
		{"rotated audit-2026-08-01.jsonl", filepath.Join(home, "system", "audit-2026-08-01.jsonl"), true, "deny"},
		{"system directory itself", filepath.Join(home, "system"), true, ""},
		{"control: ordinary file in $OMNIPUS_HOME", filepath.Join(home, "notes.txt"), false, "ORDINARY-HOME-FILE"},
		{"control: file in the agent's own work dir", filepath.Join(agentHome, "own.txt"), false, "OWN-WORKDIR-FILE"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.Execute(context.Background(), map[string]any{"path": tc.path})
			body := readFileResultText(t, res)

			if tc.wantDenied {
				require.True(t, res.IsError, "read_file must be denied for %s, got success: %s", tc.path, body)
				if tc.marker != "" {
					assert.NotContains(t, body, tc.marker, "audit log content leaked into the tool result")
				}
				return
			}

			require.False(t, res.IsError, "control must stay readable: %s", body)
			assert.Contains(t, body, tc.marker)
		})
	}
}

// TestSecretSet_WriteFileToolCannotTruncateOrOverwriteAuditLog drives
// write_file end-to-end against audit.jsonl, both as an overwrite (the
// truncate-then-write shape a `: > audit.jsonl` shell redirect performs) and
// as a fresh write to a NEW file inside system/ (proving the directory
// itself is denied, not merely the one pre-existing name).
func TestSecretSet_WriteFileToolCannotTruncateOrOverwriteAuditLog(t *testing.T) {
	home, agentHome, auditPath := systemSecretHome(t)

	tool := NewWriteFileTool(agentHome, false)

	before, err := os.ReadFile(auditPath)
	require.NoError(t, err)

	res := tool.Execute(context.Background(), map[string]any{
		"path":      auditPath,
		"content":   "",
		"overwrite": true,
	})
	require.True(t, res.IsError, "write_file must be denied for audit.jsonl, got success: %s %s", res.ForLLM, res.ForUser)

	after, err := os.ReadFile(auditPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "audit.jsonl must be byte-for-byte unmodified after the denied write")

	newFile := filepath.Join(home, "system", "planted-by-agent.json")
	res2 := tool.Execute(context.Background(), map[string]any{
		"path":    newFile,
		"content": "{}",
	})
	require.True(t, res2.IsError, "write_file must be denied for a NEW file inside system/, got success: %s %s", res2.ForLLM, res2.ForUser)
	_, statErr := os.Stat(newFile)
	assert.True(t, os.IsNotExist(statErr), "a new file must not be plantable inside system/")

	// Control: the write path still works outside system/.
	controlPath := filepath.Join(agentHome, "scratch.txt")
	res3 := tool.Execute(context.Background(), map[string]any{
		"path":    controlPath,
		"content": "scratch",
	})
	require.False(t, res3.IsError, "control write inside the agent's own work dir must succeed: %s %s", res3.ForLLM, res3.ForUser)
}

// TestSecretSet_SendFileResolutionCannotReachAuditLog drives the exact
// resolver call send_file.go makes (FSOpSend via ResolvePathAllowingPatterns)
// — the path with no restriction of its own beyond the carve-out check.
func TestSecretSet_SendFileResolutionCannotReachAuditLog(t *testing.T) {
	_, agentHome, auditPath := systemSecretHome(t)

	ctx := context.Background()
	policy, err := ResolveTurnFSPolicy(ctx, agentHome, false)
	require.NoError(t, err)

	handle, err := ResolvePathAllowingPatterns(ctx, policy, "send_file", "", FSOpSend, auditPath, nil)
	require.Error(t, err, "send_file must not resolve %s — that is a one-call audit-log disclosure", auditPath)
	require.ErrorIs(t, err, ErrCarveOut, "the refusal must come from the secret set, not incidentally from scope")
	assert.Nil(t, handle)
}
