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
// ROUND-2 IMP-1 CORRECTION (2026-10-03, qa-lead). The header above records
// this pack's original provenance, quoting register row 18's pre-correction
// "self-evaluated" wording. The settlement (w2 spec §3.8 E-1 cell, §4.1 Save
// row, §4.2 gate-decision row; register R-4) splits row 18's unified
// condition across TWO components with ONE evaluator and ONE enforcer: the
// PUBLISHER (w5-integration) runs the check-ignore-equivalent evaluation —
// this package's EvaluateStagingExclusion is its E-1 entry point — and
// publishes a GateDecision (allowed Boolean + nullable safe notice_code);
// the ENFORCER (FolderSnapshotStore.Save) consumes the published decision,
// never re-evaluates, and fails CLOSED on absence. Per §7.1's gate rows this
// pack therefore injects the decision in all three shapes — ABSENT (refuse +
// live-only + cache_unavailable + zero artifacts), NOT-ALLOWED (the shape the
// E-3 pre-tracked case arrives in: refuse, tracked state untouched) and
// ALLOWED (the write succeeds) — and, where the chain matters, derives the
// injected decision from the publisher's own EvaluateStagingExclusion
// verdict asserted in the same test. Assertions are never weakened relative
// to the pre-correction pack; each test below states what moved where.
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

// testNoticeCacheUnavailable is the Phase-1 safe notice_code the w2 spec
// pins for the disabled disk cache (§3.8 gate paragraph; the landed
// contracts/components/schemas/MailReadMetadata.yaml notice_code value).
const testNoticeCacheUnavailable = "cache_unavailable"

// noticePtr hands a GateDecision its *string NoticeCode.
func noticePtr(s string) *string { return &s }

// gateStore builds a snapshot store whose baseDir stands in for the resolved
// data root (§3.8: <OmnipusHomeDir()>/mail-cache/<opaque-pair-id>/folders.enc;
// OmnipusHomeDir is injected by the caller in production).
//
// This builder injects NO gate decision: it is the ABSENT-decision
// instrument of §7.1's split (round-2 IMP-1, 2026-10-03) — a store built
// before/without the publisher's decision must refuse every write (fail
// closed on absence, §3.8 gate paragraph), which is exactly the row
// TestMailCacheExclusionGate_BlocksWhenMissing asserts. For the other two
// injection shapes see gateStoreAllowed / gateStoreNotAllowed.
func gateStore(t *testing.T, base string) *FolderSnapshotStore {
	t.Helper()
	return NewFolderSnapshotStore(base,
		func(Scope) ([]byte, error) { return repeatByte(0xAA, 32), nil },
		testPurposeFolder,
		func(Scope) Revision { return Revision(1) },
	)
}

// gateStoreWithDecision builds the same store with an explicit published
// GateDecision — §7.1's injection point (round-2 IMP-1). The decision below
// models the PUBLISHER's verdict for the fixture under test; every caller
// asserts the publisher's own EvaluateStagingExclusion result first, so the
// injection is derived from the evaluation — never a store-side
// re-evaluation and never a free-floating allowance.
func gateStoreWithDecision(t *testing.T, base string, d GateDecision) *FolderSnapshotStore {
	t.Helper()
	return NewFolderSnapshotStore(base,
		func(Scope) ([]byte, error) { return repeatByte(0xAA, 32), nil },
		testPurposeFolder,
		func(Scope) Revision { return Revision(1) },
		WithGateDecision(d),
	)
}

// gateStoreAllowed is the ALLOWED injection of §7.1: the publisher evaluated
// both halves as holding (the staging half via EvaluateStagingExclusion —
// asserted by the caller — and the E-2 backup skip attested by its owner)
// and published allowed=true; the write path must proceed (§7.1: allowed=true
// → write succeeds).
func gateStoreAllowed(t *testing.T, base string) *FolderSnapshotStore {
	t.Helper()
	return gateStoreWithDecision(t, base, GateDecision{Allowed: true})
}

// gateStoreNotAllowed is the NOT-ALLOWED injection of §7.1, carrying the
// Phase-1 safe notice: the shape the E-3 pre-tracked case arrives in (§7.1:
// "the pre-tracked case arrives as a not-allowed decision and is refused")
// and the shape the publisher publishes wherever the evaluation cannot prove
// exclusion. The write path must refuse, surface the notice and leave
// tracked/existing state untouched.
func gateStoreNotAllowed(t *testing.T, base string) *FolderSnapshotStore {
	t.Helper()
	return gateStoreWithDecision(t, base, GateDecision{Allowed: false, NoticeCode: noticePtr(testNoticeCacheUnavailable)})
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

// TestMailCacheExclusionGate_BlocksWhenMissing — G-2, US-7.2, CX-14; §7.1's
// ABSENT-decision row.
// Round-2 IMP-1 correction (2026-10-03): the refusal asserted here now
// arrives through the settled mechanism — a store built WITHOUT a published
// gate decision refuses every write (fail closed on absence, §3.8 gate
// paragraph; §4.2), because the enforcer never re-evaluates the exclusion
// itself (register row 18's one-evaluator/one-enforcer split). The property
// is unchanged: zero cache files exist, the cache-unavailable class
// surfaces, the mailbox runs live-only. CX-14's mutation — a gate that logs
// and continues — still dies on the file assertions, and the fail-open
// mutant ("no decision → write anyway") dies on the refusal assertion.
func TestMailCacheExclusionGate_BlocksWhenMissing(t *testing.T) {
	base := t.TempDir()
	gitIn(t, base, "init")
	if !gitIsRepo(t, base) {
		t.Fatal("precondition: the base dir must be a git repository for this gate case")
	}
	// Publisher half, asserted negatively so the fixture stays consistent
	// with the injected absence: in a bare repository with no covering rule,
	// the publisher's E-1 evaluation must NOT prove exclusion (§3.8 E-1) —
	// i.e. a real publisher would have published not-allowed, never allowed.
	if evalErr := EvaluateStagingExclusion(base); evalErr == nil {
		t.Fatal("precondition: the publisher's staging-half evaluation must not prove exclusion in a bare repository without a covering rule (§3.8 E-1)")
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
// G-1, US-7.1, §7.1's ALLOWED-decision row, and the dispatch brief's PRODUCT
// property: a cache write leaves the data folder's version-control status
// unchanged.
// Round-2 IMP-1 correction (2026-10-03): the withdrawn design had the STORE
// self-evaluate the staging half and allow the write on its own verdict; that
// reading is settled away (register row 18; w2 spec §3.8 E-1 cell, §4.1 Save
// row — one evaluator, one enforcer). The property now spans the chain, and
// this test asserts each link in order: (1) the PUBLISHER's E-1 evaluation —
// EvaluateStagingExclusion, the entry point §3.8/§4.2 name — proves exclusion
// under the committed covering rule; (2) the publisher publishes allowed=true
// (modelled by the injection, derived from the verdict just asserted — never
// a store-side re-evaluation); (3) the ENFORCER consumes the allowed decision
// and the write succeeds (§7.1: allowed=true → write succeeds; §7 line 510).
// Oracle: git's status — the independent instrument the spec's
// "check-ignore-equivalent semantics" names — shows NOTHING before and
// after; and a POSITIVE CONTROL plain file written beside it IS reported by
// the same instrument, proving the instrument could have seen a capture
// (false-green rule: test the instrument).
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

	// Link 1 — the publisher's staging-half evaluation proves exclusion
	// (§3.8 E-1; the evaluation is the publisher's per register row 18).
	if evalErr := EvaluateStagingExclusion(base); evalErr != nil {
		t.Fatalf("the publisher's staging-half evaluation must prove exclusion under the committed covering rule (§3.8 E-1, register row 18): %v", evalErr)
	}

	// Links 2+3 — the published allowed decision (modelled by the injection)
	// lets the first write through (§7.1 ALLOWED row; G-1).
	store := gateStoreAllowed(t, base)
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

// TestMailCacheExclusionGate_DetectsPreTrackedFile — G-3, E-3; §7.1's
// NOT-ALLOWED row ("the pre-tracked case arrives as a not-allowed decision
// and is refused").
// Round-2 IMP-1 correction (2026-10-03): under the settled split the tracked
// verdict belongs to the PUBLISHER's evaluation (EvaluateStagingExclusion
// reporting the E-3 ErrCachePathTracked flavour, asserted below), and the
// write path refuses the two refusal shapes §7.1 names — the ABSENT decision
// (fail closed on absence) and the published NOT-ALLOWED decision that a
// real publisher publishes for this fixture. The git oracle cross-check
// still shows the ignore rule alone WOULD match the path — proving the
// tracked-ness, not the rule, flips the verdict — and the tracked file must
// survive both refusals untouched.
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

	// Publisher half, asserted so the injected decision below is derived from
	// the evaluation (§3.8 E-3; §4.2): the publisher's E-1 evaluation must
	// report the E-3 TRACKED flavour here — the decision its publisher would
	// publish is not-allowed, never allowed.
	if evalErr := EvaluateStagingExclusion(base); evalErr == nil || !errors.Is(evalErr, ErrCachePathTracked) {
		t.Fatalf("the publisher's evaluation must report the E-3 tracked flavour for a committed cache path (§3.8 E-3; §7.1: the pre-tracked case arrives as a not-allowed decision), got %v", evalErr)
	}

	// §7.1's prescribed arrival shape: the pre-tracked case reaches the write
	// path as a published NOT-ALLOWED decision (with the Phase-1 safe notice).
	// The refusal must surface that notice and STILL leave the tracked file
	// exactly as committed (the enforcer never removes on a gate refusal —
	// §3.8/Save: it cannot distinguish a tracked path from a not-provable
	// one; cleanup belongs to the removal cascade).
	refused := gateStoreNotAllowed(t, base)
	err := refused.Save(scope, testSnapshot(), Revision(1), repeatByte(0xAA, 32))
	if !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("a published not-allowed decision must refuse the write on a pre-tracked cache path (§7.1/E-3), got %v", err)
	}
	if !strings.Contains(err.Error(), testNoticeCacheUnavailable) {
		t.Fatalf("the not-allowed refusal must carry the safe notice_code %q (§3.8 gate paragraph: visible notice), got %v", testNoticeCacheUnavailable, err)
	}
	got2, readErr2 := os.ReadFile(cachePath)
	if readErr2 != nil || string(got2) != pretracked {
		t.Fatalf("the not-allowed refusal must not touch the tracked file, err=%v content=%q", readErr2, got2)
	}
}

// TestMailCacheExclusionGate_NoRepositoryHoldsTrivially — E-1's trivial-hold
// clause (conformance item (e), §3.8 evaluator conformance contract); §7.1's
// ALLOWED row.
// Round-2 IMP-1 correction (2026-10-03): the trivial hold is a verdict of
// the PUBLISHER's evaluation, not of the store — where no version-control
// staging exists in the data directory there is nothing to be captured by
// and the staging half holds trivially (§3.8 E-1), so the publisher publishes
// allowed=true and the enforcer writes. The test asserts both links: the
// evaluation's trivial hold, then the write through the injected allowed
// decision derived from it. The git instrument still proves the precondition
// (no repo), keeping this from silently becoming "git missing = pass".
func TestMailCacheExclusionGate_NoRepositoryHoldsTrivially(t *testing.T) {
	base := t.TempDir()
	if gitIsRepo(t, base) {
		t.Fatal("precondition: the base dir must NOT be a repository for this gate case")
	}

	// Link 1 — the publisher's evaluation holds trivially (§3.8 E-1 (e)).
	if evalErr := EvaluateStagingExclusion(base); evalErr != nil {
		t.Fatalf("with no repository the staging half must hold trivially (§3.8 E-1; conformance item (e)), got %v", evalErr)
	}

	// Link 2 — the published allowed decision lets the write through.
	store := gateStoreAllowed(t, base)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := store.Save(scope, testSnapshot(), Revision(1), repeatByte(0xAA, 32)); err != nil {
		t.Fatalf("the staging half holds trivially with no repository — the publisher publishes allowed and the write must succeed (E-1/§7.1), got %v", err)
	}
	if _, statErr := os.Stat(snapshotPath(base, "pair-1")); statErr != nil {
		t.Fatalf("the sealed snapshot must exist: %v", statErr)
	}
}
