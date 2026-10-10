// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Deletion guards (session-core DEL-13, DEL-17): the removed helpers must not
// come back as a "conflict resolution", and the kept ones must still exist so
// the scan itself is proven able to see a declaration.
package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// declaredFuncs returns "Recv.Name" / "Name" for every function declared in the
// non-test Go files of dir.
func declaredFuncs(t *testing.T, dir string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	out := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				name := fd.Name.Name
				if fd.Recv != nil && len(fd.Recv.List) == 1 {
					switch rt := fd.Recv.List[0].Type.(type) {
					case *ast.StarExpr:
						if id, ok := rt.X.(*ast.Ident); ok {
							name = id.Name + "." + name
						}
					case *ast.Ident:
						name = rt.Name + "." + name
					}
				}
				out[name] = true
			}
		}
	}
	return out
}

// DEL-13: SteerBootRecovery.failLegacy is gone; failInterrupted-era helpers and
// recoverOrdinaryRoot stay.
func TestDEL13_FailLegacyStaysDeleted(t *testing.T) {
	funcs := declaredFuncs(t, ".")
	for _, kept := range []string{"SteerBootRecovery.recoverSteered", "AgentLoop.ensureOrdinaryRootRecord"} {
		if !funcs[kept] {
			t.Fatalf("instrument check: the scan must see %s", kept)
		}
	}
	if !funcs["SteerBootRecovery.recoverOrdinaryRoot"] {
		t.Fatalf("DEL-13 keeps recoverOrdinaryRoot")
	}
	if funcs["SteerBootRecovery.failLegacy"] {
		t.Fatal("DEL-13: SteerBootRecovery.failLegacy must stay deleted")
	}
}

// DEL-17: the u11CollectDescendantSessionIDs shim is gone from the gateway and
// no prose in pkg/agent/cancel.go still describes it; the one collector stays.
func TestDEL17_U11CollectShimStaysDeleted(t *testing.T) {
	if !declaredFuncs(t, ".")["CollectDescendantSessionIDs"] {
		t.Fatal("DEL-17 keeps the one shared collector CollectDescendantSessionIDs")
	}
	gw := filepath.Join("..", "gateway")
	gwFuncs := declaredFuncs(t, gw)
	if !gwFuncs["WSHandler.buildCancelHooks"] {
		t.Fatal("instrument check: the scan must see a known gateway function")
	}
	if gwFuncs["u11CollectDescendantSessionIDs"] {
		t.Fatal("DEL-17: the gateway u11CollectDescendantSessionIDs shim must stay deleted")
	}
	// Prose and call sites in the non-test sources of both packages.
	for _, dir := range []string{".", gw} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") ||
				e.Name() == "del13_del17_deletion_guard_test.go" {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "u11CollectDescendantSessionIDs") {
				t.Errorf("DEL-17: %s still mentions u11CollectDescendantSessionIDs", filepath.Join(dir, e.Name()))
			}
		}
	}
}
