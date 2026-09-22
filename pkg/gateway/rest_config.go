// rest_config.go: Read and write config.json, credential refs, and service rewiring

package gateway

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// --- Config ---

// HandleConfig handles GET /api/v1/config and PUT /api/v1/config.
// Both verbs require only authentication (registered under withAuth in
// gateway.go): mutating gateway config can change ports, dev_mode_bypass, and
// provider settings, but under the single-account model the authenticated
// caller IS the sole account, so no further role gate applies.
func (a *restAPI) HandleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getConfig(w)
	case http.MethodPut:
		a.updateConfig(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) getConfig(w http.ResponseWriter) {
	cfg := a.agentLoop.GetConfig()

	// Marshal to JSON then unmarshal to a generic map so we can redact credential fields.
	raw, err := json.Marshal(cfg)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not serialize config")
		return
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not process config")
		return
	}

	// Redact any top-level field names that look like credentials.
	redactSensitiveFields(m)

	// Strip internal-only bookkeeping keys from the wire.
	sanitizeConfigForWire(m)

	jsonOK(w, m)
}

// wireExcludedConfigFields is the single place listing top-level config.json
// keys that are internal-only bookkeeping: they stay on disk but must never
// cross the wire. Current entries:
//
//   - seeded_skill_grants (judgment-first spec US-4 S6 / R2-04): records which
//     one-shot allowlist migrations have run on THIS install (ADR-074 D4) —
//     an implementation detail of the boot seed, not operator-facing config.
//   - seeded_tool_policy_updates: records which one-time updates to a seeded
//     agent's stored tool policy have run on THIS install (e.g. the Worker
//     goal_claim update, coreagent.ToolPolicyUpdateWorkerGoalClaimAllow) —
//     the same kind of boot-seed bookkeeping.
var wireExcludedConfigFields = []string{
	"seeded_skill_grants",
	"seeded_tool_policy_updates",
}

// sanitizeConfigForWire strips every wireExcludedConfigFields key from a
// decoded config map before it is served. Used by getConfig; any future
// endpoint that serves the raw config map must call it too, so the excluded
// list lives in exactly one place.
func sanitizeConfigForWire(m map[string]any) {
	for _, k := range wireExcludedConfigFields {
		delete(m, k)
	}
}

// redactSensitiveFields recursively redacts map values whose keys contain
// sensitive keywords. Credential data must live in credentials.json, not config.json,
// but this is a defense-in-depth measure per BRD SEC-23.
func redactSensitiveFields(m map[string]any) {
	sensitive := []string{"key", "token", "secret", "password", "credential", "api_key"}
	for k, v := range m {
		kl := strings.ToLower(k)
		for _, s := range sensitive {
			if strings.Contains(kl, s) {
				if str, ok := v.(string); ok && str != "" {
					m[k] = "[redacted]"
				}
				break
			}
		}
		if sub, ok := v.(map[string]any); ok {
			redactSensitiveFields(sub)
		}
		if arr, ok := v.([]any); ok {
			for _, elem := range arr {
				if subMap, ok := elem.(map[string]any); ok {
					redactSensitiveFields(subMap)
				}
			}
		}
	}
}

// configPath returns the path to config.json under the home directory.
func (a *restAPI) configPath() string {
	return filepath.Join(a.homePath, "config.json")
}

// resolveCredentialRef resolves a credential reference from the shared credential store.
// Returns an error if the store is locked or the ref is not found, so callers can
// surface a meaningful error instead of silently returning "".
func (a *restAPI) resolveCredentialRef(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			return "", fmt.Errorf("credential store locked: %w", err)
		}
	}
	value, err := credentials.ResolveRef(store, ref)
	if err != nil {
		return "", fmt.Errorf("credential store: %w", err)
	}
	return value, nil
}

// describeCredentialResolutionError classifies a resolveCredentialRef failure into
// an operator-facing message with the CORRECT remediation advice. fmt.Errorf's %w
// already preserves errors.As-compatibility through the wrap in resolveCredentialRef
// above — the bug this helper fixes is that callers never called errors.As and instead
// treated every non-nil error identically.
//
// Three semantically distinct causes exist:
//   - *credentials.NotFoundError: the ref itself is not in the store (stale/deleted
//     credential, a hand-edited config with a typo'd ref, …). Unlocking the vault
//     changes nothing here — the correct advice is to re-enter the API key.
//   - *credentials.EntryAuthError: the ref is present but its stored value does not
//     authenticate under that name — edited on disk, moved here from another entry,
//     or written by an older release that did not bind the name. Unlock-and-retry
//     would be the wrong advice (the store is open; the bytes are the problem), so
//     this names the entry and asks for it to be entered again.
//   - anything else (locked store, …): the vault genuinely could not be read.
//     This is transient — unlock and retry IS correct advice.
//
// Order matters: *EntryAuthError unwraps to credentials.ErrWrongKey, so it must be
// matched by type before the fallthrough.
func describeCredentialResolutionError(err error) string {
	var notFound *credentials.NotFoundError
	if errors.As(err, &notFound) {
		return "the configured credential reference no longer exists — re-enter the API key."
	}
	var entryAuth *credentials.EntryAuthError
	if errors.As(err, &entryAuth) {
		return fmt.Sprintf(
			"the stored value for credential %q did not authenticate — it was edited or moved "+
				"from another entry, or written by an older release — re-enter this credential.",
			entryAuth.Name,
		)
	}
	return "API key is configured but the credential vault could not be read " +
		"(store locked or undecryptable) — unlock and retry."
}

// storeCredential stores an API key in the encrypted credentials store and
// returns the credential reference name. Returns an error if the store is locked
// or unavailable — never falls back to plaintext (SEC-23).
func (a *restAPI) storeCredential(refName, apiKey string) (string, error) {
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			return "", fmt.Errorf(
				"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets: %w",
				err,
			)
		}
	}
	if err := store.Set(refName, apiKey); err != nil {
		return "", fmt.Errorf("failed to store API key in credentials store: %w", err)
	}
	return refName, nil
}

// credentialStoreReady reports whether the encrypted credential store can accept a
// write (unlocked / master key available), WITHOUT writing anything. It mirrors the
// readiness path of storeCredential so the PUT handler can return 503 BEFORE running a
// live key-validation probe — there is no point making a billable upstream call to
// validate a key we cannot persist (SEC-23: no plaintext fallback).
func (a *restAPI) credentialStoreReady() error {
	if a.credStore != nil {
		return nil // an already-unlocked store was injected
	}
	store := credentials.NewStore(a.credentialsStorePath())
	if err := credentials.Unlock(store); err != nil {
		return fmt.Errorf(
			"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets: %w",
			err,
		)
	}
	return nil
}

// channelCredKey is the credential-store key for a channel's secret field. The
// format is opaque to readers (channel constructors resolve secrets via the
// config <field>_ref, never by reconstructing this key); it exists so the
// producer side has a single definition.
func channelCredKey(channelID, field string) string {
	return "channel_" + channelID + "_" + field
}

// mcpEnvCredKey returns the canonical credential-store key for one MCP
// server's env var secret. MUST stay identical to
// pkg/sysagent/tools/mcp.go's mcpEnvCredKey ("mcp_<server>_<envKey>") — the
// two packages independently produce and resolve refs under the same key
// space (add_mcp_server / addMCPServer both write; pkg/mcp.ResolveServerEnvRefs
// reads regardless of which path created the ref), so the format cannot
// diverge between them.
func mcpEnvCredKey(serverName, envKey string) string {
	return "mcp_" + serverName + "_" + envKey
}

// removeStoredCredential removes refName from the credential store. A missing
// entry is not an error — clearing a never-set secret is legitimate. (Distinct
// from the deleteCredential REST handler, which writes an HTTP response.)
func (a *restAPI) removeStoredCredential(refName string) error {
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			return fmt.Errorf("credential store locked: %w", err)
		}
	}
	if err := store.Delete(refName); err != nil {
		var nf *credentials.NotFoundError
		if errors.As(err, &nf) {
			return nil
		}
		return err
	}
	return nil
}

// credentialRefResolves reports whether refName names a non-empty secret in the
// credential store. Used by testChannel (#289). The returned error is non-nil
// only for store-access faults (locked / wrong master key / I/O) — distinct from
// a genuinely absent secret, which returns (false, nil). The caller MUST surface
// a store fault separately rather than reporting the field as "missing", or
// "Test" would tell the user to re-enter a secret that is already correct.
func (a *restAPI) credentialRefResolves(refName string) (bool, error) {
	refName = strings.TrimSpace(refName)
	if refName == "" {
		return false, nil // genuinely not configured
	}
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			return false, fmt.Errorf("credential store locked: %w", err)
		}
	}
	v, err := store.Get(refName)
	if err != nil {
		var nf *credentials.NotFoundError
		if errors.As(err, &nf) {
			return false, nil // truly absent
		}
		return false, err // locked / wrong key / I/O — surface to the caller
	}
	return strings.TrimSpace(v) != "", nil
}

// safeUpdateConfigJSON reads config.json, applies a mutation function on the raw JSON map,
// and writes it back atomically. This preserves SecureStrings (API keys) that would be
// destroyed by config.SaveConfig's JSON round-trip through the Go struct.
//
// After a successful atomic write it calls refreshConfigAndRewireServices so the
// configSnapshotMiddleware picks up the new config immediately AND sensitive-data
// scrubbing is re-armed with the new credentials (A1+A2 fix). If the in-memory
// refresh fails the error is returned to the caller so the HTTP handler can surface
// a 500 rather than silently serving stale state.
func (a *restAPI) safeUpdateConfigJSON(mutate func(m map[string]any) error) error {
	// configMu serializes concurrent REST config writes (read-modify-write cycles).
	// Sysagent mutations go through MutateConfig (al.mu) with SaveConfigLocked,
	// which does not acquire configMu — so there is no lock ordering conflict.
	a.configMu.Lock()
	defer a.configMu.Unlock()
	return a.updateConfigJSONLocked(mutate)
}

// updateConfigJSONLocked is safeUpdateConfigJSON's body with the configMu
// acquisition factored out. Callers that must validate a candidate config
// snapshot and persist it as ONE atomic critical section — closing a TOCTOU
// window where two concurrent writes could each validate against a stale
// snapshot and both pass individually while the COMBINED persisted result has
// a gap neither observed alone (config.ValidateToolPolicyCoverage, CLAUDE.md
// hard constraint 6) — take a.configMu.Lock() themselves around the whole
// read-validate-persist sequence and call this method directly instead of
// safeUpdateConfigJSON, which would deadlock re-acquiring the same
// non-reentrant sync.Mutex. See createAgent, updateAgent, updateAgentTools
// (this file) and putToolPolicies (rest_tool_policies.go) for the pattern.
func (a *restAPI) updateConfigJSONLocked(mutate func(m map[string]any) error) error {
	raw, err := os.ReadFile(a.configPath())
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return fmt.Errorf("parse config: %w", unmarshalErr)
	}
	if mutateErr := mutate(m); mutateErr != nil {
		return mutateErr
	}
	// Ensure "version" is always stamped before writing back, mirroring
	// config.SaveConfig's own version-stamping for the struct-based save path.
	// Without this, a config.json that reached this raw-map read-modify-write
	// cycle without a "version" key (e.g. hand-edited, restored from an old
	// backup, or — as this exact bug once did — a stale test fixture) would
	// permanently fail every subsequent reload with "unsupported config
	// version: 0", since there is no more v0 migration fallback to bail it out.
	if v, ok := m["version"].(float64); !ok || int(v) < config.CurrentVersion {
		m["version"] = config.CurrentVersion
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}
	if writeErr := fileutil.WriteFileAtomic(a.configPath(), out, 0o600); writeErr != nil {
		return writeErr
	}
	// Register the content hash of what we just wrote so the config file
	// watcher knows this is an app-initiated write and does not trigger a
	// full service reload (channels disconnect/reconnect, cron lanes canceled).
	// We register `out` first so even a fast poller tick before the refresh sees
	// a known hash.
	if a.selfWriteReg != nil {
		a.selfWriteReg.register(sha256.Sum256(out))
	}
	// Refresh the in-memory config AND rewire sensitive-data scrubbing.
	// Propagate the error so callers fail the HTTP request rather than silently
	// serving stale in-memory state (prevents A1 regression on REST-initiated writes).
	if refreshErr := a.refreshConfigAndRewireServices(a.configPath()); refreshErr != nil {
		return fmt.Errorf("config written but in-memory refresh failed: %w", refreshErr)
	}
	// refreshConfigAndRewireServices loads the config, and config.LoadConfig may
	// normalize + re-save the file (config.go SaveConfig-on-load), producing
	// different bytes than `out`. Register the FINAL on-disk content hash too, so
	// the poller recognizes the post-refresh file as an app write and suppresses
	// the reload. Best-effort: a read failure just means the poller may reload once.
	if a.selfWriteReg != nil {
		if finalBytes, readErr := os.ReadFile(a.configPath()); readErr == nil {
			a.selfWriteReg.register(sha256.Sum256(finalBytes))
		} else {
			slog.Warn("rest: safeUpdateConfigJSON: could not read final config hash",
				"path", a.configPath(), "error", readErr)
		}
	}
	// FR-061 single chokepoint: every config mutation invalidates all cached
	// system-prompt preambles so the next agent turn rebuilds from the updated
	// config. Doing this inside safeUpdateConfigJSON removes the need for
	// individual call sites to remember the invalidation step.
	if a.agentLoop != nil {
		if reg := a.agentLoop.ContextBuilderRegistry(); reg != nil {
			reg.InvalidateAllContextBuilders()
		}
	}
	return nil
}

// ensureMap walks m through the given keys, creating intermediate map[string]any
// nodes as needed, and returns the deepest map. Panics only on a non-map value
// at a pre-existing key (a legitimate config.json corruption that should abort
// the request handler). Pure function — callers are already inside
// safeUpdateConfigJSON's mutex, so no locking here.
func ensureMap(m map[string]any, keys ...string) map[string]any {
	cur := m
	for _, k := range keys {
		existing, ok := cur[k]
		if !ok {
			next := map[string]any{}
			cur[k] = next
			cur = next
			continue
		}
		// Already exists — must be a map. Panic surfaces as 500 to the caller,
		// which is correct: a non-map node where a map is expected means
		// config.json on disk is structurally broken.
		nested, ok := existing.(map[string]any)
		if !ok {
			panic(fmt.Sprintf("ensureMap: expected map at key %q, got %T", k, existing))
		}
		cur = nested
	}
	return cur
}

// refreshConfigAndRewireServices loads a fresh config from disk, re-resolves the
// credential bundle, registers all resolved plaintexts with the sensitive-data
// replacer, and atomically swaps the in-memory config on the agent loop.
//
// This is the single authoritative refresh path — both safeUpdateConfigJSON and
// any future REST-initiated config write must call this method rather than
// calling a bare SwapConfig (which skips credential resolution and
// RegisterSensitiveValues, causing an A1-class scrubber regression).
//
// When a.credStore is nil (e.g. tests that don't wire a store), the function
// falls back to config.LoadConfig (no migration, no credential resolution) and
// skips RegisterSensitiveValues — there are no credentials to re-arm in that case.
//
// Credential-resolution escalation: mirrors bootCredentials/executeReload
// (gateway.go) — a resolution failure on a ref that buildEnabledRefMap
// considers actually enabled/in-use (an enabled channel, or an in-use voice /
// web-search / skill-marketplace credential) is escalated to an error instead
// of a bare Warn, whether the failure is a NotFoundError (ref genuinely
// missing) or something worse (wrong master key, corrupted store entry —
// the ref IS configured but unreadable). Unlike executeReload, this call site
// cannot "reject and roll back" a config write: by the time this method runs,
// updateConfigJSONLocked has ALREADY durably written the new config.json to
// disk (fileutil.WriteFileAtomic runs before this call). Rejection here
// therefore means: do NOT swap the broken config into the live in-memory
// pointer below (the gateway keeps serving the previous good config), and
// return the error so the caller's HTTP handler surfaces a 500 instead of
// silently reporting success — the operator then knows the save did not fully
// take effect and must fix the credential before it applies.
//
// Called while a.configMu is held.
// populateAgentsListFromStore is the ADR-054 D3/§5 "read through an
// in-memory cache" bridge for the config-reload path — see
// populateAgentsListFromEntityStoreStrict's doc comment (gateway.go) for the
// full rationale. The 2026-07-26 privilege-escalation chain documented there
// ran through the "main" sentinel, which no longer exists; the remaining half
// still stands on its own — a silently-empty roster makes the tool-policy
// coverage gate pass vacuously, over zero agents.
//
// SECURITY FIX (RELEASE BLOCKER, F3 follow-up): this used to call the LENIENT
// populateAgentsListFromEntityStore (log-and-continue on failure, silently
// leaving cfg.Agents.List whatever it already was — which, on a genuine
// entity-store failure, is often already empty this early in a fresh
// *config.Config's life). refreshConfigAndRewireServices is the single
// authoritative refresh path for EVERY REST-initiated config write — agent
// create/update/delete, channel configure, tool-policy write, mailbox grant,
// god-mode toggle, all of it — making this the highest-traffic call site for
// the bug F3 closed in gateway.go's boot/manual-reload/file-watcher paths.
// Now calls the STRICT variant directly (same package, same function — no
// export needed) and returns its error so refreshConfigAndRewireServices can
// reject the candidate config exactly like it already does for a credential-
// resolution failure below: never call SwapConfig, propagate the error so the
// caller's updateConfigJSONLocked fails the write and the HTTP handler
// surfaces a 500 instead of silently serving a config whose roster may have
// come back empty. restAPI has no reference to gateway.go's *services (and
// therefore no markReloadDegraded hook to call) — the synchronous REST-write
// path's equivalent signal is failing THIS request with a real error instead
// of a fake 200, which is the same "reject, don't swap" semantic gateway.go's
// async reload loop expresses via markReloadDegraded + a degraded /health.
func (a *restAPI) populateAgentsListFromStore(cfg *config.Config) error {
	return populateAgentsListFromEntityStoreStrict(cfg, a.homePath)
}

func (a *restAPI) refreshConfigAndRewireServices(configPath string) error {
	if a.credStore == nil {
		// No credential store wired — use the plain loader (no v0 migration, no
		// credential resolution). Safe because without a store there are no
		// secrets to re-arm in the replacer.
		newCfg, err := config.LoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("load config (no store): %w", err)
		}
		if rosterErr := a.populateAgentsListFromStore(newCfg); rosterErr != nil {
			slog.Error("refreshConfigAndRewireServices: rejecting in-memory refresh — "+
				"agent roster population failed (no credential store variant)", "error", rosterErr)
			return fmt.Errorf("agent roster population failed: %w", rosterErr)
		}
		a.agentLoop.SwapConfig(newCfg)
		// Hot-apply the log level: gateway.log_level is a hot-reload key (not
		// restart-gated), so a Settings save must take effect immediately
		// rather than waiting for a manual restart.
		logger.SetLevelFromString(newCfg.Gateway.LogLevel)
		return nil
	}
	newCfg, err := config.LoadConfigWithStore(configPath, a.credStore)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if rosterErr := a.populateAgentsListFromStore(newCfg); rosterErr != nil {
		slog.Error("refreshConfigAndRewireServices: rejecting in-memory refresh — "+
			"agent roster population failed", "error", rosterErr)
		return fmt.Errorf("agent roster population failed: %w", rosterErr)
	}
	// Build a ref→in-use map so a resolution failure on something actually
	// enabled/in-use (fatal — surfaced as a failed request) can be
	// distinguished from one on a disabled channel or unused feature
	// (Warn + continue), matching bootCredentials/executeReload.
	enabledRefs := buildEnabledRefMap(newCfg)
	bundle, bundleErrs := credentials.ResolveBundle(newCfg, a.credStore)
	for _, e := range bundleErrs {
		var notFound *credentials.NotFoundError
		if errors.As(e, &notFound) {
			if enabledRefs[notFound.Name] {
				refreshErr := fmt.Errorf(
					"enabled credential %q not found in store: %w", notFound.Name, e,
				)
				slog.Error("refreshConfigAndRewireServices: rejecting in-memory refresh — "+
					"enabled credential not found", "ref", notFound.Name, "error", e)
				return refreshErr
			}
			slog.Info("refreshConfigAndRewireServices: credential not found (not currently enabled/in use)",
				"ref", notFound.Name)
			continue
		}
		// Any error other than "not found" on an enabled/in-use ref is worse
		// than a simple missing ref (the credential exists but can't be
		// read) — escalate exactly like the NotFoundError-on-enabled case
		// above instead of a log-only Warn.
		if ref, ok := enabledRefFromBundleError(e, enabledRefs); ok {
			refreshErr := fmt.Errorf(
				"enabled credential %q failed to resolve (not simply missing — check "+
					"OMNIPUS_MASTER_KEY / credentials.json integrity): %w", ref, e,
			)
			slog.Error("refreshConfigAndRewireServices: rejecting in-memory refresh — "+
				"enabled credential failed to resolve", "ref", ref, "error", e)
			return refreshErr
		}
		// Non-fatal: a disabled channel / unused feature missing its cred is
		// acceptable here.
		slog.Warn("refreshConfigAndRewireServices: bundle resolution error", "error", e)
	}
	// Replace (not append) the entire sensitive-values set so rotated secrets
	// are evicted and the scrubber reflects the current config refs plus every
	// OAuth grant Omnipus owns in the encrypted store.
	values := make([]string, 0, len(bundle))
	for _, v := range bundle {
		if v != "" {
			values = append(values, v)
		}
	}
	values = append(values, providers.CollectOAuthSensitiveValues(a.credStore)...)
	newCfg.RegisterSensitiveValues(values)
	// Atomically swap the config pointer so all subsequent requests see the
	// new config with scrubbing fully re-armed.
	a.agentLoop.SwapConfig(newCfg)
	// Hot-apply the log level: gateway.log_level is a hot-reload key (not
	// restart-gated), so a Settings save must take effect immediately
	// rather than waiting for a manual restart.
	logger.SetLevelFromString(newCfg.Gateway.LogLevel)
	return nil
}

func (a *restAPI) updateConfig(w http.ResponseWriter, r *http.Request) {
	// Read the raw body once so we can decode it into two shapes: a RawMessage
	// map for the existing deep-merge persistence path, and a fully-typed
	// map[string]any for the blockedPaths walker which needs to
	// recurse into nested objects.
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var updates map[string]json.RawMessage
	if decodeErr := json.Unmarshal(rawBody, &updates); decodeErr != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Block credential fields and providers (credentials must use /providers endpoint)
	for k := range updates {
		kl := strings.ToLower(k)
		if kl == "providers" || strings.Contains(kl, "api_key") || strings.Contains(kl, "secret") ||
			strings.Contains(kl, "password") {
			jsonErr(w, http.StatusForbidden, fmt.Sprintf("credential field %q cannot be set via config endpoint", k))
			return
		}
	}

	// Block security-sensitive paths at ANY nesting depth. The walker handles
	// both nested bodies ({"gateway":{"users":[...]}}) and dot-path literal
	// keys ({"gateway.users":[...]}). Rejected requests persist NOTHING — we
	// return before safeUpdateConfigJSON is ever called, so benign sibling
	// keys in the same body are not written either.
	var typedBody map[string]any
	if decodeErr := json.Unmarshal(rawBody, &typedBody); decodeErr != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if path, blocked := matchBlockedPath(typedBody, blockedPaths); blocked {
		jsonErr(
			w,
			http.StatusForbidden,
			fmt.Sprintf("%s is a blocked path — use the dedicated endpoint", path),
		)
		return
	}

	// Use safeUpdateConfigJSON to hold configMu during the read-modify-write cycle.
	// Deep merge nested objects so partial updates don't wipe sibling keys
	// (e.g., updating gateway.port must not delete gateway.users).
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		for k, v := range updates {
			var parsed any
			if err := json.Unmarshal(v, &parsed); err != nil {
				return fmt.Errorf("invalid value for %q: %w", k, err)
			}
			// Deep merge maps; replace scalars/arrays.
			if existingMap, ok := m[k].(map[string]any); ok {
				if newMap, ok := parsed.(map[string]any); ok {
					for nk, nv := range newMap {
						existingMap[nk] = nv
					}
					continue // merged into existing map
				}
			}
			m[k] = parsed
		}
		return nil
	}); err != nil {
		slog.Error("rest: save config", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	a.getConfig(w)
}

// rotateGatewayToken generates a new random bearer token, persists it to config, and returns it.
// POST /api/v1/config/gateway/rotate-token
func (a *restAPI) rotateGatewayToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		slog.Error("rest: generate gateway token", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not generate token")
		return
	}
	// Token format: "omnipus_" + 64-char lowercase hex (32 random bytes).
	// This matches the BearerToken schema pattern '^omnipus_[a-f0-9]{64}$'.
	newToken := "omnipus_" + hex.EncodeToString(tokenBytes)
	// Persist to config.json BEFORE updating the live config.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, _ := m["gateway"].(map[string]any)
		if gw == nil {
			gw = map[string]any{}
			m["gateway"] = gw
		}
		gw["token"] = newToken
		return nil
	}); err != nil {
		slog.Error("rest: save config for token rotation", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// Persistence succeeded. Reload so the in-memory config picks up the new token.
	// If reload fails, the new token is on disk but not yet active — return 500 so the
	// caller knows the token is not yet in effect and can retry.
	//
	// triggerReloadAndWait (not the bare TriggerReload): the caller's very next
	// request authenticates with the token in this response body, so returning
	// before the reload lands hands out a token that 401s. A request that
	// arrives mid-reload used to be dropped entirely, making that permanent
	// until some unrelated reload happened to run.
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Error("config reload after token rotation failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("token saved but reload failed: %v", err))
		return
	}
	jsonOK(w, gen.RotateTokenResponse{Token: newToken})
}
