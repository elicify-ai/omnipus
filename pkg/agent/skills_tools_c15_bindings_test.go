//go:build goolm && stdjson

package agent

// GREEN wiring of the Skills & Tools C-15 seams (#1963) to the production
// entry points: the one visibility authority (tools.ToolSurface), the stored
// levels in config.json and the one-time tool-loading conversion
// (skillperm.Run).

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skillperm"
	"github.com/elicify-ai/omnipus/pkg/tools"

	"github.com/stretchr/testify/require"
)

func init() {
	levelTurnPlanOf = c15LevelTurnPlan
	runToolLoadingConversion = c15RunConversion
	seedToolVisibility = c15SeedVisibility
}

// c15LevelTurnPlan asks the production authority for one tool stored at
// level; "deny" is the effective deny of the tool (D2 row 6).
func c15LevelTurnPlan(level, name, description string) levelTurnPlan {
	denied := level == "deny"
	if denied {
		level = "full"
	}
	surface := tools.ToolSurface{Levels: map[string]string{name: level}}
	p := surface.Plan(name, description, denied)
	return levelTurnPlan{inRequest: p.InRequest, manifestLine: p.ManifestLine, searchable: p.Searchable}
}

func c15RunConversion(t testing.TB, home string) {
	t.Helper()
	require.NoError(t, skillperm.Run(home, skillperm.Options{}))
}

// c15SeedVisibility stores level for key: skill_visibility when key is an
// installed skill, else tool_visibility. For a tool level, a home the
// conversion has not reached yet is converted first through the production upgrade, from the
// loading switch the harness loop runs with (off), so every other tool keeps
// the level it had.
func c15SeedVisibility(t testing.TB, home, key, level string) {
	t.Helper()
	path := filepath.Join(home, "config.json")
	doc := c15ReadConfig(t, path)
	sandbox, _ := doc["sandbox"].(map[string]any)
	field := "tool_visibility"
	if _, err := os.Stat(filepath.Join(home, "skills", key, "SKILL.md")); err == nil {
		field = "skill_visibility"
	}
	if _, converted := sandbox["tool_visibility"]; !converted && field == "tool_visibility" {
		tl, _ := doc["tools"].(map[string]any)
		if tl == nil {
			tl = map[string]any{}
			doc["tools"] = tl
		}
		manifest, _ := tl["manifest"].(map[string]any)
		if manifest == nil {
			manifest = map[string]any{}
			tl["manifest"] = manifest
		}
		if _, set := manifest["compressed"]; !set {
			manifest["compressed"] = false
		}
		c15WriteConfig(t, path, doc)
		c15RunConversion(t, home)
		doc = c15ReadConfig(t, path)
		sandbox, _ = doc["sandbox"].(map[string]any)
	}
	if sandbox == nil {
		sandbox = map[string]any{}
		doc["sandbox"] = sandbox
	}
	levels, _ := sandbox[field].(map[string]any)
	if levels == nil {
		levels = map[string]any{}
		sandbox[field] = levels
	}
	levels[key] = level
	c15WriteConfig(t, path, doc)
}

func c15ReadConfig(t testing.TB, path string) map[string]any {
	t.Helper()
	doc := map[string]any{}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return doc
	}
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc
}

func c15WriteConfig(t testing.TB, path string, doc map[string]any) {
	t.Helper()
	raw, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}
