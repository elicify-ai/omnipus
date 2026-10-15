// Omnipus — tests for ADR-072 D3 (reminder) and adjacent turn-assembly
// checks (D1's "no force-loaded skill bodies" headline claim).
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Test names below are pinned by the workflow's traceability step:
// TestReminder_WithinByteBudget (spec test 25),
// TestTurnAssembly_MenuInsideCacheBoundaryReminderOutside (test 35),
// TestTurnAssembly_NoSkillBodiesWhenNoneActive (test 33),
// TestTurnAssembly_LoadedSkillPresentThisTurnOnly (test 34).

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReminder_WithinByteBudget (test 25, FR-013/MIN-001) — the ADR-072 D3
// reminder is <=240 BYTES (len over a string), not tokens. FR-009 requires
// the unchanged reminder in BuildMessages' actual trailing user block and
// outside the system prefix. Check emitted assembly, not an internal renderer
// whose metadata/volatile split may change without changing this contract.
func TestReminder_WithinByteBudget(t *testing.T) {
	byteLen := len(skillReminderNote)
	if byteLen > maxSkillReminderBytes {
		t.Fatalf("skillReminderNote is %d bytes, want <= %d", byteLen, maxSkillReminderBytes)
	}
	if byteLen == 0 {
		t.Fatalf("skillReminderNote must not be empty")
	}

	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)
	cb := NewContextBuilder(tmpDir)

	history := []providers.Message{
		{Role: "user", Content: "earlier request"},
		{Role: "assistant", Content: "earlier reply"},
	}
	msgs := cb.BuildMessages(0, history, "new request", nil, "", "test", "chat1", "user1", "Alice", "", nil)
	require.Len(t, msgs, len(history)+3, "system, unchanged history, current user and one trailing reminder block")
	require.Equal(t, "system", msgs[0].Role)
	assert.Equal(t, history, msgs[1:len(history)+1], "the reminder must not displace or change history")
	assert.Equal(t, providers.Message{Role: "user", Content: "new request"}, msgs[len(msgs)-2],
		"the current request remains immediately before the volatile tail")
	tail := msgs[len(msgs)-1]
	assert.Equal(t, "user", tail.Role, "FR-009: the reminder is in the final user block")
	assert.Contains(t, tail.Content, skillReminderNote, "the emitted tail must contain the bounded reminder verbatim")
	assert.Equal(t, 1, strings.Count(tail.Content, skillReminderNote), "the reminder is emitted once, not duplicated")
	for _, msg := range msgs {
		if msg.Role == "system" {
			assert.NotContains(t, msg.Content, skillReminderNote, "FR-009: the reminder must not be in system Content")
			for _, part := range msg.SystemParts {
				assert.NotContains(t, part.Text, skillReminderNote, "FR-009: the reminder must not be in a system block")
			}
		}
	}
}

// TestTurnAssembly_MenuInsideCacheBoundaryReminderOutside (test 35, D3 part
// 1+2) — the "# Skills" menu stays inside the CACHED static prompt block
// (SystemParts[0], the block carrying CacheControl), while the D3 reminder
// lands in the uncached trailing user block after the conversation, alongside
// the clock (prompt-caching FR-007/009 and regression table).
// The skill menu remains in the stable prefix; the reminder is never system-role.
func TestTurnAssembly_MenuInsideCacheBoundaryReminderOutside(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	skillDir := filepath.Join(tmpDir, "skills", "release-notes")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
		"---\nname: release-notes\ndescription: Use when drafting release notes\n---\n\nBODY\n"),
		0o644))

	cb := NewContextBuilder(tmpDir).WithSkillAllowlist([]string{"release-notes"})

	history := []providers.Message{
		{Role: "user", Content: "earlier question"},
		{Role: "assistant", Content: "earlier answer"},
	}
	msgs := cb.BuildMessages(0, history, "hello", nil, "", "test", "chat1", "user1", "Alice", "", nil)
	require.NotEmpty(t, msgs)
	require.Equal(t, "system", msgs[0].Role)

	staticBlock := msgs[0].SystemParts[0]
	require.NotNil(t, staticBlock.CacheControl, "the static block must carry cache_control so the skill menu stays in the stable prefix")
	assert.Equal(t, "ephemeral", staticBlock.CacheControl.Type)
	assert.Contains(t, staticBlock.Text, "# Skills", "the menu heading must be inside the cached static block")
	assert.Contains(t, staticBlock.Text, "release-notes", "the granted skill's menu entry must be inside the cached static block")
	assert.NotContains(t, staticBlock.Text, skillReminderNote, "the reminder must NOT be inside the cached static block")
	for _, part := range msgs[0].SystemParts {
		assert.NotContains(t, part.Text, skillReminderNote, "the reminder moves behind the conversation; it is not a system block")
	}
	for _, msg := range msgs {
		if msg.Role == "system" {
			assert.NotContains(t, msg.Content, skillReminderNote, "the reminder must not be a system-role message")
		}
	}
	var tail string
	seenHello := false
	for _, msg := range msgs {
		if msg.Content == "hello" {
			seenHello = true
			continue
		}
		if seenHello && msg.Role == "user" {
			tail += msg.Content
		}
	}
	require.True(t, seenHello, "the current user message must be present so the reminder can sit after it")
	assert.Contains(t, tail, skillReminderNote, "the per-turn skill reminder must be a user-role block after the conversation")
	assert.NotContains(t, tail, "release-notes", "the granted skill's menu entry must not be duplicated into the reminder tail")
}

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
