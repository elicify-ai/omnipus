// Omnipus — Skill tool tests (ADR-072 D1)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestSkillSearch_MatchListPolicyFilteredAfterRanking covers ADR-072 D4/D1
// and ADR-071 §3.2.2 applied to skills (spec #26, FR-022): ranking runs over
// the WHOLE corpus (both candidates strongly match the query), and only the
// policy-filtered MATCH LIST omits the ungranted skill — its name and
// description must not appear anywhere in the result at all.
func TestSkillSearch_MatchListPolicyFilteredAfterRanking(t *testing.T) {
	tool := NewSkillTool(5)
	corpus := []SkillSearchDoc{
		{Slug: "granted-skill", Description: "Use when the user asks to publish release notes"},
		{Slug: "ungranted-skill", Description: "Use when the user asks to publish release notes too"},
	}
	tool.SetResolver(
		func(ctx context.Context, slug string) SkillLoadOutcome { return SkillLoadOutcome{} },
		func(ctx context.Context, slug string) bool { return slug == "granted-skill" },
		func(ctx context.Context) []SkillSearchDoc { return corpus },
	)

	result := tool.Execute(context.Background(), map[string]any{"query": "publish release notes"})

	if strings.Contains(result.ForLLM, "ungranted-skill") {
		t.Fatalf("ungranted skill's slug leaked into search results: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "granted-skill") {
		t.Fatalf("expected the granted skill to appear in search results: %s", result.ForLLM)
	}
}

// TestSkillSearch_MatchListEmptyWhenNothingAllowed is a companion edge case:
// a query that ranks matches, none of which the agent may use, must report
// no matches rather than leaking a raw ranked list.
func TestSkillSearch_MatchListEmptyWhenNothingAllowed(t *testing.T) {
	tool := NewSkillTool(5)
	corpus := []SkillSearchDoc{
		{Slug: "denied-a", Description: "widgets widgets widgets"},
		{Slug: "denied-b", Description: "widgets widgets widgets too"},
	}
	tool.SetResolver(
		func(ctx context.Context, slug string) SkillLoadOutcome { return SkillLoadOutcome{} },
		func(ctx context.Context, slug string) bool { return false },
		func(ctx context.Context) []SkillSearchDoc { return corpus },
	)

	result := tool.Execute(context.Background(), map[string]any{"query": "widgets"})
	if strings.Contains(result.ForLLM, "denied-a") || strings.Contains(result.ForLLM, "denied-b") {
		t.Fatalf("a denied skill must never be named in the response: %s", result.ForLLM)
	}
}

// TestSkillSearch_InheritsResultBound covers ADR-072 D1.2/MIN-003 (spec #27,
// FR-008): search introduces NO new limit of its own — it inherits whatever
// bound the caller wires it with (mirroring ToolSearch's MaxSearchResults),
// and returns at most that many matches even when far more candidates rank
// and are all granted.
func TestSkillSearch_InheritsResultBound(t *testing.T) {
	const bound = 5
	tool := NewSkillTool(bound)

	const total = 25 // well past the bound
	corpus := make([]SkillSearchDoc, 0, total)
	for i := 0; i < total; i++ {
		slug := fmt.Sprintf("skill-%02d", i)
		corpus = append(corpus, SkillSearchDoc{Slug: slug, Description: "Use when the user asks about widgets"})
	}
	tool.SetResolver(
		func(ctx context.Context, slug string) SkillLoadOutcome { return SkillLoadOutcome{} },
		func(ctx context.Context, slug string) bool { return true }, // every candidate granted
		func(ctx context.Context) []SkillSearchDoc { return corpus },
	)

	result := tool.Execute(context.Background(), map[string]any{"query": "widgets"})

	idx := strings.Index(result.ForLLM, "{")
	if idx < 0 {
		t.Fatalf("expected a JSON payload in the result: %s", result.ForLLM)
	}
	var parsed struct {
		Matches []ToolSearchResult `json:"matches"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM[idx:]), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v (%s)", err, result.ForLLM)
	}
	if len(parsed.Matches) != bound {
		t.Fatalf("expected exactly %d matches (the inherited bound), got %d", bound, len(parsed.Matches))
	}
}

// TestSkillTool_LoadDeniedAndNotFoundAreDistinct is a sanity check that the
// load path's three-way outcome actually produces distinguishable results —
// exercised at the tool level, complementing pkg/skills'
// TestResolveSkillName_DeniesUngrantedRegistrySlug which covers the
// underlying resolution the wired `load` callback is expected to use.
func TestSkillTool_LoadDeniedAndNotFoundAreDistinct(t *testing.T) {
	tool := NewSkillTool(5)
	tool.SetResolver(
		func(ctx context.Context, slug string) SkillLoadOutcome {
			switch slug {
			case "granted-skill":
				return SkillLoadOutcome{Status: SkillLoadLoaded, Content: "the skill body", CanonicalSlug: slug}
			case "denied-skill":
				return SkillLoadOutcome{Status: SkillLoadDenied}
			default:
				return SkillLoadOutcome{Status: SkillLoadNotFound}
			}
		},
		func(ctx context.Context, slug string) bool { return slug == "granted-skill" },
		func(ctx context.Context) []SkillSearchDoc { return nil },
	)

	loaded := tool.Execute(context.Background(), map[string]any{"name": "granted-skill"})
	if loaded.IsError || !strings.Contains(loaded.ForLLM, "the skill body") {
		t.Fatalf("expected the granted skill to load successfully, got: %+v", loaded)
	}

	denied := tool.Execute(context.Background(), map[string]any{"name": "denied-skill"})
	if !denied.IsError || !strings.Contains(denied.ForLLM, PermissionDeniedCode) {
		t.Fatalf("expected a permission_denied result for a denied slug, got: %+v", denied)
	}

	notFound := tool.Execute(context.Background(), map[string]any{"name": "no-such-skill"})
	if !notFound.IsError || !strings.Contains(notFound.ForLLM, SkillNotFoundCode) {
		t.Fatalf("expected a skill_not_found result for an unresolvable slug, got: %+v", notFound)
	}

	if strings.Contains(denied.ForLLM, SkillNotFoundCode) {
		t.Fatalf("denied and not-found must use DIFFERENT discriminators: %s", denied.ForLLM)
	}
	if strings.Contains(notFound.ForLLM, PermissionDeniedCode) {
		t.Fatalf("denied and not-found must use DIFFERENT discriminators: %s", notFound.ForLLM)
	}
}
