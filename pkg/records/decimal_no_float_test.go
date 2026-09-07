// Omnipus — FR-020b enforced mechanically: no binary floating point in this package.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS, AND WHY IT IS NAMED THIS
//
// doc.go tells anyone adding numeric code here that "the mechanical guard is
// decimal_no_float_test.go". For a while that file did not exist — the guard
// lived in decimal_test.go — which is worse than a stale reference: it asserted
// an enforcement a reader could not find and therefore could not check. The
// guard now lives where the documentation says it does.
//
// WHAT THE GUARD LOOKS AT, AND WHY IT IS NOT A grep
//
// A textual grep flags every COMMENT explaining why floats are banned —
// including the ones in doc.go and frontmatter.go that are the primary
// documentation of this rule. A guard that punishes you for documenting the
// rule gets deleted, and then nothing guards anything. So this walks the AST
// with comments switched off, and looks at three things:
//
//	1. IDENTIFIERS naming a binary float type: float32, float64, complex64,
//	   complex128 — the type, the conversion, the struct field, all the same.
//	2. IDENTIFIERS whose name CONTAINS "Float", plus Inf and NaN. That is the
//	   whole float-producing surface of the standard library in one rule:
//	   big.Float, big.NewFloat, big.ParseFloat, strconv.ParseFloat,
//	   strconv.FormatFloat, (*big.Int).Float64, SetFloat64, math.MaxFloat64,
//	   math.Inf, math.NaN.
//	3. Untyped FLOAT (and imaginary) LITERALS: `x := 349.98` has no banned
//	   identifier anywhere and is still a float64 the moment it is assigned to
//	   a variable.
//
// (2) and (3) are here because the first version of this guard checked (1)
// only, and so would have passed a package containing exactly
// `big.NewFloat(349.98)` — both halves invisible to it. That is not a
// hypothetical: TestDecimal_NoBinaryFPGuardActuallyDetects below feeds it that
// precise fragment and asserts it is caught.
//
// WHAT IT CANNOT SEE, stated so nobody over-trusts it: a float that arrives
// through `any` at runtime — yaml or json decoded into an interface — carries
// no float token in the source. That vector is closed by construction instead
// (frontmatter.go parses through yaml.Node and keeps every numeric value in its
// lexical form), and this guard is the second line, not the only one.
//
// A deliberate float — there is no legitimate use today — must be argued for by
// adding a file to allowedFiles below, which makes it a decision rather than an
// accident.
// ---------------------------------------------------------------------------

// bannedIdent reports whether an identifier names, produces or consumes a
// binary floating-point value.
func bannedIdent(name string) bool {
	switch name {
	case "float32", "float64", "complex64", "complex128", "Inf", "NaN":
		return true
	}
	// Catches big.Float, NewFloat, ParseFloat, FormatFloat, AppendFloat,
	// SetFloat64, Float64, Float32, MaxFloat64 and anything shaped like them.
	return strings.Contains(name, "Float")
}

// scanForBinaryFP returns one line per offence in a parsed file. The real guard
// and the guard's own self-test both call THIS function — a self-test with its
// own copy of the rules proves only that the copy works.
func scanForBinaryFP(fset *token.FileSet, name string, file *ast.File) []string {
	var offences []string
	at := func(n ast.Node) string {
		return fmt.Sprintf("%s:%d", name, fset.Position(n.Pos()).Line)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Ident:
			if bannedIdent(node.Name) {
				offences = append(offences, at(node)+": identifier "+node.Name)
			}
		case *ast.BasicLit:
			if node.Kind == token.FLOAT || node.Kind == token.IMAG {
				offences = append(offences, at(node)+": binary floating-point literal "+node.Value)
			}
		}
		return true
	})
	return offences
}

// TestDecimal_NoBinaryFPTypesInThePackage is FR-020b enforced MECHANICALLY.
//
// Every other test in this package proves the current code is exact. This one
// is the guard against the next change: a float reintroduced anywhere in the
// numeric path would be invisible to a reviewer reading a diff of one function,
// and its symptom (a cent adrift on a large total) surfaces months later in an
// operator's spreadsheet rather than in CI.
func TestDecimal_NoBinaryFPTypesInThePackage(t *testing.T) {
	// Empty on purpose. Adding an entry is the argument for a float.
	allowedFiles := map[string]bool{}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	fset := token.NewFileSet()
	var offences []string
	var scanned int

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || allowedFiles[name] {
			continue
		}
		// Parse WITHOUT comments so prose about floats is not an offence.
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		scanned++
		offences = append(offences, scanForBinaryFP(fset, name, file)...)
	}

	if scanned < 5 {
		// A guard that scanned nothing passes trivially. This package has well
		// over five Go files; if it does not, the walk is broken, not clean.
		t.Fatalf("the guard scanned only %d files — it is not actually checking the package", scanned)
	}
	// This file is scanned like every other, so it must itself stay free of
	// banned identifiers — the banned NAMES live here only as string literals.
	if len(offences) > 0 {
		t.Fatalf("FR-013 / FR-020b: integer and decimal must never touch binary floating point, but %d offence(s) were found across %d files:\n  %s",
			len(offences), scanned, strings.Join(offences, "\n  "))
	}
}

// TestDecimal_NoBinaryFPGuardActuallyDetects proves the guard above can fail.
//
// A guard nobody has seen fail is not evidence. Each fragment here is a way a
// float has actually reached a package like this one; the last is the control.
// The two middle cases are the ones the previous, identifier-only guard let
// through — they are why this test exists in this shape.
func TestDecimal_NoBinaryFPGuardActuallyDetects(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "a declared float type",
			src: `package records

// This comment mentions float64 and must NOT be what trips the guard.
func drifting(x float64) float64 { return x * 2 }
`,
			want: 2,
		},
		{
			name: "an UNTYPED literal, which names no banned type at all",
			src: `package records

func total() interface{} {
	x := 349.98
	return x
}
`,
			want: 1,
		},
		{
			name: "big.NewFloat, whose call names neither big nor Float exactly",
			src: `package records

import "math/big"

func drift(n *big.Int) interface{} { return new(big.Rat).SetInt(n) }

func worse() interface{} { return big.NewFloat(349.98) }
`,
			want: 2, // the NewFloat selector AND the 349.98 literal
		},
		{
			name: "strconv.ParseFloat",
			src: `package records

import "strconv"

func lossy(s string) interface{} { v, _ := strconv.ParseFloat(s, 64); return v }
`,
			want: 1,
		},
		{
			name: "the control: floats named only in prose and in strings",
			src: `package records

// This comment mentions float64, ParseFloat and big.Float. All prose.
const banned = "float64 ParseFloat big.Float 349.98"

func exact(x int64) int64 { return x * 2 }
`,
			want: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "fragment.go", c.src, 0)
			if err != nil {
				t.Fatalf("parsing fragment: %v", err)
			}
			got := scanForBinaryFP(fset, "fragment.go", file)
			if len(got) != c.want {
				t.Fatalf("the guard reported %d offence(s), want %d:\n  %s", len(got), c.want, strings.Join(got, "\n  "))
			}
		})
	}
}
