package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// webFetchParseArgs validates fetch_url arguments and runs the lightweight
// SSRF pre-flight. The real SSRF guard is webFetchDialContext at connect time
// (SEC-24), which re-resolves and re-checks every candidate address to close
// the TOCTOU window a pre-flight-only check would leave open.
func webFetchParseArgs(
	args map[string]any,
	defaultMaxChars int,
	ssrf *security.SSRFChecker,
) (urlStr string, maxChars int, errResult *ToolResult) {
	urlStr, ok := args["url"].(string)
	if !ok {
		return "", 0, ErrorResult("url is required")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", 0, ErrorResult(fmt.Sprintf("invalid URL: %v", err))
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", 0, ErrorResult("only http/https URLs are allowed")
	}

	if parsedURL.Host == "" {
		return "", 0, ErrorResult("missing domain in URL")
	}

	if isObviousPrivateHost(parsedURL.Hostname(), ssrf) {
		return "", 0, ErrorResult("fetching private or local network hosts is not allowed")
	}

	maxChars = defaultMaxChars
	if mc, ok := args["maxChars"].(float64); ok {
		if int(mc) >= 100 {
			maxChars = int(mc)
		}
	}
	return urlStr, maxChars, nil
}

func (t *WebFetchTool) doFetch(ctx context.Context, urlStr, ua string) (*http.Response, []byte, error) {
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
	if readErr != nil {
		return resp, b, fmt.Errorf("read response body: %w", readErr)
	}
	return resp, b, nil
}

func webFetchReadError(err error, fetchLimitBytes int64) *ToolResult {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return ErrorResult(
			fmt.Sprintf(
				"failed to read response: size exceeded %d bytes limit",
				fetchLimitBytes,
			),
		)
	}
	return ErrorResult(err.Error())
}

// fetchURL performs the GET and, if a Cloudflare-style bot challenge is
// returned, retries once with the honest omnipus User-Agent. Bodies are
// closed before return; the caller only needs status, Content-Type, and the
// already-buffered body.
func (t *WebFetchTool) fetchURL(
	ctx context.Context,
	urlStr string,
) (status int, contentType string, body []byte, errResult *ToolResult) {
	resp, body, err := t.doFetch(ctx, urlStr, userAgent)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return 0, "", nil, webFetchReadError(err, t.fetchLimitBytes)
	}

	// Cloudflare (and similar WAFs) signal bot challenges with 403 + cf-mitigated: challenge.
	// Retry once with an honest User-Agent that identifies omnipus, which some
	// operators explicitly allow-list for AI assistants.
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("Cf-Mitigated") == "challenge" {
		logger.DebugCF("tool", "Cloudflare challenge detected, retrying with honest User-Agent",
			map[string]any{"url": urlStr})
		resp.Body.Close()
		honestUA := fmt.Sprintf(userAgentHonest, config.Version)
		resp2, body2, err2 := t.doFetch(ctx, urlStr, honestUA)
		if resp2 != nil && resp2.Body != nil {
			defer resp2.Body.Close()
		}

		if err2 == nil {
			resp, body = resp2, body2
		} else {
			return 0, "", nil, webFetchReadError(err2, t.fetchLimitBytes)
		}
	}

	return resp.StatusCode, resp.Header.Get("Content-Type"), body, nil
}

func (t *WebFetchTool) decodeFetchedBody(
	body []byte,
	contentType string,
) (text, extractor string, nonUTF8Charset bool, errResult *ToolResult) {
	bodyStr := string(body)

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
			var convErr error
			text, convErr = utils.HtmlToMarkdown(bodyStr)
			if convErr != nil {
				return "", "", false, ErrorResult(fmt.Sprintf("failed to HTML to markdown: %v", convErr))
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

	return text, extractor, nonUTF8Charset, nil
}

func webFetchBuildResult(
	urlStr string,
	status int,
	text, extractor string,
	maxChars int,
	nonUTF8Charset bool,
) *ToolResult {
	truncated := len(text) > maxChars
	if truncated {
		text = text[:maxChars] + "\n[Content truncated due to size limit]"
	}

	if nonUTF8Charset {
		text += "\n\n[Warning: Content charset is not UTF-8; text may contain encoding artifacts]"
	}

	result := map[string]any{
		"url":       urlStr,
		"status":    status,
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
