// Omnipus — ADR-067 US-9 (P0), FR-052/FR-053: workspace isolation for the
// knowledge tools.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// Fixtures. Every helper here is prefixed b5 so it cannot collide with the
// helpers the other test files in this package already define.
// ---------------------------------------------------------------------------

var b5Seq atomic.Uint64

// b5Home returns a fresh $OMNIPUS_HOME.
func b5Home(t *testing.T) string {
	t.Helper()
	return b5Real(t, t.TempDir())
}

// b5Real resolves a path the way this package does, so a macOS /var →
// /private/var symlink cannot make an otherwise-correct assertion fail.
func b5Real(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return filepath.Clean(resolved)
}

// b5Workspace seeds a minimal valid workspace record and returns its id.
func b5Workspace(t *testing.T, home string) string {
	t.Helper()
	id := "ws-" + strconv.FormatUint(b5Seq.Add(1), 10)
	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: id, Name: id, Status: "active", CreatedAt: now, UpdatedAt: now,
	}))
	return id
}

// b5Vault creates a knowledge base at dir with an Omnipus marker naming it.
func b5Vault(t *testing.T, dir, displayName string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, MarkerDirName), 0o700))
	raw, err := json.Marshal(Marker{DisplayName: displayName})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, MarkerDirName, markerFileName), raw, 0o600))
	return b5Real(t, dir)
}

// b5Note writes a note inside a collection.
func b5Note(t *testing.T, root, relPath, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
}

// b5Mount mounts hostPath into workspace wsID under name.
func b5Mount(t *testing.T, home, wsID, name, hostPath string) {
	t.Helper()
	_, _, err := workspace.CreateMount(home, wsID, name, hostPath)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// US-9 AS-1/AS-2 — the isolation negative test. This is the requirement.
// ---------------------------------------------------------------------------

// TestScope_CrossWorkspaceCollectionIsNotAddressable is the scope-layer half of
// spec test 26 and of AC-7.1.
//
// A knowledge base mounted ONLY into workspace B must be invisible to an agent
// in workspace A: not in the enumeration, not selectable by name, not
// selectable by its own absolute path, and not contained by A's roots.
//
// The positive control in the same test is what keeps it from passing
// vacuously. A scope resolution that returned nothing for EVERY workspace —
// a typo'd home, a mount that never persisted, an enumeration that silently
// found nothing — would satisfy the negative half perfectly. Asserting that
// workspace B DOES see the same collection is the only thing that distinguishes
// "isolated" from "broken".
func TestScope_CrossWorkspaceCollectionIsNotAddressable(t *testing.T) {
	home := b5Home(t)
	hostParent := b5Real(t, t.TempDir())
	vaultB := b5Vault(t, filepath.Join(hostParent, "vault-b"), "Vault B")

	wsA := b5Workspace(t, home)
	wsB := b5Workspace(t, home)
	b5Mount(t, home, wsB, "notes", vaultB)

	// --- positive control: B can address it ---
	scopeB := ResolveScope(home, wsB)
	require.Len(t, scopeB.Collections(), 1,
		"workspace B mounted this collection and must see it — without this half the "+
			"negative assertions below are satisfied by any scope that finds nothing at all")
	gotB, ok := scopeB.Select("Vault B")
	require.True(t, ok)
	assert.Equal(t, vaultB, gotB.Root)

	// --- the requirement: A cannot ---
	scopeA := ResolveScope(home, wsA)
	assert.Empty(t, scopeA.Collections(),
		"a knowledge base mounted only into workspace B must not appear in workspace A's scope (US-9 AS-1)")
	assert.Empty(t, scopeA.Names(),
		"workspace A must not even learn the NAME of workspace B's collection")

	_, ok = scopeA.Select("Vault B")
	assert.False(t, ok, "workspace B's collection must not be selectable by name from workspace A (US-9 AS-2)")
	_, ok = scopeA.Select("notes")
	assert.False(t, ok, "nor by the mount name it carries in workspace B")
	_, ok = scopeA.Select(vaultB)
	assert.False(t, ok, "nor by its absolute path — naming the path must not become an escape hatch")
	assert.False(t, scopeA.Contains(vaultB),
		"workspace B's collection root must not be inside any of workspace A's allowed roots")
}

// TestScope_SharedMountVisibleToBoth is spec test 27 (US-9 AS-3): isolation is
// per-workspace, not per-collection. One host folder mounted into two
// workspaces is ONE collection, addressable from both, with the same identity
// (FR-031's resolved real path).
func TestScope_SharedMountVisibleToBoth(t *testing.T) {
	home := b5Home(t)
	shared := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "shared"), "Shared")

	wsA := b5Workspace(t, home)
	wsB := b5Workspace(t, home)
	b5Mount(t, home, wsA, "shared", shared)
	b5Mount(t, home, wsB, "shared-elsewhere", shared)

	for _, ws := range []string{wsA, wsB} {
		got, ok := ResolveScope(home, ws).Select("Shared")
		require.Truef(t, ok, "workspace %s mounted the shared collection and must see it", ws)
		assert.Equal(t, shared, got.Root,
			"both workspaces must resolve the shared collection to the same real path (FR-031)")
	}
}

// TestScope_NoWorkspaceGrantsNothing pins the fail-closed direction.
//
// An empty or unknown workspace id is the shape a tool call takes when the
// calling agent is on no workspace team at all. The dangerous reading of that
// state is "no workspace, therefore no restriction", which is the unscoped
// search US-9 forbids and which would look exactly like a working feature.
func TestScope_NoWorkspaceGrantsNothing(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	for name, id := range map[string]string{
		"empty":   "",
		"unknown": "no-such-workspace",
	} {
		t.Run(name, func(t *testing.T) {
			s := ResolveScope(home, id)
			assert.Empty(t, s.Collections(), "a %s workspace id must grant no collection", name)
			assert.Empty(t, s.Roots())
			_, ok := s.Select("Vault")
			assert.False(t, ok)
			assert.False(t, s.Contains(vault))
		})
	}

	t.Run("empty home", func(t *testing.T) {
		s := ResolveScope("", ws)
		assert.Empty(t, s.Collections(), "an empty home must grant nothing, never the process working directory")
	})
}

// ---------------------------------------------------------------------------
// D7 "KBs WITHIN those roots", and FR-020's detection rule
// ---------------------------------------------------------------------------

// TestScope_FindsCollectionsBelowAMountedParent covers the ordinary operator
// shape: the mount is a parent folder and the vault sits inside it. D7 says
// "KBs within those roots", not "roots that are KBs".
//
// It also pins FR-020/US-4 AS-3 at the scope layer: a folder full of .md files
// with NEITHER marker is an ordinary folder and must not become a collection.
func TestScope_FindsCollectionsBelowAMountedParent(t *testing.T) {
	home := b5Home(t)
	parent := b5Real(t, t.TempDir())
	nested := b5Vault(t, filepath.Join(parent, "team", "vault"), "Nested")

	// A sibling folder of markdown with no marker at all.
	plain := filepath.Join(parent, "just-markdown")
	b5Note(t, plain, "note.md", "# Not a vault\n")

	// An Obsidian-only vault: detection accepts either marker (FR-020).
	obsidian := filepath.Join(parent, "obsidian-vault")
	require.NoError(t, os.MkdirAll(filepath.Join(obsidian, ObsidianMarkerDirName), 0o700))

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "everything", parent)

	s := ResolveScope(home, ws)
	roots := make([]string, 0, len(s.Collections()))
	for _, c := range s.Collections() {
		roots = append(roots, c.Root)
	}
	assert.ElementsMatch(t, []string{nested, b5Real(t, obsidian)}, roots,
		"both markers make a knowledge base; a folder of .md files with neither does not (FR-020, US-4 AS-3)")

	_, ok := s.Select("Nested")
	assert.True(t, ok, "a collection below the mount root must be addressable by its display name")
	assert.NotContains(t, s.Names(), filepath.Base(plain),
		"an ordinary markdown folder must never be offered as a knowledge base")
}

// TestScope_SymlinkOutOfTheMountIsNotFollowed is the isolation property a
// string-level containment check cannot provide (FR-044, and US-10's reasoning
// applied to scope resolution).
//
// A symlink planted inside workspace A's own mounted folder, pointing at
// workspace B's vault, must not pull that vault into A's scope. Skipping the
// link is the rule; following it and then checking containment is not, because
// by then the other workspace's collection is already enumerated.
func TestScope_SymlinkOutOfTheMountIsNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	home := b5Home(t)
	hostParent := b5Real(t, t.TempDir())
	vaultB := b5Vault(t, filepath.Join(hostParent, "vault-b"), "Vault B")

	ownedByA := filepath.Join(hostParent, "a-folder")
	require.NoError(t, os.MkdirAll(ownedByA, 0o755))
	require.NoError(t, os.Symlink(vaultB, filepath.Join(ownedByA, "sneaky")))

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "a-folder", ownedByA)

	s := ResolveScope(home, ws)
	assert.Empty(t, s.Collections(),
		"a symlink pointing out of the mounted folder must be SKIPPED, not followed (FR-044)")
	_, ok := s.Select("Vault B")
	assert.False(t, ok)
}

// TestScope_DepthBoundIsEnforced pins ScopeMaxDepth as a real bound rather than
// a comment. A collection at exactly the bound is enumerated; one a level
// deeper is not.
//
// The two collections live in SEPARATE branches on purpose. An earlier version
// of this test put the deeper one INSIDE the shallower one, and it passed with
// the depth guard deleted — because FR-026's "do not descend into a collection"
// rule was excluding it, not the depth bound. The test measured the wrong
// mechanism and would have reported a removed bound as green.
func TestScope_DepthBoundIsEnforced(t *testing.T) {
	home := b5Home(t)
	parent := b5Real(t, t.TempDir())

	atLimitDir := parent
	for i := 0; i < ScopeMaxDepth; i++ {
		atLimitDir = filepath.Join(atLimitDir, "a"+strconv.Itoa(i))
	}
	b5Vault(t, atLimitDir, "At Limit")

	tooDeepDir := parent
	for i := 0; i <= ScopeMaxDepth; i++ {
		tooDeepDir = filepath.Join(tooDeepDir, "b"+strconv.Itoa(i))
	}
	b5Vault(t, tooDeepDir, "Too Deep")

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "parent", parent)

	names := ResolveScope(home, ws).Names()
	assert.Contains(t, names, "At Limit", "a collection at exactly ScopeMaxDepth must be found")
	assert.NotContains(t, names, "Too Deep", "enumeration must stop at ScopeMaxDepth")
}

// TestScope_NestedCollectionInsideACollectionIsNotASecondOne pins FR-026: a
// knowledge base is exactly one folder, so a marker below one is not a second
// collection.
func TestScope_NestedCollectionInsideACollectionIsNotASecondOne(t *testing.T) {
	home := b5Home(t)
	outer := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "outer"), "Outer")
	b5Vault(t, filepath.Join(outer, "inner"), "Inner")

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "outer", outer)

	s := ResolveScope(home, ws)
	assert.Equal(t, []string{"Outer"}, s.Names(),
		"a marker inside a collection does not make a second collection (FR-026)")
}

// ---------------------------------------------------------------------------
// Select semantics
// ---------------------------------------------------------------------------

// TestScope_SelectRules covers how an agent addresses a collection: by display
// name, by mount name, by absolute path, and the empty-argument rule.
func TestScope_SelectRules(t *testing.T) {
	home := b5Home(t)
	parent := b5Real(t, t.TempDir())
	one := b5Vault(t, filepath.Join(parent, "one"), "Alpha")

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "alpha-mount", one)

	s := ResolveScope(home, ws)

	t.Run("empty selects the only collection", func(t *testing.T) {
		got, ok := s.Select("")
		require.True(t, ok)
		assert.Equal(t, one, got.Root)
	})
	t.Run("by display name, case-insensitively", func(t *testing.T) {
		got, ok := s.Select("alpha")
		require.True(t, ok)
		assert.Equal(t, one, got.Root)
	})
	t.Run("by mount name", func(t *testing.T) {
		got, ok := s.Select("alpha-mount")
		require.True(t, ok)
		assert.Equal(t, one, got.Root)
	})
	t.Run("by absolute path", func(t *testing.T) {
		got, ok := s.Select(one)
		require.True(t, ok)
		assert.Equal(t, one, got.Root)
	})
	t.Run("unknown name selects nothing", func(t *testing.T) {
		_, ok := s.Select("Beta")
		assert.False(t, ok)
	})

	// A second collection makes the empty argument ambiguous, and ambiguity
	// must be refused rather than guessed: picking one silently would send an
	// agent's search at a collection it did not name.
	two := b5Vault(t, filepath.Join(parent, "two"), "Beta")
	b5Mount(t, home, ws, "beta-mount", two)
	s2 := ResolveScope(home, ws)
	require.Len(t, s2.Collections(), 2)
	_, ok := s2.Select("")
	assert.False(t, ok, "with two collections in scope, an unnamed collection must not be guessed")
}

// TestScope_WorkspaceWorkTreeCollectionIsInScope covers D11: Omnipus creates
// knowledge bases inside the workspace's own work tree, and a scope built only
// from mounts would make every one of them unreachable by its own tools.
func TestScope_WorkspaceWorkTreeCollectionIsInScope(t *testing.T) {
	home := b5Home(t)
	ws := b5Workspace(t, home)

	workDir, err := workspace.SafeWorkDir(home, ws)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(workDir, 0o700))
	created := b5Vault(t, filepath.Join(workDir, "my-notes"), "My Notes")

	s := ResolveScope(home, ws)
	got, ok := s.Select("My Notes")
	require.True(t, ok, "a knowledge base created in the workspace work tree must be addressable (D11)")
	assert.Equal(t, created, got.Root)
	assert.Equal(t, WorkTreeOrigin, got.Origin)

	// And it stays private to its workspace, exactly like a mount.
	other := b5Workspace(t, home)
	_, ok = ResolveScope(home, other).Select("My Notes")
	assert.False(t, ok, "another workspace must not reach into this workspace's work tree")
}
