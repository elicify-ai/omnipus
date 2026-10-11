// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// S1-14: FakeRunner.InjectEvent returns ErrFakeRunnerCancelled when the fake
// runner has already been cancelled (the event stream is closed, so the event
// is dropped rather than sent on a closed channel). A call site that discards
// that error silently drops the event it believes it injected: the test then
// either passes vacuously or fails much later with a confusing timeout, and
// the failure names nothing that points at the real cause.
//
// This is a source-level guard, not a behavioural one: it fails when ANY
// InjectEvent call in a _test.go file has its result discarded. It is the
// structural counterpart to the behavioural contract already pinned in
// pkg/agent/runner/runnertest/fake_test.go
// (TestInjectEvent_AfterCancelDropsTheEventAndReturnsAnError).

package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// injectEventIsDiscarded reports the source position of an InjectEvent call
// whose result is thrown away, or "" when the call is checked.
//
// Checked means the call is NOT the whole of a statement: it is an argument
// (require.NoError(t, fr.InjectEvent(ev))), the right-hand side of an
// assignment (err := fr.InjectEvent(ev)), or a condition. Only a bare
// statement — `fr.InjectEvent(ev)` — and a bare `go`/`defer` of it are
// discards.
func injectEventIsDiscarded(fset *token.FileSet, n ast.Node) string {
	switch stmt := n.(type) {
	case *ast.ExprStmt:
		if call, ok := stmt.X.(*ast.CallExpr); ok && isInjectEventCall(call) {
			return fset.Position(call.Pos()).String()
		}
	case *ast.GoStmt:
		if isInjectEventCall(stmt.Call) {
			return fset.Position(stmt.Call.Pos()).String()
		}
	case *ast.DeferStmt:
		if isInjectEventCall(stmt.Call) {
			return fset.Position(stmt.Call.Pos()).String()
		}
	}
	return ""
}

func isInjectEventCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "InjectEvent"
}

func TestFakeRunnerInjectEventResultIsNeverDiscarded(t *testing.T) {
	root := findRepoRoot(t)
	var offenders []string
	total := 0

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "vendor", "spa":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, src, 0)
		if parseErr != nil {
			// A file this guard cannot parse is not this guard's finding.
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && isInjectEventCall(call) {
				total++
			}
			if pos := injectEventIsDiscarded(fset, n); pos != "" {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				offenders = append(offenders, rel+":"+strings.TrimPrefix(pos, path+":"))
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", root, walkErr)
	}

	// Instrument check: the scan must be able to SEE InjectEvent call sites at
	// all, or a green here proves nothing. fake_test.go alone has several.
	if total == 0 {
		t.Fatal("instrument: the scan found no InjectEvent call site anywhere — " +
			"the guard cannot see the failure it exists to catch")
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("S1-14: %d InjectEvent call site(s) discard the error, so an event dropped "+
			"after Cancel would be silent:\n  %s\nCheck it (require.NoError) or say so "+
			"explicitly (`_ =`).", len(offenders), strings.Join(offenders, "\n  "))
	}
}
