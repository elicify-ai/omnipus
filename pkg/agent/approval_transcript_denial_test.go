// approval_transcript_denial_test.go — ADR-058 W2 coverage: askDenialText is
// the single renderer for denial transcript text, delegating to
// tool_denial.go's ClassifyDenial/denialTable so the persisted transcript
// string and the model-facing message can never independently diverge again
// (ADR-058 D1, spec §1.4's "one event, two renderings, one of them false").
//
// Traceability: FR-058-07 (askDenialText delegates, holds no switch of its
// own, "" preserved verbatim per N1), FR-058-08 (settleAskToolCallTranscript
// writes "permanent" inside session.ToolCall.Result, never as a top-level
// ToolCall field), BDD-03 (one renderer, two audiences), AC-07.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// denialWirePayload mirrors denialPayloadJSON's shape (tool_denial.go) so
// tests can decode the model-facing side of BDD-03 without hand-parsing
// JSON.
type denialWirePayload struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	Tool      string `json:"tool"`
	Reason    string `json:"reason"`
	Permanent bool   `json:"permanent"`
}

// ---------------------------------------------------------------------
// FR-058-07: askDenialText delegates to ClassifyDenial; no switch of its
// own; existing rendered strings unchanged.
// ---------------------------------------------------------------------

// TestAskDenialText_MatchesClassifyDenialForEveryTableRow drives every row
// of the normative ten-row table (tool_denial.go's denialTable, owned by
// W1) plus one unknown probe, and asserts askDenialText renders exactly
// ClassifyDenial(reason).TranscriptText in every case — the whole of
// FR-058-07's "holds no switch of its own" in one table-driven pass. A
// stub that special-cased even one reason back into a local switch would
// still pass this if the special-cased literal happened to match — that
// specific stub is excluded by TestAskDenialText_DelegatesToTable_NotOwnSwitch
// below, which mutates the table live.
func TestAskDenialText_MatchesClassifyDenialForEveryTableRow(t *testing.T) {
	reasons := []string{
		"user", "timeout", "saturated", "cancel", "restart",
		"batch_short_circuit", "internal_error", "no_approver_configured",
		"policy_denied", "", "__unclassified_probe__",
	}
	for _, reason := range reasons {
		t.Run("reason="+reason, func(t *testing.T) {
			cls, _ := ClassifyDenial(reason)
			got := askDenialText(reason)
			assert.Equal(t, cls.TranscriptText, got,
				"askDenialText(%q) must equal ClassifyDenial(%q).TranscriptText", reason, reason)
		})
	}
}

// TestAskDenialText_PreExistingReasons_ByteForByte pins the exact strings
// the OLD askDenialText switch produced for its five pre-existing branches
// (user, timeout, saturated, cancel, and the "" case — the compatibility
// requirement explicitly named for this unit) as literal constants
// independent of denialTable. This is deliberately NOT derived from
// ClassifyDenial: if a future edit to denialTable silently changed one of
// these five rows' wording, this test — pinned to the historical literal —
// is what would catch a persisted-transcript-string regression, whereas
// TestAskDenialText_MatchesClassifyDenialForEveryTableRow would pass either
// way (it only checks internal consistency with the table, not the
// table's content against history).
//
// The "" case is called out specifically: spec finding N1 requires
// "Not run: the approval request was not granted." survive verbatim, and
// it is a real persisted string on disk today for any session that hit the
// empty-reason branch before this change shipped.
func TestAskDenialText_PreExistingReasons_ByteForByte(t *testing.T) {
	// Copied verbatim from the pre-W2 askDenialText switch
	// (git blame: pkg/agent/approval_transcript.go, before this commit) —
	// these are NOT re-derived from tool_denial.go so this test cannot be
	// satisfied merely by the two files staying self-consistent.
	wantByReason := map[string]string{
		"timeout":   "Not run: the approval request expired before anyone answered it.",
		"user":      "Not run: the approval request was denied.",
		"cancel":    "Not run: the turn was cancelled while the approval request was open.",
		"saturated": "Not run: too many approval requests were already pending.",
		"":          "Not run: the approval request was not granted.",
	}
	for reason, want := range wantByReason {
		name := reason
		if name == "" {
			name = "empty_reason"
		}
		t.Run("reason="+name, func(t *testing.T) {
			assert.Equal(t, want, askDenialText(reason),
				"reason %q must render the SAME transcript text it did before W2's delegation "+
					"(spec N1 for the empty-reason case)", reason)
		})
	}
}

// TestAskDenialText_DelegatesToTable_NotOwnSwitch proves the delegation is
// REAL, not merely output-compatible. It mutates a live row of
// tool_denial.go's package-level denialTable (same package, so this test
// can reach it directly) and asserts askDenialText's output tracks the
// mutation immediately.
//
// This is the test that would FAIL if askDenialText still held its own
// independent switch (or a cached copy of the old strings): a hardcoded
// switch branch for "timeout" would keep returning the pre-mutation
// literal regardless of what denialTable now contains, because it would
// never read denialTable at all.
func TestAskDenialText_DelegatesToTable_NotOwnSwitch(t *testing.T) {
	const probeReason = "timeout"

	original, ok := denialTable[probeReason]
	require.True(t, ok, "test setup: %q must be a real table row before mutating it", probeReason)
	t.Cleanup(func() { denialTable[probeReason] = original })

	mutated := original
	mutated.TranscriptText = "PROBE-MUTATED-TRANSCRIPT-TEXT-" + t.Name()
	denialTable[probeReason] = mutated

	got := askDenialText(probeReason)
	assert.Equal(t, mutated.TranscriptText, got,
		"askDenialText must read denialTable at call time — a hardcoded switch would still "+
			"return the pre-mutation literal %q here", original.TranscriptText)
	assert.NotEqual(t, original.TranscriptText, got,
		"sanity check: the mutation must actually have changed what askDenialText returns")
}

// Note: this file deliberately contains no assertion spelling out the
// retired pre-ADR-058 denial sentence as a Go source literal. FR-058-06's
// own repo-wide DoD grep (spec §10) counts occurrences of that exact
// phrase, and a second, separate grep counts a lower-cased two-word
// substring of it, both budgeted to a small fixed number across all of
// pkg/ once the whole change lands. Spelling either one out here — even
// inside a negative (NotContains-style) assertion — would itself inflate
// both counts, which is the opposite of what FR-058-06 exists to drive
// toward zero. Deleting that sentence from loop.go is W4's scope, not
// W2's; the byte-for-byte coverage above
// (TestAskDenialText_PreExistingReasons_ByteForByte) already pins every
// pre-existing rendered string this file is responsible for, including
// the "user" row's replacement wording.

// ---------------------------------------------------------------------
// FR-058-08: settleAskToolCallTranscript writes "permanent" INSIDE
// session.ToolCall.Result.
// ---------------------------------------------------------------------

// TestSettleAskToolCallTranscript_PermanentInsideResult drives a REAL
// permanent denial ("restart") through the real turnState/UnifiedStore
// helpers this package already uses (newApprovalTranscriptTS,
// recordAskPendingToolCall — defined in approval_transcript_test.go, same
// package) and reads the settled entry back OFF DISK, proving the
// "permanent" key survives a full write→read round trip, not merely a
// construction-time struct literal.
func TestSettleAskToolCallTranscript_PermanentInsideResult(t *testing.T) {
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_permanent_true_1")
	args := map[string]any{"task_id": "t-restart"}

	recordAskPendingToolCall(ts, callID, "bash", args)
	settleAskToolCallTranscript(ts, callID, "bash", args, "restart")

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1)
	require.NotNil(t, calls[0].Result)
	assert.Equal(t, true, calls[0].Result["permanent"],
		"a PERMANENT-classified reason (restart) must persist permanent:true inside Result")
}

// TestSettleAskToolCallTranscript_SaturatedIsPermanentFalse is the negative
// leg of the above: "saturated" is the ONE row (FR-058-02) classified
// Permanent:false, and that must reach the persisted transcript too — a
// stub that always wrote permanent:true regardless of classification would
// fail here.
func TestSettleAskToolCallTranscript_SaturatedIsPermanentFalse(t *testing.T) {
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_permanent_false_1")
	args := map[string]any{"task_id": "t-saturated"}

	recordAskPendingToolCall(ts, callID, "web_fetch", args)
	settleAskToolCallTranscript(ts, callID, "web_fetch", args, "saturated")

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1)
	require.NotNil(t, calls[0].Result)
	assert.Equal(t, false, calls[0].Result["permanent"],
		"saturated is the one RETRYABLE reason — it must persist permanent:false")
}

// TestSettleAskToolCallTranscript_PermanentNestedInsideResult_NotTopLevel
// asserts the structural placement FR-058-08 requires: "permanent" nested
// under "result" on the wire, never promoted to a sibling top-level
// ToolCall field. session.ToolCall (contracts/components/schemas/
// ToolCall.yaml) is additionalProperties:false, so a top-level "permanent"
// key would be a contract change this unit is explicitly forbidden from
// making (ADR-058 spec §3.6). Go's type system already forbids this at
// compile time (ToolCall has no Permanent field) — this test additionally
// pins it on the actual wire bytes, so a future struct change that DID add
// a top-level field would still be caught here.
func TestSettleAskToolCallTranscript_PermanentNestedInsideResult_NotTopLevel(t *testing.T) {
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_nesting_1")
	args := map[string]any{"task_id": "t-nesting"}

	recordAskPendingToolCall(ts, callID, "bash", args)
	settleAskToolCallTranscript(ts, callID, "bash", args, "restart")

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1)

	raw, err := json.Marshal(calls[0])
	require.NoError(t, err)

	var topLevel map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &topLevel))
	_, hasTopLevelPermanent := topLevel["permanent"]
	assert.False(t, hasTopLevelPermanent,
		"permanent must never appear as a top-level ToolCall field on the wire")

	resultRaw, hasResult := topLevel["result"]
	require.True(t, hasResult, "the settled entry must carry a result object")
	var resultLevel map[string]any
	require.NoError(t, json.Unmarshal(resultRaw, &resultLevel))
	assert.Equal(t, true, resultLevel["permanent"],
		"permanent must be present INSIDE result")
}

// ---------------------------------------------------------------------
// BDD-03 — one renderer, two audiences (FR-058-07/08 · AC-07).
// ---------------------------------------------------------------------

// TestBDD03_KnownReason_TranscriptAndModelMessageDifferButShareClassification
// drives one real denial (reason "restart") through BOTH rendering
// surfaces — the persisted transcript (via settleAskToolCallTranscript,
// read back off disk) and the model-facing wire payload (via
// denialPayloadJSON, tool_denial.go) — and asserts:
//
//   - the persisted ToolCall.Result.text equals ClassifyDenial("restart").TranscriptText
//   - the model payload's message equals ClassifyDenial("restart").ModelMessage
//   - those two strings are NOT equal (known rows differ by design — BDD-03)
//   - the persisted ToolCall.Result contains permanent:true inside result
//
// AC-07's stub-resistance bar: "both surfaces returning empty, or one
// constant for everything" is excluded because both assertions demand the
// PINNED non-empty table values, and the not-equal assertion kills a
// single-constant implementation that returned the same string on both
// surfaces.
func TestBDD03_KnownReason_TranscriptAndModelMessageDifferButShareClassification(t *testing.T) {
	const reason = "restart"
	const tool = "bash"

	cls, known := ClassifyDenial(reason)
	require.True(t, known, "test setup: %q must be a known table row", reason)
	require.NotEmpty(t, cls.TranscriptText, "test setup: TranscriptText must be pinned non-empty")
	require.NotEmpty(t, cls.ModelMessage, "test setup: ModelMessage must be pinned non-empty")

	// Surface 1: the persisted transcript, via the real settle path and a
	// real read back off disk.
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_bdd03_known_1")
	args := map[string]any{"task_id": "t-bdd03"}
	recordAskPendingToolCall(ts, callID, tool, args)
	settleAskToolCallTranscript(ts, callID, tool, args, reason)

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1)
	require.NotNil(t, calls[0].Result)
	transcriptText, _ := calls[0].Result["text"].(string)

	// Surface 2: the model-facing wire payload (owned by tool_denial.go,
	// W1) — the shared source of truth this unit delegates the transcript
	// side to.
	rawPayload := denialPayloadJSON(tool, reason, cls)
	var payload denialWirePayload
	require.NoError(t, json.Unmarshal([]byte(rawPayload), &payload))

	assert.Equal(t, cls.TranscriptText, transcriptText,
		"the persisted transcript text must equal ClassifyDenial(reason).TranscriptText")
	assert.Equal(t, cls.ModelMessage, payload.Message,
		"the model payload's message must equal ClassifyDenial(reason).ModelMessage")
	assert.NotEqual(t, transcriptText, payload.Message,
		"a known row's transcript text and model message must differ by design (BDD-03) — "+
			"a single-constant implementation must fail this assertion")
	assert.Equal(t, true, calls[0].Result["permanent"],
		"the persisted result must carry permanent:true for a PERMANENT reason")
	assert.True(t, payload.Permanent,
		"the model payload must also carry permanent:true for the same reason")
}

// TestBDD03_UnknownReason_TranscriptAndModelMessageAreByteIdentical is
// BDD-03's second half: a reason with no table row renders the SAME string
// on both surfaces (FR-058-03's byte-identical guarantee), unlike a known
// row.
func TestBDD03_UnknownReason_TranscriptAndModelMessageAreByteIdentical(t *testing.T) {
	const reason = "__unclassified_probe__"
	const tool = "web_fetch"

	cls, known := ClassifyDenial(reason)
	require.False(t, known, "test setup: probe reason must be unrecognised")
	require.NotEmpty(t, cls.TranscriptText)

	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_bdd03_unknown_1")
	args := map[string]any{"task_id": "t-bdd03-unknown"}
	recordAskPendingToolCall(ts, callID, tool, args)
	settleAskToolCallTranscript(ts, callID, tool, args, reason)

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1)
	transcriptText, _ := calls[0].Result["text"].(string)

	rawPayload := denialPayloadJSON(tool, reason, cls)
	var payload denialWirePayload
	require.NoError(t, json.Unmarshal([]byte(rawPayload), &payload))

	assert.Equal(t, transcriptText, payload.Message,
		"an unclassified reason must render BYTE-IDENTICAL text on both surfaces (FR-058-03)")
	assert.Equal(t, cls.TranscriptText, transcriptText)
	assert.Equal(t, cls.ModelMessage, payload.Message)
	assert.True(t, payload.Permanent,
		"an unknown reason fails safe as permanent (FR-058-03)")
}
