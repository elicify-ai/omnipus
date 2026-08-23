package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"

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

// structuredFailureMarshalFailureTotal counts how many times a structured
// tool-failure producer in this package fell back to its plain-text/static
// fallback because marshalWithinBudget (or the json.Marshal beneath it)
// returned an error. This should never fire in production — every producer
// below marshals a plain, statically-shaped struct with no unmarshalable
// field (no channel, func, or cycle) — but issue #618 shipped from a caller
// reasoning "this input class can't reach here" and being wrong, so the
// fallback path is made observable rather than silently swallowed.
var structuredFailureMarshalFailureTotal atomic.Int64

// warnStructuredFailureMarshalError logs a marshal-failure fallback with
// enough context to diagnose it (producer, tool, discriminator, error) and
// bumps structuredFailureMarshalFailureTotal.
// ReportStructuredFailureMarshalError is the exported entry point for
// producers OUTSIDE this package (pkg/agent's denialPayloadJSON and the
// tool-assembly-duplicate guard in loop.go) whose fallback also substitutes a
// static payload for real caller-supplied content. They were built with the
// same static-fallback design as the producers here but had no observability
// at all, which is the silent-failure shape this whole family exists to close.
func ReportStructuredFailureMarshalError(producer, tool, discriminator string, err error) {
	warnStructuredFailureMarshalError(producer, tool, discriminator, err)
}

func warnStructuredFailureMarshalError(producer, tool, discriminator string, err error) {
	structuredFailureMarshalFailureTotal.Add(1)
	slog.Warn("tools: structured failure payload marshal failed; using fallback",
		"producer", producer,
		"tool", tool,
		"discriminator", discriminator,
		"error", err,
		"failure_total", structuredFailureMarshalFailureTotal.Load(),
	)
}

// DelegationDeniedResult builds a structured error ToolResult for a denied
// delegation. ForLLM is the JSON-encoded DelegationFailure (the generated wire
// type, contracts/asyncapi.yaml → pkg/api/generated) so both the LLM and the
// SPA receive the same typed shape; IsError is set so the frame status is
// "error". The wrapped Err preserves the reason for logging.
//
// Invariant defense: the contract requires reason (minLength:1) and a policy
// enum value. A denial that arrives with an empty reason or an unrecognized
// policy would serialize a schema-invalid payload — today that means
// documentary schema drift, not an enforced drop, since neither the Go
// generated types nor the TS/Zod generated types actually validate the
// ToolCallResultFrame.result oneOf at runtime (see PermissionDeniedPayload's
// doc comment for the full explanation) — so we default a non-empty reason
// and a valid policy axis before marshaling regardless.
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
		warnStructuredFailureMarshalError("DelegationDeniedResult", tool, DelegationDeniedCode, err)
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
	// that is schema-invalid — the oneOf is not actually enforced anywhere
	// today (see PermissionDeniedPayload's doc comment for why), so nothing
	// currently drops it at a boundary, but a schema-invalid payload is
	// still a broken contract and downstream json.Unmarshal callers
	// (parseStructuredToolFailure) still choke on outright-malformed JSON.
	// Defaulting empty fields keeps the payload both schema-valid and
	// parseable, which is strictly better than forwarding a caller's empty
	// string unchanged.
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
		// here would ship no machine-checkable discriminator at all, only
		// prose — worse than the tag this replaces, even though nothing
		// today actually enforces the oneOf at a boundary (see
		// PermissionDeniedPayload's doc comment). Logged because "this can't
		// happen" was the exact reasoning that let issue #618 ship.
		warnStructuredFailureMarshalError("FileExistsRefusalResult", tool, FileExistsRefusalCode, err)
		return ErrorResult(reason)
	}
	return (&ToolResult{
		ForLLM:  string(encoded),
		IsError: true,
	}).WithError(fmt.Errorf("%s: %s", tool, reason))
}

// FileExistsRefusalCode, DelegationDeniedCode, PermissionDeniedCode, and
// ToolAssemblyDuplicateCode are the fixed discriminators from the
// FileExistsRefusal, DelegationFailure, PermissionDenied, and
// ToolAssemblyDuplicate contract schemas (the last two added by issue #618).
//
// They are exported so the gateway's structured-failure allow-list keys off
// the same symbols the producers write, rather than re-typing the literals —
// which is what it did at first, making an earlier version of this comment's
// "cannot drift apart" claim false on arrival.
//
// ADR-060 (docs/internal/architecture/ADR-060-structured-tool-failure-family.md)
// is the canonical reference for this family: D1 states the 5-requirement
// membership checklist (inline schema, exported *Code constant here, a
// single marshalWithinBudget-routed producer, a family-register entry in
// scripts/check-no-handwritten-wire-types.sh, and — for toolResult-channel
// members only — an allow-list entry / oneOf entry / SPA detector). D2
// defines the toolResult-vs-message delivery-channel taxonomy that decides
// which of those apply to a given member; D5 names, rather than hides, the
// four ways a fifth member can still slip past both enforcement mechanisms.
// Read there before adding a fifth discriminator, rather than re-deriving
// the rule from these four constants.
//
// ToolArgumentsTooLargeCode and ToolResultRecallMarkCode are the two ADR-066
// members (contracts/asyncapi.yaml ToolArgumentRefusal and
// ToolResultRecallMark, T066-01 schemas / T066-04 producers). The refusal is
// toolResult-channel (it IS the tool's result, US-5.AC3). The recall mark is
// NOT a failure — `error` is only the family's discriminator key; the tool
// call succeeded and the mark lives in the model's window in place of the
// result's content. Its allow-list membership is defensive over-provisioning
// on the ToolAssemblyDuplicate precedent (ADR-060 §7 item 3): it keeps the
// coverage test enrolling the producer in the schema-and-budget assertions.
const (
	FileExistsRefusalCode     = "file_exists"
	DelegationDeniedCode      = "delegation_denied"
	PermissionDeniedCode      = "permission_denied"
	ToolAssemblyDuplicateCode = "tool_assembly_duplicate"
	ToolArgumentsTooLargeCode = "tool_arguments_too_large"
	ToolResultRecallMarkCode  = "tool_result_recall_mark"
)

// AllStructuredFailureCodes returns every structured tool-failure
// discriminator this package defines. This is the SINGLE authoritative
// enumeration a new producer's Code constant must be added to — pkg/gateway
// derives its structuredFailureFamily/allow-list membership from this
// rather than re-typing the same four literals independently in a second
// package, which is what let issue #618 ship: a discriminator could exist
// here with a real producer while the gateway's hand-maintained list simply
// never learned about it. This does not, by itself, guarantee every new
// producer registers here — that still depends on a future author following
// the pattern — but it collapses "two lists that must stay in lockstep" to
// "one list plus its dependents", which is the direction the drift class
// closes in, not just relocates.
func AllStructuredFailureCodes() []string {
	return []string{
		DelegationDeniedCode,
		FileExistsRefusalCode,
		PermissionDeniedCode,
		ToolAssemblyDuplicateCode,
		ToolArgumentsTooLargeCode,
		ToolResultRecallMarkCode,
	}
}

// ToolArgumentRefusalPayload builds the JSON-encoded, budget-bounded
// ToolArgumentRefusal wire payload (contracts/asyncapi.yaml, ADR-066 D4 /
// spec FR-016): the result the loop hands the model INSTEAD of executing a
// tool call whose serialised arguments exceed the builtin success cap. It
// names the tool, the size the model sent and the live cap so a retry can
// target it. Reason is the one field marshalWithinBudget may shrink.
//
// Like its siblings it uses encoding/json (via marshalWithinBudget) rather
// than fmt.Sprintf, and defaults every required field to a schema-valid
// minimum (minLength:1 / minimum:1) so a zero-value caller can never emit a
// schema-invalid payload.
func ToolArgumentRefusalPayload(tool string, sizeChars, capChars int) ([]byte, error) {
	if tool == "" {
		tool = "(unknown tool)"
	}
	tool = clampRefusalField(tool, maxRefusalToolRunes)
	sizeChars = max(1, sizeChars)
	capChars = max(1, capChars)
	reason := "tool call refused: the serialised arguments for " + tool + " are " +
		strconv.Itoa(sizeChars) + " characters, over the " + strconv.Itoa(capChars) +
		"-character cap. The tool did not run. Retry with smaller arguments — " +
		"for example write large content in several smaller pieces."
	payload := generated.ToolArgumentRefusal{
		Error:     ToolArgumentsTooLargeCode,
		Reason:    reason,
		Tool:      tool,
		SizeChars: sizeChars,
		CapChars:  capChars,
	}
	return marshalWithinBudget(&payload, &payload.Reason)
}

// ToolArgumentRefusalResult wraps ToolArgumentRefusalPayload as the error
// ToolResult the loop admits through the D4 choke point (US-5.AC3) in place
// of the tool's own result. ForLLM is the payload verbatim — the model reads
// ForLLM and nothing else (ADR-059 W3) — and IsError is set so the frame
// status is "error"; Err keeps the reason for logging.
func ToolArgumentRefusalResult(tool string, sizeChars, capChars int) *ToolResult {
	encoded, err := ToolArgumentRefusalPayload(tool, sizeChars, capChars)
	if err != nil {
		warnStructuredFailureMarshalError("ToolArgumentRefusalResult", tool, ToolArgumentsTooLargeCode, err)
		return ErrorResult("tool call refused: serialised arguments exceed the cap; retry with smaller arguments")
	}
	return (&ToolResult{
		ForLLM:  string(encoded),
		IsError: true,
	}).WithError(fmt.Errorf("%s: arguments of %d chars exceed the %d-char cap", tool, sizeChars, capChars))
}

// RecallMarkParams are the facts a recall mark states (ADR-066 D5 / spec
// FR-018). Tool and ToolCallID are sanitised by the producer — at most
// maxRefusalToolRunes printable runes each — so a hostile MCP tool name or a
// provider-generated id never reaches the model or the SPA unbounded (E6).
// ArchiveLine is the zero-based archive index of the result line; SizeChars
// the full result's length in characters as archived; Turn the number
// recall_conversation's turn_range mode addresses (1 + the count of
// role:user archive lines preceding ArchiveLine — computed by the pkg/agent
// caller, which owns the archive).
type RecallMarkParams struct {
	Tool        string
	ToolCallID  string
	ArchiveLine int
	SizeChars   int
	Turn        int
}

// CappedMarkPayload builds the ToolResultRecallMark payload for a result the
// D4 choke point admitted head-and-tail capped (content_state=capped). Its
// length counts toward the cap (FR-011), which is why the mark is short and
// deterministic.
func CappedMarkPayload(p RecallMarkParams) ([]byte, error) {
	return recallMarkPayload("capped", p)
}

// EmptiedMarkPayload builds the ToolResultRecallMark payload that REPLACES a
// result's content in the model's window when D5 empties it in place
// (content_state=emptied). The archive keeps the full content.
func EmptiedMarkPayload(p RecallMarkParams) ([]byte, error) {
	return recallMarkPayload("emptied", p)
}

// recallMarkPayload is the single producer behind both mark states. It is
// routed through marshalWithinBudget like every family member; the hint is
// the one shrinkable field, and with both ids bounded to 64 runes the
// payload never approaches the budget in practice.
func recallMarkPayload(state string, p RecallMarkParams) ([]byte, error) {
	tool := SanitiseMarkField(p.Tool, "(unknown tool)")
	id := SanitiseMarkField(p.ToolCallID, "(unknown id)")
	hint := "Call recall_conversation with tool_call_id=" + strconv.Quote(id) +
		" and archive_line=" + strconv.Itoa(max(0, p.ArchiveLine)) +
		" (offset, length) to read the full content in pages."
	payload := generated.ToolResultRecallMark{
		Error:        ToolResultRecallMarkCode,
		Tool:         tool,
		ToolCallId:   id,
		ArchiveLine:  max(0, p.ArchiveLine),
		SizeChars:    max(1, p.SizeChars),
		Turn:         max(1, p.Turn),
		ContentState: state,
		Hint:         hint,
	}
	return marshalWithinBudget(&payload, &payload.Hint)
}

// SanitiseMarkField bounds a tool name or tool_call_id for a recall mark
// (FR-018): every non-printable rune (controls, DEL, unassigned) is
// stripped, the HEAD is kept up to maxRefusalToolRunes runes (the namespace
// prefix of an MCP tool name — `mcp_<server>_` — is the informative part),
// and an input that strips to nothing becomes fallback so the minLength:1
// contract fields never go empty.
func SanitiseMarkField(s, fallback string) string {
	out := make([]rune, 0, min(len(s), maxRefusalToolRunes))
	for _, r := range s {
		if !unicode.IsPrint(r) {
			continue
		}
		out = append(out, r)
		if len(out) == maxRefusalToolRunes {
			break
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return string(out)
}

// PermissionDeniedPayload builds the JSON-encoded, budget-bounded
// PermissionDenied wire payload (contracts/asyncapi.yaml PermissionDenied
// schema) shared by BOTH permission-denial producers in this codebase
// (issue #618):
//
//   - pkg/agent's tool-policy denial path (tool_denial.go's
//     denialPayloadJSON), for approval-flow outcomes (user / timeout /
//     saturated / policy_denied / no_approver_configured / the headless
//     ask-policy auto-deny / ...).
//   - pkg/tools' filesystem-scope denial path (PermissionDeniedResult
//     below), for ResolvePath's ErrOutsideScope / ErrCarveOut /
//     ErrPathInvalid classification.
//
// Uses encoding/json (via marshalWithinBudget), NOT fmt.Sprintf's %q — both
// call sites used %q before this fix, and %q is Go-string quoting, not JSON
// quoting. It is provably unsafe for this payload: it breaks on invalid
// UTF-8 and on any C0/C1 control byte outside \n\t\r (%q emits \xNN, which
// is not a legal JSON escape — json.Unmarshal fails with "invalid character
// 'x' in string escape code"). reason and tool are both attacker/
// environment-influenced — an MCP-declared tool name is not statically
// enumerable, and a *os.PathError's Error() concatenates the raw, unescaped
// filesystem path — so this is reachable, not theoretical: an ordinary
// ~830-character path (a fifth of Linux's 4096-byte PATH_MAX) already
// overflows a naive per-field budget once JSON-escaped, and
// resolvepath.go's ErrOutsideScope error message embeds three path-shaped
// values (the raw path, its resolved form, and the working directory) —
// only one of which is the raw path itself, but any one of the three can
// individually be long enough to matter.
//
// There was never an "old per-field budget" this schema shipped with and
// then tightened — the whole point of #618 was that PRE-fix, this payload
// had NO length budget at all (hand-built with %q, unmeasured). The 1900-
// rune maxRefusalPayloadRunes cap and the per-field clamps below are the
// first budget this payload has ever had.
//
// permanent has no default here: both callers always know a concrete
// boolean value (ClassifyDenial's DenialClass.Permanent, or "always true"
// for a filesystem-scope denial — see PermissionDeniedResult's own comment)
// and must pass it explicitly.
func PermissionDeniedPayload(tool, message, reason string, permanent bool) ([]byte, error) {
	// The contract requires all three string fields non-empty (minLength:1).
	// A payload violating that is schema-invalid — nothing today actually
	// enforces the ToolCallResultFrame.result oneOf at a boundary (neither
	// the generated Go type, `any`-typed, nor the generated Zod schema,
	// z.unknown()-typed, validate it at runtime; the oneOf is documentary),
	// but shipping malformed structure is still wrong on its own terms, and
	// an empty required field would make parseStructuredToolFailure's
	// downstream reason-lifting less useful even where json.Unmarshal still
	// succeeds. Default rather than forward an empty caller value, mirroring
	// FileExistsRefusalResult.
	if message == "" {
		message = "Access to this action is denied."
	}
	if tool == "" {
		tool = "(unknown tool)"
	}
	if reason == "" {
		reason = "no additional detail available"
	}
	tool = clampRefusalField(tool, maxRefusalToolRunes)
	message = clampRefusalField(message, maxRefusalReasonRunes)
	reason = clampRefusalField(reason, maxRefusalReasonRunes)
	payload := generated.PermissionDenied{
		Error:     PermissionDeniedCode,
		Message:   message,
		Tool:      tool,
		Reason:    reason,
		Permanent: permanent,
	}
	// All three of reason, tool, and message are offered to the shrink
	// loop, in this PRIORITY ORDER (marshalWithinBudget exhausts each field
	// fully before touching the next): reason first (the field most likely
	// to actually be long — an escape-heavy filesystem path or a verbose
	// approval-flow explanation), then tool (an MCP-declared name; already
	// clamped to maxRefusalToolRunes above, but that clamp alone was H3's
	// finding — the sole protection for an attacker/environment-influenced
	// field must not be a single deletable line, so it is also shrinkable
	// here), and message LAST — message is always a short fixed literal at
	// every real call site (the model-facing "do not retry" guidance), so it
	// should only ever be touched as a last resort, never degraded just
	// because reason needed shrinking (an earlier version of this loop
	// shrank every field every round in lockstep, which could reduce
	// message's actionable guidance to a fragment even though message
	// itself was never the field that made the payload oversized).
	return marshalWithinBudget(&payload, &payload.Reason, &payload.Tool, &payload.Message)
}

// ToolAssemblyDuplicatePayload builds the JSON-encoded, budget-bounded
// ToolAssemblyDuplicate wire payload (contracts/asyncapi.yaml
// ToolAssemblyDuplicate schema) for pkg/agent/loop.go's tool-call dedup
// invariant guard (checkToolDedupInvariant) — issue #618's fourth,
// previously fully ungoverned member (no schema, no allow-list entry,
// %q-built, unbounded message).
//
// This payload is emitted as plain message content (providers.Message,
// role="system"), never as a ToolCallResultFrame's `result` field — it has
// no SPA detector BY DESIGN, not as a gap: parseStructuredToolFailure is
// only ever invoked on a tool_call_result frame's result string or a
// persisted ToolCall.Error string, and this producer's call site
// (pkg/agent/loop.go) never populates either. It is LLM-facing only.
//
// Uses encoding/json rather than fmt.Sprintf's %q for the same reason
// PermissionDeniedPayload does (see its doc comment). message is
// dedupErr.Error(), unbounded input, so it is bounded by
// marshalWithinBudget exactly like the two adjacent producers.
func ToolAssemblyDuplicatePayload(message string) ([]byte, error) {
	if message == "" {
		message = "duplicate tool assembly detected"
	}
	payload := generated.ToolAssemblyDuplicate{
		Error:   ToolAssemblyDuplicateCode,
		Message: clampRefusalField(message, maxRefusalReasonRunes),
	}
	return marshalWithinBudget(&payload, &payload.Message)
}

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
// maxRefusalPayloadRunes, shrinks the caller-supplied fields it is given and
// re-encodes until it fits.
//
// It measures the ENCODED size because that is the only thing the downstream
// truncation sees. Clamping inputs cannot bound it: JSON escaping expands
// characters by up to 6x, and which characters appear is entirely up to
// whoever named the file.
//
// shrinkable is shrunk in PRIORITY ORDER, not in lockstep: this function
// fully exhausts shrinkable[0] (halving it down to a single rune if
// necessary) before touching shrinkable[1], and so on. An earlier version
// halved every field every round together, which meant an oversized field
// near the end of the payload could drag a short, fixed-literal field near
// the front (e.g. PermissionDeniedPayload's model-facing `message` guidance)
// down to a near-empty fragment even though that field was never what made
// the payload oversized. Callers should therefore order shrinkable from
// "most likely to actually need shrinking" to "shrink only as a last
// resort" — see each producer's own call site for its reasoning.
//
// No field is ever shrunk to the empty string — clampRefusalField's floor is
// 1 rune — so every field this function is given as shrinkable stays
// non-empty (and therefore minLength:1-valid) even if the budget is never
// met: a required field going empty would make the payload schema-invalid
// in a way prose "worse than nothing" replaces gracefully; a 1-rune field
// does not.
//
// The loop is bounded and always terminates: each round on a given field
// halves it, so within a handful of iterations per field it reaches a
// single character and the envelope alone is far under the cap. If the
// budget is still not met after exhausting every shrinkable field (only
// possible if the fixed, non-shrinkable parts of v's JSON shape alone
// exceed the cap — none of this package's producers are anywhere close),
// the last encoding is returned rather than an error: an over-long payload
// that might get truncated downstream is strictly better than none.
func marshalWithinBudget(v any, shrinkable ...*string) ([]byte, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	for _, f := range shrinkable {
		if f == nil {
			continue
		}
		for i := 0; i < 16 && len([]rune(string(encoded))) > maxRefusalPayloadRunes; i++ {
			n := len([]rune(*f))
			if n <= 1 {
				break
			}
			*f = clampRefusalField(*f, max(1, n/2))
			encoded, err = json.Marshal(v)
			if err != nil {
				return nil, err
			}
		}
		if len([]rune(string(encoded))) <= maxRefusalPayloadRunes {
			break
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
