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

// knowledgeEditResolveSchema resolves src's own declared record type against
// set, when it has one. ok is false whenever there is nothing to validate
// against — src's frontmatter does not parse, declares no `type:`, or
// declares a type set has no schema for — and both callers below (schema
// validation and the link operation's arity lookup) treat that identically:
// FR-005's "ordinary notes are unconstrained".
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
func knowledgeEditResolveSchema(set *records.SchemaSet, src []byte, property string) (schema *records.Schema, typeName string, ok bool) {
	fm, ferr := records.ParseFrontmatter(src)
	if ferr != nil {
		return nil, "", false
	}
	rec := records.Record{Frontmatter: fm}
	typeName = rec.TypeName()
	if typeName == "" {
		return nil, "", false
	}
	schema, ok = set.Get(typeName)
	if !ok {
		return nil, "", false
	}
	return schema, typeName, true
}

// knowledgeEditValidateValue validates an INCOMING write (values, isList) against
// property's declaration in src's own record type, if src declares one that
// resolves in set. It returns nil when there is nothing to violate — no
// declared type, an undeclared type (knowledgeEditResolveSchema), or (by
// construction of the caller) a property the schema does not mention gets
// its own named refusal below.
//
// values holds exactly one element for a scalar write, N for a list write —
// the SHAPE THE CALLER SENT, which is what an arity mismatch is measured
// against (FR-006/FR-042: "the interesting fact is the shape").
func knowledgeEditValidateValue(set *records.SchemaSet, src []byte, property string, values []string, isList bool) error {
	schema, typeName, ok := knowledgeEditResolveSchema(set, src, property)
	if !ok {
		return nil
	}
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

// knowledgeEditPropertyDeclared reports whether property is declared AT ALL on
// src's own record type, and — only when it is — whether that declaration
// says many-valued. Used by the link operation to decide whether linking
// through a relation property ADDS to a list or OVERWRITES a scalar.
//
// declared is false whenever nothing constrains this property's cardinality
// at all: no declared `type:`, a declared type with no matching schema
// file, or a schema that does not mention this property. FR-005 calls that
// state "ordinary notes are unconstrained" — and an ordinary note is
// exactly what most link targets are, since a relation is routinely put on
// a note whose author never wrote (or needed) a records/*.yaml for it. When
// declared is false, many is meaningless and always returned false; callers
// must branch on declared first.
//
// Earlier, this reported one bool (knowledgeEditPropertyMany) collapsing
// "explicitly declared single-valued" and "nothing declared at all" into
// the same false — see the correction on knowledgeEditLinkPropertyEdit below
// for why that collapse was itself the defect.
func knowledgeEditPropertyDeclared(set *records.SchemaSet, src []byte, property string) (declared, many bool) {
	schema, _, ok := knowledgeEditResolveSchema(set, src, property)
	if !ok {
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
func knowledgeEditSetPropertyEdit(set *records.SchemaSet, property string, values []string, isList bool) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := knowledgeEditValidateValue(set, src, property, values, isList); err != nil {
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
func knowledgeEditListOpEdit(set *records.SchemaSet, property, value string, add bool) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := knowledgeEditValidateValue(set, src, property, []string{value}, true); err != nil {
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
func knowledgeEditLinkPropertyEdit(set *records.SchemaSet, property, wikilink string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		declared, many := knowledgeEditPropertyDeclared(set, src, property)
		add := !declared || many
		if err := knowledgeEditValidateValue(set, src, property, []string{wikilink}, add); err != nil {
			return nil, err
		}
		if add {
			return AddListValue(property, wikilink)(src)
		}
		return SetPropertyScalarChecked(property, wikilink)(src)
	}
}
