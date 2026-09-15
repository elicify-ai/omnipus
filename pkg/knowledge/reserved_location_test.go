// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"errors"
	"testing"
)

// TestIsReservedLocation_MatchesTheRefusal pins that the exported predicate
// answers yes for exactly the paths the knowledge layer refuses with
// ErrReservedLocation — so a door that asks it before routing can never
// disagree with the refusal it is avoiding (UAT re-test U-58).
func TestIsReservedLocation_MatchesTheRefusal(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{".omnipus-vault", true},
		{".omnipus-vault/trash/2026-09-01T10-00-00Z/Old.md", true},
		{".obsidian/app.json", true},
		{".git", true},
		{".trash/Discarded.md", true},
		{"Projects/.obsidian/plugins/x.md", true}, // any depth, not just the first segment
		{"Projects/Atlas.md", false},
		{"assets", false},
		{".trashy/note.md", false},      // a name that merely starts like one
		{"notes.git/readme.md", false},  // a name that merely ends like one
		{"omnipus-vault-backup", false}, // the marker renamed away
		{"Projects/.omnipus-vault", true},
	}
	for _, tc := range cases {
		got := IsReservedLocation(tc.rel)
		if got != tc.want {
			t.Errorf("IsReservedLocation(%q) = %v, want %v", tc.rel, got, tc.want)
		}
		refused := errors.Is(authorRefuseReserved(tc.rel), ErrReservedLocation)
		if got != refused {
			t.Errorf("IsReservedLocation(%q) = %v but the refusal says %v", tc.rel, got, refused)
		}
	}
}
