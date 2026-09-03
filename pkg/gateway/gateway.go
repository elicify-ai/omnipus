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
	"net"
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
	"github.com/elicify-ai/omnipus/pkg/agentstore"
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
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/heartbeat"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/policy"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/session"
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
	DeviceService    *devices.Service
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
// Provider APIKeyRef misses are NOT included here — and, since 2026-08-14,
// NOT because InjectFromConfig already aborts on them (it no longer does; a
// single unresolvable provider ref bricked whole installs, see
// reportInjectionErrors). They are excluded because a provider whose
// credential is genuinely absent from the vault (*credentials.NotFoundError)
// is handled end to end as a DEGRADED entry rather than a fatal one: ERROR in
// gateway.log at injection time, status reported through GET
// /api/v1/providers, and a startupBlockedProvider naming the missing
// credential if it is the default model's provider. Escalating the same ref
// again here would put back exactly the fatal boot this change removed.
// A provider whose credential fails for any OTHER reason — wrong master key,
// corrupted store entry — is NOT silently degraded: reportInjectionErrors
// keeps that fatal at injection time, so it never reaches "degraded
// provider" state and never needs to appear in this map at all.
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
	// MCP server env-var credential refs (BUG 4 / architect finding, closed
	// alongside the SEC-23-style migration in pkg/sysagent/tools/mcp.go and
	// pkg/gateway/rest.go's mcp-servers handlers): mirror the Enabled-gate
	// pattern above, at both the per-server level (srv.Enabled) and the
	// global kill-switch level (cfg.Tools.MCP.Enabled) — an MCP server whose
	// config is Enabled but sits under a globally-disabled tools.mcp.enabled
	// never actually connects, so its ref is not "in use" any more than a
	// disabled channel's is.
	//
	// NOTE: unlike every other category in this function, MCP refs are NOT
	// resolved by credentials.ResolveBundle — they are resolved by a wholly
	// separate pipeline (pkg/mcp.ResolveServerEnvRefs, invoked from
	// pkg/agent/loop_mcp.go's reconcileLocked at connect time, not at boot
	// credential-bundle time). That means marking a ref "in use" here has NO
	// effect on the ResolveBundle-error fatal/degraded classification this
	// map exists to drive (bootCredentials/executeReload, below) — recorded
	// here anyway for completeness/documentation. The actual sensitive-value
	// registration for MCP secrets (so they get scrubbed by
	// SensitiveDataReplacer) is done separately by
	// mcpEnabledEnvSensitiveValues, called from bootCredentials/executeReload
	// alongside cfg.RegisterSensitiveValues.
	//
	// Boot-time asymmetry (documented, not fixed — see mcpEnabledEnvSensitiveValues
	// and pkg/agent/loop_mcp.go's reconcileLocked): a dangling ref on an
	// ENABLED channel aborts boot fatally (the "fatal: enabled credential ...
	// not found" branch below); a dangling ref on an enabled+globally-enabled
	// MCP server does not — reconcileLocked logs a WARN and skips connecting
	// just that server, leaving the rest of boot to proceed normally. This
	// asymmetry predates this fix and is left in place deliberately (making
	// it fatal would be new boot-time behavior with its own blast radius —
	// out of scope for this pass).
	if cfg.Tools.MCP.Enabled {
		for _, srv := range cfg.Tools.MCP.Servers {
			if !srv.Enabled {
				continue
			}
			for _, ref := range srv.EnvRefs {
				if ref != "" {
					m[ref] = true
				}
			}
		}
	}
	return m
}

// mcpEnabledEnvSensitiveValues resolves the real (plaintext) value behind
// every EnvRefs credential reference belonging to a live MCP server —
// Enabled on the server AND the global tools.mcp.enabled kill-switch on,
// the same Enabled-gate buildEnabledRefMap's MCP loop uses above — and
// returns them for registration with cfg.RegisterSensitiveValues (BUG 4 /
// architect finding).
//
// Unlike the channel/voice/web-search/marketplace/mailbox categories, MCP
// env refs are not part of credentials.ResolveBundle's output (see the note
// in buildEnabledRefMap above), so there is no existing bundle this function
// can read from — it resolves each ref directly against the credential
// store. A resolution failure (locked store, deleted ref) is swallowed here:
// registering sensitive VALUES is this function's only job, and a dangling
// or unreadable ref simply contributes nothing to scrub — the connect-time
// failure itself is already surfaced (WARN + skip) by
// pkg/agent/loop_mcp.go's reconcileLocked.
func mcpEnabledEnvSensitiveValues(cfg *config.Config, store *credentials.Store) []string {
	if store == nil || cfg == nil || !cfg.Tools.MCP.Enabled {
		return nil
	}
	var values []string
	for _, srv := range cfg.Tools.MCP.Servers {
		if !srv.Enabled || len(srv.EnvRefs) == 0 {
			continue
		}
		for _, ref := range srv.EnvRefs {
			if ref == "" {
				continue
			}
			value, err := store.Get(ref)
			if err != nil || value == "" {
				continue
			}
			values = append(values, value)
		}
	}
	return values
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

// reportInjectionErrors logs every credentials.InjectFromConfig failure at
// ERROR and returns only the ones that must stop the caller (boot or reload).
//
// The split, and why it is not "everything is fatal" any more (2026-08-14,
// corrected 2026-08-15 — see the "wrong master key" note below):
//
//   - A *credentials.CredentialRefError whose Err unwraps to a
//     *credentials.NotFoundError is SCOPED to one config entry — one
//     provider's api_key_ref, one mailbox's password_ref genuinely is not in
//     the vault. It makes that ONE thing unusable. Treating it as fatal is
//     what bricked an install: a config.json carrying a leftover
//     onboarding-created provider entry (api_key_ref "openrouter_API_KEY")
//     whose credential was never stored made the gateway print "provider
//     credential injection failed" and exit on every start. The operator
//     could not reach the UI to delete the entry — the only recovery was
//     hand-editing config.json. One stale line of config must not cost the
//     whole application.
//
//   - Everything else is STORE-WIDE, and stays fatal: the bare
//     credentials.ErrStoreLocked (store never unlocked — short-circuited
//     before this function is even reached, see InjectFromConfig), AND —
//     this is the part the 2026-08-14 fix got wrong — a *CredentialRefError
//     whose Err is credentials.ErrWrongKey or a corrupted-entry decrypt
//     failure. UnlockWithKey performs NO verification against the stored
//     data, so a stale/rotated master.key or a drifted OMNIPUS_MASTER_KEY
//     unlocks cleanly (IsLocked() is false, the ErrStoreLocked short-circuit
//     never fires) and EVERY store.Get call then fails with ErrWrongKey —
//     wrapped, per ref, in a *CredentialRefError that looks identically
//     "scoped" to the NotFoundError case above. It is not: the cause is the
//     master key, not that one config entry, and every OTHER provider and
//     mailbox is equally dead even though only one happened to be checked
//     first. Degrading on the wrapper TYPE alone (any *CredentialRefError)
//     let a wrong master key boot as if a single stale provider were the
//     only casualty — silently serving with a broken vault, which is worse
//     than refusing to start. The discriminator has to be the Err field's
//     type, not the wrapper's type: only *NotFoundError degrades; ErrWrongKey,
//     store corruption, and any other cause (including an os.Setenv failure)
//     stay fatal. This exactly mirrors rest.go's
//     describeCredentialResolutionError, which classifies the same two cases
//     for the REST credential-resolution path.
//
// Unknown error shapes (neither *CredentialRefError nor recognized inside
// one) fall into the fatal bucket on purpose: a future failure mode nobody
// has classified yet stops the process loudly rather than being silently
// downgraded to a log line.
//
// This is a change in HOW LOUD, not in WHETHER we complain. Every degraded
// entry is logged at ERROR naming the scope, the owner and the credential, so
// it is unmissable in gateway.log; the provider is additionally reported as
// unusable through GET /api/v1/providers, and a default model whose credential
// is missing gets a startupBlockedProvider that says exactly that instead of
// an upstream 401 (see createStartupProvider). Nothing here degrades quietly.
//
// The channel-credential path (ResolveBundle, below) is deliberately NOT
// changed: a missing credential on an ENABLED channel remains fatal, because
// a channel silently not connecting is invisible to the operator in a way a
// provider in the Settings list is not.
func reportInjectionErrors(errs []error, phase string) []error {
	var fatal []error
	for _, e := range errs {
		var refErr *credentials.CredentialRefError
		if errors.As(e, &refErr) {
			var notFound *credentials.NotFoundError
			if errors.As(refErr.Err, &notFound) {
				slog.Error(
					phase+": credential unusable — the referencing entry will not work until it is fixed, "+
						"the rest of the system continues",
					"scope", refErr.Scope,
					"owner", refErr.Owner,
					"workspace", refErr.SubOwner,
					"credential_ref", refErr.Ref,
					"error", refErr.Err,
				)
				continue
			}
			// The ref IS configured but the cause is store-wide, not scoped
			// to this one entry — see the doc comment above. Treat it the
			// same as a locked store: fatal.
			slog.Error(
				phase+": credential store unreadable for a configured ref — "+
					"not a simple missing ref, treating as store-wide (wrong master key or "+
					"corrupted credential store), not a scoped failure",
				"scope", refErr.Scope,
				"owner", refErr.Owner,
				"workspace", refErr.SubOwner,
				"credential_ref", refErr.Ref,
				"error", refErr.Err,
			)
			fatal = append(fatal, e)
			continue
		}
		slog.Error(phase+": provider credential injection failed", "error", e)
		fatal = append(fatal, e)
	}
	return fatal
}

// bootCredentials runs the canonical credential + config boot sequence and
// returns the initialized config, secret bundle, and store.
//
// Sequence (matches ADR-004 §Boot Order Contract):
//  1. NewStore → Unlock (fatal on failure)
//  2. LoadConfigWithStore (fatal on failure)
//  3. InjectFromConfig for provider env-vars (fatal only on a store-wide
//     failure; a single unresolvable ref is an ERROR + degraded entry — see
//     reportInjectionErrors)
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
	//
	// A single unresolvable ref no longer aborts boot — it is logged at ERROR
	// and that provider/mailbox is left unusable, so the operator can start the
	// gateway and fix it in the UI. A store-wide failure is still fatal. See
	// reportInjectionErrors for the full rationale and the incident behind it.
	if errs := credentials.InjectFromConfig(cfg, credStore); len(errs) > 0 {
		if fatal := reportInjectionErrors(errs, "boot"); len(fatal) > 0 {
			return nil, nil, nil, fmt.Errorf(
				"fatal: provider credential injection failed — ensure OMNIPUS_MASTER_KEY is set and all referenced credentials exist: %w",
				errors.Join(fatal...),
			)
		}
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
	// ADR-068 FR-046/security paragraph (T068-32): stored device-code OAuth
	// tokens (openai_OAUTH, and once configured xai_OAUTH) are not part of
	// the config-ref-driven bundle above — nothing in config.json references
	// them — so a restart would otherwise leave a previously-signed-in
	// session's tokens unscrubbed until the next explicit sign-in. Fold them
	// in here too.
	values = append(values, providers.CollectOAuthSensitiveValues(credStore)...)
	// BUG 4 / architect finding: MCP server env-var secrets (resolved via
	// EnvRefs) were never registered for scrubbing at all — see
	// mcpEnabledEnvSensitiveValues's doc comment for why they cannot simply
	// ride along in `bundle` above.
	values = append(values, mcpEnabledEnvSensitiveValues(cfg, credStore)...)
	cfg.RegisterSensitiveValues(values)

	// Wire the shared credential store for CreateProviderFromConfig's
	// openai-chatgpt (device-code) dispatch — see
	// providers.SetDefaultCredentialStore's doc comment for why this
	// package-level seam exists instead of threading a *credentials.Store
	// through every CreateProviderFromConfig call site.
	providers.SetDefaultCredentialStore(credStore)

	return cfg, bundle, credStore, nil
}

// wireOAuthSensitiveValueRegistrar installs providers' sensitive-value
// registration hook (ADR-068 FR-046). See the call site in Run for why the
// seam exists and why the config is read through a getter rather than
// captured.
//
// getCfg returns the live config (nil-safe); store is the unlocked credential
// store. Errors are swallowed deliberately — this is housekeeping on a
// security control, and the alternative to a best-effort re-registration is
// no re-registration at all.
func wireOAuthSensitiveValueRegistrar(getCfg func() *config.Config, store *credentials.Store) {
	providers.SetSensitiveValueRegistrar(oauthSensitiveValueRegistrar(getCfg, store))
}

// oauthSensitiveValueRegistrar builds the closure wireOAuthSensitiveValueRegistrar
// installs. Split out so it can be exercised directly: installing it into
// providers' package-level seam makes it unreachable from a test, and a
// re-registration that silently registers nothing is exactly the failure this
// whole seam exists to prevent.
func oauthSensitiveValueRegistrar(getCfg func() *config.Config, store *credentials.Store) func(values ...string) {
	return func(values ...string) {
		if getCfg == nil || store == nil {
			return
		}
		cfg := getCfg()
		if cfg == nil {
			return
		}
		bundle, _ := credentials.ResolveBundle(cfg, store)
		complete := make([]string, 0, len(bundle)+len(values)+2)
		for _, v := range bundle {
			if v != "" {
				complete = append(complete, v)
			}
		}
		complete = append(complete, providers.CollectOAuthSensitiveValues(store)...)
		for _, v := range values {
			if v != "" {
				complete = append(complete, v)
			}
		}
		cfg.RegisterSensitiveValues(complete)
	}
}

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

func sweepOrphanedProviderCredentials(cfg *config.Config, store *credentials.Store, auditor *audit.Logger) {
	if cfg == nil || store == nil || store.IsLocked() {
		return
	}
	names, err := store.List()
	if err != nil {
		slog.Warn("gateway: credential sweep skipped: could not list credentials", "error", err)
		return
	}
	configured := make(map[string]struct{}, len(cfg.Providers))
	configuredVendors := make(map[string]struct{}, len(cfg.Providers))
	referenced := make(map[string]struct{}, len(cfg.Providers))
	for _, row := range cfg.Providers {
		if row == nil {
			continue
		}
		// M1: a SEEDED TEMPLATE row is not a configured provider and must
		// never populate the keep-set. pkg/config/defaults.go seeds ~10
		// permanent keyless template rows (model + api_base, no credential
		// ref, no auth_method) — including `{Provider: "openai"}`. Without
		// this filter `configuredVendors["openai"]` was populated on EVERY
		// install, so `openai_OAUTH` — the only OAuth grant the product
		// currently issues — was structurally unsweepable, and `<id>_API_KEY`
		// was unsweepable for every seeded id. The orphan this sweep exists
		// to reclaim (process dies between the config write and the
		// credential delete during provider removal, leaving a live access
		// AND refresh token with nothing in the UI referencing it) was
		// therefore precisely the orphan it declined to touch.
		//
		// isSeedTemplateRow (rest.go) is the SAME predicate every other
		// consumer of cfg.Providers applies — GET /providers' list branch
		// among them — so "configured" means one thing across the package.
		//
		// The filter narrows the id/vendor keep-sets ONLY. The `referenced`
		// keep-set below stays unconditional: a row carrying an api_key_ref
		// is by definition not a keyless template, but the belt-and-braces
		// "keep any name a row points at, whatever its shape" rule must not
		// acquire an exception — wrongly deleting a live secret is
		// unrecoverable, failing to sweep is harmless.
		if ref := strings.TrimSpace(row.APIKeyRef); ref != "" {
			referenced[ref] = struct{}{}
		}
		id := strings.TrimSpace(row.Provider)
		if id == "" {
			continue
		}
		seedShaped := isSeedTemplateRow(row)
		if !seedShaped {
			configured[id] = struct{}{}
		}
		// The OAUTH keep-set needs a NARROWER filter than the API_KEY one,
		// and the asymmetric-risk rule above is why.
		//
		// A sign_in row legitimately has no api_key_ref, no api_base and no
		// models — a sign-in provider authenticates with a vendor session,
		// not a key — so it can be seed-SHAPED while being a real,
		// operator-configured row holding a live OAuth grant. Filtering the
		// vendor keep-set on seed shape alone would let the boot sweep
		// delete that grant, which is unrecoverable and strictly worse than
		// the orphan M1 set out to reclaim. (The first version of this fix
		// did exactly that; TestCredentialSweep_OrphanedOAuthEntries caught
		// it.)
		//
		// A row is therefore kept out of the vendor keep-set only when it is
		// seed-shaped AND its id maps to its own vendor identity. That
		// second clause is exactly what the shipped seed cannot satisfy for
		// the one grant that matters: `openai_OAUTH` belongs to vendor
		// `openai`, reached only from the sign-in row `openai-chatgpt`
		// (OAuthVendorID maps it), never from the seeded api-key row
		// `openai` (which maps to itself). So the seeded template stops
		// shielding `openai_OAUTH` — the M1 defect — while every row that
		// could actually own an OAuth entry still protects it.
		//
		// A row that declares auth_method sign_in is never seed-shaped
		// (isSeedTemplateRow tests AuthMethod), so real sign-in rows are
		// covered by the ordinary path regardless of their id mapping.
		if !seedShaped || providers.OAuthVendorID(id) != id {
			// A vendor entry can back MORE THAN ONE row (openai-chatgpt and
			// any future OpenAI-family sign-in row share `openai_OAUTH`), so
			// the keep-set is keyed on the vendor, not the row id.
			configuredVendors[providers.OAuthVendorID(id)] = struct{}{}
		}
	}
	for _, name := range names {
		id, sweepable := sweepableOrphanCredential(name, configured, configuredVendors, referenced)
		if !sweepable {
			continue
		}
		if err := store.Delete(name); err != nil {
			var nf *credentials.NotFoundError
			if errors.As(err, &nf) {
				continue // already gone — absence is success (FR-010 step 3 posture)
			}
			slog.Warn("gateway: credential sweep: could not delete orphaned credential",
				"credential_ref", name, "error", err)
			continue
		}
		// The one INFO line per swept orphan — ref NAME only, never the value.
		slog.Info("gateway: swept orphaned provider credential",
			"credential_ref", name, "provider_id", id)
		if auditor != nil {
			if err := auditor.Log(&audit.Entry{
				Event:    EventProviderCredentialSwept,
				Decision: audit.DecisionAllow,
				Details: map[string]any{
					"provider":       id,
					"credential_ref": name,
				},
			}); err != nil {
				slog.Warn("audit write failed", "event", EventProviderCredentialSwept, "error", err)
			}
		}
	}
}

// oauthEntrySuffix is the suffix credentials.OAuthEntryName appends, derived
// from that function itself (it takes the vendor id as its only argument, so
// the empty id yields the bare suffix) rather than restated as a literal — a
// rename there cannot silently desync this sweep.
var oauthEntrySuffix = credentials.OAuthEntryName("")

// sweepableOrphanCredential decides whether a credential-store entry name is
// an orphaned provider secret the boot sweep may delete, and returns the
// provider/vendor label to log and audit it under. Everything that is not
// unambiguously an orphan is left alone — see the rules and the asymmetric
// risk argument in sweepOrphanedProviderCredentials' doc comment.
func sweepableOrphanCredential(name string, configured, configuredVendors, referenced map[string]struct{}) (string, bool) {
	// A name any provider row's api_key_ref points at is kept whatever its
	// shape — belt-and-braces for a row whose ref was renamed by hand.
	if _, ok := referenced[name]; ok {
		return "", false
	}
	if id, ok := strings.CutSuffix(name, "_API_KEY"); ok {
		if !isProviderCredentialID(id) {
			return "", false
		}
		if _, cfgd := configured[id]; cfgd {
			return "", false
		}
		return id, true
	}
	if vendor, ok := strings.CutSuffix(name, oauthEntrySuffix); ok {
		if !isProviderCredentialID(vendor) {
			return "", false
		}
		if _, backed := configuredVendors[vendor]; backed {
			return "", false
		}
		return vendor, true
	}
	return "", false
}

// isProviderCredentialID reports whether id has the shape of a provider row
// id as written by onboarding and PUT /providers/{id} (catalog ids are
// models.dev slugs — lowercase letters, digits, '-', '.', '_' — and the
// contract caps ids at 64 chars). Uppercase prefixes are OUT by design: they
// belong to the integration refs (BRAVE_API_KEY, …), which are not provider
// credentials. A custom row id containing uppercase is simply never swept —
// the conservative direction for housekeeping.
func isProviderCredentialID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
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
//   - systools.AllTools(nil) has no equivalent metadata-only catalog
//     function, but is safe to call for name-harvesting alone: every
//     constructor in pkg/sysagent/tools does nothing but store the *Deps
//     pointer it is given (never dereferenced at construction time), and
//     every tool's Name() method is a static string literal that never reads
//     deps. Passing nil deps is therefore safe PROVIDED the returned tools
//     are never Execute()d here — and they never are; only .Name() is called
//     below.
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
		for _, t := range systools.AllTools(nil) {
			out[t.Name()] = struct{}{}
		}
		// ADR-052 (autonomous agent plan execution, FR-027) — the four
		// planning/verifier tool names (create_plan, execute_plan, run_task,
		// inspect_session) are unioned in explicitly here, independent of
		// their pkg/tools|pkg/sysagent/tools implementation landing, so the
		// tool-policy coverage universe (config.ValidateToolPolicyCoverage /
		// RepairIncompleteToolPolicyCoverage) recognizes them from the
		// config-seeding side immediately. Mirrors
		// pkg/coreagent/core.go's allStaticToolNames literal-for-literal
		// (TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog
		// enforces the two stay in sync). Once the real tool implementations
		// register themselves via GeneralBuiltinMetadata/AllTools, this union
		// is idempotent (same names, no duplicate entries in a set).
		for _, name := range []string{"create_plan", "execute_plan", "run_task", "inspect_session"} {
			out[name] = struct{}{}
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
	// ADR-071 §5.3.5a: this migration MUST run FIRST, before
	// RepairIncompleteToolPolicyCoverage below — not merely before the
	// validator. The repair backfills any (agent, tool) pair with no policy
	// entry to an explicit "deny" (correct, fail-closed, for its own
	// purpose), and ToolSearch/switch_agent are new names with no policy
	// entry anywhere until this migration folds the retired load_tool /
	// hand_off / return_to_default keys forward. Sequenced any later, the
	// first post-upgrade boot would silently deny both on every agent —
	// boot succeeds with no gap and no abort, and every agent silently
	// loses hand-off (and, after D3, ToolSearch denies 71% of the catalog).
	if config.MigrateLegacyToolPolicyKeys(cfg) {
		slog.Info("gateway: migrated legacy tool-policy keys to their ADR-071 replacements",
			"migrations", "load_tool->ToolSearch, hand_off/return_to_default->switch_agent",
		)
	}
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

// errEgressProxyDisabled is the sentinel returned by buildEgressProxyOrAbort
// when construction failed but no sandbox.egress_allow_list was configured
// (the operator never opted into egress restriction). Callers use
// errors.Is(err, errEgressProxyDisabled) to distinguish this non-fatal,
// log-and-continue outcome from a real boot-abort error — a plain (nil, nil)
// return would leave both states indistinguishable to the caller (nilnil).
var errEgressProxyDisabled = errors.New("gateway: egress proxy disabled (construction failed with no sandbox.egress_allow_list configured)")

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
		return nil, errEgressProxyDisabled
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

// seedSystemAgentEagerSouls eagerly backfills EVERY seeded System Agent's
// SOUL.md with its compiled default soul (coreagent.SystemAgentDefaultSoul —
// JudgeDefaultRubric for the Judge, PlanSupervisorDefaultRubric for the
// PlanSupervisor) at gateway boot, right after coreagent.SeedConfig has
// ensured their AgentConfig entries exist.
//
// It iterates coreagent.SystemAgents() rather than naming ids, so adding a
// System Agent with a default soul needs no edit here — a previous version
// looped for the Judge alone, which is precisely how the PlanSupervisor's
// rubric ended up existing only as a Go constant that never reached disk.
//
// FOR THE JUDGE this fixes an operator-reported UX gap: its soul used to
// materialize ONLY lazily, on its first real verifier dispatch (pkg/agent's
// ensureVerifierSoul) — but the soul is now operator-editable in the SPA
// (judge_soul_editable_test.go), so a fresh install's Judge profile must show
// the default standards immediately, not stay blank until the operator has
// already triggered a judgment.
//
// FOR THE PLANSUPERVISOR this is not a UX nicety but the ONLY seed path
// (plan-supervisor-spec FR-005 rev 2 deliberately adds no lazy backstop: the
// Judge's backstop is Judge-gated and sits on the verifier-dispatch path,
// which a bus-woken PlanSupervisor never reaches). If this call does not
// fire, the adjudicator wakes with an EMPTY prompt.
//
// This call site — pkg/gateway's boot sequence — was chosen over folding
// the write into coreagent.SeedConfig/seedSystemAgents themselves for two
// independent reasons, both already true of the pre-existing lazy seed
// (see ensureVerifierSoul's doc comment, verifier_adjudication.go):
//
//  1. coreagent.SeedConfig is documented, and relied on by its own test
//     suite (none of which sets OMNIPUS_HOME), as a PURE config-struct
//     mutation with zero filesystem side effects. Adding a disk write there
//     would start silently touching the real machine's home directory on
//     every `go test ./pkg/coreagent/...` run.
//  2. pkg/coreagent cannot cleanly resolve a System Agent's REAL workspace
//     path itself — that resolution (OMNIPUS_HOME lookup, ID sanitization,
//     traversal guarding) lives in agent.ResolveAgentHome, and
//     pkg/coreagent cannot import pkg/agent
//     (pkg/agent already imports pkg/coreagent — that direction would be a
//     cycle). Reimplementing the resolution a second time in pkg/coreagent
//     would be a second source of truth that could silently drift from the
//     path the agent's real AgentInstance.Home resolves to at runtime.
//
// pkg/gateway already imports both pkg/agent and pkg/coreagent, so it is
// the cleanest place that can call the real, single-source-of-truth
// agent.ResolveAgentHome and land each seed at EXACTLY the directory that
// agent's own AgentInstance will later use — then delegates the actual
// write (mkdir + backfill-only-when-missing/empty + atomic write) to
// agent.SeedSystemAgentSoulFile, the same helper ensureVerifierSoul uses, so
// the call sites can never diverge on write semantics. In particular the
// "never overwrite existing non-empty content" rule lives THERE, which is
// what keeps this safe to run on every boot: an operator's edited soul
// survives a restart untouched, exactly like the identity/type/locked/
// tool-policy re-enforcement in seedSystemAgents leaves Model/Provider alone.
//
// Non-fatal per agent: a failure is logged at WARN and boot continues, and
// one agent's failure never skips the rest — an empty soul degrades that
// agent, it is not a boot-blocking condition.
func seedSystemAgentEagerSouls(cfg *config.Config) {
	for _, sa := range coreagent.SystemAgents() {
		if strings.TrimSpace(coreagent.SystemAgentDefaultSoul(sa.ID)) == "" {
			// A System Agent with no compiled default soul has nothing to
			// backfill (its prompt comes from elsewhere). Skipping here keeps
			// SeedSystemAgentSoulFile's "no default soul" error a real,
			// loud misconfiguration signal for other callers instead of a
			// WARN this loop would emit on every boot forever.
			continue
		}
		idx := -1
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == string(sa.ID) {
				idx = i
				break
			}
		}
		if idx < 0 {
			// coreagent.SeedConfig runs immediately before this and seeds
			// every System Agent, so a missing entry means the roster and the
			// System-Agents list have diverged — loud, because for the
			// PlanSupervisor this loop is the ONLY path that gives it a prompt.
			slog.Warn("gateway: System Agent missing from roster; default soul not seeded",
				"agent_id", string(sa.ID))
			continue
		}
		home := agent.ResolveAgentHome(&cfg.Agents.List[idx], &cfg.Agents.Defaults)
		if err := agent.SeedSystemAgentSoulFile(home, sa.ID); err != nil {
			slog.Warn("gateway: could not eagerly seed System Agent default soul",
				"error", err, "agent_id", string(sa.ID), "workspace", home)
		}
	}
}

// lastNonEmptyRosters remembers, per home directory, the most recently
// observed NON-EMPTY agent roster loaded by
// populateAgentsListFromEntityStoreStrict. It backs that function's
// regression guard: a fresh entity-store List() that comes back EMPTY for a
// home directory that previously yielded a real roster is treated as a hard
// failure rather than silently wiping the in-memory roster — see that
// function's doc comment for the full rationale (an empty roster does not
// merely mean "nothing to route to"; it promotes ALL traffic to an
// unrestricted fallback agent).
//
// Keyed by homePath (never a single global) so multiple *config.Config
// instances/tests rooted at different homes cannot cross-contaminate each
// other's remembered roster. Process-lifetime only, in-memory — a
// genuinely fresh process/home combination has no entry yet, so its first
// (legitimately empty, pre-SeedConfig) population never trips the guard.
var (
	lastNonEmptyRostersMu sync.Mutex
	lastNonEmptyRosters   = map[string][]config.AgentConfig{}
)

// forgetRosterBaseline drops the remembered non-empty roster for homePath so
// the next populateAgentsListFromEntityStoreStrict call will not treat a
// legitimately-shrunk roster as a regression.
//
// WHY THIS IS NEEDED, and why the guard alone is not enough: the regression
// guard cannot distinguish "the store broke and handed back nothing" from
// "the operator deleted the last agent" — both look like non-empty -> empty,
// and an on-disk file count does not separate them either (a homePath that
// resolves to the WRONG directory also reports zero records, which is the
// precise failure the guard exists to catch). The authority on an INTENTIONAL
// shrink is the mutation path, so deleteAgent tells the guard rather than the
// guard trying to infer it.
//
// Without this, deleting the LAST agent wedged the running gateway: the entity
// record was removed from disk, the post-delete reload was rejected by the
// guard, and the in-memory roster kept serving the deleted agent until a
// restart — permanent divergence between disk and memory. Regression coverage:
// TestHandleAgentsDelete_OK (rest_clidetect_test.go) deletes the only agent and
// asserts the subsequent GET is 404.
//
// The narrow trade-off is deliberate: if the store ALSO fails during the very
// next reload after an intentional delete, that one reload accepts an empty
// roster instead of rejecting it. The baseline re-establishes on the following
// successful load, and an operator-initiated delete is a far weaker signal of
// compromise than an unexplained disappearance.
func forgetRosterBaseline(homePath string) {
	lastNonEmptyRostersMu.Lock()
	delete(lastNonEmptyRosters, homePath)
	lastNonEmptyRostersMu.Unlock()
}

// populateAgentsListFromEntityStore — the legacy void, log-and-continue
// bridge between the per-entity agent store (entities/agents/<id>.json) and
// cfg.Agents.List — was DELETED (RELEASE BLOCKER security-fix follow-up,
// 2026-07-26). It was kept only for pkg/gateway/rest.go's
// populateAgentsListFromStore and rest_pending_restart.go's
// HandlePendingRestart, which were out of this security-fix pass's original
// file-ownership scope; once those call sites were fixed to call the strict,
// fail-closed populateAgentsListFromEntityStoreStrict directly (same package,
// no export needed) and reject on error instead of silently proceeding with
// whatever roster the entity store handed back, nothing in the codebase
// called this lenient wrapper anymore (verified: `grep -rn
// "populateAgentsListFromEntityStore("` finds only the strict variant's own
// definition). See populateAgentsListFromEntityStoreStrict's doc comment
// immediately below for the full privilege-escalation rationale this
// wrapper's removal closes off entirely rather than leaving as a
// still-reachable, silently-permissive code path.

// populateAgentsListFromEntityStoreStrict is populateAgentsListFromEntityStore's
// fail-closed variant. It returns a non-nil error whenever the entity
// store's state cannot be trusted enough to safely (re)populate
// cfg.Agents.List, and on ANY error path it leaves cfg.Agents.List and
// cfg.SkippedAgentIDs COMPLETELY UNTOUCHED — callers own the decision of
// what "cannot trust this" means for them (boot aborts; a reload rejects the
// candidate config and marks the service degraded via
// (*services).markReloadDegraded rather than swapping it in).
//
// This distinction matters far more than it looks: an EMPTY cfg.Agents.List
// does not merely mean "no agent to route a message to". Verified
// 2026-07-26 as a real privilege-escalation chain, not a theoretical one.
//
// The chain's ENTRY POINT is now closed: NewAgentRegistry used to ALWAYS
// register an unrestricted "main" sentinel AgentConfig carrying no
// Tools/Policies at all, and that agent has been removed — the registry now
// contains only agents from cfg.Agents.List, each of which the coverage gate
// does validate. The MECHANISM it exploited is unchanged and still worth
// guarding: pkg/tools/compositor.go's global×agent policy merge
// (resolveEffectivePolicyWith) falls through to the GLOBAL floor for every
// tool an agent has no per-agent policy entry for — which was every tool,
// for that sentinel. pkg/config/defaults.go seeds that global floor "allow"
// for bash, write_file, edit_file, delegate, send_email, and more. So a
// wiped roster silently promotes ALL routed traffic (via
// AgentRegistry.GetDefaultAgent's fallback ladder) to an unrestricted
// agent — and repairAndValidateToolPolicyCoverage (this file), which walks
// cfg.Agents.List to find coverage gaps, finds ZERO agents to check and
// vacuously PASSES an empty roster, so the existing coverage gate does not
// catch this at all. Silently limping on with whatever (potentially empty)
// roster the entity store handed back — the historical behavior, preserved
// only in the legacy populateAgentsListFromEntityStore wrapper above — is
// therefore never acceptable from a fresh call site.
//
// Three independent failure classes are rejected here:
//
//  1. A genuine entity.Store.List() error (e.g. EMFILE/ENFILE under fd
//     pressure, EACCES after a restore with the wrong ownership, EIO,
//     entities/agents shadowed by a regular file) — propagated directly.
//     This is DIFFERENT from "the directory does not exist yet", which
//     entity.Store.List() maps to (nil, nil, nil): a genuine fresh-install
//     state, not an error.
//  2. Every on-disk agent record failed to parse (List() succeeds, but
//     every id it found landed in `skipped`, none in `agents`) — e.g. a
//     breaking schema change. total := len(agents)+len(skipped) is the true
//     on-disk record count (every id List() finds lands in exactly one of
//     the two); total > 0 with zero LOADED agents must never be treated as
//     "fresh install, nothing configured" (total == 0 is the genuine
//     fresh-install case and is unaffected).
//  3. A regression within this process's own lifetime: homePath previously
//     yielded a non-empty roster (tracked in lastNonEmptyRosters) and this
//     call now yields an empty one. A genuinely fresh process/home
//     combination never has a prior entry, so this cannot fire on a real
//     first boot — it only fires on a live process observing its own
//     roster apparently disappear, e.g. homePath momentarily/incorrectly
//     resolving to the wrong directory (see setupConfigWatcherPolling's
//     homePath-threading fix) or a transient store hiccup that happened to
//     return a clean empty list instead of a class-1 error.
//
// Also closes the ADR-054-era normalization gap: entity-loaded agents never
// pass through loadConfigInternal's own NormalizeFallbacks /
// migrateAgentPrimaryProvider passes (those only run against config.json's
// agents.list inside config.LoadConfig*, which is stripped to empty before
// this bridge ever runs) — config.NormalizeAgentRoster applies both to the
// roster on every successful load here so an agent whose FallbackModel/
// primary-model fields were written pre-split still resolves correctly.
func populateAgentsListFromEntityStoreStrict(cfg *config.Config, homePath string) error {
	agents, skipped, err := agentstore.New(homePath).List()
	if err != nil {
		logger.Errorf("gateway: agent entity store list failed at %q: %v", homePath, err)
		return fmt.Errorf("gateway: could not list agent entity records at %q: %w", homePath, err)
	}

	if total := len(agents) + len(skipped); total > 0 && len(agents) == 0 {
		logger.Errorf("gateway: agent entity store at %q has %d on-disk record(s), all %d "+
			"unparseable — refusing to treat this as a fresh install", homePath, total, len(skipped))
		return fmt.Errorf(
			"gateway: entity store at %q has %d on-disk agent record(s), all %d unparseable — "+
				"refusing to treat this as a fresh install with zero agents",
			homePath, total, len(skipped),
		)
	}

	if len(agents) == 0 {
		lastNonEmptyRostersMu.Lock()
		previous := lastNonEmptyRosters[homePath]
		lastNonEmptyRostersMu.Unlock()
		if len(previous) > 0 {
			logger.Errorf("gateway: agent entity store at %q returned an EMPTY roster where a "+
				"NON-EMPTY roster (%d agents) was previously loaded for this home — refusing to "+
				"overwrite the in-memory roster", homePath, len(previous))
			return fmt.Errorf(
				"gateway: entity store at %q returned an EMPTY roster where a NON-EMPTY roster "+
					"(%d agents) was previously loaded for this home", homePath, len(previous),
			)
		}
	}

	cfg.Agents.List = agents
	cfg.SkippedAgentIDs = skipped
	config.NormalizeAgentRoster(cfg)

	if len(agents) > 0 {
		rosterCopy := make([]config.AgentConfig, len(agents))
		copy(rosterCopy, agents)
		lastNonEmptyRostersMu.Lock()
		lastNonEmptyRosters[homePath] = rosterCopy
		lastNonEmptyRostersMu.Unlock()
	}
	return nil
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

// persistSeededCoreAgents persists every agent SeedConfig added-or-touched
// via the agent store: Create for one with no existing record, Update (full-
// record replace) for one that already has one — matching SeedConfig's own
// "re-enforce identity fields on existing core agents" semantics. Extracted
// from RunContextWithOptions as its own function so the fix below (a single
// corrupt/unparseable entity record must degrade, never abort boot —
// ADR-054 D7 + §0 R3) is directly unit-testable without spinning up the
// full boot sequence (credentials, providers, agent loop).
//
// store.Get's error is explicitly classified rather than treated as a bare
// "absent" signal: gating solely on "any error means create" (the previous
// behavior) mis-handled a PARSE error (corrupt entities/agents/<id>.json)
// identically to "record does not exist yet" — store.Create then hit
// entity.ErrAlreadyExists (the file DOES exist, it just didn't parse) and
// that error was propagated as a hard boot-abort. One unparseable agent
// record made the entire gateway unbootable, inverting ADR-054's own D7
// ("unparseable record -> skip + ERROR + mark degraded") and §0 R3, which
// explicitly rejected fail-closed here because a single corrupt file
// dropping ALL inbound traffic has no in-product repair path. Only a true
// entity.ErrNotFound now takes the create path; anything else (a corrupt
// record, a permission error, etc.) is skipped with an ERROR log so boot
// continues — the entity's on-disk record is left exactly as it was rather
// than being clobbered by a Create attempt that would only fail anyway.
func persistSeededCoreAgents(homePath string, agents []config.AgentConfig) error {
	store := agentstore.New(homePath)
	for i := range agents {
		seeded := agents[i]
		_, getErr := store.Get(seeded.ID)
		switch {
		case getErr == nil:
			// Record exists and parsed fine — re-enforce identity fields.
			if _, updateErr := store.Update(seeded.ID, func(existing *config.AgentConfig) error {
				*existing = seeded
				return nil
			}); updateErr != nil {
				return fmt.Errorf("gateway: failed to persist seeded core agent %q: %w", seeded.ID, updateErr)
			}
		case errors.Is(getErr, entity.ErrNotFound):
			// No record on disk yet — create it.
			if createErr := store.Create(seeded.ID, &seeded); createErr != nil {
				return fmt.Errorf("gateway: failed to persist seeded core agent %q: %w", seeded.ID, createErr)
			}
		default:
			// Get failed for a reason OTHER than "not found" — most commonly a
			// corrupt/unparseable record. Skip re-seeding this one agent rather
			// than aborting the whole boot; see this function's doc comment.
			logger.Errorf("gateway: seeded core agent %q record exists but could not be read "+
				"(corrupt/unparseable?) — skipping re-seed for this agent; boot continues degraded "+
				"for this agent only: %v", seeded.ID, getErr)
		}
	}
	return nil
}

// persistFreshInstallDefaultAgentID writes agents.defaults.default_agent_id
// into config.json's raw JSON map, preserving every other key exactly as-is —
// unlike config.SaveConfig, which round-trips the whole typed Config struct
// and can clobber SecureString-backed API keys (CLAUDE.md hard rule: "NEVER
// use config.SaveConfig() — it corrupts API keys"). Mirrors
// pkg/gateway/rest.go's updateConfigJSONLocked/ensureMap read-modify-write
// convention. Called exactly once, at boot, immediately after
// coreagent.SeedConfig sets this field in memory on a genuinely fresh
// install (SeedConfig itself performs no file I/O by design) — see the call
// site's doc comment for why this durability step cannot live inside
// SeedConfig or persistSeededCoreAgents.
func persistFreshInstallDefaultAgentID(configPath, agentID string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return fmt.Errorf("parse config: %w", unmarshalErr)
	}
	ensureMap(m, "agents", "defaults")["default_agent_id"] = agentID
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}
	if err := fileutil.WriteFileAtomic(configPath, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

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
	if chainKey, derrErr := credStore.DeriveSubkey(audit.AuditChainKeyInfo); derrErr == nil {
		audit.SetProcessChainKey(chainKey)
	} else {
		slog.Warn("audit: could not derive HMAC chain key from master key — "+
			"audit.NewLogger will fail closed below if sandbox.audit_log is enabled",
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
	// ADR-068 FR-020: the startup provider's model id is NEVER written back
	// into agents.defaults.default_model. The old `ModelName == ""` back-fill
	// guard that lived here was deleted with the alias — the pair is written
	// only by onboarding completion and the default-model PUT.
	provider, _, err := createStartupProvider(cfg, allowEmptyStartup)
	if err != nil {
		return fmt.Errorf("error creating provider: %w", err)
	}

	// ADR-054 D2/D3: bring in any agents already persisted as entity records
	// (entities/agents/<id>.json) from a previous run BEFORE SeedConfig looks
	// at cfg.Agents.List to decide which core agents are "already present" —
	// otherwise every boot would look like a fresh install (cfg.Agents.List
	// starts empty: config.LoadConfig strips config.json's legacy agents.list
	// unconditionally, see legacy_agents_list.go) and SeedConfig would
	// re-create core agents from their seed defaults on every restart,
	// discarding any operator customization. Strict variant: a roster-
	// population failure here (genuine store error, every on-disk record
	// unparseable, or a same-process non-empty→empty regression) must abort
	// boot rather than silently proceed with an empty/partial roster — see
	// populateAgentsListFromEntityStoreStrict's doc for the verified
	// privilege-escalation chain an empty roster otherwise opens up.
	if rosterErr := populateAgentsListFromEntityStoreStrict(cfg, homePath); rosterErr != nil {
		return fmt.Errorf("gateway: could not populate agent roster from entity store at boot: %w", rosterErr)
	}

	// Seed core agents into config on first boot. Core agents are stored in
	// cfg.Agents.List with Locked=true so they appear alongside custom agents
	// in the REST API with type "core". SeedConfig is idempotent — it only adds
	// agents that are not already present (checked by ID).
	if coreagent.SeedConfig(cfg) {
		// ADR-054 D2/§11: core agents now persist as entity records
		// (entities/agents/<id>.json) — never back into config.json's
		// agents.list. config.SaveConfig here would be a double violation:
		// (a) it is the full-struct save CLAUDE.md forbids for exactly this
		// reason ("corrupts API keys" via SecureString round-trip), and (b)
		// anything it wrote to agents.list would be stripped again on the
		// very next config.LoadConfig call, so it would not even survive.
		// persistSeededCoreAgents persists every agent SeedConfig
		// added-or-touched (its own "re-enforce identity fields on existing
		// core agents" pass, and any brand-new core agent it appended) via
		// the agent store — see its own doc comment for the corrupt-record
		// handling that makes this safe against a single bad entity file.
		if seedErr := persistSeededCoreAgents(homePath, cfg.Agents.List); seedErr != nil {
			return seedErr
		}
	}

	// RELEASE BLOCKER fix follow-up (2026-07-26): on a genuinely fresh
	// install, coreagent.SeedConfig also sets the settings singleton
	// (cfg.Agents.Defaults.DefaultAgentID = "mia") — the ONLY field
	// agent.AgentRegistry.GetDefaultAgent and pkg/routing's
	// resolveDefaultAgentID consult (ADR-054 D6.4). SeedConfig is a pure
	// in-memory struct mutation by design, with zero filesystem side effects
	// (mirrors why seedSystemAgentEagerSouls below is a separate call site
	// rather than living inside SeedConfig itself) — so without persisting it here,
	// the singleton lives only in THIS process's in-memory cfg. NewAgentLoop
	// immediately below reads it fine, so THIS boot resolves correctly, but
	// config.json on disk never gets it: the very next restart reloads an
	// empty default_agent_id, isFreshInstall is now false (agents already
	// exist), SeedConfig never re-seeds it, and the two resolution ladders'
	// differing Priority-2 fallbacks silently disagree again — reopening the
	// exact bug this fix closes, just delayed by one restart. Persist it
	// directly into config.json's raw JSON map (mirrors rest.go's
	// updateConfigJSONLocked/ensureMap convention — never config.SaveConfig,
	// which round-trips the whole typed struct and can clobber
	// SecureString-backed API keys, CLAUDE.md's hard rule). Runs once, here,
	// strictly before the config-file watcher starts later in this function,
	// so there is no self-write-registry race to account for. Best-effort:
	// a write failure only means the in-memory resolution (correct for this
	// boot) does not survive a restart — not a boot-time fatal, since the
	// gateway is otherwise fully healthy.
	if cfg.Agents.Defaults.DefaultAgentID != "" {
		if persistErr := persistFreshInstallDefaultAgentID(configPath, cfg.Agents.Defaults.DefaultAgentID); persistErr != nil {
			slog.Warn("gateway: could not persist fresh-install default_agent_id to config.json; "+
				"in-memory resolution is correct for this boot but will not survive a restart",
				"default_agent_id", cfg.Agents.Defaults.DefaultAgentID, "error", persistErr)
		}
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
	seedSystemAgentEagerSouls(cfg)

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
	providerCatalog := catalog.Boot(
		context.Background(),
		catalog.EmbeddedSnapshot,
		catalog.NewGHReleasePuller(),
		catalog.NewFileStore(homePath),
		catalogLogAdapter{},
	)
	agent.SetWindowCatalog(providerCatalog)
	// ADR-067 FR-012: the provider FACTORY dispatches on the protocol this
	// same document carries, so it must read the same instance — otherwise
	// the gateway would resolve windows from the pulled document while
	// constructing transports from the embedded snapshot.
	providers.SetCatalog(providerCatalog)

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

	// T068-10 (ADR-068 FR-010 last clause): startup sweep of orphaned
	// `<id>_API_KEY` credentials. Config is loaded and the store unlocked
	// (bootCredentials above); this is the earliest point where the audit
	// logger exists, so the sweep's provider.credential_swept entries land
	// in the real audit chain. Synchronous and fast (one store read, at
	// most a handful of deletes); never fatal.
	sweepOrphanedProviderCredentials(cfg, credStore, agentLoop.AuditLogger())

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
	wireOAuthSensitiveValueRegistrar(func() *config.Config { return agentLoop.GetConfig() }, credStore)

	// Install the same catalog instance on the agent loop: the presentation
	// gate (ADR-067 FR-004) and ResolveWindow's rung 5 read one document, and
	// setupAndStartServices reads it back from here for the REST surface and
	// the refresh loop.
	agentLoop.SetCapabilityCatalog(providerCatalog)

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
	agent.SetLiveWindowLookup(
		newLiveLimitsForBoot(homePath, credStore, agentLoop, reloadOnLiveWindow(agentLoop)).Lookup)

	// FR-007 / US-2.AC2: an agent on a `locality: local` row the catalog
	// cannot size resolved UNKNOWN above (the rung was not installed yet) and
	// every one of its turns is refused with context_window_unknown. Ask the
	// rung once for exactly that population, in the background and off the
	// boot path; when the endpoint answers, reloadOnLiveWindow rebuilds those
	// agents with the window it reported. Without this a fresh Ollama install
	// stays refused indefinitely and the refusal points the operator at a
	// control (Settings → Models → Model overrides) they should not have
	// needed. Not a timer: one pass, only for agents that are already broken.
	go primeUnknownWindows(agentLoop)

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
	if pool := agentLoop.BrowserPool(); pool != nil {
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
		go runBrowserCacheTrimSchedule(ctx, pool)
		go func() {
			path, ppErr := pool.Preprovision(ctx)
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
	for _, t := range systools.AllTools(nil) {
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
	// Wire the credential-store resolver so ReconcileMCP can resolve
	// add_mcp_server's EnvRefs (pkg/sysagent/tools/mcp.go routes MCP server
	// `env` secrets through the encrypted credential store instead of
	// config.json plaintext) back into real values at connect time. Store.Get
	// has exactly the func(string) (string, error) signature
	// AgentLoop.SetCredentialResolver expects. Guarded on credStore != nil
	// defensively — bootCredentials aborts boot on failure, so this should
	// always be non-nil in practice, but a nil receiver would panic inside
	// Store.Get's mutex lock.
	if credStore != nil {
		agentLoop.SetCredentialResolver(credStore.Get)
	}

	runningServices, err := setupAndStartServices(
		ctx,
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

	// Boot-time browser warm-up steps 1 and 2 — the first TAB and the WebRTC
	// CAPTURE (see startBrowserWarmBoot's block comment). Deliberately here,
	// after setupAndStartServices: both need this gateway's own listener to be
	// accepting, since the warm tab lands on the gateway-served start page and
	// the capture's encoder page dials the gateway's loopback capture-ingest
	// WS. Step 0 (the Chrome PROCESS) still runs earlier, where it has nothing
	// to wait for. Returns immediately; nothing joins it, and nothing it does
	// can fail boot.
	startBrowserWarmBoot(ctx, cfg, homePath, agentLoop, runningServices.browserWS)

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

	// SkillInstaller backs remove_skill (SkillRemoveTool, its only consumer —
	// see pkg/sysagent/tools/skill.go). skills.SkillInstaller.Uninstall joins
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
	// anywhere else can never find what install_skill actually wrote. Rooting
	// it at skillsWorkspace was exactly this bug: list_skills correctly
	// reported an installed skill (via the loader's global tier), but
	// remove_skill always reported it NOT_FOUND, because Uninstall only ever
	// looked under skillsWorkspace/skills.
	//
	// GitHub token/proxy from the first github marketplace entry (optional;
	// empty → unauthenticated API calls).
	githubToken, githubProxy := skills.FirstGitHubMarketplaceCreds(cfg, bundle.GetString)
	sysSkillInstaller, err := skills.NewSkillInstallerWithSSRF(
		homePath, githubToken, githubProxy, ssrfChecker,
	)
	if err != nil {
		slog.Warn("gateway: could not create skill installer; remove_skill unavailable",
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
		CredStore:  credStore,
		ReloadFunc: reloadTrigger,
		// WaitForReloadFunc: AgentDeleteTool's synchronous reload wait (see
		// systools.Deps.WaitForReloadFunc's doc comment for the delete_agent →
		// list_agents ghost-listing race this closes). Built on the same
		// TriggerReload + IsReloadPending polling primitive that backs
		// restAPI.triggerReloadAndWait, so a delete_agent tool call blocks for
		// exactly as long as REST's DELETE /api/v1/agents/{id} does before
		// either call reports success.
		WaitForReloadFunc: func() error { return waitForReload(agentLoop) },
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
		UpsertAgentFastFunc: func(agentID string) error {
			if agentLoop.IsReloadPending() {
				return reloadTrigger()
			}
			if err := agentLoop.MutateConfig(func(cfg *config.Config) error {
				return populateAgentsListFromEntityStoreStrict(cfg, homePath)
			}); err != nil {
				slog.Warn("gateway: sysagent fast agent upsert: could not refresh the agent roster "+
					"from the entity store; falling back to full reload",
					"agent_id", agentID, "error", err)
				return reloadTrigger()
			}
			if _, err := agentLoop.UpsertAgentFast(agentLoop.GetConfig(), agentID); err != nil {
				slog.Warn("gateway: sysagent fast agent upsert failed; falling back to full reload",
					"agent_id", agentID, "error", err)
				return reloadTrigger()
			}
			return nil
		},
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
		// D2 rule 5 (FR-017/052, review r1 major M5): create_task_in_workspace
		// parity with the plain create_task tool's own bash-policy checker —
		// MUST be wired in production, leaving it nil fails CLOSED (unlike
		// DelegationDeny above) per systools.Deps.ResolveBashPolicy's doc
		// comment.
		ResolveBashPolicy: agentLoop.NewSysagentBashPolicyResolver(),
		// ADR-057 U9 changed ListAllSessions' signature to
		// (limit, offset int, parentSessionID string, flat bool), so it can no
		// longer be assigned here as a bare method value — this field's type
		// is the zero-arg func() ([]*session.UnifiedMeta, []error) shape
		// get_usage (the sole consumer) actually needs. See
		// u25AllSessionsForUsage's doc comment for why flat=true is required
		// here (FR-104: roots-only would silently under-count delegated
		// children's token spend).
		ListSessions: u25AllSessionsForUsage(agentLoop),
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
	for _, t := range systools.AllTools(sysAgentDeps) {
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
	if sharedStore := agentLoop.GetSessionStore(); sharedStore != nil {
		startRetentionSweepLoop(ctx, sharedStore, agentLoop.GetConfig, 24*time.Hour)
	}

	// FR-031: Launch the nightly retro sweep goroutine alongside the session sweep.
	// Iterates all agents and calls SweepRetros per MemoryStore.
	startRetentionRetroSweepLoop(ctx, agentLoop, agentLoop.GetConfig, 24*time.Hour)

	// FR-032/FR-032a: Bootstrap recap pass — on gateway start, re-cap sessions
	// that lack LAST_SESSION.md. Runs once in a goroutine.
	go agentLoop.BootstrapRecapPass(ctx)

	// ADR-053 Phase 2 on-ramp: start the SessionMessageChan kind-router
	// consumer. This is the MISSING consumer for the bus's 4th channel — until
	// it runs, SessionMessageChan() has no drainer and any publisher would
	// block. The consumer dispatches by kind: steer/respond → child steering
	// queue; child->parent kinds → durable inbox Append + bounded typed wake;
	// engine kinds → WS frame. Bound to ctx so it shuts down with the gateway.
	// Honors session_messaging.enabled live (read per event) — when false the
	// consumer drains-and-discards (channel stays unblocked) but no-ops.
	smConsumerCancel := agentLoop.StartSessionMessageConsumer(ctx)
	defer smConsumerCancel()

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
		// ADR-054 §0 R3: the configured default agent naming an entity record
		// that failed to load is a degraded state, not a silent fallback —
		// EvaluateDefaultAgentHealth (called from NewAgentRegistry) records it.
		// SetDegradedFunc is a single-slot hook, so this COMPOSES with the
		// checks above rather than replacing them.
		if registry := agentLoop.GetRegistry(); registry != nil {
			if degraded, reason := registry.DefaultAgentDegraded(); degraded {
				return true, reason
			}
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
	// ADR-067 FR-037 (T067-10): /health reports the provider catalog
	// degraded — with the last refresh error — whenever no document is
	// loaded, the last refresh failed, or the served document is stale
	// (updated_at older than 14 days). Like audit_degraded it is a FIELD,
	// not a 503: an out-of-date registry snapshot makes the model picker
	// less accurate, it does not stop the gateway serving turns.
	runningServices.HealthServer.SetCatalogStateFunc(func() (bool, string) {
		return catalogHealthState(providerCatalog)
	})

	var configReloadChan <-chan *config.Config
	stopWatch := func() {}
	if cfg.Gateway.HotReload {
		configReloadChan, stopWatch = setupConfigWatcherPolling(
			configPath,
			homePath,
			debug,
			credStore,
			runningServices.selfWriteReg,
			runningServices.markReloadDegraded,
		)
		logger.Info("Config hot reload enabled")
	}
	defer stopWatch()

	// loadReloadConfig re-reads config.json for a reload. It backs the /reload
	// path AND every coalesced follow-up reload — the latter MUST re-read from
	// disk, since the whole reason a request was coalesced is that its write
	// post-dates the running reload's snapshot.
	//
	// LoadConfigWithStoreAndSelfHealHook (not LoadConfigWithStore): this path
	// bypasses safeUpdateConfigJSON's configMu + selfWriteReg registration, so
	// if the single-user-model role self-heal writes config.json here, the write
	// must be registered manually or the watcher's next tick would misidentify
	// it as an external edit.
	loadReloadConfig := func() (*config.Config, error) {
		newCfg, err := config.LoadConfigWithStoreAndSelfHealHook(
			configPath, credStore, selfHealWriteHook(runningServices.selfWriteReg),
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
			runningServices.markReloadDegraded(
				fmt.Errorf("reload rejected: agent roster population failed: %w", err),
			)
			return nil, fmt.Errorf("agent roster population failed: %w", err)
		}
		if err = newCfg.ValidateProviders(); err != nil {
			return nil, fmt.Errorf("config validation failed: %w", err)
		}
		return newCfg, nil
	}

	// runOneReload is the production executor handed to runReloadCycle. provider
	// is captured by address because executeReload swaps it in place on success.
	runOneReload := func(c *config.Config) error {
		return executeReload(ctx, agentLoop, c, &provider, runningServices, msgBus, allowEmptyStartup)
	}

	for {
		select {
		case <-agentLoopCtx.Done():
			logger.Info("Shutting down...")
			omnipusGracefulShutdown(runningServices, agentLoop, provider, cfg)
			return nil
		case newCfg := <-configReloadChan:
			if !runningServices.beginReload(agentLoop.MarkReloadPending) {
				// NOT a drop: beginReload recorded the request, and the cycle
				// that owns the in-flight reload will run a follow-up reload
				// that re-reads this file change from disk before it finishes.
				logger.Info("Config reload coalesced into the in-flight reload")
				continue
			}
			runReloadCycle(agentLoop, runningServices, newCfg, runOneReload, loadReloadConfig)
		case <-manualReloadChan:
			// The slot was already claimed by reloadTrigger before it signalled
			// this channel, so do NOT call beginReload here — runReloadCycle
			// takes ownership of the release directly.
			logger.Info("Manual reload triggered via /reload endpoint")
			runReloadCycle(agentLoop, runningServices, nil, runOneReload, loadReloadConfig)
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

// --- boot-time browser WARM SURFACES (tab + capture) ------------------------
//
// browserWarmUpEnabled above governs step 0 of the warm-up ladder: the Chrome
// PROCESS. Measured on a host whose Chrome binary is already present (the
// Docker-image case, no download), that is genuinely all it does — the gateway
// answered HTTP at 2.6s and Chrome's main process was live at 2.8s, with ZERO
// renderer processes: no browsing context, no tab, no page. The first panel
// open then paid for everything else on the user's own critical path:
// attach 0.4s + tab-on-demand 1.0-2.2s + capture-extension load and WebRTC
// negotiation 1.7-6.7s + first frame 1.2-4.3s, ~9.5s in total, and one run in
// three failed outright with an ingest timeout ("no ingest video track after
// waiting 15s").
//
// Two further steps close that gap, deliberately kept SEPARATELY controllable
// because their cost profiles are nothing alike:
//
//	step 1 — the TAB (tools.browser.warm_tab_at_boot, default true).
//	         One renderer parked on a static local page. Nearly free to keep.
//	step 2 — the CAPTURE (tools.browser.warm_capture_at_boot, default true).
//	         The expensive one: a running capture encodes video continuously.
//	         It therefore stops itself after tools.browser.warm_capture_idle_sec
//	         with no viewer ever attached, and the next offer starts a fresh one.
//
// Every property the process warm-up has is preserved here, because the
// reasons for them are unchanged (Hard Constraint #4, graceful degradation):
// best-effort, non-blocking (a bare goroutine nothing joins), panic-contained,
// audited on failure, and skipped entirely by every existing opt-out —
// tools.browser.warm_at_boot, tools.browser.enabled, a set cdp_url (a remote
// CDP Chrome is not ours to warm) and OMNIPUS_SKIP_BROWSER_PREPROVISION=1 —
// since browserWarmTabEnabled/browserWarmCaptureEnabled are both defined as
// browserWarmUpEnabled AND their own flag. Nothing here can fail boot: the
// caller starts it and moves on.
//
// PARITY (macOS vs Linux): everything above the CDP transport is identical on
// both — same defaults, same config keys, same selection rule, same idle stop,
// same log/audit lines, same recovery (the ordinary lazy path). What differs is
// underneath and invisible from here: macOS encodes in hardware over loopback,
// a hosted Linux box encodes in software over a real network, so the WALL-CLOCK
// saving is larger on Linux than on a laptop. A failure degrades identically on
// both: the warm surface is skipped, one WARN + one audit entry is written, and
// the first open behaves exactly as it did before this existed.

// warmBootListenerBudget bounds how long the warm-boot goroutine waits for the
// gateway's own HTTP listener before giving up and warming anyway.
//
// This wait is load-bearing, not politeness: the start page a warm tab lands on
// is served BY THIS GATEWAY (tools.browser.start_page_url defaults to
// http://localhost:<gateway.port>/browser-start, pkg/agent/loop.go), and the
// capture's encoder page connects back to this gateway's own loopback
// capture-ingest WS. Warming before the listener accepts would leave the warm
// tab parked on about:blank — which on this surface reads as a BROKEN panel,
// indistinguishable from a real capture failure (the exact confusion
// manager.go's navigateNewTabToStartPage exists to avoid) — and would fail the
// capture outright. On expiry we proceed regardless rather than skipping: the
// warm-up is best-effort, and a listener that is late is not a reason to leave
// the first open cold.
const warmBootListenerBudget = 20 * time.Second

// warmBootListenerDialTimeout / warmBootListenerRetryDelay pace that wait.
const (
	warmBootListenerDialTimeout = 500 * time.Millisecond
	warmBootListenerRetryDelay  = 100 * time.Millisecond
)

// warmCaptureIdleCheckInterval is how often the idle watcher re-reads a warm
// capture's viewer count. Short relative to the (minutes-long) default idle
// timeout so the handover to a real viewer is noticed promptly, and so the
// watcher exits quickly on gateway shutdown.
//
// 1s, not the original 5s (round-2 finding F2): the handover is no longer a
// bookkeeping event the watcher merely notices — it now performs an action on
// the viewer's behalf (warmCaptureAdaptResetMinAge below), and every second of
// detection lag is a second of that action landing later, mid-view, instead of
// during the first moment of a panel that is still settling. A 1s tick that
// reads one atomic counter, for at most the idle window, is not a cost worth
// weighing against that.
const warmCaptureIdleCheckInterval = time.Second

// warmCaptureAdaptResetMinAge is how long a boot-warmed capture must have run
// UNWATCHED before the handover to its first real viewer forces a recapture.
//
// Why the handover forces one at all (round-2 finding F2): the encoder page
// runs a bounded resolution-adaptation loop (captureext/embedded/encoder.js)
// that steps the picture down when the encoder reports it is CPU-limited. A
// boot-warmed capture is a full software encode with NOBODY WATCHING, running
// during the busiest minute of the process's life — gateway boot, Chrome
// launch, extension load — and on a 2-core hosted Linux box it can reach the
// loop's hard floor (a QUARTER of the pixels) within ~8 seconds. Without this,
// the user's first panel open inherits a resolution decided by, and for, no
// one, and needs 20+ seconds of sustained high frame rate to climb back out.
//
// A recapture is the signal, because it is the one the encoder already
// understands — the same control frame a panel resize sends — and because the
// encoder's own carry-over rule (adaptCarryOverIndex) is what interprets it:
// the FIRST rebuild of a capture starts the viewer at full quality, later ones
// keep whatever the loop learned while its evidence is still fresh. The
// gateway does not, and should not, know about scale factors; it knows about
// viewers, which is the thing the encoder cannot see.
//
// Why an age gate rather than "always": a viewer who opens the panel seconds
// after boot — precisely the case the warm-up was built for, and the one that
// measured 6,655ms -> 1,041ms to first frame — cannot have inherited an
// adaptation, because the loop only starts on the PeerConnection's first
// 'connected' transition and needs two further 2s samples before it can step
// at all (~6s at the very earliest). Recapturing there would spend the whole
// win on a rebuild that resets nothing.
//
// This is a SAFETY NET, not the primary mechanism, which is why a gate that
// sits past the earliest possible step is acceptable rather than sloppy: the
// SPA reports its panel geometry on every fresh attach (browserLiveWs.ts's
// sendViewport, ~650ms in), and that already forces the encoder's first
// rebuild — the one adaptCarryOverIndex resets unconditionally. What this
// adds is the GUARANTEE, so a viewer whose geometry happened not to change,
// or a future SPA that stops sending one, cannot silently inherit a
// resolution chosen with nobody watching. The cost of the belt as well as the
// braces is at most one extra capture rebuild (the same brief blip a panel
// resize causes) in the first seconds of an open, and only for a capture that
// has been warming, unwatched, for longer than this.
//
// A var, not a const, purely so the handover test can exercise the rule
// without a 15-second sleep (same pattern as captureIngestWriteTimeout in
// browser_webrtc.go). Never reassigned in production code.
var warmCaptureAdaptResetMinAge = 15 * time.Second

// browserWarmTabEnabled reports whether step 1 (warm the first tab) should
// run. AND-ed with browserWarmUpEnabled by construction: warming a tab
// presupposes warming the process, so every existing opt-out — including
// OMNIPUS_SKIP_BROWSER_PREPROVISION=1 and a remote cdp_url — governs this
// too, and an operator who turned warm_at_boot off gets a fully lazy browser
// rather than a half-warmed one.
func browserWarmTabEnabled(cfg *config.Config) bool {
	return browserWarmUpEnabled(cfg) && cfg.Tools.Browser.WarmTabAtBoot
}

// browserWarmCaptureEnabled reports whether step 2 (warm the WebRTC capture)
// should run. Same AND-ing rationale as browserWarmTabEnabled. Note this is
// only the CONFIG half of the decision: the warm-boot path additionally
// re-uses webrtcUnavailableReason — the exact gate a real viewer offer
// applies — so warm-up can never start a capture an offer would have refused.
func browserWarmCaptureEnabled(cfg *config.Config) bool {
	return browserWarmUpEnabled(cfg) && cfg.Tools.Browser.WarmCaptureAtBoot
}

// warmCaptureIdleTimeout is how long a boot-warmed capture may run with no
// viewer ever attached. 0 means "never stop it on idle" (an explicit
// operator choice — see WarmCaptureIdleSec's doc comment), which is why a
// non-positive configured value maps to 0 rather than to the default: an
// operator who writes 0 is opting OUT of the idle stop, not asking for the
// shipped 5 minutes back.
func warmCaptureIdleTimeout(cfg *config.Config) time.Duration {
	if cfg.Tools.Browser.WarmCaptureIdleSec <= 0 {
		return 0
	}
	return time.Duration(cfg.Tools.Browser.WarmCaptureIdleSec) * time.Second
}

// pickWarmBrowserManager chooses the ONE browser whose tab (and, optionally,
// capture) gets warmed at boot: the browser of the workspace the DEFAULT agent
// resolves to (ADR-072 FR-016b). It returns (nil, reason) when there is
// nothing to warm — reason is a short operator-facing sentence, and the caller
// logs it exactly once at INFO.
//
// Why one and not all: each warmed tab is a renderer process (74-268MB RSS
// measured on the UAT box, coordinator.go), and a warmed capture is a
// continuously encoding video pipeline of which one host can only usefully
// serve ONE at a time anyway (ADR-048 condition 2 — handleWebRTCOffer still
// denies a second ACTIVELY-VIEWED capture, now across workspaces). Under
// FR-001 a manager is a WORKSPACE's browser, so warming every manager would
// multiply a whole Chrome process and profile by the workspace count to save
// time on exactly one panel.
//
// Why the DEFAULT AGENT'S RESOLVED WORKSPACE, and no fallback:
//
//   - Selection used to compare agents.defaults.default_agent_id against
//     mgr.AgentID(). After FR-001 that accessor returns the manager's BROWSING
//     KEY ("ws:<id>"), so the comparison could never match again and every
//     boot silently took the lexicographic branch instead. This is the fix for
//     that, and it is why the four selection tests in browser_warmboot_test.go
//     had to change with it.
//   - The old lexicographic fallback is GONE. It was a tie-break over
//     workspaces, and picking one would mean starting a Chrome against one
//     workspace's profile — one particular set of live logins — because it
//     sorted first, with nobody watching and nobody asked. That is the same
//     implicit choice FR-033 refuses at every other resolution point, and a
//     latency optimisation is the weakest possible reason to make it. When the
//     default agent resolves to no workspace we warm NOTHING and say so; the
//     lazy path is a complete fallback and costs one panel one cold open.
//
// Determinism is therefore trivial rather than sorted: there is only ever one
// candidate key, so two boots of the same install — and a macOS and a Linux
// host of it — warm the same browser or none.
func pickWarmBrowserManager(
	cfg *config.Config, home string, mgrs []*browser.BrowserManager,
) (*browser.BrowserManager, string) {
	byKey := make(map[string]*browser.BrowserManager, len(mgrs))
	for _, mgr := range mgrs {
		if mgr == nil {
			continue
		}
		// Two hard requirements, both of which a candidate in the normal
		// coordinator-mode gateway always satisfies:
		//   - a real browsing key, because that is what names the browser to
		//     warm and the zero key is not a browser;
		//   - an attached coordinator, so warming drives the ONE shared
		//     Chrome. A manager with no coordinator falls back to the legacy
		//     one-Chrome-per-manager managed mode (manager.go's ensureStarted),
		//     which would have boot spawn a SECOND Chrome process — the exact
		//     opposite of a cheap warm-up. WebRTC capture requires the
		//     coordinator anyway (defaultEncoderStarter refuses without one).
		key := mgr.BrowsingKey()
		if key.IsZero() || mgr.Coordinator() == nil {
			continue
		}
		if _, dup := byKey[key.String()]; dup {
			continue
		}
		byKey[key.String()] = mgr
	}
	if len(byKey) == 0 {
		return nil, "no workspace has a browser manager yet"
	}

	def := strings.TrimSpace(cfg.Agents.Defaults.DefaultAgentID)
	if def == "" {
		return nil, "no default agent is set (agents.defaults.default_agent_id), " +
			"so there is no one workspace to warm"
	}
	key, err := browser.ResolveBrowsingKeyForAgent(home, def, "")
	if err != nil {
		return nil, fmt.Sprintf(
			"the default agent %q is not on exactly one workspace's team, so it resolves to no "+
				"single browser to warm", def)
	}
	mgr, ok := byKey[key.String()]
	if !ok {
		return nil, fmt.Sprintf(
			"the default agent %q resolves to workspace %q, which has no browser manager yet",
			def, key.WorkspaceID())
	}
	return mgr, ""
}

// waitForGatewayListener blocks until this gateway's own HTTP listener accepts
// a loopback TCP connection, ctx is canceled, or budget expires. Reports
// whether the listener actually answered.
//
// Loopback specifically (not cfg.Gateway.Host): both consumers of this wait are
// in-process loopback clients — the warm tab fetches the start page and the
// encoder page dials ws://127.0.0.1:<port>/api/v1/browser/capture-ingest, the
// same hardcoded loopback address handleWebRTCOffer already builds. A gateway
// bound exclusively to a non-loopback address therefore never satisfies this
// wait, and we proceed on the budget instead — the same outcome the capture
// path would reach on its own.
func waitForGatewayListener(ctx context.Context, port int, budget time.Duration) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(budget)
	for {
		conn, err := net.DialTimeout("tcp", addr, warmBootListenerDialTimeout)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(warmBootListenerRetryDelay):
		}
	}
}

// warmCaptureHandle is the slice of *browser.CaptureSession the idle watcher
// needs. An interface, not the concrete type, so the watcher's stop/handover
// decision is testable without a real Chrome, a real encoder page and a real
// WebRTC relay — the three things that make the capture path otherwise
// untestable off a live host.
type warmCaptureHandle interface {
	ViewerCount() int
	Done() <-chan struct{}
	Stop()
	// ResetAdaptation pushes a browser_capture_control{adapt_reset} frame to
	// the encoder page: restore full quality WITHOUT rebuilding the capture.
	// Used at handover — see warmCaptureAdaptResetMinAge.
	ResetAdaptation(reason string)
}

// watchWarmCaptureIdle stops a boot-warmed capture that no viewer ever came to
// watch, and gets out of the way of one that a viewer DID take over.
//
// The handover matters: CaptureSession's own grace-stop timer is armed by
// RemoveViewer, so it only ever protects a session that HAD a viewer. A capture
// started by boot warm-up has never had one, so nothing in the capture session
// itself would ever stop it — it would encode video for the process's whole
// lifetime. Once a viewer attaches, that ordinary last-detach grace stop owns
// the session and this watcher exits without touching it.
//
// Deliberately NOT via CaptureSession.SetOnStopped: ensureCaptureSession
// already installs the hook that clears the capture registry and notifies
// viewers, and that hook is single-slot — overwriting it here would silently
// break the teardown path for every session.
func watchWarmCaptureIdle(ctx context.Context, cs warmCaptureHandle, idle time.Duration, agentID string) {
	if cs == nil || idle <= 0 {
		return
	}
	startedAt := time.Now()
	deadline := startedAt.Add(idle)
	ticker := time.NewTicker(warmCaptureIdleCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cs.Done():
			// Someone else already stopped it (an offer superseded it, the
			// browser died, shutdown) — nothing to do.
			return
		case <-ticker.C:
			if cs.ViewerCount() > 0 {
				unwatchedFor := time.Since(startedAt)
				// Round-2 F2: hand the viewer a capture that starts from the
				// quality THEY warrant, not one shaped by minutes of
				// unwatched, boot-contended encoding — see
				// warmCaptureAdaptResetMinAge for the full rationale.
				if unwatchedFor >= warmCaptureAdaptResetMinAge {
					// WARN, not INFO: this costs the viewer a brief, visible
					// stream blip in the first seconds of their panel open,
					// and production logs are WARN-only — an INFO line here
					// would leave an operator investigating "the video
					// blinked when I opened the panel" with nothing to find.
					// INFO, not WARN: this no longer costs the viewer a
					// visible blip. It used to force a capture REBUILD, which
					// measured ~17s to first frame against ~4s without it
					// (hosted box, 2026-08-17) and made a long-lived warm
					// capture worse than none. ResetAdaptation keeps the
					// guarantee -- the viewer starts at full quality -- and
					// drops the rebuild.
					logger.InfoCF("browser",
						"boot-warmed capture adopted by a viewer after running unwatched — resetting the encoder's adaptation so the picture starts at full quality; "+
							"the resolution the encoder settled on with nobody watching is not evidence about this viewer",
						map[string]any{
							"agent_id":      agentID,
							"unwatched_for": unwatchedFor.Round(time.Second).String(),
						})
					cs.ResetAdaptation("boot-warmed capture handed to its first viewer")
				} else {
					logger.InfoCF("browser", "boot-warmed capture adopted by a viewer — handing it to the normal grace-stop path",
						map[string]any{"agent_id": agentID, "unwatched_for": unwatchedFor.Round(time.Second).String()})
				}
				return
			}
			if time.Now().Before(deadline) {
				continue
			}
			// WARN for the same reason the start line is (see
			// startBrowserWarmBoot): this is the moment the idle CPU burn
			// ENDS, and an operator who saw the start line must be able to
			// see it end — in a WARN-only production log, an INFO here would
			// leave "is it still encoding?" unanswerable.
			logger.WarnCF("browser", "boot-warmed capture idle-stopped (no viewer ever attached) — CPU released; the next viewer starts a fresh one",
				map[string]any{"agent_id": agentID, "idle": idle.String()})
			cs.Stop()
			return
		}
	}
}

// startBrowserWarmBoot kicks off warm-up steps 1 and 2 (see the block comment
// above) on their own goroutine and returns immediately. Call it AFTER the HTTP
// listener is up: both steps need this gateway to be serving (start page /
// capture-ingest WS), which is why they live here rather than alongside the
// process warm-up earlier in boot.
//
// Nothing joins the returned goroutine, exactly like the process warm-up's:
// gateway shutdown must not wait for a browser, and ctx (the gateway's own
// shutdown-aware context) is what stops it early.
func startBrowserWarmBoot(
	ctx context.Context,
	cfg *config.Config,
	homePath string,
	agentLoop *agent.AgentLoop,
	h *BrowserWSHandler,
) {
	if agentLoop == nil {
		return
	}
	wantTab := browserWarmTabEnabled(cfg)
	wantCapture := browserWarmCaptureEnabled(cfg)
	if !wantTab && !wantCapture {
		return
	}
	mgr, skipReason := pickWarmBrowserManager(cfg, homePath, agentLoop.BrowserManagers())
	if mgr == nil {
		// Say so rather than falling through silently — "enabled but nothing
		// to warm" is otherwise indistinguishable from "disabled" and from
		// "still running", the same three-way ambiguity the process warm-up's
		// own no-coordinator branch calls out.
		//
		// ONE line, at INFO (FR-016b). Nothing is wrong here: skipping the
		// warm-up costs the first panel open a cold start and nothing else,
		// and a WARN would tell an operator to fix a configuration that may be
		// exactly what they intended.
		logger.InfoCF("browser",
			"boot-time browser tab/capture warm-up enabled but skipped — "+skipReason+
				"; the first panel open will build the browser lazily",
			nil)
		return
	}

	go func() {
		// A panic here must never take the gateway down with it: this is an
		// optional latency optimisation, and the lazy path is a complete
		// fallback. Same contract as the process warm-up's own recover().
		defer func() {
			if r := recover(); r != nil {
				logger.WarnCF("browser",
					"boot-time browser tab/capture warm-up panicked — recovered; the first panel open will build them lazily",
					map[string]any{"panic": fmt.Sprintf("%v", r)})
				audit.Emit(context.Background(), agentLoop.AuditLogger(),
					audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
					map[string]any{"stage": "warm_boot", "reason": "panic", "error": fmt.Sprintf("%v", r)})
			}
		}()

		agentID := mgr.AgentID()
		if !waitForGatewayListener(ctx, cfg.Gateway.Port, warmBootListenerBudget) {
			if ctx.Err() != nil {
				return
			}
			logger.WarnCF("browser",
				"boot-time browser warm-up: gateway listener did not answer on loopback in time — warming anyway (a warm tab may land on about:blank)",
				map[string]any{"agent_id": agentID, "budget": warmBootListenerBudget.String()})
		}
		if ctx.Err() != nil {
			return
		}

		// Step 1 — the tab. mgr.Session(mgr.OperatorSessionID()) is the SAME
		// call the live panel and the capture's tab resolution make, so this
		// warms the tab they will actually use rather than parking a stray
		// extra one in the shared Chrome (which the encoder's fallback tab
		// resolution could then bind to by mistake).
		//
		// It is the WORKSPACE-OWNED tab set, not an agent's: under ADR-072
		// FR-080 a browser_* tool addresses its own SESSION's tabs, which do
		// not exist until that session browses and cannot be warmed at boot.
		// The panel's tabs can be, and are.
		if wantTab {
			started := time.Now()
			if _, err := mgr.Session(mgr.OperatorSessionID()); err != nil {
				logger.WarnCF("browser",
					"boot-time browser tab warm-up failed — the first panel open will build the tab lazily",
					map[string]any{"agent_id": agentID, "error": err.Error()})
				audit.Emit(context.Background(), agentLoop.AuditLogger(),
					audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
					map[string]any{"stage": "tab", "reason": "error", "agent_id": agentID, "error": err.Error()})
			} else {
				logger.InfoCF("browser", "boot-time browser tab warmed (parked on the start page)",
					map[string]any{"agent_id": agentID, "took": time.Since(started).String()})
			}
		}

		if !wantCapture || h == nil || ctx.Err() != nil {
			return
		}

		// Step 2 — the capture. Refuse for exactly the reasons a real viewer
		// offer would refuse (webrtcUnavailableReason is the shared gate
		// handleWebRTCOffer and announceWebRTCAvailability both use), so
		// warm-up can never start something an offer would not have.
		if reason := webrtcUnavailableReason(cfg, mgr); reason != "" {
			logger.InfoCF("browser", "boot-time capture warm-up skipped — WebRTC capture is unavailable for this agent",
				map[string]any{"agent_id": agentID, "reason": reason})
			return
		}

		// Take the same fence the offer path takes around get-or-create, so a
		// viewer offer racing this warm-up cannot produce two capture sessions
		// for the same agent (ADR-048 condition 2 / the fence's own TOCTOU
		// rationale). Released before Start, which does CDP work — never hold
		// a process-wide mutex across that.
		//
		// The empty panel tab set id is deliberate (issue #671): boot-time
		// warm-up has no viewer and no chat to resolve against, so the capture
		// binds to the operator's workspace-owned set — the same set step 1
		// above just warmed, and the same one this path has always used. A
		// real viewer's offer resolves its own.
		h.captureFenceMu.Lock()
		cs, err := h.ensureCaptureSession(mgr, agentID, "", cfg)
		h.captureFenceMu.Unlock()
		if err != nil {
			logger.WarnCF("browser", "boot-time capture warm-up: could not create the capture session",
				map[string]any{"agent_id": agentID, "error": err.Error()})
			audit.Emit(context.Background(), agentLoop.AuditLogger(),
				audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
				map[string]any{"stage": "capture", "reason": "ensure_failed", "agent_id": agentID, "error": err.Error()})
			return
		}

		started := time.Now()
		ingestURL := fmt.Sprintf("ws://127.0.0.1:%d/api/v1/browser/capture-ingest", cfg.Gateway.Port)
		if _, startErr := cs.Start(ctx, ingestURL); startErr != nil {
			logger.WarnCF("browser",
				"boot-time capture warm-up failed to start — the first viewer will start one the ordinary way",
				map[string]any{"agent_id": agentID, "error": startErr.Error()})
			audit.Emit(context.Background(), agentLoop.AuditLogger(),
				audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
				map[string]any{"stage": "capture", "reason": "start_failed", "agent_id": agentID, "error": startErr.Error()})
			// Stop() is idempotent and its onStopped hook (installed by
			// ensureCaptureSession) clears BOTH the manager's reference and the
			// token registry, so the next offer builds a fresh session instead
			// of reusing this permanently-broken one — the same recovery
			// handleWebRTCOffer performs on a failed Start.
			cs.Stop()
			return
		}
		idle := warmCaptureIdleTimeout(cfg)
		// WARN, deliberately, for a SUCCESSFUL step (round-2 finding F2).
		// This line is the only disclosure an operator gets that their box is
		// now software-encoding video for a viewer who does not exist —
		// measured at ~26% of one core for the whole idle window on macOS,
		// and more on a hosted Linux box with no hardware encoder. Every
		// other warm-boot line is INFO, and this project's production logs
		// are WARN-only, so at INFO the entire feature (and its cost) is
		// invisible in exactly the deployment where it is most expensive.
		// Bounded: this fires at most once per boot, and only when
		// tools.browser.warm_capture_at_boot is on. It names both dials so
		// the line itself is the fix.
		//
		// A warm capture that is NEVER idle-stopped (idle_sec 0, an explicit
		// operator opt-out) burns that core for the process's whole lifetime,
		// so it says so instead of printing "0s".
		idleDesc := idle.String()
		if idle <= 0 {
			idleDesc = "never (tools.browser.warm_capture_idle_sec=0)"
		}
		logger.WarnCF("browser",
			"boot-time capture warm-up started: Chrome is now encoding this agent's tab with NO viewer attached, to make the first panel open fast. "+
				"Cost ~1/4 of a CPU core until a viewer attaches or it idle-stops. Turn it off with tools.browser.warm_capture_at_boot=false, "+
				"or shorten tools.browser.warm_capture_idle_sec.",
			map[string]any{
				"agent_id":     agentID,
				"took":         time.Since(started).String(),
				"idle_stop_in": idleDesc,
			})
		// Note the boundary this leaves, deliberately: with idle-stop turned
		// OFF (warm_capture_idle_sec=0) there is no watcher, so there is no
		// handover recapture either — the F2 belt is absent in exactly the
		// configuration where an unwatched capture runs longest. The braces
		// still hold there: the encoder discards what its FIRST capture
		// learned on that capture's first rebuild (adaptCarryOverIndex), and
		// the SPA forces one by reporting its panel geometry on attach. An
		// operator who opts out of the idle stop gets the same picture, just
		// without the second, gateway-side guarantee.
		if idle > 0 {
			go watchWarmCaptureIdle(ctx, cs, idle, agentID)
		}
	}()
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
	DeviceService  *devices.Service
}

func snapshotServices(svc *services) servicesSnapshot {
	return servicesSnapshot{
		bundle:         svc.bundle,
		ChannelManager: svc.ChannelManager,
		CronService:    svc.CronService,
		TaskTrigger:    svc.TaskTrigger,
		LoopScheduler:  svc.LoopScheduler,
		MediaStore:     svc.MediaStore,
		DeviceService:  svc.DeviceService,
	}
}

func restoreServices(svc *services, snap servicesSnapshot) {
	svc.bundle = snap.bundle
	svc.ChannelManager = snap.ChannelManager
	svc.CronService = snap.CronService
	svc.TaskTrigger = snap.TaskTrigger
	svc.LoopScheduler = snap.LoopScheduler
	svc.MediaStore = snap.MediaStore
	svc.DeviceService = snap.DeviceService
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

		// Re-register resolved plaintexts so the scrubber stays current after reload.
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
	if cfg.Agents.Defaults.DefaultModel.IsZero() && allowEmptyStartup {
		reason := "no default model configured; gateway started in limited mode"
		fmt.Printf("⚠ Warning: %s\n", reason)
		logger.WarnCF("gateway", "Gateway started without default model", map[string]any{
			"limited_mode": true,
		})
		return &startupBlockedProvider{reason: reason}, "", nil
	}

	// The default model's credential never resolved (2026-08-14). Boot no
	// longer dies on this — but it must not paper over it either. Without this
	// branch the factory happily builds an HTTP provider with an EMPTY API key
	// (pkg/providers/factory_provider.go accepts api_key OR api_base), and the
	// operator's first chat message comes back as a bare upstream 401 that
	// names neither the provider nor the credential. Answer with the real
	// cause on every turn instead, using the same limited-mode mechanism the
	// no-model case already uses.
	if reason, blocked := defaultModelCredentialBlocked(cfg); blocked {
		fmt.Printf("⚠ Warning: %s\n", reason)
		logger.WarnCF("gateway", "Gateway started with an unusable default provider", map[string]any{
			"limited_mode": true,
			"reason":       reason,
		})
		slog.Error("gateway: default model's provider is unusable", "reason", reason)
		return &startupBlockedProvider{reason: reason}, "", nil
	}

	return providers.CreateProvider(cfg)
}

// defaultModelCredentialBlocked reports whether EVERY providers[] entry backing
// the default model names an api_key_ref that did not resolve — i.e. the
// credential is absent from (or unreadable in) the vault, so no request to that
// provider can succeed.
//
// "Every entry" and not "the entry" on purpose: several entries may carry the
// same (provider, model) pair for load balancing (config.GetModelConfig
// round-robins over them). Matching is by the exact pair (ADR-068 D14.1) — a
// row serving the same model under another provider never backs the default.
// If any one of them still has a usable key, the model is not blocked and the
// factory keeps its existing behaviour.
//
// Entries with no api_key_ref at all (local models, CLI/OAuth providers) are
// never blocked — they are not supposed to have a vault credential.
//
// The returned message unconditionally says the credential is "missing from
// the credential vault" and advises re-entering the API key. That wording is
// only correct for a genuinely-absent credential (*credentials.NotFoundError)
// — for a wrong master key or a corrupted store entry, the credential is NOT
// missing (it is there, encrypted under the right key) and re-entering it
// would encrypt the new value under the WRONG key into the same
// credentials.json, corrupting that entry for good once the real master key
// is restored (mirrors rest.go's describeCredentialResolutionError, which
// draws exactly this NotFoundError-vs-everything-else line for the same
// reason). This function does NOT itself re-derive the cause from cfg — it
// can't; by the time createStartupProvider is reached, cfg.Providers carries
// no error value, only an empty ModelConfig.APIKey(). Correctness here
// depends entirely on an upstream invariant: reportInjectionErrors (called by
// both bootCredentials and executeReload, the only two callers of
// createStartupProvider) keeps any *CredentialRefError whose cause is NOT a
// *NotFoundError fatal, aborting boot/reload before this function is ever
// reached. So the only way m.APIKey() can be empty here is the NotFoundError
// case, and the wording is safe. Do not weaken reportInjectionErrors's
// fatal-on-non-NotFoundError behavior without also fixing this message —
// see the incident note on reportInjectionErrors (2026-08-15).
func defaultModelCredentialBlocked(cfg *config.Config) (string, bool) {
	pair := cfg.Agents.Defaults.DefaultModel
	if pair.IsZero() {
		return "", false
	}
	wantProvider := strings.TrimSpace(pair.Provider)
	wantModel := strings.TrimSpace(pair.Model)
	var missingRef string
	var candidates int
	for _, m := range cfg.Providers {
		if m == nil || strings.TrimSpace(m.Provider) != wantProvider || strings.TrimSpace(m.Model) != wantModel {
			continue
		}
		candidates++
		ref := strings.TrimSpace(m.APIKeyRef)
		if ref == "" || m.APIKey() != "" {
			return "", false // this entry is usable — nothing to block
		}
		if missingRef == "" {
			missingRef = ref
		}
	}
	if candidates == 0 || missingRef == "" {
		// No entry matches the default model name at all — that is
		// providers.CreateProvider's error to report ("model %q not found"),
		// not ours to pre-empt.
		return "", false
	}
	return fmt.Sprintf(
		"the default model %q cannot be used: its credential %q is missing from the credential vault — "+
			"re-enter the API key in Settings → Providers (or remove the stale provider entry)",
		pair.String(), missingRef,
	), true
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

	// /loop time-driven scheduler (ADR-049 D6/D7, Wave 2-C2): a SECOND
	// dedicated CronService instance, mirroring TaskTrigger's own pattern
	// immediately above — orthogonal to the gateway's user-schedules
	// service. No boot reconcile needed: unlike task triggers (derived from
	// the task store), /loop jobs already persist their own cron store and
	// their session-side UnifiedMeta state independently; a job whose
	// session lost its loop state self-removes on next fire
	// (LoopScheduler.RunScheduled).
	loopSchedStorePath := filepath.Join(homePath, "loops", "jobs.json")
	runningServices.LoopScheduler = agent.NewLoopScheduler(loopSchedStorePath, agentLoop)
	if startErr := runningServices.LoopScheduler.Start(); startErr != nil {
		return nil, fmt.Errorf("error starting loop scheduler: %w", startErr)
	}
	agentLoop.SetLoopScheduler(runningServices.LoopScheduler)
	fmt.Println("✓ Loop scheduler started")

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
	if epErr != nil && !errors.Is(epErr, errEgressProxyDisabled) {
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
	runningServices.browserWS = browserWSHandler
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
		approvalTimeoutDur = defaultToolApprovalTimeout
	}
	approvalReg := newApprovalRegistryV2(effectiveCap, approvalTimeoutDur)
	wsHandler.approvalRegV2 = approvalReg

	// Wire the policy approver into the agent loop (FR-011, C3).
	// The adapter bridges agent.PolicyApprover → approvalRegistryV2 + WSHandler.
	agentLoop.SetToolApprover(newPolicyApproverAdapter(approvalReg, wsHandler))

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

	// REST API endpoints for frontend data.
	//
	// M3: sample the onboarding state file's READABILITY before constructing
	// the manager. onboarding.NewManager treats every load failure as a fresh
	// install (and renames an unparseable file aside), so this is the only
	// moment at which "corrupt/unreadable" is distinguishable from "genuinely
	// never onboarded". The FR-050 pre-auth provider routes fail closed on
	// the unknown case — see preAuthOnboardingWindowOpen (rest_auth.go).
	onboardingStateUnknown := onboardingStateUnreadable(homePath)
	onboardingMgr := onboarding.NewManager(homePath)
	tStore := agent.GetTaskStore(agentLoop)
	tExecutor := agent.GetTaskExecutor(agentLoop)

	// ADR-049 D1/D4 (Wave 2-C1): construct the Plan store + the single hybrid
	// plan-engine instance. planStore is shared with restAPI (Plans REST
	// surface, rest_plans.go) AND with the engine itself — both write through
	// the SAME *plan.Store, so planStore.OnChange (wired here) is the single
	// choke point that emits a plan_status WS frame for every plan mutation,
	// regardless of whether the engine or a REST handler made it.
	planStore := plan.New(filepath.Join(homePath, "plans"))
	planStore.OnChange = func(p *plan.Plan) {
		progress := p.Progress
		if tStore != nil {
			if _, _, computed, cerr := plan.ComputeProgress(p.ID, tStore); cerr == nil {
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
		agentLoop.EmitPlanStatusChanged(payload)
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
	agentLoop.SetPlanStore(planStore)

	// Channel ownership for send_message (ADR-065). Injected here, next to the
	// plan store, for the same reason: it reads live config, so pkg/agent
	// cannot construct it without importing pkg/gateway. Until this runs
	// send_message refuses every target except the turn's own conversation.
	agentLoop.SetChannelOwnership(newChannelOwnershipResolver(agentLoop.GetConfig))
	if agentLoop.GetPlanStore() == nil {
		return nil, fmt.Errorf("gateway: plan store wiring failed — SetPlanStore did not install a non-nil store")
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
	if tExecutor != nil {
		tExecutor.SetPlanStore(planStore)
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
	intentLogChainKey, ilKeyErr := credStore.DeriveSubkey(plan.IntentLogChainKeyInfo)
	if ilKeyErr != nil {
		return nil, fmt.Errorf("gateway: failed to derive intent log HMAC chain key: %w", ilKeyErr)
	}
	lifecycleStore := session.NewLifecycleStore(filepath.Join(homePath, "session_lifecycle"))
	intentLog, ilDirErr := plan.NewIntentLog(filepath.Join(homePath, "plan_intents"), intentLogChainKey)
	if ilDirErr != nil {
		return nil, fmt.Errorf("gateway: failed to create intent log dir: %w", ilDirErr)
	}
	bootSweepCfg := agentLoop.GetConfig().Planning

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
	messageInboxStore := session.NewMessageInboxStore(filepath.Join(homePath, "session_messages"))
	// Apply the live config's caps to the store (the store's own caps are
	// plain fields, re-read per call). This boot-time application alone does
	// NOT make a session_messaging edit hot-reload — restartServices
	// (pkg/gateway/gateway.go) re-applies these same five fields from
	// al.GetMessageInboxStore() on every config reload; that is what actually
	// keeps a live edit in effect. See restartServices' own comment at that
	// call site for the incident this split (boot-only vs boot+reload) fixed.
	smCfg := agentLoop.GetConfig().SessionMessaging
	messageInboxStore.ChildSendRatePerMinute = smCfg.EffectiveChildSendRatePerMinute()
	messageInboxStore.ChildSendBodyBytes = smCfg.EffectiveChildSendBodyBytes()
	messageInboxStore.ChildSendMaxDepth = smCfg.EffectiveChildSendMaxDepth()
	messageInboxStore.InboxUnackedMax = smCfg.EffectiveInboxUnackedMax()
	messageInboxStore.InboxPerTypeCeiling = smCfg.EffectiveInboxPerTypeCeiling()
	agentLoop.SetSessionMessagingStores(messageInboxStore, lifecycleStore)
	if agentLoop.GetMessageInboxStore() == nil {
		return nil, fmt.Errorf("gateway: session-messaging store wiring failed — SetSessionMessagingStores did not install a non-nil inbox")
	}
	fmt.Println("✓ Session-messaging plane wired (delegate + message_parent stores injected)")

	// Mirrors the TaskDrain/TaskTrigger/MailboxDrain degrade-not-abort
	// convention immediately below/above for a missing task store/executor
	// (e.g. a minimal test harness's AgentLoop) — the plan engine needs both.
	if tStore != nil && tExecutor != nil {
		planEngine := agent.NewPlanEngine(agentLoop, planStore, tStore, tExecutor)
		// Boot-sweep + intent-log wiring (must precede Start so the first boot
		// pass runs synchronously inside Start).
		planEngine.SetLifecycleStore(lifecycleStore)
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
		tExecutor.SetLifecycleStore(lifecycleStore)
		planEngine.SetIntentLog(intentLog)
		// D13/G-12 Play-from-commit: install the gitevidence-backed resume
		// resolver so Play resumes a failed/cancelled member from its last
		// boundary commit (FR-144). The resolver degrades PER WORKSPACE
		// (nested-repo / no-commit / unmaterialized work dir -> fresh attempt,
		// FR-155), so it is wired unconditionally; a nil resolver here would
		// mask a valid evidence repo on one workspace with a nested-repo
		// degrade on another. It resolves the workspace lazily from the task
		// record at Play time, so no workspace needs to be open at boot.
		planEngine.SetCommitResolver(agent.NewLastMemberCommitResolver(tStore, homePath))
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
		scanner, scanErr := audit.NewSecretScanner(cfg.SensitiveDataReplacer(), nil)
		switch {
		case scanErr != nil:
			slog.Error("evidence committer: secret scanner construction failed — "+
				"boundary commits disabled, Play will always take the fresh-attempt path",
				"error", scanErr)
		default:
			tExecutor.SetEvidenceCommitter(agent.NewWorkspaceEvidenceCommitter(homePath, scanner))
		}
		if bsec := bootSweepCfg.EffectiveBootSweepBudgetSeconds(); bsec > 0 {
			planEngine.SetBootSweepBudget(time.Duration(bsec) * time.Second)
		}
		if smb := bootSweepCfg.EffectiveSnapshotMaxBytes(); smb > 0 {
			planEngine.SetSnapshotMaxBytes(smb)
		}
		// session.failed hook: best-effort recovery signal. The plan engine's
		// own tick loop re-arms idle settlement after Start; this hook is where
		// a future event-bus emission of session.failed would plug in.
		planEngine.SetSessionFailedHook(func(sessionID, reason string) {
			slog.Info("gateway: boot sweep: session.failed", "session_id", sessionID, "reason", reason)
		})
		// Wave 2-C2 supplies the real /goal and /loop active-loop counters via
		// these exact call sites; until then they contribute 0 to the R5
		// global active-loop cap (documented boot-ordering requirement on
		// PlanEngine.RegisterActiveCounter's doc comment). Wave 2-C2 (ADR-049
		// D6/D7, R5): "goal" counts sessions carrying an active
		// UnifiedMeta.GoalCondition in the shared session store (the only
		// store /goal can ever write to — it requires a live
		// TranscriptStore/TranscriptSessionID, which for every webchat/
		// channel turn resolves to GetSessionStore()'s shared store, see
		// resolveOrCreateChannelSession / the WS message handler's session
		// minting); "loop" counts currently-enabled cron jobs owned by the
		// dedicated LoopScheduler (constructed above, before this block).
		planEngine.RegisterActiveCounter("goal", func() (int, error) {
			store := agentLoop.GetSessionStore()
			if store == nil {
				return 0, nil
			}
			sessions, listErr := store.ListSessions()
			if listErr != nil {
				return 0, fmt.Errorf("active-goal counter: list sessions: %w", listErr)
			}
			count := 0
			for _, s := range sessions {
				if s != nil && s.GoalCondition != "" {
					count++
				}
			}
			return count, nil
		})
		planEngine.RegisterActiveCounter("loop", func() (int, error) {
			if runningServices.LoopScheduler == nil {
				return 0, nil
			}
			return len(runningServices.LoopScheduler.ListEnabledJobs()), nil
		})
		if startErr := planEngine.Start(context.Background()); startErr != nil {
			return nil, fmt.Errorf("error starting plan engine: %w", startErr)
		}
		agentLoop.SetPlanEngine(planEngine)
		runningServices.PlanEngine = planEngine
		fmt.Println("✓ Plan engine started")
	} else {
		fmt.Println("⚠ Plan engine disabled: task store/executor unavailable")
	}

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

	// ADR-067 T067-07: the ONE provider catalog for this process was booted
	// in Run (before NewAgentLoop, so every agent's window resolution sees
	// rung 5) and installed on the agent loop. Read it back rather than
	// building a second one — the embedded snapshot is 2 MB and parsing it
	// twice would double both boot cost and resident memory for no gain.
	// nil only in tests that construct services without the boot path; every
	// consumer below treats nil as "no catalog", never a 500.
	providerCatalog := agentLoop.GetCapabilityCatalog()

	api := &restAPI{
		agentLoop:       agentLoop,
		providerCatalog: providerCatalog, // ADR-067: the booted catalog (nil in non-boot tests)
		allowedOrigin:   allowedOrigin,
		onboardingMgr:   onboardingMgr,
		// M3: "unknown" is not "fresh install" — see the field's doc comment.
		onboardingStateUnknown: onboardingStateUnknown,
		homePath:               homePath,
		taskStore:              tStore,
		taskExecutor:           tExecutor,
		planStore:              planStore, // ADR-049 D1: Plans REST surface (rest_plans.go) + plan_id FK check
		credStore:              credStore,
		mediaStore:             runningServices.MediaStore,
		ssrfChecker:            agent.GetSSRFChecker(agentLoop), // SEC-24: nil when SSRF disabled
		sandboxResult:          sandboxResult,                   // immutable post-boot snapshot
		appliedConfig:          mustDeepCopyConfig(cfg),         // boot-time snapshot for pending-restart diff
		servedSubdirs:          runningServices.servedSubdirs,   // web_serve static-mode token registry
		devServers:             runningServices.devServers,      // web_serve dev-mode process registry
		approvalReg:            approvalReg,                     // in-process tool-approval registry (FR-016)
		builtinRegistry:        builtinReg,                      // M16: central builtin registry (FR-001)
		mcpRegistry:            mcpReg,                          // M16: central MCP registry (FR-001)
		skillRegistry:          skillRegistry,                   // ClawHub marketplace (search + install-by-slug)
		allowGodMode:           allowGodMode,                    // god-mode latch (2)
		notifStore:             runningServices.notifStore,      // #264: notification center
		auditor:                agentLoop.AuditLogger(),         // shared audit logger for REST mutations
		selfWriteReg:           selfWriteReg,                    // suppress watcher reload on app-initiated writes
		taskLock:               task.TaskFileLock,               // shared striped lock for board task RMW
	}
	api.cronService.Store(runningServices.CronService) // #264: schedules CRUD (atomic.Pointer)
	// ADR-067 FR-037 (T067-11): a catalog refresh invalidates the
	// entitlement cache — the intersection behind every cached answer was
	// computed against a document that is no longer the served one.
	registerEntitlementCacheInvalidation(providerCatalog, api)
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
	runningServices.reloadTrigger = newReloadTrigger(runningServices, agentLoop)
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
	go runCatalogRefreshLoop(
		ctx,
		providerCatalog,
		catalog.NewFileStore(homePath),
		catalogRefreshInterval,
		catalogRefreshTimeout,
		catalogStartupSkipWindow,
	)
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
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
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
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()

	return runningServices, nil
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
	if runningServices.PlanEngine != nil {
		runningServices.PlanEngine.Stop()
	}
	if runningServices.TaskTrigger != nil {
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
	homePath := runningServices.homePath

	if runningServices.notifStore == nil {
		// Derive the home dir from the workspace path (workspace == <home>/workspace).
		runningServices.notifStore = notifications.NewStore(
			filepath.Join(homePath, "notifications"),
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

	// Restart the SAME plan-engine instance (ADR-049 D4) — unlike CronService,
	// the engine is not reconstructed on reload: it was Stop()'d in
	// stopAndCleanupServices(isReload=true) above, and Start() on an
	// already-constructed *PlanEngine is safe to call again (fresh stopCh,
	// re-subscribes to the event bus, re-runs boot reconciliation). taskStore/
	// taskExecutor are themselves stable across a reload (owned by the SAME
	// al, never recreated), so there is nothing to re-wire.
	if runningServices.PlanEngine != nil {
		if startErr := runningServices.PlanEngine.Start(context.Background()); startErr != nil {
			return fmt.Errorf("error restarting plan engine: %w", startErr)
		}
		fmt.Println("  ✓ Plan engine restarted")
	}

	// Restart the task time-trigger scheduler on its dedicated CronService. The
	// previous instance was already Stop()'d in stopAndCleanupServices(isReload).
	if tStore := agent.GetTaskStore(al); tStore != nil {
		triggerStorePath := filepath.Join(homePath, "tasks_triggers", "jobs.json")
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

	// Restart the /loop scheduler on a fresh dedicated CronService (ADR-049
	// D6/D7). The previous instance was already Stop()'d in
	// stopAndCleanupServices(isReload) — mirrors the task trigger restart
	// immediately above.
	{
		loopSchedStorePath := filepath.Join(homePath, "loops", "jobs.json")
		runningServices.LoopScheduler = agent.NewLoopScheduler(loopSchedStorePath, al)
		if startErr := runningServices.LoopScheduler.Start(); startErr != nil {
			return fmt.Errorf("error restarting loop scheduler: %w", startErr)
		}
		al.SetLoopScheduler(runningServices.LoopScheduler)
		fmt.Println("  ✓ Loop scheduler restarted")
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
	if inbox := al.GetMessageInboxStore(); inbox != nil {
		smCfg := cfg.SessionMessaging
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

// runBrowserCacheTrimSchedule is ADR-072 FR-072 triggers 2 and 3: the boot sweep
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
