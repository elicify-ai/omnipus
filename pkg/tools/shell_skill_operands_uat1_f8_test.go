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

// Security review 2 (/workspace/omnipus-coordination/designs-20261029/
// skills-security-report-2.md). Text-level, real turn policy. "Refused" means
// the path guard returns a message; "asked" means the no-sandbox gate does not
// treat the command as a read. Nothing runs, so the fixture file is also
// checked for unchanged bytes.

func sec2Bytes(t *testing.T, f f8Fixture) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.granted, "SKILL.md"))
	require.NoError(t, err)
	return string(raw)
}

// SEC2-01: rg runs an arbitrary command per file with --pre, so it is not a
// proven read.
func TestShellGuardSEC2_RgPreprocessorIsNotAReadOfASkill(t *testing.T) {
	f := f8Setup(t)
	before := sec2Bytes(t, f)
	tool := f8Tool(t, f, true)
	folders := []string{f.granted}
	target := f.granted + "/SKILL.md"
	for _, cmd := range []string{
		"rg --pre=rm x " + target,
		"rg --pre rm x " + target,
		"rg --pre-glob '*.md' --pre rm x " + target,
	} {
		t.Run(cmd, func(t *testing.T) {
			msg, _ := tool.guardProtectedOperands(f8Ctx(), cmd)
			assert.NotEmpty(t, msg, "the path guard refuses an rg preprocessor on a skill file")
			assert.False(t, commandIsNoSandboxReadOnlyFor(cmd, folders), "the no-sandbox gate asks for it")
		})
	}
	assert.Equal(t, before, sec2Bytes(t, f), "the skill file is unchanged")
}

// SEC2-02: a glob that starts with the granted folder's name also names
// ungranted siblings.
func TestShellGuardSEC2_GlobBesideAnUngrantedSiblingIsRefused(t *testing.T) {
	f := f8Setup(t)
	private := filepath.Join(f.home, "skills", "f8-granted-private")
	require.NoError(t, os.MkdirAll(private, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(private, "SKILL.md"), []byte("---\nname: f8-granted-private\ndescription: private\n---\nSECRET\n"), 0o644))
	skillsDir := filepath.Join(f.home, "skills")
	tool := f8Tool(t, f, true)
	folders := []string{f.granted}

	// Control: the plain granted path is still a read.
	msg, _ := tool.guardProtectedOperands(f8Ctx(), "cat "+f.granted+"/SKILL.md")
	require.Empty(t, msg, "control: a plain granted path is allowed")

	for _, cmd := range []string{
		"cat " + skillsDir + "/f8-granted*/SKILL.md",
		"cat " + skillsDir + "/f8-grante?/SKILL.md",
		"cat " + skillsDir + "/f8-granted[-]private/SKILL.md",
		"cat " + skillsDir + "/f8-g*/SKILL.md",
	} {
		t.Run(cmd, func(t *testing.T) {
			msg, _ := tool.guardProtectedOperands(f8Ctx(), cmd)
			assert.NotEmpty(t, msg, "the path guard refuses a glob-bearing skill operand")
			assert.False(t, commandIsNoSandboxReadOnlyFor(cmd, folders), "the no-sandbox gate asks for it")
		})
	}
}

// SEC2-03: after a cd into a skill folder, a later relative operand or a
// redirect is a change inside that folder, which a text guard must not admit.
// Whether `cd <skill> && ls` or `cat SKILL.md` may stay allowed depends on the
// developer proving them read-only; they are not asserted here (the F-8 shape
// `cd <skill> && ./scripts/x.sh` stays allowed, see the loadable-folder test).
func TestShellGuardSEC2_ChangeAfterCdIntoASkillIsRefused(t *testing.T) {
	f := f8Setup(t)
	before := sec2Bytes(t, f)
	tool := f8Tool(t, f, true)
	folders := []string{f.granted}
	for _, cmd := range []string{
		"cd " + f.granted + " && chmod 000 SKILL.md",
		"cd " + f.granted + " && printf x > SKILL.md",
		"cd " + f.granted + " && rm SKILL.md",
		"cd " + f.granted + " && cp a n",
		"cd " + f.granted + " && echo x >> SKILL.md",
	} {
		t.Run(cmd, func(t *testing.T) {
			msg, _ := tool.guardProtectedOperands(f8Ctx(), cmd)
			assert.NotEmpty(t, msg, "the path guard refuses a relative change after a skill cd")
			assert.False(t, commandIsNoSandboxReadOnlyFor(cmd, folders), "the no-sandbox gate asks for it")
		})
	}
	assert.Equal(t, before, sec2Bytes(t, f), "the skill file is unchanged")
}

// SEC3-01 (skills-security-report-3.md): after a literal skill-folder cd, a
// later operand that leaves the folder (equal to or containing a `..` segment)
// reads ungranted siblings, so the path guard refuses it. Reads of the folder's
// own files stay allowed.
func TestShellGuardSEC3_ReadThatLeavesTheSkillFolderAfterCdIsRefused(t *testing.T) {
	f := f8Setup(t)
	private := filepath.Join(f.home, "skills", "f8-granted-private")
	require.NoError(t, os.MkdirAll(private, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(private, "SKILL.md"), []byte("---\nname: f8-granted-private\ndescription: private\n---\nSECRET\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.granted, "notes.md"), []byte("notes\n"), 0o644))
	before := sec2Bytes(t, f)
	tool := f8Tool(t, f, true)

	// Controls: reads inside the folder stay allowed (a positive instrument for
	// the refusals below).
	for _, cmd := range []string{
		"cd " + f.granted + " && cat SKILL.md",
		"cd " + f.granted + " && ls",
		"cd " + f.granted + " && cat ./notes.md",
	} {
		msg, _ := tool.guardProtectedOperands(f8Ctx(), cmd)
		require.Empty(t, msg, "control: %s", cmd)
	}

	for _, cmd := range []string{
		"cd " + f.granted + "; grep -r . ..",
		"cd " + f.granted + " && cat ../f8-granted-private/SKILL.md",
		"cd " + f.granted + " && ls ..",
		"cd " + f.granted + " && grep -r . ../",
		"cd " + f.granted + " && cat ./../f8-granted-private/SKILL.md",
	} {
		t.Run(cmd, func(t *testing.T) {
			msg, _ := tool.guardProtectedOperands(f8Ctx(), cmd)
			assert.NotEmpty(t, msg, "the path guard refuses an operand that leaves the skill folder")
		})
	}
	assert.Equal(t, before, sec2Bytes(t, f), "the skill file is unchanged")
}
