// Omnipus — Skill tool search-path audit tests (ADR-072 D3.1, S67 fix)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// newSkillAuditTestLogger spins up an audit.Logger writing to a temp
// directory and returns it alongside the path its JSONL is written to.
// Mirrors pkg/audit/events_test.go's own newTestLogger fixture, duplicated
// here because that helper is unexported and package-local to pkg/audit.
func newSkillAuditTestLogger(t *testing.T) (*audit.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  1024 * 1024,
		RetentionDays: 1,
		RedactEnabled: false,
	})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := lg.Close(); closeErr != nil {
			_ = closeErr // cleanup only; t.TempDir() reclaims the directory regardless
		}
	})
	return lg, filepath.Join(dir, "audit.jsonl")
}

// readSkillCallEntries drains every "skill.call" JSONL line from path into
// typed audit.Entry values, in file order.
func readSkillCallEntries(t *testing.T, path string) []audit.Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("ReadFile: %v", err)
	}
	var out []audit.Entry
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var e audit.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		if e.Event != audit.EventSkillCall {
			continue
		}
		out = append(out, e)
	}
	return out
}

// TestSkillSearch_ProducesExactlyOneAuditRecord is the regression test for
// UAT S67: pkg/tools/skill.go's execSearch never called audit.EmitSkillCall
// at all, so ADR-072 D3.1's "every Skill call is audited" guarantee silently
// did not hold for the search mode — only load calls were ever recorded. A
// successful search (a query that ranks and returns a usable match) must
// produce EXACTLY one "skill.call" audit entry, with mode "search".
func TestSkillSearch_ProducesExactlyOneAuditRecord(t *testing.T) {
	lg, path := newSkillAuditTestLogger(t)

	tool := NewSkillTool(5)
	tool.SetAuditLogger(lg)
	corpus := []SkillSearchDoc{
		{Slug: "release-notes", Description: "Use when the user asks to cut a release or publish notes"},
	}
	tool.SetResolver(
		func(ctx context.Context, slug string) SkillLoadOutcome { return SkillLoadOutcome{} },
		func(ctx context.Context, slug string) bool { return true },
		func(ctx context.Context) []SkillSearchDoc { return corpus },
	)

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	result := tool.Execute(ctx, map[string]any{"query": "publish release notes"})
	if result.IsError || !strings.Contains(result.ForLLM, "release-notes") {
		t.Fatalf("expected the search to find the matching skill, got: %+v", result)
	}

	if err := lg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries := readSkillCallEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 skill.call audit entry, got %d: %#v", len(entries), entries)
	}

	e := entries[0]
	if got := e.Details["mode"]; got != "search" {
		t.Errorf("mode = %v, want %q", got, "search")
	}
	if got := e.Details["outcome"]; got != "loaded" {
		t.Errorf("outcome = %v, want %q (a usable match was found)", got, "loaded")
	}
	if e.AgentID != "jim" {
		t.Errorf("agent_id = %q, want %q", e.AgentID, "jim")
	}
	if got := e.Details["workspace_id"]; got != "ws-1" {
		t.Errorf("workspace_id = %v, want %q", got, "ws-1")
	}
}

// TestSkillSearch_NoMatchesStillAuditsOnce covers the companion outcome: a
// search that finds nothing usable (empty corpus, no ranked hits, or every
// candidate filtered out by canUse) must still produce exactly one audit
// record, with outcome "not_found" rather than silently skipping the audit.
func TestSkillSearch_NoMatchesStillAuditsOnce(t *testing.T) {
	lg, path := newSkillAuditTestLogger(t)

	tool := NewSkillTool(5)
	tool.SetAuditLogger(lg)
	tool.SetResolver(
		func(ctx context.Context, slug string) SkillLoadOutcome { return SkillLoadOutcome{} },
		func(ctx context.Context, slug string) bool { return true },
		func(ctx context.Context) []SkillSearchDoc { return nil }, // empty corpus
	)

	ctx := WithAgentID(context.Background(), "ava")
	ctx = WithWorkspaceID(ctx, "ws-2")
	result := tool.Execute(ctx, map[string]any{"query": "nothing installed matches this"})
	if result.IsError {
		t.Fatalf("expected a silent no-matches result, not an error: %+v", result)
	}

	if err := lg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries := readSkillCallEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 skill.call audit entry, got %d: %#v", len(entries), entries)
	}

	e := entries[0]
	if got := e.Details["mode"]; got != "search" {
		t.Errorf("mode = %v, want %q", got, "search")
	}
	if got := e.Details["outcome"]; got != "not_found" {
		t.Errorf("outcome = %v, want %q", got, "not_found")
	}
	if got := e.Details["slug"]; got != "" {
		t.Errorf("slug = %v, want empty (a search names no single slug)", got)
	}
	if got := e.Details["shelf"]; got != "" {
		t.Errorf("shelf = %v, want empty (a search is not attributable to one shelf)", got)
	}
}

// TestSkillSearch_NilAuditLoggerIsNoOp confirms a nil auditLogger (audit
// logging disabled, or SetAuditLogger never called) does not panic and does
// not block the search from completing normally — mirroring
// audit.EmitSkillCall's own documented nil-logger no-op contract.
func TestSkillSearch_NilAuditLoggerIsNoOp(t *testing.T) {
	tool := NewSkillTool(5) // SetAuditLogger deliberately never called
	corpus := []SkillSearchDoc{
		{Slug: "release-notes", Description: "Use when the user asks to cut a release"},
	}
	tool.SetResolver(
		func(ctx context.Context, slug string) SkillLoadOutcome { return SkillLoadOutcome{} },
		func(ctx context.Context, slug string) bool { return true },
		func(ctx context.Context) []SkillSearchDoc { return corpus },
	)

	result := tool.Execute(context.Background(), map[string]any{"query": "cut a release"})
	if result.IsError || !strings.Contains(result.ForLLM, "release-notes") {
		t.Fatalf("expected the search to still succeed with no audit logger wired, got: %+v", result)
	}
}
