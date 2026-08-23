// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/voice"
)

// ---------------------------------------------------------------------------
// Re-auth consent primitive (FR-12.2)
//
// This is the NEW HTTP-layer consent check required before a sensitive settings
// change. It is DISTINCT from RequireNotBypass: RequireNotBypass returns 503
// when dev_mode_bypass is on (a dev-only guard that has nothing to do with the
// user re-typing their password). Re-auth re-verifies the single user's one
// password and mints a short-lived, single-use token the SPA replays in the
// X-Reauth-Token header on the immediately-following sensitive request.
// ---------------------------------------------------------------------------

// reAuthTokenTTL is how long a minted consent token stays valid. Short by
// design — it gates a single follow-up request, not a session.
const reAuthTokenTTL = 5 * time.Minute

// reAuthHeader is the header the SPA replays the consent token in.
const reAuthHeader = "X-Reauth-Token"

// reauthEntry is a minted consent token bound to a username with an expiry.
type reauthEntry struct {
	username  string
	expiresAt time.Time
}

// reauthStore is an in-memory, single-use store of re-auth consent tokens.
// Tokens are minted by HandleReAuth and consumed by consumeReAuthToken. It is
// process-local (single-user, single-binary) — no persistence is required or
// desirable for a 5-minute consent token.
type reauthStore struct {
	mu     sync.Mutex
	tokens map[string]reauthEntry
}

func newReAuthStore() *reauthStore {
	return &reauthStore{tokens: make(map[string]reauthEntry)}
}

// mint creates a new consent token for username and records it with a TTL.
func (s *reauthStore) mint(username string) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("rand read failed: %w", err)
	}
	token := "reauth_" + hex.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.tokens[token] = reauthEntry{username: username, expiresAt: time.Now().Add(reAuthTokenTTL)}
	return token, nil
}

// consume validates and removes a token (single-use). It returns true only when
// the token exists, is unexpired, and belongs to username. A constant-time
// comparison is unnecessary because tokens are 192-bit random map keys (no
// secret-dependent branch leaks the key), but expiry and ownership are checked.
func (s *reauthStore) consume(token, username string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	entry, ok := s.tokens[token]
	if !ok {
		return false
	}
	// Always delete on lookup — single-use, even on a username mismatch, so a
	// guessed/stale token cannot be retried.
	delete(s.tokens, token)
	if time.Now().After(entry.expiresAt) {
		return false
	}
	return entry.username == username
}

// pruneLocked drops expired entries. Caller must hold s.mu.
func (s *reauthStore) pruneLocked() {
	now := time.Now()
	for t, e := range s.tokens {
		if now.After(e.expiresAt) {
			delete(s.tokens, t)
		}
	}
}

// verifyUserPassword re-verifies the supplied plaintext password against the
// stored bcrypt hash for user. Reuses the exact comparison HandleChangePassword
// performs (rest_auth.go) so there is one password-verify code path. Returns
// ErrInvalidCredentials on mismatch and ErrUserNotFound when the user has no
// stored hash.
func verifyUserPassword(user *config.UserConfig, password string) error {
	if user == nil {
		return ErrUserNotFound
	}
	if strings.TrimSpace(user.PasswordHash) == "" {
		return ErrUserNotFound
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// HandleReAuth handles POST /api/v1/auth/reauth — the consent primitive
// (FR-12.2). It re-verifies the authenticated user's one password and, on
// success, mints a short-lived consent token returned in the body for the SPA
// to replay in the X-Reauth-Token header on the next sensitive request.
func (a *restAPI) HandleReAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var body gen.ReAuthRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ReAuthRequest", &body, validateEnabled) {
		return
	}
	if body.Password == "" {
		jsonErr(w, http.StatusBadRequest, "password is required")
		return
	}
	if err := verifyUserPassword(user, body.Password); err != nil {
		// Audit the failed consent attempt — repeated failures are an attack signal.
		a.auditReAuth(r, user.Username, false)
		// Both mismatch and missing-hash present as 401 to avoid leaking which
		// failed; the user experience ("wrong password") is identical.
		jsonErr(w, http.StatusUnauthorized, "password is incorrect")
		return
	}
	token, err := a.reauthStoreOrInit().mint(user.Username)
	if err != nil {
		slog.Error("auth: reauth token mint failed", "error", err, "username", user.Username)
		jsonErr(w, http.StatusInternalServerError, "re-authentication failed")
		return
	}
	a.auditReAuth(r, user.Username, true)
	jsonOK(w, gen.ReAuthResponse{
		Verified:  true,
		Token:     token,
		ExpiresIn: int(reAuthTokenTTL.Seconds()),
	})
}

// auditReAuth records a re-auth consent attempt. Best-effort: a nil logger or a
// write failure is logged, never fatal.
func (a *restAPI) auditReAuth(r *http.Request, username string, verified bool) {
	logger := a.agentLoop.AuditLogger()
	if logger == nil {
		return
	}
	if err := audit.EmitSecuritySettingChange(
		r.Context(), logger, "auth.reauth",
		map[string]any{"username": username},
		map[string]any{"username": username, "verified": verified},
	); err != nil {
		slog.Error("rest: audit emit reauth failed", "error", err)
	}
}

// requireReAuth enforces the consent primitive on a sensitive request. It reads
// the X-Reauth-Token header, validates+consumes it (single-use), and returns
// true when the caller may proceed. On failure it writes a 403 and returns
// false. This is the HTTP-layer counterpart to the tool-layer ws_approval used
// for skill writes — and explicitly NOT RequireNotBypass (a 503 dev guard).
func (a *restAPI) requireReAuth(w http.ResponseWriter, r *http.Request, username string) bool {
	token := strings.TrimSpace(r.Header.Get(reAuthHeader))
	if a.reauthStoreOrInit().consume(token, username) {
		return true
	}
	jsonErr(w, http.StatusForbidden,
		"this change requires re-typing your password — call POST /api/v1/auth/reauth first")
	return false
}

// ---------------------------------------------------------------------------
// Integrations provider-picker (FR-12.1)
//
// Surfaces the existing non-LLM integrations — web-search providers
// (pkg/tools/web SearchProvider) and voice-input transcribers (pkg/voice
// Transcriber) — for configuration in Settings → Integrations. API keys are
// stored encrypted; only credential refs land in config.json. Provider edits
// are sensitive and require a valid re-auth token (requireReAuth).
// ---------------------------------------------------------------------------

// integrationDef is the static catalog of a configurable integration provider:
// its id, kind, display name, whether it needs a key, and the credential-ref env
// var name used when a key is stored.
type integrationDef struct {
	id          string
	kind        string // "search" | "voice"
	displayName string
	requiresKey bool
	credRef     string // env-var ref name in the credential store ("" for keyless)
}

// integrationCatalogue is the fixed set of providers surfaced in the UI. It
// mirrors the providers wired in pkg/tools/web.go and pkg/voice. Keyless
// providers (DuckDuckGo, SearXNG, audio-model) have requiresKey=false and an
// empty credRef; SearXNG additionally needs a base_url and audio-model needs a
// voice.model_name (checked at activation time).
var integrationCatalogue = []integrationDef{
	// Search providers (pkg/tools/web.go SearchProvider implementations).
	{id: "brave", kind: "search", displayName: "Brave Search", requiresKey: true, credRef: "BRAVE_API_KEY"},
	{id: "tavily", kind: "search", displayName: "Tavily", requiresKey: true, credRef: "TAVILY_API_KEY"},
	{id: "perplexity", kind: "search", displayName: "Perplexity", requiresKey: true, credRef: "PERPLEXITY_API_KEY"},
	{id: "duckduckgo", kind: "search", displayName: "DuckDuckGo", requiresKey: false, credRef: ""},
	{id: "searxng", kind: "search", displayName: "SearXNG", requiresKey: false, credRef: ""},
	{id: "glm", kind: "search", displayName: "GLM Search", requiresKey: true, credRef: "GLM_API_KEY"},
	{id: "baidu", kind: "search", displayName: "Baidu Search", requiresKey: true, credRef: "BAIDU_API_KEY"},
	// Voice transcribers (pkg/voice Transcriber implementations).
	{
		id:          "elevenlabs",
		kind:        "voice",
		displayName: "ElevenLabs Scribe",
		requiresKey: true,
		credRef:     "ELEVENLABS_API_KEY",
	},
	{id: "groq", kind: "voice", displayName: "Groq Whisper", requiresKey: true, credRef: "GROQ_API_KEY"},
	{id: "audio-model", kind: "voice", displayName: "Audio Model (provider)", requiresKey: false, credRef: ""},
}

// integrationDefByID returns the catalog entry for id, or false.
func integrationDefByID(id string) (integrationDef, bool) {
	for _, d := range integrationCatalogue {
		if d.id == id {
			return d, true
		}
	}
	return integrationDef{}, false
}

// HandleIntegrationProviders dispatches GET (list) and PUT (configure) for
// /api/v1/integrations/providers[/{id}].
func (a *restAPI) HandleIntegrationProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleIntegrationProvidersList(w, r)
	case http.MethodPut:
		a.handleIntegrationProviderUpdate(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// buildIntegrationResponse computes the live provider catalog from the active
// config: configured (a key is present), active (the selected provider for its
// kind), and the active_search / active_voice selectors.
func (a *restAPI) buildIntegrationResponse(cfg *config.Config) gen.IntegrationProvidersResponse {
	activeSearch := a.activeSearchProviderID(cfg)
	activeVoice := a.activeVoiceProviderID(cfg)

	var resp gen.IntegrationProvidersResponse
	resp.Search = []gen.IntegrationProvider{}
	resp.Voice = []gen.IntegrationProvider{}

	for _, d := range integrationCatalogue {
		configured := a.integrationConfigured(cfg, d)
		active := (d.kind == "search" && d.id == activeSearch) ||
			(d.kind == "voice" && d.id == activeVoice)
		activeCopy := active
		entry := gen.IntegrationProvider{
			Id:          d.id,
			Kind:        gen.IntegrationProviderKind(d.kind),
			DisplayName: d.displayName,
			Configured:  configured,
			RequiresKey: d.requiresKey,
			Active:      &activeCopy,
		}
		if d.kind == "search" {
			resp.Search = append(resp.Search, entry)
		} else {
			resp.Voice = append(resp.Voice, entry)
		}
	}
	if activeSearch != "" {
		resp.ActiveSearch = &activeSearch
	}
	if activeVoice != "" {
		resp.ActiveVoice = &activeVoice
	}
	return resp
}

// integrationConfigured reports whether provider d is usable given cfg. Keyed
// providers are configured when their credential ref resolves in the store.
// Keyless providers have provider-specific prerequisites: DuckDuckGo is always
// available; SearXNG needs a base_url; audio-model needs a voice.model_name.
func (a *restAPI) integrationConfigured(cfg *config.Config, d integrationDef) bool {
	if d.requiresKey {
		ok, err := a.credentialRefResolves(d.credRef)
		if err != nil {
			// Store fault — treat as not-configured but log so the operator
			// can diagnose a locked store rather than silently swallowing it.
			slog.Warn("integrations: credential ref check failed",
				"provider", d.id, "ref", d.credRef, "error", err)
		}
		return ok
	}
	switch d.id {
	case "duckduckgo":
		return true
	case "searxng":
		return strings.TrimSpace(cfg.Tools.Web.SearXNG.BaseURL) != ""
	case "audio-model":
		return strings.TrimSpace(cfg.Voice.ModelName) != ""
	default:
		return true
	}
}

// integrationActivationReady reports whether the provider's prerequisites for
// activation are met. Keyed providers need a key (checked elsewhere via
// requiresKey); keyless providers with prerequisites (SearXNG base_url,
// audio-model model_name) are checked here. Returns true when activation may
// proceed, plus a human-readable reason when not.
func (a *restAPI) integrationActivationReady(cfg *config.Config, def integrationDef) (bool, string) {
	switch def.id {
	case "searxng":
		if strings.TrimSpace(cfg.Tools.Web.SearXNG.BaseURL) == "" {
			return false, "SearXNG requires a base URL (configure tools.web.searxng.base_url) before it can be activated"
		}
	case "audio-model":
		if strings.TrimSpace(cfg.Voice.ModelName) == "" {
			return false, "audio-model requires a voice model (configure voice.model_name under Providers) before it can be activated"
		}
	}
	return true, ""
}

// activeSearchProviderID derives the currently-selected search provider from the
// web tools config. The selection priority mirrors NewWebSearchTool
// (pkg/tools/web.go): Perplexity > Brave > SearXNG > Tavily > DuckDuckGo >
// Baidu > GLM. DuckDuckGo (keyless) is the fallback when no keyed provider is
// configured; SearXNG (keyless) is selected only when enabled with a base_url.
func (a *restAPI) activeSearchProviderID(cfg *config.Config) string {
	web := cfg.Tools.Web
	switch {
	case strings.TrimSpace(web.Perplexity.APIKeyRef) != "":
		return "perplexity"
	case strings.TrimSpace(web.Brave.APIKeyRef) != "":
		return "brave"
	case web.SearXNG.Enabled && strings.TrimSpace(web.SearXNG.BaseURL) != "":
		return "searxng"
	case strings.TrimSpace(web.Tavily.APIKeyRef) != "":
		return "tavily"
	case strings.TrimSpace(web.BaiduSearch.APIKeyRef) != "":
		return "baidu"
	case strings.TrimSpace(web.GLMSearch.APIKeyRef) != "":
		return "glm"
	default:
		return "duckduckgo"
	}
}

// activeVoiceProviderID derives the active transcriber, mirroring
// DetectTranscriber's priority (pkg/voice/transcriber.go): model_name
// (audio-capable) > ElevenLabs > Groq > Groq-provider-fallback. The
// audio-model entry is active when voice.model_name resolves to an
// audio-capable model; elevenlabs/groq are active when their respective
// credential refs are set.
func (a *restAPI) activeVoiceProviderID(cfg *config.Config) string {
	if mn := strings.TrimSpace(cfg.Voice.ModelName); mn != "" {
		if mc, err := cfg.FindModelConfigBySlug(mn); err == nil && voice.ModelSupportsAudioTranscription(mc.Model) {
			return "audio-model"
		}
	}
	if strings.TrimSpace(cfg.Voice.ElevenLabsAPIKeyRef) != "" {
		return "elevenlabs"
	}
	if strings.TrimSpace(cfg.Voice.GroqAPIKeyRef) != "" {
		return "groq"
	}
	return ""
}

func (a *restAPI) handleIntegrationProvidersList(w http.ResponseWriter, r *http.Request) {
	cfg := a.agentLoop.GetConfig()
	jsonOK(w, a.buildIntegrationResponse(cfg))
}

func (a *restAPI) handleIntegrationProviderUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Extract {id} from the path: /api/v1/integrations/providers/{id}.
	const prefix = "/api/v1/integrations/providers/"
	id := strings.TrimPrefix(r.URL.Path, prefix)
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		jsonErr(w, http.StatusBadRequest, "provider id is required in the path")
		return
	}
	def, known := integrationDefByID(id)
	if !known {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("unknown integration provider %q", id))
		return
	}

	var body gen.IntegrationProviderUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "IntegrationProviderUpdateRequest", &body, validateEnabled) {
		return
	}
	if string(body.Kind) != def.kind {
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("provider %q is a %q integration, not %q", id, def.kind, body.Kind))
		return
	}

	// Sensitive change → require the re-auth consent token (FR-12.2).
	if !a.requireReAuth(w, r, user.Username) {
		return
	}

	apiKey := ""
	if body.ApiKey != nil {
		apiKey = strings.TrimSpace(*body.ApiKey)
	}
	if def.requiresKey && apiKey == "" && body.Active != nil && *body.Active {
		// Selecting a key-requiring provider as active is only valid when a key
		// already exists (or is supplied in this request).
		hasKey, err := a.credentialRefResolves(def.credRef)
		if err != nil {
			jsonErr(w, http.StatusServiceUnavailable, "credential store locked")
			return
		}
		if !hasKey {
			jsonErr(w, http.StatusBadRequest,
				fmt.Sprintf("%s requires an API key before it can be activated", def.displayName))
			return
		}
	}
	if !def.requiresKey && body.Active != nil && *body.Active {
		// Keyless providers with prerequisites (SearXNG base_url, audio-model
		// model_name) must have those set before activation.
		if ok, reason := a.integrationActivationReady(a.agentLoop.GetConfig(), def); !ok {
			jsonErr(w, http.StatusBadRequest, reason)
			return
		}
	}

	// Store the key (if supplied) in the encrypted credential store BEFORE
	// writing the ref to config.json (SEC-23: no plaintext fallback).
	if apiKey != "" {
		if def.credRef == "" {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("%s does not accept an API key", def.displayName))
			return
		}
		if _, err := a.storeCredential(def.credRef, apiKey); err != nil {
			slog.Error("integrations: credential store failed", "provider", id, "error", err)
			jsonErr(w, http.StatusServiceUnavailable,
				"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets")
			return
		}
	}

	makeActive := body.Active != nil && *body.Active

	// safeUpdateConfigJSON writes config.json atomically AND refreshes the live
	// in-memory config + rewires services, so no separate reload is needed.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		return applyIntegrationConfig(m, def, apiKey != "", makeActive)
	}); err != nil {
		slog.Error("integrations: config update failed", "provider", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to save integration config")
		return
	}

	// Audit the change (resource names the integration; values omit the secret).
	if logger := a.agentLoop.AuditLogger(); logger != nil {
		if err := audit.EmitSecuritySettingChange(
			r.Context(), logger, "integrations.provider",
			map[string]any{"provider": id},
			map[string]any{"provider": id, "kind": def.kind, "key_set": apiKey != "", "active": makeActive},
		); err != nil {
			slog.Error("rest: audit emit integration change failed", "error", err)
		}
	}

	jsonOK(w, a.buildIntegrationResponse(a.agentLoop.GetConfig()))
}

// applyIntegrationConfig mutates the raw config map to (a) set the credential
// ref on the provider when a key was stored and (b) select the provider as
// active for its kind. For search, "active" means: set this provider's
// api_key_ref and clear the OTHER keyed search providers' refs so exactly one is
// active (DuckDuckGo/SearXNG, keyless, are the implicit fallbacks when none is
// keyed; SearXNG toggles its enabled flag). For voice, "active" sets the
// provider's api_key_ref (elevenlabs/groq) and clears the other keyed
// transcriber's ref; audio-model activation clears both keyed refs so
// DetectTranscriber falls through to voice.model_name.
func applyIntegrationConfig(m map[string]any, def integrationDef, keySet, makeActive bool) error {
	switch def.kind {
	case "search":
		return applySearchIntegration(m, def, keySet, makeActive)
	case "voice":
		return applyVoiceIntegration(m, def, keySet, makeActive)
	default:
		return fmt.Errorf("unknown integration kind %q", def.kind)
	}
}

// searchRefKeyByID maps a search provider id to its config sub-object key and
// the ref field within it.
var searchRefKeyByID = map[string]struct{ section string }{
	"brave":      {"brave"},
	"tavily":     {"tavily"},
	"perplexity": {"perplexity"},
	"glm":        {"glm_search"},
	"baidu":      {"baidu_search"},
}

func applySearchIntegration(m map[string]any, def integrationDef, keySet, makeActive bool) error {
	tools := mapChild(m, "tools")
	web := mapChild(tools, "web")

	// Set this provider's ref when a key was stored.
	if keySet && def.credRef != "" {
		sec, ok := searchRefKeyByID[def.id]
		if ok {
			section := mapChild(web, sec.section)
			section["api_key_ref"] = def.credRef
		}
	}

	// Activation: ensure exactly one keyed search provider is active by clearing
	// the others' refs. DuckDuckGo/SearXNG activation clears all keyed refs
	// (falls back to the keyless provider); SearXNG additionally sets enabled.
	if makeActive {
		for pid, sec := range searchRefKeyByID {
			section := mapChild(web, sec.section)
			if pid == def.id {
				if def.credRef != "" {
					section["api_key_ref"] = def.credRef
				}
			} else {
				delete(section, "api_key_ref")
			}
		}
		// Toggle the keyless providers' enabled flags so the active one is
		// enabled and the other is not (DuckDuckGo needs no flag — it is the
		// implicit fallback when no keyed provider is set).
		searxng := mapChild(web, "searxng")
		if def.id == "searxng" {
			searxng["enabled"] = true
		} else {
			searxng["enabled"] = false
		}
	}
	return nil
}

func applyVoiceIntegration(m map[string]any, def integrationDef, keySet, makeActive bool) error {
	voiceCfg := mapChild(m, "voice")
	switch def.id {
	case "elevenlabs":
		if keySet && def.credRef != "" {
			voiceCfg["elevenlabs_api_key_ref"] = def.credRef
		}
		if makeActive {
			// Activation with no stored key is rejected upstream; here a key is
			// guaranteed present, so point the ref at it. Clear the Groq ref so
			// exactly one keyed transcriber is active (DetectTranscriber checks
			// elevenlabs before groq, but keeping a single ref avoids ambiguity
			// in the Integrations picker).
			if def.credRef != "" {
				voiceCfg["elevenlabs_api_key_ref"] = def.credRef
			}
			delete(voiceCfg, "groq_api_key_ref")
		}
	case "groq":
		if keySet && def.credRef != "" {
			voiceCfg["groq_api_key_ref"] = def.credRef
		}
		if makeActive {
			if def.credRef != "" {
				voiceCfg["groq_api_key_ref"] = def.credRef
			}
			// Clear the ElevenLabs ref so Groq is the active keyed transcriber.
			// voice.model_name (audio-model) is left untouched — it is
			// configured under Providers and takes precedence in
			// DetectTranscriber when set to an audio-capable model.
			delete(voiceCfg, "elevenlabs_api_key_ref")
		}
	case "audio-model":
		// Keyless: activation clears the keyed transcriber refs so
		// DetectTranscriber falls through to voice.model_name (which must be
		// set — checked by integrationActivationReady). No key is stored.
		if makeActive {
			delete(voiceCfg, "elevenlabs_api_key_ref")
			delete(voiceCfg, "groq_api_key_ref")
		}
	}
	return nil
}

// mapChild returns m[key] as a map[string]any, creating it when absent or when
// the existing value is not an object. Used to safely descend into the raw
// config JSON during read-modify-write.
func mapChild(m map[string]any, key string) map[string]any {
	if existing, ok := m[key].(map[string]any); ok {
		return existing
	}
	child := map[string]any{}
	m[key] = child
	return child
}

// ---------------------------------------------------------------------------
// Composer mic — voice transcription (FR-12.1)
// ---------------------------------------------------------------------------

// maxTranscribeBytes caps the uploaded audio size (25 MiB — comfortably above a
// minute of compressed speech, well under provider limits).
const maxTranscribeBytes = 25 << 20

// HandleTranscribe handles POST /api/v1/voice/transcribe. It accepts a
// multipart audio file ("audio"), writes it to a temp file, and runs it through
// the active Transcriber, returning the text. 503 when no transcriber is
// configured.
func (a *restAPI) HandleTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	transcriber := a.resolveTranscriber()
	if transcriber == nil {
		jsonErr(w, http.StatusServiceUnavailable,
			"no voice transcriber configured — configure one in Settings → Integrations")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTranscribeBytes+(1<<20))
	if err := r.ParseMultipartForm(maxTranscribeBytes); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid or oversized multipart upload")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("audio")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "missing audio file field \"audio\"")
		return
	}
	defer file.Close()

	tmpPath, cleanup, err := a.spoolUpload(file, header.Filename)
	if err != nil {
		slog.Error("transcribe: spool upload failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read uploaded audio")
		return
	}
	defer cleanup()

	res, err := transcriber.Transcribe(r.Context(), tmpPath)
	if err != nil {
		slog.Error("transcribe: transcription failed", "provider", transcriber.Name(), "error", err)
		jsonErr(w, http.StatusBadGateway, "transcription failed: "+err.Error())
		return
	}

	resp := gen.TranscribeResponse{Text: res.Text}
	if res.Language != "" {
		lang := res.Language
		resp.Language = &lang
	}
	if res.Duration > 0 {
		dur := float32(res.Duration)
		resp.Duration = &dur
	}
	jsonOK(w, resp)
}

// resolveTranscriber returns the agent loop's active transcriber, or, when the
// loop has none cached, attempts a fresh detection from the live config + bundle.
func (a *restAPI) resolveTranscriber() voice.Transcriber {
	if t := a.agentLoop.GetTranscriber(); t != nil {
		return t
	}
	cfg := a.agentLoop.GetConfig()
	if cfg == nil {
		return nil
	}
	// ResolveBundle dereferences the store (IsLocked) — a nil credStore (test
	// setups, or a pre-unlock boot) would panic. When there is no store, fall
	// back to an empty bundle: model-based transcription may still be detected
	// from cfg, and ElevenLabs/Groq simply resolve to no key.
	if a.credStore == nil {
		return voice.DetectTranscriber(cfg, credentials.SecretBundle{})
	}
	bundle, err := credentials.ResolveBundle(cfg, a.credStore)
	if err != nil {
		// A locked or failing credential store yields an empty bundle here, which
		// silently disables voice transcription. Surface it at Error so operators
		// can diagnose (mirrors the credential-ref check log path above).
		slog.Error("integrations: resolve credential bundle for transcriber failed",
			"error", err)
	}
	return voice.DetectTranscriber(cfg, bundle)
}

// spoolUpload writes an uploaded file to a temp file under the OS temp dir,
// preserving the original extension so the transcriber can content-sniff. It
// returns the temp path and a cleanup func that removes it.
func (a *restAPI) spoolUpload(src io.Reader, filename string) (string, func(), error) {
	ext := filepath.Ext(filename)
	if len(ext) > 8 { // guard against absurd extensions
		ext = ""
	}
	tmp, err := os.CreateTemp("", "omnipus-voice-*"+ext)
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}
	if _, err := io.Copy(tmp, io.LimitReader(src, maxTranscribeBytes)); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", func() {}, err
	}
	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
}
