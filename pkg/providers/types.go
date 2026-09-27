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
)

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
// onReasoning receives the accumulated reasoning display text so far (not
// individual deltas) on each reasoning-bearing delta — display text only, no
// signature or ciphertext data.
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
// All three callbacks may be nil; a nil onProgress means the caller does not
// want tool-argument progress and costs the provider nothing. A nil
// onReasoning means the caller does not want live reasoning text and costs
// the provider nothing.
type StreamingProvider interface {
	ChatStream(
		ctx context.Context,
		messages []Message,
		tools []ToolDefinition,
		model string,
		options map[string]any,
		onChunk func(accumulated string),
		onProgress OnToolCallProgress,
		onReasoning func(accumulated string),
	) (*LLMResponse, error)
}

// ThinkingCapable is an optional interface for providers that support a
// provider-native thinking/reasoning mode (e.g. Anthropic). Callers use it
// to ask whether the active provider can act on a thinking request at all,
// independent of any particular effort/control mechanism.
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
