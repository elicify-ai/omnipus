# pkg/knowledge — knowledge base

One product module, five packages: `pkg/knowledge` (engine + agent tools),
`pkg/records` (typed record model, schemas, the `knowledgefind/` retrieval
path), `pkg/vaultimport` (Obsidian-vault importer, Bases translation),
`pkg/vaultprops` (`knowledge_find` wrapper), and `pkg/library` underneath (the
file-explorer surface — has its own file).

## SQLite here is derived and disposable

SQLite backs exactly one thing in this module: the derived properties index
(ADR-068 D16). Delete it and it rebuilds from the notes — nothing lives in
SQLite that is not reconstructible from Markdown. It is not a general
application store; a new feature wanting SQLite storage belongs elsewhere.

## The no-SQLite stub refuses by name, never returns empty

On targets where `modernc.org/sqlite` cannot build, the properties index is a
stub (`pkg/records/propindex_stub_unavailable.go`, gated by
`propindex_stub_available.go`'s build tags; linux/mipsle is the one shipped
case). The stub refuses typed filters, joins, grouping and aggregation BY
NAME — a specific error, not a generic "unavailable" — and never returns an
empty result. An operator on that platform must see the refusal; a silently
empty query reads as "no matches". Keep both properties when touching it.

## Files deliberately left whole

- `knowledge_edit.go` — its own header rules out a further split: unlike
  `knowledge_describe.go` / `knowledge_read.go`, it already imports `pkg/tools`
  transitively through `AuthoringDeps`, so there is no boundary left to
  preserve by splitting.
- `pkg/records/knowledgefind/find.go` — "the retrieval path and its two
  bounds" (ADR-068 D15.3): the cursor-restore/caching logic is intrinsic to
  the one retrieval algorithm, not a second concern.

Do not "helpfully" split either; the split target list in
`docs/internal/architecture/draft-module-map.md` deliberately leaves both.
