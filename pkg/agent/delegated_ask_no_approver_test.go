// delegated_ask_no_approver_test.go — D2 capability spec §10 order 0b.
//
// WHAT THIS COVERS THAT subturn_ask_deny_test.go DOES NOT.
//
// That file always wires a PolicyApprover (one that records and denies) and
// varies AutoDenyAsk. This one wires NO approver at all and leaves AutoDenyAsk
// false — an ordinary interactive delegation on a process whose approval
// plumbing is missing (a bare CLI process, or a gateway whose boot did not
// complete). The loop's fail-closed fallback (nopPolicyApprover,
// tool_approver.go) is supposed to deny with "no_approver_configured" rather
// than block, and nothing asserted that a DELEGATED turn survives it.
//
// The oracle is TURN COMPLETION, not the denial string. A turn that hangs
// waiting for an approval nobody can answer is the failure this exists to
// catch, and it is invisible to any assertion about what the denial said.
//
// It is deliberately written against a GENERIC ask-policy tool, never
// browser_upload_file: FR-029 holds that tool's registration until #659 is
// closed, so a test naming it would either not compile or would smuggle the
// held tool into the build.
//
// #659 STATUS AT THE TIME OF WRITING: the code half has landed —
// pkg/agent/subturn.go inherits AutoDenyAsk from the parent turn, with
// TestSubTurn_AskPolicyIsAutoDenied covering it. The GitHub issue is still
// open. This file therefore ships UNSKIPPED (the spec's t.Skip was for a tree
// where the inheritance did not exist), and it does not depend on the
// inheritance at all — it exercises the orthogonal no-approver path.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestDelegatedSubTurn_AskWithNoApprover_Terminates drives a real delegated
// sub-turn whose target reaches an `ask`-policy tool on a loop with no
// PolicyApprover wired, and asserts the turn COMPLETES.
//
// The whole assertion is the completion, bounded by a watchdog well under the
// approval registry's 300 s default: a build that blocks here would otherwise
// look like an ordinary slow test rather than the hang it is.
func TestDelegatedSubTurn_AskWithNoApprover_Terminates(t *testing.T) {
	al, home := schedTestLoop(t)

	const toolName = "ask_policy_tool"

	parent := registerAgent(t, al, home, "mia", testutil.NewScenario().WithText("parent idle"), true)

	targetProvider := testutil.NewScenario().
		WithToolCall(toolName, `{}`).
		WithText("understood — I could not use that tool")
	target := registerAgent(t, al, home, "worker", targetProvider, false)

	stub := &namedStubTool{name: toolName}
	target.Tools.Register(stub)
	target.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{toolName: "ask"},
	})

	// THE VARIABLE: no al.SetToolApprover call anywhere in this test. The loop
	// falls back to nopPolicyApprover, which denies with
	// "no_approver_configured" on the default build. (The auto-APPROVE variant
	// exists only under `//go:build test`, which this suite is not built with —
	// tags are goolm,stdjson.)

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-no-approver",
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
			// An ORDINARY interactive turn. AutoDenyAsk false is deliberate:
			// the headless shortcut is what subturn_ask_deny_test.go covers,
			// and taking it here would mean this test never reaches the
			// approver seam it exists to exercise.
			AutoDenyAsk: false,
		},
	}

	type outcome struct {
		res *tools.ToolResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-no-approver")
		res, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
			Model:         "test-model",
			SystemPrompt:  "use the ask-policy tool",
			TargetAgentID: "worker",
			Async:         false,
		})
		done <- outcome{res: res, err: err}
	}()

	// 30 s is two orders of magnitude above what a completing turn needs here
	// and an order of magnitude below the approval registry's 300 s timeout, so
	// a trip on this watchdog means "blocked on an approval", never "slow host".
	var got outcome
	select {
	case got = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the delegated sub-turn did not finish within 30s. It is blocked on an approval " +
			"request that no operator can answer: no PolicyApprover is wired on this loop, so the " +
			"fail-closed nopPolicyApprover must deny immediately rather than queue. A turn that " +
			"waits here burns the approval registry's full timeout with nothing to show for it")
	}

	require.NoError(t, got.err, "spawnSubTurn must not error")
	require.NotNil(t, got.res)
	require.False(t, got.res.IsError,
		"the delegated turn must COMPLETE, not fail: %s", got.res.ForLLM)

	assert.False(t, stub.wasCalled.Load(),
		"the ask-policy tool must NOT execute when there is no approver — a fallback that runs the "+
			"tool anyway would be a silent auto-approve, which is the exact defect the fail-closed "+
			"nopPolicyApprover replaced")

	// The turn carried on past the denial and produced the model's follow-up.
	// Asserted on the model's own words rather than on the denial payload:
	// the denial reaching the transcript is not the property under test, the
	// turn getting past it is.
	assert.Contains(t, got.res.ForLLM, "could not use that tool",
		"the sub-turn must resume after the denial rather than ending at it")

	// The denial reason the fallback produces must be a KNOWN one, or the
	// agent is handed an unclassified refusal it cannot reason about.
	cls, known := ClassifyDenial(nopApproverDenialReason)
	require.True(t, known,
		"%q is not in denialTable — an operator seeing this reason gets the generic template "+
			"instead of the wiring diagnosis", nopApproverDenialReason)
	assert.True(t, cls.Permanent,
		"a missing approver does not appear mid-turn, so retrying within the turn cannot help")
}
