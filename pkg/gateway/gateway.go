//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	runtimedebug "runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/dingtalk"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/discord"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/feishu"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/googlechat"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/irc"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/line"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/qq"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/slack"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/telegram"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/wecom"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/weixin"
	_ "github.com/dapicom-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/daemon"
	"github.com/dapicom-ai/omnipus/pkg/datamodel"
	"github.com/dapicom-ai/omnipus/pkg/devices"
	"github.com/dapicom-ai/omnipus/pkg/email"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
	"github.com/dapicom-ai/omnipus/pkg/health"
	"github.com/dapicom-ai/omnipus/pkg/heartbeat"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/media"
	"github.com/dapicom-ai/omnipus/pkg/notifications"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/policy"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/skills"
	"github.com/dapicom-ai/omnipus/pkg/state"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
	"github.com/dapicom-ai/omnipus/pkg/task"
	"github.com/dapicom-ai/omnipus/pkg/tools"
	"github.com/dapicom-ai/omnipus/pkg/tools/browser"
	"github.com/dapicom-ai/omnipus/pkg/voice"
)

const (
	serviceShutdownTimeout  = 30 * time.Second
	providerReloadTimeout   = 30 * time.Second
	gracefulShutdownTimeout = 15 * time.Second

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
	CronService *cron.CronService
	TaskTrigger *agent.TaskTriggerScheduler // fires once/every/recurring task triggers via a dedicated CronService
	// TaskDrain owns the queued-task (`next` → dispatch) poll unconditionally,
	// independent of which heartbeat path is active. The now-removed global
	// HeartbeatService was skipped whenever a per-agent heartbeat was active,
	// which meant `next` tasks never dispatched on those installs; TaskDrain is
	// decoupled from that path so `next` tasks always dispatch.
	TaskDrain *heartbeat.TaskDrainService
	// MailboxDrain owns the M11 unhandled-mail → Board-task poll. Like TaskDrain
	// it is decoupled from the HEARTBEAT.md path so email work surfaces on the
	// Board regardless of which heartbeat path is active. Nil when no mailbox is
	// configured (the scanner is a no-op).
	MailboxDrain *heartbeat.MailboxDrainService
	MediaStore   media.MediaStore
	// notifStore backs schedule-failure notifications and the header
	// notification center (#264). Created once at boot, reused across reloads.
	notifStore *notifications.Store
	// ChannelManager is read-only to HTTP handlers (they access it via the
	// agent loop's GetChannelManager). It is written only during executeReload,
	// which is single-flighted by the reloading atomic.Bool. No handler reads
	// this field directly, so no additional lock is required for read access.
	ChannelManager   *channels.Manager
	DeviceService    *devices.Service
	HealthServer     *health.Server
	manualReloadChan chan struct{}
	reloading        atomic.Bool
	reloadTrigger    func() error
	credStore        *credentials.Store
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

	// homePath is the Omnipus home directory. Stored here so omnipusGracefulShutdown
	// can remove the self-registered PID file without an additional parameter.
	homePath string
}

type startupBlockedProvider struct {
	reason string
}

func (p *startupBlockedProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return nil, fmt.Errorf("%s", p.reason)
}

func (p *startupBlockedProvider) GetDefaultModel() string {
	return ""
}

// buildEnabledRefMap returns a set of credential ref names that belong to
// channels that are currently enabled. Used by bootCredentials to distinguish
// a missing credential on an enabled channel (fatal) from one on a disabled
// channel (Info + continue). Only channel refs are included — provider
// APIKeyRef misses are already fatal via InjectFromConfig.
func buildEnabledRefMap(cfg *config.Config) map[string]bool {
	m := make(map[string]bool)
	for _, inst := range cfg.Channels {
		if !inst.Enabled {
			continue
		}
		// Collect all *_ref fields that are non-empty for this enabled instance.
		for _, ref := range []string{
			inst.TokenRef,
			inst.BotTokenRef,
			inst.AppTokenRef,
			inst.AppSecretRef,
			inst.EncryptKeyRef,
			inst.VerificationTokenRef,
			inst.ClientSecretRef,
			inst.AccessTokenRef,
			inst.CryptoPassphraseRef,
			inst.ChannelSecretRef,
			inst.ChannelAccessTokenRef,
			inst.SecretRef,
			inst.WebhookURLRef,
			inst.ServiceAccountJSONRef,
			inst.PasswordRef,
			inst.NickServPasswordRef,
			inst.SASLPasswordRef,
		} {
			if ref != "" {
				m[ref] = true
			}
		}
	}
	return m
}

// bootCredentials runs the canonical credential + config boot sequence and
// returns the initialized config, secret bundle, and store.
//
// Sequence (matches ADR-004 §Boot Order Contract):
//  1. NewStore → Unlock (fatal on failure)
//  2. LoadConfigWithStore (fatal on failure)
//  3. InjectFromConfig for provider env-vars (fatal on failure)
//  4. ResolveBundle for channel secrets (NotFoundError for disabled channels is Info, rest Warn)
//  5. cfg.RegisterSensitiveValues with all resolved plaintexts
//
// Both Run and boot_order_test.go call this helper so that a refactor of one
// cannot silently drift from the other.
func bootCredentials(
	homePath, configPath string,
) (*config.Config, credentials.SecretBundle, *credentials.Store, error) {
	credStore := credentials.NewStore(filepath.Join(homePath, "credentials.json"))
	if unlockErr := credentials.Unlock(credStore); unlockErr != nil {
		return nil, nil, nil, fmt.Errorf("credential store: %w", unlockErr)
	}

	cfg, err := config.LoadConfigWithStore(configPath, credStore)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error loading config: %w", err)
	}

	// Inject provider API keys into the process environment so LLM SDK clients
	// can read them via os.Getenv. Channels use SecretBundle instead (no env injection).
	if errs := credentials.InjectFromConfig(cfg, credStore); len(errs) > 0 {
		for _, e := range errs {
			slog.Error("provider credential injection failed", "error", e)
		}
		return nil, nil, nil, fmt.Errorf(
			"fatal: provider credential injection failed — ensure OMNIPUS_MASTER_KEY is set and all referenced credentials exist",
		)
	}

	// Build a ref→enabled map so we can distinguish a missing credential on an
	// enabled channel (fatal) from one on a disabled channel (Info + continue).
	enabledRefs := buildEnabledRefMap(cfg)

	// Resolve all credential refs into a SecretBundle. Channels receive secrets
	// via the bundle — no os.Setenv for channel credentials (B1 fix).
	bundle, bundleErrs := credentials.ResolveBundle(cfg, credStore)
	for _, e := range bundleErrs {
		var notFound *credentials.NotFoundError
		if errors.As(e, &notFound) {
			if enabledRefs[notFound.Name] {
				// Missing credential on an enabled channel is fatal at boot.
				return nil, nil, nil, fmt.Errorf(
					"fatal: enabled channel credential %q not found in store — "+
						"ensure the credential is stored before starting: %w",
					notFound.Name, e,
				)
			}
			slog.Info("channel credential not found (channel is disabled)", "ref", notFound.Name)
			continue
		}
		slog.Warn("credential bundle resolution error", "error", e)
	}

	// Register all resolved plaintext credentials with the config's sensitive-data
	// replacer so they are scrubbed from LLM output and audit logs (A1 fix).
	// Semantics are "replace", so every call installs the complete current set.
	values := make([]string, 0, len(bundle))
	for _, v := range bundle {
		if v != "" {
			values = append(values, v)
		}
	}
	cfg.RegisterSensitiveValues(values)

	return cfg, bundle, credStore, nil
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

// resolveAllowGodMode is the single, boot-time computation of god-mode
// AVAILABILITY authorization (O14): the legacy --allow-god-mode CLI flag OR
// the config-persisted sandbox.god_mode_allowed grant (written by POST
// /api/v1/gateway/god-mode enabled=true from the Settings UI). It does NOT
// consult sandbox.GodModeAvailable (build support) — callers combine that
// separately so the two failure modes ("not authorized" vs "not supported by
// this build") stay distinguishable.
//
// Pure and deterministic so it is unit-testable without booting a gateway.
// A nil cfg only consults the flag — defensive for callers that have not yet
// loaded config (never happens on the real boot path, but keeps this safe to
// call early).
//
// Called exactly once at boot; the result flows unchanged into
// agent.AgentLoop.SetAllowGodMode (the process-wide availability atomic that
// gates the live tool-policy/sandbox override) and restAPI.allowGodMode (the
// REST predicate consulted by GET/POST /api/v1/gateway/god-mode). Because
// this is computed once and frozen for the process lifetime, a config-only
// grant (UI enable while allowGodMode was false at boot) does not change live
// enforcement until the next restart re-reads sandbox.god_mode_allowed —
// this is intentional and is exactly why setGodMode reports
// restart_required=true for that case.
func resolveAllowGodMode(cliFlag bool, cfg *config.Config) bool {
	if cliFlag {
		return true
	}
	return cfg != nil && cfg.Sandbox.GodModeAllowed
}

// SandboxBootError wraps a sandbox Apply/Install failure so the CLI entry
// point can distinguish it from generic boot errors and exit with the
// Sprint-J-specific EX_CONFIG (78) code per FR-J-004.
type SandboxBootError struct {
	Err error
}

// Error makes SandboxBootError satisfy the error interface.
func (e *SandboxBootError) Error() string {
	if e == nil || e.Err == nil {
		return "sandbox boot error"
	}
	return e.Err.Error()
}

// Unwrap exposes the underlying error for errors.Is/As traversal.
func (e *SandboxBootError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Run starts the gateway runtime using the configuration loaded from configPath.
// It installs OS signal handlers (SIGINT, SIGTERM) and blocks until one fires,
// then delegates to RunContext for the actual boot and serve logic.
// Zero behavior change from the caller's perspective — the CLI entry point
// continues to call Run unchanged.
//
// For Sprint-J options (--sandbox), call RunWithOptions instead.
func Run(debug bool, homePath, configPath string, allowEmptyStartup bool) error {
	return RunWithOptions(RunOptions{
		Debug:             debug,
		HomePath:          homePath,
		ConfigPath:        configPath,
		AllowEmptyStartup: allowEmptyStartup,
	})
}

// RunWithOptions is the Sprint-J entry point. Handles the same boot flow as
// Run but accepts the expanded RunOptions struct (including SandboxMode).
// Installs OS signal handlers the same way Run does, then delegates to
// RunContextWithOptions.
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
// Run is a thin signal-driven wrapper around this function. Tests call RunContext
// directly with a context they control, enabling in-process integration testing
// without signal wiring.
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

// RunContextWithOptions is the Sprint-J context-cancellable entry point.
// RunContext is a thin wrapper that builds a legacy RunOptions and calls this.
func RunContextWithOptions(ctx context.Context, opts RunOptions) error {
	debug := opts.Debug
	homePath := opts.HomePath
	configPath := opts.ConfigPath
	allowEmptyStartup := opts.AllowEmptyStartup
	allowGodMode := opts.AllowGodMode
	// Bootstrap ~/.omnipus/ directory tree on every start (idempotent, US-1).
	if err := datamodel.Init(homePath); err != nil {
		return fmt.Errorf("directory initialization failed: %w", err)
	}

	panicPath := filepath.Join(homePath, logPath, panicFile)
	panicFunc, err := logger.InitPanic(panicPath)
	if err != nil {
		return fmt.Errorf("error initializing panic log: %w", err)
	}
	defer panicFunc()

	if err = logger.EnableFileLogging(filepath.Join(homePath, logPath, logFile)); err != nil {
		panic(fmt.Errorf("error enabling file logging: %w", err))
	}
	defer logger.DisableFileLogging()

	// Construct and unlock the credential store BEFORE loading config so that
	// v0→v1 migration (MigrateWithStore) can persist legacy plaintext secrets.
	// Implements BRD SEC-22/SEC-23 deny-by-default behavior.
	cfg, bundle, credStore, err := bootCredentials(homePath, configPath)
	if err != nil {
		return err
	}

	// v0.2 #155: derive the audit-chain HMAC key from the master key and
	// install it process-wide BEFORE constructing the agent loop. The
	// agent loop's audit.NewLogger picks this up via the package-level
	// fallback when LoggerConfig.HMACKey is nil. The master key never
	// crosses the credentials package boundary — DeriveSubkey runs HKDF
	// internally and returns 32 bytes of independent key material.
	//
	// Failure here is logged and not fatal: audit.NewLogger will fall
	// back to its dev-only deterministic key with a sticky slog.Warn.
	// Operators running with audit_log=true should treat this warning as
	// a configuration bug.
	if chainKey, derrErr := credStore.DeriveSubkey(audit.AuditChainKeyInfo); derrErr == nil {
		audit.SetProcessChainKey(chainKey)
	} else {
		slog.Warn("audit: could not derive HMAC chain key from master key — audit chain will use dev-only fallback",
			"error", derrErr)
	}

	logger.SetLevelFromString(cfg.Gateway.LogLevel)

	if debug {
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
	case opts.AllowGodMode:
		godModeSource = "--allow-god-mode"
	case cfg.Sandbox.GodModeAllowed:
		godModeSource = "sandbox.god_mode_allowed"
	}
	allowGodMode = resolveAllowGodMode(allowGodMode, cfg)

	// Enforce god-mode latches: abort boot if either authorization source
	// granted god mode but the build does not support it (nogodmode tag
	// compiles GodModeAvailable=false). Fail closed: a contradictory grant
	// (authorized but unsupported) must never be silently downgraded to
	// "unavailable" — it is a misconfiguration and boot must stop.
	if allowGodMode && !sandbox.GodModeAvailable {
		return fmt.Errorf(
			"gateway: god mode unavailable in this build (compiled with nogodmode); " +
				"remove --allow-god-mode / sandbox.god_mode_allowed and restart",
		)
	}
	// Emit a persistent WARN so operators cannot claim they were not warned.
	if allowGodMode {
		fmt.Fprintf(os.Stderr,
			"WARN: gateway started with god mode available (source: %s) — agents may have sandbox disabled\n",
			godModeSource)
		slog.Warn("gateway started with god mode available — agents may have sandbox disabled",
			"source", godModeSource)
	}

	// Build the real LLM provider. The test_harness override hook + scripted-
	// scenario fallback was removed 2026-05-10; tests now run against real
	// OpenRouter via the configured provider entry.
	provider, modelID, err := createStartupProvider(cfg, allowEmptyStartup)
	if err != nil {
		return fmt.Errorf("error creating provider: %w", err)
	}

	// Only override ModelName if it was empty (first boot / migration).
	// Don't overwrite an alias (e.g. "openrouter-auto") with the raw model slug
	// (e.g. "z-ai/glm-5v-turbo") — the alias is what GetModelConfig looks up by.
	if modelID != "" && cfg.Agents.Defaults.ModelName == "" {
		cfg.Agents.Defaults.ModelName = modelID
	}

	// Seed core agents into config on first boot. Core agents are stored in
	// cfg.Agents.List with Locked=true so they appear alongside custom agents
	// in the REST API with type "core". SeedConfig is idempotent — it only adds
	// agents that are not already present (checked by ID).
	if coreagent.SeedConfig(cfg) {
		if saveErr := config.SaveConfig(configPath, cfg); saveErr != nil {
			return fmt.Errorf("gateway: failed to persist seeded core agents: %w", saveErr)
		}
	}

	msgBus := bus.NewMessageBus()
	var agentLoop *agent.AgentLoop
	agentLoop, err = agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		// B1.2(b): when the failure is an audit logger construction error and
		// the operator explicitly requested audit logging (cfg.Sandbox.AuditLog
		// = true), this is a fail-closed boot abort. CLAUDE.md "audit-everything
		// stance is non-negotiable" — silently disabling audit while it's
		// requested would be a security regression. Map to SandboxBootError so
		// cmd/omnipus exits with EX_CONFIG (78) per FR-J-004 and surfaces the
		// remediation message ("either disable `sandbox.audit_log` or fix
		// <error>") to the operator.
		var auditConstructErr *audit.LoggerConstructionError
		if errors.As(err, &auditConstructErr) && cfg.Sandbox.AuditLog {
			audit.EmitBootAbortStderr(
				"gateway.audit.construction_failed",
				"-",
				auditConstructErr.Dir,
				auditConstructErr,
				nil,
			)
			return &SandboxBootError{Err: auditConstructErr}
		}
		return fmt.Errorf("gateway: agent loop boot failed: %w", err)
	}

	// B1.2(d): wire the per-thread restrict-failure audit emitter into the
	// sandbox package now that the agent loop (and thus the audit logger) is
	// constructed. The hook bridges sandbox → audit without an import cycle —
	// sandbox only knows about the *audit.Entry type, not the agent loop.
	// SetRestrictAuditHook is idempotent so this is safe across hot reloads
	// and the test gateway helpers that re-boot in-process.
	{
		al := agentLoop
		sandbox.SetRestrictAuditHook(func(entry *audit.Entry) {
			if al == nil {
				return
			}
			logger := al.AuditLogger()
			if logger == nil {
				slog.Error("sandbox: per-thread restrict failed (audit logger disabled)",
					"event", entry.Event, "details", entry.Details)
				return
			}
			// B1.2(a): logger.Log is nil-safe; no further guard needed.
			if logErr := logger.Log(entry); logErr != nil {
				slog.Error("sandbox: restrict-failure audit write failed",
					"event", entry.Event, "error", logErr)
			}
		})
	}

	// Spec-4 FR-5.3 (M-4): GC orphaned external-CLI run directories left behind by
	// a prior process (crash / SIGKILL / power loss). Runs ONCE at boot, BEFORE any
	// new external-cli sub-agent run can be dispatched, so every run dir present is
	// safely an orphan. Non-fatal: a reaper failure must not block boot.
	{
		reapCtx, reapCancel := context.WithTimeout(ctx, 60*time.Second)
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
		agentsDir := filepath.Join(homePath, "agents")
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
			return fmt.Errorf("gateway: agent config validation failed — aborting boot (FR-062)")
		}
	}

	// Apply the kernel sandbox to the gateway process BEFORE any HTTP listener
	// binds. Strict ordering:
	//   unlock → config → NewAgentLoop → applySandbox → setupAndStartServices
	// where setupAndStartServices ends in ChannelManager.StartAll which calls
	// ListenAndServe on the shared HTTP server. During the Apply→Install→listen
	// window, external TCP probes receive ECONNREFUSED because the socket does
	// not exist yet.
	sandboxResult, sandboxErr := applySandbox(SandboxApplyOptions{
		CLIMode:  opts.SandboxMode,
		Cfg:      cfg,
		HomePath: homePath,
		Backend:  agentLoop.SandboxBackend(),
	})
	if sandboxErr != nil {
		// FR-J-004: kernel apply failure on a capable kernel is fatal.
		// Never bind the HTTP listener in this state — a half-sandboxed
		// process is worse than failing to boot. Wrapping in
		// SandboxBootError lets cmd/omnipus map this to exit code 78.
		slog.Error("gateway: sandbox apply failed — aborting boot",
			"error", sandboxErr,
			"requested_mode", opts.SandboxMode)
		return &SandboxBootError{Err: sandboxErr}
	}

	// Log the applied sandbox mode and degradation state so operators can
	// verify the runtime posture from logs without hitting the authenticated
	// /api/v1/security/sandbox-status endpoint. applySandbox is the single
	// source of truth for mode resolution (CLI > config > default); any
	// discrepancy between the applied mode and the config file is already
	// visible via result.ApplyState in /api/v1/security/sandbox-status.
	slog.Info(
		"gateway: sandbox applied",
		"applied_mode", string(sandboxResult.Mode),
		"backend", sandboxResult.BackendName,
		"landlock_enforced", sandboxResult.ApplyState.LandlockEnforced,
		"seccomp_enforced", sandboxResult.ApplyState.SeccompEnforced,
		"audit_only", sandboxResult.ApplyState.AuditOnly,
		"disabled_by", sandboxResult.DisabledBy,
	)

	// Thread the actually-applied mode into the agent loop so exec tool
	// deps use the true runtime enforcement level (not the config file value).
	agentLoop.SetAppliedSandboxMode(sandboxResult.Mode)

	fmt.Println("\n📦 Agent Status:")
	startupInfo := agentLoop.GetStartupInfo()
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
		return fmt.Errorf("gateway: inbound schema pre-compile failed: %w", compileErr)
	}

	// Arm the permissive / production-off nag banner AFTER pre-compile so that a
	// pre-compile failure does not leak the nag goroutine (StartNagBanner allocates
	// a goroutine that must be stopped via stopNag()).
	stopNag := StartNagBanner(sandboxResult.NagReason, nil)
	slog.Info("gateway: inbound schemas pre-compiled successfully")

	// M16 (FR-001/FR-002): pre-instantiate central registries.
	// BuiltinRegistry is populated here with nil deps (for name/description metadata only).
	// System.* tools are registered with nil deps; general builtins are registered as
	// metadata-only instances (deps-free, never executed — per ADR-018 D-A1).
	// After sysAgentDeps is wired (below), the registry is re-populated with live deps.
	// MCPRegistry starts empty; MCP servers populate it at connection time.
	centralBuiltinReg := tools.NewBuiltinRegistry()
	for _, t := range systools.AllTools(nil, nil) {
		if regErr := centralBuiltinReg.RegisterBuiltin(t); regErr != nil {
			slog.Warn("gateway: central builtin registry pre-population skipped duplicate",
				"tool", t.Name(), "error", regErr)
		}
	}
	// Register general-builtin metadata (SC-108 / Issue #350): these instances
	// expose Name/Description/Category for /api/v1/tools but are NEVER Execute()d.
	// Constructor errors are logged and skipped per the log-and-skip invariant.
	for _, t := range tools.GeneralBuiltinMetadata() {
		if regErr := centralBuiltinReg.RegisterBuiltin(t); regErr != nil {
			slog.Warn("gateway: central builtin registry general-builtin skipped",
				"tool", t.Name(), "error", regErr)
		}
	}
	// Register browser.* metadata (Issue #350 / catalog gap): browser tools register
	// into the per-agent registry at agent-loop boot, so without this they were absent
	// from GET /api/v1/tools. These metadata-only instances (nil *BrowserManager) are
	// never Execute()d — they expose Name/Description/Category only (ADR-018 D-A1).
	for _, t := range browser.BrowserBuiltinMetadata() {
		if regErr := centralBuiltinReg.RegisterBuiltin(t); regErr != nil {
			slog.Warn("gateway: central builtin registry browser-builtin skipped",
				"tool", t.Name(), "error", regErr)
		}
	}
	centralMCPReg := tools.NewMCPRegistry()

	runningServices, err := setupAndStartServices(
		cfg,
		bundle,
		agentLoop,
		msgBus,
		homePath,
		credStore,
		sandboxResult,
		centralBuiltinReg,
		centralMCPReg,
		allowGodMode,
	)
	if err != nil {
		stopNag() // don't leak the nag goroutine if service setup fails.
		return err
	}
	runningServices.stopNagBanner = stopNag

	// Surface sandbox state on /health via the existing degraded/check
	// infrastructure. Registering a RegisterCheck puts the {mode, backend,
	// applied} triplet into the /health response body.
	if runningServices.HealthServer != nil && sandboxResult != nil {
		registerSandboxHealthCheck(runningServices.HealthServer, sandboxResult)
	}

	// The /reload trigger + manualReloadChan were wired inside
	// setupAndStartServices BEFORE the listener went live (avoids the
	// boot-ordering race where /reload could 503 "reload not configured").
	// Reuse them here for the reload consumer loop, the agent loop, and the
	// sysagent ReloadFunc — do NOT re-create them.
	manualReloadChan := runningServices.manualReloadChan
	reloadTrigger := runningServices.reloadTrigger
	agentLoop.SetReloadFunc(reloadTrigger)

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
			wd = homePath
		}
		skillsBuiltinDir = filepath.Join(wd, "skills")
	}
	skillsWorkspace := cfg.WorkspacePath()
	skillsGlobalDir := filepath.Join(homePath, "skills")

	// First-boot seed of the embedded default skill set (Spec-6 U3, FR-9.3).
	// SeedDefaults is idempotent: it only fills in skills that are missing from
	// the global skills dir and never overwrites existing (possibly user-edited)
	// skills. A fresh install therefore ships with summarize, skill-authoring,
	// plan, and daily-briefing without any external files.
	if seedRes, seedErr := skills.SeedDefaults(skillsGlobalDir); seedErr != nil {
		slog.Warn("gateway: failed to seed default skills from embed", "error", seedErr)
	} else if len(seedRes.Seeded) > 0 {
		slog.Info("gateway: seeded default skills from embed",
			"seeded", seedRes.Seeded, "skipped", seedRes.Skipped)
	}

	sysSkillsLoader := skills.NewSkillsLoader(skillsWorkspace, skillsGlobalDir, skillsBuiltinDir)
	// SkillWriter authors/versions skills into the global skills dir so editing a
	// built-in produces a user override rather than mutating the shipped built-in.
	sysSkillWriter := skills.NewSkillWriter(skillsGlobalDir)

	// RegistryManager: fans out search/install to all configured marketplaces
	// (FR-10.1 unified list). The SSRF checker (nil when SSRF is disabled) is
	// injected into the ClawHub HTTP client so outbound registry traffic honors
	// the SSRF policy (SEC-24).
	ssrfChecker := agent.GetSSRFChecker(agentLoop)
	var ssrfClient *http.Client
	if ssrfChecker != nil {
		ssrfClient = ssrfChecker.SafeClient()
	}
	regCfg := skills.RegistryConfig{
		Marketplaces:          skills.MarketplacesFromConfig(cfg, bundle.GetString, ssrfClient, skillsWorkspace),
		MaxConcurrentSearches: cfg.Tools.Skills.MaxConcurrentSearches,
	}
	sysRegistryManager := skills.NewRegistryManagerFromConfig(regCfg)

	// SkillInstaller: downloads and installs skills into the operator workspace.
	// GitHub token/proxy from the first github marketplace entry (optional;
	// empty → unauthenticated API calls).
	githubToken, githubProxy := skills.FirstGitHubMarketplaceCreds(cfg, bundle.GetString)
	sysSkillInstaller, err := skills.NewSkillInstallerWithSSRF(
		skillsWorkspace, githubToken, githubProxy, ssrfChecker,
	)
	if err != nil {
		slog.Warn("gateway: could not create skill installer; system.skill.install unavailable",
			"error", err)
		sysSkillInstaller = nil
	}

	sysAgentDeps := &systools.Deps{
		Home:         homePath,
		ConfigPath:   configPath,
		GetCfg:       agentLoop.GetConfig,
		MutateConfig: agentLoop.MutateConfig,
		SaveConfigLocked: func(c *config.Config) error {
			return config.SaveConfig(configPath, c)
		},
		CredStore:       credStore,
		ReloadFunc:      reloadTrigger,
		SkillsLoader:    sysSkillsLoader,
		RegistryManager: sysRegistryManager,
		SkillInstaller:  sysSkillInstaller,
		SkillWriter:     sysSkillWriter,
		// §4 behavioral-parity gap: the cross-workspace task tools
		// (create/update/delete_task_in_workspace) must enforce the SAME FR-6.2
		// delegation policy the same-workspace create_task/update_task tools
		// enforce. The resolver loads the calling agent's config from the live
		// config and builds the task-mode gate dynamically (the sysagent tools are
		// registered once on a central registry, so a per-agent checker can't be
		// bound at construction). MUST be wired in production — leaving it nil
		// fails OPEN.
		DelegationDeny: agentLoop.NewSysagentDelegationDeny(),
		ListSessions:   agentLoop.ListAllSessions,
	}
	agentLoop.WireSysagentDeps(sysAgentDeps)

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
	centralBuiltinReg = tools.NewBuiltinRegistry()
	for _, t := range systools.AllTools(sysAgentDeps, nil) {
		if err := centralBuiltinReg.RegisterBuiltin(t); err != nil {
			slog.Warn("gateway: central builtin registry re-population skipped duplicate",
				"tool", t.Name(), "error", err)
		}
	}
	// Re-register general-builtin metadata in the live-deps registry (metadata-only,
	// never executed; duplicates skipped). These instances expose correct
	// Name/Description/Category for /api/v1/tools without executing anything.
	generalBuiltinsRegistered := 0
	for _, t := range tools.GeneralBuiltinMetadata() {
		if err := centralBuiltinReg.RegisterBuiltin(t); err != nil {
			slog.Warn("gateway: central builtin registry general-builtin re-population skipped",
				"tool", t.Name(), "error", err)
		} else {
			generalBuiltinsRegistered++
		}
	}
	// Re-register browser.* metadata (metadata-only, never executed; duplicates
	// skipped) — same catalog-gap fix as the pre-deps block above.
	browserBuiltinsRegistered := 0
	for _, t := range browser.BrowserBuiltinMetadata() {
		if err := centralBuiltinReg.RegisterBuiltin(t); err != nil {
			slog.Warn("gateway: central builtin registry browser-builtin re-population skipped",
				"tool", t.Name(), "error", err)
		} else {
			browserBuiltinsRegistered++
		}
	}
	// Propagate the updated registry to the already-constructed restAPI (SC-108 fix).
	if runningServices.restAPIRef != nil {
		runningServices.restAPIRef.builtinRegistry = centralBuiltinReg
	}
	slog.Info("gateway: central BuiltinRegistry re-populated with live deps",
		"system_tools", centralBuiltinReg.Count()-generalBuiltinsRegistered-browserBuiltinsRegistered,
		"general_builtins", generalBuiltinsRegistered,
		"browser_builtins", browserBuiltinsRegistered,
		"total", centralBuiltinReg.Count())

	fmt.Printf("✓ Gateway started on %s:%d\n", cfg.Gateway.Host, cfg.Gateway.Port)
	fmt.Println("Press Ctrl+C to stop")

	// agentLoopCtx is canceled if the agent loop exits unexpectedly (e.g. panic
	// recovery). The outer select below treats this the same as ctx cancellation.
	agentLoopCtx, agentLoopCancel := context.WithCancel(ctx)
	defer agentLoopCancel()

	// agentLoopDead is set when the agent loop exits (normally or via panic).
	// The /health endpoint returns 503 when this flag is set, signaling to
	// load-balancers and monitors that the gateway is no longer functional.
	var agentLoopDead atomic.Bool

	go func() {
		defer agentLoopCancel()
		defer func() {
			if r := recover(); r != nil {
				stack := runtimedebug.Stack()
				slog.Error("agent loop panicked — gateway is now non-functional",
					"panic", r, "stack", string(stack))
				// Append to the panic log file so ops can find the crash.
				if f, openErr := os.OpenFile(panicPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600); openErr == nil {
					fmt.Fprintf(f, "\n\nagent loop panic: %v\n%s\n", r, stack)
					f.Close()
				}
				agentLoopDead.Store(true)
			}
		}()
		agentLoop.Run(agentLoopCtx)
	}()

	// Launch the nightly retention sweep goroutine. Uses ctx (not agentLoopCtx)
	// so it shuts down on gateway stop regardless of agent-loop liveness.
	// GetSessionStore returns the shared UnifiedStore; when nil (misconfigured
	// home) the goroutine is a no-op — getCfg returning a nil cfg is guarded
	// inside executeSweepTick.
	if sharedStore := agentLoop.GetSessionStore(); sharedStore != nil {
		startRetentionSweepLoop(ctx, sharedStore, agentLoop.GetConfig, 24*time.Hour)
	}
	// Tool-result file sweep runs alongside the transcript sweep on the same
	// retention window. setupAndStartServices already constructed the store.
	if runningServices.toolStore != nil {
		retentionToolResultSweepFn = runningServices.toolStore.retentionSweep
	}

	// FR-031: Launch the nightly retro sweep goroutine alongside the session sweep.
	// Iterates all agents and calls SweepRetros per MemoryStore.
	startRetentionRetroSweepLoop(ctx, agentLoop, agentLoop.GetConfig, 24*time.Hour)

	// FR-032/FR-032a: Bootstrap recap pass — on gateway start, re-cap sessions
	// that lack LAST_SESSION.md. Runs once in a goroutine.
	go agentLoop.BootstrapRecapPass(ctx)

	// Wire a second degraded check: report 503 when the agent loop has died.
	runningServices.HealthServer.SetDegradedFunc(func() (bool, string) {
		if agentLoopDead.Load() {
			return true, "agent loop exited unexpectedly — gateway requires restart"
		}
		runningServices.reloadMu.Lock()
		defer runningServices.reloadMu.Unlock()
		if runningServices.reloadDegraded {
			return true, fmt.Sprintf("config reload failed: %v", runningServices.reloadError)
		}
		return false, ""
	})

	// B1.2(f): /health surfaces audit-degraded mode. The closure reports
	// whether the agent loop currently has a non-nil audit logger so /health
	// can flag "audit_logger: unavailable" without exposing the pointer.
	runningServices.HealthServer.SetAuditLoggerAvailableFunc(func() bool {
		return agentLoop.AuditLogger() != nil
	})
	// audit_degraded should only fire when the operator asked for audit AND
	// it isn't working. cfg.Sandbox.AuditLog=false is a deliberate off-state.
	runningServices.HealthServer.SetAuditLoggerConfiguredFunc(func() bool {
		return agentLoop.GetConfig().Sandbox.AuditLog
	})

	var configReloadChan <-chan *config.Config
	stopWatch := func() {}
	if cfg.Gateway.HotReload {
		configReloadChan, stopWatch = setupConfigWatcherPolling(
			configPath,
			debug,
			credStore,
			runningServices.selfWriteReg,
		)
		logger.Info("Config hot reload enabled")
	}
	defer stopWatch()

	for {
		select {
		case <-agentLoopCtx.Done():
			logger.Info("Shutting down...")
			omnipusGracefulShutdown(runningServices, agentLoop, provider, cfg)
			return nil
		case newCfg := <-configReloadChan:
			if !runningServices.reloading.CompareAndSwap(false, true) {
				logger.Warn("Config reload skipped: another reload is in progress")
				continue
			}
			err := executeReload(ctx, agentLoop, newCfg, &provider, runningServices, msgBus, allowEmptyStartup)
			if err != nil {
				logger.Errorf("Config reload failed: %v", err)
			}
		case <-manualReloadChan:
			logger.Info("Manual reload triggered via /reload endpoint")
			// LoadConfigWithStoreAndSelfHealHook (not LoadConfigWithStore): this
			// path bypasses safeUpdateConfigJSON's configMu + selfWriteReg
			// registration, so if the single-user-model role self-heal writes
			// config.json here, the write must be registered manually or the
			// watcher's next tick would misidentify it as an external edit.
			newCfg, err := config.LoadConfigWithStoreAndSelfHealHook(
				configPath, credStore, selfHealWriteHook(runningServices.selfWriteReg),
			)
			if err != nil {
				logger.Errorf("Error loading config for manual reload: %v", err)
				agentLoop.ClearReloadPending()
				runningServices.reloading.Store(false)
				continue
			}
			if err = newCfg.ValidateProviders(); err != nil {
				logger.Errorf("Config validation failed: %v", err)
				agentLoop.ClearReloadPending()
				runningServices.reloading.Store(false)
				continue
			}
			err = executeReload(ctx, agentLoop, newCfg, &provider, runningServices, msgBus, allowEmptyStartup)
			if err != nil {
				logger.Errorf("Manual reload failed: %v", err)
			} else {
				logger.Info("Manual reload completed successfully")
			}
		}
	}
}

// servicesSnapshot captures all fields that restartServices and executeReload
// mutate, so they can be atomically restored on reload failure.
type servicesSnapshot struct {
	bundle         credentials.SecretBundle
	ChannelManager *channels.Manager
	CronService    *cron.CronService
	TaskTrigger    *agent.TaskTriggerScheduler
	MediaStore     media.MediaStore
	DeviceService  *devices.Service
}

func snapshotServices(svc *services) servicesSnapshot {
	return servicesSnapshot{
		bundle:         svc.bundle,
		ChannelManager: svc.ChannelManager,
		CronService:    svc.CronService,
		TaskTrigger:    svc.TaskTrigger,
		MediaStore:     svc.MediaStore,
		DeviceService:  svc.DeviceService,
	}
}

func restoreServices(svc *services, snap servicesSnapshot) {
	svc.bundle = snap.bundle
	svc.ChannelManager = snap.ChannelManager
	svc.CronService = snap.CronService
	svc.TaskTrigger = snap.TaskTrigger
	svc.MediaStore = snap.MediaStore
	svc.DeviceService = snap.DeviceService
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
	// Defers run LIFO: ClearReloadPending fires first (unblocks triggerReloadAndWait pollers),
	// then reloading fires (allows the next CAS to succeed). A concurrent TriggerReload
	// that races between these two defers gets ErrReloadAlreadyInProgress and polls — safe
	// because triggerReloadAndWait polls through ErrReloadAlreadyInProgress so the pending
	// flag is never cleared prematurely.
	defer runningServices.reloading.Store(false)
	defer agentLoop.ClearReloadPending()

	// Snapshot all service fields that restartServices mutates so they can be
	// restored atomically if the reload fails. bundle and ChannelManager are
	// mutated here in executeReload itself; the rest are mutated in
	// restartServices (CronService, TaskTrigger, MediaStore, DeviceService).
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

	// Re-inject provider credentials for the new config so LLM SDK clients
	// receive their secrets. If injection fails, reject the reload.
	if cs := runningServices.credStore; cs != nil {
		if errs := credentials.InjectFromConfig(newCfg, cs); len(errs) > 0 {
			for _, e := range errs {
				slog.Error("reload: provider credential injection failed — rejecting reload", "error", e)
			}
			reloadErr := fmt.Errorf("reload rejected: provider credential injection failed")
			markDegraded(reloadErr)
			return reloadErr
		}

		// Re-resolve the SecretBundle for channels (no os.Setenv for channel creds).
		newBundle, bundleErrs := credentials.ResolveBundle(newCfg, cs)
		for _, e := range bundleErrs {
			var notFound *credentials.NotFoundError
			if errors.As(e, &notFound) {
				slog.Info("reload: channel credential not found (channel may be disabled)", "error", e)
				continue
			}
			slog.Warn("reload: credential bundle resolution error", "error", e)
		}
		runningServices.bundle = newBundle

		// Re-register resolved plaintexts so the scrubber stays current after reload.
		reloadValues := make([]string, 0, len(newBundle))
		for _, v := range newBundle {
			if v != "" {
				reloadValues = append(reloadValues, v)
			}
		}
		if len(reloadValues) > 0 {
			newCfg.RegisterSensitiveValues(reloadValues)
		}
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

func createStartupProvider(
	cfg *config.Config,
	allowEmptyStartup bool,
) (providers.LLMProvider, string, error) {
	modelName := cfg.Agents.Defaults.GetModelName()
	if modelName == "" && allowEmptyStartup {
		reason := "no default model configured; gateway started in limited mode"
		fmt.Printf("⚠ Warning: %s\n", reason)
		logger.WarnCF("gateway", "Gateway started without default model", map[string]any{
			"limited_mode": true,
		})
		return &startupBlockedProvider{reason: reason}, "", nil
	}

	return providers.CreateProvider(cfg)
}

func setupAndStartServices(
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
) (*services, error) {
	runningServices := &services{credStore: credStore, bundle: bundle, sandboxResult: sandboxResult, homePath: homePath}

	// Per-user notification store (#264). Backs schedule-failure notifications and
	// the header notification center.
	runningServices.notifStore = notifications.NewStore(filepath.Join(homePath, "notifications"))

	var err error
	runningServices.CronService, err = setupCronTool(
		agentLoop,
		msgBus,
		cfg.WorkspacePath(),
		cfg,
		runningServices.notifStore,
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up cron service: %w", err)
	}
	if err = runningServices.CronService.Start(); err != nil {
		return nil, fmt.Errorf("error starting cron service: %w", err)
	}
	fmt.Println("✓ Cron service started")

	// ADR-027 — heartbeat is workspace-scoped. Reconcile workspace member_configs
	// into the cron engine: every (workspace, agent) pair with heartbeat.enabled=true
	// gets a recurring job. Best-effort: a reconcile failure is logged but does not
	// abort boot — the next hot-path write (workspace PUT) will re-converge.
	{
		wsFiles, wsErr := listWorkspaceFiles(homePath)
		if wsErr != nil {
			slog.Warn("gateway: heartbeat schedule reconcile: list workspaces failed", "error", wsErr)
		} else if hbErr := ReconcileHeartbeatSchedules(
			runningServices.CronService,
			wsFiles,
			configOnlyIsWorker(cfg),
		); hbErr != nil {
			slog.Warn("gateway: heartbeat schedule reconcile failed", "error", hbErr)
		}
	}
	fmt.Println("✓ Heartbeat running as workspace-scoped schedules")

	// Queued-task draining (dispatch of `next` tasks) is owned UNCONDITIONALLY by
	// the dedicated TaskDrainService — never by the heartbeat path.
	if te := agent.GetTaskExecutor(agentLoop); te != nil {
		runningServices.TaskDrain = heartbeat.NewTaskDrainService(te, 0)
		runningServices.TaskDrain.Start()
		fmt.Println("✓ Queued-task drain owned by: TaskDrainService (dedicated poll)")
	} else {
		fmt.Println("⚠ Queued-task drain disabled: no task executor available")
	}

	// Mailbox drain (M11): unhandled inbound mail → Board tasks. The provider is
	// rebuilt from live config + the credential store on every tick, so adding,
	// changing, or removing a mailbox via the Connectors API is picked up without
	// a restart. Started unconditionally; the scanner is a no-op when no mailbox
	// is configured.
	if tStore := agent.GetTaskStore(agentLoop); tStore != nil {
		provider := email.MailboxProviderFunc(func() []email.Mailbox {
			return buildMailboxes(agentLoop.GetConfig(), credStore)
		})
		drainer := email.NewDrainer(tStore, provider, 0)
		runningServices.MailboxDrain = heartbeat.NewMailboxDrainService(drainer, 0)
		runningServices.MailboxDrain.Start()
		fmt.Println("✓ Mailbox drain owned by: MailboxDrainService (unhandled mail → Board)")
	}

	// Task time-trigger executor: fires once/every/recurring task triggers via a
	// dedicated CronService instance (reusing the pkg/cron engine, NOT a second
	// scheduler). Boot-reconciles existing tasks' triggers, then the create/
	// update/delete REST + tool paths (re)register/remove jobs via
	// AgentLoop.NotifyTaskUpserted / NotifyTaskDeleted.
	if tStore := agent.GetTaskStore(agentLoop); tStore != nil {
		triggerStorePath := filepath.Join(homePath, "tasks_triggers", "jobs.json")
		runningServices.TaskTrigger = agent.NewTaskTriggerScheduler(
			triggerStorePath, tStore, agent.GetTaskExecutor(agentLoop),
		)
		if startErr := runningServices.TaskTrigger.Start(); startErr != nil {
			return nil, fmt.Errorf("error starting task trigger scheduler: %w", startErr)
		}
		agentLoop.SetTaskTriggerScheduler(runningServices.TaskTrigger)
		if recErr := runningServices.TaskTrigger.Reconcile(); recErr != nil {
			slog.Error("gateway: task trigger boot reconcile failed", "error", recErr)
		}
		fmt.Println("✓ Task trigger scheduler started")
	}

	runningServices.MediaStore = media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{
		Enabled:  cfg.Tools.MediaCleanup.Enabled,
		MaxAge:   time.Duration(cfg.Tools.MediaCleanup.MaxAge) * time.Minute,
		Interval: time.Duration(cfg.Tools.MediaCleanup.Interval) * time.Minute,
	})
	if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
		// Reload refs persisted by a previous gateway instance so
		// /api/v1/media/<ref> URLs in old session transcripts still resolve.
		// Best-effort — a load failure should not block boot.
		if loadErr := fms.LoadRegistry(); loadErr != nil {
			slog.Warn("media: failed to load persisted registry", "error", loadErr)
		}
		fms.Start()
	}

	runningServices.ChannelManager, err = channels.NewManager(
		cfg,
		runningServices.bundle,
		msgBus,
		runningServices.MediaStore,
	)
	if err != nil {
		if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
			fms.Stop()
		}
		return nil, fmt.Errorf("error creating channel manager: %w", err)
	}

	agentLoop.SetChannelManager(runningServices.ChannelManager)
	agentLoop.SetMediaStore(runningServices.MediaStore)
	// Wire all observer callbacks (CancelInterceptor, PairingObserver, …) via
	// the shared helper so this path stays in sync with restartServices.
	// Must happen after SetChannelManager so the Manager's channels map is
	// already populated when SetCancelInterceptor is called.
	wireChannelManager(runningServices.ChannelManager, agentLoop)

	if transcriber := voice.DetectTranscriber(cfg, runningServices.bundle); transcriber != nil {
		agentLoop.SetTranscriber(transcriber)
		logger.InfoCF("voice", "Transcription enabled (agent-level)", map[string]any{"provider": transcriber.Name()})
	}

	enabledChannels := runningServices.ChannelManager.GetEnabledChannels()
	if len(enabledChannels) > 0 {
		fmt.Printf("✓ Channels enabled: %s\n", enabledChannels)
	} else {
		fmt.Println("⚠ Warning: No channels enabled")
	}

	// Validate preview config and apply computed defaults (FR-001..FR-005, FR-027, FR-028).
	// ValidateAndApplyPreviewDefaults mutates cfg.Gateway in-place.
	if err = cfg.Gateway.ValidateAndApplyPreviewDefaults(); err != nil {
		return nil, fmt.Errorf("gateway config: %w", err)
	}
	// Apply warmup timeout default (FR-013 / CR-04).
	cfg.Tools.ApplyWarmupTimeoutDefault()

	addr := fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port)
	runningServices.HealthServer = health.NewServer(cfg.Gateway.Host, cfg.Gateway.Port)
	runningServices.ChannelManager.SetupHTTPServer(addr, runningServices.HealthServer)

	// Compute the main gateway origin for CORS and CSP frame-ancestors.
	// Use PublicURL when set (reverse-proxy deployment); otherwise derive from host:port.
	// When host is a wildcard (0.0.0.0, ::), allowedOrigin is empty and the WARN
	// is emitted below (FR-007e / MR-03).
	allowedOrigin := middleware.CanonicalGatewayOrigin(cfg)
	if allowedOrigin == "" {
		// Wildcard bind host and no public_url → frame-ancestors must fall back to *.
		// Log once at WARN so operators know to set gateway.public_url for strict control.
		//
		// NOTE: this WARN is emitted at boot only and is NOT re-evaluated on
		// hot-reload of gateway.public_url. Operators changing the field at
		// runtime must restart the gateway for the WARN to re-fire on the new
		// value and for the new origin to take effect in CSP headers.
		slog.Warn("frame-ancestors fallback to '*' — set gateway.public_url for strict embedding control",
			"host", cfg.Gateway.Host)
	}

	// Set up the preview listener when enabled (FR-001, FR-020).
	previewListenerEnabled := cfg.Gateway.IsPreviewListenerEnabled()
	var gatewayPreviewBaseURL string
	if previewListenerEnabled {
		previewHost := cfg.Gateway.PreviewHost
		previewPort := int(cfg.Gateway.PreviewPort)
		previewAddr := fmt.Sprintf("%s:%d", previewHost, previewPort)
		runningServices.ChannelManager.SetupPreviewServer(previewAddr)
		// Warn when the preview listener is bound on a wildcard address but
		// gateway.preview_origin is not configured. In that case the computed
		// preview URL will contain "0.0.0.0" or "::" which is unreachable from
		// a browser (R3 in the per-agent sandbox plan).
		if shouldWarnPreviewOrigin(previewHost, cfg.Gateway.PreviewOrigin) {
			slog.Warn("gateway listening on wildcard with empty gateway.preview_origin — " +
				"preview URLs will use 0.0.0.0 and be unreachable from a browser. " +
				"Set gateway.preview_origin to your public hostname or IP " +
				"(e.g. https://your.host:6061).")
		}
		// Compute the preview base URL for tool result generation.
		if cfg.Gateway.PreviewOrigin != "" {
			gatewayPreviewBaseURL = cfg.Gateway.PreviewOrigin
		} else {
			scheme := "http"
			if cfg.Gateway.PublicURL != "" {
				// Inherit the scheme from PublicURL when preview_origin is not set.
				if len(cfg.Gateway.PublicURL) >= 5 && cfg.Gateway.PublicURL[:5] == "https" {
					scheme = "https"
				}
			}
			gatewayPreviewBaseURL = fmt.Sprintf(
				"%s://%s",
				scheme,
				net.JoinHostPort(previewHost, strconv.Itoa(previewPort)),
			)
		}
	}
	// Fix-5: warn when workspace.shell is enabled on a non-Linux host where the
	// kernel sandbox (Landlock + seccomp) is unavailable. Single-shot at boot.
	if runtime.GOOS != "linux" {
		wsEnabled := cfg.Sandbox.Experimental.WorkspaceShellEnabled != nil &&
			*cfg.Sandbox.Experimental.WorkspaceShellEnabled
		if !wsEnabled {
			// Also check per-agent overrides — future-proofing when per-agent flags land.
			for _, ag := range cfg.Agents.List {
				_ = ag // per-agent workspace_shell_enabled not yet per-config; global only
			}
		}
		if wsEnabled {
			fmt.Fprintf(
				os.Stderr,
				"WARN: kernel sandbox unavailable on %s; workspace.shell runs with application-level path checks only — do not enable on multi-tenant systems\n",
				runtime.GOOS,
			)
		}
	}

	// Fix-6: warn when any agent with remote channels has a non-deny exec policy.
	// The GHSA-pv8c-p6jf-3fpp channel block was removed; operators must now
	// configure per-agent ToolPolicyCfg to restrict exec.
	emitGHSARemovalWarn(cfg)

	// Construct the web_serve static-mode (Tier 1) and dev-mode (Tier 3)
	// shared registries. These are created regardless of previewListenerEnabled so
	// that operators who run on the single-port fallback still get static-mode serving.
	// Dev mode requires the DevServerRegistry; the tool itself gates to Linux.
	servedSubdirs := agent.NewServedSubdirs()
	devServers := sandbox.NewDevServerRegistry()
	runningServices.servedSubdirs = servedSubdirs
	runningServices.devServers = devServers

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
		al := agentLoop // captured by reference — may be wired up by reload
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

	var egressProxy *sandbox.EgressProxy
	if egressProx, epErr := sandbox.NewEgressProxy(cfg.Sandbox.EgressAllowList, egressAuditFn); epErr != nil {
		slog.Warn("gateway: egress proxy failed to start; web_serve dev mode will run without egress enforcement",
			"error", epErr)
	} else {
		egressProxy = egressProx
		runningServices.egressProxy = egressProxy
	}

	// Build and wire Tier13Deps into every agent via the agent loop.
	tier13 := agent.Tier13Deps{
		ServedSubdirs:         servedSubdirs,
		DevServerRegistry:     devServers,
		EgressProxy:           egressProxy,
		GatewayPreviewBaseURL: gatewayPreviewBaseURL,
	}
	agentLoop.WireTier13Deps(tier13)

	// SSE chat endpoint — kept for backward compatibility; streaming tokens now route through WebSocket.
	sseHandler := newSSEHandler(msgBus, nil, allowedOrigin, func() *config.Config { return cfg })
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/chat", sseHandler)

	// WebSocket chat endpoint — primary transport for bi-directional chat streaming.
	wsHandler := newWSHandler(msgBus, agentLoop, allowedOrigin)
	wsHandler.home = homePath
	toolStore := newToolResultStore(homePath)
	wsHandler.toolStore = toolStore
	runningServices.toolStore = toolStore
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/chat/ws", wsHandler)
	// Register WebSocket handler as stream fallback so streaming tokens route back for webchat.
	runningServices.ChannelManager.SetStreamFallback(wsHandler)
	// Register webchat as a channel so outbound messages (non-streaming) also route back.
	// The webchatChannel and wsHandler share a reference so streaming can suppress duplicate Send().
	wch := newWebchatChannel(wsHandler)
	wsHandler.webchatCh = wch
	runningServices.ChannelManager.RegisterChannel("webchat", wch)

	// Build the in-process tool-approval registry (FR-016, FR-070, M10).
	// policy.ValidateSaturationCap enforces FR-016 semantics:
	//   cap < 0 → fatal (emit HIGH audit + abort)
	//   cap == 0 → unlimited (emit WARN audit, ShouldSaturate always false)
	//   cap > 0 → use as-is
	approvalMaxPending := cfg.Gateway.ToolApprovalMaxPending
	effectiveCap, capOK := policy.ValidateSaturationCap(context.Background(), nil, approvalMaxPending)
	if !capOK {
		return nil, fmt.Errorf(
			"gateway: invalid tool_approval_max_pending=%d — boot aborted (FR-016)",
			approvalMaxPending,
		)
	}
	approvalTimeout := cfg.Gateway.ToolApprovalTimeout
	var approvalTimeoutDur time.Duration
	if approvalTimeout > 0 {
		approvalTimeoutDur = time.Duration(approvalTimeout) * time.Second
	} else {
		approvalTimeoutDur = 300 * time.Second
	}
	approvalReg := newApprovalRegistryV2(effectiveCap, approvalTimeoutDur)
	wsHandler.approvalRegV2 = approvalReg

	// Wire the policy approver into the agent loop (FR-011, C3).
	// The adapter bridges agent.PolicyApprover → approvalRegistryV2 + WSHandler.
	agentLoop.SetToolApprover(newPolicyApproverAdapter(approvalReg, wsHandler))

	// Wire the filter-metrics recorder into pkg/tools so FilterToolsByPolicy
	// can emit FR-039 omnipus_tool_filter_total counters. (C4)
	tools.SetToolMetricsRecorder(globalToolMetrics)

	// REST API endpoints for frontend data.
	onboardingMgr := onboarding.NewManager(homePath)
	tStore := agent.GetTaskStore(agentLoop)
	tExecutor := agent.GetTaskExecutor(agentLoop)

	// Wire god-mode opt-in into the agent loop for runtime coercion.
	agentLoop.SetAllowGodMode(allowGodMode)

	// selfWriteReg is shared between safeUpdateConfigJSON (registers hashes of
	// app-initiated writes) and setupConfigWatcherPolling (suppresses reload for
	// those writes). Created here so both can reference the same instance.
	selfWriteReg := &configSelfWriteRegistry{
		hashes: make(map[[32]byte]struct{}),
	}
	runningServices.selfWriteReg = selfWriteReg

	// ClawHub marketplace registry backing GET /api/v1/skills/search and
	// install-by-slug. Built from the unified Marketplaces list (FR-10.1) with
	// the SSRF-safe HTTP client (SEC-24) so outbound registry traffic honors
	// the SSRF policy. The client defaults BaseURL to https://clawhub.ai when
	// unset. Auth token (optional) is resolved from the credential bundle.
	var restSSRFClient *http.Client
	if restSSRF := agent.GetSSRFChecker(agentLoop); restSSRF != nil {
		restSSRFClient = restSSRF.SafeClient()
	}
	var skillRegistry skills.SkillRegistry
	if chEntry, ok := skills.ClawHubMarketplaceFromConfig(cfg, bundle.GetString, restSSRFClient); ok {
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

	api := &restAPI{
		agentLoop:       agentLoop,
		allowedOrigin:   allowedOrigin,
		onboardingMgr:   onboardingMgr,
		homePath:        homePath,
		taskStore:       tStore,
		taskExecutor:    tExecutor,
		credStore:       credStore,
		mediaStore:      runningServices.MediaStore,
		ssrfChecker:     agent.GetSSRFChecker(agentLoop), // SEC-24: nil when SSRF disabled
		sandboxResult:   sandboxResult,                   // immutable post-boot snapshot
		appliedConfig:   mustDeepCopyConfig(cfg),         // boot-time snapshot for pending-restart diff
		servedSubdirs:   runningServices.servedSubdirs,   // web_serve static-mode token registry
		devServers:      runningServices.devServers,      // web_serve dev-mode process registry
		approvalReg:     approvalReg,                     // in-process tool-approval registry (FR-016)
		builtinRegistry: builtinReg,                      // M16: central builtin registry (FR-001)
		mcpRegistry:     mcpReg,                          // M16: central MCP registry (FR-001)
		skillRegistry:   skillRegistry,                   // ClawHub marketplace (search + install-by-slug)
		allowGodMode:    allowGodMode,                    // god-mode latch (2)
		notifStore:      runningServices.notifStore,      // #264: notification center
		auditor:         agentLoop.AuditLogger(),         // shared audit logger for REST mutations
		selfWriteReg:    selfWriteReg,                    // suppress watcher reload on app-initiated writes
		taskLock:        task.TaskFileLock,               // shared striped lock for board task RMW
	}
	api.cronService.Store(runningServices.CronService) // #264: schedules CRUD (atomic.Pointer)
	// Stash the api ref so RunContextWithOptions can update builtinRegistry
	// after the M16 live-deps re-population (which creates a fresh *BuiltinRegistry
	// that would otherwise not reach the already-constructed api).
	runningServices.restAPIRef = api
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/sessions", api.withAuth(api.HandleSessions))
	// /api/v1/sessions/ handles: sessions CRUD AND the tool-results sub-resource
	// GET /api/v1/sessions/{session_id}/tool-results/{ref} (dispatched inside HandleSessions).
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/sessions/", api.withAuth(api.HandleSessions))
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/agents", api.withAuth(api.HandleAgents))
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/agents/", api.withAuth(api.HandleAgents))
	runningServices.ChannelManager.RegisterHTTPHandler(
		"/api/v1/config",
		api.withAuth(withRateLimit(configLimiter, api.HandleConfig)),
	)
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/skills", api.withAuth(api.HandleSkills))
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/skills/", api.withAuth(api.HandleSkills))
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/commands", api.withAuth(api.HandleListCommands))
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/doctor", api.withAuth(api.HandleDoctor))

	// Ensure the default workspace exists (FR-1.6). Best-effort: a failure
	// is logged but does not abort gateway startup.
	// ownerUsername is taken from the first configured user (empty on fresh install — that is fine).
	ownerUsername := ""
	if len(cfg.Gateway.Users) > 0 {
		ownerUsername = cfg.Gateway.Users[0].Username
	}
	if wsErr := ensureDefaultWorkspace(homePath, ownerUsername, cfg); wsErr != nil {
		slog.Error("gateway: default workspace auto-creation failed", "error", wsErr)
	}

	// Recover tasks left "in_progress" by a crashed/abandoned previous process.
	// Runs before the HTTP listener accepts connections (StartAll, below), so no
	// handler can race reconciliation.
	api.reconcileStuckTasks()

	// Drop blocked_by edges pointing at task files that no longer exist, so the
	// dependency graph self-heals on boot (a waiting task gated only on an orphan
	// would otherwise never advance). Same pre-listener safety window as above.
	api.reconcileOrphanBlockedByEdges()

	// Register additional endpoints for frontend features.
	// These return proper JSON responses instead of letting the SPA catch-all
	// serve HTML (which causes "Unexpected token '<'" JSON parse errors).
	api.registerAdditionalEndpoints(runningServices.ChannelManager)

	// Register /preview/ (canonical web_serve URL) plus back-compat /serve/ and /dev/
	// shims on the preview mux (FR-005, FR-006). These paths are intentionally absent
	// from the main mux — any hit on <main_host>:<port>/preview/... returns 404 from
	// the catch-all handler. The preview mux has no auth middleware: the URL path
	// token is the credential (FR-023). All handlers live in rest_preview.go.
	if previewListenerEnabled {
		api.registerPreviewEndpoints(runningServices.ChannelManager)
	}

	// Catch-all for any /api/ path not registered — returns JSON 404 instead of SPA HTML.
	// Do not echo r.URL.Path in the response; that leaks internal routing details.
	runningServices.ChannelManager.RegisterHTTPHandler(
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
	if spaHandler := newSPAHandler(); spaHandler != nil {
		runningServices.ChannelManager.RegisterHTTPHandler("/", spaHandler)
	} else {
		fmt.Println("Note: No embedded SPA (run 'pnpm build' in web/frontend to enable UI)")
	}

	// Wrap the HTTP server handler with config snapshot middleware so all
	// request handlers see a consistent config even during hot-reload.
	if err = runningServices.ChannelManager.WrapHTTPHandler(api.configSnapshotMiddleware); err != nil {
		return nil, fmt.Errorf("wrapping HTTP handler: %w", err)
	}
	// F-13: wrap the preview server with the same middleware. Without this,
	// handlers on the preview mux (HandleServeWorkspace, HandleDevProxy) call
	// configFromContext(r.Context()) which returns nil and falls back to a
	// live read of the config pointer — a torn read during hot-reload.
	// WrapPreviewHandler is a no-op when the preview listener is disabled.
	if previewListenerEnabled {
		if err = runningServices.ChannelManager.WrapPreviewHandler(api.configSnapshotMiddleware); err != nil {
			return nil, fmt.Errorf("wrapping preview handler: %w", err)
		}
	}

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
		middleware.WithClientIPFunc(clientIP),
		middleware.WithReporter(func(r *http.Request, sourceIP, route string) {
			// Best-effort audit log of CSRF mismatches (SEC-15). Never blocks
			// or crashes the request path — the middleware already returns 403.
			logger := api.agentLoop.AuditLogger()
			if logger == nil {
				slog.Warn("csrf: token mismatch (no audit logger)",
					"source_ip", sourceIP, "route", route, "method", r.Method)
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
					"route":     route,
					"method":    r.Method,
				},
				PolicyRule: "csrf: cookie/header mismatch on state-changing request",
			}); logErr != nil {
				slog.Warn("csrf: audit log write failed", "error", logErr)
			}
		}),
	)
	if err = runningServices.ChannelManager.WrapHTTPHandler(csrfMW); err != nil {
		return nil, fmt.Errorf("wrapping HTTP handler with CSRF: %w", err)
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
	runningServices.manualReloadChan = make(chan struct{}, 1)
	runningServices.reloadTrigger = func() error {
		if !runningServices.reloading.CompareAndSwap(false, true) {
			return fmt.Errorf("reload already in progress")
		}
		select {
		case runningServices.manualReloadChan <- struct{}{}:
			return nil
		default:
			// Should not happen, but reset flag if channel is full.
			runningServices.reloading.Store(false)
			return fmt.Errorf("reload already queued")
		}
	}
	runningServices.HealthServer.SetReloadFunc(runningServices.reloadTrigger)

	if err = runningServices.ChannelManager.StartAll(context.Background()); err != nil {
		return nil, fmt.Errorf("error starting channels: %w", err)
	}

	// Boot logging (FR-020): main listener first, then preview (or disabled message).
	mainAddr := fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port)
	slog.Info("gateway listening on " + mainAddr)
	if previewListenerEnabled {
		previewAddr := fmt.Sprintf("%s:%d", cfg.Gateway.PreviewHost, int(cfg.Gateway.PreviewPort))
		slog.Info("preview listening on " + previewAddr)
	} else {
		slog.Info("preview listener disabled by config")
	}

	// Write port file so external callers (e.g. eval-runner) can discover the bound port.
	portFile := filepath.Join(cfg.WorkspacePath(), "gateway.port")
	portData := strconv.Itoa(cfg.Gateway.Port)
	if writeErr := os.WriteFile(portFile, []byte(portData+"\n"), 0o600); writeErr != nil {
		return nil, fmt.Errorf("write gateway.port: %w", writeErr)
	}

	// Self-register this process's PID so that `omnipus stop` and Status work
	// regardless of how the gateway was launched (spawner-started OR hand-started
	// via `omnipus start`). WritePID uses an atomic rename so a concurrent Status
	// call never reads a partial write. MAJOR-2: without this, a hand-started
	// gateway leaves no PID file and `omnipus stop` reports "not running".
	if pidErr := daemon.WritePID(homePath, os.Getpid()); pidErr != nil {
		// Non-fatal: the gateway is already serving traffic. Log prominently so
		// the operator knows that `omnipus stop` will not find this process.
		slog.Warn("gateway: failed to write self PID file — `omnipus stop` will not track this process",
			"pid", os.Getpid(), "home", homePath, "error", pidErr)
	} else {
		slog.Info("gateway: registered self PID", "pid", os.Getpid(), "home", homePath)
	}

	fmt.Printf(
		"✓ Health endpoints available at http://%s:%d/health, /ready and /reload (POST)\n",
		cfg.Gateway.Host,
		cfg.Gateway.Port,
	)

	stateManager := state.NewManager(cfg.WorkspacePath())
	runningServices.DeviceService = devices.NewService(devices.Config{
		Enabled:    cfg.Devices.Enabled,
		MonitorUSB: cfg.Devices.MonitorUSB,
	}, stateManager)
	runningServices.DeviceService.SetBus(msgBus)
	// Invariant: when cfg.Devices.Enabled==true, a Start failure is fatal and
	// propagated to the caller (Run returns the error). When disabled, Start
	// failures are only warnings. A unit test for this path is not included
	// because devices.Service is a concrete struct (not an interface) and
	// mocking it would require invasive refactoring; the behavior is exercised
	// by integration tests that configure a real USB monitor on supported hosts.
	if err = runningServices.DeviceService.Start(context.Background()); err != nil {
		if cfg.Devices.Enabled {
			return nil, fmt.Errorf("device service: %w", err)
		}
		logger.WarnCF(
			"device",
			"device service start failed (devices disabled, continuing)",
			map[string]any{"error": err.Error()},
		)
	} else if cfg.Devices.Enabled {
		fmt.Println("✓ Device event service started")
	}

	return runningServices, nil
}

func stopAndCleanupServices(runningServices *services, shutdownTimeout time.Duration, isReload bool) {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	// reload should not stop channel manager
	if !isReload && runningServices.ChannelManager != nil {
		runningServices.ChannelManager.StopAll(shutdownCtx)
	}
	if runningServices.DeviceService != nil {
		runningServices.DeviceService.Stop()
	}
	if runningServices.TaskDrain != nil {
		runningServices.TaskDrain.Stop()
	}
	if runningServices.MailboxDrain != nil {
		runningServices.MailboxDrain.Stop()
	}
	if runningServices.CronService != nil {
		runningServices.CronService.Stop()
	}
	if runningServices.TaskTrigger != nil {
		runningServices.TaskTrigger.Stop()
	}
	if runningServices.MediaStore != nil {
		if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
			fms.Stop()
		}
	}
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

	newModel := newCfg.Agents.Defaults.ModelName

	logger.Infof(" New model is '%s', recreating provider...", newModel)

	logger.Info("  Stopping all services...")
	stopAndCleanupServices(runningServices, serviceShutdownTimeout, true)

	// Build the real LLM provider on reload. The test_harness override hook
	// was removed 2026-05-10; reload always recreates the real provider from
	// the new config's `providers` entry.
	newProvider, newModelID, err := createStartupProvider(newCfg, allowEmptyStartup)
	if err != nil {
		logger.Errorf("  ⚠ Error creating new provider: %v", err)
		logger.Warn("  Attempting to restart services with old provider and config...")
		if restartErr := restartServices(al, runningServices, msgBus); restartErr != nil {
			logger.Errorf("  ⚠ Failed to restart services: %v", restartErr)
		}
		return fmt.Errorf("error creating new provider: %w", err)
	}

	if newModelID != "" && newCfg.Agents.Defaults.ModelName == "" {
		newCfg.Agents.Defaults.ModelName = newModelID
	}

	reloadCtx, reloadCancel := context.WithTimeout(context.Background(), providerReloadTimeout)
	defer reloadCancel()

	if err := al.ReloadProviderAndConfig(reloadCtx, newProvider, newCfg); err != nil {
		logger.Errorf("  ⚠ Error reloading agent loop: %v", err)
		if cp, ok := newProvider.(providers.StatefulProvider); ok {
			cp.Close()
		}
		logger.Warn("  Attempting to restart services with old provider and config...")
		if restartErr := restartServices(al, runningServices, msgBus); restartErr != nil {
			logger.Errorf("  ⚠ Failed to restart services: %v", restartErr)
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

func restartServices(
	al *agent.AgentLoop,
	runningServices *services,
	msgBus *bus.MessageBus,
) error {
	cfg := al.GetConfig()

	if runningServices.notifStore == nil {
		// Derive the home dir from the workspace path (workspace == <home>/workspace).
		runningServices.notifStore = notifications.NewStore(
			filepath.Join(filepath.Dir(cfg.WorkspacePath()), "notifications"),
		)
	}
	var err error
	runningServices.CronService, err = setupCronTool(
		al,
		msgBus,
		cfg.WorkspacePath(),
		cfg,
		runningServices.notifStore,
	)
	if err != nil {
		return fmt.Errorf("error restarting cron service: %w", err)
	}
	if err = runningServices.CronService.Start(); err != nil {
		return fmt.Errorf("error restarting cron service: %w", err)
	}
	// Re-point the restAPI's cronService field to the newly started instance.
	// restAPI.cronService is assigned once at construction time
	// (setupAndStartServices). On each reload, restartServices replaces
	// runningServices.CronService with a new instance whose laneCtx is live;
	// without this update the restAPI holds a stale pointer whose laneCtx was
	// canceled by the previous Stop(), causing "turn not started: context
	// canceled" on every RunNow call (#412).
	if runningServices.restAPIRef != nil {
		runningServices.restAPIRef.cronService.Store(runningServices.CronService)
	}
	fmt.Println("  ✓ Cron service restarted")

	// Restart the task time-trigger scheduler on its dedicated CronService. The
	// previous instance was already Stop()'d in stopAndCleanupServices(isReload).
	if tStore := agent.GetTaskStore(al); tStore != nil {
		triggerStorePath := filepath.Join(filepath.Dir(cfg.WorkspacePath()), "tasks_triggers", "jobs.json")
		runningServices.TaskTrigger = agent.NewTaskTriggerScheduler(
			triggerStorePath, tStore, agent.GetTaskExecutor(al),
		)
		if startErr := runningServices.TaskTrigger.Start(); startErr != nil {
			return fmt.Errorf("error restarting task trigger scheduler: %w", startErr)
		}
		al.SetTaskTriggerScheduler(runningServices.TaskTrigger)
		if recErr := runningServices.TaskTrigger.Reconcile(); recErr != nil {
			slog.Error("gateway: task trigger reconcile failed on reload", "error", recErr)
		}
		fmt.Println("  ✓ Task trigger scheduler restarted")
	}

	// Queued-task draining is owned by the dedicated TaskDrainService, never the
	// heartbeat path — restart it here so dispatch survives a reload regardless of
	// the heartbeat configuration.
	if te := agent.GetTaskExecutor(al); te != nil {
		runningServices.TaskDrain = heartbeat.NewTaskDrainService(te, 0)
		runningServices.TaskDrain.Start()
		fmt.Println("  ✓ Queued-task drain restarted (TaskDrainService)")
	}

	// Restart the M11 mailbox drain (unhandled mail → Board tasks). The previous
	// instance was Stop()'d in stopAndCleanupServices(isReload). The provider reads
	// live config + the credential store on each tick, so a mailbox added/removed
	// before this reload is reflected immediately.
	if tStore := agent.GetTaskStore(al); tStore != nil {
		credStore := runningServices.credStore
		provider := email.MailboxProviderFunc(func() []email.Mailbox {
			return buildMailboxes(al.GetConfig(), credStore)
		})
		drainer := email.NewDrainer(tStore, provider, 0)
		runningServices.MailboxDrain = heartbeat.NewMailboxDrainService(drainer, 0)
		runningServices.MailboxDrain.Start()
		fmt.Println("  ✓ Mailbox drain restarted (MailboxDrainService)")
	}

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
	oldStore := runningServices.MediaStore
	runningServices.MediaStore = media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{
		Enabled:  cfg.Tools.MediaCleanup.Enabled,
		MaxAge:   time.Duration(cfg.Tools.MediaCleanup.MaxAge) * time.Minute,
		Interval: time.Duration(cfg.Tools.MediaCleanup.Interval) * time.Minute,
	})
	if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
		// Reload refs persisted by a previous gateway instance so
		// /api/v1/media/<ref> URLs in old session transcripts still resolve.
		// Best-effort — a load failure should not block boot.
		if loadErr := fms.LoadRegistry(); loadErr != nil {
			slog.Warn("media: failed to load persisted registry", "error", loadErr)
		}
		fms.Start()
	}
	// Swap the live pointer first so uploads arriving after this point use the
	// new store (closes the N-D reload-swap window).
	al.SetMediaStore(runningServices.MediaStore)
	// Now flush and stop the old store. Stop() is idempotent and safe to call
	// even if the cleanup goroutine was never started (N3 lifecycle fix).
	if oldFMS, ok := oldStore.(*media.FileMediaStore); ok {
		oldFMS.Stop()
	}

	al.SetChannelManager(runningServices.ChannelManager)
	// Pre-Reload wire: set observers before Reload() so that channels whose
	// Start() runs *inside* Reload() already have the CancelInterceptor and
	// PairingObserver set when they first emit events.
	wireChannelManager(runningServices.ChannelManager, al)

	if err = runningServices.ChannelManager.Reload(context.Background(), cfg, runningServices.bundle); err != nil {
		return fmt.Errorf("error reload channels: %w", err)
	}
	// Post-Reload re-wire: Reload() may have recreated channel instances (new
	// struct value with nil fields), which clears the observer pointer set
	// above.  Re-wiring here ensures the observers are always live once the
	// reload completes, regardless of whether instances were recreated.
	wireChannelManager(runningServices.ChannelManager, al)
	fmt.Println("  ✓ Channels restarted.")

	enabledChannels := runningServices.ChannelManager.GetEnabledChannels()
	if len(enabledChannels) > 0 {
		fmt.Printf("  ✓ Channels enabled: %s\n", enabledChannels)
	} else {
		fmt.Println("  ⚠ Warning: No channels enabled")
	}

	// Stop the previous DeviceService before replacing it to avoid goroutine
	// leaks: the old service's goroutine would keep running with a dangling
	// pointer if we only overwrite the field.
	if oldDS := runningServices.DeviceService; oldDS != nil {
		oldDS.Stop()
	}
	stateManager := state.NewManager(cfg.WorkspacePath())
	runningServices.DeviceService = devices.NewService(devices.Config{
		Enabled:    cfg.Devices.Enabled,
		MonitorUSB: cfg.Devices.MonitorUSB,
	}, stateManager)
	runningServices.DeviceService.SetBus(msgBus)
	if err := runningServices.DeviceService.Start(context.Background()); err != nil {
		if cfg.Devices.Enabled {
			return fmt.Errorf("device service: %w", err)
		}
		logger.WarnCF(
			"device",
			"device service start failed (devices disabled, continuing)",
			map[string]any{"error": err.Error()},
		)
	} else if cfg.Devices.Enabled {
		fmt.Println("  ✓ Device event service restarted")
	}

	transcriber := voice.DetectTranscriber(cfg, runningServices.bundle)
	al.SetTranscriber(transcriber)
	if transcriber != nil {
		logger.InfoCF("voice", "Transcription re-enabled (agent-level)", map[string]any{"provider": transcriber.Name()})
	} else {
		logger.InfoCF("voice", "Transcription disabled", nil)
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
func setupConfigWatcherPolling(
	configPath string,
	debug bool,
	credStore *credentials.Store,
	selfWriteReg *configSelfWriteRegistry,
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
	// M2: resolve the channel registry lazily — the channel manager is wired onto
	// the agent loop after this runner is built (SetChannelManager during service
	// start), so the runner re-fetches it at delivery time. A nil manager makes
	// channelIsActive degrade to the legacy non-empty check.
	runner.setChannelChecker(func() channelChecker {
		if cm := agentLoop.GetChannelManager(); cm != nil {
			return cm
		}
		return nil
	})
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

// agentCheckerFunc adapts a func to the agentChecker interface used by the
// scheduled runner.
type agentCheckerFunc func(agentID string) bool

func (f agentCheckerFunc) IsRegistered(agentID string) bool { return f(agentID) }

// shouldWarnPreviewOrigin returns true when the preview listener is bound on a
// wildcard address (0.0.0.0 or "::") and gateway.preview_origin is empty.
// In that situation the computed preview URL will contain the literal string
// "0.0.0.0" which browsers cannot reach. Extracted as a pure function so it
// can be unit-tested without starting a full gateway.
func shouldWarnPreviewOrigin(previewHost, previewOrigin string) bool {
	if previewOrigin != "" {
		return false
	}
	return previewHost == "0.0.0.0" || previewHost == "::"
}

// emitGHSARemovalWarn logs a WARN when any agent that has a remote channel
// mapping does NOT explicitly deny the exec tool. The GHSA-pv8c-p6jf-3fpp
// per-channel exec block was removed; exec access is now governed entirely by
// per-agent ToolPolicyCfg. This single-shot boot warning prompts operators to
// review agent policies.
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

	// Scan agents: flag any that do not explicitly deny exec.
	var flagged []string
	for _, ag := range cfg.Agents.List {
		if ag.Tools == nil {
			// No tools config → inherits default_policy (allow). Flagged.
			flagged = append(flagged, ag.ID)
			continue
		}
		policy := ag.Tools.Builtin.ResolvePolicy("exec")
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
		"exec tool no longer blocked at the channel layer (was GHSA-pv8c-p6jf-3fpp). "+
			"Agents with remote channels and non-deny exec policy: ["+strings.Join(flagged, ", ")+
			"]. Review per-agent ToolPolicyCfg.",
		"remote_channels", channels,
		"flagged_agents", flagged,
	)
}
