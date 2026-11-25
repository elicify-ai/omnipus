//go:build goolm && stdjson

package gateway

// Test 65, TestFullSkills_PageSizeFields — skills-tools-redesign-spec test table row 65,
// Dataset D18 rows 1-3 and 6-10 (server side), C-14 "Page warning", FR-079, S-130 (amended in
// b27c64109).
//
// Oracle: the spec's own literals, never a number read off the code:
//
//	D18#1  a skill at Full, its block estimated at 3,240           -> Skill.full_tokens 3,240
//	D18#2  the same skill at Name and short description            -> 3,240 still ("served for every
//	       installed skill whatever its level")
//	D18#3  a skill with a load_error                               -> full_tokens absent
//	D18#6  granted, Full, Allow, verified, 4,001 tokens, window 8,000 -> Agent.full_skills_tokens 4,001
//	D18#7  the same with 4,000 tokens                              -> 4,000
//	D18#8  granted Full/Allow 3,000 + granted Full/Ask, not approved, 3,000 -> 3,000
//	D18#9  Full/Allow 5,000 NOT granted; the granted skill has the agent's own Deny -> 0
//	D18#10 granted, Full, Allow, unverified, trust Ask, no approval -> 0; after "Approve this
//	       version" 3,000
//
// and, for the agent, test row 65's last clause: "full_skills_tokens equals the Full-skills charge
// captured from the agent's next turn (same selection as C-14)".
//
// HOW THE LITERALS ARE REACHED (fixture arithmetic, not an oracle). The spec says "fixed token
// estimator in the Go test". The only such seam, pkg/agent's setFixedTokenEstimators, replaces the
// WHOLE C-14 send check (window, output reserve, pinned core, Full skills, tool definitions,
// message) and is not per-skill; it is also unreachable from pkg/gateway. D18 names the shipped
// built-ins `plan` and `deep-research`, whose bodies a test does not control. So each row installs
// fixture skills (the harness's `notes-helper` in the role of D18's `plan`; `deep-research-fixture`
// and `plan-fixture` in the roles of `deep-research` and, for the per-skill rows, `plan`) whose
// bodies are sized so the REAL estimator returns D18's literal. pkg/agent/context_budget.go::
// estimateMessageTokens charges (runes + 12) * 2 / 5 for one system message (integer division), so
// the smallest text of N tokens has (5N+1)/2 - 12 runes and one rune fewer is N-1 tokens:
//
//	3,240 -> 8,088   3,000 -> 7,488   4,001 -> 9,991   4,000 -> 9,988   5,000 -> 12,488
//
// Per-skill rows size the block `### Skill: <id>\n\n<body>`; agent rows size the WHOLE injected
// text, which pkg/agent/full_skills.go::fullSkillsContext builds as the 95-rune header
// "# Active Skills ..." followed by the blocks joined by "\n\n---\n\n". If a later change moves the
// header or the formula, the INSTRUMENT assertions below fail first and say "fixture drift"; the
// product assertions only run against a fixture the instruments have accepted. A per-skill
// estimator seam (or a fixed estimator reachable from pkg/gateway) would let these rows drop the
// sizing; that is a testability request, listed in fix4-report.md Part F.
//
// OBSERVERS. REST: the raw JSON of GET /skills, GET /agents and GET /agents/{id} (Hard Constraint 8:
// no hand-written wire type, and the generated types do not carry the new members yet).
// Turn: the next real chat turn's request as the scripted model received it, from which the
// Full-skills block is read and its estimate computed with the formula above.
//
// AGENT. The harness's agent is `test-agent`; it stands in for D18's "Mia" (D18 names a role, the
// rows say nothing that depends on the id). Its window of 8,000 is the per-agent override of the
// window ladder (ADR-066 rung 1, the live roster entry's ContextWindowOverride), set AFTER the
// observed turn: C-14's own send check would otherwise judge that turn against 8,000 tokens and the
// request the rows read would never be sent. The page rule that compares half of the window is
// test 66's; here the window is only the second input of D18#6/#7 and a control read.
//
// RED today (saved run fix4f-05, head cc616bf74, whose production code b27c64109 leaves unchanged;
// direct_exit=1): `full_tokens` and `full_skills_tokens` are served by nothing, so every product
// check fails with "no member": all four levels of D18#1/#2, the readable skill beside the
// load_error one in D18#3, both agent reads (the list entry and GET /agents/{id}) of D18#6-#10,
// before and after the window is set in #6/#7, and D18#10 in both states. Product checks report
// with t.Errorf, so one run shows every one of them and the controls after them still run. Those
// controls PASS in that run: the instruments (sizes, header, sentinels, and the charge captured
// from each turn equal to D18's literal in all of #6-#10, 0 before and 3,000 after "Approve this
// version" in #10), the load_error input of D18#3 (its absent half passes trivially and means
// something only beside the sized control), and the 8,000 window read-back of D18#6/#7
// (`context_window_effective`, an existing member).
//
// MUTATION PROBES. The one this test is built for is "count an Ask or not-granted skill in
// full_skills_tokens": D18#8, #9 and #10 turn red under it. The other new probe, "list a skipped
// agent whose record file is gone", is test 67's. Both are CHECK's to run: deferred to CHECK.
// Beyond the spec's two, a mutant that puts the header or a separator into a per-skill number
// would turn D18#1 red, and one that serves `full_tokens` only for a skill at Full would turn the
// three lower levels of D18#2 red.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
	"github.com/elicify-ai/omnipus/tests/helpers/sandboxkeys"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// What fullSkillsContext puts before the skills (pkg/agent/full_skills.go::fullSkillsContext).
	d18Header      = "# Active Skills\n\nThe following skills are active for this request. Follow them when relevant.\n\n"
	d18HeaderRunes = 95

	// The smallest text whose estimate is each D18 literal (derivation in the header).
	d18Runes3240 = 8088
	d18Runes3000 = 7488
	d18Runes4001 = 9991
	d18Runes4000 = 9988
	d18Runes5000 = 12488

	d18NotesSentinel    = "D18-NOTES-ZEBRA-1"
	d18ResearchSentinel = "D18-RESEARCH-ZEBRA-2"
	d18PlanSentinel     = "D18-PLAN-ZEBRA-3"
)

// d18Skill is one fixture skill: its package size, how it is installed and what the three stores say.
type d18Skill struct {
	id, sentinel string
	blockRunes   int    // size of its `### Skill: <id>\n\n<body>` block, in runes
	verified     bool   // true: installed through the real review/install route (C-5)
	policy       string // sandbox.skill_policies[id] (C-2)
	level        string // sandbox.skill_visibility[id] (C-6)
}

// d18Row is one agent row of D18: the skills, the grants, the agent's own values and the stores.
type d18Row struct {
	row      int
	scenario string
	trust    string            // sandbox.skill_trust (C-3)
	skills   []d18Skill        // every skill installed for the row
	granted  []string          // test-agent's skills list; nil leaves the harness's grant alone
	own      map[string]string // test-agent's own skill values (flat store record)
}

// d18Observation is what one next turn and one REST read must show.
type d18Observation struct {
	why       string
	want      int64    // D18's literal for Agent.full_skills_tokens
	textRunes int      // intended size of the captured Full-skills text; 0 = no block expected
	injected  []string // sentinels the block must carry (instrument)
	absent    []string // sentinels it must not carry (instrument)
}

func TestFullSkills_PageSizeFields(t *testing.T) {
	t.Run("instrument: each fixture size maps to its D18 literal under the estimator's formula, one rune fewer does not", d18SizesMapToLiterals)
	t.Run("D18#1 and #2: full_tokens is the block's estimate, served at every level", d18PerSkillRow)
	t.Run("D18#3: a skill with a load_error has no full_tokens", d18LoadErrorRow)
	t.Run("D18#6 and #7: full_skills_tokens is the next turn's Full-skills charge, either side of half the window", d18BoundaryRows)
	t.Run("D18#8: a Full skill that is Ask and not approved is not counted", func(t *testing.T) {
		h := newD18Harness(t, d18Row{
			row: 8, scenario: "Full/Allow 3,000 plus Full/Ask not approved 3,000", trust: "ask",
			skills: []d18Skill{
				{"notes-helper", d18NotesSentinel, d18Runes3000 - d18HeaderRunes, true, "allow", "full"},
				{"deep-research-fixture", d18ResearchSentinel, d18Runes3000, true, "ask", "full"},
			},
			granted: []string{"notes-helper", "deep-research-fixture"},
		})
		d18Observe(t, h, d18Observation{
			why: "D18#8", want: 3000, textRunes: d18Runes3000,
			injected: []string{d18NotesSentinel}, absent: []string{d18ResearchSentinel},
		})
	})
	t.Run("D18#9: a skill the agent is not granted, and one its own value denies, are not counted", func(t *testing.T) {
		h := newD18Harness(t, d18Row{
			row: 9, scenario: "Full/Allow 5,000 not granted; the granted skill is denied by the agent's own value", trust: "ask",
			skills: []d18Skill{
				{"notes-helper", d18NotesSentinel, d18Runes3000 - d18HeaderRunes, true, "allow", "full"},
				{"deep-research-fixture", d18ResearchSentinel, d18Runes5000, true, "allow", "full"},
			},
			granted: []string{"notes-helper"},
			own:     map[string]string{"notes-helper": "deny"},
		})
		d18Observe(t, h, d18Observation{
			why: "D18#9", want: 0, textRunes: 0,
			absent: []string{d18NotesSentinel, d18ResearchSentinel},
		})
	})
	t.Run("D18#10: an unverified Full skill under trust Ask counts only after Approve this version", d18UnverifiedRow)
}

// d18SizesMapToLiterals is fixture hygiene: pure arithmetic, no product code. A typo in a size
// constant would otherwise surface as a product failure.
func d18SizesMapToLiterals(t *testing.T) {
	for _, c := range []struct{ runes, tokens int }{
		{d18Runes3240, 3240}, {d18Runes3000, 3000}, {d18Runes4001, 4001}, {d18Runes4000, 4000}, {d18Runes5000, 5000},
	} {
		require.Equal(t, c.tokens, (c.runes+12)*2/5, "%d runes must estimate to the D18 literal", c.runes)
		require.Equal(t, c.tokens-1, (c.runes-1+12)*2/5, "%d runes is the smallest size for it, so a one-rune error cannot pass", c.runes)
	}
	require.Equal(t, d18HeaderRunes, utf8.RuneCountInString(d18Header), "the header constant is the 95 runes the sizes assume")
}

func d18PerSkillRow(t *testing.T) {
	h := newD18Harness(t, d18Row{
		row: 1, scenario: "one skill whose block is 3,240 tokens", trust: "ask",
		skills: []d18Skill{{"plan-fixture", d18PlanSentinel, d18Runes3240, false, "ask", "full"}},
	})
	for _, level := range []string{"full", "name_and_description", "name_only", "hidden"} {
		d18SetLevel(t, h, "plan-fixture", level)
		skill := d18Find(t, d18GetSkills(t, h), "plan-fixture")
		d18CheckInt(t, skill, "full_tokens", 3240, "GET /skills at level "+level+" (C-14: served for every installed skill whatever its level)")
	}
}

func d18LoadErrorRow(t *testing.T) {
	h := newD18Harness(t, d18Row{
		row: 3, scenario: "a skill whose file cannot be read, beside one that can", trust: "ask",
		skills: []d18Skill{{"plan-fixture", d18PlanSentinel, d18Runes3240, false, "ask", "full"}},
	})
	previous := readListedSkillRevision
	readListedSkillRevision = func(root, id string) (string, error) {
		if id == "notes-helper" {
			return "", errors.New("the skill file could not be read")
		}
		return previous(root, id)
	}
	t.Cleanup(func() { readListedSkillRevision = previous })

	listed := d18GetSkills(t, h)
	broken := d18Find(t, listed, "notes-helper")
	loadError, _ := broken["load_error"].(string)
	require.NotEmpty(t, loadError, "instrument: the fault gave notes-helper a load_error, the D18#3 input")
	_, present := d18Int(t, broken, "full_tokens")
	assert.False(t, present, "D18#3: a skill with a load_error has no full_tokens")
	// The check that makes the absent half mean something: the readable skill beside it is sized.
	d18CheckInt(t, d18Find(t, listed, "plan-fixture"), "full_tokens", 3240, "D18#3, the readable skill in the same response")
}

func d18BoundaryRows(t *testing.T) {
	for _, c := range []struct {
		row    int
		tokens int64
		runes  int
	}{
		{6, 4001, d18Runes4001}, {7, 4000, d18Runes4000},
	} {
		t.Run(fmt.Sprintf("D18#%d: %d tokens", c.row, c.tokens), func(t *testing.T) {
			h := newD18Harness(t, d18Row{
				row: c.row, scenario: fmt.Sprintf("granted, Full, Allow, verified, %d tokens", c.tokens), trust: "ask",
				skills:  []d18Skill{{"notes-helper", d18NotesSentinel, c.runes - d18HeaderRunes, true, "allow", "full"}},
				granted: []string{"notes-helper"},
			})
			why := fmt.Sprintf("D18#%d", c.row)
			charge := d18ObserveTurn(t, h, d18Observation{
				why: why, want: c.tokens, textRunes: c.runes, injected: []string{d18NotesSentinel},
			})
			d18ReadAgentFields(t, h, why, charge, c.tokens)
			// The row's other input: the window the page compares half of. Set after the turn, because C-14's own
			// send check would otherwise judge the turn against an 8,000-token window and hide the request. The
			// control comes before the second product read, so a red run shows the window read is sound.
			d18SetWindow(t, h, 8000)
			agent := d18Find(t, d18GetAgents(t, h), "test-agent")
			d18RequireInt(t, agent, "context_window_effective", 8000, "control: the window the page rule reads (existing member)")
			d18ReadAgentFields(t, h, why+", window 8,000 (the window does not change the charge)", charge, c.tokens)
		})
	}
}

func d18UnverifiedRow(t *testing.T) {
	h := newD18Harness(t, d18Row{
		row: 10, scenario: "granted, Full, Allow, unverified, trust Ask, no approval", trust: "ask",
		skills:  []d18Skill{{"notes-helper", d18NotesSentinel, d18Runes3000 - d18HeaderRunes, false, "allow", "full"}},
		granted: []string{"notes-helper"},
	})
	d18Observe(t, h, d18Observation{why: "D18#10 before approval", want: 0, textRunes: 0, absent: []string{d18NotesSentinel}})
	approveSkillViaPersonRoute(t, h, "notes-helper")
	d18Observe(t, h, d18Observation{
		why: "D18#10 after Approve this version", want: 3000, textRunes: d18Runes3000, injected: []string{d18NotesSentinel},
	})
}

// d18Observe runs the agent's next real turn, checks the instruments on what the model received, then reads
// the REST fields (d18ObserveTurn, then d18ReadAgentFields).
func d18Observe(t *testing.T, h *nativeSkillHarness, o d18Observation) {
	t.Helper()
	charge := d18ObserveTurn(t, h, o)
	d18ReadAgentFields(t, h, o.why, charge, o.want)
}

// d18ObserveTurn runs the agent's next real turn and checks the instruments on what the model received; it
// returns the Full-skills charge of that turn. The instruments are fatal and run before any product read, so a
// red product assertion is never a fixture fault.
func d18ObserveTurn(t *testing.T, h *nativeSkillHarness, o d18Observation) int64 {
	t.Helper()
	request, raw := d18NextTurn(t, h)
	text := d18FullText(t, request)
	if o.textRunes == 0 {
		require.Empty(t, text, "instrument (%s): no Full-skills block is sent", o.why)
	} else {
		require.True(t, strings.HasPrefix(text, d18Header), "instrument (%s): fixture drift, the block does not start with the header the sizes assume", o.why)
		require.Equal(t, o.textRunes, utf8.RuneCountInString(text), "instrument (%s): fixture drift, the block is not the intended size", o.why)
	}
	for _, sentinel := range o.injected {
		require.Contains(t, text, sentinel, "instrument (%s): the skill the row counts reached the model inside the Full-skills block", o.why)
	}
	// Absence is read from the WHOLE request the model received, never only from the block: a block
	// that is empty because the structured parts are missing would make a block-only check vacuous.
	for _, sentinel := range o.absent {
		require.NotContains(t, raw, sentinel, "instrument (%s): the skill the row does not count was not sent to the model in any part", o.why)
	}
	charge := d18Charge(text)
	require.Equal(t, o.want, charge, "instrument (%s): the charge of what the turn carried is D18's literal", o.why)
	return charge
}

// d18ReadAgentFields reads GET /agents (the list entry) and GET /agents/test-agent and checks
// Agent.full_skills_tokens on both: D18's literal, and test row 65's last clause, the Full-skills charge
// captured from the agent's next turn (same selection as C-14). Both reads always run, 0 included: "0 when no
// Full skill would be injected", never absent.
func d18ReadAgentFields(t *testing.T, h *nativeSkillHarness, why string, charge, want int64) {
	t.Helper()
	d18CheckAgentField(t, d18Find(t, d18GetAgents(t, h), "test-agent"), charge, want, "GET /agents, "+why)
	d18CheckAgentField(t, d18GetAgent(t, h, "test-agent"), charge, want, "GET /agents/test-agent, "+why)
}

// The product checks below report with t.Errorf, not t.Fatalf: one run then shows every missing member, and
// the controls that follow a product check (the later levels, the window read, the second state of D18#10)
// still run, so a red run proves they are sound.
func d18CheckAgentField(t *testing.T, agent map[string]any, charge, want int64, where string) {
	t.Helper()
	got, present := d18Int(t, agent, "full_skills_tokens")
	if !present {
		t.Errorf("%s: no full_skills_tokens member (want %d, the charge captured from the next turn is %d) — skills-tools-redesign-spec C-14 'Page warning', test row 65", where, want, charge)
		return
	}
	assert.Equal(t, charge, got, "%s: full_skills_tokens equals the Full-skills charge captured from the agent's next turn", where)
	assert.Equal(t, want, got, "%s: full_skills_tokens is D18's literal", where)
}

func d18CheckInt(t *testing.T, object map[string]any, member string, want int64, why string) {
	t.Helper()
	got, present := d18Int(t, object, member)
	if !present {
		t.Errorf("%s: the response has no %q member (want %d) — skills-tools-redesign-spec C-14 'Page warning', FR-079", why, member, want)
		return
	}
	assert.Equal(t, want, got, "%s: %q", why, member)
}

// newD18Harness builds the native harness, installs the row's skills, then saves the grants and the agent's own
// values, then publishes the three stores through the real reload (the order of D9/D10: folders first, so every
// generation grant is bound to a folder that exists).
func newD18Harness(t *testing.T, row d18Row) *nativeSkillHarness {
	t.Helper()
	h, _ := newNativeD10Harness(t, "", "allow")
	for _, s := range row.skills {
		d18Install(t, h, s)
	}
	if row.granted != nil {
		_, err := agentstore.New(h.api.homePath).Update("test-agent", func(a *config.AgentConfig) error {
			a.Skills = append([]string(nil), row.granted...)
			return nil
		})
		require.NoError(t, err)
		d18RefreshRecord(t, h)
	}
	for key, value := range row.own {
		d18PlantOwn(t, h, key, value)
	}
	policies, levels := map[string]string{}, map[string]string{}
	for _, s := range row.skills {
		policies[s.id], levels[s.id] = s.policy, s.level
	}
	sandboxkeys.Store(t, h.api.agentLoop.GetConfig, d9ReloadPublisher(h), map[string]any{
		"skill_policies": policies, "skill_trust": row.trust, "skill_visibility": levels,
	}, fmt.Sprintf("D18 row %d (%s; C-2 sandbox.skill_policies, C-3 sandbox.skill_trust, C-6 sandbox.skill_visibility)", row.row, row.scenario))
	return h
}

// d18Package is a skill's literal SKILL.md: a frontmatter, then a one-line body that starts with the sentinel and
// ends with a non-blank rune, so no trimming by the loader can change its length. The block is exactly blockRunes.
func d18Package(t *testing.T, s d18Skill) string {
	t.Helper()
	bodyRunes := s.blockRunes - utf8.RuneCountInString("### Skill: "+s.id+"\n\n")
	require.Greater(t, bodyRunes, len(s.sentinel)+2, "fixture: the block has room for its sentinel")
	body := s.sentinel + " " + strings.Repeat("w", bodyRunes-len(s.sentinel)-1)
	require.Equal(t, bodyRunes, utf8.RuneCountInString(body), "fixture: the body has the intended length")
	return "---\nname: " + s.id + "\ndescription: D18 size fixture " + s.id + ".\n---\n" + body
}

func d18Install(t *testing.T, h *nativeSkillHarness, s d18Skill) {
	t.Helper()
	pkg := d18Package(t, s)
	dir := filepath.Join(h.api.homePath, "skills", s.id)
	write := func(content string) {
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600))
	}
	if s.verified {
		if s.id != "notes-helper" { // the harness installs notes-helper; any other verified skill replaces a placeholder
			write("---\nname: " + s.id + "\ndescription: placeholder before the verified install.\n---\nplaceholder")
		}
		seedNativeD10VerifiedInstall(t, h, s.id, pkg)
	} else {
		write(pkg)
		require.Equal(t, nativeD10Revision(t, pkg), nativeD10Current(t, h, s.id), "fixture: the unverified package is the literal bytes")
	}
	require.Equal(t, s.verified, d18Find(t, d18GetSkills(t, h), s.id)["verified"] == true,
		"instrument: the real inventory reports the verification input of %s", s.id)
}

// d18RefreshRecord copies the stored record of test-agent into the live roster, as D10's own-value plant does.
func d18RefreshRecord(t *testing.T, h *nativeSkillHarness) {
	t.Helper()
	persisted, err := agentstore.New(h.api.homePath).Get("test-agent")
	require.NoError(t, err)
	cfg := h.api.agentLoop.GetConfig()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "test-agent" {
			cfg.Agents.List[i] = *persisted
			return
		}
	}
	t.Fatal("fixture: test-agent is not on the live roster")
}

// d18PlantOwn writes the agent's own skill value at the carrier the store keeps (the flat record's top-level
// `tools` member, as D10's plant does) and confirms the carrier through tests/helpers/agentrecord.
func d18PlantOwn(t *testing.T, h *nativeSkillHarness, key, value string) {
	t.Helper()
	path := filepath.Join(h.api.homePath, "entities", "agents", "test-agent.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, json.Unmarshal(raw, &record))
	ownTools, _ := record["tools"].(map[string]any)
	if ownTools == nil {
		ownTools = map[string]any{}
		record["tools"] = ownTools
	}
	ownTools["skills"] = map[string]any{"policies": map[string]string{key: value}}
	raw, err = json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	require.Equal(t, map[string]any{key: value}, agentrecord.Policies(t, h.api.homePath, "test-agent", agentrecord.Skills),
		"fixture: the own value is planted at the carrier the store keeps")
	d18RefreshRecord(t, h)
}

func d18SetLevel(t *testing.T, h *nativeSkillHarness, id, level string) {
	t.Helper()
	sandboxkeys.Store(t, h.api.agentLoop.GetConfig, d9ReloadPublisher(h),
		map[string]any{"skill_visibility": map[string]string{id: level}}, "D18 level change (C-6 sandbox.skill_visibility)")
}

// d18SetWindow gives test-agent the per-agent context-window override (ADR-066 rung 1) on the live roster.
func d18SetWindow(t *testing.T, h *nativeSkillHarness, window int) {
	t.Helper()
	cfg := h.api.agentLoop.GetConfig()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "test-agent" {
			value := window
			cfg.Agents.List[i].ContextWindowOverride = &value
			return
		}
	}
	t.Fatal("fixture: test-agent is not on the live roster")
}

// d18NextTurn runs one real chat turn of test-agent and returns the first request the scripted model received,
// both as messages and as the JSON the model was sent (d9ChatTurn checks it carries this turn's own message).
func d18NextTurn(t *testing.T, h *nativeSkillHarness) (request []providers.Message, raw string) {
	t.Helper()
	before := len(h.script.captured(""))
	raw = d9ChatTurn(t, h, d9ChatSession(t, h), "D18 next turn")
	requests := h.script.captured("")
	require.Greater(t, len(requests), before, "the scripted model received the next turn's request")
	return requests[before], raw
}

// d18FullText returns the system block that starts with the Full skills header, "" when the turn carried none.
func d18FullText(t *testing.T, request []providers.Message) string {
	t.Helper()
	var found []string
	structured := false
	for _, message := range request {
		if message.Role != "system" {
			continue
		}
		structured = structured || len(message.SystemParts) > 0
		for _, part := range message.SystemParts {
			if strings.HasPrefix(part.Text, "# Active Skills") {
				found = append(found, part.Text)
			}
		}
	}
	require.True(t, structured, "instrument: the captured system message carries its structured parts (system_parts), where the Full-skills block is read from")
	require.LessOrEqual(t, len(found), 1, "instrument: a request carries at most one Full-skills block")
	if len(found) == 0 {
		return ""
	}
	return found[0]
}

// d18Charge is the C-14 charge of a Full-skills block: pkg/agent/full_skills.go::fullSkillsTokens estimates it as one
// system message, 0 for no block. The function itself is unexported, so its documented shape is repeated here:
// (runes + 12) * 2 / 5, integer division (pkg/agent/context_budget.go::estimateMessageTokens).
func d18Charge(text string) int64 {
	if text == "" {
		return 0
	}
	return int64((utf8.RuneCountInString(text) + 12) * 2 / 5)
}

func d18GetSkills(t *testing.T, h *nativeSkillHarness) []map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.api.HandleSkills(w, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)))
	return d18RawList(t, w, "GET /api/v1/skills")
}

func d18GetAgents(t *testing.T, h *nativeSkillHarness) []map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.api.HandleAgents(w, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)))
	return d18RawList(t, w, "GET /api/v1/agents")
}

func d18GetAgent(t *testing.T, h *nativeSkillHarness, id string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.api.HandleAgents(w, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil)))
	require.Equal(t, http.StatusOK, w.Code, "GET /api/v1/agents/%s: %s", id, w.Body.String())
	var object map[string]any
	d18Decode(t, w, &object)
	return object
}

func d18RawList(t *testing.T, w *httptest.ResponseRecorder, what string) []map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "%s: %s", what, w.Body.String())
	var list []map[string]any
	d18Decode(t, w, &list)
	return list
}

// d18Decode reads the raw JSON with numbers kept exact (Hard Constraint 8: no hand-written wire type).
func d18Decode(t *testing.T, w *httptest.ResponseRecorder, into any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(w.Body.String()))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(into), "the response is JSON: %s", w.Body.String())
}

func d18Find(t *testing.T, list []map[string]any, id string) map[string]any {
	t.Helper()
	for _, entry := range list {
		if entry["id"] == id {
			return entry
		}
	}
	t.Fatalf("the response does not list %q", id)
	return nil
}

// d18Int reads an integer member of a raw JSON object; present is false when the member is absent or null.
func d18Int(t *testing.T, object map[string]any, member string) (value int64, present bool) {
	t.Helper()
	raw, ok := object[member]
	if !ok || raw == nil {
		return 0, false
	}
	number, isNumber := raw.(json.Number)
	require.True(t, isNumber, "%q must be a JSON number, got %T", member, raw)
	value, err := number.Int64()
	require.NoError(t, err, "%q must be an integer, got %s", member, number)
	return value, true
}

func d18RequireInt(t *testing.T, object map[string]any, member string, want int64, why string) {
	t.Helper()
	got, present := d18Int(t, object, member)
	require.True(t, present, "%s: the response has no %q member (want %d) — skills-tools-redesign-spec C-14 'Page warning', FR-079", why, member, want)
	require.Equal(t, want, got, "%s: %q", why, member)
}
