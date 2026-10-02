package email

// RED additions — W2 staging-exclusion EVALUATOR conformance (oracle: spec
// only, never the implementation).
//
// This file closes the CHECK mutation-audit hole F-1 (survivor M11b): a naive
// evaluator that reads rule EXISTENCE ("a .gitignore exists ⇒ excluded")
// instead of rule MATCHING passed the entire gate family, so a data directory
// whose ignore file does not cover the cache path would have its first cache
// write accepted — and the next staging pass captures the sealed snapshot,
// the exact capture §3.8 exists to make impossible.
//
// Every expected value derives from the specification, never from the code:
//
//   - w2 spec §3.8 E-1/E-3 and the "Evaluator conformance contract"
//     (round-2 IMP-2): check-ignore-EQUIVALENT semantics — negation with
//     last-match-wins, nested ignore files, $GIT_DIR/info/exclude,
//     tracked-state precedence, no-repo holds trivially, and the divergence
//     rule (uninterpretable rule state fails CLOSED: not provable ⇒ refuse).
//   - §7.1 evaluator-conformance rows and §7.2 DT-6 (the conformance dataset).
//   - §11 CX-21: the whitelist-style repo (`*` + `!config.json` …) is NOT
//     excluded — the naive "matched some deny rule ⇒ excluded" matcher must
//     die here.
//
// ORACLE INSTRUMENT (the same named instrument the gate pack uses): the real
// git binary builds the repository states and cross-checks the ignore
// verdicts. Test-only — the spec forbids production from shelling out to git
// on this path (§3.8 E-1 / Hard Constraint #2). Every git invocation runs
// with a scrubbed environment (mail_cache_gate_test.go::gitIn).
//
// The POSITIVE CONTROL (TestExclusionEvaluator_CoveringRepoIsExcluded) keeps
// this conformance set from passing by always refusing: a repo whose rules
// genuinely cover the cache path must let the write through.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// evaluatorFixture builds a committed repository at a fresh data root with
// the given root .gitignore content and returns the base path.
func evaluatorFixture(t *testing.T, gitignore string) string {
	t.Helper()
	base := t.TempDir()
	gitIn(t, base, "init")
	if !gitIsRepo(t, base) {
		t.Fatal("precondition: the fixture directory must be a git repository")
	}
	if gitignore != "" {
		if err := os.WriteFile(filepath.Join(base, ".gitignore"), []byte(gitignore), 0o644); err != nil {
			t.Fatalf("write .gitignore: %v", err)
		}
		// -f: the whitelist shape's "*" rule ignores .gitignore itself — a
		// committed fixture is the point (staging must capture the rule state).
		gitIn(t, base, "add", "-f", ".gitignore")
		commitAll(t, base, "fixture ignore rules")
	}
	return base
}

// writeEvalFile writes a file creating parent directories.
func writeEvalFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// gitCheckIgnore runs `git check-ignore [--no-index] -v <path>` in dir with
// the scrubbed environment and reports git's verdict. Directory queries pass
// a trailing slash ("mail-cache/") so directory-only patterns apply even
// though the gate has not created the directory. With -v the PATTERN column
// is the verdict — git exits 0 for a negation match too, so the parsed
// pattern decides: a "!"-prefixed match means NOT ignored. Exit 1 = no
// matching rule = not ignored. Anything else is a fixture or git failure
// (fatal — the instrument must never fail silently).
func gitCheckIgnore(t *testing.T, dir, path string, noIndex bool) (ignored bool, source string) {
	t.Helper()
	args := []string{"check-ignore", "-v"}
	if noIndex {
		args = append(args, "--no-index")
	}
	args = append(args, path)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir),
	}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	switch {
	case err == nil:
		if text == "" {
			return false, "" // defensive: git exits 0 with no -v output for a non-matching --no-index query
		}
		line := strings.SplitN(text, "\n", 2)[0]
		spec := strings.SplitN(line, "\t", 2)[0] // "<source>:<lineno>:<pattern>"
		pattern := spec[strings.LastIndex(spec, ":")+1:]
		if strings.HasPrefix(pattern, "!") {
			return false, line // re-included by a negation: NOT ignored
		}
		return true, line
	case strings.Contains(err.Error(), "exit status 1"):
		return false, ""
	default:
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
		return false, ""
	}
}

// assertFirstWriteRefused drives the gate through Save and asserts the typed
// refusal plus zero artifacts left behind (CX-14 hygiene: a refusing gate
// leaves nothing, not even the directory).
func assertFirstWriteRefused(t *testing.T, base string) {
	t.Helper()
	store := gateStore(t, base)
	err := store.Save(Scope{PairID: "pair-1", Generation: "gen-1"}, testSnapshot(), Revision(1), repeatByte(0xAA, 32))
	if !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("the first cache write must be refused where the data directory's ignore state does not provably exclude the cache path (§3.8 E-1 check-and-refuse), got %v", err)
	}
	if _, statErr := os.Stat(snapshotPath(base, "pair-1")); statErr == nil {
		t.Fatal("a refused write left folders.enc behind — the gate refuses before any filesystem effect (§3.8)")
	}
}

// TestExclusionEvaluator_WhitelistRepoIsNotExcluded — CX-21, DT-6 row 1, the
// F-1 killer. Oracle: the whitelist-style data-repository shape re-includes
// directories (`!*/`), so `git check-ignore mail-cache` returns NOT ignored —
// a repo whose ignore file EXISTS but does not cover the cache path must not
// be judged excluded. Two shapes: (a) the spec's whitelist repo; (b) a
// .gitignore with unrelated rules only. A rule-EXISTENCE evaluator (audit
// mutant M11b) accepts the first write in both and dies here.
func TestExclusionEvaluator_WhitelistRepoIsNotExcluded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		gitignore string
	}{
		{"whitelist-star-with-directory-reinclude", "*\n!*/\n!config.json\n"},
		{"unrelated-rules-only", "*.log\nnode_modules/\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := evaluatorFixture(t, tc.gitignore)

			// Instrument cross-check: git itself does NOT ignore the cache
			// path. The directory must EXIST for git's directory
			// re-inclusion to show (the post-write staging state); the
			// trailing slash alone is not enough for a directory that was
			// never created.
			if err := os.MkdirAll(filepath.Join(base, "mail-cache"), 0o700); err != nil {
				t.Fatalf("mkdir cache dir for the instrument check: %v", err)
			}
			if ignored, src := gitCheckIgnore(t, base, "mail-cache/", false); ignored {
				t.Fatalf("git oracle: check-ignore reported mail-cache ignored via %q — the fixture no longer matches CX-21's not-covered shape", src)
			}
			if err := os.Remove(filepath.Join(base, "mail-cache")); err != nil {
				t.Fatalf("remove cache dir after the instrument check: %v", err)
			}

			// The evaluator's own verdict, directly: rule MATCHING says the
			// path is not covered.
			repo, err := findGitRepo(base)
			if err != nil || repo == nil {
				t.Fatalf("findGitRepo: repo=%v err=%v", repo, err)
			}
			ignored, evalErr := repo.pathIgnored("mail-cache")
			if evalErr != nil {
				t.Fatalf("pathIgnored: %v", evalErr)
			}
			if ignored {
				t.Fatal("the evaluator judged the whitelist-style repo excluded — it read rule EXISTENCE, not rule MATCHING (CX-21/DT-6): want not excluded")
			}

			// The gate: the first write is refused (exclusion not provable).
			assertFirstWriteRefused(t, base)
		})
	}
}

// TestExclusionEvaluator_NegationReincludeRespected — DT-6 row 2. Oracle: a
// negation BELOW a denied directory cannot re-include the directory itself
// (git's ancestor stickiness): `mail-cache/` denied, `!mail-cache/keep.txt`
// re-including a descendant — the DIRECTORY's verdict stays ignored, so the
// gate can prove exclusion and the write succeeds. A mutant that treats any
// negation as "not excluded" dies on the success assertion.
func TestExclusionEvaluator_NegationReincludeRespected(t *testing.T) {
	base := evaluatorFixture(t, "mail-cache/\n!mail-cache/keep.txt\n")

	if ignored, src := gitCheckIgnore(t, base, "mail-cache/", false); !ignored {
		t.Fatal("git oracle: the denied directory must stay ignored despite the descendant negation — fixture drift")
	} else {
		t.Logf("git oracle: %s", src)
	}

	repo, err := findGitRepo(base)
	if err != nil || repo == nil {
		t.Fatalf("findGitRepo: repo=%v err=%v", repo, err)
	}
	ignored, evalErr := repo.pathIgnored("mail-cache")
	if evalErr != nil {
		t.Fatalf("pathIgnored: %v", evalErr)
	}
	if !ignored {
		t.Fatal("a negation below a denied directory must not re-include the directory itself (git last-match/ancestor semantics, DT-6 row 2): want excluded")
	}

	store := gateStore(t, base)
	if err := store.Save(Scope{PairID: "pair-1", Generation: "gen-1"}, testSnapshot(), Revision(1), repeatByte(0xAA, 32)); err != nil {
		t.Fatalf("a genuinely excluded cache path must let the first write through (G-1), got %v", err)
	}
}

// TestExclusionEvaluator_NestedIgnorePrecedence — DT-6 row 3. Oracle: a
// shallower deny sticks over a deeper negation for the DIRECTORY itself (a
// negation in mail-cache/.gitignore can never re-include mail-cache/), while
// with no deny anywhere the path is NOT excluded — the deeper file cannot
// manufacture coverage either. Both verdicts are git-cross-checked.
func TestExclusionEvaluator_NestedIgnorePrecedence(t *testing.T) {
	t.Run("shallower deny sticks over deeper negation", func(t *testing.T) {
		base := evaluatorFixture(t, "mail-cache/\n!mail-cache/keep.txt\n")
		// The deeper ignore file stays UNTRACKED working-tree state: at the
		// gate's decision time nothing inside mail-cache/ has ever been
		// staged (a TRACKED file inside would flip git's own directory
		// verdict, a different scenario — see the tracked row).
		writeEvalFile(t, filepath.Join(base, "mail-cache", ".gitignore"), "!*\n")

		if ignored, src := gitCheckIgnore(t, base, "mail-cache/", false); !ignored {
			t.Fatalf("git oracle: the shallower deny must stick — check-ignore said not ignored (%q)", src)
		}
		repo, err := findGitRepo(base)
		if err != nil || repo == nil {
			t.Fatalf("findGitRepo: repo=%v err=%v", repo, err)
		}
		ignored, evalErr := repo.pathIgnored("mail-cache")
		if evalErr != nil {
			t.Fatalf("pathIgnored: %v", evalErr)
		}
		if !ignored {
			t.Fatal("the deeper negation must not re-include the denied ancestor directory (git nested-file precedence, DT-6 row 3): want excluded")
		}
		store := gateStore(t, base)
		if err := store.Save(Scope{PairID: "pair-1", Generation: "gen-1"}, testSnapshot(), Revision(1), repeatByte(0xAA, 32)); err != nil {
			t.Fatalf("the ancestor deny proves exclusion — the write must succeed (G-1), got %v", err)
		}
	})

	t.Run("no deny anywhere is not excluded", func(t *testing.T) {
		base := evaluatorFixture(t, "*.log\n")
		writeEvalFile(t, filepath.Join(base, "mail-cache", ".gitignore"), "!*\n")

		if ignored, _ := gitCheckIgnore(t, base, "mail-cache/", false); ignored {
			t.Fatal("git oracle: no rule denies the cache path — fixture drift")
		}
		repo, err := findGitRepo(base)
		if err != nil || repo == nil {
			t.Fatalf("findGitRepo: repo=%v err=%v", repo, err)
		}
		ignored, evalErr := repo.pathIgnored("mail-cache")
		if evalErr != nil {
			t.Fatalf("pathIgnored: %v", evalErr)
		}
		if ignored {
			t.Fatal("a negation-only rule stack must not be read as coverage (DT-6 row 3 / CX-21 family): want not excluded")
		}
		assertFirstWriteRefused(t, base)
	})
}

// TestExclusionEvaluator_InfoExcludeHonoured — DT-6 row 4 (the
// .git/info/exclude face). Oracle: $GIT_DIR/info/exclude is part of the data
// directory's ACTUAL version-control state (E-1), honoured at its git
// precedence position: a deny only there still proves exclusion. (The
// core.excludesFile face of DT-6 row 4 is deliberately not asserted here —
// the global operator-config layer is spec-withheld from this evaluator by
// E-1's "never trusting an operator's setup" rule; recorded for adjudication
// in the delivery report.)
func TestExclusionEvaluator_InfoExcludeHonoured(t *testing.T) {
	base := evaluatorFixture(t, "")
	writeEvalFile(t, filepath.Join(base, ".git", "info", "exclude"), "mail-cache/\n")

	if ignored, src := gitCheckIgnore(t, base, "mail-cache/", false); !ignored {
		t.Fatalf("git oracle: .git/info/exclude must deny the path (got not ignored; last line: %q)", src)
	}

	repo, err := findGitRepo(base)
	if err != nil || repo == nil {
		t.Fatalf("findGitRepo: repo=%v err=%v", repo, err)
	}
	ignored, evalErr := repo.pathIgnored("mail-cache")
	if evalErr != nil {
		t.Fatalf("pathIgnored: %v", evalErr)
	}
	if !ignored {
		t.Fatal("a cache path denied only via .git/info/exclude must be judged excluded (DT-6 row 4): want excluded")
	}

	store := gateStore(t, base)
	if err := store.Save(Scope{PairID: "pair-1", Generation: "gen-1"}, testSnapshot(), Revision(1), repeatByte(0xAA, 32)); err != nil {
		t.Fatalf("the info/exclude deny proves exclusion — the write must succeed (G-1), got %v", err)
	}
}

// TestExclusionEvaluator_TrackedFileNotExcludedDespiteRule — DT-6 row 5, E-3
// at the evaluator's own layer (the gate-level twin is
// TestMailCacheExclusionGate_DetectsPreTrackedFile). Oracle: a cache path
// already in the index is NOT excluded even where a deny rule matches —
// ignore rules do not apply to tracked files. The git instrument shows both
// halves: --no-index proves the RULE matches, the plain probe proves the
// tracked state defeats it.
func TestExclusionEvaluator_TrackedFileNotExcludedDespiteRule(t *testing.T) {
	base := evaluatorFixture(t, "mail-cache/\n")
	cachePath := snapshotPath(base, "pair-1")
	writeEvalFile(t, cachePath, "committed-before-the-gate")
	gitIn(t, base, "add", "-f", "mail-cache/pair-1/folders.enc")
	commitAll(t, base, "a pre-tracked cache path")

	if ignored, src := gitCheckIgnore(t, base, "mail-cache/pair-1/folders.enc", true); !ignored {
		t.Fatalf("git oracle (--no-index): the deny rule should match the path — %q", src)
	}
	if ignored, _ := gitCheckIgnore(t, base, "mail-cache/pair-1/folders.enc", false); ignored {
		t.Fatal("git oracle: a TRACKED path is never ignored by plain check-ignore — fixture drift")
	}

	repo, err := findGitRepo(base)
	if err != nil || repo == nil {
		t.Fatalf("findGitRepo: repo=%v err=%v", repo, err)
	}
	tracked, trackErr := repo.tracksUnder("mail-cache")
	if trackErr != nil {
		t.Fatalf("tracksUnder: %v", trackErr)
	}
	if !tracked {
		t.Fatal("the evaluator must see the committed cache path as tracked (E-3/DT-6 row 5)")
	}

	// The gate refuses a tracked cache path whatever the rules say (E-3). The
	// tracked placeholder file legitimately exists — the refusal must leave
	// it exactly as committed, never overwrite or remove it.
	store := gateStore(t, base)
	err = store.Save(Scope{PairID: "pair-1", Generation: "gen-1"}, testSnapshot(), Revision(1), repeatByte(0xAA, 32))
	if !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("a pre-tracked cache path must refuse the first write even under a covering deny rule (E-3/DT-6 row 5), got %v", err)
	}
	got, readErr := os.ReadFile(cachePath)
	if readErr != nil || string(got) != "committed-before-the-gate" {
		t.Fatalf("the refused write must not touch the tracked file, err=%v content=%q", readErr, got)
	}
}

// TestExclusionEvaluator_UnparseableRulesFailClosed — DT-6 row 7, the §3.8
// divergence rule. Oracle: an ignore file carrying a construct the evaluator
// cannot interpret with check-ignore-equivalent semantics (here an
// unterminated character class) fails CLOSED — the evaluation is not
// provable, the gate refuses, the mailbox runs live-only. An unparseable
// state never widens the gate.
func TestExclusionEvaluator_UnparseableRulesFailClosed(t *testing.T) {
	base := evaluatorFixture(t, "open[\n*.log\n") // "open[" = unterminated "[" class

	repo, err := findGitRepo(base)
	if err != nil || repo == nil {
		t.Fatalf("findGitRepo: repo=%v err=%v", repo, err)
	}
	_, evalErr := repo.pathIgnored("mail-cache")
	if evalErr == nil {
		t.Fatal("an uninterpretable rule construct must fail the evaluation closed (§3.8 divergence rule / DT-6 row 7), got a clean verdict")
	}

	assertFirstWriteRefused(t, base)
}

// TestExclusionEvaluator_CoveringRepoIsExcluded — POSITIVE CONTROL for this
// conformance set (G-1/US-7.1). Oracle: a repository whose rules genuinely
// cover the cache path IS excluded and the first write succeeds. Without this
// control the set above could pass by always refusing — the mirror image of
// the mutant it kills.
func TestExclusionEvaluator_CoveringRepoIsExcluded(t *testing.T) {
	base := evaluatorFixture(t, "mail-cache/\n")

	ignored, src := gitCheckIgnore(t, base, "mail-cache/", false)
	if !ignored {
		t.Fatalf("git oracle: the covering rule must ignore the cache path (last line: %q)", src)
	}

	repo, err := findGitRepo(base)
	if err != nil || repo == nil {
		t.Fatalf("findGitRepo: repo=%v err=%v", repo, err)
	}
	ignored, evalErr := repo.pathIgnored("mail-cache")
	if evalErr != nil {
		t.Fatalf("pathIgnored: %v", evalErr)
	}
	if !ignored {
		t.Fatal("a repo whose rules genuinely cover the cache path must be judged excluded — the conformance set must not pass by always refusing")
	}

	store := gateStore(t, base)
	if err := store.Save(Scope{PairID: "pair-1", Generation: "gen-1"}, testSnapshot(), Revision(1), repeatByte(0xAA, 32)); err != nil {
		t.Fatalf("where the evaluation proves exclusion the first write must succeed (G-1/US-7.1), got %v", err)
	}
	if _, statErr := os.Stat(snapshotPath(base, "pair-1")); statErr != nil {
		t.Fatalf("the sealed snapshot must exist after the allowed write: %v", statErr)
	}
}
