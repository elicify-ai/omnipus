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
	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// ExecToolDeps bundles the ADR-035/ADR-036/ADR-092 dependencies for the bash
// tool. All fields are optional — nil ShellMode/ApprovalRequester disable
// the ADR-092 D1/D3/D7/D8 machinery and fail CLOSED to ShellModeAsk (see
// shell_permission_mode.go's resolveShellMode) rather than silently running
// unconfined.
//
// Note: the interactive approval layer (SEC-08, "ask" prompts) and the
// allow/ask/deny TOOL POLICY gate are handled upstream of this tool entirely
// (HookManager.ApproveTool / the compositor's EffectiveToolPolicy resolution)
// — a "deny" verdict means Execute is never called at all, so bash does not
// re-implement that check. What DOES live here is the surviving structural
// guards and — ADR-092 — the D1 mode-sensitive escalation machinery that
// applies WITHIN an "allow" (Auto mode) or "ask" (Ask mode) ceiling, which
// the upstream allow/ask/deny gate alone cannot express.
type ExecToolDeps struct {
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

	// ShellMode resolves the ADR-092 D1 effective mode (Ask/Auto/God)
	// governing each call. Wired by pkg/agent to ShellPermissionGate
	// (loop_policy.go), whose liveMode resolves: God Mode active -> God;
	// bash's tool policy resolving to anything but "ask" -> Ask (an "allow"
	// tool policy runs without the Auto machinery, a "deny" never reaches
	// execution); "ask" with Auto-approve off, or Auto-approve on but no
	// active kernel sandbox, -> Ask; "ask" + Auto-approve on + a kernel
	// sandbox installed -> Auto. Auto is therefore not an independent
	// third mode an operator picks directly — it is a narrower behavior
	// that only ever applies when bash's own tool policy has already
	// resolved to "ask".
	ShellMode ShellModeResolver

	// ApprovalRequester is the interactive escalation fallback for the D3
	// ask-rule and D7/D8 pre-flight call sites (FR-039). Wired by pkg/agent
	// to AgentLoop.CheckGrantOrRequestApproval — the SAME consultation
	// function the classic ask-policy path already uses.
	ApprovalRequester ShellApprovalRequester

	// ApprovalGrants is the session-scoped grant store (pkg/security).
	// ADR-092's D7/D8 escalation flows consult and record directly against
	// this reference (prefix/path-widening/network-widening — none of which
	// fit the classic exact-fingerprint IsAllowed check ApprovalRequester's
	// own fallback still performs for its own, narrower purpose). Wired by
	// pkg/agent to the SAME store instance AgentLoop.ApprovalGrants()
	// returns, so a grant recorded here is visible to (and inherited by)
	// every other consumer of that store.
	ApprovalGrants *security.ApprovalGrantStore

	// CommandRules is the ADR-092 D3 operator rule set (config.SandboxConfig.
	// CommandRules, json command_rules, config-file-only, no wire schema —
	// FR-018), evaluated in every mode. Empty/nil means no operator rules
	// are configured — D3 then defers entirely to the ceiling/mode
	// machinery, which is default-permissive by design.
	CommandRules []shellrule.Rule
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

	sessionManager *SessionManager

	// ADR-092 D1/D3/D7/D8: mode resolution, interactive escalation, the
	// session grant store, and the operator command-rule set. See
	// ExecToolDeps' own doc comments; all four are nil-safe to leave unwired
	// (shell_permission_mode.go's resolveShellMode/enforce* functions fail
	// closed rather than panic).
	shellMode         ShellModeResolver
	approvalRequester ShellApprovalRequester
	approvalGrants    *security.ApprovalGrantStore
	commandRules      []shellrule.Rule

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
}

// SetDocumentRuntime makes the versioned document toolchain reachable from
// this bash tool. ADR-090 environment-setup: every native agent gets the
// same read+execute view of the shared prefix; writable state (the cache)
// is resolved per turn INSIDE the turn's authorized root, and there is no
// Admin write grant — installation goes through environment_setup, not a
// bash finalizer (ES-FR-03/04).
func (t *ExecTool) SetDocumentRuntime(layout documentruntime.Layout) {
	layoutCopy := layout
	t.documentRuntime = &layoutCopy
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
	tool.godMode = deps.GodMode
	tool.proxy = deps.Proxy
	tool.auditFailClosed = deps.AuditFailClosed
	tool.shellMode = deps.ShellMode
	tool.approvalRequester = deps.ApprovalRequester
	tool.approvalGrants = deps.ApprovalGrants
	tool.commandRules = deps.CommandRules
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
		"need all of it. Commands are screened by a safety guard (deny patterns and a path-use check) before " +
		"they run — writing outside your workspace requires a mount first (see list_mounts / request_mount); " +
		"a \"blocked by safety guard\" error means the guard refused the command, not that it failed to run. " +
		"Document runtime provisioning goes through the environment_setup tool, not a bash command."
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

	// The document probe is an immutable first-party command whose manifest
	// lives outside the agent work directory by design. Its filesystem access
	// is still confined by the per-turn kernel policy augmented below, and it
	// is exempt from the ADR-092 D1/D3/D7/D8 machinery for the same reason it
	// is exempt from guardCommand below: it is a fixed, non-attacker-
	// influenced, system-triggered command, not a candidate for an
	// escalation prompt.
	isDocumentProbe := t.documentRuntime != nil && command == strings.Join(documentruntime.ProbeArgv(*t.documentRuntime), " ")

	// ADR-092 D1/D3/D7/D8: resolve the effective shell mode, evaluate the
	// unified operator rule engine (every mode), and — Auto mode only — run
	// the filesystem/network pre-flights, escalating and recording any
	// newly-approved grant. This is the fix for the B-1 reachability defect:
	// grant consultation now runs from the real execution path, not only
	// from the classic "ask" tool-policy branch upstream of Execute.
	var perm *shellPermissionResult
	if !isDocumentProbe {
		var permErr *ToolResult
		perm, permErr = t.enforceShellPermissionMode(ctx, command)
		if permErr != nil {
			t.emitAudit(ctx, command, cwd, audit.DecisionDeny)
			return permErr
		}
	}

	// FR-B4 surviving guards (the structural substitution guard) + the
	// legacy command-text absolute-path scan, now grant-aware (ADR-092
	// FR-036): perm.grants() is nil for Ask/God Mode and for the document
	// probe, so guardCommand behaves exactly as it did before ADR-092 in
	// both of those cases (FR-050's Ask/God branches) — Auto mode's own
	// widenings were already resolved and recorded above, so this scan
	// passes cleanly for exactly what was approved.
	if guardErr := t.guardCommand(ctx, command, cwd, perm.grants()); guardErr != "" && !isDocumentProbe {
		t.emitAudit(ctx, command, cwd, audit.DecisionDeny)
		return ErrorResult(guardErr)
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

	// ADR-090 environment-setup: resolve the per-turn runtime layers ONCE per
	// run call — the generic prefixes (storage's RuntimeEnvPaths over the
	// turn's authorized root) and the optional managed document runtime
	// (readiness + per-turn cache) — then derive the kernel policy and the
	// child environment from the SAME resolved layers, preserving each spawn
	// path's baseline (god: scrubbed environ; background sandbox:
	// sandboxLimitsEnv, whose proxy/npm injections must survive because that
	// path bypasses sandbox.Run's merge; foreground sandbox: nil = inherit,
	// preserved when nothing composes). Everything follows the turn's
	// authorized root, never a construction-time identity (ES-FR-04).
	docLayer := t.documentEnvTurn(baseDir)
	rtLayer, rtErr := runtimeEnvTurn(baseDir)
	// Truthful readiness reporting: a broken optional layer never blocks the
	// command, but it is never silent either (coordinator ruling). The doc
	// notice covers the managed document runtime; the runtime notice covers
	// unresolved or corrupt generic runtime state — where silent fall-through
	// to the host PATH could run a different program of the same name.
	turnNotice := docLayer.notice
	if n := runtimeEnvNotice(rtLayer, rtErr); n != "" {
		if turnNotice != "" {
			turnNotice += "\n\n"
		}
		turnNotice += n
	}

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
		kernelPolicy, kpErr := t.turnKernelPolicy(ctx, cwd, rtLayer, docLayer, perm)
		if kpErr != nil {
			t.emitAudit(ctx, command, cwd, audit.DecisionDeny)
			return ErrorResult(fmt.Sprintf("sandbox policy error: %v", kpErr))
		}
		lim.KernelPolicy = kernelPolicy
	}

	var execEnv []string
	switch {
	case runInBackground && !t.godMode:
		execEnv = composeExecutionEnv(sandboxLimitsEnv(lim), docLayer, rtLayer)
	case runInBackground && t.godMode:
		execEnv = composeExecutionEnv(scrubbedEnv(os.Environ()), docLayer, rtLayer)
	case !t.godMode:
		// sandbox.Run re-appends the proxy/npm injections after whatever env
		// is passed, so nil stays nil when nothing has to be composed.
		execEnv = composeExecutionEnv(nil, docLayer, rtLayer)
	default:
		execEnv = composeExecutionEnv(scrubbedEnv(os.Environ()), docLayer, rtLayer)
	}

	if runInBackground {
		ownerSessionID := ToolTranscriptSessionID(ctx)
		startedResult := t.runBackground(ctx, command, cwd, baseDir, timeoutSeconds, lim, execEnv, ownerSessionID, cb)
		// The background START result carries the same truthful notice as the
		// foreground path: a broken optional runtime never blocks the command,
		// but it is never silent either.
		return appendRuntimeNotice(startedResult, turnNotice)
	}
	started := time.Now()
	result := t.runForeground(ctx, command, lim, timeoutSeconds, execEnv)
	result = t.sweepAfterRun(ctx, command, cwd, baseDir, started, result)
	return appendRuntimeNotice(result, turnNotice)
}

// appendRuntimeNotice attaches the per-turn readiness notice to a run result.
// The command still ran; the notice reports the broken wired runtime honestly
// so the agent can repair it via environment_setup instead of misreading a
// missing managed toolchain as "bash is broken".
func appendRuntimeNotice(result *ToolResult, notice string) *ToolResult {
	if result == nil || notice == "" {
		return result
	}
	result.ForLLM = result.ContentForLLM() + "\n\n" + notice
	if result.ForUser != "" {
		result.ForUser += "\n\n" + notice
	}
	return result
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
// may this turn touch" from one input rather than two. rt is the generic
// runtime layer executeRun resolved ONCE this turn (zero on resolution
// failure) — the same snapshot that composed the child env, so env and
// kernel grants cannot drift (runtime-final-review R2).
//
// Returns (nil, nil) when no kernel policy is in force — sandbox off, or a
// platform that degraded to application-level enforcement. The spawn then uses
// whatever the boot profile is, exactly as before.
//
// Returns an error, rather than a nil policy, when a policy IS in force but
// could not be derived. Falling back on failure would hand the child the boot
// profile, which is the WIDER of the two — a derivation bug would then quietly
// restore the very cross-agent reach this exists to remove.
// perm is ADR-092's resolved permission state for this call (nil for the
// document probe, which bypasses the whole D1/D3/D7/D8 machinery — see
// executeRun). When perm is non-nil and its mode is Auto, the returned
// policy's PathGrants render the D7 widenings already resolved this call
// (via ResolveTurnFSPolicy's grant-overlay parameter) and its network
// posture is set by applyAutoNetworkPosture (D8) — deny-by-default unless
// perm.networkGranted.
func (t *ExecTool) turnKernelPolicy(ctx context.Context, cwd string, rt environmentsetup.RuntimeEnv, doc documentEnvLayer, perm *shellPermissionResult) (*sandbox.SandboxPolicy, error) {
	if !sandbox.TurnPolicyBaseInstalled() {
		return nil, nil
	}
	// restrict is t.restrictToWorkspace, not the hardcoded true that cwd
	// resolution uses: cwd confinement is a separate, deliberately stricter
	// decision (see resolveCWD's note 3), while this is the turn's real posture.
	// Scope does not change the derived rules anyway — post-ADR-062 it governs
	// writes through the work dir, which is identical either way (FR-2.5).
	authored, err := ResolveTurnFSPolicy(ctx, t.workingDir, t.restrictToWorkspace, perm.grants())
	if err != nil {
		return nil, fmt.Errorf("resolve turn filesystem policy: %w", err)
	}
	policy, err := sandbox.KernelPolicyForTurn(authored)
	if err != nil || policy == nil {
		return policy, err
	}
	augmented, err := t.augmentKernelPolicy(*policy, cwd, rt, doc)
	if err != nil {
		return nil, fmt.Errorf("apply runtime sandbox access: %w", err)
	}
	if perm != nil && perm.mode == ShellModeAuto {
		applyAutoNetworkPosture(&augmented, perm.networkGranted)
	}
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
	execEnv []string,
) *ToolResult {
	argv := buildShellArgv(command)

	if t.godMode {
		return t.runUnconstrained(ctx, argv, lim.WorkspaceDir, timeoutSeconds, execEnv)
	}

	res, err := sandbox.Run(ctx, argv, execEnv, lim)
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
	execEnv []string,
) *ToolResult {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	if cwdPath != "" {
		cmd.Dir = cwdPath
	}
	cmd.Env = execEnv

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
