// subturn_ask_deny_test.go — ADR-075 D1 test 30 (FR-032, issue #659).
//
// THE DEFECT. `AutoDenyAsk` means "there is no operator on this run, so an
// `ask`-policy tool must be DENIED rather than queued for an approval nobody
// can answer". It is set true only for headless/scheduled runs
// (ProcessScheduled). A delegated child of such a run is just as unattended as
// its parent — no second operator appeared because the work was delegated —
// but the child's processOptions were built without the flag. Its first
// `ask`-policy tool therefore issued an approval request into a run with
// nobody watching, and the turn blocked until the approval registry's timeout.
//
// WHY ADR-075 MAKES IT URGENT rather than tidy. Under D1 a delegated sub-turn
// browses its workspace's SIGNED-IN browser, and D2.9 seeds
// browser_upload_file as `ask` for every agent. Without the inheritance the
// first delegated sub-turn to reach it hangs — §0.2 records this as the
// prerequisite for the risk D1 accepts.
//
// THE NEGATIVE HALF IS THE POINT. Forcing auto-deny on for EVERY delegated
// child would pass any "the ask tool was denied" assertion while silently
// converting an interactive user's delegation into blanket denials — an
// operator IS attached to that run and the prompt is what they expect. So the
// interactive case is asserted too, in the same file, with the same harness.

package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// recordingPolicyApprover is the observable for "an approval request was
// created". It is a PolicyApprover — the SAME seam the gateway wires its
// approval registry and its tool_approval_required WS frame into — so a
// non-zero count here means a real operator prompt was raised.
//
// It denies rather than hanging, so the interactive half of this test
// terminates instead of blocking for the registry's 300-second timeout.
type recordingPolicyApprover struct{ requests atomic.Int32 }

func (r *recordingPolicyApprover) RequestApproval(
	_ context.Context, _ PolicyApprovalReq,
) (bool, string) {
	r.requests.Add(1)
	return false, "denied by the test approver"
}

func (r *recordingPolicyApprover) calls() int32 { return r.requests.Load() }

// askDenyFixture builds a parent agent and a delegation TARGET whose scripted
// provider calls one `ask`-policy tool and then stops. parentAutoDenyAsk is the
// only variable between this test's two halves.
func askDenyFixture(
	t *testing.T, parentAutoDenyAsk bool,
) (result *tools.ToolResult, stub *namedStubTool, approver *recordingPolicyApprover) {
	t.Helper()
	al, home := schedTestLoop(t)

	const toolName = "ask_policy_tool"

	// The parent never makes an LLM call in this test — spawnSubTurn is
	// driven directly, which is the same entry point DelegateTool.executeSync
	// reaches through SubTurnSpawner.
	parent := registerAgent(t, al, home, "mia", testutil.NewScenario().WithText("parent idle"), true)

	targetProvider := testutil.NewScenario().
		WithToolCall(toolName, `{}`).
		WithText("understood — I could not use that tool")
	target := registerAgent(t, al, home, "worker", targetProvider, false)

	stub = &namedStubTool{name: toolName}
	target.Tools.Register(stub)
	target.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{toolName: "ask"},
	})

	approver = &recordingPolicyApprover{}
	al.SetToolApprover(approver)

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-ask-deny",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               parent,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     sessionStore,
		opts: processOptions{
			// THE ONE VARIABLE. True is what ProcessScheduled sets for a
			// headless run; false is an ordinary interactive turn.
			AutoDenyAsk: parentAutoDenyAsk,
		},
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-ask-deny")
	res, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "use the ask-policy tool",
		TargetAgentID: "worker",
		Async:         false,
	})
	require.NoError(t, err, "spawnSubTurn must not error")
	require.NotNil(t, res)
	require.False(t, res.IsError, "the delegated turn must COMPLETE, not fail: %s", res.ForLLM)
	return res, stub, approver
}

// TestSubTurn_AskPolicyIsAutoDenied — the headless case. The sub-turn reaches
// an `ask`-policy tool, the call is denied inside the turn, no approval
// request is created, and the turn does not block.
func TestSubTurn_AskPolicyIsAutoDenied(t *testing.T) {
	res, stub, approver := askDenyFixture(t, true)

	assert.False(t, stub.wasCalled.Load(),
		"an ask-policy tool must not EXECUTE on a headless delegated sub-turn")
	assert.Zero(t, approver.calls(),
		"NO approval request may be created: there is no operator on this run, so asking is a "+
			"guaranteed wait for an answer that cannot come. A non-zero count here is issue #659 — "+
			"AutoDenyAsk not reaching the delegated child")

	// The turn finished with the model's own follow-up, i.e. it saw the denial
	// and carried on rather than hanging.
	assert.Contains(t, res.ForLLM, "could not use that tool",
		"the sub-turn must complete after the denial, not block")

	// The denial carries the HEADLESS reason specifically — a dedicated
	// denialTable row with headless guidance, not the generic refusal template.
	cls, known := ClassifyDenial(autoDenyHeadlessReason)
	require.True(t, known)
	assert.True(t, cls.Permanent,
		"a delegated headless run has no operator for its whole duration, so the denial is permanent")
	assert.Contains(t, strings.ToLower(cls.ModelMessage), "headless")
}

// TestSubTurn_AskPolicyStillPromptsWhenAnOperatorIsAttached is the negative
// half, and it is why the fix is INHERITANCE rather than a blanket rule.
//
// Same fixture, same delegation, same ask-policy tool — the only difference is
// that the PARENT turn is an ordinary interactive one. The approval path MUST
// still be reached: an operator is attached, and silently denying instead of
// asking would take away a decision that is theirs to make.
func TestSubTurn_AskPolicyStillPromptsWhenAnOperatorIsAttached(t *testing.T) {
	_, stub, approver := askDenyFixture(t, false)

	assert.Equal(t, int32(1), approver.calls(),
		"with an operator attached, a delegated sub-turn must still ASK. Forcing auto-deny on for "+
			"every delegated child would pass the headless test above while converting an "+
			"interactive user's delegation into blanket denials")
	assert.False(t, stub.wasCalled.Load(),
		"the approver denied, so the tool still must not run — this asserts the ASK HAPPENED, not "+
			"that the tool was allowed")
}

// compile-time: the observable really is the seam the gateway wires.
var _ PolicyApprover = (*recordingPolicyApprover)(nil)
