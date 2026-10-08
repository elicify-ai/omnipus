package tools

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestResolveEffectivePolicyIfCovered(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		cfg         *ToolPolicyCfg
		wantVerdict string
		wantCovered bool
	}{
		{"nil cfg", nil, "", false},
		{"no entries", &ToolPolicyCfg{}, "", false},
		{"unrelated entry", &ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"read_file": "deny"}}, "", false},
		{"exact allow", &ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"message_parent": "allow"}}, "allow", true},
		{"global wildcard deny", &ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"message_*": "deny"}}, "deny", true},
		{"agent deny beats global allow", &ToolPolicyCfg{
			GlobalPolicies: map[string]config.ToolPolicy{"message_parent": "allow"},
			Policies:       map[string]config.ToolPolicy{"message_parent": "deny"},
		}, "deny", true},
		{"god mode covers", &ToolPolicyCfg{GodMode: true}, "allow", true},
	}
	for _, tc := range cases {
		got, covered := ResolveEffectivePolicyIfCovered(tc.cfg, "message_parent")
		if got != tc.wantVerdict || covered != tc.wantCovered {
			t.Errorf("%s: got (%q,%v), want (%q,%v)", tc.name, got, covered, tc.wantVerdict, tc.wantCovered)
		}
	}
}
