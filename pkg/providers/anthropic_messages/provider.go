// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package anthropicmessages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

type (
	ToolCall               = protocoltypes.ToolCall
	FunctionCall           = protocoltypes.FunctionCall
	LLMResponse            = protocoltypes.LLMResponse
	UsageInfo              = protocoltypes.UsageInfo
	Message                = protocoltypes.Message
	ToolDefinition         = protocoltypes.ToolDefinition
	ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition
)

const (
	defaultAPIVersion     = "2023-06-01"
	defaultBaseURL        = "https://api.anthropic.com/v1"
	defaultRequestTimeout = common.DefaultRequestTimeout
)

// Provider implements Anthropic Messages API via HTTP (without SDK).
// It supports custom endpoints that use Anthropic's native message format.
type Provider struct {
	apiKey     string
	apiBase    string
	httpClient *http.Client
}

// NewProvider creates a new Anthropic Messages API provider.
func NewProvider(apiKey, apiBase string) *Provider {
	return NewProviderWithTimeout(apiKey, apiBase, 0)
}

// NewProviderWithTimeout creates a provider with custom request timeout.
func NewProviderWithTimeout(apiKey, apiBase string, timeoutSeconds int) *Provider {
	baseURL := normalizeBaseURL(apiBase)

	// common.NewHTTPClient configures HTTP/2 health-check pings (ReadIdleTimeout)
	// and a shortened idle-connection window so a pooled connection the server
	// has since GOAWAY'd/closed is detected and discarded instead of reused
	// mid-stream (see common.NewHTTPClient doc comment). Building the client by
	// hand here (as this provider previously did) does not get that fix.
	// NewHTTPClient only errors when given a non-empty, malformed proxy URL;
	// this call always passes no proxy, so err is handled defensively rather
	// than assumed impossible.
	httpClient, err := common.NewHTTPClient("")
	if err != nil {
		logger.WarnCF(
			"anthropic_messages",
			"NewHTTPClient failed, falling back to a plain http.Client without HTTP/2 health-check tuning",
			map[string]any{"error": err.Error()},
		)
		httpClient = &http.Client{Timeout: defaultRequestTimeout}
	}
	if timeoutSeconds > 0 {
		httpClient.Timeout = time.Duration(timeoutSeconds) * time.Second
	}

	return &Provider{
		apiKey:     apiKey,
		apiBase:    baseURL,
		httpClient: httpClient,
	}
}

// Chat sends messages to the Anthropic Messages API and returns the response.
func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key not configured")
	}

	// Build request body
	requestBody, err := buildRequestBody(messages, tools, model, options)
	if err != nil {
		return nil, fmt.Errorf("building request body: %w", err)
	}

	// Serialize to JSON
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("serializing request body: %w", err)
	}

	// Build request URL
	endpointURL, err := url.JoinPath(p.apiBase, "messages")
	if err != nil {
		return nil, fmt.Errorf("building endpoint URL: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", p.apiKey) //nolint:canonicalheader // Anthropic API requires exact header name
	req.Header.Set("Anthropic-Version", defaultAPIVersion)

	// Execute request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Check for HTTP errors. common.HandleErrorResponse also detects an HTML
	// error body (misconfigured api_base or a proxy intercepting the request)
	// and reports that distinctly, which the provider's previous hand-rolled
	// per-status switch did not.
	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, p.apiBase)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Parse response (Anthropic Messages wire format — not OpenAI-compatible,
	// so this uses the provider's own parser rather than common.ParseResponse).
	return parseResponseBody(body)
}

// GetDefaultModel returns the default model for this provider.
func (p *Provider) GetDefaultModel() string {
	return "claude-sonnet-4.6"
}

// mergeToolResultIntoLastUser appends toolResultBlock to the last user message's content
// if it already holds tool_result blocks. Returns the updated slice and true if merged,
// or the original slice and false if a new message must be appended instead.
func mergeToolResultIntoLastUser(apiMessages []any, toolResultBlock map[string]any) ([]any, bool) {
	if len(apiMessages) > 0 {
		if prev, ok := apiMessages[len(apiMessages)-1].(map[string]any); ok && prev["role"] == "user" {
			if content, ok := prev["content"].([]map[string]any); ok {
				prev["content"] = append(content, toolResultBlock)
				return apiMessages, true
			}
		}
	}
	return append(apiMessages, map[string]any{
		"role":    "user",
		"content": []map[string]any{toolResultBlock},
	}), false
}

// buildRequestBody converts internal message format to Anthropic Messages API format.
func buildRequestBody(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (map[string]any, error) {
	// max_tokens is required and guaranteed by agent loop
	maxTokens, ok := common.AsInt(options["max_tokens"])
	if !ok {
		return nil, fmt.Errorf("max_tokens is required in options")
	}

	result := map[string]any{
		"model":      model,
		"max_tokens": int64(maxTokens),
		"messages":   []any{},
	}

	// Set temperature from options
	if temp, ok := common.AsFloat(options["temperature"]); ok {
		result["temperature"] = temp
	}

	// Process messages
	var systemPrompt string
	var apiMessages []any

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			// Accumulate system messages
			if systemPrompt != "" {
				systemPrompt += "\n\n" + msg.Content
			} else {
				systemPrompt = msg.Content
			}

		case "user":
			if msg.ToolCallID != "" {
				// Tool result message — merge into previous user message if it contains tool_results
				toolResultBlock := map[string]any{
					"type":        "tool_result",
					"tool_use_id": msg.ToolCallID,
					"content":     msg.Content,
				}
				var merged bool
				apiMessages, merged = mergeToolResultIntoLastUser(apiMessages, toolResultBlock)
				if merged {
					continue
				}
			} else if len(msg.Media) > 0 {
				// User message with media attachments — build multipart content.
				content := buildAnthropicUserContent(msg.Content, msg.Media)
				apiMessages = append(apiMessages, map[string]any{
					"role":    "user",
					"content": content,
				})
			} else {
				// Regular user message
				apiMessages = append(apiMessages, map[string]any{
					"role":    "user",
					"content": msg.Content,
				})
			}

		case "assistant":
			content := []any{}

			// Add text content if present
			if msg.Content != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": msg.Content,
				})
			}

			// Add tool_use blocks
			for _, tc := range msg.ToolCalls {
				if strings.TrimSpace(tc.Name) == "" {
					continue
				}

				// Handle nil Arguments (GLM-4 may return null input)
				input := tc.Arguments
				if input == nil {
					input = map[string]any{}
				}

				toolUse := map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": input,
				}
				content = append(content, toolUse)
			}

			apiMessages = append(apiMessages, map[string]any{
				"role":    "assistant",
				"content": content,
			})

		case "tool":
			// Tool result (alternative format) — merge into previous user message if it contains tool_results
			toolResultBlock := map[string]any{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     msg.Content,
			}
			var merged bool
			apiMessages, merged = mergeToolResultIntoLastUser(apiMessages, toolResultBlock)
			if merged {
				continue
			}
		}
	}

	result["messages"] = apiMessages

	// Set system prompt if present
	if systemPrompt != "" {
		result["system"] = systemPrompt
	}

	// Add tools if present
	if len(tools) > 0 {
		result["tools"] = buildTools(tools)
	}

	return result, nil
}

// buildTools converts tool definitions to Anthropic format.
func buildTools(tools []ToolDefinition) []any {
	result := make([]any, len(tools))
	for i, tool := range tools {
		toolDef := map[string]any{
			"name":         tool.Function.Name,
			"description":  tool.Function.Description,
			"input_schema": tool.Function.Parameters,
		}
		result[i] = toolDef
	}
	return result
}

// buildAnthropicUserContent builds the Anthropic multipart content block for a
// user message that has media attachments. Each data URL is converted to the
// appropriate Anthropic content block type:
//
//   - data:image/<type>;base64,<data> → image block with base64 source
//   - data:application/pdf;base64,<data> → document block with base64 source
//
// Unrecognized data URLs are silently skipped.
func buildAnthropicUserContent(text string, media []string) []any {
	content := make([]any, 0, 1+len(media))
	if text != "" {
		content = append(content, map[string]any{
			"type": "text",
			"text": text,
		})
	}
	for _, dataURL := range media {
		if strings.HasPrefix(dataURL, "data:image/") {
			// Strip "data:" prefix and split into "type;base64" and "<data>" parts.
			payload := strings.TrimPrefix(dataURL, "data:")
			meta, b64data, found := strings.Cut(payload, ",")
			if !found {
				continue
			}
			mediaType, _, _ := strings.Cut(meta, ";")
			mediaType = strings.TrimSpace(mediaType)
			if mediaType == "" {
				continue
			}
			content = append(content, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": mediaType,
					"data":       b64data,
				},
			})
			continue
		}
		if strings.HasPrefix(dataURL, "data:application/pdf;base64,") {
			b64data := strings.TrimPrefix(dataURL, "data:application/pdf;base64,")
			content = append(content, map[string]any{
				"type": "document",
				"source": map[string]any{
					"type":       "base64",
					"media_type": "application/pdf",
					"data":       b64data,
				},
			})
			continue
		}
	}
	return content
}

// parseResponseBody parses Anthropic Messages API response.
func parseResponseBody(body []byte) (*LLMResponse, error) {
	var resp anthropicMessageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing JSON response: %w", err)
	}

	// Extract content and tool calls
	var content strings.Builder
	toolCalls := make([]ToolCall, 0) // Initialize as empty slice (not nil) for consistent JSON serialization

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			argsJSON, marshalErr := json.Marshal(block.Input)
			if marshalErr != nil {
				return nil, fmt.Errorf("failed to marshal tool call arguments for %q: %w", block.Name, marshalErr)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
				Function: &FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	// Map stop_reason
	finishReason := "stop"
	switch resp.StopReason {
	case "tool_use":
		finishReason = "tool_calls"
	case "max_tokens":
		finishReason = "length"
	case "end_turn":
		finishReason = "stop"
	case "stop_sequence":
		finishReason = "stop"
	}

	// PromptTokens = plain (uncached) input; cache tokens are tracked separately.
	// TotalTokens = plain input + cache_creation + cache_read + output.
	cacheWrite := int(resp.Usage.CacheCreationInputTokens)
	cacheRead := int(resp.Usage.CacheReadInputTokens)
	promptTokens := int(resp.Usage.InputTokens)
	completionTokens := int(resp.Usage.OutputTokens)
	total := promptTokens + cacheWrite + cacheRead + completionTokens

	return &LLMResponse{
		Content:      content.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage: &UsageInfo{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			CacheWriteTokens: cacheWrite,
			CacheReadTokens:  cacheRead,
			TotalTokens:      total,
		},
	}, nil
}

// normalizeBaseURL ensures the base URL is properly formatted.
// It removes /v1 suffix if present (to avoid duplication) and always appends /v1.
// This handles edge cases like "https://api.example.com/v1/proxy" correctly.
func normalizeBaseURL(apiBase string) string {
	base := strings.TrimSpace(apiBase)
	if base == "" {
		return defaultBaseURL
	}

	// Remove trailing slashes
	base = strings.TrimRight(base, "/")

	// Remove /v1 suffix if present (will be re-added)
	// This prevents duplication for URLs like "https://api.example.com/v1/proxy"
	if before, ok := strings.CutSuffix(base, "/v1"); ok {
		base = before
	}

	// Ensure we don't have an empty string after cutting
	if base == "" {
		return defaultBaseURL
	}

	// Add /v1 suffix (required by Anthropic Messages API)
	return base + "/v1"
}

// Anthropic API response structures

type anthropicMessageResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Model      string         `json:"model"`
	Usage      usageInfo      `json:"usage"`
}

type contentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
}

type usageInfo struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// APIBase returns the normalised base URL this provider posts to (ADR-067
// DS-3 asserts the URL the catalog row produced).
func (p *Provider) APIBase() string {
	if p == nil {
		return ""
	}
	return p.apiBase
}
