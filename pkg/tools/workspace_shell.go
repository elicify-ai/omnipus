// Package tools — workspace.shell foreground tool (dark-launched).
//
// workspace.shell is a free-form foreground shell that runs inside the agent's
// workspace directory under the kernel sandbox profile configured for the agent.
// It replaces the brittle Tier-3 command allowlist pattern with a principled
// sandbox-at-the-kernel-layer approach.
//
// SECURITY CONTRACT:
//   - The tool is registered only when experimental.workspace_shell_enabled=true.
//   - Per-agent ToolPolicyCfg (allow/ask/deny) governs which agents can call it.
//   - The executing child process runs under sandbox.Limits derived from the
//     agent's SandboxProfile via LimitsForProfile.
//   - SandboxProfile="off" (god mode) skips ApplyChildHardening; all other
//     profiles apply hardening. God-mode gating is fully enforced in PR 4.
//   - The deny-pattern mechanism (global + per-agent) is wired but inert by
//     default (EnableDenyPatterns defaults to false).
//   - Every invocation — allow and deny — is recorded in the audit log.
//   - The cwd argument is resolved under the agent workspace; any value
//     that escapes the workspace returns "path escapes workspace".
//
// THREAT ACKNOWLEDGEMENT: even with SandboxProfile="workspace", a process can
// read anything inside the workspace including credentials symlinked there.
// Operators must not store sensitive files in agent workspaces.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// WorkspaceShellTool implements the workspace.shell builtin tool.
// One instance per agent; constructor dependencies are injected.
type WorkspaceShellTool struct {
	BaseTool

	// workspaceDir is the agent's resolved workspace root. All relative cwd
	// arguments are resolved against this path.
	workspaceDir string

	// profile is the agent's SandboxProfile. Drives LimitsForProfile.
	profile config.SandboxProfile

	// proxy is the process-wide EgressProxy. May be nil (no egress filtering).
	proxy *sandbox.EgressProxy

	// auditLogger records every invocation (allow and deny).
	// When nil, audit logging is skipped (test path or not configured).
	auditLogger *audit.Logger

	// globalDenyPatterns is the operator-level compiled deny list
	// (config.Sandbox.ShellDenyPatterns). These are always applied when
	// EnableDenyPatterns is true, regardless of per-agent overrides.
	globalDenyPatterns []*regexp.Regexp

	// agentDenyPatterns is the per-agent compiled deny list
	// (AgentShellPolicy.CustomDenyPatterns). Applied on top of globalDenyPatterns.
	agentDenyPatterns []*regexp.Regexp

	// auditFailClosed mirrors WorkspaceShellDeps.AuditFailClosed. When true
	// (default), the tool refuses to execute when the audit write fails.
	auditFailClosed bool

	// enableDenyPatterns mirrors AgentShellPolicy.EnableDenyPatterns. When
	// false no deny patterns are checked — same semantics as shell.go today.
	enableDenyPatterns bool
}

// WorkspaceShellDeps bundles the injectable dependencies for NewWorkspaceShellTool.
type WorkspaceShellDeps struct {
	// WorkspaceDir is the resolved absolute path to the agent's workspace.
	WorkspaceDir string

	// Profile is the agent's SandboxProfile.
	Profile config.SandboxProfile

	// Proxy is the process-wide egress proxy. May be nil.
	Proxy *sandbox.EgressProxy

	// AuditLogger receives audit entries. May be nil in tests.
	AuditLogger *audit.Logger

	// AuditFailClosed mirrors PathGuardAuditFailClosed. When true (default),
	// the tool refuses to execute when the audit write fails — same semantics
	// as WorkspaceShellBgDeps.AuditFailClosed.
	AuditFailClosed bool

	// GlobalShellDenyPatterns is from config.Sandbox.ShellDenyPatterns.
	GlobalShellDenyPatterns []string

	// AgentShellPolicy is from AgentConfig.ShellPolicy. May be nil.
	AgentShellPolicy *config.AgentShellPolicy
}

// NewWorkspaceShellTool constructs a WorkspaceShellTool with the supplied deps.
func NewWorkspaceShellTool(deps WorkspaceShellDeps) *WorkspaceShellTool {
	t := &WorkspaceShellTool{
		workspaceDir:       deps.WorkspaceDir,
		profile:            deps.Profile,
		proxy:              deps.Proxy,
		auditLogger:        deps.AuditLogger,
		auditFailClosed:    deps.AuditFailClosed,
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
func (t *WorkspaceShellTool) ProfileForTest() config.SandboxProfile { return t.profile }

// SetAuditLogger satisfies the auditLoggerAware contract used by the ToolRegistry.
func (t *WorkspaceShellTool) SetAuditLogger(l *audit.Logger) {
	if t == nil {
		return
	}
	t.auditLogger = l
}

func (t *WorkspaceShellTool) Name() string { return "workspace.shell" }

func (t *WorkspaceShellTool) Scope() ToolScope { return ScopeCore }

func (t *WorkspaceShellTool) Category() ToolCategory { return CategoryWorkspace }

func (t *WorkspaceShellTool) Description() string {
	return `Run a shell command inside the agent's workspace directory under the configured sandbox profile. Unlike 'exec', this tool is not restricted to internal channels — the sandbox profile enforces the security boundary at the kernel level. Returns exit_code, stdout, stderr, and duration_ms.`
}

func (t *WorkspaceShellTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute. Run under sh -c on Linux/macOS, powershell -Command on Windows.",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory relative to the agent workspace (e.g. 'my-project'). Absolute paths and '..' escapes are rejected. Defaults to the workspace root.",
			},
			"env": map[string]any{
				"type":                 "object",
				"description":          "Extra environment variables for the child process. Keys and values must be strings.",
				"additionalProperties": map[string]any{"type": "string"},
			},
			"timeout_sec": map[string]any{
				"type":        "integer",
				"description": "Wall-clock timeout in seconds. 0 or omitted = use the sandbox profile default (300 s). Maximum 3600.",
			},
		},
		"required": []string{"command"},
	}
}

// workspaceShellResult is the JSON shape returned to the agent.
type workspaceShellResult struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMS int64  `json:"duration_ms"`
	Summary    string `json:"_summary"`
}

// Execute runs the shell command and returns structured JSON.
func (t *WorkspaceShellTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return ErrorResult("command is required and must be a non-empty string")
	}
	command = strings.TrimSpace(command)

	// Resolve cwd. Default = workspace root.
	cwd, cwdErr := t.resolveCWD(args)
	if cwdErr != nil {
		return t.denyResult(ctx, command, cwdErr.Error())
	}

	// Resolve timeout.
	timeoutSec := int32(300)
	if rawTimeout, ok := args["timeout_sec"]; ok {
		switch v := rawTimeout.(type) {
		case float64:
			timeoutSec = int32(v)
		case int:
			timeoutSec = int32(v)
		case int32:
			timeoutSec = v
		case int64:
			timeoutSec = int32(v)
		}
	}
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	if timeoutSec > 3600 {
		timeoutSec = 3600
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

	// Emit allow audit entry before spawning. Fail closed when configured.
	agentID := ToolAgentID(ctx)
	if auditResult := t.emitAuditOrDeny(agentID, command, cwd, audit.DecisionAllow); auditResult != nil {
		return auditResult
	}

	// Build sandbox limits.
	lim, limErr := sandbox.LimitsForProfile(t.profile, t.workspaceDir, t.proxy, timeoutSec)
	if limErr != nil {
		return ErrorResult(fmt.Sprintf("sandbox profile error: %v", limErr))
	}
	// Override WorkspaceDir with the resolved cwd for the child process.
	lim.WorkspaceDir = cwd

	// Spawn.
	result, runErr := t.run(ctx, command, envSlice, lim, timeoutSec)
	if runErr != nil {
		return ErrorResult(fmt.Sprintf("failed to run command: %v", runErr))
	}

	data, err := json.Marshal(result)
	if err != nil {
		slog.Warn("workspace.shell: failed to marshal result", "error", err)
		return ErrorResult(fmt.Sprintf("failed to serialize result: %v", err))
	}

	isError := result.ExitCode != 0
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: result.Summary,
		IsError: isError,
	}
}

// resolveCWD resolves the cwd argument to an absolute path under workspaceDir.
// Returns workspaceDir when cwd is absent or empty.
func (t *WorkspaceShellTool) resolveCWD(args map[string]any) (string, error) {
	rawCWD, _ := args["cwd"].(string)
	rawCWD = strings.TrimSpace(rawCWD)

	if rawCWD == "" {
		// Default: workspace root.
		abs, err := filepath.Abs(t.workspaceDir)
		if err != nil {
			return "", fmt.Errorf("workspace dir not resolvable: %w", err)
		}
		return abs, nil
	}

	// Reject absolute paths outright — callers must use relative paths under
	// the workspace.
	if filepath.IsAbs(rawCWD) {
		return "", fmt.Errorf("path escapes workspace: absolute path not allowed (use a relative path)")
	}

	// Use the shared validatePathWithAllowPaths helper which also catches
	// symlink escapes and cross-agent workspace access.
	resolved, err := validatePathWithAllowPaths(rawCWD, t.workspaceDir, true, nil)
	if err != nil {
		return "", fmt.Errorf("path escapes workspace: %w", err)
	}
	return resolved, nil
}

// run spawns the shell command and returns a workspaceShellResult.
func (t *WorkspaceShellTool) run(
	ctx context.Context,
	command string,
	extraEnv []string,
	lim sandbox.Limits,
	timeoutSec int32,
) (workspaceShellResult, error) {
	// Choose the shell invocation. On Linux/macOS: sh -c. On Windows: powershell.
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", command}
	} else {
		argv = []string{"sh", "-c", command}
	}

	// Build env: start from extra env, then let sandbox.Run's mergeEnv layer
	// add proxy + npm-cache vars. Passing nil would inherit the parent env,
	// which is correct for workspace.shell (agent inherits PATH, HOME, etc.).
	// We pass extraEnv so operator env additions are included; nil means
	// "inherit + inject", which is what we want here.
	env := extraEnv // nil or populated

	// God mode: skip ApplyChildHardening entirely.
	// Pass the original timeoutSec rather than lim.TimeoutSeconds because
	// LimitsForProfile returns zero-value Limits for profile=off, so
	// lim.TimeoutSeconds is always 0 on the god-mode path.
	if sandbox.IsGodMode(t.profile) {
		return t.runUnconstrained(ctx, argv, env, lim.WorkspaceDir, timeoutSec)
	}

	// Normal path: sandbox.Run applies hardening.
	sandboxResult, err := sandbox.Run(ctx, argv, env, lim)
	if err != nil {
		return workspaceShellResult{}, fmt.Errorf("sandbox.Run: %w", err)
	}

	summary := buildSummary(command, sandboxResult.ExitCode, sandboxResult.TimedOut, sandboxResult.Duration)
	return workspaceShellResult{
		ExitCode:   sandboxResult.ExitCode,
		Stdout:     string(sandboxResult.Stdout),
		Stderr:     string(sandboxResult.Stderr),
		DurationMS: sandboxResult.Duration.Milliseconds(),
		Summary:    summary,
	}, nil
}

// scrubbedEnv returns a copy of base with sensitive Omnipus env vars removed.
// Strips OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, and OMNIPUS_BEARER_TOKEN so
// god-mode child processes cannot read the gateway's master key or auth token.
func scrubbedEnv(base []string) []string {
	blocked := map[string]bool{
		"OMNIPUS_MASTER_KEY":   true,
		"OMNIPUS_KEY_FILE":     true,
		"OMNIPUS_BEARER_TOKEN": true,
	}
	out := make([]string, 0, len(base))
	for _, kv := range base {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if !blocked[key] {
			out = append(out, kv)
		}
	}
	return out
}

// runUnconstrained runs the command without ApplyChildHardening. Used only
// when profile=off (god mode). The command still runs in the resolved cwd
// and with the user-supplied timeout. The parent environment is inherited with
// sensitive Omnipus credentials stripped; extraEnv entries are appended last
// and override any earlier entries with the same key.
func (t *WorkspaceShellTool) runUnconstrained(
	ctx context.Context,
	argv []string,
	extraEnv []string,
	cwdPath string,
	timeoutSec int32,
) (workspaceShellResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	if cwdPath != "" {
		cmd.Dir = cwdPath
	}
	// Inherit the parent environment but strip sensitive Omnipus vars, then
	// append caller-supplied extras so they can override (e.g. PATH overrides).
	cmd.Env = append(scrubbedEnv(os.Environ()), extraEnv...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)

	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			return workspaceShellResult{}, fmt.Errorf("command failed: %w", runErr)
		}
	}

	summary := buildSummary(strings.Join(argv, " "), exitCode, false, dur)

	return workspaceShellResult{
		ExitCode:   exitCode,
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		DurationMS: dur.Milliseconds(),
		Summary:    summary,
	}, nil
}

// denyResult emits an audit deny entry and returns a ToolResult with IsError=true.
func (t *WorkspaceShellTool) denyResult(ctx context.Context, command, reason string) *ToolResult {
	agentID := ToolAgentID(ctx)
	t.emitAudit(agentID, command, "", audit.DecisionDeny)
	return ErrorResult(reason)
}

// emitAuditOrDeny writes an audit.Entry for this invocation on the allow path.
// When the write fails and auditFailClosed is true it returns a ToolResult that
// aborts the execution — mirroring WorkspaceShellBgTool.auditStart. Returns nil
// to mean "continue".
func (t *WorkspaceShellTool) emitAuditOrDeny(agentID, command, cwd, decision string) *ToolResult {
	if t.auditLogger == nil {
		return nil
	}
	details := map[string]any{
		"workspace": t.workspaceDir,
		"profile":   string(t.profile),
	}
	if cwd != "" {
		details["cwd"] = cwd
	}
	logErr := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: decision,
		AgentID:  agentID,
		Tool:     t.Name(),
		Command:  command,
		Details:  details,
	})
	if logErr == nil {
		return nil
	}
	if t.auditFailClosed {
		slog.Error("workspace.shell: audit logger degraded; refusing to execute (audit_fail_closed=true)",
			"agent_id", agentID, "command", command, "error", logErr)
		return &ToolResult{
			IsError: true,
			ForLLM:  "audit log write failed; refusing to execute (audit_fail_closed=true)",
			ForUser: "workspace.shell requires audit logging; aborting",
		}
	}
	slog.Warn("workspace.shell: audit write failed", "agent_id", agentID, "error", logErr)
	return nil
}

// emitAudit writes an audit.Entry for this invocation. Used on deny paths
// where fail-closed semantics do not apply (the command is already blocked).
// Nil logger is a no-op.
func (t *WorkspaceShellTool) emitAudit(agentID, command, cwd, decision string) {
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
		slog.Warn("workspace.shell: audit write failed", "agent_id", agentID, "error", err)
	}
}

// buildSummary constructs the human-readable _summary field for the tool result.
func buildSummary(command string, exitCode int, timedOut bool, dur time.Duration) string {
	// Truncate command to 80 chars for the summary line.
	display := command
	if len(display) > 80 {
		display = display[:77] + "..."
	}

	if timedOut {
		return fmt.Sprintf("workspace.shell: command timed out after %s: %s", dur.Round(time.Millisecond), display)
	}
	if exitCode == 0 {
		return fmt.Sprintf("workspace.shell: exited 0 in %s: %s", dur.Round(time.Millisecond), display)
	}
	return fmt.Sprintf("workspace.shell: exited %d in %s: %s", exitCode, dur.Round(time.Millisecond), display)
}
