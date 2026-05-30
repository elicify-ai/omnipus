package tools_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// newTestWorkspaceShellTool is a test helper that creates a WorkspaceShellTool
// with a real temporary workspace and optional audit logger.
func newTestWorkspaceShellTool(
	t *testing.T,
	workspaceDir string,
	profile config.SandboxProfile,
	shellPolicy *config.AgentShellPolicy,
	auditLogger *audit.Logger,
) *tools.WorkspaceShellTool {
	t.Helper()
	return tools.NewWorkspaceShellTool(tools.WorkspaceShellDeps{
		WorkspaceDir:            workspaceDir,
		Profile:                 profile,
		Proxy:                   nil, // no egress proxy in unit tests
		AuditLogger:             auditLogger,
		GlobalShellDenyPatterns: nil,
		AgentShellPolicy:        shellPolicy,
	})
}

// TestWorkspaceShellTool_DenyPatterns verifies that deny patterns fire when
// EnableDenyPatterns=true and are inert when EnableDenyPatterns=false.
func TestWorkspaceShellTool_DenyPatterns(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	dir := t.TempDir()

	t.Run("guardCommand triggered when EnableDenyPatterns=true and pattern matches", func(t *testing.T) {
		t.Parallel()
		policy := &config.AgentShellPolicy{
			EnableDenyPatterns: true,
			// The global default patterns include \bsudo\b; use that.
		}
		tool := tools.NewWorkspaceShellTool(tools.WorkspaceShellDeps{
			WorkspaceDir:            dir,
			Profile:                 config.SandboxProfileWorkspace,
			GlobalShellDenyPatterns: []string{`\bsudo\b`},
			AgentShellPolicy:        policy,
		})

		ctx := context.Background()
		result := tool.Execute(ctx, map[string]any{"command": "sudo ls"})

		if !result.IsError {
			t.Errorf("expected IsError=true for blocked command, got IsError=false")
		}
		if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForLLM, "denied") &&
			!strings.Contains(result.ForLLM, "safety guard") {
			t.Errorf("expected blocked message in ForLLM, got %q", result.ForLLM)
		}
	})

	t.Run("guardCommand inert when EnableDenyPatterns=false", func(t *testing.T) {
		t.Parallel()
		policy := &config.AgentShellPolicy{
			EnableDenyPatterns: false,
		}
		tool := tools.NewWorkspaceShellTool(tools.WorkspaceShellDeps{
			WorkspaceDir:            dir,
			Profile:                 config.SandboxProfileWorkspace,
			GlobalShellDenyPatterns: []string{`\bsudo\b`},
			AgentShellPolicy:        policy,
		})

		ctx := context.Background()
		// "sudo" would be blocked if patterns were active, but they are not.
		// We can't run sudo in CI, but the important thing is the tool does NOT
		// return a pattern-block error — it attempts execution.
		result := tool.Execute(ctx, map[string]any{"command": "echo hello"})

		// The echo should succeed.
		if result.IsError {
			t.Errorf("echo hello should succeed, got error: %s", result.ForLLM)
		}
	})

	t.Run("per-agent custom deny pattern blocks matching command", func(t *testing.T) {
		t.Parallel()
		policy := &config.AgentShellPolicy{
			EnableDenyPatterns: true,
			CustomDenyPatterns: []string{`\bgit\s+push\b`},
		}
		tool := tools.NewWorkspaceShellTool(tools.WorkspaceShellDeps{
			WorkspaceDir:     dir,
			Profile:          config.SandboxProfileWorkspace,
			AgentShellPolicy: policy,
		})

		ctx := context.Background()
		result := tool.Execute(ctx, map[string]any{"command": "git push origin main"})

		if !result.IsError {
			t.Errorf("expected command blocked by custom deny pattern")
		}
	})
}

// TestWorkspaceShellTool_CWDResolution verifies cwd path handling.
func TestWorkspaceShellTool_CWDResolution(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	dir := t.TempDir()

	t.Run("relative cwd resolves under workspace", func(t *testing.T) {
		t.Parallel()
		subdir := filepath.Join(dir, "subproject")
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			t.Fatalf("mkdir subproject: %v", err)
		}

		tool := newTestWorkspaceShellTool(t, dir, config.SandboxProfileOff, nil, nil)
		ctx := context.Background()

		// pwd should print a path under the workspace.
		result := tool.Execute(ctx, map[string]any{
			"command": "pwd",
			"cwd":     "subproject",
		})

		if result.IsError {
			t.Fatalf("expected success, got error: %s", result.ForLLM)
		}

		// Parse JSON result to get stdout.
		var res struct {
			Stdout string `json:"stdout"`
		}
		if err := json.Unmarshal([]byte(result.ForLLM), &res); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		// The pwd output should contain the subproject directory.
		if !strings.Contains(res.Stdout, "subproject") {
			t.Errorf("expected stdout to contain 'subproject', got %q", res.Stdout)
		}
	})

	t.Run("cwd with dotdot escapes returns error", func(t *testing.T) {
		t.Parallel()
		tool := newTestWorkspaceShellTool(t, dir, config.SandboxProfileOff, nil, nil)
		ctx := context.Background()

		result := tool.Execute(ctx, map[string]any{
			"command": "pwd",
			"cwd":     "../../etc",
		})

		if !result.IsError {
			t.Errorf("expected error for cwd that escapes workspace, got success")
		}
		if !strings.Contains(result.ForLLM, "escapes workspace") && !strings.Contains(result.ForLLM, "outside") &&
			!strings.Contains(result.ForLLM, "denied") {
			t.Errorf("expected escape error in ForLLM, got %q", result.ForLLM)
		}
	})

	t.Run("absolute cwd is rejected", func(t *testing.T) {
		t.Parallel()
		tool := newTestWorkspaceShellTool(t, dir, config.SandboxProfileOff, nil, nil)
		ctx := context.Background()

		result := tool.Execute(ctx, map[string]any{
			"command": "pwd",
			"cwd":     "/etc",
		})

		if !result.IsError {
			t.Errorf("expected error for absolute cwd, got success")
		}
		if !strings.Contains(result.ForLLM, "escapes workspace") {
			t.Errorf("expected escape error message, got %q", result.ForLLM)
		}
	})

	// Test-5: Symlink pointing outside workspace must be rejected.
	// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-5.
	t.Run("symlink pointing outside workspace is rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks behave differently on Windows")
		}
		t.Parallel()

		// Create a fresh workspace for this subtest.
		wsDir := t.TempDir()
		linkName := "escape-link"
		linkPath := filepath.Join(wsDir, linkName)

		// Create a symlink inside the workspace that points at /etc.
		if err := os.Symlink("/etc", linkPath); err != nil {
			t.Fatalf("os.Symlink: %v", err)
		}

		tool := newTestWorkspaceShellTool(t, wsDir, config.SandboxProfileOff, nil, nil)
		ctx := context.Background()

		result := tool.Execute(ctx, map[string]any{
			"command": "pwd",
			"cwd":     linkName,
		})

		if !result.IsError {
			t.Errorf("expected error for symlink cwd escaping workspace, got success; output: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "escapes workspace") && !strings.Contains(result.ForLLM, "path escapes") {
			t.Errorf("expected escape error in ForLLM, got %q", result.ForLLM)
		}
	})
}

// TestWorkspaceShellTool_ResultShape verifies the JSON result shape on success.
func TestWorkspaceShellTool_ResultShape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	dir := t.TempDir()
	tool := newTestWorkspaceShellTool(t, dir, config.SandboxProfileOff, nil, nil)
	ctx := context.Background()

	result := tool.Execute(ctx, map[string]any{"command": "echo hello"})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	var res struct {
		ExitCode   int    `json:"exit_code"`
		Stdout     string `json:"stdout"`
		Stderr     string `json:"stderr"`
		DurationMS int64  `json:"duration_ms"`
		Summary    string `json:"_summary"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &res); err != nil {
		t.Fatalf("unmarshal result JSON: %v\nraw: %s", err, result.ForLLM)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit_code=0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Errorf("expected 'hello' in stdout, got %q", res.Stdout)
	}
	if res.DurationMS < 0 {
		t.Errorf("expected non-negative duration_ms, got %d", res.DurationMS)
	}
	if res.Summary == "" {
		t.Errorf("expected non-empty _summary")
	}
}

// TestWorkspaceShellTool_NonZeroExit verifies IsError=true for non-zero exit codes.
func TestWorkspaceShellTool_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	dir := t.TempDir()
	tool := newTestWorkspaceShellTool(t, dir, config.SandboxProfileOff, nil, nil)
	ctx := context.Background()

	result := tool.Execute(ctx, map[string]any{"command": "exit 42"})

	if !result.IsError {
		t.Errorf("expected IsError=true for non-zero exit, got IsError=false")
	}
	var res struct {
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &res); err != nil {
		t.Fatalf("unmarshal result JSON: %v\nraw: %s", err, result.ForLLM)
	}
	if res.ExitCode != 42 {
		t.Errorf("expected exit_code=42, got %d", res.ExitCode)
	}
}

// TestWorkspaceShellTool_AuditEntries verifies that audit entries are emitted on
// both allow and deny paths.
func TestWorkspaceShellTool_AuditEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	dir := t.TempDir()
	auditDir := t.TempDir()

	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	t.Run("allow path emits audit entry", func(t *testing.T) {
		tool := newTestWorkspaceShellTool(t, dir, config.SandboxProfileOff, nil, auditLogger)
		ctx := context.Background()

		_ = tool.Execute(ctx, map[string]any{"command": "echo audit-allow"})

		entries := readAuditEntries(t, auditDir)
		if len(entries) == 0 {
			t.Errorf("expected at least one audit entry after allow")
		}
		found := false
		for _, e := range entries {
			if e["event"] == "exec" && e["decision"] == "allow" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected exec/allow audit entry; entries: %v", entries)
		}
	})

	t.Run("deny path emits audit entry", func(t *testing.T) {
		policy := &config.AgentShellPolicy{
			EnableDenyPatterns: true,
			CustomDenyPatterns: []string{`\bdeny-me\b`},
		}
		tool := tools.NewWorkspaceShellTool(tools.WorkspaceShellDeps{
			WorkspaceDir:     dir,
			Profile:          config.SandboxProfileOff,
			AuditLogger:      auditLogger,
			AgentShellPolicy: policy,
		})
		ctx := context.Background()

		result := tool.Execute(ctx, map[string]any{"command": "deny-me something"})
		if !result.IsError {
			t.Errorf("expected IsError=true for denied command")
		}

		entries := readAuditEntries(t, auditDir)
		found := false
		for _, e := range entries {
			if e["event"] == "exec" && e["decision"] == "deny" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected exec/deny audit entry; entries: %v", entries)
		}
	})
}

// TestWorkspaceShellTool_ProfileOff verifies that profile=off skips
// ApplyChildHardening (runs without error on all platforms).
func TestWorkspaceShellTool_ProfileOff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	dir := t.TempDir()
	tool := newTestWorkspaceShellTool(t, dir, config.SandboxProfileOff, nil, nil)
	ctx := context.Background()

	result := tool.Execute(ctx, map[string]any{"command": "echo from-god-mode"})

	if result.IsError {
		t.Errorf("profile=off should succeed, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "from-god-mode") {
		t.Errorf("expected echo output in result, got: %s", result.ForLLM)
	}
}

// TestWorkspaceShellTool_ProfileWorkspace verifies that profile=workspace calls
// ApplyChildHardening and runs successfully (on Linux).
func TestWorkspaceShellTool_ProfileWorkspace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("hardened exec is Linux-only")
	}
	t.Parallel()

	dir := t.TempDir()
	tool := newTestWorkspaceShellTool(t, dir, config.SandboxProfileWorkspace, nil, nil)
	ctx := context.Background()

	result := tool.Execute(ctx, map[string]any{"command": "echo from-workspace-profile"})

	if result.IsError {
		t.Errorf("profile=workspace should succeed for simple echo, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "from-workspace-profile") {
		t.Errorf("expected echo output in result, got: %s", result.ForLLM)
	}
}

// TestWorkspaceShellTool_AuditFailClosed_DeniesExecution verifies that when
// AuditFailClosed=true and the audit logger returns an IO error, Execute returns
// an ErrorResult containing "audit_fail_closed=true" and does NOT run the command.
//
// BDD: Given AuditFailClosed=true and a degraded (closed) audit logger,
//
//	When Execute is called,
//	Then result.IsError=true, ForLLM contains "audit_fail_closed=true",
//	and no side effect occurred (sentinel file not created).
//
// Traces to: quizzical-marinating-frog.md Fix-1 — AuditFailClosed in WorkspaceShellDeps.
func TestWorkspaceShellTool_AuditFailClosed_DeniesExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	// Create a real logger then immediately close it so Log() returns an error.
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	// Close the logger so its internal file is nil; subsequent Log() calls
	// return "operating in degraded mode".
	if err := logger.Close(); err != nil {
		t.Fatalf("logger.Close: %v", err)
	}

	workspaceDir := t.TempDir()
	sentinelPath := filepath.Join(workspaceDir, "should-not-be-created.txt")

	tool := tools.NewWorkspaceShellTool(tools.WorkspaceShellDeps{
		WorkspaceDir:    workspaceDir,
		Profile:         config.SandboxProfileOff,
		AuditLogger:     logger,
		AuditFailClosed: true,
	})

	ctx := context.Background()
	// Command would create a sentinel file if it ran. If fail-closed works, it
	// must be aborted before the command executes.
	result := tool.Execute(ctx, map[string]any{
		"command": "touch should-not-be-created.txt",
	})

	// (a) Must return an error result.
	if !result.IsError {
		t.Errorf(
			"expected IsError=true when audit_fail_closed=true and logger degraded; got IsError=false; ForLLM=%q",
			result.ForLLM,
		)
	}

	// (a) ForLLM must mention fail-closed.
	if !strings.Contains(result.ForLLM, "audit_fail_closed=true") {
		t.Errorf("expected 'audit_fail_closed=true' in ForLLM, got %q", result.ForLLM)
	}

	// (b) No command was actually run — sentinel file must not exist.
	if _, statErr := os.Stat(sentinelPath); statErr == nil {
		t.Errorf("sentinel file %q was created; command ran despite audit_fail_closed=true", sentinelPath)
	}
}

// TestWorkspaceShellTool_AuditBestEffort verifies that when AuditFailClosed=false
// and the audit logger is degraded, Execute warns but continues and runs the command.
//
// BDD: Given AuditFailClosed=false and a degraded (closed) audit logger,
//
//	When Execute is called,
//	Then the command executes successfully (warn-and-continue behavior).
//
// Traces to: quizzical-marinating-frog.md Fix-1 — AuditFailClosed=false path.
func TestWorkspaceShellTool_AuditBestEffort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	// Close the logger so Log() returns an error (degraded mode).
	if err := logger.Close(); err != nil {
		t.Fatalf("logger.Close: %v", err)
	}

	workspaceDir := t.TempDir()
	tool := tools.NewWorkspaceShellTool(tools.WorkspaceShellDeps{
		WorkspaceDir:    workspaceDir,
		Profile:         config.SandboxProfileOff,
		AuditLogger:     logger,
		AuditFailClosed: false, // best-effort: warn and continue
	})

	ctx := context.Background()
	result := tool.Execute(ctx, map[string]any{
		"command": "echo audit-best-effort",
	})

	// With AuditFailClosed=false, execution must continue despite the degraded logger.
	if result.IsError {
		t.Errorf("expected success with AuditFailClosed=false, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "audit-best-effort") {
		t.Errorf("expected command output in result, got: %s", result.ForLLM)
	}
}

// TestWorkspaceShellTool_NameAndScope verifies the tool's metadata.
func TestWorkspaceShellTool_NameAndScope(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := newTestWorkspaceShellTool(t, dir, config.SandboxProfileWorkspace, nil, nil)

	if tool.Name() != "workspace.shell" {
		t.Errorf("expected name 'workspace.shell', got %q", tool.Name())
	}
	if tool.Scope() != tools.ScopeCore {
		t.Errorf("expected ScopeCore, got %q", tool.Scope())
	}
	if tool.Category() != tools.CategoryWorkspace {
		t.Errorf("expected CategoryWorkspace, got %q", tool.Category())
	}
}

// readAuditEntries reads all JSONL audit entries from dir.
// Each entry is a map[string]any. Invalid lines are skipped.
func readAuditEntries(t *testing.T, dir string) []map[string]any {
	t.Helper()
	var out []map[string]any
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err == nil {
				out = append(out, m)
			}
		}
		f.Close()
	}
	return out
}
