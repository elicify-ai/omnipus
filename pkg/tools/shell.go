// The unified `bash` tool (ADR-036, bash-tool-spec.md).
//
// `bash` replaces the three previously-separate shell tools (`exec`,
// `workspace_shell`, `workspace_shell_bg`) with ONE tool, ONE policy surface,
// and ONE hardening path. See docs/internal/architecture/ADR-036-consolidate-
// shell-and-subagent-tools.md and docs/internal/specs/bash-tool-spec.md for
// the full rationale and FR/BDD traceability. Highlights:
//
//   - Registration is universal (every agent), governed exclusively by
//     ToolPolicyCfg — the old experimental.workspace_shell_enabled gate is
//     retired (FR-B8).
//   - `cwd` is relative-to-workspace ONLY; there is no absolute-path escape
//     hatch (FR-B2/FR-B13). The guard resolves symlinks before the
//     containment check, so a workspace-internal symlink pointing outside the
//     workspace cannot be used to escape it.
//   - The hardcoded deny-pattern baseline (rm -rf /, master.key/
//     credentials.json literal guards, curl-pipe-to-shell, fork bomb, ...)
//     applies unconditionally — no policy verdict or operator configuration
//     can disable it (FR-B4). It is layered with an operator-extensible
//     custom-pattern mechanism (global + per-agent), which IS opt-in/off by
//     default.
//   - `pkg/policy.Evaluator.EvaluateExec` (the binary allowlist, SEC-05)
//     applies identically to foreground and background calls (FR-B5).
//   - Every non-god-mode invocation routes through `sandbox.ResolveLimits` +
//     `sandbox.ApplyChildHardening`/`sandbox.Run` (ADR-035 §7) — there is no
//     longer a separate "sandbox off but not god mode" state; the fixed
//     kernel-sandbox boundary is universal except when the global god-mode
//     override (agent.GodModeActive) is active (FR-B6).
//   - Audit-log write failures fail CLOSED — the call is refused rather than
//     silently proceeding unaudited (FR-B7).
//   - PTY / interactive sessions (send-keys, write) and free-form background
//     port-exposure (the old workspace_shell_bg capability) are DROPPED
//     entirely — accepted capability reductions per ADR-036 §3.1. The
//     `web_serve` tool covers the legitimate dev-server use case.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/policy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// ExecPolicyAuditor evaluates a bash command against the policy engine and
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

// ExecToolDeps bundles the ADR-035/ADR-036 dependencies for the bash tool.
// All fields are optional — a nil PolicyAuditor disables binary allowlist
// enforcement (useful when the policy layer is not configured).
//
// Note: the interactive approval layer (SEC-08, "ask" prompts) and the
// allow/ask/deny TOOL POLICY gate are handled upstream of this tool entirely
// (HookManager.ApproveTool / the compositor's EffectiveToolPolicy resolution)
// — a "deny" verdict means Execute is never called at all, so bash does not
// re-implement that check. What DOES live here is the narrower, automated
// binary allowlist (SEC-05) and the deny-pattern/sandbox layers that apply
// regardless of the policy verdict.
type ExecToolDeps struct {
	// PolicyAuditor enforces the binary allowlist (SEC-05) and audit-logs the
	// decision. Nil disables the check (default-permissive).
	PolicyAuditor ExecPolicyAuditor

	// GodMode reflects agent.GodModeActive(cfg), resolved ONCE at wiring time.
	// When true, ApplyChildHardening/sandbox.Run are skipped entirely and the
	// command runs with full host latitude (see runUnconstrained).
	GodMode bool

	// Proxy is the process-wide kernel-sandbox egress proxy (SSRF
	// protection). May be nil (no HTTP_PROXY injection on the sandbox-on
	// path); ignored entirely under GodMode.
	Proxy *sandbox.EgressProxy

	// AuditFailClosed: when true, an audit-log write failure aborts execution
	// (FR-B7) rather than proceeding unaudited.
	AuditFailClosed bool

	// GlobalShellDenyPatterns is the operator-global, OPT-IN deny-pattern
	// extension list (config.Sandbox.ShellDenyPatterns). Layered ON TOP of
	// (never a substitute for) the hardcoded baseline, and only consulted
	// when AgentShellPolicy.EnableDenyPatterns is true.
	GlobalShellDenyPatterns []string

	// AgentShellPolicy is the per-agent shell policy (AgentConfig.ShellPolicy).
	// Nil means no per-agent custom patterns and EnableDenyPatterns=false
	// (operator-extensible layer off; the hardcoded baseline still applies).
	AgentShellPolicy *config.AgentShellPolicy
}

var (
	globalSessionManager = NewSessionManager()
	sessionManagerMu     sync.RWMutex
)

// marshalErrorFallback builds a safe minimal JSON payload for the rare case
// where json.Marshal(resp) itself fails on one of the ExecResponse
// constructions below. Uses encoding/json rather than fmt.Sprintf's %s,
// which interpolated marshalErr.Error() — an arbitrary, unescaped string —
// directly into a JSON string literal: exactly the #618 hand-built-JSON
// defect class (Go-string/verb substitution instead of JSON quoting) fixed
// elsewhere in this codebase (pkg/tools/result.go's marshalWithinBudget
// producers). json.Marshal of a map[string]string cannot itself fail for any
// valid Go string — encoding/json replaces invalid UTF-8 rather than
// rejecting it — so the inner error branch is unreachable in practice; it
// exists only so this helper never panics or returns invalid JSON.
func marshalErrorFallback(marshalErr error) []byte {
	data, err := json.Marshal(map[string]string{
		"error": "failed to serialize response: " + marshalErr.Error(),
	})
	if err != nil {
		return []byte(`{"error":"failed to serialize response"}`)
	}
	return data
}

func getSessionManager() *SessionManager {
	sessionManagerMu.RLock()
	defer sessionManagerMu.RUnlock()
	return globalSessionManager
}

// ExecTool implements the `bash` builtin tool (registered name: "bash";
// the Go type retains its historical "Exec" name to minimize unrelated churn
// across ~20 call sites — see bash-tool-spec.md Assumptions: "the exact final
// home of the merged implementation ... is an implementer's naming choice").
type ExecTool struct {
	BaseTool

	workingDir          string
	restrictToWorkspace bool
	allowedPathPatterns []*regexp.Regexp

	// denyPatterns is the hardcoded baseline (defaultDenyPatterns).
	// Unconditional: FR-B4 forbids disabling this via policy or config.
	denyPatterns []*regexp.Regexp

	// operatorDenyPatterns is the OPT-IN, operator-extensible layer (global +
	// per-agent custom patterns), only consulted when
	// enableOperatorDenyPatterns is true. Layered on top of denyPatterns,
	// never a substitute for it.
	operatorDenyPatterns       []*regexp.Regexp
	enableOperatorDenyPatterns bool

	sessionManager *SessionManager

	// policyAuditor enforces the binary allowlist (SEC-05) uniformly for
	// foreground and background calls (FR-B5).
	policyAuditor ExecPolicyAuditor

	// godMode / proxy: see ExecToolDeps. Resolved once at wiring time.
	godMode bool
	proxy   *sandbox.EgressProxy

	// auditLogger + auditFailClosed implement FR-B7 (fail-closed on audit
	// write failure).
	auditLogger     *audit.Logger
	auditFailClosed bool

	// killAuditFn is a pre-built audit callback for process-kill failures,
	// constructed once at SetAuditLogger time so every kill site (foreground
	// timeout, background timeout, explicit kill action) shares one closure.
	// Nil when auditLogger is nil.
	killAuditFn func(pid int, killErr error, caller string)

	// backgroundSweepFn, when non-nil, replaces sweepAfterRun on the
	// background completion path ONLY (sweepBackgroundCompletion). It is nil
	// in every production build; it exists so a test can make the sweep panic
	// and prove the completion goroutine survives it.
	backgroundSweepFn func(ctx context.Context, command, cwd, baseDir string, started time.Time, result *ToolResult) *ToolResult

	documentRuntime *documentruntime.Layout
	documentAdmin   bool
}

// SetDocumentRuntime makes the versioned document toolchain reachable from
// this bash tool. The caller decides whether this agent is the setup-owning
// Admin; all other agents receive read+execute access only.
func (t *ExecTool) SetDocumentRuntime(layout documentruntime.Layout, admin bool) {
	copy := layout
	t.documentRuntime = &copy
	t.documentAdmin = admin
}

// GodModeForTest exposes the resolved god-mode flag for white-box testing.
// Kept as a production method because pkg/agent tests require cross-package
// access (mirrors the pre-consolidation WorkspaceShellTool.GodModeForTest).
func (t *ExecTool) GodModeForTest() bool { return t.godMode }

// SetAuditLogger injects an audit.Logger into the ExecTool so exec/deny
// decisions and kill failures are recorded. Satisfies the auditLoggerAware
// contract used by the ToolRegistry. Calling this on a nil ExecTool is a no-op.
func (t *ExecTool) SetAuditLogger(l *audit.Logger) {
	if t == nil {
		return
	}
	t.auditLogger = l
	if l == nil {
		t.killAuditFn = nil
		return
	}
	al := l
	t.killAuditFn = func(pid int, killErr error, caller string) {
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
}

var (
	// defaultDenyPatterns is the hardcoded deny-pattern baseline, ported
	// verbatim from the pre-consolidation `exec` tool (FR-B4). Applies
	// UNCONDITIONALLY — no policy verdict or operator configuration disables
	// this list.
	//
	// The secrets-subtree literal guards (v0.2 #155 item 8) are appended below
	// via secretGuardPatterns rather than hand-copied here — see that var's doc
	// comment for why hand-copying is exactly the bug this replaces.
	defaultDenyPatterns = append([]*regexp.Regexp{
		regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
		regexp.MustCompile(`\bdel\s+/[fq]\b`),
		regexp.MustCompile(`\brmdir\s+/s\b`),
		// Match disk wiping commands (must be followed by space/args)
		regexp.MustCompile(
			`(?:^|[;&|]\s*|sudo\s+)(format|mkfs|diskpart)\s`,
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
		// RLIMIT_NPROC in hardened_exec_linux.go is the kernel-layer
		// backstop for any shape that still slips through.
		regexp.MustCompile(`(?s)([A-Za-z_]\w*|:)\s*\(\s*\)\s*\{[^{}]*[|&][^{}]*\}\s*;\s*([A-Za-z_]\w*|:)`),
		// NOTE: the blanket `\$\([^)]+\)` rule that used to sit here —
		// "reject ANY command substitution" — was removed. It blocked benign
		// substitutions (`$(seq 1 5)`, `$(date)`, `$(pwd)`), making bounded
		// `for` loops unusable, and it made the four `$(cat|curl|wget|which `
		// rules below it unreachable. Command substitutions are now judged
		// STRUCTURALLY by substitutionGuard (shell_subst_guard.go), which is
		// applied on this same unconditional baseline path in guardCommand and
		// preserves every dangerous shape the blanket rule caught. Do not
		// reinstate a blanket rule here without reading that file's threat
		// notes first.
		regexp.MustCompile(`\$\{[^}]+\}`),
		regexp.MustCompile("`[^`]+`"),
		regexp.MustCompile(`\|\s*sh\b`),
		regexp.MustCompile(`\|\s*bash\b`),
		regexp.MustCompile(`;\s*rm\s+-[rf]`),
		regexp.MustCompile(`&&\s*rm\s+-[rf]`),
		regexp.MustCompile(`\|\|\s*rm\s+-[rf]`),
		// REMOVED (ADR-068 §3, 2026-08-23): `regexp.MustCompile("<<\\s*EOF")`.
		// Do NOT restore it. applyDenyPatterns LOWERCASES the command before
		// matching (lowerASCII, shell_guard.go:32), so an uppercase `EOF`
		// literal could never match anything: the rule was unreachable from the
		// day it was written and blocked exactly zero commands over its whole
		// life. Deleting it therefore changes no runtime behaviour.
		//
		// Making it fire instead would have been a REGRESSION dressed up as a
		// fix — heredocs (`cat > notes.md << EOF … EOF`) are an ordinary,
		// currently-working way for an agent to write a file, and the ADR-068
		// investigation confirmed the deny layer allows every heredoc write
		// shape UAT defect 003 reported as blocked (those were blocked by the
		// path-containment scan below, not here).
		//
		// Two tests pin this: TestDefaultDenyPatterns_HeredocsArePermitted
		// asserts heredocs stay allowed, and
		// TestDefaultDenyPatterns_ContainNoUppercaseOnlyLiterals asserts no
		// pattern in this list can contain an uppercase-only literal again —
		// the whole bug class, not just this one instance (shell_guard_test.go).
		// The four substitution rules below are now ALSO covered by
		// substitutionGuard's R2 (which additionally handles `$(/bin/cat …)`,
		// `$(FOO=1 curl …)` and mid-pipeline positions). They are retained
		// verbatim as literal, cheap redundancy: if the structural scanner ever
		// regresses, these still fire.
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
	}, secretGuardPatterns...)

	// secretGuardPatterns is the v0.2 #155 item 8 secrets-subtree literal-text
	// backstop (option B), generated FROM fspolicy.SecretEntriesAlways rather
	// than hand-copied.
	//
	// It used to be two hardcoded lines here — `\bmaster\.key\b` and
	// `\bcredentials\.json\b` — written when the secret set had exactly those
	// two entries. The set has since grown to five (config.json, cli.token,
	// entities joined master.key and credentials.json; see
	// fspolicy.SecretEntriesAlways), and grew again since (auth.json, backups).
	// The hand-copied pair never gained any of them: this guard is a backstop
	// over a boundary the kernel sandbox already enforces, so its silent
	// drift was invisible in every test that exercises the kernel deny
	// instead. A backstop that covers 2 of N entries and looks like it covers
	// all of them is worse than no backstop, because a reviewer reads
	// "secrets-subtree path-guard" and stops checking.
	//
	// Generating the list closes that class of drift structurally — there is
	// no second copy to fall behind. TestSecretGuardPatterns_CoverEverySecretEntryAlways
	// (shell_secret_guard_test.go) is the regression: it fails the moment
	// SecretEntriesAlways gains an entry this can't already reach, which is
	// possible only if this generation is ever replaced with a literal list
	// again.
	//
	// Scoped to SecretEntriesAlways, not the combined SecretEntriesRelative:
	// the per-turn half (agents/, workspaces/) is made of ordinary English
	// words an agent legitimately types constantly ("list the workspaces",
	// "check the agents dir"), and the own-tree exception that makes reaching
	// them sometimes correct (fspolicy.DeniedPathsFor) is inherently
	// contextual — a static text guard has no turn to evaluate that against.
	// The five ALWAYS names are never legitimate in ANY turn, which is what
	// makes a context-free literal match safe for them and not for the rest.
	secretGuardPatterns = buildSecretGuardPatterns()
)

// buildSecretGuardPatterns compiles one case-insensitive-by-construction
// (applyDenyPatterns lowercases the command before matching) word-boundary
// regex per fspolicy.SecretEntriesAlways entry.
func buildSecretGuardPatterns() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(fspolicy.SecretEntriesAlways))
	for _, name := range fspolicy.SecretEntriesAlways {
		out = append(out, regexp.MustCompile(`\b`+regexp.QuoteMeta(strings.ToLower(name))+`\b`))
	}
	return out
}

// NewExecTool constructs a minimal bash tool with no deps injected (test /
// metadata-only use).
func NewExecTool(workingDir string, restrict bool, allowPaths ...[]*regexp.Regexp) (*ExecTool, error) {
	return NewExecToolWithConfig(workingDir, restrict, nil, allowPaths...)
}

// NewExecToolWithDeps constructs an ExecTool with the full ADR-036 dependency
// set (policy auditor, god-mode, egress proxy, audit-fail-closed, deny
// patterns). Callers that do not need these should use NewExecToolWithConfig.
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
	tool.godMode = deps.GodMode
	tool.proxy = deps.Proxy
	tool.auditFailClosed = deps.AuditFailClosed

	tool.operatorDenyPatterns = compileDenyPatterns(deps.GlobalShellDenyPatterns, "global")
	if deps.AgentShellPolicy != nil {
		tool.enableOperatorDenyPatterns = deps.AgentShellPolicy.EnableDenyPatterns
		tool.operatorDenyPatterns = append(
			tool.operatorDenyPatterns,
			compileDenyPatterns(deps.AgentShellPolicy.CustomDenyPatterns, "agent")...,
		)
	}
	return tool, nil
}

// NewExecToolWithConfig constructs a bash tool without the ADR-036 deps
// (policy auditor / god-mode / proxy / operator deny patterns default to
// off/nil). Used for early registration (before the AgentLoop's dependencies
// are ready — see pkg/agent/instance.go) and for metadata-only catalog
// instances (pkg/tools/general_builtin_catalog.go). cfg is accepted for
// signature stability with existing call sites; the ADR-036 config-derived
// deps are supplied later via NewExecToolWithDeps.
func NewExecToolWithConfig(
	workingDir string,
	restrict bool,
	_ *config.Config,
	allowPaths ...[]*regexp.Regexp,
) (*ExecTool, error) {
	var allowedPathPatterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		allowedPathPatterns = allowPaths[0]
	}

	return &ExecTool{
		workingDir:          workingDir,
		restrictToWorkspace: restrict,
		allowedPathPatterns: allowedPathPatterns,
		denyPatterns:        defaultDenyPatterns,
		sessionManager:      getSessionManager(),
	}, nil
}

func (t *ExecTool) Name() string { return "bash" }

func (t *ExecTool) Scope() ToolScope { return ScopeCore }

func (t *ExecTool) Category() ToolCategory { return CategoryShell }

func (t *ExecTool) Description() string {
	return "Execute a shell command (foreground or backgrounded) in the workspace and get its output.\n" +
		"sh -c on Linux/macOS, powershell on Windows. Set run_in_background=true for long-running commands " +
		"(returns a session_id immediately); use action=poll/read/kill with that session_id to check on it, " +
		"read incremental output, or terminate it. cwd is relative to the workspace only (no absolute paths, " +
		"no '..' escapes). timeout_seconds defaults to 300 and must be between 1 and 3600; enforced identically " +
		"in the foreground and in the background — a background session times out on its own after " +
		"timeout_seconds elapses, and is otherwise stopped only by an explicit kill action or an explicit " +
		"session cancel. Output is truncated beyond a size cap — a SUCCEEDING command keeps up to 64,000 " +
		"characters, a FAILING one only 10,000 (the failure cap is smaller, so a large error command's output " +
		"is cut harder than a successful one's); redirect to a file and read it with read_file/offset when you " +
		"need all of it. Commands are screened by a safety guard (deny patterns, a binary allowlist, and a " +
		"path-use check) before they run — writing outside your workspace requires a mount first (see " +
		"list_mounts / request_mount); a \"blocked by safety guard\" or \"blocked by exec allowlist\" error means " +
		"the guard refused the command, not that it failed to run. Admin can publish a staged document " +
		"runtime with the exact command `" + documentruntime.FinalizeCommand + "`."
}

func (t *ExecTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute (required for action=run)",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional human-readable description of what this command does (documentation only)",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory relative to the workspace (e.g. 'my-project'). Absolute paths and '..' escapes are rejected. Defaults to the workspace root.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Wall-clock timeout in seconds, applied identically in the foreground and background. Default 300. Must be between 1 and 3600.",
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "Run the command in the background and return immediately with a session_id (default false).",
			},
			"persistent": map[string]any{
				"type":        "boolean",
				"description": "Reserved for a future long-lived session mode. Only meaningful together with run_in_background=true (default false).",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"run", "poll", "read", "kill"},
				"description": "run (default): execute command. poll/read/kill: manage a background session_id.",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Background session id (required for action=poll/read/kill)",
			},
		},
	}
}

// --- Execute / AsyncExecutor ------------------------------------------------

var _ AsyncExecutor = (*ExecTool)(nil)

func (t *ExecTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, nil)
}

// ExecuteAsync implements AsyncExecutor. cb is only ever invoked for a
// run_in_background=true call: it is captured by the background-completion
// goroutine and fired exactly once — on natural completion, failure, timeout,
// or kill (FR-B9) — regardless of how long after this call returns that
// happens. Every other action (foreground run, poll, read, kill) never
// invokes cb; it is simply unused for those calls.
func (t *ExecTool) ExecuteAsync(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	return t.execute(ctx, args, cb)
}

func (t *ExecTool) execute(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	action, _ := args["action"].(string)
	if action == "" {
		action = "run"
	}

	switch action {
	case "run":
		return t.executeRun(ctx, args, cb)
	case "poll":
		return t.executePoll(ctx, args)
	case "read":
		return t.executeRead(ctx, args)
	case "kill":
		return t.executeKill(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

func getBoolArg(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

const (
	defaultTimeoutSeconds = int32(300)
	minTimeoutSeconds     = int32(1)
	maxTimeoutSeconds     = int32(3600)
)

// resolveTimeoutSeconds implements FR-B15: a timeout_seconds value outside
// [1, 3600] is REJECTED as invalid input, never clamped or silently ignored.
// Absent/nil defaults to 300 (the documented default).
func resolveTimeoutSeconds(args map[string]any) (int32, error) {
	raw, ok := args["timeout_seconds"]
	if !ok || raw == nil {
		return defaultTimeoutSeconds, nil
	}
	var v int64
	switch n := raw.(type) {
	case float64:
		v = int64(n)
	case int:
		v = int64(n)
	case int32:
		v = int64(n)
	case int64:
		v = n
	default:
		return 0, fmt.Errorf("timeout_seconds must be a number")
	}
	if v < int64(minTimeoutSeconds) || v > int64(maxTimeoutSeconds) {
		return 0, fmt.Errorf(
			"timeout_seconds must be between %d and %d (got %d)",
			minTimeoutSeconds, maxTimeoutSeconds, v,
		)
	}
	return int32(v), nil
}

func (t *ExecTool) executeRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return ErrorResult("command is required and must be a non-empty string")
	}
	command = strings.TrimSpace(command)
	if command == documentruntime.FinalizeCommand {
		if auditResult := t.emitAuditOrDeny(ctx, command, t.workingDir); auditResult != nil {
			return auditResult
		}
		return t.finalizeDocumentRuntime()
	}

	runInBackground := getBoolArg(args, "run_in_background")
	persistent := getBoolArg(args, "persistent")
	if persistent && !runInBackground {
		return ErrorResult("persistent requires run_in_background to also be true")
	}

	timeoutSeconds, err := resolveTimeoutSeconds(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	// Resolve the base working directory for this call. When the turn
	// re-roots to a workspace dir (the agent is a member of that Workspace's
	// CoreTeam), the base becomes workspaces/<id>/ for this turn only;
	// otherwise it is the fixed agent dir. All cwd guards below are evaluated
	// relative to baseDir.
	baseDir := t.workingDir
	if d := TurnWorkspaceDir(ctx); d != "" {
		baseDir = d
	}

	cwd, cwdErr := t.resolveCWD(ctx, args, baseDir)
	if cwdErr != nil {
		t.emitAudit(ctx, command, "", audit.DecisionDeny)
		return ErrorResult(cwdErr.Error())
	}

	// FR-B4: hardcoded deny-pattern baseline (unconditional) + the opt-in
	// operator-extensible layer + the legacy command-text absolute-path scan.
	// The document probe is an immutable first-party command whose manifest
	// lives outside the agent work directory by design. Its filesystem access
	// is still confined by the per-turn kernel policy augmented below.
	isDocumentProbe := t.documentRuntime != nil && command == strings.Join(documentruntime.ProbeArgv(*t.documentRuntime), " ")
	if guardErr := t.guardCommand(ctx, command, cwd); guardErr != "" && !isDocumentProbe {
		t.emitAudit(ctx, command, cwd, audit.DecisionDeny)
		return ErrorResult(guardErr)
	}

	// FR-B5: binary allowlist (SEC-05), applied uniformly to foreground and
	// background — this check runs BEFORE the foreground/background branch
	// below, so both paths are covered by the same call site.
	if t.policyAuditor != nil {
		agentID := ToolAgentID(ctx)
		decision := t.policyAuditor.EvaluateExec(agentID, command)
		if !decision.Allowed {
			t.emitAudit(ctx, command, cwd, audit.DecisionDeny)
			return ErrorResult(fmt.Sprintf("Command blocked by exec allowlist: %s", decision.PolicyRule))
		}
	}

	// FR-B7: audit-log write failure fails CLOSED.
	if auditResult := t.emitAuditOrDeny(ctx, command, cwd); auditResult != nil {
		return auditResult
	}

	// FR-B6: route every non-god-mode invocation through sandbox.ResolveLimits.
	// P3 (ADR-046): EffectiveFSPolicy feeds the per-child Landlock ruleset
	// here — sandbox.ResolveLimits is where the fresh, per-call ruleset
	// (FR-023: deny -> working dir + libs + /tmp; ask -> +approved; allow ->
	// per the P3 spike's carve-out decision) will be computed and applied via
	// the non-latched apply path, once the P3 kernel-sandbox rearchitecture
	// lands. Today this remains the pre-ADR-046 god-mode/no-god-mode split.
	lim, limErr := sandbox.ResolveLimits(t.godMode, baseDir, t.proxy, timeoutSeconds)
	if limErr != nil {
		return ErrorResult(fmt.Sprintf("sandbox limits error: %v", limErr))
	}
	lim.WorkspaceDir = cwd

	// ADR-063 FR-3.5: carry THIS TURN's filesystem policy to the kernel, so the
	// child bash spawns is confined the same way the app-layer path resolver
	// confines this same turn's read_file/write_file.
	//
	// Without this the child inherited the BOOT profile, which grants
	// $OMNIPUS_HOME as one tree — so `bash` from any agent could read and write
	// every other agent's home and every workspace record, while the app layer
	// denied exactly those paths. That divergence was demonstrated against real
	// children, not inferred.
	//
	// God mode is excluded deliberately: it is an explicit operator opt-out of
	// confinement, and runForeground routes it to runUnconstrained anyway.
	if !t.godMode {
		kernelPolicy, kpErr := t.turnKernelPolicy(ctx)
		if kpErr != nil {
			t.emitAudit(ctx, command, cwd, audit.DecisionDeny)
			return ErrorResult(fmt.Sprintf("sandbox policy error: %v", kpErr))
		}
		lim.KernelPolicy = kernelPolicy
	}

	if runInBackground {
		ownerSessionID := ToolTranscriptSessionID(ctx)
		return t.runBackground(ctx, command, cwd, baseDir, timeoutSeconds, lim, ownerSessionID, cb)
	}
	started := time.Now()
	result := t.runForeground(ctx, command, lim, timeoutSeconds)
	return t.sweepAfterRun(ctx, command, cwd, baseDir, started, result)
}

func (t *ExecTool) finalizeDocumentRuntime() *ToolResult {
	if t.documentRuntime == nil {
		return ErrorResult("document runtime is not configured")
	}
	if !t.documentAdmin {
		return ErrorResult("document runtime setup is restricted to Admin")
	}
	data, err := os.ReadFile(t.documentRuntime.Manifest)
	if err != nil {
		return ErrorResult(fmt.Sprintf("document runtime manifest unavailable: %v", err))
	}
	var manifest documentruntime.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ErrorResult(fmt.Sprintf("invalid document runtime manifest: %v", err))
	}
	if err := documentruntime.FinalizeAdminSetup(*t.documentRuntime, manifest); err != nil {
		return ErrorResult(fmt.Sprintf("document runtime setup incomplete: %v", err))
	}
	return SilentResult(`{"ok":true,"component":"document_runtime","status":"ready"}`)
}

// sweepAfterRun runs the post-command escaping-symlink sweep (D-14, see
// shell_escape_sweep.go) over the turn's roots and appends its report to the
// tool result. The sweep REPORTS and never removes (Codex review 2026-09-14
// finding #2: a link inside the command's time window cannot be attributed
// to the command, and deleting an unattributable link destroys a person's
// work). Every finding is written to the audit log as well, so the operator
// sees it even if the agent ignores the notice. God mode is the operator's
// explicit opt-out of confinement and is skipped; so is an unrestricted tool
// (restrictToWorkspace=false), whose whole point is that the workspace is
// not a boundary. Background runs ARE swept — at their completion, in
// runBackground's completion goroutine, the one place that knows the
// process has exited and its pipes are drained, so the walk cannot race a
// command that is still writing (Claude review 2026-09-14: sweeping only
// the foreground path left a background run free to plant the very escape
// the sweep exists to name).
func (t *ExecTool) sweepAfterRun(ctx context.Context, command, cwd, baseDir string, started time.Time, result *ToolResult) *ToolResult {
	if result == nil || t.godMode || !t.restrictToWorkspace {
		return result
	}
	// baseDir is resolved through this package's own sanctioned resolver
	// (the one ResolvePath itself uses) rather than a locally glued
	// filepath.EvalSymlinks — FR-034 routes every path resolution in
	// pkg/tools through resolveRealpathUnderWorkDir (see grep.go's
	// guardCarveOuts comment for the established pattern).
	roots := []string{baseDir}
	if resolved, err := resolveRealpathUnderWorkDir(baseDir, ""); err == nil {
		roots = []string{resolved}
	}
	// mountRootsResolved records whether the turn's mount list could be
	// resolved at all. A nil AllowedRoots is NOT a failure (a workspace with
	// no mounts yields nil) — only an error is, and only the error leaves the
	// sweep ignorant of trees a link may legitimately point into. The sweep
	// still runs and still only REPORTS (nothing is removed, nothing is
	// refused on it), so the worst case is an over-broad finding, which the
	// honesty note below names rather than letting the notice claim
	// "... and its mounts" over mounts it never enumerated.
	mountRootsResolved := true
	if authored, err := ResolveTurnFSPolicy(ctx, t.workingDir, t.restrictToWorkspace); err == nil {
		roots = append(roots, authored.AllowedRoots...)
	} else {
		mountRootsResolved = false
	}
	res := sweepEscapingSymlinks(roots, roots, started)
	// Claude review 2026-09-14 cut-list: a partial sweep must be visible to
	// the OPERATOR too, not only in the tool result an agent can paraphrase
	// away — one WARN per run naming exactly what was skipped (mounts among
	// it), so a log reader knows the sweep was partial and where.
	if res.Partial {
		slog.Warn("bash: post-command symlink sweep was partial — some roots were not inspected",
			"agent_id", ToolAgentID(ctx),
			"partial_root", res.PartialRoot,
			"swept", strings.Join(res.Swept, ", "),
			"skipped", strings.Join(res.Skipped, ", "),
			"found", len(res.Found))
	}
	notice := escapeSweepNotice(res)
	if len(res.Found) > 0 && !mountRootsResolved {
		notice += escapeSweepMountsUnresolvedNote()
	}
	if notice == "" {
		return result
	}
	if len(res.Found) > 0 {
		links := make([]map[string]string, 0, len(res.Found))
		for _, r := range res.Found {
			links = append(links, map[string]string{"link": r.Link, "target": r.Target})
		}
		if t.auditLogger != nil {
			// The command itself was allowed and ran; this entry is a
			// warning attached to it, not a denial of anything. "removed"
			// is deliberately absent from the details: nothing was.
			if err := t.auditLogger.Log(&audit.Entry{
				Event:    audit.EventExec,
				Decision: audit.DecisionAllow,
				AgentID:  ToolAgentID(ctx),
				Tool:     t.Name(),
				Command:  command,
				Details: map[string]any{
					"cwd":               cwd,
					"warning":           "escaping_symlinks",
					"escaping_symlinks": links,
					"reason": "symlink(s) pointing outside the workspace appeared during this command's window; " +
						"they were reported, not removed (creator cannot be attributed with certainty) — operator review",
				},
			}); err != nil {
				slog.Warn("bash: audit write failed", "agent_id", ToolAgentID(ctx), "error", err)
			}
		}
	}
	result.ForLLM = result.ContentForLLM() + notice
	if result.ForUser != "" {
		result.ForUser += notice
	}
	return result
}

// escapeSweepMountsUnresolvedNote is appended to a sweep notice that reported
// findings while the turn's mount list could not be resolved (Claude review
// 2026-09-14 PLAUSIBLE, verified then hardened): without it the notice
// asserts the links point "outside the workspace and its mounts" over a mount
// list it never saw, and a link legitimately pointing INTO a mounted folder
// would be described as an escape. The sweep is report-only either way —
// nothing is removed or refused on these findings — so this is honesty about
// the report's own coverage, not a new enforcement.
func escapeSweepMountsUnresolvedNote() string {
	return "\n  Note: this turn's mount list could not be resolved for this sweep, so a link above that points into a MOUNTED folder may be listed although pointing into a mount is legitimate. Only the workspace itself was swept."
}

// turnKernelPolicy derives the per-turn kernel policy for this bash call from
// the SAME authored fspolicy.FSPolicy that every path-taking tool resolves
// (ResolveTurnFSPolicy), so the kernel and the app layer are answering "what
// may this turn touch" from one input rather than two.
//
// Returns (nil, nil) when no kernel policy is in force — sandbox off, or a
// platform that degraded to application-level enforcement. The spawn then uses
// whatever the boot profile is, exactly as before.
//
// Returns an error, rather than a nil policy, when a policy IS in force but
// could not be derived. Falling back on failure would hand the child the boot
// profile, which is the WIDER of the two — a derivation bug would then quietly
// restore the very cross-agent reach this exists to remove.
func (t *ExecTool) turnKernelPolicy(ctx context.Context) (*sandbox.SandboxPolicy, error) {
	if !sandbox.TurnPolicyBaseInstalled() {
		return nil, nil
	}
	// restrict is t.restrictToWorkspace, not the hardcoded true that cwd
	// resolution uses: cwd confinement is a separate, deliberately stricter
	// decision (see resolveCWD's note 3), while this is the turn's real posture.
	// Scope does not change the derived rules anyway — post-ADR-062 it governs
	// writes through the work dir, which is identical either way (FR-2.5).
	authored, err := ResolveTurnFSPolicy(ctx, t.workingDir, t.restrictToWorkspace)
	if err != nil {
		return nil, fmt.Errorf("resolve turn filesystem policy: %w", err)
	}
	policy, err := sandbox.KernelPolicyForTurn(authored)
	if err != nil || policy == nil || t.documentRuntime == nil {
		return policy, err
	}
	augmented := documentruntime.ApplySandboxAccess(*policy, *t.documentRuntime, t.documentAdmin)
	return &augmented, nil
}

// --- cwd resolution (FR-B2/FR-B13) -----------------------------------------

// resolveCWD resolves the optional cwd argument to an absolute path under
// baseDir. Absolute paths are rejected outright (no escape hatch); relative
// paths are resolved via ResolvePath (resolvepath.go, ADR-046's mandatory
// chokepoint), which also follows symlinks and anchors confinement on the
// realpath before the containment check — a workspace-internal symlink
// pointing outside the workspace is rejected the same way an absolute path
// is (FR-B13). Empty cwd defaults to baseDir.
//
// SEC-05/FR-B2 threat model: t.allowedPathPatterns is the operator-configured
// cross-tool allowlist that read_file/write_file/list_directory legitimately
// honor (e.g. the shared media/attachments temp dir — see
// buildAllowReadPatterns/mediaTempDirPattern in pkg/agent/instance.go). That
// directory is shared across ALL agents and ALL sessions, so it is NOT a safe
// place for bash to chdir into. bash's cwd must never consult that allowlist
// at all (mirrors the pre-consolidation workspace_shell.go, which rejected
// filepath.IsAbs(rawCWD) outright and passed nil — not the real patterns —
// into the legacy validator). Concretely:
//  1. Any absolute path is rejected unconditionally, with no allowlist-based
//     exception, before ResolvePath is ever called.
//  2. Below calls the PLAIN ResolvePath (never ResolvePathAllowingPatterns),
//     which has no patterns parameter at all — so a relative cwd that
//     traverses (e.g. "../../media") out to the shared media dir cannot
//     short-circuit past the containment check either; it is judged purely
//     on the effective working directory's confinement, same as any other
//     outside-workspace target.
//  3. The scope is forced to fspolicy.FSScopeConfined for this call
//     regardless of the exec tool's own restrictToWorkspace setting —
//     matching the pre-ADR-046 behavior, which always passed
//     restrict=true (hardcoded) into the legacy validator for cwd
//     resolution specifically, never t.restrictToWorkspace. bash's OWN
//     host-filesystem reach (when unrestricted) is governed entirely by
//     sandbox.ResolveLimits/god-mode below, not by widening cwd's escape
//     hatch.
func (t *ExecTool) resolveCWD(ctx context.Context, args map[string]any, baseDir string) (string, error) {
	rawCWD, _ := args["cwd"].(string)
	rawCWD = strings.TrimSpace(rawCWD)

	if filepath.IsAbs(rawCWD) {
		return "", fmt.Errorf("path escapes workspace: absolute path not allowed (use a relative path)")
	}

	policyForCWD, err := fspolicy.EffectiveFSPolicy(
		ctx, baseDir, "", true, config.OmnipusHomeDir(), ToolAgentID(ctx), ToolWorkspaceID(ctx),
	)
	if err != nil {
		return "", fmt.Errorf("workspace dir not resolvable: %w", err)
	}

	handle, err := ResolvePath(ctx, policyForCWD, "bash", "", FSOpExec, rawCWD)
	if err != nil {
		return "", fmt.Errorf("path escapes workspace: %w", err)
	}
	defer handle.Close()

	realPath, err := handle.RealPath()
	if err != nil {
		return "", fmt.Errorf("failed to resolve cwd: %w", err)
	}
	return realPath, nil
}

// --- audit helpers -----------------------------------------------------------

// emitAuditOrDeny writes an allow-decision audit.Entry before spawning. When
// the write fails and auditFailClosed is true, it returns a ToolResult that
// aborts execution (FR-B7). Returns nil to mean "continue".
func (t *ExecTool) emitAuditOrDeny(ctx context.Context, command, cwd string) *ToolResult {
	if t.auditLogger == nil {
		return nil
	}
	agentID := ToolAgentID(ctx)
	logErr := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: audit.DecisionAllow,
		AgentID:  agentID,
		Tool:     t.Name(),
		Command:  command,
		Details: map[string]any{
			"cwd":      cwd,
			"god_mode": t.godMode,
		},
	})
	if logErr == nil {
		return nil
	}
	if t.auditFailClosed {
		slog.Error("bash: audit logger degraded; refusing to execute (audit_fail_closed=true)",
			"agent_id", agentID, "command", command, "error", logErr)
		return &ToolResult{
			IsError: true,
			ForLLM:  "audit log write failed; refusing to execute (audit_fail_closed=true)",
			ForUser: "bash requires audit logging; aborting",
		}
	}
	slog.Warn("bash: audit write failed", "agent_id", agentID, "error", logErr)
	return nil
}

// emitAudit writes a deny-decision audit.Entry. Used on paths that are
// already rejected — fail-closed semantics do not apply here (the command was
// never going to run). Nil logger is a no-op.
func (t *ExecTool) emitAudit(ctx context.Context, command, cwd, decision string) {
	if t.auditLogger == nil {
		return
	}
	agentID := ToolAgentID(ctx)
	if err := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: decision,
		AgentID:  agentID,
		Tool:     t.Name(),
		Command:  command,
		Details: map[string]any{
			"cwd":      cwd,
			"god_mode": t.godMode,
		},
	}); err != nil {
		slog.Warn("bash: audit write failed", "agent_id", agentID, "error", err)
	}
}

// --- foreground execution ----------------------------------------------------

// buildShellArgv returns the platform-appropriate shell argv for a free-form
// command.
func buildShellArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", command}
	}
	return []string{"sh", "-c", command}
}

// runForeground executes command synchronously and returns its output. Under
// god mode, hardening is skipped entirely (runUnconstrained); otherwise the
// command is routed through sandbox.Run, which applies Landlock/seccomp
// inheritance, resource limits, the egress proxy, and the wall-clock timeout
// uniformly (FR-B6). timeoutSeconds is passed explicitly (not read from
// lim.TimeoutSeconds) because sandbox.ResolveLimits returns the ZERO VALUE
// under god mode (lim.TimeoutSeconds would be 0) — workspace_shell's run()
// used the same explicit-parameter pattern for exactly this reason.
func (t *ExecTool) runForeground(
	ctx context.Context,
	command string,
	lim sandbox.Limits,
	timeoutSeconds int32,
) *ToolResult {
	argv := buildShellArgv(command)

	if t.godMode {
		return t.runUnconstrained(ctx, argv, lim.WorkspaceDir, timeoutSeconds)
	}

	var env []string
	if t.documentRuntime != nil {
		env = documentruntime.ChildEnvironment(sandbox.ScrubGatewayEnv(), *t.documentRuntime)
	}
	res, err := sandbox.Run(ctx, argv, env, lim)
	if err != nil {
		return ErrorResult(fmt.Sprintf("sandbox.Run failed: %v", err))
	}
	return foregroundResultFromSandbox(res, timeoutSeconds)
}

func foregroundResultFromSandbox(res sandbox.Result, timeoutSeconds int32) *ToolResult {
	output := string(res.Stdout)
	if len(res.Stderr) > 0 {
		if output != "" {
			output += "\n"
		}
		output += "STDERR:\n" + string(res.Stderr)
	}

	if res.TimedOut {
		msg := fmt.Sprintf("Command timed out after %d seconds", timeoutSeconds)
		if output != "" {
			msg += "\n\nPartial output before timeout:\n" + output
		}
		return &ToolResult{
			ForLLM:   msg,
			ForUser:  msg,
			IsError:  true,
			Err:      errors.New("command timeout"),
			TimedOut: true,
		}
	}

	// review r2 HIGH-1: capture the real exit code in the structured,
	// truncation-immune field FIRST (see ToolResult.ExitCode's doc comment) —
	// this is what judge.go's interpretBashResult reads to adjudicate a
	// machine-check criterion, never the text below. The human-readable
	// suffix is appended AFTER truncateOutput (not before, as this used to
	// do) so a large output can never truncate the AUTHORITATIVE suffix away
	// while leaving an earlier, worker-embedded fake suffix as the text's
	// last occurrence.
	exitCode := res.ExitCode
	output = truncateOutput(output, res.ExitCode)
	if res.ExitCode != 0 {
		output += fmt.Sprintf("\n\n[Command exited with code %d]", res.ExitCode)
		if res.ExitCode == -1 {
			output += " (killed by signal)"
		}
	}

	return &ToolResult{
		ForLLM:   output,
		ForUser:  output,
		IsError:  res.ExitCode != 0,
		ExitCode: &exitCode,
	}
}

// scrubbedEnv returns a copy of base with sensitive Omnipus env vars removed.
// Used only on the god-mode path (runUnconstrained / background godMode
// spawn), where sandbox.ScrubGatewayEnv's stricter allowlist is intentionally
// NOT applied (god mode preserves the operator's full environment) but the
// gateway's own credential material must still never leak to a child.
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

// runUnconstrained runs the command without ApplyChildHardening/sandbox.Run.
// Used only under the global god-mode override. The command still runs in
// the resolved cwd and honors the caller-supplied timeout via context
// cancellation (exec.CommandContext); the parent environment is inherited
// with sensitive Omnipus credentials stripped.
func (t *ExecTool) runUnconstrained(
	ctx context.Context,
	argv []string,
	cwdPath string,
	timeoutSeconds int32,
) *ToolResult {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	if cwdPath != "" {
		cmd.Dir = cwdPath
	}
	cmd.Env = scrubbedEnv(os.Environ())
	if t.documentRuntime != nil {
		cmd.Env = documentruntime.ChildEnvironment(cmd.Env, *t.documentRuntime)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)

	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)

	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else if !timedOut {
			return ErrorResult(fmt.Sprintf("command failed: %v", runErr))
		} else {
			exitCode = -1
		}
	}

	output := stdoutBuf.String()
	if stderrBuf.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += "STDERR:\n" + stderrBuf.String()
	}

	if timedOut {
		msg := fmt.Sprintf("Command timed out after %d seconds", timeoutSeconds)
		if output != "" {
			msg += fmt.Sprintf("\n\nPartial output before timeout (ran %s):\n%s", dur.Round(time.Millisecond), output)
		}
		return &ToolResult{
			ForLLM:   msg,
			ForUser:  msg,
			IsError:  true,
			Err:      errors.New("command timeout"),
			TimedOut: true,
		}
	}

	// review r2 HIGH-1: same fix as foregroundResultFromSandbox above — the
	// structured field is set first (truncation-immune, authoritative for
	// the judge), and the display suffix is appended AFTER truncation.
	realExitCode := exitCode
	output = truncateOutput(output, exitCode)
	if exitCode != 0 {
		output += fmt.Sprintf("\n\n[Command exited with code %d]", exitCode)
	}

	return &ToolResult{
		ForLLM:   output,
		ForUser:  output,
		IsError:  exitCode != 0,
		ExitCode: &realExitCode,
	}
}

// Foreground output caps, aligned to the ADR-066 D4 per-surface figures
// (FR-014, B-15) so a bash result never reaches the tool-result choke point
// already larger than the cap it will be held to. A successful command
// (exit 0) may return up to the builtin-success cap; a failed one is held
// to the builtin-failure cap. There is no per-tool opt-out.
const (
	maxForegroundSuccessOutputLen = config.DefaultBuiltinSuccessCap // 64,000 chars
	maxForegroundOutputLen        = config.DefaultBuiltinFailureCap // 10,000 chars (failure path)
)

func truncateOutput(output string, exitCode int) string {
	if output == "" {
		return "(no output)"
	}
	limit := maxForegroundOutputLen
	if exitCode == 0 {
		limit = maxForegroundSuccessOutputLen
	}
	// Rune-sliced (never splits a multi-byte UTF-8 codepoint mid-character,
	// unlike a raw output[:limit] byte slice) — see G-1 in the tool-catalog
	// review; same bug class already fixed in BuildCompressedManifest
	// (manifest.go). Deliberately NOT utils.Truncate here: that helper
	// reserves 3 of the caller's own limit chars for its own "..." marker,
	// which would cut the body short of the exact limit AND double up with
	// this function's own, more informative "(truncated, N more chars)"
	// suffix below.
	runes := []rune(output)
	runeCount := len(runes)
	if runeCount > limit {
		return string(runes[:limit]) + fmt.Sprintf(
			"\n... (truncated, %d more chars)",
			runeCount-limit,
		)
	}
	return output
}
