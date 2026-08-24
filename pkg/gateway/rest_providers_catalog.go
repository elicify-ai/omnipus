// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_providers_catalog.go — the REST half of ADR-067 (T067-10).
//
// Three things live here, all of them reading the ONE booted catalog
// (restAPI.providerCatalog) rather than any vendor table:
//
//  1. GET /api/v1/providers/catalog (FR-017) — the pre-serialised bytes +
//     quoted strong ETag pair that pkg/providers/catalog built at apply
//     time. The handler never marshals anything (SC-011) and never touches
//     the network.
//  2. The model-list source rule for GET /api/v1/providers (FR-020): a
//     `locality = cloud` row's models come from the catalog with NO
//     outbound call; only a `locality = local` row is listed live.
//  3. The PUT /api/v1/providers/{id} admission rules (FR-019, FR-035) —
//     unknown id, `tier: unsupported`, and the custom-row requirement.
//
// The /health catalog hook (FR-037) is here too, beside the catalog code
// it reports on.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

const (
	// providerCatalogUnavailableMsg is the FR-017 / US-7.AC4 503 body: a
	// typed error, never an empty 200 that a client would read as "this
	// installation has no providers".
	providerCatalogUnavailableMsg = "provider catalog unavailable"

	// providerCatalogCacheControl is the fixed Cache-Control the catalog
	// GET always sends (FR-017 "HTTP caching" block). The document is
	// per-installation state behind a bearer token, so it is `private`;
	// `max-age=0, must-revalidate` makes every read an If-None-Match
	// round trip that costs 304 bytes when nothing changed.
	providerCatalogCacheControl = "private, max-age=0, must-revalidate"
)

// HandleProvidersCatalog answers GET /api/v1/providers/catalog (FR-017,
// US-7.AC1–AC4, DS-5 rows 1–4).
//
// It is registered on its OWN exact path under withAuth, ahead of the
// /api/v1/providers/ subtree dispatcher — "catalog" is a reserved path
// segment and is never a provider id — so the 401 for an unauthenticated
// caller (US-7.AC2) is the middleware's, not this function's.
//
// The body is written verbatim from the snapshot the catalog published:
// bytes and ETag are read as ONE ServedCatalog value, so a concurrent
// apply can never hand a caller the bytes of one document under the ETag
// of another (T34c).
func (a *restAPI) HandleProvidersCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.providerCatalog == nil {
		jsonErr(w, http.StatusServiceUnavailable, providerCatalogUnavailableMsg)
		return
	}
	served, ok := a.providerCatalog.Served()
	if !ok {
		// E7: the embedded snapshot failed its own validation and no
		// persisted last-known-good could stand in. One boot ERROR was
		// already logged; every read of this route says so honestly.
		jsonErr(w, http.StatusServiceUnavailable, providerCatalogUnavailableMsg)
		return
	}

	w.Header().Set("ETag", served.ETag)
	w.Header().Set("Cache-Control", providerCatalogCacheControl)

	// Exact byte comparison, deliberately NOT RFC 7232 list parsing: the
	// spec's caching block admits a single quoted strong validator and
	// treats a weak (`W/"…"`) or unquoted value as no match (200). There
	// is no content negotiation either — one representation exists.
	if r.Header.Get("If-None-Match") == served.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(served.Body)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(served.Body); err != nil {
		// A reader that disconnected mid-body is not operator-actionable.
		slog.Debug("rest: providers catalog write failed", "error", err)
	}
}

// ── /health (FR-037) ────────────────────────────────────────────────────────

// catalogHealthState is the closure the gateway hands pkg/health: the
// catalog is degraded when no document is loaded (E7), when the last
// refresh attempt failed, when the served document came over the degraded
// raw-fallback transport, or when it is stale (updated_at older than 14
// days). The reason string is the catalog's own error text — the operator
// reads WHY on /health without correlating log lines.
func catalogHealthState(cat *catalog.Catalog) (bool, string) {
	if cat == nil {
		return true, "catalog: no document loaded"
	}
	degraded, err := cat.Degraded()
	switch {
	case !degraded:
		return false, ""
	case err != nil:
		return true, err.Error()
	default:
		return true, "catalog: degraded"
	}
}

// ── the model-list source rule (FR-020) ─────────────────────────────────────

// providerRowSource is the resolved provenance of one configured provider
// row: what the catalog says about the id, whether the operator typed it
// as a custom endpoint, and which base URL and protocol the row actually
// uses. It is computed once per provider in the GET /providers loop.
type providerRowSource struct {
	// catalogReady is false when no catalog document is loaded (E7, and
	// any unit test that never installs one). Every classification below
	// is suppressed in that state: an absent catalog must not turn every
	// configured row into an unknown provider.
	catalogReady bool
	// row is the catalog row for the id; valid only when known.
	row   catalog.Provider
	known bool
	// custom is the persisted ModelConfig.Custom flag (X-13). Checks key
	// on this flag, never on a literal id.
	custom   bool
	protocol catalog.Protocol
	locality catalog.Locality
	apiBase  string
}

// unknownProvider reports the FR-016 state: the catalog is loaded, it does
// not carry this id, and the row is not a custom endpoint either.
func (s providerRowSource) unknownProvider() bool {
	return s.catalogReady && !s.known && !s.custom
}

// resolveProviderRow classifies one configured provider id against the
// served catalog. cfgRow may be nil (no representative config row).
func (a *restAPI) resolveProviderRow(id string, cfgRow *config.ModelConfig) providerRowSource {
	src := providerRowSource{}
	if cfgRow != nil {
		src.custom = cfgRow.Custom
		src.protocol = catalog.Protocol(strings.TrimSpace(cfgRow.Protocol))
		src.apiBase = strings.TrimSpace(cfgRow.APIBase)
	}
	if a.providerCatalog != nil && a.providerCatalog.Document() != nil {
		src.catalogReady = true
		src.row, src.known = a.providerCatalog.Provider(id)
	}
	if src.known {
		if src.protocol == "" {
			src.protocol = src.row.Protocol
		}
		if src.apiBase == "" {
			src.apiBase = src.row.API
		}
		src.locality = src.row.Locality
		return src
	}
	// Not a catalog row: derive locality from what the operator configured
	// (FR-039's single predicate — a custom row pointing at a loopback or
	// private host is local; anything else is cloud).
	src.locality = catalog.DeriveLocality(id, src.protocol, src.custom, src.apiBase)
	return src
}

// providerModelList is FR-020's single rule for what `Provider.models`
// contains, and the only place in the providers GET that may make an
// outbound call:
//
//   - locality = cloud, id in the catalog → the catalog's model ids, with
//     NO outbound request (US-9.AC1, SC-003 — the offline path);
//   - locality = local → the live listing, because a local endpoint's
//     model set is whatever the operator has pulled onto that machine and
//     no published document can know it (US-9.AC3);
//   - unknown-provider → the empty list (S67 Q4);
//   - custom row, or no catalog loaded at all → the operator's own slugs.
//
// The returned warning is non-fatal advisory text for Provider.warning.
func (a *restAPI) providerModelList(
	ctx context.Context,
	id string,
	src providerRowSource,
	apiKey string,
	userModels []string,
) (models []string, warning string) {
	switch {
	case src.locality == catalog.LocalityLocal && src.apiBase != "":
		live, err := fetchLocalModels(ctx, src.protocol, src.apiBase, apiKey)
		if err != nil {
			slog.Warn("rest: could not list models from local provider endpoint",
				"provider", id, "api_base", src.apiBase, "error", err)
			warning = fmt.Sprintf("could not fetch upstream model list: %v", err)
			break
		}
		models = live
	case src.known:
		models = catalogModelIDs(src.row)
	case src.unknownProvider():
		models = []string{}
	}
	if models == nil {
		if slugs := dedupeNonEmpty(userModels); len(slugs) > 0 {
			models = slugs
		}
	}
	if models == nil {
		// Provider.yaml requires models:array — nil marshals as null and
		// fails the SPA's zod edge.
		models = []string{}
	}
	return models, warning
}

// catalogModelIDs returns the row's model ids in document order.
func catalogModelIDs(row catalog.Provider) []string {
	out := make([]string, 0, len(row.Models))
	for i := range row.Models {
		out = append(out, row.Models[i].ID)
	}
	return out
}

// localModelListTimeout bounds the one live call the providers GET may
// make. A local endpoint that is not running must not hold the request
// open — the row simply lists nothing and carries the warning.
const localModelListTimeout = 5 * time.Second

// fetchLocalModels lists the models a LOCAL endpoint currently serves
// (FR-020, US-9.AC3): `/api/tags` for the ollama protocol, `/v1/models`
// for everything else (the catalog's `api` for those rows already ends in
// /v1, which providers.FetchModels completes with /models).
//
// No SSRF checker is passed, deliberately and by definition: `locality =
// local` MEANS loopback/private (catalog.DeriveLocality), so the guard
// would reject every single one of these calls and FR-020's local half
// would be dead code. The URL is not request-controlled — it is the
// catalog row's own `api`, or a persisted api_base that was already
// classified local — so there is no caller-supplied target to pivot with.
func fetchLocalModels(ctx context.Context, protocol catalog.Protocol, apiBase, apiKey string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, localModelListTimeout)
	defer cancel()
	if protocol == catalog.ProtocolOllama {
		return fetchOllamaTags(ctx, apiBase)
	}
	return providers_pkg.FetchModels(ctx, apiBase, apiKey, providers_pkg.NoopChecker{})
}

// fetchOllamaTags lists the locally pulled models from ollama's native
// /api/tags endpoint. The catalog carries ollama's OpenAI-compatible base
// (…:11434/v1); /api/tags hangs off the root, so the /v1 suffix is
// trimmed.
func fetchOllamaTags(ctx context.Context, apiBase string) ([]string, error) {
	root := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(apiBase), "/"), "/v1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: localModelListTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama tags: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
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

// ── PUT admission (FR-019, FR-035) ──────────────────────────────────────────

// providerAdmission is PUT /providers/{id}'s view of the ONE FR-019/FR-035
// gate. The rule itself lives in providers.AdmitIn so the onboarding probe
// and the CLI wizard cannot drift from it (T067-12); this wrapper exists
// only to keep the handler reading against the catalog the gateway booted
// rather than the process-wide one.
//
// It returns the typed provider error to report, or nil when the id may be
// configured. custom reports whether the accepted row is an operator-named
// custom endpoint, which the caller persists as ModelConfig.Custom.
func providerAdmission(
	cat *catalog.Catalog,
	id string,
	apiBase string,
	protocol string,
) (custom bool, err error) {
	return providers_pkg.AdmitIn(cat, id, apiBase, protocol)
}

// providerWireProtocol maps a resolved protocol onto the wire enum,
// returning nil when it is empty or off-contract (an unsupported catalog
// row carries no protocol at all).
func providerWireProtocol(p catalog.Protocol) *gen.ProviderProtocol {
	v := gen.ProviderProtocol(p)
	if !v.Valid() {
		return nil
	}
	return &v
}

// providerWireLocality maps the derived locality onto the wire enum.
func providerWireLocality(l catalog.Locality) *gen.ProviderLocality {
	v := gen.ProviderLocality(l)
	if !v.Valid() {
		return nil
	}
	return &v
}

// providerWireCLIKind maps a catalog row's cli_kind onto the wire enum.
func providerWireCLIKind(kind string) *gen.ProviderCliKind {
	v := gen.ProviderCliKind(strings.TrimSpace(kind))
	if !v.Valid() {
		return nil
	}
	return &v
}

// applyProviderIdentity stamps the ADR-067 identity fields onto a
// providers[] entry being written by PUT /api/v1/providers/{id}: the
// explicit base URL, the selected protocol, and the custom flag (X-13 —
// `custom: true` is what every later check reads, never the literal id).
//
// An absent request field leaves the persisted value alone; `custom` is
// rewritten on every PUT because admission has just recomputed it against
// the current catalog, and a row that stopped being custom (its id joined
// the catalog) must stop claiming to be.
func applyProviderIdentity(entry map[string]any, apiBase, protocol string, custom bool) {
	if apiBase != "" {
		entry["api_base"] = apiBase
	}
	if protocol != "" {
		entry["protocol"] = protocol
	}
	if custom {
		entry["custom"] = true
	} else {
		delete(entry, "custom")
	}
}
