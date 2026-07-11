package tools

// Tests for write_file's cross-agent overwrite-attribution note.
//
// Background: agents that are members of the same pkg/workspace.Workspace's
// CoreTeam deliberately share one physical directory for the duration of a
// turn (TurnWorkspaceDir, wired in pkg/agent/loop.go) — that sharing is
// accepted, decided architecture (ADR-032), not a bug. The gap this note
// closes is narrower: when a SECOND agent's write_file call replaces a file
// a DIFFERENT agent wrote earlier in that shared directory, the call
// succeeds silently today — no warning, no conflict signal, nothing in the
// tool result distinguishes it from overwriting your own prior file. These
// tests assert the informational note write_file now attaches to its
// SilentResult ForLLM content when that cross-agent case is detected, and
// that it does NOT fire for the normal, expected same-agent-overwrites-itself
// case or for two agents writing the same relative filename into two
// SEPARATE (non-shared) private workspaces.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// newWriteAuditLogger builds a real audit.Logger backed by a fresh temp dir,
// closed automatically at test cleanup. Mirrors the pattern used by
// shell_audit_test.go / registry_audit_test.go.
func newWriteAuditLogger(t *testing.T) *audit.Logger {
	t.Helper()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           t.TempDir(),
		RetentionDays: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = auditLogger.Close() })
	return auditLogger
}

// ---------------------------------------------------------------------------
// TestWriteFileTool_CrossAgentOverwrite_AttributesPriorWriter
// BDD:
//
//	Given a CoreTeam-shared workspace directory (TurnWorkspaceDir) and an
//	     audit logger wired into WriteFileTool
//	When agent "agent-jim" writes identity.txt (no prior file), and later
//	     agent "agent-worker" writes identity.txt with overwrite=true
//	Then agent-worker's tool result ForLLM includes a note naming agent-jim
//	     as the file's last writer
//
// This is the exact scenario reported: sequential, cross-agent, same
// relative filename, inside one shared directory.
// ---------------------------------------------------------------------------
func TestWriteFileTool_CrossAgentOverwrite_AttributesPriorWriter(t *testing.T) {
	sharedDir := t.TempDir()
	auditLogger := newWriteAuditLogger(t)

	tool := NewWriteFileTool(sharedDir, true)
	tool.SetAuditLogger(auditLogger)

	// agent-jim writes identity.txt directly into the shared workspace dir.
	jimCtx := WithAgentID(context.Background(), "agent-jim")
	jimCtx = WithTurnWorkspaceDir(jimCtx, sharedDir)
	jimResult := tool.Execute(jimCtx, map[string]any{
		"path":    "identity.txt",
		"content": "I am Jim",
	})
	require.False(t, jimResult.IsError, "jim's initial write should succeed: %s", jimResult.ForLLM)
	assert.NotContains(t, jimResult.ForLLM, "Note: you are replacing",
		"first write of a path must never carry a clobber note (nothing existed to clobber)")

	// agent-worker, delegated into the SAME shared directory, later writes
	// the SAME relative filename with overwrite=true.
	workerCtx := WithAgentID(context.Background(), "agent-worker")
	workerCtx = WithTurnWorkspaceDir(workerCtx, sharedDir)
	workerResult := tool.Execute(workerCtx, map[string]any{
		"path":      "identity.txt",
		"content":   "I am Worker",
		"overwrite": true,
	})
	require.False(t, workerResult.IsError, "worker's overwrite should still succeed: %s", workerResult.ForLLM)

	// The write must have actually happened (no new blocking behavior).
	data, err := os.ReadFile(filepath.Join(sharedDir, "identity.txt"))
	require.NoError(t, err)
	assert.Equal(t, "I am Worker", string(data), "overwrite must proceed regardless of the note")

	// The tool result must attribute the prior writer.
	assert.Contains(t, workerResult.ForLLM, "agent-jim",
		"worker's write result must reference jim as the file's last writer, got: %s", workerResult.ForLLM)
	assert.True(t, strings.Contains(workerResult.ForLLM, "Note: you are replacing a file last written via write_file by agent agent-jim"),
		"expected a well-formed attribution note, got: %s", workerResult.ForLLM)
}

// ---------------------------------------------------------------------------
// TestWriteFileTool_SameAgentOverwrite_NoFalsePositiveNote
// BDD:
//
//	Given a CoreTeam-shared workspace directory and an audit logger
//	When agent-jim writes identity.txt, and later agent-jim OVERWRITES its
//	     own identity.txt again
//	Then the second write's tool result carries NO clobber note
//
// An agent replacing its own earlier file is normal, expected iteration —
// not the cross-agent clobber this note exists to surface.
// ---------------------------------------------------------------------------
func TestWriteFileTool_SameAgentOverwrite_NoFalsePositiveNote(t *testing.T) {
	sharedDir := t.TempDir()
	auditLogger := newWriteAuditLogger(t)

	tool := NewWriteFileTool(sharedDir, true)
	tool.SetAuditLogger(auditLogger)

	jimCtx := WithAgentID(context.Background(), "agent-jim")
	jimCtx = WithTurnWorkspaceDir(jimCtx, sharedDir)

	first := tool.Execute(jimCtx, map[string]any{
		"path":    "notes.txt",
		"content": "draft one",
	})
	require.False(t, first.IsError, "first write should succeed: %s", first.ForLLM)

	second := tool.Execute(jimCtx, map[string]any{
		"path":      "notes.txt",
		"content":   "draft two",
		"overwrite": true,
	})
	require.False(t, second.IsError, "self-overwrite should succeed: %s", second.ForLLM)

	assert.NotContains(t, second.ForLLM, "Note: you are replacing",
		"same-agent overwriting its own prior file must never carry the cross-agent note, got: %s", second.ForLLM)

	data, err := os.ReadFile(filepath.Join(sharedDir, "notes.txt"))
	require.NoError(t, err)
	assert.Equal(t, "draft two", string(data))
}

// ---------------------------------------------------------------------------
// TestWriteFileTool_SamePathDifferentPrivateWorkspaces_NoFalsePositiveNote
// BDD:
//
//	Given TWO SEPARATE (non-shared) private workspace directories and an
//	     audit logger
//	When agent-jim writes identity.txt in workspace A, and agent-worker later
//	     writes identity.txt (overwrite=true) in workspace B — a physically
//	     different file that merely happens to share a relative filename
//	Then agent-worker's tool result carries NO clobber note
//
// Guards the canonicalization design: the note must key off the resolved
// absolute path (workspace-rooted), not the raw relative arg string, or two
// agents in completely unrelated private workspaces would spuriously look
// like they clobbered each other.
// ---------------------------------------------------------------------------
func TestWriteFileTool_SamePathDifferentPrivateWorkspaces_NoFalsePositiveNote(t *testing.T) {
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	auditLogger := newWriteAuditLogger(t)

	toolA := NewWriteFileTool(workspaceA, true)
	toolA.SetAuditLogger(auditLogger)
	toolB := NewWriteFileTool(workspaceB, true)
	toolB.SetAuditLogger(auditLogger)

	jimCtx := WithAgentID(context.Background(), "agent-jim")
	jimResult := toolA.Execute(jimCtx, map[string]any{
		"path":    "identity.txt",
		"content": "I am Jim, workspace A",
	})
	require.False(t, jimResult.IsError, "jim's write in workspace A should succeed: %s", jimResult.ForLLM)

	// Seed workspace B with an existing file so worker's call is a genuine
	// overwrite (of ITS OWN prior file, not a fresh create) — otherwise the
	// "file doesn't exist yet" branch would trivially skip the lookup and
	// prove nothing about the canonicalization.
	require.NoError(t, os.WriteFile(filepath.Join(workspaceB, "identity.txt"), []byte("placeholder"), 0o644))

	workerCtx := WithAgentID(context.Background(), "agent-worker")
	workerResult := toolB.Execute(workerCtx, map[string]any{
		"path":      "identity.txt",
		"content":   "I am Worker, workspace B",
		"overwrite": true,
	})
	require.False(t, workerResult.IsError, "worker's write in workspace B should succeed: %s", workerResult.ForLLM)

	assert.NotContains(t, workerResult.ForLLM, "Note: you are replacing",
		"same relative filename in two SEPARATE private workspaces must never be treated as a clobber, got: %s",
		workerResult.ForLLM)
}

// ---------------------------------------------------------------------------
// TestWriteFileTool_NilAuditLogger_NoNoteNoPanic
// Confirms the note lookup is best-effort: without an audit logger wired in,
// write_file must still succeed (no new blocking behavior) and simply omit
// the note rather than erroring or panicking.
// ---------------------------------------------------------------------------
func TestWriteFileTool_NilAuditLogger_NoNoteNoPanic(t *testing.T) {
	sharedDir := t.TempDir()
	tool := NewWriteFileTool(sharedDir, true) // no SetAuditLogger call

	jimCtx := WithAgentID(context.Background(), "agent-jim")
	jimCtx = WithTurnWorkspaceDir(jimCtx, sharedDir)
	first := tool.Execute(jimCtx, map[string]any{"path": "f.txt", "content": "a"})
	require.False(t, first.IsError, "first write should succeed: %s", first.ForLLM)

	workerCtx := WithAgentID(context.Background(), "agent-worker")
	workerCtx = WithTurnWorkspaceDir(workerCtx, sharedDir)
	second := tool.Execute(workerCtx, map[string]any{
		"path": "f.txt", "content": "b", "overwrite": true,
	})
	require.False(t, second.IsError, "overwrite should still succeed without an audit logger: %s", second.ForLLM)
	assert.NotContains(t, second.ForLLM, "Note: you are replacing")
}

// ---------------------------------------------------------------------------
// TestWriteFileTool_DegradedAuditLogger_NoNoteNoBlock
// Distinct from TestWriteFileTool_NilAuditLogger_NoNoteNoPanic above: here an
// audit logger IS wired in (t.auditLogger != nil), but the lookup itself
// fails (closedAuditLogger — see bash_test.go — is latched into degraded
// mode). Protects the "never blocking" contract against a future refactor
// that might accidentally make WriteFileTool's read path error-propagating:
// the write must still succeed and simply omit the note, exactly as if no
// logger were configured at all.
// ---------------------------------------------------------------------------
func TestWriteFileTool_DegradedAuditLogger_NoNoteNoBlock(t *testing.T) {
	sharedDir := t.TempDir()
	tool := NewWriteFileTool(sharedDir, true)
	tool.SetAuditLogger(closedAuditLogger(t)) // present but degraded -> lookup fails

	jimCtx := WithAgentID(context.Background(), "agent-jim")
	jimCtx = WithTurnWorkspaceDir(jimCtx, sharedDir)
	first := tool.Execute(jimCtx, map[string]any{"path": "f.txt", "content": "a"})
	require.False(t, first.IsError, "first write must succeed even though the audit logger is degraded: %s", first.ForLLM)

	workerCtx := WithAgentID(context.Background(), "agent-worker")
	workerCtx = WithTurnWorkspaceDir(workerCtx, sharedDir)
	second := tool.Execute(workerCtx, map[string]any{
		"path": "f.txt", "content": "b", "overwrite": true,
	})
	require.False(t, second.IsError, "overwrite must succeed even though the audit-backed lookup fails: %s", second.ForLLM)
	assert.NotContains(t, second.ForLLM, "Note: you are replacing",
		"a degraded/unreadable audit logger must never block the write or fabricate a note, got: %s", second.ForLLM)

	data, err := os.ReadFile(filepath.Join(sharedDir, "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, "b", string(data), "overwrite must proceed regardless of the failed lookup")
}

// ---------------------------------------------------------------------------
// TestAuditLogger_LastWriterForPath_Unit
// Unit-level coverage for the audit.Logger.LastWriterForPath query itself,
// independent of WriteFileTool: emits two file_op entries for the same path
// from two different agents and confirms the LAST one wins.
// ---------------------------------------------------------------------------
func TestAuditLogger_LastWriterForPath_Unit(t *testing.T) {
	auditLogger := newWriteAuditLogger(t)

	require.NoError(t, auditLogger.Log(&audit.Entry{
		Event:    FileWriteEvent,
		Decision: audit.DecisionAllow,
		AgentID:  "agent-jim",
		Tool:     "write_file",
		Details:  map[string]any{"path": "/shared/identity.txt", "op": "write"},
	}))
	require.NoError(t, auditLogger.Log(&audit.Entry{
		Event:    FileWriteEvent,
		Decision: audit.DecisionAllow,
		AgentID:  "agent-worker",
		Tool:     "write_file",
		Details:  map[string]any{"path": "/shared/identity.txt", "op": "write"},
	}))

	got, found := auditLogger.LastWriterForPath(FileWriteEvent, "/shared/identity.txt")
	assert.True(t, found)
	assert.Equal(t, "agent-worker", got, "most recent writer must win over the earlier one")

	_, found = auditLogger.LastWriterForPath(FileWriteEvent, "/shared/does-not-exist.txt")
	assert.False(t, found, "no entries for this path -> not found")
}
