// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Fixture helper: seed the default workspace's delegation graph for the
// ADR-091 fixture home.
//
// The U5a launch gate (pkg/agent/steer_launcher.go::startingRemainingDepth)
// consults the resolved (for an unbound turn, the is_default) workspace's
// delegation graph for the caller→target edge of every launch whose steering
// session has an identified owner. The ADR-091 fixtures write a fixture
// workspace that is NOT flagged is_default and carries no delegation edges, so
// every launch in this package refused (steer.ErrInvalidEdge) where it used to
// run once the gate landed. This seeds an is_default workspace carrying a
// directed edge for every ordered pair of the fixture's agents, restoring the
// graph-free latitude the fixtures were written against.
package adr091_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// adr091DelegationDefaultWorkspaceID is the id of the is_default workspace this
// helper seeds. Kept distinct from the fixtures' own "adr091-fixture-workspace"
// so the two never collide.
const adr091DelegationDefaultWorkspaceID = "adr091-fixture-delegation-default"

// seedADR091DelegationGraph writes an is_default workspace under home with a
// delegation edge for every ordered pair of agentIDs (self edges included),
// unless home already carries an is_default workspace of its own. The workspace
// record carries no core_team, so it stays invisible to
// workspace.FindForAgent's membership scan.
func seedADR091DelegationGraph(t *testing.T, home string, agentIDs []string) {
	t.Helper()

	if def, err := workspace.ResolveDefaultID(home); err == nil && def != "" && def != adr091DelegationDefaultWorkspaceID {
		return // a fixture seeded its own default graph — never touch it
	}

	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		t.Fatalf("seedADR091DelegationGraph: create workspaces dir: %v", err)
	}
	record, err := json.Marshal(map[string]any{
		"id":         adr091DelegationDefaultWorkspaceID,
		"is_default": true,
	})
	if err != nil {
		t.Fatalf("seedADR091DelegationGraph: marshal workspace record: %v", err)
	}
	if writeErr := os.WriteFile(filepath.Join(wsDir, adr091DelegationDefaultWorkspaceID+".json"), record, 0o600); writeErr != nil {
		t.Fatalf("seedADR091DelegationGraph: write workspace record: %v", writeErr)
	}

	edges := make([]workspace.DelegationEdge, 0, len(agentIDs)*len(agentIDs))
	for _, from := range agentIDs {
		for _, to := range agentIDs {
			edges = append(edges, workspace.DelegationEdge{FromAgent: from, ToAgent: to})
		}
	}
	if len(edges) == 0 {
		return
	}
	if saveErr := workspace.SaveDelegation(home, adr091DelegationDefaultWorkspaceID, edges); saveErr != nil {
		t.Fatalf("seedADR091DelegationGraph: save delegation: %v", saveErr)
	}
}
