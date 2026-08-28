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
// argument for six vault_* tools replacing nine knowledge_* ones is arithmetic
// over this number.
//
// That number has already been wrong twice in prose. ADR-068 revision 5 stated
// 102, from a grep that swept in tool-policy VALUES ("ask", "allow", "deny")
// alongside tool NAMES; every figure derived from it was wrong too. It was
// quoted three times as counted-not-estimated, inside the decision that makes
// count load-bearing. Prose cannot defend a number; a test can.
//
// HOW THE NUMBER WAS DERIVED, twice, independently:
//
//  1. Strip trailing // comments from the allStaticToolNames composite literal
//     (pkg/coreagent/core.go) and take the unique quoted identifiers -> 98.
//  2. Do the same over Sandbox.ToolPolicies in pkg/config/defaults.go, the
//     global ceiling, which is maintained separately -> 98, entry for entry,
//     with no diff.
//
// Two independently maintained sources agreeing is the reason to believe this
// count and not the previous one. TestCatalog_MatchesGlobalCeilingEntryForEntry
// (plan_supervisor_seed_test.go) keeps (2) honest; this file pins (1).
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
//
//   - Stage 4 of the vault-records plan is the one change already decided and
//     scheduled: it retires the nine knowledge_* tools and adds six vault_*
//     ones, so this constant becomes 95 (98 - 9 + 6) and
//     knowledgeToolCountToday becomes 0. That expected value is recorded here
//     deliberately, so whoever lands Stage 4 can confirm they got the arithmetic
//     they intended rather than discovering a number that merely differs.
//     See docs/internal/specs/vault-records-implementation-plan-2026-08-28.md,
//     Stage 4 exit: "the catalog assertion reads 95".
const catalogSizeToday = 98

// knowledgeToolCountToday is how many of the catalog are knowledge_* tools —
// the nine ADR-068 D15 retires. Becomes 0 after Stage 4.
const knowledgeToolCountToday = 9

// vaultToolCountAfterStage4 is the replacement surface: six vault_* tools
// (ADR-068 D15, five in revision 5 plus D15.6's sixth).
const vaultToolCountAfterStage4 = 6

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

// TestCatalog_KnowledgeToolCount pins the nine names ADR-068 D15 retires. It is
// the other half of the arithmetic: without it, Stage 4 could delete eight and
// still satisfy a count that had been adjusted to match.
func TestCatalog_KnowledgeToolCount(t *testing.T) {
	want := map[string]bool{
		"knowledge_search":         true,
		"knowledge_graph":          true,
		"knowledge_tasks":          true,
		"knowledge_create":         true,
		"knowledge_link":           true,
		"knowledge_set_property":   true,
		"knowledge_append_section": true,
		"knowledge_move":           true,
		"knowledge_rename":         true,
	}
	if len(want) != knowledgeToolCountToday {
		t.Fatalf("this test's own expected set holds %d names, but "+
			"knowledgeToolCountToday says %d", len(want), knowledgeToolCountToday)
	}

	found := 0
	for _, n := range allStaticToolNames {
		if !strings.HasPrefix(n, "knowledge_") {
			continue
		}
		found++
		if !want[n] {
			t.Errorf("unexpected knowledge_* tool %q in the catalog — ADR-068 D15 "+
				"enumerates exactly nine, and Stage 4 retires that set by name", n)
		}
		delete(want, n)
	}
	for n := range want {
		t.Errorf("ADR-068 D15 names %q as one of the nine tools to retire, but it is "+
			"not in the catalog", n)
	}
	if found != knowledgeToolCountToday {
		t.Errorf("catalog holds %d knowledge_* tools, expected %d",
			found, knowledgeToolCountToday)
	}
}

// TestCatalog_Stage4Arithmetic states the target rather than leaving it in prose.
// It cannot fail today; it exists so that the number Stage 4 must produce is
// written down next to the number it replaces, in the file whoever lands Stage 4
// will be editing.
func TestCatalog_Stage4Arithmetic(t *testing.T) {
	const wantAfterStage4 = 95
	got := catalogSizeToday - knowledgeToolCountToday + vaultToolCountAfterStage4
	if got != wantAfterStage4 {
		t.Fatalf("ADR-068 D15.0's arithmetic is %d - %d + %d = %d, but the ADR and the "+
			"implementation plan both say the catalog reads %d after Stage 4. One of "+
			"the four numbers in this file is wrong.",
			catalogSizeToday, knowledgeToolCountToday, vaultToolCountAfterStage4,
			got, wantAfterStage4)
	}

	// Guard against this test quietly outliving its purpose: once no
	// knowledge_* tool remains, catalogSizeToday must already have been moved
	// to the post-Stage-4 value.
	if knowledgeToolCountToday == 0 && catalogSizeToday != wantAfterStage4 {
		t.Errorf("the knowledge_* tools are retired but catalogSizeToday is still %d, "+
			"not %d", catalogSizeToday, wantAfterStage4)
	}
}

// TestCatalog_NoVaultToolsYet is the counterpart: it asserts Stage 4 has not
// half-landed. A vault_* name appearing while the knowledge_* nine are still
// present would put the catalog at 104, the shape D15.0 explicitly rejects
// (nine new tools ALONGSIDE nine old ones was revision 4's 107).
func TestCatalog_NoVaultToolsYet(t *testing.T) {
	if knowledgeToolCountToday == 0 {
		t.Skip("Stage 4 has landed; the co-existence this guards against is over")
	}
	for _, n := range allStaticToolNames {
		if strings.HasPrefix(n, "vault_") {
			t.Errorf("vault_* tool %q is in the catalog while all nine knowledge_* "+
				"tools are still present — ADR-068 D15 replaces them, it does not add "+
				"to them. Retire the nine in the same change.", n)
		}
	}
}
