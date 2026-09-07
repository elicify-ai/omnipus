// Omnipus — ADR-068: the vault records typed-record layer.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package records is the type system and schema validator for vault records
// (ADR-068, spec docs/internal/specs/vault-records-spec-2026-08-25.md, FR-001..FR-015).
//
// ---------------------------------------------------------------------------
// WHAT THIS PACKAGE DOES NOT DO — ADR-068 D0, and it is not negotiable
//
// Omnipus ships MECHANISM. The vault ships CONVENTION. This package therefore
// declares NO record types of its own — no company, no contact, no deal, no
// interaction, not even as an overridable default. Every record type in
// existence comes from a schema file the operator wrote in their own vault.
//
// The reason is stated in D0: a shipped default becomes the de-facto standard.
// It stops being questioned, and our idea of what a "contact" is leaks into
// every vault that never got round to editing it. Vaults differ enormously;
// that is the normal case, not the edge case.
//
// A consequence worth stating for anyone adding to this package: the EIGHT
// PROPERTY TYPES (text, enum, relation, date, integer, decimal, person,
// checkbox) are mechanism and are closed (FR-004, as amended by FR-004c /
// ADR-068 D24.5 — the count was seven through spec Draft 9 and `checkbox`
// joined in Draft 11; do not "correct" it back). The `person` type in particular does NOT
// imply a built-in person record type — it is a relation whose target type is
// whatever the vault's own schema names in `to:`, and with no `to:` only the
// link SHAPE is validated. If you find yourself wanting to hardcode a type
// name here, D0 says the answer is a schema file, not Go code.
//
// ---------------------------------------------------------------------------
// WHAT THIS PACKAGE DELIBERATELY HAS NO DEPENDENCY ON
//
// Storage. There is no index, no bleve, no filesystem walk, no pkg/knowledge
// import. Schema loading reads schema FILES (FR-001 names a path), and record
// parsing takes note BYTES, but nothing here knows how notes are found, cached
// or indexed. That keeps the type system independently testable, and keeps the
// exact-decimal promise (FR-013, FR-020b) auditable in one place instead of
// spread across a retrieval path.
//
// ---------------------------------------------------------------------------
// THE FLOAT64 RULE — read before adding any numeric code here
//
// FR-013 and FR-020b: a `decimal` is exact and must never become a binary
// floating-point number ANYWHERE in the path, and an `integer` is a bounded
// int64 that is refused rather than widened. That rule is why this package
// parses frontmatter through yaml.Node and keeps every numeric value in its
// LEXICAL form (see frontmatter.go) rather than unmarshalling into `any`.
// Unmarshalling `349.98` into an `any` produces a float64 before any code of
// ours gets a say, and 349.98 is not representable in binary floating point.
// `integer` is held to the same standard, because FR/DS-1 requires 2^53+1 to
// survive exactly and a float64 cannot represent it.
//
// The mechanical guard is decimal_no_float_test.go. It does NOT grep — this
// line used to say it did, and named a file that did not exist, which is the
// worse of the two errors: it asserted an enforcement a reader could not find.
// The guard parses every .go file in this package with go/ast (comments off,
// so prose like the paragraph above is not an offence) and fails on three
// things: an identifier naming a binary float type, an identifier containing
// "Float" (big.NewFloat, strconv.ParseFloat, SetFloat64, ...), and an untyped
// floating-point literal such as `x := 349.98`. The last two were added after
// a review found the identifier-only version would pass a package containing
// exactly `big.NewFloat(349.98)`.
package records
