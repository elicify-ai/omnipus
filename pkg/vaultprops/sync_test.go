// Omnipus — tests for the properties-index build pipeline (sync.go).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// --- fixtures ----------------------------------------------------------------

// syncVault builds a knowledge base at a REAL path (macOS temp dirs are
// symlinks, and Sync/knowledge.Scan key everything off the resolved root).
func syncVault(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, knowledge.MarkerDirName), 0o700); err != nil {
		t.Fatalf("marker dir: %v", err)
	}
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return resolved
}

func syncHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return resolved
}

const plantSchema = "schema_version: 1\n" +
	"type: plant\n" +
	"properties:\n" +
	"  condition: { type: text }\n" +
	"  height_cm: { type: decimal }\n"

const fernNote = "---\n" +
	"type: plant\n" +
	"id: PL-0001\n" +
	"condition: growing\n" +
	"height_cm: 12.5\n" +
	"---\n" +
	"# Fern\n" +
	"- [ ] water it\n"

// findViaTool builds knowledgefind.Deps exactly the way FindTool.buildDeps does
// (same package, calling the unexported method directly, so this test drives
// production code rather than a re-implementation) and runs a real
// knowledge_find call against them.
func findViaTool(t *testing.T, home, root string, args map[string]any) string {
	t.Helper()
	ctx := context.Background()
	tool := NewFindTool(home)
	col := knowledge.ScopedCollection{Name: "vault", Root: root, Origin: knowledge.WorkTreeOrigin}
	deps, closeDeps, err := tool.buildDeps(ctx, col)
	defer closeDeps()
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	text, callErr := knowledgefind.Call(ctx, deps, raw)
	if callErr != nil {
		return text // refusal text IS the thing under test in the "before" case
	}
	return text
}

func skipWithoutSQLite(t *testing.T) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; Sync is a documented no-op there")
	}
}

// --- the behavioural proof -----------------------------------------------

// TestSync_FillsThePropertiesIndexSoKnowledgeFindStopsRefusing is the exit
// proof: BEFORE Sync ever runs, a typed knowledge_find query over a freshly
// mounted vault refuses because the properties index was never built — the
// exact gap find_tool.go's own header used to document as a "PRODUCTION GAP".
// AFTER Sync runs once, the SAME call returns the record.
func TestSync_FillsThePropertiesIndexSoKnowledgeFindStopsRefusing(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
		"Notes/loose.md":                    "# Just a note\nNo frontmatter here.\n",
	})

	// BEFORE: nothing has ever indexed this collection's properties.
	before := findViaTool(t, home, root, map[string]any{"type": "plant"})
	if !strings.Contains(before, "properties index is not open") {
		t.Fatalf("expected the documented refusal before Sync ever ran, got:\n%s", before)
	}

	stats, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Indexed == 0 {
		t.Fatalf("Sync reported zero notes indexed: %+v", stats)
	}

	// AFTER: the same call, same arguments, now returns the record.
	after := findViaTool(t, home, root, map[string]any{"type": "plant"})
	if strings.Contains(after, "properties index is not open") {
		t.Fatalf("knowledge_find still refuses after Sync:\n%s", after)
	}
	if !strings.Contains(after, "PL-0001") {
		t.Fatalf("the indexed record's id did not come back from knowledge_find:\n%s", after)
	}
	if !strings.Contains(after, "Fern") {
		t.Fatalf("the indexed record's path did not come back from knowledge_find:\n%s", after)
	}

	t.Logf("BEFORE:\n%s\n\nAFTER:\n%s", before, after)
}

// --- initial build ------------------------------------------------------

func TestSync_OrdinaryNoteGetsABareRowAndNoProperties(t *testing.T) {
	skipWithoutSQLite(t)
	home := syncHome(t)
	root := syncVault(t, map[string]string{
		"Notes/loose.md": "# Just a note\nNo frontmatter here.\n",
	})
	if _, err := Sync(context.Background(), home, root, SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	var got []propindex.Candidate
	if err := store.Candidates(context.Background(), propindex.Selector{}, func(c propindex.Candidate) (propindex.Verdict, error) {
		got = append(got, c)
		return propindex.Rejected, nil
	}); err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one indexed note, got %d: %+v", len(got), got)
	}
	if got[0].RecordType != "" || len(got[0].PropOrder) != 0 {
		t.Errorf("an ordinary note must declare no record type and no properties, got %+v", got[0])
	}
}

func TestSync_AttachmentIsIndexedByNameNeverOpened(t *testing.T) {
	skipWithoutSQLite(t)
	home := syncHome(t)
	root := syncVault(t, map[string]string{
		"Photos/fern.png": "not really a png, and Sync must never read it as one",
	})
	stats, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Indexed != 1 {
		t.Fatalf("expected the attachment to be indexed, got stats %+v", stats)
	}

	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()
	var hash string
	found := false
	if err := store.AllPaths(context.Background(), func(n propindex.IndexedNote) error {
		if n.Path == "Photos/fern.png" {
			found = true
			hash = n.SourceHash
		}
		return nil
	}); err != nil {
		t.Fatalf("AllPaths: %v", err)
	}
	if !found {
		t.Fatal("the attachment was not indexed at all")
	}
	if hash != "" {
		t.Errorf("an attachment must never be hashed (that means reading it) — got hash %q", hash)
	}
}

// --- incremental behaviour ------------------------------------------------

func TestSync_UnchangedNoteIsSkippedOnTheSecondRun(t *testing.T) {
	skipWithoutSQLite(t)
	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
	})
	first, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if first.Indexed == 0 {
		t.Fatalf("first Sync indexed nothing: %+v", first)
	}

	second, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if second.Indexed != 0 {
		t.Errorf("second Sync re-wrote %d unchanged note(s): %+v", second.Indexed, second)
	}
	if second.Unchanged == 0 {
		t.Errorf("second Sync did not report the unchanged note: %+v", second)
	}
}

func TestSync_EditedNoteIsReindexedWithItsNewValue(t *testing.T) {
	skipWithoutSQLite(t)
	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
	})
	if _, err := Sync(context.Background(), home, root, SyncOptions{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	edited := strings.Replace(fernNote, "condition: growing", "condition: wilting", 1)
	if err := os.WriteFile(filepath.Join(root, "Plants", "Fern.md"), []byte(edited), 0o600); err != nil {
		t.Fatalf("rewrite note: %v", err)
	}

	stats, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if stats.Indexed != 1 {
		t.Fatalf("the edited note was not re-indexed: %+v", stats)
	}

	got := findRecordCondition(t, home, root, "PL-0001")
	if got != "wilting" {
		t.Errorf("the index still holds the old value: got %q, want %q", got, "wilting")
	}
}

// TestSync_RenameRemovesTheOldPathAndIndexesTheNew covers created / edited /
// renamed / moved / trashed in one behaviour: the diff is keyed on PATH, and a
// rename is indistinguishable from "the old path is gone and a new one
// appeared" — which is exactly how Scan reports it.
func TestSync_RenameRemovesTheOldPathAndIndexesTheNew(t *testing.T) {
	skipWithoutSQLite(t)
	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
	})
	if _, err := Sync(context.Background(), home, root, SyncOptions{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	oldPath := filepath.Join(root, "Plants", "Fern.md")
	newPath := filepath.Join(root, "Plants", "Fern-Renamed.md")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	stats, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if stats.Removed != 1 {
		t.Errorf("the old path was not removed: %+v", stats)
	}
	if stats.Indexed != 1 {
		t.Errorf("the new path was not indexed: %+v", stats)
	}

	assertPathsIndexed(t, home, root, map[string]bool{
		"Plants/Fern.md":         false,
		"Plants/Fern-Renamed.md": true,
	})
}

// TestSync_TrashedNoteIsRemoved — .trash is a scanSkippedDirName, so a note
// moved there simply stops appearing in the walk, and the diff pass removes it
// exactly as it would a deletion.
func TestSync_TrashedNoteIsRemoved(t *testing.T) {
	skipWithoutSQLite(t)
	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
	})
	if _, err := Sync(context.Background(), home, root, SyncOptions{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	trashDir := filepath.Join(root, ".trash")
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		t.Fatalf("mkdir .trash: %v", err)
	}
	if err := os.Rename(filepath.Join(root, "Plants", "Fern.md"), filepath.Join(trashDir, "Fern.md")); err != nil {
		t.Fatalf("move to trash: %v", err)
	}

	stats, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if stats.Removed != 1 {
		t.Fatalf("the trashed note was not removed from the index: %+v", stats)
	}
	assertPathsIndexed(t, home, root, map[string]bool{"Plants/Fern.md": false})
}

// TestSync_UnreadableNoteIsRemovedNotLeftStale — failure modes 2 and 3 from
// the task brief, exercised together: a note that WAS indexed and then
// becomes unreadable (permission revoked, mid-vault) must not silently keep
// answering queries with its last-known content. Sync must report it AND
// remove its rows — the same rule knowledge.Index.SyncWith already applies to
// the text index, matched here rather than reinvented.
func TestSync_UnreadableNoteIsRemovedNotLeftStale(t *testing.T) {
	skipWithoutSQLite(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on windows")
	}
	if os.Geteuid() == 0 {
		// root ignores the 0o000 mode and reads the note anyway, so the
		// unreadable-note precondition this test rests on cannot hold (seen on
		// the root-user CI worker). It still runs for every non-root context —
		// dev machines and GitHub CI.
		t.Skip("runs as root cannot make a file unreadable via chmod 0o000")
	}
	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
	})
	first, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if first.Indexed != 1 {
		t.Fatalf("the note was not indexed on the first pass: %+v", first)
	}
	assertPathsIndexed(t, home, root, map[string]bool{"Plants/Fern.md": true})

	notePath := filepath.Join(root, "Plants", "Fern.md")
	if chmodErr := os.Chmod(notePath, 0o000); chmodErr != nil {
		t.Fatalf("chmod: %v", chmodErr)
	}
	t.Cleanup(func() { _ = os.Chmod(notePath, 0o600) }) // let TempDir cleanup remove it

	stats, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if stats.Removed != 1 {
		t.Errorf("the now-unreadable note's stale rows were not removed: %+v", stats)
	}
	found := false
	for _, p := range stats.Problems {
		if p.RelPath == "Plants/Fern.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("the read failure was not reported: %+v", stats.Problems)
	}
	assertPathsIndexed(t, home, root, map[string]bool{"Plants/Fern.md": false})
}

// --- non-conforming values keep their evidence -----------------------------

func TestSync_NonConformingPropertyKeepsItsProblemEvidence(t *testing.T) {
	skipWithoutSQLite(t)
	home := syncHome(t)
	badNote := "---\n" +
		"type: plant\n" +
		"id: PL-0002\n" +
		"height_cm: fifty\n" + // not a decimal
		"---\n" +
		"# Bad Plant\n"
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Bad.md":                     badNote,
	})
	if _, err := Sync(context.Background(), home, root, SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	var cand *propindex.Candidate
	if err := store.Candidates(context.Background(), propindex.Selector{RecordType: "plant"}, func(c propindex.Candidate) (propindex.Verdict, error) {
		if c.RecordID == "PL-0002" {
			cc := c
			cand = &cc
		}
		return propindex.Rejected, nil
	}); err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if cand == nil {
		t.Fatal("the non-conforming note was not indexed at all — its evidence is now unreachable")
	}
	prop, ok := cand.Prop("height_cm")
	if !ok {
		t.Fatal("the non-conforming property has no row at all")
	}
	if prop.Got == "" || prop.Expected == "" {
		t.Errorf("the non-conforming property's evidence was dropped: got=%q expected=%q", prop.Got, prop.Expected)
	}
	if !strings.Contains(prop.Got, "fifty") {
		t.Errorf("Got does not name what the file actually held: %q", prop.Got)
	}
}

// --- unreadable frontmatter is reported, not silently swallowed ------------

func TestSync_UnparseableFrontmatterIsReportedButTheNoteStillGetsARow(t *testing.T) {
	skipWithoutSQLite(t)
	home := syncHome(t)
	broken := "---\n" +
		"type: [this is not valid: yaml: at all\n" +
		"---\n" +
		"# Broken\n"
	root := syncVault(t, map[string]string{
		"Notes/Broken.md": broken,
	})
	stats, err := Sync(context.Background(), home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	found := false
	for _, p := range stats.Problems {
		if p.RelPath == "Notes/Broken.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("the unparseable frontmatter was not reported as a problem: %+v", stats.Problems)
	}

	assertPathsIndexed(t, home, root, map[string]bool{"Notes/Broken.md": true})
}

// --- permissions -----------------------------------------------------------

func TestSync_WritesTheIndexFileAt0600(t *testing.T) {
	skipWithoutSQLite(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on windows")
	}
	home := syncHome(t)
	root := syncVault(t, map[string]string{"Notes/loose.md": "# hi\n"})
	if _, err := Sync(context.Background(), home, root, SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	path, err := knowledge.PropertiesIndexPath(home, root)
	if err != nil {
		t.Fatalf("PropertiesIndexPath: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat index file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("index file mode = %v, want 0600", fi.Mode().Perm())
	}
}

// --- helpers ---------------------------------------------------------------

func openStoreForTest(t *testing.T, home, root string) propindex.Store {
	t.Helper()
	path, err := knowledge.PropertiesIndexPath(home, root)
	if err != nil {
		t.Fatalf("PropertiesIndexPath: %v", err)
	}
	store, err := propindex.Open(context.Background(), path, propindex.Options{})
	if err != nil {
		t.Fatalf("propindex.Open: %v", err)
	}
	return store
}

func assertPathsIndexed(t *testing.T, home, root string, want map[string]bool) {
	t.Helper()
	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	present := map[string]bool{}
	if err := store.AllPaths(context.Background(), func(n propindex.IndexedNote) error {
		present[n.Path] = true
		return nil
	}); err != nil {
		t.Fatalf("AllPaths: %v", err)
	}
	var mismatches []string
	for path, wantPresent := range want {
		if present[path] != wantPresent {
			mismatches = append(mismatches, path)
		}
	}
	sort.Strings(mismatches)
	if len(mismatches) > 0 {
		t.Errorf("path presence mismatch for %v; index holds %v", mismatches, present)
	}
}

func findRecordCondition(t *testing.T, home, root, recordID string) string {
	t.Helper()
	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	var got string
	if err := store.Candidates(context.Background(), propindex.Selector{RecordType: "plant"}, func(c propindex.Candidate) (propindex.Verdict, error) {
		if c.RecordID == recordID {
			if p, ok := c.Prop("condition"); ok && len(p.Elems) > 0 {
				got = p.Elems[0].Text
			}
		}
		return propindex.Rejected, nil
	}); err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	return got
}
