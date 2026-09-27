// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "testing"

// TestSuggestedPrefix_Pinned covers ADR-092 spec FR-026's three PINNED
// rows (P7, P10, P14) verbatim.
func TestSuggestedPrefix_Pinned(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"P7 — env wrapper stops at its first argument", "env X=1 cmd", "env"},
		{"P10 — timeout wrapper stops at its first argument", "timeout 5 npm test", "timeout"},
		{"P14 — leading assignment stripped, no further stop token", "VAR=value echo hi", "echo hi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := SuggestedPrefix(c.command, POSIX, fakeHeadResolver)
			if !ok {
				t.Fatalf("SuggestedPrefix(%q) returned ok=false, want a suggestion", c.command)
			}
			if got != c.want {
				t.Errorf("SuggestedPrefix(%q) = %q, want %q", c.command, got, c.want)
			}
		})
	}
}

// TestSuggestedPrefix_Wrappers exercises every ADR-092 D4-named wrapper,
// including the two-word "sh -c" compound.
func TestSuggestedPrefix_Wrappers(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"sudo apt install curl", "sudo"},
		{"env FOO=bar printenv FOO", "env"},
		{"timeout 30 curl https://x.example", "timeout"},
		{"xargs -I{} rm {}", "xargs"},
		{"sh -c 'rm -rf /'", "sh -c"},
	}
	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			got, ok := SuggestedPrefix(c.command, POSIX, fakeHeadResolver)
			if !ok || got != c.want {
				t.Errorf("SuggestedPrefix(%q) = (%q, %v), want (%q, true)", c.command, got, ok, c.want)
			}
		})
	}
}

// TestSuggestedPrefix_StopTokens exercises the three stop-token classes
// (flag, path-shaped, URL) for an ordinary (non-wrapper) binary.
func TestSuggestedPrefix_StopTokens(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"flag stops the prefix", "git commit -m msg", "git commit"},
		{"leading path-shaped token stops immediately", "cat /etc/passwd", "cat"},
		{"URL token stops the prefix", "curl https://example.com/api", "curl"},
		{"plain sub-command words with no stop token", "npm run test", "npm run test"},
		{"relative path token stops the prefix", "cat ./secret", "cat"},
		{"home-relative token stops the prefix", "cat ~/secrets", "cat"},
		{"bare binary, no arguments", "ls", "ls"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := SuggestedPrefix(c.command, POSIX, fakeHeadResolver)
			if !ok {
				t.Fatalf("SuggestedPrefix(%q) returned ok=false", c.command)
			}
			if got != c.want {
				t.Errorf("SuggestedPrefix(%q) = %q, want %q", c.command, got, c.want)
			}
		})
	}
}

func TestSuggestedPrefix_BlindSpotsRefuseASuggestion(t *testing.T) {
	cases := []string{
		`echo "a;b`,         // unbalanced quote
		"{cat,/etc/passwd}", // brace expansion
		"> out",             // no resolvable head
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			if _, ok := SuggestedPrefix(command, POSIX, fakeHeadResolver); ok {
				t.Errorf("SuggestedPrefix(%q) returned ok=true for a known blind spot, want false", command)
			}
		})
	}
}

// TestSuggestedPrefix_Windows_NeverSuggests proves FR-041: Windows offers
// no prefix option at all, regardless of command shape.
func TestSuggestedPrefix_Windows_NeverSuggests(t *testing.T) {
	if _, ok := SuggestedPrefix("git commit -m msg", WindowsPlatform, fakeHeadResolver); ok {
		t.Error("SuggestedPrefix on WindowsPlatform returned ok=true, want false (no prefix option)")
	}
}
