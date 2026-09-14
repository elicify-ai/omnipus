// Regression tests: Index.UpdatePath on an unchanged file is a no-op (Claude
// review cut-list perf, deferred by fix3/gateway-refresh, fixed on
// fix4/index-perf).
//
// UpdatePath used to rewrite the manifest even for a byte-identical note, so
// the watcher's follow-up pass (~300 ms after every direct refresh) did a
// second full manifest rewrite per edit. Oracle: the manifest file's identity
// and modification time must not move on an unchanged UpdatePath (the manifest
// is written by temp-file + rename, so a rewrite always produces a new file
// even when the bytes are identical — round 3's probe saw 395 -> 395 bytes
// with a moved mtime).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// perfManifestSnapshot pushes the manifest's mtime into the past (so a rewrite
// is visible even with coarse timestamps) and returns its stat.
func perfManifestSnapshot(t *testing.T, ix *Index) os.FileInfo {
	t.Helper()
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(ix.ManifestPath(), past, past))
	info, err := os.Stat(ix.ManifestPath())
	require.NoError(t, err)
	return info
}

func perfManifestRewritten(t *testing.T, ix *Index, before os.FileInfo) bool {
	t.Helper()
	after, err := os.Stat(ix.ManifestPath())
	require.NoError(t, err)
	return !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime())
}

func TestUpdatePath_UnchangedNoteDoesNotRewriteManifest(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)
	b2WriteFile(t, root, "same.md", "a note that will not change at all\n")
	require.NoError(t, ix.UpdatePath(context.Background(), "same.md"))

	before := perfManifestSnapshot(t, ix)
	require.NoError(t, ix.UpdatePath(context.Background(), "same.md"))
	require.False(t, perfManifestRewritten(t, ix, before),
		"UpdatePath on a byte-identical, stat-identical note must not rewrite the manifest")

	hits, err := ix.Search("change", 10)
	require.NoError(t, err)
	require.True(t, containsPath(hits, "same.md"), "the unchanged note must stay findable")
}

func TestUpdatePath_UnchangedAttachmentDoesNotRewriteManifest(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)
	b2WriteFile(t, root, "pic.png", "bytes")
	require.NoError(t, ix.UpdatePath(context.Background(), "pic.png"))

	before := perfManifestSnapshot(t, ix)
	require.NoError(t, ix.UpdatePath(context.Background(), "pic.png"))
	require.False(t, perfManifestRewritten(t, ix, before))
}

func TestUpdatePath_ChangedNoteStillReindexes(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)
	b2WriteFile(t, root, "n.md", "alphaword first version\n")
	require.NoError(t, ix.UpdatePath(context.Background(), "n.md"))

	before := perfManifestSnapshot(t, ix)
	b2WriteFile(t, root, "n.md", "betaword second, longer version of the note\n")
	require.NoError(t, ix.UpdatePath(context.Background(), "n.md"))
	require.True(t, perfManifestRewritten(t, ix, before), "a changed note must update the manifest")

	hits, err := ix.Search("betaword", 10)
	require.NoError(t, err)
	require.True(t, containsPath(hits, "n.md"))
	old, err := ix.Search("alphaword", 10)
	require.NoError(t, err)
	require.False(t, containsPath(old, "n.md"), "the old content must no longer be findable")
}

// A same-size edit whose mtime lands on the recorded value (a coarse-mtime
// filesystem, or an explicit touch back) must still reindex: UpdatePath's
// callers say the content may have changed, so a stat match alone is not proof.
func TestUpdatePath_SameStatDifferentBytesStillReindexes(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)
	abs := b2WriteFile(t, root, "n.md", "gammaword one\n")
	require.NoError(t, ix.UpdatePath(context.Background(), "n.md"))
	m, err := LoadManifest(ix.ManifestPath(), ix.Root())
	require.NoError(t, err)
	rec, ok := m.Get("n.md")
	require.True(t, ok)

	b2WriteFile(t, root, "n.md", "deltaword two\n") // same length as before
	mt := time.Unix(0, rec.ModTimeNanos)
	require.NoError(t, os.Chtimes(abs, mt, mt))
	entry, err := StatEntry(ix.Root(), "n.md")
	require.NoError(t, err)
	require.True(t, m.StatUnchanged(entry), "precondition: the stat must look unchanged")

	require.NoError(t, ix.UpdatePath(context.Background(), "n.md"))
	hits, err := ix.Search("deltaword", 10)
	require.NoError(t, err)
	require.True(t, containsPath(hits, "n.md"), "new bytes under an identical stat must still be indexed")
}
