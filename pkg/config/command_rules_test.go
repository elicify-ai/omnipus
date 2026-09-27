// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

func TestValidateCommandRules(t *testing.T) {
	ok := []shellrule.Rule{
		{Action: shellrule.ActionDeny, Binary: "rm"},
		{Action: shellrule.ActionAsk, Binary: "git", ArgPrefix: "push"},
		{Action: shellrule.ActionAllow, Binary: "/usr/bin/npm", ArgPrefix: "run test"},
	}
	if err := ValidateCommandRules(ok); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
	if err := ValidateCommandRules(nil); err != nil {
		t.Fatalf("empty rule list rejected: %v", err)
	}

	bad := []struct {
		name string
		rule shellrule.Rule
		want string
	}{
		{"capitalised action", shellrule.Rule{Action: "Deny", Binary: "rm"}, `action "Deny"`},
		{"empty action", shellrule.Rule{Binary: "rm"}, "action"},
		{"missing binary", shellrule.Rule{Action: shellrule.ActionDeny}, "binary is required"},
		{"binary with space", shellrule.Rule{Action: shellrule.ActionDeny, Binary: "rm -rf"}, "shell metacharacters"},
		{"binary with pipe", shellrule.Rule{Action: shellrule.ActionDeny, Binary: "a|b"}, "shell metacharacters"},
		{"relative path", shellrule.Rule{Action: shellrule.ActionDeny, Binary: "bin/rm"}, "relative path"},
		{"control char in prefix", shellrule.Rule{Action: shellrule.ActionDeny, Binary: "git", ArgPrefix: "push\n"}, "control characters"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			rules := []shellrule.Rule{ok[0], tc.rule}
			err := ValidateCommandRules(rules)
			if err == nil {
				t.Fatalf("invalid rule accepted: %+v", tc.rule)
			}
			if !strings.Contains(err.Error(), "command_rules[1]") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q must name command_rules[1] and contain %q", err, tc.want)
			}
		})
	}

	tooMany := make([]shellrule.Rule, MaxCommandRules+1)
	for i := range tooMany {
		tooMany[i] = ok[0]
	}
	if err := ValidateCommandRules(tooMany); err == nil {
		t.Fatal("more than MaxCommandRules rules accepted")
	}
}

// TestLoadConfig_RejectsInvalidCommandRule proves the validator runs on the
// real load path, so a hand-edited config.json with a typo fails loudly.
func TestLoadConfig_RejectsInvalidCommandRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"version":1,"sandbox":{"command_rules":[{"action":"deny","binary":"rm"},{"action":"Deny","binary":"curl"}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("config with an invalid command rule loaded")
	}
	if !strings.Contains(err.Error(), "sandbox.command_rules[1]") {
		t.Fatalf("load error %q must name the bad rule", err)
	}
}

func TestLoadConfig_LoadsCommandRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"version":1,"sandbox":{"command_rules":[{"action":"ask","binary":"git","arg_prefix":"push"}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := shellrule.Rule{Action: shellrule.ActionAsk, Binary: "git", ArgPrefix: "push"}
	if len(cfg.Sandbox.CommandRules) != 1 || cfg.Sandbox.CommandRules[0] != want {
		t.Fatalf("CommandRules = %+v, want [%+v]", cfg.Sandbox.CommandRules, want)
	}
}
