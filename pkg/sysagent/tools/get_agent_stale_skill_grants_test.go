package systools_test

// get_agent returns the same skill status the REST agent detail shows:
// `skills` (granted names) plus `stale_skill_grants` (granted names whose
// installed skill no longer matches the grant, via skills.StaleGrants).

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func staleGrantSkill(t *testing.T, home, slug, generation string) {
	t.Helper()
	dir := filepath.Join(home, "skills", slug)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+slug+"\ndescription: x\n---\nBody.\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-identity"), []byte(generation+"\n"), 0o644))
}

func TestGetAgent_StaleSkillGrants(t *testing.T) {
	d, cfg := newTestDeps(t)
	staleGrantSkill(t, d.Home, "kept", "11111111-1111-1111-1111-111111111111")
	staleGrantSkill(t, d.Home, "recreated", "22222222-2222-2222-2222-222222222222")
	a := config.AgentConfig{ID: "sg1", Name: "SG1", Type: config.AgentTypeCustom, Skills: []string{"kept", "recreated"}}
	// The store binds each grant to the installed generation at persist time.
	_, err := agentstore.New(d.Home).CreateState(a.ID, &a, "soul")
	require.NoError(t, err)
	// The granted skill is deleted and recreated: a new generation.
	require.NoError(t, os.RemoveAll(filepath.Join(d.Home, "skills", "recreated")))
	staleGrantSkill(t, d.Home, "recreated", "99999999-9999-9999-9999-999999999999")
	cfg.Agents.List = append(cfg.Agents.List, a)

	read := func(fields ...any) map[string]any {
		r := systools.NewAgentGetTool(d).Execute(tools.WithAgentID(context.Background(), "sg1"),
			map[string]any{"id": "sg1", "fields": fields})
		require.False(t, r.IsError, "%s", r.ForLLM)
		return wu3Success(t, r)
	}

	got := read("skills", "stale_skill_grants")
	assert.Equal(t, []any{"kept", "recreated"}, got["skills"])
	assert.Equal(t, []any{"recreated"}, got["stale_skill_grants"], "only the recreated grant is stale")

	assert.Contains(t, tools.AgentFieldNames, "stale_skill_grants")
	all := wu3AgentRead(t, d, "sg1") // every field except tool_permissions (needs a catalog)
	assert.Equal(t, []any{"recreated"}, all["stale_skill_grants"], "selecting every field includes it")
}

func TestGetAgent_StaleSkillGrantsEmptyWhenNoneStale(t *testing.T) {
	d, cfg := newTestDeps(t)
	a := config.AgentConfig{ID: "sg2", Name: "SG2", Type: config.AgentTypeCustom}
	_, err := agentstore.New(d.Home).CreateState(a.ID, &a, "soul")
	require.NoError(t, err)
	cfg.Agents.List = append(cfg.Agents.List, a)
	r := systools.NewAgentGetTool(d).Execute(context.Background(),
		map[string]any{"id": "sg2", "fields": []any{"stale_skill_grants"}})
	got := wu3Success(t, r)
	assert.Equal(t, []any{}, got["stale_skill_grants"], "an empty list, not absent")
}
