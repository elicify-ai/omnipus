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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http2"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
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

// OpenAINonStreamUsage is the intermediate struct for OpenAI-compatible usage
// objects (both the non-streaming response's top-level "usage" field and a
// streaming response's final SSE usage chunk share this shape). It captures
// prompt_tokens_details.cached_tokens to separate uncached prompt tokens from
// cached ones before populating UsageInfo. Exported so other providers in this
// package family (e.g. openai_compat's SSE parser) can decode into it directly
// instead of duplicating the struct and its conversion logic.
type OpenAINonStreamUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

// ToUsageInfo converts a non-streaming usage chunk to UsageInfo.
// PromptTokens in the result is uncached input only. CacheReadTokens holds the
// cached portion. CacheWriteTokens stays 0 (OpenAI does not report cache writes).
func (u *OpenAINonStreamUsage) ToUsageInfo() *UsageInfo {
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
		Usage *OpenAINonStreamUsage `json:"usage"`
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
			decodedArgs, err := DecodeToolCallArguments(tc.Function.Arguments, name)
			if err != nil {
				// The refused attempt's finish reason and billed usage are
				// both already in hand at this scope (choice.FinishReason,
				// apiResponse.Usage) — attach them so the caller's
				// classifier (and cost accounting) sees real evidence
				// instead of having to guess from the fragment alone
				// (ADR-087 D3.9 / D5).
				return nil, AttachToolArgumentsEvidence(err, choice.FinishReason, apiResponse.Usage.ToUsageInfo())
			}
			arguments = decodedArgs
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
		Usage:            apiResponse.Usage.ToUsageInfo(),
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

// ErrToolArgumentsUndecodable reports a tool call whose `arguments` payload
// was PRESENT but could not be decoded into a JSON object.
//
// This is a hard error — never a degraded dispatch — and the reason is
// truncation. When a generation hits the output-token cap partway through a
// tool call, what reaches us is a fragment: `{"query`, or in the shape
// llama.cpp is known to emit, a bare `{`. Those are not "weird arguments",
// they are the visible end of a response that was cut off, and the only
// honest reading is that the model never finished saying what it wanted done.
//
// Why we don't rely on finish_reason alone (ADR-087 D7): on the primary
// transports this project talks to — OpenAI-compatible and
// Anthropic-compatible endpoints — finish_reason (respectively stop_reason)
// IS reliable, and is wired up as real truncation evidence rather than
// ignored (see ToolArgumentsError.Truncated below). But a minority of
// OpenAI-compatible servers get the field wrong specifically on the
// tool-call path: vLLM's streaming handler has marked a choice as having
// produced tool calls the moment any delta carries one, with no check that
// the call is complete, reporting "tool_calls" in place of the engine's
// real "length" (vllm#47903, open; the proposed fix vllm#47963 has sat
// unreviewed, and the merged non-streaming fix is gated behind a flag the
// Hermes path does not set). Parsing the arguments is cheap, local
// insurance against that minority — it costs nothing when finish_reason is
// right, and it is what catches the fragment when finish_reason is wrong.
//
// What went wrong before this was an error: every decode site substituted a
// stand-in — a `raw` key holding the fragment, or an empty map — and
// dispatched the tool anyway. The tool then failed downstream on schema
// validation with "missing required property path" or "unexpected property
// raw", which is a true statement about the map and a false statement about
// the cause. The model, told it forgot a parameter, re-sends the same
// oversized call and is truncated again. Surfacing truncation as truncation
// is what breaks that loop.
//
// Refusing the whole response rather than dropping the one bad call is
// deliberate. A response cut off mid-call is incomplete as a whole, so the
// tool calls that did parse are a partial view of a plan the model never
// finished expressing; running them commits side effects for a decision that
// was never fully stated. It also keeps the fragment out of session history —
// an undecodable call appended to the transcript is re-sent on every
// subsequent request, which is how one truncated call permanently wedges a
// conversation (llama.cpp#21771, open).
// The message opens with "invalid tool arguments" deliberately: that exact
// phrase is the pinned substring pkg/agent's translate_error.go matches to
// label an error CodeToolArgs (ADR-051 Rev 4 FR-018). Without it the turn
// still fails loudly but the user is shown the generic CodeUnknown copy
// instead of the tool-argument copy that actually describes what happened.
// Wording note: this text must never contain the phrase "token limit" — the
// classifier's contextOverflowPatterns match it and would re-label a
// truncated call as a context-window overflow, which is a different fault
// with different advice. Say "output cap" instead.
var ErrToolArgumentsUndecodable = errors.New("invalid tool arguments: payload is not decodable JSON")

// maxUndecodableArgumentsQuoted bounds how many bytes of an offending payload
// an error quotes. The fragment is the diagnostic — it is what tells an
// operator "this was cut off" rather than "this was malformed" — but a
// hostile or runaway payload must not flood a log line.
const maxUndecodableArgumentsQuoted = 256

// DecodeToolCallArguments decodes one tool call's `arguments` payload into the
// map the dispatcher hands the tool.
//
// This is the ONLY tool-argument decoder in the codebase. Every provider —
// openai_compat (streaming and non-streaming), anthropic, the OpenAI
// Responses API, bedrock, and the CLI-provider text extractor — routes
// through it, because the alternative is what was here before: eight decode
// sites that had drifted into five different policies for the same fragment
// (`{"raw": …}`, `{"_raw": …}` under a different key, an empty map, with and
// without a log). Add a new provider by calling this, not by writing a sixth.
//
// An ABSENT payload is not an error. Empty, whitespace, and explicit `null`
// all decode to an empty map with a nil error, because a zero-parameter tool
// legitimately sends nothing at all — `list_mounts`, `browser_snapshot` and
// the sysagent's parameterless tools all do. Only a payload that is present
// and undecodable returns ErrToolArgumentsUndecodable; conflating the two is
// the defect this function exists to avoid.
func DecodeToolCallArguments(raw json.RawMessage, name string) (map[string]any, error) {
	arguments := make(map[string]any)
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return arguments, nil
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, undecodableArgumentsError(name, string(raw), err)
	}

	switch v := decoded.(type) {
	case string:
		// The OpenAI wire format specifies `arguments` as a JSON-ENCODED
		// STRING, so a conforming payload decodes once to a string that must
		// itself be decoded. A truncated call typically fails on this second
		// pass, not the first.
		if strings.TrimSpace(v) == "" {
			return arguments, nil
		}
		if err := json.Unmarshal([]byte(v), &arguments); err != nil {
			return nil, undecodableArgumentsError(name, v, err)
		}
		return arguments, nil
	case map[string]any:
		return v, nil
	default:
		// Valid JSON, wrong shape: a bare number, array or boolean where an
		// object belongs. Not dispatchable as named parameters, so it fails
		// exactly like a fragment does. This shape is NEVER truncation —
		// json.Unmarshal above already succeeded, so there is no prefix to
		// have been cut off; only finish-reason evidence attached later
		// (AttachToolArgumentsEvidence) can mark it Truncated (ADR-087 D5).
		return nil, NewToolArgumentsError(name, fmt.Errorf(
			"%w: tool %q: arguments decoded to %T, want a JSON object: %s",
			ErrToolArgumentsUndecodable, name, decoded, quoteUndecodableArguments(string(raw)),
		), false)
	}
}

// undecodableArgumentsError builds the error for a payload that would not
// parse, quoting the fragment so the truncation is visible in the message.
func undecodableArgumentsError(name, payload string, cause error) error {
	// Both the sentinel and the underlying JSON cause are wrapped with %w, so
	// errors.Is matches ErrToolArgumentsUndecodable AND a caller that cares
	// can still reach the *json.SyntaxError underneath.
	return NewToolArgumentsError(name, fmt.Errorf(
		"%w: tool %q: %w: %s",
		ErrToolArgumentsUndecodable, name, cause, quoteUndecodableArguments(payload),
	), isEOFShapedToolArgumentsFragment(payload))
}

// ToolArgumentsError reports a tool call whose `arguments` payload was
// refused by DecodeToolCallArguments, distinguishing genuine truncation
// (the response was cut off before the call finished) from a well-formed
// payload of the wrong shape (ADR-087 D5) — ErrToolArgumentsUndecodable
// alone does not prove truncation, since it also fires for valid JSON like
// `42`, `true`, or `[1,2,3]` (TestDecodeToolCallArguments_NonObjectRefused).
//
// This is the fixed D→C interface WP D ships for pkg/agent's
// TranslateTurnError to classify on (ADR-087 §9): Cause wraps
// ErrToolArgumentsUndecodable, Truncated gates CodeToolCallTruncated vs
// CodeToolArgs. Shape is not negotiable — see the ADR.
type ToolArgumentsError struct {
	// Cause wraps ErrToolArgumentsUndecodable (and, for a decode failure,
	// the underlying JSON error) — see undecodableArgumentsError and the
	// non-object branch of DecodeToolCallArguments.
	Cause error
	// ToolName is the tool the undecodable arguments belonged to.
	ToolName string
	// FinishReason is the finish/stop reason as received from the provider
	// for the refused attempt, unnormalized, "" if unknown. Populated by
	// AttachToolArgumentsEvidence — DecodeToolCallArguments itself has no
	// visibility into it.
	FinishReason string
	// Truncated is true only when the evidence supports truncation: the
	// finish reason is length/max_tokens/truncated, OR the refused fragment
	// is the unclosed prefix of a JSON object. NEVER true for well-formed
	// JSON of the wrong shape absent finish-reason evidence.
	Truncated bool
	// Usage is the refused attempt's billed usage, when the provider
	// returned one. The provider already charged for these tokens even
	// though the call was refused; the caller debits them once.
	Usage *UsageInfo
}

// Error implements error. It returns Cause's text unchanged — this is the
// same message DecodeToolCallArguments callers saw before this type
// existed (tool name + quoted fragment), so no classifier substring match
// anywhere in the codebase shifts as a side effect of this type's
// introduction.
func (e *ToolArgumentsError) Error() string {
	if e == nil {
		return "<nil ToolArgumentsError>"
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return ErrToolArgumentsUndecodable.Error()
}

// Unwrap exposes Cause to errors.Is / errors.As chain walks, so
// errors.Is(err, ErrToolArgumentsUndecodable) keeps working exactly as it
// did when DecodeToolCallArguments returned a bare wrapped error.
func (e *ToolArgumentsError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// As implements the errors.As extension point (see the standard library's
// errors.As documentation) so a *ToolArgumentsError still satisfies every
// existing consumer that walks an error chain looking for a *ProviderError
// — pkg/agent's errorToProviderError chief among them. Status reads 0 (no
// HTTP request failed: the upstream returned a healthy response whose
// content was cut off) and Body carries the pinned "invalid tool arguments"
// substring, exactly the shape NewToolArgumentsError produced before this
// type existed — TestToolArgumentsErrorClassifiesAsToolArgs pins both.
// This keeps that whole classification path working without this type
// embedding a *ProviderError instance, which would have given it two
// independent "what actually happened" stories to keep in sync.
func (e *ToolArgumentsError) As(target any) bool {
	if e == nil {
		return false
	}
	if pp, ok := target.(**ProviderError); ok {
		*pp = &ProviderError{
			Status: 0,
			Body:   e.Error(),
			Err:    e,
		}
		return true
	}
	return false
}

// NewToolArgumentsError builds the typed refusal DecodeToolCallArguments (and
// ParseResponse's non-object branch) returns for an undecodable `arguments`
// payload. truncated must be the caller's best local judgement from the
// fragment shape alone — AttachToolArgumentsEvidence layers finish-reason
// evidence on afterward, once the caller has it.
func NewToolArgumentsError(name string, cause error, truncated bool) *ToolArgumentsError {
	return &ToolArgumentsError{
		Cause:     cause,
		ToolName:  name,
		Truncated: truncated,
	}
}

// isEOFShapedToolArgumentsFragment reports whether payload looks like an
// object literal whose closing brace never arrived — the shape a
// generation leaves behind when it is cut off mid tool-call (`{"query`,
// the bare `{` llama.cpp shape, `{"path":"a.txt","content":"hello`), as
// opposed to well-formed JSON of the wrong type. Only ever called on a
// payload that already failed json.Unmarshal (undecodableArgumentsError's
// caller), so "never closes" — an unterminated string or an unbalanced
// brace count — is the only thing left to establish (ADR-087 D5).
func isEOFShapedToolArgumentsFragment(payload string) bool {
	trimmed := strings.TrimSpace(payload)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	depth := 0
	inString := false
	escaped := false
	for _, r := range trimmed {
		if inString {
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return inString || depth > 0
}

// isTruncationFinishReason reports whether reason (raw, as received from
// the provider — "length", "max_tokens", or the post-normalizeFinishReason
// spelling "truncated") is evidence the generation was cut off at the
// output-token cap. Matching is case-insensitive; every other value
// (including "" / unknown) is not truncation evidence by itself.
func isTruncationFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "truncated":
		return true
	}
	return false
}

// AttachToolArgumentsEvidence enriches any *ToolArgumentsError reachable in
// err's chain with the finish reason and billed usage of the refused
// attempt, and folds finish-reason evidence into Truncated (ADR-087 D5: the
// finish reason wins even over a well-formed-shaped fragment — it is never
// used to downgrade an already-true Truncated). Returns err unchanged
// (including nil, and including an err with no *ToolArgumentsError in its
// chain) so callers can call this unconditionally on every decode-failure
// return without a type check first.
func AttachToolArgumentsEvidence(err error, finishReason string, usage *UsageInfo) error {
	var tae *ToolArgumentsError
	if !errors.As(err, &tae) {
		return err
	}
	tae.FinishReason = finishReason
	tae.Usage = usage
	if isTruncationFinishReason(finishReason) {
		tae.Truncated = true
	}
	return err
}

// quoteUndecodableArguments renders a payload for an error message, capped at
// maxUndecodableArgumentsQuoted bytes and reporting the true length when it
// had to cut. Quoting via %q escapes the partial UTF-8 sequence a byte-offset
// cut can leave behind, so the result is always printable.
func quoteUndecodableArguments(payload string) string {
	if len(payload) > maxUndecodableArgumentsQuoted {
		return fmt.Sprintf(
			"%q…[%d bytes total]",
			payload[:maxUndecodableArgumentsQuoted], len(payload),
		)
	}
	return fmt.Sprintf("%q", payload)
}

// --- HTTP response helpers ---

// handleErrorBodyCap is the upper bound (in bytes) on the response body
// the helpers read into memory. 8 KiB is generous enough to capture every
// real provider error envelope (OpenAI/Anthropic/Google/xAI all stay
// under 4 KiB even on verbose validation errors) and small enough to be
// safe against a hostile upstream. Above this we keep the partial bytes
// the read returned and surface BodyTruncated=true so the caller (and the
// classifier at the choke point) knows it is partial.
const handleErrorBodyCap = 8 * 1024

// ProviderError carries the structured provider-error info up the stack so
// the agent-loop classifier (pkg/agent/translate_error.go) sees status +
// body instead of a stringified message. Body is the full body when it
// fit within handleErrorBodyCap; partial bytes otherwise (BodyTruncated
// is true). BodyPreview is the 512B preview used only for log lines, never
// for classification.
//
// Created by HandleErrorResponse and WrapHTMLResponseError. Converted
// across the providers → agent boundary via the agent package's own
// ProviderError (a separate type — this one lives here because it is the
// provider's view; the agent-package one is the wire-shape seam).
type ProviderError struct {
	Status        int
	Body          string
	BodyTruncated bool
	BodyPreview   string
	ContentType   string
	Err           error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil ProviderError>"
	}
	preview := e.BodyPreview
	if preview == "" {
		preview = e.Body
	}
	// LOW item (review): BodyTruncated was written in three places
	// (HandleErrorResponse x2, WrapHTMLResponseError) but never read
	// anywhere — a dead field. Wire it into the message itself: an
	// operator or classifier reading the error text can now tell a
	// genuinely short body apart from a body that hit handleErrorBodyCap
	// and lost data, instead of the flag being invisible dead weight.
	// This only appends a suffix — it does not change the existing
	// status=/content-type=/body= prefix any substring-matching caller
	// scans for.
	if e.BodyTruncated {
		return fmt.Sprintf(
			"provider error: status=%d content-type=%s body=%q (truncated at %d bytes)",
			e.Status, e.ContentType, preview, handleErrorBodyCap,
		)
	}
	return fmt.Sprintf(
		"provider error: status=%d content-type=%s body=%q",
		e.Status, e.ContentType, preview,
	)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// HandleErrorResponse reads a non-200 response body and returns an
// appropriate error. Wave 1 (ADR-051 §RD5 MAJ-008): reads the FULL body
// (capped at handleErrorBodyCap for safety) before any 512B log preview
// so the classifier at the agent-loop choke point sees the entire
// provider response, not a truncated fragment that could misclassify a
// media-rejection message. Partial bytes are preserved when the read
// fails; the returned *ProviderError carries a BodyTruncated flag so the
// caller knows.
func HandleErrorResponse(resp *http.Response, apiBase string) error {
	contentType := resp.Header.Get("Content-Type")
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, handleErrorBodyCap))
	truncated := false
	if readErr != nil {
		// Preserve partial bytes on read failure: keep what was read so the
		// classifier still has SOMETHING to substring-match on, and surface
		// the read error on pe.Err so operator triage can see it.
		truncated = len(body) >= handleErrorBodyCap
		pe := &ProviderError{
			Status:        resp.StatusCode,
			Body:          string(body),
			BodyTruncated: truncated,
			BodyPreview:   ResponsePreview(body, 512),
			ContentType:   contentType,
			Err:           fmt.Errorf("failed to read response: %w", readErr),
		}
		return pe
	}
	// Detect a body that hit the cap exactly — that means the read was
	// truncated by the LimitReader rather than the upstream finishing.
	if len(body) >= handleErrorBodyCap {
		truncated = true
	}
	if LooksLikeHTML(body, contentType) {
		return WrapHTMLResponseError(resp.StatusCode, body, contentType, apiBase)
	}
	return &ProviderError{
		Status:        resp.StatusCode,
		Body:          string(body),
		BodyTruncated: truncated,
		BodyPreview:   ResponsePreview(body, 512),
		ContentType:   contentType,
		Err: fmt.Errorf("API request failed: status=%d body=%s",
			resp.StatusCode, ResponsePreview(body, 512)),
	}
}

// ReadAndParseResponse peeks at the response body to detect HTML errors,
// then parses the JSON response into an LLMResponse.
func ReadAndParseResponse(resp *http.Response, apiBase string) (*LLMResponse, error) {
	contentType := resp.Header.Get("Content-Type")
	reader := bufio.NewReader(resp.Body)
	prefix, err := reader.Peek(256)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
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
// Wave 1 (choke-point unification): returns a *ProviderError (not a
// plain error) so the agent-loop classifier sees status/body uniformly
// across every error path — wave-2 choke points consume one shape.
func WrapHTMLResponseError(statusCode int, body []byte, contentType, apiBase string) error {
	respPreview := ResponsePreview(body, 128)
	truncated := len(body) >= handleErrorBodyCap
	return &ProviderError{
		Status:        statusCode,
		Body:          string(body),
		BodyTruncated: truncated,
		BodyPreview:   respPreview,
		ContentType:   contentType,
		Err: fmt.Errorf(
			"API request failed: %s returned HTML instead of JSON (content-type: %s); check api_base or proxy configuration.\n  Status: %d\n  Body:   %s",
			apiBase,
			contentType,
			statusCode,
			respPreview,
		),
	}
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
