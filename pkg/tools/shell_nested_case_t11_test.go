// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Q21 A: ordinary nested-script decisions are invariant under letter case for
// recognized shell/eval names, not for merely similar words. These fixtures
// call the real guard only; no command is executed.
type t11NestedCase struct {
	name, command, lowerCommand, lowerDecision, path string
	allow                                            bool
}

func t11CheckNestedCase(t *testing.T, tool *ExecTool, cwd string, c t11NestedCase) {
	t.Helper()
	got := tool.guardCommand(context.Background(), c.command, cwd)
	record, err := json.Marshal(map[string]any{
		"name": c.name, "command": c.command, "lower_command": c.lowerCommand,
		"lower_decision": c.lowerDecision, "actual": got, "allow": c.allow,
	})
	require.NoError(t, err, "fixture: record complete lowercase and variant decisions")
	t.Logf("Q21_CASE %s", record)
	t11CheckHereDocRuleCase(t, tool, cwd, t11HereDocRuleCase{
		c.name, c.command, c.allow, false, "path outside working dir", c.path,
	})
	require.Equal(t, c.lowerDecision, got,
		"Q21 A: changing only a recognized name's letter case preserves the COMPLETE lowercase decision")
}

func t11NestedInlineCommand(program, script string, evalSyntax bool) string {
	if evalSyntax {
		return program + ` "` + script + `"`
	}
	return program + ` -c "` + script + `"`
}

func t11InterpreterCaseSpellings(word string) []string {
	out := make([]string, 0, 1<<len(word))
	for bits := 0; bits < 1<<len(word); bits++ {
		letters := []byte(word)
		for i := range letters {
			if bits&(1<<i) != 0 {
				letters[i] -= 'a' - 'A'
			}
		}
		out = append(out, string(letters))
	}
	return out
}

func TestGuard_NestedInterpreterLetterCase(t *testing.T) {
	tool, cwd := guardFixture(t)
	t.Cleanup(t11ForceRootWritable(t, false))
	path := t11AbsentTop(t) + "/v1/users"
	names := []string{"sh", "bash", "zsh", "dash", "ksh", "mksh", "ash", "fish", "eval"}
	listed := make([]string, 0, len(braceExpandingShells)+1)
	for name, recognized := range braceExpandingShells {
		require.True(t, recognized, "fixture: listed interpreter is recognized")
		listed = append(listed, name)
	}
	listed = append(listed, "eval")
	require.ElementsMatch(t, names, listed, "Q21 A: cover every recognized ordinary shell/eval name")
	bodies := []struct {
		name, script string
		allow        bool
	}{
		{"scan required", "echo {left,right}; : '" + path + "'", false},
		{"plain data", ": '" + path + "'", true},
	}
	for _, body := range bodies {
		for _, name := range names {
			lowerCommand := t11NestedInlineCommand(name, body.script, name == "eval")
			lowerDecision := tool.guardCommand(context.Background(), lowerCommand, cwd)
			spellings := t11InterpreterCaseSpellings(name)
			require.Len(t, spellings, 1<<len(name), "fixture: enumerate every letter-case combination")
			seen := make(map[string]bool, len(spellings))
			for _, spelling := range spellings {
				require.Equal(t, name, strings.ToLower(spelling), "fixture: only letter case changes")
				require.False(t, seen[spelling], "fixture: each case combination occurs exactly once")
				seen[spelling] = true
				caseName := body.name + "/" + name + "/" + spelling
				t.Run(caseName, func(t *testing.T) {
					t11CheckNestedCase(t, tool, cwd, t11NestedCase{
						name: caseName, command: t11NestedInlineCommand(spelling, body.script, name == "eval"),
						lowerCommand: lowerCommand, lowerDecision: lowerDecision, path: path, allow: body.allow,
					})
				})
			}
		}
	}
}

func TestGuard_NestedInterpreterSimilarWords(t *testing.T) {
	tool, cwd := guardFixture(t)
	t.Cleanup(t11ForceRootWritable(t, false))
	path := t11AbsentTop(t) + "/v1/users"
	script := "echo {left,right}; : '" + path + "'"
	words := []struct {
		name       string
		evalSyntax bool
	}{
		{"bashful", false},
		{"prebash", false},
		{"prebashpost", false},
		{"evaluation", true},
		{"uneval", true},
	}
	for _, word := range words {
		lowerCommand := t11NestedInlineCommand(word.name, script, word.evalSyntax)
		lowerDecision := tool.guardCommand(context.Background(), lowerCommand, cwd)
		spellings := []string{word.name, strings.ToUpper(word.name), strings.ToUpper(word.name[:1]) + word.name[1:]}
		for _, spelling := range spellings {
			caseName := "similar word/" + word.name + "/" + spelling
			t.Run(caseName, func(t *testing.T) {
				t11CheckNestedCase(t, tool, cwd, t11NestedCase{
					name: caseName, command: t11NestedInlineCommand(spelling, script, word.evalSyntax),
					lowerCommand: lowerCommand, lowerDecision: lowerDecision, path: path, allow: true,
				})
			})
		}
	}
}
