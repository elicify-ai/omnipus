// Omnipus — one ordering authority for enums, enforced mechanically.
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
// WHY THIS FILE EXISTS
//
// An enum value's ordinal had two sources that agreed only by accident:
//
//	Property.EnumPosition  the index into the property's declared Values,
//	                       scanned from the slice when the O(1) cache was never
//	                       built. Explicitly documented as supporting a Property
//	                       assembled outside this package from a struct literal.
//	EnumValue.Position     a struct FIELD, stamped only by the schema loader and
//	                       NewProperty. Zero on every value of a struct-literal
//	                       Property.
//
// compare_oracle.go's R-5 branch read the FIELD, so on a struct-literal
// property every position read 0: `todo < done` was FALSE and `done <= todo`
// was TRUE, with nothing reported, while SortByEnumOrder — going through
// EnumPosition — sorted the same three values correctly. One property, two
// contradictory answers, no complaint.
//
// A comment saying "ask EnumPosition" cannot stop the next author reaching for
// the field, and neither can the tests: the defect survived a full suite
// because every enum fixture in it hand-filled Position to match the slice
// index, which is precisely the condition under which the two authorities
// agree. So the rule is enforced here instead.
//
// THE RULE
//
// In this package's NON-TEST source, `Position` may be WRITTEN and may not be
// READ. Writing it is the loader and NewProperty stamping derived output that
// the wire type (contracts/components/schemas/EnumValueDef.yaml) requires;
// reading it is a second ordering authority being born.
//
// WHY IT IS NOT A grep. A textual search for "Position" flags every comment
// that explains this rule — including the ones in schema.go and
// compare_oracle.go that are its primary documentation — and a guard that
// punishes you for documenting the rule gets deleted, after which nothing
// guards anything. This walks the AST with comments switched off and
// distinguishes the three syntactic positions the identifier can occupy.
//
// WHAT IT CANNOT SEE, stated so nobody over-trusts it: a read through
// reflection, or through a local variable copied from the field in one
// statement and used in another. The external test in
// external_enum_ordering_test.go is the behavioural line; this is the
// structural one, and neither is the only one.
//
// A deliberate read — there is no legitimate one today — must be argued for by
// adding its file to positionReadAllowedFiles, which makes it a decision rather
// than an accident.
// ---------------------------------------------------------------------------

// positionReadAllowedFiles names non-test files permitted to READ
// EnumValue.Position. Empty on purpose.
var positionReadAllowedFiles = map[string]bool{}

// scanForPositionReads returns one line per read of a `.Position` field.
//
// A selector `x.Position` occupies one of three syntactic positions, and only
// the third is a read:
//
//  1. a composite-literal KEY   — EnumValue{Name: n, Position: i}   (a write)
//  2. an assignment TARGET      — p.Values[i].Position = i          (a write)
//  3. anything else             — compareInt(a.Enum.Position, ...)  (a READ)
//
// The real guard and the guard's own self-test both call THIS function; a
// self-test carrying its own copy of the rules proves only that the copy works.
func scanForPositionReads(fset *token.FileSet, name string, file *ast.File) []string {
	// Collect the selector nodes that are NOT field reads, so the walk can skip
	// them: writes, and method calls that merely share the name (go/token's
	// fset.Position(pos) is the one that turns up in practice).
	writes := map[ast.Node]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Position" {
				writes[sel] = true
			}
		case *ast.KeyValueExpr:
			// `Position: i` inside a composite literal. The key is a bare Ident,
			// not a SelectorExpr, so it never reaches the read check below — but
			// record it for clarity and in case of `&EnumValue{Position: i}`.
			if id, ok := node.Key.(*ast.Ident); ok && id.Name == "Position" {
				writes[node.Key] = true
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Position" {
					writes[sel] = true
				}
			}
		case *ast.IncDecStmt:
			if sel, ok := node.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Position" {
				writes[sel] = true
			}
		}
		return true
	})

	var offences []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Position" || writes[sel] {
			return true
		}
		offences = append(offences, fmt.Sprintf("%s:%d: reads the .Position field; ask Property.EnumPosition for an enum ordinal",
			name, fset.Position(sel.Pos()).Line))
		return true
	})
	return offences
}

// TestEnumPosition_IsTheOnlyOrderingAuthority walks every non-test file in the
// package and fails on any read of the Position field.
func TestEnumPosition_IsTheOnlyOrderingAuthority(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	var offences []string
	scanned := 0

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if positionReadAllowedFiles[name] {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		scanned++
		offences = append(offences, scanForPositionReads(fset, name, file)...)
	}

	// A guard that scanned nothing passes for the wrong reason. compare_oracle.go
	// and schema.go are the two files this rule is about, so require both.
	if scanned < 2 {
		t.Fatalf("the guard scanned %d non-test files; it must scan the package's source or it is asserting nothing", scanned)
	}
	if len(offences) > 0 {
		t.Errorf("EnumValue.Position is derived OUTPUT, not the ordering authority — Property.EnumPosition is (see its doc comment).\n%s",
			strings.Join(offences, "\n"))
	}
}

// TestEnumPosition_GuardActuallyDetects feeds the guard the exact line that was
// wrong, plus the two writes that must stay legal. A guard nobody has seen fail
// is a guard nobody knows works.
func TestEnumPosition_GuardActuallyDetects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		src    string
		caught bool
	}{
		{
			name: "the read that was there",
			src: `package records
func f(op Operator, a, b TypedValue) bool {
	return orderingHolds(op, compareInt(a.Enum.Position, b.Enum.Position))
}`,
			caught: true,
		},
		{
			name: "a read hidden in a comparison",
			src: `package records
func f(a, b EnumValue) bool { return a.Position < b.Position }`,
			caught: true,
		},
		{
			name: "the loader's normalising write stays legal",
			src: `package records
func f(p *Property) {
	for i := range p.Values {
		p.Values[i].Position = i
	}
}`,
			caught: false,
		},
		{
			name: "a composite-literal write stays legal",
			src: `package records
func f(name string, position int) EnumValue {
	return EnumValue{Name: name, Position: position, Group: ""}
}`,
			caught: false,
		},
		{
			name: "a comment naming the field is not a read",
			src: `package records
// Position is the declared index; EnumPosition is the authority, not p.Position.
func f() {}`,
			caught: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "sample.go", tc.src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parsing the sample: %v", err)
			}
			offences := scanForPositionReads(fset, "sample.go", file)
			if tc.caught && len(offences) == 0 {
				t.Errorf("the guard missed a read it must catch:\n%s", tc.src)
			}
			if !tc.caught && len(offences) > 0 {
				t.Errorf("the guard flagged something legal (%v):\n%s", offences, tc.src)
			}
		})
	}
}
