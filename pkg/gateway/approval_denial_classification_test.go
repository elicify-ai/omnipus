// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for ADR-058 FR-058-04: the approvals.go denial-reason literals are
// collected into named constants + allApprovalDenialReasons, and every
// member classifies as a known denial via agent.ClassifyDenial. Spec:
// docs/internal/specs/adr-058-tool-denial-semantics-spec.md §8.3 item 4,
// work unit W3 (§9).
//
// Scope: this file owns the constants introduced in pkg/gateway/approvals.go
// only. It does not re-litigate the full ten-row classification table
// (pkg/agent/tool_denial_test.go, W1, covers that) — it proves the specific
// literals THIS package emits match what agent.ClassifyDenial recognizes,
// driven through the real approvalRegistryV2 wherever a real denial flow
// can produce the reason, per §8.1's "no spy that merely records its
// argument" bar.
//
// batch_short_circuit is driven directly at the registry
// (reg.cancelBatchShortCircuit), never through a turn — ADR-058 spec §2.4
// F1 established cancelBatchShortCircuit has zero production callers, so a
// turn-level test would misrepresent a dead control as reachable. Precedent:
// approvals_test.go::TestApprovalRegistry_BatchShortCircuitDeletesEntry.
//
// internal_error (policy_approver.go::policyApproverAdapter.RequestApproval's
// nil-entry branch) is policyApproverAdapter's defensive nil-entry branch —
// requestApproval always returns a non-nil entry today, so there is no real
// flow to drive it through either; it is asserted at the classifier only,
// honestly, for the same reason (spec classification-table row 7: "defensive
// branch; [UNVERIFIED] whether reachable in practice").
package gateway

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllApprovalDenialReasons_EveryMemberClassifiesAsKnown is FR-058-04's
// core requirement: every reason in allApprovalDenialReasons must be a
// known, classified denial in pkg/agent. A reason added to the slice with
// no matching pkg/agent/tool_denial.go table row must fail THIS test, not
// default silently at runtime.
func TestAllApprovalDenialReasons_EveryMemberClassifiesAsKnown(t *testing.T) {
	require.Len(t, allApprovalDenialReasons, 8,
		"six approvals.go literals + internal_error + session canceled; approved must stay excluded")

	seen := make(map[string]bool, len(allApprovalDenialReasons))
	for _, reason := range allApprovalDenialReasons {
		t.Run(reason, func(t *testing.T) {
			require.False(t, seen[reason], "duplicate reason %q in allApprovalDenialReasons", reason)
			seen[reason] = true

			cls, known := agent.ClassifyDenial(reason)
			assert.True(t, known, "reason %q must classify as known", reason)
			assert.Equal(t, reason, cls.Reason, "DenialClass.Reason must echo the classified reason")
			assert.NotEmpty(t, cls.ModelMessage, "reason %q must have a non-empty ModelMessage", reason)
			assert.NotEmpty(t, cls.TranscriptText, "reason %q must have a non-empty TranscriptText", reason)
		})
	}

	assert.NotContains(t, allApprovalDenialReasons, "approved",
		"approved carries Approved:true and is not a denial (ADR-058 spec §2.2)")
}

// TestClassifyDenial_ApprovedIsNotAKnownDenial is the negative control for
// the loop above: "approved" is deliberately absent from
// allApprovalDenialReasons because approvals.go::approvalRegistryV2.resolve's
// ApprovalActionApprove branch sets Approved:true. A classifier that
// returned known=true unconditionally — which would still pass the loop
// above — fails this.
func TestClassifyDenial_ApprovedIsNotAKnownDenial(t *testing.T) {
	_, known := agent.ClassifyDenial("approved")
	assert.False(t, known, `"approved" is not a denial reason and must not classify as known`)
}

// TestAllApprovalDenialReasons_ExactlyOneRetryable cross-checks the
// aggregate slice against pkg/agent's classification: "saturated" is the
// ONE retryable (Permanent:false) reason (ADR-058 spec §3.5, Binding Rule
// 4). A stub classifying everything as permanent — which would still pass
// the "known" loop above — fails this.
func TestAllApprovalDenialReasons_ExactlyOneRetryable(t *testing.T) {
	retryable := 0
	for _, reason := range allApprovalDenialReasons {
		cls, known := agent.ClassifyDenial(reason)
		require.True(t, known, "reason %q must be known", reason)
		if !cls.Permanent {
			retryable++
			assert.Equal(t, denialReasonSaturated, reason,
				"the only retryable reason in this slice must be saturated")
		}
	}
	assert.Equal(t, 1, retryable, "exactly one member of allApprovalDenialReasons must be retryable")
}

// --- real-registry driven denials ---
//
// Each case below drives the actual approvalRegistryV2 to produce a real
// ApprovalOutcome, then asserts (a) the outcome's Reason equals the named
// constant this package now uses at that call site, and (b)
// agent.ClassifyDenial classifies that real, registry-produced value as
// known. This is the "no behaviour change" guarantee made concrete: the
// constants introduced by this change are not merely equal to string
// literals in isolation — they are what the registry actually emits.

// drainOutcome blocks for the entry's real ApprovalOutcome. resultCh is
// buffered (cap 1) and every terminal transition below sends into it
// synchronously before returning, so a real receive — not a mock — proves
// the registry actually reached that state.
func drainOutcome(t *testing.T, ch <-chan ApprovalOutcome) ApprovalOutcome {
	t.Helper()
	select {
	case outcome := <-ch:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ApprovalOutcome from a real registry entry")
		return ApprovalOutcome{}
	}
}

func TestApprovalDenialReasons_Saturated_RealRegistry(t *testing.T) {
	reg := newApprovalRegistryV2(1, 300*time.Second)

	holder, accepted := reg.requestApproval(
		"tc-w3-saturated-holder", "web_fetch",
		map[string]any{"url": "https://example.invalid/holder"},
		"agent-w3-sat", "sess-w3-sat", "turn-w3-sat-1",
	)
	require.True(t, accepted, "first request under cap=1 must be accepted")
	require.NotNil(t, holder)

	overflow, accepted := reg.requestApproval(
		"tc-w3-saturated-overflow", "web_fetch",
		map[string]any{"url": "https://example.invalid/overflow"},
		"agent-w3-sat", "sess-w3-sat", "turn-w3-sat-2",
	)
	require.False(t, accepted, "second request over cap=1 must be rejected as saturated")
	require.NotNil(t, overflow)

	outcome := drainOutcome(t, overflow.resultCh)
	require.False(t, outcome.Approved)
	assert.Equal(t, denialReasonSaturated, outcome.Reason)

	cls, known := agent.ClassifyDenial(outcome.Reason)
	assert.True(t, known)
	assert.False(t, cls.Permanent, "saturated must be the retryable classification (Binding Rule 4)")
}

func TestApprovalDenialReasons_User_RealRegistry(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	entry, accepted := reg.requestApproval(
		"tc-w3-user-deny", "bash",
		map[string]any{"cmd": "ls"},
		"agent-w3-user", "sess-w3-user", "turn-w3-user",
	)
	require.True(t, accepted)

	ok, gone := reg.resolve(entry.ApprovalID, ApprovalActionDeny)
	require.True(t, ok)
	require.False(t, gone)

	outcome := drainOutcome(t, entry.resultCh)
	require.False(t, outcome.Approved)
	assert.Equal(t, denialReasonUser, outcome.Reason)

	cls, known := agent.ClassifyDenial(outcome.Reason)
	assert.True(t, known)
	assert.True(t, cls.Permanent)
}

func TestApprovalDenialReasons_Cancel_RealRegistry(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	entry, accepted := reg.requestApproval(
		"tc-w3-cancel", "run_task",
		map[string]any{"task_id": "t-w3-cancel"},
		"agent-w3-cancel", "sess-w3-cancel", "turn-w3-cancel",
	)
	require.True(t, accepted)

	ok, gone := reg.resolve(entry.ApprovalID, ApprovalActionCancel)
	require.True(t, ok)
	require.False(t, gone)

	outcome := drainOutcome(t, entry.resultCh)
	require.False(t, outcome.Approved)
	assert.Equal(t, denialReasonCancel, outcome.Reason)

	cls, known := agent.ClassifyDenial(outcome.Reason)
	assert.True(t, known)
	assert.True(t, cls.Permanent)
}

func TestApprovalDenialReasons_Restart_RealRegistry(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	entry, accepted := reg.requestApproval(
		"tc-w3-restart", "delegate",
		map[string]any{"target": "jim"},
		"agent-w3-restart", "sess-w3-restart", "turn-w3-restart",
	)
	require.True(t, accepted)

	canceled := reg.cancelAllPendingForRestart()
	require.Len(t, canceled, 1)

	outcome := drainOutcome(t, entry.resultCh)
	require.False(t, outcome.Approved)
	assert.Equal(t, denialReasonRestart, outcome.Reason)

	cls, known := agent.ClassifyDenial(outcome.Reason)
	assert.True(t, known)
	assert.True(t, cls.Permanent)
}

// TestApprovalDenialReasons_SessionCanceled_RealRegistry drives the actual
// production path for denialReasonSessionCanceled: cancelAllPendingForSessions
// with the literal reason pkg/agent/cancel.go::AgentLoop.RequestCancel passes
// through hooks.CancelPendingApprovals (see denialReasonSessionCanceled's
// doc comment). This is not a fixed literal emitted inside approvals.go
// itself — cancelAllPendingForSessions's reason parameter is caller-supplied
// — so this test drives it with the exact value the real caller uses rather
// than asserting the constant in isolation.
func TestApprovalDenialReasons_SessionCanceled_RealRegistry(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	entry, accepted := reg.requestApproval(
		"tc-w3-session-canceled", "web_fetch",
		map[string]any{"url": "https://example.invalid/session-canceled"},
		"agent-w3-session-canceled", "sess-w3-session-canceled", "turn-w3-session-canceled",
	)
	require.True(t, accepted)

	count := reg.cancelAllPendingForSessions([]string{"sess-w3-session-canceled"}, denialReasonSessionCanceled)
	require.Equal(t, 1, count)

	outcome := drainOutcome(t, entry.resultCh)
	require.False(t, outcome.Approved)
	assert.Equal(t, denialReasonSessionCanceled, outcome.Reason)
	assert.Equal(t, "session canceled", outcome.Reason,
		`the production caller's literal must remain exactly "session canceled"`)

	cls, known := agent.ClassifyDenial(outcome.Reason)
	assert.True(t, known, "session canceled must classify as known once pkg/agent/tool_denial.go carries its table row")
	assert.True(t, cls.Permanent)
}

// TestApprovalDenialReasons_BatchShortCircuit_RealRegistry_NeverThroughATurn
// drives cancelBatchShortCircuit directly at the registry, matching
// approvals_test.go::TestApprovalRegistry_BatchShortCircuitDeletesEntry's
// precedent. ADR-058 spec §2.4 F1: this control has zero production
// callers, so it must never be exercised via a simulated turn — that would
// misrepresent a dead control as reachable.
func TestApprovalDenialReasons_BatchShortCircuit_RealRegistry_NeverThroughATurn(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	entry, accepted := reg.requestApproval(
		"tc-w3-batch-sc", "search_web",
		map[string]any{"q": "w3-denial-const"},
		"agent-w3-batch", "sess-w3-batch", "turn-w3-batch",
	)
	require.True(t, accepted)

	ok := reg.cancelBatchShortCircuit(entry.ApprovalID)
	require.True(t, ok, "cancelBatchShortCircuit must return true for a pending entry")

	outcome := drainOutcome(t, entry.resultCh)
	require.False(t, outcome.Approved)
	assert.Equal(t, denialReasonBatchShortCircuit, outcome.Reason)

	cls, known := agent.ClassifyDenial(outcome.Reason)
	assert.True(t, known)
	assert.True(t, cls.Permanent)
}

// TestApprovalDenialReasons_Timeout_RealRegistry_FireTimeoutUnchanged lets
// the real timer fire rather than reaching into internals: fireTimeout
// itself is untouched by this change (ADR-058 spec FR-058-04: "fireTimeout
// is byte-for-byte unchanged") — it still emits the raw literal "timeout"
// rather than referencing denialReasonTimeout. This proves that literal is
// still exactly what allApprovalDenialReasons enumerates and what
// pkg/agent classifies.
func TestApprovalDenialReasons_Timeout_RealRegistry_FireTimeoutUnchanged(t *testing.T) {
	reg := newApprovalRegistryV2(64, 50*time.Millisecond)

	entry, accepted := reg.requestApproval(
		"tc-w3-timeout", "bash",
		map[string]any{"cmd": "sleep 1"},
		"agent-w3-timeout", "sess-w3-timeout", "turn-w3-timeout",
	)
	require.True(t, accepted)

	outcome := drainOutcome(t, entry.resultCh)
	require.False(t, outcome.Approved)
	assert.Equal(t, denialReasonTimeout, outcome.Reason)
	assert.Equal(t, "timeout", outcome.Reason, `fireTimeout's own literal must remain exactly "timeout"`)

	cls, known := agent.ClassifyDenial(outcome.Reason)
	assert.True(t, known)
	assert.True(t, cls.Permanent)
}

// TestApprovalDenialReasons_InternalError_ClassifierOnly asserts the one
// member of allApprovalDenialReasons that this package cannot drive through
// a real flow: internal_error is
// policy_approver.go::policyApproverAdapter.RequestApproval's defensive
// nil-entry branch, reached only when requestApproval returns a nil entry —
// which it never does today. Rather than fabricate that condition, this
// test honestly classifies the literal value directly, matching the spec's
// classification-table row 7 ("defensive branch; [UNVERIFIED] whether
// reachable in practice").
func TestApprovalDenialReasons_InternalError_ClassifierOnly(t *testing.T) {
	require.Contains(t, allApprovalDenialReasons, denialReasonInternalError)
	assert.Equal(t, "internal_error", denialReasonInternalError)

	cls, known := agent.ClassifyDenial(denialReasonInternalError)
	assert.True(t, known)
	assert.True(t, cls.Permanent)
}
