// Omnipus — the Obsidian-vault importer (spec FR-100..FR-103).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package vaultimport is the one-shot operator/CLI importer that bootstraps
// a vault's ADR-068 records control plane (.omnipus-vault/records/,
// .omnipus-vault/views/) from what is ALREADY on disk in an Obsidian vault:
// the frontmatter of its notes (record-type inference, HALF 1) and its
// `.base` files (saved-view translation, HALF 2).
//
// FR-100 (spec docs/internal/specs/vault-records-spec-2026-08-25.md,
// revision 3, ADR-068 D15.4): this is an operator/CLI one-shot, never an
// agent tool. FR-102: `.base` files are read here, once, and NEVER on the
// query path — pkg/records' own view loader never opens a `.base` file.
// FR-103: nothing in this package is registered as a tool or holds a
// tool-policy entry; cmd/omnipus/internal/records is its only caller.
//
// This package depends on pkg/records for EVERY parse/validate primitive
// (ParseFrontmatter via records.Record, ParseValue, ParseWikilink,
// ParseSchema, ParseView, Validate) and on pkg/knowledge only for note
// discovery (Scan) and eviction-aware content reads (ReadNoteContent). It
// is deliberately NOT a second parser or a second validator — see each
// file's header for exactly which pkg/records primitive it reuses.
//
// THE HONESTY CONTRACT (the reason this package exists in the shape it
// does): every property this package cannot classify without guessing is
// reported as an AMBIGUOUS INFERENCE rather than silently resolved, and
// every `.base` filter expression this package cannot translate is
// preserved VERBATIM in a report and in the produced view's
// `untranslated` field — never dropped in silence, never approximated.
// See Report in report.go for the shape that contract takes on disk.
package vaultimport
