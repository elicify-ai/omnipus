// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Tests for the delegation STORE's WIRING — that the runtime enforcement read
// (ReadDelegation) actually sources its edges from the store, and that an edge
// planted in the child-writable workspace record authorizes nothing.
//
// Issue #636: the store existed but nothing called it, so the vulnerable read
// was still live. These tests fail if that wiring is ever undone.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run 'Delegation' -p 1 ./pkg/workspace/

package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// plantHostileEdgeInWorkspaceRecord reproduces, byte for byte, what a
// sandboxed `bash` child can do to its own workspace record.
//
// Why it works through a raw map rather than the typed writer: the kernel
// policy grants $OMNIPUS_HOME read/write/execute as one coarse rule, and
// fspolicy.DeniedPathsFor re-admits the whole `workspaces` root for any
// re-rooted workspace turn (the turn's work dir, <home>/workspaces/<id>/work,
// is a proper descendant of it). So the child can rewrite
// <home>/workspaces/<id>.json with arbitrary JSON. It is NOT constrained by
// the Go struct — which is exactly why the fix cannot be "remove the field and
// trust the type": the field's absence stops OUR writers, the file's location
// is what stops the attacker's.
func plantHostileEdgeInWorkspaceRecord(t *testing.T, home, id string, edge map[string]any) {
	t.Helper()
	path := filepath.Join(dirFor(home), id+".json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the workspace record must exist before it is tampered with")
	var rec map[string]any
	require.NoError(t, json.Unmarshal(raw, &rec))
	rec["delegation"] = []map[string]any{edge}
	tampered, err := json.MarshalIndent(rec, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, tampered, 0o600))
}

// seedWorkspaceRecordForDelegation writes a minimal, VALID workspace record —
// the state a real workspace is in before the child tampers with it.
func seedWorkspaceRecordForDelegation(t *testing.T, home, id string, coreTeam ...string) {
	t.Helper()
	require.NoError(t, SaveRecord(home, Workspace{
		ID:        id,
		Name:      "test",
		Status:    "active",
		CoreTeam:  coreTeam,
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-01T00:00:00Z",
	}))
}

// TestHostileDelegationEdgeInWorkspaceRecordAuthorizesNothing is the
// regression test for issue #636 — the self-authorization this store exists to
// close.
//
// The attack, in full: a delegated child agent runs `bash` inside its
// workspace turn. The workspace record is kernel-writable during that turn
// (see plantHostileEdgeInWorkspaceRecord). The child appends
//
//	"delegation": [{"from_agent": "<itself>", "to_agent": "<anyone>"}]
//
// and — before this wiring — ReadDelegation read that array straight back out
// and handed it to the enforcement gate (pkg/agent's
// buildDelegationDenyChecker), so the child had just granted ITSELF delegation
// rights to any agent on the workspace. Per ADR-037 that edge list is the SOLE
// control governing delegation, so there was nothing behind it.
//
// Note what this test does NOT rely on: it does not assert the hostile edge is
// REJECTED by validation. `{"from_agent":"worker","to_agent":"jim"}` is
// SHAPE-LEGAL — indistinguishable from an edge an operator created in the Team
// tab — so re-validating on load passes it. The only durable fix is that
// nothing reads the list from there at all, which is what this asserts.
func TestHostileDelegationEdgeInWorkspaceRecordAuthorizesNothing(t *testing.T) {
	home := t.TempDir()
	const id = "hostile-ws"
	seedWorkspaceRecordForDelegation(t, home, id, "jim", "ava", "worker")

	// The child grants itself delegation to an agent it has no edge to.
	plantHostileEdgeInWorkspaceRecord(t, home, id, map[string]any{
		"from_agent": "worker",
		"to_agent":   "jim",
	})

	edges, err := ReadDelegation(home, id)
	require.NoError(t, err, "a tampered record must not break the read — it must simply not be believed")
	for _, e := range edges {
		require.Falsef(t, e.FromAgent == "worker" && e.ToAgent == "jim",
			"an edge planted in the child-writable workspace record reached the enforcement gate: %+v — "+
				"this is the self-authorization issue #636 describes", e)
	}
	require.Empty(t, edges,
		"a workspace whose only 'edge' was planted in its record must present an EMPTY graph "+
			"(deny-by-default at the gate)")

	// CONTROL — without this the assertion above could pass for the wrong
	// reason (e.g. ReadDelegation silently returning nothing for every
	// workspace). An edge written through the real lifecycle IS honoured.
	unlock := LockID(id)
	require.NoError(t, SaveDelegation(home, id, []DelegationEdge{{FromAgent: "jim", ToAgent: "ava"}}))
	unlock()

	edges, err = ReadDelegation(home, id)
	require.NoError(t, err)
	require.Len(t, edges, 1, "an edge saved through the real lifecycle must be honoured")
	require.Equal(t, "jim", edges[0].FromAgent)
	require.Equal(t, "ava", edges[0].ToAgent)

	// ...and the planted edge is STILL inert alongside a legitimate one: the
	// two sources must never be merged.
	for _, e := range edges {
		require.Falsef(t, e.FromAgent == "worker",
			"the planted edge became live once a legitimate edge existed: %+v", e)
	}
}

// TestSaveDelegationWritesOnlyTheDelegationStore proves the write half: the
// sanctioned writer must not leave a copy of the edge list in the
// child-writable record. A second copy there is not merely redundant — it is a
// live authorization source the moment any future reader picks it up, which is
// how this class of bug returns.
func TestSaveDelegationWritesOnlyTheDelegationStore(t *testing.T) {
	home := t.TempDir()
	const id = "write-ws"
	seedWorkspaceRecordForDelegation(t, home, id, "jim", "ava")

	unlock := LockID(id)
	require.NoError(t, SaveDelegation(home, id, []DelegationEdge{{FromAgent: "jim", ToAgent: "ava"}}))
	unlock()

	storePath, err := DelegationStorePath(home, id)
	require.NoError(t, err)
	require.FileExists(t, storePath, "the edge list must be persisted in the delegation store")

	raw, err := os.ReadFile(filepath.Join(dirFor(home), id+".json"))
	require.NoError(t, err)
	var rec map[string]any
	require.NoError(t, json.Unmarshal(raw, &rec))
	require.NotContains(t, rec, "delegation",
		"the workspace record must carry no delegation array — it is writable by the principal the "+
			"edges constrain, so any copy there is a latent authorization source")
}

// TestWorkspaceRecordRoundTripDropsAPlantedDelegationArray pins the stated
// migration behaviour: an old (or planted) `delegation` array in a workspace
// record is ignored on load and gone on the next save. Importing it into the
// protected store would launder attacker-controlled data into the trusted
// location — the precise thing the store exists to prevent.
func TestWorkspaceRecordRoundTripDropsAPlantedDelegationArray(t *testing.T) {
	home := t.TempDir()
	const id = "legacy-ws"
	seedWorkspaceRecordForDelegation(t, home, id, "jim")
	plantHostileEdgeInWorkspaceRecord(t, home, id, map[string]any{
		"from_agent": "worker",
		"to_agent":   "jim",
	})

	ws, err := loadWorkspaceRecord(home, id)
	require.NoError(t, err)
	require.NoError(t, SaveRecord(home, ws))

	raw, err := os.ReadFile(filepath.Join(dirFor(home), id+".json"))
	require.NoError(t, err)
	var rec map[string]any
	require.NoError(t, json.Unmarshal(raw, &rec))
	require.NotContains(t, rec, "delegation",
		"a planted delegation array must be dropped on the next save, never migrated into the store")

	edges, err := ReadDelegation(home, id)
	require.NoError(t, err)
	require.Empty(t, edges, "nothing from the record may ever reach the enforcement gate")
}

// TestDelegationStorePathIsUnderEntities pins WHERE the protection comes from.
// The store's safety is not a property of the store — it is a property of its
// location: `entities` is in fspolicy.SecretEntriesAlways, denied
// unconditionally on both enforcement layers with no own-tree exception.
// pkg/tools/delegation_store_unreachable_test.go asserts that containment
// against fspolicy's own output; this one keeps the layout honest from inside
// the package that owns it.
func TestDelegationStorePathIsUnderEntities(t *testing.T) {
	home := t.TempDir()
	got, err := DelegationStorePath(home, "ws1")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "entities", "delegation", "ws1.json"), got)
	require.NotContains(t, got, filepath.Join(home, "workspaces"),
		"the delegation store must never sit under the child-writable workspaces root")
}
