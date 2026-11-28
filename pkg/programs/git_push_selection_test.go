package programs

import (
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Name-selection integrity only. The actual S20 oracle must run separately in
// package agent, which imports programs and owns the real private loop boundary.
// No nested Go process, product result simulation or guard bypass is involved.
func requireS20RealLoopLeaf(t *testing.T) {
	t.Helper()
	const top = "TestProgramGitHelper_ResetChain"
	const leaf = "S20_no_token_in_config_or_arguments"
	path := filepath.Join(repoRootForSpawnGuard(t), "pkg", "agent", "programs_git_https_push_test.go")
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = "linux", "amd64", false
	ctx.BuildTags = []string{"goolm", "stdjson", "s1seams"}
	selected, err := ctx.MatchFile(filepath.Dir(path), filepath.Base(path))
	require.NoError(t, err, "BLOCKED: S20 real-loop source must exist in the tagged build")
	require.True(t, selected, "BLOCKED: S20 real-loop source excluded from the tagged build")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	require.True(t, s1HasExactSeamLeaf(file, top, leaf), "BLOCKED: exact S20 real-loop name absent; static selection cannot silently certify a removed test")

	// Exercise the same name detector with independent in-memory source. A
	// missing top, missing leaf, or names found only in comments must fail.
	control, err := parser.ParseFile(token.NewFileSet(), "s20_control_test.go", `package fixture
func TestSelected(t *testing.T) { t.Run("expected_scenario", func(t *testing.T) {}) }
// TestMissing / missing_scenario
`, 0)
	require.NoError(t, err)
	require.True(t, s1HasExactSeamLeaf(control, "TestSelected", "expected_scenario"))
	require.False(t, s1HasExactSeamLeaf(control, "TestMissing", "expected_scenario"), "instrument: absent top must fail the same detector")
	require.False(t, s1HasExactSeamLeaf(control, "TestSelected", "missing_scenario"), "instrument: absent/comment-only leaf must fail the same detector")
	t.Logf("S20 static tagged-build/name integrity ONLY: separately execute pkg/agent %s/%s for real-loop product proof", top, leaf)
}
