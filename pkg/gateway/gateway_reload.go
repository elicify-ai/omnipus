// gateway_reload.go: Live reload - config watcher and service restart

package gateway

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
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
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/heartbeat"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/voice"
)

// markReloadDegraded records a reload-adjacent failure so /health surfaces it
// (503, "config reload failed: <err>") until the next successful reload
// clears it (mirrors executeReload's own local markDegraded closure, which
// writes the same two fields under the same reloadMu). Exists as a method —
// rather than duplicating the lock/set/unlock pattern — so failure paths
// that reject a candidate config BEFORE executeReload is even reached (the
// manual-reload branch in RunContextWithOptions, and the file-watcher poller
// in setupConfigWatcherPolling — both guarding
// populateAgentsListFromEntityStoreStrict) can surface the same
// operator-visible degraded signal without reaching into executeReload's
// local closure or duplicating reloadMu handling at each call site.
func (s *services) markReloadDegraded(err error) {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.reloadDegraded = true
	s.reloadError = err
}

// servicesSnapshot captures all fields that restartServices and executeReload
// mutate, so they can be atomically restored on reload failure.
type servicesSnapshot struct {
	bundle         credentials.SecretBundle
	ChannelManager *channels.Manager
	CronService    *cron.CronService
	TaskTrigger    *agent.TaskTriggerScheduler
	LoopScheduler  *agent.LoopScheduler
	MediaStore     media.MediaStore
}

func snapshotServices(svc *services) servicesSnapshot {
	return servicesSnapshot{
		bundle:         svc.bundle,
		ChannelManager: svc.ChannelManager,
		CronService:    svc.CronService,
		TaskTrigger:    svc.TaskTrigger,
		LoopScheduler:  svc.LoopScheduler,
		MediaStore:     svc.MediaStore,
	}
}

func restoreServices(svc *services, snap servicesSnapshot) {
	svc.bundle = snap.bundle
	svc.ChannelManager = snap.ChannelManager
	svc.CronService = snap.CronService
	svc.TaskTrigger = snap.TaskTrigger
	svc.LoopScheduler = snap.LoopScheduler
	svc.MediaStore = snap.MediaStore
}

// beginReload claims the single-flight reload slot.
//
// Returns true when the caller now OWNS the reload and must run a cycle
// (runReloadCycle), which is then responsible for releasing the slot.
//
// Returns false when a reload is already in flight. The caller's request is NOT
// dropped: it is recorded in reloadRequested, and the owning cycle runs an
// additional reload — with a config re-read from disk — before releasing the
// slot. Callers must therefore treat false as "accepted, will be served
// shortly", never as an error.
//
// markPending (AgentLoop.MarkReloadPending in every caller) is invoked on BOTH
// branches, under the lock, and is what closes the last stale-read window.
// AgentLoop.TriggerReload has to set the pending flag before it can call
// reloadFunc — so between those two steps a finishing cycle can see no
// registered request, clear the flag, and release the slot; the request then
// arrives with a cleared flag and its poller returns immediately against the
// older config snapshot. Re-marking here makes "request registered" and "flag
// set" one atomic step against finishReload/abandonReload's clear, which take
// the same mutex. The resulting invariant is total: the pending flag is set on
// every path that leaves the slot claimed or a request recorded, and the only
// paths that clear it also release the slot, under this same lock.
func (s *services) beginReload(markPending func()) bool {
	s.reloadCoalesceMu.Lock()
	defer s.reloadCoalesceMu.Unlock()
	if s.reloadInFlight {
		s.reloadRequested = true
		markPending()
		return false
	}
	s.reloadInFlight = true
	s.reloadRequested = false
	markPending()
	return true
}

// finishReload decides the fate of the reload slot at the end of one reload,
// atomically with respect to beginReload.
//
// Returns true when another reload was requested while the one that just
// finished was running: the request is consumed, the slot is RETAINED, and the
// caller must run one more reload. Retaining the slot rather than releasing and
// re-acquiring is what makes a coalesced successor indivisible from its
// predecessor.
//
// Returns false when nothing is outstanding. Only then does it invoke
// clearPending — the agent loop's reload-pending flag, which
// restAPI.triggerReloadAndWait polls — and release the slot, both under the
// same lock acquisition as the check. Clearing that flag BETWEEN a reload and
// its coalesced successor would release pollers against a registry rebuilt from
// the older config snapshot: the very stale read this mechanism exists to
// prevent, entered through a different door.
//
// Because the check, the clear and the release happen under one lock, a
// concurrent beginReload either lands before it (and is observed as
// reloadRequested) or after it (and claims the freed slot itself). It can never
// fall between the two and be lost.
func (s *services) finishReload(clearPending func()) bool {
	s.reloadCoalesceMu.Lock()
	defer s.reloadCoalesceMu.Unlock()
	if s.reloadRequested {
		s.reloadRequested = false
		return true
	}
	clearPending()
	s.reloadInFlight = false
	return false
}

// abandonReload force-releases the slot without serving any outstanding
// request. Used only on a cycle's abnormal exits (panic, or a follow-up config
// load that failed), where continuing to hold the slot would wedge every future
// reload for the process lifetime, and by the trigger's unreachable
// channel-full fail-safe.
//
// clearPending may be nil (the trigger's fail-safe never owned the pending
// flag). When non-nil it is invoked BEFORE the slot is released and under the
// same lock, matching finishReload's ordering: releasing first would let a new
// trigger claim the slot and then have its freshly-set pending flag cleared by
// this call.
func (s *services) abandonReload(clearPending func()) {
	s.reloadCoalesceMu.Lock()
	defer s.reloadCoalesceMu.Unlock()
	if clearPending != nil {
		clearPending()
	}
	s.reloadInFlight = false
	s.reloadRequested = false
}

// newReloadTrigger builds the reload trigger closure wired into
// AgentLoop.SetReloadFunc, health.Server.SetReloadFunc and the sysagent tool
// deps. It claims the single-flight slot and hands the reload to the consumer
// loop over manualReloadChan.
//
// It NEVER reports "reload already in progress" as an error any more. A request
// that arrives while a reload is running is recorded by beginReload and served
// by a follow-up reload; the caller is told nil, because the request really was
// accepted. Returning an error there is what dropped the request outright and
// produced the POST /agents 201 → POST /tasks "agent not found" blocker
// documented on the services struct's reloadCoalesceMu field.
func newReloadTrigger(runningServices *services, agentLoop *agent.AgentLoop) func() error {
	return func() error {
		if !runningServices.beginReload(agentLoop.MarkReloadPending) {
			return nil
		}
		select {
		case runningServices.manualReloadChan <- struct{}{}:
			return nil
		default:
			// Unreachable in practice: the slot stays held until the consuming
			// cycle finishes, which is strictly after the receive, so the cap-1
			// channel is always drained whenever the slot is free. Kept as a
			// fail-safe — release the slot rather than wedging every future
			// reload behind a claim nobody will ever finish.
			runningServices.abandonReload(nil)
			return fmt.Errorf("reload already queued")
		}
	}
}

// runReloadCycle runs one config reload plus every reload coalesced into it
// while it was running, then releases the single-flight slot and clears the
// agent loop's reload-pending flag exactly once, at the very end.
//
// The caller MUST already own the slot — either beginReload returned true, or
// the reloadTrigger closure claimed it before signalling manualReloadChan.
//
// first is the config for the initial reload (the file-watcher path already has
// a candidate), or nil to load it from disk via loadNext (the /reload path).
// Every coalesced follow-up always re-reads from disk: serving it from the
// snapshot the previous reload used would defeat the entire point.
//
// exec runs one reload (executeReload in production) and loadNext re-reads
// config.json; both are parameters so the coalescing contract can be tested
// without standing up the full service-restart pipeline.
func runReloadCycle(
	agentLoop *agent.AgentLoop,
	runningServices *services,
	first *config.Config,
	exec func(*config.Config) error,
	loadNext func() (*config.Config, error),
) {
	slotHeld := true
	defer func() {
		// Abnormal exit only (panic, or a follow-up config load that failed).
		// On the normal path finishReload already cleared the pending flag and
		// released the slot, and slotHeld is false.
		if slotHeld {
			runningServices.abandonReload(agentLoop.ClearReloadPending)
		}
	}()

	cfg := first
	for {
		if cfg == nil {
			loaded, err := loadNext()
			if err != nil {
				// Retry once: a coalesced follow-up (or the manual /reload
				// path, which also enters here on its very first iteration)
				// re-reads config.json from disk, and a concurrent writer
				// (e.g. safeUpdateConfigJSON's atomic temp-file-then-rename)
				// can transiently race that read. One immediate retry
				// self-heals the common transient case without giving up on
				// a request beginReload already promised to serve.
				loaded, err = loadNext()
			}
			if err != nil {
				// FIX (14-reviewer sign-off, MEDIUM): a genuine, non-transient
				// config load failure here used to log-and-return, letting
				// the deferred abandonReload(agentLoop.ClearReloadPending)
				// clear the agent loop's reload-pending flag exactly as a
				// NORMAL completion would. Every triggerReloadAndWaitOutcome
				// poller still waiting on this cycle then observed
				// IsReloadPending()==false and reported confirmed=true — a
				// false "your change is live" for a request that beginReload
				// had recorded (markPending) but that this cycle never
				// actually served: no valid config was available, so exec
				// never ran for it.
				//
				// Mirror newReloadTrigger's own abandonReload(nil) fail-safe
				// a few dozen lines above — used for the identical reason (a
				// request recorded but never served): release the
				// single-flight slot, so future reloads are not wedged
				// forever (per abandonReload's own doc comment), but leave
				// the agent loop's pending flag SET. A poller then either
				// observes the NEXT reload cycle's genuine completion
				// (success or failure) clear it, or times out and correctly
				// reports confirmed=false — never the false confirmed=true
				// this replaces. Also mark the service degraded so GET
				// /health surfaces the failure immediately rather than only
				// through the (now honestly-still-set) pending flag.
				logger.Errorf("Config reload aborted: %v", err)
				runningServices.markReloadDegraded(fmt.Errorf("config reload aborted: reading config: %w", err))
				runningServices.abandonReload(nil)
				slotHeld = false
				return
			}
			cfg = loaded
		}
		if err := exec(cfg); err != nil {
			logger.Errorf("Config reload failed: %v", err)
		} else {
			logger.Info("Config reload completed successfully")
		}
		if !runningServices.finishReload(agentLoop.ClearReloadPending) {
			slotHeld = false
			return
		}
		// A reload was requested while the one above was running. That
		// requester's write landed AFTER the reload's config snapshot was taken,
		// so the reload it just waited through cannot have picked it up. Re-read
		// config from disk and reload again. We still hold the slot and the
		// pending flag is still set, so a triggerReloadAndWait poller stays
		// blocked across this boundary.
		logger.Info("Serving coalesced config reload request")
		cfg = nil
	}
}

func executeReload(
	ctx context.Context,
	agentLoop *agent.AgentLoop,
	newCfg *config.Config,
	provider *providers.LLMProvider,
	runningServices *services,
	msgBus *bus.MessageBus,
	allowEmptyStartup bool,
) error {
	// NOTE: this function deliberately does NOT release the reload slot or clear
	// the agent loop's reload-pending flag. Its caller (runReloadCycle) owns
	// both, because only the caller knows whether another reload was coalesced
	// into this one and must therefore still run before pollers are released.

	// Snapshot all service fields that restartServices mutates so they can be
	// restored atomically if the reload fails. bundle and ChannelManager are
	// mutated here in executeReload itself; the rest are mutated in
	// restartServices (CronService, TaskTrigger, MediaStore).
	// TaskDrain and MailboxDrain are also recreated by restartServices but are
	// NOT part of this atomic rollback snapshot.
	snap := snapshotServices(runningServices)

	markDegraded := func(err error) {
		slog.Error("config reload failed — rolling back to previous in-memory state", "error", err)
		restoreServices(runningServices, snap)
		runningServices.reloadMu.Lock()
		runningServices.reloadDegraded = true
		runningServices.reloadError = err
		runningServices.reloadMu.Unlock()
	}
	clearDegraded := func() {
		runningServices.reloadMu.Lock()
		runningServices.reloadDegraded = false
		runningServices.reloadError = nil
		runningServices.reloadMu.Unlock()
	}

	// CLAUDE.md hard constraint 6 / config.ValidateToolPolicyCoverage: a
	// config reload (file-watcher poll via configReloadChan, or manual
	// /reload via manualReloadChan — both funnel through this function) must
	// be held to the same tool-policy coverage bar as boot and the REST
	// write handlers. Without this check, a hand-edited config.json picked
	// up by hot-reload would bypass coverage enforcement entirely, silently
	// reintroducing a runtime-default gap this whole change eliminated.
	// repairAndValidateToolPolicyCoverage reconciles the global ceiling first
	// (same migration semantics as boot), then validates as a never-firing
	// correctness tripwire. A genuine gap rejects the reload and keeps
	// serving the PREVIOUS live config — mirrors the credential-injection-
	// failure rejection pattern immediately below.
	if gaps := repairAndValidateToolPolicyCoverage(newCfg); len(gaps) > 0 {
		for _, g := range gaps {
			slog.Error("gateway: reload tool-policy coverage gap", "detail", g.String())
		}
		reloadErr := fmt.Errorf(
			"reload rejected: tool-policy coverage validation failed (%d gap(s): %s)",
			len(gaps), joinCoverageGapMessages(gaps),
		)
		markDegraded(reloadErr)
		return reloadErr
	}

	// Re-inject provider credentials for the new config so LLM SDK clients
	// receive their secrets.
	//
	// Symmetric with boot (2026-08-14): one unresolvable provider/mailbox ref
	// is an ERROR + a degraded entry, NOT a rejected reload. Rejecting threw
	// away every other edit in the same save — an operator who typo'd one
	// api_key_ref got none of their changes applied and a degraded gateway,
	// and the same config would then refuse to boot on the next restart. A
	// store-wide failure (locked vault) still rejects and rolls back.
	if cs := runningServices.credStore; cs != nil {
		if errs := credentials.InjectFromConfig(newCfg, cs); len(errs) > 0 {
			if fatal := reportInjectionErrors(errs, "reload"); len(fatal) > 0 {
				reloadErr := fmt.Errorf(
					"reload rejected: provider credential injection failed: %w",
					errors.Join(fatal...),
				)
				markDegraded(reloadErr)
				return reloadErr
			}
		}

		// Re-resolve the SecretBundle for channels (no os.Setenv for channel creds).
		// enabledRefs mirrors bootCredentials: a NotFoundError (or any other
		// resolution error) on a channel ref that IS enabled in newCfg must
		// reject the reload — not silently continue — because "channel may be
		// disabled" is only true if we actually check. Missing the enabled
		// check here (unlike boot's equivalent NotFoundError-on-enabled fatal
		// branch) would let a reload silently break an enabled channel's
		// credentials while reporting success.
		//
		// Cross-cutting interaction (intentional, fail-closed tradeoff — see
		// the matching note on rest.go's configureChannel audit block): this
		// check is global, not scoped to whatever channel triggered this
		// reload. Config writes default to HotReload=true
		// (pkg/config/defaults.go), so ANY config.json write — including an
		// unrelated configureChannel call that only touched a different
		// channel — runs this same all-enabled-channels credential check via
		// the file-watcher. That means a configureChannel request can be
		// audited as DecisionAllow (its own write succeeded) and 200 OK to
		// the caller, and then have its effect asynchronously rolled back
		// moments later by markDegraded below because SOME OTHER enabled
		// channel's pre-existing credential ref fails to resolve — not the
		// channel the caller just configured. This is deliberate: we fail
		// closed rather than silently run an enabled channel with a broken
		// credential. There is no correlation ID linking the earlier audit
		// entry to this later rejection; an operator discovers it via
		// reloadDegraded surfaced on GET /health (503, "reason": "config
		// reload failed: …") and the "config reload failed — rolling back to
		// previous in-memory state" / "reload rejected: enabled channel
		// credential …" slog.Error lines emitted by markDegraded and the
		// branches below. Making rejection scoped to only the
		// just-edited channel would be a structural change to
		// reload-rejection scoping and is out of scope for this hotfix pass.
		enabledRefs := buildEnabledRefMap(newCfg)
		newBundle, bundleErrs := credentials.ResolveBundle(newCfg, cs)
		for _, e := range bundleErrs {
			var notFound *credentials.NotFoundError
			if errors.As(e, &notFound) {
				if enabledRefs[notFound.Name] {
					reloadErr := fmt.Errorf(
						"reload rejected: enabled credential %q not found in store: %w",
						notFound.Name, e,
					)
					markDegraded(reloadErr)
					return reloadErr
				}
				slog.Info("reload: credential not found (not currently enabled/in use)", "ref", notFound.Name)
				continue
			}
			// Any error other than "not found" on an enabled/in-use ref is
			// worse than a simple missing ref (the credential exists but can't
			// be read) — escalate exactly like the NotFoundError-on-enabled
			// case above, reusing the existing reject-and-rollback mechanism
			// (markDegraded / reloadDegraded) rather than a log-only Warn or a
			// new degraded-signal field.
			if ref, ok := enabledRefFromBundleError(e, enabledRefs); ok {
				reloadErr := fmt.Errorf(
					"reload rejected: enabled credential %q failed to resolve: %w",
					ref, e,
				)
				markDegraded(reloadErr)
				return reloadErr
			}
			slog.Warn("reload: credential bundle resolution error", "error", e)
		}
		runningServices.bundle = newBundle

		// Re-register the complete plaintext set so the scrubber stays current
		// after reload: config refs, enabled MCP env values, and OAuth grants
		// Omnipus owns in the encrypted store.
		reloadValues := make([]string, 0, len(newBundle))
		for _, v := range newBundle {
			if v != "" {
				reloadValues = append(reloadValues, v)
			}
		}
		// BUG 4 / architect finding: MCP server env-var secrets — see
		// mcpEnabledEnvSensitiveValues's doc comment (bootCredentials has the
		// matching call for the boot path).
		reloadValues = append(reloadValues, mcpEnabledEnvSensitiveValues(newCfg, cs)...)
		reloadValues = append(reloadValues, providers.CollectOAuthSensitiveValues(cs)...)
		newCfg.RegisterSensitiveValues(reloadValues)
	}
	if err := handleConfigReload(
		ctx,
		agentLoop,
		newCfg,
		provider,
		runningServices,
		msgBus,
		allowEmptyStartup,
	); err != nil {
		markDegraded(err)
		return err
	}
	clearDegraded()
	return nil
}

func handleConfigReload(
	ctx context.Context,
	al *agent.AgentLoop,
	newCfg *config.Config,
	providerRef *providers.LLMProvider,
	runningServices *services,
	msgBus *bus.MessageBus,
	allowEmptyStartup bool,
) error {
	logger.Info("🔄 Config file changed, reloading...")

	newModel := newCfg.Agents.Defaults.DefaultModel.String()

	logger.Infof(" New model is '%s', recreating provider...", newModel)

	logger.Info("  Stopping all services...")
	stopAndCleanupServices(runningServices, serviceShutdownTimeout, true)

	// Build the real LLM provider on reload. The test_harness override hook
	// was removed 2026-05-10; reload always recreates the real provider from
	// the new config's `providers` entry.
	newProvider, _, err := createStartupProvider(newCfg, allowEmptyStartup)
	if err != nil {
		logger.Errorf("  ⚠ Error creating new provider: %v", err)
		logger.Warn("  Attempting to restart services with old provider and config...")
		if restartErr := restartServices(al, runningServices, msgBus); restartErr != nil {
			// The rollback restart ALSO failed — services may now be left in a
			// worse, partially-restarted state than a plain "provider creation
			// failed" error implies. Discarding restartErr here (previously only
			// logged) would hide that from the caller (executeReload's
			// markDegraded only ever sees the returned error); join both so
			// reloadError / the reload's returned error surfaces the compound
			// failure instead of just the primary one.
			logger.Errorf("  ⚠ Failed to restart services: %v", restartErr)
			return fmt.Errorf(
				"error creating new provider: %w; additionally, rollback restart failed: %w",
				err,
				restartErr,
			)
		}
		return fmt.Errorf("error creating new provider: %w", err)
	}

	// ADR-068 FR-020: no reload path back-fills agents.defaults.default_model
	// (the twin `ModelName == ""` guard was deleted with the alias).
	reloadCtx, reloadCancel := context.WithTimeout(context.Background(), providerReloadTimeout)
	defer reloadCancel()

	if err := al.ReloadProviderAndConfig(reloadCtx, newProvider, newCfg); err != nil {
		logger.Errorf("  ⚠ Error reloading agent loop: %v", err)
		if cp, ok := newProvider.(providers.StatefulProvider); ok {
			cp.Close()
		}
		logger.Warn("  Attempting to restart services with old provider and config...")
		if restartErr := restartServices(al, runningServices, msgBus); restartErr != nil {
			// Same compound-failure concern as the provider-creation branch
			// above: surface the rollback failure alongside the primary one
			// instead of discarding it after only a log line.
			logger.Errorf("  ⚠ Failed to restart services: %v", restartErr)
			return fmt.Errorf(
				"error reloading agent loop: %w; additionally, rollback restart failed: %w",
				err,
				restartErr,
			)
		}
		return fmt.Errorf("error reloading agent loop: %w", err)
	}

	*providerRef = newProvider

	logger.Info("  Restarting all services with new configuration...")
	if err := restartServices(al, runningServices, msgBus); err != nil {
		logger.Errorf("  ⚠ Error restarting services: %v", err)
		return fmt.Errorf("error restarting services: %w", err)
	}

	logger.Info("  ✓ Provider, configuration, and services reloaded successfully (thread-safe)")
	return nil
}

// restartServicesState carries the shared state of restartServices across its stages.
type restartServicesState struct {
	al              *agent.AgentLoop
	runningServices *services
	msgBus          *bus.MessageBus
	cfg             *config.Config
	homePath        string
	err             error
}

func restartServices(
	al *agent.AgentLoop,
	runningServices *services,
	msgBus *bus.MessageBus,
) error {
	rs := &restartServicesState{al: al, runningServices: runningServices, msgBus: msgBus}

	if r0, stop := rs.restartCron(); stop {
		return r0
	}

	if r0, stop := rs.restartSchedulersAndDrains(); stop {
		return r0
	}

	rs.replaceMediaStore()

	if r0, stop := rs.reloadChannels(); stop {
		return r0
	}

	if r0, stop := rs.restartVoice(); stop {
		return r0
	}

	return rs.applyMessagingCaps()
}

// restartCron recreates and starts the cron service, then refreshes its REST API reference.
func (rs *restartServicesState) restartCron() (error, bool) {
	rs.cfg = rs.al.GetConfig()

	// FIX (14-reviewer sign-off, MEDIUM): this used to derive the home dir as
	// filepath.Dir(cfg.AgentHomeBasePath()) — i.e. assuming
	// cfg.Agents.Defaults.Home is always exactly "<OMNIPUS_HOME>/agents", so
	// stepping one directory up recovers OMNIPUS_HOME. Boot
	// (setupAndStartServices) never made that assumption: it takes homePath
	// as an explicit parameter and joins directly off it, storing the same
	// value in runningServices.homePath for exactly this reason (see that
	// field's doc comment). Whenever an operator customizes
	// agents.defaults.home to a path whose parent isn't OMNIPUS_HOME (or that
	// doesn't even live under it), the two derivations diverge and a hot
	// reload silently re-homes the notification store / task-trigger
	// scheduler / loop scheduler onto a different, likely-empty directory —
	// the operator's existing notifications/triggers/loop jobs would appear
	// to vanish. Use the SAME field boot used, not a re-derivation, so the
	// two paths are equal by construction rather than by convention.
	rs.homePath = rs.runningServices.homePath

	if rs.runningServices.notifStore == nil {
		// Derive the home dir from the workspace path (workspace == <home>/workspace).
		rs.runningServices.notifStore = notifications.NewStore(
			filepath.Join(rs.homePath, "notifications"),
		)
	}

	rs.runningServices.CronService, rs.err = setupCronTool(
		rs.al,
		rs.msgBus,
		rs.cfg.AgentHomeBasePath(),
		rs.cfg,
		rs.runningServices.notifStore,
	)
	if rs.err != nil {
		return fmt.Errorf("error restarting cron service: %w", rs.err), true
	}
	if rs.err = rs.runningServices.CronService.Start(); rs.err != nil {
		return fmt.Errorf("error restarting cron service: %w", rs.err), true
	}
	// Re-point the restAPI's cronService field to the newly started instance.
	// restAPI.cronService is assigned once at construction time
	// (setupAndStartServices). On each reload, restartServices replaces
	// runningServices.CronService with a new instance whose laneCtx is live;
	// without this update the restAPI holds a stale pointer whose laneCtx was
	// canceled by the previous Stop(), causing "turn not started: context
	// canceled" on every RunNow call (#412).
	if rs.runningServices.restAPIRef != nil {
		rs.runningServices.restAPIRef.cronService.Store(rs.runningServices.CronService)
	}
	fmt.Println("  ✓ Cron service restarted")
	return nil, false
}

// restartSchedulersAndDrains restarts the plan engine, schedulers, and background task and mailbox drains.
func (rs *restartServicesState) restartSchedulersAndDrains() (error, bool) {
	// Restart the SAME plan-engine instance (ADR-049 D4) — unlike CronService,
	// the engine is not reconstructed on reload: it was Stop()'d in
	// stopAndCleanupServices(isReload=true) above, and Start() on an
	// already-constructed *PlanEngine is safe to call again (fresh stopCh,
	// re-subscribes to the event bus, re-runs boot reconciliation). taskStore/
	// taskExecutor are themselves stable across a reload (owned by the SAME
	// al, never recreated), so there is nothing to re-wire.
	if rs.runningServices.PlanEngine != nil {
		if startErr := rs.runningServices.PlanEngine.Start(context.Background()); startErr != nil {
			return fmt.Errorf("error restarting plan engine: %w", startErr), true
		}
		fmt.Println("  ✓ Plan engine restarted")
	}

	// Restart the task time-trigger scheduler on its dedicated CronService. The
	// previous instance was already Stop()'d in stopAndCleanupServices(isReload).
	if tStore := agent.GetTaskStore(rs.al); tStore != nil {
		triggerStorePath := filepath.Join(rs.homePath, "tasks_triggers", "jobs.json")
		rs.runningServices.TaskTrigger = agent.NewTaskTriggerScheduler(
			triggerStorePath, tStore, agent.GetTaskExecutor(rs.al),
		)
		if startErr := rs.runningServices.TaskTrigger.Start(); startErr != nil {
			return fmt.Errorf("error restarting task trigger scheduler: %w", startErr), true
		}
		rs.al.SetTaskTriggerScheduler(rs.runningServices.TaskTrigger)
		if recErr := rs.runningServices.TaskTrigger.Reconcile(); recErr != nil {
			slog.Error("gateway: task trigger reconcile failed on reload", "error", recErr)
		}
		fmt.Println("  ✓ Task trigger scheduler restarted")
	}

	// Restart the /loop scheduler on a fresh dedicated CronService (ADR-049
	// D6/D7). The previous instance was already Stop()'d in
	// stopAndCleanupServices(isReload) — mirrors the task trigger restart
	// immediately above.
	{
		loopSchedStorePath := filepath.Join(rs.homePath, "loops", "jobs.json")
		rs.runningServices.LoopScheduler = agent.NewLoopScheduler(loopSchedStorePath, rs.al)
		if startErr := rs.runningServices.LoopScheduler.Start(); startErr != nil {
			return fmt.Errorf("error restarting loop scheduler: %w", startErr), true
		}
		rs.al.SetLoopScheduler(rs.runningServices.LoopScheduler)
		fmt.Println("  ✓ Loop scheduler restarted")
	}

	// Queued-task draining is owned by the dedicated TaskDrainService, never the
	// heartbeat path — restart it here so dispatch survives a reload regardless of
	// the heartbeat configuration.
	if te := agent.GetTaskExecutor(rs.al); te != nil {
		rs.runningServices.TaskDrain = heartbeat.NewTaskDrainService(te, 0)
		rs.runningServices.TaskDrain.Start()
		fmt.Println("  ✓ Queued-task drain restarted (TaskDrainService)")
	}

	// Restart the M11 mailbox drain (unhandled mail → Board tasks). The previous
	// instance was Stop()'d in stopAndCleanupServices(isReload). The provider reads
	// live config + the credential store on each tick, so a mailbox added/removed
	// before this reload is reflected immediately.
	if tStore := agent.GetTaskStore(rs.al); tStore != nil {
		credStore := rs.runningServices.credStore
		provider := email.MailboxProviderFunc(func() []email.Mailbox {
			return buildMailboxes(rs.al.GetConfig(), credStore)
		})
		drainer := email.NewDrainer(tStore, provider, 0)
		rs.runningServices.MailboxDrain = heartbeat.NewMailboxDrainService(drainer, 0)
		rs.runningServices.MailboxDrain.Start()
		fmt.Println("  ✓ Mailbox drain restarted (MailboxDrainService)")
	}
	return nil, false
}

// replaceMediaStore installs a fresh media store before flushing and stopping the previous store.
func (rs *restartServicesState) replaceMediaStore() {
	// N-D fix: build and wire the NEW store BEFORE stopping the old one so that
	// any upload whose scheduleSave fires in the narrow window between the Stop
	// and the SetMediaStore calls writes into the new (live) store rather than
	// being silently dropped by the stopped store.
	//
	// Order:
	//   1. Create new store (inactive; Start() below begins the cleanup goroutine).
	//   2. Swap the agent-loop pointer so new uploads land in the new store.
	//   3. Flush the old store (pending debounced saves) and stop its goroutines.
	//
	// The old store is retained via oldStore until after Stop() completes so the
	// GC does not reclaim it while the debounced save goroutine may still be
	// running.
	oldStore := rs.runningServices.MediaStore
	rs.runningServices.MediaStore = media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{
		Enabled:  rs.cfg.Tools.MediaCleanup.Enabled,
		MaxAge:   time.Duration(rs.cfg.Tools.MediaCleanup.MaxAge) * time.Minute,
		Interval: time.Duration(rs.cfg.Tools.MediaCleanup.Interval) * time.Minute,
	})
	if fms, ok := rs.runningServices.MediaStore.(*media.FileMediaStore); ok {
		// Reload refs persisted by a previous gateway instance so
		// /api/v1/media/<ref> URLs in old session transcripts still resolve.
		// Best-effort — a load failure should not block boot.
		if loadErr := fms.LoadRegistry(); loadErr != nil {
			slog.Warn("media: failed to load persisted registry", "error", loadErr)
		}
		fms.Start()
		// Re-wire workspace-library provider onto the new store (same shared
		// AgentLoop cache as boot) so media://workspace/ refs keep resolving
		// after hot reload.
		fms.SetWorkspaceLibraryProvider(func(workspaceID string) (media.WorkspaceLibraryResolver, error) {
			lib := rs.al.GetWorkspaceLibrary(workspaceID)
			if lib == nil {
				return nil, fmt.Errorf("workspace library unavailable for %q", workspaceID)
			}
			return lib, nil
		})
	}
	// Swap the live pointer first so uploads arriving after this point use the
	// new store (closes the N-D reload-swap window).
	rs.al.SetMediaStore(rs.runningServices.MediaStore)
	// Now flush and stop the old store. Stop() is idempotent and safe to call
	// even if the cleanup goroutine was never started (N3 lifecycle fix).
	if oldFMS, ok := oldStore.(*media.FileMediaStore); ok {
		oldFMS.Stop()
	}
}

// reloadChannels rewires and reloads channels, then reports the enabled channel set.
func (rs *restartServicesState) reloadChannels() (error, bool) {
	rs.al.SetChannelManager(rs.runningServices.ChannelManager)
	// Pre-Reload wire: set observers before Reload() so that channels whose
	// Start() runs *inside* Reload() already have the CancelInterceptor and
	// PairingObserver set when they first emit events.
	wireChannelManager(rs.runningServices.ChannelManager, rs.al)

	if rs.err = rs.runningServices.ChannelManager.Reload(context.Background(), rs.cfg, rs.runningServices.bundle); rs.err != nil {
		return fmt.Errorf("error reload channels: %w", rs.err), true
	}
	// Post-Reload re-wire: Reload() may have recreated channel instances (new
	// struct value with nil fields), which clears the observer pointer set
	// above.  Re-wiring here ensures the observers are always live once the
	// reload completes, regardless of whether instances were recreated.
	wireChannelManager(rs.runningServices.ChannelManager, rs.al)
	fmt.Println("  ✓ Channels restarted.")

	enabledChannels := rs.runningServices.ChannelManager.GetEnabledChannels()
	if len(enabledChannels) > 0 {
		fmt.Printf("  ✓ Channels enabled: %s\n", enabledChannels)
	} else {
		fmt.Println("  ⚠ Warning: No channels enabled")
		// FIX 2: reload-time twin of the same warning in setupAndStartServices —
		// pair with logger.WarnCF for the same reason (invisible otherwise on a
		// backgrounded gateway; a reload can be the moment the last channel got
		// disabled, which is exactly when an operator most needs to notice).
		logger.WarnCF("gateway", "no channels enabled after reload — gateway has no reachable channel", nil)
	}
	return nil, false
}

// restartVoice refreshes the configured transcriber.
func (rs *restartServicesState) restartVoice() (error, bool) {
	transcriber := voice.DetectTranscriber(rs.cfg, rs.runningServices.bundle)
	rs.al.SetTranscriber(transcriber)
	if transcriber != nil {
		logger.InfoCF("voice", "Transcription re-enabled (agent-level)", map[string]any{"provider": transcriber.Name()})
	} else {
		logger.InfoCF("voice", "Transcription disabled", nil)
	}
	return nil, false
}

// applyMessagingCaps reapplies live session-messaging limits to the durable inbox store.
func (rs *restartServicesState) applyMessagingCaps() error {
	// FIX (14-reviewer sign-off, MEDIUM): re-apply the live session_messaging
	// caps to the durable inbox store on every reload. setupAndStartServices'
	// own comment at the boot call site ("Apply the live config's caps to the
	// store so a session_messaging edit (hot-reloaded) is reflected on the
	// next Append") was a promise this function never kept — the caps were
	// only ever written once, at boot, and restartServices never revisited
	// them. An operator editing session_messaging.* limits and reloading saw
	// "Config reload completed successfully" while the inbox kept enforcing
	// the ORIGINAL boot-time caps for the rest of the process's life. The
	// store itself is process-lifetime (never reconstructed on reload, unlike
	// CronService/MediaStore above), so re-fetch it via the agent loop and
	// overwrite its plain-field caps in place — the store's own doc comment
	// on these fields (pkg/session/message_inbox.go) already documents them
	// as "re-read per call", so this is safe to do without touching the
	// store's other state.
	if inbox := rs.al.GetMessageInboxStore(); inbox != nil {
		smCfg := rs.cfg.SessionMessaging
		inbox.ChildSendRatePerMinute = smCfg.EffectiveChildSendRatePerMinute()
		inbox.ChildSendBodyBytes = smCfg.EffectiveChildSendBodyBytes()
		inbox.ChildSendMaxDepth = smCfg.EffectiveChildSendMaxDepth()
		inbox.InboxUnackedMax = smCfg.EffectiveInboxUnackedMax()
		inbox.InboxPerTypeCeiling = smCfg.EffectiveInboxPerTypeCeiling()
		fmt.Println("  ✓ Session-messaging caps re-applied from live config")
	}

	return nil
}

// setupConfigWatcherPolling starts a background goroutine that polls
// configPath every 2 s for mtime/size changes and emits the new *config.Config
// on the returned channel.
//
// selfWriteReg (may be nil) is consulted to suppress reloads for writes that
// the app itself made via safeUpdateConfigJSON. When the detected change is
// identified as an app-initiated write its hash is consumed from the registry
// and the reload is skipped, preventing spurious full-service restarts on every
// login, settings change, or channel-config write. Only genuine external edits
// (hashes not present in the registry) proceed to executeReload.
//
// homePath is $OMNIPUS_HOME, threaded through explicitly by the caller
// (RunContextWithOptions, which already resolves it correctly) rather than
// derived from configPath here. A prior version of this function derived the
// entities root as filepath.Dir(configPath) under a comment claiming
// "configPath is always $OMNIPUS_HOME/config.json by convention (see
// agentstore.New's own doc comment)" — that citation was fabricated
// (agentstore.New's doc says nothing of the kind) and the claim itself is
// false whenever OMNIPUS_CONFIG (config.EnvConfig,
// cmd/omnipus/internal/helpers.go's GetConfigPath) overrides the config path
// to somewhere outside $OMNIPUS_HOME: filepath.Dir(configPath) then points at
// the wrong directory, entity.Store.List maps a missing entities/agents/ dir
// to (nil, nil, nil) — no error — and the entire in-memory agent roster
// silently vanishes on the next external config edit. Passing the real
// homePath in avoids the derivation entirely.
//
// markDegraded (may be nil, e.g. in tests) is called when
// populateAgentsListFromEntityStoreStrict rejects a candidate config for
// this home — see its doc for why an empty/wiped roster is a
// privilege-escalation risk, not merely a UX gap. This lets the poller
// surface the same operator-visible /health degraded signal executeReload's
// own internal checks already produce, for a failure that happens BEFORE
// executeReload is even reached.
func setupConfigWatcherPolling(
	configPath string,
	homePath string,
	debug bool,
	credStore *credentials.Store,
	selfWriteReg *configSelfWriteRegistry,
	markDegraded func(error),
) (chan *config.Config, func()) {
	configChan := make(chan *config.Config, 1)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		lastModTime := getFileModTime(configPath)
		lastSize := getFileSize(configPath)

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				currentModTime := getFileModTime(configPath)
				currentSize := getFileSize(configPath)

				if currentModTime.After(lastModTime) || currentSize != lastSize {
					if debug {
						logger.Debugf("🔍 Config file change detected")
					}

					// 500 ms debounce: let concurrent rapid writes settle so
					// we read the final state rather than a transient version.
					time.Sleep(500 * time.Millisecond)

					// Re-stat after the debounce; the file may have changed again.
					lastModTime = getFileModTime(configPath)
					lastSize = getFileSize(configPath)

					// Read current file content to compute identity hash.
					fileBytes, readErr := os.ReadFile(configPath)
					if readErr != nil {
						logger.Errorf("⚠ Could not read config for change check: %v", readErr)
						continue
					}
					currentHash := sha256.Sum256(fileBytes)

					// Suppress reload when the change was made by the app itself.
					if selfWriteReg != nil && selfWriteReg.consume(currentHash) {
						if debug {
							logger.Debugf("Config file change is app-initiated — skipping reload")
						}
						continue
					}

					// LoadConfigWithStoreAndSelfHealHook (not LoadConfigWithStore):
					// this poller already reads config.json outside configMu, so if
					// the single-user-model role self-heal performs a write as a
					// side effect of THIS load, it must be registered with
					// selfWriteReg too — otherwise the self-heal's own write would
					// be misdetected as a second external edit on the next tick.
					newCfg, err := config.LoadConfigWithStoreAndSelfHealHook(
						configPath, credStore, selfHealWriteHook(selfWriteReg),
					)
					if err != nil {
						logger.Errorf("⚠ Error loading new config: %v", err)
						logger.Warn("  Using previous valid config")
						continue
					}
					// ADR-054 D2/D3: repopulate cfg.Agents.List from the agent
					// store — config.LoadConfig* strips agents.list on every
					// load (legacy_agents_list.go), and this file-watcher
					// poller is a separate config-load call site from
					// restAPI.refreshConfigAndRewireServices's own bridge.
					// Strict variant + the real homePath (see this function's
					// doc comment for why filepath.Dir(configPath) was wrong):
					// a roster-population failure must reject this reload
					// attempt, not silently proceed with an empty/stale
					// roster.
					if rosterErr := populateAgentsListFromEntityStoreStrict(newCfg, homePath); rosterErr != nil {
						logger.Errorf("⚠ Config reload: agent roster population failed: %v", rosterErr)
						logger.Warn("  Using previous valid config")
						if markDegraded != nil {
							markDegraded(fmt.Errorf("config reload rejected: agent roster population failed: %w", rosterErr))
						}
						continue
					}

					if err := newCfg.ValidateProviders(); err != nil {
						logger.Errorf("  ⚠ New config validation failed: %v", err)
						logger.Warn("  Using previous valid config")
						continue
					}

					logger.Info("✓ Config file validated and loaded (external edit)")

					select {
					case configChan <- newCfg:
					default:
						logger.Warn("⚠ Previous config reload still in progress, skipping")
					}
				}
			case <-stop:
				return
			}
		}
	}()

	stopFunc := func() {
		close(stop)
		wg.Wait()
	}

	return configChan, stopFunc
}

func getFileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		slog.Debug("gateway: could not stat file for mod time", "path", path, "error", err)
		return time.Time{}
	}
	return info.ModTime()
}

func getFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		slog.Debug("gateway: could not stat file for size", "path", path, "error", err)
		return 0
	}
	return info.Size()
}
