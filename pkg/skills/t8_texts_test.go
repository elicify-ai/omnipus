package skills_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: tool-parity r17 FR-153 / 19.19. Seed the real embedded package,
// load through the real Skill tool using the real filesystem loader. The
// injected resolver is a filesystem/policy edge, not a fake Skill result.
func TestSkills_T8Texts_FreshInstallLoad(t *testing.T) {
	root := t.TempDir()
	_, err := skills.SeedDefaults(root)
	require.NoError(t, err)
	loader := skills.NewSkillsLoader("", root, "")
	tool := tools.NewSkillTool(10)
	tool.SetResolver(func(_ context.Context, slug string) tools.SkillLoadOutcome {
		text, ok := loader.LoadSkill(slug)
		if !ok {
			return tools.SkillLoadOutcome{Status: tools.SkillLoadNotFound}
		}
		return tools.SkillLoadOutcome{Status: tools.SkillLoadLoaded, Content: text, CanonicalSlug: slug}
	}, func(context.Context, string) bool { return true }, nil)
	for _, slug := range []string{"create-agent", "change-agent", "capability-plan"} {
		t.Run(slug, func(t *testing.T) {
			path := filepath.Join(root, slug, "SKILL.md")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			r := tool.Execute(tools.WithAgentID(context.Background(), "omnipus"), map[string]any{"name": slug})
			require.False(t, r.IsError, "real Skill load: %s", r.ForLLM)
			assert.Contains(t, r.ForLLM, "get_agent")
			assert.Contains(t, r.ForLLM, "fields")
			for _, old := range []string{"get_agent_tools", "read_agent_metadata"} {
				assert.NotContains(t, r.ForLLM, old, "19.17/19.19 loaded package has no removed name")
				assert.NotContains(t, string(before), old)
			}
			_, err = skills.SeedDefaults(root)
			require.NoError(t, err)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "r17 no refresh or rewrite of an existing file")
		})
	}
}
