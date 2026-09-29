package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

const (
	userAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	userAgentHonest = "omnipus/%s (+https://github.com/elicify-ai/omnipus; AI assistant bot)"

	// HTTP client timeouts for web tool providers.
	searchTimeout     = 10 * time.Second // Brave, Tavily, DuckDuckGo
	perplexityTimeout = 30 * time.Second // Perplexity (LLM-based, slower)
	fetchTimeout      = 60 * time.Second // WebFetchTool

	defaultMaxChars = 50000
	maxRedirects    = 5
)

// Pre-compiled regexes for HTML text extraction
var (
	reScript     = regexp.MustCompile(`<script[\s\S]*?</script>`)
	reStyle      = regexp.MustCompile(`<style[\s\S]*?</style>`)
	reTags       = regexp.MustCompile(`<[^>]+>`)
	reWhitespace = regexp.MustCompile(`[^\S\n]+`)
	reBlankLines = regexp.MustCompile(`\n{3,}`)

	// DuckDuckGo result extraction
	reDDGLink = regexp.MustCompile(
		`<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>([\s\S]*?)</a>`,
	)
	reDDGSnippet = regexp.MustCompile(`<a class="result__snippet[^"]*".*?>([\s\S]*?)</a>`)
)

type APIKeyPool struct {
	keys    []string
	current uint32
}

func NewAPIKeyPool(keys []string) *APIKeyPool {
	return &APIKeyPool{
		keys: keys,
	}
}

type APIKeyIterator struct {
	pool     *APIKeyPool
	startIdx uint32
	attempt  uint32
}

func (p *APIKeyPool) NewIterator() *APIKeyIterator {
	if len(p.keys) == 0 {
		return &APIKeyIterator{pool: p}
	}
	idx := atomic.AddUint32(&p.current, 1) - 1
	return &APIKeyIterator{
		pool:     p,
		startIdx: idx,
	}
}

func (it *APIKeyIterator) Next() (string, bool) {
	length := uint32(len(it.pool.keys))
	if length == 0 || it.attempt >= length {
		return "", false
	}
	key := it.pool.keys[(it.startIdx+it.attempt)%length]
	it.attempt++
	return key, true
}

type SearchProvider interface {
	Search(ctx context.Context, query string, count int, rangeCode string) (string, error)
}

func normalizeSearchRange(raw string) (string, error) {
	rangeCode := strings.ToLower(strings.TrimSpace(raw))
	switch rangeCode {
	case "", "d", "w", "m", "y":
		return rangeCode, nil
	default:
		return "", fmt.Errorf("range must be one of: d, w, m, y")
	}
}

func mapBraveFreshness(rangeCode string) string {
	switch rangeCode {
	case "d":
		return "pd"
	case "w":
		return "pw"
	case "m":
		return "pm"
	case "y":
		return "py"
	default:
		return ""
	}
}

func mapTavilyTimeRange(rangeCode string) string {
	switch rangeCode {
	case "d":
		return "day"
	case "w":
		return "week"
	case "m":
		return "month"
	case "y":
		return "year"
	default:
		return ""
	}
}

func mapPerplexityRecencyFilter(rangeCode string) string {
	switch rangeCode {
	case "d":
		return "day"
	case "w":
		return "week"
	case "m":
		return "month"
	case "y":
		return "year"
	default:
		return ""
	}
}

func mapDuckDuckGoDateFilter(rangeCode string) string {
	switch rangeCode {
	case "d":
		return "d"
	case "w":
		return "w"
	case "m":
		return "m"
	case "y":
		return "t"
	default:
		return ""
	}
}

func mapSearXNGTimeRange(rangeCode string) string {
	switch rangeCode {
	case "d":
		return "day"
	case "w":
		return "week"
	case "m":
		return "month"
	case "y":
		return "year"
	default:
		return ""
	}
}

func mapGLMRecencyFilter(rangeCode string) string {
	switch rangeCode {
	case "d":
		return "oneDay"
	case "w":
		return "oneWeek"
	case "m":
		return "oneMonth"
	case "y":
		return "oneYear"
	default:
		return "noLimit"
	}
}

func mapBaiduRecencyFilter(rangeCode string) string {
	switch rangeCode {
	case "d", "w":
		// Baidu does not expose a day-level filter. Use the closest supported
		// window to keep recency bias instead of silently dropping the filter.
		return "week"
	case "m":
		return "month"
	case "y":
		return "year"
	default:
		return ""
	}
}

type BraveSearchProvider struct {
	keyPool *APIKeyPool
	// keySource is the D4a live resolver handle (finding K1): when non-nil
	// (the ADR-096 dynamic path), the key set is read from it at CALL time —
	// the same live config the usability test reads. The construction
	// snapshot (keyPool) stays the fallback for the legacy path and direct
	// constructions. An empty effective key set is "not usable: no API key"
	// (D16), never a hop.
	keySource func() []string
	// rotation carries the cross-call round-robin over the effective key
	// list — the same behaviour APIKeyPool.NewIterator gave the snapshot.
	rotation    uint32
	baseURL     string // ADR-096: "" → default at search time
	proxy       string
	client      *http.Client
	ingestBound int64 // ADR-066 D10: ingest_bound_bytes; ≤ 0 → config default
}

func (p *BraveSearchProvider) Search(
	ctx context.Context,
	query string,
	count int,
	rangeCode string,
) (string, error) {
	searchURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), count)
	if freshness := mapBraveFreshness(rangeCode); freshness != "" {
		searchURL += "&freshness=" + url.QueryEscape(freshness)
	}

	var lastErr error
	iter := p.keyPool.NewIterator()

	for {
		apiKey, ok := iter.Next()
		if !ok {
			break
		}

		req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Subscription-Token", apiKey)

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		body, err := readIngestBounded(resp.Body, p.ingestBound, "Brave Search")
		resp.Body.Close()

		if err != nil {
			var ibe *IngestBoundError
			if errors.As(err, &ibe) {
				return "", err // ADR-066 D10: a bound violation is final, not retried per key
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
			// Log error body for debugging
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		results := searchResp.Web.Results
		if len(results) == 0 {
			return fmt.Sprintf("No results for: %s", query), nil
		}

		var lines []string
		lines = append(lines, fmt.Sprintf("Results for: %s", query))
		for i, item := range results {
			if i >= count {
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

type TavilySearchProvider struct {
	keyPool *APIKeyPool
	// keySource is the D4a live resolver handle (finding K1): when non-nil
	// (the ADR-096 dynamic path), the key set is read from it at CALL time —
	// the same live config the usability test reads. The construction
	// snapshot (keyPool) stays the fallback for the legacy path and direct
	// constructions. An empty effective key set is "not usable: no API key"
	// (D16), never a hop.
	keySource func() []string
	// rotation carries the cross-call round-robin over the effective key
	// list — the same behaviour APIKeyPool.NewIterator gave the snapshot.
	rotation    uint32
	baseURL     string
	proxy       string
	client      *http.Client
	ingestBound int64  // ADR-066 D10: ingest_bound_bytes; ≤ 0 → config default
	searchDepth string // ADR-096: operator-set depth ceiling; "" → "advanced"
}

func (p *TavilySearchProvider) Search(
	ctx context.Context,
	query string,
	count int,
	rangeCode string,
) (string, error) {
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://api.tavily.com/search"
	}

	var lastErr error
	iter := p.keyPool.NewIterator()

	for {
		apiKey, ok := iter.Next()
		if !ok {
			break
		}

		depth, _ := p.effectiveDepth("")
		payload := map[string]any{
			"api_key":             apiKey,
			"query":               query,
			"search_depth":        depth,
			"include_answer":      false,
			"include_images":      false,
			"include_raw_content": false,
			"max_results":         count,
		}
		if timeRange := mapTavilyTimeRange(rangeCode); timeRange != "" {
			payload["time_range"] = timeRange
		}

		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("failed to marshal payload: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", userAgent)

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		// ADR-066 D10: every network read is bounded at ingest. Tavily is
		// not named in FR-038's list, but an unbounded io.ReadAll here
		// buffers the whole response into the gateway process before any
		// parsing or capping — the D4 window cap protects the window, it
		// cannot protect the process.
		body, err := readIngestBounded(resp.Body, p.ingestBound, "Tavily")
		resp.Body.Close()

		if err != nil {
			var ibe *IngestBoundError
			if errors.As(err, &ibe) {
				return "", err // a bound violation is final, not retried per key
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

		results := searchResp.Results
		if len(results) == 0 {
			return fmt.Sprintf("No results for: %s", query), nil
		}

		var lines []string
		lines = append(lines, fmt.Sprintf("Results for: %s (via Tavily)", query))
		for i, item := range results {
			if i >= count {
				break
			}
			lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.URL))
			if item.Content != "" {
				lines = append(lines, fmt.Sprintf("   %s", item.Content))
			}
		}

		return strings.Join(lines, "\n"), nil
	}

	return "", fmt.Errorf("all api keys failed, last error: %w", lastErr)
}

// effectiveDepth maps the agent-requested depth through the operator
// ceiling: no agent depth means the operator's own depth (or "advanced");
// an agent depth is clamped when it exceeds the ceiling (D20). The order is
// ultra-fast < fast < basic < advanced.
func (p *TavilySearchProvider) effectiveDepth(agentDepth string) (string, bool) {
	ceiling := p.searchDepth
	if ceiling == "" {
		ceiling = "advanced"
	}
	if agentDepth == "" {
		return ceiling, false
	}
	order := map[string]int{"ultra-fast": 0, "fast": 1, "basic": 2, "advanced": 3}
	mapping := map[string]string{"low": "fast", "medium": "basic", "high": "advanced"}
	mapped := mapping[agentDepth]
	if order[mapped] > order[ceiling] {
		return ceiling, true
	}
	return mapped, false
}

// honoursDepth: Tavily exposes search_depth (capability matrix).
func (p *TavilySearchProvider) honoursDepth() bool {
	return catalogueHonoursDepth(config.SearchProviderTavily)
}

// honoursSiteFilters: Tavily supports include_domains/exclude_domains.
func (p *TavilySearchProvider) honoursSiteFilters() bool {
	return catalogueHonoursSiteFilters(config.SearchProviderTavily)
}

// SearchWithCaps is the ADR-096 capability-aware entry point for Tavily.
func (p *TavilySearchProvider) SearchWithCaps(ctx context.Context, req searchRequest) (string, error) {
	return p.searchCaps(ctx, req)
}

type DuckDuckGoSearchProvider struct {
	baseURL     string // ADR-096: "" → default at search time
	proxy       string
	client      *http.Client
	ingestBound int64 // ADR-066 D10: ingest_bound_bytes; ≤ 0 → config default
	// emptyRun counts consecutive empty results (D20's consecutive-empty
	// warning); reset to 0 by any non-empty result.
	emptyRun int32
}

func (p *DuckDuckGoSearchProvider) Search(
	ctx context.Context,
	query string,
	count int,
	rangeCode string,
) (string, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))
	if dateFilter := mapDuckDuckGoDateFilter(rangeCode); dateFilter != "" {
		searchURL += "&df=" + url.QueryEscape(dateFilter)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := readIngestBounded(resp.Body, p.ingestBound, "DuckDuckGo")
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return p.extractResults(string(body), count, query)
}

func (p *DuckDuckGoSearchProvider) extractResults(
	html string,
	count int,
	query string,
) (string, error) {
	// Simple regex based extraction for DDG HTML
	// Strategy: Find all result containers or key anchors directly

	// Try finding the result links directly first, as they are the most critical
	// Pattern: <a class="result__a" href="...">Title</a>
	// The previous regex was a bit strict. Let's make it more flexible for attributes order/content
	matches := reDDGLink.FindAllStringSubmatch(html, count+5)

	if len(matches) == 0 {
		return fmt.Sprintf("No results found or extraction failed. Query: %s", query), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via DuckDuckGo)", query))

	// Pre-compile snippet regex to run inside the loop
	// We'll search for snippets relative to the link position or just globally if needed
	// But simple global search for snippets might mismatch order.
	// Since we only have the raw HTML string, let's just extract snippets globally and assume order matches (risky but simple for regex)
	// Or better: Let's assume the snippet follows the link in the HTML

	// A better regex approach: iterate through text and find matches in order
	// But for now, let's grab all snippets too
	snippetMatches := reDDGSnippet.FindAllStringSubmatch(html, count+5)

	maxItems := min(len(matches), count)

	for i := range maxItems {
		urlStr := matches[i][1]
		title := stripTags(matches[i][2])
		title = strings.TrimSpace(title)

		// URL decoding if needed
		if strings.Contains(urlStr, "uddg=") {
			if u, err := url.QueryUnescape(urlStr); err == nil {
				_, after, ok := strings.Cut(u, "uddg=")
				if ok {
					urlStr = after
				}
			}
		}

		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, title, urlStr))

		// Attempt to attach snippet if available and index aligns
		if i < len(snippetMatches) {
			snippet := stripTags(snippetMatches[i][1])
			snippet = strings.TrimSpace(snippet)
			if snippet != "" {
				lines = append(lines, fmt.Sprintf("   %s", snippet))
			}
		}
	}

	return strings.Join(lines, "\n"), nil
}

func stripTags(content string) string {
	return reTags.ReplaceAllString(content, "")
}

type PerplexitySearchProvider struct {
	keyPool *APIKeyPool
	// keySource is the D4a live resolver handle (finding K1): when non-nil
	// (the ADR-096 dynamic path), the key set is read from it at CALL time —
	// the same live config the usability test reads. The construction
	// snapshot (keyPool) stays the fallback to the legacy path and direct
	// constructions. An empty effective key set is "not usable: no API key"
	// (D16), never a hop.
	keySource func() []string
	// rotation carries the cross-call round-robin over the effective key
	// list — the same behaviour APIKeyPool.NewIterator gave the snapshot.
	rotation    uint32
	baseURL     string // ADR-096: "" → default at search time
	proxy       string
	client      *http.Client
	ingestBound int64  // ADR-066 D10: ingest_bound_bytes; ≤ 0 → config default
	contextSize string // ADR-096 D20: operator ceiling clamping agent depth; "" → agent depth only
}

func (p *PerplexitySearchProvider) Search(
	ctx context.Context,
	query string,
	count int,
	rangeCode string,
) (string, error) {
	searchURL := "https://api.perplexity.ai/chat/completions"

	var lastErr error
	iter := p.keyPool.NewIterator()

	for {
		apiKey, ok := iter.Next()
		if !ok {
			break
		}

		payload := map[string]any{
			"model": "sonar",
			"messages": []map[string]string{
				{
					"role":    "system",
					"content": "You are a search assistant. Provide concise search results with titles, URLs, and brief descriptions in the following format:\n1. Title\n   URL\n   Description\n\nDo not add extra commentary.",
				},
				{
					"role": "user",
					"content": fmt.Sprintf(
						"Search for: %s. Provide up to %d relevant results.",
						query,
						count,
					),
				},
			},
			"max_tokens": 1000,
		}
		if recencyFilter := mapPerplexityRecencyFilter(rangeCode); recencyFilter != "" {
			payload["search_recency_filter"] = recencyFilter
		}

		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("failed to marshal request: %w", err)
		}

		req, err := http.NewRequestWithContext(
			ctx,
			"POST",
			searchURL,
			strings.NewReader(string(payloadBytes)),
		)
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("User-Agent", userAgent)

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		body, err := readIngestBounded(resp.Body, p.ingestBound, "Perplexity")
		resp.Body.Close()

		if err != nil {
			var ibe *IngestBoundError
			if errors.As(err, &ibe) {
				return "", err // ADR-066 D10: a bound violation is final, not retried per key
			}
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("Perplexity API error: %s", string(body))
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
		}

		if err := json.Unmarshal(body, &searchResp); err != nil {
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		if len(searchResp.Choices) == 0 {
			return fmt.Sprintf("No results for: %s", query), nil
		}

		return fmt.Sprintf(
			"Results for: %s (via Perplexity)\n%s",
			query,
			searchResp.Choices[0].Message.Content,
		), nil
	}

	return "", fmt.Errorf("all api keys failed, last error: %w", lastErr)
}

type SearXNGSearchProvider struct {
	baseURL     string
	client      *http.Client
	ingestBound int64 // ADR-066 D10 / ADR-096 D10: <= 0 means the config default
}

func (p *SearXNGSearchProvider) Search(
	ctx context.Context,
	query string,
	count int,
	rangeCode string,
) (string, error) {
	searchURL := fmt.Sprintf("%s/search?q=%s&format=json&categories=general",
		strings.TrimSuffix(p.baseURL, "/"),
		url.QueryEscape(query))
	if timeRange := mapSearXNGTimeRange(rangeCode); timeRange != "" {
		searchURL += "&time_range=" + url.QueryEscape(timeRange)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// FR-020 / ADR-096 D10: bound the body before decode. SearXNG's base
	// URL is operator-set, so an unbounded read can exhaust memory. The
	// bound error is returned unwrapped so the ladder classifies it as
	// ingest-bound and does not hop.
	body, err := readIngestBounded(resp.Body, p.ingestBound, "SearXNG")
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SearXNG returned status %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Content string  `json:"content"`
			Engine  string  `json:"engine"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Results) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}

	// Limit results to requested count
	if len(result.Results) > count {
		result.Results = result.Results[:count]
	}

	// Format results in standard Omnipus format
	var b strings.Builder
	fmt.Fprintf(&b, "Results for: %s (via SearXNG)\n", query)
	for i, r := range result.Results {
		fmt.Fprintf(&b, "%d. %s\n", i+1, r.Title)
		fmt.Fprintf(&b, "   %s\n", r.URL)
		if r.Content != "" {
			fmt.Fprintf(&b, "   %s\n", r.Content)
		}
	}

	return b.String(), nil
}

type GLMSearchProvider struct {
	apiKey string
	// keySource is the D4a live resolver handle (finding K1): when non-nil
	// (the ADR-096 dynamic path), the key is read from it at CALL time — the
	// same live config the usability test reads. The construction snapshot
	// (apiKey) stays the fallback for the legacy path and direct
	// constructions. An empty effective key is "not usable: no API key"
	// (D16), never a hop.
	keySource    func() string
	baseURL      string
	searchEngine string
	proxy        string
	client       *http.Client
	ingestBound  int64  // ADR-066 D10: ingest_bound_bytes; ≤ 0 → config default
	contentSize  string // ADR-096: operator-set content_size, D20's ceiling on agent depth; "" → "medium"
}

func (p *GLMSearchProvider) Search(
	ctx context.Context,
	query string,
	count int,
	rangeCode string,
) (string, error) {
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://open.bigmodel.cn/api/paas/v4/web_search"
	}

	// effectiveContentSize returns (value, clamped); the legacy path ignores
	// clamped — it never sends an agent depth — so the payload stays
	// byte-identical.
	contentSize, _ := p.effectiveContentSize("")
	payload := map[string]any{
		"search_query":  query,
		"search_engine": p.searchEngine,
		"search_intent": false,
		"count":         count,
		"content_size":  contentSize,
	}
	if recencyFilter := mapGLMRecencyFilter(rangeCode); recencyFilter != "" {
		payload["search_recency_filter"] = recencyFilter
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// ADR-066 D10: raised from the former 1 MiB LimitReader to ingest_bound_bytes.
	body, err := readIngestBounded(resp.Body, p.ingestBound, "GLM Search")
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
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

	results := searchResp.SearchResult
	if len(results) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via GLM Search)", query))
	for i, item := range results {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.Link))
		if item.Content != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Content))
		}
	}

	return strings.Join(lines, "\n"), nil
}

type BaiduSearchProvider struct {
	apiKey string
	// keySource is the D4a live resolver handle (finding K1): when non-nil
	// (the ADR-096 dynamic path), the key is read from it at CALL time — the
	// same live config the usability test reads. The construction snapshot
	// (apiKey) stays the fallback for the legacy path and direct
	// constructions. An empty effective key is "not usable: no API key"
	// (D16), never a hop.
	keySource   func() string
	baseURL     string
	proxy       string
	client      *http.Client
	ingestBound int64 // ADR-066 D10: ingest_bound_bytes; ≤ 0 → config default
}

func (p *BaiduSearchProvider) Search(
	ctx context.Context,
	query string,
	count int,
	rangeCode string,
) (string, error) {
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://qianfan.baidubce.com/v2/ai_search/web_search"
	}

	payload := map[string]any{
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": query,
			},
		},
		"search_source":        "baidu_search_v2",
		"resource_type_filter": []map[string]any{{"type": "web", "top_k": count}},
	}
	if recencyFilter := mapBaiduRecencyFilter(rangeCode); recencyFilter != "" {
		payload["search_recency_filter"] = recencyFilter
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// K1: currentKey() reads the live resolver on the dynamic path; on the
	// legacy path (keySource nil) it returns the construction snapshot, so
	// legacy behaviour is unchanged.
	req.Header.Set("Authorization", "Bearer "+p.currentKey())

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("baidu search request failed: %w", err)
	}
	defer resp.Body.Close()

	// ADR-066 D10: raised from the former 1 MiB LimitReader to ingest_bound_bytes.
	body, err := readIngestBounded(resp.Body, p.ingestBound, "Baidu Search")
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("baidu search API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		References []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"references"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.References) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}

	lines := []string{fmt.Sprintf("Results for: %s (via Baidu Search)", query)}
	for i, item := range result.References {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.URL))
		if item.Content != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Content))
		}
	}

	return strings.Join(lines, "\n"), nil
}

type WebSearchTool struct {
	BaseTool
	provider   SearchProvider
	maxResults int
	// roles is the ADR-096 WS-TOOL live resolver. nil = the exact legacy
	// path (schema, execution, and error text unchanged).
	roles func() *config.WebToolsConfig
	// dynamic is the ADR-096 provider map (catalogue id -> provider), built
	// only when Roles is set; the R-table ladder dispatches over it.
	dynamic map[string]SearchProvider
	// constructErr is a catalogue id whose constructor failed, so it is
	// absent from dynamic. The call reports "not usable: <cause>" and
	// does not hop (ADR-096 D16). nil when Roles is nil.
	constructErr map[string]string
	// callBudget is D17a: the whole call's wall-clock budget (0 → default).
	callBudget time.Duration
	// redact runs over every provider message that reaches the result text.
	redact func(string) string
}

type WebSearchToolOptions struct {
	// IngestBoundBytes is ADR-066 D10's ingest_bound_bytes: the maximum
	// response size any provider reads. ≤ 0 → config.DefaultIngestBoundBytes.
	IngestBoundBytes      int
	BraveAPIKeys          []string
	BraveMaxResults       int
	BraveEnabled          bool
	TavilyAPIKeys         []string
	TavilyBaseURL         string
	TavilyMaxResults      int
	TavilyEnabled         bool
	DuckDuckGoMaxResults  int
	DuckDuckGoEnabled     bool
	PerplexityAPIKeys     []string
	PerplexityMaxResults  int
	PerplexityEnabled     bool
	SearXNGBaseURL        string
	SearXNGMaxResults     int
	SearXNGEnabled        bool
	GLMSearchAPIKey       string
	GLMSearchBaseURL      string
	GLMSearchEngine       string
	GLMSearchMaxResults   int
	GLMSearchEnabled      bool
	BaiduSearchAPIKey     string
	BaiduSearchBaseURL    string
	BaiduSearchMaxResults int
	BaiduSearchEnabled    bool
	// ADR-096: Exa registers in the keyless-warning list and carries its
	// wiring in options like every keyed provider. The selection ladder
	// itself is another lane's surface — only the warning list changes here.
	ExaAPIKey    string
	ExaAPIKeyRef string
	ExaEnabled   bool

	// ADR-096 WS-TOOL: per-provider base URLs and capability defaults. The
	// base-URL fields let tests pin provider wire traffic; at production
	// wiring they are empty and the provider defaults apply.
	BraveBaseURL          string
	DuckDuckGoBaseURL     string
	PerplexityBaseURL     string
	TavilySearchDepth     string // operator tavily.search_depth; "" -> "advanced" (legacy behaviour)
	GLMContentSize        string // operator glm_search.content_size; "" -> "medium" (legacy behaviour)
	PerplexityContextSize string // operator perplexity.search_context_size; "" -> never sent
	ExaBaseURL            string // "" -> "https://api.exa.ai/search" at construction
	CallBudget            time.Duration
	Redact                func(string) string
	Roles                 func() *config.WebToolsConfig

	// Per-provider credential ref NAMES for the misconfiguration WARN — the
	// warning names the configured ref so the operator knows which vault
	// entry to check. Names only: a ref is a label, never a key value, and
	// an empty ref through the legacy path means the wiring has not supplied
	// one. On the new path (Roles non-nil) these stay empty and the live
	// resolver carries them instead.
	PerplexityAPIKeyRef  string
	BraveAPIKeyRef       string
	TavilyAPIKeyRef      string
	GLMSearchAPIKeyRef   string
	BaiduSearchAPIKeyRef string

	Proxy string

	// SSRFChecker enforces SSRF protection (SEC-24) on all outbound HTTP
	// connections made by the search provider. When non-nil, SafeClient()
	// is used instead of utils.CreateHTTPClient, blocking connections to
	// private/internal IP ranges and cloud metadata endpoints.
	SSRFChecker *security.SSRFChecker
}

// makeSearchClient returns an HTTP client for a search provider.
// When an SSRFChecker is provided, it returns an SSRF-safe client that blocks
// connections to private/internal IP ranges (SEC-24). Otherwise it falls back
// to the proxy-aware client from utils.CreateHTTPClient.
func makeSearchClient(ssrf *security.SSRFChecker, proxy string, timeout time.Duration) (*http.Client, error) {
	if ssrf != nil {
		// SafeClient() enforces SSRF protection at the dial layer (connect-time
		// re-resolution). The proxy setting is intentionally not applied on top of
		// SafeClient because proxy URLs could themselves be used to bypass SSRF;
		// operators who need a proxy with SSRF protection should configure it at
		// the OS/network level.
		return ssrf.SafeClient(), nil
	}
	client, err := utils.CreateHTTPClient(proxy, timeout)
	if err != nil {
		return nil, fmt.Errorf("makeSearchClient: %w", err)
	}
	return client, nil
}

// misconfiguredSearchProvider is one keyed search provider that is enabled in
// config but has no resolved key at tool construction — unusable as
// configured, so selection will skip it.
type misconfiguredSearchProvider struct {
	// name is the provider id ("tavily", "brave", …).
	name string
	// ref is the configured credential ref NAME (never a key value); ""
	// when the wiring did not supply one.
	ref string
}

// enabledButKeylessSearchProviders returns the keyed providers that are
// enabled but carry no key at tool construction. DuckDuckGo is never included
// (keyless by design) and SearXNG is never included (self-hosted: it has an
// optional base URL but no credential ref). Two paths (D15/FR-035): with a
// live-roles config the list derives from the provider catalogue; the legacy
// flat-options walk stays for constructions without Roles.
func enabledButKeylessSearchProviders(opts WebSearchToolOptions) []misconfiguredSearchProvider {
	if opts.Roles == nil {
		return enabledButKeylessFromFlatOpts(opts)
	}
	return enabledButKeylessFromConfig(opts.Roles)
}

// enabledButKeylessFromFlatOpts is the legacy flat walk over the per-provider
// WebSearchToolOptions fields — the path every pre-ADR-096 construction and
// test takes, unchanged.
func enabledButKeylessFromFlatOpts(opts WebSearchToolOptions) []misconfiguredSearchProvider {
	var out []misconfiguredSearchProvider
	addIfKeyless := func(enabled bool, hasKey bool, name, ref string) {
		if enabled && !hasKey {
			out = append(out, misconfiguredSearchProvider{name: name, ref: ref})
		}
	}
	addIfKeyless(opts.PerplexityEnabled, len(opts.PerplexityAPIKeys) > 0, "perplexity", opts.PerplexityAPIKeyRef)
	addIfKeyless(opts.BraveEnabled, len(opts.BraveAPIKeys) > 0, "brave", opts.BraveAPIKeyRef)
	addIfKeyless(opts.TavilyEnabled, len(opts.TavilyAPIKeys) > 0, "tavily", opts.TavilyAPIKeyRef)
	addIfKeyless(opts.GLMSearchEnabled, opts.GLMSearchAPIKey != "", "glm_search", opts.GLMSearchAPIKeyRef)
	addIfKeyless(opts.BaiduSearchEnabled, opts.BaiduSearchAPIKey != "", "baidu_search", opts.BaiduSearchAPIKeyRef)
	// ADR-096: exa joins the enabled-but-keyless warning list.
	addIfKeyless(opts.ExaEnabled, opts.ExaAPIKey != "", "exa", opts.ExaAPIKeyRef)
	return out
}

// enabledButKeylessFromConfig derives the same list from the provider
// catalogue over the LIVE config (D15/FR-035): every keyed def enabled with
// no resolved key. Names are the catalogue Section (the warning text keeps
// saying "glm_search"/"baidu_search"); the ref comes from the config's own
// api_key_ref, so the warning names the ref the operator must fill.
func enabledButKeylessFromConfig(getCfg func() *config.WebToolsConfig) []misconfiguredSearchProvider {
	cfg := getCfg()
	if cfg == nil {
		return nil
	}
	var out []misconfiguredSearchProvider
	for _, def := range config.SearchProviderCatalogue {
		if !def.Keyed {
			continue
		}
		if !def.Enabled(cfg) || def.APIKey(cfg) != "" {
			continue
		}
		ref := ""
		if def.APIKeyRef != nil {
			ref = def.APIKeyRef(cfg)
		}
		out = append(out, misconfiguredSearchProvider{name: def.Section, ref: ref})
	}
	return out
}

// newPerplexitySearchProvider builds the Perplexity search provider from
// opts. It returns the provider and the configured maxResults override
// (0 = leave maxResults at the default).
func newPerplexitySearchProvider(opts WebSearchToolOptions, ingestBound int64) (SearchProvider, int, error) {
	client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, perplexityTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create HTTP client for Perplexity: %w", err)
	}
	provider := &PerplexitySearchProvider{
		keyPool:     NewAPIKeyPool(opts.PerplexityAPIKeys),
		baseURL:     opts.PerplexityBaseURL,
		contextSize: opts.PerplexityContextSize,
		proxy:       opts.Proxy,
		client:      client,
		ingestBound: ingestBound,
	}
	// K1: on the dynamic path the key set is read at call time through the
	// same live config the usability test reads (D4a / AC-16). The snapshot
	// pool stays the fallback for the legacy path and direct constructions.
	if opts.Roles != nil {
		roles := opts.Roles
		provider.keySource = func() []string {
			c := roles()
			if c == nil {
				return nil
			}
			return singleKeyList(c.Perplexity.APIKey())
		}
	}
	if opts.PerplexityMaxResults > 0 {
		return provider, min(opts.PerplexityMaxResults, 10), nil
	}
	return provider, 0, nil
}

// newBraveSearchProvider builds the Brave search provider from opts. It
// returns the provider and the configured maxResults override (0 = leave
// maxResults at the default).
func newBraveSearchProvider(opts WebSearchToolOptions, ingestBound int64) (SearchProvider, int, error) {
	client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create HTTP client for Brave: %w", err)
	}
	provider := &BraveSearchProvider{
		keyPool:     NewAPIKeyPool(opts.BraveAPIKeys),
		baseURL:     opts.BraveBaseURL,
		proxy:       opts.Proxy,
		client:      client,
		ingestBound: ingestBound,
	}
	// K1: on the dynamic path the key set is read at call time through the
	// same live config the usability test proves (D4a / AC-16). The snapshot
	// pool stays the fallback for the legacy path and direct constructions.
	if opts.Roles != nil {
		roles := opts.Roles
		keySource := func() []string {
			c := roles()
			if c == nil {
				return nil
			}
			return singleKeyList(c.Brave.APIKey())
		}
		provider.keySource = keySource
	}
	if opts.BraveMaxResults > 0 {
		return provider, min(opts.BraveMaxResults, 10), nil
	}
	return provider, 0, nil
}

// newSearXNGSearchProvider builds the SearXNG search provider from opts.
// The client comes from makeSearchClient, like every other provider
// (ADR-096 D10 / AC-9): SSRF-safe when a checker is set, proxy-aware
// otherwise. The response body is capped at ingestBound.
func newSearXNGSearchProvider(opts WebSearchToolOptions, ingestBound int64) (SearchProvider, int, error) {
	client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create HTTP client for SearXNG: %w", err)
	}
	provider := &SearXNGSearchProvider{
		baseURL:     opts.SearXNGBaseURL,
		client:      client,
		ingestBound: ingestBound,
	}
	if opts.SearXNGMaxResults > 0 {
		return provider, min(opts.SearXNGMaxResults, 10), nil
	}
	return provider, 0, nil
}

// newTavilySearchProvider builds the Tavily search provider from opts. It
// returns the provider and the configured maxResults override (0 = leave
// maxResults at the default).
func newTavilySearchProvider(opts WebSearchToolOptions, ingestBound int64) (SearchProvider, int, error) {
	client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create HTTP client for Tavily: %w", err)
	}
	provider := &TavilySearchProvider{
		keyPool:     NewAPIKeyPool(opts.TavilyAPIKeys),
		baseURL:     opts.TavilyBaseURL,
		proxy:       opts.Proxy,
		searchDepth: opts.TavilySearchDepth,
		client:      client,
		ingestBound: ingestBound,
	}
	// K1: on the dynamic path the key set is read at call time through the
	// same live config the usability test reads (D4a / AC-16). The snapshot
	// pool stays the fallback for the legacy path and direct constructions.
	if opts.Roles != nil {
		roles := opts.Roles
		provider.keySource = func() []string {
			c := roles()
			if c == nil {
				return nil
			}
			return singleKeyList(c.Tavily.APIKey())
		}
	}
	if opts.TavilyMaxResults > 0 {
		return provider, min(opts.TavilyMaxResults, 10), nil
	}
	return provider, 0, nil
}

// newDuckDuckGoSearchProvider builds the DuckDuckGo search provider from
// opts. It returns the provider and the configured maxResults override
// (0 = leave maxResults at the default).
func newDuckDuckGoSearchProvider(opts WebSearchToolOptions, ingestBound int64) (SearchProvider, int, error) {
	client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create HTTP client for DuckDuckGo: %w", err)
	}
	provider := &DuckDuckGoSearchProvider{baseURL: opts.DuckDuckGoBaseURL, proxy: opts.Proxy, client: client, ingestBound: ingestBound}
	if opts.DuckDuckGoMaxResults > 0 {
		return provider, min(opts.DuckDuckGoMaxResults, 10), nil
	}
	return provider, 0, nil
}

// newBaiduSearchProvider builds the Baidu Search provider from opts. It
// returns the provider and the configured maxResults override (0 = leave
// maxResults at the default).
func newBaiduSearchProvider(opts WebSearchToolOptions, ingestBound int64) (SearchProvider, int, error) {
	client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, perplexityTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create HTTP client for Baidu Search: %w", err)
	}
	provider := &BaiduSearchProvider{
		apiKey:      opts.BaiduSearchAPIKey,
		baseURL:     opts.BaiduSearchBaseURL,
		proxy:       opts.Proxy,
		client:      client,
		ingestBound: ingestBound,
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
			return c.BaiduSearch.APIKey()
		}
	}
	if opts.BaiduSearchMaxResults > 0 {
		return provider, min(opts.BaiduSearchMaxResults, 10), nil
	}
	return provider, 0, nil
}

// newGLMSearchProvider builds the GLM Search provider from opts. It returns
// the provider and the configured maxResults override (0 = leave maxResults
// at the default).
func newGLMSearchProvider(opts WebSearchToolOptions, ingestBound int64) (SearchProvider, int, error) {
	client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create HTTP client for GLM Search: %w", err)
	}
	searchEngine := opts.GLMSearchEngine
	if searchEngine == "" {
		searchEngine = "search_std"
	}
	provider := &GLMSearchProvider{
		apiKey:       opts.GLMSearchAPIKey,
		baseURL:      opts.GLMSearchBaseURL,
		searchEngine: searchEngine,
		proxy:        opts.Proxy,
		client:       client,
		ingestBound:  ingestBound,
		contentSize:  opts.GLMContentSize,
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
			return c.GLMSearch.APIKey()
		}
	}
	if opts.GLMSearchMaxResults > 0 {
		return provider, min(opts.GLMSearchMaxResults, 10), nil
	}
	return provider, 0, nil
}

// newDuckDuckGoFallbackSearchProvider builds the keyless DuckDuckGo provider
// that NewWebSearchTool falls back to when no selection branch matched, and
// carries the fallback branch's WARN/INFO distinction verbatim.
func newDuckDuckGoFallbackSearchProvider(opts WebSearchToolOptions, ingestBound int64) (SearchProvider, int, error) {
	// No keyed or explicitly-enabled provider was selected — fall back to
	// DuckDuckGo, the built-in keyless provider. DuckDuckGo is the default
	// whenever no other provider is available: it needs no API key, so web
	// search must never be unavailable for lack of one. This guarantees
	// search_web always registers and works — including for a config that
	// never wrote a tools.web section (a minimal or v0->v1-migrated config,
	// where DuckDuckGoEnabled defaults to false) — instead of silently
	// dropping the tool and leaving research agents (Ray) with no search.
	//
	// Distinguish two cases so operators can spot misconfiguration:
	//   • "enabled but unusable" (a provider was switched on but lost its key)
	//     → WARN, because this almost certainly means a config migration issue.
	//   • "nothing configured" (fresh/minimal config, no provider section at all)
	//     → INFO, because DuckDuckGo-as-default is the expected initial state.
	anyEnabled := opts.PerplexityEnabled || opts.BraveEnabled || opts.SearXNGEnabled ||
		opts.TavilyEnabled || opts.DuckDuckGoEnabled || opts.BaiduSearchEnabled || opts.GLMSearchEnabled
	if anyEnabled {
		logger.WarnCF("tool", "no search provider configured; defaulting to keyless DuckDuckGo",
			map[string]any{
				"hint": "a provider was enabled but its key or base URL is missing — check tools.web config",
			})
	} else {
		logger.InfoCF("tool", "no search provider configured; defaulting to keyless DuckDuckGo",
			map[string]any{
				"hint": "set tools.web in config to use a keyed provider",
			})
	}
	client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create HTTP client for DuckDuckGo fallback: %w", err)
	}
	provider := &DuckDuckGoSearchProvider{baseURL: opts.DuckDuckGoBaseURL, proxy: opts.Proxy, client: client, ingestBound: ingestBound}
	if opts.DuckDuckGoMaxResults > 0 {
		return provider, min(opts.DuckDuckGoMaxResults, 10), nil
	}
	return provider, 0, nil
}

// warnKeylessSearchProviders logs the misconfiguration WARN: a keyed
// provider that is enabled but has no resolved key. Previously this WARN
// lived only in the final-fallback branch, so it could not fire while
// tools.web.duckduckgo.enabled ships true — a fully configured Tavily
// silently degraded to DuckDuckGo for two months with zero WARNs in the
// log. It fires BEFORE selection, regardless of which branch wins, naming
// the affected providers and their configured credential refs (names only,
// never key values).
func warnKeylessSearchProviders(opts WebSearchToolOptions) {
	if misconfigured := enabledButKeylessSearchProviders(opts); len(misconfigured) > 0 {
		names := make([]string, 0, len(misconfigured))
		refs := make([]string, 0, len(misconfigured))
		for _, m := range misconfigured {
			names = append(names, m.name)
			if m.ref != "" {
				refs = append(refs, m.name+"="+m.ref)
			}
		}
		logger.WarnCF("tool", "search provider enabled but no resolved key; selection will skip it",
			map[string]any{
				"providers": strings.Join(names, ","),
				"refs":      strings.Join(refs, ","),
				"hint":      "the credential ref is configured but its key did not reach the process environment — check the vault entry and boot credential-injection logs",
			})
	}
}

func NewWebSearchTool(opts WebSearchToolOptions) (*WebSearchTool, error) {
	var provider SearchProvider
	maxResults := 10
	ingestBound := effectiveIngestBound(int64(opts.IngestBoundBytes))
	roles := opts.Roles

	// A keyed provider that is enabled but has no resolved key is a
	// misconfiguration the operator must see. Previously this WARN lived only
	// in the final-fallback branch, so it could not fire while
	// tools.web.duckduckgo.enabled ships true — a fully configured Tavily
	// silently degraded to DuckDuckGo for two months with zero WARNs in the
	// log. It now fires BEFORE selection, regardless of which branch wins,
	// naming the affected providers and their configured credential refs
	// (names only, never key values).
	warnKeylessSearchProviders(opts)

	// Priority: Perplexity > Brave > SearXNG > Tavily > DuckDuckGo > Baidu Search > GLM Search.
	// A constructor error fails the tool only on the legacy path (Roles nil),
	// where this one provider is the whole tool. With Roles set, the dynamic
	// map records the cause and the call reports "not usable" — boot continues.
	assign := func(prov SearchProvider, override int, err error) error {
		if err != nil {
			if roles != nil {
				return nil
			}
			return err
		}
		provider = prov
		if override > 0 {
			maxResults = override
		}
		return nil
	}
	if opts.PerplexityEnabled && len(opts.PerplexityAPIKeys) > 0 {
		if err := assign(newPerplexitySearchProvider(opts, ingestBound)); err != nil {
			return nil, err
		}
	} else if opts.BraveEnabled && len(opts.BraveAPIKeys) > 0 {
		if err := assign(newBraveSearchProvider(opts, ingestBound)); err != nil {
			return nil, err
		}
	} else if opts.SearXNGEnabled && opts.SearXNGBaseURL != "" {
		if err := assign(newSearXNGSearchProvider(opts, ingestBound)); err != nil {
			return nil, err
		}
	} else if opts.TavilyEnabled && len(opts.TavilyAPIKeys) > 0 {
		if err := assign(newTavilySearchProvider(opts, ingestBound)); err != nil {
			return nil, err
		}
	} else if opts.DuckDuckGoEnabled {
		if err := assign(newDuckDuckGoSearchProvider(opts, ingestBound)); err != nil {
			return nil, err
		}
	} else if opts.BaiduSearchEnabled && opts.BaiduSearchAPIKey != "" {
		if err := assign(newBaiduSearchProvider(opts, ingestBound)); err != nil {
			return nil, err
		}
	} else if opts.GLMSearchEnabled && opts.GLMSearchAPIKey != "" {
		if err := assign(newGLMSearchProvider(opts, ingestBound)); err != nil {
			return nil, err
		}
	} else {
		if err := assign(newDuckDuckGoFallbackSearchProvider(opts, ingestBound)); err != nil {
			return nil, err
		}
	}

	// ADR-096: with Roles set, build the FULL provider map so the R-table
	// ladder can fail over across providers; the legacy single-provider
	// chain above stays the path when Roles is nil.
	var dynamic map[string]SearchProvider
	var constructErr map[string]string
	if roles != nil {
		dynamic, constructErr = buildDynamicSearchProviders(opts, ingestBound)
	}

	return &WebSearchTool{
		provider:     provider,
		maxResults:   maxResults,
		roles:        roles,
		dynamic:      dynamic,
		constructErr: constructErr,
		callBudget:   opts.CallBudget,
		redact:       opts.Redact,
	}, nil
}

// buildDynamicSearchProviders constructs the whole ADR-096 provider map.
// A constructor error is not swallowed: it is logged with the provider id
// and the cause, and returned so the call can report "not usable: <cause>"
// urlUserinfoRe matches the "user:password@" part of any URL quoted in an
// error. Go's url.Parse error quotes the whole input, so a malformed
// credentialed proxy URL would otherwise surface its password.
var urlUserinfoRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s"]+@`)

// redactURLUserinfo hides URL credentials in a message that is logged and
// shown to the agent (gate round 2, security NEW-1). The cause stays readable.
func redactURLUserinfo(msg string) string {
	return urlUserinfoRe.ReplaceAllString(msg, "${1}***@")
}

// instead of a network hop (ADR-096 D16).
func buildDynamicSearchProviders(opts WebSearchToolOptions, ingestBound int64) (map[string]SearchProvider, map[string]string) {
	dynamic := map[string]SearchProvider{}
	failed := map[string]string{}
	note := func(id string, err error) {
		if err == nil {
			return
		}
		logger.WarnCF("tool", "search provider not usable: constructor failed", map[string]any{
			"provider": id,
			"cause":    redactURLUserinfo(err.Error()),
		})
		failed[id] = redactURLUserinfo(err.Error())
	}
	if p, _, err := newPerplexitySearchProvider(opts, ingestBound); err != nil {
		note(config.SearchProviderPerplexity, err)
	} else {
		dynamic[config.SearchProviderPerplexity] = p
	}
	if p, _, err := newBraveSearchProvider(opts, ingestBound); err != nil {
		note(config.SearchProviderBrave, err)
	} else {
		dynamic[config.SearchProviderBrave] = p
	}
	if p, _, err := newSearXNGSearchProvider(opts, ingestBound); err != nil {
		note(config.SearchProviderSearXNG, err)
	} else {
		dynamic[config.SearchProviderSearXNG] = p
	}
	if p, _, err := newTavilySearchProvider(opts, ingestBound); err != nil {
		note(config.SearchProviderTavily, err)
	} else {
		dynamic[config.SearchProviderTavily] = p
	}
	if p, _, err := newDuckDuckGoSearchProvider(opts, ingestBound); err != nil {
		note(config.SearchProviderDuckDuckGo, err)
	} else {
		dynamic[config.SearchProviderDuckDuckGo] = p
	}
	if p, _, err := newBaiduSearchProvider(opts, ingestBound); err != nil {
		note(config.SearchProviderBaidu, err)
	} else {
		dynamic[config.SearchProviderBaidu] = p
	}
	if p, _, err := newGLMSearchProvider(opts, ingestBound); err != nil {
		note(config.SearchProviderGLM, err)
	} else {
		dynamic[config.SearchProviderGLM] = p
	}
	if p, err := newExaProvider(opts, ingestBound); err != nil {
		note(config.SearchProviderExa, err)
	} else {
		dynamic[config.SearchProviderExa] = p
	}
	return dynamic, failed
}

func (t *WebSearchTool) Name() string {
	return "search_web"
}

func (t *WebSearchTool) Description() string {
	return "Search the web for current information. Supports query, count, and an optional temporal range filter. Returns titles, URLs, and snippets from search results."
}

func (t *WebSearchTool) Scope() ToolScope       { return ScopeGeneral }
func (t *WebSearchTool) Category() ToolCategory { return CategoryWeb }

func (t *WebSearchTool) Parameters() map[string]any {
	if t.roles != nil {
		return t.parametersDynamic()
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Number of results (default: %d, max: 10). Out-of-range values are rejected, not clamped.",
					t.maxResults,
				),
				"minimum": 1.0,
				"maximum": 10.0,
			},
			"range": map[string]any{
				"type":        "string",
				"description": "Optional time filter: d (day), w (week), m (month), y (year)",
				"enum":        []string{"d", "w", "m", "y"},
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.roles != nil {
		return t.executeDynamic(ctx, args)
	}
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return ErrorResult("query is required")
	}
	query = strings.TrimSpace(query)

	count64, err := getInt64Arg(args, "count", int64(t.maxResults))
	if err != nil {
		return ErrorResult(err.Error())
	}
	// House style (see shell.go's resolveTimeoutSeconds): an out-of-range
	// value is REJECTED, never silently clamped or dropped to the default.
	if count64 < 1 || count64 > 10 {
		return ErrorResult(fmt.Sprintf("count must be between 1 and 10 (got %d)", count64))
	}
	count := int(count64)

	rangeCode, err := normalizeSearchRange("")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if rawRange, exists := args["range"]; exists {
		rangeStr, ok := rawRange.(string)
		if !ok {
			return ErrorResult("range must be a string")
		}
		rangeCode, err = normalizeSearchRange(rangeStr)
		if err != nil {
			return ErrorResult(err.Error())
		}
	}

	result, err := t.provider.Search(ctx, query, count, rangeCode)
	if err != nil {
		return ErrorResult(fmt.Sprintf("search failed: %v", err))
	}

	return &ToolResult{
		ForLLM:  result,
		ForUser: result,
	}
}

type WebFetchTool struct {
	BaseTool
	maxChars        int
	proxy           string
	client          *http.Client
	format          string
	fetchLimitBytes int64
	// ssrf enforces SSRF protection (SEC-24) on every fetch_url request via
	// the shared, tested security.SSRFChecker — never nil. Unlike
	// WebSearchTool (which only gets SSRF protection when the operator opts
	// into sandbox.ssrf.enabled — see WebSearchToolOptions.SSRFChecker),
	// WebFetchTool has always enforced its own private-IP/cloud-metadata
	// blocking unconditionally: the tool exists specifically to fetch
	// arbitrary, LLM-supplied URLs, so this guard must never be optional.
	// This checker is constructed fresh in every NewWebFetchToolWithConfig
	// call from cfg.Tools.Web.PrivateHostWhitelist — it is intentionally NOT
	// threaded through from a shared/optional caller-supplied checker the
	// way WebSearchTool's is, to avoid silently losing protection on
	// installs where sandbox.ssrf.enabled defaults to false.
	ssrf *security.SSRFChecker
}

func NewWebFetchTool(maxChars int, format string, fetchLimitBytes int64) (*WebFetchTool, error) {
	// createHTTPClient cannot fail with an empty proxy string.
	return NewWebFetchToolWithConfig(maxChars, "", format, fetchLimitBytes, nil)
}

// allowPrivateWebFetchHosts controls whether loopback/private hosts are allowed.
// This is false in normal runtime to reduce SSRF exposure, and tests can override it temporarily.
var allowPrivateWebFetchHosts atomic.Bool

func NewWebFetchToolWithProxy(
	maxChars int,
	proxy string,
	format string,
	fetchLimitBytes int64,
	privateHostWhitelist []string,
) (*WebFetchTool, error) {
	return NewWebFetchToolWithConfig(maxChars, proxy, format, fetchLimitBytes, privateHostWhitelist)
}

func NewWebFetchToolWithConfig(
	maxChars int,
	proxy string,
	format string,
	fetchLimitBytes int64,
	privateHostWhitelist []string,
) (*WebFetchTool, error) {
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	if err := validateWebFetchWhitelist(privateHostWhitelist); err != nil {
		return nil, fmt.Errorf("failed to parse web fetch private host whitelist: %w", err)
	}
	// SEC-24: WebFetchTool always builds its own SSRFChecker instance — see
	// the doc comment on WebFetchTool.ssrf for why this is unconditional
	// rather than threaded through from an optional, operator-toggled
	// checker like WebSearchTool's.
	ssrf := security.NewSSRFChecker(privateHostWhitelist)

	client, err := utils.CreateHTTPClient(proxy, fetchTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client for web fetch: %w", err)
	}
	if transport, ok := client.Transport.(*http.Transport); ok {
		dialer := &net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		transport.DialContext = webFetchDialContext(dialer, ssrf)
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		if isObviousPrivateHost(req.URL.Hostname(), ssrf) {
			return fmt.Errorf("redirect target is private or local network host")
		}
		return nil
	}
	if fetchLimitBytes <= 0 {
		// ADR-066 D10 (FR-038): the fetch_url fallback is the ingest bound
		// default (8,000,000 bytes), down from 10 MiB.
		fetchLimitBytes = int64(config.DefaultIngestBoundBytes)
	}
	return &WebFetchTool{
		maxChars:        maxChars,
		proxy:           proxy,
		client:          client,
		format:          format,
		fetchLimitBytes: fetchLimitBytes,
		ssrf:            ssrf,
	}, nil
}

func (t *WebFetchTool) Name() string {
	return "fetch_url"
}

func (t *WebFetchTool) Description() string {
	return "Fetch a URL and extract readable content (HTML to text). Use this to get weather info, news, articles, or any web content. " +
		"Only http/https. Private, loopback and link-local addresses are refused — you cannot fetch a local dev server or preview URL " +
		"with this tool; use the browser tools instead. Content is truncated to maxChars."
}

func (t *WebFetchTool) Scope() ToolScope       { return ScopeGeneral }
func (t *WebFetchTool) Category() ToolCategory { return CategoryWeb }

func (t *WebFetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "URL to fetch",
			},
			"maxChars": map[string]any{
				"type":        "integer",
				"description": "Maximum characters to extract",
				"minimum":     100.0,
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.ssrf == nil {
		return ErrorResult("internal error: SSRF checker not initialized")
	}

	urlStr, maxChars, note, errResult := webFetchParseArgs(args, t.maxChars, t.ssrf)
	if errResult != nil {
		return errResult
	}

	status, finalURL, contentType, body, errResult := t.fetchURL(ctx, urlStr)
	if errResult != nil {
		return errResult
	}

	text, extractor, nonUTF8Charset, errResult := t.decodeFetchedBody(body, contentType)
	if errResult != nil {
		return errResult
	}

	return webFetchBuildResult(finalURL, status, text, extractor, maxChars, note, nonUTF8Charset)
}

func looksLikeHTML(body string) bool {
	if body == "" {
		return false
	}

	lower := strings.ToLower(body)

	return strings.HasPrefix(body, "<!doctype") ||
		strings.HasPrefix(lower, "<html")
}

func (t *WebFetchTool) extractText(htmlContent string) string {
	result := reScript.ReplaceAllLiteralString(htmlContent, "")
	result = reStyle.ReplaceAllLiteralString(result, "")
	result = reTags.ReplaceAllLiteralString(result, "")

	result = strings.TrimSpace(result)

	result = reWhitespace.ReplaceAllString(result, " ")
	result = reBlankLines.ReplaceAllString(result, "\n\n")

	lines := strings.Split(result, "\n")
	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	return strings.Join(cleanLines, "\n")
}

// webFetchDialContext re-resolves DNS at connect time to mitigate DNS rebinding
// (TOCTOU) where a hostname resolves to a public IP during pre-flight but a
// private IP at connect time. All SSRF enforcement (IP-range/CIDR classification,
// cloud-metadata blocking, 6to4/Teredo unwrapping, allowlist handling — SEC-24)
// is delegated to the shared, independently-tested security.SSRFChecker via
// SafeDialContext rather than duplicated here. The only thing layered on top is
// allowPrivateWebFetchHosts, a test-only escape hatch (see its doc comment)
// that is specific to this tool's test suite and not a concept SSRFChecker
// itself needs to know about.
func webFetchDialContext(
	dialer *net.Dialer,
	ssrf *security.SSRFChecker,
) func(context.Context, string, string) (net.Conn, error) {
	safeDial := ssrf.SafeDialContext(dialer)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if allowPrivateWebFetchHosts.Load() {
			return dialer.DialContext(ctx, network, address)
		}
		return safeDial(ctx, network, address)
	}
}

// validateWebFetchWhitelist enforces that WebFetchTool's private-host whitelist
// entries (cfg.Tools.Web.PrivateHostWhitelist) are IP addresses or CIDR ranges
// only. This is intentionally stricter than security.SSRFChecker's
// allowInternal parameter, which also accepts bare hostnames (see
// NewSSRFChecker's doc comment) — WebFetchTool's whitelist has always been
// IP/CIDR-only, and silently accepting a mistyped entry as a "hostname
// allow-rule" would fail in a confusing way (looks like validation passed, but
// the allow-rule almost certainly never matches any real target). Rejecting
// malformed entries at construction time keeps the fail-closed guarantee: a
// bad config value is a startup error, not a silently inert no-op.
func validateWebFetchWhitelist(entries []string) error {
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if net.ParseIP(entry) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(entry); err == nil {
			continue
		}
		return fmt.Errorf("invalid entry %q: expected IP or CIDR", entry)
	}
	return nil
}

// isObviousPrivateHost performs a lightweight, no-DNS check for obviously private
// hosts. It catches localhost, literal private IPs, and empty hosts. It does NOT
// resolve DNS — the real SSRF guard is webFetchDialContext, which checks
// resolved IPs at connect time. IP-range classification (RFC 1918, loopback,
// link-local, cloud metadata, multicast, 6to4/Teredo unwrapping, etc.) is
// delegated to the shared security.SSRFChecker.CheckIP (SEC-24) instead of
// being duplicated here.
func isObviousPrivateHost(host string, ssrf *security.SSRFChecker) bool {
	if allowPrivateWebFetchHosts.Load() {
		return false
	}

	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return true
	}

	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}

	if ip := net.ParseIP(h); ip != nil {
		return ssrf.CheckIP(ip) != nil
	}

	return false
}
