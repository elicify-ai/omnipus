package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// SearchConnectionProbe is one addressed, pinned-key search client. It does
// not install the live key resolver, role router, fallback or result cache.
// Construct a fresh probe for each admitted check; do not share it across calls.
type SearchConnectionProbe struct {
	id          string
	provider    capabilitySearchProvider
	observation *searchCheckTransport
}

// NewSearchConnectionProbe uses the same provider factories, endpoint options,
// proxy rules and SSRF-safe clients as normal search. The caller snapshots the
// saved key under its configuration mutation locks before constructing it.
func NewSearchConnectionProbe(id, key string, web config.WebToolsConfig, ingestBound int, ssrf *security.SSRFChecker) (*SearchConnectionProbe, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("saved search key is unavailable")
	}
	opts := WebSearchToolOptions{
		BraveAPIKeys: []string{key}, BraveBaseURL: web.Brave.BaseURL, BraveEnabled: true,
		TavilyAPIKeys: []string{key}, TavilyBaseURL: web.Tavily.BaseURL, TavilyEnabled: true,
		TavilySearchDepth: web.Tavily.SearchDepth,
		PerplexityAPIKeys: []string{key}, PerplexityBaseURL: web.Perplexity.BaseURL, PerplexityEnabled: true,
		PerplexityContextSize: web.Perplexity.SearchContextSize,
		GLMSearchAPIKey:       key, GLMSearchBaseURL: web.GLMSearch.BaseURL, GLMSearchEnabled: true,
		GLMSearchEngine: web.GLMSearch.SearchEngine, GLMContentSize: web.GLMSearch.ContentSize,
		BaiduSearchAPIKey: key, BaiduSearchBaseURL: web.BaiduSearch.BaseURL, BaiduSearchEnabled: true,
		ExaAPIKey: key, ExaBaseURL: web.Exa.BaseURL, ExaEnabled: true,
		Proxy: web.Proxy, SSRFChecker: ssrf,
	}
	bound := effectiveIngestBound(int64(ingestBound))
	provider, err := newSearchConnectionProvider(id, opts, bound)
	if err != nil {
		return nil, errors.New("could not prepare the search client")
	}
	client, endpoint := searchConnectionClient(provider)
	if client == nil {
		return nil, errors.New("search client is unavailable")
	}
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return nil, errors.New("search endpoint configuration is invalid")
		}
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	observation := &searchCheckTransport{base: transport, id: id, bound: bound}
	client.Transport = observation
	// Do not follow even an otherwise safe redirect: it would be a second
	// request, potentially forwarding an API key to a different endpoint.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &SearchConnectionProbe{id: id, provider: provider, observation: observation}, nil
}

func newSearchConnectionProvider(id string, opts WebSearchToolOptions, bound int64) (capabilitySearchProvider, error) {
	var provider SearchProvider
	var err error
	switch id {
	case config.SearchProviderBrave:
		provider, _, err = newBraveSearchProvider(opts, bound)
	case config.SearchProviderTavily:
		provider, _, err = newTavilySearchProvider(opts, bound)
	case config.SearchProviderPerplexity:
		provider, _, err = newPerplexitySearchProvider(opts, bound)
	case config.SearchProviderGLM:
		provider, _, err = newGLMSearchProvider(opts, bound)
	case config.SearchProviderBaidu:
		provider, _, err = newBaiduSearchProvider(opts, bound)
	case config.SearchProviderExa:
		provider, err = newExaProvider(opts, bound)
	default:
		return nil, errors.New("unsupported search service")
	}
	if err != nil {
		return nil, err
	}
	caps, ok := provider.(capabilitySearchProvider)
	if !ok {
		return nil, errors.New("search service has no diagnostic client")
	}
	return caps, nil
}

func searchConnectionClient(provider capabilitySearchProvider) (*http.Client, string) {
	switch p := provider.(type) {
	case *BraveSearchProvider:
		return p.client, p.baseURL
	case *TavilySearchProvider:
		return p.client, p.baseURL
	case *PerplexitySearchProvider:
		return p.client, p.baseURL
	case *GLMSearchProvider:
		return p.client, p.baseURL
	case *BaiduSearchProvider:
		return p.client, p.baseURL
	case *ExaSearchProvider:
		return p.client, p.baseURL
	}
	return nil, ""
}

// Check performs one real search and deliberately discards both results and
// free-form provider errors. Classification uses HTTP metadata and typed local
// errors, never text from a remote body or a global redaction bundle that may
// have replaced this pinned key while the request was in flight.
func (p *SearchConnectionProbe) Check(ctx context.Context) (gen.SearchProviderCheckResponse, error) {
	defer p.observation.closeIdleConnections()
	req := searchRequest{query: "Omnipus", count: 1, depth: "low", metadataOnly: true}
	if p.id == config.SearchProviderGLM {
		req.depth = "medium" // GLM does not support low.
	}
	_, err := p.provider.SearchWithCaps(ctx, req)
	status, classifyErr := p.observation.outcome(ctx, err)
	if classifyErr != nil {
		return gen.SearchProviderCheckResponse{}, classifyErr
	}
	response := gen.SearchProviderCheckResponse{
		ProviderId: gen.SearchProviderCheckResponseProviderId(p.id),
		Status:     status,
		CheckedAt:  time.Now().UTC(),
	}
	if status == gen.SearchProviderCheckResponseStatusRateLimited {
		response.RetryAfterSeconds = p.observation.retryAfter
	}
	return response, nil
}

// searchCheckTransport observes the real client's single request. It retains
// only classification metadata; response bytes are passed back to the existing
// parser and are never exposed in a diagnostic response, log or audit record.
type searchCheckTransport struct {
	base          http.RoundTripper
	id            string
	bound         int64
	attempted     bool
	status        int
	retryAfter    *int
	valid         bool
	networkFailed bool
	timedOut      bool
	oversized     bool
}

func (t *searchCheckTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.attempted {
		return nil, errors.New("search diagnostic permits only one request")
	}
	t.attempted = true
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.recordReadFailure(err)
		return nil, err
	}
	t.status = resp.StatusCode
	if t.status == http.StatusTooManyRequests {
		t.retryAfter = normalizedSearchRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	}
	body, readErr := readIngestBounded(resp.Body, t.bound, "search connection check")
	closeErr := resp.Body.Close()
	if readErr == nil {
		readErr = closeErr
	}
	if readErr != nil {
		t.recordReadFailure(readErr)
		return nil, readErr
	}
	t.valid = t.status == http.StatusOK && validSearchCheckEnvelope(t.id, body)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func (t *searchCheckTransport) closeIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t *searchCheckTransport) recordReadFailure(err error) {
	var timeout net.Error
	t.timedOut = errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout())
	var oversized *IngestBoundError
	t.oversized = errors.As(err, &oversized)
	t.networkFailed = !t.oversized
}

func (t *searchCheckTransport) outcome(ctx context.Context, providerErr error) (gen.SearchProviderCheckResponseStatus, error) {
	// The client may impose a shorter deadline than the gateway context. Its
	// Do method marks a timer-triggered transport cancellation as a timeout;
	// that typed error is only available after the provider returns it.
	var timeout net.Error
	clientTimedOut := errors.Is(providerErr, context.DeadlineExceeded) ||
		(errors.As(providerErr, &timeout) && timeout.Timeout())
	contextErr := ctx.Err()
	// A completed transport failure is a diagnostic outcome, not an internal error.
	requestFailed := t.networkFailed || contextErr != nil
	switch {
	case errors.Is(contextErr, context.DeadlineExceeded), t.timedOut, clientTimedOut:
		return gen.SearchProviderCheckResponseStatusTimeout, nil
	case requestFailed:
		return gen.SearchProviderCheckResponseStatusNetworkError, nil
	case !t.attempted:
		return "", errors.New("search client did not start a diagnostic request")
	case t.status == http.StatusUnauthorized || t.status == http.StatusForbidden:
		return gen.SearchProviderCheckResponseStatusAuthError, nil
	case t.status == http.StatusTooManyRequests:
		return gen.SearchProviderCheckResponseStatusRateLimited, nil
	case t.status != http.StatusOK:
		return gen.SearchProviderCheckResponseStatusProviderError, nil
	case t.oversized || !t.valid || providerErr != nil:
		return gen.SearchProviderCheckResponseStatusInvalidResponse, nil
	default:
		return gen.SearchProviderCheckResponseStatusSuccess, nil
	}
}

func normalizedSearchRetryAfter(value string, now time.Time) *int {
	value = strings.TrimSpace(value)
	seconds, err := strconv.Atoi(value)
	if err != nil {
		stamp, parseErr := http.ParseTime(value)
		if parseErr != nil || !stamp.After(now) {
			return nil
		}
		seconds = int((stamp.Sub(now) + time.Second - 1) / time.Second)
	}
	if seconds <= 0 {
		return nil
	}
	return &seconds
}

// Missing/null/wrong-type result containers are not a valid empty search.
// Unknown provider fields may remain: upstream APIs can extend their payloads.
func validSearchCheckEnvelope(id string, body []byte) bool {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return false
	}
	field, link := "results", "url"
	switch id {
	case config.SearchProviderBrave:
		if err := json.Unmarshal(envelope["web"], &envelope); err != nil || envelope == nil {
			return false
		}
	case config.SearchProviderGLM:
		field, link = "search_result", "link"
	case config.SearchProviderBaidu:
		field = "references"
	case config.SearchProviderPerplexity:
		field, link = "choices", "content"
	}
	array := bytes.TrimSpace(envelope[field])
	if len(array) == 0 || array[0] != '[' {
		return false
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(array, &rows); err != nil {
		return false
	}
	for _, row := range rows {
		if id == config.SearchProviderPerplexity {
			if err := json.Unmarshal(row["message"], &row); err != nil || row == nil {
				return false
			}
		}
		value := bytes.TrimSpace(row[link])
		if len(value) == 0 || value[0] != '"' {
			return false
		}
	}
	return true
}
