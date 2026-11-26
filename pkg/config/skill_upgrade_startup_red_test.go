//go:build goolm && stdjson

package config_test

// Fix round 4, Part H item 3 (team-lead): the real-path halves of two upgrade
// tests whose rows run in package config over the production planners.
//
//   - TestSkillPolicyUpgrade_StartupWritesThePlan: S-16 (all four examples),
//     Dataset D1 rows 15-16 and C-19 steps 3-4. The real startup upgrader
//     (skillperm.Run, the call gateway.go::loadConfigAndProvider makes before
//     the config is loaded) runs on a literal OLD home and the stored values are
//     read back from config.json and Mia's agent record.
//   - TestVisibilityStartingLevels_Upgrade_StartupWritesTheConversion: C-6
//     (starting levels), C-19 step 3, S-41 and Dataset D7 rows 1-14 and 16, plus
//     row 15 (input "any row, second start", expected "nothing written").
//
// Why they live here and not beside the planner rows: pkg/skillperm imports
// pkg/config, so an internal pkg/config test cannot import it. This is the
// external test package, as upgrade_stop_boundaries_red_test.go already is.
//
// Oracles are the spec's tables, spelled literally below. The old homes are
// literal JSON, never marshalled from the new config structs (that could erase
// the exact old values being translated). The persisted files are read as raw
// JSON; no production planner is called to build an expected value.
//
// What a mutation must kill (CHECK): the planner the upgrade calls (a fixed
// Allow, a missing global value, an override for an ungranted skill, a wrong
// level for a tool or server), the write of any planned value, and a second
// start that rewrites. Mutation proof is deferred to CHECK.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skillperm"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeOldFile writes one literal file of an old home.
func writeOldFile(t *testing.T, home, relative, content string) {
	t.Helper()
	path := filepath.Join(home, relative)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// oldHomeConfig is the old config.json: the old blanket Skill tool policy, the
// old trust word and the old manifest and MCP switches. The members are the
// ones the snapshot reads (sandbox.tool_policies.Skill, tools.manifest.*,
// tools.mcp.discovery.enabled and tools.mcp.servers.<id>.deferred).
func oldHomeConfig(t *testing.T, oldGlobalSkill string, manifest, mcp map[string]any) string {
	t.Helper()
	policies := map[string]any{"bash": "ask"}
	if oldGlobalSkill != "" {
		policies["Skill"] = oldGlobalSkill
	}
	document := map[string]any{
		"version": 1,
		"sandbox": map[string]any{"tool_policies": policies, "skill_trust": "warn_unverified"},
		"tools":   map[string]any{"manifest": manifest, "mcp": mcp},
	}
	raw, err := json.Marshal(document)
	require.NoError(t, err)
	return string(raw)
}

// seedOldSkill installs one skill folder the way an old install has it.
func seedOldSkill(t *testing.T, home, slug string) {
	t.Helper()
	writeOldFile(t, home, "skills/"+slug+"/SKILL.md", "---\nname: "+slug+"\ndescription: old home fixture.\n---\n\nLEGACY-"+slug+"-4R\n")
}

// readStoredStringMap reads sandbox.<member> of the persisted config.json as a
// string map ({} when the member is absent).
func readStoredStringMap(t *testing.T, home, member string) map[string]string {
	t.Helper()
	sandbox := upgradeChildObject(t, upgradeReadObject(t, home, "config.json"), "sandbox")
	out := map[string]string{}
	raw, present := sandbox[member]
	if !present {
		return out
	}
	object, ok := raw.(map[string]any)
	require.True(t, ok, "sandbox.%s must be an object", member)
	for key, value := range object {
		text, isString := value.(string)
		require.True(t, isString, "sandbox.%s[%q] must be a string, got %T", member, key, value)
		out[key] = text
	}
	return out
}

// startOldHome is the one real startup call: gateway.go::loadConfigAndProvider
// runs skillperm.Run(home, skillperm.Options{}) before it loads the config.
func startOldHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	require.NoError(t, skillperm.Run(home, skillperm.Options{}), "the startup upgrade of a well-formed old home finishes")
}

// TestSkillPolicyUpgrade_StartupWritesThePlan: Traces to BDD S-16; spec C-19
// steps 3-4; Dataset D1 rows 15-16.
func TestSkillPolicyUpgrade_StartupWritesThePlan(t *testing.T) {
	rows := []struct {
		name       string
		oldGlobal  string   // the old stored global Skill tool value
		oldAgent   string   // Mia's old stored Skill value ("" = none)
		installed  []string // installed skill folders
		granted    []string // skills granted to Mia
		wantGlobal map[string]string
		wantMiaOwn map[string]any // Mia's stored skills.policies; nil = nothing stored
	}{
		{
			name: "S-16 r1 old global allow, agent none", oldGlobal: "allow", oldAgent: "",
			installed: []string{"plan", "deep-research"}, granted: []string{"plan", "deep-research"},
			wantGlobal: map[string]string{"plan": "allow", "deep-research": "allow"}, wantMiaOwn: nil,
		},
		{
			name: "S-16 r2 old global allow, agent deny (granted skills)", oldGlobal: "allow", oldAgent: "deny",
			installed: []string{"plan", "deep-research"}, granted: []string{"plan", "deep-research"},
			wantGlobal: map[string]string{"plan": "allow", "deep-research": "allow"},
			wantMiaOwn: map[string]any{"plan": "deny", "deep-research": "deny"},
		},
		{
			name: "D1#15 old global ask, agent none", oldGlobal: "ask", oldAgent: "",
			installed: []string{"plan"}, granted: []string{"plan"},
			wantGlobal: map[string]string{"plan": "ask"}, wantMiaOwn: nil,
		},
		{
			// C-4 "stored as given": the looser allow is stored; only the run-time floor makes it ask.
			name: "S-16 r3 old global ask, agent allow stored as given", oldGlobal: "ask", oldAgent: "allow",
			installed: []string{"plan"}, granted: []string{"plan"},
			wantGlobal: map[string]string{"plan": "ask"}, wantMiaOwn: map[string]any{"plan": "allow"},
		},
		{
			name: "S-16 r4 old global deny, agent none", oldGlobal: "deny", oldAgent: "",
			installed: []string{"plan"}, granted: []string{"plan"},
			wantGlobal: map[string]string{"plan": "deny"}, wantMiaOwn: nil,
		},
		{
			// C-19 step 4: only skills granted to the agent get its old value.
			name: "C-19 step 4 an installed skill Mia does not hold gets no override", oldGlobal: "deny", oldAgent: "deny",
			installed: []string{"plan", "verify"}, granted: []string{"plan"},
			wantGlobal: map[string]string{"plan": "deny", "verify": "deny"}, wantMiaOwn: map[string]any{"plan": "deny"},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			home := t.TempDir()
			writeOldFile(t, home, "config.json", oldHomeConfig(t, row.oldGlobal,
				map[string]any{"compressed": true}, map[string]any{"discovery": map[string]any{"enabled": false}, "servers": map[string]any{}}))
			for _, slug := range row.installed {
				seedOldSkill(t, home, slug)
			}
			skillList, err := json.Marshal(row.granted)
			require.NoError(t, err)
			builtin := `{}`
			if row.oldAgent != "" {
				builtin = `{"Skill":"` + row.oldAgent + `"}`
			}
			writeOldFile(t, home, "entities/agents/mia.json",
				`{"id":"mia","name":"Mia","skills":`+string(skillList)+`,"tools":{"builtin":{"policies":`+builtin+`}}}`)
			// Instrument: the raw fixture shows the old values before any startup can change them.
			require.Equal(t, row.oldGlobal, readOldGlobalSkill(t, home), "fixture: the old global Skill value is stored")
			require.Empty(t, readStoredStringMap(t, home, "skill_policies"), "fixture: an old home has no per-skill values")

			startOldHome(t, home)

			stored := readStoredStringMap(t, home, "skill_policies")
			for slug, want := range row.wantGlobal {
				assert.Equal(t, want, stored[slug], "%s: every installed skill's global value is the old global Skill value (C-19 step 3), key %q", row.name, slug)
			}
			own := agentrecord.Policies(t, home, "mia", agentrecord.Skills)
			if row.wantMiaOwn == nil {
				assert.Empty(t, own, "%s: an agent with no old value gets no override (translation, never backfill)", row.name)
			} else {
				assert.Equal(t, row.wantMiaOwn, own, "%s: Mia's own values are her old Skill value, as given, for the skills she holds (C-19 step 4)", row.name)
			}

			// Dataset D7 row 15 ("any row, second start" gives "nothing written"), applied to these S-16
			// homes too (the spec states that row for D7 only): every byte, name and mode stays.
			before := readUpgradeTree(t, home)
			startOldHome(t, home)
			assertUpgradeTreeUnchanged(t, before, readUpgradeTree(t, home))
		})
	}
}

// readOldGlobalSkill reads the stored global Skill tool policy of config.json.
func readOldGlobalSkill(t *testing.T, home string) string {
	t.Helper()
	sandbox := upgradeChildObject(t, upgradeReadObject(t, home, "config.json"), "sandbox")
	value, _ := upgradeChildObject(t, sandbox, "tool_policies")["Skill"].(string)
	return value
}

// d7OldHome is one pre-upgrade switch state of Dataset D7 plus the rows it covers.
type d7OldHome struct {
	name      string
	manifest  map[string]any
	discovery map[string]any            // tools.mcp.discovery
	servers   map[string]map[string]any // tools.mcp.servers
	// wantTools: sandbox.tool_visibility entries the conversion must store (a subset: the spec
	// pins these rows; every other catalog tool gets a level too).
	wantTools map[string]string
	// wantSkills: sandbox.skill_visibility entries.
	wantSkills map[string]string
}

// offlineServer is an MCP server the upgrade could never reach: its command does not exist.
// The conversion is computed from the stored config alone (D7 row 13).
func offlineServer(deferred any) map[string]any {
	server := map[string]any{"enabled": true, "command": "/nonexistent/old-mcp-server", "args": []string{}}
	if deferred != nil {
		server["deferred"] = deferred
	}
	return server
}

// TestVisibilityStartingLevels_Upgrade_StartupWritesTheConversion: Traces to BDD S-41; spec C-6
// (starting levels), C-19 step 3; Dataset D7 rows 1-16.
func TestVisibilityStartingLevels_Upgrade_StartupWritesTheConversion(t *testing.T) {
	homes := []d7OldHome{
		{
			name:      "compressed on, preview off, discovery on (D7 rows 1, 2, 3, 5, 6, 13, 14, 16)",
			manifest:  map[string]any{"compressed": true, "preview_all_lazy": false},
			discovery: map[string]any{"enabled": true},
			servers:   map[string]map[string]any{"github": offlineServer(nil), "visible": offlineServer(false)},
			wantTools: map[string]string{
				"read_file":        "full",                 // D7#1  the old 36-tool list
				"serve_web":        "name_and_description", // D7#2  the old previewed tool
				"browser_evaluate": "hidden",               // D7#3  search-only
				"ToolSearch":       "full",                 // D7#16 locked full
				"mcp_github_*":     "hidden",               // D7#5 (deferred unset) and D7#13 (server unreachable)
				"mcp_visible_*":    "hidden",               // D7#6  registered visible but lazy
			},
			wantSkills: map[string]string{"plan": "name_and_description", "deep-research": "name_and_description"}, // D7#14
		},
		{
			name:      "compressed on, preview on, discovery on (D7 rows 4, 7, 8)",
			manifest:  map[string]any{"compressed": true, "preview_all_lazy": true},
			discovery: map[string]any{"enabled": true},
			servers:   map[string]map[string]any{"shown": offlineServer(false), "lazy": offlineServer(nil)},
			wantTools: map[string]string{
				"browser_evaluate": "name_and_description", // D7#4 old switch on lifts the search-only tool
				"mcp_shown_*":      "name_and_description", // D7#7 visible + preview
				"mcp_lazy_*":       "hidden",               // D7#8 registered hidden stays hidden
			},
		},
		{
			name:      "compressed off, discovery on (D7 rows 9, 10, 12)",
			manifest:  map[string]any{"compressed": false},
			discovery: map[string]any{"enabled": true},
			servers:   map[string]map[string]any{"github": offlineServer(true), "plain": offlineServer(false)},
			wantTools: map[string]string{
				"browser_evaluate": "full",   // D7#9  any static tool
				"mcp_github_*":     "hidden", // D7#10 effectively deferred
				"mcp_plain_*":      "full",   // D7#12 not deferred
			},
		},
		{
			name:      "compressed off, discovery key absent (D7 row 11)",
			manifest:  map[string]any{"compressed": false},
			discovery: nil,
			servers:   map[string]map[string]any{"github": offlineServer(true)},
			wantTools: map[string]string{"mcp_github_*": "full"}, // D7#11 discovery off
		},
	}
	for _, old := range homes {
		t.Run(old.name, func(t *testing.T) {
			home := t.TempDir()
			mcp := map[string]any{"servers": old.servers}
			if old.discovery != nil {
				mcp["discovery"] = old.discovery
			}
			writeOldFile(t, home, "config.json", oldHomeConfig(t, "allow", old.manifest, mcp))
			for _, slug := range []string{"plan", "deep-research"} {
				seedOldSkill(t, home, slug)
			}
			// Instrument: no level is stored before the upgrade, so a value found afterwards was written by it.
			require.Empty(t, readStoredStringMap(t, home, "tool_visibility"), "fixture: an old home stores no levels")
			require.Empty(t, readStoredStringMap(t, home, "skill_visibility"), "fixture: an old home stores no skill levels")

			startOldHome(t, home)

			tools := readStoredStringMap(t, home, "tool_visibility")
			for name, want := range old.wantTools {
				assert.Equal(t, want, tools[name], "%s: starting level of %q (Dataset D7; the level of the old request pipeline)", old.name, name)
			}
			skillLevels := readStoredStringMap(t, home, "skill_visibility")
			for key, want := range old.wantSkills {
				assert.Equal(t, want, skillLevels[key], "%s: starting level of skill %q (D7 row 14)", old.name, key)
			}
			for key := range old.wantSkills {
				assert.NotContains(t, tools, key, "a skill key is stored with the skills, not with the tools")
			}

			// D7 row 15: a second start recomputes the same plan and writes nothing.
			before := readUpgradeTree(t, home)
			startOldHome(t, home)
			assertUpgradeTreeUnchanged(t, before, readUpgradeTree(t, home))
		})
	}
}
