// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"encoding/json"
	"testing"
)

// Q31 A: known wrapper spellings retain the inner program's configured denial.
// The lookalike controls remain ordinary non-wrappers. No command is executed.
func TestEvaluateCommand_FoldedWrapperDeny(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sudo", "ſudo", "SUDO", "setsid", "ſetsid", "setſid", "SETSID", "env", "bash", "printf", "sudoish", "ſudoish"} {
		writeFakeExecutable(t, dir, name)
	}
	opts := baseOptions(dir)
	rule := Rule{Action: ActionDeny, Binary: "printf"}
	for _, c := range t11FoldedWrapperCases(t) {
		t.Run(c.name, func(t *testing.T) {
			resolved, err := ResolveBinary(c.spelling, dir)
			if err != nil {
				t.Fatalf("fixture: wrapper spelling %q must resolve: %v", c.spelling, err)
			}
			head, _, _ := opts.HeadResolver(c.spelling + " q31-control")
			known := IsWrapperCommand(head)
			got := EvaluateCommand(c.command, []Rule{rule}, opts)
			record, err := json.Marshal(map[string]any{
				"name": c.name, "command": c.command, "rule": rule, "want_action": c.want,
				"wrapper_head": head, "want_wrapper": c.known, "actual_wrapper": known,
				"resolved_fixture": resolved, "verdict": got,
			})
			if err != nil {
				t.Fatalf("fixture: record complete wrapper verdict: %v", err)
			}
			t.Logf("WRAPPER_RULE_CASE %s", record)
			if known != c.known {
				t.Errorf("Q31: IsWrapperCommand(%q) = %v, want %v for this name class", head, known, c.known)
			}
			if len(got.Segments) != 1 {
				t.Fatalf("Q31: got %d segments, want one", len(got.Segments))
			}
			if got.Action != c.want {
				t.Fatalf("Q31: EvaluateCommand(%q).Action = %q, want %q", c.command, got.Action, c.want)
			}
			segment := got.Segments[0]
			if segment.Action != c.want || segment.Blind != (c.want == ActionAsk) {
				t.Errorf("Q31: segment Action=%q Blind=%v, want Action=%q Blind=%v", segment.Action, segment.Blind, c.want, c.want == ActionAsk)
			}
			if c.want == ActionNone {
				if segment.MatchedRule != nil || segment.ResolvedPath != resolved {
					t.Errorf("Q31: non-wrapper control must retain its ordinary resolved path %q and no rule; got %+v", resolved, segment)
				}
			} else if segment.MatchedRule == nil || *segment.MatchedRule != rule {
				t.Errorf("Q31: MatchedRule = %+v, want exact configured rule %+v", segment.MatchedRule, rule)
			}
			if got.FullyAllowed() {
				t.Error("Q31: no allow rule is configured; FullyAllowed must be false")
			}
		})
	}
}

type t11FoldedWrapperCase struct {
	name, spelling, command string
	want                    Action
	known                   bool
}

func t11FoldedWrapperCases(t *testing.T) []t11FoldedWrapperCase {
	t.Helper()
	cases := make([]t11FoldedWrapperCase, 0, 7*3+4)
	for _, spelling := range []string{"sudo", "ſudo", "SUDO", "setsid", "ſetsid", "setſid", "SETSID"} {
		cases = append(cases,
			t11FoldedWrapperCase{"direct/" + spelling, spelling, spelling + " printf q31-control", ActionDeny, true},
			t11FoldedWrapperCase{"inline/" + spelling, spelling, spelling + ` bash -c "printf q31-control"`, ActionDeny, true},
			t11FoldedWrapperCase{"recursive/" + spelling, spelling, "env " + spelling + " printf q31-control", ActionDeny, true},
		)
	}
	for _, spelling := range []string{"sudoish", "ſudoish"} {
		cases = append(cases,
			t11FoldedWrapperCase{"direct/" + spelling, spelling, spelling + " printf q31-control", ActionAsk, false},
			t11FoldedWrapperCase{"inline/" + spelling, spelling, spelling + ` bash -c "printf q31-control"`, ActionNone, false},
		)
	}
	// Seven known spellings in three forms, plus four non-wrapper controls.
	if len(cases) != 25 {
		t.Fatalf("fixture: got %d wrapper rows, want 7*3+4", len(cases))
	}
	return cases
}
