// Omnipus — tests for view_find_bridge.go's ViewDef -> VaultFindRequest
// translation, oracled directly against each format's own documented
// semantics (RecordFilter.Negate/IncludeAbsent; find's tool.go Parameters()
// doc on `not:`/`<>`), never against the implementation under test.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func ptr[T any](v T) *T { return &v }

func newTestView(name string, filters []generated.RecordFilter) *SavedView {
	def := generated.ViewDef{
		Name:          name,
		Type:          ptr("deal"),
		SchemaVersion: 1,
	}
	if filters != nil {
		def.Filters = &filters
	}
	return &SavedView{Def: def, SourcePath: name + ".yaml"}
}

func newSet(views ...*SavedView) *ViewSet {
	s := NewViewSet()
	for _, v := range views {
		s.add(v)
	}
	return s
}

// TestViewFindLoader_MechanicalFieldsCarryOver proves the 1:1 fields
// (type/group_by/sort/limit/properties/aggregates) survive translation
// unchanged, for a view with no filter.
func TestViewFindLoader_MechanicalFieldsCarryOver(t *testing.T) {
	def := generated.ViewDef{
		Name: "open-deals", Type: ptr("deal"), SchemaVersion: 1,
		GroupBy:    ptr([]string{"stage"}),
		Properties: ptr([]string{"name", "amount"}),
		Limit:      ptr(25),
		Sort:       ptr([]generated.RecordSort{{Property: "amount", Direction: generated.RecordSortDirectionDesc}}),
		Aggregates: ptr([]generated.RecordAggregate{{Op: generated.RecordAggregateOpSum, Property: ptr("amount")}}),
	}
	v := &SavedView{Def: def}
	loader := NewViewFindLoader(newSet(v))

	req, ok := loader.View("open-deals")
	if !ok {
		t.Fatal("View(\"open-deals\") = false, want true")
	}
	if req.Type == nil || *req.Type != "deal" {
		t.Fatalf("Type = %v, want deal", req.Type)
	}
	if req.GroupBy == nil || (*req.GroupBy)[0] != "stage" {
		t.Fatalf("GroupBy = %v, want [stage]", req.GroupBy)
	}
	if req.Select == nil || len(*req.Select) != 2 || (*req.Select)[0] != "name" {
		t.Fatalf("Select = %v, want [name amount]", req.Select)
	}
	if req.Limit == nil || *req.Limit != 25 {
		t.Fatalf("Limit = %v, want 25", req.Limit)
	}
	if req.Sort == nil || (*req.Sort)[0].Property != "amount" || *(*req.Sort)[0].Direction != generated.VaultFindSortDirectionDesc {
		t.Fatalf("Sort = %+v, want [{amount desc}]", req.Sort)
	}
	if req.Aggregate == nil || (*req.Aggregate)[0].Op != generated.VaultFindAggregateOpSum {
		t.Fatalf("Aggregate = %+v, want [{sum amount}]", req.Aggregate)
	}
}

// TestViewFindLoader_FilterTable exercises translateRecordFilter's whole
// negate/include_absent table against expected wire ops, oracled from
// RecordFilter's own doc (default include_absent=true on negate) and find's
// tool.go doc ("{not:{p,'=',v}} to include absent; {p,'<>',v} excludes").
func TestViewFindLoader_FilterTable(t *testing.T) {
	cases := []struct {
		name    string
		filter  generated.RecordFilter
		wantOp  *generated.VaultFilterNodeOp // nil means the leaf is wrapped in `not`
		wantNot bool
	}{
		{
			name:   "eq positive",
			filter: generated.RecordFilter{Property: "status", Op: generated.Eq, Values: ptr([]generated.RecordValue{{Type: "enum", Enum: ptr("won")}})},
			wantOp: ptr(generated.Equal),
		},
		{
			name:    "eq negated default include_absent=true wraps in not",
			filter:  generated.RecordFilter{Property: "status", Op: generated.Eq, Negate: ptr(true), Values: ptr([]generated.RecordValue{{Type: "enum", Enum: ptr("won")}})},
			wantNot: true,
		},
		{
			name:   "eq negated include_absent=false becomes <>",
			filter: generated.RecordFilter{Property: "status", Op: generated.Eq, Negate: ptr(true), IncludeAbsent: ptr(false), Values: ptr([]generated.RecordValue{{Type: "enum", Enum: ptr("won")}})},
			wantOp: ptr(generated.LessThanGreaterThan),
		},
		{
			name:   "gt negated exclude-absent becomes <=",
			filter: generated.RecordFilter{Property: "amount", Op: generated.Gt, Negate: ptr(true), IncludeAbsent: ptr(false), Values: ptr([]generated.RecordValue{{Type: "integer", Integer: ptr("100")}})},
			wantOp: ptr(generated.LessThanEqual),
		},
		{
			name:   "gte negated exclude-absent becomes <",
			filter: generated.RecordFilter{Property: "amount", Op: generated.Gte, Negate: ptr(true), IncludeAbsent: ptr(false), Values: ptr([]generated.RecordValue{{Type: "integer", Integer: ptr("100")}})},
			wantOp: ptr(generated.LessThan),
		},
		{
			name:   "is_absent positive becomes IS NULL",
			filter: generated.RecordFilter{Property: "closed_at", Op: generated.IsAbsent},
			wantOp: ptr(generated.ISNULL),
		},
		{
			name:   "is_absent negated becomes IS NOT NULL regardless of include_absent",
			filter: generated.RecordFilter{Property: "closed_at", Op: generated.IsAbsent, Negate: ptr(true)},
			wantOp: ptr(generated.ISNOTNULL),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node, refusal := translateRecordFilter(tc.filter, "table-view", 1)
			if refusal != nil {
				t.Fatalf("translateRecordFilter(%+v) refused (%s), want a translation", tc.filter, refusal)
			}
			if tc.wantNot {
				if node.Not == nil {
					t.Fatalf("node = %+v, want a `not` wrapper", node)
				}
				return
			}
			if node.Op == nil || *node.Op != *tc.wantOp {
				t.Fatalf("op = %v, want %v", node.Op, *tc.wantOp)
			}
		})
	}
}

// TestViewFindLoader_ContainsIsUntranslatable proves the operator-vocabulary
// gap this file's header documents: `contains` has no VaultFilterNodeOp
// representation, so a view using it must be refused, never silently
// dropped or substituted.
func TestViewFindLoader_ContainsIsUntranslatable(t *testing.T) {
	v := newTestView("tagged", []generated.RecordFilter{
		{Property: "tags", Op: generated.Contains, Values: ptr([]generated.RecordValue{{Type: "text", Text: ptr("urgent")}})},
	})
	loader := NewViewFindLoader(newSet(v))

	if _, ok := loader.View("tagged"); ok {
		t.Fatal("View(\"tagged\") using `contains` = true, want false (untranslatable)")
	}
	for _, n := range loader.Names() {
		if n == "tagged" {
			t.Fatal("Names() lists an untranslatable view — View() and Names() must agree")
		}
	}
}

// TestViewFindLoader_ViaIsUntranslatable proves the second documented gap:
// a relation-hop filter leaf has no per-leaf equivalent in find's grammar.
func TestViewFindLoader_ViaIsUntranslatable(t *testing.T) {
	v := newTestView("via-deals", []generated.RecordFilter{
		{Property: "industry", Op: generated.Eq, Via: ptr([]string{"company"}),
			Values: ptr([]generated.RecordValue{{Type: "text", Text: ptr("fintech")}})},
	})
	loader := NewViewFindLoader(newSet(v))

	if _, ok := loader.View("via-deals"); ok {
		t.Fatal("View(\"via-deals\") using `via` = true, want false (untranslatable)")
	}
}

// TestViewFindLoader_UnknownNameRefused proves the ordinary "not declared at
// all" case still reports ok=false, the same as an untranslatable one — the
// two collapse to one bool by the interface's own shape (this file's header
// explains why).
func TestViewFindLoader_UnknownNameRefused(t *testing.T) {
	loader := NewViewFindLoader(newSet(newTestView("real-view", nil)))
	if _, ok := loader.View("does-not-exist"); ok {
		t.Fatal("View(\"does-not-exist\") = true, want false")
	}
}

// TestViewFindLoader_MultiLeafAndsTogether proves a multi-clause view
// becomes one `all` node, per ViewDef.Filters' own "combined with AND" doc.
func TestViewFindLoader_MultiLeafAndsTogether(t *testing.T) {
	v := newTestView("big-open-deals", []generated.RecordFilter{
		{Property: "status", Op: generated.Eq, Values: ptr([]generated.RecordValue{{Type: "enum", Enum: ptr("open")}})},
		{Property: "amount", Op: generated.Gte, Values: ptr([]generated.RecordValue{{Type: "integer", Integer: ptr("10000")}})},
	})
	loader := NewViewFindLoader(newSet(v))

	req, ok := loader.View("big-open-deals")
	if !ok {
		t.Fatal("View(\"big-open-deals\") = false, want true")
	}
	if req.Filter == nil || req.Filter.All == nil || len(*req.Filter.All) != 2 {
		t.Fatalf("Filter = %+v, want an `all` node with 2 children", req.Filter)
	}
}

// TestViewFindLoader_NilSetIsEmptyNotPanic proves the "no saved views" shape
// degrades exactly like LoadViews' own empty case, per NewViewFindLoader's
// doc comment.
func TestViewFindLoader_NilSetIsEmptyNotPanic(t *testing.T) {
	loader := NewViewFindLoader(nil)
	if names := loader.Names(); len(names) != 0 {
		t.Fatalf("Names() on a nil set = %v, want empty", names)
	}
	if _, ok := loader.View("anything"); ok {
		t.Fatal("View() on a nil set = true, want false")
	}
}
