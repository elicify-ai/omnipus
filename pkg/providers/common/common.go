// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package common provides shared utilities used by multiple LLM provider
// implementations (openai_compat, azure, etc.).
package common

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http2"

	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/providers/protocoltypes"
)

// Re-export protocol types used across providers.
type (
	ToolCall               = protocoltypes.ToolCall
	FunctionCall           = protocoltypes.FunctionCall
	LLMResponse            = protocoltypes.LLMResponse
	UsageInfo              = protocoltypes.UsageInfo
	Message                = protocoltypes.Message
	ToolDefinition         = protocoltypes.ToolDefinition
	ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition
	ExtraContent           = protocoltypes.ExtraContent
	GoogleExtra            = protocoltypes.GoogleExtra
	ReasoningDetail        = protocoltypes.ReasoningDetail
)

const DefaultRequestTimeout = 120 * time.Second

// NewHTTPClient creates an *http.Client with an optional proxy and the default timeout.
// Returns an error if proxy is non-empty and cannot be parsed as a URL.
//
// HTTP/2 is KEPT (OpenRouter/Cloudflare negotiate h2 over TLS and reject plain
// HTTP/1.1 with an h2 SETTINGS frame the h1 client can't parse — "malformed HTTP
// response"). The real problem is intermittent "streaming read error: http2:
// response body closed" mid-stream resets, caused by the client reusing a pooled
// connection the server has since GOAWAY'd/closed. Because tokens have already
// streamed, the agent loop can't safely inline-retry (would duplicate text), so a
// single reset aborts the whole turn. The fix is to make h2 connection reuse
// robust: enable health-check PINGs (ReadIdleTimeout) so a dead connection is
// detected and discarded instead of reused, and shorten the idle window so stale
// connections age out quickly.
func NewHTTPClient(proxy string) (*http.Client, error) {
	// Clone DefaultTransport to preserve its TLS/dial/timeout tuning and keep h2.
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}

	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", proxy, err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}

	// Age stale pooled connections out quickly (default is 90s).
	transport.IdleConnTimeout = 30 * time.Second

	// Configure the HTTP/2 transport with health-check pings: if a pooled
	// connection is idle for ReadIdleTimeout, send a PING; if no PONG arrives
	// within PingTimeout, mark the connection dead so it is not reused mid-stream.
	// This eliminates the "http2: response body closed" resets on reused conns.
	if h2t, err := http2.ConfigureTransports(transport); err == nil && h2t != nil {
		h2t.ReadIdleTimeout = 15 * time.Second
		h2t.PingTimeout = 5 * time.Second
	}

	return &http.Client{
		Timeout:   DefaultRequestTimeout,
		Transport: transport,
	}, nil
}

// --- Message serialization ---

// openaiMessage is the wire-format message for OpenAI-compatible APIs.
// It mirrors protocoltypes.Message but omits SystemParts, which is an
// internal field that would be unknown to third-party endpoints.
type openaiMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

// SerializeMessages converts internal Message structs to the OpenAI wire format.
//   - Strips SystemParts (unknown to third-party endpoints)
//   - Converts messages with Media to multipart content format (text + image_url parts)
//   - Preserves ToolCallID, ToolCalls, and ReasoningContent for all messages
func SerializeMessages(messages []Message) []any {
	out := make([]any, 0, len(messages))
	for _, m := range messages {
		if len(m.Media) == 0 {
			out = append(out, openaiMessage{
				Role:             m.Role,
				Content:          m.Content,
				ReasoningContent: m.ReasoningContent,
				ToolCalls:        m.ToolCalls,
				ToolCallID:       m.ToolCallID,
			})
			continue
		}

		// Multipart content format for messages with media
		parts := make([]map[string]any, 0, 1+len(m.Media))
		if m.Content != "" {
			parts = append(parts, map[string]any{
				"type": "text",
				"text": m.Content,
			})
		}
		for _, mediaURL := range m.Media {
			if strings.HasPrefix(mediaURL, "data:image/") {
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": mediaURL,
					},
				})
				continue
			}

			if strings.HasPrefix(mediaURL, "data:application/pdf;base64,") {
				// OpenRouter / OpenAI-compat native PDF file part.
				// The full data URL (including the data:application/pdf;base64, prefix)
				// is passed in the file_data field; OpenRouter routes it to the model's
				// file-parsing pipeline.
				parts = append(parts, map[string]any{
					"type": "file",
					"file": map[string]any{
						"filename":  "attachment.pdf",
						"file_data": mediaURL,
					},
				})
				continue
			}

			if format, data, ok := parseDataAudioURL(mediaURL); ok {
				parts = append(parts, map[string]any{
					"type": "input_audio",
					"input_audio": map[string]any{
						"data":   data,
						"format": format,
					},
				})
			}
		}

		msg := map[string]any{
			"role":    m.Role,
			"content": parts,
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		if m.ReasoningContent != "" {
			msg["reasoning_content"] = m.ReasoningContent
		}
		out = append(out, msg)
	}
	return out
}

func parseDataAudioURL(mediaURL string) (format, data string, ok bool) {
	if !strings.HasPrefix(mediaURL, "data:audio/") {
		return "", "", false
	}

	payload := strings.TrimPrefix(mediaURL, "data:audio/")
	meta, data, found := strings.Cut(payload, ",")
	if !found {
		return "", "", false
	}

	format, _, _ = strings.Cut(meta, ";")
	format = strings.TrimSpace(format)
	data = strings.TrimSpace(data)
	if format == "" || data == "" {
		return "", "", false
	}
	return format, data, true
}

// --- Response parsing ---

// openaiNonStreamUsage is the intermediate struct for non-streaming OpenAI-compatible
// usage objects. It captures prompt_tokens_details.cached_tokens to separate uncached
// prompt tokens from cached ones before populating UsageInfo.
type openaiNonStreamUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

// toUsageInfo converts a non-streaming usage chunk to UsageInfo.
// PromptTokens in the result is uncached input only. CacheReadTokens holds the
// cached portion. CacheWriteTokens stays 0 (OpenAI does not report cache writes).
func (u *openaiNonStreamUsage) toUsageInfo() *UsageInfo {
	if u == nil {
		return nil
	}
	cachedTokens := 0
	if u.PromptTokensDetails != nil {
		cachedTokens = u.PromptTokensDetails.CachedTokens
	}
	promptUncached := u.PromptTokens - cachedTokens
	if promptUncached < 0 {
		promptUncached = 0
	}
	total := u.TotalTokens
	if total == 0 {
		total = promptUncached + cachedTokens + u.CompletionTokens
	}
	return &UsageInfo{
		PromptTokens:     promptUncached,
		CompletionTokens: u.CompletionTokens,
		CacheReadTokens:  cachedTokens,
		TotalTokens:      total,
	}
}

// ParseResponse parses a JSON chat completion response body into an LLMResponse.
func ParseResponse(body io.Reader) (*LLMResponse, error) {
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content          string            `json:"content"`
				ReasoningContent string            `json:"reasoning_content"`
				Reasoning        string            `json:"reasoning"`
				ReasoningDetails []ReasoningDetail `json:"reasoning_details"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function *struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
					ExtraContent *struct {
						Google *struct {
							ThoughtSignature string `json:"thought_signature"`
						} `json:"google"`
					} `json:"extra_content"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *openaiNonStreamUsage `json:"usage"`
	}

	if err := json.NewDecoder(body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return nil, fmt.Errorf("API returned 0 choices")
	}

	choice := apiResponse.Choices[0]
	toolCalls := make([]ToolCall, 0, len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		arguments := make(map[string]any)
		name := ""

		// Extract thought_signature from Gemini/Google-specific extra content
		thoughtSignature := ""
		if tc.ExtraContent != nil && tc.ExtraContent.Google != nil {
			thoughtSignature = tc.ExtraContent.Google.ThoughtSignature
		}

		if tc.Function != nil {
			name = tc.Function.Name
			arguments = DecodeToolCallArguments(tc.Function.Arguments, name)
		}

		toolCall := ToolCall{
			ID:               tc.ID,
			Name:             name,
			Arguments:        arguments,
			ThoughtSignature: thoughtSignature,
		}

		if thoughtSignature != "" {
			toolCall.ExtraContent = &ExtraContent{
				Google: &GoogleExtra{
					ThoughtSignature: thoughtSignature,
				},
			}
		}

		toolCalls = append(toolCalls, toolCall)
	}

	return &LLMResponse{
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
		Reasoning:        choice.Message.Reasoning,
		ReasoningDetails: choice.Message.ReasoningDetails,
		ToolCalls:        toolCalls,
		FinishReason:     normalizeFinishReason(choice.FinishReason),
		Usage:            apiResponse.Usage.toUsageInfo(),
	}, nil
}

// normalizeFinishReason normalizes finish_reason values across providers.
// Converts "length" to "truncated" for consistent handling.
func normalizeFinishReason(reason string) string {
	if reason == "length" {
		return "truncated"
	}
	return reason
}

// DecodeToolCallArguments decodes a tool call's arguments from raw JSON.
func DecodeToolCallArguments(raw json.RawMessage, name string) map[string]any {
	arguments := make(map[string]any)
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return arguments
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		logger.WarnCF(
			"common",
			"failed to decode tool call arguments payload",
			map[string]any{"tool": name, "error": err.Error()},
		)
		arguments["raw"] = string(raw)
		return arguments
	}

	switch v := decoded.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return arguments
		}
		if err := json.Unmarshal([]byte(v), &arguments); err != nil {
			logger.WarnCF(
				"common",
				"failed to decode tool call arguments",
				map[string]any{"tool": name, "error": err.Error()},
			)
			arguments["raw"] = v
		}
		return arguments
	case map[string]any:
		return v
	default:
		logger.WarnCF(
			"common",
			"unsupported tool call arguments type",
			map[string]any{"tool": name, "type": fmt.Sprintf("%T", decoded)},
		)
		arguments["raw"] = string(raw)
		return arguments
	}
}

// --- HTTP response helpers ---

// HandleErrorResponse reads a non-200 response body and returns an appropriate error.
func HandleErrorResponse(resp *http.Response, apiBase string) error {
	contentType := resp.Header.Get("Content-Type")
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if readErr != nil {
		return fmt.Errorf("failed to read response: %w", readErr)
	}
	if LooksLikeHTML(body, contentType) {
		return WrapHTMLResponseError(resp.StatusCode, body, contentType, apiBase)
	}
	return fmt.Errorf(
		"API request failed:\n  Status: %d\n  Body:   %s",
		resp.StatusCode,
		ResponsePreview(body, 512),
	)
}

// ReadAndParseResponse peeks at the response body to detect HTML errors,
// then parses the JSON response into an LLMResponse.
func ReadAndParseResponse(resp *http.Response, apiBase string) (*LLMResponse, error) {
	contentType := resp.Header.Get("Content-Type")
	reader := bufio.NewReader(resp.Body)
	prefix, err := reader.Peek(256)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, fmt.Errorf("failed to inspect response: %w", err)
	}
	if LooksLikeHTML(prefix, contentType) {
		return nil, WrapHTMLResponseError(resp.StatusCode, prefix, contentType, apiBase)
	}
	out, err := ParseResponse(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}
	return out, nil
}

// LooksLikeHTML checks if the response body appears to be HTML.
func LooksLikeHTML(body []byte, contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml") {
		return true
	}
	prefix := bytes.ToLower(leadingTrimmedPrefix(body, 128))
	return bytes.HasPrefix(prefix, []byte("<!doctype html")) ||
		bytes.HasPrefix(prefix, []byte("<html")) ||
		bytes.HasPrefix(prefix, []byte("<head")) ||
		bytes.HasPrefix(prefix, []byte("<body"))
}

// WrapHTMLResponseError creates a descriptive error for HTML responses.
func WrapHTMLResponseError(statusCode int, body []byte, contentType, apiBase string) error {
	respPreview := ResponsePreview(body, 128)
	return fmt.Errorf(
		"API request failed: %s returned HTML instead of JSON (content-type: %s); check api_base or proxy configuration.\n  Status: %d\n  Body:   %s",
		apiBase,
		contentType,
		statusCode,
		respPreview,
	)
}

// ResponsePreview returns a truncated preview of response body for error messages.
func ResponsePreview(body []byte, maxLen int) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "<empty>"
	}
	if len(trimmed) <= maxLen {
		return string(trimmed)
	}
	return string(trimmed[:maxLen]) + "..."
}

func leadingTrimmedPrefix(body []byte, maxLen int) []byte {
	i := 0
	for i < len(body) {
		switch body[i] {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			i++
		default:
			end := i + maxLen
			if end > len(body) {
				end = len(body)
			}
			return body[i:end]
		}
	}
	return nil
}

// --- Numeric helpers ---

// AsInt converts various numeric types to int.
func AsInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int64:
		return int(val), true
	case float64:
		return int(val), true
	case float32:
		return int(val), true
	default:
		return 0, false
	}
}

// AsFloat converts various numeric types to float64.
func AsFloat(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}
