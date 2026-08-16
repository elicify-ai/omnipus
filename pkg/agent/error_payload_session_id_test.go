package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestErrorPayloadCompositeLitsStampSessionID is a trip-wire: every
// ErrorPayload{...} composite in the live producers must set SessionID.
// ChatID alone dies when ServeHTTP mints a new webchat: uuid, and
// matchesEvent then drops the typed error for a second tab / reload.
func TestErrorPayloadCompositeLitsStampSessionID(t *testing.T) {
	files := []string{"loop.go", "external_dispatch.go"}
	fset := token.NewFileSet()
	for _, name := range files {
		src, err := os.ReadFile(filepath.Join(".", name))
		require.NoError(t, err, name)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err, name)
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if !identNameIs(cl.Type, "ErrorPayload") {
				return true
			}
			hasSession := false
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == "SessionID" {
					hasSession = true
					break
				}
			}
			if !hasSession {
				t.Errorf("%s: ErrorPayload composite at %s is missing SessionID — a second tab will never see this error",
					name, fset.Position(cl.Pos()))
			}
			return true
		})
	}
}

func identNameIs(expr ast.Expr, name string) bool {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name == name
	case *ast.SelectorExpr:
		return x.Sel.Name == name
	}
	return false
}
