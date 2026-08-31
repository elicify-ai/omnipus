// Omnipus — the suite for WIDENING AN INFERRED ENUM FROM A `.base` FILE.
//
// An inferred enum's closed set is the DISTINCT VALUES OBSERVED, which makes
// it a statement about what the vault currently holds and not about what the
// operator considers legal. When a base filters on a value no note carries
// yet, those two readings come apart: `status == "doing"` against an enum
// inferred as (blocked, done, open, todo) is refused, the clause is dropped,
// and FR-105 disables the whole view.
//
// The rule under test admits the literal to the closed set and REPORTS it.
// The tests below pin both halves — the widening, and the account of it —
// plus the boundary the widening must not cross.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// taskLikeSchema is the founder's `task` as this run infers it: `status` is
// an enum of the four values his notes actually carry, and Tasks.base then
// filters a fifth ("doing") that none of them do.
func taskLikeSchema() map[string][]InferredProperty {
	return map[string][]InferredProperty{
		"task": {
			{Name: "status", Type: records.TypeEnum, EnumValues: []string{"blocked", "done", "open", "todo"}},
			{Name: "labels", Type: records.TypeEnum, Many: true, EnumValues: []string{"legal", "urgent"}},
			{Name: "priority", Type: records.TypeInteger},
			{Name: "title", Type: records.TypeText},
		},
	}
}

// widenFrom parses one `.base` source, runs the widening over a fresh
// task-like schema, and returns both the mutated schema and the account.
func widenFrom(t *testing.T, src string) (map[string][]InferredProperty, []EnumWidening) {
	t.Helper()
	pb, err := ParseBaseFile([]byte(src))
	if err != nil {
		t.Fatalf("fixture base does not parse: %v", err)
	}
	inferred := taskLikeSchema()
	w := WidenEnumsFromBases(inferred, []string{"Tasks.base"}, map[string]*ParsedBase{"Tasks.base": pb})
	return inferred, w
}

// enumOf pulls one property's declared values out of a schema map.
func enumOf(t *testing.T, inferred map[string][]InferredProperty, recordType, prop string) []string {
	t.Helper()
	for _, p := range inferred[recordType] {
		if p.Name == prop {
			return p.EnumValues
		}
	}
	t.Fatalf("%s.%s is not declared", recordType, prop)
	return nil
}

const baseDoingNow = `
filters:
  and:
    - type == "task"
views:
  - type: table
    name: Doing now
    filters:
      and:
        - status == "doing"
`

// TestEnumWidening_ABaseLiteralNoNoteCarriesJoinsTheClosedSet is the case
// this rule exists for.
func TestEnumWidening_ABaseLiteralNoNoteCarriesJoinsTheClosedSet(t *testing.T) {
	inferred, widenings := widenFrom(t, baseDoingNow)

	vals := enumOf(t, inferred, "task", "status")
	if !containsString(vals, "doing") {
		t.Fatalf("status values are %v — the base filters on \"doing\", so the operator has said it is a legal value even though no note carries it yet", vals)
	}
	for _, observed := range []string{"blocked", "done", "open", "todo"} {
		if !containsString(vals, observed) {
			t.Errorf("widening dropped the observed value %q (values now %v) — it may only ADD", observed, vals)
		}
	}

	if len(widenings) != 1 {
		t.Fatalf("got %d widening record(s), want exactly 1 — a closed set that grew silently is the failure mode this rule is most likely to produce", len(widenings))
	}
	w := widenings[0]
	if w.RecordType != "task" || w.Property != "status" {
		t.Errorf("widening record names %s.%s, want task.status", w.RecordType, w.Property)
	}
	if len(w.Added) != 1 || w.Added[0] != "doing" {
		t.Errorf("Added=%v, want exactly [doing]", w.Added)
	}
	if !containsString(w.Bases, "Tasks.base") {
		t.Errorf("Bases=%v — the operator has to be told WHICH file asked for this", w.Bases)
	}
	lines := w.ReportLines()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "doing") || !strings.Contains(joined, "Tasks.base") {
		t.Errorf("the account does not name the value and the file it came from:\n%s", joined)
	}
	if !strings.Contains(joined, "no note") {
		t.Errorf("the account does not say the value was never observed, which is the whole thing the operator needs to check:\n%s", joined)
	}
}

// TestEnumWidening_NamesTheNearestDeclaredValueSoATypoIsVisible.
//
// The genuinely ambiguous case: the operator may be filtering for a state
// that has not occurred yet, or may have mistyped one that has. The importer
// cannot tell them apart and does not pretend to — it reproduces the filter
// faithfully (see the negation test below for why that is safe) and puts the
// nearest declared value in front of the operator so a typo is a one-glance
// check rather than a mystery about why a view matches nothing.
func TestEnumWidening_NamesTheNearestDeclaredValueSoATypoIsVisible(t *testing.T) {
	_, widenings := widenFrom(t, `
filters:
  and:
    - type == "task"
views:
  - type: table
    name: Finished
    filters:
      and:
        - status == "doen"
`)
	if len(widenings) != 1 {
		t.Fatalf("got %d widening record(s), want 1", len(widenings))
	}
	// The assertion has to be the TYPO PROMPT itself, not the string "done".
	// "done" is also printed in the observed-values list on the first line, so
	// an earlier version of this test passed with the entire near-miss check
	// deleted — it was reading the wrong sentence.
	var hint string
	for _, l := range widenings[0].ReportLines() {
		if strings.Contains(l, "CHECK FOR A TYPO") {
			hint = l
		}
	}
	if hint == "" {
		t.Fatalf("no CHECK FOR A TYPO line — `doen` is one transposition from the declared `done`, and a typo that widens a closed set makes a view match nothing forever and look fine:\n%s",
			strings.Join(widenings[0].ReportLines(), "\n"))
	}
	if !strings.Contains(hint, "doen") || !strings.Contains(hint, "done") {
		t.Errorf("the typo prompt does not put both spellings in front of the operator:\n%s", hint)
	}
}

// TestEnumWidening_ANovelValueGetsNoTypoPrompt is the other half: the prompt
// must not fire on every widening, or it stops meaning anything.
func TestEnumWidening_ANovelValueGetsNoTypoPrompt(t *testing.T) {
	_, widenings := widenFrom(t, baseDoingNow)
	if len(widenings) != 1 {
		t.Fatalf("got %d widening record(s), want 1", len(widenings))
	}
	for _, l := range widenings[0].ReportLines() {
		if strings.Contains(l, "CHECK FOR A TYPO") {
			t.Errorf("`doing` is two edits from `done` and three from `todo`, so it reads as a real state, not a slip — a prompt on every widening is a prompt nobody reads:\n%s", l)
		}
	}
}

// TestEnumWidening_AValueAlreadyDeclaredIsNotReWidened, case-folded, because
// canonicalEnumValue matches with records.FoldKey and a rule that used a
// different comparison would add a duplicate spelling of a value that is
// already there.
func TestEnumWidening_AValueAlreadyDeclaredIsNotReWidened(t *testing.T) {
	inferred, widenings := widenFrom(t, `
filters:
  and:
    - type == "task"
views:
  - type: table
    name: Done
    filters:
      and:
        - status == "DONE"
`)
	if len(widenings) != 0 {
		t.Errorf("widened %v — `DONE` already resolves to the declared `done` through records.FoldKey, which is the same comparison the translator makes", widenings)
	}
	if vals := enumOf(t, inferred, "task", "status"); len(vals) != 4 {
		t.Errorf("status values are %v, want the original four — a second spelling of a declared value is not a new value", vals)
	}
}

// TestEnumWidening_OnlyAnEnumIsWidened. A literal compared against a text,
// integer or date property says nothing about a closed set, because those
// types have none.
func TestEnumWidening_OnlyAnEnumIsWidened(t *testing.T) {
	inferred, widenings := widenFrom(t, `
filters:
  and:
    - type == "task"
views:
  - type: table
    name: Titled
    filters:
      and:
        - title == "anything at all"
        - priority == "7"
`)
	if len(widenings) != 0 {
		t.Errorf("widened %v from a comparison against a non-enum property", widenings)
	}
	for _, p := range inferred["task"] {
		if p.Name != "status" && p.Name != "labels" && len(p.EnumValues) > 0 {
			t.Errorf("%q was given enum values %v — it is declared %s and has no closed set to widen", p.Name, p.EnumValues, p.Type)
		}
	}
}

// TestEnumWidening_AnOrderingComparisonIsNotAMembershipClaim. `status >
// "doing"` is not the operator saying `doing` is a legal status; it is a
// comparison this importer refuses on other grounds. Admitting the literal
// would widen a closed set on the strength of a clause that is lost anyway.
func TestEnumWidening_AnOrderingComparisonIsNotAMembershipClaim(t *testing.T) {
	_, widenings := widenFrom(t, `
filters:
  and:
    - type == "task"
views:
  - type: table
    name: After doing
    filters:
      and:
        - status > "doing"
`)
	if len(widenings) != 0 {
		t.Errorf("widened %v from an ORDERING comparison — only equality and membership assert that a value belongs to the set", widenings)
	}
}

// TestEnumWidening_StopsAtTheCeilingThatMakesAPropertyAnEnumAtAll.
//
// enumMaxDistinct is the count above which classifyProperty declines to call
// a property an enum at all. A base naming enough unknown literals to push
// the set past it is not evidence for a wider enum — it is evidence that the
// inference was wrong about the property, and manufacturing a 20-value closed
// set would bury that rather than report it.
func TestEnumWidening_StopsAtTheCeilingThatMakesAPropertyAnEnumAtAll(t *testing.T) {
	var b strings.Builder
	b.WriteString("filters:\n  and:\n    - type == \"task\"\nviews:\n  - type: table\n    name: Many\n    filters:\n      or:\n")
	for i := 0; i < enumMaxDistinct+5; i++ {
		b.WriteString("        - status == \"novel-state-")
		b.WriteString(string(rune('a' + i)))
		b.WriteString("\"\n")
	}
	inferred, widenings := widenFrom(t, b.String())

	vals := enumOf(t, inferred, "task", "status")
	if len(vals) > enumMaxDistinct {
		t.Errorf("status now declares %d values (%v) — past enumMaxDistinct=%d, which is the point at which this package's own inference stops believing a property is an enum", len(vals), vals, enumMaxDistinct)
	}
	if len(vals) != 4 {
		t.Errorf("status values are %v, want the original four untouched — a partial widening picks an arbitrary subset of the operator's literals", vals)
	}
	if len(widenings) != 1 {
		t.Fatalf("got %d widening record(s), want 1 — a REFUSAL to widen still has to be reported, or the operator learns nothing about why the view is disabled", len(widenings))
	}
	if !widenings[0].Refused {
		t.Error("the record does not mark itself as a refusal, so a reader cannot tell it apart from a widening that happened")
	}
	joined := strings.Join(widenings[0].ReportLines(), "\n")
	if !strings.Contains(joined, "NOT widened") {
		t.Errorf("the account of a refusal does not say the set was left alone:\n%s", joined)
	}
}

// ---------------------------------------------------------------------------
// THE NEGATION TEST — the one that decides whether this rule is safe at all.
//
// A rewrite that NARROWS a leaf BROADENS the view when the leaf sits under a
// `not:`, because `not:` inverts and a subset's complement is a superset.
// That is how a proof about a leaf gets the view wrong, and it is why this
// rule had to be argued at the tree level rather than the clause level.
//
// Widening survives it for a reason that is not available to a narrowing
// rewrite: the translated clause is EQUIVALENT to Obsidian's, not a subset of
// it. `status == "doing"` in Obsidian matches the records whose status is
// `doing`; the widened enum emits `status = doing`, which matches the same
// records. Equivalence is preserved by complement — if A = B then ¬A = ¬B —
// so the clause is faithful in both polarities.
//
// The mechanical fact that argument rests on is asserted here: the emitted
// node is an EQUALITY against the literal the operator wrote, nested under a
// `not`, with no loss and no widening operator (no LIKE, no IS NOT NULL)
// substituted anywhere in the tree.
// ---------------------------------------------------------------------------

func TestEnumWidening_UnderANegationTheClauseIsEquivalentAndTheViewIsEnabled(t *testing.T) {
	src := `
filters:
  and:
    - type == "task"
views:
  - type: table
    name: Not doing
    filters:
      not:
        - status == "doing"
`
	pb, err := ParseBaseFile([]byte(src))
	if err != nil {
		t.Fatalf("fixture base does not parse: %v", err)
	}
	inferred := taskLikeSchema()
	if got := WidenEnumsFromBases(inferred, []string{"Tasks.base"}, map[string]*ParsedBase{"Tasks.base": pb}); len(got) != 1 {
		t.Fatalf("a literal under a `not:` was not widened (%d records) — the operator asserted it is a legal value there exactly as they would have outside the negation", len(got))
	}

	outcome, produced := TranslateBase(pb, "Tasks.base", NewSchemaIndex(inferred), NewSlugRegistry())
	if len(outcome.Views) != 1 {
		t.Fatalf("got %d view outcomes, want 1", len(outcome.Views))
	}
	v := outcome.Views[0]
	if v.Disabled {
		t.Errorf("the view is DISABLED after widening: %v", v.Losses)
	}
	if len(v.Losses) != 0 {
		t.Errorf("the view carries losses after widening: %v", v.Losses)
	}
	if len(produced) != 1 {
		t.Fatalf("got %d produced view(s), want 1", len(produced))
	}

	// The emitted filter must be `not` over a bare equality against the exact
	// literal. Anything looser under a negation is broadening at the view.
	body := string(produced[0].Bytes)
	for _, widening := range []string{"LIKE", "is_not_null", "IS NOT NULL", "is_null"} {
		if strings.Contains(body, widening) {
			t.Errorf("the produced view uses %q under a `not:` — a widened operator inside a negation returns MORE rows than the original (FR-105):\n%s", widening, body)
		}
	}
	// Assert the emitted STRUCTURE, not three loose substrings: a `not:` whose
	// single child is a bare equality against the literal. `strings.Contains(
	// body, "not")` would match any word containing those letters, and
	// `Contains(body, "=")` matches every YAML line with an operator on it.
	want := "filter:\n    not:\n        property: status\n        op: " + string(generated.Equal) + "\n        value: doing\n"
	if !strings.Contains(body, want) {
		t.Errorf("the produced view is not a `not:` over a bare equality against the operator's own literal.\nwant to find:\n%s\ngot:\n%s", want, body)
	}
}

// TestEnumWidening_MembershipOnAListAlsoAsserts: `labels.contains("audit")`
// on a MANY enum is element membership — the same claim `==` makes on a
// scalar, and the same closed set the translator checks the literal against.
func TestEnumWidening_MembershipOnAListAlsoAsserts(t *testing.T) {
	inferred, widenings := widenFrom(t, `
filters:
  and:
    - type == "task"
views:
  - type: table
    name: Audit
    filters:
      and:
        - labels.contains("audit")
`)
	if len(widenings) != 1 {
		t.Fatalf("got %d widening record(s), want 1 — buildV2LeafNode checks a `.contains` literal against the very same closed set it checks an `==` literal against", len(widenings))
	}
	if vals := enumOf(t, inferred, "task", "labels"); !containsString(vals, "audit") {
		t.Errorf("labels values are %v, want `audit` among them", vals)
	}
}

// TestEnumWidening_ContainsOnAScalarEnumAssertsNothing: on a property that is
// NOT many, `.contains` is refused by buildV2LeafNode on its own terms and
// the literal is never tested against the closed set — so widening the set
// would change the schema an operator reads without changing one row.
func TestEnumWidening_ContainsOnAScalarEnumAssertsNothing(t *testing.T) {
	_, widenings := widenFrom(t, `
filters:
  and:
    - type == "task"
views:
  - type: table
    name: Scalar contains
    filters:
      and:
        - status.contains("doing")
`)
	if len(widenings) != 0 {
		t.Errorf("widened %v from a `.contains` on a SCALAR enum — that clause is refused whatever the closed set holds, so admitting the value buys no row and costs a schema the operator did not ask for", widenings)
	}
}

// TestEnumWidening_AnUntypedViewWidensNothing. With no record type resolved
// there is no schema to widen and no property to attribute the literal to.
func TestEnumWidening_AnUntypedViewWidensNothing(t *testing.T) {
	_, widenings := widenFrom(t, `
filters:
  and:
    - file.inFolder("Tasks")
views:
  - type: table
    name: Folder scoped
    filters:
      and:
        - status == "doing"
`)
	if len(widenings) != 0 {
		t.Errorf("widened %v from an UNTYPED view — the literal cannot be attributed to any record type's property", widenings)
	}
}

func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
