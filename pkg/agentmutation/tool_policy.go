package agentmutation

import (
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

var ErrCeilingChanged = errors.New("agentmutation: global ceiling changed")

type ToolPolicyChanges struct {
	Set    map[string]config.ToolPolicy
	Remove []string
}

func validPolicy(p config.ToolPolicy) bool {
	return p == config.ToolPolicyAllow || p == config.ToolPolicyAsk || p == config.ToolPolicyDeny
}

func ApplyToolPolicyChanges(current map[string]config.ToolPolicy, patch ToolPolicyChanges, known map[string]struct{}) (map[string]config.ToolPolicy, error) {
	seenRemove := make(map[string]struct{}, len(patch.Remove))
	for _, name := range patch.Remove {
		if _, exists := seenRemove[name]; exists {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"tool_policy_changes.remove"}, Reason: "duplicate removal: " + name}
		}
		seenRemove[name] = struct{}{}
		if _, exists := known[name]; !exists {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"tool_policy_changes.remove"}, Reason: "unknown tool: " + name}
		}
		if _, overlap := patch.Set[name]; overlap {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"tool_policy_changes"}, Reason: "tool appears in both set and remove: " + name}
		}
	}
	for name, policy := range patch.Set {
		if _, exists := known[name]; !exists {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"tool_policy_changes.set"}, Reason: "unknown tool: " + name}
		}
		if !validPolicy(policy) {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"tool_policy_changes.set"}, Reason: fmt.Sprintf("invalid policy %q for %s", policy, name)}
		}
	}
	out := make(map[string]config.ToolPolicy, len(current)+len(patch.Set))
	for name, policy := range current {
		out[name] = policy
	}
	for name, policy := range patch.Set {
		out[name] = policy
	}
	for _, name := range patch.Remove {
		delete(out, name)
	}
	return out, nil
}

func SelectOverrides(complete map[string]config.ToolPolicy, overrideNames []string, ceiling map[string]config.ToolPolicy) (map[string]config.ToolPolicy, error) {
	if len(complete) != len(ceiling) {
		return nil, &FieldError{Code: InvalidInput, Fields: []string{"config.builtin.policies"}, Reason: "complete policy map required"}
	}
	for name, global := range ceiling {
		value, ok := complete[name]
		if !ok || !validPolicy(value) {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"config.builtin.policies"}, Reason: "missing or invalid tool: " + name}
		}
		_ = global
	}
	overrides := make(map[string]config.ToolPolicy, len(overrideNames))
	selected := make(map[string]struct{}, len(overrideNames))
	for _, name := range overrideNames {
		if _, duplicate := selected[name]; duplicate {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"override_names"}, Reason: "duplicate override: " + name}
		}
		selected[name] = struct{}{}
		value, ok := complete[name]
		if !ok {
			return nil, &FieldError{Code: InvalidInput, Fields: []string{"override_names"}, Reason: "unknown override: " + name}
		}
		overrides[name] = value
	}
	for name, global := range ceiling {
		if _, explicit := selected[name]; explicit {
			continue
		}
		if complete[name] != global {
			return nil, fmt.Errorf("%w: inherited echo for %s is %q, current ceiling is %q", ErrCeilingChanged, name, complete[name], global)
		}
	}
	return overrides, nil
}
