// rule_ask_repeat_grant_test.go: ADR-092 §5.7 fix — two bugs found while
// verifying docs/security.md's claim that choosing "Always Allow" on the
// one-dialog rule_ask card records an exact grant so the same command does
// not prompt again in that chat.
//
//  1. The grant IS recorded under the card's own args — which, for a
//     rule_ask prompt, carry the adr092_kind:"rule_ask" + note augmentation
//     ruleAskRequestArgs builds — but resolveAskPolicy's own repeat-call
//     grant lookup used to check the tool's bare (unaugmented) args, a
//     different JSON fingerprint. That mismatch never actually re-prompted a
//     human a second time (CheckGrantOrRequestApproval's OWN inner lookup
//     used the correctly augmented args and caught the grant), but it did
//     force every repeat call through requestAskApproval, which leads to bug
//     2 below.
//  2. requestAskApproval wrote the `pending` transcript placeholder
//     unconditionally, before ever consulting the grant store — so a call a
//     standing grant already settles could still flash an "awaiting
//     approval" card for a human nobody was actually about to ask.
//
// Both are fixed by loop_policy.go's checkStandingGrant (the one grant
// lookup every call site along this path now shares) being consulted, with
// the SAME args a grant would be recorded under, BEFORE either the repeat-
// call fast path returns "approved" or the placeholder is written.

package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// grantRecordingApprover mimics the wire "Always Allow" / "Approve Once"
// actions (pkg/gateway/rest_tool_registry.go's HandleToolApprovals +
// approvalGrantRecorder): on approval it optionally ALSO records a session
// grant into the loop's ApprovalGrants store, using the SAME args
// PolicyApprovalReq carries — exactly what production does, since
// HandleToolApprovals records the approval entry's own Args, which were set
// from this same req.Args when the approval was created
// (CheckGrantOrRequestApproval -> approver.RequestApproval(PolicyApprovalReq{
// Args: cloneStringAnyMap(args)})). alwaysAllow selects "Always Allow" (grant
// recorded) vs. "Approve Once" (approved, no grant) so one fake drives both
// halves of the wire contract.
type grantRecordingApprover struct {
	mu          sync.Mutex
	reqs        []PolicyApprovalReq
	al          *AgentLoop
	alwaysAllow bool
}

func (a *grantRecordingApprover) RequestApproval(_ context.Context, req PolicyApprovalReq) (bool, string, bool) {
	a.mu.Lock()
	a.reqs = append(a.reqs, req)
	a.mu.Unlock()
	if a.alwaysAllow {
		a.al.ApprovalGrants().Record(req.SessionID, req.AgentID, req.ToolName, req.Args)
	}
	return true, "", false
}

func (a *grantRecordingApprover) requests() []PolicyApprovalReq {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]PolicyApprovalReq(nil), a.reqs...)
}

// ruleAskRepeatCmd is the one D3-ask-matching bash command both tests below
// call twice, verbatim, in the same chat session.
const ruleAskRepeatCmd = `{"command":"echo repeatgrant-99"}`

func ruleAskRepeatRules() []shellrule.Rule {
	return []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: "echo"}}
}

// TestAutoApprove_RuleAsk_AlwaysAllowSuppressesRepeatPrompt is item 1: proves
// (or would have refuted) docs/security.md's claim. Two identical bash calls
// in the same chat session: the first gets "Always Allow" on the rule_ask
// card, and the second must reach the approver ZERO times and still run.
func TestAutoApprove_RuleAsk_AlwaysAllowSuppressesRepeatPrompt(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{autoToolCall("repeat-call-1", "bash", ruleAskRepeatCmd)}).WithText("first done").
		WithToolCalls([]providers.ToolCall{autoToolCall("repeat-call-2", "bash", ruleAskRepeatCmd)}).WithText("second done")
	al, _ := ruleAskLoop(t, provider, ruleAskRepeatRules(), true)
	approver := &grantRecordingApprover{al: al, alwaysAllow: true}
	al.SetToolApprover(approver)

	_, err := al.ProcessDirect(context.Background(), "run it", "repeat-grant-session")
	require.NoError(t, err)
	reqs := approver.requests()
	require.Len(t, reqs, 1, "first call: exactly one dialog")
	assert.Equal(t, "rule_ask", reqs[0].Args["adr092_kind"], "the one dialog carries the D3 context")
	assert.Contains(t, toolResultText(t, provider, "repeat-call-1"), "repeatgrant-99", "first call runs")

	_, err = al.ProcessDirect(context.Background(), "run it again", "repeat-grant-session")
	require.NoError(t, err)
	assert.Len(t, approver.requests(), 1,
		"second identical call in the same session must reach the approver ZERO more times — "+
			"the Always Allow grant settles it")
	assert.Contains(t, toolResultText(t, provider, "repeat-call-2"), "repeatgrant-99",
		"second call still runs, without ever having been asked again")
}

// TestAutoApprove_RuleAsk_ApproveOnceDoesNotSuppressRepeatPrompt is item 1's
// companion: "Approve Once" on the rule_ask card must NOT install a grant, so
// an identical repeat call prompts again.
func TestAutoApprove_RuleAsk_ApproveOnceDoesNotSuppressRepeatPrompt(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{autoToolCall("once-call-1", "bash", ruleAskRepeatCmd)}).WithText("first done").
		WithToolCalls([]providers.ToolCall{autoToolCall("once-call-2", "bash", ruleAskRepeatCmd)}).WithText("second done")
	al, _ := ruleAskLoop(t, provider, ruleAskRepeatRules(), true)
	approver := &grantRecordingApprover{al: al, alwaysAllow: false}
	al.SetToolApprover(approver)

	_, err := al.ProcessDirect(context.Background(), "run it", "approve-once-session")
	require.NoError(t, err)
	require.Len(t, approver.requests(), 1, "first call: exactly one dialog")

	_, err = al.ProcessDirect(context.Background(), "run it again", "approve-once-session")
	require.NoError(t, err)
	assert.Len(t, approver.requests(), 2,
		"Approve Once records no grant, so an identical repeat call must prompt again")
}

// TestAutoApprove_RuleAsk_NoPlaceholderWhenGranted is item 2: a call a
// standing grant already settles must write ZERO `pending` transcript
// placeholders, while a call that genuinely blocks on a human writes exactly
// one. askPendingPlaceholdersWritten (approval_transcript.go) is the direct
// instrument — recordAskPendingToolCall is the ONLY writer of a `pending`
// placeholder, and both call sites that could reach it (resolveAskPolicy's
// fast path, requestAskApproval) now consult the grant store first.
func TestAutoApprove_RuleAsk_NoPlaceholderWhenGranted(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{autoToolCall("placeholder-call-1", "bash", ruleAskRepeatCmd)}).WithText("first done").
		WithToolCalls([]providers.ToolCall{autoToolCall("placeholder-call-2", "bash", ruleAskRepeatCmd)}).WithText("second done")
	al, _ := ruleAskLoop(t, provider, ruleAskRepeatRules(), true)
	approver := &grantRecordingApprover{al: al, alwaysAllow: true}
	al.SetToolApprover(approver)

	before := AskPendingPlaceholdersWritten()
	_, err := al.ProcessDirect(context.Background(), "run it", "placeholder-session")
	require.NoError(t, err)
	afterFirst := AskPendingPlaceholdersWritten()
	assert.Equal(t, before+1, afterFirst,
		"a call that genuinely blocks on a human writes exactly one pending placeholder")

	_, err = al.ProcessDirect(context.Background(), "run it again", "placeholder-session")
	require.NoError(t, err)
	afterSecond := AskPendingPlaceholdersWritten()
	assert.Equal(t, afterFirst, afterSecond,
		"a call the Always Allow grant already settles must write ZERO pending placeholders — "+
			"nobody was asked, so no 'awaiting approval' card may render for it")
}
