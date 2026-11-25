//go:build goolm && stdjson

package agent

// Order 41 — TestToolSearch_FindsSkills (S-96; C-17; ST-Q6 A). Oracle:
// skills-tools-redesign-spec.md S-96 — "Given deep-research granted to Mia,
// Allow, at Hidden; When Mia calls ToolSearch with 'research sources'; Then a
// result line starts with '[skill] deep-research' and says 'Load it with the
// Skill tool.'; And the skill is not loaded by the search; But a skill Denied
// for Mia is not returned." C-17: ToolSearch also searches the skills the
// calling agent may use (granted or project-authorized, outcome not deny, any
// level including Hidden); a result gives name and description and the
// sentence "Load it with the Skill tool."; it does not load the skill. The
// traceability table lists this row as "Integration Go".
//
// Moved here from pkg/tools (CHECK round 4 N21). The earlier test built a bare
// ToolSearch over a mocked read_file registry with a temp directory of skill
// files that nothing was bound to: no agent called Mia, no grant, no Allow /
// Hidden / Deny input. A correct implementation had no skill to find, and the
// "no instructions" and "denied skill" negatives passed because neither skill
// could ever enter the search corpus. The one place the product binds ToolSearch
// to a home, an agent's grants and the stored skill permissions is the loop
// (pkg/agent/loop_wire.go::registerToolSearch, beside registerSkillTool), so the
// test runs on the real loop and calls the ToolSearch tool that loop registered
// for the agent, the instance a model call reaches. No new API name is needed
// from GREEN, because the test never constructs or wires a search itself. Every
// assertion of the earlier test is carried over unchanged: the found
// "[skill] deep-research" line with its "Load it with the Skill tool." sentence,
// the instructions never returned, the Denied skill never returned.
//
// Fixture (real inputs, no test-owned closure on the observed side):
//   - the skills are installed under $OMNIPUS_HOME/skills, and Mia's stored
//     record grants both (persistSkillGrantFixture binds each grant to the
//     installed folder through the real agent store, as boot does);
//   - Allow, Hidden and Deny are the literal stored keys sandbox.skill_policies
//     and sandbox.skill_visibility (C-2, C-6), written through the loop's own
//     reload (tests/helpers/sandboxkeys). A config that does not carry them ends
//     the test with BLOCKED; there is no variable a stub could set;
//   - audit logging is on, so every Skill call leaves a skill.call row.
//
// deep-research is also a skill the product ships (pkg/skills/embedded), so a
// load of it certifies no revision and skills.SeenRevision cannot witness it
// (pkg/skills/activation.go::LoadActivation, builtin branch). The "not loaded"
// witnesses are therefore the two the product leaves for any load: the
// instructions in the tool result, and a skill.call audit row of mode "load".
//
// Independent witnesses, so a negative cannot pass because nothing was ever
// there to find or to load:
//   - the granted skills are shown to exist for Mia in this loop through the
//     product's own Skill search, and the menu lists them at the default level
//     (instrument subtests, observed on today's product);
//   - an explicit Skill load shows both no-load witnesses can see a load: the
//     instructions appear in the tool result and a load row is written;
//   - "Denied is not returned" is witnessed by the same granted skill being
//     listed for the same query while it is Allow, then absent after only its
//     stored permission flips to deny, with the Allow skill still listed;
//   - "Hidden" is witnessed by the skill leaving the next turn's menu while the
//     search still finds it.
//
// The flow behind the stored-key guard cannot run before GREEN carries the two
// keys and wires the skill search into ToolSearch; until then it stops at the
// loud BLOCKED guard, and its outcomes stay UNVERIFIED.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/helpers/sandboxkeys"

	"github.com/stretchr/testify/require"
)

const (
	s96Agent    = "mia"
	s96Query    = "research sources"
	s96Allowed  = "deep-research"
	s96Denied   = "blocked-skill"
	s96Sentinel = "SENTINEL-DEEP-RESEARCH"
	s96LoadHint = "Load it with the Skill tool."
)

// newS96Harness boots the real loop with both skills installed under the test
// home and granted to Mia through her stored record. The shipped built-in
// skills are pointed at an empty directory so no machine-specific built-in
// folder can stand in for the fixture's own.
func newS96Harness(t *testing.T) *transitionsHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	for _, skill := range []struct{ slug, description, body string }{
		{s96Allowed, "Research sources across the web.", s96Sentinel + " instructions."},
		{s96Denied, "A denied skill about research sources.", "Denied body."},
	} {
		dir := filepath.Join(home, "skills", skill.slug)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte("---\nname: "+skill.slug+"\ndescription: "+skill.description+"\n---\n\n"+skill.body+"\n"), 0o600))
	}
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	granted := persistSkillGrantFixture(t, home,
		config.AgentConfig{ID: s96Agent, Home: home, Skills: []string{s96Allowed, s96Denied}})
	provider := &capturingProvider{}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "s96-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 1,
			},
			List: []config.AgentConfig{granted},
		},
	}
	cfg.Sandbox.AuditLog = true
	cfg.Sandbox.ToolPolicies = map[string]string{"read_file": "allow", "write_file": "allow", "Skill": "allow"}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	return &transitionsHarness{t: t, al: al, provider: provider}
}

// s96Store writes S-96's literal permission and visibility inputs. The reload
// rebuilds the agent instances, so a tool fetched before a store is stale: every
// call below fetches the loop's current instance.
func s96Store(t *testing.T, h *transitionsHarness, policies, visibility map[string]string) {
	t.Helper()
	sandboxkeys.Store(t, h.al.GetConfig, reloadPublisher(h.al, h.provider),
		map[string]any{"skill_policies": policies, "skill_visibility": visibility},
		"C-2/C-6/S-96: sandbox.skill_policies and sandbox.skill_visibility are S-96's literal Given (deep-research Allow at Hidden, a skill Denied for Mia); ToolSearch's skill search (C-17) reads them")
}

// s96Tool returns the tool the real loop registered for Mia under name.
func s96Tool(t *testing.T, h *transitionsHarness, name string) tools.Tool {
	t.Helper()
	inst, ok := h.al.registry.GetAgent(s96Agent)
	require.True(t, ok, "the loop has the agent %q", s96Agent)
	tool, ok := inst.Tools.Get(name)
	require.True(t, ok, "the real loop registers %s for %q", name, s96Agent)
	return tool
}

// s96ToolSearch calls Mia's registered ToolSearch with S-96's query.
func s96ToolSearch(t *testing.T, h *transitionsHarness, session string) string {
	t.Helper()
	res := s96Tool(t, h, "ToolSearch").Execute(execCtx(s96Agent, session), map[string]any{"query": s96Query})
	require.False(t, res.IsError, "ToolSearch must answer a query: %s", res.ForLLM)
	return res.ForLLM
}

// s96SkillLine returns the first result line that starts with the skill label
// and slug, the way S-96 words it ("a result line starts with ...").
func s96SkillLine(out, slug string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[skill] "+slug) {
			return line, true
		}
	}
	return "", false
}

// s96LoadRows returns the skill.call audit rows of mode "load" the loop has
// written so far, read from the loop's own audit file (written and flushed per
// row, so a row is visible as soon as the call returns).
func s96LoadRows(t *testing.T, h *transitionsHarness) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, entry := range readAuditEvents(t, filepath.Join(h.al.homePath, "system", "audit.jsonl")) {
		details, _ := entry["details"].(map[string]any)
		if entry["event"] == string(audit.EventSkillCall) && details["mode"] == "load" {
			rows = append(rows, entry)
		}
	}
	return rows
}

func TestToolSearch_FindsSkills(t *testing.T) {
	t.Run("instrument both skills are granted to Mia and match the query in this loop's own Skill search", func(t *testing.T) {
		h := newS96Harness(t)
		res := s96Tool(t, h, "Skill").Execute(execCtx(s96Agent, "s96-instrument-skill-search"), map[string]any{"query": s96Query})
		require.False(t, res.IsError, "the Skill search answers: %s", res.ForLLM)
		for _, slug := range []string{s96Allowed, s96Denied} {
			require.Contains(t, res.ForLLM, `"name":"`+slug+`"`,
				"the installed, granted skill %s is in the product's skill corpus for Mia and matches %q: %s", slug, s96Query, res.ForLLM)
		}
	})

	t.Run("instrument the granted skills are in the menu at the default level", func(t *testing.T) {
		h := newS96Harness(t)
		h.turn("s96-instrument-menu")
		menu := h.requestOf()
		for _, listed := range []string{s96Allowed, "Research sources across the web.", s96Denied} {
			require.Contains(t, menu, listed, "the menu lists the granted skill before any visibility is stored")
		}
	})

	t.Run("instrument an explicit Skill load is visible to both no-load witnesses", func(t *testing.T) {
		h := newS96Harness(t)
		require.Empty(t, s96LoadRows(t, h), "nothing has loaded a skill in this loop yet")
		loaded := s96Tool(t, h, "Skill").Execute(execCtx(s96Agent, "s96-instrument-explicit-load"), map[string]any{"name": s96Allowed})
		require.False(t, loaded.IsError, "the granted skill loads by name: %s", loaded.ForLLM)
		require.Contains(t, loaded.ForLLM, s96Sentinel, "witness 1: a load puts the instructions in the tool result")
		rows := s96LoadRows(t, h)
		require.Len(t, rows, 1, "witness 2: a load writes exactly one skill.call row of mode load: %v", rows)
		details, _ := rows[0]["details"].(map[string]any)
		require.Equal(t, s96Allowed, details["slug"], "the row names the loaded skill: %v", rows[0])
		require.Equal(t, "loaded", details["outcome"], "the row records a successful load: %v", rows[0])
		require.Equal(t, s96Agent, rows[0]["agent_id"], "the row names the loading agent: %v", rows[0])
	})

	h := newS96Harness(t)
	s96Store(t, h, map[string]string{s96Allowed: "allow", s96Denied: "allow"}, map[string]string{s96Allowed: "hidden"})

	t.Run("S-96 Given: the Allow skill is Hidden, so the next turn's menu does not list it", func(t *testing.T) {
		h.turn("s96-menu-hidden")
		menu := h.requestOf()
		require.NotContains(t, menu, s96Allowed, "Hidden: the granted Allow skill is not listed in the menu")
		require.Contains(t, menu, s96Denied, "control: the other granted skill, with no stored level, is still listed")
	})

	loadRowsBefore := len(s96LoadRows(t, h))
	allowedOut := s96ToolSearch(t, h, "s96-search-allowed")

	t.Run("S-96: a granted Allow Hidden skill is found and labelled as a skill", func(t *testing.T) {
		line, found := s96SkillLine(allowedOut, s96Allowed)
		require.True(t, found, "S-96: the result must contain a '[skill] %s' line, got:\n%s", s96Allowed, allowedOut)
		require.Contains(t, line, s96LoadHint, "S-96: the skill result line says how to load it")
	})

	t.Run("S-96: the search never loads the skill", func(t *testing.T) {
		_, reached := s96SkillLine(allowedOut, s96Allowed)
		require.True(t, reached, "witness: the search reached the skill, so 'not loaded' below is not vacuous:\n%s", allowedOut)
		require.NotContains(t, allowedOut, s96Sentinel, "S-96: the skill's instructions must not be returned by the search")
		require.Len(t, s96LoadRows(t, h), loadRowsBefore, "S-96: the search writes no load row for any skill")
	})

	// Only the stored permission of the Denied skill changes; the Allow skill
	// and the query stay as they were.
	s96Store(t, h, map[string]string{s96Allowed: "allow", s96Denied: "deny"}, map[string]string{s96Allowed: "hidden"})
	deniedOut := s96ToolSearch(t, h, "s96-search-denied")

	t.Run("S-96: a skill Denied for Mia is not returned", func(t *testing.T) {
		_, witnessed := s96SkillLine(allowedOut, s96Denied)
		require.True(t, witnessed,
			"witness: while it is Allow, the same granted skill matches the query and is listed, so its absence below is the Deny and not a missing grant or an empty corpus:\n%s", allowedOut)
		require.NotContains(t, deniedOut, s96Denied, "S-96: a Denied skill must not be discoverable")
		_, controlFound := s96SkillLine(deniedOut, s96Allowed)
		require.True(t, controlFound, "control: the Allow skill is still found after the Deny, so the search itself still works:\n%s", deniedOut)
	})
}
