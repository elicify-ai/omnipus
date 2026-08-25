// Omnipus — ADR-068 D1: a record is a note with a declared record type.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "strings"

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// D1: "A record is an ordinary Markdown file with YAML frontmatter, in the
// operator's own vault. There is no separate database." So a Record here is a
// thin pairing of a path (for reporting) and parsed frontmatter — nothing is
// copied into a second store, and nothing is normalised on the way in.
//
// FR-005 is the load-bearing behaviour: a note whose `type` matches no schema
// is an ORDINARY NOTE, without error. That is not an edge case to be tolerated;
// it is the majority of every real vault, and ADR-068 §9's first holdout
// scenario is 500 notes with no schema at all raising nothing.
// ---------------------------------------------------------------------------

const (
	// RecordTypeKey is the frontmatter key that makes a note a record (D1).
	RecordTypeKey = "type"

	// RecordIDKey is where D7's identifier is written. D7's example writes
	// `id: CO-0142` while D8 says fields Omnipus maintains carry an `omni_`
	// prefix — the two are not reconciled in the ADR. This package READS both
	// (`id` first, then `omni_id`) and WRITES neither: minting identifiers is
	// D7.1's allocator, outside this package. The ambiguity is recorded rather
	// than silently resolved, because picking one here would make this package
	// the de-facto decision.
	RecordIDKey = "id"

	// RecordIDKeyNamespaced is the D8-prefixed spelling, accepted on read.
	RecordIDKeyNamespaced = "omni_id"
)

// Record is one note, parsed far enough to validate.
type Record struct {
	// Path identifies the note in every report. It is caller-supplied and is
	// never interpreted — this package does no filesystem walking.
	Path string

	// Frontmatter is the note's lexical frontmatter (frontmatter.go).
	Frontmatter Frontmatter

	// ParseError is set when the frontmatter could not be read at all. It is a
	// string rather than an error so a Record stays comparable and printable in
	// a report; validation turns it into a finding.
	ParseError string
}

// ParseRecord reads a note's bytes into a Record.
//
// It never returns an error: a note that cannot be parsed is still a note that
// must appear in a report by name (FR-026's "named, with the reason"), and
// swallowing it into an error return is how a record goes missing from an
// answer that then claims to be complete.
func ParseRecord(path string, src []byte) Record {
	r := Record{Path: path}
	fm, err := ParseFrontmatter(src)
	r.Frontmatter = fm
	if err != nil {
		r.ParseError = err.Error()
	}
	return r
}

// TypeName returns the declared record type, or "" if the note declares none.
//
// A list-valued or empty `type` returns "" — the note is then an ordinary note
// (FR-005), because "this note is two record types at once" is not a statement
// this package can act on and is not an error the operator asked for.
func (r Record) TypeName() string {
	n, ok := r.Frontmatter.Get(RecordTypeKey)
	if !ok || n.Kind != KindScalar {
		return ""
	}
	return strings.TrimSpace(n.Text)
}

// ID returns the record's identifier if one is written, checking both
// spellings named on RecordIDKey.
func (r Record) ID() string {
	for _, key := range []string{RecordIDKey, RecordIDKeyNamespaced} {
		if n, ok := r.Frontmatter.Get(key); ok && n.Kind == KindScalar {
			if v := strings.TrimSpace(n.Text); v != "" {
				return v
			}
		}
	}
	return ""
}

// IsRecord reports whether this note is a record of a type the set declares.
//
// This is FR-005's decision point, and it is deliberately a plain boolean with
// no error: "not a record" is a normal, silent, correct outcome.
func (r Record) IsRecord(set *SchemaSet) bool {
	t := r.TypeName()
	if t == "" {
		return false
	}
	_, ok := set.Get(t)
	return ok
}
