// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 18.
// TestSetConfig_RefusesSandboxSkillKeys: set_config cannot change the global
// skill settings (S-73) — the C-2 keys live under the sandbox namespace, and
// the sandbox subtree block refuses every write there with the sandbox
// reason, changing nothing.
//
// Oracle: skills-tools-redesign-spec.md S-73 ("When it sets
// sandbox.skill_policies.plan to allow, the call is refused with the sandbox
// reason and nothing changes") and the C-2 key names
// (sandbox.skill_policies / sandbox.skill_visibility / sandbox.skill_trust).
// The existing guard is pkg/sysagent/tools/config.go::blockedConfigKeys'
// sandbox subtree block — this test pins that the NEW keys are refused by it
// (they are new members of an already-refused namespace).
//
// Note for the RED report: the refusal exists at this commit, so this test
// may pass today; it pins the guard rather than new behaviour (the same
// guard-row shape as order 14's S-23 subtest). GREEN adds the config fields
// themselves, which moves any unknown-key refusal to the sandbox reason.

import (
	"context"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"

	"github.com/stretchr/testify/require"
)

// TestSetConfig_RefusesSandboxSkillKeys (order 18, S-73).
// Traces to: BDD S-73; spec C-2 key names under the sandbox namespace.
func TestSetConfig_RefusesSandboxSkillKeys(t *testing.T) {
	const wantReason = "sandbox namespace is the enforcement boundary"

	for _, tc := range []struct {
		key   string
		value any
	}{
		{"sandbox.skill_policies.plan", "allow"},
		{"sandbox.skill_policies.deep-research", "deny"},
		{"sandbox.skill_visibility.plan", "name_only"},
		{"sandbox.skill_trust", "allow"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			deps, cfg := newTestDepsWithCredStore(t)
			set := systools.NewConfigSetTool(deps)
			res := set.Execute(context.Background(), map[string]any{
				"key":   tc.key,
				"value": tc.value,
			})
			require.True(t, res.IsError,
				"S-73: set_config on %s must be refused; got success: %s", tc.key, res.ForLLM)
			require.Contains(t, res.ForLLM, wantReason,
				"the refusal must name the sandbox reason (S-73); got: %s", res.ForLLM)
			// Nothing changed: the global ceiling and any skill maps are
			// untouched by the refused write. SkillTrust ships a non-empty
			// default (warn_unverified), so the unchanged pin is that the
			// refused value never landed.
			_, hasSkillPolicies := cfg.Sandbox.ToolPolicies["plan"]
			require.False(t, hasSkillPolicies,
				"a refused write must not leak into the tool-policy ceiling")
			require.NotEqual(t, tc.value, string(cfg.Sandbox.SkillTrust),
				"a refused write must not set the trust value")
		})
	}
}
