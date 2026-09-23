// loop_config.go: Live config, model and reload

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

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
	al.wireExecToolDepsOn(registry, cfg)

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

// SetReloadFunc sets the callback function for triggering config reload.
func (al *AgentLoop) SetReloadFunc(fn func() error) {
	al.reloadFunc = fn
}

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
