// Omnipus - Plan authorship kind (CreatedByKind) unit tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

import (
	"errors"
	"strings"
	"testing"
)

// The plan authorship kinds are the explicit discriminator for CreatedBy.
// CreatedByKind (whose doc comment on Plan records the human-admin/agent-ID
// collision the old approve-time registry heuristic mis-decided). Values
// mirror task.CriterionAuthor.Kind's convention.
func TestPlanCreatedByKindConstants_MirrorTaskCriterionConvention(t *testing.T) {
	if CreatedByKindUser != "user" {
		t.Errorf("CreatedByKindUser = %q, want %q", CreatedByKindUser, "user")
	}
	if CreatedByKindAgent != "agent" {
		t.Errorf("CreatedByKindAgent = %q, want %q", CreatedByKindAgent, "agent")
	}
}

// TestPlanAuthoredByAgent covers the SD-A7 gate's origin resolution: the
// explicit creation-time stamp wins outright; a kindless (legacy) plan falls
// back to the injected registry probe; a nil probe (unwired checker)
// fail-closes to the strict tier.
func TestPlanAuthoredByAgent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		kind      string
		createdBy string
		isAgent   func(string) bool
		want      bool
	}{
		{"explicit agent kind wins even when probe says human", CreatedByKindAgent, "someone", func(string) bool { return false }, true},
		{"explicit user kind wins even when probe says agent", CreatedByKindUser, "admin", func(string) bool { return true }, false},
		{"legacy kindless + probe agent", "", "admin", func(string) bool { return true }, true},
		{"legacy kindless + probe human", "", "daniel", func(string) bool { return false }, false},
		{"legacy kindless + nil probe fail-closed", "", "admin", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &Plan{Title: "t", WorkspaceID: "ws", OwnerAgentID: "a", CreatedBy: tc.createdBy, CreatedByKind: tc.kind}
			if got := p.AuthoredByAgent(tc.isAgent); got != tc.want {
				t.Errorf("AuthoredByAgent() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPlanCreate_RejectsUnknownCreatedByKind proves the store rejects an
// unknown non-empty authorship kind at Create (normalize validation), while
// the empty (legacy kindless) state keeps passing for pre-existing data.
func TestPlanCreate_RejectsUnknownCreatedByKind(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := New(root + "/plans")

	bad := &Plan{Title: "bad kind", WorkspaceID: "ws", OwnerAgentID: "a", CreatedByKind: "robot"}
	err := s.Create(bad)
	if err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("Create with unknown kind: err=%v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "created_by_kind") {
		t.Errorf("error %q should name the offending field", err.Error())
	}

	// The legacy kindless state still creates fine (no migration needed).
	ok := &Plan{Title: "legacy-shaped", WorkspaceID: "ws", OwnerAgentID: "a"}
	if err := s.Create(ok); err != nil {
		t.Fatalf("kindless Create rejected: %v", err)
	}
}
