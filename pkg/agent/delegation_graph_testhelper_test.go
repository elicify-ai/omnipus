package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
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
		if rmErr := os.Remove(storePath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
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

// testHarnessDelegationDefaultID is the id of the is_default workspace the
// shared loop harness (mustNewAgentLoop, via seedDefaultDelegationGraphForLoop)
// seeds when the effective home carries none of its own.
//
// Deliberately DISTINCT from testHarnessWorkspaceMembershipID: that workspace
// carries core_team membership only — never is_default, never a delegation
// edge — while THIS one is the workspace every UNBOUND delegation/launch gate
// resolves (workspace.ResolveDefaultID). Keeping them apart is what lets
// TestDelegationDistinction_RealWiringThroughCreateTask (delegation_enforce_test.go)
// keep asserting a denial: it binds testHarnessWorkspaceMembershipID on the
// turn, which has no delegation edges, while this default workspace is never
// consulted for a bound turn.
const testHarnessDelegationDefaultID = "test-harness-delegation-default"

// seedDefaultDelegationGraph writes an is_default workspace under home that
// carries a directed delegation edge for EVERY ordered pair of agentIDs — self
// edges (a→a) included.
//
// Why it is needed: the U5a launch gate
// (steer_launcher.go::startingRemainingDepth, landed 7b069b66d) consults the
// resolved (for an unbound turn, the is_default) workspace's delegation graph
// for the caller→target edge of every launch whose steering session has an
// identified owner. The launcher/steer fixtures predate that gate and declare
// no graph at all, so a launch with an identified owner now refuses
// (steer.ErrInvalidEdge) where it used to run. Seeding the mesh restores the
// graph-free latitude those fixtures were written against, WITHOUT asserting
// any specific edge a test did not ask for.
//
// Never clobbers a test's own graph: a home that already carries an is_default
// workspace whose id is NOT testHarnessDelegationDefaultID (i.e. the test
// seeded its own with seedWorkspaceGraph) is left byte-for-byte untouched, so
// every graph-refusal test keeps its controlled edge set and its refusal
// reason. The mesh is a UNION across calls, because the shared TestMain home
// is reused by the whole test binary — a later test's agents must be able to
// delegate to each other too.
//
// The workspace record carries NO core_team, so it is invisible to
// workspace.FindForAgent's membership scan and can never make an ordinary
// agent ambiguous across workspaces.
//
// Same discipline as seedTestWorkspaceMembershipForIDs: guarded by the
// package-level testHarnessWorkspaceMu (so it is safe under t.Parallel) and
// written atomically.
func seedDefaultDelegationGraph(t *testing.T, home string, agentIDs []string) {
	t.Helper()
	testHarnessWorkspaceMu.Lock()
	defer testHarnessWorkspaceMu.Unlock()

	if def, err := workspace.ResolveDefaultID(home); err == nil && def != "" && def != testHarnessDelegationDefaultID {
		// The test seeded its own default graph — never touch it.
		return
	}

	// Union the existing mesh (if this default already exists) with the new
	// ordered pairs, so the shared home only ever GROWS coverage.
	type pair struct{ from, to string }
	seen := map[pair]bool{}
	if existing, ok := workspace.LoadDelegation(home, testHarnessDelegationDefaultID); ok {
		for _, e := range existing {
			seen[pair{e.FromAgent, e.ToAgent}] = true
		}
	}
	for _, from := range agentIDs {
		for _, to := range agentIDs {
			seen[pair{from, to}] = true
		}
	}
	edges := make([]graphEdge, 0, len(seen))
	for p := range seen {
		edges = append(edges, edge(p.from, p.to, nil, nil))
	}

	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("seedDefaultDelegationGraph: mkdir %s: %v", wsDir, err)
	}
	wsData, err := json.Marshal(testWorkspaceRecord{ID: testHarnessDelegationDefaultID, IsDefault: true})
	if err != nil {
		t.Fatalf("seedDefaultDelegationGraph: marshal workspace record: %v", err)
	}
	if err := fileutil.WriteFileAtomic(filepath.Join(wsDir, testHarnessDelegationDefaultID+".json"), wsData, 0o644); err != nil {
		t.Fatalf("seedDefaultDelegationGraph: write workspace record: %v", err)
	}

	if len(edges) == 0 {
		return
	}
	storePath, pathErr := workspace.DelegationStorePath(home, testHarnessDelegationDefaultID)
	if pathErr != nil {
		t.Fatalf("seedDefaultDelegationGraph: delegation store path: %v", pathErr)
	}
	if mkErr := os.MkdirAll(filepath.Dir(storePath), 0o700); mkErr != nil {
		t.Fatalf("seedDefaultDelegationGraph: mkdir delegation store: %v", mkErr)
	}
	storeData, marshalErr := json.Marshal(testDelegationStoreRecord{
		WorkspaceID: testHarnessDelegationDefaultID,
		Delegation:  edges,
	})
	if marshalErr != nil {
		t.Fatalf("seedDefaultDelegationGraph: marshal delegation store: %v", marshalErr)
	}
	if wErr := fileutil.WriteFileAtomic(storePath, storeData, 0o600); wErr != nil {
		t.Fatalf("seedDefaultDelegationGraph: write delegation store: %v", wErr)
	}
}

// seedDefaultDelegationGraphForLoop seeds the default delegation mesh under the
// CURRENT effective home for every agent the loop's registry knows. It is the
// single call site every shared loop harness uses.
func seedDefaultDelegationGraphForLoop(t *testing.T, al *AgentLoop) {
	t.Helper()
	ids := al.GetRegistry().ListAgentIDs()
	if len(ids) == 0 {
		ids = testHarnessAgentIDs(al.GetConfig())
	}
	seedDefaultDelegationGraph(t, omnipusHome(), ids)
}
