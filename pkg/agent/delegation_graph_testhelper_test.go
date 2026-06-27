package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// graphEdge is the test-side mirror of the on-disk delegation edge. Modes empty
// = all modes; Depth nil = inherit. JSON tags match the production
// workspace.DelegationEdge / storedDelegationEdge.
type graphEdge struct {
	FromAgent string   `json:"from_agent"`
	ToAgent   string   `json:"to_agent"`
	Modes     []string `json:"modes,omitempty"`
	Depth     *int     `json:"depth,omitempty"`
}

// testWorkspaceRecord is the minimal on-disk workspace shape the graph reader
// consumes (id, is_default, delegation). It deliberately mirrors only the fields
// ReadDelegation / ResolveDefaultID read.
type testWorkspaceRecord struct {
	ID         string      `json:"id"`
	IsDefault  bool        `json:"is_default,omitempty"`
	Delegation []graphEdge `json:"delegation,omitempty"`
}

// seedWorkspaceGraph points OMNIPUS_HOME at a fresh temp dir (auto-restored) and
// writes workspaces/<id>.json carrying the given delegation edges. When isDefault
// is true the record is flagged is_default so workspace.ResolveDefaultID finds it.
// Returns the home dir.
func seedWorkspaceGraph(t *testing.T, wsID string, isDefault bool, edges []graphEdge) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspaces: %v", err)
	}
	rec := testWorkspaceRecord{ID: wsID, IsDefault: isDefault, Delegation: edges}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal workspace record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, wsID+".json"), data, 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	return home
}

// rewriteWorkspaceGraph overwrites workspaces/<id>.json under an EXISTING home
// (set by a prior seedWorkspaceGraph) with a new edge set. Used to prove the
// runtime checker re-reads the graph per-call (an edit takes effect without a
// checker rebuild).
func rewriteWorkspaceGraph(t *testing.T, home, wsID string, isDefault bool, edges []graphEdge) {
	t.Helper()
	rec := testWorkspaceRecord{ID: wsID, IsDefault: isDefault, Delegation: edges}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal workspace record: %v", err)
	}
	path := filepath.Join(home, "workspaces", wsID+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("rewrite workspace file: %v", err)
	}
}

// ctxWS returns a context at the given delegation depth bound to workspace wsID.
func ctxWS(wsID string, depth int) context.Context {
	return tools.WithWorkspaceID(ctxAtDepth(depth), wsID)
}

// edge is a terse constructor for a graphEdge.
func edge(from, to string, modes []string, depth *int) graphEdge {
	return graphEdge{FromAgent: from, ToAgent: to, Modes: modes, Depth: depth}
}
