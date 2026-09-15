// gateway_boot.go: Boot - unlock credentials, load souls, seed the roster, build services

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
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
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/state"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/voice"
)

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

// persistSeededSkillGrants durably records config.seeded_skill_grants (the
// ADR-074 D4 one-shot migration markers coreagent.SeedConfig checks) into
// config.json's raw JSON map, preserving every other key exactly as-is — the
// same read-modify-write convention as persistFreshInstallDefaultAgentID
// above, and for the same reason: SeedConfig is a pure config-struct mutation
// with zero filesystem side effects, so without this step the marker lives
// only in THIS process's in-memory cfg and the migration would re-run on
// every boot (harmless in effect — it is additive and append-if-lacking — but
// it would defeat the marker's "run once, recorded" contract and rewrite
// config.json every boot).
//
// Idempotent at the byte level: when the on-disk key already equals the
// in-memory value the file is left completely untouched (no write, no mtime
// churn), making the second boot a byte-level no-op (judgment-first spec
// test 16).
func persistSeededSkillGrants(configPath string, markers []string) error {
	return persistConfigMarkerList(configPath, "seeded_skill_grants", markers)
}

// persistSeededToolPolicyUpdates durably records
// config.seeded_tool_policy_updates (the one-time seeded tool-policy update
// markers coreagent.SeedConfig checks, e.g. the Worker goal_claim update)
// into config.json. Same contract as persistSeededSkillGrants: raw-map
// read-modify-write that preserves every other key, and no write at all when
// the on-disk value already matches.
func persistSeededToolPolicyUpdates(configPath string, markers []string) error {
	return persistConfigMarkerList(configPath, "seeded_tool_policy_updates", markers)
}

// persistConfigMarkerList writes markers under the top-level config.json key
// key, preserving every other key exactly as-is, and skips the write entirely
// when the on-disk list already equals markers (byte-level no-op on a
// settled install).
func persistConfigMarkerList(configPath, key string, markers []string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return fmt.Errorf("parse config: %w", unmarshalErr)
	}
	// Skip the write entirely when the on-disk value already matches.
	if existing, ok := m[key].([]any); ok && len(existing) == len(markers) {
		same := true
		for i := range markers {
			if s, isStr := existing[i].(string); !isStr || s != markers[i] {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}
	m[key] = markers
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}
	if err := fileutil.WriteFileAtomic(configPath, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// deleteOrphanedDefineDoneDir deletes the orphaned define-done/ skill
// directory left behind by the ADR-080 D-SKILL define-goal rename — but
// ONLY when the replacement define-goal/ directory is verifiably present on
// disk (fix-wave finding #1, operator-ratified 2026-09-07 Q3). The
// migration marker (SkillsMigrationDefineGoalRename) alone is NOT
// sufficient evidence the rename actually landed: skills.SeedDefaults can
// fail (disk full, permissions, a corrupt embed) after the marker was
// already recorded in SeededSkillGrants, and deleting define-done/ on the
// marker's say-so alone would leave a fresh boot with NEITHER directory on
// disk — loadDefineGoalSkillContent silently returns "" in that state, and
// every `/goal` compile silently loses its quality bar with no observable
// signal at compile time.
//
// The safe order is: marker present -> define-goal/ verifiably present ->
// ONLY THEN delete define-done/. Any other combination fails SAFE (not
// open): define-done/ is left untouched and the reason is returned so the
// caller can WARN. Returns deleted=true only when define-done/ was actually
// removed by THIS call; a repeat call after a successful deletion (or when
// the marker is absent, or define-done/ was never there) is a clean, silent
// no-op (deleted=false, err=nil) — idempotent by the directories' own
// on-disk state, never a second marker.
func deleteOrphanedDefineDoneDir(skillsGlobalDir string, markers []string) (deleted bool, err error) {
	renamed := false
	for _, m := range markers {
		if m == coreagent.SkillsMigrationDefineGoalRename {
			renamed = true
			break
		}
	}
	if !renamed {
		// A pre-ADR-080 install that has not yet run the rename never
		// reaches this branch — its define-done/ stays untouched until its
		// own boot actually rewrites its allowlists.
		return false, nil
	}

	defineGoalDir := filepath.Join(skillsGlobalDir, "define-goal")
	if _, statErr := os.Stat(defineGoalDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return false, fmt.Errorf(
				"replacement define-goal/ skill directory not found at %s (SeedDefaults may have failed) "+
					"— preserving define-done/ rather than deleting it", defineGoalDir)
		}
		return false, fmt.Errorf("could not stat replacement define-goal skill directory %s: %w", defineGoalDir, statErr)
	}

	orphanedDir := filepath.Join(skillsGlobalDir, "define-done")
	if _, statErr := os.Stat(orphanedDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil // already deleted (or never existed) — clean no-op
		}
		return false, fmt.Errorf("could not stat orphaned define-done skill directory %s: %w", orphanedDir, statErr)
	}

	if rmErr := os.RemoveAll(orphanedDir); rmErr != nil {
		return false, fmt.Errorf("could not delete orphaned define-done skill directory %s: %w", orphanedDir, rmErr)
	}
	return true, nil
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
// resolves to (ADR-075 FR-016b). It returns (nil, reason) when there is
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

// warmBootListenerDialTimeout / warmBootListenerRetryDelay pace that wait.
const (
	warmBootListenerDialTimeout = 500 * time.Millisecond
	warmBootListenerRetryDelay  = 100 * time.Millisecond
)

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
		// It is the WORKSPACE-OWNED tab set, not an agent's: under ADR-075
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

type startupBlockedProvider struct {
	reason string
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

// The production values for the ADR-067 FR-008 background refresh: one pull
// every 24 h, each attempt bounded to 30 s, and a startup pull skipped
// entirely when the persisted last-known-good was written less than an hour
// ago.
const (
	catalogRefreshInterval   = 24 * time.Hour
	catalogRefreshTimeout    = 30 * time.Second
	catalogStartupSkipWindow = time.Hour
)

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
	// Broadcast every pending→terminal transition, whatever caused it (a
	// decision from any tab, timeout, Stop, agent deletion, shutdown), so no
	// open tab keeps a dialog for an approval the server has already closed.
	approvalReg.setResolutionListener(wsHandler.broadcastToolApprovalResolved)

	// Wire the policy approver into the agent loop (FR-011, C3).
	// The adapter bridges agent.PolicyApprover → approvalRegistryV2 + WSHandler.
	agentLoop.SetToolApprover(newPolicyApproverAdapter(approvalReg, wsHandler))

	// AskUserQuestion pending registry (askuserquestion-tool-spec v3, ADR-074
	// D4b; W9b wiring): durable state lives in each owner session's
	// UnifiedMeta (pending_ask), the in-process registry mirrors it with the
	// global cap + default-safe timers, the card sink broadcasts
	// ask_user_question WS frames, and the resume dispatcher publishes the
	// §0.2 answers message back into the owner session's turn machinery.
	if sharedStore := agentLoop.GetSessionStore(); sharedStore != nil {
		askSink := &askUserCardSink{h: wsHandler}
		askReg := askuser.NewRegistry(
			sharedStore,
			&askUserResumeDispatcher{msgBus: msgBus},
			askuser.Options{
				Sink:  askSink,
				Audit: &askUserAuditSink{al: agentLoop},
			},
		)
		askSink.delayFn = askReg.EffectiveDefaultSafeDelay
		wsHandler.askUserReg = askReg
		agentLoop.SetAskUserRegistry(askReg)
		// ADR-088 FR-031: wire the goal-routing store resolver at boot so a
		// cold-start channel record echo / keeper action can rehydrate the
		// persisted GoalRoute* fields before any /goal command runs.
		agentLoop.SetGoalRouteSessionStore()
		// Goal outcome line: a task-owned goal that ends with its task leaves
		// the same lasting outcome line in the task's run session as a chat
		// goal does (pkg/agent/goal_outcome.go).
		agentLoop.InstallTaskGoalOutcomeRecorder()
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
			<-ctx.Done()
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
			goalStore := goal.NewStore(homePath)
			active, listErr := goalStore.ListActiveByOwnerKind(gen.GoalOwnerKindSession)
			if listErr != nil {
				return 0, fmt.Errorf("active-goal counter: list active session-owned goals: %w", listErr)
			}
			return len(active), nil
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
		liveTaskActivity:       tExecutor, // founder decision 2026-09-14: Task.last_activity_at
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
	// D-107: the Library REST write handlers broadcast a library_changed WS
	// frame after every landed mutation, so a second tab's folder listing
	// reconciles without a reload. wsHandler was built earlier in boot; store
	// its broadcast method behind the restAPI's nil-safe hook
	// (library_change_broadcast.go) — nil until here, no-op after shutdownless
	// tests that never wire it.
	libraryChangeFn := func(f gen.LibraryChangedFrame) { wsHandler.broadcastLibraryChange(f) }
	api.libraryChangeBroadcast.Store(&libraryChangeFn)
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

	// ADR-067 W3 (FR-030..FR-034a, FR-038a, FR-039, FR-080): open the index for
	// every already-mounted knowledge base, push indexing progress over the
	// WebSocket, and start each collection's drift schedule. Runs after the
	// workspaces are ensured (it reads their mount records) and before the
	// listener accepts connections. Interval 0 means FR-038a's six-hour default:
	// there is no config key for it yet, and KnowledgeLifecycleOptions.DriftInterval
	// is where one would be passed in.
	startKnowledgeLifecycle(homePath, wsHandler, 0,
		knowledgeDriftNotifier(runningServices.notifStore, agentLoop, agentLoop.GetConfig))

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
	// The allow-list accessor is a closure over the LIVE config (ADR-083
	// EMB-081): gateway.video_embed_hosts is read per response, so an operator
	// who empties it stops the external frame source reaching the served policy
	// on the next page load rather than at the next restart. The same resolver
	// feeds GET /api/v1/state, which is what keeps the browser's list and the
	// browser's policy equal (EMB-080).
	if spaHandler := newSPAHandler(func() []string {
		return ResolveVideoEmbedHosts(agentLoop.GetConfig())
	}); spaHandler != nil {
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
	// Cancel-and-wait, not fire-and-forget: the loop stops on ctx, but
	// RunContext must not return while a refresh is still between "pull
	// completed" and "file written". startCatalogRefreshLoop hands shutdown
	// a cancel plus a done channel it waits on (step 1, shutdown.go).
	runningServices.catalogRefreshCancel, runningServices.catalogRefreshDone = startCatalogRefreshLoop(
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
