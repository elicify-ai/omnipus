// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Office-document preview — builtin catalog registration and reachability
// (office-preview spec AD-13, §11; TDD row T-20 catalog leg).
//
// Oracles from the spec:
//   - §11 Registration: "grep -rl '\"preview_document\"' pkg/coreagent/
//     pkg/config/ pkg/tools/  >= 3 files" — the tool must be registered in
//     the central builtin catalog (pkg/tools/general_builtin_catalog.go)
//     and referenced in the policy/seed sources, or nobody can call it
//     whatever the tests say (root CLAUDE.md, Definition of Done).
//   - AD-13: registered at the read_file site and in
//     general_builtin_catalog.go.
//
// RED reason on the pre-change code: GeneralBuiltinMetadata() has no
// preview_document entry, and zero non-test files in the three trees carry
// the name (grep verified before writing this test).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const officePreviewToolName = "preview_document"

// TestOfficePreview_MetadataCatalogRegistersPreviewDocument: the central
// catalog capability reference must include the tool (metadata instance
// only — never executed here, per the catalog's own design invariants).
func TestOfficePreview_MetadataCatalogRegistersPreviewDocument(t *testing.T) {
	for _, tool := range GeneralBuiltinMetadata() {
		if tool.Name() == officePreviewToolName {
			// Metadata contract: the three getters must answer without
			// panicking on the dummy-wire instance.
			_ = tool.Description()
			_ = tool.Category()
			return
		}
	}
	t.Fatalf("BLOCKED: %q absent from GeneralBuiltinMetadata() — required by office-preview spec AD-13/§11 (T-20); nobody can see or govern the tool", officePreviewToolName)
}

// TestOfficePreview_ReachabilityThreeTreesReferenceTool (§11): at least
// three non-test files across pkg/coreagent/, pkg/config/ and pkg/tools/
// name the tool. This is the spec's own reachability grep, encoded as a
// test so the cut cannot silently regress it.
func TestOfficePreview_ReachabilityThreeTreesReferenceTool(t *testing.T) {
	// go test runs with the package dir as CWD (pkg/tools); the three trees
	// are pkg/coreagent, pkg/config and pkg/tools under the repo root.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root failed: %v", err)
	}
	trees := []string{
		filepath.Join(repoRoot, "pkg", "coreagent"),
		filepath.Join(repoRoot, "pkg", "config"),
		filepath.Join(repoRoot, "pkg", "tools"),
	}
	hits := map[string]int{}
	for _, tree := range trees {
		err := filepath.WalkDir(tree, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "spa" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), "\""+officePreviewToolName+"\"") {
				hits[tree]++
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s failed: %v", tree, err)
		}
	}
	total := 0
	for tree, n := range hits {
		if n > 0 {
			t.Logf("reachability: %s: %d file(s) reference %q", tree, n, officePreviewToolName)
		}
		total += n
	}
	if total < 3 {
		t.Fatalf("BLOCKED: only %d non-test file(s) reference %q across pkg/coreagent, pkg/config, pkg/tools (hits=%v); spec §11 requires >= 3 — the tool is not reachable", total, officePreviewToolName, hits)
	}
}
