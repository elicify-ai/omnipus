package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// shellBgResult mirrors the JSON shape workspace.shell_bg returns.
// Must match RunInWorkspaceResult in src/lib/api.ts (path, url, expires_at,
// command, port) plus _summary.
type shellBgResult struct {
	Path      string  `json:"path"`
	URL       string  `json:"url"`
	ExpiresAt string  `json:"expires_at"`
	Command   string  `json:"command"`
	Port      float64 `json:"port"` // JSON numbers decode as float64
	Summary   string  `json:"_summary"`
}

// newTestShellBgTool creates a WorkspaceShellBgTool for unit tests.
// registry, proxy, and auditLogger may be nil (will default or skip).
func newTestShellBgTool(
	t *testing.T,
	workspaceDir string,
	profile config.SandboxProfile,
	registry *sandbox.DevServerRegistry,
	auditLogger *audit.Logger,
	gatewayHost string,
) *tools.WorkspaceShellBgTool {
	t.Helper()
	return tools.NewWorkspaceShellBgTool(tools.WorkspaceShellBgDeps{
		WorkspaceDir:    workspaceDir,
		Profile:         profile,
		Proxy:           nil,
		AuditLogger:     auditLogger,
		AuditFailClosed: false, // don't fail closed in unit tests
		Registry:        registry,
		MaxConcurrent:   2,
		PortRange:       [2]int32{18000, 18999},
		GatewayHost:     gatewayHost,
	})
}

// TestWorkspaceShellBgTool_NonLinuxReturnsError verifies the Linux gate.
func TestWorkspaceShellBgTool_NonLinuxReturnsError(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux: this test verifies non-Linux behavior only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := newTestShellBgTool(t, t.TempDir(), config.SandboxProfileOff, reg, nil, "")
	ctx := tools.WithAgentID(context.Background(), "test-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 1",
		"expose_port": float64(18000),
	})

	if !result.IsError {
		t.Errorf("expected IsError=true on non-Linux, got false")
	}
}

// TestWorkspaceShellBgTool_NilRegistryReturnsError verifies defense-in-depth
// when the registry is not wired.
func TestWorkspaceShellBgTool_NilRegistryReturnsError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only — nil registry check is behind the Linux gate")
	}
	t.Parallel()

	tool := newTestShellBgTool(t, t.TempDir(), config.SandboxProfileOff, nil /* nil registry */, nil, "")
	ctx := tools.WithAgentID(context.Background(), "test-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 1",
		"expose_port": float64(18000),
	})

	if !result.IsError {
		t.Errorf("expected IsError=true for nil registry")
	}
	if !strings.Contains(result.ForLLM, "registry not configured") {
		t.Errorf("expected 'registry not configured' in error, got %q", result.ForLLM)
	}
}

// TestWorkspaceShellBgTool_PortOutOfRange verifies port range validation.
func TestWorkspaceShellBgTool_PortOutOfRange(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := newTestShellBgTool(t, t.TempDir(), config.SandboxProfileOff, reg, nil, "")
	ctx := tools.WithAgentID(context.Background(), "test-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 1",
		"expose_port": float64(80), // outside [18000, 18999]
	})

	if !result.IsError {
		t.Errorf("expected IsError=true for out-of-range port")
	}
	if !strings.Contains(result.ForLLM, "allowed range") {
		t.Errorf("expected 'allowed range' in error, got %q", result.ForLLM)
	}
}

// TestWorkspaceShellBgTool_CWDEscapeRejected verifies that a cwd argument
// that escapes the workspace returns an error.
func TestWorkspaceShellBgTool_CWDEscapeRejected(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := newTestShellBgTool(t, t.TempDir(), config.SandboxProfileOff, reg, nil, "")
	ctx := tools.WithAgentID(context.Background(), "test-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 1",
		"expose_port": float64(18000),
		"cwd":         "../../etc",
	})

	if !result.IsError {
		t.Errorf("expected IsError=true for cwd escaping workspace")
	}
}

// TestWorkspaceShellBgTool_AbsoluteCWDRejected verifies that an absolute cwd
// argument is rejected.
func TestWorkspaceShellBgTool_AbsoluteCWDRejected(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := newTestShellBgTool(t, t.TempDir(), config.SandboxProfileOff, reg, nil, "")
	ctx := tools.WithAgentID(context.Background(), "test-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 1",
		"expose_port": float64(18000),
		"cwd":         "/etc",
	})

	if !result.IsError {
		t.Errorf("expected IsError=true for absolute cwd")
	}
}

// TestWorkspaceShellBgTool_DenyPatternBlocks verifies that deny patterns fire
// when EnableDenyPatterns=true.
func TestWorkspaceShellBgTool_DenyPatternBlocks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := tools.NewWorkspaceShellBgTool(tools.WorkspaceShellBgDeps{
		WorkspaceDir:            t.TempDir(),
		Profile:                 config.SandboxProfileOff,
		Registry:                reg,
		MaxConcurrent:           2,
		PortRange:               [2]int32{18000, 18999},
		AuditFailClosed:         false,
		GlobalShellDenyPatterns: []string{`\bsudo\b`},
		AgentShellPolicy: &config.AgentShellPolicy{
			EnableDenyPatterns: true,
		},
	})
	ctx := tools.WithAgentID(context.Background(), "deny-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sudo sleep 1",
		"expose_port": float64(18000),
	})

	if !result.IsError {
		t.Errorf("expected IsError=true for deny-pattern match")
	}
	if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForLLM, "safety guard") {
		t.Errorf("expected safety guard message, got %q", result.ForLLM)
	}
}

// TestWorkspaceShellBgTool_DenyPatternInertWhenDisabled verifies that deny
// patterns are inert when EnableDenyPatterns=false (the default).
func TestWorkspaceShellBgTool_DenyPatternInertWhenDisabled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	dir := t.TempDir()
	tool := tools.NewWorkspaceShellBgTool(tools.WorkspaceShellBgDeps{
		WorkspaceDir:            dir,
		Profile:                 config.SandboxProfileOff,
		Registry:                reg,
		MaxConcurrent:           2,
		PortRange:               [2]int32{18000, 18999},
		AuditFailClosed:         false,
		GlobalShellDenyPatterns: []string{`\bsudo\b`},
		AgentShellPolicy: &config.AgentShellPolicy{
			EnableDenyPatterns: false, // inert
		},
	})
	ctx := tools.WithAgentID(context.Background(), "inert-deny-agent")

	// "sleep" should start regardless of the pattern because patterns are disabled.
	// We use a background-spawnable command; immediately cancel after verification.
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	result := tool.Execute(cancelCtx, map[string]any{
		"command":     "sleep 60",
		"expose_port": float64(18001),
	})

	// The command itself won't be blocked; it may succeed or fail at the OS
	// level for other reasons (e.g. missing binary on test host), but it MUST
	// NOT fail with a deny-pattern message.
	if result.IsError &&
		(strings.Contains(result.ForLLM, "blocked") || strings.Contains(result.ForLLM, "safety guard")) {
		t.Errorf("deny patterns should be inert; got block message: %q", result.ForLLM)
	}

	// Clean up any registered server.
	reg.UnregisterByAgent("inert-deny-agent")
}

// TestWorkspaceShellBgTool_DevURLShape verifies that a successful spawn
// returns JSON with all required fields matching RunInWorkspaceResult shape.
//
// This test spawns `sleep 60` as a stand-in dev server and immediately
// terminates it via the registry.
func TestWorkspaceShellBgTool_DevURLShape(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	dir := t.TempDir()
	gatewayHost := "https://preview.example.com"
	tool := newTestShellBgTool(t, dir, config.SandboxProfileOff, reg, nil, gatewayHost)

	ctx := tools.WithAgentID(context.Background(), "shape-agent")
	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 60",
		"expose_port": float64(18000),
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}

	var parsed shellBgResult
	if err := json.Unmarshal([]byte(result.ForLLM), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v\nraw: %s", err, result.ForLLM)
	}

	// path must start with /dev/ and end with /
	if !strings.HasPrefix(parsed.Path, "/dev/") {
		t.Errorf("path = %q; want prefix /dev/", parsed.Path)
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		t.Errorf("path = %q; want trailing /", parsed.Path)
	}

	// url must start with gatewayHost and end with path
	if !strings.HasPrefix(parsed.URL, gatewayHost) {
		t.Errorf("url = %q; want prefix %q", parsed.URL, gatewayHost)
	}
	if !strings.HasSuffix(parsed.URL, parsed.Path) {
		t.Errorf("url = %q; does not end with path %q", parsed.URL, parsed.Path)
	}

	// expires_at must be a non-empty RFC3339 string
	if parsed.ExpiresAt == "" {
		t.Errorf("expires_at is empty")
	}
	if _, err := time.Parse(time.RFC3339, parsed.ExpiresAt); err != nil {
		t.Errorf("expires_at %q is not RFC3339: %v", parsed.ExpiresAt, err)
	}

	// command must be echoed back
	if parsed.Command != "sleep 60" {
		t.Errorf("command = %q; want %q", parsed.Command, "sleep 60")
	}

	// port must be numeric (18000)
	if int(parsed.Port) != 18000 {
		t.Errorf("port = %v; want 18000", parsed.Port)
	}

	// Clean up.
	reg.UnregisterByAgent("shape-agent")
}

// TestWorkspaceShellBgTool_TokenRegisteredInRegistry verifies that the
// token embedded in the result URL is registered in the DevServerRegistry.
func TestWorkspaceShellBgTool_TokenRegisteredInRegistry(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	dir := t.TempDir()
	tool := newTestShellBgTool(t, dir, config.SandboxProfileOff, reg, nil, "")

	ctx := tools.WithAgentID(context.Background(), "reg-agent")
	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 60",
		"expose_port": float64(18000),
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}

	var parsed shellBgResult
	if err := json.Unmarshal([]byte(result.ForLLM), &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Extract the token from the path: /dev/<agent>/<token>/
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 3 {
		t.Fatalf("unexpected path format: %q", parsed.Path)
	}
	token := parts[2]

	// The token should exist in the registry.
	reg2 := reg.Lookup(token)
	if reg2 == nil {
		t.Errorf("token %q not found in DevServerRegistry after successful spawn", token)
	}

	// Clean up.
	reg.UnregisterByAgent("reg-agent")
}

// TestWorkspaceShellBgTool_URLEmptyGatewayHost verifies path-only URL when
// gatewayHost is empty.
func TestWorkspaceShellBgTool_URLEmptyGatewayHost(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	dir := t.TempDir()
	tool := newTestShellBgTool(t, dir, config.SandboxProfileOff, reg, nil, "" /* empty */)

	ctx := tools.WithAgentID(context.Background(), "nohost-agent")
	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 60",
		"expose_port": float64(18000),
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}

	var parsed shellBgResult
	if err := json.Unmarshal([]byte(result.ForLLM), &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// URL should equal path when gatewayHost is empty.
	if parsed.URL != parsed.Path {
		t.Errorf("url = %q; want path-only %q when gatewayHost is empty", parsed.URL, parsed.Path)
	}

	reg.UnregisterByAgent("nohost-agent")
}

// TestWorkspaceShellBgTool_SubdirCWD verifies that the cwd argument resolves
// to a subdirectory of the workspace, enabling subproject dev servers.
func TestWorkspaceShellBgTool_SubdirCWD(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	dir := t.TempDir()
	subdir := filepath.Join(dir, "hello-world")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := newTestShellBgTool(t, dir, config.SandboxProfileOff, reg, nil, "")
	ctx := tools.WithAgentID(context.Background(), "subdir-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 60",
		"expose_port": float64(18000),
		"cwd":         "hello-world",
	})

	if result.IsError {
		t.Fatalf("expected success with valid cwd, got error: %s", result.ForLLM)
	}

	reg.UnregisterByAgent("subdir-agent")
}

// TestWorkspaceShellBgTool_AuditEmittedOnSuccess verifies that an audit
// entry is written on a successful spawn.
func TestWorkspaceShellBgTool_AuditEmittedOnSuccess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	auditDir := t.TempDir()

	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	defer func() { _ = logger.Close() }()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := newTestShellBgTool(t, t.TempDir(), config.SandboxProfileOff, reg, logger, "")
	ctx := tools.WithAgentID(context.Background(), "audit-success-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 60",
		"expose_port": float64(18000),
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}

	// Flush and read audit dir for any JSONL file.
	if err := logger.Close(); err != nil {
		t.Fatalf("audit logger close: %v", err)
	}

	entries, readErr := os.ReadDir(auditDir)
	if readErr != nil {
		t.Fatalf("read audit dir: %v", readErr)
	}
	var auditContent string
	for _, e := range entries {
		if !e.IsDir() {
			data, _ := os.ReadFile(filepath.Join(auditDir, e.Name()))
			auditContent += string(data)
		}
	}

	if !strings.Contains(auditContent, "exec") {
		t.Errorf("expected 'exec' event in audit log, got:\n%s", auditContent)
	}
	if !strings.Contains(auditContent, "audit-success-agent") {
		t.Errorf("expected agent_id in audit log, got:\n%s", auditContent)
	}

	reg.UnregisterByAgent("audit-success-agent")
}

// TestWorkspaceShellBgTool_AuditEmittedOnDeny verifies that an audit
// entry is written when the command is denied by a deny pattern.
func TestWorkspaceShellBgTool_AuditEmittedOnDeny(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	auditDir := t.TempDir()

	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	defer func() { _ = logger.Close() }()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := tools.NewWorkspaceShellBgTool(tools.WorkspaceShellBgDeps{
		WorkspaceDir:            t.TempDir(),
		Profile:                 config.SandboxProfileOff,
		Registry:                reg,
		MaxConcurrent:           2,
		PortRange:               [2]int32{18000, 18999},
		AuditLogger:             logger,
		AuditFailClosed:         false,
		GlobalShellDenyPatterns: []string{`\bsudo\b`},
		AgentShellPolicy: &config.AgentShellPolicy{
			EnableDenyPatterns: true,
		},
	})
	ctx := tools.WithAgentID(context.Background(), "audit-deny-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sudo sleep 1",
		"expose_port": float64(18000),
	})

	if !result.IsError {
		t.Errorf("expected IsError=true for deny-pattern match")
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("audit logger close: %v", err)
	}

	entries, readErr := os.ReadDir(auditDir)
	if readErr != nil {
		t.Fatalf("read audit dir: %v", readErr)
	}
	var auditContent string
	for _, e := range entries {
		if !e.IsDir() {
			data, _ := os.ReadFile(filepath.Join(auditDir, e.Name()))
			auditContent += string(data)
		}
	}

	if !strings.Contains(auditContent, "deny") {
		t.Errorf("expected 'deny' decision in audit log, got:\n%s", auditContent)
	}
}

// TestWorkspaceShellBgTool_ProfileOffSkipsHardening verifies that profile=off
// (god mode) spawns successfully — SpawnBackgroundChild detects zero Limits
// and skips ApplyChildHardening.
func TestWorkspaceShellBgTool_ProfileOffSkipsHardening(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := newTestShellBgTool(t, t.TempDir(), config.SandboxProfileOff, reg, nil, "")
	ctx := tools.WithAgentID(context.Background(), "godmode-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 60",
		"expose_port": float64(18000),
	})

	if result.IsError {
		t.Fatalf("expected success for profile=off (god mode), got error: %s", result.ForLLM)
	}

	reg.UnregisterByAgent("godmode-agent")
}

// TestWorkspaceShellBgTool_AuditFailClosedReturnsDeny verifies that when
// AuditFailClosed=true and the audit logger returns an IO error, Execute returns
// an ErrorResult and does NOT spawn a process (DevServerRegistry has zero entries).
//
// BDD: Given AuditFailClosed=true and a degraded (closed) audit logger,
//
//	When Execute is called on workspace.shell_bg,
//	Then result.IsError=true and DevServerRegistry has zero entries.
//
// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-2.
func TestWorkspaceShellBgTool_AuditFailClosedReturnsDeny(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	t.Parallel()

	// Create a real audit logger and immediately close it so Log() returns an error.
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	// Close so the logger enters degraded mode; subsequent Log() returns an error.
	if err := logger.Close(); err != nil {
		t.Fatalf("logger.Close: %v", err)
	}

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	tool := tools.NewWorkspaceShellBgTool(tools.WorkspaceShellBgDeps{
		WorkspaceDir:    t.TempDir(),
		Profile:         config.SandboxProfileOff,
		Registry:        reg,
		MaxConcurrent:   2,
		PortRange:       [2]int32{18000, 18999},
		AuditLogger:     logger,
		AuditFailClosed: true, // fail closed — must deny when audit fails
	})

	ctx := tools.WithAgentID(context.Background(), "fail-closed-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 60",
		"expose_port": float64(18000),
	})

	// Must return an error result.
	if !result.IsError {
		t.Errorf(
			"expected IsError=true when audit_fail_closed=true and logger degraded; got false; ForLLM=%q",
			result.ForLLM,
		)
	}

	// DevServerRegistry must have zero entries — no spawn occurred.
	count := reg.Count()
	if count != 0 {
		t.Errorf("expected zero DevServerRegistry entries after fail-closed deny; got %d", count)
	}
}

// TestWorkspaceShellBgTool_ProfileWorkspaceCallsHardening verifies that
// profile=workspace (non-god-mode) succeeds without error when spawning
// a simple process. The hardening is applied via sandbox.Limits; we don't
// introspect SysProcAttr directly, but a successful spawn is evidence the
// hardening path was entered and did not error.
func TestWorkspaceShellBgTool_ProfileWorkspaceCallsHardening(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only — ApplyChildHardening uses Linux-specific syscalls")
	}
	t.Parallel()

	reg := sandbox.NewDevServerRegistry()
	defer reg.Close()

	dir := t.TempDir()
	tool := newTestShellBgTool(t, dir, config.SandboxProfileWorkspace, reg, nil, "")
	ctx := tools.WithAgentID(context.Background(), "ws-profile-agent")

	result := tool.Execute(ctx, map[string]any{
		"command":     "sleep 60",
		"expose_port": float64(18000),
	})

	if result.IsError {
		t.Fatalf("expected success for profile=workspace, got error: %s", result.ForLLM)
	}

	reg.UnregisterByAgent("ws-profile-agent")
}
