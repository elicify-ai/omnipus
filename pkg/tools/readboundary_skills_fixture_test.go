package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// newRBSkillsFixture extends only the migrated skills row of the #920 pack.
// r15 §6.1 supersedes its instruction-leaf gate with whole-folder authority.
// The original workspace/mount/audit registry stays real; generation binding
// and editor permission come from real persisted agent/config records.
func newRBSkillsFixture(t *testing.T, granted, editor bool) (*rbFixture, string, string) {
	t.Helper()
	f := newRBFixture(t)
	root := filepath.Join(f.home, "skills")
	demo := filepath.Join(root, "demo")
	rbWrite(t, filepath.Join(demo, "SKILL.md"), "---\nname: demo\ndescription: Read-boundary fixture.\n---\nneedle-instruction-SKILL.md\n")
	for _, leaf := range []string{"AGENT.md", "AGENTS.md"} {
		rbWrite(t, filepath.Join(demo, leaf), "needle-instruction-"+leaf+"\n")
	}
	rbWrite(t, filepath.Join(demo, "scripts", "needle-helper.py"), "print('needle-helper-content')\n")
	for _, control := range []string{".staging", ".swap", "invalid.name"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, control), 0o700))
	}
	a := config.AgentConfig{ID: rbAgentID, Home: filepath.Join(f.home, "agents", rbAgentID), Tools: &config.AgentToolsCfg{}}
	a.Tools.Builtin.Policies = map[string]config.ToolPolicy{"edit_skill": config.ToolPolicyDeny}
	if editor {
		a.Tools.Builtin.Policies["edit_skill"] = config.ToolPolicyAsk
	}
	if granted {
		a.Skills = []string{"demo"}
	}
	store := agentstore.New(f.home)
	require.NoError(t, store.Create(a.ID, &a))
	stored, err := store.Get(a.ID)
	require.NoError(t, err)
	if granted {
		stamp, readErr := os.ReadFile(filepath.Join(demo, ".omnipus-identity"))
		require.NoError(t, readErr)
		require.NotEmpty(t, strings.TrimSpace(string(stamp)))
		require.Equal(t, strings.TrimSpace(string(stamp)), stored.SkillGrants["demo"], "real persist supplies the matching grant")
	} else {
		require.Empty(t, stored.SkillGrants, "editor read authority must not fabricate a load grant")
	}
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{*stored}
	require.NoError(t, config.SaveConfig(filepath.Join(f.home, "config.json"), cfg))
	return f, root, demo
}
