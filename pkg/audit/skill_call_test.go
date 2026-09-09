// Omnipus — Skill-call audit tests (ADR-072 D3.1, MAJ-006)
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Test names below are pinned by the workflow's traceability step:
// TestAudit_EverySkillCallRecorded (spec test 50), TestAudit_HiddenDenialStillRecorded
// (test 51), TestAudit_ModeAndOutcomeClosedSet (test 51j, MAJ-002 — the
// closed set itself, not "N distinct values").

package audit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// readSkillCallEntries drains every EventSkillCall JSONL line from path into
// typed Entry values, in file order.
func readSkillCallEntries(t *testing.T, path string) []Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var out []Entry
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		if e.Event != EventSkillCall {
			continue
		}
		out = append(out, e)
	}
	return out
}

// TestAudit_EverySkillCallRecorded (test 50, D3.1) — every Skill-tool call
// outcome (both modes, all three outcomes) produces exactly one audit entry
// carrying slug, mode, outcome, shelf, agent id and workspace id.
func TestAudit_EverySkillCallRecorded(t *testing.T) {
	lg, path := newTestLogger(t)

	cases := []struct {
		agentID, workspaceID, slug string
		mode                       SkillCallMode
		outcome                    SkillCallOutcome
		shelf                      string
	}{
		{"jim", "ws-1", "release-notes", SkillCallModeLoad, SkillCallOutcomeLoaded, "registry"},
		{"jim", "ws-1", "no-such-skill", SkillCallModeLoad, SkillCallOutcomeNotFound, ""},
		{"ava", "ws-2", "deploy", SkillCallModeLoad, SkillCallOutcomeDenied, ""},
		{"ava", "ws-2", "", SkillCallModeSearch, SkillCallOutcomeLoaded, ""},
	}
	for _, c := range cases {
		EmitSkillCall(lg, c.agentID, c.workspaceID, c.slug, c.mode, c.outcome, c.shelf)
	}
	if err := lg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries := readSkillCallEntries(t, path)
	if len(entries) != len(cases) {
		t.Fatalf("expected %d skill.call entries, got %d: %#v", len(cases), len(entries), entries)
	}

	for i, c := range cases {
		e := entries[i]
		if e.AgentID != c.agentID {
			t.Errorf("entry %d: agent_id = %q, want %q", i, e.AgentID, c.agentID)
		}
		if got := e.Details["slug"]; got != c.slug {
			t.Errorf("entry %d: slug = %v, want %q", i, got, c.slug)
		}
		if got := e.Details["mode"]; got != string(c.mode) {
			t.Errorf("entry %d: mode = %v, want %q", i, got, c.mode)
		}
		if got := e.Details["outcome"]; got != string(c.outcome) {
			t.Errorf("entry %d: outcome = %v, want %q", i, got, c.outcome)
		}
		if got := e.Details["shelf"]; got != c.shelf {
			t.Errorf("entry %d: shelf = %v, want %q", i, got, c.shelf)
		}
		if got := e.Details["workspace_id"]; got != c.workspaceID {
			t.Errorf("entry %d: workspace_id = %v, want %q", i, got, c.workspaceID)
		}
	}
}

// TestAudit_HiddenDenialStillRecorded (test 51, N6 "render != audit") —
// ADR-072 D3 hides a SUCCESSFUL Skill call from the chat thread by default
// (src/lib/toolVisibility.ts, a frontend-owned, out-of-package concern this
// test does not exercise) but a DENIED call is a security-relevant event
// (FR-019) and is audited unconditionally, exactly like every other outcome.
// This test proves the audit primitive itself has no concept of render
// visibility at all — it fires on every call regardless of how the SPA
// would render it, which is what makes the hide a render-only decision.
func TestAudit_HiddenDenialStillRecorded(t *testing.T) {
	lg, path := newTestLogger(t)

	EmitSkillCall(lg, "ray", "ws-3", "internal-only-skill", SkillCallModeLoad, SkillCallOutcomeDenied, "")

	if err := lg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries := readSkillCallEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 skill.call entry for the denied load, got %d: %#v", len(entries), entries)
	}
	e := entries[0]
	if e.Decision != DecisionDeny {
		t.Errorf("decision = %q, want %q", e.Decision, DecisionDeny)
	}
	if got := e.Details["outcome"]; got != string(SkillCallOutcomeDenied) {
		t.Errorf("outcome = %v, want %q", got, SkillCallOutcomeDenied)
	}
	if got := e.Details["slug"]; got != "internal-only-skill" {
		t.Errorf("slug = %v, want %q", got, "internal-only-skill")
	}
	if e.AgentID != "ray" {
		t.Errorf("agent_id = %q, want %q", e.AgentID, "ray")
	}
}

// TestAudit_ModeAndOutcomeClosedSet (test 51j, MAJ-002) — asserts the CLOSED
// SET itself (every valid member accepted, every near-miss rejected), not
// merely "N distinct values accepted" — FR-018a's exact requirement.
func TestAudit_ModeAndOutcomeClosedSet(t *testing.T) {
	validModes := []SkillCallMode{SkillCallModeLoad, SkillCallModeSearch}
	for _, m := range validModes {
		if !IsValidSkillCallMode(m) {
			t.Errorf("IsValidSkillCallMode(%q) = false, want true", m)
		}
	}
	invalidModes := []SkillCallMode{"", "LOAD", "Load", "write", "delete", "loaded", "search "}
	for _, m := range invalidModes {
		if IsValidSkillCallMode(m) {
			t.Errorf("IsValidSkillCallMode(%q) = true, want false", m)
		}
	}

	validOutcomes := []SkillCallOutcome{
		SkillCallOutcomeLoaded, SkillCallOutcomeDenied, SkillCallOutcomeNotFound,
	}
	for _, o := range validOutcomes {
		if !IsValidSkillCallOutcome(o) {
			t.Errorf("IsValidSkillCallOutcome(%q) = false, want true", o)
		}
	}
	invalidOutcomes := []SkillCallOutcome{
		"", "LOADED", "Denied", "not-found", "notfound", "allow", "deny", "error",
	}
	for _, o := range invalidOutcomes {
		if IsValidSkillCallOutcome(o) {
			t.Errorf("IsValidSkillCallOutcome(%q) = true, want false", o)
		}
	}
}

// TestLastInvokedForSkill_TracksMostRecentLoad is bonus coverage (not in the
// pinned name list) for the FR-020 query primitive: LastInvokedForSkill
// answers "when did the model last actually request this skill by name",
// counting both loaded and denied load outcomes (see the function's own doc
// comment for why) and ignoring search hits and other slugs entirely.
func TestLastInvokedForSkill_TracksMostRecentLoad(t *testing.T) {
	lg, _ := newTestLogger(t)

	if _, found := lg.LastInvokedForSkill("release-notes"); found {
		t.Fatalf("expected no last-invoked record before any call")
	}

	// Every write flushes synchronously (audit.go's writeLine), so
	// LastInvokedForSkill can read back the still-open logger's file
	// directly — no Close() needed (Close() would latch the logger
	// degraded, which LastInvokedForSkill deliberately treats as
	// "answer unavailable", so it must run BEFORE any Close()).
	EmitSkillCall(lg, "jim", "ws-1", "release-notes", SkillCallModeLoad, SkillCallOutcomeLoaded, "registry")
	time.Sleep(2 * time.Millisecond)
	EmitSkillCall(lg, "jim", "ws-1", "other-skill", SkillCallModeLoad, SkillCallOutcomeLoaded, "registry")
	time.Sleep(2 * time.Millisecond)
	// A search hit that merely ranks release-notes into a match list must
	// not move its last-invoked time.
	EmitSkillCall(lg, "jim", "ws-1", "", SkillCallModeSearch, SkillCallOutcomeLoaded, "")
	time.Sleep(2 * time.Millisecond)
	EmitSkillCall(lg, "jim", "ws-1", "release-notes", SkillCallModeLoad, SkillCallOutcomeDenied, "")

	got, found := lg.LastInvokedForSkill("release-notes")
	if !found {
		t.Fatalf("expected a last-invoked record for release-notes")
	}
	if got.IsZero() {
		t.Fatalf("expected a non-zero last-invoked timestamp")
	}
	firstLoad, _ := lg.LastInvokedForSkill("other-skill")
	if !got.After(firstLoad) {
		t.Errorf("expected release-notes' last-invoked (%v, the later denied load) to be after other-skill's (%v)", got, firstLoad)
	}
}
