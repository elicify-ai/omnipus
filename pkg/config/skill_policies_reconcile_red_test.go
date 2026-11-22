// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 2.
// TestSkillPolicyReconcile_NewSkillAsk: the C-2 self-heal for the skill maps
// ("same pattern as ReconcileToolPolicyCeiling, additive, never overwriting").
//
// Oracle: skills-tools-redesign-spec.md C-2 self-heal paragraph and S-17 /
// ST-Q3 B / ST-Q10 A — a skill key with no stored global permission gets
// "ask", EXCEPT a built-in skill shipped by a newer Omnipus release which
// gets "allow" (its shipped default); a skill with no stored level gets
// "name_and_description"; an existing operator value is never overwritten;
// a second run adds nothing (idempotent); retired keys are not re-added.
//
// The production reconciler does not exist at this commit. Per the qa-lead
// RED rule the test fails LOUD with BLOCKED until it lands;
// reconcileSkillMapsOf is the test-local seam the fix wires to the
// production entry point.

import "testing"

// reconcileSkillInput is one self-heal scenario: the stored skill maps plus
// the installed skills the reconciler runs over.
type reconcileSkillInput struct {
	name string
	// stored maps (nil = absent from config)
	storedSkillPolicies   map[string]string // sandbox.skill_policies
	storedSkillVisibility map[string]string // sandbox.skill_visibility
	// installed skills seen at load; builtinNewRelease marks a built-in skill
	// shipped by a newer Omnipus release (ST-Q10 A)
	installed []installedSkillInfo
	// expected maps after reconcile (complete equality)
	wantSkillPolicies   map[string]string
	wantSkillVisibility map[string]string
}

type installedSkillInfo struct {
	key               string // C-23 key: skill id or project:<W>/<slug>
	builtinNewRelease bool
}

// reconcileSkillMapsOf is the test-local seam for the C-2 self-heal
// (the skills twin of ReconcileToolPolicyCeiling). The GREEN step wires it
// to the production entry point; it stays nil here so the RED run fails
// with BLOCKED instead of panicking.
var reconcileSkillMapsOf func(storedPolicies, storedVisibility map[string]string, installed []installedSkillInfo) (policies, visibility map[string]string)

// TestSkillPolicyReconcile_NewSkillAsk drives the C-2 self-heal rows.
// Traces to: BDD S-17; spec C-2 (self-heal), ST-Q3 B, ST-Q10 A.
func TestSkillPolicyReconcile_NewSkillAsk(t *testing.T) {
	if reconcileSkillMapsOf == nil {
		t.Fatal("BLOCKED: the C-2 skill-map self-heal is not implemented — " +
			"required by skills-tools-redesign-spec C-2 (self-heal, same pattern " +
			"as ReconcileToolPolicyCeiling): a skill key with no stored global " +
			"permission gets ask (ST-Q3 B; built-in of a newer release gets its " +
			"shipped allow, ST-Q10 A) and no stored level gets " +
			"name_and_description; S-17")
	}
	cases := []reconcileSkillInput{
		{
			name:                "S-17 newly installed skill starts at ask (ST-Q3 B)",
			storedSkillPolicies: map[string]string{"plan": "deny"},
			installed:           []installedSkillInfo{{key: "plan"}, {key: "weather"}},
			wantSkillPolicies:   map[string]string{"plan": "deny", "weather": "ask"},
			wantSkillVisibility: map[string]string{"plan": "name_and_description", "weather": "name_and_description"},
		},
		{
			name:                "ST-Q10 A built-in skill of a newer release starts at allow",
			storedSkillPolicies: map[string]string{},
			installed:           []installedSkillInfo{{key: "new-builtin", builtinNewRelease: true}},
			wantSkillPolicies:   map[string]string{"new-builtin": "allow"},
			wantSkillVisibility: map[string]string{"new-builtin": "name_and_description"},
		},
		{
			name: "reconcile never overwrites an operator-set value",
			storedSkillPolicies: map[string]string{
				"plan": "deny",
			},
			storedSkillVisibility: map[string]string{"plan": "hidden"},
			installed:             []installedSkillInfo{{key: "plan"}},
			wantSkillPolicies:     map[string]string{"plan": "deny"},
			wantSkillVisibility:   map[string]string{"plan": "hidden"},
		},
		{
			name:                "second run adds nothing (idempotent)",
			storedSkillPolicies: map[string]string{"weather": "ask"},
			storedSkillVisibility: map[string]string{
				"weather": "name_and_description",
			},
			installed:           []installedSkillInfo{{key: "weather"}},
			wantSkillPolicies:   map[string]string{"weather": "ask"},
			wantSkillVisibility: map[string]string{"weather": "name_and_description"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotPolicies, gotVisibility := reconcileSkillMapsOf(tc.storedSkillPolicies, tc.storedSkillVisibility, tc.installed)
			assertStringMap(t, "skill_policies", gotPolicies, tc.wantSkillPolicies)
			assertStringMap(t, "skill_visibility", gotVisibility, tc.wantSkillVisibility)
		})
	}
}

// assertStringMap compares got/want with per-key messages so a failure names
// the drifted key, not just the map.
func assertStringMap(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s[%q] = %q, want %q (spec C-2 self-heal)", what, k, got[k], v)
		}
	}
	for k, v := range got {
		if _, ok := want[k]; !ok {
			t.Fatalf("%s has unexpected key %q = %q (spec C-2 self-heal)", what, k, v)
		}
	}
	if (got == nil) != (want == nil) && len(want) > 0 {
		t.Fatalf("%s is nil, want entries (spec C-2 self-heal)", what)
	}
}
