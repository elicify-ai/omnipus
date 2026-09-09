// Package tools — regression tests for the metadata path guard.
//
// These tests exercise the fail-closed guard in metadata_guard.go that blocks
// read_file / write_file / edit_file / append_file from accessing
// agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md and redirects callers to
// read_agent_metadata (read) / update_agent (write).
//
// Regression tests per issue #240:
//   - BEFORE the guard: writes to SOUL.md via write_file succeeded (SilentResult,
//     file on disk).
//   - AFTER the guard: result.IsError==true, ForLLM contains
//     "update_agent" or "read_agent_metadata", and file
//     key hints in the suggestion.
//
// The fuzzy case covers agent-prefixed filenames (e.g. "hustle-heartbeat.md"
// → suggests file="heartbeat").

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// setupMetadataTestWorkspace creates the directory layout:
//
//	<tmp>/agents/test-agent/
//
// and returns the workspace path (the agent directory).
func setupMetadataTestWorkspace(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	ws := filepath.Join(home, "agents", "test-agent")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return ws
}

// TestFileTools_BlockAgentMetadata verifies that the generic file tools block
// reads and writes to known metadata filenames inside an agents/<id>/ directory.
//
// Traces to: issue #240 — agent metadata files must be self-validating and
// deterministic; generic file tools must fail-closed with a structured error.
func TestFileTools_BlockAgentMetadata(t *testing.T) {
	ws := setupMetadataTestWorkspace(t)
	ctx := context.Background()

	// Filenames that must be blocked, including lowercase and exact cases.
	blockedFilenames := []string{
		"SOUL.md", "soul.md",
		"HEARTBEAT.md", "heartbeat.md",
		"MEMORY.md", "memory.md",
		"AGENT.md", "agent.md",
	}

	for _, fname := range blockedFilenames {
		t.Run("write_file/"+fname, func(t *testing.T) {
			wt := tools.NewWriteFileTool(ws, false)
			result := wt.Execute(ctx, map[string]any{
				"path":      fname,
				"content":   "blocked content",
				"overwrite": true,
			})
			if !result.IsError {
				t.Fatalf("write_file(%q): expected blocked error, got success: %s", fname, result.ForLLM)
			}
			assertMetadataGuardError(t, result.ForLLM, "write")
			// File must NOT have been created on disk.
			if _, err := os.Stat(filepath.Join(ws, strings.ToUpper(fname))); err == nil {
				t.Errorf("write_file(%q): file was written despite guard", fname)
			}
		})

		t.Run("read_file/"+fname, func(t *testing.T) {
			// Pre-create the file so the only failure can come from the guard.
			path := filepath.Join(ws, fname)
			if err := os.WriteFile(path, []byte("existing content"), 0o644); err != nil {
				t.Fatalf("setup pre-create: %v", err)
			}
			defer os.Remove(path)

			rt := tools.NewReadFileTool(ws, false, 1024)
			result := rt.Execute(ctx, map[string]any{"path": fname})
			if !result.IsError {
				t.Fatalf("read_file(%q): expected blocked error, got success: %s", fname, result.ForLLM)
			}
			assertMetadataGuardError(t, result.ForLLM, "read")
		})

		t.Run("edit_file/"+fname, func(t *testing.T) {
			path := filepath.Join(ws, fname)
			if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
				t.Fatalf("setup: %v", err)
			}
			defer os.Remove(path)

			et := tools.NewEditFileTool(ws, false)
			result := et.Execute(ctx, map[string]any{
				"path":     fname,
				"old_text": "old content",
				"new_text": "new content",
			})
			if !result.IsError {
				t.Fatalf("edit_file(%q): expected blocked error, got success: %s", fname, result.ForLLM)
			}
			assertMetadataGuardError(t, result.ForLLM, "write")
		})

		t.Run("append_file/"+fname, func(t *testing.T) {
			at := tools.NewAppendFileTool(ws, false)
			result := at.Execute(ctx, map[string]any{
				"path":    fname,
				"content": "appended content",
			})
			if !result.IsError {
				t.Fatalf("append_file(%q): expected blocked error, got success: %s", fname, result.ForLLM)
			}
			assertMetadataGuardError(t, result.ForLLM, "write")
		})
	}
}

// TestFileTools_AllowNonMetadataFiles verifies that normal files within the
// agent workspace are NOT affected by the metadata guard.
//
// Traces to: issue #240 — the guard must only block the four canonical files.
func TestFileTools_AllowNonMetadataFiles(t *testing.T) {
	ws := setupMetadataTestWorkspace(t)
	ctx := context.Background()

	// Use absolute paths to avoid writing to the package source directory
	// when restrict=false (hostFs writes to the literal path provided).
	normalFiles := []string{"README.md", "notes.txt", "data.json", "SOUL.txt", "HEARTBEAT.json"}
	for _, fname := range normalFiles {
		t.Run("write_file/"+fname, func(t *testing.T) {
			absPath := filepath.Join(ws, fname)
			wt := tools.NewWriteFileTool(ws, false)
			result := wt.Execute(ctx, map[string]any{
				"path":      absPath,
				"content":   "allowed content",
				"overwrite": true,
			})
			if result.IsError {
				// Only fail if the error contains the guard code.
				if strings.Contains(result.ForLLM, "USE_METADATA_TOOL") {
					t.Errorf("write_file(%q): incorrectly blocked by metadata guard: %s", fname, result.ForLLM)
				}
			}
		})
	}
}

// TestFileTools_HeartbeatGuardSuggestsKey verifies that blocking a write to
// HEARTBEAT.md emits a suggestion that names the "heartbeat" update_agent
// field and the update_agent replacement tool.
//
// Traces to: issue #240 — the structured error must hand the agent the exact
// metadata key to use.
func TestFileTools_HeartbeatGuardSuggestsKey(t *testing.T) {
	ws := setupMetadataTestWorkspace(t)
	ctx := context.Background()

	wt := tools.NewWriteFileTool(ws, false)
	result := wt.Execute(ctx, map[string]any{
		"path":      "HEARTBEAT.md",
		"content":   "x",
		"overwrite": true,
	})
	if !result.IsError {
		t.Fatalf("expected guard error for HEARTBEAT.md, got success: %s", result.ForLLM)
	}
	// The suggestion must include heartbeat somewhere in it (the update_agent field).
	// The JSON encoding escapes the double quotes, so we look for the plain string.
	if !strings.Contains(result.ForLLM, "heartbeat") {
		t.Errorf("guard error should suggest heartbeat, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "update_agent") {
		t.Errorf("guard error should mention update_agent, got: %s", result.ForLLM)
	}
}

// TestFileTools_NoWorkspaceNoGuard verifies that tools constructed without a
// workspace (workspace="") do NOT trigger the guard — they have no agent
// workspace context to protect.
func TestFileTools_NoWorkspaceNoGuard(t *testing.T) {
	// Create a temp dir and write SOUL.md directly there (not under agents/<id>/).
	dir := t.TempDir()
	soulPath := filepath.Join(dir, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("existing soul"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ctx := context.Background()

	// A tool with workspace="" should NOT guard — the path is not inside
	// agents/<id>/ so metadataFileMatch won't trigger anyway.
	wt := tools.NewWriteFileTool("", false)
	result := wt.Execute(ctx, map[string]any{
		"path":      soulPath,
		"content":   "new content",
		"overwrite": true,
	})
	// The empty workspace means the tool can't resolve relative paths, but an
	// absolute path to a non-agents-layout file should NOT return USE_METADATA_TOOL.
	if result.IsError && strings.Contains(result.ForLLM, "USE_METADATA_TOOL") {
		t.Errorf("guard triggered for a non-agent-layout path: %s", result.ForLLM)
	}
}

// TestMetadataGuardError_StructuredJSON verifies that the guard error is valid
// JSON with the expected structure.
func TestMetadataGuardError_StructuredJSON(t *testing.T) {
	ws := setupMetadataTestWorkspace(t)
	ctx := context.Background()

	wt := tools.NewWriteFileTool(ws, false)
	result := wt.Execute(ctx, map[string]any{
		"path":      "SOUL.md",
		"content":   "blocked",
		"overwrite": true,
	})

	if !result.IsError {
		t.Fatalf("expected error, got success")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &parsed); err != nil {
		t.Fatalf("guard error is not valid JSON: %v\nbody: %s", err, result.ForLLM)
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'error' object in JSON, got: %s", result.ForLLM)
	}

	if errObj["code"] != "USE_METADATA_TOOL" {
		t.Errorf("code = %v, want USE_METADATA_TOOL", errObj["code"])
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "SOUL.md") {
		t.Errorf("message should mention SOUL.md, got: %s", msg)
	}
	if sug, _ := errObj["suggestion"].(string); !strings.Contains(sug, "update_agent") {
		t.Errorf("suggestion should mention update_agent, got: %s", sug)
	}
	if sug, _ := errObj["suggestion"].(string); !strings.Contains(sug, "soul") {
		t.Errorf("suggestion should contain soul file key, got: %s", sug)
	}
}

// assertMetadataGuardError asserts that body is a USE_METADATA_TOOL error
// pointing to the right tool name for the given op ("read" or "write").
func assertMetadataGuardError(t *testing.T, body, op string) {
	t.Helper()

	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("guard error is not valid JSON: %v\nbody: %s", err, body)
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'error' object in guard JSON, got: %s", body)
	}

	if errObj["code"] != "USE_METADATA_TOOL" {
		t.Errorf("code = %v, want USE_METADATA_TOOL; body: %s", errObj["code"], body)
	}

	sug, _ := errObj["suggestion"].(string)
	var expectedTool string
	if op == "read" {
		expectedTool = "read_agent_metadata"
	} else {
		expectedTool = "update_agent"
	}
	if !strings.Contains(sug, expectedTool) {
		t.Errorf("suggestion should mention %s for op=%q, got: %s", expectedTool, op, sug)
	}
}

// TestMetadataFileMatch_CaseSensitivity verifies the guard is case-insensitive
// for the file basename but requires the "agents" segment to exist.
func TestMetadataFileMatch_EdgeCases(t *testing.T) {
	ws := setupMetadataTestWorkspace(t)
	ctx := context.Background()

	// Absolute path to SOUL.md — should trigger the guard.
	absPath := filepath.Join(ws, "SOUL.md")
	wt := tools.NewWriteFileTool(ws, false)
	result := wt.Execute(ctx, map[string]any{
		"path":      absPath,
		"content":   "absolute path test",
		"overwrite": true,
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "USE_METADATA_TOOL") {
		t.Errorf("absolute path to SOUL.md should be blocked: %s", result.ForLLM)
	}

	// A dotslash relative path — should also trigger.
	result2 := wt.Execute(ctx, map[string]any{
		"path":      "./soul.md",
		"content":   "relative test",
		"overwrite": true,
	})
	if !result2.IsError || !strings.Contains(result2.ForLLM, "USE_METADATA_TOOL") {
		t.Errorf("./soul.md should be blocked: %s", result2.ForLLM)
	}
}

// TestMetadataGuard_SymlinkDecoyBlocked verifies the hardened guard fires when
// a decoy filename is a symlink to a canonical metadata file. Before the
// EvalSymlinks hardening, `ln -s SOUL.md decoy.md` + write_file("decoy.md")
// bypassed the guard in god-mode because the lexical basename ("decoy.md") did
// not match. The guard now resolves the symlink to SOUL.md before matching.
//
// Traces to: issue #240 METADATA-GUARD finding 1 (symlink bypass).
func TestMetadataGuard_SymlinkDecoyBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is unreliable on Windows CI without privilege")
	}
	ws := setupMetadataTestWorkspace(t)
	ctx := context.Background()

	// Create the real SOUL.md, then a decoy.md symlink pointing at it.
	soul := filepath.Join(ws, "SOUL.md")
	if err := os.WriteFile(soul, []byte("real soul"), 0o644); err != nil {
		t.Fatalf("setup soul: %v", err)
	}
	decoy := filepath.Join(ws, "decoy.md")
	if err := os.Symlink(soul, decoy); err != nil {
		t.Fatalf("setup symlink: %v", err)
	}

	// write_file via the decoy must be blocked (the symlink resolves to SOUL.md).
	wt := tools.NewWriteFileTool(ws, false)
	result := wt.Execute(ctx, map[string]any{
		"path":      "decoy.md",
		"content":   "pwned via symlink",
		"overwrite": true,
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "USE_METADATA_TOOL") {
		t.Fatalf("symlink decoy.md -> SOUL.md must be blocked, got: %s", result.ForLLM)
	}
	// SOUL.md content must be unchanged (write was rejected before any I/O).
	got, err := os.ReadFile(soul)
	if err != nil {
		t.Fatalf("read soul after blocked write: %v", err)
	}
	if string(got) != "real soul" {
		t.Errorf("SOUL.md was modified through the symlink: %q", string(got))
	}

	// read_file via the decoy must also be blocked.
	rt := tools.NewReadFileTool(ws, false, 1024)
	rres := rt.Execute(ctx, map[string]any{"path": "decoy.md"})
	if !rres.IsError || !strings.Contains(rres.ForLLM, "USE_METADATA_TOOL") {
		t.Errorf("read via symlink decoy.md -> SOUL.md must be blocked, got: %s", rres.ForLLM)
	}
}

// TestMetadataGuard_DotDotReentryBlocked verifies that a "../"-reentrant path
// that lands back on a canonical metadata file is blocked. The cleaned path
// agents/<id>/sub/../SOUL.md collapses to agents/<id>/SOUL.md.
//
// Traces to: issue #240 METADATA-GUARD finding 8 (..-reentry).
func TestMetadataGuard_DotDotReentryBlocked(t *testing.T) {
	ws := setupMetadataTestWorkspace(t)
	ctx := context.Background()

	wt := tools.NewWriteFileTool(ws, false)
	result := wt.Execute(ctx, map[string]any{
		"path":      "sub/../SOUL.md",
		"content":   "reentry bypass attempt",
		"overwrite": true,
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "USE_METADATA_TOOL") {
		t.Errorf("sub/../SOUL.md should be blocked, got: %s", result.ForLLM)
	}
}

// TestMetadataGuard_TrailingSlashBlocked verifies that a trailing slash on the
// path argument does not defeat the basename match (Clean strips it).
//
// Traces to: issue #240 METADATA-GUARD finding 8 (trailing-slash).
func TestMetadataGuard_TrailingSlashBlocked(t *testing.T) {
	ws := setupMetadataTestWorkspace(t)
	ctx := context.Background()

	wt := tools.NewWriteFileTool(ws, false)
	result := wt.Execute(ctx, map[string]any{
		"path":      "SOUL.md/",
		"content":   "trailing slash attempt",
		"overwrite": true,
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "USE_METADATA_TOOL") {
		t.Errorf("SOUL.md/ should be blocked, got: %s", result.ForLLM)
	}
}

// TestMetadataGuard_NestedAgentsDirBlocked verifies the guard fires for a
// metadata file under a deeply nested agents/<id>/ layout (e.g. when the
// workspace itself contains a nested agents tree).
//
// Traces to: issue #240 METADATA-GUARD finding 8 (nested-agents dir).
func TestMetadataGuard_NestedAgentsDirBlocked(t *testing.T) {
	home := t.TempDir()
	// Layout: <home>/workspaces/agents/nested-agent/HEARTBEAT.md, with the
	// workspace rooted at <home>/workspaces so the relative path crosses an
	// agents/<id>/ boundary.
	ws := filepath.Join(home, "workspaces")
	nested := filepath.Join(ws, "agents", "nested-agent")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("setup nested: %v", err)
	}
	ctx := context.Background()

	wt := tools.NewWriteFileTool(ws, false)
	result := wt.Execute(ctx, map[string]any{
		"path":      "agents/nested-agent/HEARTBEAT.md",
		"content":   "nested attempt",
		"overwrite": true,
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "USE_METADATA_TOOL") {
		t.Errorf("agents/nested-agent/HEARTBEAT.md should be blocked, got: %s", result.ForLLM)
	}
}

// TestMetadataGuard_FailClosedOnEmptyWorkspace verifies that the guard helper
// fails CLOSED when it cannot establish an agent workspace context: an empty
// workspace yields a USE_METADATA_TOOL deny rather than silently falling
// through to the real write. This pins the fix for the fail-OPEN finding.
//
// Traces to: issue #240 METADATA-GUARD finding 2 (fail-open) and finding 8
// (fail-closed-on-resolve-error).
func TestMetadataGuard_FailClosedOnEmptyWorkspace(t *testing.T) {
	denied := tools.GuardMetadataPathForTest("", "agents/x/SOUL.md", "write")
	if denied == nil {
		t.Fatal("guardMetadataPath with empty workspace must fail closed (deny), got nil")
	}
	if !denied.IsError || !strings.Contains(denied.ForLLM, "USE_METADATA_TOOL") {
		t.Errorf("empty-workspace guard must return USE_METADATA_TOOL deny, got: %s", denied.ForLLM)
	}
}
