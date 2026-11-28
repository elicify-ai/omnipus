package programs

// The S1 acceptance gate must execute the tagged real-flow successors. This
// cheap guard never calls another Go process. Untagged CI remains loudly red;
// with s1seams, build selection and both exact names must survive. A passing
// inventory guard is NOT runtime proof for the successor.
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

type s1SeamLeaf struct{ file, top, leaf string }

func requireS1TaggedLeaf(t *testing.T, row string) {
	t.Helper()
	inventory := map[string]s1SeamLeaf{
		"H42": {"pkg/gateway/programs_delayed_launch_seam_linux_test.go", "TestProgramCheck_ActualDelayedLaunch", "S139_delayed_launch_cannot_stop_the_newer_check"},
		"H43": {"pkg/agent/programs_egress_mint_seam_test.go", "TestProgramNetwork_ActualMintHistory", "S25_god_mode_token_available_no_egress_token"},
		"H44": {"pkg/agent/programs_capture_fault_seam_test.go", "TestProgramMasking_ActualCaptureSeam", "S103_Dev_exec_reaches_instrumented_cap"},
		"H45": {"pkg/agent/programs_capture_fault_seam_test.go", "TestProgramMasking_ActualReadFaultSeam", "D9_row7_readerror_stdout10_background_read"},
	}
	want, ok := inventory[row]
	require.True(t, ok, "instrument: every migrated row has an explicit exact successor")
	if !s1SeamTestBuild {
		t.Fatalf("BLOCKED: %s requires the real acceptance leaf %s/%s with -tags goolm,stdjson,s1seams; an untagged run cannot certify it", row, want.top, want.leaf)
	}
	root := repoRootForSpawnGuard(t)
	path := filepath.Join(root, filepath.FromSlash(want.file))
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = "linux", "amd64", false
	ctx.BuildTags = []string{"goolm", "stdjson", "s1seams"}
	selected, err := ctx.MatchFile(filepath.Dir(path), filepath.Base(path))
	require.NoError(t, err, "the expected tagged successor file must be readable: %s", want.file)
	require.True(t, selected, "%s's successor must be included in the actual Linux s1seams build selection", row)
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	require.True(t, s1HasExactSeamLeaf(file, want.top, want.leaf), "%s's exact top-level function and named scenario must remain in the selected source", row)

	// Positive + adversarial controls use separate in-memory source, not edits
	// to production or the checked-in successor. Name absence cannot pass.
	control, err := parser.ParseFile(token.NewFileSet(), "control_test.go", `package fixture
func TestSelected(t *testing.T) { t.Run("expected_scenario", func(t *testing.T) {}) }
`, 0)
	require.NoError(t, err)
	require.True(t, s1HasExactSeamLeaf(control, "TestSelected", "expected_scenario"))
	require.False(t, s1HasExactSeamLeaf(control, "TestSelected", "missing_scenario"), "instrument: missing scenario name fails the SAME detector")
	require.False(t, s1HasExactSeamLeaf(control, "TestMissing", "expected_scenario"), "instrument: a correct leaf under the wrong top-level test cannot satisfy the guard")
	ctx.BuildTags = []string{"goolm", "stdjson"}
	selected, err = ctx.MatchFile(filepath.Dir(path), filepath.Base(path))
	require.NoError(t, err)
	require.False(t, selected, "instrument: this exact successor really depends on s1seams, so untagged omission must remain loud")
	t.Logf("%s static build/name guard only: execute %s/%s separately with s1seams for product acceptance", row, want.top, want.leaf)
}

func s1HasExactSeamLeaf(file *ast.File, top, leaf string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != top || function.Body == nil {
			continue
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
