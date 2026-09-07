// Omnipus — ADR-068 D15.0: the static builtin tool catalog's size, asserted.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY A TEST ASSERTS A NUMBER — ADR-068 D15.0
//
// ADR-068 makes tool COUNT a load-bearing design constraint: every tool
// definition is serialised into the system prompt of every agent on every
// request, and selection accuracy degrades as the catalog grows. The whole
// argument for six knowledge_* tools replacing nine knowledge_* ones is
// arithmetic over this number.
//
// That number has already been wrong twice in prose. ADR-068 revision 5
// stated 102, from a grep that swept in tool-policy VALUES ("ask", "allow",
// "deny") alongside tool NAMES; every figure derived from it was wrong too.
// It was quoted three times as counted-not-estimated, inside the decision
// that makes count load-bearing. Prose cannot defend a number; a test can.
//
// HOW THE NUMBER WAS DERIVED, twice, independently:
//
//  1. Strip trailing // comments from the allStaticToolNames composite literal
//     (pkg/coreagent/core.go) and take the unique quoted identifiers -> 95.
//  2. Do the same over Sandbox.ToolPolicies in pkg/config/defaults.go, the
//     global ceiling, which is maintained separately -> 95, entry for entry,
//     with no diff.
//
// Two independently maintained sources agreeing is the reason to believe this
// count. TestCatalog_MatchesGlobalCeilingEntryForEntry
// (plan_supervisor_seed_test.go) keeps (2) honest; this file pins (1).
//
// STAGE 4 HAS LANDED (Wave 2): the nine ADR-067 knowledge_* names
// (knowledge_search, knowledge_graph, knowledge_create, knowledge_link,
// knowledge_set_property, knowledge_append_section, knowledge_tasks,
// knowledge_move, knowledge_rename) are retired from this catalog and
// replaced by ADR-068's six (knowledge_describe, knowledge_find,
// knowledge_read, knowledge_edit, knowledge_restructure,
// knowledge_configure) — 98 - 9 + 6 = 95, matching
// docs/internal/specs/vault-records-implementation-plan-2026-08-28.md's
// Stage 4 exit ("the catalog assertion reads 95"). An earlier draft of this
// file (and that plan doc, before its own docs-rename cleanup) referred to
// the replacement six as "vault_*" tools; the names that actually shipped in
// pkg/knowledge/tools.go, knowledge_edit.go, knowledge_restructure.go,
// knowledge_configure.go and pkg/records/knowledgefind/tool.go all keep the
// "knowledge_" prefix — only the WIRE contract types (VaultFindRequest,
// VaultFilterNode, …) carry "Vault". This file is corrected to match the
// code that actually ships, not the earlier naming draft.
// ---------------------------------------------------------------------------

// catalogSizeToday is the current size of the static builtin tool catalog.
//
// >>> WHEN THIS FAILS, READ THIS BEFORE CHANGING THE NUMBER. <<<
//
// This is not a bookkeeping constant to bump until the test goes green. It is
// the input to ADR-068 D15.0's argument, so a change to it is a change to a
// design constraint and belongs in a commit that says so.
//
//   - Adding a tool: the count rises. ADR-068 D15.0's position is that the
//     catalog is already at the size where selection accuracy is the binding
//     constraint, so a new entry needs a reason, not just a slot. Update this
//     number in the same commit, and say in the message which tool and why.
const catalogSizeToday = 95

// currentKnowledgeToolNames is the exact six ADR-068 D15.3 seeds — the
// replacement for ADR-067's nine, superseded (Stage 4, Wave 2). Split by
// blast radius: three read (describe/find/read), knowledge_edit (one named
// file), knowledge_restructure (cascading rename/move/trash/restore),
// knowledge_configure (the schema/view control plane).
var currentKnowledgeToolNames = map[string]bool{
	"knowledge_describe":    true,
	"knowledge_find":        true,
	"knowledge_read":        true,
	"knowledge_edit":        true,
	"knowledge_restructure": true,
	"knowledge_configure":   true,
}

// retiredKnowledgeToolNames is ADR-067's nine, which ADR-068 supersedes.
// None of these may appear in allStaticToolNames any more — the retirement
// is a removal from the agent-callable catalog, not an addition alongside
// the six (which would put the catalog at 104, the shape ADR-068 D15.0
// explicitly rejects — revision 4's 107 was nine new tools alongside nine
// old ones).
var retiredKnowledgeToolNames = []string{
	"knowledge_search", "knowledge_graph", "knowledge_create",
	"knowledge_link", "knowledge_set_property", "knowledge_append_section",
	"knowledge_tasks", "knowledge_move", "knowledge_rename",
}

// TestCatalog_SizeIsPinned asserts the count ADR-068 D15.0 reasons from, so the
// ADR's prose cannot rot away from the code again.
func TestCatalog_SizeIsPinned(t *testing.T) {
	if got := len(allStaticToolNames); got != catalogSizeToday {
		t.Errorf("the static builtin tool catalog holds %d tools, expected %d.\n"+
			"This is ADR-068 D15.0's load-bearing number, not bookkeeping — read the "+
			"comment on catalogSizeToday in this file before changing it, and say in "+
			"the commit message which tool moved and why.", got, catalogSizeToday)
	}
}

// TestCatalog_HasNoDuplicates — a duplicate would make len() overstate the real
// surface, so the pinned count only means something alongside this.
func TestCatalog_HasNoDuplicates(t *testing.T) {
	seen := make(map[string]int, len(allStaticToolNames))
	for _, n := range allStaticToolNames {
		seen[n]++
	}
	for n, c := range seen {
		if c > 1 {
			t.Errorf("tool %q appears %d times in allStaticToolNames", n, c)
		}
	}
	if got := len(seen); got != catalogSizeToday {
		t.Errorf("catalog has %d unique names but %d entries — the pinned count of %d "+
			"is only meaningful if every entry is distinct",
			got, len(allStaticToolNames), catalogSizeToday)
	}
}

// TestCatalog_KnowledgeToolCount pins the exact six ADR-068 names present in
// the catalog — the other half of the arithmetic: without it, a change could
// retire eight of the nine and add seven of the six and still satisfy a bare
// count that had been adjusted to match.
func TestCatalog_KnowledgeToolCount(t *testing.T) {
	want := make(map[string]bool, len(currentKnowledgeToolNames))
	for n := range currentKnowledgeToolNames {
		want[n] = true
	}

	found := 0
	for _, n := range allStaticToolNames {
		if !strings.HasPrefix(n, "knowledge_") {
			continue
		}
		found++
		if !want[n] {
			t.Errorf("unexpected knowledge_* tool %q in the catalog — ADR-068 D15.3 "+
				"names exactly six, and this is not one of them", n)
			continue
		}
		delete(want, n)
	}
	for n := range want {
		t.Errorf("ADR-068 D15.3 names %q as one of the six current knowledge tools, "+
			"but it is not in the catalog", n)
	}
	if found != len(currentKnowledgeToolNames) {
		t.Errorf("catalog holds %d knowledge_* tools, expected %d",
			found, len(currentKnowledgeToolNames))
	}
}

// TestCatalog_RetiredKnowledgeToolsAreGone proves the nine ADR-067 names are
// actually ABSENT, not merely uncounted — a name could vanish from
// TestCatalog_KnowledgeToolCount's tally through a typo (e.g. a stray
// underscore) rather than a genuine retirement, and that typo'd name would
// still be silently present in the catalog, occupying a policy-coverage slot
// nobody intended to keep.
func TestCatalog_RetiredKnowledgeToolsAreGone(t *testing.T) {
	present := make(map[string]bool, len(allStaticToolNames))
	for _, n := range allStaticToolNames {
		present[n] = true
	}
	for _, n := range retiredKnowledgeToolNames {
		if present[n] {
			t.Errorf("retired ADR-067 tool %q is still in the catalog — ADR-068 D15 "+
				"replaces the nine, it does not keep any of them alongside the six", n)
		}
	}
}

// TestCatalog_Stage4Arithmetic states the arithmetic Stage 4 was built to
// satisfy, so the number this catalog holds today stays traceable to the
// decision that set it rather than becoming a bare literal nobody can check.
func TestCatalog_Stage4Arithmetic(t *testing.T) {
	const (
		preStage4Size         = 98
		retiredCount          = 9
		newKnowledgeToolCount = 6
	)
	if retiredCount != len(retiredKnowledgeToolNames) {
		t.Fatalf("retiredCount const says %d but retiredKnowledgeToolNames holds %d names",
			retiredCount, len(retiredKnowledgeToolNames))
	}
	if newKnowledgeToolCount != len(currentKnowledgeToolNames) {
		t.Fatalf("newKnowledgeToolCount const says %d but currentKnowledgeToolNames holds %d names",
			newKnowledgeToolCount, len(currentKnowledgeToolNames))
	}
	got := preStage4Size - retiredCount + newKnowledgeToolCount
	if got != catalogSizeToday {
		t.Fatalf("ADR-068 D15.0's arithmetic is %d - %d + %d = %d, but catalogSizeToday "+
			"says %d. One of the numbers in this file is wrong.",
			preStage4Size, retiredCount, newKnowledgeToolCount, got, catalogSizeToday)
	}
}
