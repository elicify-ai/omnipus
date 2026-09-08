// Tests for annotateKnowledgeBaseEntries (GET /library/{ws}/entries' vault
// flag): it must reach the SAME verdict knowledge.Detect reaches, without
// leaving the confined os.Root the listing handler already holds, and without
// reading a whole target directory per listed row.
//
// Expected values come from pkg/knowledge's own detection contract — either
// marker directory alone suffices (FR-020), and a marker must be a real
// DIRECTORY at the folder's root — never from what the annotator happens to do.

package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// openLibRootForTest opens the workspace's real library root, closing it with
// the test.
func openLibRootForTest(t *testing.T, api *restAPI, ws string) *library.Root {
	t.Helper()
	root, err := library.OpenRoot(api.homePath, ws)
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	return root
}

// dirRows builds one is_dir listing row per workspace-relative path.
func dirRows(paths ...string) []gen.LibraryEntry {
	rows := make([]gen.LibraryEntry, 0, len(paths))
	for _, p := range paths {
		rows = append(rows, gen.LibraryEntry{Path: p, Name: filepath.Base(p), IsDir: true})
	}
	return rows
}

// TestAnnotateKnowledgeBaseEntries_CannotEscapeTheConfinedRoot is the
// confinement contract. The annotator receives entries and a *library.Root; it
// must resolve every one of them THROUGH that root, so a row naming something
// that resolves outside the Library is refused by os.Root rather than read.
//
// The entry fed here carries is_dir=true over a path that is a symlink to an
// outside directory. That is a state the handler itself can hold: List reports
// a MOUNT's entry with is_dir forced true over a symlink
// (library.Root.annotateMount), and any ordinary directory row can become a
// symlink between the listing read and this annotation — a dir→symlink swap
// this function must not be able to follow. Reading the raw host path made the
// escape unconditional; routing through the root makes it impossible.
func TestAnnotateKnowledgeBaseEntries_CannotEscapeTheConfinedRoot(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	dir := workDir(api, ws)
	require.NoError(t, os.MkdirAll(dir, 0o700))

	// A knowledge base OUTSIDE the Library entirely.
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(outside, knowledge.ObsidianMarkerDirName), 0o700))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "escape")))

	// A real knowledge base INSIDE the Library: the control proving the fix
	// confines the read rather than disabling detection.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "real vault", knowledge.MarkerDirName), 0o700))

	root := openLibRootForTest(t, api, ws)
	entries := dirRows("escape", "real vault")
	annotateKnowledgeBaseEntries(root, entries)

	if entries[0].IsKnowledgeBase != nil && *entries[0].IsKnowledgeBase {
		t.Errorf("annotation followed a symlink out of the Library root and read %q — "+
			"the escape os.Root exists to prevent", outside)
	}
	require.NotNil(t, entries[1].IsKnowledgeBase,
		"a real knowledge base inside the root must still be detected")
	require.True(t, *entries[1].IsKnowledgeBase,
		"a folder with a %s/ marker is a knowledge base (FR-020)", knowledge.MarkerDirName)
}

// TestAnnotateKnowledgeBaseEntries_CostDoesNotScaleWithDirectorySize holds the
// function's own cost claim ("cheap, small list cost profile") to something
// observable and load-independent.
//
// Detection needs to know only whether two specific marker entries exist at a
// folder's root. Reading and sorting the WHOLE folder instead costs one
// allocation pair per dirent — per listed row, on an interactive endpoint, so
// a directory of 200 subfolders holding 10,000 notes each costs ~2,000,000
// dirents for one GET. Allocation counts are deterministic in the number of
// entries read, unlike wall-clock time, so they can state the property
// directly: annotating a folder with thousands of notes must cost no more than
// annotating one with a single note.
func TestAnnotateKnowledgeBaseEntries_CostDoesNotScaleWithDirectorySize(t *testing.T) {
	const bigVaultNotes = 4000

	api, ws := buildLibraryTestAPI(t)
	dir := workDir(api, ws)
	require.NoError(t, os.MkdirAll(dir, 0o700))

	for _, v := range []struct {
		name  string
		notes int
	}{{"small vault", 1}, {"big vault", bigVaultNotes}} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, v.name, knowledge.MarkerDirName), 0o700))
		for i := range v.notes {
			require.NoError(t, os.WriteFile(
				filepath.Join(dir, v.name, fmt.Sprintf("note-%05d.md", i)), []byte("x"), 0o600))
		}
	}

	root := openLibRootForTest(t, api, ws)
	small := dirRows("small vault")
	big := dirRows("big vault")

	// Both must actually detect, or the comparison below would be measuring
	// two different amounts of work for an unrelated reason.
	annotateKnowledgeBaseEntries(root, small)
	annotateKnowledgeBaseEntries(root, big)
	require.NotNil(t, small[0].IsKnowledgeBase)
	require.True(t, *small[0].IsKnowledgeBase)
	require.NotNil(t, big[0].IsKnowledgeBase)
	require.True(t, *big[0].IsKnowledgeBase)

	allocsSmall := testing.AllocsPerRun(2, func() { annotateKnowledgeBaseEntries(root, dirRows("small vault")) })
	allocsBig := testing.AllocsPerRun(2, func() { annotateKnowledgeBaseEntries(root, dirRows("big vault")) })

	// A full directory read costs at least one allocation per dirent; a
	// marker existence check costs the same handful either way. The bound is
	// deliberately loose — it only has to separate "constant" from "linear in
	// the folder's size".
	const slack = 200
	if allocsBig > allocsSmall+slack {
		t.Errorf("annotating a %d-note folder allocated %.0f vs %.0f for a 1-note folder "+
			"(+%.0f, budget +%d): the whole directory is still being read per listed row",
			bigVaultNotes, allocsBig, allocsSmall, allocsBig-allocsSmall, slack)
	}
}

// TestAnnotateKnowledgeBaseEntries_MarkerRulesAreUnchanged pins the detection
// answer itself, so making it cheaper and confined cannot quietly change WHAT
// counts as a knowledge base. Each case states the rule it comes from.
func TestAnnotateKnowledgeBaseEntries_MarkerRulesAreUnchanged(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	dir := workDir(api, ws)
	require.NoError(t, os.MkdirAll(dir, 0o700))

	// FR-020: either marker DIRECTORY alone suffices.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "omnipus vault", knowledge.MarkerDirName), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "obsidian vault", knowledge.ObsidianMarkerDirName), 0o700))
	// A plain folder with neither.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "plain", "sub"), 0o700))
	// A marker that is a FILE, not a directory: not a marker (FR-020).
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "file marker"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file marker", knowledge.ObsidianMarkerDirName),
		[]byte("{}"), 0o600))
	// A marker nested one level down: detection looks at the ROOT only.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested", "inner", knowledge.MarkerDirName), 0o700))

	root := openLibRootForTest(t, api, ws)
	entries := dirRows("omnipus vault", "obsidian vault", "plain", "file marker", "nested")
	annotateKnowledgeBaseEntries(root, entries)

	want := map[string]bool{
		"omnipus vault":  true,
		"obsidian vault": true,
		"plain":          false,
		"file marker":    false,
		"nested":         false,
	}
	for _, e := range entries {
		require.NotNil(t, e.IsKnowledgeBase, "%s: detection could complete, so the field must be present", e.Path)
		require.Equal(t, want[e.Path], *e.IsKnowledgeBase, "%s", e.Path)
	}

	// A FILE row is never annotated: the flag is a directory fact.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte("x"), 0o600))
	fileRow := []gen.LibraryEntry{{Path: "note.md", Name: "note.md", IsDir: false}}
	annotateKnowledgeBaseEntries(root, fileRow)
	require.Nil(t, fileRow[0].IsKnowledgeBase, "a file must never carry is_knowledge_base")
}

// TestAnnotateKnowledgeBaseEntries_DetectsThroughAMount proves the confined
// route is mount-aware. A mount's own row is the one library.Root.List
// deliberately reports as is_dir over a symlink, and the folder it points at
// is a legitimate, granted root — so a mounted vault must still be detected,
// through the mount's own os.Root rather than through the work tree.
func TestAnnotateKnowledgeBaseEntries_DetectsThroughAMount(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	require.NoError(t, os.MkdirAll(workDir(api, ws), 0o700))

	target := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(target, knowledge.ObsidianMarkerDirName), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(target, "inner", knowledge.MarkerDirName), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(target, "plain"), 0o700))
	_, _, err := workspace.CreateMount(api.homePath, ws, "vaults", target)
	require.NoError(t, err)

	root := openLibRootForTest(t, api, ws)
	entries := dirRows("vaults", "vaults/inner", "vaults/plain")
	annotateKnowledgeBaseEntries(root, entries)

	want := map[string]bool{"vaults": true, "vaults/inner": true, "vaults/plain": false}
	for _, e := range entries {
		require.NotNil(t, e.IsKnowledgeBase, "%s", e.Path)
		require.Equal(t, want[e.Path], *e.IsKnowledgeBase, "%s", e.Path)
	}
}

// TestAnnotateKnowledgeBaseEntries_SymlinkedMarkerIsTheKnownDivergence pins the
// one place this listing's answer differs from knowledge.Detect's, so it stays
// a documented, bounded residual rather than drifting further unnoticed.
//
// Detect never follows a symlink (FR-044): a marker must be a real directory,
// or a folder could claim knowledge-base status by pointing at another
// folder's config. library.Root.StatDir resolves through os.Root.Stat, which
// follows a symlink that stays inside the root — so an in-root relative
// symlink named .obsidian/ is counted here and not by Detect.
//
// The divergence over-detects and never under-detects, and an ESCAPING
// symlink is refused by both. Closing it needs a confined lstat that
// library.Root does not currently expose — see detectKnowledgeBaseInRoot.
func TestAnnotateKnowledgeBaseEntries_SymlinkedMarkerIsTheKnownDivergence(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	dir := workDir(api, ws)
	require.NoError(t, os.MkdirAll(dir, 0o700))

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "claimant"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "someone elses config"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join("..", "someone elses config"),
		filepath.Join(dir, "claimant", knowledge.ObsidianMarkerDirName)))

	// knowledge.Detect — the rule of record — says NO.
	detected, err := knowledge.IsKnowledgeBase(filepath.Join(dir, "claimant"))
	require.NoError(t, err)
	require.False(t, detected, "fixture drift: a symlinked marker is not a marker (FR-044)")

	root := openLibRootForTest(t, api, ws)
	entries := dirRows("claimant")
	annotateKnowledgeBaseEntries(root, entries)

	require.NotNil(t, entries[0].IsKnowledgeBase)
	require.True(t, *entries[0].IsKnowledgeBase,
		"KNOWN RESIDUAL: the confined stat follows an in-root symlinked marker where "+
			"knowledge.Detect does not. If this now reports false, a confined lstat has "+
			"landed and this test should be inverted to assert parity instead.")
}
