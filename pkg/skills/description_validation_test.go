// Omnipus — Skill Description Authoring Validation Tests (ADR-072 D2)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSkillDescription_RejectsEmptyAndNameEcho covers spec FR-010/FR-011
// (Dataset F rows 1-3, spec #23): an empty description, a whitespace-only
// description, and a description that is EXACTLY the skill's own name are
// all rejected.
func TestSkillDescription_RejectsEmptyAndNameEcho(t *testing.T) {
	if err := ValidateSkillDescription("release-notes", ""); err == nil {
		t.Fatal("expected an error for an empty description")
	}
	if err := ValidateSkillDescription("release-notes", "   "); err == nil {
		t.Fatal("expected an error for a whitespace-only description")
	}
	if err := ValidateSkillDescription("release-notes", "release-notes"); err == nil {
		t.Fatal("expected an error for a description that is exactly the skill's own name")
	}
}

// TestSkillDescription_LengthBoundary covers spec FR-012 (Dataset F rows
// 4-5, spec #24): exactly MaxDescriptionLength characters is accepted;
// MaxDescriptionLength+1 is rejected and the error names the limit.
func TestSkillDescription_LengthBoundary(t *testing.T) {
	atLimit := strings.Repeat("a", MaxDescriptionLength)
	if err := ValidateSkillDescription("some-skill", atLimit); err != nil {
		t.Fatalf("expected exactly %d characters to be accepted, got: %v", MaxDescriptionLength, err)
	}

	overLimit := strings.Repeat("a", MaxDescriptionLength+1)
	err := ValidateSkillDescription("some-skill", overLimit)
	if err == nil {
		t.Fatalf("expected %d characters to be rejected", MaxDescriptionLength+1)
	}
	assert.Contains(t, err.Error(), "1024", "the rejection must state the limit")
}

// TestSkillDescription_LengthBoundary_TriggerConditionAccepted is a happy-path
// sanity check (Dataset F row 6): a real trigger-condition description is
// accepted outright.
func TestSkillDescription_LengthBoundary_TriggerConditionAccepted(t *testing.T) {
	err := ValidateSkillDescription(
		"release-notes",
		"Use when the user asks to cut a release, tag a version, or publish release notes",
	)
	assert.NoError(t, err)
}

// TestDescription_NearMissNameRestatement covers FR-011's EXACT comparison
// (MAJ-002 — Dataset F rows 8-10, spec #30f): case and separator differences
// from the slug are still a restatement and are rejected; trailing
// punctuation is still a restatement and is rejected; but a description that
// adds real words is NOT a restatement — no fuzzy matching, no edit
// distance — and must be accepted.
func TestDescription_NearMissNameRestatement(t *testing.T) {
	cases := []struct {
		name    string
		desc    string
		wantErr bool
	}{
		{"case and separator differ from the slug", "Release Notes", true},
		{"trailing punctuation added", "release notes.", true},
		{"extra words — not a restatement, must be accepted", "Handles release notes", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSkillDescription("release-notes", tc.desc)
			if tc.wantErr {
				assert.Error(t, err, "expected %q to be rejected as a near-miss restatement of the slug", tc.desc)
			} else {
				assert.NoError(t, err, "expected %q to be accepted — it adds real words, not just case/punctuation", tc.desc)
			}
		})
	}
}
