package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
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
	keyPool     *APIKeyPool
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
	baseURL string
	proxy   string
	client  *http.Client
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

		payload := map[string]any{
			"api_key":             apiKey,
			"query":               query,
			"search_depth":        "advanced",
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

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
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

type DuckDuckGoSearchProvider struct {
	proxy       string
	client      *http.Client
	ingestBound int64 // ADR-066 D10: ingest_bound_bytes; ≤ 0 → config default
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
	keyPool     *APIKeyPool
	proxy       string
	client      *http.Client
	ingestBound int64 // ADR-066 D10: ingest_bound_bytes; ≤ 0 → config default
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
	baseURL string
	client  *http.Client
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

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
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
	b.WriteString(fmt.Sprintf("Results for: %s (via SearXNG)\n", query))
	for i, r := range result.Results {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
		b.WriteString(fmt.Sprintf("   %s\n", r.URL))
		if r.Content != "" {
			b.WriteString(fmt.Sprintf("   %s\n", r.Content))
		}
	}

	return b.String(), nil
}

type GLMSearchProvider struct {
	apiKey       string
	baseURL      string
	searchEngine string
	proxy        string
	client       *http.Client
	ingestBound  int64 // ADR-066 D10: ingest_bound_bytes; ≤ 0 → config default
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

	payload := map[string]any{
		"search_query":  query,
		"search_engine": p.searchEngine,
		"search_intent": false,
		"count":         count,
		"content_size":  "medium",
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
	apiKey      string
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
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

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
	Proxy                 string

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
	return utils.CreateHTTPClient(proxy, timeout)
}

func NewWebSearchTool(opts WebSearchToolOptions) (*WebSearchTool, error) {
	var provider SearchProvider
	maxResults := 10
	ingestBound := effectiveIngestBound(int64(opts.IngestBoundBytes))
	// Priority: Perplexity > Brave > SearXNG > Tavily > DuckDuckGo > Baidu Search > GLM Search
	if opts.PerplexityEnabled && len(opts.PerplexityAPIKeys) > 0 {
		client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, perplexityTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client for Perplexity: %w", err)
		}
		provider = &PerplexitySearchProvider{
			keyPool:     NewAPIKeyPool(opts.PerplexityAPIKeys),
			proxy:       opts.Proxy,
			client:      client,
			ingestBound: ingestBound,
		}
		if opts.PerplexityMaxResults > 0 {
			maxResults = min(opts.PerplexityMaxResults, 10)
		}
	} else if opts.BraveEnabled && len(opts.BraveAPIKeys) > 0 {
		client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client for Brave: %w", err)
		}
		provider = &BraveSearchProvider{
			keyPool:     NewAPIKeyPool(opts.BraveAPIKeys),
			proxy:       opts.Proxy,
			client:      client,
			ingestBound: ingestBound,
		}
		if opts.BraveMaxResults > 0 {
			maxResults = min(opts.BraveMaxResults, 10)
		}
	} else if opts.SearXNGEnabled && opts.SearXNGBaseURL != "" {
		// SearXNG: when SSRFChecker is present use its safe client; otherwise
		// use a minimal stock client (SearXNG is self-hosted so no proxy needed).
		var searXNGClient *http.Client
		if opts.SSRFChecker != nil {
			searXNGClient = opts.SSRFChecker.SafeClient()
		} else {
			searXNGClient = &http.Client{Timeout: 10 * time.Second}
		}
		provider = &SearXNGSearchProvider{
			baseURL: opts.SearXNGBaseURL,
			client:  searXNGClient,
		}
		if opts.SearXNGMaxResults > 0 {
			maxResults = min(opts.SearXNGMaxResults, 10)
		}
	} else if opts.TavilyEnabled && len(opts.TavilyAPIKeys) > 0 {
		client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client for Tavily: %w", err)
		}
		provider = &TavilySearchProvider{
			keyPool: NewAPIKeyPool(opts.TavilyAPIKeys),
			baseURL: opts.TavilyBaseURL,
			proxy:   opts.Proxy,
			client:  client,
		}
		if opts.TavilyMaxResults > 0 {
			maxResults = min(opts.TavilyMaxResults, 10)
		}
	} else if opts.DuckDuckGoEnabled {
		client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client for DuckDuckGo: %w", err)
		}
		provider = &DuckDuckGoSearchProvider{proxy: opts.Proxy, client: client, ingestBound: ingestBound}
		if opts.DuckDuckGoMaxResults > 0 {
			maxResults = min(opts.DuckDuckGoMaxResults, 10)
		}
	} else if opts.BaiduSearchEnabled && opts.BaiduSearchAPIKey != "" {
		client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, perplexityTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client for Baidu Search: %w", err)
		}
		provider = &BaiduSearchProvider{
			apiKey:      opts.BaiduSearchAPIKey,
			baseURL:     opts.BaiduSearchBaseURL,
			proxy:       opts.Proxy,
			client:      client,
			ingestBound: ingestBound,
		}
		if opts.BaiduSearchMaxResults > 0 {
			maxResults = min(opts.BaiduSearchMaxResults, 10)
		}
	} else if opts.GLMSearchEnabled && opts.GLMSearchAPIKey != "" {
		client, err := makeSearchClient(opts.SSRFChecker, opts.Proxy, searchTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client for GLM Search: %w", err)
		}
		searchEngine := opts.GLMSearchEngine
		if searchEngine == "" {
			searchEngine = "search_std"
		}
		provider = &GLMSearchProvider{
			apiKey:       opts.GLMSearchAPIKey,
			baseURL:      opts.GLMSearchBaseURL,
			searchEngine: searchEngine,
			proxy:        opts.Proxy,
			client:       client,
			ingestBound:  ingestBound,
		}
		if opts.GLMSearchMaxResults > 0 {
			maxResults = min(opts.GLMSearchMaxResults, 10)
		}
	} else {
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
			return nil, fmt.Errorf("failed to create HTTP client for DuckDuckGo fallback: %w", err)
		}
		provider = &DuckDuckGoSearchProvider{proxy: opts.Proxy, client: client, ingestBound: ingestBound}
		if opts.DuckDuckGoMaxResults > 0 {
			maxResults = min(opts.DuckDuckGoMaxResults, 10)
		}
	}

	return &WebSearchTool{
		provider:   provider,
		maxResults: maxResults,
	}, nil
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
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": "Number of results (default: 10, max: 10)",
				"minimum":     1.0,
				"maximum":     10.0,
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
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return ErrorResult("query is required")
	}
	query = strings.TrimSpace(query)

	count64, err := getInt64Arg(args, "count", int64(t.maxResults))
	if err != nil {
		return ErrorResult(err.Error())
	}
	count := t.maxResults
	if count64 > 0 && count64 <= 10 {
		count = int(count64)
	}

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
	return "Fetch a URL and extract readable content (HTML to text). Use this to get weather info, news, articles, or any web content."
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

	urlStr, ok := args["url"].(string)
	if !ok {
		return ErrorResult("url is required")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid URL: %v", err))
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return ErrorResult("only http/https URLs are allowed")
	}

	if parsedURL.Host == "" {
		return ErrorResult("missing domain in URL")
	}

	// Lightweight pre-flight: block obvious localhost/literal-IP without DNS resolution.
	// The real SSRF guard is webFetchDialContext at connect time (SEC-24), which
	// re-resolves and re-checks every candidate address to close the TOCTOU
	// window a pre-flight-only check would leave open.
	hostname := parsedURL.Hostname()
	if isObviousPrivateHost(hostname, t.ssrf) {
		return ErrorResult("fetching private or local network hosts is not allowed")
	}

	maxChars := t.maxChars
	if mc, ok := args["maxChars"].(float64); ok {
		if int(mc) > 100 {
			maxChars = int(mc)
		}
	}

	doFetch := func(ua string) (*http.Response, []byte, error) {
		req, reqErr := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		if reqErr != nil {
			return nil, nil, fmt.Errorf("failed to create request: %w", reqErr)
		}
		req.Header.Set("User-Agent", ua)
		resp, doErr := t.client.Do(req)
		if doErr != nil {
			return nil, nil, fmt.Errorf("request failed: %w", doErr)
		}
		resp.Body = http.MaxBytesReader(nil, resp.Body, t.fetchLimitBytes)

		b, readErr := io.ReadAll(resp.Body)
		return resp, b, readErr
	}

	resp, body, err := doFetch(userAgent)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return ErrorResult(
				fmt.Sprintf(
					"failed to read response: size exceeded %d bytes limit",
					t.fetchLimitBytes,
				),
			)
		}
		return ErrorResult(err.Error())
	}

	// Cloudflare (and similar WAFs) signal bot challenges with 403 + cf-mitigated: challenge.
	// Retry once with an honest User-Agent that identifies omnipus, which some
	// operators explicitly allow-list for AI assistants.
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("Cf-Mitigated") == "challenge" {
		logger.DebugCF("tool", "Cloudflare challenge detected, retrying with honest User-Agent",
			map[string]any{"url": urlStr})
		resp.Body.Close()
		honestUA := fmt.Sprintf(userAgentHonest, config.Version)
		resp2, body2, err2 := doFetch(honestUA)
		if resp2 != nil && resp2.Body != nil {
			defer resp2.Body.Close()
		}

		if err2 == nil {
			resp, body = resp2, body2
		} else {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err2, &maxBytesErr) {
				return ErrorResult(
					fmt.Sprintf("failed to read response: size exceeded %d bytes limit", t.fetchLimitBytes),
				)
			}
			return ErrorResult(err2.Error())
		}
	}

	bodyStr := string(body)
	contentType := resp.Header.Get("Content-Type")

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// The most common error here is "mime: no media type" if the header is empty.
		logger.WarnCF("tool", "Failed to parse Content-Type", map[string]any{
			"raw_header": contentType,
			"error":      err.Error(),
		})

		// security fallback
		mediaType = "application/octet-stream"
	}

	var nonUTF8Charset bool
	charset, hasCharset := params["charset"]
	if hasCharset {
		// If the charset is not utf-8, we might have to convert the bodyStr
		// before passing it to the HTML/Markdown parser
		if strings.ToLower(charset) != "utf-8" {
			logger.WarnCF(
				"tool",
				"Note: the content is not in UTF-8",
				map[string]any{"charset": charset},
			)
			nonUTF8Charset = true
		}
	}

	var text, extractor string

	switch {
	case mediaType == "application/json":
		var jsonData any
		if err := json.Unmarshal(body, &jsonData); err != nil {
			text = bodyStr
			extractor = "raw"
			break
		}

		formatted, err := json.MarshalIndent(jsonData, "", "  ")
		if err != nil {
			text = bodyStr
			extractor = "raw"
			break
		}

		text = string(formatted)
		extractor = "json"

	case mediaType == "text/html" || looksLikeHTML(bodyStr):
		switch strings.ToLower(t.format) {
		case "markdown":
			var err error
			text, err = utils.HtmlToMarkdown(bodyStr)
			if err != nil {
				return ErrorResult(fmt.Sprintf("failed to HTML to markdown: %v", err))
			}
			extractor = "markdown"

		default:
			text = t.extractText(bodyStr)
			extractor = "text"
		}

	default:
		text = bodyStr
		extractor = "raw"
	}

	truncated := len(text) > maxChars
	if truncated {
		text = text[:maxChars] + "\n[Content truncated due to size limit]"
	}

	if nonUTF8Charset {
		text += "\n\n[Warning: Content charset is not UTF-8; text may contain encoding artifacts]"
	}

	result := map[string]any{
		"url":       urlStr,
		"status":    resp.StatusCode,
		"extractor": extractor,
		"truncated": truncated,
		"length":    len(text),
		"text":      text,
	}

	resultJSON, marshalErr := json.MarshalIndent(result, "", "  ")
	if marshalErr != nil {
		return ErrorResult(fmt.Sprintf("failed to format fetch result: %v", marshalErr))
	}

	return &ToolResult{
		ForLLM: string(resultJSON),
		ForUser: fmt.Sprintf(
			"Fetched %d bytes from %s (extractor: %s, truncated: %v)",
			len(text),
			urlStr,
			extractor,
			truncated,
		),
	}
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
