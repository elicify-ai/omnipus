package tools

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 1.
// TestSkillPolicyResolution_Table: the per-skill decision of C-11 as a table
// over Dataset D1.
//
// Oracles: skills-tools-redesign-spec.md C-0 (two layers, strictest wins),
// C-3 (trust floor only for unverified skills; survives God Mode), C-11
// (grant → global × agent strictest-wins → God Mode floor on the global side
// → trust floor; outcome deny | ask_pending | allowed) and Dataset D1 rows
// 1–14. Rows 15–16 (upgrade) are order 3 (TestSkillPolicyUpgrade_FromSkillTool).
//
// The production resolver ("one function beside
// pkg/tools/compositor.go::resolveEffectivePolicyWith", C-11) does not exist
// at this commit. Per the qa-lead RED rule the test is written anyway and
// fails LOUD with BLOCKED until the symbol lands; skillOutcomeOf below is the
// test-local seam the fix wires to the production entry point. Expected
// values below come from the spec's datasets, never from the implementation.

import (
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
)

// skillOutcomeInput is one Dataset D1 input row: (global, agent override,
// trust, verified, God Mode) plus the grant gate that C-11 puts first.
// Values are the spec's stored vocabulary: "allow" | "ask" | "deny" | ""
// ("" = no stored entry on that side).
type skillOutcomeInput struct {
	name        string
	granted     bool
	global      string
	agentValue  string
	trust       string
	verified    bool
	godMode     bool
	wantOutcome string // "allow" | "ask_pending" | "deny" (C-11 outcome set)
	wantRefusal string // non-empty: the refusal reason the outcome must name
}

// d1ResolutionTable is Dataset D1 rows 1–14, transcribed from the spec.
// Every expected value cites its row in the spec's dataset.
var d1ResolutionTable = []skillOutcomeInput{
	{
		name: "D1#1 trust ignored for a verified skill (S-13)",
		// allow, none, ask, yes, off -> allow
		granted: true, global: "allow", agentValue: "", trust: "ask", verified: true, godMode: false,
		wantOutcome: "allow",
	},
	{
		name: "D1#2 agent stricter than global wins (S-09)",
		// allow, deny, ask, yes, off -> deny
		granted: true, global: "allow", agentValue: "deny", trust: "ask", verified: true, godMode: false,
		wantOutcome: "deny",
	},
	{
		name: "D1#3 stored looser agent allow is floored to global ask (S-19)",
		// ask, allow, ask, yes, off -> ask
		granted: true, global: "ask", agentValue: "allow", trust: "ask", verified: true, godMode: false,
		wantOutcome: "ask_pending",
	},
	{
		name: "D1#4 global deny wins over agent allow (S-19)",
		// deny, none, allow, yes, off -> deny
		granted: true, global: "deny", agentValue: "", trust: "allow", verified: true, godMode: false,
		wantOutcome: "deny",
	},
	{
		name: "D1#5 trust deny refuses an unverified skill (S-29)",
		// allow, none, deny, no, off -> deny
		granted: true, global: "allow", agentValue: "", trust: "deny", verified: false, godMode: false,
		wantOutcome: "deny",
	},
	{
		name: "D1#6 trust ask floors an unverified skill to ask (S-30)",
		// allow, none, ask, no, off -> ask
		granted: true, global: "allow", agentValue: "", trust: "ask", verified: false, godMode: false,
		wantOutcome: "ask_pending",
	},
	{
		name: "D1#7 trust allow leaves the permission outcome (S-32)",
		// allow, none, allow, no, off -> allow
		granted: true, global: "allow", agentValue: "", trust: "allow", verified: false, godMode: false,
		wantOutcome: "allow",
	},
	{
		name: "D1#8 God Mode floors a global ask (S-31)",
		// ask, none, allow, no, on -> allow
		granted: true, global: "ask", agentValue: "", trust: "allow", verified: false, godMode: true,
		wantOutcome: "allow",
	},
	{
		name: "D1#9 trust floor survives God Mode (S-31, ST-Q2 A)",
		// allow, none, ask, no, on -> ask (trust card)
		granted: true, global: "allow", agentValue: "", trust: "ask", verified: false, godMode: true,
		wantOutcome: "ask_pending",
	},
	{
		name: "D1#10 per-agent value survives God Mode (S-19)",
		// allow, ask, ask, yes, on -> ask
		granted: true, global: "allow", agentValue: "ask", trust: "ask", verified: true, godMode: true,
		wantOutcome: "ask_pending",
	},
	{
		name: "D1#11 built-in skill loads under trust deny (S-35)",
		// allow, none, deny, yes (built-in), off -> allow
		granted: true, global: "allow", agentValue: "", trust: "deny", verified: true, godMode: false,
		wantOutcome: "allow",
	},
	{
		name: "D1#12 God Mode floors a global deny; agent allow then holds (S-76)",
		// deny, allow, allow, yes, on -> allow
		granted: true, global: "deny", agentValue: "allow", trust: "allow", verified: true, godMode: true,
		wantOutcome: "allow",
	},
	{
		name: "D1#13 no entry on either side denies (tripwire, S-17)",
		// missing, none, ask, yes, off -> deny (+ Error log; the log tripwire
		// is exercised at the entry point — the C-2 self-heal (order 2)
		// removes the missing-entry state entirely)
		granted: true, global: "", agentValue: "", trust: "ask", verified: true, godMode: false,
		wantOutcome: "deny",
	},
	{
		name: "D1#14 a skill not granted is refused whatever its permission (S-14)",
		// allow, none, ask, yes, not granted -> refused: not granted.
		// C-11 puts the grant gate first, so the resolver's outcome is deny;
		// the entry point (E1) words the refusal as not granted.
		granted: false, global: "allow", agentValue: "", trust: "ask", verified: true, godMode: false,
		wantOutcome: "deny",
		wantRefusal: "not granted",
	},
}

// skillOutcomeOf is the test-local seam for the C-11 resolver
// ("one function beside pkg/tools/compositor.go::resolveEffectivePolicyWith").
// The GREEN step wires it to the production entry point; it stays nil here so
// the RED run fails with BLOCKED instead of panicking.
// Only raw inputs cross the native boundary. The spec's expected outcome and
// refusal stay in the test table and are never handed to a resolver adapter.
var skillOutcomeOf func(granted bool, global, agentValue, trust string, verified, godMode bool) (outcome string, refusal string)

// TestSkillPolicyResolution_Table drives Dataset D1 rows 1–14 through the
// C-11 per-skill decision.
// Traces to: BDD S-09, S-13, S-14, S-19, S-29, S-30, S-31, S-35; Dataset D1.
func TestSkillPolicyResolution_Table(t *testing.T) {
	nativebinding.Require(t, "pkg/tools", skillOutcomeOf,
		"C-0/C-3/C-11/D1: actual per-skill resolver; post-GREEN every-consumer re-audit required")
	for _, tc := range d1ResolutionTable {
		t.Run(tc.name, func(t *testing.T) {
			outcome, refusal := skillOutcomeOf(tc.granted, tc.global, tc.agentValue, tc.trust, tc.verified, tc.godMode)
			if outcome != tc.wantOutcome {
				t.Fatalf("outcome = %q, want %q (spec Dataset %s)", outcome, tc.wantOutcome, tc.name)
			}
			if tc.wantRefusal != "" && refusal == "" {
				t.Fatalf("refusal reason empty, want it to name %q (spec Dataset %s)", tc.wantRefusal, tc.name)
			}
			if tc.wantRefusal == "" && refusal != "" {
				t.Fatalf("refusal reason = %q, want none (spec Dataset %s)", refusal, tc.name)
			}
		})
	}
}

// skillEntryPointRows is C-11's entry-point table (E1-E9) as the spec names the
// entry points, with the real-path observer this pack already has for the row
// (verified by test name and header comment), or "none cited".
var skillEntryPointRows = []struct {
	id, label, entry, observer string
}{
	{"E1", "skill load", "pkg/tools/skill.go::SkillTool.execLoad -> pkg/agent/skill_activation.go::loadSkillActivation",
		"pkg/agent TestSkillLoad_AskUnattendedRefused (unattended; D1#10, D1#11, S-13)"},
	{"E2", "skill menu", "pkg/skills/loader.go::BuildSkillsSummaryFuncWithProject",
		"pkg/agent TestVisibilityTransitions row 6 (level only); none cited for the deny outcome"},
	{"E3", "skill search", "pkg/tools/skill.go::SkillTool.execSearch", "none cited"},
	{"E4", "slash command", "pkg/agent/loop_slash.go::applyExplicitSkillCommand", "none cited"},
	{"E5", "delegation requested_skill", "pkg/agent/subturn_identity.go::resolveRequestedSkillForChild", "none cited"},
	{"E6", "file tools on a skill folder", "pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority",
		"pkg/tools TestSkillAuthority_FoldersFollowOutcome (S-85, S-86, S-87)"},
	{"E7", "helper execution", "pkg/tools/shell_skill_operands.go::loadableSkillReadOrRun",
		"pkg/tools TestSkillAuthority_FoldersFollowOutcome (S-85, S-86, S-87)"},
	{"E8", "Full level", "C-1 Full level, C-14", "pkg/agent TestFullSkills_SentinelAndBudget (D9 rows 5-10)"},
	{"E9", "unified search", "C-17 unified search", "pkg/agent TestToolSearch_FindsSkills (S-96)"},
}

// TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome is the native-path
// binding obligation for skillOutcomeOf (CHECK round 4, false-green table row
// 23). TestSkillPolicyResolution_Table drives Dataset D1 through one adapter
// with the grant, global, agent, trust, verification and God Mode inputs handed
// in; it cannot tell whether any of the nine entry points of C-11 reaches the
// function the adapter binds to, so a resolver only that table calls passes it
// while each entry point decides on its own. FR-066: every entry point E1-E9
// MUST use the single per-skill outcome. This can only be checked after GREEN,
// so each row is a loud BLOCKED leaf (never a skip); the test author replaces
// each at test-side reconciliation with a check that seeds a stored per-skill
// outcome (never the adapter) and enters through that entry point, and CHECK
// then mutates the bound function and requires the check of every row to fail.
func TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome(t *testing.T) {
	for _, row := range skillEntryPointRows {
		t.Run(row.id+" "+row.label, func(t *testing.T) {
			t.Fatalf("BLOCKED: %s (%s) is not shown to take its decision from the one "+
				"function skillOutcomeOf binds to — required by C-11 %s and FR-066 (every "+
				"entry point E1-E9 MUST use the single per-skill outcome); a resolver only "+
				"the D1 table calls must never turn this green; known real-path observer: %s",
				row.id, row.entry, row.id, row.observer)
		})
	}
}
