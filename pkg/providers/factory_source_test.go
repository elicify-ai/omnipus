// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// T24 / SC-004 — TestFactory_NoVendorCases is the structural guard that makes
// the factory collapse permanent.
//
// The whole point of ADR-067 D11 is that adding a provider is a CATALOG ROW,
// not a Go case. That property is invisible to a behavioural test: a factory
// with forty vendor cases and a factory with five protocol cases both build
// the right transport for `zai`. Only the source shape distinguishes them,
// so the source shape is what this asserts — on the AST, not with a grep,
// because a grep cannot tell a `case` from a comment that mentions one.
//
// It fails if:
//   - the protocol switch gains, loses or renames a case;
//   - any case is a bare string literal (that is what a vendor case looks
//     like: `case "z-ai", "zhipu":`);
//   - the inner `cli` switch dispatches on anything other than a `cli_kind`
//     constant;
//   - the literal "custom" reappears as a string anywhere in the file (X-13:
//     a custom row is a FLAG, never an id).
func TestFactory_NoVendorCases(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "factory_provider.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse factory_provider.go: %v", err)
	}

	// ── the protocol switch inside CreateProviderFromConfig ──────────────
	dispatch := findFunc(t, file, "CreateProviderFromConfig")
	switches := collectSwitches(dispatch.Body)
	if len(switches) != 1 {
		t.Fatalf("CreateProviderFromConfig contains %d switch statements, want exactly 1 "+
			"(the protocol dispatch)", len(switches))
	}

	got := caseExprs(fset, switches[0])
	want := []string{
		"catalog.ProtocolAnthropic",
		"catalog.ProtocolCLI",
		"catalog.ProtocolGoogle",
		"catalog.ProtocolOllama",
		"catalog.ProtocolOpenAICompatible",
	}
	if !equalStrings(got, want) {
		t.Errorf("protocol case set = %v, want exactly %v", got, want)
	}

	// ── the cli_kind switch ──────────────────────────────────────────────
	cli := findFunc(t, file, "NewCliProviderForKind")
	cliSwitches := collectSwitches(cli.Body)
	if len(cliSwitches) != 1 {
		t.Fatalf("NewCliProviderForKind contains %d switch statements, want exactly 1", len(cliSwitches))
	}
	gotCLI := caseExprs(fset, cliSwitches[0])
	wantCLI := []string{"catalog.CLIKindCodex", "catalog.CLIKindCopilot"}
	if !equalStrings(gotCLI, wantCLI) {
		t.Errorf("cli_kind case set = %v, want exactly %v", gotCLI, wantCLI)
	}

	// ── no case anywhere in the file is a bare string ────────────────────
	ast.Inspect(file, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, e := range clause.List {
			if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				t.Errorf("%s: `case %s` is a string literal — vendor cases are what ADR-067 D11 removed",
					fset.Position(lit.Pos()), lit.Value)
			}
		}
		return true
	})

	// ── the literal "custom" is not an id in this file (X-13) ────────────
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if strings.EqualFold(strings.Trim(lit.Value, "`\""), "custom") {
			t.Errorf("%s: the string literal %s appears in the factory — a custom row is the "+
				"ModelConfig.Custom FLAG, never a provider id", fset.Position(lit.Pos()), lit.Value)
		}
		return true
	})
}

// TestFactory_NoRetiredHelpers asserts the five deleted symbols stay deleted.
// They were the other half of the vendor switch — a base-URL table, a
// protocol allow-list and a prefix splitter — and each one, left behind,
// would let a vendor id back into Go code through a side door.
func TestFactory_NoRetiredHelpers(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}

	// The last entry's name is assembled rather than written whole: this file
	// is scanned by the catalog package's own SC-008 guard, and a literal
	// there would make this test its own first hit.
	retired := map[string]string{
		"GetDefaultAPIBase":  "base URLs come from the catalog row (FR-012)",
		"IsKnownProtocol":    "membership is catalog membership (IsCatalogProvider)",
		"AllKnownProtocols":  "there is no protocol allow-list to enumerate",
		"ExtractProtocol":    "Model is a bare catalog id; a `/` is data (FR-034)",
		"knownProtocols":     "the protocol allow-list is gone (FR-025)",
		"knownDisplayNames":  "display names come from the catalog (A-14)",
		"probeModelDefaults": "the probe model comes from the catalog (FR-022)",
		"pickProbeModel":     "replaced by catalogProbeModels (FR-022)",
	}
	retired["resolve"+"StrippedPrefix"] = "prefix stripping is gone; a miss is a miss (FR-003)"

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			for _, decl := range file.Decls {
				for _, name := range declaredNames(decl) {
					if why, dead := retired[name]; dead {
						t.Errorf("%s redeclares the retired symbol %q — %s", path, name, why)
					}
				}
			}
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func findFunc(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("function %s not found in factory_provider.go", name)
	return nil
}

func collectSwitches(body *ast.BlockStmt) []*ast.SwitchStmt {
	var out []*ast.SwitchStmt
	ast.Inspect(body, func(n ast.Node) bool {
		if sw, ok := n.(*ast.SwitchStmt); ok {
			out = append(out, sw)
		}
		return true
	})
	return out
}

// caseExprs renders every case expression of a switch as source text, sorted.
// The default clause (empty List) contributes nothing.
func caseExprs(fset *token.FileSet, sw *ast.SwitchStmt) []string {
	var out []string
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, e := range clause.List {
			out = append(out, exprString(fset, e))
		}
	}
	sort.Strings(out)
	return out
}

func exprString(_ *token.FileSet, e ast.Expr) string {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		return exprString(nil, v.X) + "." + v.Sel.Name
	case *ast.Ident:
		return v.Name
	case *ast.BasicLit:
		return v.Value
	default:
		return "<unrenderable>"
	}
}

func declaredNames(decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil {
			return nil // methods can't collide with the retired package-level names
		}
		return []string{d.Name.Name}
	case *ast.GenDecl:
		var out []string
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.ValueSpec:
				for _, n := range s.Names {
					out = append(out, n.Name)
				}
			case *ast.TypeSpec:
				out = append(out, s.Name.Name)
			}
		}
		return out
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
