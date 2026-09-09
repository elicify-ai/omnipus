package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMountAdd_ThresholdWarnsAndStillCreates (test 51e, ADR-072 T8c, D1.2)
// verifies the mount-add-time threshold: a mount whose recognised skills
// directory would contribute more skills than the configured threshold
// produces a warning stating the count and its per-turn consequence — but
// this is advisory only. It is not a refusal (nothing in this package's API
// returns an error or blocks anything), and per FR-076 the disclosure itself
// makes no claim of capping the menu — that stays a separate, later
// integration's job to leave uncapped.
// Traces to: spec scenario "A mount contributing an implausible number of skills warns at creation" (FR-074, FR-075, FR-076).
func TestMountAdd_ThresholdWarnsAndStillCreates(t *testing.T) {
	mountRoot := t.TempDir()
	skillsDir := filepath.Join(mountRoot, ".claude", "skills")
	const n = 5000
	for i := 0; i < n; i++ {
		writeSkillFile(t, skillsDir, fmt.Sprintf("skill-%04d", i), "Use when doing thing")
	}

	disclosure := EvaluateMountSkillsDisclosure("monorepo", mountRoot, DefaultMountSkillsWarnThreshold)

	require.Equal(t, n, disclosure.Count, "the disclosure must report the true discovered count")
	require.NotEmpty(t, disclosure.ThresholdWarning, "exceeding the threshold must produce a warning")
	assert.Contains(t, disclosure.ThresholdWarning, "5000", "the warning must state the count")
	assert.NotEmpty(t, disclosure.GrantsMessage, "the grants disclosure is independent of the threshold and must still be present")

	// A caller-supplied lower threshold is honored (an operator-tunable value).
	small := EvaluateMountSkillsDisclosure("monorepo", mountRoot, 10)
	assert.NotEmpty(t, small.ThresholdWarning)

	// Below the default threshold: no warning, but the count is still correct.
	fewRoot := t.TempDir()
	writeSkillFile(t, filepath.Join(fewRoot, ".claude", "skills"), "only-one", "Use when only-one")
	few := EvaluateMountSkillsDisclosure("small-repo", fewRoot, DefaultMountSkillsWarnThreshold)
	assert.Equal(t, 1, few.Count)
	assert.Empty(t, few.ThresholdWarning, "a mount well under the threshold must not warn")
}

// TestMountAdd_FirstSkillsDirDisclosesInstructionGrant (test 51f, MAJ-004,
// FR-074a) verifies the disclosure that is INDEPENDENT of the count
// threshold: any time a mount is found to carry a recognised skills
// directory — even one with only three skills, far below the warning
// threshold — the operator must be told plainly that those skills become
// auto-loadable agent instructions, not merely files sitting in the repo.
// Traces to: spec scenario "A mount's first skills directory discloses what it grants" (FR-074a).
func TestMountAdd_FirstSkillsDirDisclosesInstructionGrant(t *testing.T) {
	mountRoot := t.TempDir()
	skillsDir := filepath.Join(mountRoot, ".claude", "skills")
	writeSkillFile(t, skillsDir, "one", "Use when one")
	writeSkillFile(t, skillsDir, "two", "Use when two")
	writeSkillFile(t, skillsDir, "three", "Use when three")

	disclosure := EvaluateMountSkillsDisclosure("tiny-repo", mountRoot, DefaultMountSkillsWarnThreshold)

	require.Equal(t, 3, disclosure.Count)
	assert.Empty(t, disclosure.ThresholdWarning, "3 is far below the default threshold of 500")
	require.NotEmpty(t, disclosure.GrantsMessage, "the grants disclosure must appear even for a tiny, well-under-threshold mount")
	assert.Contains(t, disclosure.GrantsMessage, "instructions",
		"the disclosure must say what these ARE — auto-loadable agent instructions — not merely that files exist")
}

// TestMountAdd_NoSkillsDirDisclosesNothing verifies the FR-039 companion
// case: a mount with no recognised skills directory produces an empty
// disclosure — no count, no grants message, no warning, and (per D6) no
// error either.
func TestMountAdd_NoSkillsDirDisclosesNothing(t *testing.T) {
	mountRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "README.md"), []byte("just a readme"), 0o600))

	disclosure := EvaluateMountSkillsDisclosure("plain-repo", mountRoot, DefaultMountSkillsWarnThreshold)
	assert.Equal(t, 0, disclosure.Count)
	assert.Empty(t, disclosure.GrantsMessage)
	assert.Empty(t, disclosure.ThresholdWarning)
}
