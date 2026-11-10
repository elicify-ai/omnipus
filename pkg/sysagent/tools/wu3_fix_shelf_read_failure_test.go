package systools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// r15 §5.3 retains checked management discovery. The former slug mutation
// fallback is withdrawn (§5.4); removed arguments refuse explicitly rather
// than accidentally mutating an identically named installed skill.
func TestWU3UnreadableTargetMountsRefuseInsteadOfGlobalFallback(t *testing.T) {
	d, ctx, _, target := wu3ProjectTarget(t)
	projectBody := wu3ReadFile(t, filepath.Join(target, "crm", "SKILL.md"))
	// A global copy with the same bytes, hence the same revision.
	globalDir := filepath.Join(d.Home, "skills", "crm")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "SKILL.md"), projectBody, 0o600))
	revision, err := skills.NewSkillWriter(target).SkillRevision("crm")
	require.NoError(t, err)
	globalRevision, err := skills.NewSkillWriter(filepath.Join(d.Home, "skills")).SkillRevision("crm")
	require.NoError(t, err)
	require.Equal(t, revision, globalRevision, "fixture: identical copies share a revision")

	storePath, err := workspace.MountStorePath(d.Home, "sales")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(storePath, []byte("{not json"), 0o600))

	assertRemovedSkillMutationArgs(t, d, ctx, "crm", "sales")
	list := systools.NewSkillListTool(d).Execute(ctx, map[string]any{"scope": "management", "workspace_id": "sales"})
	body := wu3Error(t, list, "READ_FAILED")
	assert.Contains(t, body["message"], "sales")

	assert.Equal(t, projectBody, wu3ReadFile(t, filepath.Join(target, "crm", "SKILL.md")), "project copy untouched")
	assert.Equal(t, projectBody, wu3ReadFile(t, filepath.Join(globalDir, "SKILL.md")), "global copy untouched")
}
