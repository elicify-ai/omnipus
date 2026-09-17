// Omnipus — plan discoverability guard for multi-part parallel work
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT 2026-09-14 (A-17, B-7/B-9/B-10): an agent granted create_plan /
// execute_plan never created a plan for a multi-part "work in parallel" goal
// and fanned out raw delegate calls instead (0/5 runs). At the time
// create_plan and execute_plan were ADR-071 Tier 3 (search-only): the model
// could reach them only by naming them in ToolSearch or by a query that ranks
// them. delegate is Tier 1, so its description is the one text the model sees
// on every request. The ADR-071 amendment of 2026-09-14 has since moved both
// plan tools to the previewed tier (one line each in the "More tools" block),
// but a previewed tool is still loaded through ToolSearch, so both halves
// below still matter. These tests pin both halves of the fix: delegate's description names the
// plan tools by their exact, loadable names, and the natural ways a model
// describes parallel multi-part work find create_plan through ToolSearch —
// without create_plan swallowing queries that belong to other tools.

package tools_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newRealCatalogToolSearch builds a ToolSearch over the real static catalog
// (general builtins + system tools) with the production result/TTL defaults
// (loop.go: MaxSearchResults and TTL both default to 5) and an allow-all
// policy, so ranking and auto-load reflect the shipped descriptions alone.
func newRealCatalogToolSearch(t *testing.T) *tools.ToolsTool {
	t.Helper()
	reg := tools.NewToolRegistry()
	for _, tl := range tools.GeneralBuiltinMetadata() {
		reg.Register(tl)
	}
	for _, tl := range systools.AllTools(nil) {
		if _, exists := reg.GetIncludingHidden(tl.Name()); !exists {
			reg.Register(tl)
		}
	}
	tt := tools.NewToolsTool(reg, 5, 5)
	tt.SetResolver(
		func(context.Context, string) (bool, string) { return true, "" },
		func(_ context.Context, names []string) (map[string]any, []string) {
			schemas := make(map[string]any, len(names))
			for _, n := range names {
				schemas[n] = map[string]any{}
			}
			return schemas, nil
		},
	)
	return tt
}

var toolSearchPayload = regexp.MustCompile(`(?s)\{.*\}`)

// toolSearchLoaded runs a ToolSearch query and returns the auto-loaded names.
func toolSearchLoaded(t *testing.T, tt *tools.ToolsTool, query string) []string {
	t.Helper()
	res := tt.Execute(context.Background(), map[string]any{"query": query})
	if res.IsError {
		t.Fatalf("ToolSearch(%q) returned an error: %s", query, res.ForLLM)
	}
	raw := toolSearchPayload.FindString(res.ForLLM)
	if raw == "" {
		return nil // "No tools found matching the query." — nothing loaded.
	}
	var payload struct {
		Loaded []string `json:"loaded"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("ToolSearch(%q): cannot decode result payload: %v\n%s", query, err, res.ForLLM)
	}
	return payload.Loaded
}

func TestDelegateDescription_PointsParallelMultiPartWorkAtPlans(t *testing.T) {
	desc := tools.NewDelegateTool("", 0, 0).Description()
	for _, want := range []string{"create_plan", "execute_plan", "ToolSearch", "parallel", "write_set"} {
		if !strings.Contains(desc, want) {
			t.Errorf("delegate description must mention %q so a model fanning out parallel work "+
				"is pointed at a plan; got:\n%s", want, desc)
		}
	}

	// The pointer is only useful if the names it gives are real upfront tools.
	catalog := make(map[string]bool)
	for _, tl := range tools.GeneralBuiltinMetadata() {
		catalog[tl.Name()] = true
	}
	for _, name := range []string{"create_plan", "execute_plan"} {
		if !catalog[name] {
			t.Errorf("delegate's description names %q, but no catalog tool has that name — the pointer dangles", name)
		}
		if got := tools.ToolManifestTier(name); got != tools.ManifestFull {
			t.Errorf("ToolManifestTier(%q) = %v, want ManifestFull under ADR-090", name, got)
		}
	}
}

func TestPlanToolsAreUpfrontAndAbsentFromDeferredSearchCorpus(t *testing.T) {
	reg := tools.NewToolRegistry()
	for _, tl := range tools.GeneralBuiltinMetadata() {
		reg.Register(tl)
	}
	snapshot := reg.SnapshotSearchableTools()
	for _, doc := range snapshot.Docs {
		if doc.Name == "create_plan" || doc.Name == "execute_plan" {
			t.Errorf("upfront plan tool %q leaked into deferred ToolSearch corpus", doc.Name)
		}
	}
}
