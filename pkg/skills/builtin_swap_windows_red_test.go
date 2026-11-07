//go:build windows

package skills

// RED tests — landing/skills (T9): S-12.1, the Windows pipeline leg (spec
// §17.1 row 26, T-W1, Windows CI). PLATFORM-GATED: this pod is Linux; the
// file compiles and runs only on Windows CI. Native Windows RED is NOT
// established by source inspection or a Linux cross-compile. The implementing
// worker must retain the native Windows failing-run receipt; until then that
// execution claim is UNVERIFIED.
// Traces to: skills-spec-r15.md S-12.1, §17.1 row 26.
//
// BDD (S-12.1): on Windows, the §7.1 swap (skills/.swap/<slug> → live) and
// the whole pipeline are what actually run on a real Windows volume —
// rename-after-replace semantics, no POSIX atomicity guarantees, path
// separators — and every scenario S-1.1…S-11.19 assertion that touches the
// filesystem must hold there via the same ApplyChange path.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWindowsPipeline_SwapSemantics (S-12.1).
// BDD: on Windows, an edit through ApplyChange commits (swap-in works with
// Windows rename semantics), the committed state round-trips, and the
// refusal paths (built-in write) behave identically.
// Traces to: skills-spec-r15.md S-12.1.
func TestWindowsPipeline_SwapSemantics(t *testing.T) {
	root := t.TempDir()
	dir := writeSkillFixture(t, root, "foo")

	// A committed edit round-trips through the Windows swap.
	out, v1 := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "win v1", "windows body"),
		readLiveRevision(t, dir))
	require.Equal(t, PersistenceComplete, out.PersistenceStatus, "the edit commits on Windows")
	body, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, body, "windows body", "the swapped-in bytes are live")

	// Restore round-trips through the same path.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v1,
		Expect: Expectation{Revision: readLiveRevision(t, dir)},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err, "restore works on Windows")

	// The built-in refusal is identical.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "plan"), 0o755), "seed the built-in folder")
	require.NoError(t, os.WriteFile(filepath.Join(root, "plan", "SKILL.md"),
		[]byte(validSkillMD("plan", "win builtin", "b")), 0o644))
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "plan", Path: "SKILL.md",
		Content: []byte(validSkillMD("plan", "win edited", "x")),
		Expect:  Expectation{Revision: readLiveRevision(t, filepath.Join(root, "plan"))},
		Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
	})
	require.Error(t, err, "built-ins are read-only on Windows too")
	assert.True(t, errors.Is(err, ErrBuiltinReadOnly), "the refusal carries BUILTIN_READ_ONLY")
}
