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
// ErrorPayload{...} and RateLimitPayload{...} composite in the live
// producers must set SessionID to a non-empty expression. ChatID alone
// dies when ServeHTTP mints a new webchat: uuid, and matchesEvent then
// drops the typed error for a second tab / reload. A SessionID: ""
// literal would pass a key-exists scan and still leave the second tab
// silent.
func TestErrorPayloadCompositeLitsStampSessionID(t *testing.T) {
	files := []string{"loop.go", "external_dispatch.go"}
	wanted := map[string]struct{}{
		"ErrorPayload":     {},
		"RateLimitPayload": {},
	}
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
			typeName := identName(cl.Type)
			if _, want := wanted[typeName]; !want {
				return true
			}
			hasSession := false
			emptyLiteral := false
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				ident, ok := kv.Key.(*ast.Ident)
				if !ok || ident.Name != "SessionID" {
					continue
				}
				hasSession = true
				if bl, ok := kv.Value.(*ast.BasicLit); ok && bl.Kind == token.STRING && bl.Value == `""` {
					emptyLiteral = true
				}
			}
			if !hasSession {
				t.Errorf("%s: %s composite at %s is missing SessionID — a second tab will never see this error",
					name, typeName, fset.Position(cl.Pos()))
			}
			if emptyLiteral {
				t.Errorf("%s: %s composite at %s sets SessionID to \"\" — a second tab will never see this error",
					name, typeName, fset.Position(cl.Pos()))
			}
			return true
		})
	}
}

func identName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}
