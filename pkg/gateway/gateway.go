//go:build !cgo

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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	runtimedebug "runtime/debug"
	"strconv"
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
	"github.com/elicify-ai/omnipus/pkg/daemon"
	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/devices"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/heartbeat"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/policy"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/capabilities"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/state"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/voice"
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
// channels that are currently enabled, PLUS the non-channel credential refs
// (voice transcription, web-search tools, skill marketplaces) that are
// currently in use. Used by bootCredentials/executeReload/
// refreshConfigAndRewireServices to distinguish a credential resolution
// failure on something actually in use (fatal — see enabledRefFromBundleError
// for why a non-NotFoundError failure is worse than a simple missing ref)
// from one on a disabled/unused feature (Info/Warn + continue).
//
// Provider APIKeyRef misses are NOT included here — they are already fatal
// via InjectFromConfig, which independently aborts boot/reload on any
// provider credential injection error (see bootCredentials and executeReload,
// both of which call InjectFromConfig before ever consulting this map), so
// they never need this map's separate escalation path.
//
// The non-channel categories (voice, web-search tools, skill marketplaces)
// mirror credentials.ResolveAll's nonChannelRefs slice (pkg/credentials/
// inject.go). Mailbox refs are NOT part of that slice — ResolveAll resolves
// cfg.Mailboxes via its own separate per-(agent,workspace) loop — but they
// are still a category credentials.ResolveBundle (== ResolveAll) can
// produce a resolution error for, so they are covered below too. Together,
// nonChannelRefs + the mailbox loop + the channel *_ref fields above are the
// full set of refs ResolveAll can fail to resolve; this map must stay in
// sync with all of them or a corrupted ref anywhere in that set degrades to
// a silent Warn again.
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
	// Voice transcription keys have no separate on/off toggle in VoiceConfig
	// (unlike the web-search tools below) — a populated ref IS the "in use"
	// signal, matching how the ElevenLabs/Groq transcribers key off ref
	// presence alone.
	for _, ref := range []string{cfg.Voice.ElevenLabsAPIKeyRef, cfg.Voice.GroqAPIKeyRef} {
		if ref != "" {
			m[ref] = true
		}
	}
	// Web-search tool keys — only "in use" when the tool itself is enabled,
	// mirroring the channel-Enabled gate above.
	for _, webTool := range []struct {
		enabled bool
		ref     string
	}{
		{cfg.Tools.Web.Brave.Enabled, cfg.Tools.Web.Brave.APIKeyRef},
		{cfg.Tools.Web.Tavily.Enabled, cfg.Tools.Web.Tavily.APIKeyRef},
		{cfg.Tools.Web.Perplexity.Enabled, cfg.Tools.Web.Perplexity.APIKeyRef},
		{cfg.Tools.Web.GLMSearch.Enabled, cfg.Tools.Web.GLMSearch.APIKeyRef},
		{cfg.Tools.Web.BaiduSearch.Enabled, cfg.Tools.Web.BaiduSearch.APIKeyRef},
	} {
		if webTool.enabled && webTool.ref != "" {
			m[webTool.ref] = true
		}
	}
	// Skill marketplace credential refs — only "in use" when the marketplace
	// entry itself is enabled.
	for _, mk := range cfg.Tools.Skills.Marketplaces {
		if !mk.Enabled {
			continue
		}
		for _, ref := range []string{mk.AuthTokenRef, mk.TokenRef} {
			if ref != "" {
				m[ref] = true
			}
		}
	}
	// Mailbox passwords (M11) — resolved by ResolveAll's own dedicated
	// per-(agent,workspace) loop (pkg/credentials/inject.go), not part of
	// nonChannelRefs. MailboxConfig.Enabled gates whether the owning agent's
	// email tools are registered for that (agent, workspace) pair — mirror
	// that as the "in use" signal here too, the same Enabled-gate pattern
	// used for channels and skill marketplaces above (not the ref-presence-
	// alone signal used for voice, which has no separate toggle).
	for _, byWorkspace := range cfg.Mailboxes {
		for _, mb := range byWorkspace {
			if !mb.Enabled {
				continue
			}
			if ref := mb.PasswordRef; ref != "" {
				m[ref] = true
			}
		}
	}
	return m
}

// resolveAllRefPattern extracts the credential ref name that
// credentials.ResolveAll embeds in every per-ref resolution error:
// `fmt.Errorf("ResolveAll: credential %q: %w", ref, err)`. The capture group
// is the full Go-quoted (%q) literal, including its surrounding double
// quotes and any backslash escapes — decoded via strconv.Unquote below so a
// ref name containing a quote or backslash (however unlikely) round-trips
// correctly instead of truncating the match early.
var resolveAllRefPattern = regexp.MustCompile(`credential ("(?:[^"\\]|\\.)*"):`)

// enabledRefFromBundleError attributes a non-NotFoundError credential bundle
// resolution error (wrong master key, corrupted store entry, decrypt
// failure, ...) to the currently-in-use ref in enabledRefs — an enabled
// channel's ref or an in-use non-channel ref (voice, web-search tool, skill
// marketplace; see buildEnabledRefMap).
//
// This parses the ref name directly out of ResolveAll's wrap format via
// resolveAllRefPattern instead of doing a Contains-loop over every enabled
// ref. The Contains-loop approach was ambiguous: if two enabled refs are
// substrings of each other's names (e.g. "sec" and "my_secret_token", both
// enabled), a failure on "my_secret_token" could match "sec" first — Go map
// iteration order is randomized, so the misattribution was nondeterministic
// across runs. The escalation path (fatal boot / rejected reload) still
// fired correctly either way — this was a misdirection bug for the operator
// reading the error, not a missed-detection bug. Parsing the exact ref out
// of the error message removes the ambiguity entirely: there is exactly one
// ref embedded in the message, and we look it up in enabledRefs rather than
// searching enabledRefs for a substring match against the message.
//
// Returns ("", false) when the error doesn't match ResolveAll's wrap format,
// or when the parsed ref is not present in enabledRefs (e.g. it belongs to a
// disabled channel or a provider key — Warn is sufficient for those, as
// today).
func enabledRefFromBundleError(err error, enabledRefs map[string]bool) (string, bool) {
	match := resolveAllRefPattern.FindStringSubmatch(err.Error())
	if match == nil {
		return "", false
	}
	ref, unquoteErr := strconv.Unquote(match[1])
	if unquoteErr != nil {
		return "", false
	}
	if !enabledRefs[ref] {
		return "", false
	}
	return ref, true
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

	// Build a ref→in-use map so we can distinguish a missing credential on
	// something actually enabled/in-use (fatal) from one on a disabled
	// channel or unused feature (Info + continue).
	enabledRefs := buildEnabledRefMap(cfg)

	// Resolve all credential refs into a SecretBundle. Channels receive secrets
	// via the bundle — no os.Setenv for channel credentials (B1 fix).
	bundle, bundleErrs := credentials.ResolveBundle(cfg, credStore)
	for _, e := range bundleErrs {
		var notFound *credentials.NotFoundError
		if errors.As(e, &notFound) {
			if enabledRefs[notFound.Name] {
				// Missing credential on something actually enabled/in-use
				// (channel, voice, web-search tool, skill marketplace) is
				// fatal at boot.
				return nil, nil, nil, fmt.Errorf(
					"fatal: enabled credential %q not found in store — "+
						"ensure the credential is stored before starting: %w",
					notFound.Name, e,
				)
			}
			slog.Info("credential not found (not currently enabled/in use)", "ref", notFound.Name)
			continue
		}
		// Any error other than "ref not found" (wrong master key, corrupted
		// credential store entry, decrypt failure, ...) means the ref IS
		// configured but unreadable — worse than a simple missing ref, since
		// the operator believes it is set up correctly. On something that is
		// actually ENABLED/in-use this would otherwise only produce a
		// slog.Warn an operator can easily miss, then it starts (or keeps
		// running) silently without its secret. Escalate to the same fatal
		// treatment boot already applies to the NotFoundError-on-enabled case
		// above, instead of inventing a separate degraded-signal mechanism.
		if ref, ok := enabledRefFromBundleError(e, enabledRefs); ok {
			return nil, nil, nil, fmt.Errorf(
				"fatal: enabled credential %q failed to resolve (not simply "+
					"missing — check OMNIPUS_MASTER_KEY / credentials.json integrity): %w",
				ref, e,
			)
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

// buildKnownBuiltinToolNames returns the set of every static builtin tool name
// across the three built-in catalogs — general, browser, and sysagent (the
// system.* / "system agent" tools). This is the wildcard-free literal
// universe config.ValidateToolPolicyCoverage checks every agent's tool-policy
// map against (CLAUDE.md hard constraint 6). MCP tool names are deliberately
// excluded: they aren't known until an operator connects a server at runtime
// and remain governed by the per-server wildcard-bulk-policy exception.
//
// All three sources are metadata-only, never-Execute()d constructions:
//
//   - tools.GeneralBuiltinMetadata() and browser.BrowserBuiltinMetadata() are
//     the existing central-registry catalog functions (Issue #350, ADR-018
//     D-A1).
//
//     NOTE: this function is NOT the source GET /api/v1/tools uses (an
//     earlier version of this comment incorrectly claimed it was — confirmed
//     false by architect review). That endpoint (HandleToolsRegistry,
//     pkg/gateway/rest_tool_registry.go) enumerates tools independently via
//     a.builtinRegistry.All() (or, when unwired, the default agent's live
//     registered Tools.GetAll()) plus a.mcpRegistry.All() for MCP — a
//     completely separate, independently-maintained code path that only
//     coincidentally agrees with this function's output today (every static
//     tool registers with IsCore:true unconditionally, so it always appears
//     regardless of which agent is default; only MCP tools use a different,
//     TTL-gated registration path). See
//     TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog for
//     the drift-detection regression test this package carries instead.
//
//   - systools.AllTools(nil, nil) has no equivalent metadata-only catalog
//     function, but is safe to call for name-harvesting alone: every
//     constructor in pkg/sysagent/tools does nothing but store the *Deps
//     pointer it is given (never dereferenced at construction time), and
//     every tool's Name() method is a static string literal that never reads
//     deps. Passing nil deps and a nil NavigateCallback is therefore safe
//     PROVIDED the returned tools are never Execute()d here — and they never
//     are; only .Name() is called below.
//
// The three static catalogs never change at runtime, so the result is
// computed once (guarded by knownBuiltinToolNamesOnce) and the same shared
// map is returned to every caller thereafter. Safe only because no caller
// mutates the returned map — it is read-only past this function.
func buildKnownBuiltinToolNames() map[string]struct{} {
	knownBuiltinToolNamesOnce.Do(func() {
		out := make(map[string]struct{})
		for _, t := range tools.GeneralBuiltinMetadata() {
			out[t.Name()] = struct{}{}
		}
		for _, t := range browser.BrowserBuiltinMetadata() {
			out[t.Name()] = struct{}{}
		}
		for _, t := range systools.AllTools(nil, nil) {
			out[t.Name()] = struct{}{}
		}
		knownBuiltinToolNamesCache = out
	})
	return knownBuiltinToolNamesCache
}

var (
	knownBuiltinToolNamesOnce  sync.Once
	knownBuiltinToolNamesCache map[string]struct{}
)

// repairAndValidateToolPolicyCoverage runs the shared "backfill pre-existing
// gaps, then hard-validate what remains" sequence used identically at boot
// (RunContextWithOptions) and at hot-reload (executeReload) — CLAUDE.md hard
// constraint 6. Both call sites previously hand-rolled this same
// knownTools-assembly + repair + summary-log sequence independently; sharing
// one helper means the two can no longer silently diverge on what "repair
// then validate" means, mirroring the reasoning behind
// config.RepairIncompleteToolPolicyCoverage now delegating to
// config.ValidateToolPolicyCoverage instead of re-deriving its predicate.
//
// Repairing first means installations whose on-disk config predates the
// DefaultPolicy/default_policy fallback removal (sparse per-agent Policies
// maps) get backfilled to explicit "deny" instead of tripping validation on
// every restart/reload. Logs one WARN naming every backfilled (agent, tool)
// pair (config.RepairIncompleteToolPolicyCoverage itself also logs one WARN
// per repaired agent; this one is the gateway-level summary).
//
// Returns the remaining gaps (empty = fully covered) — the caller decides
// what "remaining gaps" means for it (abort boot vs. reject the reload and
// keep serving the previous config).
func repairAndValidateToolPolicyCoverage(cfg *config.Config) []config.CoverageGap {
	knownTools := buildKnownBuiltinToolNames()
	if repaired := config.RepairIncompleteToolPolicyCoverage(cfg, knownTools); len(repaired) > 0 {
		agentIDs := make(map[string]struct{}, len(repaired))
		for _, gap := range repaired {
			agentIDs[gap.AgentID] = struct{}{}
		}
		slog.Warn("gateway: backfilled incomplete tool-policy coverage on load",
			"agent_count", len(agentIDs),
			"gap_count", len(repaired),
			"gaps", joinCoverageGapMessages(repaired),
		)
	}
	return config.ValidateToolPolicyCoverage(cfg, knownTools)
}

// egressProxyConstructor abstracts sandbox.NewEgressProxy so
// buildEgressProxyOrAbort is unit-testable without depending on
// sandbox.NewEgressProxy's real failure modes (a malformed allow-list entry,
// or the practically-unforceable net.Listen("tcp", "127.0.0.1:0") error).
// Production always passes sandbox.NewEgressProxy itself.
type egressProxyConstructor func([]string, sandbox.EgressAuditFunc) (*sandbox.EgressProxy, error)

// buildEgressProxyOrAbort constructs the Tier 2/3 egress proxy and decides
// what a construction failure means, per CLAUDE.md hard constraint 6
// (deny-by-default / fail-closed for security controls).
//
// Downstream, a nil *sandbox.EgressProxy is NOT a "feature refuses to run"
// signal — it is silently interpreted as "run unrestricted":
//   - pkg/tools/web_serve.go's (*WebServeTool).proxyAddr() returns "" for a
//     nil proxy, so spawnDevChild's sandbox.Limits.EgressProxyAddr is "" and
//     the Tier 3 dev-server child gets no HTTP_PROXY/HTTPS_PROXY at all.
//   - pkg/tools/shell.go's sandboxLimitsEnv (~line 941-958) only injects
//     HTTP_PROXY/HTTPS_PROXY when lim.EgressProxyAddr != "" — an empty addr
//     means bash's hardened path runs with zero egress restriction.
//
// So: when the operator left sandbox.egress_allow_list EMPTY (never opted
// into egress restriction), a construction failure is logged and swallowed
// exactly as before — nil proxy, log-and-continue is the documented
// graceful-degradation behavior for a feature nobody asked for.
//
// When the operator configured a NON-EMPTY sandbox.egress_allow_list
// (explicit opt-in) and construction fails, continuing to boot would
// silently run web_serve dev-mode and bash's egress-dependent path fully
// unrestricted — the exact protection the operator asked for would vanish
// with only a Warn-level log line. That is a strictly worse outcome than
// refusing to start, so this mirrors the audit-logger-construction
// precedent a few hundred lines up (RunContextWithOptions's
// audit.LoggerConstructionError branch, ~line 723-741): wrap the error in
// *SandboxBootError so cmd/omnipus's existing exit-code mapping
// (EX_CONFIG=78, FR-J-004) applies without further plumbing, and emit the
// same stderr breadcrumb via audit.EmitBootAbortStderr.
func buildEgressProxyOrAbort(
	allowList []string,
	auditFn sandbox.EgressAuditFunc,
	newProxy egressProxyConstructor,
) (*sandbox.EgressProxy, error) {
	proxy, err := newProxy(allowList, auditFn)
	if err == nil {
		return proxy, nil
	}

	if len(allowList) == 0 {
		slog.Warn("gateway: egress proxy failed to start; web_serve dev mode will run without egress enforcement",
			"error", err)
		return nil, nil
	}

	audit.EmitBootAbortStderr(
		"gateway.egress_proxy.construction_failed",
		"-",
		"",
		err,
		[]audit.KV{{Key: "allow_list_entries", Value: strconv.Itoa(len(allowList))}},
	)
	return nil, &SandboxBootError{Err: fmt.Errorf(
		"gateway: egress proxy failed to start with sandbox.egress_allow_list configured (%d entr%s) — "+
			"refusing to boot rather than silently running web_serve dev-mode and bash's hardened exec path "+
			"WITHOUT the egress enforcement you configured; fix the allow-list entries or the underlying "+
			"error and restart, or clear sandbox.egress_allow_list to accept unrestricted egress: %w",
		len(allowList), pluralSuffix(len(allowList)), err,
	)}
}

// pluralSuffix returns "y" for n == 1 ("entry") and "ies" otherwise
// ("entries") — small formatting helper for buildEgressProxyOrAbort's error
// message so it reads correctly for both singular and plural allow-lists.
func pluralSuffix(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
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

// RunContextWithOptions is the Sprint-J context-cancellable entry point.
// RunContext is a thin wrapper that builds a legacy RunOptions and calls this.
func RunContextWithOptions(ctx context.Context, opts RunOptions) error {
	debug := opts.Debug
	homePath := opts.HomePath
	configPath := opts.ConfigPath
	allowEmptyStartup := opts.AllowEmptyStartup
	allowGodMode := opts.AllowGodMode

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
	logsDir := filepath.Join(homePath, logPath)
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	panicPath := filepath.Join(logsDir, panicFile)
	panicFunc, err := logger.InitPanic(panicPath)
	if err != nil {
		return fmt.Errorf("error initializing panic log: %w", err)
	}
	defer panicFunc()

	// Re-review FIX 1 (two independent reviewers, plus a third verifying
	// pass): bootLoggingAndDataModel runs logger.EnableFileLogging,
	// installSlogBridge, and datamodel.Init in the one order that captures
	// datamodel.Init's own first-run slog calls instead of silently losing
	// them — this used to call datamodel.Init BEFORE any of this boot
	// preamble existed. See bootLoggingAndDataModel's doc comment for the
	// full account and why logger.InitPanic stays inline here rather than
	// folding into that helper.
	if err = bootLoggingAndDataModel(homePath); err != nil {
		return err
	}
	defer logger.DisableFileLogging()

	// Construct and unlock the credential store BEFORE loading config, per the
	// documented credential boot contract (ADR-004).
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
	for _, browserMgr := range agentLoop.BrowserManagers() {
		// Go 1.22+ loop semantics: browserMgr is per-iteration already, no
		// shadow copy needed before capturing it in the goroutine below.
		go func() {
			path, ppErr := browserMgr.Preprovision(ctx)
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
				logger.InfoCF("browser", "preprovision resolved",
					map[string]any{"exec_path": path})
			}
		}()
	}

	// Boot-time browser WARM-UP (distinct from Preprovision above):
	// Preprovision only RESOLVES (and, on a fresh install, downloads) a
	// Chromium binary — it never launches the process. Chrome launch, the
	// agent's first tab, and the CDP capture-extension load stayed entirely
	// lazy, deferred to an agent's first browser tool call
	// (BrowserManager.ensureStarted, reached via Session()/createFirstTab()).
	// That lazy path is the unbounded cold start ADR-042 recorded at 30-60s
	// historically — long enough to blow past the browser WS handler's 60s
	// read deadline and tear down the connection. Kick the actual launch off
	// here too so the shared Chrome is already up (process live, CDP pipe
	// dialed, capture extension loaded if configured — see
	// BrowserCoordinator.WarmUp) by the time any agent's first interaction
	// needs it.
	//
	// Only ONE shared, gateway-scoped Chrome exists regardless of agent
	// count (coordinator.go's whole design) — this warms that ONE instance,
	// found via the first browser manager whose Coordinator() is non-nil (in
	// coordinator mode every agent's manager is attached to the exact same
	// *browser.BrowserCoordinator, so any one of them resolves it). A
	// per-agent Chrome pool is explicitly out of scope — tracked separately
	// as issue #570.
	//
	// Respects tools.browser.cdp_url: in remote-CDP mode there is nothing to
	// warm (an operator-managed Chromium elsewhere; launching a local Chrome
	// here would be wrong and wasteful), so this is skipped entirely when
	// cdp_url is set. tools.browser.enabled (default true — browser tools
	// are a standard built-in like exec/web/cron) additionally gates it so a
	// deployment that has explicitly disabled browser tooling doesn't pay
	// for it either.
	//
	// Operator opt-out: `tools.browser.warm_at_boot`
	// (config.BrowserToolConfig.WarmAtBoot, env
	// OMNIPUS_TOOLS_BROWSER_WARM_AT_BOOT), default true — following the same
	// "default true" pattern as LiveViewEnabled/WebRTCEnabled/
	// CaptureSharedContext. Setting it false keeps browser tools fully
	// available and simply defers the Chrome launch to first use; it does
	// not disable the browser.
	//
	// Skippable via OMNIPUS_SKIP_BROWSER_PREPROVISION=1 — the same escape
	// hatch an integration test harness needs whenever it wants a fully
	// browser-inert boot (no launch attempt of any kind, eager or lazy):
	// without it, a harness that configures a real/valid exec_path for a
	// browser-tool test would otherwise have this warm-up race a test's
	// t.TempDir() cleanup exactly like the historical Preprovision-download
	// flake this mirrors the escape hatch for.
	//
	// Best-effort + non-blocking, matching the Preprovision loop's own
	// contract exactly: boot must not stall or fail on a browser problem
	// (CLAUDE.md graceful degradation, Hard Constraint #4). A bare `go
	// func(){}()`, not joined by anything — gateway shutdown does not wait
	// for it, so it cannot delay RunContext's return. ctx is the gateway's
	// own shutdown-aware context (canceled when the gateway is asked to
	// stop); WarmUp's underlying ensureLaunched/exec-path resolution honors
	// it for cancellation during resolution, and BrowserCoordinator.Shutdown
	// (invoked from the gateway's own Close path, not from here) remains the
	// sole process-kill path regardless of whether this warm-up finished.
	// browserWarmUpEnabled/findSharedBrowserCoordinator are extracted (rather
	// than inlined here) so the exact decision logic gateway.RunContext acts
	// on is unit-testable without booting a full gateway.
	if browserWarmUpEnabled(cfg) {
		sharedBrowserCoordinator := findSharedBrowserCoordinator(agentLoop.BrowserManagers())
		if sharedBrowserCoordinator == nil {
			// Do NOT fall through silently. Warm-up being enabled but finding
			// nothing to warm is otherwise indistinguishable from "warm-up is
			// off" and from "warm-up is still running" — an operator who set
			// warm_at_boot and then sees a slow first interaction would have
			// no way to tell which of the three happened.
			logger.InfoCF(
				"browser",
				"boot-time Chrome warm-up enabled but no shared browser coordinator was found — nothing to warm; Chrome will launch lazily at first browser tool use",
				nil,
			)
		} else {
			go func() {
				// Boot must never die on a browser problem (Hard Constraint #4).
				// The error path is already best-effort, but an unexpected PANIC
				// in the warm-up chain would take the whole gateway process down
				// with it — turning an optional latency optimisation into a boot
				// crash. Contain it and fall back to the lazy path.
				// Both failure paths below ALSO emit an audit event, not just a
				// log line. Every other browser-lifecycle failure in this
				// codebase is auditable (EventBrowserWebRTCStreamStartFailed,
				// EventBrowserWebRTCIngestAuthRejected,
				// EventBrowserWebRTCViewerOfferFailed); warm-up was log-only, so
				// an operator reconstructing "did Chrome come up at boot?" from
				// the audit trail found nothing — silence indistinguishable from
				// "warm-up disabled" and from "warm-up succeeded". Success stays
				// log-only: the audit trail records the exceptional outcome, not
				// the routine one.
				defer func() {
					if r := recover(); r != nil {
						logger.WarnCF(
							"browser",
							"boot-time Chrome warm-up panicked — recovered; will launch lazily at first browser tool use",
							map[string]any{"panic": fmt.Sprintf("%v", r)},
						)
						audit.Emit(
							context.Background(), agentLoop.AuditLogger(),
							audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
							map[string]any{"reason": "panic", "error": fmt.Sprintf("%v", r)},
						)
					}
				}()
				if warmErr := sharedBrowserCoordinator.WarmUp(ctx); warmErr != nil {
					logger.WarnCF(
						"browser",
						"boot-time Chrome warm-up failed — will launch lazily at first browser tool use",
						map[string]any{"error": warmErr.Error()},
					)
					audit.Emit(
						context.Background(), agentLoop.AuditLogger(),
						audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
						map[string]any{"reason": "error", "error": warmErr.Error()},
					)
					return
				}
				logger.InfoCF("browser", "shared Chrome warmed up at boot", nil)
			}()
		}
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
		// repairAndValidateToolPolicyCoverage runs the shared "backfill
		// pre-existing gaps, then hard-validate what remains" sequence: it
		// migrates installations whose on-disk config predates the
		// DefaultPolicy/default_policy fallback removal (sparse Policies
		// maps that relied on the deleted default field) by backfilling every
		// missing entry to explicit "deny". Without this, upgrading an
		// existing installation would find a coverage gap for nearly every
		// static tool on nearly every agent and abort boot on every restart.
		// After the repair, validation should almost always find zero gaps —
		// it remains as the hard backstop for anything the repair cannot
		// close (e.g. a genuinely corrupt config).
		if gaps := repairAndValidateToolPolicyCoverage(cfg); len(gaps) > 0 {
			for _, g := range gaps {
				slog.Error("gateway: tool-policy coverage gap", "detail", g.String())
			}
			return fmt.Errorf(
				"gateway: tool-policy coverage validation failed — aborting boot (%d gap(s), see preceding error logs)",
				len(gaps),
			)
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
	// Wire the central registries into the agent loop so ReconcileMCP (triggered
	// by REST/sysagent MCP writes and hot-reload) populates the SAME registry
	// instance restAPI reads for GET /api/v1/tools and /mcp-servers tool_count —
	// otherwise connected MCP tools would never surface outside the per-agent
	// registries. Nil-safe on the AgentLoop side.
	agentLoop.SetCentralMCPRegistries(centralMCPReg, centralBuiltinReg)

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
	skillsWorkspace := cfg.AgentHomeBasePath()
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
		// Live MCP reconciliation: without these, add/remove_mcp_server
		// only persist config — the server never actually connects and its tools never
		// reach the central/per-agent registries until the next hot reload or process restart.
		ReconcileMCP: agentLoop.ReconcileMCP,
		MCPStatus:    agentLoop.MCPServerStatus,
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

	// centralBuiltinReg was just reassigned to a fresh instance above — re-wire
	// it (and centralMCPReg, unchanged but re-asserted for clarity) into the
	// agent loop so MCP name-collision admission (ValidateMCPName) checks
	// against the live-deps builtin instance, not the pre-deps one the earlier
	// SetCentralMCPRegistries call saw.
	agentLoop.SetCentralMCPRegistries(centralMCPReg, centralBuiltinReg)

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

	// Per-task run-history prune + stuck-run reaper (ADR-050 RD9/RD10) runs
	// alongside the transcript sweep on the same retention window/cadence —
	// see pkg/gateway/retention_task_runs.go. GetTaskStore returns nil only
	// when the task store failed to initialize; the tick no-ops via the nil
	// check in executeSweepTick when that happens.
	if tStore := agent.GetTaskStore(agentLoop); tStore != nil {
		retentionTaskRunSweepFn = func(cutoff time.Time) (int, error) {
			return pruneAllTaskRuns(tStore, cutoff)
		}
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

// browserWarmUpEnabled reports whether RunContextWithOptions' boot-time
// browser warm-up (see the "Boot-time browser WARM-UP" block above) should
// run for cfg, given the current process environment. Extracted as a small,
// pure predicate — no side effects, no coordinator/manager access — so the
// exact decision gateway.go's boot sequence makes is unit-testable without
// booting a full gateway.
//
// cfg.Tools.Browser.WarmAtBoot (default true, see its doc comment on
// BrowserToolConfig) is the operator's opt-out: setting it false keeps browser
// tools fully available and simply defers the Chrome launch to first use.
//
// cfg.Tools.Browser.Enabled (default true) and an empty CDPURL together mean
// "this deployment wants local browser automation" — an empty/remote-CDP or
// explicitly-disabled config must never trigger a local Chrome launch.
//
// OMNIPUS_SKIP_BROWSER_PREPROVISION=1 is an unconditional override: when
// set, this returns false regardless of cfg, matching Preprovision's own
// long-standing "escape hatch a test harness needs" contract (see the boot
// wiring's doc comment for why an integration test harness needs one).
func browserWarmUpEnabled(cfg *config.Config) bool {
	return cfg.Tools.Browser.WarmAtBoot &&
		cfg.Tools.Browser.Enabled &&
		cfg.Tools.Browser.CDPURL == "" &&
		os.Getenv("OMNIPUS_SKIP_BROWSER_PREPROVISION") != "1"
}

// findSharedBrowserCoordinator returns the ONE gateway-scoped
// *browser.BrowserCoordinator shared across every agent's *browser.
// BrowserManager in coordinator mode (nil when none is attached — e.g. every
// agent is configured for remote CDP, or the list is empty). All managers in
// coordinator mode share the exact same coordinator instance
// (pkg/agent/loop.go's registerSharedTools wiring), so the first non-nil
// Coordinator() found is sufficient; there is no need to look further once
// one is found. Extracted from the boot-time warm-up wiring so it is
// unit-testable in isolation with fake managers.
func findSharedBrowserCoordinator(mgrs []*browser.BrowserManager) *browser.BrowserCoordinator {
	for _, mgr := range mgrs {
		if mgr == nil {
			continue
		}
		if coord := mgr.Coordinator(); coord != nil {
			return coord
		}
	}
	return nil
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

	// CLAUDE.md hard constraint 6 / config.ValidateToolPolicyCoverage: a
	// config reload (file-watcher poll via configReloadChan, or manual
	// /reload via manualReloadChan — both funnel through this function) must
	// be held to the same tool-policy coverage bar as boot and the REST
	// write handlers. Without this check, a hand-edited config.json picked
	// up by hot-reload would bypass coverage enforcement entirely, silently
	// reintroducing a runtime-default gap this whole change eliminated.
	// repairAndValidateToolPolicyCoverage repairs first (same migration
	// semantics as boot: backfill any missing entry to explicit "deny"), then
	// validates as the hard backstop. A genuine gap rejects the reload and
	// keeps serving the PREVIOUS live config — mirrors the
	// credential-injection-failure rejection pattern immediately below.
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
) (rs *services, retErr error) {
	runningServices := &services{credStore: credStore, bundle: bundle, sandboxResult: sandboxResult, homePath: homePath}

	// Per-user notification store (#264). Backs schedule-failure notifications and
	// the header notification center.
	runningServices.notifStore = notifications.NewStore(filepath.Join(homePath, "notifications"))

	var err error
	runningServices.CronService, err = setupCronTool(
		agentLoop,
		msgBus,
		cfg.AgentHomeBasePath(),
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

	// Wire the workspace-library provider into the media store so
	// media://workspace/<ws>/<id> refs resolve through the owning
	// workspace's library (FR-028). MUST share AgentLoop's workspace
	// library cache — a separate cache here caused live UAT failures
	// where uploads (via GetWorkspaceLibrary) updated one in-memory
	// manifest while resolve (via this provider) read a stale sibling.
	if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
		fms.SetWorkspaceLibraryProvider(func(workspaceID string) (media.WorkspaceLibraryResolver, error) {
			lib := agentLoop.GetWorkspaceLibrary(workspaceID)
			if lib == nil {
				return nil, fmt.Errorf("workspace library unavailable for %q", workspaceID)
			}
			return lib, nil
		})
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
		// FIX 2: genuinely operator-relevant (no channel is reachable at all)
		// and otherwise invisible on a backgrounded launch — pair with
		// logger.WarnCF, same reasoning as the queued-task-drain warning above.
		logger.WarnCF("gateway", "no channels enabled — gateway has no reachable channel", nil)
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
	emitGHSARemovalWarn(cfg)

	// Construct the web_serve static-mode (Tier 1) and dev-mode (Tier 3)
	// shared registries. These are always created; gateway.preview_enabled
	// (ADR-044) gates /preview/ and serve_web live, per-request — it does not
	// affect whether these registries themselves are constructed.
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

	egressProxy, epErr := buildEgressProxyOrAbort(cfg.Sandbox.EgressAllowList, egressAuditFn, sandbox.NewEgressProxy)
	if epErr != nil {
		return nil, epErr
	}
	if egressProxy != nil {
		runningServices.egressProxy = egressProxy
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
	browserWSHandler := newBrowserWSHandler(agentLoop, allowedOrigin)
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/browser/ws", browserWSHandler)

	// Capture-ingest WS (ADR-047 D6, wave-plan W2-A) — the gateway-owned
	// WebRTC capture extension's ingest leg. Loopback-only (RemoteAddr
	// gate in captureIngestWSHandler.ServeHTTP, not an origin/auth check —
	// the caller is a CDP-driven page inside the managed Chrome, not a
	// browser client), authorized by a per-stream token (BindIngest/
	// findByToken), sharing browserWSHandler's captureRegistry so a
	// browser_webrtc_offer's session can be found by its ingest hello.
	captureIngestHandler := newCaptureIngestWSHandler(agentLoop, browserWSHandler.captures)
	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/browser/capture-ingest", captureIngestHandler)

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

	// Build the capability catalog for model media-modality resolution
	// (FR-024/FR-025/FR-026): seeded from embedded data, refreshed from
	// the Omnipus GitHub release every 7 days.
	//
	// Re-review FIX 1b: capabilities.NewCatalog's `log` parameter used to be
	// slog.Default() directly, which meant every Warn/Info diagnostic
	// catalog.go emits (failed pull, degraded/unverified transport
	// fallback, rejected pulled catalog, version regression, last-known-good
	// persistence failure) went to the same invisible-on-a-backgrounded-
	// gateway sink FIX 1 above fixes — but catalog.go is owned by another
	// agent and out of scope to edit here. capabilityCatalogLogAdapter
	// satisfies the same minimal (unexported, structurally-matched)
	// Warn/Info interface catalog.go declares — exactly the way
	// slog.Default() (a *slog.Logger, which also has Warn/Info methods)
	// already did — so no change to catalog.go is needed: passing a
	// differently-backed value that satisfies the same structural interface
	// is sufficient.
	capCatalog, catErr := capabilities.NewCatalog(
		capabilities.EmbeddedSeed(),
		capabilities.NewGHReleasePuller("elicify-ai", "omnipus", "providers_capabilities.json"),
		&capFileStore{path: filepath.Join(homePath, "capabilities_catalog.json")},
		capabilityCatalogLogAdapter{},
	)
	if catErr != nil {
		logger.WarnCF("gateway", "capability catalog construction failed; model modality detection degraded",
			map[string]any{"error": catErr})
	} else {
		agentLoop.SetCapabilityCatalog(capCatalog)
		// Start the 7-day background refresh (FR-025). Non-fatal: a pull
		// failure retains last-known-good and is logged but does not abort
		// the gateway or the refresh loop. Extracted into its own function
		// (rather than an inline goroutine literal) so the "refresh once
		// immediately, THEN wait for the ticker" ordering is independently
		// unit-testable without booting the whole gateway — see
		// capability_catalog_refresh_test.go.
		go runCapabilityCatalogRefreshLoop(
			capCatalog,
			capabilityCatalogRefreshInterval,
			capabilityCatalogRefreshTimeout,
		)
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

	// ADR-046 P1 (FR-007/008): execution is workspace-scoped, and the system
	// deliberately never auto-adds a custom/pre-existing agent to any
	// workspace team (FR-008 — no silent global-roster membership). That is
	// correct for a fresh install (ensureDefaultWorkspace seeds the built-in
	// roster) but means an operator upgrading an install with pre-existing
	// CUSTOM agents can end up with agents that silently cannot execute at
	// all until manually added via a workspace's Team tab — previously only
	// discoverable one per-turn refusal at a time. Surface the full list ONCE
	// at boot, after workspaces are ensured, so it's visible up front instead.
	logWorkspacelessAgents(homePath, cfg)

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

	// Register /preview/ (canonical web_serve URL) on the MAIN mux (ADR-044,
	// FR-001/FR-002/FR-003). There is no separate preview listener anymore —
	// /preview/ shares gateway.port with the SPA and /api/v1/*. It is
	// registered bare: no withAuth/session/CSRF/origin wrapping — the URL path
	// token is the credential (FR-023) — but it DOES inherit the global
	// configSnapshotMiddleware wrap applied below (FR-002: race-free live-config
	// reads). HandlePreview itself checks cfg.IsPreviewEnabled() live on every
	// request and 404s when disabled (FR-006) — no restart required to flip it.
	// All handlers live in rest_preview.go.
	api.registerPreviewEndpoints(runningServices.ChannelManager)

	// Omnipus start page — what a fresh browser tab opens instead of
	// about:blank. Registered bare (no auth) for the same structural reason as
	// /preview/: the client is the managed headless Chrome, which carries no
	// session cookie, and the page is static and non-sensitive. See
	// browser_start_page.go.
	api.registerBrowserStartPage(runningServices.ChannelManager)

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
		middleware.WithClientIPFunc(api.clientIPWithLiveFallback),
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
			stopAndCleanupServices(runningServices, 5*time.Second, false)
		}
	}()

	// Boot logging: main listener (ADR-044: /preview/ shares this same listener,
	// no separate preview port/address to log). preview_enabled is read live
	// (not restart-gated), so this line only reflects the value at boot time.
	mainAddr := fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port)
	slog.Info("gateway listening on " + mainAddr)
	if cfg.IsPreviewEnabled() {
		slog.Info("preview enabled: /preview/ served on the main listener")
	} else {
		slog.Info("preview disabled by config (gateway.preview_enabled=false)")
	}

	// Write port file so external callers (e.g. eval-runner) can discover the bound port.
	portFile := filepath.Join(cfg.AgentHomeBasePath(), "gateway.port")
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

	stateManager := state.NewManager(cfg.AgentHomeBasePath())
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

	// Start the orphan GC scheduler: runs Library.OrphanGC across every
	// workspace media library every hour (best-effort). A single failure
	// (e.g. corrupted manifest) does not abort the loop — the error is
	// logged and the next tick proceeds. Libraries with no orphan files
	// are a fast no-op.
	go func() {
		const orphanInterval = 1 * time.Hour
		ticker := time.NewTicker(orphanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a := agentLoop
				if a == nil {
					continue
				}
				wsFiles, wsErr := listWorkspaceFiles(homePath)
				if wsErr != nil {
					slog.Warn("orphan-gc: list workspaces failed", "error", wsErr)
					continue
				}
				for _, ws := range wsFiles {
					lib, libErr := library.New(homePath, ws.ID)
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
			a := agentLoop
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
		}
		for range ticker.C {
			sweep()
		}
	}()

	return runningServices, nil
}

// capFileStore implements capabilities.Store by persisting the catalog JSON
// to a single file on disk. It is a gateway-private type; the capabilities
// package defines the Store interface.
type capFileStore struct {
	path string
	mu   sync.Mutex
}

func (s *capFileStore) Read(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *capFileStore) Write(ctx context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fileutil.WriteFileAtomic(s.path, data, 0o600)
}

// capabilityCatalogRefreshInterval and capabilityCatalogRefreshTimeout are
// the production values for the FR-025 background refresh: pull at most
// once every 7 days, bound each individual pull attempt to 30s.
const (
	capabilityCatalogRefreshInterval = 7 * 24 * time.Hour
	capabilityCatalogRefreshTimeout  = 30 * time.Second
)

// capabilityCatalogLogAdapter routes capabilities.NewCatalog's minimal
// Warn(msg string, args ...any) / Info(msg string, args ...any) logger
// dependency through pkg/logger instead of log/slog.Default() (re-review
// FIX 1b — see the doc comment at this type's construction site). args
// follows log/slog's own alternating-key/value convention, since that is
// the shape catalog.go's call sites already use (they were written
// against slog.Logger's Warn/Info signature).
type capabilityCatalogLogAdapter struct{}

func (capabilityCatalogLogAdapter) Warn(msg string, args ...any) {
	logger.WarnCF("capabilities", msg, slogArgsToFields(args))
}

func (capabilityCatalogLogAdapter) Info(msg string, args ...any) {
	logger.InfoCF("capabilities", msg, slogArgsToFields(args))
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

// runCapabilityCatalogRefreshLoop performs an immediate refresh, then a
// refresh every interval thereafter, forever. It never returns — the sole
// caller (setupAndStartServices) invokes it in its own goroutine.
//
// The immediate call before the ticker loop is load-bearing, not
// cosmetic: Go's time.Ticker does NOT fire on creation, so a bare
// `for { select { case <-ticker.C: ... } } }` loop (the previous shape of
// this code, inlined at the call site) never invokes the puller until
// `interval` has elapsed — meaning any gateway restarted more often than
// that (dev pods, containers, k8s rolling deploys, systemd restarts) runs
// indefinitely on the build-time embedded seed and never refreshes at
// all. That contradicts both this package's own intent (FR-025) and
// catalog.go's doc comment, which promises a refresh "on gateway startup
// and every 7 days". A failed initial refresh does not prevent the ticker
// loop from starting: last-known-good (the embedded seed, on a fresh
// catalog) is retained and the failure is logged, exactly like any
// periodic refresh failure.
//
// Extracted as its own function (rather than an inline goroutine literal)
// specifically so this startup-ordering invariant is unit-testable
// without booting the whole gateway — see
// capability_catalog_refresh_test.go, which passes a very long interval
// and a fake Puller to prove the first Pull happens immediately rather
// than only after the (never-fired-in-the-test) ticker tick.
func runCapabilityCatalogRefreshLoop(cat *capabilities.Catalog, interval, refreshTimeout time.Duration) {
	refresh := func(failureLogMsg string) {
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		if err := cat.Refresh(ctx); err != nil {
			// Re-review FIX 1: this used to be a bare slog.Warn. slog.SetDefault
			// is never called anywhere in this repo, so log/slog.Default() sits
			// on its zero-value stderr-only handler — invisible on a
			// backgrounded gateway (the documented `./omnipus gateway
			// --allow-empty &` launch form), since nothing routes stderr into
			// $OMNIPUS_HOME/logs/gateway.log. Route through pkg/logger (the
			// same structured sink pkg/agent already uses, wired to
			// gateway.log via logger.EnableFileLogging at this file's own
			// EnableFileLogging call) so a refresh failure is actually
			// discoverable.
			logger.WarnCF("gateway", failureLogMsg, map[string]any{"error": err})
		}
	}

	refresh("gateway: capability catalog initial refresh failed; embedded seed retained")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		refresh("gateway: capability catalog refresh failed; last-known-good retained")
	}
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
			filepath.Join(filepath.Dir(cfg.AgentHomeBasePath()), "notifications"),
		)
	}
	var err error
	runningServices.CronService, err = setupCronTool(
		al,
		msgBus,
		cfg.AgentHomeBasePath(),
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
		triggerStorePath := filepath.Join(filepath.Dir(cfg.AgentHomeBasePath()), "tasks_triggers", "jobs.json")
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
		// Re-wire workspace-library provider onto the new store (same shared
		// AgentLoop cache as boot) so media://workspace/ refs keep resolving
		// after hot reload.
		fms.SetWorkspaceLibraryProvider(func(workspaceID string) (media.WorkspaceLibraryResolver, error) {
			lib := al.GetWorkspaceLibrary(workspaceID)
			if lib == nil {
				return nil, fmt.Errorf("workspace library unavailable for %q", workspaceID)
			}
			return lib, nil
		})
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
		// FIX 2: reload-time twin of the same warning in setupAndStartServices —
		// pair with logger.WarnCF for the same reason (invisible otherwise on a
		// backgrounded gateway; a reload can be the moment the last channel got
		// disabled, which is exactly when an operator most needs to notice).
		logger.WarnCF("gateway", "no channels enabled after reload — gateway has no reachable channel", nil)
	}

	// Stop the previous DeviceService before replacing it to avoid goroutine
	// leaks: the old service's goroutine would keep running with a dangling
	// pointer if we only overwrite the field.
	if oldDS := runningServices.DeviceService; oldDS != nil {
		oldDS.Stop()
	}
	stateManager := state.NewManager(cfg.AgentHomeBasePath())
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
