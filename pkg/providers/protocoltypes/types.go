package protocoltypes

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

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

type LLMResponse struct {
	Content          string            `json:"content"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	FinishReason     string            `json:"finish_reason"`
	Usage            *UsageInfo        `json:"usage,omitempty"`
	Reasoning        string            `json:"reasoning"`
	ReasoningDetails []ReasoningDetail `json:"reasoning_details"`
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

// ToolChoiceMode selects how a provider should treat tool/function calling
// for a single request (ADR-081 D3 [G-B1]).
type ToolChoiceMode string

const (
	// ToolChoiceAuto lets the model decide whether to call a tool — the
	// behavior every native builder already had before this type existed.
	ToolChoiceAuto ToolChoiceMode = "auto"
	// ToolChoiceRequired forces the model to call one of the offered tools.
	// Model-specific single-tool forcing (naming exactly one tool by name)
	// is deliberately NOT represented: ADR-081 D3 narrows the offered tool
	// list to the exact set the caller wants forced, rather than asking a
	// provider to pin one tool while other tools are still on offer.
	ToolChoiceRequired ToolChoiceMode = "required"
)

// ToolChoice is the typed tool-choice-forcing value threaded through
// LLMProvider.Chat/ChatStream's options map under OptionKeyToolChoice
// (ADR-081 D3 [G-B1], threading-mechanism choice (a): a well-known options
// key whose VALUE is a typed struct, resolved by a checked type assertion
// that WARNs on mismatch — see ResolveToolChoice). This replaces a bare
// options["tool_choice"] string key, whose typo would silently degrade
// every builder to its own default instead of failing anywhere: a caller
// must construct a ToolChoice value, so passing the wrong Go type is caught
// by ResolveToolChoice's WARN-and-degrade path, and an unrecognized Mode
// value is caught the same way, instead of a misspelled string sailing
// straight into a wire request unnoticed.
type ToolChoice struct {
	Mode ToolChoiceMode
}

// OptionKeyToolChoice is the options map key under which a ToolChoice value
// is threaded through LLMProvider.Chat/ChatStream (ADR-081 D3 [G-B1]).
const OptionKeyToolChoice = "tool_choice"

// ResolveToolChoice reads a typed ToolChoice from options under
// OptionKeyToolChoice. ok is true only when the key holds a ToolChoice
// value with a recognized Mode; every native builder is expected to treat
// ok=false as "leave tool_choice at this provider's pre-ADR-081 default"
// (typically omitted/auto), never as an error.
//
// Absence of the key is silent and does not log: it is overwhelmingly the
// common case (tool_choice is only set on a goal turn's forced first move,
// ADR-081 D3), and warning on "not present" would spam every ordinary
// request on every other turn. A key PRESENT with the wrong Go type, or a
// Mode this package does not recognize, is different — that is always a
// caller bug (a stale string, a copy-paste typo, a value from a future
// schema version) — and is logged at WARN naming providerName so an
// operator can trace which builder saw it, then degrades to ok=false
// rather than panicking or erroring the whole request.
func ResolveToolChoice(options map[string]any, providerName string) (ToolChoice, bool) {
	raw, present := options[OptionKeyToolChoice]
	if !present {
		return ToolChoice{}, false
	}
	tc, ok := raw.(ToolChoice)
	if !ok {
		logger.WarnCF(providerName, "tool_choice option has unexpected type; degrading to provider default", map[string]any{
			"type": fmt.Sprintf("%T", raw),
		})
		return ToolChoice{}, false
	}
	switch tc.Mode {
	case ToolChoiceAuto, ToolChoiceRequired:
		return tc, true
	default:
		logger.WarnCF(providerName, "tool_choice option has unrecognized mode; degrading to provider default", map[string]any{
			"mode": string(tc.Mode),
		})
		return ToolChoice{}, false
	}
}
