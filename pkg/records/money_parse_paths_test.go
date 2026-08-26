// Omnipus — the sweep's own guard: no money parse path may go uncovered.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMoney_NoParsePathEscapesTheSweep is the guard on moneyForms.
//
// WHY IT EXISTS. moneyForms is the table three bound-sweeps drive, and it used
// to carry a comment admitting it could not do its job: "add a parse form
// without adding it here and the sweep still passes, so the list is the thing
// to keep honest". It was not kept honest. The list held three entries while
// parseMoneyValue had four paths, and the uncovered one — {amount, currency,
// scale} — is precisely where a mistyped `scal:` key was silently dropped,
// turning 349.98 SGD into 34998 SGD with no finding.
//
// A hand-maintained list of paths is the same defect one level up, so the list
// is no longer the thing being trusted. This test reads value.go and counts
// every place that CONSTRUCTS an accepted value — a parse path's only possible
// ending. A new path has to build a value somewhere, that changes this count,
// and the count is asserted. The honour system is replaced by arithmetic.
//
// WHAT IT DOES NOT CLAIM. It cannot prove a path is *meaningfully* exercised —
// only that no accepted-value construction site exists that nobody declared.
// The scale, exponent and minor-units invariants are what the sweeps assert;
// this only guarantees the sweeps are asked about every form.
func TestMoney_NoParsePathEscapesTheSweep(t *testing.T) {
	sites := acceptedValueSites(t, "value.go")

	// Every function in value.go that can hand back an accepted TypedValue,
	// with how many places inside it do so. Two money entries are expected in
	// parseMoneyMapping because the inferred-scale and declared-scale branches
	// end separately; parseMoneyScalar has one ending that serves both lexical
	// orders ("349.98 SGD" and "SGD 349.98").
	want := map[string]int{
		"parseTextValue":     1,
		"parseEnumValueNode": 1,
		"parseLinkValue":     1,
		"parseDateValue":     1,
		"parseNumberValue":   1,
		"parseMoneyScalar":   1,
		"parseMoneyMapping":  2,
	}

	for fn, n := range sites {
		if want[fn] != n {
			t.Fatalf("value.go: %s now builds an accepted value in %d place(s), not the %d this test recorded.\n"+
				"If that new ending accepts a MONEY value, add its form to moneyForms in money_test.go — otherwise the scale bound, the exponent refusal and the minor-units invariant are never asked about it, which is the exact gap that let a mistyped `scal:` key through. Then update this map.",
				fn, n, want[fn])
		}
	}
	for fn, n := range want {
		if sites[fn] != n {
			t.Fatalf("value.go: %s built an accepted value in %d place(s) and now builds %d. If a parse path was removed, remove its form from moneyForms and this map together — a sweep over a form that no longer exists is coverage of nothing.",
				fn, n, sites[fn])
		}
	}

	// The forms table and the money endings are related but not equal, and the
	// relationship is the thing worth stating: the inline scalar ending accepts
	// two lexical orders through one `return`, so the table carries one more
	// entry than value.go has money endings.
	const inlineOrdersSharingOneEnding = 1
	moneyEndings := want["parseMoneyScalar"] + want["parseMoneyMapping"]
	if got, expect := len(moneyForms), moneyEndings+inlineOrdersSharingOneEnding; got != expect {
		t.Fatalf("moneyForms enumerates %d forms but value.go has %d money endings (+%d for the currency-first order sharing the inline ending) = %d.\n"+
			"The bound sweeps only cover what this table lists, so a missing entry is an unbounded path, not a tidy-up.",
			got, moneyEndings, inlineOrdersSharingOneEnding, expect)
	}
}

// acceptedValueSites counts, per enclosing function, the composite literals
// that build a NON-ZERO TypedValue in the named source file.
//
// A zero `TypedValue{}` is what every rejection returns alongside its error, so
// it is deliberately not counted: only a literal carrying fields is a value
// being handed back as accepted.
func acceptedValueSites(t *testing.T, filename string) map[string]int {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("reading %s to count its parse paths: %v", filename, err)
	}

	sites := map[string]int{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || len(lit.Elts) == 0 {
				return true
			}
			if ident, ok := lit.Type.(*ast.Ident); ok && ident.Name == "TypedValue" {
				sites[fn.Name.Name]++
			}
			return true
		})
	}
	if len(sites) == 0 {
		t.Fatalf("%s: found no accepted-value construction at all — this guard is measuring nothing, which is worse than not existing", filename)
	}
	return sites
}
