// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "testing"

// TestDecide_DenyBeatsAskBeatsAllow proves ADR-091 D3's precedence: "deny
// beats ask beats allow, specificity-blind" — a narrower rule (more of a
// match, e.g. one with an ArgPrefix) is never preferred merely for being
// narrower; only the strictest Action among the matched set wins,
// regardless of input order.
func TestDecide_DenyBeatsAskBeatsAllow(t *testing.T) {
	cases := []struct {
		name    string
		matched []Rule
		want    Action
	}{
		{"empty set has no verdict", nil, ActionNone},
		{"allow only", []Rule{{Action: ActionAllow, Binary: "git"}}, ActionAllow},
		{"ask only", []Rule{{Action: ActionAsk, Binary: "git"}}, ActionAsk},
		{"deny only", []Rule{{Action: ActionDeny, Binary: "git"}}, ActionDeny},
		{
			"deny beats allow regardless of order — deny first",
			[]Rule{{Action: ActionDeny, Binary: "git"}, {Action: ActionAllow, Binary: "git", ArgPrefix: "push"}},
			ActionDeny,
		},
		{
			"deny beats allow regardless of order — allow first",
			[]Rule{{Action: ActionAllow, Binary: "git", ArgPrefix: "push"}, {Action: ActionDeny, Binary: "git"}},
			ActionDeny,
		},
		{
			"deny beats ask",
			[]Rule{{Action: ActionAsk, Binary: "git"}, {Action: ActionDeny, Binary: "git"}},
			ActionDeny,
		},
		{
			"ask beats allow — a narrow allow does not loosen a broad ask",
			[]Rule{{Action: ActionAllow, Binary: "git", ArgPrefix: "status"}, {Action: ActionAsk, Binary: "git"}},
			ActionAsk,
		},
		{
			"all three present — deny still wins",
			[]Rule{{Action: ActionAllow, Binary: "git"}, {Action: ActionAsk, Binary: "git"}, {Action: ActionDeny, Binary: "git"}},
			ActionDeny,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.matched); got != c.want {
				t.Errorf("Decide(%+v) = %q, want %q", c.matched, got, c.want)
			}
		})
	}
}

func TestAction_Valid(t *testing.T) {
	for _, a := range []Action{ActionDeny, ActionAsk, ActionAllow} {
		if !a.Valid() {
			t.Errorf("Action(%q).Valid() = false, want true", a)
		}
	}
	for _, a := range []Action{ActionNone, "REALLY_ALLOW", ""} {
		if a.Valid() {
			t.Errorf("Action(%q).Valid() = true, want false", a)
		}
	}
}

func TestTokenBoundaryHasPrefix(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		prefix []string
		want   bool
	}{
		{"empty prefix matches anything", []string{"anything", "at", "all"}, nil, true},
		{"empty prefix matches empty args", nil, nil, true},
		{"exact word match", []string{"run", "test"}, []string{"run", "test"}, true},
		{"prefix of longer args", []string{"run", "test", "-v"}, []string{"run", "test"}, true},
		{"token-boundary — testfoo does not match test", []string{"run", "testfoo"}, []string{"run", "test"}, false},
		{"args shorter than prefix", []string{"run"}, []string{"run", "test"}, false},
		{"different word entirely", []string{"build"}, []string{"run"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tokenBoundaryHasPrefix(c.args, c.prefix); got != c.want {
				t.Errorf("tokenBoundaryHasPrefix(%v, %v) = %v, want %v", c.args, c.prefix, got, c.want)
			}
		})
	}
}

func TestTokenBoundaryExact(t *testing.T) {
	if !tokenBoundaryExact([]string{"run", "test"}, "run test") {
		t.Error("exact match of full joined args should be true")
	}
	if tokenBoundaryExact([]string{"run", "test", "-v"}, "run test") {
		t.Error("a proper prefix must NOT satisfy an exact match (Windows FR-041: no prefix option)")
	}
	if tokenBoundaryExact([]string{"run", "testfoo"}, "run test") {
		t.Error("a different word must not satisfy an exact match")
	}
}
