// turn_transcript.go: Transcript recording for a turn — tool-call, assistant and error entries

package agent

import (
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

// transcriptSuppressedErrors is incremented each time appendErrorTranscript
// is called with no transcript store wired (e.g. boot misconfig where
// transcriptStore is nil). A persistent non-zero value is a strong signal
// that error events are vanishing from the on-disk transcript; surfaces
// the otherwise-silent "errors-but-no-record" gap (W4-18). DEBUG logs of
// the no-op are promoted to WARN to keep this signal visible in production.
var transcriptSuppressedErrors atomic.Uint64

// TranscriptSuppressedErrors returns the current value of the
// transcript-suppressed-error counter. Used by tests and operator tooling.
func TranscriptSuppressedErrors() uint64 {
	return transcriptSuppressedErrors.Load()
}

// transcriptWriteFailures is incremented each time one of this file's four
// transcript writers (appendToolCallTranscript, appendIntermediateAssistantTranscript,
// appendAssistantTranscript, appendErrorTranscript) calls
// UnifiedStore.AppendTranscriptStrict against a session id that does not
// resolve to a real, store-backed session (ADR-057 FR-001/FR-002, W3;
// BDD-03). Before ADR-057, AppendTranscript silently minted an orphan
// session directory for exactly this case and returned nil, so a lost
// transcript write was indistinguishable from a successful one; the four
// call sites already logged a WARN on error, but nothing counted it. This is
// a DIFFERENT failure than transcriptSuppressedErrors (which fires when no
// transcript store is wired at all, i.e. ts.transcriptStore == nil ||
// ts.transcriptSessionID == "") — this counter fires only when the store IS
// wired and the call actually reaches it, but the session id it names does
// not exist.
var transcriptWriteFailures atomic.Uint64

// TranscriptWriteFailures returns the current value of the
// transcript-write-failure counter (ADR-057 FR-001/FR-002). Used by tests
// and operator tooling.
func TranscriptWriteFailures() uint64 {
	return transcriptWriteFailures.Load()
}

// outcomeRelabelApplies reports whether FR-017a's media-retry stamp should
// override this persist/emit. Residual 4xx stays CodeUnknown so the stamp
// can label that inconclusive trigger as media after a successful
// strip-retry. A later distinct classified failure must keep its own code
// — otherwise reload tells the user the model rejected an image.
func outcomeRelabelApplies(current, relabel LLMErrorCode) bool {
	return relabel != "" && (current == "" || current == CodeUnknown)
}

// warnAbandonedTranscriptWrite emits the ADR-057 FR-003/BDD-04 WARN record
// for the ts.abandoned write-suppression branch shared by all four
// transcript writers below, naming the session id and the suppression
// reason. `[grill C-2]` The abandonedWritesSuppressed counter already exists
// and already increments at each of these four sites (and three more
// outside this file) — this call adds only the previously-missing log
// record; the counter's own existing behavior is untouched by this change.
func (ts *turnState) warnAbandonedTranscriptWrite(writer string) {
	logger.WarnCF("agent", "transcript write suppressed: turn marked abandoned",
		map[string]any{"session_id": ts.transcriptSessionID, "writer": writer, "reason": "abandoned"})
}

// appendToolCallTranscript records a tool call to the session transcript.
// It is a no-op when no transcript store or session ID is configured, or when
// the turn has been marked abandoned (B4: suppresses writes from stuck goroutines).
//
// Bug 1 fix: the AgentID on the entry reflects the runtime-current active agent
// (via activeAgentResolver) rather than the turn's starting agent. This ensures
// that tool_call entries produced after a handoff carry the correct agent_id —
// the new active agent — instead of the one that initiated the turn.
func (ts *turnState) appendToolCallTranscript(tc session.ToolCall) {
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		ts.warnAbandonedTranscriptWrite("appendToolCallTranscript")
		return
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return
	}

	// Approval-gate settle (approval_transcript.go): when this call previously
	// wrote a "pending" placeholder because it blocked on human approval,
	// REPLACE that entry rather than appending a second one with the same ID.
	// Only ever true for ask-policy calls that actually reached the approver,
	// so every other tool call skips straight to the append below.
	//
	// The placeholder itself arrives here with Status "pending" and must not
	// try to replace itself, hence the status guard. A failed replacement falls
	// through to the append — a duplicate entry is a far better outcome than a
	// lost record of what the tool did.
	if tc.Status != toolCallStatusPending {
		if _, hadPending := ts.askPendingToolCalls.LoadAndDelete(tc.ID); hadPending {
			if replaceToolCallInTranscript(ts, tc.ID, toolCallStatusPending, tc) {
				return
			}
		}
	}

	agentID := ts.resolveActiveAgentID()
	entry := session.TranscriptEntry{
		ID:        string(tc.ID),
		Type:      session.EntryTypeToolCall,
		AgentID:   agentID,
		Timestamp: time.Now().UTC(),
		ToolCalls: []session.ToolCall{tc},
		// ADR-066 FR-046: hydration attaches a standalone tool_call entry to
		// the preceding assistant message of the SAME turn; the turn id makes
		// that match exact instead of inferred from the last user boundary.
		TurnID: ts.turnID,
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
		logger.WarnCF("agent", "could not record tool call to transcript",
			map[string]any{"session_id": ts.transcriptSessionID, "tool": tc.Tool, "error": err.Error()})
	}
}

// appendIntermediateAssistantTranscript persists an assistant text segment that
// immediately precedes a round of tool calls within a single turn. It is called
// once per tool-call iteration when the LLM emits both narration text AND tool
// calls in the same response — the text must be recorded BEFORE the tool_call
// entries so the transcript faithfully reflects the interleaved order the user
// saw live.
//
// Tokens and cost are always 0 for intermediate entries to avoid double-counting:
// the turn total is attributed to the final assistant entry written by either
// wsStreamer.Finalize (streaming path) or appendAssistantTranscript (non-streaming).
//
// Bug #416 fix: without this, only the last text segment reached the transcript.
//
// producedModel is the model string that emitted THIS segment. When the
// caller is a streaming path or a sub-agent, the per-message model differs
// from ts.lastProducedModel (a single slot which the parent's LLM call may
// have just overwritten). Pass "" to fall back to ts.lastProducedModel
// for callers that don't have a per-message producer.
func (ts *turnState) appendIntermediateAssistantTranscript(content string, producedModel ...string) {
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		ts.warnAbandonedTranscriptWrite("appendIntermediateAssistantTranscript")
		return
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" || content == "" {
		return
	}
	agentID := ts.resolveActiveAgentID()
	model := ts.lastProducedModel
	if len(producedModel) > 0 && producedModel[0] != "" {
		model = producedModel[0]
	}
	entry := session.TranscriptEntry{
		ID:        uuid.New().String(),
		Role:      "assistant",
		AgentID:   agentID,
		Content:   content,
		Timestamp: time.Now().UTC(),
		Model:     model,
		// TurnID: without this, neither MarkLastEntryTruncated's own
		// turn-scoped backward-walk (H2 fix — it matches on e.TurnID ==
		// turnID) nor the frontend's turn_canceled -> assistant-message
		// replay correlation can ever match a REAL entry; both were only
		// ever exercised by tests that hand-seed TurnID directly. See
		// appendAssistantTranscript's identical fix for the full rationale.
		TurnID: ts.turnID,
		// ParentSpawnCallID: non-empty only when ts is a CHILD delegation
		// sub-turn (spawnSubTurn stamps childTS.parentSpawnCallID before any
		// turn processing runs). Lets pkg/gateway/replay.go withhold this
		// entry from top-level replay, matching live rendering — see
		// session.TranscriptEntry.ParentSpawnCallID's doc comment for the
		// full root-cause writeup (live/reload bubble-count divergence on
		// multi-step delegation).
		ParentSpawnCallID: ts.parentSpawnCallID,
		// Tokens and Cost are intentionally 0 — the turn total is attributed to
		// the final assistant entry only. See appendAssistantTranscript.
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
		logger.WarnCF("agent", "could not record intermediate assistant message to transcript",
			map[string]any{"session_id": ts.transcriptSessionID, "error": err.Error()})
	}
}

// appendAssistantTranscript persists a completed assistant text response to the
// session transcript. It is called on the non-streaming path (no wsStreamer) so
// that replay can reconstruct the full conversation even after a WS disconnect.
//
// Bug 3 fix: the wsStreamer.Finalize path already handles streaming responses.
// For non-streaming turns (WS disconnected or channel that never streams), this
// ensures the assistant content reaches transcript.jsonl.
//
// producedModel is the model string that emitted THIS response. Pass ""
// to fall back to ts.lastProducedModel.
func (ts *turnState) appendAssistantTranscript(content string, producedModel ...string) {
	ts.appendAssistantTranscriptImpl(content, false, false, "", producedModel...)
}

// validTruncationReasonsAgent mirrors pkg/session's unexported
// validTruncationReasons (ADR-087 D2): the only two values
// appendAssistantTranscriptTruncated will ever stamp onto a persisted
// entry. Kept in lockstep with session.MarkLastEntryTruncated's own set —
// pkg/session cannot be imported for the map itself since it is
// unexported, but both lists must never diverge.
var validTruncationReasonsAgent = map[string]bool{
	"cancelled":         true,
	"max_output_tokens": true,
}

// appendAssistantTranscriptTruncated is the ADR-087 D4a/D4b non-streaming
// write-choke-point fix: it stamps Truncated=true and TruncationReason on
// the assistant entry in the SAME construction/write appendAssistantTranscript
// already performs, instead of the pre-fix two-step pattern (append the
// entry, then have loop.go immediately call session.MarkLastEntryTruncated
// to re-read, re-parse, and rewrite the whole transcript.jsonl just to
// stamp two fields on the entry that was built one call earlier). The
// streaming path already writes truncation state in a single pass via
// wsStreamer.Finalize (commit 47c086ca); this brings the non-streaming
// path to parity.
//
// content == "" is allowed and always written (D4a: a turn truncated with
// no output produced still needs a persisted, correctly-flagged entry) —
// the streamed path's equivalent zero-content write is wsStreamer.Finalize
// itself (WP C, pkg/gateway/websocket.go), stamped via the
// streamerTruncationSetter probe in finalizeStreamer below.
//
// reason MUST be one of session's two accepted values ("cancelled",
// "max_output_tokens" — ADR-087 D2, see validTruncationReasonsAgent). Any
// other value is a programming error at this call site: rather than
// silently persist an unrecognized reason (which session.MarkLastEntryTruncated
// would itself have rejected), this logs loudly at Error level and falls
// back to writing the entry WITHOUT the truncation fields — so the
// content is never lost, but a bad reason can never masquerade as a valid
// one on disk.
func (ts *turnState) appendAssistantTranscriptTruncated(content, reason string, producedModel ...string) {
	if !validTruncationReasonsAgent[reason] {
		logger.ErrorCF("agent", "appendAssistantTranscriptTruncated: invalid truncation reason, entry written WITHOUT truncation stamp",
			map[string]any{"session_id": ts.transcriptSessionID, "turn_id": ts.turnID, "reason": reason})
		ts.appendAssistantTranscriptImpl(content, true, false, "", producedModel...)
		return
	}
	ts.appendAssistantTranscriptImpl(content, true, true, reason, producedModel...)
}

// appendAssistantTranscriptImpl is the shared body behind
// appendAssistantTranscript and appendAssistantTranscriptTruncated —
// allowEmpty controls only whether a "" content is written (D4a) or
// no-opped (every other caller); truncated / truncationReason set
// session.TranscriptEntry's Truncated / TruncationReason fields at
// construction, so a truncated non-streaming entry is written once instead
// of appended-then-rewritten.
func (ts *turnState) appendAssistantTranscriptImpl(content string, allowEmpty bool, truncated bool, truncationReason string, producedModel ...string) {
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		ts.warnAbandonedTranscriptWrite("appendAssistantTranscript")
		return
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" || (content == "" && !allowEmpty) {
		return
	}
	agentID := ts.resolveActiveAgentID()
	model := ts.lastProducedModel
	if len(producedModel) > 0 && producedModel[0] != "" {
		model = producedModel[0]
	}
	// Populate token/cost from accumulated turn stats so scheduled and
	// non-websocket turns record real usage, mirroring the wsStreamer.Finalize
	// path (#411). GetTurnStats is safe to call here — the turn is finishing.
	turnTokens, turnCost := ts.GetTurnStats()
	turnCacheRead, turnCacheWrite := ts.GetTurnCacheStats()
	turnPromptTokens, turnCompletionTokens := ts.GetTurnIOStats()
	entry := session.TranscriptEntry{
		ID:      uuid.New().String(),
		Role:    "assistant",
		AgentID: agentID,
		Content: content,
		// TurnID stamps this entry with its own producing turn. This is
		// THE fix for the confirmed live-verification bug: before this,
		// TurnID was set on the turn_canceled entry (cancel.go) but NEVER on
		// the assistant entry it describes, so (1) the frontend's
		// turn_canceled -> assistant-message replay correlation could never
		// match a real entry (chatTurnCanceledNoMatch always fired on
		// reload), and (2) MarkLastEntryTruncated's own turn-scoped
		// backward-walk (which requires e.TurnID == turnID) could never
		// match a real entry either, silently disabling the Truncated flag
		// for every real cancel. Both were only ever exercised by tests that
		// hand-seed TurnID directly on the entry.
		TurnID:           ts.turnID,
		Timestamp:        time.Now().UTC(),
		Tokens:           int(turnTokens),
		Cost:             turnCost,
		Model:            model,
		PromptTokens:     turnPromptTokens,
		CompletionTokens: turnCompletionTokens,
		CacheReadTokens:  turnCacheRead,
		CacheWriteTokens: turnCacheWrite,
		// ParentSpawnCallID: see appendIntermediateAssistantTranscript's
		// identical stamp for the full rationale — non-empty only for a
		// child delegation sub-turn's own final-turn text.
		ParentSpawnCallID: ts.parentSpawnCallID,
		// Truncated / TruncationReason: set only by
		// appendAssistantTranscriptTruncated (ADR-087 D4a/D4b) so a
		// non-streaming truncated turn's Truncated flag lands in this same
		// write, never via a follow-up rewrite of transcript.jsonl.
		Truncated:        truncated,
		TruncationReason: truncationReason,
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
		logger.WarnCF("agent", "could not record assistant message to transcript",
			map[string]any{"session_id": ts.transcriptSessionID, "error": err.Error()})
	}
}

// appendErrorTranscript writes a system entry to the JSONL transcript so a
// later session replay can render the error after a page reload. The
// `kind` parameter is the EventKind label that triggered the write
// ("error" for a provider error, "rate_limit" for a rate-limit denial);
// `stage` is the loop stage ("runTurn", "hooks", etc.); `message` is the
// human-readable description; `pe` is the optional structured provider
// error (status/body) — when present, the classifier runs here (write
// choke point, ADR-051 §RD5) so raw provider text never lands on disk.
//
// Used by recordRateLimitDenial and the LLM-call-error paths in loop.go to
// satisfy FR-001 (rate_limit → transcript) and FR-002 (provider error →
// transcript) of docs/internal/specs/phase-1-chat-model-and-errors.md, plus
// the translation-at-write invariant of ADR-051 §RD5 (FR-007/008).
//
// Rate-limit skip (ADR-051 §RD5 MAJ-001/004): when kind==EventKindRateLimit
// the caller-supplied message is already friendly (rate_limit: policyRule
// (retry after Ns)) — passing pe=nil here lets the classifier recognize
// it via the message substring and emit CodeRateLimited, but the caller
// message is preserved as-is. Either way, raw provider text never reaches
// the JSONL.
//
// Silently no-ops when the turn has been abandoned or when no transcript
// store is wired (matches appendAssistantTranscript's failure semantics — a
// failed transcript write must NOT abort the in-flight turn). Debug-level
// logging is emitted when the no-op fires so an operator can trace why a
// transcript entry was suppressed.
// trustedInternalStages is the set of stage+kind tuples that the write
// choke point MUST NOT sanitize. Callers of appendErrorTranscript for
// these stages produce curated, generic copy that is the actionable
// signal a user/operator needs to see; sanitizing them would clobber
// the operator-friendly shape. Provider-originated text NEVER enters
// these paths (the prior call site is an internal hook, abort, or
// synthetic-deny source).
type internalStage struct{ stage, kind string }

var trustedInternalStageSet = map[internalStage]struct{}{
	{"rate_limit", "rate_limit"}:   {},
	{"model_switch", "error"}:      {},
	{"before_llm", "error"}:        {},
	{"after_llm", "error"}:         {},
	{"llm_call", "error"}:          {},
	{"llm_retry_backoff", "error"}: {},
	{"turn_loop", "error"}:         {},
	// ADR-058 §10.A3: FR-084 (the retired synthetic-error-floor feature) was
	// deleted in full — no producer calls appendErrorTranscript with that
	// stage name anymore, so a trust-set entry for it does not belong here.
	// FIX 6: hookAbortError (loop.go) is the SOLE producer of hook-abort
	// transcript entries, and it ALWAYS calls appendErrorTranscript with the
	// literal stage "hooks" — regardless of which HookInterceptor stage
	// (before_llm/after_llm/before_tool/after_tool) actually triggered the
	// abort; that more specific stage name only flows into the error
	// MESSAGE text and the live EventPayload.Stage ("hook."+stage), never
	// into this appendErrorTranscript call. So "hooks" is the one entry
	// that actually matters here: without it, hookAbortError's
	// decision.Reason (caller-curated text from a HookInterceptor — before_
	// tool/after_tool share the exact same ToolInterceptor/HookManager
	// plumbing and provenance as before_llm/after_llm, already trusted
	// above) gets re-run through the classifier, and any hook reason that
	// happens to contain a pinned substring (e.g. "safety", "rate limit")
	// is silently replaced with generic boilerplate — even though the SAME
	// reason survives byte-for-byte in the CodeUnknown/no-providerErr
	// fallback below for reasons that don't happen to match a substring.
	// "before_tool"/"after_tool" are added too — unlike "hooks" above, these
	// ARE reachable today, via a DIFFERENT call site than hookAbortError:
	// abortTurn (loop.go) passes its `stage` argument straight through to
	// appendErrorTranscript verbatim (no hardcoded "hooks" collapse), and is
	// itself called as al.abortTurn(ts, "before_tool", decision.Reason) /
	// al.abortTurn(ts, "after_tool", decision.Reason) on a HookActionHardAbort
	// decision (loop.go's before_tool and after_tool HookInterceptor call
	// sites). decision.Reason is the same caller-curated
	// HookInterceptor/HookManager text as before_llm/after_llm/"hooks" above,
	// so it needs the identical trusted-stage protection from re-classification.
	{"hooks", "error"}:       {},
	{"before_tool", "error"}: {},
	{"after_tool", "error"}:  {},
	// Workspace-membership refusals (runTurn after EventKindTurnStart).
	// The caller already ran TranslateTurnError and passed the catalogue
	// sentence. Without this entry the write choke point re-classifies
	// that sentence as unknown, and replay looks up the unknown line
	// ("we can't tell why") — the live fix vanishing on reload.
	{"workspace", "error"}:    {},
	{"external_cli", "error"}: {},
}

func isTrustedInternalStage(stage, kind string) bool {
	_, ok := trustedInternalStageSet[internalStage{stage: stage, kind: kind}]
	return ok
}

func (ts *turnState) appendErrorTranscript(kind, stage, message string, pe ...*ProviderError) {
	ts.writeErrorTranscript(kind, stage, message, "", pe...)
}

// appendClassifiedError persists an error the caller already classified.
// Replay looks up the bubble by ErrorCode, not Content. Re-running the
// catalogue sentence through TranslateLLMError stamps unknown — the live
// fix then vanishes on reload. Pass the live LLMError instead.
func (ts *turnState) appendClassifiedError(kind, stage string, llm LLMError) {
	ts.writeErrorTranscript(kind, stage, llm.Message, llm.Code)
}

// appendDetachedTerminalError is the controller-owned timeout write allowed after
// MarkAbandoned. The abandoned flag suppresses writes from the detached child
// goroutine; it must not suppress the coordinator's single terminal timeout,
// or a session reload would lose the reason the child stopped.
func (ts *turnState) appendDetachedTerminalError(kind, stage string, llm LLMError) {
	ts.writeErrorTranscriptWithAbandonment(kind, stage, llm.Message, llm.Code, true)
}

// appendDelegatedTaskLimitNotice is the sole persistence entry point for the
// identifier-rich delegated-task notice. Its current producers restrict
// variable content to bounded correlation fields, so this method can preserve
// that copy without opening the generic classified-error path to arbitrary
// child output.
func (ts *turnState) appendDelegatedTaskLimitNotice(notice delegatedTaskLimitNotice) {
	kind := EventKindError.String()
	stage := string(notice.stage)
	if !ts.canWriteErrorTranscript(kind, stage, notice.message, false) {
		return
	}
	ts.persistErrorTranscript(kind, stage, notice.llmError(), notice.message)
}

func (ts *turnState) writeErrorTranscript(kind, stage, message string, code LLMErrorCode, pe ...*ProviderError) {
	ts.writeErrorTranscriptWithAbandonment(kind, stage, message, code, false, pe...)
}

func (ts *turnState) writeErrorTranscriptWithAbandonment(
	kind, stage, message string,
	code LLMErrorCode,
	allowAbandoned bool,
	pe ...*ProviderError,
) {
	if !ts.canWriteErrorTranscript(kind, stage, message, allowAbandoned) {
		return
	}

	// ADR-051 §RD5 write choke point: translate the message via the shared
	// classifier so raw provider text never persists. The classifier reads
	// pe.Status/pe.Body when present (nil-safe — see classifyByProviderError);
	// pe nil means "this is not a provider error" (e.g. internal model_switch
	// failures, hook aborts, rate-limit denials) and the classifier falls
	// back to substring matching on the caller-supplied message.
	var providerErr *ProviderError
	if len(pe) > 0 {
		providerErr = pe[0]
	}
	// Trusted internal-stages bypass (ADR-051 §RD5 IMPORTANT 1):
	// the caller has produced a curated, generic message for these stages
	// that does NOT carry raw provider text. Sanitizing them would clobber
	// the actionable signal the operator relies on (e.g. hook abort reason,
	// synthetic-error-floor count, model-switch friendly guidance). The
	// classifier still stamps a typed code on the entry for replay routing,
	// but the user-visible text is the caller-provided copy verbatim.
	llm := TranslateLLMError(providerErr, message)
	trustedMessage := isTrustedInternalStage(stage, kind)

	// Prefer the caller's already-classified code. Catalogue sentences do
	// not contain the classifier substrings, so a second TranslateLLMError
	// would stamp unknown and reload would say we cannot tell why.
	if code != "" {
		llm.Code = code
		llm.Retryable = isRetryable(code)
		if allowAbandoned || trustedMessage {
			llm.Message = message
		} else {
			llm.Message = defaultUserMessage(code)
		}
	}
	// Belt for the one uncoded producer that is unambiguous: an internal
	// SEC-26 denial written as kind=rate_limit, stage=rate_limit. Live is
	// EventKindRateLimit; replay looks up by ErrorCode.
	if code == "" && stage == "rate_limit" && kind == EventKindRateLimit.String() {
		llm.Code = CodeRateLimited
		llm.Message = message
		llm.Retryable = isRetryable(CodeRateLimited)
	}

	// FR-017a: label an *inconclusive* residual 4xx after a successful
	// strip-retry. Do not overwrite a later distinct classified code.
	if outcomeRelabelApplies(llm.Code, ts.outcomeRelabel) {
		llm.Code = ts.outcomeRelabel
		llm.Message = defaultUserMessage(ts.outcomeRelabel)
	}

	written := message
	if !allowAbandoned && !trustedMessage {
		// Friendly short-circuit for rate-limit messages whose caller-supplied
		// copy is already generic and safe (rate_limit: policyRule (retry
		// after Ns)); translation reuses it. This is the ADR-051 §RD5
		// MAJ-001/004 carve-out — the classifier still RECOGNIZES rate-limit
		// shape, but the emitted Content is the caller-provided message
		// verbatim (no double translate, no model-name leak from MAJ-003).
		if llm.Code == CodeRateLimited {
			written = message
		} else if llm.Code != CodeUnknown || providerErr != nil {
			written = llm.Message
		}
	}

	ts.persistErrorTranscript(kind, stage, llm, written)
}

func (ts *turnState) canWriteErrorTranscript(kind, stage, message string, allowAbandoned bool) bool {
	if ts == nil {
		return false
	}
	if ts.abandoned.Load() && !allowAbandoned {
		abandonedWritesSuppressed.Add(1)
		ts.warnAbandonedTranscriptWrite("appendErrorTranscript")
		return false
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		transcriptSuppressedErrors.Add(1)
		logger.WarnCF(
			"agent",
			"appendErrorTranscript: suppressed (no transcript store wired) — error event will NOT appear in replay",
			map[string]any{
				"event_kind":  kind,
				"stage":       stage,
				"message_len": len(message),
			},
		)
		return false
	}
	return true
}

func (ts *turnState) persistErrorTranscript(kind, stage string, llm LLMError, content string) {
	entry := session.TranscriptEntry{
		ID:             uuid.New().String(),
		Type:           session.EntryTypeSystem,
		AgentID:        ts.resolveActiveAgentID(),
		Content:        content,
		Timestamp:      time.Now().UTC(),
		ErrorCode:      string(llm.Code),
		ErrorRetryable: llm.Retryable,
		// Status="error" lets the replay path distinguish error entries from
		// informational system entries (e.g. compaction summaries) without
		// parsing the free-text Content.
		Status: "error",
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
		logger.WarnCF("agent", "could not record error to transcript",
			map[string]any{
				"session_id": ts.transcriptSessionID,
				"event_kind": kind,
				"stage":      stage,
				"error":      err.Error(),
			})
	}
}
