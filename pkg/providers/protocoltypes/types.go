package protocoltypes

type ToolCall struct {
	ID               string         `json:"id"`
	Type             string         `json:"type,omitempty"`
	Function         *FunctionCall  `json:"function,omitempty"`
	Name             string         `json:"-"`
	Arguments        map[string]any `json:"-"`
	ThoughtSignature string         `json:"-"` // Internal use only
	ExtraContent     *ExtraContent  `json:"extra_content,omitempty"`
}

type ExtraContent struct {
	Google *GoogleExtra `json:"google,omitempty"`
}

type GoogleExtra struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

type FunctionCall struct {
	Name             string `json:"name"`
	Arguments        string `json:"arguments"`
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// ThinkingBlock is one signed reasoning block from Anthropic's Messages API
// (ADR-095 D6): "thinking" carries the display text plus the signature the
// provider requires on the next request; "redacted_thinking" carries only
// opaque data. Every field serializes — D8 forbids a json:"-" strip step here
// (explicitly unlike ToolCall.ThoughtSignature, whose precedent must NOT be
// copied): a block that lost its signature could never round-trip.
type ThinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

type LLMResponse struct {
	Content          string            `json:"content"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	FinishReason     string            `json:"finish_reason"`
	Usage            *UsageInfo        `json:"usage,omitempty"`
	Reasoning        string            `json:"reasoning"`
	ReasoningDetails []ReasoningDetail `json:"reasoning_details"`
	// ThinkingBlocks carries the signed thinking/redacted_thinking blocks
	// exactly as the provider returned them (byte-exact, order-stable) so the
	// turn loop can copy them onto the assistant history Message for the next
	// request's round-trip (ADR-095 D6/D7). Empty when the response had none.
	ThinkingBlocks []ThinkingBlock `json:"thinking_blocks,omitempty"`
}

type ReasoningDetail struct {
	Format string `json:"format"`
	Index  int    `json:"index"`
	Type   string `json:"type"`
	Text   string `json:"text"`
}

// UsageInfo records token counts for one LLM call.
//
// Token accounting convention:
//   - PromptTokens    = UNCACHED input tokens only (new tokens sent to the model).
//   - CacheReadTokens = input tokens served from the provider's KV cache
//     (Anthropic: cache_read_input_tokens; OpenAI: prompt_tokens_details.cached_tokens).
//   - CacheWriteTokens = input tokens written into a new cache entry
//     (Anthropic: cache_creation_input_tokens; OpenAI: not reported, stays 0).
//   - CompletionTokens = output tokens.
//   - TotalTokens = PromptTokens + CacheReadTokens + CacheWriteTokens + CompletionTokens.
//
// Providers that previously collapsed cache tokens into PromptTokens now keep them
// separate. Callers must use TotalTokens for the full usage count; summing
// PromptTokens + CompletionTokens will UNDER-count when caching is active.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// CacheReadTokens holds tokens served from the provider's prompt cache
	// (not re-computed). Populated when the provider reports them; 0 otherwise.
	CacheReadTokens int `json:"cache_read_tokens,omitempty"`
	// CacheWriteTokens holds tokens written into a new cache entry this call.
	// Populated when the provider reports them (Anthropic only); 0 otherwise.
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	// ThinkingTokens holds the reasoning ("thinking") token count when the
	// provider reports one (OpenAI-compatible:
	// completion_tokens_details.reasoning_tokens); 0 otherwise — never a
	// guessed default. A SUBSET of CompletionTokens, never added on top of it.
	ThinkingTokens int `json:"thinking_tokens,omitempty"`
}

// CacheControl marks a content block for LLM-side prefix caching.
// Currently only "ephemeral" is supported (used by Anthropic).
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// ContentBlock represents a structured segment of a system message.
// Adapters that understand SystemParts can use these blocks to set
// per-block cache control (e.g. Anthropic's cache_control: ephemeral).
type ContentBlock struct {
	Type         string        `json:"type"` // "text"
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type Message struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	Media            []string       `json:"media,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	SystemParts      []ContentBlock `json:"system_parts,omitempty"` // structured system blocks for cache-aware adapters
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	// ThinkingBlocks carries the signed thinking blocks this assistant turn
	// produced (Anthropic). They serialize wholesale — no strip step (D8) —
	// and round-trip to the provider on the next request byte-exact and in
	// order, thinking blocks preceding text/tool_use blocks (D6).
	ThinkingBlocks []ThinkingBlock `json:"thinking_blocks,omitempty"`
}

type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

type ToolFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
