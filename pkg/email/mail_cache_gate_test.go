package email

// RED pack — W2 first-write exclusion gate (oracle: spec + the git binary as
// the independent semantics oracle, never the implementation).
//
// Every expected value derives from the specification, written down before any
// implementation existed (the gate-enforcing Save does not compile yet — the
// expected RED state for a Wave-C RED pack):
//
//   - w2 spec §3.8 (the product-owned exclusion guarantee, rules E-1/E-3;
//     founder ruling Q-A 2026-10-02), §5 US-7, §6 G-1..G-3, §7.1 gate rows,
//     §11 CX-14; landing-order register row 18 (the unified stricter
//     condition: git check-ignore-EQUIVALENT semantics, self-evaluated in
//     product code, failing closed to live-only with the visible
//     cache_unavailable notice).
//   - ADR-20261001 filesystem table: "Verify cache files are not already
//     tracked; ignoring tracked files does not remove them."
//
// ORACLE INSTRUMENT (deliberate and named): these TESTS invoke the real git
// binary to build the data-directory states and to cross-check the ignore
// semantics. This is test-only. The SPEC forbids production code from
// shelling out to git on this path (§3.8 E-1 / Hard Constraint #2 / register
// row 18) — production must implement the equivalent in-process; git here is
// the independent definition of "check-ignore-equivalent" the spec names.
// Every git invocation runs with a scrubbed environment (no inherited
// GIT_* state, no system/global config, identity passed per-command), which
// is also how the pack pins the founder's "nothing depends on any
// machine-setup repository" ruling: the gate's verdict here flips ONLY on the
// data directory's own contents.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gateStore builds a snapshot store whose baseDir stands in for the resolved
// data root (§3.8: <OmnipusHomeDir()>/mail-cache/<opaque-pair-id>/folders.enc;
// OmnipusHomeDir is injected by the caller in production).
func gateStore(t *testing.T, base string) *FolderSnapshotStore {
	t.Helper()
	return NewFolderSnapshotStore(base,
		func(Scope) ([]byte, error) { return repeatByte(0xAA, 32), nil },
		testPurposeFolder,
		func(Scope) Revision { return Revision(1) },
	)
}

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// gitIn runs git in dir with a fully scrubbed environment and returns stdout.
// The scrubbing is the machine-independence instrument: no operator git
// config, no inherited GIT_* variables, no network.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir),
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func gitStatus(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, repo, "status", "--porcelain"))
}

func commitAll(t *testing.T, repo string, msg string) {
	t.Helper()
	gitIn(t, repo,
		"-c", "user.name=Cache Gate Test",
		"-c", "user.email=cache-gate-test@example.invalid",
		"commit", "-m", msg,
	)
}

func gitIsRepo(t *testing.T, dir string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1"}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// TestMailCacheExclusionGate_BlocksWhenMissing — G-2, US-7.2, CX-14.
// Oracle: where version-control staging exists (a data-directory repository)
// and the product cannot prove the cache path excluded, the first cache write
// is REFUSED: zero cache files exist, the cache-unavailable class surfaces,
// and the mailbox runs live-only. CX-14's mutation — a gate that logs and
// continues — dies on the file assertions.
func TestMailCacheExclusionGate_BlocksWhenMissing(t *testing.T) {
	base := t.TempDir()
	gitIn(t, base, "init")
	if !gitIsRepo(t, base) {
		t.Fatal("precondition: the base dir must be a git repository for this gate case")
	}

	store := gateStore(t, base)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	err := store.Save(scope, testSnapshot(), Revision(1), repeatByte(0xAA, 32))
	if !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("the first cache write must be refused where staging exists without a provable exclusion (G-2/E-1), got %v", err)
	}

	// Zero cache artifacts: no folders.enc, no temp/journal file (R-3.7-5:
	// no plaintext temporary ever exists), nothing outside .git.
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(base, path)
		if rerr != nil {
			return rerr
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return nil
		}
		t.Errorf("gate refused the write but %s exists — a refusing gate must leave nothing behind (CX-14)", rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestMailCacheExclusionGate_PassesWhenProvableAndWriteLeavesStatusUnchanged —
// G-1, US-7.1, and the dispatch brief's PRODUCT property: a cache write
// leaves the data folder's version-control status unchanged.
// Oracle: with a committed ignore rule covering the cache path (the
// product's own check can prove exclusion), the write succeeds; git's status
// — the independent instrument the spec's "check-ignore-equivalent semantics"
// names — shows NOTHING before and after; and a POSITIVE CONTROL plain file
// written beside it IS reported by the same instrument, proving the
// instrument could have seen a capture (false-green rule: test the
// instrument).
func TestMailCacheExclusionGate_PassesWhenProvableAndWriteLeavesStatusUnchanged(t *testing.T) {
	base := t.TempDir()
	gitIn(t, base, "init")
	if err := os.WriteFile(filepath.Join(base, ".gitignore"), []byte("mail-cache/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	gitIn(t, base, "add", ".gitignore")
	commitAll(t, base, "ignore the mail cache directory")
	if status := gitStatus(t, base); status != "" {
		t.Fatalf("precondition: the repo must be clean before the write, status: %s", status)
	}

	store := gateStore(t, base)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := store.Save(scope, testSnapshot(), Revision(1), repeatByte(0xAA, 32)); err != nil {
		t.Fatalf("the first cache write must succeed where both exclusions are provable (G-1/E-1), got %v", err)
	}
	if _, statErr := os.Stat(snapshotPath(base, "pair-1")); statErr != nil {
		t.Fatalf("the sealed snapshot must exist after a gated write: %v", statErr)
	}

	// THE PRODUCT PROPERTY: the write left the data folder's VCS status
	// unchanged — the cache file is invisible to staging.
	if status := gitStatus(t, base); status != "" {
		t.Fatalf("after a gated cache write, git status must be unchanged (empty), got:\n%s", status)
	}

	// POSITIVE CONTROL: the same instrument sees an ordinary allowed file.
	if err := os.WriteFile(filepath.Join(base, "control-marker.txt"), []byte("captured-if-not-ignored"), 0o644); err != nil {
		t.Fatalf("write control marker: %v", err)
	}
	status := gitStatus(t, base)
	if !strings.Contains(status, "control-marker.txt") {
		t.Fatalf("the instrument must see an ordinary untracked file (positive control), status:\n%s", status)
	}
	_ = os.Remove(filepath.Join(base, "control-marker.txt"))
}

// TestMailCacheExclusionGate_DetectsPreTrackedFile — G-3, E-3.
// Oracle: a cache path ever committed to the data-directory repository is
// NOT untracked by a deny rule (ignore rules do not apply to tracked files);
// the gate treats a tracked cache path as not-excluded and refuses disk
// writes. The git oracle cross-check shows the ignore rule alone WOULD match
// the path — proving the tracked-ness, not the rule, flips the verdict.
func TestMailCacheExclusionGate_DetectsPreTrackedFile(t *testing.T) {
	base := t.TempDir()
	gitIn(t, base, "init")
	if err := os.WriteFile(filepath.Join(base, ".gitignore"), []byte("mail-cache/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	cachePath := snapshotPath(base, "pair-1")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const pretracked = "pretracked-before-the-gate"
	if err := os.WriteFile(cachePath, []byte(pretracked), 0o600); err != nil {
		t.Fatalf("write pre-tracked placeholder: %v", err)
	}
	// add -f: tracking an ignored file is exactly the E-3 scenario.
	gitIn(t, base, "add", "-f", ".gitignore", "mail-cache/pair-1/folders.enc")
	commitAll(t, base, "a cache path that is already tracked")

	// Instrument cross-check. Git rule: ignore rules never apply to TRACKED
	// paths, so plain `git check-ignore` exits 1 with no output for a file
	// staged with -f — exactly this scenario — and the plain probe could
	// never pass here. `--no-index` evaluates the rule text regardless of the
	// index state, proving the RULE matches the path (so a gate trusting
	// ignore rules alone would wrongly allow it); the gate refusal below then
	// proves the tracked-ness is what flips the verdict (E-3).
	if out := gitIn(t, base, "check-ignore", "--no-index", "-v", "mail-cache/pair-1/folders.enc"); !strings.Contains(out, "mail-cache/pair-1/folders.enc") {
		t.Fatalf("instrument check: the ignore rule should match the pre-tracked cache path, got: %s", out)
	}

	store := gateStore(t, base)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := store.Save(scope, testSnapshot(), Revision(1), repeatByte(0xAA, 32)); !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("a pre-tracked cache path must block the gate (E-3/G-3), got %v", err)
	}
	got, readErr := os.ReadFile(cachePath)
	if readErr != nil || string(got) != pretracked {
		t.Fatalf("the refused write must not touch the tracked file, err=%v content=%q", readErr, got)
	}
}

// TestMailCacheExclusionGate_NoRepositoryHoldsTrivially — E-1's trivial-hold
// clause.
// Oracle: "where no version-control staging exists in the data directory
// there is nothing to be captured by and the staging half holds trivially" —
// the write succeeds in a plain (non-repository) directory. The git
// instrument proves the precondition (no repo), keeping this from silently
// becoming "git missing = pass".
func TestMailCacheExclusionGate_NoRepositoryHoldsTrivially(t *testing.T) {
	base := t.TempDir()
	if gitIsRepo(t, base) {
		t.Fatal("precondition: the base dir must NOT be a repository for this gate case")
	}

	store := gateStore(t, base)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := store.Save(scope, testSnapshot(), Revision(1), repeatByte(0xAA, 32)); err != nil {
		t.Fatalf("the staging half holds trivially with no repository — the write must succeed (E-1), got %v", err)
	}
	if _, statErr := os.Stat(snapshotPath(base, "pair-1")); statErr != nil {
		t.Fatalf("the sealed snapshot must exist: %v", statErr)
	}
}
