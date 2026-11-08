// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway implements the Omnipus HTTP/WS API gateway: REST handlers
// (rest*.go), WebSocket streaming (websocket*.go), the embedded SPA
// (go:embed), auth (auth.go/rest_auth.go), and the credential/service boot
// sequence (this file's Run/RunWithOptions/RunContext) that wires config,
// credentials, the agent loop, and the channel manager together and starts
// the gateway.port listener (ADR-044: /preview/ shares this same listener —
// there is no separate preview_port anymore). REST handlers that persist
// config always go through safeUpdateConfigJSON (never config.SaveConfig)
// so encrypted credential references in config.json are never clobbered.
package gateway

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	runtimedebug "runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	_ "github.com/elicify-ai/omnipus/pkg/channels/dingtalk"
	_ "github.com/elicify-ai/omnipus/pkg/channels/discord"
	_ "github.com/elicify-ai/omnipus/pkg/channels/feishu"
	_ "github.com/elicify-ai/omnipus/pkg/channels/googlechat"
	_ "github.com/elicify-ai/omnipus/pkg/channels/irc"
	_ "github.com/elicify-ai/omnipus/pkg/channels/line"
	_ "github.com/elicify-ai/omnipus/pkg/channels/qq"
	_ "github.com/elicify-ai/omnipus/pkg/channels/slack"
	_ "github.com/elicify-ai/omnipus/pkg/channels/telegram"
	_ "github.com/elicify-ai/omnipus/pkg/channels/wecom"
	_ "github.com/elicify-ai/omnipus/pkg/channels/weixin"
	_ "github.com/elicify-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/heartbeat"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/steer"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

const (
	serviceShutdownTimeout  = 30 * time.Second
	providerReloadTimeout   = 30 * time.Second
	gracefulShutdownTimeout = 15 * time.Second

	// defaultToolApprovalTimeout bounds how long an `ask`-gated tool call
	// waits for a human before the approval registry fails it CLOSED (denied,
	// reason "timeout") — never open. Applies only when
	// `gateway.tool_approval_timeout` is unset; operators override it there or
	// via OMNIPUS_TOOL_APPROVAL_TIMEOUT.
	//
	// Raised 300s -> 600s (issue #594). Five minutes was too short for a human
	// who stepped away, and the expiry is indistinguishable to the agent from
	// a real denial: a live UAT saw an agent re-request the same denied tool
	// every ~5 minutes, once per expiry window, with no ceiling — the turn
	// never terminated on its own. Ten minutes is a more honest "nobody is
	// coming" signal. Note this only lengthens each window; it does NOT bound
	// the retry loop, which is the other half of #594 and is fixed separately.
	defaultToolApprovalTimeout = 600 * time.Second

	logPath   = "logs"
	panicFile = "gateway_panic.log"
	logFile   = "gateway.log"
)

// configSelfWriteRegistry tracks sha256 hashes of config.json contents that
// were written by the app itself (via safeUpdateConfigJSON). The config file
// watcher consults this to distinguish internal writes from genuine external
// operator edits, suppressing spurious full-service reloads on every login,
// settings change, or channel config write.
//
// Concurrency: all methods are safe for concurrent use. The mu protects the
// hashes map. The poller goroutine and HTTP handler goroutines access this
// concurrently.
type configSelfWriteRegistry struct {
	mu     sync.Mutex
	hashes map[[32]byte]struct{}
}

// register records a sha256 hash as an app-initiated write. Called by
// safeUpdateConfigJSON immediately after a successful atomic write.
func (r *configSelfWriteRegistry) register(h [32]byte) {
	r.mu.Lock()
	r.hashes[h] = struct{}{}
	r.mu.Unlock()
}

// consume checks whether h is a known app-initiated write. If it is, the
// entry is removed and true is returned. The entire set is also cleared of
// any older accumulated hashes (the file can only have one current content,
// so any hash that is not the current one is stale). Returns false for
// hashes that were not registered (external edits).
func (r *configSelfWriteRegistry) consume(h [32]byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, known := r.hashes[h]
	// Always clear all accumulated hashes on any check: older hashes from
	// previous writes can never match the current file content again.
	r.hashes = make(map[[32]byte]struct{})
	return known
}

// selfHealWriteHook builds a config.SelfHealWriteHook that registers the
// sha256 hash of any bytes config.loadConfigInternal's on-disk self-heal
// (migrateCLITokenOutOfUsers, cli_token_migration.go) writes with reg, so
// setupConfigWatcherPolling's next tick recognizes that write as
// app-initiated rather than a genuine external edit (and does not trigger a
// spurious full-service reload).
//
// Used at the two call sites that invoke config.LoadConfigWithStore directly
// — the watcher's own external-edit path and the manual /reload trigger —
// which, unlike safeUpdateConfigJSON, do not otherwise register anything
// with reg. Safe to pass a nil reg (e.g. hot-reload disabled, no registry
// constructed): the returned hook is a no-op in that case.
func selfHealWriteHook(reg *configSelfWriteRegistry) config.SelfHealWriteHook {
	return func(writtenBytes []byte) {
		if reg == nil {
			return
		}
		reg.register(sha256.Sum256(writtenBytes))
	}
}

type services struct {
	// ensureWorkspaceMains provisions a workspace team's mains (final-gate H2);
	// set at boot once the REST API exists, read when the system tools are wired.
	ensureWorkspaceMains func(workspaceID string) error
	// stopSessionForDelete stops one session's tree before the delete_agent cascade
	// deletes it; non-empty means it did not stop.
	stopSessionForDelete func(sessionID string) string
	CronService          *cron.CronService
	// LiveLimits is ADR-066 rung 4; retained so shutdown can Close it (abort
	// in-flight fetches, forbid cache writes) — see agent.LiveLimits.Close.
	LiveLimits *agent.LiveLimits
	// catalogRefreshCancel / catalogRefreshDone are startCatalogRefreshLoop's
	// handles; shutdown cancels, then waits on done (bounded).
	catalogRefreshCancel context.CancelFunc
	catalogRefreshDone   <-chan struct{}
	// bootBackgroundCancel / bootBackgroundDone are the handles of the
	// boot-stage writer goroutines initializeAgentLoop starts (the browser
	// cache-trim schedule, the managed-Chromium preprovision download and the
	// unknown-window primer). runContextWithOptions.startServices attaches
	// them here once services exists; they are nil on services objects built
	// by callers that never ran initializeAgentLoop (every
	// setupAndStartServices harness) — every consumer is nil-safe. Both
	// shutdown drains cancel and then join them: an unjoined preprovision
	// download kept writing browser/chromium/*.zip.part-* into the test home
	// past every cleanup and raced t.TempDir removal
	// (#1644/#1695 — the TempDir-cleanup family).
	bootBackgroundCancel context.CancelFunc
	bootBackgroundDone   <-chan struct{}
	// orphanGCDone joins startBackgroundServices' media-library orphan-GC
	// goroutine, whose FIRST act at boot is repairLibraryAssociationsOnce —
	// a writer under workspaces/ (#1598's "workspaces: directory not
	// empty"). orphanGCCancel is the goroutine's own cancel handle (its
	// context is a child of the boot ctx): both shutdown drains call it
	// BEFORE waiting on done, because on a partial-boot abort or a test
	// harness teardown the parent ctx stays live and a join-only drain would
	// wait out its whole budget while the goroutine kept running (CHECK 1
	// finding F2, 2026-11-08).
	orphanGCDone   chan struct{}
	orphanGCCancel context.CancelFunc
	TaskTrigger    *agent.TaskTriggerScheduler // fires once/every/recurring task triggers via a dedicated CronService
	// TaskDrain owns the queued-task (`next` → dispatch) poll unconditionally,
	// independent of which heartbeat path is active. The now-removed global
	// HeartbeatService was skipped whenever a per-agent heartbeat was active,
	// which meant `next` tasks never dispatched on those installs; TaskDrain is
	// decoupled from that path so `next` tasks always dispatch.
	TaskDrain *heartbeat.TaskDrainService
	// MailWatch owns the new-mail watcher poll (badge state only). Like TaskDrain
	// it is decoupled from the HEARTBEAT.md path so email work surfaces on the
	// Board regardless of which heartbeat path is active. Nil when no mailbox is
	// configured (the scanner is a no-op).
	MailWatch  *heartbeat.MailWatchService
	MediaStore media.MediaStore
	// PlanEngine is the single hybrid plan-coordinator instance (ADR-049 D4,
	// Wave 2-B). Constructed once at boot alongside planStore (both are
	// process-lifetime singletons — a hot reload Stop()s/Start()s the SAME
	// instance rather than reconstructing it, mirroring how taskStore/
	// taskExecutor are stable across reload). Nil only if plan-engine
	// construction failed at boot (a fatal error today — see
	// setupAndStartServices).
	PlanEngine *agent.PlanEngine
	// LoopScheduler is the dedicated `/loop` time-driven scheduler (ADR-049
	// D6/D7, Wave 2-C2), mirroring TaskTrigger's own dedicated-CronService
	// pattern above. Constructed and Start()'d alongside TaskTrigger.
	LoopScheduler *agent.LoopScheduler
	// notifStore backs schedule-failure notifications and the header
	// notification center (#264). Created once at boot, reused across reloads.
	notifStore *notifications.Store
	// ChannelManager is read-only to HTTP handlers (they access it via the
	// agent loop's GetChannelManager). It is written only during executeReload,
	// which is single-flighted by the reloading atomic.Bool. No handler reads
	// this field directly, so no additional lock is required for read access.
	ChannelManager *channels.Manager
	// browserWS is the live-browser panel WS handler (browser_ws.go),
	// kept here so boot can reach the capture registry it owns AFTER
	// setupAndStartServices returns — that is where the boot-time capture
	// warm-up (startBrowserWarmBoot) lives, because it needs this gateway's
	// own listener to already be accepting.
	browserWS        *BrowserWSHandler
	HealthServer     *health.Server
	manualReloadChan chan struct{}
	// reloadCoalesceMu guards reloadInFlight and reloadRequested. Together they
	// single-flight config reloads AND coalesce the requests that arrive while
	// one is already running, instead of dropping them.
	//
	// RELEASE BLOCKER this replaced: reloadInFlight used to be a bare
	// atomic.Bool, and a failed CompareAndSwap made reloadTrigger return
	// "reload already in progress" — the request was DROPPED, and nothing ever
	// re-queued it. The in-memory AgentRegistry is rebuilt by exactly one path
	// (executeReload → restartServices → ReloadProviderAndConfig →
	// NewAgentRegistry; AgentRegistry.UpsertAgent has no production callers), so
	// a dropped request left an agent that POST /agents had already persisted
	// (and answered 201 for) permanently invisible to AgentRegistry.GetAgent.
	// The fingerprint: POST /plans accepted that agent (validatePlanOwnerAgent
	// reads cfg.Agents.List, which SwapConfig had already updated) while
	// POST /tasks rejected it as `agent "x" not found` (validateTaskAgentID
	// reads the registry). Under load a reload takes tens of seconds instead of
	// sub-second, so every concurrent create landed mid-reload and was silently
	// dropped — deterministic, 9/9 in CI.
	//
	// reloadRequested is the coalescing flag: set when a reload is requested
	// while one is in flight. The owning cycle then runs an ADDITIONAL reload
	// with a config re-read from disk, because the in-flight reload's config
	// snapshot was taken before the requester's write and therefore cannot
	// serve it.
	reloadCoalesceMu sync.Mutex
	reloadInFlight   bool
	reloadRequested  bool
	reloadTrigger    func() error
	// reloadOutcome records each reload's rebuild outcome (runReloadCycle);
	// shared with restAPI so PUT /performance can tell a failed rebuild from
	// an applied one. Nil in test constructions (a nil tracker is a no-op).
	reloadOutcome *reloadOutcomeTracker
	// lastAppliedConfigReadSeq is reloadOutcomeTracker.markConfigRead's
	// return value for the config a swap-time re-read
	// (gateway_reload.go::reloadConfigForSwap) most recently produced
	// INSIDE THE CURRENT reload cycle's exec call — the config
	// handleConfigReload actually applies (its caller assigns
	// `newCfg = swapCfg` immediately after reloadConfigForSwap returns, and
	// every step after that — createStartupProvider,
	// al.ReloadProviderAndConfig, restartServices — acts on that swapped-in
	// config, never on the pre-swap snapshot). Round-6 finding: without this
	// field, runReloadCycle told reloadOutcomeTracker.finish the reload
	// cycle's OUTER readSeq (the `first`/loadNext read taken before
	// handleConfigReload ran), which can predate a write that landed on
	// disk between that outer read and the swap-time re-read — so a config
	// that WAS actually applied, and WAS read after a pending-apply mark,
	// reported as though its provenance predated that mark, and
	// performancePendingApply.clearAfterReload withheld a clear it should
	// have granted.
	//
	// runReloadCycle resets this to 0 immediately before each exec call and
	// reads it back immediately after (gateway_reload.go);
	// reloadConfigForSwap sets it only once its swap-time re-read has fully
	// passed prepareReloadConfig. Both happen on the same goroutine within
	// one exec call — the single-flight reload slot (beginReload/
	// finishReload) serializes every reload cycle process-wide — so no
	// additional lock is required. Zero (no swap-time re-read ran this exec
	// call: loadConfigForSwap is unset, which is every test that never
	// wires it, and the manual-reload path before this cycle's own
	// handleConfigReload runs) tells runReloadCycle to keep the cycle's own
	// outer readSeq unchanged.
	lastAppliedConfigReadSeq uint64
	credStore                *credentials.Store
	// toolStore owns the on-disk tool-result offload directory. Exposed here
	// so RunContext can wire its retentionSweep into the nightly sweep loop.
	toolStore *toolResultStore
	// sandboxResult is the Apply/Install outcome from boot. Populated by
	// applySandbox before services start (and before any HTTP listener
	// binds). Read-only after initialization — sandbox config has no
	// hot-reload path, so this never changes for the process lifetime.
	sandboxResult *SandboxApplyResult
	// stopNagBanner cancels the permissive / production-off nag goroutine
	// on shutdown. No-op when no banner was armed.
	stopNagBanner func()
	// bundle is read-only to HTTP handlers (channels receive secrets at
	// construction time via SecretBundle; handlers do not access bundle
	// directly). Written only during executeReload under the reloading
	// single-flight guard. No additional lock is required for read access.
	bundle credentials.SecretBundle
	// bundleCaptureVersion is the channel manager's credential-write version
	// stamped immediately before bundle was resolved (channels spec FR-050
	// (ii)); written and read with bundle. 0 = unstamped (boot).
	bundleCaptureVersion uint64

	// reloadDegraded is set to true when a config reload fails and the service
	// is running on the last successfully loaded config. Cleared on next
	// successful reload. Protected by reloadMu.
	reloadMu       sync.Mutex
	reloadDegraded bool
	reloadError    error

	// Tier 1/3 preview-tool registries. Created once at boot; closed on shutdown.
	// servedSubdirs is always non-nil after a successful boot with preview enabled.
	// devServers is non-nil on the same condition.
	// egressProxy is non-nil when sandbox.EgressAllowList is non-empty or egress
	// enforcement is enabled.
	servedSubdirs *agent.ServedSubdirs
	devServers    *sandbox.DevServerRegistry
	egressProxy   *sandbox.EgressProxy

	// restAPIRef holds a pointer to the restAPI constructed by setupAndStartServices.
	// Used by RunContextWithOptions to update builtinRegistry after live-deps are wired
	// (the M16 live-deps re-population in RunContextWithOptions creates a fresh
	// *BuiltinRegistry that must reach the already-constructed restAPI; storing the
	// ref here avoids a larger refactor of setupAndStartServices).
	restAPIRef *restAPI

	// selfWriteReg is the shared registry of config.json content hashes that
	// were written by the app itself. Created in setupAndStartServices when the
	// restAPI is constructed; passed to setupConfigWatcherPolling so the poller
	// can suppress reload on app-initiated writes.
	selfWriteReg *configSelfWriteRegistry

	// loadConfigForSwap re-reads config.json for handleConfigReload's swap,
	// under restAPI.configMu, so a config write committed through
	// safeUpdateConfigJSON while the reload was stopping services is not
	// overwritten by the reload's older snapshot (a lost update: a login's
	// session-cookie hash vanished and the next request got 401). Set by
	// serveReloadLoop to the same loader every reload uses
	// (newReloadConfigLoader). Nil — tests and paths that never serve
	// reloads — keeps the loaded config as is.
	loadConfigForSwap func() (*config.Config, error)

	// homePath is the Omnipus home directory. Stored here so omnipusGracefulShutdown
	// can remove the self-registered PID file without an additional parameter.
	homePath string

	// SteerDeps bundles every ADR-091 pkg/steer implementation built at
	// boot from the pkg/agent side (I-2/I-5/I-6/I-8) plus the
	// two real stores and the boot hook — see
	// gateway_boot.go::wireSteerDeps for the one section that constructs
	// it. All four implementations are real (Launcher, Canceller,
	// Deliverer, Classifier). Exported so other gateway boundaries
	// can read it off the running *services to inject into
	// their own code without re-wiring pkg/agent themselves.
	SteerDeps steer.Deps
	// SteerAudienceResolver is I-5's AudienceResolver — not part of
	// steer.Deps' fixed shape (that shape is I-7's fixture dependency
	// list), but every boundary owner (pkg/tools boundary 8, pkg/channels
	// boundary 7, pkg/gateway boundary 6, pkg/askuser boundary 12) needs
	// one to inject. Built alongside SteerDeps by the same
	// gateway_boot.go::wireSteerDeps section.
	SteerAudienceResolver steer.AudienceResolver
}

// RunOptions carries the inputs for the gateway runtime. Kept as a struct
// so new Sprint-J options (SandboxMode) and future options can be added
// without churning the Run signature. The legacy Run function remains as a
// thin wrapper for callers that have not migrated.
type RunOptions struct {
	Debug             bool
	HomePath          string
	ConfigPath        string
	AllowEmptyStartup bool
	// SandboxMode is the value of the --sandbox CLI flag ("enforce",
	// "permissive", "off"). Empty means "no flag set, use config". See
	// FR-J-006 for CLI > config > default precedence.
	SandboxMode string
	// AllowGodMode is set by the --allow-god-mode CLI flag. When true,
	// the global god-mode runtime toggle (sandbox.god_mode) is authorized to
	// take effect: switching it on disables the kernel sandbox for every
	// agent. Without this flag, the toggle has no effect even if enabled. Has
	// no effect when sandbox.GodModeAvailable is false (nogodmode build tag).
	//
	// This is ONE of two ways to grant god-mode availability — the other is
	// the config-persisted sandbox.god_mode_allowed flag (set by the
	// Settings UI's god-mode toggle). See resolveAllowGodMode.
	AllowGodMode bool
}

// SandboxBootError wraps a sandbox Apply/Install failure so the CLI entry
// point can distinguish it from generic boot errors and exit with the
// Sprint-J-specific EX_CONFIG (78) code per FR-J-004.
type SandboxBootError struct {
	Err error
}

// RunWithOptions is the Sprint-J entry point. Handles the gateway boot flow,
// accepting the expanded RunOptions struct (including SandboxMode). Installs
// OS signal handlers (SIGINT, SIGTERM) and blocks until one fires, then
// delegates to RunContextWithOptions. This is the CLI entry point
// (cmd/omnipus/internal/gateway/command.go).
func RunWithOptions(opts RunOptions) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case <-sigChan:
			cancel()
		case <-ctx.Done():
		}
	}()

	return RunContextWithOptions(ctx, opts)
}

// RunContext is the context-cancellable entry point for the gateway runtime.
// RunWithOptions is a thin signal-driven wrapper around this function (via
// RunContextWithOptions). Tests call RunContext directly with a context they
// control, enabling in-process integration testing without signal wiring.
//
// The caller is responsible for canceling ctx when the gateway should shut down.
// RunContext blocks until ctx is canceled or a fatal error occurs, then performs
// the full graceful shutdown sequence (channels → agent loop → background services
// → provider) before returning.
//
// Tests that need to override Sprint-J options (sandbox mode) should call
// RunContextWithOptions instead.
func RunContext(ctx context.Context, debug bool, homePath, configPath string, allowEmptyStartup bool) error {
	return RunContextWithOptions(ctx, RunOptions{
		Debug:             debug,
		HomePath:          homePath,
		ConfigPath:        configPath,
		AllowEmptyStartup: allowEmptyStartup,
	})
}

// errEgressProxyDisabled is the sentinel returned by buildEgressProxyOrAbort
// when construction failed but no sandbox.egress_allow_list was configured
// (the operator never opted into egress restriction). Callers use
// errors.Is(err, errEgressProxyDisabled) to distinguish this non-fatal,
// log-and-continue outcome from a real boot-abort error — a plain (nil, nil)
// return would leave both states indistinguishable to the caller (nilnil).
var errEgressProxyDisabled = errors.New("gateway: egress proxy disabled (construction failed with no sandbox.egress_allow_list configured)")

// u25AllSessionsForUsage adapts AgentLoop.ListAllSessions' ADR-057/U9
// paginated signature (limit, offset int, parentSessionID string, flat bool)
// back to the zero-arg, "return everything" shape systools.Deps.ListSessions
// still exposes (ADR-057 U25, W16i, [grill2 C2-1]).
//
// The single consumer of Deps.ListSessions is the get_usage tool
// (pkg/sysagent/tools/diag.go's UsageQueryTool), which aggregates token
// spend by period/agent/model/session with no pagination concept of its own
// — it always wants the WHOLE session set in one pass, so widening its field
// to accept limit/offset/parentSessionID would add parameters no caller can
// ever usefully vary. flat=true is not a default of convenience: FR-104
// warns explicitly that a roots-only listing silently DROPS a delegated
// child's token spend from the merged set (Total.Sessions would only carry
// parents), which would make get_usage under-report real spend with a green
// build — exactly this ADR's defining defect class. limit=0/offset=0 mean
// "no limit, from the start" (FR-098(b)), matching this method's pre-
// pagination "collect every session, every store" behavior exactly. See
// rest_stats_adr057_test.go's TestU25ListAllSessions_RootsOnly_
// UndercountsDelegatedChildSpend (red) and
// TestU25AllSessionsForUsage_FeedsGetUsageWithDelegatedChildSpend (green)
// for the proof: roots-only under-counts a real delegated child's spend;
// this flat=true wiring does not.
func u25AllSessionsForUsage(al *agent.AgentLoop) func() ([]*session.UnifiedMeta, []error) {
	return func() ([]*session.UnifiedMeta, []error) {
		page, errs := al.ListAllSessions(0, 0, "", true)
		return page.Sessions, errs
	}
}

// runContextWithOptions carries the shared state of RunContextWithOptions across its stages.
type runContextWithOptions struct {
	ctx                context.Context
	opts               RunOptions
	debug              bool
	homePath           string
	configPath         string
	allowEmptyStartup  bool
	allowGodMode       bool
	panicPath          string
	panicFunc          func()
	err                error
	cfg                *config.Config
	bundle             credentials.SecretBundle
	credStore          *credentials.Store
	provider           providers.LLMProvider
	providerCatalog    *catalog.Catalog
	msgBus             *bus.MessageBus
	agentLoop          *agent.AgentLoop
	liveLimits         *agent.LiveLimits
	sandboxResult      *SandboxApplyResult
	stopNag            context.CancelFunc
	centralBuiltinReg  *tools.BuiltinRegistry
	centralMCPReg      *tools.MCPRegistry
	runningServices    *services
	manualReloadChan   chan struct{}
	reloadTrigger      func() error
	sysSkillsLoader    *skills.SkillsLoader
	sysSkillWriter     *skills.SkillWriter
	sysRegistryManager *skills.RegistryManager
	sysSkillInstaller  *skills.SkillInstaller
	agentLoopCtx       context.Context
	agentLoopCancel    context.CancelFunc
	agentLoopDead      atomic.Bool
	smConsumerCancel   context.CancelFunc
	configReloadChan   <-chan watchedConfigChange
	stopWatch          func()
	// Boot-stage background writers (initializeAgentLoop): the browser
	// cache-trim schedule, the managed-Chromium preprovision download and
	// the unknown-window primer. They derive from bootBackgroundCtx so one
	// cancel stops all three, and bootBackgroundDone closes when the
	// WaitGroup has joined them — the same cancel-and-join contract the
	// catalog refresh loop has had since its own TempDir-race fix
	// (gateway_boot.go's startCatalogRefreshLoop doc comment). Without the
	// join, a caller whose teardown removes homePath (every in-process
	// gateway test) raced the preprovision download's .part-* writes
	// (#1644/#1695).
	bootBackgroundCtx    context.Context
	bootBackgroundCancel context.CancelFunc
	bootBackgroundWG     sync.WaitGroup
	bootBackgroundDone   chan struct{}
}

// RunContextWithOptions is the Sprint-J context-cancellable entry point.
// RunContext is a thin wrapper that builds a legacy RunOptions and calls this.
func RunContextWithOptions(ctx context.Context, opts RunOptions) error {
	rc := &runContextWithOptions{ctx: ctx, opts: opts}

	if r0, stop := rc.initializePanicLog(); stop {
		return r0
	}
	defer rc.panicFunc()

	if r0, stop := rc.initializeLoggingAndDataModel(); stop {
		return r0
	}
	defer logger.DisableFileLogging()

	if r0, stop := rc.loadConfigAndProvider(); stop {
		return r0
	}

	if r0, stop := rc.initializeAgentLoop(); stop {
		return r0
	}

	if r0, stop := rc.validateAndApplySandbox(); stop {
		return r0
	}
	if r0, stop := rc.startServices(); stop {
		return r0
	}

	rc.prepareSkillServices()

	rc.wireSystemTools()

	rc.announceStartup()
	defer rc.agentLoopCancel()

	rc.startBackgroundServices()
	defer rc.smConsumerCancel()

	rc.configureHealthAndWatcher()
	defer rc.stopWatch()

	return rc.serveReloadLoop()
}

// initializePanicLog creates the protected log directory and initializes panic logging.
func (rc *runContextWithOptions) initializePanicLog() (error, bool) {
	rc.debug = rc.opts.Debug
	rc.homePath = rc.opts.HomePath
	rc.configPath = rc.opts.ConfigPath
	rc.allowEmptyStartup = rc.opts.AllowEmptyStartup
	rc.allowGodMode = rc.opts.AllowGodMode

	// Create $OMNIPUS_HOME/logs ourselves, at 0700, before anything else
	// touches it. datamodel.Init (below) also creates this directory as
	// part of omnipusDirs, at 0700 — but only later in this sequence;
	// logger.InitPanic and logger.EnableFileLogging each MkdirAll the same
	// directory too, at 0755. os.MkdirAll is a silent no-op — it does not
	// chmod — when the directory already exists, so whichever of these
	// calls creates the directory first permanently wins its permission
	// bits. Creating it here first, at the stricter 0700, is what stops the
	// two logger calls' 0755 from leaking through and leaving
	// $OMNIPUS_HOME/logs group/world-readable for the life of the install.
	logsDir := filepath.Join(rc.homePath, logPath)
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		return fmt.Errorf("creating log directory: %w", err), true
	}

	rc.panicPath = filepath.Join(logsDir, panicFile)
	rc.panicFunc, rc.err = logger.InitPanic(rc.panicPath)
	if rc.err != nil {
		return fmt.Errorf("error initializing panic log: %w", rc.err), true
	}
	return nil, false
}

// initializeLoggingAndDataModel initializes file logging and the data model in boot order.
func (rc *runContextWithOptions) initializeLoggingAndDataModel() (error, bool) {
	// Re-review FIX 1 (two independent reviewers, plus a third verifying
	// pass): bootLoggingAndDataModel runs logger.EnableFileLogging,
	// installSlogBridge, and datamodel.Init in the one order that captures
	// datamodel.Init's own first-run slog calls instead of silently losing
	// them — this used to call datamodel.Init BEFORE any of this boot
	// preamble existed. See bootLoggingAndDataModel's doc comment for the
	// full account and why logger.InitPanic stays inline here rather than
	// folding into that helper.
	if rc.err = bootLoggingAndDataModel(rc.homePath); rc.err != nil {
		return rc.err, true
	}
	return nil, false
}

// loadConfigAndProvider loads credentials and configuration, seeds agents, and installs the provider catalog.
func (rc *runContextWithOptions) loadConfigAndProvider() (error, bool) {
	// Construct and unlock the credential store BEFORE loading config, per the
	// documented credential boot contract (ADR-004).
	// Implements BRD SEC-22/SEC-23 deny-by-default behavior.
	rc.cfg, rc.bundle, rc.credStore, rc.err = bootCredentials(rc.homePath, rc.configPath)
	if rc.err != nil {
		return rc.err, true
	}
	// Skills secret check (skills spec r15 §7.4): every skill change is
	// checked against the stored secret values from this store.
	wireSkillSecretCheck(rc.credStore)

	// #155: derive the audit-chain HMAC key from the master key and
	// install it process-wide BEFORE constructing the agent loop. The
	// agent loop's audit.NewLogger picks this up via the package-level
	// fallback when LoggerConfig.HMACKey is nil. The master key never
	// crosses the credentials package boundary — DeriveSubkey runs HKDF
	// internally and returns 32 bytes of independent key material.
	//
	// CORRECTED (14-reviewer sign-off, MEDIUM — this comment previously
	// claimed a fallback that does not exist in production): a derivation
	// failure here does not itself abort boot — it is logged and
	// audit.SetProcessChainKey is simply never called — but that is NOT the
	// same as audit.NewLogger silently running with a dev-only key.
	// audit.NewLogger's own resolveChainKey (pkg/audit/hmac.go) gates its
	// deterministic dev key behind testing.Testing(): a real gateway binary
	// with no process chain key fails that resolution outright, and the
	// agent.NewAgentLoop call below maps the resulting
	// audit.LoggerConstructionError to a fatal SandboxBootError whenever the
	// operator has cfg.Sandbox.AuditLog enabled (see the errors.As branch a
	// few lines down). So: with audit_log=true, a derivation failure here is
	// an imminent boot abort a few lines later, not a soft warning to
	// dismiss; with audit_log=false, audit.NewLogger is never even
	// constructed and this WARN is genuinely inconsequential.
	if chainKey, derrErr := rc.credStore.DeriveSubkey(audit.AuditChainKeyInfo); derrErr == nil {
		audit.SetProcessChainKey(chainKey)
	} else {
		slog.Warn("audit: could not derive HMAC chain key from master key — "+
			"audit.NewLogger will fail closed below if sandbox.audit_log is enabled",
			"error", derrErr)
	}

	logger.SetLevelFromString(rc.cfg.Gateway.LogLevel)

	if rc.debug {
		logger.SetLevel(logger.DEBUG)
		fmt.Println("🔍 Debug mode enabled")
	}

	// O14: fold the config-persisted operator authorization
	// (sandbox.god_mode_allowed, granted by a prior UI enable via POST
	// /api/v1/gateway/god-mode) into the boot flag. This is the single point
	// where the two authorization sources combine into one value; every
	// downstream consumer of god-mode AVAILABILITY (agent.SetAllowGodMode's
	// process-wide atomic AND restAPI.allowGodMode) reads this same
	// allowGodMode variable, so there is exactly one boot-frozen source of
	// truth for the rest of the process's lifetime. See
	// resolveAllowGodMode's doc comment for why a config-only grant needs a
	// restart to take effect.
	godModeSource := "none"
	switch {
	case rc.opts.AllowGodMode:
		godModeSource = "--allow-god-mode"
	case rc.cfg.Sandbox.GodModeAllowed:
		godModeSource = "sandbox.god_mode_allowed"
	}
	rc.allowGodMode = resolveAllowGodMode(rc.allowGodMode, rc.cfg)

	// Enforce god-mode latches: abort boot if either authorization source
	// granted god mode but the build does not support it (nogodmode tag
	// compiles GodModeAvailable=false). Fail closed: a contradictory grant
	// (authorized but unsupported) must never be silently downgraded to
	// "unavailable" — it is a misconfiguration and boot must stop.
	if rc.allowGodMode && !sandbox.GodModeAvailable {
		return fmt.Errorf(
			"gateway: god mode unavailable in this build (compiled with nogodmode); " +
				"remove --allow-god-mode / sandbox.god_mode_allowed and restart",
		), true
	}
	// Emit a persistent WARN so operators cannot claim they were not warned.
	if rc.allowGodMode {
		fmt.Fprintf(os.Stderr,
			"WARN: gateway started with god mode available (source: %s) — agents may have sandbox disabled\n",
			godModeSource)
		slog.Warn("gateway started with god mode available — agents may have sandbox disabled",
			"source", godModeSource)
	}

	// godmode-policy-freeze fix (2026-09-25): publish god-mode AVAILABILITY
	// to agent's package-level atomic HERE, before initializeAgentLoop calls
	// agent.NewAgentLoop below. NewAgentLoop builds every agent instance
	// eagerly, and each instance's tool-policy snapshot
	// (agentToolsCfgToPolicy) and bash tool (wireExecToolDepsOn) read
	// agent.GodModeActive(cfg) AT CONSTRUCTION TIME — the OLD call site
	// (AgentLoop.SetAllowGodMode, invoked from gateway_boot.go deep inside
	// setupAndStartServices) ran AFTER those instances already existed, so a
	// persisted sandbox.god_mode=true survived a restart with every
	// already-built consumer frozen at GodMode=false until the next
	// TriggerReload. Mirrors the same "must run before NewAgentLoop"
	// requirement this function's own agent.SetWindowCatalog call documents
	// a few lines below for the provider-catalog boot sequence.
	agent.SetGodModeAvailable(rc.allowGodMode)

	// Build the real LLM provider. The test_harness override hook + scripted-
	// scenario fallback was removed 2026-05-10; tests now run against real
	// OpenRouter via the configured provider entry.
	// ADR-068 FR-020: the startup provider's model id is NEVER written back
	// into agents.defaults.default_model. The old `ModelName == ""` back-fill
	// guard that lived here was deleted with the alias — the pair is written
	// only by onboarding completion and the default-model PUT.
	rc.provider, _, rc.err = createStartupProvider(rc.cfg, rc.allowEmptyStartup)
	if rc.err != nil {
		return fmt.Errorf("error creating provider: %w", rc.err), true
	}

	// ADR-054 D2/D3 + SeedConfig + its one-time migrations, persisted. The
	// whole sequence lives in seedAndPersistAgentRoster (boot_agent_roster.go)
	// so the upgrade path can be tested against a real on-disk fixture with
	// exactly the calls boot makes, in exactly this order.
	if rosterErr := seedAndPersistAgentRoster(rc.cfg, rc.homePath, rc.configPath); rosterErr != nil {
		return rosterErr, true
	}

	// Skills start-up (skills spec r15 §8.5): recovery, built-in sync and
	// retirement, before NewAgentLoop builds any agent, so the first live
	// instance never holds a retired grant. Never fatal: a failure is logged
	// and the step runs again at the next start.
	if _, skillsErr := bootSkills(rc.cfg, rc.homePath); skillsErr != nil {
		slog.Warn("gateway: skills start-up step did not complete; it runs again at the next start", "error", skillsErr)
	}

	// Eagerly seed every System Agent's SOUL.md right after SeedConfig.
	// For the Judge this closes an operator-reported UX gap: its soul only
	// materialized lazily on its FIRST real verifier dispatch (pkg/agent's
	// ensureVerifierSoul), so a fresh install's Judge profile showed an empty
	// soul in the SPA — but the soul is now operator-editable there, so the
	// operator must be able to see the default judging standards they'd be
	// overriding before ever running a judgment. For the PlanSupervisor this
	// is the ONLY seed path at all (plan-supervisor-spec FR-005 rev 2 adds no
	// lazy backstop), so without this call the adjudicator would wake with an
	// empty prompt. seedSystemAgentEagerSouls writes only to SOUL.md (plain
	// file I/O, not config.json) so it needs no safeUpdateConfigJSON/configMu
	// involvement at all. See its doc comment for why this call site — not
	// coreagent.SeedConfig itself — is where it lives.
	seedSystemAgentEagerSouls(rc.cfg)

	// ADR-067 T067-07: boot the ONE provider catalog for this process. Boot
	// performs no network I/O — it parses the embedded snapshot, reads the
	// persisted last-known-good from $OMNIPUS_HOME/providers_catalog.json,
	// and serves whichever is valid and newest (E6). It never fails: with
	// neither usable, the catalog serves nothing, every lookup misses, and
	// the media path stays optimistic (E7).
	//
	// It must happen HERE, before NewAgentLoop, because NewAgentLoop builds
	// every agent instance and each construction runs ADR-066's ResolveWindow
	// ladder — whose rung 5 is this catalog. Installing it afterwards would
	// leave every agent's window resolved from the floor at boot and only
	// corrected at the next reload.
	//
	// The startup pull and the 24 h ticker are NOT started here; they start
	// after the listener is bound (see setupAndStartServices).
	rc.providerCatalog = bootProviderCatalog(rc.homePath)
	agent.SetWindowCatalog(rc.providerCatalog)
	// A8 mail-operation budget: install the shared per-account gate BEFORE
	// NewAgentLoop builds every agent instance and its registerSharedTools
	// registers the email tools — the tools capture it at registration, the
	// same "must run before NewAgentLoop" ordering rule as SetWindowCatalog
	// above. Keyed by the data dir, so this is the SAME instance the watcher
	// set (gateway_boot/reload) and the REST handlers resolve.
	agent.SetSharedMailBudget(email.SharedMailBudget(rc.homePath))
	// Shared Mail runtime (w5-integration, MC-1): construct the ONE pooled
	// session manager for this data root and install it — plus the pair
	// generation resolver (register row 12's single construction) — BEFORE
	// NewAgentLoop, by the same ordering rule. The tools capture both at
	// registration; the watcher set and the REST handlers resolve the same
	// state-dir-keyed instance themselves (mail_runtime.go's wiring table).
	// The runtime holder also carries the boot config/credential store so
	// the pool's establishment-time credential resolver works from the first
	// tool registration onward; initializeAgentLoop publishes the live loop
	// the moment it exists.
	mailRT := initGatewayMailRuntime(rc.homePath, rc.cfg, rc.credStore)
	agent.SetSharedMailSessions(gatewayMailSessionsFor(rc.homePath))
	agent.SetMailGenerationResolver(mailRT.mailGenerationForPair)
	// ADR-067 FR-012: the provider FACTORY dispatches on the protocol this
	// same document carries, so it must read the same instance — otherwise
	// the gateway would resolve windows from the pulled document while
	// constructing transports from the embedded snapshot.
	providers.SetCatalog(rc.providerCatalog)
	return nil, false
}

// initializeAgentLoop constructs the agent loop and starts its early background provisioning.
func (rc *runContextWithOptions) initializeAgentLoop() (error, bool) {
	rc.msgBus = bus.NewMessageBus()

	rc.agentLoop, rc.err = agent.NewAgentLoop(rc.cfg, rc.msgBus, rc.provider)
	if rt := gatewayMail.Load(); rt != nil {
		rt.setAgentLoop(rc.agentLoop)
	}
	if rc.err != nil {
		// B1.2(b): when the failure is an audit logger construction error and
		// the operator explicitly requested audit logging (cfg.Sandbox.AuditLog
		// = true), this is a fail-closed boot abort. CLAUDE.md "audit-everything
		// stance is non-negotiable" — silently disabling audit while it's
		// requested would be a security regression. Map to SandboxBootError so
		// cmd/omnipus exits with EX_CONFIG (78) per FR-J-004 and surfaces the
		// remediation message ("either disable `sandbox.audit_log` or fix
		// <error>") to the operator.
		var auditConstructErr *audit.LoggerConstructionError
		if errors.As(rc.err, &auditConstructErr) && rc.cfg.Sandbox.AuditLog {
			audit.EmitBootAbortStderr(
				"gateway.audit.construction_failed",
				"-",
				auditConstructErr.Dir,
				auditConstructErr,
				nil,
			)
			return &SandboxBootError{Err: auditConstructErr}, true
		}
		return fmt.Errorf("gateway: agent loop boot failed: %w", rc.err), true
	}

	// T068-10 (ADR-068 FR-010 last clause): startup sweep of orphaned
	// `<id>_API_KEY` credentials. Config is loaded and the store unlocked
	// (bootCredentials above); this is the earliest point where the audit
	// logger exists, so the sweep's provider.credential_swept entries land
	// in the real audit chain. Synchronous and fast (one store read, at
	// most a handful of deletes); never fatal.
	sweepOrphanedProviderCredentials(rc.cfg, rc.credStore, rc.agentLoop.AuditLogger())

	// Skills start-up audit (§8.5): the facts bootSkills gathered before the
	// audit logger existed are replayed now, once, through the real logger
	// NewAgentLoop wired (the same logger SetSkillsWriteAuditLogger installed).
	// Never fatal: lost lines are counted on the audit-degraded counter.
	if replayErr := replayBootSkillAudit(skillsBootReport, rc.agentLoop.AuditLogger()); replayErr != nil {
		slog.Warn("gateway: skills start-up audit lines were not all written", "error", replayErr)
	}
	skillsBootReport = skills.SyncReport{}

	// ADR-068 FR-046: an agent-path OAuth refresh (providers'
	// NewStoreOAuthTokenSource, invoked mid-turn) mints a NEW access+refresh
	// pair and persists it. bootCredentials above registered the tokens that
	// existed AT BOOT; without this hook the scrubber would keep protecting
	// those superseded values while the live pair travelled unprotected until
	// the next boot, sign-in or sign-in-status poll.
	//
	// pkg/providers cannot do this itself — it has no *config.Config — so it
	// exposes a one-line registration seam and the gateway fills it in here,
	// where both the live config and the unlocked store are in scope. Wired
	// after the agent loop exists so the closure can read the CURRENT config
	// on every call: a config reload swaps the *config.Config out from under
	// us, and registering onto the boot-time instance would silently stop
	// protecting anything after the first reload.
	//
	// RegisterSensitiveValues replaces rather than appends, so the closure
	// recomputes the COMPLETE set (config-ref bundle + every stored
	// "<vendor>_OAUTH" entry) exactly as boot does; the freshly minted values
	// it is handed are folded in explicitly so the registration does not
	// depend on the store write having already landed. Best-effort by
	// design: a failure here must never break a turn.
	wireOAuthSensitiveValueRegistrar(func() *config.Config { return rc.agentLoop.GetConfig() }, rc.credStore)

	// Install the same catalog instance on the agent loop: the presentation
	// gate (ADR-067 FR-004) and ResolveWindow's rung 5 read one document, and
	// setupAndStartServices reads it back from here for the REST surface and
	// the refresh loop.
	rc.agentLoop.SetCapabilityCatalog(rc.providerCatalog)

	// ADR-066 D2 rung 4 (T066-10): install the on-demand, 24 h-cached live
	// limits query AFTER NewAgentLoop has built every instance, so boot's
	// own window resolutions never reach the rung (FR-003: never at boot,
	// never on a timer). Installing performs no I/O; the first resolution
	// that reaches the rung — the next reload, a settings write, the
	// catalog projection — starts one background fetch per (provider, base
	// URL, model) and the live value applies at the resolution after it.
	// The credential comes from the store via the provider's api_key_ref
	// (InjectFromConfig's env injection first); a cloud row without one is
	// skipped, never queried.
	// The instance is RETAINED (runningServices.LiveLimits, below) so
	// shutdown can Close it: a fetch that is still in flight when the gateway
	// stops must not write cache/model_limits.json after RunContext returns.
	rc.liveLimits = newLiveLimitsForBoot(rc.homePath, rc.credStore, rc.agentLoop, reloadOnLiveWindow(rc.agentLoop))
	agent.SetLiveWindowLookup(rc.liveLimits.Lookup)

	// Boot-stage background writers, under one cancel-and-join umbrella. The
	// three goroutines below used to be bare `go` statements: nothing
	// stopped them and nothing waited for them, so a gateway that shut down
	// (or a test whose t.TempDir cleanup ran — every in-process gateway
	// test) raced whatever they were still writing. Measured 2026-11-08
	// (TempDir-cleanup family #1644/#1695): the preprovision download was
	// still growing browser/chromium/<version>/chrome-linux64.zip.part-*
	// seconds AFTER the test's full cleanup sequence had returned, which is
	// exactly the "TempDir RemoveAll cleanup: ... browser: directory not
	// empty" CI failure. The same cancel-and-join contract the catalog
	// refresh loop got for its identical failure (gateway_boot.go
	// startCatalogRefreshLoop's doc comment) now covers these three: one
	// derived context (rc.bootBackgroundCtx, child of rc.ctx — production
	// RunContextWithOptions shutdown cancels rc.ctx; the explicit drains
	// call stopBootBackgroundWriters, which cancels AND joins), one
	// WaitGroup, one done channel. rc.startServices attaches both handles
	// onto runningServices so stopBackgroundWriters (graceful shutdown) and
	// stopAndCleanupServices (partial-boot abort, reload teardown, and every
	// setupAndStartServices-based test harness) drain them.
	rc.bootBackgroundCtx, rc.bootBackgroundCancel = context.WithCancel(rc.ctx)
	rc.bootBackgroundDone = make(chan struct{})

	// FR-007 / US-2.AC2: an agent on a `locality: local` row the catalog
	// cannot size resolved UNKNOWN above (the rung was not installed yet) and
	// every one of its turns is refused with context_window_unknown. Ask the
	// rung once for exactly that population, in the background and off the
	// boot path; when the endpoint answers, reloadOnLiveWindow rebuilds those
	// agents with the window it reported. Without this a fresh Ollama install
	// stays refused indefinitely and the refusal points the operator at a
	// control (Settings → Models → Model overrides) they should not have
	// needed. Not a timer: one pass, only for agents that are already broken.
	rc.bootBackgroundWG.Add(1)
	go func() {
		defer rc.bootBackgroundWG.Done()
		primeUnknownWindowsFn(rc.agentLoop)
	}()

	// Boot-time browser provisioning: NewAgentLoop above just finished
	// registering browser tools for every agent (registerSharedTools →
	// browser.RegisterTools), each backed by its own *browser.BrowserManager.
	// Historically the managed Chromium/headless-shell binary was resolved
	// (and, on a fresh install, downloaded — chrome-for-testing, 100+MB) only
	// lazily, at an agent's FIRST browser tool call. Kick that resolution off
	// NOW instead, in the background, so a fresh install's download is
	// already in flight (or done) well before any agent needs it — this is
	// also where a broken $PATH candidate (e.g. Ubuntu's chromium-browser
	// snap stub on a host with no snapd) gets discovered and skipped in
	// favor of the managed fallback, rather than surfacing as a "chrome
	// failed to start" error deep inside a user's first browser_navigate.
	//
	// Uses ctx (the gateway's own shutdown-aware context — canceled when the
	// gateway is asked to stop, per RunContext's doc comment), the same
	// pattern already used for other detached boot-time background work
	// below (e.g. go agentLoop.BootstrapRecapPass(ctx)). Non-blocking: boot
	// must not stall on a multi-second download (CLAUDE.md graceful
	// degradation, Hard Constraint #4). Non-fatal: a failure here only means
	// resolution/download is retried lazily at first real use, exactly the
	// pre-existing behavior — so it is logged at WARN, never returned as a
	// boot error.
	// FR-016c: ONE preprovision for the whole install, driven by the pool
	// rather than by a range over BrowserManagers().
	//
	// The old loop was silently a no-op under the pool. It ranged over the
	// managers that exist at boot, and under a lazy pool that slice is EMPTY —
	// so the download this block exists to start never started, and every
	// fresh install paid the 30-60 s Chrome-for-Testing fetch on a user's
	// first click instead. A loop that iterates nothing looks exactly like a
	// loop that had nothing to do.
	//
	// There is one managed-Chromium install for every workspace
	// (InstallRootForProfileDir is key-independent by construction, FR-037a),
	// so this resolves it once with zero live keys.
	if pool := rc.agentLoop.BrowserPool(); pool != nil {
		// FR-072 triggers 2 and 3, both off the boot path so neither delays it.
		//
		// Trigger 2 (boot): sweep every profile on disk that has no live
		// Chrome. This is what reaches profiles closed by a run that crashed,
		// or by a gateway that never got to run its post-close trim.
		//
		// Trigger 3 (schedule): a SEPARATE, much slower ticker than the
		// one-minute reaper sweep, and deliberately not folded into it — the
		// reaper does a map scan, this walks directories. It is not the
		// primary trigger either; pool.Close(k) returning is, and that fires
		// within milliseconds with no interval to wait for.
		rc.bootBackgroundWG.Add(2)
		go func() {
			defer rc.bootBackgroundWG.Done()
			runBrowserCacheTrimSchedule(rc.bootBackgroundCtx, pool)
		}()
		go func() {
			defer rc.bootBackgroundWG.Done()
			path, ppErr := pool.Preprovision(rc.bootBackgroundCtx)
			if ppErr != nil {
				// logger (not slog): slog writes to fd 2, which boot's
				// initPanicFile redirects to gateway_panic.log — operators
				// read gateway.log for runtime diagnostics, so route this
				// through zerolog like the rest of the browser package.
				logger.WarnCF("browser", "preprovision failed — will retry lazily at first browser tool use",
					map[string]any{"error": ppErr.Error()})
				return
			}
			if path != "" {
				logger.InfoCF("browser", "preprovision resolved", map[string]any{"exec_path": path})
			}
		}()
	}

	// The joiner starts only AFTER every Add above: a Wait that begins before
	// the first positive-delta Add would see a zero counter, return at once,
	// and close bootBackgroundDone before the writers even started (the
	// sync.WaitGroup doc's ordering rule).
	go func() {
		rc.bootBackgroundWG.Wait()
		close(rc.bootBackgroundDone)
	}()

	// B1.2(d) + ADR-053 D17: wire the sandbox → audit bridges now that the
	// agent loop (and thus the audit logger) is constructed. See
	// sandbox_audit_hooks.go; unwired symmetrically in shutdown.go.
	wireSandboxAuditHooks(rc.agentLoop)
	return nil, false
}

// stopBootBackgroundWriters cancels the boot-stage background writer
// goroutines (browser cache-trim schedule, managed-Chromium preprovision,
// unknown-window primer — the WaitGroup initializeAgentLoop started them
// under) and waits, bounded by ctx, until all of them have returned. Every
// teardown that removes homePath while the process is still alive —
// RunContextWithOptions' graceful shutdown (via stopBackgroundWriters), the
// boot-abort branch of startServices, and in-process gateway tests'
// t.TempDir cleanup — must drain them BEFORE that removal: the preprovision
// download's browser/chromium/*.zip.part-* writes raced t.TempDir removal
// and produced "TempDir RemoveAll cleanup: ... browser: directory not empty"
// in CI (#1644/#1695).
//
// The WaitGroup join does NOT cover the primer's fetch: primeUnknownWindows
// calls agent.ResolveWindow, whose rung-4 Lookup starts a fetch whose writer
// runs under the LiveLimits instance's own lifetime (agent.NewLiveLimits
// derives it from context.Background(), deliberately independent of every
// gateway context). A boot abort that returned after the join alone left
// that fetch running: releasing its held HTTP answer afterwards wrote
// cache/model_limits.json into a home the caller had already started
// removing (CHECK 2, 2026-11-08, defect 1). So after the join this drain
// also Closes rc.liveLimits — cancelling any fetch the primer (or a reload,
// or a settings write) started and waiting, bounded by the same ctx, for
// its writer to exit; a call still running at the deadline is not abandoned
// half-way, matching stopBackgroundWriters' posture for its own bounded
// waits. LiveLimits.Close is once-guarded (agent.LiveLimits.Close), so the
// later teardown closes — the startServices abort branch, the graceful
// shutdown, every test cleanup — stay idempotent, never a double-close
// panic. Nil-safe twice over: a boot that stopped before
// initializeAgentLoop has neither a WaitGroup nor a LiveLimits to close.
func (rc *runContextWithOptions) stopBootBackgroundWriters(ctx context.Context) {
	if rc == nil || rc.bootBackgroundCancel == nil {
		return
	}
	rc.bootBackgroundCancel()
	select {
	case <-rc.bootBackgroundDone:
	case <-ctx.Done():
		slog.Warn("gateway: boot background writers did not stop within the shutdown budget; continuing",
			"error", ctx.Err())
	}
	if rc.liveLimits != nil {
		closed := make(chan struct{})
		go func() {
			rc.liveLimits.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-ctx.Done():
			slog.Warn("gateway: live-limits close did not finish within the shutdown budget; continuing",
				"error", ctx.Err())
		}
	}
}

// bootAbortDrainTimeout bounds the boot-abort drain of the boot-stage
// background writers in validateAndApplySandbox and startServices. The cancel
// is already delivered when the wait starts, so this only waits out the
// writers' exit after cancellation — the same posture as
// bootBackgroundStopTimeout, just tighter: a boot that is already aborting
// should not spend its full stop budget waiting for a download to notice.
const bootAbortDrainTimeout = 5 * time.Second

// validateAndApplySandbox cleans orphaned runs, validates policies, applies the sandbox, and starts its nag banner.
//
// Every stop=true return below runs AFTER initializeAgentLoop started the
// boot-stage background writers (the browser cache-trim schedule, the
// managed-Chromium preprovision download, the unknown-window primer —
// TempDir-cleanup family #1644/#1695), so each one must cancel and join them
// before returning: the preprovision download keeps writing
// browser/chromium/*.zip.part-* under homePath until it is cancelled, and a
// test (or a supervisor) that treats this return as "the gateway is gone"
// removes homePath right after it. The deferred guard drains once for all
// four abort returns. validateAndApplySandbox runs on the boot path only —
// the reload path never calls it — so this cannot cut short a live gateway's
// writers, and stopBootBackgroundWriters is nil-safe for a boot that stopped
// before initializeAgentLoop.
func (rc *runContextWithOptions) validateAndApplySandbox() (err error, stop bool) {
	defer func() {
		if !stop {
			return
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), bootAbortDrainTimeout)
		defer stopCancel()
		rc.stopBootBackgroundWriters(stopCtx)
	}()

	// Spec-4 FR-5.3 (M-4): GC orphaned external-CLI run directories left behind by
	// a prior process (crash / SIGKILL / power loss). Runs ONCE at boot, BEFORE any
	// new external-cli sub-agent run can be dispatched, so every run dir present is
	// safely an orphan. Non-fatal: a reaper failure must not block boot.
	{
		reapCtx, reapCancel := context.WithTimeout(rc.ctx, 60*time.Second)
		if reapRes, reapErr := runner.ReapOrphans(reapCtx); reapErr != nil {
			slog.Warn("gateway: external-runner orphan reaper failed (non-fatal)", "error", reapErr)
		} else if reapRes.Removed > 0 || len(reapRes.Errors) > 0 {
			slog.Info("gateway: external-runner orphan reaper swept",
				"scanned", reapRes.Scanned, "removed", reapRes.Removed, "errors", len(reapRes.Errors))
		}
		reapCancel()
	}

	// Backstop for the executor-smoke-test ephemeral workspace cleanup race
	// (see drainUntilClosedOrGrace's doc in rest_executor_smoketest.go): that
	// handler is normally solely responsible for removing its own scratch
	// dir under $OMNIPUS_HOME/executor-smoke-test-runs, but a crash, SIGKILL,
	// or a client disconnect racing a still-alive subprocess can leave one
	// behind. Runs ONCE at boot, BEFORE any new smoke-test run can be
	// dispatched, so every dir present is safely an orphan — mirrors
	// ReapOrphans' reasoning above. Non-fatal: a sweep failure must not block
	// boot.
	if removed, sweepErrs := sweepSmokeTestOrphans(); removed > 0 || len(sweepErrs) > 0 {
		slog.Info("gateway: executor-smoke-test orphan sweep completed",
			"removed", removed, "errors", len(sweepErrs))
	}

	// Boot Order step 4 (FR-062 / M7): validate per-agent tool policies before
	// the sandbox applies. Ava-equivalent core agents abort boot on violation;
	// custom agents log and continue.
	{
		agentsDir := filepath.Join(rc.homePath, "agents")
		valResults, abortBoot := config.ValidateAgentConfigs(
			agentsDir,
			coreagent.HasSystemAllowsInConstructorSeed,
			nil, // knownTools: central registry not yet fully populated at this stage
			nil, // auditLog: audit subsystem not yet available; falls back to stderr
		)
		for _, r := range valResults {
			for _, e := range r.PolicyErrors {
				slog.Warn("gateway: agent policy validation error", "agent_id", r.AgentID, "error", e)
			}
		}
		if abortBoot {
			return fmt.Errorf("gateway: agent config validation failed — aborting boot (FR-062)"), true
		}

		// Hard-validation of tool-policy COVERAGE (CLAUDE.md hard constraint
		// 6): every static builtin tool must resolve from an explicit,
		// literal, wildcard-free policy entry — global sandbox.tool_policies
		// and/or an agent's tools.builtin.policies — for every agent. There is
		// no hardcoded allow/deny/ask fallback anywhere in Go for tool-policy
		// resolution (pkg/tools/compositor.go now fails closed to "deny" on a
		// genuine no-match); this is the other half of that contract — a gap
		// must abort boot, not surface as a silent runtime deny the operator
		// never asked for. The gap list is logged in full so the failure is
		// immediately actionable.
		//
		// repairAndValidateToolPolicyCoverage runs the shared "reconcile the
		// global ceiling, then hard-validate what remains" sequence (ADR-077
		// two-layer model): it migrates installations whose on-disk config
		// predates a newly-added static builtin tool by reconciling the
		// GLOBAL ceiling with that tool's real shipped default from
		// pkg/config/defaults.go. Without this, upgrading an existing
		// installation would find a coverage gap for every newly-shipped
		// tool on every agent and abort boot on every restart. After the
		// reconcile, validation should almost always find zero gaps — it
		// remains as a never-firing correctness tripwire for anything
		// reconcile cannot close (e.g. a genuinely corrupt config, or a
		// catalog/defaults.go drift).
		if gaps := repairAndValidateToolPolicyCoverage(rc.cfg); len(gaps) > 0 {
			for _, g := range gaps {
				slog.Error("gateway: tool-policy coverage gap", "detail", g.String())
			}
			return fmt.Errorf(
				"gateway: tool-policy coverage validation failed — aborting boot (%d gap(s), see preceding error logs)",
				len(gaps),
			), true
		}
	}

	// Apply the kernel sandbox to the gateway process BEFORE any HTTP listener
	// binds. Strict ordering:
	//   unlock → config → NewAgentLoop → applySandbox → setupAndStartServices
	// where setupAndStartServices ends in ChannelManager.StartAll which calls
	// ListenAndServe on the shared HTTP server. During the Apply→Install→listen
	// window, external TCP probes receive ECONNREFUSED because the socket does
	// not exist yet.
	var sandboxErr error
	rc.sandboxResult, sandboxErr = applySandbox(SandboxApplyOptions{
		CLIMode:  rc.opts.SandboxMode,
		Cfg:      rc.cfg,
		HomePath: rc.homePath,
		Backend:  rc.agentLoop.SandboxBackend(),
	})
	if sandboxErr != nil {
		// FR-J-004: kernel apply failure on a capable kernel is fatal.
		// Never bind the HTTP listener in this state — a half-sandboxed
		// process is worse than failing to boot. Wrapping in
		// SandboxBootError lets cmd/omnipus map this to exit code 78.
		slog.Error("gateway: sandbox apply failed — aborting boot",
			"error", sandboxErr,
			"requested_mode", rc.opts.SandboxMode)
		return &SandboxBootError{Err: sandboxErr}, true
	}

	// Log the applied sandbox mode and degradation state so operators can
	// verify the runtime posture from logs without hitting the authenticated
	// /api/v1/security/sandbox-status endpoint. applySandbox is the single
	// source of truth for mode resolution (CLI > config > default); any
	// discrepancy between the applied mode and the config file is already
	// visible via result.ApplyState in /api/v1/security/sandbox-status.
	slog.Info(
		"gateway: sandbox applied",
		"applied_mode", string(rc.sandboxResult.Mode),
		"backend", rc.sandboxResult.BackendName,
		"landlock_enforced", rc.sandboxResult.ApplyState.LandlockEnforced,
		"seccomp_enforced", rc.sandboxResult.ApplyState.SeccompEnforced,
		"audit_only", rc.sandboxResult.ApplyState.AuditOnly,
		"disabled_by", rc.sandboxResult.DisabledBy,
	)

	// Thread the actually-applied mode into the agent loop so exec tool
	// deps use the true runtime enforcement level (not the config file value).
	rc.agentLoop.SetAppliedSandboxMode(rc.sandboxResult.Mode)

	// Thread the actually-applied backend into the agent loop. On the degrade
	// path (sandbox_apply.go::degradeAfterLandlockFailure) applySandbox returns
	// a fresh FallbackBackend that differs from the construction-time selection
	// seeded above; this call keeps the sandbox-status handler and each agent's
	// system preamble reporting that fallback, not the stale kernel backend.
	rc.agentLoop.SetSandboxBackend(rc.sandboxResult.Backend)

	fmt.Println("\n📦 Agent Status:")
	startupInfo := rc.agentLoop.GetStartupInfo()
	toolsInfo, _ := startupInfo["tools"].(map[string]any)
	skillsInfo, _ := startupInfo["skills"].(map[string]any)
	if toolsInfo == nil {
		toolsInfo = map[string]any{"count": 0}
	}
	if skillsInfo == nil {
		skillsInfo = map[string]any{"available": 0, "total": 0}
	}
	fmt.Printf("  • Tools: %d loaded\n", toolsInfo["count"])
	fmt.Printf("  • Skills: %d/%d available\n", skillsInfo["available"], skillsInfo["total"])

	logger.InfoCF("agent", "Agent initialized",
		map[string]any{
			"tools_count":      toolsInfo["count"],
			"skills_total":     skillsInfo["total"],
			"skills_available": skillsInfo["available"],
		})

	// Pre-compile all embedded inbound validation schemas before the HTTP listener
	// starts. Any schema-compile failure aborts boot immediately with a clear error
	// rather than silently degrading to no-validation at first request.
	if compileErr := PreCompileAllInboundSchemas(); compileErr != nil {
		return fmt.Errorf("gateway: inbound schema pre-compile failed: %w", compileErr), true
	}

	// Arm the permissive / production-off nag banner AFTER pre-compile so that a
	// pre-compile failure does not leak the nag goroutine (StartNagBanner allocates
	// a goroutine that must be stopped via stopNag()).
	rc.stopNag = StartNagBanner(rc.sandboxResult.NagReason, nil)
	return nil, false
}

// startServices starts gateway services and wires their central registries and reload trigger.
func (rc *runContextWithOptions) startServices() (error, bool) {
	slog.Info("gateway: inbound schemas pre-compiled successfully")

	// M16 (FR-001/FR-002): pre-instantiate central registries.
	// BuiltinRegistry is populated here with nil deps (for name/description metadata only).
	// System.* tools are registered with nil deps; general builtins are registered as
	// metadata-only instances (deps-free, never executed — per ADR-018 D-A1).
	// After sysAgentDeps is wired (below), the registry is re-populated with live deps.
	// MCPRegistry starts empty; MCP servers populate it at connection time.
	rc.centralBuiltinReg, _ = buildCentralBuiltinRegistry(nil)
	rc.centralMCPReg = tools.NewMCPRegistry()
	// Wire the central registries into the agent loop so ReconcileMCP (triggered
	// by REST/sysagent MCP writes and hot-reload) populates the SAME registry
	// instance restAPI reads for GET /api/v1/tools and /mcp-servers tool_count —
	// otherwise connected MCP tools would never surface outside the per-agent
	// registries. Nil-safe on the AgentLoop side.
	rc.agentLoop.SetCentralMCPRegistries(rc.centralMCPReg, rc.centralBuiltinReg)
	// Wire the credential-store resolver so ReconcileMCP can resolve
	// add_mcp_server's EnvRefs (pkg/sysagent/tools/mcp.go routes MCP server
	// `env` secrets through the encrypted credential store instead of
	// config.json plaintext) back into real values at connect time. Store.Get
	// has exactly the func(string) (string, error) signature
	// AgentLoop.SetCredentialResolver expects. Guarded on credStore != nil
	// defensively — bootCredentials aborts boot on failure, so this should
	// always be non-nil in practice, but a nil receiver would panic inside
	// Store.Get's mutex lock.
	if rc.credStore != nil {
		rc.agentLoop.SetCredentialResolver(rc.credStore.Get)
	}

	rc.runningServices, rc.err = setupAndStartServices(
		rc.ctx,
		rc.cfg,
		rc.bundle,
		rc.agentLoop,
		rc.msgBus,
		rc.homePath,
		rc.credStore,
		rc.sandboxResult,
		rc.centralBuiltinReg,
		rc.centralMCPReg,
		rc.allowGodMode,
	)
	if rc.err != nil {
		rc.stopNag() // don't leak the nag goroutine if service setup fails.
		rc.liveLimits.Close()
		// Boot aborted: join the boot-stage writers before returning, or the
		// preprovision download keeps writing into homePath after this
		// returns (#1644/#1695 — same race as the teardown paths below).
		stopCtx, stopCancel := context.WithTimeout(context.Background(), bootAbortDrainTimeout)
		defer stopCancel()
		rc.stopBootBackgroundWriters(stopCtx)
		return rc.err, true
	}
	rc.runningServices.stopNagBanner = rc.stopNag
	rc.runningServices.LiveLimits = rc.liveLimits
	// Attach the boot-stage writers' cancel-and-join handles so both shutdown
	// drains (stopBackgroundWriters on the graceful path, stopAndCleanupServices
	// on the reload/teardown path) can reach them through runningServices.
	rc.runningServices.bootBackgroundCancel = rc.bootBackgroundCancel
	rc.runningServices.bootBackgroundDone = rc.bootBackgroundDone

	// Boot-time browser warm-up steps 1 and 2 — the first TAB and the WebRTC
	// CAPTURE (see startBrowserWarmBoot's block comment). Deliberately here,
	// after setupAndStartServices: both need this gateway's own listener to be
	// accepting, since the warm tab lands on the gateway-served start page and
	// the capture's encoder page dials the gateway's loopback capture-ingest
	// WS. Step 0 (the Chrome PROCESS) still runs earlier, where it has nothing
	// to wait for. Returns immediately; nothing joins it, and nothing it does
	// can fail boot.
	startBrowserWarmBoot(rc.ctx, rc.cfg, rc.homePath, rc.agentLoop, rc.runningServices.browserWS)

	// Surface sandbox state on /health via the existing degraded/check
	// infrastructure. Registering a RegisterCheck puts the {mode, backend,
	// applied} triplet into the /health response body.
	if rc.runningServices.HealthServer != nil && rc.sandboxResult != nil {
		registerSandboxHealthCheck(rc.runningServices.HealthServer, rc.sandboxResult)
	}

	// The /reload trigger + manualReloadChan were wired inside
	// setupAndStartServices BEFORE the listener went live (avoids the
	// boot-ordering race where /reload could 503 "reload not configured").
	// Reuse them here for the reload consumer loop, the agent loop, and the
	// sysagent ReloadFunc — do NOT re-create them.
	rc.manualReloadChan = rc.runningServices.manualReloadChan
	rc.reloadTrigger = rc.runningServices.reloadTrigger
	rc.agentLoop.SetReloadFunc(rc.reloadTrigger)
	return nil, false
}

// prepareSkillServices seeds skills and prepares marketplace-backed skill services.
func (rc *runContextWithOptions) prepareSkillServices() {
	// Wire management tool dependencies into the agent loop (FR-001, FR-002).
	// Called after SetReloadFunc so the reload trigger is available to system
	// tools that trigger hot-reload (e.g., create_agent).
	// WireSysagentDeps immediately registers all 35 management tools on every agent
	// in the current registry and stashes deps for re-application on hot-reload.

	// Build skill engine components for the sysagent tool deps (Spec-6 U1).
	//
	// SkillsLoader: workspace > global (~/.omnipus/skills) > builtin (CWD/skills
	// or OMNIPUS_BUILTIN_SKILLS). Priority order follows context.go::NewContextBuilder.
	skillsBuiltinDir := strings.TrimSpace(os.Getenv(config.EnvBuiltinSkills))
	if skillsBuiltinDir == "" {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			slog.Warn("gateway: os.Getwd failed; builtin skills dir unavailable", "error", wdErr)
			wd = rc.homePath
		}
		skillsBuiltinDir = filepath.Join(wd, "skills")
	}
	skillsWorkspace := rc.cfg.AgentHomeBasePath()
	skillsGlobalDir := filepath.Join(rc.homePath, "skills")

	rc.sysSkillsLoader = skills.NewSkillsLoader(skillsWorkspace, skillsGlobalDir, skillsBuiltinDir)
	// SkillWriter authors/versions skills into the global skills dir so editing a
	// built-in produces a user override rather than mutating the shipped built-in.
	rc.sysSkillWriter = skills.NewSkillWriter(skillsGlobalDir)

	// RegistryManager: fans out search/install to all configured marketplaces
	// (FR-10.1 unified list). The SSRF checker (nil when SSRF is disabled) is
	// injected into the ClawHub HTTP client so outbound registry traffic honors
	// the SSRF policy (SEC-24).
	ssrfChecker := agent.GetSSRFChecker(rc.agentLoop)
	var ssrfClient *http.Client
	if ssrfChecker != nil {
		ssrfClient = ssrfChecker.SafeClient()
	}
	regCfg := skills.RegistryConfig{
		Marketplaces:          skills.MarketplacesFromConfig(rc.cfg, rc.bundle.GetString, ssrfClient, skillsWorkspace),
		MaxConcurrentSearches: rc.cfg.Tools.Skills.MaxConcurrentSearches,
	}
	rc.sysRegistryManager = skills.NewRegistryManagerFromConfig(regCfg)

	// SkillInstaller is handed to the sysagent tool deps (Deps.SkillInstaller);
	// since remove_skill was retired (skills spec r15 §5.1) no skill tool calls
	// it. skills.SkillInstaller.Uninstall joins
	// "skills" onto its own root internally (see pkg/skills/installer.go:
	// NewSkillWriter(filepath.Join(si.workspace, "skills"))), so the value
	// passed here must be the PARENT of the global skills directory — i.e.
	// homePath ($OMNIPUS_HOME) — not skillsGlobalDir itself (that would
	// double-join to $OMNIPUS_HOME/skills/skills) and not skillsWorkspace
	// ($OMNIPUS_HOME/workspace, a different, effectively-empty directory in
	// the common case).
	//
	// ADR-046 FR-009 made install_skill (the chat tool,
	// pkg/tools/skills_install.go) target skillsGlobalDir unconditionally —
	// the same directory sysSkillsLoader's "global" tier above and
	// sysSkillWriter both already use — so a SkillInstaller resolving
	// anywhere else can never find what install_skill actually wrote.
	//
	// GitHub token/proxy from the first github marketplace entry (optional;
	// empty → unauthenticated API calls).
	githubToken, githubProxy := skills.FirstGitHubMarketplaceCreds(rc.cfg, rc.bundle.GetString)
	rc.sysSkillInstaller, rc.err = skills.NewSkillInstallerWithSSRF(
		rc.homePath, githubToken, githubProxy, ssrfChecker,
	)
	if rc.err != nil {
		slog.Warn("gateway: could not create skill installer",
			"error_type", fmt.Sprintf("%T", rc.err))
		rc.sysSkillInstaller = nil
	}
}

// sysagentFastUpsert is the agent tools' publish hook (create_agent,
// update_agent, set_agent_permissions): it re-derives the agent roster from the
// entity store, then republishes ONE agent into the live registry — so a
// permission change made by an agent tool takes effect on the next turn
// without a restart. It falls back to a full reload when one is already
// pending or the fast path fails.
func sysagentFastUpsert(al *agent.AgentLoop, home string, reloadTrigger func() error) func(string) error {
	return func(agentID string) error {
		if al.IsReloadPending() {
			return reloadTrigger()
		}
		if err := al.MutateConfig(func(cfg *config.Config) error {
			return populateAgentsListFromEntityStoreStrict(cfg, home)
		}); err != nil {
			slog.Warn("gateway: sysagent fast agent upsert: could not refresh the agent roster "+
				"from the entity store; falling back to full reload",
				"agent_id", agentID, "error", err)
			return reloadTrigger()
		}
		if _, err := al.UpsertAgentFast(al.GetConfig(), agentID); err != nil {
			slog.Warn("gateway: sysagent fast agent upsert failed; falling back to full reload",
				"agent_id", agentID, "error", err)
			return reloadTrigger()
		}
		return nil
	}
}

// wireSystemTools wires system-tool dependencies and refreshes the central builtin registry.
func (rc *runContextWithOptions) wireSystemTools() {
	sysAgentDeps := &systools.Deps{
		Home:         rc.homePath,
		ConfigPath:   rc.configPath,
		GetCfg:       rc.agentLoop.GetConfig,
		MutateConfig: rc.agentLoop.MutateConfig,
		SaveConfigLocked: func(c *config.Config) error {
			return config.SaveConfig(rc.configPath, c)
		},
		// omnipus-system-agent FR-085: tool writes to providers, MCP servers
		// and channel instances take the Settings config write lock first
		// (config lock -> agent-loop lock, the Settings order).
		LockConfigWrites: func() func() { return lockConfigWrites(rc.runningServices) },
		CredStore:        rc.credStore,
		ReloadFunc:       rc.reloadTrigger,
		// channels-spec-r8 FR-020/FR-030: set_channel_outbound calls the same
		// writer as PUT /api/v1/channels/{id}/outbound, resolved at call
		// time through the one restAPI.
		ApplyChannelOutbound: func(ctx context.Context, change systools.ChannelOutboundChange) (systools.ChannelOutboundSaved, error) {
			if rc.runningServices == nil || rc.runningServices.restAPIRef == nil {
				return systools.ChannelOutboundSaved{}, errors.New("the outbound writer is not wired")
			}
			return rc.runningServices.restAPIRef.applyOutboundChangeForTool(ctx, change)
		},
		// Channels Q2-B: the read-only preview before the approval card and
		// the audit of the tool's own refusals before it.
		PreviewChannelOutbound: func(ctx context.Context, change systools.ChannelOutboundChange) (systools.ChannelOutboundPreview, error) {
			if rc.runningServices == nil || rc.runningServices.restAPIRef == nil {
				return systools.ChannelOutboundPreview{}, errors.New("the outbound preview is not wired")
			}
			return rc.runningServices.restAPIRef.previewOutboundChange(ctx, change)
		},
		AuditChannelOutboundRefusal: func(ctx context.Context, change systools.ChannelOutboundChange, reason string) {
			if rc.runningServices == nil || rc.runningServices.restAPIRef == nil {
				slog.Error("gateway: channel outbound refusal audit is not wired", "channel", change.ID, "reason", reason)
				return
			}
			rc.runningServices.restAPIRef.auditOutboundRefusalBeforeApproval(ctx, change, reason)
		},
		// EnsureWorkspaceMains (final-gate H2): create_agent's team join gives the
		// new member its main chat through the workspace-save path. Resolved at
		// call time through the one restAPI (the same method boot stores in
		// runningServices.ensureWorkspaceMains), so the hook never depends on
		// which of the two was wired first (FR-041).
		EnsureWorkspaceMains: func(workspaceID string) error {
			if rc.runningServices == nil || rc.runningServices.restAPIRef == nil {
				return errors.New("workspace main provisioning is not wired")
			}
			return rc.runningServices.restAPIRef.ensureWorkspaceMains(workspaceID)
		},
		UnbindWorkspaceChannels: func(workspaceID string) ([]string, error) {
			if rc.runningServices == nil || rc.runningServices.restAPIRef == nil {
				return nil, errors.New("workspace channel cleanup is not wired")
			}
			return unbindChannelInstancesForWorkspace(rc.runningServices.restAPIRef, workspaceID)
		},
		// DEL-10: the delete_agent cascade uses the loop's one live store and stops
		// each session first.
		DeleteSessionStore: func() systools.AgentSessionStore {
			if s := rc.agentLoop.GetSessionStore(); s != nil {
				return s
			}
			return nil
		},
		StopSessionForDelete: rc.runningServices.stopSessionForDelete,
		// WaitForReloadFunc: AgentDeleteTool's synchronous reload wait (see
		// systools.Deps.WaitForReloadFunc's doc comment for the delete_agent →
		// list_agents ghost-listing race this closes). Built on the same
		// TriggerReload + IsReloadPending polling primitive that backs
		// restAPI.triggerReloadAndWait, so a delete_agent tool call blocks for
		// exactly as long as REST's DELETE /api/v1/agents/{id} does before
		// either call reports success.
		WaitForReloadFunc: func() error { return waitForReload(rc.agentLoop, rc.runningServices.reloadOutcome) },
		// WaitForPendingReloadFunc (agent-picker-freshness fix, #1009): the
		// wait-only IsReloadPending poll for the create/update publish path
		// (publishAgentActivation). NEVER calls TriggerReload / reloadTrigger
		// — on the fast-path-success branch (UpsertAgentFastFunc updated the
		// live registry inline, nothing queued) this returns immediately,
		// where WaitForReloadFunc above would unconditionally kick off a
		// fresh, unnecessary full reload cycle (channels/cron/plan-engine/
		// scheduler restart cascade, the exact ~60s-under-load mechanism
		// issue #571 exists to avoid). On the fallback-reload branch (the
		// closure above fell back to rc.reloadTrigger()), beginReload has
		// already marked the pending flag under its own mutex before this
		// is reached, so the poll waits out the actually-queued reload. See
		// systools.Deps.WaitForPendingReloadFunc's doc comment for the full
		// safety argument and the round-3-vs-round-4 distinction.
		WaitForPendingReloadFunc: func() error { return waitForPendingReload(rc.agentLoop) },
		// UpsertAgentFastFunc (issue #571, sysagent half): mirrors rest.go's
		// fastAgentUpsert so system.agent.create/update (an agent creating or
		// updating another agent) gets the same fast-path publish REST
		// already has, instead of triggering the full restartServices
		// cascade (channels/cron/plan-engine/schedulers) on every call. Defers
		// to an already in-flight full reload rather than racing it, exactly
		// like fastAgentUpsert's own IsReloadPending check; any fast-path
		// failure falls back to the full reload trigger so the caller here
		// still gets a single hot-reload call site to invoke.
		//
		// Unlike the REST path — where updateConfigJSONLocked's
		// refreshConfigAndRewireServices has ALREADY re-derived
		// cfg.Agents.List from the entity store by the time fastAgentUpsert
		// runs — the sysagent tools (AgentCreateTool/AgentUpdateTool)
		// persist straight to the entity store (agentstore.Store) and never
		// touch config.json or al.cfg at all (ADR-054: agents are per-entity
		// records, not config.json's agents.list). So al.cfg.Agents.List
		// would still be missing the just-created/updated agent here unless
		// this closure re-derives it first. Reuses
		// populateAgentsListFromEntityStoreStrict — the SAME in-memory,
		// no-service-restart "list agents from the entity store" step the
		// REST refresh performs — via MutateConfig so the read-modify-write
		// is serialized with every other al.mu-guarded reader/writer, rather
		// than reloading config.json from disk or re-resolving credentials
		// (neither of which agent CRUD via the entity store can affect).
		UpsertAgentFastFunc: sysagentFastUpsert(rc.agentLoop, rc.homePath, rc.reloadTrigger),
		// agent-picker-freshness fix (#1009): create_agent's own counterpart
		// to the REST createAgent handler's emitAgentCreated call — see
		// systools.Deps.NotifyAgentCreated's doc comment. restAPIRef is
		// already populated by setupAndStartServices (rc.startServices,
		// called above this method) by the time wireSystemTools runs;
		// emitAgentCreated is itself nil-safe (agentCreatedBroadcast not
		// yet wired), so the extra nil check here only guards a restAPIRef
		// that a test harness constructed without going through boot at all.
		NotifyAgentCreated: func(agentID string) {
			if rc.runningServices != nil && rc.runningServices.restAPIRef != nil {
				rc.runningServices.restAPIRef.emitAgentCreated(agentID)
			}
		},
		SkillsLoader:      rc.sysSkillsLoader,
		RegistryManager:   rc.sysRegistryManager,
		SkillInstaller:    rc.sysSkillInstaller,
		SkillWriter:       rc.sysSkillWriter,
		SkillGrants:       newDeleteGrantEditor(rc.homePath),
		ResolveToolPolicy: rc.agentLoop.ResolveRegisteredToolPolicy,
		GodModeActive:     func() bool { return agent.GodModeActive(rc.agentLoop.GetConfig()) },
		// ADR-057 U9 changed ListAllSessions' signature to
		// (limit, offset int, parentSessionID string, flat bool), so it can no
		// longer be assigned here as a bare method value — this field's type
		// is the zero-arg func() ([]*session.UnifiedMeta, []error) shape
		// get_usage (the sole consumer) actually needs. See
		// u25AllSessionsForUsage's doc comment for why flat=true is required
		// here (FR-104: roots-only would silently under-count delegated
		// children's token spend).
		ListSessions: u25AllSessionsForUsage(rc.agentLoop),
		// Live MCP reconciliation: without these, add/remove_mcp_server
		// only persist config — the server never actually connects and its tools never
		// reach the central/per-agent registries until the next hot reload or process restart.
		ReconcileMCP: rc.agentLoop.ReconcileMCP,
		MCPStatus:    rc.agentLoop.MCPServerStatus,
		AgentConfigInventory: func() systools.AgentConfigInventory {
			return sysagentAgentConfigInventory(rc)
		},
		ToolCatalog: func() []tools.Tool {
			return sysagentToolCatalog(rc)
		},
		AgentIsLive: func(id string) bool {
			return sysagentAgentIsLive(rc, id)
		},
		AgentActiveRevision: func(id string) string {
			return sysagentAgentActiveRevision(rc.agentLoop, rc.homePath, id)
		},
	}
	// FR-031: configure_provider's model-listing check calls an operator- or
	// agent-supplied endpoint; it goes through the same SSRF guard as the
	// gateway's own provider listings. Assigned only when non-nil so a
	// disabled guard is never a typed-nil interface.
	if chk := agent.GetSSRFChecker(rc.agentLoop); chk != nil {
		sysAgentDeps.URLChecker = chk
	}
	rc.agentLoop.WireSysagentDeps(sysAgentDeps)

	// M16: update the central BuiltinRegistry with fully-wired deps now that
	// sysAgentDeps is available. Re-populate with real deps so Execute paths
	// (if ever routed through the central registry) have valid deps.
	// The registry created before setupAndStartServices used nil deps for
	// the shape/name/description metadata only; swap to real deps here.
	//
	// Fix #350 (SC-108): also re-register general-builtin metadata and propagate
	// the updated registry to restAPI.builtinRegistry. Without this the restAPI
	// (constructed inside setupAndStartServices) would retain the pre-sysAgentDeps
	// registry. The restAPIRef field was stored by setupAndStartServices exactly
	// for this late-wire step.
	var builtinCounts centralBuiltinCounts
	rc.centralBuiltinReg, builtinCounts = buildCentralBuiltinRegistry(sysAgentDeps)
	// Propagate the updated registry to the already-constructed restAPI (SC-108 fix).
	if rc.runningServices.restAPIRef != nil {
		rc.runningServices.restAPIRef.builtinRegistry = rc.centralBuiltinReg
	}
	slog.Info("gateway: central BuiltinRegistry re-populated with live deps",
		"system_tools", builtinCounts.system,
		"general_builtins", builtinCounts.general,
		"browser_builtins", builtinCounts.browser,
		"knowledge_builtins", builtinCounts.knowledge,
		"total", builtinCounts.total())

	// centralBuiltinReg was just reassigned to a fresh instance above — re-wire
	// it (and centralMCPReg, unchanged but re-asserted for clarity) into the
	// agent loop so MCP name-collision admission (ValidateMCPName) checks
	// against the live-deps builtin instance, not the pre-deps one the earlier
	// SetCentralMCPRegistries call saw.
	rc.agentLoop.SetCentralMCPRegistries(rc.centralMCPReg, rc.centralBuiltinReg)
}

// announceStartup announces startup and creates the agent-loop cancellation context.
func (rc *runContextWithOptions) announceStartup() {
	fmt.Printf("✓ Gateway started on %s:%d\n", rc.cfg.Gateway.Host, rc.cfg.Gateway.Port)
	fmt.Println("Press Ctrl+C to stop")

	// agentLoopCtx is canceled if the agent loop exits unexpectedly (e.g. panic
	// recovery). The outer select below treats this the same as ctx cancellation.
	rc.agentLoopCtx, rc.agentLoopCancel = context.WithCancel(rc.ctx)
}

// startBackgroundServices starts the agent loop and retention, recap, and session-message background services.
func (rc *runContextWithOptions) startBackgroundServices() {
	// agentLoopDead is set when the agent loop exits (normally or via panic).
	// The /health endpoint returns 503 when this flag is set, signaling to
	// load-balancers and monitors that the gateway is no longer functional.

	go func() {
		defer rc.agentLoopCancel()
		defer func() {
			if r := recover(); r != nil {
				stack := runtimedebug.Stack()
				slog.Error("agent loop panicked — gateway is now non-functional",
					"panic", r, "stack", string(stack))
				// Append to the panic log file so ops can find the crash.
				if f, openErr := os.OpenFile(rc.panicPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600); openErr == nil {
					_, writeErr := fmt.Fprintf(f, "\n\nagent loop panic: %v\n%s\n", r, stack)
					closeErr := f.Close()
					if writeErr == nil {
						writeErr = closeErr
					}
					if writeErr != nil {
						slog.Error("agent loop panic log write failed", "error_type", fmt.Sprintf("%T", writeErr))
					}
				}
				rc.agentLoopDead.Store(true)
			}
		}()
		rc.agentLoop.Run(rc.agentLoopCtx)
	}()

	// Launch the nightly retention sweep goroutine. Uses ctx (not agentLoopCtx)
	// so it shuts down on gateway stop regardless of agent-loop liveness.
	// GetSessionStore returns the shared UnifiedStore; when nil (misconfigured
	// home) the goroutine is a no-op — getCfg returning a nil cfg is guarded
	// inside executeSweepTick.
	// Tool-result file sweep runs alongside the transcript sweep on the same
	// retention window. setupAndStartServices already constructed the store.
	//
	// These assignments MUST stay ABOVE startRetentionSweepLoop. The sweep
	// loop performs a deliberate boot-time sweep BEFORE its first ticker wait
	// (retention_goroutine.go:81), so it reads retentionToolResultSweepFn AND
	// retentionTaskRunSweepFn immediately — both are bare package-level funcs
	// with no mutex or atomic. Assigning either one after the goroutine was
	// launched is a real data race, caught by `-race` on tests/integration
	// once the package became race-buildable:
	//
	//	Read at … executeSweepTick() retention_goroutine.go:145 (tool-result var)
	//	Previous write at … RunContextWithOptions() gateway.go:1955
	//
	// The user-visible symptom is the boot-time sweep nondeterministically
	// observing nil and silently skipping — which executeSweepTick's own
	// comment used to excuse as "tests with a disabled toolStore" for the
	// tool-result var. Ordering is sufficient rather than a mutex: these are
	// the ONLY writers in the tree, so once the goroutine starts neither
	// value ever changes.
	//
	// REGRESSION (found while chasing an unrelated tests/integration -race
	// failure, 2026-08-07): retentionTaskRunSweepFn used to be wired in its
	// own block AFTER startRetentionSweepLoop below — reintroducing the
	// EXACT race this comment already documents fixing, just for the sibling
	// var (WARNING: DATA RACE, retention_goroutine.go:161 vs this file's
	// former line 2335). Per-task run-history pruning (ADR-050 RD9/RD10,
	// pkg/gateway/retention_task_runs.go) could nondeterministically no-op on
	// the very first boot-time tick in production, not just under -race —
	// the race detector only makes an existing ordering bug loud, it doesn't
	// create the bug. Moved up alongside the tool-result wiring so both
	// hooks are fully wired before the sweep goroutine can ever read them.
	if rc.runningServices.toolStore != nil {
		retentionToolResultSweepFn = rc.runningServices.toolStore.retentionSweep
	}
	// Per-task run-history prune + stuck-run reaper (ADR-050 RD9/RD10) runs
	// alongside the transcript sweep on the same retention window/cadence —
	// see pkg/gateway/retention_task_runs.go. GetTaskStore returns nil only
	// when the task store failed to initialize; the tick no-ops via the nil
	// check in executeSweepTick when that happens.
	if tStore := agent.GetTaskStore(rc.agentLoop); tStore != nil {
		retentionTaskRunSweepFn = func(cutoff time.Time) (int, error) {
			return pruneAllTaskRuns(tStore, cutoff)
		}
	}
	if sharedStore := rc.agentLoop.GetSessionStore(); sharedStore != nil {
		startRetentionSweepLoop(rc.ctx, sharedStore, rc.agentLoop.GetConfig, 24*time.Hour)
	}

	// FR-031: Launch the nightly retro sweep goroutine alongside the session sweep.
	// Iterates all agents and calls SweepRetros per MemoryStore.
	startRetentionRetroSweepLoop(rc.ctx, rc.agentLoop, rc.agentLoop.GetConfig, 24*time.Hour)

	// FR-032/FR-032a: Bootstrap recap pass — on gateway start, re-cap sessions
	// that lack a retro. Runs once in a goroutine.
	go rc.agentLoop.BootstrapRecapPass(rc.ctx)

	// ADR-053 Phase 2 on-ramp: start the SessionMessageChan kind-router
	// consumer. This is the MISSING consumer for the bus's 4th channel — until
	// it runs, SessionMessageChan() has no drainer and any publisher would
	// block. The consumer dispatches by kind: steer/respond → child steering
	// queue; child->parent kinds → durable inbox Append + bounded typed wake;
	// engine kinds → WS frame. Bound to ctx so it shuts down with the gateway.
	// Honors session_messaging.enabled live (read per event) — when false the
	// consumer drains-and-discards (channel stays unblocked) but no-ops.
	rc.smConsumerCancel = rc.agentLoop.StartSessionMessageConsumer(rc.ctx)
}

// configureHealthAndWatcher configures health reporting and starts the configuration watcher.
func (rc *runContextWithOptions) configureHealthAndWatcher() {
	// Wire a second degraded check: report 503 when the agent loop has died.
	rc.runningServices.HealthServer.SetDegradedFunc(func() (bool, string) {
		if rc.agentLoopDead.Load() {
			return true, "agent loop exited unexpectedly — gateway requires restart"
		}
		if degraded, reason := rc.runningServices.reloadDegradedStatus(); degraded {
			return true, reason
		}
		return false, ""
	})

	// B1.2(f): /health surfaces audit-degraded mode. The closure reports
	// whether the agent loop currently has a non-nil audit logger so /health
	// can flag "audit_logger: unavailable" without exposing the pointer.
	rc.runningServices.HealthServer.SetAuditLoggerAvailableFunc(func() bool {
		return rc.agentLoop.AuditLogger() != nil
	})
	// audit_degraded should only fire when the operator asked for audit AND
	// it isn't working. cfg.Sandbox.AuditLog=false is a deliberate off-state.
	rc.runningServices.HealthServer.SetAuditLoggerConfiguredFunc(func() bool {
		return rc.agentLoop.GetConfig().Sandbox.AuditLog
	})
	// ADR-067 FR-037 (T067-10): /health reports the provider catalog
	// degraded — with the last refresh error — whenever no document is
	// loaded, the last refresh failed, or the served document is stale
	// (updated_at older than 14 days). Like audit_degraded it is a FIELD,
	// not a 503: an out-of-date registry snapshot makes the model picker
	// less accurate, it does not stop the gateway serving turns.
	rc.runningServices.HealthServer.SetCatalogStateFunc(func() (bool, string) {
		return catalogHealthState(rc.providerCatalog)
	})

	rc.stopWatch = func() {}
	if rc.cfg.Gateway.HotReload {
		rc.configReloadChan, rc.stopWatch = setupConfigWatcherPolling(
			rc.configPath,
			rc.homePath,
			rc.debug,
			rc.credStore,
			rc.runningServices.selfWriteReg,
			rc.runningServices.markReloadDegraded,
			rc.runningServices.reloadOutcome,
		)
		logger.Info("Config hot reload enabled")
	}
}

// serveReloadLoop loads reload configuration and serves shutdown and reload events.
func (rc *runContextWithOptions) serveReloadLoop() error {
	// loadReloadConfig re-reads config.json for a reload. It backs the /reload
	// path AND every coalesced follow-up reload — the latter MUST re-read from
	// disk, since the whole reason a request was coalesced is that its write
	// post-dates the running reload's snapshot.
	loadReloadConfig := newReloadConfigLoader(rc.configPath, rc.homePath, rc.credStore, rc.runningServices)
	// The swap-time re-read (handleConfigReload, under restAPI.configMu) must
	// read the same file with the same checks as every other reload load.
	rc.runningServices.loadConfigForSwap = loadReloadConfig

	// runOneReload is the production executor handed to runReloadCycle. provider
	// is captured by address because executeReload swaps it in place on success.
	runOneReload := func(c *config.Config) error {
		return executeReload(rc.ctx, rc.agentLoop, c, &rc.provider, rc.runningServices, rc.msgBus, rc.allowEmptyStartup)
	}

	for {
		select {
		case <-rc.agentLoopCtx.Done():
			logger.Info("Shutting down...")
			omnipusGracefulShutdown(rc.runningServices, rc.agentLoop, rc.provider, rc.cfg)
			return nil
		case change := <-rc.configReloadChan:
			if !rc.runningServices.beginReload(rc.agentLoop.MarkReloadPending) {
				// NOT a drop: beginReload recorded the request, and the cycle
				// that owns the in-flight reload will run a follow-up reload
				// that re-reads this file change from disk before it finishes.
				logger.Info("Config reload coalesced into the in-flight reload")
				continue
			}
			runReloadCycle(rc.agentLoop, rc.runningServices, change.cfg, change.readSeq, runOneReload, loadReloadConfig)
		case <-rc.manualReloadChan:
			// The slot was already claimed by reloadTrigger before it signalled
			// this channel, so do NOT call beginReload here — runReloadCycle
			// takes ownership of the release directly.
			logger.Info("Manual reload triggered via /reload endpoint")
			runReloadCycle(rc.agentLoop, rc.runningServices, nil, 0, runOneReload, loadReloadConfig)
		}
	}
}

// newReloadConfigLoader returns the loader every reload uses to read
// config.json: the /reload path, every coalesced follow-up reload, and the
// swap-time re-read handleConfigReload takes under restAPI.configMu.
//
// LoadConfigWithStoreAndSelfHealHook (not LoadConfigWithStore): this path
// bypasses safeUpdateConfigJSON's configMu + selfWriteReg registration, so
// if the single-user-model role self-heal writes config.json here, the write
// must be registered manually or the watcher's next tick would misidentify
// it as an external edit.
func newReloadConfigLoader(
	configPath, homePath string, credStore *credentials.Store, rs *services,
) func() (*config.Config, error) {
	return func() (*config.Config, error) {
		newCfg, err := config.LoadConfigWithStoreAndSelfHealHook(
			configPath, credStore, selfHealWriteHook(rs.selfWriteReg),
		)
		if err != nil {
			return nil, fmt.Errorf("loading config for reload: %w", err)
		}
		// ADR-054 D2/D3: repopulate cfg.Agents.List from the agent store —
		// config.LoadConfig* strips agents.list on every load
		// (legacy_agents_list.go), and this reload path is a separate
		// config-load call site from restAPI.refreshConfigAndRewireServices's
		// own bridge. Strict variant: a roster-population failure here must
		// reject this reload attempt exactly like the config-load/validation
		// failures around it, not silently proceed with an empty/stale roster
		// (see populateAgentsListFromEntityStoreStrict's doc for why) — and mark
		// the service degraded so /health surfaces it.
		if err = populateAgentsListFromEntityStoreStrict(newCfg, homePath); err != nil {
			rs.markReloadDegraded(
				fmt.Errorf("reload rejected: agent roster population failed: %w", err),
			)
			return nil, fmt.Errorf("agent roster population failed: %w", err)
		}
		if err = newCfg.ValidateProviders(); err != nil {
			return nil, fmt.Errorf("config validation failed: %w", err)
		}
		return newCfg, nil
	}
}

// bootProviderCatalog boots the ADR-067 provider catalog this gateway serves:
// the embedded snapshot, the persisted last-known-good under homePath, and
// the release puller the refresh ticker uses later (Boot itself does no
// network I/O). The served projection hides `sign_in` on rows this build
// cannot sign in with (providers.HasSignInMechanism, provider hotfix H1).
func bootProviderCatalog(homePath string) *catalog.Catalog {
	return catalog.Boot(
		context.Background(),
		catalog.EmbeddedSnapshot,
		catalog.NewGHReleasePuller(),
		catalog.NewFileStore(homePath),
		catalogLogAdapter{},
		catalog.WithSignInGate(providers.HasSignInMechanism),
	)
}

// catalogLogAdapter routes catalog.Logger's slog-shaped
// Info/Warn/Error(msg string, args ...any) through pkg/logger instead of
// log/slog.Default(). slog.SetDefault is never called anywhere in this repo,
// so log/slog.Default() sits on its zero-value stderr-only handler —
// invisible on a backgrounded gateway (the documented `./omnipus gateway
// --allow-empty &` launch form), since nothing routes stderr into
// $OMNIPUS_HOME/logs/gateway.log. args follows slog's own
// alternating-key/value convention, which is the shape the catalog package's
// call sites already use.
type catalogLogAdapter struct{}

// catalogRefreshStopTimeout bounds how long shutdown waits for the refresh
// loop to exit after cancelling it. The loop's own attempt context is
// cancelled with it, so an in-flight pull aborts at once; this only guards
// against a wedged transport.
const catalogRefreshStopTimeout = 10 * time.Second

// bootBackgroundStopTimeout bounds each single-writer join wait in
// drainBootBackgroundWriters / waitBootBackgroundDone. Same rationale as
// catalogRefreshStopTimeout: the cancel is already delivered, so this only
// guards against a wedged transport or a mid-download writer whose HTTP
// client is slow to observe the cancelled context.
const bootBackgroundStopTimeout = 10 * time.Second

// stopPollersReporting stops the queued-task and mail pollers. Each Stop
// waits, bounded, for a cycle already in flight; when that wait runs out the
// cycle is still running (it is never cancelled mid-cycle), so the operator
// gets a warning naming the poller, what it may still be doing, and that the
// reload or stop otherwise went ahead.
func stopPollersReporting(runningServices *services, isReload bool) {
	phase := "stopping services"
	if isReload {
		phase = "applying the configuration reload"
	}
	if runningServices.TaskDrain != nil {
		if err := runningServices.TaskDrain.Stop(); err != nil {
			slog.Warn("gateway: the queued-task poller was still in a check after its stop wait; "+phase+
				" continued without it. The check finishes on its own; if this repeats, look for a slow or locked task store.",
				"error", err.Error())
		}
	}
	if runningServices.MailWatch != nil {
		if err := runningServices.MailWatch.Stop(); err != nil {
			slog.Warn("gateway: the mail poller was still in a mail check after its stop wait; "+phase+
				" continued without it. The check finishes on its own; if this repeats, check the mailbox connection.",
				"error", err.Error())
		}
	}
}

func stopAndCleanupServices(runningServices *services, shutdownTimeout time.Duration, isReload bool) {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if !isReload && runningServices.browserWS != nil {
		runningServices.browserWS.closeMediaTransport()
	}

	// reload should not stop channel manager
	if !isReload && runningServices.ChannelManager != nil {
		runningServices.ChannelManager.StopAll(shutdownCtx)
	}
	stopPollersReporting(runningServices, isReload)
	if runningServices.CronService != nil {
		runningServices.CronService.Stop()
	}
	// ADR-067 W3: stop every knowledge drift schedule and close every open
	// collection index. Keyed by $OMNIPUS_HOME rather than carried on
	// runningServices — see knowledgeLifecycles' doc comment.
	//
	// RELOAD MUST NOT STOP IT — same rule as the channel manager above, and
	// for a sharper reason. startKnowledgeLifecycle has exactly ONE production
	// call site (setupAndStartServices, boot only); restartServices does not
	// mention knowledge at all. So a reload that stopped the lifecycle would
	// never get one back, a.knowledgeLifecycle() would return nil for the rest
	// of the process's life, and AttachMountAsync's nil-receiver guard — right
	// for a harness that wires no lifecycle — would turn every later mount
	// into a silent no-op. Mounting a vault would 201 and index nothing, with
	// no error and no log line, until the process restarted. That shipped and
	// was found in manual testing; see
	// docs/internal/design/knowledge-lifecycle-reload-survival-2026-08-24.md.
	//
	// Nothing the lifecycle captured goes stale across a reload: homePath is
	// fixed for the process, wsHandler is never rebuilt, and the drift
	// notifier closes over agentLoop.GetConfig, which reads the CURRENT config
	// off the same *AgentLoop that handleConfigReload mutates in place. If a
	// config key ever needs to reach the lifecycle live (a drift-interval knob
	// is the obvious candidate — see setupAndStartServices), restart it from
	// restartServices rather than deleting this guard.
	if !isReload {
		stopKnowledgeLifecycles()
	}
	if runningServices.PlanEngine != nil {
		runningServices.PlanEngine.Stop()
	}
	// RELOAD MUST NOT STOP IT (tool-parity spec r17 §7.2 TK-30, 18.40): the
	// task-trigger scheduler is not reconstructed on reload, so its queued
	// and running callbacks settle, re-arm and publish into the same live
	// engine, and no reload reads or writes its store. It reads the live
	// task store and executor of the same *AgentLoop, so nothing it captured
	// goes stale across a reload.
	if !isReload && runningServices.TaskTrigger != nil {
		runningServices.TaskTrigger.Stop()
	}
	if runningServices.LoopScheduler != nil {
		runningServices.LoopScheduler.Stop()
	}
	if runningServices.MediaStore != nil {
		if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
			fms.Stop()
		}
	}
	// TempDir-cleanup family (#1644/#1695/#1598): this function is the
	// teardown every setupAndStartServices harness (production boot-abort,
	// reload teardown, and the in-process gateway tests) runs, so it must
	// also drain the boot-stage writers the graceful shutdown's
	// stopBackgroundWriters drains: the provider-catalog refresh loop (cancel
	// + join — its startup pull writes providers_catalog.json at homePath and
	// raced t.TempDir removal), the browser cache-trim schedule, the
	// managed-Chromium preprovision download and the library orphan-GC
	// goroutine (whose boot pass repairs associations under workspaces/).
	// RELOAD MUST NOT STOP THEM — same rule as the channel manager and the
	// knowledge lifecycle above: reload restarts into the same process with
	// these loops still live, and nothing here reconstructs them.
	if !isReload {
		drainBootBackgroundWriters(shutdownCtx, runningServices)
	}
}

// drainBootBackgroundWriters cancels and joins the background writers that
// outlive the services they were started by: the provider-catalog refresh
// loop, the boot-stage browser/preprovision/prime goroutines and the media
// orphan-GC goroutine. Every handle is nil-safe — a services object built by
// a harness that never started a given writer has nothing to drain. Waits
// are bounded by ctx, mirroring stopBackgroundWriters' budgeted waits.
func drainBootBackgroundWriters(ctx context.Context, rs *services) {
	if rs == nil {
		return
	}
	// Cancel everything first, then join, so the writers settle concurrently
	// instead of serializing their exit waits.
	if rs.catalogRefreshCancel != nil {
		rs.catalogRefreshCancel()
	}
	if rs.bootBackgroundCancel != nil {
		rs.bootBackgroundCancel()
	}
	if rs.orphanGCCancel != nil {
		rs.orphanGCCancel()
	}
	if rs.catalogRefreshDone != nil {
		waitBootBackgroundDone(ctx, rs.catalogRefreshDone, "provider-catalog refresh loop")
	}
	if rs.bootBackgroundDone != nil {
		waitBootBackgroundDone(ctx, rs.bootBackgroundDone, "boot browser/preprovision goroutines")
	}
	if rs.orphanGCDone != nil {
		waitBootBackgroundDone(ctx, rs.orphanGCDone, "media library orphan-GC goroutine")
	}
}

// waitBootBackgroundDone waits for one writer goroutine's done channel,
// bounded by ctx. A writer still running at the deadline has already had its
// cancel delivered and finishes on its own; teardown continues without it —
// the same posture stopBackgroundWriters documents for the catalog loop.
func waitBootBackgroundDone(ctx context.Context, done <-chan struct{}, what string) {
	timer := time.NewTimer(bootBackgroundStopTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		slog.Warn("gateway: boot background writer did not exit in time; continuing",
			"what", what, "timeout", bootBackgroundStopTimeout.String())
	case <-ctx.Done():
		slog.Warn("gateway: boot background writer did not exit within the shutdown budget; continuing",
			"what", what, "error", ctx.Err())
	}
}

// browserCacheTrimReconcileEvery bounds how long a LIVE change to
// tools.browser.cache_trim_interval waits before the sweep schedule follows it.
//
// The schedule used to be a time.Ticker built once, at boot, from
// pool.CacheTrimInterval(). A Settings save reached the POOL (loop.go's reload
// pass calls BrowserPool.ApplyRuntimeConfig, which updates the interval) and
// stopped there: the ticker had already been armed and never re-read it. An
// operator lowering the interval because their disk was filling saw the setting
// accepted, saw the new value read back, and got the old hourly sweep — the
// ADR-037 "reports success and changes nothing" anti-pattern this project bans,
// and one that docs/configuration.md contradicted in writing.
//
// It is a RECONCILE BOUND, not the sweep period: the loop below wakes at most
// this often purely to re-read the configured interval, and sweeps only when the
// interval has genuinely elapsed since the last sweep. Fifteen seconds is chosen
// to be far below any plausible trim interval (the default is an hour) while
// costing one timer wakeup and two clock reads per quarter-minute. A var, not a
// const, so a test can drive the reconcile without sleeping for real time.
var browserCacheTrimReconcileEvery = 15 * time.Second

// minBrowserCacheTrimInterval floors the effective sweep period. The pool
// already substitutes its own default for a zero or negative interval, so this
// only guards against a future pool that stops doing so turning the loop below
// into a spin. A one-second floor is not a policy about how often to trim; it is
// the smallest gap at which "wake, compare, sweep" is still a schedule.
const minBrowserCacheTrimInterval = time.Second

// browserCacheTrimScheduler is the narrow slice of *browser.BrowserPool the
// scheduled cache trim needs. Declared so the schedule can be tested against a
// fake whose interval an operator's config.json really drives, without a Chrome,
// a pool, or a profile directory anywhere in the test.
type browserCacheTrimScheduler interface {
	TrimAllEligible() []browser.TrimResult
	CacheTrimInterval() time.Duration
}

// runBrowserCacheTrimSchedule is ADR-075 FR-072 triggers 2 and 3: the boot sweep
// of every profile with no live Chrome, then the recurring sweep.
//
// The interval is re-read from the pool on EVERY round rather than captured
// once, which is what makes tools.browser.cache_trim_interval a live setting.
// Lowering it takes effect within browserCacheTrimReconcileEvery, including when
// the new interval has ALREADY elapsed since the last sweep — the next
// reconcile finds the sweep overdue and runs it immediately, rather than serving
// out the remainder of the old, longer wait.
//
// Returns when ctx is done (the gateway's own shutdown-aware context).
func runBrowserCacheTrimSchedule(ctx context.Context, pool browserCacheTrimScheduler) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("browser-trim: cache trim panicked; skipped", "panic", fmt.Sprintf("%v", r))
		}
	}()
	if results := pool.TrimAllEligible(); len(results) > 0 {
		slog.Info("browser-trim: trimmed closed workspace browser caches at boot",
			"profiles", len(results))
	}

	last := time.Now()
	for {
		interval := pool.CacheTrimInterval()
		if interval < minBrowserCacheTrimInterval {
			interval = minBrowserCacheTrimInterval
		}
		due := last.Add(interval)
		wait := time.Until(due)
		if wait > browserCacheTrimReconcileEvery {
			wait = browserCacheTrimReconcileEvery
		}
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if time.Now().Before(due) {
			// Woke to reconcile, not to sweep. Round again and re-read the
			// interval — this is the branch a lowered setting arrives through.
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("browser-trim: scheduled trim panicked; paused until the next tick",
						"panic", fmt.Sprintf("%v", r))
				}
			}()
			if results := pool.TrimAllEligible(); len(results) > 0 {
				slog.Info("browser-trim: trimmed closed workspace browser caches",
					"profiles", len(results))
			}
		}()
		last = time.Now()
	}
}
