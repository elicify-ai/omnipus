// Omnipus — ADR-068 D5 / spec FR-032: the derived, undeclared reverse edge.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "strings"

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS
//
// D5: "A relation is declared on one side and stored on one side":
//
//	deal.company: { type: relation, to: company, inverse: deals }
//
// `company.deals` then exists and is NEVER STORED (FR-032) — it is computed
// from the index. This file is the ONE step that computation needs and
// nothing did before it: turning the derived name "deals", asked of a
// "company" record, back into the forward declaration a caller can actually
// walk. Everything past that — scanning deal's relation edges, resolving
// each one to a record identity, keeping only the ones that land on the
// company in hand — is pkg/records/vaultfind's, because it needs the
// properties index and the relation resolver, neither of which this package
// may import (pkg/records is the type core; it depends on nothing else in
// Omnipus, and that is deliberate — see doc.go).
// ---------------------------------------------------------------------------

// InverseEdge names one declared relation whose reverse edge is exposed,
// undeclared a second time, on the relation's OWN target type.
type InverseEdge struct {
	// SourceType is the record type that DECLARES the relation — "deal" in
	// the example above.
	SourceType string
	// Source is the declared relation property itself (deal's "company"
	// property). Its To is the type the inverse is exposed on and its
	// Inverse is the name it is exposed under; both are also available as
	// TargetType() and Name() below so a caller does not have to know that.
	Source *Property
}

// TargetType is the record type the inverse is exposed ON — Source.To.
func (e InverseEdge) TargetType() string {
	if e.Source == nil {
		return ""
	}
	return e.Source.To
}

// Name is the inverse's own exposed name — Source.Inverse.
func (e InverseEdge) Name() string {
	if e.Source == nil {
		return ""
	}
	return e.Source.Inverse
}

// FindInverses returns every declared relation, anywhere in the schema set,
// whose derived inverse is exposed as `name` on `targetType` records.
//
// It is deliberately a SLICE, not a single value or an (edge, ok) pair. D5
// declares an inverse on the relation's own side, once — but nothing in the
// schema forbids two DIFFERENT record types each declaring `inverse: deals`
// onto the SAME target type (a "contract" and a "deal" schema could each
// point `company: {to: company, inverse: deals}`). Resolving that silently
// to whichever one the map happened to visit first would be exactly the
// class of failure ADR-068 exists to remove: the same query would answer
// differently depending on iteration order, and nothing would say so.
//
//   - Zero matches — "deals" names no declared inverse onto this type. The
//     caller reports it the same way an unknown property is reported
//     (FR-024): named, never a silent empty result.
//   - Exactly one — the ordinary case, and what D5's own example produces.
//   - More than one — a genuine name collision. The caller must report it
//     BY NAME (every colliding source type), never pick one.
//
// A record type declaring a property is unrelated to another type
// declaring one of the same name (FR-009) — inverse names inherit that
// rule rather than being exempt from it, which is why this walks every
// declared type rather than stopping at the first match.
func (s *SchemaSet) FindInverses(targetType, name string) []InverseEdge {
	name = strings.TrimSpace(name)
	if s == nil || targetType == "" || name == "" {
		return nil
	}
	var out []InverseEdge
	for _, t := range s.Types() {
		sc, ok := s.Get(t)
		if !ok {
			continue
		}
		for _, pname := range sc.PropertyNames() {
			p, ok := sc.Property(pname)
			if !ok {
				continue
			}
			if p.Inverse == "" || p.To == "" {
				continue
			}
			if p.Inverse != name || p.To != targetType {
				continue
			}
			out = append(out, InverseEdge{SourceType: t, Source: p})
		}
	}
	return out
}
