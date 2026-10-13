package sandbox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FR-239 explicitly requires a production spawn-site inventory. This structural
// guard is additional to the four real-child tests, not behavioral proof itself.
// It scans all platform files, with no fixed site count or suppressions. It checks
// explicit environment assignment and raw-parent provenance within each function.
// Explicit helper/parameter handoffs still require behavioral verification; this
// inventory does not claim interprocedural or branch-dominance proof.
func TestWU19_SpawnSiteInventory(t *testing.T) {
	t.Run("instrument", func(t *testing.T) {
		cases := []struct {
			name, body string
			unsafe     bool
		}{
			{"omitted", `cmd := exec.Command("x"); cmd.Run()`, true},
			{"nil", `cmd := exec.Command("x"); cmd.Env = nil; cmd.Run()`, true},
			{"raw", `cmd := exec.Command("x"); cmd.Env = os.Environ(); cmd.Run()`, true},
			{"indirect", `env := os.Environ(); cmd := exec.Command("x"); cmd.Env = append(env, "SITE=x"); cmd.Run()`, true},
			{"conditional", `cmd := exec.Command("x"); if flag { cmd.Env = sandbox.ScrubGatewayEnv() }; cmd.Run()`, true},
			{"scrubbed", `cmd := exec.Command("x"); cmd.Env = append(sandbox.ScrubGatewayEnv(), "SITE=x"); cmd.Run()`, false},
			{"comments_are_not_proof", `cmd := exec.Command("x"); /* cmd.Env = sandbox.ScrubGatewayEnv() */ cmd.Run()`, true},
		}
		for _, tc := range cases {
			f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package fixture; func run() {"+tc.body+"}", 0)
			if err != nil {
				t.Fatal(err)
			}
			got := wu19UnprovedSpawns(f)
			if (len(got) > 0) != tc.unsafe {
				t.Errorf("%s: unsafe=%t want %t", tc.name, len(got) > 0, tc.unsafe)
			}
		}
	})
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("inventory root instrument: %v", err)
	}
	var sites int
	for _, tree := range []string{"pkg", "cmd", "internal"} {
		base := filepath.Join(root, tree)
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue // some repositories have no internal/ tree; pkg/ is checked above.
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "testutil" || d.Name() == "runnertest" {
					return filepath.SkipDir // test fixture programs, not production.
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok && wu19IsExec(c) {
					sites++
				}
				return true
			})
			for _, name := range wu19UnprovedSpawns(f) {
				t.Errorf("FR-239 BLOCKED: %s::%s child environment not proven scrubbed (raw, nil, conditional or unresolved handoff)", path, name)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", tree, err)
		}
	}
	if sites == 0 {
		t.Fatal("inventory instrument failed: zero production exec constructors found")
	}
	t.Logf("inventoried %d production exec constructors across all platform files", sites)
}

func wu19IsExec(c *ast.CallExpr) bool {
	s, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || (s.Sel.Name != "Command" && s.Sel.Name != "CommandContext") {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	return ok && x.Name == "exec"
}

func wu19UnprovedSpawns(f *ast.File) []string {
	var failures []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		vars := map[string]ast.Expr{}
		constructors := map[*ast.CallExpr]string{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if a, ok := n.(*ast.AssignStmt); ok && len(a.Lhs) == len(a.Rhs) {
				for i, lhs := range a.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						vars[id.Name] = a.Rhs[i]
						if c, ok := a.Rhs[i].(*ast.CallExpr); ok && wu19IsExec(c) {
							constructors[c] = id.Name
						}
					}
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok || !wu19IsExec(c) {
				return true
			}
			cmd := constructors[c]
			proven := false
			// Require a base assignment in the same block as the constructor;
			// an assignment hidden only inside a later if cannot suffice.
			block := fn.Body
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if b, ok := n.(*ast.BlockStmt); ok && b.Pos() <= c.Pos() && c.End() <= b.End() && b.End()-b.Pos() < block.End()-block.Pos() {
					block = b
				}
				return true
			})
			for _, stmt := range block.List {
				a, ok := stmt.(*ast.AssignStmt)
				if !ok || len(a.Lhs) != 1 || len(a.Rhs) != 1 || a.Pos() <= c.Pos() {
					continue
				}
				s, ok := a.Lhs[0].(*ast.SelectorExpr)
				if !ok || s.Sel.Name != "Env" {
					continue
				}
				id, ok := s.X.(*ast.Ident)
				if ok && cmd != "" && id.Name == cmd {
					proven = !wu19UnsafeEnvExpr(a.Rhs[0], vars, map[string]bool{})
				}
			}
			if !proven {
				failures = append(failures, fn.Name.Name)
			}
			return true
		})
	}
	return failures
}

func wu19UnsafeEnvExpr(e ast.Expr, vars map[string]ast.Expr, seen map[string]bool) bool {
	if e == nil {
		return false // unresolved parameter/helper: behavioral proof still needed.
	}
	if id, ok := e.(*ast.Ident); ok {
		if id.Name == "nil" || seen[id.Name] {
			return true
		}
		seen[id.Name] = true
		return wu19UnsafeEnvExpr(vars[id.Name], vars, seen)
	}
	c, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	if s, ok := c.Fun.(*ast.SelectorExpr); ok {
		if id, ok := s.X.(*ast.Ident); ok {
			if id.Name == "os" && s.Sel.Name == "Environ" {
				return true
			}
			if id.Name == "sandbox" && (s.Sel.Name == "ScrubGatewayEnv" || s.Sel.Name == "ScrubGatewayEnvForRunner") {
				return false
			}
		}
	}
	for _, arg := range c.Args {
		if wu19UnsafeEnvExpr(arg, vars, seen) {
			return true
		}
	}
	return false
}
