// Omnipus — W7's exit-criterion harness: re-import the founder's real vault
// and report the three-way outcome per base against the 0-clean-of-18
// baseline (spec FR-104..FR-109, W7).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kbFixtureEnv names the environment variable pointing at a COPY of the
// founder's real Obsidian vault. The measurement this variable enables is
// W7's exit criterion and it cannot be synthesised: the whole point of the
// number is that it comes from 757 real notes and 18 real `.base` files
// somebody actually wrote, not from a fixture built to pass.
//
// Unset, every test here SKIPS. That is deliberate and it is also the one
// thing about this harness worth distrusting — a skipped test is green, so
// the CI-visible signal from this file is nil. The tests that MUST hold on
// every machine live in the other _test.go files in this package and use
// committed testdata.
const kbFixtureEnv = "OMNIPUS_KB_FIXTURE"

// copyVaultTo copies a vault tree into dst, skipping the control-plane
// directory so every run starts from the same place regardless of what a
// previous run wrote. The source is never modified: FR-104b writes `type:`
// into notes, so importing the founder's own copy in place would edit it.
func copyVaultTo(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		top := strings.Split(filepath.ToSlash(rel), "/")[0]
		if top == ".omnipus-vault" || top == ".git" || top == ".obsidian" || top == ".claude" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copying the fixture vault: %v", err)
	}
}

// fixtureVaultCopy returns a writable copy of the fixture vault, or skips.
func fixtureVaultCopy(t *testing.T) string {
	t.Helper()
	src := os.Getenv(kbFixtureEnv)
	if src == "" {
		t.Skipf("%s is unset — this is the real-vault exit-criterion harness (W7); set it to a copy of the founder's vault to run it", kbFixtureEnv)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("%s=%q does not exist: %v", kbFixtureEnv, src, err)
	}
	dst := t.TempDir()
	copyVaultTo(t, src, dst)
	return dst
}

// TestFixtureVault_ThreeWayOutcomeAgainstBaseline re-imports the founder's
// vault and PRINTS the whole report plus the counted three-way outcome. It
// asserts only what the requirements actually promise about this vault, and
// the numbers themselves are reported rather than pinned — pinning a count
// that moves whenever a peer's package changes an inference would turn W7's
// measurement into a brittle equality nobody trusts.
func TestFixtureVault_ThreeWayOutcomeAgainstBaseline(t *testing.T) {
	root := fixtureVaultCopy(t)

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	var buf bytes.Buffer
	rep.Render(&buf)
	writeArtifact(t, "import-report.txt", buf.Bytes())
	t.Log("\n" + buf.String())

	var clean, lossy, refused, disabled, views int
	for _, b := range rep.Bases {
		switch b.Status {
		case OutcomeConverted:
			clean++
		case OutcomeConvertedWithLosses:
			lossy++
		case OutcomeRefused:
			refused++
		}
		for _, v := range b.Views {
			views++
			if v.Disabled {
				disabled++
			}
		}
	}
	t.Logf("THREE-WAY OUTCOME over %d `.base` files: CLEAN=%d  WITH-LOSSES=%d  REFUSED=%d  (views=%d, of which DISABLED=%d)",
		len(rep.Bases), clean, lossy, refused, views, disabled)
	t.Logf("BASELINE (Draft 10, before this work): 10 converted with losses, 8 refused, 0 clean")

	if len(rep.Bases) == 0 {
		t.Fatal("no `.base` files were found at all — the fixture is not the vault this measurement is about")
	}

	// FR-105: the ONE assertion this harness makes unconditionally. Any view
	// carrying a loss whose position affects the row set must be DISABLED.
	// A loss in an annotation position must NOT disable it. Both halves are
	// checked, so the assertion cannot pass by disabling everything.
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			rowSetLoss := false
			for _, l := range v.Losses {
				if lossPositionAffectsRowSet(l) {
					rowSetLoss = true
				}
			}
			if rowSetLoss && !v.Disabled {
				t.Errorf("FR-105 VIOLATED: %s / %q carries a row-set-affecting loss and is ENABLED — it would return more rows than the original.\n  losses: %v",
					b.BaseRelPath, v.DisplayName, v.Losses)
			}
			if !rowSetLoss && v.Disabled {
				t.Errorf("%s / %q is disabled with no row-set-affecting loss — a view disabled for an annotation loss is a false negative.\n  losses: %v",
					b.BaseRelPath, v.DisplayName, v.Losses)
			}
		}
	}
}

// TestFixtureVault_UntypedNotesAreDecided is FR-104b's real-vault half: the
// founder's vault carries untyped notes, and after the import EVERY one of
// them has a recorded outcome — a written `type:` or a named reason it could
// not be inferred. "Left as is" must be a decision on the record, never a
// silent skip.
func TestFixtureVault_UntypedNotesAreDecided(t *testing.T) {
	root := fixtureVaultCopy(t)

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	untyped := rep.Discriminator.WithoutType
	decided := len(rep.TypeInference.Notes)
	t.Logf("FR-104b: %d notes carried no `type:`; %d have a recorded outcome (written=%d, ambiguous=%d, no-match=%d)",
		untyped, decided, rep.TypeInference.Written, rep.TypeInference.Ambiguous, rep.TypeInference.NoMatch)

	if untyped == 0 {
		t.Skip("this vault has no untyped notes — FR-104b has nothing to decide here")
	}
	if decided != untyped {
		t.Errorf("FR-104b: %d untyped notes but only %d recorded outcomes — %d were silently skipped", untyped, decided, untyped-decided)
	}
	for _, n := range rep.TypeInference.Notes {
		if n.Reason == "" {
			t.Errorf("FR-104b: %s has an outcome with no stated reason", n.RelPath)
		}
	}
}

// writeArtifact drops a copy of the rendered report beside the test output
// when OMNIPUS_KB_ARTIFACTS names a directory, so the numbers can be read
// without scrolling a test log.
func writeArtifact(t *testing.T, name string, data []byte) {
	t.Helper()
	dir := os.Getenv("OMNIPUS_KB_ARTIFACTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("could not create artifact dir %q: %v", dir, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Logf("could not write artifact %q: %v", name, err)
	}
}

var _ io.Writer = (*bytes.Buffer)(nil)
