// Omnipus — a lookup over the inferred schemas, for the Base translator.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

// SchemaIndex answers "does this record type declare this property, and as
// what" during Base translation — the same question
// records.ValidateViewAgainstSchemas asks against a *records.SchemaSet, but
// asked here BEFORE a view is written, so a bad reference can be dropped and
// reported as a named loss instead of rejecting the whole produced file.
type SchemaIndex struct {
	byType map[string]map[string]InferredProperty
}

// NewSchemaIndex builds an index from InferSchema's output.
func NewSchemaIndex(inferred map[string][]InferredProperty) *SchemaIndex {
	si := &SchemaIndex{byType: map[string]map[string]InferredProperty{}}
	for t, props := range inferred {
		m := map[string]InferredProperty{}
		for _, p := range props {
			m[p.Name] = p
		}
		si.byType[t] = m
	}
	return si
}

// HasType reports whether a record type has an inferred schema at all.
func (si *SchemaIndex) HasType(recordType string) bool {
	_, ok := si.byType[recordType]
	return ok
}

// Lookup returns one property's inferred declaration, scoped to its record
// type — the same FR-009 scoping the real schema enforces.
func (si *SchemaIndex) Lookup(recordType, property string) (InferredProperty, bool) {
	m, ok := si.byType[recordType]
	if !ok {
		return InferredProperty{}, false
	}
	p, ok := m[property]
	return p, ok
}
