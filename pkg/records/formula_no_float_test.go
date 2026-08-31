// Omnipus — FR-013/FR-020b/R-15: no binary floating point in the FORMULA path.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS WHEN decimal_no_float_test.go ALREADY WALKS THE PACKAGE
//
// It is not a duplicate, and the difference is the point.
// TestDecimal_NoBinaryFPTypesInThePackage walks EVERY .go file in this
// directory, which today covers formula_rat.go and formula_eval.go for free.
// That coverage is INCIDENTAL: it holds only while the walk stays package-wide,
// and the walk has an `allowedFiles` escape hatch whose whole purpose is to let
// somebody argue a file out of it.
//
// FR-144 makes a specific promise about a specific path — "internal arithmetic
// runs over exact rationals; no binary float exists anywhere in evaluation" —
// and a promise about a named path deserves an assertion that NAMES the path.
// So this test does two things the package-wide one cannot:
//
//	1. It asserts the formula files are ACTUALLY THERE and scanned. A guard
//	   that silently scanned zero formula files would pass forever; the
//	   file-count assertion is what makes the null result trustworthy.
//	2. It refuses the escape hatch for these files specifically. A float
//	   admitted into decimal_no_float_test.go's allowedFiles would still fail
//	   here, and the failure would name FR-144.
//
// It deliberately REUSES scanForBinaryFP rather than copying the rules — a
// self-test with its own copy of the rules proves only that the copy works,
// which is decimal_no_float_test.go's own stated reasoning applied one file
// over.
// ---------------------------------------------------------------------------

// TestFormula_NoBinaryFloatOnTheArithmeticPath is FR-144 / R-15 enforced
// mechanically over the formula layer's own files.
func TestFormula_NoBinaryFloatOnTheArithmeticPath(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	fset := token.NewFileSet()
	var offences []string
	var scanned []string

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "formula") || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			// Test files are scanned by the package-wide guard. Excluding them
			// here keeps THIS assertion about the production path, which is
			// what FR-144's promise is about.
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		scanned = append(scanned, name)
		offences = append(offences, scanForBinaryFP(fset, name, file)...)
	}

	// THE INSTRUMENT CHECK. Without this the test passes trivially the day the
	// files are renamed, moved, or the prefix changes — a green result meaning
	// "nothing was examined" rather than "nothing was found".
	//
	// The seven files are named explicitly rather than counted, so a file that
	// disappears is reported BY NAME.
	wantFiles := []string{
		"formula_ast.go",   // the tree
		"formula_lex.go",   // the tokenizer, where a numeric literal enters
		"formula_parse.go", // the parser
		"formula_type.go",  // static typing, which reads literal scales
		"formula_set.go",   // caps and cycles
		"formula_rat.go",   // THE arithmetic: exact rationals and the boundary
		"formula_eval.go",  // evaluation
	}
	have := map[string]bool{}
	for _, f := range scanned {
		have[f] = true
	}
	for _, want := range wantFiles {
		if !have[want] {
			t.Fatalf("the guard did not scan %s — it is not checking the formula arithmetic path. Scanned: %v", want, scanned)
		}
	}

	if len(offences) > 0 {
		t.Fatalf("FR-144 / R-15: the formula path must run over EXACT RATIONALS with no binary floating point, but %d offence(s) were found across %d files:\n  %s",
			len(offences), len(scanned), strings.Join(offences, "\n  "))
	}
}

// TestFormula_NoFloatGuardActuallyDetects proves this guard can fail, using the
// two shapes a float would most plausibly take in THIS layer.
//
// Both are real temptations rather than invented ones: `Rat.FloatString(n)` is a
// one-line replacement for the whole of ratToDecimal, and `strconv.ParseFloat`
// is the obvious way to turn a numeric literal's token text into a number.
func TestFormula_NoFloatGuardActuallyDetects(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "Rat.FloatString — the one-line replacement for the rounding boundary",
			src: `package records

import "math/big"

func render(r *big.Rat) string { return r.FloatString(10) }
`,
			want: 1,
		},
		{
			name: "strconv.ParseFloat on a literal's token text",
			src: `package records

import "strconv"

func literal(text string) interface{} { v, _ := strconv.ParseFloat(text, 64); return v }
`,
			want: 1,
		},
		{
			name: "the control: exact rationals, with floats named only in prose",
			src: `package records

import "math/big"

// FR-144 forbids float64 and big.Float here; this comment must not trip it.
func exact(a, b *big.Rat) *big.Rat { return new(big.Rat).Quo(a, b) }
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
