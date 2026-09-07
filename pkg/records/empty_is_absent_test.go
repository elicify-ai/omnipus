// Omnipus — FR-007a / ADR-068 D24.6 ruling 1: an empty string is ABSENT for
// every non-text type.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "testing"

// nonTextTypeFixture is one declared type and a note that writes `""` for it.
//
// The list is EVERY non-text type, enumerated from FR-004's set rather than
// sampled, because FR-007a is stated over all of them and a rule that held for
// `date` and not for `enum` would leave the 4 enum findings in place.
func nonTextTypeFixtures(t *testing.T) []*Property {
	t.Helper()
	mk := func(decl Property) *Property {
		t.Helper()
		decl.RecordType = "task"
		p, err := NewProperty(decl)
		if err != nil {
			t.Fatalf("NewProperty(%s): %v", decl.Type, err)
		}
		return p
	}
	return []*Property{
		mk(Property{Name: "completed", Type: TypeDate}),
		mk(Property{Name: "count", Type: TypeInteger}),
		mk(Property{Name: "amount", Type: TypeDecimal}),
		mk(Property{Name: "stage", Type: TypeEnum, Values: []EnumValue{{Name: "open"}, {Name: "won"}}}),
		mk(Property{Name: "company", Type: TypeRelation, To: "company"}),
		mk(Property{Name: "owner", Type: TypePerson}),
		mk(Property{Name: "meditated", Type: TypeCheckbox}),
	}
}

// TestEmptyString_IsAbsentForNonTextTypesAndAValueForText is FR-007a's SCALAR
// half, over every type the rule names.
//
// FR-007a: "A value that is `""` (after trimming) on a `date`, `enum`,
// `integer`, `decimal`, `relation`, `person` or `checkbox` property resolves to
// StateAbsent — `IS NULL` matches it, FR-008 re-includes it under negation, and
// NO finding is raised. For `text`, `""` remains a PRESENT empty string."
//
// The founder's vault writes `completed: ""` to mean unset. Before this rule it
// was read as a value that FAILED to be a date, and 52 real findings (46
// not-a-date, 4 enum, 2 wikilink) came from reporting at length on a value that
// visibly is not there.
func TestEmptyString_IsAbsentForNonTextTypesAndAValueForText(t *testing.T) {
	for _, p := range nonTextTypeFixtures(t) {
		t.Run(string(p.Type), func(t *testing.T) {
			rec := ParseRecord("task.md", []byte("---\ntype: task\n"+p.Name+": \"\"\n---\n"))
			pv := ResolveProperty(rec, p)

			if pv.State != StateAbsent {
				t.Fatalf(`FR-007a: %s: "" must resolve to ABSENT, got %s with values %v`, p.Type, pv.State, pv.Values)
			}
			// The half that removes the 52 findings. A rule that reached the
			// absent STATE while still reporting would have fixed nothing.
			if len(pv.Findings) != 0 {
				t.Fatalf("FR-007a: %s: an empty string must raise NO finding; got %v", p.Type, pv.Findings)
			}
			if len(pv.Values) != 0 {
				t.Fatalf("FR-007a: %s: absence carries no value; got %v", p.Type, pv.Values)
			}

			c := Comparator{}
			absentRight := PropertyValue{Property: p, State: StateAbsent}
			if ok, _ := c.Evaluate(OpIsNull, pv, absentRight); !ok {
				t.Errorf(`FR-007a: %s: "" must match IS NULL`, p.Type)
			}
			if ok, _ := c.Evaluate(OpIsNotNull, pv, absentRight); ok {
				t.Errorf(`FR-007a: %s: "" must NOT match IS NOT NULL`, p.Type)
			}
		})
	}

	t.Run("whitespace only is the same intent as empty", func(t *testing.T) {
		// FR-007a says "(after trimming)". `completed: "   "` is the same
		// operator intent as `completed: ""`.
		p := nonTextTypeFixtures(t)[0] // date
		rec := ParseRecord("task.md", []byte("---\ntype: task\ncompleted: \"   \"\n---\n"))
		if pv := ResolveProperty(rec, p); pv.State != StateAbsent {
			t.Fatalf(`FR-007a: a whitespace-only value must be ABSENT, got %s`, pv.State)
		}
	})

	// THE NEGATIVE, AND IT IS THE LOAD-BEARING HALF. The rule is scoped to
	// non-text types. A predicate that over-reached would silently delete
	// legitimate empty strings out of every text property in the vault, and
	// `summary IS NULL` would start matching notes that HAVE a summary — a
	// worse defect than the 52 findings the rule removes, and a quieter one.
	t.Run("for text an empty string stays a PRESENT value", func(t *testing.T) {
		p, err := NewProperty(Property{Name: "summary", Type: TypeText, RecordType: "task"})
		if err != nil {
			t.Fatalf("NewProperty(text): %v", err)
		}
		rec := ParseRecord("task.md", []byte("---\ntype: task\nsummary: \"\"\n---\n"))
		pv := ResolveProperty(rec, p)

		if pv.State != StatePresent {
			t.Fatalf(`FR-007a/R-3: text "" is a PRESENT empty string, not absence; got %s`, pv.State)
		}
		if len(pv.Values) != 1 || pv.Values[0].Text != "" {
			t.Fatalf("text \"\" must carry exactly one empty value; got %v", pv.Values)
		}
		c := Comparator{}
		if ok, _ := c.Evaluate(OpIsNull, pv, PropertyValue{Property: p, State: StateAbsent}); ok {
			t.Error(`R-3: a text property holding "" must NOT match IS NULL — a note that wrote an empty summary said something different from a note that omitted it`)
		}
		// Whitespace is not trimmed on text either: it is prose, and D3 says
		// text is never validated.
		rec2 := ParseRecord("task.md", []byte("---\ntype: task\nsummary: \"   \"\n---\n"))
		if pv2 := ResolveProperty(rec2, p); pv2.State != StatePresent {
			t.Fatalf("text whitespace is a value, not absence; got %s", pv2.State)
		}
	})

	t.Run("FR-008: a negative filter re-includes an empty-string record", func(t *testing.T) {
		// The consequence the ruling names: Obsidian's idiomatic `field != ""`
		// ("is set") translates mechanically to `IS NOT NULL`, and a record
		// whose value is absent is re-included under negation rather than
		// dropped the way SQL's three-valued NOT drops it.
		p := nonTextTypeFixtures(t)[3] // enum
		rec := ParseRecord("task.md", []byte("---\ntype: task\nstage: \"\"\n---\n"))
		pv := ResolveProperty(rec, p)

		sc := &Schema{
			SchemaVersion: SupportedSchemaVersion,
			Type:          "task",
			Properties:    map[string]*Property{p.Name: p},
			PropertyOrder: []string{p.Name},
		}
		f := Filter{Property: p.Name, Op: OpEqual, Literal: "won", LiteralGiven: true, Negate: true}
		res, err := f.MatchValue(Comparator{}, sc, pv)
		if err != nil {
			t.Fatalf("MatchValue: %v", err)
		}
		if !res.Matched {
			t.Error("FR-008: a record whose value is absent must be RE-INCLUDED by a negative filter; an empty-string enum is such a record")
		}
	})
}

// TestEmptyString_IsAnAbsentElementInAListProperty is FR-007a's LIST half —
// "in a `many` property, empty elements resolve as absent elements".
//
// IT IS A SEPARATE TEST BECAUSE IT IS A SEPARATE CODE PATH, and it is the one
// more likely to be quietly wrong: an element skipped as absent changes the
// property's ARITY, arity feeds R-1's cross-type rule, and R-1 fails silently
// by design. A test that only covered `completed: ""` at the top level would
// pass with this path fully broken.
func TestEmptyString_IsAnAbsentElementInAListProperty(t *testing.T) {
	mk := func(t *testing.T) *Property {
		t.Helper()
		p, err := NewProperty(Property{
			Name: "stage", Type: TypeEnum, Many: true, RecordType: "task",
			Values: []EnumValue{{Name: "open"}, {Name: "won"}},
		})
		if err != nil {
			t.Fatalf("NewProperty: %v", err)
		}
		return p
	}

	t.Run("an empty element is skipped, with no finding", func(t *testing.T) {
		p := mk(t)
		rec := ParseRecord("task.md", []byte("---\ntype: task\nstage:\n  - open\n  - \"\"\n  - won\n---\n"))
		pv := ResolveProperty(rec, p)

		if pv.State != StatePresent {
			t.Fatalf("a list with an empty element is still a PRESENT list; got %s (findings %v)", pv.State, pv.Findings)
		}
		if len(pv.Findings) != 0 {
			t.Fatalf("FR-007a: an empty ELEMENT raises no finding; got %v", pv.Findings)
		}
		if len(pv.Values) != 2 {
			t.Fatalf("FR-007a: the empty element resolves as absent and is not carried; want 2 values, got %d (%v)", len(pv.Values), pv.Values)
		}
		if pv.Values[0].Enum.Name != "open" || pv.Values[1].Enum.Name != "won" {
			t.Fatalf("the surviving values are wrong: %v", pv.Values)
		}
		// SourceIndex must still name the operator's own positions: `won` is
		// element 2 in their file even though it is Values[1] here. A finding
		// that reported the Values index would send them to the wrong line.
		if got := pv.SourcePosition(1); got != 2 {
			t.Errorf("SourcePosition(1) = %d, want 2 — the file's own element position", got)
		}
	})

	t.Run("an empty ELEMENT is NOT the same as an empty ENTRY", func(t *testing.T) {
		// `- ""` on a non-text property is the founder's convention for an
		// unset element and raises nothing. `- ` with no value at all is a
		// shape fault this package has always reported. Merging the two
		// branches would either resurrect the 52 findings or silence a real
		// one, so they are asserted apart.
		p := mk(t)
		rec := ParseRecord("task.md", []byte("---\ntype: task\nstage:\n  - open\n  -\n---\n"))
		pv := ResolveProperty(rec, p)
		if pv.State != StateNonConforming {
			t.Fatalf("`- ` (an entry with no value) must stay a shape fault; got %s", pv.State)
		}
		if len(pv.Findings) == 0 {
			t.Fatal("`- ` must still be REPORTED — FR-007a did not silence it")
		}
		if pv.Findings[0].Code != FindingWrongShape {
			t.Errorf("code = %q, want %q", pv.Findings[0].Code, FindingWrongShape)
		}
	})

	t.Run("a list whose every element is empty is a present EMPTY list", func(t *testing.T) {
		// R-3: an empty list is a VALUE, not absence — the operator wrote a
		// list. This is the arity consequence the ruling has to settle.
		p := mk(t)
		rec := ParseRecord("task.md", []byte("---\ntype: task\nstage:\n  - \"\"\n  - \"\"\n---\n"))
		pv := ResolveProperty(rec, p)
		if pv.State != StatePresent {
			t.Fatalf("R-3: a list of empty elements is a present empty list, not absence; got %s", pv.State)
		}
		if len(pv.Values) != 0 {
			t.Fatalf("want zero surviving values, got %v", pv.Values)
		}
		if ok, _ := (Comparator{}).Evaluate(OpIsNull, pv, PropertyValue{Property: p, State: StateAbsent}); ok {
			t.Error("R-3: a present empty list must NOT match IS NULL — the operator wrote a list")
		}
	})

	t.Run("an empty scalar on a MANY property is absence, not an arity fault", func(t *testing.T) {
		// The ordering decision inside ResolveProperty: FR-007a is settled
		// BEFORE the arity check. `stage: ""` on a list property is the
		// operator saying "unset", and an arity finding there would send them
		// to put brackets around nothing.
		p := mk(t)
		rec := ParseRecord("task.md", []byte("---\ntype: task\nstage: \"\"\n---\n"))
		pv := ResolveProperty(rec, p)
		if pv.State != StateAbsent {
			t.Fatalf(`FR-007a: "" on a many property is ABSENT, not an arity violation; got %s (findings %v)`, pv.State, pv.Findings)
		}
		if len(pv.Findings) != 0 {
			t.Fatalf("no finding is due; got %v", pv.Findings)
		}
	})
}
