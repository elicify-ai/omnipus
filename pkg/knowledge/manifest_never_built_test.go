// Omnipus — regression coverage for F-3/F-6 (UAT
// docs/internal/uat/uat-findings-knowledge-tools-2026-09-01-run2.md) and
// ruling R2 / plan item 4 in
// docs/internal/design/knowledge-tools-remediation.md.
//
// THE DEFECT THIS FILE GUARDS: LoadManifest returns (NewManifest(root), nil)
// — a zero manifest AND a nil error — when the manifest file simply does not
// exist. Before the fix, knowledge_describe's index section read that nil
// error as "the manifest was loaded and it has zero entries", so a
// NEVER-INDEXED collection rendered as either "indexed and empty" or a false
// DRIFT state ("index holds 0 notes, N on disk — re-index to reconcile") that
// names a remedy no agent tool, CLI verb or REST endpoint can perform.
//
// This file proves the three states a collection's index can be in are
// rendered distinctly, so collapsing any two of them together is a test
// failure:
//
//   - NEVER BUILT   — no manifest file exists at all.
//   - BUILT, FRESH   — a manifest exists and matches what is on disk.
//   - BUILT, DRIFTED — a manifest exists but disagrees with what is on disk.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestManifestExists_|^TestDescribe_IndexState' ./pkg/knowledge/
package knowledge

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ManifestExists itself — the mechanism the describe fix is built on.
// ---------------------------------------------------------------------------

// TestManifestExists_DistinguishesAbsentFromEmpty pins ManifestExists'
// contract directly, independent of the describe tool that consumes it: an
// absent file must report (false, nil) — never an error, matching
// LoadManifest's own "first run, not a failure" contract — and a present,
// loadable manifest must report (true, nil), even when that manifest has zero
// entries. If ManifestExists cannot tell those two apart, nothing built on it
// can either.
func TestManifestExists_DistinguishesAbsentFromEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFileName)

	// Precondition: the file genuinely does not exist yet. Asserted with the
	// same fs.ErrNotExist check LoadManifest itself uses, so this test is not
	// merely trusting the function under test to report its own precondition.
	if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("test precondition failed: %s already exists (stat err = %v) before ManifestExists was ever called", path, statErr)
	}

	exists, err := ManifestExists(path)
	require.NoError(t, err)
	if exists {
		t.Fatalf("ManifestExists(%s) = true for a file that was just proven absent", path)
	}

	// Now build a real, empty-but-valid manifest and save it — the "built and
	// empty" state ManifestExists must NOT confuse with "never built".
	m := NewManifest("/collections/example")
	require.NoError(t, m.Save(path))
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("test precondition failed: manifest Save did not produce a file at %s: %v", path, statErr)
	}

	exists, err = ManifestExists(path)
	require.NoError(t, err)
	if !exists {
		t.Fatalf("ManifestExists(%s) = false for a manifest file just proven present on disk", path)
	}
}

// TestManifestExists_ReportsAnUnreadableDirectoryAsUnknownNotAbsent asserts
// the third case: a stat failure that is NOT "not exist" (here, a directory
// with its execute bit removed so the kernel refuses to even look inside it)
// must come back as an error, not as false. Silently mapping "I could not
// look" onto "I looked and found nothing" is the exact defect this whole file
// exists to catch, one layer further down.
func TestManifestExists_ReportsAnUnreadableDirectoryAsUnknownNotAbsent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission bits; this case cannot be reproduced running as root")
	}
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	require.NoError(t, os.Mkdir(blocked, 0o755))
	path := filepath.Join(blocked, ManifestFileName)

	require.NoError(t, os.Chmod(blocked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) }) // let t.TempDir() clean up

	exists, err := ManifestExists(path)
	if err == nil {
		t.Fatalf("ManifestExists(%s) = (%v, nil), want a non-nil error — a permission failure is not evidence the manifest is absent", path, exists)
	}
	if exists {
		t.Fatalf("ManifestExists(%s) = (true, %v) — cannot be both", path, err)
	}
}

// ---------------------------------------------------------------------------
// The full tool surface — knowledge_describe over a real collection.
// ---------------------------------------------------------------------------

// mnbDescribe runs knowledge_describe with the index section and the
// integrity sweep on (the sweep is what populates NotesOnDisk/NotesCounted,
// which the drift branch of indexFreshness needs) and returns the rendered
// text.
func mnbDescribe(t *testing.T, home, agentID, wsID string) string {
	t.Helper()
	tool := NewDescribeTool(ToolDeps{Home: home}, nil)
	ctx := b5Ctx(agentID, wsID)
	res := tool.Execute(ctx, map[string]any{
		"include":         []any{DescribeSectionIndex},
		"check_integrity": true,
	})
	require.NotNil(t, res)
	require.False(t, res.IsError, "knowledge_describe returned an error: %s", res.ForLLM)
	return res.ForLLM
}

// assertManifestAbsent is the precondition every "never built" case in this
// file must establish before trusting the rendered message: if the manifest
// happens to already exist (e.g. a fixture helper elsewhere in this package
// grew a side effect that indexes on mount), this test would otherwise pass
// for a reason that has nothing to do with the fix it is meant to guard.
func assertManifestAbsent(t *testing.T, home, collectionRoot string) {
	t.Helper()
	dir, err := IndexDirFor(home, collectionRoot)
	require.NoError(t, err)
	manifestPath := filepath.Join(dir, ManifestFileName)
	exists, existsErr := ManifestExists(manifestPath)
	require.NoError(t, existsErr)
	if exists {
		t.Fatalf("test precondition failed: a manifest already exists at %s; this collection was not supposed to be indexed yet", manifestPath)
	}
}

// TestDescribe_IndexState_NeverBuiltSaysSoAndNamesNoRemedy is F-3/F-6's exact
// reproduction: a collection with real notes on disk and a valid
// .omnipus-vault/ marker, that has never been through OpenIndex/Sync, so no
// manifest file exists anywhere under $OMNIPUS_HOME for it.
func TestDescribe_IndexState_NeverBuiltSaysSoAndNamesNoRemedy(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "one.md", "# One\n\nsomething\n")
	b5Note(t, vault, "two.md", "# Two\n\nsomething else\n")
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)
	// Deliberately NOT indexed — no OpenIndex, no Sync, ever, for this root.

	assertManifestAbsent(t, home, vault)

	text := mnbDescribe(t, home, "mia", ws)

	if !strings.Contains(text, "NOT INDEXED yet") {
		t.Errorf("a never-built collection must render NOT INDEXED yet; got:\n%s", text)
	}
	if strings.Contains(text, "disagree") {
		t.Errorf("a never-built collection must not be reported as DRIFT (index vs. disk disagreeing); got:\n%s", text)
	}
	if strings.Contains(text, "indexed and empty") {
		t.Errorf("a never-built collection must not be reported as an empty INDEXED collection; got:\n%s", text)
	}
	if strings.Contains(text, "re-index") {
		t.Errorf("the message must not name 're-index' or any other remedy action that does not exist "+
			"(design doc R2/plan item 6 is not implemented); got:\n%s", text)
	}
}

// TestDescribe_IndexState_BuiltAndFreshIsNotConfusedWithNeverBuilt is the
// positive control for the case above: without it, a describe path that
// ALWAYS says "NOT INDEXED yet" would also pass the never-built test.
func TestDescribe_IndexState_BuiltAndFreshIsNotConfusedWithNeverBuilt(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "one.md", "# One\n\nsomething\n")
	b5Note(t, vault, "two.md", "# Two\n\nsomething else\n")
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)
	b5Index(t, home, vault) // OpenIndex + Sync — a real, completed build.

	// Precondition: the manifest now exists AND its recorded count matches
	// what is actually on disk (2 notes), so this genuinely exercises "built
	// and fresh" rather than "built and drifted".
	dir, derr := IndexDirFor(home, vault)
	require.NoError(t, derr)
	manifestPath := filepath.Join(dir, ManifestFileName)
	exists, existsErr := ManifestExists(manifestPath)
	require.NoError(t, existsErr)
	if !exists {
		t.Fatalf("test precondition failed: manifest still absent at %s after OpenIndex+Sync", manifestPath)
	}
	m, loadErr := LoadManifest(manifestPath, vault)
	require.NoError(t, loadErr)
	if m.Len() != 2 {
		t.Fatalf("test precondition failed: manifest holds %d entries, want 2 (one.md, two.md) before asserting freshness", m.Len())
	}

	text := mnbDescribe(t, home, "mia", ws)

	if strings.Contains(text, "NOT INDEXED") {
		t.Errorf("a freshly built, matching index must not render NOT INDEXED; got:\n%s", text)
	}
	if strings.Contains(text, "disagree") {
		t.Errorf("a freshly built, matching index must not render DRIFT; got:\n%s", text)
	}
	if !strings.Contains(text, "index holds 2 notes") {
		t.Errorf("expected the plain 'index holds 2 notes' phrasing; got:\n%s", text)
	}
}

// TestDescribe_IndexState_DriftedIsNotConfusedWithNeverBuilt is the third
// state: a manifest exists (so ManifestKnown must be true, unlike the
// never-built case) but a note was added to disk after the last sync, so the
// manifest's count and the disk count genuinely disagree. Before the fix,
// this state and the never-built state were reached through the SAME code
// path (merr == nil); collapsing them the other way — reporting drift as
// "NOT INDEXED yet" — would be the mirror-image regression the task
// description warns about.
//
// WORDING UPDATE (F-9, reopened a second time — see
// f9_describe_symptom_test.go): this branch's message used to say "the two
// disagree; re-index to reconcile". That named a remedy no agent tool, CLI
// verb or REST endpoint performs, for THIS case exactly as much as for the
// never-fully-synced-at-all case knowledge_edit's instant-indexing path can
// now also produce (one create on an unswept collection leaves a one-entry
// manifest that reaches this exact branch). The manifest records no
// provenance for how each entry got there — nothing distinguishes "built
// once, then drifted" (this test's own setup) from "never built, only
// touched" at the data level — so indexFreshness no longer tries to and
// instead states the one thing it actually knows, honestly, for both: how
// much of what's on disk the index currently covers. This test now asserts
// THAT phrasing, and that no remedy is named, rather than the retired
// "disagree" wording — the three-state distinction it exists to prove
// (never built vs. built-and-fresh vs. built-and-incomplete) still holds:
// this collection's message is neither "NOT INDEXED yet" nor the plain
// "index holds N notes" the fresh case renders.
func TestDescribe_IndexState_DriftedIsNotConfusedWithNeverBuilt(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "one.md", "# One\n\nsomething\n")
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)
	b5Index(t, home, vault) // manifest now records exactly 1 note.

	// Drift it: add a second note WITHOUT re-syncing.
	b5Note(t, vault, "two.md", "# Two\n\nadded after the last sync\n")

	dir, derr := IndexDirFor(home, vault)
	require.NoError(t, derr)
	manifestPath := filepath.Join(dir, ManifestFileName)
	exists, existsErr := ManifestExists(manifestPath)
	require.NoError(t, existsErr)
	if !exists {
		t.Fatalf("test precondition failed: manifest absent at %s; drift cannot be tested against a nonexistent manifest", manifestPath)
	}
	m, loadErr := LoadManifest(manifestPath, vault)
	require.NoError(t, loadErr)
	if m.Len() != 1 {
		t.Fatalf("test precondition failed: manifest holds %d entries, want exactly 1 (only one.md was synced) so the disk count of 2 genuinely disagrees", m.Len())
	}

	text := mnbDescribe(t, home, "mia", ws)

	if strings.Contains(text, "NOT INDEXED") {
		t.Errorf("a built-but-drifted collection must not be reported as never built; got:\n%s", text)
	}
	if strings.Contains(text, "index holds 2 notes") {
		t.Errorf("a built-but-drifted collection must not render the plain matched-count phrasing "+
			"the fresh case uses; got:\n%s", text)
	}
	if !strings.Contains(text, "index holds 1 of 2 notes on disk") {
		t.Errorf("a built-but-drifted collection must state its true coverage; got:\n%s", text)
	}
	if strings.Contains(text, "re-index") {
		t.Errorf("the message must not name 're-index' or any other remedy action that does not exist; got:\n%s", text)
	}
}
