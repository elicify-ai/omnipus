// Omnipus — FR-104a's unit suite: a relation's `to:` is inferred from what
// its links actually resolve to. Unanimity or a >=2/3 supermajority declares
// it; below that the importer says so and names the fix instead of guessing.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// nameIndexOf builds a link-target index directly: stem -> record type. An
// empty type means "a real note that is not a record", which is a distinct
// case from "no note with that title", and the two must not be conflated.
func nameIndexOf(byStem map[string]string) *NameIndex {
	idx := &NameIndex{byStem: map[string][]string{}}
	for stem, typ := range byStem {
		idx.byStem[records.FoldKey(stem)] = append(idx.byStem[records.FoldKey(stem)], typ)
	}
	return idx
}

// linksTo builds one property observation whose values are wikilinks to the
// named targets.
func linksTo(prop string, targets ...string) *PropertyObservation {
	po := &PropertyObservation{Name: prop}
	for i, tgt := range targets {
		po.Values = append(po.Values, observedValue{
			Text:     "[[" + tgt + "]]",
			NotePath: fmt.Sprintf("note-%d.md", i),
		})
	}
	return po
}

// mixedIndex names `contactN` notes as contacts and `taskN` notes as tasks.
func mixedIndex(contacts, tasks int) (*NameIndex, []string) {
	stems := map[string]string{}
	var targets []string
	for i := 0; i < contacts; i++ {
		s := fmt.Sprintf("contact%d", i)
		stems[s] = "contact"
		targets = append(targets, s)
	}
	for i := 0; i < tasks; i++ {
		s := fmt.Sprintf("task%d", i)
		stems[s] = "task"
		targets = append(targets, s)
	}
	return nameIndexOf(stems), targets
}

// ---------------------------------------------------------------------------
// The threshold, at its exact boundary.
// ---------------------------------------------------------------------------

// TestInferRelationTarget_SupermajorityThreshold is FR-104a's rule as a
// boundary table. The test is `count/total >= 2/3` in exact integer
// arithmetic, so 2 of 3 and 6 of 9 PASS while 4 of 7 and 5 of 9 FAIL, with
// no rounding to argue about.
//
// The 4-of-7 row is the one that matters: under the PLURALITY rule this
// replaced, `contact` would win outright and be written into the schema as
// `to: contact` with a majority of the evidence against it.
func TestInferRelationTarget_SupermajorityThreshold(t *testing.T) {
	cases := []struct {
		contacts, tasks int
		wantTo          string
		wantRule        RelationRule
		wantDeclared    string
	}{
		{3, 0, "contact", RelationUnanimous, ""},             // unanimous: nil report
		{2, 1, "contact", RelationSupermajority, "relation"}, // exactly 2/3 — passes
		{6, 3, "contact", RelationSupermajority, "relation"}, // exactly 2/3 — passes
		{7, 3, "contact", RelationSupermajority, "relation"}, // 7/10 — passes
		{4, 3, "", RelationNoMajority, "text"},               // 4/7 — just under
		{5, 4, "", RelationNoMajority, "text"},               // 5/9 — just under
		{1, 1, "", RelationNoMajority, "text"},               // a dead tie is never a winner
	}
	for _, tc := range cases {
		name := fmt.Sprintf("%d_contact_%d_task", tc.contacts, tc.tasks)
		t.Run(name, func(t *testing.T) {
			idx, targets := mixedIndex(tc.contacts, tc.tasks)
			po := linksTo("related", targets...)

			to, rep := inferRelationTarget("project", po, idx)

			if to != tc.wantTo {
				t.Errorf("to = %q, want %q (%d of %d resolved links)",
					to, tc.wantTo, tc.contacts, tc.contacts+tc.tasks)
			}
			if tc.wantRule == RelationUnanimous {
				if rep != nil {
					t.Errorf("a unanimous target produced a split report: %+v", rep)
				}
				return
			}
			if rep == nil {
				t.Fatalf("no split report for a non-unanimous inference (rule %s)", tc.wantRule)
			}
			if rep.Rule != tc.wantRule {
				t.Errorf("rule = %q, want %q", rep.Rule, tc.wantRule)
			}
			if rep.Declared != tc.wantDeclared {
				t.Errorf("declared = %q, want %q", rep.Declared, tc.wantDeclared)
			}
			if rep.ResolvedTotal != tc.contacts+tc.tasks {
				t.Errorf("resolved total = %d, want %d", rep.ResolvedTotal, tc.contacts+tc.tasks)
			}
		})
	}
}

// TestInferRelationTarget_SupermajorityNamesTheMinority: FR-104a requires
// the minority to be reported BY NAME. Those links are real type mismatches
// and D5/FR-034's relation_type_mismatch finding is where they surface —
// the operator has to be told which ones, not merely that some exist.
func TestInferRelationTarget_SupermajorityNamesTheMinority(t *testing.T) {
	idx, targets := mixedIndex(2, 1)
	to, rep := inferRelationTarget("project", linksTo("related", targets...), idx)

	if to != "contact" {
		t.Fatalf("to = %q, want contact", to)
	}
	if rep == nil {
		t.Fatal("no split report")
	}
	if rep.MajorityType != "contact" || rep.MajorityCount != 2 {
		t.Errorf("majority = %s x%d, want contact x2", rep.MajorityType, rep.MajorityCount)
	}
	if len(rep.Minority) != 1 || !strings.Contains(rep.Minority[0], "task") {
		t.Errorf("minority = %v, want the one task link named", rep.Minority)
	}
	if !strings.Contains(rep.Minority[0], "1") {
		t.Errorf("minority %q does not carry its count", rep.Minority[0])
	}
}

// TestInferRelationTarget_NoMajorityNamesTheFix: the report must hand the
// operator the one-line edit, not just the problem. A finding with no
// remedy is a complaint.
func TestInferRelationTarget_NoMajorityNamesTheFix(t *testing.T) {
	idx, targets := mixedIndex(4, 3)
	to, rep := inferRelationTarget("project", linksTo("related", targets...), idx)

	if to != "" {
		t.Fatalf("to = %q, want empty — 4 of 7 is below the threshold", to)
	}
	if rep.Remedy == "" {
		t.Fatal("no remedy named for a property the importer refused to type")
	}
	if !strings.Contains(rep.Remedy, "knowledge_configure") {
		t.Errorf("remedy does not name the command that fixes it: %q", rep.Remedy)
	}
	for _, want := range []string{"project", "related", "contact", "task"} {
		if !strings.Contains(rep.Remedy, want) {
			t.Errorf("remedy omits %q, so it cannot be run as written: %q", want, rep.Remedy)
		}
	}
	// With no majority, the report must present the WHOLE evidence set —
	// that is what the operator chooses from.
	if len(rep.Minority) != 2 {
		t.Errorf("evidence set = %v, want both candidate types", rep.Minority)
	}
	if rep.MajorityType != "" || rep.MajorityCount != 0 {
		t.Errorf("a no-majority report still names a majority: %s x%d — that is the plurality rule leaking back in",
			rep.MajorityType, rep.MajorityCount)
	}
}

// TestInferRelationTarget_NothingResolved covers the two ways a link can
// fail to be evidence: no note has that title, and the note it names is not
// a record. Neither is evidence for any target type.
func TestInferRelationTarget_NothingResolved(t *testing.T) {
	t.Run("dangling links", func(t *testing.T) {
		idx := nameIndexOf(map[string]string{"somewhere": "contact"})
		to, rep := inferRelationTarget("project", linksTo("related", "Ghost", "Phantom"), idx)
		if to != "" {
			t.Errorf("to = %q, want empty", to)
		}
		if rep.Rule != RelationUnresolved || rep.Declared != "text" {
			t.Errorf("rule = %q declared = %q, want unresolved/text", rep.Rule, rep.Declared)
		}
		if rep.Unresolved != 2 {
			t.Errorf("unresolved = %d, want 2", rep.Unresolved)
		}
		if rep.Remedy == "" {
			t.Error("no remedy named")
		}
	})

	t.Run("links resolve to notes that are not records", func(t *testing.T) {
		// An empty type is a real note that carries no `type:`. It resolves,
		// so it is not "unresolved", but it is not evidence for a type
		// either — and it must never be counted as one.
		idx := nameIndexOf(map[string]string{"readme": "", "scratch": ""})
		to, rep := inferRelationTarget("project", linksTo("related", "README", "Scratch"), idx)
		if to != "" {
			t.Fatalf("to = %q, want empty — a non-record note is not evidence for any type", to)
		}
		if rep.ResolvedTotal != 0 {
			t.Errorf("resolved total = %d, want 0", rep.ResolvedTotal)
		}
		if rep.Rule != RelationUnresolved {
			t.Errorf("rule = %q, want unresolved", rep.Rule)
		}
	})
}

// ---------------------------------------------------------------------------
// The FR-034 consequence: a relation with no `to:` is rejected at load time,
// so "no majority" must declare TEXT, never a relation with a blank target.
// ---------------------------------------------------------------------------

func TestClassifyProperty_NoMajorityDeclaresTextNotABrokenRelation(t *testing.T) {
	idx, targets := mixedIndex(4, 3)
	po := linksTo("related", targets...)

	ip := classifyProperty("project", po, len(po.Values), idx)

	if ip.Type == records.TypeRelation {
		t.Fatalf("declared a relation with to=%q — schema load REJECTS a relation with no target, taking the whole record type down", ip.To)
	}
	if ip.Type != records.TypeText {
		t.Errorf("type = %q, want text", ip.Type)
	}
	if ip.To != "" {
		t.Errorf("to = %q on a text property", ip.To)
	}
	if ip.RelationSplit == nil {
		t.Error("the refusal was not reported at all — it would be invisible to the operator")
	}
}

func TestClassifyProperty_UnanimousLinksDeclareARelation(t *testing.T) {
	idx, targets := mixedIndex(3, 0)
	po := linksTo("owner", targets...)

	ip := classifyProperty("project", po, len(po.Values), idx)

	if ip.Type != records.TypeRelation {
		t.Fatalf("type = %q, want relation", ip.Type)
	}
	if ip.To != "contact" {
		t.Errorf("to = %q, want contact", ip.To)
	}
	if ip.Kind != ClassifyRelation {
		t.Errorf("kind = %q, want %q", ip.Kind, ClassifyRelation)
	}
	if ip.RelationSplit != nil {
		t.Error("a unanimous relation produced a split report — there is nothing split about it")
	}
}

// ---------------------------------------------------------------------------
// Determinism.
// ---------------------------------------------------------------------------

// TestHighestCount_TieBreaksByNameNotMapOrder: Go randomises map iteration,
// so a tie decided by "whichever key came first" gives a different schema on
// different runs of the same vault. The tie-break is by name.
func TestHighestCount_TieBreaksByNameNotMapOrder(t *testing.T) {
	byType := map[string]int{"zebra": 4, "alpha": 4, "middle": 4}
	first, firstCount := highestCount(byType)
	if firstCount != 4 {
		t.Fatalf("count = %d, want 4", firstCount)
	}
	if first != "alpha" {
		t.Errorf("tie broke to %q, want the lexically first name \"alpha\"", first)
	}
	for i := 0; i < 200; i++ {
		got, _ := highestCount(byType)
		if got != first {
			t.Fatalf("highestCount is not deterministic: got %q then %q on iteration %d", first, got, i)
		}
	}
}

// TestInferRelationTarget_IsDeterministicAcrossRuns runs the whole inference
// repeatedly over a tied vault. The answer, and the reported evidence, must
// be byte-identical every time or two imports of one vault disagree.
func TestInferRelationTarget_IsDeterministicAcrossRuns(t *testing.T) {
	idx, targets := mixedIndex(3, 3)
	var wantTo string
	var wantMinority string
	for i := 0; i < 100; i++ {
		to, rep := inferRelationTarget("project", linksTo("related", targets...), idx)
		minority := strings.Join(rep.Minority, "|")
		if i == 0 {
			wantTo, wantMinority = to, minority
			continue
		}
		if to != wantTo || minority != wantMinority {
			t.Fatalf("run %d disagreed: to=%q minority=%q, first run said to=%q minority=%q",
				i, to, minority, wantTo, wantMinority)
		}
	}
}

// ---------------------------------------------------------------------------
// End-to-end through the real grouping path.
// ---------------------------------------------------------------------------

// TestInferSchema_RelationTargetFromRealNotes drives FR-104a through the
// functions the importer actually calls — CollectTypeGroups, BuildNameIndex,
// InferSchema — rather than the internal one, so the wiring between them is
// covered too.
func TestInferSchema_RelationTargetFromRealNotes(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "Alice.md", "---\ntype: contact\nname: Alice\n---\n"),
		noteOnDisk(t, dir, "Bob.md", "---\ntype: contact\nname: Bob\n---\n"),
		noteOnDisk(t, dir, "Carol.md", "---\ntype: contact\nname: Carol\n---\n"),
		noteOnDisk(t, dir, "Apollo.md", "---\ntype: project\nowner: \"[[Alice]]\"\n---\n"),
		noteOnDisk(t, dir, "Gemini.md", "---\ntype: project\nowner: \"[[Bob]]\"\n---\n"),
		noteOnDisk(t, dir, "Mercury.md", "---\ntype: project\nowner: \"[[Carol]]\"\n---\n"),
	}

	groups := CollectTypeGroups(notes)
	idx := BuildNameIndex(notes)
	got := InferSchema(groups["project"], idx)

	var owner *InferredProperty
	for i := range got {
		if got[i].Name == "owner" {
			owner = &got[i]
		}
	}
	if owner == nil {
		t.Fatalf("no `owner` property inferred for type project; got %+v", got)
	}
	if owner.Type != records.TypeRelation {
		t.Fatalf("owner type = %q, want relation", owner.Type)
	}
	if owner.To != "contact" {
		t.Errorf("owner to = %q, want contact — every link resolves to a contact note", owner.To)
	}
}
