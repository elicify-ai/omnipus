package tools

// UAT-1 F-8 (skills spec r15 §1 item 2, §6.1 item 5, S-1.5): bash may run and
// read the files of a skill the agent can load, and may not change any skill
// path. The path guard used to refuse every command naming the skills folder
// (protected_internal_file) while the load header says "run scripts with bash".
//
// Real policy: the loadable folder comes from a persisted agent record, a
// stamped folder and a bound grant, through ResolveTurnFSPolicy, never from
// hand-set HomeGuard fields. Text-level only (no child process runs).
//
// Spec gap recorded in the ledger: §6.1 item 5 does not give the refusal
// text for a write-shaped operand; the tests assert that it names the skill
// folder and is not the generic protected-internal-file text.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

type f8Fixture struct {
	home, agentHome, granted, other string
}

func f8Setup(t *testing.T) f8Fixture {
	t.Helper()
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)
	f := f8Fixture{home: home, agentHome: filepath.Join(home, "agents", "self")}
	for _, slug := range []string{"f8-granted", "f8-other"} {
		dir := filepath.Join(home, "skills", slug)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "scripts"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+slug+"\ndescription: f8\n---\n\nBody.\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "x.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	}
	for _, control := range []string{".staging", ".swap"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, "skills", control, "f8-granted"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(home, "skills", control, "f8-granted", "SKILL.md"), []byte("x"), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(home, "system", "skill-history", "f8-granted", "versions"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "system", "skill-history", "f8-granted", "versions", "v1.json"), []byte("{}"), 0o644))
	realTurnPolicy(t, home, "f8-granted", false) // persists agent "self" granted f8-granted, bound to a stamp
	f.granted = filepath.Join(home, "skills", "f8-granted")
	f.other = filepath.Join(home, "skills", "f8-other")
	return f
}

func f8Tool(t *testing.T, f f8Fixture, restrict bool) *ExecTool {
	t.Helper()
	tool, err := NewExecTool(f.agentHome, restrict)
	require.NoError(t, err)
	return tool
}

func f8Ctx() context.Context { return WithAgentID(context.Background(), "self") }

func TestShellGuard_LoadableSkillFolderRunsAndReads(t *testing.T) {
	f := f8Setup(t)
	for _, restrict := range []bool{true, false} {
		tool := f8Tool(t, f, restrict)
		for _, cmd := range []string{
			"sh " + f.granted + "/scripts/x.sh",
			"cat " + f.granted + "/SKILL.md",
			"cd " + f.granted + " && ./scripts/x.sh",
		} {
			name := cmd
			t.Run(name, func(t *testing.T) {
				msg, protected := tool.guardProtectedOperands(f8Ctx(), cmd)
				assert.Empty(t, msg, "the unconditional operand leg lets a loadable skill's files be run and read")
				assert.False(t, protected)
				assert.Empty(t, tool.guardCommand(f8Ctx(), cmd, f.agentHome), "the whole guard (restrict=%v) allows it", restrict)
			})
		}
	}
}

func TestShellGuard_WriteShapedOperandIntoASkillIsRefusedWithASkillMessage(t *testing.T) {
	f := f8Setup(t)
	for _, restrict := range []bool{true, false} {
		tool := f8Tool(t, f, restrict)
		for _, cmd := range []string{
			"echo x >> " + f.granted + "/SKILL.md",
			"rm -rf " + f.granted,
			"cp a " + f.granted + "/n",
		} {
			t.Run(cmd, func(t *testing.T) {
				msg := tool.guardCommand(f8Ctx(), cmd, f.agentHome)
				require.NotEmpty(t, msg, "a write-shaped operand into a skill is refused (restrict=%v)", restrict)
				assert.Contains(t, strings.ToLower(msg), "skill", "the refusal names the skill folder, not only internal files")
				assert.NotContains(t, msg, "protected internal files", "not the generic protected-internal-file text")
			})
		}
	}
}

func TestShellGuard_EveryFormIsRefusedForWhatTheAgentCannotLoad(t *testing.T) {
	f := f8Setup(t)
	home := f.home
	targets := map[string]string{
		"not granted":   f.other,
		".staging":      filepath.Join(home, "skills", ".staging", "f8-granted"),
		".swap":         filepath.Join(home, "skills", ".swap", "f8-granted"),
		"skill-history": filepath.Join(home, "system", "skill-history", "f8-granted"),
	}
	forms := func(folder string) []string {
		return []string{
			"sh " + folder + "/scripts/x.sh",
			"cat " + folder + "/SKILL.md",
			"cd " + folder + " && ./scripts/x.sh",
			"echo x >> " + folder + "/SKILL.md",
			"rm -rf " + folder,
			"cp a " + folder + "/n",
		}
	}
	for _, restrict := range []bool{true, false} {
		tool := f8Tool(t, f, restrict)
		for name, folder := range targets {
			for _, cmd := range forms(folder) {
				t.Run(name+"/"+cmd, func(t *testing.T) {
					assert.NotEmpty(t, tool.guardCommand(f8Ctx(), cmd, f.agentHome), "refused (restrict=%v)", restrict)
					msg, _ := tool.guardProtectedOperands(f8Ctx(), cmd)
					assert.NotEmpty(t, msg, "the unconditional leg refuses it too")
				})
			}
		}
	}
}

func TestShellGuard_NoTurnPolicyRefusesEverySkillForm(t *testing.T) {
	f := f8Setup(t)
	tool := f8Tool(t, f, true)
	for _, cmd := range []string{
		"sh " + f.granted + "/scripts/x.sh",
		"cat " + f.granted + "/SKILL.md",
		"echo x >> " + f.granted + "/SKILL.md",
		"rm -rf " + f.granted,
	} {
		t.Run(cmd, func(t *testing.T) {
			// No agent in the context: there is no turn authority, so nothing is loadable.
			msg, _ := tool.guardProtectedOperands(context.Background(), cmd)
			assert.NotEmpty(t, msg, "fail closed without a turn policy")
		})
	}
}
