// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agentmutation

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// SkillPolicyChanges is the skill_policy_changes patch of the agent tools
// (Skills & Tools C-21/C-23): set adds or changes overrides, remove deletes
// them, clear deletes all of them first.
type SkillPolicyChanges struct {
	Set    map[string]config.ToolPolicy
	Remove []string
	Clear  bool
}

// ApplySkillPolicyChanges applies patch to the stored overrides. checkKey
// returns the stored form of a key or its refusal (C-23); every set key is
// checked, a removed key that is stored is accepted as it is.
func ApplySkillPolicyChanges(current map[string]config.ToolPolicy, patch SkillPolicyChanges, checkKey func(string) (string, error)) (map[string]config.ToolPolicy, error) {
	out := map[string]config.ToolPolicy{}
	if !patch.Clear {
		for k, v := range current {
			out[k] = v
		}
	}
	seen := map[string]bool{}
	for key, policy := range patch.Set {
		stored, err := checkKey(key)
		if err != nil {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"skill_policy_changes.set"}, Reason: err.Error(), Message: err.Error()}
		}
		if seen[stored] {
			msg := fmt.Sprintf("`%s` is listed twice.", key)
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"skill_policy_changes.set"}, Reason: msg, Message: msg}
		}
		seen[stored] = true
		if !validPolicy(policy) {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"skill_policy_changes.set"}, Reason: fmt.Sprintf("invalid policy %q for %s: use allow, ask or deny", policy, key)}
		}
		out[stored] = policy
	}
	for _, key := range patch.Remove {
		stored := key
		if _, ok := out[key]; !ok {
			k, err := checkKey(key)
			if err != nil {
				return nil, &FieldError{Code: InvalidInput, Fields: []string{"skill_policy_changes.remove"}, Reason: err.Error(), Message: err.Error()}
			}
			stored = k
		}
		if seen[stored] {
			msg := fmt.Sprintf("`%s` is listed twice.", key)
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"skill_policy_changes"}, Reason: msg, Message: msg}
		}
		seen[stored] = true
		delete(out, stored)
	}
	return out, nil
}
