// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-092 D3: looking through a wrapper must retain its own restriction as
// well as restrictions on the inner program. All commands are text only.
func TestEvaluateCommand_WrapperOwnDenyPreserved(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"sudo", "ſudo", "env", "printf"} {
		writeFakeExecutable(t, bin, name)
		if _, err := ResolveBinary(name, bin); err != nil {
			t.Fatalf("fixture: %q must resolve before policy evaluation: %v", name, err)
		}
	}
	inner, err := filepath.EvalSymlinks(filepath.Join(bin, "printf"))
	if err != nil {
		t.Fatalf("fixture: resolve the inner metadata path: %v", err)
	}
	for _, c := range t11WrapperPolicyCases(t) {
		t.Run(c.name, func(t *testing.T) {
			got := EvaluateCommand(c.command, c.rules, baseOptions(bin))
			record, err := json.Marshal(map[string]any{
				"name": c.name, "command": c.command, "rules": c.rules, "want_action": c.want, "verdict": got,
			})
			if err != nil {
				t.Fatalf("fixture: record the complete policy verdict: %v", err)
			}
			t.Logf("WRAPPER_POLICY_CASE %s", record)
			if len(got.Segments) != 1 {
				t.Fatalf("wrapper policy: got %d segments, want one", len(got.Segments))
			}
			if got.Action != c.want {
				t.Fatalf("wrapper policy: Action = %q, want %q from the configured rule", got.Action, c.want)
			}
			seg := got.Segments[0]
			if seg.Action != c.want || seg.Blind || seg.BlindReason != "" {
				t.Errorf("wrapper policy: Action=%q Blind=%v Reason=%q, want Action=%q without a blind result", seg.Action, seg.Blind, seg.BlindReason, c.want)
			}
			if c.want == ActionDeny {
				if seg.MatchedRule == nil || *seg.MatchedRule != c.rules[0] {
					t.Errorf("wrapper policy: matched rule = %+v, want exact configured rule %+v", seg.MatchedRule, c.rules[0])
				}
			} else if seg.MatchedRule != nil || seg.ResolvedPath != inner {
				t.Errorf("no-rule control: matched rule=%+v path=%q, want no rule and inner path %q", seg.MatchedRule, seg.ResolvedPath, inner)
			}
			if got.FullyAllowed() {
				t.Error("wrapper policy: no allow rule is configured, so FullyAllowed must be false")
			}
		})
	}
}

type t11WrapperPolicyCase struct {
	name, command string
	rules         []Rule
	want          Action
}

func t11WrapperPolicyCases(t *testing.T) []t11WrapperPolicyCase {
	t.Helper()
	// Two spellings, two wrapper depths and three independent rule classes.
	cases := make([]t11WrapperPolicyCase, 0, 2*2*3)
	for _, spelling := range []string{"sudo", "ſudo"} {
		for _, outer := range []string{"", "env "} {
			depth := "direct"
			if outer != "" {
				depth = "nested"
			}
			command := outer + spelling + " printf q43-control"
			cases = append(cases,
				t11WrapperPolicyCase{depth + "/wrapper_deny/" + spelling, command, []Rule{{Action: ActionDeny, Binary: spelling}}, ActionDeny},
				t11WrapperPolicyCase{depth + "/inner_deny/" + spelling, command, []Rule{{Action: ActionDeny, Binary: "printf"}}, ActionDeny},
				t11WrapperPolicyCase{depth + "/no_rule/" + spelling, command, nil, ActionNone},
			)
		}
	}
	if len(cases) != 12 {
		t.Fatalf("fixture: got %d policy rows, want 2*2*3", len(cases))
	}
	return cases
}

// ADR-092 D4: recognized shell forms retain their suggested-prefix shape.
// Only the head's spelling may be canonicalized; argument text stays exact.
func TestSuggestedPrefix_ShellSpellingParity(t *testing.T) {
	cases := []struct{ name, command, head, suffix string }{
		{"plain_script", "sh sample.sh", "sh", "sample.sh"},
		{"folded_script", "ſh sample.sh", "sh", "sample.sh"},
		{"plain_inline", `sh -c "printf q43-control"`, "sh", "-c"},
		{"folded_inline", `ſh -c "printf q43-control"`, "sh", "-c"},
		{"plain_near_name", "shish sample.sh", "shish", "sample.sh"},
		{"folded_near_name", "ſhish sample.sh", "shish", "sample.sh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefix, ok := SuggestedPrefix(c.command, POSIX, fakeHeadResolver)
			record, err := json.Marshal(map[string]any{
				"name": c.name, "command": c.command, "want_head_class": c.head, "want_suffix": c.suffix, "prefix": prefix, "ok": ok,
			})
			if err != nil {
				t.Fatalf("fixture: record the suggestion: %v", err)
			}
			t.Logf("SHELL_PREFIX_CASE %s", record)
			if !ok {
				t.Fatal("shell prefix: expected a readable suggestion")
			}
			head, suffix, _ := strings.Cut(prefix, " ")
			if !strings.EqualFold(head, c.head) || suffix != c.suffix {
				t.Errorf("shell prefix: got (%q, %q), want head in %q fold class and exact suffix %q", head, suffix, c.head, c.suffix)
			}
		})
	}
}
