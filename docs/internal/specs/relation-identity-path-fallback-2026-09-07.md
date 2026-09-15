# Relation identity — path fallback (R2-A)

Status: RATIFIED (founder, 2026-09-07, via the session's decision prompt) → implementing
Date: 2026-09-07
Branch: feat/library-improvements

## The defect (R2-A, found by the round-2 UAT)

Grouping a view by a relation — the founder's "group_by company should resolve"
— returns *"could not resolve … to a record"* for every row, so CRM/deal/any
relation-grouped view shows nothing. This is the deeper root of "bases look
broken"; the E-track data cleanup made bases show rows, but grouping by a
relation still resolved nothing.

**Root cause, traced in code (not the data):**
- `groupKeys` (`pkg/records/knowledgefind/project.go`) buckets a relation value
  by `q.resolve(link)` → an identity string. Empty/!ok ⇒ the value is
  "unresolved" and excluded from every group.
- `q.resolve` is the resolver's `(string,bool)` adapter
  (`pkg/vaultprops/relation_resolver.go` `AsFunc`/`Resolve`). It resolves the
  wikilink to a **file** (`rl.To`, always available on success) and then reads
  `RelatedIdentity.RecordID`, returning `("", false)` whenever
  `!HasIdentity()`.
- `RelatedIdentity.RecordID` is `Record.ID()`, which reads only the `id` /
  `omni_id` frontmatter key and **writes neither**. **0 of the vault's 784
  notes carry either key**, so every target is idless, every relation is
  "unresolved", and grouping produces nothing.

## Decision — the hybrid (founder-ratified)

**An explicit record id wins; the resolved note's path is the fallback.**

The resolver's `(string,bool)` adapter returns:
1. the wikilink resolves to no file (`ok == false`) → `("", false)` — a link to
   a genuinely nonexistent note stays unresolved (unchanged);
2. the target carries an explicit `id`/`omni_id` → that id (unchanged);
3. the target exists but has no explicit id → **`"path:" + <resolved path>`** —
   the note is identified by its location, the same thing wikilinks already
   resolve by.

The `"path:"` prefix namespaces the fallback so it can never collide with a
real record-id value.

### Why this is the right seam
- Wikilinks *already* resolve by path/name; grouping by relation now uses the
  same resolution the rest of the vault uses.
- Two relation values pointing at the same note yield the same `"path:"` key →
  they bucket together; different notes → different keys. Correct grouping and
  correct relation-equality fall out for free.
- It is contained to `AsFunc`/`Resolve`. It does **not** change:
  - `Record.ID()` (still reads only explicit keys),
  - `RelatedIdentity.HasIdentity()` (still "is a typed record with an id" — the
    signal FR-034 "resolves but wrong type" depends on),
  - the properties index `record_id` column (no reindex, no migration),
  - `check_integrity`'s unresolved-relation reporting, which uses
    `ResolveIdentity` directly and still keys on genuine non-existence.

## Gaps this leaves (told to the founder, accepted)

| Gap | Consequence | Mitigation |
|---|---|---|
| Identity moves on rename (identity == location) | a relation to the old name breaks | wikilinks already break on rename unless rewritten; `knowledge_restructure` rewrites them — consistent with today |
| Requires unambiguous names | an ambiguous name can't name one target | the E1 collision cleanup drove this to near-zero; the new ambiguity lint flags any regression |
| No cross-vault / export-stable handle, no dedup | can't reference a record from outside the vault | out of scope; add an explicit `omni_id` to any note that later needs it — the hybrid already prefers it |

The escape hatch is built in: any note that needs rename-stable or external
identity gets an explicit `omni_id`, which wins over the path fallback.

## Blast radius & tests

Change site: `pkg/vaultprops/relation_resolver.go` `AsFunc` + `Resolve` only.

Tests (pkg/vaultprops):
- an idless-but-existing target → identity `"path:<path>"`, not unresolved;
- two links to the same idless note → identical key (bucket together);
- a link to a nonexistent note → still `("", false)`;
- a target with an explicit `id`/`omni_id` → that id wins (path not used).

End-to-end verification: against the live E-fixed vault, a `knowledge_find`
`group_by=company` over contacts now resolves to company buckets instead of
reporting every row unresolved (the founder's exact complaint).
