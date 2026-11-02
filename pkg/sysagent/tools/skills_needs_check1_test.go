package systools_test

// CHECK1 CK6 and CK18 (skills spec r15 FR-003, §5.7) seen from get_skill.
//
// CK6: metadata.omnipus-requires entries that are not plain program names are
// "declared, not checked" — get_skill's `needs` (status present|missing, C-7)
// never reports them missing, and the checked rows keep their status.
// CK18: `needs` is checked over the PATH the agent's bash gets (the turn
// context's agent-bash PATH), not the gateway process PATH.
//
// Failability: against the code before commit 380dec186 the "pypdf>=4" entry
// is looked up and reported as a missing program row (CK6 test fails); against
// the code before be757e7bf get_skill used os.Getenv("PATH"), so a program
// only on the agent's bash PATH is reported missing (CK18 test fails).

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func needsFixture(t *testing.T, requires string) (*systools.Deps, string) {
	t.Helper()
	global := t.TempDir()
	dir := filepath.Join(global, "needs-skill")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	md := "---\nname: needs-skill\ndescription: declares requirements\nmetadata:\n  omnipus-requires: " + requires + "\n---\n\nBody.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))
	deps := newSkillDeps(t, global, skillDepsOpts{
		agentID:  "agent-a",
		grants:   []string{"needs-skill"},
		policies: map[string]string{"get_skill": "allow"},
	})
	return deps, dir
}

func needsRows(t *testing.T, res *tools.ToolResult) map[string]string {
	t.Helper()
	m := parseToolJSON(t, res.ForLLM)
	require.NotContains(t, m, "error", "get_skill must succeed: %s", res.ForLLM)
	raw, ok := m["needs"].([]any)
	require.True(t, ok, "needs is a list: %s", res.ForLLM)
	rows := map[string]string{}
	for _, r := range raw {
		row, isMap := r.(map[string]any)
		require.True(t, isMap)
		program, _ := row["program"].(string)
		status, _ := row["status"].(string)
		rows[program] = status
	}
	return rows
}

func TestGetSkillNeeds_NonProgramDeclarationsAreNotReportedMissing(t *testing.T) {
	deps, _ := needsFixture(t, `'sh "pypdf>=4" ck6-missing-prog pkg>=2'`)
	res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-a"), map[string]any{
		"id": "needs-skill", "fields": []any{"needs"},
	})
	rows := needsRows(t, res)
	assert.Equal(t, "missing", rows["ck6-missing-prog"], "a plain program name that is absent is still checked and missing")
	if runtime.GOOS != "windows" {
		if _, err := os.Stat("/bin/sh"); err == nil {
			assert.Equal(t, "present", rows["sh"], "a plain program name on PATH is present")
		}
	}
	for program := range rows {
		assert.NotContains(t, program, ">=", "a version-constraint declaration is not a checked program row")
		assert.NotContains(t, program, "pypdf", "a quoted declaration is not a checked program row")
	}
}

func TestGetSkillNeeds_UsesTheAgentBashPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bit fixture is POSIX")
	}
	deps, _ := needsFixture(t, "ck18-only-in-bash-path")
	bin := t.TempDir()
	prog := filepath.Join(bin, "ck18-only-in-bash-path")
	require.NoError(t, os.WriteFile(prog, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	args := map[string]any{"id": "needs-skill", "fields": []any{"needs"}}

	t.Run("without the agent bash PATH the program is missing", func(t *testing.T) {
		rows := needsRows(t, systools.NewGetSkillTool(deps).Execute(readCtx("agent-a"), args))
		assert.Equal(t, "missing", rows["ck18-only-in-bash-path"], "control: the program is on no ambient PATH")
	})

	t.Run("a program on the agent bash PATH is present", func(t *testing.T) {
		ctx := tools.WithAgentBashPATH(readCtx("agent-a"), func(_ context.Context) string { return bin + string(os.PathListSeparator) + "/usr/bin" })
		rows := needsRows(t, systools.NewGetSkillTool(deps).Execute(ctx, args))
		assert.Equal(t, "present", rows["ck18-only-in-bash-path"], "needs is checked over the PATH the agent's bash gets")
	})

	t.Run("a program only on the gateway process PATH is not reported present", func(t *testing.T) {
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		ctx := tools.WithAgentBashPATH(readCtx("agent-a"), func(_ context.Context) string { return "/usr/bin" })
		rows := needsRows(t, systools.NewGetSkillTool(deps).Execute(ctx, args))
		assert.Equal(t, "missing", rows["ck18-only-in-bash-path"], "the gateway process PATH is not what the agent's bash gets")
	})
}
