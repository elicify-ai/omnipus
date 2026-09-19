package skills_test

import (
	"maps"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// FR-013 (ADR-090 agent-configuration-and-skills-spec): "Capability lint
// checks every invocation branch against the assigned role's effective seeded
// grants; limitation/prohibition references are allowed without execution
// permission. For shared skills, at least one assigned role must permit each
// applicable invocation and every role-specific branch must permit its own
// required operations."
//
// THE GRAMMAR (documentation and lint only — no runtime code parses any of
// it, and unmarked prose is out of scope by design, so nothing here is a
// proprietary runtime skill contract):
//
//   - `tool:<name>`   — an INVOCATION reference: the text directs or expects
//     the addressed role to call or hold the tool. Must be permitted (allow
//     or ask) by the addressed role's effective policy.
//   - `notool:<name>` — a PROHIBITION/limitation reference: the text states
//     the role does not hold or must not use the tool. Exempt from the
//     permission check (that is its purpose), but the name must still exist
//     in the catalog, so drift in a prohibition is caught too.
//   - `branch:<role>[,<role>]` — a LINE-SCOPED role qualifier for shared
//     skill bodies: invocation marks after it on the SAME line are checked
//     against exactly the listed roles (every listed role must both be a
//     seeded assignee of that skill and permit the mark). The scope resets at
//     the line break; marks on lines without a token are shared, and a shared
//     mark passes when at least one assigned role permits it. Prompts and
//     system souls address exactly one role each, so a branch token there is
//     a finding, not a scoping device.
//
// THE ORACLE — the ACTUAL runtime permission, never a declared copy.
// "Effective seeded policy" is resolved the way a fresh install actually
// resolves it: freshSeededConfig builds config.DefaultConfig (the shipped
// global tool-policy ceiling) and runs coreagent.SeedConfig over it;
// effectivePolicyOracle then answers each (role, tool) through the same
// config-only per-agent bridge the real approval gates fall back to
// (tools.BuildFallbackPolicyCfg) and the same bare global×agent
// strictest-wins merge (tools.ResolveEffectivePolicy). No merge logic is
// duplicated in this file. coreagent.ADR090RolePolicyInventory — FR-008's
// DECLARED intended map — is deliberately NOT consulted here: ordinary-role
// inventory-to-runtime consistency is a separate gate, and FR-013's
// instruction-to-runtime consistency is only honest against the runtime
// itself. TestADR090_CapabilityLint_OracleHonorsRuntimeRestrictions proves
// the oracle really is that resolver: a tightened global ceiling and a
// per-agent override each flip its answers. Skill-to-role assignments come
// from the same seeded config, so a future assignment change is enforced
// without editing this test.
//
// prohibitedToolRef matches a prohibition/limitation mark. "notool:x"
// CONTAINS an invocation mark for "x" at a two-byte offset, which
// extractCapabilityMarks handles by span exclusion — proven by a dedicated
// fixture, because a naive tool: scan would double-count every prohibition
// as an invocation and make the exemption illusory.
var prohibitedToolRef = regexp.MustCompile(`notool:([A-Za-z0-9_]+)`)

// branchScopeToken matches a line-scoped role qualifier. Role ids are the
// seeded CoreAgentID values (lowercase, e.g. "plansupervisor"), optionally
// comma-separated for an all-listed-roles-permit branch.
var branchScopeToken = regexp.MustCompile(`branch:([a-z][a-z0-9]*(?:,[a-z][a-z0-9]*)*)`)

// scopedInvocation is one invocation mark with its resolved scope: roles is
// nil for a shared mark.
type scopedInvocation struct {
	name  string
	roles []string
}

// extractCapabilityMarks splits marked references into invocation marks
// (with their line-scoped branch roles) and prohibition names. The walk is
// line-by-line and, within a line, positional: a branch token re-scopes only
// the marks that FOLLOW it on that line, and the scope resets at the line
// break. Deterministic by construction (text order).
func extractCapabilityMarks(text string) (invocations []scopedInvocation, prohibitions []string) {
	for line := range strings.SplitSeq(text, "\n") {
		prohibitedLocs := prohibitedToolRef.FindAllStringIndex(line, -1)
		prohibitionSpans := make([][2]int, 0, len(prohibitedLocs))
		for _, loc := range prohibitedLocs {
			prohibitionSpans = append(prohibitionSpans, [2]int{loc[0], loc[1]})
			prohibitions = append(prohibitions, line[loc[0]+len("notool:"):loc[1]])
		}
		withinProhibition := func(start int) bool {
			for _, span := range prohibitionSpans {
				if start >= span[0] && start < span[1] {
					return true
				}
			}
			return false
		}
		type markEvent struct {
			pos    int
			branch bool
			data   string
		}
		var events []markEvent
		for _, loc := range branchScopeToken.FindAllStringSubmatchIndex(line, -1) {
			events = append(events, markEvent{pos: loc[0], branch: true, data: line[loc[2]:loc[3]]})
		}
		for _, loc := range markedToolRef.FindAllStringIndex(line, -1) {
			if withinProhibition(loc[0]) {
				continue
			}
			events = append(events, markEvent{pos: loc[0], data: line[loc[0]+len("tool:") : loc[1]]})
		}
		sort.Slice(events, func(i, j int) bool { return events[i].pos < events[j].pos })
		var scope []string
		for _, ev := range events {
			if ev.branch {
				scope = strings.Split(ev.data, ",")
				continue
			}
			invocations = append(invocations, scopedInvocation{name: ev.data, roles: scope})
		}
	}
	return invocations, prohibitions
}

// capabilityPolicyFunc resolves (role, tool) to an effective policy. ok=false
// means the role is unknown to the oracle, which counts as deny.
type capabilityPolicyFunc func(role, tool string) (config.ToolPolicy, bool)

// capabilitySurface is one lintable text with its addressing context.
type capabilitySurface struct {
	label      string
	text       string
	assigned   []string // seeded assignees; prompts/souls carry exactly one
	policy     capabilityPolicyFunc
	branchesOK bool // skill bodies may carry branch tokens; prompts/souls may not
}

// permitsInvocation reports whether the policy is allow or ask. Ask counts
// as permitted: an Ask-gated invocation is a runtime confirmation flow, not
// a prohibition, so a prompt telling a role to use an Ask tool is honest.
func permitsInvocation(p config.ToolPolicy) bool {
	return p == config.ToolPolicyAllow || p == config.ToolPolicyAsk
}

// lintCapabilitySurface returns every FR-013 capability finding for one
// surface, in scan order. Both invocation and prohibition names must exist
// in the catalog; invocation marks are then checked against the effective
// policy of the addressed role(s) per the shared/branch rules above.
func lintCapabilitySurface(s capabilitySurface, catalog map[string]bool) []string {
	var findings []string
	invocations, prohibitions := extractCapabilityMarks(s.text)

	for _, name := range prohibitions {
		if !catalog[name] {
			findings = append(findings, s.label+": prohibition names "+name+" which does not exist in the catalog")
		}
	}
	if !s.branchesOK && branchScopeToken.MatchString(s.text) {
		findings = append(findings, s.label+": carries a branch: scope token but addresses exactly one role — branch tokens belong in shared skill bodies only")
	}

	assigned := map[string]bool{}
	for _, role := range s.assigned {
		assigned[role] = true
	}
	for _, inv := range invocations {
		if !catalog[inv.name] {
			findings = append(findings, s.label+": invocation names "+inv.name+" which does not exist in the catalog")
			continue
		}
		if len(inv.roles) == 0 {
			if len(s.assigned) == 0 {
				findings = append(findings, s.label+": shared invocation of "+inv.name+" but no role is assigned this skill by the seed — seed an assignment or drop the invocation")
				continue
			}
			permitted := false
			for _, role := range s.assigned {
				if p, ok := s.policy(role, inv.name); ok && permitsInvocation(p) {
					permitted = true
				}
			}
			if !permitted {
				findings = append(findings, s.label+": shared invocation of "+inv.name+" is not permitted (allow/ask) by ANY assigned role "+strings.Join(s.assigned, ","))
			}
			continue
		}
		for _, role := range inv.roles {
			if !assigned[role] {
				findings = append(findings, s.label+": branch "+role+" for "+inv.name+" names a role the seed does not assign this skill")
				continue
			}
			if p, ok := s.policy(role, inv.name); !ok || !permitsInvocation(p) {
				findings = append(findings, s.label+": branch "+role+" requires "+inv.name+" but that role's effective seeded policy does not permit it")
			}
		}
	}
	return findings
}

// freshSeededConfig returns what a fresh install boots with: the shipped
// DefaultConfig (global ceiling included) run through coreagent.SeedConfig.
// DefaultConfig populates agent defaults only — never Agents.List — so the
// seeded roster is exactly the ADR-090 nine, and one config can feed both
// the policy oracle and the assignment map below.
func freshSeededConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	if !coreagent.SeedConfig(cfg) {
		t.Fatal("SeedConfig on a fresh DefaultConfig must report modified=true — the runtime oracle depends on the fresh seed")
	}
	return cfg
}

// capabilityLintRoles is every role the lint addresses: the seven ordinary
// built-ins plus the two hidden system agents.
func capabilityLintRoles() []string {
	roles := make([]string, 0, 9)
	for _, a := range coreagent.All() {
		roles = append(roles, string(a.ID))
	}
	for _, sa := range coreagent.SystemAgents() {
		roles = append(roles, string(sa.ID))
	}
	return roles
}

// effectivePolicyOracle answers (role, tool) with the ACTUAL effective
// permission a fresh install of cfg would enforce: per role it builds the
// same config-only per-agent bridge the real approval gates fall back to
// (tools.BuildFallbackPolicyCfg) and resolves through the same bare
// global×agent strictest-wins merge (tools.ResolveEffectivePolicy). The cfg
// parameter (not a captured fresh seed) is what lets the negative-control
// test prove ceiling and per-agent restrictions flow through this oracle.
// pkg/tools imports pkg/skills, which is why this file lives in the external
// skills_test package — an in-package test could not import it.
func effectivePolicyOracle(t *testing.T, cfg *config.Config) capabilityPolicyFunc {
	t.Helper()
	bridges := map[string]*tools.ToolPolicyCfg{}
	for _, role := range capabilityLintRoles() {
		polCfg, _ := tools.BuildFallbackPolicyCfg(cfg, role)
		if polCfg == nil {
			t.Fatalf("BuildFallbackPolicyCfg returned nil for role %q — it is documented non-nil; the oracle cannot run", role)
		}
		bridges[role] = polCfg
	}
	if len(bridges) == 0 {
		t.Fatal("coreagent.All()/SystemAgents() are empty — the capability lint would have no oracle")
	}
	return func(role, tool string) (config.ToolPolicy, bool) {
		polCfg, ok := bridges[role]
		if !ok {
			return config.ToolPolicyDeny, false
		}
		return config.ToolPolicy(tools.ResolveEffectivePolicy(polCfg, tool)), true
	}
}

// seededSkillAssignments derives skill-id -> assigned role ids from the same
// fresh seeded config (coreAgentSkills and systemAgentSkills materialized by
// SeedConfig), so the "assigned role" of every shared skill is the seed's
// own answer.
func seededSkillAssignments(t *testing.T) map[string][]string {
	t.Helper()
	cfg := freshSeededConfig(t)
	sets := map[string]map[string]bool{}
	for _, entry := range cfg.Agents.List {
		for _, skill := range entry.Skills {
			if sets[skill] == nil {
				sets[skill] = map[string]bool{}
			}
			sets[skill][entry.ID] = true
		}
	}
	if len(sets) == 0 {
		t.Fatal("the fresh seed assigned no skills — the capability lint's shared-skill rule would be vacuous")
	}
	out := map[string][]string{}
	for skill, roles := range sets {
		ids := make([]string, 0, len(roles))
		for role := range roles {
			ids = append(ids, role)
		}
		sort.Strings(ids)
		out[skill] = ids
	}
	return out
}

// TestADR090_CapabilityLint_OracleHonorsRuntimeRestrictions proves the
// oracle is the real resolver, not a declared copy: a tighter GLOBAL CEILING
// and, separately, a PER-AGENT restriction each flip its answers exactly as
// the compositor's strictest-wins merge demands. Every mutation is to a
// test-local config built from freshSeededConfig (DefaultConfig constructs
// fresh policy maps on every call, and the per-agent map is cloned before
// mutation), so no product policy and no shared state is touched.
func TestADR090_CapabilityLint_OracleHonorsRuntimeRestrictions(t *testing.T) {
	roles := capabilityLintRoles()
	if len(roles) == 0 {
		t.Fatal("no roles to control — the oracle controls would be vacuous")
	}
	fresh := effectivePolicyOracle(t, freshSeededConfig(t))

	// Vacuity guard: at least one seeded role must permit bash on the fresh
	// seed, or the ceiling control below could pass without the merge ever
	// having to tighten anything.
	somePermitsBash := false
	for _, role := range roles {
		if p, ok := fresh(role, "bash"); ok && permitsInvocation(p) {
			somePermitsBash = true
			break
		}
	}
	if !somePermitsBash {
		t.Fatal("no seeded role permits bash on the fresh seed — the ceiling-tightening control would be vacuous; pick another probe tool")
	}

	// Control 1 — tighter global ceiling: deny bash globally and EVERY role's
	// answer must flip to deny, including roles whose own per-agent entry
	// permits it (deny on either side beats everything; a copy of a declared
	// map could never honor this mutation).
	ceilingCfg := freshSeededConfig(t)
	// The ceiling map is raw map[string]string (validated at the write
	// boundary) — hence the string conversion, mirroring
	// tools.BuildFallbackPolicyCfg's own trusting conversion.
	ceilingCfg.Sandbox.ToolPolicies["bash"] = string(config.ToolPolicyDeny)
	ceilingOracle := effectivePolicyOracle(t, ceilingCfg)
	for _, role := range roles {
		if p, ok := ceilingOracle(role, "bash"); !ok || p != config.ToolPolicyDeny {
			t.Errorf("role %q: tightened global ceiling (bash=deny) must yield deny via strictest-wins; oracle answered (%q, ok=%v) — the oracle is not reading the real merge", role, p, ok)
		}
	}

	// Control 2 — per-agent restriction: deny write_file on mia's own entry
	// only. mia's answer must flip to deny while EVERY other role's answer
	// stays byte-identical to the fresh oracle's.
	freshP, ok := fresh(string(coreagent.IDMia), "write_file")
	if !ok || !permitsInvocation(freshP) {
		t.Fatalf("fresh seed must permit mia write_file for the per-agent control to be meaningful; oracle answered (%q, ok=%v)", freshP, ok)
	}
	agentCfg := freshSeededConfig(t)
	var mia *config.AgentConfig
	for i := range agentCfg.Agents.List {
		if agentCfg.Agents.List[i].ID == string(coreagent.IDMia) {
			mia = &agentCfg.Agents.List[i]
			break
		}
	}
	if mia == nil {
		t.Fatal("the fresh seed carries no mia entry — the per-agent control cannot run")
	}
	if mia.Tools == nil {
		mia.Tools = &config.AgentToolsCfg{}
	}
	restricted := make(map[string]config.ToolPolicy, len(mia.Tools.Builtin.Policies)+1)
	maps.Copy(restricted, mia.Tools.Builtin.Policies)
	restricted["write_file"] = config.ToolPolicyDeny
	mia.Tools.Builtin.Policies = restricted
	agentOracle := effectivePolicyOracle(t, agentCfg)

	if p, ok := agentOracle(string(coreagent.IDMia), "write_file"); !ok || p != config.ToolPolicyDeny {
		t.Errorf("mia: per-agent write_file=deny must yield deny via strictest-wins; oracle answered (%q, ok=%v) — the oracle is not reading the real merge", p, ok)
	}
	for _, role := range roles {
		if role == string(coreagent.IDMia) {
			continue
		}
		before, okBefore := fresh(role, "write_file")
		after, okAfter := agentOracle(role, "write_file")
		if before != after || okBefore != okAfter {
			t.Errorf("role %q: mia-only restriction leaked — write_file changed from (%q, ok=%v) to (%q, ok=%v)", role, before, okBefore, after, okAfter)
		}
	}
}

// TestADR090_CapabilityLint_InvocationsPermittedByEffectiveSeededPolicy walks
// the same structural inventory as the catalog lint (every ordinary prompt,
// both system souls, every file of every embedded package — here as
// materialized by the real first-boot seeder) and checks every invocation
// mark against the seed's own runtime answer for the addressed role(s).
// Coverage is structural, not a list: anything new under embedded/ or a new
// roster entry is linted without updating this test.
func TestADR090_CapabilityLint_InvocationsPermittedByEffectiveSeededPolicy(t *testing.T) {
	catalog := lintCatalogSnapshot(t)
	policy := effectivePolicyOracle(t, freshSeededConfig(t))
	assignments := seededSkillAssignments(t)

	// Spec-derived canaries (FR-009/FR-013 tables, an INDEPENDENT oracle —
	// not read back from the seed): if SeedConfig ever stops assigning these,
	// the shared-skill rule silently checks nothing for them, and these fail.
	for skill, want := range map[string][]string{
		"interview": {"admin", "ava", "jim", "mia"},
		"plan":      {"jim", "planner", "plansupervisor"},
		"verify":    {"judge"},
	} {
		if got := assignments[skill]; !stringSlicesEqualForLint(got, want) {
			t.Errorf("seeded assignment canary for %q: got %v, want exactly %v — the capability lint's addressing for this skill changed underneath it", skill, got, want)
		}
	}

	surfaces := []capabilitySurface{}
	for _, a := range coreagent.All() {
		prompt := coreagent.GetPrompt(string(a.ID))
		if prompt == "" {
			if !coreagent.IsWorkerID(a.ID) {
				t.Fatalf("role %q has no compiled prompt to lint", a.ID)
			}
			continue
		}
		surfaces = append(surfaces, capabilitySurface{
			label: "prompt:" + string(a.ID), text: prompt,
			assigned: []string{string(a.ID)}, policy: policy,
		})
	}
	for _, sa := range coreagent.SystemAgents() {
		soul := coreagent.SystemAgentDefaultSoul(sa.ID)
		if soul == "" {
			t.Fatalf("system agent %q has no default soul to lint", sa.ID)
		}
		surfaces = append(surfaces, capabilitySurface{
			label: "soul:" + string(sa.ID), text: soul,
			assigned: []string{string(sa.ID)}, policy: policy,
		})
	}

	skillMDInvoked := map[string]bool{} // non-elicify package dir -> carried an invocation mark
	for _, f := range seededEmbeddedFiles(t) {
		surfaces = append(surfaces, capabilitySurface{
			label: f.relPath, text: f.text,
			assigned: assignments[f.pkg], policy: policy, branchesOK: true,
		})
		if f.isSkillMD && !strings.HasPrefix(f.pkg, "elicify-") {
			invocations, _ := extractCapabilityMarks(f.text)
			skillMDInvoked[f.pkg] = len(invocations) > 0
		}
	}

	// Non-vacuousness: a capability check that never engaged proves nothing.
	// Every prompt, every soul, and every non-pinned SKILL.md must carry at
	// least one INVOCATION mark (prohibitions alone exercise no permission
	// check). The elicify-* packages stay exempt: pinned upstream bytes.
	for _, s := range surfaces {
		if strings.HasPrefix(s.label, "prompt:") || strings.HasPrefix(s.label, "soul:") {
			invocations, _ := extractCapabilityMarks(s.text)
			if len(invocations) == 0 {
				t.Errorf("%s carries no marked invocation — the capability lint cannot see this surface", s.label)
			}
		}
	}
	dirs := make([]string, 0, len(skillMDInvoked))
	for dir := range skillMDInvoked {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if !skillMDInvoked[dir] {
			t.Errorf("embedded skill %q carries no marked invocation — the capability lint cannot see this surface (elicify-* pinned packages are the only exemption)", dir)
		}
	}

	for _, s := range surfaces {
		for _, finding := range lintCapabilitySurface(s, catalog) {
			t.Error(finding)
		}
	}
}

// TestADR090_CapabilityLint_DetectorNegativeFixtures proves the detector on
// a FIXED synthetic corpus with a FIXED synthetic policy table — never on
// production text and never with expectations read off production output, so
// the test cannot inherit whatever the prompts happen to say.
func TestADR090_CapabilityLint_DetectorNegativeFixtures(t *testing.T) {
	fixturePolicy := func(role, tool string) (config.ToolPolicy, bool) {
		table := map[string]map[string]config.ToolPolicy{
			"alpha": {"hammer": config.ToolPolicyAllow, "drill": config.ToolPolicyAsk, "saw": config.ToolPolicyDeny, "clamp": config.ToolPolicyAllow},
			"beta":  {"hammer": config.ToolPolicyDeny, "drill": config.ToolPolicyDeny, "saw": config.ToolPolicyAllow, "clamp": config.ToolPolicyAllow},
		}
		p, ok := table[role][tool]
		return p, ok
	}
	catalog := map[string]bool{"hammer": true, "drill": true, "saw": true, "wrench": true, "clamp": true}

	cases := []struct {
		name      string
		surface   capabilitySurface
		wantCount int // exact number of findings expected
		wantSubs  []string
	}{
		{"control clean", capabilitySurface{label: "ctl", text: "Drive `tool:hammer` and `tool:drill`; never `notool:saw`. Unmarked saw prose is out of scope.", assigned: []string{"alpha"}, policy: fixturePolicy, branchesOK: true}, 0, nil},
		{"shared invocation denied by sole assignee", capabilitySurface{label: "denied", text: "Use `tool:saw`.", assigned: []string{"alpha"}, policy: fixturePolicy, branchesOK: true}, 1, []string{"saw", "not permitted"}},
		{"unknown invocation name", capabilitySurface{label: "unk-inv", text: "Use `tool:chainsaw`.", assigned: []string{"alpha"}, policy: fixturePolicy, branchesOK: true}, 1, []string{"chainsaw"}},
		{"unknown prohibition name", capabilitySurface{label: "unk-pro", text: "Never `notool:chainsaw`.", assigned: []string{"alpha"}, policy: fixturePolicy, branchesOK: true}, 1, []string{"chainsaw"}},
		{"shared mark passes when one of two assignees permits", capabilitySurface{label: "one-of-two", text: "Use `tool:saw`.", assigned: []string{"alpha", "beta"}, policy: fixturePolicy, branchesOK: true}, 0, nil},
		{"shared mark denied by all assignees", capabilitySurface{label: "none-permit", text: "Use `tool:wrench`.", assigned: []string{"alpha", "beta"}, policy: fixturePolicy, branchesOK: true}, 1, []string{"wrench", "ANY assigned role"}},
		{"branch stricter than shared rule", capabilitySurface{label: "branch-strict", text: "branch:alpha drive `tool:saw`.", assigned: []string{"alpha", "beta"}, policy: fixturePolicy, branchesOK: true}, 1, []string{"branch alpha", "saw"}},
		{"branch names an unassigned role", capabilitySurface{label: "branch-unassigned", text: "branch:gamma use `tool:hammer`.", assigned: []string{"alpha", "beta"}, policy: fixturePolicy, branchesOK: true}, 1, []string{"gamma"}},
		{"comma branch requires every listed role", capabilitySurface{label: "comma-deny", text: "branch:alpha,beta use `tool:hammer`.", assigned: []string{"alpha", "beta"}, policy: fixturePolicy, branchesOK: true}, 1, []string{"beta", "hammer"}},
		{"comma branch clean when all permit", capabilitySurface{label: "comma-clean", text: "branch:alpha,beta use `tool:clamp`.", assigned: []string{"alpha", "beta"}, policy: fixturePolicy, branchesOK: true}, 0, nil},
		{"branch token on a single-role surface", capabilitySurface{label: "prompt:x", text: "branch:alpha use `tool:hammer`.", assigned: []string{"alpha"}, policy: fixturePolicy}, 1, []string{"one role"}},
		{"invocation with no assigned role at all", capabilitySurface{label: "orphan", text: "Use `tool:hammer`.", policy: fixturePolicy, branchesOK: true}, 1, []string{"no role is assigned"}},
		{"branch scope resets at the line break", capabilitySurface{label: "reset", text: "branch:alpha uses `tool:hammer`\nnext line `tool:saw`", assigned: []string{"alpha"}, policy: fixturePolicy, branchesOK: true}, 1, []string{"saw", "ANY assigned role"}},
	}
	for _, tc := range cases {
		findings := lintCapabilitySurface(tc.surface, catalog)
		if len(findings) != tc.wantCount {
			t.Errorf("case %q: got %d findings %v, want exactly %d", tc.name, len(findings), findings, tc.wantCount)
			continue
		}
		for _, sub := range tc.wantSubs {
			matched := false
			for _, f := range findings {
				if strings.Contains(f, sub) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("case %q: no finding contains %q (got %v)", tc.name, sub, findings)
			}
		}
	}

	// The notool/tool overlap: "notool:bash" must yield exactly one
	// prohibition and must NOT be counted as an invocation; a separate
	// tool:bash on the same line is the only invocation.
	invocations, prohibitions := extractCapabilityMarks("never notool:bash but do use tool:bash")
	if len(prohibitions) != 1 || prohibitions[0] != "bash" {
		t.Errorf("overlap fixture: prohibitions = %v, want exactly [bash]", prohibitions)
	}
	if len(invocations) != 1 || invocations[0].name != "bash" || invocations[0].roles != nil {
		t.Errorf("overlap fixture: invocations = %+v, want exactly one shared bash", invocations)
	}
}

// stringSlicesEqualForLint is set-independent order-sensitive equality for
// the sorted slices the canaries compare.
func stringSlicesEqualForLint(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
