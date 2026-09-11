// Omnipus — ADR-083 CW-4/CW-5: one conversion from a declared *Property to
// the wire shapes that describe it, shared by every caller that renders a
// schema property onto the gateway/SPA boundary.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "github.com/elicify-ai/omnipus/pkg/api/generated"

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
		if v.Group != "" {
			group := generated.EnumValueDefGroup(v.Group)
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
		Type:     generated.PropertyDefType(p.Type),
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
