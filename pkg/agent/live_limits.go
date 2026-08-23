// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ADR-066 D2 rung 4 — the live provider limits query (T066-10; FR-003,
// FR-001, FR-007; B-04, B-08).
//
// LiveLimits answers "what context window does the PROVIDER say this model
// has?" from a 24 h on-disk cache at $OMNIPUS_HOME/cache/model_limits.json,
// keyed by (provider id, base URL, model) — a catalog `api` change therefore
// yields a new key and a fresh answer.
//
// The query is made ON DEMAND ONLY: at the first resolution that reaches
// this rung for a key, never at boot (constructing or installing the rung
// performs no I/O beyond reading the cache file) and never on a timer. It
// is also never on the turn path: Lookup NEVER blocks on the network. A
// cache miss returns (0, false) immediately — ResolveWindow continues to
// the next rung now — and starts one background fetch for that key; the
// live value applies at the next resolution (the next reload, the next
// settings write, the next catalog projection). A failed fetch caches
// nothing (E9: an expired entry plus a dead endpoint falls through to the
// catalog/floor for a cloud row and to the context_window_unknown refusal
// for a local row) and backs off for liveLimitsFailureBackoff so repeated
// resolutions do not hammer a dead endpoint.
//
// Credential: a cloud row is queried only when the credential store has a
// key for the provider (rung skipped without one, nothing fetched). Local
// rows (ADR-067 `locality: local`) and OpenRouter need none.
//
// E17: a hand-edited cache can only LOWER the window — ResolveWindow clamps
// a live value to the catalog's capability when the catalog knows one.
//
// Per-protocol endpoints (ADR-066 D2, 2026-08-22 survey):
//   - ollama (local): GET {host}/api/ps → models[].context_length, the
//     window the model is LOADED with; then POST {host}/api/show →
//     model_info["<arch>.context_length"], the architecture maximum, for a
//     model that is not resident. Both field names were verified against a
//     running daemon (Ollama 0.32.3, 2026-08-24; ADR §16's "unverified" is
//     resolved). The native API lives at the row's host, not under /v1.
//   - openai-compatible, google, anthropic, xai: GET {base}/models (xAI:
//     /language-models; Google: `models[]` with `name: models/<id>` and
//     `inputTokenLimit`), matching the model's `id`/`name` and reading the
//     first present of context_length, context_window, max_input_tokens,
//     max_context_length, inputTokenLimit, max_model_len (vLLM). Providers
//     whose list carries no limit (OpenAI, DeepSeek, Z.ai, Moonshot,
//     MiniMax) simply miss and the ladder continues to the catalog.

const (
	// liveLimitsTTL is how long a fetched window is trusted (FR-003).
	liveLimitsTTL = 24 * time.Hour
	// liveLimitsFailureBackoff is how long a failed fetch suppresses a
	// retry for the same key. In-memory only — a restart retries.
	liveLimitsFailureBackoff = 15 * time.Minute
	// liveLimitsRequestTimeout bounds one background fetch.
	liveLimitsRequestTimeout = 10 * time.Second
	// liveLimitsMaxBody caps an upstream response body.
	liveLimitsMaxBody = 4 << 20
	// liveLimitsMaxWindow is the sanity ceiling on any reported window; a
	// value above it (tampered cache, broken endpoint) is ignored.
	liveLimitsMaxWindow = 100_000_000
	// liveLimitsFileVersion is the on-disk cache format version.
	liveLimitsFileVersion = 1
)

// LiveLimitsOptions configures NewLiveLimits.
type LiveLimitsOptions struct {
	// CachePath is the cache file, normally
	// $OMNIPUS_HOME/cache/model_limits.json. Required.
	CachePath string
	// Credential returns the provider's API key from the credential store,
	// or "" when there is none. nil means "no credential for anyone": every
	// cloud row that needs a key is skipped.
	Credential func(provider string) string
	// Client performs the upstream requests. nil uses a plain client with
	// liveLimitsRequestTimeout.
	Client *http.Client
	// Now is the clock; nil uses time.Now. Tests drive the TTL with it.
	Now func() time.Time
}

// LiveLimits is rung 4's implementation. Install with
// SetLiveWindowLookup(ll.Lookup).
type LiveLimits struct {
	cachePath  string
	credential func(string) string
	client     *http.Client
	now        func() time.Time

	mu       sync.Mutex
	loaded   bool
	entries  map[string]liveLimitEntry // key → entry (the on-disk mirror)
	inflight map[string]struct{}
	failedAt map[string]time.Time
	wg       sync.WaitGroup
}

// liveLimitEntry is one cached answer. not-wire-format: the on-disk cache
// record, never emitted to the SPA.
type liveLimitEntry struct {
	Provider  string    `json:"provider"`
	BaseURL   string    `json:"base_url"`
	Model     string    `json:"model"`
	Window    int       `json:"window"`
	FetchedAt time.Time `json:"fetched_at"`
}

// liveLimitsFile is the cache file. not-wire-format: on-disk only.
type liveLimitsFile struct {
	Version int              `json:"version"`
	Entries []liveLimitEntry `json:"entries"`
}

// NewLiveLimits builds the rung. It performs no I/O: the cache file is read
// lazily at the first Lookup and nothing is ever fetched until a resolution
// reaches the rung.
func NewLiveLimits(opts LiveLimitsOptions) *LiveLimits {
	ll := &LiveLimits{
		cachePath:  opts.CachePath,
		credential: opts.Credential,
		client:     opts.Client,
		now:        opts.Now,
		entries:    map[string]liveLimitEntry{},
		inflight:   map[string]struct{}{},
		failedAt:   map[string]time.Time{},
	}
	if ll.credential == nil {
		ll.credential = func(string) string { return "" }
	}
	if ll.client == nil {
		ll.client = &http.Client{Timeout: liveLimitsRequestTimeout}
	}
	if ll.now == nil {
		ll.now = time.Now
	}
	return ll
}

// Lookup is the LiveWindowLookup. It returns (window, true) from a fresh
// cache entry; otherwise (0, false) at once, starting a background fetch
// for the key when one is warranted (not already in flight, not backing
// off, credential present when required).
func (ll *LiveLimits) Lookup(provider, baseURL, model string) (int, bool) {
	provider, baseURL, model = strings.TrimSpace(provider), strings.TrimSpace(baseURL), strings.TrimSpace(model)
	if provider == "" || model == "" {
		return 0, false
	}
	key := liveLimitsKey(provider, baseURL, model)
	now := ll.now()

	ll.mu.Lock()
	defer ll.mu.Unlock()
	ll.loadLocked()

	if e, ok := ll.entries[key]; ok && liveEntryFresh(e, now) {
		return e.Window, true
	}
	if _, busy := ll.inflight[key]; busy {
		return 0, false
	}
	if at, failed := ll.failedAt[key]; failed && now.Sub(at) < liveLimitsFailureBackoff {
		return 0, false
	}

	target, ok := ll.planLocked(provider, baseURL, model)
	if !ok {
		return 0, false
	}
	ll.inflight[key] = struct{}{}
	ll.wg.Add(1)
	go ll.fetchAndStore(key, target)
	return 0, false
}

// Wait blocks until every background fetch started so far has finished.
// Shutdown and tests use it; the resolver never does.
func (ll *LiveLimits) Wait() { ll.wg.Wait() }

// liveTarget is one planned fetch.
type liveTarget struct {
	provider string
	baseURL  string
	model    string
	protocol catalog.Protocol
	apiKey   string
}

// planLocked decides whether and how the key is fetched: the row's
// protocol (catalog first, then inferred for a custom row) and whether a
// credential is required and present. ll.mu must be held.
func (ll *LiveLimits) planLocked(provider, baseURL, model string) (liveTarget, bool) {
	t := liveTarget{provider: provider, baseURL: baseURL, model: model}
	var locality catalog.Locality
	if row, ok := windowCatalog().Provider(provider); ok {
		t.protocol = row.Protocol
		locality = row.Locality
		if baseURL == "" {
			t.baseURL = row.API
		}
	} else {
		t.protocol = catalog.ProtocolOpenAICompatible
		if provider == "ollama" {
			t.protocol = catalog.ProtocolOllama
		}
		locality = catalog.DeriveLocality(provider, t.protocol, true, t.baseURL)
	}
	if t.baseURL == "" || t.protocol == catalog.ProtocolCLI {
		return t, false
	}
	if liveNeedsCredential(provider, locality) {
		t.apiKey = strings.TrimSpace(ll.credential(provider))
		if t.apiKey == "" {
			logger.DebugCF("agent", "Live limits query skipped: no credential in the store for the provider",
				map[string]any{"provider": provider, "model": model})
			return t, false
		}
	} else {
		t.apiKey = strings.TrimSpace(ll.credential(provider)) // sent when present, never required
	}
	return t, true
}

// liveNeedsCredential: local rows and OpenRouter need no key; every other
// cloud row does (FR-003 "rung skipped without one").
func liveNeedsCredential(provider string, locality catalog.Locality) bool {
	if locality == catalog.LocalityLocal {
		return false
	}
	return provider != "openrouter"
}

// fetchAndStore runs one background fetch and records the outcome.
func (ll *LiveLimits) fetchAndStore(key string, t liveTarget) {
	defer ll.wg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), liveLimitsRequestTimeout)
	defer cancel()
	window, err := ll.fetch(ctx, t)

	ll.mu.Lock()
	defer ll.mu.Unlock()
	delete(ll.inflight, key)
	now := ll.now()
	if err != nil || window <= 0 || window > liveLimitsMaxWindow {
		ll.failedAt[key] = now
		fields := map[string]any{"provider": t.provider, "model": t.model, "base_url": t.baseURL}
		if err != nil {
			fields["error"] = err.Error()
		} else {
			fields["window"] = window
		}
		logger.DebugCF("agent", "Live limits query did not yield a usable context window; next rung applies", fields)
		return
	}
	delete(ll.failedAt, key)
	ll.entries[key] = liveLimitEntry{Provider: t.provider, BaseURL: t.baseURL, Model: t.model, Window: window, FetchedAt: now}
	if err := ll.saveLocked(); err != nil {
		logger.WarnCF("agent", "Could not persist the live model limits cache", map[string]any{
			"path": ll.cachePath, "error": err.Error(),
		})
	}
	logger.InfoCF("agent", "Live context window cached", map[string]any{
		"provider": t.provider, "model": t.model, "window": window, "ttl": liveLimitsTTL.String(),
	})
}

// fetch dispatches on the row's protocol.
func (ll *LiveLimits) fetch(ctx context.Context, t liveTarget) (int, error) {
	switch t.protocol {
	case catalog.ProtocolOllama:
		return ll.fetchOllama(ctx, t)
	case catalog.ProtocolGoogle:
		return ll.fetchModelList(ctx, t, "/models?pageSize=1000", func(req *http.Request) {
			req.Header.Set("X-Goog-Api-Key", t.apiKey)
		})
	case catalog.ProtocolAnthropic:
		return ll.fetchModelList(ctx, t, "/models?limit=1000", func(req *http.Request) {
			req.Header.Set("X-Api-Key", t.apiKey)
			req.Header.Set("Anthropic-Version", "2023-06-01")
		})
	default:
		path := "/models"
		if t.provider == "xai" {
			path = "/language-models"
		}
		return ll.fetchModelList(ctx, t, path, func(req *http.Request) {
			if t.apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+t.apiKey)
			}
		})
	}
}

// fetchModelList GETs a model list and extracts the model's window.
func (ll *LiveLimits) fetchModelList(ctx context.Context, t liveTarget, path string, auth func(*http.Request)) (int, error) {
	body, err := ll.do(ctx, http.MethodGet, strings.TrimSuffix(t.baseURL, "/")+path, nil, auth)
	if err != nil {
		return 0, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return 0, fmt.Errorf("model list: %w", err)
	}
	var items []map[string]any
	for _, k := range []string{"data", "models"} {
		if raw, ok := doc[k]; ok {
			if err := json.Unmarshal(raw, &items); err == nil && len(items) > 0 {
				break
			}
		}
	}
	for _, item := range items {
		if !liveModelMatches(item, t.model) {
			continue
		}
		return liveWindowField(item), nil
	}
	return 0, nil
}

// fetchOllama reads the loaded window from /api/ps, then the architecture
// maximum from /api/show. The native API is at the row's host: the catalog
// `api` is the OpenAI-compatible http://host:11434/v1 and /v1 is dropped.
func (ll *LiveLimits) fetchOllama(ctx context.Context, t liveTarget) (int, error) {
	host := strings.TrimSuffix(strings.TrimSuffix(t.baseURL, "/"), "/v1")
	if body, err := ll.do(ctx, http.MethodGet, host+"/api/ps", nil, nil); err == nil {
		var ps struct { // not-wire-format: Ollama /api/ps response
			Models []map[string]any `json:"models"`
		}
		if jsonErr := json.Unmarshal(body, &ps); jsonErr == nil {
			for _, m := range ps.Models {
				if liveModelMatches(m, t.model) {
					if w := liveWindowField(m); w > 0 {
						return w, nil
					}
				}
			}
		}
	} else {
		return 0, err
	}
	payload, _ := json.Marshal(map[string]string{"model": t.model})
	body, err := ll.do(ctx, http.MethodPost, host+"/api/show", payload, func(req *http.Request) {
		req.Header.Set("Content-Type", "application/json")
	})
	if err != nil {
		return 0, err
	}
	var show struct { // not-wire-format: Ollama /api/show response
		ModelInfo map[string]any `json:"model_info"`
	}
	if err := json.Unmarshal(body, &show); err != nil {
		return 0, fmt.Errorf("ollama show: %w", err)
	}
	for k, v := range show.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			if w := liveInt(v); w > 0 {
				return w, nil
			}
		}
	}
	return 0, nil
}

// do performs one bounded request and returns the body on a 2xx JSON reply.
func (ll *LiveLimits) do(ctx context.Context, method, rawURL string, payload []byte, auth func(*http.Request)) ([]byte, error) {
	if _, err := url.Parse(rawURL); err != nil {
		return nil, err
	}
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if auth != nil {
		auth(req)
	}
	resp, err := ll.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("live limits: %s %s → status %d", method, rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, liveLimitsMaxBody))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("live limits: empty response")
	}
	return body, nil
}

// liveModelMatches: the item's id/name/model equals the model, with
// Google's "models/<id>" prefix and Ollama's ":latest" normalisation.
func liveModelMatches(item map[string]any, model string) bool {
	for _, k := range []string{"id", "name", "model"} {
		s, _ := item[k].(string)
		if s == "" {
			continue
		}
		s = strings.TrimPrefix(s, "models/")
		if s == model || s == model+":latest" {
			return true
		}
	}
	return false
}

// liveWindowField reads the first present limit field.
func liveWindowField(item map[string]any) int {
	for _, k := range []string{"context_length", "context_window", "max_input_tokens", "max_context_length", "inputTokenLimit", "max_model_len"} {
		if w := liveInt(item[k]); w > 0 {
			return w
		}
	}
	return 0
}

func liveInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return 0
}

func liveLimitsKey(provider, baseURL, model string) string {
	return provider + "\x1f" + baseURL + "\x1f" + model
}

func liveEntryFresh(e liveLimitEntry, now time.Time) bool {
	if e.Window <= 0 || e.Window > liveLimitsMaxWindow || e.FetchedAt.IsZero() {
		return false
	}
	age := now.Sub(e.FetchedAt)
	return age >= 0 && age < liveLimitsTTL
}

// loadLocked reads the cache file once. A missing or corrupt file is a cold
// cache, never an error. ll.mu must be held.
func (ll *LiveLimits) loadLocked() {
	if ll.loaded {
		return
	}
	ll.loaded = true
	raw, err := os.ReadFile(ll.cachePath)
	if err != nil {
		return
	}
	var f liveLimitsFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != liveLimitsFileVersion {
		logger.WarnCF("agent", "Ignoring an unreadable live model limits cache; it will be rebuilt on demand",
			map[string]any{"path": ll.cachePath})
		return
	}
	for _, e := range f.Entries {
		if e.Provider == "" || e.Model == "" {
			continue
		}
		ll.entries[liveLimitsKey(e.Provider, e.BaseURL, e.Model)] = e
	}
}

// saveLocked writes the fresh entries atomically. ll.mu must be held.
func (ll *LiveLimits) saveLocked() error {
	now := ll.now()
	f := liveLimitsFile{Version: liveLimitsFileVersion}
	for _, e := range ll.entries {
		if liveEntryFresh(e, now) {
			f.Entries = append(f.Entries, e)
		}
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ll.cachePath), 0o700); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(ll.cachePath, raw, 0o600)
}
