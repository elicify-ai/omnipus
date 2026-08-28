// Omnipus — tests for the cross-field rules a property declaration must satisfy,
// and for building a Property without the schema loader.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"strings"
	"testing"
)

// minimalDeclFor returns the smallest declaration body that makes each property
// type valid, so a test can add ONE bad field to it and know that field is the
// only reason the declaration fails.
func minimalDeclFor(t PropertyType) string {
	switch t {
	case TypeRelation:
		return "type: relation, to: person"
	case TypeEnum:
		return "type: enum, values: [a, b]"
	default:
		return "type: " + string(t)
	}
}

// TestSchema_ValuesAreRejectedOnEveryNonEnumType covers the whole closed set of
// FR-004 types, not one reported instance.
//
// The rule was written as the `default:` arm of a type switch, so any type with
// a case of its own skipped it. `relation` had one, so
// `{type: relation, to: person, values: [a, b]}` was ACCEPTED and its values
// silently discarded — while the identical `person` declaration was correctly
// refused. Iterating PropertyTypes means the next type to gain a case cannot
// reopen the hole.
func TestSchema_ValuesAreRejectedOnEveryNonEnumType(t *testing.T) {
	for _, pt := range PropertyTypes {
		if pt == TypeEnum {
			continue // `values` is what an enum IS
		}
		t.Run(string(pt), func(t *testing.T) {
			decl := minimalDeclFor(pt)

			// Positive control: the same declaration without `values` must
			// load, so a rejection below can only be about `values`.
			clean := fmt.Sprintf("schema_version: 1\ntype: widget\nproperties:\n  p: { %s }\n", decl)
			if _, rej := ParseSchema("widget.yaml", []byte(clean)); rej != nil {
				t.Fatalf("control declaration for %s must load; it was rejected: %s", pt, rej.Reason)
			}

			body := fmt.Sprintf("schema_version: 1\ntype: widget\nproperties:\n  p: { %s, values: [a, b] }\n", decl)
			sc, rej := ParseSchema("widget.yaml", []byte(body))
			if rej == nil {
				p, _ := sc.Property("p")
				t.Fatalf("`values` on a %s must be refused, not silently discarded; the schema loaded with %d values kept", pt, len(p.Values))
			}
			if rej.Code != RejectBadProperty {
				t.Fatalf("expected %q, got %q (%s)", RejectBadProperty, rej.Code, rej.Reason)
			}
			if !strings.Contains(rej.Reason, "`values` is only meaningful on an enum") {
				t.Fatalf("the rejection must say WHY, in the same words as every other type; got %q", rej.Reason)
			}
			if !strings.Contains(rej.Reason, string(pt)) {
				t.Fatalf("the rejection must name the type it is talking about; got %q", rej.Reason)
			}
		})
	}
}

// TestSchema_LoaderAndNewPropertyAgree pins that the two ways to build a
// Property enforce the same rules. They share finalize precisely so they cannot
// drift; this is what would notice if one of them stopped calling it.
func TestSchema_LoaderAndNewPropertyAgree(t *testing.T) {
	cases := []struct {
		name string
		decl Property
		body string // the equivalent YAML declaration
		want string // substring both refusals must contain
	}{
		{
			name: "a relation must declare its target",
			decl: Property{Name: "owner", Type: TypeRelation},
			body: "type: relation",
			want: "must declare its target record type",
		},
		{
			name: "an enum must declare its values",
			decl: Property{Name: "status", Type: TypeEnum},
			body: "type: enum",
			want: "an enum must declare its `values`",
		},
		{
			name: "unit is number-only",
			decl: Property{Name: "when", Type: TypeDate, Unit: "minutes"},
			body: "type: date, unit: minutes",
			want: "`unit` is only meaningful on an integer or a decimal",
		},
		{
			name: "to is relation/person-only",
			decl: Property{Name: "note", Type: TypeText, To: "person"},
			body: "type: text, to: person",
			want: "`to`/`inverse` are only meaningful on a relation or person",
		},
		{
			name: "values are enum-only",
			decl: Property{Name: "owner", Type: TypeRelation, To: "person", Values: []EnumValue{{Name: "a"}}},
			body: "type: relation, to: person, values: [a]",
			want: "`values` is only meaningful on an enum",
		},
		{
			name: "an enum value is never declared twice",
			decl: Property{Name: "status", Type: TypeEnum, Values: []EnumValue{{Name: "a"}, {Name: "a"}}},
			body: "type: enum, values: [a, a]",
			want: "is declared twice",
		},
		{
			name: "an enum value is never empty",
			decl: Property{Name: "status", Type: TypeEnum, Values: []EnumValue{{Name: "a"}, {Name: "  "}}},
			body: `type: enum, values: [a, "  "]`,
			want: "is empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProperty(tc.decl)
			if err == nil {
				t.Fatalf("NewProperty accepted a declaration the loader refuses")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewProperty: expected a refusal containing %q, got %q", tc.want, err)
			}

			yaml := fmt.Sprintf("schema_version: 1\ntype: widget\nproperties:\n  %s: { %s }\n", tc.decl.Name, tc.body)
			_, rej := ParseSchema("widget.yaml", []byte(yaml))
			if rej == nil {
				t.Fatalf("the loader accepted a declaration NewProperty refuses — the two have drifted apart")
			}
			if !strings.Contains(rej.Reason, tc.want) {
				t.Fatalf("loader: expected a refusal containing %q, got %q", tc.want, rej.Reason)
			}
		})
	}
}

// TestSchema_NewPropertyIndexesEnumValues asserts the constructor produces a
// property indistinguishable from a parsed one.
func TestSchema_NewPropertyIndexesEnumValues(t *testing.T) {
	built, err := NewProperty(Property{
		Name:       "status",
		Type:       TypeEnum,
		RecordType: "widget",
		Values:     []EnumValue{{Name: "todo"}, {Name: "doing", Group: "open"}, {Name: "done"}},
	})
	if err != nil {
		t.Fatalf("NewProperty: %v", err)
	}

	set := loadSet(t, map[string]string{"widget.yaml": "schema_version: 1\ntype: widget\nproperties:\n  status: { type: enum, values: [todo, {name: doing, group: open}, done] }\n"})
	sc, _ := set.Get("widget")
	parsed, _ := sc.Property("status")

	for i, name := range []string{"todo", "doing", "done"} {
		gotVal, ok := built.ResolveEnum(name)
		if !ok {
			t.Fatalf("a value the property itself declares must be in its set; %q was not", name)
		}
		wantVal, ok := parsed.ResolveEnum(name)
		if !ok {
			t.Fatalf("the PARSED property must resolve %q too", name)
		}
		if gotVal.Name != name || wantVal.Name != name {
			t.Fatalf("%q must resolve to itself; built said %q, parsed said %q", name, gotVal.Name, wantVal.Name)
		}
		if built.Values[i].Name != name {
			t.Fatalf("declared order must be preserved for reporting; Values[%d].Name = %q, want %q", i, built.Values[i].Name, name)
		}
	}
	if built.Values[1].Group != "open" {
		t.Fatalf("the constructor must not lose an enum value's lifecycle group; got %q", built.Values[1].Group)
	}
	// REVERSED by ruling R-D. This assertion previously required `Done` NOT to
	// resolve, on the ground that enum matching was exact-case. It is now
	// case-INSENSITIVE (FR-011a) and must resolve to the DECLARED spelling —
	// resolving `Done` to `done` collapses two spellings into one value, which
	// is not what D4 forbids.
	resolved, ok := built.ResolveEnum("Done")
	if !ok {
		t.Fatal("FR-011a: enum matching ignores case; `Done` must resolve to the declared `done`")
	}
	if resolved.Name != "done" {
		t.Fatalf("resolution must yield the DECLARED spelling, not the caller's; got %q", resolved.Name)
	}
	if built.PermittedValues()[0] != "todo" || len(built.PermittedValues()) != 3 {
		t.Fatalf("PermittedValues must list the declared set in order; got %v", built.PermittedValues())
	}

	// The caller's slice must not be aliased — mutating it afterwards cannot
	// desynchronise the property from its own index.
	src := []EnumValue{{Name: "x"}, {Name: "y"}}
	p, err := NewProperty(Property{Name: "s", Type: TypeEnum, Values: src})
	if err != nil {
		t.Fatalf("NewProperty: %v", err)
	}
	src[0].Name = "mutated"
	if p.Values[0].Name != "x" {
		t.Fatalf("NewProperty must copy the caller's values; the property now says %q", p.Values[0].Name)
	}
	if _, ok := p.ResolveEnum("x"); !ok {
		t.Fatalf("the index must still resolve %q after the caller mutated their own slice", "x")
	}
}
