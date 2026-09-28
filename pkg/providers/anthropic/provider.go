package anthropicprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

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
	ThinkingBlock          = protocoltypes.ThinkingBlock
	ToolDefinition         = protocoltypes.ToolDefinition
	ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition
)

const (
	defaultBaseURL      = "https://api.anthropic.com"
	anthropicBetaHeader = "oauth-2025-04-20"
)

type Provider struct {
	client             *anthropic.Client
	tokenSource        func() (string, error)
	baseURL            string
	streamStallTimeout time.Duration // streaming silence limit; 0 = common.DefaultStreamStallTimeout
}

// SupportsThinking implements providers.ThinkingCapable.
func (p *Provider) SupportsThinking() bool { return true }

func NewProvider(token string) *Provider {
	return NewProviderWithBaseURL(token, "")
}

func NewProviderWithBaseURL(token, apiBase string) *Provider {
	baseURL := normalizeBaseURL(apiBase)
	client := anthropic.NewClient(
		option.WithAuthToken(token),
		option.WithBaseURL(baseURL),
	)
	return &Provider{
		client:  &client,
		baseURL: baseURL,
	}
}

// WithStreamStallTimeout sets the streaming silence limit on this provider
// (founder decision 2026-09-14): a ChatStream call receiving no events of any
// kind for this long is aborted with common.ErrStreamStalled. Non-positive
// falls back to common.DefaultStreamStallTimeout. NOT a wall-clock limit — a
// stream that keeps delivering, however slowly, is never cut.
func (p *Provider) WithStreamStallTimeout(d time.Duration) *Provider {
	if d > 0 {
		p.streamStallTimeout = d
	}
	return p
}

// effectiveStreamStallTimeout resolves the silence limit for this provider.
func (p *Provider) effectiveStreamStallTimeout() time.Duration {
	if p.streamStallTimeout > 0 {
		return p.streamStallTimeout
	}
	return common.DefaultStreamStallTimeout
}

func NewProviderWithClient(client *anthropic.Client) *Provider {
	return &Provider{
		client:  client,
		baseURL: defaultBaseURL,
	}
}

func NewProviderWithTokenSource(token string, tokenSource func() (string, error)) *Provider {
	return NewProviderWithTokenSourceAndBaseURL(token, tokenSource, "")
}

func NewProviderWithTokenSourceAndBaseURL(token string, tokenSource func() (string, error), apiBase string) *Provider {
	p := NewProviderWithBaseURL(token, apiBase)
	p.tokenSource = tokenSource
	return p
}

func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	var opts []option.RequestOption
	if p.tokenSource != nil {
		tok, err := p.tokenSource()
		if err != nil {
			return nil, fmt.Errorf("refreshing token: %w", err)
		}
		opts = append(opts,
			option.WithAuthToken(tok),
			option.WithHeader("anthropic-beta", anthropicBetaHeader),
		)
	}

	params, err := buildParams(messages, tools, model, options)
	if err != nil {
		return nil, err
	}

	// OAuth/setup-tokens require streaming; API keys use non-streaming.
	if p.tokenSource != nil {
		return p.chatStreaming(ctx, params, opts)
	}

	resp, err := p.client.Messages.New(ctx, params, opts...)
	if err != nil {
		return nil, fmt.Errorf("claude API call: %w", err)
	}

	return parseResponse(resp)
}

// ChatStream implements providers.StreamingProvider.
//
// Before this existed the Provider did not satisfy StreamingProvider at all,
// so the agent loop's type assertion failed and EVERY Anthropic-backed call
// — including delegated sub-turns — went down the non-streaming path. That
// made the whole response a black box: no partial text, no tool-argument
// progress, nothing to distinguish a model still working from one that had
// hung. Delegated workers were killed on that ambiguity.
//
// onReasoning receives the ACCUMULATED thinking display text on every
// thinking_delta — display text only, never signature or redacted data
// (ADR-095 D7: signatures are captured at parseResponse, where both the
// streaming and non-streaming paths funnel; they never ride a streaming
// callback). Nil-safe like the other callbacks.
func (p *Provider) ChatStream(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(accumulated string),
	onProgress protocoltypes.OnToolCallProgress,
	onReasoning func(accumulated string),
) (*LLMResponse, error) {
	var opts []option.RequestOption
	if p.tokenSource != nil {
		tok, err := p.tokenSource()
		if err != nil {
			return nil, fmt.Errorf("refreshing token: %w", err)
		}
		opts = append(opts,
			option.WithAuthToken(tok),
			option.WithHeader("anthropic-beta", anthropicBetaHeader),
		)
	}

	params, err := buildParams(messages, tools, model, options)
	if err != nil {
		return nil, err
	}

	return p.streamWithCallbacks(ctx, params, opts, onChunk, onProgress, onReasoning)
}

func (p *Provider) chatStreaming(
	ctx context.Context,
	params anthropic.MessageNewParams,
	opts []option.RequestOption,
) (*LLMResponse, error) {
	return p.streamWithCallbacks(ctx, params, opts, nil, nil, nil)
}

// streamWithCallbacks consumes the SSE stream, accumulating into a Message
// exactly as before, and additionally reports forward progress.
//
// Progress is derived from the ACCUMULATED message after each event rather
// than by decoding delta union types. That keeps this robust across SDK
// revisions: whatever shape the deltas take, the accumulated content blocks
// are the same ones parseResponse already reads.
//
// Silence check (founder decision 2026-09-14): a stream that delivers no
// BYTE for p's silence limit is aborted with common.ErrStreamStalled. The
// clock re-arms on every byte read off the response body (middleware below),
// not on parsed events — the SDK swallows Anthropic's keep-alive pings
// internally (Stream.Next's "ping" case), so event-level arming would
// misread a ping-only stream as silent.
func (p *Provider) streamWithCallbacks(
	ctx context.Context,
	params anthropic.MessageNewParams,
	opts []option.RequestOption,
	onChunk func(accumulated string),
	onProgress protocoltypes.OnToolCallProgress,
	onReasoning func(accumulated string),
) (*LLMResponse, error) {
	stall := p.effectiveStreamStallTimeout()

	// stallArmer hands the monitor's arm fn to the body-wrapping middleware
	// without an ordering dependency: the middleware runs inside
	// NewStreaming, before the monitor exists, so the cell is filled
	// immediately after the stream is created and every subsequent Read
	// re-arms the clock. Reads that race the fill simply skip one arm.
	var armer stallBodyArmer
	streamOpts := opts
	if stall > 0 {
		streamOpts = append(append([]option.RequestOption{}, opts...),
			option.WithMiddleware(func(req *http.Request, next func(*http.Request) (*http.Response, error)) (*http.Response, error) {
				resp, err := next(req)
				if err != nil || resp == nil || resp.Body == nil {
					return resp, err
				}
				// Method value, bound to &armer: reads made before the monitor
				// exists find armer.arm nil and skip; every later read re-arms.
				resp.Body = &armReader{ReadCloser: resp.Body, onByte: armer.onByte}
				return resp, nil
			}))
	}

	stream := p.client.Messages.NewStreaming(ctx, params, streamOpts...)
	defer stream.Close()

	var watch *common.StreamStallWatch
	if stall > 0 {
		watch = common.WatchStreamStall(ctx, func() { _ = stream.Close() }, stall)
		defer watch.Stop()
		armer.set(watch.Arm)
	}

	var msg anthropic.Message
	var lastTextLen int
	var lastReasoningLen int
	var lastReasoningTextLen int
	lastArgsLen := map[int]int{}

	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			return nil, fmt.Errorf("claude streaming accumulate: %w", err)
		}
		if onChunk == nil && onProgress == nil && onReasoning == nil {
			continue
		}

		// One pass to measure, then emit — so TotalArgsBytes is the true
		// total across all blocks rather than a running partial.
		var text strings.Builder
		var reasoningText strings.Builder
		argsLen := make(map[int]int, len(msg.Content))
		names := make(map[int]string, len(msg.Content))
		totalArgs := 0
		reasoning := 0
		for i, block := range msg.Content {
			// Read the union's DIRECT fields, never the As*() accessors.
			//
			// AsText()/AsToolUse() re-unmarshal from ContentBlockUnion.JSON.raw,
			// and raw is only populated at content_block_start and re-marshalled
			// at content_block_stop — the deltas in between mutate the direct
			// fields and never touch raw. Using the accessors therefore reports
			// text="" and Input="{}" (2 bytes) for the entire lifetime of a
			// block, then everything at once when it closes.
			//
			// That is exactly the blackout this callback exists to eliminate: a
			// 45-second, 40 KB tool argument would emit one 2-byte event at the
			// start and nothing again until it was already finished. Verified
			// empirically against anthropic-sdk-go v1.48.0.
			switch block.Type {
			case "text":
				text.WriteString(block.Text)
			case "tool_use":
				n := len(block.Input)
				argsLen[i] = n
				names[i] = block.Name
				totalArgs += n
			case "thinking":
				// Extended thinking counts as progress (founder decision
				// 2026-09-14): minutes of thinking with no text or tool
				// bytes must not read as a hung call. Length only.
				reasoning += len(block.Thinking)
				reasoningText.WriteString(block.Thinking)
			case "redacted_thinking":
				reasoning += len(block.Data)
			}
		}

		// Emit ONLY on growth, never on shrink. The agent loop's streaming
		// consumer computes its delta as accumulated[len(lastChunk):]
		// (loop.go), which panics with slice-out-of-range if a later
		// accumulated value is shorter than an earlier one. Accumulate()
		// should only ever grow the text, but a block reorder, a
		// replacement, or a future SDK revision changing those semantics
		// would otherwise crash the turn rather than degrade.
		if onChunk != nil && text.Len() > lastTextLen {
			lastTextLen = text.Len()
			onChunk(text.String())
		}
		// ADR-095 D5b/D7: the thinking display text streams to the same
		// accumulated-string contract the text callback has — growth-only,
		// one call per thinking-bearing event, DISPLAY TEXT ONLY. The
		// signature arrives as its own signature_delta and is captured at
		// parseResponse instead; it never rides this callback.
		if onReasoning != nil && reasoningText.Len() > lastReasoningTextLen {
			lastReasoningTextLen = reasoningText.Len()
			onReasoning(reasoningText.String())
		}
		if onProgress != nil {
			// Same growth-only rule as text and arguments.
			if reasoning > lastReasoningLen {
				lastReasoningLen = reasoning
				protocoltypes.SafeInvoke(onProgress, protocoltypes.ToolCallProgress{
					Index:          protocoltypes.ReasoningProgressIndex,
					TotalArgsBytes: totalArgs,
					ReasoningBytes: reasoning,
				})
			}
			for i, n := range argsLen {
				if n <= lastArgsLen[i] {
					continue
				}
				lastArgsLen[i] = n
				// SafeInvoke: this runs synchronously in the stream loop, so a
				// panicking consumer would otherwise kill the turn (AC-06).
				protocoltypes.SafeInvoke(onProgress, protocoltypes.ToolCallProgress{
					Index:          i,
					Name:           names[i],
					ArgsBytes:      n,
					TotalArgsBytes: totalArgs,
					ReasoningBytes: reasoning,
				})
			}
		}
	}
	if err := stream.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// Fired() (set by our own monitor just before it closes the stream)
		// is what distinguishes our abort of a fully silent stream from a
		// genuine server-side drop, which surfaces as the same read error.
		if watch.Fired() {
			return nil, common.NewStallError(stall)
		}
		return nil, fmt.Errorf("claude API call: %w", err)
	}

	return parseResponse(&msg)
}

// stallBodyArmer is the late-bound cell connecting the stall monitor's re-arm
// function to the response-body middleware (see streamWithCallbacks).
type stallBodyArmer struct {
	mu  sync.Mutex
	arm func()
}

func (a *stallBodyArmer) set(arm func()) {
	a.mu.Lock()
	a.arm = arm
	a.mu.Unlock()
}

// onByte re-arms the silence clock. Called after every successful Read of the
// response body; safe concurrently with set.
func (a *stallBodyArmer) onByte() {
	a.mu.Lock()
	arm := a.arm
	a.mu.Unlock()
	if arm != nil {
		arm()
	}
}

// armReader wraps the response body so every byte delivered re-arms the stall
// monitor — pings, comments, and events alike.
type armReader struct {
	io.ReadCloser
	onByte func()
}

func (r *armReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.onByte != nil {
		r.onByte()
	}
	return n, err
}

func (p *Provider) GetDefaultModel() string {
	return "claude-sonnet-4.6"
}

func (p *Provider) BaseURL() string {
	return p.baseURL
}

func buildParams(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (anthropic.MessageNewParams, error) {
	var system []anthropic.TextBlockParam
	var anthropicMessages []anthropic.MessageParam

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			// Prefer structured SystemParts for per-block cache_control.
			// This enables LLM-side KV cache reuse: the static block's prefix
			// hash stays stable across requests while dynamic parts change freely.
			if len(msg.SystemParts) > 0 {
				for _, part := range msg.SystemParts {
					block := anthropic.TextBlockParam{Text: part.Text}
					if part.CacheControl != nil && part.CacheControl.Type == "ephemeral" {
						block.CacheControl = anthropic.NewCacheControlEphemeralParam()
					}
					system = append(system, block)
				}
			} else {
				system = append(system, anthropic.TextBlockParam{Text: msg.Content})
			}
		case "user":
			if msg.ToolCallID != "" {
				anthropicMessages = append(anthropicMessages,
					anthropic.NewUserMessage(anthropicToolResult(msg)),
				)
			} else {
				anthropicMessages = append(anthropicMessages,
					anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)),
				)
			}
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				// ADR-095 D6/D8: echo this turn's signed thinking blocks
				// byte-exact and in order, BEFORE the text/tool_use blocks —
				// Anthropic rejects a thinking-enabled continuation whose
				// assistant tool_use turn does not start with its thinking
				// blocks. The echo is unconditional (D8.5 one-directional):
				// existing blocks round-trip whether or not THIS request asks
				// for new thinking; only the thinking REQUEST below depends
				// on the resolved effort.
				blocks = appendThinkingBlocks(blocks, msg.ThinkingBlocks)
				if msg.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				for _, tc := range msg.ToolCalls {
					// Skip tool calls with empty names to avoid API errors
					if tc.Name == "" {
						continue
					}
					// OUTBOUND rebuild: this re-serialises OUR OWN history back
					// into an Anthropic request, so unlike the inbound decode in
					// parseResponse a failure here is an internal invariant
					// violation, not a truncated upstream reply. It stays
					// non-fatal — refusing to rebuild would make one bad history
					// entry permanently unsendable, which is the wedge described
					// in common.ErrToolArgumentsUndecodable — but it no longer
					// happens in silence. Before, the error was discarded
					// outright and an empty arguments block was sent as if the
					// model had called the tool with no parameters.
					args := tc.Arguments
					if args == nil && tc.Function != nil && tc.Function.Arguments != "" {
						decoded, err := common.DecodeToolCallArguments(
							json.RawMessage(tc.Function.Arguments), tc.Name,
						)
						if err != nil {
							logger.ErrorCF(
								"anthropic",
								"stored tool call arguments will not decode when rebuilding the request; "+
									"sending an empty arguments block",
								map[string]any{"tool": tc.Name, "id": tc.ID, "error": err.Error()},
							)
						} else {
							args = decoded
						}
					}
					if args == nil {
						args = map[string]any{}
					}
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, args, tc.Name))
				}
				anthropicMessages = append(anthropicMessages, anthropic.NewAssistantMessage(blocks...))
			} else {
				// Plain-text assistant turn — same D6 echo, blocks preceding
				// the text block. When there are neither blocks nor content
				// the historical single-empty-text-block shape is kept.
				var blocks []anthropic.ContentBlockParamUnion
				blocks = appendThinkingBlocks(blocks, msg.ThinkingBlocks)
				if msg.Content != "" || len(blocks) == 0 {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				anthropicMessages = append(anthropicMessages,
					anthropic.NewAssistantMessage(blocks...),
				)
			}
		case "tool":
			anthropicMessages = append(anthropicMessages,
				anthropic.NewUserMessage(anthropicToolResult(msg)),
			)
		}
	}

	maxTokens := int64(4096)
	if mt, ok := options["max_tokens"].(int); ok {
		maxTokens = int64(mt)
	}

	// Normalize model ID: Anthropic API uses hyphens (claude-sonnet-4-6),
	// but config may use dots (claude-sonnet-4.6).
	apiModel := strings.ReplaceAll(model, ".", "-")

	params := anthropic.MessageNewParams{
		Model:     apiModel,
		Messages:  anthropicMessages,
		MaxTokens: maxTokens,
	}

	if len(system) > 0 {
		params.System = system
	}

	if temp, ok := options["temperature"].(float64); ok {
		params.Temperature = anthropic.Float(temp)
	}

	if len(tools) > 0 {
		params.Tools = translateTools(tools)
	}

	// ADR-095 D9/D24/D32: the effort-request mapping, keyed on the
	// reasoning_effort option the C5 resolver sets. A recognized named level
	// requests Anthropic's ADAPTIVE thinking mode (thinking.type = "adaptive",
	// never the older budget_tokens mechanism) paired with the named level on
	// output_config.effort; absent / "default" / unrecognized sends NEITHER
	// field — the provider default applies, no thinking request at all.
	// D18: whenever thinking is requested the display pin is "summarized",
	// unconditionally — the per-login show_thinking toggle gates DISPLAY and
	// never enters the adapter's options.
	if effort, ok := options["reasoning_effort"].(string); ok && effortLevelToOutputConfig(effort) != "" {
		if historyCarriesAssistantThinkingBlocks(messages) {
			params.Thinking = anthropic.ThinkingConfigParamUnion{
				OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{
					Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized,
				},
			}
			params.OutputConfig = anthropic.OutputConfigParam{
				Effort: anthropic.OutputConfigEffort(effort),
			}
		} else {
			// ADR-095 D8.5 availability guard: a pre-feature / cross-provider /
			// hook-edited history has an assistant tool_use turn with no
			// thinking blocks to echo; Anthropic would reject the request.
			// Omit the thinking config — graceful degradation, never a failed
			// turn — and log the omission under one named field so it is
			// never silent (spec Section 16 test 13).
			logger.InfoCF("anthropic", "thinking requested but history has no assistant thinking blocks; omitting thinking config", map[string]any{
				"thinking_omitted": true,
				"reason":           "assistant_tool_use_without_thinking_blocks",
				"reasoning_effort": effort,
				"model":            apiModel,
			})
		}
	}

	return params, nil
}

// recognizedEffortLevels are the named levels the C5 resolver's catalog
// carries, mapped to Anthropic's output_config.effort enum (five levels —
// low/medium/high/xhigh/max). Anything else is treated as unset (D24: never
// guess an effort from an unrecognized value).
var recognizedEffortLevels = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
	"xhigh":  true,
	"max":    true,
}

// effortLevelToOutputConfig returns the level unchanged when it is one of the
// recognized named levels, "" otherwise (so callers can treat any non-empty
// result as "thinking requested this turn").
func effortLevelToOutputConfig(level string) string {
	if recognizedEffortLevels[level] {
		return level
	}
	return ""
}

// historyCarriesAssistantThinkingBlocks reports whether every assistant
// message in the request that could need its thinking blocks echoed actually
// carries them. Anthropic requires the assistant tool_use turn of a
// thinking-enabled request to start with the thinking blocks it produced; a
// history where such a turn has none (pre-feature session, cross-provider
// fallback, hook edit) cannot take a thinking-enabled request, so the adapter
// degrades by omitting the thinking config (ADR-095 D8.5).
func historyCarriesAssistantThinkingBlocks(messages []Message) bool {
	for _, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 && len(msg.ThinkingBlocks) == 0 {
			return false
		}
	}
	return true
}

// appendThinkingBlocks rebuilds the carrier's blocks into Anthropic's wire
// params, byte-exact and in order: "thinking" carries text + signature,
// "redacted_thinking" its opaque data (ADR-095 D6/D8 — the wholesale
// round-trip, no strip step).
func appendThinkingBlocks(blocks []anthropic.ContentBlockParamUnion, tbs []ThinkingBlock) []anthropic.ContentBlockParamUnion {
	for _, tb := range tbs {
		if tb.Type == "redacted_thinking" {
			blocks = append(blocks, anthropic.ContentBlockParamUnion{
				OfRedactedThinking: &anthropic.RedactedThinkingBlockParam{Data: tb.Data},
			})
			continue
		}
		blocks = append(blocks, anthropic.ContentBlockParamUnion{
			OfThinking: &anthropic.ThinkingBlockParam{
				Thinking:  tb.Thinking,
				Signature: tb.Signature,
			},
		})
	}
	return blocks
}

func anthropicToolResult(msg Message) anthropic.ContentBlockParamUnion {
	if len(msg.Media) == 0 {
		return anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false)
	}
	content := []anthropic.ToolResultBlockParamContentUnion{{OfText: &anthropic.TextBlockParam{Text: msg.Content}}}
	unsupported, unsupportedFormat, malformed := 0, 0, 0
	for _, dataURL := range msg.Media {
		if !strings.HasPrefix(dataURL, "data:image/") {
			unsupported++
			continue
		}
		payload := strings.TrimPrefix(dataURL, "data:")
		meta, data, ok := strings.Cut(payload, ",")
		if !ok || !strings.HasSuffix(meta, ";base64") || data == "" {
			malformed++
			continue
		}
		mediaType, _, _ := strings.Cut(meta, ";")
		switch mediaType {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			unsupportedFormat++
			continue
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			malformed++
			continue
		}
		content = append(content, anthropic.ToolResultBlockParamContentUnion{OfImage: &anthropic.ImageBlockParam{Source: anthropic.ImageBlockParamSourceUnion{OfBase64: &anthropic.Base64ImageSourceParam{Data: data, MediaType: anthropic.Base64ImageSourceMediaType(mediaType)}}}})
	}
	if unsupported > 0 || unsupportedFormat > 0 || malformed > 0 {
		parts := make([]string, 0, 3)
		if unsupported > 0 {
			parts = append(parts, fmt.Sprintf("unsupported media type (%d)", unsupported))
		}
		if malformed > 0 {
			parts = append(parts, fmt.Sprintf("malformed image data (%d)", malformed))
		}
		if unsupportedFormat > 0 {
			parts = append(parts, fmt.Sprintf("unsupported image format (%d)", unsupportedFormat))
		}
		warning := "[Tool-result media omitted for Anthropic: " + strings.Join(parts, ", ") + ". Re-read the attachment in a supported image format.]"
		content = append(content, anthropic.ToolResultBlockParamContentUnion{OfText: &anthropic.TextBlockParam{Text: warning}})
		logger.WarnCF("anthropic", "tool-result media omitted", map[string]any{
			"unsupported_media_type_count":   unsupported,
			"unsupported_image_format_count": unsupportedFormat,
			"malformed_image_data_count":     malformed,
		})
	}
	return anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{ToolUseID: msg.ToolCallID, Content: content}}
}

func translateTools(tools []ToolDefinition) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		tool := anthropic.ToolParam{
			Name: t.Function.Name,
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: t.Function.Parameters["properties"],
			},
		}
		if desc := t.Function.Description; desc != "" {
			tool.Description = anthropic.String(desc)
		}
		if req, ok := t.Function.Parameters["required"].([]any); ok {
			required := make([]string, 0, len(req))
			for _, r := range req {
				if s, ok := r.(string); ok {
					required = append(required, s)
				}
			}
			tool.InputSchema.Required = required
		}
		result = append(result, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return result
}

func parseResponse(resp *anthropic.Message) (*LLMResponse, error) {
	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls []ToolCall

	// PromptTokens = plain (uncached) input; cache tokens are tracked separately.
	// TotalTokens = plain input + cache_creation + cache_read + output.
	// Computed up front (resp.Usage/resp.StopReason are top-level fields, not
	// dependent on the content-block loop below) so a tool-call decode
	// failure can attach this same evidence to the refusal (ADR-087 D3.9 /
	// D5 / D7) instead of returning bare.
	cacheWrite := int(resp.Usage.CacheCreationInputTokens)
	cacheRead := int(resp.Usage.CacheReadInputTokens)
	promptTokens := int(resp.Usage.InputTokens)
	completionTokens := int(resp.Usage.OutputTokens)
	total := promptTokens + cacheWrite + cacheRead + completionTokens
	usage := &UsageInfo{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CacheWriteTokens: cacheWrite,
		CacheReadTokens:  cacheRead,
		TotalTokens:      total,
		// ThinkingTokens is Anthropic's output_tokens_details.thinking_tokens —
		// a subset of output_tokens, never added on top. Stays 0 when the
		// provider does not report it (never a guessed default).
		ThinkingTokens: int(resp.Usage.OutputTokensDetails.ThinkingTokens),
	}

	// ADR-095 D6/D7: capture every signed thinking block byte-exact and in
	// response order — "thinking" with its signature, "redacted_thinking" with
	// its opaque data. The display copy below stays untouched; this is the
	// round-trip carrier the turn loop copies onto the assistant history
	// Message. Capture happens HERE only: both the streaming and non-streaming
	// paths funnel through parseResponse, and signatures never ride a
	// streaming callback (D7).
	var thinkingBlocks []ThinkingBlock
	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			tb := block.AsThinking()
			thinkingBlocks = append(thinkingBlocks, ThinkingBlock{
				Type:      "thinking",
				Thinking:  tb.Thinking,
				Signature: tb.Signature,
			})
			reasoning.WriteString(tb.Thinking)
		case "redacted_thinking":
			rb := block.AsRedactedThinking()
			thinkingBlocks = append(thinkingBlocks, ThinkingBlock{
				Type: "redacted_thinking",
				Data: rb.Data,
			})
		case "text":
			tb := block.AsText()
			content.WriteString(tb.Text)
		case "tool_use":
			tu := block.AsToolUse()
			args, err := common.DecodeToolCallArguments(tu.Input, tu.Name)
			if err != nil {
				// The refused attempt's stop reason and billed usage are
				// both already in hand at this scope — attach them so the
				// caller's classifier (and cost accounting) sees real
				// evidence instead of having to guess from the fragment
				// alone (ADR-087 D3.9 / D5 / D7). resp.StopReason's raw
				// string spelling ("max_tokens") already matches the
				// normalised spelling AttachToolArgumentsEvidence looks
				// for, so it is passed through unmapped.
				return nil, common.AttachToolArgumentsEvidence(err, string(resp.StopReason), usage)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:        tu.ID,
				Name:      tu.Name,
				Arguments: args,
			})
		}
	}

	finishReason := "stop"
	switch resp.StopReason {
	case anthropic.StopReasonToolUse:
		finishReason = "tool_calls"
	case anthropic.StopReasonMaxTokens:
		finishReason = "length"
	case anthropic.StopReasonEndTurn:
		finishReason = "stop"
	case anthropic.StopReasonStopSequence:
		finishReason = "stop"
	case anthropic.StopReasonPauseTurn:
		// Long-running turn (e.g. server-side tool use) paused mid-flight;
		// the caller is expected to continue with the same request. This is
		// NOT a completed turn, so it must not be coerced to "stop".
		finishReason = "pause_turn"
	case anthropic.StopReasonRefusal:
		// Model declined to generate content for safety reasons; treat like
		// the other providers' content-policy rejection (bedrock uses the
		// same "content_filter" value for its analogous case).
		finishReason = "content_filter"
	}

	return &LLMResponse{
		Content:        content.String(),
		Reasoning:      reasoning.String(),
		ToolCalls:      toolCalls,
		FinishReason:   finishReason,
		Usage:          usage,
		ThinkingBlocks: thinkingBlocks,
	}, nil
}

func normalizeBaseURL(apiBase string) string {
	base := strings.TrimSpace(apiBase)
	if base == "" {
		return defaultBaseURL
	}

	base = strings.TrimRight(base, "/")
	if before, ok := strings.CutSuffix(base, "/v1"); ok {
		base = before
	}
	if base == "" {
		return defaultBaseURL
	}

	return base
}
