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
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/policy"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/state"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/utils"
	"github.com/elicify-ai/omnipus/pkg/voice"
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
	// openSubTurnSpans counts, per parentSpawnCallID, the sub-turn spans whose
	// EventKindSubTurnSpawn has been emitted and whose EventKindSubTurnEnd has
	// not been emitted yet. Guarded by subTurnSpansMu, allocated lazily. See
	// markSubTurnSpanOpen (steering.go) for why liveness needs it.
	subTurnSpansMu     sync.Mutex
	openSubTurnSpans   map[string]int
	subTurnCounter     atomic.Int64 // Counter for generating unique SubTurn IDs
	sessionActiveAgent sync.Map     // key: "session:"+sessionID (string), value: agentID (string); set by handoff, cleared on agent deletion
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

	// delegateTools are the per-agent DelegateTool instances this loop built,
	// retained for ONE reason: Close() has to drain their background (async=true)
	// delegation goroutines before the stores those goroutines write through are
	// torn down.
	//
	// DelegateTool has always exposed WaitForAsyncTasks for exactly this, and its
	// doc comment names both callers that need it — "tests rooted at t.TempDir()"
	// and "a graceful-shutdown path that swaps stores". Neither ever called it:
	// the tools were constructed as locals in registerAgentTools and dropped, so
	// Close() had no handle to drain and its own promise that "nothing writes
	// after Close() returns to race temp-dir cleanup" was false for background
	// delegation specifically. The symptom is a cleanup-time failure in whichever
	// test happens to lose the race ("TempDir RemoveAll cleanup: ... directory not
	// empty"), attributed to that test rather than to the missing drain.
	delegateToolsMu sync.Mutex
	delegateTools   []*tools.DelegateTool

	// stopCancel is the CancelFunc created by Run to support Stop(). When
	// Stop() is called it cancels this func so the Run select wakes
	// immediately without waiting for the next message or ticker. Stored
	// atomically via the atomic.Pointer so Stop() can be called before Run.
	stopCancel atomic.Pointer[context.CancelFunc]

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

	// rootDelegationAdmission is the ADR-057 W17 (FR-069/FR-070/FR-095)
	// process-wide gate for ROOT-level `delegate` fan-out — see admission.go's
	// "ADR-057 W17" block for the full rationale. Constructed exactly once in
	// NewAgentLoop from agents.defaults.subturn.max_concurrent (unclamped) and
	// shared by every per-agent DelegateTool's wrapped spawner
	// (rootDelegationAdmittingSpawner, registerSharedTools' delegate-tool
	// block) so the cap is enforced once for the whole running process, not
	// per agent. Always non-nil after successful construction — NewAgentLoop
	// fails closed (returns an error) rather than proceeding with a nil gate,
	// per ErrRootDelegationCapMisconfigured's doc comment.
	rootDelegationAdmission *RootDelegationAdmission

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
	UserID                  string                // Authenticated gateway principal (FR-017)
	UserMessage             string                // User message content (may include prefix)
	ForcedSkills            []string              // Skills explicitly requested for this message
	Media                   []string              // media:// refs from inbound message
	InitialSteeringMessages []providers.Message   // Steering messages from refactor/agent
	DefaultResponse         string                // Response when LLM returns empty
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
	// processTaskDirect, processTaskDirectExternalCLI, processSystemMessage,
	// spawnSubTurn — leaves this at its zero value (false), which is the
	// correct fail-closed answer for every one of those non-user origins.
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

// Orphan tool-call markup repair — see the choke point in runTurn and
// providers.DetectOrphanToolCallMarkup for the failure this handles.
const (
	// maxOrphanToolMarkupRepairs bounds the re-prompts spent on one turn.
	// Two is enough to clear a one-off upstream parse failure (the observed
	// case) without letting a model that cannot produce structured tool calls
	// at all spend the whole iteration budget getting nowhere.
	maxOrphanToolMarkupRepairs = 2
	// orphanToolMarkupRetryReason labels the retry on the event bus so an
	// operator can tell this apart from an empty-response retry.
	orphanToolMarkupRetryReason = "orphan_tool_markup"
	// orphanToolMarkupStage labels the terminal error event/transcript entry.
	orphanToolMarkupStage = "orphan_tool_markup"
)

// truncationSuccessAction is evaluateTruncatedSuccess's verdict — see its
// doc comment for what each caller must do.
type truncationSuccessAction int

const (
	// truncationActionNone: the guard did not match (not truncated, or the
	// response carried tool calls) — the caller's existing logic runs
	// completely unchanged.
	truncationActionNone truncationSuccessAction = iota
	// truncationActionContinue: D6 — the caller must set `messages` to
	// verdict.messages and `continue turnLoop`.
	truncationActionContinue
	// truncationActionEnd: D4a or D4b — the caller must set finalContent to
	// verdict.finalContent and `break turnLoop`.
	truncationActionEnd
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
// applies uniformly to top-level and delegated (spawnSubTurn) turns alike,
// since both resolve ts.agent.ID the same way in the re-root block below.
var ErrAgentNotWorkspaceMember = errors.New("agent is not a member of any workspace; turn refused")

// ErrWorkspaceWorkDirUnavailable is returned when the agent belongs to a
// workspace but its work/ directory could not be created or opened.
var ErrWorkspaceWorkDirUnavailable = errors.New("workspace work directory unavailable")

// ErrAgentHomeUnavailable is returned when a system agent's private home
// directory is missing or could not be created.
var ErrAgentHomeUnavailable = errors.New("agent home directory unavailable")

// ErrAgentNeedsProvider is returned by runTurn when the agent's PRIMARY
// provider id is UNKNOWN (ADR-067 FR-016/FR-038): neither a catalog id nor a
// constructible custom row — including an id that differs from a configured
// one only by case, which is exact-compared and therefore unknown (FR-036).
//
// The turn is refused with LLMError code needs_provider (attribution
// `config`), logged at WARN, and ZERO upstream requests are made. It is
// evaluated FIRST in the pre-turn gate, ahead of ADR-068's model_unassigned
// and ADR-066's ErrContextWindowUnknown: a provider must exist before a model
// can, and a model must exist before its window can be sized.
//
// The refusal clears the moment the operator re-points the agent at a real
// provider through the existing agent-update path — no restart beyond the
// reload that path already triggers (US-6.AC3).
var ErrAgentNeedsProvider = errors.New("agent's provider is not configured; turn refused")

// ErrAgentModelUnassigned is returned by runTurn when the agent has no model
// to send the request to (ADR-068 FR-014/FR-015, MAJ-008). Two shapes reach
// it, and they are exactly the two halves of the derived `needs_model` the
// gateway projects onto Agent.needs_model:
//
//   - the agent pins no primary model and `agents.defaults.default_model`
//     names none either, so there is literally nothing to call; or
//   - the model it does pin routes through a provider that is not configured,
//     which is ADR-067's `needs_provider` state — and that code WINS, because
//     the pre-turn gate evaluates it first (see below).
//
// The turn is refused with LLMError code model_unassigned (attribution
// `config`) and ZERO upstream requests are made. It is evaluated SECOND in
// the pre-turn gate: after ADR-067's ErrAgentNeedsProvider (a provider must
// exist before a model can) and before ADR-066's ErrContextWindowUnknown (a
// model must exist before its window can be sized). The overlap is not
// hypothetical — an agent bound to an unknown provider satisfies BOTH
// predicates, and SC-013 requires it to end with `needs_provider`.
//
// The refusal clears as soon as the operator assigns a model through the
// existing agent-update path, which triggers its own reload.
var ErrAgentModelUnassigned = errors.New("agent has no model assigned; turn refused")

// ErrContextWindowUnknown is returned by runTurn when the agent's provider is
// a `locality: local` endpoint that reported no context window and no
// operator override exists (ADR-066 D3, FR-008). The turn is refused — never
// run on a guessed window — with LLMError code context_window_unknown
// (attribution config). It is evaluated THIRD in the pre-turn gate, after
// ADR-067's needs_provider and ADR-068's model_unassigned. Setting
// ContextSettings.model_overrides[] for the (provider, model) triggers a
// reload and clears it without a restart.
var ErrContextWindowUnknown = errors.New("context window unknown for this model; turn refused")

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

	if r0, r1, stop := nal.initializeAudit(); stop {
		return r0, r1
	}

	nal.initializeSecurity()

	return nal.initializeRuntime()
}

// initializeCore builds the registry, shared routing state, task executor, and session store.
func (nal *newAgentLoop) initializeCore() {
	nal.registry = NewAgentRegistry(nal.cfg, nal.provider)

	// Apply configurable default agent override.
	if nal.cfg.Agents.Defaults.DefaultAgentID != "" {
		nal.registry.SetDefaultAgentOverride(nal.cfg.Agents.Defaults.DefaultAgentID)
	}

	// Set up shared fallback chain with per-candidate timeout so a primary
	// provider timeout does not strand fallback candidates with an exhausted
	// context deadline (#235).
	cooldown := providers.NewCooldownTracker()
	fallbackChain := providers.NewFallbackChainWithTimeout(cooldown, perCandidateTimeoutFromConfig(nal.cfg))

	// Create state manager using default agent's workspace for channel recording
	defaultAgent := nal.registry.GetDefaultAgent()
	var stateManager *state.Manager
	if defaultAgent != nil {
		stateManager = state.NewManager(defaultAgent.Home)
	}

	// ADR-057 W17: a boot-time diagnostic only — genuine construction of the
	// root-delegation admission gate happens AFTER al exists, below, via a
	// LIVE resolver (concurrency-gate consolidation, 2026-08-04). A NEGATIVE
	// agents.defaults.subturn.max_concurrent is the only case
	// ResolveRootDelegationCap treats as an error (an unset/zero value now
	// resolves straight to the central Performance.EffectiveMaxParallelAgents()
	// authority, not an error — see ResolveRootDelegationCap's doc comment).
	// Logged loudly here so a genuine operator misconfiguration is
	// diagnosable at boot; does not abort construction, since the live
	// resolver's own error branch (below) keeps the gate GATED at the
	// central value either way, never nil (nil would mean UNLIMITED root
	// fan-out — the "silently reinterpreted as no gate" outcome ADR-037
	// bans).
	if _, err := ResolveRootDelegationCap(nal.cfg); err != nil {
		logger.ErrorCF("agent",
			"agents.defaults.subturn.max_concurrent is configured to a negative value — the root-delegation admission gate falls back to the central Performance.EffectiveMaxParallelAgents() authority; set it to 0 (inherit the central value) or a positive explicit override",
			map[string]any{"error": err.Error()})
	}

	eventBus := NewEventBus()
	nal.al = &AgentLoop{
		bus:                     nal.msgBus,
		cfg:                     nal.cfg,
		registry:                nal.registry,
		state:                   stateManager,
		eventBus:                eventBus,
		fallback:                fallbackChain,
		cmdRegistry:             commands.NewRegistry(commands.BuiltinDefinitions()),
		steering:                newSteeringQueue(parseSteeringMode(nal.cfg.Agents.Defaults.SteeringMode)),
		contextBuilderRegistry:  NewContextBuilderRegistry(),
		loadedTools:             make(map[string]map[string]bool),
		pendingSearchPromotions: make(map[string]map[string]int),
		bucketTurnCounter:       make(map[string]int),
		browserMgrs:             make(map[string]*browser.BrowserManager),
		browserRegisteredAgents: make(map[string]bool),
	}
	// Concurrency-gate consolidation (2026-08-04): session admission's cap is
	// resolved LIVE from the SAME central authority TaskExecutor's dispatch
	// semaphore uses (Performance.EffectiveMaxParallelAgents), instead of the
	// former independent, hardcoded runtime.NumCPU()*4 soft cap — see
	// AdmissionController.resolveCap's doc comment (admission.go) for why
	// this must be resolved fresh on every check rather than cached once
	// here at construction time.
	nal.al.admission = newAdmissionControllerWithResolver(func() int {
		n, _ := nal.al.GetConfig().Performance.EffectiveMaxParallelAgents()
		return n
	})
	// ADR-057 W17, same live-resolution treatment: root-level delegate()
	// fan-out must never drift from the central authority either. On
	// ResolveRootDelegationCap's error branch (a NEGATIVE configured value)
	// this falls back directly to EffectiveMaxParallelAgents() so the gate
	// stays GATED at the central value rather than degrading to unlimited.
	nal.al.rootDelegationAdmission = newRootDelegationAdmissionWithResolver(func() int {
		liveCfg := nal.al.GetConfig()
		if resolvedCap, capErr := ResolveRootDelegationCap(liveCfg); capErr == nil {
			return resolvedCap
		}
		if liveCfg != nil {
			n, _ := liveCfg.Performance.EffectiveMaxParallelAgents()
			return n
		}
		return 1
	})
	nal.al.hooks = NewHookManager(eventBus)
	configureHookManagerFromConfig(nal.al.hooks, nal.cfg)

	// Initialize the unified task store at ~/.omnipus/tasks/ (the single store —
	// the legacy GTD tasks/ and workflow-tasks/ split was removed in Sprint 2).
	nal.homePath = filepath.Dir(nal.cfg.AgentHomeBasePath())
	nal.al.homePath = nal.homePath
	nal.al.taskStore = task.New(filepath.Join(nal.homePath, "tasks"))
	nal.al.taskExecutor = newTaskExecutor(nal.al, nal.al.taskStore)
	// Founder decision 2026-09-14: expose the running task's live progress
	// stamp (reasoning counts) so the REST tasks surface can stamp
	// Task.last_activity_at. The loop is the authority; the executor is the
	// holder because the REST side already reaches the task engine here.
	nal.al.taskExecutor.SetLiveTaskActivitySource(nal.al)

	// Initialize shared session store at $OMNIPUS_HOME/sessions/.
	// All new chat sessions are created here (joined session model).
	sharedDir := filepath.Join(nal.homePath, "sessions")
	if err := os.MkdirAll(sharedDir, 0o700); err != nil {
		logger.ErrorCF("agent", "Shared session store unavailable — new sessions will use per-agent stores",
			map[string]any{"dir": sharedDir, "error": err.Error()})
	} else {
		sharedStore, ssErr := session.NewUnifiedStoreWithHome(sharedDir, nal.homePath)
		if ssErr != nil {
			logger.ErrorCF("agent", "Shared session store init failed — new sessions will use per-agent stores",
				map[string]any{"dir": sharedDir, "error": ssErr.Error()})
		} else {
			nal.al.sharedSessionStore = sharedStore
			// FR-067/SC-048 (ADR-057): apply the operator's resolved
			// stats-flush override onto the store's live periodic flusher.
			// Without this call, startStatsFlusher (unified_stats_flush.go,
			// invoked unconditionally from NewUnifiedStoreWithHome) always
			// runs on the hardcoded config.DefaultSessionStatsFlushInterval
			// (5s) constant — a seeded, documented
			// sessions.stats_flush_interval key in config.json would persist
			// but have zero runtime effect. cfg.Session.
			// EffectiveStatsFlushInterval() resolves the operator's value (or
			// the same 5s default when unset), exactly matching
			// startStatsFlusher's own doc comment naming this call site.
			sharedStore.SetStatsFlushInterval(nal.cfg.Session.EffectiveStatsFlushInterval())
			nal.al.rebuildChannelSessionIndex()
		}
	}
}

// initializeAudit constructs audit logging and wires it into registries when enabled.
func (nal *newAgentLoop) initializeAudit() (*AgentLoop, error, bool) {
	// SEC-15: Initialize structured audit logging (ON by default since the
	// 2026-09-11 founder decision — see cfg.Sandbox.AuditLog's doc comment)
	// and policy evaluation (always on). Audit directory is ~/.omnipus/system/
	// (sibling of workspace). The audit logger and the policy evaluator are
	// decoupled: disabling audit logging must NOT disable enforcement.
	if nal.cfg.Sandbox.AuditLog {
		auditDir := filepath.Join(nal.homePath, "system")
		auditLogger, auditErr := audit.NewLogger(audit.LoggerConfig{
			Dir:           auditDir,
			RetentionDays: 90,
			// CRIT-2: signal to NewLogger that audit logging is genuinely
			// wanted here. Without this, NewLogger would swallow
			// openCurrentFile errors and return a degraded logger + nil error
			// — the gateway would think audit_logger=ok at startup while every
			// subsequent write rejects in degraded mode. Setting
			// AuditLogRequested makes openCurrentFile failure surface as a
			// *LoggerConstructionError instead.
			//
			// This stays unconditionally true now that audit is on by default.
			// It is deliberately NOT wired to AuditLogExplicit: the flag's job
			// is to stop NewLogger from hiding a failure, and a failure must
			// never be hidden regardless of who asked. WHAT WE DO about the
			// surfaced failure is the part that depends on provenance, and
			// that decision is taken below, at this call site, which is the
			// only place that knows it.
			AuditLogRequested: true,
		})
		if auditErr != nil {
			// The audit logger could not be built. Two different populations
			// reach this line and they have earned different answers.
			//
			// (1) Somebody WROTE `"audit_log": true` — in config.json, or in
			//     a Config built directly in Go. Unchanged from B1.2(b):
			//     fail-closed boot abort. They asked for a compliance
			//     guarantee we cannot deliver, and running on without it
			//     would silently break the SEC-15 audit-everything contract.
			//     The gateway maps the returned typed error to a
			//     SandboxBootError + EX_CONFIG (78) exit code; see
			//     pkg/gateway/gateway.go around the agent.NewAgentLoop call,
			//     whose branch is still gated on cfg.Sandbox.AuditLog being
			//     true. This is the branch a zero-valued provenance field
			//     selects, deliberately — see AuditLogFromDefault's doc
			//     comment on the polarity being the safety property.
			//
			// (2) Audit is on because it is now the DEFAULT and this config
			//     never mentioned it. Degrade loudly and keep booting.
			//
			// Why (2) is not also an abort. Fail-closed is justified by
			// consent: the operator requested a guarantee, so not delivering
			// it silently is the regression. A default is not a request.
			// Aborting on it would convert a security improvement into a
			// denial of service for installs that never opted in and that
			// booted perfectly well yesterday with audit off — an upgrade
			// would turn "audit was off" into "the product does not start",
			// on a read-only filesystem, a full disk, or a partially-mounted
			// container volume. Strictly compared against the status quo this
			// branch is still an improvement: that population previously had
			// audit off AND no error; it now has audit off, a loud error, and
			// a degraded health endpoint.
			//
			// The failure is NOT silent. al.auditLogger stays nil, so
			// AgentLoop.AuditLogger() returns nil while the gateway's
			// SetAuditLoggerConfiguredFunc still reports configured=true from
			// cfg.Sandbox.AuditLog — the exact pair pkg/health/server.go
			// documents as "audit_logger=unavailable AND operator asked for
			// audit → degraded (broken)". /health reads degraded, and the
			// ERROR below names the directory and the underlying cause.
			//
			// In practice (2) should be close to unreachable: auditDir is
			// $OMNIPUS_HOME/system, the same tree that already holds
			// config.json, master.key, sessions and token_budget.json, so an
			// install that cannot write it is broken in ways that surface
			// elsewhere anyway. That is an argument for the blast radius of
			// this branch being small — not an argument for making a default
			// the thing that refuses to start.
			if !nal.cfg.Sandbox.AuditLogFromDefault {
				logger.ErrorCF("agent",
					"Audit logger construction failed; aborting boot because sandbox.audit_log=true was explicitly set",
					map[string]any{"error": auditErr.Error(), "dir": auditDir})
				return nil, &audit.LoggerConstructionError{Dir: auditDir, Err: auditErr}, true
			}
			logger.ErrorCF("agent",
				"Audit logger construction failed; continuing WITHOUT audit logging because audit_log is on by default, not by explicit configuration. "+
					"No security audit entries will be recorded for the lifetime of this process. "+
					"/health reports audit as degraded. Fix the directory, or set sandbox.audit_log=true to make this failure abort boot instead.",
				map[string]any{"error": auditErr.Error(), "dir": auditDir})
			auditLogger = nil
		}
		if auditLogger != nil {
			nal.al.auditLogger = auditLogger

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
			nal.al.wireMemoryAuditLoggerOn(nal.registry, auditLogger)

			// ADR-072 D6.1.1/R4 fix: install the process-wide skills write-audit
			// logger. tools.SetSkillsWriteAuditLogger's own doc comment names
			// this exact call site ("a later integration phase wires this at
			// gateway boot, alongside the other audit-logger wiring") — until
			// this call existed nowhere in production, tools.ResolvePath's
			// write hook (and pkg/sysagent/tools' project-shelf authoring path,
			// via tools.EmitSkillWriteAudit) was a permanent silent no-op:
			// write_file/edit_file/edit_skill/remove_skill writes into a
			// recognised skills location produced zero audit entries regardless
			// of sandbox.audit_log. Idempotent (last caller wins), mirrors
			// audit.SetProcessChainKey's process-wide-var pattern exactly.
			tools.SetSkillsWriteAuditLogger(auditLogger)
		}
	}
	return nil, nil, false
}

// initializeSecurity builds policy enforcement, sandboxing, prompt protection, and the exec proxy.
func (nal *newAgentLoop) initializeSecurity() {
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
	if len(nal.cfg.Tools.Exec.AllowedBinaries) > 0 {
		defaultPolicy = policy.PolicyDeny
	}
	secCfg := &policy.SecurityConfig{
		DefaultPolicy: defaultPolicy,
		Policy: policy.PolicySection{
			Exec: policy.ExecPolicy{
				AllowedBinaries: nal.cfg.Tools.Exec.AllowedBinaries,
				Approval:        nal.cfg.Tools.Exec.Approval,
			},
		},
	}
	policyEval := policy.NewEvaluator(secCfg)

	// Wrap the evaluator in a PolicyAuditor so every decision is audit-logged
	// (ADR-002 §W-3). When audit logging is disabled the bridge is nil; the
	// PolicyAuditor tolerates a nil logger and still enforces — enforcement
	// must NOT depend on audit logging being enabled.
	var auditBridgeImpl *auditBridge
	if nal.al.auditLogger != nil {
		auditBridgeImpl = newAuditBridge(nal.al.auditLogger)
	}
	var policyAuditorLogger policy.AuditLogger
	if auditBridgeImpl != nil {
		policyAuditorLogger = auditBridgeImpl
	}
	nal.al.policyAuditor = policy.NewPolicyAuditor(policyEval, policyAuditorLogger, "")

	// SEC-01/02/03: Select the best-available sandbox backend. This never
	// fails: on unsupported kernels SelectBackend returns a FallbackBackend.
	backend, backendName := sandbox.SelectBackend()
	nal.al.sandboxBackend = backend
	logger.InfoCF("agent", "Sandbox backend selected", map[string]any{"backend": backendName})

	// SEC-25: Initialize the prompt-injection guard. NewPromptGuardFromConfig
	// defaults to "medium" strictness when the field is empty. Construction
	// is cheap and cannot fail, so we always build it — runTurn checks the
	// untrusted-tool allowlist before invoking it, so trusted results are
	// never sanitized even when the guard is non-nil.
	nal.al.promptGuard = security.NewPromptGuardFromConfig(policy.PromptGuardConfig{
		Strictness: string(nal.cfg.Sandbox.PromptInjectionLevel),
	})
	logger.InfoCF("agent", "Prompt guard initialized",
		map[string]any{"strictness": string(nal.al.promptGuard.Strictness())})

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
	if nal.cfg.Sandbox.SSRF.Enabled {
		merged := make([]string, 0,
			len(nal.cfg.Sandbox.SSRF.AllowInternal)+len(nal.cfg.Sandbox.EgressAllowCIDRs))
		merged = append(merged, nal.cfg.Sandbox.SSRF.AllowInternal...)
		merged = append(merged, nal.cfg.Sandbox.EgressAllowCIDRs...)
		nal.al.ssrfChecker = security.NewSSRFChecker(merged)
		logger.InfoCF("agent", "SSRF protection enabled",
			map[string]any{
				"allow_internal_count":     len(nal.cfg.Sandbox.SSRF.AllowInternal),
				"egress_allow_cidrs_count": len(nal.cfg.Sandbox.EgressAllowCIDRs),
			})
	}

	// SEC-28: Start the loopback SSRF proxy for exec child processes when
	// enabled. On bind failure we log and fall back to degraded mode (child
	// processes run without HTTP_PROXY env vars — LIM-02) rather than
	// failing startup, because exec is a core tool and a proxy bind failure
	// on a shared port should not take the whole agent loop down.
	if nal.cfg.Tools.Exec.EnableProxy {
		// Reuse the singleton SSRF checker (which may be nil when SSRF is disabled).
		proxy := security.NewExecProxy(nal.al.ssrfChecker, nil)
		if err := proxy.Start(); err != nil {
			logger.ErrorCF("agent", "Failed to start exec SSRF proxy; child processes will run without proxy env vars",
				map[string]any{"error": err.Error()})
		} else {
			nal.al.execProxy = proxy
			logger.InfoCF("agent", "Exec SSRF proxy started",
				map[string]any{"addr": proxy.Addr()})
		}
	}
}

// initializeRuntime installs runtime limiters and finishes shared tool wiring.
func (nal *newAgentLoop) initializeRuntime() (*AgentLoop, error) {
	// Initialize cancel abuse detector (shared across all four cancel entry points).
	nal.al.cancelAbuse = newCancelAbuseDetector()

	// Initialize the pre-registration cancel latch table (cancel_prearm.go).
	nal.al.cancelPreArm = newCancelPreArm()

	// SEC-26: Initialize rate limiter registry. The registry always exists
	// so per-agent windows can be created even when no limit is configured.
	nal.al.rateLimiter = security.NewRateLimiterRegistry()
	logger.InfoCF("agent", "Rate limiter initialized",
		map[string]any{
			"max_agent_llm_calls_per_hour":    nal.cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour,
			"max_agent_tool_calls_per_minute": nal.cfg.Sandbox.RateLimits.MaxAgentToolCallsPerMinute,
		})

	// Session-scoped tool-approval grant store (consent boundary fix): shared
	// by the gateway's tool-approval REST path and the delegate tool's
	// async/await paths. Always non-nil.
	nal.al.approvalGrants = security.NewApprovalGrantStore()

	// Process-wide AsyncNotifier (async-notifier-spec.md): the reusable
	// "wake the conversation when background work finishes" primitive,
	// extracted from the asyncCallback closure below. Always non-nil.
	nal.al.asyncNotifier = newAsyncNotifier(nal.al)

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
	nal.al.memoryRateLimiter = tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{})
	nal.al.wireMemoryRateLimiterOn(nal.registry, nal.al.memoryRateLimiter)
	logger.InfoCF("agent", "Memory write rate limiter initialized",
		map[string]any{
			"per_agent_per_minute":  nal.al.memoryRateLimiter.PerAgentLimit(),
			"per_caller_per_minute": nal.al.memoryRateLimiter.PerCallerLimit(),
		})

	// Register shared tools to all agents (now that al is created)
	registerSharedTools(nal.al, nal.cfg, nal.msgBus, nal.registry, nal.provider)

	// Replace the exec tool in each agent's registry with a version that has
	// the policy auditor and sandbox backend wired in. Registering the same
	// tool name overwrites the previous entry (see ToolRegistry.Register).
	nal.al.wireExecToolDeps()

	// Fix A (FR-057): wire the environment provider into every agent's
	// ContextBuilder now that the sandbox backend is known. Also register each
	// ContextBuilder into the registry so config-change invalidation (FR-061)
	// can broadcast across all agents.
	nal.al.wireEnvProviders(nal.cfg, nal.registry)

	return nal.al, nil
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

	// Drain background (async=true) delegations for the same reason and with the
	// same bounded-drain shape as the two above. A background delegate call is
	// fire-and-forget for its CALLER by design, so its goroutine outlives the
	// Execute that started it and keeps writing lifecycle/session state through
	// the stores torn down below.
	al.waitDelegateAsyncDrain(30 * time.Second)

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

// waitDelegateAsyncDrain blocks until every agent's in-flight background
// delegation goroutine has finished, or the budget expires.
//
// Bounded for the same reason waitRecapDrain is: a wedged sub-turn (a mock
// provider that never returns, a real LLM hanging past its own timeout) must
// never freeze teardown. Exceeding the budget is logged and teardown proceeds —
// a delegation that did not finish writing is strictly better than a process
// that will not exit.
func (al *AgentLoop) waitDelegateAsyncDrain(budget time.Duration) {
	al.delegateToolsMu.Lock()
	pending := append([]*tools.DelegateTool(nil), al.delegateTools...)
	al.delegateToolsMu.Unlock()
	if len(pending) == 0 {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, dt := range pending {
			dt.WaitForAsyncTasks()
		}
	}()
	select {
	case <-done:
		// Every background delegation finished writing.
	case <-time.After(budget):
		logger.WarnCF("agent", "Close: background-delegation drain budget exceeded; proceeding with teardown",
			map[string]any{"budget": budget.String(), "tools": len(pending)})
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

// u9ToolExecSessionIDs computes the two identity fields ADR-057's W4 stamping
// contract requires on the wire for a session-scoped frame, for the two Go
// event payloads events.go (U23) gave a ProducingSessionID field —
// ToolExecStartPayload and ToolExecEndPayload, the only two of the 19
// SESSION_SCOPED_FRAME_TYPES classified as needing it at the Go-payload
// level today (class (a) per the W5 audit, FR-089/BDD-16: a child turn
// genuinely emits tool_call_start/tool_call_result, so the wire frame
// carries both ids). Factored into one function, called from both
// construction sites below, so this file has exactly one place that answers
// "what goes on the wire" rather than two independently-maintained copies of
// the same two-field contract.
//
//   - sessionID (FR-011/FR-012): the ROUTING identity — the id inherited
//     verbatim from the root of the delegation subtree — never this turn's
//     own transcriptSessionID, which for a delegated child differs from the
//     root's.
//   - producingSessionID (FR-013): the zero value when ts IS the routing
//     session (producing == routing — the common non-delegated case, and
//     every root turn), so the WS forwarder (pkg/gateway/websocket.go, U11)
//     can implement the "present iff it differs from session_id" rule with a
//     plain non-empty-and-unequal check before stamping the wire's optional
//     producing_session_id. Otherwise this turn's own real, store-backed
//     session id — see ToolExecStartPayload.ProducingSessionID's doc comment
//     (events.go) for the full rationale.
func u9ToolExecSessionIDs(ts *turnState) (sessionID string, producingSessionID session.SessionID) {
	sessionID = string(ts.routingSessionID)
	if ts.transcriptSessionID == sessionID {
		return sessionID, ""
	}
	return sessionID, session.SessionID(ts.transcriptSessionID)
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
	resp, err := pm.al.runAgentLoop(pm.ctx, agent, pm.opts)
	return resp, agent, err
}

// prepareInbound logs the inbound message, transcribes audio, and sends any deferred placeholder.
func (pm *agentLoopProcessMessage) prepareInbound() {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(pm.msg.Content, "Error:") || strings.Contains(pm.msg.Content, "error") {
		logContent = pm.msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(pm.msg.Content, 80)
	}
	logger.InfoCF(
		"agent",
		fmt.Sprintf("Processing message from %s:%s: %s", pm.msg.Channel, pm.msg.Sender.CanonicalID, logContent),
		map[string]any{
			"channel":     pm.msg.Channel,
			"chat_id":     pm.msg.ChatID,
			"sender_id":   pm.msg.Sender.CanonicalID,
			"session_key": pm.msg.SessionKey,
		},
	)

	var hadAudio bool
	pm.msg, hadAudio = pm.al.transcribeAudioInMessage(pm.ctx, pm.msg)

	// For audio messages the placeholder was deferred by the channel.
	// Now that transcription (and optional feedback) is done, send it.
	if hadAudio {
		if cm := pm.al.getChannelManager(); cm != nil {
			cm.SendPlaceholder(pm.ctx, pm.msg.Channel, pm.msg.ChatID)
		}
	}
}

// resolveWorkspace selects the active workspace from session, channel, or inbound metadata.
func (pm *agentLoopProcessMessage) resolveWorkspace() {
	// M4: bind the active workspace into this turn so a task an agent creates
	// (task_create / delegation) lands on the ACTIVE workspace's board rather
	// than the agent's default workspace. A web-chat / channel session that was
	// opened from a workspace carries the workspace ID on its meta (set via the
	// session-scope PUT or at session creation). Resolve it here so the tool
	// context (loop.go: WithWorkspaceID) carries it through to resolveWorkspaceID.
	// Falls back to the inbound metadata key when present (e.g. board-task runs),
	// and finally to "" — task_create then resolves the real default workspace.
	pm.workspaceID = ""
	if pm.transcriptStore != nil && pm.transcriptSessionID != "" {
		// FIX 1 (re-review of the re-review): distinguish a real meta-read
		// failure from "no workspace bound" — see
		// resolveWorkspaceIDForContinuation's doc comment (above,
		// ~line 3099) for the full rationale.
		if meta, mErr := pm.transcriptStore.GetMeta(pm.transcriptSessionID); mErr != nil {
			if !errors.Is(mErr, os.ErrNotExist) {
				logger.WarnCF("agent", "could not read session meta while resolving workspace; workspace unresolved",
					map[string]any{"session_id": pm.transcriptSessionID, "error": mErr.Error()})
			}
		} else if meta != nil {
			pm.workspaceID = meta.WorkspaceID
		}
	}
	if pm.workspaceID == "" {
		// The channel instance itself, before falling back to inbound metadata.
		//
		// resolveWorkspaceIDForContinuation has had this rung all along; this
		// path did not, and the asymmetry was the bug: a session created
		// BEFORE its channel was bound to a workspace keeps an empty
		// workspace_id forever (resolveOrCreateChannelSession returns early on
		// an index hit and never patches an existing session), and
		// resolveEffectiveWorkspaceID then silently substitutes the DEFAULT
		// workspace. Since ADR-037 makes delegation trust workspace-scoped,
		// that authorises delegation against the wrong workspace's trust
		// graph, and memory rooms, task placement and the working directory
		// degrade the same way.
		//
		// setChannelRouting now re-stamps existing sessions when a binding is
		// written, which repairs data. This closes it at resolution time as
		// well, so a session created by any path that never went through that
		// handler still resolves correctly — and so the two ladders stop
		// disagreeing on this axis, which is the same defect shape as the
		// default-agent divergence.
		if instanceID := inboundInstanceID(pm.msg); instanceID != "" {
			if cfg := pm.al.GetConfig(); cfg != nil {
				if inst, ok := cfg.Channels[instanceID]; ok && inst.WorkspaceID != "" {
					pm.workspaceID = inst.WorkspaceID
				}
			}
		}
	}
	if pm.workspaceID == "" {
		pm.workspaceID = inboundMetadata(pm.msg, "workspace_id")
	}
}

// prepareTurn builds the turn options and releases browser ownership for an operator prompt.
func (pm *agentLoopProcessMessage) prepareTurn() {
	pm.opts = processOptions{
		SessionKey:        pm.sessionKey,
		Channel:           pm.msg.Channel,
		ChatID:            pm.msg.ChatID,
		SenderID:          pm.msg.Sender.CanonicalID,
		SenderDisplayName: pm.msg.Sender.DisplayName,
		// FR-017: thread the authenticated gateway principal into the turn for
		// audit attribution. Only the gateway webchat WS path sets
		// msg.GatewayUserID (= wc.userID, the WS-authenticated identity, e.g.
		// "cli" or an admin username). Channel/task/scheduled inbound messages
		// never set it, so their turns leave audit.Entry.User empty structurally.
		// We deliberately do NOT read msg.Sender.Username here: channels populate
		// it with the platform handle (e.g. "@alice"), which is not a gateway
		// principal and must never be stamped as audit User.
		UserID:              gatewayPrincipal(pm.msg),
		UserInitiated:       userInitiated(pm.msg),
		UserMessage:         pm.msg.Content,
		Media:               pm.msg.Media,
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: pm.transcriptSessionID,
		TranscriptStore:     pm.transcriptStore,
		WorkspaceID:         pm.workspaceID,
		// Carry inbound metadata so runTurn can detect a per-thread model
		// switch (FR-011). The map is copied by reference — the turn flow
		// only reads from it.
		Metadata: pm.msg.Metadata,
	}

	// ADR-085 BROWSER-FR-029: release a held browser wheel BEFORE this turn
	// begins, if and only if msg.OperatorPrompt is true (set ONLY at the
	// three operator-originated publish sites: websocket.go, sse.go,
	// channels/base.go::HandleMessage — never here, never by the bus, never
	// by the async notifier or a goal-loop follow-up). A nil hook (no
	// gateway wired — headless/test builds) is a silent no-op. See
	// browser_deferral.go for the hook's registration and the fail-closed
	// contract on OperatorPrompt itself.
	invokeBrowserWheelReleaseHookIfOperatorPrompt(pm.ctx, pm.msg, pm.transcriptSessionID)
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

	if opts.SendResponse && result.finalContent != "" {
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
	// ts.Finish/al.clearActiveTurn), not here — see that registration
	// site's comment for why the ORDER relative to Finish is load-bearing
	// (FIX 1/5c live-verification finding).

	// Inject turnState and AgentLoop into context so tools (e.g. delegate) can retrieve them.
	turnCtx = withTurnState(turnCtx, ts)
	turnCtx = WithAgentLoop(turnCtx, al)
	// SEC-15: Inject agent ID so audit entries carry the agent identity.
	turnCtx = tools.WithAgentID(turnCtx, ts.agent.ID)
	// Inject session key so switch_agent can address the session.
	if ts.sessionKey == "" {
		logger.WarnCF("agent", "runTurn: sessionKey is empty — switch_agent tool will not work",
			map[string]any{"agent_id": ts.agentID, "chat_id": ts.chatID})
	}
	turnCtx = tools.WithSessionKey(turnCtx, ts.sessionKey)
	// Inject the actual session ID (directory name) for the switch_agent tool.
	// The session key is a routing key; the transcript session ID is the
	// real session directory (e.g., "session_01KP30THP63YFESKGECYYHYQWY").
	turnCtx = tools.WithTranscriptSessionID(turnCtx, ts.opts.TranscriptSessionID)
	// ADR-085 BROWSER-FR-021: stamp the ROOT chat session id (ADR-057
	// routingSessionID, inherited verbatim through a whole delegation
	// subtree) so pkg/tools/browser/tools.go::controlledResult can evaluate
	// its FR-020 second coverage check — the tab set the live panel would
	// hold the lock on for the chat this turn (or its delegated ancestor)
	// belongs to — even for a delegated child driving its OWN tab set
	// (FR-023). A turn with no root chat (cron/heartbeat/task) stamps "",
	// which controlledResult's own doc comment documents as "skip that
	// check entirely" (fails open, never closed).
	turnCtx = withBrowserRootChatSessionID(turnCtx, ts)
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
	// (workspaces/<id>/work/, materialized via workspace.EnsureWorkDir — the
	// sanctioned SafeWorkDir+MkdirAll+git-evidence replacement) instead of its
	// private per-agent one. Unconditional (no feature flag) and PRIMARILY driven by
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
	// The CALL itself is below, AFTER registerActiveTurn and the turn-end
	// defers. A prior version returned here, before the turn existed as a
	// turn: no EventKindError, no transcript, no done frame. The classifier
	// already knew the cause (TranslateTurnError → agent_not_configured) and
	// still had nowhere to put it. The user saw a turn that never started.
	// Moving the call below makes a refusal a real failed turn the SPA can
	// render. The gate function is unchanged — only when it runs changed.

	// FR-7.5 / NFR-1: install a per-turn citation tracker so recall_memory can
	// report surfaced memories and the loop can emit op:cited counter events
	// when the LLM references them by ID/title. Nil for the main gateway agent
	// (no memory store); WithCitationTracker is a no-op in that case.
	citationTracker := newCitationTracker(ts.agent.ContextBuilder.Memory())
	turnCtx = tools.WithCitationTracker(turnCtx, citationTracker)

	al.registerActiveTurn(ts)
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
	defer func() { ts.Finish(ts.hardAbortRequested()) }()
	defer ts.finalizeStreamer(ctx)
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
	defer al.preserveTruncatedAccumulator(ts)
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
				// ADR-057 FR-012 (W4/U9): the "done" frame is one of the 19
				// SESSION_SCOPED_FRAME_TYPES (src/store/chat.ts), so its wire
				// session_id MUST come from the ROUTING identity — the id
				// inherited verbatim from the root of the delegation subtree
				// — not this turn's own store-backed transcriptSessionID,
				// which for a delegated child differs from the root's. No
				// ProducingSessionID sibling exists on this payload today
				// (events.go/U23 added that field only to
				// ToolExecStart/EndPayload) even though the W5 audit already
				// classifies "done" as carrying both ids on the wire schema
				// (contracts/asyncapi.yaml) — closing that gap is events.go's
				// (U23) and the WS forwarder's (U11) cross-unit follow-up,
				// not something addable from this file.
				SessionID: string(ts.routingSessionID),
				IsRoot:    ts.parentTurnID == "",
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
		if turnStatus == TurnEndStatusError {
			ts.markTurnFailed()
		}
	}()

	al.emitEvent(
		EventKindTurnStart,
		ts.eventMeta("runTurn", "turn.start"),
		TurnStartPayload{
			Channel:     ts.channel,
			ChatID:      ts.chatID,
			UserMessage: ts.userMessage,
			MediaCount:  len(ts.media),
			IsRoot:      ts.parentTurnID == "",
		},
	)

	// Shared workspace-membership gate — see the ADR-046 comment above for
	// WHY this refuses. It runs HERE so a refusal is a registered turn:
	// turn.start has already gone out, the LIFO defers (markTurnFailed →
	// turn.end → finalizeStreamer) will fire on return, and the typed
	// EventKindError below is what the SPA actually renders. Returning
	// before registerActiveTurn left the user with silence and a classifier
	// result nobody ever saw.
	wsDir, wsErr := resolveTurnWorkDirOrRefuse(turnCtx, ts.agent.ID, ts.agent.Home, ts.opts.WorkspaceID)
	if wsErr != nil {
		turnStatus = TurnEndStatusError
		llm := TranslateTurnError(wsErr)
		al.emitEvent(
			EventKindError,
			ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{
				Stage:     "workspace",
				ChatID:    ts.chatID,
				SessionID: string(ts.routingSessionID),
				Code:      string(llm.Code),
				Message:   llm.Message,
			},
		)
		ts.appendClassifiedError(EventKindError.String(), "workspace", llm)
		return turnResult{}, wsErr
	}
	turnCtx = tools.WithTurnWorkspaceDir(turnCtx, wsDir)

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
				ts.opts.TranscriptSessionID,
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
				// The classifier has never recognized this sentence — the
				// comment that said it did was stale. Stamp the dedicated
				// code so live and replay both say the switch failed, not
				// "we can't tell why". Raw stays in the warn log.
				switchFailMsg := fmt.Sprintf(
					"Could not switch to model %q: %s. This reply used %q instead.",
					requested, switchErr.Error(), ts.agent.Model,
				)
				switchLLM := LLMError{
					Code:      CodeModelUnavailable,
					Message:   defaultUserMessage(CodeModelUnavailable),
					Retryable: isRetryable(CodeModelUnavailable),
					Detail:    buildDetail(nil, switchFailMsg),
				}
				al.emitEvent(
					EventKindError,
					ts.eventMeta("runTurn", "turn.error"),
					ErrorPayload{
						Stage:     "model_switch",
						Code:      string(switchLLM.Code),
						Message:   switchLLM.Message,
						ChatID:    ts.chatID,
						SessionID: string(ts.routingSessionID),
					},
				)
				ts.appendClassifiedError(EventKindError.String(), "model_switch", switchLLM)
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
	if !ts.opts.NoHistory {
		// FR-069 / FR-088: recover orphaned tool calls left by a SIGKILL or OOM
		// kill while the gateway was paused awaiting approval. The function is
		// idempotent — it no-ops on clean sessions and on sessions where the
		// synthetic turn_canceled_restart entry already exists. The on-disk
		// transcript is preserved; only the LLM-context slice (history) is
		// cleaned so the LLM does not see dangling unanswered tool_call entries.
		history = RecoverOrphanedToolCalls(ts.agent.Sessions, ts.sessionKey, al.auditLogger)
	}

	// Site-1: initial assembly (CRITICAL 2 — error handled inside assembleMessages).
	messages := al.assembleMessages(
		turnCtx,
		ts,
		history,
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
	turnProvider, turnModel := ts.agent.primaryModelPair()
	messages = resolveMediaRefsWithOffload(
		messages, turnMediaStore, maxMediaSize, turnProvider, turnModel,
		&offloadSink{workDir: wsDir}, turnCatalog, turnRefcounter,
		ts.opts.WorkspaceID,
	)

	// ADR-067 FR-016/FR-038 pre-turn gate (FIRST of the three). An agent whose
	// PRIMARY provider id is unknown cannot reach any upstream at all, so it
	// is refused here — before the model gate can ask which model, and before
	// the window gate can ask how big that model's context is. Like the two
	// gates below it the refusal is a REAL failed turn: turn.start already
	// went out, the LIFO defers fire on return, and the typed EventKindError
	// is what the SPA renders. The WARN and the error name the operator's own
	// spelling of the id and nothing else — never a canonical alternative
	// (SC-010).
	if needsProvider, unknownProviderID := ts.agent.needsProviderSnapshot(); needsProvider {
		turnStatus = TurnEndStatusError
		providerErr := fmt.Errorf("%w: agent_id=%s provider=%s",
			ErrAgentNeedsProvider, ts.agent.ID, unknownProviderID)
		llm := TranslateTurnError(providerErr)
		logger.WarnCF("agent", "Turn refused: the agent's provider is unknown",
			map[string]any{"agent_id": ts.agent.ID, "provider": unknownProviderID})
		al.emitEvent(
			EventKindError,
			ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{
				Stage:     "provider",
				ChatID:    ts.chatID,
				SessionID: string(ts.routingSessionID),
				Code:      string(llm.Code),
				Message:   llm.Message,
			},
		)
		ts.appendClassifiedError(EventKindError.String(), "provider", llm)
		return turnResult{}, providerErr
	}

	// ADR-068 FR-014/FR-015 pre-turn gate (SECOND of the three). An agent
	// with no model to call is refused here — after the provider gate above
	// (a provider must exist before a model can, SC-013) and before the
	// window gate below (a model must exist before its window can be sized).
	// Same shape as its two siblings: a REAL failed turn with turn.start
	// already out, the LIFO defers firing on return, and a typed
	// EventKindError the SPA renders. The agent's stored model is NOT
	// touched — a refusal never re-points an agent at some other model
	// (US-3.AC4).
	if ts.agent.needsModelSnapshot() {
		turnStatus = TurnEndStatusError
		modelErr := fmt.Errorf("%w: agent_id=%s", ErrAgentModelUnassigned, ts.agent.ID)
		llm := TranslateTurnError(modelErr)
		logger.WarnCF("agent", "Turn refused: the agent has no model assigned",
			map[string]any{"agent_id": ts.agent.ID})
		al.emitEvent(
			EventKindError,
			ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{
				Stage:     "model",
				ChatID:    ts.chatID,
				SessionID: string(ts.routingSessionID),
				Code:      string(llm.Code),
				Message:   llm.Message,
			},
		)
		ts.appendClassifiedError(EventKindError.String(), "model", llm)
		return turnResult{}, modelErr
	}

	// ADR-066 D3 pre-turn gate (FR-008): a local endpoint that reported no
	// context window is refused, never run on a guessed number. Order:
	// needs_provider (ADR-067, above) → model_unassigned (ADR-068) →
	// context_window_unknown (here, third). It sits after the model switch
	// so a switch onto an unsized local model is refused too, and before
	// the first budget check so nothing ever computes a budget from W = 0.
	// Like the workspace gate above, the refusal is a REAL failed turn:
	// turn.start went out, the LIFO defers fire on return, and the typed
	// EventKindError is what the SPA renders.
	if _, windowExempt, windowUnknown := ts.agent.windowSnapshot(); windowUnknown && !windowExempt {
		turnStatus = TurnEndStatusError
		windowErr := fmt.Errorf("%w: agent_id=%s model=%s", ErrContextWindowUnknown, ts.agent.ID, ts.agent.Model)
		llm := TranslateTurnError(windowErr)
		al.emitEvent(
			EventKindError,
			ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{
				Stage:     "context_window",
				ChatID:    ts.chatID,
				SessionID: string(ts.routingSessionID),
				Code:      string(llm.Code),
				Message:   llm.Message,
			},
		)
		ts.appendClassifiedError(EventKindError.String(), "context_window", llm)
		return turnResult{}, windowErr
	}

	// FR-005: an exempt provider (subprocess CLI) manages its own context —
	// the pre-turn trim and every budget check are skipped.
	if !ts.opts.NoHistory && !ts.agent.budgetChecksExempt() {
		// FR-028: the pre-turn check reads the one budget B — the same value
		// windowTrim fits the suffix against — never the raw window — AND the
		// same tool surface windowTrim measures (sentToolSurfaceTokens, what
		// the turn actually sends). Charging the whole registry here, as this
		// site used to, fired the check on a conversation that fit and had
		// windowTrim evict one turn per turn.
		toolDefsTokens := al.sentToolSurfaceTokens(ts.agent, ts.opts.TranscriptSessionID, ts.sessionKey)
		// C1: `messages` never carries the ephemeral system notes runTurn
		// injects into callMessages before the request that is actually
		// sent (scratchpad, workspace instructions — AGENT.md, up to
		// 262,144 bytes with no budget-aware cap — and the web-rendering
		// note); the compressed manifest note is already folded into
		// toolDefsTokens above. Without ephemeralSystemNoteTokens here, a
		// large AGENT.md alone can push the assembled request tens of
		// thousands of tokens past what this check saw, producing a
		// provider context_too_long on a window this check believed it was
		// protecting.
		nonMessageTokens := toolDefsTokens + al.ephemeralSystemNoteTokens(ts)
		if isOverContextBudgetTokens(agentContextBudget(ts.agent), messages, nonMessageTokens) {
			logger.WarnCF("agent", "Proactive window trim: context budget exceeded before LLM call",
				map[string]any{"session_key": ts.sessionKey})
			if compression, ok := al.windowTrim(ts.agent, ts.opts.TranscriptSessionID, ts.sessionKey); ok {
				al.emitEvent(
					EventKindContextCompress,
					ts.eventMeta("runTurn", "turn.context.compress"),
					ContextCompressPayload{
						Reason:            ContextCompressReasonProactive,
						DroppedMessages:   compression.DroppedMessages,
						RemainingMessages: compression.RemainingMessages,
					},
				)
			}
			// Site-2: post-proactive-trim assembly.
			newHistory := ts.agent.Sessions.GetHistory(ts.sessionKey)
			messages = al.assembleMessages(
				turnCtx,
				ts,
				newHistory,
				ts.userMessage,
				ts.media,
				activeSkillNames(ts.agent, ts.opts),
			)
			trimProvider, trimModel := ts.agent.primaryModelPair()
			messages = resolveMediaRefsWithOffload(
				messages, turnMediaStore, maxMediaSize, trimProvider, trimModel,
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
	// midTurnGuardErr carries the ADR-066 D6 thrash-guard error out of the
	// per-result mid-turn window checks below (midturn_budget.go). Declared
	// before the turnLoop label so the `goto turnLoop` below never jumps
	// over its declaration.
	var midTurnGuardErr error
	emptyResponseRetries := 0
	const maxEmptyResponseRetries = 1
	// orphanToolMarkupRepairs counts how many times this turn has re-prompted
	// a model that emitted its tool call as unparseable text (see the strip
	// choke point below). Bounded so a model that cannot comply ends the turn
	// with a visible error instead of looping on the user's budget.
	orphanToolMarkupRepairs := 0
	// continuationChain (ADR-087 D6.7) holds the exact {assistant, user} pair
	// a previous round appended to `messages` as the D6 continuation chain,
	// so the next round REPLACES it — by identity, via stripContinuationChain
	// — instead of accumulating duplicate copies of the answer-so-far. nil
	// means no chain is currently live in `messages`. Declared before
	// turnLoop so it survives both `continue turnLoop` and `goto turnLoop`.
	var continuationChain []providers.Message

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

		// toolCallTruncationRepairUsed (ADR-087 D3.4) bounds a truncated
		// tool call to exactly one repair per turnLoop round — reset every
		// round (this `:=` runs again on every loop-body execution,
		// including via `continue turnLoop`), shared across this round's
		// three possible provider-call sites (D9).
		toolCallTruncationRepairUsed := false

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
						SessionID:         string(ts.routingSessionID),
					},
					map[string]any{"retry_after_seconds": result.RetryAfterSeconds},
				)
				turnStatus = TurnEndStatusError
				// ADR-087 D6.8: a rate-limit denial makes no provider call —
				// a D6 continuation left unresolved by a prior round is
				// preserved by runTurn's deferred preserveTruncatedAccumulator
				// choke point, which covers this return like every other.
				return turnResult{}, fmt.Errorf("rate limit: %s (retry after %.0fs)",
					result.PolicyRule, result.RetryAfterSeconds)
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
				pendingMessages, turnMediaStore, maxMediaSize,
				candidateProvider(activeCandidates), activeModel,
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
		// (FR-041) handles human-in-the-loop confirmation; see the ADR-058
		// quarantine gate and recordToolDenial (tool_denial.go).
		allAgentTools := ts.agent.Tools.GetAll()
		policyFilteredTools, filterTimePolicyMap := tools.FilterToolsByPolicy(allAgentTools, ts.agent.AgentType, ts.agent.LoadToolPolicy())

		// The unified `ToolSearch` infra tool is registration-gated, NOT policy-gated:
		// when compressed mode is on it must be callable by EVERY agent — including
		// deny-by-default agents (Ava/Mia/Ray) — or the model is shown `ToolSearch` in
		// its defs but its EXECUTION is denied, leaving every lazy tool permanently
		// unreachable. Force it into both the sent defs (policyFilteredTools) and
		// the execution-time policy snapshot (filterTimePolicyMap, consulted by
		// resolveToolPolicyAtExec) as "allow". This mirrors the defs force-include
		// in buildCompressedToolDefs at the authorization layer. (Found by live
		// validation: a deny-by-default agent called ToolSearch and the exec gate
		// denied it — reachability broke.)
		policyFilteredTools = ensureInfraToolsExecutable(
			ts.agent.Tools, policyFilteredTools, filterTimePolicyMap)

		// ADR-088 D3/D4 (spec FR-007/009/010/011): evaluated ONCE per request,
		// right after the policy filter settles, so both the tool-surface
		// narrowing below and the rubric-note injection further down (and the
		// FR-010 question-budget bump after this iteration's tool-execution
		// loop) read the exact same verdict. See evaluateGoalForcing's own doc
		// comment for the full predicate.
		goalForce := al.evaluateGoalForcing(ts, iteration, policyFilteredTools)

		// FR-066: dedup invariant — tools[] must be name-unique after filter+assembly.
		// If a duplicate is detected, emit HIGH audit and return an error turn result
		// so the loop does not feed a malformed tool list to the LLM.
		if dedupErr := al.checkToolDedupInvariant(ts, policyFilteredTools); dedupErr != nil {
			// issue #618 (fourth ungoverned member): this used to be built by
			// hand with fmt.Sprintf's %q verb — Go-string quoting, not JSON
			// quoting, unbounded, no contract schema, no allow-list entry, no
			// SPA detector. tools.ToolAssemblyDuplicatePayload (the generated
			// ToolAssemblyDuplicate schema) fixes all three: encoding/json
			// escaping (valid on invalid UTF-8 and C0/C1 control bytes alike),
			// a 1900-rune encoded budget via marshalWithinBudget, and a real
			// contract schema wired into the structured-failure allow-list.
			var denyMsg string
			if encoded, encErr := tools.ToolAssemblyDuplicatePayload(dedupErr.Error()); encErr == nil {
				denyMsg = string(encoded)
			} else {
				// Fully static fallback — no interpolated content — so a
				// marshal failure can never itself reintroduce the escaping
				// bug this fix exists to close. Reported rather than
				// swallowed: this branch discards dedupErr's real text.
				tools.ReportStructuredFailureMarshalError("pkg/agent.checkToolDedupInvariant", "", tools.ToolAssemblyDuplicateCode, encErr)
				denyMsg = `{"error":"tool_assembly_duplicate","message":"An internal error occurred while building the duplicate tool-assembly payload."}`
			}
			syntheticDenyMsg := providers.Message{Role: "system", Content: denyMsg}
			if !ts.opts.NoHistory {
				ts.agent.Sessions.AddFullMessage(ts.sessionKey, syntheticDenyMsg)
			}
			// ADR-058 §3.2/§10.A3: this branch used to also invoke FR-084's
			// per-turn synthetic-deny counter-and-abort helper before
			// returning below. FR-084 is deleted in full — this was the one
			// call site whose abort branch could NEVER fire
			// (every other call site's shouldAbort path was reachable; this
			// one always returns unconditionally on the very next line, so
			// its counter could reach at most 1 and a floor of 8 was
			// structurally unreachable, issue #595). Nothing behavioural is
			// lost: the turn already terminates unconditionally below.
			//
			// Fail the LLM call for this iteration by returning an error turn result.
			turnStatus = TurnEndStatusError
			return turnResult{status: TurnEndStatusError, finalContent: denyMsg}, dedupErr
		}

		var providerToolDefs []providers.ToolDefinition
		switch {
		case goalForce.layer1:
			// ADR-088 D3 Layer 1 (spec test 8's compressed-mode-suspension
			// row): "exactly the pair" is exact — bypass
			// buildCompressedToolDefs/stripInfraToolDefs entirely for this one
			// narrowed request, including the compressed-mode ToolSearch
			// force-through those helpers would otherwise apply.
			providerToolDefs = tools.ToolsToProviderDefs(goalForce.narrowed)
		case cfg.Tools.Manifest.Compressed:
			providerToolDefs = al.buildCompressedToolDefs(ts, policyFilteredTools)
		default:
			// Non-compressed defs path: strip manifest infra tools (ToolSearch)
			// before surfacing defs to the model. ToolSearch resolves through the
			// same global×agent merge as every other static builtin tool and is
			// seeded "allow" as real, explicit data for every agent
			// (pkg/coreagent/core.go), so it is typically present in
			// policyFilteredTools even when compression is off; but ToolSearch
			// exists only to drive the compressed manifest mechanism and has no
			// function when compression is off, so the model never sees it here
			// regardless of what the agent's tool-policy map resolves for it (see
			// stripInfraToolDefs for the mostly-deny vs. mostly-allow behavior
			// note) (#438).
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
			if note := al.buildScratchpadNote(ts.agent.ID, ts.opts.TranscriptSessionID); note != "" && len(callMessages) > 0 {
				// Insert after callMessages[0] (the system prompt) so it immediately
				// follows the agent's identity, before the conversation history.
				injected := make([]providers.Message, 0, len(callMessages)+1)
				injected = append(injected, callMessages[0])
				injected = append(injected, providers.Message{Role: "system", Content: note})
				injected = append(injected, callMessages[1:]...)
				callMessages = injected
			}
			// Inject per-turn workspace instructions (AGENT.md) as an ephemeral
			// system message immediately after the system prompt. Empty/absent
			// instructions are a no-op — zero behavioral change.
			//
			// Ordering note (finding 10c, context-audit 2026-08 — ADR-088 D9
			// retires the ADR-078 D2 goal-pending note that used to sit between
			// this call and injectManifestNote below; buildGoalPendingNote/
			// injectGoalPendingNote, pkg/agent/goal_pending_note.go, are deleted
			// in full — instant activation leaves no pending state for a note to
			// describe): the remaining injectors (this one, injectWebRenderingNote,
			// injectManifestNote) insert at index 1 of the message array, so call
			// order alone determines final position — the LAST call ends up
			// CLOSEST to the system message. With every note present this turn,
			// final order is: [0] system prompt · [1] manifest note · [2]
			// web-rendering note · [3] workspace instructions · [4] scratchpad
			// (spliced above, before this call) · [5+] history. See
			// injectWorkspaceInstructions' own doc comment
			// (workspace_instructions.go) for the authoritative, single-sourced
			// version of this contract.
			callMessages = injectWorkspaceInstructions(callMessages, buildWorkspaceInstructionsNote(ts.opts.WorkspaceID))
			// Web-only: encourage Mermaid diagrams when the turn comes from the web
			// chat (the sole surface that renders them). Per-turn + surface-gated on
			// ts.channel — deliberately NOT in the cached system prompt, since one
			// agent serves multiple channels (see web_rendering_note.go).
			callMessages = injectWebRenderingNote(callMessages, buildWebRenderingNote(ts.channel))
			// ADR-088 D4 (spec FR-011, D3 amendment 2026-09-07): the goal
			// rubric + first-move instruction + define-goal skill quality
			// bar, injected exactly when the D3 base predicate holds
			// (goalForce.rubric — active goal AND an empty compiled record,
			// this turn's first LLM request) on EITHER origin — webchat gets
			// it alongside the narrowed two-tool surface below; a channel
			// origin gets the SAME note (with its conversational-ask
			// addendum) narrowed to {set_goal} alone (AskUserQuestion stays
			// permanently web-only). Neither origin forces a tool choice —
			// the note ASSISTS; the immediate post-turn correction
			// (checkGoalLoopAfterTurn, goal_loop.go) carries the guarantee.
			// buildGoalRubricInjectionNote returns "" when the predicate
			// does not hold, making this call a no-op on every non-goal turn.
			callMessages = injectGoalRubricNote(callMessages,
				buildGoalRubricInjectionNote(goalForce.rubric, goalForce.isWebchat))
			// Re-inject the compressed manifest of unloaded lazy tools as an ephemeral
			// system message. Like the scratchpad, it is rebuilt every turn (never
			// persisted) so it is never stale and not double-counted in the cached
			// system prompt. Injected only when Compressed is active and there are
			// unloaded lazy tools to list.
			//
			// Not on an ADR-088 D3 narrowed request (goalForce.layer1): that
			// request offers only {set_goal[, AskUserQuestion]}, and the block's
			// header tells the model to "call `ToolSearch`" to load a listed
			// tool — advertising a tool the request does not offer. UAT B-10
			// run 1 made exactly that un-offered ToolSearch call on the narrowed
			// request. The dispatch loop also refuses any call to a tool the
			// request did not offer (toolNotOfferedRefusal, tool_offer_gate.go).
			if cfg.Tools.Manifest.Compressed && !goalForce.layer1 {
				callMessages = injectManifestNote(callMessages, al.buildToolManifestNote(ts, policyFilteredTools))
			}
		}
		if gracefulTerminal {
			callMessages = append(append([]providers.Message(nil), repairedHistory...), ts.interruptHintMessage())
			providerToolDefs = nil
			ts.markGracefulTerminalUsed()
		}

		// ADR-088 D3 Layer 1 narrowing is active for THIS request exactly
		// when goalForce.layer1 holds and gracefulTerminal hasn't nilled the
		// tool surface. review-round-1 finding #6 (kept under the D3
		// amendment, 2026-09-07): native_search must never ride alongside
		// the narrowed pair — it would silently add a THIRD callable "tool"
		// (the provider's own built-in search) outside {set_goal[,
		// AskUserQuestion]}, undermining the narrowed surface's "exactly the
		// pair" promise even though nothing forces the model to touch it
		// anymore (provider tool-choice forcing is deleted — determinism now
		// comes from the immediate post-turn correction, goal_loop.go, not
		// the request shape). Suppress native search for this one request
		// while narrowing is active; the client-side search_web tool is not
		// offered here either (it's excluded from goalForce.narrowed, same
		// as every other non-goal tool). No tool-choice option is ever set —
		// see evaluateGoalForcing's doc comment for why.
		narrowingActive := goalForce.layer1 && !gracefulTerminal
		llmOpts := map[string]any{
			"max_tokens":       ts.agent.MaxTokens,
			"temperature":      ts.agent.Temperature,
			"prompt_cache_key": ts.agent.ID,
		}
		if useNativeSearch && !narrowingActive {
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

		// The exact tool set this request offers, captured AFTER every step that
		// shapes providerToolDefs (goal-door narrowing, compressed manifest,
		// native-search strip, graceful-terminal clearing, BeforeLLM hook). The
		// tool loop below refuses any call to a tool outside it — see
		// toolNotOfferedRefusal (tool_offer_gate.go).
		offeredTools := newOfferedToolSet(providerToolDefs)

		// G1 fix: a cheap, non-blocking tool-call-argument progress callback,
		// so a `delegate action=status` poll on a running child can tell
		// "still generating a large tool-call argument" apart from "hung".
		// The incident this closes: an orchestrator polled a delegated
		// worker 75 times over 46s, saw no activity because the only output
		// was landing inside a streaming tool-call argument, and killed it
		// mid-write.
		//
		// This is passed to ChatStream as an explicit ARGUMENT, not smuggled
		// through the llmOpts map (ADR-059 W1/D1). The map route was fragile
		// in a way that had already produced one near-miss: a BeforeLLM hook
		// returning HookActionModify replaces llmOpts wholesale — possibly
		// with a nil map — so the key had to be re-injected after the hook
		// block, in a specific position, with a comment explaining why. A
		// parameter cannot be dropped by a hook.
		//
		// It is NOT, however, compile-enforced on implementers, and an earlier
		// version of this comment claimed it was. StreamingProvider is
		// consulted by runtime type assertion a few hundred lines below
		// (`activeProvider.(providers.StreamingProvider)`), so a provider that
		// keeps the old signature simply stops satisfying the interface: the
		// build succeeds and the turn silently drops to the non-streaming
		// path. That is exactly how ClaudeProvider shipped with no ChatStream
		// at all. The real enforcement is pkg/providers/compliance.go's
		// `var _` assertions plus streaming_forwarding_test.go, and any new
		// implementer has to be added to them by hand.
		//
		// The callback only does an atomic store per delta (see
		// turnState.recordToolCallProgress) — cheap and non-blocking, safe
		// to call synchronously from the provider's SSE read loop. ts is
		// this turn's own turnState, captured by the closure: concurrent
		// turns each get their own callback writing into their own state,
		// never into another turn's. That per-turn binding is the reason the
		// handler travels with the call rather than being set on the
		// provider, which is shared across every concurrent turn.
		onToolCallProgress := protocoltypes.OnToolCallProgress(func(p protocoltypes.ToolCallProgress) {
			ts.recordToolCallProgress(p)
		})

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
			// Clear tool-argument progress when the round ends, on EVERY exit
			// path (success, error, retry, recovery). Placed here rather than
			// at the four call sites so no future path can forget it.
			//
			// Without this the signal keeps asserting "generating" for the rest
			// of the turn — including while the tool it just finished streaming
			// is actually EXECUTING. A worker blocked twenty minutes inside a
			// bash command would still report as generating, and an
			// orchestrator taught by this very feature to read that as "leave
			// it alone" would leave a genuinely hung child alone. That is the
			// original defect inverted: it killed healthy workers; this would
			// suppress the kill a hung one needs.
			defer ts.clearToolCallProgress()

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
					// Residual native tool-call markup must never reach the
					// live view. This is not only a rendering concern: the
					// gateway streamer PERSISTS what it accumulated from these
					// Update calls (wsStreamer.Finalize prefers its own buffer
					// over the turn's final content), so anything forwarded
					// here also lands in transcript.jsonl. Filtering at this
					// seam is what keeps the live bubble and the persisted
					// entry identical — and both clean. See
					// providers.StreamTextFilter.
					var streamFilter providers.StreamTextFilter
					resp, streamErr := sp.ChatStream(providerCtx, messagesForCall, toolDefsForCall, llmModel, llmOpts, func(accumulated string) {
						// B4: if the turn has been abandoned (stuck-goroutine detach),
						// suppress further frame emits so a zombie goroutine cannot
						// push frames to disconnected clients.
						if ts.abandoned.Load() {
							abandonedWritesSuppressed.Add(1)
							return
						}
						visible := streamFilter.Visible(accumulated)
						// Send only the new delta (visible minus what we already sent).
						//
						// Defensive: this slice panics with index-out-of-range if a
						// provider ever emits an accumulated string SHORTER than its
						// predecessor. The contract is monotonic growth, but a provider
						// bug, a block reorder, or an SDK revision changing accumulation
						// semantics would otherwise take down the whole turn. Treat a
						// non-growing value as "nothing new" and skip it. The filter
						// upholds the same non-shrinking contract on its own output.
						if len(visible) < len(lastChunk) {
							logger.DebugCF("agent", "Streaming callback emitted a shorter accumulated string; ignoring", map[string]any{
								"previous_len": len(lastChunk),
								"new_len":      len(visible),
							})
							return
						}
						delta := visible[len(lastChunk):]
						lastChunk = visible
						if delta != "" {
							if err := streamer.Update(providerCtx, delta); err != nil {
								logger.DebugCF("agent", "Streaming update error (client may have disconnected)", map[string]any{"error": err.Error()})
							}
						}
					}, onToolCallProgress)
					// Reconcile against the provider's final text. Two things
					// need this: the few bytes the filter holds back mid-stream
					// in case they start a marker split across SSE chunks, and
					// a provider that returned content without ever invoking
					// the callback. Without it those bytes would be dropped
					// silently — the streamer's buffer is what gets persisted.
					if streamErr == nil && resp != nil && !ts.abandoned.Load() {
						finalVisible := resp.Content
						if om, isOrphan := providers.DetectOrphanToolCallMarkup(finalVisible); isOrphan {
							finalVisible = om.Prose
						}
						if len(finalVisible) > len(lastChunk) && strings.HasPrefix(finalVisible, lastChunk) {
							if err := streamer.Update(providerCtx, finalVisible[len(lastChunk):]); err != nil {
								logger.DebugCF("agent", "Streaming tail flush error (client may have disconnected)", map[string]any{"error": err.Error()})
							}
						}
					}
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
			// ADR-087 D3/D9 (main call site): a tool call cut off at the
			// output-token limit gets one bounded repair before falling
			// through to ClassifyError/the media-downgrade/PDF paths below.
			if repaired, ok := al.evaluateTruncatedToolCallError(ts, err, callMessages, retry, maxRetries, &toolCallTruncationRepairUsed, llmModel, iteration); ok {
				callMessages = repaired
				continue
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
				// ADR-087 D3/D9 (media-downgrade retry call site): same
				// bounded repair as the main call site above.
				if repaired, ok := al.evaluateTruncatedToolCallError(ts, err, callMessages, retry, maxRetries, &toolCallTruncationRepairUsed, llmModel, iteration); ok {
					callMessages = repaired
					continue
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
				//
				// FR-028 / B-38: the check reads the one budget B — the retired
				// summarize_token_percent no longer scales the window here. What
				// it measures is the request the RETRY would assemble: the
				// messages of the failed call plus any recall span that became
				// active during it (the retry re-assembles from the session, so
				// an active span is part of the next request even though it was
				// not part of callMessages). windowTrim counts that span the
				// same way (FR-019 drop-span-first).
				if !compactionAttemptedOnTimeout && !ts.opts.NoHistory && !ts.agent.budgetChecksExempt() {
					// The sent surface, not the whole registry — same helper
					// windowTrim measures with (FR-028; see the pre-turn site).
					toolDefsTokens := al.sentToolSurfaceTokens(ts.agent, ts.opts.TranscriptSessionID, ts.sessionKey)
					retryMessages := callMessages
					if span := al.activeRecallSpan(ts.sessionKey); span != nil {
						retryMessages = append(append([]providers.Message(nil), callMessages...), span.Messages()...)
					}
					if isOverContextBudgetTokens(agentContextBudget(ts.agent), retryMessages, toolDefsTokens) {
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
						compression, ok := al.windowTrim(ts.agent, ts.opts.TranscriptSessionID, ts.sessionKey)
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
							// Site-3: post-timeout-trim assembly.
							newHistory := ts.agent.Sessions.GetHistory(ts.sessionKey)
							messages = al.assembleMessages(turnCtx, ts, newHistory, "", nil, activeSkillNames(ts.agent, ts.opts))
							continuationChain = nil
							if ts.continuationUnresolved() {
								// ADR-087 D6.7: a history rebuild loses the D6
								// continuation chain (it was never persisted
								// to session history) — re-append it exactly
								// once so the model still sees what it has
								// already written.
								continuationChain = continuationChainMessages(ts.continuationAccumulated())
								messages = append(messages, continuationChain...)
							}
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

				// force: the PROVIDER rejected this request with a context
				// error, so our own estimate said it fit and was wrong.
				// Honouring the "already fits" guard here would make the
				// retry byte-identical to the call that just failed.
				if compression, ok := al.windowTrimForce(ts.agent, ts.opts.TranscriptSessionID, ts.sessionKey, true); ok {
					al.emitEvent(
						EventKindContextCompress,
						ts.eventMeta("runTurn", "turn.context.compress"),
						ContextCompressPayload{
							Reason:            ContextCompressReasonRetry,
							DroppedMessages:   compression.DroppedMessages,
							RemainingMessages: compression.RemainingMessages,
						},
					)
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
				messages = al.assembleMessages(turnCtx, ts, newHistory, "", nil, activeSkillNames(ts.agent, ts.opts))
				continuationChain = nil
				if ts.continuationUnresolved() {
					// ADR-087 D6.7: same rebuild-restoration as Site-3 above.
					continuationChain = continuationChainMessages(ts.continuationAccumulated())
					messages = append(messages, continuationChain...)
				}
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
			// ADR-066 D7: typed, never silent — see typedTurnExit.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				var res turnResult
				var exitErr error
				res, turnStatus, exitErr = al.typedTurnExit(ts, iteration, llmModel, err)
				return res, exitErr
			}
		}
		if err != nil {
			turnStatus = TurnEndStatusError
			// Wave 1 (error-provenance hardening, ADR-051 §RD5 CRIT-001):
			// never emit raw err.Error() to the assistant-facing bus /
			// transcript. Build a *ProviderError from the wrapped chain
			// (best-effort — falls back to substring matching on err.Error()
			// when no FailoverError is in the chain) for the live
			// ErrorPayload, and classify via TranslateTurnError (ADR-087
			// D5): it recognizes a *common.ToolArgumentsError in err's chain
			// (CodeToolCallTruncated vs CodeToolArgs, per whether the
			// refusal carries real truncation evidence) before falling back
			// to the exact same errorToProviderError + TranslateLLMError
			// path this used to call directly — so a 401/413 buried in err
			// still classifies correctly (Codex C6).
			pe := errorToProviderError(err)
			llm := TranslateTurnError(err)

			// FR-017a: label an inconclusive residual 4xx after a
			// successful strip-retry. A later distinct classified
			// failure keeps its own code so live and persist agree.
			if outcomeRelabelApplies(llm.Code, ts.outcomeRelabel) {
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
					SessionID:     string(ts.routingSessionID),
				},
			)
			// FR-002: persist the translated provider error to the transcript
			// (write choke point — ADR-051 §RD5). pe threaded through so the
			// classifier sees status/body, not the stringified err.
			ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
			logger.ErrorCF("agent", "LLM call failed",
				map[string]any{
					"agent_id":  ts.agent.ID,
					"iteration": iteration,
					"model":     llmModel,
					"error":     err.Error(),
					"code":      string(llm.Code),
				})
			// ADR-087 D6.8: exhausted retries make no further provider call —
			// runTurn's deferred preserveTruncatedAccumulator keeps a D6
			// continuation left unresolved by a prior round.
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

		// ── Orphan tool-call markup: the single strip choke point ──
		//
		// Some models emit tool calls as XML-ish markup in the completion
		// TEXT and rely on the hosting provider to parse it back into
		// `tool_calls`. When that upstream parse does not complete, the
		// unconsumed remainder is flushed into the text instead — see
		// providers.DetectOrphanToolCallMarkup for the full dialect and the
		// live evidence. Two things must happen, and they are separate:
		//
		//  1. The residue must never be shown to a user as the assistant's
		//     own words. That is THIS strip, applied once here so every
		//     downstream consumer (citations, the transcript writers, the
		//     assistant history message, the terminal answer) sees text that
		//     has already been cleaned. The live-stream surface is filtered
		//     independently at the ChatStream callback above, because the
		//     gateway streamer persists what it accumulated, not this value.
		//
		//  2. A round that produced NO tool calls has to be repaired or
		//     reported — handled in the no-tool-calls branch below. Silence
		//     is the defect there, not the malformation.
		//
		// ReasoningContent is stripped too: the no-tool-calls branch falls
		// back to it when Content is empty, so leaving it alone would just
		// move the leak. Mutating it here is safe — handleReasoning below
		// receives its own string copy.
		orphanMarkup, hasOrphanMarkup := stripOrphanToolCallMarkup(response)
		if hasOrphanMarkup {
			logger.WarnCF("agent", "LLM emitted unparseable tool-call markup as text; markup suppressed",
				map[string]any{
					"agent_id":      ts.agent.ID,
					"iteration":     iteration,
					"model":         llmModel,
					"marker":        orphanMarkup.Marker,
					"markup_chars":  len(orphanMarkup.Markup),
					"prose_chars":   len(orphanMarkup.Prose),
					"tool_calls":    len(response.ToolCalls),
					"finish_reason": response.FinishReason,
				})
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

		// Record the provider-reported usage on the turn. debitLLMUsage also
		// records ts.lastUsage — the write that used to sit ~90 lines above
		// this block, guarded by its own
		// turnStateFromContext(turnCtx) lookup that resolves to this very same
		// ts (withTurnState(turnCtx, ts) is how turnCtx was built). Two copies
		// of one accounting step is how the ADR-087 D3.9 refused-attempt debit
		// came to omit SetLastUsage; there is now exactly one.
		//
		// ADR-087 D8: the old lastUsage site also called the now-deleted
		// SetLastFinishReason("for SubTurn truncation detection") — that
		// consumer was never built (GetLastFinishReason had zero callers); see
		// §5.1 of the ADR for the recorded gap.
		if response != nil {
			al.debitLLMUsage(ts, llmModel, response.Usage)
		}

		if len(response.ToolCalls) == 0 || gracefulTerminal {
			responseContent := response.Content
			if responseContent == "" && response.ReasoningContent != "" {
				responseContent = response.ReasoningContent
			}

			// ── Orphan tool-call markup with NO tool call: repair, or fail loudly ──
			//
			// The model tried to call a tool and nothing came back as a
			// structured call, so this round did no work at all. Ending the
			// turn here — which is what happened before this branch existed —
			// presents whatever prose survived the strip as a finished answer
			// and, when the whole response was markup, presents nothing at
			// all: a spinner that resolves into silence, with the goal record
			// left untouched and no error anywhere. That is the defect.
			//
			// Repair first: re-prompt with an explicit instruction to use the
			// tool-calling API, bounded by maxOrphanToolMarkupRepairs so a
			// model that cannot comply does not burn the turn. The repair note
			// is appended to the in-flight request only — never to session
			// history — so a transient protocol fault leaves no residue in the
			// durable archive, and the residue itself is never echoed back
			// (that would invite the model to repeat it verbatim).
			//
			// A graceful interrupt is the one case that does not repair: the
			// user asked the turn to wind down, so the stripped response
			// stands and the empty-response fallback below covers it.
			if hasOrphanMarkup && len(response.ToolCalls) == 0 {
				switch {
				case gracefulTerminal:
					// Fall through: honour the interrupt, do not re-prompt.
				case orphanToolMarkupRepairs < maxOrphanToolMarkupRepairs:
					orphanToolMarkupRepairs++
					logger.WarnCF("agent", "Tool call arrived as unparseable text; re-prompting the model",
						map[string]any{
							"agent_id":      ts.agent.ID,
							"iteration":     iteration,
							"model":         llmModel,
							"marker":        orphanMarkup.Marker,
							"finish_reason": response.FinishReason,
							"attempt":       orphanToolMarkupRepairs,
							"max_attempts":  maxOrphanToolMarkupRepairs,
						})
					al.emitEvent(
						EventKindLLMRetry,
						ts.eventMeta("runTurn", "turn.llm.retry"),
						LLMRetryPayload{
							Attempt:    orphanToolMarkupRepairs,
							MaxRetries: maxOrphanToolMarkupRepairs,
							Reason:     orphanToolMarkupRetryReason,
						},
					)
					messages = append(messages, orphanToolMarkupRepairMessage(response.FinishReason))
					continue
				default:
					// Repair budget spent. Fail LOUDLY — a typed error event
					// for the live client and a typed transcript entry for
					// replay. CodeToolArgs is the contract's existing
					// "tool-call argument format error"; the vocabulary is
					// contract data (contracts/components/schemas/LLMError.yaml),
					// so this path reuses it rather than inventing a code the
					// SPA has no catalogue entry for.
					turnStatus = TurnEndStatusError
					llm := LLMError{
						Code:      CodeToolArgs,
						Message:   UserMessageForCode(CodeToolArgs),
						Retryable: isRetryable(CodeToolArgs),
					}
					logger.WarnCF("agent", "Tool call kept arriving as unparseable text; ending turn with an error",
						map[string]any{
							"agent_id":      ts.agent.ID,
							"iteration":     iteration,
							"model":         llmModel,
							"marker":        orphanMarkup.Marker,
							"finish_reason": response.FinishReason,
							"attempts":      orphanToolMarkupRepairs,
						})
					al.emitEvent(
						EventKindError,
						ts.eventMeta("runTurn", "turn.error"),
						ErrorPayload{
							Stage:     orphanToolMarkupStage,
							Code:      string(llm.Code),
							Message:   llm.Message,
							ChatID:    ts.opts.ChatID,
							SessionID: string(ts.routingSessionID),
						},
					)
					ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
					// UAT A-12: wrap the TYPED refusal a provider raises for an
					// undecodable tool call. A task attempt's turn error is
					// classified by type only (task_attempt_turn_error.go's
					// attemptRecoverableTurnErrorCode — errors.As for
					// *common.ToolArgumentsError, then TranslateTurnError ->
					// CodeToolArgs, the same code this exit already reports to
					// the client). Untyped, this exhaustion failed the task on
					// the spot instead of consuming one attempt.
					return turnResult{}, fmt.Errorf(
						"model emitted unparseable tool-call markup (marker %q, finish_reason %q) after %d repair attempts: %w",
						orphanMarkup.Marker, response.FinishReason, orphanToolMarkupRepairs,
						common.NewToolArgumentsError("", common.ErrToolArgumentsUndecodable, false))
				}
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
			// ADR-087 D4/D6/D9: the one success-arm truncation handler,
			// ahead of the legacy empty-response retry loop below. Guarded
			// internally on isTruncatedFinishReason(FinishReason) &&
			// len(ToolCalls)==0 — when that guard does not match, the
			// verdict is truncationActionNone and every line below runs
			// completely unchanged (§7.10: a normal empty response with a
			// non-truncated finish reason still falls through to the
			// legacy loop). This single insertion covers both the main and
			// media-downgrade-retry call sites, since both `break` into
			// this shared downstream code on success; the empty-response
			// retry's own successful attempt (site 3) reaches the
			// identical branch again below, inside that loop.
			if verdict := al.evaluateTruncatedSuccess(ts, response, messages, providerToolDefs, gracefulTerminal, iteration, llmModel, &continuationChain); verdict.action != truncationActionNone {
				switch verdict.action {
				case truncationActionContinue:
					messages = verdict.messages
					continue turnLoop
				case truncationActionEnd:
					finalContent = verdict.finalContent
					break turnLoop
				}
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
					// ADR-087 D3/D9 (empty-response retry call site): same
					// bounded repair as the other two call sites — issue the
					// repaired call directly rather than looping, since this
					// mini-loop's own iteration budget is about EMPTY
					// content, a different concern from a truncated tool
					// call.
					if repaired, ok := al.evaluateTruncatedToolCallError(ts, retryErr, callMessages, 0, 1, &toolCallTruncationRepairUsed, llmModel, iteration); ok {
						callMessages = repaired
						retryResp, retryErr = callLLM(callMessages, providerToolDefs)
					}
					if retryErr != nil {
						// Propagate the error back to the outer error-handling block by
						// overwriting response/err and breaking out of both loops.
						response = nil
						err = retryErr
						break
					}
				}
				response = retryResp
				responseContent = response.Content
				if responseContent == "" && response.ReasoningContent != "" {
					responseContent = response.ReasoningContent
				}
				// ADR-087 D4/D6/D9: the empty-response retry's own
				// successful attempt goes through the SAME success-arm
				// handler as the other two call sites (§7.9).
				if verdict := al.evaluateTruncatedSuccess(ts, response, messages, providerToolDefs, gracefulTerminal, iteration, llmModel, &continuationChain); verdict.action != truncationActionNone {
					switch verdict.action {
					case truncationActionContinue:
						messages = verdict.messages
						continue turnLoop
					case truncationActionEnd:
						finalContent = verdict.finalContent
						break turnLoop
					}
				}
				// ADR-087 D3/D9: a repaired call can come back carrying a
				// (smaller, complete) TOOL CALL — the whole point of the D3
				// repair note is to solicit one. This mini-loop lives inside
				// the direct-answer branch, which was entered because the
				// ORIGINAL response had none, so nothing below inspects
				// response.ToolCalls: the repaired call would be discarded and
				// the turn would end on the defaultResponse fallback with
				// markTurnFailed, as if the model had stayed silent. Stop
				// retrying and let the fall-through below hand it to the
				// normal tool-dispatch path — which is what the main call site
				// would have done with the identical response (D9's "identical
				// at all three sites").
				if len(response.ToolCalls) > 0 && !gracefulTerminal {
					break
				}
			}
			// If the inner retry loop set an error, surface it via the outer error path.
			if err != nil {
				// ADR-066 D7: typed, never silent — see typedTurnExit.
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					var res turnResult
					var exitErr error
					res, turnStatus, exitErr = al.typedTurnExit(ts, iteration, llmModel, err)
					return res, exitErr
				}
				turnStatus = TurnEndStatusError
				// Wave 1 (error-provenance hardening): translate via the
				// shared classifier (CRIT-001). Never surface raw err.Error()
				// to the assistant / bus / transcript. ADR-087 D5/D9: this
				// empty-response retry's own error path is subsumed into the
				// same TranslateTurnError classification the main terminal
				// path uses, so a truncated tool call refused here reports
				// CodeToolCallTruncated identically to every other site.
				pe := errorToProviderError(err)
				llm := TranslateTurnError(err)

				// FR-017a: label an inconclusive residual 4xx after a
				// successful strip-retry. A later distinct classified
				// failure keeps its own code so live and persist agree.
				if outcomeRelabelApplies(llm.Code, ts.outcomeRelabel) {
					llm.Code = ts.outcomeRelabel
					llm.Message = UserMessageForCode(ts.outcomeRelabel)
				}

				al.emitEvent(
					EventKindError,
					ts.eventMeta("runTurn", "turn.error"),
					ErrorPayload{Stage: "llm_empty_retry", Code: string(llm.Code), Message: llm.Message, ProviderError: pe, ChatID: ts.opts.ChatID, SessionID: string(ts.routingSessionID)},
				)
				// FR-002: persist this provider error to the transcript (write
				// choke point).
				ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
				// ADR-087 D6.8: no further provider call follows this error
				// either — runTurn's deferred preserveTruncatedAccumulator
				// keeps a D6 continuation left unresolved by a prior round.
				return turnResult{}, fmt.Errorf("LLM call failed during empty-response retry: %w", err)
			}
			// ADR-087 D3/D9: re-test the CURRENT response. This branch was
			// entered on the ORIGINAL response's len(ToolCalls) == 0, but the
			// empty-response retry above may since have replaced `response`
			// with a D3-repaired one that carries a valid, smaller tool call.
			// The condition is byte-identical to this branch's own entry
			// condition, so the graceful-terminal case still finishes here — a
			// winding-down turn must never start executing tools — and
			// everything else falls out of this block into the ordinary
			// tool-dispatch path below.
			if len(response.ToolCalls) == 0 || gracefulTerminal {
				if strings.TrimSpace(responseContent) == "" {
					responseContent = defaultResponse
					ts.markTurnFailed()
					logger.WarnCF("agent", "LLM returned empty response after retry; using fallback message",
						map[string]any{"agent_id": ts.agent.ID, "iteration": iteration})
				}
				// ADR-087 D6.10: this round did not go through
				// evaluateTruncatedSuccess (it was not itself truncated, or it
				// carried tool calls on an earlier pass through this loop) —
				// but if an EARLIER round in this same turn dispatched a D6
				// continuation, the accumulator holds that earlier content and
				// must be prefixed here, or the prior round's answer is
				// silently dropped and only this round's own text survives.
				// (What the accumulator holds at this point is only what has
				// NOT already been settled into the record by
				// flushContinuationAccumulator — see its doc comment.)
				if ts.hadContinuation() {
					responseContent = ts.appendToAccumulator(responseContent)
					ts.resolveContinuation()
				}
				finalContent = responseContent
				logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
					map[string]any{
						"agent_id":      ts.agent.ID,
						"iteration":     iteration,
						"content_chars": len(finalContent),
					})
				break turnLoop
			}
			logger.InfoCF("agent", "empty-response retry returned a repaired tool call; dispatching it",
				map[string]any{
					"agent_id":   ts.agent.ID,
					"iteration":  iteration,
					"tool_calls": len(response.ToolCalls),
				})
		}

		// ADR-087 D6.10 / D4 (last paragraph, "truncated and has complete
		// tool calls"): a round with tool calls never reaches
		// evaluateTruncatedSuccess (its guard requires len(ToolCalls)==0),
		// so this is the one place that handles a D6 chain across a
		// tool-calling round.
		//
		// FIRST, unconditionally, settle whatever the accumulator still holds.
		// Everything this round is about to write — the assistant tool_calls
		// message into session history, and appendIntermediateAssistantTranscript's
		// narration entry into the transcript — lands AFTER any earlier
		// continuation prefix was produced, so the prefix has to reach disk
		// first or the record comes out as [P2][P1+P3] instead of
		// [P1][P2][P3]. flushContinuationAccumulator writes it in order and
		// clears it, which is also what stops it being emitted a second time
		// at turn end.
		//
		// THEN the two truncation cases:
		//   - FinishReason NOT truncated: an ordinary follow-up round
		//     resolves any D6 chain a prior round left pending.
		//   - FinishReason truncated but the response still carried
		//     complete tool calls (parseStreamResponse succeeds once every
		//     collected argument set decodes, regardless of finishReason):
		//     execute the calls once (unchanged below) and carry the
		//     truncation forward as PENDING ONLY. This round's own narration
		//     is deliberately NOT seeded into the accumulator: the tool-call
		//     branch immediately below persists that exact text itself, three
		//     ways (messages, Sessions.AddFullMessage,
		//     appendIntermediateAssistantTranscript), so seeding it here made
		//     every later accumulator reader emit the narration a second time
		//     — and, if a later call errored, made preserveTruncatedAccumulator
		//     append a second identical copy marked truncated. Never
		//     re-executed because of a continuation: nothing here re-dispatches
		//     these tool calls.
		al.flushContinuationAccumulator(ts, &continuationChain)
		if isTruncatedFinishReason(response.FinishReason) {
			ts.markContinuationPending()
		} else {
			ts.resolveContinuation()
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
			// ADR-066 D4×D6 (T066-13): an over-bound arguments string never
			// enters memory. The dispatch below refuses the call from the
			// PARSED args (FR-016 — this elision cannot mask that check),
			// and the refusal result names the real size, so echoing the
			// full blob into the assistant message would plant budget-
			// busting bytes in the archive and every later request that D5
			// can never empty (an assistant message is not a tool result) —
			// DS-3 #2 at the default window would then trip the D6 guard
			// that B-19 forbids. The elided echo is what the archive, the
			// window and every reload all see, so live == reload holds with
			// no projection entry.
			if bound := toolArgumentsBound(cfg); UserMessageChars(string(argumentsJSON)) > bound {
				elided, elideErr := json.Marshal(map[string]any{
					"_omnipus":   "arguments_elided_over_bound",
					"size_chars": UserMessageChars(string(argumentsJSON)),
					"cap_chars":  bound,
				})
				if elideErr == nil {
					argumentsJSON = elided
				} else {
					argumentsJSON = []byte("{}")
				}
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
		// setGoalSucceededThisRound tracks whether a set_goal call in THIS
		// model response already registered (or updated) the goal record — the
		// gate a few branches down uses to refuse a trailing AskUserQuestion
		// from the same response (UAT B-9 run 4: set_goal plus an invented
		// "Placeholder question - not used" ask in one response both ran; the
		// ask parked a turn whose goal record was already registered, freezing
		// the session for 18 minutes). A successful set_goal only ever happens
		// on a goal turn, so no separate goal-turn predicate is needed.
		setGoalSucceededThisRound := false
		for i, tc := range normalizedToolCalls {
			if ts.hardAbortRequested() {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts, "tool_loop", hardInterruptAbortReason)
			}

			// A done turn context (the agent's own turn timeout, or any other
			// cancellation of turnCtx that is not a hard abort) means no further
			// tool call in this batch may start. ExecuteWithContext does not
			// consult the context itself, so before this check a batch that began
			// before the deadline kept dispatching every queued call after it.
			// Mirrors the hard-abort check above (end the turn now) and the
			// steering/graceful-interrupt skip at the end of this loop (every call
			// that will not run still gets a synthetic result, so each tool_call
			// in the assistant message keeps its paired tool result). The turn
			// then ends through typedTurnExit — the same typed cancel/timeout exit
			// the provider call uses when this context is done — instead of
			// spending a provider round that can only fail on the same context.
			if ctxErr := turnCtx.Err(); ctxErr != nil {
				const ctxDoneSkipMessage = "Skipped: this turn ran out of time or was cancelled before this tool call could start."
				skipReason := "turn context done (" + ctxErr.Error() + ")"
				logger.InfoCF("agent", "Turn checkpoint: turn context done, skipping remaining tools",
					map[string]any{
						"agent_id":  ts.agent.ID,
						"completed": i,
						"skipped":   len(normalizedToolCalls) - i,
						"reason":    skipReason,
					})
				for j := i; j < len(normalizedToolCalls); j++ {
					skippedTC := normalizedToolCalls[j]
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   skippedTC.Name,
							Reason: skipReason,
						},
					)
					// ADR-066 D4: a synthetic skipped result is a builtin-failure
					// surface result like any other skip (FR-009).
					skippedMsg := al.admitToolResult(ts, toolResultAdmission{
						Tool: skippedTC.Name, ToolCallID: skippedTC.ID, Content: ctxDoneSkipMessage, IsError: true, ParallelN: len(normalizedToolCalls),
					}).Message
					messages = append(messages, skippedMsg)
				}
				res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, ctxErr)
				turnStatus = status
				return res, exitErr
			}

			// Unsanitize tool name from LLM — dots were replaced with underscores
			// for Anthropic/Azure API compatibility (e.g., "browser_navigate" → "browser.navigate").
			toolName := ts.agent.Tools.UnsanitizeToolName(tc.Name)
			toolArgs := cloneStringAnyMap(tc.Arguments)

			// pkg/agent/verifier_budget.go::VerifierBudget (JUDGE-FR-051/
			// FR-052): once a verifier adjudication's tool-call or byte cap
			// has been reached by every call already admitted this turn,
			// refuse EVERY further tool call here — before the quarantine
			// gate, before any hook, before dispatch — with a tool-result
			// message telling the Judge the cap was reached and to conclude
			// with the evidence already gathered. The turn is NEVER killed
			// (FR-052): this is an ordinary refused tool-result, the same
			// shape as the serialised-tool-argument-bound refusal further
			// below, and the loop continues so the Judge's next assistant
			// message can still emit its verdict.
			// verifierBudgetForTurn returns nil for every non-verifier turn
			// (the overwhelming majority — an ordinary chat turn's turnID
			// was never registered), and CheckCap on a nil *VerifierBudget
			// is a no-op, so this costs one map lookup on the hot path and
			// nothing more.
			if vb := verifierBudgetForTurn(ts.turnID); vb != nil {
				if refusal, capped := vb.CheckCap(); capped {
					logger.WarnCF("agent", "verifier tool call refused: adjudication budget cap reached (JUDGE-FR-051/FR-052)",
						map[string]any{
							"agent_id": ts.agent.ID,
							"tool":     toolName,
						})
					// ADR-066 D4: refused results enter through the choke
					// point on the builtin-failure surface (FR-009).
					// SkipVerifierBudgetAccounting is set because this
					// result exists ONLY because the cap was already
					// reached — it must not itself count toward that same
					// cap.
					refusedMsg := al.admitToolResult(ts, toolResultAdmission{
						Tool: tc.Name, ToolCallID: tc.ID, Content: refusal, IsError: true, ParallelN: len(normalizedToolCalls),
						SkipVerifierBudgetAccounting: true,
					}).Message
					messages = append(messages, refusedMsg)
					// ADR-066 D6 (T066-13): the window check runs after EVERY
					// admitted result — empty-only mid-turn, Skip never
					// moves; a thrash-guard fire ends the turn typed with no
					// further provider call (FR-032).
					if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
						res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
						turnStatus = status
						return res, exitErr
					}
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   toolName,
							Reason: refusal,
						},
					)
					continue
				}
			}

			// A call to a tool this request did not offer never runs (ADR-088
			// D3: the narrowed goal request's "exactly two" is exact; ADR-071
			// §1.1: a lazy tool is callable only once ToolSearch promotes it).
			// Checked on the pre-hook name, before the quarantine gate, hooks,
			// the argument bound and any approval prompt — a call that will not
			// run must not cost the user an approval. Not a policy denial: the
			// denial ledger and quarantine are not consulted, and a tool policy
			// denies at filter time is left to the exec-time deny path below
			// (see toolNotOfferedRefusal).
			if refusal, notOffered := al.toolNotOfferedRefusal(
				ts, offeredTools, tc.Name, toolName, filterTimePolicyMap, goalForce, cfg.Tools.Manifest.Compressed,
			); notOffered {
				logger.WarnCF("agent", "Tool call refused: the tool was not offered in this request",
					map[string]any{
						"agent_id":  ts.agent.ID,
						"tool":      toolName,
						"iteration": iteration,
						"narrowed":  goalForce.layer1,
					})
				refusedMsg := al.admitToolResult(ts, toolResultAdmission{
					Tool: tc.Name, ToolCallID: tc.ID, Content: refusal, IsError: true, ParallelN: len(normalizedToolCalls),
				}).Message
				messages = append(messages, refusedMsg)
				// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
				// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
				// ends the turn typed with no further provider call (FR-032).
				if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
					res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
					turnStatus = status
					return res, exitErr
				}
				al.emitEvent(
					EventKindToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   toolName,
						Reason: "tool_not_offered",
					},
				)
				continue
			}

			// UAT B-9 run 4: an AskUserQuestion trailing a SUCCESSFUL set_goal
			// from the SAME model response never runs. The rubric note already
			// says the two narrowed doors are alternatives ("Call exactly ONE
			// of the two, never both in the same response"); the live case had
			// the model register the record and then emit an invented
			// "Placeholder question - not used" ask, whose park froze a session
			// for 18 minutes even though the goal record was registered. The
			// ask is refused with a result telling the model to work now, or to
			// ask a REAL question on its next turn. Refused here, before
			// dispatch, so no park happens, no question card is created, and
			// the FR-010 question-round budget is not spent (that bump fires
			// only on a genuine ParksTurn success below).
			if toolName == tools.AskUserQuestionToolName && setGoalSucceededThisRound {
				const askAfterSetGoalRefusal = "AskUserQuestion was not called: this same response already " +
					"registered the goal record with set_goal. Registering the record and asking are alternatives — " +
					"you chose to register. Start working on the goal now (your full tool set returns on the next " +
					"request), or, if you are genuinely blocked on a real question, ask that real question on your " +
					"next turn — never a placeholder."
				logger.WarnCF("agent", "goal: refusing an AskUserQuestion trailing a successful set_goal in the same response",
					map[string]any{
						"agent_id":  ts.agent.ID,
						"turn_id":   ts.turnID,
						"iteration": iteration,
					})
				refusedMsg := al.admitToolResult(ts, toolResultAdmission{
					Tool: tc.Name, ToolCallID: tc.ID, Content: askAfterSetGoalRefusal, IsError: true, ParallelN: len(normalizedToolCalls),
				}).Message
				messages = append(messages, refusedMsg)
				// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
				// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
				// ends the turn typed with no further provider call (FR-032).
				if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
					res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
					turnStatus = status
					return res, exitErr
				}
				al.emitEvent(
					EventKindToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   toolName,
						Reason: "goal_turn_ask_after_set_goal",
					},
				)
				continue
			}

			// ADR-058 fix: ledgerToolName is the PRE-HOOK tool name, captured
			// before hooks.BeforeTool below gets a chance to run. Every
			// recordToolDenial/recordQuarantineReplay call for THIS call must
			// key the ledger by this value, not by whatever toolName holds
			// after hooks.BeforeTool's HookActionContinue/HookActionModify
			// case (a few lines down) may have reassigned it to
			// toolReq.Tool — because the quarantine gate immediately below
			// looks a tool up BEFORE any hook runs, using exactly this
			// pre-hook value, on EVERY call including the next one. A hook
			// that renames a tool would otherwise store its quarantine entry
			// under the RENAMED (post-hook) name while every future lookup
			// for the same incoming call keys on the ORIGINAL (pre-hook)
			// name — a latent key-shape mismatch that means the short-circuit
			// silently never fires for a renaming hook, and every repeat call
			// resumes a full approval round-trip. No in-tree hook renames a
			// tool today, but nothing prevented one from doing so.
			ledgerToolName := toolName

			// ADR-058 FR-058-11: the quarantine gate. A tool that has already
			// produced one PERMANENT denial earlier in this turn is answered
			// from the cached payload here — before hooks.BeforeTool, the
			// TOCTOU re-check, and the approval path, so none of them run for
			// this call: no hook call, no policy re-resolution, no
			// CheckGrantOrRequestApproval, no RequestApproval, no
			// tool_approval_required frame. The turn CONTINUES (D5 rejected
			// removing the tool from tools[]; the advertised tool set stays
			// stable and this gate is what makes offering it again safe).
			if payload, qReason, quarantined := ts.quarantinedDenialFor(ledgerToolName); quarantined {
				al.emitPolicyDenyAudit(ts, toolName, "quarantined", qReason)
				// ADR-058 fix: persist a transcript record for this replay too.
				// Before this, only the FIRST denial that created the
				// quarantine entry ever produced a tool_call transcript
				// entry — replays 2..N left no record at all and vanished on
				// reload. This never blocks (quarantine is a synchronous
				// short-circuit), so there is no preceding `pending`
				// placeholder to settle; settleAskToolCallTranscript already
				// handles that "no placeholder" case by appending directly
				// (the same shape the headless auto-deny site below relies
				// on for the identical reason).
				settleAskToolCallTranscript(ts, session.ToolCallID(tc.ID), toolName, toolArgs, qReason)
				// ADR-066 D4: denied results enter through the choke point on the
				// builtin-failure surface (FR-009); it persists the line itself.
				deniedMsg := al.admitToolResult(ts, toolResultAdmission{
					Tool: tc.Name, ToolCallID: tc.ID, Content: payload, IsError: true, ParallelN: len(normalizedToolCalls),
				}).Message
				messages = append(messages, deniedMsg)
				// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
				// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
				// ends the turn typed with no further provider call (FR-032).
				if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
					res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
					turnStatus = status
					return res, exitErr
				}
				al.emitEvent(
					EventKindToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   toolName,
						Reason: fmt.Sprintf("permission_denied (quarantined: %s)", qReason),
					},
				)
				if used, exhausted := ts.recordQuarantineReplay(ledgerToolName); exhausted {
					turnStatus = TurnEndStatusAborted
					return al.abortTurnForToolDenialBudget(ts, ledgerToolName, qReason, used)
				}
				continue
			}

			// ADR-085 BROWSER-FR-016/FR-016a: once this turn's control-gate
			// deferral bound (BROWSER-FR-014, N=3) has been reached, every
			// LATER control-gated browser tool call short-circuits HERE —
			// before hooks.BeforeTool, before dispatch, before any CDP
			// contact, no lease acquisition, no audit action row, no entry
			// into pkg/tools/browser at all. This is a SEPARATE ledger and
			// refusal from the quarantine gate immediately above: it shares
			// this tool-dispatch point and nothing else (see loop.go's
			// shared-file-chain doc). Never mixes with turnDenialBudget.
			if isBrowserControlGatedTool(ledgerToolName) && ts.browserControlGateExhausted() {
				al.emitEvent(
					EventKindToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   toolName,
						Reason: "browser_control_gate_exhausted",
					},
				)
				exhaustedMsg := browserControlGateExhaustedMessage(toolName)
				settleAskToolCallTranscript(ts, session.ToolCallID(tc.ID), toolName, toolArgs, exhaustedMsg)
				admittedMsg := al.admitToolResult(ts, toolResultAdmission{
					Tool: tc.Name, ToolCallID: tc.ID, Content: exhaustedMsg, IsError: false, ParallelN: len(normalizedToolCalls),
				}).Message
				messages = append(messages, admittedMsg)
				if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
					res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
					turnStatus = status
					return res, exitErr
				}
				continue
			}

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
					// ADR-066 D4: denied results enter through the choke point on the
					// builtin-failure surface (FR-009); it persists the line itself.
					deniedMsg := al.admitToolResult(ts, toolResultAdmission{
						Tool: tc.Name, ToolCallID: tc.ID, Content: denyContent, IsError: true, ParallelN: len(normalizedToolCalls),
					}).Message
					messages = append(messages, deniedMsg)
					// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
					// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
					// ends the turn typed with no further provider call (FR-032).
					if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
						res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
						turnStatus = status
						return res, exitErr
					}
					// ADR-058 fix: this branch used to `continue` with no
					// ClassifyDenial, no recordToolDenial and no budget check
					// at all — a third-party ProcessHook that denies a tool
					// reproduced the pre-ADR-058 infinite retry exactly,
					// despite tool_denial.go's package doc, turnDenialLedger's
					// doc, and audit.EventTurnAbortedToolDenialBudget's doc all
					// asserting the budget covers "every denial response
					// handed to the model". This does NOT route through
					// ClassifyDenial/denialPayloadJSON — decision.Reason is
					// arbitrary third-party-hook free text with no fixed
					// literal to classify against, and denyContent (plain
					// text, not a JSON envelope) is already an honest
					// attribution ("denied by hook", never a false claim of a
					// human decision) so D1/D2 do not apply here. quarantine
					// is unconditionally true: a hook that explicitly denies a
					// tool is a deliberate decision, like a human "no" or a
					// resolved policy deny, and there is no FR-079-style
					// re-check mechanism for hook decisions that a cached
					// short-circuit would disable.
					const hookDeniedLedgerReason = "hook_denied"
					if used, exhausted := ts.recordToolDenial(ledgerToolName, hookDeniedLedgerReason, true, denyContent); exhausted {
						turnStatus = TurnEndStatusAborted
						return al.abortTurnForToolDenialBudget(ts, ledgerToolName, hookDeniedLedgerReason, used)
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

			// ADR-066 D4 / FR-016: the tool-argument bound, measured on the
			// serialised arguments AFTER hooks.BeforeTool (a hook may rewrite
			// them) and BEFORE any approval round-trip — a call that will be
			// refused must not cost the user an approval prompt. Over the
			// cap the tool does not run; the ADR-060-family refusal
			// (tools.ToolArgumentRefusalResult) enters through the choke
			// point like any other result and the turn continues — the model
			// sees the size and the cap and retries smaller. Not a policy
			// denial: the ledger/quarantine machinery is deliberately not
			// consulted.
			if argChars, argCap := serialisedToolArgsChars(toolArgs), toolArgumentsBound(cfg); argChars > argCap {
				refusal := tools.ToolArgumentRefusalResult(toolName, argChars, argCap)
				logger.WarnCF("agent", "Tool call refused: serialised arguments exceed the cap (ADR-066 D4)",
					map[string]any{
						"agent_id":   ts.agent.ID,
						"tool":       toolName,
						"size_chars": argChars,
						"cap_chars":  argCap,
					})
				refusedMsg := al.admitToolResult(ts, toolResultAdmission{
					Tool: tc.Name, ToolCallID: tc.ID, Content: refusal.ContentForLLM(), IsError: true, ParallelN: len(normalizedToolCalls),
				}).Message
				messages = append(messages, refusedMsg)
				// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
				// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
				// ends the turn typed with no further provider call (FR-032).
				if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
					res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
					turnStatus = status
					return res, exitErr
				}
				al.emitEvent(
					EventKindToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   toolName,
						Reason: fmt.Sprintf("%s: serialised arguments of %d chars exceed the %d-char cap", tools.ToolArgumentsTooLargeCode, argChars, argCap),
					},
				)
				continue
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
					// ADR-066 D4: denied results enter through the choke point on the
					// builtin-failure surface (FR-009); it persists the line itself.
					deniedMsg := al.admitToolResult(ts, toolResultAdmission{
						Tool: tc.Name, ToolCallID: tc.ID, Content: denyContent, IsError: true, ParallelN: len(normalizedToolCalls),
					}).Message
					messages = append(messages, deniedMsg)
					// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
					// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
					// ends the turn typed with no further provider call (FR-032).
					if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
						res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
						turnStatus = status
						return res, exitErr
					}
					// ADR-058 fix: same rationale as the HookActionDenyTool
					// branch above — this hook-deny path used to bypass the
					// ledger entirely (no ClassifyDenial, no
					// recordToolDenial, no budget), so a ToolApprover hook
					// (e.g. the gateway's wsApprovalHook) denying the same
					// tool repeatedly reproduced the pre-ADR-058 infinite
					// retry exactly, despite this file's own doc comments
					// claiming total budget coverage. Kept as plain text
					// (denyContent), not the JSON permission_denied envelope:
					// approval.Reason is arbitrary hook-supplied free text
					// with no fixed literal to classify against, and the
					// message already names the actual cause ("denied by
					// approval hook") rather than claiming a human decision
					// that did not occur.
					const approvalHookDeniedLedgerReason = "approval_hook_denied"
					if used, exhausted := ts.recordToolDenial(ledgerToolName, approvalHookDeniedLedgerReason, true, denyContent); exhausted {
						turnStatus = TurnEndStatusAborted
						return al.abortTurnForToolDenialBudget(ts, ledgerToolName, approvalHookDeniedLedgerReason, used)
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
				// ADR-058 site 1: this branch has no approver-supplied
				// reason at all, so it uses the fixed loop pseudo-reason
				// "policy_denied" (spec §4.1 row 9) — the one table row that
				// exists purely so this site can be uniformly rewired
				// through denialPayloadJSON/ClassifyDenial without changing
				// the pre-existing message text ("already true" per ADR D2;
				// the payload as a whole does still gain "reason" and
				// "permanent" fields it previously lacked, see the
				// denialTable row's own comment).
				const policyDeniedReason = "policy_denied"
				cls, _ := ClassifyDenial(policyDeniedReason)
				denyMsg := denialPayloadJSON(toolName, policyDeniedReason, cls)
				al.emitPolicyDenyAudit(ts, toolName, "deny", "mid_turn_policy_change")
				// ADR-066 D4: denied results enter through the choke point on the
				// builtin-failure surface (FR-009); it persists the line itself.
				deniedMsg := al.admitToolResult(ts, toolResultAdmission{
					Tool: tc.Name, ToolCallID: tc.ID, Content: denyMsg, IsError: true, ParallelN: len(normalizedToolCalls),
				}).Message
				messages = append(messages, deniedMsg)
				// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
				// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
				// ends the turn typed with no further provider call (FR-032).
				if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
					res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
					turnStatus = status
					return res, exitErr
				}
				al.emitEvent(
					EventKindToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   toolName,
						Reason: "permission_denied (mid-turn policy change)",
					},
				)
				// ADR-058 fix: policy_denied must NOT quarantine, even
				// though cls.Permanent is true for message-classification
				// purposes. FR-079's TOCTOU re-check exists BECAUSE policy
				// can change again mid-turn — quarantining here would
				// silently disable that re-check for the rest of the turn,
				// serving every later call to this tool a stale cached
				// denial with no policy re-resolution at all. If an
				// operator fixes the policy back to allow/ask a moment
				// later, a quarantined tool would never notice; passing
				// `false` here (not cls.Permanent) keeps recordToolDenial's
				// aggregate-budget counting intact while excluding this one
				// reason from the quarantine cache, so resolveToolPolicyAtExec
				// keeps running on every subsequent call to this tool. See
				// recordToolDenial's own doc for the quarantine-vs-Permanent
				// distinction this relies on.
				if used, exhausted := ts.recordToolDenial(ledgerToolName, policyDeniedReason, false, denyMsg); exhausted {
					turnStatus = TurnEndStatusAborted
					return al.abortTurnForToolDenialBudget(ts, ledgerToolName, policyDeniedReason, used)
				}
				continue
			}
			if toctouPolicy == "ask" {
				// Headless auto-deny (issue #264, FR-009): a scheduled run has no
				// operator to approve, so any `ask`-policy tool is denied without
				// ever issuing an approval request — the run must never stall.
				if ts.opts.AutoDenyAsk {
					// ADR-058: this literal is a DEDICATED denialTable row
					// (agent.autoDenyHeadlessReason, tool_denial.go) with
					// headless-specific wording, not the generic
					// unknown-reason fallback — an earlier revision of this
					// comment described the fallback path, which produced a
					// stuttering message ("the tool call was refused (reason:
					// auto-denied: ...)") with no headless-specific guidance
					// and failed AC-01's "every driven reason must be known"
					// guard. A headless scheduled run has no operator by
					// construction, for the whole run, so Permanent: true is
					// the correct classification (ADR D1 row 9).
					const denialReason = autoDenyHeadlessReason
					cls, _ := ClassifyDenial(denialReason)
					denyMsg := denialPayloadJSON(toolName, denialReason, cls)
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
					// Persist the refusal as a real tool_call entry. This path
					// never blocks (there is no approver on a headless run), so
					// there is no pending placeholder to settle — but without
					// this the scheduled run's transcript showed the tool had
					// simply never been called, with the reason living only in
					// the audit log. settleAskToolCallTranscript appends when it
					// finds no placeholder, which is exactly this case.
					settleAskToolCallTranscript(
						ts, session.ToolCallID(tc.ID), toolName, toolArgs, denialReason)
					// ADR-066 D4: denied results enter through the choke point on the
					// builtin-failure surface (FR-009); it persists the line itself.
					deniedMsg := al.admitToolResult(ts, toolResultAdmission{
						Tool: tc.Name, ToolCallID: tc.ID, Content: denyMsg, IsError: true, ParallelN: len(normalizedToolCalls),
					}).Message
					messages = append(messages, deniedMsg)
					// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
					// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
					// ends the turn typed with no further provider call (FR-032).
					if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
						res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
						turnStatus = status
						return res, exitErr
					}
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   toolName,
							Reason: fmt.Sprintf("permission_denied (ask auto-denied: %s)", denialReason),
						},
					)
					if used, exhausted := ts.recordToolDenial(ledgerToolName, denialReason, cls.Permanent, denyMsg); exhausted {
						turnStatus = TurnEndStatusAborted
						return al.abortTurnForToolDenialBudget(ts, ledgerToolName, denialReason, used)
					}
					continue
				}
				// ask-policy: consult the session-scoped "Always Allow" grant
				// store first (ADR-036 §3.4 — the sole grant-consultation point
				// now that the legacy WS-frame gate, wsApprovalHook, has been
				// retired), then fall through to interactive human approval
				// (FR-011) only when no grant is on file.
				//
				// A standing grant resolves without ever contacting a human, so
				// it must NOT write a pending placeholder — that would render an
				// "awaiting approval" card for a call nobody was asked about.
				// Consult the grant store separately here (CheckGrantOrRequestApproval
				// consults the SAME store first, so a granted call still
				// short-circuits identically), and write the placeholder only on
				// the path that genuinely blocks on a human.
				approved := al.ApprovalGrants().IsAllowed(ts.transcriptSessionID, ts.agentID, toolName, toolArgs)
				denialReason := ""
				if !approved {
					// About to block on a human, for up to the approval
					// registry's timeout (600 s by default, configurable —
					// pkg/gateway/gateway.go's defaultToolApprovalTimeout). The
					// wait is server-side and needs no browser attached: a task
					// run's approval waits exactly like a chat turn's (ADR-082;
					// pinned by pkg/gateway/task_run_ask_approval_test.go).
					// Record the call as `pending`
					// FIRST so the thread shows what the turn is waiting on for
					// the whole wait, and so a reload mid-wait still shows it:
					// the tool_approval_required WS frame is live-only and does
					// not survive a refresh. Before this, an unanswered approval
					// rendered nothing at all and the turn looked hung for no
					// visible reason.
					recordAskPendingToolCall(ts, session.ToolCallID(tc.ID), toolName, toolArgs)
					approved, denialReason = al.CheckGrantOrRequestApproval(
						turnCtx, ts.transcriptSessionID, ts.agentID, toolName, tc.ID, ts.turnID, toolArgs,
					)
				}
				if !approved {
					// Settle the placeholder to `denied` with the outcome
					// reason, so "denied by the user" and "expired after five
					// minutes with nobody watching" are distinguishable in the
					// thread and on replay.
					settleAskToolCallTranscript(
						ts, session.ToolCallID(tc.ID), toolName, toolArgs, denialReason)
					// ADR-058 site 3 — the original defect: denialReason here
					// is verbatim from CheckGrantOrRequestApproval, so it is
					// classified for real rather than assumed to be a user
					// "no". ClassifyDenial handles every reason this call is
					// KNOWN to be able to produce — not just the
					// approvals.go-authored six (user, timeout, saturated,
					// cancel, restart, batch_short_circuit), but also
					// internal_error (policy_approver.go's nil-entry branch),
					// no_approver_configured (tool_approver.go's nop
					// fallback), the empty reason, and "session canceled"
					// (verified end-to-end in this session:
					// pkg/agent/cancel.go::AgentLoop.RequestCancel ->
					// hooks.CancelPendingApprovals ->
					// pkg/gateway/approvals.go::cancelAllPendingForSessions's
					// ApprovalOutcome{Reason: "session canceled"} -> here,
					// distinct from the single-word "cancel" reason above).
					// An earlier revision of this comment claimed the table
					// "covers every reason this call can produce" and
					// enumerated only nine of these — that was never a
					// closed set, and any reason NOT in denialTable still
					// fails safe (Permanent: true) through ClassifyDenial's
					// unknown-reason fallback rather than being silently
					// treated as retryable.
					cls, _ := ClassifyDenial(denialReason)
					denyMsg := denialPayloadJSON(toolName, denialReason, cls)
					al.emitPolicyDenyAudit(ts, toolName, "ask", denialReason)
					// ADR-066 D4: denied results enter through the choke point on the
					// builtin-failure surface (FR-009); it persists the line itself.
					deniedMsg := al.admitToolResult(ts, toolResultAdmission{
						Tool: tc.Name, ToolCallID: tc.ID, Content: denyMsg, IsError: true, ParallelN: len(normalizedToolCalls),
					}).Message
					messages = append(messages, deniedMsg)
					// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
					// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
					// ends the turn typed with no further provider call (FR-032).
					if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
						res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
						turnStatus = status
						return res, exitErr
					}
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   toolName,
							Reason: fmt.Sprintf("permission_denied (ask denied: %s)", denialReason),
						},
					)
					// ADR-058 §3.5 (R5, Binding Rule 4 — the positive lower
					// bound): cls.Permanent is false ONLY for "saturated" at
					// THIS site, so recordToolDenial never quarantines it
					// here — a later call to the same tool in the same turn
					// is free to reach the approver and execute (AC-06).
					if used, exhausted := ts.recordToolDenial(ledgerToolName, denialReason, cls.Permanent, denyMsg); exhausted {
						turnStatus = TurnEndStatusAborted
						return al.abortTurnForToolDenialBudget(ts, ledgerToolName, denialReason, used)
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
			toolExecSID, toolExecProducingSID := u9ToolExecSessionIDs(ts)
			al.emitEvent(
				EventKindToolExecStart,
				ts.eventMeta("runTurn", "turn.tool.start"),
				ToolExecStartPayload{
					ToolCallID: session.ToolCallID(tc.ID),
					ChatID:     ts.chatID,
					// ADR-057 FR-011/FR-012/FR-013 (W4/W5d, U9): see
					// u9ToolExecSessionIDs and
					// ToolExecStartPayload.SessionID/.ProducingSessionID's doc
					// comments (events.go, U23) for the full rationale.
					SessionID:          toolExecSID,
					Tool:               toolName,
					Arguments:          cloneEventArguments(toolArgs),
					ParentSpawnCallID:  session.ToolCallID(ts.parentSpawnCallID),
					AgentID:            ts.resolveActiveAgentID(), // Bug 1: runtime-current agent
					ProducingSessionID: toolExecProducingSID,
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

				// FIX 3: a turn that was ever the target of a cancel claim
				// (ts.cancelFired — set exactly once, and never reset, by
				// ClaimCancel/handleCancel; see cancel.go's RequestCancel) must
				// not spring back to life through this async completion. This
				// closure captures the PARENT ts that dispatched the async
				// tool call (delegate/background bash); the callback fires
				// independently, on its own goroutine, whenever that tool
				// finishes — which is routinely AFTER the user has already
				// clicked Stop, since the whole point of async dispatch is
				// that the parent turn moves on immediately (see executeAsync's
				// doc comment in pkg/tools/delegate.go). Without this guard,
				// Notify below publishes an inbound "system" message
				// unconditionally, and processSystemMessage (this file) turns
				// it into a BRAND NEW, fully-tooled turn — the agent can then
				// narrate the delegation and even issue ANOTHER delegate call,
				// seconds after being told to stop (live-reproduced: a third
				// turn, ID parent+2, arriving ~3s after the cancel completed,
				// calling delegate again). Skipping Notify here does not lose
				// the async tool's own result: spawnSubTurn inherits the
				// parent's TranscriptSessionID/TranscriptStore (subturn.go), so
				// the child's own tool calls and final answer are already
				// persisted to the SAME session's transcript via its own turn
				// — only this callback's REACTIVE continuation turn is
				// suppressed, which is exactly the behavior a canceled turn
				// should have.
				if ts.cancelFired.Load() {
					logger.InfoCF("agent", "Suppressing async-notify continuation turn: originating turn was canceled",
						map[string]any{
							"tool":        asyncToolName,
							"channel":     ts.channel,
							"chat_id":     ts.chatID,
							"agent_id":    ts.agent.ID,
							"content_len": len(content),
						})
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
							SessionID:         string(ts.routingSessionID),
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
					// ADR-066 D4: denied results enter through the choke point on the
					// builtin-failure surface (FR-009); it persists the line itself.
					deniedMsg := al.admitToolResult(ts, toolResultAdmission{
						Tool: tc.Name, ToolCallID: tc.ID, Content: errMsg, IsError: true, ParallelN: len(normalizedToolCalls),
					}).Message
					messages = append(messages, deniedMsg)
					// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
					// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
					// ends the turn typed with no further provider call (FR-032).
					if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
						res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
						turnStatus = status
						return res, exitErr
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

			// UAT fix (fix/uat-defects-2026-08-22, Defect 1): dispatch-side
			// circuit breaker for a tool call that has already failed with
			// this exact same name+arguments toolFailureCircuitBreakThreshold
			// times in a row THIS turn (see tool_failure_circuit_breaker.go).
			// Mirrors the SEC-26 rate-limit denial immediately above — fail
			// closed (do not even call Execute), surface the denial as a
			// normal tool-result error so the model can react, keep the
			// turn running rather than aborting it. Skipping Execute here
			// (rather than only warning post-hoc) is what actually bounds
			// the token burn: a model that ignores the warning notice below
			// still cannot force more than toolFailureCircuitBreakThreshold
			// real dispatch attempts of the identical call in one turn.
			toolCBSig := toolCallSignature(toolName, toolArgs)
			if cbReason, tripped := ts.toolCircuitBreakerTripped(toolCBSig); tripped {
				errMsg := toolCircuitBreakerDenialMessage(toolName, cbReason)
				deniedMsg := al.admitToolResult(ts, toolResultAdmission{
					Tool: tc.Name, ToolCallID: tc.ID, Content: errMsg, IsError: true, ParallelN: len(normalizedToolCalls),
				}).Message
				messages = append(messages, deniedMsg)
				if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
					res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
					turnStatus = status
					return res, exitErr
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

			// ADR-071 §4.3.1(a) "Clear": a tool about to be dispatched is, by
			// definition, no longer an abandoned promotion — delete any
			// pending search-follow-up entry for it under this agent's
			// bucket. Runs unconditionally (harmless no-op when there is no
			// pending entry, e.g. a full-tier tool or a by-name load).
			al.clearPendingSearchPromotion(ts.manifestBucket(), toolName)

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
			// Carry the turn's EXISTING AutoDenyAsk onto the tool context.
			// The loop already uses it to auto-deny `ask`-policy calls; a
			// tool that must refuse one ARGUMENT rather than the whole call
			// (browser_handle_dialog{accept:true}) has no other way to know
			// whether anyone is there to approve. Deliberately the same
			// field, not a second discriminator: two independently-computed
			// answers to "is anyone there" would eventually disagree.
			execCtx = tools.WithAutoDenyAsk(execCtx, ts.opts.AutoDenyAsk)
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

			// ADR-085 BROWSER-FR-012a/FR-013/FR-015: a REAL dispatch (not the
			// FR-016 short-circuit above, which never reaches here) came back
			// deferred by the browser control gate. Record it on this turn's
			// ledger via the STRUCTURAL Deferred field alone — never by
			// parsing ForLLM's prose — and, on exactly the call that reaches
			// BROWSER-FR-014's bound (the third), append FR-015's terminal
			// instruction to this one result's own ForLLM.
			if toolResult.Deferred != nil && toolResult.Deferred.Gate == browserControlDeferralGate {
				if _, justReachedBound := ts.recordBrowserControlDeferral(); justReachedBound {
					toolResult.ForLLM += browserControlGateBoundReachedNote
				}
			}

			// UAT fix (fix/uat-defects-2026-08-22, Defect 1): update this
			// exact call's consecutive-failure streak. A success (or a hook
			// that turned a failure into one) clears the streak outright; a
			// real failure bumps it and, once it crosses the warn threshold,
			// augments the error content the model is about to see. The
			// REFUSAL side lives entirely in the pre-dispatch check above
			// (toolCircuitBreakerTripped): D-81 made it refuse attempt
			// number toolFailureCircuitBreakThreshold of an identical call
			// BEFORE dispatch, so a streak recorded here can never reach the
			// break threshold — a post-dispatch arm that tripped the breaker
			// at streak >= toolFailureCircuitBreakThreshold was unreachable
			// and is deleted (round-3 cut list, 2026-09-14 review; see the
			// reachability note in tool_failure_circuit_breaker.go). Keyed
			// on toolCBSig computed before dispatch/hooks so a hook renaming
			// the tool does not fragment the streak it is meant to track.
			if toolResult.IsError {
				// A failure ends any identical-SUCCESS run; identical failures
				// are the failure streak's job.
				ts.resetToolSuccessRepeat()
				streak := ts.recordToolFailure(toolCBSig)
				switch {
				case streak >= toolFailureCircuitBreakThreshold:
					reason := toolFailureCircuitBreakerReason(toolName, streak)
					ts.tripToolCircuitBreaker(toolCBSig, reason)
					toolResult.ForLLM = toolResult.ContentForLLM() + toolFailureWarnNotice(toolName, streak)
				case streak >= toolFailureWarnThreshold:
					toolResult.ForLLM = toolResult.ContentForLLM() + toolFailureWarnNotice(toolName, streak)
				}
			} else {
				ts.recordToolSuccess(toolCBSig)
				// Feeds the same-response ask refusal above: a set_goal that
				// succeeded in this response makes any trailing AskUserQuestion
				// in the same batch a refusal, not a park.
				if ledgerToolName == tools.SetGoalToolName {
					setGoalSucceededThisRound = true
				}
				// Identical SUCCESSFUL repetition (tool_failure_circuit_breaker.go):
				// warn the model, and at the stop threshold end the turn after
				// this round (the takeToolRepeatStop check after the tool loop).
				switch run := ts.recordToolSuccessRepeat(toolCBSig); {
				case run >= toolRepeatStopThreshold:
					ts.requestToolRepeatStop(toolRepeatStopNotice(toolName, run))
					toolResult.ForLLM = toolResult.ContentForLLM() + toolRepeatWarnNotice(toolName, run)
				case run >= toolRepeatWarnThreshold:
					toolResult.ForLLM = toolResult.ContentForLLM() + toolRepeatWarnNotice(toolName, run)
				}
			}
			// UAT 2026-09-13 D-23: a loop of SUCCESSFUL, mutually-cancelling
			// calls (create X / delete X / create X …) never touches the
			// streak above. Record every dispatched call's signature and warn
			// once the turn's history repeats a short cycle; the pre-dispatch
			// check above (toolCircuitBreakerTripped) refuses the call that
			// would extend it past the break point.
			if loopNotice := ts.recordToolCallForLoopDetection(toolCBSig); loopNotice != "" {
				toolResult.ForLLM = toolResult.ContentForLLM() + loopNotice
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
					// ADR-065 FR-6: media sends carry their origin too, so
					// send_file is not a silent gap in the audit trail.
					//
					// From ts.agent.ID, NOT tools.ToolAgentID(ctx): ctx here is
					// runTurn's ORIGINAL parameter and never carries the agent
					// id — only the derived turnCtx does. The FIX 1 comment on
					// WorkspaceID two fields below says precisely this, and the
					// first version of this line ignored it and read back "".
					AgentID: ts.agent.ID,
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

			// ADR-066 D4 (FR-009, FR-013): the sensitive-data filter now runs
			// INSIDE the choke point, on the full content, before the cap —
			// so a secret straddling the head or tail cut is redacted whole
			// in both the archive and the window (B-16). The choke point
			// also persists the archive line itself (the mark cites that
			// line), so the AddFullMessage this site used to do is gone.
			// Media refs are resolved on a scratch message first so both
			// the archived and the window form carry them.
			var mediaMsg providers.Message
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
				attachToolResultMedia(&mediaMsg, toolResult.Media, turnMediaStore, maxMediaSize)
			}
			// ADR-066 D5.4 (FR-041/FR-042): budget-first recall decision,
			// BEFORE the choke point so the archive, transcript and events
			// carry the truthful outcome — the tool's "now in your context"
			// receipt only when the span will be spliced below, the non-fit
			// message otherwise. A no-op for every other tool.
			recallDecision := al.decideRecallInjection(ts, tc.Name, messages, contentForLLM)
			contentForLLM = recallDecision.content
			admitted := al.admitToolResult(ts, toolResultAdmission{
				Tool:       tc.Name,
				ToolCallID: toolCallID,
				Content:    contentForLLM,
				Media:      mediaMsg.Media,
				IsError:    toolResult.IsError,
				ParallelN:  len(normalizedToolCalls),
			})
			// contentForLLM from here on is the FILTERED full content the
			// archive holds — what the event sinks and the transcript error
			// field always carried (the gateway tool_results/ store keeps it
			// for Verbose chat); the window form is toolResultMsg.
			contentForLLM = admitted.Archived.Content
			toolResultMsg := admitted.Message
			endSID, endProducingSID := u9ToolExecSessionIDs(ts)
			al.emitEvent(
				EventKindToolExecEnd,
				ts.eventMeta("runTurn", "turn.tool.end"),
				ToolExecEndPayload{
					ToolCallID: session.ToolCallID(toolCallID),
					ChatID:     ts.chatID,
					// ADR-057 FR-011/FR-012/FR-013 (W4/W5d, U9): see the
					// matching ToolExecStartPayload construction above —
					// identical contract on the result frame.
					SessionID:          endSID,
					Tool:               toolName,
					Duration:           toolDuration,
					ForLLMLen:          len(contentForLLM),
					ForUserLen:         len(toolResult.ForUser),
					IsError:            toolResult.IsError,
					Async:              toolResult.Async,
					Result:             contentForLLM,
					ParentSpawnCallID:  session.ToolCallID(ts.parentSpawnCallID),
					AgentID:            ts.resolveActiveAgentID(), // Bug 1: runtime-current agent
					ProducingSessionID: endProducingSID,
				},
			)
			tcStatus := "success"
			switch {
			case toolResult.ParksTurn:
				// ADR-057 UAT defect C2 fix (2026-08-04): a SYNCHRONOUS
				// delegate/spawn call whose child sub-turn parked awaiting
				// the parent's answer (message_parent(kind="question",
				// wait=true) — see pkg/agent/subturn.go's spawnSubTurn,
				// the `if turnRes.status == TurnEndStatusParked` branch
				// that sets ToolResult.ParksTurn, the single source of
				// truth for this signal). Without this case, a parked
				// child's toolResult here has Interrupted==false and
				// IsError==false (it is neither a failure nor a
				// cancellation), so tcStatus fell through to the
				// "success" initializer — persisting the OUTER delegate
				// tool call's own tc.Status as "success" even though the
				// live subagent_end WS frame (spawnSubTurn's endStatus
				// switch, now SubTurnStatusParked) already correctly said
				// "parked". That divergence meant a SESSION RELOAD
				// (pkg/gateway/replay.go's resolveStatus(tc.Status), used
				// to reconstruct the subagent_end frame from this exact
				// persisted record) would show "success" for a
				// synchronously-dispatched parked child even after the
				// live-render half of this fix, exactly the class of
				// live/reload-parity bug the surrounding tcStatus switch
				// already exists to close for "interrupted" below.
				// Checked FIRST (highest priority), mirroring this same
				// loop's `parked := toolResult.ParksTurn` priority check
				// (below, in the tool-execution loop) — a park must win
				// over the (mutually exclusive, by construction) Interrupted/
				// IsError cases.
				tcStatus = "parked"
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
			// RC-5 (ADR-057 UAT root-cause fix): for every OTHER failed tool
			// call — bash, write_file, async delegate, anything not covered by
			// the two Result-populating branches above — persist the same
			// human-readable reason already sent to the LLM (contentForLLM,
			// computed above) so the durable transcript is never left with a
			// null result and no explanation. Gated on tcRecord.Result == nil
			// so a call that already carries a richer Result (media
			// descriptors, or buildSyncDelegateResult's {"text":…,"error":true}
			// shape) is not given a redundant, differently-shaped Error too.
			//
			// No new data exposure: contentForLLM at this point has already
			// passed through the SEC-25 prompt-guard sanitizer (untrusted
			// tools only) and — CONDITIONALLY, only when
			// cfg.Tools.IsFilterSensitiveDataEnabled() is true — cfg.
			// FilterSensitiveData above (see the `if cfg.Tools.
			// IsFilterSensitiveDataEnabled()` gate a few lines up); when that
			// setting is off, contentForLLM here is unfiltered, matching what
			// was actually sent to the LLM as the tool-result message and
			// already logged via ToolExecEndPayload.Result a few lines up
			// (Duration/IsError block) — this call is not introducing any
			// exposure beyond what those two sinks already have. Truncated via
			// truncateRunes (reusing task_completion_signal.go's existing
			// rune-safe truncation convention, not inventing a new one) to
			// bound transcript growth from a single pathological tool error.
			//
			// Not necessarily redundant with the in-memory session history:
			// ts.agent.Sessions.AddFullMessage below (the tool-result message
			// history write) is gated on `!ts.opts.NoHistory`, but
			// appendToolCallTranscript (which persists tcRecord, including
			// this Error field) is not — it only requires a wired
			// transcriptStore/transcriptSessionID (turn.go's
			// appendToolCallTranscript). So on a NoHistory turn (e.g. a
			// delegated sub-turn's ephemeral history — see subturn.go), this
			// durable transcript write is the ONLY copy of the failure reason
			// that survives the turn at all.
			if toolResult.IsError && tcRecord.Result == nil {
				tcRecord.Error = truncateRunes(contentForLLM, maxFailClosedOutputChars)
			}
			// ADR-066 FR-046: the transcript tool_call entry carries the
			// BOUNDED result the model saw (the window form) so D5.5
			// hydration (T066-06) rebuilds a window that is not lossy, plus
			// the projection state for the SPA's content_state. Only when
			// nothing richer is there already (media descriptors, the sync
			// delegate shape, or the failure Error text).
			if tcRecord.Result == nil && tcRecord.Error == "" {
				if text := strings.TrimSpace(toolResultMsg.Content); text != "" {
					tcRecord.Result = map[string]any{"text": text}
				}
			}
			if admitted.Capped {
				// Always the plain "capped": the SPA-facing content_state
				// enum (ToolCall.yaml) is full | capped | emptied and does
				// NOT distinguish the D4 surface. The internal state also
				// records which cap produced the live bytes
				// (memory.ProjectionCappedFailure) — that value must never
				// be written here, it is not on the wire.
				tcRecord.ContentState = string(memory.ProjectionCapped)
			}
			ts.appendToolCallTranscript(tcRecord)
			messages = append(messages, toolResultMsg)
			// ADR-066 D5.4 (FR-041): the recalled text joins the in-memory
			// slice HERE — the same mutation point every mid-turn request
			// is built from — so the provider's next call carries it.
			if recallDecision.inject {
				messages = al.spliceRecallSpan(ts, messages, recallDecision.span)
			}
			// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
			// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
			// ends the turn typed with no further provider call (FR-032).
			if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
				res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
				turnStatus = status
				return res, exitErr
			}

			if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
				pendingMessages = append(pendingMessages, steerMsgs...)
			}

			// C2 (ADR-057 UAT 2026-08-03): a successful message_parent(kind=
			// question, wait=true) call parks the CALLING child's own durable
			// LifecycleRecord in needs_input (pkg/tools/message_parent.go's
			// parkNeedsInput) — but until this check existed, this in-memory
			// loop was completely blind to that transition and kept iterating,
			// eventually overwriting the durable park with a later terminal
			// state before any `delegate respond` could ever reach it (the
			// child "kept running" past its own park, permanently stranding
			// the correlation_id). toolResult.ParksTurn is the signal
			// message_parent.go sets on exactly that success path; checked
			// FIRST (highest priority) because a park must win over an
			// in-flight steering message or graceful interrupt too.
			parked := toolResult.ParksTurn

			// ADR-088 FR-010: the question door was genuinely taken on a
			// narrowed goal turn — bump the persisted per-generation
			// question-round budget. Scoped tightly: only THIS exact tool
			// (never any other ParksTurn tool, e.g. a nested delegate's
			// parked child), only when the narrowed pair actually offered
			// the ask door THIS request (goalForce.layer1 &&
			// goalForce.askOffered — a stray AskUserQuestion call on some
			// unrelated turn must never consume a goal's budget it has no
			// relation to), and only on the genuine success path
			// (parked==true — a refused/errored ask attempt asked nothing
			// and must not spend the round, spec S-14/E6).
			if parked && goalForce.layer1 && goalForce.askOffered && toolName == tools.AskUserQuestionToolName {
				al.bumpGoalQuestionRoundsUsed(goalForce)
			}

			skipReason := ""
			skipMessage := ""
			if parked {
				skipReason = "session parked (message_parent question wait=true)"
				skipMessage = "Skipped: this session parked awaiting the parent's answer."
			} else if len(pendingMessages) > 0 {
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
						// ADR-066 D4: the synthetic skipped result is a builtin-failure
						// surface result like any denial (FR-009).
						skippedMsg := al.admitToolResult(ts, toolResultAdmission{
							Tool: skippedTC.Name, ToolCallID: skippedTC.ID, Content: skipMessage, IsError: true, ParallelN: len(normalizedToolCalls),
						}).Message
						messages = append(messages, skippedMsg)
					}
				}
				if parked {
					// Stop the turn NOW — modeled on the hardAbortRequested
					// early-return above (this same loop), not on the
					// graceful-interrupt `break` below: `break` only exits
					// THIS tool-execution loop and falls through to another
					// LLM call at the top of the iteration loop (turnLoop),
					// which is exactly the bug (the loop resuming past the
					// park). A genuine `return` here is what actually stops
					// runTurn. Unlike abortTurn, this deliberately does NOT
					// call ts.restoreSession — a park is not a rollback: the
					// history through this tool call's own recorded result
					// must survive on disk exactly as-is so a later `delegate
					// respond` resumes from this point, not from a rewound
					// pre-turn snapshot.
					ts.setPhase(TurnPhaseParked)
					turnStatus = TurnEndStatusParked
					return turnResult{
						status:     TurnEndStatusParked,
						followUps:  append([]bus.InboundMessage(nil), ts.followUps...),
						turnFailed: ts.turnFailed,
					}, nil
				}
				// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
				// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
				// ends the turn typed with no further provider call (FR-032).
				if messages, midTurnGuardErr = al.midTurnWindowCheck(ts, messages, providerToolDefs); midTurnGuardErr != nil {
					res, status, exitErr := al.typedTurnExit(ts, iteration, llmModel, midTurnGuardErr)
					turnStatus = status
					return res, exitErr
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

		// Identical SUCCESSFUL repetition reached toolRepeatStopThreshold this
		// round (tool_failure_circuit_breaker.go). Every tool result of the
		// round is already recorded, so the history stays well-formed; end the
		// turn through the same finalization path the iteration cap uses, with
		// a visible final message instead of another provider round. A queued
		// user message is new input: let it through and restart the count.
		if notice := ts.takeToolRepeatStop(); notice != "" {
			if len(pendingMessages) > 0 {
				ts.resetToolSuccessRepeat()
			} else {
				logger.WarnCF("agent", "Turn stopped: an identical tool call kept succeeding without progress",
					map[string]any{
						"agent_id":  ts.agent.ID,
						"turn_id":   ts.turnID,
						"iteration": iteration,
						"threshold": toolRepeatStopThreshold,
					})
				finalContent = notice
				ts.markTurnFailed()
				break turnLoop
			}
		}
	}

	// ADR-071 §4.3.1(a): advance and sweep this bucket's search-promotion
	// horizon exactly once per REAL conversational turn, not once per
	// turnLoop round-trip. This deliberately sits OUTSIDE (after) the
	// turnLoop for-loop above, unlike the (unrelated) MCP discovery TTL tick
	// it used to sit next to: `iteration`, incremented once per pass through
	// that loop, counts LLM-call rounds within a single turn — a turn that
	// makes several sequential tool calls before its final response can pass
	// through the loop body, and therefore the old in-loop call site, many
	// times before the user ever sees a reply. With
	// searchPromotionHorizonTurns = 5 that could silently expire a
	// ToolSearch promotion mid-turn, even though the field's own doc comment
	// says it counts "across the whole conversation" (turns, not rounds).
	//
	// This site fires once per natural exit of the turnLoop for-loop, which
	// is once per real conversational turn in the overwhelmingly common
	// case. The one nuance: late-arriving steering messages `goto turnLoop`
	// below to continue THIS SAME turn rather than starting a new one — each
	// such continuation is itself a further round of natural back-to-back
	// tool-calling activity on the same turn, so ticking again when it in
	// turn naturally exits is consistent with "count real conversational
	// turns" rather than "count LLM-call rounds," not a double-count of one
	// turn. A turn that instead exits via an early return above (hard abort,
	// delegate park) never reaches this line, so it does not tick at all —
	// deliberate: neither is a completed conversational round from the
	// user's perspective, and a parked turn is expected to resume later
	// rather than count as elapsed time against the horizon.
	al.tickSearchPromotionHorizon(ts.manifestBucket())

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
		if ts.getTruncationReason() != "" {
			// ADR-087 D4a: this IS the deliberate outcome, not a fallthrough
			// — a truncated turn that produced literally no content. No
			// retry, no fallback substitution, no markTurnFailed; finalContent
			// stays "" and the write choke points below persist a zero-content
			// entry for MarkLastEntryTruncated to find.
		} else if ts.currentIteration() >= ts.agent.MaxIterations && ts.agent.MaxIterations > 0 {
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
					SessionID: string(ts.routingSessionID),
				},
			)
			// US-1: persist the session-save failure to the JSONL
			// transcript so the replay path re-renders it after reload (see
			// appendErrorTranscript docstring).
			ts.appendClassifiedError(EventKindError.String(), "runTurn", saveLLM)
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
	truncReason := ts.getTruncationReason()
	if !hasActiveStreamer {
		// ADR-087 D4a/D4b: this is the non-streaming write choke point.
		// When the turn was truncated, stamp Truncated/TruncationReason in
		// the SAME write as the content — see
		// appendAssistantTranscriptTruncated's doc comment for why this
		// replaces the former append-then-MarkLastEntryTruncated two-step
		// (for the streaming case, finalizeStreamer's own deferred call
		// does the single-write equivalent after wsStreamer.Finalize
		// persists its entry).
		if truncReason != "" {
			ts.appendAssistantTranscriptTruncated(finalContent, truncReason)
		} else if finalContent != "" {
			ts.appendAssistantTranscript(finalContent)
		}
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
// Every typed exit produces the three SC-006 artefacts:
//   - one log line carrying the typed code AND the raw cause (operator triage),
//   - one EventKindError carrying the typed code (live wire; the deferred
//     EventKindTurnEnd in runTurn fires on return with the status returned
//     here — TurnEndStatusAborted for a cancel, which is an intentional user
//     action and must not mark the turn failed; TurnEndStatusError for a
//     timeout),
//   - one transcript entry with the typed code (replay).
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

	al.emitTurnErrorFrame(ts, ts.eventMeta("runTurn", "turn.error"), "llm", "runTurn", llm)
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
// ADR-057 FR-014: this is a WS-payload-stamping consumer of routingSessionID.
// The frame's SessionID is ts.routingSessionID — the session a second tab or a
// reload is attached to; a webchat ChatID alone is a dead per-connection id —
// and the value never leaves this function. Two exits share it: typedTurnExit
// (a turn cancelled, timed out, or out of context) and subturn.go's
// subTurnTimedOutResult (a delegation force-cancelled at its time limit, the
// exit a timed-out child took through typedTurnExit before the force-cancel
// existed). Code that needs the id for any other purpose must read the field
// itself and justify that read against the consumer-set test
// (routing_session_id_consumer_set_adr057_test.go).
func (al *AgentLoop) emitTurnErrorFrame(
	ts *turnState, meta EventMeta, payloadStage, transcriptStage string, llm LLMError,
) {
	al.emitEvent(EventKindError, meta, ErrorPayload{
		Stage:     payloadStage,
		ChatID:    ts.opts.ChatID,
		Code:      string(llm.Code),
		Message:   llm.Message,
		SessionID: string(ts.routingSessionID),
	})
	ts.appendClassifiedError(EventKindError.String(), transcriptStage, llm)
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
