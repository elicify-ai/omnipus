// Omnipus — ADR-068 D16.2: the derived properties index.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package propindex is the vault's DERIVED, DISPOSABLE properties index.
//
// ---------------------------------------------------------------------------
// THE ONE RULE THAT GOVERNS EVERY LINE OF THIS PACKAGE
//
//	SQLite NARROWS CANDIDATES. SQLite DECIDES NOTHING.
//
// ADR-068 D16.2b (reversed in revision 7 by operator ruling R-A) and the
// implementing spec's FR-021: filtering, grouping, joining, ordering and
// aggregation are evaluated IN GO, by the comparator that implements §8's
// R-1..R-13, over the candidate set this package returns. This package answers
// exactly four set-membership questions — which record type, which note kind,
// which path prefix, and which child rows belong to a candidate — and hands
// back rows. It never evaluates a comparison, because SQLite's defaults
// contradict ten of the thirteen comparison rules and nine of them silently
// (ADR-068 D16.6). Five of those contradictions were verified by execution:
//
//	'3' > 2                        -> 1     (any text outranks any number)
//	NOT (status = 'done') over NULL -> 0 rows (the guarded form returns 1)
//	ORDER BY over an enum column   -> alphabetical, not declared order
//	'ACME' LIKE '%acme%'           -> 1
//	lower('MÜLLER')                -> 'mÜller'  (folds ASCII ONLY)
//
// The last one is why case-insensitive matching is only DELIVERABLE in Go:
// SQLite here folds no non-ASCII case at all — no ICU, no loadable extension.
//
// The mechanical consequence, enforced by TestQuery_NoComparisonIsDelegatedToSQL
// (spec §7 test 39a / AC-8.10): the SQL this package emits on a read path
// contains no comparison operator, no LIKE, no IN, no GROUP BY, no ORDER BY, no
// aggregate and no COLLATE, outside a closed, named allow-list of narrowing
// predicates. Every statement goes through one chokepoint (sqlgate.go) so a
// recorder can see all of it, and a source-level test fails the build if a
// second path to the driver appears.
//
// ---------------------------------------------------------------------------
// DERIVED AND DISPOSABLE
//
// Notes remain the sole source of truth (ADR-068 D8, D9, FR-020a). Nothing is
// held here that is not reconstructible from Markdown: delete the file, reopen,
// re-index, and every query answers identically. That property is asserted, not
// asserted-about — see TestRebuild_DeleteAndReopenYieldsIdenticalResults.
//
// ---------------------------------------------------------------------------
// PLATFORM (ADR-068 D16.2a)
//
// modernc.org/sqlite cannot build on linux/mipsle, netbsd/*, or freebsd/arm.
// The real implementation carries the same build constraint the repository's
// two existing SQLite consumers carry; propindex_nosqlite.go substitutes on
// those targets and Open REFUSES BY NAME. It never returns an empty index,
// because an empty answer that looks complete is the failure ADR-068 exists to
// remove (D13, FR-020h).
//
// ---------------------------------------------------------------------------
// WRITE ORDERING (ADR-068 D16.5, as re-derived in revision 7)
//
//	SQLite row (with its source_hash) -> bleve document -> manifest entry.
//
// Revision 6 specified bleve first and that made the reachable failure
// UNDETECTABLE: a crash after bleve and before SQLite leaves the SQLite row and
// the manifest both at the old hash, so they compare EQUAL and a stale answer is
// reported complete. IndexNote is the ordering, written as code rather than as a
// paragraph, so a caller cannot get it wrong; ordering_test.go walks every row of
// D16.5's failure-point table.
package propindex
