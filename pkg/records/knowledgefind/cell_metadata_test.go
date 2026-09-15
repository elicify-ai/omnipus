// Omnipus — applyCellMetadata is the SOLE server-side source of the five
// fields the entire client editor gate reads (type, values, derived,
// relation, many); it had zero tests before this file.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// project.go's applyCellMetadata (not renderRow, not the propindex layer) is
// the ONE place that decides what an editor is allowed to offer for a cell:
// its declared type, its enum choices, whether it is computed rather than
// stored, whether it is a relation edge, and whether it is list-valued. Every
// one of the SPA's editor-gating tests hand-builds its own VaultFindCell
// fixtures, so a server that silently stopped setting, say, `derived: true`
// on a formula cell would still pass every SPA test on both sides of the wire
// — the SPA would just never be handed a cell that exercises the gate. This
// file is the ONLY place the server's own half of that contract is checked
// against a real *records.Property.
//
// Every case below asserts ALL FIVE fields (present-with-value, or
// explicitly nil), so a fix to one field cannot silently regress another
// field in the same call.
// ---------------------------------------------------------------------------

// wantCell is the full five-field expectation for one applyCellMetadata
// call. A nil pointer means "must be nil (omitted)" — the wire's own contract
// for "does not apply", never a placeholder value.
type wantCell struct {
	typ      *generated.VaultFindCellType
	many     *bool
	derived  *bool
	relation *bool
	values   []string // nil means cell.Values must be nil; non-nil is the expected Value list, in order
}

func checkCell(t *testing.T, name string, cell generated.VaultFindCell, want wantCell) {
	t.Helper()

	switch {
	case want.typ == nil && cell.Type != nil:
		t.Errorf("%s: expected Type to be nil (omitted), got %q", name, *cell.Type)
	case want.typ != nil && cell.Type == nil:
		t.Errorf("%s: expected Type %q, got nil", name, *want.typ)
	case want.typ != nil && cell.Type != nil && *cell.Type != *want.typ:
		t.Errorf("%s: expected Type %q, got %q", name, *want.typ, *cell.Type)
	}

	switch {
	case want.many == nil && cell.Many != nil:
		t.Errorf("%s: expected Many to be nil (omitted), got %v", name, *cell.Many)
	case want.many != nil && cell.Many == nil:
		t.Errorf("%s: expected Many %v, got nil", name, *want.many)
	case want.many != nil && cell.Many != nil && *cell.Many != *want.many:
		t.Errorf("%s: expected Many %v, got %v", name, *want.many, *cell.Many)
	}

	switch {
	case want.derived == nil && cell.Derived != nil:
		t.Errorf("%s: expected Derived to be nil (omitted), got %v", name, *cell.Derived)
	case want.derived != nil && cell.Derived == nil:
		t.Errorf("%s: expected Derived %v, got nil", name, *want.derived)
	case want.derived != nil && cell.Derived != nil && *cell.Derived != *want.derived:
		t.Errorf("%s: expected Derived %v, got %v", name, *want.derived, *cell.Derived)
	}

	switch {
	case want.relation == nil && cell.Relation != nil:
		t.Errorf("%s: expected Relation to be nil (omitted), got %v", name, *cell.Relation)
	case want.relation != nil && cell.Relation == nil:
		t.Errorf("%s: expected Relation %v, got nil", name, *want.relation)
	case want.relation != nil && cell.Relation != nil && *cell.Relation != *want.relation:
		t.Errorf("%s: expected Relation %v, got %v", name, *want.relation, *cell.Relation)
	}

	if want.values == nil {
		if cell.Values != nil {
			t.Errorf("%s: expected Values to be nil (never a placeholder empty slice), got %+v", name, *cell.Values)
		}
		return
	}
	if cell.Values == nil {
		t.Fatalf("%s: expected %d Values, got nil", name, len(want.values))
	}
	got := *cell.Values
	if len(got) != len(want.values) {
		t.Fatalf("%s: expected %d Values, got %d: %+v", name, len(want.values), len(got), got)
	}
	for i, w := range want.values {
		if got[i].Value != w {
			t.Errorf("%s: Values[%d] = %q, want %q (declaration order must be preserved)", name, i, got[i].Value, w)
		}
		if got[i].Position != i {
			t.Errorf("%s: Values[%d].Position = %d, want %d", name, i, got[i].Position, i)
		}
	}
}

func typePtr(t generated.VaultFindCellType) *generated.VaultFindCellType { return &t }

// TestApplyCellMetadata_PopulatesEveryEditorGate is the table-driven pin on
// applyCellMetadata (pkg/records/knowledgefind/project.go). Every row checks
// ALL FIVE editor-gate fields together so no half can pass alone.
func TestApplyCellMetadata_PopulatesEveryEditorGate(t *testing.T) {
	tests := []struct {
		name string
		prop *records.Property
		want wantCell
	}{
		{
			name: "a plain declared enum property carries its type and its values in declaration order",
			prop: &records.Property{
				Name: "status",
				Type: records.TypeEnum,
				Values: []records.EnumValue{
					{Name: "prospect"},
					{Name: "active"},
					{Name: "won"},
				},
			},
			want: wantCell{
				typ:    typePtr(generated.VaultFindCellTypeEnum),
				values: []string{"prospect", "active", "won"},
			},
		},
		{
			name: "a declared text property with Many:true sets the C1 arity signal",
			prop: &records.Property{
				Name: "tags",
				Type: records.TypeText,
				Many: true,
			},
			want: wantCell{
				typ:  typePtr(generated.VaultFindCellTypeText),
				many: boolPtr(true),
			},
		},
		{
			name: "a property with Formula set is derived",
			prop: &records.Property{
				Name:    "total",
				Type:    records.TypeDecimal,
				Formula: "sum(line_items.amount)",
			},
			want: wantCell{
				typ:     typePtr(generated.VaultFindCellTypeDecimal),
				derived: boolPtr(true),
			},
		},
		{
			name: "a file.* virtual property is derived even with NO Formula — the second clause the code's own comment claims would be missed otherwise",
			prop: &records.Property{
				Name: records.FileNamespace + "tags",
				Type: records.TypeText,
				// Formula deliberately left empty: this row exists to prove
				// the namespace-prefix check fires on its own.
			},
			want: wantCell{
				typ:     typePtr(generated.VaultFindCellTypeText),
				derived: boolPtr(true),
			},
		},
		{
			name: "a relation property is flagged relation AND typed relation",
			prop: &records.Property{
				Name: "owner",
				Type: records.TypeRelation,
				To:   "person",
			},
			want: wantCell{
				typ:      typePtr(generated.VaultFindCellTypeRelation),
				relation: boolPtr(true),
			},
		},
		{
			name: "a person property is flagged relation",
			prop: &records.Property{
				Name: "assignee",
				Type: records.TypePerson,
			},
			want: wantCell{
				typ:      typePtr(generated.VaultFindCellTypePerson),
				relation: boolPtr(true),
			},
		},
		{
			name: "an Undeclared property gets NO type and NO many, even though the synthesised Property itself says Many:true — VaultFindCell.type is reserved for a genuinely declared type",
			prop: &records.Property{
				Name:       "custom_field",
				Type:       records.TypeText,
				Many:       true,
				Undeclared: true,
			},
			want: wantCell{
				typ:  nil,
				many: nil,
			},
		},
		{
			name: "a non-enum property has nil Values, never an empty slice",
			prop: &records.Property{
				Name: "due",
				Type: records.TypeDate,
			},
			want: wantCell{
				typ: typePtr(generated.VaultFindCellTypeDate),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cell := &generated.VaultFindCell{}
			applyCellMetadata(cell, tc.prop)
			checkCell(t, tc.name, *cell, tc.want)
		})
	}
}
