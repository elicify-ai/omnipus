package openai_compat

import (
	"bufio"
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
	ExtraContent           = protocoltypes.ExtraContent
	GoogleExtra            = protocoltypes.GoogleExtra
	ReasoningDetail        = protocoltypes.ReasoningDetail
)

type Provider struct {
	apiKey             string
	apiBase            string
	maxTokensField     string // Field name for max tokens (e.g., "max_completion_tokens" for o1/glm models)
	httpClient         *http.Client
	extraBody          map[string]any // Additional fields to inject into request body
	streamStallTimeout time.Duration  // streaming silence limit; 0 = common.DefaultStreamStallTimeout
}

type Option func(*Provider)

const defaultRequestTimeout = common.DefaultRequestTimeout

func WithMaxTokensField(maxTokensField string) Option {
	return func(p *Provider) {
		p.maxTokensField = maxTokensField
	}
}

func WithRequestTimeout(timeout time.Duration) Option {
	return func(p *Provider) {
		if timeout > 0 {
			p.httpClient.Timeout = timeout
		}
	}
}

func WithExtraBody(extraBody map[string]any) Option {
	return func(p *Provider) {
		p.extraBody = extraBody
	}
}

// WithStreamStallTimeout sets the streaming silence limit (founder decision
// 2026-09-14): a ChatStream call receiving no bytes of any kind for this long
// is aborted with common.ErrStreamStalled. Non-positive falls back to
// common.DefaultStreamStallTimeout. NOT a wall-clock limit — a stream that
// keeps delivering, however slowly, is never cut.
func WithStreamStallTimeout(d time.Duration) Option {
	return func(p *Provider) {
		if d > 0 {
			p.streamStallTimeout = d
		}
	}
}

// effectiveStreamStallTimeout resolves the silence limit for this provider.
func (p *Provider) effectiveStreamStallTimeout() time.Duration {
	if p.streamStallTimeout > 0 {
		return p.streamStallTimeout
	}
	return common.DefaultStreamStallTimeout
}

func NewProvider(apiKey, apiBase, proxy string, opts ...Option) (*Provider, error) {
	httpClient, err := common.NewHTTPClient(proxy)
	if err != nil {
		return nil, fmt.Errorf("openai_compat: %w", err)
	}
	p := &Provider{
		apiKey:     apiKey,
		apiBase:    strings.TrimRight(apiBase, "/"),
		httpClient: httpClient,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}

	return p, nil
}

func NewProviderWithMaxTokensFieldAndTimeout(
	apiKey, apiBase, proxy, maxTokensField string,
	requestTimeoutSeconds int,
) (*Provider, error) {
	return NewProvider(
		apiKey,
		apiBase,
		proxy,
		WithMaxTokensField(maxTokensField),
		WithRequestTimeout(time.Duration(requestTimeoutSeconds)*time.Second),
	)
}

// buildRequestBody constructs the common request body for Chat and ChatStream.
func (p *Provider) buildRequestBody(
	messages []Message, tools []ToolDefinition, model string, options map[string]any,
) map[string]any {
	requestBody := map[string]any{
		"model":    model,
		"messages": common.SerializeMessages(messages),
	}

	// When fallback uses a different provider (e.g. DeepSeek), that provider must not inject web_search_preview.
	nativeSearch, _ := options["native_search"].(bool)
	nativeSearch = nativeSearch && isNativeSearchHost(p.apiBase)
	if len(tools) > 0 || nativeSearch {
		requestBody["tools"] = buildToolsList(tools, nativeSearch)
		requestBody["tool_choice"] = "auto"
	}

	if maxTokens, ok := common.AsInt(options["max_tokens"]); ok {
		fieldName := p.maxTokensField
		if fieldName == "" {
			lowerModel := strings.ToLower(model)
			if strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "o1") ||
				strings.Contains(lowerModel, "gpt-5") {
				fieldName = "max_completion_tokens"
			} else {
				fieldName = "max_tokens"
			}
		}
		requestBody[fieldName] = maxTokens
	}

	if temperature, ok := common.AsFloat(options["temperature"]); ok {
		lowerModel := strings.ToLower(model)
		if strings.Contains(lowerModel, "kimi") && strings.Contains(lowerModel, "k2") {
			requestBody["temperature"] = 1.0
		} else {
			requestBody["temperature"] = temperature
		}
	}

	if seed, ok := common.AsInt(options["seed"]); ok {
		requestBody["seed"] = seed
	}

	// Prompt caching: pass a stable cache key so OpenAI can bucket requests
	// with the same key and reuse prefix KV cache across calls.
	// Prompt caching is only supported by OpenAI-native endpoints.
	// Non-OpenAI providers reject unknown fields with 422 errors.
	if cacheKey, ok := options["prompt_cache_key"].(string); ok && cacheKey != "" {
		if supportsPromptCacheKey(p.apiBase) {
			requestBody["prompt_cache_key"] = cacheKey
		}
	}

	// Merge extra body fields configured per-provider/model.
	// These are injected last so they take precedence over defaults.
	for k, v := range p.extraBody {
		requestBody[k] = v
	}

	return requestBody
}

func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	if p.apiBase == "" {
		return nil, fmt.Errorf("API base not configured")
	}

	requestBody := p.buildRequestBody(messages, tools, model, options)

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiBase+"/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, p.apiBase)
	}

	return common.ReadAndParseResponse(resp, p.apiBase)
}

// ChatStream implements streaming via OpenAI-compatible SSE (stream: true).
// onChunk receives the accumulated text so far on each text delta.
func (p *Provider) ChatStream(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(accumulated string),
	onProgress protocoltypes.OnToolCallProgress,
) (*LLMResponse, error) {
	if p.apiBase == "" {
		return nil, fmt.Errorf("API base not configured")
	}

	requestBody := p.buildRequestBody(messages, tools, model, options)
	requestBody["stream"] = true
	requestBody["stream_options"] = map[string]any{"include_usage": true}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiBase+"/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	// Use a client without http.Client.Timeout for streaming — that timeout covers
	// the entire request lifecycle including body reads, which would kill long streams.
	// Context cancellation from the caller (turn timeout) provides the safety net.
	streamClient := &http.Client{Transport: p.httpClient.Transport}
	//nolint:bodyclose // body is closed via defer resp.Body.Close() below; goroutine also closes to unblock scanner on ctx cancel
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, p.apiBase)
	}

	// Intentional concurrent close: net/http response bodies are safe to Close() concurrently with reads, unblocking any blocked scanner.Scan().
	go func() {
		<-ctx.Done()
		resp.Body.Close()
	}()

	// Silence check (founder decision 2026-09-14): abort this call when
	// NOTHING has arrived for the configured limit. The monitor's clock is
	// re-armed by the parser on every consumed SSE event (parseStreamResponse
	// holds the arm fn), so any byte of any kind — content, tool-call delta,
	// reasoning, keep-alive — keeps a slow stream alive. This is not a
	// wall-clock limit.
	stall := p.effectiveStreamStallTimeout()
	watch := common.WatchStreamStall(ctx, func() { _ = resp.Body.Close() }, stall)
	defer watch.Stop()

	return parseStreamResponse(ctx, resp.Body, onChunk, onProgress, watch)
}

// parseStreamResponse parses an OpenAI-compatible SSE stream. watch, when
// non-nil, is re-armed after EVERY consumed event (data line, comment,
// [DONE], even a malformed one) so the caller's stall monitor restarts its
// silence clock — any byte of any kind counts as the provider still
// responding.
func parseStreamResponse(
	ctx context.Context,
	reader io.Reader,
	onChunk func(accumulated string),
	onProgress protocoltypes.OnToolCallProgress,
	watch *common.StreamStallWatch,
) (*LLMResponse, error) {
	var textContent strings.Builder
	var finishReason string
	var usage *UsageInfo
	var totalArgsBytes int
	var totalReasoningBytes int

	// Tool call assembly: OpenAI streams tool calls as incremental deltas
	type toolAccum struct {
		id       string
		name     string
		argsJSON strings.Builder
	}
	activeTools := map[int]*toolAccum{}

	var malformedChunks int
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 1MB initial, 10MB max
	for scanner.Scan() {
		// Check for context cancellation between chunks
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// Every line the scanner delivers is a byte from the provider: even a
		// comment, a keep-alive, or a malformed chunk proves the connection is
		// alive. Re-arm the stall clock before looking at the content.
		watch.Arm()

		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					// Reasoning ("thinking") deltas — see
					// reasoningDeltaBytes for the three spellings. Only
					// their length is ever used.
					Reasoning        string                  `json:"reasoning"`
					ReasoningContent string                  `json:"reasoning_content"`
					ReasoningDetails []streamReasoningDetail `json:"reasoning_details"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function *struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *common.OpenAINonStreamUsage `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			malformedChunks++
			continue
		}

		if chunk.Usage != nil {
			usage = chunk.Usage.ToUsageInfo()
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		// Accumulate text content
		if choice.Delta.Content != "" {
			textContent.WriteString(choice.Delta.Content)
			if onChunk != nil {
				onChunk(textContent.String())
			}
		}

		// Reasoning deltas count as forward progress (founder decision
		// 2026-09-14, UAT E-15c): a model can reason for many minutes before
		// its first content or tool-call byte, and ignoring these deltas made
		// that indistinguishable from a hung call. The reasoning TEXT is not
		// kept or forwarded — only its byte count reaches the callback.
		if n := reasoningDeltaBytes(
			choice.Delta.Reasoning, choice.Delta.ReasoningContent, choice.Delta.ReasoningDetails,
		); n > 0 {
			totalReasoningBytes += n
			protocoltypes.SafeInvoke(onProgress, protocoltypes.ToolCallProgress{
				Index:          protocoltypes.ReasoningProgressIndex,
				TotalArgsBytes: totalArgsBytes,
				ReasoningBytes: totalReasoningBytes,
			})
		}

		// Accumulate tool call deltas.
		//
		// Every argument delta also emits a progress signal. Without it a
		// model spending tens of seconds emitting a large tool argument
		// produces no observable output at all — indistinguishable from a
		// hung generation, which has caused healthy delegated workers to be
		// killed mid-write. See protocoltypes.ToolCallProgress.
		for _, tc := range choice.Delta.ToolCalls {
			acc, ok := activeTools[tc.Index]
			if !ok {
				acc = &toolAccum{}
				activeTools[tc.Index] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function != nil {
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.argsJSON.WriteString(tc.Function.Arguments)
					totalArgsBytes += len(tc.Function.Arguments)
					// SafeInvoke: the handler runs synchronously in this SSE
					// read loop, so a panic in a consumer would unwind through
					// the parser and kill the turn (ADR-059 AC-06).
					protocoltypes.SafeInvoke(onProgress, protocoltypes.ToolCallProgress{
						Index:          tc.Index,
						Name:           acc.name,
						ArgsBytes:      acc.argsJSON.Len(),
						TotalArgsBytes: totalArgsBytes,
						ReasoningBytes: totalReasoningBytes,
					})
				}
			}
		}

		if choice.FinishReason != nil {
			finishReason = *choice.FinishReason
		}
	}

	if err := scanner.Err(); err != nil {
		// If the caller's context was canceled or timed out, our own ctx.Done()
		// watchdog goroutine (ChatStream) closed resp.Body to unblock this scanner
		// — the resulting "http2: response body closed" is NOT a server-side
		// connection drop but a cancellation/timeout. Surface the context error so
		// callers classify it correctly (context.Canceled → clean cancel;
		// context.DeadlineExceeded → llm_timeout) instead of misreading it as a
		// transient stream reset and retrying a request the caller already abandoned.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// A body-closed read error while the context is still alive is EITHER
		// our stall monitor aborting a fully silent stream (founder decision
		// 2026-09-14) or a genuine server-side reset — the same bytes carry
		// both. Fired() is the only honest discriminator: it is set by our own
		// monitor just before it closes the body. Only then report the typed
		// stall error so callers classify it as a retryable provider fault
		// rather than a transient reset; a server drop keeps its historical
		// streaming-read-error classification.
		if watch.Fired() && common.IsBodyClosedStreamError(err) {
			return nil, common.NewStallError(watch.SilentFor())
		}
		return nil, fmt.Errorf("streaming read error: %w", err)
	}
	if malformedChunks > 0 {
		logger.WarnCF("openai_compat", "skipped malformed SSE chunks", map[string]any{"count": malformedChunks})
	}

	// Assemble tool calls from accumulated deltas
	var toolCalls []ToolCall
	for i := 0; i < len(activeTools); i++ {
		acc, ok := activeTools[i]
		if !ok {
			continue
		}
		// The streaming path is where truncation actually lands: the arguments
		// arrive as a run of deltas appended into acc.argsJSON, and a
		// generation that hits the output-token cap simply stops mid-run,
		// leaving a fragment. Decoding it is the check that catches that —
		// finish_reason cannot be relied on here (see
		// common.ErrToolArgumentsUndecodable).
		args, err := common.DecodeToolCallArguments(
			json.RawMessage(acc.argsJSON.String()), acc.name,
		)
		if err != nil {
			// finishReason and usage are both already captured off the
			// stream at this point (the loop above collects every chunk
			// before tool-call assembly runs) — attach them to the refusal
			// so the caller's classifier can tell truncation from a
			// well-formed wrong-shaped payload, and so the refused
			// attempt's billed usage isn't silently discarded (ADR-087
			// D3.9 / D5).
			return nil, common.AttachToolArgumentsEvidence(err, finishReason, usage)
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:        acc.id,
			Name:      acc.name,
			Arguments: args,
		})
	}

	if finishReason == "" {
		if textContent.Len() > 0 || len(activeTools) > 0 {
			logger.WarnCF("openai_compat", "stream ended without finish_reason; defaulting to \"unknown\"", nil)
			finishReason = "unknown"
		} else {
			finishReason = "stop"
		}
	}

	return &LLMResponse{
		Content:      textContent.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}

// streamReasoningDetail is one element of OpenRouter's structured
// `reasoning_details` delta. Its type decides which field is populated:
// `reasoning.text` fills Text, `reasoning.summary` fills Summary, and
// `reasoning.encrypted` fills Data. Only the lengths are read.
type streamReasoningDetail struct {
	Text    string `json:"text"`
	Summary string `json:"summary"`
	Data    string `json:"data"`
}

// reasoningDeltaBytes returns how many reasoning bytes one streamed delta
// carries. OpenAI-compatible providers spell reasoning three ways:
//
//   - `reasoning` — OpenRouter's normalised string;
//   - `reasoning_content` — Z.AI (GLM) and DeepSeek;
//   - `reasoning_details` — OpenRouter's structured array.
//
// OpenRouter sends `reasoning` AND `reasoning_details` in the same delta with
// the same text, so the first non-empty spelling wins rather than summing all
// three — otherwise that provider would report every byte twice.
func reasoningDeltaBytes(reasoning, reasoningContent string, details []streamReasoningDetail) int {
	if reasoning != "" {
		return len(reasoning)
	}
	if reasoningContent != "" {
		return len(reasoningContent)
	}
	n := 0
	for _, d := range details {
		n += len(d.Text) + len(d.Summary) + len(d.Data)
	}
	return n
}

func buildToolsList(tools []ToolDefinition, nativeSearch bool) []any {
	result := make([]any, 0, len(tools)+1)
	for _, t := range tools {
		if nativeSearch && strings.EqualFold(t.Function.Name, "web_search") {
			continue
		}
		result = append(result, t)
	}
	if nativeSearch {
		result = append(result, map[string]any{"type": "web_search_preview"})
	}
	return result
}

func (p *Provider) SupportsNativeSearch() bool {
	return isNativeSearchHost(p.apiBase)
}

func isNativeSearchHost(apiBase string) bool {
	u, err := url.Parse(apiBase)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "api.openai.com" || strings.HasSuffix(host, ".openai.azure.com")
}

// supportsPromptCacheKey reports whether the given API base is known to
// support the prompt_cache_key request field. Currently only OpenAI's own
// API and Azure OpenAI support this. All other OpenAI-compatible providers
// (Mistral, Gemini, DeepSeek, Groq, etc.) reject unknown fields with 422 errors.
func supportsPromptCacheKey(apiBase string) bool {
	u, err := url.Parse(apiBase)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "api.openai.com" || strings.HasSuffix(host, ".openai.azure.com")
}

// APIBase returns the resolved base URL this provider posts to (ADR-067
// DS-3 asserts the URL the catalog row produced).
func (p *Provider) APIBase() string {
	if p == nil {
		return ""
	}
	return p.apiBase
}
