// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 20.
// TestWorkflowStart_W5: the server-side per-workflow rule on a chat
// message's workflow_start — W1 and W18 require target_workspace_id (the
// server refuses the start without it); W5, the write-skill workflow, is
// accepted with or without a target; unknown workflows stay refused.
//
// Oracle: skills-tools-redesign-spec.md C-8 ("workflow enum adds W5 …
// target_workspace_id becomes required for W1 and W18 only; for W5 it is
// optional") and the shipped inbound schema
// pkg/gateway/inboundschemas/WorkflowStart.yaml, which already lists W5 and
// states: "OpenAPI 3.0 cannot express the per-workflow requirement, so the
// server enforces it (C-8)." BDD S-53 (the write-skill start carries the
// person's message and Omnipus loads write-skill) is the client-side half;
// this test pins the server's per-workflow decision, which no production
// code reads at this commit (workflow_start.target_workspace_id has no
// non-test reader in pkg/), so the schema's enforcement promise is the
// missing behaviour.
//
// workflowStartTargetRequired is the test-local seam GREEN wires to the
// server's per-workflow check; nil keeps the RED run loud (BLOCKED).

import (
	"testing"
)

// workflowStartTargetRequired reports whether a chat message's
// workflow_start is refused: true = the server refuses the start (W1/W18
// without a target; an unknown workflow), false = accepted (W5 with or
// without a target; W1/W18 with one).
var workflowStartTargetRequired func(workflow string, targetSet bool) bool

// TestWorkflowStart_W5 (order 20).
// Traces to: BDD S-53; spec C-8; inbound schema WorkflowStart.yaml.
func TestWorkflowStart_W5(t *testing.T) {
	if workflowStartTargetRequired == nil {
		t.Fatal("BLOCKED: the server-side per-workflow enforcement of " +
			"workflow_start is not implemented — required by " +
			"skills-tools-redesign-spec C-8 and the WorkflowStart.yaml schema's " +
			"'the server enforces it (C-8)' note; BDD S-53")
	}
	const refusalNote = "C-8: target_workspace_id is required for W1 and W18 only"

	t.Run("W5 without a target is accepted", func(t *testing.T) {
		if workflowStartTargetRequired("W5", false) {
			t.Fatalf(refusalNote + " — a W5 start without a target must be accepted (C-8, S-53)")
		}
	})

	t.Run("W5 with a target is accepted", func(t *testing.T) {
		if workflowStartTargetRequired("W5", true) {
			t.Fatalf(refusalNote + " — a W5 start with a target must be accepted (C-8)")
		}
	})

	t.Run("W1 without a target is refused", func(t *testing.T) {
		if !workflowStartTargetRequired("W1", false) {
			t.Fatalf("C-8: a W1 start without target_workspace_id must be refused")
		}
	})

	t.Run("W18 without a target is refused", func(t *testing.T) {
		if !workflowStartTargetRequired("W18", false) {
			t.Fatalf("C-8: a W18 start without target_workspace_id must be refused")
		}
	})

	t.Run("an unknown workflow is refused", func(t *testing.T) {
		if !workflowStartTargetRequired("W9", true) {
			t.Fatalf("the workflow enum must stay closed (Wu0a 'unknown workflow' row)")
		}
	})
}
