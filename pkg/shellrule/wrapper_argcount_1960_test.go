// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "testing"

// TestWrapperArgCount_1960 pins the exported view of the ONE wrapper list
// that #1960 S0's path guard uses to find the real program word: how many of
// a wrapper's argument words belong to the wrapper itself.
func TestWrapperArgCount_1960(t *testing.T) {
	cases := []struct {
		name   string
		head   string
		args   []string
		want   int
		wantOK bool
	}{
		{"env, program at once", "env", []string{"/opt/gh", "auth"}, 0, true},
		{"env with flag and assignment", "env", []string{"-i", "A=1", "/opt/gh"}, 2, true},
		{"env -C takes a value", "env", []string{"-C", "/tmp/x", "/opt/gh"}, 2, true},
		{"timeout duration is positional", "timeout", []string{"5", "/opt/gh"}, 1, true},
		{"timeout signal flag then duration", "timeout", []string{"-s", "KILL", "5", "/opt/gh"}, 3, true},
		{"nohup", "nohup", []string{"/opt/gh"}, 0, true},
		{"sudo -u value", "sudo", []string{"-u", "root", "/opt/gh"}, 2, true},
		{"wrapper alone has no program", "env", []string{"-i"}, 0, false},
		{"not a wrapper", "gh", []string{"auth"}, 0, false},
		{"wrapper in another letter case", "ENV", []string{"/opt/gh"}, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := WrapperArgCount(c.head, c.args)
			if ok != c.wantOK || got != c.want {
				t.Fatalf("WrapperArgCount(%q, %q) = (%d, %v), want (%d, %v)", c.head, c.args, got, ok, c.want, c.wantOK)
			}
		})
	}
}
