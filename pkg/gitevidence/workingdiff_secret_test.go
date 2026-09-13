// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// workingdiff_secret_test.go pins the MIN-5 secret discipline on
// DiffWorkingTree (review finding 6, 2026-09-11).
//
// The defect these tests close: DiffWorkingTree walked the live working tree
// with a raw os.ReadFile per path and NO secret check at all, while Commit —
// the narrower exposure, since it only writes a local repo — refuses to run
// without a guard and excludes matching paths. The Judge path opened the repo
// with no options whatsoever and called DiffWorkingTree, so an agent that
// wrote an API key into work/.env got it excluded from the boundary commit,
// then classified here as an "insert", then rendered into the Judge's prompt
// and sent to the external model on EVERY round.
package gitevidence

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// testSecretKey is a syntactically real, never-issued OpenAI-shaped key: it
// matches audit's shipped `sk-[a-zA-Z0-9\-]{20,}` format pattern, so the
// scanner's pattern layer flags it with no credential registry needed.
const testSecretKey = "sk-verifierleakcanary0123456789abcdef"

func newSecretScannerForTest(t *testing.T) *audit.SecretScanner {
	t.Helper()
	s, err := audit.NewSecretScanner(nil, nil)
	if err != nil {
		t.Fatalf("audit.NewSecretScanner: %v", err)
	}
	return s
}

// TestDiffWorkingTree_RefusesWithoutSecretGuard is the fail-closed half: a
// Repo opened with NEITHER WithSecretScanner NOR WithRedactor — which is
// exactly how the judge path used to open it — must refuse the read rather
// than return unscanned working-tree content. Mirrors Commit's own
// MAJOR-3/MIN-5 guard.
func TestDiffWorkingTree_RefusesWithoutSecretGuard(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir) // deliberately unguarded — no options at all
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeFile(t, dir, ".env", "OPENAI_API_KEY="+testSecretKey+"\n")

	ev, err := r.DiffWorkingTree("", nil)
	if err == nil {
		t.Fatalf("DiffWorkingTree on an unguarded repo must refuse; got evidence %+v", ev)
	}
	if ev != nil {
		t.Errorf("a refused DiffWorkingTree must return nil evidence, got %+v", ev)
	}
	if !strings.Contains(err.Error(), "MIN-5") {
		t.Errorf("refusal must name the MIN-5 fail-closed rule, got %q", err.Error())
	}
	if strings.Contains(err.Error(), testSecretKey) {
		t.Error("the refusal error must not echo file content")
	}
}

// TestDiffWorkingTree_WithholdsSecretBearingPatch is the exclusion half, and
// the one that directly models the reported leak: an uncommitted work/.env
// holding an API key. The CHANGE must still be reported (the Judge must never
// be told "nothing happened here") while the CONTENT must not appear anywhere
// in the evidence.
func TestDiffWorkingTree_WithholdsSecretBearingPatch(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, WithSecretScanner(newSecretScannerForTest(t)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeFile(t, dir, ".env", "OPENAI_API_KEY="+testSecretKey+"\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	ev, err := r.DiffWorkingTree("", nil)
	if err != nil {
		t.Fatalf("DiffWorkingTree: %v", err)
	}

	var envDiff, cleanDiff *FileDiff
	for i := range ev.Files {
		switch ev.Files[i].Path {
		case ".env":
			envDiff = &ev.Files[i]
		case "main.go":
			cleanDiff = &ev.Files[i]
		}
	}
	if envDiff == nil {
		t.Fatal("the secret-bearing path must still be REPORTED as changed — withholding the whole file " +
			"would tell the Judge nothing changed there (fail-open)")
	}
	if envDiff.Kind != "insert" {
		t.Errorf(".env Kind = %q, want %q", envDiff.Kind, "insert")
	}
	if strings.Contains(envDiff.Patch, testSecretKey) {
		t.Fatal("SECRET LEAK: the withheld patch still carries the key that would reach the external model")
	}
	if envDiff.Patch != secretWithheldPatch {
		t.Errorf(".env Patch = %q, want the withheld marker %q", envDiff.Patch, secretWithheldPatch)
	}

	// Control: a clean sibling in the same diff is unaffected — the exclusion
	// is per-path, never "one secret suppresses the round's evidence".
	if cleanDiff == nil {
		t.Fatal("the clean path must still be diffed")
	}
	if !strings.Contains(cleanDiff.Patch, "func main()") {
		t.Errorf("clean path's patch lost its real content: %q", cleanDiff.Patch)
	}

	// And nothing anywhere in the rendered evidence carries the key.
	for _, f := range ev.Files {
		if strings.Contains(f.Patch, testSecretKey) {
			t.Fatalf("SECRET LEAK in %s", f.Path)
		}
	}
}

// TestDiffWorkingTree_WithholdsSecretAlreadyInHistory covers the FROM side:
// a secret that reached a commit before the guard existed is read back out of
// the committed tree by this function too, and must be withheld on that side
// as well. (The file is staged with an identity redactor so the commit itself
// is possible; the diff is then taken through a real scanner — the shape of a
// repo whose history predates the guard.)
func TestDiffWorkingTree_WithholdsSecretAlreadyInHistory(t *testing.T) {
	dir := t.TempDir()
	unguarded, err := Open(dir, WithRedactor(noOpRedactor))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeFile(t, dir, "config.yaml", "token: "+testSecretKey+"\n")
	res, err := unguarded.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"config.yaml"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Skipped {
		t.Fatalf("arrange: commit skipped (%s)", res.SkipReason)
	}

	guarded, err := Open(dir, WithSecretScanner(newSecretScannerForTest(t)))
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	// Change the file so it shows up as a modify against the committed side.
	writeFile(t, dir, "config.yaml", "token: "+testSecretKey+"\nextra: 1\n")

	ev, err := guarded.DiffWorkingTree(res.Hash, nil)
	if err != nil {
		t.Fatalf("DiffWorkingTree: %v", err)
	}
	if len(ev.Files) != 1 || ev.Files[0].Path != "config.yaml" {
		t.Fatalf("Files = %+v, want exactly config.yaml", ev.Files)
	}
	if ev.Files[0].Kind != "modify" {
		t.Errorf("Kind = %q, want modify", ev.Files[0].Kind)
	}
	if strings.Contains(ev.Files[0].Patch, testSecretKey) {
		t.Fatal("SECRET LEAK: a secret already in history was rendered into the diff evidence")
	}
	if ev.Files[0].Patch != secretWithheldPatch {
		t.Errorf("Patch = %q, want the withheld marker", ev.Files[0].Patch)
	}
}

// TestDiffWorkingTree_GuardedRepoWithNoSecretsIsUnchanged is the regression
// control: adding the guard must not change the evidence a clean workspace
// produces.
func TestDiffWorkingTree_GuardedRepoWithNoSecretsIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, WithSecretScanner(newSecretScannerForTest(t)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeFile(t, dir, "a.txt", "hello\n")

	ev, err := r.DiffWorkingTree("", nil)
	if err != nil {
		t.Fatalf("DiffWorkingTree: %v", err)
	}
	if len(ev.Files) != 1 || ev.Files[0].Path != "a.txt" {
		t.Fatalf("Files = %+v, want exactly a.txt", ev.Files)
	}
	if !strings.Contains(ev.Files[0].Patch, "+hello") {
		t.Errorf("a clean file's real patch must survive, got %q", ev.Files[0].Patch)
	}
	if ev.Matched != 1 || ev.Total != 1 {
		t.Errorf("Matched/Total = %d/%d, want 1/1", ev.Matched, ev.Total)
	}
}
