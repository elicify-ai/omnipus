package bus

// Spec I-2/S111: the current durable entry points must take explicit provenance
// in their input. This source-level contract does not substitute for dispatch
// tests. A particular request type name is intentionally not prescribed.
import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type programWriteBoundary struct{ receiver, method string }

func programRequestBoundaryViolations(t *testing.T, sources map[string]string, boundaries []programWriteBoundary) []string {
	t.Helper()
	types := map[string]ast.Expr{}
	methods := map[programWriteBoundary]*ast.FuncDecl{}
	for path, source := range sources {
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		require.NoError(t, err, "unparseable source cannot clear the required-provenance contract")
		ast.Inspect(file, func(n ast.Node) bool {
			if d, ok := n.(*ast.TypeSpec); ok {
				types[d.Name.Name] = d.Type
			}
			return true
		})
		for _, decl := range file.Decls {
			d, ok := decl.(*ast.FuncDecl)
			if !ok || d.Recv == nil || len(d.Recv.List) != 1 {
				continue
			}
			expr := d.Recv.List[0].Type
			if ptr, ok := expr.(*ast.StarExpr); ok {
				expr = ptr.X
			}
			if id, ok := expr.(*ast.Ident); ok {
				methods[programWriteBoundary{id.Name, d.Name.Name}] = d
			}
		}
	}
	carries := func(expr ast.Expr) bool {
		if ptr, ok := expr.(*ast.StarExpr); ok {
			expr = ptr.X
		}
		if id, ok := expr.(*ast.Ident); ok {
			expr = types[id.Name]
		}
		record, ok := expr.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range record.Fields.List {
			for _, name := range field.Names {
				if name.Name == "Provenance" {
					return true
				}
			}
		}
		return false
	}
	var violations []string
	for _, boundary := range boundaries {
		fn := methods[boundary]
		if fn == nil {
			violations = append(violations, fmt.Sprintf("%s.%s: durable entry point missing from parsed source", boundary.receiver, boundary.method))
			continue
		}
		found := false
		for _, parameter := range fn.Type.Params.List {
			found = found || carries(parameter.Type)
		}
		if !found {
			violations = append(violations, fmt.Sprintf("%s.%s: no explicit input carries Provenance; a record/opaque closure cannot establish I-2's required provenance", boundary.receiver, boundary.method))
		}
	}
	return violations
}

func assertProgramRequiredWriteBoundary(t *testing.T, packageName string, boundaries []programWriteBoundary) {
	t.Helper()
	dir := filepath.Join(repoRootForGuard(), "pkg", packageName)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	sources := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		sources[path] = string(raw)
	}
	require.NotEmpty(t, sources, "instrument: the actual write-boundary package was read")
	require.Empty(t, programRequestBoundaryViolations(t, sources, boundaries), "I-2/S111: provenance is explicit at every listed durable write, not inferred after it")
}

func TestProgramProvenance_RequiredWriteInstrument(t *testing.T) {
	boundary := []programWriteBoundary{{"Store", "Update"}}
	positive := "package probe; type Store struct{}; type Write struct{ Provenance []string }; func (*Store) Update(w Write) {}"
	require.Empty(t, programRequestBoundaryViolations(t, map[string]string{"positive.go": positive}, boundary))
	negative := "package probe; type Store struct{}; type Write struct{ Prompt string }; func (*Store) Update(w Write) {}"
	bad := programRequestBoundaryViolations(t, map[string]string{"omitted.go": negative}, boundary)
	require.Len(t, bad, 1, "instrument must reject an input without provenance")
	require.Contains(t, bad[0], "Store.Update: no explicit input carries Provenance")
	closure := "package probe; type Store struct{}; type Record struct{ Provenance []string }; func (*Store) Update(id string, mutate func(*Record) error) {}"
	require.Len(t, programRequestBoundaryViolations(t, map[string]string{"closure.go": closure}, boundary), 1, "a captured opaque mutation closure is not the required provenance input")
}
