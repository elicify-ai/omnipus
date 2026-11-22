//go:build goolm && stdjson

package tools

// Order 62 — TestCardLabels_RetiredAlwaysAllowWording (C-26 point 1, founder
// R2-Q1 2026-11-22; the repo guard of the card time choices). Oracle:
// skills-tools-redesign-spec.md —
//   C-26 point 1: "No approval card offers 'Always allow', 'Always Allow'
//        or 'Allow everywhere' any more, for tools and for skills. Cards
//        offer Approve Once; Allow for 1 hour / 3 hours / 1 day; and Deny."
//   The spec's Regression table: the old Always Allow cases are rewritten
//        to their C-26 timed equivalents — the wording itself is retired,
//        so no hand-written production source may carry it.
//
// This is the repository guard (the check-no-goal-confirm-gate.sh
// convention): scan hand-written sources, drop comment-only lines, exclude
// generated trees, and FAIL listing every file that still carries a retired
// phrase. Scan roots missing is a fixture error, never a pass.
//
// CHECK round 1 finding 11: the directory skip used filepath.SkipAll, which
// stops the ENTIRE walk — every file after the first excluded directory went
// unscanned, so the guard could not have seen a violation placed after
// node_modules/. The skip is filepath.SkipDir (only that subtree), and
// TestCardLabels_GuardInstrument_PlantedPositiveControl proves the
// instrument in an isolated fixture: a violation planted AFTER an excluded
// subtree is caught, the excluded copy and the test-source copy are not, and
// a clean tree passes.
//
// RED today: the retired wording still lives in production sources —
// ToolApprovalPanel.tsx / ToolApprovalModal.tsx (the old card UI) and the
// Go grant paths (loop_policy.go, tool_approver.go, rest_tool_registry.go,
// approvalgrants.go, …) — so the guard fails listing them.
import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var retiredCardPhrases = []string{"Always Allow", "Always allow", "Allow everywhere"}

// scanForRetiredCardPhrases walks root, skipping excluded subtrees and
// comment-only lines, and returns one "path :: phrase" entry per offending
// file.
func scanForRetiredCardPhrases(root string, match func(path, name string) bool) ([]string, error) {
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "dist", ".git", "spa", "generated", "tests", "__tests__", "testdata":
				// CHECK finding 11: SkipDir skips ONLY this subtree and the
				// walk continues with the siblings. (filepath.SkipAll — the
				// earlier draft — abandoned the whole walk here, leaving
				// every later file unscanned.)
				return filepath.SkipDir
			}
			return nil
		}
		if !match(path, d.Name()) {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		found := ""
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "//") ||
				strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") {
				continue // comment-only line (the established guard convention)
			}
			for _, phrase := range retiredCardPhrases {
				if strings.Contains(line, phrase) {
					found = path + " :: " + phrase
					break
				}
			}
			if found != "" {
				break // one offending line is enough to name the file
			}
		}
		if found != "" {
			offenders = append(offenders, found)
		}
		return nil
	})
	return offenders, err
}

func isRetiredCardTestSource(name string) bool {
	return strings.HasSuffix(name, "_test.go") ||
		strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".test.tsx") ||
		strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".spec.tsx")
}

func TestCardLabels_RetiredAlwaysAllowWording(t *testing.T) {
	// The test binary runs with cwd = this package's directory.
	srcRoot := filepath.Join("..", "..", "src")
	pkgRoot := filepath.Join("..", "..", "pkg")
	for _, root := range []string{srcRoot, pkgRoot} {
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			t.Fatalf("BLOCKED: scan root %s is missing — the guard refuses to green on an absent tree", root)
		}
	}

	var offenders []string
	scan := func(root string, match func(path, name string) bool) {
		t.Helper()
		found, err := scanForRetiredCardPhrases(root, match)
		if err != nil {
			t.Fatalf("BLOCKED: scanning %s failed: %v", root, err)
		}
		offenders = append(offenders, found...)
	}

	scan(srcRoot, func(path, name string) bool {
		return (strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")) && !isRetiredCardTestSource(name)
	})
	scan(pkgRoot, func(path, name string) bool {
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	})

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("C-26 point 1 (R2-Q1): the retired card wording still lives in %d hand-written "+
			"production source(s); no approval card may offer \"Always allow\", \"Always Allow\" "+
			"or \"Allow everywhere\" any more:\n%s", len(offenders), strings.Join(offenders, "\n"))
	}
}

// TestCardLabels_GuardInstrument_PlantedPositiveControl proves the guard
// could have seen a violation (CHECK round 1 finding 11: test the
// instrument). The isolated fixture plants offenders after an excluded
// subtree and in a nested directory, a copy of the phrase inside the
// excluded subtree, a copy in a test source, and a comment-only copy in the
// clean file — then asserts exactly the reachable offenders are named.
func TestCardLabels_GuardInstrument_PlantedPositiveControl(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}

	write("a-clean-source.ts", "export const ok = 1\n// Always Allow — comment-only lines never offend\n")
	write("node_modules/leftover.ts", "const x = 'Always Allow'\n")        // excluded subtree
	write("t.test.tsx", "const x = 'Always Allow'\n")                      // test source, not scanned
	write("z-planted-after-excluded.ts", "const label = 'Always Allow'\n") // AFTER the excluded subtree
	write("nested/inner.ts", "const label = 'Allow everywhere'\n")         // nested, after it too
	write("generated/gen.ts", "const label = 'Always allow'\n")            // excluded generated tree
	write("go-pkg/after.go", "msg := \"Always allow\"\n")                  // Go source after exclusions

	offenders, err := scanForRetiredCardPhrases(root, func(path, name string) bool {
		if strings.Contains(path, string(os.PathSeparator)+"node_modules"+string(os.PathSeparator)) ||
			strings.Contains(path, string(os.PathSeparator)+"generated"+string(os.PathSeparator)) {
			return false
		}
		if strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx") {
			return !isRetiredCardTestSource(name)
		}
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	})
	if err != nil {
		t.Fatalf("instrument: the scan failed: %v", err)
	}

	sort.Strings(offenders)
	want := []string{
		filepath.Join(root, "go-pkg", "after.go") + " :: Always allow",
		filepath.Join(root, "nested", "inner.ts") + " :: Allow everywhere",
		filepath.Join(root, "z-planted-after-excluded.ts") + " :: Always Allow",
	}
	if len(offenders) != len(want) {
		t.Fatalf("instrument: the guard saw %v; want exactly the %d planted, reachable offenders %v — "+
			"excluded-subtree, generated, test-source and comment-only copies must stay unlisted",
			offenders, len(want), want)
	}
	for i := range want {
		if offenders[i] != want[i] {
			t.Fatalf("instrument: offender[%d] = %q, want %q", i, offenders[i], want[i])
		}
	}

	// The clean control: with only clean sources the instrument reports
	// nothing — so a green from the real-tree scan means clean, not blind.
	cleanRoot := t.TempDir()
	write2 := func(rel, body string) {
		t.Helper()
		path := filepath.Join(cleanRoot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	write2("clean.ts", "export const ok = 1\n")
	write2("node_modules/quiet.ts", "const x = 'Always Allow'\n")
	cleanOffenders, err := scanForRetiredCardPhrases(cleanRoot, func(_ string, name string) bool {
		return strings.HasSuffix(name, ".ts")
	})
	if err != nil {
		t.Fatalf("instrument: the clean scan failed: %v", err)
	}
	if len(cleanOffenders) != 0 {
		t.Fatalf("instrument: the clean tree was reported as offending: %v", cleanOffenders)
	}
}
