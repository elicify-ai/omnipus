// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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

	// ── no if/else-if ladder emulates the banned vendor switch (O17) ─────
	// collectSwitches above only ever finds *ast.SwitchStmt nodes, so a
	// `case "z-ai", "zhipu":`-shaped vendor dispatch spelled as
	// `if cfg.Provider == "z-ai" { ... } else if cfg.Provider == "zhipu"
	// { ... }` changes neither the switch count nor its case set and would
	// pass untouched. scanVendorLadder closes that gap.
	for _, v := range scanVendorLadder(fset, file) {
		t.Errorf("%s: an if/else-if ladder compares to a string literal — a vendor case in `if` "+
			"clothing; ADR-067 D11 removed vendor branching, not just vendor `switch` cases", v.pos)
	}
}

// vendorLadderViolation is one if/else-if chain scanVendorLadder judged to
// be a vendor switch in disguise (O17).
type vendorLadderViolation struct {
	pos string
}

// scanVendorLadder finds every if/else-if ladder (2+ branches linked by
// `else if`) in file where at least one branch's condition contains a
// `==`/`!=` comparison against a string literal — the if-statement
// equivalent of `switch id { case "z-ai": ...; case "zhipu": ... }`.
//
// A LONE `if` (Else == nil, or a single trailing plain `else` block with no
// further `else if`) is deliberately NOT flagged: factory_provider.go has
// exactly one such case today — `if cfg.Provider == "openai-chatgpt" { ...
// }` inside CreateProviderFromConfig, a reviewed, single-branch OAuth
// special case (ADR-068 §8b) with no case-like, multi-branch structure to
// it. Only a genuine ladder — two or more branches chained by `else if` —
// resembles a vendor switch; that is the shape O17 named.
func scanVendorLadder(fset *token.FileSet, file *ast.File) []vendorLadderViolation {
	var out []vendorLadderViolation
	consumed := map[*ast.IfStmt]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok || consumed[ifStmt] {
			return true
		}
		chain := []*ast.IfStmt{ifStmt}
		cur := ifStmt
		for cur.Else != nil {
			next, ok := cur.Else.(*ast.IfStmt)
			if !ok {
				break
			}
			chain = append(chain, next)
			cur = next
		}
		for _, link := range chain {
			consumed[link] = true
		}
		if len(chain) < 2 {
			return true
		}
		for _, link := range chain {
			if exprHasStringCompare(link.Cond) {
				out = append(out, vendorLadderViolation{pos: fset.Position(ifStmt.Pos()).String()})
				break
			}
		}
		return true
	})
	return out
}

// exprHasStringCompare reports whether e contains a `==`/`!=` comparison
// against a string literal anywhere within it, so a combined condition like
// `cfg.Provider == "z-ai" || cfg.Provider == "zhipu"` inside one `if` also
// counts as vendor-shaped.
func exprHasStringCompare(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || (be.Op != token.EQL && be.Op != token.NEQ) {
			return true
		}
		if isStringBasicLit(be.X) || isStringBasicLit(be.Y) {
			found = true
		}
		return true
	})
	return found
}

func isStringBasicLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// TestFactory_NoVendorCases_CatchesIfElseLadder pins the O17 fix in
// isolation: scanVendorLadder is exercised directly against synthetic
// sources (parser.ParseFile accepts source text with no file on disk, so no
// fixture directory is needed), independent of whether real code today
// happens to contain a violation.
func TestFactory_NoVendorCases_CatchesIfElseLadder(t *testing.T) {
	parse := func(t *testing.T, src string) (*token.FileSet, *ast.File) {
		t.Helper()
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "fixture.go", src, 0)
		if err != nil {
			t.Fatalf("parse fixture: %v", err)
		}
		return fset, file
	}

	t.Run("an if/else-if ladder on a vendor id is caught", func(t *testing.T) {
		fset, file := parse(t, `package providers

func dispatch(cfg *config.ModelConfig) int {
	if cfg.Provider == "z-ai" {
		return 1
	} else if cfg.Provider == "zhipu" {
		return 2
	} else {
		return 0
	}
}
`)
		violations := scanVendorLadder(fset, file)
		if len(violations) != 1 {
			t.Fatalf("want exactly 1 violation, got %d: %+v", len(violations), violations)
		}
	})

	t.Run("a combined-condition ladder (||) is also caught", func(t *testing.T) {
		fset, file := parse(t, `package providers

func dispatch(cfg *config.ModelConfig) int {
	if cfg.Provider == "z-ai" || cfg.Provider == "zhipu" {
		return 1
	} else if cfg.Provider == "anthropic" {
		return 2
	}
	return 0
}
`)
		if len(scanVendorLadder(fset, file)) != 1 {
			t.Fatalf("combined-condition ladder must be caught: %+v", scanVendorLadder(fset, file))
		}
	})

	t.Run("no false positive: a lone if with no else-if chain", func(t *testing.T) {
		fset, file := parse(t, `package providers

func dispatch(cfg *config.ModelConfig) {
	if cfg.Provider == "openai-chatgpt" {
		return
	}
}
`)
		if v := scanVendorLadder(fset, file); len(v) != 0 {
			t.Errorf("a lone if (no else-if chain) must not be flagged as a ladder: %+v", v)
		}
	})

	t.Run("no false positive: an if/else-if ladder with no string comparison", func(t *testing.T) {
		fset, file := parse(t, `package providers

func dispatch(n int) int {
	if n == 1 {
		return 1
	} else if n == 2 {
		return 2
	}
	return 0
}
`)
		if v := scanVendorLadder(fset, file); len(v) != 0 {
			t.Errorf("an int-comparison ladder must not be flagged: %+v", v)
		}
	})

	t.Run("no false positive: a plain if/else with a single condition (not a ladder)", func(t *testing.T) {
		fset, file := parse(t, `package providers

func dispatch(cfg *config.ModelConfig) int {
	if cfg.Provider == "openai-chatgpt" {
		return 1
	} else {
		return 0
	}
}
`)
		if v := scanVendorLadder(fset, file); len(v) != 0 {
			t.Errorf("a single-condition if/else (no else-if) must not be flagged: %+v", v)
		}
	})

	t.Run("real factory_provider.go has no such ladder", func(t *testing.T) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "factory_provider.go", nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse factory_provider.go: %v", err)
		}
		if v := scanVendorLadder(fset, file); len(v) != 0 {
			t.Errorf("factory_provider.go must have zero vendor-ladder violations: %+v", v)
		}
	})
}

// TestFactory_NoRetiredHelpers asserts the five deleted symbols stay deleted.
// They were the other half of the vendor switch — a base-URL table, a
// protocol allow-list and a prefix splitter — and each one, left behind,
// would let a vendor id back into Go code through a side door.
func TestFactory_NoRetiredHelpers(t *testing.T) {
	fset := token.NewFileSet()

	// parser.ParseDir is deprecated (does not consider build tags when
	// grouping files into packages) — but this test never used the package
	// grouping it produced anyway, only the flat set of per-file ASTs, so a
	// direct directory listing + per-file parser.ParseFile is a strict
	// simplification, not a behavior change: every non-test .go file in this
	// directory is still scanned, build tags and all.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
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

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			for _, declName := range declaredNames(decl) {
				if why, dead := retired[declName]; dead {
					t.Errorf("%s redeclares the retired symbol %q — %s", name, declName, why)
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
