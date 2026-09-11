// Omnipus — verifier capability gate tests (ADR-084 D10, wave E1)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
)

// TestVerifierGodModeRefusalReason_RefusesUnderGodMode pins the
// JUDGE-FR-057 predicate this wave can build and test in isolation: with
// god mode enabled, the gate refuses and returns the FR-057/FR-057a
// machine-readable reason, prefixed exactly as the spec requires so a
// downstream consumer (goal-status frame, task run record) can match on it.
//
// This test proves the pure predicate works. It does NOT prove
// runVerifierAdjudication actually calls it before creating a verifier
// session (pkg/agent/judge.go, wave E3/E9's region — out of this wave's
// write-set) — that wiring, and the full
// TestRunVerifierAdjudication_GodModeRefusal_SurfacesDistinctReason
// integration oracle the spec names for FR-057/FR-057a, is E3/E9's to add
// when judge.go's runVerifierAdjudication exists to wire it into.
func TestVerifierGodModeRefusalReason_RefusesUnderGodMode(t *testing.T) {
	reason, refuse := VerifierGodModeRefusalReason(true)
	if !refuse {
		t.Fatal("VerifierGodModeRefusalReason(true) must refuse")
	}
	const wantPrefix = "god_mode: "
	if len(reason) < len(wantPrefix) || reason[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("reason must start with %q, got %q", wantPrefix, reason)
	}
	if reason != VerifierGodModeRefusalReasonPrefix+"adjudication refused because god mode floors every tool at allow" {
		t.Fatalf("unexpected reason text: %q", reason)
	}
}

// TestVerifierGodModeRefusalReason_NoRefusalWithoutGodMode is the negative
// control: with god mode disabled, the gate must not refuse and must
// return an empty reason — a gate that always refused would trivially
// "pass" the positive test above for the wrong reason.
func TestVerifierGodModeRefusalReason_NoRefusalWithoutGodMode(t *testing.T) {
	reason, refuse := VerifierGodModeRefusalReason(false)
	if refuse {
		t.Fatal("VerifierGodModeRefusalReason(false) must not refuse")
	}
	if reason != "" {
		t.Fatalf("reason must be empty when not refusing, got %q", reason)
	}
}

// TestContextBuilder_WithNoProjectShelf_ClosesBothInstallPaths pins
// JUDGE-FR-059a's actual, testable behaviour: a ContextBuilder that has had
// a project shelf and a per-workspace resolver installed, then has
// WithNoProjectShelf() called, resolves a project-shelf-only skill to
// DENIED for every workspace id — matching "the verifier turn's
// ContextBuilder MUST have no project shelf: WithProjectShelf(nil) and
// WithProjectShelfResolver(nil)" by actually exercising ResolveSkillName,
// not by inspecting the two fields' values directly (a field-value
// assertion would pass against a builder nobody ever routes a skill lookup
// through).
//
// This is the closure this wave can build and test end-to-end within
// pkg/agent/context.go alone. It does NOT prove the real Judge instance's
// verifier-turn ContextBuilder actually calls WithNoProjectShelf() — that
// wiring lives in runVerifierAdjudication (pkg/agent/judge.go, wave E3/E9,
// out of this wave's write-set) — so it is not the same proof as the spec's
// named integration oracle TestJudgeInstance_WorkspaceProjectShelfSkillIsDenied,
// which requires a real Judge AgentInstance and re-rooted workspace to
// construct.
func TestContextBuilder_WithNoProjectShelf_ClosesBothInstallPaths(t *testing.T) {
	workspace := t.TempDir()

	mountRoot := t.TempDir()
	skillDir := filepath.Join(mountRoot, ".claude", "skills", "onboarding")
	writeProjectSkillFile(t, skillDir, "onboarding", "Use when onboarding a new teammate")

	projectShelf, collisions := skills.MergeProjectSkills([]skills.ProjectMount{{Name: "acme", Root: mountRoot}})
	if len(collisions) != 0 {
		t.Fatalf("expected no collisions, got %v", collisions)
	}

	// Sanity: with the shelf and resolver installed, the project skill
	// resolves — confirms the test's setup actually exercises the shelf
	// before proving the closure removes it.
	resolver := func(workspaceID string) skills.ProjectShelf {
		return projectShelf
	}
	cb := NewContextBuilder(workspace).
		WithSkillAllowlist([]string{}).
		WithProjectShelf(projectShelf).
		WithProjectShelfResolver(resolver)

	if _, ok := cb.ResolveSkillName("onboarding"); !ok {
		t.Fatal("setup check failed: the project skill must resolve BEFORE WithNoProjectShelf is called")
	}

	cb.WithNoProjectShelf()

	if _, ok := cb.ResolveSkillName("onboarding"); ok {
		t.Fatal("JUDGE-FR-059a: a project-shelf skill must NOT resolve after WithNoProjectShelf()")
	}
	if got := cb.ProjectShelfForWorkspace("any-workspace-id"); got != nil {
		t.Fatalf("JUDGE-FR-059a: ProjectShelfForWorkspace must return nil for every workspace id after "+
			"WithNoProjectShelf(), got %#v", got)
	}
	if got := cb.ProjectShelfForWorkspace(""); got != nil {
		t.Fatalf("JUDGE-FR-059a: the workspace-blind (\"\") shelf must also be nil after "+
			"WithNoProjectShelf(), got %#v", got)
	}
}
