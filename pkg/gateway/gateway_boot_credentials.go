// gateway_boot_credentials.go: Unlock the credential store and resolve secret bundles at boot, including the blocked-provider fallback when the default model's credential never resolves

package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
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
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
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
// Known per-entry authentication or encoding damage is deferred until the
// independent store health check: another decryptable entry permits a loud,
// scoped degradation. A wrong master key, unreadable document, locked store or
// unknown failure still refuses boot/reload. reportInjectionErrors handles
// provider attribution separately, so those refs do not belong in this map.
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
	// Boot-time asymmetry (resolved 2026-10-13 — see the #1277 notes on
	// bootCredentials): a dangling ref on an ENABLED channel used to abort
	// boot fatally (the "fatal: enabled credential ..." branch); since the
	// #1277 founder decision it degrades only that channel — visibly, naming
	// the ref — exactly like the enabled+globally-enabled MCP server case,
	// where reconcileLocked logs a WARN and skips connecting just that server,
	// leaving the rest of boot to proceed normally. Both integration classes
	// now share the same degrade-not-fatal semantics; only store-level
	// failures stay fatal.
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

// reportInjectionErrors logs every credentials.InjectFromConfig failure at
// ERROR and returns the ones that must stop the caller (boot or reload) —
// plus, separately, known per-entry authentication or encoding failures whose
// fatal-vs-scoped verdict must wait until it has checked the store's health
// (#1277, see credentials.Store.HealthCheck).
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
//   - An *EntryAuthError cause (which unwraps to ErrWrongKey) is AMBIGUOUS at
//     injection time: a single tampered entry and a wrong whole-store master
//     key produce the identical per-ref error shape, and EntryAuthError's own
//     doc comment says the discriminating evidence is SCOPE — a wrong key
//     fails every entry, a tampered one fails only itself. Since #1277
//     (founder decision 2026-10-12) that class is therefore NOT decided here:
//     it comes back as deferred. The caller checks the store independently of
//     the configured refs: at least one decryptable entry, even unreferenced,
//     proves the store is readable and this error can be scoped. No decryptable
//     entry in a nonempty store means a whole-store failure, which stays fatal.
//
//   - An *EntryEncodingError identifies malformed Base64 or nonce length in
//     one entry. It is deferred to the same health decision, including when
//     a provider or mailbox references it and injection runs before HealthCheck.
//     With a decryptable sibling it degrades only the affected consumer, just
//     like an unreadable channel entry found during bundle resolution.
//
//   - Everything else is STORE-WIDE, and stays fatal: the bare
//     credentials.ErrStoreLocked (store never unlocked — short-circuited
//     before this function is even reached, see InjectFromConfig), a corrupt
//     credentials.json parse failure, an os.Setenv failure, or any other
//     cause. This exactly mirrors rest.go's describeCredentialResolutionError,
//     which classifies the same cases for the REST credential-resolution path.
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
func reportInjectionErrors(errs []error, phase string) (fatal []error, deferredAuth []error) {
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
			var entryAuth *credentials.EntryAuthError
			var entryEncoding *credentials.EntryEncodingError
			if errors.As(refErr.Err, &entryAuth) || errors.As(refErr.Err, &entryEncoding) {
				// The caller's store health check decides whether this entry's
				// authentication or encoding failure is scoped or whole-store.
				deferredAuth = append(deferredAuth, e)
				continue
			}
			// An unclassified configured-ref failure (including a corrupt store
			// document or env failure) stays fatal.
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
	return fatal, deferredAuth
}

// splitEntryAuthFailures extracts per-entry authentication and encoding
// failures for the store-health decision. Both are scoped only with a
// decryptable witness; file, lock and unknown failures remain in rest.
func splitEntryAuthFailures(errs []error) (auth, rest []error) {
	for _, e := range errs {
		var entryAuth *credentials.EntryAuthError
		var entryEncoding *credentials.EntryEncodingError
		if errors.As(e, &entryAuth) || errors.As(e, &entryEncoding) {
			auth = append(auth, e)
			continue
		}
		rest = append(rest, e)
	}
	return auth, rest
}

// errCredentialStoreUnreadableClass marks a refusal whose cause is the
// store-level failure class, so /health's degraded reason can name the store
// without leaking the underlying error detail (see reloadDegradedStatus).
var errCredentialStoreUnreadableClass = errors.New("credential store unreadable")

// storeHealthFailure turns a credential-store health result into the
// boot/reload/refresh decision: nil when the store is healthy, nil (after an
// ERROR log naming them) when only some entries are damaged and the store is
// otherwise readable — a visible, scoped failure, never a whole-store refusal
// (founder 2026-10-15, WU-4) — and the error to refuse with otherwise.
func storeHealthFailure(phase string, healthErr error) error {
	if healthErr == nil {
		return nil
	}
	var damaged *credentials.DamagedEntriesError
	if errors.As(healthErr, &damaged) {
		slog.Error(phase+": damaged credential entries cannot be read — re-enter them; "+
			"the rest of the credential store is readable and the system continues",
			"credential_refs", damaged.Names, "error", healthErr)
		return nil
	}
	return healthErr
}

// fatalStoreWideAuthFailure refuses boot, reload or refresh on a store-level
// failure. The sentinel supports a fixed public health reason; cause preserves
// the typed store error, and refErrs adds any known configuration attribution.
func fatalStoreWideAuthFailure(cause error, refErrs []error) error {
	slog.Error(
		"credential store is unreadable — this is a whole-store failure "+
			"(wrong master key, corrupted or locked store), "+
			"not a single integration's missing secret; refusing to continue",
		"error", cause.Error(),
	)
	for _, e := range refErrs {
		var refErr *credentials.CredentialRefError
		if errors.As(e, &refErr) {
			slog.Error(
				"credential store unreadable for a configured ref — the whole store failed to "+
					"decrypt under this master key, not just this entry",
				"scope", refErr.Scope,
				"owner", refErr.Owner,
				"workspace", refErr.SubOwner,
				"credential_ref", refErr.Ref,
				"error", refErr.Err,
			)
			continue
		}
		slog.Error(
			"credential store unreadable for a configured ref — the whole store failed to "+
				"decrypt under this master key, not just this entry",
			"error", e,
		)
	}
	joined := append([]error{cause}, refErrs...)
	return fmt.Errorf("fatal: %w (store-level failure — wrong master key, corrupted or locked store): %w",
		errCredentialStoreUnreadableClass, errors.Join(joined...))
}

// logScopedCredentialFailures logs entry-auth failures that the store health
// check proved scoped — another entry decrypts — as loud per-integration
// degradations (#1277), naming the config entry and the credential reference
// wherever the error shape carries them.
func logScopedCredentialFailures(phase string, authErrs []error) {
	for _, e := range authErrs {
		var refErr *credentials.CredentialRefError
		if errors.As(e, &refErr) {
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
		slog.Error(
			phase+": credential unusable — the referencing entry will not work until it is fixed, "+
				"the rest of the system continues",
			"error", e,
		)
	}
}

// bootCredentials runs the canonical credential + config boot sequence and
// returns the initialized config, secret bundle, and store.
//
// Sequence (matches ADR-004 §Boot Order Contract):
//  1. NewStore → Unlock (fatal on failure)
//  2. LoadConfigWithStore (fatal on failure)
//  3. EditionMisbuild tripwire (fatal on failure — ADR-0010): a core-stamped
//     binary that has a platform trust anchor configured is a hosted or
//     desktop build that lost its edition stamp, and must never serve the
//     password login that combination implies.
//  4. InjectFromConfig for provider env-vars (fatal only on a store-wide
//     failure; a single unresolvable ref is an ERROR + degraded entry — see
//     reportInjectionErrors)
//  5. ResolveBundle for channel secrets (#1277, founder decision 2026-10-12:
//     a missing or single-entry-unreadable secret degrades only that
//     integration — loud ERROR + the integration's visible error state; a
//     whole-store entry-auth failure and unclassified shapes stay fatal)
//  6. cfg.RegisterSensitiveValues with all resolved plaintexts
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

	// ADR-0010 boot tripwire: refuse to serve a misbuilt binary. See
	// config.EditionMisbuild's doc for the two shapes this refuses.
	if reason := config.EditionMisbuild(cfg.Security.PlatformAuth.Issuer); reason != "" {
		return nil, nil, nil, fmt.Errorf("refusing to start: %s", reason)
	}

	// Inject provider API keys into the process environment so LLM SDK clients
	// can read them via os.Getenv. Channels use SecretBundle instead (no env injection).
	//
	// A single unresolvable ref no longer aborts boot — it is logged at ERROR
	// and that provider/mailbox is left unusable, so the operator can start the
	// gateway and fix it in the UI. A store-wide failure is still fatal. See
	// reportInjectionErrors for the full rationale and the incident behind it.
	// Entry-auth failures come back deferred until the unconditional store
	// health check below decides their scope (#1277).
	var deferredAuth []error
	if errs := credentials.InjectFromConfig(cfg, credStore); len(errs) > 0 {
		fatal, auth := reportInjectionErrors(errs, "boot")
		deferredAuth = append(deferredAuth, auth...)
		if len(fatal) > 0 {
			return nil, nil, nil, fmt.Errorf(
				"fatal: provider credential injection failed — ensure OMNIPUS_MASTER_KEY is set and all referenced credentials exist: %w",
				errors.Join(fatal...),
			)
		}
	}

	// ADR-096 D11: the web-search roles migration runs HERE — after
	// injection (each provider's APIKey() reads the config default, process
	// environment populated by InjectFromConfig) and before the agent loop
	// builds its tools. Deliberately NOT a config-load migration: the
	// environment is empty during config load, so a load-time migration
	// would read no keys and record duckduckgo for every upgrade, permanently.
	config.MigrateWebSearchRoles(cfg, configPath, nil)

	// Enabled missing refs are logged at ERROR; disabled/unused ones at INFO.
	// Neither is a whole-store failure.
	enabledRefs := buildEnabledRefMap(cfg)

	// Resolve all credential refs into a SecretBundle. Channels receive secrets
	// via the bundle — no os.Setenv for channel credentials (B1 fix).
	bundle, bundleErrs := credentials.ResolveBundle(cfg, credStore)

	bundleAuth, bundleRest := splitEntryAuthFailures(bundleErrs)
	// The store, not the configured refs or resolved bundle, decides whole-store
	// health. Run even with only MCP refs or no refs; an absent file is healthy.
	if healthErr := storeHealthFailure("boot", credStore.HealthCheck()); healthErr != nil {
		return nil, nil, nil, fatalStoreWideAuthFailure(healthErr,
			append(append([]error{}, deferredAuth...), bundleAuth...))
	}
	logScopedCredentialFailures("boot", deferredAuth)
	for _, e := range bundleAuth {
		slog.Error(
			"boot: credential unusable — the referencing entry will not work until it is fixed, "+
				"the rest of the system continues",
			"error", e,
		)
	}

	for _, e := range bundleRest {
		var notFound *credentials.NotFoundError
		if errors.As(e, &notFound) {
			if enabledRefs[notFound.Name] {
				// #1277: a missing credential on something actually
				// enabled/in-use (channel, voice, web-search tool, skill
				// marketplace) degrades only that integration — logged loud,
				// boot continues, and the affected channel/connector/provider
				// reports the ref-naming reason in its Settings entry.
				slog.Error(
					"boot: credential unusable — the referencing entry will not work until it is fixed, "+
						"the rest of the system continues",
					"credential_ref", notFound.Name,
					"error", e,
				)
				continue
			}
			slog.Info("credential not found (not currently enabled/in use)", "ref", notFound.Name)
			continue
		}
		// Do not lose a file/lock failure observed by resolution but not by
		// the health check (the store may have changed between reads).
		return nil, nil, nil, fatalStoreWideAuthFailure(e, nil)
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
