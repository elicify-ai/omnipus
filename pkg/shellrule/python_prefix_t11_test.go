// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"encoding/json"
	"testing"
)

// The interpreter command-rule contract covers Python and its versioned names,
// not a shorter part of the name. These commands are evaluated as text only;
// none of the filesystem fixtures or inline scripts is executed.
func TestEvaluateCommand_PythonInterpreterPrefixDeny(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "printf")
	opts := baseOptions(dir)
	rule := Rule{Action: ActionDeny, Binary: "printf"}
	cases := []struct {
		program string
		want    Action
	}{
		{"python3", ActionDeny},
		{"python", ActionDeny},
		{"pyth", ActionNone},
	}
	for _, c := range cases {
		t.Run(c.program, func(t *testing.T) {
			fixturePath := writeFakeExecutable(t, dir, c.program)
			resolved, err := ResolveBinary(c.program, dir)
			if err != nil || resolved != fixturePath {
				t.Fatalf("fixture: %q must resolve to %q before evaluating rules; got %q, error %v",
					c.program, fixturePath, resolved, err)
			}
			command := c.program + ` -c "printf q27-control"`
			got := EvaluateCommand(command, []Rule{rule}, opts)
			record, err := json.Marshal(map[string]any{
				"command": command, "rule": rule, "want_action": c.want,
				"fixture_path": fixturePath, "verdict": got,
			})
			if err != nil {
				t.Fatalf("fixture: record the complete command-rule verdict: %v", err)
			}
			t.Logf("PYTHON_PREFIX_CASE %s", record)
			if len(got.Segments) != 1 {
				t.Fatalf("EvaluateCommand(%q): got %d segments, want exactly one", command, len(got.Segments))
			}
			if got.Action != c.want {
				t.Fatalf("EvaluateCommand(%q).Action = %q, want %q from the configured interpreter deny rule",
					command, got.Action, c.want)
			}
			segment := got.Segments[0]
			if segment.Action != c.want {
				t.Errorf("segment Action = %q, want %q for %q", segment.Action, c.want, command)
			}
			if segment.Blind || segment.BlindReason != "" {
				t.Errorf("segment Blind=%v, BlindReason=%q; want a nonblind policy decision", segment.Blind, segment.BlindReason)
			}
			if c.want == ActionDeny {
				if segment.MatchedRule == nil || *segment.MatchedRule != rule {
					t.Errorf("MatchedRule = %+v, want the exact configured rule %+v", segment.MatchedRule, rule)
				}
			} else {
				if segment.MatchedRule != nil {
					t.Errorf("short-name control MatchedRule = %+v, want nil", segment.MatchedRule)
				}
				if segment.ResolvedPath != fixturePath {
					t.Errorf("short-name control ResolvedPath = %q, want ordinary resolved command %q", segment.ResolvedPath, fixturePath)
				}
			}
			if got.FullyAllowed() {
				t.Error("FullyAllowed() = true, want false: no allow rule is configured")
			}
		})
	}
}
