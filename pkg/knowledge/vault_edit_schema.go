// Omnipus — ADR-068 FR-011/FR-042: refusing a vault_edit write that does not
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

// vaultEditValidateValue validates an INCOMING write (values, isList) against
// property's declaration in src's own record type, if src declares one that
// resolves in set. It returns nil when there is nothing to violate — no
// declared type, an undeclared type, or (by construction of the caller) a
// property the schema does not mention gets its own named refusal below.
//
// values holds exactly one element for a scalar write, N for a list write —
// the SHAPE THE CALLER SENT, which is what an arity mismatch is measured
// against (FR-006/FR-042: "the interesting fact is the shape").
func vaultEditValidateValue(set *records.SchemaSet, src []byte, property string, values []string, isList bool) error {
	fm, ferr := records.ParseFrontmatter(src)
	if ferr != nil {
		// An unparsable frontmatter block is refused by EditNote's own lower
		// layer (fmParse -> ErrFrontmatterUnterminated) before a write ever
		// reaches this validation in practice. Treated as "no schema
		// applies" here rather than compounding an unrelated failure with a
		// second, different-shaped one.
		return nil
	}
	rec := records.Record{Frontmatter: fm}
	typeName := rec.TypeName()
	if typeName == "" {
		return nil
	}
	schema, ok := set.Get(typeName)
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

// vaultEditPropertyMany reports whether property is declared many-valued on
// src's own record type — used by the link operation to decide whether
// linking through a relation property ADDS to a list or OVERWRITES a scalar,
// without the caller having to say which. false (scalar) is the answer for
// every case where nothing is declared, which is the conservative direction:
// overwrite-one is reversible by linking again, while a caller who wanted
// "add" and silently got "overwrite" would lose a relation with no error at
// all.
func vaultEditPropertyMany(set *records.SchemaSet, src []byte, property string) bool {
	fm, ferr := records.ParseFrontmatter(src)
	if ferr != nil {
		return false
	}
	rec := records.Record{Frontmatter: fm}
	typeName := rec.TypeName()
	if typeName == "" {
		return false
	}
	schema, ok := set.Get(typeName)
	if !ok {
		return false
	}
	prop, ok := schema.Property(property)
	if !ok {
		return false
	}
	return prop.Many
}

// vaultEditSetPropertyEdit composes schema validation with the low-level
// splice: a NoteEdit that refuses (leaving src untouched) when the value
// does not conform, and otherwise delegates to the scalar or list splice.
func vaultEditSetPropertyEdit(set *records.SchemaSet, property string, values []string, isList bool) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := vaultEditValidateValue(set, src, property, values, isList); err != nil {
			return nil, err
		}
		if isList {
			return SetPropertyList(property, values)(src)
		}
		return SetPropertyScalarChecked(property, values[0])(src)
	}
}

// vaultEditListOpEdit composes schema validation with AddListValue /
// RemoveListValue for set_property's list_op mode.
func vaultEditListOpEdit(set *records.SchemaSet, property, value string, add bool) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := vaultEditValidateValue(set, src, property, []string{value}, true); err != nil {
			return nil, err
		}
		if add {
			return AddListValue(property, value)(src)
		}
		return RemoveListValue(property, value)(src)
	}
}

// vaultEditLinkPropertyEdit composes schema validation with a relation write:
// ADD when the property is declared many-valued, SET (overwrite) otherwise —
// see vaultEditPropertyMany.
func vaultEditLinkPropertyEdit(set *records.SchemaSet, property, wikilink string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		many := vaultEditPropertyMany(set, src, property)
		if err := vaultEditValidateValue(set, src, property, []string{wikilink}, many); err != nil {
			return nil, err
		}
		if many {
			return AddListValue(property, wikilink)(src)
		}
		return SetPropertyScalarChecked(property, wikilink)(src)
	}
}
