package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/policy"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// ExecPolicyAuditor evaluates an exec command against the policy engine and
// audit-logs the decision. Implemented by *policy.PolicyAuditor. Defined as an
// interface so tests can supply lightweight mocks and so this package does not
// need to directly import the audit package through this dependency edge.
//
// Contract: implementations MUST audit-log every decision (allow AND deny) as
// a side effect of EvaluateExec. Returning a decision without logging violates
// the SEC-15/ADR-002 §W-3 contract. This is not expressible in the signature
// but is part of the type's invariant — test doubles must honor it.
type ExecPolicyAuditor interface {
	EvaluateExec(agentID, command string) policy.Decision
}

// ExecSandboxBackend applies kernel or application-level sandbox restrictions
// to a child process before it starts. Implemented by sandbox.SandboxBackend.
// Defined as a narrow interface so the tool only depends on the single method
// it actually calls.
type ExecSandboxBackend interface {
	ApplyToCmd(cmd *exec.Cmd, policy sandbox.SandboxPolicy) error
}

// ExecChildProxy optionally routes a child process's HTTP(S) traffic through a
// loopback SSRF proxy (SEC-28). Implemented by security.ExecProxy. The
// interface is intentionally narrow so shell.go depends only on the two
// methods it actually calls.
//
// PrepareCmd must be called before cmd.Start() and must be paired with exactly
// one CmdDone() call once the process has exited so the proxy's active-cmd
// counter stays balanced and its idle-shutdown logic can fire.
type ExecChildProxy interface {
	PrepareCmd(cmd *exec.Cmd)
	CmdDone()
}

// ExecToolDeps bundles optional Wave 2 security dependencies for ExecTool.
// All fields are optional — a nil PolicyAuditor disables binary allowlist
// enforcement (useful when the policy layer is not configured), and a nil
// SandboxBackend disables per-process sandbox application (useful in headless
// tests and on unsupported platforms).
//
// Note: the interactive approval layer (SEC-08, "ask" prompts) is handled at
// the agent loop level by HookManager.ApproveTool and routed to the WebSocket
// approval hook — do NOT also prompt here from shell.go, which would block on
// stdin in headless deployments. The two layers run in this runtime order:
//   - Pre-dispatch: interactive approval (SEC-08) via HookManager.ApproveTool
//     → gateway/ws_approval.go, before the tool ever sees the command
//   - In-tool: automated binary allowlist (SEC-05) via this file, inside
//     executeRun after guardCommand() but before the command runs
//
// Both layers must allow for the command to run; either can deny.
type ExecToolDeps struct {
	PolicyAuditor  ExecPolicyAuditor
	SandboxBackend ExecSandboxBackend
	SandboxPolicy  sandbox.SandboxPolicy
	// ExecProxy optionally routes child-process HTTP(S) traffic through a
	// loopback SSRF proxy (SEC-28). Nil disables proxy injection — the child
	// receives no HTTP_PROXY env vars and traffic flows directly to the network.
	ExecProxy ExecChildProxy

	// SandboxMode is the applied sandbox mode from al.appliedSandboxMode
	// (set via AgentLoop.SetAppliedSandboxMode after boot). The value flows
	// from SandboxApplyResult.Mode — the post-Apply truth — rather than from
	// cfg.Sandbox.ResolvedMode(), which only reports what the config file says
	// and would disagree when the CLI --sandbox flag overrides config.
	//
	// "enforce" and "permissive" route the child process through sandbox.Run
	// (foreground) or ApplyChildHardening (background) so it inherits the
	// gateway's Landlock + seccomp + egress-proxy policy.
	// "off" and the empty string both map to sandbox disabled: a plain
	// `sh -c <cmd>` with no proxy and full user latitude. See sandboxOn().
	SandboxMode string

	// EgressProxy is the kernel-sandbox HTTP/HTTPS egress proxy. When non-nil
	// AND SandboxMode != "off", the child's HTTP_PROXY/HTTPS_PROXY env vars
	// are pointed at it via sandbox.Limits.EgressProxyAddr. Nil disables the
	// proxy injection on the sandboxed path; the SEC-28 ExecProxy above is a
	// separate, complementary mechanism kept for the sandbox-off / fallback
	// platform paths.
	EgressProxy *sandbox.EgressProxy

	// ExecTimeoutSeconds is the wall-clock timeout passed to sandbox.Run when
	// the sandbox-on path is taken. 0 disables the per-call timeout (the
	// caller's context governs cancellation). Sourced from
	// cfg.Tools.Exec.TimeoutSeconds.
	ExecTimeoutSeconds int32
}

var (
	globalSessionManager = NewSessionManager()
	sessionManagerMu     sync.RWMutex
)

func getSessionManager() *SessionManager {
	sessionManagerMu.RLock()
	defer sessionManagerMu.RUnlock()
	return globalSessionManager
}

type ExecTool struct {
	BaseTool
	workingDir           string
	timeout              time.Duration
	maxBackgroundSeconds int // hard-kill timeout for background sessions; 0 = disabled
	denyPatterns         []*regexp.Regexp
	allowPatterns        []*regexp.Regexp
	customAllowPatterns  []*regexp.Regexp
	allowedPathPatterns  []*regexp.Regexp
	restrictToWorkspace  bool
	sessionManager       *SessionManager

	// Wave 2: Security wiring (SEC-01/02/03/05).
	// policyAuditor enforces the binary allowlist (SEC-05) and audit-logs the
	// decision — the allowlist itself lives on the PolicyAuditor's Evaluator,
	// not on this struct, so it stays in sync with the live policy config.
	// sandboxBackend applies kernel (Landlock/seccomp) or application-level
	// restrictions to every child process. Both are optional: nil means the
	// corresponding feature is disabled.
	policyAuditor  ExecPolicyAuditor
	sandboxBackend ExecSandboxBackend
	sandboxPolicy  sandbox.SandboxPolicy
	// execProxy is the SEC-28 SSRF proxy for child processes. Nil = disabled.
	// When non-nil, PrepareCmd injects HTTP_PROXY/HTTPS_PROXY env vars before
	// Start() and CmdDone() is called exactly once after cmd.Wait() returns.
	execProxy ExecChildProxy
	// auditLogger receives path.access_denied events for workspace-guard
	// rejections. Nil means audit logging is disabled (best-effort).
	auditLogger *audit.Logger

	// sandboxMode is the resolved sandbox mode; "off" → legacy `sh -c` path;
	// any other value (including "" which defaults to enforce in the policy
	// engine) → hardened-exec path via sandbox.Run / ApplyChildHardening.
	sandboxMode string
	// sandboxEgressProxy is the *sandbox.EgressProxy plumbed into the
	// hardened-exec Limits. Nil disables proxy injection on the sandbox-on
	// path (children make HTTP requests directly).
	sandboxEgressProxy *sandbox.EgressProxy
	// execTimeoutSeconds is the timeout passed to sandbox.Run (sandbox-on
	// foreground path). 0 = no timeout.
	execTimeoutSeconds int32

	// killAuditFn is a pre-built audit callback for process-kill failures.
	// H5-BK: constructed once at SetAuditLogger time so all kill sites
	// (runSync's terminateProcessTree, runSync's fallback kill, background
	// session hard-kill, and kill_session tool path) share one closure rather
	// than each rebuilding it inline. Nil when auditLogger is nil.
	killAuditFn func(pid int, killErr error, caller string)
}

// SetAuditLogger injects an audit.Logger into the ExecTool so that
// path.access_denied events are emitted on workspace-guard rejections.
// Satisfies the auditLoggerAware contract used by the ToolRegistry.
// Calling this on a nil ExecTool is a no-op.
//
// H5-BK: also pre-builds killAuditFn once so all kill sites (runSync's
// terminateProcessTree, runSync's fallback kill, background session hard-kill,
// and kill_session tool path) share one closure rather than each rebuilding it.
func (t *ExecTool) SetAuditLogger(l *audit.Logger) {
	if t == nil {
		return
	}
	t.auditLogger = l
	if l != nil {
		al := l
		t.killAuditFn = func(pid int, killErr error, caller string) {
			// CRIT-6 + typed-Event migration: route through audit.EmitEntry so
			// Log failure bumps the audit-skipped counter; use the typed
			// EventProcessKillFailed constant in place of the raw literal.
			audit.EmitEntry(al, &audit.Entry{
				Event:    audit.EventProcessKillFailed,
				Decision: audit.DecisionError,
				Details: map[string]any{
					"pid":    pid,
					"error":  killErr.Error(),
					"caller": caller,
				},
			})
		}
	} else {
		t.killAuditFn = nil
	}
}

var (
	defaultDenyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
		regexp.MustCompile(`\bdel\s+/[fq]\b`),
		regexp.MustCompile(`\brmdir\s+/s\b`),
		// Match disk wiping commands (must be followed by space/args)
		regexp.MustCompile(
			`\b(format|mkfs|diskpart)\b\s`,
		),
		regexp.MustCompile(`\bdd\s+if=`),
		// Block writes to block devices (all common naming schemes).
		regexp.MustCompile(
			`>\s*/dev/(sd[a-z]|hd[a-z]|vd[a-z]|xvd[a-z]|nvme\d|mmcblk\d|loop\d|dm-\d|md\d|sr\d|nbd\d)`,
		),
		regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),
		// Fork-bomb guard. Widened in v0.2 #155 item 5 to match every
		// documented bypass shape:
		//   `: ( ) { :|:& };:`        (whitespace anywhere)
		//   `b(){b|b};b`              (disguised with arbitrary identifier)
		//   `:(){ :|:& \n };:`        (newline inside braces)
		// Three changes from the original `:\(\)\s*\{.*\};\s*:`:
		//   1. `(?s)` lets `.` cross `\n` so the body can span lines
		//   2. The function name accepts any shell identifier
		//      (`[A-Za-z_]\w*`) OR the literal `:`. Without backreferences
		//      in RE2 we cannot enforce that the head-name and tail-name
		//      match, so we accept any identifier in both positions. False
		//      positives are acceptable since they just deny commands that
		//      LOOK like fork bombs.
		//   3. `[|&]` constraint inside the body keeps the false-positive
		//      surface tight: the recursive call must contain a pipe or
		//      ampersand, which is what makes a bomb a bomb.
		// RLIMIT_NPROC in hardened_exec_linux.go is the kernel-layer
		// backstop for any shape that still slips through.
		regexp.MustCompile(`(?s)([A-Za-z_]\w*|:)\s*\(\s*\)\s*\{[^{}]*[|&][^{}]*\}\s*;\s*([A-Za-z_]\w*|:)`),
		regexp.MustCompile(`\$\([^)]+\)`),
		regexp.MustCompile(`\$\{[^}]+\}`),
		regexp.MustCompile("`[^`]+`"),
		regexp.MustCompile(`\|\s*sh\b`),
		regexp.MustCompile(`\|\s*bash\b`),
		regexp.MustCompile(`;\s*rm\s+-[rf]`),
		regexp.MustCompile(`&&\s*rm\s+-[rf]`),
		regexp.MustCompile(`\|\|\s*rm\s+-[rf]`),
		regexp.MustCompile(`<<\s*EOF`),
		regexp.MustCompile(`\$\(\s*cat\s+`),
		regexp.MustCompile(`\$\(\s*curl\s+`),
		regexp.MustCompile(`\$\(\s*wget\s+`),
		regexp.MustCompile(`\$\(\s*which\s+`),
		regexp.MustCompile(`\bsudo\b`),
		regexp.MustCompile(`\bchmod\s+[0-7]{3,4}\b`),
		regexp.MustCompile(`\bchown\b`),
		regexp.MustCompile(`\bpkill\b`),
		regexp.MustCompile(`\bkillall\b`),
		regexp.MustCompile(`\bkill\b`),
		regexp.MustCompile(`\bcurl\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bwget\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bnpm\s+install\s+-g\b`),
		regexp.MustCompile(`\bpip\s+install\s+--user\b`),
		regexp.MustCompile(`\bapt\s+(install|remove|purge)\b`),
		regexp.MustCompile(`\byum\s+(install|remove)\b`),
		regexp.MustCompile(`\bdnf\s+(install|remove)\b`),
		regexp.MustCompile(`\bdocker\s+run\b`),
		regexp.MustCompile(`\bdocker\s+exec\b`),
		regexp.MustCompile(`\bgit\s+push\b`),
		regexp.MustCompile(`\bgit\s+force\b`),
		regexp.MustCompile(`\bssh\b.*@`),
		regexp.MustCompile(`\beval\b`),
		regexp.MustCompile(`\bsource\s+.*\.sh\b`),
		regexp.MustCompile(`<\([^)]*\)`),
		regexp.MustCompile(`>\([^)]*\)`),
		// v0.2 #155 item 8 — secrets-subtree path-guard (option B backstop).
		regexp.MustCompile(`\bmaster\.key\b`),
		regexp.MustCompile(`\bcredentials\.json\b`),
	}

	// absolutePathPattern matches absolute file paths in commands (Unix and Windows).
	absolutePathPattern = regexp.MustCompile(`[A-Za-z]:\\[^\\\"']+|/[^\s\"']+`)

	// safePaths are kernel pseudo-devices that are always safe to reference in
	// commands, regardless of workspace restriction. They contain no user data
	// and cannot cause destructive writes.
	safePaths = map[string]bool{
		"/dev/null":    true,
		"/dev/zero":    true,
		"/dev/random":  true,
		"/dev/urandom": true,
		"/dev/stdin":   true,
		"/dev/stdout":  true,
		"/dev/stderr":  true,
	}
)

func NewExecTool(workingDir string, restrict bool, allowPaths ...[]*regexp.Regexp) (*ExecTool, error) {
	return NewExecToolWithConfig(workingDir, restrict, nil, allowPaths...)
}

// NewExecToolWithDeps constructs an ExecTool with optional Wave 2 security
// dependencies. Callers that do not need the policy auditor or sandbox backend
// should use NewExecToolWithConfig, which passes a zero-valued ExecToolDeps.
func NewExecToolWithDeps(
	workingDir string,
	restrict bool,
	cfg *config.Config,
	deps ExecToolDeps,
	allowPaths ...[]*regexp.Regexp,
) (*ExecTool, error) {
	tool, err := NewExecToolWithConfig(workingDir, restrict, cfg, allowPaths...)
	if err != nil {
		return nil, err
	}
	tool.policyAuditor = deps.PolicyAuditor
	tool.sandboxBackend = deps.SandboxBackend
	tool.sandboxPolicy = deps.SandboxPolicy
	tool.execProxy = deps.ExecProxy
	tool.sandboxMode = deps.SandboxMode
	tool.sandboxEgressProxy = deps.EgressProxy
	tool.execTimeoutSeconds = deps.ExecTimeoutSeconds
	return tool, nil
}

// sandboxOn reports whether the exec tool should route children through the
// hardened-exec path (sandbox.Run / ApplyChildHardening). The decision is
// explicit-opt-in: only "enforce" and "permissive" turn it on. Any other
// value — including "off" and the empty string — preserves today's behavior
// (`sh -c <cmd>`, no proxy, no workspace cwd unless the caller passes one).
//
// Empty string explicitly maps to OFF so the many test paths that construct
// an ExecTool via NewExecTool / NewExecToolWithConfig (no deps) continue to
// behave exactly as before. Production boot routes through
// NewExecToolWithDeps which sets SandboxMode from cfg.Sandbox.ResolvedMode(),
// so production is unaffected by this default.
func (t *ExecTool) sandboxOn() bool {
	if t == nil {
		return false
	}
	switch t.sandboxMode {
	case string(sandbox.ModeEnforce), string(sandbox.ModePermissive):
		return true
	default:
		return false
	}
}

// applySandboxToCmd applies the configured sandbox policy to a child process
// before Start(). When no backend is configured this is a no-op.
//
// On Linux 5.13+ the kernel backend is effectively a no-op because
// Landlock+seccomp are already in effect on the Omnipus process and inherit
// to children. On unsupported kernels and other platforms, the
// FallbackBackend injects OMNIPUS_SANDBOX_PATHS so cooperative helper scripts
// can self-enforce.
//
// The returned error is already prefixed with "sandbox setup failed: " so
// callers can pass it directly to ErrorResult.
func (t *ExecTool) applySandboxToCmd(cmd *exec.Cmd) error {
	if t.sandboxBackend == nil {
		return nil
	}
	if err := t.sandboxBackend.ApplyToCmd(cmd, t.sandboxPolicy); err != nil {
		return fmt.Errorf("sandbox setup failed: %v", err)
	}
	return nil
}

func NewExecToolWithConfig(
	workingDir string,
	restrict bool,
	config *config.Config,
	allowPaths ...[]*regexp.Regexp,
) (*ExecTool, error) {
	denyPatterns := make([]*regexp.Regexp, 0)
	customAllowPatterns := make([]*regexp.Regexp, 0)
	var allowedPathPatterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		allowedPathPatterns = allowPaths[0]
	}

	if config != nil {
		execConfig := config.Tools.Exec
		enableDenyPatterns := execConfig.EnableDenyPatterns
		if enableDenyPatterns {
			denyPatterns = append(denyPatterns, defaultDenyPatterns...)
			if len(execConfig.CustomDenyPatterns) > 0 {
				logger.InfoCF(
					"shell",
					"Using custom deny patterns",
					map[string]any{"patterns": execConfig.CustomDenyPatterns},
				)
				for _, pattern := range execConfig.CustomDenyPatterns {
					re, err := regexp.Compile(pattern)
					if err != nil {
						return nil, fmt.Errorf("invalid custom deny pattern %q: %w", pattern, err)
					}
					denyPatterns = append(denyPatterns, re)
				}
			}
		} else {
			// If deny patterns are disabled, we won't add any patterns, allowing all commands.
			logger.WarnCF("shell", "deny patterns are disabled — all commands will be allowed", nil)
		}
		for _, pattern := range execConfig.CustomAllowPatterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid custom allow pattern %q: %w", pattern, err)
			}
			customAllowPatterns = append(customAllowPatterns, re)
		}
	} else {
		denyPatterns = append(denyPatterns, defaultDenyPatterns...)
	}

	var timeout time.Duration
	if config != nil && config.Tools.Exec.TimeoutSeconds > 0 {
		timeout = time.Duration(config.Tools.Exec.TimeoutSeconds) * time.Second
	}

	var maxBgSecs int
	if config != nil {
		maxBgSecs = config.Tools.Exec.MaxBackgroundSeconds
	}

	return &ExecTool{
		workingDir:           workingDir,
		timeout:              timeout,
		maxBackgroundSeconds: maxBgSecs,
		denyPatterns:         denyPatterns,
		allowPatterns:        nil,
		customAllowPatterns:  customAllowPatterns,
		allowedPathPatterns:  allowedPathPatterns,
		restrictToWorkspace:  restrict,
		sessionManager:       getSessionManager(),
	}, nil
}

func (t *ExecTool) Name() string {
	return "exec"
}

func (t *ExecTool) Scope() ToolScope { return ScopeCore }

func (t *ExecTool) Description() string {
	return `Execute shell commands. Use background=true for long-running commands (returns sessionId). Use pty=true for interactive commands (can combine with background=true). Use poll/read/write/send-keys/kill with sessionId to manage background sessions. Sessions auto-cleanup 30 minutes after process exits; use kill to terminate early. Output buffer limit: 1MB.`
}

func (t *ExecTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"run", "list", "poll", "read", "write", "kill", "send-keys"},
				"description": "Action: run (execute command), list (show sessions), poll (check status), read (get output), write (send input), kill (terminate), send-keys (send keys to PTY)",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute (required for run)",
			},
			"sessionId": map[string]any{
				"type":        "string",
				"description": "Session ID (required for poll/read/write/kill/send-keys)",
			},
			"keys": map[string]any{
				"type":        "string",
				"description": "Key names for send-keys: up, down, left, right, enter, tab, escape, backspace, ctrl-c, ctrl-d, home, end, pageup, pagedown, f1-f12",
			},
			"data": map[string]any{
				"type":        "string",
				"description": "Data to write to stdin (required for write)",
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "Run in background immediately",
			},
			"pty": map[string]any{
				"type":        "boolean",
				"description": "Run in a pseudo-terminal (PTY) when available",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory for the command",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (0 = no timeout)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, _ := args["action"].(string)
	if action == "" {
		return ErrorResult("action is required")
	}

	switch action {
	case "run":
		return t.executeRun(ctx, args)
	case "list":
		return t.executeList()
	case "poll":
		return t.executePoll(args)
	case "read":
		return t.executeRead(args)
	case "write":
		return t.executeWrite(args)
	case "kill":
		return t.executeKill(args)
	case "send-keys":
		return t.executeSendKeys(args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

func (t *ExecTool) executeRun(ctx context.Context, args map[string]any) *ToolResult {
	command, ok := args["command"].(string)
	if !ok {
		return ErrorResult("command is required")
	}

	// GHSA-pv8c-p6jf-3fpp channel block removed. exec is now governed
	// purely by per-agent ToolPolicyCfg. Agents that should not use exec on
	// remote channels must have exec: deny in their tool policy.

	getBoolArg := func(key string) bool {
		switch v := args[key].(type) {
		case bool:
			return v
		case string:
			return v == "true"
		}
		return false
	}
	isPty := getBoolArg("pty")
	isBackground := getBoolArg("background")

	if isPty {
		if runtime.GOOS == "windows" {
			return ErrorResult("PTY is not supported on Windows. Use background=true without pty.")
		}
	}

	cwd := t.workingDir
	if wd, ok := args["cwd"].(string); ok && wd != "" {
		if t.restrictToWorkspace && t.workingDir != "" {
			resolvedWD, err := validatePathWithAllowPaths(wd, t.workingDir, true, t.allowedPathPatterns)
			if err != nil {
				return ErrorResult("Command blocked by safety guard (" + err.Error() + ")")
			}
			cwd = resolvedWD
		} else {
			cwd = wd
		}
	}

	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ErrorResult(fmt.Sprintf("cannot determine working directory: %v", err))
		}
		cwd = wd
	}

	if guardError := t.guardCommand(command, cwd); guardError != "" {
		return ErrorResult(guardError)
	}

	// SEC-05: Binary allowlist enforcement (Wave 2).
	//
	// Delegates unconditionally to the policy auditor, which auto-logs every
	// decision to the audit log (ADR-002 §W-3). The evaluator decides whether
	// to enforce based on its own default_policy + allowed_binaries state:
	//   - empty allowlist + default_policy="allow" → always allows
	//   - empty allowlist + default_policy="deny" → always denies
	//   - non-empty allowlist → matches against patterns, honors default_policy on miss
	// This is the automated enforcement layer — the interactive approval
	// prompt is handled separately by the agent loop's HookManager.ApproveTool
	// which routes through the WebSocket approval channel, so we deliberately
	// do not call ExecApprovalManager.CheckApproval() here (that would block
	// on stdin in headless deployments).
	if t.policyAuditor != nil {
		agentID := ToolAgentID(ctx)
		decision := t.policyAuditor.EvaluateExec(agentID, command)
		if !decision.Allowed {
			return ErrorResult(fmt.Sprintf("Command blocked by exec allowlist: %s", decision.PolicyRule))
		}
	}

	// Re-resolve symlinks immediately before execution to shrink the TOCTOU window
	// between validation and cmd.Dir assignment.
	if t.restrictToWorkspace && t.workingDir != "" && cwd != t.workingDir {
		resolved, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return ErrorResult(fmt.Sprintf("Command blocked by safety guard (path resolution failed: %v)", err))
		}
		if isAllowedPath(resolved, t.allowedPathPatterns) {
			cwd = resolved
		} else {
			absWorkspace, absErr := filepath.Abs(t.workingDir)
			if absErr != nil {
				return ErrorResult(
					fmt.Sprintf("Command blocked by safety guard (workspace path resolution failed: %v)", absErr),
				)
			}
			wsResolved, symlinkErr := filepath.EvalSymlinks(absWorkspace)
			if symlinkErr != nil {
				return ErrorResult(
					fmt.Sprintf(
						"Command blocked by safety guard (workspace symlink resolution failed: %v)",
						symlinkErr,
					),
				)
			}
			if wsResolved == "" {
				wsResolved = absWorkspace
			}
			rel, err := filepath.Rel(wsResolved, resolved)
			if err != nil || !filepath.IsLocal(rel) {
				return ErrorResult("Command blocked by safety guard (working directory escaped workspace)")
			}
			cwd = resolved
		}
	}

	if isBackground {
		return t.runBackground(ctx, command, cwd, isPty)
	}

	return t.runSync(ctx, command, cwd)
}

func (t *ExecTool) runSync(ctx context.Context, command, cwd string) *ToolResult {
	// Sandbox-on path: route the child through sandbox.Run so it picks up
	// workspace cwd, the egress proxy, RLIMITs, npm cache, scrubbed env, and
	// inherits the gateway's kernel Landlock + seccomp policy (FS rules + the
	// new bind port allow-list). The kernel layer is the load-bearing
	// piece — without it, the agent could still bind 5173 and serve the
	// internet directly, defeating the purpose of web_serve.
	if t.sandboxOn() {
		return t.runSyncHardened(ctx, command, cwd)
	}

	// Legacy path (sandbox=off): trust mode. Operator opted out of the
	// sandbox; exec runs as the user with full host latitude (sudo if the
	// user has it, any port, any path the user can reach). This is the
	// documented contract for ModeOff.
	//
	// Security: even on the legacy path, scrub gateway secrets from the
	// inherited environment unconditionally. This prevents a prompt-injected
	// LLM from reading OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, or
	// OMNIPUS_BEARER_TOKEN via `exec env`. The master-key leak is a total
	// credential-store compromise regardless of sandbox mode.

	// timeout == 0 means no timeout
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if t.timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, t.timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}
	// Unconditionally scrub sensitive gateway env vars so children never see
	// OMNIPUS_MASTER_KEY/OMNIPUS_KEY_FILE/OMNIPUS_BEARER_TOKEN regardless of
	// sandbox mode. This replaces the nil Env (which would inherit the full
	// process environment including secrets) with a scrubbed copy.
	cmd.Env = sandbox.ScrubGatewayEnv()

	prepareCommandForTermination(cmd)

	// SEC-01/02/03: Apply sandbox restrictions to the child process before Start().
	// See applySandboxToCmd for the threat-model rationale.
	if err := t.applySandboxToCmd(cmd); err != nil {
		return ErrorResult(err.Error())
	}

	// SEC-28: Route child-process HTTP(S) traffic through the loopback SSRF
	// proxy. PrepareCmd must run AFTER applySandboxToCmd so the sandbox env
	// setup does not clobber the proxy vars, and BEFORE Start() so the child
	// inherits them. CmdDone() balances the proxy's active-cmd counter once
	// cmd.Wait() returns, regardless of exit path (success, error, timeout).
	if t.execProxy != nil {
		t.execProxy.PrepareCmd(cmd)
		defer t.execProxy.CmdDone()
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var err error
	select {
	case err = <-done:
	case <-cmdCtx.Done():
		// H5-BK: use the pre-built killAuditFn stored on the ExecTool so all
		// kill sites share one closure. Nil-safe: terminateProcessTree and the
		// fallback both guard on killAuditFn != nil before calling.
		if termErr := terminateProcessTree(cmd, t.killAuditFn); termErr != nil {
			logger.WarnCF("shell", "terminateProcessTree error", map[string]any{"error": termErr.Error()})
		}
		select {
		case err = <-done:
		case <-time.After(2 * time.Second):
			// H2-BK: the 2-second graceful-exit window elapsed. This is the
			// absolute last-resort kill. Failure here means the shell child is
			// permanently loose — surface it through the same audit hook used by
			// terminateProcessTree so operators see the event in the audit log.
			if cmd.Process != nil {
				if killErr := cmd.Process.Kill(); killErr != nil {
					// Use errors.Is instead of string comparison (quick sweep 1).
					if !errors.Is(killErr, os.ErrProcessDone) && !errors.Is(killErr, syscall.ESRCH) {
						slog.Error("shell: runSync fallback Kill failed; child PID may be loose",
							"pid", cmd.Process.Pid, "error", killErr)
						if t.killAuditFn != nil {
							t.killAuditFn(cmd.Process.Pid, killErr, "runSync_fallback_kill")
						}
					}
				}
			}
			err = <-done
		}
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			msg := fmt.Sprintf("Command timed out after %v", t.timeout)
			if output != "" {
				msg += "\n\nPartial output before timeout:\n" + output
			}
			return &ToolResult{
				ForLLM:  msg,
				ForUser: msg,
				IsError: true,
				Err:     fmt.Errorf("command timeout: %w", err),
			}
		}

		// Extract detailed exit information
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode := exitErr.ExitCode()
			output += fmt.Sprintf("\n\n[Command exited with code %d]", exitCode)

			// Add signal information if killed by signal (Unix)
			if exitCode == -1 {
				output += " (killed by signal)"
			}
		} else {
			output += fmt.Sprintf("\n\n[Command failed: %v]", err)
		}
	}

	if output == "" {
		output = "(no output)"
	}

	maxLen := 10000
	if len(output) > maxLen {
		totalLen := len(output)
		output = output[:maxLen] + fmt.Sprintf("\n... (truncated, %d more chars)", totalLen-maxLen)
	}

	if err != nil {
		return &ToolResult{
			ForLLM:  output,
			ForUser: output,
			IsError: true,
		}
	}

	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
		IsError: false,
	}
}

// runSyncHardened runs the command via sandbox.Run so it inherits the
// gateway's Landlock + seccomp policy and gets workspace cwd, scrubbed env,
// and the egress proxy injected. Output shape mirrors the legacy runSync
// path so the agent UX does not change.
func (t *ExecTool) runSyncHardened(ctx context.Context, command, cwd string) *ToolResult {
	argv := buildShellArgv(command)

	wsDir := cwd
	if wsDir == "" {
		wsDir = t.workingDir
	}

	lim := sandbox.Limits{
		TimeoutSeconds: t.execTimeoutSeconds,
		WorkspaceDir:   wsDir,
	}
	if t.sandboxEgressProxy != nil {
		lim.EgressProxyAddr = t.sandboxEgressProxy.Addr()
	}

	res, err := sandbox.Run(ctx, argv, nil, lim)
	if err != nil {
		return ErrorResult(fmt.Sprintf("sandbox.Run failed: %v", err))
	}

	output := string(res.Stdout)
	if len(res.Stderr) > 0 {
		if output != "" {
			output += "\n"
		}
		output += "STDERR:\n" + string(res.Stderr)
	}

	if res.TimedOut {
		msg := fmt.Sprintf("Command timed out after %d seconds", t.execTimeoutSeconds)
		if output != "" {
			msg += "\n\nPartial output before timeout:\n" + output
		}
		return &ToolResult{
			ForLLM:  msg,
			ForUser: msg,
			IsError: true,
			Err:     errors.New("command timeout"),
		}
	}

	if res.ExitCode != 0 {
		output += fmt.Sprintf("\n\n[Command exited with code %d]", res.ExitCode)
		if res.ExitCode == -1 {
			output += " (killed by signal)"
		}
	}

	if output == "" {
		output = "(no output)"
	}

	const maxLen = 10000
	if len(output) > maxLen {
		totalLen := len(output)
		output = output[:maxLen] + fmt.Sprintf("\n... (truncated, %d more chars)", totalLen-maxLen)
	}

	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
		IsError: res.ExitCode != 0,
	}
}

// sandboxLimitsEnv builds the environment for background sessions on the
// sandbox-on path. It starts from sandbox.ScrubGatewayEnv() (which under
// v0.2 #155 item 3 enforces a closed allowlist of PATH/HOME/LANG/LC_*/XDG_*/
// OMNIPUS_CHILD_*) and then layers the Limits-derived injections
// (HTTP_PROXY, npm_config_cache) on top so they take precedence on duplicate
// keys (POSIX exec(3) semantics).
func sandboxLimitsEnv(lim sandbox.Limits) []string {
	scrubbed := sandbox.ScrubGatewayEnv()
	if lim.EgressProxyAddr != "" {
		proxyURL := "http://" + lim.EgressProxyAddr
		scrubbed = append(scrubbed,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
			"NO_PROXY=127.0.0.1,localhost,::1",
			"no_proxy=127.0.0.1,localhost,::1",
		)
	}
	if lim.WorkspaceDir != "" {
		scrubbed = append(scrubbed, "npm_config_cache="+lim.WorkspaceDir+"/.npm-cache")
	}
	return scrubbed
}

// buildShellArgv returns the platform-appropriate shell argv to execute a
// free-form command. Mirrors the legacy runSync / runBackground branches so
// shell features (pipes, &&, redirects) keep working on the sandbox-on path.
func buildShellArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", command}
	}
	return []string{"sh", "-c", command}
}

func (t *ExecTool) runBackground(ctx context.Context, command, cwd string, ptyEnabled bool) *ToolResult {
	sessionID := generateSessionID()
	session := &ProcessSession{
		ID:         sessionID,
		Command:    command,
		PTY:        ptyEnabled,
		Background: true,
		StartTime:  time.Now().Unix(),
		Status:     "running",
		ptyKeyMode: PtyKeyModeCSI,
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Unconditionally scrub sensitive gateway env vars so background children
	// never see OMNIPUS_MASTER_KEY/OMNIPUS_KEY_FILE/OMNIPUS_BEARER_TOKEN
	// regardless of sandbox mode. The sandbox-on path below will override
	// cmd.Env with sandboxLimitsEnv (which also scrubs), but we set the
	// scrubbed base here so the sandbox-off legacy path is covered too.
	cmd.Env = sandbox.ScrubGatewayEnv()

	prepareCommandForTermination(cmd)

	var stdoutReader io.ReadCloser
	var stderrReader io.ReadCloser
	var stdinWriter io.WriteCloser
	var ptySlaveToClose io.Closer

	if ptyEnabled {
		ptmx, tty, err := pty.Open()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to create PTY: %v", err))
		}

		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty
		ptySlaveToClose = tty

		// For PTY, we need Setsid to create a new session.
		// Note: Setsid and Setpgid conflict, so we must replace SysProcAttr entirely.
		setSysProcAttrForPty(cmd)

		session.ptyMaster = ptmx
	} else {
		var err error
		stdoutReader, err = cmd.StdoutPipe()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to create stdout pipe: %v", err))
		}
		stderrReader, err = cmd.StderrPipe()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to create stderr pipe: %v", err))
		}
		stdinWriter, err = cmd.StdinPipe()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to create stdin pipe: %v", err))
		}
		session.stdinWriter = stdinWriter
	}

	// SEC-01/02/03: Apply sandbox restrictions to the background child process
	// before Start(). Must happen after the PTY / pipe setup above (which may
	// replace SysProcAttr) but before Start().
	if err := t.applySandboxToCmd(cmd); err != nil {
		if session.ptyMaster != nil {
			session.ptyMaster.Close()
		}
		if ptySlaveToClose != nil {
			ptySlaveToClose.Close()
		}
		return ErrorResult(err.Error())
	}

	// Sandbox-on path for background sessions: inject the same Limits-derived
	// env (HTTP_PROXY/HTTPS_PROXY, NO_PROXY, npm_config_cache) that
	// sandbox.Run applies on the foreground path, and apply Setpgid + Pdeathsig
	// process-group hardening via ApplyChildHardening when not in PTY mode.
	// PTY sessions require Setsid (set by setSysProcAttrForPty above), which
	// conflicts with Setpgid; skip the SysProcAttr step for PTY. Kernel
	// Landlock + seccomp + the bind/connect port allow-list are process-wide
	// and inherit to the child regardless, so the security-critical pieces
	// are still in force on the PTY path.
	var hardenedLim sandbox.Limits
	if t.sandboxOn() {
		hardenedLim = sandbox.Limits{
			TimeoutSeconds: 0, // background sessions are governed by maxBackgroundSeconds
			WorkspaceDir:   cwd,
		}
		if t.sandboxEgressProxy != nil {
			hardenedLim.EgressProxyAddr = t.sandboxEgressProxy.Addr()
		}
		// Build env: scrub gateway secrets, then layer the Limits injections.
		// We let mergeEnv do the work by handing its output back via cmd.Env.
		cmd.Env = sandboxLimitsEnv(hardenedLim)

		if !ptyEnabled {
			if err := sandbox.ApplyChildHardening(cmd, hardenedLim); err != nil {
				if session.ptyMaster != nil {
					session.ptyMaster.Close()
				}
				if ptySlaveToClose != nil {
					ptySlaveToClose.Close()
				}
				return ErrorResult(fmt.Sprintf("sandbox hardening failed: %v", err))
			}
		}
	}

	// SEC-28: Route child-process HTTP(S) traffic through the loopback SSRF
	// proxy. Unlike runSync we cannot defer CmdDone() here because the command
	// runs asynchronously; the paired CmdDone() call is made from the wait
	// goroutine below (one for PTY mode, one for non-PTY mode). We use
	// proxyActive to make the counter increment conditional on PrepareCmd
	// actually having been called, and to avoid a double-decrement if Start()
	// fails after PrepareCmd.
	proxyActive := false
	if t.execProxy != nil {
		t.execProxy.PrepareCmd(cmd)
		proxyActive = true
	}

	// When the sandbox is on, the spawn must happen on a thread that has
	// Landlock re-applied so the child inherits the kernel domain. Without
	// this hop, Go's M:N scheduler routes the fork through whichever worker
	// thread is currently running this goroutine, and that thread is almost
	// never the boot thread that received restrict_self at gateway start —
	// so the child silently escapes the sandbox. PTY mode skips Setpgid but
	// still needs the per-thread re-apply for the Landlock inheritance to
	// kick in.
	startErr := func() error {
		if t.sandboxOn() {
			return sandbox.StartLocked(cmd)
		}
		return cmd.Start()
	}()
	if startErr != nil {
		// Start failed after PrepareCmd: balance the counter immediately so
		// the proxy's idle watcher can still shut it down.
		if proxyActive {
			t.execProxy.CmdDone()
			proxyActive = false
		}
		if session.ptyMaster != nil {
			session.ptyMaster.Close()
		}
		if ptySlaveToClose != nil {
			ptySlaveToClose.Close()
		}
		return ErrorResult(fmt.Sprintf("failed to start command: %v", startErr))
	}
	if ptySlaveToClose != nil {
		ptySlaveToClose.Close()
	}

	session.PID = cmd.Process.Pid
	t.sessionManager.Add(session)

	session.outputBuffer = &bytes.Buffer{}

	// PTY mode: read from ptyMaster and wait for process
	// Note: On Linux, closing ptyMaster doesn't interrupt blocking Read() calls,
	// so we need cmd.Wait() in a separate goroutine to detect process exit.
	if session.PTY && session.ptyMaster != nil {
		go func() {
			waitErr := cmd.Wait() // Wait for process to exit
			// SEC-28: Balance the proxy's active-cmd counter. CmdDone()
			// must run after cmd.Wait() (not before Start) so the child's
			// network lifetime is fully covered by the proxy.
			if proxyActive {
				t.execProxy.CmdDone()
			}
			session.mu.Lock()
			if cmd.ProcessState != nil {
				session.ExitCode = cmd.ProcessState.ExitCode()
			} else {
				// ProcessState is nil: process did not exit normally.
				if waitErr != nil {
					logger.WarnCF("shell", "PTY cmd.Wait returned error with nil ProcessState",
						map[string]any{"session_id": sessionID, "error": waitErr.Error()})
				}
				session.ExitCode = -1
			}
			session.Status = "done"
			session.mu.Unlock()
		}()

		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := session.ptyMaster.Read(buf)
				if n > 0 {
					raw := string(buf[:n])
					if mode := detectPtyKeyMode(raw); mode != PtyKeyModeNotFound && mode != session.GetPtyKeyMode() {
						session.SetPtyKeyMode(mode)
					}

					session.mu.Lock()
					if session.outputBuffer.Len() >= maxOutputBufferSize {
						if !session.outputTruncated {
							session.outputBuffer.WriteString(outputTruncateMarker)
							session.outputTruncated = true
						}
					} else {
						session.outputBuffer.Write(buf[:n])
					}
					session.mu.Unlock()
				}
				if err != nil {
					break
				}
			}
		}()
	} else {
		// Non-PTY mode: read stdout and stderr concurrently to avoid pipe-buffer deadlocks.
		// Each goroutine drains one pipe until EOF. A third goroutine waits for both to
		// finish before calling cmd.Wait() and marking the session done.
		pipeReadFn := func(r io.Reader) {
			buf := make([]byte, 4096)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					session.mu.Lock()
					if session.outputBuffer.Len() >= maxOutputBufferSize {
						if !session.outputTruncated {
							session.outputBuffer.WriteString(outputTruncateMarker)
							session.outputTruncated = true
						}
					} else {
						session.outputBuffer.Write(buf[:n])
					}
					session.mu.Unlock()
				}
				if err != nil {
					break
				}
			}
		}

		var pipeWG sync.WaitGroup
		pipeWG.Add(2)
		go func() { defer pipeWG.Done(); pipeReadFn(stdoutReader) }()
		go func() { defer pipeWG.Done(); pipeReadFn(stderrReader) }()

		go func() {
			pipeWG.Wait()
			if stdinWriter != nil {
				stdinWriter.Close()
			}
			waitErr := cmd.Wait()
			// SEC-28: Balance the proxy's active-cmd counter after the
			// process has fully exited and its pipes have drained.
			if proxyActive {
				t.execProxy.CmdDone()
			}
			session.mu.Lock()
			if cmd.ProcessState != nil {
				session.ExitCode = cmd.ProcessState.ExitCode()
			} else {
				// ProcessState is nil: process did not exit normally.
				if waitErr != nil {
					logger.WarnCF("shell", "non-PTY cmd.Wait returned error with nil ProcessState",
						map[string]any{"session_id": sessionID, "error": waitErr.Error()})
				}
				session.ExitCode = -1
			}
			session.Status = "done"
			session.mu.Unlock()
		}()
	}

	// Background process hard-kill timeout (FR-007).
	// If maxBackgroundSeconds > 0, send SIGTERM after the timeout, then SIGKILL after 5s.
	if t.maxBackgroundSeconds > 0 {
		sid := sessionID
		maxBgDuration := time.Duration(t.maxBackgroundSeconds) * time.Second
		// Capture cmd.Process so we use the OS handle rather than the PID integer,
		// eliminating the PID-reuse race (SC1/SH2): the handle stays valid until
		// cmd.Wait() returns, even if the PID is recycled by the OS.
		proc := cmd.Process
		go func() {
			timer := time.NewTimer(maxBgDuration)
			defer timer.Stop()
			<-timer.C

			// Atomically: check status, kill via OS handle, update status.
			// Using the OS process handle (cmd.Process) instead of the raw PID prevents
			// the PID-reuse race where a newly spawned unrelated process inherits the PID.
			session.mu.Lock()
			if session.Status != "running" {
				session.mu.Unlock()
				return
			}
			pid := session.PID
			session.mu.Unlock()

			logger.WarnCF("shell", "Background session exceeded max_background_seconds; sending SIGTERM",
				map[string]any{
					"session_id":             sid,
					"pid":                    pid,
					"max_background_seconds": t.maxBackgroundSeconds,
				})

			// Use gracefulKillProcessGroup with the captured process handle for SIGTERM,
			// then fall back to SIGKILL via the OS handle.
			gracefulKillProcessGroup(pid, 5*time.Second)
			// Ensure the process is gone via the safe OS handle (no PID reuse risk).
			// H5-BK: surface kill failures through the stored audit callback so
			// operators see a process_kill_failed event in the audit log, not just
			// a silent discard.
			if proc != nil {
				if killErr := proc.Kill(); killErr != nil {
					// Use errors.Is instead of string comparison (quick sweep 1).
					if !errors.Is(killErr, os.ErrProcessDone) && !errors.Is(killErr, syscall.ESRCH) {
						slog.Error("shell: background session hard-kill failed; child PID may be loose",
							"session_id", sid, "pid", pid, "error", killErr)
						if t.killAuditFn != nil {
							t.killAuditFn(pid, killErr, "bg_session_hard_kill")
						}
					}
				}
			}

			// Mark session done under lock only if still running (the wait goroutine
			// may have already transitioned it).
			session.mu.Lock()
			if session.Status == "running" {
				session.Status = "done"
				session.ExitCode = -1
			}
			session.mu.Unlock()
			logger.WarnCF("shell", "Background session hard-killed",
				map[string]any{"session_id": sid, "pid": pid})
		}()
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    "running",
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("shell", "failed to marshal exec start response", map[string]any{"error": marshalErr.Error()})
		data = []byte(fmt.Sprintf(`{"error":"failed to serialize response: %s"}`, marshalErr.Error()))
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s started", sessionID),
		IsError: marshalErr != nil,
	}
}

func (t *ExecTool) executeList() *ToolResult {
	sessions := t.sessionManager.List()
	resp := ExecResponse{
		Sessions: sessions,
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("shell", "failed to marshal exec list response", map[string]any{"error": marshalErr.Error()})
		data = []byte(fmt.Sprintf(`{"error":"failed to serialize response: %s"}`, marshalErr.Error()))
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("%d active sessions", len(sessions)),
		IsError: marshalErr != nil,
	}
}

func (t *ExecTool) executePoll(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    session.GetStatus(),
		ExitCode:  session.GetExitCode(),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("shell", "failed to marshal exec poll response", map[string]any{"error": marshalErr.Error()})
		data = []byte(fmt.Sprintf(`{"error":"failed to serialize response: %s"}`, marshalErr.Error()))
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: marshalErr != nil,
	}
}

func (t *ExecTool) executeRead(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	output := session.Read()

	resp := ExecResponse{
		SessionID: sessionID,
		Output:    output,
		Status:    session.GetStatus(),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("shell", "failed to marshal exec read response", map[string]any{"error": marshalErr.Error()})
		data = []byte(fmt.Sprintf(`{"error":"failed to serialize response: %s"}`, marshalErr.Error()))
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: marshalErr != nil,
	}
}

func (t *ExecTool) executeWrite(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	data, ok := args["data"].(string)
	if !ok {
		return ErrorResult("data is required")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	if session.IsDone() {
		return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
	}

	if err := session.Write(data); err != nil {
		if errors.Is(err, ErrSessionDone) {
			return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
		}
		return ErrorResult(fmt.Sprintf("failed to write to session: %v", err))
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    session.GetStatus(),
	}
	respData, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("shell", "failed to marshal exec write response", map[string]any{"error": marshalErr.Error()})
		respData = []byte(fmt.Sprintf(`{"error":"failed to serialize response: %s"}`, marshalErr.Error()))
	}
	return &ToolResult{
		ForLLM:  string(respData),
		IsError: marshalErr != nil,
	}
}

func (t *ExecTool) executeKill(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	if session.IsDone() {
		return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
	}

	if err := session.Kill(); err != nil {
		// H5-BK: emit an audit entry when kill_session fails so operators
		// can detect loose processes via the audit log, not just the tool
		// error response. Use the stored killAuditFn built at SetAuditLogger
		// time — nil when auditLogger is not wired (god-mode / tests).
		if t.killAuditFn != nil {
			t.killAuditFn(session.PID, err, "kill_session_tool")
		}
		return ErrorResult(fmt.Sprintf("failed to kill session: %v", err))
	}

	t.sessionManager.Remove(sessionID)

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    "done",
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("shell", "failed to marshal exec kill response", map[string]any{"error": marshalErr.Error()})
		data = []byte(fmt.Sprintf(`{"error":"failed to serialize response: %s"}`, marshalErr.Error()))
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s killed", sessionID),
		IsError: marshalErr != nil,
	}
}

// keyMap maps key names to their escape sequences.
var keyMap = map[string]string{
	"enter":     "\r",
	"return":    "\r",
	"tab":       "\t",
	"escape":    "\x1b",
	"esc":       "\x1b",
	"space":     " ",
	"backspace": "\x7f",
	"bspace":    "\x7f",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"home":      "\x1b[1~",
	"end":       "\x1b[4~",
	"pageup":    "\x1b[5~",
	"pagedown":  "\x1b[6~",
	"pgup":      "\x1b[5~",
	"pgdn":      "\x1b[6~",
	"insert":    "\x1b[2~",
	"ic":        "\x1b[2~",
	"delete":    "\x1b[3~",
	"del":       "\x1b[3~",
	"dc":        "\x1b[3~",
	"btab":      "\x1b[Z",
	"f1":        "\x1bOP",
	"f2":        "\x1bOQ",
	"f3":        "\x1bOR",
	"f4":        "\x1bOS",
	"f5":        "\x1b[15~",
	"f6":        "\x1b[17~",
	"f7":        "\x1b[18~",
	"f8":        "\x1b[19~",
	"f9":        "\x1b[20~",
	"f10":       "\x1b[21~",
	"f11":       "\x1b[23~",
	"f12":       "\x1b[24~",
}

// ss3KeysMap maps key names to SS3 escape sequences
var ss3KeysMap = map[string]string{
	"up":    "\x1bOA",
	"down":  "\x1bOB",
	"right": "\x1bOC",
	"left":  "\x1bOD",
	"home":  "\x1bOH",
	"end":   "\x1bOF",
}

func detectPtyKeyMode(raw string) PtyKeyMode {
	const SMKX = "\x1b[?1h"
	const RMKX = "\x1b[?1l"

	lastSmkx := strings.LastIndex(raw, SMKX)
	lastRmkx := strings.LastIndex(raw, RMKX)

	if lastSmkx == -1 && lastRmkx == -1 {
		return PtyKeyModeNotFound
	}

	if lastSmkx > lastRmkx {
		return PtyKeyModeSS3
	}
	return PtyKeyModeCSI
}

// encodeKeyToken encodes a single key token into its escape sequence.
// Supports:
//   - Named keys: "enter", "tab", "up", "ctrl-c", "alt-x", etc.
//   - Ctrl modifier: "ctrl-c" or "c-c" (sends Ctrl+char)
//   - Alt modifier: "alt-x" or "m-x" (sends ESC+char)
func encodeKeyToken(token string, ptyKeyMode PtyKeyMode) (string, error) {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return "", nil
	}

	// Handle ctrl-X format (c-x)
	if strings.HasPrefix(token, "c-") {
		char := token[2]
		if char >= 'a' && char <= 'z' {
			return string(rune(char) & 0x1f), nil // ctrl-a through ctrl-z
		}
		return "", fmt.Errorf("invalid ctrl key: %s", token)
	}

	// Handle ctrl-X format (ctrl-x)
	if strings.HasPrefix(token, "ctrl-") {
		char := token[5]
		if char >= 'a' && char <= 'z' {
			return string(rune(char) & 0x1f), nil
		}
		return "", fmt.Errorf("invalid ctrl key: %s", token)
	}

	// Handle alt-X format (m-x or alt-x)
	if strings.HasPrefix(token, "m-") || strings.HasPrefix(token, "alt-") {
		var char string
		if strings.HasPrefix(token, "m-") {
			char = token[2:]
		} else {
			char = token[4:]
		}
		if len(char) == 1 {
			return "\x1b" + char, nil
		}
		return "", fmt.Errorf("invalid alt key: %s", token)
	}

	// Handle shift modifier for special keys (shift-up, shift-down, etc.)
	if strings.HasPrefix(token, "s-") || strings.HasPrefix(token, "shift-") {
		var key string
		if strings.HasPrefix(token, "s-") {
			key = token[2:]
		} else {
			key = token[6:]
		}
		// Apply shift modifier: for single-char keys, return uppercase
		if seq, ok := keyMap[key]; ok {
			// For escape sequences, we can't easily add shift
			// For single-char keys (letters), return uppercase
			if len(seq) == 1 {
				return strings.ToUpper(seq), nil
			}
			return seq, nil
		}
		return "", fmt.Errorf("unknown key with shift: %s", key)
	}

	if ptyKeyMode == PtyKeyModeSS3 {
		if seq, ok := ss3KeysMap[token]; ok {
			return seq, nil
		}
	}

	if seq, ok := keyMap[token]; ok {
		return seq, nil
	}

	return "", fmt.Errorf("unknown key: %s (use write action for text input)", token)
}

// encodeKeySequence encodes a slice of key tokens into a single string.
func encodeKeySequence(tokens []string, ptyKeyMode PtyKeyMode) (string, error) {
	var result string
	for _, token := range tokens {
		seq, err := encodeKeyToken(token, ptyKeyMode)
		if err != nil {
			return "", err
		}
		result += seq
	}
	return result, nil
}

func (t *ExecTool) executeSendKeys(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	keysStr, ok := args["keys"].(string)
	if !ok {
		return ErrorResult("keys must be a string")
	}

	if keysStr == "" {
		return ErrorResult("keys cannot be empty")
	}

	// Parse comma-separated key names
	keyNames := strings.Split(keysStr, ",")
	var keys []string
	for _, k := range keyNames {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}

	if len(keys) == 0 {
		return ErrorResult("keys cannot be empty")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	ptyKeyMode := session.GetPtyKeyMode()

	data, err := encodeKeySequence(keys, ptyKeyMode)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid key: %v", err))
	}

	if session.IsDone() {
		return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
	}

	if err := session.Write(data); err != nil {
		if errors.Is(err, ErrSessionDone) {
			return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
		}
		return ErrorResult(fmt.Sprintf("failed to send keys: %v", err))
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    "running",
		Output:    fmt.Sprintf("Sent keys: %v", keys),
	}
	respData, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("shell", "failed to marshal exec sendkeys response", map[string]any{"error": marshalErr.Error()})
		respData = []byte(fmt.Sprintf(`{"error":"failed to serialize response: %s"}`, marshalErr.Error()))
	}
	return &ToolResult{
		ForLLM:  string(respData),
		IsError: marshalErr != nil,
	}
}

func (t *ExecTool) guardCommand(command, cwd string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	// Custom allow patterns exempt a command from deny checks.
	explicitlyAllowed := false
	for _, pattern := range t.customAllowPatterns {
		if pattern.MatchString(lower) {
			explicitlyAllowed = true
			break
		}
	}

	if !explicitlyAllowed {
		for _, pattern := range t.denyPatterns {
			if pattern.MatchString(lower) {
				return "Command blocked by safety guard (dangerous pattern detected)"
			}
		}
	}

	if len(t.allowPatterns) > 0 {
		allowed := false
		for _, pattern := range t.allowPatterns {
			if pattern.MatchString(lower) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "Command blocked by safety guard (not in allowlist)"
		}
	}

	if t.restrictToWorkspace {
		// Shallow supplementary guard: catches obvious literal traversal sequences.
		// The real enforcement is the absolute-path regex validator below.
		if strings.Contains(cmd, "..\\") || strings.Contains(cmd, "../") {
			return "Command blocked by safety guard (path traversal detected)"
		}

		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return "cannot resolve working directory"
		}

		// Web URL schemes whose path components (starting with //) should be exempt
		// from workspace sandbox checks. file: is intentionally excluded so that
		// file:// URIs are still validated against the workspace boundary.
		webSchemes := []string{"http:", "https:", "ftp:", "ftps:", "sftp:", "ssh:", "git:"}

		matchIndices := absolutePathPattern.FindAllStringIndex(cmd, -1)

		for _, loc := range matchIndices {
			raw := cmd[loc[0]:loc[1]]

			// Skip URL path components that look like they're from web URLs.
			// When a URL like "https://github.com" is parsed, the regex captures
			// "//github.com" as a match (the path portion after "https:").
			// Use the exact match position (loc[0]) so that duplicate //path substrings
			// in the same command are each evaluated at their own position.
			if strings.HasPrefix(raw, "//") && loc[0] > 0 {
				before := cmd[:loc[0]]
				isWebURL := false

				for _, scheme := range webSchemes {
					if strings.HasSuffix(before, scheme) {
						isWebURL = true
						break
					}
				}

				if isWebURL {
					continue
				}
			}

			p, err := filepath.Abs(raw)
			if err != nil {
				return "Command blocked by safety guard (cannot resolve path)"
			}

			if safePaths[p] {
				continue
			}
			if isAllowedPath(p, t.allowedPathPatterns) {
				continue
			}

			rel, err := filepath.Rel(cwdPath, p)
			if err != nil {
				return "Command blocked by safety guard (cannot resolve relative path)"
			}

			if strings.HasPrefix(rel, "..") {
				return "Command blocked by safety guard (path outside working dir)"
			}
		}
	}

	return ""
}

func (t *ExecTool) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

func (t *ExecTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *ExecTool) SetAllowPatterns(patterns []string) error {
	t.allowPatterns = make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("invalid allow pattern %q: %w", p, err)
		}
		t.allowPatterns = append(t.allowPatterns, re)
	}
	return nil
}
