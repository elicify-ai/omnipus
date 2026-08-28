// Omnipus — ToolSearch ambiguity band tests (ADR-071 D2, §3.2)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Unit-level tests against selectSearchPromotionCandidates/candidateQualifies
// directly, using hand-crafted scores rather than real BM25 ranking — the
// BDD scenarios in docs/internal/specs/tool-manifest-tier-redesign-spec.md
// (User Story 3) are stated in terms of exact scores ("top scores 10.0,
// runner-up scores 8.5"), which this file reproduces verbatim rather than
// approximating via query text tuned to produce them.

package tools

import (
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

func cand(name string, score float32, cat ToolCategory) toolSearchCandidate {
	return toolSearchCandidate{name: name, description: "desc for " + name, score: score, category: cat}
}

// Scenario: Two near-tied results are both made usable.
func TestAmbiguity_NearTiedBothPromoted(t *testing.T) {
	loadable := []toolSearchCandidate{
		cand("top", 10.0, CategoryFilesystem),
		cand("runner", 8.5, CategoryFilesystem),
	}
	got := selectSearchPromotionCandidates(loadable)
	if len(got) != 2 {
		t.Fatalf("promoted count = %d, want 2 (%v)", len(got), got)
	}
	if got[0].name != "top" || got[1].name != "runner" {
		t.Errorf("promoted = %v, want [top runner] in rank order", got)
	}
}

// Scenario: A dominant top result is made usable alone.
func TestAmbiguity_DominantTopAlone(t *testing.T) {
	loadable := []toolSearchCandidate{
		cand("top", 10.0, CategoryFilesystem),
		cand("r1", 3.9, CategoryFilesystem),
		cand("r2", 2.0, CategoryFilesystem),
	}
	got := selectSearchPromotionCandidates(loadable)
	if len(got) != 1 || got[0].name != "top" {
		t.Errorf("promoted = %v, want exactly [top]", got)
	}
}

// Scenario: A cross-kind near-miss is made usable — send_file (communication)
// at 9.1 and write_file (filesystem) at 5.2 (5.2 >= 0.50*9.1 = 4.55).
func TestAmbiguity_CrossKindNearMissPromoted(t *testing.T) {
	loadable := []toolSearchCandidate{
		cand("send_file", 9.1, CategoryCommunication),
		cand("write_file", 5.2, CategoryFilesystem),
	}
	got := selectSearchPromotionCandidates(loadable)
	if len(got) != 2 {
		t.Fatalf("promoted count = %d, want 2 (%v)", len(got), got)
	}
}

// Scenario: The cross-kind rule is decided by the tools' own category
// values — the SAME score pair with equal categories promotes only the top.
func TestAmbiguity_CrossKindRuleUsesCategoryEquality(t *testing.T) {
	sameCategory := []toolSearchCandidate{
		cand("send_file", 9.1, CategoryFilesystem),
		cand("write_file", 5.2, CategoryFilesystem),
	}
	got := selectSearchPromotionCandidates(sameCategory)
	if len(got) != 1 {
		t.Errorf("same-category pair: promoted = %v, want exactly [send_file] (5.2/9.1 < 0.80, same category)", got)
	}
}

// Scenario: A destructive cross-kind near-miss is excluded.
func TestAmbiguity_DestructiveCrossKindExcluded(t *testing.T) {
	if !isAdministrativeToolName("delete_workspace") {
		t.Fatal("fixture defect: delete_workspace must be in administrativeToolNames")
	}
	loadable := []toolSearchCandidate{
		cand("top_hit", 9.1, CategoryFilesystem),
		cand("delete_workspace", 5.2, CategoryWorkspaces),
	}
	got := selectSearchPromotionCandidates(loadable)
	if len(got) != 1 || got[0].name != "top_hit" {
		t.Errorf("promoted = %v, want exactly [top_hit] — administrative tool must not enter the speculative band", got)
	}
}

// Scenario: A confirmation-gated ("ask" policy) cross-kind near-miss is excluded.
func TestAmbiguity_AskPolicyCrossKindExcluded(t *testing.T) {
	loadable := []toolSearchCandidate{
		cand("top_hit", 9.1, CategoryFilesystem),
		{name: "ask_gated", description: "d", score: 5.2, category: CategoryWorkspaces, askPolicy: true},
	}
	got := selectSearchPromotionCandidates(loadable)
	if len(got) != 1 || got[0].name != "top_hit" {
		t.Errorf("promoted = %v, want exactly [top_hit] — ask-policy tool must not enter the speculative band", got)
	}
}

// Scenario: A destructive result inside the confident band is still made
// usable — the exclusion narrows only the speculative (cross-category)
// comparison, not the confident score-band one.
func TestAmbiguity_DestructiveInConfidentBandStillPromoted(t *testing.T) {
	if !isAdministrativeToolName("delete_workspace") {
		t.Fatal("fixture defect: delete_workspace must be in administrativeToolNames")
	}
	loadable := []toolSearchCandidate{
		cand("top_hit", 10.0, CategoryWorkspaces),
		cand("delete_workspace", 8.5, CategoryWorkspaces), // 8.5/10.0 = 0.85 >= 0.80
	}
	got := selectSearchPromotionCandidates(loadable)
	if len(got) != 2 {
		t.Errorf("promoted = %v, want [top_hit delete_workspace] — confident band is unrestricted", got)
	}
}

// Scenario: The number made usable is capped at three — five permitted
// results scoring 10.0, 9.5, 9.2, 9.0, 8.8 (all within the 0.80 band of the
// top) promote only the three highest-ranked.
func TestAmbiguity_CappedAtThree(t *testing.T) {
	loadable := []toolSearchCandidate{
		cand("r1", 10.0, CategoryFilesystem),
		cand("r2", 9.5, CategoryFilesystem),
		cand("r3", 9.2, CategoryFilesystem),
		cand("r4", 9.0, CategoryFilesystem),
		cand("r5", 8.8, CategoryFilesystem),
	}
	got := selectSearchPromotionCandidates(loadable)
	if len(got) != 3 {
		t.Fatalf("promoted count = %d, want 3 (%v)", len(got), got)
	}
	want := []string{"r1", "r2", "r3"}
	for i, w := range want {
		if got[i].name != w {
			t.Errorf("promoted[%d] = %q, want %q (must be the three highest-ranked, in rank order)", i, got[i].name, w)
		}
	}
}

// Scenario: A single permitted result skips the comparison entirely.
func TestAmbiguity_SingleResultSkipsComparison(t *testing.T) {
	loadable := []toolSearchCandidate{cand("only", 1.0, CategoryFilesystem)}
	got := selectSearchPromotionCandidates(loadable)
	if len(got) != 1 || got[0].name != "only" {
		t.Errorf("promoted = %v, want exactly [only]", got)
	}
}

// Edge case: a candidate's score is zero or negative relative to a positive
// top score is below both thresholds and never promoted.
func TestAmbiguity_ZeroOrNegativeScoreNeverPromoted(t *testing.T) {
	loadable := []toolSearchCandidate{
		cand("top", 10.0, CategoryFilesystem),
		cand("zero", 0, CategoryCommunication),
		cand("negative", -1, CategoryCommunication),
	}
	got := selectSearchPromotionCandidates(loadable)
	if len(got) != 1 || got[0].name != "top" {
		t.Errorf("promoted = %v, want exactly [top]", got)
	}
}

// TestAdministrativeToolNames_Drift pins ADR-071 §3.2.1's 13-name
// "destructive-and-install-wide" seed and the coverage tripwire regex,
// mirroring the established TestVisibility_PreviewedSetIsExactlyEight /
// TestCatalog_MatchesGlobalCeilingEntryForEntry pattern.
func TestAdministrativeToolNames_Drift(t *testing.T) {
	want := []string{
		"delete_agent", "delete_task", "delete_task_in_workspace", "delete_workspace",
		"remove_mcp_server", "remove_skill", "disable_channel", "enable_channel",
		"add_mcp_server", "configure_channel", "configure_provider", "set_config",
		"stop_plan",
	}
	got := AdministrativeToolNames()
	if len(got) != len(want) {
		t.Fatalf("AdministrativeToolNames() = %v (len %d), want %v (len %d)", got, len(got), want, len(want))
	}
	wantSet := make(map[string]bool, len(want))
	for _, n := range want {
		wantSet[n] = true
	}
	for _, n := range got {
		if !wantSet[n] {
			t.Errorf("AdministrativeToolNames() contains unexpected name %q", n)
		}
	}
	for _, n := range want {
		if !isAdministrativeToolName(n) {
			t.Errorf("isAdministrativeToolName(%q) = false, want true", n)
		}
	}

	// Assertion 3: every administrative name must resolve to Tier 3
	// (ManifestSearchOnly) — the narrowing is meaningless for a Tier 1/2
	// tool, so promoting one out of Tier 3 must force a re-decision.
	for _, n := range got {
		if tier := ToolManifestTier(n); tier != ManifestLazy {
			t.Errorf("administrative tool %q resolves ManifestTier %v, want ManifestLazy", n, tier)
		}
		if vis := ToolManifestVisibility(n); vis != ManifestSearchOnly {
			t.Errorf("administrative tool %q resolves ManifestVisibility %v, want ManifestSearchOnly", n, vis)
		}
	}

	// Assertion 2: every administrative name must appear in the global
	// static-catalog ceiling (pkg/coreagent.allStaticToolNames) — this is
	// the assertion that catches a rename (a D1/D4-shaped change) turning a
	// live exclusion into a dead string with no other symptom.
	catalog := make(map[string]bool)
	for _, n := range coreagent.AllStaticToolNames() {
		catalog[n] = true
	}
	for _, n := range got {
		if !catalog[n] {
			t.Errorf("administrative tool %q is not in coreagent.AllStaticToolNames() — renamed or removed?", n)
		}
	}

	// Assertion 4 (coverage tripwire): every catalog name matching the
	// destructive/administrative naming convention — or the literal
	// "set_config" — must be adjudicated: present in administrativeToolNames
	// or explicitly exempted (with a reason) in administrativeExemptNames.
	// This is a backstop, not a definition (ADR-071 §3.2.1's "residual gap"
	// note) — it cannot detect a destructive tool with a benign name, only
	// force a decision on one whose name follows the convention.
	adminPattern := regexp.MustCompile(`^(delete|remove|disable|purge|wipe|revoke|drop|reset|destroy)_`)
	for _, n := range coreagent.AllStaticToolNames() {
		if !adminPattern.MatchString(n) && n != "set_config" {
			continue
		}
		if isAdministrativeToolName(n) {
			continue
		}
		if _, exempt := administrativeExemptNames[n]; exempt {
			continue
		}
		t.Errorf(
			"catalog tool %q matches the destructive-name pattern but is in neither "+
				"administrativeToolNames nor administrativeExemptNames — adjudicate it",
			n,
		)
	}
}
