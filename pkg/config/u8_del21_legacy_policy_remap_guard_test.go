// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// u8_del21_legacy_policy_remap_guard_test.go — deletion guard for session-core
// U8's DEL-21 (PLAN.md row U8; docs/internal/specs/session-core-spec.md, DEL-21).
//
// DEL-21 deletes `pkg/config/validate.go::MigrateLegacyToolPolicyKeys`,
// `migrateLegacyToolPolicyMap`, the old tool-key rename entries feeding
// switch_agent (`hand_off` / `return_to_default` → `switch_agent`), and the
// loader wiring for those retired aliases. Retired saved settings become
// inert/ignored — no rewrite, conversion, merge or backfill (ADR-077 D2).
//
// KEEP (the guard asserts these survive, so the deletion cannot over-reach):
// `ReconcileToolPolicyCeiling` and `ValidateToolPolicyCoverage` — the two-layer
// ceiling reconcile is the default, and deleting the legacy remap must not
// touch it.
//
// The scan covers every non-test Go file under pkg/ because DEL-21 also names
// "loader wiring" — a call to the migration on any boot/reload path keeps the
// retirement incomplete even if the definition moves.
package config

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

// u8Del21RetiredIdents are DEL-21's named symbols: the migration entry point,
// its generic worker, and the alias table it reads.
var u8Del21RetiredIdents = map[string]string{
	"MigrateLegacyToolPolicyKeys":   "the legacy tool-policy key migration entry point",
	"migrateLegacyToolPolicyMap":    "the generic legacy tool-policy key fold",
	"legacyToolPolicyKeyMigrations": "the retired tool-key rename table (hand_off/return_to_default → switch_agent)",
}

// u8Del21RetiredLiterals are the retired alias KEYS DEL-21 folds into
// switch_agent. DEL-21 names exactly these two; the `load_tool → ToolSearch`
// rename in the same table is a different retired key that DEL-21 does not
// name, so it is deliberately NOT listed here.
var u8Del21RetiredLiterals = []string{"hand_off", "return_to_default"}

// u8Del21KeptIdents are the KEEP side of the deletion: the ceiling reconcile and
// its tripwire validator must survive, or the delete removed a live rule.
var u8Del21KeptIdents = []string{"ReconcileToolPolicyCeiling", "ValidateToolPolicyCoverage"}

// TestU8Del21_LegacyToolPolicyRemapRemoved is the guard.
func TestU8Del21_LegacyToolPolicyRemapRemoved(t *testing.T) {
	idents, literals, files := u8Del21ScanPkg(t)
	if files == 0 {
		t.Fatal("the scanner parsed NO Go files under pkg/ — it has stopped working, and a guard " +
			"that guards nothing passes forever")
	}

	for ident, why := range u8Del21RetiredIdents {
		for _, where := range u8Del21Sorted(idents[ident]) {
			t.Errorf("DEL-21 NOT DELETED: %s still declares/uses `%s` (%s).\n"+
				"DEL-21 deletes the legacy tool-policy key remap and its loader wiring. Retired saved "+
				"settings must be inert/ignored with no rewrite, conversion, merge or backfill. Resolve by "+
				"KEEPING the deletion — do not re-add the symbol as a compatibility shim.",
				where, ident, why)
		}
	}
	for _, lit := range u8Del21RetiredLiterals {
		for _, where := range u8Del21Sorted(literals[lit]) {
			t.Errorf("DEL-21 NOT DELETED: %s still references the retired tool-policy key %q.\n"+
				"DEL-21 (with DEL-07) deletes the hand_off/return_to_default → switch_agent rename table "+
				"and its loader wiring. No legacy key remap survives; retired saved settings are inert.",
				where, lit)
		}
	}

	// KEEP side: a delete that also removes the reconcile is worse than the remap.
	for _, ident := range u8Del21KeptIdents {
		if len(idents[ident]) == 0 {
			t.Errorf("DEL-21 OVER-DELETED: `%s` is gone from pkg/. DEL-21 keeps Reconcile/current "+
				"ceiling+tightening; deleting the legacy remap must not remove the two-layer tool-policy "+
				"ceiling or its coverage validator.", ident)
		}
	}

	// The existing validate_test.go cases that assert the migration WORKS are
	// compatibility-only and go with the feature. They are _test.go files, so
	// this scanner does not see them — leaving one behind would keep CI red
	// after the production delete, not keep this guard red.
	t.Log("DEL-21 boundary checked: retired remap must be absent, Reconcile/Validate must remain.")
}

// u8Del21ScanPkg parses every non-test Go file under pkg/ and returns the
// identifier -> files map, the string-literal value -> files map, and how many
// files it parsed.
func u8Del21ScanPkg(t *testing.T) (idents, literals map[string][]string, files int) {
	t.Helper()
	root, err := filepath.Abs("..") // pkg/
	if err != nil {
		t.Fatalf("resolve pkg/ root: %v", err)
	}
	idents = map[string][]string{}
	literals = map[string][]string{}
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
						literals[v] = append(literals[v], where)
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
	return idents, literals, files
}

func u8Del21Sorted(in []string) []string {
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
