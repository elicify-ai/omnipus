// Omnipus — active-skill turn assembly tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTurnAssembly_NoSkillBodiesWhenNoneActive (test 33) — ADR-072 D1's
// headline claim: with no skill activated for this turn (no explicit
// /<skill> command, no Skill-tool load this turn), the assembled system
// message carries no "# Active Skills" block and no skill body at all — only
// the "# Skills" menu of slug/name/description, which costs nothing until a
// skill is actually invoked.
func TestTurnAssembly_NoSkillBodiesWhenNoneActive(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	skillDir := filepath.Join(tmpDir, "skills", "release-notes")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
		"---\nname: release-notes\ndescription: Use when drafting release notes\n---\n\nSKILL-BODY-MARKER\n"),
		0o644))

	cb := NewContextBuilder(tmpDir).WithSkillAllowlist([]string{"release-notes"})

	msgs := cb.BuildMessages(0, nil, "hello", nil, "", "test", "chat1", "user1", "Alice", "", nil)
	require.NotEmpty(t, msgs)
	require.Equal(t, "system", msgs[0].Role)

	assert.NotContains(t, msgs[0].Content, "# Active Skills",
		"no skill was activated for this turn, so no Active Skills block should be emitted")
	assert.NotContains(t, msgs[0].Content, "SKILL-BODY-MARKER",
		"a granted-but-not-activated skill's body must never enter the turn")
}

// TestTurnAssembly_LoadedSkillPresentThisTurnOnly (test 34) — a skill
// activated for one turn via BuildMessages' activeSkills variadic (the
// explicit /<slug> activation path, opts.ForcedSkills) appears in THAT
// turn's system message and nowhere else; a subsequent call on the same
// ContextBuilder with no activeSkills carries no trace of it. This covers
// explicit per-turn activation only: the Skill TOOL does not use this path —
// its loaded body is an ordinary tool message kept in the session window.
func TestTurnAssembly_LoadedSkillPresentThisTurnOnly(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	skillDir := filepath.Join(tmpDir, "skills", "release-notes")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
		"---\nname: release-notes\ndescription: Use when drafting release notes\n---\n\nSKILL-BODY-MARKER\n"),
		0o644))

	cb := NewContextBuilder(tmpDir).WithSkillAllowlist([]string{"release-notes"})

	activatedMsgs := cb.BuildMessages(0, nil, "draft the notes", nil, "", "test", "chat1", "user1", "Alice", "", nil, "release-notes")
	require.NotEmpty(t, activatedMsgs)
	assert.Contains(t, activatedMsgs[0].Content, "# Active Skills")
	assert.Contains(t, activatedMsgs[0].Content, "SKILL-BODY-MARKER",
		"the loaded skill's body must be present on the turn that activated it")

	laterMsgs := cb.BuildMessages(0, nil, "what else can you do", nil, "", "test", "chat1", "user1", "Alice", "", nil)
	require.NotEmpty(t, laterMsgs)
	assert.NotContains(t, laterMsgs[0].Content, "# Active Skills",
		"a later turn with no activeSkills must carry no Active Skills block")
	assert.NotContains(t, laterMsgs[0].Content, "SKILL-BODY-MARKER",
		"the previously-loaded skill's body must not leak into a turn that did not request it")
}
