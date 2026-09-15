// Omnipus — regression coverage for R2 of docs/internal/design/
// knowledge-tools-remediation.md: the knowledge indexer's boot-time
// enumeration must answer "which collections exist" from
// knowledge.ResolveScope, the SAME answer every knowledge tool
// (describe/read/find) already consults, rather than from the mount store
// alone.
//
// Before this file's changes to knowledge_lifecycle.go, a knowledge base
// copied directly into a workspace's own work tree — the only creation path
// ADR-067 D11 leaves available, since a work tree can never become a mount
// (pkg/workspace/mount.go's ErrMountRefused/CheckMountTarget, FR-7.5) — was
// discoverable by knowledge_describe and knowledge_read (both resolve
// through knowledge.ResolveScope) but permanently unindexed: nothing ever
// called AttachMount for it, because AttachAllMounts read only the mount
// store. That is the specific defect these tests are the regression guard
// for (uat-findings-knowledge-tools-2026-09-01-run2.md F-3).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// countPhase returns how many recorded frames are in the given phase.
func countPhase(frames []gen.KnowledgeIndexProgressFrame, phase string) int {
	n := 0
	for _, f := range frames {
		if f.Phase == phase {
			n++
		}
	}
	return n
}

// klsWorkTreeCollection creates a knowledge base directly inside workspaceID's
// own work tree (workspace.SafeWorkDir), at work/<name>/, WITHOUT going
// through workspace.CreateMount — this is what "a collection copied into a
// workspace work tree" means on disk, and it is the one route ADR-067 D11
// leaves available for creating a knowledge base at all. Returns the
// collection's real (symlink-resolved) root.
func klsWorkTreeCollection(t *testing.T, home, workspaceID, name string, files map[string]string) string {
	t.Helper()
	workDir, err := workspace.SafeWorkDir(home, workspaceID)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	root := filepath.Join(workDir, name)
	require.NoError(t, os.MkdirAll(filepath.Join(root, knowledge.MarkerDirName), 0o700))
	for relPath, body := range files {
		full := filepath.Join(root, filepath.FromSlash(relPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return realRoot
}

// --- a work-tree collection actually gets indexed ---------------------------

func TestKnowledgeLifecycle_WorkTreeCollectionIsIndexedAtBoot(t *testing.T) {
	home := kltHome(t)
	const wsID = "ws-worktree"
	kwWorkspace(t, home, wsID)

	root := klsWorkTreeCollection(t, home, wsID, "field-notes", map[string]string{
		"a.md": "# Field notes\nxyzzyplugh appears nowhere else in this fixture",
	})

	// PRECONDITION, per the remediation design's own test requirement: this
	// collection must NOT be a registered mount, or a pass here would prove
	// nothing about the work-tree path this test exists to cover — it could
	// just as easily be passing via the ordinary mount path.
	mounts, ok := workspace.LoadMounts(home, wsID)
	if !ok {
		t.Fatalf("workspace.LoadMounts(%q) reported the mount store unusable; "+
			"this test cannot establish its precondition", wsID)
	}
	if len(mounts) != 0 {
		t.Fatalf("fixture bug: workspace %q already has %d mount(s) recorded — "+
			"this test proves nothing about a work-tree collection unless it starts "+
			"with zero mounts", wsID, len(mounts))
	}
	for _, r := range workspace.AllowedMountRoots(home, wsID) {
		if r == root {
			t.Fatalf("fixture bug: %q is already an allowed mount root — "+
				"this test's collection must be reachable ONLY via the work tree", root)
		}
	}

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home})
	kl.AttachAllMounts()
	kl.WaitForAttaches()

	assert.Contains(t, kl.AttachedRoots(), root,
		"a knowledge base living in the workspace's own work tree must be attached "+
			"at boot, exactly like a mounted one (R2)")

	ix, ok := kl.IndexForRoot(root)
	require.True(t, ok, "work-tree collection must have an open index")
	hits, err := ix.Search("xyzzyplugh", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1, "the note's content must actually be searchable, not merely attached")
	assert.Equal(t, "a.md", hits[0].Path)
}

// --- a mounted collection still indexes at boot: no regression -------------

func TestKnowledgeLifecycle_MountedCollectionStillIndexesAtBoot(t *testing.T) {
	root := kltVault(t, map[string]string{
		"a.md": "# A\nquibbleflarn is the only place this word occurs",
	})
	home := kltHome(t)
	const wsID = "ws-mount-regress"
	kwWorkspace(t, home, wsID)

	_, _, err := workspace.CreateMount(home, wsID, "vault", root)
	require.NoError(t, err)

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home})
	kl.AttachAllMounts()
	kl.WaitForAttaches()

	assert.Contains(t, kl.AttachedRoots(), root,
		"switching the enumeration source to knowledge.ResolveScope must not stop "+
			"an ordinary mounted collection from being reopened at boot (FR-039)")

	ix, ok := kl.IndexForRoot(root)
	require.True(t, ok)
	hits, err := ix.Search("quibbleflarn", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1, "a mounted collection's content must still be searchable after this change")
}

// --- the same root, reachable twice through the new boot sweep, is one index, not two

func TestKnowledgeLifecycle_RepeatedBootSweepDoesNotReattachOrReindex(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)
	const wsID = "ws-repeat-sweep"
	kwWorkspace(t, home, wsID)

	_, _, err := workspace.CreateMount(home, wsID, "vault", root)
	require.NoError(t, err)

	rec := &kltFrames{}
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home, Emit: rec.emit})

	kl.AttachAllMounts()
	kl.WaitForAttaches()
	require.Equal(t, 1, kl.HoldersFor(root))
	firstIdleFrames := countPhase(rec.all(), "idle")
	require.Equal(t, 1, firstIdleFrames, "the first sweep must reconcile exactly once")

	// A second full sweep — e.g. a caller re-running boot's enumeration —
	// must not open a second holder or trigger a second reconcile. FR-031:
	// one folder, one index, one indexing run.
	kl.AttachAllMounts()
	kl.WaitForAttaches()

	assert.Equal(t, []string{root}, kl.AttachedRoots(), "still exactly one collection")
	assert.Equal(t, 1, kl.HoldersFor(root), "the repeat sweep must not add a second holder")
	assert.Equal(t, firstIdleFrames, countPhase(rec.all(), "idle"),
		"the repeat sweep must not re-index a collection that is already attached")
}

// --- routing must not trust ScopedCollection.Origin's string value ---------

// TestKnowledgeLifecycle_MountNamedWorkspaceIsNotMisroutedAsWorkTree is the
// regression for the ambiguity R2/R3(c) of the remediation design name
// explicitly: knowledge.WorkTreeOrigin is the literal string "workspace",
// and nothing in workspace.ValidateMountName forbids an operator naming a
// REAL mount "workspace" too. A routing rule that compared
// ScopedCollection.Origin against knowledge.WorkTreeOrigin by string equality
// would misfile this mount onto the root-keyed work-tree attach path — which
// has no mount name at all — orphaning it from RevokeMount's mount-name
// lookup. The mount would then never be released: an operator deleting it
// would see 200 OK and a mount that no longer exists, while its index
// silently stayed open forever.
//
// The oracle is RevokeMount itself: it must find and close this mount's
// index by name, proving the boot-time sweep attached it through the SAME
// mount-name-keyed path AttachMount has always used — not the new
// root-keyed one.
func TestKnowledgeLifecycle_MountNamedWorkspaceIsNotMisroutedAsWorkTree(t *testing.T) {
	require.Equal(t, "workspace", knowledge.WorkTreeOrigin,
		"fixture assumption: this test only reproduces the ambiguity if the "+
			"work-tree sentinel and the mount name below are the same string")

	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)
	const wsID = "ws-ambiguous-name"
	kwWorkspace(t, home, wsID)

	// A real mount, legally named "workspace" — ValidateMountName forbids a
	// path separator, "." and "..", nothing else.
	_, _, err := workspace.CreateMount(home, wsID, "workspace", root)
	require.NoError(t, err)

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home})
	kl.AttachAllMounts()
	kl.WaitForAttaches()

	require.Contains(t, kl.AttachedRoots(), root, "the mount must still be attached")

	require.NoError(t, kl.RevokeMount(wsID, "workspace"))
	assert.NotContains(t, kl.AttachedRoots(), root,
		"RevokeMount must be able to find and close this mount by name — if the "+
			"boot sweep had routed it onto the root-keyed work-tree path instead, "+
			"this revoke would silently no-op and leak the index open")
}

// --- O1: a document alongside notes is indexed by title, not by content ----

// TestKnowledgeLifecycle_AttachmentIsFindableByNameNotByBody covers operator
// ruling O1 (knowledge-tools-remediation.md §7): a vault folder holds files
// other than notes (PDFs and the like), those must be findable, and their
// content must NOT be extracted or indexed. Nothing in this change adds a
// new mechanism for that — pkg/knowledge's existing FR-039a "attachment"
// path already does both halves unconditionally for any collection this
// file attaches. This test is the proof, not the implementation.
func TestKnowledgeLifecycle_AttachmentIsFindableByNameNotByBody(t *testing.T) {
	const secretBody = "PORTCULLIS_NEVER_SHOULD_BE_INDEXED_AS_BODY_TEXT"
	root := kltVault(t, map[string]string{
		// Deliberately does NOT mention the document's filename/title anywhere
		// in its own body — the fixture must isolate "found via the PDF's own
		// name" from "found via an unrelated note that happens to say it".
		"notes/intro.md": "# Intro\nan unrelated note about wombat migration",
		// Not a real PDF — this package never opens the file at all (FR-039a),
		// so the bytes' actual format is irrelevant to what gets indexed.
		"quarterly-report.pdf": secretBody,
	})
	home := kltHome(t)
	const wsID = "ws-attachment"
	kwWorkspace(t, home, wsID)
	_, _, err := workspace.CreateMount(home, wsID, "vault", root)
	require.NoError(t, err)

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home})
	kl.AttachAllMounts()
	kl.WaitForAttaches()

	ix, ok := kl.IndexForRoot(root)
	require.True(t, ok)

	// Findable: a search on the filename stem (its title, per O1's "filename
	// stem is the title floor" decision) returns the document.
	hits, err := ix.Search("quarterly-report", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1, "a document must be findable by its filename/title")
	assert.Equal(t, "quarterly-report.pdf", hits[0].Path)

	// NOT full-text extracted: the document's actual byte content must never
	// reach the index. This is O1's negative assertion.
	hits, err = ix.Search(secretBody, 10)
	require.NoError(t, err)
	assert.Empty(t, hits, "a document's content must NEVER be read or indexed (O1, FR-039a)")
}
