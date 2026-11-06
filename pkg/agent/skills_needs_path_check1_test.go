package agent

// CHECK1 CK18 (skills spec r15 §5.7): the Skill load header's `needs` line is
// checked over the PATH the agent's bash child gets, not the gateway process
// PATH. The agent's bash PATH carries the workspace runtime layer
// (<workspace>/.omnipus/env/bin) in front of the scrubbed inherited tail.
//
// Failability: before commit be757e7bf the load passed os.Getenv("PATH"), so a
// program published only in the workspace runtime layer read "MISSING"; the
// test fails there.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ck18Fixture(t *testing.T, program string) (al *AgentLoop, inst *AgentInstance, resolved skills.ResolvedSkill, workspaceDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("executable bit fixture is POSIX")
	}
	home := filepath.Join(t.TempDir(), "omnipus")
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	root := filepath.Join(home, "skills")
	_, err := skills.ApplyChange(root, skills.ChangeRequest{
		Kind: skills.KindCreate, Slug: "needs-path",
		Content: []byte("---\nname: needs-path\ndescription: path requirement\nmetadata:\n  omnipus-requires: " + program + "\n---\n\nBODY\n"),
		Expect:  skills.Expectation{Absent: true},
		Actor:   skills.ChangeActor{Kind: "person", Tool: "rest-create"},
	})
	require.NoError(t, err)
	provider := testutil.NewScenario().WithText("unused")
	al = ck2Loop(t, home, []string{"needs-path"}, provider)
	got, ok := al.registry.GetAgent("mia")
	require.True(t, ok)
	_, hasBash := got.Tools.Get("bash")
	require.True(t, hasBash, "positive control: the agent has the bash tool whose PATH the check must follow")
	workspaceDir = t.TempDir()
	resolved = skills.ResolvedSkill{Slug: "needs-path", Shelf: skills.ShelfRegistry, Path: filepath.Join(root, "needs-path", "SKILL.md")}
	return al, got, resolved, workspaceDir
}

func ck18Load(t *testing.T, al *AgentLoop, inst *AgentInstance, resolved skills.ResolvedSkill, workspaceDir string) string {
	t.Helper()
	ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "mia"), "ck18-session")
	ctx = tools.WithTurnWorkspaceDir(ctx, workspaceDir)
	out := al.loadSkillActivation(ctx, inst, "mia", "", resolved)
	require.Equal(t, tools.SkillLoadLoaded, out.Status, "the load must succeed: %s", out.Message)
	return out.Content
}

func TestSkillLoadNeeds_ChecksTheAgentBashPATHNotTheGatewayPATH(t *testing.T) {
	al, inst, resolved, workspaceDir := ck18Fixture(t, "ck18-ws-only")

	// Control: before the runtime layer exists the program is missing.
	assert.Contains(t, ck18Load(t, al, inst, resolved, workspaceDir), "ck18-ws-only — MISSING")

	bin := filepath.Join(workspaceDir, ".omnipus", "env", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "ck18-ws-only"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	header := ck18Load(t, al, inst, resolved, workspaceDir)
	assert.Contains(t, header, "ck18-ws-only — present (", "a program published in the workspace runtime layer is on the agent bash PATH")
	assert.NotContains(t, header, "ck18-ws-only — MISSING")
}
