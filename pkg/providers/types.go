package providers

import (
	"context"
	"fmt"

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
	ContentBlock           = protocoltypes.ContentBlock
	ToolCallProgress       = protocoltypes.ToolCallProgress
	OnToolCallProgress     = protocoltypes.OnToolCallProgress
	CacheControl           = protocoltypes.CacheControl
	// ToolChoice, ToolChoiceMode re-export protocoltypes' typed tool-choice
	// forcing value (ADR-081 D3 [G-B1]) so callers of this package (e.g. the
	// agent loop) never need to import pkg/providers/protocoltypes directly.
	ToolChoice     = protocoltypes.ToolChoice
	ToolChoiceMode = protocoltypes.ToolChoiceMode
)

const (
	ToolChoiceAuto      = protocoltypes.ToolChoiceAuto
	ToolChoiceRequired  = protocoltypes.ToolChoiceRequired
	OptionKeyToolChoice = protocoltypes.OptionKeyToolChoice
)

// ResolveToolChoice re-exports protocoltypes.ResolveToolChoice — see its
// doc comment for the resolution/WARN contract.
func ResolveToolChoice(options map[string]any, providerName string) (ToolChoice, bool) {
	return protocoltypes.ResolveToolChoice(options, providerName)
}

type LLMProvider interface {
	Chat(
		ctx context.Context,
		messages []Message,
		tools []ToolDefinition,
		model string,
		options map[string]any,
	) (*LLMResponse, error)
	GetDefaultModel() string
}

type StatefulProvider interface {
	LLMProvider
	Close()
}

// StreamingProvider is an optional interface for providers that support token streaming.
// onChunk receives the accumulated text so far (not individual deltas).
// The returned LLMResponse is the same complete response for compatibility with tool-call handling.
//
// onProgress is a PER-CALL parameter, deliberately not a setter on the
// provider (ADR-059 D1). AgentInstance.Provider is a single shared pointer
// used concurrently by every turn running on THAT agent — parallel
// delegations to the same target, and a self-delegating sub-turn alongside its
// parent, all reach the same value. (A cross-agent delegation does not: per
// ADR-032 the sub-turn runs as the target agent, whose provider is a different
// object.) A SetProgressHandler-style capability would therefore be
// last-writer-wins wherever the pointer IS shared: two concurrent delegations
// would silently report each other's progress, which is worse than reporting
// none. Passing it down the call stack keeps it bound to the one request that
// asked for it.
//
// Both callbacks may be nil; a nil onProgress means the caller does not want
// tool-argument progress and costs the provider nothing.
type StreamingProvider interface {
	ChatStream(
		ctx context.Context,
		messages []Message,
		tools []ToolDefinition,
		model string,
		options map[string]any,
		onChunk func(accumulated string),
		onProgress OnToolCallProgress,
	) (*LLMResponse, error)
}

// ThinkingCapable is an optional interface for providers that support
// extended thinking (e.g. Anthropic). Used by the agent loop to warn
// when thinking_level is configured but the active provider cannot use it.
type ThinkingCapable interface {
	SupportsThinking() bool
}

// NativeSearchCapable is an optional interface for providers that support
// built-in web search during LLM inference (e.g. OpenAI web_search_preview,
// xAI Grok search). When the active provider implements this interface and
// returns true, the agent loop can hide the client-side web_search tool to
// avoid duplicate search surfaces and use the provider's native search instead.
type NativeSearchCapable interface {
	SupportsNativeSearch() bool
}

// ToolChoiceForcingCapable is an optional interface for providers that
// support REQUEST-SHAPE tool_choice forcing (ADR-081 D3 Layer 1: narrowing
// to an exact tool pair with tool_choice=required). Review-round-1 finding
// #12: the agent loop's isCLIBridgedProvider used to check a hardcoded
// concrete-type switch (*CodexCliProvider, *CopilotCliProvider), which
// misses provider-pool fallback candidates and any future CLI-bridged
// wrapper — this interface lets a provider self-declare instead. A provider
// that does NOT implement this interface is treated as forcing-capable by
// default (SupportsToolChoiceForcing's absence, not its value, is the
// "capable" signal — mirrors ThinkingCapable/NativeSearchCapable's own
// opt-in shape); only a provider that explicitly implements it and returns
// false is treated as unable to force. CodexCliProvider and
// CopilotCliProvider both implement this returning false (they flatten
// tools into prompt text — no request-shape tool_choice field exists to
// set, spec FR-009). Their own per-request no-op+WARN when a caller sets
// tool_choice=required anyway (ResolveToolChoice + slog.Warn in both
// providers' Chat) remains the last line of defense for a provider-pool
// fallback that reaches one of them despite the engine's own check here.
type ToolChoiceForcingCapable interface {
	SupportsToolChoiceForcing() bool
}

// FailoverReason classifies why an LLM request failed for fallback decisions.
type FailoverReason string

const (
	FailoverAuth            FailoverReason = "auth"
	FailoverRateLimit       FailoverReason = "rate_limit"
	FailoverBilling         FailoverReason = "billing"
	FailoverTimeout         FailoverReason = "timeout"
	FailoverFormat          FailoverReason = "format"
	FailoverContextOverflow FailoverReason = "context_overflow"
	FailoverOverloaded      FailoverReason = "overloaded"
	FailoverUnknown         FailoverReason = "unknown"
)

// FailoverError wraps an LLM provider error with classification metadata.
type FailoverError struct {
	Reason   FailoverReason
	Provider string
	Model    string
	Status   int
	Wrapped  error
}

func (e *FailoverError) Error() string {
	return fmt.Sprintf("failover(%s): provider=%s model=%s status=%d: %v",
		e.Reason, e.Provider, e.Model, e.Status, e.Wrapped)
}

func (e *FailoverError) Unwrap() error {
	return e.Wrapped
}

// IsRetriable returns true if this error should trigger fallback to next candidate.
// Non-retriable: Format errors (bad request structure, image dimension/size).
func (e *FailoverError) IsRetriable() bool {
	return e.Reason != FailoverFormat && e.Reason != FailoverContextOverflow
}

// ModelConfig holds primary model and fallback list.
type ModelConfig struct {
	Primary   string
	Fallbacks []string
}
