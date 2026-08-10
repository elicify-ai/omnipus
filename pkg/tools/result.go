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

	// Interrupted indicates the tool's underlying work was cut short by a
	// parent-turn cancellation (or a direct cancel targeting it) rather than
	// a genuine execution failure. Currently set only by the synchronous
	// delegate/spawn path (pkg/agent/subturn.go's spawnSubTurn cleanup defer,
	// which is the single source of truth for the classification — mirrors
	// the exact SubTurnStatusInterrupted/SubTurnStatusCancelled check used
	// for the live subagent_end frame) so pkg/agent/loop.go's tool-call-
	// transcript persistence can record status "interrupted" instead of
	// folding it into the generic IsError=true/"error" bucket every other
	// tool failure uses (Finding F / A-I4 round 5: without this, a session
	// reload read the same persisted status a genuine failure would have
	// produced, showing "failed" for a span live correctly labeled
	// "interrupted (parent canceled)"). IsError is deliberately left true
	// alongside this: the OUTER tool_call_result frame for the same call has
	// a strict success/error wire enum with no "interrupted" value, and its
	// live behavior for a canceled synchronous delegate has always been
	// "error" — this flag only enriches the SPAN's own terminal status
	// (SubagentEndFrame.status, which does support "interrupted"), it does
	// not change the outer badge.
	Interrupted bool `json:"interrupted,omitempty"`

	// ParksTurn signals that this tool call requires the ENCLOSING turn to
	// stop immediately, without being treated as an error or an abort. Set
	// exclusively by pkg/tools/message_parent.go on a successful kind=
	// question, wait=true call — the exact moment it has parked the calling
	// child's own durable LifecycleRecord into needs_input (ADR-053 §5.1,
	// parkNeedsInput). pkg/agent/loop.go's runTurn tool-execution loop is the
	// sole consumer (Correctness-C2, ADR-057 UAT 2026-08-03): on seeing this
	// flag it finishes this tool call's own bookkeeping (transcript/message
	// append) exactly as normal, marks any REMAINING queued tool calls in the
	// same LLM batch as skipped (mirroring the graceful-interrupt skip path),
	// and returns TurnEndStatusParked — deliberately WITHOUT the session
	// rollback a hard-abort performs, so the durable needs_input record and
	// the turn's own conversation history survive untouched, exactly as they
	// were at park time, ready for `delegate respond` to resume. Every other
	// tool/result constructor leaves this at its zero value (false); it must
	// never be set for a failed/rejected park attempt (message_parent.go only
	// sets it once inbox.Append AND parkNeedsInput have both already
	// succeeded).
	ParksTurn bool `json:"parks_turn,omitempty"`

	// ExitCode is the real process exit code for a shell/bash foreground
	// execution (pkg/tools/shell.go's foregroundResultFromSandbox /
	// runUnconstrained), or nil when no real exit code is available (a
	// timeout, a command blocked before it ever ran, or a non-shell tool).
	// review r2 HIGH-1: this is the AUTHORITATIVE source for a machine-check
	// criterion's verdict (pkg/agent/judge.go's interpretBashResult reads it
	// directly) — unlike ForLLM's human-readable "[Command exited with code
	// N]" text suffix, it is set directly from the real exit code and is
	// never subject to output truncation or a worker's own command echoing a
	// fake suffix into stdout/stderr. A pointer (not a bare int) because 0 is
	// a valid, meaningful exit code that must be distinguishable from "not
	// set".
	ExitCode *int `json:"exit_code,omitempty"`

	// TimedOut is set directly (never scraped from output text) by the bash
	// tool's own timeout path (pkg/tools/shell.go's foregroundResultFromSandbox
	// / runUnconstrained) when the command was killed for exceeding
	// timeout_seconds. Fix-wave finding 4: this is the AUTHORITATIVE source
	// for a machine-check criterion's timeout classification
	// (pkg/agent/judge.go's interpretBashResult reads it directly, exactly
	// like ExitCode) — unlike sniffing ForLLM for the human-readable "command
	// timed out after Ns" prose, it can never be true for a check that
	// genuinely exited 0 just because its own output happens to CONTAIN that
	// text (e.g. a log line describing an unrelated retry). Always false for
	// a non-timeout result, and false by default for producers that predate
	// this field (the prose sniff survives only as a legacy fallback for
	// that case — see interpretBashResult's doc comment).
	TimedOut bool `json:"timed_out,omitempty"`
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
	// reason and target are model-influenced (the delegate tool's arguments
	// reach them), so they are clamped for the same reason the write refusal
	// clamps its path: an unbounded payload is truncated downstream and then
	// no longer parses at all.
	reason = clampRefusalField(reason, maxRefusalReasonRunes)
	failure := generated.DelegationFailure{
		Error:  DelegationDeniedCode,
		Reason: reason,
		Policy: string(policy),
		Tool:   tool,
	}
	if d.TargetAgentID != "" {
		tgt := clampRefusalField(d.TargetAgentID, maxRefusalPathRunes)
		failure.TargetAgentId = &tgt
	}
	encoded, err := marshalWithinBudget(&failure, &failure.Reason, failure.TargetAgentId)
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

// FileExistsRefusalResult builds the structured refusal write_file returns
// when the target path already exists and overwrite was not requested.
//
// This is a PRECONDITION REFUSAL, not an I/O failure. The distinction matters
// because the two used to be indistinguishable: both arrived as prose with
// IsError set, so a worker could not tell "a sibling already wrote this, no
// action needed" from "the write broke" without matching on wording, and
// neither could the SPA.
//
// The payload is the tool's ForLLM verbatim, following DelegationDeniedResult
// (ADR-058). That is deliberate and is the whole mechanism: the calling agent
// reads ForLLM and nothing else, so a discriminator carried anywhere else —
// a Go struct field, a side channel — is invisible to a language model. An
// earlier attempt did exactly that and was deleted (ADR-059 W3).
//
// Reason keeps the original sentence, so nothing a human or a model reads is
// lost; it gains a machine-checkable tag alongside it.
func FileExistsRefusalResult(tool, path, reason string) *ToolResult {
	// Every field carries minLength:1 in the contract. A payload violating
	// that is schema-invalid, and the SPA drops it — leaving the caller with
	// nothing at all, which is strictly worse than the prose this replaces.
	if reason == "" {
		reason = "file already exists"
	}
	if tool == "" {
		tool = "write_file"
	}
	if path == "" {
		path = "(unknown path)"
	}
	// Bound the two caller-supplied strings so the ENCODED payload cannot
	// exceed the 2000-rune cap applied downstream (maxFailClosedOutputChars
	// when persisted, maxLiveErrorChars on the live frame).
	//
	// This is not cosmetic. The gateway recovers the discriminator with a
	// whole-JSON json.Unmarshal, so a severed payload does not degrade — it
	// stops parsing entirely, and replay then renders the truncated JSON
	// fragment verbatim where the live view showed a sentence. That is the
	// exact regression W5 exists to prevent, and it is reachable: the path
	// appears TWICE (once in path, once inside reason), so a ~950-character
	// path already overflows the budget. Under Linux's 4096-byte PATH_MAX
	// that is an ordinary deeply-nested workspace path, not a pathological
	// one. Prefix-positioning the discriminator does not help here, because
	// nothing downstream does a prefix match.
	path = clampRefusalField(path, maxRefusalPathRunes)
	reason = clampRefusalField(reason, maxRefusalReasonRunes)
	tool = clampRefusalField(tool, maxRefusalToolRunes)
	refusal := generated.FileExistsRefusal{
		Error:  FileExistsRefusalCode,
		Reason: reason,
		Tool:   tool,
		Path:   path,
	}
	encoded, err := marshalWithinBudget(&refusal, &refusal.Path, &refusal.Reason)
	if err != nil {
		// The contract requires non-empty reason/tool/path; a marshal failure
		// here would ship a schema-invalid payload the SPA drops, leaving the
		// caller with nothing. Prose is worse than the tag but far better than
		// silence.
		return ErrorResult(reason)
	}
	return (&ToolResult{
		ForLLM:  string(encoded),
		IsError: true,
	}).WithError(fmt.Errorf("%s: %s", tool, reason))
}

// FileExistsRefusalCode and DelegationDeniedCode are the fixed discriminators
// from the FileExistsRefusal and DelegationFailure contract schemas.
//
// They are exported so the gateway's structured-failure allow-list keys off
// the same symbols the producers write, rather than re-typing the literals —
// which is what it did at first, making an earlier version of this comment's
// "cannot drift apart" claim false on arrival.
const (
	FileExistsRefusalCode = "file_exists"
	DelegationDeniedCode  = "delegation_denied"
)

// Field budgets for a structured refusal payload, and the hard ceiling they
// serve.
//
// maxRefusalPayloadRunes is the real contract: the ENCODED JSON must stay
// under the 2000-rune cap that the persisted transcript
// (pkg/agent.maxFailClosedOutputChars) and the live frame
// (pkg/gateway.maxLiveErrorChars) both apply. A payload cut at that boundary
// does not merely lose its tail — it stops being JSON, so the gateway's
// whole-document unmarshal fails and the reader gets a fragment instead of a
// sentence.
//
// The per-field budgets are a cheap FIRST pass, not the guarantee. An earlier
// version treated them as the guarantee and was wrong: they count INPUT runes
// while the cap counts ENCODED ones, and encoding/json HTML-escapes < > & to
// six runes each and doubles " and \. All are legal filename characters, so a
// path holding ~67 of them overflowed a budget that arithmetic on ASCII said
// was safe. marshalWithinBudget closes that by measuring the encoded result.
const (
	maxRefusalPayloadRunes = 1900 // leaves headroom under the 2000-rune cap
	maxRefusalPathRunes    = 700
	maxRefusalReasonRunes  = 900
	maxRefusalToolRunes    = 64
)

// marshalWithinBudget encodes v and, if the result exceeds
// maxRefusalPayloadRunes, shrinks the two caller-supplied fields it is given
// and re-encodes until it fits.
//
// It measures the ENCODED size because that is the only thing the downstream
// truncation sees. Clamping inputs cannot bound it: JSON escaping expands
// characters by up to 6x, and which characters appear is entirely up to
// whoever named the file.
//
// The loop is bounded and always terminates: each round halves every
// shrinkable field, so within a handful of iterations they are single
// characters and the envelope alone is far under the cap. If it somehow still
// does not fit, the last encoding is returned rather than an error — an
// over-long payload that might get truncated is strictly better than none.
func marshalWithinBudget(v any, shrinkable ...*string) ([]byte, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 16 && len([]rune(string(encoded))) > maxRefusalPayloadRunes; i++ {
		for _, f := range shrinkable {
			if f == nil {
				continue
			}
			if n := len([]rune(*f)); n > 1 {
				*f = clampRefusalField(*f, max(1, n/2))
			}
		}
		encoded, err = json.Marshal(v)
		if err != nil {
			return nil, err
		}
	}
	return encoded, nil
}

// clampRefusalField bounds s to maxRunes, keeping the TAIL rather than the
// head. For a filesystem path the basename is the informative part and the
// leading directories are the repetitive part, so cutting from the front is
// what a reader would do by hand.
func clampRefusalField(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	const ellipsis = "…"
	return ellipsis + string(runes[len(runes)-maxRunes+1:])
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
