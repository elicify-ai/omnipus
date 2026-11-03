package tools

// UAT run 2 R2-1 / F-10, guard layer: with the operator's edit_skill Deny
// persisted (the real gateway path), the path resolver refuses a write into a
// skill folder the agent cannot load. The loop decides "this is a skill write"
// (and so answers skill_edit_denied with its audit entry) from
// SkillWriteTarget; it must still recognise such a path as a skill write
// instead of falling through to the tool's protected-internal-file refusal.
// The loop-harness test (pkg/agent skills_uat1_f10_edit_denied_test.go) sets
// edit_skill Deny only in memory, so the persisted turn policy there still
// admits every skill and does not reach this gap.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSkillWriteTarget_PersistedEditSkillDeny_StillNamesTheSkill(t *testing.T) {
	f := f8Setup(t) // persisted agent "self": f8-granted granted, edit_skill Deny
	cases := map[string]struct {
		path string
		key  string
	}{
		"granted existing skill": {filepath.Join(f.granted, "notes.md"), "f8-granted"},
		"ungranted installed":    {filepath.Join(f.other, "notes.md"), "f8-other"},
		"absent skill (create)":  {filepath.Join(f.home, "skills", "brand-new", "SKILL.md"), "brand-new"},
	}
	for _, restrict := range []bool{true, false} {
		targeters := map[string]SkillWriteTargeter{
			"write_file": NewWriteFileTool(f.agentHome, restrict),
			"edit_file":  NewEditFileTool(f.agentHome, restrict),
		}
		for toolName, targeter := range targeters {
			for name, tc := range cases {
				t.Run(toolName+"/"+name, func(t *testing.T) {
					key, ok := targeter.SkillWriteTarget(f8Ctx(), map[string]any{"path": tc.path})
					assert.True(t, ok, "a write into %s is a skill write (restrict=%v)", tc.path, restrict)
					assert.Equal(t, tc.key, key)
				})
			}
		}
	}
	// Control: a path outside the skills tree is not a skill write.
	key, ok := NewWriteFileTool(f.agentHome, true).SkillWriteTarget(f8Ctx(), map[string]any{"path": filepath.Join(f.agentHome, "notes.md")})
	assert.False(t, ok, "control: %q", key)
}
