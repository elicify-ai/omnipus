package gateway

// H46's retained exact-name row is an omission guard only. The mandatory real
// mux/writer leaves must run separately; this parser never launches a Go process.
import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

type pgS113Leaf struct {
	file, top, leaf string
	tagged          bool
}

func pgRequireS113Inventory(t *testing.T) {
	t.Helper()
	if !pgS113SeamsBuild {
		t.Fatal("BLOCKED: H46 requires -tags goolm,stdjson,s1seams and separate runs of TestProgramCredential_LiveMuxInventory/local, TestProgramCredential_LiveMuxInventory/platform, TestProgramCredential_WriterReferences, TestProgramCredential_WriterReferenceInstrument and TestProgramCredential_RouteSweep_PlantedIndirectWriter; this retained exact-name row is a selection guard only")
	}
	rows := []pgS113Leaf{
		{"programs_live_mux_s1seams_test.go", "TestProgramCredential_LiveMuxInventory", "local", true},
		{"programs_live_mux_s1seams_test.go", "TestProgramCredential_LiveMuxInventory", "platform", true},
		{"programs_writer_guard_test.go", "TestProgramCredential_WriterReferences", "", false},
		{"programs_writer_instrument_test.go", "TestProgramCredential_WriterReferenceInstrument", "", false},
		{"programs_route_sweep_test.go", "TestProgramCredential_RouteSweep_PlantedIndirectWriter", "", false},
	}
	root := pgWriterRoot(t)
	for _, row := range rows {
		path := filepath.Join(root, "pkg", "gateway", row.file)
		ctx := build.Default
		ctx.CgoEnabled = false
		ctx.BuildTags = []string{"goolm", "stdjson", "s1seams"}
		selected, err := ctx.MatchFile(filepath.Dir(path), filepath.Base(path))
		require.NoError(t, err)
		require.True(t, selected, "H46 mandatory successor must be build-selected: %s", row.file)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		require.True(t, pgHasExactInventoryLeaf(file, row.top, row.leaf), "H46 requires exact successor %s/%s in %s", row.top, row.leaf, row.file)
		if row.tagged {
			ctx.BuildTags = []string{"goolm", "stdjson"}
			selected, err = ctx.MatchFile(filepath.Dir(path), filepath.Base(path))
			require.NoError(t, err)
			require.False(t, selected, "instrument: actual live-mux successor is omitted without s1seams")
		}
	}
	control, err := parser.ParseFile(token.NewFileSet(), "control_test.go", `package fixture
func TestSelected(t *testing.T) { t.Run("expected_scenario", func(t *testing.T) {}) }
`, 0)
	require.NoError(t, err)
	require.True(t, pgHasExactInventoryLeaf(control, "TestSelected", ""))
	require.True(t, pgHasExactInventoryLeaf(control, "TestSelected", "expected_scenario"))
	require.False(t, pgHasExactInventoryLeaf(control, "TestSelected", "missing_scenario"), "instrument: missing leaf fails the same detector")
	require.False(t, pgHasExactInventoryLeaf(control, "TestMissing", "expected_scenario"), "instrument: right leaf under wrong top cannot satisfy this guard")
	require.False(t, pgHasExactInventoryLeaf(control, "TestMissing", ""), "instrument: missing top-level writer leaf fails the same detector")
	t.Log("H46 static build/name guard only: all five mandatory successors selected by exact names; run them separately for acceptance, never infer execution from this guard")
}

func pgHasExactInventoryLeaf(file *ast.File, top, leaf string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != top || function.Body == nil || len(function.Body.List) == 0 {
			continue
		}
		if leaf == "" {
			return true
		}
		found := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Run" {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok || receiver.Name != "t" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if ok && literal.Kind == token.STRING {
				name, err := strconv.Unquote(literal.Value)
				found = found || (err == nil && name == leaf)
			}
			return true
		})
		return found
	}
	return false
}
