// environment_setup.go — the ADR-090 environment setup tool, GENERIC
// command/script installation (founder decision "GENERIC INSTALL OPTION A",
// 2026-09-18; docs/internal/specs/adr-090-environment-setup-spec.md
// ES-FR-01..04). Supersedes the rejected structured-dependency/Engine design.
//
// The approval IS the existing Ask policy (ES-FR-01): the loop's approval
// path decides everything before Execute, and the approval display shows the
// ACTUAL agent-supplied command. There is no approval logic here and no
// bash-policy borrowing: environment_setup's policy entry is its own, and the
// binary allowlist / deny-pattern guards are bash-tool layers that do not
// apply (the sandbox boundary below is the confinement, per ES-FR-03).
//
// An approved install is a REAL subprocess started as a Bash-style background
// session (ES-FR-02): the same shared SessionManager, the same ownership
// (GetOwned on the caller's transcript session), the same terminal relabels
// (killed/timeout/canceled), and the same async completion notification as
// bash — zero new jobs machinery (the previous worker's process-less
// managed-job session extension was removed with the Engine design it served).
//
// Authority (ES-FR-02): the destination is always derived server-side — own
// current workspace context by default; an explicit `target_workspace` needs
// strict ID validation plus either an own-workspace match or wiring-time
// Admin authority over an EXISTING workspace record. A host path from the
// request is never accepted.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// EnvironmentSetupStore is the tool's seam over installation-area storage
// (pkg/environmentsetup in production, adapted by the agent wiring). The tool
// itself stays free of storage-package knowledge; tests supply a fake.
type EnvironmentSetupStore interface {
	// BeginInstall allocates the installation destination. appDataRoot is the
	// Omnipus data root (shared scope), workspaceRoot the tool-authorized
	// workspace (workspace scope); scope arrives schema-validated
	// ("workspace"/"shared" — the same string values storage's Scope uses).
	BeginInstall(appDataRoot, workspaceRoot, scope string) (EnvironmentSetupTarget, error)
}

// EnvironmentSetupTarget is one allocated installation destination behind the
// seam. Commit publishes a successful shared-scope install (generation ID and
// published directory); Abort releases a failed/canceled one.
type EnvironmentSetupTarget interface {
	// Scope reports "workspace" or "shared".
	Scope() string
	// Prefix is the script-visible installation prefix.
	Prefix() string
	// Cache and Tmp are the operation's writable cache/temp directories.
	Cache() string
	Tmp() string
	// WritableRoots is the EXACT write grant for the setup child — the
	// storage contract is {Prefix, Cache, Tmp}, never enclosing directories.
	WritableRoots() []string
	// Commit publishes a shared-scope install: (generationID, publishedDir, err).
	// Workspace scope returns an error (publication is a shared-scope concept).
	Commit() (string, string, error)
	// Abort releases the target (shared: removes the unpublished generation).
	Abort() error
}

// EnvironmentSetupToolDeps carries the wiring-time facts the tool needs.
// Admin and GodMode are WIRING-TIME identity/config facts — never per-call
// arguments ("a supplied workspace ID is not authority", ES-FR-02). Admin
// mirrors the server-derived role of the agent this instance is built for;
// GodMode mirrors ExecToolDeps.GodMode (the same operator opt-out that
// governs that agent's bash children).
type EnvironmentSetupToolDeps struct {
	// Home is OMNIPUS_HOME; cross-workspace targets resolve workspace
	// records under it, and the shared store resolves under it (data root).
	Home string

	// AgentWorkDir is this agent's fixed work root — the default destination
	// when the turn carries no workspace re-rooting and no explicit target.
	AgentWorkDir string

	// Admin marks this tool instance as holding cross-workspace setup
	// authority (server-derived). Non-Admin callers may install into their
	// own current workspace context only.
	Admin bool

	// GodMode skips child hardening exactly as it does for this agent's bash
	// children (resolved once at wiring time; never per-call).
	GodMode bool

	// Proxy is the process-wide egress proxy (SSRF protection) passed to the
	// sandbox limits, mirroring ExecToolDeps.Proxy. May be nil.
	Proxy *sandbox.EgressProxy

	// Store is the installation-storage seam (pkg/environmentsetup adapted by
	// the agent wiring in production). Nil means installation is unavailable
	// and every run request is refused.
	Store EnvironmentSetupStore

	// AuditFailClosed mirrors bash FR-B7: when true, a failed audit write on
	// the install-start path aborts the install.
	AuditFailClosed bool
}

// EnvironmentSetupTool implements the `environment_setup` builtin tool.
type EnvironmentSetupTool struct {
	BaseTool

	home         string
	agentWorkDir string
	admin        bool
	godMode      bool
	proxy        *sandbox.EgressProxy

	store EnvironmentSetupStore

	sessionManager *SessionManager

	auditLogger     *audit.Logger
	auditFailClosed bool

	// killAuditFn is the kill-failure shared-session kill-failure audit
	// callback, built once at NewEnvironmentSetupTool time (mirrors
	// ExecTool.killAuditFn).
	killAuditFn func(pid int, killErr error, caller string)
}

// NewEnvironmentSetupTool constructs the tool. The audit logger is injected
// later by the registry (auditLoggerAware), mirroring every sibling tool.
func NewEnvironmentSetupTool(deps EnvironmentSetupToolDeps) *EnvironmentSetupTool {
	return &EnvironmentSetupTool{
		home:            deps.Home,
		agentWorkDir:    deps.AgentWorkDir,
		admin:           deps.Admin,
		godMode:         deps.GodMode,
		proxy:           deps.Proxy,
		store:           deps.Store,
		sessionManager:  getSessionManager(),
		auditFailClosed: deps.AuditFailClosed,
	}
}

// SetAuditLogger satisfies the registry's auditLoggerAware contract.
func (t *EnvironmentSetupTool) SetAuditLogger(l *audit.Logger) {
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

func (t *EnvironmentSetupTool) Name() string { return "environment_setup" }

func (t *EnvironmentSetupTool) Scope() ToolScope { return ScopeGeneral }

func (t *EnvironmentSetupTool) Category() ToolCategory { return CategoryPlatform }

// Description states the generic-input contract and the authority rules.
func (t *EnvironmentSetupTool) Description() string {
	return "Install dependencies by running an agent-supplied installation command or inline " +
		"script as a background session, confined to a selected installation area (no " +
		"application package catalogue: the command is the dependency knowledge). The approval " +
		"displays the actual command. action=run (default) starts the session and returns " +
		"session_id, the resolved installation prefix (also exported to the script as " +
		"OMNIPUS_ENV_PREFIX, with writable cache/tmp as OMNIPUS_ENV_CACHE/OMNIPUS_ENV_TMP), the " +
		"scope and the target workspace; action=poll/read/kill with that session_id check status, " +
		"read bounded output, or cancel. scope defaults to workspace (<workspace>/.omnipus/env); " +
		"scope=shared requests the application-managed shared runtime area (explicit in the " +
		"approval, published read/execute-only on exit 0, unpublished on failure). target_workspace " +
		"names an authorized workspace; only Admin can target a workspace it does not belong to. " +
		"Exit 0 reports command success only — verify the installed capability in your own " +
		"runtime before resuming."
}

// --- Schema and limits -------------------------------------------------------

const (
	environmentSetupMaxPurpose = 500

	// ES-FR02: the command is capped at 65536 UTF-8 bytes with NO silent
	// truncation — oversized input is refused, never clipped.
	environmentSetupMaxCommandBytes = 65536

	environmentSetupDefaultTimeoutSeconds = int32(1800)

	environmentSetupScopeWorkspace = "workspace"
	environmentSetupScopeShared    = "shared"

	// Documented generic env-var names the setup child receives (the storage
	// package owns the canonical constants — EnvVarPrefix/EnvVarCache/
	// EnvVarTmp in generic-storage-interface.md; keep values in lockstep
	// until the direct import replaces these literals).
	environmentSetupEnvVarPrefix = "OMNIPUS_ENV_PREFIX"
	environmentSetupEnvVarCache  = "OMNIPUS_ENV_CACHE"
	environmentSetupEnvVarTmp    = "OMNIPUS_ENV_TMP"
)

// environmentSetupWorkspaceIDPattern is the strict shape for an explicit
// target_workspace: no slashes, dots, whitespace or shell metacharacters —
// the value can never carry a host-path shape.
var environmentSetupWorkspaceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// environmentSetupResolveTimeout bounds the session timeout. Same [1,3600]
// wall-clock bounds as bash (shared constants), an installation-sized default
// (30 minutes).
func environmentSetupResolveTimeout(args map[string]any) (int32, error) {
	raw, ok := args["timeout_seconds"]
	if !ok || raw == nil {
		return environmentSetupDefaultTimeoutSeconds, nil
	}
	var v int64
	switch n := raw.(type) {
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("timeout_seconds must be an integer number of seconds")
		}
		v = int64(n)
	case int:
		v = int64(n)
	case int32:
		v = int64(n)
	case int64:
		v = n
	case json.Number:
		var pErr error
		v, pErr = n.Int64()
		if pErr != nil {
			return 0, fmt.Errorf("timeout_seconds must be a number")
		}
	default:
		return 0, fmt.Errorf("timeout_seconds must be a number")
	}
	if v < int64(minTimeoutSeconds) || v > int64(maxTimeoutSeconds) {
		return 0, fmt.Errorf("timeout_seconds must be between %d and %d (got %d)", minTimeoutSeconds, maxTimeoutSeconds, v)
	}
	return int32(v), nil
}

// --- Target resolution (ES-FR-02/ES-BDD-03/ES-BDD-10) ------------------------

// environmentSetupTarget is the authorized destination for one install.
type environmentSetupTarget struct {
	ID   string // workspace id, "" when the turn has no workspace context
	Root string // absolute directory derived server-side, never from the request
}

// resolveTarget implements the authority model: own context by default; an
// explicit target needs ID validation plus either an own-workspace match or
// wiring-time Admin authority over an EXISTING workspace record. The returned
// Root is always derived under $OMNIPUS_HOME — a host path from the request
// is structurally impossible.
func (t *EnvironmentSetupTool) resolveTarget(ctx context.Context, requested string) (environmentSetupTarget, error) {
	requested = strings.TrimSpace(requested)
	ownDir := t.agentWorkDir
	if d := TurnWorkspaceDir(ctx); d != "" {
		ownDir = d
	}
	ownID := ToolWorkspaceID(ctx)

	if requested == "" {
		return environmentSetupTarget{ID: ownID, Root: ownDir}, nil
	}

	if !environmentSetupWorkspaceIDPattern.MatchString(requested) {
		return environmentSetupTarget{}, fmt.Errorf(
			"target_workspace %q is not a valid workspace identifier (no paths or separators)", requested)
	}

	// Own-workspace match: the caller's CURRENT turn workspace. The turn
	// context is the authority here — the supplied ID must EQUAL it, not
	// merely exist.
	if requested == ownID && ownDir != "" {
		return environmentSetupTarget{ID: ownID, Root: ownDir}, nil
	}

	if !t.admin {
		return environmentSetupTarget{}, fmt.Errorf(
			"target_workspace %q is outside your authorized workspace; only Admin can target another workspace", requested)
	}

	// Admin cross-workspace authority WITHOUT membership (ES-BDD-10): the
	// workspace RECORD must exist under $OMNIPUS_HOME — a nonexistent name
	// is refused even for Admin (no host-path probing via workspaces/).
	record := filepath.Join(t.home, "workspaces", requested+".json")
	if _, err := os.Stat(record); err != nil {
		return environmentSetupTarget{}, fmt.Errorf(
			"target_workspace %q does not exist", requested)
	}
	// The root is the workspace's WORK dir (workspaces/<id>/work/) — where
	// member turns' tools are actually re-rooted — never the record root
	// (workspaces/<id>/, which holds AGENT.md and the shared memory room and
	// is read by no member turn). Otherwise an Admin install lands where the
	// target team can never see it (security review MAJ-6).
	//
	// Admin first-use initialization: a freshly created workspace record has
	// no work/ dir until a member turn or mount creates one
	// (WorkspaceCreateTool writes only the record; mount.go ensures only on
	// mount creation), so ensure it here with the sanctioned facility —
	// validated id, derived under $OMNIPUS_HOME, no arbitrary host path —
	// before storage opens its confined root (storage requires the
	// authorized root to exist). Member turns need nothing here: the loop's
	// re-rooting ensures the dir every turn.
	if _, err := workspace.EnsureWorkDir(t.home, requested); err != nil {
		return environmentSetupTarget{}, fmt.Errorf("target_workspace %q work directory could not be prepared: %w", requested, err)
	}
	dir, err := workspace.SafeWorkDir(t.home, requested)
	if err != nil {
		// SafeWorkDir cannot fail for an ID this strict pattern already
		// accepted; treated defensively as a refusal, never as a bypass.
		return environmentSetupTarget{}, fmt.Errorf("target_workspace %q is not a safe workspace identifier", requested)
	}
	return environmentSetupTarget{ID: requested, Root: dir}, nil
}

// --- Execute dispatch --------------------------------------------------------

var _ AsyncExecutor = (*EnvironmentSetupTool)(nil)

// Execute dispatches by action. Approval has already happened upstream (the
// Ask policy); nothing here re-checks permissions (ES-FR-01).
func (t *EnvironmentSetupTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, nil)
}

// ExecuteAsync implements AsyncExecutor: the callback fires exactly once when
// a started session reaches a terminal state (done/failed/killed/timeout/
// canceled), mirroring bash's FR-B9 notification contract.
func (t *EnvironmentSetupTool) ExecuteAsync(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	return t.execute(ctx, args, cb)
}

// execute dispatches by action. A present-but-non-string action is refused
// rather than silently treated as the run default.
func (t *EnvironmentSetupTool) execute(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	if raw, present := args["action"]; present && raw != nil {
		if _, isStr := raw.(string); !isStr {
			return t.emitDeny(ctx, args, ErrorResult("action must be a string"))
		}
	}
	action, _ := args["action"].(string)
	if action == "" {
		action = "run"
	}
	switch action {
	case "run":
		return t.startRun(ctx, args, cb)
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

// startRun validates the request, resolves the authorized destination and the
// installation area (BeginInstall), audits, and starts the real background
// session. Starting is not installation success (ES-FR-02/04): exit status,
// output, paths and publication outcome surface later through poll/read and
// the async completion.
func (t *EnvironmentSetupTool) startRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return t.emitDeny(ctx, args, ErrorResult("command is required and must be a non-empty string (multiline scripts allowed)"))
	}
	command = strings.TrimSpace(command)
	if len(command) > environmentSetupMaxCommandBytes {
		return t.emitDeny(ctx, args, ErrorResult(fmt.Sprintf("command must be at most %d bytes", environmentSetupMaxCommandBytes)))
	}

	purpose, ok := args["purpose"].(string)
	if !ok || strings.TrimSpace(purpose) == "" {
		return t.emitDeny(ctx, args, ErrorResult("purpose is required and must be a string"))
	}
	purpose = strings.TrimSpace(purpose)
	if utf8.RuneCountInString(purpose) > environmentSetupMaxPurpose {
		return t.emitDeny(ctx, args, ErrorResult(fmt.Sprintf("purpose must be at most %d characters", environmentSetupMaxPurpose)))
	}

	timeoutSeconds, err := environmentSetupResolveTimeout(args)
	if err != nil {
		return t.emitDeny(ctx, args, ErrorResult(err.Error()))
	}

	scope, ok := args["scope"].(string)
	if !ok {
		if args["scope"] == nil {
			scope = environmentSetupScopeWorkspace
		} else {
			return t.emitDeny(ctx, args, ErrorResult("scope must be a string"))
		}
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = environmentSetupScopeWorkspace
	}
	if scope != environmentSetupScopeWorkspace && scope != environmentSetupScopeShared {
		return t.emitDeny(ctx, args, ErrorResult(fmt.Sprintf("scope must be %q or %q", environmentSetupScopeWorkspace, environmentSetupScopeShared)))
	}
	targetRaw, ok := args["target_workspace"].(string)
	if !ok && args["target_workspace"] != nil {
		return t.emitDeny(ctx, args, ErrorResult("target_workspace must be a string"))
	}
	if scope == environmentSetupScopeShared && strings.TrimSpace(targetRaw) != "" {
		return t.emitDeny(ctx, args, ErrorResult("target_workspace selects a workspace-scope destination; use scope=workspace with it"))
	}

	if t.store == nil {
		return t.emitDeny(ctx, args, ErrorResult("environment setup storage is not wired; installation is unavailable in this build"))
	}
	target, err := t.resolveTarget(ctx, targetRaw)
	if err != nil {
		return t.emitDisallowedTarget(ctx, args, targetRaw, err)
	}

	// BeginInstall creates/allocates the area. From here the target MUST be
	// terminated exactly once: synchronous failure paths below Abort it and
	// return; once the session starts, ownership passes to the completion
	// goroutine (Commit on exit 0 / Abort otherwise for shared scope).
	area, err := t.store.BeginInstall(t.home, target.Root, scope)
	if err != nil {
		return t.emitDeny(ctx, args, ErrorResult(fmt.Sprintf("installation area could not be created: %v", err)))
	}

	prefix := area.Prefix()
	// Resolve the runtime view once. The same snapshot supplies both PATH and
	// the child policy, so a just-published generation cannot appear in one
	// and be absent from the other.
	runtimeEnv, err := environmentsetup.RuntimeEnvPaths(t.home, target.Root)
	if err != nil {
		return t.emitDeny(ctx, args, setupStartAbortResult(area, fmt.Sprintf("runtime environment error: %v", err)))
	}
	lim, err := t.buildSetupLimits(ctx, area, target.Root, runtimeEnv, timeoutSeconds)
	if err != nil {
		return t.emitDeny(ctx, args, setupStartAbortResult(area, fmt.Sprintf("sandbox limits error: %v", err)))
	}

	// FR-B7-style audit before spawn; fail-closed when configured.
	if auditResult := t.emitRunAudit(ctx, command, purpose, scope, target, prefix); auditResult != nil {
		return t.emitDeny(ctx, args, setupStartAbortResult(area, auditResult.ForLLM))
	}

	env := t.setupEnv(lim, area, runtimeEnv)
	return t.startSession(ctx, startPlan{
		command:        command,
		purpose:        purpose,
		scope:          scope,
		target:         target,
		area:           area,
		cwd:            prefix,
		timeoutSeconds: timeoutSeconds,
		lim:            lim,
		env:            env,
		ownerSessionID: ToolTranscriptSessionID(ctx),
		agentID:        ToolAgentID(ctx),
		cb:             cb,
	})
}

// emitDeny writes a deny-decision audit entry for a refused run request and
// returns the error result. Nil logger is a no-op (mirrors bash's emitAudit).
func (t *EnvironmentSetupTool) emitDeny(ctx context.Context, args map[string]any, result *ToolResult) *ToolResult {
	if t.auditLogger != nil {
		cmd, _ := args["command"].(string)
		entry := &audit.Entry{
			Event:    audit.EventExec,
			Decision: audit.DecisionDeny,
			AgentID:  ToolAgentID(ctx),
			Tool:     t.Name(),
			Command:  cmd,
			Details: map[string]any{
				"reason": result.ForLLM,
			},
		}
		if err := t.auditLogger.Log(entry); err != nil {
			slog.Warn("environment_setup: audit write failed", "agent_id", ToolAgentID(ctx), "error", err)
		}
	}
	return result
}

// emitDisallowedTarget is emitDeny with the target recorded, so the audit
// trail shows WHO tried to reach WHICH workspace (the cross-workspace probe
// signal ES-BDD-03 is about).
func (t *EnvironmentSetupTool) emitDisallowedTarget(ctx context.Context, args map[string]any, requested string, err error) *ToolResult {
	if t.auditLogger != nil {
		cmd, _ := args["command"].(string)
		entry := &audit.Entry{
			Event:    audit.EventExec,
			Decision: audit.DecisionDeny,
			AgentID:  ToolAgentID(ctx),
			Tool:     t.Name(),
			Command:  cmd,
			Details: map[string]any{
				"reason":           err.Error(),
				"target_workspace": requested,
			},
		}
		if e := t.auditLogger.Log(entry); e != nil {
			slog.Warn("environment_setup: audit write failed", "agent_id", ToolAgentID(ctx), "error", e)
		}
	}
	return ErrorResult(err.Error())
}

// emitRunAudit writes the allow-decision entry before spawn; fail-closed when
// AuditFailClosed is set (FR-B7 shape, mirrors bash's emitAuditOrDeny).
func (t *EnvironmentSetupTool) emitRunAudit(ctx context.Context, command, purpose, scope string, target environmentSetupTarget, prefix string) *ToolResult {
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
			"purpose":          purpose,
			"scope":            scope,
			"target_workspace": target.ID,
			"prefix":           prefix,
			"god_mode":         t.godMode,
		},
	})
	if logErr == nil {
		return nil
	}
	if t.auditFailClosed {
		slog.Error("environment_setup: audit logger degraded; refusing to execute (audit_fail_closed=true)",
			"agent_id", agentID, "command", command, "error", logErr)
		return &ToolResult{
			IsError: true,
			ForLLM:  "audit log write failed; refusing to execute (audit_fail_closed=true)",
			ForUser: "environment_setup requires audit logging; aborting",
		}
	}
	slog.Warn("environment_setup: audit write failed", "agent_id", agentID, "error", logErr)
	return nil
}

func (t *EnvironmentSetupTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"run", "poll", "read", "kill"},
				"description": "run (default): start a background installation. poll/read/kill: manage a background session_id.",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "The installation command or multiline script to run (max 65536 bytes). Shown verbatim in the approval. Runs with cwd set to the installation prefix.",
			},
			"purpose": map[string]any{
				"type":        "string",
				"description": "Short reason for the installation, shown to the user for approval (max 500 chars).",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"workspace", "shared"},
				"description": "Installation scope; defaults to workspace (<workspace>/.omnipus/env). shared requests the application-managed shared runtime area.",
			},
			"target_workspace": map[string]any{
				"type":        "string",
				"description": "Authorized workspace to install into (only Admin can target another existing workspace).",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Background session id (required for action=poll/read/kill).",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Session wall-clock timeout; default 1800, bounds [1, 3600].",
			},
		},
	}
}
