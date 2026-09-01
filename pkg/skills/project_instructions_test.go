package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectInstructions_SingleFilePerMountDeterministic (test 16)
// verifies ADR-072 D7: exactly one instruction file per mount is used, chosen
// deterministically (CLAUDE.md wins when both exist), and multiple mounts'
// contributions are ordered deterministically by mount name — not by input
// order — matching FR-042 and FR-044.
// Traces to: spec scenario "Only one instruction file per mount is used".
func TestProjectInstructions_SingleFilePerMountDeterministic(t *testing.T) {
	t.Run("CLAUDE.md wins when both are present", func(t *testing.T) {
		mountRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "CLAUDE.md"), []byte("claude instructions"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "AGENTS.md"), []byte("agents instructions"), 0o600))

		content, fileName, ok := SelectProjectInstructionFile(mountRoot)
		require.True(t, ok)
		assert.Equal(t, "CLAUDE.md", fileName)
		assert.Equal(t, "claude instructions", content)

		// Deterministic across repeated calls.
		content2, fileName2, ok2 := SelectProjectInstructionFile(mountRoot)
		require.True(t, ok2)
		assert.Equal(t, fileName, fileName2)
		assert.Equal(t, content, content2)
	})

	t.Run("AGENTS.md used when CLAUDE.md is absent", func(t *testing.T) {
		mountRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "AGENTS.md"), []byte("agents only"), 0o600))

		content, fileName, ok := SelectProjectInstructionFile(mountRoot)
		require.True(t, ok)
		assert.Equal(t, "AGENTS.md", fileName)
		assert.Equal(t, "agents only", content)
	})

	t.Run("neither file present: contributes nothing", func(t *testing.T) {
		mountRoot := t.TempDir()
		_, _, ok := SelectProjectInstructionFile(mountRoot)
		assert.False(t, ok)
	})

	t.Run("multiple mounts ordered by mount name, not input order", func(t *testing.T) {
		betaRoot := t.TempDir()
		alphaRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(betaRoot, "CLAUDE.md"), []byte("beta says hi"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(alphaRoot, "CLAUDE.md"), []byte("alpha says hi"), 0o600))

		composed, truncated := ComposeProjectInstructions([]ProjectInstructionMount{
			{Name: "beta", Root: betaRoot},
			{Name: "alpha", Root: alphaRoot},
		})
		require.False(t, truncated)
		alphaIdx := strings.Index(composed, "alpha says hi")
		betaIdx := strings.Index(composed, "beta says hi")
		require.True(t, alphaIdx >= 0 && betaIdx >= 0)
		assert.Less(t, alphaIdx, betaIdx, "alpha sorts before beta and must appear first, deterministically")
		assert.Contains(t, composed, "alpha")
		assert.Contains(t, composed, "beta")
	})
}

// TestProjectInstructions_TruncationIsMarked (test 17) verifies ADR-072 D7's
// byte cap (262144, MaxInstructionsBytes) applies across the WHOLE composed
// mounts block, not per file, and that oversized content is cut with a
// VISIBLE marker — never silently truncated.
// Traces to: spec scenario "Oversized composed instructions truncate visibly", Dataset D rows 3-5.
func TestProjectInstructions_TruncationIsMarked(t *testing.T) {
	t.Run("exactly at the budget: included whole, not marked", func(t *testing.T) {
		mountRoot := t.TempDir()
		// Account for the "### <name>\n\n" header this composer adds.
		header := "### m\n\n"
		body := strings.Repeat("a", MaxInstructionsBytes-len(header))
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "CLAUDE.md"), []byte(body), 0o600))

		composed, truncated := ComposeProjectInstructions([]ProjectInstructionMount{{Name: "m", Root: mountRoot}})
		require.False(t, truncated)
		assert.Len(t, composed, MaxInstructionsBytes)
		assert.NotContains(t, composed, "truncated")
	})

	t.Run("one byte over the budget: truncated and marked", func(t *testing.T) {
		mountRoot := t.TempDir()
		body := strings.Repeat("a", MaxInstructionsBytes+1)
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "CLAUDE.md"), []byte(body), 0o600))

		composed, truncated := ComposeProjectInstructions([]ProjectInstructionMount{{Name: "m", Root: mountRoot}})
		require.True(t, truncated)
		assert.Contains(t, composed, "truncated", "a visible marker must state that truncation occurred")
		assert.LessOrEqual(t, len(composed), MaxInstructionsBytes)
	})

	t.Run("three mounts summing over the budget: cut at budget, marked", func(t *testing.T) {
		roots := []string{t.TempDir(), t.TempDir(), t.TempDir()}
		names := []string{"m1", "m2", "m3"}
		var mounts []ProjectInstructionMount
		for i, root := range roots {
			require.NoError(t, os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(strings.Repeat("b", MaxInstructionsBytes/2)), 0o600))
			mounts = append(mounts, ProjectInstructionMount{Name: names[i], Root: root})
		}

		composed, truncated := ComposeProjectInstructions(mounts)
		require.True(t, truncated, "three mounts each near half the budget must sum past it")
		assert.Contains(t, composed, "truncated")
		assert.LessOrEqual(t, len(composed), MaxInstructionsBytes)
	})

	t.Run("no instruction file anywhere: no block, not truncated", func(t *testing.T) {
		composed, truncated := ComposeProjectInstructions([]ProjectInstructionMount{{Name: "m", Root: t.TempDir()}})
		assert.Empty(t, composed)
		assert.False(t, truncated)
	})

	t.Run("unreadable file contributes nothing, turn proceeds", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root ignores file permission bits")
		}
		mountRoot := t.TempDir()
		path := filepath.Join(mountRoot, "CLAUDE.md")
		require.NoError(t, os.WriteFile(path, []byte("secret instructions"), 0o600))
		require.NoError(t, os.Chmod(path, 0o000))
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

		composed, truncated := ComposeProjectInstructions([]ProjectInstructionMount{{Name: "m", Root: mountRoot}})
		assert.Empty(t, composed, "an unreadable instruction file must contribute nothing, not fail the composition")
		assert.False(t, truncated)
	})
}
