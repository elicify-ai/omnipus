// Omnipus — drift guard for the Library doors' note-lock derivation (round-3
// cut list, 2026-09-14 review): resolveLibraryLock (the whole-file save door)
// and libraryNoteInCollection (the rename/delete knowledge-cascade door) used
// to carry two independent copies of the same derivation — enclosing
// collection, LockDirFor, NoteLockConfig, collection-relative path — and two
// copies of one rule are how a split lock happens: change one, not the other,
// and the save door and the rename door stop excluding each other over the
// same note while every test that exercises only ONE door stays green.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/library"
)

// TestLibraryLockDerivation_IsOneRuleForBothDoors asserts, over a real
// workspace with a real knowledge base, that BOTH Library doors derive the
// byte-identical lock (collection root, lock directory, collection-relative
// path) for the same note. The derivation is shared
// (resolveCollectionNoteLock), so this test's job is to fail the day someone
// forks one door's side of it again — not merely to observe today's equality.
func TestLibraryLockDerivation_IsOneRuleForBothDoors(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	work := workDir(api, id)
	vaultDir := filepath.Join(work, "KB")
	makeKnowledgeBase(t, vaultDir, "KB")
	require.NoError(t, os.WriteFile(filepath.Join(vaultDir, "note.md"), []byte("hi"), 0o600))

	root, err := library.OpenRoot(api.homePath, id)
	require.NoError(t, err)
	defer root.Close()

	const rel = "KB/note.md"

	saveCfg, saveRel, err := resolveLibraryLock(root, api.homePath, id, rel)
	require.NoError(t, err)

	note, governed, err := api.libraryNoteInCollection(root, rel)
	require.NoError(t, err)
	require.True(t, governed, "fixture sanity: KB/note.md must resolve to a governed note")

	assert.Equal(t, saveCfg, note.lock,
		"the whole-file save door and the rename/delete cascade door must derive the SAME "+
			"lock for the same note — one drifted derivation is a split lock, and two writers "+
			"holding different locks do not exclude each other")
	assert.Equal(t, saveRel, note.relInCol,
		"both doors must key the lock on the same collection-relative path")

	// The shared rule must keep answering the enclosing cases the way each
	// door always did: innermost collection for nesting, no collection for a
	// loose file.
	t.Run("innermost collection", func(t *testing.T) {
		outerDir := filepath.Join(work, "Outer")
		innerDir := filepath.Join(outerDir, "Inner")
		makeKnowledgeBase(t, outerDir, "Outer")
		makeKnowledgeBase(t, innerDir, "Inner")
		require.NoError(t, os.WriteFile(filepath.Join(innerDir, "note.md"), []byte("hi"), 0o600))

		cfg, lockRel, err := resolveLibraryLock(root, api.homePath, id, "Outer/Inner/note.md")
		require.NoError(t, err)
		inner, governed, err := api.libraryNoteInCollection(root, "Outer/Inner/note.md")
		require.NoError(t, err)
		require.True(t, governed)
		assert.Equal(t, cfg, inner.lock, "nested case: both doors must still agree")
		assert.Equal(t, "note.md", lockRel)
		assert.Equal(t, "Outer/Inner", inner.collRel,
			"the INNERMOST collection governs; collRel is its workspace-relative directory")
	})

	t.Run("no enclosing collection", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(work, "loose.md"), []byte("hi"), 0o600))
		cfg, deg, err := resolveLibraryLock(root, api.homePath, id, "loose.md")
		require.NoError(t, err)
		assert.Empty(t, cfg.CollectionRoot, "degraded in-process lock, G7c")
		assert.Equal(t, id+"/loose.md", deg)

		_, governed, err := api.libraryNoteInCollection(root, "loose.md")
		require.NoError(t, err)
		assert.False(t, governed, "a loose file is not governed by any knowledge base")
	})
}
