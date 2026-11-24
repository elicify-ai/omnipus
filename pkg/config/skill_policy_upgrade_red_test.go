// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 3.
// TestSkillPolicyUpgrade_FromSkillTool: the C-19 step 2/3 translation of the
// old blanket `Skill` tool values into per-skill stored values.
//
// Oracles: skills-tools-redesign-spec.md S-16 Scenario Outline examples,
// C-19 step 3 ("each skill's global value = the old global `Skill` value")
// and step 4 ("its old `Skill` value for each skill granted to it and each
// project skill of its mounts"), and Dataset D1 rows 15–16. The stored
// per-agent value is written AS GIVEN (C-4: "stored as given ... and floored
// at run time"), so S-16 row 3 stores the looser allow and only the runtime
// floor makes it ask.
//
// The production upgrade plan does not exist at this commit. Per the qa-lead
// RED rule the test fails LOUD with BLOCKED until it lands;
// skillPermissionsUpgradePlanOf is the test-local seam the fix wires to.

import (
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
)

// upgradePlanInput is one pre-upgrade state: the old blanket `Skill` values
// plus which skills exist and which the agent holds.
type upgradePlanInput struct {
	name string
	// oldGlobalSkillValue is the stored global tool policy of the `Skill`
	// tool before the upgrade ("" = no stored entry).
	oldGlobalSkillValue string
	// oldAgentSkillValue is Mia's stored per-agent `Skill` value ("" = none).
	oldAgentSkillValue string
	// installedSkills / grantedSkills name the skill keys in play.
	installedSkills []string
	grantedSkills   []string
	// wantGlobal: every installed skill's expected global value after step 3.
	wantGlobal map[string]string
	// wantAgentOverrides: Mia's expected skills.policies after step 4
	// (nil = no overrides written).
	wantAgentOverrides map[string]string
}

// skillPermissionsUpgradePlanOf is the test-local seam for the C-19
// upgrade plan (steps 2–4, computed in memory from the snapshot alone).
// The GREEN step wires it to the production entry point; it stays nil here
// so the RED run fails with BLOCKED instead of panicking.
// Expected maps and case names never enter the production planner.
var skillPermissionsUpgradePlanOf func(oldGlobal, oldAgent string, installed, granted []string) (global, agentOverrides map[string]string)

// TestSkillPolicyUpgrade_FromSkillTool drives S-16's four examples plus
// Dataset D1 rows 15–16.
// Traces to: BDD S-16; spec C-19 steps 3–4; Dataset D1 rows 15–16.
func TestSkillPolicyUpgrade_FromSkillTool(t *testing.T) {
	nativebinding.Require(t, "pkg/config", skillPermissionsUpgradePlanOf,
		"C-19/S-16/D1#15-16: native snapshot upgrade planner; post-GREEN boot-path re-audit required")
	cases := []upgradePlanInput{
		{
			// S-16 example row 1 / D1#15 variant: allow, none
			name:                "S-16 r1 old global allow, agent none",
			oldGlobalSkillValue: "allow",
			oldAgentSkillValue:  "",
			installedSkills:     []string{"plan", "deep-research"},
			grantedSkills:       []string{"plan", "deep-research"},
			wantGlobal:          map[string]string{"plan": "allow", "deep-research": "allow"},
			wantAgentOverrides:  nil,
		},
		{
			// S-16 example row 2 / D1#16: allow, deny
			name:                "S-16 r2 old global allow, agent deny (granted skills)",
			oldGlobalSkillValue: "allow",
			oldAgentSkillValue:  "deny",
			installedSkills:     []string{"plan", "deep-research"},
			grantedSkills:       []string{"plan", "deep-research"},
			wantGlobal:          map[string]string{"plan": "allow", "deep-research": "allow"},
			wantAgentOverrides:  map[string]string{"plan": "deny", "deep-research": "deny"},
		},
		{
			// D1#15: Skill global ask, agent none -> every skill global ask
			name:                "D1#15 upgrade: Skill global ask, agent none",
			oldGlobalSkillValue: "ask",
			oldAgentSkillValue:  "",
			installedSkills:     []string{"plan"},
			grantedSkills:       []string{"plan"},
			wantGlobal:          map[string]string{"plan": "ask"},
			wantAgentOverrides:  nil,
		},
		{
			// S-16 example row 3: ask, allow — the looser allow is STORED and
			// only floored to ask at run time (C-4 "stored as given").
			name:                "S-16 r3 old global ask, agent allow stored as given",
			oldGlobalSkillValue: "ask",
			oldAgentSkillValue:  "allow",
			installedSkills:     []string{"plan"},
			grantedSkills:       []string{"plan"},
			wantGlobal:          map[string]string{"plan": "ask"},
			wantAgentOverrides:  map[string]string{"plan": "allow"},
		},
		{
			// S-16 example row 4: deny, none
			name:                "S-16 r4 old global deny, agent none",
			oldGlobalSkillValue: "deny",
			oldAgentSkillValue:  "",
			installedSkills:     []string{"plan"},
			grantedSkills:       []string{"plan"},
			wantGlobal:          map[string]string{"plan": "deny"},
			wantAgentOverrides:  nil,
		},
		{
			// C-19 step 4: only granted skills get the agent's old value;
			// a granted-after-the-upgrade skill follows the global value.
			name:                "C-19 step 4 ungranted installed skill gets no agent override",
			oldGlobalSkillValue: "deny",
			oldAgentSkillValue:  "deny",
			installedSkills:     []string{"plan", "verify"},
			grantedSkills:       []string{"plan"},
			wantGlobal:          map[string]string{"plan": "deny", "verify": "deny"},
			wantAgentOverrides:  map[string]string{"plan": "deny"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotGlobal, gotAgent := skillPermissionsUpgradePlanOf(tc.oldGlobalSkillValue, tc.oldAgentSkillValue, tc.installedSkills, tc.grantedSkills)
			assertStringMap(t, "global skill_policies plan", gotGlobal, tc.wantGlobal)
			assertStringMap(t, "agent skills.policies plan", gotAgent, tc.wantAgentOverrides)
		})
	}
}

// TestSkillPolicyUpgrade_StartupWritesThePlan is the native-path binding
// obligation for skillPermissionsUpgradePlanOf (CHECK round 4, false-green
// table row 7). TestSkillPolicyUpgrade_FromSkillTool reads an adapter that
// computes the plan from raw inputs; it cannot show that the real C-19 upgrade
// (steps 3 and 4) writes that plan, so a planner only the adapter calls passes
// it. C-19 step 2 says every target value is computed in memory from the
// snapshot alone. This can only be checked after GREEN, so it is a loud BLOCKED
// leaf (never a skip). Real-path observer today: TestSkillPermissionsUpgrade_StopAtEveryStep
// runs the real startup upgrader through its startSkillUpgradeAtBoot adapter
// on a literal old home and reads the persisted global skill_policies and Mia's
// skills.policies from disk; it is unbound (loud BLOCKED) until GREEN. The test
// author reconciles both at test-side reconciliation, and CHECK then mutates
// the bound planner and requires that observer to fail.
func TestSkillPolicyUpgrade_StartupWritesThePlan(t *testing.T) {
	t.Fatal("BLOCKED: the real C-19 upgrade (steps 3 and 4, run at startup " +
		"before any turn) is not shown to write the values of the function " +
		"skillPermissionsUpgradePlanOf binds to, computed from the snapshot " +
		"alone — required by C-19 steps 2-4, S-16 and Dataset D1 rows 15-16; a " +
		"planner only this adapter calls must never turn this green")
}
