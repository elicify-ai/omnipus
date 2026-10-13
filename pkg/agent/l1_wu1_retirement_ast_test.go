package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-120 / spec §6.2 and test-plan item 22 explicitly require a source scan
// for SYMBOL RETIREMENT ONLY. Behaviour is exercised by the REST/WS/turn tests.
// Comments/quoted prose are not symbols, so this is an AST scan, not a grep.
func l1WU1RetiredSymbols(file *ast.File) []string {
	banned := map[string]bool{"GetDefaultAgent": true, "SetDefaultAgentOverride": true, "DefaultAgentID": true}
	found := []string{}
	ast.Inspect(file, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && banned[id.Name] {
			found = append(found, id.Name)
		}
		return true
	})
	return found
}

func TestL1WU1NoDefaultAgentSymbols_ProductionASTAndInstrumentControl(t *testing.T) {
	fset := token.NewFileSet()
	t.Run("scanner_positive_control", func(t *testing.T) {
		bad, err := parser.ParseFile(fset, "known-banned.go", `package fixture; func probe(r any) { r.GetDefaultAgent() }`, 0)
		require.NoError(t, err)
		assert.Equal(t, []string{"GetDefaultAgent"}, l1WU1RetiredSymbols(bad), "empty-search instrument must detect the banned selector")
		good, err := parser.ParseFile(fset, "prose-only.go", "package fixture\n// GetDefaultAgent in a historical comment is not a symbol\nvar text = \"DefaultAgentID\"", 0)
		require.NoError(t, err)
		assert.Empty(t, l1WU1RetiredSymbols(good))
	})
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(here), "..", "..", "pkg"))
	files := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		files++
		// The only allowed exception is a warning/ignore loader function;
		// declarations of a Config field or resolver are never exceptions.
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if isFunc && strings.Contains(filepath.ToSlash(path), "/pkg/config/") &&
				(strings.Contains(strings.ToLower(fn.Name.Name), "load") || strings.Contains(strings.ToLower(fn.Name.Name), "ignore")) {
				copyFile := &ast.File{Decls: []ast.Decl{decl}}
				for _, name := range l1WU1RetiredSymbols(copyFile) {
					assert.Equal(t, "DefaultAgentID", name, "loader cannot retain an executable default-agent resolver")
				}
				continue
			}
			copyFile := &ast.File{Decls: []ast.Decl{decl}}
			assert.Empty(t, l1WU1RetiredSymbols(copyFile), "FR-120: retired default-agent symbol remains in %s", path)
		}
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, files, 1, "the scan must traverse production files, not one asset")
}
