// Omnipus — ADR-068 D15.3 / spec 4.1.2 and 4.2: vault_find, the one retrieval path.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package vaultfind implements `vault_find` — plain words, typed filters, saved
// views, relation joins, `kind: task`, `explain`, and `near` with `hops`.
//
// It replaces `knowledge_search`, `knowledge_tasks` and the never-built
// `record_query` / `record_explain`. There is no second retrieval tool, and
// that is a design commitment rather than a tidiness preference: two retrieval
// paths means two completeness stories, two bounds tables and two ways to
// report a stale row, and the second one is always the one nobody maintains.
//
// ---------------------------------------------------------------------------
// THREE THINGS THIS PACKAGE IS ORGANISED AROUND
// ---------------------------------------------------------------------------
//
// 1. SQLITE NARROWS. THE GO COMPARATOR DECIDES. (ADR-068 ruling R-A.)
//
// The only thing this package can ask the store for is a propindex.Selector,
// and a Selector has exactly three fields — RecordType, Kind, PathPrefix. Every
// one is set membership over an indexed column. There is no field on it that
// can hold a property name or a property value, so a typed predicate is
// UNEXPRESSIBLE in SQL here rather than merely unused.
//
// That is the enforcement, and it is worth stating why it was built that way.
// Review round 6 of the ADR found SEVEN surviving SQL-side evaluations in the
// revision whose headline was this ruling. A rule in a comment would have found
// none of them. If you ever find yourself wanting to push a predicate down —
// wanting one more field on Selector — that is the ruling breaking, and the
// correct response is to report it, not to add the field.
//
// 2. THE RESPONSE FORMAT IS AS LOAD-BEARING AS THE RETRIEVAL.
//
// Moving results from inline text to a file collapsed measured agent accuracy
// from 93.1% to 55.2% — as large a swing as replacing the retriever outright.
// So render.go is not a presentation layer bolted on at the end; it is half the
// feature, and its rules are requirements:
//
//   - Compact TEXT to the model, never JSON. The wire type stays contract-
//     defined (Hard Constraint #8); what changes is the projection.
//   - COMPLETENESS FIRST, in the header — a reader must never have to reach the
//     bottom of a table to learn the answer was partial.
//   - Exclusions named INLINE WITH THE FIX: "arr is '50k' where a decimal is
//     required — write 50000", never "3 records excluded".
//   - Borrowed values marked as borrowed, never merged into the row's columns.
//   - Totals state their scope in the same sentence as the number.
//   - End with addressable next actions. In an agentic loop every response is
//     the prompt for the next call.
//
// 3. A TYPE MISMATCH IS NEVER A SILENT EMPTY RESULT.
//
// Every refusal in this package names the remedy in the same string. An unknown
// property is refused listing the declared ones; an unsupported SQL construct is
// refused listing the ten operators and naming the parameter that does the job;
// a build with no properties index refuses BY NAME rather than returning zero
// rows. "No matches" and "you spelled it wrong" are indistinguishable to a
// caller, and the second is far more common.
//
// ---------------------------------------------------------------------------
// WHAT THIS PACKAGE DELIBERATELY DOES NOT OWN
// ---------------------------------------------------------------------------
//
// There is exactly ONE comparison implementation and it is records.Comparator;
// there is exactly ONE matching layer and it is records.PreparedFilter. This
// package calls both and reimplements neither. A local "small comparator just
// for this case" is the false-green shape spec section 8 exists to prevent: the
// verified comparator sits off the query path while an unverified one does the
// real filtering, and the truth table then guarantees the correctness of code
// nobody calls.
//
// Relation EDGE TRAVERSAL for `near`/`hops` is Stage 3's. What lives here is the
// parameter validation, FR-065's third-hop refusal, and the relation-join
// capability gate — all of which really refuse, and none of which is a stub.
package vaultfind
