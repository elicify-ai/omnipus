// Omnipus — tests for view_find_bridge.go's ViewDef -> VaultFindRequest
// carry-over, oracled against each format's own documented semantics, never
// against the implementation under test.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func ptr[T any](v T) *T { return &v }

// newTestView builds a view directly, for the cases that are about the LOADER
// rather than about a file. `filter` may be nil for a view with no query.
func newTestView(name string, filter *generated.VaultFilterNode) *SavedView {
	return &SavedView{
		Def:        generated.ViewDef{Name: name, Type: ptr("deal"), Filter: filter},
		SourcePath: name + ".yaml",
	}
}

func newSet(views ...*SavedView) *ViewSet {
	s := NewViewSet()
	for _, v := range views {
		s.add(v)
	}
	return s
}

// TestViewFindLoader_MechanicalFieldsCarryOver proves the 1:1 fields
// (type/grouping/sort/limit/properties/aggregates) survive the carry-over
// unchanged, for a view with no filter.
func TestViewFindLoader_MechanicalFieldsCarryOver(t *testing.T) {
	def := generated.ViewDef{
		Name: "open-deals", Type: ptr("deal"),
		Grouping:   ptr([]generated.ViewGroupBy{{Property: "stage"}}),
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
	if req.GroupBy == nil || (*req.GroupBy)[0].Property != "stage" {
		t.Fatalf("GroupBy = %+v, want [stage]", req.GroupBy)
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

// TestViewFindLoader_UnknownNameRefused proves the ordinary "not declared at
// all" case reports ok=false, the same as an unservable one — the two collapse
// to one bool by the ViewLoader interface's own shape (view_find_bridge.go's
// header explains why, and why ServeRefusal exists alongside it).
func TestViewFindLoader_UnknownNameRefused(t *testing.T) {
	loader := NewViewFindLoader(newSet(newTestView("real-view", nil)))
	if _, ok := loader.View("does-not-exist"); ok {
		t.Fatal("View(\"does-not-exist\") = true, want false")
	}
}

// TestViewFindLoader_FilterTreeIsCopiedNotAliased is what makes "the filter
// half is a COPY, not a translation" a rule rather than a comment.
//
// A view's `filter` already IS find's VaultFilterNode, so the bridge hands the
// request the same shape. The hazard is therefore not mistranslation — it is
// SHARING: a request that pointed into the saved view's own tree would let
// find's engine, which normalises a request in place, rewrite the in-memory
// copy of a file on disk. Every subsequent use of that view would then run a
// query nobody wrote, for as long as the process lived.
//
// A top-level `req.Filter != v.Def.Filter` check does not catch that on its
// own: a one-level copy still shares the child SLICE, so this asserts
// independence at DEPTH by mutating a grandchild of the request and requiring
// the view to be unchanged.
func TestViewFindLoader_FilterTreeIsCopiedNotAliased(t *testing.T) {
	tree := &generated.VaultFilterNode{All: &[]generated.VaultFilterNode{
		{Property: ptr("status"), Op: ptr(generated.Equal), Value: ptr("open")},
		{Any: &[]generated.VaultFilterNode{
			{Property: ptr("amount"), Op: ptr(generated.GreaterThanEqual), Value: ptr("10000")},
			{Not: &generated.VaultFilterNode{Property: ptr("owner"), Op: ptr(generated.ISNULL)}},
		}},
	}}
	v := newTestView("big-open-deals", tree)
	loader := NewViewFindLoader(newSet(v))

	req, ok := loader.View("big-open-deals")
	if !ok {
		t.Fatal("View(\"big-open-deals\") = false, want true")
	}
	if req.Filter == nil || req.Filter.All == nil || len(*req.Filter.All) != 2 {
		t.Fatalf("Filter = %+v, want an `all` node with 2 children", req.Filter)
	}
	if req.Filter == v.Def.Filter {
		t.Fatal("the request's filter IS the view's own node; find normalises a request in place, so the view on disk's in-memory copy would be rewritten under it")
	}

	// Reach two levels down and change the request. The view must not move.
	nested := (*req.Filter.All)[1]
	if nested.Any == nil || len(*nested.Any) != 2 {
		t.Fatalf("the nested `any` did not survive the copy: %+v", nested)
	}
	(*nested.Any)[0].Value = ptr("999999999")
	(*req.Filter.All)[0].Property = ptr("hijacked")

	original := *v.Def.Filter
	if (*original.All)[0].Property == nil || *(*original.All)[0].Property != "status" {
		t.Errorf("mutating the request rewrote the view's own leaf: %s", renderNode(original))
	}
	deepLeaf := (*(*original.All)[1].Any)[0]
	if deepLeaf.Value == nil || *deepLeaf.Value != "10000" {
		t.Errorf("mutating the request two levels down rewrote the view's own tree — the copy is shallow: %s", renderNode(original))
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
