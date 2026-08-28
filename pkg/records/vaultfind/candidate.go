// Omnipus — ADR-068 D16.2 / spec FR-021b: one narrowed record, decoded once.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultfind

import (
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// candidate is one record the narrowing predicates selected, with a memo of the
// properties already decoded.
//
// The memo exists because a filter tree can name one property in several leaves
// — `{any: [{arr, ">=", 50000}, {arr, "IS NULL"}]}` decodes `arr` twice without
// it — and decoding runs pkg/records' own parser over the stored elements. It is
// a per-candidate map and dies with the candidate, so it holds one record's
// properties at a time and never accumulates: FR-066b's "one record in memory at
// a time" is a property of the STREAM, and a memo that outlived the record would
// quietly break it.
type candidate struct {
	rows  propindex.Candidate
	memo  map[string]records.PropertyValue
	stale bool
}

func newCandidate(c propindex.Candidate) candidate {
	return candidate{rows: c, memo: make(map[string]records.PropertyValue, 4)}
}

// identity is what a problem line names: the record identifier if the note
// carries one, and the path otherwise.
//
// An ordinary note with no identifier is the majority of every real vault
// (FR-005) and is not an error, so falling back to the path is the normal case
// rather than a degraded one.
func (c candidate) identity() string {
	if c.rows.RecordID != "" {
		return c.rows.RecordID
	}
	return c.rows.Path
}

// value decodes one declared property into the form the comparator consumes.
//
// An ABSENT property returns StateAbsent rather than an error, and the
// distinction is the whole of FR-007: absence is a legitimate third state, not a
// failure. It is what makes `{not: {p, "=", v}}` able to include the days nobody
// recorded a value for — precisely the days being asked about.
func (c candidate) value(prop *records.Property) (records.PropertyValue, error) {
	if v, ok := c.memo[prop.Name]; ok {
		return v, nil
	}
	sp, ok := c.rows.Prop(prop.Name)
	if !ok {
		v := records.PropertyValue{Property: prop, State: records.StateAbsent}
		c.memo[prop.Name] = v
		return v, nil
	}
	v, err := sp.Typed(prop)
	if err != nil {
		return records.PropertyValue{}, err
	}
	v.Property = prop
	c.memo[prop.Name] = v
	return v, nil
}

// evidence returns what the note actually held for a non-conforming property and
// the shape that would have been accepted.
//
// Both are empty when the property is conforming or absent, and when the note
// was indexed before the index carried this evidence — so a caller must render
// the fallback rather than an empty pair of quotes.
func (c candidate) evidence(name string) (got, expected string) {
	sp, ok := c.rows.Prop(name)
	if !ok {
		return "", ""
	}
	return sp.Got, sp.Expected
}
