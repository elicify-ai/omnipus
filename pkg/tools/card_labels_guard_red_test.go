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

func TestCardLabels_RetiredAlwaysAllowWording(t *testing.T) {
	// The test binary runs with cwd = this package's directory.
	srcRoot := filepath.Join("..", "..", "src")
	pkgRoot := filepath.Join("..", "..", "pkg")
	for _, root := range []string{srcRoot, pkgRoot} {
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			t.Fatalf("BLOCKED: scan root %s is missing — the guard refuses to green on an absent tree", root)
		}
	}

	retired := []string{"Always Allow", "Always allow", "Allow everywhere"}
	var offenders []string

	scan := func(root string, match func(path string, name string) bool) {
		t.Helper()
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", "dist", ".git", "spa", "generated", "tests", "__tests__", "testdata":
					return filepath.SkipAll
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
				for _, phrase := range retired {
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
		if err != nil {
			t.Fatalf("BLOCKED: scanning %s failed: %v", root, err)
		}
	}

	isTestSource := func(name string) bool {
		return strings.HasSuffix(name, "_test.go") ||
			strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".test.tsx") ||
			strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".spec.tsx")
	}

	scan(srcRoot, func(path, name string) bool {
		return (strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")) && !isTestSource(name)
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
