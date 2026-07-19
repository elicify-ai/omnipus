// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTask_TagValidation exercises the "Dataset: Tag boundary validation"
// rows from the Planning & Goals spec (Part A §B) via the store's Create path.
func TestTask_TagValidation(t *testing.T) {
	cases := []struct {
		name     string
		tags     []string
		wantErr  bool
		wantTags []string
	}{
		{name: "empty slice — tags optional", tags: []string{}, wantTags: nil},
		{name: "1-char tag ok", tags: []string{"a"}, wantTags: []string{"a"}},
		{name: "64-rune tag accepted (max)", tags: []string{strings.Repeat("x", 64)}, wantTags: []string{strings.Repeat("x", 64)}},
		{name: "65-rune tag rejected (over max)", tags: []string{strings.Repeat("x", 65)}, wantErr: true},
		{name: "17 distinct tags rejected", tags: seventeenDistinctTags(), wantErr: true},
		{name: "16 distinct tags accepted", tags: seventeenDistinctTags()[:16], wantTags: seventeenDistinctTags()[:16]},
		{name: "empty string rejected", tags: []string{""}, wantErr: true},
		{name: "whitespace-only rejected", tags: []string{"   "}, wantErr: true},
		{name: "tab-only rejected", tags: []string{"\t"}, wantErr: true},
		{name: "leading/trailing trimmed", tags: []string{" x "}, wantTags: []string{"x"}},
		{name: "uppercase normalized to lowercase", tags: []string{"RELEASE"}, wantTags: []string{"release"}},
		{name: "case-fold dedup", tags: []string{"Q3", "q3"}, wantTags: []string{"q3"}},
		{name: "unicode combining char counted by rune", tags: []string{"café"}, wantTags: []string{"café"}},
		{name: "prefix convention accepted verbatim", tags: []string{"milestone:q3"}, wantTags: []string{"milestone:q3"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			tk := mkTask("tagged", "ws-1")
			tk.Tags = tc.tags
			err := s.Create(tk)
			if tc.wantErr {
				require.Error(t, err, "case %q must be rejected", tc.name)
				assert.True(t, errors.Is(err, ErrValidation), "case %q must wrap ErrValidation", tc.name)
				return
			}
			require.NoError(t, err, "case %q must be accepted", tc.name)
			assert.Equal(t, tc.wantTags, tk.Tags, "case %q: normalized tag mismatch", tc.name)
		})
	}
}

// seventeenDistinctTags returns 17 distinct normalized tags for the
// max-collection boundary rows.
func seventeenDistinctTags() []string {
	tags := make([]string, 17)
	for i := range tags {
		tags[i] = string(rune('a'+i)) + "-tag"
	}
	return tags
}

// TestTask_TagPatch_ReplacesAtomically verifies Patch.Tags replaces the whole
// set (validated + normalized the same way as Create).
func TestTask_TagPatch_ReplacesAtomically(t *testing.T) {
	s := newStore(t)
	tk := mkTask("patchable", "ws-1")
	tk.Tags = []string{"alpha"}
	require.NoError(t, s.Create(tk))

	newTags := []string{"Beta", "beta", " gamma "}
	updated, err := s.Update(tk.ID, Patch{Tags: &newTags})
	require.NoError(t, err)
	assert.Equal(t, []string{"beta", "gamma"}, updated.Tags,
		"patch must normalize+dedup exactly like create, and fully replace the prior set")

	// Invalid patch is rejected without touching the persisted task.
	badTags := []string{""}
	_, err = s.Update(tk.ID, Patch{Tags: &badTags})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))

	reread, err := s.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"beta", "gamma"}, reread.Tags, "a rejected patch must not have mutated the stored tags")
}

// TestTask_NoMilestoneField verifies Task.MilestoneID no longer exists at
// all (FR-032) — the on-disk JSON of a fully-populated task must never carry
// a "milestone_id" key.
func TestTask_NoMilestoneField(t *testing.T) {
	s := newStore(t)
	tk := mkTask("no-milestone", "ws-1")
	tk.Tags = []string{"release-1"}
	tk.PlanID = "plan-1"
	require.NoError(t, s.Create(tk))

	data, err := json.Marshal(tk)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "milestone_id",
		"the on-disk JSON representation must never carry a milestone_id key")

	// Filter has no MilestoneID field either — PlanID/Tag are its replacements
	// (compile-time enforced; this call would fail to build otherwise).
	_, err = s.List(Filter{PlanID: "plan-1", Tag: "release-1"})
	require.NoError(t, err)
}

// TestTask_TagWorkspaceScoped verifies SD-A8: tags are workspace-scoped in
// INTERPRETATION only, with no global registry — identical tag strings in
// different workspaces are unrelated. Filtering by workspace + tag must not
// leak a same-named tag from a different workspace.
func TestTask_TagWorkspaceScoped(t *testing.T) {
	s := newStore(t)

	inWS1 := mkTask("ws1-task", "ws-1")
	inWS1.Tags = []string{"release"}
	require.NoError(t, s.Create(inWS1))

	inWS2 := mkTask("ws2-task", "ws-2")
	inWS2.Tags = []string{"release"}
	require.NoError(t, s.Create(inWS2))

	got, err := s.List(Filter{WorkspaceID: "ws-1", Tag: "release"})
	require.NoError(t, err)
	require.Len(t, got, 1, "same tag string in a different workspace must not leak in")
	assert.Equal(t, inWS1.ID, got[0].ID)

	got2, err := s.List(Filter{WorkspaceID: "ws-2", Tag: "release"})
	require.NoError(t, err)
	require.Len(t, got2, 1)
	assert.Equal(t, inWS2.ID, got2[0].ID)

	// Differentiation: the two workspace-scoped result sets are different tasks.
	assert.NotEqual(t, got[0].ID, got2[0].ID)
}
