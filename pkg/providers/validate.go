// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// URLChecker is the SSRF guard interface. The gateway passes its *security.SSRFChecker;
// the CLI (and the gateway when SSRF is globally disabled) passes a NoopChecker — an
// explicit, greppable "no SSRF guard" decision rather than a nil that could equally
// mean "I forgot". *security.SSRFChecker already satisfies this interface.
//
// Callers MUST pass a non-nil URLChecker. The package still tolerates nil defensively,
// but NoopChecker is the supported way to express "no guard".
type URLChecker interface {
	CheckURL(ctx context.Context, rawURL string) error
	// SafeClient returns an *http.Client the caller may mutate (e.g. set Timeout);
	// implementations MUST return a fresh client per call so that mutation is race-free.
	SafeClient() *http.Client
}

// NoopChecker is a URLChecker that performs no SSRF check and returns a plain client.
// Use it on the CLI/operator-run path (no localhost-pivot threat) instead of a nil
// interface — this makes "SSRF disabled" an explicit, type-safe choice and removes the
// typed-nil footgun (a non-nil interface wrapping a nil *security.SSRFChecker pointer).
type NoopChecker struct{}

// CheckURL always permits the URL (no SSRF guard).
func (NoopChecker) CheckURL(context.Context, string) error { return nil }

// SafeClient returns a fresh default client the caller may mutate.
func (NoopChecker) SafeClient() *http.Client { return &http.Client{} }

// Outcome is the classified result of a key-validation probe.
// String values are the wire enum — must match contracts/components/schemas/ProviderValidation.yaml.
type Outcome string

const (
	// OutcomeValid means the key authenticated successfully.
	OutcomeValid Outcome = "valid"
	// OutcomeInvalidKey means the provider confirmed the key is wrong/revoked. This is the
	// only outcome that blocks; all others proceed with a warning.
	OutcomeInvalidKey Outcome = "invalid_key"
	// OutcomeNoCredit means the key works but the account has no credit/quota.
	OutcomeNoCredit Outcome = "no_credit"
	// OutcomeUnreachable means the provider could not be reached (transport error, 5xx,
	// pre-auth 429, 404) — transient; proceed with a warning.
	OutcomeUnreachable Outcome = "unreachable"
	// OutcomeRestricted means the key works but access is regionally or model-level
	// blocked (403 without a credential marker).
	OutcomeRestricted Outcome = "restricted"
)

// ValidationResult is the classified result of ValidateKey.
//
// SEC-16: this struct deliberately carries NO `json:` tags and MUST NEVER be marshaled
// directly onto the wire — RawDetail holds the raw upstream body for debug logging only.
// Callers copy Outcome/Message into the generated wire types; RawDetail goes solely to
// slog.Debug. Do not add json tags here and do not serialize ValidationResult.
type ValidationResult struct {
	// Outcome is one of the five wire enum values.
	Outcome Outcome
	// Message is the curated FR-7 plain-English message for the user (SEC-16: never
	// contains the raw body or the API key).
	Message string
	// RawDetail is the raw upstream detail for the server debug log ONLY. Never send
	// this to the user or on the wire.
	RawDetail string
	// ProbedModel is the model the completion was actually fired against —
	// the last candidate tried, which is the one this Outcome describes.
	// Empty when no probe ran at all (empty key, or no candidate anywhere).
	// ADR-068 FR-036 surfaces it as ProbeProviderResponse.probed_model so the
	// operator can tie a green probe to the exact model they picked.
	ProbedModel string
}

// Blocks reports whether this outcome must block the flow. Derived solely from Outcome
// (only InvalidKey blocks) so the block/proceed decision has a single source of truth
// and cannot drift from Outcome.
func (r ValidationResult) Blocks() bool { return r.Outcome == OutcomeInvalidKey }

// ValidateInput carries the inputs for ValidateKey.
type ValidateInput struct {
	// ProviderID is the canonical lowercase provider id (openrouter, openai, gemini, …).
	ProviderID string
	// ProviderName is the display name for the FR-7 message (e.g. "OpenRouter").
	ProviderName string
	// BaseURL is the resolved provider api_base (SSRF-checked by the caller before
	// passing here when checker != nil).
	BaseURL string
	// APIKey is the key to validate.
	APIKey string
	// Catalog is the already-fetched model list. If empty, ValidateKey will attempt
	// to fetch it via FetchModels before picking a probe model.
	Catalog []string
	// ProbeModels, when non-empty, IS the candidate list — in order, capped
	// at maxProbeAttempts — and neither the catalog document nor a live
	// /models call is consulted to build one. The caller has already decided
	// which models this probe is about (ADR-068 FR-036: the operator's
	// verbatim pick, or the provider's Recommended-for-chat shortlist), and a
	// second opinion here would answer a question nobody asked.
	ProbeModels []string
}

// ── Credential-marker set (R-A) ──────────────────────────────────────────────
// Narrow, explicit set that unambiguously means "the credential is wrong/revoked".
// Deliberately EXCLUDES "permission_denied" and "unauthenticated" — those mean
// region/permission blocks, not a wrong key (they live in the 403→Restricted branch).
var credentialMarkers = []errorPattern{
	substr("api_key_invalid"),
	substr("api key not valid"),
	substr("invalid api key"),
	substr("invalid_api_key"),
	substr("incorrect api key"),
	substr("incorrect_api_key"),
	substr("no auth credentials"),
	substr("invalid x-api-key"),
	substr("revoked"),
	substr("authentication_error"),
	substr("authentication fails"),
}

// ── Credit-marker set (R-A / m5) ─────────────────────────────────────────────
// Reuses error_classifier.go's billingPatterns via matchesAny, extended below.
var creditMarkersExtra = []errorPattern{
	substr("insufficient_quota"),
	substr("insufficient quota"),
	substr("insufficient credits"),
	substr("insufficient balance"),
	substr("credit balance is too low"),
	substr("exceeded your current quota"),
	substr("out of credits"),
}

// isCreditMarker returns true when msg contains a credit/billing pattern.
// Reuses billingPatterns from error_classifier.go (m5 — no second billing list).
func isCreditMarker(msg string) bool {
	return matchesAny(msg, billingPatterns) || matchesAny(msg, creditMarkersExtra)
}

// isCredentialMarker returns true when msg contains a narrow credential-invalid pattern.
func isCredentialMarker(msg string) bool {
	return matchesAny(msg, credentialMarkers)
}

// ── Body parsing ──────────────────────────────────────────────────────────────

// providerErrorBody is used to parse the OpenAI-compat error envelope.
// Providers vary; we parse defensively and never panic on malformed JSON.
type providerErrorBody struct { // not-wire-format: decodes upstream provider error body, never emitted to SPA
	Error struct {
		Code    any    `json:"code"` // may be string or int
		Type    string `json:"type"`
		Status  any    `json:"status"` // may be string ("PERMISSION_DENIED") or int
		Message string `json:"message"`
	} `json:"error"`
}

// parseErrorBody attempts to extract (message, embeddedErrPresent, embeddedCode) from
// a JSON body. It is defensive — it never panics, and it returns ("", false, 0) on any
// parse failure or absent error key.
//
// embeddedCode is the numeric HTTP status embedded in the body (e.g. some providers
// echo the HTTP status as error.code = 401). It is 0 when absent or non-numeric.
func parseErrorBody(body []byte) (msg string, present bool, embeddedCode int) {
	if len(body) == 0 {
		return "", false, 0
	}
	var e providerErrorBody
	if err := json.Unmarshal(body, &e); err != nil {
		// Non-JSON body or truncated — treat as absent error object.
		return "", false, 0
	}
	// Determine whether an "error" key is genuinely present.
	// We check the union of non-zero fields.
	if e.Error.Code == nil && e.Error.Type == "" && e.Error.Status == nil && e.Error.Message == "" {
		return "", false, 0
	}
	// Build the consolidated message string from all available fields.
	parts := make([]string, 0, 4)
	if e.Error.Message != "" {
		parts = append(parts, e.Error.Message)
	}
	if e.Error.Type != "" {
		parts = append(parts, e.Error.Type)
	}
	// code and status: stringify whatever type they are.
	if e.Error.Code != nil {
		parts = append(parts, fmt.Sprintf("%v", e.Error.Code))
	}
	if e.Error.Status != nil {
		parts = append(parts, fmt.Sprintf("%v", e.Error.Status))
	}
	msg = strings.Join(parts, " ")

	// Attempt to extract a numeric HTTP status from error.code or error.status.
	// Some providers (e.g. older OpenAI-compat) echo the HTTP status as error.code.
	code := 0
	if e.Error.Code != nil {
		switch v := e.Error.Code.(type) {
		case float64:
			code = int(v)
		case string:
			code = parseDigits(v)
		}
	}
	if code == 0 && e.Error.Status != nil {
		switch v := e.Error.Status.(type) {
		case float64:
			c := int(v)
			if c >= 100 && c < 600 {
				code = c
			}
		}
	}
	return msg, true, code
}

// ── R-A Classifier ────────────────────────────────────────────────────────────

// classify implements the R-A precedence algorithm exactly as specified.
//
// Precedence (first match wins):
//  1. transportErr                              → Unreachable
//  2. parse body → (msg, embeddedErrPresent, embeddedCode)
//  3. status == 200:
//     if !embeddedErrPresent                   → Valid
//     else: status = embeddedCode (or 0); continue
//  4. m := lower(msg)
//     creditMarker(m)                          → NoCredit
//     credentialMarker(m)                      → InvalidKey
//  5. switch status:
//     401                                      → InvalidKey
//     402                                      → NoCredit
//     403                                      → Restricted
//     400                                      → Valid  (auth reached; request-level error)
//     404/429/5xx/0/other                      → Unreachable
//     2xx (non-200)                            → Valid
func classify(transportErr error, status int, body []byte) Outcome {
	// Step 1: transport error.
	if transportErr != nil {
		return OutcomeUnreachable
	}

	// Step 2: parse body.
	msg, embeddedErrPresent, embeddedCode := parseErrorBody(body)

	// Step 3: handle 200 first.
	if status == 200 {
		if !embeddedErrPresent {
			return OutcomeValid
		}
		// 200 with an embedded error — reclassify using the embedded code.
		// The message is already populated; replace status.
		if embeddedCode >= 100 && embeddedCode < 600 {
			status = embeddedCode
		} else {
			status = 0 // unknown — will fall to Unreachable at step 5
		}
	}

	// Step 4: message-driven classification (marker sets take priority over status).
	m := strings.ToLower(msg)
	if m != "" {
		if isCreditMarker(m) {
			return OutcomeNoCredit
		}
		if isCredentialMarker(m) {
			return OutcomeInvalidKey
		}
	}

	// Step 5: status switch.
	switch {
	case status == 401:
		return OutcomeInvalidKey
	case status == 402:
		return OutcomeNoCredit
	case status == 403:
		return OutcomeRestricted
	case status == 400:
		return OutcomeValid // auth reached; request-level error (bad model etc.) is not a key problem
	case status == 404:
		return OutcomeUnreachable // wrong base URL or missing endpoint
	case status == 429:
		return OutcomeUnreachable // pre-auth rate-limit or un-marked rate-limit
	case status >= 500:
		return OutcomeUnreachable
	case status >= 201 && status < 300:
		return OutcomeValid // non-200 2xx
	default:
		return OutcomeUnreachable // 0 (200+unrecognized-error) or anything unmapped
	}
}

// ── Probe model selection (R-E / FR-009) ─────────────────────────────────────

// nonChatSubstrings are substrings whose presence in a model name indicates it is
// NOT a chat-capable model. Matching is case-insensitive.
var nonChatSubstrings = []string{
	"embed", "whisper", "tts", "dall-e", "image", "rerank", "moderation",
}

// maxProbeAttempts bounds the fall-through when a probe model turns out not
// to exist upstream (FR-022, F-25). Three is enough to step past a couple of
// entitlement gaps in a provider's own catalog and small enough that a badly
// stale document cannot turn one key check into a burst of requests.
const maxProbeAttempts = 3

// catalogProbeModels returns the probe candidates for a CATALOG provider, in
// document order: the first `status: active`, tool-calling, text-modality
// models of that provider, capped at maxProbeAttempts (FR-022, A-20).
//
// The old per-provider slug table (`probeModelDefaults`) is gone. It was a
// hand-typed list of ten vendor model ids that went stale silently — a
// retired slug there produced a 404 that classified as *Unreachable*, i.e. a
// perfectly good key reported as "provider unreachable". The catalog knows
// which models are live, so the probe now asks it.
//
// An id the catalog does not carry returns nil: a custom or local endpoint
// has no catalog models, and its caller falls back to the live list.
func catalogProbeModels(providerID string) []string {
	row, ok := CatalogProvider(providerID)
	if !ok {
		return nil
	}
	var out []string
	for _, m := range row.Models {
		if m.Status != catalog.StatusActive || !m.ToolCall {
			continue
		}
		if !hasTextModality(m.InputModalities) {
			continue
		}
		out = append(out, m.ID)
		if len(out) == maxProbeAttempts {
			break
		}
	}
	return out
}

// hasTextModality reports whether a model accepts text input. Every valid
// catalog model does (FR-002), so this is a belt-and-braces read of the
// document rather than a filter that routinely fires.
func hasTextModality(mods []catalog.Modality) bool {
	for _, m := range mods {
		if m == catalog.ModalityText {
			return true
		}
	}
	return false
}

// chatProbeCandidates filters a LIVE model list down to plausibly chat-capable
// entries and caps it at maxProbeAttempts. It is the fallback path for rows
// with no catalog models: operator-typed custom endpoints and local runtimes,
// whose model list only exists upstream.
func chatProbeCandidates(models []string) []string {
	var out []string
	for _, m := range models {
		ml := strings.ToLower(m)
		skip := false
		for _, sub := range nonChatSubstrings {
			if strings.Contains(ml, sub) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		out = append(out, m)
		if len(out) == maxProbeAttempts {
			break
		}
	}
	return out
}

// modelNotFoundMarkers are the upstream phrasings that mean "that model id
// does not exist here" as opposed to "your key is bad" or "we are down".
// Only these justify trying the next candidate (F-25).
var modelNotFoundMarkers = []string{
	"model_not_found",
	"model not found",
	"does not exist",
	"unknown model",
	"invalid model",
	"no such model",
}

// isModelNotFound reports whether an upstream detail names a missing model.
func isModelNotFound(rawDetail string) bool {
	d := strings.ToLower(rawDetail)
	for _, m := range modelNotFoundMarkers {
		if strings.Contains(d, m) {
			return true
		}
	}
	return false
}

// ── model listing (moved from pkg/gateway/rest.go fetchUpstreamModels) ───────

// modelListTimeout bounds every live listing call in this file.
const modelListTimeout = 10 * time.Second

// anthropicVersionHeader is the dated Anthropic API version every Anthropic
// REST call must carry (including GET /v1/models).
const anthropicVersionHeader = "2023-06-01"

// UpstreamStatusError is returned when a provider's listing endpoint was
// reached but answered a non-2xx status. It exists so a caller can report
// the STATUS rather than a generic transport failure — ADR-067 FR-021 (X-12)
// requires the entitlement endpoint to answer
// `could not fetch upstream model list: status <n>`, which is only possible
// if the status survives the return.
type UpstreamStatusError struct {
	Status int
}

func (e *UpstreamStatusError) Error() string {
	return fmt.Sprintf("upstream models: status %d", e.Status)
}

// ListModels lists the model ids a provider's live listing endpoint reports,
// dispatching on the catalog PROTOCOL (ADR-067 FR-021):
//
//   - openai-compatible, google (and an empty protocol) → GET {base}/models
//     with a Bearer key;
//   - anthropic → GET {base}/v1/models with `x-api-key` + `anthropic-version`;
//   - ollama → GET {base without /v1}/api/tags, unauthenticated.
//
// Any other protocol (notably `cli`, which has no HTTP listing at all) is an
// error: callers decide the status code — the gateway's entitlement endpoint
// refuses those rows with 409 BEFORE reaching here.
//
// This is the only listing entry point with a protocol argument. Its two
// callers are the entitlement endpoint and the providers GET's local-endpoint
// listing; everything else (onboarding, ValidateKey) still calls FetchModels
// directly because it is already talking to an OpenAI-compatible base.
func ListModels(
	ctx context.Context,
	protocol catalog.Protocol,
	baseURL, apiKey string,
	checker URLChecker,
) ([]string, error) {
	switch protocol {
	case catalog.ProtocolOllama:
		return fetchOllamaTags(ctx, baseURL, checker)
	case catalog.ProtocolAnthropic:
		return fetchAnthropicModels(ctx, baseURL, apiKey, checker)
	case catalog.ProtocolOpenAICompatible, catalog.ProtocolGoogle, "":
		return FetchModels(ctx, baseURL, apiKey, checker)
	default:
		return nil, fmt.Errorf("model listing is not supported for protocol %q", protocol)
	}
}

// FetchModels fetches the list of available models from an OpenAI-compatible
// provider's /models endpoint. Returns model IDs sorted alphabetically, or nil on error.
// Behavior is identical to the former gateway.fetchUpstreamModels.
//
// SEC-24: when checker is non-nil the request is made through the SSRF-safe HTTP
// client. A nil checker uses a plain client (CLI path — operator-run, accepted).
func FetchModels(ctx context.Context, baseURL, apiKey string, checker URLChecker) ([]string, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + apiKey,
		"X-Api-Key":     apiKey,
	}
	// Legacy host sniff, kept for the callers that reach an Anthropic base
	// through this OpenAI-compatible entry point (onboarding, ValidateKey).
	// The protocol-dispatched path never relies on it — see
	// fetchAnthropicModels.
	if strings.Contains(baseURL, "anthropic") {
		headers["Anthropic-Version"] = anthropicVersionHeader
	}
	ids, err := fetchModelIDs(ctx, strings.TrimSuffix(baseURL, "/")+"/models", headers, checker)
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// fetchAnthropicModels lists models over the Anthropic protocol: the dated
// version header is mandatory and the key travels in `x-api-key`, never as a
// Bearer token. A base that already ends in /v1 is not doubled.
func fetchAnthropicModels(ctx context.Context, baseURL, apiKey string, checker URLChecker) ([]string, error) {
	root := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(baseURL), "/"), "/v1")
	ids, err := fetchModelIDs(ctx, root+"/v1/models", map[string]string{
		"X-Api-Key":         apiKey,
		"Anthropic-Version": anthropicVersionHeader,
	}, checker)
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// fetchModelIDs performs one listing GET and decodes the `{"data":[{"id"}]}`
// envelope both the OpenAI-compatible and the Anthropic listing return. A
// non-2xx comes back as *UpstreamStatusError so the caller can report the
// status verbatim.
func fetchModelIDs(
	ctx context.Context,
	url string,
	headers map[string]string,
	checker URLChecker,
) ([]string, error) {
	body, err := getUpstreamJSON(ctx, url, headers, checker)
	if err != nil {
		return nil, err
	}
	var result struct { // not-wire-format: decodes upstream provider /models API response, never emitted to SPA
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			models = append(models, id)
		}
	}
	return models, nil
}

// fetchOllamaTags lists the models an ollama endpoint has actually pulled,
// from its native /api/tags. The catalog carries ollama's OpenAI-compatible
// base (…:11434/v1); /api/tags hangs off the root, so the /v1 suffix is
// trimmed. The endpoint is unauthenticated — no key is sent.
//
// Document order is preserved (unlike the /models paths, which sort): the
// order ollama reports is the order the operator's own machine has them.
func fetchOllamaTags(ctx context.Context, baseURL string, checker URLChecker) ([]string, error) {
	root := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(baseURL), "/"), "/v1")
	body, err := getUpstreamJSON(ctx, root+"/api/tags", nil, checker)
	if err != nil {
		return nil, err
	}
	var decoded struct { // not-wire-format: decodes ollama's /api/tags response, never emitted to the SPA
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(decoded.Models))
	for _, m := range decoded.Models {
		if name := strings.TrimSpace(m.Name); name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}

// getUpstreamJSON issues one bounded GET and returns at most 2 MB of JSON
// body. SEC-24: a non-nil checker supplies the SSRF-safe client; a nil
// checker uses a plain one (CLI path — operator-run, accepted).
func getUpstreamJSON(
	ctx context.Context,
	url string,
	headers map[string]string,
	checker URLChecker,
) ([]byte, error) {
	client := &http.Client{Timeout: modelListTimeout}
	if checker != nil {
		client = checker.SafeClient()
		client.Timeout = modelListTimeout
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &UpstreamStatusError{Status: resp.StatusCode}
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "application/json") {
		return nil, fmt.Errorf("upstream models: unexpected Content-Type %q", ct)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MB limit
}

// ── BuildMessage (FR-7 catalog) ───────────────────────────────────────────────

// isLoopbackBaseURL reports whether baseURL's host is a loopback address —
// "localhost", 127.0.0.0/8, or "::1" — i.e. a provider that must be running
// on THIS machine (Ollama, LM Studio, LiteLLM, vLLM, …) rather than a remote
// hosted API. An unparsable baseURL is treated as non-loopback (falls back to
// the ordinary network-connectivity advice).
//
// This intentionally duplicates rather than reuses two existing loopback
// checks: pkg/sysagent/tools/mcp.go's mcpURLSchemeValid (unexported, and in a
// higher-level package — pkg/sysagent/tools imports pkg/providers, not the
// reverse, so importing it here would invert the dependency) and
// pkg/providers/catalog/locality.go's unexported isLocalHost (also
// unexported, and answers a broader question — it also treats RFC1918,
// link-local, and ULA hosts as "local", where a LAN-hosted custom endpoint
// should still get the ordinary "check your connection" advice). Both were
// checked before writing this; neither fit without either an import
// inversion or a semantics change out of scope for this fix.
func isLoopbackBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

// BuildMessage returns the curated FR-7 plain-English message for the given outcome,
// provider name, and the base URL that was probed. Valid returns "". The message
// never contains the API key or raw upstream body (SEC-16).
func BuildMessage(outcome Outcome, providerName, baseURL string) string {
	switch outcome {
	case OutcomeValid:
		return ""
	case OutcomeInvalidKey:
		return fmt.Sprintf(
			"The API key was rejected by %s. Check you copied the whole key and that it's still active in your %s account.",
			providerName,
			providerName,
		)
	case OutcomeNoCredit:
		return fmt.Sprintf(
			"Your %s key works, but the account has no credit. Add funds in your %s dashboard to use it.",
			providerName, providerName,
		)
	case OutcomeUnreachable:
		if isLoopbackBaseURL(baseURL) {
			// A loopback provider (Ollama, LM Studio, …) that can't be reached
			// almost certainly means the local server process isn't running —
			// the user's network is irrelevant, and there may be no "key" to
			// check at all for a keyless local server. Tell them what to do:
			// start it.
			return fmt.Sprintf(
				"Couldn't reach %s — the local server doesn't seem to be running. Start %s, then try again. Continuing for now; the key will be used as entered.",
				providerName, providerName,
			)
		}
		return fmt.Sprintf(
			"Couldn't reach %s to check the key — check your internet connection. Continuing for now; the key will be used as entered.",
			providerName,
		)
	case OutcomeRestricted:
		return fmt.Sprintf(
			"Your %s key works, but %s blocked this request (it may be restricted in your region, or the selected model isn't available to your account).",
			providerName,
			providerName,
		)
	default:
		return ""
	}
}

// ── ValidateKey ───────────────────────────────────────────────────────────────

// ValidateKey validates a provider API key by probing a real completion endpoint.
//
// Behavior:
//   - Empty/whitespace key → InvalidKey immediately, no network call (FR-007).
//   - Non-empty key → pick a probe model (FetchModels if Catalog empty), POST a
//     minimal completion to {BaseURL}/chat/completions, classify the response via
//     the R-A algorithm (FR-003).
//   - Only InvalidKey blocks (see ValidationResult.Blocks, derived from Outcome).
//   - Message is the curated FR-7 text (SEC-16: never the key or raw body).
//   - RawDetail holds the raw upstream snippet for server debug logging only.
func ValidateKey(ctx context.Context, in ValidateInput, checker URLChecker) ValidationResult {
	// FR-007: empty/whitespace key short-circuit — no network call.
	if strings.TrimSpace(in.APIKey) == "" {
		return ValidationResult{
			Outcome:   OutcomeInvalidKey,
			Message:   BuildMessage(OutcomeInvalidKey, in.ProviderName, in.BaseURL),
			RawDetail: "empty api key",
		}
	}

	// Resolve the probe candidates. For a CATALOG provider the document is
	// the source and NO `/models` pre-fetch happens at all (FR-022) — that
	// round trip used to run on every key check and told us nothing the
	// catalog does not already know. Only a row the catalog has no models
	// for (a custom endpoint, a local runtime) falls back to the live list.
	candidates := in.ProbeModels
	if len(candidates) > maxProbeAttempts {
		candidates = candidates[:maxProbeAttempts]
	}
	if len(candidates) == 0 {
		candidates = catalogProbeModels(in.ProviderID)
	}
	if len(candidates) == 0 {
		live := in.Catalog
		if len(live) == 0 {
			fetched, err := FetchModels(ctx, in.BaseURL, in.APIKey, checker)
			if err != nil {
				slog.Debug("providers: FetchModels failed during ValidateKey; proceeding without model list",
					"provider", in.ProviderID, "error", err)
			} else {
				live = fetched
			}
		}
		candidates = chatProbeCandidates(live)
	}

	if len(candidates) == 0 {
		// No probe model anywhere. Return Unreachable (proceed with a
		// warning) rather than a false InvalidKey.
		slog.Warn("providers: no probe model available; returning Unreachable",
			"provider", in.ProviderID)
		return ValidationResult{
			Outcome:   OutcomeUnreachable,
			Message:   BuildMessage(OutcomeUnreachable, in.ProviderName, in.BaseURL),
			RawDetail: "no chat model found in catalog",
		}
	}

	// Probe in document order, falling through to the next candidate only
	// when the upstream said the MODEL is missing — never on a credential or
	// transport answer, which is what we came to find out (F-25). Bounded by
	// maxProbeAttempts, which len(candidates) already respects.
	var outcome Outcome
	var rawDetail string
	var probedModel string
	for i, model := range candidates {
		outcome, rawDetail = probeCompletion(ctx, in.BaseURL, in.APIKey, model, checker)
		probedModel = model
		if i+1 < len(candidates) && outcome != OutcomeValid && isModelNotFound(rawDetail) {
			slog.Debug("providers: probe model not found upstream; trying the next candidate",
				"provider", in.ProviderID, "model", model)
			continue
		}
		break
	}

	return ValidationResult{
		Outcome:     outcome,
		Message:     BuildMessage(outcome, in.ProviderName, in.BaseURL),
		RawDetail:   rawDetail,
		ProbedModel: probedModel,
	}
}

// probeCompletion fires a single minimal POST /chat/completions and classifies the result.
// Returns (outcome, rawDetail). rawDetail is for server debug logging only (SEC-16).
func probeCompletion(ctx context.Context, baseURL, apiKey, model string, checker URLChecker) (Outcome, string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	payload := fmt.Sprintf(
		`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":1}`,
		model,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(baseURL, "/")+"/chat/completions",
		strings.NewReader(payload),
	)
	if err != nil {
		// URL construction failure — treat as unreachable.
		slog.Warn("providers: probe request construction failed",
			"provider_base", baseURL, "error", err)
		return OutcomeUnreachable, fmt.Sprintf("request construction: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("X-Api-Key", apiKey)
	// Anthropic-compat endpoints require the version header (SEC-24 / behavior-preserving).
	if strings.Contains(baseURL, "anthropic") {
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	if checker != nil {
		client = checker.SafeClient()
		client.Timeout = 15 * time.Second
	}

	resp, err := client.Do(req)
	if err != nil {
		// Transport / timeout / SSRF-block.
		return classify(err, 0, nil), fmt.Sprintf("transport error: %v", err)
	}
	defer resp.Body.Close()

	// Read a limited body for classification; cap at 4 KB.
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if readErr != nil {
		// A mid-stream read failure can leave body empty; surface it for debugging an
		// odd misclassification. classify still defaults safely (empty body → status-only).
		slog.Debug("providers: probe body read error", "status", resp.StatusCode, "error", readErr)
	}

	outcome := classify(nil, resp.StatusCode, body)
	// rawDetail for server debug log only — the caller must NOT send this to the user.
	raw := fmt.Sprintf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	return outcome, raw
}

// DefaultProbeModel returns the first catalog model a provider offers that is
// active, tool-calling and text-capable — the same rule the key-validation
// probe uses (FR-022, A-20), exported for the CLI onboarding wizard, which
// needs a concrete model id to write into the config it creates (A-21).
//
// Empty when the id is not a catalog provider or the row lists no such model;
// the caller then asks the operator.
func DefaultProbeModel(providerID string) string {
	c := catalogProbeModels(providerID)
	if len(c) == 0 {
		return ""
	}
	return c[0]
}
