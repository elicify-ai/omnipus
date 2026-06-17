// Omnipus - Ultra-lightweight personal AI agent
// Built on Omnipus's foundation. See CLAUDE.md for project lineage.
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/constants"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/media"
	"github.com/dapicom-ai/omnipus/pkg/policy"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/routing"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/security"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/skills"
	"github.com/dapicom-ai/omnipus/pkg/state"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
	"github.com/dapicom-ai/omnipus/pkg/tools"
	"github.com/dapicom-ai/omnipus/pkg/tools/browser"
	"github.com/dapicom-ai/omnipus/pkg/utils"
	"github.com/dapicom-ai/omnipus/pkg/voice"
)

type AgentLoop struct {
	// Core dependencies
	bus      *bus.MessageBus
	cfg      *config.Config
	registry *AgentRegistry
	state    *state.Manager

	// Event system
	eventBus *EventBus
	hooks    *HookManager

	// Runtime state
	running     atomic.Bool
	summarizing sync.Map
	fallback    *providers.FallbackChain
	transcriber voice.Transcriber

	// channelManager and mediaStore are replaced on every restartServices
	// (config-watch goroutine). They are read on the hot turn path and from
	// voice/transcription helpers. These dedicated mutexes prevent data races
	// between the writer goroutine and the reader goroutines. Always acquire
	// channelManagerMu/mediaStoreMu independently; never hold both at the
	// same time to avoid deadlock.
	channelManagerMu sync.RWMutex
	channelManager   *channels.Manager
	mediaStoreMu     sync.RWMutex
	mediaStore       media.MediaStore
	cmdRegistry      *commands.Registry
	mcp              mcpRuntime
	hookRuntime      hookRuntime
	steering         *steeringQueue
	pendingSkills    sync.Map
	mu               sync.RWMutex

	// Concurrent turn management
	activeTurnStates   sync.Map     // key: sessionKey (string), value: *turnState
	subTurnCounter     atomic.Int64 // Counter for generating unique SubTurn IDs
	sessionActiveAgent sync.Map     // key: "session:"+sessionID (string), value: agentID (string); set by handoff, cleared on agent deletion

	// Turn tracking
	turnSeq        atomic.Uint64
	activeRequests sync.WaitGroup

	// mediaRefsDropped counts media refs that could not be resolved (unknown ref
	// or file missing on disk). Observable via GetMediaRefsDropped for tests and
	// diagnostics; incremented atomically from resolveMediaRefs hot path.
	mediaRefsDropped atomic.Int64

	reloadFunc    func() error
	reloadPending atomic.Bool // set by TriggerReload; cleared by ClearReloadPending (called from gateway executeReload)

	// Task management
	taskStore    *taskstore.TaskStore
	taskExecutor *TaskExecutor

	// Security (SEC-15, SEC-17): audit logging and policy evaluation.
	// Initialized in NewAgentLoop when sandbox.audit_log is enabled.
	auditLogger   *audit.Logger
	policyAuditor *policy.PolicyAuditor

	// Kernel-level sandbox backend (SEC-01, SEC-02, SEC-03). Selected at startup
	// via sandbox.SelectBackend: LinuxBackend on Linux 5.13+ (Landlock+seccomp),
	// FallbackBackend elsewhere (cooperative env vars). Applied to every exec
	// child via ExecTool.sandboxBackend.
	sandboxBackend sandbox.SandboxBackend

	// Prompt injection defense (SEC-25). Sanitizes untrusted tool results —
	// web_search, web_fetch, browser_*, read_file — before they enter the
	// LLM's context. Nil when the guard is misconfigured; callers must
	// nil-check. Trusted tool results (exec, spawn, message, etc.) are NEVER
	// sanitized so the LLM sees verbatim user and internal output.
	promptGuard *security.PromptGuard

	// SSRF proxy for exec child processes (SEC-28). Only started when
	// cfg.Tools.Exec.EnableProxy is true. The proxy is idle-stop: it exits
	// after DefaultIdleTimeout (30s) when no commands are active, and is
	// automatically restarted by PrepareCmd() on the next exec command so
	// long-lived agent loops continue to enforce SSRF protection. On initial
	// bind failure this field is nil and exec children run without proxy env
	// vars (degraded mode — LIM-02).
	execProxy *security.ExecProxy

	// ssrfChecker is the singleton SSRFChecker built from cfg.Sandbox.SSRF at
	// startup (SEC-24). It is nil when SSRF protection is disabled. All outbound
	// HTTP tool surfaces (web_search, skills installer, browser, exec proxy)
	// receive this instance so the allow_internal policy is honored uniformly.
	ssrfChecker *security.SSRFChecker

	// Browser automation manager (US-4/US-6/US-7). Nil when browser tools
	// are disabled. Shutdown() is called in AgentLoop.Close().
	browserMgr *browser.BrowserManager

	// Tier 1/3 deps — stored so WireTier13Deps can re-run on hot reload.
	// Without this, hot-reload would drop web_serve, workspace.shell, and
	// workspace.shell_bg from every agent because ReloadProviderAndConfig
	// builds a fresh registry.
	tier13Deps *Tier13Deps

	// sandboxEgressProxy is the kernel-sandbox HTTP/HTTPS egress proxy that
	// is created once at gateway boot and shared by web_serve, the workspace
	// shell tools, and (when sandbox is on) the exec tool. Stashed here so
	// wireExecToolDeps can plumb it into ExecToolDeps.EgressProxy after
	// WireTier13Deps has handed us the singleton. Nil when sandbox.NewEgressProxy
	// failed at boot or sandbox is off — in either case the exec tool falls
	// back to running children without HTTP_PROXY env vars on the
	// hardened-exec path.
	sandboxEgressProxy *sandbox.EgressProxy

	// appliedSandboxMode is the mode actually applied by the kernel sandbox at
	// boot (from SandboxApplyResult.Mode), set via SetAppliedSandboxMode. This
	// is the authoritative source for ExecToolDeps.SandboxMode — using the boot
	// result rather than cfg.Sandbox.ResolvedMode() prevents the two from
	// disagreeing when the CLI --sandbox flag overrides the config file (MAJOR-1).
	// Zero value (empty string) maps to ModeOff in wireExecToolDepsOn.
	appliedSandboxMode sandbox.Mode

	// sysagentDeps holds the dependencies for system.* tool registration (FR-001, FR-002).
	// Wired by the gateway after boot via SetSysagentDeps. Nil until wired; if nil,
	// system tools are not registered (graceful degradation in tests without a store).
	sysagentDeps *systools.Deps

	// contextBuilderRegistry is a broadcast channel for config-change
	// invalidation (FR-061). REST config-write handlers call
	// InvalidateAllContextBuilders so every agent's next turn rebuilds the
	// env preamble with the new values.
	contextBuilderRegistry *ContextBuilderRegistry

	// Per-agent rate limiting and global daily cost cap (SEC-26).
	// rateLimiter manages sliding-window counters; costTracker persists the
	// daily cost accumulator across restarts. Both are always non-nil after
	// NewAgentLoop — the registry exists even when no limits are configured
	// so it can record costs for observability. The per-call sites check
	// cfg.Sandbox.RateLimits.* > 0 to decide whether to enforce.
	rateLimiter *security.RateLimiterRegistry
	costTracker *security.CostTracker

	// sharedSessionStore is the single UnifiedStore at $OMNIPUS_HOME/sessions/
	// used for all new sessions (joined session model). Legacy per-agent stores
	// remain accessible via GetAgentStore for read-only access to old sessions.
	sharedSessionStore *session.UnifiedStore

	// toolApprover is the gateway-injected implementation of the human-in-the-loop
	// approval gate (FR-011, FR-082). Nil until SetToolApprover is called; when nil,
	// ask-policy tools are treated as allow (open gate, no WS event).
	toolApprover PolicyApprover

	// allowGodMode is set when the gateway was started with --allow-god-mode.
	// When false, sandbox_profile=off is coerced to workspace at tool-wiring time.
	// Latch (2) — runtime coercion.
	allowGodMode bool

	// coercionLogged tracks which agent IDs have already emitted a coercion WARN
	// so the log fires at most once per process lifetime per agent (not on every
	// hot reload).
	coercionLogged sync.Map

	// Lane S: session-end recap pipeline (FR-023..FR-030).

	// idleTickers maps sessionID → context.CancelFunc for the idle timeout ticker.
	// Populated by RegisterIdleTicker; canceled by cancelIdleTicker.
	idleTickers sync.Map // sessionID (string) → context.CancelFunc

	// agentCurrentSession maps agentID → sessionID (atomic CAS).
	// Used by the lazy-CAS logic to detect session switches and trigger recap.
	agentCurrentSession sync.Map // agentID (string) → sessionID (string)

	// claimedCloseSessions is the idempotency gate for CloseSession (FR-027).
	// LoadOrStore ensures only one goroutine triggers recap per session.
	claimedCloseSessions sync.Map // sessionID (string) → true

	// Session-end recap drain (#265). Recap goroutines (CloseSession → runRecap)
	// write LAST_SESSION.md / retro files and emit audit entries in the
	// background. Close() drains them (recapWG.Wait) BEFORE it tears down the
	// registry, session stores, and audit logger they write through, so the
	// recaps both complete and finish writing before shutdown returns — leaving
	// them detached races the temp-dir cleanup in tests. recapMu+closing gate
	// scheduling so a recap can never be Added after the drain begins (no
	// WaitGroup Add-after-Wait). Recaps are not canceled; each self-bounds at
	// 60s and shares the 70s graceful-shutdown budget with the active-turn wait.
	recapMu sync.Mutex
	closing bool
	recapWG sync.WaitGroup

	// stopCancel is the CancelFunc created by Run to support Stop(). When
	// Stop() is called it cancels this func so the Run select wakes
	// immediately without waiting for the next message or ticker. Stored
	// atomically via the atomic.Pointer so Stop() can be called before Run.
	stopCancel atomic.Pointer[context.CancelFunc]

	// cancelAbuse is the shared abuse detector used by RequestCancel across all
	// four cancel entry points (web, Tier A /cancel, Tier B text-parsing, CLI).
	// Initialized in NewAgentLoop; always non-nil after construction.
	cancelAbuse *cancelAbuseDetector

	// sessionWorkers holds the active per-scope session workers (sync.Map).
	// Key: scope string (e.g. "agent:jim:session:abc123").
	// Value: *sessionWorker.
	// Workers self-remove after workerIdleTimeout; Close() cancels all remaining.
	sessionWorkers sync.Map

	// admission is the soft-cap gate for concurrent session workers.
	// Phase 1: gates inbound user-message dispatch only, per unique scope.
	// Resource-aware admission (CPU load, RSS, goroutine count) is out of
	// scope for v0.1 and filed as a follow-up.
	admission *AdmissionController

	// channelSessionIdx maps "channel/chatID" → shared session ID for fast per-peer
	// session resumption. Built on startup and updated on every new channel session.
	channelSessionIdx sync.Map
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey              string                // Session identifier for history/context
	Channel                 string                // Target channel for tool execution
	ChatID                  string                // Target chat ID for tool execution
	SenderID                string                // Current sender ID for dynamic context
	SenderDisplayName       string                // Current sender display name for dynamic context
	UserMessage             string                // User message content (may include prefix)
	ForcedSkills            []string              // Skills explicitly requested for this message
	SystemPromptOverride    string                // Override the default system prompt (Used by SubTurns)
	Media                   []string              // media:// refs from inbound message
	InitialSteeringMessages []providers.Message   // Steering messages from refactor/agent
	DefaultResponse         string                // Response when LLM returns empty
	EnableSummary           bool                  // Whether to trigger summarization
	SendResponse            bool                  // Whether to send response via bus
	SuppressToolFeedback    bool                  // Whether to suppress inline tool feedback messages
	NoHistory               bool                  // If true, don't load session history (for heartbeat)
	SkipInitialSteeringPoll bool                  // If true, skip the steering poll at loop start (used by Continue)
	TranscriptSessionID     string                // Session ID for transcript tool call recording (empty = disabled)
	TranscriptStore         *session.UnifiedStore // Store for transcript tool call recording (nil = disabled)

	// WorkspaceID is the Spec-1 Workspace identifier for this turn.
	// When set, the memory store uses the shared workspace room
	// ($OMNIPUS_HOME/workspaces/<id>/.omnipus/) for memories scoped to "shared".
	// Empty means no workspace is associated (private room only).
	WorkspaceID string

	// AutoDenyAsk, when true, makes every `ask`-policy tool call auto-DENIED
	// without ever requesting human approval (issue #264, FR-009). Scheduled
	// runs are headless — there is no operator to approve, so blocking on an
	// approval prompt would stall the run forever. Only ProcessScheduled sets
	// this; interactive paths leave it false so `ask` keeps prompting.
	AutoDenyAsk bool

	// Metadata carries the inbound message metadata (bus.InboundMessage.Metadata)
	// through to the turn flow. The agent loop reads Metadata["model_name"] to
	// detect a per-thread model switch (FR-011) and apply switch-time
	// compress before the next LLM call.
	Metadata map[string]string
}

// ScheduledJobInfo carries the schedule/job identity that ProcessScheduled
// callers inject into the run context via WithScheduledJobContext. The
// auto-deny path reads it so the emitted audit entry names the responsible
// schedule (F-13 / O-3 observability requirement, issue #342).
type ScheduledJobInfo struct {
	JobID   string
	JobName string
}

// scheduledJobContextKey is the unexported context key for ScheduledJobInfo.
type scheduledJobContextKey struct{}

// WithScheduledJobContext returns a child context carrying the schedule
// identity. Call this in the cron fire path (pkg/gateway/schedules.go
// RunScheduled) before calling ProcessScheduled so the auto-deny audit entry
// can include the job id and name.
func WithScheduledJobContext(ctx context.Context, jobID, jobName string) context.Context {
	return context.WithValue(ctx, scheduledJobContextKey{}, ScheduledJobInfo{
		JobID:   jobID,
		JobName: jobName,
	})
}

// scheduledJobContextFrom retrieves the ScheduledJobInfo from ctx, or returns
// a zero-value struct when no info was injected (interactive / non-scheduled
// runs). The boolean reports whether info was present.
func scheduledJobContextFrom(ctx context.Context) (ScheduledJobInfo, bool) {
	v, ok := ctx.Value(scheduledJobContextKey{}).(ScheduledJobInfo)
	return v, ok
}

type continuationTarget struct {
	SessionKey string
	Channel    string
	ChatID     string
}

const (
	defaultResponse           = "The model returned an empty response. This may indicate a provider error or token limit."
	toolLimitResponse         = "I've reached `max_tool_iterations` without a final response. Increase `max_tool_iterations` in config.json if this task needs more tool steps."
	sessionKeyAgentPrefix     = "agent:"
	metadataKeyAccountID      = "account_id"
	metadataKeyGuildID        = "guild_id"
	metadataKeyTeamID         = "team_id"
	metadataKeyInstanceID     = "instance_id"
	metadataKeyParentPeerKind = "parent_peer_kind"
	metadataKeyParentPeerID   = "parent_peer_id"
)

// ErrReloadNotConfigured is returned by TriggerReload when no reload function
// has been registered. This is normal in unit-test environments where the full
// gateway reload pipeline is not wired. Production always configures the reload
// function during startup, so callers outside tests should treat this as
// unexpected and log accordingly.
var ErrReloadNotConfigured = errors.New("reload not configured")

// ErrReloadAlreadyInProgress is returned by TriggerReload when a reload is
// already running. The caller should treat this as "poll anyway" — the in-flight
// reload will call ClearReloadPending when it completes, unblocking any poller.
var ErrReloadAlreadyInProgress = errors.New("reload already in progress")

// RecapModelBootError is returned by NewAgentLoop when AutoRecapEnabled is true
// and the resolved recap model is not in the cheap-model allow-list (FR-029a).
// Callers should map this to a non-zero exit code and log the message.
type RecapModelBootError struct {
	Model     string
	AllowList []string
}

func (e *RecapModelBootError) Error() string {
	return fmt.Sprintf(
		"config error: recap_model %q is not in the cheap-model allow-list %v; "+
			"set cfg.Routing.LightModel to a supported model or set AutoRecapEnabled=false",
		e.Model, e.AllowList,
	)
}

// perCandidateTimeoutFromConfig derives a per-candidate timeout for the fallback
// chain from the provider config. It uses the RequestTimeout of the first provider
// that has a positive RequestTimeout value, falling back to the providers package
// default (120s) when no provider is configured or no provider has a positive
// timeout. This value is intentionally used as a global ceiling across all
// candidates — it is derived from a representative provider config, not from the
// specific candidate being attempted, because all candidates share the same
// per-candidate deadline contract.
func perCandidateTimeoutFromConfig(cfg *config.Config) time.Duration {
	for _, p := range cfg.Providers {
		if p != nil && p.RequestTimeout > 0 {
			return time.Duration(p.RequestTimeout) * time.Second
		}
	}
	// 0 signals NewFallbackChainWithTimeout to use its own default (120s).
	return 0
}

// NewAgentLoop constructs an AgentLoop from the given config, message bus, and LLM provider.
// Returns (*AgentLoop, nil) on success. Returns (nil, *RecapModelBootError) when
// AutoRecapEnabled is true and the recap model fails the allow-list gate (FR-029a) —
// callers should treat this as a fatal configuration error and abort boot.
func NewAgentLoop(
	cfg *config.Config,
	msgBus *bus.MessageBus,
	provider providers.LLMProvider,
) (*AgentLoop, error) {
	registry := NewAgentRegistry(cfg, provider)

	// Apply configurable default agent override.
	if cfg.Agents.Defaults.DefaultAgentID != "" {
		registry.SetDefaultAgentOverride(cfg.Agents.Defaults.DefaultAgentID)
	}

	// Set up shared fallback chain with per-candidate timeout so a primary
	// provider timeout does not strand fallback candidates with an exhausted
	// context deadline (#235).
	cooldown := providers.NewCooldownTracker()
	fallbackChain := providers.NewFallbackChainWithTimeout(cooldown, perCandidateTimeoutFromConfig(cfg))

	// Create state manager using default agent's workspace for channel recording
	defaultAgent := registry.GetDefaultAgent()
	var stateManager *state.Manager
	if defaultAgent != nil {
		stateManager = state.NewManager(defaultAgent.Workspace)
	}

	eventBus := NewEventBus()
	al := &AgentLoop{
		bus:                    msgBus,
		cfg:                    cfg,
		registry:               registry,
		state:                  stateManager,
		eventBus:               eventBus,
		summarizing:            sync.Map{},
		fallback:               fallbackChain,
		cmdRegistry:            commands.NewRegistry(commands.BuiltinDefinitions()),
		steering:               newSteeringQueue(parseSteeringMode(cfg.Agents.Defaults.SteeringMode)),
		contextBuilderRegistry: NewContextBuilderRegistry(),
		admission:              newAdmissionController(0),
	}
	al.hooks = NewHookManager(eventBus)
	configureHookManagerFromConfig(al.hooks, cfg)

	// Initialize task store at ~/.omnipus/workflow-tasks/ (separate from GTD tasks/).
	homePath := filepath.Dir(cfg.WorkspacePath())
	al.taskStore = taskstore.New(filepath.Join(homePath, "workflow-tasks"))
	al.taskExecutor = newTaskExecutor(al, al.taskStore)

	// Register workspace session linker: auto-links sessions to workspaces on task create/update.
	if err := al.hooks.Mount(NamedHook("workspace-session-linker", &workspaceLinkerAdapter{
		linker: systools.NewProjectSessionLinker(homePath),
	})); err != nil {
		logger.WarnCF("agent", "Failed to mount workspace-session-linker hook", map[string]any{
			"error": err.Error(),
		})
	}

	// Initialize shared session store at $OMNIPUS_HOME/sessions/.
	// All new chat sessions are created here (joined session model).
	sharedDir := filepath.Join(homePath, "sessions")
	if err := os.MkdirAll(sharedDir, 0o700); err != nil {
		logger.ErrorCF("agent", "Shared session store unavailable — new sessions will use per-agent stores",
			map[string]any{"dir": sharedDir, "error": err.Error()})
	} else {
		sharedStore, ssErr := session.NewUnifiedStoreWithHome(sharedDir, homePath)
		if ssErr != nil {
			logger.ErrorCF("agent", "Shared session store init failed — new sessions will use per-agent stores",
				map[string]any{"dir": sharedDir, "error": ssErr.Error()})
		} else {
			al.sharedSessionStore = sharedStore
			al.rebuildChannelSessionIndex()
		}
	}

	// SEC-15: Initialize structured audit logging (optional) and policy
	// evaluation (always on). Audit directory is ~/.omnipus/system/ (sibling of
	// workspace). The audit logger and the policy evaluator are decoupled:
	// disabling audit logging must NOT disable enforcement.
	if cfg.Sandbox.AuditLog {
		auditDir := filepath.Join(homePath, "system")
		auditLogger, auditErr := audit.NewLogger(audit.LoggerConfig{
			Dir:           auditDir,
			RetentionDays: 90,
			// CRIT-2: signal to NewLogger that the operator explicitly enabled
			// audit. Without this, NewLogger would swallow openCurrentFile
			// errors and return a degraded logger + nil error — the gateway
			// would think audit_logger=ok at startup while every subsequent
			// write rejects in degraded mode. Setting AuditLogRequested makes
			// openCurrentFile failure surface as a *LoggerConstructionError so
			// the gateway boot path can fail closed.
			AuditLogRequested: true,
		})
		if auditErr != nil {
			// B1.2(b): when sandbox.audit_log is explicitly enabled,
			// audit construction failure is a fail-closed boot abort.
			// CLAUDE.md "audit-everything stance is non-negotiable" —
			// silently dropping audit while the operator asked for it would
			// be a security regression. The gateway maps the returned typed
			// error to a SandboxBootError + EX_CONFIG (78) exit code; see
			// pkg/gateway/gateway.go around the agent.NewAgentLoop call.
			logger.ErrorCF("agent",
				"Audit logger construction failed; aborting boot because sandbox.audit_log=true",
				map[string]any{"error": auditErr.Error(), "dir": auditDir})
			return nil, &audit.LoggerConstructionError{Dir: auditDir, Err: auditErr}
		}
		{
			al.auditLogger = auditLogger

			// Log startup event. CRIT-6: route through audit.EmitEntry so a
			// Log failure bumps the audit-skipped counter (/health audit_degraded).
			audit.EmitEntry(auditLogger, &audit.Entry{
				Event:    audit.EventStartup,
				Decision: audit.DecisionAllow,
				Details: map[string]any{
					"audit_dir": auditDir,
				},
			})

			// Wire audit logger into all agent tool registries.
			for _, agentID := range registry.ListAgentIDs() {
				if agent, ok := registry.GetAgent(agentID); ok {
					agent.Tools.SetAuditLogger(auditLogger)
					// Memory tools carry their own audit-logger reference for
					// structured per-entry events (content_sha256 etc.).
					// SetAuditLogger on the registry propagates via auditLoggerAware,
					// but the explicit cast below documents the dependency clearly.
					if t, ok := agent.Tools.Get("remember"); ok {
						if rt, ok := t.(*tools.RememberTool); ok {
							rt.SetAuditLogger(auditLogger)
						} else {
							// SF4: wrong type registered for "remember" — surface the
							// registration-order bug so it doesn't silently skip audit wiring.
							logger.WarnCF("agent",
								"'remember' tool is not a *tools.RememberTool; audit wiring skipped",
								map[string]any{
									"agent_id":  agentID,
									"tool_type": fmt.Sprintf("%T", t),
								})
						}
					}
					if t, ok := agent.Tools.Get("retrospective"); ok {
						if rt, ok := t.(*tools.RetrospectiveTool); ok {
							rt.SetAuditLogger(auditLogger)
						} else {
							logger.WarnCF("agent",
								"'retrospective' tool is not a *tools.RetrospectiveTool; audit wiring skipped",
								map[string]any{
									"agent_id":  agentID,
									"tool_type": fmt.Sprintf("%T", t),
								})
						}
					}
				}
			}
		}
	}

	// SEC-05/SEC-07: Build the policy evaluator from the live config.
	// `cfg.Tools.Exec.AllowedBinaries` is the single source of truth for the
	// exec allowlist (the same field the UI writes to via
	// /api/v1/security/exec-allowlist). Constructing with an explicit
	// SecurityConfig avoids the deny-everything trap of `NewEvaluator(nil)`.
	//
	// Default policy derivation:
	//   - A non-empty allowlist means the operator opted into SEC-05 binary
	//     restriction — default_policy is "deny" so unlisted binaries are blocked.
	//   - An empty allowlist means no opt-in — default_policy is "allow" so
	//     the existing guardCommand() checks remain the only exec restriction.
	// This preserves backward compatibility for agents that never touched the
	// allowlist, while honoring fail-closed semantics for agents that did.
	defaultPolicy := policy.PolicyAllow
	if len(cfg.Tools.Exec.AllowedBinaries) > 0 {
		defaultPolicy = policy.PolicyDeny
	}
	// Convert global tool policies from config (map[string]string) to the
	// typed map[string]ToolPolicy that SecurityConfig expects.
	var globalToolPolicies map[string]policy.ToolPolicy
	if len(cfg.Sandbox.ToolPolicies) > 0 {
		globalToolPolicies = make(map[string]policy.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
		for k, v := range cfg.Sandbox.ToolPolicies {
			globalToolPolicies[k] = policy.ToolPolicy(v)
		}
	}
	secCfg := &policy.SecurityConfig{
		DefaultPolicy: defaultPolicy,
		Policy: policy.PolicySection{
			Exec: policy.ExecPolicy{
				AllowedBinaries: cfg.Tools.Exec.AllowedBinaries,
				Approval:        cfg.Tools.Exec.Approval,
			},
		},
		ToolPolicies:      globalToolPolicies,
		DefaultToolPolicy: policy.ToolPolicy(cfg.Sandbox.DefaultToolPolicy),
	}
	policyEval := policy.NewEvaluator(secCfg)

	// Wrap the evaluator in a PolicyAuditor so every decision is audit-logged
	// (ADR-002 §W-3). When audit logging is disabled the bridge is nil; the
	// PolicyAuditor tolerates a nil logger and still enforces — enforcement
	// must NOT depend on audit logging being enabled.
	var auditBridgeImpl *auditBridge
	if al.auditLogger != nil {
		auditBridgeImpl = newAuditBridge(al.auditLogger)
	}
	var policyAuditorLogger policy.AuditLogger
	if auditBridgeImpl != nil {
		policyAuditorLogger = auditBridgeImpl
	}
	al.policyAuditor = policy.NewPolicyAuditor(policyEval, policyAuditorLogger, "")

	// SEC-01/02/03: Select the best-available sandbox backend. This never
	// fails: on unsupported kernels SelectBackend returns a FallbackBackend.
	backend, backendName := sandbox.SelectBackend()
	al.sandboxBackend = backend
	logger.InfoCF("agent", "Sandbox backend selected", map[string]any{"backend": backendName})

	// SEC-25: Initialize the prompt-injection guard. NewPromptGuardFromConfig
	// defaults to "medium" strictness when the field is empty. Construction
	// is cheap and cannot fail, so we always build it — runTurn checks the
	// untrusted-tool allowlist before invoking it, so trusted results are
	// never sanitized even when the guard is non-nil.
	al.promptGuard = security.NewPromptGuardFromConfig(policy.PromptGuardConfig{
		Strictness: string(cfg.Sandbox.PromptInjectionLevel),
	})
	logger.InfoCF("agent", "Prompt guard initialized",
		map[string]any{"strictness": string(al.promptGuard.Strictness())})

	// SEC-24: Build the singleton SSRFChecker from config. When SSRF is enabled,
	// all outbound HTTP tool surfaces receive this checker so allow_internal is
	// honored uniformly. When disabled the checker is nil and callers fall back
	// to their default (proxy-aware) HTTP clients.
	//
	// v0.2 (#155 item 4): cfg.Sandbox.EgressAllowCIDRs is the operator escape
	// hatch for the default-deny outbound posture. Entries here are merged
	// into the SSRFChecker's allow-list alongside the SSRF.AllowInternal list
	// so a single field per concern keeps semantics clear: SSRF allow-list =
	// "this hostname/IP/CIDR is exempt from SSRF blocking". Both fields feed
	// the same checker; the merge is order-stable so an operator who lists
	// "10.0.0.5" in AllowInternal AND "10.0.0.0/8" in EgressAllowCIDRs gets
	// both — the more specific exact-IP entry takes O(1) precedence in
	// CheckIP's lookup map.
	if cfg.Sandbox.SSRF.Enabled {
		merged := make([]string, 0,
			len(cfg.Sandbox.SSRF.AllowInternal)+len(cfg.Sandbox.EgressAllowCIDRs))
		merged = append(merged, cfg.Sandbox.SSRF.AllowInternal...)
		merged = append(merged, cfg.Sandbox.EgressAllowCIDRs...)
		al.ssrfChecker = security.NewSSRFChecker(merged)
		logger.InfoCF("agent", "SSRF protection enabled",
			map[string]any{
				"allow_internal_count":     len(cfg.Sandbox.SSRF.AllowInternal),
				"egress_allow_cidrs_count": len(cfg.Sandbox.EgressAllowCIDRs),
			})
	}

	// SEC-28: Start the loopback SSRF proxy for exec child processes when
	// enabled. On bind failure we log and fall back to degraded mode (child
	// processes run without HTTP_PROXY env vars — LIM-02) rather than
	// failing startup, because exec is a core tool and a proxy bind failure
	// on a shared port should not take the whole agent loop down.
	if cfg.Tools.Exec.EnableProxy {
		// Reuse the singleton SSRF checker (which may be nil when SSRF is disabled).
		proxy := security.NewExecProxy(al.ssrfChecker, nil)
		if err := proxy.Start(); err != nil {
			logger.ErrorCF("agent", "Failed to start exec SSRF proxy; child processes will run without proxy env vars",
				map[string]any{"error": err.Error()})
		} else {
			al.execProxy = proxy
			logger.InfoCF("agent", "Exec SSRF proxy started",
				map[string]any{"addr": proxy.Addr()})
		}
	}

	// Initialize cancel abuse detector (shared across all four cancel entry points).
	al.cancelAbuse = newCancelAbuseDetector()

	// SEC-26: Initialize rate limiter registry and persistent cost tracker.
	// The registry always exists so per-agent windows can be created even when
	// no cap is configured; SetDailyCostCap(0) disables cost-cap enforcement.
	al.rateLimiter = security.NewRateLimiterRegistry()
	al.rateLimiter.SetDailyCostCap(cfg.Sandbox.RateLimits.DailyCostCapUSD)
	costPath := filepath.Join(homePath, "system", "cost.json")
	al.costTracker = security.NewCostTracker(costPath)
	al.costTracker.LoadIntoRegistry(al.rateLimiter)
	logger.InfoCF("agent", "Rate limiter initialized",
		map[string]any{
			"daily_cost_cap_usd":              cfg.Sandbox.RateLimits.DailyCostCapUSD,
			"max_agent_llm_calls_per_hour":    cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour,
			"max_agent_tool_calls_per_minute": cfg.Sandbox.RateLimits.MaxAgentToolCallsPerMinute,
		})

	// v0.2 #155 item 6: build the shared memory-write rate limiter and
	// propagate it to every agent's tool registry. One limiter is shared
	// across all agents so the per-caller bucket is genuinely global —
	// otherwise a malicious caller could route writes through different
	// agents to dodge the per-caller ceiling. The per-agent bucket is keyed
	// on the agent ID inside the limiter so independence is preserved.
	//
	// Defaults (60 per agent / minute, 600 per caller / minute) are intentional;
	// not configurable via cfg today because no operator has expressed a need
	// to tune them and exposing knobs invites footguns. The constructor accepts
	// a MemoryRateLimitConfig so a future config-backed override can be wired
	// in without a structural change.
	memRL := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{})
	for _, agentID := range registry.ListAgentIDs() {
		if agentInst, ok := registry.GetAgent(agentID); ok {
			agentInst.Tools.SetMemoryRateLimiter(memRL)
		}
	}
	logger.InfoCF("agent", "Memory write rate limiter initialized",
		map[string]any{
			"per_agent_per_minute":  memRL.PerAgentLimit(),
			"per_caller_per_minute": memRL.PerCallerLimit(),
		})

	// Register shared tools to all agents (now that al is created)
	registerSharedTools(al, cfg, msgBus, registry, provider)

	// Replace the exec tool in each agent's registry with a version that has
	// the policy auditor and sandbox backend wired in. Registering the same
	// tool name overwrites the previous entry (see ToolRegistry.Register).
	al.wireExecToolDeps()

	// FR-029a: Validate the recap model allow-list at boot.
	// If AutoRecapEnabled is true and the resolved recap model doesn't match any
	// pattern in the allow-list, log an error and exit — misconfigured recap model
	// must not allow silent fallback to an expensive model at runtime.
	if cfg.Agents.Defaults.AutoRecapEnabled {
		var recapModel string
		if cfg.Agents.Defaults.Routing != nil {
			recapModel = cfg.Agents.Defaults.Routing.LightModel
		}
		if recapModel == "" {
			// Use the default agent's primary model.
			if defaultAgent := registry.GetDefaultAgent(); defaultAgent != nil {
				recapModel = defaultAgent.Model
			}
		}
		if recapModel != "" {
			allowList := cfg.Agents.Defaults.ResolveRecapModelAllowList()
			matched := false
			for _, pattern := range allowList {
				if strings.HasPrefix(recapModel, pattern) {
					matched = true
					break
				}
			}
			if !matched {
				return nil, &RecapModelBootError{
					Model:     recapModel,
					AllowList: allowList,
				}
			}
		}
	}

	// Fix A (FR-057): wire the environment provider into every agent's
	// ContextBuilder now that the sandbox backend is known. Also register each
	// ContextBuilder into the registry so config-change invalidation (FR-061)
	// can broadcast across all agents.
	al.wireEnvProviders(cfg, registry)

	return al, nil
}

// ContextBuilderRegistry returns the registry used to broadcast system-prompt
// cache invalidation when operator config changes (FR-061). Always non-nil
// after NewAgentLoop.
func (al *AgentLoop) ContextBuilderRegistry() *ContextBuilderRegistry {
	if al == nil {
		return nil
	}
	return al.contextBuilderRegistry
}

// GetCurrentSession returns the active session ID for the given agent, and whether
// one was found. Used by the WebSocket lazy-CAS logic (FR-024).
func (al *AgentLoop) GetCurrentSession(agentID string) (string, bool) {
	if v, ok := al.agentCurrentSession.Load(agentID); ok {
		return v.(string), true
	}
	return "", false
}

// SetCurrentSession records the active session ID for the given agent.
// Used by the WebSocket lazy-CAS logic (FR-024).
func (al *AgentLoop) SetCurrentSession(agentID, sessionID string) {
	al.agentCurrentSession.Store(agentID, sessionID)
}

// RegisterIdleTicker stores a cancel function for the idle ticker of a session.
// Calling it again for the same session replaces the previous cancel without
// canceling it — use resetIdleTicker for the reset path.
func (al *AgentLoop) RegisterIdleTicker(sessionID string, cancel context.CancelFunc) {
	al.idleTickers.Store(sessionID, cancel)
}

// cancelIdleTicker cancels and removes the idle ticker for a session.
// No-op if no ticker is registered.
func (al *AgentLoop) cancelIdleTicker(sessionID string) {
	if v, ok := al.idleTickers.LoadAndDelete(sessionID); ok {
		v.(context.CancelFunc)()
	}
}

// resetIdleTicker cancels any existing idle ticker for sessionID and starts a
// new one. On timeout, CloseSession is called with trigger="idle". The timer
// is driven by cfg.Agents.Defaults.GetIdleTimeoutMinutes(). If AutoRecapEnabled
// is false the ticker is still started but CloseSession will return immediately.
func (al *AgentLoop) resetIdleTicker(sessionID string) {
	if sessionID == "" {
		return
	}
	// Cancel the previous ticker (if any).
	al.cancelIdleTicker(sessionID)

	cfg := al.GetConfig()
	timeoutMinutes := cfg.Agents.Defaults.GetIdleTimeoutMinutes()
	ctx, cancel := context.WithCancel(context.Background())
	al.RegisterIdleTicker(sessionID, cancel)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("agent", "Idle ticker goroutine panic recovered",
					map[string]any{"session_id": sessionID, "panic": fmt.Sprintf("%v", r)})
			}
		}()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(timeoutMinutes) * time.Minute):
			al.CloseSession(sessionID, "idle")
		}
	}()
}

// AuditLogger returns the audit logger, or nil if audit logging is disabled.
// Used by gateway handlers that need to log policy changes.
func (al *AgentLoop) AuditLogger() *audit.Logger {
	if al == nil {
		return nil
	}
	return al.auditLogger
}

// ExecProxy returns the SEC-28 SSRF proxy for exec child processes, or nil
// when the proxy is disabled or failed to bind. Used by gateway handlers that
// report the proxy status and by tests that exercise the proxy lifecycle.
func (al *AgentLoop) ExecProxy() *security.ExecProxy {
	if al == nil {
		return nil
	}
	return al.execProxy
}

// PromptGuard returns the SEC-25 prompt-injection guard. Always non-nil after
// NewAgentLoop — even when no config field is set, the factory returns a
// medium-strictness guard. Used by runTurn and by gateway status handlers.
func (al *AgentLoop) PromptGuard() *security.PromptGuard {
	if al == nil {
		return nil
	}
	return al.promptGuard
}

// RateLimiter returns the SEC-26 rate limiter registry. Always non-nil after
// NewAgentLoop. Used by runTurn for per-agent limit checks and by gateway
// handlers that report the current rate limit / cost status.
func (al *AgentLoop) RateLimiter() *security.RateLimiterRegistry {
	if al == nil {
		return nil
	}
	return al.rateLimiter
}

// SandboxBackend returns the active sandbox backend, or nil if sandboxing is
// disabled. Used by gateway handlers that report sandbox status.
func (al *AgentLoop) SandboxBackend() sandbox.SandboxBackend {
	if al == nil {
		return nil
	}
	return al.sandboxBackend
}

// SetAppliedSandboxMode stores the mode that the kernel sandbox actually applied
// at boot (from SandboxApplyResult.Mode). Must be called from the gateway boot
// path after applySandbox returns successfully, before WireTier13Deps and
// wireExecToolDeps run, so that ExecToolDeps.SandboxMode reflects the true
// runtime enforcement level rather than the config file value.
func (al *AgentLoop) SetAppliedSandboxMode(mode sandbox.Mode) {
	if al == nil {
		return
	}
	al.appliedSandboxMode = mode
}

// recordRateLimitDenial writes an audit entry and emits a RateLimit event for
// a denied rate-limit or cost-cap check (SEC-26). Centralizing this avoids
// repeating the same audit + emit boilerplate for each of the three checks
// (LLM calls, tool calls, global cost cap). extraDetails is merged into the
// audit entry's Details map under a "limit_type" key and caller-supplied
// fields. Audit failures are logged at warn level and swallowed — a rate-limit
// denial must still be reported to the caller even when the audit logger is
// unhealthy.
func (al *AgentLoop) recordRateLimitDenial(
	ts *turnState,
	limitType string,
	payload RateLimitPayload,
	extraDetails map[string]any,
) {
	if al.auditLogger != nil {
		details := map[string]any{"limit_type": limitType}
		for k, v := range extraDetails {
			details[k] = v
		}
		// CRIT-6: route through audit.EmitEntry — Log failure bumps the
		// audit-skipped counter (/health audit_degraded).
		audit.EmitEntry(al.auditLogger, &audit.Entry{
			Event:      audit.EventRateLimit,
			Decision:   audit.DecisionDeny,
			AgentID:    ts.agent.ID,
			Tool:       payload.Tool,
			PolicyRule: payload.PolicyRule,
			Details:    details,
		})
	}
	// FIX-1 / FR-001: rate-limit denials MUST be visible after a page reload.
	// The spec requires EventKindError in the JSONL transcript (consumed by the
	// replay path on session reopen), but the live WS UI still subscribes to
	// EventKindRateLimit for the dedicated denial banner. We therefore emit BOTH
	// — EventKindError drives the persistent record + replay, EventKindRateLimit
	// drives the live toast/banner. The transcript write below closes the
	// "Error replay gap" called out as US-1.
	al.emitEvent(
		EventKindError,
		ts.eventMeta("runTurn", "turn.error"),
		payload,
	)
	al.emitEvent(
		EventKindRateLimit,
		ts.eventMeta("runTurn", "turn.rate_limit"),
		payload,
	)
	// Persist the rate-limit context to the JSONL transcript so a session
	// reopen re-renders the error in the chat. Without this, the live
	// `rate_limit` frame is only visible during the current session — the
	// spec calls this out as the "Error replay gap" (US-1).
	//
	// Default the retry hint to "retry shortly" when the policy didn't
	// surface a positive RetryAfterSeconds — a zero/negative value would
	// otherwise render as "retry after 0s" or "retry after -5s", which
	// reads as a bug to the operator.
	retryHint := fmt.Sprintf("retry after %.0fs", payload.RetryAfterSeconds)
	if payload.RetryAfterSeconds <= 0 {
		retryHint = "retry shortly"
	}
	rlMsg := fmt.Sprintf("rate limit: %s (%s)", payload.PolicyRule, retryHint)
	ts.appendErrorTranscript(EventKindRateLimit.String(), "runTurn", rlMsg)
}

// wireExecToolDeps replaces each agent's exec tool with one constructed via
// NewExecToolWithDeps, injecting the policy auditor (SEC-05) and the sandbox
// backend (SEC-01/02/03). This runs after NewAgentInstance has created the
// default exec tool so that all other tool setup (deny patterns, allow paths,
// timeouts) is preserved — we only add the security deps on top.
//
// No-op when the agent has exec disabled or when the registry lookup fails.
func (al *AgentLoop) wireExecToolDeps() {
	al.wireExecToolDepsOn(al.registry)
}

// wireExecToolDepsOn is the registry-parameterized form of wireExecToolDeps,
// used by hot-reload to wire the new registry before the atomic swap.
func (al *AgentLoop) wireExecToolDepsOn(registry *AgentRegistry) {
	if registry == nil {
		return
	}
	cfg := al.cfg
	if cfg == nil {
		return
	}
	allowReadPaths := buildAllowReadPatterns(cfg)

	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok || agent == nil || agent.Tools == nil {
			continue
		}

		// Workspace-scoped sandbox policy: allow read/write/execute under
		// the agent's workspace. Landlock inherits to children natively on
		// Linux 5.13+, so no per-child application is actually required
		// there — the fallback backend still uses this to emit
		// OMNIPUS_SANDBOX_PATHS for cooperative scripts.
		//
		// Bind-port rules: kernel-level enforcement is installed once at
		// gateway boot (see pkg/gateway/sandbox_apply.go) and inherited by
		// all child processes via Landlock's restrict_self ratchet — there
		// is no need to re-add the rules on each exec child. We still
		// populate BindPortRules here so cooperative children that read
		// OMNIPUS_SANDBOX_* env vars get the full picture of what they may
		// bind, and the rules are visible in tooling that introspects
		// ExecToolDeps.
		//
		// ConnectPortRules were re-introduced in v0.2 (#155 item 4) and
		// are installed process-wide once at gateway boot via DefaultPolicy
		// + sandbox_apply.go. Children inherit the connect-port allow-list
		// through Landlock's restrict_self ratchet — no per-child re-add
		// is required. The tool-side ExecToolDeps.SandboxPolicy struct
		// carries an empty ConnectPortRules slice here intentionally; it
		// exists to keep the type symmetric and to feed cooperative
		// FallbackBackend env-var injection without duplicating the
		// kernel-level allow-list across every spawned child.
		var bindPorts []sandbox.NetPortRule
		if al.sandboxBackend != nil {
			abi := 0
			if rep, ok := al.sandboxBackend.(interface{ ABIVersion() int }); ok {
				abi = rep.ABIVersion()
			}
			if abi >= 4 {
				pr := cfg.Sandbox.DevServerPortRange
				if !pr.IsZero() {
					for p := pr.Min(); p <= pr.Max(); p++ {
						if p < 1 || p > 65535 {
							continue
						}
						bindPorts = append(bindPorts, sandbox.NetPortRule{Port: uint16(p)})
					}
				}
			}
		}
		policy := sandbox.SandboxPolicy{
			FilesystemRules: []sandbox.PathRule{
				{
					Path:   agent.Workspace,
					Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute,
				},
			},
			BindPortRules:     bindPorts,
			InheritToChildren: true,
		}

		// Use the mode that the kernel sandbox actually applied at boot
		// (SetAppliedSandboxMode sets this from SandboxApplyResult.Mode).
		// Zero value maps to ModeOff in ExecTool.sandboxOn(), which is the
		// correct default when the gateway did not wire the applied mode
		// (headless tests, legacy callers).
		deps := tools.ExecToolDeps{
			SandboxPolicy:      policy,
			SandboxMode:        string(al.appliedSandboxMode),
			ExecTimeoutSeconds: int32(cfg.Tools.Exec.TimeoutSeconds),
		}
		// Plumb the kernel-sandbox egress proxy into the exec tool so the
		// hardened-exec path (sandbox=enforce / permissive) injects
		// HTTP_PROXY pointing at the allow-listed proxy. Nil-guarded to
		// avoid the typed-nil-in-interface trap and so the exec tool
		// gracefully degrades to no-proxy when the boot-time
		// NewEgressProxy call failed.
		if al.sandboxEgressProxy != nil {
			deps.EgressProxy = al.sandboxEgressProxy
		}
		// Both dependency fields use interfaces, so we must nil-guard at
		// assignment time to avoid typed-nil traps: storing a nil
		// *policy.PolicyAuditor or nil sandbox.SandboxBackend in an interface
		// field would create a non-nil interface holding a nil pointer,
		// defeating downstream `!= nil` checks and causing nil-pointer panics.
		if al.policyAuditor != nil {
			deps.PolicyAuditor = al.policyAuditor
		}
		if al.sandboxBackend != nil {
			deps.SandboxBackend = al.sandboxBackend
		}
		// SEC-28: Hand the exec proxy to the tool so it can inject
		// HTTP_PROXY env vars on every child. nil-guarded at assignment
		// time to avoid the typed-nil-in-interface trap.
		if al.execProxy != nil {
			deps.ExecProxy = al.execProxy
		}

		restrict := cfg.Agents.Defaults.RestrictToWorkspace
		execTool, err := tools.NewExecToolWithDeps(agent.Workspace, restrict, cfg, deps, allowReadPaths)
		if err != nil {
			// Fail closed: if security wiring fails, remove the exec tool from the
			// registry entirely. The agent will lose exec capability but
			// cannot run commands without the security layer.
			logger.ErrorCF("agent", "Failed to wire exec tool deps; removing exec tool (fail closed)",
				map[string]any{"agent_id": agentID, "error": err.Error()})
			agent.Tools.Unregister("exec")
			continue
		}
		agent.Tools.Register(execTool)
	}
}

// WireTier13Deps registers the web_serve, workspace.shell, and
// workspace.shell_bg tools into every non-system agent's tool registry using
// the shared infrastructure instances created once at gateway boot. Called
// from gateway.go after NewAgentLoop and after the Tier13Deps registries
// (DevServerRegistry, ServedSubdirs, EgressProxy) are constructed. The
// "Tier13" name is historical — Tier 1 (static serve) and Tier 3 (dev-server
// proxy) used to live in two separate tools (serve_workspace and
// run_in_workspace); both are now subsumed by web_serve, but the deps struct
// keeps the legacy name for cross-package callers.
//
// Mirrors the wireExecToolDeps pattern: post-creation injection so the heavy
// singleton objects (EgressProxy, DevServerRegistry) are not re-created per
// agent. Nil fields in deps skip the corresponding tool registration
// (graceful degradation when preview is disabled or Tier 3 unsupported).
func (al *AgentLoop) WireTier13Deps(deps Tier13Deps) {
	// Stash a copy so hot-reload can re-apply the wiring on the rebuilt registry.
	depsCopy := deps
	al.tier13Deps = &depsCopy

	// Stash the sandbox egress proxy for the exec tool. wireExecToolDeps
	// (which originally ran during NewAgentLoop, before the gateway had
	// constructed the proxy) is re-run below so each agent's exec tool now
	// picks up the proxy address on the sandbox-on path.
	if deps.EgressProxy != nil {
		al.sandboxEgressProxy = deps.EgressProxy
	}

	al.wireTier13DepsLocked(al.registry, deps)

	// Re-wire exec deps now that we have the egress proxy. Without this,
	// the exec tool's hardened path runs without HTTP_PROXY env vars.
	al.wireExecToolDeps()
}

// wireTier13DepsLocked is the actual wiring logic, factored out so hot-reload
// can re-apply it against a freshly-built registry without re-stashing.
func (al *AgentLoop) wireTier13DepsLocked(registry *AgentRegistry, deps Tier13Deps) {
	if registry == nil {
		return
	}
	cfg := al.cfg
	if cfg == nil {
		return
	}

	minDurSec := cfg.Tools.ServeWorkspace.MinDurationSeconds
	maxDurSec := cfg.Tools.ServeWorkspace.MaxDurationSeconds

	for _, agentID := range registry.ListAgentIDs() {
		ag, ok := registry.GetAgent(agentID)
		if !ok || ag == nil || ag.Tools == nil {
			continue
		}

		// web_serve — unified Tier 1 (static) + Tier 3 (dev server) tool.
		// Registered whenever ServedSubdirs is available; dev mode is gated to
		// Linux at runtime inside the tool itself (Tier3UnsupportedMessage).
		if deps.ServedSubdirs != nil {
			previewURL := deps.GatewayPreviewBaseURL
			if previewURL == "" {
				previewURL = deps.GatewayBaseURL
			}
			portRange := cfg.Sandbox.DevServerPortRange
			webServeCfg := tools.WebServeDevConfig{
				Tier3Commands:   cfg.Sandbox.Tier3Commands,
				PortRange:       [2]int32{portRange[0], portRange[1]},
				MaxConcurrent:   cfg.Sandbox.MaxConcurrentDevServers,
				EgressAllowList: cfg.Sandbox.EgressAllowList,
				AuditFailClosed: resolveBoolWithDefault(cfg.Sandbox.PathGuardAuditFailClosed, cfg.Sandbox.AuditLog),
			}
			webServeTool := tools.NewWebServeTool(
				ag.Workspace,
				ag.ID,
				previewURL,
				deps.ServedSubdirs,
				deps.DevServerRegistry, // nil on non-Linux; tool guards internally
				webServeCfg,
				deps.EgressProxy,
				al.auditLogger,
				minDurSec,
				maxDurSec,
			)
			ag.Tools.Register(webServeTool)
		}

		// workspace.shell (experimental foreground shell).
		// Only registered when experimental.workspace_shell_enabled=true.
		// The tool is per-agent because it needs the agent's workspace path,
		// sandbox profile, and shell policy — the same reason web_serve dev
		// mode is wired here rather than in the process-level BuiltinRegistry.
		if resolveBoolWithDefault(cfg.Sandbox.Experimental.WorkspaceShellEnabled, false) {
			// Resolve SandboxProfile for this agent from the global config.
			// We scan cfg.Agents.List for a matching entry; if not found we
			// use the global DefaultProfile (which itself defaults to workspace).
			agentProfile := cfg.Sandbox.DefaultProfile
			var agentShellPolicy *config.AgentShellPolicy
			for i := range cfg.Agents.List {
				entry := &cfg.Agents.List[i]
				if entry.ID == ag.ID {
					if entry.SandboxProfile != "" {
						agentProfile = entry.SandboxProfile
					}
					agentShellPolicy = entry.ShellPolicy
					break
				}
			}
			// Coerce off → workspace when god mode is unavailable or not opted in.
			// This is the runtime-side enforcement of latches (1) and (2). The REST
			// handler enforces the same at write time so persisted config never has
			// off without the latches; this is defense-in-depth.
			if agentProfile == config.SandboxProfileOff {
				al.mu.RLock()
				godModeOptedIn := al.allowGodMode
				al.mu.RUnlock()
				if !sandbox.GodModeAvailable || !godModeOptedIn {
					// Log at most once per process lifetime per agent to prevent
					// the WARN from flooding on hot reloads.
					if _, alreadyLogged := al.coercionLogged.LoadOrStore(ag.ID, true); !alreadyLogged {
						logger.WarnCF("agent", "sandbox_profile=off coerced to workspace; --allow-god-mode not set",
							map[string]any{"agent_id": ag.ID})
						// CRIT-6: route through audit.EmitEntry so a Log failure
						// bumps the audit-skipped counter (/health audit_degraded).
						audit.EmitEntry(al.auditLogger, &audit.Entry{
							Event:    audit.EventPolicyEval,
							Decision: audit.DecisionDeny,
							AgentID:  ag.ID,
							Tool:     "sandbox_profile",
							Details: map[string]any{
								"reason":       "god_mode_unavailable",
								"coerced_from": "off",
								"coerced_to":   "workspace",
							},
						})
					}
					agentProfile = config.SandboxProfileWorkspace
				}
			}
			shellTool := tools.NewWorkspaceShellTool(tools.WorkspaceShellDeps{
				WorkspaceDir: ag.Workspace,
				Profile:      agentProfile,
				Proxy:        deps.EgressProxy,
				AuditLogger:  al.auditLogger,
				AuditFailClosed: resolveBoolWithDefault(
					cfg.Sandbox.PathGuardAuditFailClosed,
					cfg.Sandbox.AuditLog,
				),
				GlobalShellDenyPatterns: cfg.Sandbox.ShellDenyPatterns,
				AgentShellPolicy:        agentShellPolicy,
			})
			ag.Tools.Register(shellTool)

			// workspace.shell_bg (experimental background tool).
			// Registered under the same workspace_shell_enabled flag as
			// workspace.shell — same trust level, same governance.
			if deps.DevServerRegistry != nil {
				portRange := cfg.Sandbox.DevServerPortRange
				shellBgTool := tools.NewWorkspaceShellBgTool(tools.WorkspaceShellBgDeps{
					WorkspaceDir: ag.Workspace,
					Profile:      agentProfile,
					Proxy:        deps.EgressProxy,
					AuditLogger:  al.auditLogger,
					AuditFailClosed: resolveBoolWithDefault(
						cfg.Sandbox.PathGuardAuditFailClosed,
						cfg.Sandbox.AuditLog,
					),
					Registry:                deps.DevServerRegistry,
					MaxConcurrent:           cfg.Sandbox.MaxConcurrentDevServers,
					PortRange:               [2]int32{portRange[0], portRange[1]},
					GatewayHost:             deps.GatewayPreviewBaseURL,
					GlobalShellDenyPatterns: cfg.Sandbox.ShellDenyPatterns,
					AgentShellPolicy:        agentShellPolicy,
				})
				ag.Tools.Register(shellBgTool)
			}
		}
	}

	logger.InfoCF("agent", "Tier 1/2/3 tools wired into agent registry", map[string]any{
		"preview_base_url":          deps.GatewayPreviewBaseURL,
		"served_subdirs_ready":      deps.ServedSubdirs != nil,
		"dev_server_registry_ready": deps.DevServerRegistry != nil,
		"egress_proxy_ready":        deps.EgressProxy != nil,
		"workspace_shell_enabled":   resolveBoolWithDefault(cfg.Sandbox.Experimental.WorkspaceShellEnabled, false),
	})
}

// resolveBoolWithDefault returns the bool value from a *bool, falling back to
// defaultVal when the pointer is nil. Used by WireTier13Deps for config fields
// that default true when absent.
func resolveBoolWithDefault(p *bool, defaultVal bool) bool {
	if p == nil {
		return defaultVal
	}
	return *p
}

// registerSharedTools registers tools that are shared across all agents.
func registerSharedTools(
	al *AgentLoop,
	cfg *config.Config,
	msgBus *bus.MessageBus,
	registry *AgentRegistry,
	provider providers.LLMProvider,
) {
	allowReadPaths := buildAllowReadPatterns(cfg)

	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok {
			continue
		}

		// Web search tool — always registered; policy decides invocation.
		// Per-provider Enabled sub-flags (Brave, Tavily, etc.) are retained because
		// they select which upstream API is used, not whether the tool exists.
		searchTool, err := tools.NewWebSearchTool(tools.WebSearchToolOptions{
			BraveAPIKeys:          braveKeys(cfg.Tools.Web.Brave.APIKey()),
			BraveMaxResults:       cfg.Tools.Web.Brave.MaxResults,
			BraveEnabled:          cfg.Tools.Web.Brave.Enabled,
			TavilyAPIKeys:         tavilyKeys(cfg.Tools.Web.Tavily.APIKey()),
			TavilyBaseURL:         cfg.Tools.Web.Tavily.BaseURL,
			TavilyMaxResults:      cfg.Tools.Web.Tavily.MaxResults,
			TavilyEnabled:         cfg.Tools.Web.Tavily.Enabled,
			DuckDuckGoMaxResults:  cfg.Tools.Web.DuckDuckGo.MaxResults,
			DuckDuckGoEnabled:     cfg.Tools.Web.DuckDuckGo.Enabled,
			PerplexityAPIKeys:     perplexityKeys(cfg.Tools.Web.Perplexity.APIKey()),
			PerplexityMaxResults:  cfg.Tools.Web.Perplexity.MaxResults,
			PerplexityEnabled:     cfg.Tools.Web.Perplexity.Enabled,
			SearXNGBaseURL:        cfg.Tools.Web.SearXNG.BaseURL,
			SearXNGMaxResults:     cfg.Tools.Web.SearXNG.MaxResults,
			SearXNGEnabled:        cfg.Tools.Web.SearXNG.Enabled,
			GLMSearchAPIKey:       cfg.Tools.Web.GLMSearch.APIKey(),
			GLMSearchBaseURL:      cfg.Tools.Web.GLMSearch.BaseURL,
			GLMSearchEngine:       cfg.Tools.Web.GLMSearch.SearchEngine,
			GLMSearchMaxResults:   cfg.Tools.Web.GLMSearch.MaxResults,
			GLMSearchEnabled:      cfg.Tools.Web.GLMSearch.Enabled,
			BaiduSearchAPIKey:     cfg.Tools.Web.BaiduSearch.APIKey(),
			BaiduSearchBaseURL:    cfg.Tools.Web.BaiduSearch.BaseURL,
			BaiduSearchMaxResults: cfg.Tools.Web.BaiduSearch.MaxResults,
			BaiduSearchEnabled:    cfg.Tools.Web.BaiduSearch.Enabled,
			Proxy:                 cfg.Tools.Web.Proxy,
			SSRFChecker:           al.ssrfChecker, // SEC-24: nil when SSRF disabled
		})
		if err != nil {
			logger.ErrorCF("agent", "Failed to create web search tool", map[string]any{"error": err.Error()})
		} else if searchTool != nil {
			agent.Tools.Register(searchTool)
		}

		fetchTool, err := tools.NewWebFetchToolWithProxy(
			50000,
			cfg.Tools.Web.Proxy,
			cfg.Tools.Web.Format,
			cfg.Tools.Web.FetchLimitBytes,
			cfg.Tools.Web.PrivateHostWhitelist)
		if err != nil {
			logger.ErrorCF("agent", "Failed to create web fetch tool", map[string]any{"error": err.Error()})
		} else {
			agent.Tools.Register(fetchTool)
		}

		// Message tool — outbound inter-agent message via bus.
		messageTool := tools.NewMessageTool()
		messageTool.SetSendCallback(func(channel, chatID, content string) error {
			pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer pubCancel()
			return msgBus.PublishOutbound(pubCtx, bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Content: content,
			})
		})
		agent.Tools.Register(messageTool)

		// Handoff tools — always registered (ScopeCore).
		getRegistryReader := func() tools.AgentRegistryReader {
			return al.GetRegistry()
		}
		onHandoffFrontend := func(evt tools.HandoffEvent) {
			// Override is keyed by session_id so each session carries its own
			// handoff state. Switching to another session never inherits this.
			if evt.SessionID == "" {
				return
			}
			if evt.AgentID == "" {
				al.sessionActiveAgent.Delete("session:" + evt.SessionID)
			} else {
				al.sessionActiveAgent.Store("session:"+evt.SessionID, evt.AgentID)
			}
		}
		getContextWindow := func(targetAgentID string) int {
			currentCfg := al.GetConfig()
			if currentCfg.Agents.Defaults.ContextWindow > 0 {
				return currentCfg.Agents.Defaults.ContextWindow
			}
			return 8192
		}
		getDefaultAgent := func() string {
			currentCfg := al.GetConfig()
			if currentCfg.Agents.Defaults.DefaultAgentID != "" {
				return currentCfg.Agents.Defaults.DefaultAgentID
			}
			return DefaultAgentID
		}
		// sharedStore is the shared session store; tools handle a nil store by
		// skipping transcript ops (nil only occurs in tests without a store).
		sharedStore := al.GetSessionStore()
		agent.Tools.Register(tools.NewHandoffTool(getRegistryReader, sharedStore, getContextWindow, onHandoffFrontend))
		agent.Tools.Register(tools.NewReturnToDefaultTool(sharedStore, getDefaultAgent, onHandoffFrontend))

		// Send file tool (outbound media via MediaStore — store injected later by SetMediaStore).
		sendFileTool := tools.NewSendFileTool(
			agent.Workspace,
			cfg.Agents.Defaults.RestrictToWorkspace,
			cfg.Agents.Defaults.GetMaxMediaSize(),
			nil,
			allowReadPaths,
		)
		agent.Tools.Register(sendFileTool)

		// Skill discovery and installation tools — always registered.
		// Runtime failures (ClawHub unreachable, no auth token) surface at call
		// time with clear errors.
		{
			clawHubConfig := cfg.Tools.Skills.Registries.ClawHub
			githubConfig := cfg.Tools.Skills.Github

			// GitHub registry: enabled when a token ref is configured.
			// The token is resolved via the credential store (SEC-23): credentials.InjectFromConfig
			// writes the secret into the env-var named by TokenRef before this point.
			var githubRegistries []skills.GitHubRegistryConfig
			if githubConfig.TokenRef != "" {
				githubRegistries = []skills.GitHubRegistryConfig{
					{
						Enabled:   true,
						Name:      "github",
						Token:     os.Getenv(githubConfig.TokenRef),
						Proxy:     githubConfig.Proxy,
						Workspace: agent.Workspace,
					},
				}
			}

			// Agent↔UI parity: the UI builds its ClawHub client directly via
			// skills.NewClawHubRegistry, which defaults BaseURL to
			// https://clawhub.ai when empty. Mirror that here so the agent's
			// find_skills/install_skill reach the SAME ClawHub the UI does.
			// (config.restoreSkillDiscoveryDefaults already heals Enabled/BaseURL
			// for configs that never explicitly disabled ClawHub; this is a
			// belt-and-suspenders default so an empty URL never yields a broken
			// registry.) An operator who explicitly disabled ClawHub keeps
			// Enabled=false and ClawHub is skipped downstream.
			clawHubBaseURL := clawHubConfig.BaseURL
			if clawHubBaseURL == "" {
				clawHubBaseURL = "https://clawhub.ai"
			}

			registryMgr := skills.NewRegistryManagerFromConfig(skills.RegistryConfig{
				MaxConcurrentSearches: cfg.Tools.Skills.MaxConcurrentSearches,
				ClawHub: skills.ClawHubConfig{
					Enabled:         clawHubConfig.Enabled,
					BaseURL:         clawHubBaseURL,
					AuthToken:       os.Getenv(clawHubConfig.AuthTokenRef),
					SearchPath:      clawHubConfig.SearchPath,
					SkillsPath:      clawHubConfig.SkillsPath,
					DownloadPath:    clawHubConfig.DownloadPath,
					Timeout:         clawHubConfig.Timeout,
					MaxZipSize:      clawHubConfig.MaxZipSize,
					MaxResponseSize: clawHubConfig.MaxResponseSize,
				},
				GitHubRegistries: githubRegistries,
			})

			searchCache := skills.NewSearchCache(
				cfg.Tools.Skills.SearchCache.MaxSize,
				time.Duration(cfg.Tools.Skills.SearchCache.TTLSeconds)*time.Second,
			)
			agent.Tools.Register(tools.NewFindSkillsTool(registryMgr, searchCache))
			agent.Tools.Register(tools.NewInstallSkillTool(registryMgr, agent.Workspace))
		}

		// Spawn, spawn_status, and subagent share a SubagentManager. All three
		// are registered unconditionally — the subagent→spawn coupling is a
		// semantic invariant, not a user-visible toggle.
		{
			subagentManager := tools.NewSubagentManager(provider, agent.Model, agent.Workspace)
			subagentManager.SetLLMOptions(agent.MaxTokens, agent.Temperature)

			// Set the spawner that links into AgentLoop's turnState
			subagentManager.SetSpawner(func(
				ctx context.Context,
				task, label, targetAgentID string,
				tls *tools.ToolRegistry,
				maxTokens int,
				temperature float64,
				hasMaxTokens, hasTemperature bool,
			) (*tools.ToolResult, error) {
				// 1. Recover parent Turn State from Context
				parentTS := turnStateFromContext(ctx)
				if parentTS == nil {
					// Fallback: If no turnState exists in context, create an isolated ad-hoc root turn state
					// so that the tool can still function outside of an agent loop (e.g. tests, raw invocations).
					// M2: log a warning when no real turnState is in context — this usually
					// means spawn was called outside of an agent loop (e.g. tests or raw
					// invocations). The ad-hoc state is functional but has no session.
					logger.WarnCF("agent", "Spawn callback using ad-hoc turnState: no parent turnState in context", nil)
					// Drive the ad-hoc semaphore capacity from the resolved MaxParallelAgents
					// value (FR-6.6) so that even out-of-loop spawn calls respect the
					// configured fan-out ceiling instead of the former hardcoded 5.
					adHocSemCap := al.getSubTurnConfig().maxConcurrent
					parentTS = &turnState{
						ctx:            ctx,
						turnID:         "adhoc-root",
						depth:          0,
						session:        nil, // Ephemeral session not needed for adhoc spawn
						pendingResults: make(chan *tools.ToolResult, 16),
						concurrencySem: make(chan struct{}, adHocSemCap),
					}
				}

				// 2. Build Tools slice from registry
				var tlSlice []tools.Tool
				for _, name := range tls.List() {
					if t, ok := tls.Get(name); ok {
						tlSlice = append(tlSlice, t)
					}
				}

				// 3. Resolve Model
				modelToUse := agent.Model
				if targetAgentID != "" {
					if targetAgent, ok := al.GetRegistry().GetAgent(targetAgentID); ok {
						modelToUse = targetAgent.Model
					}
				}

				// 4. Build SubTurnConfig. The task is the first USER message; the
				//    delegate's soul (worker / configured agent) is resolved inside
				//    spawnSubTurn and used as the system role. The legacy
				//    "You are a subagent" wrapper is REMOVED — workers and
				//    configured agents now expose their own persona, and a worker
				//    with an empty soul runs with an empty system role (soul is
				//    OPTIONAL). The label, when set, is preserved as the task label
				//    for the WS subTurn_start frame.
				cfg := SubTurnConfig{
					Model:         modelToUse,
					Tools:         tlSlice,
					SystemPrompt:  task,
					TargetAgentID: targetAgentID,
					TaskLabel:     label,
				}
				if hasMaxTokens {
					cfg.MaxTokens = maxTokens
				}

				// 5. Spawn SubTurn
				return spawnSubTurn(ctx, al, parentTS, cfg)
			})

			// Clone the parent's tool registry so subagents can use all
			// tools registered so far (file, web, etc.) but NOT spawn/
			// spawn_status which are added below — preventing recursive
			// subagent spawning.
			subagentManager.SetTools(agent.Tools.Clone())
			spawnTool := tools.NewSpawnTool(subagentManager)
			spawnTool.SetSpawner(NewSubTurnSpawner(al))
			currentAgentID := agentID
			// spawnTool: repointed to unified DelegationPolicy.To (FR-6.3).
			// Falls back to SubagentsConfig.AllowAgents via CanSpawnSubagent
			// when DelegationPolicy is nil (backward compat, no silent widening).
			spawnAgentCfg := findAgentConfig(cfg, currentAgentID)
			spawnTool.SetAllowlistChecker(func(targetAgentID string) bool {
				toList := config.ResolveDelegationTo(spawnAgentCfg, cfg.Agents.Defaults)
				if toList != nil {
					// Canonical unified policy is set — use it.
					return config.IsDelegationAllowed(toList, targetAgentID)
				}
				// Unified policy not set for this agent — fall back to
				// SubagentsConfig.AllowAgents (legacy path, deny-by-default preserved).
				return registry.CanSpawnSubagent(currentAgentID, targetAgentID)
			})
			// FR-6.2: full-policy gate — trust set + mode("background") + depth.
			// Takes precedence over the allowlist checker and surfaces a reason.
			spawnTool.SetDelegationDenyChecker(buildDelegationDenyChecker(
				currentAgentID, spawnAgentCfg, cfg.Agents.Defaults,
				config.DelegationModeBackground, registry,
			))

			agent.Tools.Register(spawnTool)

			// Also register the synchronous subagent tool.
			// Gate: uses the unified DelegationPolicy.To via IsDelegationAllowedAny
			// (sync subagent has no explicit target; the check is "can delegate at all").
			subagentTool := tools.NewSubagentTool(subagentManager)
			subagentTool.SetSpawner(NewSubTurnSpawner(al))
			// FR-6.3: gate the previously-ungated sync subagent tool.
			subagentAgentCfg := spawnAgentCfg // same agent, captured once
			subagentTool.SetDelegateChecker(func() bool {
				toList := config.ResolveDelegationTo(subagentAgentCfg, cfg.Agents.Defaults)
				if toList != nil {
					// Canonical policy present — allowed only if at least one target is permitted.
					return config.IsDelegationAllowedAny(toList)
				}
				// No canonical policy — fall back to SubagentsConfig.AllowAgents existence.
				// If AllowAgents is non-nil (even empty), the operator set a spawn policy;
				// treat non-nil as opt-in allowed (AllowAgents nil → deny, per legacy semantics).
				if subagentAgentCfg != nil && subagentAgentCfg.Subagents != nil {
					return subagentAgentCfg.Subagents.AllowAgents != nil
				}
				return false
			})
			// FR-6.2: full-policy gate for the synchronous "await" mode. The sync
			// subagent tool has no explicit target, so the trust check is "can
			// delegate at all"; mode + depth still apply.
			subagentTool.SetDelegationDenyChecker(buildSubagentDelegationDenyChecker(
				subagentAgentCfg, cfg.Agents.Defaults,
			))
			agent.Tools.Register(subagentTool)

			agent.Tools.Register(tools.NewSpawnStatusTool(subagentManager))
		}

		// Task tools — require a task store (available after first NewAgentLoop call).
		if al.taskStore != nil {
			currentAgentID := agentID
			agentCfg := findAgentConfig(cfg, currentAgentID)

			agent.Tools.Register(tools.NewTaskListTool(al.taskStore))

			taskCreate := tools.NewTaskCreateTool(al.taskStore)
			taskCreate.SetDelegateChecker(buildDelegateChecker(agentCfg, cfg.Agents.Defaults))
			// FR-6.2: full-policy gate — trust set + mode("task") + depth.
			// Takes precedence over the boolean delegate checker.
			taskCreate.SetDelegationDenyChecker(buildDelegationDenyChecker(
				currentAgentID, agentCfg, cfg.Agents.Defaults,
				config.DelegationModeTask, registry,
			))
			agent.Tools.Register(taskCreate)

			taskUpdate := tools.NewTaskUpdateTool(al.taskStore)
			if al.taskExecutor != nil {
				taskUpdate.SetOnComplete(al.taskExecutor.onTaskComplete)
			}
			agent.Tools.Register(taskUpdate)

			agent.Tools.Register(tools.NewTaskDeleteTool(al.taskStore))
			agent.Tools.Register(tools.NewAgentListTool(func() []tools.AgentInfo {
				var infos []tools.AgentInfo
				for _, id := range registry.ListAgentIDs() {
					if a, ok := registry.GetAgent(id); ok {
						infos = append(infos, tools.AgentInfo{ID: a.ID, Name: a.Name, Type: "custom"})
					}
				}
				return infos
			}))
		}

		// Browser automation tools (US-4/US-6/US-7).
		// Tools are always registered; whether an agent can actually invoke them
		// is determined by the policy engine. Chromium presence is checked lazily
		// at first use and produces a clear error if missing.
		// browser.evaluate is denied by default via builtinToolPolicies in pkg/policy.
		{
			browserCfg, cfgErr := browser.DefaultConfig()
			if cfgErr != nil {
				logger.ErrorCF("agent", "Browser tools: cannot determine defaults — skipping",
					map[string]any{"error": cfgErr.Error()})
			} else {
				// DefaultConfig sets Headless=true; only override if config explicitly sets fields.
				if cfg.Tools.Browser.CDPURL != "" {
					browserCfg.CDPURL = cfg.Tools.Browser.CDPURL
				}
				if cfg.Tools.Browser.PageTimeoutSec > 0 {
					browserCfg.PageTimeout = time.Duration(cfg.Tools.Browser.PageTimeoutSec) * time.Second
				}
				if cfg.Tools.Browser.MaxTabs > 0 {
					browserCfg.MaxTabs = cfg.Tools.Browser.MaxTabs
				}
				if cfg.Tools.Browser.ProfileDir != "" {
					browserCfg.ProfileDir = cfg.Tools.Browser.ProfileDir
				}
				browserCfg.PersistSession = cfg.Tools.Browser.PersistSession

				// Use the singleton SSRF checker (built from config, honors
				// allow_internal). Fall back to a default checker (no allowlist)
				// when SSRF is not explicitly enabled — browser tools always
				// require a non-nil SSRFChecker (see browser.NewBrowserManager).
				browserSSRF := al.ssrfChecker
				if browserSSRF == nil {
					browserSSRF = security.NewSSRFChecker(nil)
				}
				// browser.evaluate registration: always register the tool so the
				// LLM sees it in its tool list. The safety floor (deny by default)
				// is enforced at dispatch time by pkg/policy.builtinToolPolicies
				// (SEC-04/SEC-06). BrowserEvaluateEnabled=true is still required
				// as an explicit operator opt-in for the tool to actually execute;
				// the policy gate provides a second, independent deny layer.
				evaluateEnabled := cfg.Sandbox.BrowserEvaluateEnabled
				mgr, regErr := browser.RegisterTools(agent.Tools, browserCfg, browserSSRF, evaluateEnabled)
				if regErr != nil {
					logger.ErrorCF("agent", "Failed to register browser tools — "+
						"ensure Chromium/Chrome is installed or set tools.browser.cdp_url",
						map[string]any{"error": regErr.Error()})
				} else {
					al.mu.Lock()
					if al.browserMgr != nil {
						al.browserMgr.Shutdown()
					}
					al.browserMgr = mgr
					al.mu.Unlock()
				}
			}
		}
	}
}

// findAgentConfig returns the AgentConfig for the given agent ID, or nil if not found.
func findAgentConfig(cfg *config.Config, agentID string) *config.AgentConfig {
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			return &cfg.Agents.List[i]
		}
	}
	return nil
}

// buildDelegateChecker returns a function that checks whether delegation from agentCfg
// to a target agent is allowed.
//
// FR-6.3 (Spec-3 keystone): repointed to the unified DelegationPolicy.To via
// config.ResolveDelegationTo + config.IsDelegationAllowed. The legacy
// CanDelegateTo fields remain as a backward-compat fallback when
// DelegationPolicy is nil (handled inside ResolveDelegationTo).
// No silent authz widening: deny-by-default is preserved when toList is nil.
func buildDelegateChecker(agentCfg *config.AgentConfig, defaults config.AgentDefaults) func(string) bool {
	toList := config.ResolveDelegationTo(agentCfg, defaults)
	return func(targetAgentID string) bool {
		return config.IsDelegationAllowed(toList, targetAgentID)
	}
}

// currentDelegationDepth reports the delegation-chain depth of the turn that is
// about to delegate, read from the turnState carried on ctx. The root user turn
// has depth 0; each nested sub-turn increments it. Returns 0 when no turnState is
// present (e.g. ad-hoc/raw invocations or tests) — a conservative default that
// never spuriously trips the depth cap.
func currentDelegationDepth(ctx context.Context) int {
	if ts := turnStateFromContext(ctx); ts != nil {
		return ts.depth
	}
	return 0
}

// buildDelegationDenyChecker returns the full FR-6.2 delegation gate for a
// targeted delegation tool (spawn = "background", task_create = "task"). It
// enforces, in order, and returns the first violation as a human-readable reason
// (empty string = allowed):
//
//  1. trust set — the target must be permitted by the unified DelegationPolicy.To
//     (with the legacy CanDelegateTo / SubagentsConfig.AllowAgents fallbacks).
//  2. mode      — the tool's delegation mode must be permitted by the policy's
//     Modes list (empty Modes = all allowed; nil policy = unconstrained).
//  3. depth     — the current delegation-chain depth must be below the policy's
//     per-agent Depth cap (0 = uncapped; the global SubTurn.MaxDepth ceiling
//     still applies independently at sub-turn dispatch).
//
// An empty targetAgentID means "no explicit target" (the LLM omitted agent_id);
// the trust check is then skipped here — untargeted spawns resolve to the default
// agent and are gated by the existing allowlist semantics — while mode and depth
// still apply.
func buildDelegationDenyChecker(
	currentAgentID string,
	agentCfg *config.AgentConfig,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
	registry *AgentRegistry,
) func(ctx context.Context, targetAgentID string) string {
	policy := config.ResolveDelegationPolicy(agentCfg, defaults)
	toList := config.ResolveDelegationTo(agentCfg, defaults)
	policyDepth := config.ResolveDelegationDepth(policy)

	return func(ctx context.Context, targetAgentID string) string {
		// 1. Trust set (only when an explicit target is named).
		if targetAgentID != "" {
			allowed := false
			if toList != nil {
				allowed = config.IsDelegationAllowed(toList, targetAgentID)
			} else {
				// No canonical/legacy To — fall back to the registry allowlist.
				allowed = registry.CanSpawnSubagent(currentAgentID, targetAgentID)
			}
			if !allowed {
				logger.WarnCF("agent", "delegation denied: target not in trust set", map[string]any{
					"agent_id": currentAgentID, "target": targetAgentID, "mode": string(mode),
				})
				return fmt.Sprintf("agent %q is not in this agent's delegation trust set ('to' allowlist)", targetAgentID)
			}
		}

		// 2. Mode.
		if !config.IsDelegationModeAllowed(policy, mode) {
			logger.WarnCF("agent", "delegation denied: mode not permitted", map[string]any{
				"agent_id": currentAgentID, "target": targetAgentID, "mode": string(mode),
			})
			return fmt.Sprintf("delegation mode %q is not permitted by this agent's delegation policy", string(mode))
		}

		// 3. Depth.
		if policyDepth > 0 {
			if d := currentDelegationDepth(ctx); d >= policyDepth {
				logger.WarnCF("agent", "delegation denied: max delegation depth exceeded", map[string]any{
					"agent_id": currentAgentID, "target": targetAgentID, "mode": string(mode),
					"current_depth": d, "max_depth": policyDepth,
				})
				return fmt.Sprintf("maximum delegation depth (%d) reached — cannot delegate further", policyDepth)
			}
		}

		return ""
	}
}

// buildSubagentDelegationDenyChecker returns the FR-6.2 gate for the synchronous
// subagent tool (mode = "await"). The sync subagent tool has no explicit target,
// so the trust check is "can this agent delegate at all" (at least one permitted
// target), then mode and depth apply identically to the targeted path.
func buildSubagentDelegationDenyChecker(
	agentCfg *config.AgentConfig,
	defaults config.AgentDefaults,
) func(ctx context.Context) string {
	policy := config.ResolveDelegationPolicy(agentCfg, defaults)
	toList := config.ResolveDelegationTo(agentCfg, defaults)
	policyDepth := config.ResolveDelegationDepth(policy)

	return func(ctx context.Context) string {
		// 1. Trust: must be able to delegate at all.
		canDelegate := false
		if toList != nil {
			canDelegate = config.IsDelegationAllowedAny(toList)
		} else if agentCfg != nil && agentCfg.Subagents != nil {
			// Legacy: a non-nil AllowAgents means the operator opted in.
			canDelegate = agentCfg.Subagents.AllowAgents != nil
		}
		if !canDelegate {
			logger.WarnCF("agent", "delegation denied: no permitted targets", map[string]any{
				"mode": string(config.DelegationModeAwait),
			})
			return "no target agent is permitted by this agent's delegation policy ('to' allowlist is empty)"
		}

		// 2. Mode.
		if !config.IsDelegationModeAllowed(policy, config.DelegationModeAwait) {
			logger.WarnCF("agent", "delegation denied: mode not permitted", map[string]any{
				"mode": string(config.DelegationModeAwait),
			})
			return fmt.Sprintf("delegation mode %q is not permitted by this agent's delegation policy", string(config.DelegationModeAwait))
		}

		// 3. Depth.
		if policyDepth > 0 {
			if d := currentDelegationDepth(ctx); d >= policyDepth {
				logger.WarnCF("agent", "delegation denied: max delegation depth exceeded", map[string]any{
					"mode": string(config.DelegationModeAwait), "current_depth": d, "max_depth": policyDepth,
				})
				return fmt.Sprintf("maximum delegation depth (%d) reached — cannot delegate further", policyDepth)
			}
		}

		return ""
	}
}

func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)

	// Wrap the caller's context with a cancel so Stop() can unblock the
	// select without requiring the outer context to be canceled. This
	// replaces the previous 100 ms idle ticker (H4: each wakeup was wasted
	// CPU polling a bool that almost always returns true).
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	al.stopCancel.Store(&runCancel)

	if err := al.ensureHooksInitialized(runCtx); err != nil {
		return err
	}
	if err := al.ensureMCPInitialized(runCtx); err != nil {
		return err
	}

	for {
		select {
		case <-runCtx.Done():
			al.stopSessionWorkers()
			return nil
		case msg, ok := <-al.bus.InboundChan():
			if !ok {
				al.stopSessionWorkers()
				return nil
			}

			// System messages are handled inline in a goroutine (no scope).
			if msg.Channel == "system" {
				// Track in activeRequests so graceful shutdown's
				// WaitForActiveRequests drains this turn before teardown —
				// otherwise its cost.json / session-context writes can outlive
				// RunContext and race temp-dir cleanup (#265, macOS APFS).
				al.activeRequests.Add(1)
				go func() {
					defer al.activeRequests.Done()
					defer func() {
						if r := recover(); r != nil {
							logger.ErrorCF("agent", "Panic in system-message goroutine",
								map[string]any{
									"panic":   r,
									"channel": msg.Channel,
									"chat_id": msg.ChatID,
								})
						}
					}()
					if _, err := al.processSystemMessage(runCtx, msg); err != nil {
						logger.WarnCF("agent", "processSystemMessage returned error",
							map[string]any{
								"channel": msg.Channel,
								"chat_id": msg.ChatID,
								"error":   err.Error(),
							})
					}
				}()
				continue
			}

			scope, _, ok := al.resolveSteeringTarget(msg)
			if !ok {
				// Unroutable — fall through to the original single-shot path so
				// channels with no configured agent still get an error reply.
				// Tracked in activeRequests so shutdown drains it (#265).
				al.activeRequests.Add(1)
				go func() {
					defer al.activeRequests.Done()
					defer func() {
						if r := recover(); r != nil {
							logger.ErrorCF("agent", "Panic in unroutable-message goroutine",
								map[string]any{
									"panic":   r,
									"channel": msg.Channel,
									"chat_id": msg.ChatID,
								})
						}
					}()
					response, ag, err := al.processMessage(runCtx, msg)
					if err != nil && response == "" {
						response = fmt.Sprintf("Error processing message: %v", err)
					}
					if response != "" {
						al.publishResponseIfNeeded(runCtx, ag, msg.Channel, msg.ChatID, response)
					}
				}()
				continue
			}

			// If a worker already exists for this scope AND is not in the
			// middle of exiting, enqueue into it. The exiting check closes
			// the silent-drop race (pass-2 silent-failure-hunter N1) where
			// the dispatcher Load'd a worker whose idleTimer had already
			// fired but whose deferred sessionWorkers.Delete had not yet
			// run — enqueue into the dying worker's inbox would never be
			// drained. When exiting=true we fall through to the spawn path
			// below, which will create a fresh worker.
			if existing, ok := al.sessionWorkers.Load(scope); ok {
				if w := existing.(*sessionWorker); !w.exiting.Load() {
					w.enqueue(msg)
					continue
				}
				// Dying worker — fall through to spawn replacement.
			}

			// No worker yet — atomically claim an admission slot for this scope.
			// TryAdmit returns (true, release) when admitted; (false, nil) when at cap.
			// Using TryAdmit rather than a separate ShouldAdmit+OnTurnStart pair
			// closes the TOCTOU window where two concurrent dispatchers both pass
			// the check and overshoot the cap.
			admitted, release := al.admission.TryAdmit(scope)
			if !admitted {
				logger.WarnCF("agent", "At capacity — rejecting new session",
					map[string]any{
						"scope":    scope,
						"active":   al.admission.ActiveScopes(),
						"soft_cap": al.admission.SoftCap(),
						"channel":  msg.Channel,
						"chat_id":  msg.ChatID,
					})
				// Send user-visible capacity reply.
				rejectCtx, rejectCancel := context.WithTimeout(runCtx, 3*time.Second)
				if pubErr := al.bus.PublishOutbound(rejectCtx, bus.OutboundMessage{
					Channel: msg.Channel,
					ChatID:  msg.ChatID,
					Content: "I'm at capacity right now — please try again in a few seconds.",
				}); pubErr != nil {
					logger.WarnCF("agent", "Failed to send capacity-rejection reply",
						map[string]any{"channel": msg.Channel, "error": pubErr.Error()})
				}
				rejectCancel()
				continue
			}

			// Spawn a new worker for this scope. The worker holds the admission
			// slot via release() and calls it in its deferred runLoop cleanup.
			w := newSessionWorker(scope, al, release)
			al.sessionWorkers.Store(scope, w)
			go w.runLoop()
			w.enqueue(msg)
		}
	}
}

// stopSessionWorkers cancels all active session workers and waits for each
// to drain, with a 5 s per-worker budget. Called when Run() exits.
// Idempotent — safe even if Run() already called it.
func (al *AgentLoop) stopSessionWorkers() {
	const workerShutdownBudget = 5 * time.Second

	// Collect first, then cancel — avoids holding sync.Map's range lock
	// while canceling (which could deadlock against concurrent Store calls).
	var workers []*sessionWorker
	al.sessionWorkers.Range(func(_, v any) bool {
		workers = append(workers, v.(*sessionWorker))
		return true
	})

	for _, w := range workers {
		w.cancel()
	}

	for _, w := range workers {
		select {
		case <-w.done:
		case <-time.After(workerShutdownBudget):
			logger.WarnCF("agent", "Session worker did not drain within shutdown budget",
				map[string]any{"scope": w.scope})
		}
	}
}

func (al *AgentLoop) Stop() {
	al.running.Store(false)
	// Cancel the Run context so the select wakes immediately rather than
	// waiting for the next inbound message. Safe to call before Run (the
	// atomic.Pointer is nil until Run stores a cancel func).
	if fn := al.stopCancel.Load(); fn != nil {
		(*fn)()
	}
}

func (al *AgentLoop) publishResponseIfNeeded(ctx context.Context, ag *AgentInstance, channel, chatID, response string) {
	if response == "" {
		return
	}

	alreadySent := false
	if ag == nil {
		ag = al.GetRegistry().GetDefaultAgent()
	}
	if ag != nil {
		if tool, ok := ag.Tools.Get("message"); ok {
			if mt, ok := tool.(*tools.MessageTool); ok {
				alreadySent = mt.HasSentInRound()
			}
		}
	}

	if alreadySent {
		logger.DebugCF(
			"agent",
			"Skipped outbound (message tool already sent)",
			map[string]any{"channel": channel},
		)
		return
	}

	if err := al.bus.PublishOutbound(ctx, bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: response,
	}); err != nil {
		logger.ErrorCF("agent", "Failed to publish outbound response",
			map[string]any{"channel": channel, "chat_id": chatID, "error": err.Error()})
		return
	}
	logger.InfoCF("agent", "Published outbound response",
		map[string]any{
			"channel":     channel,
			"chat_id":     chatID,
			"content_len": len(response),
		})
}

func (al *AgentLoop) buildContinuationTarget(msg bus.InboundMessage) (*continuationTarget, error) {
	if msg.Channel == "system" {
		return nil, nil
	}

	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		return nil, err
	}

	return &continuationTarget{
		SessionKey: resolveScopeKey(route, msg.SessionKey),
		Channel:    msg.Channel,
		ChatID:     msg.ChatID,
	}, nil
}

// WaitForActiveRequests blocks until all in-flight LLM calls tracked by
// activeRequests have completed. Used by the graceful shutdown sequence to
// ensure active turns finish before the process exits.
func (al *AgentLoop) WaitForActiveRequests() {
	al.activeRequests.Wait()
}

// Close releases resources held by agent session stores. Call after Stop.
func (al *AgentLoop) Close() {
	// #265: stop scheduling new recaps, then drain the in-flight ones FIRST —
	// while the registry, session stores, memory stores, and audit logger they
	// write through are all still live (the teardown below closes them). A recap
	// caught mid-flight completes its summary and finishes writing before we
	// proceed, so nothing writes after Close() returns to race temp-dir cleanup.
	al.recapMu.Lock()
	al.closing = true
	al.recapMu.Unlock()
	al.recapWG.Wait()

	// Cancel all active session workers and wait for them to drain (5 s budget).
	// stopSessionWorkers is idempotent — safe to call here even if Run() has
	// already called it on context-cancellation, because workers cancel their
	// own context; a double-cancel is a no-op.
	al.stopSessionWorkers()

	// Shutdown the browser manager (if any) to kill the Chromium subprocess.
	al.mu.Lock()
	if al.browserMgr != nil {
		al.browserMgr.Shutdown()
		al.browserMgr = nil
	}
	al.mu.Unlock()

	mcpManager := al.mcp.takeManager()

	if mcpManager != nil {
		if err := mcpManager.Close(); err != nil {
			logger.ErrorCF("agent", "Failed to close MCP manager",
				map[string]any{
					"error": err.Error(),
				})
		}
	}

	al.GetRegistry().Close()
	if al.hooks != nil {
		al.hooks.Close()
	}
	if al.eventBus != nil {
		al.eventBus.Close()
	}

	// SEC-28: Stop the exec SSRF proxy (idle auto-stop may have already
	// stopped it, but Stop() is idempotent and safe to call either way).
	if al.execProxy != nil {
		al.execProxy.Stop()
	}

	// Lane S (FR-025): cancel all outstanding idle tickers on shutdown.
	al.idleTickers.Range(func(k, v any) bool {
		v.(context.CancelFunc)()
		al.idleTickers.Delete(k)
		return true
	})

	// SEC-26: Persist the accumulated daily cost so the next startup can
	// restore it via LoadIntoRegistry, preventing double-counting on restarts.
	// A save failure here means the cap will under-count after the next
	// restart — worth an Error-level log plus the daily total so operators
	// can reconcile manually.
	if al.costTracker != nil && al.rateLimiter != nil {
		if err := al.costTracker.SaveFromRegistry(al.rateLimiter); err != nil {
			logger.ErrorCF(
				"agent",
				"SEC-26: failed to persist daily cost on shutdown — cap may under-count after restart",
				map[string]any{
					"error":          err.Error(),
					"daily_cost_usd": al.rateLimiter.GetDailyCost(),
				},
			)
		}
	}

	// FR-048: On graceful shutdown, write turn_canceled_restart synthetic entries
	// to any sessions that have active turns paused awaiting approval. This makes
	// the restart visible to the session on next load, preventing the user from
	// seeing a dangling tool_call with no result.
	al.writeTurnCancelledRestartForActiveTurns()

	// SEC-15: Log shutdown event and close audit logger.
	if al.auditLogger != nil {
		// CRIT-6 + typed-Decision migration: route through audit.EmitEntry so
		// a Log failure bumps the audit-skipped counter, and use the typed
		// Decision constant in place of the raw "allow" literal.
		audit.EmitEntry(al.auditLogger, &audit.Entry{
			Event:    audit.EventShutdown,
			Decision: audit.DecisionAllow,
		})
		if err := al.auditLogger.Close(); err != nil {
			logger.ErrorCF("agent", "Failed to close audit logger",
				map[string]any{"error": err.Error()})
		}
	}
}

// writeTurnCancelledRestartForActiveTurns writes a synthetic turn_canceled_restart
// system message to every session that has an active (in-progress) turn at the time
// of graceful shutdown (FR-048). This ensures the session transcript is clean on
// next load: the SIGKILL recovery path (FR-069) will detect the synthetic entry
// and not attempt to resume the canceled turn.
func (al *AgentLoop) writeTurnCancelledRestartForActiveTurns() {
	al.activeTurnStates.Range(func(key, value any) bool {
		sessionKey, _ := key.(string)
		ts, _ := value.(*turnState)
		if ts == nil || ts.agent == nil || ts.agent.Sessions == nil {
			return true
		}

		// Append a synthetic system message documenting the shutdown.
		syntheticContent := fmt.Sprintf(
			`{"type":"turn_canceled_restart","session_key":%q,"reason":"graceful_shutdown"}`,
			sessionKey,
		)
		ts.agent.Sessions.AddMessage(sessionKey, "system", syntheticContent)
		if err := ts.agent.Sessions.Save(sessionKey); err != nil {
			logger.WarnCF("agent", "FR-048: failed to persist turn_canceled_restart on shutdown",
				map[string]any{"session_key": sessionKey, "error": err.Error()})
		} else {
			logger.InfoCF("agent", "FR-048: turn_canceled_restart written on graceful shutdown",
				map[string]any{"session_key": sessionKey})
		}

		// Emit audit event (FR-048).
		// CRIT-6 + typed-Decision/Event migration: route through audit.EmitEntry
		// so Log failure bumps the audit-skipped counter; use typed Event +
		// Decision constants in place of raw string literals.
		audit.EmitEntry(al.auditLogger, &audit.Entry{
			Event:     audit.EventToolPolicyAskDenied,
			Decision:  audit.DecisionDeny,
			SessionID: sessionKey,
			Details: map[string]any{
				"reason":   "restart",
				"turn_id":  ts.turnID,
				"agent_id": ts.agentID,
				"shutdown": "graceful",
			},
		})
		return true
	})
}

// MountHook registers an in-process hook on the agent loop.
func (al *AgentLoop) MountHook(reg HookRegistration) error {
	if al == nil || al.hooks == nil {
		return fmt.Errorf("hook manager is not initialized")
	}
	return al.hooks.Mount(reg)
}

// UnmountHook removes a previously registered in-process hook.
func (al *AgentLoop) UnmountHook(name string) {
	if al == nil || al.hooks == nil {
		return
	}
	al.hooks.Unmount(name)
}

// SubscribeEvents registers a subscriber for agent-loop events.
func (al *AgentLoop) SubscribeEvents(buffer int) EventSubscription {
	if al == nil || al.eventBus == nil {
		ch := make(chan Event)
		close(ch)
		return EventSubscription{C: ch}
	}
	return al.eventBus.Subscribe(buffer)
}

// UnsubscribeEvents removes a previously registered event subscriber.
func (al *AgentLoop) UnsubscribeEvents(id uint64) {
	if al == nil || al.eventBus == nil {
		return
	}
	al.eventBus.Unsubscribe(id)
}

// EventDrops returns the number of dropped events for the given kind.
func (al *AgentLoop) EventDrops(kind EventKind) int64 {
	if al == nil || al.eventBus == nil {
		return 0
	}
	return al.eventBus.Dropped(kind)
}

type turnEventScope struct {
	agentID    string
	sessionKey string
	turnID     string
}

func (al *AgentLoop) newTurnEventScope(agentID, sessionKey string) turnEventScope {
	seq := al.turnSeq.Add(1)
	return turnEventScope{
		agentID:    agentID,
		sessionKey: sessionKey,
		turnID:     fmt.Sprintf("%s-turn-%d", agentID, seq),
	}
}

func (ts turnEventScope) meta(iteration int, source, tracePath string) EventMeta {
	return EventMeta{
		AgentID:    ts.agentID,
		TurnID:     ts.turnID,
		SessionKey: ts.sessionKey,
		Iteration:  iteration,
		Source:     source,
		TracePath:  tracePath,
	}
}

func (al *AgentLoop) emitEvent(kind EventKind, meta EventMeta, payload any) {
	evt := Event{
		Kind:    kind,
		Meta:    meta,
		Payload: payload,
	}

	if al == nil || al.eventBus == nil {
		return
	}

	al.logEvent(evt)

	al.eventBus.Emit(evt)
}

// EmitWhatsAppPairing publishes a WhatsApp native/QR pairing update (QR code or
// status) onto the event bus so every connected SPA WebSocket client receives a
// whatsapp_pairing frame (#283). Safe to call from a channel's own goroutine —
// the bus drops to a full subscriber rather than blocking. Wired into the
// WhatsApp native channel at gateway boot via SetPairingObserver.
func (al *AgentLoop) EmitWhatsAppPairing(channelID string, status channels.PairingStatus, qr, message string) {
	al.emitEvent(EventKindWhatsAppPairing, EventMeta{Source: "channel"}, WhatsAppPairingPayload{
		ChannelID: channelID,
		Status:    status,
		QR:        qr,
		Message:   message,
	})
	// FR-111 (#358): audit-log device-pairing lifecycle transitions so linking a new
	// WhatsApp device leaves a tamper-evident trail. We deliberately do NOT log the
	// `code`/`connecting` states (high-frequency, and the QR itself is a scannable
	// secret that must never reach the audit file) — only the terminal outcomes.
	// Decision uses the audit vocabulary (allow=linked, error=failed/expired); the
	// exact pairing status rides in Details.
	switch status {
	case channels.PairingStatusLinked, channels.PairingStatusError, channels.PairingStatusTimeout:
		decision := audit.DecisionAllow
		if status != channels.PairingStatusLinked {
			decision = audit.DecisionError
		}
		audit.EmitEntry(al.auditLogger, &audit.Entry{
			Timestamp: time.Now().UTC(),
			Event:     audit.EventChannelPairing,
			Decision:  decision,
			Details: map[string]any{
				"channel": channelID,
				"status":  string(status),
				"message": message,
			},
		})
	}
}

// EmitNotification publishes a user-facing notification onto the event bus so
// the recipient's SPA WebSocket connections receive a notification frame (#264).
// The WS forwarder filters delivery by Recipient (==wsConn.userID), so the
// payload is not broadcast to every authenticated tab. Safe to call from any
// goroutine — the bus drops to a full subscriber rather than blocking.
func (al *AgentLoop) EmitNotification(p NotificationPayload) {
	al.emitEvent(EventKindNotification, EventMeta{Source: "schedule"}, p)
}

func cloneEventArguments(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(args))
	for k, v := range args {
		cloned[k] = v
	}
	return cloned
}

func (al *AgentLoop) hookAbortError(ts *turnState, stage string, decision HookDecision) error {
	reason := decision.Reason
	if reason == "" {
		reason = "hook requested turn abort"
	}

	err := fmt.Errorf("hook aborted turn during %s: %s", stage, reason)
	al.emitEvent(
		EventKindError,
		ts.eventMeta("hooks", "turn.error"),
		ErrorPayload{
			Stage:   "hook." + stage,
			Message: err.Error(),
		},
	)
	// FIX-2 / US-1: persist the hook abort to the JSONL transcript so the
	// replay path re-renders it after page reload (see appendErrorTranscript
	// docstring). Without this, hook aborts vanish on session reopen.
	ts.appendErrorTranscript(
		EventKindError.String(), "hooks",
		err.Error(),
	)
	return err
}

func hookDeniedToolContent(prefix, reason string) string {
	if reason == "" {
		return prefix
	}
	return prefix + ": " + reason
}

func (al *AgentLoop) logEvent(evt Event) {
	fields := map[string]any{
		"event_kind":  evt.Kind.String(),
		"agent_id":    evt.Meta.AgentID,
		"turn_id":     evt.Meta.TurnID,
		"session_key": evt.Meta.SessionKey,
		"iteration":   evt.Meta.Iteration,
	}

	if evt.Meta.TracePath != "" {
		fields["trace"] = evt.Meta.TracePath
	}
	if evt.Meta.Source != "" {
		fields["source"] = evt.Meta.Source
	}

	switch payload := evt.Payload.(type) {
	case TurnStartPayload:
		fields["channel"] = payload.Channel
		fields["chat_id"] = payload.ChatID
		fields["user_len"] = len(payload.UserMessage)
		fields["media_count"] = payload.MediaCount
	case TurnEndPayload:
		fields["status"] = payload.Status
		fields["iterations_total"] = payload.Iterations
		fields["duration_ms"] = payload.Duration.Milliseconds()
		fields["final_len"] = payload.FinalContentLen
	case LLMRequestPayload:
		fields["model"] = payload.Model
		fields["messages"] = payload.MessagesCount
		fields["tools"] = payload.ToolsCount
		fields["max_tokens"] = payload.MaxTokens
	case LLMDeltaPayload:
		fields["content_delta_len"] = payload.ContentDeltaLen
		fields["reasoning_delta_len"] = payload.ReasoningDeltaLen
	case LLMResponsePayload:
		fields["content_len"] = payload.ContentLen
		fields["tool_calls"] = payload.ToolCalls
		fields["has_reasoning"] = payload.HasReasoning
	case LLMRetryPayload:
		fields["attempt"] = payload.Attempt
		fields["max_retries"] = payload.MaxRetries
		fields["reason"] = payload.Reason
		fields["error"] = payload.Error
		fields["backoff_ms"] = payload.Backoff.Milliseconds()
	case ContextCompressPayload:
		fields["reason"] = payload.Reason
		fields["dropped_messages"] = payload.DroppedMessages
		fields["remaining_messages"] = payload.RemainingMessages
	case SessionSummarizePayload:
		fields["summarized_messages"] = payload.SummarizedMessages
		fields["kept_messages"] = payload.KeptMessages
		fields["summary_len"] = payload.SummaryLen
		fields["omitted_oversized"] = payload.OmittedOversized
	case ToolExecStartPayload:
		fields["tool"] = payload.Tool
		fields["args_count"] = len(payload.Arguments)
	case ToolExecEndPayload:
		fields["tool"] = payload.Tool
		fields["duration_ms"] = payload.Duration.Milliseconds()
		fields["for_llm_len"] = payload.ForLLMLen
		fields["for_user_len"] = payload.ForUserLen
		fields["is_error"] = payload.IsError
		fields["async"] = payload.Async
	case ToolExecSkippedPayload:
		fields["tool"] = payload.Tool
		fields["reason"] = payload.Reason
	case SteeringInjectedPayload:
		fields["count"] = payload.Count
		fields["total_content_len"] = payload.TotalContentLen
	case FollowUpQueuedPayload:
		fields["source_tool"] = payload.SourceTool
		fields["channel"] = payload.Channel
		fields["chat_id"] = payload.ChatID
		fields["content_len"] = payload.ContentLen
	case InterruptReceivedPayload:
		fields["interrupt_kind"] = payload.Kind
		fields["role"] = payload.Role
		fields["content_len"] = payload.ContentLen
		fields["queue_depth"] = payload.QueueDepth
		fields["hint_len"] = payload.HintLen
	case SubTurnSpawnPayload:
		fields["child_agent_id"] = payload.AgentID
		fields["label"] = payload.Label
	case SubTurnEndPayload:
		fields["child_agent_id"] = payload.AgentID
		fields["status"] = payload.Status
	case SubTurnResultDeliveredPayload:
		fields["target_channel"] = payload.TargetChannel
		fields["target_chat_id"] = payload.TargetChatID
		fields["content_len"] = payload.ContentLen
	case ErrorPayload:
		fields["stage"] = payload.Stage
		fields["error"] = payload.Message
	}

	logger.InfoCF("eventbus", fmt.Sprintf("Agent event: %s", evt.Kind.String()), fields)
}

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	registry := al.GetRegistry()
	for _, agentID := range registry.ListAgentIDs() {
		if agent, ok := registry.GetAgent(agentID); ok {
			agent.Tools.Register(tool)
		}
	}
}

func (al *AgentLoop) SetChannelManager(cm *channels.Manager) {
	al.channelManagerMu.Lock()
	al.channelManager = cm
	al.channelManagerMu.Unlock()
}

// getChannelManager returns the current channel manager under the read lock.
// Internal callers on the hot turn path use this to avoid the N2 data race.
func (al *AgentLoop) getChannelManager() *channels.Manager {
	al.channelManagerMu.RLock()
	cm := al.channelManager
	al.channelManagerMu.RUnlock()
	return cm
}

// GetChannelManager returns the current channel manager under the read lock
// (may be nil before channels start, e.g. during onboarding). Exported for
// pkg/gateway: REST handlers inspect runtime channel state (e.g. FailedChannels),
// and the scheduled runner validates that a deliver=true target channel is
// registered before publishing (M2). Set after construction via
// SetChannelManager, so callers must tolerate nil and re-fetch at use time.
func (al *AgentLoop) GetChannelManager() *channels.Manager {
	return al.getChannelManager()
}

// ReloadProviderAndConfig atomically swaps the provider and config with proper synchronization.
// It uses a context to allow timeout control from the caller.
// Returns an error if the reload fails or context is canceled.
func (al *AgentLoop) ReloadProviderAndConfig(
	ctx context.Context,
	provider providers.LLMProvider,
	cfg *config.Config,
) error {
	// Validate inputs
	if provider == nil {
		return fmt.Errorf("provider cannot be nil")
	}
	if cfg == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Create new registry with updated config and provider
	// Wrap in defer/recover to handle any panics gracefully
	var registry *AgentRegistry
	var panicErr error
	done := make(chan struct{}, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicErr = fmt.Errorf("panic during registry creation: %v", r)
				logger.ErrorCF("agent", "Panic during registry creation",
					map[string]any{"panic": r})
			}
			close(done)
		}()

		registry = NewAgentRegistry(cfg, provider)
	}()

	// Wait for completion or context cancellation
	select {
	case <-done:
		if registry == nil {
			if panicErr != nil {
				return fmt.Errorf("registry creation failed: %w", panicErr)
			}
			return fmt.Errorf("registry creation failed (nil result)")
		}
	case <-ctx.Done():
		return fmt.Errorf("context canceled during registry creation: %w", ctx.Err())
	}

	// Check context again before proceeding
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context canceled after registry creation: %w", err)
	}

	// Apply configurable default agent override on the new registry.
	if cfg.Agents.Defaults.DefaultAgentID != "" {
		registry.SetDefaultAgentOverride(cfg.Agents.Defaults.DefaultAgentID)
	}

	// Ensure shared tools are re-registered on the new registry
	registerSharedTools(al, cfg, al.bus, registry, provider)

	// Re-wire Tier 1/3 tools (web_serve + workspace.shell*) on the new registry.
	// Without this, hot-reload silently drops them because NewAgentRegistry creates
	// fresh AgentInstances whose Tools registries don't know about the shared
	// ServedSubdirs / DevServerRegistry / EgressProxy singletons.
	if al.tier13Deps != nil {
		al.wireTier13DepsLocked(registry, *al.tier13Deps)
	}

	// Re-wire exec tool deps (sandbox mode + egress proxy) on the new
	// registry. Without this, the rebuilt exec tool would lose the kernel
	// sandbox routing and revert to the legacy `sh -c` path on a hot reload.
	al.wireExecToolDepsOn(registry)

	// Re-wire system.* tools on the new registry (FR-001, FR-002).
	if al.sysagentDeps != nil {
		al.wireSysagentDepsLocked(registry, al.sysagentDeps)
	}

	// Atomically swap the config and registry under write lock
	// This ensures readers see a consistent pair
	al.mu.Lock()
	oldRegistry := al.registry

	// Store new values
	al.cfg = cfg
	al.registry = registry

	// Also update fallback chain with new config
	al.fallback = providers.NewFallbackChainWithTimeout(
		providers.NewCooldownTracker(),
		perCandidateTimeoutFromConfig(cfg),
	)

	al.mu.Unlock()

	al.hookRuntime.reset(al)
	configureHookManagerFromConfig(al.hooks, cfg)

	// Close old provider after releasing the lock
	// This prevents blocking readers while closing
	if oldProvider, ok := extractProvider(oldRegistry); ok {
		if stateful, ok := oldProvider.(providers.StatefulProvider); ok {
			// Give in-flight requests a moment to complete
			// Use a reasonable timeout that balances cleanup vs resource usage
			select {
			case <-time.After(100 * time.Millisecond):
				stateful.Close()
			case <-ctx.Done():
				// Context canceled, close immediately but log warning
				logger.WarnCF("agent", "Context canceled during provider cleanup, forcing close",
					map[string]any{"error": ctx.Err()})
				stateful.Close()
			}
		}
	}

	// Note: oldRegistry is intentionally NOT closed here. Closing it would
	// terminate session stores that may still be in use by in-flight turns.
	// The old registry's resources (session file handles) will be GC'd when
	// no more references exist. This trades a brief fd leak during reload
	// for crash safety.

	logger.InfoCF("agent", "Provider and config reloaded successfully",
		map[string]any{
			"model": cfg.Agents.Defaults.GetModelName(),
		})

	return nil
}

// GetRegistry returns the current registry (thread-safe)
func (al *AgentLoop) GetRegistry() *AgentRegistry {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.registry
}

// GetConfig returns the current config (thread-safe)
func (al *AgentLoop) GetConfig() *config.Config {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.cfg
}

// GetSessionActiveAgent returns the agent that the handoff tool last switched
// the given session to. Returns ("", false) if no handoff override is active
// for this session_id.
func (al *AgentLoop) GetSessionActiveAgent(sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	if v, ok := al.sessionActiveAgent.Load("session:" + sessionID); ok {
		return v.(string), true
	}
	return "", false
}

// SwapConfig atomically replaces the in-memory config with the supplied,
// fully-initialized *config.Config (credentials resolved, sensitive values
// registered). Callers are responsible for calling credentials.ResolveBundle
// and cfg.RegisterSensitiveValues before SwapConfig — this method only does
// the atomic pointer swap.
func (al *AgentLoop) SwapConfig(newCfg *config.Config) {
	al.mu.Lock()
	al.cfg = newCfg
	al.mu.Unlock()
}

// MutateConfig acquires the agent loop write lock and calls fn with the
// live *config.Config pointer. This serializes sysagent mutations with all
// REST readers that go through GetConfig (which holds RLock). fn must not
// call GetConfig or SwapConfig — deadlock would result.
//
// The caller (typically Deps.WithConfig) is responsible for snapshotting and
// rolling back cfg fields if fn or the subsequent SaveConfig fails.
func (al *AgentLoop) MutateConfig(fn func(*config.Config) error) error {
	al.mu.Lock()
	defer al.mu.Unlock()
	if al.cfg == nil {
		return fmt.Errorf("agent loop config is nil")
	}
	return fn(al.cfg)
}

// GetTaskStore returns the shared TaskStore (may be nil in tests).
func GetTaskStore(al *AgentLoop) *taskstore.TaskStore {
	return al.taskStore
}

// ApplyAgentModel switches a live agent instance to a new primary model in
// place — rebuilding its provider, candidate chain, and thinking level under
// the instance lock WITHOUT recreating the instance. This preserves the agent's
// in-memory conversation state and avoids a config hot-reload that would drop
// the WebSocket (#73). The new model must already be persisted to config (the
// REST handler writes config.json first) so resolution observes it. Returns the
// previous primary model. Shared by the switch_model tool and the
// PUT /api/v1/agents/{id} model-change path.
func (al *AgentLoop) ApplyAgentModel(agentID, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	agent, ok := al.registry.GetAgent(agentID)
	if !ok {
		return "", fmt.Errorf("agent %q not found", agentID)
	}

	al.mu.RLock()
	cfg := al.cfg
	al.mu.RUnlock()

	modelCfg, err := resolvedModelConfig(cfg, model, agent.Workspace)
	if err != nil {
		return "", err
	}
	nextProvider, _, err := providers.CreateProviderFromConfig(modelCfg)
	if err != nil {
		return "", fmt.Errorf("failed to initialize model %q: %w", model, err)
	}
	nextCandidates := resolveModelCandidatesForAgent(cfg, cfg.Agents.Defaults.Provider, modelCfg.Model, agent)
	if len(nextCandidates) == 0 {
		return "", fmt.Errorf("model %q did not resolve to any provider candidates", model)
	}

	// FR-007: rebuild the provider pool so the new model switch has every
	// distinct provider pre-built. The agent's existing pool may carry stale
	// entries for the previous primary's provider; rebuilding from the new
	// candidate chain keeps ProviderPool coherent with Candidates.
	newPool := buildProviderPool(cfg, nextCandidates)

	agent.mu.Lock()
	oldModel := agent.Model
	oldProvider := agent.Provider
	agent.Model = model
	agent.Provider = nextProvider
	agent.Candidates = nextCandidates
	agent.ThinkingLevel = parseThinkingLevel(modelCfg.ThinkingLevel)
	// Publish the new pool INSIDE the same lock as the Model + Provider +
	// Candidates flip. The atomic.Pointer in StoreProviderPool would protect
	// the pool's map against concurrent read/write on its own, but an
	// in-flight turn that has just RLock'd agent.mu to read the old Model
	// would then Load() a pool that no longer matches the model — the
	// fallback chain would route through the NEW pool's primary credentials
	// while the model field still says OLD. Holding the lock across the
	// full tuple flip makes (Model, Provider, Candidates, ProviderPool) a
	// single coherent swap from any reader's perspective.
	agent.StoreProviderPool(newPool)
	agent.mu.Unlock()

	// Close the previous provider if it holds resources (e.g. a stateful
	// session) and is actually being replaced.
	if oldProvider != nil && oldProvider != nextProvider {
		if stateful, ok := oldProvider.(providers.StatefulProvider); ok {
			stateful.Close()
		}
	}
	return oldModel, nil
}

// GetTaskExecutor returns the shared TaskExecutor (may be nil in tests).
func GetTaskExecutor(al *AgentLoop) *TaskExecutor {
	return al.taskExecutor
}

// GetSSRFChecker returns the singleton SSRFChecker built from the SSRF policy
// config at startup (SEC-24). Returns nil when SSRF protection is disabled
// (sandbox.ssrf.enabled = false in config.json). Gateway handlers that make
// outbound HTTP calls (e.g. the skills installer) should pass this to their
// HTTP client constructors so allow_internal is honored consistently.
func GetSSRFChecker(al *AgentLoop) *security.SSRFChecker {
	return al.ssrfChecker
}

// GetSessionStore returns the shared UnifiedStore for new sessions. May be nil
// in tests or when the shared sessions directory could not be initialized.
func (al *AgentLoop) GetSessionStore() *session.UnifiedStore {
	return al.sharedSessionStore
}

// GetAgentStore returns the UnifiedStore for a given agent, or nil if not found
// or if the agent's session store is not a UnifiedStore.
// Use GetSessionStore() for creating new sessions; GetAgentStore is kept for
// legacy per-agent session access.
func (al *AgentLoop) GetAgentStore(agentID string) *session.UnifiedStore {
	agent, ok := al.GetRegistry().GetAgent(agentID)
	if !ok {
		return nil
	}
	us, ok := agent.Sessions.(*session.UnifiedStore)
	if !ok {
		logger.WarnCF("agent", "GetAgentStore: session store is not UnifiedStore",
			map[string]any{"agent_id": agentID})
		return nil
	}
	return us
}

// getLegacyAgentStore returns the per-agent UnifiedStore for legacy session
// access. It is an internal alias for GetAgentStore used by ListAllSessions.
func (al *AgentLoop) getLegacyAgentStore(agentID string) *session.UnifiedStore {
	return al.GetAgentStore(agentID)
}

// rebuildChannelSessionIndex populates channelSessionIdx from existing shared sessions.
// Called once after sharedSessionStore is initialized.
func (al *AgentLoop) rebuildChannelSessionIndex() {
	if al.sharedSessionStore == nil {
		return
	}
	sessions, _ := al.sharedSessionStore.ListSessions()
	for _, s := range sessions {
		if s.Channel != "" && s.Channel != "webchat" && s.PeerID != "" {
			al.channelSessionIdx.Store(s.Channel+"/"+s.PeerID, s.ID)
		}
	}
}

// resolveOrCreateChannelSession returns the shared session ID for (channel, chatID),
// creating a new session in the shared store if none exists yet.
// Returns "" if the shared store is unavailable or inputs are empty.
func (al *AgentLoop) resolveOrCreateChannelSession(channel, chatID, agentID, displayName string) string {
	if al.sharedSessionStore == nil || channel == "" || chatID == "" {
		return ""
	}
	key := channel + "/" + chatID
	if v, ok := al.channelSessionIdx.Load(key); ok {
		return v.(string)
	}
	title := displayName
	if title == "" {
		title = chatID
	}
	meta, err := al.sharedSessionStore.NewChannelSession(channel, chatID, agentID, title)
	if err != nil {
		logger.WarnCF("agent", "Failed to create channel session",
			map[string]any{"channel": channel, "chat_id": chatID, "error": err.Error()})
		return ""
	}
	al.channelSessionIdx.Store(key, meta.ID)
	return meta.ID
}

// ResolveSessionStore finds which UnifiedStore owns the given sessionID.
// Checks the shared store first, then the main agent's legacy store, then
// all other per-agent stores. Returns nil if the session cannot be found.
func (al *AgentLoop) ResolveSessionStore(sessionID string) *session.UnifiedStore {
	// Fast path: shared store owns new sessions.
	if al.sharedSessionStore != nil {
		if _, err := al.sharedSessionStore.GetMeta(sessionID); err == nil {
			return al.sharedSessionStore
		}
	}
	// Legacy fast path: main agent owns most old sessions.
	if store := al.GetAgentStore(DefaultAgentID); store != nil {
		if _, err := store.GetMeta(sessionID); err == nil {
			return store
		}
	}
	// Slow path: scan all per-agent stores.
	for _, id := range al.GetRegistry().ListAgentIDs() {
		if id == DefaultAgentID {
			continue
		}
		store := al.GetAgentStore(id)
		if store == nil {
			continue
		}
		if _, err := store.GetMeta(sessionID); err == nil {
			return store
		}
	}
	return nil
}

// ListAllSessions returns sessions from the shared store merged with legacy
// per-agent stores, deduplicated and sorted by UpdatedAt descending.
// The second return value collects per-store errors so callers can distinguish
// "no sessions" from "all stores failed". Callers should surface partial errors
// as warnings rather than treating the entire response as a failure.
func (al *AgentLoop) ListAllSessions() ([]*session.UnifiedMeta, []error) {
	var all []*session.UnifiedMeta
	var errs []error

	// 1. Shared store (new sessions).
	sharedIDs := make(map[string]bool)
	if al.sharedSessionStore != nil {
		shared, err := al.sharedSessionStore.ListSessions()
		if err != nil {
			logger.WarnCF("agent", "ListAllSessions: could not list shared sessions",
				map[string]any{"error": err.Error()})
			errs = append(errs, fmt.Errorf("shared: %w", err))
		} else {
			for _, s := range shared {
				sharedIDs[s.ID] = true
				all = append(all, s)
			}
		}
	}

	// 2. Legacy per-agent stores — deduplicate against shared.
	for _, id := range al.GetRegistry().ListAgentIDs() {
		store := al.getLegacyAgentStore(id)
		if store == nil {
			continue
		}
		sessions, err := store.ListSessions()
		if err != nil {
			logger.WarnCF("agent", "ListAllSessions: could not list sessions for agent",
				map[string]any{"agent_id": id, "error": err.Error()})
			errs = append(errs, fmt.Errorf("agent=%s: %w", id, err))
			continue
		}
		for _, s := range sessions {
			if !sharedIDs[s.ID] {
				all = append(all, s)
			}
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].UpdatedAt.After(all[j].UpdatedAt)
	})
	return all, errs
}

// processTaskDirect runs the agent loop for a task, dispatching to the given agent.
// taskChatID identifies the WebSocket chat for event forwarding (defaults to "task:" + sessionKey).
// Channel is "webchat" for streaming; tool context is "system" so exec/cron tools are permitted.
func (al *AgentLoop) processTaskDirect(
	ctx context.Context,
	agentID, prompt, sessionKey, taskChatID string,
) (string, error) {
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", fmt.Errorf("processTaskDirect: hooks: %w", err)
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", fmt.Errorf("processTaskDirect: mcp: %w", err)
	}

	registry := al.GetRegistry()
	ag, ok := registry.GetAgent(agentID)
	if !ok {
		logger.WarnCF(
			"agent",
			"processTaskDirect: agent not found, using default",
			map[string]any{"requested": agentID},
		)
		ag = registry.GetDefaultAgent()
	}
	if ag == nil {
		return "", fmt.Errorf("processTaskDirect: no agent %q", agentID)
	}

	// Tool context uses "system" channel so exec/cron tools are permitted.
	taskCtx := tools.WithAgentID(ctx, agentID)
	taskCtx = tools.WithToolContext(taskCtx, "system", "")

	if taskChatID == "" {
		taskChatID = "task:" + sessionKey
	}

	return al.runAgentLoop(taskCtx, ag, processOptions{
		SessionKey:          sessionKey,
		Channel:             "webchat",
		ChatID:              taskChatID,
		SenderID:            "task-executor",
		UserMessage:         prompt,
		DefaultResponse:     defaultResponse,
		EnableSummary:       false,
		SendResponse:        false,
		TranscriptSessionID: taskChatID,
		TranscriptStore:     al.GetAgentStore(agentID),
	})
}

// ExecuteBoardTask dispatches a GTD board task to the agent loop in a background
// goroutine. The session must already exist in the per-agent store (via GetAgentStore).
// onComplete is called with the result string and execution error once the agent
// finishes; the caller is responsible for persisting the terminal task status.
//
// Shutdown behavior:
//   - Graceful shutdown (Stop + WaitForActiveRequests): the goroutine is tracked in
//     activeRequests, so WaitForActiveRequests/Close drain it before the process exits
//     and onComplete is called with the cancellation error, transitioning the task to
//     "failed" normally.
//   - Crash / SIGKILL / OOM: the goroutine is abandoned and onComplete never runs,
//     leaving the task persisted with status "active". On next boot,
//     gateway.reconcileStuckBoardTasks scans for any task with status=="active" and
//     resets it to "failed" with a note that the gateway restarted while it was running.
func (al *AgentLoop) ExecuteBoardTask(agentID, taskID, sessionID, prompt string, onComplete func(string, error)) {
	// Board-task goroutines run on context.Background() — they are detached from the
	// HTTP request lifecycle and outlive the Run loop.
	taskCtx := context.Background()

	al.activeRequests.Add(1)
	go func() {
		defer al.activeRequests.Done()
		defer func() {
			if r := recover(); r != nil {
				panicMsg := fmt.Sprintf("%v", r)
				logger.ErrorCF("agent", "ExecuteBoardTask: panic recovered",
					map[string]any{
						"task_id":    taskID,
						"agent_id":   agentID,
						"session_id": sessionID,
						"panic":      panicMsg,
					})
				if onComplete != nil {
					onComplete("", fmt.Errorf("panic: %v", r))
				}
			}
		}()
		sessionKey := fmt.Sprintf("agent:%s:board:%s", agentID, taskID)
		logger.InfoCF("agent", "ExecuteBoardTask: dispatching",
			map[string]any{
				"task_id":     taskID,
				"agent_id":    agentID,
				"session_id":  sessionID,
				"session_key": sessionKey,
			})
		result, err := al.processTaskDirect(taskCtx, agentID, prompt, sessionKey, sessionID)
		if err != nil {
			logger.ErrorCF("agent", "ExecuteBoardTask: execution failed",
				map[string]any{
					"task_id":    taskID,
					"agent_id":   agentID,
					"session_id": sessionID,
					"error":      err.Error(),
				})
		}
		if onComplete != nil {
			onComplete(result, err)
		}
	}()
}

// SetMediaStore injects a MediaStore for media lifecycle management.
func (al *AgentLoop) SetMediaStore(s media.MediaStore) {
	al.mediaStoreMu.Lock()
	al.mediaStore = s
	al.mediaStoreMu.Unlock()

	// Propagate store to all registered tools that can emit media.
	registry := al.GetRegistry()
	for _, agentID := range registry.ListAgentIDs() {
		if agent, ok := registry.GetAgent(agentID); ok {
			agent.Tools.SetMediaStore(s)
		}
	}
}

// GetMediaStore returns the currently injected media store. Callers that serve
// media over HTTP must use this getter (not a cached reference) because the
// store is replaced on every restartServices — a cached pointer goes stale.
func (al *AgentLoop) GetMediaStore() media.MediaStore {
	if al == nil {
		return nil
	}
	al.mediaStoreMu.RLock()
	s := al.mediaStore
	al.mediaStoreMu.RUnlock()
	return s
}

// GetMediaRefsDropped returns the cumulative count of media refs that were
// dropped because they could not be resolved (unknown ref or file missing
// on disk). Safe for concurrent access; incremented on the hot turn path.
func (al *AgentLoop) GetMediaRefsDropped() int64 {
	return al.mediaRefsDropped.Load()
}

// SetTranscriber injects a voice transcriber for agent-level audio transcription.
func (al *AgentLoop) SetTranscriber(t voice.Transcriber) {
	al.transcriber = t
}

// GetTranscriber returns the currently configured voice transcriber, or nil
// when none is configured. The gateway's POST /voice/transcribe handler uses
// this to serve the composer-mic flow with the same transcriber the agent loop
// uses for inbound audio messages (Spec-6 FR-12.1).
func (al *AgentLoop) GetTranscriber() voice.Transcriber {
	return al.transcriber
}

// SetReloadFunc sets the callback function for triggering config reload.
func (al *AgentLoop) SetReloadFunc(fn func() error) {
	al.reloadFunc = fn
}

// SetSysagentDeps stores the system.* tool dependencies for use by hot-reload.
// Call WireSysagentDeps after this to immediately register the tools.
func (al *AgentLoop) SetSysagentDeps(deps *systools.Deps) {
	al.sysagentDeps = deps
}

// SetToolApprover injects the gateway's policy-level approval implementation into
// the loop (FR-011). Must be called before any turns start; safe from any goroutine.
// Passing nil clears the approver (ask-policy tools treated as allow — open gate).
func (al *AgentLoop) SetToolApprover(a PolicyApprover) {
	al.mu.Lock()
	al.toolApprover = a
	al.mu.Unlock()
}

// SetAllowGodMode sets the god-mode opt-in flag (latch 2). Must be called
// before WireTier13Deps so the coercion logic picks up the correct value. If
// called after WireTier13Deps, the change takes effect on the next hot-reload.
func (al *AgentLoop) SetAllowGodMode(allow bool) {
	al.mu.Lock()
	al.allowGodMode = allow
	al.mu.Unlock()
}

// WireSysagentDeps registers all 41 system.* tools on every agent in the
// current registry (FR-001, FR-002). Mirrors the WireTier13Deps pattern:
// called once at boot after NewAgentLoop, and again on hot-reload. The deps
// pointer is stashed so hot-reload can re-apply the wiring on the rebuilt registry.
//
// Per-agent policy (seeded via coreagent.SeedConfig) governs which agents may
// actually invoke these tools at LLM-call time — this registration is the
// supply side; policy is the demand filter.
func (al *AgentLoop) WireSysagentDeps(deps *systools.Deps) {
	depsCopy := *deps
	al.sysagentDeps = &depsCopy
	al.wireSysagentDepsLocked(al.registry, &depsCopy)
}

// wireSysagentDepsLocked registers system.* tools on all agents in registry.
// Factored out so hot-reload can re-apply against a freshly-built registry.
func (al *AgentLoop) wireSysagentDepsLocked(registry *AgentRegistry, deps *systools.Deps) {
	if registry == nil || deps == nil {
		return
	}
	sysToolList := systools.AllTools(deps, nil)
	for _, agentID := range registry.ListAgentIDs() {
		ag, ok := registry.GetAgent(agentID)
		if !ok || ag == nil || ag.Tools == nil {
			continue
		}
		for _, t := range sysToolList {
			ag.Tools.Register(t)
		}
	}
	logger.InfoCF("agent", "system.* tools wired into agent registry",
		map[string]any{"tool_count": len(sysToolList)})
}

// TriggerReload triggers a config reload so the in-memory config picks up
// changes written to disk by safeUpdateConfigJSON. Called by REST handlers
// after persisting config changes (agent create/update, token rotate, etc.).
//
// Concurrency: the underlying reloadFunc (set in gateway.go) is guarded by
// an atomic CompareAndSwap that serializes concurrent calls — only one reload
// can be in flight at a time. A second concurrent call returns an error
// ("reload already in progress") rather than queuing a second reload.
func (al *AgentLoop) TriggerReload() error {
	if al.reloadFunc == nil {
		return ErrReloadNotConfigured
	}
	al.reloadPending.Store(true)
	if err := al.reloadFunc(); err != nil {
		// Only clear the pending flag if this was a genuine failure.
		// If another reload is already in progress, that reload owns the flag —
		// clearing it here would prematurely unblock any concurrent poller.
		if strings.Contains(err.Error(), "already in progress") {
			return ErrReloadAlreadyInProgress
		}
		al.reloadPending.Store(false)
		return err
	}
	return nil
}

// IsReloadPending reports whether a config reload is currently in flight.
// Returns false once ClearReloadPending is called by the executing reload.
func (al *AgentLoop) IsReloadPending() bool {
	return al.reloadPending.Load()
}

// ClearReloadPending marks the in-flight reload as complete.
// Called by gateway.executeReload (via defer) after the reload finishes.
func (al *AgentLoop) ClearReloadPending() {
	al.reloadPending.Store(false)
}

var audioAnnotationRe = regexp.MustCompile(`\[(voice|audio)(?::[^\]]*)?\]`)

// transcribeAudioInMessage resolves audio media refs, transcribes them, and
// replaces audio annotations in msg.Content with the transcribed text.
// Returns the (possibly modified) message and true if audio was transcribed.
func (al *AgentLoop) transcribeAudioInMessage(ctx context.Context, msg bus.InboundMessage) (bus.InboundMessage, bool) {
	store := al.GetMediaStore()
	if al.transcriber == nil || store == nil || len(msg.Media) == 0 {
		return msg, false
	}

	// Transcribe each audio media ref in order.
	var transcriptions []string
	for _, ref := range msg.Media {
		path, meta, err := store.ResolveWithMeta(ref)
		if err != nil {
			logger.WarnCF("voice", "Failed to resolve media ref", map[string]any{"ref": ref, "error": err})
			continue
		}
		if !utils.IsAudioFile(meta.Filename, meta.ContentType) {
			continue
		}
		result, err := al.transcriber.Transcribe(ctx, path)
		if err != nil {
			logger.WarnCF("voice", "Transcription failed", map[string]any{"ref": ref, "error": err})
			transcriptions = append(transcriptions, "")
			continue
		}
		transcriptions = append(transcriptions, result.Text)
	}

	if len(transcriptions) == 0 {
		return msg, false
	}

	al.sendTranscriptionFeedback(ctx, msg.Channel, msg.ChatID, msg.MessageID, transcriptions)

	// Replace audio annotations sequentially with transcriptions.
	idx := 0
	newContent := audioAnnotationRe.ReplaceAllStringFunc(msg.Content, func(match string) string {
		if idx >= len(transcriptions) {
			return match
		}
		text := transcriptions[idx]
		idx++
		return "[voice: " + text + "]"
	})

	// Append any remaining transcriptions not matched by an annotation.
	for ; idx < len(transcriptions); idx++ {
		newContent += "\n[voice: " + transcriptions[idx] + "]"
	}

	msg.Content = newContent
	return msg, true
}

// sendTranscriptionFeedback sends feedback to the user with the result of
// audio transcription if the option is enabled. It uses Manager.SendMessage
// which executes synchronously (rate limiting, splitting, retry) so that
// ordering with the subsequent placeholder is guaranteed.
func (al *AgentLoop) sendTranscriptionFeedback(
	ctx context.Context,
	channel, chatID, messageID string,
	validTexts []string,
) {
	cfg := al.GetConfig()
	if !cfg.Voice.EchoTranscription {
		return
	}
	cm := al.getChannelManager()
	if cm == nil {
		return
	}

	var nonEmpty []string
	for _, t := range validTexts {
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}

	var feedbackMsg string
	if len(nonEmpty) > 0 {
		feedbackMsg = "Transcript: " + strings.Join(nonEmpty, "\n")
	} else {
		feedbackMsg = "No voice detected in the audio"
	}

	err := cm.SendMessage(ctx, bus.OutboundMessage{
		Channel:          channel,
		ChatID:           chatID,
		Content:          feedbackMsg,
		ReplyToMessageID: messageID,
	})
	if err != nil {
		logger.WarnCF("voice", "Failed to send transcription feedback", map[string]any{"error": err.Error()})
	}
}

// inferMediaType determines the media type ("image", "audio", "video", "file")
// from a filename and MIME content type.
func inferMediaType(filename, contentType string) string {
	ct := strings.ToLower(contentType)
	fn := strings.ToLower(filename)

	if strings.HasPrefix(ct, "image/") {
		return "image"
	}
	if strings.HasPrefix(ct, "audio/") || ct == "application/ogg" {
		return "audio"
	}
	if strings.HasPrefix(ct, "video/") {
		return "video"
	}

	// Fallback: infer from extension
	ext := filepath.Ext(fn)
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return "image"
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac", ".wma", ".opus":
		return "audio"
	case ".mp4", ".avi", ".mov", ".webm", ".mkv":
		return "video"
	}

	return "file"
}

// RecordLastChannel records the last active channel for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChannel(channel string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChannel(channel)
}

// RecordLastChatID records the last active chat ID for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChatID(chatID string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChatID(chatID)
}

func (al *AgentLoop) ProcessDirect(
	ctx context.Context,
	content, sessionKey string,
) (string, error) {
	return al.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

func (al *AgentLoop) ProcessDirectWithChannel(
	ctx context.Context,
	content, sessionKey, channel, chatID string,
) (string, error) {
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", err
	}

	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "cron",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	resp, _, err := al.processMessage(ctx, msg)
	return resp, err
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
func (al *AgentLoop) ProcessHeartbeat(
	ctx context.Context,
	content, channel, chatID string,
) (string, error) {
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", err
	}

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		return "", fmt.Errorf("no default agent for heartbeat")
	}
	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:           "heartbeat",
		Channel:              channel,
		ChatID:               chatID,
		UserMessage:          content,
		DefaultResponse:      defaultResponse,
		EnableSummary:        false,
		SendResponse:         false,
		SuppressToolFeedback: true,
		NoHistory:            true, // Don't load session history for heartbeat
	})
}

// ProcessScheduled runs a fired schedule's message as ownerAgentID against the
// concrete pre-created sessionID (issue #264, W-1). It is the dedicated headless
// entry point for the cron → agent fire path and deliberately differs from the
// human message path:
//
//   - It pins ownerAgentID directly via runAgentLoop — it does NOT consult
//     routing or the sessionActiveAgent handoff map, so a human switching agents
//     in this session cannot hijack the scheduled run, and a missing/disabled
//     owner is a hard error (never a default-agent fallback, the core #264 bug).
//   - It passes the concrete sessionID as TranscriptSessionID so the turn
//     registers under it (GetActiveTurnHookForSession matches by
//     transcriptSessionID) and RequestCancel(CancelScope{SessionID}) can abort
//     it on a caller-imposed deadline. The session key is the per-owner
//     "agent:<owner>:session:<id>" form, collision-free across isolated runs.
//   - It sets AutoDenyAsk so any `ask`-policy tool call is denied without
//     blocking for approval (FR-009) — no operator is present.
//
// The caller (the gateway's scheduled-job runner) resolves the owner + picks
// the session per session_mode and supplies a concrete sessionID; it imposes
// the deadline on ctx and calls RequestCancel on timeout. ProcessScheduled
// only guarantees
// owner-pinning, cancellability, and prompt return.
//
// Returns the agent's reply and a non-nil error on run failure. An aborted
// (canceled/deadline) run returns a context-derived error promptly.
func (al *AgentLoop) ProcessScheduled(
	ctx context.Context,
	ownerAgentID, sessionID, content, channel, chatID string,
) (string, error) {
	if ownerAgentID == "" {
		return "", fmt.Errorf("owner unavailable: empty agent id")
	}
	if sessionID == "" {
		return "", fmt.Errorf("scheduled run requires a concrete session id")
	}

	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", err
	}

	// Owner pinning (FR-001): a missing owner is a hard error — NEVER fall back
	// to GetDefaultAgent. Note GetAgent registers ALL agents regardless of
	// Enabled, so registration alone does not imply the owner is active; the
	// config IsActive() check below rejects a disabled owner.
	agent, ok := al.GetRegistry().GetAgent(ownerAgentID)
	if !ok || agent == nil {
		return "", fmt.Errorf("owner unavailable: agent %q not found", ownerAgentID)
	}
	// Disabled-owner guard (FR-001): a registered-but-disabled agent must not
	// run. The runtime registry keeps disabled agents, so consult config.
	if ac := findAgentConfig(al.GetConfig(), ownerAgentID); ac != nil && !ac.IsActive() {
		return "", fmt.Errorf("owner unavailable: agent %q is disabled", ownerAgentID)
	}

	// Per-owner session key (collision-free across isolated runs). Built here
	// rather than via agentSessionKey because we bypass resolveMessageRoute.
	sessionKey := fmt.Sprintf("agent:%s:session:%s", ownerAgentID, sessionID)

	// Resolve the transcript store for the concrete session so tool calls are
	// recorded and the turn registers under transcriptSessionID == sessionID
	// (which is what RequestCancel(CancelScope{SessionID}) matches against).
	transcriptStore := al.ResolveSessionStore(sessionID)
	if transcriptStore == nil {
		// Hard error: without a store the user message cannot be recorded, the
		// assistant reply will be lost, and message_count stays at 0. Do not
		// silently degrade — return now so the caller records an explicit failure.
		return "", fmt.Errorf(
			"scheduled run: session store not found for session %q (owner %s) — aborting to avoid unrecorded turn",
			sessionID, ownerAgentID,
		)
	}

	// Append the user message to the transcript before running the agent loop,
	// mirroring the interactive websocket path (pkg/gateway/websocket.go ~l980).
	// Without this, message_count stays at 0 and "Run now" always shows "error"
	// because the agent loop's assistant reply has no paired user turn to count.
	userEntry := session.TranscriptEntry{
		ID:        fmt.Sprintf("scheduled-%s-%d", sessionID, time.Now().UnixNano()),
		Role:      "user",
		AgentID:   ownerAgentID,
		Content:   content,
		Timestamp: time.Now().UTC(),
	}
	if err := transcriptStore.AppendTranscript(sessionID, userEntry); err != nil {
		logger.ErrorCF("agent", "scheduled run: failed to record user message to transcript",
			map[string]any{"session_id": sessionID, "owner": ownerAgentID, "error": err.Error()})
		return "", fmt.Errorf("scheduled run: transcript write failed for session %q: %w", sessionID, err)
	}

	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:          sessionKey,
		Channel:             channel,
		ChatID:              chatID,
		UserMessage:         content,
		DefaultResponse:     defaultResponse,
		EnableSummary:       true,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     transcriptStore,
		AutoDenyAsk:         true, // FR-009: headless — auto-deny ask-policy tools
	})
}

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, *AgentInstance, error) {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(msg.Content, "Error:") || strings.Contains(msg.Content, "error") {
		logContent = msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(msg.Content, 80)
	}
	logger.InfoCF(
		"agent",
		fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, logContent),
		map[string]any{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.SenderID,
			"session_key": msg.SessionKey,
		},
	)

	var hadAudio bool
	msg, hadAudio = al.transcribeAudioInMessage(ctx, msg)

	// For audio messages the placeholder was deferred by the channel.
	// Now that transcription (and optional feedback) is done, send it.
	if hadAudio {
		if cm := al.getChannelManager(); cm != nil {
			cm.SendPlaceholder(ctx, msg.Channel, msg.ChatID)
		}
	}

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		resp, err := al.processSystemMessage(ctx, msg)
		return resp, nil, err
	}

	route, agent, routeErr := al.resolveMessageRoute(msg)
	if routeErr != nil {
		return "", nil, routeErr
	}

	// Reset message-tool state for this round so we don't skip publishing due to a previous round.
	if tool, ok := agent.Tools.Get("message"); ok {
		if resetter, ok := tool.(interface{ ResetSentInRound() }); ok {
			resetter.ResetSentInRound()
		}
	}

	// Resolve session key from route, while preserving explicit agent-scoped keys.
	scopeKey := resolveScopeKey(route, msg.SessionKey)
	sessionKey := scopeKey

	logger.InfoCF("agent", "Routed message",
		map[string]any{
			"agent_id":      agent.ID,
			"scope_key":     scopeKey,
			"session_key":   sessionKey,
			"matched_by":    route.MatchedBy,
			"route_agent":   route.AgentID,
			"route_channel": route.Channel,
		})

	// For non-webchat channel messages arriving without a session ID, create or
	// resume a persistent shared session so they appear in the session history panel.
	if msg.SessionID == "" && msg.Channel != "webchat" && msg.Channel != "system" && msg.Channel != "" {
		if sid := al.resolveOrCreateChannelSession(
			msg.Channel, msg.ChatID, agent.ID, msg.Sender.DisplayName,
		); sid != "" {
			msg.SessionID = sid
		}
	}

	// Resolve transcript store for tool call recording. SessionID is now
	// authoritative — populated directly by the gateway from frame.SessionID.
	var transcriptSessionID string
	var transcriptStore *session.UnifiedStore
	if msg.SessionID != "" {
		transcriptSessionID = msg.SessionID
		transcriptStore = al.ResolveSessionStore(msg.SessionID)
		if transcriptStore == nil {
			logger.WarnCF(
				"agent",
				"session_id present but store not found — tool calls will not be recorded",
				map[string]any{"session_id": msg.SessionID},
			)
		}
	}

	// Write user message to transcript for channel sessions.
	// Web-chat user messages are written by the WebSocket handler before the
	// turn starts (websocket.go); this path covers every other channel
	// (Telegram, Slack, Discord, etc.) so that replays show the user's prompt
	// alongside tool calls and assistant responses.
	channelNeedsTranscript := transcriptStore != nil &&
		msg.Channel != "webchat" && msg.Channel != "system" &&
		strings.TrimSpace(msg.Content) != ""
	if channelNeedsTranscript {
		entry := session.TranscriptEntry{
			ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
			Role:      "user",
			AgentID:   agent.ID,
			Content:   msg.Content,
			Timestamp: time.Now().UTC(),
		}
		if err := transcriptStore.AppendTranscript(transcriptSessionID, entry); err != nil {
			logger.WarnCF("agent", "could not record channel user message to transcript",
				map[string]any{"session_id": transcriptSessionID, "channel": msg.Channel, "error": err.Error()})
		}
	}

	opts := processOptions{
		SessionKey:          sessionKey,
		Channel:             msg.Channel,
		ChatID:              msg.ChatID,
		SenderID:            msg.SenderID,
		SenderDisplayName:   msg.Sender.DisplayName,
		UserMessage:         msg.Content,
		Media:               msg.Media,
		DefaultResponse:     defaultResponse,
		EnableSummary:       true,
		SendResponse:        false,
		TranscriptSessionID: transcriptSessionID,
		TranscriptStore:     transcriptStore,
		// Carry inbound metadata so runTurn can detect a per-thread model
		// switch (FR-011). The map is copied by reference — the turn flow
		// only reads from it.
		Metadata: msg.Metadata,
	}

	// FR-025: reset idle ticker on every user turn, using transcript session ID
	// when available (web-chat sessions). This starts the ticker on the first
	// turn and resets it on every subsequent turn.
	if transcriptSessionID != "" {
		al.resetIdleTicker(transcriptSessionID)
		// FR-024: track current session per agent for lazy CAS on switch.
		al.agentCurrentSession.Store(agent.ID, transcriptSessionID)

		// Self-heal: rebuild the agent's per-session history from the shared
		// transcript when the in-memory copy is missing or stale. We rehydrate
		// not only when the bucket is empty (handoff, fresh process, etc.) but
		// also when the bucket has *no* assistant/tool entries while the
		// transcript carries tool_calls owned by this agent — the symptom of
		// the prior wsStreamer bug that wrote assistant text with empty AgentID
		// and left the per-agent bucket stuck on user messages only. Without
		// this stronger trigger, an old session keeps starting from scratch
		// every turn because GetHistory returns the broken cached state and
		// the empty-only check above is satisfied.
		if agent.Sessions != nil {
			cur := agent.Sessions.GetHistory(sessionKey)
			needsHydrate := len(cur) == 0
			if !needsHydrate && transcriptStore != nil {
				hasAssistantOrTool := false
				for _, m := range cur {
					if m.Role == "assistant" || m.Role == "tool" {
						hasAssistantOrTool = true
						break
					}
				}
				if !hasAssistantOrTool {
					if entries, err := transcriptStore.ReadTranscript(transcriptSessionID); err == nil {
						for i := range entries {
							e := &entries[i]
							if (e.AgentID == agent.ID || e.AgentID == "") &&
								(e.Type == session.EntryTypeToolCall || e.Role == "assistant") {
								needsHydrate = true
								break
							}
						}
					}
				}
			}
			if needsHydrate {
				if err := al.HydrateAgentHistoryFromTranscript(transcriptSessionID); err != nil {
					logger.WarnCF("agent", "self-heal hydrate failed", map[string]any{
						"agent_id":   agent.ID,
						"session_id": transcriptSessionID,
						"error":      err.Error(),
					})
				}
			}
		}
	}

	// context-dependent commands check their own Runtime fields and report
	// "unavailable" when the required capability is nil.
	if response, handled := al.handleCommand(ctx, msg, agent, &opts); handled {
		return response, agent, nil
	}

	if pending := al.takePendingSkills(opts.SessionKey); len(pending) > 0 {
		opts.ForcedSkills = append(opts.ForcedSkills, pending...)
		logger.InfoCF("agent", "Applying pending skill override",
			map[string]any{
				"session_key": opts.SessionKey,
				"skills":      strings.Join(pending, ","),
			})
	}

	resp, err := al.runAgentLoop(ctx, agent, opts)
	return resp, agent, err
}

func (al *AgentLoop) resolveMessageRoute(msg bus.InboundMessage) (routing.ResolvedRoute, *AgentInstance, error) {
	registry := al.GetRegistry()

	// Explicit agent_id in message metadata takes top priority. The user
	// switching the SPA dropdown to a different agent is an authoritative
	// re-targeting that must win over any prior handoff routing override —
	// otherwise a Mia → Ray handoff persists silently after the user
	// explicitly switches back to Jim, and Jim's UI receives Ray's replies.
	// The override is still consulted below for messages without an
	// explicit agent_id (e.g. channel inputs that don't track agent state).
	if explicitID := inboundMetadata(msg, "agent_id"); explicitID != "" {
		if agent, ok := registry.GetAgent(explicitID); ok {
			// A worker is a delegation-only labour tier — never a chat target.
			// An inbound message that explicitly addresses a worker (e.g. a stale
			// SPA dropdown value or a crafted channel payload) must NOT let the
			// worker answer as a live persona. Degrade to the normal routing
			// cascade, which resolves a chat-target default. Do not delete any
			// handoff pin here — falling through preserves an existing chat-target
			// override if one is set.
			if agent.IsWorker() {
				logger.WarnCF("agent", "Explicit agent_id references a worker (not a chat target); ignoring and falling back to default route", map[string]any{
					"agent_id":   explicitID,
					"session_id": msg.SessionID,
					"reason":     "worker is invoked via delegation, not as a chat target",
				})
				// Fall through to the handoff-override / ResolveRoute cascade below.
			} else {
				// Clear stale handoff override only when the explicit target differs from
				// the current override. If the user selects the same agent that the handoff
				// already set, clearing the override would incorrectly reset routing state.
				if cur, ok := al.sessionActiveAgent.Load(sessionScopeKey(msg)); !ok || cur.(string) != explicitID {
					al.sessionActiveAgent.Delete(sessionScopeKey(msg))
				}
				logger.InfoCF("agent", "Routed to explicit agent (dropdown)", map[string]any{
					"agent_id":   explicitID,
					"session_id": msg.SessionID,
					"workspace":  agent.Workspace,
				})
				sk := agentSessionKey(explicitID, msg)
				return routing.ResolvedRoute{AgentID: explicitID, SessionKey: sk}, agent, nil
			}
		} else {
			logger.ErrorCF("agent", "explicit agent_id not found in registry", map[string]any{
				"agent_id":       explicitID,
				"registered_ids": registry.ListAgentIDs(),
			})
			return routing.ResolvedRoute{}, nil, fmt.Errorf("the requested agent is not available")
		}
	}

	// Check session/chat-scope handoff override. Only reached when the message
	// carries no explicit agent_id. sessionScopeKey prevents non-webchat
	// channels without a SessionID from collapsing into a single global bucket.
	{
		scopeKey := sessionScopeKey(msg)
		if activeAgent, ok := al.sessionActiveAgent.Load(scopeKey); ok {
			agentID := activeAgent.(string)
			if agentID != "" {
				if agent, ok := registry.GetAgent(agentID); ok {
					// A worker must never be a live chat target. A pin that points at
					// a worker is stale/illegitimate (handoff now rejects worker
					// targets, but a pin created before this guard, or via another
					// path, could still exist). Drop the stale pin and fall through
					// to the normal ResolveRoute cascade so a chat-target default
					// answers instead of the worker.
					if agent.IsWorker() {
						logger.WarnCF("agent", "Session handoff pin references a worker (not a chat target); clearing stale pin and falling back to default route", map[string]any{
							"session_id": msg.SessionID,
							"agent_id":   agentID,
							"reason":     "worker is invoked via delegation, not as a chat target",
						})
						al.sessionActiveAgent.Delete(scopeKey)
					} else {
						logger.InfoCF("agent", "Session handoff override active", map[string]any{
							"session_id": msg.SessionID,
							"agent_id":   agentID,
						})
						sk := agentSessionKey(agentID, msg)
						return routing.ResolvedRoute{AgentID: agentID, SessionKey: sk}, agent, nil
					}
				} else {
					// Agent was deleted after the override was set — clean up and fall through.
					al.sessionActiveAgent.Delete(scopeKey)
				}
			}
		}
	}

	instanceID := inboundInstanceID(msg)
	route := registry.ResolveRoute(routing.RouteInput{
		Channel:    msg.Channel,
		AccountID:  inboundMetadata(msg, metadataKeyAccountID),
		Peer:       extractPeer(msg),
		ParentPeer: extractParentPeer(msg),
		GuildID:    inboundMetadata(msg, metadataKeyGuildID),
		TeamID:     inboundMetadata(msg, metadataKeyTeamID),
		InstanceID: instanceID,
		Identity:   al.resolveInboundIdentity(instanceID),
	})

	agent, ok := registry.GetAgent(route.AgentID)
	if !ok {
		agent = registry.GetDefaultAgent()
	}
	if agent == nil {
		// FR-015: log unroutable message with structured context before rejecting.
		logger.WarnCF("agent", "Unroutable message rejected — no matching agent and no default",
			map[string]any{
				"channel":        msg.Channel,
				"sender_id":      msg.SenderID,
				"chat_id":        msg.ChatID,
				"resolved_agent": route.AgentID,
			})
		return routing.ResolvedRoute{}, nil, fmt.Errorf("no agent available for route (agent_id=%s)", route.AgentID)
	}

	return route, agent, nil
}

func resolveScopeKey(route routing.ResolvedRoute, msgSessionKey string) string {
	if msgSessionKey != "" && strings.HasPrefix(msgSessionKey, sessionKeyAgentPrefix) {
		return msgSessionKey
	}
	return route.SessionKey
}

// sessionScopeKey returns a stable bucket key for a message.
// When SessionID is set, returns "session:<sessionID>".
// When SessionID is empty, returns "chat:<channel>:<chatID>" so that
// messages from non-webchat channels that haven't been assigned a session yet
// do not all collapse into a single "session:" bucket.
func sessionScopeKey(msg bus.InboundMessage) string {
	if msg.SessionID != "" {
		return "session:" + msg.SessionID
	}
	return "chat:" + msg.Channel + ":" + msg.ChatID
}

// agentSessionKey builds the per-agent session key combining agentID with the
// message's scope bucket. Uses session-scoped format when SessionID is known;
// falls back to chat-scoped format for channels that haven't minted a session.
func agentSessionKey(agentID string, msg bus.InboundMessage) string {
	if msg.SessionID != "" {
		return fmt.Sprintf("agent:%s:session:%s", agentID, msg.SessionID)
	}
	return fmt.Sprintf("agent:%s:chat:%s:%s", agentID, msg.Channel, msg.ChatID)
}

func (al *AgentLoop) resolveSteeringTarget(msg bus.InboundMessage) (string, string, bool) {
	if msg.Channel == "system" {
		return "", "", false
	}

	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil || agent == nil {
		return "", "", false
	}

	// Per-session worker scope: when msg.SessionID is set, append it so each
	// session for the same agent gets its OWN worker goroutine. Without this,
	// the routing layer's SessionKey collapses to "agent:<id>:<id>" for all
	// channels that haven't explicitly carried a session_key, and every
	// session for a given agent ends up sharing one worker — reintroducing
	// the serialization regression the per-session-worker design exists to fix.
	scope := resolveScopeKey(route, msg.SessionKey)
	if msg.SessionID != "" {
		scope = scope + ":" + msg.SessionID
	}
	return scope, agent.ID, true
}

func (al *AgentLoop) processSystemMessage(
	ctx context.Context,
	msg bus.InboundMessage,
) (string, error) {
	if msg.Channel != "system" {
		return "", fmt.Errorf(
			"processSystemMessage called with non-system message channel: %s",
			msg.Channel,
		)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]any{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel, originChatID string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
		originChatID = msg.ChatID[idx+1:]
	} else {
		originChannel = "cli"
		originChatID = msg.ChatID
	}

	// Extract subagent result from message content
	// Format: "Task 'label' completed.\n\nResult:\n<actual content>"
	content := msg.Content
	if idx := strings.Index(content, "Result:\n"); idx >= 0 {
		content = content[idx+8:] // Extract just the result part
	}

	// Skip internal channels - only log, don't send to user
	if constants.IsInternalChannel(originChannel) {
		logger.InfoCF("agent", "Subagent completed (internal channel)",
			map[string]any{
				"sender_id":   msg.SenderID,
				"content_len": len(content),
				"channel":     originChannel,
			})
		return "", nil
	}

	// Use default agent for system messages
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		return "", fmt.Errorf("no default agent for system message")
	}

	// Use the origin session for context
	sessionKey := routing.BuildAgentMainSessionKey(agent.ID)

	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         originChannel,
		ChatID:          originChatID,
		UserMessage:     fmt.Sprintf("[System: %s] %s", msg.SenderID, msg.Content),
		DefaultResponse: "Background task completed.",
		EnableSummary:   false,
		SendResponse:    true,
	})
}

// runAgentLoop remains the top-level shell that starts a turn and publishes
// any post-turn work. runTurn owns the full turn lifecycle.
func (al *AgentLoop) runAgentLoop(
	ctx context.Context,
	agent *AgentInstance,
	opts processOptions,
) (string, error) {
	// Record last channel for heartbeat notifications (skip internal channels and cli)
	if opts.Channel != "" && opts.ChatID != "" && !constants.IsInternalChannel(opts.Channel) {
		channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
		if err := al.RecordLastChannel(channelKey); err != nil {
			logger.WarnCF(
				"agent",
				"Failed to record last channel",
				map[string]any{"error": err.Error()},
			)
		}
	}

	ts := newTurnState(agent, opts, al.newTurnEventScope(agent.ID, opts.SessionKey))
	// Bug 1 fix: wire a resolver so appendToolCallTranscript (and event payloads)
	// use the runtime-current active agent rather than the turn's starting agent.
	// After a handoff, sessionActiveAgent reflects the new agent; tool_call entries
	// produced in the same turn will carry the correct post-handoff agent_id.
	if opts.TranscriptSessionID != "" {
		resolverKey := "session:" + opts.TranscriptSessionID
		ts.activeAgentResolver = func() string {
			if v, ok := al.sessionActiveAgent.Load(resolverKey); ok {
				if id, ok := v.(string); ok && id != "" {
					return id
				}
			}
			return ""
		}
	}
	result, err := al.runTurn(ctx, ts)
	if err != nil {
		return "", err
	}
	if result.status == TurnEndStatusAborted {
		return "", nil
	}

	for _, followUp := range result.followUps {
		if pubErr := al.bus.PublishInbound(ctx, followUp); pubErr != nil {
			logger.WarnCF("agent", "Failed to publish follow-up after turn",
				map[string]any{
					"turn_id": ts.turnID,
					"error":   pubErr.Error(),
				})
		}
	}

	if opts.SendResponse && result.finalContent != "" {
		if err := al.bus.PublishOutbound(ctx, bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: result.finalContent,
		}); err != nil {
			logger.ErrorCF("agent", "Failed to publish outbound response after turn",
				map[string]any{"channel": opts.Channel, "chat_id": opts.ChatID, "error": err.Error()})
		}
	}

	if result.finalContent != "" {
		responsePreview := utils.Truncate(result.finalContent, 120)
		logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
			map[string]any{
				"agent_id":     agent.ID,
				"session_key":  opts.SessionKey,
				"iterations":   ts.currentIteration(),
				"final_length": len(result.finalContent),
			})
	}

	return result.finalContent, nil
}

func (al *AgentLoop) targetReasoningChannelID(channelName string) (chatID string) {
	cm := al.getChannelManager()
	if cm == nil {
		return ""
	}
	if ch, ok := cm.GetChannel(channelName); ok {
		return ch.ReasoningChannelID()
	}
	return ""
}

func (al *AgentLoop) handleReasoning(
	ctx context.Context,
	reasoningContent, channelName, channelID string,
) {
	if reasoningContent == "" || channelName == "" || channelID == "" {
		return
	}

	// Check context cancellation before attempting to publish,
	// since PublishOutbound's select may race between send and ctx.Done().
	if ctx.Err() != nil {
		return
	}

	// Use a short timeout so the goroutine does not block indefinitely when
	// the outbound bus is full.  Reasoning output is best-effort; dropping it
	// is acceptable to avoid goroutine accumulation.
	pubCtx, pubCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pubCancel()

	if err := al.bus.PublishOutbound(pubCtx, bus.OutboundMessage{
		Channel: channelName,
		ChatID:  channelID,
		Content: reasoningContent,
	}); err != nil {
		// Treat context.DeadlineExceeded / context.Canceled as expected
		// (bus full under load, or parent canceled).  Check the error
		// itself rather than ctx.Err(), because pubCtx may time out
		// (5 s) while the parent ctx is still active.
		// Also treat ErrBusClosed as expected — it occurs during normal
		// shutdown when the bus is closed before all goroutines finish.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
			errors.Is(err, bus.ErrBusClosed) {
			logger.DebugCF("agent", "Reasoning publish skipped (timeout/cancel)", map[string]any{
				"channel": channelName,
				"error":   err.Error(),
			})
		} else {
			logger.WarnCF("agent", "Failed to publish reasoning (best-effort)", map[string]any{
				"channel": channelName,
				"error":   err.Error(),
			})
		}
	}
}

func (al *AgentLoop) runTurn(ctx context.Context, ts *turnState) (turnResult, error) {
	// H1: guard against an already-canceled or timed-out context before doing any work.
	if ctx.Err() != nil {
		return turnResult{}, fmt.Errorf("turn not started: %w", ctx.Err())
	}

	// Snapshot the media store once at turn start to avoid repeated lock
	// acquisitions on the hot path and to ensure consistency across the turn
	// even if restartServices swaps the store mid-turn (N2 data-race fix).
	turnMediaStore := al.GetMediaStore()
	// Snapshot the channel manager for the same reason.
	turnChannelManager := al.getChannelManager()

	var turnCtx context.Context
	var turnCancel context.CancelFunc
	turnTimeout := time.Duration(ts.agent.TimeoutSeconds) * time.Second
	if turnTimeout > 0 {
		turnCtx, turnCancel = context.WithTimeout(ctx, turnTimeout)
	} else {
		turnCtx, turnCancel = context.WithCancel(ctx)
	}
	defer turnCancel()
	ts.setTurnCancel(turnCancel)

	// Finalize the streamer when the turn ends (regardless of how it exits).
	// This sends the "done" frame exactly once, at turn completion, rather than
	// after each intermediate LLM call that may be followed by tool execution.
	defer ts.finalizeStreamer(ctx)

	// Inject turnState and AgentLoop into context so tools (e.g. spawn) can retrieve them.
	turnCtx = withTurnState(turnCtx, ts)
	turnCtx = WithAgentLoop(turnCtx, al)
	// SEC-15: Inject agent ID so audit entries carry the agent identity.
	turnCtx = tools.WithAgentID(turnCtx, ts.agent.ID)
	// Inject session key so handoff/return_to_default tools can address the session.
	if ts.sessionKey == "" {
		logger.WarnCF("agent", "runTurn: sessionKey is empty — handoff tool will not work",
			map[string]any{"agent_id": ts.agentID, "chat_id": ts.chatID})
	}
	turnCtx = tools.WithSessionKey(turnCtx, ts.sessionKey)
	// Inject the actual session ID (directory name) for the handoff tool.
	// The session key is a routing key; the transcript session ID is the
	// real session directory (e.g., "session_01KP30THP63YFESKGECYYHYQWY").
	turnCtx = tools.WithTranscriptSessionID(turnCtx, ts.opts.TranscriptSessionID)
	// Inject the session owner so sysagent tools (system.workspace.create,
	// system.task.create) can stamp the owner on newly created entities
	// (Rule-2 of the sysagent ownership rule, SEC-2/#406).
	if ts.opts.TranscriptSessionID != "" {
		if store := al.ResolveSessionStore(ts.opts.TranscriptSessionID); store != nil {
			if meta, err := store.GetMeta(ts.opts.TranscriptSessionID); err == nil && meta.Owner != "" {
				turnCtx = tools.WithSessionOwner(turnCtx, meta.Owner)
			}
		}
	}
	// Inject the workspace ID so memory tools can route to the shared room (FR-7.1).
	if ts.opts.WorkspaceID != "" {
		turnCtx = tools.WithWorkspaceID(turnCtx, ts.opts.WorkspaceID)
	}

	al.registerActiveTurn(ts)
	// B1: Finish must run before clearActiveTurn so that IsAlive() goes false
	// and the onCancelFinish callback fires on natural turn completion, not only
	// on explicit cancel paths. closeOnce.Do inside Finish makes repeated calls
	// safe — the cancel path may have already called Finish(true); the second
	// call here is a no-op via isFinished.Store(true) being idempotent and
	// closeOnce.Do executing at most once.
	defer ts.Finish(false)
	defer al.clearActiveTurn(ts)

	turnStatus := TurnEndStatusCompleted
	defer func() {
		al.emitEvent(
			EventKindTurnEnd,
			ts.eventMeta("runTurn", "turn.end"),
			TurnEndPayload{
				Status:          turnStatus,
				Iterations:      ts.currentIteration(),
				Duration:        time.Since(ts.startedAt),
				FinalContentLen: ts.finalContentLen(),
				ChatID:          ts.chatID,
				SessionID:       ts.transcriptSessionID,
				IsRoot:          ts.parentTurnID == "",
			},
		)
	}()

	al.emitEvent(
		EventKindTurnStart,
		ts.eventMeta("runTurn", "turn.start"),
		TurnStartPayload{
			Channel:     ts.channel,
			ChatID:      ts.chatID,
			UserMessage: ts.userMessage,
			MediaCount:  len(ts.media),
		},
	)

	// FR-011, FR-012: detect a per-thread model switch via the inbound message
	// metadata. When the user-selected model differs from the agent's currently
	// loaded model, run handleModelSwitch BEFORE the first LLM call so the next
	// request sees the compressed, annotated history. handleModelSwitch is a
	// no-op when no switch is requested.
	if requested := strings.TrimSpace(inboundMetadata(bus.InboundMessage{Metadata: ts.opts.Metadata}, "model_name")); requested != "" {
		// Skip when the requested model is the same as the agent's currently
		// loaded one — this is the no-op case in spec §11 Dataset 3 row 1.
		if requested != ts.agent.Model {
			switchedAgent, switchErr := al.handleModelSwitch(
				ctx,
				ts.agent,
				ts.sessionKey,
				requested,
				bus.InboundMessage{Metadata: ts.opts.Metadata},
			)
			if switchErr != nil {
				logger.WarnCF("agent", "switch-time compress failed; continuing with current model",
					map[string]any{
						"agent_id":   ts.agentID,
						"session_id": ts.sessionKey,
						"new_model":  requested,
						"error":      switchErr.Error(),
					})
			} else if switchedAgent != nil {
				// Re-point the turn at the (possibly mutated) agent so
				// subsequent reads of ts.agent.Model reflect the switch.
				ts.agent = switchedAgent
			}
		}
	}

	var history []providers.Message
	var summary string
	if !ts.opts.NoHistory {
		// FR-069 / FR-088: recover orphaned tool calls left by a SIGKILL or OOM
		// kill while the gateway was paused awaiting approval. The function is
		// idempotent — it no-ops on clean sessions and on sessions where the
		// synthetic turn_canceled_restart entry already exists. The on-disk
		// transcript is preserved; only the LLM-context slice (history) is
		// cleaned so the LLM does not see dangling unanswered tool_call entries.
		history = RecoverOrphanedToolCalls(ts.agent.Sessions, ts.sessionKey, al.auditLogger)
		summary = ts.agent.Sessions.GetSummary(ts.sessionKey)
	}
	ts.captureRestorePoint(history, summary)

	messages := ts.agent.ContextBuilder.BuildMessages(
		history,
		summary,
		ts.userMessage,
		ts.media,
		ts.channel,
		ts.chatID,
		ts.opts.SenderID,
		ts.opts.SenderDisplayName,
		activeSkillNames(ts.agent, ts.opts)...,
	)

	cfg := al.GetConfig()
	maxMediaSize := cfg.Agents.Defaults.GetMaxMediaSize()
	messages = resolveMediaRefs(messages, turnMediaStore, maxMediaSize, ts.agent.Model)

	if !ts.opts.NoHistory {
		toolDefs := ts.agent.Tools.ToProviderDefs()
		if isOverContextBudget(ts.agent.ContextWindow, messages, toolDefs, ts.agent.MaxTokens) {
			logger.WarnCF("agent", "Proactive compression: context budget exceeded before LLM call",
				map[string]any{"session_key": ts.sessionKey})
			if compression, ok := al.forceCompression(ts.agent, ts.sessionKey); ok {
				al.emitEvent(
					EventKindContextCompress,
					ts.eventMeta("runTurn", "turn.context.compress"),
					ContextCompressPayload{
						Reason:            ContextCompressReasonProactive,
						DroppedMessages:   compression.DroppedMessages,
						RemainingMessages: compression.RemainingMessages,
					},
				)
				ts.refreshRestorePointFromSession(ts.agent)
			}
			newHistory := ts.agent.Sessions.GetHistory(ts.sessionKey)
			newSummary := ts.agent.Sessions.GetSummary(ts.sessionKey)
			messages = ts.agent.ContextBuilder.BuildMessages(
				newHistory, newSummary, ts.userMessage,
				ts.media, ts.channel, ts.chatID,
				ts.opts.SenderID, ts.opts.SenderDisplayName,
				activeSkillNames(ts.agent, ts.opts)...,
			)
			messages = resolveMediaRefs(messages, turnMediaStore, maxMediaSize, ts.agent.Model)
		}
	}

	// Save user message to session
	if !ts.opts.NoHistory && (strings.TrimSpace(ts.userMessage) != "" || len(ts.media) > 0) {
		rootMsg := providers.Message{
			Role:    "user",
			Content: ts.userMessage,
			Media:   append([]string(nil), ts.media...),
		}
		if len(rootMsg.Media) > 0 {
			ts.agent.Sessions.AddFullMessage(ts.sessionKey, rootMsg)
		} else {
			ts.agent.Sessions.AddMessage(ts.sessionKey, rootMsg.Role, rootMsg.Content)
		}
		ts.recordPersistedMessage(rootMsg)
	}

	ts.agent.mu.RLock()
	activeCandidates, activeModel, usedLight := al.selectCandidates(ts.agent, ts.userMessage, messages)
	activeProvider := ts.agent.Provider
	if usedLight && ts.agent.LightProvider != nil {
		activeProvider = ts.agent.LightProvider
	}
	ts.agent.mu.RUnlock()
	pendingMessages := append([]providers.Message(nil), ts.opts.InitialSteeringMessages...)
	var finalContent string
	emptyResponseRetries := 0
	const maxEmptyResponseRetries = 1

turnLoop:
	for ts.currentIteration() < ts.agent.MaxIterations || len(pendingMessages) > 0 || func() bool {
		graceful, _ := ts.gracefulInterruptRequested()
		return graceful
	}() {
		if ts.hardAbortRequested() {
			turnStatus = TurnEndStatusAborted
			return al.abortTurn(ts)
		}

		iteration := ts.currentIteration() + 1
		ts.setIteration(iteration)

		// Hard ceiling: never exceed 2x MaxIterations regardless of pending messages or
		// graceful-interrupt state. This prevents an unbounded loop when the agent keeps
		// producing follow-up messages or the interrupt flag is never cleared.
		if hardCeiling := 2 * ts.agent.MaxIterations; iteration > hardCeiling {
			logger.WarnCF("agent", "Turn exceeded hard iteration ceiling, breaking unconditionally",
				map[string]any{
					"agent_id":     ts.agentID,
					"turn_id":      ts.turnID,
					"iteration":    iteration,
					"max_iter":     ts.agent.MaxIterations,
					"hard_ceiling": hardCeiling,
				})
			break turnLoop
		}

		ts.setPhase(TurnPhaseRunning)

		// SEC-26: Per-agent LLM call rate limit check. Runs once per turn
		// iteration, before the actual LLM call. The system agent is exempt.
		if al.rateLimiter != nil && cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour > 0 &&
			!security.IsPrivilegedAgent(ts.agent.AgentType) {
			window := al.rateLimiter.GetOrCreate(
				"agent:"+ts.agent.ID+":llm_call",
				cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour,
				time.Hour,
				security.ScopeAgent,
				ts.agent.ID,
				"llm_call",
			)
			if result := window.Allow(); !result.Allowed {
				al.recordRateLimitDenial(
					ts,
					"agent_llm_calls_per_hour",
					RateLimitPayload{
						Scope:             string(security.ScopeAgent),
						Resource:          "llm_call",
						PolicyRule:        result.PolicyRule,
						RetryAfterSeconds: result.RetryAfterSeconds,
						AgentID:           ts.agent.ID,
						ChatID:            ts.chatID,
					},
					map[string]any{"retry_after_seconds": result.RetryAfterSeconds},
				)
				turnStatus = TurnEndStatusError
				return turnResult{}, fmt.Errorf("rate limit: %s (retry after %.0fs)",
					result.PolicyRule, result.RetryAfterSeconds)
			}
		}

		// SEC-26: Global daily cost cap pre-check. Deny if the accumulated cost
		// for today already meets or exceeds the cap. The system agent is exempt.
		if al.rateLimiter != nil && cfg.Sandbox.RateLimits.DailyCostCapUSD > 0 &&
			!security.IsPrivilegedAgent(ts.agent.AgentType) {
			if currentCost := al.rateLimiter.GetDailyCost(); currentCost >= cfg.Sandbox.RateLimits.DailyCostCapUSD {
				capRule := fmt.Sprintf("global daily cost cap exceeded ($%.2f)", cfg.Sandbox.RateLimits.DailyCostCapUSD)
				al.recordRateLimitDenial(
					ts,
					"daily_cost_cap_usd",
					RateLimitPayload{
						Scope:      string(security.ScopeGlobal),
						Resource:   "daily_cost_usd",
						PolicyRule: capRule,
						AgentID:    ts.agent.ID,
						ChatID:     ts.chatID,
					},
					map[string]any{
						"daily_cost_usd": currentCost,
						"daily_cost_cap": cfg.Sandbox.RateLimits.DailyCostCapUSD,
					},
				)
				turnStatus = TurnEndStatusError
				return turnResult{}, fmt.Errorf("rate limit: %s", capRule)
			}
		}

		if iteration > 1 {
			if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
				pendingMessages = append(pendingMessages, steerMsgs...)
			}
		} else if !ts.opts.SkipInitialSteeringPoll {
			if steerMsgs := al.dequeueSteeringMessagesForScopeWithFallback(ts.sessionKey); len(steerMsgs) > 0 {
				pendingMessages = append(pendingMessages, steerMsgs...)
			}
		}

		// Check if parent turn has ended (SubTurn support)
		if ts.parentTurnState != nil && ts.IsParentEnded() {
			if !ts.critical {
				logger.InfoCF("agent", "Parent turn ended, non-critical SubTurn exiting gracefully", map[string]any{
					"agent_id":  ts.agentID,
					"iteration": iteration,
					"turn_id":   ts.turnID,
				})
				break
			}
			logger.InfoCF("agent", "Parent turn ended, critical SubTurn continues running", map[string]any{
				"agent_id":  ts.agentID,
				"iteration": iteration,
				"turn_id":   ts.turnID,
			})
		}

		// Poll for pending SubTurn results
		if ts.pendingResults != nil {
			select {
			case result, ok := <-ts.pendingResults:
				if ok && result != nil && result.ForLLM != "" {
					content := cfg.FilterSensitiveData(result.ForLLM)
					msg := providers.Message{Role: "user", Content: fmt.Sprintf("[SubTurn Result] %s", content)}
					pendingMessages = append(pendingMessages, msg)
				}
			default:
				// No results available
			}
		}

		// Inject pending steering messages
		if len(pendingMessages) > 0 {
			resolvedPending := resolveMediaRefs(pendingMessages, turnMediaStore, maxMediaSize, activeModel)
			totalContentLen := 0
			for i, pm := range pendingMessages {
				messages = append(messages, resolvedPending[i])
				totalContentLen += len(pm.Content)
				if !ts.opts.NoHistory {
					// Persist the original (unresolved) message to session history to preserve
					// compact media refs; resolved (base64) form is only used for the LLM request.
					ts.agent.Sessions.AddFullMessage(ts.sessionKey, pm)
					ts.recordPersistedMessage(pm)
				}
				logger.InfoCF("agent", "Injected steering message into context",
					map[string]any{
						"agent_id":    ts.agent.ID,
						"iteration":   iteration,
						"content_len": len(pm.Content),
						"media_count": len(pm.Media),
					})
			}
			al.emitEvent(
				EventKindSteeringInjected,
				ts.eventMeta("runTurn", "turn.steering.injected"),
				SteeringInjectedPayload{
					Count:           len(pendingMessages),
					TotalContentLen: totalContentLen,
				},
			)
			pendingMessages = nil
		}

		logger.DebugCF("agent", "LLM iteration",
			map[string]any{
				"agent_id":  ts.agent.ID,
				"iteration": iteration,
				"max":       ts.agent.MaxIterations,
			})

		gracefulTerminal, _ := ts.gracefulInterruptRequested()

		// FR-003, FR-041: Apply per-agent tool policy at LLM-call assembly time.
		// FilterToolsByPolicy enforces global × agent deny>ask>allow resolution and
		// the ScopeCore-on-custom-agent gate before the tool list reaches the LLM.
		// Tools with effective policy "ask" are included — the mid-turn policy snapshot
		// (FR-041) handles human-in-the-loop confirmation; see recordSyntheticDeny.
		allAgentTools := ts.agent.Tools.GetAll()
		policyFilteredTools, filterTimePolicyMap := tools.FilterToolsByPolicy(allAgentTools, ts.agent.AgentType, ts.agent.LoadToolPolicy())

		// FR-066: dedup invariant — tools[] must be name-unique after filter+assembly.
		// If a duplicate is detected, emit HIGH audit and return an error turn result
		// so the loop does not feed a malformed tool list to the LLM.
		if dedupErr := al.checkToolDedupInvariant(ts, policyFilteredTools); dedupErr != nil {
			denyMsg := fmt.Sprintf(`{"error":"tool_assembly_duplicate","message":%q}`, dedupErr.Error())
			syntheticDenyMsg := providers.Message{Role: "system", Content: denyMsg}
			if !ts.opts.NoHistory {
				ts.agent.Sessions.AddFullMessage(ts.sessionKey, syntheticDenyMsg)
				ts.recordPersistedMessage(syntheticDenyMsg)
			}
			if shouldAbort, _ := al.recordSyntheticDeny(ts); shouldAbort {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts)
			}
			// Fail the LLM call for this iteration by returning an error turn result.
			turnStatus = TurnEndStatusError
			return turnResult{status: TurnEndStatusError, finalContent: denyMsg}, dedupErr
		}

		providerToolDefs := tools.ToolsToProviderDefs(policyFilteredTools)

		// Native web search support
		_, hasWebSearch := ts.agent.Tools.Get("web_search")
		useNativeSearch := cfg.Tools.Web.PreferNative &&
			hasWebSearch &&
			func() bool {
				// Check if provider supports native search
				if ns, ok := activeProvider.(interface{ SupportsNativeSearch() bool }); ok {
					return ns.SupportsNativeSearch()
				}
				return false
			}()

		if useNativeSearch {
			// Filter out client-side web_search tool
			filtered := make([]providers.ToolDefinition, 0, len(providerToolDefs))
			for _, td := range providerToolDefs {
				if td.Function.Name != "web_search" {
					filtered = append(filtered, td)
				}
			}
			providerToolDefs = filtered
		}

		// Transparent repair of orphan tool_use / tool_result pairs in the
		// outbound history. OpenRouter's mid-stream provider rotation can leave
		// the context jsonl desynced with its own transcript; we reconcile from
		// the transcript before every LLM call so Anthropic never sees a broken
		// pair. No-op fast path when there are no orphans (the common case).
		repairedHistory := messages
		if ts.opts.TranscriptStore != nil && ts.opts.TranscriptSessionID != "" && ts.agent != nil && ts.agent.Tools != nil {
			repaired, _ := repairHistory(turnCtx, messages, ts.opts.TranscriptStore, ts.opts.TranscriptSessionID, ts.agent.Tools, ts.agent.ID, ts.agent.LoadToolPolicy())
			repairedHistory = repaired
		}

		callMessages := repairedHistory
		if gracefulTerminal {
			callMessages = append(append([]providers.Message(nil), repairedHistory...), ts.interruptHintMessage())
			providerToolDefs = nil
			ts.markGracefulTerminalUsed()
		}

		llmOpts := map[string]any{
			"max_tokens":       ts.agent.MaxTokens,
			"temperature":      ts.agent.Temperature,
			"prompt_cache_key": ts.agent.ID,
		}
		if useNativeSearch {
			llmOpts["native_search"] = true
		}
		ts.agent.mu.RLock()
		agentThinkingLevel := ts.agent.ThinkingLevel
		ts.agent.mu.RUnlock()
		if agentThinkingLevel != ThinkingOff {
			if tc, ok := activeProvider.(providers.ThinkingCapable); ok && tc.SupportsThinking() {
				llmOpts["thinking_level"] = string(agentThinkingLevel)
			} else {
				logger.WarnCF("agent", "thinking_level is set but current provider does not support it, ignoring",
					map[string]any{"agent_id": ts.agent.ID, "thinking_level": string(agentThinkingLevel)})
			}
		}

		llmModel := activeModel
		if al.hooks != nil {
			llmReq, decision := al.hooks.BeforeLLM(turnCtx, &LLMHookRequest{
				Meta:             ts.eventMeta("runTurn", "turn.llm.request"),
				Model:            llmModel,
				Messages:         callMessages,
				Tools:            providerToolDefs,
				Options:          llmOpts,
				Channel:          ts.channel,
				ChatID:           ts.chatID,
				GracefulTerminal: gracefulTerminal,
			})
			switch decision.normalizedAction() {
			case HookActionContinue, HookActionModify:
				if llmReq != nil {
					llmModel = llmReq.Model
					callMessages = llmReq.Messages
					providerToolDefs = llmReq.Tools
					llmOpts = llmReq.Options
				}
			case HookActionAbortTurn:
				turnStatus = TurnEndStatusError
				return turnResult{}, al.hookAbortError(ts, "before_llm", decision)
			case HookActionHardAbort:
				_ = ts.requestHardAbort()
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts)
			}
		}

		al.emitEvent(
			EventKindLLMRequest,
			ts.eventMeta("runTurn", "turn.llm.request"),
			LLMRequestPayload{
				Model:         llmModel,
				MessagesCount: len(callMessages),
				ToolsCount:    len(providerToolDefs),
				MaxTokens:     ts.agent.MaxTokens,
				Temperature:   ts.agent.Temperature,
			},
		)

		systemPromptLen := 0
		if len(callMessages) > 0 {
			systemPromptLen = len(callMessages[0].Content)
		}
		logger.DebugCF("agent", "LLM request",
			map[string]any{
				"agent_id":          ts.agent.ID,
				"iteration":         iteration,
				"model":             llmModel,
				"messages_count":    len(callMessages),
				"tools_count":       len(providerToolDefs),
				"max_tokens":        ts.agent.MaxTokens,
				"temperature":       ts.agent.Temperature,
				"system_prompt_len": systemPromptLen,
			})
		logger.DebugCF("agent", "Full LLM request",
			map[string]any{
				"iteration":     iteration,
				"messages_json": formatMessagesForLog(callMessages),
				"tools_json":    formatToolsForLog(providerToolDefs),
			})

		callLLM := func(messagesForCall []providers.Message, toolDefsForCall []providers.ToolDefinition) (*providers.LLMResponse, error) {
			providerCtx, providerCancel := context.WithCancel(turnCtx)
			ts.setProviderCancel(providerCancel)
			defer func() {
				providerCancel()
				ts.clearProviderCancel(providerCancel)
			}()

			al.activeRequests.Add(1)
			defer al.activeRequests.Done()

			if len(activeCandidates) > 1 && al.fallback != nil {
				fbResult, fbErr := al.fallback.Execute(
					providerCtx,
					activeCandidates,
					func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
						// FR-007: look up the provider instance that matches
						// this candidate's pinned Provider. Without this,
						// every fallback routes through activeProvider (the
						// primary's instance) — defeating the point of
						// provider-aware fallbacks. Falls back to
						// activeProvider when the candidate has no Provider
						// pinned (legacy wire shape) or no pool entry.
						p := ts.agent.GetProviderForCandidate(providers.FallbackCandidate{Provider: provider, Model: model})
						if p == nil {
							p = activeProvider
						}
						return p.Chat(ctx, messagesForCall, toolDefsForCall, model, llmOpts)
					},
				)
				if fbErr != nil {
					return nil, fbErr
				}
				if fbResult.Provider != "" && len(fbResult.Attempts) > 0 {
					logger.InfoCF(
						"agent",
						fmt.Sprintf("Fallback: succeeded with %s/%s after %d attempts",
							fbResult.Provider, fbResult.Model, len(fbResult.Attempts)+1),
						map[string]any{"agent_id": ts.agent.ID, "iteration": iteration},
					)
				}
				// Phase 1B FR-013: record the model that actually produced
				// the response (may differ from the agent's primary model
				// when a fallback candidate was used).
				ts.setLastProducedModel(fbResult.Model)
				ts.markLastStreamerProducedModel(fbResult.Model)
				return fbResult.Response, nil
			}
			// Use streaming if the provider supports it and we have a streamer for this channel.
			if sp, ok := activeProvider.(providers.StreamingProvider); ok && al.bus != nil {
				logger.DebugCF("agent", "Provider supports streaming, checking for streamer", map[string]any{"channel": ts.channel, "chat_id": ts.chatID})
				if streamer, hasStreamer := al.bus.GetStreamer(providerCtx, ts.channel, ts.chatID, ts.transcriptSessionID); hasStreamer {
					logger.InfoCF("agent", "Using streaming for response", map[string]any{"channel": ts.channel, "chat_id": ts.chatID})
					var lastChunk string
					resp, streamErr := sp.ChatStream(providerCtx, messagesForCall, toolDefsForCall, llmModel, llmOpts, func(accumulated string) {
						// B4: if the turn has been abandoned (stuck-goroutine detach),
						// suppress further frame emits so a zombie goroutine cannot
						// push frames to disconnected clients.
						if ts.abandoned.Load() {
							abandonedWritesSuppressed.Add(1)
							return
						}
						// Send only the new delta (accumulated minus what we already sent)
						delta := accumulated[len(lastChunk):]
						lastChunk = accumulated
						if delta != "" {
							if err := streamer.Update(providerCtx, delta); err != nil {
								logger.DebugCF("agent", "Streaming update error (client may have disconnected)", map[string]any{"error": err.Error()})
							}
						}
					})
					// Do NOT finalize here — the turn may continue with tool calls.
					// Store the streamer so the turn-level code can finalize once,
					// after the last LLM call, preventing premature "done" frames
					// that tell the frontend the response is complete mid-turn.
					ts.setLastStreamer(streamer)
					ts.setLastProducedModel(llmModel)
					// FR-013: also push to the streamer so Finalize stamps the
					// per-turn Model field on the streamed assistant entry.
					ts.markLastStreamerProducedModel(llmModel)
					return resp, streamErr
				}
			}
			ts.setLastProducedModel(llmModel)
			return activeProvider.Chat(providerCtx, messagesForCall, toolDefsForCall, llmModel, llmOpts)
		}

		var response *providers.LLMResponse
		var err error
		maxRetries := 2
		compactionAttemptedOnTimeout := false
		contextCompressionFailed := false // C3: tracks that compression was tried but returned ok=false

		// tryPDFTextFallback is the provider-agnostic safety net for native-PDF
		// rejections. pdfCapableModel cannot perfectly track OpenRouter's
		// per-route capabilities (e.g. Claude Haiku routed via Amazon Bedrock
		// 400s on PDF input), and each provider phrases the rejection
		// differently, so instead of matching error strings it triggers on the
		// STRUCTURAL signal: a terminal failure on a request that carried a
		// native PDF document block. It downgrades the PDF to extracted text —
		// which every model accepts — and retries once. The turn was going to
		// fail anyway, so this can only improve the outcome. Returns true when
		// the retry succeeded (response/err are updated in place).
		tryPDFTextFallback := func() bool {
			if !downgradePDFMediaToText(callMessages) {
				return false
			}
			logger.WarnCF(
				"agent",
				"provider rejected request carrying a native PDF block — retrying with extracted text",
				map[string]any{
					"agent_id": ts.agent.ID,
					"model":    llmModel,
					"error":    err.Error(),
				},
			)
			response, err = callLLM(callMessages, providerToolDefs)
			return err == nil
		}

		for retry := 0; retry <= maxRetries; retry++ {
			response, err = callLLM(callMessages, providerToolDefs)
			if err == nil {
				break
			}
			// If the provider rejected image input on a tool result, strip
			// inline image data URLs from tool messages and retry once. This
			// keeps text-only models working while still giving vision-capable
			// models the picture.
			if strings.Contains(err.Error(), "image input") {
				stripped := false
				for i := range callMessages {
					if callMessages[i].Role == "tool" && len(callMessages[i].Media) > 0 {
						callMessages[i].Media = nil
						stripped = true
					}
				}
				if stripped {
					logger.WarnCF("agent", "provider rejected image input — retrying without media",
						map[string]any{"agent_id": ts.agent.ID, "model": llmModel})
					response, err = callLLM(callMessages, providerToolDefs)
					if err == nil {
						break
					}
				}
			}
			if ts.hardAbortRequested() && errors.Is(err, context.Canceled) {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts)
			}

			// I3: if the FallbackChain already exhausted all candidates, don't retry
			// in the outer loop — the chain already tried everything. Break immediately
			// so the error surfaces to the caller without redundant delay.
			var exhaustedErr *providers.FallbackExhaustedError
			if errors.As(err, &exhaustedErr) {
				// All candidates failed — if the request carried a native PDF
				// block, every provider may have rejected it. Degrade to text
				// and try once more across the chain before surfacing.
				tryPDFTextFallback()
				break
			}

			// Use ClassifyError to distinguish turn-level errors from provider errors.
			// Provider-transient errors (429, 5xx, auth) are handled by the FallbackChain;
			// break here and let the error propagate to the caller.
			//
			// C1: pass the provider name (not the model name) as the second argument.
			// The provider name comes from the first active candidate; fall back to the
			// agent's configured provider field when no candidates are resolved.
			activeProviderName := ""
			if len(activeCandidates) > 0 {
				activeProviderName = activeCandidates[0].Provider
			}
			failErr := providers.ClassifyError(err, activeProviderName, llmModel)

			var isTimeoutError bool
			var isContextError bool
			if failErr != nil {
				isTimeoutError = failErr.Reason == providers.FailoverTimeout
				isContextError = failErr.Reason == providers.FailoverContextOverflow
				// Retriable provider errors (rate limit, auth, overloaded) are handled
				// by the FallbackChain. Don't retry inline — break so the error surfaces.
				if failErr.IsRetriable() && !isTimeoutError {
					break
				}
				// Non-retriable, non-timeout, non-context errors: break immediately.
				// First, if the request carried a native PDF block, try the
				// provider-agnostic PDF→text fallback once — a terminal error
				// here is very likely a PDF-input rejection.
				if !isTimeoutError && !isContextError {
					tryPDFTextFallback()
					break
				}
			} else {
				// ClassifyError returned nil: unknown error. Don't retry.
				break
			}

			if isTimeoutError && retry < maxRetries {
				// FIX 2: re-check hard-abort FIRST. A user cancel mid-stream can
				// surface as a transport-drop string (classified FailoverTimeout)
				// rather than context.Canceled. Without this guard the branch would
				// emit a spurious "Retrying…" message + stray LLMRetry/TurnTimeout
				// events before the canceled turnCtx collapses the backoff. Breaking
				// here lets the canceled turn finalize quietly.
				if ts.hardAbortRequested() {
					break
				}
				// FIX 1: only inline-retry a transport-drop when NO partial content
				// was already streamed to the client for this attempt. If tokens were
				// already streamed, the dropped attempt sent no `done` frame, so the
				// SPA's bubble stays in "streaming" state; re-streaming the full
				// response on retry would concatenate attempt-2 onto attempt-1 and
				// visibly duplicate text. In that case break instead — the turn
				// surfaces the error normally and the SPA finalizes the bubble as
				// interrupted. When there is no active streamer (non-streaming /
				// Chat path) or it streamed nothing yet (drop before the first
				// token — the common, safe case), retry as before.
				if sc, ok := ts.lastStreamer.(interface{ StreamedContentLen() int }); ok && sc.StreamedContentLen() > 0 {
					logger.WarnCF("agent", "Transport drop after partial stream; not inline-retrying to avoid duplicated text", map[string]any{
						"agent_id":  ts.agent.ID,
						"iteration": iteration,
						"streamed":  sc.StreamedContentLen(),
						"error":     err.Error(),
					})
					break
				}
				// I1: emit EventKindTurnTimeout when a timeout error is detected.
				al.emitEvent(
					EventKindTurnTimeout,
					ts.eventMeta("runTurn", "turn.timeout"),
					TurnTimeoutPayload{
						TimeoutSeconds: ts.agent.TimeoutSeconds,
						Compacted:      compactionAttemptedOnTimeout,
						Retried:        retry > 0,
					},
				)
				// Timeout recovery: compact context if it's heavily loaded, then retry once.
				if !compactionAttemptedOnTimeout && ts.agent.SummarizeTokenPercent > 0 && !ts.opts.NoHistory {
					toolDefs := ts.agent.Tools.ToProviderDefs()
					if isOverContextBudget(
						ts.agent.ContextWindow*ts.agent.SummarizeTokenPercent/100,
						callMessages, toolDefs, ts.agent.MaxTokens,
					) {
						compactionAttemptedOnTimeout = true
						if compression, ok := al.forceCompression(ts.agent, ts.sessionKey); ok {
							al.emitEvent(
								EventKindContextCompress,
								ts.eventMeta("runTurn", "turn.context.compress"),
								ContextCompressPayload{
									Reason:            ContextCompressReasonRetry,
									DroppedMessages:   compression.DroppedMessages,
									RemainingMessages: compression.RemainingMessages,
								},
							)
							// I1: emit EventKindCompactionRetry when compaction is triggered
							// during timeout recovery (separate from the general compress event).
							al.emitEvent(
								EventKindCompactionRetry,
								ts.eventMeta("runTurn", "turn.compaction_retry"),
								CompactionRetryPayload{
									DroppedMessages:   compression.DroppedMessages,
									RemainingMessages: compression.RemainingMessages,
								},
							)
							ts.refreshRestorePointFromSession(ts.agent)
							newHistory := ts.agent.Sessions.GetHistory(ts.sessionKey)
							newSummary := ts.agent.Sessions.GetSummary(ts.sessionKey)
							messages = ts.agent.ContextBuilder.BuildMessages(
								newHistory, newSummary, "",
								nil, ts.channel, ts.chatID, ts.opts.SenderID, ts.opts.SenderDisplayName,
								activeSkillNames(ts.agent, ts.opts)...,
							)
							callMessages = messages
							if gracefulTerminal {
								callMessages = append(append([]providers.Message(nil), messages...), ts.interruptHintMessage())
							}
						} else {
							// Compaction failed: return partial content + timeout message.
							logger.WarnCF("agent", "Compaction failed during timeout recovery; returning partial response",
								map[string]any{"agent_id": ts.agent.ID, "iteration": iteration})
							break
						}
					}
				}

				// Exponential backoff with full jitter (base 2s, max 30s).
				base := 2 * time.Second
				calculated := base * (1 << uint(retry)) // 2^retry * base
				if calculated > 30*time.Second {
					calculated = 30 * time.Second
				}
				jitter := time.Duration(rand.Int64N(int64(calculated) + 1))
				// M3: enforce a minimum backoff floor of 500ms so jitter can never produce
				// a zero or near-zero delay (rand.Int64N(1) == 0 when calculated == 0).
				backoff := jitter
				if backoff < 500*time.Millisecond {
					backoff = 500 * time.Millisecond
				}
				al.emitEvent(
					EventKindLLMRetry,
					ts.eventMeta("runTurn", "turn.llm.retry"),
					LLMRetryPayload{
						Attempt:    retry + 1,
						MaxRetries: maxRetries,
						Reason:     "timeout",
						Error:      err.Error(),
						Backoff:    backoff,
					},
				)
				if retry == 0 && !constants.IsInternalChannel(ts.channel) {
					if notifyErr := al.bus.PublishOutbound(turnCtx, bus.OutboundMessage{
						Channel: ts.channel,
						ChatID:  ts.chatID,
						Content: "Retrying — please wait...",
					}); notifyErr != nil {
						logger.WarnCF("agent", "Failed to send retry indicator",
							map[string]any{"channel": ts.channel, "error": notifyErr.Error()})
					}
				}
				logger.WarnCF("agent", "Timeout error, retrying after backoff", map[string]any{
					"error":   err.Error(),
					"retry":   retry,
					"backoff": backoff.String(),
				})
				if sleepErr := sleepWithContext(turnCtx, backoff); sleepErr != nil {
					if ts.hardAbortRequested() {
						turnStatus = TurnEndStatusAborted
						return al.abortTurn(ts)
					}
					err = sleepErr
					break
				}
				continue
			}

			if isContextError && retry < maxRetries && !ts.opts.NoHistory {
				// C3: if a previous compression attempt returned ok=false and we're
				// still getting context errors, retrying with identical data won't help.
				// Break to surface the error rather than burning the remaining budget.
				if contextCompressionFailed {
					logger.WarnCF("agent", "Context overflow persists after failed compression; aborting retry",
						map[string]any{"agent_id": ts.agent.ID, "iteration": iteration, "retry": retry})
					break
				}
				al.emitEvent(
					EventKindLLMRetry,
					ts.eventMeta("runTurn", "turn.llm.retry"),
					LLMRetryPayload{
						Attempt:    retry + 1,
						MaxRetries: maxRetries,
						Reason:     "context_limit",
						Error:      err.Error(),
					},
				)
				logger.WarnCF(
					"agent",
					"Context window error detected, attempting compression",
					map[string]any{
						"error": err.Error(),
						"retry": retry,
					},
				)

				if retry == 0 && !constants.IsInternalChannel(ts.channel) {
					if notifyErr := al.bus.PublishOutbound(turnCtx, bus.OutboundMessage{
						Channel: ts.channel,
						ChatID:  ts.chatID,
						Content: "Context window exceeded. Compressing history and retrying...",
					}); notifyErr != nil {
						logger.WarnCF("agent", "Failed to notify user of context compression",
							map[string]any{"channel": ts.channel, "error": notifyErr.Error()})
					}
				}

				if compression, ok := al.forceCompression(ts.agent, ts.sessionKey); ok {
					al.emitEvent(
						EventKindContextCompress,
						ts.eventMeta("runTurn", "turn.context.compress"),
						ContextCompressPayload{
							Reason:            ContextCompressReasonRetry,
							DroppedMessages:   compression.DroppedMessages,
							RemainingMessages: compression.RemainingMessages,
						},
					)
					ts.refreshRestorePointFromSession(ts.agent)
				} else {
					// C3: compaction returned ok=false (nothing to compress). Mark the
					// flag so the NEXT retry attempt will break rather than burning more
					// budget on identical data. We still allow this single retry through
					// because the provider might succeed without context reduction.
					contextCompressionFailed = true
					logger.WarnCF("agent", "Compaction failed during context overflow recovery; will not retry further",
						map[string]any{"agent_id": ts.agent.ID, "iteration": iteration})
				}

				newHistory := ts.agent.Sessions.GetHistory(ts.sessionKey)
				newSummary := ts.agent.Sessions.GetSummary(ts.sessionKey)
				messages = ts.agent.ContextBuilder.BuildMessages(
					newHistory, newSummary, "",
					nil, ts.channel, ts.chatID, ts.opts.SenderID, ts.opts.SenderDisplayName,
					activeSkillNames(ts.agent, ts.opts)...,
				)
				callMessages = messages
				if gracefulTerminal {
					callMessages = append(append([]providers.Message(nil), messages...), ts.interruptHintMessage())
				}
				continue
			}
			break
		}

		if err != nil {
			// C2: check for context cancellation/timeout before reporting a generic
			// "LLM call failed" error — these are user/system actions, not LLM failures.
			if errors.Is(err, context.Canceled) {
				return turnResult{}, fmt.Errorf("turn canceled")
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return turnResult{}, fmt.Errorf("turn timed out")
			}
			// Friendly degradation for non-vision models. A user attached an image
			// to a model that can't view images, so the provider returned a raw
			// 400 whose message specifically indicates a missing vision capability
			// (e.g. "'claude-3-5-haiku-...' does not support image input.").
			// isImageInputRejection uses a narrow match: errors about corrupt
			// images, oversized files, or content-moderation blocks are NOT
			// intercepted here and fall through to the normal error path.
			// Images have no text fallback (unlike PDFs), and the tool-result
			// image-strip above only covers agent-generated images — so instead
			// of surfacing the raw error, synthesize a clear, actionable
			// assistant reply and fall through to the normal emit path
			// (streamed/published like any other response).
			if isImageInputRejection(err) {
				logger.WarnCF("agent", "model rejected image input — returning guidance instead of error",
					map[string]any{"agent_id": ts.agent.ID, "model": llmModel, "error": err.Error()})
				response = &providers.LLMResponse{
					Content: fmt.Sprintf(
						"I can't view images with the current model (%s). To work with images, switch this "+
							"agent to a model that supports image input, then try again.",
						llmModel,
					),
				}
				err = nil
				// The synthesized guidance above is NOT produced by llmModel — the
				// provider refused the call entirely. Stamp a sentinel so the
				// transcript model field does NOT mis-attribute this turn to llmModel
				// (silent-failure-A #5). The original error stays in the warn log
				// above so operators can debug; we surface a debug-level marker here
				// for search/discovery.
				ts.setLastProducedModel("(image-rejection synthesis)")
				logger.DebugCF("agent", "image-rejection synthesis stamped; llmModel retained in error log only",
					map[string]any{"agent_id": ts.agent.ID, "model": llmModel})
				// fall through to the normal post-LLM handling below.
			}
		}
		if err != nil {
			turnStatus = TurnEndStatusError
			al.emitEvent(
				EventKindError,
				ts.eventMeta("runTurn", "turn.error"),
				ErrorPayload{
					Stage:   "llm",
					Message: err.Error(),
				},
			)
			// FIX-1 / FR-002: persist the provider error to the transcript (see
			// appendErrorTranscript docstring for rationale).
			ts.appendErrorTranscript(
				EventKindError.String(), "runTurn",
				fmt.Sprintf("LLM call failed after retries: %s", err.Error()),
			)
			logger.ErrorCF("agent", "LLM call failed",
				map[string]any{
					"agent_id":  ts.agent.ID,
					"iteration": iteration,
					"model":     llmModel,
					"error":     err.Error(),
				})
			return turnResult{}, fmt.Errorf("LLM call failed after retries: %w", err)
		}

		if al.hooks != nil {
			llmResp, decision := al.hooks.AfterLLM(turnCtx, &LLMHookResponse{
				Meta:     ts.eventMeta("runTurn", "turn.llm.response"),
				Model:    llmModel,
				Response: response,
				Channel:  ts.channel,
				ChatID:   ts.chatID,
			})
			switch decision.normalizedAction() {
			case HookActionContinue, HookActionModify:
				if llmResp != nil && llmResp.Response != nil {
					response = llmResp.Response
				}
			case HookActionAbortTurn:
				turnStatus = TurnEndStatusError
				return turnResult{}, al.hookAbortError(ts, "after_llm", decision)
			case HookActionHardAbort:
				_ = ts.requestHardAbort()
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts)
			}
		}

		// Save finishReason to turnState for SubTurn truncation detection.
		// H5: use turnCtx (the per-turn context that carries the turnState value),
		// not the outer ctx which may not have the turnState attached.
		if innerTS := turnStateFromContext(turnCtx); innerTS != nil {
			innerTS.SetLastFinishReason(response.FinishReason)
			// Save usage for token budget tracking
			if response.Usage != nil {
				innerTS.SetLastUsage(response.Usage)
			}
		}

		reasoningContent := response.Reasoning
		if reasoningContent == "" {
			reasoningContent = response.ReasoningContent
		}
		go al.handleReasoning(
			turnCtx,
			reasoningContent,
			ts.channel,
			al.targetReasoningChannelID(ts.channel),
		)
		al.emitEvent(
			EventKindLLMResponse,
			ts.eventMeta("runTurn", "turn.llm.response"),
			LLMResponsePayload{
				ContentLen:   len(response.Content),
				ToolCalls:    len(response.ToolCalls),
				HasReasoning: response.Reasoning != "" || response.ReasoningContent != "",
			},
		)

		llmResponseFields := map[string]any{
			"agent_id":       ts.agent.ID,
			"iteration":      iteration,
			"content_chars":  len(response.Content),
			"tool_calls":     len(response.ToolCalls),
			"reasoning":      response.Reasoning,
			"target_channel": al.targetReasoningChannelID(ts.channel),
			"channel":        ts.channel,
		}
		if response.Usage != nil {
			llmResponseFields["prompt_tokens"] = response.Usage.PromptTokens
			llmResponseFields["completion_tokens"] = response.Usage.CompletionTokens
			llmResponseFields["total_tokens"] = response.Usage.TotalTokens
		}
		logger.DebugCF("agent", "LLM response", llmResponseFields)

		// SEC-26: Record the cost of this completed LLM call in the daily
		// accumulator. We MUST use RecordSpend (not CheckGlobalCostCap) here:
		// the call already happened, so the spend must be recorded even if it
		// pushes the total past the cap — the next turn's pre-check will deny
		// further calls. CheckGlobalCostCap silently skipped the increment on
		// denials, which caused the accumulator to stick below the cap and let
		// every subsequent call sneak through.
		if al.rateLimiter != nil && response != nil && response.Usage != nil {
			callCost := estimateLLMCallCost(llmModel, response.Usage)
			al.rateLimiter.RecordSpend(callCost, ts.agent.AgentType)
			// Accumulate turn-level stats so the "done" WS frame can surface
			// real token counts and cost to the chat UI (issue #12).
			ts.AddTurnStats(int64(response.Usage.TotalTokens), callCost)
			if al.costTracker != nil {
				if saveErr := al.costTracker.SaveFromRegistry(al.rateLimiter); saveErr != nil {
					logger.ErrorCF("agent", "SEC-26: failed to persist daily cost after LLM call — cap may under-count on restart",
						map[string]any{
							"error":          saveErr.Error(),
							"agent_id":       ts.agent.ID,
							"call_cost_usd":  callCost,
							"daily_cost_usd": al.rateLimiter.GetDailyCost(),
							"model":          llmModel,
						})
				}
			}
		}

		if len(response.ToolCalls) == 0 || gracefulTerminal {
			responseContent := response.Content
			if responseContent == "" && response.ReasoningContent != "" {
				responseContent = response.ReasoningContent
			}
			if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
				logger.InfoCF("agent", "Steering arrived after direct LLM response; continuing turn",
					map[string]any{
						"agent_id":       ts.agent.ID,
						"iteration":      iteration,
						"steering_count": len(steerMsgs),
					})
				pendingMessages = append(pendingMessages, steerMsgs...)
				continue
			}
			// Empty response recovery (FR-006): if LLM returned empty content with no
			// reasoning and no tool calls, retry once before surfacing a fallback message.
			//
			// H3: perform the retry in an inner loop that calls callLLM directly, so we
			// do NOT increment the outer iteration counter (which would consume the agent's
			// MaxIterations budget for what is purely a provider-level retry).
			for strings.TrimSpace(responseContent) == "" && emptyResponseRetries < maxEmptyResponseRetries {
				emptyResponseRetries++
				logger.WarnCF("agent", "Empty response from LLM, retrying", map[string]any{
					"agent_id":  ts.agent.ID,
					"iteration": iteration,
					"attempt":   emptyResponseRetries,
				})
				al.emitEvent(
					EventKindLLMRetry,
					ts.eventMeta("runTurn", "turn.llm.retry"),
					LLMRetryPayload{
						Attempt:    emptyResponseRetries,
						MaxRetries: maxEmptyResponseRetries,
						Reason:     "empty_response",
					},
				)
				// I1: also emit the dedicated EventKindEmptyResponseRetry for subscribers
				// that specifically track empty-response retry behavior.
				al.emitEvent(
					EventKindEmptyResponseRetry,
					ts.eventMeta("runTurn", "turn.empty_response_retry"),
					EmptyResponseRetryPayload{
						Attempt:    emptyResponseRetries,
						MaxRetries: maxEmptyResponseRetries,
					},
				)
				// Re-call the LLM directly without advancing the outer turn iteration.
				retryResp, retryErr := callLLM(callMessages, providerToolDefs)
				if retryErr != nil {
					// Propagate the error back to the outer error-handling block by
					// overwriting response/err and breaking out of both loops.
					response = nil
					err = retryErr
					break
				}
				response = retryResp
				responseContent = response.Content
				if responseContent == "" && response.ReasoningContent != "" {
					responseContent = response.ReasoningContent
				}
			}
			// If the inner retry loop set an error, surface it via the outer error path.
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return turnResult{}, fmt.Errorf("turn canceled")
				}
				if errors.Is(err, context.DeadlineExceeded) {
					return turnResult{}, fmt.Errorf("turn timed out")
				}
				turnStatus = TurnEndStatusError
				al.emitEvent(
					EventKindError,
					ts.eventMeta("runTurn", "turn.error"),
					ErrorPayload{Stage: "llm_empty_retry", Message: err.Error()},
				)
				// FIX-1 / FR-002: persist this provider error to the transcript (see
				// appendErrorTranscript docstring for rationale).
				ts.appendErrorTranscript(
					EventKindError.String(), "runTurn",
					fmt.Sprintf("LLM call failed during empty-response retry: %s", err.Error()),
				)
				return turnResult{}, fmt.Errorf("LLM call failed during empty-response retry: %w", err)
			}
			if strings.TrimSpace(responseContent) == "" {
				responseContent = defaultResponse
				logger.WarnCF("agent", "LLM returned empty response after retry; using fallback message",
					map[string]any{"agent_id": ts.agent.ID, "iteration": iteration})
			}
			finalContent = responseContent
			logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
				map[string]any{
					"agent_id":      ts.agent.ID,
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})
			break
		}

		normalizedToolCalls := make([]providers.ToolCall, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			normalizedToolCalls = append(normalizedToolCalls, providers.NormalizeToolCall(tc))
		}

		toolNames := make([]string, 0, len(normalizedToolCalls))
		for _, tc := range normalizedToolCalls {
			toolNames = append(toolNames, tc.Name)
		}
		logger.InfoCF("agent", "LLM requested tool calls",
			map[string]any{
				"agent_id":  ts.agent.ID,
				"tools":     toolNames,
				"count":     len(normalizedToolCalls),
				"iteration": iteration,
			})

		assistantMsg := providers.Message{
			Role:             "assistant",
			Content:          response.Content,
			ReasoningContent: response.ReasoningContent,
		}
		for _, tc := range normalizedToolCalls {
			argumentsJSON, marshalErr := json.Marshal(tc.Arguments)
			if marshalErr != nil {
				logger.WarnCF("agent", "failed to marshal tool call arguments", map[string]any{"tool": tc.Name, "error": marshalErr.Error()})
				argumentsJSON = []byte("{}")
			}
			extraContent := tc.ExtraContent
			thoughtSignature := ""
			if tc.Function != nil {
				thoughtSignature = tc.Function.ThoughtSignature
			}
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Name: tc.Name,
				Function: &providers.FunctionCall{
					Name:             tc.Name,
					Arguments:        string(argumentsJSON),
					ThoughtSignature: thoughtSignature,
				},
				ExtraContent:     extraContent,
				ThoughtSignature: thoughtSignature,
			})
		}
		messages = append(messages, assistantMsg)
		if !ts.opts.NoHistory {
			ts.agent.Sessions.AddFullMessage(ts.sessionKey, assistantMsg)
			ts.recordPersistedMessage(assistantMsg)
		}

		// Bug #416 fix: persist the narration text the LLM emitted alongside
		// this round's tool calls. Without this, only the FINAL iteration's text
		// reaches the transcript — intermediate "Okay, I've saved X." sentences
		// are shown live via wsStreamer.Update but never written to transcript.jsonl.
		//
		// We write BEFORE the tool_call entries so the transcript order mirrors
		// the live stream: [text segment N] → [tool_call round N] → …
		//
		// Tokens/cost are 0 here — the turn total is attributed to the final
		// assistant entry only (wsStreamer.Finalize or appendAssistantTranscript).
		ts.appendIntermediateAssistantTranscript(response.Content)
		if response.Content != "" {
			// The narration is now in the transcript. If this round's streamer
			// ends up being finalized (the turn exits via max_tool_iterations
			// exhaustion, where the last executed round is a tool-call round),
			// suppress its duplicate transcript write (#416 gate fix). The check
			// mirrors appendIntermediateAssistantTranscript's own content==""
			// early-return so the mark fires only when a write actually happened.
			ts.markLastStreamerTranscriptPersisted()
		}

		ts.setPhase(TurnPhaseTools)
		for i, tc := range normalizedToolCalls {
			if ts.hardAbortRequested() {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts)
			}

			// Unsanitize tool name from LLM — dots were replaced with underscores
			// for Anthropic/Azure API compatibility (e.g., "browser_navigate" → "browser.navigate").
			toolName := ts.agent.Tools.UnsanitizeToolName(tc.Name)
			toolArgs := cloneStringAnyMap(tc.Arguments)

			if al.hooks != nil {
				toolReq, decision := al.hooks.BeforeTool(turnCtx, &ToolCallHookRequest{
					Meta:      ts.eventMeta("runTurn", "turn.tool.before"),
					Tool:      toolName,
					Arguments: toolArgs,
					Channel:   ts.channel,
					ChatID:    ts.chatID,
				})
				switch decision.normalizedAction() {
				case HookActionContinue, HookActionModify:
					if toolReq != nil {
						toolName = toolReq.Tool
						toolArgs = toolReq.Arguments
					}
				case HookActionDenyTool:
					denyContent := hookDeniedToolContent("Tool execution denied by hook", decision.Reason)
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   toolName,
							Reason: denyContent,
						},
					)
					deniedMsg := providers.Message{
						Role:       "tool",
						Content:    denyContent,
						ToolCallID: tc.ID,
					}
					messages = append(messages, deniedMsg)
					if !ts.opts.NoHistory {
						ts.agent.Sessions.AddFullMessage(ts.sessionKey, deniedMsg)
						ts.recordPersistedMessage(deniedMsg)
					}
					continue
				case HookActionAbortTurn:
					turnStatus = TurnEndStatusError
					return turnResult{}, al.hookAbortError(ts, "before_tool", decision)
				case HookActionHardAbort:
					_ = ts.requestHardAbort()
					turnStatus = TurnEndStatusAborted
					return al.abortTurn(ts)
				}
			}

			if al.hooks != nil {
				approval := al.hooks.ApproveTool(turnCtx, &ToolApprovalRequest{
					Meta:      ts.eventMeta("runTurn", "turn.tool.approve"),
					Tool:      toolName,
					Arguments: toolArgs,
					Channel:   ts.channel,
					ChatID:    ts.chatID,
					SessionID: ts.transcriptSessionID,
				})
				if !approval.IsApproved() {
					denyContent := hookDeniedToolContent("Tool execution denied by approval hook", approval.Reason)
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   toolName,
							Reason: denyContent,
						},
					)
					deniedMsg := providers.Message{
						Role:       "tool",
						Content:    denyContent,
						ToolCallID: tc.ID,
					}
					messages = append(messages, deniedMsg)
					if !ts.opts.NoHistory {
						ts.agent.Sessions.AddFullMessage(ts.sessionKey, deniedMsg)
						ts.recordPersistedMessage(deniedMsg)
					}
					continue
				}
			}

			// FR-079 (M2): TOCTOU re-check. Re-load the policy pointer and re-resolve
			// the effective policy for this specific tool right before execution.
			// This closes the window between filter-time tools[] assembly and execution.
			toctouPolicy := al.resolveToolPolicyAtExec(ts, toolName, filterTimePolicyMap)
			if toctouPolicy == "deny" {
				// Policy flipped to deny between filter-time and exec-time.
				denyMsg := fmt.Sprintf(`{"error":"permission_denied","message":"Tool execution denied by policy.","tool":%q}`, toolName)
				al.emitPolicyDenyAudit(ts, toolName, "deny", "mid_turn_policy_change")
				deniedMsg := providers.Message{
					Role:       "tool",
					Content:    denyMsg,
					ToolCallID: tc.ID,
				}
				messages = append(messages, deniedMsg)
				if !ts.opts.NoHistory {
					ts.agent.Sessions.AddFullMessage(ts.sessionKey, deniedMsg)
					ts.recordPersistedMessage(deniedMsg)
				}
				al.emitEvent(
					EventKindToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   toolName,
						Reason: "permission_denied (mid-turn policy change)",
					},
				)
				if shouldAbort, _ := al.recordSyntheticDeny(ts); shouldAbort {
					turnStatus = TurnEndStatusAborted
					return al.abortTurn(ts)
				}
				continue
			}
			if toctouPolicy == "ask" {
				// Headless auto-deny (issue #264, FR-009): a scheduled run has no
				// operator to approve, so any `ask`-policy tool is denied without
				// ever issuing an approval request — the run must never stall.
				if ts.opts.AutoDenyAsk {
					const denialReason = "auto-denied: ask-policy tool not allowed in a headless scheduled run"
					denyMsg := fmt.Sprintf(`{"error":"permission_denied","message":"User denied tool execution.","tool":%q,"reason":%q}`, toolName, denialReason)
					// Build optional extra Details for the deny.attempted entry so
					// both correlated records carry the schedule identity (O-3 / F-13
					// / issue #342). scheduledJobContextFrom is a no-op read — safe to
					// call even when no job info was injected.
					var denyExtra map[string]any
					if jobInfo, ok := scheduledJobContextFrom(turnCtx); ok && jobInfo.JobID != "" {
						denyExtra = map[string]any{
							"schedule_job_id":   jobInfo.JobID,
							"schedule_job_name": jobInfo.JobName,
						}
					}
					al.emitPolicyDenyAudit(ts, toolName, "ask", denialReason, denyExtra)
					// O-3 / F-13 / issue #342: emit the canonical tool.policy.ask.denied
					// entry via EmitToolPolicyAskDenied (CRIT-6 compliant, INFO severity,
					// reason=AskDenyReasonScheduled). See emitScheduledAutoDenyAudit.
					al.emitScheduledAutoDenyAudit(turnCtx, ts, toolName, tc.ID)
					deniedMsg := providers.Message{
						Role:       "tool",
						Content:    denyMsg,
						ToolCallID: tc.ID,
					}
					messages = append(messages, deniedMsg)
					if !ts.opts.NoHistory {
						ts.agent.Sessions.AddFullMessage(ts.sessionKey, deniedMsg)
						ts.recordPersistedMessage(deniedMsg)
					}
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   toolName,
							Reason: fmt.Sprintf("permission_denied (ask auto-denied: %s)", denialReason),
						},
					)
					if shouldAbort, _ := al.recordSyntheticDeny(ts); shouldAbort {
						turnStatus = TurnEndStatusAborted
						return al.abortTurn(ts)
					}
					continue
				}
				// ask-policy: pause and request human approval (FR-011).
				approver := al.loadToolApprover()
				approved, denialReason := approver.RequestApproval(turnCtx, PolicyApprovalReq{
					ToolCallID:    tc.ID,
					ToolName:      toolName,
					Args:          cloneStringAnyMap(toolArgs),
					AgentID:       ts.agentID,
					SessionID:     ts.sessionKey,
					TurnID:        ts.turnID,
					RequiresAdmin: al.toolRequiresAdmin(ts, toolName),
				})
				if !approved {
					denyMsg := fmt.Sprintf(`{"error":"permission_denied","message":"User denied tool execution.","tool":%q,"reason":%q}`, toolName, denialReason)
					al.emitPolicyDenyAudit(ts, toolName, "ask", denialReason)
					deniedMsg := providers.Message{
						Role:       "tool",
						Content:    denyMsg,
						ToolCallID: tc.ID,
					}
					messages = append(messages, deniedMsg)
					if !ts.opts.NoHistory {
						ts.agent.Sessions.AddFullMessage(ts.sessionKey, deniedMsg)
						ts.recordPersistedMessage(deniedMsg)
					}
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   toolName,
							Reason: fmt.Sprintf("permission_denied (ask denied: %s)", denialReason),
						},
					)
					if shouldAbort, _ := al.recordSyntheticDeny(ts); shouldAbort {
						turnStatus = TurnEndStatusAborted
						return al.abortTurn(ts)
					}
					continue
				}
				// Approved: fall through to execute.
			}

			argsJSON, marshalErr := json.Marshal(toolArgs)
			if marshalErr != nil {
				logger.WarnCF("agent", "failed to marshal tool args for preview", map[string]any{"tool": toolName, "error": marshalErr.Error()})
				argsJSON = []byte("{}")
			}
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", toolName, argsPreview),
				map[string]any{
					"agent_id":  ts.agent.ID,
					"tool":      toolName,
					"iteration": iteration,
				})
			al.emitEvent(
				EventKindToolExecStart,
				ts.eventMeta("runTurn", "turn.tool.start"),
				ToolExecStartPayload{
					ToolCallID:        session.ToolCallID(tc.ID),
					ChatID:            ts.chatID,
					SessionID:         ts.transcriptSessionID,
					Tool:              toolName,
					Arguments:         cloneEventArguments(toolArgs),
					ParentSpawnCallID: session.ToolCallID(ts.parentSpawnCallID),
					AgentID:           ts.resolveActiveAgentID(), // Bug 1: runtime-current agent
				},
			)

			// Send tool feedback to chat channel if enabled
			if cfg.Agents.Defaults.IsToolFeedbackEnabled() &&
				ts.channel != "" &&
				!ts.opts.SuppressToolFeedback {
				feedbackPreview := utils.Truncate(
					string(argsJSON),
					cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength(),
				)
				feedbackMsg := fmt.Sprintf("[tool] `%s`\n```\n%s\n```", tc.Name, feedbackPreview)
				fbCtx, fbCancel := context.WithTimeout(turnCtx, 3*time.Second)
				if fbErr := al.bus.PublishOutbound(fbCtx, bus.OutboundMessage{
					Channel: ts.channel,
					ChatID:  ts.chatID,
					Content: feedbackMsg,
				}); fbErr != nil {
					logger.WarnCF("agent", "Failed to publish tool feedback",
						map[string]any{"tool": tc.Name, "channel": ts.channel, "error": fbErr.Error()})
				}
				fbCancel()
			}

			toolCallID := tc.ID
			toolIteration := iteration
			asyncToolName := toolName
			asyncCallback := func(_ context.Context, result *tools.ToolResult) {
				// Send ForUser content directly to the user (immediate feedback),
				// mirroring the synchronous tool execution path.
				if !result.Silent && result.ForUser != "" {
					outCtx, outCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer outCancel()
					// M1: capture and log publish errors instead of silently discarding them.
					if pubErr := al.bus.PublishOutbound(outCtx, bus.OutboundMessage{
						Channel: ts.channel,
						ChatID:  ts.chatID,
						Content: result.ForUser,
					}); pubErr != nil {
						logger.WarnCF("agent", "Async tool ForUser content failed to publish",
							map[string]any{
								"tool":    asyncToolName,
								"channel": ts.channel,
								"error":   pubErr.Error(),
							})
					}
				}

				// Determine content for the agent loop (ForLLM or error).
				content := result.ContentForLLM()
				if content == "" {
					return
				}

				// Filter sensitive data before publishing
				content = cfg.FilterSensitiveData(content)

				logger.InfoCF("agent", "Async tool completed, publishing result",
					map[string]any{
						"tool":        asyncToolName,
						"content_len": len(content),
						"channel":     ts.channel,
					})
				al.emitEvent(
					EventKindFollowUpQueued,
					ts.scope.meta(toolIteration, "runTurn", "turn.follow_up.queued"),
					FollowUpQueuedPayload{
						SourceTool: asyncToolName,
						Channel:    ts.channel,
						ChatID:     ts.chatID,
						ContentLen: len(content),
					},
				)

				pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer pubCancel()
				if pubErr := al.bus.PublishInbound(pubCtx, bus.InboundMessage{
					Channel:  "system",
					SenderID: fmt.Sprintf("async:%s", asyncToolName),
					ChatID:   fmt.Sprintf("%s:%s", ts.channel, ts.chatID),
					Content:  content,
				}); pubErr != nil {
					logger.ErrorCF("agent", "Failed to publish async tool result; result permanently lost",
						map[string]any{"tool": asyncToolName, "channel": ts.channel, "error": pubErr.Error()})
				}
			}

			// SEC-26: Per-agent tool call rate limit check. The system agent is exempt.
			if al.rateLimiter != nil && cfg.Sandbox.RateLimits.MaxAgentToolCallsPerMinute > 0 &&
				!security.IsPrivilegedAgent(ts.agent.AgentType) {
				toolWindow := al.rateLimiter.GetOrCreate(
					"agent:"+ts.agent.ID+":tool_call",
					cfg.Sandbox.RateLimits.MaxAgentToolCallsPerMinute,
					time.Minute,
					security.ScopeAgent,
					ts.agent.ID,
					"tool_call",
				)
				if toolRLResult := toolWindow.Allow(); !toolRLResult.Allowed {
					al.recordRateLimitDenial(
						ts,
						"agent_tool_calls_per_minute",
						RateLimitPayload{
							Scope:             string(security.ScopeAgent),
							Resource:          "tool_call",
							PolicyRule:        toolRLResult.PolicyRule,
							RetryAfterSeconds: toolRLResult.RetryAfterSeconds,
							AgentID:           ts.agent.ID,
							ChatID:            ts.chatID,
							Tool:              toolName,
						},
						map[string]any{"retry_after_seconds": toolRLResult.RetryAfterSeconds},
					)
					// Soft denial: the tool call is rejected (fail closed — the tool
					// does not execute) but the denial is surfaced as a tool-result
					// error rather than aborting the turn, so the LLM can react
					// (e.g. inform the user, back off). Contrast with the LLM-call
					// rate limit above, which aborts the turn entirely.
					errMsg := fmt.Sprintf("Rate limited: %s (retry after %.0fs)",
						toolRLResult.PolicyRule, toolRLResult.RetryAfterSeconds)
					deniedMsg := providers.Message{
						Role:       "tool",
						Content:    errMsg,
						ToolCallID: tc.ID,
					}
					messages = append(messages, deniedMsg)
					if !ts.opts.NoHistory {
						ts.agent.Sessions.AddFullMessage(ts.sessionKey, deniedMsg)
						ts.recordPersistedMessage(deniedMsg)
					}
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   toolName,
							Reason: errMsg,
						},
					)
					continue
				}
			}

			toolStart := time.Now()
			// Inject the current tool call's ID into the context so that tools like
			// spawn can read it as their parentSpawnCallID when they in turn call
			// SpawnSubTurn (FR-H-003).
			execCtx := withSpawnToolCallID(turnCtx, tc.ID)
			toolResult := ts.agent.Tools.ExecuteWithContext(
				execCtx,
				toolName,
				toolArgs,
				ts.channel,
				ts.chatID,
				asyncCallback,
			)
			toolDuration := time.Since(toolStart)

			if ts.hardAbortRequested() {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts)
			}

			if al.hooks != nil {
				toolResp, decision := al.hooks.AfterTool(turnCtx, &ToolResultHookResponse{
					Meta:      ts.eventMeta("runTurn", "turn.tool.after"),
					Tool:      toolName,
					Arguments: toolArgs,
					Result:    toolResult,
					Duration:  toolDuration,
					Channel:   ts.channel,
					ChatID:    ts.chatID,
				})
				switch decision.normalizedAction() {
				case HookActionContinue, HookActionModify:
					if toolResp != nil {
						if toolResp.Tool != "" {
							toolName = toolResp.Tool
						}
						if toolResp.Result != nil {
							toolResult = toolResp.Result
						}
					}
				case HookActionAbortTurn:
					turnStatus = TurnEndStatusError
					return turnResult{}, al.hookAbortError(ts, "after_tool", decision)
				case HookActionHardAbort:
					_ = ts.requestHardAbort()
					turnStatus = TurnEndStatusAborted
					return al.abortTurn(ts)
				}
			}

			if toolResult == nil {
				toolResult = tools.ErrorResult("hook returned nil tool result")
			}
			// Always deliver any media the tool produced AND tag the result with
			// artifact references so the LLM can reason about them in the
			// follow-up call. The follow-up call itself is now unconditional —
			// the model decides whether to add a caption, emit empty content,
			// or run more tools.
			if len(toolResult.Media) > 0 {
				parts := make([]bus.MediaPart, 0, len(toolResult.Media))
				for _, ref := range toolResult.Media {
					part := bus.MediaPart{Ref: ref}
					if turnMediaStore != nil {
						if _, meta, err := turnMediaStore.ResolveWithMeta(ref); err == nil {
							part.Filename = meta.Filename
							part.ContentType = meta.ContentType
							part.Type = inferMediaType(meta.Filename, meta.ContentType)
						}
					}
					parts = append(parts, part)
				}
				outboundMedia := bus.OutboundMediaMessage{
					Channel:   ts.channel,
					ChatID:    ts.chatID,
					SessionID: ts.transcriptSessionID,
					Parts:     parts,
				}
				if turnChannelManager != nil && ts.channel != "" && !constants.IsInternalChannel(ts.channel) {
					if err := turnChannelManager.SendMedia(ctx, outboundMedia); err != nil {
						logger.WarnCF("agent", "Failed to deliver tool media",
							map[string]any{
								"agent_id": ts.agent.ID,
								"tool":     toolName,
								"channel":  ts.channel,
								"chat_id":  ts.chatID,
								"error":    err.Error(),
							})
						toolResult = tools.ErrorResult(fmt.Sprintf("failed to deliver attachment: %v", err)).WithError(err)
					}
				} else if al.bus != nil {
					al.bus.PublishOutboundMedia(ctx, outboundMedia)
				}
				toolResult.ArtifactTags = buildArtifactTags(turnMediaStore, toolResult.Media)
			}

			if !toolResult.Silent && toolResult.ForUser != "" && ts.opts.SendResponse {
				if pubErr := al.bus.PublishOutbound(ctx, bus.OutboundMessage{
					Channel: ts.channel,
					ChatID:  ts.chatID,
					Content: toolResult.ForUser,
				}); pubErr != nil {
					logger.WarnCF("agent", "PublishOutbound failed for tool result",
						map[string]any{
							"tool":  toolName,
							"error": pubErr.Error(),
						})
				} else {
					logger.DebugCF("agent", "Sent tool result to user",
						map[string]any{
							"tool":        toolName,
							"content_len": len(toolResult.ForUser),
						})
				}
			}

			contentForLLM := toolResult.ContentForLLM()

			// SEC-25: Sanitize tool results from untrusted sources (web fetch,
			// web search, browser output, read_file) before they enter the
			// LLM's context. Trusted tools (exec, spawn, message, task_*,
			// file writes, etc.) are NEVER sanitized because their output is
			// either user-authored or produced by a peer agent inside the
			// same trust boundary.
			//
			// Order of operations: prompt guard FIRST, sensitive-data filter
			// SECOND. Reversing the order would let an injection payload
			// that mentions a secret pattern be partially redacted, leaving
			// the injection prefix intact and feeding it to the LLM.
			if al.promptGuard != nil && isUntrustedToolResult(toolName) {
				original := contentForLLM
				contentForLLM = al.promptGuard.Sanitize(contentForLLM, false)
				// Log every actual mutation to the operator stream AND to the
				// audit log (when enabled). Mutation is the signal the security
				// team cares about; logging no-op passes would drown real
				// events. The operator-stream log is unconditional so that
				// disabling audit logging does NOT hide prompt-guard rewrites.
				if contentForLLM != original {
					details := map[string]any{
						"action":          "prompt_guard_sanitize",
						"strictness":      string(al.promptGuard.Strictness()),
						"original_bytes":  len(original),
						"sanitized_bytes": len(contentForLLM),
						"tool":            toolName,
						"agent_id":        ts.agent.ID,
					}
					logger.InfoCF("agent", "prompt guard sanitized tool result", details)
					// CRIT-6: route through audit.EmitEntry — Log failure bumps the
					// audit-skipped counter so /health audit_degraded surfaces gaps.
					audit.EmitEntry(al.auditLogger, &audit.Entry{
						Event:    audit.EventPolicyEval,
						Decision: audit.DecisionAllow,
						AgentID:  ts.agent.ID,
						Tool:     toolName,
						Details:  details,
					})
				}
			}

			// Filter sensitive data (API keys, tokens, secrets) before sending to LLM
			if cfg.Tools.IsFilterSensitiveDataEnabled() {
				contentForLLM = cfg.FilterSensitiveData(contentForLLM)
			}

			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: toolCallID,
			}
			// Attach inline image data URLs so vision-capable models can SEE the
			// screenshot/image returned by the tool. Without this the LLM only
			// gets the placeholder text and cannot reason about the picture.
			if len(toolResult.Media) > 0 && turnMediaStore != nil {
				for _, ref := range toolResult.Media {
					localPath, meta, err := turnMediaStore.ResolveWithMeta(ref)
					if err != nil || !strings.HasPrefix(meta.ContentType, "image/") {
						continue
					}
					data, err := os.ReadFile(localPath)
					if err != nil {
						continue
					}
					dataURL := "data:" + meta.ContentType + ";base64," + base64.StdEncoding.EncodeToString(data)
					toolResultMsg.Media = append(toolResultMsg.Media, dataURL)
				}
			}
			al.emitEvent(
				EventKindToolExecEnd,
				ts.eventMeta("runTurn", "turn.tool.end"),
				ToolExecEndPayload{
					ToolCallID:        session.ToolCallID(toolCallID),
					ChatID:            ts.chatID,
					SessionID:         ts.transcriptSessionID,
					Tool:              toolName,
					Duration:          toolDuration,
					ForLLMLen:         len(contentForLLM),
					ForUserLen:        len(toolResult.ForUser),
					IsError:           toolResult.IsError,
					Async:             toolResult.Async,
					Result:            contentForLLM,
					ParentSpawnCallID: session.ToolCallID(ts.parentSpawnCallID),
					AgentID:           ts.resolveActiveAgentID(), // Bug 1: runtime-current agent
				},
			)
			tcStatus := "success"
			if toolResult.IsError {
				tcStatus = "error"
			}
			tcRecord := session.ToolCall{
				ID:               session.ToolCallID(toolCallID),
				Tool:             toolName,
				Status:           tcStatus,
				DurationMS:       toolDuration.Milliseconds(),
				Parameters:       cloneEventArguments(toolArgs),
				ParentToolCallID: session.ToolCallID(ts.parentSpawnCallID),
			}
			// Persist media descriptors so replay can re-emit the `media`
			// frame and reopened sessions show the attachments the user
			// originally saw. We store enough metadata (ref + filename +
			// content_type + type) for replay to reconstruct the wire frame
			// without re-resolving against the MediaStore at replay time.
			if len(toolResult.Media) > 0 {
				descs := make([]map[string]any, 0, len(toolResult.Media))
				for _, ref := range toolResult.Media {
					d := map[string]any{"ref": ref}
					if turnMediaStore != nil {
						if _, meta, err := turnMediaStore.ResolveWithMeta(ref); err == nil {
							if meta.Filename != "" {
								d["filename"] = meta.Filename
							}
							if meta.ContentType != "" {
								d["content_type"] = meta.ContentType
							}
							d["type"] = inferMediaType(meta.Filename, meta.ContentType)
						}
					}
					descs = append(descs, d)
				}
				tcRecord.Result = map[string]any{"media": descs}
			}
			ts.appendToolCallTranscript(tcRecord)
			messages = append(messages, toolResultMsg)
			if !ts.opts.NoHistory {
				ts.agent.Sessions.AddFullMessage(ts.sessionKey, toolResultMsg)
				ts.recordPersistedMessage(toolResultMsg)
			}

			if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
				pendingMessages = append(pendingMessages, steerMsgs...)
			}

			skipReason := ""
			skipMessage := ""
			if len(pendingMessages) > 0 {
				skipReason = "queued user steering message"
				skipMessage = "Skipped due to queued user message."
			} else if gracefulPending, _ := ts.gracefulInterruptRequested(); gracefulPending {
				skipReason = "graceful interrupt requested"
				skipMessage = "Skipped due to graceful interrupt."
			}

			if skipReason != "" {
				remaining := len(normalizedToolCalls) - i - 1
				if remaining > 0 {
					logger.InfoCF("agent", "Turn checkpoint: skipping remaining tools",
						map[string]any{
							"agent_id":  ts.agent.ID,
							"completed": i + 1,
							"skipped":   remaining,
							"reason":    skipReason,
						})
					for j := i + 1; j < len(normalizedToolCalls); j++ {
						skippedTC := normalizedToolCalls[j]
						al.emitEvent(
							EventKindToolExecSkipped,
							ts.eventMeta("runTurn", "turn.tool.skipped"),
							ToolExecSkippedPayload{
								Tool:   skippedTC.Name,
								Reason: skipReason,
							},
						)
						skippedMsg := providers.Message{
							Role:       "tool",
							Content:    skipMessage,
							ToolCallID: skippedTC.ID,
						}
						messages = append(messages, skippedMsg)
						if !ts.opts.NoHistory {
							ts.agent.Sessions.AddFullMessage(ts.sessionKey, skippedMsg)
							ts.recordPersistedMessage(skippedMsg)
						}
					}
				}
				break
			}

			// Also poll for any SubTurn results that arrived during tool execution.
			if ts.pendingResults != nil {
				select {
				case result, ok := <-ts.pendingResults:
					if ok && result != nil && result.ForLLM != "" {
						content := cfg.FilterSensitiveData(result.ForLLM)
						msg := providers.Message{Role: "user", Content: fmt.Sprintf("[SubTurn Result] %s", content)}
						messages = append(messages, msg)
						if !ts.opts.NoHistory {
							ts.agent.Sessions.AddFullMessage(ts.sessionKey, msg)
						}
					}
				default:
					// No results available
				}
			}
		}

		ts.agent.Tools.TickTTL()
		logger.DebugCF("agent", "TTL tick after tool execution", map[string]any{
			"agent_id": ts.agent.ID, "iteration": iteration,
		})
	}

	if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
		logger.InfoCF("agent", "Steering arrived after turn completion; continuing turn before finalizing",
			map[string]any{
				"agent_id":       ts.agent.ID,
				"steering_count": len(steerMsgs),
				"session_key":    ts.sessionKey,
			})
		pendingMessages = append(pendingMessages, steerMsgs...)
		finalContent = ""
		// I2: guard against bypassing the hard iteration ceiling via goto.
		// If the ceiling is exceeded, fall through to finalization rather than
		// re-entering turnLoop, which would be invalid at this point anyway.
		if ts.currentIteration() < 2*ts.agent.MaxIterations {
			goto turnLoop
		}
	}

	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	if finalContent == "" {
		if ts.currentIteration() >= ts.agent.MaxIterations && ts.agent.MaxIterations > 0 {
			finalContent = toolLimitResponse
		} else {
			finalContent = ts.opts.DefaultResponse
		}
	}

	ts.setPhase(TurnPhaseFinalizing)
	ts.setFinalContent(finalContent)
	if !ts.opts.NoHistory {
		finalMsg := providers.Message{Role: "assistant", Content: finalContent}
		ts.agent.Sessions.AddMessage(ts.sessionKey, finalMsg.Role, finalMsg.Content)
		ts.recordPersistedMessage(finalMsg)
		if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
			turnStatus = TurnEndStatusError
			al.emitEvent(
				EventKindError,
				ts.eventMeta("runTurn", "turn.error"),
				ErrorPayload{
					Stage:   "session_save",
					Message: err.Error(),
				},
			)
			// FIX-2 / US-1: persist the session-save failure to the JSONL
			// transcript so the replay path re-renders it after reload (see
			// appendErrorTranscript docstring).
			ts.appendErrorTranscript(
				EventKindError.String(), "runTurn",
				fmt.Sprintf("session save failed: %s", err.Error()),
			)
			return turnResult{}, err
		}
	}

	// Bug 3 fix: persist assistant text to transcript.jsonl when no wsStreamer
	// was active (WS disconnected, non-webchat channel, or headless run).
	// When ts.lastStreamer != nil, the deferred ts.finalizeStreamer will call
	// wsStreamer.Finalize which writes the accumulated streaming content to the
	// transcript — but ONLY if the streamer's token buffer has content. When
	// every Update() call silently failed (WS closed mid-stream) the buffer is
	// empty and Finalize would skip the write. We hand finalContent to the
	// streamer via SetFinalContent so it can fall back to that text.
	ts.SetFinalContent(finalContent)
	ts.mu.RLock()
	hasActiveStreamer := ts.lastStreamer != nil
	ts.mu.RUnlock()
	if !hasActiveStreamer && finalContent != "" {
		ts.appendAssistantTranscript(finalContent)
	}

	if ts.opts.EnableSummary {
		al.maybeSummarize(ts.agent, ts.sessionKey, ts.scope)
	}

	ts.setPhase(TurnPhaseCompleted)
	return turnResult{
		finalContent: finalContent,
		status:       turnStatus,
		followUps:    append([]bus.InboundMessage(nil), ts.followUps...),
	}, nil
}

func (al *AgentLoop) abortTurn(ts *turnState) (turnResult, error) {
	ts.setPhase(TurnPhaseAborted)
	if !ts.opts.NoHistory {
		if err := ts.restoreSession(ts.agent); err != nil {
			al.emitEvent(
				EventKindError,
				ts.eventMeta("abortTurn", "turn.error"),
				ErrorPayload{
					Stage:   "session_restore",
					Message: err.Error(),
				},
			)
			return turnResult{}, err
		}
	}
	return turnResult{status: TurnEndStatusAborted}, nil
}

// defaultSyntheticErrorFloor is the default value of
// gateway.turn_synthetic_error_floor (FR-084). After this many consecutive
// synthetic-deny tool results in a single turn, the turn is aborted.
const defaultSyntheticErrorFloor = 8

// syntheticErrorFloor returns the configured synthetic-error floor for the
// current loop config. Negative values return the default. 0 means disabled.
func (al *AgentLoop) syntheticErrorFloor() int {
	cfg := al.GetConfig()
	n := cfg.Gateway.TurnSyntheticErrorFloor
	if n < 0 {
		return defaultSyntheticErrorFloor
	}
	return n
}

// recordSyntheticDeny increments the turn's consecutive-synthetic-deny counter
// and returns true when the turn should be aborted (FR-084). It also appends a
// system message to the session documenting the abort reason so the LLM can
// observe it on the next prompt.
//
// Returns (shouldAbort bool, abortMsg string). The caller is responsible for
// appending abortMsg to messages and calling abortTurn if shouldAbort is true.
func (al *AgentLoop) recordSyntheticDeny(ts *turnState) (shouldAbort bool, abortMsg string) {
	ts.syntheticErrorCount++
	floor := al.syntheticErrorFloor()
	if floor <= 0 || ts.syntheticErrorCount < floor {
		return false, ""
	}
	msg := fmt.Sprintf(
		`{"role":"system","type":"turn_aborted","reason":"synthetic_error_loop","count":%d}`,
		ts.syntheticErrorCount,
	)
	if !ts.opts.NoHistory {
		ts.agent.Sessions.AddMessage(ts.sessionKey, "system", msg)
		if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
			logger.WarnCF("agent", "FR-084: failed to persist turn_aborted message",
				map[string]any{"session_key": ts.sessionKey, "error": err.Error()})
		}
	}
	// CRIT-6 + typed-Decision/Event migration: route through audit.EmitEntry
	// so Log failure bumps the audit-skipped counter; use the typed
	// EventTurnAbortedSyntheticLoop and DecisionDeny constants.
	audit.EmitEntry(al.auditLogger, &audit.Entry{
		Event:     audit.EventTurnAbortedSyntheticLoop,
		Decision:  audit.DecisionDeny,
		AgentID:   ts.agentID,
		SessionID: ts.sessionKey,
		Details: map[string]any{
			"turn_id":               ts.turnID,
			"synthetic_error_count": ts.syntheticErrorCount,
			"floor":                 floor,
		},
	})
	logger.WarnCF("agent", "FR-084: synthetic-error floor reached — aborting turn",
		map[string]any{
			"agent_id":    ts.agentID,
			"session_key": ts.sessionKey,
			"count":       ts.syntheticErrorCount,
			"floor":       floor,
		})
	return true, msg
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// selectCandidates returns the model candidates and resolved model name to use
// for a conversation turn. When model routing is configured and the incoming
// message scores below the complexity threshold, it returns the light model
// candidates instead of the primary ones.
//
// The returned (candidates, model) pair is used for all LLM calls within one
// turn — tool follow-up iterations use the same tier as the initial call so
// that a multi-step tool chain doesn't switch models mid-way.
func (al *AgentLoop) selectCandidates(
	agent *AgentInstance,
	userMsg string,
	history []providers.Message,
) (candidates []providers.FallbackCandidate, model string, usedLight bool) {
	if agent.Router == nil || len(agent.LightCandidates) == 0 {
		return agent.Candidates, resolvedCandidateModel(agent.Candidates, agent.Model), false
	}

	_, usedLight, score := agent.Router.SelectModel(userMsg, history, agent.Model)
	if !usedLight {
		logger.DebugCF("agent", "Model routing: primary model selected",
			map[string]any{
				"agent_id":  agent.ID,
				"score":     score,
				"threshold": agent.Router.Threshold(),
			})
		return agent.Candidates, resolvedCandidateModel(agent.Candidates, agent.Model), false
	}

	logger.InfoCF("agent", "Model routing: light model selected",
		map[string]any{
			"agent_id":    agent.ID,
			"light_model": agent.Router.LightModel(),
			"score":       score,
			"threshold":   agent.Router.Threshold(),
		})
	return agent.LightCandidates, resolvedCandidateModel(agent.LightCandidates, agent.Router.LightModel()), true
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
func (al *AgentLoop) maybeSummarize(agent *AgentInstance, sessionKey string, turnScope turnEventScope) {
	newHistory := agent.Sessions.GetHistory(sessionKey)
	tokenEstimate := al.estimateTokens(newHistory)
	threshold := agent.ContextWindow * agent.SummarizeTokenPercent / 100

	if len(newHistory) > agent.SummarizeMessageThreshold || tokenEstimate > threshold {
		summarizeKey := agent.ID + ":" + sessionKey
		if _, loading := al.summarizing.LoadOrStore(summarizeKey, true); !loading {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.ErrorCF("agent", "Panic during summarization", map[string]any{"panic": r})
					}
					al.summarizing.Delete(summarizeKey)
				}()
				logger.Debug("Memory threshold reached. Optimizing conversation history...")
				al.summarizeSession(agent, sessionKey, turnScope)
			}()
		}
	}
}

type compressionResult struct {
	DroppedMessages   int
	RemainingMessages int
}

// forceCompression aggressively reduces context when the limit is hit.
// It drops the oldest ~50% of Turns (a Turn is a complete user→LLM→response
// cycle, as defined in #1316), so tool-call sequences are never split.
//
// If the history is a single Turn with no safe split point, the function
// falls back to keeping only the most recent user message. This breaks
// Turn atomicity as a last resort to avoid a context-exceeded loop.
//
// Session history contains only user/assistant/tool messages — the system
// prompt is built dynamically by BuildMessages and is NOT stored here.
// The compression note is recorded in the session summary so that
// BuildMessages can include it in the next system prompt.
func (al *AgentLoop) forceCompression(agent *AgentInstance, sessionKey string) (compressionResult, bool) {
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) <= 2 {
		return compressionResult{}, false
	}

	// Split at a Turn boundary so no tool-call sequence is torn apart.
	// splitHistoryAtTurnMidpoint gives us (dropped, kept, ok); ok=false
	// means no safe boundary — we then fall back to keeping only the
	// most recent user message (Turn atomicity last-resort break).
	var keptHistory []providers.Message
	_, kept, ok := splitHistoryAtTurnMidpoint(history)
	if !ok {
		// No safe Turn boundary — the entire history is a single Turn
		// (e.g. one user message followed by a massive tool response).
		// Keeping everything would leave the agent stuck in a context-
		// exceeded loop, so fall back to keeping only the most recent
		// user message. This breaks Turn atomicity as a last resort.
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" {
				keptHistory = []providers.Message{history[i]}
				break
			}
		}
	} else {
		keptHistory = kept
	}

	droppedCount := len(history) - len(keptHistory)

	// Record compression in the session summary so BuildMessages includes it
	// in the system prompt. We do not modify history messages themselves.
	existingSummary := agent.Sessions.GetSummary(sessionKey)
	compressionNote := fmt.Sprintf(
		"[Emergency compression dropped %d oldest messages due to context limit]",
		droppedCount,
	)
	if existingSummary != "" {
		compressionNote = existingSummary + "\n\n" + compressionNote
	}
	agent.Sessions.SetSummary(sessionKey, compressionNote)

	agent.Sessions.SetHistory(sessionKey, keptHistory)
	if saveErr := agent.Sessions.Save(sessionKey); saveErr != nil {
		logger.ErrorCF("agent", "forceCompression: failed to persist compressed session",
			map[string]any{"session_key": sessionKey, "error": saveErr.Error()})
	}

	logger.WarnCF("agent", "Forced compression executed", map[string]any{
		"session_key":  sessionKey,
		"dropped_msgs": droppedCount,
		"new_count":    len(keptHistory),
	})

	return compressionResult{
		DroppedMessages:   droppedCount,
		RemainingMessages: len(keptHistory),
	}, true
}

// SwitchAction is the result of decideSwitchCompressAction: should we
// compress the conversation at model-switch time, or no-op?
//
// (FR-011, spec §11 Dataset 3.)
type SwitchAction int

const (
	// SwitchActionNoop means the new window fits the current conversation;
	// no compression needed.
	SwitchActionNoop SwitchAction = iota
	// SwitchActionCompress means the new window is smaller than the current
	// conversation; the loop MUST invoke summarizeDroppedTurns and then
	// forceCompression before the next LLM call.
	SwitchActionCompress
)

// String returns a stable name for logging/metrics.
func (a SwitchAction) String() string {
	switch a {
	case SwitchActionNoop:
		return "noop"
	case SwitchActionCompress:
		return "compress"
	default:
		return "unknown"
	}
}

// decideSwitchCompressAction decides whether the loop must run switch-time
// compression (FR-011) given the current conversation size and the new
// model's context window. The decision is pure — it has no side effects and
// no LLM call — so the call site can gate the expensive summary path.
//
// Decision matrix (spec §11 Dataset 3):
//
//	currentConvTokens == 0           → Noop
//	newContextWindow  <= 0           → Noop (graceful — see handleModelSwitch)
//	currentConvTokens <= newWindow    → Noop
//	currentConvTokens >  newWindow    → Compress
//
// Tested by TestSwitchTimeCompress_*.
func decideSwitchCompressAction(currentConvTokens, newContextWindow int) SwitchAction {
	if currentConvTokens <= 0 || newContextWindow <= 0 {
		return SwitchActionNoop
	}
	if currentConvTokens <= newContextWindow {
		return SwitchActionNoop
	}
	return SwitchActionCompress
}

// estimateHistoryTokens is a small helper that sums estimateMessageTokens
// across a history slice. The loop's switch-time compress path uses it to
// compute the "current conversation size" fed into decideSwitchCompressAction.
func estimateHistoryTokens(history []providers.Message) int {
	total := 0
	for _, m := range history {
		total += estimateMessageTokens(m)
	}
	return total
}

// summarizeDroppedTurns makes a small LLM call asking the new model (or the
// agent's configured provider) to produce a real prose summary of the
// dropped turns. The returned summary is bounded to ≤50% of the new model's
// context window (per FR-011) by passing a max_tokens request hint to the
// provider; the actual response length is whatever the provider returns —
// there is no post-hoc truncation, so a misbehaving provider may exceed
// the budget. If the LLM call fails, the caller is expected to fall back
// to forceCompression and surface a warning.
//
// We use the agent's currently configured provider/model so the summary is
// cheap (already in the context, no new credential resolution). This matches
// the spec direction in §13 FR-011.
//
// Parameters:
//   - ctx:    request context (caller passes a sensible timeout).
//   - agent:  the agent whose Provider will produce the summary.
//   - dropped: the messages we are about to drop from history (in order,
//     oldest first).
//   - newContextWindow: the new model's context window — used to bound the
//     summary's length to ≤50% (rounded down to whole tokens).
//
// Returns the summary string and any error from the LLM call. Pure I/O —
// does not mutate the agent or the session.
func (al *AgentLoop) summarizeDroppedTurns(
	ctx context.Context,
	agent *AgentInstance,
	dropped []providers.Message,
	newContextWindow int,
) (string, error) {
	if agent == nil || agent.Provider == nil {
		return "", fmt.Errorf("summarizeDroppedTurns: nil agent/provider")
	}

	// Bound the summary to ≤50% of the new context window. MaxTokens on the
	// agent is the OUTPUT cap; the agent's existing MaxTokens is also a
	// reasonable ceiling on the summary size when the new window is very
	// small.
	summaryBudget := newContextWindow / 2
	if agent.MaxTokens > 0 && agent.MaxTokens < summaryBudget {
		summaryBudget = agent.MaxTokens
	}
	if summaryBudget <= 0 {
		// Defensive floor: when the new model's resolved window is missing
		// or non-positive, fall back to a small fixed budget so the prompt
		// still asks for something bounded. The caller (handleModelSwitch)
		// will route this through forceCompression on error, so a tight
		// budget here is a deliberate last-resort cap.
		logger.WarnCF("agent", "summarizeDroppedTurns: summaryBudget fell back to default 256",
			map[string]any{
				"new_context_window": newContextWindow,
				"agent_max_tokens":   agent.MaxTokens,
			})
		summaryBudget = 256
	}

	// Build the prompt: render the dropped turns into a single transcript
	// block, then ask the LLM to summarize. We deliberately keep the
	// instruction short and the format request explicit ("plain prose, no
	// markdown") so the new model gets a clean hint and no tool calls.
	var b strings.Builder
	b.WriteString("Summarize the key decisions, facts, and open questions from this conversation. Output ≤")
	b.WriteString(strconv.Itoa(summaryBudget))
	b.WriteString(" tokens. Plain prose, no markdown formatting beyond plain text.\n\n---\n")
	for i, m := range dropped {
		// Truncate per-message to avoid blowing the prompt on a single
		// dropped message that itself was over-budget.
		content := m.Content
		if len(content) > 2000 {
			content = content[:2000] + "…"
		}
		fmt.Fprintf(&b, "[%s] %s\n", m.Role, content)
		if i == 16 {
			b.WriteString("… (later messages truncated)\n")
			break
		}
	}

	prompt := []providers.Message{
		{Role: "user", Content: b.String()},
	}

	model := agent.Model
	if model == "" {
		model = agent.Provider.GetDefaultModel()
	}

	resp, err := agent.Provider.Chat(ctx, prompt, nil, model, map[string]any{
		"max_tokens": summaryBudget,
	})
	if err != nil {
		return "", fmt.Errorf("summarizeDroppedTurns: provider chat: %w", err)
	}
	if resp == nil || strings.TrimSpace(resp.Content) == "" {
		return "", fmt.Errorf("summarizeDroppedTurns: empty summary from provider")
	}
	return strings.TrimSpace(resp.Content), nil
}

// buildSyntheticSwitchMessage renders the agreed synthetic system message
// (spec Q4 wording): "Conversation moved to {new_model} from {old_model} on
// {timestamp}. The prior turns have been compressed to fit the new context
// window. Summary: {summary}". The message carries Synthetic=true so the UI
// can render it distinctly if it wants to.
func buildSyntheticSwitchMessage(oldModel, newModel, summary string, timestamp time.Time) providers.Message {
	wording := fmt.Sprintf(
		"Conversation moved to %s from %s on %s. The prior turns have been compressed to fit the new context window. Summary: %s",
		newModel,
		oldModel,
		timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
		summary,
	)
	return providers.Message{
		Role:      "system",
		Content:   wording,
		Synthetic: true,
	}
}

// handleModelSwitch is the switch-time compress path (FR-011, FR-012). It is
// invoked when an incoming bus message carries a model_name metadata that
// differs from the agent's current Model.
//
// Behaviour:
//  1. Resolve the new model's ContextWindow. In this worktree we read it
//     from the agent's stored defaults when available; otherwise we fall
//     back to the agent's current ContextWindow (no shrink detected → noop).
//  2. Estimate the current conversation size.
//  3. If decideSwitchCompressAction says Compress:
//     - call summarizeDroppedTurns with the dropped turns (oldest first,
//     after a single forceCompression pass that splits at a Turn
//     boundary and keeps the most recent half);
//     - on LLM error, fall back to the existing forceCompression path and
//     emit a warn-level log;
//     - prepend the synthetic system message to the kept history;
//     - persist the session.
//  4. Update agent.Model to the new model and return the agent for the
//     caller to use as the turn's effective model.
//
// The function is intentionally side-effect-light on the agent: it does NOT
// touch agent.Provider / agent.Candidates — those are resolved by the
// existing ApplyAgentModel path (FR-004). handleModelSwitch is the
// conversation-shape half of the switch; ApplyAgentModel is the
// provider/credential half. The turn flow wires both.
//
// Returns the (possibly mutated) agent so callers can keep using the same
// pointer.
func (al *AgentLoop) handleModelSwitch(
	ctx context.Context,
	agent *AgentInstance,
	sessionKey string,
	newModel string,
	_ bus.InboundMessage,
) (*AgentInstance, error) {
	if agent == nil {
		return nil, fmt.Errorf("handleModelSwitch: nil agent")
	}
	if newModel == "" {
		return agent, nil
	}

	oldModel := agent.Model
	if oldModel == newModel {
		return agent, nil
	}

	history := agent.Sessions.GetHistory(sessionKey)
	currentConvTokens := estimateHistoryTokens(history)

	// Resolve the new model's window. ModelConfig itself doesn't carry a
	// window (the per-agent defaults hold the canonical window), so we read
	// the configured default from cfg.Agents.Defaults.ContextWindow. On any
	// miss (cfg nil, model unknown, defaults unset) fall back to the agent's
	// existing ContextWindow — preserving the historical "treat unknown as
	// fit" behaviour so the next LLM call's forceCompression still trips on
	// overflow. Force a 128k floor for sub-zero agent defaults so decision
	// logic still has a sane bound.
	newContextWindow := agent.ContextWindow
	if newContextWindow <= 0 {
		newContextWindow = 128000
	}
	al.mu.RLock()
	cfg := al.cfg
	al.mu.RUnlock()
	if cfg != nil {
		// Use ResolveModelCfg to confirm the new model resolves (so we know
		// it's a real entry in the registry). The actual window still comes
		// from agent defaults — ModelConfig doesn't carry one. We deliberately
		// keep the "unknown model = no-op switch" behavior (the next LLM
		// call will trip on overflow, forceCompression will still fire), but
		// we MUST surface the miss to the operator via a WARN. Discarding the
		// error (W4-4 silent-failure-A) would let a typo'd `metadata.model_name`
		// from the operator silently route the next LLM call through the
		// agent's PRIMARY model — the exact FR-007 failure mode. Logging the
		// resolve error at WARN with the requested model + agent id gives
		// operators a breadcrumb to spot the typo at the switch site rather
		// than several stack frames later.
		if _, resolveErr := ResolveModelCfg(cfg, newModel, agent.Workspace); resolveErr != nil {
			logger.WarnCF("agent", "handleModelSwitch: requested model did not resolve; falling back to agent defaults",
				map[string]any{
					"requested_model": newModel,
					"agent_id":        agent.ID,
					"resolve_error":   resolveErr.Error(),
				})
		}
		if cfg.Agents.Defaults.ContextWindow > 0 {
			newContextWindow = cfg.Agents.Defaults.ContextWindow
		}
	}

	action := decideSwitchCompressAction(currentConvTokens, newContextWindow)
	if action == SwitchActionCompress {
		// 1. Drop the oldest ~half of turns (reuse the existing safe-split
		//    logic in forceCompression) to recover headroom for the new
		//    window. We do this BEFORE the LLM summary call so the dropped
		//    set is small enough for a cheap summarization request.
		dropped, kept := splitForSwitchCompress(history)

		// 2. Ask the new model (via the agent's current provider) for a
		//    prose summary of the dropped turns. On error, fall back to
		//    the existing forceCompression note.
		//
		// Defensive timeout: the caller's context governs the LLM round-trip
		// (ADR-005 — in-loop synchronous LLM calls are the caller's
		// responsibility). 15s is generous for a short summarization prompt
		// but short enough that a stuck provider cannot block the turn
		// indefinitely.
		sumCtx, sumCancel := context.WithTimeout(ctx, 15*time.Second)
		var summary string
		sumSummary, summaryErr := al.summarizeDroppedTurns(sumCtx, agent, dropped, newContextWindow)
		sumCancel()
		if summaryErr == nil {
			summary = sumSummary
		}
		if summaryErr != nil {
			logger.WarnCF("agent", "switch-time summarizeDroppedTurns failed; falling back to forceCompression",
				map[string]any{
					"session_key": sessionKey,
					"old_model":   oldModel,
					"new_model":   newModel,
					"error":       summaryErr.Error(),
				})
			// Per FR-011: the spec-correct fallback when the LLM summarization
			// call fails is to invoke forceCompression — that path is the
			// canonical "I could not summarize, so I'll drop more aggressively
			// and surface a note" behaviour and is what `forceCompression`
			// already records in the session summary. We then build a brief
			// meta-note that mentions the switch but does not attempt an
			// additional LLM call. Persist via the same path so the next
			// turn's system prompt can surface the note.
			if _, compOK := al.forceCompression(agent, sessionKey); !compOK {
				logger.DebugCF("agent", "handleModelSwitch: forceCompression returned false (history too small)",
					map[string]any{"session_key": sessionKey})
			}
			summary = fmt.Sprintf("(summary unavailable: %s) — moved from %s to %s", summaryErr.Error(), oldModel, newModel)
		}

		// 3. Strategy B for FR-012: route the switch summary into the dynamic
		//    system prompt via Sessions.SetSummary instead of inserting a
		//    separate role:"system" message into history. sanitizeHistoryForProvider
		//    (context.go) deliberately drops every role:"system" message from
		//    history before sending to the LLM because BuildMessages
		//    constructs its own single system message — inserting a synthetic
		//    here would be silently stripped (FR-012 violation). The summary
		//    already carries the "moved from X to Y" wording.
		switchNote := fmt.Sprintf(
			"Conversation moved to %s from %s on %s.\n\n%s",
			newModel,
			oldModel,
			time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
			summary,
		)
		// Preserve any existing summary (e.g. an old forceCompression note)
		// so we don't blow it away; append the switch note.
		if existing := agent.Sessions.GetSummary(sessionKey); existing != "" {
			switchNote = switchNote + "\n\n---\n\n" + existing
		}
		agent.Sessions.SetSummary(sessionKey, switchNote)

		// 4. Trim the kept history to fit the new window. The switch note
		//    lives in the session summary (consumed by BuildMessages), so we
		//    do NOT prepend anything to the message list — that avoids the
		//    "synthetic system message" history poll and the
		//    sanitizeHistoryForProvider-strip bug.
		newHistory := fitWithinBudget(kept, newContextWindow)

		agent.Sessions.SetHistory(sessionKey, newHistory)
		if saveErr := agent.Sessions.Save(sessionKey); saveErr != nil {
			logger.ErrorCF("agent", "handleModelSwitch: failed to persist switched session",
				map[string]any{"session_key": sessionKey, "error": saveErr.Error()})
		}
		logger.InfoCF("agent", "switch-time compress completed",
			map[string]any{
				"session_key":   sessionKey,
				"old_model":     oldModel,
				"new_model":     newModel,
				"dropped":       len(dropped),
				"kept":          len(kept),
				"summary_chars": len(switchNote),
			})
	}

	// 5. Orchestrate the full in-memory model swap under the agent mutex.
	//    ApplyAgentModel resolves Model + Provider + Candidates atomically
	//    (FR-004 / FR-011), which is what the next LLM call will read. We
	//    run the whole swap inside agent.mu so concurrent runTurn readers
	//    never observe a torn (new Model, old Provider) state — that's the
	//    race the Go race detector was flagging on the bare `agent.Model =`
	//    write.
	//
	//    ApplyAgentModel takes its own agent.mu lock internally, so we
	//    dispatch it and rely on its serialization rather than double-locking.
	if _, applyErr := al.ApplyAgentModel(agent.ID, newModel); applyErr != nil {
		// ApplyAgentModel failed — leave the in-memory state untouched and
		// surface the error. We do NOT fall back to the bare Model write
		// because that would re-introduce the torn-state race.
		return agent, fmt.Errorf("handleModelSwitch: ApplyAgentModel(%q): %w", newModel, applyErr)
	}

	return agent, nil
}

// splitForSwitchCompress splits history into (dropped, kept) for switch-time
// compression. It is a thin wrapper over splitHistoryAtTurnMidpoint that
// degrades to "keep everything" when no safe boundary exists — handleModelSwitch
// is allowed to feed the full history to summarizeDroppedTurns if it has to,
// because the next LLM call will still trip forceCompression on overflow.
//
// Call chain: handleModelSwitch → splitForSwitchCompress →
// summarizeDroppedTurns (only path that uses `dropped`; `kept` is then
// combined with the synthetic switch message and re-trimmed via
// truncateHistoryToBudget).
func splitForSwitchCompress(history []providers.Message) (dropped, kept []providers.Message) {
	if len(history) == 0 {
		return nil, nil
	}
	if d, k, ok := splitHistoryAtTurnMidpoint(history); ok {
		return d, k
	}
	return nil, append([]providers.Message(nil), history...)
}

// fitWithinBudget aggressively trims history until its token estimate fits
// within newContextWindow. With Strategy B the switch summary lives in
// Sessions.GetSummary (not in history), so the first message here is a real
// conversation turn — we are free to drop it if it overflows the window on
// its own. The minimum result is an empty history (BuildMessages handles
// empty history gracefully; the next-turn context-window check via
// forceCompression is the last line of defence).
func fitWithinBudget(history []providers.Message, newContextWindow int) []providers.Message {
	if len(history) == 0 {
		return history
	}
	for estimateHistoryTokens(history) > newContextWindow && len(history) > 0 {
		// Drop from the tail backwards. When only one message remains and it
		// still overflows, drop it too — Strategy B has no synthetic anchor
		// to preserve, so an oversized last message is better dropped than
		// left as guaranteed overflow. forceCompression at the next LLM call
		// is the final defence.
		if len(history) == 1 {
			history = nil
			break
		}
		history = history[:len(history)-1]
	}
	return history
}

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]any {
	info := make(map[string]any)

	registry := al.GetRegistry()
	agent := registry.GetDefaultAgent()
	if agent == nil {
		return info
	}

	// Tools info
	toolsList := agent.Tools.List()
	info["tools"] = map[string]any{
		"count": len(toolsList),
		"names": toolsList,
	}

	// Skills info
	info["skills"] = agent.ContextBuilder.GetSkillsInfo()

	// Agents info
	info["agents"] = map[string]any{
		"count": len(registry.ListAgentIDs()),
		"ids":   registry.ListAgentIDs(),
	}

	return info
}

// ListSkillsDetailed returns the full per-skill metadata (name, source,
// description, author, version) for every installed skill, sourced from the
// default agent's ContextBuilder skills loader — the same loader that feeds
// GetStartupInfo's skills summary. This is the data path GET /api/v1/skills uses
// to enrich its response beyond bare names. Returns nil when there is no default
// agent or its ContextBuilder is unset (e.g. an uninitialized loop).
func (al *AgentLoop) ListSkillsDetailed() []skills.SkillInfo {
	registry := al.GetRegistry()
	if registry == nil {
		return nil
	}
	agent := registry.GetDefaultAgent()
	if agent == nil || agent.ContextBuilder == nil {
		return nil
	}
	return agent.ContextBuilder.ListSkillsDetailed()
}

// formatMessagesForLog formats messages for logging
func formatMessagesForLog(messages []providers.Message) string {
	if len(messages) == 0 {
		return "[]"
	}

	var sb strings.Builder
	sb.WriteString("[\n")
	for i, msg := range messages {
		fmt.Fprintf(&sb, "  [%d] Role: %s\n", i, msg.Role)
		if len(msg.ToolCalls) > 0 {
			sb.WriteString("  ToolCalls:\n")
			for _, tc := range msg.ToolCalls {
				fmt.Fprintf(&sb, "    - ID: %s, Type: %s, Name: %s\n", tc.ID, tc.Type, tc.Name)
				if tc.Function != nil {
					fmt.Fprintf(
						&sb,
						"      Arguments: %s\n",
						utils.Truncate(tc.Function.Arguments, 200),
					)
				}
			}
		}
		if msg.Content != "" {
			content := utils.Truncate(msg.Content, 200)
			fmt.Fprintf(&sb, "  Content: %s\n", content)
		}
		if msg.ToolCallID != "" {
			fmt.Fprintf(&sb, "  ToolCallID: %s\n", msg.ToolCallID)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("]")
	return sb.String()
}

// formatToolsForLog formats tool definitions for logging
func formatToolsForLog(toolDefs []providers.ToolDefinition) string {
	if len(toolDefs) == 0 {
		return "[]"
	}

	var sb strings.Builder
	sb.WriteString("[\n")
	for i, tool := range toolDefs {
		fmt.Fprintf(&sb, "  [%d] Type: %s, Name: %s\n", i, tool.Type, tool.Function.Name)
		fmt.Fprintf(&sb, "      Description: %s\n", tool.Function.Description)
		if len(tool.Function.Parameters) > 0 {
			fmt.Fprintf(
				&sb,
				"      Parameters: %s\n",
				utils.Truncate(fmt.Sprintf("%v", tool.Function.Parameters), 200),
			)
		}
	}
	sb.WriteString("]")
	return sb.String()
}

// summarizeSession summarizes the conversation history for a session.
func (al *AgentLoop) summarizeSession(agent *AgentInstance, sessionKey string, turnScope turnEventScope) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistory(sessionKey)
	summary := agent.Sessions.GetSummary(sessionKey)

	// Keep the most recent Turns for continuity, aligned to a Turn boundary
	// so that no tool-call sequence is split.
	if len(history) <= 4 {
		return
	}

	safeCut := findSafeBoundary(history, len(history)-4)
	if safeCut <= 0 {
		return
	}
	keepCount := len(history) - safeCut
	toSummarize := history[:safeCut]

	// Oversized Message Guard
	maxMessageTokens := agent.ContextWindow / 2
	validMessages := make([]providers.Message, 0)
	omitted := false

	for _, m := range toSummarize {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		msgTokens := len(m.Content) / 2
		if msgTokens > maxMessageTokens {
			omitted = true
			continue
		}
		validMessages = append(validMessages, m)
	}

	if len(validMessages) == 0 {
		return
	}

	const (
		maxSummarizationMessages = 10
		llmMaxRetries            = 3
		llmTemperature           = 0.3
		fallbackMaxContentLength = 200
	)

	// Multi-Part Summarization
	var finalSummary string
	if len(validMessages) > maxSummarizationMessages {
		mid := len(validMessages) / 2

		mid = al.findNearestUserMessage(validMessages, mid)

		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		s1, _ := al.summarizeBatch(ctx, agent, part1, "")
		s2, _ := al.summarizeBatch(ctx, agent, part2, "")

		mergePrompt := fmt.Sprintf(
			"Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s",
			s1,
			s2,
		)

		resp, err := al.retryLLMCall(ctx, agent, mergePrompt, llmMaxRetries)
		if err == nil && resp.Content != "" {
			finalSummary = resp.Content
		} else {
			finalSummary = s1 + " " + s2
		}
	} else {
		finalSummary, _ = al.summarizeBatch(ctx, agent, validMessages, summary)
	}

	if omitted && finalSummary != "" {
		finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
	}

	if finalSummary != "" {
		agent.Sessions.SetSummary(sessionKey, finalSummary)
		agent.Sessions.TruncateHistory(sessionKey, keepCount)
		if saveErr := agent.Sessions.Save(sessionKey); saveErr != nil {
			logger.ErrorCF("agent", "summarizeSession: failed to persist summarized session",
				map[string]any{"session_key": sessionKey, "error": saveErr.Error()})
		}
		al.emitEvent(
			EventKindSessionSummarize,
			turnScope.meta(0, "summarizeSession", "turn.session.summarize"),
			SessionSummarizePayload{
				SummarizedMessages: len(validMessages),
				KeptMessages:       keepCount,
				SummaryLen:         len(finalSummary),
				OmittedOversized:   omitted,
			},
		)
	}
}

// findNearestUserMessage finds the nearest user message to the given index.
// It searches backward first, then forward if no user message is found.
func (al *AgentLoop) findNearestUserMessage(messages []providers.Message, mid int) int {
	originalMid := mid

	for mid > 0 && messages[mid].Role != "user" {
		mid--
	}

	if messages[mid].Role == "user" {
		return mid
	}

	mid = originalMid
	for mid < len(messages) && messages[mid].Role != "user" {
		mid++
	}

	if mid < len(messages) {
		return mid
	}

	return originalMid
}

// retryLLMCall calls the LLM with retry logic.
func (al *AgentLoop) retryLLMCall(
	ctx context.Context,
	agent *AgentInstance,
	prompt string,
	maxRetries int,
) (*providers.LLMResponse, error) {
	const (
		llmTemperature = 0.3
	)

	var resp *providers.LLMResponse
	var err error

	for attempt := 0; attempt < maxRetries; attempt++ {
		al.activeRequests.Add(1)
		resp, err = func() (*providers.LLMResponse, error) {
			defer al.activeRequests.Done()
			return agent.Provider.Chat(
				ctx,
				[]providers.Message{{Role: "user", Content: prompt}},
				nil,
				agent.Model,
				map[string]any{
					"max_tokens":       agent.MaxTokens,
					"temperature":      llmTemperature,
					"prompt_cache_key": agent.ID,
				},
			)
		}()

		if err == nil && resp != nil && resp.Content != "" {
			return resp, nil
		}
		if attempt < maxRetries-1 {
			if sleepErr := sleepWithContext(ctx, time.Duration(attempt+1)*100*time.Millisecond); sleepErr != nil {
				return resp, sleepErr
			}
		}
	}

	return resp, err
}

// summarizeBatch summarizes a batch of messages.
func (al *AgentLoop) summarizeBatch(
	ctx context.Context,
	agent *AgentInstance,
	batch []providers.Message,
	existingSummary string,
) (string, error) {
	const (
		llmMaxRetries             = 3
		llmTemperature            = 0.3
		fallbackMinContentLength  = 200
		fallbackMaxContentPercent = 10
	)

	var sb strings.Builder
	sb.WriteString(
		"Provide a concise summary of this conversation segment, preserving core context and key points.\n",
	)
	if existingSummary != "" {
		sb.WriteString("Existing context: ")
		sb.WriteString(existingSummary)
		sb.WriteString("\n")
	}
	sb.WriteString("\nCONVERSATION:\n")
	for _, m := range batch {
		fmt.Fprintf(&sb, "%s: %s\n", m.Role, m.Content)
	}
	prompt := sb.String()

	response, err := al.retryLLMCall(ctx, agent, prompt, llmMaxRetries)
	if err == nil && response.Content != "" {
		return strings.TrimSpace(response.Content), nil
	}

	var fallback strings.Builder
	fallback.WriteString("Conversation summary: ")
	for i, m := range batch {
		if i > 0 {
			fallback.WriteString(" | ")
		}
		content := strings.TrimSpace(m.Content)
		runes := []rune(content)
		if len(runes) == 0 {
			fallback.WriteString(fmt.Sprintf("%s: ", m.Role))
			continue
		}

		keepLength := len(runes) * fallbackMaxContentPercent / 100
		if keepLength < fallbackMinContentLength {
			keepLength = fallbackMinContentLength
		}

		if keepLength > len(runes) {
			keepLength = len(runes)
		}

		content = string(runes[:keepLength])
		if keepLength < len(runes) {
			content += "..."
		}
		fallback.WriteString(fmt.Sprintf("%s: %s", m.Role, content))
	}
	return fallback.String(), nil
}

// estimateTokens estimates the number of tokens in a message list.
// Counts Content, ToolCalls arguments, and ToolCallID metadata so that
// tool-heavy conversations are not systematically undercounted.
func (al *AgentLoop) estimateTokens(messages []providers.Message) int {
	total := 0
	for _, m := range messages {
		total += estimateMessageTokens(m)
	}
	return total
}

func (al *AgentLoop) handleCommand(
	ctx context.Context,
	msg bus.InboundMessage,
	agent *AgentInstance,
	opts *processOptions,
) (string, bool) {
	if !commands.HasCommandPrefix(msg.Content) {
		return "", false
	}

	if matched, handled, reply := al.applyExplicitSkillCommand(msg.Content, agent, opts); matched {
		return reply, handled
	}

	if al.cmdRegistry == nil {
		return "", false
	}

	rt := al.buildCommandsRuntime(agent, opts)
	executor := commands.NewExecutor(al.cmdRegistry, rt)

	var commandReply string
	result := executor.Execute(ctx, commands.Request{
		Channel:  msg.Channel,
		ChatID:   msg.ChatID,
		SenderID: msg.SenderID,
		Text:     msg.Content,
		Reply: func(text string) error {
			commandReply = text
			return nil
		},
	})

	switch result.Outcome {
	case commands.OutcomeHandled:
		if result.Err != nil {
			return mapCommandError(result), true
		}
		if commandReply != "" {
			return commandReply, true
		}
		return "", true
	default: // OutcomePassthrough — let the message fall through to LLM
		return "", false
	}
}

func activeSkillNames(agent *AgentInstance, opts processOptions) []string {
	if agent == nil {
		return nil
	}

	combined := make([]string, 0, len(agent.SkillsFilter)+len(opts.ForcedSkills))
	combined = append(combined, agent.SkillsFilter...)
	combined = append(combined, opts.ForcedSkills...)
	if len(combined) == 0 {
		return nil
	}

	var resolved []string
	seen := make(map[string]struct{}, len(combined))
	for _, name := range combined {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if agent.ContextBuilder != nil {
			if canonical, ok := agent.ContextBuilder.ResolveSkillName(name); ok {
				name = canonical
			}
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		resolved = append(resolved, name)
	}

	return resolved
}

func (al *AgentLoop) applyExplicitSkillCommand(
	raw string,
	agent *AgentInstance,
	opts *processOptions,
) (matched bool, handled bool, reply string) {
	cmdName, ok := commands.CommandName(raw)
	if !ok || cmdName != "use" {
		return false, false, ""
	}

	if agent == nil || agent.ContextBuilder == nil {
		return true, true, commandsUnavailableSkillMessage()
	}

	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) < 2 {
		return true, true, buildUseCommandHelp(agent)
	}

	arg := strings.TrimSpace(parts[1])
	if strings.EqualFold(arg, "clear") || strings.EqualFold(arg, "off") {
		if opts != nil {
			al.clearPendingSkills(opts.SessionKey)
		}
		return true, true, "Cleared pending skill override."
	}

	skillName, ok := agent.ContextBuilder.ResolveSkillName(arg)
	if !ok {
		return true, true, fmt.Sprintf("Unknown skill: %s\nUse /list skills to see installed skills.", arg)
	}

	if len(parts) < 3 {
		if opts == nil || strings.TrimSpace(opts.SessionKey) == "" {
			return true, true, commandsUnavailableSkillMessage()
		}
		al.setPendingSkills(opts.SessionKey, []string{skillName})
		return true, true, fmt.Sprintf(
			"Skill %q is armed for your next message. Send your next prompt normally, or use /use clear to cancel.",
			skillName,
		)
	}

	message := strings.TrimSpace(strings.Join(parts[2:], " "))
	if message == "" {
		return true, true, buildUseCommandHelp(agent)
	}

	if opts != nil {
		opts.ForcedSkills = append(opts.ForcedSkills, skillName)
		opts.UserMessage = message
	}

	return true, false, ""
}

func (al *AgentLoop) buildCommandsRuntime(agent *AgentInstance, opts *processOptions) *commands.Runtime {
	registry := al.GetRegistry()
	cfg := al.GetConfig()
	rt := &commands.Runtime{
		Config:          cfg,
		ListAgentIDs:    registry.ListAgentIDs,
		ListDefinitions: al.cmdRegistry.Definitions,
		GetEnabledChannels: func() []string {
			if cm := al.getChannelManager(); cm != nil {
				return cm.GetEnabledChannels()
			}
			return nil
		},
		GetActiveTurn: func() any {
			info := al.GetActiveTurn()
			if info == nil {
				return nil
			}
			return info
		},
		SwitchChannel: func(value string) error {
			cm := al.getChannelManager()
			if cm == nil {
				return fmt.Errorf("channel manager not initialized")
			}
			if _, exists := cm.GetChannel(value); !exists && value != "cli" {
				return fmt.Errorf("channel '%s' not found or not enabled", value)
			}
			return nil
		},
	}
	if agent != nil && agent.ContextBuilder != nil {
		rt.ListSkillNames = agent.ContextBuilder.ListSkillNames
	}
	rt.ReloadConfig = func() error {
		if al.reloadFunc == nil {
			return ErrReloadNotConfigured
		}
		return al.reloadFunc()
	}
	if agent != nil {
		if agent.ContextBuilder != nil {
			rt.ListSkillNames = agent.ContextBuilder.ListSkillNames
		}
		rt.GetModelInfo = func() (string, string) {
			agent.mu.RLock()
			m, c := agent.Model, agent.Candidates
			agent.mu.RUnlock()
			return m, resolvedCandidateProvider(c, cfg.Agents.Defaults.Provider)
		}
		rt.SwitchModel = func(value string) (string, error) {
			// Shared in-place model switch (#73): same path as the
			// PUT /api/v1/agents/{id} model change.
			return al.ApplyAgentModel(agent.ID, value)
		}

		rt.ClearHistory = func() error {
			if opts == nil {
				return fmt.Errorf("process options not available")
			}
			if agent.Sessions == nil {
				return fmt.Errorf("sessions not initialized for agent")
			}

			agent.Sessions.SetHistory(opts.SessionKey, make([]providers.Message, 0))
			agent.Sessions.SetSummary(opts.SessionKey, "")
			return agent.Sessions.Save(opts.SessionKey)
		}
	}

	// Inject the session ID accessor so /cancel can target the current session.
	if opts != nil {
		sessionKey := opts.SessionKey
		rt.SessionID = func() string { return sessionKey }
	}

	// Inject the agent loop so CancelActiveTurn can call InterruptSession.
	rt = rt.WithAgentLoop(al)

	return rt
}

func commandsUnavailableSkillMessage() string {
	return "Skill selection is unavailable in the current context."
}

func buildUseCommandHelp(agent *AgentInstance) string {
	if agent == nil || agent.ContextBuilder == nil {
		return "Usage: /use <skill> [message]"
	}

	names := agent.ContextBuilder.ListSkillNames()
	if len(names) == 0 {
		return "Usage: /use <skill> [message]\nNo installed skills found."
	}

	return fmt.Sprintf(
		"Usage: /use <skill> [message]\n\nInstalled Skills:\n- %s\n\nUse /use <skill> to apply a skill to your next message, or /use <skill> <message> to force it immediately.",
		strings.Join(names, "\n- "),
	)
}

func (al *AgentLoop) setPendingSkills(sessionKey string, skillNames []string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(skillNames) == 0 {
		return
	}

	filtered := make([]string, 0, len(skillNames))
	for _, name := range skillNames {
		name = strings.TrimSpace(name)
		if name != "" {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		return
	}

	al.pendingSkills.Store(sessionKey, filtered)
}

func (al *AgentLoop) takePendingSkills(sessionKey string) []string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}

	value, ok := al.pendingSkills.LoadAndDelete(sessionKey)
	if !ok {
		return nil
	}

	skills, ok := value.([]string)
	if !ok {
		return nil
	}

	return append([]string(nil), skills...)
}

func (al *AgentLoop) clearPendingSkills(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	al.pendingSkills.Delete(sessionKey)
}

func mapCommandError(result commands.ExecuteResult) string {
	if result.Command == "" {
		return fmt.Sprintf("Failed to execute command: %v", result.Err)
	}
	return fmt.Sprintf("Failed to execute /%s: %v", result.Command, result.Err)
}

// extractPeer extracts the routing peer from the inbound message's structured Peer field.
func extractPeer(msg bus.InboundMessage) *routing.RoutePeer {
	if msg.Peer.Kind == "" {
		return nil
	}
	peerID := msg.Peer.ID
	if peerID == "" {
		if msg.Peer.Kind == "direct" {
			peerID = msg.SenderID
		} else {
			peerID = msg.ChatID
		}
	}
	return &routing.RoutePeer{Kind: string(msg.Peer.Kind), ID: peerID}
}

func inboundMetadata(msg bus.InboundMessage, key string) string {
	if msg.Metadata == nil {
		return ""
	}
	return msg.Metadata[key]
}

// inboundInstanceID returns the channel-instance key a message arrived on
// (Spec-2 FR-2.5). Channels MAY tag the instance explicitly via the
// "instance_id" metadata key; in v0.1 (cap-1/type) the instance key equals the
// channel type, so an untagged message falls back to msg.Channel. The result is
// lower-cased to match the config map keys (which are channel-type names).
func inboundInstanceID(msg bus.InboundMessage) string {
	if id := strings.TrimSpace(inboundMetadata(msg, metadataKeyInstanceID)); id != "" {
		return strings.ToLower(id)
	}
	return strings.ToLower(strings.TrimSpace(msg.Channel))
}

// resolveInboundIdentity returns the persisted routing identity for the channel
// instance a message arrived on (Spec-2 US-5 / FR-2.9), or nil when the instance
// has no identity override configured. The identity selects how an inbound
// message is attributed/routed: kind "agent" binds the connection to a specific
// agent; kind "user" (or no identity) leaves the normal binding cascade in
// effect. Returns a copy so the caller never mutates the live config.
func (al *AgentLoop) resolveInboundIdentity(instanceID string) *config.ChannelIdentity {
	if instanceID == "" {
		return nil
	}
	cfg := al.GetConfig()
	if cfg == nil || cfg.Channels == nil {
		return nil
	}
	inst, ok := cfg.Channels[instanceID]
	if !ok || inst.Identity == nil {
		return nil
	}
	id := *inst.Identity
	return &id
}

// extractParentPeer extracts the parent peer (reply-to) from inbound message metadata.
func extractParentPeer(msg bus.InboundMessage) *routing.RoutePeer {
	parentKind := inboundMetadata(msg, metadataKeyParentPeerKind)
	parentID := inboundMetadata(msg, metadataKeyParentPeerID)
	if parentKind == "" || parentID == "" {
		return nil
	}
	return &routing.RoutePeer{Kind: parentKind, ID: parentID}
}

// isNativeSearchProvider reports whether the given LLM provider implements
// NativeSearchCapable and returns true for SupportsNativeSearch.
func isNativeSearchProvider(p providers.LLMProvider) bool {
	if ns, ok := p.(providers.NativeSearchCapable); ok {
		return ns.SupportsNativeSearch()
	}
	return false
}

// filterClientWebSearch returns a copy of tools with the client-side
// web_search tool removed. Used when native provider search is preferred.
func filterClientWebSearch(tools []providers.ToolDefinition) []providers.ToolDefinition {
	result := make([]providers.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if strings.EqualFold(t.Function.Name, "web_search") {
			continue
		}
		result = append(result, t)
	}
	return result
}

// Helper to extract provider from registry for cleanup
func extractProvider(registry *AgentRegistry) (providers.LLMProvider, bool) {
	if registry == nil {
		return nil, false
	}
	// Get any agent to access the provider
	defaultAgent := registry.GetDefaultAgent()
	if defaultAgent == nil {
		return nil, false
	}
	return defaultAgent.Provider, true
}

// llmRate holds approximate per-1K-token pricing for a model family.
type llmRate struct {
	inputPer1K, outputPer1K float64
}

// llmRateFallback is used when no prefix in llmRateTable matches. It is
// deliberately conservative so unknown models over-count rather than silently
// escape the cost cap.
var llmRateFallback = llmRate{inputPer1K: 0.003, outputPer1K: 0.015}

// llmRateTable is an ordered prefix lookup — first match wins. Longer/more
// specific prefixes must appear before shorter ones. Rates are approximations
// for budgeting only and will not match provider invoices exactly.
var llmRateTable = []struct {
	prefix string
	rate   llmRate
}{
	// Anthropic Claude 3.x
	{"claude-3-5-haiku", llmRate{0.0008, 0.004}},
	{"claude-3-5-sonnet", llmRate{0.003, 0.015}},
	{"claude-3-haiku", llmRate{0.00025, 0.00125}},
	{"claude-3-sonnet", llmRate{0.003, 0.015}},
	{"claude-3-opus", llmRate{0.015, 0.075}},
	// Anthropic Claude 4.x
	{"claude-opus-4", llmRate{0.015, 0.075}},
	{"claude-sonnet-4", llmRate{0.003, 0.015}},
	{"claude-haiku-4", llmRate{0.0008, 0.004}},
	// OpenAI GPT-4 family
	{"gpt-4o-mini", llmRate{0.00015, 0.0006}},
	{"gpt-4o", llmRate{0.005, 0.015}},
	{"gpt-4-turbo", llmRate{0.01, 0.03}},
	{"gpt-4", llmRate{0.03, 0.06}},
	{"gpt-3.5-turbo", llmRate{0.0005, 0.0015}},
	// Google Gemini
	{"gemini-1.5-flash", llmRate{0.000075, 0.0003}},
	{"gemini-1.5-pro", llmRate{0.00125, 0.005}},
	{"gemini-2.0-flash", llmRate{0.0001, 0.0004}},
	{"gemini-2.5-pro", llmRate{0.00125, 0.01}},
}

// estimateLLMCallCost returns a conservative cost estimate in USD for a single
// LLM call given the model name and token usage (SEC-26). Unknown models fall
// back to llmRateFallback so the cost accumulator never under-counts.
func estimateLLMCallCost(model string, usage *providers.UsageInfo) float64 {
	if usage == nil {
		return 0
	}

	lowerModel := strings.ToLower(model)
	rate := llmRateFallback
	for _, entry := range llmRateTable {
		if strings.HasPrefix(lowerModel, entry.prefix) {
			rate = entry.rate
			break
		}
	}

	inputCost := float64(usage.PromptTokens) / 1000.0 * rate.inputPer1K
	outputCost := float64(usage.CompletionTokens) / 1000.0 * rate.outputPer1K
	return inputCost + outputCost
}

// braveKeys returns a []string for use as BraveAPIKeys. Returns nil if the key is empty.
func braveKeys(key string) []string {
	if key == "" {
		return nil
	}
	return []string{key}
}

// tavilyKeys returns a []string for use as TavilyAPIKeys. Returns nil if the key is empty.
func tavilyKeys(key string) []string {
	if key == "" {
		return nil
	}
	return []string{key}
}

// perplexityKeys returns a []string for use as PerplexityAPIKeys. Returns nil if the key is empty.
func perplexityKeys(key string) []string {
	if key == "" {
		return nil
	}
	return []string{key}
}

// checkToolDedupInvariant verifies that the assembled tool list has no duplicate
// names (FR-066). Returns a non-nil error on the first duplicate found; also emits
// a HIGH audit event "tool.assembly.duplicate_name" and logs a structured error.
func (al *AgentLoop) checkToolDedupInvariant(ts *turnState, filtered []tools.Tool) error {
	seen := make(map[string]string, len(filtered)) // name → first source tag
	for _, t := range filtered {
		name := t.Name()
		var sourceTag string
		if cat := t.Category(); cat == tools.CategoryMCP {
			sourceTag = "mcp:unknown"
		} else {
			sourceTag = "builtin"
		}
		if firstSrc, exists := seen[name]; exists {
			// Duplicate detected — audit + fail.
			sources := []string{firstSrc, sourceTag}
			details := map[string]any{
				"tool_name": name,
				"sources":   sources,
				"kept":      firstSrc,
				"agent_id":  ts.agentID,
				"turn_id":   ts.turnID,
			}
			logger.ErrorCF("agent", "FR-066: tools[] dedup invariant violated",
				map[string]any{"tool_name": name, "sources": sources, "agent_id": ts.agentID})
			// CRIT-6 + typed-Decision/Event migration: route through audit.EmitEntry
			// so Log failure bumps the audit-skipped counter; use the typed
			// EventToolAssemblyDuplicateName and DecisionDeny constants.
			audit.EmitEntry(al.auditLogger, &audit.Entry{
				Event:     audit.EventToolAssemblyDuplicateName,
				Decision:  audit.DecisionDeny,
				AgentID:   ts.agentID,
				Tool:      name,
				SessionID: ts.sessionKey,
				Details:   details,
			})
			return fmt.Errorf("tools[] dedup invariant violated: tool %q appears from sources %v", name, sources)
		}
		seen[name] = sourceTag
	}
	return nil
}

// resolveToolPolicyAtExec performs the FR-079 TOCTOU re-check: re-loads the
// per-agent policy pointer and re-resolves the effective policy for toolName
// immediately before Execute is called.
//
// Returns the live effective policy ("allow", "ask", "deny").
// If the live policy matches the filter-time snapshot (filterTimePolicyMap) the
// return value is the same and no audit is emitted; discrepancies are audited as
// "mid_turn_policy_change" by the caller.
//
// A tool absent from filterTimePolicyMap was not in the filter-time allow set
// (it was deny at filter time or not registered). If it is also deny at re-check
// time we return "deny"; if it has become allow/ask we still return "deny" to be
// conservative (the LLM was not given this tool's definition, so executing it is
// unsound).
func (al *AgentLoop) resolveToolPolicyAtExec(
	ts *turnState,
	toolName string,
	filterTimePolicyMap map[string]string,
) string {
	filterTimePolicy, wasInFilterMap := filterTimePolicyMap[toolName]
	if !wasInFilterMap {
		// Tool was not included at filter time — treat as deny regardless of live policy.
		return "deny"
	}

	// Re-run FilterToolsByPolicy with the freshly loaded pointer to get the live
	// effective policy for this one tool. We run on the full tool list but only
	// care about our tool.
	livePolicy := al.resolveSingleToolPolicy(ts, toolName)

	// If policy flipped to deny mid-turn, the caller will audit "mid_turn_policy_change".
	if livePolicy == "deny" && filterTimePolicy != "deny" {
		return "deny"
	}
	// If policy is now ask but was allow at filter time — treat as ask (conservative).
	if livePolicy == "ask" {
		return "ask"
	}
	// Use filter-time policy as the authoritative effective policy when live==allow
	// and filter-time was ask — preserves the ask gate from filter time.
	if filterTimePolicy == "ask" {
		return "ask"
	}
	return filterTimePolicy
}

// resolveSingleToolPolicy loads the current policy pointer and resolves the
// effective policy for toolName using FilterToolsByPolicy. Returns "" if the
// tool is not found in the agent's registered tools.
func (al *AgentLoop) resolveSingleToolPolicy(ts *turnState, toolName string) string {
	allTools := ts.agent.Tools.GetAll()
	_, pmap := tools.FilterToolsByPolicy(allTools, ts.agent.AgentType, ts.agent.LoadToolPolicy())
	p, ok := pmap[toolName]
	if !ok {
		return "deny"
	}
	return p
}

// loadToolApprover returns the wired PolicyApprover or, when none has been
// set, a fail-closed nopPolicyApprover that denies every ask with reason
// "no_approver_configured" and emits one `approver.fallback` audit row per
// process (V2.B; closes silent-failure-hunter BE CRIT-1). The nop carries
// al.auditLogger so the diagnostic emit lands in the operator's JSONL.
//
// The previous default returned `nopPolicyApprover{}` which auto-approved
// every ask call — including admin-flagged tools — with zero log and zero
// audit. Test code that needs the auto-approve behavior now installs
// `testAutoApproveApprover{}` explicitly via SetToolApprover (build tag
// `test`; see `tool_approver_testonly.go`).
func (al *AgentLoop) loadToolApprover() PolicyApprover {
	al.mu.RLock()
	a := al.toolApprover
	logger := al.auditLogger
	al.mu.RUnlock()
	if a == nil {
		return nopPolicyApprover{auditLogger: logger}
	}
	return a
}

// toolRequiresAdmin returns true when the named tool implements RequiresAdminAsk()
// and that method returns true.
func (al *AgentLoop) toolRequiresAdmin(ts *turnState, toolName string) bool {
	t, ok := ts.agent.Tools.Get(toolName)
	if !ok {
		return false
	}
	if asker, ok := t.(interface{ RequiresAdminAsk() bool }); ok {
		return asker.RequiresAdminAsk()
	}
	return false
}

// emitPolicyDenyAudit writes a tool.policy.deny.attempted audit entry.
// context is a free-form note such as "mid_turn_policy_change" or the denial reason.
// extra, when non-nil, is merged into the Details map so callers can attach
// structured fields (e.g. schedule_job_id) without a separate emit.
//
// CRIT-6 + typed-Decision/Event migration: routes through audit.EmitEntry so
// Log failure bumps the audit-skipped counter (/health audit_degraded), and
// uses the typed EventToolPolicyDenyAttempted + DecisionDeny constants in
// place of raw string literals.
func (al *AgentLoop) emitPolicyDenyAudit(
	ts *turnState,
	toolName, resolvedPolicy, context string,
	extra ...map[string]any,
) {
	details := map[string]any{
		"turn_id":         ts.turnID,
		"resolved_policy": resolvedPolicy,
		"context":         context,
	}
	for _, m := range extra {
		for k, v := range m {
			details[k] = v
		}
	}
	audit.EmitEntry(al.auditLogger, &audit.Entry{
		Event:     audit.EventToolPolicyDenyAttempted,
		Decision:  audit.DecisionDeny,
		AgentID:   ts.agentID,
		Tool:      toolName,
		SessionID: ts.sessionKey,
		Details:   details,
	})
}

// emitScheduledAutoDenyAudit writes a tool.policy.ask.denied audit entry for
// the headless scheduled-run auto-deny path (O-3 / F-13 / issue #342).
//
// It routes through audit.EmitToolPolicyAskDenied (the canonical helper) so:
//   - CRIT-6 is honored: write failures bump IncSkipped via the Emit path,
//     making /health audit_degraded accurate.
//   - Severity is SeverityInfo (documented contract for ask.denied events).
//   - The reason field carries AskDenyReasonScheduled so SIEM rules can filter
//     headless auto-denies without string-matching the context note.
//
// The schedule identity (job_id, job_name) is read from the run context via
// scheduledJobContextFrom — the cron fire path (gateway RunScheduled) injects
// it with WithScheduledJobContext before calling ProcessScheduled. When the
// job info is missing from the context (e.g. a caller that omits the wrapper),
// a slog.Warn is emitted so the lost attribution is loud, and the ask.denied
// entry is still written (with empty schedule fields) so the denial is always
// recorded.
//
// The companion emitPolicyDenyAudit call (tool.policy.deny.attempted) at the
// same call site carries the same schedule identity in its Details map, giving
// operators two correlated entries per auto-deny: one for the policy-deny
// audit trail and one for the structured ask.denied reason.
func (al *AgentLoop) emitScheduledAutoDenyAudit(
	ctx context.Context,
	ts *turnState,
	toolName, toolCallID string,
) {
	var jobID, jobName string
	if info, ok := scheduledJobContextFrom(ctx); ok && info.JobID != "" {
		jobID = info.JobID
		jobName = info.JobName
	} else {
		// MEDIUM: missing job identity means the audit entry can't name the
		// schedule that was responsible for the skip. This is loud so operators
		// notice mis-wired fire paths (e.g. ProcessScheduled called without
		// WithScheduledJobContext). The ask.denied entry is still emitted —
		// losing the denial record entirely is worse than a partially-attributed
		// one.
		logger.WarnCF("agent", "scheduled auto-deny audit: job identity missing from context",
			map[string]any{
				"note":        "ask.denied entry will lack schedule_job_id/name",
				"agent_id":    ts.agentID,
				"tool":        toolName,
				"session_key": ts.sessionKey,
			},
		)
	}

	// Emit the canonical tool.policy.ask.denied record. approvalID and
	// approverUserID are empty: in the headless path no approval was ever
	// requested, so there is no approval id to reference and no human actor.
	// argsHash and cancelledToolCallIDs are also empty / nil for the same reason.
	audit.EmitToolPolicyAskDenied(
		ctx,
		al.auditLogger,
		"", // approvalID — no approval request was made
		"", // approverUserID — no human actor
		toolName,
		ts.agentID,
		ts.sessionKey,
		ts.turnID,
		audit.AskDenyReasonScheduled,
		"",  // argsHash — not available at this call site
		nil, // cancelledToolCallIDs — not applicable
	)

	// The schedule identity (jobID, jobName) also appears in the companion
	// tool.policy.deny.attempted entry emitted by the emitPolicyDenyAudit call
	// at the auto-deny call site — the caller reads it from the context and
	// passes it as extra Details there. Log it here too so the structured log
	// line is self-contained even when audit writing is disabled.
	if jobID != "" {
		logger.InfoCF("agent", "scheduled auto-deny: ask-gated tool skipped in headless run",
			map[string]any{
				"schedule_job_id":   jobID,
				"schedule_job_name": jobName,
				"tool":              toolName,
				"agent_id":          ts.agentID,
			},
		)
	}
}
