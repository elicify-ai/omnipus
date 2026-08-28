// Omnipus — ADR-068 D16.2a / FR-020h: the guard on the CALLERS of the refusal.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS — the hole every other test in this package leaves open
//
// propindex_stub_test.go proves RequirePropertyIndex REFUSES. It cannot prove a
// CALLER passes that refusal on. The failure mode FR-020h exists to prevent is
// not a missing refusal, it is a swallowed one:
//
//	if err := records.RequirePropertyIndex(records.CapabilityTypedFilter); err != nil {
//	        return []Record{}, nil          // <- an EMPTY SUCCESS. This is the bug.
//	}
//
// That code refuses correctly at the seam and reports "no records matched" to
// the operator, which is exactly the confidently-wrong answer ADR-068 was
// written to remove — and every test in this package would still be green.
//
// So this test does not test behaviour, it tests SHAPE: it parses every
// non-test Go file in the repository, finds every call to RequirePropertyIndex,
// and fails if the returned error is discarded or not propagated. It needs no
// cooperation from Stage 2 and it bites the moment the first bad call site is
// written, in the commit that writes it.
//
// WHAT IT CANNOT SEE, stated plainly rather than left to be discovered: a caller
// that never calls RequirePropertyIndex AT ALL. No static rule in this package
// can know that some future function in some other package needed the properties
// index. Two things narrow that gap, and neither is this test:
//
//   - AssertRefusesWhenIndexUnavailable (propindex_contract.go) — the harness a
//     Stage 2 test points at each of its six entry points, which asserts the
//     real call refuses rather than returning a zero value.
//   - the six entry points are enumerated in ADR-068 D15 and the spec's AC-F6 /
//     SC-023, so the list is written down and reviewable.
// ---------------------------------------------------------------------------

// guardFuncName is the seam every caller must go through.
const guardFuncName = "RequirePropertyIndex"

// guardEscapeComment lets a caller with a shape this guard cannot read opt out.
// It is deliberately greppable and deliberately ugly: every use should be
// visible in review, and there should be almost none.
const guardEscapeComment = "//records:refusal-handled"

// refusalViolation is one bad call site.
type refusalViolation struct {
	Pos    string
	Caller string
	Reason string
}

// findSwallowedRefusals reports every call to RequirePropertyIndex in file whose
// error is discarded, or whose detecting branch does not propagate it.
//
// The rule, precisely — and it is deliberately BRANCH-scoped, not function-scoped:
//
//	the refusal must be returned directly, or the branch that detects it
//	(`if err != nil { … }`) must return, and every return in that branch must
//	mention the error.
//
// An earlier version of this analyser asked the weaker question "does the name
// appear in ANY return of the enclosing function". That is a false negative
// factory, and it was caught the only way this kind of bug is ever caught: by
// mutating a REAL call site (pkg/records/propindex/sqlite.go's Open, whose
// guard clause was changed to `return nil, nil`) and finding the guard silent.
// Open reuses `err` for its later sql.Open error, so the swallowed branch was
// covered by an unrelated return forty lines away. Branch scoping is the fix.
func findSwallowedRefusals(fset *token.FileSet, file *ast.File) []refusalViolation {
	var out []refusalViolation

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Walk with a node stack so each call can find its enclosing statement
		// and that statement's parent.
		var stack []ast.Node
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)

			call, ok := n.(*ast.CallExpr)
			if !ok || !isGuardCall(call) {
				return true
			}
			if v := classifyCallSite(fset, fn, call, stack); v != nil {
				out = append(out, *v)
			}
			return true
		})
	}
	return out
}

// isGuardCall matches RequirePropertyIndex(...) and pkg.RequirePropertyIndex(...).
func isGuardCall(call *ast.CallExpr) bool {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name == guardFuncName
	case *ast.SelectorExpr:
		return f.Sel != nil && f.Sel.Name == guardFuncName
	}
	return false
}

// classifyCallSite decides whether one call site propagates the refusal.
func classifyCallSite(
	fset *token.FileSet,
	fn *ast.FuncDecl,
	call *ast.CallExpr,
	stack []ast.Node,
) *refusalViolation {
	bad := func(reason string) *refusalViolation {
		return &refusalViolation{
			Pos:    fset.Position(call.Pos()).String(),
			Caller: fn.Name.Name,
			Reason: reason,
		}
	}

	// Nearest enclosing statement, and that statement's parent node.
	stmtIdx := -1
	for i := len(stack) - 1; i >= 0; i-- {
		if _, ok := stack[i].(ast.Stmt); ok {
			stmtIdx = i
			break
		}
	}
	if stmtIdx < 0 {
		return bad("the guard could not find the statement containing this call")
	}
	stmt := stack[stmtIdx].(ast.Stmt)
	var parent ast.Node
	if stmtIdx > 0 {
		parent = stack[stmtIdx-1]
	}

	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		// return RequirePropertyIndex(c), or return nil, wrap(RequirePropertyIndex(c)).
		return nil

	case *ast.ExprStmt:
		return bad("the returned refusal is thrown away — the caller carries on as though " +
			"the properties index were present, and will produce the empty result FR-020h forbids")

	case *ast.AssignStmt:
		names, blankOnly := assignedNames(s)
		if blankOnly {
			return bad("the refusal is assigned to _ — an explicitly discarded error")
		}
		if len(names) == 0 {
			return bad("the guard cannot tell what the refusal was assigned to")
		}

		// Shape 1: if err := Require(...); err != nil { ... }
		if ifs, ok := parent.(*ast.IfStmt); ok && ifs.Init == stmt {
			return branchViolation(bad, ifs.Body, names)
		}

		// Shape 2: err := Require(...) followed by an if on that name.
		blk := enclosingBlock(stack, stmtIdx)
		if blk == nil {
			return bad("the refusal is assigned outside any block the guard can follow")
		}
		if ifs := followingNilCheck(blk, stmt, names); ifs != nil {
			return branchViolation(bad, ifs.Body, names)
		}
		// No nil-check, but a plain `return v, err` propagates the refusal just
		// as well and is idiomatic when there is nothing to add. Scoped to the
		// statements AFTER the assignment in THIS block, never the whole
		// function — that looseness is the regression documented above.
		if returnedLaterInBlock(blk, stmt, names) {
			return nil
		}
		return bad(fmt.Sprintf(
			"the refusal is assigned to %s, but %s is neither checked against nil nor returned "+
				"anywhere after this point in the block — the caller proceeds as though the "+
				"properties index were present",
			strings.Join(names, "/"), pluralIt(names)))

	default:
		return bad(fmt.Sprintf(
			"the guard does not recognise this call shape (%T) and therefore cannot see the "+
				"refusal being propagated", stmt))
	}
}

// branchViolation checks the branch that detected the refusal. It must return,
// and every return it makes must mention the error.
func branchViolation(
	bad func(string) *refusalViolation,
	body *ast.BlockStmt,
	names []string,
) *refusalViolation {
	if body == nil {
		return bad("the branch that detects the refusal has no body")
	}
	if containsPanic(body) {
		return nil
	}

	var returns []*ast.ReturnStmt
	ast.Inspect(body, func(n ast.Node) bool {
		if r, ok := n.(*ast.ReturnStmt); ok {
			returns = append(returns, r)
		}
		return true
	})
	if len(returns) == 0 {
		return bad(fmt.Sprintf(
			"the branch that detects the refusal does not return — execution falls through "+
				"and the caller continues as though %s were nil. That is the empty-success "+
				"bug FR-020h exists to prevent", strings.Join(names, "/")))
	}
	for _, r := range returns {
		if !returnMentions(r, names) {
			return bad(fmt.Sprintf(
				"the branch that detects the refusal returns WITHOUT it (%s is dropped) — "+
					"the caller reports success, and an operator on a SQLite-less build is told "+
					"there is nothing there rather than that the query cannot be answered",
				strings.Join(names, "/")))
		}
	}
	return nil
}

// assignedNames pulls the non-blank identifiers off an assignment's left side.
func assignedNames(s *ast.AssignStmt) (names []string, blankOnly bool) {
	blankOnly = true
	for _, lhs := range s.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok {
			blankOnly = false
			continue
		}
		if id.Name == "_" {
			continue
		}
		blankOnly = false
		names = append(names, id.Name)
	}
	return names, blankOnly
}

// enclosingBlock finds the innermost block statement containing stack[stmtIdx].
func enclosingBlock(stack []ast.Node, stmtIdx int) *ast.BlockStmt {
	for i := stmtIdx - 1; i >= 0; i-- {
		if b, ok := stack[i].(*ast.BlockStmt); ok {
			return b
		}
	}
	return nil
}

// followingNilCheck finds the first `if <name> != nil` after after in blk.
func followingNilCheck(blk *ast.BlockStmt, after ast.Stmt, names []string) *ast.IfStmt {
	seen := false
	for _, st := range blk.List {
		if st == after {
			seen = true
			continue
		}
		if !seen {
			continue
		}
		ifs, ok := st.(*ast.IfStmt)
		if !ok {
			continue
		}
		bin, ok := ifs.Cond.(*ast.BinaryExpr)
		if !ok || bin.Op != token.NEQ {
			continue
		}
		if identIn(bin.X, names) || identIn(bin.Y, names) {
			return ifs
		}
	}
	return nil
}

// returnedLaterInBlock reports whether a return after `after` in blk mentions
// one of names.
func returnedLaterInBlock(blk *ast.BlockStmt, after ast.Stmt, names []string) bool {
	seen := false
	for _, st := range blk.List {
		if st == after {
			seen = true
			continue
		}
		if !seen {
			continue
		}
		found := false
		ast.Inspect(st, func(n ast.Node) bool {
			if r, ok := n.(*ast.ReturnStmt); ok && returnMentions(r, names) {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

func identIn(e ast.Expr, names []string) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	for _, n := range names {
		if id.Name == n {
			return true
		}
	}
	return false
}

// returnMentions reports whether a return statement references one of names.
func returnMentions(r *ast.ReturnStmt, names []string) bool {
	found := false
	for _, res := range r.Results {
		ast.Inspect(res, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && identIn(id, names) {
				found = true
			}
			return !found
		})
	}
	return found
}

// containsPanic reports whether body calls panic — aborting is propagation too.
func containsPanic(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "panic" {
				found = true
			}
		}
		return !found
	})
	return found
}

func pluralIt(names []string) string {
	if len(names) == 1 {
		return "it"
	}
	return "any of them"
}

// ---------------------------------------------------------------------------
// The repository scan
// ---------------------------------------------------------------------------

// TestPropertyIndexGuard_NoCallerSwallowsTheRefusal walks the whole module and
// fails on any production call site that does not propagate the refusal.
//
// It passes trivially today because Stage 2 has not written the six call sites
// yet. That is not a false green — there is genuinely nothing to check — and
// TestPropertyIndexGuard_DetectsEachSwallowShape proves the analyser itself
// fires, so the day the first call site lands the guard is already armed.
func TestPropertyIndexGuard_NoCallerSwallowsTheRefusal(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()

	var violations []refusalViolation
	var scanned, withCalls int

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", ".gitnexus":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, readErr := os.ReadFile(path) //nolint:gosec // walking our own module
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		scanned++
		if !bytes.Contains(src, []byte(guardFuncName)) {
			return nil
		}

		f, parseErr := parser.ParseFile(fset, path, src, parser.ParseComments)
		if parseErr != nil {
			// A file that does not parse is someone else's in-flight edit, not
			// a refusal being swallowed. Say so and move on rather than
			// reporting a failure this test cannot substantiate.
			t.Logf("skipping unparseable %s: %v", path, parseErr)
			return nil
		}

		found := findSwallowedRefusals(fset, f)
		if len(found) > 0 || bytes.Contains(src, []byte(guardFuncName+"(")) {
			withCalls++
		}
		for _, v := range found {
			if hasEscapeComment(fset, f, v.Pos) {
				t.Logf("call site at %s opted out with %s", v.Pos, guardEscapeComment)
				continue
			}
			violations = append(violations, v)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if scanned == 0 {
		t.Fatalf("scanned no Go files under %s — the guard is not looking at anything", root)
	}

	sort.Slice(violations, func(i, j int) bool { return violations[i].Pos < violations[j].Pos })
	for _, v := range violations {
		// The message has to survive being read by someone who has never heard
		// of ADR-068 and did not expect a test in pkg/records to fail their
		// change. It therefore says what they did, why it matters, what to type
		// instead, and where the rule comes from — in that order.
		t.Errorf("%s: %s() swallows the properties-index refusal.\n"+
			"    WHAT: %s.\n"+
			"    WHY:  on linux/mipsle there is no properties index, so this path must tell the "+
			"operator the query cannot be answered. Returning a zero value tells them instead that "+
			"there is nothing to find, which is a confidently wrong answer.\n"+
			"    FIX:  return the error unchanged, or wrapped with %%w so errors.Is still finds it.\n"+
			"    RULE: ADR-068 D16.2a / spec FR-020h. The seam is records.RequirePropertyIndex "+
			"(pkg/records/propindex_stub.go); this guard is pkg/records/propindex_caller_guard_test.go.\n"+
			"    If this call really is handled and the guard cannot see the shape, put %s on the "+
			"call line — it is greppable on purpose, so keep it rare.",
			v.Pos, v.Caller, v.Reason, guardEscapeComment)
	}
	t.Logf("scanned %d non-test Go files; %d contain a %s call site; %d violations",
		scanned, withCalls, guardFuncName, len(violations))
}

// hasEscapeComment reports whether the line of the violation carries the opt-out.
func hasEscapeComment(fset *token.FileSet, f *ast.File, pos string) bool {
	// pos is "file:line:col"; pull the line number back out.
	parts := strings.Split(pos, ":")
	if len(parts) < 2 {
		return false
	}
	wantLine := parts[len(parts)-2]

	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if !strings.Contains(c.Text, guardEscapeComment) {
				continue
			}
			p := fset.Position(c.Pos())
			if fmt.Sprint(p.Line) == wantLine {
				return true
			}
		}
	}
	return false
}

// moduleRoot walks up from the test's working directory to the go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s — cannot locate the module root", dir)
		}
		dir = parent
	}
}

// ---------------------------------------------------------------------------
// Self-verification: the analyser must actually fire
// ---------------------------------------------------------------------------

// TestPropertyIndexGuard_DetectsEachSwallowShape is the mutation test for the
// guard itself. A repository-scanning test that reports zero violations is
// indistinguishable from a broken one, which is this project's documented
// false-green pattern; these fixtures make the difference observable.
func TestPropertyIndexGuard_DetectsEachSwallowShape(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		wantCaught bool
	}{
		{
			name:       "the real bug: refuses, then returns an empty success",
			wantCaught: true,
			src: `package p
func Find(c int) ([]int, error) {
	if err := RequirePropertyIndex(c); err != nil {
		return []int{}, nil
	}
	return nil, nil
}`,
		},
		{
			// THE REGRESSION. An earlier, function-scoped version of this
			// analyser passed this fixture silently: `err` is reused for the
			// later sql.Open failure, so "does err appear in any return of this
			// function" answered yes while the refusal branch dropped it. This
			// is the exact shape of pkg/records/propindex/sqlite.go's Open, and
			// mutating that real file is how the weakness was found.
			name:       "swallowed in the branch, but err is legitimately returned later",
			wantCaught: true,
			src: `package p
func Open(c int, path string) (*T, error) {
	if err := records.RequirePropertyIndex(c); err != nil {
		return nil, nil
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("propindex: open %q: %w", path, err)
	}
	return db, nil
}`,
		},
		{
			name:       "detected but not returned — execution falls through",
			wantCaught: true,
			src: `package p
func Find(c int) ([]int, error) {
	if err := RequirePropertyIndex(c); err != nil {
		println("unavailable")
	}
	return []int{}, nil
}`,
		},
		{
			// Not a bug: returning the refusal alongside a zero value is
			// propagation. Written down because the first version of this
			// analyser flagged it, and a guard that cries wolf gets disabled.
			name:       "correct: assigned, then returned with no nil-check",
			wantCaught: false,
			src: `package p
func Find(c int) ([]int, error) {
	err := RequirePropertyIndex(c)
	return []int{}, err
}`,
		},
		{
			name:       "assigned, then genuinely dropped",
			wantCaught: true,
			src: `package p
func Find(c int) ([]int, error) {
	err := RequirePropertyIndex(c)
	_ = err
	return []int{}, nil
}`,
		},
		{
			name:       "correct: panicking is propagation too",
			wantCaught: false,
			src: `package p
func MustFind(c int) []int {
	if err := RequirePropertyIndex(c); err != nil {
		panic(err)
	}
	return nil
}`,
		},
		{
			name:       "error discarded with the blank identifier",
			wantCaught: true,
			src: `package p
func Find(c int) error {
	_ = RequirePropertyIndex(c)
	return nil
}`,
		},
		{
			name:       "call result thrown away entirely",
			wantCaught: true,
			src: `package p
func Find(c int) error {
	RequirePropertyIndex(c)
	return nil
}`,
		},
		{
			name:       "refusal logged and then dropped",
			wantCaught: true,
			src: `package p
func Find(c int) ([]int, error) {
	err := RequirePropertyIndex(c)
	if err != nil {
		println(err.Error())
	}
	return []int{}, nil
}`,
		},
		{
			name:       "qualified call, swallowed",
			wantCaught: true,
			src: `package p
func Find(c int) ([]int, error) {
	if err := records.RequirePropertyIndex(c); err != nil {
		return nil, nil
	}
	return nil, nil
}`,
		},
		{
			name:       "correct: returned directly",
			wantCaught: false,
			src: `package p
func Find(c int) error {
	return RequirePropertyIndex(c)
}`,
		},
		{
			name:       "correct: guard clause returns the error",
			wantCaught: false,
			src: `package p
func Find(c int) ([]int, error) {
	if err := RequirePropertyIndex(c); err != nil {
		return nil, err
	}
	return nil, nil
}`,
		},
		{
			name:       "correct: wrapped with %w",
			wantCaught: false,
			src: `package p
func Find(c int) ([]int, error) {
	if err := RequirePropertyIndex(c); err != nil {
		return nil, fmt.Errorf("vault_find: %w", err)
	}
	return nil, nil
}`,
		},
		{
			name:       "correct: assigned early, returned later",
			wantCaught: false,
			src: `package p
func Find(c int) ([]int, error) {
	err := RequirePropertyIndex(c)
	name := "x"
	_ = name
	if err != nil {
		return nil, err
	}
	return nil, nil
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "fixture.go", tc.src, parser.ParseComments)
			if err != nil {
				t.Fatalf("fixture does not parse: %v", err)
			}
			got := findSwallowedRefusals(fset, f)
			if tc.wantCaught && len(got) == 0 {
				t.Errorf("the guard did NOT catch this swallow — it would ship:\n%s", tc.src)
			}
			if !tc.wantCaught && len(got) > 0 {
				t.Errorf("the guard falsely flagged correct code (%s):\n%s", got[0].Reason, tc.src)
			}
		})
	}
}
