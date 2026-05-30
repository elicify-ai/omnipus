// Package tools — workspace.shell_bg background tool (dark-launched).
//
// workspace.shell_bg starts a long-running process (typically a dev server)
// inside the agent's workspace directory, registers it with the gateway's
// DevServerRegistry, and returns a /dev/<agent>/<token>/ URL that the
// existing chat UI renders as a clickable iframe preview. The /dev/ path
// resolves via the back-compat shim on the preview listener; web_serve dev
// mode emits /preview/ URLs instead.
//
// Design differences from web_serve dev mode:
//   - No Tier-3 command prefix-allowlist (commandAllowed). The sandbox
//     profile is the security boundary; the agent invokes any command.
//   - No ensureNodeModules shim. The agent runs `npm install` itself via
//     workspace.shell before calling workspace.shell_bg.
//   - No GHSA channel block. Per-agent ToolPolicyCfg governs access.
//   - cwd arg (relative to workspace) is supported — fixes the subdir bug
//     where `next dev` must run from inside `hello-world/` not the workspace
//     root.
//   - Uses sandbox.LimitsForProfile to derive Limits from the agent's profile.
//   - Returns the same JSON shape as web_serve dev mode so RunInWorkspaceUI.tsx
//     and IframePreview.tsx render the preview without modification.
//
// SECURITY CONTRACT:
//   - The tool is registered only when experimental.workspace_shell_enabled=true.
//   - Per-agent ToolPolicyCfg (allow/ask/deny) governs which agents can call it.
//   - The executing child runs under sandbox.Limits from LimitsForProfile.
//   - SandboxProfile="off" (god mode) skips ApplyChildHardening.
//   - Deny-pattern mechanism (global + per-agent) is wired but inert by default.
//   - Every invocation — allow and deny — is recorded in the audit log.
//   - AuditFailClosed=true (default): if audit write fails, the server is NOT
//     started. workspace.shell_bg is a TRUSTED-PROMPT FEATURE.
//   - cwd escaping the workspace returns an error.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// WorkspaceShellBgTool implements the workspace.shell_bg builtin tool.
// One instance per agent; constructor dependencies are injected.
type WorkspaceShellBgTool struct {
	BaseTool

	// workspaceDir is the agent's resolved workspace root.
	workspaceDir string

	// profile is the agent's SandboxProfile. Drives LimitsForProfile.
	profile config.SandboxProfile

	// proxy is the process-wide EgressProxy. May be nil.
	proxy *sandbox.EgressProxy

	// auditLogger records every invocation.
	auditLogger *audit.Logger

	// auditFailClosed mirrors WebServeDevConfig.AuditFailClosed
	// (pkg/tools/web_serve.go); the on-disk field is
	// sandbox.path_guard_audit_fail_closed. When true (default) the tool
	// refuses to start when audit write fails.
	auditFailClosed bool

	// registry is the shared DevServerRegistry. Must be non-nil in production.
	registry *sandbox.DevServerRegistry

	// maxConcurrent is the per-gateway active-server cap (from config).
	maxConcurrent int32

	// portRange is [min, max] inclusive for expose_port validation.
	portRange [2]int32

	// gatewayHost is the operator-configured PREVIEW listener origin used
	// to construct the absolute URL in the tool result. See web_serve.go
	// for the two-port topology rationale and scheme-coercion rules.
	gatewayHost string

	// Deny-pattern fields (same semantics as WorkspaceShellTool).
	globalDenyPatterns []*regexp.Regexp
	agentDenyPatterns  []*regexp.Regexp
	enableDenyPatterns bool

	// runMu serializes the per-agent cap pre-check + spawn so two concurrent
	// invocations on the same agent cannot both pass before either registers.
	runMu sync.Mutex
}

// WorkspaceShellBgDeps bundles injectable dependencies for NewWorkspaceShellBgTool.
type WorkspaceShellBgDeps struct {
	// WorkspaceDir is the resolved absolute path to the agent's workspace.
	WorkspaceDir string

	// Profile is the agent's SandboxProfile.
	Profile config.SandboxProfile

	// Proxy is the process-wide egress proxy. May be nil.
	Proxy *sandbox.EgressProxy

	// AuditLogger receives audit entries. May be nil in tests.
	AuditLogger *audit.Logger

	// AuditFailClosed — when true (default), refuse to start when audit fails.
	AuditFailClosed bool

	// Registry is the shared DevServerRegistry. Must be non-nil in production.
	Registry *sandbox.DevServerRegistry

	// MaxConcurrent is the per-gateway active dev-server cap.
	MaxConcurrent int32

	// PortRange is [min, max] inclusive for expose_port validation.
	PortRange [2]int32

	// GatewayHost is the preview-listener origin (gateway.preview_origin).
	GatewayHost string

	// GlobalShellDenyPatterns from config.Sandbox.ShellDenyPatterns.
	GlobalShellDenyPatterns []string

	// AgentShellPolicy from AgentConfig.ShellPolicy. May be nil.
	AgentShellPolicy *config.AgentShellPolicy
}

// NewWorkspaceShellBgTool constructs a WorkspaceShellBgTool with the supplied deps.
func NewWorkspaceShellBgTool(deps WorkspaceShellBgDeps) *WorkspaceShellBgTool {
	portRange := deps.PortRange
	if portRange == ([2]int32{}) {
		portRange = [2]int32{18000, 18999}
	}
	maxConcurrent := deps.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}

	t := &WorkspaceShellBgTool{
		workspaceDir:       deps.WorkspaceDir,
		profile:            deps.Profile,
		proxy:              deps.Proxy,
		auditLogger:        deps.AuditLogger,
		auditFailClosed:    deps.AuditFailClosed,
		registry:           deps.Registry,
		maxConcurrent:      maxConcurrent,
		portRange:          portRange,
		gatewayHost:        deps.GatewayHost,
		globalDenyPatterns: compileDenyPatterns(deps.GlobalShellDenyPatterns, "global"),
	}

	if deps.AgentShellPolicy != nil {
		t.enableDenyPatterns = deps.AgentShellPolicy.EnableDenyPatterns
		t.agentDenyPatterns = compileDenyPatterns(deps.AgentShellPolicy.CustomDenyPatterns, "agent")
	}

	return t
}

// ProfileForTest exposes the resolved sandbox profile for white-box testing.
// Kept as a production method because pkg/agent tests require cross-package access.
func (t *WorkspaceShellBgTool) ProfileForTest() config.SandboxProfile { return t.profile }

// SetAuditLogger satisfies the auditLoggerAware contract used by the ToolRegistry.
func (t *WorkspaceShellBgTool) SetAuditLogger(l *audit.Logger) {
	if t == nil {
		return
	}
	t.auditLogger = l
}

func (t *WorkspaceShellBgTool) Name() string { return "workspace.shell_bg" }

func (t *WorkspaceShellBgTool) Scope() ToolScope { return ScopeCore }

func (t *WorkspaceShellBgTool) Category() ToolCategory { return CategoryWorkspace }

func (t *WorkspaceShellBgTool) Description() string {
	return `Start a long-running background process (dev server, proxy, etc.) in the agent's workspace directory under the configured sandbox profile. Unlike 'web_serve' dev mode, there is no command allow-list — any command may be run. The agent is responsible for running 'npm install' or equivalent setup before calling this tool. Returns a dev_url JSON payload with a clickable iframe preview URL.`
}

func (t *WorkspaceShellBgTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Command to run in the background (e.g. 'npm run dev', 'next dev', 'python -m http.server').",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory relative to the agent workspace (e.g. 'hello-world'). Absolute paths and '..' escapes are rejected. Defaults to the workspace root.",
			},
			"expose_port": map[string]any{
				"type":        "integer",
				"description": "TCP port the process will bind to. Must be within the operator-configured port range (default [18000, 18999]).",
			},
			"env": map[string]any{
				"type":                 "object",
				"description":          "Extra environment variables for the child process.",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required": []string{"command", "expose_port"},
	}
}

// devServerStartupGraceShellBg is how long we wait between spawning the child
// and returning the URL. Mirrors web_serve dev mode's devServerStartupGrace.
const devServerStartupGraceShellBg = 3 * time.Second

// BackgroundShellLinuxOnlyMessage is the error returned when workspace.shell_bg
// is called on a non-Linux platform. workspace.shell_bg requires Linux for
// sandbox.ApplyChildHardening (Setpgid, Pdeathsig) and the /dev/ reverse-proxy.
const BackgroundShellLinuxOnlyMessage = "background shell tools require Linux for sandboxing; not supported on this platform"

// shellBgResult is the JSON shape returned to the agent. This shape MUST
// match RunInWorkspaceResult in src/lib/api.ts so RunInWorkspaceUI.tsx and
// IframePreview.tsx render the preview without modification.
//
// Fields: path, url, expires_at, command, port, _summary.
// The frontend type guard (isRunInWorkspaceResult) checks: hasPreviewShape
// (path+url both string) AND command is string AND port is number.
type shellBgResult struct {
	Path      string `json:"path"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	Command   string `json:"command"`
	Port      int32  `json:"port"`
	Summary   string `json:"_summary"`
}

// Execute validates args, spawns the background process, registers it, and
// returns the dev_url JSON.
func (t *WorkspaceShellBgTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// Linux gate: sandbox.ApplyChildHardening uses Linux-specific syscalls
	// for process group management; dev server proxying via the /dev/ route
	// is also Linux-only in the current gateway.
	if runtime.GOOS != "linux" {
		return ErrorResult(BackgroundShellLinuxOnlyMessage)
	}

	if t.registry == nil {
		return ErrorResult("workspace.shell_bg: dev-server registry not configured")
	}

	// Parse required args.
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return ErrorResult("command is required and must be a non-empty string")
	}
	command = strings.TrimSpace(command)

	exposePortRaw, ok := args["expose_port"]
	if !ok {
		return ErrorResult("expose_port is required")
	}
	exposePort, err := normalisePort(exposePortRaw)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid expose_port: %v", err))
	}

	// Port range check.
	if exposePort < t.portRange[0] || exposePort > t.portRange[1] {
		return ErrorResult(fmt.Sprintf(
			"expose_port %d is outside the allowed range [%d, %d]",
			exposePort, t.portRange[0], t.portRange[1],
		))
	}

	// Resolve cwd. Default = workspace root.
	cwd, cwdErr := t.resolveCWD(args)
	if cwdErr != nil {
		return t.denyResult(ctx, command, cwdErr.Error())
	}

	// Parse extra env.
	var envSlice []string
	if rawEnv, ok := args["env"].(map[string]any); ok {
		for k, v := range rawEnv {
			if vs, ok := v.(string); ok {
				envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, vs))
			}
		}
	}

	// Deny pattern check (inert by default unless EnableDenyPatterns=true).
	if t.enableDenyPatterns {
		merged := make([]*regexp.Regexp, 0, len(t.globalDenyPatterns)+len(t.agentDenyPatterns))
		merged = append(merged, t.globalDenyPatterns...)
		merged = append(merged, t.agentDenyPatterns...)
		if msg := applyDenyPatterns(command, merged, nil); msg != "" {
			return t.denyResult(ctx, command, msg)
		}
	}

	agentID := ToolAgentID(ctx)
	if agentID == "" {
		return ErrorResult("workspace.shell_bg: missing agent id in context")
	}

	// Per-agent cap pre-check (same as web_serve dev mode).
	t.runMu.Lock()
	existing := t.registry.LookupByAgent(agentID)
	t.runMu.Unlock()
	if existing != nil {
		return ErrorResult(fmt.Sprintf(
			"server already running on this agent; previous registration expires at %s",
			existing.CreatedAt.Add(sandbox.HardTimeout).UTC().Format(time.RFC3339),
		))
	}

	// Audit BEFORE spawn — fail closed when auditFailClosed=true.
	if auditErr := t.auditStart(ctx, agentID, command, exposePort); auditErr != nil {
		return auditErr
	}

	// Derive sandbox Limits from the agent's profile.
	// For god mode (profile=off) LimitsForProfile returns zero Limits and
	// SpawnBackgroundChild detects zero-value Limits and skips hardening.
	lim, limErr := sandbox.LimitsForProfile(t.profile, t.workspaceDir, t.proxy, 0)
	if limErr != nil {
		return ErrorResult(fmt.Sprintf("workspace.shell_bg: sandbox profile error: %v", limErr))
	}
	// Override WorkspaceDir in the Limits to the resolved cwd so the child
	// starts in the right subdirectory and npm_config_cache is rooted there.
	// Note: we still pass workspaceDir to LimitsForProfile (to ensure it
	// exists) but override it before spawn.
	lim.WorkspaceDir = cwd

	// Spawn the background child.
	parts := strings.Fields(command)
	cmd, spawnErr := sandbox.SpawnBackgroundChild(parts, cwd, envSlice, exposePort, lim)
	if spawnErr != nil {
		return ErrorResult(fmt.Sprintf("workspace.shell_bg: failed to start process: %v", spawnErr))
	}

	// Register with the DevServerRegistry.
	reg, regErr := t.registry.Register(agentID, exposePort, cmd.Process.Pid, command, int(t.maxConcurrent))
	if regErr != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		// Log the orphaned child outcome so operators can diagnose
		// registration failures correlated with child behavior.
		orphanPid := cmd.Process.Pid
		go func() {
			waitErr := cmd.Wait()
			exitCode := -1
			if waitErr == nil {
				exitCode = 0
			} else if ee, ok := waitErr.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			}
			slog.Info("workspace.shell_bg: orphaned child exited (registration failed)",
				"agent_id", agentID, "pid", orphanPid, "exit_code", exitCode, "error", waitErr)
		}()
		var capErr sandbox.GatewayCapError
		if errors.As(regErr, &capErr) {
			return ErrorResult(fmt.Sprintf(
				"too many concurrent dev servers (%d/%d); previous registration expires at %s",
				capErr.Current, capErr.Max, capErr.EarliestExpiry.UTC().Format(time.RFC3339),
			))
		}
		if errors.Is(regErr, sandbox.ErrPerAgentCap) {
			return ErrorResult("server already running on this agent")
		}
		return ErrorResult(fmt.Sprintf("workspace.shell_bg: registration failed: %v", regErr))
	}

	// Reap the child in a background goroutine to avoid zombies. We do NOT
	// Unregister the token here: many real dev launchers (npm, yarn, pnpm)
	// exec into the actual server (next, vite) and then exit cleanly with
	// status 0 once the server takes over. cmd.Wait() returns at that point
	// even though the listener is still alive on the port. The
	// DevServerRegistry's idle-timeout / hard-cap expiry is the authoritative
	// lifecycle signal — let it own teardown.
	bgPid := cmd.Process.Pid
	go func() {
		waitErr := cmd.Wait()
		exitCode := -1
		if waitErr == nil {
			exitCode = 0
		} else if ee, ok := waitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		slog.Info("workspace.shell_bg: launcher process exited (registration retained)",
			"agent_id", agentID, "pid", bgPid, "token", reg.Token,
			"exit_code", exitCode, "error", waitErr)
	}()

	// Brief grace period so the server can bind before the agent hits the URL.
	select {
	case <-ctx.Done():
	case <-time.After(devServerStartupGraceShellBg):
	}

	// Build the response. Shape MUST match RunInWorkspaceResult in api.ts.
	path := fmt.Sprintf("/dev/%s/%s/", agentID, reg.Token)
	url := sandbox.BuildDevURL(agentID, reg.Token, t.gatewayHost)
	deadline := reg.CreatedAt.Add(sandbox.HardTimeout).UTC().Format(time.RFC3339)
	summary := fmt.Sprintf(
		"workspace.shell_bg: background process started. URL: %s. Command: %s. Port: %d. Token expires after 30 min idle / 4 h hard cap.",
		url,
		command,
		exposePort,
	)

	res := shellBgResult{
		Path:      path,
		URL:       url,
		ExpiresAt: deadline,
		Command:   command,
		Port:      exposePort,
		Summary:   summary,
	}
	data, marshalErr := json.Marshal(res)
	if marshalErr != nil {
		slog.Warn("workspace.shell_bg: failed to marshal result", "error", marshalErr)
		return ErrorResult(fmt.Sprintf("failed to serialize result: %v", marshalErr))
	}

	return NewToolResult(string(data))
}

// resolveCWD resolves the optional cwd argument to an absolute path under
// workspaceDir. Returns workspaceDir when cwd is absent or empty.
func (t *WorkspaceShellBgTool) resolveCWD(args map[string]any) (string, error) {
	rawCWD, _ := args["cwd"].(string)
	rawCWD = strings.TrimSpace(rawCWD)

	if rawCWD == "" {
		abs, err := filepath.Abs(t.workspaceDir)
		if err != nil {
			return "", fmt.Errorf("workspace dir not resolvable: %w", err)
		}
		return abs, nil
	}

	// Reject absolute paths — callers must use relative paths under workspace.
	if filepath.IsAbs(rawCWD) {
		return "", fmt.Errorf("path escapes workspace: absolute path not allowed (use a relative path)")
	}

	// Validate via shared helper (catches symlink escapes and cross-agent access).
	resolved, err := validatePathWithAllowPaths(rawCWD, t.workspaceDir, true, nil)
	if err != nil {
		return "", fmt.Errorf("path escapes workspace: %w", err)
	}
	return resolved, nil
}

// denyResult emits an audit deny entry and returns an error ToolResult.
func (t *WorkspaceShellBgTool) denyResult(ctx context.Context, command, reason string) *ToolResult {
	agentID := ToolAgentID(ctx)
	t.emitAudit(agentID, command, "", audit.DecisionDeny)
	return ErrorResult(reason)
}

// auditStart emits an audit.EventExec entry before spawning the child. When
// auditFailClosed=true and the write fails, returns a ToolResult that aborts
// the spawn. Returns nil to mean "continue".
func (t *WorkspaceShellBgTool) auditStart(ctx context.Context, agentID, command string, port int32) *ToolResult {
	if t.auditLogger == nil {
		// No logger wired (test path). Continue without auditing.
		return nil
	}
	logErr := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: audit.DecisionAllow,
		AgentID:  agentID,
		Tool:     t.Name(),
		Command:  command,
		Details: map[string]any{
			"expose_port": port,
			"workspace":   t.workspaceDir,
			"profile":     string(t.profile),
		},
	})
	if logErr == nil {
		return nil
	}
	if t.auditFailClosed {
		slog.Error("workspace.shell_bg: audit logger degraded; refusing to run trusted-prompt feature",
			"agent_id", agentID, "command", command, "error", logErr, "audit_fail_closed", true)
		return &ToolResult{
			IsError: true,
			ForLLM:  "audit logger degraded; refusing to run trusted-prompt feature without compliance trail",
			ForUser: "workspace.shell_bg requires audit logging; aborting",
		}
	}
	slog.Error("workspace.shell_bg: audit write failed (continuing — audit_fail_closed=false)",
		"agent_id", agentID, "command", command, "error", logErr)
	return nil
}

// emitAudit writes an audit.Entry. Nil logger is a no-op.
func (t *WorkspaceShellBgTool) emitAudit(agentID, command, cwd, decision string) {
	if t.auditLogger == nil {
		return
	}
	details := map[string]any{
		"workspace": t.workspaceDir,
		"profile":   string(t.profile),
	}
	if cwd != "" {
		details["cwd"] = cwd
	}
	if err := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: decision,
		AgentID:  agentID,
		Tool:     t.Name(),
		Command:  command,
		Details:  details,
	}); err != nil {
		slog.Warn("workspace.shell_bg: audit write failed", "agent_id", agentID, "error", err)
	}
}
