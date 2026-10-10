// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// u8_del07_switch_agent_guard_test.go — deletion guard for session-core U8's
// DEL-07 (PLAN.md row U8; docs/internal/specs/session-core-spec.md, DEL-07).
//
// DEL-07 deletes the switch_agent/handover surface: the implementation, its
// catalog entry, its prompt/tool-exclusion entries,
// `pkg/session/unified_write.go::SwitchAgent`, and the routing handoff pins
// that feed it. Direct MAIN-peer messaging and session navigation replace it —
// the slash navigation command `/switch-agent` is a different string and is NOT
// in scope (DEL-07 says so explicitly), which is why this guard matches
// `switch_agent` with an underscore and never the hyphenated command.
//
// The guard scans every non-test Go file under pkg/ (the same root the sibling
// deletion guards use) and fails while any of the named symbols is still
// declared or still named as a tool. It is deliberately a scan rather than a
// compile-time assertion: a compile-time assertion would have to name the
// symbol, so it would pass by not compiling once the deletion landed — the
// deletion it is supposed to prove would break it.
package session

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// u8Del07RetiredIdents are the identifiers DEL-07 deletes:
//
//   - SwitchAgent          — pkg/session/unified_write.go's store method, and the
//     SessionStore method the tool interface declared.
//   - SwitchAgentTool      — pkg/tools/handoff.go's tool implementation.
//   - NewSwitchAgentTool   — its constructor (pkg/tools/general_builtin_catalog.go).
//   - SwitchAgentDefaultTarget — the tool's reserved `target` value.
//   - ExcludedSwitchAgent  — the catalog's child-exclusion entry.
var u8Del07RetiredIdents = map[string]string{
	"SwitchAgent":              "the session store's agent-switch method (pkg/session/unified_write.go::SwitchAgent)",
	"SwitchAgentTool":          "the switch_agent tool implementation (pkg/tools/handoff.go::SwitchAgentTool)",
	"NewSwitchAgentTool":       "the switch_agent tool constructor (pkg/tools/general_builtin_catalog.go)",
	"SwitchAgentDefaultTarget": "switch_agent's reserved default-target value",
	"ExcludedSwitchAgent":      "the catalog's switch_agent child-exclusion entry (pkg/tools/registry.go)",
}

// u8Del07RetiredNames are the retired wire/prompt names DEL-07 deletes: the
// tool's own name wherever a catalog, manifest, prompt or exclusion list still
// holds it.
var u8Del07RetiredNames = map[string]string{
	"switch_agent": "the retired tool name (catalog/manifest/prompt/exclusion entry)",
}

// TestU8Del07_SwitchAgentSurfaceRemoved is the guard. It fails while any
// switch_agent implementation, catalog entry or prompt entry survives under
// pkg/.
func TestU8Del07_SwitchAgentSurfaceRemoved(t *testing.T) {
	idents, names, files := u8ScanPkg(t)

	if files == 0 {
		t.Fatal("the scanner parsed NO Go files under pkg/ — it has stopped working, and a guard " +
			"that guards nothing passes forever")
	}

	offenders := 0
	for ident, why := range u8Del07RetiredIdents {
		for _, where := range sortedStrings(idents[ident]) {
			t.Errorf("DEL-07 NOT DELETED: %s still declares `%s` (%s).\n"+
				"DEL-07 deletes the switch_agent/handover surface outright — implementation, catalog and "+
				"prompt/tool-exclusion entries — replaced by direct MAIN-peer messaging and session "+
				"navigation. Resolve by KEEPING the deletion, never by re-adding the symbol.",
				where, ident, why)
			offenders++
		}
	}
	for name, why := range u8Del07RetiredNames {
		for _, where := range sortedStrings(names[name]) {
			t.Errorf("DEL-07 NOT DELETED: %s still names %q (%s).\n"+
				"DEL-07 deletes the switch_agent callable and its references. If this is the slash "+
				"navigation command, note it is `/switch-agent` (hyphen) and out of scope — an underscore "+
				"`switch_agent` here is a leftover tool/catalog/prompt entry.",
				where, name, why)
			offenders++
		}
	}
	if offenders == 0 {
		t.Log("DEL-07 clean: no switch_agent implementation, catalog entry or prompt entry under pkg/")
	}
}

// u8ScanPkg parses every non-test Go file under pkg/ and returns
//
//   - idents: identifier name -> files that declare or mention it as an identifier,
//   - names:  string-literal value -> files that contain it as a literal,
//   - files:  how many files the scan parsed (0 means the scanner broke).
//
// Comments are not scanned: a comment mentioning a symbol would keep this guard
// red after the deletion landed, and a guard that fails on prose is a guard
// people learn to weaken.
func u8ScanPkg(t *testing.T) (idents, names map[string][]string, files int) {
	t.Helper()
	root, err := filepath.Abs("..") // pkg/
	if err != nil {
		t.Fatalf("resolve pkg/ root: %v", err)
	}
	idents = map[string][]string{}
	names = map[string][]string{}
	fset := token.NewFileSet()

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch filepath.Base(path) {
			case "generated", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rErr := filepath.Rel(filepath.Dir(root), path)
		if rErr != nil {
			return rErr
		}
		file, pErr := parser.ParseFile(fset, path, nil, 0)
		if pErr != nil {
			return pErr
		}
		files++
		where := filepath.ToSlash(rel)
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.Ident:
				idents[node.Name] = append(idents[node.Name], where)
			case *ast.BasicLit:
				if node.Kind == token.STRING {
					if v, uErr := strconv.Unquote(node.Value); uErr == nil {
						names[v] = append(names[v], where)
					}
				}
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk pkg/: %v", walkErr)
	}
	return idents, names, files
}

func sortedStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
