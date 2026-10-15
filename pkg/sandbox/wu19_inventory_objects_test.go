package sandbox

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// The inventory deliberately includes every platform file and incomplete
// instrument programs. Collect per-file lexical identities, not a package build
// verdict; the inventory's own import/type resolver still follows other files.
func (f *wu19File) resolveObjects(fset *token.FileSet) {
	f.identities = &types.Info{
		Defs:   map[*ast.Ident]types.Object{},
		Uses:   map[*ast.Ident]types.Object{},
		Scopes: map[ast.Node]*types.Scope{},
	}
	cfg := types.Config{Error: func(err error) { f.identityErrors = append(f.identityErrors, err) }}
	if _, err := cfg.Check(f.pkg, fset, []*ast.File{f.ast}, f.identities); err != nil && len(f.identityErrors) == 0 {
		panic(fmt.Sprintf("%s: identity checker failed without diagnostics: %v", f.path, err))
	}
	f.declarations = map[types.Object]ast.Node{}
	ast.Inspect(f.ast, func(n ast.Node) bool {
		var names []*ast.Ident
		switch node := n.(type) {
		case *ast.Field:
			names = node.Names
		case *ast.ValueSpec:
			names = node.Names
		case *ast.TypeSpec:
			names = []*ast.Ident{node.Name}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					names = append(names, id)
				}
			}
		}
		for _, name := range names {
			if object := f.identities.Defs[name]; object != nil {
				f.declarations[object] = n
			}
		}
		return true
	})
}

func (f *wu19File) object(id *ast.Ident) types.Object {
	object := f.identities.ObjectOf(id)
	if object == nil {
		// Incomplete programs can omit Uses on an ill-typed expression. The
		// type checker's lexical scopes still supply the declaration identity;
		// this is not a name-only alias or a package type-check success claim.
		var inner *types.Scope
		for _, scope := range f.identities.Scopes {
			if scope.Contains(id.Pos()) && (inner == nil || scope.End()-scope.Pos() < inner.End()-inner.Pos()) {
				inner = scope
			}
		}
		if inner != nil {
			_, object = inner.LookupParent(id.Name, id.Pos())
		}
	}
	if object == nil {
		for declared := range f.declarations {
			scope := declared.Parent()
			if scope != nil && scope.Contains(id.Pos()) && declared.Name() == id.Name && declared.Pos() <= id.Pos() {
				panic(fmt.Sprintf("identity resolution lost local %s in %s", id.Name, f.path))
			}
		}
	}
	if object == nil || object.Parent() == types.Universe {
		return nil // predeclared names and cross-file globals use existing resolution.
	}
	if _, imported := object.(*types.PkgName); imported {
		return nil // import identity comes from the existing explicit import table.
	}
	return object
}

// FR-239: an incomplete type-check verdict is expected, a lost local identity
// is not. It must stop the inventory instead of hiding a constructor or alias.
func TestWU19_IdentityResolutionInstrument(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "identity.go", "package probe; func probe() { value := 1; _ = value }", parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	source := &wu19File{path: "identity.go", pkg: "probe", ast: file}
	source.resolveObjects(fset)
	var use *ast.Ident
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "value" && source.identities.Uses[id] != nil {
			use = id
		}
		return true
	})
	if use == nil {
		t.Fatal("positive control: type checker did not record the local use")
	}
	object := source.object(use)
	if object == nil || object.Name() != "value" {
		t.Fatalf("positive control: local value identity = %v", object)
	}
	delete(source.identities.Uses, use)
	if recovered := source.object(use); recovered != object {
		t.Fatalf("lexical scope recovery changed local identity: got %v, want %v", recovered, object)
	}
	clear(source.identities.Scopes)
	defer func() {
		const want = "identity resolution lost local value in identity.go"
		if got := recover(); got != want {
			t.Fatalf("missing local identity panic = %v, want %q", got, want)
		}
	}()
	source.object(use)
}
