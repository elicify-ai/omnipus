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
	"github.com/elicify-ai/omnipus/pkg/agentstore"
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
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
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
	recallSpans        sync.Map     // key: sessionKey (string), value: *RecallSpan
	activeTurnStates   sync.Map     // key: sessionKey (string), value: *turnState
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
	// TokenBudget is the sole app-level spend brake; see pkg/agent/budget.go (D12 / R§8.3).
	rateLimiter *security.RateLimiterRegistry

	// tokenBudget is the ADR-053 Phase-2 / D12 app-level OVERALL token budget
	// (R§8.3): ONE atomic pool debited by ALL workloads (owner/member/verifier/
	// Judge) from provider-reported usage, deliberately NOT honoring
	// IsPrivilegedAgent (FR-172). Default cap 0 = unbounded (FR-175). The
	// ceiling is restart-gated (FR-177). Always non-nil after NewAgentLoop so
	// the debit path is a unconditional no-op when unbounded. The persisted
	// consumed counter is reconciled at boot from system/token_budget.json.
	tokenBudget *TokenBudget

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

// userInitiated returns msg's fail-closed origin signal (ADR-049 Gap #8/r2,
// R6) for threading onto processOptions.UserInitiated. It reads ONLY
// bus.InboundMessage.UserInitiated — set true exclusively by the gateway
// webchat WS `message` handler and by channel adapters' HandleMessage (a
// real platform sender). Every other producer of an InboundMessage
// (async-notifier, followUps re-publish, ProcessDirect/ProcessDirectWithChannel)
// leaves the field at its zero value, so this returns false for them by
// construction — mirroring gatewayPrincipal's "read the one dedicated
// carrier, never infer" discipline above.
func userInitiated(msg bus.InboundMessage) bool {
	return msg.UserInitiated
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

// ErrReloadAlreadyInProgress is returned by TriggerReload when a reload
// function reports that a reload is already running. The caller should treat
// this as "poll anyway" — that reload will call ClearReloadPending when it
// completes, unblocking any poller.
//
// NOT REACHABLE FROM PRODUCTION. The only reloadFunc production installs is the
// gateway's coalescing trigger (pkg/gateway.newReloadTrigger), which never
// reports contention as an error: a request arriving mid-reload is recorded and
// served by a follow-up reload, so it returns nil. This sentinel and the branch
// that produces it survive as a defensive net for any other reloadFunc (and are
// exercised by pkg/gateway's rest_auth_test.go, which installs one deliberately)
// — do not read them as describing live gateway behaviour.
var ErrReloadAlreadyInProgress = errors.New("reload already in progress")

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
	if _, err := ResolveRootDelegationCap(cfg); err != nil {
		logger.ErrorCF("agent",
			"agents.defaults.subturn.max_concurrent is configured to a negative value — the root-delegation admission gate falls back to the central Performance.EffectiveMaxParallelAgents() authority; set it to 0 (inherit the central value) or a positive explicit override",
			map[string]any{"error": err.Error()})
	}

	eventBus := NewEventBus()
	al := &AgentLoop{
		bus:                     msgBus,
		cfg:                     cfg,
		registry:                registry,
		state:                   stateManager,
		eventBus:                eventBus,
		fallback:                fallbackChain,
		cmdRegistry:             commands.NewRegistry(commands.BuiltinDefinitions()),
		steering:                newSteeringQueue(parseSteeringMode(cfg.Agents.Defaults.SteeringMode)),
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
	al.admission = newAdmissionControllerWithResolver(func() int {
		n, _ := al.GetConfig().Performance.EffectiveMaxParallelAgents()
		return n
	})
	// ADR-057 W17, same live-resolution treatment: root-level delegate()
	// fan-out must never drift from the central authority either. On
	// ResolveRootDelegationCap's error branch (a NEGATIVE configured value)
	// this falls back directly to EffectiveMaxParallelAgents() so the gate
	// stays GATED at the central value rather than degrading to unlimited.
	al.rootDelegationAdmission = newRootDelegationAdmissionWithResolver(func() int {
		liveCfg := al.GetConfig()
		if resolvedCap, capErr := ResolveRootDelegationCap(liveCfg); capErr == nil {
			return resolvedCap
		}
		if liveCfg != nil {
			n, _ := liveCfg.Performance.EffectiveMaxParallelAgents()
			return n
		}
		return 1
	})
	al.hooks = NewHookManager(eventBus)
	configureHookManagerFromConfig(al.hooks, cfg)

	// Initialize the unified task store at ~/.omnipus/tasks/ (the single store —
	// the legacy GTD tasks/ and workflow-tasks/ split was removed in Sprint 2).
	homePath := filepath.Dir(cfg.AgentHomeBasePath())
	al.homePath = homePath
	al.taskStore = task.New(filepath.Join(homePath, "tasks"))
	al.taskExecutor = newTaskExecutor(al, al.taskStore)

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
			sharedStore.SetStatsFlushInterval(cfg.Session.EffectiveStatsFlushInterval())
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

	// Initialize the pre-registration cancel latch table (cancel_prearm.go).
	al.cancelPreArm = newCancelPreArm()

	// SEC-26: Initialize rate limiter registry. The registry always exists
	// so per-agent windows can be created even when no limit is configured.
	// TokenBudget is the sole app-level spend brake; see pkg/agent/budget.go (D12 / R§8.3).
	al.rateLimiter = security.NewRateLimiterRegistry()

	// ADR-053 Phase-2 / D12 (R§8.3): app-level OVERALL token budget — the
	// sole app-level spend brake. The ceiling is restart-gated (FR-177) —
	// read ONCE at boot from PlanningConfig and never live-reloaded; the
	// live spend lever is Stop/cancel. The persister reconciles the
	// consumed counter across restarts. cap 0 = unbounded (FR-175).
	tbPath := filepath.Join(homePath, "system", "token_budget.json")
	al.tokenBudget = NewTokenBudget(cfg.Planning.EffectiveTokenBudget(), NewTokenBudgetPersister(tbPath))
	logger.InfoCF("agent", "Rate limiter initialized",
		map[string]any{
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
		s, ok := v.(string)
		if !ok {
			logger.ErrorCF("agent", "agentCurrentSession: invariant violated — unexpected value type",
				map[string]any{"agent_id": agentID, "got_type": fmt.Sprintf("%T", v)})
			return "", false
		}
		return s, true
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
		cancel, ok := v.(context.CancelFunc)
		if !ok {
			logger.ErrorCF("agent", "idleTickers: invariant violated — unexpected value type",
				map[string]any{"session_id": sessionID, "got_type": fmt.Sprintf("%T", v)})
			return
		}
		cancel()
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

// TokenBudget returns the ADR-053 Phase-2 app-level OVERALL token budget
// (D12/R§8.3). Always non-nil after NewAgentLoop (a nil AgentLoop returns
// nil). Used by the goal loop's graceful-wind-down brake (checkGoalLoopAfterTurn)
// and by gateway Usage handlers that report spend / set the restart-gated ceiling.
func (al *AgentLoop) TokenBudget() *TokenBudget {
	if al == nil {
		return nil
	}
	return al.tokenBudget
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
	// Read al.cfg under al.mu.RLock (GetConfig), NOT bare. This helper runs
	// inside UpsertAgentFast's and ReloadProviderAndConfig's wiring pass with
	// NO al.mu held, so a bare `al.cfg` read races every pointer-swap publisher
	// (SwapConfig, ReloadProviderAndConfig, and MutateConfig's copy-then-swap)
	// writing the al.cfg slot under al.mu.Lock. The locked read establishes the
	// happens-before edge the bare read lacked.
	cfg := al.GetConfig()
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
		agent.Tools.RegisterReplacing(execTool)
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
	// Read al.cfg under al.mu.RLock (GetConfig), NOT bare — see the matching
	// comment in wireExecToolDepsOn: this helper likewise runs in the unlocked
	// wiring pass of UpsertAgentFast/ReloadProviderAndConfig, and a bare al.cfg
	// read races every pointer-swap publisher of al.cfg.
	cfg := al.GetConfig()
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
			ag.Tools.RegisterReplacing(webServeTool)
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

	// FR-026b. Browser managers are per BROWSING KEY, and N agents commonly
	// share one workspace — so the per-agent loop below must do the
	// create/Release/Shutdown cycle exactly ONCE per key, not once per agent.
	// Without this set, five agents on one workspace would tear down and
	// rebuild the same browser five times on every Settings save, and the
	// fifth pass would Release a manager the fourth had just installed.
	seenBrowserKeys := make(map[string]bool)
	liveBrowserKeys := make(map[string]bool)

	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok {
			continue
		}

		// Web search tool — always registered; policy decides invocation.
		// Per-provider Enabled sub-flags (Brave, Tavily, etc.) are retained because
		// they select which upstream API is used, not whether the tool exists.
		searchTool, err := tools.NewWebSearchTool(tools.WebSearchToolOptions{
			IngestBoundBytes:      cfg.Context.IngestBoundBytes, // ADR-066 D10
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
			agent.Tools.RegisterReplacing(searchTool)
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
			agent.Tools.RegisterReplacing(fetchTool)
		}

		// Message tool — outbound inter-agent message via bus.
		messageTool := tools.NewMessageTool()
		messageTool.SetSendCallback(func(channel, chatID, content string, origin tools.SendOrigin) error {
			pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer pubCancel()
			// Origin travels with the message (ADR-065 spec FR-6) so a send can
			// be attributed, and so dispatch can re-check ownership at the last
			// common point before the wire. System-originated publishes leave
			// these empty and are exempt by that emptiness.
			return msgBus.PublishOutbound(pubCtx, bus.OutboundMessage{
				Channel:          channel,
				ChatID:           chatID,
				Content:          content,
				AgentID:          origin.AgentID,
				WorkspaceID:      origin.WorkspaceID,
				OwnershipChecked: origin.OwnershipChecked,
			})
		})
		// Re-apply the stored resolver: this runs on every reload, and the
		// MessageTool above is brand new each time (ADR-065).
		if own := al.ChannelOwnership(); own != nil {
			messageTool.SetChannelOwnership(own)
		}
		// RegisterReplacing, not Register: #278 hardened Register to KEEP the
		// incumbent and DISCARD a same-name newcomer. Since ADR-065 rebuilds
		// messageTool on every reload, plain Register would silently throw the
		// fresh instance away and leave the stale ChannelOwnership resolver
		// live — the same defect that made browser tool re-wiring drop the
		// operator's current security state. See
		// docs/internal/false-green-patterns.md §5.
		agent.Tools.RegisterReplacing(messageTool)

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
			// Record the tool's own toDefault intent, keyed the same way
			// GetSessionActiveAgent is (evt.SessionID, "session:" prefix) so
			// the WS agent_switched frame builder can read it back via the
			// exact evtSID it already uses to look up the active agent.
			if evt.SessionID != "" {
				al.lastSwitchToDefault.Store("session:"+evt.SessionID, evt.ToDefault)
			}
		}
		// The handoff target's window is the one its own instance resolved
		// through the ADR-066 D2 ladder (its provider, its model, its
		// override) — never a config default. An unknown target, an exempt
		// one or an unknown window yields 0: the handoff then transfers no
		// recent context and the summary line names what was left out.
		getContextWindow := func(targetAgentID string) int {
			liveRegistry := al.GetRegistry()
			if liveRegistry == nil {
				return 0
			}
			target, ok := liveRegistry.GetAgent(targetAgentID)
			if !ok || target == nil {
				return 0
			}
			window, _, _ := target.windowSnapshot()
			return window
		}
		getDefaultAgent := func() string {
			currentCfg := al.GetConfig()
			if currentCfg.Agents.Defaults.DefaultAgentID != "" {
				return currentCfg.Agents.Defaults.DefaultAgentID
			}
			// No configured override — fall through to the registry's own
			// resolution ladder (lexicographically-first non-worker agent)
			// rather than a hardcoded name; SwitchAgentTool.Execute's
			// target:"default" branch already handles an empty result as
			// "no default agent configured" rather than silently switching
			// to a name that doesn't exist.
			// liveRegistry, not the `registry` parameter this closure could
			// capture: that one is the boot-time instance, and a full registry
			// rebuild (TriggerReload, e.g. after the default agent changes)
			// REPLACES al.registry. This closure runs long after construction,
			// so reading the captured parameter would resolve the default
			// against a stale roster. The name difference is deliberate — it
			// used to shadow, which read as an accident rather than intent.
			if liveRegistry := al.GetRegistry(); liveRegistry != nil {
				if def := liveRegistry.GetDefaultAgent(); def != nil {
					return def.ID
				}
			}
			return ""
		}
		// sharedStore is the shared session store; tools handle a nil store by
		// skipping transcript ops (nil only occurs in tests without a store).
		sharedStore := al.GetSessionStore()
		agent.Tools.RegisterReplacing(tools.NewSwitchAgentTool(getRegistryReader, sharedStore, getContextWindow, getDefaultAgent, onHandoffFrontend))

		// Send file tool (outbound media via MediaStore — store injected later by SetMediaStore).
		sendFileTool := tools.NewSendFileTool(
			agent.Home,
			cfg.Agents.Defaults.RestrictToWorkspace,
			cfg.Agents.Defaults.GetMaxMediaSize(),
			nil,
			allowReadPaths,
		)
		agent.Tools.RegisterReplacing(sendFileTool)

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
			agent.Tools.RegisterReplacing(tools.NewFindSkillsTool(registryMgr, searchCache))
			// ADR-046 FR-009: install_skill targets the fixed, install-wide
			// GLOBAL skills directory ($OMNIPUS_HOME/skills) — the SAME
			// directory every agent's own SkillsLoader searches (see
			// globalSkillsDir's doc comment in context.go) — never
			// agent.Home, so a skill installed by one agent is discoverable
			// by every other agent.
			agent.Tools.RegisterReplacing(tools.NewInstallSkillTool(registryMgr, globalSkillsDir()))
			// remove_skill is NOT registered here: it is a ScopeCore
			// management tool (systools.SkillRemoveTool, "remove_skill"),
			// wired onto every agent's Tools registry by WireSysagentDeps
			// (pkg/gateway/gateway.go), which shares its SkillInstaller/
			// SkillsLoader with this same skill engine. A prior version of
			// this block registered a second, competing ScopeGeneral
			// implementation here, constructed against the agent's own
			// per-agent workspace root (the field ADR-057 FR-001/FR-002
			// renamed to .Home) — a root that predates ADR-046 FR-009's move
			// to a single global skills directory and that install_skill
			// above no longer targets. Do not reintroduce it.
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
			// ADR-057 W17 (FR-069/FR-070/FR-095): wrap the real spawner with
			// the root-delegation admission gate (admission.go) so a
			// ROOT-level `delegate` fan-out from this agent is actually
			// capped by al.rootDelegationAdmission — the SAME shared,
			// process-wide instance every other agent's DelegateTool is
			// wrapped with, so the cap applies once across the whole running
			// gateway, not per agent. See rootDelegationAdmittingSpawner's
			// doc comment (admission.go) for why wrapping SpawnSubTurn here
			// is the correct choke point for both sync and async delegation.
			delegateTool.SetSpawner(newRootDelegationAdmittingSpawner(NewSubTurnSpawner(al), al.rootDelegationAdmission, agentID))
			// Retain it so Close() can drain its background delegations before
			// the stores they write through are torn down. See delegateTools.
			al.delegateToolsMu.Lock()
			al.delegateTools = append(al.delegateTools, delegateTool)
			al.delegateToolsMu.Unlock()
			// FR-196 kill switch — wire it HERE, at construction, not only in
			// SetSessionMessagingStores' later re-wire. This is a PER-AGENT
			// DelegateTool: the session_messaging_wire.go re-wire walks the
			// shared registry, so an agent-scoped instance built here would
			// otherwise never be wired at all. An unwired tool fails CLOSED
			// (delegate.go sessionMessagingPlaneEnabled), which would deny the
			// whole gated action set — cancel/steer/respond/inbox/inbox_ack/
			// follow_up/peek — for every agent. The closure re-reads config per
			// call, so a live kill-switch flip is still honored.
			delegateTool.SetSessionMessagingEnabled(al.sessionMessagingEnabledLive())
			// R2-MAJ-015 — the operator kill switch for delegate's FR-015
			// fail-closed parent-agent-id guard
			// (tools.delegate.require_parent_agent_id). Same live-closure
			// discipline as the FR-196 switch immediately above, and for a
			// sharper reason: this guard's failure mode is "delegation stops
			// entirely across the install", so the escape hatch is worthless
			// if escaping it needs a restart. al.GetConfig() is re-read per
			// call rather than captured here — an eagerly-read value would
			// freeze at whatever this wiring pass saw, which for a dependency
			// the gateway assigns AFTER tool wiring means frozen at nil while
			// registration still looks correct.
			delegateTool.SetRequireParentAgentID(func() bool {
				return al.GetConfig().Tools.Delegate.EffectiveRequireParentAgentID()
			})
			// W2: action:"status" live-progress snapshot for a running native
			// task. sharedStore mirrors the exact store wiring the
			// tools.NewSwitchAgentTool(...) call above already uses — the same
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
			// NewSwitchAgentTool wiring above shares this pre-existing latent
			// pattern; tracked separately.)
			if sharedStore != nil {
				delegateTool.SetSessionStore(sharedStore)
			}
			// G1 fix: wire the live tool-call-argument progress reader so
			// action:"status" can tell "still generating a large tool-call
			// argument" apart from "hung" for a running native child — see
			// tools.DelegateProgressReader's doc comment. al itself
			// implements the interface (AgentLoop.ProgressForSession,
			// turn.go), reading straight from al.activeTurnStates — the
			// same live-turn registry GetActiveTurnHookForSession/
			// claimAnyTurnForSession already use for cancellation — so no
			// typed-nil guard is needed here (unlike sharedStore above):
			// al is the *AgentLoop this tool is being registered on, never
			// nil at this point in construction.
			delegateTool.SetProgressReader(al)
			delegateTool.SetAgentRegistry(func() tools.DelegateAgentRegistry { return al.GetRegistry() })
			// FR-028/BDD-29 (ADR-057 U14): wire the shared, process-wide
			// SessionManager so `delegate action="cancel"` actually kills the
			// TARGET child's own background bash/exec shells, not just its
			// turn. Without this, killChildBackgroundShells (delegate.go)
			// starts with `if t.sessionManager == nil { return }` — always
			// taken — and a cancelled delegate's background dev server (or
			// any other backgrounded shell) is silently orphaned holding its
			// port. tools.GetSharedSessionManager() is the SAME process-wide
			// singleton ExecTool/bash register their background sessions
			// with (session.go/session_manager_export.go), so this ties the
			// cancel path to the actual live session registry rather than a
			// fresh, empty one.
			delegateTool.SetSessionManager(tools.GetSharedSessionManager())
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
					agentExistsChecker(registry),
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
				buildDelegationDenyCheckerForDelegate(
					currentAgentID, cfg.Agents.Defaults, config.DelegationModeAwait, agentExistsChecker(registry),
				),
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

			// ADR-057: derive the ownership-walk bound from the SAME operator
			// setting that bounds delegation depth. Left unwired, the walk used
			// a hardcoded 3 while delegation depth stayed configurable — so
			// raising max_depth made cancel/steer/peek on a legitimate depth-4+
			// child fail with an ownership error indistinguishable from a real
			// cross-tenant attempt. Zero/unset is ignored by the setter, which
			// keeps its own default.
			delegateTool.SetOwnershipWalkMaxDepth(cfg.Agents.Defaults.SubTurn.MaxDepth)

			agent.Tools.RegisterReplacing(delegateTool)
		}

		// ADR-053 Phase 2 on-ramp (session_messaging_wire.go): wire the S2/S3
		// session-control hooks onto THIS agent's delegate + message_parent
		// tools. On the FIRST registerSharedTools pass (inside NewAgentLoop,
		// before the gateway constructs the stores) the stores are nil → both
		// tools register fail-closed (Execute returns "not configured"). The
		// gateway's later SetSessionMessagingStores call re-runs this wiring
		// with the real stores, mirroring SetPlanStore's late-binding
		// discipline exactly. Safe on hot-reload (idempotent re-wire).
		al.wireSessionMessagingForAgent(agent)

		// Task tools — require a task store (available after first NewAgentLoop call).
		if al.taskStore != nil {
			currentAgentID := agentID

			agent.Tools.RegisterReplacing(tools.NewTaskListTool(al.taskStore))

			taskCreate := tools.NewTaskCreateTool(al.taskStore)
			// Resolve the real default workspace (is_default ULID) when a
			// chat-delegated task has no workspace bound to the turn — never the
			// literal "default" (which would land it in an invisible workspace).
			taskCreate.SetHome(filepath.Dir(cfg.AgentHomeBasePath()))
			// ADR-052 FR-002: wire the plan store so the optional plan_id
			// linkage arg can be validated (validateTaskPlanLinkage,
			// pkg/tools/plan.go) instead of failing closed with "plan store is
			// not configured" on every call. al.GetPlanStore() may still be nil
			// on this very first registerSharedTools pass (it runs inside
			// NewAgentLoop, before the gateway's setupAndStartServices
			// constructs the real plan.Store) — that is fine, SetPlanStore's
			// per-agent loop below re-wires this tool with the real store once
			// it exists, exactly like wirePlanToolsForAgent's own create_plan/
			// execute_plan late-binding discipline.
			taskCreate.SetPlanStore(al.GetPlanStore())
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
					agentExistsChecker(registry),
				),
			)
			// Task-mode recursion bound: reject a task_create issued from within a
			// task run whose delegation generation already sits at the ceiling. The
			// per-agent depth gate cannot bound task mode on its own because every
			// task run starts a fresh turn at depth 0 (see processTaskDirect depth
			// seeding); this hard ceiling closes that gap.
			taskCreate.SetMaxDelegationDepth(maxTaskDepth)
			// D2 rule 5 (FR-017/052, review r1 major M5): reject an all-check
			// criteria create outright when the assignee's effective bash
			// policy is deny or ask — structurally unsatisfiable, mirrors
			// judge.go's runMachineCheck policy resolution exactly (same
			// registry, same EffectiveToolPolicy call, ScopeCore).
			taskCreate.SetBashPolicyChecker(func(assigneeAgentID string) (policy string, ok bool) {
				agentInst, found := al.GetRegistry().GetAgent(assigneeAgentID)
				if !found || agentInst == nil {
					return "", false
				}
				return tools.EffectiveToolPolicy(agentInst.LoadToolPolicy(), tools.ScopeCore, agentInst.AgentType, "bash"), true
			})
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
			agent.Tools.RegisterReplacing(taskCreate)

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
					agentExistsChecker(registry),
				),
			)
			// Same rationale as taskCreate above: the subagent_3p reassignment
			// guard is retired now that processTaskDirect dispatches an
			// external-CLI worker's task run through runExternalCLISubTurn.
			agent.Tools.RegisterReplacing(taskUpdate)

			setTodos := tools.NewSetTodosTool(al.taskStore)
			setTodos.SetHome(filepath.Dir(cfg.AgentHomeBasePath()))
			agent.Tools.RegisterReplacing(setTodos)
			agent.Tools.RegisterReplacing(tools.NewTaskDeleteTool(al.taskStore))
			agent.Tools.RegisterReplacing(tools.NewAgentListTool(func() []tools.AgentInfo {
				var infos []tools.AgentInfo
				for _, id := range registry.ListAgentIDs() {
					if a, ok := registry.GetAgent(id); ok {
						// ADR-049 D3: System Agents (the Judge) are excluded from
						// list_agents — it is the delegation picker ("resolve agent
						// names to IDs before delegating"), and a System Agent is
						// never a delegation target (nor a chat target). Excluding it
						// here keeps the picker consistent with the workspace
						// delegation graph, which never contains a System Agent.
						if a.AgentType == string(config.AgentTypeSystem) {
							continue
						}
						infos = append(infos, tools.AgentInfo{ID: a.ID, Name: a.Name, Type: "custom"})
					}
				}
				return infos
			}))
		}

		// ADR-052 plan/task tool surface (create_plan, execute_plan,
		// run_task, inspect_session) — the single wiring site Wave 1 left
		// unwired for Wave 2 (see pkg/tools/plan.go / run_task.go /
		// inspect_session.go's "another wave's job" doc comments). Not
		// nested inside the `al.taskStore != nil` guard above —
		// wirePlanToolsForAgent does its own nil-checks per dependency
		// (taskStore, taskExecutor, planStore, session store) and logs
		// loudly (Error) on any gap rather than silently skipping the whole
		// surface. al.GetPlanStore() may still return nil here on the very
		// FIRST pass — this call runs inside NewAgentLoop, before the
		// gateway constructs the real plan.Store in setupAndStartServices —
		// so create_plan/execute_plan register with a nil store and fail
		// closed at Execute() (Wave-1 discipline) until SetPlanStore
		// re-wires every agent with the real store once it exists. Read via
		// the accessor (not the bare al.planStore field) since SetPlanStore
		// writes it under al.mu — a bare field read here would race that
		// writer (7-reviewer gate NIT).
		al.wirePlanToolsForAgent(agent, al.GetPlanStore())

		// list_jobs (the unified background-job roster). Separate from the
		// plan surface above because it spans plans, standalone tasks AND
		// delegated sessions, and because it needs no late re-bind — every
		// store is read through a live adapter (see wireJobRosterForAgent).
		al.wireJobRosterForAgent(agent)

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
				// FR-023a: the lease wait is CLAMPED against page_timeout at
				// load AND here on every reload — EffectiveLeaseWaitSec is the
				// one function that does both, so the two can never disagree.
				browserCfg.LeaseWait = time.Duration(
					cfg.Tools.Browser.EffectiveLeaseWaitSec(),
				) * time.Second
				if cfg.Tools.Browser.ProfileDir != "" {
					browserCfg.ProfileDir = cfg.Tools.Browser.ProfileDir
				}
				if cfg.Tools.Browser.ExecPath != "" {
					browserCfg.ExecPath = cfg.Tools.Browser.ExecPath
				}
				// Idle reaping: 0 = unset, keep browser.DefaultIdleTTL;
				// negative = operator explicitly disables reaping (mapped to 0,
				// which ReapIdleSessions treats as "never reap").
				if cfg.Tools.Browser.IdleTTLSec > 0 {
					browserCfg.IdleTTL = time.Duration(cfg.Tools.Browser.IdleTTLSec) * time.Second
				} else if cfg.Tools.Browser.IdleTTLSec < 0 {
					browserCfg.IdleTTL = 0
				}
				// ADR-075 FR-040a / FR-072: the whole-browser idle window and
				// the closed-profile cache-trim schedule. Both are documented
				// operator keys, and both were unreachable until this line —
				// the value was parsed into nothing and the pool silently ran
				// its built-in constants, so an operator who changed the number
				// saw exactly what one who had not saw. Assigned
				// UNCONDITIONALLY (0 means "unset", which is what the pool's
				// own default fallback expects) and on the reload pass as well
				// as the fresh-seed one, so a Settings save takes effect
				// without a restart. Zero and negative both mean "use the
				// default" — there is no value that switches idle close off
				// (FR-061). Regression coverage:
				// pkg/tools/browser/pool_ttl_config_reachability_test.go.
				browserCfg.IdleCloseTTL = cfg.Tools.Browser.EffectiveIdleCloseTTL()
				browserCfg.CacheTrimInterval = cfg.Tools.Browser.EffectiveCacheTrimInterval()
				// Start page: an operator override wins; otherwise default to
				// the gateway's own served start page so a fresh tab lands
				// somewhere branded and legible instead of about:blank (a blank
				// void is indistinguishable from a broken panel on this
				// surface). Addressed over LOOPBACK deliberately — the client
				// is the managed headless Chrome running on this same host, so
				// localhost is reachable even when the gateway binds a wildcard
				// address (where the canonical public origin is empty) and even
				// with no public URL configured at all. The same
				// localhost:port origin is already granted through the SSRF
				// checker just above.
				if cfg.Tools.Browser.StartPageURL != "" {
					browserCfg.StartPageURL = cfg.Tools.Browser.StartPageURL
				} else if cfg.Gateway.Port > 0 {
					browserCfg.StartPageURL = fmt.Sprintf("http://localhost:%d/browser-start", cfg.Gateway.Port)
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

				// browser_evaluate registration: the tool is ALWAYS registered,
				// on every agent, regardless of this flag — registration has
				// never been conditional. What the flag gates is EXECUTION, at
				// EvaluateTool.Execute.
				//
				// sandbox.browser_evaluate_enabled is now SEEDED TRUE
				// (ADR D1.9b ruling 2), so on a fresh install the tool works and
				// which agents may call it is decided by tool policy. This
				// remains the operator's runtime kill switch.
				//
				// nil resolves to FALSE, not true: a construction that skips
				// DefaultConfig() must not silently turn arbitrary in-page
				// JavaScript on. The default lives in the seed, which is data,
				// never in this resolution. (#438: the
				// pkg/policy.builtinToolPolicies entry is advisory; that path is
				// test-only, not a live dispatch gate.)
				evaluateEnabled := config.ResolveBool(cfg.Sandbox.BrowserEvaluateEnabled, false)
				// ADR-043: ensure the gateway-scoped shared-Chrome coordinator
				// exists (constructed once; reused across hot-reload so the
				// per-agent browser contexts it owns — and thus agents' login
				// state — survive a Settings save). An agent configured with an
				// explicit tools.browser.cdp_url bypasses the coordinator: its
				// ensureStarted takes the CDPURL branch first.
				//
				// MED-1: on a RELOAD (coordinator already exists), apply the
				// runtime-cheap config deltas.
				// headless/exec_path/profile_dir are launch-time properties of
				// the already-running Chrome and cannot hot-apply —
				// ApplyRuntimeConfig warn-logs those so an operator isn't
				// silently misled. CRIT-002 stays intact: the coordinator is
				// never rebuilt on reload.
				al.mu.Lock()
				if al.browserPool == nil {
					al.browserPool = browser.NewBrowserPool(al.homePath, browserCfg)
					// FR-042a: before this gateway launches anything, settle
					// what a PREVIOUS run left behind — stale markers cleared,
					// orphaned Chromes terminated, keys another live gateway
					// still owns refused. Discriminated by the launch lock, not
					// by the marker's pid; see ReconcileMarkers for why that
					// distinction is what stops one gateway killing another's
					// browser.
					if refused := al.browserPool.ReconcileMarkers(); len(refused) > 0 {
						logger.WarnCF("agent", "another gateway owns some workspaces' browsers — this one will not start them",
							map[string]any{"workspaces": refused})
					}
				} else {
					al.browserPool.ApplyRuntimeConfig(browserCfg)
				}
				// FR-034: push tools.browser.actionability_gate into the
				// actionability gate's single chokepoint. It runs on the
				// fresh-seed pass AND on every config reload — the revert
				// switch takes effect without a restart, which is the whole
				// reason it exists.
				browser.SetActionabilityGate(cfg.Tools.Browser.ActionabilityGate)
				// ADR-075 D2 FR-027: browser_snapshot renders field VALUES by
				// operator ruling, so its rendered outline is run through the
				// credential replacer before it is returned. Wired at this
				// call site, and for the same reason as the line above: it
				// runs on the fresh-seed pass AND on every config reload, so a
				// secret the operator registers after boot is covered without
				// a restart. Defence in depth, not the control that makes the
				// tool safe — it substitutes registered credential plaintexts
				// and does nothing for arbitrary form values.
				browser.SetSensitiveDataReplacer(cfg.SensitiveDataReplacer())
				pool := al.browserPool
				al.mu.Unlock()
				// fs-workspace: browser tools (browser_screenshot) get agent.Home +
				// RestrictToWorkspace so screenshot paths resolve through the same
				// workspace root as the other file tools (FR-009).
				// FR-002a: the tools take a RESOLVER, not a manager. The
				// browser a tool drives is now a property of the TURN
				// (ResolveBrowsingKey + BrowserManagerForKey), never of
				// whichever agent it was registered under — which is the
				// reported defect ADR-075 §1.1 records.
				if regErr := browser.RegisterTools(
					agent.Tools, al.browserResolver(), evaluateEnabled,
					agent.Home, cfg.Agents.Defaults.RestrictToWorkspace,
				); regErr != nil {
					logger.ErrorCF("agent", "Failed to register browser tools",
						map[string]any{"error": regErr.Error(), "agent_id": agentID})
				} else {
					al.mu.Lock()
					al.browserRegisteredAgents[agentID] = true
					// The factory carries THIS reload's config + SSRF checker,
					// so a lazily-created manager gets the operator's current
					// security state rather than boot-time state.
					cfgSnapshot := browserCfg
					ssrfSnapshot := browserSSRF
					al.browserFactory = func(key browser.BrowsingKey) (*browser.BrowserManager, error) {
						m, err := browser.NewBrowserManager(cfgSnapshot, ssrfSnapshot)
						if err != nil {
							return nil, err
						}
						m.AttachPool(pool, key)
						return m, nil
					}
					factory := al.browserFactory
					al.mu.Unlock()

					// FR-026b: one register/release cycle per BROWSING KEY per
					// reload, not per agent. N agents on one workspace resolve
					// to ONE key, and doing this per agent would tear the same
					// browser down and back up N times per Settings save.
					key, keyErr := browser.ResolveBrowsingKeyForAgent(omnipusHome(), agentID, "")
					switch {
					case keyErr != nil:
						// No workspace (or an ambiguous membership, FR-033).
						// The tools stay registered and each call reports
						// ErrNoBrowsingContext by name — never a shared browser.
						logger.DebugCF("agent", "no browser for this agent yet — it is not rooted in one workspace",
							map[string]any{"agent_id": agentID, "reason": keyErr.Error()})
					case seenBrowserKeys[key.String()]:
						liveBrowserKeys[key.String()] = true
					default:
						seenBrowserKeys[key.String()] = true
						liveBrowserKeys[key.String()] = true
						al.rewireBrowserManagerForKey(key, factory)
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
		// Registered for every agent (mirrors instance.go's remember/
		// recall_memory/run_retrospective registration): this used to be
		// gated on `agentID != "main"`, excluding the retired sentinel. That
		// was a hardcoded identity check, not a capability one — the gate
		// went with the sentinel, same as the other memory tools. Whether an
		// agent may recall its own conversation history is its tool policy,
		// like every other tool.
		//
		// RegisterReplacing, not Register: registerSharedTools re-runs on
		// every reload and builds a fresh tool each time; #278 made Register
		// discard a same-name newcomer, so plain Register would keep the
		// stale instance. See docs/internal/false-green-patterns.md §5.
		if agent.Sessions != nil {
			agent.Tools.RegisterReplacing(NewRecallConversationTool(agent.Sessions, al))
		} else {
			logger.WarnCF("agent",
				"recall_conversation not registered — agent.Sessions is nil",
				map[string]any{"agent_id": agentID})
		}

		// Register the unified `ToolSearch` infra tool (search + load paths).
		// Replaces the former search_tools_bm25 + search_tools_regex + standalone load_tool trio.
		// The resolver uses context-aware closures so per-session and per-agent state
		// is read from the tool ctx at call time, avoiding data races on the shared
		// instance across concurrent turns on the same agent.
		//
		// ALWAYS registered unconditionally — regardless of cfg.Tools.Manifest.Compressed
		// or MCP discovery settings. Registration is cheap and harmless when unused.
		//
		// Why unconditional: the tools_on_demand PUT endpoint flips Compressed live via
		// SwapConfig without re-running agent registration. If ToolSearch was only registered
		// when Compressed=true at boot, a false→true live toggle would leave ToolSearch absent
		// from the registry, causing Get("ToolSearch") to return !ok in buildCompressedToolDefs
		// and ensureInfraToolsExecutable — every lazy tool silently unreachable, no error logged.
		// The "no restart needed" promise the UI makes becomes false.
		//
		// When Compressed is OFF at turn time, the per-turn gates (cfg.Tools.Manifest.Compressed
		// at lines ~5049, ~5115, ~5026) skip the compressed paths entirely: ToolSearch is never
		// sent to the model and never force-added to policyFiltered. For an agent whose tools
		// mostly resolve to deny it is also stripped by FilterToolsByPolicy in the uncompressed
		// path (not in allow-list), so no spurious callable appears. For an agent whose tools
		// mostly resolve to allow it may appear in the uncompressed defs, which is harmless
		// (the model has all tools anyway).
		//
		// Guard against double-registration in case the MCP init path already added it.
		// Derives the name(s) to check from tools.InfraManifestToolNames() rather than a
		// hardcoded literal, so this guard cannot silently stop guarding on a future rename.
		{
			alreadyTools := true
			for _, infraName := range tools.InfraManifestToolNames() {
				if _, ok := agent.Tools.Get(infraName); !ok {
					alreadyTools = false
					break
				}
			}
			if !alreadyTools {
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
					// string is surfaced verbatim in the ToolSearch error message.
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
						policyFiltered, policyVerdicts := tools.FilterToolsByPolicy(
							allAgentTools,
							callerAgent.AgentType,
							callerAgent.LoadToolPolicy(),
						)
						// Tier gate: full/infra tools are already callable — they never
						// need to be loaded. Check policy FIRST so a denied full-tier tool
						// gets a clear "denied" signal rather than a false "already available".
						// If policy allows a full-tier tool, return the sentinel
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
								// ADR-071 §3.2's ambiguity band needs to know when a
								// loadable tool's resolved policy is "ask" (requires
								// user confirmation) so it can exclude such tools from
								// the speculative cross-category promotion clause.
								// FilterToolsByPolicy already resolved this per-tool
								// verdict; surface it via the typed sentinel reason
								// rather than a second lookup.
								if policyVerdicts[name] == "ask" {
									return true, tools.CanLoadAskPolicyPrefix
								}
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
							hiddenAllowed, hiddenVerdicts := tools.FilterToolsByPolicy(
								[]tools.Tool{hiddenTool},
								callerAgent.AgentType,
								callerAgent.LoadToolPolicy(),
							)
							if len(hiddenAllowed) > 0 {
								if hiddenVerdicts[name] == "ask" {
									return true, tools.CanLoadAskPolicyPrefix
								}
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
						// can correct a hallucinated or transposed name (C4 fix). Match
						// against policyFiltered (the POLICY-ALLOWED set), not allAgentTools
						// (the pre-policy set) — a typo suggestion must never point at a tool
						// this agent's policy denies. Cost: hidden MCP tools aren't in
						// policyFiltered, so a near-miss typo of a hidden MCP tool's name
						// gets a bare "unknown tool" with no "did you mean" hint. That's the
						// correct tradeoff (never suggest a name the agent can't call).
						if suggestion := tools.FindClosestToolName(policyFiltered, name); suggestion != "" {
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
						// ADR-071 D3 §4.6: the bucket is (agent, session), not
						// session alone — callerID is the same value already
						// resolved above (tools.ToolAgentID(ctx), falling back
						// to capturedAgentID), matching what the readers
						// (buildCompressedToolDefs/buildToolManifestNote) derive
						// from ts.agent.ID.
						bucket := manifestBucketKey(
							callerID,
							tools.ToolTranscriptSessionID(ctx),
							tools.ToolSessionKey(ctx),
						)
						al.markToolsLoaded(bucket, loadedOK)

						// ADR-071 §4.3.1(a) FR-038/FR-038a: record a pending
						// search-follow-up entry for each newly-promoted name,
						// but ONLY on the query (by-description) path — an
						// exact-name `names` load is the model deliberately
						// naming a tool it already knows about, and recording
						// it would reintroduce the false-positive floor r3/r4
						// diagnosed and corrected (see the ADR's MIN-001 note).
						if tools.IsSearchPromotion(ctx) {
							al.recordPendingSearchPromotions(bucket, loadedOK)
						}
						return schemas, rejected
					},
				)
				agent.Tools.RegisterReplacing(toolsTool)
			}
		}

		// Register the ADR-072 D1 `Skill` tool (load-by-slug + search-by-query
		// paths) — this codebase's second instance of the "index in context,
		// content on demand" pattern ADR-071 established for ToolSearch
		// immediately above, one layer up for skills. ALWAYS registered
		// unconditionally, mirroring ToolSearch's own registration exactly:
		// Constraint #6 seeds "Skill": allow for every agent
		// (pkg/coreagent/core.go, pkg/config/defaults.go), so the tool must
		// exist to be governed by that policy regardless of whether this
		// installation has any skills installed yet. Resolver closures read
		// per-call state from ctx at call time, avoiding data races on the
		// shared instance across concurrent turns on the same agent.
		{
			capturedAgentID := agentID

			skillMaxResults := cfg.Tools.MCP.Discovery.MaxSearchResults
			if skillMaxResults <= 0 {
				// ADR-072 D1.2/MIN-003: Skill's search mode deliberately
				// inherits ToolSearch's own result cap rather than
				// introducing a second number to reason about.
				skillMaxResults = 5
			}

			if _, already := agent.Tools.Get("Skill"); !already {
				skillTool := tools.NewSkillTool(skillMaxResults)
				skillTool.SetResolver(
					// load resolves slug for the acting agent through the full
					// per-shelf grant model (ADR-072 D4/D4.1, via
					// ContextBuilder.ResolveSkillFullForWorkspace) and loads its
					// body directly from the resolved shelf's own on-disk path
					// for this turn only — every Skill call audited (D3.1).
					func(ctx context.Context, slug string) tools.SkillLoadOutcome {
						callerID := tools.ToolAgentID(ctx)
						if callerID == "" {
							callerID = capturedAgentID
						}
						workspaceID := tools.ToolWorkspaceID(ctx)
						callerAgent, ok := al.registry.GetAgent(callerID)
						if !ok {
							audit.EmitSkillCall(al.auditLogger, callerID, workspaceID, slug,
								audit.SkillCallModeLoad, audit.SkillCallOutcomeNotFound, "")
							return tools.SkillLoadOutcome{Status: tools.SkillLoadNotFound}
						}

						if resolved, resolvedOK := callerAgent.ContextBuilder.ResolveSkillFullForWorkspace(workspaceID, slug); resolvedOK {
							if content, readOK := skills.LoadSkillFile(resolved.Path); readOK {
								audit.EmitSkillCall(al.auditLogger, callerID, workspaceID, resolved.Slug,
									audit.SkillCallModeLoad, audit.SkillCallOutcomeLoaded, string(resolved.Shelf))
								return tools.SkillLoadOutcome{
									Status:        tools.SkillLoadLoaded,
									Content:       content,
									Shelf:         resolved.Shelf,
									CanonicalSlug: resolved.Slug,
								}
							}
							// Resolved but the file vanished or became unreadable
							// between resolution and read (rare race) — report
							// not-found rather than a silent empty load.
							logger.WarnCF("agent", "skill resolved but its content could not be read",
								map[string]any{"agent_id": callerID, "skill": slug, "path": resolved.Path})
							audit.EmitSkillCall(al.auditLogger, callerID, workspaceID, slug,
								audit.SkillCallModeLoad, audit.SkillCallOutcomeNotFound, "")
							return tools.SkillLoadOutcome{Status: tools.SkillLoadNotFound}
						}

						// Not resolved — distinguish "exists but this agent is
						// not granted it" (registry/builtin shelf, unfiltered
						// via ListSkillsDetailed) from "genuinely absent on any
						// shelf" (ADR-072 D4/FR-054's SkillNotFoundCode).
						for _, s := range callerAgent.ContextBuilder.ListSkillsDetailed() {
							if strings.EqualFold(s.ID, slug) || strings.EqualFold(s.Name, slug) {
								audit.EmitSkillCall(al.auditLogger, callerID, workspaceID, slug,
									audit.SkillCallModeLoad, audit.SkillCallOutcomeDenied, "")
								return tools.SkillLoadOutcome{Status: tools.SkillLoadDenied}
							}
						}
						audit.EmitSkillCall(al.auditLogger, callerID, workspaceID, slug,
							audit.SkillCallModeLoad, audit.SkillCallOutcomeNotFound, "")
						return tools.SkillLoadOutcome{Status: tools.SkillLoadNotFound}
					},
					// canUse reports whether the acting agent may load slug —
					// the SAME per-shelf grant model `load` consults, exposed
					// separately so the search path can filter the ranked
					// match list without loading every candidate's full body.
					func(ctx context.Context, slug string) bool {
						callerID := tools.ToolAgentID(ctx)
						if callerID == "" {
							callerID = capturedAgentID
						}
						callerAgent, ok := al.registry.GetAgent(callerID)
						if !ok {
							return false
						}
						workspaceID := tools.ToolWorkspaceID(ctx)
						_, resolvedOK := callerAgent.ContextBuilder.ResolveSkillFullForWorkspace(workspaceID, slug)
						return resolvedOK
					},
					// corpus returns every installed skill's slug+description
					// across every shelf visible to the acting agent's
					// workspace — registry+builtin (UNFILTERED by any grant)
					// plus that workspace's own project shelf — for BM25
					// ranking (ADR-071 §3.2.2, applied to skills by ADR-072
					// D1): the corpus must never be pre-filtered, only the
					// ranked match list (via canUse above).
					func(ctx context.Context) []tools.SkillSearchDoc {
						callerID := tools.ToolAgentID(ctx)
						if callerID == "" {
							callerID = capturedAgentID
						}
						callerAgent, ok := al.registry.GetAgent(callerID)
						if !ok {
							return nil
						}
						workspaceID := tools.ToolWorkspaceID(ctx)
						all := callerAgent.ContextBuilder.ListSkillsDetailed()
						projectShelf := callerAgent.ContextBuilder.ProjectShelfForWorkspace(workspaceID)

						docs := make([]tools.SkillSearchDoc, 0, len(all)+len(projectShelf))
						seen := make(map[string]struct{}, len(all)+len(projectShelf))
						for _, s := range all {
							docs = append(docs, tools.SkillSearchDoc{Slug: s.ID, Description: s.Description})
							seen[strings.ToLower(s.ID)] = struct{}{}
						}
						for key, ps := range projectShelf {
							if _, dup := seen[key]; dup {
								// D4.2 carve-out: a granted registry/builtin
								// slug already claims this name in the menu and
								// on resolution — the search corpus must not
								// offer it twice under two different shelves.
								continue
							}
							docs = append(docs, tools.SkillSearchDoc{Slug: ps.ID, Description: ps.Description})
						}
						return docs
					},
				)
				agent.Tools.RegisterReplacing(skillTool)
			}
		}
	}

	// FR-026a. A workspace whose last agent was removed (or whose team moved
	// off it) leaves a BrowserManager in al.browserMgrs and — worse — a
	// coordinator-owned browser context (cookie/localStorage partition)
	// leaking forever in c.contexts. Diff the LIVE BROWSING KEYS this pass
	// resolved against what the map holds, and dispose the difference via
	// coordinator.RemoveAgent (which cancels the OWNING chromedp context so
	// chromedp runs Target.disposeBrowserContext, unlike reload-Release which
	// preserves it).
	//
	// ⚠️ The liveness predicate is the set of live BROWSING KEYS, never
	// registry.ListAgentIDs(). This diff used to compare the map against agent
	// ids, which was correct only while the map WAS keyed by agent id: run
	// unchanged against a key-keyed map it matches nothing, so every browser
	// looks removed and every workspace's Chrome context is disposed on the
	// first Settings save — logins gone, silently, with a cheerful INFO line
	// per workspace saying it removed a manager for a "deleted agent".
	al.mu.Lock()
	pool := al.browserPool
	var removedKeys []string
	for k := range al.browserMgrs {
		if !liveBrowserKeys[k] {
			removedKeys = append(removedKeys, k)
			delete(al.browserMgrs, k)
		}
	}
	registeredAgentIDs := registry.ListAgentIDs()
	stillPresent := make(map[string]bool, len(registeredAgentIDs))
	for _, id := range registeredAgentIDs {
		stillPresent[id] = true
	}
	for id := range al.browserRegisteredAgents {
		if !stillPresent[id] {
			delete(al.browserRegisteredAgents, id)
		}
	}
	al.mu.Unlock()
	for _, k := range removedKeys {
		// FR-026's roster-change half: a workspace that no longer has a single
		// browser-policy-allowed agent on its CoreTeam gets its Chrome CLOSED.
		//
		// Closed, not deleted. The workspace still exists and its user still
		// expects to be logged in when an agent is added back, so the profile
		// directory stays on disk (FR-043a: workspace DELETION is the only
		// trigger that removes it, and that path lives in the REST handler).
		if pool != nil {
			if key, kerr := browser.ParseBrowsingKeyString(k); kerr == nil {
				pool.Close(key)
			}
		}
		logger.InfoCF("agent", "closed the browser for a workspace no live agent is rooted in (its profile is kept)",
			map[string]any{"browsing_key": k})
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

// agentExistsChecker builds the read-only registry existence probe threaded
// through the delegation-deny checkers so a denial can say "agent not found"
// instead of the generic trust-set message when the named target was never a
// real agent at all. Consulted for message text ONLY — never for the
// allow/deny decision.
//
// A nil registry (should not happen in production; defensive only) returns
// nil rather than a closure that would falsely report every id as
// nonexistent — a probe that lies "does not exist" about an agent that may
// in fact exist is worse than no probe at all. The nil return collapses the
// nil-registry path onto the SAME generic "not permitted" fallback the
// caller would have hit by omitting the variadic arg entirely, instead of
// fabricating a misleading "does not exist" message.
func agentExistsChecker(registry *AgentRegistry) func(id string) bool {
	if registry == nil {
		return nil
	}
	return func(id string) bool {
		if _, ok := registry.GetAgent(id); ok {
			return true
		}
		// Fall back to the durable entity store before concluding the agent
		// is genuinely nonexistent. The in-memory registry this probe
		// consults is only refreshed by the reload pipeline (the async,
		// fire-and-forget gateway.go reloadTrigger for a plain hot-reload;
		// UpsertAgentFast for create/update's fast path, which itself
		// defers to that same async reload when one is already in flight —
		// see UpsertAgentFastFunc's own doc comment), so an agent whose
		// entity record was JUST durably written (agentstore.Store.Create
		// always runs synchronously before either publish path — see
		// UpsertAgentFast's DEFECT 1 fix comment in registry.go, which
		// establishes this exact "ask the durable entity store, not the
		// possibly-stale in-memory view" precedent) can be real on disk
		// before the registry catches up. Without this fallback, a
		// delegate/switch_agent call landing in that window reports the
		// misleading "agent %q does not exist" — masking the actual denial
		// reason (e.g. a missing trust edge) a UAT run observed when the
		// target agent, in fact, existed. Best-effort: a store read error
		// here is treated the same as "not found" (the pre-existing
		// behavior for a target that genuinely never existed) rather than
		// failing the whole delegation check — this probe is message-only
		// and never controls the allow/deny outcome (see this function's
		// own callers' doc comments).
		_, err := agentstore.New(omnipusHome()).Get(id)
		return err == nil
	}
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
// agentExists is an OPTIONAL trailing arg (variadic so the many pre-existing
// call sites — production and test — that predate this distinction keep
// compiling unchanged), consulted ONLY to distinguish the no-edge denial's
// MESSAGE. Without it, delegating to an agent that EXISTS but has no trust
// edge from the caller, and delegating to a genuinely NONEXISTENT agent,
// both returned byte-identical generic denial text — making a typo'd
// agent_id indistinguishable from a real permissions gap. It never changes
// the allow/deny OUTCOME — both cases still deny with Policy: DenyTrustSet —
// it only selects which of the two messages below is returned. Omitting it
// (or passing nil) falls back to the pre-existing generic "not permitted"
// message — every production wiring site (registerSharedTools,
// NewSysagentDelegationDeny, both in loop.go) passes a real checker.
//
// Returns (edge, nil) when an authorizing edge exists; (nil, denial) otherwise.
func findDelegationEdge(
	ctx context.Context,
	callerAgentID, targetAgentID string,
	mode config.DelegationMode,
	agentExists ...func(id string) bool,
) (*workspace.DelegationEdge, *tools.DelegationDenial) {
	var exists func(string) bool
	if len(agentExists) > 0 {
		exists = agentExists[0]
	}
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

	if exists != nil && !exists(targetAgentID) {
		logger.WarnCF("agent", "delegation denied: target agent does not exist", map[string]any{
			"agent_id": callerAgentID, "target": targetAgentID, "workspace_id": wsID, "mode": string(mode),
		})
		return nil, &tools.DelegationDenial{
			Reason:        fmt.Sprintf("agent %q does not exist", targetAgentID),
			Policy:        tools.DenyTrustSet,
			TargetAgentID: targetAgentID,
		}
	}

	logger.WarnCF("agent", "delegation denied: no edge in workspace graph", map[string]any{
		"agent_id": callerAgentID, "target": targetAgentID, "workspace_id": wsID, "mode": string(mode),
	})
	return nil, &tools.DelegationDenial{
		Reason: fmt.Sprintf(
			"delegation to agent %q is not permitted in this workspace",
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
//
// agentExists is an optional trailing arg (variadic, same rationale as
// findDelegationEdge's own doc comment) forwarded to findDelegationEdge
// purely to distinguish the no-edge denial's message — message-only, never
// affects the allow/deny decision itself.
func buildDelegationDenyChecker(
	currentAgentID string,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
	selfAssignmentExempt bool,
	agentExists ...func(id string) bool,
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
			edge, denial := findDelegationEdge(ctx, currentAgentID, targetAgentID, mode, agentExists...)
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
// agentExists is an optional trailing arg (variadic — see findDelegationEdge's
// own doc comment for why): a read-only existence probe against the live
// agent registry, consulted only to distinguish "target agent doesn't exist"
// from "target agent exists but has no trust edge" in the denial MESSAGE —
// it never affects the allow/deny outcome. Omit it only in tests that don't
// care about the distinction; every production wiring site passes a real
// checker (see registerSharedTools / NewSysagentDelegationDeny).
func buildDelegationDenyCheckerForDelegate(
	currentAgentID string,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
	agentExists ...func(id string) bool,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	return buildDelegationDenyChecker(currentAgentID, defaults, mode, false, agentExists...)
}

// buildDelegationDenyCheckerForTaskReassignment is the wiring-site constructor for the
// task tools: create_task / update_task and the cross-workspace
// create_task_in_workspace / update_task_in_workspace (via NewSysagentDelegationDeny).
// It bakes in selfAssignmentExempt=true: reassigning a task to the agent that already
// owns it is NOT delegation (no new instance is spawned), so a self-target is allowed
// without consulting the graph. Non-self targets are still fully graph-gated.
//
// agentExists: see buildDelegationDenyCheckerForDelegate's doc comment — same
// optional-trailing-arg, message-only distinction, same "pass a real checker
// in production" expectation.
func buildDelegationDenyCheckerForTaskReassignment(
	currentAgentID string,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
	agentExists ...func(id string) bool,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	return buildDelegationDenyChecker(currentAgentID, defaults, mode, true, agentExists...)
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
		gate := buildDelegationDenyCheckerForTaskReassignment(
			callerAgentID, defaults, config.DelegationModeTask, agentExistsChecker(al.GetRegistry()),
		)
		return gate(ctx, targetAgentID)
	}
}

// NewSysagentBashPolicyResolver builds the systools.Deps.ResolveBashPolicy
// closure (ADR-049 D2 rule 5, FR-017/052, review r1 major M5): resolves an
// assignee agent's effective "bash" tool policy from the SAME live registry
// judge.go's runMachineCheck and the plain create_task tool's own
// bashPolicyChecker use (tools.EffectiveToolPolicy, ScopeCore) — parity
// between the same-workspace and cross-workspace (create_task_in_workspace)
// task-creation surfaces.
func (al *AgentLoop) NewSysagentBashPolicyResolver() func(assigneeAgentID string) (policy string, ok bool) {
	return func(assigneeAgentID string) (policy string, ok bool) {
		agentInst, found := al.GetRegistry().GetAgent(assigneeAgentID)
		if !found || agentInst == nil {
			return "", false
		}
		return tools.EffectiveToolPolicy(agentInst.LoadToolPolicy(), tools.ScopeCore, agentInst.AgentType, "bash"), true
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
			// middle of exiting, enqueue into it. The exiting check closes
			// the silent-drop race (pass-2 silent-failure-hunter N1) where
			// the dispatcher Load'd a worker whose idleTimer had already
			// fired but whose deferred sessionWorkers.Delete had not yet
			// run — enqueue into the dying worker's inbox would never be
			// drained. When exiting=true we fall through to the spawn path
			// below, which will create a fresh worker.
			if existing, ok := al.sessionWorkers.Load(scope); ok {
				w, ok := existing.(*sessionWorker)
				if !ok {
					logger.ErrorCF("agent", "sessionWorkers: invariant violated — unexpected value type",
						map[string]any{"scope": scope, "got_type": fmt.Sprintf("%T", existing)})
					// Fall through to spawn a replacement, same as a dying worker.
				} else if !w.exiting.Load() {
					w.enqueue(msg)
					continue
				}
				// Dying worker (or corrupted entry) — fall through to spawn replacement.
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

// ErrNoContinuationTarget is a sentinel returned by buildContinuationTarget
// for messages that have no continuation target by design (e.g. the
// synthetic "system" channel). Callers use errors.Is to distinguish this
// expected no-target case from a genuine resolution failure.
var ErrNoContinuationTarget = errors.New("no continuation target for message")

func (al *AgentLoop) buildContinuationTarget(msg bus.InboundMessage) (*continuationTarget, error) {
	if msg.Channel == "system" {
		return nil, ErrNoContinuationTarget
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
			// FIX 1 (re-review of the re-review): a real meta-read failure
			// (corrupt meta.json, decode error, I/O error — see
			// pkg/session/unified.go's readMetaLocked) must not take the
			// identical silent path as "this session legitimately has no
			// workspace". os.ErrNotExist (no meta.json yet — a genuinely new
			// session) is the one expected, silent case; anything else is a
			// storage-integrity signal worth a WARN, matching the standard
			// the WRITE side of this same data already holds itself to
			// (pkg/gateway/schedules.go's stampScheduledSessionWorkspace).
			if meta, mErr := store.GetMeta(msg.SessionID); mErr != nil {
				if !errors.Is(mErr, os.ErrNotExist) {
					logger.WarnCF("agent",
						"continuation: could not read session meta while resolving workspace; workspace unresolved",
						map[string]any{"session_id": msg.SessionID, "error": mErr.Error()})
				}
			} else if meta != nil && meta.WorkspaceID != "" {
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

// EmitPlanStatusChanged publishes a Plan state/phase/progress transition onto
// the event bus so every connected SPA WebSocket client receives a
// plan_status frame (ADR-049 D4/D7, spec Part B R3). Safe to call from any
// goroutine — the bus drops to a full subscriber rather than blocking. The
// production emission path is pkg/plan.Store.OnChange, wired at gateway boot
// (setupAndStartServices) to call this after every successful plan
// Create/Update — see that wiring for why this is the single choke point for
// both the plan engine's and the gateway REST layer's mutations.
func (al *AgentLoop) EmitPlanStatusChanged(p PlanStatusChangedPayload) {
	al.emitEvent(EventKindPlanStatusChanged, EventMeta{Source: "plan_engine"}, p)
}

// EmitGoalStatusChanged publishes a `/goal` loop status transition onto the
// event bus so every connected SPA WebSocket client receives a goal_status
// frame (ADR-049 D6/D7, spec Part B US-8). Safe to call from any goroutine.
func (al *AgentLoop) EmitGoalStatusChanged(p GoalStatusChangedPayload) {
	al.emitEvent(EventKindGoalStatusChanged, EventMeta{Source: "goal_loop"}, p)
}

// EmitLoopStatusChanged publishes a `/loop` status transition onto the event
// bus so every connected SPA WebSocket client receives a loop_status frame
// (ADR-049 D6/D7, spec Part B US-9). Safe to call from any goroutine.
func (al *AgentLoop) EmitLoopStatusChanged(p LoopStatusChangedPayload) {
	al.emitEvent(EventKindLoopStatusChanged, EventMeta{Source: "goal_loop"}, p)
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

// RegisterTool installs tool into every currently-registered agent's tool
// registry. It is exported test/instrumentation surface only (58 call sites,
// all in _test.go files as of this writing) — the standard pattern is a test
// building a real AgentLoop (whose normal boot wiring, e.g. wireExecToolDeps,
// already registers a real "bash" tool) and then calling RegisterTool with a
// wrapper/capturing/scripted double under the SAME name to observe or
// control behavior (e.g. bash_async_completion_test.go's sessionCapturingBash
// wrapping the real ExecTool). This is a deliberate, intentional override —
// not a hijack — so it uses RegisterReplacing, not Register: issue #278's
// collision-rejection in Register/RegisterHidden exists to stop an
// MCP-supplied tool from silently squatting on a trusted name (see
// pkg/agent/loop_mcp.go's registerServerTools, the only caller that ever
// registers untrusted/MCP-origin tools); this method is never called with an
// MCP-origin tool, so collision rejection here would only ever block the
// test/instrumentation override it exists to perform.
func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	registry := al.GetRegistry()
	for _, agentID := range registry.ListAgentIDs() {
		if agent, ok := registry.GetAgent(agentID); ok {
			agent.Tools.RegisterReplacing(tool)
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
		// ADR-072 D6.1.1/R4 fix: re-assert the process-wide skills write-audit
		// logger on reload too, mirroring the memory-tool re-wire immediately
		// above. SetSkillsWriteAuditLogger is a process-wide var (not
		// registry-scoped), so this is idempotent, but a hot reload must not
		// be the one path that silently leaves it unset if a future change
		// ever makes al.auditLogger's identity or lifetime reload-sensitive.
		tools.SetSkillsWriteAuditLogger(al.auditLogger)
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

	// Re-wire per-workspace project-shelf resolvers (ADR-072 R1 fix regression,
	// live UAT 2026-09-02): this was missing here, so a mounted project's
	// skills silently stopped resolving for every agent after this reload path
	// ran even once — which onboarding itself triggers, so it hit nearly every
	// real install. Mirror the two siblings above.
	wireProjectShelfResolvers(al, registry)

	// Atomically swap the config and registry under write lock
	// This ensures readers see a consistent pair
	al.mu.Lock()
	oldRegistry := al.registry

	// Store new values
	al.cfg = cfg
	al.registry = registry
	// DEFECT 2 fix (concurrency review): keep configGen in lockstep with
	// every al.cfg replacement, not only a bare SwapConfig — see configGen's
	// doc comment on the AgentLoop struct.
	al.configGen.Add(1)

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
			"model": cfg.Agents.Defaults.DefaultModel.String(),
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
// contextSettings returns the live ADR-066 ContextSettings (caps, trigger,
// ingest bound) — read per call, so a settings write applies to the next
// tool result without a restart (US-3.AC11). Satisfies the
// contextSettingsSource the recall_conversation tool type-asserts.
func (al *AgentLoop) contextSettings() config.ContextSettings {
	if cfg := al.GetConfig(); cfg != nil {
		return cfg.Context
	}
	return config.ContextSettings{}
}

func (al *AgentLoop) GetConfig() *config.Config {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.cfg
}

// BrowserResolveOutcome is a closed enum naming WHY a browser could not be
// resolved. "not registered" and "no workspace" are DIFFERENT operator-facing
// problems and were indistinguishable before ADR-075 — browser_inspect.go
// reported the former for both, so an operator whose agent simply was not on a
// workspace team was told browser tools had failed to register.
//
// There is deliberately NO BrowserResolvePoolFull: ADR-075 D1.5a deleted every
// counter, so the panel never has a capacity reason to render.
type BrowserResolveOutcome int

const (
	BrowserResolveOK BrowserResolveOutcome = iota
	// BrowserResolveNoWorkspace is browser.ErrNoBrowsingContext: this agent is
	// not rooted in a workspace, so it has no browser of its own.
	BrowserResolveNoWorkspace
	// BrowserResolveAmbiguous is FR-033: more than one candidate workspace and
	// no preference supplied. The browser REFUSES rather than tie-breaking,
	// because choosing would silently pick which set of live logins to act with.
	BrowserResolveAmbiguous
	// BrowserResolveNotRegistered means browser tools genuinely are not
	// registered for this agent.
	BrowserResolveNotRegistered
	// BrowserResolveLaunchFailed means the browser is addressable but could not
	// be created (config or SSRF wiring failure).
	BrowserResolveLaunchFailed
)

// BrowserManagerForKey returns (creating on first use) the manager that owns
// key's browser. Exactly one manager and one Chrome per key, process-wide —
// FR-001. There is no cap: ADR-075 D1.5a made live memory the only limit, and
// it is enforced at each tab open inside the manager (FR-060).
func (al *AgentLoop) BrowserManagerForKey(
	_ context.Context, key browser.BrowsingKey,
) (*browser.BrowserManager, error) {
	if key.IsZero() {
		return nil, browser.ErrNoBrowsingContext
	}
	al.mu.Lock()
	if mgr, ok := al.browserMgrs[key.String()]; ok && mgr != nil {
		al.mu.Unlock()
		return mgr, nil
	}
	factory := al.browserFactory
	al.mu.Unlock()
	if factory == nil {
		return nil, fmt.Errorf("browser: browser tools are not registered on this gateway")
	}
	mgr, err := factory(key)
	if err != nil {
		return nil, err
	}
	// Re-check under the lock: two turns on one workspace can reach here
	// concurrently, and the loser must DISCARD its manager rather than install
	// a second Chrome for the same key.
	al.mu.Lock()
	if existing, ok := al.browserMgrs[key.String()]; ok && existing != nil {
		al.mu.Unlock()
		mgr.Shutdown()
		return existing, nil
	}
	al.browserMgrs[key.String()] = mgr
	al.mu.Unlock()
	return mgr, nil
}

// rewireBrowserManagerForKey installs a freshly-configured manager for key,
// tearing down the one it replaces. Called once per key per reload (FR-026b).
//
// The teardown discipline is ADR-043's, unchanged, with agentID replaced by the
// browsing key: coordinator.Release drops the old manager's connection and
// bookkeeping WITHOUT killing Chrome or disposing the browser context (CRIT-002
// — the context persists so the new manager re-adopts it and login survives the
// save), and prior.Shutdown() additionally covers the explicit-cdp_url manager
// that never registered with the coordinator at all and would otherwise leak
// its allocator on every reload.
func (al *AgentLoop) rewireBrowserManagerForKey(
	key browser.BrowsingKey,
	factory func(browser.BrowsingKey) (*browser.BrowserManager, error),
) {
	if factory == nil {
		return
	}
	mgr, err := factory(key)
	if err != nil {
		logger.ErrorCF("agent", "Failed to create the browser for this workspace — "+
			"ensure Chromium/Chrome is installed or set tools.browser.cdp_url",
			map[string]any{"error": err.Error(), "browsing_key": key.String()})
		return
	}
	al.mu.Lock()
	prior := al.browserMgrs[key.String()]
	pool := al.browserPool
	al.browserMgrs[key.String()] = mgr
	al.mu.Unlock()
	if pool != nil {
		// Reload: drop the OLD manager's registration only. The Chrome
		// process and its profile directory survive, which is what makes a
		// Settings save cost nobody their login (FR-043).
		pool.Release(key, prior)
	}
	if prior != nil {
		prior.Shutdown()
		prior.InvalidateExecPathCache()
	}
}

// BrowserPool returns the per-workspace browser pool (ADR-075 FR-037), or nil
// before the first registration pass has built it. The gateway needs it for
// boot preprovision (FR-016c), for the one-minute sweep's whole-Chrome idle
// close (FR-040a) and for workspace-deletion disposal (FR-026).
func (al *AgentLoop) BrowserPool() *browser.BrowserPool {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.browserPool
}

// browserResolver returns the browser.ManagerResolver every browser tool
// resolves its manager through, per Execute (FR-002a).
func (al *AgentLoop) browserResolver() browser.ManagerResolver {
	return &agentLoopBrowserResolver{al: al}
}

// agentLoopBrowserResolver implements browser.ManagerResolver over
// ResolveBrowsingKey + BrowserManagerForKey. The interface is declared in
// pkg/tools/browser and implemented here because the import direction forbids
// the reverse.
type agentLoopBrowserResolver struct{ al *AgentLoop }

func (r *agentLoopBrowserResolver) ManagerFor(
	ctx context.Context,
) (*browser.BrowserManager, browser.BrowsingKey, browser.TabOwner, error) {
	// omnipusHome(), not al.homePath: workspace membership is resolved from
	// $OMNIPUS_HOME everywhere else in this package (wireWorkingDirInjectors,
	// resolveTurnWorkDirOrRefuse), and the browser must not disagree with the
	// work dir about which workspace a turn is rooted in. al.homePath is the
	// coordinator's ownership-marker root, which is a different question.
	key, err := browser.ResolveBrowsingKey(ctx, omnipusHome())
	if err != nil {
		return nil, browser.BrowsingKey{}, browser.TabOwner{}, err
	}
	// FR-080: the tab set is the SESSION's, keyed on transcriptSessionID and
	// never on routingSessionID (which a whole delegation subtree shares, so it
	// would merge every descendant's tabs into the root's). An empty transcript
	// session is a NAMED FAILURE, never a fall-through to the operator's
	// workspace-owned set.
	//
	// This is the turn's HOME tab set, which is not always the set the call
	// ACTS on. An agent reaches the operator's workspace-owned tabs by acting
	// on one browser_list_tabs showed it (FR-070 — implicit acquisition, no
	// tool, no policy entry, no wire field), and pkg/tools/browser's
	// resolveTurn resolves that: it is a property of the call, not of the turn,
	// so there is nothing for this resolver to decide. Do NOT "fix" this to
	// return TabOwnerWorkspace() under any condition — a transcript-less or
	// misrouted turn silently landing on the operator's tabs is the implicit
	// merge ErrNoTabOwner exists to prevent.
	owner, err := browser.TabOwnerSession(tools.ToolTranscriptSessionID(ctx))
	if err != nil {
		return nil, browser.BrowsingKey{}, browser.TabOwner{}, err
	}
	mgr, err := r.al.BrowserManagerForKey(ctx, key)
	if err != nil {
		return nil, browser.BrowsingKey{}, browser.TabOwner{}, err
	}
	return mgr, key, owner, nil
}

// BrowserManagerForAgent is RETAINED for the gateway. It resolves
// agentID -> BrowsingKey server-side using preferredWorkspaceID (from the
// attaching chat session's meta, FR-017) and delegates to BrowserManagerForKey.
//
// The second return distinguishes the failure reasons the panel must show
// differently (FR-008a) — it is NOT a bare bool, because "browser tools are not
// registered for this agent" and "this agent is not on a workspace team" need
// different operator advice and used to render identically.
func (al *AgentLoop) BrowserManagerForAgent(
	ctx context.Context, agentID, preferredWorkspaceID string,
) (*browser.BrowserManager, BrowserResolveOutcome) {
	al.mu.RLock()
	registered := al.browserRegisteredAgents[agentID]
	al.mu.RUnlock()
	if !registered {
		return nil, BrowserResolveNotRegistered
	}
	key, err := browser.ResolveBrowsingKeyForAgent(omnipusHome(), agentID, preferredWorkspaceID)
	if err != nil {
		// ResolveBrowsingKeyForAgent reports both "no workspace" and FR-033's
		// ambiguous multi-membership as ErrNoBrowsingContext (they are the same
		// answer to the agent: this turn has no browser of its own). The panel
		// wants them apart, and the only thing that separates them is whether
		// more than one workspace claims the agent.
		if ids, _ := workspace.FindAllForAgent(omnipusHome(), agentID); len(ids) > 1 {
			return nil, BrowserResolveAmbiguous
		}
		return nil, BrowserResolveNoWorkspace
	}
	mgr, err := al.BrowserManagerForKey(ctx, key)
	if err != nil {
		return nil, BrowserResolveLaunchFailed
	}
	return mgr, BrowserResolveOK
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
// It encapsulates, in order: (1) the scope gate (fail-closed for unknown
// scopes), and (2) global×agent strictest-wins (deny > ask > allow, god-mode,
// wildcards). ToolSearch resolves through this same merge as every other
// static builtin tool — it is seeded "allow" as real, explicit data for every
// agent (pkg/coreagent/core.go), not a code-level force-allow (there used to
// be an unconditional infra fast-path here; it was a CLAUDE.md
// hard-constraint-6 violation and has been removed).
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
		s, ok := v.(string)
		if !ok {
			logger.ErrorCF("agent", "sessionActiveAgent: invariant violated — unexpected value type",
				map[string]any{"session_id": sessionID, "got_type": fmt.Sprintf("%T", v)})
			return "", false
		}
		return s, true
	}
	return "", false
}

// GetLastSwitchToDefault returns whether the most recent switch_agent call
// on the given session was a return-to-default (true) or a named-agent
// hand-off (false), as reported by the tool itself
// (tools.HandoffEvent.ToDefault) rather than re-derived from the resulting
// agent id. Returns (false, false) if no such record is pending — e.g. no
// switch_agent has run yet for this session, or it has already been
// consumed.
//
// One-shot: this LoadAndDeletes the entry, since it exists only to answer
// "was the switch that just completed a return-to-default" once, at the WS
// agent_switched frame builder that reads it right after the matching
// ToolExecEnd event fires. Leaving stale entries around risks a later,
// unrelated switch_agent call on the same session silently reusing a value
// it never itself observed.
func (al *AgentLoop) GetLastSwitchToDefault(sessionID string) (bool, bool) {
	if sessionID == "" {
		return false, false
	}
	v, ok := al.lastSwitchToDefault.LoadAndDelete("session:" + sessionID)
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	if !ok {
		logger.ErrorCF("agent", "lastSwitchToDefault: invariant violated — unexpected value type",
			map[string]any{"session_id": sessionID, "got_type": fmt.Sprintf("%T", v)})
		return false, false
	}
	return b, true
}

// SwapConfig atomically replaces the in-memory config with the supplied,
// fully-initialized *config.Config (credentials resolved, sensitive values
// registered). Callers are responsible for calling credentials.ResolveBundle
// and cfg.RegisterSensitiveValues before SwapConfig — this method only does
// the atomic pointer swap.
func (al *AgentLoop) SwapConfig(newCfg *config.Config) {
	al.mu.Lock()
	al.cfg = newCfg
	// DEFECT 2 fix (concurrency review): bump configGen so a concurrent
	// UpsertAgentFast in-flight against the PRE-swap cfg detects this write
	// even though al.registry is untouched — see configGen's doc comment on
	// the AgentLoop struct.
	al.configGen.Add(1)
	al.mu.Unlock()
}

// MutateConfig acquires the agent loop write lock and calls fn with a PRIVATE
// deep copy of the live *config.Config (via config.Clone, which also carries
// the runtime-registered sensitive plaintexts). fn may freely mutate that copy
// — fields, slices, maps — without racing the many UNLOCKED readers of the live
// al.cfg, most importantly fastAgentUpsert/UpsertAgentFast, whose wiring pass
// reads the exact *config.Config pointer GetConfig hands out here WITHOUT al.mu
// (see UpsertAgentFast's doc comment and its "residual, narrower hazard" note
// in registry.go — that residual is what this copy-then-swap closes).
//
// On a nil error from fn, the copy is PUBLISHED as the new al.cfg via the SAME
// pointer-swap + configGen-bump idiom SwapConfig and ReloadProviderAndConfig
// already use (under al.mu.Lock), so a concurrent UpsertAgentFast detects it
// through its configGen CAS and rebases, instead of silently reverting it. On a
// non-nil error the copy is discarded and the live al.cfg is left untouched —
// equivalent to the rollback the two existing publishers' callers perform, but
// built in: the live object was never mutated, so there is nothing to restore.
//
// fn must not call GetConfig or SwapConfig — deadlock would result (both take
// al.mu, which this method holds for the entire call). fn receives a copy, not
// the live pointer; callers that persist (e.g. systools.Deps.WithConfig)
// continue to receive that same copy and SaveConfigLocked it directly, then
// this method publishes it — persisted-disk and live-pointer stay consistent.
func (al *AgentLoop) MutateConfig(fn func(*config.Config) error) error {
	al.mu.Lock()
	defer al.mu.Unlock()
	if al.cfg == nil {
		return fmt.Errorf("agent loop config is nil")
	}
	// Deep copy so fn's mutations never touch the live object GetConfig handed
	// out (and still hands out) to concurrent unlocked readers. config.Clone
	// carries registeredSensitive so the credential-scrubbing invariant survives
	// the swap below.
	clone, err := al.cfg.Clone()
	if err != nil {
		return fmt.Errorf("agent loop: clone config for mutation: %w", err)
	}
	if err := fn(clone); err != nil {
		return err
	}
	// Publish via pointer-swap + configGen bump — the SAME shape SwapConfig
	// (al.cfg replace alone) and ReloadProviderAndConfig (al.cfg + al.registry)
	// use — so a concurrent UpsertAgentFast sees this change via its configGen
	// CAS and rebases rather than silently reverting it (DEFECT 2 family).
	al.cfg = clone
	al.configGen.Add(1)
	return nil
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
	nextCandidates := resolveModelCandidatesForAgent(cfg, cfg.Agents.Defaults.DefaultModel.Provider, modelCfg.Model, agent)
	if len(nextCandidates) == 0 {
		return "", fmt.Errorf("model %q did not resolve to any provider candidates", model)
	}

	// FR-007: rebuild the provider pool so the new model switch has every
	// distinct provider pre-built. The agent's existing pool may carry stale
	// entries for the previous primary's provider; rebuilding from the new
	// candidate chain keeps ProviderPool coherent with Candidates.
	newBuild := buildProviderPool(cfg, nextCandidates, agent.ID)

	// ADR-066 D2: the window is part of the model identity. Re-resolve it
	// through the ONE ladder for the new primary (provider, model) and flip
	// it inside the same lock as Model / Provider / Candidates so a reader
	// never pairs the new model with the old window. FR-005b: max_tokens is
	// re-clamped against the new window so B stays positive.
	windowProvider, windowModel := primaryWindowPair(nextCandidates, cfg.Agents.Defaults.DefaultModel.Provider, modelCfg.Model)
	window := ResolveWindow(cfg, windowProvider, windowModel, agent.ID)

	agent.mu.Lock()
	oldModel := agent.Model
	oldProvider := agent.Provider
	agent.Model = model
	agent.Provider = nextProvider
	agent.Candidates = nextCandidates
	agent.ThinkingLevel = parseThinkingLevel(modelCfg.ThinkingLevel)
	agent.applyWindowResolutionLocked(window)
	// From the CONFIGURED max_tokens, never from the current (possibly
	// already-clamped) field: the clamp only lowers, so re-feeding its own
	// output ratcheted the value down permanently — a round-trip through a
	// small-window model left the agent capped at that model's window/4 on a
	// 200k model, with no log line and no recovery short of a restart.
	agent.MaxTokens = clampMaxTokensForWindow(window.Window, agent.configuredMaxTokensLocked(), model)
	// Publish the new pool INSIDE the same lock as the Model + Provider +
	// Candidates flip. The atomic.Pointer in StoreProviderPool would protect
	// the pool's map against concurrent read/write on its own, but an
	// in-flight turn that has just RLock'd agent.mu to read the old Model
	// would then Load() a pool that no longer matches the model — the
	// fallback chain would route through the NEW pool's primary credentials
	// while the model field still says OLD. Holding the lock across the
	// full tuple flip makes (Model, Provider, Candidates, ProviderPool) a
	// single coherent swap from any reader's perspective.
	agent.StoreProviderPool(newBuild.pool)
	// ADR-067 FR-016: the degrade is part of the model identity too — a
	// switch onto a provider the catalog does not know must leave the agent
	// refusing turns, and a switch OFF one must clear the refusal without a
	// restart (US-6.AC3). Flipped inside the same lock as the rest of the
	// tuple so a turn never pairs the new model with the old verdict.
	agent.needsProvider = newBuild.primaryUnknown
	agent.needsProviderID = newBuild.primaryProvider
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

// SetPlanEngine installs the single hybrid plan-coordinator instance
// (ADR-049 D4) so command handlers and REST handlers can reach its Admit/
// Release admission authority and PausePlansOwnedBy/ResumePlansOwnedBy/
// HasActivePlansOwnedBy owner-lifecycle hooks. Called once at boot by the
// gateway (setupAndStartServices), before any /goal or /loop admission can
// occur. Idempotent.
func (al *AgentLoop) SetPlanEngine(pe *PlanEngine) {
	al.mu.Lock()
	al.planEngine = pe
	al.mu.Unlock()
}

// GetPlanEngine returns the installed PlanEngine (may be nil in tests or
// before boot wiring completes — Wave 2-C2's /goal and /loop admission paths
// MUST nil-check before calling Admit). Mirrors GetTaskStore/GetTaskExecutor's
// free-function-with-al-parameter convention.
func GetPlanEngine(al *AgentLoop) *PlanEngine {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.planEngine
}

// SetPlanStore installs the shared *plan.Store (ADR-052) so the
// create_plan/execute_plan agent tools can read/write it, and re-wires the
// full plan/task tool surface (create_plan, execute_plan, run_task,
// inspect_session) for every CURRENTLY-registered agent with the real
// store — mirrors SetPlanEngine's late-binding discipline exactly. Called
// once at boot by the gateway (setupAndStartServices), right where
// plan.New(...) constructs the store — see gateway.go's boot wiring
// region. Idempotent; safe to call again on hot-reload (registerSharedTools
// already re-runs wirePlanToolsForAgent on every reload, so this mainly
// matters for the initial boot gap between NewAgentLoop and
// setupAndStartServices).
func (al *AgentLoop) SetPlanStore(store *plan.Store) {
	al.mu.Lock()
	al.planStore = store
	al.mu.Unlock()

	if store == nil {
		logger.ErrorCF("agent", "SetPlanStore: installed a nil plan store — "+
			"create_plan/execute_plan will remain fail-closed", nil)
		return
	}

	reg := al.GetRegistry()
	if reg == nil {
		logger.ErrorCF("agent", "SetPlanStore: no agent registry available — "+
			"plan tool surface not re-wired", nil)
		return
	}
	for _, agentID := range reg.ListAgentIDs() {
		inst, ok := reg.GetAgent(agentID)
		if !ok || inst == nil {
			continue
		}
		al.wirePlanToolsForAgent(inst, store)

		// create_task is NOT part of the wirePlanToolsForAgent surface (that
		// function's own doc comment enumerates create_plan/execute_plan/
		// run_task/inspect_session/plan_correct/stop_plan only) — it is
		// constructed separately in registerSharedTools's "Task tools" block.
		// Re-wire its plan store here too, on the same late-binding pass, so
		// create_task(plan_id=...) stops failing closed with "plan store is
		// not configured" once the real store exists. A missing or wrong-typed
		// tool is not an error here — task tools are gated behind
		// al.taskStore != nil in registerSharedTools, so an agent with no task
		// store never registered create_task at all.
		if inst.Tools == nil {
			continue
		}
		if raw, ok := inst.Tools.Get("create_task"); ok {
			if taskCreate, ok := raw.(*tools.TaskCreateTool); ok {
				taskCreate.SetPlanStore(store)
			}
		}
	}

	// UAT fix (fix/uat-defects-2026-08-22): re-wire the system.* tool surface
	// (create_task_in_workspace, pkg/sysagent/tools) with the real plan store
	// too. WireSysagentDeps runs at boot BEFORE this store exists — the
	// gateway constructs sysAgentDeps and calls WireSysagentDeps well ahead
	// of plan.New/SetPlanStore (see gateway.go's boot wiring region) — so
	// every system.* tool instance registered by then was built with a nil
	// deps.PlanStore. Without this, create_task_in_workspace(plan_id=...)
	// fails closed with "plan store is not configured" FOREVER, for every
	// agent, even against a plan that was just created in the very same
	// workspace by the very same turn (the plain create_task tool above was
	// already re-wired here; the system.* twin was not).
	//
	// al.sysagentDeps is read-modify-written under al.mu (mirrors the
	// al.planStore guard a few lines up in this same function) because the
	// gateway listener is already live by the time this runs (boot wires
	// sysAgentDeps and starts serving well before constructing planStore),
	// so a concurrent hot-reload's ReloadProviderAndConfig could in
	// principle race the field. wireSysagentDepsLocked itself is called
	// OUTSIDE the lock — it does not touch al.mu, and holding al.mu across
	// it would only widen the critical section for no benefit (mirrors the
	// wirePlanToolsForAgent loop above, which does the same).
	al.mu.Lock()
	var sysDeps *systools.Deps
	if al.sysagentDeps != nil {
		depsCopy := *al.sysagentDeps
		depsCopy.PlanStore = store
		al.sysagentDeps = &depsCopy
		sysDeps = al.sysagentDeps
	}
	al.mu.Unlock()
	if sysDeps != nil {
		al.wireSysagentDepsLocked(reg, sysDeps)
	}
}

// GetPlanStore returns the installed plan.Store (may be nil in tests or
// before boot wiring completes). Mirrors GetTaskStore/GetPlanEngine's
// free-function-with-al-parameter convention is intentionally NOT followed
// here since every other Set/Get pair on AgentLoop that is read from
// pkg/tools construction sites (SetMediaStore/GetMediaStore) uses the
// method form; kept consistent with that sibling pair.
func (al *AgentLoop) GetPlanStore() *plan.Store {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.planStore
}

// validatePlanOwnerAgentForTool mirrors pkg/gateway/rest_plans.go's
// validatePlanOwnerAgent EXACTLY (same rule, same error text shape) for
// create_plan's SetOwnerValidator seam. pkg/agent cannot import pkg/gateway
// (gateway already imports agent — that would be a cycle), so this is a
// same-behavior local copy consumed only here. Rejects an owner_agent_id
// that is not a registered agent, or that IS registered but is a System
// Agent or worker — OwnerAgentID's contract ("the agent woken at plan
// decision points", ADR-049 D4) requires a real, addressable agent.
func validatePlanOwnerAgentForTool(cfg *config.Config, ownerAgentID string) error {
	if cfg == nil {
		return fmt.Errorf("owner_agent_id %q is not a registered agent", ownerAgentID)
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != ownerAgentID {
			continue
		}
		if !cfg.Agents.List[i].IsChatTarget() {
			return fmt.Errorf(
				"owner_agent_id %q is a System Agent or worker and cannot own a plan", ownerAgentID)
		}
		return nil
	}
	return fmt.Errorf("owner_agent_id %q is not a registered agent", ownerAgentID)
}

// isRegisteredAgentID reports whether id resolves to a known agent in the
// live registry — mirrors pkg/gateway/rest_plans.go's restAPI.isAgentID
// exactly, and drives execute_plan's SD-A7 tiered-DoD gate via
// SetIsAgentIDChecker (a plan whose CreatedBy resolves to an agent — strict
// tier — must carry >=1 DoD criterion).
func (al *AgentLoop) isRegisteredAgentID(id string) bool {
	if id == "" {
		return false
	}
	reg := al.GetRegistry()
	if reg == nil {
		return false
	}
	_, ok := reg.GetAgent(id)
	return ok
}

// wirePlanToolsForAgent constructs and registers the ADR-052 create_plan /
// execute_plan / run_task / inspect_session tool surface, plus the ADR-055
// plan_correct / stop_plan supervision surface, for a single
// agent instance — the single clear wiring site Wave 1 left unwired for
// Wave 2 (pkg/tools/plan.go / run_task.go / inspect_session.go's "another
// wave's job" doc comments name pkg/agent/loop.go explicitly). Called from
// registerSharedTools's per-agent loop (planStore may be nil there on the
// very first pass) and again from SetPlanStore for every already-registered
// agent once the gateway installs the real store.
//
// Every dependency gap is logged LOUDLY (Error, never silently) at wiring
// time so an unwired seam is visible in the boot log — on top of, not
// instead of, each tool's own Wave-1 fail-closed Execute() behavior (nil
// store / nil checker / nil dispatcher => explicit error result, never an
// implicit allow or a silently-dead no-op tool).
// The six tools below (the ADR-052 four, plus ADR-055's plan_correct and
// stop_plan) are registered via RegisterReplacing, not Register:
// this function is called once per agent at registerSharedTools time AND
// again for every already-registered agent from SetPlanStore once the real
// plan.Store is installed (this function's own doc comment) — the second
// pass is an EXPECTED same-name re-registration, not an accidental
// collision, so it must not spam a WARN per tool per agent (7-reviewer gate
// item 5; see ToolRegistry.RegisterReplacing's own doc comment).
func (al *AgentLoop) wirePlanToolsForAgent(agent *AgentInstance, planStore *plan.Store) {
	if agent == nil || agent.Tools == nil {
		return
	}

	if planStore == nil {
		logger.WarnCF("agent", "wirePlanToolsForAgent: plan store not yet installed — "+
			"create_plan/execute_plan register but will fail closed until SetPlanStore runs",
			map[string]any{"agent_id": agent.ID})
	}

	// create_plan (FR-001, US-1): owner_agent_id validated against the live
	// config/registry (SetOwnerValidator — see the field doc on
	// tools.PlanCreateTool for the fail-closed-when-unwired discipline this
	// honors).
	planCreate := tools.NewPlanCreateTool(planStore)
	planCreate.SetHome(al.homePath)
	planCreate.SetOwnerValidator(func(ownerAgentID string) error {
		return validatePlanOwnerAgentForTool(al.GetConfig(), ownerAgentID)
	})
	agent.Tools.RegisterReplacing(planCreate)

	// execute_plan (FR-003/004/030, US-3): SD-A7 tiered-DoD gate's isAgentID
	// checker (SetIsAgentIDChecker) mirrors gateway's restAPI.isAgentID.
	if al.taskStore == nil {
		logger.ErrorCF("agent", "wirePlanToolsForAgent: task store unavailable — "+
			"execute_plan registered but will fail closed (no task store to list members)",
			map[string]any{"agent_id": agent.ID})
	}
	planExecute := tools.NewPlanExecuteTool(planStore, al.taskStore)
	planExecute.SetIsAgentIDChecker(al.isRegisteredAgentID)
	agent.Tools.RegisterReplacing(planExecute)

	// run_task (FR-019, US-10): dispatches via the real
	// TaskExecutor.StartTaskNow — the standalone-task full attempt loop.
	taskRun := tools.NewTaskRunTool(al.taskStore)
	if al.taskExecutor != nil {
		taskRun.SetStartTaskNow(al.taskExecutor.StartTaskNow)
	} else {
		logger.ErrorCF("agent", "wirePlanToolsForAgent: task executor unavailable — "+
			"run_task registered but will fail closed (no dispatcher installed)",
			map[string]any{"agent_id": agent.ID})
	}
	agent.Tools.RegisterReplacing(taskRun)

	// inspect_session (FR-033, US-13 Acceptance 3): verifier-role-only by
	// seeded tool policy (enforced outside this function); target-session
	// locked via the engine-set WithVerifierSessionScope ctx value
	// (verifier_adjudication.go), not by anything wired here.
	//
	// Deliberately NOT wired to al.GetSessionStore() alone: that is only
	// the SHARED store at $OMNIPUS_HOME/sessions/ (new webchat/channel
	// sessions), but a task's own session — the exact thing task-scope
	// verification targets — is created via createTaskSessionSync
	// (task_executor.go), which writes through al.GetAgentStore(t.AgentID):
	// the ASSIGNEE agent's own per-agent legacy store, a DIFFERENT
	// directory. A single fixed store can never cover both. agentLoopInspectSessionStore
	// (below) instead adapts al.ResolveSessionStore — the SAME
	// shared-store-first-then-per-agent-scan resolver cancel.go's
	// RequestCancel already uses to find an arbitrary session id's owning
	// store — so inspect_session finds a target session regardless of
	// which store actually holds it. Always non-nil (a value type over a
	// non-nil al): Execute()'s own not-found error (via GetMeta/
	// ReadTranscript, once VerifierSessionScopeAllows has already passed)
	// is the fail-closed signal, not a nil-store check.
	agent.Tools.RegisterReplacing(tools.NewInspectSessionTool(agentLoopInspectSessionStore{al: al}))

	// --- ADR-055 plan supervision surface: plan_correct + stop_plan --------
	//
	// Both engine hooks are installed as LATE-RESOLVING CLOSURES over
	// GetPlanEngine(al), never as a value captured here. This is load-bearing,
	// not a style choice: the gateway installs the plan engine LAST
	// (gateway.go's SetPlanEngine, after SetPlanStore), and SetPlanEngine only
	// assigns the field — it does not re-run this function. A hook capturing
	// al.planEngine at wiring time would therefore be nil on EVERY pass and
	// both tools would sit permanently in their fail-closed "engine is not
	// wired" branch, in a build where everything looks correctly registered.
	//
	// The closures preserve the fail-closed contract they replace: an absent
	// engine returns an explicit error, so the tool reports a failure and
	// never a silent success. Neither tool's authority is wired here — both
	// gate internally, and deliberately in opposite directions: plan_correct
	// admits only the PlanSupervisor identity (tools.PlanSupervisorAgentID),
	// stop_plan admits only the plan's own owner agent. The adjudicator
	// corrects; the owner contains.
	planCorrect := tools.NewPlanCorrectTool(planStore, al.taskStore)
	planCorrect.SetAppendCorrection(func(
		ctx context.Context, planID string, caller tools.CorrectionCaller, req tools.CorrectionRequest,
	) (string, bool, error) {
		pe := GetPlanEngine(al)
		if pe == nil {
			return "", false, errors.New(
				"plan engine is not installed — corrections cannot be applied")
		}
		// Passed through whole, NOT rebuilt field-by-field. FR-004 moved the
		// correction types into pkg/plan and left tools.CorrectionCaller /
		// agent.CorrectionCaller as type ALIASES of plan.CorrectionCaller
		// (likewise CorrectionRequest), so these are one type, not two that
		// happen to match — the compiler accepts the value directly and the
		// old CorrectionVerb() conversion became a no-op the linter flags.
		//
		// The rebuild also had to go on its own merits: enumerating the
		// fields here means the next field added to plan.CorrectionRequest is
		// silently dropped at this seam, with nothing failing to compile.
		// This branch has shipped that exact shape more than once.
		res, err := pe.AppendCorrection(ctx, planID, caller, req)
		if err != nil {
			return "", false, err
		}
		if res == nil {
			// Defensive: a nil result with a nil error would otherwise be
			// reported to the adjudicator as a successful correction carrying
			// an empty revision id.
			return "", false, errors.New(
				"plan engine returned no correction result")
		}
		return res.RevisionID, res.HonestExit, nil
	})
	agent.Tools.RegisterReplacing(planCorrect)

	// stop_plan (FR-042/FR-043, US-8): the containment control. StopPlan's
	// signature matches StopPlanFunc exactly, so the closure adds only the
	// late engine resolution described above.
	planStop := tools.NewPlanStopTool(planStore)
	planStop.SetStopPlan(func(ctx context.Context, planID, userID, channel string) (*plan.Plan, error) {
		pe := GetPlanEngine(al)
		if pe == nil {
			return nil, errors.New(
				"plan engine is not installed — the plan cannot be stopped")
		}
		return pe.StopPlan(ctx, planID, userID, channel)
	})
	agent.Tools.RegisterReplacing(planStop)
}

// agentLoopInspectSessionStore adapts AgentLoop.ResolveSessionStore to the
// tools.InspectSessionStore interface (GetMeta/ReadTranscript keyed purely
// on session ID — see that interface's own doc comment: "the store resolves
// the owning agent internally"). ResolveSessionStore already implements
// exactly that resolution (shared store fast path, then a scan across every
// per-agent store, cancel.go) — reused
// here rather than re-implemented, so inspect_session and RequestCancel
// agree on where any given session id actually lives.
type agentLoopInspectSessionStore struct {
	al *AgentLoop
}

func (s agentLoopInspectSessionStore) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	store := s.al.ResolveSessionStore(sessionID)
	if store == nil {
		return nil, fmt.Errorf("session %q not found in any known session store", sessionID)
	}
	return store.GetMeta(sessionID)
}

func (s agentLoopInspectSessionStore) ReadTranscript(sessionID string) ([]session.TranscriptEntry, error) {
	store := s.al.ResolveSessionStore(sessionID)
	if store == nil {
		return nil, fmt.Errorf("session %q not found in any known session store", sessionID)
	}
	return store.ReadTranscript(sessionID)
}

// --- list_jobs wiring (the unified background-job roster) -----------------
//
// Every store below is reached through a LATE-RESOLVING adapter rather than a
// value captured at wiring time, because the three stores list_jobs reads are
// installed at three DIFFERENT points in gateway boot, all of which can follow
// this wiring: the task store exists from NewAgentLoop, the plan store arrives
// with SetPlanStore, and the lifecycle store arrives later still with
// SetSessionMessagingStores (which re-wires only the session-messaging tool
// surface, never this one). A captured lifecycle store would be nil forever
// and the `subagent` kind would silently report an empty roster on every call.
//
// Each adapter returns an explicit ERROR when its store is absent — never an
// empty slice. list_jobs turns that into a per-kind error entry, which is the
// whole point: "a short list that looks complete is the worst possible output"
// (NewListJobsTool's own doc). A nil-return adapter would produce exactly the
// silent-undercount failure the tool is built to prevent.
//
// DELIBERATELY NOT IMPLEMENTED HERE: the optional ListLenient siblings
// (tools.jobPlanLenientLister and friends). list_jobs picks those up by
// OPTIONAL type assertion against the value passed to NewListJobsTool — i.e.
// against these adapters. None of pkg/plan, pkg/task or pkg/session implements
// ListLenient yet, so defining a forwarding method here would make the
// assertion succeed while the count it feeds (`unreadable`) is structurally
// always 0 — an honest-looking zero that means "not measured", not "nothing
// was corrupt". The seam is left unsatisfied on purpose so it stays visibly
// unwired. WHEN ListLenient LANDS on the concrete stores, add the matching
// forwarding method to the adapter below; that is the only change needed.
type agentLoopJobPlanLister struct{ al *AgentLoop }

func (l agentLoopJobPlanLister) List(filter plan.Filter) ([]plan.Plan, error) {
	store := l.al.GetPlanStore()
	if store == nil {
		return nil, errors.New("plan store is not installed")
	}
	return store.List(filter)
}

type agentLoopJobTaskLister struct{ al *AgentLoop }

func (l agentLoopJobTaskLister) List(filter task.Filter) ([]task.Task, error) {
	store := GetTaskStore(l.al)
	if store == nil {
		return nil, errors.New("task store is not installed")
	}
	return store.List(filter)
}

type agentLoopJobLifecycleLister struct{ al *AgentLoop }

func (l agentLoopJobLifecycleLister) List(
	filter session.LifecycleFilter,
) ([]session.LifecycleRecord, error) {
	store := l.al.GetSessionLifecycleStore()
	if store == nil {
		return nil, errors.New("session lifecycle store is not installed")
	}
	return store.List(filter)
}

// agentLoopJobAgentNamer resolves a delegated agent's display name for a
// subagent row's label. AgentRegistry.GetAgentName already has exactly this
// contract (name+true when the agent exists, the raw id when its name is
// empty, ("",false) when it does not), so this forwards rather than
// re-deriving. A false return is a NORMAL case: durable lifecycle records
// outlive the agents they name, and the tool falls back to the raw agent id.
type agentLoopJobAgentNamer struct{ al *AgentLoop }

func (n agentLoopJobAgentNamer) AgentDisplayName(agentID string) (string, bool) {
	reg := n.al.GetRegistry()
	if reg == nil {
		return "", false
	}
	return reg.GetAgentName(agentID)
}

// wireJobRosterForAgent registers `list_jobs` for one agent.
//
// Called from registerSharedTools' per-agent loop (so it re-runs on every hot
// reload, picking up the fresh config). It is deliberately NOT called from
// SetPlanStore's re-wire loop the way the plan surface is: the adapters above
// read every store live, so there is nothing for a later pass to re-bind.
//
// Two of this tool's seven setters are left UNWIRED because the
// implementations they need do not exist yet. Each omission degrades honestly
// and is listed here so the gap is visible at the wiring site rather than
// inferred from behaviour:
//
//   - SetCapSnapshotSource — needs PlanEngine.CapSnapshot, a LOCK-FREE reader
//     over values the engine already published from inside its own admission
//     path. It must never be faked from Admit (which takes the engine mutex
//     exclusively and re-scans the plan store) nor re-derived independently.
//     Unwired, the cap fields are omitted as a pair, which is the designed
//     degradation; a fabricated source reporting 0 would be strictly worse
//     than absent.
//   - SetScanCeiling — the config key list_jobs' own doc names
//     (tools.list_jobs.max_records_scanned_per_kind) does not exist in
//     pkg/config. Unwired, the package default (5000/kind) applies and a
//     crossing is still REPORTED via notes.scan_truncated.
//
// SetAuditLogger is intentionally not called either, but for the opposite
// reason: it is already satisfied. ListJobsTool implements the registry's
// auditLoggerAware contract, and ToolRegistry propagates the logger on
// registration and on its own SetAuditLogger — wiring it by hand here would
// duplicate that with a value that goes stale.
func (al *AgentLoop) wireJobRosterForAgent(agent *AgentInstance) {
	if agent == nil || agent.Tools == nil {
		return
	}
	listJobs := tools.NewListJobsTool(
		agentLoopJobPlanLister{al: al},
		agentLoopJobTaskLister{al: al},
		agentLoopJobLifecycleLister{al: al},
	)
	// A LIVE closure, never al.GetConfig()'s value: this function is reached
	// from registerSharedTools, which the hot-reload path runs BEFORE it swaps
	// al.cfg. A value read here is therefore the PRE-reload config on every
	// reload, so the reload that enables tools.filter_sensitive_data would be
	// exactly the one list_jobs missed — it would keep emitting plan and task
	// titles unredacted until some unrelated later reload happened to run.
	// (Boot is unaffected, which is what made this invisible.) The setter takes
	// only a closure so the mistake cannot be re-made here.
	listJobs.SetConfig(func() *config.Config { return al.GetConfig() })
	listJobs.SetAgentNamer(func() tools.JobAgentNamer {
		return agentLoopJobAgentNamer{al: al}
	})
	// SetSessionResolver over THIS agent's own *tools.DelegateTool (now that
	// it implements tools.JobSessionResolver via ResolvableSessionIDs). A
	// LIVE closure, same discipline as SetConfig/SetAgentNamer above — it
	// re-resolves agent.Tools.Get("delegate") on every list_jobs call rather
	// than capturing a value at wiring time, so wiring order relative to the
	// delegate tool's own registration in this same per-agent pass does not
	// matter, and a hot-reload that replaces the delegate tool instance is
	// picked up automatically. A missing or
	// wrong-typed "delegate" registration (an agent with no delegate tool at
	// all) resolves to a nil JobSessionResolver, which collectSubagentRows
	// already treats as "nothing resolves" — the same honest degradation
	// this setter's absence used to produce, not a new failure mode.
	listJobs.SetSessionResolver(func() tools.JobSessionResolver {
		raw, ok := agent.Tools.Get("delegate")
		if !ok {
			return nil
		}
		delegateTool, ok := raw.(*tools.DelegateTool)
		if !ok {
			return nil
		}
		return delegateTool
	})
	// SetLabelResolver over the SAME *tools.DelegateTool (UAT M3, 2026-08-03
	// / #584): DelegateTool now also implements tools.JobLabelResolver via
	// ResolvableLabels, so list_jobs' label_contains filter can match a
	// subagent's custom delegate label instead of only its raw agent/session
	// identifiers. Same live-closure discipline as SetSessionResolver
	// immediately above — re-resolves on every call rather than capturing a
	// value at wiring time — for the identical reasons (wiring-order
	// independence, hot-reload safety, honest nil degradation when no
	// delegate tool is registered).
	listJobs.SetLabelResolver(func() tools.JobLabelResolver {
		raw, ok := agent.Tools.Get("delegate")
		if !ok {
			return nil
		}
		delegateTool, ok := raw.(*tools.DelegateTool)
		if !ok {
			return nil
		}
		return delegateTool
	})
	// RegisterReplacing, not Register: registerSharedTools re-runs on every hot
	// reload, so a same-name re-registration is EXPECTED and must not log a
	// WARN per agent per reload.
	agent.Tools.RegisterReplacing(listJobs)
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
		if s, ok := v.(string); ok {
			return s
		}
		logger.ErrorCF("agent", "channelSessionIdx: invariant violated — unexpected value type, treating as cache miss",
			map[string]any{"key": key, "got_type": fmt.Sprintf("%T", v)})
	}
	title := displayName
	if title == "" {
		title = chatID
	}
	// Persist the instance identity, not just the bare type. The in-memory
	// index above has always keyed on it ("so two instances of the same type
	// do not collide") — the session record did not, so that distinction was
	// lost the moment the process restarted, and anything acting on "this
	// channel's sessions" could not tell a hundred WhatsApp numbers apart.
	meta, err := al.sharedSessionStore.NewChannelSession(channel, indexID, chatID, agentID, title)
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
//
// FIX (silent-corruption gap): each probe below used to accept a store only
// on err == nil, treating "session genuinely doesn't live here"
// (os.ErrNotExist — the frequent, legitimate case for a session that hasn't
// been created yet) and "session lives here but its meta.json is corrupt or
// unreadable" (JSON decode error, I/O error, permission error) identically —
// both fell through to the next probe, and a corrupt hit on the LAST probe
// made the function return nil exactly as if the session never existed.
// Callers (resolveWorkspaceIDForContinuation, ProcessScheduled,
// processMessage's M4 block, processSystemMessage) then proceeded as if the
// session had no workspace bound, silently dropping the binding — and their
// own downstream WARN logging (which re-reads GetMeta expecting to
// distinguish the same two cases) never fired, because they never got a
// non-nil store to call GetMeta on in the first place.
//
// A non-ErrNotExist GetMeta error on a given store is itself strong evidence
// that THIS store owns the session — readUnifiedMeta only reaches the JSON
// decode (or a bare read failure past ENOENT) once sessionDir/meta.json
// exists under that store's baseDir, so there is no reason to keep scanning
// remaining stores for a "real" copy elsewhere: continuing to probe would
// either find nothing (wasted work, same nil-after-corruption outcome) or,
// in the pathological case of a duplicate session ID across stores, mask the
// corruption behind an unrelated hit. So on a non-ErrNotExist error this
// still returns the store immediately (log first) rather than falling
// through — that is what makes the store reach the caller at all, which is
// what makes the four downstream WARNs reachable. An out-of-scope
// alternative — changing this function's signature to return the error too
// — was rejected: it would ripple into pkg/gateway/websocket.go and
// pkg/gateway/rest.go call sites outside this agent's edit scope for no
// benefit over logging here and letting callers' own GetMeta re-read see the
// identical error.
func (al *AgentLoop) ResolveSessionStore(sessionID string) *session.UnifiedStore {
	// Fast path: shared store owns new sessions.
	if al.sharedSessionStore != nil {
		if _, err := al.sharedSessionStore.GetMeta(sessionID); err == nil {
			return al.sharedSessionStore
		} else if !errors.Is(err, os.ErrNotExist) {
			logger.WarnCF(
				"agent",
				"ResolveSessionStore: session meta unreadable (not a missing-session case); returning owning store despite read failure",
				map[string]any{"session_id": sessionID, "store": al.sharedSessionStore.BaseDir(), "error": err.Error()},
			)
			return al.sharedSessionStore
		}
	}
	// Slow path: scan all per-agent stores. The former "legacy fast path"
	// here special-cased the retired "main" sentinel agent (which used to own
	// most old sessions); with the sentinel removed there is no reserved
	// agent ID to fast-path or skip, so every registered agent is scanned
	// uniformly.
	for _, id := range al.GetRegistry().ListAgentIDs() {
		store := al.GetAgentStore(id)
		if store == nil {
			continue
		}
		if _, err := store.GetMeta(sessionID); err == nil {
			return store
		} else if !errors.Is(err, os.ErrNotExist) {
			logger.WarnCF(
				"agent",
				"ResolveSessionStore: session meta unreadable (not a missing-session case); returning owning store despite read failure",
				map[string]any{"session_id": sessionID, "store": store.BaseDir(), "error": err.Error()},
			)
			return store
		}
	}
	return nil
}

// ListAllSessions returns a stably-ordered, paginated window of sessions from
// the shared store merged with legacy per-agent stores, deduplicated
// (ADR-057 FR-092/FR-098, W16b, owner U9 — the loop layer of the four-layer
// pagination stack FR-068/FR-092 requires: UnifiedStore.ListSessions (U6) ->
// AgentLoop.ListAllSessions (here) -> restAPI.listSessions (U18) ->
// fetchSessions (U12)).
//
// Ordering and pagination contract (FR-098, stated once here as this
// method's owner):
//
//   - (a) The merged sequence is ordered by UpdatedAt descending with the
//     session id as a stable tiebreak, so two sessions sharing a timestamp
//     cannot silently swap places between two calls with no intervening
//     write — mirroring UnifiedStore.ListSessions' own post-FR-097a
//     ordering exactly (pkg/session/unified.go).
//   - (b) Paging is offset-based over that merged sequence. limit <= 0 means
//     "no limit" — the remainder of the sequence from offset is returned in
//     one page, matching UnifiedStore.ListSessionsPage's contract. offset <
//     0 is treated as 0. An offset at or beyond the end of the sequence
//     returns an empty, non-nil page with NextOffset == -1, not an error. A
//     cursor built from NextOffset stays valid for the duration of a
//     client's expansion even if a store's contents change in between — a
//     shifted window is acceptable, a duplicated or skipped row within one
//     already-served page is not, because this method never re-derives an
//     already-returned row's position from anything but that same total
//     order.
//   - (c) A legacy per-agent store that errors mid-merge contributes zero
//     rows, is appended to the returned errs, and does NOT halt the page or
//     invalidate the cursor — the merge simply continues over the remaining
//     stores (unchanged from this method's pre-pagination behavior; see the
//     loop below). Callers should surface these as partial_errors rather
//     than treating the whole response as a failure.
//
// Hierarchy (FR-091/FR-104) is applied BEFORE pagination, over the full
// merged set, so a page boundary can never split a parent from its
// child-count context or silently promote/demote a row depending on which
// page it lands on:
//
//   - flat == true: no hierarchy filter — every merged, deduplicated session
//     is a candidate row (FR-104's per-session usage-accounting listing).
//     parentSessionID is ignored in this combination; the 400 for supplying
//     both is a REST-layer concern (U18), not this method's.
//   - flat == false, parentSessionID != "": only that session's DIRECT
//     children (meta.ParentSessionID == parentSessionID) are returned.
//   - flat == false, parentSessionID == "": only ROOT sessions are returned
//     — meta.ParentSessionID == "", OR meta.ParentSessionID names a session
//     absent from this merge (an orphan; FR-091: "a session whose
//     ParentSessionID names a session that no longer resolves MUST be
//     returned as a root"). Orphan detection is computed across the WHOLE
//     merged id set rather than by delegating to UnifiedStore.IsOrphan
//     (which is scoped to one store's own metaCache) because this method
//     spans multiple stores — in practice every delegated child lives in
//     the one shared store per FR-010, but resolving membership against the
//     full merge is correct regardless of that placement detail.
func (al *AgentLoop) ListAllSessions(limit, offset int, parentSessionID string, flat bool) (session.SessionListPage, []error) {
	var all []*session.UnifiedMeta
	var errs []error

	// 1. Shared store (new sessions).
	sharedIDs := make(map[string]bool)
	allIDs := make(map[string]bool)
	if al.sharedSessionStore != nil {
		shared, err := al.sharedSessionStore.ListSessions()
		if err != nil {
			logger.WarnCF("agent", "ListAllSessions: could not list shared sessions",
				map[string]any{"error": err.Error()})
			errs = append(errs, fmt.Errorf("shared: %w", err))
		} else {
			for _, s := range shared {
				sharedIDs[s.ID] = true
				allIDs[s.ID] = true
				all = append(all, s)
			}
		}
	}

	// 2. Legacy per-agent stores — deduplicate against shared. FR-098(c): a
	// store that errors here contributes zero rows and the merge continues.
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
				allIDs[s.ID] = true
				all = append(all, s)
			}
		}
	}

	all = u9FilterSessionHierarchy(all, allIDs, parentSessionID, flat)

	// FR-098(a): stable total order.
	sort.Slice(all, func(i, j int) bool { return u9SessionRecencyLess(all[i], all[j]) })

	// FR-098(b): offset-based paging over the merged, filtered, sorted
	// sequence — mirrors UnifiedStore.ListSessionsPage's contract exactly.
	if offset < 0 {
		offset = 0
	}
	total := len(all)
	if offset >= total {
		return session.SessionListPage{Sessions: []*session.UnifiedMeta{}, NextOffset: -1, Total: total}, errs
	}
	end := total
	if limit > 0 && offset+limit < total {
		end = offset + limit
	}
	nextOffset := -1
	if end < total {
		nextOffset = end
	}
	page := make([]*session.UnifiedMeta, end-offset)
	copy(page, all[offset:end])
	return session.SessionListPage{Sessions: page, NextOffset: nextOffset, Total: total}, errs
}

// u9FilterSessionHierarchy applies FR-091/FR-104's hierarchy rule to a
// merged, deduplicated session list, given the full set of ids present in
// that same merge (allIDs) so the orphan clause of FR-091 ("a session whose
// ParentSessionID names a session that no longer resolves MUST be returned
// as a root") is evaluated across every store ListAllSessions merged, not
// just one. See ListAllSessions' doc comment for the three cases.
func u9FilterSessionHierarchy(all []*session.UnifiedMeta, allIDs map[string]bool, parentSessionID string, flat bool) []*session.UnifiedMeta {
	if flat {
		return all
	}
	filtered := make([]*session.UnifiedMeta, 0, len(all))
	if parentSessionID != "" {
		for _, s := range all {
			if s.ParentSessionID == parentSessionID {
				filtered = append(filtered, s)
			}
		}
		return filtered
	}
	for _, s := range all {
		if s.ParentSessionID == "" || !allIDs[s.ParentSessionID] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// u9SessionRecencyLess is ListAllSessions' FR-098(a) total order, factored
// into a named, directly-testable function rather than left as an inline
// sort.Slice closure: UpdatedAt descending, with session id as a stable
// tiebreak so two sessions sharing a timestamp (down to whatever resolution
// the clock gives) cannot silently swap places between two calls with no
// intervening write. Mirrors UnifiedStore.ListSessions' own post-FR-097a
// comparator (pkg/session/unified.go) exactly, at the layer that merges
// ACROSS stores rather than within one.
func u9SessionRecencyLess(a, b *session.UnifiedMeta) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID < b.ID
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
		SendResponse:           false,
		TranscriptSessionID:    taskChatID,
		TranscriptStore:        al.GetAgentStore(agentID),
		InitialDelegationDepth: delegationDepth,
		IsTaskRun:              true,
		// WorkspaceID is already on taskCtx via tools.WithWorkspaceID (the task
		// executor sets it on ctx before calling processTaskDirect — see
		// runTask/runTaskFromInProgress's tools.WithWorkspaceID(ctx, t.WorkspaceID)
		// calls in task_executor.go); thread it through processOptions
		// explicitly too, mirroring processTaskDirectExternalCLI's identical
		// field below, so runTurn's re-root block (loop.go ~6428) resolves the
		// work dir from ts.opts.WorkspaceID via FindForAgentPreferring rather
		// than falling through to workspace.FindForAgent's arbitrary
		// sort.Strings(matches)[0] pick when the agent belongs to 2+
		// workspaces. Without this, a native task run silently rooted in the
		// WRONG workspace whenever the assigned agent had more than one
		// CoreTeam membership — this field reads ts.opts, not the context, so
		// leaving it unset here (while the external-CLI sibling below sets it)
		// was the gap.
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
	// Calling Finish here is safe to add: at THIS point (construction, right
	// before registerActiveTurn ran above) ts.cancelFunc/ts.providerCancel
	// are still nil — cancel-propagation FIX 1 (external_dispatch.go's
	// runExternalCLISubTurn) only wires them once dispatch actually starts,
	// below. By the time this function returns and the deferred Finish call
	// below actually RUNS, those fields are typically non-nil (set to the
	// dispatch's own context.CancelFunc) — see this function's top doc
	// comment's STALE-COMMENT CORRECTION note for the full explanation. That
	// does not change this safety argument: Finish's cancelFunc branch
	// (`if ts.cancelFunc != nil { ts.cancelFunc() }`) simply invokes it,
	// which is exactly what dispatchCancel's own `defer dispatchCancel()`
	// below already guarantees happens — canceling an already-canceled
	// context is a no-op, so calling it twice (once via that defer, once via
	// Finish) is harmless. Below (FIX 2), this call passes
	// ts.hardAbortRequested() rather than a hardcoded false, so the
	// child-cascade branch CAN run here when a hard abort was requested — but
	// that is also safe: Finish's closeOnce.Do + the
	// cancelFired-swap-then-nil-check around onCancelFinish make ANY repeated
	// Finish call (e.g. a concurrent InterruptSessionHard elsewhere calling
	// requestHardAbort on this same ts via steering.go, whether or not this
	// site's own call also cascades) idempotent — the identical safety
	// runTurn's own deferred Finish call already relies on for the
	// hard-abort-then-deferred-Finish sequence (loop.go, "closeOnce.Do
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
	// Finish run LAST, exactly like runTurn's own Finish/clearActiveTurn pair.
	//
	// FIX 2: call Finish with ts.hardAbortRequested(), not a hardcoded false.
	// InterruptSessionHard (the session-wide web-cancel escalation path;
	// steering.go) hard-aborts a turn by calling ts.requestHardAbort() alone —
	// it never calls ts.Finish(true) itself (only the legacy single-session
	// HardAbort()/InterruptHard do that). For a turn hard-aborted that way,
	// THIS deferred call is the only Finish call that will ever happen, so a
	// hardcoded false silently mislabeled a genuine hard abort as a graceful
	// finish — wrong for the cancelFired-gated onCancelFinish callback
	// (cancel.go's RequestCancel), which threads its "graceful"/"hard"
	// cancelMethod straight into the persisted turn_canceled transcript entry
	// that pkg/gateway/replay.go renders back to the user as
	// "Turn canceled (%s)". Must be wrapped in a closure: a bare
	// `defer ts.Finish(ts.hardAbortRequested())` would evaluate
	// hardAbortRequested() immediately at THIS defer statement (Go evaluates
	// deferred arguments at registration time, not at call time) — i.e.
	// always false, reproducing the exact bug this fixes.
	defer func() { ts.Finish(ts.hardAbortRequested()) }()
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
	// not just an agent-to-agent delegate call. An empty soul (a soul-less
	// custom agent — a seeded worker's compiled prompt is non-empty as of
	// the RC-6 fix, coreagent's "worker" prompts-map entry) yields
	// task-only input, identical to the pre-fix behavior.
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
	sysToolList := systools.AllTools(deps)
	for _, agentID := range registry.ListAgentIDs() {
		ag, ok := registry.GetAgent(agentID)
		if !ok || ag == nil || ag.Tools == nil {
			continue
		}
		for _, t := range sysToolList {
			ag.Tools.RegisterReplacing(t)
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
	// This deliberately does NOT mark the reload pending. Only the reload
	// function itself can, because only it knows whether a reload was actually
	// queued — and it sets the flag while holding the same mutex that clears it
	// (pkg/gateway's services.beginReload / finishReload), which is what makes
	// "request registered" and "flag set" atomic against a concurrently
	// finishing reload.
	//
	// Setting it here instead was a real defect. TriggerReload cannot register
	// the request until it calls reloadFunc, so a flag set beforehand sits in a
	// window where a finishing cycle sees no registered request, clears the
	// flag, and releases the slot; the caller's poller then returns immediately
	// against a config snapshot predating its own write. It also made the flag
	// LIE whenever reloadFunc completed nothing — every test fake that returns
	// nil without running a reload left the flag stuck on, so callers polling
	// it blocked for the full deadline against a reload that was never coming.
	if err := al.reloadFunc(); err != nil {
		// Only clear the pending flag if this was a genuine failure.
		// If another reload is already in progress, that reload owns the flag —
		// clearing it here would prematurely unblock any concurrent poller.
		// Defensive/test-only in practice: the production reloadFunc coalesces
		// instead of reporting contention. See ErrReloadAlreadyInProgress.
		if strings.Contains(err.Error(), "already in progress") {
			return ErrReloadAlreadyInProgress
		}
		al.reloadPending.Store(false)
		return err
	}
	return nil
}

// MarkReloadPending marks a config reload as pending — the flag
// restAPI.triggerReloadAndWait polls, and the one ClearReloadPending clears.
//
// This is the ONLY way the flag gets set, and it is called from exactly one
// place in production: pkg/gateway's services.beginReload, while it holds the
// mutex that finishReload/abandonReload clear under. That placement is the
// whole design — it makes "a reload is queued" and "the flag is set" the same
// atomic fact, so the flag can never be set for a reload that will not run, nor
// cleared out from under a request that has just been registered.
//
// Callers other than the reload bookkeeping should not use it; a flag set
// without a queued reload blocks every poller until the wait deadline expires.
//
// Idempotent, safe to call repeatedly and concurrently.
func (al *AgentLoop) MarkReloadPending() {
	al.reloadPending.Store(true)
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

	// ADR-066 D4 / FR-015: the user-message bound, at the one point where an
	// inbound message becomes a turn — BEFORE routing mints a channel session,
	// before the transcript write below, before turn registration. Over the
	// bound, the reply is returned as this message's ordinary response: the
	// caller (session worker / unroutable path) publishes it on the
	// originating channel like any assistant reply — no error frame, no
	// transcript entry, no turn id. Media refs ride in msg.Media and are not
	// counted. See user_message_bound.go.
	if reply, refused := al.refuseOversizedUserMessage(msg); refused {
		logger.InfoCF("agent", "Refused oversized user message before turn start (ADR-066 D4)",
			map[string]any{
				"channel":     msg.Channel,
				"chat_id":     msg.ChatID,
				"size_chars":  UserMessageChars(msg.Content),
				"bound_chars": al.UserMessageBound(),
			})
		return reply, nil, nil
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
		// FIX 1 (re-review of the re-review): distinguish a real meta-read
		// failure from "no workspace bound" — see
		// resolveWorkspaceIDForContinuation's doc comment (above,
		// ~line 3099) for the full rationale.
		if meta, mErr := transcriptStore.GetMeta(transcriptSessionID); mErr != nil {
			if !errors.Is(mErr, os.ErrNotExist) {
				logger.WarnCF("agent", "could not read session meta while resolving workspace; workspace unresolved",
					map[string]any{"session_id": transcriptSessionID, "error": mErr.Error()})
			}
		} else if meta != nil {
			workspaceID = meta.WorkspaceID
		}
	}
	if workspaceID == "" {
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
		if instanceID := inboundInstanceID(msg); instanceID != "" {
			if cfg := al.GetConfig(); cfg != nil {
				if inst, ok := cfg.Channels[instanceID]; ok && inst.WorkspaceID != "" {
					workspaceID = inst.WorkspaceID
				}
			}
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
		UserInitiated:       userInitiated(msg),
		UserMessage:         msg.Content,
		Media:               msg.Media,
		DefaultResponse:     defaultResponse,
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
			// ADR-066 D5.5 (FR-045): this emptiness condition is unchanged;
			// hydration itself now refuses to touch an agent archive that
			// already has lines, so a window that is empty only because Skip
			// reached the end of a non-empty archive is never rebuilt.
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

	// ADR-074 D4a reply routing (judgment-first spec US-3 S9): when this
	// session carries a pending goal state (compiled-awaiting-confirmation or
	// awaiting a clarification answer), a BARE chat message may be the confirm
	// token or the clarification answer. The hook answers synchronously
	// (handled=true), rewrites the turn into round 1 on a fresh-goal confirm
	// (handled=false + opts.UserMessage), or passes an ordinary message
	// through untouched — a routine chat message never silently mutates goal
	// state.
	if goalHandled, goalReply := al.applyGoalPendingReply(ctx, msg, agent, &opts); goalHandled {
		return goalReply, agent, nil
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
				curStr, curOK := "", false
				cur, ok := al.sessionActiveAgent.Load(sessionScopeKey(msg))
				if ok {
					curStr, curOK = cur.(string)
					if !curOK {
						logger.ErrorCF("agent", "sessionActiveAgent: invariant violated — unexpected value type, clearing stale entry",
							map[string]any{"session_id": msg.SessionID, "got_type": fmt.Sprintf("%T", cur)})
					}
				}
				if !ok || !curOK || curStr != explicitID {
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
			agentID, agentIDOK := activeAgent.(string)
			if !agentIDOK {
				logger.ErrorCF("agent", "sessionActiveAgent: invariant violated — unexpected value type, clearing stale entry",
					map[string]any{"session_id": msg.SessionID, "got_type": fmt.Sprintf("%T", activeAgent)})
				al.sessionActiveAgent.Delete(scopeKey)
			}
			if agentIDOK && agentID != "" {
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
		// FIX 1 (re-review of the re-review): distinguish a real meta-read
		// failure from "no workspace bound" — see
		// resolveWorkspaceIDForContinuation's doc comment (above,
		// ~line 3099) for the full rationale.
		if meta, mErr := transcriptStore.GetMeta(transcriptSessionID); mErr != nil {
			if !errors.Is(mErr, os.ErrNotExist) {
				logger.WarnCF("agent",
					"delegate-completion: could not read session meta while resolving workspace; workspace unresolved",
					map[string]any{"session_id": transcriptSessionID, "error": mErr.Error()})
			}
		} else if meta != nil {
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

	// ADR-049 D6/D7 (US-8): judge-gated /goal round advance. Fast no-op
	// unless opts.TranscriptSessionID's session carries an active goal; may
	// append a steering follow-up to result.followUps, published by the loop
	// immediately below exactly like any other follow-up.
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
						SessionID:         string(ts.routingSessionID),
					},
					map[string]any{"retry_after_seconds": result.RetryAfterSeconds},
				)
				turnStatus = TurnEndStatusError
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
		if cfg.Tools.Manifest.Compressed {
			providerToolDefs = al.buildCompressedToolDefs(ts, policyFilteredTools)
		} else {
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
			// system message immediately after the system prompt. Empty/absent
			// instructions are a no-op — zero behavioral change.
			//
			// Ordering note (finding 10c, context-audit 2026-08 — this comment
			// previously claimed "workspace instructions land at [2]", which
			// stopped being true once the web-rendering note was added between
			// this call and injectManifestNote below): all three of these
			// injectors (this one, injectWebRenderingNote, injectManifestNote)
			// insert at index 1 of the message array, so call order alone
			// determines final position — the LAST call ends up CLOSEST to the
			// system message. With every note present this turn, final order is:
			// [0] system prompt · [1] manifest note · [2] web-rendering note ·
			// [3] workspace instructions · [4] scratchpad (spliced above, before
			// this block) · [5+] history. See injectWorkspaceInstructions' own
			// doc comment (workspace_instructions.go) for the authoritative,
			// single-sourced version of this contract.
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
					resp, streamErr := sp.ChatStream(providerCtx, messagesForCall, toolDefsForCall, llmModel, llmOpts, func(accumulated string) {
						// B4: if the turn has been abandoned (stuck-goroutine detach),
						// suppress further frame emits so a zombie goroutine cannot
						// push frames to disconnected clients.
						if ts.abandoned.Load() {
							abandonedWritesSuppressed.Add(1)
							return
						}
						// Send only the new delta (accumulated minus what we already sent).
						//
						// Defensive: this slice panics with index-out-of-range if a
						// provider ever emits an accumulated string SHORTER than its
						// predecessor. The contract is monotonic growth, but a provider
						// bug, a block reorder, or an SDK revision changing accumulation
						// semantics would otherwise take down the whole turn. Treat a
						// non-growing value as "nothing new" and skip it.
						if len(accumulated) < len(lastChunk) {
							logger.DebugCF("agent", "Streaming callback emitted a shorter accumulated string; ignoring", map[string]any{
								"previous_len": len(lastChunk),
								"new_len":      len(accumulated),
							})
							return
						}
						delta := accumulated[len(lastChunk):]
						lastChunk = accumulated
						if delta != "" {
							if err := streamer.Update(providerCtx, delta); err != nil {
								logger.DebugCF("agent", "Streaming update error (client may have disconnected)", map[string]any{"error": err.Error()})
							}
						}
					}, onToolCallProgress)
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
			// when no FailoverError is in the chain), route through the
			// shared translateLLMError, and surface the generic message
			// instead of the raw provider text. Raw stays in logger.ErrorCF
			// + the wrapped fmt.Errorf return for operator triage.
			pe := errorToProviderError(err)
			llm := TranslateLLMError(pe, err.Error())

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

		// ADR-053 Phase-2 / D12 (R§8.3d/FR-171/FR-172/FR-173): debit the ONE
		// app-level OVERALL token pool from provider-reported usage. Agent-agnostic
		// by design (no agentType arg) — the IsPrivilegedAgent exemption is removed
		// so core-agent turns debit the same pool. Atomic RMW under one lock; the
		// graceful-wind-down gate (Exhausted) is consulted at the next turn/
		// adjudication boundary, NEVER mid-turn (FR-174). The debit is unconditional
		// even when unbounded (cap 0) so Usage accounting stays correct.
		//
		// TokenBudget is the sole app-level spend brake; see pkg/agent/budget.go (D12 / R§8.3).
		if response != nil && response.Usage != nil {
			callCost := estimateLLMCallCost(llmModel, response.Usage)
			if al.tokenBudget != nil && response.Usage.TotalTokens > 0 {
				al.tokenBudget.Debit(int64(response.Usage.TotalTokens))
			}
			// Accumulate turn-level stats so the "done" WS frame can surface
			// real token counts and cost to the chat UI (issue #12).
			ts.AddTurnStats(int64(response.Usage.TotalTokens), callCost)
			// Accumulate cache token split for transcript entry (Wave 1 token tracking).
			ts.AddTurnCacheStats(response.Usage.CacheReadTokens, response.Usage.CacheWriteTokens)
			// Accumulate the input/output split. The provider reports it and
			// estimateLLMCallCost above already consumes it, but until this
			// call existed it was dropped here — AddTurnStats carries only the
			// collapsed total — so session stats could never report tokens_in.
			ts.AddTurnIOStats(response.Usage.PromptTokens, response.Usage.CompletionTokens)
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
				// to the assistant / bus / transcript.
				pe := errorToProviderError(err)
				llm := TranslateLLMError(pe, err.Error())

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
		for i, tc := range normalizedToolCalls {
			if ts.hardAbortRequested() {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts, "tool_loop", hardInterruptAbortReason)
			}

			// Unsanitize tool name from LLM — dots were replaced with underscores
			// for Anthropic/Azure API compatibility (e.g., "browser_navigate" → "browser.navigate").
			toolName := ts.agent.Tools.UnsanitizeToolName(tc.Name)
			toolArgs := cloneStringAnyMap(tc.Arguments)

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
					// registry's timeout (300 s by default — see
					// pkg/gateway/approvals.go). Record the call as `pending`
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

			// UAT fix (fix/uat-defects-2026-08-22, Defect 1): update this
			// exact call's consecutive-failure streak. A success (or a hook
			// that turned a failure into one) clears the streak outright; a
			// real failure bumps it and, once it crosses either threshold,
			// augments the error content the model is about to see (warn),
			// or trips the pre-dispatch breaker above for every later call
			// with this identical signature this turn (hard stop). Keyed on
			// toolCBSig computed before dispatch/hooks so a hook renaming the
			// tool does not fragment the streak it is meant to track.
			if toolResult.IsError {
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
	if !hasActiveStreamer && finalContent != "" {
		ts.appendAssistantTranscript(finalContent)
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

	al.emitEvent(
		EventKindError,
		ts.eventMeta("runTurn", "turn.error"),
		ErrorPayload{
			Stage:     "llm",
			ChatID:    ts.opts.ChatID,
			Code:      string(llm.Code),
			Message:   llm.Message,
			SessionID: string(ts.routingSessionID),
		},
	)
	ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
	level("agent", "Turn exited: "+string(code), map[string]any{
		"agent_id":  ts.agent.ID,
		"iteration": iteration,
		"model":     llmModel,
		"code":      string(code),
		"cause":     cause.Error(),
	})
	return turnResult{status: status}, status, fmt.Errorf("%w: %w", sentinel, cause)
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
// Callers pass fresh history (post-trim when called after windowTrim), the
// current user message + media, and active skill names. The recall span is
// read from al.recallSpans for the given sessionKey.
func (al *AgentLoop) assembleMessages(
	ctx context.Context,
	ts *turnState,
	history []providers.Message,
	userMsg string,
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
		// ADR-066 FR-019: apply the persisted projection state so the
		// window the provider sees here (turn start, post-trim, reload) is
		// byte-identical to what the choke point / the D5 emptying pass
		// produced live. Pure; no-op when nothing was capped or emptied.
		if pm := ts.agent.Sessions.Projection(ts.sessionKey); len(pm.Entries) > 0 {
			var cs config.ContextSettings
			if cfg := al.GetConfig(); cfg != nil {
				cs = cfg.Context
			}
			history = projectMessages(history, archiveLineResolver(archive, history), pm.Entries, projectionContext{
				policy:  capPolicyFor(cs, agentContextBudget(ts.agent)),
				archive: archive,
			})
		}
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
	span := al.activeRecallSpan(ts.sessionKey)
	// ADR-066 D5.4 (FR-043): this from-scratch assembly includes the active
	// span exactly once (BuildMessages, after the pinned core). Record it by
	// identity so the tool-result site never splices it a second time, and
	// so a same-turn replacement (E20) can find the block to remove.
	recordAssembledRecallSpan(ts, span, history)
	return ts.agent.ContextBuilder.BuildMessages(
		history,
		userMsg,
		media,
		ts.opts.WorkspaceID,
		ts.channel,
		ts.chatID,
		ts.opts.SenderID,
		ts.opts.SenderDisplayName,
		breadcrumb,
		span.Messages(),
		skillNames...,
	)
}

// sentToolSurfaceTokens estimates the tokens the tool surface ACTUALLY costs on
// the wire for this agent+session — the non-evictable overhead history has to
// fit around.
//
// This is deliberately NOT agent.Tools.ToProviderDefs(): that returns a full
// JSON schema for EVERY registered tool, but under a compressed manifest only
// three groups are sent (buildCompressedToolDefs, tool_manifest.go):
//
//   - ManifestFull  — full schema, every turn
//   - ManifestInfra — full schema (ToolSearch is always callable)
//   - ManifestLazy  — full schema ONLY while loaded this session; otherwise it
//     is one line in the compact manifest block, ~25x cheaper
//
// Charging every lazy tool a full schema made the budget shrink with the size
// of the CATALOG rather than the size of the REQUEST. With ~15 MCP servers
// connected (150-450 lazy tools, none of them sent) the over-count exceeds the
// whole context window, driving the history budget negative: the trimmer would
// evict every turn, still not fit, and stop at its FR-003 floor keeping only
// the last user message — silently, on every turn. TestSwitchTime_EndToEnd
// caught the small-scale version of this at a 20k window.
//
// The manifest block is MEASURED via the same builder the turn uses, not
// approximated by a per-entry constant, so the two cannot drift.
//
// When the compressed manifest is off every tool really is sent, so the whole
// registry is the correct answer and we fall back to it.
//
// transcriptID/sessionKey are the same two inputs manifestBucketKey takes
// everywhere else (ADR-071 D3 §4.6) — callers that have a *turnState in
// scope MUST pass ts.opts.TranscriptSessionID and ts.sessionKey (or, more
// directly, thread ts.manifestBucket() down to whichever caller owns this
// call). Passing a bare sessionKey with no agentID/transcriptID component
// (the pre-fix bug here) can never match a bucket written by markToolsLoaded,
// so the lookup below always saw an empty loaded set.
func (al *AgentLoop) sentToolSurfaceTokens(agent *AgentInstance, transcriptID, sessionKey string) int {
	if agent == nil || agent.Tools == nil {
		return 0
	}
	all := agent.Tools.GetAll()

	cfg := al.GetConfig()
	if cfg == nil || !cfg.Tools.Manifest.Compressed {
		// Uncompressed: every tool is sent as a full def.
		return estimateToolDefsTokens(agent.Tools.ToProviderDefs())
	}

	bucket := manifestBucketKey(agent.ID, transcriptID, sessionKey)
	loaded := al.sessionLoadedTools(bucket)

	sent := make([]tools.Tool, 0, len(all))
	for _, t := range all {
		switch tools.ToolManifestTier(t.Name()) {
		case tools.ManifestFull, tools.ManifestInfra:
			sent = append(sent, t)
		case tools.ManifestLazy:
			if loaded[t.Name()] {
				sent = append(sent, t)
			}
		}
	}

	total := estimateToolDefsTokens(tools.ToolsToProviderDefs(sent))
	// The compact block for the lazy tools that are NOT loaded. Measured with
	// the real builder: same input shape the turn passes, same filtering.
	if note := tools.BuildCompressedManifest(all, loaded); note != "" {
		total += estimateMessageTokens(providers.Message{Role: "system", Content: note})
	}

	// Loud, non-fatal: if the surface alone rivals the window, the trimmer will
	// bottom out on its floor and the agent will look like it lost its memory
	// for no visible reason. Name the numbers so that is diagnosable from logs.
	if window := agent.ContextWindow; window > 0 && total > window/2 {
		logger.WarnCF("agent", "tool definitions occupy over half the context window; "+
			"history retention will be severely reduced", map[string]any{
			"agent_id":          agent.ID,
			"tool_surface_toks": total,
			"context_window":    window,
			"tools_registered":  len(all),
			"tools_sent":        len(sent),
		})
	}
	return total
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
//
// The tool-surface term is what the turn ACTUALLY SENDS, not the whole
// registry — see sentToolSurfaceTokens.
//
// transcriptID is forwarded to sentToolSurfaceTokens unchanged — pass
// ts.opts.TranscriptSessionID when a *turnState is in scope, "" otherwise
// (manifestBucketKey then falls back to sessionKey alone for the loaded-tool
// bucket, same as an agent with no transcript session).
func (al *AgentLoop) windowTrim(agent *AgentInstance, transcriptID, sessionKey string) (compressionResult, bool) {
	return al.windowTrimForce(agent, transcriptID, sessionKey, false)
}

// windowTrimForce is windowTrim with the "the window already fits, do
// nothing" guard optionally disabled.
//
// force=false (windowTrim, every proactive caller): a caller that measured
// the budget itself and decided to trim can be WRONG — the pre-turn check
// historically charged the whole tool registry while this function charges
// only the sent surface — and without the guard a mis-fired call silently
// evicted the oldest turn every turn.
//
// force=true: the PROVIDER rejected the request with its own context error.
// Our estimate said it fit and the provider says otherwise, so the estimate
// is what is wrong; refusing to trim here would leave the retry identical to
// the call that just failed. This is the documented reactive fallback for
// "the estimate undershoots reality".
func (al *AgentLoop) windowTrimForce(
	agent *AgentInstance, transcriptID, sessionKey string, force bool,
) (compressionResult, bool) {
	if agent.budgetChecksExempt() {
		// FR-005: an exempt provider manages its own context; there is no
		// budget to fit against, so there is nothing to trim.
		return compressionResult{NothingToTrim: true}, false
	}
	window := agent.Sessions.GetHistory(sessionKey)
	if len(window) <= 1 {
		// Nothing to evict: a single-message window cannot be shrunk further.
		return compressionResult{NothingToTrim: true}, false
	}

	toolDefsTokens := al.sentToolSurfaceTokens(agent, transcriptID, sessionKey)

	// ADR-066 FR-019: measure the window AS THE PROVIDER SEES IT. GetHistory
	// returns the archive's raw tail; results the choke point capped or an
	// earlier pass emptied are projected only at assembly. Counting their
	// full content here would over-evict (and, on the floor path, re-empty
	// results that are already marks). One archive read serves the
	// projection, the floor-path emptying below and the M5 stat.
	archive, archErr := agent.Sessions.ReadArchive(context.Background(), sessionKey)
	if archErr != nil {
		logger.DebugCF("agent", "windowTrim: ReadArchive failed; window measured unprojected, no floor emptying",
			map[string]any{"session_key": sessionKey, "error": archErr.Error()})
	}
	measured := window
	lineOf := func(int) int { return -1 }
	if archErr == nil {
		lineOf = archiveLineResolver(archive, window)
		if pm := agent.Sessions.Projection(sessionKey); len(pm.Entries) > 0 {
			var cs config.ContextSettings
			if cfg := al.GetConfig(); cfg != nil {
				cs = cfg.Context
			}
			measured = projectMessages(window, lineOf, pm.Entries, projectionContext{
				policy:  capPolicyFor(cs, agentContextBudget(agent)),
				archive: archive,
			})
		}
	}

	// Recall span tokens — updated after a potential drop below.
	recallSpan := al.activeRecallSpan(sessionKey)
	recallSpanTokens := 0
	if recallSpan != nil {
		recallSpanTokens = recallSpan.Tokens
	}

	// The ONE budget B (ADR-066 FR-028): W − max_tokens − ceil(0.05·W) −
	// pinnedCoreOverhead, resolved by the same helper the pre-turn and
	// timeout-recovery checks call, so the suffix fit-check below and the
	// checks that decide to invoke it can never disagree. The 5 % headroom
	// keeps a just-trimmed window from re-trimming on the very next turn;
	// the pinned-core term (M3 fix) is what stops under-eviction on
	// small-window models — the system prompt and breadcrumb are sent every
	// turn but are not part of `window`.
	budget := agentContextBudget(agent)

	// FR-019 drop-span-first: if an active span exists and we're over budget,
	// drop it and re-check. Only evict real window Turns if still over budget.
	currentWindowTokens := sumMessageTokens(measured)

	// Nothing to do: the window already fits. Without this, a caller that
	// mis-fired (historically the pre-turn check, which charged the whole
	// tool registry while this function charges only the sent surface) would
	// still reach the boundary walk below, take the first non-zero boundary
	// whose suffix fits, and evict the oldest turn — every turn, silently,
	// on a conversation that never came close to the budget.
	//
	// Skipped under force: there the provider itself rejected the request, so
	// it is our estimate that is wrong, not the window.
	if !force && currentWindowTokens+toolDefsTokens+recallSpanTokens <= budget {
		return compressionResult{NothingToTrim: true, RemainingMessages: len(window)}, false
	}

	if recallSpan != nil && (currentWindowTokens+toolDefsTokens+recallSpanTokens > budget) {
		al.dropRecallSpan(sessionKey, "pressure")
		recallSpanTokens = 0
		// Re-check against the same budget B used for the suffix fit-check
		// below. Using the raw window here would pass cases that the suffix
		// walk would still reject, causing unnecessary evictions on the next call.
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
		suffix := measured[b:]
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
	var emptiedCount int
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
		if droppedCount > 0 {
			agent.Sessions.TruncateHistory(sessionKey, keepLast)
		}

		// ADR-066 D5, register #3 / B-21b (FR-017): the floor kept an
		// oversized turn whole. Its tool results — every one whose call is
		// in the kept slice, EXCEPT the floor set (the results of its last
		// assistant step) — are emptied oldest-first until the kept turn
		// fits B, or none is left. Skip does not move again: this is an
		// empty, not a cut. The pass persists (id, line) → emptied; the
		// caller's post-trim assembleMessages re-applies it, so the
		// in-memory copy mutated here is only the fit measurement.
		if archErr == nil {
			kept := append([]providers.Message(nil), measured[keepStart:]...)
			keptLineOf := func(i int) int { return lineOf(keepStart + i) }
			fits := func(m []providers.Message) bool {
				return sumMessageTokens(m)+toolDefsTokens+recallSpanTokens <= budget
			}
			emptiedCount = len(al.emptyInPlace(
				al.getActiveTurnState(sessionKey), agent, sessionKey, kept, keptLineOf, archive, fits, emptyingSitePreTurn))
		}
	}

	if droppedCount == 0 && emptiedCount == 0 {
		// The window is already a single turn whose results are all in the
		// floor set (or already marks): nothing this site may do. Not an
		// error — D6's clamp keeps that set under B (CRIT-002).
		return compressionResult{NothingToTrim: true, RemainingMessages: len(window)}, false
	}

	if saveErr := agent.Sessions.Save(sessionKey); saveErr != nil {
		logger.ErrorCF("agent", "windowTrim: failed to persist trimmed session",
			map[string]any{"session_key": sessionKey, "error": saveErr.Error()})
	}

	// M4 fix: verify the window actually shrank after the TruncateHistory call.
	// The backends' TruncateHistory is fire-and-forget (errors are logged, not
	// returned). Re-read GetHistory and compare: if the window is the same size
	// as before, the trim silently failed — log the error and return ok=false so
	// the caller does not misreport a successful eviction. An empty-only pass
	// (droppedCount == 0) shrinks bytes, not the message count, so it is
	// exempt from the count check.
	postWindow := agent.Sessions.GetHistory(sessionKey)
	if droppedCount > 0 && len(postWindow) >= len(window) {
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

	// M5 / FR-018: emit context_archive_lines from the archive read above.
	// Eviction never deletes bytes from the JSONL file, only advances Skip,
	// and emptying never touches it either (ADR-028 / ADR-066 B-23), so the
	// pre-trim read is the post-trim truth. Actual byte stat requires
	// fs.Stat — not exposed through SessionStore; the line count is the
	// proxy observable alongside the real skip value. -1 = unavailable.
	archiveBytes := int64(-1)
	if archErr == nil {
		archiveBytes = int64(len(archive))
	}

	logger.WarnCF("agent", "windowTrim: evicted oldest Turns from live window",
		map[string]any{
			"session_key":            sessionKey,
			"turns_evicted":          droppedCount,
			"results_emptied":        emptiedCount,
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
	transcriptID string,
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

	// ADR-066 D2: the new model's window comes from the ONE resolver, keyed
	// by the new primary's (provider, model) and this agent's id — the same
	// call NewAgentInstance and ApplyAgentModel make, so the switch-time
	// re-window, the pre-turn trim and the mid-turn check compare against
	// one budget B (B-06). An unresolvable model keeps the agent's current
	// window (the "unknown model = no-op switch" behaviour — ApplyAgentModel
	// below will fail and the turn continues on the previous model), but
	// the miss MUST be surfaced: discarding it would let a typo'd
	// `metadata.model_name` silently route the next call through the
	// agent's PRIMARY model — the exact FR-007 failure mode.
	newContextWindow, _, _ := agent.windowSnapshot()
	al.mu.RLock()
	cfg := al.cfg
	al.mu.RUnlock()
	if cfg != nil {
		if modelCfg, resolveErr := ResolveModelCfg(cfg, newModel, agent.Home); resolveErr != nil {
			logger.WarnCF("agent", "handleModelSwitch: requested model did not resolve; keeping the current window",
				map[string]any{
					"requested_model": newModel,
					"agent_id":        agent.ID,
					"resolve_error":   resolveErr.Error(),
				})
		} else {
			candidates := resolveModelCandidatesForAgent(cfg, cfg.Agents.Defaults.DefaultModel.Provider, modelCfg.Model, agent)
			windowProvider, windowModel := primaryWindowPair(candidates, cfg.Agents.Defaults.DefaultModel.Provider, modelCfg.Model)
			newContextWindow = ResolveWindow(cfg, windowProvider, windowModel, agent.ID).Window
		}
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
	// An exempt or unknown new window (0) is a Noop: nothing to trim against.
	action := decideSwitchCompressAction(currentConvTokens, newContextWindow)
	if action == SwitchActionCompress {
		// Temporarily set the agent's context window to the new model's window
		// so windowTrim computes the correct budget. We restore it after the
		// trim; ApplyAgentModel (below) sets the canonical value under the
		// instance lock together with the rest of the model identity.
		agent.mu.Lock()
		oldContextWindow := agent.ContextWindow
		agent.ContextWindow = newContextWindow
		agent.mu.Unlock()

		if _, trimOK := al.windowTrim(agent, transcriptID, sessionKey); !trimOK {
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

		agent.mu.Lock()
		agent.ContextWindow = oldContextWindow
		agent.mu.Unlock()
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
	// Tools and skills are install-wide facts that every agent sees the same
	// way — skills in particular load from ONE global directory
	// ($OMNIPUS_HOME/skills; see globalSkillsDir). Reading them "through the
	// default agent" was only ever a convenient handle, and it silently became
	// a dependency on a default EXISTING once the "main" sentinel was removed
	// (ADR-064): with no default, this returned an empty map, which made
	// restAPI.installedSkillIDs empty, which made validateSkillIDs skip
	// validation entirely and ACCEPT unknown skill ids. A fail-open reached
	// through three layers of indirection.
	//
	// Any agent answers these questions identically, so ask any.
	agent := registry.GetDefaultAgent()
	if agent == nil {
		for _, id := range registry.ListAgentIDs() {
			if ag, ok := registry.GetAgent(id); ok && ag != nil {
				agent = ag
				break
			}
		}
	}
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

	// /goal and /loop (ADR-049 D6, Gap #8/r2 origin gating) — checked before
	// registered-command dispatch so a matched verb can answer synchronously
	// (status/clear/stop) or rewrite the turn (goal set) exactly like the
	// hooks above. A non-user-initiated turn's "/goal"/"/loop" text is NOT
	// matched by either hook and falls through to normal dispatch/passthrough.
	if matched, handled, reply := al.applyGoalCommandPrompt(ctx, msg, agent, opts); matched {
		return reply, handled
	}
	if matched, handled, reply := al.applyLoopCommandPrompt(ctx, msg, agent, opts); matched {
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

// activeSkillNames returns the skills active for THIS turn only — never the
// agent's full grant list (ADR-072 D1/D3: skills are loaded on demand via the
// Skill tool, not force-injected into every turn's context).
//
// Before ADR-072, this unioned agent.SkillsFilter (the agent's ENTIRE
// per-agent grant list, agentCfg.Skills) with opts.ForcedSkills every single
// message — the exact force-load mechanism the on-demand Skill tool
// (pkg/tools/skill.go) replaces. A turn's active skills are now only what was
// explicitly loaded this turn: via opts.ForcedSkills, which the Skill tool's
// "load" outcome and the pre-existing /<slug> slash-command
// (applyExplicitSkillCommand) and delegate's requested_skill (D9,
// spawnSubTurn's ForcedSkills append) all populate one-shot, per turn — never
// via the agent's static grant list, which only gates WHICH skills may be
// loaded (skillAllowed/D5), not which ones are.
func activeSkillNames(agent *AgentInstance, opts processOptions) []string {
	if agent == nil {
		return nil
	}

	if len(opts.ForcedSkills) == 0 {
		return nil
	}

	var resolved []string
	seen := make(map[string]struct{}, len(opts.ForcedSkills))
	for _, name := range opts.ForcedSkills {
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
// Every agent is given the remember / recall_memory / recall_conversation /
// run_retrospective tools now (pkg/agent/instance.go and this package's
// agent.Tools.Register calls register them unconditionally — the retired
// "main" sentinel used to be excluded by a hardcoded identity check, which
// went away with the sentinel). Whether an agent can actually use one is
// governed by its own tool policy like any other tool; the steering prompt
// still degrades gracefully if a policy denies it — the model simply reports
// it doesn't have that capability instead of the turn erroring.
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
			return m, resolvedCandidateProvider(c, cfg.Agents.Defaults.DefaultModel.Provider)
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

			return clearSessionWindow(agent.Sessions, opts.SessionKey)
		}
	}

	// Inject the session ID accessor so /cancel can target the current session.
	if opts != nil {
		sessionKey := opts.SessionKey
		rt.SessionID = func() string { return sessionKey }
	}

	// Inject the agent loop so CancelActiveTurn can call
	// RequestCancelForSession (ADR-057 FR-100/FR-041: InterruptSession, the
	// symbol this comment used to name, was retired by U8's collapse of the
	// four legacy interrupt entry points behind Interrupt/InterruptSessionHard
	// plus a mandatory InterruptScope; CancelActiveTurn's own call has always
	// gone through RequestCancelForSession, pkg/commands/runtime.go, never
	// direct to an Interrupt* function).
	rt = rt.WithAgentLoop(al)

	return rt
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
// effective policy for toolName using FilterToolsByPolicy. Returns "deny" if
// the tool is not found in the agent's registered tools or has no policy
// entry on either side.
//
// The unified `ToolSearch` infra tool used to get an unconditional
// registration-gated force-allow here (bypassing FilterToolsByPolicy
// entirely) because no seeded agent named it in its own tool-policy override
// map — a CLAUDE.md hard-constraint-6 violation. ToolSearch is now seeded
// "allow" as real, explicit data for every agent (pkg/coreagent/core.go), so
// it resolves correctly through the same FilterToolsByPolicy call as every
// other tool below; the force-allow shortcut has been removed.
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
// Identity (ADR-057 FR-031/FR-080, W10b — corrected from the pre-ADR-057
// description this comment used to carry): sessionID MUST be the caller's
// own ACTING session id — turnState.transcriptSessionID — NOT the
// session-store scope key (turnState.sessionKey) and NOT the ROUTING
// identity (turnState.routingSessionID, W4). This is a narrower requirement
// than the pre-ADR-057 invariant it replaces: before D1, a delegated child's
// transcriptSessionID was always threaded through unchanged from its parent
// (subturn.go's spawnSubTurn), so "the one identity shared across a
// delegation chain" and "the child's own identity" were the same value and
// this distinction did not exist. Under ADR-057 the child gets its OWN
// distinct, store-backed transcriptSessionID (FR-005/FR-007/FR-009), and
// ApprovalGrantStore.InheritFrom (pkg/security/approvalgrants.go, U17a)
// copies grants at spawn time INTO exactly that child key — {dstSessionID:
// childID, dstAgentID} — never into the routing/root id. A grant read here
// keyed on anything other than the calling turn's own transcriptSessionID
// (in particular, keying on routingSessionID, which for a grandchild equals
// the ROOT's session id) would silently miss every grant InheritFrom wrote
// for THIS turn and force a real human through the 300s interactive
// approval wait on every delegated call — the exact failure class FR-031's
// two-key InheritFrom redesign exists to prevent on the write side; this is
// its read-side half (see pkg/security/approvalgrants_adr057_test.go for the
// write side, TestCheckGrantOrRequestApproval_UsesActingSessionKey below for
// this one). ClearSession (session teardown, U17b) uses the same key for the
// same reason: it is the acting session's own bucket, not a shared one.
func (al *AgentLoop) CheckGrantOrRequestApproval(
	ctx context.Context,
	sessionID, agentID, toolName, toolCallID, turnID string,
	args map[string]any,
) (approved bool, denialReason string) {
	if al.ApprovalGrants().IsAllowed(sessionID, agentID, toolName, args) {
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

// forgetSession removes every (agent, session) bucket belonging to sessionID
// from the loaded-tool map and its pendingSearchPromotions/bucketTurnCounter
// siblings, preventing unbounded memory growth. Called from CloseSession with
// the transcript sessionID — the same value manifestBucketKey's session
// component derives from.
//
// ADR-071 D3 §4.6 point 4: since loadedTools is now keyed by
// manifestBucketKey(agentID, transcriptID, sessionKey) — a composite key —
// an exact-match `delete(al.loadedTools, sessionID)` would match nothing and
// silently reintroduce the unbounded growth this function exists to prevent.
// This is now a suffix sweep for every key ending in
// manifestBucketKeySep+sessionID, mirroring the O(n) recallSpans scan two
// lines below (same justification: session close is a cold path, not the hot
// turn path).
//
// §4.3.1(a) r5: the same sweep MUST cover pendingSearchPromotions too — it is
// not swept by virtue of sharing loadedToolsMu (the mutex protects the maps,
// it does not enumerate them). Every entry found there is, by definition, a
// promotion abandoned before its follow-up horizon elapsed (the session is
// closing), so it is tallied and counted via
// tools.RecordToolSearchNoFollowUp() — count-then-delete in the same critical
// section, and the recorder call made AFTER releasing the lock so no
// cross-package call happens under it.
//
// Safe for concurrent access — protected by loadedToolsMu. No-op for the
// empty key.
func (al *AgentLoop) forgetSession(sessionID string) {
	if sessionID == "" {
		return
	}
	suffix := manifestBucketKeySep + sessionID

	al.loadedToolsMu.Lock()
	for key := range al.loadedTools {
		if key == sessionID || strings.HasSuffix(key, suffix) {
			delete(al.loadedTools, key)
		}
	}
	var abandonedPromotions int
	for key, pending := range al.pendingSearchPromotions {
		if key == sessionID || strings.HasSuffix(key, suffix) {
			abandonedPromotions += len(pending)
			delete(al.pendingSearchPromotions, key)
		}
	}
	for key := range al.bucketTurnCounter {
		if key == sessionID || strings.HasSuffix(key, suffix) {
			delete(al.bucketTurnCounter, key)
		}
	}
	al.loadedToolsMu.Unlock()

	for i := 0; i < abandonedPromotions; i++ {
		tools.RecordToolSearchNoFollowUp()
	}

	// MINOR fix: clean up recall spans for this session so recallSpans sync.Map
	// does not grow without bound as sessions are closed (FR-019). The span key
	// is ts.sessionKey ("agent:<agentID>:session:<sessionID>") while forgetSession
	// receives only the transcript sessionID. We scan the map for any key that
	// contains the sessionID as a suffix so we delete spans regardless of agentID.
	// The scan is safe here because forgetSession is on the session-close path
	// (not the hot turn path), so the O(n) Range is acceptable.
	recallSuffix := ":session:" + sessionID
	al.recallSpans.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok {
			if key == sessionID || strings.HasSuffix(key, recallSuffix) {
				al.recallSpans.Delete(key)
			}
		}
		return true
	})
}

// searchPromotionHorizonTurns is the number of turns a query-path ToolSearch
// promotion may sit unused before it counts toward
// omnipus_toolsearch_no_followup_total (ADR-071 §4.3.1a, FR-038).
//
// This is a NEW, INDEPENDENT literal — it MUST NOT be derived from, coupled
// to, or merged with cfg.Tools.MCP.Discovery.TTL, whose default is also 5.
// The equal value is a coincidence of two separate choices: the MCP TTL
// decides when an externally-provided tool stops being callable; this
// horizon decides only when an unused static discovery is COUNTED, and
// withdraws nothing from anyone. They are also not operator-equivalent — the
// MCP TTL is operator-configurable and an operator who has tuned it away
// from 5 must not see this horizon move with it. Conflating the two is the
// exact defect ADR-071 §1.1.1 records as its own worst mistake.
const searchPromotionHorizonTurns = 5

// recordPendingSearchPromotions records each newly query-path-promoted name
// in names against bucket's current turn index, for later no-followup
// detection (ADR-071 §4.3.1a). Only ever called from the markLoaded closure
// when tools.IsSearchPromotion(ctx) is true — an exact-name `names` load
// must never reach here (FR-038a). No-op for an empty bucket or names.
// Safe for concurrent access — protected by loadedToolsMu.
func (al *AgentLoop) recordPendingSearchPromotions(bucket string, names []string) {
	if bucket == "" || len(names) == 0 {
		return
	}
	al.loadedToolsMu.Lock()
	defer al.loadedToolsMu.Unlock()
	if al.pendingSearchPromotions[bucket] == nil {
		al.pendingSearchPromotions[bucket] = make(map[string]int, len(names))
	}
	turn := al.bucketTurnCounter[bucket]
	for _, n := range names {
		al.pendingSearchPromotions[bucket][n] = turn
	}
}

// clearPendingSearchPromotion deletes bucket's pending-discovery record for
// name, if any, because it is about to be invoked (ADR-071 §4.3.1a "Clear").
// Called from the tool-dispatch site on every call regardless of tier — a
// no-op map delete when there is no pending entry for name.
// Safe for concurrent access — protected by loadedToolsMu.
func (al *AgentLoop) clearPendingSearchPromotion(bucket, name string) {
	if bucket == "" || name == "" {
		return
	}
	al.loadedToolsMu.Lock()
	defer al.loadedToolsMu.Unlock()
	if pending := al.pendingSearchPromotions[bucket]; pending != nil {
		delete(pending, name)
	}
}

// tickSearchPromotionHorizon advances bucket's turn counter by one and
// sweeps its pendingSearchPromotions entries for staleness (ADR-071
// §4.3.1a). Called exactly once per real conversational turn — after
// runTurn's turnLoop for-loop naturally exits, deliberately NOT inside that
// loop (unlike the unrelated per-round ts.agent.Tools.TickTTL() call it used
// to sit next to) — one existing call site. Any entry whose
// recorded turn index is more than searchPromotionHorizonTurns turns old
// increments omnipus_toolsearch_no_followup_total exactly once and is
// deleted (deleting on fire is what makes it fire exactly once per wasted
// promotion, not every turn thereafter). Purely observational: nothing is
// evicted from loadedTools, nothing changes about which tools are callable.
// No-op for the empty bucket.
func (al *AgentLoop) tickSearchPromotionHorizon(bucket string) {
	if bucket == "" {
		return
	}
	al.loadedToolsMu.Lock()
	al.bucketTurnCounter[bucket]++
	current := al.bucketTurnCounter[bucket]
	pending := al.pendingSearchPromotions[bucket]
	var stale int
	for name, recordedTurn := range pending {
		if current-recordedTurn > searchPromotionHorizonTurns {
			delete(pending, name)
			stale++
		}
	}
	al.loadedToolsMu.Unlock()

	for i := 0; i < stale; i++ {
		tools.RecordToolSearchNoFollowUp()
	}
}

// ChannelOwnership returns the stored resolver, or nil before wiring.
func (al *AgentLoop) ChannelOwnership() tools.ChannelOwnership {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.channelOwnership
}

// SetChannelOwnership installs the channel-ownership resolver on every
// registered agent's send_message tool (ADR-065).
//
// It is injected AFTER construction, following the SetPlanStore precedent,
// because the resolver reads live gateway config and pkg/agent cannot import
// pkg/gateway without a cycle. Until it runs, send_message has no ownership
// information: it will reply into the turn's own conversation and REFUSE any
// other target rather than assume the send is allowed. That is deliberate —
// an unresolvable ownership question must not read as permission.
func (al *AgentLoop) SetChannelOwnership(o tools.ChannelOwnership) {
	// STORE it, not just push it. registerSharedTools builds a FRESH
	// MessageTool per agent on every reload — and it re-runs on agent
	// create/update, tool-policy writes, god-mode toggles and mailbox changes
	// via ReloadProviderAndConfig and UpsertAgentFast. Pushing only into the
	// instances that exist right now meant every one of those silently reset
	// ownership to nil for the rest of the process lifetime. Because the tool
	// is fail-closed that degraded into "refuses every target except the
	// turn's own conversation" rather than a hole, but it was permanent and
	// silent. This mirrors what SetPlanStore actually does: al.planStore is
	// stored and re-read at registration.
	al.mu.Lock()
	al.channelOwnership = o
	al.mu.Unlock()

	if o == nil {
		logger.ErrorCF("agent", "SetChannelOwnership: installed a nil resolver — "+
			"send_message will refuse every target except the turn's own conversation", nil)
		return
	}
	reg := al.GetRegistry()
	if reg == nil {
		logger.WarnCF("agent", "SetChannelOwnership: no registry yet; ownership not installed", nil)
		return
	}
	installed := 0
	for _, id := range reg.ListAgentIDs() {
		ag, ok := reg.GetAgent(id)
		if !ok || ag == nil || ag.Tools == nil {
			continue
		}
		t, found := ag.Tools.Get("send_message")
		if !found {
			continue
		}
		mt, isMessageTool := t.(*tools.MessageTool)
		if !isMessageTool {
			continue
		}
		mt.SetChannelOwnership(o)
		installed++
	}
	logger.InfoCF("agent", "channel ownership installed on send_message",
		map[string]any{"agents": installed})
}
