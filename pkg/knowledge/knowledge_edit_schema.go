// Omnipus — ADR-068 FR-011/FR-042: refusing a knowledge_edit write that does not
// conform to the note's own declared record schema, rather than writing it
// and letting check_integrity discover the damage later.
//
// # Why this validation happens HERE and not in pkg/records
//
// pkg/records/validate.go answers "does this ALREADY-WRITTEN note conform to
// its schema" — it reads a Record's parsed Frontmatter and reports findings.
// This file answers a different, earlier question: "does the value an agent
// is ABOUT TO WRITE conform", using the exact same authority (Property,
// ParseValue, ResolveEnum) so the two can never disagree about what is
// valid. It does not duplicate validate.go's logic — it builds a synthetic
// records.Node from the incoming write value and hands it to the same
// records.ParseValue every read-time validation uses.
//
// # Ordinary notes are unconstrained (FR-005)
//
// A note with no declared `type:`, or a declared type with no matching
// schema file, is not a record. Every property name and every value is
// accepted without comment — there is nothing to violate, because nothing
// was ever declared. This mirrors ResolveProperty's own "absent state" and
// Record.IsRecord's boolean-with-no-error philosophy.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// Sentinel errors for a schema-refused write, so a caller can branch on the
// CLASS without parsing message text — mirroring author.go's own sentinel
// pattern for its refusals.
var (
	// ErrUnknownProperty means the record's schema declares no property by
	// that name.
	ErrUnknownProperty = errors.New("knowledge: record schema declares no such property")
	// ErrPropertyArity means the value's shape (scalar vs list) disagrees
	// with the property's declared Many.
	ErrPropertyArity = errors.New("knowledge: value arity does not match the declared property")
	// ErrPropertyValue means one element failed type, enum or shape
	// validation (records.ParseValue).
	ErrPropertyValue = errors.New("knowledge: value does not conform to the declared property")
)

// knowledgeEditGovernanceReason distinguishes WHY a write's record type did
// not resolve to a schema (G3, design doc §1.3/§3 class 5) — three distinct
// misses that used to collapse into one indistinguishable "nothing was
// checked" outcome: unparsable frontmatter, an absent/empty/list-valued
// `type:`, and a declared type with no matching schema. A fourth case (G4)
// is a declared type whose OWN schema FILE exists but was REJECTED at load
// time (malformed YAML, missing schema_version, a duplicate declaration,
// ...) — records.LoadSchemas simply omits a rejected type from the returned
// SchemaSet, so without tracking this separately it is indistinguishable
// from "nobody ever declared this type", which is exactly the silent
// degrade G4 exists to stop.
type knowledgeEditGovernanceReason int

const (
	// knowledgeEditGoverned means a schema resolved for this write's record
	// type and validation ran against it. The zero value, so a
	// knowledgeEditGovernance left unset (an op that never touches a
	// property, e.g. append_section/replace_body) also reads as "nothing to
	// report" via Note().
	knowledgeEditGoverned knowledgeEditGovernanceReason = iota
	// knowledgeEditUnparsable means src's frontmatter could not be parsed at
	// all.
	knowledgeEditUnparsable
	// knowledgeEditNoType means src declares no `type:` — absent, empty, or
	// (per Record.TypeName) list-valued.
	knowledgeEditNoType
	// knowledgeEditUnknownType means src declares a type, but no schema file
	// in this vault declares that type — and no rejected schema file claims
	// it either (see knowledgeEditRejectedSchema).
	knowledgeEditUnknownType
	// knowledgeEditRejectedSchema means src declares a type whose OWN schema
	// file was found and parsed as a candidate, but rejected at load time
	// (G4) — RejectionReason on knowledgeEditGovernance names why.
	knowledgeEditRejectedSchema
)

// knowledgeEditGovernance is what a write's schema resolution decided —
// carried out of the validation call so the tool-level result can tell the
// caller "validated and fine" from "nothing was checked, because ..." (G3).
// The zero value means "nothing to report": either a schema governed the
// write (ordinary success — the caller already knows from the absence of an
// error) or this op never populates one at all.
type knowledgeEditGovernance struct {
	Reason knowledgeEditGovernanceReason
	// TypeName is the declared type, when one was declared at all —
	// populated for knowledgeEditUnknownType and knowledgeEditRejectedSchema
	// (and, redundantly but harmlessly, knowledgeEditGoverned).
	TypeName string
	// RejectionReason is the records.SchemaRejection.Reason text for
	// TypeName's schema file — populated only for knowledgeEditRejectedSchema.
	RejectionReason string
}

// Note renders the governance outcome as an appendable result line, or ""
// when there is nothing to say: a schema governed the write, or this op
// never resolves one. FR-072: compact text, one line, no JSON.
func (g knowledgeEditGovernance) Note() string {
	switch g.Reason {
	case knowledgeEditUnparsable:
		return "NOTE: no schema governed this write — the note's frontmatter could not be parsed, so nothing was checked"
	case knowledgeEditNoType:
		return "NOTE: no schema governed this write — the note declares no record type, so nothing was checked"
	case knowledgeEditUnknownType:
		return fmt.Sprintf("NOTE: no schema governed this write — %q has no schema in this vault, so nothing was checked", g.TypeName)
	case knowledgeEditRejectedSchema:
		return fmt.Sprintf("NOTE: no schema governed this write — %s's schema file failed to load (%s), so nothing was checked; fix it via knowledge_configure", g.TypeName, g.RejectionReason)
	default:
		return ""
	}
}

// knowledgeEditRejectedSchemaDetail reports whether report rejected a schema
// file that declared typeName, and if so, the reason text (G4). A nil report
// (a caller that has none to hand, or hasn't been updated) always answers
// false — the caller then falls back to knowledgeEditUnknownType, which was
// this whole call site's behaviour before G4.
func knowledgeEditRejectedSchemaDetail(report *records.SchemaLoadReport, typeName string) (reason string, rejected bool) {
	if report == nil {
		return "", false
	}
	for _, rej := range report.Rejections {
		if rej.Type == typeName {
			return rej.Reason, true
		}
	}
	return "", false
}

// knowledgeEditResolveSchema resolves src's own declared record type against
// set, when it has one. reason is knowledgeEditGoverned exactly when there is
// something to validate against; every other reason is a distinct miss
// (G3/G4) that both callers below (schema validation and the link
// operation's arity lookup) still treat identically for THEIR OWN decision
// (FR-005's "ordinary notes are unconstrained") — but the reason itself now
// survives to the tool-result layer instead of being thrown away.
//
// It is deliberately not an error return, even though a records.ParseFrontmatter
// failure is a real parse error: the shared reason is documented once here
// rather than twice at the two call sites.
//
// An unparsable frontmatter block specifically is not THIS function's
// failure to report. Every splice this package's callers run immediately
// after a successful resolution here (SetPropertyList, SetPropertyScalarChecked,
// AddListValue, RemoveListValue) calls parseListSpan -> fmParse on the very
// same src, independently and unconditionally, and refuses with
// ErrFrontmatterUnterminated when it cannot parse. A caller of this
// function never ends up believing an unparsable note was accepted: the very
// next call in the same NoteEdit closure re-parses it and refuses. Two
// parses of the same bytes for two different questions ("does a schema
// apply" vs. "where do I splice") is the cost of keeping those two
// questions separate rather than threading a parsed block through both.
//
// The `ferr != nil` branch below is, under records.ParseFrontmatter's
// CURRENT implementation, provably unreachable as a distinguishing case: read
// against its source, every one of its error returns hands back a
// Frontmatter with an empty Values map, which makes rec.TypeName() return ""
// unconditionally — the exact same answer the `typeName == ""` check two
// lines down already gives. A mutation that deletes this branch cannot be
// killed by any test built on the real parser, and one was tried. The branch
// stays anyway: "every current error path happens to also leave Values
// empty" is an implementation detail of another package, not a promise in
// ParseFrontmatter's documented contract, and collapsing this check into
// that assumption would make vault_edit_schema.go's correctness depend on
// something it has no way to notice changing.
func knowledgeEditResolveSchema(set *records.SchemaSet, report *records.SchemaLoadReport, src []byte) (schema *records.Schema, typeName string, reason knowledgeEditGovernanceReason, rejectionDetail string) {
	fm, ferr := records.ParseFrontmatter(src)
	if ferr != nil {
		return nil, "", knowledgeEditUnparsable, ""
	}
	rec := records.Record{Frontmatter: fm}
	typeName = rec.TypeName()
	if typeName == "" {
		return nil, "", knowledgeEditNoType, ""
	}
	schema, ok := set.Get(typeName)
	if ok {
		return schema, typeName, knowledgeEditGoverned, ""
	}
	// G4: a type that resolves to no LIVE schema might still be one whose
	// schema file the loader saw and rejected — distinct from a type nobody
	// ever declared at all (knowledgeEditUnknownType).
	if detail, rejected := knowledgeEditRejectedSchemaDetail(report, typeName); rejected {
		return nil, typeName, knowledgeEditRejectedSchema, detail
	}
	return nil, typeName, knowledgeEditUnknownType, ""
}

// knowledgeEditValidatePropertyAgainstSchema is the CORE arity/value check,
// shared by every write path that has already resolved a governing schema —
// knowledgeEditValidateValue (one property, from a tool argument) and
// knowledgeEditValidateAssembledFrontmatter (G1: every property present in
// a freshly-assembled note, including ones that arrived via raw body/template
// bytes rather than the `frontmatter` argument). Keeping the check in exactly
// one place is what "the same sentinel errors and message quality — do not
// invent a second error vocabulary" (G1's brief) means in code: there is
// only one vocabulary because there is only one function that speaks it.
func knowledgeEditValidatePropertyAgainstSchema(schema *records.Schema, typeName, property string, values []string, isList bool) error {
	prop, ok := schema.Property(property)
	if !ok {
		return fmt.Errorf("%w: %s declares no property %q; declared properties are %s",
			ErrUnknownProperty, typeName, property, strings.Join(schema.PropertyNames(), ", "))
	}
	if isList != prop.Many {
		schemaPath := records.VaultMarkerDirName + "/" + records.RecordsDirName + "/" + typeName + ".yaml"
		if isList {
			return fmt.Errorf("%w: %s.%s holds one value; got a list of %d — send a single value, "+
				"or declare many: true in %s",
				ErrPropertyArity, typeName, property, len(values), schemaPath)
		}
		return fmt.Errorf("%w: %s.%s is declared as a list (many: true); got a single value — send a list",
			ErrPropertyArity, typeName, property)
	}
	for i, v := range values {
		node := records.Node{Kind: records.KindScalar, Text: v}
		if _, verr := records.ParseValue(prop, node); verr != nil {
			label := property
			if isList {
				label = fmt.Sprintf("%s[%d]", property, i)
			}
			msg := fmt.Sprintf("%s.%s holds %q, which is not %s", typeName, label, v, verr.Expected)
			if len(verr.Permitted) > 0 {
				msg += "; permitted values are " + strings.Join(verr.Permitted, ", ")
			}
			return fmt.Errorf("%w: %s", ErrPropertyValue, msg)
		}
	}
	return nil
}

// knowledgeEditValidateValue validates an INCOMING write (values, isList) against
// property's declaration in src's own record type, if src declares one that
// resolves in set. It returns nil when there is nothing to violate — no
// declared type, an undeclared type, or a rejected schema file
// (knowledgeEditResolveSchema) — or (by construction of the caller) a
// property the schema does not mention gets its own named refusal below.
//
// values holds exactly one element for a scalar write, N for a list write —
// the SHAPE THE CALLER SENT, which is what an arity mismatch is measured
// against (FR-006/FR-042: "the interesting fact is the shape").
//
// gov, when non-nil, is filled in with WHY nothing was checked whenever
// nothing was (G3) — the tool-result layer reads it back to say so instead
// of returning nil indistinguishably from "validated and fine". Passing nil
// is for callers (e.g. execCreate's per-pair splice loop) that compute their
// own governance note separately, over the fully assembled note, rather than
// per property.
func knowledgeEditValidateValue(set *records.SchemaSet, report *records.SchemaLoadReport, src []byte, property string, values []string, isList bool, gov *knowledgeEditGovernance) error {
	schema, typeName, reason, detail := knowledgeEditResolveSchema(set, report, src)
	if reason != knowledgeEditGoverned {
		if gov != nil {
			*gov = knowledgeEditGovernance{Reason: reason, TypeName: typeName, RejectionReason: detail}
		}
		return nil
	}
	if gov != nil {
		*gov = knowledgeEditGovernance{Reason: knowledgeEditGoverned, TypeName: typeName}
	}
	return knowledgeEditValidatePropertyAgainstSchema(schema, typeName, property, values, isList)
}

// knowledgeEditPropertyDeclared reports whether property is declared AT ALL on
// src's own record type, and — only when it is — whether that declaration
// says many-valued. Used by the link operation to decide whether linking
// through a relation property ADDS to a list or OVERWRITES a scalar.
//
// declared is false whenever nothing constrains this property's cardinality
// at all: no declared `type:`, a declared type with no matching schema
// file (whether never declared or rejected at load time), or a schema that
// does not mention this property. FR-005 calls that state "ordinary notes
// are unconstrained" — and an ordinary note is exactly what most link
// targets are, since a relation is routinely put on a note whose author
// never wrote (or needed) a records/*.yaml for it. When declared is false,
// many is meaningless and always returned false; callers must branch on
// declared first.
//
// Earlier, this reported one bool (knowledgeEditPropertyMany) collapsing
// "explicitly declared single-valued" and "nothing declared at all" into
// the same false — see the correction on knowledgeEditLinkPropertyEdit below
// for why that collapse was itself the defect.
func knowledgeEditPropertyDeclared(set *records.SchemaSet, report *records.SchemaLoadReport, src []byte, property string) (declared, many bool) {
	schema, _, reason, _ := knowledgeEditResolveSchema(set, report, src)
	if reason != knowledgeEditGoverned {
		return false, false
	}
	prop, ok := schema.Property(property)
	if !ok {
		return false, false
	}
	return true, prop.Many
}

// knowledgeEditSetPropertyEdit composes schema validation with the low-level
// splice: a NoteEdit that refuses (leaving src untouched) when the value
// does not conform, and otherwise delegates to the scalar or list splice.
func knowledgeEditSetPropertyEdit(set *records.SchemaSet, report *records.SchemaLoadReport, property string, values []string, isList bool, gov *knowledgeEditGovernance) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := knowledgeEditValidateValue(set, report, src, property, values, isList, gov); err != nil {
			return nil, err
		}
		if isList {
			return SetPropertyList(property, values)(src)
		}
		return SetPropertyScalarChecked(property, values[0])(src)
	}
}

// knowledgeEditListOpEdit composes schema validation with AddListValue /
// RemoveListValue for set_property's list_op mode.
func knowledgeEditListOpEdit(set *records.SchemaSet, report *records.SchemaLoadReport, property, value string, add bool, gov *knowledgeEditGovernance) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := knowledgeEditValidateValue(set, report, src, property, []string{value}, true, gov); err != nil {
			return nil, err
		}
		if add {
			return AddListValue(property, value)(src)
		}
		return RemoveListValue(property, value)(src)
	}
}

// knowledgeEditLinkPropertyEdit composes schema validation with a relation
// write. Its arity decision (ADD vs. SET) is spec-argued in the file
// header's D5 citation ("Cardinality is declared and enforced (many: true
// or not)"): cardinality is a property of the SCHEMA's declaration, and the
// only case where this file has a genuine declaration to enforce is when
// the record's own type resolves to a schema that declares this property.
//
//   - Declared many: true  -> ADD. The schema says this relation holds more
//     than one edge; the tool named "link" adds an edge, per its own
//     description ("link to another note").
//   - Declared many: false -> SET (overwrite, still guarded by
//     SetPropertyScalarChecked's list-shape refusal). The schema declares a
//     single-edge slot on purpose; replacing that one edge is what
//     "enforced" cardinality of one means, and is what op: link's caller
//     asking to link a NEW target to that slot intends.
//   - Not declared at all (no type, an undeclared type, or a schema that
//     never mentions this property) -> ADD, not SET. FR-005's "ordinary
//     notes are unconstrained" describes what the SCHEMA layer permits, not
//     what op: link should assume about the caller's intent — an
//     undeclared property carries no cardinality-of-one guarantee to honour,
//     so treating it as one is not enforcing a declaration, it is inventing
//     one, and the direction that invention took here (overwrite) is the
//     one that can silently destroy a list of existing relations with a
//     tool whose own name and rendered reply ("LINK x -> y") look additive.
//     ADD is the failure-safe default for the undeclared case: adding to an
//     absent key creates a fresh one-item list (AddListValue's own defined
//     behaviour), and AddListValue itself still refuses — rather than
//     silently promotes — an existing SCALAR value, so a genuinely
//     single-valued undeclared property is protected too; only a caller who
//     explicitly wants that conversion (set_property with a list value)
//     performs it.
func knowledgeEditLinkPropertyEdit(set *records.SchemaSet, report *records.SchemaLoadReport, property, wikilink string, gov *knowledgeEditGovernance) NoteEdit {
	return func(src []byte) ([]byte, error) {
		declared, many := knowledgeEditPropertyDeclared(set, report, src, property)
		add := !declared || many
		if err := knowledgeEditValidateValue(set, report, src, property, []string{wikilink}, add, gov); err != nil {
			return nil, err
		}
		if add {
			return AddListValue(property, wikilink)(src)
		}
		return SetPropertyScalarChecked(property, wikilink)(src)
	}
}

// ---------------------------------------------------------------------------
// G1 — validating the WHOLE assembled frontmatter of a note being created,
// not just the `frontmatter` argument map.
// ---------------------------------------------------------------------------

// knowledgeEditValidateAssembledFrontmatter validates EVERY property present
// in a freshly-assembled note's frontmatter — not only the ones that arrived
// through create's `frontmatter` argument, but also anything already baked
// into the raw `body` or an expanded `template` before this runs — against
// the note's own declared record type, through the exact same
// knowledgeEditValidatePropertyAgainstSchema authority set_property and link
// use (G1's brief: "the same sentinel errors and message quality — do not
// invent a second error vocabulary").
//
// Governance is resolved ONCE from content's OWN `type:` (already spliced in
// by the time this runs — from the raw body, the template, or a prior
// `frontmatter.type` pair in the same create call; see execCreate's
// pairs-sorted-type-first ordering), so every property this pass checks is
// measured against that SAME schema. There is no separate "type first" step
// to repeat here: by construction, whichever property named `type` is
// already reflected in content by the time the WHOLE document is re-parsed.
//
// RecordTypeKey/RecordIDKey/RecordIDKeyNamespaced are reserved
// discriminator/identity keys, never ordinary declared properties — this
// mirrors records/validate.go's own exclusion list for the identical reason:
// a schema does not (and must not have to) declare `type` as one of its own
// properties for `type: <itself>` to be legal.
//
// An explicit null (`status:` with nothing after it) is FR-007 absence, not
// a value — skipped, exactly as ParseValue's callers elsewhere never see a
// null node either.
func knowledgeEditValidateAssembledFrontmatter(set *records.SchemaSet, report *records.SchemaLoadReport, content []byte) (knowledgeEditGovernance, error) {
	schema, typeName, reason, detail := knowledgeEditResolveSchema(set, report, content)
	if reason != knowledgeEditGoverned {
		return knowledgeEditGovernance{Reason: reason, TypeName: typeName, RejectionReason: detail}, nil
	}

	// A second parse of the same bytes for a different question ("every
	// property present" rather than "what type does this declare") — the
	// same trade-off knowledgeEditResolveSchema's own doc comment already
	// makes for its callers, kept here for the same reason: it keeps the two
	// questions independent rather than threading one parse's internals
	// through both.
	fm, ferr := records.ParseFrontmatter(content)
	if ferr != nil {
		// Not this function's failure to report, by the same reasoning
		// knowledgeEditResolveSchema's own doc comment gives: an unparsable
		// frontmatter block is reported through the governance reason
		// (knowledgeEditUnparsable), not as an error — the caller's own
		// CreateNote call still writes the note as ordinary content when
		// nothing else refuses it, which is correct: an unparsable
		// frontmatter block is not this layer's problem to solve.
		return knowledgeEditGovernance{Reason: knowledgeEditUnparsable}, nil //nolint:nilerr // reported via the governance reason, not an error
	}

	for _, key := range fm.Keys {
		if key == records.RecordTypeKey || key == records.RecordIDKey || key == records.RecordIDKeyNamespaced {
			continue
		}
		node := fm.Values[key]
		if node.Kind == records.KindNull {
			continue
		}
		var values []string
		isList := node.Kind == records.KindSequence
		switch node.Kind {
		case records.KindScalar:
			values = []string{node.Text}
		case records.KindSequence:
			for i, item := range node.Items {
				if item.Kind != records.KindScalar {
					return knowledgeEditGovernance{}, fmt.Errorf(
						"frontmatter.%s: %w: element %d is %s, not a single value",
						key, ErrPropertyValue, i, item.Kind)
				}
				values = append(values, item.Text)
			}
		default: // records.KindMapping — no property type accepts one
			return knowledgeEditGovernance{}, fmt.Errorf(
				"frontmatter.%s: %w: is a mapping, not a single value or a list",
				key, ErrPropertyValue)
		}
		if err := knowledgeEditValidatePropertyAgainstSchema(schema, typeName, key, values, isList); err != nil {
			return knowledgeEditGovernance{}, fmt.Errorf("frontmatter.%s: %w", key, err)
		}
	}
	return knowledgeEditGovernance{Reason: knowledgeEditGoverned, TypeName: typeName}, nil
}
