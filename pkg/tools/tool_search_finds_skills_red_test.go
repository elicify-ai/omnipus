//go:build goolm && stdjson

package tools

// Order 41 — TestToolSearch_FindsSkills (S-96; C-17; ST-Q6 A). Oracle:
// skills-tools-redesign-spec.md S-96 — deep-research granted to Mia, Allow,
// at Hidden; ToolSearch with "research sources" returns a result line that
// STARTS WITH "[skill] deep-research" and says "Load it with the Skill
// tool."; the search does not LOAD the skill (its instructions never appear);
// a skill Denied for Mia is not returned.
//
// Harness: the real ToolSearch tool (pkg/tools/tools_tool.go::ToolsTool,
// ADR-090 infrastructure — always registered, non-deniable) over a real
// registry, with the seeded skills under the test home the granted set
// names. GREEN wires the corpus to include the agent's granted installed and
// project skills (C-17), read from the same authority
// resolveTurnSkillAuthority already resolves (order 33).
//
// RED today: the corpus holds tools only — no skill line is returned, so
// every leg fails for the missing C-17 behaviour.
import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func seedToolSearchSkills(t *testing.T, home string) {
	t.Helper()
	write := func(slug, body string) {
		dir := filepath.Join(home, "skills", slug)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))
	}
	write("deep-research", "---\nname: deep-research\ndescription: Research sources across the web.\n---\n\nSENTINEL-DEEP-RESEARCH instructions.\n")
	write("blocked-skill", "---\nname: blocked-skill\ndescription: A denied skill about research sources.\n---\n\nDenied body.\n")
}

func TestToolSearch_FindsSkills(t *testing.T) {
	home := t.TempDir()
	seedToolSearchSkills(t, home)
	reg := NewToolRegistry()
	reg.Register(&mockSearchableTool{name: "read_file", desc: "Read the contents of a file from disk"})
	toolSearch := NewToolsTool(reg, 300, 10)

	ctx := WithAgentID(context.Background(), "mia")
	res := toolSearch.Execute(ctx, map[string]any{"query": "research sources"})
	require.False(t, res.IsError, "ToolSearch must answer a query: %s", res.ForLLM)

	out := res.ForLLM
	lines := strings.Split(out, "\n")

	t.Run("S-96: a granted Allow Hidden skill is found and labelled as a skill", func(t *testing.T) {
		found := false
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "[skill] deep-research") {
				found = true
				require.Contains(t, line, "Load it with the Skill tool.", "S-96: the skill result line says how to load it")
			}
		}
		require.True(t, found, "S-96: the result must contain a '[skill] deep-research' line, got:\n%s", out)
	})

	t.Run("S-96: the search never loads the skill", func(t *testing.T) {
		require.NotContains(t, out, "SENTINEL-DEEP-RESEARCH", "S-96: the skill's instructions must not be returned by the search")
	})

	t.Run("S-96: a skill Denied for Mia is not returned", func(t *testing.T) {
		require.NotContains(t, out, "blocked-skill", "S-96: a denied skill must not be discoverable")
	})
}
