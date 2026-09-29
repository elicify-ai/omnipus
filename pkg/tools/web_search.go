package tools

// web_search.go — the ADR-096 WS-TOOL surface: role resolution (R-table),
// the failover ladder, capability arguments, and the result-text rules.
// Oracle: docs/internal/specs/web-search-provider-model-spec.md.
//
// DUAL PATH: when WebSearchToolOptions.Roles is nil the tool keeps the exact
// legacy behaviour (schema and error text unchanged — the legacy guard test
// pins both). When Roles is non-nil, Execute and Parameters dispatch here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"bytes"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// roleEntries is the per-call snapshot of the usable set the resolver and
// the dynamic schema both read.
type roleEntries struct {
	defaultID    string
	fallbackID   string // "" = no fallback
	fallbackAuto bool   // true when the fallback is the R3 auto-DuckDuckGo
	// ignoredSameAsDefault is the R5 wire signal: the stored fallback named
	// the default, so resolution healed the interpretation without a write.
	// Only set when a default is actually stored — the both-fields-empty
	// (undecided) state also satisfies fallbackRaw == defaultID and must not
	// read as "ignored".
	ignoredSameAsDefault bool
	usable               map[string]bool
}

// searchProviderError pairs a failure class with the provider's message.
// Error() returns the message only, so legacy error text is unchanged.
type searchProviderError struct {
	class string
	msg   string
}

func (e *searchProviderError) Error() string { return e.msg }

// capabilitySearchProvider is implemented by providers that can honour depth
// and/or site filters; the tool type-switches, never the concrete types.
type capabilitySearchProvider interface {
	SearchProvider
	SearchWithCaps(ctx context.Context, req searchRequest) (string, error)
	honoursDepth() bool
	honoursSiteFilters() bool
}

// searchProviderCatalogueIDs derives the catalogue ids in catalogue order —
// the operator-facing order lists in refusals, notes and the provider enum
// render in. Derived (D15/FR-035): an id appended to the catalogue is
// immediately part of every rendered list with no edit here.
func searchProviderCatalogueIDs() []string {
	ids := make([]string, 0, len(config.SearchProviderCatalogue))
	for _, def := range config.SearchProviderCatalogue {
		ids = append(ids, def.ID)
	}
	return ids
}

// searchProviderKeyed reports whether an id needs an API key to be usable.
// Catalogue-defined base-URL providers need a URL; DuckDuckGo needs nothing. Derived from
// the catalogue (D15).
func searchProviderKeyed(id string) bool {
	def, ok := config.SearchProviderDefByID(id)
	return ok && def.Keyed
}

// unusableReason maps a not-currently-usable id onto the spec's fixed reason
// vocabulary (spec 338–341): "no API key", "switched off", "unknown id".
// Enabled-but-keyless is "no API key"; anything else not usable is "switched
// off" (a catalogue-defined base-URL provider with a missing URL also
// reports "switched off"; there is no separate vocabulary for that).
func unusableReason(cfg *config.WebToolsConfig, id string) string {
	if !slices.Contains(searchProviderCatalogueIDs(), id) {
		return "unknown id"
	}
	if cfg.UsableSearchProvider(id) {
		return ""
	}
	if searchProviderKeyed(id) {
		if searchProviderEnabledInCfg(cfg, id) {
			return "no API key"
		}
		return "switched off"
	}
	return "switched off"
}

// searchProviderEnabledInCfg reads only the enabled flag for an id. Derived
// from the catalogue accessors (D15).
func searchProviderEnabledInCfg(cfg *config.WebToolsConfig, id string) bool {
	def, ok := config.SearchProviderDefByID(id)
	if !ok {
		return false
	}
	return def.Enabled(cfg)
}

// catalogueHonoursDepth and catalogueHonoursSiteFilters read the capability
// matrix from the single provider catalogue (ADR-096 D15/FR-035). The
// per-provider honours* methods below stay one-line delegations so interface
// compliance is unchanged while the VALUE lives only in the catalogue.
func catalogueHonoursDepth(id string) bool {
	if def, ok := config.SearchProviderDefByID(id); ok {
		return def.HonoursDepth
	}
	return false
}

func catalogueHonoursSiteFilters(id string) bool {
	if def, ok := config.SearchProviderDefByID(id); ok {
		return def.HonoursSiteFilters
	}
	return false
}

// resolveRoles runs the R-table: resolve default, resolve fallback, then
// classify per the spec's operational order (spec 220–317).
func resolveRoles(cfg *config.WebToolsConfig) roleEntries {
	defaultID := strings.TrimSpace(cfg.DefaultProvider)
	fallbackRaw := strings.TrimSpace(cfg.FallbackProvider)

	usable := make(map[string]bool, len(searchProviderCatalogueIDs()))
	for _, id := range searchProviderCatalogueIDs() {
		usable[id] = cfg.UsableSearchProvider(id)
	}

	entries := roleEntries{defaultID: defaultID, usable: usable}

	// Fallback resolution, in the spec's precedence order.
	switch {
	case fallbackRaw == config.SearchProviderNone:
		// R2: an explicit "none" means no fallback, even if DDG is usable.
	case fallbackRaw == defaultID:
		// R5: fallback equal to the default is no fallback at all.
		if defaultID != "" {
			entries.ignoredSameAsDefault = true
		}
	case fallbackRaw == "":
		// R3: absent → auto-DuckDuckGo when DDG is usable and the default
		// is a USABLE provider other than DuckDuckGo. The usable-default
		// conjunct is R3's own third condition: without it, an unusable
		// default + absent fallback + usable DDG would hand off to DDG
		// even though R6 requires an R3/R4 fallback and R7 says nobody
		// runs — D3's "it does not run because nothing else matched".
		// US-1 acceptance 5 stays reachable: its "resolved fallback is a
		// usable DuckDuckGo" is an explicit R4 pick.
		if defaultID != config.SearchProviderDuckDuckGo &&
			usable[config.SearchProviderDuckDuckGo] &&
			usable[defaultID] {
			entries.fallbackID = config.SearchProviderDuckDuckGo
			entries.fallbackAuto = true
		}
	case !slices.Contains(searchProviderCatalogueIDs(), fallbackRaw):
		// Unknown fallback id: not a role at all — never called, never
		// listed (lane decision; R4b covers known-unusable ids only).
	case !usable[fallbackRaw]:
		// R4b: a known id that is not usable is still a role — the ladder
		// must never call it, but must list it as "not called: reason".
		entries.fallbackID = fallbackRaw
	default:
		// R4: an explicit usable fallback.
		entries.fallbackID = fallbackRaw
	}

	return entries
}

// SearchRoleSnapshot is the exported, gateway-facing form of the resolved
// web-search roles: who is the default, who is the resolved fallback, and
// which ids are usable. ADR-096 (FR-035: one resolver — the gateway must not
// grow a second implementation of R1-R9, so it calls this instead).
type SearchRoleSnapshot struct {
	// DefaultID is the stored default exactly as the config carries it —
	// possibly unusable (FR-028: the payload's default_search keeps the
	// stored id while the row's active flag stays off).
	DefaultID string
	// FallbackID is the resolved fallback: the stored id when usable (R4),
	// the auto-DuckDuckGo when the stored value is absent and R3 applies,
	// and also a KNOWN-but-unusable stored id (R4b — the role exists, the
	// ladder must never call it, the payload lists it under not called).
	// "" for explicit none (R2), R5 (same as default), an UNKNOWN fallback
	// id (not a role at all), and the undecided state.
	FallbackID string
	// FallbackAutomatic is true only when FallbackID came from the R3
	// absent-value rule rather than an operator choice.
	FallbackAutomatic bool
	// IgnoredSameAsDefault is the R5 signal (fallback_ignored_reason).
	IgnoredSameAsDefault bool
	// Usable is one usability verdict per catalog id.
	Usable map[string]bool
}

// ResolveSearchRoleSnapshot resolves the web-search roles for cfg through the
// same resolver the runtime ladder uses (pkg/tools/web_search.go::resolveRoles)
// — the single authority on R1-R9.
func ResolveSearchRoleSnapshot(cfg *config.WebToolsConfig) SearchRoleSnapshot {
	e := resolveRoles(cfg)
	return SearchRoleSnapshot{
		DefaultID:            e.defaultID,
		FallbackID:           e.fallbackID,
		FallbackAutomatic:    e.fallbackAuto,
		IgnoredSameAsDefault: e.ignoredSameAsDefault,
		Usable:               e.usable,
	}
}

// notCalledReason returns the spec reason for a role that was never called,
// or "" when the provider was usable at call time.
func (e roleEntries) notCalledReason(cfg *config.WebToolsConfig, id string) string {
	if e.usable[id] {
		return ""
	}
	return unusableReason(cfg, id)
}

// searchRequest is everything one provider attempt needs.
type searchRequest struct {
	query          string
	count          int
	rangeFilter    string
	depth          string // agent-requested depth: ""/low/medium/high
	includeDomains []string
	excludeDomains []string
	namedID        string // provider arg, "" = not named
}

// ExaSearchProvider calls the Exa search API (ADR-096 D2/AC-1).
type ExaSearchProvider struct {
	apiKey string
	// keySource is the D4a live resolver handle (finding K1): when non-nil
	// (the ADR-096 dynamic path), the key is read from it at CALL time — the
	// same live config the usability test reads. The construction snapshot
	// (apiKey) stays the fallback for the legacy path and direct
	// constructions. An empty effective key is "not usable: no API key"
	// (D16), never a hop.
	keySource   func() string
	baseURL     string
	client      *http.Client
	ingestBound int64
	maxResults  int
}

// newExaProvider builds the Exa provider; empty base URL falls back to the
// shipped default.
func newExaProvider(opts WebSearchToolOptions, ingestBound int64) (*ExaSearchProvider, error) {
	baseURL := opts.ExaBaseURL
	if baseURL == "" {
		baseURL = "https://api.exa.ai/search"
	}
	// S1: the same client factory as every other provider. A stock
	// http.Client would send the Bearer key to whatever base_url is set,
	// including a private address the SSRF checker is supposed to block.
	client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client for Exa: %w", err)
	}
	provider := &ExaSearchProvider{
		apiKey:      opts.ExaAPIKey,
		baseURL:     baseURL,
		client:      client,
		ingestBound: ingestBound,
		maxResults:  10,
	}
	// K1: on the dynamic path the key is read at call time through the same
	// live config the usability test reads (D4a / AC-16). The snapshot above
	// stays the fallback for the legacy path and direct constructions.
	if opts.Roles != nil {
		roles := opts.Roles
		provider.keySource = func() string {
			c := roles()
			if c == nil {
				return ""
			}
			return c.Exa.APIKey()
		}
	}
	return provider, nil
}

// Failure classes (spec 292–317). Hop classes move to the fallback; final
// classes end the call. "empty" is a success shape (well-formed, zero
// results), not an error class here.
const (
	classNetwork     = "network"
	classAuth        = "auth"
	classRateLimit   = "rate_limit"
	classUpstream    = "upstream"
	classBadResponse = "bad_response"
	classRejected    = "rejected"
	classCancelled   = "cancelled"
	classIngestBound = "ingest-bound"
	// classNotUsable is a constructor failure. It is not a hop class: the
	// provider was never built, so the agent is told "not usable: <cause>"
	// (ADR-096 D16) instead of a network retry.
	classNotUsable = "not usable"
)

// hopClass reports whether a failure class hops to the fallback (spec "Which
// failures hop": network, auth, rate_limit, upstream, bad_response). Final
// classes — rejected, cancelled, ingest-bound — end the call.
func hopClass(class string) bool {
	switch class {
	case classNetwork, classAuth, classRateLimit, classUpstream, classBadResponse:
		return true
	}
	return false
}

// defaultCallBudget is D17a's default: the whole call — every attempt, all
// hops included — must finish inside this budget.
const defaultCallBudget = 45 * time.Second

// providerTimeout maps a provider id to its own HTTP timeout (the same
// constants the constructors use).
func providerTimeout(id string) time.Duration {
	switch id {
	case config.SearchProviderPerplexity:
		return perplexityTimeout
	default:
		return searchTimeout
	}
}

// classifySearchFailure maps a provider error message onto a failure class.
// Substring rules run first (the "all api keys failed" wrapper embeds the
// inner message, so status and transport text survives aggregation), then a
// numeric status parse. Attempt-ctx expiry is reclassified as network by
// runProvider before this runs.
func classifySearchFailure(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "ingest bound"):
		return classIngestBound
	case strings.Contains(lower, "context canceled"):
		return classCancelled
	case strings.Contains(lower, "context deadline exceeded"):
		return classNetwork
	case strings.Contains(lower, "request failed"):
		return classNetwork
	case strings.Contains(lower, "failed to read response"):
		return classNetwork
	case strings.Contains(lower, "failed to parse response"):
		return classBadResponse
	}
	if s := statusInMessage(lower); s > 0 {
		switch {
		case s == 401 || s == 403:
			return classAuth
		case s == 429:
			return classRateLimit
		case s >= 500 || s == 408:
			return classUpstream
		case s == 400 || s == 422:
			return classRejected
		default:
			return classBadResponse
		}
	}
	return classBadResponse
}

// statusInMessage extracts the first numeric HTTP status the provider error
// text carries ("tavily api error (status 401): ..."), 0 when none.
func statusInMessage(lower string) int {
	idx := strings.Index(lower, "status ")
	if idx < 0 {
		return 0
	}
	rest := lower[idx+len("status "):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

// runProvider is the per-attempt wrapper: attempt ctx (the whole call's
// remaining budget capped at the provider's own timeout), capability dispatch
// (type-switch on capabilitySearchProvider), and class wrapping. Redaction
// and truncation happen at text-composition time, not here.
func (t *WebSearchTool) runProvider(
	ctx context.Context,
	start time.Time,
	id string,
	req searchRequest,
) (string, *searchProviderError) {
	p, ok := t.dynamic[id]
	if reason, failed := t.constructErr[id]; failed {
		return "", &searchProviderError{class: classNotUsable, msg: reason}
	}
	if !ok {
		return "", &searchProviderError{class: classNetwork, msg: id + ": provider not constructed"}
	}
	budget := t.callBudget
	if budget <= 0 {
		budget = defaultCallBudget
	}
	remaining := budget - time.Since(start)
	timeout := providerTimeout(id)
	if remaining < timeout {
		timeout = remaining
	}
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var text string
	var err error
	if capP, isCap := p.(capabilitySearchProvider); isCap {
		text, err = capP.SearchWithCaps(attemptCtx, req)
	} else {
		text, err = p.Search(attemptCtx, req.query, req.count, req.rangeFilter)
	}
	if err != nil {
		var spErr *searchProviderError
		if errors.As(err, &spErr) {
			// The provider pre-classified the failure (e.g. DDG non-200 is a
			// bad_response, not the generic 5xx upstream).
			return "", spErr
		}
		if attemptCtx.Err() == context.DeadlineExceeded {
			// Attempt-ctx expiry (budget or per-provider timeout) reads as
			// network so the hop ladder treats it as transient (D17a).
			return "", &searchProviderError{class: classNetwork, msg: err.Error()}
		}
		if ctx.Err() != nil {
			// The PARENT context died: the whole call is cancelled — final.
			return "", &searchProviderError{class: classCancelled, msg: err.Error()}
		}
		return "", &searchProviderError{class: classifySearchFailure(err.Error()), msg: err.Error()}
	}
	return text, nil
}

// searchProviderDefaultCount is the dynamic path's default result count.
const searchProviderDefaultCount = 10

// validSearchHostname applies FR-026: the rule set of
// pkg/gateway/video_embed_hosts.go::validVideoEmbedHosts (bare DNS name,
// no IP literal, at most 253 characters, labels of 1-63), after the search
// normalisation the spec adds (lowercase, one trailing dot stripped).
// Returns the normalised hostname. A failure rejects the call; entries are
// not dropped.
func validSearchHostname(raw string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(raw))
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", fmt.Errorf("empty domain entry")
	}
	// Length is the normalised name, so a trailing dot does not count.
	// The raw value is not quoted: a 10 KB entry must not be copied into the
	// tool error.
	if len(h) > 253 {
		return "", fmt.Errorf("entry is over 253 characters")
	}
	if strings.Contains(h, "*") {
		return "", fmt.Errorf("%q is not a bare hostname (no wildcards)", raw)
	}
	if strings.Contains(h, "://") || strings.ContainsAny(h, "/:?#@\\ ") {
		return "", fmt.Errorf("%q is not a bare hostname (no ports, schemes or paths)", raw)
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("%q is not a bare hostname", raw)
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("%q has an invalid label", raw)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "", fmt.Errorf("%q has invalid characters", raw)
			}
		}
	}
	last := labels[len(labels)-1]
	allDigits := true
	for _, r := range last {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return "", fmt.Errorf("%q is an IP literal, not a hostname", raw)
	}
	return h, nil
}

// normalizeSearchDomains validates a raw argument list into the wire list:
// lowercase + trim, ONE trailing dot stripped, dedupe preserving first
// occurrence, count cap 10, per-entry cap 253 chars.
func normalizeSearchDomains(raws []any, kind string) ([]string, error) {
	if len(raws) > 10 {
		return nil, fmt.Errorf("%s: at most 10 domains (got %d)", kind, len(raws))
	}
	var out []string
	for _, raw := range raws {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%s entries must be strings", kind)
		}
		h, err := validSearchHostname(s)
		if err != nil {
			return nil, err
		}
		// Dedupe, preserving first occurrence.
		if slices.Contains(out, h) {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

// parseDynamicArgs reads and validates the dynamic schema's arguments.
// Non-nil ToolResult is a refusal (query missing, count/range/depth/domain
// violations — all before any request; house style: reject, never clamp).
func (t *WebSearchTool) parseDynamicArgs(args map[string]any) (searchRequest, *ToolResult) {
	req := searchRequest{count: searchProviderDefaultCount}
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return req, ErrorResult("query is required")
	}
	req.query = strings.TrimSpace(query)

	count64, err := getInt64Arg(args, "count", int64(searchProviderDefaultCount))
	if err != nil {
		return req, ErrorResult(err.Error())
	}
	if count64 < 1 || count64 > 10 {
		// Rejected, not clamped — same house style as the legacy path.
		return req, ErrorResult(fmt.Sprintf("count must be between 1 and 10 (got %d)", count64))
	}
	req.count = int(count64)

	rangeCode, err := normalizeSearchRange("")
	if err != nil {
		return req, ErrorResult(err.Error())
	}
	if rawRange, exists := args["range"]; exists {
		rangeStr, ok := rawRange.(string)
		if !ok {
			return req, ErrorResult("range must be a string")
		}
		rangeCode, err = normalizeSearchRange(rangeStr)
		if err != nil {
			return req, ErrorResult(err.Error())
		}
	}
	req.rangeFilter = rangeCode
	if rawDepth, exists := args["depth"]; exists {
		depth, ok := rawDepth.(string)
		if !ok {
			return req, ErrorResult("depth must be a string")
		}
		switch depth {
		case "", "low", "medium", "high":
		default:
			return req, ErrorResult("depth must be one of: low, medium, high")
		}
		req.depth = depth
	}
	for _, pair := range []struct {
		key  string
		dest *[]string
	}{
		{"include_domains", &req.includeDomains},
		{"exclude_domains", &req.excludeDomains},
	} {
		if rawList, exists := args[pair.key]; exists && rawList != nil {
			list, ok := rawList.([]any)
			if !ok {
				return req, ErrorResult(pair.key + " must be an array of strings")
			}
			domains, err := normalizeSearchDomains(list, pair.key)
			if err != nil {
				return req, ErrorResult("rejected: " + err.Error())
			}
			*pair.dest = domains
		}
	}
	// Exclusion wins: a domain in both lists drops out of the include list;
	// the exclude list is kept whole (spec 479-527).
	if len(req.includeDomains) > 0 && len(req.excludeDomains) > 0 {
		kept := req.includeDomains[:0]
		for _, d := range req.includeDomains {
			if !slices.Contains(req.excludeDomains, d) {
				kept = append(kept, d)
			}
		}
		req.includeDomains = kept
	}
	if rawProvider, exists := args["provider"]; exists && rawProvider != nil {
		id, ok := rawProvider.(string)
		if !ok {
			return req, ErrorResult("provider must be a string")
		}
		req.namedID = strings.TrimSpace(id)
	}
	return req, nil
}

// searchCallRecord carries ADR-096 D20's per-call observability fields from
// the executors back to executeDynamic, which emits exactly ONE structured
// record per search call (spec test 50). The provider that answered and its
// role, the class of the attempt that ended the call, and whether a hop,
// refusal or skip occurred; the resolved default and fallback come from
// roleEntries at emission time.
type searchCallRecord struct {
	servedID string
	role     string
	class    string
	hop      bool
	refusal  bool
	skip     string
}

// executeDynamic is the new-path Execute: parse, resolve roles, run the
// pre-flight refusals against the first runner, then the ladder.
func (t *WebSearchTool) executeDynamic(ctx context.Context, args map[string]any) *ToolResult {
	req, errRes := t.parseDynamicArgs(args)
	if errRes != nil {
		return errRes
	}
	cfg := t.roles()
	entries := resolveRoles(cfg)
	start := time.Now()
	rec := &searchCallRecord{}
	var res *ToolResult
	if req.namedID != "" {
		res = t.executeChosen(ctx, cfg, entries, req, start, rec)
	} else {
		res = t.executeDefaultPath(ctx, cfg, entries, req, start, rec)
	}
	t.emitSearchCallRecord(entries, req, rec)
	return res
}

// emitSearchCallRecord writes the D20 record for one finished search call.
// "skip" carries the exact skip reason ("cannot honour include_domains",
// "no time budget remaining"); "class" is the class of the attempt that
// ended the call ("" on a clean success).
func (t *WebSearchTool) emitSearchCallRecord(entries roleEntries, req searchRequest, rec *searchCallRecord) {
	if rec == nil {
		return
	}
	logger.InfoCF("tool", "web search call", map[string]any{
		"default":  entries.defaultID,
		"fallback": entries.fallbackID,
		"served":   rec.servedID,
		"role":     rec.role,
		"depth":    req.depth,
		"class":    rec.class,
		"hop":      rec.hop,
		"refusal":  rec.refusal,
		"skip":     rec.skip,
	})
}

// capabilityRefusal builds the pre-request refusal when the provider that
// would run cannot honour a requested capability. Exact spec shape:
// "search failed" + "- <id> (<role>): rejected: <what> is not supported"
// + "Providers that support <what>: <usable ids that honour it>".
func (t *WebSearchTool) capabilityRefusal(
	entries roleEntries,
	id, role, what string,
) *ToolResult {
	plural := what + "s"
	if what == "depth" {
		plural = "depth"
	}
	var ids []string
	for _, cand := range searchProviderCatalogueIDs() {
		if entries.usable[cand] {
			if c, ok := t.dynamic[cand].(capabilitySearchProvider); ok {
				if (what == "depth" && c.honoursDepth()) || (what != "depth" && c.honoursSiteFilters()) {
					ids = append(ids, cand)
				}
			}
		}
	}
	text := fmt.Sprintf("search failed\n- %s (%s): rejected: %s is not supported\nProviders that support %s: %s",
		id, role, what, plural, strings.Join(ids, ", "))
	return ErrorResult(text)
}

// preflightCheck refuses a call before any request when the first runner
// cannot honour a requested capability. Two cases: depth on a provider that
// honours no depth, and GLM with agent depth low (GLM honours depth but has
// no low value — spec: refuse, no request). Include-domain pre-flight.
func (t *WebSearchTool) preflightCheck(
	entries roleEntries,
	firstID, firstRole string,
	req searchRequest,
) *ToolResult {
	if req.depth != "" {
		c, isCap := t.dynamic[firstID].(capabilitySearchProvider)
		if !isCap || !c.honoursDepth() {
			return t.capabilityRefusal(entries, firstID, firstRole, "depth")
		}
		if firstID == config.SearchProviderGLM && req.depth == "low" {
			return t.capabilityRefusal(entries, firstID, firstRole, "depth")
		}
	}
	if len(req.includeDomains) > 0 {
		c, isCap := t.dynamic[firstID].(capabilitySearchProvider)
		if !isCap || !c.honoursSiteFilters() {
			return t.capabilityRefusal(entries, firstID, firstRole, "site filter")
		}
	}
	return nil
}

// successText composes the success result: the provider's result text, then
// the "Search provider:" role line, then any notes.
func successText(providerText, id, role string, notes []string) *ToolResult {
	text := providerText + "\n\nSearch provider: " + id + " (" + role + ")"
	for _, n := range notes {
		text += "\n" + n
	}
	return &ToolResult{ForLLM: text, ForUser: text}
}

// sanitizeProviderMessage applies the tool's Redact hook (wired to the
// process-wide sensitive-data filter at loop_wire) and truncates to 300
// chars, so a provider's raw error body can never leak secrets or flood the
// agent's context (spec 323–397).
func (t *WebSearchTool) sanitizeProviderMessage(msg string) string {
	if t.redact != nil {
		msg = t.redact(msg)
	}
	msg = strings.TrimSpace(msg)
	runes := []rune(msg)
	if len(runes) > 300 {
		return string(runes[:300])
	}
	return msg
}

// failureLine renders one "- id (role): class: msg" line with sanitization.
func (t *WebSearchTool) failureLine(id, role, class, msg string) string {
	return fmt.Sprintf("- %s (%s): %s: %s", id, role, class, t.sanitizeProviderMessage(msg))
}

// notCalledLine renders one "- id (role): not called: reason" line.
func notCalledLine(id, role, reason string) string {
	return fmt.Sprintf("- %s (%s): not called: %s", id, role, reason)
}

// hopNote renders the spec's hop note: "Note: <id> (<role>) failed and was
// not used for these results. <id>: <class>: <msg>"
func (t *WebSearchTool) hopNote(id, role, class, msg string) string {
	return fmt.Sprintf("Note: %s (%s) failed and was not used for these results. %s: %s: %s",
		id, role, id, class, t.sanitizeProviderMessage(msg))
}

// fallbackEligibility returns "" when the resolved fallback may run after a
// hop-class failure; otherwise the exact spec skip reason.
func (t *WebSearchTool) fallbackEligibility(
	entries roleEntries,
	req searchRequest,
	start time.Time,
) string {
	fb := entries.fallbackID
	if fb == "" || !entries.usable[fb] {
		return ""
	}
	if len(req.includeDomains) > 0 {
		if c, ok := t.dynamic[fb].(capabilitySearchProvider); !ok || !c.honoursSiteFilters() {
			return "cannot honour include_domains"
		}
	}
	budget := t.callBudget
	if budget <= 0 {
		budget = defaultCallBudget
	}
	if budget-time.Since(start) < providerTimeout(fb) {
		return "no time budget remaining"
	}
	return ""
}

// executeDefaultPath runs the spec's default-first ladder: the usable
// default runs first; a hop-class failure moves to an eligible resolved
// fallback; final classes (and R6/R7 shapes) end the call.
func (t *WebSearchTool) executeDefaultPath(
	ctx context.Context,
	cfg *config.WebToolsConfig,
	entries roleEntries,
	req searchRequest,
	start time.Time,
	rec *searchCallRecord,
) *ToolResult {
	defaultID := entries.defaultID
	if !entries.usable[defaultID] {
		return t.executeNotUsableDefault(ctx, cfg, entries, req, start, rec)
	}
	// Pre-flights: the first runner is the default.
	if res := t.preflightCheck(entries, defaultID, "default", req); res != nil {
		rec.refusal = true
		return res
	}
	text, spErr := t.runProvider(ctx, start, defaultID, req)
	if spErr == nil {
		rec.servedID = defaultID
		rec.role = "default"
		return successText(text, defaultID, "default", t.excludeNote(entries, defaultID, req))
	}
	// Final classes never hop (spec "Which failures hop").
	if !hopClass(spErr.class) {
		rec.class = spErr.class
		lines := []string{"search failed", t.failureLine(defaultID, "default", spErr.class, spErr.msg)}
		if entries.fallbackID != "" {
			if reason := entries.notCalledReason(cfg, entries.fallbackID); reason != "" {
				rec.skip = reason
				lines = append(lines, notCalledLine(entries.fallbackID, "fallback", reason))
			}
		}
		return ErrorResult(strings.Join(lines, "\n"))
	}
	// Hop-class failure.
	if reason := t.fallbackEligibility(entries, req, start); reason != "" {
		rec.class = spErr.class
		rec.skip = reason
		lines := []string{"search failed", t.failureLine(defaultID, "default", spErr.class, spErr.msg)}
		if entries.fallbackID != "" {
			lines = append(lines, notCalledLine(entries.fallbackID, "fallback", reason))
		}
		return ErrorResult(strings.Join(lines, "\n"))
	}
	if entries.fallbackID == "" || !entries.usable[entries.fallbackID] {
		rec.class = spErr.class
		lines := []string{"search failed", t.failureLine(defaultID, "default", spErr.class, spErr.msg)}
		if entries.fallbackID != "" {
			if reason := entries.notCalledReason(cfg, entries.fallbackID); reason != "" {
				rec.skip = reason
				lines = append(lines, notCalledLine(entries.fallbackID, "fallback", reason))
			}
		}
		return ErrorResult(strings.Join(lines, "\n"))
	}
	text2, spErr2 := t.runProvider(ctx, start, entries.fallbackID, req)
	if spErr2 == nil {
		rec.servedID = entries.fallbackID
		rec.role = "fallback"
		rec.hop = true
		notes := t.excludeNote(entries, entries.fallbackID, req)
		notes = append(notes, t.hopNote(defaultID, "default", spErr.class, spErr.msg))
		return successText(text2, entries.fallbackID, "fallback", notes)
	}
	rec.class = spErr2.class
	lines := []string{"search failed",
		t.failureLine(defaultID, "default", spErr.class, spErr.msg),
		t.failureLine(entries.fallbackID, "fallback", spErr2.class, spErr2.msg)}
	return ErrorResult(strings.Join(lines, "\n"))
}

// executeNotUsableDefault is the R6/R7 branch: the default is not usable, so
// the resolved fallback runs (R6) or nobody runs (R7). The default is always
// listed as "not called: reason" in both.
func (t *WebSearchTool) executeNotUsableDefault(
	ctx context.Context,
	cfg *config.WebToolsConfig,
	entries roleEntries,
	req searchRequest,
	start time.Time,
	rec *searchCallRecord,
) *ToolResult {
	defaultID := entries.defaultID
	reason := entries.notCalledReason(cfg, defaultID)
	fb := entries.fallbackID
	if fb == "" || !entries.usable[fb] {
		// R7: nobody can run. List the default (and any R4b fallback).
		rec.skip = reason
		lines := []string{"search failed", notCalledLine(defaultID, "default", reason)}
		if fb != "" && fb != defaultID {
			lines = append(lines, notCalledLine(fb, "fallback", entries.notCalledReason(cfg, fb)))
		}
		return ErrorResult(strings.Join(lines, "\n"))
	}
	// R6: the fallback runs; the default is named in a note or an error line.
	// Pre-flight the fallback as this path's first runner (K3): depth low on
	// GLM (or depth/site-filter asks a provider cannot honour) is refused
	// before any request fires.
	if res := t.preflightCheck(entries, fb, "fallback", req); res != nil {
		rec.refusal = true
		return res
	}
	text, spErr := t.runProvider(ctx, start, fb, req)
	if spErr == nil {
		rec.servedID = fb
		rec.role = "fallback"
		notes := t.excludeNote(entries, entries.fallbackID, req)
		notes = append(notes, fmt.Sprintf("Note: %s (default) was not called: %s.", defaultID, reason))
		return successText(text, fb, "fallback", notes)
	}
	rec.class = spErr.class
	lines := []string{"search failed",
		notCalledLine(defaultID, "default", reason),
		t.failureLine(fb, "fallback", spErr.class, spErr.msg)}
	return ErrorResult(strings.Join(lines, "\n"))
}

// executeChosen is the US-4 flow: the agent named a provider. An unusable or
// unknown pick gets an honest refusal; a usable pick runs once; a hop-class
// failure on a plain call hops, a capability use hard-fails.
func (t *WebSearchTool) executeChosen(
	ctx context.Context,
	cfg *config.WebToolsConfig,
	entries roleEntries,
	req searchRequest,
	start time.Time,
	rec *searchCallRecord,
) *ToolResult {
	id := req.namedID
	// A capability use never rides the default shortcut (US-4): include/
	// exclude, depth, or naming Perplexity itself (its prose answer is its
	// own capability) keeps the chosen contract.
	usesCap := len(req.includeDomains) > 0 || len(req.excludeDomains) > 0 ||
		req.depth != "" || id == config.SearchProviderPerplexity
	// Unknown or unusable picks are refused honestly, no silent substitution.
	if !slices.Contains(searchProviderCatalogueIDs(), id) || !entries.usable[id] {
		rec.refusal = true
		return t.chosenRefusal(entries, id)
	}
	// Naming the default equals omitting the argument entirely (US-4) --
	// plain calls only.
	if id == entries.defaultID && !usesCap {
		req.namedID = ""
		return t.executeDefaultPath(ctx, cfg, entries, req, start, rec)
	}
	// Pre-flight: the chosen provider is about to be the first runner; the
	// same capability refusals that guard the default path guard it here
	// (K3): depth on a non-depth provider, GLM low, site filters.
	if res := t.preflightCheck(entries, id, "chosen", req); res != nil {
		rec.refusal = true
		return res
	}
	// Usable pick: one named attempt.
	text, spErr := t.runProvider(ctx, start, id, req)
	if spErr == nil {
		rec.servedID = id
		rec.role = "chosen"
		return successText(text, id, "chosen", t.excludeNote(entries, id, req))
	}
	// D20: the record carries the class of the attempt that ended the call,
	// on this path exactly as on executeDefaultPath (gate round 2).
	rec.class = spErr.class
	// A capability use never hops (US-4); usesCap was computed on entry. A
	// plain named call hops only when the fallback passes the SAME
	// eligibility gate as the default path (K4): site filters and the D17a
	// time budget.
	if hopClass(spErr.class) && !usesCap {
		if reason := t.fallbackEligibility(entries, req, start); reason != "" {
			rec.skip = reason
			return ErrorResult(strings.Join([]string{
				"search failed",
				t.failureLine(id, "chosen", spErr.class, spErr.msg),
				notCalledLine(entries.fallbackID, "fallback", reason),
			}, "\n"))
		}
		if entries.usable[entries.fallbackID] {
			text2, spErr2 := t.runProvider(ctx, start, entries.fallbackID, req)
			if spErr2 == nil {
				rec.servedID = entries.fallbackID
				rec.role = "fallback"
				rec.hop = true
				return successText(text2, entries.fallbackID, "fallback",
					[]string{t.hopNote(id, "chosen", spErr.class, spErr.msg)})
			}
			rec.class = spErr2.class
			return ErrorResult(strings.Join([]string{"search failed",
				t.failureLine(id, "chosen", spErr.class, spErr.msg),
				t.failureLine(entries.fallbackID, "fallback", spErr2.class, spErr2.msg)}, "\n"))
		}
	}
	// Hard fail: capability use, no fallback, or final class.
	tail := fmt.Sprintf("This call named %s, so the fallback was not tried. "+
		"Omit provider to use the default and the fallback, or set provider to one of: %s.",
		id, strings.Join(usableOthers(entries, id), ", "))
	return ErrorResult(strings.Join([]string{
		"search failed",
		t.failureLine(id, "chosen", spErr.class, spErr.msg),
		tail}, "\n"))
}

// chosenRefusal is US-4's honest refusal for a named provider that is not
// usable now: "search failed / - <id>: not usable / Usable providers: ...".
func (t *WebSearchTool) chosenRefusal(entries roleEntries, id string) *ToolResult {
	var usableIds []string
	for _, cand := range searchProviderCatalogueIDs() {
		if entries.usable[cand] {
			usableIds = append(usableIds, cand)
		}
	}
	return ErrorResult(strings.Join([]string{
		"search failed",
		"- " + id + ": not usable",
		"Usable providers: " + strings.Join(usableIds, ", "),
	}, "\n"))
}

// usableOthers lists usable ids other than one, catalogue order.
func usableOthers(entries roleEntries, id string) []string {
	var out []string
	for _, cand := range searchProviderCatalogueIDs() {
		if cand != id && entries.usable[cand] {
			out = append(out, cand)
		}
	}
	return out
}

// excludeNote builds the D9 exclude-is-a-preference note when the provider
// that served the results cannot honour site filters: "Note: <id> does not
// support site filters; excluded domains were not applied."
func (t *WebSearchTool) excludeNote(entries roleEntries, served string, req searchRequest) []string {
	if len(req.excludeDomains) == 0 {
		return nil
	}
	if c, ok := t.dynamic[served].(capabilitySearchProvider); ok && c.honoursSiteFilters() {
		return nil
	}
	return []string{fmt.Sprintf("Note: %s does not support site filters; excluded domains were not applied.", served)}
}

// dynamicSearchDescriptionCap is D7's cap on the rendered definition
// (description + serialised Parameters) at the maximal configuration.
const dynamicSearchDescriptionCap = 2400

// parametersDynamic builds the D7 schema from the usable-set snapshot.
func (t *WebSearchTool) parametersDynamic() map[string]any {
	cfg := t.roles()
	entries := resolveRoles(cfg)
	var usableList []string
	for _, id := range searchProviderCatalogueIDs() {
		if entries.usable[id] {
			usableList = append(usableList, id)
		}
	}
	props := t.baseSearchProps()

	// Capability arguments follow the resolved default's own abilities.
	offCaps := false
	switch {
	case len(usableList) == 1:
		offCaps = t.idHonoursAny(entries, usableList[0])
	case len(usableList) >= 2:
		offCaps = entries.usable[entries.defaultID] && t.idHonoursAny(entries, entries.defaultID)
	}
	if offCaps {
		props["depth"] = t.depthArg(entries)
		props["include_domains"] = t.includeArg(entries)
		props["exclude_domains"] = t.excludeArg()
	}
	if len(usableList) >= 2 {
		props["provider"] = t.providerArg(usableList, false)
	}
	rendered := t.Description() + string(mustJSONForParams(props))
	if len(rendered) > dynamicSearchDescriptionCap {
		props = t.leanProps(entries, usableList)
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   []string{"query"},
	}
}

// baseSearchProps is the argument set every configuration offers.
func (t *WebSearchTool) baseSearchProps() map[string]any {
	return map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "Search query",
		},
		"count": map[string]any{
			"type":        "integer",
			"description": "Number of results (default 10, max 10). Out-of-range values are rejected, not clamped.",
			"minimum":     1.0,
			"maximum":     10.0,
		},
		"range": map[string]any{
			"type":        "string",
			"description": "Optional time filter: d (day), w (week), m (month), y (year)",
			"enum":        []string{"d", "w", "m", "y"},
		},
	}
}

// depthArg/includeArg/excludeArg build the capability arguments. Each
// description states the provider's own mapping (D7's good-for lines).
func (t *WebSearchTool) depthArg(entries roleEntries) map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        []string{"low", "medium", "high"},
		"description": "Result depth: low, medium or high. Tavily maps these to fast/basic/advanced; Perplexity sets search_context_size; GLM refuses low.",
	}
}

func (t *WebSearchTool) includeArg(entries roleEntries) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": "Restrict results to these domains (max 10). Refused when the provider that would run cannot honour site filters.",
	}
}

func (t *WebSearchTool) excludeArg() map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": "Domains to avoid (max 10). A preference, not a requirement: if the provider cannot honour exclusions the search proceeds and the result says so.",
	}
}

// providerArg builds the provider argument: a string enum over the usable
// ids with good-for lines in the description (D7). When lean is true the
// good-for text is dropped (overflow fallback).
func (t *WebSearchTool) providerArg(usableList []string, lean bool) map[string]any {
	desc := "Which provider to use."
	if !lean {
		lines := make([]string, 0, len(usableList))
		for _, id := range usableList {
			lines = append(lines, t.goodForLine(id))
		}
		desc += " " + strings.Join(lines, " ")
	}
	return map[string]any{
		"type":        "string",
		"enum":        usableList,
		"description": desc,
	}
}

// goodForLine is D7's per-provider good-for clause.
func (t *WebSearchTool) goodForLine(id string) string {
	var caps []string
	if c, ok := t.dynamic[id].(capabilitySearchProvider); ok {
		if c.honoursDepth() {
			caps = append(caps, "depth")
		}
		if c.honoursSiteFilters() {
			caps = append(caps, "site filters")
		}
	}
	if len(caps) == 0 {
		caps = append(caps, "quick keyless search")
	}
	return id + " - " + strings.Join(caps, " and ") + ";"
}

// idHonoursAny reports whether an id honours depth or site filters —
// the gate for offering the capability arguments (D7, round-2 rule:
// capabilities follow the resolved default).
func (t *WebSearchTool) idHonoursAny(entries roleEntries, id string) bool {
	c, ok := t.dynamic[id].(capabilitySearchProvider)
	return ok && (c.honoursDepth() || c.honoursSiteFilters())
}

// leanProps is the overflow fallback: same argument SET (the enum survives),
// minimal descriptions.
func (t *WebSearchTool) leanProps(entries roleEntries, usableList []string) map[string]any {
	props := map[string]any{
		"query": map[string]any{"type": "string", "description": "Search query"},
		"count": map[string]any{"type": "integer", "description": "Number of results (max 10)."},
		"range": map[string]any{"type": "string", "enum": []string{"d", "w", "m", "y"}},
	}
	if len(usableList) >= 2 {
		props["provider"] = t.providerArg(usableList, true)
	}
	return props
}

// mustJSONForParams marshals for size measurement; on marshal failure the
// cap check simply skips (the schema is map-based, so this never fails).
func mustJSONForParams(props map[string]any) []byte {
	b, err := json.Marshal(props)
	if err != nil {
		return nil
	}
	return b
}

// Search is the legacy 4-arg surface; the dynamic path uses SearchWithCaps.
func (p *ExaSearchProvider) Search(ctx context.Context, query string, count int, rangeCode string) (string, error) {
	return p.SearchWithCaps(ctx, searchRequest{query: query, count: count, rangeFilter: rangeCode})
}

// SearchWithCaps calls the Exa search API: POST JSON, Bearer auth,
// numResults, camelCase includeDomains/excludeDomains (US-6, spec 560–580).
func (p *ExaSearchProvider) SearchWithCaps(ctx context.Context, req searchRequest) (string, error) {
	return p.search(ctx, req)
}

func (p *ExaSearchProvider) search(ctx context.Context, req searchRequest) (string, error) {
	// K1: the key is read at call time (D4a); an empty effective key is
	// "not usable" (D16), never a hop.
	apiKey := p.currentKey()
	if apiKey == "" {
		return "", errNoAPIKey()
	}
	payload := map[string]any{
		"query":      req.query,
		"numResults": req.count,
	}
	if len(req.includeDomains) > 0 {
		payload["includeDomains"] = req.includeDomains
	}
	if len(req.excludeDomains) > 0 {
		payload["excludeDomains"] = req.excludeDomains
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := readIngestBounded(resp.Body, p.ingestBound, "Exa")
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exa api error (status %d): %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if len(parsed.Results) == 0 {
		return fmt.Sprintf("No results for: %s", req.query), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via Exa)", req.query))
	for i, item := range parsed.Results {
		if i >= req.count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.URL))
	}
	return strings.Join(lines, "\n"), nil
}

// honoursDepth: Exa does not expose a depth control (capability matrix).
func (p *ExaSearchProvider) honoursDepth() bool {
	return catalogueHonoursDepth(config.SearchProviderExa)
}

// honoursSiteFilters: Exa supports includeDomains/excludeDomains.
func (p *ExaSearchProvider) honoursSiteFilters() bool {
	return catalogueHonoursSiteFilters(config.SearchProviderExa)
}

// searchCaps is the capability-aware Tavily search: snake_case domain
// filters plus the agent's depth through the operator ceiling (D20).
func (p *TavilySearchProvider) searchCaps(ctx context.Context, req searchRequest) (string, error) {
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://api.tavily.com/search"
	}
	keys := p.currentKeys()
	if len(keys) == 0 {
		return "", errNoAPIKey()
	}
	var lastErr error
	for _, apiKey := range keys {
		depth, clamped := p.effectiveDepth(req.depth)
		payload := map[string]any{
			"api_key":        apiKey,
			"query":          req.query,
			"search_depth":   depth,
			"include_answer": false,
			"max_results":    req.count,
		}
		if timeRange := mapTavilyTimeRange(req.rangeFilter); timeRange != "" {
			payload["time_range"] = timeRange
		}
		if len(req.includeDomains) > 0 {
			payload["include_domains"] = req.includeDomains
		}
		if len(req.excludeDomains) > 0 {
			payload["exclude_domains"] = req.excludeDomains
		}
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("failed to marshal payload: %w", err)
		}
		request, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", userAgent)
		resp, err := p.client.Do(request)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		body, err := readIngestBounded(resp.Body, p.ingestBound, "Tavily")
		resp.Body.Close()
		if err != nil {
			var ibe *IngestBoundError
			if errors.As(err, &ibe) {
				return "", err
			}
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("tavily api error (status %d): %s", resp.StatusCode, string(body))
			if resp.StatusCode == http.StatusTooManyRequests ||
				resp.StatusCode == http.StatusUnauthorized ||
				resp.StatusCode == http.StatusForbidden ||
				resp.StatusCode >= 500 {
				continue
			}
			return "", lastErr
		}
		var searchResp struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &searchResp); err != nil {
			return "", fmt.Errorf("failed to parse response: %w", err)
		}
		if len(searchResp.Results) == 0 {
			return fmt.Sprintf("No results for: %s", req.query), nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("Results for: %s (via Tavily)", req.query))
		for i, item := range searchResp.Results {
			if i >= req.count {
				break
			}
			lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.URL))
			if item.Content != "" {
				lines = append(lines, fmt.Sprintf("   %s", item.Content))
			}
		}
		if clamped {
			lines = append(lines, fmt.Sprintf(
				"Note: requested depth %q was clamped to the operator ceiling %q.", req.depth, depth))
		}
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("all api keys failed, last error: %w", lastErr)
}

// effectiveContentSize maps agent depth onto GLM's content_size through the
// operator ceiling (ADR-096 D20): no agent depth means the ceiling ("" →
// "medium", the spec's migration value); an agent depth is clamped when it
// asks above the ceiling. Order medium < high; agent low is refused upstream
// at pre-flight and never reaches here.
func (p *GLMSearchProvider) effectiveContentSize(agentDepth string) (string, bool) {
	ceiling := p.contentSize
	if ceiling == "" {
		ceiling = "medium"
	}
	if agentDepth == "" {
		return ceiling, false
	}
	order := map[string]int{"low": 0, "medium": 1, "high": 2}
	if order[agentDepth] > order[ceiling] {
		return ceiling, true
	}
	return agentDepth, false
}

// honoursDepth: GLM exposes content_size (matrix), but agent depth low is
// refused upstream at pre-flight.
func (p *GLMSearchProvider) honoursDepth() bool {
	return catalogueHonoursDepth(config.SearchProviderGLM)
}

// honoursSiteFilters: GLM has no site-filter support.
func (p *GLMSearchProvider) honoursSiteFilters() bool {
	return catalogueHonoursSiteFilters(config.SearchProviderGLM)
}

// SearchWithCaps maps agent depth onto GLM's content_size (medium/high;
// low is refused at pre-flight) and mirrors the legacy request shape.
func (p *GLMSearchProvider) SearchWithCaps(ctx context.Context, req searchRequest) (string, error) {
	return p.searchCaps(ctx, req)
}

// searchCaps mirrors the legacy GLM request with content_size from the
// agent's depth (D-GLM row).
func (p *GLMSearchProvider) searchCaps(ctx context.Context, req searchRequest) (string, error) {
	// FR-015: GLM has no low content_size value. Agent depth low is refused
	// here — final class, no request fires — on every path that reaches this
	// provider, so a hop or a fallback can never route a low-depth ask to
	// GLM even where the tool-level pre-flight does not run.
	if req.depth == "low" {
		return "", &searchProviderError{class: classRejected, msg: "depth is not supported"}
	}
	// K1: the key is read at call time (D4a); an empty effective key is
	// "not usable" (D16), never a hop.
	apiKey := p.currentKey()
	if apiKey == "" {
		return "", errNoAPIKey()
	}
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://open.bigmodel.cn/api/paas/v4/web_search"
	}
	contentSize, clamped := p.effectiveContentSize(req.depth)
	payload := map[string]any{
		"search_query":  req.query,
		"search_engine": p.searchEngine,
		"search_intent": false,
		"count":         req.count,
		"content_size":  contentSize,
	}
	if recencyFilter := mapGLMRecencyFilter(req.rangeFilter); recencyFilter != "" {
		payload["search_recency_filter"] = recencyFilter
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := readIngestBounded(resp.Body, p.ingestBound, "GLM Search")
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GLM Search API error (status %d): %s", resp.StatusCode, string(body))
	}
	var searchResp struct {
		SearchResult []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			Link    string `json:"link"`
		} `json:"search_result"`
	}
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if len(searchResp.SearchResult) == 0 {
		return fmt.Sprintf("No results for: %s", req.query), nil
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via GLM Search)", req.query))
	for i, item := range searchResp.SearchResult {
		if i >= req.count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.Link))
		if item.Content != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Content))
		}
	}
	if clamped {
		lines = append(lines, fmt.Sprintf(
			"Note: requested depth %q was clamped to the operator ceiling %q.", req.depth, contentSize))
	}
	return strings.Join(lines, "\n"), nil
}

// honoursDepth: Perplexity exposes search_context_size (capability matrix).
func (p *PerplexitySearchProvider) honoursDepth() bool {
	return catalogueHonoursDepth(config.SearchProviderPerplexity)
}

// honoursSiteFilters: Perplexity supports search_domain_filter.
func (p *PerplexitySearchProvider) honoursSiteFilters() bool {
	return catalogueHonoursSiteFilters(config.SearchProviderPerplexity)
}

// SearchWithCaps runs the ADR-096 Perplexity flow: temperature 0, context
// size sent only when the operator set it or the agent passed depth, domain
// filter, and citations rendered as a Sources list.
func (p *PerplexitySearchProvider) SearchWithCaps(ctx context.Context, req searchRequest) (string, error) {
	return p.searchCaps(ctx, req)
}

// perplexityDepthToContextSize maps agent depth onto search_context_size.
func perplexityDepthToContextSize(depth string) string {
	switch depth {
	case "low", "medium", "high":
		return depth
	}
	return ""
}

// effectiveContextSize maps agent depth through the operator's
// search_context_size ceiling (ADR-096 D20). No operator value: the agent's
// depth decides and an omitted depth sends nothing (D11 — a default would
// change the bill on existing installs). A set ceiling is returned when the
// agent asks above it, with clamped=true. Order low < medium < high.
func (p *PerplexitySearchProvider) effectiveContextSize(agentDepth string) (string, bool) {
	agentSize := perplexityDepthToContextSize(agentDepth)
	if p.contextSize == "" {
		return agentSize, false
	}
	if agentSize == "" {
		return p.contextSize, false
	}
	order := map[string]int{"low": 0, "medium": 1, "high": 2}
	if order[agentSize] > order[p.contextSize] {
		return p.contextSize, true
	}
	return agentSize, false
}

// searchCaps mirrors the legacy Perplexity request with the ADR-096
// additions; the legacy Search payload stays byte-identical.
func (p *PerplexitySearchProvider) searchCaps(ctx context.Context, req searchRequest) (string, error) {
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://api.perplexity.ai/chat/completions"
	}

	keys := p.currentKeys()
	if len(keys) == 0 {
		return "", errNoAPIKey()
	}
	// D20: when the agent's depth ask exceeds the operator ceiling, the
	// result carries a clamp note naming both values.
	var clampDepth, clampCeiling string
	var lastErr error
	for _, apiKey := range keys {
		payload := map[string]any{
			"model":       "sonar",
			"temperature": 0.0,
			"messages": []map[string]string{
				{"role": "system", "content": "You are a search assistant. Provide concise search results with titles, URLs, and brief descriptions in the following format:\n1. Title\n   URL\n   Description\n\nDo not add extra commentary."},
				{"role": "user", "content": fmt.Sprintf("Search for: %s. Provide up to %d relevant results.", req.query, req.count)},
			},
			"max_tokens": 1000,
		}

		// search_context_size is sent ONLY when the operator set it or the
		// agent passed depth (D-PPLX gating); a set operator value is the
		// ceiling (D20), clamping an agent ask above it.
		contextSize, clamped := p.effectiveContextSize(req.depth)
		if clamped {
			clampDepth, clampCeiling = req.depth, contextSize
		}
		if contextSize != "" {
			payload["web_search_options"] = map[string]any{"search_context_size": contextSize}
		}

		if len(req.includeDomains) > 0 || len(req.excludeDomains) > 0 {
			domainFilter := append([]string{}, req.includeDomains...)
			for _, d := range req.excludeDomains {
				domainFilter = append(domainFilter, "-"+d)
			}
			payload["search_domain_filter"] = domainFilter
		}
		if recencyFilter := mapPerplexityRecencyFilter(req.rangeFilter); recencyFilter != "" {
			payload["search_recency_filter"] = recencyFilter
		}

		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("failed to marshal request: %w", err)
		}
		request, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+apiKey)
		request.Header.Set("User-Agent", userAgent)
		resp, err := p.client.Do(request)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		body, err := readIngestBounded(resp.Body, p.ingestBound, "Perplexity")
		resp.Body.Close()
		if err != nil {
			var ibe *IngestBoundError
			if errors.As(err, &ibe) {
				return "", err
			}
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("perplexity API error (status %d): %s", resp.StatusCode, string(body))
			if resp.StatusCode == http.StatusTooManyRequests ||
				resp.StatusCode == http.StatusUnauthorized ||
				resp.StatusCode == http.StatusForbidden ||
				resp.StatusCode >= 500 {
				continue
			}
			return "", lastErr
		}

		var searchResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Citations []string `json:"citations"`
		}

		if err := json.Unmarshal(body, &searchResp); err != nil {
			return "", fmt.Errorf("failed to parse response: %w", err)
		}
		if len(searchResp.Choices) == 0 {
			return fmt.Sprintf("No results for: %s", req.query), nil
		}
		result := fmt.Sprintf("Results for: %s (via Perplexity)\n%s", req.query, searchResp.Choices[0].Message.Content)

		// Citations render as a source list under the prose (US-PPLX).
		if len(searchResp.Citations) == 0 {
			result += "\nSources: none returned"
		} else {
			result += "\nSources:"
			for _, c := range searchResp.Citations {
				result += " " + c
			}
		}
		if clampDepth != "" {
			result += fmt.Sprintf("\nNote: requested depth %q was clamped to the operator ceiling %q.", clampDepth, clampCeiling)
		}
		return result, nil
	}
	return "", fmt.Errorf("all api keys failed, last error: %w", lastErr)
}

// honoursDepth/honoursSiteFilters: DuckDuckGo supports neither (matrix).
func (p *DuckDuckGoSearchProvider) honoursDepth() bool {
	return catalogueHonoursDepth(config.SearchProviderDuckDuckGo)
}
func (p *DuckDuckGoSearchProvider) honoursSiteFilters() bool {
	return catalogueHonoursSiteFilters(config.SearchProviderDuckDuckGo)
}

// SearchWithCaps is the ADR-096 DuckDuckGo entry: honours the per-tool
// base URL and treats non-200 as a bad_response error (the legacy path
// never checked status).
func (p *DuckDuckGoSearchProvider) SearchWithCaps(ctx context.Context, req searchRequest) (string, error) {
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://html.duckduckgo.com/html/"
	}

	searchURL += "?q=" + url.QueryEscape(req.query)
	if dateFilter := mapDuckDuckGoDateFilter(req.rangeFilter); dateFilter != "" {
		searchURL += "&df=" + url.QueryEscape(dateFilter)
	}
	request, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	request.Header.Set("User-Agent", userAgent)
	resp, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := readIngestBounded(resp.Body, p.ingestBound, "DuckDuckGo")
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", &searchProviderError{
			class: classBadResponse,
			msg:   fmt.Sprintf("duckduckgo api error (status %d): %s", resp.StatusCode, string(body)),
		}
	}
	return p.noteEmptyRun(p.extractResults(string(body), req.count, req.query))
}

// ddgEmptyWarnThreshold is how many consecutive empty DuckDuckGo results
// trigger D20's consecutive-empty warning ("the cheap version of the
// block-page fingerprint"). Lane decision: the spec fixes the warning, not
// a number.
const ddgEmptyWarnThreshold = 3

// noteEmptyRun tracks consecutive empty results: an empty result is a
// SUCCESS shape (it carries no failure class), so the warning is the only
// signal an operator gets before the agent keeps trusting a blocked page.
// Any non-empty result resets the count.
func (p *DuckDuckGoSearchProvider) noteEmptyRun(text string, err error) (string, error) {
	if err != nil {
		return text, err
	}
	if strings.HasPrefix(text, "No results found or extraction failed") {
		if n := atomic.AddInt32(&p.emptyRun, 1); n >= ddgEmptyWarnThreshold {
			logger.WarnCF("tool", "duckduckgo returned no results repeatedly", map[string]any{
				"consecutive_empty": n,
			})
		}
		return text, nil
	}
	atomic.StoreInt32(&p.emptyRun, 0)
	return text, nil
}

// honoursDepth/honoursSiteFilters: Brave supports neither (capability
// matrix) — depth or include_domains on Brave are refused at pre-flight.
func (p *BraveSearchProvider) honoursDepth() bool {
	return catalogueHonoursDepth(config.SearchProviderBrave)
}
func (p *BraveSearchProvider) honoursSiteFilters() bool {
	return catalogueHonoursSiteFilters(config.SearchProviderBrave)
}

// SearchWithCaps is the ADR-096 Brave entry: honours the per-tool base URL
// (the legacy path keeps its hardcoded endpoint).
func (p *BraveSearchProvider) SearchWithCaps(ctx context.Context, req searchRequest) (string, error) {
	base := p.baseURL
	if base == "" {
		base = "https://api.search.brave.com/res/v1/web/search"
	}
	searchURL := base + "?q=" + url.QueryEscape(req.query) + "&count=" + strconv.Itoa(req.count)
	if freshness := mapBraveFreshness(req.rangeFilter); freshness != "" {
		searchURL += "&freshness=" + url.QueryEscape(freshness)
	}

	keys := p.currentKeys()
	if len(keys) == 0 {
		return "", errNoAPIKey()
	}
	var lastErr error
	for _, apiKey := range keys {
		request, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-Subscription-Token", apiKey)
		resp, err := p.client.Do(request)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		body, err := readIngestBounded(resp.Body, p.ingestBound, "Brave")
		resp.Body.Close()

		if err != nil {
			var ibe *IngestBoundError
			if errors.As(err, &ibe) {
				return "", err
			}
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
			if resp.StatusCode == http.StatusTooManyRequests ||
				resp.StatusCode == http.StatusUnauthorized ||
				resp.StatusCode == http.StatusForbidden ||
				resp.StatusCode >= 500 {
				continue
			}
			return "", lastErr
		}

		var searchResp struct {
			Web struct {
				Results []struct {
					Title       string `json:"title"`
					URL         string `json:"url"`
					Description string `json:"description"`
				} `json:"results"`
			} `json:"web"`
		}
		if err := json.Unmarshal(body, &searchResp); err != nil {
			return "", fmt.Errorf("failed to parse response: %w", err)
		}
		results := searchResp.Web.Results
		if len(results) == 0 {
			return fmt.Sprintf("No results for: %s", req.query), nil
		}

		var lines []string
		lines = append(lines, fmt.Sprintf("Results for: %s", req.query))
		for i, item := range results {
			if i >= req.count {
				break
			}
			lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.URL))
			if item.Description != "" {
				lines = append(lines, fmt.Sprintf("   %s", item.Description))
			}
		}
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("all api keys failed, last error: %w", lastErr)
}

// honoursDepth/honoursSiteFilters: Baidu supports neither (matrix).
func (p *BaiduSearchProvider) honoursDepth() bool {
	return catalogueHonoursDepth(config.SearchProviderBaidu)
}
func (p *BaiduSearchProvider) honoursSiteFilters() bool {
	return catalogueHonoursSiteFilters(config.SearchProviderBaidu)
}

// SearchWithCaps delegates to the legacy search (base-URL aware already).
func (p *BaiduSearchProvider) SearchWithCaps(ctx context.Context, req searchRequest) (string, error) {
	// K1: the key is read at call time (D4a); an empty effective key is
	// "not usable" (D16), never a hop. The legacy Search re-reads the same
	// currentKey() when it builds its header.
	if p.currentKey() == "" {
		return "", errNoAPIKey()
	}
	return p.Search(ctx, req.query, req.count, req.rangeFilter)
}
