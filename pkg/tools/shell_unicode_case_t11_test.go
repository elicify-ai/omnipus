// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/stretchr/testify/require"
)

// Q21 A: Unicode simple-case-fold spellings retain both ordinary nested-script
// decisions. These are real-guard rows, not a contract on a private name helper.
// Every command below is text only; none is executed.
func TestGuard_NestedInterpreterUnicodeCaseFold(t *testing.T) {
	tool, cwd := guardFixture(t)
	t.Cleanup(t11ForceRootWritable(t, false))
	path := t11AbsentTop(t) + "/v1/users"
	bodies := []struct {
		name, script string
		allow        bool
	}{
		{"scan_required", "echo {left,right}; : '" + path + "'", false},
		{"plain_data", ": '" + path + "'", true},
	}
	for _, body := range bodies {
		lowerCommand := t11NestedInlineCommand("bash", body.script, false)
		lowerDecision := tool.guardCommand(context.Background(), lowerCommand, cwd)
		// U+017F (long s), alone and with ASCII capitals, is the independently
		// requested case-fold class. Do not use the byte-based ASCII generator.
		for _, spelling := range []string{"bash", "baſh", "BAſH"} {
			caseName := body.name + "/" + spelling
			t.Run(caseName, func(t *testing.T) {
				t11CheckNestedCase(t, tool, cwd, t11NestedCase{
					name: caseName, command: t11NestedInlineCommand(spelling, body.script, false),
					lowerCommand: lowerCommand, lowerDecision: lowerDecision, path: path, allow: body.allow,
				})
			})
		}
	}
}

// Q27 A and docs/security.md, Shell command rules: a deny-rule program appearing
// as a whole word in interpreter text is denied. The plain and folded spellings
// share the same configured-rule oracle and the real shell-tool evaluator.
func TestExecTool_CommandRulesUnicodeInterpreterDeny(t *testing.T) {
	rule := shellrule.Rule{Action: shellrule.ActionDeny, Binary: "printf"}
	tool, _ := t11CommandRuleFixture(t, []shellrule.Rule{rule})
	for _, spelling := range []string{"bash", "baſh", "BAſH"} {
		t.Run(spelling, func(t *testing.T) {
			command := spelling + ` -c "printf q27-control"`
			got := tool.EvaluateCommandRules(command)
			t11LogCommandRuleCase(t, command, rule, got)
			require.Len(t, got.Segments, 1, "Q27: the quoted script remains in one command segment")
			require.Equal(t, shellrule.ActionDeny, got.Action,
				"Q27 A: the configured deny applies equally to plain and Unicode-folded interpreter names")
			segment := got.Segments[0]
			require.Equal(t, shellrule.ActionDeny, segment.Action, "Q27: the interpreter segment itself must deny")
			require.Equal(t, &rule, segment.MatchedRule, "Q27: the exact configured rule drives the denial")
			require.False(t, segment.Blind, "Q27: this is a genuine deny match, not an unrelated unreadable command")
			require.False(t, got.FullyAllowed(), "Q27: a denied interpreter cannot be fully allowed")
		})
	}
}

// FR-040's restriction is allow-only: neither case normalization nor a stripped
// directory prefix may turn the name into an automatic allowance. The canonical
// positive control prevents a missing rule or broken fixture from passing this.
func TestExecTool_CommandRulesNormalisedNameCannotAllow(t *testing.T) {
	rule := shellrule.Rule{Action: shellrule.ActionAllow, Binary: "bash"}
	tool, bin := t11CommandRuleFixture(t, []shellrule.Rule{rule})
	cases := []struct {
		name, program string
		allow         bool
	}{
		{"canonical", "bash", true},
		{"ASCII_case", "BASH", false},
		{"Unicode_fold", "baſh", false},
		{"mixed_Unicode_fold", "BAſH", false},
		{"directory_prefix", filepath.ToSlash(filepath.Join(bin, "bash")), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			command := c.program + " q27-control"
			got := tool.EvaluateCommandRules(command)
			t11LogCommandRuleCase(t, command, rule, got)
			require.Len(t, got.Segments, 1, "FR-040: each control is one plain command segment")
			segment := got.Segments[0]
			require.False(t, segment.Blind, "fixture: an unrelated parse or resolution failure cannot prove the restriction")
			require.Equal(t, c.allow, got.FullyAllowed(), "FR-040: only the canonical plain name obtains automatic allowance")
			if c.allow {
				require.Equal(t, shellrule.ActionAllow, got.Action, "control: the canonical command must match the real allow rule")
				require.Equal(t, shellrule.ActionAllow, segment.Action, "control: the segment must receive the allow")
				require.Equal(t, &rule, segment.MatchedRule, "control: the exact configured allow rule must match")
			} else {
				require.NotEqual(t, shellrule.ActionAllow, got.Action, "FR-040: a normalized name must not obtain an allow action")
				require.NotEqual(t, shellrule.ActionAllow, segment.Action, "FR-040: the segment must not inherit the canonical allow")
				require.Nil(t, segment.MatchedRule, "FR-040: the only configured rule is ineligible for this spelling")
			}
		})
	}
}

// t11CommandRuleFixture supplies filesystem metadata at the process edge, not a
// fake parser, resolver or verdict. The inert files are never launched. Creating
// every spelling makes the conservative name contract independent of filesystem
// case folding; identical metadata also works when two names alias on disk.
func t11CommandRuleFixture(t *testing.T, rules []shellrule.Rule) (*ExecTool, string) {
	t.Helper()
	bin := t.TempDir()
	for _, spelling := range []string{"bash", "BASH", "baſh", "BAſH"} {
		err := os.WriteFile(filepath.Join(bin, spelling), []byte("metadata fixture only; never executed\n"), 0o700)
		require.NoError(t, err, "fixture: create inert executable metadata for %q", spelling)
	}
	t.Setenv("PATH", bin)
	for _, spelling := range []string{"bash", "BASH", "baſh", "BAſH"} {
		resolved, err := shellrule.ResolveBinary(spelling, bin)
		require.NoError(t, err, "fixture: %q must resolve before a rule verdict is tested", spelling)
		t.Logf("Q27_RESOLVE name=%q path=%q", spelling, resolved)
	}
	tool, err := NewExecToolWithDeps(t.TempDir(), true, nil, ExecToolDeps{CommandRules: rules})
	require.NoError(t, err, "fixture: construct the real shell tool with operator rules")
	return tool, bin
}

func t11LogCommandRuleCase(t *testing.T, command string, rule shellrule.Rule, got shellrule.CommandVerdict) {
	t.Helper()
	record, err := json.Marshal(map[string]any{"command": command, "rule": rule, "verdict": got})
	require.NoError(t, err, "fixture: record the complete evaluator result before asserting")
	t.Logf("Q27_CASE %s", record)
}
