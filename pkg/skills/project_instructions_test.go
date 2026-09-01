package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

	t.Run("multi-byte rune straddling the exact cut boundary: never split", func(t *testing.T) {
		// ADR-072 Finding C: ComposeProjectInstructions used to cut at a raw
		// byte offset with no UTF-8 rune-boundary adjustment. This test
		// engineers a 2-byte rune (π, U+03C0 — 0xCF 0x80) whose SECOND byte
		// lands exactly at the naive (pre-fix) cut offset, so the pre-fix
		// code would have sliced composed[:cut] with 0xCF as the trailing
		// byte — a lone leading byte of a multi-byte sequence, i.e. invalid
		// UTF-8 injected directly into the per-turn prompt block.
		mountRoot := t.TempDir()

		// Reconstruct the exact marker ComposeProjectInstructions appends,
		// to compute the exact raw byte offset it naively cuts at.
		marker := fmt.Sprintf("\n\n[project instructions truncated at %d bytes — content past this point was cut]", MaxInstructionsBytes)
		cutBudget := MaxInstructionsBytes - len(marker)
		header := "### m\n\n"

		pos := cutBudget - len(header) - 1
		require.Greater(t, pos, 0, "test fixture assumption: budget large enough to place the straddling rune")
		body := strings.Repeat("a", pos) + "π" + strings.Repeat("a", 10000)
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "CLAUDE.md"), []byte(body), 0o600))

		composed, truncated := ComposeProjectInstructions([]ProjectInstructionMount{{Name: "m", Root: mountRoot}})
		require.True(t, truncated)
		assert.LessOrEqual(t, len(composed), MaxInstructionsBytes, "truncation must never exceed the byte budget")
		assert.True(t, utf8.ValidString(composed), "truncated output must be valid UTF-8, never split mid-rune")
		assert.Contains(t, composed, "truncated")
		// The straddling rune must be excluded whole, not left half-present:
		// the cut point only ever moves backward past it, it never re-admits
		// a partial encoding of it.
		assert.NotContains(t, composed, "π")
		assert.NotContains(t, composed, "\xcf", "a lone leading byte of the straddling rune must never survive the cut")
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
