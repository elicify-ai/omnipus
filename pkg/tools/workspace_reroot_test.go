package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover per-turn workspace re-rooting at the tool layer: when a
// per-turn workspace dir is carried on the context (via WithTurnWorkspaceDir,
// which the agent loop sets when the turn's agent belongs to a Workspace's
// CoreTeam — see workspace.FindForAgent), the file/exec tools resolve paths
// against that dir instead of their fixed agent root. When no turn workspace
// dir is present, the tools behave byte-for-byte as before.

// agentRoot + workspaceRoot model the two sibling subtrees under $OMNIPUS_HOME.
func rerootDirs(t *testing.T) (home, agentDir, wsDir string) {
	t.Helper()
	home = t.TempDir()
	agentDir = filepath.Join(home, "agents", "agent-A")
	wsDir = filepath.Join(home, "workspaces", "ws-1")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agentDir: %v", err)
	}
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir wsDir: %v", err)
	}
	return home, agentDir, wsDir
}

// TestWriteFile_FlagOff_LandsInAgentHome verifies that with NO turn workspace
// dir on the context, write_file lands in the fixed agent root — unchanged.
func TestWriteFile_FlagOff_LandsInAgentHome(t *testing.T) {
	_, agentDir, wsDir := rerootDirs(t)
	tool := NewWriteFileTool(agentDir, true /*restrict*/)

	res := tool.Execute(context.Background(), map[string]any{
		"path":    "out.txt",
		"content": "hello",
	})
	if res.IsError {
		t.Fatalf("write failed: %s", res.ForLLM)
	}

	// File must exist in the agent dir, NOT the workspace dir.
	if _, err := os.Stat(filepath.Join(agentDir, "out.txt")); err != nil {
		t.Fatalf("expected file in agent dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wsDir, "out.txt")); err == nil {
		t.Fatalf("file unexpectedly landed in workspace dir with flag off")
	}
}

// TestWriteFile_FlagOn_LandsInWorkspace verifies that WITH a turn workspace dir
// on the context, write_file re-roots into workspaces/<id>/.
func TestWriteFile_FlagOn_LandsInWorkspace(t *testing.T) {
	_, agentDir, wsDir := rerootDirs(t)
	tool := NewWriteFileTool(agentDir, true /*restrict*/)

	ctx := WithTurnWorkspaceDir(context.Background(), wsDir)
	res := tool.Execute(ctx, map[string]any{
		"path":    "out.txt",
		"content": "hello-ws",
	})
	if res.IsError {
		t.Fatalf("write failed: %s", res.ForLLM)
	}

	// File must exist in the workspace dir, NOT the agent dir.
	got, err := os.ReadFile(filepath.Join(wsDir, "out.txt"))
	if err != nil {
		t.Fatalf("expected file in workspace dir: %v", err)
	}
	if string(got) != "hello-ws" {
		t.Fatalf("workspace file content = %q, want %q", got, "hello-ws")
	}
	if _, err := os.Stat(filepath.Join(agentDir, "out.txt")); err == nil {
		t.Fatalf("file unexpectedly landed in agent dir when re-rooted")
	}
}

// TestWriteFile_FlagOn_EmptyWorkspaceID_FallsBack verifies that an empty turn
// workspace dir (the loop never sets it to "" — but defensively) falls back to
// the agent root, matching "flag on + no workspace_id".
func TestWriteFile_EmptyTurnDir_FallsBack(t *testing.T) {
	_, agentDir, _ := rerootDirs(t)
	tool := NewWriteFileTool(agentDir, true)

	// WithTurnWorkspaceDir("") is a no-op, so the ctx carries no turn dir.
	ctx := WithTurnWorkspaceDir(context.Background(), "")
	if got := TurnWorkspaceDir(ctx); got != "" {
		t.Fatalf("TurnWorkspaceDir after empty set = %q, want \"\"", got)
	}
	res := tool.Execute(ctx, map[string]any{"path": "fb.txt", "content": "x"})
	if res.IsError {
		t.Fatalf("write failed: %s", res.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "fb.txt")); err != nil {
		t.Fatalf("expected fallback file in agent dir: %v", err)
	}
}

// TestReadEditAppend_FlagOn_TargetWorkspace verifies read/edit/append all
// re-root to the workspace dir.
func TestReadEditAppend_FlagOn_TargetWorkspace(t *testing.T) {
	_, agentDir, wsDir := rerootDirs(t)
	ctx := WithTurnWorkspaceDir(context.Background(), wsDir)

	// Seed a file directly in the workspace dir.
	if err := os.WriteFile(filepath.Join(wsDir, "f.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	read := NewReadFileTool(agentDir, true, MaxReadFileSize)
	rr := read.Execute(ctx, map[string]any{"path": "f.txt"})
	if rr.IsError || !strings.Contains(rr.ForLLM, "alpha") {
		t.Fatalf("read did not see workspace file: err=%v out=%s", rr.IsError, rr.ForLLM)
	}

	edit := NewEditFileTool(agentDir, true)
	er := edit.Execute(ctx, map[string]any{"path": "f.txt", "old_text": "alpha", "new_text": "beta"})
	if er.IsError {
		t.Fatalf("edit failed: %s", er.ForLLM)
	}

	app := NewAppendFileTool(agentDir, true)
	ar := app.Execute(ctx, map[string]any{"path": "f.txt", "content": "-gamma"})
	if ar.IsError {
		t.Fatalf("append failed: %s", ar.ForLLM)
	}

	got, err := os.ReadFile(filepath.Join(wsDir, "f.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "beta-gamma" {
		t.Fatalf("workspace file = %q, want %q", got, "beta-gamma")
	}
}

// TestListDir_FlagOn_ListsWorkspace verifies list_directory re-roots so "." is
// the workspace dir, not the agent dir.
func TestListDir_FlagOn_ListsWorkspace(t *testing.T) {
	_, agentDir, wsDir := rerootDirs(t)
	if err := os.WriteFile(filepath.Join(wsDir, "only-in-ws.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "only-in-agent.txt"), []byte("y"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := NewListDirTool(agentDir, true)
	ctx := WithTurnWorkspaceDir(context.Background(), wsDir)
	res := tool.Execute(ctx, map[string]any{"path": "."})
	if res.IsError {
		t.Fatalf("list failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "only-in-ws.txt") {
		t.Fatalf("listing did not include workspace file: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "only-in-agent.txt") {
		t.Fatalf("listing leaked agent-dir file when re-rooted: %s", res.ForLLM)
	}
}

// TestWriteFile_FlagOn_CannotEscapeWorkspace verifies the os.Root confinement
// still applies relative to the re-rooted dir: a traversal out of the workspace
// is denied.
func TestWriteFile_FlagOn_CannotEscapeWorkspace(t *testing.T) {
	_, agentDir, wsDir := rerootDirs(t)
	tool := NewWriteFileTool(agentDir, true)
	ctx := WithTurnWorkspaceDir(context.Background(), wsDir)

	res := tool.Execute(ctx, map[string]any{
		"path":    "../../agents/agent-A/escape.txt",
		"content": "nope",
	})
	if !res.IsError {
		t.Fatalf("expected traversal out of re-rooted workspace to be denied, got success")
	}
	if _, err := os.Stat(filepath.Join(agentDir, "escape.txt")); err == nil {
		t.Fatalf("traversal escaped the re-rooted workspace and wrote into agent dir")
	}
}

// TestExecCwd_ReflectsEffectiveRoot proves exec runs with cwd = the re-rooted
// workspace dir when a turn workspace dir is present, and = the agent dir when
// it is not. Uses sandbox=off (legacy sh -c path) so the test needs no kernel
// Landlock support; the cwd-selection logic is identical on both paths.
func TestExecCwd_ReflectsEffectiveRoot(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("pwd-based cwd assertion is POSIX-only")
	}
	_, agentDir, wsDir := rerootDirs(t)
	tool, err := NewExecTool(agentDir, true /*restrict*/)
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}

	// Resolve symlinks because t.TempDir() on macOS/Linux may be under /private
	// or /var symlinks, and the shell's pwd reports the resolved path.
	wantWS, _ := filepath.EvalSymlinks(wsDir)
	wantAgent, _ := filepath.EvalSymlinks(agentDir)

	// Flag ON: cwd should be the workspace dir.
	ctxWS := WithTurnWorkspaceDir(context.Background(), wsDir)
	res := tool.Execute(ctxWS, map[string]any{"action": "run", "command": "pwd"})
	if res.IsError {
		t.Fatalf("exec(pwd) re-rooted failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, wantWS) {
		t.Fatalf("re-rooted exec cwd = %q, want to contain %q", strings.TrimSpace(res.ForLLM), wantWS)
	}

	// Flag OFF (no turn dir): cwd should be the agent dir.
	res = tool.Execute(context.Background(), map[string]any{"action": "run", "command": "pwd"})
	if res.IsError {
		t.Fatalf("exec(pwd) fixed-root failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, wantAgent) {
		t.Fatalf("fixed-root exec cwd = %q, want to contain %q", strings.TrimSpace(res.ForLLM), wantAgent)
	}
}

// ---------------------------------------------------------------------------
// GAP 1: read_file + exec fallback when turn dir is empty
// ---------------------------------------------------------------------------

// TestReadFile_EmptyTurnDir_FallsBack verifies that with NO turn workspace dir
// on the context (empty string → no-op), read_file reads from the fixed agent
// root, not a workspace. Mirrors TestWriteFile_EmptyTurnDir_FallsBack.
//
// Traces to: workspace_reroot_test.go — gap 1 (read fallback, no BDD scenario
// yet); mirrors TestWriteFile_EmptyTurnDir_FallsBack pattern.
func TestReadFile_EmptyTurnDir_FallsBack(t *testing.T) {
	_, agentDir, _ := rerootDirs(t)

	// Seed a file in the agent dir.
	if err := os.WriteFile(filepath.Join(agentDir, "agent-only.txt"), []byte("agent-data"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := NewReadFileTool(agentDir, true, MaxReadFileSize)

	// WithTurnWorkspaceDir("") is a no-op — ctx carries no turn dir.
	ctx := WithTurnWorkspaceDir(context.Background(), "")
	if got := TurnWorkspaceDir(ctx); got != "" {
		t.Fatalf("TurnWorkspaceDir after empty set = %q, want \"\"", got)
	}

	res := tool.Execute(ctx, map[string]any{"path": "agent-only.txt"})
	if res.IsError {
		t.Fatalf("read failed (fallback): %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "agent-data") {
		t.Fatalf("read fallback content = %q, want to contain %q", res.ForLLM, "agent-data")
	}
}

// TestExecCwd_EmptyTurnDir_FallsBackToAgentHome verifies that with NO turn
// workspace dir on the context, exec cwd is the fixed agent dir, not any
// workspace. Mirrors TestWriteFile_EmptyTurnDir_FallsBack for exec.
//
// Traces to: workspace_reroot_test.go — gap 1 (exec fallback, no BDD scenario
// yet); mirrors TestExecCwd_ReflectsEffectiveRoot "Flag OFF" sub-case but
// exercises the explicit-empty-string path through WithTurnWorkspaceDir.
func TestExecCwd_EmptyTurnDir_FallsBackToAgentHome(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("pwd-based cwd assertion is POSIX-only")
	}
	_, agentDir, _ := rerootDirs(t)
	tool, err := NewExecTool(agentDir, true)
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}

	wantAgent, _ := filepath.EvalSymlinks(agentDir)

	// Explicit empty string → no-op, same as not setting it at all.
	ctx := WithTurnWorkspaceDir(context.Background(), "")
	res := tool.Execute(ctx, map[string]any{"action": "run", "command": "pwd"})
	if res.IsError {
		t.Fatalf("exec(pwd) empty-turn-dir failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, wantAgent) {
		t.Fatalf("exec fallback cwd = %q, want to contain %q", strings.TrimSpace(res.ForLLM), wantAgent)
	}
}

// ---------------------------------------------------------------------------
// GAP 2: read_file + list_directory traversal-escape under re-root
// ---------------------------------------------------------------------------

// TestReadFile_FlagOn_CannotEscapeWorkspace verifies that os.Root confinement
// applies to read_file when re-rooted: a traversal path that would exit the
// workspace dir is denied. Mirrors TestWriteFile_FlagOn_CannotEscapeWorkspace.
//
// Traces to: workspace_reroot_test.go — gap 2 (read escape, no BDD scenario
// yet); mirrors TestWriteFile_FlagOn_CannotEscapeWorkspace.
func TestReadFile_FlagOn_CannotEscapeWorkspace(t *testing.T) {
	_, agentDir, wsDir := rerootDirs(t)

	// Seed a secret file in the agent dir — OUTSIDE the workspace.
	secretPath := filepath.Join(agentDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("do-not-read"), 0o644); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	tool := NewReadFileTool(agentDir, true, MaxReadFileSize)
	ctx := WithTurnWorkspaceDir(context.Background(), wsDir)

	// Attempt traversal out of the re-rooted workspace.
	res := tool.Execute(ctx, map[string]any{"path": "../../agents/agent-A/secret.txt"})
	if !res.IsError {
		t.Fatalf("expected traversal out of re-rooted workspace to be denied, got success: %s", res.ForLLM)
	}
	// The secret content must NOT appear in the response.
	if strings.Contains(res.ForLLM, "do-not-read") {
		t.Fatalf("traversal escaped workspace: secret content leaked in response: %s", res.ForLLM)
	}
}

// TestListDir_FlagOn_CannotEscapeWorkspace verifies that os.Root confinement
// applies to list_directory when re-rooted: a traversal path that would exit
// the workspace dir is denied. Mirrors TestWriteFile_FlagOn_CannotEscapeWorkspace.
//
// Traces to: workspace_reroot_test.go — gap 2 (list escape, no BDD scenario
// yet); mirrors TestWriteFile_FlagOn_CannotEscapeWorkspace.
func TestListDir_FlagOn_CannotEscapeWorkspace(t *testing.T) {
	_, agentDir, wsDir := rerootDirs(t)

	// Seed a file only in the agent dir — OUTSIDE the workspace.
	if err := os.WriteFile(filepath.Join(agentDir, "agent-secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Workspace is empty by contrast.

	tool := NewListDirTool(agentDir, true)
	ctx := WithTurnWorkspaceDir(context.Background(), wsDir)

	// Attempt to list a directory outside the re-rooted workspace via traversal.
	res := tool.Execute(ctx, map[string]any{"path": "../../agents/agent-A"})
	if !res.IsError {
		t.Fatalf("expected traversal listing out of re-rooted workspace to be denied, got success: %s", res.ForLLM)
	}
	// Contents of the agent dir must not appear.
	if strings.Contains(res.ForLLM, "agent-secret.txt") {
		t.Fatalf("traversal escaped workspace: agent-dir listing leaked in response: %s", res.ForLLM)
	}
}

// ---------------------------------------------------------------------------
// GAP 3: exec cwd-argument escape guard under re-root
// ---------------------------------------------------------------------------

// TestExecCwd_FlagOn_EscapeViaExplicitCwdBlocked verifies that bash's
// app-level cwd safety guard (resolveCWD -> ResolvePath, which performs
// carve-out checking, workspace-containment, and symlink re-resolution in
// one chokepoint as of ADR-046 P1 — resolveCWD previously called the since-
// retired validatePathWithAllowPaths, ADR-036/FR-B2/FR-B13) blocks an
// explicit `cwd` argument that escapes the re-rooted workspace, even when
// sandbox=off
// (no kernel Landlock). The guard is app-level and runs regardless of sandbox
// mode. Mirrors TestExecCwd_ReflectsEffectiveRoot for sandbox mode setup.
//
// Traces to: shell.go's resolveCWD (cwd validation under re-root);
// workspace_reroot_test.go — gap 3 (exec escape via explicit cwd, no BDD
// scenario yet).
func TestExecCwd_FlagOn_EscapeViaExplicitCwdBlocked(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("path-escape guard assertion is POSIX-only")
	}
	_, agentDir, wsDir := rerootDirs(t)
	tool, err := NewExecTool(agentDir, true /*restrict*/)
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}

	ctx := WithTurnWorkspaceDir(context.Background(), wsDir)

	// Explicit cwd argument that attempts to escape the re-rooted workspace via
	// relative traversal. The safety guard resolves this relative to baseDir
	// (wsDir after re-rooting) and must reject it.
	res := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "pwd",
		"cwd":     "../../agents/agent-A",
	})
	if !res.IsError {
		t.Fatalf("expected exec cwd-escape to be blocked by safety guard, got success: %s", res.ForLLM)
	}
	// Error message must mention the escape so callers know why it failed
	// (ADR-036 standardized this wording — see resolveCWD in shell.go).
	if !strings.Contains(res.ForLLM, "escapes workspace") {
		t.Fatalf("expected 'escapes workspace' in error, got: %s", res.ForLLM)
	}
}
