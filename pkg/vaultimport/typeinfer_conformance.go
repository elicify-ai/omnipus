// Omnipus — FR-104b, condition (4): a note is only typed T when T would
// actually ACCEPT the values the note is carrying.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// THE DEFECT THIS FILE CLOSES
//
// Conditions (1)-(3) of the match rule are about SHAPE — which keys a note
// carries and which of a type's required keys are present. Shape alone is
// not enough, and the gap it leaves is not theoretical. Run against a vault
// whose `project` notes all spell `status` as `active` or `done`, three
// untyped notes matched `project` on shape and had `type: project` WRITTEN
// INTO THEM:
//
//	status: blocked            -> not one of project's status values
//	due: sometime next spring  -> not a date, and project's `due` is a date
//	owner: [[[Alice]], [[Bob]]] -> a list where project's `owner` is single
//
// Each write produced a note that FAILS the very schema the same run had
// just generated from the same vault. The importer would then report those
// notes as invalid records — three findings whose sole cause was the
// importer's own guess. That is precisely the "silent wrong guess" the
// founder's ruling forbids: a guess is fine, a guess the operator has to
// discover by reading validation errors is not.
//
// So the rule gains a fourth condition:
//
//	(4) EVERY value the note carries must be a value T would ACCEPT —
//	    right arity, right type, and for an enum, one of T's declared values.
//
// WHY THIS IS NOT A SECOND VALIDATOR. The judgement is not made here at
// all. It is delegated whole to records.ResolveProperty — the SAME function
// records.Validate calls, whose own doc says "validation and filtering both
// go through it, so the two can never disagree about what a record says".
// This file asks it one question per property and reads the answer.
//
// That matters because the acceptance bar for FR-104b is stated in terms of
// the validator: after an import, the number of notes the importer TYPED and
// then REPORTED INVALID must be zero. A conformance check that reasoned
// independently could drift from the validator and reintroduce exactly the
// defect it was written to close — an importer manufacturing its own
// validation errors. Asking the validator's own resolver makes that drift
// impossible rather than merely unlikely.
//
// An earlier draft of this file mirrored validate.go's guard ORDER (absence,
// then arity, then per-element parse) around records.ParseValue. It agreed
// with the validator on every case tested, which is precisely the problem: a
// mirror agrees until the day it does not, and nothing would have caught the
// day it did not.
//
// WHAT (4) DELIBERATELY DOES NOT CHECK: a relation's TARGET type. If
// `owner` is declared `relation to: person` and this note's `owner` points
// at a project, the note still matches. That mismatch is a real finding and
// D5/FR-034's `relation_type_mismatch` is where it belongs — the same place
// it surfaces for the minority links of an already-typed note under
// FR-104a's supermajority rule. Holding an untyped note to a STRICTER
// standard than a typed one would refuse to type notes the vault is already
// full of.
// ---------------------------------------------------------------------------

// shapeBlock names one type that a note fit on shape but not on values, and
// the single value that stopped it. It exists so a refusal can be explained
// in one line naming the property and the offending value, rather than a
// bare "no match" the operator cannot act on.
type shapeBlock struct {
	// Type is the record type the note's SHAPE fit.
	Type string
	// Property is the key whose value T would not accept.
	Property string
	// Detail is the one-line explanation, in the words records itself uses
	// to reject the value.
	Detail string
}

// String renders a block for the report.
func (b shapeBlock) String() string {
	return fmt.Sprintf("%s (its `%s`: %s)", b.Type, b.Property, b.Detail)
}

// joinBlocks renders every near-miss for one note, in shape order.
func joinBlocks(blocks []shapeBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "; ")
}

// everyValueAccepted applies condition (4) to one note against one inferred
// type, returning the first property the type would reject.
//
// Only keys the note actually carries are examined. A property T declares
// that the note omits is condition (3)'s business (required) or simply
// absent (optional), and neither is a value fault.
//
// NOTE WHAT IS **NOT** ASKED HERE: whether a required property is PRESENT.
// That question is condition (3)'s, it is answered by nodeHasValue, and it
// must NOT be re-answered from the PropertyValue.State this function
// receives — see the warning on presenceIsNotResolvedHere below.
func everyValueAccepted(rec records.Record, shape typeShape) (shapeBlock, bool) {
	for _, key := range rec.Frontmatter.Keys {
		if isReservedKey(key) {
			continue
		}
		decl, declared := shape.declared[key]
		if !declared {
			// Condition (2) already rejected this type; nothing to add.
			continue
		}
		if _, present := rec.Frontmatter.Get(key); !present {
			continue
		}

		// The whole judgement, delegated to the validator's own resolver.
		pv := records.ResolveProperty(rec, probeProperty(decl))
		if pv.State != records.StateNonConforming {
			continue
		}
		return shapeBlock{Type: shape.name, Property: key, Detail: findingDetail(pv)}, false
	}
	return shapeBlock{}, true
}

// ---------------------------------------------------------------------------
// PRESENCE IS NOT RESOLVED HERE — a warning, because this looks like a
// simplification waiting to happen and it is not one.
//
// records.ResolveProperty decides PRESENCE by FR-007a's rule, under which
// `note: ""` on a TEXT property is a real, present value. This package
// inferred `required` by a DIFFERENT rule — collectNodeValues counts an
// empty scalar as absence, for every type — and that is the rule that
// produced the schemas being matched against.
//
// So the two disagree, on purpose, and condition (3) must keep asking
// nodeHasValue rather than reading State off the PropertyValue above. If it
// switched, the matching rule would start using a definition of "present"
// that the `required` flags it is testing were never derived from: a
// property marked required because every note had a non-empty value could
// then be satisfied by a note whose value is `""`, and the note would be
// typed and then fail the very requirement that typed it.
//
// In one line: condition (3) uses nodeHasValue; condition (4) uses
// records.ResolveProperty. They are different questions and they stay on
// different oracles.
// ---------------------------------------------------------------------------

// findingDetail renders the first finding on a non-conforming property in
// the words records itself uses, so an operator reading this report and then
// reading a validation finding sees one vocabulary, not two.
func findingDetail(pv records.PropertyValue) string {
	if len(pv.Findings) == 0 {
		// Defensive: StateNonConforming is only ever set alongside a
		// finding, but a detail-less refusal would be exactly the
		// unexplainable inference this work exists to prevent.
		return "the value is not one this type accepts"
	}
	f := pv.Findings[0]
	detail := f.Reason
	if f.Expected != "" {
		detail += "; expected " + f.Expected
	}
	if len(f.Permitted) > 0 {
		detail += "; permitted values are " + strings.Join(f.Permitted, ", ")
	}
	return detail
}

// probeProperty builds the *records.Property that an inferred declaration
// WOULD become once written to a schema file and loaded back.
//
// It must stay faithful to what schema_write.go's renderPropertyDecl emits,
// because the whole point of condition (4) is that a note this run types
// will PASS the schema this run writes. The fields that survive that round
// trip and matter to a value judgement are exactly these four: type, arity,
// the enum's closed set, and a relation's target.
//
// Building a Property with a plain struct literal is explicitly supported by
// pkg/records: ResolveEnum scans Values when its fold cache is nil, and its
// doc comment names this case, so enum membership here is the same
// case-insensitive full-Unicode answer the loaded schema gives.
func probeProperty(p InferredProperty) *records.Property {
	rp := &records.Property{
		Name: p.Name,
		Type: p.Type,
		Many: p.Many,
		To:   p.To,
	}
	for _, v := range p.EnumValues {
		rp.Values = append(rp.Values, records.EnumValue{Name: v})
	}
	return rp
}
