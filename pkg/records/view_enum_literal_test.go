// Omnipus — the ViewDef contract's second half: "a view naming a property OR
// ENUM VALUE that does not exist is REJECTED at write time".
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// The contract sentence has two halves and only the first was enforced. Names
// were checked in every position; LITERALS were checked nowhere, so a view
// filtering `state = "Closed Won"` against an enum declaring
// [draft, shipped, withdrawn] was written, stored and served, and refused only
// when somebody ran it. An enum literal nothing validates is a filter that
// matches nothing — and a filter matching nothing is indistinguishable from a
// genuinely empty result, which is the failure the whole surface exists to
// remove.
//
// The membership oracle here is Property.ResolveEnum, and it has to be: it is
// the SAME oracle value.go::parseEnumValueNode asks at query time. A second
// implementation would agree on `won` and disagree on the Turkish dotted İ.
//
// WHAT IS DELIBERATELY NOT CHECKED, and why each is not a gap:
//
//	LIKE      — filter.go carries a LIKE pattern as TEXT precisely because
//	            `do%` is not a declared value and never will be. Refusing it
//	            here would refuse a legitimate query.
//	IS NULL   — takes no literal at all.
//	untyped   — an untyped view has no schema to check against (FR-018b).
//	non-enum  — a date or integer literal is checked by the parser at query
//	            time against rules that live in value.go; re-deriving them
//	            here would be a second implementation of them.
// ---------------------------------------------------------------------------

// TestView_EnumLiteralIsCheckedAtLoad is the restored half of the contract.
func TestView_EnumLiteralIsCheckedAtLoad(t *testing.T) {
	t.Run("a literal outside the declared set is REJECTED, naming the permitted values", func(t *testing.T) {
		cases := []struct {
			name       string
			body       string
			mustNameds []string
		}{
			{
				name:       "a bare `=` leaf",
				body:       "name: v\ntype: widget\nfilter: {property: state, op: \"=\", value: Closed Won}\n",
				mustNameds: []string{"Closed Won", "state", "draft", "shipped", "withdrawn", "filter"},
			},
			{
				name:       "a `<>` leaf — the complement is just as wrong, and it matches EVERYTHING",
				body:       "name: v\ntype: widget\nfilter: {property: state, op: \"<>\", value: Closed Won}\n",
				mustNameds: []string{"Closed Won", "draft"},
			},
			{
				name:       "one bad member of an `IN` set",
				body:       "name: v\ntype: widget\nfilter: {property: state, op: \"IN\", values: [draft, Closed Won]}\n",
				mustNameds: []string{"Closed Won", "draft"},
			},
			{
				name: "a leaf buried in an any/all tree — the PATH is named, not just the property",
				body: "name: v\ntype: widget\nfilter:\n" +
					"  any:\n" +
					"    - {property: batch, op: \">\", value: \"1\"}\n" +
					"    - all:\n" +
					"        - {property: state, op: \"=\", value: Closed Won}\n",
				mustNameds: []string{"Closed Won", "filter.any[1].all[0]"},
			},
			{
				name: "a leaf under `not` — negation does not re-admit an undeclared value",
				body: "name: v\ntype: widget\nfilter:\n" +
					"  not: {property: state, op: \"=\", value: Closed Won}\n",
				mustNameds: []string{"Closed Won", "filter.not"},
			},
			{
				name:       "an ordering leaf — enum orders lexically, but only over DECLARED values",
				body:       "name: v\ntype: widget\nfilter: {property: state, op: \">\", value: Closed Won}\n",
				mustNameds: []string{"Closed Won", "draft"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				root, schemas := viewFixtureSchemas(t, "")
				root = writeVaultView(t, root, "v.yaml", tc.body)
				set, report, err := LoadViews(root, schemas)
				if err != nil {
					t.Fatalf("LoadViews: %v", err)
				}
				if len(report.Rejections) != 1 {
					t.Fatalf("an undeclared enum literal must reject the view; got %d rejection(s), %d loaded view(s)",
						len(report.Rejections), set.Len())
				}
				rej := report.Rejections[0]
				if rej.Code != RejectViewUnknownEnumValue {
					t.Errorf("code = %q, want %q", rej.Code, RejectViewUnknownEnumValue)
				}
				for _, must := range tc.mustNameds {
					if !strings.Contains(rej.Reason, must) {
						t.Errorf("the rejection must name %q so the fix is one edit; got %q", must, rej.Reason)
					}
				}
				if _, ok := set.Get("v"); ok {
					t.Error("a rejected view must not be served: it was stored and would answer queries")
				}
			})
		}
	})

	t.Run("what must still LOAD — every one of these is a legitimate view", func(t *testing.T) {
		cases := []struct {
			name string
			body string
		}{
			{
				name: "a declared value",
				body: "name: v\ntype: widget\nfilter: {property: state, op: \"=\", value: shipped}\n",
			},
			{
				// FR-011a's fold is the SAME rule that makes `Won` resolve to a
				// declared `won`. Refusing a case difference would send the
				// author to fix the one thing that is not wrong.
				name: "a declared value in a different case",
				body: "name: v\ntype: widget\nfilter: {property: state, op: \"=\", value: SHIPPED}\n",
			},
			{
				name: "a LIKE pattern, which is a SHAPE and not a value",
				body: "name: v\ntype: widget\nfilter: {property: state, op: \"LIKE\", value: \"ship%\"}\n",
			},
			{
				name: "IS NULL, which takes no literal",
				body: "name: v\ntype: widget\nfilter: {property: state, op: \"IS NULL\"}\n",
			},
			{
				name: "a non-enum literal, checked by the parser at query time",
				body: "name: v\ntype: widget\nfilter: {property: batch, op: \"=\", value: \"7\"}\n",
			},
			{
				name: "an UNTYPED view, which has no schema to check against",
				body: "name: v\nfilter: {property: state, op: \"=\", value: Closed Won}\n",
			},
			{
				name: "a `file.` property, which is not a record property at all",
				body: "name: v\ntype: widget\nfilter: {property: file.folder, op: \"=\", value: Deals}\n",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				root, schemas := viewFixtureSchemas(t, "")
				root = writeVaultView(t, root, "v.yaml", tc.body)
				set, report, err := LoadViews(root, schemas)
				if err != nil {
					t.Fatalf("LoadViews: %v", err)
				}
				if !report.OK() {
					t.Fatalf("this view is legitimate and must load; got %v", report.Rejections)
				}
				if _, ok := set.Get("v"); !ok {
					t.Fatalf("the view did not load; names: %v", set.Names())
				}
			})
		}
	})
}
