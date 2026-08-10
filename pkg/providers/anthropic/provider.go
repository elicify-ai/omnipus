package anthropicprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/elicify-ai/omnipus/pkg/logger"
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
	defaultBaseURL      = "https://api.anthropic.com"
	anthropicBetaHeader = "oauth-2025-04-20"
)

type Provider struct {
	client      *anthropic.Client
	tokenSource func() (string, error)
	baseURL     string
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

	return parseResponse(resp), nil
}

// ChatStream implements providers.StreamingProvider.
//
// Before this existed the Provider did not satisfy StreamingProvider at all,
// so the agent loop's type assertion failed and EVERY Anthropic-backed call
// — including delegated sub-turns — went down the non-streaming path. That
// made the whole response a black box: no partial text, no tool-argument
// progress, nothing to distinguish a model still working from one that had
// hung. Delegated workers were killed on that ambiguity.
func (p *Provider) ChatStream(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(accumulated string),
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

	return p.streamWithCallbacks(ctx, params, opts, onChunk,
		protocoltypes.ToolCallProgressFromOptions(options))
}

func (p *Provider) chatStreaming(
	ctx context.Context,
	params anthropic.MessageNewParams,
	opts []option.RequestOption,
) (*LLMResponse, error) {
	return p.streamWithCallbacks(ctx, params, opts, nil, nil)
}

// streamWithCallbacks consumes the SSE stream, accumulating into a Message
// exactly as before, and additionally reports forward progress.
//
// Progress is derived from the ACCUMULATED message after each event rather
// than by decoding delta union types. That keeps this robust across SDK
// revisions: whatever shape the deltas take, the accumulated content blocks
// are the same ones parseResponse already reads.
func (p *Provider) streamWithCallbacks(
	ctx context.Context,
	params anthropic.MessageNewParams,
	opts []option.RequestOption,
	onChunk func(accumulated string),
	onProgress protocoltypes.OnToolCallProgress,
) (*LLMResponse, error) {
	stream := p.client.Messages.NewStreaming(ctx, params, opts...)
	defer stream.Close()

	var msg anthropic.Message
	var lastTextLen int
	lastArgsLen := map[int]int{}

	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			return nil, fmt.Errorf("claude streaming accumulate: %w", err)
		}
		if onChunk == nil && onProgress == nil {
			continue
		}

		// One pass to measure, then emit — so TotalArgsBytes is the true
		// total across all blocks rather than a running partial.
		var text strings.Builder
		argsLen := make(map[int]int, len(msg.Content))
		names := make(map[int]string, len(msg.Content))
		totalArgs := 0
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
		if onProgress != nil {
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
				})
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("claude API call: %w", err)
	}

	return parseResponse(&msg), nil
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
					anthropic.NewUserMessage(anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false)),
				)
			} else {
				anthropicMessages = append(anthropicMessages,
					anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)),
				)
			}
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				if msg.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				for _, tc := range msg.ToolCalls {
					// Skip tool calls with empty names to avoid API errors
					if tc.Name == "" {
						continue
					}
					args := tc.Arguments
					if args == nil && tc.Function != nil && tc.Function.Arguments != "" {
						if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
							args = map[string]any{}
						}
					}
					if args == nil {
						args = map[string]any{}
					}
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, args, tc.Name))
				}
				anthropicMessages = append(anthropicMessages, anthropic.NewAssistantMessage(blocks...))
			} else {
				anthropicMessages = append(anthropicMessages,
					anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content)),
				)
			}
		case "tool":
			anthropicMessages = append(anthropicMessages,
				anthropic.NewUserMessage(anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false)),
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

	// Extended Thinking / Adaptive Thinking
	// The thinking_level value directly determines the API parameter format:
	//   "adaptive" → {thinking: {type: "adaptive"}} + output_config.effort
	//   "low/medium/high/xhigh" → {thinking: {type: "enabled", budget_tokens: N}}
	if level, ok := options["thinking_level"].(string); ok && level != "" && level != "off" {
		applyThinkingConfig(&params, level)
	}

	return params, nil
}

// applyThinkingConfig sets thinking parameters based on the level value.
// "adaptive" uses the adaptive thinking API (Claude 4.6+).
// All other levels use budget_tokens which is universally supported.
//
// Anthropic API constraint: temperature must not be set when thinking is enabled.
// budget_tokens must be strictly less than max_tokens.
func applyThinkingConfig(params *anthropic.MessageNewParams, level string) {
	// Anthropic API rejects requests with temperature set alongside thinking.
	// Reset to zero value (omitted from JSON serialization).
	if params.Temperature.Valid() {
		logger.DebugCF("anthropic", "temperature cleared because thinking is enabled", map[string]any{"level": level})
	}
	params.Temperature = anthropic.MessageNewParams{}.Temperature

	if level == "adaptive" {
		adaptive := anthropic.ThinkingConfigAdaptiveParam{}
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
		params.OutputConfig = anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffortHigh,
		}
		return
	}

	budget := int64(levelToBudget(level))
	if budget <= 0 {
		return
	}

	// budget_tokens must be < max_tokens; clamp to respect user's max_tokens setting.
	if budget >= params.MaxTokens {
		logger.WarnCF("anthropic", "budget_tokens clamped to max_tokens-1", map[string]any{
			"budget_tokens": budget,
			"clamped_to":    params.MaxTokens - 1,
		})
		budget = params.MaxTokens - 1
	} else if budget > params.MaxTokens*80/100 {
		logger.WarnCF("anthropic", "thinking budget exceeds 80% of max_tokens, output may be truncated", map[string]any{
			"budget_tokens": budget,
			"max_tokens":    params.MaxTokens,
		})
	}
	params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
}

// levelToBudget maps a thinking level to budget_tokens.
// Values are based on Anthropic's recommendations and community best practices:
//
//	low    =  4,096  — simple reasoning, quick debugging (Claude Code "think")
//	medium = 16,384  — Anthropic recommended sweet spot for most tasks
//	high   = 32,000  — complex architecture, deep analysis (diminishing returns above this)
//	xhigh  = 64,000  — extreme reasoning, research problems, benchmarks
//
// Note: For Claude 4.6+, prefer adaptive thinking over manual budget_tokens.
func levelToBudget(level string) int {
	switch level {
	case "low":
		return 4096
	case "medium":
		return 16384
	case "high":
		return 32000
	case "xhigh":
		return 64000
	default:
		return 0
	}
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

func parseResponse(resp *anthropic.Message) *LLMResponse {
	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls []ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			tb := block.AsThinking()
			reasoning.WriteString(tb.Thinking)
		case "text":
			tb := block.AsText()
			content.WriteString(tb.Text)
		case "tool_use":
			tu := block.AsToolUse()
			var args map[string]any
			if err := json.Unmarshal(tu.Input, &args); err != nil {
				logger.WarnCF("anthropic", "failed to decode tool call input", map[string]any{
					"tool":  tu.Name,
					"error": err.Error(),
				})
				args = map[string]any{"raw": string(tu.Input)}
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
		Reasoning:    reasoning.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage: &UsageInfo{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			CacheWriteTokens: cacheWrite,
			CacheReadTokens:  cacheRead,
			TotalTokens:      total,
		},
	}
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
