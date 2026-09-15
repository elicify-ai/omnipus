// Omnipus — ADR-083 CW-4/CW-5: one conversion from a declared *Property to
// the wire shapes that describe it, shared by every caller that renders a
// schema property onto the gateway/SPA boundary.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// RecordType.Properties (contracts/components/schemas/RecordType.yaml,
// ADR-083 CW-4) and VaultFindCell's type/values metadata
// (VaultFindCell.yaml, CW-5) both describe the SAME fact — a declared
// property's type, arity and (for an enum) closed value set — to two
// different wire shapes (PropertyDef and EnumValueDef respectively). Writing
// that conversion twice, once in the gateway's record-schema handler and
// once in knowledgefind's cell renderer, is how the two end up disagreeing
// about what an enum's declared position or label means. This file is the
// one place that decides.
//
// ---------------------------------------------------------------------------
// AND IT DECIDES BY SWITCH, NEVER BY CAST (ADR-083 review F1)
//
// Go lets `generated.EnumValueDefGroup(v.Group)` compile for any string.
// The generated type is a CLOSED wire enum; the Go value feeding it was not.
// A cast between them is not a conversion, it is an unchecked assertion, and
// when it is wrong nothing in Go notices — the failure surfaces at the SPA's
// Zod edge, where ONE non-conforming field drops the WHOLE response and a
// dashboard goes blank with no server error to explain it.
//
// So every Go-value-to-wire-enum conversion in this package goes through a
// function here, and each one is pinned by a test against a NAMED source of
// truth (PropertyTypes, EnumGroups) so a ninth property type or a fourth
// lifecycle group fails a test rather than a browser. This is the discipline
// pkg/gateway/rest_knowledge.go's knowledgeEdgeUnresolvedReason established
// and documented at length; F1 is what happened where it was not applied.
//
// The two families differ in what an unmapped value MEANS, and therefore in
// what they do about it:
//
//	PropertyType   a closed Go enum this package owns and validates at parse
//	               time. An unmapped value cannot come from operator data; it
//	               can only mean a developer added a ninth type and did not
//	               extend the wire contract. That is a bug in this build, so
//	               it PANICS — loud, recovered per-request by net/http, and
//	               made unreachable by the pinning test.
//
//	enum `group`   operator-authored YAML. An unmapped value is a person's
//	               typo, not our bug, so it is OMITTED rather than fatal —
//	               absence is already the documented "ungrouped" state, so
//	               dropping it is lossless to the client. The operator is
//	               told separately: parseEnumValue refuses the file, and the
//	               refusal reaches them as a RecordProblem.
// ---------------------------------------------------------------------------

// wirePropertyTypeString validates that t is a member of the closed
// PropertyTypes set and returns its wire spelling.
//
// It exists so the four generated enums that carry a property type —
// PropertyDefType, VaultFindCellType, RecordPropertyValueType and
// RecordValueType — are fed a string PROVEN to be one of the eight, from ONE
// place, rather than from four independent casts that each compile whatever
// they are given. The four wire enums having identical members is a coupling
// nothing in the generated code enforces; TestWirePropertyType_EveryDeclared
// TypeIsValidInAllFourWireEnums enforces it here instead.
func wirePropertyTypeString(t PropertyType) string {
	for _, known := range PropertyTypes {
		if t == known {
			return string(t)
		}
	}
	// Unreachable while the pinning test passes; see this file's header for
	// why a panic rather than a silent degrade is the right failure here.
	panic(fmt.Sprintf("records: property type %q is not a member of PropertyTypes — "+
		"a new type was added without extending the wire enums it feeds "+
		"(contracts/components/schemas/PropertyDef.yaml, VaultFindCell.yaml, "+
		"RecordPropertyValue.yaml, RecordValue.yaml)", string(t)))
}

// WirePropertyDefType renders a declared property type as PropertyDef.type.
func WirePropertyDefType(t PropertyType) generated.PropertyDefType {
	return generated.PropertyDefType(wirePropertyTypeString(t))
}

// WireVaultFindCellType renders a declared property type as VaultFindCell.type.
func WireVaultFindCellType(t PropertyType) generated.VaultFindCellType {
	return generated.VaultFindCellType(wirePropertyTypeString(t))
}

// WireRecordPropertyValueType renders a declared property type as
// RecordPropertyValue.type.
func WireRecordPropertyValueType(t PropertyType) generated.RecordPropertyValueType {
	return generated.RecordPropertyValueType(wirePropertyTypeString(t))
}

// WireRecordValueType renders a VALUE's own type as RecordValue.type. The
// domain is the same closed PropertyType set — a TypedValue carries the type
// it was parsed as — so it shares the same validation.
func WireRecordValueType(t PropertyType) generated.RecordValueType {
	return generated.RecordValueType(wirePropertyTypeString(t))
}

// WireEnumValueGroup renders D4's lifecycle bucket as EnumValueDef.group.
//
// Returns ok=false for the empty string (ungrouped — the documented, common
// case) AND for any value outside EnumGroups, and the caller MUST then leave
// the wire field absent. The two are deliberately one outcome: the wire has
// no spelling for "grouped, but not one of the three", so absence is the only
// truthful rendering of either.
func WireEnumValueGroup(group string) (generated.EnumValueDefGroup, bool) {
	switch group {
	case EnumGroupOpen:
		return generated.EnumValueDefGroupOpen, true
	case EnumGroupDone:
		return generated.EnumValueDefGroupDone, true
	case EnumGroupCancelled:
		return generated.EnumValueDefGroupCancelled, true
	default:
		return "", false
	}
}

// ---------------------------------------------------------------------------

// EnumValueDefs renders a property's declared enum values (Values) in the
// wire shape VaultFindCell.values and RecordType.PropertyDef.values share.
// Position is the declared index (FR-010's own declaration order — sorting is
// LEXICAL, this is display order only), so a rejection and a dropdown both
// read the operator's own file back in the order they wrote it.
//
// Returns nil for a non-enum property (Values is empty for every other
// type), so a caller can assign the result straight to an optional pointer
// field and get "absent" for free rather than a present-but-empty slice.
func (p *Property) EnumValueDefs() []generated.EnumValueDef {
	if p == nil || len(p.Values) == 0 {
		return nil
	}
	out := make([]generated.EnumValueDef, 0, len(p.Values))
	for i, v := range p.Values {
		d := generated.EnumValueDef{Value: v.Name, Position: i}
		if v.Label != "" {
			label := v.Label
			d.Label = &label
		}
		if group, ok := WireEnumValueGroup(v.Group); ok {
			d.Group = &group
		}
		out = append(out, d)
	}
	return out
}

// Wire renders a declared property as a RecordType.PropertyDef.
//
// Formula is deliberately NEVER populated here, even for a Property built
// with one set (e.g. a saved view's formula-namespace property,
// knowledgefind/namespace.go): a schema loaded through LoadSchemas can never
// carry a Formula-bearing property in the first place — a schema file that
// declares `formula:` on a property is refused at load (schema.go,
// propertyDeclKeys' `formula` entry) — so a *Property reaching RecordSchema's
// wire form is by construction never a formula property. PropertyDef.Formula
// exists on the wire only so the refusal has something to name, never so the
// server can echo one back.
func (p *Property) Wire() generated.PropertyDef {
	d := generated.PropertyDef{
		Name:     p.Name,
		Type:     WirePropertyDefType(p.Type),
		Many:     p.Many,
		Required: p.Required,
	}
	if p.Label != "" {
		label := p.Label
		d.Label = &label
	}
	if p.To != "" {
		to := p.To
		d.To = &to
	}
	if p.Inverse != "" {
		inverse := p.Inverse
		d.Inverse = &inverse
	}
	if p.Unit != "" {
		unit := p.Unit
		d.Unit = &unit
	}
	if p.UnitProperty != "" {
		unitProp := p.UnitProperty
		d.UnitProperty = &unitProp
	}
	if vals := p.EnumValueDefs(); len(vals) > 0 {
		d.Values = &vals
	}
	return d
}
