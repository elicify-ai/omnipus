// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "testing"

func TestHasUnbalancedQuote(t *testing.T) {
	cases := []struct {
		name string
		seg  string
		want bool
	}{
		{"balanced, no quotes", "echo hi", false},
		{"balanced double quotes", `echo "a b"`, false},
		{"balanced single quotes", `echo 'a b'`, false},
		{"R12 left half of a split quoted separator", `echo "a`, true},
		{"R12 right half of a split quoted separator", `b"`, true},
		{"escaped quote does not count", `echo \"hi`, false},
		{"unbalanced single quote", `echo 'a`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasUnbalancedQuote(c.seg); got != c.want {
				t.Errorf("hasUnbalancedQuote(%q) = %v, want %v", c.seg, got, c.want)
			}
		})
	}
}

func TestLooksLikeBraceExpansion(t *testing.T) {
	cases := []struct {
		name string
		seg  string
		want bool
	}{
		{"R14 brace expansion head", "{cat,/etc/passwd}", true},
		{"leading whitespace tolerated", "  {cat,/etc/passwd}", true},
		{"single value brace is not expansion syntax", "{onlyone}", false},
		{"ordinary command", "cat /etc/passwd", false},
		{"unterminated brace", "{cat,/etc/passwd", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := looksLikeBraceExpansion(c.seg); got != c.want {
				t.Errorf("looksLikeBraceExpansion(%q) = %v, want %v", c.seg, got, c.want)
			}
		})
	}
}

func TestClassifySegment(t *testing.T) {
	cases := []struct {
		name             string
		seg              string
		allowRuleContext bool
		wantHead         string
		wantReason       blindSpotReason
	}{
		{"ordinary command resolves cleanly", "git status", false, "git", blindNone},
		{"R12 quote-blind over-split routes to ask", `echo "a`, false, "", blindUnbalancedQuote},
		{"R13 redirection-only routes to ask", "> out", false, "", blindNoResolvableHead},
		{"R14 brace expansion routes to ask", "{cat,/etc/passwd}", false, "", blindBraceExpansion},
		{"R9-shaped expansion head routes to ask", "$X -rf /", false, "", blindFromExpansion},
		{"dir-stripped head OK outside allow context", "/usr/bin/git status", false, "git", blindNone},
		{"dir-stripped head refused for an allow match", "/usr/bin/git status", true, "", blindNormalisedHead},
		{"case-folded head OK outside allow context", "GIT status", false, "git", blindNone},
		{"case-folded head refused for an allow match", "GIT status", true, "", blindNormalisedHead},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			head, reason := classifySegment(c.seg, fakeHeadResolver, c.allowRuleContext)
			if head != c.wantHead || reason != c.wantReason {
				t.Errorf("classifySegment(%q, allowRuleContext=%v) = (%q, %q), want (%q, %q)",
					c.seg, c.allowRuleContext, head, reason, c.wantHead, c.wantReason)
			}
		})
	}
}
