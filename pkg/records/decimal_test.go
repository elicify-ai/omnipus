// Omnipus — tests for FR-013 and FR-020b: exact decimal, no binary floats.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"os"
	"strings"
	"testing"
)

// TestDecimal_ParsesAndRendersExactly checks the lexical round trip. The
// oracle is arithmetic, not the implementation: each expectation is what the
// decimal value IS, written out.
func TestDecimal_ParsesAndRendersExactly(t *testing.T) {
	cases := []struct {
		text  string
		want  string
		scale int32
	}{
		{"0", "0", 0},
		{"349.98", "349.98", 2},
		{"-349.98", "-349.98", 2},
		{"+7", "7", 0},
		{"0.10", "0.10", 2},
		{".5", "0.5", 1},
		{"1.005", "1.005", 3},
		{"9007199254740993", "9007199254740993", 0}, // 2^53 + 1 — DS-1
		{"0.000000000000000001", "0.000000000000000001", 18},
		{"1e3", "1000", -3},
		{"1.5e2", "150", -1},
		{"12345e-2", "123.45", 2},
	}
	for _, tc := range cases {
		d, err := ParseDecimal(tc.text)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", tc.text, err)
		}
		if got := d.String(); got != tc.want {
			t.Fatalf("ParseDecimal(%q).String() = %q, want %q", tc.text, got, tc.want)
		}
		if d.Scale() != tc.scale {
			t.Fatalf("ParseDecimal(%q).Scale() = %d, want %d", tc.text, d.Scale(), tc.scale)
		}
	}
}

// TestDecimal_HoldsValuesFloat64Cannot is FR-020b's positive case. Every value
// here is provably wrong in float64.
func TestDecimal_HoldsValuesFloat64Cannot(t *testing.T) {
	t.Run("2^53+1 survives", func(t *testing.T) {
		// float64(9007199254740993) == 9007199254740992. DS-1 requires exact.
		d, err := ParseDecimal("9007199254740993")
		if err != nil {
			t.Fatalf("%v", err)
		}
		want, _ := new(big.Int).SetString("9007199254740993", 10)
		if d.Unscaled().Cmp(want) != 0 {
			t.Fatalf("2^53+1 must survive exactly; got %s", d.Unscaled())
		}
		// And it must be distinguishable from 2^53, which float64 conflates it with.
		other, _ := ParseDecimal("9007199254740992")
		if d.Equal(other) {
			t.Fatalf("2^53 and 2^53+1 must not compare equal — that is precisely the float64 defect")
		}
	})

	t.Run("0.1 + 0.2 is exactly 0.3", func(t *testing.T) {
		a, _ := ParseDecimal("0.1")
		b, _ := ParseDecimal("0.2")
		sum, err := a.Add(b)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := sum.String(); got != "0.3" {
			t.Fatalf("FR-013: 0.1 + 0.2 must be exactly 0.3 (float64 gives 0.30000000000000004); got %s", got)
		}
		three, _ := ParseDecimal("0.3")
		if !sum.Equal(three) {
			t.Fatalf("0.1 + 0.2 must equal 0.3")
		}
	})

	t.Run("a 40-digit value is exact", func(t *testing.T) {
		text := "1234567890123456789012345678901234567890.12345"
		d, err := ParseDecimal(text)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := d.String(); got != text {
			t.Fatalf("want %s, got %s", text, got)
		}
	})

	t.Run("comparison is by value, not representation", func(t *testing.T) {
		a, _ := ParseDecimal("349.98")
		b, _ := ParseDecimal("349.9800")
		if !a.Equal(b) {
			t.Fatalf("349.98 and 349.9800 are the same number and must compare equal")
		}
		if a.Scale() == b.Scale() {
			t.Fatalf("the fixture is pointless unless the scales differ")
		}
	})
}

// TestDecimal_RejectsWhatIsNotANumber pins DS-1's `PLACEHOLDER — unknown` row
// and the near-misses that must not be quietly accepted.
func TestDecimal_RejectsWhatIsNotANumber(t *testing.T) {
	bad := []string{
		"", " ", ".", "-", "+",
		"PLACEHOLDER — unknown",
		"1.2.3",
		"NaN", "Inf", "-Inf", "inf",
		"0x1f",
		"1,000",
		"1_000",
		"349.98 SGD", // money has its own parser; a glued currency is ambiguous
		"12abc",
		"1e",
		"1e2.5",
	}
	for _, text := range bad {
		if d, err := ParseDecimal(text); err == nil {
			t.Fatalf("ParseDecimal(%q) must be rejected; it returned %s", text, d.String())
		}
	}
}

// TestDecimal_ZeroValueIsUsableAndNeverPanics guards §8 R-11's "total and never
// panics" at the numeric layer: a zero Decimal has a nil big.Int inside.
func TestDecimal_ZeroValueIsUsableAndNeverPanics(t *testing.T) {
	var zero Decimal
	if !zero.IsZero() || zero.Sign() != 0 {
		t.Fatalf("the zero value must behave as zero")
	}
	if got := zero.String(); got != "0" {
		t.Fatalf("want 0, got %q", got)
	}
	one, _ := ParseDecimal("1")
	sum, err := zero.Add(one)
	if err != nil || sum.String() != "1" {
		t.Fatalf("0 + 1 must be 1; got %s err=%v", sum.String(), err)
	}
	if zero.Cmp(one) >= 0 {
		t.Fatalf("0 < 1")
	}
	if zero.Unscaled().Sign() != 0 {
		t.Fatalf("Unscaled on a zero value must give 0, not panic")
	}
}

// TestDecimal_NoFloatTypesInThePackage is FR-020b enforced MECHANICALLY.
//
// Every other test in this package proves the current code is exact. This one
// is the guard against the next change: `float64` reintroduced anywhere in the
// numeric path would be invisible to a reviewer reading a diff of one function,
// and its symptom (a cent adrift on a large total) surfaces months later in an
// operator's spreadsheet rather than in CI.
//
// It walks the AST rather than grepping the text, and the difference is not
// cosmetic. A textual grep flags every COMMENT that explains why floats are
// banned — including the ones in doc.go and frontmatter.go that are the primary
// documentation of this rule. A guard that punishes you for documenting it gets
// deleted, and then nothing guards anything. The AST sees identifiers only.
//
// A deliberate float — there is no legitimate use today — must be argued for by
// editing this test's allowlist, which makes it a decision rather than an
// accident.
func TestDecimal_NoFloatTypesInThePackage(t *testing.T) {
	// The banned identifiers: Go's float types and the stdlib calls that
	// produce or consume them.
	banned := map[string]bool{
		"float32": true, "float64": true,
		"ParseFloat": true, "FormatFloat": true, "AppendFloat": true,
		"Float": true, // big.Float, and any Float() accessor that yields one
	}

	// The allowlist is empty on purpose. Adding an entry is the argument.
	allowed := map[string]bool{}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	fset := token.NewFileSet()
	var offences []string
	var scanned int

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || allowed[name] {
			continue
		}
		// Parse WITHOUT comments so prose about floats is not an offence.
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		scanned++
		ast.Inspect(file, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || !banned[id.Name] {
				return true
			}
			offences = append(offences, name+":"+itoa(fset.Position(id.Pos()).Line)+": "+id.Name)
			return true
		})
	}

	if scanned < 5 {
		// A guard that scanned nothing passes trivially. This package has well
		// over five Go files; if it does not, the walk is broken, not clean.
		t.Fatalf("the guard scanned only %d files — it is not actually checking the package", scanned)
	}
	if len(offences) > 0 {
		t.Fatalf("FR-013 / FR-020b: money and number must never touch binary floating point, but %d identifier(s) name a float type:\n  %s",
			len(offences), strings.Join(offences, "\n  "))
	}
}

// TestDecimal_NoFloatGuardActuallyDetects proves the guard above can fail.
//
// A guard nobody has seen fail is not evidence. This compiles a fragment that
// uses float64 and asserts the same AST walk flags it — so the clean result
// from the real package means "no floats", not "the walk is broken".
func TestDecimal_NoFloatGuardActuallyDetects(t *testing.T) {
	const withFloat = `package records

// This comment mentions float64 and must NOT be what trips the guard.
func drifting(x float64) float64 { return x * 2 }
`
	const withoutFloat = `package records

// This comment mentions float64, ParseFloat and big.Float. All prose.
func exact(x int64) int64 { return x * 2 }
`
	banned := map[string]bool{"float32": true, "float64": true, "ParseFloat": true, "FormatFloat": true, "AppendFloat": true, "Float": true}

	count := func(src string) int {
		t.Helper()
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "fragment.go", src, 0)
		if err != nil {
			t.Fatalf("parsing fragment: %v", err)
		}
		n := 0
		ast.Inspect(file, func(node ast.Node) bool {
			if id, ok := node.(*ast.Ident); ok && banned[id.Name] {
				n++
			}
			return true
		})
		return n
	}

	if got := count(withFloat); got != 2 {
		t.Fatalf("the guard must flag both float64 identifiers in the fragment; it flagged %d", got)
	}
	if got := count(withoutFloat); got != 0 {
		t.Fatalf("the guard must ignore floats named only in COMMENTS; it flagged %d", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
