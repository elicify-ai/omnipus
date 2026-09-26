// Omnipus - Ultra-lightweight personal AI agent
// Built on Omnipus's foundation. See CLAUDE.md for project lineage.
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/state"
	"github.com/elicify-ai/omnipus/pkg/steer"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/utils"
	"github.com/elicify-ai/omnipus/pkg/voice"
)

type AgentLoop struct {
	// Core dependencies
	bus      *bus.MessageBus
	cfg      *config.Config
	registry *AgentRegistry
	state    *state.Manager

	// configGen is a monotonic counter bumped every time al.cfg is replaced
	// with a new pointer, under al.mu.Lock() — currently by SwapConfig and by
	// ReloadProviderAndConfig's own al.cfg/al.registry swap (see both call
	// sites for the Add(1) call). It exists because "al.registry changed" is
	// NOT a reliable proxy for "al.cfg changed": SwapConfig (the path every
	// REST-initiated config write goes through — see
	// pkg/gateway/rest.go's refreshConfigAndRewireServices) replaces al.cfg
	// ALONE and never touches al.registry at all. UpsertAgentFast's
	// optimistic-concurrency publish loop (pkg/agent/registry.go) reads this
	// alongside al.registry to detect ANY concurrent config writer, not just
	// a full registry-swapping reload — otherwise a bare SwapConfig landing
	// mid-upsert is invisible to a registry-pointer-only CAS check and gets
	// silently reverted by the upsert's own later `al.cfg = cfg` publish.
	configGen atomic.Uint64

	// Event system
	eventBus *EventBus
	hooks    *HookManager

	// Runtime state
	running     atomic.Bool
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
	recallSpans      sync.Map // key: sessionKey (string), value: *RecallSpan
	activeTurnStates sync.Map // key: sessionKey (string), value: *turnState
	// subTurnSpansMu/openSubTurnSpans/subTurnCounter (the sub-turn span
	// liveness tracker guarded by subTurnSpansMu, and its id counter) were
	// deleted 2026-09-24 as unreachable ADR-091 leftovers (golangci
	// unused): their only writer, steering.go's markSubTurnSpanOpen, was
	// already deleted (see websocket_replay.go's replay-derived liveness
	// comment near streamReplay) — these fields had no reader or writer
	// left anywhere in the repo.
	sessionActiveAgent sync.Map // key: "session:"+sessionID (string), value: agentID (string); set by handoff, cleared on agent deletion
	// lastSwitchToDefault records, per session, whether the most recent
	// switch_agent call was a return-to-default (tools.HandoffEvent.ToDefault)
	// rather than a named-agent hand-off. It exists so the WS agent_switched
	// frame builder (pkg/gateway/websocket.go) can report the tool's own
	// intent instead of re-deriving "was this a return to default" after the
	// fact by comparing the resulting active agent id against the configured
	// default agent id — a comparison that misreports an explicit
	// switch_agent(target:"<id>") that happens to name the current default
	// agent as a return-to-default. Populated by onHandoffFrontend
	// synchronously, before the matching ToolExecEnd event is emitted, so the
	// WS handler always observes the value it needs; read once via
	// GetLastSwitchToDefault (LoadAndDelete — one-shot per switch).
	// key: "session:"+sessionID (string), value: bool.
	lastSwitchToDefault sync.Map

	// Turn tracking
	turnSeq        atomic.Uint64
	activeRequests sync.WaitGroup
	// delegatedRateLimitSleep is a per-loop test seam. Production leaves it
	// nil and callProvider uses sleepWithContext; tests can record the exact
	// retry delays without a wall-clock assertion or process-global mutation.
	delegatedRateLimitSleep func(context.Context, time.Duration) error

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
	// planEngine is the single hybrid plan-coordinator instance (ADR-049 D4,
	// Wave 2-B's PlanEngine). Set once at boot by the gateway
	// (SetPlanEngine); nil in tests / before wiring. Wave 2-C2's /goal and
	// /loop command admission reaches it via GetPlanEngine(al).Admit(kind).
	planEngine *PlanEngine
	// planStore is the shared *plan.Store (ADR-052) the create_plan/
	// execute_plan agent tools read/write. It is constructed by the gateway
	// AFTER NewAgentLoop returns (setupAndStartServices, alongside
	// agent.NewPlanEngine — see gateway.go's boot wiring region), so it is
	// nil on the FIRST registerSharedTools pass (inside NewAgentLoop) and
	// installed later via SetPlanStore, which re-wires the plan-tool surface
	// for every currently-registered agent with the real store. Mirrors
	// planEngine's own late-binding discipline exactly. nil in tests /
	// before boot wiring completes — create_plan/execute_plan then register
	// but fail closed at Execute() (Wave-1 discipline, pkg/tools/plan.go).
	planStore *plan.Store

	// channelOwnership resolves which (workspace, agent) owns a channel
	// instance (ADR-065). Stored rather than only pushed into tools, because
	// registerSharedTools rebuilds every MessageTool on reload.
	channelOwnership tools.ChannelOwnership
	// loopSched is the dedicated /loop time-driven scheduler (ADR-049 D6/D7,
	// loop_scheduler.go). Set once at boot by the gateway
	// (SetLoopScheduler); nil in tests / before wiring — applyLoopCommandPrompt
	// nil-checks via the loopScheduler() accessor and reports "/loop
	// unavailable" rather than panicking.
	loopSched *LoopScheduler

	// messageInboxStore is the durable S3 child->parent SessionMessage inbox
	// (ADR-053 §Contract Surface, pkg/session/message_inbox.go). Constructed by
	// the gateway AFTER NewAgentLoop returns (alongside lifecycleStore), then
	// installed via SetSessionMessagingStores — which re-wires the delegate +
	// message_parent tool surface for every registered agent with the real
	// store, mirroring SetPlanStore's late-binding discipline exactly. nil on
	// the FIRST registerSharedTools pass (inside NewAgentLoop, before the store
	// exists) and in tests — the tools then fail-closed at Execute()
	// (message_parent returns "tool not fully configured"; delegate's
	// inbox/steer/cancel actions return "not configured"). See
	// session_messaging_wire.go.
	messageInboxStore *session.MessageInboxStore
	// sessionLifecycleStoreForTools is the durable S2 session-lifecycle store
	// (pkg/session/lifecycle.go), threaded to the delegate + message_parent
	// tools so a child can read/park its own record and a parent can read a
	// child's record. Distinct name from planEngine's own lifecycleStore
	// reference to keep the two consumers (boot-sweep vs tool-surface) legible;
	// both point at the SAME single *session.LifecycleStore instance the
	// gateway constructs once.
	sessionLifecycleStoreForTools *session.LifecycleStore

	// Security (SEC-15): audit logging.
	// Initialized in NewAgentLoop when sandbox.audit_log is enabled.
	auditLogger *audit.Logger

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

	// browserMgrs holds one BrowserManager per BROWSING KEY — one browser, one
	// Chrome, one profile directory, one workspace (ADR-075 FR-001). The map
	// key is browser.BrowsingKey.String() ("ws:<workspaceID>"), NOT an agent id.
	// Guarded by mu.
	//
	// ⚠️ THE KEY CHANGED, AND THE OLD KEY IS THE BUG. This map used to be keyed
	// by agentID (ADR-038 D4), which made "which browser am I driving" a
	// property of whichever agent happened to be on the chat. ADR-075 §1.1
	// records the consequence: an operator browses in the live panel, switches
	// the chat from Mia to Jim, and Jim reports zero tabs — the tab was in
	// Mia's browser. Under the browsing key every agent on a workspace shares
	// ONE browser, and whose tabs are whose inside it is a separate, explicit
	// dimension (browser.TabOwner, FR-080) rather than an accident of the map.
	//
	// Do NOT re-key this by agent, and do NOT reintroduce a single shared
	// field: the gateway's live-view WS handler needs a SPECIFIC browser, and
	// a process-wide singleton would put two workspaces' logins in one cookie
	// jar.
	//
	// Every entry's connection is torn down in AgentLoop.Close(), and whenever
	// registerSharedTools replaces an existing key's entry on hot-reload (see
	// the Release/Shutdown call at that site). In ADR-043 shared-Chrome mode
	// that teardown is coordinator.Release(key) — it drops only the manager's
	// WS connection (CRIT-002/C1: does NOT kill Chrome, does NOT dispose the
	// browser context, which survives for the new manager to re-adopt). The
	// Chrome process itself is killed solely by coordinator.Shutdown() in
	// Close(). In the no-coordinator test/legacy path the old manager IS its
	// own Chrome owner, so manager.Shutdown() (which cancels the chromedp
	// allocator context) is the real process kill there. Dropping the Go
	// *BrowserManager reference alone never kills anything — the allocator
	// context is parented on context.Background(), not on the reference.
	browserMgrs map[string]*browser.BrowserManager

	// browserRegisteredAgents records which agents actually got browser tools
	// on the last registerSharedTools pass. It is the ONLY thing that can
	// distinguish BrowserResolveNotRegistered ("this agent has no browser
	// tools") from BrowserResolveNoWorkspace ("it has them, but this turn is
	// not rooted in a workspace") now that managers are created lazily per key
	// rather than eagerly per agent — before ADR-075, absence from browserMgrs
	// meant both, and browser_inspect.go reported the former for both.
	// Guarded by mu.
	browserRegisteredAgents map[string]bool

	// browserFactory mints a BrowserManager for a browsing key, carrying the
	// CURRENT reload's BrowserConfig and SSRF checker. Set by
	// registerSharedTools on every pass; read by BrowserManagerForKey, which
	// is what creates a manager for a key no agent was registered under (rung
	// 1 of ResolveBrowsingKey accepts an explicit turn workspace_id, which need
	// not match any agent's CoreTeam membership). nil until the first
	// registration pass. Guarded by mu.
	browserFactory func(key browser.BrowsingKey) (*browser.BrowserManager, error)

	// browserCoordinator (ADR-043) is the gateway-scoped owner of the ONE
	// shared Chrome + every agent's browser context. Constructed once and
	// reused across hot-reload (ReloadProviderAndConfig reuses this *AgentLoop,
	// so the coordinator — and the per-agent contexts it owns — survive a
	// Settings save). nil only in tests that construct managers directly.
	browserCoordinator *browser.BrowserCoordinator
	// browserPool (ADR-075 FR-037) owns ONE Chrome per workspace, each with
	// its own profile directory. It supersedes browserCoordinator as the
	// thing managers attach to; the coordinator field survives only for the
	// direct/test path and for shutdown symmetry. Constructed once and reused
	// across hot-reload, for the same reason the coordinator was: a Settings
	// save must not log every workspace out.
	browserPool *browser.BrowserPool
	// homePath is $OMNIPUS_HOME (the parent of the workspace path), handed to
	// NewBrowserCoordinator, which builds the ownership-marker path
	// (<homePath>/browser/shared-chrome.pid) from it.
	homePath string

	// capabilityCatalog is the step-1 capability-gate source for the
	// presentation chain (ADR-067 FR-004). The gateway installs the ONE
	// booted catalog here at boot via SetCapabilityCatalog — the same
	// instance ResolveWindow and the REST surface read, so the 2 MB embedded
	// snapshot is parsed once per process. Nil until then, which is the
	// documented optimistic posture, not a degradation.
	// Guarded by mu (the struct's primary RWMutex).
	capabilityCatalog *catalog.Catalog

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

	// memoryRateLimiter is the shared MemoryRateLimiter (#155 item 6),
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

	// Per-agent sliding-window rate limiting (SEC-26). rateLimiter manages
	// the LLM/hr and tool/min counters per agent; always non-nil after
	// NewAgentLoop so per-call sites add a defensive nil-check that is
	// structurally unreachable. The per-call sites check
	// cfg.Sandbox.RateLimits.* > 0 to decide whether to enforce.
	rateLimiter *security.RateLimiterRegistry

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
	// see AgentLoop.CheckGrantOrRequestApproval), the bash tool (ADR-092
	// grant kinds — path/network/prefix grants, pkg/tools/shell_*.go) and,
	// pre-ADR-091, the deleted spawnSubTurn's async/await paths (InheritFrom,
	// pkg/security/approvalgrants.go, U17a). ADR-091 fix lane RX-SUBTURN
	// finding (comment-only, 2026-09-23): grep found no production caller of
	// ApprovalGrantStore.InheritFrom except through
	// AgentLoop.inheritSessionPermissions (loop_policy.go), which was itself
	// uncalled since subturn.go's deletion — a delegated child no longer
	// inherited the parent's tool-approval grants or ADR-092 Auto-approve/
	// shell-permission state. RECONNECTED (Q13 A, 2026-09-24):
	// steer_launcher.go::SteerLauncher.Launch calls
	// inheritDelegatePermissions immediately after launchSteered commits a
	// steered child's lifecycle record, before Dispatch can reconstruct a
	// turn for it — the launch path both production callers (the `delegate`
	// tool and run_task's live-chat path) funnel through.
	approvalGrants *security.ApprovalGrantStore

	sessionModes *SessionModeStore    // ADR-092 per-chat Auto-approve; see SessionModes
	shellGate    *ShellPermissionGate // ADR-092 bash gate, shared by tool and loop

	// asyncNotifier is the single process-wide AsyncNotifier instance
	// (async-notifier-spec.md), extracted from the formerly-inline
	// asyncCallback closure below. Scoped to the loop's lifetime the same
	// way approvalGrants is, per the spec's Clarifications. Always non-nil
	// after NewAgentLoop.
	asyncNotifier *asyncNotifierImpl

	// audienceResolver, boundaryObserver, upwardDeliverer are ADR-091 I-5's
	// injected pkg/steer dependencies (I-5, boundary
	// inventory §6). Nil until SetSteerAudienceDeps is called (post-boot,
	// once gateway_boot.go::wireSteerDeps has built the real
	// implementations) — every boundary reads them through steer_boundary.go's
	// audienceFor, which treats a nil resolver as AudienceUser (today's
	// unrestricted behaviour) so a bare test AgentLoop that never wires
	// steering is unaffected. Guarded by steerDepsMu (late-bound, mirroring
	// askUserRegistry/askUserRegistryMu).
	audienceResolver steer.AudienceResolver
	boundaryObserver steer.BoundaryObserver
	upwardDeliverer  steer.UpwardDeliverer
	sessionLauncher  steer.SessionLauncher
	steerDepsMu      sync.RWMutex

	// sharedSessionStore is the single UnifiedStore at $OMNIPUS_HOME/sessions/
	// used for all new sessions (joined session model). Legacy per-agent stores
	// remain accessible via GetAgentStore for read-only access to old sessions.
	sharedSessionStore *session.UnifiedStore

	// askUserRegistry is the gateway-injected AskUserQuestion pending
	// registry (askuserquestion-tool-spec v3 §0.4; pkg/askuser.Registry).
	// Nil until SetAskUserRegistry is called; the per-agent
	// AskUserQuestionTool instances resolve it LIVE per call (late-bound, so
	// registerSharedTools may run before the gateway wires it) and fail
	// closed while nil. Guarded by askUserRegistryMu.
	askUserRegistry   tools.AskUserQuestionRegistry
	askUserRegistryMu sync.RWMutex

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

	// inboundCtx is the run-scoped context Run executes under, stored at
	// startup alongside stopCancel. A revived ordinary-root turn
	// (revive_support.go::runRevivedOrdinaryTurn) runs under THIS context —
	// not context.Background() — so the same Stop that ends the dispatch
	// loop cancels it and shutdown's WaitForActiveRequests drains it like
	// every other in-flight request (ADR-093 gate fix, architect CC-2).
	// The accessor falls back to loopCtx when Run has not stored a context
	// yet, and to context.Background() only on a zero-value loop built
	// without NewAgentLoop.
	inboundCtx atomic.Pointer[context.Context]

	// loopCtx is the loop-lifetime context, created at construction and
	// cancelled by Stop() and Close(). It is the fallback a revived
	// ordinary-root turn runs under when Run has not stored inboundCtx yet
	// (a revival racing boot) — a turn started before Run must still be
	// cancellable by the same Stop, not detached on context.Background().
	// Written once by NewAgentLoop; read-only afterwards.
	loopCtx context.Context

	// loopCancel cancels loopCtx. Called by Stop() and Close(); idempotent.
	loopCancel context.CancelFunc

	// revivalFailures remembers the most recent failed revive attempt per
	// session id (revive_support.go::revivalFailure). The D2 launch backstop
	// (steer_launcher.go::launchSteered) consults it so a refusal for a
	// session whose resume attempt is itself failing tells the truth
	// ("resuming this conversation failed: <plain cause>") instead of the
	// send-a-new-message sentence that just failed (ADR-093 gate fix,
	// silent-failure-hunter #6). Entries are cleared on the next successful
	// revive; nothing here is authoritative — the lifecycle record is.
	revivalFailures sync.Map

	// cancelAbuse is the shared abuse detector used by RequestCancel across all
	// four cancel entry points (web, Tier A /cancel, Tier B text-parsing, CLI).
	// Initialized in NewAgentLoop; always non-nil after construction.
	cancelAbuse *cancelAbuseDetector

	// cancelPreArm holds pre-registration cancel latches (cancel_prearm.go) —
	// cancels that arrive before their target turn has registered in
	// activeTurnStates. Initialized in NewAgentLoop; always non-nil after
	// construction. See RequestCancel (cancel.go) for the arm side and
	// registerActiveTurn (turn.go) for the consume side.
	cancelPreArm *cancelPreArm

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
	// manifest optimization (cfg.Tools.Manifest.Compressed) for each
	// (agent, session) bucket. Key: manifestBucketKey(agentID, transcriptID,
	// sessionKey) — ADR-071 D3 §4.6 narrowed this from a session-only key so
	// a switch_agent mid-session no longer lets the incoming agent inherit
	// the outgoing agent's loaded Tier 3 tools. Value: map[string]bool (tool
	// name → loaded). Protected by loadedToolsMu. A new bucket lazily creates
	// a fresh set on first load; entries are evicted by forgetSession's
	// suffix sweep on CloseSession (transcript sessions). Only populated when
	// Compressed is true.
	loadedTools   map[string]map[string]bool
	loadedToolsMu sync.Mutex

	// pendingSearchPromotions is a side table of loadedTools (ADR-071
	// §4.3.1a): bucket key → tool name → the turn index (bucketTurnCounter
	// value) at which ToolSearch's query (by-description) path promoted it.
	// Written only on the query path (never on an exact-name `names` load —
	// FR-038a); cleared when the tool is invoked; swept for staleness once
	// per real conversational turn, after the turnLoop for-loop's per-round
	// TickTTL() calls are all done for that turn (see tickSearchPromotionHorizon's
	// call site). Purely observational
	// — nothing here evicts anything from loadedTools or changes what is
	// callable. Protected by loadedToolsMu (shares the mutex with loadedTools
	// since both are written/read together at the same call sites; the mutex
	// does NOT enumerate the map, so forgetSession's suffix sweep must reach
	// this map explicitly — see forgetSession).
	pendingSearchPromotions map[string]map[string]int

	// bucketTurnCounter tracks a monotonically increasing turn index per
	// (agent, session) bucket, incremented once per TickTTL call for that
	// bucket (ADR-071 §4.3.1a). It backs pendingSearchPromotions' "turn
	// index" write/sweep. A plain per-bucket counter rather than ts.iteration
	// because ts.iteration resets with every new turnState (one per runTurn
	// call), while the no-followup horizon must be counted across the whole
	// conversation. Protected by loadedToolsMu; swept alongside the two maps
	// above by forgetSession.
	bucketTurnCounter map[string]int

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
	UserID                  string              // Authenticated gateway principal (FR-017)
	UserMessage             string              // User message content (may include prefix)
	ForcedSkills            []string            // Skills explicitly requested for this message
	Media                   []string            // media:// refs from inbound message
	InitialSteeringMessages []providers.Message // Steering messages from refactor/agent
	// InitialSteeringCorrelationIDs (issue #870) is the parallel correlation-id
	// slice for InitialSteeringMessages — index i's id belongs to message i,
	// "" where none. Carried separately rather than folded into
	// providers.Message: that struct is the literal LLM provider request
	// wire shape, never a receipt-bookkeeping carrier.
	InitialSteeringCorrelationIDs []string
	DefaultResponse               string                // Response when LLM returns empty
	SendResponse                  bool                  // Whether to send response via bus
	SuppressToolFeedback          bool                  // Whether to suppress inline tool call and result feedback
	NoHistory                     bool                  // If true, don't load session history (for heartbeat)
	SkipInitialSteeringPoll       bool                  // If true, skip the steering poll at loop start (used by Continue)
	TranscriptSessionID           string                // Session ID for transcript tool call recording (empty = disabled)
	TranscriptStore               *session.UnifiedStore // Store for transcript tool call recording (nil = disabled)
	// OriginKind identifies the durable execution origin when this turn does
	// not have a lifecycle record to supply it. The zero value is an ordinary
	// interactive turn. Publication policy resolves the record first.
	OriginKind session.OriginKind

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

	// RunningTaskID names the unified task this turn is executing (founder
	// decision 2026-09-14: last-activity on task cards). processTaskDirect
	// sets it from the tools.WithRunningTaskID context the task executor
	// already stamps on the run; AgentLoop.TaskLiveLastActivity matches on
	// it to expose the turn's live progress stamp for exactly this task.
	// Empty for every non-task turn.
	RunningTaskID string

	// UserInitiated threads bus.InboundMessage.UserInitiated into the turn
	// (ADR-049 Gap #8/r2, spec Part B FR-075/SD-B6/R6) — see that field's doc
	// comment for the fail-closed origin contract. handleCommand reads this
	// (never msg.UserInitiated directly, for the same "read the dedicated
	// processOptions carrier, not the raw inbound field" discipline
	// UserID/gatewayPrincipal already establishes) to decide whether /goal
	// and /loop action or pass through inert as ordinary text. Every
	// processOptions literal NOT built from userInitiated(msg) — ProcessScheduled,
	// processTaskDirect, processTaskDirectExternalCLI, processSystemMessage —
	// leaves this at its zero value (false), which is the correct fail-closed
	// answer for every one of those non-user origins. Pre-ADR-091, the
	// deleted spawnSubTurn did too, for a delegated child. ADR-091 fix lane
	// RX-SUBTURN finding (comment-only; code unchanged): today's replacement
	// entry point, steer_reconstruct.go::reconstructSteeredTurn, instead sets
	// `UserInitiated: wake == nil` — TRUE for a steered session's first turn
	// (delegate child, task child, etc.), by its own doc comment's
	// deliberate design ("mark it as user-originated for the session-owned
	// goal loop"). Whether that is an intentional broadening of this field's
	// fail-closed contract for a launched child, or an unreviewed departure
	// from the invariant this comment states, needs a team check — flagged,
	// not resolved here.
	UserInitiated bool
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

// debitLLMUsage is the ONE accounting path for a provider call's reported
// usage: turnState.lastUsage and the turn's own stats — collapsed total +
// cost, the cache read/write split, and the prompt/completion split.
//
// It exists because ADR-087 D3.9 must debit a REFUSED attempt's billed usage
// (common.ToolArgumentsError.Usage) exactly the way the success arm debits a
// delivered one, and the first cut of that re-typed the calls by hand and
// dropped SetLastUsage. Two callers, one body, no drift.
func (al *AgentLoop) debitLLMUsage(ts *turnState, llmModel string, usage *providers.UsageInfo) {
	if ts == nil || usage == nil {
		return
	}
	ts.SetLastUsage(usage)
	ts.AddTurnStats(int64(usage.TotalTokens), estimateLLMCallCost(llmModel, usage))
	ts.AddTurnCacheStats(usage.CacheReadTokens, usage.CacheWriteTokens)
	ts.AddTurnIOStats(usage.PromptTokens, usage.CompletionTokens)
}

// ErrReloadNotConfigured is returned by TriggerReload when no reload function
// has been registered. This is normal in unit-test environments where the full
// gateway reload pipeline is not wired. Production always configures the reload
// function during startup, so callers outside tests should treat this as
// unexpected and log accordingly.
var ErrReloadNotConfigured = errors.New("reload not configured")

// (The TriggerReload window hook that used to live here is gone: the race it
// let tests reproduce is now structurally impossible, because TriggerReload no
// longer sets the reload-pending flag at all. See TriggerReload.)

// ErrAgentNotWorkspaceMember is returned by runTurn when the acting agent is
// not a member of any workspace's CoreTeam (ADR-046 P1, FR-007/008). Execution
// is always workspace-scoped: agents are metadata until added to a workspace's
// team, and a turn for an unassigned agent MUST be refused rather than
// silently falling through to the agent's own private home directory. This
// applies uniformly to top-level and delegated (steer_launcher.go's
// SteerLauncher; pre-ADR-091, the deleted spawnSubTurn) turns alike, since
// both resolve ts.agent.ID the same way in the re-root block below.
var ErrAgentNotWorkspaceMember = errors.New("agent is not a member of any workspace; turn refused")

// ErrWorkspaceWorkDirUnavailable is returned when the agent belongs to a
// workspace but its work/ directory could not be created or opened.
var ErrWorkspaceWorkDirUnavailable = errors.New("workspace work directory unavailable")

// ErrAgentHomeUnavailable is returned when a system agent's private home
// directory is missing or could not be created.
var ErrAgentHomeUnavailable = errors.New("agent home directory unavailable")

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

// newAgentLoop carries the shared state of NewAgentLoop across its stages.
type newAgentLoop struct {
	cfg      *config.Config
	msgBus   *bus.MessageBus
	provider providers.LLMProvider
	registry *AgentRegistry
	al       *AgentLoop
	homePath string
}

// NewAgentLoop constructs an AgentLoop from the given config, message bus, and LLM provider.
// Returns (*AgentLoop, nil) on success or (nil, error) on a fatal configuration error.
func NewAgentLoop(
	cfg *config.Config,
	msgBus *bus.MessageBus,
	provider providers.LLMProvider,
) (*AgentLoop, error) {
	nal := &newAgentLoop{cfg: cfg, msgBus: msgBus, provider: provider}

	nal.initializeCore()

	if r0, stop, r1 := nal.initializeAudit(); stop {
		return r0, r1
	}

	nal.initializeSecurity()

	return nal.initializeRuntime()
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
	if payload.SessionID == "" {
		payload.SessionID = string(ts.routingSessionID)
	}
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
	ts.appendClassifiedError(EventKindRateLimit.String(), "rate_limit", LLMError{
		Code:      CodeRateLimited,
		Message:   rlMsg,
		Retryable: isRetryable(CodeRateLimited),
	})
}

// errString returns err.Error() or "" for a nil error — a tiny helper for log
// fields where a nil error should produce no message.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
	// Revived inbound turns (revive_support.go) run under the same run-scoped
	// context as the dispatch loop itself (architect CC-2: a resumed turn is
	// cancelled by the same Stop that ends the loop and drained by
	// WaitForActiveRequests — no context.Background()).
	al.inboundCtx.Store(&runCtx)

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

			// System messages carrying a resolved async origin session
			// (#505) run through the per-session sessionWorker so the
			// reconstructed turn is serialized against the origin session's
			// live turns, exactly like every other inbound message. Before
			// this, FIX 5d's AsyncTranscriptSessionID threading made the bare
			// goroutine below able to run a real turn concurrently against the
			// SAME origin session as a live user turn (UAT A-17: five turns
			// at once on one session, each started as a background delegation
			// finished, ending in SIGKILL-recovered orphaned tool calls).
			//
			// Dispatch prefers a worker that already owns the session —
			// matched on the one scope-shape guarantee resolveSteeringTarget
			// makes unconditionally (a literal ":"+SessionID suffix; see
			// cancel_prearm.go's matching rationale) — so serialization holds
			// whichever agent-shaped key the live turn used (explicit
			// dropdown, handoff pin, or default route). With no live worker,
			// a probe message shaped like the session's own user traffic
			// (origin channel/chatID + SessionID) resolves the scope the same
			// way that traffic would, and a worker is spawned under it — the
			// residual gap is a turn whose routing diverges from the probe's
			// (e.g. an agent selected per-message via metadata while no worker
			// is live): that pair still falls back to the bare goroutine
			// below, no worse than before.
			//
			// sessionWorker.enqueue deliberately never steers a system message
			// into a live turn (session_worker.go): it queues in the worker's
			// inbox and runs as its own serialized turn after the live one —
			// a live turn waiting on the very delegation whose completion this
			// message carries can therefore never deadlock against it.
			//
			// Internal-channel origins and messages with no resolved origin
			// session have nothing to serialize against and keep the bare
			// goroutine.
			if msg.Channel == "system" && msg.AsyncTranscriptSessionID != "" {
				originChannel := "cli"
				if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
					originChannel = msg.ChatID[:idx]
				}
				if !constants.IsInternalChannel(originChannel) && al.dispatchSystemMessageToSessionWorker(msg, originChannel) {
					continue
				}
			}

			// System messages with no session to serialize against are
			// handled inline in a goroutine (no scope).
			if msg.Channel == "system" {
				// Track in activeRequests so graceful shutdown's
				// WaitForActiveRequests drains this turn before teardown —
				// otherwise its cost.json / session-context writes can outlive
				// RunContext and race temp-dir cleanup (#265, macOS APFS).
				// (The sessionWorker path above intentionally does NOT wrap
				// the message: no other worker-dispatched message is wrapped
				// either; each LLM call inside the turn tracks itself, and
				// Close()/stopSessionWorkers cancels and drains the worker
				// with a 5s budget.)
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
						//
						// TranslateTurnError, not TranslateLLMError(nil, err.Error()):
						// passing the error VALUE keeps the sentinels intact, so a turn
						// refused for a known reason (agent on no workspace) says so
						// instead of falling to the "we can't tell why" copy.
						response = TranslateTurnError(err).Message
					}
					if response != "" {
						al.publishResponseIfNeeded(runCtx, ag, msg.Channel, msg.ChatID, response)
						published = true
					}
				}()
				continue
			}

			// If a worker already exists for this scope AND is not in the
			// middle of exiting, enqueue into it; otherwise spawn one under
			// the admission controller. See dispatchSessionWorker
			// (session_worker.go) — the same helper #505's system-message
			// dispatch uses to serialize async-origin turns against the
			// origin session's worker. On an admission refusal the helper has
			// already published the capacity reply; nothing further to do.
			al.dispatchSessionWorker(scope, msg)
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
	al.sessionWorkers.Range(func(k, v any) bool {
		w, ok := v.(*sessionWorker)
		if !ok {
			logger.ErrorCF("agent", "sessionWorkers: invariant violated — unexpected value type, skipping shutdown for this entry",
				map[string]any{"scope": k, "got_type": fmt.Sprintf("%T", v)})
			return true
		}
		workers = append(workers, w)
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
	// Cancel the loop-lifetime context too, so a revived ordinary-root turn
	// started before Run stored its run-scoped context (loopCtx is
	// inboundRunContext's fallback) is cancelled by the same Stop — never
	// left detached (architect CC-2's cancellation half).
	if al.loopCancel != nil {
		al.loopCancel()
	}
}

// WaitForActiveRequests blocks until all in-flight LLM calls tracked by
// activeRequests have completed. Used by the graceful shutdown sequence to
// ensure active turns finish before the process exits.
func (al *AgentLoop) WaitForActiveRequests() {
	al.activeRequests.Wait()
}

// Close releases resources held by agent session stores. Call after Stop.
func (al *AgentLoop) Close() {
	// Cancel the loop-lifetime context FIRST, so a revived ordinary-root turn
	// running on it stops writing through the stores the teardown below
	// closes (same class as the recap/task/steered-turn drains that follow).
	if al.loopCancel != nil {
		al.loopCancel()
	}
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

	// Drain in-flight task-dispatch goroutines (runTask/runTaskFromInProgress,
	// including any goal-loop redispatch chain they trigger — see
	// TaskExecutor.wg's doc comment) BEFORE tearing down session workers,
	// browser managers, and the stores those goroutines write through —
	// mirrors waitRecapDrain's identical bounded-drain rationale immediately
	// above. Previously Close() never drained TaskExecutor at all, so a
	// still-running task goroutine (or its goal-loop's chain of re-dispatch
	// attempts) could keep writing session/transcript/run-history files after
	// Close() returned, racing a caller's own teardown (e.g. a test's
	// t.TempDir() cleanup removing the directory tree those files live
	// under).
	if al.taskExecutor != nil {
		al.taskExecutor.Drain(30 * time.Second)
	}

	// Drain ADR-091's steered-dispatch front for exactly the reason the two
	// drains above exist: an admitted steered turn runs on a detached
	// goroutine that writes lifecycle, inbox and transcript files, and so
	// does the queue promotion it fires on the way out. Neither was joined
	// by anything, so both outlived Close() and raced a caller's teardown.
	// See admission.go::goSteeredTurn for the full note.
	al.drainSteeredTurns(30 * time.Second)

	// Cancel all active session workers and wait for them to drain (5 s budget).
	// stopSessionWorkers is idempotent — safe to call here even if Run() has
	// already called it on context-cancellation, because workers cancel their
	// own context; a double-cancel is a no-op.
	al.stopSessionWorkers()

	// Drop every browser's manager connection (one per browsing key). In ADR-043
	// shared-Chrome mode this closes each manager's WS connection + detaches
	// its tabs but does NOT kill the Chrome process — that is the coordinator's
	// job, done by coordinator.Shutdown() immediately below (the SOLE process-
	// kill path, MIN-008/FR-008). In the no-coordinator test/legacy path each
	// manager IS its own Chrome owner, so manager.Shutdown() kills its Chrome.
	al.mu.Lock()
	for key, mgr := range al.browserMgrs {
		mgr.Shutdown()
		delete(al.browserMgrs, key)
	}
	al.mu.Unlock()

	// ADR-043: the coordinator owns the ONE shared Chrome process. Per-manager
	// Shutdown() above only dropped each agent's connection (the manager no
	// longer cancels an ExecAllocator in coordinator mode). This is the SOLE
	// process-kill path — disposes every agent's browser context + kills Chrome
	// (MIN-008 / FR-008: Close() is the only kill).
	if al.browserPool != nil {
		al.browserPool.Shutdown()
		al.browserPool = nil
	}
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

	// al.sharedSessionStore (the single UnifiedStore at
	// $OMNIPUS_HOME/sessions/, constructed in NewAgentLoop) is a distinct
	// resource from each AgentInstance's own per-agent session store — the
	// latter is already torn down by AgentInstance.Close() (instance.go), but
	// nothing previously closed this one. Leaving it open leaks its periodic
	// stats-flusher goroutine + live timer (unified_stats_flush.go) for the
	// life of the process, and means a session's very last write (before an
	// unclean-but-Close()'d shutdown) waits out the flush interval instead of
	// being forced to disk — losing token/cost stats for a session that just
	// received its first message. Safe to call even when nil (degraded boot,
	// loop.go's own error-logged "shared session store unavailable" branch).
	if al.sharedSessionStore != nil {
		if err := al.sharedSessionStore.Close(); err != nil {
			logger.ErrorCF("agent", "Failed to close shared session store",
				map[string]any{"error": err.Error()})
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
		cancel, ok := v.(context.CancelFunc)
		if !ok {
			logger.ErrorCF("agent", "idleTickers: invariant violated — unexpected value type, skipping cancel for this entry",
				map[string]any{"session_id": k, "got_type": fmt.Sprintf("%T", v)})
		} else {
			cancel()
		}
		al.idleTickers.Delete(k)
		return true
	})

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

// u9ToolExecSessionIDs computes the identity field ADR-057's W4 stamping
// contract requires on the wire for a session-scoped frame, for the two Go
// event payloads ToolExecStartPayload and ToolExecEndPayload.
//
// SessionID is the producer's own transcript identity. routingSessionID is
// retained only for cascade cancellation and is never a frame destination.
func u9ToolExecSessionIDs(ts *turnState) (sessionID string) {
	if ts == nil {
		return ""
	}
	if ts.transcriptSessionID != "" {
		return ts.transcriptSessionID
	}
	return ts.sessionKey
}

func (al *AgentLoop) hookAbortError(ts *turnState, stage string, decision HookDecision) error {
	reason := decision.Reason
	if reason == "" {
		reason = "hook requested turn abort"
	}

	// curatedTurnError: this text is written here, from a hook's decision —
	// never a provider's response — so a task run may show it as written
	// (turnErrorUserText), exactly as the chat bubble below does.
	err := &curatedTurnError{text: fmt.Sprintf("hook aborted turn during %s: %s", stage, reason)}
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
			SessionID: string(ts.routingSessionID),
		},
	)
	// US-1: persist the hook abort to the JSONL transcript so the
	// replay path re-renders it after page reload (see appendErrorTranscript
	// docstring). Without this, hook aborts vanish on session reopen.
	llm.Message = err.Error()
	ts.appendClassifiedError(EventKindError.String(), "hooks", llm)
	return err
}

func hookDeniedToolContent(prefix, reason string) string {
	if reason == "" {
		return prefix
	}
	return prefix + ": " + reason
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
	if meta, mErr := transcriptStore.GetMeta(sessionID); mErr != nil {
		// FIX 1 (re-review of the re-review): distinguish a real meta-read
		// failure from "no workspace bound" — see resolveWorkspaceIDForContinuation's
		// doc comment above for the full rationale and the WRITE-side standard
		// (pkg/gateway/schedules.go's stampScheduledSessionWorkspace) this matches.
		if !errors.Is(mErr, os.ErrNotExist) {
			logger.WarnCF("agent",
				"scheduled run: could not read session meta while resolving workspace; workspace unresolved",
				map[string]any{"session_id": sessionID, "error": mErr.Error()})
		}
	} else if meta != nil {
		workspaceID = meta.WorkspaceID
	}

	resp, err := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:          sessionKey,
		Channel:             channel,
		ChatID:              chatID,
		UserMessage:         content,
		DefaultResponse:     defaultResponse,
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

// agentLoopProcessMessage carries the shared state of processMessage across its stages.
type agentLoopProcessMessage struct {
	al                  *AgentLoop
	ctx                 context.Context
	msg                 bus.InboundMessage
	sessionKey          string
	transcriptSessionID string
	transcriptStore     *session.UnifiedStore
	workspaceID         string
	opts                processOptions
}

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, *AgentInstance, error) {
	pm := &agentLoopProcessMessage{al: al, ctx: ctx, msg: msg}

	pm.prepareInbound()

	// Route system messages to processSystemMessage
	if pm.msg.Channel == "system" {
		resp, err := pm.al.processSystemMessage(pm.ctx, pm.msg)
		return resp, nil, err
	}

	// ADR-066 D4 / FR-015: the user-message bound, at the one point where an
	// inbound message becomes a turn — BEFORE routing mints a channel session,
	// before the transcript write below, before turn registration. Over the
	// bound, the reply is returned as this message's ordinary response: the
	// caller (session worker / unroutable path) publishes it on the
	// originating channel like any assistant reply — no error frame, no
	// transcript entry, no turn id. Media refs ride in msg.Media and are not
	// counted. See user_message_bound.go.
	if reply, refused := pm.al.refuseOversizedUserMessage(pm.msg); refused {
		logger.InfoCF("agent", "Refused oversized user message before turn start (ADR-066 D4)",
			map[string]any{
				"channel":     pm.msg.Channel,
				"chat_id":     pm.msg.ChatID,
				"size_chars":  UserMessageChars(pm.msg.Content),
				"bound_chars": pm.al.UserMessageBound(),
			})
		return reply, nil, nil
	}

	route, agent, routeErr := pm.al.resolveMessageRoute(pm.msg)
	if routeErr != nil {
		// ADR-029 FR-028/MAJ-003: emit the drift-drop counter and audit event
		// exactly once — here, at the single point where the message is actually
		// rejected.  resolveMessageRoute returns route.Drop=true only for
		// workspace-bound instances whose configured agent is unresolvable
		// (deleted or a worker).  resolveMessageRoute itself is side-effect-free
		// w.r.t. this counter so that its multiple callers (resolveSteeringTarget,
		// buildContinuationTarget) do not double-count the same drop.
		if route.Drop {
			instanceID := inboundInstanceID(pm.msg)
			wsID := ""
			intendedAgent := ""
			if identity := pm.al.resolveInboundIdentity(instanceID); identity != nil {
				intendedAgent = strings.TrimSpace(identity.ID)
			}
			if cfg := pm.al.GetConfig(); cfg != nil {
				if inst, ok := cfg.Channels[instanceID]; ok {
					wsID = inst.WorkspaceID
				}
			}
			pm.al.driftDropped.Add(1)
			if pm.al.auditLogger != nil {
				_ = pm.al.auditLogger.Log(&audit.Entry{
					Event:    audit.EventChannelRoutingDriftDrop,
					Decision: audit.DecisionDeny,
					Details: map[string]any{
						"instance_id":       instanceID,
						"workspace_id":      wsID,
						"intended_agent_id": intendedAgent,
						"chat_id":           pm.msg.ChatID,
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
	scopeKey := resolveScopeKey(route, pm.msg.SessionKey)
	pm.sessionKey = scopeKey

	logger.InfoCF("agent", "Routed message",
		map[string]any{
			"agent_id":      agent.ID,
			"scope_key":     scopeKey,
			"session_key":   pm.sessionKey,
			"matched_by":    route.MatchedBy,
			"route_agent":   route.AgentID,
			"route_channel": route.Channel,
		})

	// For non-webchat channel messages arriving without a session ID, create or
	// resume a persistent shared session so they appear in the session history panel.
	// FR-022 (ADR-029): for a bound channel instance, stamp the session with the
	// instance's workspace_id so it appears linked to the right workspace.
	if pm.msg.SessionID == "" && pm.msg.Channel != "webchat" && pm.msg.Channel != "system" && pm.msg.Channel != "" {
		instanceSessionID := inboundInstanceID(pm.msg)
		sessionWorkspaceID := ""
		if instanceSessionID != "" {
			if sessionCfg := pm.al.GetConfig(); sessionCfg != nil {
				if instCfg, ok := sessionCfg.Channels[instanceSessionID]; ok {
					sessionWorkspaceID = instCfg.WorkspaceID
				}
			}
		}
		if sid := pm.al.resolveOrCreateChannelSession(
			pm.msg.Channel, instanceSessionID, pm.msg.ChatID, agent.ID, pm.msg.Sender.DisplayName, sessionWorkspaceID,
		); sid != "" {
			pm.msg.SessionID = sid
		}
	}

	// Resolve transcript store for tool call recording. SessionID is now
	// authoritative — populated directly by the gateway from frame.SessionID.

	if pm.msg.SessionID != "" {
		pm.transcriptSessionID = pm.msg.SessionID
		pm.transcriptStore = pm.al.ResolveSessionStore(pm.msg.SessionID)
		if pm.transcriptStore == nil {
			logger.WarnCF(
				"agent",
				"session_id present but store not found — tool calls will not be recorded",
				map[string]any{"session_id": pm.msg.SessionID},
			)
		}
	}

	// Write user message to transcript for channel sessions.
	// Web-chat user messages are written by the WebSocket handler before the
	// turn starts (websocket.go); this path covers every other channel
	// (Telegram, Slack, Discord, etc.) so that replays show the user's prompt
	// alongside tool calls and assistant responses.
	channelNeedsTranscript := pm.transcriptStore != nil &&
		pm.msg.Channel != "webchat" && pm.msg.Channel != "system" &&
		strings.TrimSpace(pm.msg.Content) != ""
	if channelNeedsTranscript {
		entry := session.TranscriptEntry{
			ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
			Role:      "user",
			AgentID:   agent.ID,
			Content:   pm.msg.Content,
			Timestamp: time.Now().UTC(),
		}
		if err := pm.transcriptStore.AppendTranscript(pm.transcriptSessionID, entry); err != nil {
			logger.WarnCF("agent", "could not record channel user message to transcript",
				map[string]any{"session_id": pm.transcriptSessionID, "channel": pm.msg.Channel, "error": err.Error()})
		}
	}

	pm.resolveWorkspace()

	pm.prepareTurn()

	// FR-025: reset idle ticker on every user turn, using transcript session ID
	// when available (web-chat sessions). This starts the ticker on the first
	// turn and resets it on every subsequent turn.
	if pm.transcriptSessionID != "" {
		pm.al.resetIdleTicker(pm.transcriptSessionID)
		// FR-024: track current session per agent for lazy CAS on switch.
		pm.al.agentCurrentSession.Store(agent.ID, pm.transcriptSessionID)

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
			cur := agent.Sessions.GetHistory(pm.sessionKey)
			needsHydrate := len(cur) == 0
			if !needsHydrate && pm.transcriptStore != nil {
				hasAssistantOrTool := false
				for _, m := range cur {
					if m.Role == "assistant" || m.Role == "tool" {
						hasAssistantOrTool = true
						break
					}
				}
				if !hasAssistantOrTool {
					if entries, err := pm.transcriptStore.ReadTranscript(pm.transcriptSessionID); err == nil {
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
			// ADR-066 D5.5 (FR-045): this emptiness condition is unchanged;
			// hydration itself now refuses to touch an agent archive that
			// already has lines, so a window that is empty only because Skip
			// reached the end of a non-empty archive is never rebuilt.
			if needsHydrate {
				if err := pm.al.HydrateAgentHistoryFromTranscript(pm.transcriptSessionID); err != nil {
					logger.WarnCF("agent", "self-heal hydrate failed", map[string]any{
						"agent_id":   agent.ID,
						"session_id": pm.transcriptSessionID,
						"error":      err.Error(),
					})
				}
			}
		}
	}

	// context-dependent commands check their own Runtime fields and report
	// "unavailable" when the required capability is nil.
	if response, handled := pm.al.handleCommand(pm.ctx, pm.msg, agent, &pm.opts); handled {
		return response, agent, nil
	}

	// ADR-088 D1/D9: the ADR-074 D4a pending-goal reply-routing hook
	// (applyGoalPendingReply) is retired — goals activate instantly now, so
	// there is no more pending/clarification state for a bare chat message to
	// resolve against (FR-022: bare "confirm" is ordinary chat, no
	// interception exists). Nothing stands between handleCommand above and
	// runAgentLoop below anymore.
	return pm.al.runInboundTurnWithRevival(pm.ctx, agent, pm.msg, pm.opts)
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
	// MERGE NOTE 2026-09-15: integrate's F2 fix called
	// al.rearmGoalAfterAbnormalTurn(opts) here (a goal-bearing session whose
	// turn died on this path never re-armed its idle quiet window and stayed
	// `active` forever). The helper lived in integrate's goal_triggers.go;
	// goal logic comes from release (#683) by founder ruling, so the call is
	// dropped and the fix is recorded as a follow-up candidate.
	if err != nil {
		return "", err
	}
	if result.status == TurnEndStatusAborted {
		// Reached only for a case-1 (user-initiated) hard abort — abortTurn
		// returns a non-nil error for every system-initiated abort (case 2),
		// which the `if err != nil` branch above already returned from. See
		// abortTurn's doc comment for the full case split.
		//
		// A task executor's worker turn (opts.RunningTaskID, set only by
		// processTaskDirect) is the one caller this silence misleads: the task
		// run loop reads a nil error as a turn that ended without a claim and
		// prompts the worker again, spending its goal tries and then a task
		// attempt — a hard Stop restarted the work. It gets the typed stop
		// instead, so task_run_loop.go::finishRunTurn ends the task "Stopped: …"
		// with no attempt and no restart, as it already does for a graceful
		// stop and an external-CLI worker's cancel. The Judge's turns set
		// IsTaskRun but not RunningTaskID and keep the silent unwind.
		if opts.RunningTaskID != "" {
			return "", fmt.Errorf("%w: %w", ErrTurnCanceled, context.Canceled)
		}
		return "", nil
	}

	// ADR-049 D6/D7 (US-8) / ADR-084 revision 9 D13 (JUDGE-FR-098, this
	// wave): judge-gated /goal round advance. Fast no-op unless
	// opts.TranscriptSessionID's session carries an active goal; may append
	// a steering follow-up to result.followUps (published by the loop
	// immediately below exactly like any other follow-up), and — on a
	// resolved `met` claim — records a DEFERRED adjudication on
	// result.goalDeferredAdjudication instead of running the Judge itself.
	al.checkGoalLoopAfterTurn(ctx, agent, opts, &result)

	for _, followUp := range result.followUps {
		if pubErr := al.bus.PublishInbound(ctx, followUp); pubErr != nil {
			logger.WarnCF("agent", "Failed to publish follow-up after turn",
				map[string]any{
					"turn_id": ts.turnID,
					"error":   pubErr.Error(),
				})
		}
	}

	// ADR-091 boundary 3 (FR-B-001): a steered session's
	// final reply is never the user's audience, regardless of
	// SendResponse — this is the re-entered-delegate leak D11 contained by
	// hand (`processSystemMessage`'s SendResponse deny) before this ADR;
	// the permanent form is asking the audience here. audienceFor also
	// calls steer.BoundaryObserver.Observe before this decision is acted on
	// (FR-B-014).
	finalReplyAudience := al.audienceFor(ctx, steer.BoundaryFinalReply, opts.TranscriptSessionID)
	if opts.SendResponse && result.finalContent != "" && finalReplyAudience == steer.AudienceUser {
		// ADR-082 D6/FR-011: carry the transcript session id so
		// webchatChannel.Send (pkg/gateway/webchat_channel.go) can resolve
		// delivery targets by session id first, chat id second — the fix for
		// E5 (keeper-originated turns carrying a stale ChatID whose only
		// live connection may have moved to a different chatID via
		// reconnect/second-tab attach, while the session id stays valid).
		if err := al.bus.PublishOutbound(ctx, bus.OutboundMessage{
			Channel:   opts.Channel,
			ChatID:    opts.ChatID,
			Content:   result.finalContent,
			SessionID: opts.TranscriptSessionID,
		}); err != nil {
			logger.ErrorCF("agent", "Failed to publish outbound response after turn",
				map[string]any{"channel": opts.Channel, "chat_id": opts.ChatID, "error": err.Error()})
		}
	}

	// JUDGE-FR-098 (D13, this wave): dispatch a claim-triggered adjudication
	// ONLY here — strictly after the operator's answer has been published
	// above — and in its own goroutine, so this turn returns to its caller
	// without waiting for the Judge. This is the whole of FR-098's
	// reordering mechanism; the dispatch itself (context.Background()-
	// derived timeout, async-notifier steer delivery) lives in
	// dispatchDeferredGoalAdjudication (goal_loop.go).
	if result.goalDeferredAdjudication != nil {
		work := result.goalDeferredAdjudication
		go al.dispatchDeferredGoalAdjudication(work)
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
	rz := &agentLoopRunTurnFinalize{}

	rz.rc = &agentLoopRunTurnConductor{}

	rz.rc.rx = &agentLoopRunTurnTools{ctx: ctx}

	rz.rc.rx.rr = &agentLoopRunTurnResponse{}

	rz.rc.rx.rr.rq = &agentLoopRunTurnRequest{}

	rz.rc.rx.rr.rq.ri = &agentLoopRunTurnIteration{}

	rz.rc.rx.rr.rq.ri.rf = &agentLoopRunTurnFallbacks{}

	rz.rc.rx.rr.rq.ri.rf.rt = &agentLoopRunTurn{al: al, ts: ts}

	// H1: guard against an already-canceled or timed-out context before doing any work.

	switch rz.rc.createTurnContext() {
	case agentLoopRunTurnConductorReturn:
		return rz.rc.ret0, rz.rc.ret1
	}

	defer rz.rc.turnCancel()

	rz.rc.registerTurnContext()

	// Execution order (LIFO defer — registered in reverse of desired run
	// order): clearActiveTurn runs FIRST, then finalizeStreamer, then
	// Finish LAST.
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
	// finalizeStreamer must still run BEFORE Finish — registered here,
	// between clearActiveTurn and Finish. finalizeStreamer is what calls
	// wsStreamer.Finalize(), which writes the assistant transcript entry
	// (FIX 1). Finish's onCancelFinish callback (pkg/agent/cancel.go) is what
	// writes the turn_canceled entry AND calls MarkLastEntryTruncated to flag
	// the assistant entry. Both depend on the assistant entry already
	// existing in transcript.jsonl:
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
	// repeated Finish calls safe — the cancel path (steering.go's legacy
	// single-session HardAbort/InterruptHard) may have already called
	// Finish(true) directly; this deferred call is then a harmless repeat of
	// the same idempotent operation.
	//
	// FIX 2: pass ts.hardAbortRequested(), not a hardcoded false. The
	// session-wide web-cancel escalation path (InterruptSessionHard,
	// steering.go) hard-aborts a turn via ts.requestHardAbort() ALONE — it
	// never calls ts.Finish(true) itself. For a turn hard-aborted that way,
	// THIS deferred call is the ONLY Finish call that ever happens, so a
	// hardcoded false silently mislabeled a genuine hard abort as a graceful
	// finish: the onCancelFinish callback above computes cancelMethod from
	// Finish's own isHardAbort argument, and that value is persisted verbatim
	// as TranscriptEntry.CancelMethod, which pkg/gateway/replay.go renders
	// back to the user as "Turn canceled (%s)" — so a hard-aborted turn was
	// always reported to the user, and audited, as a graceful cancel. Must be
	// a closure, not a bare `defer ts.Finish(ts.hardAbortRequested())`: Go
	// evaluates deferred arguments at the defer statement's registration time
	// (here, before the turn even starts), not when the deferred call
	// actually runs at function exit — a bare form would always capture
	// false and reproduce the exact bug this fixes.
	defer func() { rz.rc.rx.rr.rq.ri.rf.rt.ts.Finish(rz.rc.rx.rr.rq.ri.rf.rt.ts.hardAbortRequested()) }()
	defer rz.rc.rx.rr.rq.ri.rf.rt.ts.finalizeStreamer(rz.rc.rx.ctx)
	// ADR-087 D6.8, the ONE choke point (see preserveTruncatedAccumulator's
	// doc comment). REGISTRATION ORDER IS LOAD-BEARING, the same way the
	// markTurnFailed defer below is: Go runs defers LIFO, so registering this
	// AFTER `defer ts.finalizeStreamer(ctx)` makes it run BEFORE that call —
	// which is the only ordering that works, because on a streamed turn this
	// function merely SETS ts.finalContent/ts.truncationReason and
	// finalizeStreamer is what hands them to the streamer. Registering it
	// above finalizeStreamer's would silently drop the annotation on every
	// webchat turn. A `defer` (rather than a call at each exit) is the point:
	// runTurn returns from dozens of places inside turnLoop, and the five
	// that were hand-wired were not the ones that mattered.
	defer rz.rc.rx.rr.rq.ri.rf.rt.al.preserveTruncatedAccumulator(rz.rc.rx.rr.rq.ri.rf.rt.ts)
	defer rz.rc.rx.rr.rq.ri.rf.rt.al.clearActiveTurn(rz.rc.rx.rr.rq.ri.rf.rt.ts)

	rz.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusCompleted
	defer func() {
		rz.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindTurnEnd,
			rz.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.end"),
			TurnEndPayload{
				Status:          rz.rc.rx.rr.rq.ri.turnStatus,
				Iterations:      rz.rc.rx.rr.rq.ri.rf.rt.ts.currentIteration(),
				Duration:        time.Since(rz.rc.rx.rr.rq.ri.rf.rt.ts.startedAt),
				FinalContentLen: rz.rc.rx.rr.rq.ri.rf.rt.ts.finalContentLen(),
				ChatID:          rz.rc.rx.rr.rq.ri.rf.rt.ts.chatID,
				// ADR-057 FR-012 (W4/U9): the "done" frame is one of the 19
				// SESSION_SCOPED_FRAME_TYPES (src/store/chat.ts), so its wire
				// session_id MUST come from the ROUTING identity — the id
				// inherited verbatim from the root of the delegation subtree
				// — not this turn's own store-backed transcriptSessionID,
				// which for a delegated child differs from the root's. No
				// The frame is keyed by this producing turn's routing identity;
				// the payload carries no second session identity.
				SessionID: string(rz.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
				IsRoot:    rz.rc.rx.rr.rq.ri.rf.rt.ts.parentTurnID == "",
			},
		)
	}()

	// A turn that ended in error must never close by claiming success.
	//
	// The "done" WS frame is emitted by the streamer reached through the
	// unconditional `defer ts.finalizeStreamer(ctx)` registered above, which
	// fires on EVERY return path — including the LLM-error early returns.
	// Those paths set turnStatus = TurnEndStatusError but did NOT call
	// markTurnFailed(), so the frame went out with DoneStats.TurnFailed
	// absent — byte-for-byte what a successful turn looks like on the wire.
	// Live consequence: a provider's HTTP 429 produced a turn that opened,
	// streamed nothing, and reported success. It read as an agent ignoring
	// the user, with nothing to retry from.
	//
	// The trigger is deliberately NARROW: turnStatus == TurnEndStatusError,
	// nothing else. Emptiness is explicitly NOT a failure signal — a prior
	// investigation enumerated eight legitimate zero-output cases (shadow
	// sub-turn, user cancel/hard abort, abandoned turn, heartbeat with a
	// caller-supplied success DefaultResponse, silent tool-only turn,
	// SendResponse=false/NoHistory, client disconnected mid-stream, and the
	// media-rejection friendly-response path) that a "no output = failed"
	// guard would wrongly flag. None of the eight can reach this branch:
	// every one of them exits with turnStatus Completed, Aborted, or Parked.
	// TurnEndStatusAborted is likewise left alone — a user-initiated cancel
	// is an intentional, successful action, and a system-initiated abort
	// (abortTurn case 2) already emits its own EventKindError, so the user
	// is never left in silence there.
	//
	// ORDERING IS LOAD-BEARING. Go runs defers LIFO, so this — registered
	// AFTER the turn_end defer above and after finalizeStreamer's — runs
	// FIRST, before finalizeStreamer reads ts.turnFailed and hands it to the
	// streamer's SetTurnFailed. Registering it any earlier than
	// `defer ts.finalizeStreamer(ctx)` would make it run too late and
	// silently restore the bug — verified by mutation: moving this
	// registration above finalizeStreamer's turns
	// TestWS_ProviderRateLimitRefusal_DoneFrameDoesNotClaimSuccess red again.
	defer func() {
		if rz.rc.rx.rr.rq.ri.turnStatus == TurnEndStatusError {
			rz.rc.rx.rr.rq.ri.rf.rt.ts.markTurnFailed()
		}
	}()

	switch rz.rc.prepareTurn() {
	case agentLoopRunTurnConductorReturn:
		return rz.rc.ret0, rz.rc.ret1
	}

	// midTurnGuardErr carries the ADR-066 D6 thrash-guard error out of the
	// per-result mid-turn window checks below (midturn_budget.go). Declared
	// before the turnLoop label so the `goto turnLoop` below never jumps
	// over its declaration.

	switch rz.rc.runIterations() {
	case agentLoopRunTurnConductorReturn:
		return rz.rc.ret0, rz.rc.ret1
	}

	return rz.finalizeTurn()
}

// hardInterruptAbortReason is the abort reason used when a turn is
// hard-aborted via ts.requestHardAbort() (InterruptHard/InterruptSessionHard,
// reached from the turn loop's ts.hardAbortRequested() checks) rather than by
// a hook decision or the aggregate tool-denial budget (ADR-058,
// toolDenialAbortReason), neither of which have a more specific reason
// string available at the call site.
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

// typedTurnExit (ADR-066 D7, FR-034, SC-006) finalizes a turn that ended
// because its context was cancelled or its deadline expired while waiting on
// the provider. These were runTurn's four SILENT return sites — a bare
// fmt.Errorf("turn canceled") / ("turn timed out") with no log line, no
// EventKindError and no transcript entry, so the user saw nothing and the
// session worker rendered the "we can't tell why" copy.
//
// A root-turn typed exit produces the three SC-006 artefacts:
//   - one log line carrying the typed code AND the raw cause (operator triage),
//   - one EventKindError carrying the typed code (live wire; the deferred
//     EventKindTurnEnd in runTurn fires on return with the status returned
//     here — TurnEndStatusAborted for a cancel, which is an intentional user
//     action and must not mark the turn failed; TurnEndStatusError for a
//     timeout),
//   - one transcript entry with the typed code (replay).
//
// A delegated child timeout is the exception: typedTurnExit records the
// child-local transcript entry but leaves live publication to the session
// completion path.
// The coordinator waits until it has either disarmed the delegation timer or
// observed its completed callback before choosing either the generic
// child-timeout frame or the identified delegated-task-limit frame. That
// ordering prevents a timer callback racing this exit from publishing both.
//
// The returned error wraps BOTH the sentinel (ErrTurnCanceled /
// ErrTurnTimedOut) and the raw cause, so runAgentLoop / processMessage /
// session_worker callers that errors.Is the context error keep working and
// TranslateTurnError classifies the chain to the same code. Never `unknown`.
// ADR-087 D6.8 is NOT re-implemented here: every typed exit returns out of
// runTurn, whose deferred preserveTruncatedAccumulator is the single choke
// point that keeps an unresolved D6 continuation. The call this function used
// to make itself was one of the five hand-wired ones the defer replaced.
func (al *AgentLoop) typedTurnExit(ts *turnState, iteration int, llmModel string, cause error) (turnResult, TurnEndStatus, error) {
	code, ok := typedExitCode(cause)
	if !ok {
		// Not a typed exit — callers only route context errors here; fall
		// back to the cancel shape rather than inventing an `unknown`.
		code = CodeTurnCanceled
	}
	var (
		sentinel = ErrTurnCanceled
		status   = TurnEndStatusAborted
		level    = logger.WarnCF
	)
	switch code {
	case CodeTurnTimedOut:
		sentinel, status = ErrTurnTimedOut, TurnEndStatusError
	case CodeContextUnrecoverable:
		sentinel, status, level = ErrContextUnrecoverable, TurnEndStatusError, logger.ErrorCF
	}
	llm := typedExitError(code, cause)

	if code == CodeTurnTimedOut && ts.parentTurnState != nil {
		// The delegation coordinator owns live publication after it settles
		// timer ownership. Keep the child's private terminal record here.
		ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
	} else {
		al.emitTurnErrorFrame(ts, ts.eventMeta("runTurn", "turn.error"), "llm", "runTurn", llm)
	}
	level("agent", "Turn exited: "+string(code), map[string]any{
		"agent_id":  ts.agent.ID,
		"iteration": iteration,
		"model":     llmModel,
		"code":      string(code),
		"cause":     cause.Error(),
	})
	return turnResult{status: status}, status, fmt.Errorf("%w: %w", sentinel, cause)
}

// emitTurnErrorFrame emits one EventKindError frame for a turn that ended on a
// classified error, and records the same error in that turn's transcript so a
// reload re-renders it. payloadStage is the frame's Stage; transcriptStage is
// the transcript entry's stage.
//
// ADR-057 FR-014: emitErrorEvent is the WS-payload-stamping consumer of
// routingSessionID. The frame's SessionID is the routing session a second tab
// or reload is attached to; a webchat ChatID alone is a dead per-connection
// id. Code that needs the id for another purpose must justify that read
// against routing_session_id_consumer_set_adr057_test.go.
func (al *AgentLoop) emitTurnErrorFrame(
	ts *turnState, meta EventMeta, payloadStage, transcriptStage string, llm LLMError,
) {
	al.emitErrorEvent(ts, meta, payloadStage, llm)
	ts.appendClassifiedError(EventKindError.String(), transcriptStage, llm)
}

func (al *AgentLoop) emitErrorEvent(ts *turnState, meta EventMeta, stage string, llm LLMError) {
	sessionID := u9ToolExecSessionIDs(ts)
	if al.audienceFor(context.Background(), steer.BoundaryTypedErrorFrame, sessionID) == steer.AudienceNone {
		return
	}
	al.emitEvent(EventKindError, meta, ErrorPayload{
		Stage:     stage,
		ChatID:    ts.opts.ChatID,
		Code:      string(llm.Code),
		Message:   llm.Message,
		SessionID: sessionID,
	})
}

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
//   - any other reason (the aggregate tool-denial budget's
//     toolDenialAbortReason, ADR-058, or a hook's decision.Reason): a
//     system-initiated abort — e.g. the turn exhausting its per-turn tool-
//     denial budget or a hook's HookActionHardAbort decision. Synthesizes a
//     real, non-nil error carrying stage + reason, mirroring
//     hookAbortError's shape; emits an error event; and appends it to the
//     transcript so the user (and replay) learn why the turn ended.
//     runAgentLoop's existing `if err != nil { return "", err }` branch
//     propagates it, and session_worker.go's processTurn turns it into the
//     terminal user-facing frame every channel already knows how to render
//     (rather than silently dropping the user with no explanation).
//
// ADR-087 D6.8 is NOT re-implemented here either (see typedTurnExit): a hard
// cancel or system-initiated abort returns out of runTurn, and runTurn's
// deferred preserveTruncatedAccumulator is the single choke point that keeps
// an unresolved D6 continuation.
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
					Stage:     "session_restore",
					Code:      string(restoreLLM.Code),
					Message:   restoreLLM.Message,
					ChatID:    ts.opts.ChatID,
					SessionID: string(ts.routingSessionID),
				},
			)
			ts.appendClassifiedError(EventKindError.String(), "session_restore", restoreLLM)
			// Restore failed, so the rest of abortTurn cannot run. The
			// live error is the restore failure (already emitted). If
			// this was a system-initiated abort, also persist that
			// reason so replay is not restore-only. Do not emit a
			// second live abort frame.
			if reason != hardInterruptAbortReason {
				if reason == "" {
					reason = "no reason provided"
				}
				abortErr := fmt.Errorf("turn aborted during %s: %s", stage, reason)
				abortLLM := TranslateLLMError(nil, abortErr.Error())
				presented := abortErr.Error()
				if abortLLM.Code != CodeUnknown {
					presented = abortLLM.Message
				}
				abortLLM.Message = presented
				ts.appendClassifiedError(EventKindError.String(), stage, abortLLM)
			}
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
	// aggregate tool-denial budget, ADR-058) — synthesize a real, surfaced
	// error.
	if reason == "" {
		reason = "no reason provided"
	}
	// curatedTurnError: the stage and a hook's or the tool-denial budget's own
	// reason — never a provider's response — so a task run may show it as
	// written (turnErrorUserText), exactly as the event payload below does.
	err := &curatedTurnError{text: fmt.Sprintf("turn aborted during %s: %s", stage, reason)}
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
			Stage:     stage,
			Code:      string(abortLLM.Code),
			Message:   presented,
			ChatID:    ts.opts.ChatID,
			SessionID: string(ts.routingSessionID),
		},
	)
	abortLLM.Message = presented
	ts.appendClassifiedError(EventKindError.String(), stage, abortLLM)
	return turnResult{status: TurnEndStatusAborted}, err
}

// abortTurnForToolDenialBudget is the ONE place a turn is aborted for
// exhausting its aggregate per-turn tool-denial budget (ADR-058 FR-058-13,
// turnDenialBudget = 10). All four call sites that can exhaust the budget —
// the quarantine-gate replay and the three permission_denied emit sites —
// route through this single function rather than each constructing its own
// abort, so the audit entry and the abort reason can never diverge between
// them (the same "one renderer" discipline this ADR uses for the denial
// payload itself).
//
// Replaces FR-084's now-deleted per-turn synthetic-deny helper, which
// emitted its own audit entry immediately before calling abortTurn (§10.A3,
// spec §3.3): this does the same, with the new
// audit.EventTurnAbortedToolDenialBudget event in place of FR-084's retired
// audit event constant.
func (al *AgentLoop) abortTurnForToolDenialBudget(ts *turnState, tool, reason string, denialsUsed int) (turnResult, error) {
	audit.EmitEntry(al.auditLogger, &audit.Entry{
		Event:     audit.EventTurnAbortedToolDenialBudget,
		Decision:  audit.DecisionDeny,
		AgentID:   ts.agentID,
		Tool:      tool,
		SessionID: ts.sessionKey,
		User:      ts.auditUser(), // FR-017
		Details: map[string]any{
			"turn_id":       ts.turnID,
			"denial_reason": reason,
			"denials_used":  denialsUsed,
			"budget":        turnDenialBudget,
		},
	})
	logger.WarnCF("agent", "ADR-058: aggregate tool-denial budget exhausted — aborting turn",
		map[string]any{
			"agent_id":     ts.agentID,
			"session_key":  ts.sessionKey,
			"tool":         tool,
			"reason":       reason,
			"denials_used": denialsUsed,
			"budget":       turnDenialBudget,
		})
	return al.abortTurn(ts, "tool_denial_budget", toolDenialAbortReason(tool, reason, ts.agentID, turnDenialBudget))
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

// clearSessionWindow implements /new (alias /clear): it empties the live
// window while preserving the archive.
//
// It clears with the Skip-advancing primitive, NOT SetHistory. ADR-066 FR-047
// narrowed SetHistory to a first-fill primitive — an archive-backed store
// REFUSES it once the archive holds >= 1 line (memory.ErrArchiveNotEmpty) —
// and because SessionWriter.SetHistory is fire-and-forget the refusal is
// swallowed into a slog.Error. /clear therefore answered "Chat history
// cleared!" on CLI and every channel while clearing nothing at all, and the
// next message was answered with the whole prior conversation still in the
// window.
//
// TruncateHistory(key, 0) sets Skip = Count: the live window is empty, the
// JSONL archive is untouched (recall by tool_call_id still resolves), and the
// projection entries below the new Skip are pruned in the same meta write.
func clearSessionWindow(sessions session.SessionStore, sessionKey string) error {
	sessions.TruncateHistory(sessionKey, 0)
	return sessions.Save(sessionKey)
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
	// Any agent's provider will do — they all borrow the same one. Ask the
	// default first because it is the cheapest lookup, then any registered
	// agent, then the registry's own.
	if defaultAgent := registry.GetDefaultAgent(); defaultAgent != nil && defaultAgent.Provider != nil {
		return defaultAgent.Provider, true
	}
	for _, id := range registry.ListAgentIDs() {
		if ag, ok := registry.GetAgent(id); ok && ag != nil && ag.Provider != nil {
			return ag.Provider, true
		}
	}
	// No agents at all. Before ADR-064 this was unreachable: the "main"
	// sentinel was always registered, so a provider was always reachable
	// through it. Removing the sentinel made an empty registry real, and
	// UpsertAgentFast started failing here on first-agent creation — its
	// callers then fell back to a full config reload, the restartServices
	// cascade issue #571 exists to keep off this path.
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if registry.provider != nil {
		return registry.provider, true
	}
	return nil, false
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

// emitToolLoadPolicyDenyAudit writes the tool.policy.deny_attempted row for a
// tool whose LOAD (ToolSearch) was refused by the calling agent's policy
// (UAT 2026-09-13 D-84). Same event and decision as emitPolicyDenyAudit's
// dispatch-time deny, so one audit query finds both; Details.context names
// which gate refused. Nil audit logger is a no-op (audit.EmitEntry).
func (al *AgentLoop) emitToolLoadPolicyDenyAudit(ctx context.Context, agentID, toolName string) {
	audit.EmitEntry(al.auditLogger, &audit.Entry{
		Event:     audit.EventToolPolicyDenyAttempted,
		Decision:  audit.DecisionDeny,
		AgentID:   agentID,
		Tool:      toolName,
		SessionID: tools.ToolTranscriptSessionID(ctx),
		Details: map[string]any{
			"resolved_policy": "deny",
			"context":         "tool_load",
		},
	})
}

// buildScratchpadNote returns a short ephemeral system note for the agent's
// current set_todos scratchpad. It queries the task store for the most-recent
// active goal-task that has todos assigned to agentID, renders them, and
// returns the note string. Returns "" when there is nothing to inject (no
// store, no active goal-task with todos). This is rebuilt fresh every turn so
// it survives context compression without polluting the persisted transcript.
//
// sessionID scopes the note to the checklist the CURRENT session's own
// set_todos calls created (Task.OriginSessionID). Without the scoping the note
// carried the agent's most recent open checklist from ANY session or workspace
// (UAT B-1 runs 3 and 5), which the model treated as this conversation's
// context. An empty sessionID keeps the pre-scoping agent-wide selection.
func (al *AgentLoop) buildScratchpadNote(agentID, sessionID string) string {
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
		if sessionID != "" && tk.OriginSessionID != sessionID {
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
