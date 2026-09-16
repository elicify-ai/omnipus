// gateway_boot.go: Boot - unlock credentials, load souls, seed the roster, build services

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
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
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/daemon"
	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/devices"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/heartbeat"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/policy"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/state"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/voice"
)

// sweepOrphanedProviderCredentials is T068-10's startup sweep (ADR-068
// FR-010 last clause, D14.2): delete any `<id>_API_KEY` credential whose
// provider row is gone from cfg.Providers — greenfield housekeeping for the
// one gap DELETE /providers/{id} cannot close on its own (a crash between
// its step 2 config write and step 3 credential delete leaves an orphaned
// secret; the retry story assumes the operator retries, boot must not).
// Runs once per boot, after config load + credential-store unlock, as soon
// as the audit logger exists.
//
// It also sweeps orphaned device-code OAuth entries (`<vendor>_OAUTH`,
// ADR-068 FR-007) — a signed-in provider's stored access AND refresh token
// for the operator's real vendor account, which is the MORE sensitive of
// the two secrets a provider row can own. Two rules make this safe:
//
//   - the key is the VENDOR, not the provider id. providers.OAuthVendorID
//     maps openai-chatgpt → openai, and a single vendor entry may legitimately
//     back several configured rows, so an entry is swept only when NO
//     configured row maps to its vendor;
//   - the conservative direction differs from the API-key case and is
//     stated deliberately. Wrongly deleting an `_API_KEY` is unrecoverable
//     (Omnipus cannot re-mint the operator's key), which is why that half
//     stays as narrow as it is. Wrongly deleting an `_OAUTH` entry costs one
//     "Sign in" click — while LEAVING one costs a live, unrevokable grant
//     nothing in the UI references any more.
//
// The pattern rule (BDD "a <name> that does not match the `<id>_API_KEY`
// pattern is left untouched") is deliberately conservative — wrongly
// deleting a live secret is unrecoverable, failing to sweep is harmless:
//
//   - only names ending in exactly `_API_KEY` with a provider-id-shaped
//     `<id>` prefix (lowercase/digits/[-_.], ≤64 — the shape onboarding and
//     PUT /providers/{id} write via `provider.Id+"_API_KEY"`) are eligible.
//     This leaves the ALL-UPPERCASE integration refs (BRAVE_API_KEY,
//     GROQ_API_KEY, ELEVENLABS_API_KEY, …) and every channel/mailbox secret
//     (`channel_<id>_<field>`, `mailbox_…_password`) untouched;
//   - a name whose `<id>` matches a configured row's Provider is kept;
//   - belt-and-braces: a name ANY provider row's api_key_ref points at is
//     kept even when no row id matches its prefix.
//
// Never fatal: a locked store, a List failure, or a Delete failure is logged
// and boot proceeds — the sweep is housekeeping, not a boot gate. A nil
// auditor (sandbox.audit_log disabled) skips only the audit emission.
// onboardingStateUnreadable reports whether the onboarding state file exists
// but cannot be turned into a trustworthy answer to "has this instance been
// onboarded?" — it is unreadable (permissions, I/O error, a directory in its
// place) or it is not valid JSON.
//
// This MUST be called before onboarding.NewManager (M3): that constructor
// swallows both cases into OnboardingComplete=false and, for the parse
// failure, renames the file aside — after which a caller can no longer tell a
// corrupted long-onboarded instance from a genuine first launch, and the
// FR-050 pre-auth provider routes reopen unauthenticated for the process
// lifetime.
//
// A MISSING file is NOT unknown: absence is exactly what a real fresh install
// looks like, and returning true for it would break every first launch.
//
// Structural validity (json.Valid) is the whole test on purpose. Whether the
// parsed document says complete or incomplete is the manager's business; this
// function answers only "is the manager's answer derived from real data?".
func onboardingStateUnreadable(home string) bool {
	statePath := filepath.Join(home, "system", "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false // never onboarded — the genuine fresh-install case
		}
		slog.Warn("gateway: onboarding state unreadable — pre-auth provider routes will stay closed",
			"path", statePath, "error", err)
		return true
	}
	if !json.Valid(data) {
		slog.Warn("gateway: onboarding state is not valid JSON — pre-auth provider routes will stay closed",
			"path", statePath)
		return true
	}
	return false
}

// installSlogBridge installs logger.NewSlogHandler() as log/slog's
// process-wide default handler, so every bare `slog.Warn/Info/Error(...)`
// call site — ~1200 of them, across this package and every other package the
// gateway process links in (agent loop, sysagent tools, media library,
// etc.) — forwards into pkg/logger's zerolog sink instead of silently
// writing to log/slog.Default()'s zero-value stderr-only handler.
//
// Nothing in this repo calls slog.SetDefault in production code without
// this: on the documented backgrounded launch form (`./omnipus gateway
// --allow-empty &`), stderr is not captured anywhere, so every bare slog
// call was permanently invisible. bootLoggingAndDataModel calls this
// immediately after logger.EnableFileLogging succeeds and BEFORE
// datamodel.Init runs — the earliest subsystem boot code in the process that
// itself calls bare slog (see datamodel.Init's first-run slog.Info) — so
// every downstream slog call for the rest of this process's life lands in
// $OMNIPUS_HOME/logs/gateway.log (or wherever EnableFileLogging pointed).
//
// Re-review FIX 1 (two independent reviewers): this used to be called from
// RunContextWithOptions AFTER datamodel.Init, even though this doc comment
// already claimed "before any subsequent subsystem boot code has a chance
// to log." That claim was false by exactly the 19 lines separating the two
// calls — datamodel.Init's own first-run slog.Info/Debug calls (the single
// most operator-relevant first-run line: "default config written") fired
// before the bridge existed AND before logger.InitPanic's stderr redirect,
// so on a fresh install that line reached neither gateway.log nor
// gateway_panic.log nor anywhere else durable. Fixed by extracting the
// ordering-sensitive boot preamble into bootLoggingAndDataModel, which both
// RunContextWithOptions and the ordering test below call, so the two cannot
// silently drift apart again the way the inline call sites did.
//
// Deliberately not restored via defer on shutdown: the orphan-GC ticker
// started later in RunContextWithOptions (and any other long-lived
// background goroutine started during boot) keeps calling bare slog after
// RunContextWithOptions itself returns in an in-process test rerun, and
// those calls should stay bridged for as long as they run rather than
// reverting the instant this function unwinds. Production processes never
// return from RunContextWithOptions except at actual process exit, where
// restoring the previous default would have no observable effect anyway.
//
// See logger.SlogHandler's doc comment for the level/attribute mapping, and
// slog_bridge_wiring_test.go for the file-appears-in-gateway.log proof
// (both with and without this function called).
func installSlogBridge() {
	slog.SetDefault(slog.New(logger.NewSlogHandler()))
}

// bootLoggingAndDataModel runs logger.EnableFileLogging, installSlogBridge,
// and datamodel.Init — in that order — so datamodel.Init's own first-run
// slog.Info/Debug calls are bridged and file-logged instead of hitting
// log/slog's zero-value stderr-only default the way they used to.
//
// RunContextWithOptions calls this immediately after creating
// $OMNIPUS_HOME/logs and initializing the panic log (both of which stay
// inline in RunContextWithOptions — see the comment there for why): this
// helper is deliberately the minimal slice of the boot preamble that (a) is
// order-sensitive for the slog-visibility bug and (b) is safe to exercise
// directly from an in-process test. logger.InitPanic is NOT part of this
// helper on purpose: it Dup2's the real process stderr fd into the panic
// log file, a process-wide side effect with no built-in undo — calling it
// from a test would silently redirect the shared pkg/gateway test binary's
// stderr for the remainder of that test run. Excluding it costs nothing for
// THIS bug: InitPanic never touches log/slog, so its position relative to
// EnableFileLogging/installSlogBridge/datamodel.Init has no bearing on
// whether datamodel.Init's slog calls reach gateway.log.
//
// datamodel.Init has no dependency on anything logger.EnableFileLogging or
// installSlogBridge set up — it only calls bare slog, and creates its own
// directories independently of the logging path — so this reorder is safe.
//
// Both RunContextWithOptions and
// TestBootOrder_DataModelInitLogsReachGatewayLog (boot_order_test.go) call
// this helper so a future refactor of one cannot silently drift the order
// away from the other, the way the two inline call sites did before this
// fix (two independent reviewers plus a third verifying pass all flagged
// the same bug: this function used to be called AFTER datamodel.Init).
func bootLoggingAndDataModel(homePath string) error {
	logsDir := filepath.Join(homePath, logPath)

	// Preserves the original inline call site's panic-on-failure behavior
	// (predates this fix, not itself part of the FIX 1 ordering change) —
	// EnableFileLogging failing this early means no durable log sink exists
	// to report the failure into.
	if err := logger.EnableFileLogging(filepath.Join(logsDir, logFile)); err != nil {
		panic(fmt.Errorf("error enabling file logging: %w", err))
	}

	// Bridge log/slog's process-wide default into pkg/logger's zerolog sink
	// (console + $OMNIPUS_HOME/logs/gateway.log, just enabled above) — see
	// installSlogBridge's doc comment for why. Installed BEFORE
	// datamodel.Init so its first-run slog calls are captured too.
	installSlogBridge()

	if err := datamodel.Init(homePath); err != nil {
		return fmt.Errorf("directory initialization failed: %w", err)
	}
	return nil
}

// The production values for the ADR-067 FR-008 background refresh: one pull
// every 24 h, each attempt bounded to 30 s, and a startup pull skipped
// entirely when the persisted last-known-good was written less than an hour
// ago.
const (
	catalogRefreshInterval   = 24 * time.Hour
	catalogRefreshTimeout    = 30 * time.Second
	catalogStartupSkipWindow = time.Hour
)

// setupAndStartServicesState carries the shared state of setupAndStartServices across its stages.
type setupAndStartServicesState struct {
	ctx                    context.Context
	cfg                    *config.Config
	bundle                 credentials.SecretBundle
	agentLoop              *agent.AgentLoop
	msgBus                 *bus.MessageBus
	homePath               string
	credStore              *credentials.Store
	sandboxResult          *SandboxApplyResult
	builtinReg             *tools.BuiltinRegistry
	mcpReg                 *tools.MCPRegistry
	allowGodMode           bool
	runningServices        *services
	err                    error
	allowedOrigin          string
	wsHandler              *WSHandler
	approvalReg            *approvalRegistryV2
	onboardingStateUnknown bool
	onboardingMgr          *onboarding.Manager
	tStore                 *task.Store
	tExecutor              *agent.TaskExecutor
	planStore              *plan.Store
	lifecycleStore         *session.LifecycleStore
	intentLog              *plan.IntentLog
	bootSweepCfg           config.PlanningConfig
	providerCatalog        *catalog.Catalog
	api                    *restAPI
}

func setupAndStartServices(
	ctx context.Context, // gateway's own shutdown-aware context (RunContextWithOptions' ctx) — threaded through so background loops started here (e.g. runCatalogRefreshLoop) can observe cancellation instead of running untethered for the life of the process.
	cfg *config.Config,
	bundle credentials.SecretBundle,
	agentLoop *agent.AgentLoop,
	msgBus *bus.MessageBus,
	homePath string,
	credStore *credentials.Store,
	sandboxResult *SandboxApplyResult,
	builtinReg *tools.BuiltinRegistry, // M16: central builtin registry (FR-001)
	mcpReg *tools.MCPRegistry, // M16: central MCP registry (FR-001)
	allowGodMode bool,
) (rs *services, retErr error) {
	stg := &setupAndStartServicesState{ctx: ctx, cfg: cfg, bundle: bundle, agentLoop: agentLoop, msgBus: msgBus, homePath: homePath, credStore: credStore, sandboxResult: sandboxResult, builtinReg: builtinReg, mcpReg: mcpReg, allowGodMode: allowGodMode}

	if r0, r1, stop := stg.startSchedulers(); stop {
		rs, retErr = r0, r1
		return
	}

	if r0, r1, stop := stg.setupMediaAndChannels(); stop {
		rs, retErr = r0, r1
		return
	}

	if r0, r1, stop := stg.wireInteractiveServices(); stop {
		rs, retErr = r0, r1
		return
	}

	if r0, r1, stop := stg.setupPlans(); stop {
		rs, retErr = r0, r1
		return
	}

	if r0, r1, stop := stg.startPlanEngine(); stop {
		rs, retErr = r0, r1
		return
	}

	stg.buildRESTAPI()

	if r0, r1, stop := stg.prepareListener(); stop {
		rs, retErr = r0, r1
		return
	}

	// The HTTP listener is now accepting connections. If any later boot step
	// fails and this function returns an error, tear the started services down
	// first — otherwise the caller aborts boot on the error and the accepting
	// listener goroutine (plus device service / drains) leaks. Registered only
	// after StartAll so it never fires when the listener was not started, and
	// gated on retErr so the success path leaves the services running.
	// stopAndCleanupServices nil-checks each subsystem, so it is safe on a
	// partially-started state.
	defer func() {
		if retErr != nil {
			stopAndCleanupServices(stg.runningServices, 5*time.Second, false)
		}
	}()

	if r0, r1, stop := stg.registerProcess(); stop {
		rs, retErr = r0, r1
		return
	}

	return stg.startBackgroundServices()
}

// startSchedulers constructs and starts the gateway's cron-backed scheduling and drain services in boot order.
func (stg *setupAndStartServicesState) startSchedulers() (*services, error, bool) {
	stg.runningServices = &services{credStore: stg.credStore, bundle: stg.bundle, sandboxResult: stg.sandboxResult, homePath: stg.homePath}

	// Per-user notification store (#264). Backs schedule-failure notifications and
	// the header notification center.
	stg.runningServices.notifStore = notifications.NewStore(filepath.Join(stg.homePath, "notifications"))

	stg.runningServices.CronService, stg.err = setupCronTool(
		stg.agentLoop,
		stg.msgBus,
		stg.cfg.AgentHomeBasePath(),
		stg.cfg,
		stg.runningServices.notifStore,
	)
	if stg.err != nil {
		return nil, fmt.Errorf("error setting up cron service: %w", stg.err), true
	}
	if stg.err = stg.runningServices.CronService.Start(); stg.err != nil {
		return nil, fmt.Errorf("error starting cron service: %w", stg.err), true
	}
	fmt.Println("✓ Cron service started")

	// ADR-027 — heartbeat is workspace-scoped. Reconcile workspace member_configs
	// into the cron engine: every (workspace, agent) pair with heartbeat.enabled=true
	// gets a recurring job. Best-effort: a reconcile failure is logged but does not
	// abort boot — the next hot-path write (workspace PUT) will re-converge.
	{
		wsFiles, wsErr := listWorkspaceFiles(stg.homePath)
		if wsErr != nil {
			slog.Warn("gateway: heartbeat schedule reconcile: list workspaces failed", "error", wsErr)
		} else if hbErr := ReconcileHeartbeatSchedules(
			stg.runningServices.CronService,
			wsFiles,
			configOnlyIsWorker(stg.cfg),
		); hbErr != nil {
			slog.Warn("gateway: heartbeat schedule reconcile failed", "error", hbErr)
		}
	}
	fmt.Println("✓ Heartbeat running as workspace-scoped schedules")

	// Queued-task draining (dispatch of `next` tasks) is owned UNCONDITIONALLY by
	// the dedicated TaskDrainService — never by the heartbeat path.
	if te := agent.GetTaskExecutor(stg.agentLoop); te != nil {
		stg.runningServices.TaskDrain = heartbeat.NewTaskDrainService(te, 0)
		stg.runningServices.TaskDrain.Start()
		fmt.Println("✓ Queued-task drain owned by: TaskDrainService (dedicated poll)")
	} else {
		fmt.Println("⚠ Queued-task drain disabled: no task executor available")
		// FIX 2: this warning is genuinely operator-relevant (queued tasks will
		// never dispatch) and, unlike the interactive-only banners nearby, has
		// no other durable record — pair it with logger.WarnCF (same pattern as
		// the "Gateway started without default model" warning above) so it
		// reaches gateway.log on the documented backgrounded launch, not just
		// an attached terminal's stdout.
		logger.WarnCF("gateway", "queued-task drain disabled — no task executor available", nil)
	}

	// Mailbox drain (M11): unhandled inbound mail → Board tasks. The provider is
	// rebuilt from live config + the credential store on every tick, so adding,
	// changing, or removing a mailbox via the Connectors API is picked up without
	// a restart. Started unconditionally; the scanner is a no-op when no mailbox
	// is configured.
	if tStore := agent.GetTaskStore(stg.agentLoop); tStore != nil {
		provider := email.MailboxProviderFunc(func() []email.Mailbox {
			return buildMailboxes(stg.agentLoop.GetConfig(), stg.credStore)
		})
		drainer := email.NewDrainer(tStore, provider, 0)
		stg.runningServices.MailboxDrain = heartbeat.NewMailboxDrainService(drainer, 0)
		stg.runningServices.MailboxDrain.Start()
		fmt.Println("✓ Mailbox drain owned by: MailboxDrainService (unhandled mail → Board)")
	}

	// Task time-trigger executor: fires once/every/recurring task triggers via a
	// dedicated CronService instance (reusing the pkg/cron engine, NOT a second
	// scheduler). Boot-reconciles existing tasks' triggers, then the create/
	// update/delete REST + tool paths (re)register/remove jobs via
	// AgentLoop.NotifyTaskUpserted / NotifyTaskDeleted.
	if tStore := agent.GetTaskStore(stg.agentLoop); tStore != nil {
		triggerStorePath := filepath.Join(stg.homePath, "tasks_triggers", "jobs.json")
		stg.runningServices.TaskTrigger = agent.NewTaskTriggerScheduler(
			triggerStorePath, tStore, agent.GetTaskExecutor(stg.agentLoop),
		)
		if startErr := stg.runningServices.TaskTrigger.Start(); startErr != nil {
			return nil, fmt.Errorf("error starting task trigger scheduler: %w", startErr), true
		}
		stg.agentLoop.SetTaskTriggerScheduler(stg.runningServices.TaskTrigger)
		if recErr := stg.runningServices.TaskTrigger.Reconcile(); recErr != nil {
			slog.Error("gateway: task trigger boot reconcile failed", "error", recErr)
		}
		fmt.Println("✓ Task trigger scheduler started")
	}

	// /loop time-driven scheduler (ADR-049 D6/D7, Wave 2-C2): a SECOND
	// dedicated CronService instance, mirroring TaskTrigger's own pattern
	// immediately above — orthogonal to the gateway's user-schedules
	// service. No boot reconcile needed: unlike task triggers (derived from
	// the task store), /loop jobs already persist their own cron store and
	// their session-side UnifiedMeta state independently; a job whose
	// session lost its loop state self-removes on next fire
	// (LoopScheduler.RunScheduled).
	loopSchedStorePath := filepath.Join(stg.homePath, "loops", "jobs.json")
	stg.runningServices.LoopScheduler = agent.NewLoopScheduler(loopSchedStorePath, stg.agentLoop)
	if startErr := stg.runningServices.LoopScheduler.Start(); startErr != nil {
		return nil, fmt.Errorf("error starting loop scheduler: %w", startErr), true
	}
	stg.agentLoop.SetLoopScheduler(stg.runningServices.LoopScheduler)
	fmt.Println("✓ Loop scheduler started")
	return nil, nil, false
}

// setupMediaAndChannels constructs the media and channel services, wires them into the agent loop, and emits boot warnings.
func (stg *setupAndStartServicesState) setupMediaAndChannels() (*services, error, bool) {
	stg.runningServices.MediaStore = media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{
		Enabled:  stg.cfg.Tools.MediaCleanup.Enabled,
		MaxAge:   time.Duration(stg.cfg.Tools.MediaCleanup.MaxAge) * time.Minute,
		Interval: time.Duration(stg.cfg.Tools.MediaCleanup.Interval) * time.Minute,
	})
	if fms, ok := stg.runningServices.MediaStore.(*media.FileMediaStore); ok {
		// Reload refs persisted by a previous gateway instance so
		// /api/v1/media/<ref> URLs in old session transcripts still resolve.
		// Best-effort — a load failure should not block boot.
		if loadErr := fms.LoadRegistry(); loadErr != nil {
			slog.Warn("media: failed to load persisted registry", "error", loadErr)
		}
		fms.Start()
	}

	// Wire the workspace-library provider into the media store so
	// media://workspace/<ws>/<id> refs resolve through the owning
	// workspace's library (FR-028). MUST share AgentLoop's workspace
	// library cache — a separate cache here caused live UAT failures
	// where uploads (via GetWorkspaceLibrary) updated one in-memory
	// manifest while resolve (via this provider) read a stale sibling.
	if fms, ok := stg.runningServices.MediaStore.(*media.FileMediaStore); ok {
		fms.SetWorkspaceLibraryProvider(func(workspaceID string) (media.WorkspaceLibraryResolver, error) {
			lib := stg.agentLoop.GetWorkspaceLibrary(workspaceID)
			if lib == nil {
				return nil, fmt.Errorf("workspace library unavailable for %q", workspaceID)
			}
			return lib, nil
		})
	}

	stg.runningServices.ChannelManager, stg.err = channels.NewManager(
		stg.cfg,
		stg.runningServices.bundle,
		stg.msgBus,
		stg.runningServices.MediaStore,
	)
	if stg.err != nil {
		if fms, ok := stg.runningServices.MediaStore.(*media.FileMediaStore); ok {
			fms.Stop()
		}
		return nil, fmt.Errorf("error creating channel manager: %w", stg.err), true
	}

	stg.agentLoop.SetChannelManager(stg.runningServices.ChannelManager)
	stg.agentLoop.SetMediaStore(stg.runningServices.MediaStore)
	// Wire all observer callbacks (CancelInterceptor, PairingObserver, …) via
	// the shared helper so this path stays in sync with restartServices.
	// Must happen after SetChannelManager so the Manager's channels map is
	// already populated when SetCancelInterceptor is called.
	wireChannelManager(stg.runningServices.ChannelManager, stg.agentLoop)

	if transcriber := voice.DetectTranscriber(stg.cfg, stg.runningServices.bundle); transcriber != nil {
		stg.agentLoop.SetTranscriber(transcriber)
		logger.InfoCF("voice", "Transcription enabled (agent-level)", map[string]any{"provider": transcriber.Name()})
	}

	enabledChannels := stg.runningServices.ChannelManager.GetEnabledChannels()
	if len(enabledChannels) > 0 {
		fmt.Printf("✓ Channels enabled: %s\n", enabledChannels)
	} else {
		fmt.Println("⚠ Warning: No channels enabled")
		// FIX 2: genuinely operator-relevant (no channel is reachable at all)
		// and otherwise invisible on a backgrounded launch — pair with
		// logger.WarnCF, same reasoning as the queued-task-drain warning above.
		logger.WarnCF("gateway", "no channels enabled — gateway has no reachable channel", nil)
	}

	// Apply warmup timeout default (FR-013 / CR-04).
	stg.cfg.Tools.ApplyWarmupTimeoutDefault()

	addr := fmt.Sprintf("%s:%d", stg.cfg.Gateway.Host, stg.cfg.Gateway.Port)
	stg.runningServices.HealthServer = health.NewServer(stg.cfg.Gateway.Host, stg.cfg.Gateway.Port)
	stg.runningServices.ChannelManager.SetupHTTPServer(addr, stg.runningServices.HealthServer)

	// Compute the main gateway origin for CORS and CSP frame-ancestors.
	// Use PublicURL when set (reverse-proxy deployment); otherwise derive from host:port.
	// When host is a wildcard (0.0.0.0, ::), allowedOrigin is empty and the WARN
	// is emitted below (FR-007e / MR-03).
	stg.allowedOrigin = middleware.CanonicalGatewayOrigin(stg.cfg)
	if stg.allowedOrigin == "" {
		// Wildcard bind host and no public_url → frame-ancestors must fall back to *.
		// Log once at WARN so operators know to set gateway.public_url for strict control.
		//
		// NOTE: this WARN is emitted at boot only and is NOT re-evaluated on
		// hot-reload of gateway.public_url. Operators changing the field at
		// runtime must restart the gateway for the WARN to re-fire on the new
		// value and for the new origin to take effect in CSP headers.
		slog.Warn("frame-ancestors fallback to '*' — set gateway.public_url for strict embedding control",
			"host", stg.cfg.Gateway.Host)
	}

	// Fix-5: warn when bash's hardened path is running on a non-Linux host
	// where the kernel sandbox (Landlock + seccomp) is unavailable. Single-shot
	// at boot. Pre-ADR-036 this only fired when the (now-retired)
	// experimental.workspace_shell_enabled gate was on, because only
	// workspace_shell/workspace_shell_bg routed through sandbox.ResolveLimits;
	// `bash` now routes EVERY agent's shell access through that same
	// mechanism universally (matching the old `exec` tool's universal
	// registration), so the warning now fires unconditionally on non-Linux
	// boot rather than being gated on a flag that no longer exists.
	if runtime.GOOS != "linux" {
		fmt.Fprintf(
			os.Stderr,
			"WARN: kernel sandbox unavailable on %s; bash runs with application-level path checks only — do not enable on multi-tenant systems\n",
			runtime.GOOS,
		)
	}

	// Fix-6: warn when any agent with remote channels has a non-deny bash policy.
	// The GHSA-pv8c-p6jf-3fpp channel block was removed; operators must now
	// configure per-agent ToolPolicyCfg to restrict bash.
	emitGHSARemovalWarn(stg.cfg)
	return nil, nil, false
}

// wireInteractiveServices constructs preview, chat, browser, approval, and interactive-question services and wires their callbacks.
func (stg *setupAndStartServicesState) wireInteractiveServices() (*services, error, bool) {
	// Construct the web_serve static-mode (Tier 1) and dev-mode (Tier 3)
	// shared registries. These are always created; gateway.preview_enabled
	// (ADR-044) gates /preview/ and serve_web live, per-request — it does not
	// affect whether these registries themselves are constructed.
	// Dev mode requires the DevServerRegistry; the tool itself gates to Linux.
	servedSubdirs := agent.NewServedSubdirs()
	devServers := sandbox.NewDevServerRegistry()
	stg.runningServices.servedSubdirs = servedSubdirs
	stg.runningServices.devServers = devServers

	// F-9: wire audit-set cleanup so evicted tokens don't re-emit serve.served
	// / dev.proxied on the rare cap-reset path. The callbacks are injected here
	// rather than in the registry constructors to avoid an import cycle
	// (gateway → agent/sandbox is fine; agent/sandbox → gateway would cycle).
	servedSubdirs.SetOnEvict(purgeFirstServedTokensBulk)
	devServers.SetOnEvict(purgeFirstServedTokens)

	// Start the egress proxy only when allow-list entries are configured.
	// An empty allow-list means deny-all, which is enforced by the proxy itself;
	// the proxy is still useful for audit logging even with an empty list.
	//
	// B1.2(c): wire the structured audit closure so every egress denial and
	// upstream-error condition emits a real audit row instead of slog-only.
	// agentLoop.AuditLogger() may be nil (when sandbox.audit_log=false); the
	// closure handles the nil case by falling through to slog so denials are
	// never silently swallowed. The B1.2(a) nil-receiver guard makes this
	// safe even if the logger reference is nil at the moment of call.
	egressAuditFn := func(entry *audit.Entry) {
		al := stg.agentLoop // captured by reference — may be wired up by reload
		if al == nil {
			slog.Warn("egress_proxy: audit fired before agent loop ready",
				"event", entry.Event, "decision", entry.Decision)
			return
		}
		logger := al.AuditLogger()
		if logger == nil {
			// audit_log disabled by config — fall through to slog so the
			// denial is at least visible in operator logs. CLAUDE.md
			// "audit-everything stance" still permits this fallback because
			// the operator explicitly chose to disable structured audit
			// (sandbox.audit_log=false). Loud-failure principle: log it.
			slog.Warn("egress_proxy: audit denied (audit logger disabled)",
				"event", entry.Event, "decision", entry.Decision,
				"details", entry.Details)
			return
		}
		// B1.2(a): logger.Log is nil-safe by contract; no extra guard.
		if logErr := logger.Log(entry); logErr != nil {
			slog.Error("egress_proxy: audit write failed",
				"event", entry.Event, "error", logErr)
		}
	}

	egressProxy, epErr := buildEgressProxyOrAbort(stg.cfg.Sandbox.EgressAllowList, egressAuditFn, sandbox.NewEgressProxy)
	if epErr != nil && !errors.Is(epErr, errEgressProxyDisabled) {
		return nil, epErr, true
	}
	if egressProxy != nil {
		stg.runningServices.egressProxy = egressProxy
	}

	// Build and wire Tier13Deps into every agent via the agent loop.
	// GatewayPreviewBaseURL is retired (ADR-044, FR-003/FR-005): web_serve now
	// derives its URL live from al.GetConfig / middleware.CanonicalGatewayOrigin
	// on every call instead of a boot-frozen base URL — see
	// AgentLoop.wireTier13DepsLocked.
	tier13 := agent.Tier13Deps{
		ServedSubdirs:     servedSubdirs,
		DevServerRegistry: devServers,
		EgressProxy:       egressProxy,
	}
	stg.agentLoop.WireTier13Deps(tier13)

	// SSE chat endpoint — kept for backward compatibility; streaming tokens now route through WebSocket.
	sseHandler := newSSEHandler(stg.msgBus, nil, stg.allowedOrigin, func() *config.Config { return stg.cfg })
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/chat", sseHandler)

	// WebSocket chat endpoint — primary transport for bi-directional chat streaming.
	stg.wsHandler = newWSHandler(stg.msgBus, stg.agentLoop, stg.allowedOrigin)
	stg.wsHandler.home = stg.homePath
	toolStore := newToolResultStore(stg.homePath)
	stg.wsHandler.toolStore = toolStore
	stg.runningServices.toolStore = toolStore
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/chat/ws", stg.wsHandler)
	// Register WebSocket handler as stream fallback so streaming tokens route back for webchat.
	stg.runningServices.ChannelManager.SetStreamFallback(stg.wsHandler)
	// Register webchat as a channel so outbound messages (non-streaming) also route back.
	// The webchatChannel and wsHandler share a reference so streaming can suppress duplicate Send().
	wch := newWebchatChannel(stg.wsHandler)
	stg.wsHandler.webchatCh = wch
	stg.runningServices.ChannelManager.RegisterChannel("webchat", wch)

	// Live interactive browser panel WebSocket (ADR-038 D1) — a dedicated
	// socket, separate from chat, on this SAME gateway listener (there is no
	// second TCP port at all — ADR-044 retired the separate preview
	// listener/port, so /preview/ is served on this same listener too). The route is
	// registered UNCONDITIONALLY, regardless of
	// tools.browser.live_view_enabled/take_control_enabled — those are
	// per-connection, POST-AUTH config gates that BrowserWSHandler.ServeHTTP
	// / handleControl check after the WS upgrade + auth handshake succeed,
	// refusing with a browser_status(error) frame rather than ever removing
	// the route or the listener. See config.go's LiveViewEnabled doc for why
	// (a raw HTTP-level rejection would surface to browser JS as an opaque,
	// unparseable WebSocket error).
	browserWSHandler := newBrowserWSHandler(stg.agentLoop, stg.allowedOrigin)
	stg.runningServices.browserWS = browserWSHandler
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/browser/ws", browserWSHandler)

	// Capture-ingest WS (ADR-047 D6, wave-plan W2-A) — the gateway-owned
	// WebRTC capture extension's ingest leg. Loopback-only (RemoteAddr
	// gate in captureIngestWSHandler.ServeHTTP, not an origin/auth check —
	// the caller is a CDP-driven page inside the managed Chrome, not a
	// browser client), authorized by a per-stream token (BindIngest/
	// findByToken), sharing browserWSHandler's captureRegistry so a
	// browser_webrtc_offer's session can be found by its ingest hello.
	captureIngestHandler := newCaptureIngestWSHandler(stg.agentLoop, browserWSHandler.captures)
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/browser/capture-ingest", captureIngestHandler)

	// Build the in-process tool-approval registry (FR-016, FR-070, M10).
	// policy.ValidateSaturationCap enforces FR-016 semantics:
	//   cap < 0 → fatal (emit HIGH audit + abort)
	//   cap == 0 → unlimited (emit WARN audit, ShouldSaturate always false)
	//   cap > 0 → use as-is
	approvalMaxPending := stg.cfg.Gateway.ToolApprovalMaxPending
	effectiveCap, capOK := policy.ValidateSaturationCap(context.Background(), nil, approvalMaxPending)
	if !capOK {
		return nil, fmt.Errorf(
			"gateway: invalid tool_approval_max_pending=%d — boot aborted (FR-016)",
			approvalMaxPending,
		), true
	}
	approvalTimeout := stg.cfg.Gateway.ToolApprovalTimeout
	var approvalTimeoutDur time.Duration
	if approvalTimeout > 0 {
		approvalTimeoutDur = time.Duration(approvalTimeout) * time.Second
	} else {
		approvalTimeoutDur = defaultToolApprovalTimeout
	}
	stg.approvalReg = newApprovalRegistryV2(effectiveCap, approvalTimeoutDur)
	stg.wsHandler.approvalRegV2 = stg.approvalReg
	// Broadcast every pending→terminal transition, whatever caused it (a
	// decision from any tab, timeout, Stop, agent deletion, shutdown), so no
	// open tab keeps a dialog for an approval the server has already closed.
	stg.approvalReg.setResolutionListener(stg.wsHandler.broadcastToolApprovalResolved)

	// Wire the policy approver into the agent loop (FR-011, C3).
	// The adapter bridges agent.PolicyApprover → approvalRegistryV2 + WSHandler.
	stg.agentLoop.SetToolApprover(newPolicyApproverAdapter(stg.approvalReg, stg.wsHandler))

	// AskUserQuestion pending registry (askuserquestion-tool-spec v3, ADR-074
	// D4b; W9b wiring): durable state lives in each owner session's
	// UnifiedMeta (pending_ask), the in-process registry mirrors it with the
	// global cap + default-safe timers, the card sink broadcasts
	// ask_user_question WS frames, and the resume dispatcher publishes the
	// §0.2 answers message back into the owner session's turn machinery.
	if sharedStore := stg.agentLoop.GetSessionStore(); sharedStore != nil {
		askSink := &askUserCardSink{h: stg.wsHandler}
		askReg := askuser.NewRegistry(
			sharedStore,
			&askUserResumeDispatcher{msgBus: stg.msgBus},
			askuser.Options{
				Sink:  askSink,
				Audit: &askUserAuditSink{al: stg.agentLoop},
			},
		)
		askSink.delayFn = askReg.EffectiveDefaultSafeDelay
		stg.wsHandler.askUserReg = askReg
		stg.agentLoop.SetAskUserRegistry(askReg)
		// ADR-088 FR-031: wire the goal-routing store resolver at boot so a
		// cold-start channel record echo / keeper action can rehydrate the
		// persisted GoalRoute* fields before any /goal command runs.
		stg.agentLoop.SetGoalRouteSessionStore()
		// Goal outcome line: a task-owned goal that ends with its task leaves
		// the same lasting outcome line in the task's run session as a chat
		// goal does (pkg/agent/goal_outcome.go).
		stg.agentLoop.InstallTaskGoalOutcomeRecorder()
		// Boot rearm sweep (US-6 S1/FR-9): re-hydrate every persisted pending
		// set so its default-safe timers re-arm from the durable CreatedAt
		// (already-elapsed timers fire near-immediately) and the reconnect
		// snapshot sees it. Runs in a goroutine — meta reads only, and a
		// pending set is inert until a client answers or a timer fires.
		go func() {
			metas, listErr := sharedStore.ListSessionsFiltered(func(m *session.UnifiedMeta) bool {
				return m.PendingAskJSON != ""
			})
			if listErr != nil {
				slog.Warn("gateway: askuser boot rearm sweep failed", "error", listErr)
				return
			}
			for _, m := range metas {
				if rearmErr := askReg.RearmSession(m.ID); rearmErr != nil {
					slog.Warn("gateway: askuser rearm failed",
						"session_id", m.ID, "error", rearmErr)
				}
			}
		}()
		// Wait out in-flight timer callbacks on shutdown so a persist never
		// races the process teardown (the Quiesce contract). Bound to the
		// gateway's shutdown-aware ctx — a defer here would fire when
		// setupAndStartServices RETURNS (still at boot), not at shutdown.
		go func() {
			<-stg.ctx.Done()
			askReg.Quiesce()
		}()
	} else {
		slog.Warn("gateway: askuser registry NOT wired — no shared session store; AskUserQuestion will fail closed")
	}

	// Wire the filter-metrics recorder into pkg/tools so FilterToolsByPolicy
	// can emit FR-039 omnipus_tool_filter_total counters. (C4)
	tools.SetToolMetricsRecorder(globalToolMetrics)

	// FIX (14-reviewer sign-off, HIGH): tools.SetMessageParentWakeFailureLogger
	// was never called anywhere in the codebase, so message_parent's default
	// logMessageParentWakeFailure — a deliberate no-op — was the ONLY logger
	// ever installed on the production runtime path. A delegated child's
	// failure to wake its parent session (B.6: the bounded typed wake that
	// backs question/blocker/handback delivery) therefore vanished silently —
	// no log line, no metric, nothing an operator could see. Install a
	// slog-backed handler here, right alongside the sibling tool-level wiring
	// immediately above, so a wake failure is surfaced as a slog.Warn.
	tools.SetMessageParentWakeFailureLogger(func(kind string, err error) {
		slog.Warn("gateway: message_parent: failed to wake parent session",
			"kind", kind, "error", err)
	})
	return nil, nil, false
}

// setupPlans constructs the plan and session-messaging stores and installs them on the agent loop.
func (stg *setupAndStartServicesState) setupPlans() (*services, error, bool) {
	// REST API endpoints for frontend data.
	//
	// M3: sample the onboarding state file's READABILITY before constructing
	// the manager. onboarding.NewManager treats every load failure as a fresh
	// install (and renames an unparseable file aside), so this is the only
	// moment at which "corrupt/unreadable" is distinguishable from "genuinely
	// never onboarded". The FR-050 pre-auth provider routes fail closed on
	// the unknown case — see preAuthOnboardingWindowOpen (rest_auth.go).
	stg.onboardingStateUnknown = onboardingStateUnreadable(stg.homePath)
	stg.onboardingMgr = onboarding.NewManager(stg.homePath)
	stg.tStore = agent.GetTaskStore(stg.agentLoop)
	stg.tExecutor = agent.GetTaskExecutor(stg.agentLoop)

	// ADR-049 D1/D4 (Wave 2-C1): construct the Plan store + the single hybrid
	// plan-engine instance. planStore is shared with restAPI (Plans REST
	// surface, rest_plans.go) AND with the engine itself — both write through
	// the SAME *plan.Store, so planStore.OnChange (wired here) is the single
	// choke point that emits a plan_status WS frame for every plan mutation,
	// regardless of whether the engine or a REST handler made it.
	stg.planStore = plan.New(filepath.Join(stg.homePath, "plans"))
	stg.planStore.OnChange = func(p *plan.Plan) {
		progress := p.Progress
		if stg.tStore != nil {
			if _, _, computed, cerr := plan.ComputeProgress(p.ID, stg.tStore); cerr == nil {
				progress = computed
			} else {
				slog.Warn("gateway: plan_status: compute progress failed", "plan_id", p.ID, "error", cerr)
			}
		}
		payload := agent.PlanStatusChangedPayload{
			PlanID:    p.ID,
			State:     string(p.State),
			PlanPhase: string(p.EffectivePlanPhase()),
			Progress:  progress,
		}
		if p.PausedReason != "" {
			payload.PausedReason = p.PausedReason
		}
		stg.agentLoop.EmitPlanStatusChanged(payload)
	}

	// ADR-052 Wave 2 (caller-int): install the real plan store into the
	// create_plan/execute_plan agent-tool surface. SetPlanStore re-wires
	// wirePlanToolsForAgent (pkg/agent/loop.go) for every currently
	// registered agent — closing the DI seam Wave 1 left as
	// NewPlanCreateTool(nil)/NewPlanExecuteTool(nil, nil) inside
	// registerSharedTools's first pass (which runs inside NewAgentLoop,
	// BEFORE this planStore exists). Every dependency gap inside that
	// re-wiring is logged at Error level (loud failure, never a silently
	// dead tool) — see wirePlanToolsForAgent's doc comment. Verified
	// non-nil here too: planStore is a concrete value from plan.New just
	// above, so this guards against a future refactor silently routing a
	// nil store through, not today's happy path.
	stg.agentLoop.SetPlanStore(stg.planStore)

	// Channel ownership for send_message (ADR-065). Injected here, next to the
	// plan store, for the same reason: it reads live config, so pkg/agent
	// cannot construct it without importing pkg/gateway. Until this runs
	// send_message refuses every target except the turn's own conversation.
	stg.agentLoop.SetChannelOwnership(newChannelOwnershipResolver(stg.agentLoop.GetConfig))
	if stg.agentLoop.GetPlanStore() == nil {
		return nil, fmt.Errorf("gateway: plan store wiring failed — SetPlanStore did not install a non-nil store"), true
	}
	fmt.Println("✓ Plan tool surface wired (create_plan/execute_plan/run_task/inspect_session)")

	// S1 UAT fix (PRIYA-GATE-never-executed / PRIYA-D8-race): install the
	// SAME planStore onto the TaskExecutor so its heartbeat auto-dispatch path
	// (CheckQueuedTasks) can verify a plan member task's parent plan is
	// actually in an executing state (approved/running) before dispatching it
	// — see task_executor.go's CheckQueuedTasks doc. Mirrors the
	// degrade-not-abort convention used for tExecutor just below (a minimal
	// test harness's AgentLoop may have no task executor at all); a nil
	// tExecutor here just means there is no heartbeat drain to gate.
	if stg.tExecutor != nil {
		stg.tExecutor.SetPlanStore(stg.planStore)
	}

	// ADR-053 §5 boot sweep (FR-118/G-13) + intent-log (FR-148/M4): construct
	// the durable session-lifecycle store and the write-ahead intent-log. Both
	// are folded into the plan engine's single boot pass via the setters below
	// (SetLifecycleStore / SetIntentLog), so Start runs the intent-log replay,
	// plan reconciliation, and session boot sweep as one atomic boot step.
	//
	// sec-MINOR-3/#539: derive the intent-log's own HMAC-chain key from the
	// master key, domain-separated from the audit-chain key (distinct info
	// tag) — mirrors the audit-chain derivation earlier in bootRun (see
	// audit.SetProcessChainKey's call site).
	//
	// CORRECTED + FIXED (14-reviewer sign-off, MEDIUM/security): this used to
	// WARN and continue with a nil key, on the claim that this "mirrors
	// audit.NewLogger's fallback, exactly." That comparison was false on both
	// sides. audit.NewLogger's OWN resolveChainKey (pkg/audit/hmac.go) fails
	// CLOSED in production — its dev-only key is gated behind
	// testing.Testing() and never taken by a real binary — and the caller a
	// few hundred lines up in this same function maps that failure to a
	// fatal SandboxBootError whenever audit_log is enabled; it does not run
	// with a guessable key. plan.NewIntentLog's resolveChainKey
	// (pkg/plan/intent_log_hmac.go), by contrast, has no such gate today: a
	// nil key here makes it silently install the SAME public, hardcoded
	// dev-only constant as the tamper-evidence chain key for every
	// production install, forever — defeating the entire purpose of the HMAC
	// chain (anyone who has read the source can forge or re-chain
	// plan_intents entries undetected). Treat this exactly like the sibling
	// ilDirErr immediately below: abort boot rather than run with a known
	// key. (A companion fix is making plan.NewIntentLog itself reject an
	// empty key outside tests; this check does not depend on that landing —
	// it stops the bad key from ever reaching NewIntentLog in the first
	// place, against the constructor's current dir-only-error signature.)
	intentLogChainKey, ilKeyErr := stg.credStore.DeriveSubkey(plan.IntentLogChainKeyInfo)
	if ilKeyErr != nil {
		return nil, fmt.Errorf("gateway: failed to derive intent log HMAC chain key: %w", ilKeyErr), true
	}
	stg.lifecycleStore = session.NewLifecycleStore(filepath.Join(stg.homePath, "session_lifecycle"))
	var ilDirErr error
	stg.intentLog, ilDirErr = plan.NewIntentLog(filepath.Join(stg.homePath, "plan_intents"), intentLogChainKey)
	if ilDirErr != nil {
		return nil, fmt.Errorf("gateway: failed to create intent log dir: %w", ilDirErr), true
	}
	stg.bootSweepCfg = stg.agentLoop.GetConfig().Planning

	// ADR-053 Phase 2 on-ramp: construct the durable S3 child->parent message
	// inbox and inject it + the S2 lifecycle store into the delegate +
	// message_parent tool surface for every registered agent. Until this runs,
	// every session-control path in delegate/message_parent fail-closes on nil
	// stores (the tools registered fail-closed during NewAgentLoop's first
	// registerSharedTools pass, before this store existed). This is the keystone
	// injection that makes the S2/S3 plane LIVE — mirrors SetPlanStore's
	// late-binding discipline exactly (the store is constructed here, after
	// NewAgentLoop returned, and re-wires the tool surface for every agent).
	// session.NewMessageInboxStore's doc specifies "<OMNIPUS_HOME>/session_messages"
	// as the conventional dir every consumer agrees on.
	messageInboxStore := session.NewMessageInboxStore(filepath.Join(stg.homePath, "session_messages"))
	// Apply the live config's caps to the store (the store's own caps are
	// plain fields, re-read per call). This boot-time application alone does
	// NOT make a session_messaging edit hot-reload — restartServices
	// (pkg/gateway/gateway.go) re-applies these same five fields from
	// al.GetMessageInboxStore() on every config reload; that is what actually
	// keeps a live edit in effect. See restartServices' own comment at that
	// call site for the incident this split (boot-only vs boot+reload) fixed.
	smCfg := stg.agentLoop.GetConfig().SessionMessaging
	messageInboxStore.ChildSendRatePerMinute = smCfg.EffectiveChildSendRatePerMinute()
	messageInboxStore.ChildSendBodyBytes = smCfg.EffectiveChildSendBodyBytes()
	messageInboxStore.ChildSendMaxDepth = smCfg.EffectiveChildSendMaxDepth()
	messageInboxStore.InboxUnackedMax = smCfg.EffectiveInboxUnackedMax()
	messageInboxStore.InboxPerTypeCeiling = smCfg.EffectiveInboxPerTypeCeiling()
	stg.agentLoop.SetSessionMessagingStores(messageInboxStore, stg.lifecycleStore)
	if stg.agentLoop.GetMessageInboxStore() == nil {
		return nil, fmt.Errorf("gateway: session-messaging store wiring failed — SetSessionMessagingStores did not install a non-nil inbox"), true
	}
	fmt.Println("✓ Session-messaging plane wired (delegate + message_parent stores injected)")
	return nil, nil, false
}

// startPlanEngine configures and starts the plan engine when its task dependencies are available.
func (stg *setupAndStartServicesState) startPlanEngine() (*services, error, bool) {
	// Mirrors the TaskDrain/TaskTrigger/MailboxDrain degrade-not-abort
	// convention immediately below/above for a missing task store/executor
	// (e.g. a minimal test harness's AgentLoop) — the plan engine needs both.
	if stg.tStore != nil && stg.tExecutor != nil {
		planEngine := agent.NewPlanEngine(stg.agentLoop, stg.planStore, stg.tStore, stg.tExecutor)
		// Boot-sweep + intent-log wiring (must precede Start so the first boot
		// pass runs synchronously inside Start).
		planEngine.SetLifecycleStore(stg.lifecycleStore)
		// FR-118/G-13: install the SAME lifecycleStore instance onto the
		// TaskExecutor so it can mint/transition the durable S2 record for
		// every task/plan-member dispatch session (mintTaskLifecycleRecord,
		// transitionTaskLifecycle, finalizeTaskLifecycle — see
		// TaskExecutor.lifecycleStore's doc comment). Without this call the
		// producer side of the store was permanently nil in the real gateway
		// (every one of those methods nil-guards and silently no-ops), so the
		// boot sweep below could reconcile plan/OWNER sessions but never saw a
		// task/plan-member dispatch session at all — the exact gap this line
		// closes. Regression guard:
		// TestSetupAndStartServices_TaskExecutorLifecycleStoreWiring
		// (lifecycle_store_wiring_test.go) dispatches a real task through this
		// boot path and asserts the durable record was persisted; deleting
		// this line makes that test fail.
		stg.tExecutor.SetLifecycleStore(stg.lifecycleStore)
		planEngine.SetIntentLog(stg.intentLog)
		// D13/G-12 Play-from-commit: install the gitevidence-backed resume
		// resolver so Play resumes a failed/cancelled member from its last
		// boundary commit (FR-144). The resolver degrades PER WORKSPACE
		// (nested-repo / no-commit / unmaterialized work dir -> fresh attempt,
		// FR-155), so it is wired unconditionally; a nil resolver here would
		// mask a valid evidence repo on one workspace with a nested-repo
		// degrade on another. It resolves the workspace lazily from the task
		// record at Play time, so no workspace needs to be open at boot.
		planEngine.SetCommitResolver(agent.NewLastMemberCommitResolver(stg.tStore, stg.homePath))
		// D13/G-12 PRODUCER half (E.4): the resolver above only READS boundary
		// commits. Without a producer it resolves "" forever and Play silently
		// degrades to a fresh attempt — indistinguishable from a successful
		// resume, which is why the gap was invisible to every gate. Wire the
		// committer onto the TaskExecutor so a terminal plan member snapshots
		// its declared write set.
		//
		// The secret scanner is mandatory: gitevidence.Commit refuses to commit
		// without one (MIN-5 fail-closed), so a construction failure here means
		// no evidence would be recorded at all — logged loudly rather than left
		// to look like "no commits happened to be needed".
		// tExecutor is already non-nil here — the enclosing block is gated on it.
		scanner, scanErr := audit.NewSecretScanner(stg.cfg.SensitiveDataReplacer(), nil)
		switch {
		case scanErr != nil:
			slog.Error("evidence committer: secret scanner construction failed — "+
				"boundary commits disabled, Play will always take the fresh-attempt path",
				"error", scanErr)
		default:
			stg.tExecutor.SetEvidenceCommitter(agent.NewWorkspaceEvidenceCommitter(stg.homePath, scanner))
		}
		if bsec := stg.bootSweepCfg.EffectiveBootSweepBudgetSeconds(); bsec > 0 {
			planEngine.SetBootSweepBudget(time.Duration(bsec) * time.Second)
		}
		if smb := stg.bootSweepCfg.EffectiveSnapshotMaxBytes(); smb > 0 {
			planEngine.SetSnapshotMaxBytes(smb)
		}
		// session.failed hook: best-effort recovery signal. The plan engine's
		// own tick loop re-arms idle settlement after Start; this hook is where
		// a future event-bus emission of session.failed would plug in.
		planEngine.SetSessionFailedHook(func(sessionID, reason string) {
			slog.Info("gateway: boot sweep: session.failed", "session_id", sessionID, "reason", reason)
		})
		// These two exact call sites supply the real /goal and /loop
		// active-loop counters (documented boot-ordering requirement on
		// PlanEngine.RegisterActiveCounter's doc comment); "loop" counts
		// currently-enabled cron jobs owned by the dedicated LoopScheduler
		// (constructed above, before this block).
		//
		// "goal" (GOAL-FR-049, R-22, ADR-086, wave E11 — re-homed here from
		// the retired wave S4, D-F): re-pointed off session.UnifiedMeta's
		// GoalCondition field (which ADR-086 makes a derived legacy mirror,
		// not the source of truth) onto pkg/goal's own record store.
		// goal.Store.ListActiveByOwnerKind is C-25/R-22's shared selector —
		// the same predicate goalIdleExpirySweep (pkg/agent/goal_loop.go,
		// wave E8) reads from, so the two never diverge on what "active"
		// means. Filtered to owner_kind == session (generated.GoalOwnerKind
		// Session): the definition phase and every terminal state count 0
		// by construction (ListActive filters on generated.GoalStateActive
		// alone), and a task-owned goal is excluded by owner_kind so it
		// never counts against this global active-loop cap (task-owned
		// goals are exempt from it, R-22, delivering MV-10 for free).
		// Constructing a fresh goal.Store per call is safe and cheap —
		// pkg/entity's cross-call locking is process-wide and shared by
		// every Store[T] instance rooted at the same directory, exactly the
		// precedent pkg/gateway/rest_tasks.go's goalStoreForTasks documents.
		planEngine.RegisterActiveCounter("goal", func() (int, error) {
			goalStore := goal.NewStore(stg.homePath)
			active, listErr := goalStore.ListActiveByOwnerKind(gen.GoalOwnerKindSession)
			if listErr != nil {
				return 0, fmt.Errorf("active-goal counter: list active session-owned goals: %w", listErr)
			}
			return len(active), nil
		})
		planEngine.RegisterActiveCounter("loop", func() (int, error) {
			if stg.runningServices.LoopScheduler == nil {
				return 0, nil
			}
			return len(stg.runningServices.LoopScheduler.ListEnabledJobs()), nil
		})
		if startErr := planEngine.Start(context.Background()); startErr != nil {
			return nil, fmt.Errorf("error starting plan engine: %w", startErr), true
		}
		stg.agentLoop.SetPlanEngine(planEngine)
		stg.runningServices.PlanEngine = planEngine
		fmt.Println("✓ Plan engine started")
	} else {
		fmt.Println("⚠ Plan engine disabled: task store/executor unavailable")
	}
	return nil, nil, false
}

// buildRESTAPI constructs the REST API, registers its core routes, and performs pre-listener reconciliation.
func (stg *setupAndStartServicesState) buildRESTAPI() {
	// Wire god-mode opt-in into the agent loop for runtime coercion.
	stg.agentLoop.SetAllowGodMode(stg.allowGodMode)

	// selfWriteReg is shared between safeUpdateConfigJSON (registers hashes of
	// app-initiated writes) and setupConfigWatcherPolling (suppresses reload for
	// those writes). Created here so both can reference the same instance.
	selfWriteReg := &configSelfWriteRegistry{
		hashes: make(map[[32]byte]struct{}),
	}
	stg.runningServices.selfWriteReg = selfWriteReg

	// ClawHub marketplace registry backing GET /api/v1/skills/search and
	// install-by-slug. Built from the unified Marketplaces list (FR-10.1) with
	// the SSRF-safe HTTP client (SEC-24) so outbound registry traffic honors
	// the SSRF policy. The client defaults BaseURL to https://clawhub.ai when
	// unset. Auth token (optional) is resolved from the credential bundle.
	var restSSRFClient *http.Client
	if restSSRF := agent.GetSSRFChecker(stg.agentLoop); restSSRF != nil {
		restSSRFClient = restSSRF.SafeClient()
	}
	var skillRegistry skills.SkillRegistry
	if chEntry, ok := skills.ClawHubMarketplaceFromConfig(stg.cfg, stg.bundle.GetString, restSSRFClient); ok {
		skillRegistry = skills.NewClawHubRegistry(skills.ClawHubConfig{
			Enabled:         chEntry.Enabled,
			BaseURL:         chEntry.BaseURL,
			AuthToken:       chEntry.AuthToken,
			SearchPath:      chEntry.SearchPath,
			SkillsPath:      chEntry.SkillsPath,
			DownloadPath:    chEntry.DownloadPath,
			Timeout:         chEntry.Timeout,
			MaxZipSize:      chEntry.MaxZipSize,
			MaxResponseSize: chEntry.MaxResponseSize,
			HTTPClient:      chEntry.HTTPClient,
		})
	}

	// ADR-067 T067-07: the ONE provider catalog for this process was booted
	// in Run (before NewAgentLoop, so every agent's window resolution sees
	// rung 5) and installed on the agent loop. Read it back rather than
	// building a second one — the embedded snapshot is 2 MB and parsing it
	// twice would double both boot cost and resident memory for no gain.
	// nil only in tests that construct services without the boot path; every
	// consumer below treats nil as "no catalog", never a 500.
	stg.providerCatalog = stg.agentLoop.GetCapabilityCatalog()

	stg.api = &restAPI{
		agentLoop:       stg.agentLoop,
		providerCatalog: stg.providerCatalog, // ADR-067: the booted catalog (nil in non-boot tests)
		allowedOrigin:   stg.allowedOrigin,
		onboardingMgr:   stg.onboardingMgr,
		// M3: "unknown" is not "fresh install" — see the field's doc comment.
		onboardingStateUnknown: stg.onboardingStateUnknown,
		homePath:               stg.homePath,
		taskStore:              stg.tStore,
		taskExecutor:           stg.tExecutor,
		liveTaskActivity:       stg.tExecutor, // founder decision 2026-09-14: Task.last_activity_at
		planStore:              stg.planStore, // ADR-049 D1: Plans REST surface (rest_plans.go) + plan_id FK check
		credStore:              stg.credStore,
		mediaStore:             stg.runningServices.MediaStore,
		ssrfChecker:            agent.GetSSRFChecker(stg.agentLoop), // SEC-24: nil when SSRF disabled
		sandboxResult:          stg.sandboxResult,                   // immutable post-boot snapshot
		appliedConfig:          mustDeepCopyConfig(stg.cfg),         // boot-time snapshot for pending-restart diff
		servedSubdirs:          stg.runningServices.servedSubdirs,   // web_serve static-mode token registry
		devServers:             stg.runningServices.devServers,      // web_serve dev-mode process registry
		approvalReg:            stg.approvalReg,                     // in-process tool-approval registry (FR-016)
		builtinRegistry:        stg.builtinReg,                      // M16: central builtin registry (FR-001)
		mcpRegistry:            stg.mcpReg,                          // M16: central MCP registry (FR-001)
		skillRegistry:          skillRegistry,                       // ClawHub marketplace (search + install-by-slug)
		allowGodMode:           stg.allowGodMode,                    // god-mode latch (2)
		notifStore:             stg.runningServices.notifStore,      // #264: notification center
		auditor:                stg.agentLoop.AuditLogger(),         // shared audit logger for REST mutations
		selfWriteReg:           selfWriteReg,                        // suppress watcher reload on app-initiated writes
		taskLock:               task.TaskFileLock,                   // shared striped lock for board task RMW
	}
	stg.api.cronService.Store(stg.runningServices.CronService) // #264: schedules CRUD (atomic.Pointer)
	// D-107: the Library REST write handlers broadcast a library_changed WS
	// frame after every landed mutation, so a second tab's folder listing
	// reconciles without a reload. wsHandler was built earlier in boot; store
	// its broadcast method behind the restAPI's nil-safe hook
	// (library_change_broadcast.go) — nil until here, no-op after shutdownless
	// tests that never wire it.
	libraryChangeFn := func(f gen.LibraryChangedFrame) { stg.wsHandler.broadcastLibraryChange(f) }
	stg.api.libraryChangeBroadcast.Store(&libraryChangeFn)
	// ADR-067 FR-037 (T067-11): a catalog refresh invalidates the
	// entitlement cache — the intersection behind every cached answer was
	// computed against a document that is no longer the served one.
	registerEntitlementCacheInvalidation(stg.providerCatalog, stg.api)
	// Stash the api ref so RunContextWithOptions can update builtinRegistry
	// after the M16 live-deps re-population (which creates a fresh *BuiltinRegistry
	// that would otherwise not reach the already-constructed api).
	stg.runningServices.restAPIRef = stg.api
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/sessions", stg.api.withAuth(stg.api.HandleSessions))
	// /api/v1/sessions/ handles: sessions CRUD AND the tool-results sub-resource
	// GET /api/v1/sessions/{session_id}/tool-results/{ref} (dispatched inside HandleSessions).
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/sessions/", stg.api.withAuth(stg.api.HandleSessions))
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/agents", stg.api.withAuth(stg.api.HandleAgents))
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/agents/", stg.api.withAuth(stg.api.HandleAgents))
	stg.runningServices.ChannelManager.RegisterHTTPHandler(
		"/api/v1/config",
		stg.api.withAuth(withRateLimit(configLimiter, stg.api.HandleConfig)),
	)
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/skills", stg.api.withAuth(stg.api.HandleSkills))
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/skills/", stg.api.withAuth(stg.api.HandleSkills))
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/commands", stg.api.withAuth(stg.api.HandleListCommands))
	stg.runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/doctor", stg.api.withAuth(stg.api.HandleDoctor))

	// Ensure the default workspace exists (FR-1.6). Best-effort: a failure
	// is logged but does not abort gateway startup.
	// ownerUsername is taken from the first configured user (empty on fresh install — that is fine).
	ownerUsername := ""
	if len(stg.cfg.Gateway.Users) > 0 {
		ownerUsername = stg.cfg.Gateway.Users[0].Username
	}
	if wsErr := ensureDefaultWorkspace(stg.homePath, ownerUsername, stg.cfg); wsErr != nil {
		slog.Error("gateway: default workspace auto-creation failed", "error", wsErr)
	}

	// ADR-046 P1 (FR-007/008): execution is workspace-scoped, and the system
	// deliberately never auto-adds a custom/pre-existing agent to any
	// workspace team (FR-008 — no silent global-roster membership). That is
	// correct for a fresh install (ensureDefaultWorkspace seeds the built-in
	// roster) but means an operator upgrading an install with pre-existing
	// CUSTOM agents can end up with agents that silently cannot execute at
	// all until manually added via a workspace's Team tab — previously only
	// discoverable one per-turn refusal at a time. Surface the full list ONCE
	// at boot, after workspaces are ensured, so it's visible up front instead.
	logWorkspacelessAgents(stg.homePath, stg.cfg)

	// ADR-067 W3 (FR-030..FR-034a, FR-038a, FR-039, FR-080): open the index for
	// every already-mounted knowledge base, push indexing progress over the
	// WebSocket, and start each collection's drift schedule. Runs after the
	// workspaces are ensured (it reads their mount records) and before the
	// listener accepts connections. Interval 0 means FR-038a's six-hour default:
	// there is no config key for it yet, and KnowledgeLifecycleOptions.DriftInterval
	// is where one would be passed in.
	startKnowledgeLifecycle(stg.homePath, stg.wsHandler, 0,
		knowledgeDriftNotifier(stg.runningServices.notifStore, stg.agentLoop, stg.agentLoop.GetConfig))

	// Recover tasks left "in_progress" by a crashed/abandoned previous process.
	// Runs before the HTTP listener accepts connections (StartAll, below), so no
	// handler can race reconciliation.
	stg.api.reconcileStuckTasks()

	// Drop blocked_by edges pointing at task files that no longer exist, so the
	// dependency graph self-heals on boot (a waiting task gated only on an orphan
	// would otherwise never advance). Same pre-listener safety window as above.
	stg.api.reconcileOrphanBlockedByEdges()

	// Register additional endpoints for frontend features.
	// These return proper JSON responses instead of letting the SPA catch-all
	// serve HTML (which causes "Unexpected token '<'" JSON parse errors).
	stg.api.registerAdditionalEndpoints(stg.runningServices.ChannelManager)
}

// prepareListener registers the remaining HTTP routes and middleware, then starts the channel listener.
func (stg *setupAndStartServicesState) prepareListener() (*services, error, bool) {
	// Register /preview/ (canonical web_serve URL) on the MAIN mux (ADR-044,
	// FR-001/FR-002/FR-003). There is no separate preview listener anymore —
	// /preview/ shares gateway.port with the SPA and /api/v1/*. It is
	// registered bare: no withAuth/session/CSRF/origin wrapping — the URL path
	// token is the credential (FR-023) — but it DOES inherit the global
	// configSnapshotMiddleware wrap applied below (FR-002: race-free live-config
	// reads). HandlePreview itself checks cfg.IsPreviewEnabled() live on every
	// request and 404s when disabled (FR-006) — no restart required to flip it.
	// All handlers live in rest_preview.go.
	stg.api.registerPreviewEndpoints(stg.runningServices.ChannelManager)

	// Omnipus start page — what a fresh browser tab opens instead of
	// about:blank. Registered bare (no auth) for the same structural reason as
	// /preview/: the client is the managed headless Chrome, which carries no
	// session cookie, and the page is static and non-sensitive. See
	// browser_start_page.go.
	stg.api.registerBrowserStartPage(stg.runningServices.ChannelManager)

	// Catch-all for any /api/ path not registered — returns JSON 404 instead of SPA HTML.
	// Do not echo r.URL.Path in the response; that leaks internal routing details.
	stg.runningServices.ChannelManager.RegisterHTTPHandler(
		"/api/",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			if encodeErr := json.NewEncoder(w).
				Encode(map[string]string{"error": "endpoint not found"}); encodeErr != nil {
				slog.Debug("404 handler: encode failed", "error", encodeErr)
			}
		}),
	)

	// Serve the embedded SPA (Sovereign Deep UI) as the default handler.
	// API routes registered above take priority; anything else serves the SPA.
	// If no SPA was embedded at build time, skip registration (UI not available).
	// The allow-list accessor is a closure over the LIVE config (ADR-083
	// EMB-081): gateway.video_embed_hosts is read per response, so an operator
	// who empties it stops the external frame source reaching the served policy
	// on the next page load rather than at the next restart. The same resolver
	// feeds GET /api/v1/state, which is what keeps the browser's list and the
	// browser's policy equal (EMB-080).
	if spaHandler := newSPAHandler(func() []string {
		return ResolveVideoEmbedHosts(stg.agentLoop.GetConfig())
	}); spaHandler != nil {
		stg.runningServices.ChannelManager.RegisterHTTPHandler("/", spaHandler)
	} else {
		fmt.Println("Note: No embedded SPA (run 'pnpm build' in web/frontend to enable UI)")
	}

	// Wrap the HTTP server handler with config snapshot middleware so all
	// request handlers see a consistent config even during hot-reload.
	if stg.err = stg.runningServices.ChannelManager.WrapHTTPHandler(stg.api.configSnapshotMiddleware); stg.err != nil {
		return nil, fmt.Errorf("wrapping HTTP handler: %w", stg.err), true
	}
	// F-13 / ADR-044: /preview/ is registered on this SAME main mux (see
	// registerPreviewEndpoints above), so the WrapHTTPHandler(configSnapshotMiddleware)
	// call above already covers it — HandlePreview's configFromContext(r.Context())
	// gets a race-free snapshot with no separate wrap needed. There is no more
	// preview-only server/mux to wrap.

	// Wrap with CSRF double-submit-cookie middleware (SEC / issue #97).
	//
	// WrapHTTPHandler semantics: "wrap N times" stacks outermost-last, so the
	// execution order on a request is:
	//   CSRF check → configSnapshot injection → mux dispatch → auth check in handler
	//
	// The sprint plan (temporal-puzzling-melody.md §1) calls for
	// "auth → RBAC → CSRF → handler". We place CSRF BEFORE the per-handler
	// auth gate because (a) auth is currently inlined in withAuth / withOptionalAuth
	// wrappers rather than a separate middleware, and splitting it would be
	// substantial collateral damage for this PR; (b) failing fast on a bad
	// cookie avoids wasting a bcrypt compare on obvious cross-origin forgeries.
	// The net effect — state-changing requests without a valid cookie+header
	// get rejected — is identical.
	csrfMW := middleware.CSRFMiddleware(
		// clientIPWithLiveFallback (not the bare clientIP) — this reporter runs
		// before configSnapshotMiddleware injects a config snapshot (see the
		// wrap-order comment below), so it needs the live-config fallback to
		// honor an operator's real gateway.trust_xff setting in its audit log
		// instead of silently defaulting to false. See clientIPWithLiveFallback's
		// doc comment (rest_auth.go) for the full trace.
		middleware.WithClientIPFunc(stg.api.clientIPWithLiveFallback),
		middleware.WithReporter(func(r *http.Request, sourceIP, route string) {
			// Best-effort audit log of CSRF mismatches (SEC-15). Never blocks
			// or crashes the request path — the middleware already returns 403.
			logger := stg.api.agentLoop.AuditLogger()
			if logger == nil {
				slog.Warn("csrf: token mismatch (no audit logger)",
					"source_ip", sourceIP, "route", redactRequestPath(route), "method", r.Method)
				return
			}
			// Named logErr to avoid shadowing the outer err declared in
			// setupServices (govet shadow). The two errors have unrelated
			// lifetimes — this one is scoped entirely to the Reporter closure.
			if logErr := logger.Log(&audit.Entry{
				Event:    "csrf_mismatch",
				Decision: audit.DecisionDeny,
				Details: map[string]any{
					"source_ip": sourceIP,
					"route":     redactRequestPath(route),
					"method":    r.Method,
				},
				PolicyRule: "csrf: cookie/header mismatch on state-changing request",
			}); logErr != nil {
				slog.Warn("csrf: audit log write failed", "error", logErr)
			}
		}),
	)
	if stg.err = stg.runningServices.ChannelManager.WrapHTTPHandler(csrfMW); stg.err != nil {
		return nil, fmt.Errorf("wrapping HTTP handler with CSRF: %w", stg.err), true
	}

	// Wire the /reload trigger BEFORE StartAll launches the HTTP listener.
	// Otherwise there is a boot-ordering window where /health already answers
	// 200 (listener live) but HealthServer.reloadFunc is still nil, so a
	// concurrent POST /reload returns 503 "reload not configured". The
	// manualReloadChan is buffered (cap 1) and its consumer loop is started
	// later by the caller — signaling before the consumer exists is safe. The
	// caller reuses runningServices.reloadTrigger / .manualReloadChan (it does
	// NOT re-create them). restartServices reuses this same HealthServer, so
	// reloadFunc is never reset to nil after this point.
	stg.runningServices.manualReloadChan = make(chan struct{}, 1)
	stg.runningServices.reloadTrigger = newReloadTrigger(stg.runningServices, stg.agentLoop)
	stg.runningServices.HealthServer.SetReloadFunc(stg.runningServices.reloadTrigger)

	if stg.err = stg.runningServices.ChannelManager.StartAll(context.Background()); stg.err != nil {
		return nil, fmt.Errorf("error starting channels: %w", stg.err), true
	}
	return nil, nil, false
}

// registerProcess starts post-listener catalog work, writes process discovery files, and starts the device service.
func (stg *setupAndStartServicesState) registerProcess() (*services, error, bool) {
	// Boot logging: main listener (ADR-044: /preview/ shares this same listener,
	// no separate preview port/address to log). preview_enabled is read live
	// (not restart-gated), so this line only reflects the value at boot time.
	mainAddr := fmt.Sprintf("%s:%d", stg.cfg.Gateway.Host, stg.cfg.Gateway.Port)
	slog.Info("gateway listening on " + mainAddr)

	// ADR-067 FR-008: the catalog refresh loop starts HERE — after StartAll
	// bound the listener — and nowhere earlier. Boot must never wait on the
	// network for a document the embedded snapshot already provides: an
	// offline install serves the snapshot and reaches listen at exactly the
	// same speed as an online one (US-3.AC1). The startup pull is skipped
	// outright when the persisted last-known-good is less than an hour old,
	// so a gateway restarted in a loop cannot spend GitHub's unauthenticated
	// rate limit on a document it already has (F-34).
	//
	// ctx is passed through so the loop observes gateway shutdown instead of
	// running untethered for the life of the process — see
	// runCatalogRefreshLoop's doc comment for why this is load-bearing, not
	// cosmetic: an un-canceled startup pull performs REAL network I/O
	// (api.github.com, falling back to raw.githubusercontent.com) and then
	// writes providers_catalog.json into homePath via fileutil.WriteFileAtomic
	// on success, entirely outside every shutdown drain in shutdown.go. A
	// caller that boots and tears down many gateways in one process (every
	// integration/security test using testutil.StartTestGateway) leaked one
	// of these forever per boot, each capable of landing a straggler write in
	// homePath — including a t.TempDir() root already mid-RemoveAll —
	// well after RunContext had already returned.
	// Cancel-and-wait, not fire-and-forget: the loop stops on ctx, but
	// RunContext must not return while a refresh is still between "pull
	// completed" and "file written". startCatalogRefreshLoop hands shutdown
	// a cancel plus a done channel it waits on (step 1, shutdown.go).
	stg.runningServices.catalogRefreshCancel, stg.runningServices.catalogRefreshDone = startCatalogRefreshLoop(
		stg.ctx,
		stg.providerCatalog,
		catalog.NewFileStore(stg.homePath),
		catalogRefreshInterval,
		catalogRefreshTimeout,
		catalogStartupSkipWindow,
	)
	if stg.cfg.IsPreviewEnabled() {
		slog.Info("preview enabled: /preview/ served on the main listener")
	} else {
		slog.Info("preview disabled by config (gateway.preview_enabled=false)")
	}

	// Write port file so external callers (e.g. eval-runner) can discover the bound port.
	portFile := filepath.Join(stg.cfg.AgentHomeBasePath(), "gateway.port")
	portData := strconv.Itoa(stg.cfg.Gateway.Port)
	if writeErr := os.WriteFile(portFile, []byte(portData+"\n"), 0o600); writeErr != nil {
		return nil, fmt.Errorf("write gateway.port: %w", writeErr), true
	}

	// Self-register this process's PID so that `omnipus stop` and Status work
	// regardless of how the gateway was launched (spawner-started OR hand-started
	// via `omnipus start`). WritePID uses an atomic rename so a concurrent Status
	// call never reads a partial write. MAJOR-2: without this, a hand-started
	// gateway leaves no PID file and `omnipus stop` reports "not running".
	if pidErr := daemon.WritePID(stg.homePath, os.Getpid()); pidErr != nil {
		// Non-fatal: the gateway is already serving traffic. Log prominently so
		// the operator knows that `omnipus stop` will not find this process.
		slog.Warn("gateway: failed to write self PID file — `omnipus stop` will not track this process",
			"pid", os.Getpid(), "home", stg.homePath, "error", pidErr)
	} else {
		slog.Info("gateway: registered self PID", "pid", os.Getpid(), "home", stg.homePath)
	}

	fmt.Printf(
		"✓ Health endpoints available at http://%s:%d/health, /ready and /reload (POST)\n",
		stg.cfg.Gateway.Host,
		stg.cfg.Gateway.Port,
	)

	stateManager := state.NewManager(stg.cfg.AgentHomeBasePath())
	stg.runningServices.DeviceService = devices.NewService(devices.Config{
		Enabled:    stg.cfg.Devices.Enabled,
		MonitorUSB: stg.cfg.Devices.MonitorUSB,
	}, stateManager)
	stg.runningServices.DeviceService.SetBus(stg.msgBus)
	// Invariant: when cfg.Devices.Enabled==true, a Start failure is fatal and
	// propagated to the caller (Run returns the error). When disabled, Start
	// failures are only warnings. A unit test for this path is not included
	// because devices.Service is a concrete struct (not an interface) and
	// mocking it would require invasive refactoring; the behavior is exercised
	// by integration tests that configure a real USB monitor on supported hosts.
	if stg.err = stg.runningServices.DeviceService.Start(context.Background()); stg.err != nil {
		if stg.cfg.Devices.Enabled {
			return nil, fmt.Errorf("device service: %w", stg.err), true
		}
		logger.WarnCF(
			"device",
			"device service start failed (devices disabled, continuing)",
			map[string]any{"error": stg.err.Error()},
		)
	} else if stg.cfg.Devices.Enabled {
		fmt.Println("✓ Device event service started")
	}
	return nil, nil, false
}

// startBackgroundServices starts the shutdown-aware orphan and browser cleanup loops and returns the running services.
func (stg *setupAndStartServicesState) startBackgroundServices() (*services, error) {
	// Start the orphan GC scheduler: runs Library.OrphanGC across every
	// workspace media library every hour (best-effort). A single failure
	// (e.g. corrupted manifest) does not abort the loop — the error is
	// logged and the next tick proceeds. Libraries with no orphan files
	// are a fast no-op.
	go func() {
		const orphanInterval = 1 * time.Hour
		ticker := time.NewTicker(orphanInterval)
		defer ticker.Stop()
		// ctx.Done() must be observed here (not a bare `for range ticker.C`,
		// which never exits) — this goroutine outlives the process
		// otherwise. See runCatalogRefreshLoop's doc comment for the shared
		// class of bug: any un-canceled background loop started here can
		// still be mid-tick (Library.OrphanGC touches disk) when a caller
		// that boots/tears down many gateways in one process — every test
		// using testutil.StartTestGateway — has already moved on to
		// t.TempDir() cleanup of homePath.
		for {
			select {
			case <-stg.ctx.Done():
				return
			case <-ticker.C:
			}
			a := stg.agentLoop
			if a == nil {
				continue
			}
			wsFiles, wsErr := listWorkspaceFiles(stg.homePath)
			if wsErr != nil {
				slog.Warn("orphan-gc: list workspaces failed", "error", wsErr)
				continue
			}
			for _, ws := range wsFiles {
				lib, libErr := library.New(stg.homePath, ws.ID)
				if libErr != nil {
					slog.Warn("orphan-gc: open library", "workspace_id", ws.ID, "error", libErr)
					continue
				}
				gcEntry, gcErr := lib.OrphanGC(library.OrphanGCConfig{Enabled: true})
				if gcErr != nil {
					slog.Warn("orphan-gc: run failed", "workspace_id", ws.ID, "error", gcErr)
					continue
				}
				if len(gcEntry) > 0 {
					slog.Info("orphan-gc: deleted orphan entries",
						"workspace_id", ws.ID, "count", len(gcEntry))
				}
			}
		}
	}()

	// Start the idle-browser reaper: closes idle TABS, and any browsing context
	// they leave empty, once tools.browser.idle_ttl has passed with no attached
	// live-panel viewer.
	//
	// Why this is needed: closing the live panel is a pure UI dismiss — the
	// SPA sends no shutdown frame, and browser.CloseSession had no production
	// caller at all — so a browsing context (and its resident Chrome) outlived
	// the panel indefinitely. Reopening the panel days later showed the exact
	// page the user had left. Sweeping is best-effort and idempotent; a sweep
	// that reaps nothing is a cheap map scan.
	//
	// The interval MUST stay well under idle_ttl, or the TTL is a floor rather
	// than the actual lifetime: a tab going idle just after a sweep waits out
	// the TTL *plus* the remainder of the interval. Shipped history was a 5m
	// sweep against a 30m TTL, where the slack was proportionally small; the
	// TTL dropping to 5m made the interval the dominant term, so a "5 minute"
	// cleanup would really have meant 5-10. One minute keeps the observed
	// lifetime inside ~5-6 minutes, and a sweep that reaps nothing is a map
	// scan — sweeping more often costs far less than a renderer outliving its
	// TTL (measured 74-268MB RSS each). "Outliving", not "leaked": issue #592's
	// headline leak turned out to be one Chrome's normal process tree, and
	// this comment should not quietly reintroduce the word that misled it.
	go func() {
		const reapInterval = time.Minute
		ticker := time.NewTicker(reapInterval)
		defer ticker.Stop()
		// ctx.Done() is observed below so this goroutine actually exits on
		// gateway shutdown instead of outliving the process — see
		// runCatalogRefreshLoop's doc comment for the shared class of bug.
		//
		// Each tick is recovered INDIVIDUALLY, matching the boot-time
		// warm-up goroutine above: an unrecovered panic in any goroutine takes
		// the WHOLE gateway process down — chat, every channel, every agent —
		// and this is a best-effort idle sweep. Recovering per tick (rather
		// than around the loop) also means one bad sweep does not stop all
		// future ones, which a single outer recover would.
		sweep := func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("browser-reaper: sweep panicked; cleanup paused until the next tick",
						"panic", fmt.Sprintf("%v", r))
				}
			}()
			a := stg.agentLoop
			if a == nil {
				return
			}
			for _, mgr := range a.BrowserManagers() {
				if mgr == nil {
					continue
				}
				if reaped := mgr.ReapIdleSessions(); len(reaped) > 0 {
					slog.Info("browser-reaper: closed idle browsing contexts",
						"count", len(reaped), "session_ids", reaped)
				}
			}
			// FR-040/FR-040a: whole-Chrome idle close, AFTER the per-tab loop
			// above and inside the same per-tick recover().
			//
			// The order is load-bearing, not stylistic. The per-tab reaper is
			// what brings a browser to zero tabs in the first place; running
			// the whole-Chrome close first would always find tabs still open
			// and could never close anything. A sweep that can never close
			// anything is precisely the silent no-op FR-061 forbids — it
			// would log nothing, fail nothing, and leak a ~182 MB Chrome per
			// workspace forever.
			//
			// What survives a close: the profile directory on disk (so the
			// workspace is still logged in) and every *BrowserManager (so the
			// next tool call quietly relaunches instead of erroring). What
			// goes: the pool entry and the Chrome process.
			if pool := a.BrowserPool(); pool != nil {
				if closed := pool.CloseIdle(time.Now()); len(closed) > 0 {
					slog.Info("browser-reaper: closed idle workspace browsers (profiles kept)",
						"count", len(closed), "browsing_keys", closed)
				}
			}
		}
		for {
			select {
			case <-stg.ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()

	return stg.runningServices, nil
}

func (catalogLogAdapter) Info(msg string, args ...any) {
	logger.InfoCF("catalog", msg, slogArgsToFields(args))
}

func (catalogLogAdapter) Warn(msg string, args ...any) {
	logger.WarnCF("catalog", msg, slogArgsToFields(args))
}

func (catalogLogAdapter) Error(msg string, args ...any) {
	logger.ErrorCF("catalog", msg, slogArgsToFields(args))
}

// slogArgsToFields converts a slog-style alternating key/value argument
// list into the map[string]any pkg/logger's *CF functions take. A
// malformed odd-length call (a bug at the call site, not expected in
// practice) preserves its trailing value under "!BADKEY" rather than
// silently dropping it — the same convention log/slog itself documents
// for the identical case.
func slogArgsToFields(args []any) map[string]any {
	fields := make(map[string]any, len(args)/2+1)
	for i := 0; i+1 < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", args[i])
		}
		fields[key] = args[i+1]
	}
	if len(args)%2 == 1 {
		fields["!BADKEY"] = args[len(args)-1]
	}
	return fields
}

// persistedCatalogAger reports when the persisted last-known-good was last
// written. *catalog.FileStore implements it; the parameter is an interface
// so the skip decision is testable without touching a real clock or a real
// $OMNIPUS_HOME.
type persistedCatalogAger interface {
	ModTime() (time.Time, error)
}

// skipStartupPull reports whether the FR-008 startup pull should be skipped
// because the persisted document is younger than window. A missing or
// unreadable persisted file is NOT a skip — there is nothing to serve from
// disk, so the pull is exactly what is wanted.
func skipStartupPull(store persistedCatalogAger, window time.Duration) bool {
	if store == nil || window <= 0 {
		return false
	}
	mod, err := store.ModTime()
	if err != nil {
		return false
	}
	return time.Since(mod) < window
}

// runCatalogRefreshLoop performs the FR-008 startup pull (unless the
// persisted document is younger than skipWindow), then one pull every
// interval thereafter, until ctx is canceled. The sole caller
// (setupAndStartServices) invokes it in its own goroutine, AFTER the
// listener is bound, passing the gateway's own shutdown-aware context.
//
// ctx cancellation is load-bearing, not a nicety: this loop performs REAL
// network I/O (api.github.com, falling back to raw.githubusercontent.com on
// failure) and — on a successful pull — writes providers_catalog.json into
// store's directory via fileutil.WriteFileAtomic, entirely independent of
// every drain in shutdown.go (channel manager, cron, plan engine, active
// turns, agent loop). Before ctx was threaded through here, this goroutine
// had no way to observe shutdown at all and ran for the life of the
// process; a gateway stopped (or, in any test/harness process that boots
// many gateways via testutil.StartTestGateway, torn down) while a startup
// pull was still resolving DNS/TLS or mid-download could land a straggler
// write into homePath — including a t.TempDir() root already mid-RemoveAll
// — well after RunContext had returned, surfacing as "directory not empty"
// on the test's own cleanup. Deriving each attempt's timeout context FROM
// ctx (not context.Background()) means a cancellation during an in-flight
// HTTP request aborts it immediately via the http.Client's context
// plumbing, rather than merely blocking the NEXT attempt from starting.
//
// The pull before the ticker loop is load-bearing, not cosmetic: Go's
// time.Ticker does not fire on creation, so a bare ticker loop never
// invokes the puller until interval has elapsed — meaning any gateway
// restarted more often than that (dev pods, containers, k8s rolling
// deploys, systemd restarts) would run indefinitely on the build-time
// snapshot and never refresh at all. The skipWindow is what keeps that
// startup pull from becoming a rate-limit problem on a restart loop.
//
// Every failure is non-fatal by construction: catalog.Refresh retains the
// currently served document and logs its own reason-keyed WARN, so this
// loop only records that the attempt failed and carries on ticking.
// startCatalogRefreshLoop runs runCatalogRefreshLoop on its own goroutine
// under a child context and returns the child's cancel plus a channel closed
// when the goroutine has EXITED. Shutdown calls cancel and then waits on
// done, so no refresh can be mid-persist when RunContext returns. On
// 2026-09-12 the fire-and-forget form left providers_catalog.json being
// written into integration-test home dirs after their gateway had stopped.
func startCatalogRefreshLoop(
	ctx context.Context,
	cat *catalog.Catalog,
	store persistedCatalogAger,
	interval, refreshTimeout, skipWindow time.Duration,
) (cancel context.CancelFunc, done <-chan struct{}) {
	loopCtx, loopCancel := context.WithCancel(ctx)
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		runCatalogRefreshLoop(loopCtx, cat, store, interval, refreshTimeout, skipWindow)
	}()
	return loopCancel, ch
}

func runCatalogRefreshLoop(
	ctx context.Context,
	cat *catalog.Catalog,
	store persistedCatalogAger,
	interval, refreshTimeout, skipWindow time.Duration,
) {
	if cat == nil {
		return
	}
	refresh := func(failureLogMsg string) {
		attemptCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
		defer cancel()
		if err := cat.Refresh(attemptCtx); err != nil {
			// A cancellation reaching here mid-attempt (gateway shutting
			// down) is expected, not a real refresh failure — log it at a
			// lower level than a genuine pull/parse/apply error so shutdown
			// under load does not spam WARN.
			if ctx.Err() != nil {
				logger.InfoCF("gateway", "catalog refresh: canceled by gateway shutdown",
					map[string]any{"error": err})
				return
			}
			logger.WarnCF("gateway", failureLogMsg, map[string]any{"error": err})
		}
	}

	if ctx.Err() != nil {
		return
	}

	if skipStartupPull(store, skipWindow) {
		logger.InfoCF("gateway", "catalog: startup pull skipped; persisted document is recent",
			map[string]any{"skip_window": skipWindow.String()})
	} else {
		refresh("gateway: catalog startup refresh failed; served document retained")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh("gateway: catalog refresh failed; last-known-good retained")
		}
	}
}

// wireChannelManager consolidates all observer wiring that must be (re-)applied
// whenever a ChannelManager becomes active.  It is called once at initial boot
// (in setupAndStartServices) and twice per reload (in restartServices):
//
//  1. Before ChannelManager.Reload() — so channels whose Start() runs inside
//     Reload already have the CancelInterceptor and PairingObserver set when
//     they first emit events.
//
//  2. After ChannelManager.Reload() — because Reload may recreate channel
//     instances (new struct value with nil fields), which clears the observer
//     pointer set in the pre-Reload call.  Re-wiring guarantees the observers
//     are always live after the reload completes.
//
// Callers are responsible for calling SetChannelManager on the agent loop
// before invoking this helper, because SetCancelInterceptor requires that the
// Manager's channels map is already populated.
func wireChannelManager(cm *channels.Manager, al *agent.AgentLoop) {
	// Wire the agent loop as the CancelInterceptor so Tier B channels can fire
	// /cancel via text-parsing (FR-2).
	cm.SetCancelInterceptor(al)
	// #283 / #368: bridge WhatsApp native pairing (QR/status) → agent event bus
	// so the per-connection WS forwarder broadcasts a whatsapp_pairing frame to
	// the SPA.  Re-wiring on reload ensures the observer survives channel restarts
	// (the Manager creates new channel instances on Reload, which clears the old
	// observer pointer).
	cm.SetPairingObserver(
		func(channelID string, status channels.PairingStatus, qr, message string) {
			al.EmitWhatsAppPairing(channelID, status, qr, message)
		},
	)
}

// agentCheckerFunc adapts a func to the agentChecker interface used by the
// scheduled runner.
type agentCheckerFunc func(agentID string) bool

func setupCronTool(
	agentLoop *agent.AgentLoop,
	msgBus *bus.MessageBus,
	workspace string,
	cfg *config.Config,
	notifStore *notifications.Store,
) (*cron.CronService, error) {
	cronStorePath := filepath.Join(workspace, "cron", "jobs.json")

	cronService := cron.NewCronService(cronStorePath)

	// Owner-aware autonomous fire path (#264). The runner wakes a fired
	// schedule's OWNING agent (never the default), bounded by the per-run
	// deadline, and raises a notification + channel alert on failure. It is the
	// only fire path — the cron service records a no-op when no runner is set.
	// An owner is available when it is registered in the runtime registry.
	checker := agentCheckerFunc(func(agentID string) bool {
		_, ok := agentLoop.GetRegistry().GetAgent(agentID)
		return ok
	})
	runner := newScheduledRunner(agentLoop, checker, msgBus, notifStore, agentLoop.GetConfig)
	// Best-effort per-run child-process cleanup (FR-011). The minimal per-session
	// registry tracks PIDs the run spawns (via the tracker installed on the run
	// context, reported by the exec/shell tools) and terminates them on
	// completion — success, error, or timeout.
	procReg := newScheduledProcRegistry()
	runner.setProcessTracker(procReg.Track)
	runner.setProcessCleanup(procReg.Cleanup)
	cronService.SetRunner(runner)

	// Default agent id used only to migrate owner-less legacy jobs on load (W-8).
	defaultAgentID := ""
	if def := agentLoop.GetRegistry().GetDefaultAgent(); def != nil {
		defaultAgentID = def.ID
	}
	cronService.SetDefaultAgentID(defaultAgentID)

	if cfg != nil {
		cronService.SetMaxConcurrentRuns(cfg.Schedules.MaxConcurrentRuns)
		cronService.SetRetryBackoff(cfg.Schedules.RetryBackoffMs)
	}

	return cronService, nil
}

func (f agentCheckerFunc) IsRegistered(agentID string) bool { return f(agentID) }

// emitGHSARemovalWarn logs a WARN when any agent that has a remote channel
// mapping does NOT explicitly deny the bash tool. The GHSA-pv8c-p6jf-3fpp
// per-channel exec block was removed; bash access is now governed entirely by
// per-agent ToolPolicyCfg. This single-shot boot warning prompts operators to
// review agent policies. ADR-036 renamed the checked tool from "exec" to
// "bash" — this incidentally now also covers what used to be the separate
// workspace_shell/workspace_shell_bg tools, which this warning never covered
// before (they are the same tool now).
func emitGHSARemovalWarn(cfg *config.Config) {
	// Gather enabled remote channel types from the instance map.
	remoteChannelTypes := map[string]bool{
		"telegram":    true,
		"discord":     true,
		"slack":       true,
		"matrix":      true,
		"irc":         true,
		"google-chat": true,
		"whatsapp":    true,
	}
	enabledRemoteChannels := make(map[string]bool)
	for _, inst := range cfg.Channels {
		if inst.Enabled && remoteChannelTypes[inst.Type] {
			enabledRemoteChannels[inst.Type] = true
		}
	}
	if len(enabledRemoteChannels) == 0 {
		return
	}

	// Scan agents: flag any that do not explicitly deny bash. This is a boot
	// diagnostic (informational WARN), not an enforcement path — the real
	// enforcement is tools.EffectiveToolPolicy's fail-closed global×agent
	// merge (CLAUDE.md hard constraint 6: no default-policy fallback). Reading
	// the per-agent map directly here (rather than a resolver) means an agent
	// whose bash coverage comes only from the global sandbox.tool_policies map
	// is reported as "unset" at the per-agent layer — that is accurate for
	// this diagnostic's stated scope (per-agent policy), not a false positive.
	var flagged []string
	for _, ag := range cfg.Agents.List {
		if ag.Tools == nil {
			// No tools config at all → no explicit per-agent bash policy. Flagged.
			flagged = append(flagged, ag.ID)
			continue
		}
		policy, ok := ag.Tools.Builtin.Policies["bash"]
		if !ok {
			// No explicit per-agent entry for bash. Flagged (informational).
			flagged = append(flagged, ag.ID)
			continue
		}
		if policy != config.ToolPolicyDeny {
			flagged = append(flagged, ag.ID)
		}
	}

	if len(flagged) == 0 {
		return
	}

	channels := make([]string, 0, len(enabledRemoteChannels))
	for ch := range enabledRemoteChannels {
		channels = append(channels, ch)
	}
	slog.Warn(
		"bash tool no longer blocked at the channel layer (was GHSA-pv8c-p6jf-3fpp). "+
			"Agents with remote channels and non-deny bash policy: ["+strings.Join(flagged, ", ")+
			"]. Review per-agent ToolPolicyCfg.",
		"remote_channels", channels,
		"flagged_agents", flagged,
	)
}
