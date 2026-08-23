package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
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
// consumes (id, is_default). It deliberately mirrors only the fields
// ReadDelegation / ResolveDefaultID read from the workspace record.
//
// It carries NO delegation field, and must not gain one: the edge list moved
// OUT of this record because the record is writable by the sandboxed child the
// delegation decision constrains (issue #636, pkg/workspace/delegationstore.go).
// A fixture that seeded edges here would be seeding them where nothing reads
// them — every delegation test would then pass vacuously on an empty graph.
type testWorkspaceRecord struct {
	ID        string `json:"id"`
	IsDefault bool   `json:"is_default,omitempty"`
}

// testDelegationStoreRecord mirrors the on-disk shape of a delegation-store
// file ($OMNIPUS_HOME/entities/delegation/<id>.json) — the ONLY place the
// runtime gate reads edges from.
type testDelegationStoreRecord struct {
	WorkspaceID string      `json:"workspace_id"`
	Delegation  []graphEdge `json:"delegation,omitempty"`
}

// writeGraphFiles writes the workspace record and the delegation-store record
// for wsID under home. The store path is derived via
// workspace.DelegationStorePath rather than hardcoded, so a future relocation
// of the store breaks this fixture loudly instead of silently seeding a graph
// nothing reads.
func writeGraphFiles(t *testing.T, home, wsID string, isDefault bool, edges []graphEdge) {
	t.Helper()

	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspaces: %v", err)
	}
	data, marshalErr := json.Marshal(testWorkspaceRecord{ID: wsID, IsDefault: isDefault})
	if marshalErr != nil {
		t.Fatalf("marshal workspace record: %v", marshalErr)
	}
	if writeErr := os.WriteFile(filepath.Join(wsDir, wsID+".json"), data, 0o644); writeErr != nil {
		t.Fatalf("write workspace file: %v", writeErr)
	}

	storePath, pathErr := workspace.DelegationStorePath(home, wsID)
	if pathErr != nil {
		t.Fatalf("delegation store path: %v", pathErr)
	}
	if mkErr := os.MkdirAll(filepath.Dir(storePath), 0o700); mkErr != nil {
		t.Fatalf("mkdir delegation store: %v", mkErr)
	}
	if len(edges) == 0 {
		// "No delegation" has exactly one on-disk representation: no file.
		if rmErr := os.Remove(storePath); rmErr != nil && !os.IsNotExist(rmErr) {
			t.Fatalf("remove delegation store record: %v", rmErr)
		}
		return
	}
	storeData, storeMarshalErr := json.Marshal(testDelegationStoreRecord{WorkspaceID: wsID, Delegation: edges})
	if storeMarshalErr != nil {
		t.Fatalf("marshal delegation store record: %v", storeMarshalErr)
	}
	if storeWriteErr := os.WriteFile(storePath, storeData, 0o600); storeWriteErr != nil {
		t.Fatalf("write delegation store record: %v", storeWriteErr)
	}
}

// seedWorkspaceGraph points OMNIPUS_HOME at a fresh temp dir (auto-restored) and
// writes workspaces/<id>.json plus the delegation-store record carrying the
// given edges. When isDefault is true the workspace record is flagged
// is_default so workspace.ResolveDefaultID finds it. Returns the home dir.
func seedWorkspaceGraph(t *testing.T, wsID string, isDefault bool, edges []graphEdge) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	writeGraphFiles(t, home, wsID, isDefault, edges)
	return home
}

// rewriteWorkspaceGraph overwrites the workspace + delegation-store records
// under an EXISTING home (set by a prior seedWorkspaceGraph) with a new edge
// set. Used to prove the runtime checker re-reads the graph per-call (an edit
// takes effect without a checker rebuild).
func rewriteWorkspaceGraph(t *testing.T, home, wsID string, isDefault bool, edges []graphEdge) {
	t.Helper()
	writeGraphFiles(t, home, wsID, isDefault, edges)
}

// ctxWS returns a context at the given delegation depth bound to workspace wsID.
func ctxWS(wsID string, depth int) context.Context {
	return tools.WithWorkspaceID(ctxAtDepth(depth), wsID)
}

// edge is a terse constructor for a graphEdge.
func edge(from, to string, modes []string, depth *int) graphEdge {
	return graphEdge{FromAgent: from, ToAgent: to, Modes: modes, Depth: depth}
}
