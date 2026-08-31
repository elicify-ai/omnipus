// Omnipus — one Base filter EXPRESSION at a time: what this importer
// recognises, and what it refuses by name rather than approximating.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"reflect"
	"testing"
)

// TestParseLeaf_TranslatesTheShapesItClaims walks the closed set of
// expression shapes the importer says it supports. Each expectation is
// written from what the Obsidian expression MEANS, not from what leaf.go
// happens to produce.
func TestParseLeaf_TranslatesTheShapesItClaims(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want parsedLeaf
	}{
		{
			name: "a type equality is view-type evidence, never a filter clause",
			expr: `type == "decision"`,
			want: parsedLeaf{Kind: leafTypeLiteral, TypeLiteral: "decision"},
		},
		{
			name: "single quotes are a string literal too",
			expr: `type == 'decision'`,
			want: parsedLeaf{Kind: leafTypeLiteral, TypeLiteral: "decision"},
		},
		{
			name: "an ordinary equality",
			expr: `status == "accepted"`,
			want: parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: "status", Op: "eq", Values: []string{"accepted"}}},
		},
		{
			name: "inequality is eq with the negate flag, never a neq operator",
			expr: `status != "accepted"`,
			want: parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: "status", Op: "eq", Values: []string{"accepted"}, Negate: true}},
		},
		{
			name: "ordered comparison carries its own operator",
			expr: `priority >= 3`,
			want: parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: "priority", Op: "gte", Values: []string{"3"}}},
		},
		{
			name: "strictly-less is lt, not lte",
			expr: `priority < 3`,
			want: parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: "priority", Op: "lt", Values: []string{"3"}}},
		},
		{
			name: "contains keeps its own operator",
			expr: `labels.contains("urgent")`,
			want: parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: "labels", Op: "contains", Values: []string{"urgent"}}},
		},
		{
			name: "a bare property is Obsidian's truthy test and is MARKED as one",
			expr: `archived`,
			want: parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: "archived", Op: "is_absent", Negate: true, Truthy: true}},
		},
		{
			name: "a negated bare property is the falsy test, which is_absent narrows rather than broadens",
			expr: `!archived`,
			want: parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: "archived", Op: "is_absent"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLeaf(tc.expr)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseLeaf(%q)\n got: %+v\nwant: %+v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestParseLeaf_RefusesWhatItCannotTranslate is the FR-105 half of this
// file. Every expression here would, if guessed at, change which rows come
// back. Each must land as UNTRANSLATABLE so it becomes a named loss in a
// filter position and disables its view.
func TestParseLeaf_RefusesWhatItCannotTranslate(t *testing.T) {
	cases := []struct {
		name string
		expr string
		why  string
	}{
		{"folder membership", `file.inFolder("99-Temp")`, "the standing FR-105 example — dropping it admits every scratch note"},
		{"a file method", `file.hasTag("draft")`, "file.* is a whole grammar this importer does not implement"},
		{"a computed property", `formula.days_to_expiry <= 14`, "a formula property no schema declares; matching it as an ordinary comparison would manufacture a clause against a property that does not exist"},
		{"a date function", `due < today()`, "today() is evaluated at query time, not at import time"},
		{"a date constructor", `date(due).year == 2026`, "date(...) accessors are not in this importer's vocabulary"},
		{"now()", `updated > now()`, "same reason as today()"},
		{"an isType call", `owner.isType("person")`, "a method call, not a comparison"},
		{"a length accessor", `labels.length > 2`, "a computed cardinality"},
		{"empty-string comparison", `venture != ""`, "Obsidian's undefined-vs-empty-string semantics are not documented well enough to map onto is_absent without changing the row set"},
		{"empty-string equality", `venture == ""`, "same"},
		{"excluding a type", `type != "decision"`, "our filter surface has no set difference over the discriminator itself"},
		{"a comparison on the discriminator", `type >= "decision"`, "the discriminator is not an ordered domain"},
		{"a bare discriminator", `type`, "the discriminator is never a filter clause"},
		{"a negated bare discriminator", `!type`, "same"},
		{"contains on the discriminator", `type.contains("dec")`, "same"},
		{"contains with a non-literal argument", `labels.contains(other)`, "the argument is an expression, not a string literal"},
		{"contains with an empty literal", `labels.contains("")`, "an empty membership test has no defined meaning here"},
		{"a call on the right-hand side", `owner == link("Ada")`, "an unrecognised call in operand position is refused rather than mis-parsed"},
		{"an empty expression", `   `, "nothing to translate"},
		{"a bare boolean literal", `true`, "not a property reference"},
		{"a parenthesised group", `(a == "1" || b == "2")`, "a disjunction, which the flat AND model cannot hold"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLeaf(tc.expr)
			if got.Kind != leafUntranslatable {
				t.Errorf("parseLeaf(%q) = %+v, want UNTRANSLATABLE — %s", tc.expr, got, tc.why)
			}
		})
	}
}

// TestUnquoteLiteral_ReportsQuotedAndEmpty pins the three answers the
// callers actually branch on.
func TestUnquoteLiteral_ReportsQuotedAndEmpty(t *testing.T) {
	cases := []struct {
		raw         string
		wantValue   string
		wantQuoted  bool
		wantIsEmpty bool
	}{
		{`"accepted"`, "accepted", true, false},
		{`'accepted'`, "accepted", true, false},
		{`""`, "", true, true},
		{`3`, "3", false, false},
		{`true`, "true", false, false},
		{`  "spaced"  `, "spaced", true, false},
		{``, "", false, true},
	}
	for _, tc := range cases {
		v, q, e := unquoteLiteral(tc.raw)
		if v != tc.wantValue || q != tc.wantQuoted || e != tc.wantIsEmpty {
			t.Errorf("unquoteLiteral(%q) = (%q, %v, %v), want (%q, %v, %v)", tc.raw, v, q, e, tc.wantValue, tc.wantQuoted, tc.wantIsEmpty)
		}
	}
}
