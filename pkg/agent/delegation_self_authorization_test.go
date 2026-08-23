// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// End-to-end regression for issue #636 at the ENFORCEMENT GATE.
//
// pkg/workspace proves the planted edge never reaches the gate. This file
// proves the other half — that the gate itself DENIES — because "the reader
// doesn't return it" and "the delegation is refused" are different claims, and
// only the second one is the security property.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run 'SelfAuthoriz' -p 1 ./pkg/agent/

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// plantEdgeInWorkspaceRecord writes a `delegation` array into
// workspaces/<id>.json — the file a sandboxed `bash` child can rewrite during
// any re-rooted workspace turn (the kernel policy grants $OMNIPUS_HOME RWX and
// fspolicy.DeniedPathsFor re-admits the whole `workspaces` root whenever the
// turn's work dir is a descendant of it).
//
// It goes through a raw map because workspace.Workspace has no Delegation
// field any more — which is the point: removing the field stops OUR writers,
// but the attacker writes bytes, not Go structs, so only the file's LOCATION
// stops them.
func plantEdgeInWorkspaceRecord(t *testing.T, home, wsID string, planted []map[string]any) {
	t.Helper()
	path := filepath.Join(home, "workspaces", wsID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workspace record: %v", err)
	}
	var rec map[string]any
	if jerr := json.Unmarshal(raw, &rec); jerr != nil {
		t.Fatalf("parse workspace record: %v", jerr)
	}
	rec["delegation"] = planted
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal tampered record: %v", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write tampered record: %v", err)
	}
}

// TestDelegationDenyChecker_SelfAuthorizationViaWorkspaceRecordIsRefused is the
// attack from issue #636, driven through the real enforcement gate.
//
// A delegated child ("worker") has no outgoing edge at all. It writes
// {"from_agent":"worker","to_agent":"ray"} into its own workspace record — a
// SHAPE-LEGAL edge, indistinguishable from one an operator created in the Team
// tab, so no amount of re-validation would reject it. The gate must still
// deny, because it never reads the list from there.
func TestDelegationDenyChecker_SelfAuthorizationViaWorkspaceRecordIsRefused(t *testing.T) {
	// Legitimate graph: mia→ray only. Worker has nothing.
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"direct"}, nil),
	})

	// Control BEFORE the attack: worker→ray is denied on the honest graph, so
	// a later "denied" result cannot be mistaken for a pre-existing failure of
	// the fixture.
	workerCheck := buildDelegationDenyCheckerForDelegate(
		"worker", config.AgentDefaults{}, config.DelegationModeBackground)
	if denial := workerCheck(ctxWS(testWS, 0), "ray"); denial == nil {
		t.Fatal("fixture is wrong: worker→ray must already be denied before the attack")
	}

	// The attack: worker grants itself worker→ray by appending to its own
	// workspace record.
	plantEdgeInWorkspaceRecord(t, home, testWS, []map[string]any{
		{"from_agent": "worker", "to_agent": "ray", "modes": []string{"direct"}},
	})

	denial := workerCheck(ctxWS(testWS, 0), "ray")
	if denial == nil {
		t.Fatal("a delegated child granted ITSELF delegation by writing its own workspace record and the " +
			"gate allowed it — this is issue #636, the self-authorization the delegation store exists to close")
	}
	if denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected a trust_set denial, got %q (%s)", denial.Policy, denial.Reason)
	}

	// CONTROL AFTER the attack: the honest edge in the STORE still authorizes,
	// so the denial above proves the SOURCE is distrusted — not that the gate
	// has simply stopped working (a gate that denies everything would pass the
	// assertion above for entirely the wrong reason).
	miaCheck := buildDelegationDenyCheckerForDelegate(
		"mia", config.AgentDefaults{}, config.DelegationModeBackground)
	if denial := miaCheck(ctxWS(testWS, 0), "ray"); denial != nil {
		t.Fatalf("the legitimate mia→ray edge must still authorize; got deny: %+v", denial)
	}
}

// TestDelegationDenyChecker_PlantedRecordCannotWidenAnExistingEdge is the
// subtler half of the same attack: the child does not need a brand-new edge if
// it can WIDEN one.
//
// Here worker→ray exists legitimately with a per-edge cap of 1 — the gate
// allows a call made at chain depth 0 and denies one at depth 1
// (enforceEdgeModeAndDepth: denied when currentDepth >= cap). The child
// rewrites the record with the same edge at depth 3, which would authorize the
// depth-1 hop.
//
// A reader that merged the two sources — or preferred the record — would hand
// the gate the widened cap. It must keep enforcing the stored one.
func TestDelegationDenyChecker_PlantedRecordCannotWidenAnExistingEdge(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("worker", "ray", []string{"direct"}, intPtr(1)), // onward delegation capped at chain depth 1
	})

	check := buildDelegationDenyCheckerForDelegate(
		"worker", config.AgentDefaults{}, config.DelegationModeBackground)

	// Control: the stored cap of 1 allows depth 0 and denies depth 1, BEFORE
	// the attack. Without both halves, "denied after the attack" could mean the
	// edge was never live at all.
	if denial := check(ctxWS(testWS, 0), "ray"); denial != nil {
		t.Fatalf("fixture is wrong: a depth-1 edge must authorize a depth-0 call; got %+v", denial)
	}
	if denial := check(ctxWS(testWS, 1), "ray"); denial == nil {
		t.Fatal("fixture is wrong: a depth-1 edge must deny a depth-1 call before the attack")
	}

	plantEdgeInWorkspaceRecord(t, home, testWS, []map[string]any{
		{"from_agent": "worker", "to_agent": "ray", "modes": []string{"direct"}, "depth": 3},
	})

	if denial := check(ctxWS(testWS, 1), "ray"); denial == nil {
		t.Fatal("a depth cap was widened by rewriting the child-writable workspace record — " +
			"the gate must enforce the stored edge, never a planted one")
	}

	// Control: the stored edge still authorizes exactly what it always did, so
	// the denial above is the SOURCE being distrusted, not the gate breaking.
	if denial := check(ctxWS(testWS, 0), "ray"); denial != nil {
		t.Fatalf("the legitimate depth-1 worker→ray edge must still authorize a depth-0 call; got %+v", denial)
	}
}
