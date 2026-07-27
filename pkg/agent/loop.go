// Omnipus - Ultra-lightweight personal AI agent
// Built on Omnipus's foundation. See CLAUDE.md for project lineage.
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/policy"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/capabilities"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/state"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/captureext"
	"github.com/elicify-ai/omnipus/pkg/utils"
	"github.com/elicify-ai/omnipus/pkg/voice"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ModelSyntheticImageRejection is the transcript-stamp value the loop sets on
// `ts.lastProducedModel` when the LLM provider refuses an image-bearing input
// and we synthesize a guidance reply in place of the raw error. The provider
// did NOT actually emit this turn — the stamp exists so the transcript
// `model` field does not mis-attribute the synthesized guidance to the
// refusing model. Downstream consumers (replay, future filters) can detect
// this sentinel by typed string-comparison; the "synthetic:" prefix is
// reserved for non-LLM-produced transcripts.
const ModelSyntheticImageRejection = "synthetic:image-rejection"

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
	mu               sync.RWMutex

	// Concurrent turn management
	// recallSpans holds the transient in-memory recall span per session
	// (FR-019): key sessionKey (string), value *RecallSpan. Never persisted;
	// set by recall_conversation, read/dropped by windowTrim + assembly.
	recallSpans        sync.Map     // key: sessionKey (string), value: *RecallSpan
	activeTurnStates   sync.Map     // key: sessionKey (string), value: *turnState
	subTurnCounter     atomic.Int64 // Counter for generating unique SubTurn IDs
	sessionActiveAgent sync.Map     // key: "session:"+sessionID (string), value: agentID (string); set by handoff, cleared on agent deletion

	// orphanWatches holds the orphan-foreground-turn watchdog's pending grace
	// timer per session (ADR-045): key sessionID (string), value *orphanWatch.
	// Populated by ArmOrphanForegroundTurnWatch, removed by
	// DisarmOrphanForegroundTurnWatch or once the grace timer fires. See
	// pkg/agent/orphan_watch.go.
	orphanWatches sync.Map

	// Turn tracking
	turnSeq        atomic.Uint64
	activeRequests sync.WaitGroup

	// mediaRefsDropped counts media refs that could not be resolved (unknown ref
	// or file missing on disk). Observable via GetMediaRefsDropped for tests and
	// diagnostics; incremented atomically from resolveMediaRefs hot path.
	mediaRefsDropped atomic.Int64

	// driftDropped counts bound-instance drift drops: inbound messages on a
	// workspace-bound channel instance whose configured agent is unresolvable
	// (deleted or a worker — not a chat target). Each drop increments this
	// counter exactly once at the processMessage rejection point (ADR-029
	// FR-028 / MAJ-003). Observable via GetDriftDropped for tests and diagnostics.
	driftDropped atomic.Int64

	reloadFunc    func() error
	reloadPending atomic.Bool // set by TriggerReload; cleared by ClearReloadPending (called from gateway executeReload)

	// Task management
	taskStore    *task.Store
	taskExecutor *TaskExecutor
	// taskTrigger fires once/every/recurring task triggers via the dedicated
	// trigger CronService. Set at boot by the gateway (SetTaskTriggerScheduler);
	// nil in tests / before wiring. All notify paths are nil-safe.
	taskTrigger *TaskTriggerScheduler

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
	// nil-check. Trusted tool results (bash, delegate, message, etc.) are NEVER
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

	// browserMgrs holds one BrowserManager per agent (US-4/US-6/US-7; ADR-038
	// D4). Populated in registerSharedTools, keyed by agentID; guarded by mu
	// (the same lock the old single al.browserMgr field used). Every entry's
	// connection is torn down in AgentLoop.Close() — AND, per ADR-038 finding
	// #2 + ADR-043, whenever registerSharedTools replaces an existing agentID's
	// entry on hot-reload (see the Release/Shutdown call at that site). In
	// ADR-043 shared-Chrome mode (the normal gateway case) that teardown is
	// coordinator.Release(agentID) — it drops only the manager's WS connection
	// (CRIT-002/C1: does NOT kill Chrome, does NOT dispose the browser
	// context, which survives for the new manager to re-adopt). The Chrome
	// process itself is killed solely by coordinator.Shutdown() in Close().
	// In the no-coordinator test/legacy path the old manager IS its own Chrome
	// owner, so manager.Shutdown() (which cancels the chromedp allocator
	// context) is the real process kill there. Dropping the Go
	// *BrowserManager reference alone never kills anything — the allocator
	// context is parented on context.Background(), not on the reference — so
	// the explicit Release/Shutdown on reload is what prevents a per-reload
	// Chromium leak in that legacy path. registerSharedTools re-runs on every
	// ReloadProviderAndConfig (any Settings save).
	//
	// Before ADR-038 D4 this was a single `browserMgr *browser.BrowserManager`
	// field, overwritten (with the prior value Shutdown()'d) on every agent
	// processed by registerSharedTools's per-agent loop — so only the LAST
	// agent registered ended up with a live manager; every earlier agent's
	// browser tools silently operated on an already-Shutdown() manager. Do
	// NOT reintroduce a single shared field — the gateway's live-view WS
	// handler (pkg/gateway/browser_ws.go) needs a specific agent's manager,
	// not "whichever agent registered last."
	browserMgrs map[string]*browser.BrowserManager

	// browserCoordinator (ADR-043) is the gateway-scoped owner of the ONE
	// shared Chrome + every agent's browser context. Constructed once and
	// reused across hot-reload (ReloadProviderAndConfig reuses this *AgentLoop,
	// so the coordinator — and the per-agent contexts it owns — survive a
	// Settings save). nil only in tests that construct managers directly.
	browserCoordinator *browser.BrowserCoordinator
	// homePath is $OMNIPUS_HOME (the parent of the workspace path), handed to
	// NewBrowserCoordinator, which builds the ownership-marker path
	// (<homePath>/browser/shared-chrome.pid) from it.
	homePath string

	// capabilityCatalog (ADR-051 Rev 4, Wave 3 T9) is the step-1 capability
	// gate source for the presentation chain (FR-010). Constructed from the
	// compiled-in seed at NewAgentLoop time so the gate works without gateway
	// boot wiring; the gateway may inject a puller-equipped catalog via
	// SetCapabilityCatalog (FR-025 repo-pull). Nil → optimistic (FR-026).
	// Guarded by mu (the struct's primary RWMutex).
	capabilityCatalog *capabilities.Catalog

	// workspaceLibCache (FR-007a) caches per-workspace media libraries for
	// manifest-refcount accounting. Keyed by workspace ID. Lazily populated
	// by getWorkspaceLibrary; refcount mutations go through the cached
	// instance so a workspace always sees consistent refcount state.
	workspaceLibCache sync.Map // map[string]*library.Library

	// sessionRefcounts (FR-007a) tracks the per-session manifest-refcount
	// state (workspace ID + seen-set) so CloseSession can run the matching
	// decrement pass. Keyed by transcript session ID. Populated by
	// getTurnRefcounter during turns; drained by
	// decrementSessionMediaRefcounts at session close.
	sessionRefcounts sync.Map // map[string]*sessionRefcountState

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

	// runnerEgressProxy is a SEPARATE egress proxy for external-runner CLI
	// children (Spec-4 FR-5.3). Unlike sandboxEgressProxy (Tier2/Tier3, host
	// allow-list deny-by-default), the runner proxy applies SSRF internal-CIDR
	// blocking and allows all external hosts — external CLIs need broad egress
	// to reach their model providers. Lazily started on first external-CLI
	// dispatch (runnerEgressProxyOnce); nil when the proxy could not start
	// (dispatch degrades gracefully without HTTP_PROXY injection).
	runnerEgressProxy     *sandbox.EgressProxy
	runnerEgressProxyOnce sync.Once

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

	// memoryRateLimiter is the shared MemoryRateLimiter (v0.2 #155 item 6),
	// built once in NewAgentLoop and applied to every agent's remember/
	// run_retrospective tools via wireMemoryRateLimiterOn. Stored here (rather
	// than left a bare local, as it originally was) so ReloadProviderAndConfig
	// can re-wire the SAME limiter instance onto the freshly-built registry on
	// hot reload — constructing a new limiter on every reload would reset
	// every agent's sliding-window rate-limit buckets on any unrelated config
	// change. Like tier13Deps/sysagentDeps, this field is only ever written
	// during NewAgentLoop (single-threaded) or ReloadProviderAndConfig, which
	// the gateway serializes via its own reloadMu — no dedicated lock needed.
	memoryRateLimiter *tools.MemoryRateLimiter

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

	// approvalGrants tracks per-session "Always Allow" tool-approval grants,
	// scoped by (session_id, agent_id, tool_name). Always non-nil after
	// NewAgentLoop. Fixes the tool-consent-boundary bug: the grant used to
	// live on the per-WebSocket-CONNECTION wsApprovalHook.alwaysAllowed map
	// and was silently discarded on every reconnect (network blip, idle,
	// gateway restart, refresh — the SPA auto-reconnects on any drop). This
	// store instead lives for the AgentLoop's lifetime and is cleared
	// per-SESSION (via CloseSession), not per-connection, so the grant
	// survives reconnects while still expiring with the session it belongs
	// to. Shared by the gateway's tool-approval REST path (IsAllowed/Record —
	// see AgentLoop.CheckGrantOrRequestApproval) and the delegate tool's
	// async/await paths (Inherit — pkg/agent/subturn.go).
	approvalGrants *security.ApprovalGrantStore

	// asyncNotifier is the single process-wide AsyncNotifier instance
	// (async-notifier-spec.md), extracted from the formerly-inline
	// asyncCallback closure below. Scoped to the loop's lifetime the same
	// way approvalGrants is, per the spec's Clarifications. Always non-nil
	// after NewAgentLoop.
	asyncNotifier *asyncNotifierImpl

	// sharedSessionStore is the single UnifiedStore at $OMNIPUS_HOME/sessions/
	// used for all new sessions (joined session model). Legacy per-agent stores
	// remain accessible via GetAgentStore for read-only access to old sessions.
	sharedSessionStore *session.UnifiedStore

	// toolApprover is the gateway-injected implementation of the human-in-the-loop
	// approval gate (FR-011, FR-082). Nil until SetToolApprover is called; when nil,
	// ask-policy tools are treated as allow (open gate, no WS event).
	toolApprover PolicyApprover

	// allowGodMode is set when the gateway was started with --allow-god-mode
	// (or the config-persisted sandbox.god_mode_allowed grant — see
	// resolveAllowGodMode). Combined with sandbox.GodModeAvailable (build
	// support) via SetAllowGodMode to publish the process-wide god-mode
	// AVAILABILITY latch consulted by GodModeActive. Latch (2).
	allowGodMode bool

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

	// loadedTools tracks which lazy tools have been on-demand loaded by the
	// manifest optimization (cfg.Tools.Manifest.Compressed) for each session.
	// Key: manifest session ID (transcript session ID, or the session key when
	// transcripts are disabled — see manifestSessionID). Value: map[string]bool
	// (tool name → loaded). Protected by loadedToolsMu. A new session ID lazily
	// creates a fresh set on first load; entries are evicted by forgetSession on
	// CloseSession (transcript sessions). Only populated when Compressed is true.
	loadedTools   map[string]map[string]bool
	loadedToolsMu sync.Mutex

	// lastTurnResultMu guards lastTurnResult.  Written by runAgentLoop after
	// every turn; read by tests to assert turnFailed without threading the flag
	// through the full public call stack.  Never read in production paths.
	lastTurnResultMu sync.Mutex
	lastTurnResult   turnResult
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey        string // Session identifier for history/context
	Channel           string // Target channel for tool execution
	ChatID            string // Target chat ID for tool execution
	SenderID          string // Current sender ID for dynamic context
	SenderDisplayName string // Current sender display name for dynamic context
	// UserID is the authenticated gateway principal that initiated this turn,
	// threaded from the WS connection (websocket.go wc.userID) via the dedicated
	// bus.InboundMessage.GatewayUserID carrier (FR-017). It is stamped onto
	// turn-scoped audit.Entry.User so CLI runs (principal "cli") and admin browser
	// sessions are attributable. Empty for channel-originated turns (the platform
	// sender in Sender.Username is not a gateway principal and is never read here)
	// and unauthenticated env-token / dev-bypass paths — never guessed.
	UserID                  string                // Authenticated gateway principal (FR-017)
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

	// InitialDelegationDepth seeds the root turnState depth for a task run. A
	// task created from within another task run carries a non-zero generation
	// (task.Task.DelegationDepth); processTaskDirect seeds it here so the
	// per-workspace delegation-graph edge's depth gate (currentDelegationDepth)
	// trips on onward await/background delegation even though a task run
	// otherwise starts a fresh turn at depth 0. Interactive/chat turns leave
	// this 0.
	InitialDelegationDepth int

	// IsTaskRun marks a turn as a native task-dispatch run (set by
	// processTaskDirect's runAgentLoop call; false for interactive chat,
	// heartbeat, and the external-CLI task path, which never reaches
	// assembleMessages at all). assembleMessages reads it to decide whether to
	// append a terse TASK_STATUS/TASK_SUMMARY marker reminder to the
	// breadcrumb block (review B3): a task's marker instruction lives only in
	// its first user turn (buildPrompt, task_executor.go), and windowTrim
	// (ADR-028) can evict that turn on a long, tool-heavy task run — this flag
	// is what lets the reminder re-surface exactly when (and only when) that
	// eviction has actually happened, piggybacking on the breadcrumb's own
	// eviction-survives-everything delivery mechanism rather than adding a
	// second, parallel injection path.
	IsTaskRun bool
}

// gatewayPrincipal returns the WS-authenticated gateway principal that an
// inbound message carries for audit attribution (FR-017), or "" when none.
//
// It reads ONLY bus.InboundMessage.GatewayUserID — the dedicated carrier set
// solely by the gateway webchat WS path (pkg/gateway/websocket.go, from
// wc.userID). It deliberately ignores Sender.Username: production channels
// (Telegram, Discord, IRC, Matrix, Google Chat, WeiXin) populate Sender.Username
// with the platform handle (e.g. "@alice"), which is NOT a gateway principal and
// must never be stamped as audit User. Channel/task/scheduled inbound messages
// never set GatewayUserID, so this returns "" for them — leaving audit.Entry.User
// empty structurally rather than by a runtime channel-name guard.
func gatewayPrincipal(msg bus.InboundMessage) string {
	return msg.GatewayUserID
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
	// WorkspaceID is the workspace this continuation's turn should run inside
	// — see buildContinuationTarget's resolution comment (FIX 1 re-review).
	WorkspaceID string
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

// ErrAgentNotWorkspaceMember is returned by runTurn when the acting agent is
// not a member of any workspace's CoreTeam (ADR-046 P1, FR-007/008). Execution
// is always workspace-scoped: agents are metadata until added to a workspace's
// team, and a turn for an unassigned agent MUST be refused rather than
// silently falling through to the agent's own private home directory. This
// applies uniformly to top-level and delegated (spawnSubTurn) turns alike,
// since both resolve ts.agent.ID the same way in the re-root block below.
var ErrAgentNotWorkspaceMember = errors.New("agent is not a member of any workspace; turn refused")

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
// Returns (*AgentLoop, nil) on success or (nil, error) on a fatal configuration error.
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
		stateManager = state.NewManager(defaultAgent.Home)
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
		loadedTools:            make(map[string]map[string]bool),
		browserMgrs:            make(map[string]*browser.BrowserManager),
	}
	al.hooks = NewHookManager(eventBus)
	configureHookManagerFromConfig(al.hooks, cfg)

	// Initialize the unified task store at ~/.omnipus/tasks/ (the single store —
	// the legacy GTD tasks/ and workflow-tasks/ split was removed in Sprint 2).
	homePath := filepath.Dir(cfg.AgentHomeBasePath())
	al.homePath = homePath
	al.taskStore = task.New(filepath.Join(homePath, "tasks"))
	al.taskExecutor = newTaskExecutor(al, al.taskStore)

	// ADR-051 Rev 4 (Wave 3 T9): construct the capability catalog from the
	// compiled-in seed so the step-1 presentation gate (FR-010) works
	// immediately, without gateway boot wiring. The gateway may later inject
	// a puller-equipped catalog via SetCapabilityCatalog (FR-025 repo-pull).
	// A construction failure is non-fatal — nil catalog → optimistic (FR-026).
	if catalog, catErr := capabilities.NewCatalog(capabilities.EmbeddedSeed(), nil, nil, nil); catErr != nil {
		logger.WarnCF(
			"agent",
			"Capability catalog construction failed; presentation gate degrades to optimistic",
			map[string]any{
				"error": catErr.Error(),
			},
		)
	} else {
		al.capabilityCatalog = catalog
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

			// Wire audit logger into all agent tool registries. Factored out into
			// wireMemoryAuditLoggerOn so ReloadProviderAndConfig can re-apply the
			// same wiring against a freshly-built registry on hot reload (see that
			// method's doc comment) — without this, every agent's remember/
			// run_retrospective tools would silently lose audit logging (SEC-15)
			// the first time config reloads.
			al.wireMemoryAuditLoggerOn(registry, auditLogger)
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
	secCfg := &policy.SecurityConfig{
		DefaultPolicy: defaultPolicy,
		Policy: policy.PolicySection{
			Exec: policy.ExecPolicy{
				AllowedBinaries: cfg.Tools.Exec.AllowedBinaries,
				Approval:        cfg.Tools.Exec.Approval,
			},
		},
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

	// Session-scoped tool-approval grant store (consent boundary fix): shared
	// by the gateway's tool-approval REST path and the delegate tool's
	// async/await paths. Always non-nil.
	al.approvalGrants = security.NewApprovalGrantStore()

	// Process-wide AsyncNotifier (async-notifier-spec.md): the reusable
	// "wake the conversation when background work finishes" primitive,
	// extracted from the asyncCallback closure below. Always non-nil.
	al.asyncNotifier = newAsyncNotifier(al)

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
	//
	// Stashed on al.memoryRateLimiter (not a bare local) so hot-reload
	// (ReloadProviderAndConfig) can re-apply the SAME limiter instance onto
	// the freshly-built registry via wireMemoryRateLimiterOn — constructing a
	// new limiter on every reload would reset every agent's sliding-window
	// buckets on any unrelated config change.
	al.memoryRateLimiter = tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{})
	al.wireMemoryRateLimiterOn(registry, al.memoryRateLimiter)
	logger.InfoCF("agent", "Memory write rate limiter initialized",
		map[string]any{
			"per_agent_per_minute":  al.memoryRateLimiter.PerAgentLimit(),
			"per_caller_per_minute": al.memoryRateLimiter.PerCallerLimit(),
		})

	// Register shared tools to all agents (now that al is created)
	registerSharedTools(al, cfg, msgBus, registry, provider)

	// Replace the exec tool in each agent's registry with a version that has
	// the policy auditor and sandbox backend wired in. Registering the same
	// tool name overwrites the previous entry (see ToolRegistry.Register).
	al.wireExecToolDeps()

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

// fireIdleTimeout is a TEST SEAM. It triggers the same CloseSession("idle")
// code path that the timer in resetIdleTicker would trigger on expiry, without
// waiting for the real timer to fire. Calling this in production code is wrong
// — it exists solely to let tests exercise the idle→close pipeline in
// milliseconds rather than waiting a whole idle-timeout period (≥1 min).
// Production timing is completely unaffected: this function is never called by
// the runtime path, and adding it introduces no new goroutine or scheduling.
func (al *AgentLoop) fireIdleTimeout(sessionID string) {
	// Cancel the outstanding idle ticker first, mirroring what the timer goroutine
	// does implicitly when its context is the only reference to cancel.
	al.cancelIdleTicker(sessionID)
	al.CloseSession(sessionID, "idle")
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

// ApprovalGrants returns the session-scoped "Always Allow" tool-approval
// grant store. Always non-nil after NewAgentLoop (a nil AgentLoop returns
// nil). Every ApprovalGrantStore method is itself nil-receiver-safe and
// fails closed (IsAllowed => false, i.e. "ask"), so callers never need an
// extra nil check before chaining a call onto this accessor's result.
func (al *AgentLoop) ApprovalGrants() *security.ApprovalGrantStore {
	if al == nil {
		return nil
	}
	return al.approvalGrants
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
//
// Wave 1 (rate-limit dedup correctness, ADR-051 §RD6): this function
// emits ONE event (EventKindRateLimit) and writes ONE transcript entry —
// the previous "EventKindError + RateLimitPayload + EventKindRateLimit"
// dual-emit caused two live frames for the same condition (the WS
// forwarder then had to suppress the duplicate via code==rate_limited,
// but the EventKindError was still emitted onto the bus with a payload
// that was typed RateLimitPayload, not ErrorPayload — leaking the dual
// shape into every subscriber). The transcript write now goes through
// appendErrorTranscript's classifier with pe=nil; the classifier
// recognizes the "rate limit: …" shape via the message substring and
// emits CodeRateLimited, but the friendly caller-supplied message is
// preserved verbatim (ADR-051 §RD5 MAJ-001/004 carve-out — rate-limit
// messages are already user-safe and don't need a second translation
// pass).
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
			User:       ts.auditUser(), // FR-017
			Tool:       payload.Tool,
			PolicyRule: payload.PolicyRule,
			Details:    details,
		})
	}
	// FR-001: rate-limit denials MUST be visible after a page reload.
	// EventKindRateLimit is the authoritative live frame for the WS toast
	// AND the source for the dedicated denial banner. Replay reads the
	// transcript entry written below. No duplicate EventKindError emit —
	// the prior dual-emit (EventKindError carrying RateLimitPayload +
	// EventKindRateLimit) was a live-bus pollution source and was removed
	// in the Wave 1 fix pass.
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

// wireExecToolDeps replaces each agent's bash tool with one constructed via
// NewExecToolWithDeps, injecting the policy auditor (SEC-05), the ADR-035
// god-mode/egress-proxy hardening deps, and the deny-pattern configuration
// (ADR-036 — this is now the ONE registration path for `bash`, folding in what
// used to be the separate workspace_shell/workspace_shell_bg wiring in
// WireTier13Deps). This runs after NewAgentInstance has created the default
// bash tool so that all other tool setup (allow paths) is preserved — we only
// add the security deps on top.
//
// No-op when the agent has bash disabled or when the registry lookup fails.
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

	// O14 god-mode: the single source of truth for the sandbox escape hatch
	// (ADR-035). When active: full host fs + syscalls, network egress open,
	// shell guard / deny-patterns off, regardless of per-agent shell policy.
	godMode := GodModeActive(cfg)

	globalShellDenyPatterns := cfg.Sandbox.ShellDenyPatterns
	if godMode {
		globalShellDenyPatterns = nil
	}

	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok || agent == nil || agent.Tools == nil {
			continue
		}

		var agentShellPolicy *config.AgentShellPolicy
		for i := range cfg.Agents.List {
			entry := &cfg.Agents.List[i]
			if entry.ID == agentID {
				agentShellPolicy = entry.ShellPolicy
				break
			}
		}
		if godMode {
			agentShellPolicy = nil // drop per-agent deny patterns under god mode
		}

		deps := tools.ExecToolDeps{
			GodMode:                 godMode,
			AuditFailClosed:         resolveBoolWithDefault(cfg.Sandbox.PathGuardAuditFailClosed, cfg.Sandbox.AuditLog),
			GlobalShellDenyPatterns: globalShellDenyPatterns,
			AgentShellPolicy:        agentShellPolicy,
		}
		// Plumb the kernel-sandbox egress proxy into the bash tool so the
		// hardened path (non-god-mode) injects HTTP_PROXY pointing at the
		// allow-listed proxy. Nil-guarded so bash gracefully degrades to
		// no-proxy when the boot-time NewEgressProxy call failed.
		if al.sandboxEgressProxy != nil {
			deps.Proxy = al.sandboxEgressProxy
		}
		// Nil-guarded to avoid the typed-nil-in-interface trap: storing a nil
		// *policy.PolicyAuditor in an interface field would create a non-nil
		// interface holding a nil pointer, defeating downstream `!= nil` checks.
		if al.policyAuditor != nil {
			deps.PolicyAuditor = al.policyAuditor
		}

		restrict := cfg.Agents.Defaults.RestrictToWorkspace
		execTool, err := tools.NewExecToolWithDeps(agent.Home, restrict, cfg, deps, allowReadPaths)
		if err != nil {
			// Fail closed: if security wiring fails, remove the bash tool from the
			// registry entirely. The agent will lose bash capability but
			// cannot run commands without the security layer.
			logger.ErrorCF("agent", "Failed to wire bash tool deps; removing bash tool (fail closed)",
				map[string]any{"agent_id": agentID, "error": err.Error()})
			agent.Tools.Unregister("bash")
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
			portRange := cfg.Sandbox.DevServerPortRange
			webServeCfg := tools.WebServeDevConfig{
				Tier3Commands:   cfg.Sandbox.Tier3Commands,
				PortRange:       [2]int32{portRange[0], portRange[1]},
				MaxConcurrent:   cfg.Sandbox.MaxConcurrentDevServers,
				EgressAllowList: cfg.Sandbox.EgressAllowList,
				AuditFailClosed: resolveBoolWithDefault(cfg.Sandbox.PathGuardAuditFailClosed, cfg.Sandbox.AuditLog),
			}
			// preview-on-main-listener v5 (FR-005/FR-006): web_serve no longer
			// takes a constructor-frozen preview base URL. It gets al.GetConfig
			// (thread-safe, RLock-protected) so every serve_web call builds its
			// URL from the LIVE canonical gateway origin and reads
			// gateway.preview_enabled live — no restart, no re-wiring on hot
			// reload required for the toggle to take effect.
			webServeTool := tools.NewWebServeTool(
				ag.Home,
				ag.ID,
				al.GetConfig,
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

		// bash (ADR-036): the unified shell tool used to be wired here as
		// three separate tools (exec via wireExecToolDeps, workspace.shell /
		// workspace.shell_bg gated behind experimental.workspace_shell_enabled
		// right here). All three are now ONE tool, registered universally via
		// wireExecToolDeps alone (called again below, after this function
		// returns, and by WireTier13Deps once the egress proxy is available)
		// — governed exclusively by ToolPolicyCfg, no experimental flag.
	}

	logger.InfoCF("agent", "Tier 1/3 tools wired into agent registry", map[string]any{
		// preview-on-main-listener v5: no more boot-frozen preview_base_url —
		// web_serve now derives its URL live from al.GetConfig on every call.
		"served_subdirs_ready":      deps.ServedSubdirs != nil,
		"dev_server_registry_ready": deps.DevServerRegistry != nil,
		"egress_proxy_ready":        deps.EgressProxy != nil,
	})
}

// wireMemoryAuditLoggerOn wires auditLogger into every agent's tool registry
// in registry (SEC-15), including the direct RememberTool/RetrospectiveTool
// cast so memory tools can emit their own structured per-entry audit events
// (content_sha256 etc.). Factored out of NewAgentLoop's boot-time wiring so
// ReloadProviderAndConfig can re-apply the identical wiring against a
// freshly-built registry on hot reload — without this, NewAgentRegistry's
// brand-new RememberTool/RetrospectiveTool instances would silently lose
// audit logging the first time config reloads (e.g. onboarding completion,
// any agent PUT, token rotation — every TriggerReload call site).
func (al *AgentLoop) wireMemoryAuditLoggerOn(registry *AgentRegistry, auditLogger *audit.Logger) {
	if registry == nil || auditLogger == nil {
		return
	}
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
			if t, ok := agent.Tools.Get("run_retrospective"); ok {
				if rt, ok := t.(*tools.RetrospectiveTool); ok {
					rt.SetAuditLogger(auditLogger)
				} else {
					logger.WarnCF("agent",
						"'run_retrospective' tool is not a *tools.RetrospectiveTool; audit wiring skipped",
						map[string]any{
							"agent_id":  agentID,
							"tool_type": fmt.Sprintf("%T", t),
						})
				}
			}
		}
	}
}

// wireMemoryRateLimiterOn propagates the shared MemoryRateLimiter (v0.2 #155
// item 6) onto every agent's tool registry in registry. Factored out of
// NewAgentLoop's boot-time wiring so ReloadProviderAndConfig can re-apply the
// SAME limiter instance against a freshly-built registry on hot reload —
// see the al.memoryRateLimiter field comment for why the limiter itself must
// never be reconstructed here (that would reset every agent's sliding-window
// rate-limit buckets on any unrelated config change).
func (al *AgentLoop) wireMemoryRateLimiterOn(registry *AgentRegistry, limiter *tools.MemoryRateLimiter) {
	if registry == nil || limiter == nil {
		return
	}
	for _, agentID := range registry.ListAgentIDs() {
		if agentInst, ok := registry.GetAgent(agentID); ok {
			agentInst.Tools.SetMemoryRateLimiter(limiter)
		}
	}
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
			// The next turn resolves the active agent via sessionScopeKey(msg):
			//   webchat inbound carries a SessionID          → "session:"+SessionID
			//   channel inbound (whatsapp/telegram/…) has NO → "chat:"+channel+":"+chatID
			// Store the override under the SAME key(s) the inbound path will read.
			// The session-scoped key backs GetSessionActiveAgent + the in-turn
			// active-agent resolver; the chat-scoped key is what channel inbound
			// messages actually look up — without it a channel handoff is silently
			// dropped and routing falls back to ResolveRoute (the "agent stays" bug).
			var keys []string
			if evt.SessionID != "" {
				keys = append(keys, "session:"+evt.SessionID)
			}
			if evt.Channel != "" && evt.Channel != "webchat" && evt.ChatID != "" {
				keys = append(keys, "chat:"+evt.Channel+":"+evt.ChatID)
			}
			for _, k := range keys {
				if evt.AgentID == "" {
					al.sessionActiveAgent.Delete(k)
				} else {
					al.sessionActiveAgent.Store(k, evt.AgentID)
				}
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
			agent.Home,
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
			// Skill marketplaces are configured as a unified list (FR-10.1):
			// ClawHub + GitHub (+ future "omnipus") entries under
			// tools.skills.marketplaces. Credential refs are resolved via
			// os.Getenv (populated by credentials.InjectFromConfig, SEC-23).
			// GitHub entries get the agent workspace injected (the persisted
			// shape carries no workspace field).
			registryMgr := skills.NewRegistryManagerFromConfig(skills.RegistryConfig{
				Marketplaces: skills.MarketplacesFromConfig(
					cfg, os.Getenv, nil /* SSRF handled per-registry at the gateway */, agent.Home,
				),
				MaxConcurrentSearches: cfg.Tools.Skills.MaxConcurrentSearches,
			})

			searchCache := skills.NewSearchCache(
				cfg.Tools.Skills.SearchCache.MaxSize,
				time.Duration(cfg.Tools.Skills.SearchCache.TTLSeconds)*time.Second,
			)
			agent.Tools.Register(tools.NewFindSkillsTool(registryMgr, searchCache))
			// ADR-046 FR-009: install_skill targets the fixed, install-wide
			// GLOBAL skills directory ($OMNIPUS_HOME/skills) — the SAME
			// directory every agent's own SkillsLoader searches (see
			// globalSkillsDir's doc comment in context.go) — never
			// agent.Home, so a skill installed by one agent is discoverable
			// by every other agent.
			agent.Tools.Register(tools.NewInstallSkillTool(registryMgr, globalSkillsDir()))
		}

		// Email tools (M11) — registered ONLY for the agent that owns a configured,
		// enabled mailbox with a resolvable password. Email is a TOOL surface
		// (read_inbox · search_email · read_message · send_email · reply) over the
		// pure-Go IMAP/SMTP transport, not a conversational channel. The tools flow
		// through the normal per-agent tool policy, so god-mode / O7 policy applies.
		registerEmailToolsForAgent(cfg, agentID, agent)

		// `delegate` (ADR-036 merge of the former spawn / run_subagent /
		// check_spawn_status trio into one tool — docs/internal/specs/
		// agent-delegation-spec.md) is registered unconditionally — never
		// gated by a user-visible toggle. The legacy SubagentManager (and its
		// entirely-dead runTask/Spawn/SpawnSubTurnFunc closure path — nothing
		// in production ever called SubagentManager.Spawn, which is why
		// check_spawn_status always reported "no subagents have been spawned
		// yet" for anything spawn created) is retired: DelegateTool now owns
		// its own task-state store, written by its own async path and read by
		// action:"status" — a single, connected piece of state (FR-D2).
		{
			delegateTool := tools.NewDelegateTool(agent.Model, agent.MaxTokens, agent.Temperature)
			delegateTool.SetSpawner(NewSubTurnSpawner(al))
			// W2: action:"status" live-progress snapshot for a running native
			// task. sharedStore mirrors the exact store wiring
			// NewHandoffTool already uses just above (line ~1469) — the same
			// *session.UnifiedStore delegated children's transcript entries
			// are actually written to. It is a plain value captured once at
			// registration time (NOT a live func()), so it does not itself
			// reflect a later hot reload; SetAgentRegistry below is the one
			// that gets the live func() treatment, since al.GetRegistry() is
			// invoked fresh on every call.
			// Typed-nil guard: al.GetSessionStore() can return a nil
			// *session.UnifiedStore on a degraded boot (loop.go:609-620). Boxing
			// a nil concrete pointer into the DelegateSessionStore interface
			// yields a NON-nil interface, so SetSessionStore's own `== nil`
			// graceful-degrade guard would never fire and recentActivityLines
			// would panic on a nil receiver. Only wire the store when non-nil so
			// a running-native status snapshot degrades to prompt-only instead
			// of crashing the whole action:"status" call. (The sibling
			// NewHandoffTool/NewReturnToDefaultTool wiring above shares this
			// pre-existing latent pattern; tracked separately.)
			if sharedStore != nil {
				delegateTool.SetSessionStore(sharedStore)
			}
			delegateTool.SetAgentRegistry(func() tools.DelegateAgentRegistry { return al.GetRegistry() })
			currentAgentID := agentID
			// ADR-037: the legacy DelegationPolicy.To / SubagentsConfig.AllowAgents
			// allowlist checkers (SetAllowlistChecker / SetDelegateChecker) are
			// retired — config.AgentConfig.DelegationPolicy no longer exists, and
			// those checkers were only ever consulted as a fallback when the graph
			// checkers below were nil, which never happens in production wiring.
			// The per-workspace delegation graph (buildDelegationDenyChecker) is
			// now the ONLY delegation gate.
			//
			// FR-6.2: full-policy gate for the background (async=true, the
			// default) mode — trust set + mode("background") + depth.
			delegateTool.SetDelegationDenyCheckerBackground(
				// ForDelegate bakes in exempt=false: delegate(agent_id=self) spawns a
				// real sub-turn and MUST be graph-gated (and thus denied), never exempted.
				buildDelegationDenyCheckerForDelegate(
					currentAgentID,
					cfg.Agents.Defaults,
					config.DelegationModeBackground,
				),
			)
			// FR-6.2: full-policy gate for the await (async=false) mode. Uses
			// the same buildDelegationDenyChecker as the background gate
			// above but with DelegationModeAwait, so a targeted
			// delegate(agent_id="X", async=false) is checked against the
			// caller→X edge for the "await" mode, and an untargeted call
			// falls back to evalUntargetedDelegation.
			delegateTool.SetDelegationDenyCheckerAwait(
				// ForDelegate bakes in exempt=false: same reasoning as the background
				// gate — a self-targeted await delegate() is real delegation, graph-gated.
				buildDelegationDenyCheckerForDelegate(currentAgentID, cfg.Agents.Defaults, config.DelegationModeAwait),
			)
			// #477 / FR-D9-FR-D10: thread the SAME effective depth cap the
			// gates above just authorized against into spawnSubTurn's own
			// depth check — the resolver is mode-agnostic (sourced only from
			// the matched edge's own Depth, shared by both the background and
			// await gates) — so the spawn-time backstop does not
			// independently re-derive (and silently override) an explicit
			// per-edge Depth.
			delegateTool.SetDelegationDepthResolver(buildDelegationDepthResolver(
				currentAgentID, cfg.Agents.Defaults,
			))

			agent.Tools.Register(delegateTool)
		}

		// Task tools — require a task store (available after first NewAgentLoop call).
		if al.taskStore != nil {
			currentAgentID := agentID

			agent.Tools.Register(tools.NewTaskListTool(al.taskStore))

			taskCreate := tools.NewTaskCreateTool(al.taskStore)
			// Resolve the real default workspace (is_default ULID) when a
			// chat-delegated task has no workspace bound to the turn — never the
			// literal "default" (which would land it in an invisible workspace).
			taskCreate.SetHome(filepath.Dir(cfg.AgentHomeBasePath()))
			// ADR-037: the legacy boolean delegateCheck (SetDelegateChecker,
			// backed by config.ResolveDelegationTo) is retired — the field it
			// read no longer exists. The graph-based deny checker below is the
			// only gate now (it was already the sole gate in production).
			// FR-6.2: full-policy gate — trust set + mode("task") + depth.
			taskCreate.SetDelegationDenyChecker(
				// ForTaskReassignment bakes in exempt=true: assigning a NEW task to
				// oneself is not delegation (no new instance spawned), not graph-gated.
				buildDelegationDenyCheckerForTaskReassignment(
					currentAgentID,
					cfg.Agents.Defaults,
					config.DelegationModeTask,
				),
			)
			// Task-mode recursion bound: reject a task_create issued from within a
			// task run whose delegation generation already sits at the ceiling. The
			// per-agent depth gate cannot bound task mode on its own because every
			// task run starts a fresh turn at depth 0 (see processTaskDirect depth
			// seeding); this hard ceiling closes that gap.
			taskCreate.SetMaxDelegationDepth(maxTaskDepth)
			// subagent_3p (external-CLI) worker task assignment is no longer
			// guarded here: processTaskDirect (this file) now branches on
			// runner.ResolveDispatch and routes an external-CLI worker's task
			// run through runExternalCLISubTurn instead of the native engine —
			// see its doc comment for the dispatch design. The former
			// SetExternalCLIWorkerChecker rejection (mirrored on the REST path's
			// validateTaskAgentID) is retired now that the engine gap it
			// papered over is closed.
			taskCreate.SetOnCreate(func(entity *task.Task) {
				al.EmitTaskStatusChanged(TaskStatusChangedPayload{
					TaskID:    entity.ID,
					Status:    string(entity.Status),
					SessionID: "task:" + entity.ID,
					AgentID:   entity.AgentID,
				})
				// Register the task's time trigger (no-op for manual/heartbeat).
				al.NotifyTaskUpserted(entity)
			})
			agent.Tools.Register(taskCreate)

			taskUpdate := tools.NewTaskUpdateTool(al.taskStore)
			taskUpdate.SetOnComplete(func(t *task.Task) {
				if al.taskExecutor != nil {
					al.taskExecutor.onTaskComplete(t)
				}
				// A terminal update removes the task's trigger job UNLESS the
				// trigger repeats (recurring/every), whose series survives past a
				// per-run terminal status (OnTaskUpserted, pkg/agent/task_trigger.go);
				// a non-terminal update re-syncs it (a no-op if it is already
				// correctly armed for the current trigger content).
				al.NotifyTaskUpserted(t)
			})
			// ADR-037: legacy SetDelegateChecker retired here too — see the
			// taskCreate comment above.
			// FR-6.2: reassignment is re-delegation — gate agent_id changes through
			// the same trust-set + mode("task") + depth policy as task_create.
			taskUpdate.SetDelegationDenyChecker(
				// ForTaskReassignment bakes in exempt=true: reassigning a task to its
				// existing owner is a no-op reassignment, not delegation — not graph-gated.
				buildDelegationDenyCheckerForTaskReassignment(
					currentAgentID,
					cfg.Agents.Defaults,
					config.DelegationModeTask,
				),
			)
			// Same rationale as taskCreate above: the subagent_3p reassignment
			// guard is retired now that processTaskDirect dispatches an
			// external-CLI worker's task run through runExternalCLISubTurn.
			agent.Tools.Register(taskUpdate)

			setTodos := tools.NewSetTodosTool(al.taskStore)
			setTodos.SetHome(filepath.Dir(cfg.AgentHomeBasePath()))
			agent.Tools.Register(setTodos)
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
		// is determined by the policy engine. Chromium presence/download is now
		// preprovisioned in the background at gateway boot (BrowserManager.
		// Preprovision, kicked off from RunContextWithOptions right after
		// NewAgentLoop returns) so a fresh install's managed download starts
		// immediately instead of at an agent's first browser tool call; a first
		// tool call still resolves (and, in the rare case preprovisioning hasn't
		// finished yet, blocks on) the same resolution logic and produces a
		// clear error if a binary genuinely cannot be found/installed.
		// browser.evaluate is denied by default via its executeEnabled gate
		// (cfg.Sandbox.BrowserEvaluateEnabled); see pkg/tools/browser. (#438: the
		// pkg/policy.builtinToolPolicies entry is advisory — that path is test-only.)
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
				if cfg.Tools.Browser.ExecPath != "" {
					browserCfg.ExecPath = cfg.Tools.Browser.ExecPath
				}
				// ADR-052 D2/M1: PreferPackaged and TrustPathChrome are
				// ALWAYS copied (bool fields, no "unset vs explicit false"
				// distinction needed — the default-config zero value IS
				// the security-hardened default). Without these the runtime
				// resolver stays on its own defaults and the operator's
				// config flips have no effect (SPEC-002). Both are wired
				// every reload, not just at first-seed, so a Settings save
				// takes effect without a gateway restart.
				browserCfg.PreferPackaged = cfg.Tools.Browser.PreferPackaged
				browserCfg.TrustPathChrome = cfg.Tools.Browser.TrustPathChrome
				// Headless is intentionally NOT copied from
				// cfg.Tools.Browser.Headless here: browser.DefaultConfig()
				// always sets Headless=true, and a bare bool config field
				// can't distinguish "operator explicitly set false" from
				// "unset" — honoring a zero-value false would silently break
				// every display-less server deployment (the common case).
				// There is no supported way to run non-headless today.
				browserCfg.PersistSession = cfg.Tools.Browser.PersistSession

				// WebRTC build (ADR-047, wave-plan W2-A): seed the gateway-owned
				// tabCapture capture extension into $OMNIPUS_HOME/browser/
				// (config.OmnipusHomeDir()/browser — the helper, never an ad-hoc
				// join) and wire ExtensionDir/ExtensionID onto browserCfg so the
				// coordinator's launch flags (--allowlisted-extension-id,
				// --enable-unsafe-extension-debugging — exec_resolver.go) apply
				// and its post-launch auto-load (coordinator.go's launchChrome)
				// picks it up. Seed is atomic/idempotent (captureext.Seed) and
				// harmless even when WebRTC ends up gated off at request time
				// (WebRTCEnabled=false, lite build, or ClassifyVideoCapability
				// not_capable) — the extension simply never gets used. Best-effort:
				// a seed failure only means the WebRTC capture path degrades to
				// "not_capable"-equivalent (no ExtensionDir set, so LoadExtension
				// is never attempted) — it must never abort ordinary browser-tool
				// registration for this agent.
				if extDir, seedErr := captureext.Seed(
					filepath.Join(config.OmnipusHomeDir(), "browser"),
				); seedErr != nil {
					logger.WarnCF(
						"agent",
						"WebRTC capture extension seed failed — live-view WebRTC will report not_capable",
						map[string]any{"error": seedErr.Error()},
					)
				} else {
					browserCfg.ExtensionDir = extDir
					browserCfg.ExtensionID = captureext.ExtensionID
				}

				// preview-on-main-listener v5 (FR-018/US-10, S21): let the agent's
				// built-in browser reach the gateway's OWN preview origin.
				// serve_web mints http://localhost:<gateway.port>/preview/...
				// when gateway.public_url is unset (US-1 AS-2); CheckHost/CheckIP
				// are otherwise port-blind, so without a scoped exception the
				// gateway's own preview would either need a blanket loopback
				// allow (rejected by the ADR — opens every local dev port) or
				// stay blocked entirely. The exception scopes to exactly this
				// host:port pair — passing "localhost" (its documented expected
				// caller usage) also accepts the resolved "127.0.0.1"/"::1"
				// loopback forms for the SAME port, per r4 OBS-003.
				//
				// CRITICAL (code-review M2): the exception MUST live on a checker
				// dedicated to the browser tool, NOT on al.ssrfChecker. That
				// singleton is shared with provider base_url and skill-installer
				// URL validation (rest.go/rest_onboarding.go/gateway.go CheckURL
				// callers); mutating it in place would silently allow
				// localhost:<gateway.port> there too — the blanket-loopback
				// widening the ADR rejected. CloneWithGatewayOrigin returns an
				// independent checker sharing the singleton's block-lists/allowlist
				// but carrying its own exception. The SSRF-disabled branch already
				// mints a fresh per-agent checker, so it takes the exception directly.
				var browserSSRF *security.SSRFChecker
				if al.ssrfChecker != nil {
					browserSSRF = al.ssrfChecker.CloneWithGatewayOrigin("localhost", cfg.Gateway.Port)
				} else {
					browserSSRF = security.NewSSRFChecker(nil)
					browserSSRF.AllowGatewayOrigin("localhost", cfg.Gateway.Port)
				}

				// browser.evaluate registration: always register the tool so the
				// LLM sees it in its tool list. The live safety floor (deny by
				// default, SEC-04/SEC-06) is the tool's own executeEnabled gate —
				// BrowserEvaluateEnabled=true is the required explicit operator
				// opt-in for the tool to actually execute. (#438: the
				// pkg/policy.builtinToolPolicies entry is advisory; that path is
				// test-only, not a live dispatch gate.)
				evaluateEnabled := cfg.Sandbox.BrowserEvaluateEnabled
				// ADR-043: ensure the gateway-scoped shared-Chrome coordinator
				// exists (constructed once; reused across hot-reload so the
				// per-agent browser contexts it owns — and thus agents' login
				// state — survive a Settings save). An agent configured with an
				// explicit tools.browser.cdp_url bypasses the coordinator: its
				// ensureStarted takes the CDPURL branch first.
				//
				// MED-1: on a RELOAD (coordinator already exists), apply the
				// runtime-cheap config deltas. max_total_tabs is a live policy
				// (TryOpenTab reads it under c.mu) and takes effect immediately;
				// headless/exec_path/profile_dir are launch-time properties of
				// the already-running Chrome and cannot hot-apply —
				// ApplyRuntimeConfig warn-logs those so an operator isn't
				// silently misled. CRIT-002 stays intact: the coordinator is
				// never rebuilt on reload.
				al.mu.Lock()
				if al.browserCoordinator == nil {
					al.browserCoordinator = browser.NewBrowserCoordinator(
						al.homePath,
						browserCfg,
						cfg.Tools.Browser.MaxTotalTabs,
					)
				} else {
					al.browserCoordinator.ApplyRuntimeConfig(browserCfg, cfg.Tools.Browser.MaxTotalTabs)
				}
				// ADR-048 condition 1: thread tools.browser.capture_shared_context
				// through to the coordinator on every fresh-seed AND reload pass —
				// SetCaptureSharedContext (not NewBrowserCoordinator's constructor
				// args) so this stays a single call site regardless of which
				// branch above ran.
				al.browserCoordinator.SetCaptureSharedContext(cfg.Tools.Browser.CaptureSharedContext)
				coordinator := al.browserCoordinator
				al.mu.Unlock()
				// fs-workspace: browser tools (browser_screenshot) get agent.Home +
				// RestrictToWorkspace so screenshot paths resolve through the same
				// workspace root as the other file tools (FR-009).
				mgr, regErr := browser.RegisterTools(
					agent.Tools, browserCfg, browserSSRF, evaluateEnabled,
					agent.Home, cfg.Agents.Defaults.RestrictToWorkspace,
				)
				if regErr == nil {
					mgr.AttachSharedChrome(coordinator, agentID)
				}
				if regErr != nil {
					logger.ErrorCF("agent", "Failed to register browser tools — "+
						"ensure Chromium/Chrome is installed or set tools.browser.cdp_url",
						map[string]any{"error": regErr.Error()})
				} else {
					// ADR-038 D4/finding #2 + ADR-043: store per-agent, keyed by
					// agentID. registerSharedTools re-runs on every hot reload
					// (ReloadProviderAndConfig, any Settings save). When it
					// does, the PRIOR manager for this SAME agentID must be
					// torn down before installing the new one.
					//
					// W1/C2/F-INFO-3 (D4 invariant 3): in ADR-043 shared-Chrome
					// mode the teardown goes through the COORDINATOR's Release,
					// not a bare manager.Shutdown(). Release drops the old
					// manager's ref from the coordinator's bookkeeping (so
					// TotalOpenTabs stops counting its tabs) AND calls its
					// dropConnection (= Shutdown in coordinator mode = close
					// the WS connection + detach tabs) — WITHOUT killing Chrome
					// or disposing the agent's browser context (CRIT-002/C1:
					// the coordinator owns both; the context persists so the
					// next Register re-adopts it and login survives the save).
					// In the no-coordinator test/legacy path (coordinator==nil)
					// the old manager IS its own Chrome owner, so Shutdown() it
					// directly (the pre-ADR-043 behavior — only
					// BrowserManager.Shutdown, which cancels the chromedp
					// allocator context, kills the subprocess; dropping the Go
					// reference does nothing). Release is a full substitute for
					// the prior.Shutdown() reload call it replaces: the
					// registered manager is guaranteed to be the same object as
					// `prior` (Register is the only c.managers writer, and a
					// manager registers itself under its own agentID), so
					// Release's internal dropConnection reaches `prior` exactly.
					// An unregistered prior (no browser tool used since the last
					// reload) has started==false in coordinator mode, so there
					// is no local state to clean — Release's no-op for an absent
					// c.managers entry is correct.
					//
					// A viewer attached to the OLD manager's live view is not
					// silently orphaned: the connection teardown cancels every
					// session's chromedp context, which
					// LiveView.watchForUnexpectedDeath (pkg/tools/browser/live.go,
					// also finding #2) detects and reports to any attached
					// viewer as a browser_status(error) frame, so the SPA can
					// re-attach — which resolves the NEW manager via
					// BrowserManagerForAgent. Teardown for ALL entries still
					// also happens, unconditionally, in Close().
					//
					// BOTH calls below run whenever a coordinator exists,
					// rather than the coordinator branch being a substitute
					// for prior.Shutdown() as an earlier version of this fix
					// assumed: an agent configured with an explicit
					// tools.browser.cdp_url NEVER calls coordinator.Register
					// in the first place (ensureStarted's CDPURL branch
					// returns before ever consulting m.coordinator — see
					// AttachSharedChrome's doc comment), so `prior` in that
					// mode is absent from the coordinator's c.managers map
					// and coord.Release(agentID) is a silent no-op for it —
					// dropConnection never reaches it, Started() never flips
					// false, and its remote-allocator connection leaks on
					// every reload (caught by
					// TestRegisterSharedTools_HotReload_ShutsDownReplacedBrowserManager,
					// which pins CDPURL specifically to exercise this path).
					// prior.Shutdown() is safe to call unconditionally
					// alongside coord.Release: it is idempotent (Shutdown /
					// dropConnection share the same reset logic) and, per
					// Shutdown's own doc comment, a no-op on Chrome/context
					// in coordinator mode (m.allocCancel is the no-op stub
					// ensureStarted installs there) — so CRIT-002 (Chrome +
					// context survive a reload) is unaffected. coord.Release
					// still runs whenever coord != nil so the coordinator's
					// OWN bookkeeping (c.managers entry, tab-budget counts)
					// stays correct for managers that WERE actually
					// registered with it.
					al.mu.Lock()
					prior := al.browserMgrs[agentID]
					coord := al.browserCoordinator
					al.browserMgrs[agentID] = mgr
					al.mu.Unlock()
					if coord != nil {
						// CRIT-002 path: connection-only teardown, Chrome +
						// browser context survive for the new manager to
						// re-adopt.
						coord.Release(agentID)
					}
					if prior != nil {
						// Always Shutdown the replaced manager, even when a
						// coordinator exists: a remote-CDP manager (cdp_url
						// set) never attaches to the coordinator, so Release
						// above is a no-op for it and its allocator would
						// leak on every hot reload. Shutdown is idempotent
						// and connection-only for a coordinator-attached
						// manager (CRIT-002: it must NOT kill the shared
						// Chrome — TestManager_Shutdown_DropsConnectionNotProcess),
						// and kills the manager's own Chrome in the
						// no-coordinator legacy path.
						prior.Shutdown()
					}
				}
			}
		}

		// recall_conversation (FR-008, FR-013, FR-019): session-scoped archive
		// paging. The archive reader MUST be agent.Sessions — the same store
		// windowTrim evicts from and assembleMessages reads breadcrumbs from
		// (turn.go's ts.session = agent.Sessions) — NOT al.GetSessionStore()'s
		// shared store (rooted at $OMNIPUS_HOME/sessions/, used only for
		// session routing metadata). The two are different UnifiedStore
		// instances rooted at different directories for the same sessionKey;
		// registering against the shared store means ReadArchive always finds
		// an empty/unrelated file and recall_conversation can never reach the
		// turns the breadcrumb just told the model about. The span setter is
		// the AgentLoop itself (setRecallSpan / dropRecallSpan on
		// al.recallSpans). The routing session key is read from ctx at
		// Execute time (tools.ToolSessionKey) — no per-agent construction
		// needed beyond binding the correct archive reader here.
		// Excluded for the "main" gateway agent (no memory tools there either).
		if agentID != "main" {
			if agent.Sessions != nil {
				agent.Tools.Register(NewRecallConversationTool(agent.Sessions, al))
			} else {
				logger.WarnCF("agent",
					"recall_conversation not registered — agent.Sessions is nil",
					map[string]any{"agent_id": agentID})
			}
		}

		// Register the unified `load_tool` infra tool (search + load paths).
		// Replaces the former search_tools_bm25 + search_tools_regex + standalone load_tool trio.
		// The resolver uses context-aware closures so per-session and per-agent state
		// is read from the tool ctx at call time, avoiding data races on the shared
		// instance across concurrent turns on the same agent.
		//
		// ALWAYS registered unconditionally — regardless of cfg.Tools.Manifest.Compressed
		// or MCP discovery settings. Registration is cheap and harmless when unused.
		//
		// Why unconditional: the tools_on_demand PUT endpoint flips Compressed live via
		// SwapConfig without re-running agent registration. If load_tool was only registered
		// when Compressed=true at boot, a false→true live toggle would leave load_tool absent
		// from the registry, causing Get("load_tool") to return !ok in buildCompressedToolDefs
		// and ensureInfraToolsExecutable — every lazy tool silently unreachable, no error logged.
		// The "no restart needed" promise the UI makes becomes false.
		//
		// When Compressed is OFF at turn time, the per-turn gates (cfg.Tools.Manifest.Compressed
		// at lines ~5049, ~5115, ~5026) skip the compressed paths entirely: load_tool is never
		// sent to the model and never force-added to policyFiltered. For an agent whose tools
		// mostly resolve to deny it is also stripped by FilterToolsByPolicy in the uncompressed
		// path (not in allow-list), so no spurious callable appears. For an agent whose tools
		// mostly resolve to allow it may appear in the uncompressed defs, which is harmless
		// (the model has all tools anyway).
		//
		// Guard against double-registration in case the MCP init path already added it.
		{
			if _, alreadyTools := agent.Tools.Get("load_tool"); !alreadyTools {
				capturedAgentID := agentID

				ttl := cfg.Tools.MCP.Discovery.TTL
				if ttl <= 0 {
					ttl = 5
				}
				maxResults := cfg.Tools.MCP.Discovery.MaxSearchResults
				if maxResults <= 0 {
					maxResults = 5
				}

				toolsTool := tools.NewToolsTool(agent.Tools, ttl, maxResults)
				toolsTool.SetResolver(
					// canLoad returns (true, "") when name is a policy-allowed LAZY tool for
					// the calling agent. Full/infra tools are handled before this call (they
					// return a no-op success in execLoad). When denied, the returned reason
					// string is surfaced verbatim in the load_tool error message.
					func(ctx context.Context, name string) (bool, string) {
						callerID := tools.ToolAgentID(ctx)
						if callerID == "" {
							callerID = capturedAgentID
						}
						callerAgent, ok := al.registry.GetAgent(callerID)
						if !ok {
							return false, name + " — agent not found"
						}
						allAgentTools := callerAgent.Tools.GetAll()
						policyFiltered, _ := tools.FilterToolsByPolicy(
							allAgentTools,
							callerAgent.AgentType,
							callerAgent.LoadToolPolicy(),
						)
						// Tier gate: full/infra tools are already callable — they never
						// need to be loaded. Check policy FIRST so a denied full-tier tool
						// gets a clear "denied" signal rather than a false "already available"
						// (F4 fix). If policy allows a full-tier tool, return the sentinel
						// "already available — just call it directly" reason so execLoad can
						// treat it as a no-op success rather than a load.
						if tools.ToolManifestTier(name) != tools.ManifestLazy {
							for _, t := range policyFiltered {
								if t.Name() == name {
									// Policy-allowed full-tier: signal as "already available"
									// using the typed sentinel so execLoad can distinguish
									// this from a policy denial without substring matching.
									return false, tools.CanLoadAlreadyAvailablePrefix + " — just call it directly"
								}
							}
							// Policy-denied full-tier (or genuinely not found for this tier).
							return false, name + " — denied by this agent's policy"
						}
						for _, t := range policyFiltered {
							if t.Name() == name {
								return true, ""
							}
						}
						// Hidden tools (deferred MCP tools registered via RegisterHidden)
						// are NOT in GetAll() until promoted, so the visible check above
						// misses them. They ARE loadable: the load path promotes (un-hides)
						// them before fetching the schema. Resolve the hidden tool directly
						// and evaluate its policy so an allowed hidden MCP tool can be loaded
						// by search/auto-load. (Without this, search surfaces the MCP tool
						// but load rejects it as "unknown" — the chicken-and-egg the MCP UAT
						// caught: search uses the hidden corpus, canLoad used only GetAll.)
						if hiddenTool, hok := callerAgent.Tools.GetIncludingHidden(name); hok {
							hiddenAllowed, _ := tools.FilterToolsByPolicy(
								[]tools.Tool{hiddenTool},
								callerAgent.AgentType,
								callerAgent.LoadToolPolicy(),
							)
							if len(hiddenAllowed) > 0 {
								return true, ""
							}
							// Tool exists (visible or hidden) but policy denies it.
							return false, name + " — denied by this agent's policy"
						}
						// Tool is not in GetAll() and not hidden — check if it's in the full
						// registered set but policy-filtered out (i.e. registered but denied).
						for _, t := range allAgentTools {
							if t.Name() == name {
								return false, name + " — denied by this agent's policy"
							}
						}
						// Genuinely unknown: suggest the closest registered name so the model
						// can correct a hallucinated or transposed name (C4 fix).
						if suggestion := tools.FindClosestToolName(allAgentTools, name); suggestion != "" {
							return false, name + " — unknown tool (did you mean '" + suggestion + "'?)"
						}
						return false, name + " — unknown tool name"
					},
					// markLoaded: fetches schemas FIRST, marks only successfully resolved
					// names as loaded, and returns any names that could not be resolved in
					// the rejected slice. This ensures the model's loaded-set is always
					// consistent with what it can actually call: a name that canLoad
					// accepted but whose registry lookup or schema extraction fails is
					// reported as rejected and never marked loaded.
					func(ctx context.Context, names []string) (map[string]any, []string) {
						callerID := tools.ToolAgentID(ctx)
						if callerID == "" {
							callerID = capturedAgentID
						}
						callerAgent, ok := al.registry.GetAgent(callerID)
						if !ok {
							// No agent — reject everything so the caller can surface the error.
							rejected := make([]string, 0, len(names))
							for _, n := range names {
								rejected = append(rejected, n+" — agent not found at load time")
							}
							return map[string]any{}, rejected
						}

						// Fetch schemas first; separate names into resolved and rejected.
						schemas := make(map[string]any, len(names))
						loadedOK := make([]string, 0, len(names))
						var rejected []string
						for _, n := range names {
							t, tOK := callerAgent.Tools.Get(n)
							if !tOK {
								rejected = append(rejected, n+" — not registered for agent at load time")
								continue
							}
							schema := tools.ToolToSchema(t)
							fn, fnOK := schema["function"]
							if !fnOK {
								rejected = append(rejected, n+" — schema has no function key")
								continue
							}
							schemas[n] = fn
							loadedOK = append(loadedOK, n)
						}

						// Mark only the successfully resolved names as loaded.
						sessionID := manifestSessionID(
							tools.ToolTranscriptSessionID(ctx),
							tools.ToolSessionKey(ctx),
						)
						al.markToolsLoaded(sessionID, loadedOK)
						return schemas, rejected
					},
				)
				agent.Tools.Register(toolsTool)
			}
		}
	}

	// W4 (agent-removal doesn't dispose context): a REMOVED agent is skipped by
	// the per-agent loop above, so its BrowserManager stays in al.browserMgrs
	// and — worse — its coordinator-owned browser context (cookie/localStorage
	// partition) leaks forever in c.contexts. Diff the registered set against
	// the current config: any agentID still in al.browserMgrs but no longer in
	// the registry has been removed — dispose its context via
	// coordinator.RemoveAgent (which cancels the OWNING chromedp context so
	// chromedp runs Target.disposeBrowserContext, unlike reload-Release which
	// preserves it) and drop it from al.browserMgrs. Distinguishes a reload
	// (agent still present → Release preserves the context) from a removal
	// (agent gone → RemoveAgent frees the partition).
	registeredAgentIDs := registry.ListAgentIDs()
	stillPresent := make(map[string]bool, len(registeredAgentIDs))
	for _, id := range registeredAgentIDs {
		stillPresent[id] = true
	}
	al.mu.Lock()
	coord := al.browserCoordinator
	var removedAgentIDs []string
	for id := range al.browserMgrs {
		if !stillPresent[id] {
			removedAgentIDs = append(removedAgentIDs, id)
			delete(al.browserMgrs, id)
		}
	}
	al.mu.Unlock()
	for _, id := range removedAgentIDs {
		if coord != nil {
			coord.RemoveAgent(id)
		}
		logger.InfoCF("agent", "removed browser manager for deleted agent", map[string]any{
			"agent_id": id,
		})
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

// resolveEffectiveWorkspaceID resolves the workspace whose delegation graph
// governs the current turn. Every delegation check resolves to exactly one
// workspace:
//
//  1. the workspace bound to the turn (tools.ToolWorkspaceID), when present; else
//  2. the is_default workspace ("My Workspace"), resolved fresh from disk.
//
// It returns ("", denial) when NO workspace can be resolved at all — neither a
// bound workspace nor an is_default one exists (should not happen post-seed).
// This is a FAIL-CLOSED path: a delegation check with no governing graph DENIES
// rather than falling open. The returned denial carries a trust_set reason and
// the requested target (when one was named).
func resolveEffectiveWorkspaceID(ctx context.Context, targetAgentID string) (string, *tools.DelegationDenial) {
	wsID := tools.ToolWorkspaceID(ctx)
	if wsID == "" {
		// Default to My Workspace (the is_default workspace) so the delegation
		// graph is ALWAYS consulted — never an implicit allow.
		def, err := workspace.ResolveDefaultID(omnipusHome())
		if err != nil || def == "" {
			logger.WarnCF("agent", "delegation denied: no workspace to evaluate against", map[string]any{
				"target": targetAgentID, "error": errString(err),
			})
			return "", &tools.DelegationDenial{
				Reason: "delegation cannot be authorized: no workspace is bound to this turn " +
					"and no default workspace exists to consult its delegation graph",
				Policy:        tools.DenyTrustSet,
				TargetAgentID: targetAgentID,
			}
		}
		wsID = def
	}
	return wsID, nil
}

// findDelegationEdge loads the effective workspace's delegation graph and returns
// the edge authorizing caller→target, or a *DelegationDenial on any failure.
// FAIL-CLOSED: a graph load error, a missing/unreadable workspace, or the absence
// of a caller→target edge all DENY (trust_set). The graph is read per-call, so an
// edit to the workspace graph takes effect on the next turn with no agent rebuild.
//
// Returns (edge, nil) when an authorizing edge exists; (nil, denial) otherwise.
func findDelegationEdge(
	ctx context.Context,
	callerAgentID, targetAgentID string,
	mode config.DelegationMode,
) (*workspace.DelegationEdge, *tools.DelegationDenial) {
	wsID, denial := resolveEffectiveWorkspaceID(ctx, targetAgentID)
	if denial != nil {
		return nil, denial
	}

	edges, err := workspace.ReadDelegation(omnipusHome(), wsID)
	if err != nil {
		// FAIL-CLOSED: never fall open on a security check. An unreadable graph
		// is a closed graph.
		logger.WarnCF("agent", "delegation denied: workspace delegation graph unreadable", map[string]any{
			"agent_id": callerAgentID, "target": targetAgentID, "workspace_id": wsID,
			"mode": string(mode), "error": err.Error(),
		})
		return nil, &tools.DelegationDenial{
			Reason: fmt.Sprintf(
				"delegation cannot be authorized: workspace %q delegation graph is unreadable",
				wsID,
			),
			Policy:        tools.DenyTrustSet,
			TargetAgentID: targetAgentID,
		}
	}

	for i := range edges {
		if edges[i].FromAgent == callerAgentID && edges[i].ToAgent == targetAgentID {
			e := edges[i]
			return &e, nil
		}
	}

	logger.WarnCF("agent", "delegation denied: no edge in workspace graph", map[string]any{
		"agent_id": callerAgentID, "target": targetAgentID, "workspace_id": wsID, "mode": string(mode),
	})
	return nil, &tools.DelegationDenial{
		Reason: fmt.Sprintf(
			"agent %q is not allowed as a delegation target in this workspace",
			targetAgentID,
		),
		Policy:        tools.DenyTrustSet,
		TargetAgentID: targetAgentID,
	}
}

// EdgeModeCategory maps the delegate tool's real 3-value runtime parameter
// (config.DelegationMode: Await/Background/Task) down to the trust edge's
// collapsed 2-value vocabulary (workspace.DelegationMode: Direct/Task). Task
// maps 1:1 to workspace.ModeTask; both Await and Background — the sync-vs-async
// choice is a delegate-tool call parameter, not something the trust edge gates
// separately — map to workspace.ModeDirect. This is the single authority for
// that collapse at the enforcement gate; the inverse expansion (Direct back to
// both Await and Background for system-prompt advertising) lives in
// wireDelegationInjectors (pkg/agent/loop_env.go), and defaultWorkspaceDelegationEdges
// (pkg/gateway/rest_workspace_delegation.go) calls this function directly for
// the collapse-on-seed case — pkg/gateway already imports pkg/agent extensively
// (gateway.go, rest.go, rest_auth.go, ...), so there is no package-boundary
// reason to duplicate this logic there; exported (not unexported) specifically
// so that call site can reuse it instead of re-implementing the collapse.
//
// The switch below is deliberately exhaustive over the CLOSED 3-value
// config.DelegationMode enum (Await/Background/Task) rather than an if/else on
// the Task case with an implicit "else -> Direct" fallthrough: an if/else
// fallback is invisible to golangci's exhaustive linter (which only inspects
// real switch statements over enum-typed values) and would silently collapse
// any future 4th mode value into ModeDirect with no signal anywhere. The
// default case below keeps that same safe collapse (ModeDirect is the
// currently-correct behavior per the closed 3-value enum) but makes an
// unrecognized value NOISY via a warning log instead of silent, matching this
// file's existing convention of logging every denial/exceptional path.
func EdgeModeCategory(mode config.DelegationMode) workspace.DelegationMode {
	switch mode {
	case config.DelegationModeTask:
		return workspace.ModeTask
	case config.DelegationModeAwait, config.DelegationModeBackground:
		return workspace.ModeDirect
	default:
		logger.WarnCF("agent",
			"EdgeModeCategory: unrecognized config.DelegationMode value, defaulting to ModeDirect category",
			map[string]any{"mode": string(mode)},
		)
		return workspace.ModeDirect
	}
}

// enforceEdgeModeAndDepth applies the modes and depth constraints of a matched
// delegation edge. Returns nil when the delegation is permitted, or a
// *DelegationDenial (mode / depth) otherwise.
//
//   - modes: empty edge.Modes ⇒ all modes allowed; otherwise the current mode's
//     CATEGORY (via EdgeModeCategory — Await/Background both collapse to
//     Direct, Task stays Task) MUST be in edge.Modes. This is category
//     membership, not a raw string/cast comparison: edge.Modes uses the
//     collapsed 2-value workspace.DelegationMode vocabulary while mode is the
//     tool's real 3-value config.DelegationMode parameter, so the two are never
//     directly comparable.
//   - depth: edge.Depth (when non-nil) is the per-edge onward-delegation cap; nil
//     inherits — no per-edge cap. The global SubTurn.MaxDepth ceiling (passed as
//     globalDepthCap, 0 = none) ALWAYS applies as an additional, independent cap.
func enforceEdgeModeAndDepth(
	ctx context.Context,
	edge *workspace.DelegationEdge,
	callerAgentID, targetAgentID string,
	mode config.DelegationMode,
	globalDepthCap int,
) *tools.DelegationDenial {
	// Modes. Empty edge.Modes ⇒ all modes allowed (handled by the len > 0 guard).
	// Otherwise compare the edge's collapsed vocabulary against mode's category,
	// not mode itself.
	if len(edge.Modes) > 0 {
		category := EdgeModeCategory(mode)
		allowed := false
		for _, m := range edge.Modes {
			if m == category {
				allowed = true
				break
			}
		}
		if !allowed {
			logger.WarnCF("agent", "delegation denied: mode not permitted by edge", map[string]any{
				"agent_id": callerAgentID, "target": targetAgentID, "mode": string(mode),
				"edge_modes": edge.Modes,
			})
			return &tools.DelegationDenial{
				Reason: fmt.Sprintf(
					"delegation mode %q is not permitted for this delegation edge in this workspace",
					string(mode),
				),
				Policy:        tools.DenyMode,
				TargetAgentID: targetAgentID,
			}
		}
	}

	// Depth. This is the runtime half of the DEPTH INVARIANT documented once on
	// workspace.DelegationEdge (the single authority): depth <= 0 ⇒ this edge
	// grants NO onward delegation; depth > 0 ⇒ onward delegation is capped at that
	// chain depth.
	//
	// A per-edge cap of 0 means "no onward delegation" — the strictest possible
	// bound. A NEGATIVE cap is never a valid "uncapped" signal: an edge that
	// reached runtime with depth < 0 (e.g. one that bypassed write-time
	// validation) MUST fail closed, not silently remove the per-edge cap. So the
	// invariant is "depth <= 0 ⇒ this edge grants no further onward delegation":
	// reject unconditionally through this edge.
	if edge.Depth != nil && *edge.Depth <= 0 {
		logger.WarnCF("agent", "delegation denied: edge forbids onward delegation (depth <= 0)", map[string]any{
			"agent_id": callerAgentID, "target": targetAgentID, "mode": string(mode),
			"edge_depth": *edge.Depth,
		})
		return &tools.DelegationDenial{
			Reason: fmt.Sprintf(
				"this delegation edge forbids onward delegation (edge depth %d)",
				*edge.Depth,
			),
			Policy:        tools.DenyDepth,
			TargetAgentID: targetAgentID,
		}
	}

	// Otherwise enforce the effective depth cap: the tighter of the per-edge
	// cap (edge.Depth, nil = inherit) and the global SubTurn.MaxDepth ceiling,
	// falling back to the safety-backstop default when NEITHER source
	// expresses an explicit value. Resolved via resolveEffectiveDelegationDepth
	// — the SAME shared function spawnSubTurn's own depth check
	// (SubTurnConfig.ResolvedMaxDepth, threaded via buildDelegationDepthResolver)
	// and the delegation system-prompt builder (wireDelegationInjectors) use, so
	// this gate's decision and the eventual spawn-time enforcement are never
	// computed independently (#477, FR-D9/FR-D10).
	depthCap := resolveEffectiveDelegationDepth(edge.Depth, globalDepthCap)
	if d := currentDelegationDepth(ctx); d >= depthCap {
		logger.WarnCF("agent", "delegation denied: max delegation depth exceeded", map[string]any{
			"agent_id": callerAgentID, "target": targetAgentID, "mode": string(mode),
			"current_depth": d, "max_depth": depthCap,
		})
		return &tools.DelegationDenial{
			Reason: fmt.Sprintf(
				"maximum delegation depth (%d) reached — cannot delegate further",
				depthCap,
			),
			Policy:        tools.DenyDepth,
			TargetAgentID: targetAgentID,
		}
	}
	return nil
}

// errString returns err.Error() or "" for a nil error — a tiny helper for log
// fields where a nil error should produce no message.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// buildDelegationDenyChecker returns the per-workspace, graph-authoritative
// delegation gate for a targeted delegation tool (delegate with async=true =
// "background", create_task / update_task = "task"). The per-workspace
// delegation graph (workspaces/<id>.json → Delegation[] edges) is the SOLE
// runtime authority (ADR-037) — there is no separate per-agent delegation
// policy at all; it is never read here.
//
// It enforces, in order, returning the first violation (nil = allowed):
//
//  1. trust set — an edge caller→target MUST exist in the effective workspace's
//     delegation graph. No edge ⇒ DENY (trust_set). The workspace is the one
//     bound to the turn, defaulting to the is_default workspace when none is
//     bound — so a graph is ALWAYS consulted (never an implicit allow).
//  2. mode      — the tool's delegation mode's CATEGORY (via EdgeModeCategory)
//     must be in the edge's Modes (empty Modes = all allowed).
//  3. depth     — the current delegation-chain depth must be below the edge's
//     Depth cap (nil = inherit; 0 = no onward delegation). The global
//     SubTurn.MaxDepth ceiling always applies as an additional cap.
//
// FAIL-CLOSED: a graph load error, a missing workspace, or no default workspace
// all DENY — a delegation check with no readable governing graph never falls
// open.
//
// An empty targetAgentID means "no explicit target" (the LLM omitted agent_id);
// the trust check is then skipped here — untargeted spawns resolve to the default
// agent — while mode and depth (against any one of the caller's outgoing edges)
// still apply.
//
// selfAssignmentExempt controls the self-target (target == caller) case.
//
// DO NOT call this function directly at a wiring site. Use one of the two
// intent-named wrappers instead, so the exempt value can never be flipped
// wrong (a delegate-tool site with exempt=true silently reopens the
// self-delegation bypass — a security regression; a task-tool site with
// exempt=false merely denies a legitimate self-reassignment — loud and
// harmless, but still wrong):
//
//   - buildDelegationDenyCheckerForDelegate         (exempt=false) — the
//     `delegate` tool's background + await gates. delegate(agent_id=self) spawns
//     a real sub-turn instance, so it IS delegation and is graph-gated (and, for
//     self, ALWAYS denied — see below).
//   - buildDelegationDenyCheckerForTaskReassignment (exempt=true)  — the task
//     tools: create_task / update_task AND the cross-workspace
//     create_task_in_workspace / update_task_in_workspace via
//     NewSysagentDelegationDeny. Reassigning a task to the agent that already
//     owns it is NOT delegation (no new instance is spawned), so it is allowed
//     without consulting the graph.
//
// The exempt choice is a property of the CALLER (which tool wired this checker),
// NOT of `mode`. NEVER derive selfAssignmentExempt from `mode` — the two happen
// to correlate today (delegate uses background/await, tasks use task), but that
// is coincidence, not an invariant: a future delegate-in-task-mode would reopen
// the bypass if someone "simplified" by deriving the flag from the mode.
//
// When exempt is false, a self-target is DENIED directly here (defense-in-depth),
// without relying solely on workspace.DelegationEdge.Validate's self-edge
// prohibition as the guard, and with a distinct reason so the caught bypass
// attempt is distinguishable from a routine trust_set denial.
//
// defaults is only consulted for its SubTurn.MaxDepth global depth cap — there
// is no per-agent config.DelegationPolicy to read anymore (ADR-037); the
// per-workspace graph is the sole authority.
func buildDelegationDenyChecker(
	currentAgentID string,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
	selfAssignmentExempt bool,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	globalDepthCap := defaults.SubTurn.MaxDepth

	return func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
		if targetAgentID == currentAgentID {
			// Self-target. For the task tools (exempt=true) this is a no-op
			// reassignment to the task's existing owner, not delegation — allow.
			if selfAssignmentExempt {
				return nil
			}
			// For the delegate tool (exempt=false) self-delegation IS delegation
			// and is ALWAYS denied. Deny directly (defense-in-depth) instead of
			// falling through to findDelegationEdge and relying on the graph's
			// self-edge prohibition (DelegationEdge.Validate) as the sole guard. A
			// distinct reason + log distinguishes this caught self-delegation
			// bypass attempt from a routine "target not trusted" trust_set denial.
			logger.WarnCF("agent", "delegation denied: self-delegation is not permitted", map[string]any{
				"agent_id": currentAgentID, "target": targetAgentID, "mode": string(mode),
			})
			return &tools.DelegationDenial{
				Reason: fmt.Sprintf(
					"an agent cannot delegate to itself (%q): self-delegation is never permitted",
					currentAgentID,
				),
				Policy:        tools.DenyTrustSet,
				TargetAgentID: targetAgentID,
			}
		}

		if targetAgentID != "" {
			// Targeted delegation: require an authorizing edge, then enforce its
			// modes + depth.
			edge, denial := findDelegationEdge(ctx, currentAgentID, targetAgentID, mode)
			if denial != nil {
				return denial
			}
			return enforceEdgeModeAndDepth(ctx, edge, currentAgentID, targetAgentID, mode, globalDepthCap)
		}

		// Untargeted (agent_id omitted): trust is "can delegate at all" — the
		// caller must have at least one outgoing edge that permits this mode.
		// Mode + depth still apply against that edge.
		return evalUntargetedDelegation(ctx, currentAgentID, mode, globalDepthCap)
	}
}

// buildDelegationDenyCheckerForDelegate is the wiring-site constructor for the
// `delegate` tool's background and await gates. It bakes in selfAssignmentExempt=false:
// delegate(agent_id=self) spawns a real sub-turn instance, so it IS delegation and a
// self-target is ALWAYS denied. Use this — never the raw core with a literal false —
// so the security-critical exempt value can never be flipped wrong at a call site.
func buildDelegationDenyCheckerForDelegate(
	currentAgentID string,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	return buildDelegationDenyChecker(currentAgentID, defaults, mode, false)
}

// buildDelegationDenyCheckerForTaskReassignment is the wiring-site constructor for the
// task tools: create_task / update_task and the cross-workspace
// create_task_in_workspace / update_task_in_workspace (via NewSysagentDelegationDeny).
// It bakes in selfAssignmentExempt=true: reassigning a task to the agent that already
// owns it is NOT delegation (no new instance is spawned), so a self-target is allowed
// without consulting the graph. Non-self targets are still fully graph-gated.
func buildDelegationDenyCheckerForTaskReassignment(
	currentAgentID string,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	return buildDelegationDenyChecker(currentAgentID, defaults, mode, true)
}

// evalUntargetedDelegation gates an untargeted delegation (no explicit target)
// against the caller's outgoing edges in the effective workspace graph. It allows
// iff the caller has AT LEAST ONE outgoing edge whose modes permit the current
// mode (and whose depth cap is not exceeded). FAIL-CLOSED on graph load failure.
func evalUntargetedDelegation(
	ctx context.Context,
	callerAgentID string,
	mode config.DelegationMode,
	globalDepthCap int,
) *tools.DelegationDenial {
	wsID, denial := resolveEffectiveWorkspaceID(ctx, "")
	if denial != nil {
		return denial
	}
	edges, err := workspace.ReadDelegation(omnipusHome(), wsID)
	if err != nil {
		logger.WarnCF("agent", "delegation denied: workspace delegation graph unreadable", map[string]any{
			"agent_id": callerAgentID, "workspace_id": wsID, "mode": string(mode), "error": err.Error(),
		})
		return &tools.DelegationDenial{
			Reason: fmt.Sprintf(
				"delegation cannot be authorized: workspace %q delegation graph is unreadable",
				wsID,
			),
			Policy: tools.DenyTrustSet,
		}
	}

	// Find any outgoing edge that permits this mode and whose depth is OK.
	var firstModeDenial, firstDepthDenial *tools.DelegationDenial
	for i := range edges {
		if edges[i].FromAgent != callerAgentID {
			continue
		}
		e := edges[i]
		if d := enforceEdgeModeAndDepth(ctx, &e, callerAgentID, "", mode, globalDepthCap); d != nil {
			switch d.Policy {
			case tools.DenyMode:
				if firstModeDenial == nil {
					firstModeDenial = d
				}
			case tools.DenyDepth:
				if firstDepthDenial == nil {
					firstDepthDenial = d
				}
			}
			continue
		}
		return nil // an edge permits this delegation
	}

	// No edge permitted it. Surface the most specific reason: a mode/depth
	// denial if an edge existed but was constrained, else trust_set (no edge).
	if firstModeDenial != nil {
		return firstModeDenial
	}
	if firstDepthDenial != nil {
		return firstDepthDenial
	}
	logger.WarnCF("agent", "delegation denied: caller has no outgoing edge", map[string]any{
		"agent_id": callerAgentID, "workspace_id": wsID, "mode": string(mode),
	})
	return &tools.DelegationDenial{
		Reason: "this agent has no permitted delegation target in this workspace",
		Policy: tools.DenyTrustSet,
	}
}

// NewSysagentDelegationDeny returns a delegation-deny resolver suitable for the
// systools.Deps.DelegationDeny hook. The sysagent task tools are registered ONCE
// on a central registry (not per-agent), so they cannot bind a per-agent checker
// at construction the way the plain task tools do in NewAgentLoop. Instead this
// resolver builds the per-workspace, graph-authoritative task-mode delegation
// gate dynamically at Execute time and evaluates the requested target.
//
// This closes the §4 behavioral-parity gap: create_task_in_workspace /
// update_task_in_workspace must enforce the SAME delegation policy the plain
// create_task / update_task tools enforce. The cross-workspace surface is the
// PRIVILEGED Orchestrator path, so it must be at least as restrictive — never
// less — than the same-workspace path.
//
// The graph is the authority (workspaces/<id>.json → Delegation[] edges); the
// per-agent config is no longer consulted. A graph load failure or a missing
// workspace DENIES (fail-closed) inside buildDelegationDenyChecker.
func (al *AgentLoop) NewSysagentDelegationDeny() func(ctx context.Context, callerAgentID, targetAgentID string) *tools.DelegationDenial {
	return func(ctx context.Context, callerAgentID, targetAgentID string) *tools.DelegationDenial {
		// Self-assignment / untargeted is a no-op reassignment, not delegation, and
		// is allowed before touching the graph. This mirrors the exempt=true
		// self-target short-circuit inside buildDelegationDenyCheckerForTaskReassignment
		// (used below) and additionally covers the empty-target case (which the gate
		// would otherwise route through evalUntargetedDelegation). The cross-workspace
		// tools always supply a concrete target on a real reassignment.
		if targetAgentID == "" || targetAgentID == callerAgentID {
			return nil
		}
		var defaults config.AgentDefaults
		if cfg := al.GetConfig(); cfg != nil {
			defaults = cfg.Agents.Defaults
		}
		// ForTaskReassignment (exempt=true): these are the cross-workspace TASK tools
		// (create_task_in_workspace / update_task_in_workspace) — a self-target is a
		// no-op task reassignment, not delegation (also short-circuited above).
		gate := buildDelegationDenyCheckerForTaskReassignment(callerAgentID, defaults, config.DelegationModeTask)
		return gate(ctx, targetAgentID)
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
			//
			// FIX 5d follow-up — tracked as elicify-ai/omnipus#505 (filed,
			// not fixed in this pass): before FIX 5d, an AsyncNotifier-
			// originated system message was inert here w.r.t. the origin
			// session (no TranscriptSessionID/TranscriptStore bound), so
			// this lack of per-session serialization was harmless. FIX 5d
			// now threads AsyncOriginAgentID/AsyncTranscriptSessionID
			// through processSystemMessage, so this goroutine CAN run a real
			// turn concurrently against the SAME origin session as a live
			// user turn (unlike every other inbound message, which IS
			// serialized per session via the sessionWorker pool below).
			// File-level writes stay safe (UnifiedStore's mutex +
			// WriteFileAtomic), so this is not a NEW corruption risk on its
			// own, but the single-writer-per-session invariant other turn
			// types rely on no longer holds for this specific path. See
			// #505 for the suggested follow-up (route through sessionWorker,
			// or prove file-level locking is sufficient and close it).
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

					var response string
					var ag *AgentInstance
					published := false

					// Outer recover — preserved from before this fix. This is the
					// only supervising recover for this bare dispatcher goroutine
					// (unlike session_worker.go's runLoop→processTurn split, there
					// is no outer layer above it), so it must keep swallowing the
					// panic (log-and-exit-goroutine) rather than re-panicking again
					// — doing so would crash the whole gateway process, not just
					// this one message.
					defer func() {
						if r := recover(); r != nil {
							logger.ErrorCF("agent", "Panic in unroutable-message goroutine",
								map[string]any{
									"panic":   r,
									"channel": msg.Channel,
									"chat_id": msg.ChatID,
									"stack":   string(debug.Stack()),
								})
						}
					}()

					// C8 (chat-stream-hang): guarantee a terminal frame on EVERY
					// exit path, including panic-recover — mirrors the per-session
					// pattern in session_worker.go's processTurn defer. processMessage
					// can panic (e.g. a provider/tool nil-deref that escapes the inner
					// recovers); when it does, response is still "" and nothing is
					// ever published, leaving the SPA stuck "thinking" forever for
					// unroutable-path messages too.
					//
					// Registered AFTER the outer recover above so that — defers
					// being LIFO — THIS recover runs FIRST during unwinding: it
					// synthesizes an error response and force-publishes it via a
					// fresh bounded-timeout context (runCtx may be canceled during
					// panic unwinding), then re-panics so the outer recover above
					// still logs the "Panic in unroutable-message goroutine" event
					// exactly as before this fix.
					defer func() {
						if r := recover(); r != nil {
							// Log the ORIGINAL panic (with stack) BEFORE attempting the
							// force-publish below. If publishResponseIfNeeded (or anything
							// else in this block) itself panics, that new panic — not this
							// log call — is what would otherwise reach the outer recover's
							// log line, silently discarding the true root cause (the real
							// panic from processMessage) behind a confusing secondary
							// symptom. Logging first guarantees the root cause is always
							// on record, no matter what happens next.
							logger.ErrorCF("agent", "Panic in processMessage — emitting terminal error frame",
								map[string]any{
									"panic":   r,
									"channel": msg.Channel,
									"chat_id": msg.ChatID,
									"stack":   string(debug.Stack()),
								})
							if response == "" {
								response = "Error processing message: the agent turn failed unexpectedly. Please try again."
							}
							if !published {
								// Isolate the force-publish in its own recover so a panic
								// here (e.g. a bug in al.bus / publishResponseIfNeeded)
								// cannot prevent the panic(r) re-throw below — the outer
								// recover must always see the ORIGINAL panic value r, never
								// a secondary symptom from this publish attempt.
								func() {
									defer func() {
										if pr := recover(); pr != nil {
											logger.ErrorCF(
												"agent",
												"Panic while force-publishing terminal frame for unroutable-message panic",
												map[string]any{
													"panic":   pr,
													"channel": msg.Channel,
													"chat_id": msg.ChatID,
												},
											)
										}
									}()
									termCtx, termCancel := context.WithTimeout(context.Background(), 5*time.Second)
									defer termCancel()
									al.publishResponseIfNeeded(termCtx, ag, msg.Channel, msg.ChatID, response)
								}()
								published = true
							}
							panic(r)
						}
					}()

					var err error
					response, ag, err = al.processMessage(runCtx, msg)
					if err != nil && response == "" {
						// ADR-051 §RD5: never surface raw err text in the assistant-facing
						// reply. Route through the classifier so provider-originated body /
						// status / model identity is replaced with the typed copy.
						// The raw err stays in the defer's log line for operator triage.
						response = TranslateLLMError(nil, err.Error()).Message
					}
					if response != "" {
						al.publishResponseIfNeeded(runCtx, ag, msg.Channel, msg.ChatID, response)
						published = true
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
		if tool, ok := ag.Tools.Get("send_message"); ok {
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
		SessionKey:  resolveScopeKey(route, msg.SessionKey),
		Channel:     msg.Channel,
		ChatID:      msg.ChatID,
		WorkspaceID: al.resolveWorkspaceIDForContinuation(msg),
	}, nil
}

// resolveWorkspaceIDForContinuation resolves the workspace a steering
// continuation (Continue/continueWithSteeringMessages) should run inside
// (FIX 1 re-review). continueWithSteeringMessages previously left
// processOptions.WorkspaceID unset entirely, so a steering-continued turn's
// tool media silently degraded to the private/global room exactly like the
// other four gap sites this fix pass covers.
//
// buildContinuationTarget is called AFTER the triggering turn's own
// processMessage call has already returned (session_worker.go's runLoop) —
// msg here is session_worker's own copy of the inbound message, not the one
// processMessage mutated internally (Go passes bus.InboundMessage by value),
// so msg.SessionID reflects only what was already on the message BEFORE that
// turn ran. Two mechanisms cover this, both lifted directly from
// processMessage's own resolution (loop.go's "M4" comment, ~line 5350) rather
// than invented fresh:
//
//  1. msg.SessionID already set (always true for webchat — the gateway
//     websocket handler stamps it on the message before publish, so no
//     mutation-visibility gap applies) — resolve the session's own meta,
//     the authoritative source once a session exists.
//  2. msg.SessionID empty (the common case for a channel message that had
//     no session yet when session_worker dispatched it — processMessage
//     lazily creates one internally via resolveOrCreateChannelSession, but
//     that mutation is invisible here) — fall back to the bound channel
//     instance's own configured WorkspaceID, the exact same value
//     resolveOrCreateChannelSession itself would have seeded the new
//     session's meta with, so this independently recomputes the identical
//     answer rather than guessing.
//
// Falls back to the inbound metadata key last, matching processMessage's own
// final fallback. Returns "" (never guessed) when none of the above apply —
// e.g. an unbound channel with no session, the "system" and unrouted-message
// cases buildContinuationTarget already short-circuits above.
func (al *AgentLoop) resolveWorkspaceIDForContinuation(msg bus.InboundMessage) string {
	if msg.SessionID != "" {
		if store := al.ResolveSessionStore(msg.SessionID); store != nil {
			if meta, mErr := store.GetMeta(msg.SessionID); mErr == nil && meta != nil && meta.WorkspaceID != "" {
				return meta.WorkspaceID
			}
		}
	}
	if instanceID := inboundInstanceID(msg); instanceID != "" {
		if cfg := al.GetConfig(); cfg != nil {
			if inst, ok := cfg.Channels[instanceID]; ok && inst.WorkspaceID != "" {
				return inst.WorkspaceID
			}
		}
	}
	return inboundMetadata(msg, "workspace_id")
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
	// Bound the drain so a wedged recap goroutine (e.g. a mock or real LLM that
	// never returns) can NEVER hang teardown forever. Close() MUST be bounded:
	// an unbounded recapWG.Wait() here caused gateway tests whose t.Cleanup runs
	// al.Close() to block indefinitely, stalling every t.Parallel() peer and
	// tripping the 10-min package timeout. After the budget we proceed with
	// teardown regardless and log a warning; the worst case is a recap summary
	// that didn't finish writing, which is strictly better than a frozen process.
	al.waitRecapDrain(30 * time.Second)

	// Cancel all active session workers and wait for them to drain (5 s budget).
	// stopSessionWorkers is idempotent — safe to call here even if Run() has
	// already called it on context-cancellation, because workers cancel their
	// own context; a double-cancel is a no-op.
	al.stopSessionWorkers()

	// Drop every agent's browser-manager connection (ADR-038 D4). In ADR-043
	// shared-Chrome mode this closes each manager's WS connection + detaches
	// its tabs but does NOT kill the Chrome process — that is the coordinator's
	// job, done by coordinator.Shutdown() immediately below (the SOLE process-
	// kill path, MIN-008/FR-008). In the no-coordinator test/legacy path each
	// manager IS its own Chrome owner, so manager.Shutdown() kills its Chrome.
	al.mu.Lock()
	for agentID, mgr := range al.browserMgrs {
		mgr.Shutdown()
		delete(al.browserMgrs, agentID)
	}
	al.mu.Unlock()

	// ADR-043: the coordinator owns the ONE shared Chrome process. Per-manager
	// Shutdown() above only dropped each agent's connection (the manager no
	// longer cancels an ExecAllocator in coordinator mode). This is the SOLE
	// process-kill path — disposes every agent's browser context + kills Chrome
	// (MIN-008 / FR-008: Close() is the only kill).
	if al.browserCoordinator != nil {
		al.browserCoordinator.Shutdown()
		al.browserCoordinator = nil
	}

	// Shutdown reaper: kill every still-running background bash/exec session
	// (run_in_background=true) process-wide. These children have their
	// Pdeathsig deliberately CLEARED at spawn time (they must outlive a
	// crashed gateway, not be torn down by one — see
	// pkg/sandbox/spawn_bg_pdeath_linux.go), so nothing else reaps them on
	// process exit. Without this, a full gateway restart orphans every
	// still-running background child to PID 1 forever. This complements (but
	// is distinct from) RequestCancel's owner-scoped KillAllForSession
	// cascade — that fires per-session on an explicit user cancel; this fires
	// unconditionally, process-wide, on whole-process teardown.
	//
	// LOAD-BEARING PRECONDITION: tools.GetSharedSessionManager() returns a
	// PROCESS-WIDE singleton (a package-level var in pkg/tools) — it is NOT
	// scoped to this *AgentLoop. This reaper is only correct under the
	// assumption that a process hosts AT MOST ONE live *AgentLoop at a time,
	// which holds for the omnipus gateway/CLI binary (every real entry
	// point constructs exactly one). It does NOT hold inside this package's
	// own test suite, where many tests each construct their own *AgentLoop
	// and Close() it independently (often in parallel) — every one of those
	// Close() calls reaps the SAME shared manager. This is harmless (a
	// session already killed by another test's Close() is silently skipped
	// — see KillAll's two-phase locking) but means "killed" here can never
	// be read as "sessions THIS AgentLoop's own agents started" in a test
	// context; only in the single-AgentLoop production process is that
	// reading correct.
	//
	// Panic-guarded (mirrors cancel.go's PHASE B/C timer recover pattern):
	// the shared SessionManager is reached via a package boundary this
	// method does not otherwise control, so a panic inside it must not skip
	// the teardown steps that follow (MCP manager close, agent memory
	// stores, registry close, hooks, event bus, exec proxy, idle tickers,
	// orphan watches) — a torn-down AgentLoop that leaked those would be
	// worse than a background-kill step that failed loudly and moved on.
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("agent", "Close: panic recovered while killing background sessions",
					map[string]any{"panic": fmt.Sprintf("%v", r), "stack": string(debug.Stack())})
			}
		}()
		if killed, failed := tools.GetSharedSessionManager().KillAll(); killed > 0 || failed > 0 {
			logger.InfoCF("agent", "Close: killed background sessions on shutdown",
				map[string]any{"killed": killed, "failed": failed})
		}
	}()

	// Mark the runtime closed and take the manager as one step under initMu
	// so no ReconcileMCP pass — in flight or launched after this point — can
	// observe a manager that is mid-teardown: ReconcileMCP checks isClosed()
	// immediately after acquiring initMu and returns without touching
	// anything once it is set.
	al.mcp.initMu.Lock()
	al.mcp.setClosed()
	mcpManager := al.mcp.takeManager()
	al.mcp.initMu.Unlock()

	if mcpManager != nil {
		if err := mcpManager.Close(); err != nil {
			logger.ErrorCF("agent", "Failed to close MCP manager",
				map[string]any{
					"error": err.Error(),
				})
		}
	}

	// Close each agent's MemoryStore so the per-room bleve/scorch background
	// goroutines (introducerLoop / mergerLoop) actually exit. AgentInstance.Close
	// only tears down the session store, not the ContextBuilder's MemoryStore, so
	// without this walk those scorch goroutines leak for the life of the process —
	// in tests they stay alive 9–10 min and show up in the goroutine dump. Done
	// BEFORE registry.Close() clears the agent map. Idempotent: MemoryStore.Close
	// is safe to call more than once.
	al.closeAgentMemoryStores()

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

	// ADR-045: stop every pending orphan-foreground-turn watchdog timer so
	// none of them fire against a torn-down AgentLoop after Close() returns
	// (tests in particular construct/close many AgentLoops in quick
	// succession; a leaked timer firing later would touch a stale al).
	al.orphanWatches.Range(func(k, v any) bool {
		if ow, ok := v.(*orphanWatch); ok {
			ow.cancel()
		}
		al.orphanWatches.Delete(k)
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

// waitRecapDrain blocks until all in-flight recap goroutines tracked by recapWG
// have completed, OR until budget elapses — whichever comes first. It never blocks
// indefinitely: a recap that is wedged (a mock/real LLM that never returns) would
// otherwise hang Close() forever. On timeout it logs a warning and returns so the
// rest of teardown can proceed; the only cost is a recap summary that may not have
// finished writing.
func (al *AgentLoop) waitRecapDrain(budget time.Duration) {
	done := make(chan struct{})
	go func() {
		al.recapWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		// All recaps drained cleanly.
	case <-time.After(budget):
		logger.WarnCF("agent", "Close: recap drain budget exceeded; proceeding with teardown",
			map[string]any{"budget": budget.String()})
	}
}

// closeAgentMemoryStores walks the registry and closes every agent's MemoryStore
// so the per-room bleve/scorch background goroutines (introducerLoop, mergerLoop)
// exit. AgentInstance.Close() only tears down the session store; the MemoryStore
// held by the ContextBuilder is otherwise never closed, leaking those goroutines.
// MemoryStore.Close is idempotent and safe to call here even if already closed.
func (al *AgentLoop) closeAgentMemoryStores() {
	reg := al.GetRegistry()
	if reg == nil {
		return
	}
	for _, id := range reg.ListAgentIDs() {
		inst, ok := reg.GetAgent(id)
		if !ok || inst == nil || inst.ContextBuilder == nil {
			continue
		}
		if ms := inst.ContextBuilder.Memory(); ms != nil {
			ms.Close()
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
			User:      ts.auditUser(), // FR-017
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

// EmitTaskStatusChanged publishes a workflow task status transition onto the
// event bus so every connected SPA WebSocket client receives a
// task_status_changed frame (the SPA invalidates its tasks query cache on
// receipt). Safe to call from any goroutine — the bus drops to a full
// subscriber rather than blocking.
func (al *AgentLoop) EmitTaskStatusChanged(p TaskStatusChangedPayload) {
	al.emitEvent(EventKindTaskStatusChanged, EventMeta{AgentID: p.AgentID, Source: "task_executor"}, p)
}

// EmitTaskRunStatus publishes a per-execution TaskRun open/close transition
// (ADR-050 §3.8) onto the event bus so every connected SPA WebSocket client
// receives a task_run_status frame — additive alongside EmitTaskStatusChanged
// (see EventKindTaskRunStatus's own doc comment for why a separate event is
// needed: a recurring occurrence's run transitions do not move Task.status
// between distinct values). Safe to call from any goroutine — the bus drops
// to a full subscriber rather than blocking.
func (al *AgentLoop) EmitTaskRunStatus(p TaskRunStatusPayload) {
	al.emitEvent(EventKindTaskRunStatus, EventMeta{Source: "task_executor"}, p)
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
	// FIX 3: compute the classifier code once and thread it onto the live
	// ErrorPayload so the WS forwarder (FIX 2) does not have to re-translate
	// this curated message from scratch — mirroring appendErrorTranscript's
	// own llm.Code computation for the SAME message below. providerErr is
	// nil (hook aborts are never provider-originated); the classifier falls
	// back to substring matching on err.Error().
	llm := TranslateLLMError(nil, err.Error())
	al.emitEvent(
		EventKindError,
		ts.eventMeta("hooks", "turn.error"),
		ErrorPayload{
			Stage: "hook." + stage, ChatID: ts.opts.ChatID,
			Code: string(llm.Code), Message: err.Error(),
		},
	)
	// US-1: persist the hook abort to the JSONL transcript so the
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
		fields["degraded"] = payload.Degraded
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

	// Re-wire the audit logger onto remember/run_retrospective tools on the
	// new registry (SEC-15). Without this, hot reload would silently drop
	// memory-tool audit logging: NewAgentRegistry above built brand-new
	// RememberTool/RetrospectiveTool instances that never learned about
	// al.auditLogger. Mirrors the boot-time guard (cfg.Sandbox.AuditLog) —
	// al.auditLogger is only non-nil when audit logging was actually enabled.
	if al.auditLogger != nil {
		al.wireMemoryAuditLoggerOn(registry, al.auditLogger)
	}

	// Re-wire the shared memory-write rate limiter (v0.2 #155 item 6) onto
	// the new registry, re-applying the SAME instance built once in
	// NewAgentLoop — never a freshly constructed one, so per-agent/per-caller
	// sliding-window buckets survive config reloads. al.memoryRateLimiter is
	// unconditionally constructed in NewAgentLoop today, so this is always
	// non-nil in practice; the guard is defensive symmetry with the other
	// re-wiring steps above in case that ever becomes conditional.
	if al.memoryRateLimiter != nil {
		al.wireMemoryRateLimiterOn(registry, al.memoryRateLimiter)
	}

	// Re-wire per-turn delegation injectors on the new registry so that the
	// updated per-workspace delegation graph (read fresh per call) is
	// reflected on every agent's next turn without a static-prompt cache bust.
	wireDelegationInjectors(al, registry)

	// Re-wire per-turn working-directory injectors on the new registry, same
	// reasoning: a workspace's core_team can change via hot-reload too.
	wireWorkingDirInjectors(al, registry)

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

	// Reconcile live MCP connections against the just-swapped config, and —
	// unconditionally — re-register every already-connected server's tools
	// onto the brand-new registry above (NewAgentRegistry gives every agent a
	// fresh, empty Tools registry, which would otherwise silently drop MCP
	// tools on every hot reload / settings save). Must run after al.mu.Unlock
	// (ReconcileMCP takes al.mcp.initMu and reads al.cfg under its own
	// al.mu.RLock snapshot, and al.registry via GetRegistry — both would
	// deadlock if called while al.mu is still held here) and must not fail
	// the reload — a broken MCP server config shouldn't block a
	// provider/config swap that otherwise already succeeded. A
	// canceled/expired ctx means the pass never actually ran (reconcileLocked
	// checks ctx.Err() before touching anything) rather than having hit a
	// real per-server or systemic failure, so — unlike other errors, which
	// are just logged — that specific case also clears the initialized latch:
	// otherwise, if an earlier pass had ever succeeded, ensureMCPInitialized's
	// fast path would keep taking the "already done" shortcut forever and the
	// next turn's MCP state would never actually be reconciled. Other errors
	// are surfaced via MCPServerStatus / a later successful pass instead.
	if err := al.ReconcileMCP(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			al.mcp.clearInitialized()
		}
		logger.WarnCF("agent", "MCP reconciliation failed after config reload",
			map[string]any{"error": err.Error()})
	}

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

// BrowserManagerForAgent returns agentID's BrowserManager (ADR-038 D4),
// thread-safe. Returns (nil, false) when the agent has no browser manager —
// either browser tools failed to register for it (see the ErrorCF log in
// registerSharedTools) or agentID is unknown. The gateway's live-view WS
// handler (pkg/gateway/browser_ws.go) is the primary caller: it resolves the
// manager for the attached agent, then calls .Session(sessionID) on it to
// reach the same chromedp context that agent's browser_* tools drive.
func (al *AgentLoop) BrowserManagerForAgent(agentID string) (*browser.BrowserManager, bool) {
	al.mu.RLock()
	defer al.mu.RUnlock()
	mgr, ok := al.browserMgrs[agentID]
	return mgr, ok
}

// BrowserManagers returns a defensive-copy snapshot of every BrowserManager
// currently registered — one per agent that got browser tools during
// registerSharedTools' browser.RegisterTools call. Used by gateway boot
// (RunContextWithOptions) to kick off BrowserManager.Preprovision for each
// manager in the background right after NewAgentLoop returns, so a fresh
// install's managed Chromium download starts at boot instead of at an
// agent's first browser tool call. Thread-safe; the returned slice is safe
// to range over after this call returns even if browserMgrs is later
// mutated (e.g. by a hot reload's Shutdown()+replace in registerSharedTools).
func (al *AgentLoop) BrowserManagers() []*browser.BrowserManager {
	al.mu.RLock()
	defer al.mu.RUnlock()
	out := make([]*browser.BrowserManager, 0, len(al.browserMgrs))
	for _, mgr := range al.browserMgrs {
		out = append(out, mgr)
	}
	return out
}

// ResolveApprovalToolPolicy returns the effective tool policy ("allow"/"ask"/
// "deny") the WS approval hook consults for one (agentID, toolName) at exec time.
//
// Unification (#438): this routes the gateway exec gate through the SAME
// authoritative primitive (tools.EffectiveToolPolicy) AND the SAME live policy
// snapshot (the agent instance's LoadToolPolicy) that the agent loop's
// FilterToolsByPolicy uses at defs-assembly time, so the two can never diverge.
// It encapsulates, in order: (1) infra force-allow (load_tool → allow,
// unconditional), (2) the scope gate (fail-closed for unknown scopes), and
// (3) global×agent strictest-wins (deny > ask > allow, god-mode, wildcards).
//
// BEHAVIOR CHANGE (intentional): this ALIGNS the exec gate to the agent loop's
// wildcard-aware verdict. The OLD gateway resolver matched policy keys by
// exact-name only (it ignored ".*"/"_*" wildcard keys on both the global floor
// and the agent policy); routing through tools.EffectiveToolPolicy now honors
// those wildcards exactly as FilterToolsByPolicy always did. It only narrows or
// matches the loop's verdict — it never widens past it.
//
// Inputs are sourced from the registry when the agent is known (the exact
// snapshot the loop uses); when the agent is not in the registry it falls back
// to building a ToolPolicyCfg from the global config (sandbox floor + the agent's
// builtin policy from cfg.Agents.List) so the gate still enforces correctly
// pre-registration. The tool's scope is resolved from the agent's registry when
// the tool is registered; otherwise ScopeGeneral is assumed (a tool that reached
// the exec gate was already surfaced to the model, so it is not an unknown-scope
// tool — ScopeGeneral imposes no extra restriction beyond the policy merge).
func (al *AgentLoop) ResolveApprovalToolPolicy(agentID, toolName string) string {
	// Infra fast-path: force-allow regardless of agent/config resolution so the
	// gate behaves correctly even before the registry/config are wired.
	if tools.ToolManifestTier(toolName) == tools.ManifestInfra {
		return "allow"
	}

	// Preferred path: resolve through the agent instance's LIVE policy snapshot
	// (LoadToolPolicy — the same *ToolPolicyCfg, including any GodMode flag, that
	// FilterToolsByPolicy receives) and the tool's real scope, so this verdict
	// equals the loop's defs-filter verdict for this tool.
	if al.registry != nil {
		if inst, ok := al.registry.GetAgent(agentID); ok && inst != nil {
			scope := tools.ScopeGeneral
			if inst.Tools != nil {
				if t, found := inst.Tools.Get(toolName); found {
					scope = t.Scope()
				}
			}
			return tools.EffectiveToolPolicy(inst.LoadToolPolicy(), scope, inst.AgentType, toolName)
		}
	}

	// Fallback: build the policy inputs from the global config. Used when the
	// agent is not (yet) in the registry. This path is wildcard-aware (it routes
	// through tools.EffectiveToolPolicy) but is intentionally NOT god-mode-aware
	// (no GodMode flag set on polCfg) and assumes ScopeGeneral (we cannot resolve
	// the tool's real scope without the registry). Both omissions make this
	// fallback strictly MORE restrictive (fail-closed) than the live registry
	// path, never more permissive: under god mode it may "ask"/"deny" a tool the
	// live path would allow. The divergence is transient (only until the agent is
	// registered) and never widens, so it is safe.
	cfg := al.GetConfig()
	if cfg == nil {
		return "ask"
	}
	// No default-policy fallback (CLAUDE.md hard constraint 6): only explicit
	// global/agent entries are threaded through; a tool with no match on
	// either side fails closed to "deny" inside tools.EffectiveToolPolicy.
	polCfg, agentType := tools.BuildFallbackPolicyCfg(cfg, agentID)
	return tools.EffectiveToolPolicy(polCfg, tools.ScopeGeneral, agentType, toolName)
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

// GetTaskStore returns the shared unified task Store (may be nil in tests).
func GetTaskStore(al *AgentLoop) *task.Store {
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

	modelCfg, err := resolvedModelConfig(cfg, model, agent.Home)
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

// SetTaskTriggerScheduler installs the task time-trigger scheduler so every task
// create/update/delete path can (re)register or remove the task's cron trigger.
// Called once at boot by the gateway. Idempotent.
func (al *AgentLoop) SetTaskTriggerScheduler(s *TaskTriggerScheduler) {
	al.mu.Lock()
	al.taskTrigger = s
	al.mu.Unlock()
}

// taskTriggerScheduler returns the installed scheduler under the loop lock.
func (al *AgentLoop) taskTriggerScheduler() *TaskTriggerScheduler {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.taskTrigger
}

// NotifyTaskUpserted (re)registers or removes the task's time-trigger cron job
// after a create or update. Nil-safe — a no-op when no scheduler is wired (tests).
func (al *AgentLoop) NotifyTaskUpserted(t *task.Task) {
	if s := al.taskTriggerScheduler(); s != nil && t != nil {
		s.OnTaskUpserted(t)
	}
}

// NotifyTaskDeleted removes the task's time-trigger cron job after a delete.
// Nil-safe — a no-op when no scheduler is wired (tests).
func (al *AgentLoop) NotifyTaskDeleted(taskID string) {
	if s := al.taskTriggerScheduler(); s != nil && taskID != "" {
		s.OnTaskDeleted(taskID)
	}
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
// workspaceID is stamped onto newly-created sessions when non-empty (ADR-029
// FR-022: a session created by a bound channel instance inherits the instance's
// workspace_id). Already-existing sessions are NOT patched — the index hit
// returns the existing ID unchanged (workspace_id was set at creation time).
// Returns "" if the shared store is unavailable or inputs are empty.
func (al *AgentLoop) resolveOrCreateChannelSession(
	channel, instanceID, chatID, agentID, displayName, workspaceID string,
) string {
	if al.sharedSessionStore == nil || channel == "" || chatID == "" {
		return ""
	}
	// Index by the channel INSTANCE, not the bare type, so two instances of the
	// same type (e.g. whatsapp.eu and whatsapp.us) sharing a chat ID get DISTINCT
	// sessions (ADR-029 FR-022 / US-9 — BUG-2). instanceID is the canonical
	// instance identity (== channel for legacy single-instance); fall back
	// defensively when unstamped.
	indexID := instanceID
	if indexID == "" {
		indexID = channel
	}
	key := indexID + "/" + chatID
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
	// FR-022: stamp workspace_id on newly-created sessions for bound instances.
	if workspaceID != "" {
		if patchErr := al.sharedSessionStore.SetMeta(
			meta.ID,
			session.MetaPatch{WorkspaceID: &workspaceID},
		); patchErr != nil {
			logger.WarnCF("agent", "Failed to stamp workspace_id on channel session",
				map[string]any{"session_id": meta.ID, "workspace_id": workspaceID, "error": patchErr.Error()})
		}
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

	// Carry the task's delegation generation forward. The caller (task executor)
	// seeds tools.WithDelegationDepth(ctx, task.DelegationDepth) before invoking
	// this; we read it back to (a) seed the root turnState depth so the per-agent
	// await/background depth gate trips inside the task run, and (b) keep it on the
	// context so a nested task_create stamps its child as generation + 1. A normal
	// chat/board run leaves it 0.
	delegationDepth := tools.ToolDelegationDepth(taskCtx)

	if taskChatID == "" {
		taskChatID = "task:" + sessionKey
	}

	// Fix C: a task assigned to a subagent_3p (external-CLI) worker must
	// dispatch through the SAME external-CLI machinery the agent-to-agent
	// delegation path uses (runner.ResolveDispatch / runExternalCLISubTurn —
	// see subturn.go's identical gate ahead of spawnSubTurn's native/external
	// branch) rather than unconditionally falling into runAgentLoop below.
	// Running a subagent_3p's task on the native engine would silently
	// mis-execute it with full system-level Omnipus tool access instead of the
	// configured external CLI — exactly the gap the assignment-time guards in
	// rest_tasks.go / pkg/tools/task.go / pkg/sysagent/tools/task.go existed to
	// paper over. This dispatch branch is what lets those guards be relaxed.
	dispatchKind, dispatchErr := runner.ResolveDispatch(executorConfigOf(ag))
	if dispatchErr != nil {
		return "", fmt.Errorf("processTaskDirect: %w", dispatchErr)
	}
	if dispatchKind == runner.DispatchKindExternalCLI {
		return al.processTaskDirectExternalCLI(taskCtx, ag, prompt, sessionKey, taskChatID, delegationDepth)
	}

	return al.runAgentLoop(taskCtx, ag, processOptions{
		SessionKey:             sessionKey,
		Channel:                "webchat",
		ChatID:                 taskChatID,
		SenderID:               "task-executor",
		UserMessage:            prompt,
		DefaultResponse:        defaultResponse,
		EnableSummary:          false,
		SendResponse:           false,
		TranscriptSessionID:    taskChatID,
		TranscriptStore:        al.GetAgentStore(agentID),
		InitialDelegationDepth: delegationDepth,
		IsTaskRun:              true,
		// FIX 1 (re-review): the same genuine source processTaskDirectExternalCLI
		// already reads a few lines below (see that function's own comment) — the
		// task executor (task_executor.go) seeds tools.WithWorkspaceID(ctx,
		// task.WorkspaceID) on `ctx` BEFORE calling processTaskDirect, and taskCtx
		// is derived from that same ctx, so tools.ToolWorkspaceID(taskCtx) reads it
		// straight back. Currently inert end-to-end for THIS dispatch branch only
		// because webchatChannel.SendMedia (pkg/gateway/webchat_channel.go)
		// ignores OutboundMediaMessage.WorkspaceID — Channel is hardcoded to
		// "webchat" a few lines up — but it is not moot for every consumer of
		// ts.opts.WorkspaceID (e.g. the memory-routing injection in runTurn), and
		// stays correct with zero further changes here if webchatChannel is ever
		// updated to honor it. Set for consistency with the external-CLI sibling
		// dispatch branch, not invented.
		WorkspaceID: tools.ToolWorkspaceID(taskCtx),
	})
}

// processTaskDirectExternalCLI runs a task assigned to a subagent_3p
// (external-CLI) worker through runExternalCLISubTurn — the same dispatch
// machinery spawnSubTurn uses for agent-to-agent delegation (subturn.go). A
// task run has no parent turnState to derive a child from (unlike a delegated
// sub-turn), so this builds a minimal turnState directly for the target agent
// via newTurnState, wiring the agent snapshot, the task's transcript session
// (so the run is replayable on reload, same as the native task path), and the
// task's WorkspaceID (so workspace.FindForAgentPreferring can route the run
// into the workspace's shared work/ directory when the agent is a workspace
// CoreTeam member — mirrors the native runTurn resolution). Channel/ChatID/
// SenderID/UserMessage are ALSO set on opts — not inert: since FIX 5 (below)
// registers this turnState in al.activeTurnStates, turnState.snapshot() (via
// GetActiveTurn/GetActiveTurnBySession) now surfaces them for the duration of
// the run, exactly like a native turn's.
//
// An external-CLI worker's tool registry is its OWN CLI's, never Omnipus's —
// it has no task_update tool wired at all — so buildPrompt's ADR-043
// TASK_STATUS/TASK_SUMMARY marker instruction (task_executor.go) is this
// dispatch kind's ONLY possible completion signal. The caller
// (TaskExecutor.finishTaskRun) parses the aggregated CLI output (ForUser,
// falling back to ForLLM) for that marker: a found "success"/"failure" line
// lands the task Done/Failed with the marker's own reported words as Result;
// no parseable marker at all fails the task closed (StatusFailed) — it is
// NEVER auto-completed to Done on unverified prose alone. This replaced the
// former "auto-complete to Done, WARN-only" default (ADR-042 §3's finding);
// see ADR-043 for the full contract and finishTaskRun's own WarnCF log on the
// fail-closed path.
//
// Delegation-depth bounding: the dispatched CLI child runs as a separate OS
// process with its own tool registry — it has no delegate/create_task tools
// wired to Omnipus at all — so it structurally cannot recurse into another
// Omnipus delegation or task chain regardless of depth. ts.depth is still
// seeded from the caller's delegationDepth for observability/symmetry with
// the native branch's opts.InitialDelegationDepth.
//
// FIX 5 (7-reviewer gate, visibility): this turnState IS now registered in
// al.activeTurnStates for the run's duration (register/defer-clear below,
// mirroring native runTurn's registerActiveTurn/clearActiveTurn pair and
// spawnSubTurn's childTS registration, subturn.go:880-881) — ts.depth is read
// by cancel.go's activeTurnStates.Range-based readers now that the turn is
// reachable there (it previously was not: an unregistered turnState made
// ts.depth dead for every purpose except this function's own local seeding).
// Registering also means:
//   - writeTurnCancelledRestartForActiveTurns' FR-048 graceful-shutdown scan
//     now covers an in-flight external-CLI task run (previously it silently
//     vanished from the transcript on a mid-run restart).
//   - GetActiveTurn/GetActiveAgentIDs now report this run like any other.
//   - A RequestCancel against this session (transcriptSessionID == taskChatID)
//     can reach and ClaimCancel this turnState. STALE-COMMENT CORRECTION
//     (doc-only, cancel-propagation FIX 1): this used to say ts.cancelFunc/
//     ts.providerCancel stay nil for this dispatch path — that is no longer
//     true. runExternalCLISubTurn (external_dispatch.go) now calls
//     childTS.setTurnCancel(cancel) / childTS.setProviderCancel(cancel) on
//     THIS SAME ts (it is passed in as runExternalCLISubTurn's childTS
//     argument below), wiring both fields to the context.CancelFunc that
//     actually tears down the dispatched external-CLI subprocess (every
//     driver binds the OS child via exec.CommandContext(runCtx, ...), so
//     canceling that func kills the subprocess outright — see FIX 1's own
//     doc comment at that call site for the full rationale). So a
//     RequestCancel reaching this turnState now does more than update
//     transcript/audit bookkeeping: it ALSO cancels the real external-CLI
//     process, the same as the native delegation path. The remaining true
//     part of the original claim: this dispatch still has no
//     delegate/create_task tools that could populate ts.childTurnIDs, so the
//     hard-abort child-cascade branch is still unreachable here — that part
//     of the "no-panic" reasoning is unaffected.
func (al *AgentLoop) processTaskDirectExternalCLI(
	ctx context.Context,
	liveAgent *AgentInstance,
	prompt, sessionKey, taskChatID string,
	delegationDepth int,
) (string, error) {
	// FIX 1 (7-reviewer gate, data race): liveAgent is the LIVE registry
	// *AgentInstance (registry.GetAgent, in processTaskDirect above) —
	// SwitchModel/ApplyAgentModel may concurrently rewrite its
	// Model/Provider/Candidates/ThinkingLevel tuple (+ providerPool) while
	// this run is in flight (AgentInstance.mu's doc, instance.go:28-30), and
	// runExternalCLISubTurn reads agent.Model unlocked (transcript
	// attribution + RunOptions.Model) — a read/write race with SwitchModel.
	// snapshotForExternalDispatch takes a single RLock and copies the whole
	// mutex-protected quad together into a private AgentInstance value
	// nothing else can mutate, mirroring spawnSubTurn's execSource-snapshot
	// pattern (subturn.go ~603-662, which the native delegation path already
	// relies on for the identical reason). Every field below (opts,
	// newTurnState, composeDelegateInput) reads from this snapshot, never
	// liveAgent directly.
	agent := liveAgent.snapshotForExternalDispatch()

	opts := processOptions{
		SessionKey:          sessionKey,
		Channel:             "webchat",
		ChatID:              taskChatID,
		SenderID:            "task-executor",
		UserMessage:         prompt,
		TranscriptSessionID: taskChatID,
		TranscriptStore:     al.GetAgentStore(agent.ID),
		// WorkspaceID is already on ctx via tools.WithWorkspaceID (set by the
		// task executor before calling processTaskDirect); thread it through
		// processOptions explicitly too so runExternalCLISubTurn's
		// workspace.FindForAgentPreferring(..., childTS.opts.WorkspaceID) call
		// sees it — that field reads ts.opts, not the context.
		WorkspaceID: tools.ToolWorkspaceID(ctx),
	}
	ts := newTurnState(agent, opts, al.newTurnEventScope(agent.ID, sessionKey))
	ts.depth = delegationDepth
	ts.al = al // FIX 5: back-ref for hard-abort cascade (mirrors subturn.go:831)

	// FIX 5: register for the run's duration — see this function's doc
	// comment for the full reachability analysis.
	al.registerActiveTurn(ts)
	// FINAL-GATE FIX (2026-07-13, cancel audit-trail gap): FIX 5 registered
	// this turnState so a RequestCancel could reach and ClaimCancel it, but
	// nothing on this path ever called ts.Finish — the ONE place that fires
	// the onCancelFinish callback RequestCancel installs via
	// SetOnCancelFinish (pkg/agent/cancel.go). Without it, a cancel here
	// claimed cancelFired (CancelOutcome{Fired: true}) but produced NO
	// turn_canceled transcript entry, NO MarkLastEntryTruncated, and NO
	// audit.EventTurnCancelled — silently contradicting this function's own
	// doc comment above, which already (incorrectly, until this fix)
	// described the callback as firing.
	//
	// Finish(false) is safe to add here: at THIS point (construction, right
	// before registerActiveTurn ran above) ts.cancelFunc/ts.providerCancel
	// are still nil — cancel-propagation FIX 1 (external_dispatch.go's
	// runExternalCLISubTurn) only wires them once dispatch actually starts,
	// below. By the time this function returns and the deferred Finish(false)
	// below actually RUNS, those fields are typically non-nil (set to the
	// dispatch's own context.CancelFunc) — see this function's top doc
	// comment's STALE-COMMENT CORRECTION note for the full explanation. That
	// does not change this safety argument: Finish's cancelFunc branch
	// (`if ts.cancelFunc != nil { ts.cancelFunc() }`) simply invokes it,
	// which is exactly what dispatchCancel's own `defer dispatchCancel()`
	// below already guarantees happens — canceling an already-canceled
	// context is a no-op, so calling it twice (once via that defer, once via
	// Finish) is harmless. isHardAbort is false so the child-cascade branch
	// is unreachable, and Finish's closeOnce.Do + the
	// cancelFired-swap-then-nil-check around onCancelFinish make a second
	// Finish call (e.g. a concurrent InterruptSessionHard elsewhere calling
	// Finish(true) on this same ts via steering.go) idempotent — the
	// identical safety runTurn's own `defer ts.Finish(false)` already relies
	// on for the hard-abort-then-graceful-defer sequence (loop.go, "closeOnce.Do
	// inside Finish makes repeated Finish calls safe" comment).
	//
	// Ordering matches runTurn's LIFO defer pattern (loop.go, "Execution
	// order (LIFO defer...)" comment): clearActiveTurn must run BEFORE
	// Finish, so a cancel racing the tail end of this dispatch cannot find a
	// since-finished turnState still reachable via
	// GetActiveTurnHookForSession and register a callback that can now never
	// fire — the same class of race that ordering guards against in
	// runTurn. Defers execute LIFO, so writing Finish's defer first and
	// clearActiveTurn's defer second makes clearActiveTurn run FIRST and
	// Finish run LAST, exactly like runTurn's `defer ts.Finish(false)` /
	// `defer al.clearActiveTurn(ts)` pair.
	defer ts.Finish(false)
	defer al.clearActiveTurn(ts)

	rtCfg := al.getSubTurnConfig()

	// ADDITIONAL FINDING (surfaced while writing pr-test-analyzer's T2, not
	// one of the 11 numbered fixes): runExternalCLISubTurn never wraps its
	// own ctx with a deadline — it derives runCtx via plain
	// context.WithCancel(ctx) (external_dispatch.go) and only forwards
	// rtCfg.defaultTimeout to the DRIVER as RunOptions.TimeoutSeconds, a hint
	// each real driver applies itself (driver_claude.go/driver_codex.go/
	// driver_opencode.go all do `context.WithTimeout(runCtx,
	// TimeoutSeconds*time.Second)` internally). spawnSubTurn's native
	// delegation path already has its OWN Go-level safety-net timeout
	// (subturn.go ~458-473: `context.WithTimeout(context.Background(),
	// timeout)`) precisely so a driver that never honors/emits an end event
	// cannot hang the dispatch forever; this task-mode dispatch had no
	// equivalent — a stuck external CLI would tie up a dispatch-semaphore
	// slot indefinitely with nothing to notice. Unlike spawnSubTurn's
	// Background()-rooted child (deliberately independent so a Critical
	// sub-turn survives its parent's graceful finish), this derives the
	// deadline FROM the incoming ctx — consistent with the native task path,
	// where runTurn's own turnTimeout is likewise derived from the given ctx
	// (loop.go) — so a TaskExecutor-level cancel (te.running[taskID].cancel(),
	// ExecuteTask/StartTaskNow) still takes effect immediately in addition to
	// this deadline.
	dispatchCtx, dispatchCancel := context.WithTimeout(ctx, rtCfg.defaultTimeout)
	defer dispatchCancel()

	// FIX 2 (7-reviewer gate, persona dropped): compose the same (soul, task)
	// pair the native delegation path uses ahead of its own
	// runExternalCLISubTurn call (subturn.go composeDelegateInput call site)
	// so the target's own soul/persona travels with a TASK-mode dispatch too,
	// not just an agent-to-agent delegate call. An empty soul (the worker
	// case, where a soul is OPTIONAL) yields task-only input, identical to
	// the pre-fix behavior.
	externalInput := composeDelegateInput(al, prompt, "", agent.ID)

	result, err := runExternalCLISubTurn(dispatchCtx, al, ts, externalInput, rtCfg.defaultTimeout)
	if err != nil {
		return "", fmt.Errorf("processTaskDirect: external-cli dispatch: %w", err)
	}
	// FIX 6 (7-reviewer gate, dead defensive branch): runExternalCLISubTurn's
	// only two return statements are `return nil, fmt.Errorf(...)` (already
	// handled by the err != nil check above — a non-nil err is ALWAYS paired
	// with a nil result on that path) and the terminal `return result,
	// result.Err` (drainExternalRun always returns a non-nil *tools.ToolResult,
	// so result.Err is nil exactly when err here is nil). So once err == nil,
	// result == nil and result.Err != nil are BOTH unreachable. The old `if
	// result == nil { return "", nil }` silently reported task SUCCESS for a
	// broken invariant instead of surfacing it — fail loudly so a future
	// change that breaks the invariant is caught immediately, not masked as
	// an empty-but-successful task result.
	if result == nil {
		return "", fmt.Errorf("processTaskDirect: external-cli dispatch: nil result with no error")
	}
	if result.ForUser != "" {
		return result.ForUser, nil
	}
	return result.ForLLM, nil
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

// GetDriftDropped returns the cumulative count of bound-instance drift drops:
// inbound messages on a workspace-bound channel instance whose configured agent
// was unresolvable (deleted or a worker). Safe for concurrent access;
// incremented atomically in resolveMessageRoute (ADR-029 FR-028 / MAJ-003).
func (al *AgentLoop) GetDriftDropped() int64 {
	return al.driftDropped.Load()
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
//
// allow is expected to already be the combined boot decision — the caller
// (pkg/gateway/gateway.go's resolveAllowGodMode) ORs the --allow-god-mode CLI
// flag with the config-persisted sandbox.god_mode_allowed grant before
// calling this, so there is exactly one source of truth for availability.
func (al *AgentLoop) SetAllowGodMode(allow bool) {
	al.mu.Lock()
	al.allowGodMode = allow
	al.mu.Unlock()
	// Publish god-mode AVAILABILITY (boot flag AND build support) to the
	// package-level gate so the resolution-time override engine
	// (agentToolsCfgToPolicy / godModeActive) can decide whether the runtime
	// sandbox.god_mode switch has any effect. Availability is fixed at boot;
	// the on/off STATE lives in cfg.Sandbox.GodMode and is re-read on every
	// agent rebuild (TriggerReload).
	setGodModeAvailable(allow && sandbox.GodModeAvailable)
}

// WireSysagentDeps registers all 35 system.* tools on every agent in the
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
		path, meta, err := store.ResolveWithMetaOpts(ref, media.ResolveOpts{})
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
		Channel: channel,
		Sender: bus.SenderInfo{
			CanonicalID: "cron",
		},
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	resp, _, err := al.processMessage(ctx, msg)
	return resp, err
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
	// to GetDefaultAgent.
	agent, ok := al.GetRegistry().GetAgent(ownerAgentID)
	if !ok || agent == nil {
		return "", fmt.Errorf("owner unavailable: agent %q not found", ownerAgentID)
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

	// FIX 1 (re-review): WorkspaceID was never threaded here, so any tool
	// media a scheduled/heartbeat-fired run produces silently degraded to the
	// private/global room (bus.OutboundMediaMessage.WorkspaceID stays "")
	// even when `channel` is an operator-configured EXTERNAL channel (Slack,
	// Telegram, ...) whose SendMedia does honor it. The concrete session this
	// run writes into (sessionID) is the one genuine source available here —
	// same lookup processMessage (loop.go, ~line 5332) uses for the
	// interactive path.
	//
	// GAP CLOSED: pkg/gateway/schedules.go's scheduledRunner.pickSession now
	// resolves the schedule's workspace (from a heartbeat job's deterministic
	// name, or from the channel instance a plain schedule's payload.Channel
	// names — see resolveScheduleWorkspaceID's doc there) and stamps it onto
	// the session's meta via stampScheduledSessionWorkspace BEFORE this
	// function runs, using the exact GetSessionStore()/GetMeta/SetMeta
	// surface this read below already relies on. This function needed no
	// logic change to pick that up — it was already forward-compatible by
	// design, as this comment previously promised. A schedule with no
	// resolvable workspace (no heartbeat identity, no channel binding) still
	// correctly resolves to "" here — never fabricated.
	workspaceID := ""
	if meta, mErr := transcriptStore.GetMeta(sessionID); mErr == nil && meta != nil {
		workspaceID = meta.WorkspaceID
	}

	resp, err := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:          sessionKey,
		Channel:             channel,
		ChatID:              chatID,
		UserMessage:         content,
		DefaultResponse:     defaultResponse,
		EnableSummary:       true,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     transcriptStore,
		WorkspaceID:         workspaceID,
		AutoDenyAsk:         true, // FR-009: headless — auto-deny ask-policy tools
	})
	if err == nil && ctx.Err() == context.DeadlineExceeded {
		// A deadline-forced hard abort (pkg/gateway/schedules.go's
		// watchDeadline, on this run's own ctx deadline expiring) escalates
		// through the identical ts.requestHardAbort()/InterruptSessionHard
		// path a live user's own turn-cancel takes. abortTurn treats both as
		// a clean, intentional stop and returns a nil error — correct for an
		// interactive chat user who already knows their cancel landed, but
		// wrong here: a headless scheduled run has no such user, and the
		// caller (schedules.go) needs a non-nil error to classify this run
		// as a timeout rather than silently recording it as a success.
		// Restore the "aborted (canceled/deadline) run returns a
		// context-derived error promptly" contract promised above.
		err = fmt.Errorf("scheduled run: %w", context.DeadlineExceeded)
	}
	return resp, err
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
		fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.Sender.CanonicalID, logContent),
		map[string]any{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.Sender.CanonicalID,
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
		// ADR-029 FR-028/MAJ-003: emit the drift-drop counter and audit event
		// exactly once — here, at the single point where the message is actually
		// rejected.  resolveMessageRoute returns route.Drop=true only for
		// workspace-bound instances whose configured agent is unresolvable
		// (deleted or a worker).  resolveMessageRoute itself is side-effect-free
		// w.r.t. this counter so that its multiple callers (resolveSteeringTarget,
		// buildContinuationTarget) do not double-count the same drop.
		if route.Drop {
			instanceID := inboundInstanceID(msg)
			wsID := ""
			intendedAgent := ""
			if identity := al.resolveInboundIdentity(instanceID); identity != nil {
				intendedAgent = strings.TrimSpace(identity.ID)
			}
			if cfg := al.GetConfig(); cfg != nil {
				if inst, ok := cfg.Channels[instanceID]; ok {
					wsID = inst.WorkspaceID
				}
			}
			al.driftDropped.Add(1)
			if al.auditLogger != nil {
				_ = al.auditLogger.Log(&audit.Entry{
					Event:    audit.EventChannelRoutingDriftDrop,
					Decision: audit.DecisionDeny,
					Details: map[string]any{
						"instance_id":       instanceID,
						"workspace_id":      wsID,
						"intended_agent_id": intendedAgent,
						"chat_id":           msg.ChatID,
						"reason":            "bound agent unresolvable (deleted or worker — not a chat target)",
					},
				})
			}
		}
		return "", nil, routeErr
	}

	// Reset message-tool state for this round so we don't skip publishing due to a previous round.
	if tool, ok := agent.Tools.Get("send_message"); ok {
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
	// FR-022 (ADR-029): for a bound channel instance, stamp the session with the
	// instance's workspace_id so it appears linked to the right workspace.
	if msg.SessionID == "" && msg.Channel != "webchat" && msg.Channel != "system" && msg.Channel != "" {
		instanceSessionID := inboundInstanceID(msg)
		sessionWorkspaceID := ""
		if instanceSessionID != "" {
			if sessionCfg := al.GetConfig(); sessionCfg != nil {
				if instCfg, ok := sessionCfg.Channels[instanceSessionID]; ok {
					sessionWorkspaceID = instCfg.WorkspaceID
				}
			}
		}
		if sid := al.resolveOrCreateChannelSession(
			msg.Channel, instanceSessionID, msg.ChatID, agent.ID, msg.Sender.DisplayName, sessionWorkspaceID,
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

	// M4: bind the active workspace into this turn so a task an agent creates
	// (task_create / delegation) lands on the ACTIVE workspace's board rather
	// than the agent's default workspace. A web-chat / channel session that was
	// opened from a workspace carries the workspace ID on its meta (set via the
	// session-scope PUT or at session creation). Resolve it here so the tool
	// context (loop.go: WithWorkspaceID) carries it through to resolveWorkspaceID.
	// Falls back to the inbound metadata key when present (e.g. board-task runs),
	// and finally to "" — task_create then resolves the real default workspace.
	workspaceID := ""
	if transcriptStore != nil && transcriptSessionID != "" {
		if meta, mErr := transcriptStore.GetMeta(transcriptSessionID); mErr == nil && meta != nil {
			workspaceID = meta.WorkspaceID
		}
	}
	if workspaceID == "" {
		workspaceID = inboundMetadata(msg, "workspace_id")
	}

	opts := processOptions{
		SessionKey:        sessionKey,
		Channel:           msg.Channel,
		ChatID:            msg.ChatID,
		SenderID:          msg.Sender.CanonicalID,
		SenderDisplayName: msg.Sender.DisplayName,
		// FR-017: thread the authenticated gateway principal into the turn for
		// audit attribution. Only the gateway webchat WS path sets
		// msg.GatewayUserID (= wc.userID, the WS-authenticated identity, e.g.
		// "cli" or an admin username). Channel/task/scheduled inbound messages
		// never set it, so their turns leave audit.Entry.User empty structurally.
		// We deliberately do NOT read msg.Sender.Username here: channels populate
		// it with the platform handle (e.g. "@alice"), which is not a gateway
		// principal and must never be stamped as audit User.
		UserID:              gatewayPrincipal(msg),
		UserMessage:         msg.Content,
		Media:               msg.Media,
		DefaultResponse:     defaultResponse,
		EnableSummary:       true,
		SendResponse:        false,
		TranscriptSessionID: transcriptSessionID,
		TranscriptStore:     transcriptStore,
		WorkspaceID:         workspaceID,
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
			// A worker is a delegation-only labor tier — never a chat target.
			// An inbound message that explicitly addresses a worker (e.g. a stale
			// SPA dropdown value or a crafted channel payload) must NOT let the
			// worker answer as a live persona. Degrade to the normal routing
			// cascade, which resolves a chat-target default. Do not delete any
			// handoff pin here — falling through preserves an existing chat-target
			// override if one is set.
			if agent.IsWorker() {
				logger.WarnCF(
					"agent",
					"Explicit agent_id references a worker (not a chat target); ignoring and falling back to default route",
					map[string]any{
						"agent_id":   explicitID,
						"session_id": msg.SessionID,
						"reason":     "worker is invoked via delegation, not as a chat target",
					},
				)
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
					"workspace":  agent.Home,
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
						logger.WarnCF(
							"agent",
							"Session handoff pin references a worker (not a chat target); clearing stale pin and falling back to default route",
							map[string]any{
								"session_id": msg.SessionID,
								"agent_id":   agentID,
								"reason":     "worker is invoked via delegation, not as a chat target",
							},
						)
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
	identity := al.resolveInboundIdentity(instanceID)

	// ADR-029 FR-012/FR-014: set BoundInstance=true when the instance is
	// workspace-bound AND carries an agent-kind identity. Both conditions must
	// hold: a bare identity without a workspace (legacy routing) must not trigger
	// the drift-drop guard, and a workspace binding without an agent identity
	// cannot be drift-checked (nothing to validate). The drift guard inside
	// ResolveRoute then enforces that a workspace-bound instance never falls back
	// to the global default when its agent is unresolvable.
	var boundInstance bool
	if identity != nil && strings.ToLower(strings.TrimSpace(identity.Kind)) == "agent" {
		if cfg := al.GetConfig(); cfg != nil {
			if inst, ok := cfg.Channels[instanceID]; ok && inst.WorkspaceID != "" {
				boundInstance = true
			}
		}
	}

	route := registry.ResolveRoute(routing.RouteInput{
		Channel:       msg.Channel,
		AccountID:     inboundMetadata(msg, metadataKeyAccountID),
		Peer:          extractPeer(msg),
		ParentPeer:    extractParentPeer(msg),
		GuildID:       inboundMetadata(msg, metadataKeyGuildID),
		TeamID:        inboundMetadata(msg, metadataKeyTeamID),
		InstanceID:    instanceID,
		Identity:      identity,
		BoundInstance: boundInstance,
	})

	// ADR-029 FR-012/FR-028: a drift drop means the bound agent is unresolvable.
	// Do NOT call GetDefaultAgent — enter the FR-015 unroutable path directly.
	// The counter increment and audit event are emitted ONCE at the processMessage
	// rejection point (the single true drop site), NOT here.  resolveMessageRoute
	// is a side-effect-free resolver: multiple call sites (resolveSteeringTarget,
	// buildContinuationTarget) invoke it on the same message, so any emission here
	// would fire multiple times per inbound message and corrupt the FR-028/MAJ-003
	// counter and audit trail.  Return the route with Drop=true so the caller can
	// detect the drift condition and emit the structured event exactly once.
	if route.Drop {
		intendedAgent := ""
		if identity != nil {
			intendedAgent = strings.TrimSpace(identity.ID)
		}
		logger.WarnCF(
			"agent",
			"Bound-instance drift drop: configured agent unresolvable; message rejected (ADR-029 FR-012)",
			map[string]any{
				"instance_id": instanceID,
				"channel":     msg.Channel,
				"chat_id":     msg.ChatID,
				"matched_by":  route.MatchedBy,
			},
		)
		return route, nil, fmt.Errorf("no agent available for route (agent_id=%s)", intendedAgent)
	}

	agent, ok := registry.GetAgent(route.AgentID)
	if !ok {
		agent = registry.GetDefaultAgent()
	}
	if agent == nil {
		// FR-015: log unroutable message with structured context before rejecting.
		logger.WarnCF("agent", "Unroutable message rejected — no matching agent and no default",
			map[string]any{
				"channel":        msg.Channel,
				"sender_id":      msg.Sender.CanonicalID,
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
//
// For channel inbound, the chat-scoped key uses msg.InstanceID when non-empty
// (ADR-029 FR-023, MAJ-002): two instances of the same channel type (e.g.
// "whatsapp.eu" and "whatsapp.us") with the same ChatID must NOT share a
// transcript key. Legacy channels that have not yet been updated to stamp
// InstanceID fall back to msg.Channel (the type), preserving existing behavior.
func agentSessionKey(agentID string, msg bus.InboundMessage) string {
	if msg.SessionID != "" {
		return fmt.Sprintf("agent:%s:session:%s", agentID, msg.SessionID)
	}
	// Use the stamped InstanceID when available (per-instance isolation);
	// fall back to the channel type for adapters that have not yet been updated.
	instanceOrChannel := msg.Channel
	if msg.InstanceID != "" {
		instanceOrChannel = msg.InstanceID
	}
	return fmt.Sprintf("agent:%s:chat:%s:%s", agentID, instanceOrChannel, msg.ChatID)
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
			"sender_id": msg.Sender.CanonicalID,
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
				"sender_id":   msg.Sender.CanonicalID,
				"content_len": len(content),
				"channel":     originChannel,
			})
		return "", nil
	}

	// FIX 5d (#1): resolve the TRUE originating agent when the message carries
	// one (AsyncNotifier.Notify sets AsyncOriginAgentID for an async tool/
	// delegate result) — never guess GetDefaultAgent() when the real producer
	// is known. This is the confirmed, exact cause of a live "Worker vs Jim"
	// speaker-attribution flip: an async result from a non-default agent used
	// to be silently reattributed to whichever agent happens to be default.
	// GetDefaultAgent() remains the fallback for messages with no async
	// origin (or a named agent that has since been deleted) — a genuine
	// last resort, not the primary path.
	var agent *AgentInstance
	if msg.AsyncOriginAgentID != "" {
		if named, ok := al.GetRegistry().GetAgent(msg.AsyncOriginAgentID); ok && named != nil {
			agent = named
		} else {
			logger.WarnCF(
				"agent",
				"processSystemMessage: named async origin agent not found; falling back to default agent",
				map[string]any{"agent_id": msg.AsyncOriginAgentID},
			)
		}
	}
	if agent == nil {
		agent = al.GetRegistry().GetDefaultAgent()
	}
	if agent == nil {
		return "", fmt.Errorf("no default agent for system message")
	}

	// FIX 5d (#2): resolve the originating turn's transcript session/store so
	// this reconstructed turn persists into the SAME session the producing
	// turn was writing to — the same "run a turn that must land in a
	// specific, pre-existing session" pattern ProcessScheduled and
	// spawnSubTurn already use (al.ResolveSessionStore /
	// TranscriptSessionID+TranscriptStore threading). Without this,
	// persistence depended ENTIRELY on a live WebSocket connection still
	// being open when the async result landed — if it had already closed,
	// the result was silently, permanently lost. A session ID that no longer
	// resolves to a store (deleted session) degrades to "not persisted" —
	// the same outcome as before this fix, not a new failure mode.
	var transcriptSessionID string
	var transcriptStore *session.UnifiedStore
	if msg.AsyncTranscriptSessionID != "" {
		if store := al.ResolveSessionStore(msg.AsyncTranscriptSessionID); store != nil {
			transcriptSessionID = msg.AsyncTranscriptSessionID
			transcriptStore = store
		} else {
			logger.WarnCF(
				"agent",
				"processSystemMessage: async transcript session not found; result will not be persisted to a session",
				map[string]any{"session_id": msg.AsyncTranscriptSessionID},
			)
		}
	}

	// A-I4 round 6, Priority 2: scope the reconstructed turn's SessionKey to
	// the SPECIFIC originating session (mirroring agentSessionKey's
	// "agent:<id>:session:<sid>" convention every regular routed turn
	// already uses — pkg/agent/loop.go:4794) rather than the agent-wide
	// routing.BuildAgentMainSessionKey "agent:<id>:main" bucket this used to
	// hard-code unconditionally.
	//
	// SessionKey drives THREE things that must never be shared across
	// unrelated originating sessions: (1) agent.Sessions.GetHistory/
	// SetHistory/Save — the in-memory LLM conversation history this turn's
	// prompt is built from (loop.go:5415, 6223, 6367, 7819, 7944, 8084,
	// 8254, 8464, 8949-8951); (2) activeTurnStates registration/lookup; and
	// (3) the per-scope sessionWorker (pkg/agent/session_worker.go) that
	// serializes turns and steers same-scope late-arriving messages into an
	// ALREADY-RUNNING turn rather than starting a new one.
	//
	// Root cause this closes (A-I4 round 6 Priority 2 cross-session leak,
	// live-verified 2026-07): every delegate-completion notification for the
	// SAME agent — regardless of which real chat session originated the
	// underlying delegate/background work — used to collapse onto the ONE
	// "agent:<id>:main" key. Two delegate completions for the same agent
	// landing close together (routine under normal use — an orchestrator
	// like Jim commonly has several concurrent background delegates) then
	// shared: the same in-memory history bucket (so the second notify-turn's
	// LLM prompt was contaminated with the first, unrelated notify-turn's
	// conversation, producing a recap that blends or hallucinates facts from
	// a DIFFERENT session's delegation — reproduced live: a session that
	// only ever delegated to "Ava" received a persisted assistant turn
	// narrating a nonexistent "delegation to Ray" pulled from an unrelated
	// session's exchange); and the same sessionWorker scope (so the second
	// notify-turn could be STEERED into the first's still-running turn as a
	// mid-turn continuation instead of running as its own turn — see
	// session_worker.go's enqueue doc comment — further blending two
	// unrelated sessions' content into one LLM call).
	//
	// TranscriptSessionID/TranscriptStore (already correctly scoped by FIX
	// 5d above) only controls WHERE the turn's output is persisted — it does
	// nothing to isolate WHAT content that turn's own LLM call is built
	// from. Both must agree for one session's recap to never see another
	// session's data. Falls back to the unscoped main key only when no
	// origin session is known (a system message with no AsyncNotifier
	// origin), matching the pre-fix behavior for that narrower case.
	sessionKey := routing.BuildAgentMainSessionKey(agent.ID)
	if transcriptSessionID != "" {
		sessionKey = fmt.Sprintf("agent:%s:session:%s", agent.ID, transcriptSessionID)
	}

	// FIX 1 (re-review): mirror processMessage's WorkspaceID resolution
	// (loop.go, "M4" comment ~line 5332) so a delegate-completion / async-
	// notify turn reconstructed here also stamps bus.OutboundMediaMessage
	// with the real workspace instead of silently falling back to the
	// private/global room. originChannel is parsed straight from msg.ChatID
	// above and can be ANY external channel the producing turn was bound to
	// — the same class of gap ProcessScheduled has. The session this turn
	// persists into (transcriptSessionID, already resolved above by FIX 5d)
	// is the authoritative source: it is the SAME session the producing turn
	// wrote to, so its meta.WorkspaceID (stamped at session-creation time via
	// resolveOrCreateChannelSession's channel-binding lookup) is exactly the
	// workspace this reconstructed turn should inherit. Falls back to the
	// inbound metadata key, matching processMessage's own final fallback, for
	// callers that stamp workspace_id directly on the system message instead.
	workspaceID := ""
	if transcriptStore != nil && transcriptSessionID != "" {
		if meta, mErr := transcriptStore.GetMeta(transcriptSessionID); mErr == nil && meta != nil {
			workspaceID = meta.WorkspaceID
		}
	}
	if workspaceID == "" {
		workspaceID = inboundMetadata(msg, "workspace_id")
	}

	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:          sessionKey,
		Channel:             originChannel,
		ChatID:              originChatID,
		UserMessage:         fmt.Sprintf("[System: %s] %s", msg.Sender.CanonicalID, msg.Content),
		DefaultResponse:     "Background task completed.",
		EnableSummary:       false,
		SendResponse:        true,
		TranscriptSessionID: transcriptSessionID,
		TranscriptStore:     transcriptStore,
		WorkspaceID:         workspaceID,
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
	// Seed the delegation-chain depth for a task run started from within another
	// task (opts.InitialDelegationDepth > 0). A task run otherwise begins a fresh
	// root turn at depth 0, which would make currentDelegationDepth read 0 inside
	// the run and never trip the per-agent await/background depth gate; seeding the
	// stored generation here restores the bound. Root chat/board turns pass 0.
	if opts.InitialDelegationDepth > 0 {
		ts.depth = opts.InitialDelegationDepth
	}
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
	// Snapshot the result for test observability (lastTurnResult field).
	// This is the only writer; production callers never read lastTurnResult.
	al.lastTurnResultMu.Lock()
	al.lastTurnResult = result
	al.lastTurnResultMu.Unlock()
	if err != nil {
		return "", err
	}
	if result.status == TurnEndStatusAborted {
		// Reached only for a case-1 (user-initiated) hard abort — abortTurn
		// returns a non-nil error for every system-initiated abort (case 2),
		// which the `if err != nil` branch above already returned from. See
		// abortTurn's doc comment for the full case split.
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

// isMessagingChannel returns true for external human-messaging channel types.
// Tool feedback is emitted only on these channels; webchat, system/cli/subagent,
// cron schedules, and unknown/empty channels remain silent because either the UI
// renders tool calls inline or there is no human recipient.
//
// NOTE: extend this list whenever a new human-messaging channel is registered in
// pkg/channels/manager.go — a missing entry silently suppresses tool-feedback
// on that channel.
func isMessagingChannel(channel string) bool {
	switch channel {
	case "telegram", "discord", "slack", "whatsapp", "whatsapp_native", "matrix",
		"irc", "google-chat", "line", "wecom", "weixin", "dingtalk", "qq",
		"feishu":
		return true
	}
	return false
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
	// ADR-051 Rev 4 (Wave 3 T9): snapshot the capability catalog and the
	// per-session manifest refcounter for the presentation chain. Both are
	// nil-safe (nil catalog → optimistic gate; nil refcounter → no tracking),
	// so legacy/no-workspace turns degrade gracefully.
	turnCatalog := al.getCapabilityCatalog()
	turnRefcounter := al.getTurnRefcounter(ts.opts.WorkspaceID, ts.transcriptSessionID)

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

	// NOTE: finalizeStreamer's defer is registered further below (after
	// ts.Finish(false)/al.clearActiveTurn), not here — see that registration
	// site's comment for why the ORDER relative to Finish is load-bearing
	// (FIX 1/5c live-verification finding).

	// Inject turnState and AgentLoop into context so tools (e.g. delegate) can retrieve them.
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
	// This stays driven EXCLUSIVELY by the turn's channel-bound WorkspaceID —
	// unchanged by the CoreTeam-based filesystem re-rooting below, which is a
	// deliberately separate, independent signal (see that block's comment).
	if ts.opts.WorkspaceID != "" {
		turnCtx = tools.WithWorkspaceID(turnCtx, ts.opts.WorkspaceID)
	}

	// Filesystem re-rooting: every agent that belongs to a Workspace's CoreTeam
	// — native (Main/Subagent) or subagent_3p (external-CLI), no exceptions by
	// kind — works in that Workspace's dedicated project-work subdirectory
	// (workspaces/<id>/work/, workspace.SafeWorkDir) instead of its private
	// per-agent one. Unconditional (no feature flag) and PRIMARILY driven by
	// AGENT IDENTITY (workspace.FindForAgent's CoreTeam-membership lookup),
	// NOT by ts.opts.WorkspaceID above: those are genuinely different signals
	// that can diverge in both directions — a CoreTeam member responding via
	// an unbound channel still has ts.opts.WorkspaceID == ""; a channel bound
	// to a workspace can route to an agent stale-removed from that
	// workspace's CoreTeam. Keying off agent identity instead of the
	// turn-carried value is also what makes this correctly cover DELEGATED
	// sub-agent turns regardless of whether ts.opts.WorkspaceID happens to be
	// set on the child: this identity-keyed lookup applies uniformly to
	// top-level turns and delegated children alike, since both resolve
	// ts.agent.ID the same way.
	//
	// STALE-COMMENT CORRECTION (FIX 1 re-review): this used to say
	// spawnSubTurn never threads WorkspaceID into a child's processOptions,
	// making ts.opts.WorkspaceID structurally always "" for a delegated
	// child. That is no longer true — spawnSubTurn (pkg/agent/subturn.go)
	// now inherits WorkspaceID from the PARENT turn (session/room context,
	// same as Channel/ChatID; see that struct literal's own comment for why
	// this is deliberately NOT covered by ADR-032's target-identity
	// inheritance rule). A delegated child's ts.opts.WorkspaceID can
	// therefore be non-empty today, which is exactly what lets it
	// participate in FindForAgentPreferring's tie-break below when the
	// child agent belongs to more than one workspace's CoreTeam — it does
	// NOT change the identity-primacy described above: a child with no
	// CoreTeam membership at all still gets no re-root regardless of
	// ts.opts.WorkspaceID, and a child whose membership is unambiguous
	// (one workspace) resolves the same way with or without it.
	//
	// FindForAgentPreferring (not FindForAgent) is used here so that when the
	// SAME agent belongs to MORE than one workspace's CoreTeam — a real,
	// reachable state FindForAgent's own doc comment describes — the CURRENT
	// turn's own ts.opts.WorkspaceID (when it is itself one of the agent's
	// memberships) breaks the tie, instead of FindForAgent's arbitrary
	// sorted-first pick. This narrows an already-ambiguous choice using a
	// signal that is trustworthy exactly when it is present; it never widens
	// or overrides the identity-based membership check, so an unbound-channel
	// turn (ts.opts.WorkspaceID == "") is unaffected — it falls straight
	// through to FindForAgent.
	//
	// Security: workspaces/<id>/ lives under $OMNIPUS_HOME, which the boot
	// Landlock policy already grants RWX — this changes only the working
	// directory and app-level path-validation root, never the kernel sandbox.
	// Sessions and private agent memory stay under agents/<id>/, never
	// re-rooted. The re-root target is deliberately workspaces/<id>/work/, NOT
	// workspaces/<id>/ itself: that directory also holds AGENT.md (the
	// workspace's Project Instructions, injected into every agent's prompt)
	// and the shared memory room (.omnipus/) — a generic write_file/edit_file
	// confined (via os.Root) to work/ cannot reach either, structurally, since
	// os.Root cannot open a path outside its own root. (An earlier version of
	// this re-rooted directly to workspaces/<id>/, leaving AGENT.md reachable —
	// pkg/tools/metadata_guard.go's app-level guard only recognizes the
	// agents/<id>/ layout and does not match workspaces/<id>/AGENT.md, so
	// nothing else was catching it.)
	// ADR-046 P1 (FR-007/008): execution is always workspace-scoped. An agent
	// that is not a member of ANY workspace's CoreTeam cannot execute at all —
	// no silent fallthrough to its own agent-home directory, no ambiguous
	// lexicographic guess. This refusal deliberately runs AFTER the
	// tools.WithWorkspaceID injection above (memory routing, FR-030) so that
	// separate signal is completely unaffected by this gate either way.
	//
	// resolveTurnWorkDirOrRefuse (workspace_reroot.go) is the SHARED gate —
	// external_dispatch.go's runExternalCLISubTurn calls the exact same
	// function so the membership refusal cannot diverge between the native
	// and external-cli dispatch paths again (a prior review found
	// runExternalCLISubTurn had its own, weaker copy that fell through to the
	// agent's private home directory instead of refusing).
	wsDir, wsErr := resolveTurnWorkDirOrRefuse(ts.agent.ID, ts.opts.WorkspaceID)
	if wsErr != nil {
		return turnResult{}, wsErr
	}
	turnCtx = tools.WithTurnWorkspaceDir(turnCtx, wsDir)

	// FR-7.5 / NFR-1: install a per-turn citation tracker so recall_memory can
	// report surfaced memories and the loop can emit op:cited counter events
	// when the LLM references them by ID/title. Nil for the main gateway agent
	// (no memory store); WithCitationTracker is a no-op in that case.
	citationTracker := newCitationTracker(ts.agent.ContextBuilder.Memory())
	turnCtx = tools.WithCitationTracker(turnCtx, citationTracker)

	al.registerActiveTurn(ts)
	// Execution order (LIFO defer — registered in reverse of desired run
	// order): clearActiveTurn runs FIRST, then finalizeStreamer, then
	// Finish(false) LAST.
	//
	// clearActiveTurn must run before finalizeStreamer, which sends the
	// "done" WS frame. IsAlive()/onCancelFinish are unaffected by
	// clearActiveTurn's timing — they're driven by ts.isFinished/
	// ts.cancelFired directly, never by activeTurnStates map membership — so
	// an earlier version of this ordering (clearActiveTurn registered
	// straight after Finish, i.e. running AFTER finalizeStreamer) was correct
	// for that reasoning but wrong in practice: TestWS_Cancel_OnlyInterruptsTargetSession
	// caught a real race. A client that receives "done" and immediately sends
	// "cancel" for the same session_id can have that cancel reach
	// handleCancel -> GetActiveTurnHookForSession before this goroutine's own
	// remaining defers run — a loopback WS round trip can beat two sequential
	// Go defer calls under scheduler contention. GetActiveTurnHookForSession
	// then still finds the (already-finished) turnState in the map,
	// ClaimCancel succeeds, and a spurious cancel_stage frame goes out for a
	// turn the client already knows is done. Deleting the map entry before
	// "done" is sent closes that window: any cancel arriving after closes on
	// nothing to claim.
	//
	// finalizeStreamer must still run BEFORE ts.Finish(false) — registered
	// here, between clearActiveTurn and Finish. finalizeStreamer is what
	// calls wsStreamer.Finalize(), which writes the assistant transcript
	// entry (FIX 1). Finish(false)'s onCancelFinish callback
	// (pkg/agent/cancel.go) is what writes the turn_canceled entry AND calls
	// MarkLastEntryTruncated to flag the assistant entry. Both depend on the
	// assistant entry already existing in transcript.jsonl:
	//   - MarkLastEntryTruncated's backward-walk finds nothing to flag if the
	//     assistant entry isn't there yet (silently succeeds as a no-op —
	//     Truncated never gets set).
	//   - The frontend's turn_canceled -> assistant-message replay
	//     correlation (chatTurnCanceledNoMatch) requires the assistant
	//     ReplayMessageFrame to have already arrived on the wire before the
	//     turn_canceled frame that references its TurnID — replay emits
	//     frames in transcript.jsonl's on-disk order, so if turn_canceled is
	//     written first, it replays first too, and the frontend's "find the
	//     existing message" lookup always misses.
	// Live-verified via a mid-stream cancel: transcript.jsonl showed user ->
	// turn_canceled -> assistant (wrong order) when finalizeStreamer ran
	// after Finish's callback; user -> assistant -> turn_canceled (correct)
	// with finalizeStreamer running first. closeOnce.Do inside Finish makes
	// repeated Finish calls safe — the cancel path may have already called
	// Finish(true); this deferred Finish(false) is then a no-op.
	defer ts.Finish(false)
	defer ts.finalizeStreamer(ctx)
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
	if requested := strings.TrimSpace(
		inboundMetadata(bus.InboundMessage{Metadata: ts.opts.Metadata}, "model_name"),
	); requested != "" {
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
				// The caller explicitly asked for `requested` (typically a model
				// picked from the composer's live catalog) and the switch could not
				// be applied — the turn is about to proceed on the agent's current
				// model instead. A backend-only WARN log is not enough: nothing else
				// tells the caller their selection was ignored, which is exactly the
				// "picking a model has no effect" failure mode. Mirror the
				// FR-001/FR-002 pattern already used for rate-limit/provider errors
				// (docs/internal/specs/phase-1-chat-model-and-errors.md): emit
				// EventKindError + persist a transcript entry so replay shows it
				// (appendErrorTranscript), AND push a live notification so the
				// CURRENT session learns immediately — the transcript write alone
				// only becomes visible after a reload.
				//
				// Wave 1 (ADR-051 §RD5 MAJ-003): sanitize the model_switch
				// message via the classifier so the model name does not leak
				// into the assistant-facing copy. The classifier recognizes
				// the "could not switch to model" shape via substring and
				// emits a generic message; raw stays in logger.WarnCF for
				// operator triage.
				switchFailMsg := fmt.Sprintf(
					"Could not switch to model %q: %s. This reply used %q instead.",
					requested, switchErr.Error(), ts.agent.Model,
				)
				switchLLM := TranslateLLMError(nil, switchFailMsg)
				al.emitEvent(
					EventKindError,
					ts.eventMeta("runTurn", "turn.error"),
					ErrorPayload{
						Stage: "model_switch", Code: string(switchLLM.Code),
						Message: switchLLM.Message, ChatID: ts.opts.ChatID,
					},
				)
				ts.appendErrorTranscript(EventKindError.String(), "model_switch", switchLLM.Message)
				// NB: no notification frame here — `model_switch_failed` is not a
				// contract NotificationFrame.notification_type, so the SPA's
				// inbound Zod validation would drop it. The EventKindError +
				// error-transcript record above is the surfacing; the aggregator
				// resolver fix (ResolveModelCfg step 4) means a composer-catalog
				// pick now resolves, so this branch only fires for a genuinely
				// unroutable model (no passthrough provider configured).
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

	// Site-1: initial assembly (CRITICAL 2 — error handled inside assembleMessages).
	messages := al.assembleMessages(
		turnCtx,
		ts,
		history,
		summary,
		ts.userMessage,
		ts.media,
		activeSkillNames(ts.agent, ts.opts),
	)

	cfg := al.GetConfig()
	maxMediaSize := cfg.Agents.Defaults.GetMaxMediaSize()
	// Step-5 offload target: the workspace work/ dir already resolved (and
	// MkdirAll'd) for this turn at resolveTurnWorkDirOrRefuse above. Passing it
	// as an offloadSink lets attachments no provider can present (e.g. AVIF/HEIC
	// with no decoder) be copied into work/ and surfaced as a filesystem path +
	// guidance instead of dying the turn (ADR-051 Rev 4 FR-020/020a/021).
	messages = resolveMediaRefsWithOffload(
		messages, turnMediaStore, maxMediaSize, ts.agent.Model,
		&offloadSink{workDir: wsDir}, turnCatalog, turnRefcounter,
		ts.opts.WorkspaceID,
	)

	if !ts.opts.NoHistory {
		toolDefs := ts.agent.Tools.ToProviderDefs()
		if isOverContextBudget(ts.agent.ContextWindow, messages, toolDefs, ts.agent.MaxTokens) {
			logger.WarnCF("agent", "Proactive window trim: context budget exceeded before LLM call",
				map[string]any{"session_key": ts.sessionKey})
			if compression, ok := al.windowTrim(ts.agent, ts.sessionKey); ok {
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
			// Site-2: post-proactive-trim assembly.
			newHistory := ts.agent.Sessions.GetHistory(ts.sessionKey)
			newSummary := ts.agent.Sessions.GetSummary(ts.sessionKey)
			messages = al.assembleMessages(
				turnCtx,
				ts,
				newHistory,
				newSummary,
				ts.userMessage,
				ts.media,
				activeSkillNames(ts.agent, ts.opts),
			)
			messages = resolveMediaRefsWithOffload(
				messages, turnMediaStore, maxMediaSize, ts.agent.Model,
				&offloadSink{workDir: wsDir}, turnCatalog, turnRefcounter,
				ts.opts.WorkspaceID,
			)
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
			return al.abortTurn(ts, "turn_loop", hardInterruptAbortReason)
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
			resolvedPending := resolveMediaRefsWithOffload(
				pendingMessages, turnMediaStore, maxMediaSize, activeModel,
				&offloadSink{workDir: wsDir}, turnCatalog, turnRefcounter,
				ts.opts.WorkspaceID,
			)
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

		// The unified `load_tool` infra tool is registration-gated, NOT policy-gated:
		// when compressed mode is on it must be callable by EVERY agent — including
		// deny-by-default agents (Ava/Mia/Ray) — or the model is shown `load_tool` in
		// its defs but its EXECUTION is denied, leaving every lazy tool permanently
		// unreachable. Force it into both the sent defs (policyFilteredTools) and
		// the execution-time policy snapshot (filterTimePolicyMap, consulted by
		// resolveToolPolicyAtExec) as "allow". This mirrors the defs force-include
		// in buildCompressedToolDefs at the authorization layer. (Found by live
		// validation: a deny-by-default agent called load_tool and the exec gate
		// denied it — reachability broke.)
		policyFilteredTools = ensureInfraToolsExecutable(
			ts.agent.Tools, policyFilteredTools, filterTimePolicyMap)

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
			if shouldAbort, abortMsg := al.recordSyntheticDeny(ts); shouldAbort {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts, "synthetic_error_floor", abortMsg)
			}
			// Fail the LLM call for this iteration by returning an error turn result.
			turnStatus = TurnEndStatusError
			return turnResult{status: TurnEndStatusError, finalContent: denyMsg}, dedupErr
		}

		var providerToolDefs []providers.ToolDefinition
		if cfg.Tools.Manifest.Compressed {
			providerToolDefs = al.buildCompressedToolDefs(ts, policyFilteredTools)
		} else {
			// Non-compressed defs path: strip manifest infra tools (load_tool)
			// before surfacing defs to the model. The unified resolver
			// (tools.EffectiveToolPolicy) force-allows infra UNCONDITIONALLY, so
			// load_tool is now present in policyFilteredTools even when
			// compression is off; but load_tool exists only to drive the
			// compressed manifest mechanism and has no function when compression is
			// off, so the model never sees it here regardless of what the agent's
			// tool-policy map resolves for it (see stripInfraToolDefs for the
			// mostly-deny vs. mostly-allow behavior note) (#438).
			providerToolDefs = tools.ToolsToProviderDefs(stripInfraToolDefs(policyFilteredTools))
		}

		// Native web search support
		_, hasWebSearch := ts.agent.Tools.Get("search_web")
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
			// Filter out client-side search_web tool
			filtered := make([]providers.ToolDefinition, 0, len(providerToolDefs))
			for _, td := range providerToolDefs {
				if td.Function.Name != "search_web" {
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
		// Re-inject the acting agent's current scratchpad as an ephemeral system
		// message so the checklist survives context compression and the agent
		// always sees its plan at the top of the turn. The note is NOT persisted
		// to history — it is rebuilt fresh each turn from the task store.
		if ts.agent != nil {
			if note := al.buildScratchpadNote(ts.agent.ID); note != "" && len(callMessages) > 0 {
				// Insert after callMessages[0] (the system prompt) so it immediately
				// follows the agent's identity, before the conversation history.
				injected := make([]providers.Message, 0, len(callMessages)+1)
				injected = append(injected, callMessages[0])
				injected = append(injected, providers.Message{Role: "system", Content: note})
				injected = append(injected, callMessages[1:]...)
				callMessages = injected
			}
			// Inject per-turn workspace instructions (AGENT.md) as an ephemeral
			// system message immediately after the system prompt. Call BEFORE
			// injectManifestNote so that the manifest note lands at [1] and
			// workspace instructions land at [2] in the final message array.
			// Empty/absent instructions are a no-op — zero behavioral change.
			callMessages = injectWorkspaceInstructions(callMessages, buildWorkspaceInstructionsNote(ts.opts.WorkspaceID))
			// Web-only: encourage Mermaid diagrams when the turn comes from the web
			// chat (the sole surface that renders them). Per-turn + surface-gated on
			// ts.channel — deliberately NOT in the cached system prompt, since one
			// agent serves multiple channels (see web_rendering_note.go).
			callMessages = injectWebRenderingNote(callMessages, buildWebRenderingNote(ts.channel))
			// Re-inject the compressed manifest of unloaded lazy tools as an ephemeral
			// system message. Like the scratchpad, it is rebuilt every turn (never
			// persisted) so it is never stale and not double-counted in the cached
			// system prompt. Injected only when Compressed is active and there are
			// unloaded lazy tools to list.
			if cfg.Tools.Manifest.Compressed {
				callMessages = injectManifestNote(callMessages, al.buildToolManifestNote(ts, policyFilteredTools))
			}
		}
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
				return al.abortTurn(ts, "before_llm", decision.Reason)
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
			// Normalize at the top of every provider call — initial and every
			// retry/recovery — so that timeout-recovery and context-overflow-recovery
			// paths (which rebuild callMessages via BuildMessages and continue back
			// here) are always normalized. The fast path is allocation-free on valid
			// histories, so per-call cost is negligible.
			messagesForCall = normalizeMessagesForProvider(messagesForCall)

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
					// FIX 5a/5c: stamp the TRUE per-turn producer and this turn's own
					// ID before any token can flow — see stampStreamerProducerAgentID
					// and stampStreamerTurnID's doc comments. stampStreamerParentSpawnCallID
					// additionally stamps this turn's delegation-nesting correlation
					// (empty for a root turn) so a delegate's own streamed final
					// response round-trips through Finalize with the same
					// ParentSpawnCallID its non-streaming siblings carry — see its
					// own doc comment.
					ts.stampStreamerProducerAgentID(streamer)
					ts.stampStreamerTurnID(streamer)
					ts.stampStreamerParentSpawnCallID(streamer)
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

		// synthesizeImageRejection restores the pre-classifier friendly path for
		// image-only capability/format rejections. Image-only failures are terminal:
		// stripping the image and retrying would silently answer a different prompt.
		// PDF or mixed-media requests continue through TryMediaDowngrade below.
		synthesizeImageRejection := func(pe *ProviderError, rejectionErr error) bool {
			// FIX 5: this is the IMAGE-only friendly-rejection path — it must
			// consult ts.imageRetryDone, the image-class guard, NOT
			// ts.mediaRetryDone (the PDF-class guard). turn.go's design
			// comment on the two fields is explicit that the guard was split
			// per-class precisely so a PDF-class downgrade earlier in the
			// SAME turn can never consume the image-class budget (or vice
			// versa). Reading the wrong guard here meant a PDF downgrade
			// earlier in the turn silently blocked this friendly synthesis
			// for a LATER, unrelated image rejection — it fell through to
			// the generic classifier-driven strip-retry instead.
			if ts.imageRetryDone.Load() || rejectionErr == nil {
				return false
			}

			rejectionText := rejectionErr.Error()
			if pe != nil && pe.Body != "" {
				rejectionText = pe.Body
			}
			if !isImageRejectionMessage(rejectionText) || isPDFRejectionMessage(rejectionText) {
				return false
			}

			// No media is allowed here for compatibility with capability errors
			// returned after compaction. When media is present, every block must be
			// an image; a PDF or any other block makes this a mixed-media request.
			for _, message := range callMessages {
				for _, mediaRef := range message.Media {
					if !startsWithCaseInsensitive(mediaRef, "data:image/") {
						return false
					}
				}
			}

			logger.WarnCF("agent", "model rejected image input — returning guidance instead of retrying without it",
				map[string]any{"agent_id": ts.agent.ID, "model": llmModel, "error": rejectionErr.Error()})
			response = &providers.LLMResponse{
				Content: fmt.Sprintf(
					"I can't view images with the current model (%s). To work with images, switch this "+
						"agent to a model that supports image input, then try again.",
					llmModel,
				),
			}
			err = nil
			ts.imageRetryDone.Store(true)
			ts.setLastProducedModel(ModelSyntheticImageRejection)
			logger.DebugCF("agent", "image-rejection synthesis stamped; llmModel retained in error log only",
				map[string]any{"agent_id": ts.agent.ID, "model": llmModel})
			return true
		}

		for retry := 0; retry <= maxRetries; retry++ {
			response, err = callLLM(callMessages, providerToolDefs)
			if err == nil {
				break
			}
			// Preserve the friendly image-only synthesis before the generic media
			// downgrade path. PDF and mixed-media failures deliberately fall through.
			pe := errorToProviderError(err)
			if synthesizeImageRejection(pe, err) {
				break
			}
			// Wave 1 (ADR-051 RD2): classifier-gated media downgrade-retry.
			// Replaces the prior inline substring "image input" strip path
			// (which only handled vision-capability errors and ran on every
			// retry iteration). The new helper:
			//   1. classifies pe via the shared classifier — only retries
			//      CodeMediaUnsupported (never content-policy/auth/unknown).
			//   2. hoists the per-turn guard onto ts.mediaRetryDone — the
			//      retry cannot fire twice in the same turn.
			//   3. handles both PDF and image media (PDF via
			//      downgradePDFMediaToText; image via stripRejectedImageMedia).
			if downgradeResult := TryMediaDowngrade(ts, callMessages, pe); downgradeResult.Applied {
				// FR-017a (Slice E / Wave 1b): the helper's verdict decides
				// the recorded turn classifier code. The classifier-primary
				// path always reports CodeMediaUnsupported; the
				// outcome-based fallback may report a different code (the
				// original classifier was inconclusive). Read the helper's
				// verdict via the typed result (Wave 1 TD-M8 — the bool
				// return was overloaded and lost the trigger; this commit
				// adds DowngradeTrigger + MediaClass so the warn-log and
				// the FR-017a relabel are both data-derived from the
				// helper, not from a re-classification at the call site).
				// The message fallback is err.Error() (not ""): pe is never
				// actually nil on this path (errorToProviderError only
				// returns nil for a nil err, and this call site is inside
				// the `err != nil` retry branch), but classifyByProviderError
				// falls back to the message when pe is nil — passing the
				// real error text keeps this call correct if that
				// invariant is ever loosened, instead of being a latent
				// no-op that silently classifies "" today.
				helperCode := classifyByProviderError(pe, err.Error())
				logger.WarnCF("agent",
					"provider rejected media input — retrying with downgraded media block",
					map[string]any{
						"agent_id":    ts.agent.ID,
						"model":       llmModel,
						"error":       err.Error(),
						"code":        string(helperCode),
						"trigger":     string(downgradeResult.Trigger),
						"media_class": string(downgradeResult.MediaClass),
					})
				response, err = callLLM(callMessages, providerToolDefs)
				if err == nil {
					// FR-017a success edge (Slice E / Wave 1b): when the
					// outcome-based fallback fired (Trigger ==
					// TriggerOutcomeFallback) AND the retry succeeded,
					// the recorded turn classifier verdict MUST be
					// relabeled to CodeMediaUnsupported — the classifier
					// now LABELS the outcome (per the ADR §4
					// "classify the outcome" contract), not just the
					// trigger. The classifier-primary path's helperCode
					// is already CodeMediaUnsupported so no relabel is
					// needed for that branch.
					if downgradeResult.Trigger == TriggerOutcomeFallback {
						ts.setOutcomeRelabel(CodeMediaUnsupported)
					}
					break
				}
			}
			if ts.hardAbortRequested() && errors.Is(err, context.Canceled) {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts, "llm_call", hardInterruptAbortReason)
			}

			// I3: if the FallbackChain already exhausted all candidates, don't retry
			// in the outer loop — the chain already tried everything. Break immediately
			// so the error surfaces to the caller without redundant delay.
			//
			// Exception: if every attempt was a transient mid-stream reset (http2
			// body closed, GOAWAY, connection reset, etc.) and no content was
			// streamed to the client yet, the chain can be retried whole — a fresh
			// connection will be attempted for each candidate. This is the primary
			// fix for "0 tokens" turns caused by HTTP/2 pooled-connection drops:
			// the FallbackChain marks candidates in cooldown and returns
			// FallbackExhaustedError even for a single-candidate config, bypassing
			// the normal ClassifyError → isTimeoutError retry path below.
			var exhaustedErr *providers.FallbackExhaustedError
			if errors.As(err, &exhaustedErr) {
				// Check whether every failed attempt was a transient stream reset.
				// Skipped-cooldown entries (Skipped==true) are not counted as
				// streaming failures; we only need all *attempted* calls to have
				// been transient drops.
				allTransient := len(exhaustedErr.Attempts) > 0
				for _, a := range exhaustedErr.Attempts {
					if a.Skipped {
						continue // cooldown skip — not a new attempt, ignore
					}
					if !isTransientStreamError(a.Error) {
						allTransient = false
						break
					}
				}
				if allTransient && retry < maxRetries {
					if ts.hardAbortRequested() {
						break
					}
					// Apply the same "don't retry if partial content was already
					// streamed" guard as the isTimeoutError path to avoid duplicating
					// text in an in-progress SPA bubble.
					if sc, ok := ts.lastStreamer.(interface{ StreamedContentLen() int }); ok && sc.StreamedContentLen() > 0 {
						logger.WarnCF("agent", "Transient stream reset (fallback exhausted) after partial stream; not retrying to avoid duplicated text", map[string]any{
							"agent_id":  ts.agent.ID,
							"iteration": iteration,
							"streamed":  sc.StreamedContentLen(),
							"error":     err.Error(),
						})
						break
					}
					// Backoff: 500ms × 2^retry, capped at 4s (shorter than the
					// timeout-retry backoff — streaming resets are transient and
					// resolve quickly on a fresh connection).
					backoff := 500 * time.Millisecond * (1 << uint(retry))
					if backoff > 4*time.Second {
						backoff = 4 * time.Second
					}
					logger.WarnCF("agent", "Transient streaming reset (fallback exhausted) — retrying LLM call", map[string]any{
						"agent_id": ts.agent.ID,
						"model":    llmModel,
						"retry":    retry,
						"backoff":  backoff.String(),
						"error":    err.Error(),
					})
					al.emitEvent(
						EventKindLLMRetry,
						ts.eventMeta("runTurn", "turn.llm.retry"),
						LLMRetryPayload{
							Attempt:    retry + 1,
							MaxRetries: maxRetries,
							Reason:     "streaming_reset",
							Error:      err.Error(),
							Backoff:    backoff,
						},
					)
					if sleepErr := sleepWithContext(turnCtx, backoff); sleepErr != nil {
						if ts.hardAbortRequested() {
							turnStatus = TurnEndStatusAborted
							return al.abortTurn(ts, "llm_retry_backoff", hardInterruptAbortReason)
						}
						err = sleepErr
						break
					}
					continue
				}
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
				// ClassifyError returned nil: the error is not recognizable as a
				// provider-level condition. Before giving up, check whether it is a
				// transient mid-stream reset (e.g. a GOAWAY frame or a network drop
				// that is wrapped by layers ClassifyError does not unwrap). If so,
				// treat it as a timeout-equivalent so the isTimeoutError retry path
				// below fires, rather than breaking immediately with 0 tokens.
				if isTransientStreamError(err) {
					isTimeoutError = true
				} else {
					// Genuinely unknown error. Don't retry.
					break
				}
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
						// windowTrim has three possible outcomes here:
						//  1. ok=true — a real eviction occurred, either window Turns were
						//     dropped or dropping the active recall span alone (FR-019)
						//     brought the window back under budget — rebuild messages and
						//     retry (this branch).
						//  2. ok=false, NothingToTrim=true — nothing was eligible to evict
						//     (e.g. a fresh turn with no compressible history) — not a
						//     failure; fall through to backoff+retry unchanged.
						//  3. ok=false, NothingToTrim=false — TruncateHistory was attempted
						//     but the window genuinely did not shrink — abandon the retry.
						compression, ok := al.windowTrim(ts.agent, ts.sessionKey)
						if ok {
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
							// Site-3: post-timeout-trim assembly.
							newHistory := ts.agent.Sessions.GetHistory(ts.sessionKey)
							newSummary := ts.agent.Sessions.GetSummary(ts.sessionKey)
							messages = al.assembleMessages(turnCtx, ts, newHistory, newSummary, "", nil, activeSkillNames(ts.agent, ts.opts))
							callMessages = messages
							if gracefulTerminal {
								callMessages = append(append([]providers.Message(nil), messages...), ts.interruptHintMessage())
							}
						} else if compression.NothingToTrim {
							// Nothing eligible to evict (e.g. a fresh turn with a
							// single-message window, no compressible history yet).
							// This is not a compaction failure — the error that got us
							// here was a transient network/streaming reset (isTimeoutError),
							// not a genuine context-overflow rejection from the provider.
							// Abandoning the whole retry here would defeat the timeout-
							// retry path for any turn that happens to sit near the
							// context-budget edge with little/no history. Fall through to
							// the backoff-and-retry below with the existing messages
							// unchanged — the retried call may simply succeed.
							logger.DebugCF("agent", "Window trim skipped during timeout recovery: nothing eligible to evict; proceeding with retry",
								map[string]any{"agent_id": ts.agent.ID, "iteration": iteration})
						} else {
							// Trim was attempted against real compressible history and
							// genuinely failed (e.g. TruncateHistory could not shrink the
							// window). Unlike the isContextError path below, though, the
							// error that got us HERE is a transient transport drop
							// (streaming reset / GOAWAY), and the trim was triggered only
							// by the isOverContextBudget check above — a conservative
							// proactive heuristic (75% of the window), NOT a hard provider
							// context-overflow rejection. So the window is not necessarily
							// over the real limit, and canceling the retry would abandon a
							// call that often just succeeds on a second try (UAT: an 11th
							// tool call tipped the 75% heuristic mid-task and a break here
							// truncated a task that completed fine on retry). Fall through
							// to the backoff-and-retry below rather than returning partial.
							logger.WarnCF("agent", "Window trim failed during timeout recovery; proceeding to retry without compaction",
								map[string]any{"agent_id": ts.agent.ID, "iteration": iteration})
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
						return al.abortTurn(ts, "llm_timeout_backoff", hardInterruptAbortReason)
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

				if compression, ok := al.windowTrim(ts.agent, ts.sessionKey); ok {
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
					// C3: windowTrim returned ok=false (nothing to trim). Mark the
					// flag so the NEXT retry attempt will break rather than burning more
					// budget on identical data. We still allow this single retry through
					// because the provider might succeed without context reduction.
					contextCompressionFailed = true
					logger.WarnCF("agent", "Window trim failed during context overflow recovery; will not retry further",
						map[string]any{"agent_id": ts.agent.ID, "iteration": iteration})
				}

				// Site-4: post-context-overflow-trim assembly.
				newHistory := ts.agent.Sessions.GetHistory(ts.sessionKey)
				newSummary := ts.agent.Sessions.GetSummary(ts.sessionKey)
				messages = al.assembleMessages(turnCtx, ts, newHistory, newSummary, "", nil, activeSkillNames(ts.agent, ts.opts))
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
		}
		if err != nil {
			turnStatus = TurnEndStatusError
			// Wave 1 (error-provenance hardening, ADR-051 §RD5 CRIT-001):
			// never emit raw err.Error() to the assistant-facing bus /
			// transcript. Build a *ProviderError from the wrapped chain
			// (best-effort — falls back to substring matching on err.Error()
			// when no FailoverError is in the chain), route through the
			// shared translateLLMError, and surface the generic message
			// instead of the raw provider text. Raw stays in logger.ErrorCF
			// + the wrapped fmt.Errorf return for operator triage.
			pe := errorToProviderError(err)
			llm := TranslateLLMError(pe, err.Error())

			// FR-017a: outcome-based relabel overrides the classifier's code
			// for both the live WS frame AND the transcript write. The relabel
			// applies to any error emitted by this turn after the outcome-based
			// strip-retry succeeded.
			if ts.outcomeRelabel != "" {
				llm.Code = ts.outcomeRelabel
				llm.Message = UserMessageForCode(ts.outcomeRelabel)
			}

			al.emitEvent(
				EventKindError,
				ts.eventMeta("runTurn", "turn.error"),
				ErrorPayload{
					Stage: "llm", ChatID: ts.opts.ChatID,
					Code:          string(llm.Code),
					Message:       llm.Message,
					ProviderError: pe,
				},
			)
			// FR-002: persist the translated provider error to the transcript
			// (write choke point — ADR-051 §RD5). pe threaded through so the
			// classifier sees status/body, not the stringified err.
			ts.appendErrorTranscript(
				EventKindError.String(), "runTurn",
				llm.Message,
				pe,
			)
			logger.ErrorCF("agent", "LLM call failed",
				map[string]any{
					"agent_id":  ts.agent.ID,
					"iteration": iteration,
					"model":     llmModel,
					"error":     err.Error(),
					"code":      string(llm.Code),
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
				return al.abortTurn(ts, "after_llm", decision.Reason)
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
			// Accumulate cache token split for transcript entry (Wave 1 token tracking).
			ts.AddTurnCacheStats(response.Usage.CacheReadTokens, response.Usage.CacheWriteTokens)
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
			// FR-7.5/NFR-1: scan the assistant's final answer for references to
			// memories recalled earlier this turn and emit op:cited events.
			if citationTracker != nil {
				citationTracker.EmitCitations(responseContent)
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
				// Wave 1 (error-provenance hardening): translate via the
				// shared classifier (CRIT-001). Never surface raw err.Error()
				// to the assistant / bus / transcript.
				pe := errorToProviderError(err)
				llm := TranslateLLMError(pe, err.Error())

				// FR-017a: outcome-based relabel override for this turn.
				if ts.outcomeRelabel != "" {
					llm.Code = ts.outcomeRelabel
					llm.Message = UserMessageForCode(ts.outcomeRelabel)
				}

				al.emitEvent(
					EventKindError,
					ts.eventMeta("runTurn", "turn.error"),
					ErrorPayload{Stage: "llm_empty_retry", Code: string(llm.Code), Message: llm.Message, ProviderError: pe, ChatID: ts.opts.ChatID},
				)
				// FR-002: persist this provider error to the transcript (write
				// choke point).
				ts.appendErrorTranscript(
					EventKindError.String(), "runTurn",
					llm.Message,
					pe,
				)
				return turnResult{}, fmt.Errorf("LLM call failed during empty-response retry: %w", err)
			}
			if strings.TrimSpace(responseContent) == "" {
				responseContent = defaultResponse
				ts.markTurnFailed()
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

		// FR-7.5/NFR-1: the narration text accompanying this round of tool
		// calls may reference memories recalled in a prior iteration. Scan it
		// for citations before executing the tools.
		if citationTracker != nil {
			citationTracker.EmitCitations(response.Content)
		}

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
				return al.abortTurn(ts, "tool_loop", hardInterruptAbortReason)
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
					return al.abortTurn(ts, "before_tool", decision.Reason)
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
					// FR-017 audit-coverage fix: an approval hook (e.g. the gateway's
					// wsApprovalHook) can reject a tool for policy reasons BEFORE the
					// loop reaches the TOCTOU re-check at exec time. That branch only
					// `continue`s, so without this call a denied tool on a WS/CLI turn
					// would write NO attributed audit entry — the most common security
					// event (a policy-denied tool) would be invisible in the audit log.
					// emitPolicyDenyAudit stamps User: ts.auditUser(), so WS-originated
					// turns carry the acting principal (e.g. "cli") and channel/non-
					// gateway turns keep User empty. This is the ONLY tool-deny branch
					// that did not already audit; the TOCTOU (deny), ask-auto-deny, and
					// ask-human-deny branches below each emit their own entry, so there
					// is no double-audit.
					denyReason := approval.Reason
					if denyReason == "" {
						denyReason = "tool execution denied by approval hook"
					}
					al.emitPolicyDenyAudit(ts, toolName, "ask", "approval_hook_deny: "+denyReason)
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
				if shouldAbort, abortMsg := al.recordSyntheticDeny(ts); shouldAbort {
					turnStatus = TurnEndStatusAborted
					return al.abortTurn(ts, "synthetic_error_floor", abortMsg)
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
					if shouldAbort, abortMsg := al.recordSyntheticDeny(ts); shouldAbort {
						turnStatus = TurnEndStatusAborted
						return al.abortTurn(ts, "synthetic_error_floor", abortMsg)
					}
					continue
				}
				// ask-policy: consult the session-scoped "Always Allow" grant
				// store first (ADR-036 §3.4 — the sole grant-consultation point
				// now that the legacy WS-frame gate, wsApprovalHook, has been
				// retired), then fall through to interactive human approval
				// (FR-011) only when no grant is on file.
				approved, denialReason := al.CheckGrantOrRequestApproval(
					turnCtx, ts.transcriptSessionID, ts.agentID, toolName, tc.ID, ts.turnID, toolArgs,
				)
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
					if shouldAbort, abortMsg := al.recordSyntheticDeny(ts); shouldAbort {
						turnStatus = TurnEndStatusAborted
						return al.abortTurn(ts, "synthetic_error_floor", abortMsg)
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

			// Per-channel tool feedback routing (agent-form spec §3.3 / F-01):
			// only messaging channels emit standalone tool-call messages; webchat,
			// internal channels (system/cli/subagent), cron schedules, and empty
			// channels suppress feedback because the UI already renders tool calls
			// inline or because the channel has no human recipient.
			if cfg.Agents.Defaults.IsToolFeedbackEnabled() &&
				!ts.opts.SuppressToolFeedback &&
				isMessagingChannel(ts.channel) {
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
				// mirroring the synchronous tool execution path. This stays a
				// separate concern from AsyncNotifier (FR-N2, async-notifier-spec.md)
				// — it happens regardless of whether ContentForLLM() also triggers
				// a new turn below.
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

				// Determine content for the agent loop (ForLLM or error). Nothing to
				// relay back into the conversation — skip AsyncNotifier entirely
				// rather than publish an empty follow-up (Notify's own contract
				// permits empty Content, e.g. a silent kill, but this call site
				// chooses not to invoke it at all here, preserving today's
				// skip-when-empty behavior exactly).
				content := result.ContentForLLM()
				if content == "" {
					return
				}

				// AsyncNotifier.Notify (async-notifier-spec.md) now owns
				// sensitive-data filtering, truncation, EventKindFollowUpQueued
				// emission, and the inbound bus publish that used to be inlined
				// here. The EventMeta carried via context preserves today's
				// TurnID/SessionKey/Iteration on the emitted event byte-for-byte.
				notifyCtx := withAsyncNotifyEventMeta(
					context.Background(),
					ts.scope.meta(toolIteration, "runTurn", "turn.follow_up.queued"),
				)
				if notifyErr := al.asyncNotifier.Notify(notifyCtx, AsyncNotifyEvent{
					Channel: ts.channel,
					ChatID:  ts.chatID,
					AgentID: ts.agent.ID,
					// FIX 5d: thread the originating turn's transcript binding
					// through so the reconstructed turn persists into the SAME
					// session, independent of whether a live WS connection is
					// still open when the async result lands.
					TranscriptSessionID: ts.transcriptSessionID,
					SourceKind:          asyncToolName,
					Content:             content,
				}); notifyErr != nil {
					logger.ErrorCF("agent", "Failed to publish async tool result; result permanently lost",
						map[string]any{"tool": asyncToolName, "channel": ts.channel, "error": notifyErr.Error()})
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
			// Also expose it via the pkg/tools-level accessor (W2): DelegateTool
			// cannot see the agent-package-private spawnToolCallIDKey above, so
			// it reads its OWN call ID this way at task-creation time to record
			// the correlation anchor a spawned child sub-turn's transcript
			// entries will carry back as ParentSpawnCallID.
			execCtx = tools.WithToolCallID(execCtx, tc.ID)
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
				return al.abortTurn(ts, "after_tool_exec", hardInterruptAbortReason)
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
					return al.abortTurn(ts, "after_tool", decision.Reason)
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
						if _, meta, err := turnMediaStore.ResolveWithMetaOpts(ref, media.ResolveOpts{}); err == nil {
							part.Filename = meta.Filename
							part.ContentType = meta.ContentType
							part.Type = inferMediaType(meta.Filename, meta.ContentType)
						}
					}
					parts = append(parts, part)
				}
				outboundMedia := bus.OutboundMediaMessage{
					Channel: ts.channel,
					ChatID:  ts.chatID,
					// FIX 1: workspace-scoped media resolution (channels'
					// store.ResolveWithCallerWorkspace) was silently degrading
					// to the private/global room for every channel send
					// because WorkspaceID was never set here. ts.opts.WorkspaceID
					// is the authoritative source — it is what turnCtx itself
					// was populated with via tools.WithWorkspaceID above (see
					// "Inject the workspace ID" a few hundred lines up in this
					// function). Deliberately read directly from ts.opts rather
					// than tools.ToolWorkspaceID(ctx): `ctx` here is runTurn's
					// ORIGINAL parameter, not turnCtx — WithWorkspaceID was
					// only ever applied to the derived turnCtx (and its
					// children, e.g. execCtx), never back-propagated onto the
					// `ctx` variable, so tools.ToolWorkspaceID(ctx) would
					// always read back "". ts.opts.WorkspaceID carries the
					// exact same value turnCtx was stamped with and needs no
					// context-plumbing assumptions to stay correct.
					WorkspaceID: ts.opts.WorkspaceID,
					SessionID:   ts.transcriptSessionID,
					Parts:       parts,
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
						User:     ts.auditUser(), // FR-017
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
			//
			// ADR-051 Rev 4 Gap 4: the inline-attach site must normalize
			// non-universal image MIMEs (SVG / AVIF / HEIC / HEIF / ICO)
			// before building the data URL — providers 400 on image/svg+xml
			// blocks and the pure-Go decoder set cannot normalize the rest,
			// and the tool-result message is persisted into session
			// history at loop.go:8442-8443, so a bad MIME would poison
			// every subsequent turn. attachToolResultMedia owns that
			// guard; the artifact tag at loop.go:8246 is the path-based
			// fallback hook for the rare "rasterize failed" case.
			if len(toolResult.Media) > 0 && turnMediaStore != nil {
				attachToolResultMedia(&toolResultMsg, toolResult.Media, turnMediaStore, maxMediaSize)
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
			switch {
			case toolResult.Interrupted:
				// Finding F (A-I4 round 5): a synchronous delegate/spawn call
				// whose child sub-turn was interrupted by a parent-turn
				// cancellation — see pkg/agent/subturn.go's spawnSubTurn
				// cleanup defer, the single source of truth for this
				// classification (ToolResult.Interrupted's doc comment).
				// Persisting "interrupted" here — rather than folding it into
				// the generic "error" case below — is what lets a session
				// reload's subagent_end frame (pkg/gateway/replay.go reads
				// this exact tc.Status back) show the same terminal status
				// the live WS stream already showed, instead of "failed"
				// (SubagentEndFrame.yaml's status enum explicitly supports
				// "interrupted" for this). The OUTER tool_call_result frame
				// for this same call is unaffected — replay.go clamps any
				// non-success tc.Status down to "error" for that stricter,
				// binary wire enum, matching toolResult.IsError (still true
				// here) and today's unchanged live behavior for the outer
				// badge.
				tcStatus = "interrupted"
			case toolResult.IsError:
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
						if _, meta, err := turnMediaStore.ResolveWithMetaOpts(ref, media.ResolveOpts{}); err == nil {
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
				// Persist the tool's human-readable result text ALONGSIDE the
				// media descriptors. Previously only {media} was stored, so a
				// media-bearing tool's text vanished on reload — e.g.
				// browser_screenshot's "Current page URL: …" header showed live
				// but the reloaded-from-history view had only {media:[…]}.
				// buildMediaFrame still re-emits from Result["media"]; the
				// added "text" key is what the replayed tool card renders.
				result := map[string]any{"media": descs}
				if resultText := strings.TrimSpace(contentForLLM); resultText != "" {
					result["text"] = resultText
				}
				tcRecord.Result = result
			} else if r := buildSyncDelegateResult(toolName, contentForLLM, toolResult.IsError, toolResult.Async); r != nil {
				// W4 (sync path): spawnSubTurn's async result-persistence defer
				// (subturn.go) no-ops for SYNCHRONOUS delegation — it runs before
				// this record exists and only retries when cfg.Async — so this
				// write is the sync delegate tool_call's FINAL persisted state.
				// Populate Result with the same {"text":…}(+"error") shape the
				// async defer produces, so a reloaded sync delegation shows what
				// the delegate produced (matching the live WS stream and the
				// async path) instead of an empty result. No-op for non-delegate
				// tools AND for async delegation (buildSyncDelegateResult returns
				// nil — async is owned by the defer, never persisted here),
				// preserving prior behavior for every other case. See
				// delegate_result.go.
				tcRecord.Result = r
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
		return al.abortTurn(ts, "turn_finalize", hardInterruptAbortReason)
	}

	if finalContent == "" {
		if ts.currentIteration() >= ts.agent.MaxIterations && ts.agent.MaxIterations > 0 {
			// Genuine failure: tool-iteration ceiling hit without a final response.
			// markTurnFailed so DoneStats.TurnFailed=true reaches the done frame.
			finalContent = toolLimitResponse
			ts.markTurnFailed()
		} else {
			// The engine fell through without an LLM response and uses the
			// caller-supplied DefaultResponse as the content.  Only mark as
			// failed when the caller passed the engine's own error sentinel
			// (defaultResponse) — a caller-supplied success string such as
			// "Background task completed." (heartbeat/system path) must NOT be
			// flagged as a failed turn.
			finalContent = ts.opts.DefaultResponse
			if ts.opts.DefaultResponse == defaultResponse {
				ts.markTurnFailed()
			}
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
			// Wave 1: never surface raw err.Error() (session-save is a
			// local I/O error, not a provider error, but the same
			// invariant holds — the classifier emits a generic copy).
			saveLLM := TranslateLLMError(nil, err.Error())
			al.emitEvent(
				EventKindError,
				ts.eventMeta("runTurn", "turn.error"),
				ErrorPayload{
					Stage: "session_save", ChatID: ts.opts.ChatID,
					Code: string(saveLLM.Code), Message: saveLLM.Message,
				},
			)
			// US-1: persist the session-save failure to the JSONL
			// transcript so the replay path re-renders it after reload (see
			// appendErrorTranscript docstring).
			ts.appendErrorTranscript(
				EventKindError.String(), "runTurn",
				saveLLM.Message,
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
		turnFailed:   ts.turnFailed,
	}, nil
}

// hardInterruptAbortReason is the abort reason used when a turn is
// hard-aborted via ts.requestHardAbort() (InterruptHard/InterruptSessionHard,
// reached from the turn loop's ts.hardAbortRequested() checks) rather than by
// a hook decision or the synthetic-error floor, neither of which have a more
// specific reason string available at the call site.
//
// abortTurn treats this exact reason string as the signal for a clean,
// intentional stop rather than a failure needing a surfaced error (see
// abortTurn's doc comment). Every production path that reaches it funnels
// through RequestCancel's escalation timer (pkg/agent/cancel.go) calling
// InterruptSessionHard — this includes both a live user canceling their own
// turn AND pkg/gateway/schedules.go's watchDeadline force-aborting a scheduled
// run once its deadline expires. ProcessScheduled (below) independently
// re-derives a real error for the deadline case from ctx.Err(), since a
// headless scheduled run has no live user to "already know" the run stopped
// early — see ProcessScheduled's comment.
const hardInterruptAbortReason = "turn canceled by hard interrupt request"

// abortTurn finalizes a hard-aborted turn. It differentiates two cases by
// reason string, because they need opposite treatment: one is a successful
// user action that should end silently, the other is a failure the user
// must be told about.
//
//   - reason == hardInterruptAbortReason (the shared constant every
//     hardAbortRequested()-gated call site passes): a clean, user-initiated
//     cancel (InterruptHard / InterruptSessionHard / the turn loop's
//     ts.hardAbortRequested() checks) — an intentional, successful action
//     the caller already knows about, not a failure. Returns
//     turnResult{status: Aborted} with a nil error and skips the error
//     event/transcript entry entirely. runAgentLoop's
//     `if result.status == TurnEndStatusAborted { return "", nil }` branch
//     then does the normal silent unwind.
//     TestAgentLoop_InterruptHard_RestoresSession asserts this: a
//     hard-interrupted turn restores the session cleanly with a nil error.
//
//   - any other reason (the synthetic-error floor's abortMsg, or a hook's
//     decision.Reason): a system-initiated abort — e.g. the repeated-policy-
//     denial floor or a hook's HookActionHardAbort decision. Synthesizes a
//     real, non-nil error carrying stage + reason, mirroring
//     hookAbortError's shape; emits an error event; and appends it to the
//     transcript so the user (and replay) learn why the turn ended.
//     runAgentLoop's existing `if err != nil { return "", err }` branch
//     propagates it, and session_worker.go's processTurn turns it into the
//     terminal user-facing frame every channel already knows how to render
//     (rather than silently dropping the user with no explanation).
func (al *AgentLoop) abortTurn(ts *turnState, stage, reason string) (turnResult, error) {
	ts.setPhase(TurnPhaseAborted)
	if !ts.opts.NoHistory {
		if err := ts.restoreSession(ts.agent); err != nil {
			// Wave 1: never surface raw err.Error() — route through the
			// shared classifier (CRIT-001). Local I/O errors that aren't
			// provider-shaped still go through the same generic-message
			// path for consistency.
			restoreLLM := TranslateLLMError(nil, err.Error())
			al.emitEvent(
				EventKindError,
				ts.eventMeta("abortTurn", "turn.error"),
				ErrorPayload{
					Stage:   "session_restore",
					Code:    string(restoreLLM.Code),
					Message: restoreLLM.Message,
					ChatID:  ts.opts.ChatID,
				},
			)
			return turnResult{}, err
		}
	}

	// Case 1: user-initiated hard interrupt/cancel — a successful, intentional
	// action, not a failure. No error event, no transcript entry, nil error —
	// identical to abortTurn's behavior before 499b569f for this specific case.
	if reason == hardInterruptAbortReason {
		return turnResult{status: TurnEndStatusAborted}, nil
	}

	// Case 2: system-initiated abort (policy/hook decision or the
	// synthetic-error floor) — synthesize a real, surfaced error.
	if reason == "" {
		reason = "no reason provided"
	}
	err := fmt.Errorf("turn aborted during %s: %s", stage, reason)
	// Wave 2 (BLOCK 2 / IMPORTANT 1): system-initiated aborts are
	// operator-shaped; preserve the original reason verbatim in both the
	// returned error AND the event payload (a user/operator needs to see
	// the actionable signal: which policy, which synthetic-floor count,
	// which hook reason). The classifier's generic copy is the
	// fall-through for the LIVE wire when the caller did not produce a
	// curated message; here the caller did. The typed code stamps the
	// EventPayload so the SPA can render the right banner.
	abortLLM := TranslateLLMError(nil, err.Error())
	presented := err.Error()
	if abortLLM.Code != CodeUnknown {
		// The classifier recognized a provider-shaped signal in the abort
		// reason; use the sanitized generic copy instead of the raw text.
		presented = abortLLM.Message
	}
	al.emitEvent(
		EventKindError,
		ts.eventMeta("abortTurn", "turn.error"),
		ErrorPayload{
			Stage:   stage,
			Code:    string(abortLLM.Code),
			Message: presented,
			ChatID:  ts.opts.ChatID,
		},
	)
	ts.appendErrorTranscript(EventKindError.String(), stage, presented)
	return turnResult{status: TurnEndStatusAborted}, err
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
		User:      ts.auditUser(), // FR-017
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

// isTransientStreamError reports whether err is a transient mid-stream
// provider reset that is safe to retry from scratch. These errors arise when
// an upstream HTTP/2 connection is recycled, GOAWAY'd, or dropped while the
// scanner is still reading SSE chunks — they are NOT application-level
// rejections (4xx, auth, context-overflow) and are NOT a clean end-of-stream.
//
// The set of matched strings is intentionally tight to avoid false positives:
//   - "streaming read error:" — the prefix openai_compat/provider.go wraps
//     around scanner.Err() when the body is closed mid-SSE-parse
//   - "http2: response body closed" — Go's net/http sentinel when the HTTP/2
//     body is closed concurrently (e.g. context cancellation or server RST)
//   - "http2: server sent goaway" — GoAwayError from net/http; server reset the
//     connection with GOAWAY before/during the response (INTERNAL_ERROR, etc.)
//   - "http2: transport received server's graceful shutdown goaway" — graceful
//     GOAWAY from a load-balancer recycling the server-side connection pool
//   - "stream error:" — http2StreamError.Error() prefix ("stream error: stream
//     ID N; INTERNAL_ERROR"); note: "stream" also appears in "streaming read
//     error:" above, but they are distinct patterns
//   - "connection reset by peer", "unexpected eof", "broken pipe" — TCP-level
//     transport resets that appear when the OS closes the connection
//
// This helper is used in the retry loop (pkg/agent/loop.go) to inline-retry
// drops that are not yet caught by ClassifyError (e.g., when wrapped inside
// a FallbackExhaustedError or when ClassifyError returns nil for an unknown
// wrapping). It is intentionally a superset of connectionDropPatterns so that
// it catches wrapping layers such as "fallback: unclassified error: ...".
func isTransientStreamError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	for _, pat := range []string{
		"streaming read error:",
		"http2: response body closed",
		"http2: server sent goaway",
		"http2: transport received server's graceful shutdown goaway",
		"stream error:",
		"connection reset by peer",
		"unexpected eof",
		"broken pipe",
		"use of closed network connection",
		"server closed idle connection",
		"connection closed",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
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
	// NothingToTrim is set (alongside ok=false) when windowTrim declined to
	// run because the live window had nothing eligible to evict (e.g. a
	// fresh turn with a single-message window). Callers that treat ok=false
	// as "compaction failed, abandon retry" should check this flag first: a
	// no-op trim is not a failure of the compaction mechanism and should
	// not, on its own, be grounds for giving up on a retry triggered by an
	// unrelated transient error.
	//
	// windowTrim's other ok=false path — TruncateHistory was attempted but
	// the window did not actually shrink — is a genuine failure and leaves
	// this flag unset; the window is still over budget in that case, so
	// retrying against it is unlikely to help.
	//
	// A third windowTrim outcome exists but is deliberately NOT reported
	// through this flag: dropping the active recall span alone (FR-019) can
	// bring the window back under budget without evicting any window Turns.
	// That is a real, successful eviction, so windowTrim reports it the same
	// way as a normal window eviction — ok=true with DroppedMessages==0 —
	// rather than ok=false+NothingToTrim, so callers rebuild the assembled
	// messages (dropping the now-stale recall-span content) before retrying.
	NothingToTrim bool
}

// assembleMessages is the single consistent helper that reads the archive,
// builds the breadcrumb, splices the recall span, and calls BuildMessages —
// deduplicating the four former call sites (CRITICAL 2).
//
// It reads the archive under ONE consistent snapshot so the breadcrumb and
// the window see the same archive state (avoids the turn-number race). On a
// ReadArchive error it logs at ERROR with a stable error id and emits a
// FALLBACK breadcrumb stub so the recall path stays discoverable.
//
// Callers pass fresh history+summary (post-trim when called after windowTrim),
// the current user message + media, and active skill names. The recall span is
// read from al.recallSpans for the given sessionKey.
func (al *AgentLoop) assembleMessages(
	ctx context.Context,
	ts *turnState,
	history []providers.Message,
	summary, userMsg string,
	media []string,
	skillNames []string,
) []providers.Message {
	archive, archErr := ts.agent.Sessions.ReadArchive(ctx, ts.sessionKey)
	var breadcrumb string
	if archErr != nil {
		logger.ErrorCF("agent", "archive-read-error: could not read archive for breadcrumb; recall may be impaired",
			map[string]any{
				"session_key": ts.sessionKey,
				"error":       archErr.Error(),
			})
		// Fallback stub so the model knows earlier turns exist and how to page them.
		breadcrumb = "## Earlier in this conversation\n" +
			"Earlier turns exist but could not be indexed due to a storage error. " +
			"Use the recall_conversation tool with a turn_range to retrieve them."
	} else {
		breadcrumb = buildBreadcrumb(archive, history, breadcrumbTokenCap)
	}
	// Review B3: a task's TASK_STATUS/TASK_SUMMARY marker instruction
	// (buildPrompt, task_executor.go) lives only in the task's first user
	// turn — a long, tool-heavy task run can trip windowTrim (ADR-028) and
	// evict that turn entirely, silently dropping the instruction for the
	// rest of the run. Piggyback a terse reminder onto the SAME breadcrumb
	// block that already fires exactly when (and only when) something has
	// been evicted (breadcrumb != ""), scoped to task runs only
	// (ts.opts.IsTaskRun) — this re-surfaces the instruction precisely when
	// it risks having been evicted, at near-zero token cost, without a
	// second parallel injection mechanism.
	if ts.opts.IsTaskRun && breadcrumb != "" {
		breadcrumb += fmt.Sprintf(
			"\n\nReminder (task run): end your final message with `%s: success` or `%s: failure` (ADR-043).",
			taskStatusLabel, taskStatusLabel,
		)
	}
	spanMsgs := al.activeRecallSpan(ts.sessionKey).Messages()
	return ts.agent.ContextBuilder.BuildMessages(
		history,
		summary,
		userMsg,
		media,
		ts.opts.WorkspaceID,
		ts.channel,
		ts.chatID,
		ts.opts.SenderID,
		ts.opts.SenderDisplayName,
		breadcrumb,
		spanMsgs,
		skillNames...,
	)
}

// evictionTotal counts successful windowTrim evictions (FR-018,
// context_eviction_total). Exported for test assertions.
var evictionTotal atomic.Int64

// skipAdvanceTotal counts TruncateHistory calls that actually advanced Skip
// (FR-018, context_skip_advance_total). Exported for test assertions.
var skipAdvanceTotal atomic.Int64

// windowTrim replaces forceCompression (FR-001/002/003/004). It evicts the
// oldest whole Turn(s) from the live window by advancing meta.Skip until the
// assembled token budget fits, deleting zero bytes from disk.
//
// Algorithm (FR-001):
//  1. Read the current post-Skip live window via GetHistory.
//  2. If len(window) <= 2, nothing to trim — return false.
//  3. Drop the recall span first (FR-019) if active and over budget; re-check.
//  4. Build the 5%-headroom budget target.
//  5. Walk Turn boundaries from oldest-first; pick the smallest b such that
//     window[b:] + toolDefs + recallSpanTokens fits in budget.
//  6. keepLast = len(window) - b; call TruncateHistory (window-relative).
//  7. FR-003 floor: if no boundary fits (single huge Turn), keep only the
//     most-recent user Turn and terminate.
//
// Returns a compressionResult with DroppedMessages/RemainingMessages for
// event emission, and ok=true when eviction actually occurred.
func (al *AgentLoop) windowTrim(agent *AgentInstance, sessionKey string) (compressionResult, bool) {
	window := agent.Sessions.GetHistory(sessionKey)
	if len(window) <= 1 {
		// Nothing to evict: a single-message window cannot be shrunk further.
		return compressionResult{NothingToTrim: true}, false
	}

	toolDefs := agent.Tools.ToProviderDefs()
	toolDefsTokens := estimateToolDefsTokens(toolDefs)

	// Recall span tokens — updated after a potential drop below.
	recallSpan := al.activeRecallSpan(sessionKey)
	recallSpanTokens := 0
	if recallSpan != nil {
		recallSpanTokens = recallSpan.Tokens
	}

	contextWindow := agent.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	maxTokens := agent.MaxTokens

	// 5% headroom target: budget for the window must leave room for a normal
	// next-turn response and the 5% slack so we don't immediately re-trim.
	headroom := (contextWindow + 19) / 20 // ceil(0.05 * contextWindow)

	// M3 fix: subtract pinned-core (system prompt) + breadcrumb overhead so the
	// suffix fit-check uses the same budget basis as isOverContextBudget (which
	// counts the system message). On small-window models, omitting these causes
	// under-eviction — the assembled request is still over budget after trim.
	//
	// Estimate the system prompt token cost via the ContextBuilder cache:
	// the static prompt is already cached, so BuildSystemPromptWithCache is cheap.
	// We estimate using the same chars-per-token ratio as estimateMessageTokens.
	var pinnedCoreOverhead int
	if agent.ContextBuilder != nil {
		sysPrompt := agent.ContextBuilder.BuildSystemPromptWithCache()
		// chars*2/5 ≈ tokens — exactly the same heuristic as estimateMessageTokens
		// (which uses chars*2/5 after adding per-message overhead). chars/4 would
		// underestimate by ~38%, causing under-eviction on small-window models.
		pinnedCoreOverhead = len(sysPrompt) * 2 / 5
	}
	// breadcrumbTokenCap is the hard cap on the breadcrumb block (~1000 tokens);
	// use it as a conservative estimate of the breadcrumb overhead.
	pinnedCoreOverhead += breadcrumbTokenCap

	budget := contextWindow - maxTokens - headroom - pinnedCoreOverhead

	// FR-019 drop-span-first: if an active span exists and we're over budget,
	// drop it and re-check. Only evict real window Turns if still over budget.
	currentWindowTokens := sumMessageTokens(window)
	if recallSpan != nil && (currentWindowTokens+toolDefsTokens+recallSpanTokens+maxTokens > contextWindow) {
		al.dropRecallSpan(sessionKey, "pressure")
		recallSpanTokens = 0
		// Re-check against the same budget basis used for the suffix fit-check
		// below (contextWindow − maxTokens − headroom − pinnedCoreOverhead).
		// Using raw contextWindow here would pass cases that the suffix walk
		// would still reject, causing unnecessary evictions on the next call.
		if currentWindowTokens+toolDefsTokens <= budget {
			// The recall span alone was the problem: dropping it brought the
			// window back under budget without evicting any window Turns.
			// This IS a real, successful eviction — FR-019 names the span
			// drop as step 3 of the documented algorithm — so report
			// ok=true, the same as a normal window eviction, rather than
			// ok=false. That makes the caller rebuild the assembled
			// messages (dropping the now-stale recall-span content) instead
			// of treating a useful eviction as a compaction failure.
			return compressionResult{RemainingMessages: len(window)}, true
		}
	}

	// Walk Turn boundaries (oldest first) to find the smallest cut that fits.
	boundaries := parseTurnBoundaries(window)

	// Find the smallest boundary index b such that window[b:] fits in budget.
	cutIdx := -1 // -1 means no boundary fits
	for _, b := range boundaries {
		if b == 0 {
			// Boundary at 0 keeps everything — not a useful cut.
			continue
		}
		suffix := window[b:]
		suffixTokens := sumMessageTokens(suffix)
		if suffixTokens+toolDefsTokens+recallSpanTokens <= budget {
			cutIdx = b
			break
		}
	}

	// Determine how many messages to keep (tail of the live window).
	// cutIdx >= 0: normal path — keep window[cutIdx:].
	// cutIdx < 0 (FR-003 floor): keep from the most-recent user message onward
	//   (the last complete Turn in the live window).
	//
	// Both paths call TruncateHistory (Skip-advancing, archive-preserving; zero
	// bytes deleted from the JSONL file). SetHistory is NEVER used — it would
	// overwrite the entire JSONL and reset Skip=0, permanently destroying evicted
	// turns (SC-001). The floor keeps window[lastUserIdx:] — the user message and
	// any following assistant/tool messages — not just the bare user message.
	var droppedCount int
	if cutIdx >= 0 {
		// Normal path: tail-of-window keeps are handled by TruncateHistory.
		// TruncateHistory advances meta.Skip (archive-preserving; zero bytes
		// deleted from the JSONL file). SetHistory is NOT used here.
		keepLast := len(window) - cutIdx
		droppedCount = len(window) - keepLast
		agent.Sessions.TruncateHistory(sessionKey, keepLast)
	} else {
		// FR-003: emergency floor — no boundary fits (single huge Turn).
		// Keep the messages from the most-recent user message onward. This is
		// the last complete Turn in the window (user message + any following
		// assistant/tool messages). TruncateHistory advances meta.Skip to
		// exactly that position — archive-preserving, no SetHistory.
		lastUserIdx := -1
		for i := len(window) - 1; i >= 0; i-- {
			if window[i].Role == "user" {
				lastUserIdx = i
				break
			}
		}
		keepStart := lastUserIdx
		if keepStart < 0 {
			// Degenerate: no user message at all — keep last message.
			keepStart = len(window) - 1
		}
		keepLast := len(window) - keepStart
		droppedCount = len(window) - keepLast
		agent.Sessions.TruncateHistory(sessionKey, keepLast)
	}

	if saveErr := agent.Sessions.Save(sessionKey); saveErr != nil {
		logger.ErrorCF("agent", "windowTrim: failed to persist trimmed session",
			map[string]any{"session_key": sessionKey, "error": saveErr.Error()})
	}

	// M4 fix: verify the window actually shrank after the TruncateHistory call.
	// The backends' TruncateHistory is fire-and-forget (errors are logged, not
	// returned). Re-read GetHistory and compare: if the window is the same size
	// as before, the trim silently failed — log the error and return ok=false so
	// the caller does not misreport a successful eviction.
	postWindow := agent.Sessions.GetHistory(sessionKey)
	if len(postWindow) >= len(window) {
		logger.ErrorCF("agent", "windowTrim: TruncateHistory did not shrink the window (backend write may have failed)",
			map[string]any{
				"session_key": sessionKey,
				"before":      len(window),
				"after":       len(postWindow),
			})
		return compressionResult{}, false
	}

	evictionTotal.Add(1)
	// skipAdvanceTotal counts Skip-advancing TruncateHistory calls. Both the
	// normal path and the FR-003 floor path now use TruncateHistory (Skip-
	// preserving), so increment whenever droppedCount > 0.
	if droppedCount > 0 {
		skipAdvanceTotal.Add(1)
	}

	keptCount := len(postWindow) // use the verified post-trim window size

	// M5 / FR-018: emit context_archive_bytes by reading the full archive.
	// This is done after the confirmed trim so the stat reflects the actual
	// post-eviction archive size (which is UNCHANGED — eviction never deletes
	// bytes from the JSONL file, only advances Skip). The byte count is emitted
	// as a structured log field so it is observable in production without a
	// full metrics framework. Only done when we have an archive reader.
	archiveBytes := int64(-1) // -1 indicates unavailable (sentinel; explained below)
	if archived, err := agent.Sessions.ReadArchive(context.Background(), sessionKey); err == nil {
		// Estimate archive size from the number of archived messages (each JSON
		// line is typically 100–500 bytes; use message count as a proxy).
		// Actual byte stat requires fs.Stat — not exposed through SessionStore.
		// For now: count is a proxy observable alongside the real skip value.
		archiveBytes = int64(len(archived))
	} else {
		// M5: ReadArchive error post-eviction — the eviction itself succeeded
		// (M4 verified the window shrank), but the archive byte stat is
		// unavailable. Log at DEBUG so operators can correlate -1 in the warn
		// below with a transient I/O issue without polluting the warn stream.
		logger.DebugCF("agent", "windowTrim: M5 ReadArchive failed; context_archive_lines will be -1 (sentinel)",
			map[string]any{"session_key": sessionKey, "error": err.Error()})
	}

	logger.WarnCF("agent", "windowTrim: evicted oldest Turns from live window",
		map[string]any{
			"session_key":            sessionKey,
			"turns_evicted":          droppedCount,
			"kept_msgs":              keptCount,
			"budget":                 budget,
			"context_archive_lines":  archiveBytes, // FR-018 context_archive_bytes proxy
			"context_eviction_total": evictionTotal.Load(),
		})

	return compressionResult{
		DroppedMessages:   droppedCount,
		RemainingMessages: keptCount,
	}, true
}

// SwitchAction is the result of decideSwitchCompressAction: should we
// compress the conversation at model-switch time, or no-op?
//
// (FR-011, spec §11 Dataset 3.)
type SwitchAction int

const (
	// SwitchActionNoop means the new window fits the current conversation;
	// no re-window needed.
	SwitchActionNoop SwitchAction = iota
	// SwitchActionCompress means the new window is smaller than the current
	// conversation; handleModelSwitch MUST invoke windowTrim before the next
	// LLM call (FR-011).
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

// sumMessageTokens sums estimateMessageTokens across a message slice.
// It is the single consistent call site for token-counting a []providers.Message
// so that the heuristic (chars*2/5) is applied uniformly across windowTrim,
// estimateHistoryTokens, and any future callers.
func sumMessageTokens(msgs []providers.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMessageTokens(m)
	}
	return total
}

// estimateHistoryTokens is a small helper that sums estimateMessageTokens
// across a history slice. The loop's switch-time compress path uses it to
// compute the "current conversation size" fed into decideSwitchCompressAction.
func estimateHistoryTokens(history []providers.Message) int {
	return sumMessageTokens(history)
}

// handleModelSwitch is the switch-time re-window path (FR-011). It is invoked
// when an incoming bus message carries a model_name metadata that differs from
// the agent's current Model.
//
// Behavior:
//  1. Resolve the new model's ContextWindow.
//  2. Estimate the current conversation size.
//  3. If decideSwitchCompressAction says Compress (downsize only), call
//     windowTrim with the new budget — no LLM call, no summary written.
//     windowTrim inherits FR-003's last-user-Turn floor (MAJ-06).
//  4. Upsize (Noop): Skip stays forward-only (US-6.2). Extra room is used by
//     new turns; evicted turns are reachable via recall_conversation.
//  5. Update agent.Model to the new model and return the agent.
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
	// fit" behavior so the next LLM call's windowTrim still fires on overflow.
	// Force a 128k floor for sub-zero agent defaults so decision logic still
	// has a sane bound.
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
		// call will trip on overflow and windowTrim will fire), but
		// we MUST surface the miss to the operator via a WARN. Discarding the
		// error (W4-4 silent-failure-A) would let a typo'd `metadata.model_name`
		// from the operator silently route the next LLM call through the
		// agent's PRIMARY model — the exact FR-007 failure mode. Logging the
		// resolve error at WARN with the requested model + agent id gives
		// operators a breadcrumb to spot the typo at the switch site rather
		// than several stack frames later.
		if _, resolveErr := ResolveModelCfg(cfg, newModel, agent.Home); resolveErr != nil {
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
		// keep the "unknown model = no-op switch" behavior; the next LLM
		// call's windowTrim will fire on overflow.
	}

	// FR-011: Re-window via windowTrim when the new model's window is
	// smaller than the current conversation. windowTrim uses the agent's
	// current ContextWindow; temporarily override it with newContextWindow
	// so the trim targets the new model's budget.
	//
	// UPSIZE: Skip stays forward-only (US-6.2). Extra room goes to new
	// turns; evicted turns are reachable via recall_conversation.
	// DOWNSIZE: windowTrim inherits FR-003's last-user-Turn floor (MAJ-06).
	// No summary is written — breadcrumb in BuildMessages is the only clue.
	action := decideSwitchCompressAction(currentConvTokens, newContextWindow)
	if action == SwitchActionCompress {
		// Temporarily set the agent's context window to the new model's window
		// so windowTrim computes the correct budget. We restore it after the
		// trim; ApplyAgentModel (below) will set the canonical value.
		oldContextWindow := agent.ContextWindow
		agent.ContextWindow = newContextWindow

		if _, trimOK := al.windowTrim(agent, sessionKey); !trimOK {
			logger.DebugCF("agent", "handleModelSwitch: windowTrim returned false (history too small to trim)",
				map[string]any{"session_key": sessionKey})
		} else {
			logger.InfoCF("agent", "handleModelSwitch: re-windowed via windowTrim (no summary)",
				map[string]any{
					"session_key":        sessionKey,
					"old_model":          oldModel,
					"new_model":          newModel,
					"new_context_window": newContextWindow,
				})
		}

		agent.ContextWindow = oldContextWindow
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
	var degraded bool
	if len(validMessages) > maxSummarizationMessages {
		mid := len(validMessages) / 2

		mid = al.findNearestUserMessage(validMessages, mid)

		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		s1, s1Degraded := al.summarizeBatch(ctx, agent, part1, "")
		s2, s2Degraded := al.summarizeBatch(ctx, agent, part2, "")
		degraded = s1Degraded || s2Degraded

		mergePrompt := fmt.Sprintf(
			"Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s",
			s1,
			s2,
		)

		resp, err := al.retryLLMCall(ctx, agent, mergePrompt, llmMaxRetries)
		if err == nil && resp != nil && resp.Content != "" {
			finalSummary = resp.Content
		} else {
			logger.WarnCF(
				"agent",
				"summarizeSession: LLM merge of multi-part summaries failed after retries, falling back to concatenation",
				map[string]any{
					"session_key": sessionKey,
					"reason":      errString(err),
				},
			)
			finalSummary = s1 + " " + s2
			degraded = true
		}
	} else {
		finalSummary, degraded = al.summarizeBatch(ctx, agent, validMessages, summary)
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
				Degraded:           degraded,
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

// summarizeBatch summarizes a batch of messages. It returns the summary text
// and a degraded flag: degraded is true when the LLM summarization call
// failed after retries and the returned text is a crude per-message
// truncation fallback rather than a real LLM-produced summary.
func (al *AgentLoop) summarizeBatch(
	ctx context.Context,
	agent *AgentInstance,
	batch []providers.Message,
	existingSummary string,
) (string, bool) {
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
	if err == nil && response != nil && response.Content != "" {
		return strings.TrimSpace(response.Content), false
	}

	reason := "LLM returned empty content"
	if err != nil {
		reason = errString(err)
	}
	logger.WarnCF("agent", "summarizeBatch: LLM summarization failed after retries, falling back to crude truncation",
		map[string]any{
			"agent_id":    agent.ID,
			"batch_size":  len(batch),
			"max_retries": llmMaxRetries,
			"reason":      reason,
		})

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
	return fallback.String(), true
}

// estimateTokens estimates the number of tokens in a message list.
// Counts Content, ToolCalls arguments, and ToolCallID metadata so that
// tool-heavy conversations are not systematically undercounted.
func (al *AgentLoop) estimateTokens(messages []providers.Message) int {
	return sumMessageTokens(messages)
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

	// applyMemoryCommandPrompt runs after the skill hook and before dispatch
	// (same seam as applyExplicitSkillCommand above). Since /remember, /recall,
	// and /retrospective are registered builtins (pkg/commands/cmd_memory.go),
	// applyExplicitSkillCommand's own builtin-wins check already returns
	// matched=false for these three names, so this hook is what actually
	// rewrites their turn. See applyMemoryCommandPrompt's doc comment for why
	// a rewrite hook is used instead of a Handler.
	if matched, handled, reply := al.applyMemoryCommandPrompt(msg.Content, opts); matched {
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
		SenderID: msg.Sender.CanonicalID,
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

// applyExplicitSkillCommand handles the "/<name>" one-shot skill activation
// introduced by the unified slash-command + skill menu (D2/D3/D4/R1).
//
// Resolution order (R1):
//  1. If <name> is any registered built-in command (visible or hidden) →
//     return matched=false so the normal command dispatcher handles it.
//     Registration is the sole authority — hidden/deprecated back-compat
//     aliases also win (D3: "built-ins win").
//  2. If <name> resolves to an installed skill (exact, case-insensitive) →
//     force the skill for THIS turn (one-shot, no arming/pending state).
//     If a message follows ("/<skill> the message"), set opts.UserMessage to
//     that message. If no message follows, opts.UserMessage retains the
//     literal "/<skill-id>" token as the user turn (the skill's injected
//     instructions from its SKILL.md body drive the LLM response, per R1).
//  3. Otherwise → return matched=false so the text is delivered as a normal
//     chat message (D4 — unknown /<x> is not an error).
//
// The old arm/pending path (setPendingSkills) and /use-token guard are removed.
func (al *AgentLoop) applyExplicitSkillCommand(
	raw string,
	agent *AgentInstance,
	opts *processOptions,
) (matched bool, handled bool, reply string) {
	cmdName, ok := commands.CommandName(raw)
	if !ok {
		// No leading "/" token at all — not our concern.
		return false, false, ""
	}

	// D3: if <name> is any registered built-in (visible or hidden/deprecated),
	// let normal dispatch run. Hidden commands are back-compat aliases that must
	// not be shadowed by a skill of the same name — registration is the sole
	// authority (D3: "built-ins win").
	if al.cmdRegistry != nil {
		if _, found := al.cmdRegistry.Lookup(cmdName); found {
			return false, false, ""
		}
	}

	// No skill context — cannot resolve skills; fall through to normal message.
	if agent == nil || agent.ContextBuilder == nil {
		return false, false, ""
	}

	// Attempt exact, case-insensitive slug resolution.
	skillName, ok := agent.ContextBuilder.ResolveSkillName(cmdName)
	if !ok {
		// Unknown /<x> → normal chat message (D4).
		return false, false, ""
	}

	// Skill matched — one-shot activation (R1).
	if opts != nil {
		opts.ForcedSkills = append(opts.ForcedSkills, skillName)

		// If a message follows the skill token, replace opts.UserMessage with
		// that message. If no message is provided, leave opts.UserMessage as
		// the literal "/<skill-id>" token — it becomes the user turn text and
		// the skill's injected instructions (from SKILL.md body) drive the LLM
		// response. The literal token is intentional; the SPA renders it as a
		// compact "skill: <name>" indicator (R2) so users never see the raw token.
		parts := strings.Fields(strings.TrimSpace(raw))
		if len(parts) >= 2 {
			message := strings.TrimSpace(strings.Join(parts[1:], " "))
			if message != "" {
				opts.UserMessage = message
			}
		}
	}

	// Return matched=true, handled=false so the turn continues to the LLM.
	return true, false, ""
}

// applyMemoryCommandPrompt rewrites opts.UserMessage into a steering prompt
// for the three memory slash commands (/remember, /recall, /retrospective)
// and lets the turn continue to the LLM afterward. Templates live in
// commands.MemoryCommandSteeringPrompt (pkg/commands/cmd_memory.go).
//
// Constraint — why a rewrite hook and not a commands.Definition.Handler:
// a Handler runs synchronously inside Executor.executeDefinition and replies
// immediately (pkg/commands/executor.go), which short-circuits the turn
// BEFORE the LLM ever sees it. These three commands need the model itself to
// invoke a real tool (remember / recall_memory / recall_conversation /
// run_retrospective) and shape its reply from the tool's actual output, so
// their Definitions are registered with Handler: nil (passthrough, per
// executor.go's OutcomePassthrough) and this hook does the rewrite instead —
// mirroring how applyExplicitSkillCommand rewrites opts.UserMessage for
// one-shot skill activation above.
//
// Known nuance: agentID "main" (the gateway/router agent) is never given the
// remember / recall_memory / run_retrospective tools — pkg/agent/instance.go
// registers them only for agentID != "main". The steering prompt still
// degrades gracefully there: the model simply reports it doesn't have that
// capability instead of the turn erroring.
func (al *AgentLoop) applyMemoryCommandPrompt(
	raw string,
	opts *processOptions,
) (matched bool, handled bool, reply string) {
	cmdName, ok := commands.CommandName(raw)
	if !ok {
		return false, false, ""
	}

	args := commands.CommandArgs(raw)
	prompt, ok := commands.MemoryCommandSteeringPrompt(cmdName, args)
	if !ok {
		return false, false, ""
	}

	if opts != nil {
		opts.UserMessage = prompt
	}

	// matched=true, handled=false: the turn continues to the LLM with the
	// rewritten steering prompt as its user message.
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
			peerID = msg.Sender.CanonicalID
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
// (Spec-2 FR-2.5, ADR-029 FR-023/MAJ-002).
//
// Source priority:
//  1. msg.InstanceID — always stamped by the trusted BaseChannel adapter.
//  2. msg.Channel    — legacy fallback for single-instance adapters that have
//     not yet been updated to stamp InstanceID.
//
// The metadata["instance_id"] fallback that existed here has been removed
// (S-4 / security review 2026-07-02): msg.InstanceID is now the authoritative
// source stamped by the trusted adapter, and the Metadata map is
// content-adjacent (caller-controlled), making it a spoofing footgun.
// No adapter writes metadata["instance_id"] (confirmed by grep of pkg/channels/).
//
// The result is lower-cased to match the config map keys.
func inboundInstanceID(msg bus.InboundMessage) string {
	if id := strings.TrimSpace(msg.InstanceID); id != "" {
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
// search_web tool removed. Used when native provider search is preferred.
func filterClientWebSearch(tools []providers.ToolDefinition) []providers.ToolDefinition {
	result := make([]providers.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if strings.EqualFold(t.Function.Name, "search_web") {
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
				User:      ts.auditUser(), // FR-017
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
	// The unified `load_tool` infra tool is registration-gated, not policy-gated:
	// it only exists on the agent when compressed mode or MCP discovery is on,
	// and when present it MUST always be executable — it drives the manifest
	// mechanism itself, so denying it makes every lazy tool unreachable. Treat a
	// registered infra tool as "allow" regardless of what the agent's own
	// tool-policy map resolves for it.
	// (Without this, resolveToolPolicyAtExec re-derives livePolicy="deny" for a
	// deny-by-default agent and overrides the filter-time allow — the live bug.)
	if tools.ToolManifestTier(toolName) == tools.ManifestInfra {
		if _, ok := ts.agent.Tools.Get(toolName); ok {
			return "allow"
		}
	}
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

// CheckGrantOrRequestApproval is the SOLE consultation point for tool-approval
// grants on the "ask" policy path (ADR-036 §3.4). It first checks the
// session-scoped "Always Allow" grant store (al.ApprovalGrants()); only when
// no grant is on file does it fall through to the interactive human-approval
// flow via the wired PolicyApprover (loadToolApprover -> RequestApproval).
//
// Before ADR-036 this consultation lived in the gateway's wsApprovalHook
// (pkg/gateway/ws_approval.go, deleted by this change) — a WebSocket-frame
// approval gate that ran BEFORE runTurn's TOCTOU "ask" branch below and
// unconditionally denied after a 90s timeout once its answering frontend UI
// (ExecApprovalBlock/ExecApprovalTool) was removed in the same ADR-036
// change — making the "ask" branch, and therefore this grant store,
// permanently unreachable for any WebSocket-connected chat session. Retiring
// that gate and relocating grant consultation HERE (the only path that was
// ever reachable in practice) is the fix.
//
// Only called when the effective tool policy has already resolved to "ask" —
// callers MUST resolve policy (allow/deny/ask) themselves before calling this;
// it does not re-check policy itself. In runTurn, "deny" short-circuits with
// `continue` and "allow" falls straight through to execution, so neither ever
// reaches this function — a grant can never widen a "deny" verdict, and an
// "allow" verdict never touches the grant store at all.
//
// Exported (rather than folded inline against the unexported *turnState) so it
// is directly unit-testable — including from pkg/gateway, which imports
// pkg/agent but cannot construct a *turnState — without spinning up a
// WebSocket connection. See pkg/gateway/ws_approval_grants_test.go.
//
// Identity: sessionID MUST be the transcript-store session ID
// (turnState.transcriptSessionID), NOT the session-store scope key
// (turnState.sessionKey). transcriptSessionID is the identity
// ApprovalGrantStore.Inherit and ClearSession already use, and the ONE
// identity shared across a delegation chain: subturn.go's spawnSubTurn gives
// every child turn its own distinct, per-child SessionKey but always threads
// the PARENT's TranscriptSessionID through unchanged, so a grant recorded
// under sessionKey would never be visible to a delegated child turn.
func (al *AgentLoop) CheckGrantOrRequestApproval(
	ctx context.Context,
	sessionID, agentID, toolName, toolCallID, turnID string,
	args map[string]any,
) (approved bool, denialReason string) {
	if al.ApprovalGrants().IsAllowed(sessionID, agentID, toolName) {
		return true, ""
	}
	approver := al.loadToolApprover()
	return approver.RequestApproval(ctx, PolicyApprovalReq{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Args:       cloneStringAnyMap(args),
		AgentID:    agentID,
		SessionID:  sessionID,
		TurnID:     turnID,
	})
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
		User:      ts.auditUser(), // FR-017
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

// buildScratchpadNote returns a short ephemeral system note for the agent's
// current set_todos scratchpad. It queries the task store for the most-recent
// active goal-task that has todos assigned to agentID, renders them, and
// returns the note string. Returns "" when there is nothing to inject (no
// store, no active goal-task with todos). This is rebuilt fresh every turn so
// it survives context compression without polluting the persisted transcript.
func (al *AgentLoop) buildScratchpadNote(agentID string) string {
	if al.taskStore == nil || agentID == "" {
		return ""
	}
	tasks, err := al.taskStore.List(task.Filter{AgentID: agentID})
	if err != nil {
		logger.WarnCF("agent", "buildScratchpadNote: task list failed; dropping scratchpad note",
			map[string]any{"agent_id": agentID, "error": err},
		)
		return ""
	}
	// Find the most-recent active SCRATCHPAD goal-task that has at least one todo.
	// Restricting to Scratchpad==true ensures we never re-inject a real create_task
	// card's checklist — which would diverge from the set_todos facade's selection.
	var found *task.Task
	for i := range tasks {
		tk := &tasks[i]
		if !tk.Scratchpad {
			continue
		}
		if task.IsTerminal(tk.Status) {
			continue
		}
		if len(tk.Todos) == 0 {
			continue
		}
		// List is sorted priority ASC then created_at ASC; last match is
		// most-recently-created among equal-priority active scratchpad cards.
		found = tk
	}
	if found == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# Current scratchpad\nGoal: ")
	sb.WriteString(found.Title)
	sb.WriteByte('\n')
	for _, td := range found.Todos {
		sb.WriteString("- [")
		sb.WriteString(string(td.Status))
		sb.WriteString("] ")
		sb.WriteString(td.Text)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// markToolsLoaded records names as loaded for the given session (manifest
// optimization). Creates the inner map on first call for a session.
// Safe for concurrent access — protected by loadedToolsMu.
func (al *AgentLoop) markToolsLoaded(sessionID string, names []string) {
	if sessionID == "" || len(names) == 0 {
		return
	}
	al.loadedToolsMu.Lock()
	defer al.loadedToolsMu.Unlock()
	if al.loadedTools[sessionID] == nil {
		al.loadedTools[sessionID] = make(map[string]bool, len(names))
	}
	for _, n := range names {
		al.loadedTools[sessionID][n] = true
	}
}

// sessionLoadedTools returns a copy of the loaded-tool set for sessionID.
// Returns an empty map when sessionID is empty or has no entries.
// Safe for concurrent access — protected by loadedToolsMu.
func (al *AgentLoop) sessionLoadedTools(sessionID string) map[string]bool {
	if sessionID == "" {
		return map[string]bool{}
	}
	al.loadedToolsMu.Lock()
	defer al.loadedToolsMu.Unlock()
	src := al.loadedTools[sessionID]
	if len(src) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// forgetSession removes the session's loaded-tool entry from the manifest map,
// preventing unbounded memory growth. Called from CloseSession with the same
// key that the manifest system uses (manifestSessionID derivation).
// Safe for concurrent access — protected by loadedToolsMu. No-op for unknown keys.
func (al *AgentLoop) forgetSession(sessionID string) {
	if sessionID == "" {
		return
	}
	al.loadedToolsMu.Lock()
	defer al.loadedToolsMu.Unlock()
	delete(al.loadedTools, sessionID)

	// MINOR fix: clean up recall spans for this session so recallSpans sync.Map
	// does not grow without bound as sessions are closed (FR-019). The span key
	// is ts.sessionKey ("agent:<agentID>:session:<sessionID>") while forgetSession
	// receives only the transcript sessionID. We scan the map for any key that
	// contains the sessionID as a suffix so we delete spans regardless of agentID.
	// The scan is safe here because forgetSession is on the session-close path
	// (not the hot turn path), so the O(n) Range is acceptable.
	suffix := ":session:" + sessionID
	al.recallSpans.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok {
			if key == sessionID || strings.HasSuffix(key, suffix) {
				al.recallSpans.Delete(key)
			}
		}
		return true
	})
}
