package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

const artifactPathsLLMNote = "Use `send_file` with one of these paths to send it to the user, or use file/exec tools to save it inside the workspace if requested."

// ToolResult represents the structured return value from tool execution.
// It provides clear semantics for different types of results and supports
// async operations, user-facing messages, and error handling.
type ToolResult struct {
	// ForLLM is the content sent to the LLM for context.
	// Required for all results.
	ForLLM string `json:"for_llm"`

	// ForUser is the content sent directly to the user.
	// If empty, no user message is sent.
	// Silent=true overrides this field.
	ForUser string `json:"for_user,omitempty"`

	// Silent suppresses sending any message to the user.
	// When true, ForUser is ignored even if set.
	Silent bool `json:"silent"`

	// IsError indicates whether the tool execution failed.
	// When true, the result should be treated as an error.
	IsError bool `json:"is_error"`

	// Async indicates whether the tool is running asynchronously.
	// When true, the tool will complete later and notify via callback.
	Async bool `json:"async"`

	// Err is the underlying error (not JSON serialized).
	// Used for internal error handling and logging.
	Err error `json:"-"`

	// Media contains media store refs produced by this tool.
	// When non-empty, the agent will publish these as OutboundMediaMessage.
	Media []string `json:"media,omitempty"`

	// Messages holds the ephemeral session history after execution.
	// Only populated by SubTurn executions; used by evaluator_optimizer
	// to carry stateful worker context across evaluation iterations.
	Messages []providers.Message `json:"-"`

	// ArtifactTags exposes local artifact paths back to the LLM in a structured
	// form, e.g. "[file:/tmp/example.png]". This is used when a tool produced a
	// reusable local artifact but did not deliver it to the user yet.
	ArtifactTags []string `json:"artifact_tags,omitempty"`
}

// ContentForLLM returns the normalized textual content to append to the
// conversation after a tool call. Errors fall back to Err when ForLLM is empty.
func (tr *ToolResult) ContentForLLM() string {
	if tr == nil {
		return ""
	}
	content := tr.ForLLM
	if content == "" && tr.Err != nil {
		content = tr.Err.Error()
	}
	if len(tr.ArtifactTags) > 0 {
		artifactNote := "Local artifact paths: " + strings.Join(tr.ArtifactTags, " ") + "\n" + artifactPathsLLMNote
		if content == "" {
			content = artifactNote
		} else if !strings.Contains(content, artifactNote) {
			content += "\n" + artifactNote
		}
	}
	if content != "" {
		return content
	}
	return ""
}

// NewToolResult creates a basic ToolResult with content for the LLM.
// Use this when you need a simple result with default behavior.
//
// Example:
//
//	result := NewToolResult("File updated successfully")
func NewToolResult(forLLM string) *ToolResult {
	return &ToolResult{
		ForLLM: forLLM,
	}
}

// SilentResult creates a ToolResult that is silent (no user message).
// The content is only sent to the LLM for context.
//
// Use this for operations that should not spam the user, such as:
// - File reads/writes
// - Status updates
// - Background operations
//
// Example:
//
//	result := SilentResult("Config file saved")
func SilentResult(forLLM string) *ToolResult {
	return &ToolResult{
		ForLLM:  forLLM,
		Silent:  true,
		IsError: false,
		Async:   false,
	}
}

// AsyncResult creates a ToolResult for async operations.
// The task will run in the background and complete later.
//
// Use this for long-running operations like:
// - Subagent spawns
// - Background processing
// - External API calls with callbacks
//
// Example:
//
//	result := AsyncResult("Subagent spawned, will report back")
func AsyncResult(forLLM string) *ToolResult {
	return &ToolResult{
		ForLLM:  forLLM,
		Silent:  false,
		IsError: false,
		Async:   true,
	}
}

// DelegationDenyReason classifies WHICH delegation-policy axis rejected a
// delegation/spawn/task_create call. It is the structured discriminator the SPA
// uses to render a distinct delegation-failure block instead of relying on the
// LLM to narrate the denial.
type DelegationDenyReason string

const (
	// DenyTrustSet — the target agent is not in the caller's delegation trust
	// set ('to' allowlist), or the caller has no permitted targets at all.
	DenyTrustSet DelegationDenyReason = "trust_set"
	// DenyMode — the delegation mode (await / background / task) is not permitted
	// by the caller's delegation policy.
	DenyMode DelegationDenyReason = "mode"
	// DenyDepth — the delegation-chain depth cap was reached.
	DenyDepth DelegationDenyReason = "depth"
)

// DelegationDenial is the value the policy deny-checkers return: nil means the
// delegation is ALLOWED; a non-nil value carries the structured denial.
type DelegationDenial struct {
	Reason string
	Policy DelegationDenyReason
	// TargetAgentID is the requested target, when one was named.
	TargetAgentID string
}

// validDelegationPolicy reports whether p is one of the contract's enum values
// (asyncapi.yaml DelegationFailure.policy: trust_set | mode | depth).
func validDelegationPolicy(p DelegationDenyReason) bool {
	switch p {
	case DenyTrustSet, DenyMode, DenyDepth:
		return true
	default:
		return false
	}
}

// DelegationDeniedResult builds a structured error ToolResult for a denied
// delegation. ForLLM is the JSON-encoded DelegationFailure (the generated wire
// type, contracts/asyncapi.yaml → pkg/api/generated) so both the LLM and the
// SPA receive the same typed shape; IsError is set so the frame status is
// "error". The wrapped Err preserves the reason for logging.
//
// Invariant defense: the contract requires reason (minLength:1) and a policy
// enum value. A denial that arrives with an empty reason or an unrecognized
// policy would serialize a schema-invalid payload the SPA silently drops, so we
// default a non-empty reason and a valid policy axis before marshaling.
func DelegationDeniedResult(tool string, d *DelegationDenial) *ToolResult {
	if d == nil {
		// Defensive: never produce a "denied" result without a denial.
		return ErrorResult("delegation denied")
	}
	reason := d.Reason
	if reason == "" {
		reason = "delegation denied by policy"
	}
	policy := d.Policy
	if !validDelegationPolicy(policy) {
		policy = DenyTrustSet
	}
	failure := generated.DelegationFailure{
		Error:  "delegation_denied",
		Reason: reason,
		Policy: string(policy),
		Tool:   tool,
	}
	if d.TargetAgentID != "" {
		tgt := d.TargetAgentID
		failure.TargetAgentId = &tgt
	}
	encoded, err := json.Marshal(failure)
	if err != nil {
		// Fall back to a plain text error if marshaling somehow fails.
		return ErrorResult("delegation denied: " + reason).
			WithError(fmt.Errorf("delegation policy denied (%s): %s", tool, reason))
	}
	return (&ToolResult{
		ForLLM:  string(encoded),
		IsError: true,
	}).WithError(fmt.Errorf("delegation policy denied (%s): %s", tool, reason))
}

// ErrorResult creates a ToolResult representing an error.
// Sets IsError=true and includes the error message.
//
// Example:
//
//	result := ErrorResult("Failed to connect to database: connection refused")
func ErrorResult(message string) *ToolResult {
	return &ToolResult{
		ForLLM:  message,
		Silent:  false,
		IsError: true,
		Async:   false,
	}
}

// UserResult creates a ToolResult with content for both LLM and user.
// Both ForLLM and ForUser are set to the same content.
//
// Use this when the user needs to see the result directly:
// - Command execution output
// - Fetched web content
// - Query results
//
// Example:
//
//	result := UserResult("Total files found: 42")
func UserResult(content string) *ToolResult {
	return &ToolResult{
		ForLLM:  content,
		ForUser: content,
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}

// MediaResult creates a ToolResult with media refs for the user.
// The agent will publish these refs as OutboundMediaMessage.
//
// Example:
//
//	result := MediaResult("Image generated successfully", []string{"media://abc123"})
func MediaResult(forLLM string, mediaRefs []string) *ToolResult {
	return &ToolResult{
		ForLLM: forLLM,
		Media:  mediaRefs,
	}
}

// MarshalJSON implements custom JSON serialization.
// The Err field is excluded from JSON output via the json:"-" tag.
func (tr *ToolResult) MarshalJSON() ([]byte, error) {
	type Alias ToolResult
	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(tr),
	})
}

// WithError sets the Err field and returns the result for chaining.
// This preserves the error for logging while keeping it out of JSON.
//
// Example:
//
//	result := ErrorResult("Operation failed").WithError(err)
func (tr *ToolResult) WithError(err error) *ToolResult {
	tr.Err = err
	return tr
}
