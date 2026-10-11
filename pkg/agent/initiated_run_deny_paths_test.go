// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

// S1 M11: authorizeInitiatedRunFor is the ONE decision for a task or plan run
// an agent started, and four of its deny paths had no test at all. Each is
// driven directly here, so a regression in any single path cannot hide behind
// another path's refusal.
//
// ORACLE: the function's own documented contract
// (loop_delegation_initiated.go: "It checks, failing closed at each step") plus
// the founder ruling of 2026-10-10 (the delegation policy always applies). The
// policy enum each step must report is the point: a trust-set failure and a
// budget failure are different refusals, and a caller that only checks
// "denied" cannot tell them apart.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestAuthorizeInitiatedRunFor_DenyPaths pins each fail-closed step, with a
// positive control so a test that refuses everything cannot pass.
func TestAuthorizeInitiatedRunFor_DenyPaths(t *testing.T) {
	cases := []struct {
		name string
		// ini is the initiator the executor would carry.
		ini task.Initiator
		// depth is the configured performance.max_delegation_depth.
		depth int
		// breakLifecycle makes the initiator's lifecycle record UNREADABLE
		// (a directory where the record file belongs: ENOTDIR/EISDIR for every
		// caller, root included).
		breakLifecycle bool
		wantPolicy     tools.DelegationDenyReason
		wantReason     string
	}{
		{
			name:       "no identified initiating agent",
			ini:        task.Initiator{AgentID: ""},
			depth:      2,
			wantPolicy: tools.DenyTrustSet,
			wantReason: "identified initiating agent",
		},
		{
			name:       "invalid global delegation depth",
			ini:        task.Initiator{AgentID: rtCaller},
			depth:      -1,
			wantPolicy: tools.DenyDepth,
			wantReason: "delegation is disabled",
		},
		{
			name:           "unreadable initiator session record",
			ini:            task.Initiator{AgentID: rtCaller, SessionID: "sess-unreadable", Depth: 0},
			depth:          2,
			breakLifecycle: true,
			wantPolicy:     tools.DenyDepth,
			wantReason:     "cannot be read",
		},
		{
			// The edge check passes (depth 0 of 3); it is the INHERITED budget
			// that is already spent, which is exactly the case the edge check
			// alone cannot catch.
			name:       "no onward delegation budget left",
			ini:        task.Initiator{AgentID: rtCaller, Depth: 0, Inherited: intPtr(0)},
			depth:      3,
			wantPolicy: tools.DenyDepth,
			wantReason: "no onward delegation budget",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
			al.GetConfig().Performance.MaxDelegationDepth = tc.depth
			if tc.breakLifecycle {
				ls := al.GetSessionLifecycleStore()
				require.NotNil(t, ls, "fixture: the loop must have a lifecycle store")
				blocked := filepath.Join(ls.Dir(), tc.ini.SessionID+".jsonl")
				require.NoError(t, os.MkdirAll(blocked, 0o700))
				// Instrument: the read must really fail, and not as "not found".
				_, lerr := ls.Load(tc.ini.SessionID)
				require.Error(t, lerr, "instrument: the lifecycle read must fail")
				require.False(t, errors.Is(lerr, os.ErrNotExist),
					"instrument: the failure must be a read fault, not a missing record")
			}

			got, denial := al.authorizeInitiatedRunFor(
				context.Background(), tc.ini, rtAssignee, testWS, false)
			require.Nil(t, got, "a denied run carries no InitiatedBy")
			require.NotNil(t, denial, "this path must refuse")
			assert.Equal(t, tc.wantPolicy, denial.Policy, "the refusal must report the right policy class")
			assert.Contains(t, denial.Reason, tc.wantReason, "the refusal must say why")
			assert.Equal(t, rtAssignee, denial.TargetAgentID, "the denial names the assignee it refused")
		})
	}
}

// TestAuthorizeInitiatedRunFor_Control_EdgeWithinBudgetIsAuthorized is the
// positive control: the same shape as the budget denial, one step inside it.
func TestAuthorizeInitiatedRunFor_Control_EdgeWithinBudgetIsAuthorized(t *testing.T) {
	al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
	al.GetConfig().Performance.MaxDelegationDepth = 2

	got, denial := al.authorizeInitiatedRunFor(
		context.Background(), task.Initiator{AgentID: rtCaller, SessionID: "", Depth: 0}, rtAssignee, testWS, false)
	require.Nil(t, denial, "an edge within budget must be authorized: %+v", denial)
	require.NotNil(t, got)
	assert.Equal(t, rtCaller, got.AgentID)
	assert.Equal(t, 1, got.Depth, "the run's depth is the initiator's depth plus one")
	assert.Equal(t, 1, got.Authorization.RemainingDepth, "one level of the budget is spent on this run")
	assert.Equal(t, string(session.AuthorizationModeTask), string(got.Authorization.Mode))
}

// TestAuthorizeInitiatedRunFor_StoredSelfExemptionStillChecksTheBudget pins the
// ONE exemption (founder ruling F13): an automatic start of a task whose stored
// initiator is the assignee skips the self-EDGE, never the budget. Without the
// budget check the exemption would be an unbounded start.
func TestAuthorizeInitiatedRunFor_StoredSelfExemptionStillChecksTheBudget(t *testing.T) {
	// No self-edge in the graph at all: the exemption must carry this case.
	al, _ := newRunTaskPolicyLoop(t, nil)
	al.GetConfig().Performance.MaxDelegationDepth = 2

	within, denial := al.authorizeInitiatedRunFor(
		context.Background(), task.Initiator{AgentID: rtAssignee, Depth: 0}, rtAssignee, testWS, true)
	require.Nil(t, denial, "the stored-initiator exemption must skip the self-edge: %+v", denial)
	require.NotNil(t, within)

	over, denial := al.authorizeInitiatedRunFor(
		context.Background(), task.Initiator{AgentID: rtAssignee, Depth: 2}, rtAssignee, testWS, true)
	require.Nil(t, over, "the exemption is not a budget bypass")
	require.NotNil(t, denial, "the exemption must still refuse with no budget left")
	assert.Equal(t, tools.DenyDepth, denial.Policy)
	assert.True(t, strings.Contains(denial.Reason, "no onward delegation budget"), denial.Reason)
}
