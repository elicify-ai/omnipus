# ADR-068 — Vault records: a typed record layer with relations

- **Status:** Proposed (2026-08-25) — **revision 4**, after three adversarial reviews (BLOCK each time: 7, 8, then 10 critical). **D16 is deliberately unresolved and gated behind a measured spike** — see D16 for why three attempts to settle it on paper each rested on an unverified property of existing code, after adversarial review (`ADR-068-…-review.md`, verdict BLOCK: 7 critical, 25 major, 13 minor, 5 observations). Revision 1 made three false claims about existing code (D14, D16, D18); all three are corrected in place and the corrections are marked.
- **Implements:** founder direction 2026-08-24 ("we need something similar to bases for master data like our CRM"); explicit decision **not** to pursue Obsidian `.base` compatibility
- **Builds on:** [ADR-067](ADR-067-omnipus-knowledge-base-and-render-first-preview.md) (knowledge base, index, tools, preview), [ADR-063](ADR-063-unified-file-access-engine-and-mounts.md) (mounts), [ADR-037](ADR-037-remove-global-delegation-policy.md) (workspace = trust boundary), [ADR-054](ADR-054-entity-config-separation.md) §5 (flock platform limits)
- **Supersedes in intent:** the `.base`-evaluator direction explored on 2026-08-24 and abandoned — see §3.1
- **Research basis:** `../design/omnipus-vault-requirements-2026-08-24.md`; four research passes over Notion's template gallery/help/customer stories, Obsidian help/forum/plugin sources, and a decompiled Obsidian 1.12.7 binary
- **Branch:** `feat/library-improvements`

---

## 1. Context

### 1.1 What people actually build with these systems

ADR-067 gives the vault retrieval: search, a link graph, and a reader. It does not give it
**records** — a company with an owner and a status, a deal that belongs to that company, a
meeting that references both.

That is what Obsidian Bases and Notion databases are used for. Research across both product
ecosystems put the distribution beyond doubt:

- In a survey of Notion's own first-party templates across five record use cases, **numeric
  aggregation (`Sum`) is configured in exactly one** — CRM deal values. Charts cannot display
  rollups at all, cap at 200 groups, and are limited to one on the free plan.
- In an applicant-tracking template, the property-type mix is `select` 13, `relation` 8,
  `date` 7, `person` 6, `rollup` 5, `number` 5 — record-management types outnumber numeric
  types roughly **3.4 : 1**.
- Notion's own documentation teaches the aggregation footer in a single sentence, as a
  consequence of filtering, while grouping and filtering each get dedicated multi-page guides.

**A vault record layer is a record system. Its failures are type failures, not arithmetic
failures.** An earlier draft of this design led with numeric correctness and was wrong in
emphasis; this ADR corrects it.

### 1.2 What "no relational layer" costs, measured

The clearest artifact in the research is a published Obsidian CRM template answering one
ordinary two-hop question — *all interactions with anyone at this company*. It requires:

1. a **hand-maintained** `people: []` array on the company note, which drifts silently;
2. a **JavaScript loop**, because the query language cannot express the hop;
3. `path.includes()` — **a string comparison standing in for identity**;
4. a **raw file read** that bypasses the metadata index entirely;
5. a **regular expression over Markdown headings**, because the interaction history is prose.

Obsidian Bases has no relation, lookup or rollup column (6 column types against Notion's 17);
the gap is an open feature request. Across the entire corpus **no published example answers a
cross-entity CRM question natively in Bases** — every published answer is Dataview,
DataviewJS, or a third-party plugin.

### 1.3 The failure mode is silence, in both products

| Product | Failure |
|---|---|
| Obsidian | A property silently becomes a list when a second value is added; every query written against it then returns **nothing**, with no error. The most-cited debugging experience in the corpus, whose accepted community advice is *"keep testing until something is returned."* |
| Obsidian | A checkbox has **three** states — the Obsidian team's own words: *"There is in fact a third state 'indeterminate'."* Negative filters drop the empty ones, so "records where X did **not** happen" omits exactly the records asked about. |
| Obsidian | Grouping by a multi-value property produces **one group named after the concatenation** — `[Finance, Business]` becomes a group literally called "Finance Business". Confirmed intentional by the Obsidian team. |
| Notion | Relation reads return 25 items, writes replace the whole list, and there is no append. The ordinary read-modify-write pattern **deletes items 26..N and returns `200 OK`**. |
| Notion | *"a loop that just follows `next_cursor` until `has_more` is false looks like it finished, but on a 25,000-row database it quietly returns 10,000 rows."* — Notion's own documentation. |
| Notion | A first-party CRM template ships two differently-labelled rollups (`Won Deals Value`, `Total Pipeline Value`) with **identical configurations and no stage filter** — two figures on one dashboard that are silently the same number. |

For an agent this class of defect is worse than for a human: it will act on the result and
narrate it confidently. **A wrong answer costs more than no answer.**

### 1.4 Users are hand-rolling the missing types

Two independent studies found the same workaround: ordinal enums encoded as string prefixes
so that alphabetical sort accidentally produces the intended order —
`CRM_status: 1-Pending connect … 7-DoNotContact`, and `priority: 1-urgent / 2-high /
3-medium`. The second is published as official guidance by a task plugin, with the reason
stated plainly: *"Obsidian's Bases plugin sorts priorities alphabetically by their Value."*

The corpus contains eight distinct workarounds for the absent Select type and five for the
absent relation type. Every one carries a documented cost.

### 1.5 Scope boundary with ADR-067

ADR-067 owns: the index engine, full-text search, the link graph, the note reader, preview,
and the `knowledge_*` tools. **This ADR owns: record types, the property type system,
relations as typed edges, views over records, and the nine `record_*` tools.** Where they meet —
the index — records are stored fields in ADR-067's bleve index, with aggregation
evaluated in Go; there is no second store (D16).

---

## 2. Decision

### D0 — We ship mechanism. The vault ships convention. Agents are taught it by a skill.

**Decided 2026-08-25.** Omnipus provides the type system, validation, relations, queries and
writes. It provides **no record types of its own** — no built-in `company`, `contact`, `deal`
or `interaction`, not even as overridable defaults.

What a vault contains, what its record types are called, where their files live, and how
interactions are recorded are **that vault's conventions**, declared in its own schema files
and taught to agents through a skill.

**Why no defaults, not even overridable ones:** a shipped default becomes the de-facto
standard. It stops being questioned, our idea of "contact" leaks into every vault that never
edits it, and the product quietly acquires opinions about the operator's business that it has
no basis for. Vaults differ enormously; that is the normal case, not the edge case.

**What this constrains elsewhere in this ADR:**

- D17's interaction history specifies **how a mention is interpreted** (a correctness rule),
  never **where an interaction is stored** (a convention). `record_log` writes a record of a
  vault-declared type; it does not impose a shape.
- No decision here may name a specific record type as though the product knows about it.
  Examples in this document are illustrative of the mechanism only.
- The mechanism must be complete enough that a skill can describe any reasonable convention
  without asking us to change code.

### D1 — A record is a note with a declared record type

A record is an ordinary Markdown file with YAML frontmatter, in the operator's own vault.
There is no separate database. A note becomes a record by declaring its type:

```yaml
---
type: company
name: Acme Ltd
---
```

`type` names a **record type** declared in the vault's schema (D2). A note with no `type`, or
an unrecognised one, is an ordinary note: readable, searchable via ADR-067, and simply not a
record. This is not an error and is never reported as one.

**Why:** every published vault CRM already does this — `type: person`, `crm: deal`,
`categories: ["[[Contacts]]"]`, or a folder convention standing in for it. Making the
discriminator explicit costs nothing and is the only prerequisite for validation.

### D2 — The schema is declared, versioned, and lives in git

Record types are declared in `.omnipus-vault/records/<type>.yaml` inside the vault:

```yaml
schema_version: 1
type: company
label: Company
identity:
  prefix: CO            # yields CO-0142
properties:
  name:        { type: text,   required: true }
  status:      { type: enum,   values: [prospect, active, dormant, churned], required: true }
  segment:     { type: enum,   values: [vendor, customer, partner], many: true }
  owner:       { type: person }
  website:     { type: text }
  arr:         { type: money }
```

**Why a file and not a UI:** Notion added schema audit logging in 2026 — Enterprise-only,
365-day retention, and it does **not capture previous values**. A plain file is diffable by a
human reviewing an agent's change, and *where the vault is itself a git repo* the history is
free and complete. **That condition is the operator's, not ours** — many vaults are not git
repos, so this ADR claims reviewability, not versioning.

`schema_version` is mandatory from the first release. Obsidian's `.base` format broke in
**five consecutive releases across eight weeks**, two of them unannounced. Machine-generated
schemas make that failure worse, not better.

### D3 — Seven property types, each closing a documented failure

| Type | Stores | Closes |
|---|---|---|
| `text` | prose; never validated, never queried for equality | — |
| `enum` | one of a declared, **ordered** value set | the `1-Pending…7-DoNotContact` prefix hack (§1.4) |
| `relation` | a typed edge to another record (D5) | the five-step workaround (§1.2) |
| `date` | a day or an instant, comparable | `last_contacted` stored as text, silently unmatchable |
| `number` | a quantity, with `unit` declared as metadata rather than glued into the property name | `exercise: 60 minutes` — text to one engine, duration to another, sortable in neither |
| `money` | amount + ISO-4217 currency + scale, as **one** value | two loose fields nothing keeps together, over binary floats |
| `person` | a relation to a person record, distinct from a name typed as text | the same concept modelled both ways in one vault |

Every property additionally declares three things whose absence is what actually breaks
queries:

**D3.1 — Arity is declared.** `many: true` or absent. A scalar property never silently
becomes a list. Writing a list to a scalar property is rejected with the expected shape named.

*Why:* today the editor converts a scalar to a list the instant a second value is added, and
every query written against it returns nothing, with no error. This is the single
most-reported failure in the corpus.

**D3.2 — Absent is a distinct state from every value.** A query may filter on `is absent`,
and a negative filter (`status is not done`) **includes** records where the property is
absent, unless the query says otherwise.

*Why:* the checkbox third state. "Days I did not meditate" currently omits every day with no
value — precisely the days being asked about.

**D3.3 — Types are scoped to their record type.** `status` on a `company` and `status` on a
`task` are unrelated declarations.

*Why:* Obsidian binds a property type to a property **name, vault-wide**, which is why real
vaults carry `prm-tier`, `habits-reading`, `health_sleep_total_minutes`. Namespacing to work
around a tool limitation is not a convention worth inheriting.

### D4 — Enums are closed and ordered; ordering is data, not spelling

An enum declares its values in order. Sorting an enum column sorts by declared position.
Writing a value outside the set is **rejected**, with the permitted values named in the error.

**Why closed:** an agent writing `Won` where the schema says `won` must be corrected, not
silently create a second de-facto value. Notion's multi-select auto-creates an option on any
typo; the observable result in real vaults is `Won`, `won` and `Closed Won` in one column.

**Why ordered:** because otherwise operators encode order into strings, and it leaks into
every rendering.

Enums carry an optional `group` (`open` / `done` / `cancelled`) so that "is this finished?"
is answerable across record types with different vocabularies.

### D5 — A relation is one edge, stored once; the inverse is derived

A relation is declared on one side and stored on one side:

```yaml
# deal.yaml
company: { type: relation, to: company, inverse: deals }
```

`company.deals` then exists and is **never stored**. It is computed from the index.

**Why:** Notion's engineering, quoted publicly: *"we store relations as lists of page IDs, on
the relation property itself, so if you have a single record that's related to 10,000 other
records, that can have adverse effects."* That single implementation choice explains their
10,000-reference cap, their sync lag, and the silent divergence users repair by toggling the
relation off and on again. The DukeWood template's hand-maintained `people: []` is the same
mistake made by hand, and it drifts the first time anyone forgets.

Relations reference **record identity** (D7), not display text, so a rename cannot break one
and cannot fork a group. A relation whose target does not exist is **reported by
`record_validate`**, never silently rendered as a distinct group of one.

**Cardinality is declared and enforced** (`many: true` or not). Notion's relations are
many-to-many at the engine level with one-to-many available only as a naming convention.

**D5.1 — What is actually stored in the file, and what the index joins on.** Revision 1 left
this undefined and D5 contradicted D7 (identity vs wikilink). Resolved:

- **On disk: a quoted wikilink** — `company: "[[Acme Ltd]]"`. Human-editable, renders and
  navigates in Obsidian, and carries no Omnipus-specific encoding. This is what D8's
  no-lock-in promise requires: remove Omnipus and the relation is still a working link.
- **In the index: the target's record ID**, resolved at index time by following the wikilink
  to a file and reading its `id`.
- **A rename is safe from both directions.** ADR-067 D10 already rewrites inbound wikilinks on
  rename, so the on-disk form stays correct; and the ID is stable, so the index does not
  care whether the rewrite has happened yet.
- **An unresolvable or ambiguous wikilink is a validation finding**, named by
  `record_validate` with the offending record and the reason. It is never silently treated as
  a distinct group of one — the failure §1.3 records for broken links today.
- **A relation to a file that exists but is not a record of the declared target type** is
  likewise a finding, not a silent drop.

### D6 — The flat case is first-class: a record type need not have relations

A record type may model a related concept as an `enum` rather than a `relation`, and this is
a supported design, not a degraded one.

**Why this needs saying:** Notion's own first-party applicant-tracking guidance is **one
database with zero relations** — the role is a Select, not a link to a Roles table. The reason
is a product limitation they state in their own FAQ: *"Any way to group by a relation or
formula property? **Not currently 😓**"* A board cannot group by a relation, and the board is
the view people work in, so they flatten deliberately.

Two consequences, both binding:

1. A system that assumes every conceptual link should be a relation would model hiring wrong,
   in exactly the way Notion deliberately does not.
2. **We do not inherit the constraint that caused it.** Grouping by a relation is supported
   (D10), which removes the reason to denormalise in the first place.

### D7 — Identity is a stable ID, not the filename

Every record carries an immutable identifier, minted on creation and written to frontmatter:

```yaml
id: CO-0142
```

Prefix comes from the schema; the sequence is monotonic per type and **never reused**, so ID
count is not record count — this is stated because Notion's equivalent shares the property and
operators are surprised by it.

**Why a prefixed sequence rather than a bare UUID:** both an agent and a human can quote
`DEAL-142` in prose, in a commit message, and in a conversation. A UUID cannot be said aloud.

**Why not the filename:** across the research, filename *is* identity, which is why renaming a
company breaks every reference to it, and why `[[Acme Corp]]` and `[[Acme Corp.]]` group
separately forever. Wikilinks remain the human-facing way to reference a record and continue
to work (D5.1); the ID is what the index joins on.

**D7.1 — The allocator.** Revision 1 named an ID format and no mechanism to mint one, which
matters because the ID is the join key: a collision silently merges two records.

- The next sequence value per record type lives in `.omnipus-vault/records/.seq`, a small JSON
  map `{type: last_allocated}`.
- Allocation takes the **same lock discipline `pkg/entity` uses and proves**: a 64-shard
  in-process striped mutex plus a sidecar-file `fileutil.WithFlock` for cross-process mutual
  exclusion. `pkg/entity/store_crossprocess_test.go` demonstrates that pattern by re-execing
  the test binary as real OS processes; this reuses it rather than inventing a second one.
- **On Windows the cross-process half is a documented no-op, and this is ACCEPTED, not
  mitigated.** (`fileutil/flock_windows.go`),
  exactly as recorded in ADR-054 §5 for the whole file-store family. Two Windows gateway
  processes against one vault can therefore collide. This is inherited from ADR-054's audit of six
  existing stores, not introduced here. **The reconcile does not fix it** — reconcile heals a
  *lost counter*; it cannot un-write two records that already share an identifier.
  What we ship instead is honest detection: `record_validate` reports duplicate identifiers,
  names both files, and refuses to choose between them. The exposure window is between the
  collision and the next validation run, and it is documented rather than papered over.
- **On open, the counter is raised to at least `max(existing id for that type)`, never
  lowered.** US-5.4's "resume above max" is this rule, not the reconcile-to-max it replaced. It is a high-water mark. Revision 2 said "reconciled to max", which **guarantees
  reuse**: delete the highest-numbered record and the next allocation takes its identifier, so
  a relation written before the deletion silently resolves to a different record. That is the
  dangling-reference failure this ADR exists to prevent, introduced by its own allocator.
- **The high-water mark is persisted and survives deletion.** If the file is lost, the
  reconcile floor is the max over existing records, and the vault is flagged so an operator
  knows identifiers may have been skipped.
- **Gaps in the sequence are expected and acceptable; reuse is not.** Deleting a record burns
  its identifier permanently. Any acceptance criterion demanding "zero gaps" contradicts this
  and is wrong — AC-7.1 is restated below.
- **A duplicate ID is a hard validation error**, reported by `record_validate` with both
  offending paths. It is never resolved by silently preferring one.

**AC-7.1** — 1,000 records created concurrently across two OS processes on POSIX yield 1,000
**distinct** identifiers. Gaps are permitted; a repeat is a failure.

**AC-7.2** — deleting the highest-numbered record and creating a new one yields an identifier
**above** the deleted one, never equal to it.

### D8 — System fields are namespaced; the operator's fields are not

Fields that are **meaningless without Omnipus** carry an `omni_` prefix — index timestamps,
content hashes, internal bookkeeping. Fields that remain useful if Omnipus is uninstalled carry
**no prefix** and are never renamed by us.

**The record identifier is unprefixed: `id`, not `omni_id`.** *(Ruling 2026-08-25, resolving a
contradiction between this decision and D7 that the type-system agent surfaced.)* We mint it,
but it is not our bookkeeping — it is a business identifier the operator quotes in prose, in a
commit message and out loud (D7's whole argument for `DEAL-142` over a UUID). It stays
meaningful with Omnipus removed, so by the test above it is the operator's field, not ours.

The prefix rule is therefore **"would this survive us?"**, not "did we write it?".

**Why:** taken directly from the best-reasoned artifact in the research — a plugin that
prefixes its control fields `prm-` while deliberately leaving `email`, `phone` and `company`
unprefixed, with the reason documented: *"deliberately un-prefixed, since they're useful to
Dataview and to you independently of this plugin."*

This is the concrete answer to lock-in, and it matters more when an agent is doing the
writing: **if Omnipus is uninstalled, the vault is still a working set of notes.**

### D9 — Derived values are computed, never stored

Counts, sums, last-interaction dates, "days since", relation inverses: all computed at query
time from the index, cached with an invalidation key, and **never written into frontmatter**.

**Why:** every hand-maintained derived field found in the research is wrong in somebody's
vault right now — `last_contacted`, `total_interactions`, `open_action_items`,
`relationship_strength`, `people[]`. One published template maintains three denormalised
counters whose documented upkeep procedure is *"update them by hand… or use a small
Dataviewjs block… that counts checked/unchecked boxes under the relevant heading and writes
back to the frontmatter."* That is a hand-maintained materialised view over prose.

An agent reading frontmatter cannot distinguish a stale derived value from a fact. So we do
not put one there.

### D10 — Views are saved queries, stored as data

A view is a declarative file (`.omnipus-vault/views/<name>.yaml`) naming a record type, filters,
grouping, sort, and visible properties. Views are data an agent can author and a human can
diff.

Grouping supports:

- **two levels** — because a published CRM design specified "group by company, then
  jurisdiction" and shipped with one level, the second being inexpressible;
- **grouping by a relation** — see D6;
- **multi-value grouping**, where a record with `segment: [vendor, partner]` appears under
  **both** groups.

The last is a deliberate departure. Obsidian's behaviour — one group named "Finance Business"
— is confirmed intentional by its authors and is useless for the categorisation case it
appears in.

### D11 — Repeated sub-records — **DEFERRED, not in scope**

> **Descoped 2026-08-25 after round-2 review.** This decision was specified here but carried
> no functional requirement, no test and no owner in the implementing spec, while the spec's
> edge-case table already contracted behaviour for it — a promise with nothing behind it.
> It also depends on a frontmatter writer that does not exist (see D14). It is removed from
> scope rather than left as an unbacked commitment, and needs its own ADR if wanted.

### D12 — Time-bounded facts — **DEFERRED, not in scope**

> **Descoped 2026-08-25 after round-2 review.** This decision was specified here but carried
> no functional requirement, no test and no owner in the implementing spec, while the spec's
> edge-case table already contracted behaviour for it — a promise with nothing behind it.
> It also depends on a frontmatter writer that does not exist (see D14). It is removed from
> scope rather than left as an unbacked commitment, and needs its own ADR if wanted.

### D13 — Every query answer is complete, or names what it excluded

`record_query` returns records **and** a problem report in the same response. There is no call
shape that returns records alone.

```json
{
  "records": [ … ],
  "complete": false,
  "problems": [{
    "records": ["CO-0052"],
    "reason":  "company is text, not a relation — cannot be resolved",
    "fix":     "expected a link to a company record"
  }]
}
```

A query that excludes records because they fail their declared type **names them, with the
reason and the expected shape**. A total spanning several currencies is refused, with the
currencies listed, rather than summed (D3, `money`).

**Why:** this is the inverse of §1.3, and it is the requirement the whole ADR exists to serve.
The community's accepted debugging advice today is *"keep testing until something is
returned."* That is what a system with no error channel forces on people.

### D14 — Writes preserve the file

A write modifies the values it was asked to modify. Comments, key order, blank lines, quoting
style and every untouched byte survive.

**Why:** agents will edit records constantly, and the vault is simultaneously a human's
working notes. A writer that re-serialises YAML destroys comments and ordering on every touch,
degrading the vault a little at a time until the operator stops trusting it.

**This is not new work — it already exists on this branch.** `pkg/knowledge/author.go`
implements exactly this, and states the rule in its own header: *"AN EDIT IS A SPLICE, NEVER
A RE-SERIALISATION."* It uses no YAML library for the write path. Revision 1 called this
"genuinely bespoke" unsolved work and proposed adding `goccy/go-yaml` — a dependency that is
**not in `go.mod`** and would have been an unflagged new runtime dependency against Hard
Constraint #1. Both claims are withdrawn.

**Reuse is partial, and the gap is specified rather than assumed.** `author.go` exposes
`SetProperty(key, value string)` — **scalar strings only**, and `authorValidatePropertyKey`
rejects a key containing a colon or a line break. There is **no list writer and no nested-map
writer**. Relations with `many: true` need one, so this ADR adds:

- a list-valued splice, preserving the source's existing list style (block or flow);
- a guard that **refuses to overwrite a multi-line value with a scalar**. The existing
  key-splice removes continuation lines, so a scalar write over a multi-line list would
  silently delete it. That refusal is a hard requirement, not a nicety.

**AC-14.2** — a scalar write targeting a key whose current value spans multiple lines is
refused, and the file is unmodified.

**AC-14.1** — writing a property and reading the file back yields bytes identical to the
original outside the patched span, over a fixture corpus including comments, blank lines,
quoted and unquoted scalars, and nested sub-records.

### D15 — The agent tool surface

Nine tools. All are `record_*` and all are contract-first (D19).

(Revision 2 said "eight" while O-1's resolution added `record_view_import` — corrected.)

| Tool | Does | Notes |
|---|---|---|
| `record_schema` | returns record types, properties, types, enum values, required fields | **compact text, not JSON.** Notion measured a ~91% context-token reduction moving their AI surface from JSON schema objects to a compact textual schema |
| `record_query` | filter, group, follow relations; returns records + problem report | D13 |
| `record_explain` | what a query *would* return, and which properties it could not evaluate — without running it | the cheap defence against a confident wrong answer |
| `record_write` | typed, validated write; file preserved | D14 |
| `record_relate` | `add` / `remove` / `replace` as **three distinct verbs** | `replace` must be named explicitly. Notion offers only replace, so the ordinary read-then-write pattern deletes relations and returns success |
| `record_log` | record that an interaction happened, with type, date and the records involved | D17 |
| `record_view_write` | create or edit a saved view; rejected if it names a property or enum value that does not exist | |
| `record_validate` | run the schema across records; report failures per record with reasons | turns "the view looks wrong" into a clearable worklist |
| `record_view_import` | one-shot translation of a `.base` file; reports every expression it could not translate, verbatim | O-1. Not a query path — see O-1's resolution |

### D15.1 — Scope, bounds and refusal (closing ADR-067's C-4 for records)

ADR-067's own review was blocked partly because multi-vault search crossed the workspace
isolation boundary; it closed that with D7's workspace scoping and a negative test, now
implemented as `pkg/knowledge/scope.go` (`Scope.WorkspaceID`, `.Roots`, `.Contains`,
`.Truncated`). Records are a **stronger** read primitive — typed fields, relations, and
aggregation across them — so the same boundary applies with no exception.

**D15.1a — Workspace scoping.** Every record tool resolves through the calling agent's
workspace: agent → workspace → `AllowedMountRoots(home, workspaceID)` → records within those
roots. A record in a vault mounted only into another workspace is **not visible**, and the
out-of-scope answer is an **empty result, not a permission error** — mirroring ADR-067
FR-052/FR-053, so the error channel cannot be used to probe for records the caller may not see.

**AC-15.1a** — an agent in workspace A gets zero records from a vault mounted only in
workspace B, and cannot distinguish that from the vault being empty.

**D15.1b — Every bound is stated and every breach is a refusal.** §1.3 of this ADR cites
Notion's silent truncation as motivating evidence; shipping our own would be indefensible.

| Bound | Value | On breach |
|---|---|---|
| Results per page | default 50, max 200 | clamped, and the clamp is **reported** in the response |
| Pagination | cursor-based | a cursor that cannot be honoured is an error, never a silent restart |
| Candidate set materialised | 10,000 records | **refused** with a narrowing instruction naming the filter that would help |
| Relation hops per query | 2 | refused; deeper traversal is a follow-up query, not an implicit walk |
| Aggregation over a refused set | — | never partial. No total is returned at all |
| Rate limit | shared with ADR-067's `knowledgeRESTLimiter` | 429 with `Retry-After` |

`complete: false` (D13) is set for **every** one of these, with the reason and the remedy. A
caller that ignores `problems` still cannot mistake a bounded answer for a whole one, because
`complete` is a required field (D19).

**D15.1c — Writes carry ADR-067's version token and are audited.** Revision 1's write path
bypassed both. A record write takes the same opaque content-hash version token ADR-067 D14
defines; a stale token is **refused and the refusal is audited** (ADR-067 AC-14.2). Every
mutating record tool emits an audit entry per ADR-067 D19 — `record.write`, `record.relate`,
`record.log`, `record.view.write` — carrying agent, workspace, record ID and outcome.

**AC-15.1c** — a write with a stale version token is refused, and an audit entry exists for
the refusal.

### D16 — **UNRESOLVED.** Storage and retrieval require a measured spike before any design is accepted

**This decision has been wrong three times, in three consecutive reviews, and each error was a
property of the code that was assumed rather than measured.** Recording that honestly is more
useful than a fourth guess.

| Revision | Claim | Why it was wrong |
|---|---|---|
| 1 | Records extend "the SQLite database ADR-067 uses" | ADR-067 uses bleve and explicitly rejected SQLite (its A2) |
| 2 | Record properties become bleve fields | `doc.Dynamic=false`, `IndexDynamic=false`, `StoreDynamic=false`; `indexDoc` is a closed 5-field struct; property names are runtime data |
| 3 | One stored JSON field, evaluated in Go, gated by an index-format version bump | **There is no index format version.** Only `manifestVersion` (`manifest.go:48`), and a mismatch there re-scans *into the already-open index*. With `Dynamic=false` an undeclared field is silently ignored — every query would report `complete: true` over zero properties |

**And revision 3 carried a defect the earlier framings hid.** A stored-not-indexed field cannot
be searched, and `indexDoc.Kind` only ever holds `note` or `attachment` (`scan.go:45,48`) — so
**there is no indexed field capable of selecting records at all.** Every query would retrieve
every document, breach the candidate cap, and be refused. The per-property design made the
mapping question unmissable; collapsing to a single field made it look like an ordinary struct
addition, and the question stopped being asked.

**Decision: none is taken here.** W1 is gated behind a spike that answers three questions with
measurements, not reasoning:

| # | Question | Exit criterion |
|---|---|---|
| **S-1** | How are record candidates selected? | A named, indexed field (or fields) that narrows to one record type, demonstrated end-to-end, with the measured selectivity at 50,000 records |
| **S-2** | How does an existing index acquire a new field? | A demonstrated upgrade of an index created before the change — either a real format version that forces a rebuild, or a mapping-application path that does not exist today. **A silent no-op must be shown to be impossible** |
| **S-3** | What does Go-side evaluation actually cost? | Measured wall-clock and peak RSS for decode + filter + group over 50,000 records. ADR-068's ~794 ns/record figure covers expression evaluation only, **not** JSON decode or stored-field retrieval, and must not be cited as if it did |

**The spike selects between two designs, and it is allowed to select the second.** If S-1 or
S-2 has no acceptable answer within bleve, or S-3 misses the < 48 MB budget, then §3.6's
dedicated aggregation store is the design — and taking it **supersedes ADR-067 A2 and amends
the CLAUDE.md house rule**, in its own ADR, explicitly. That is a legitimate outcome, not a
failure of this one.

**Nothing downstream of storage may be specified as settled until the spike reports.** The
record model (D1–D15), the tool surface, scoping and the write path are unaffected by the
outcome and remain valid.

### D17 — Interaction history is derived, not maintained

An interaction is a record of whatever type the vault declares for the purpose — the product
does not define one (D0). `record_log` creates a record of that declared type and relates it to
the records involved; **it does not impose a file location, a naming scheme, or a shape.**

What the product *does* fix is how a mention is **interpreted**, because that is a correctness
rule rather than a convention:

Additionally, a mention of a record inside a dated note **is** an interaction, harvested by
the index. The exclusion rules are adopted verbatim from the most carefully-reasoned artifact
in the research, because their absence makes the feature actively harmful:

- a link inside an **unchecked** to-do does **not** count — *"writing `- [ ] reach out to
  [[Sam]]` must not mark Sam as contacted and silence the reminder that prompted it"*;
- a link inside a quote or code block does not count;
- an **embed** does not count, so a dashboard transcluding a record does not register as
  contact;
- a **completed** task does count.

`last_interaction` is therefore derived (D9) and nobody ever types it.

### D18 — Tool-policy seeding (Hard Constraint #6)

**Correcting revision 1, which asserted a safety net that does not exist.** It claimed *"Boot
aborts on any `agent × tool` coverage gap, so this is not optional."* It does not.
`repairAndValidateToolPolicyCoverage` (`pkg/config/validate.go`) calls
`RepairIncompleteToolPolicyCoverage` **first**, which backfills every gap to `deny` and logs
one WARN, and only then validates — so, in the file's own words, *"the validation call should
almost always find zero gaps afterward."* `pkg/coreagent/core.go` states the consequence
directly: **a forgotten tool ships silently denied, with the feature dead and a log line as
the only signal.**

So seeding is not protected by a boot abort. It is protected only by being written down and
tested. Accordingly:

**AC-18.1** — a test enumerates the nine record tools and asserts an explicit, literal,
wildcard-free policy entry for **every seeded agent**, not only the four base agents. Coverage
runs over all ten agents `coreagent.SeedConfig` creates (`mia`, `jim`, `ava`, `ray`, `worker`,
`planner`, `explorer`, `researcher`, `judge`, `plansupervisor`). `worker`'s map is sparse, and
in a sparse map **absence grants** — so `worker` needs explicit entries or it silently receives
tools it should not have.

**AC-18.2** — a test asserts the WARN-backfill path is never reached for these tools on a
fresh install, by asserting zero repaired pairs rather than zero gaps after repair.

Seed posture, following ADR-067 D17's precedent — reads `allow` for the four base agents;
writes `allow` for Jim and Ava, `ask` for Mia and Ray:

| Tool | Mia | Jim | Ava | Ray | worker / others |
|---|---|---|---|---|---|
| `record_schema`, `record_query`, `record_explain`, `record_validate` | allow | allow | allow | allow | explicit `deny` unless a case is made |
| `record_write`, `record_relate`, `record_log`, `record_view_write`, `record_view_import` | ask | allow | allow | ask | explicit `deny` |

### D19 — Contract-first wire types (Hard Constraint #8)

Every type crossing the gateway/SPA boundary is defined in `contracts/` before any Go or TS
code: `RecordSchema`, `RecordType`, `PropertyDef`, `RecordQueryRequest`, `RecordQueryResponse`
(carrying `complete` and `problems`), `RecordWriteRequest`, `RelationWriteRequest`,
`ViewDef`, `ValidationReport`.

`RecordQueryResponse.complete` and `.problems` are **required fields**, not optional. D13's
guarantee has to be structural: a client cannot receive records without also receiving the
completeness verdict.

### D20 — Sequencing

| Wave | Delivers | Exit criterion |
|---|---|---|
| **W1** | Schema files, the seven types, arity/presence/scope, validation, `record_schema` + `record_validate` | a schema violation is reported per record with the expected shape named |
| **W2** | Index tables, `record_query` with filters and grouping, the problem report | a query over a typed corpus returns records + a populated `problems` array; a type mismatch is never a silent empty result |
| **W3** | Relations, inverses, relation grouping, `record_relate` | the §1.2 two-hop question is one call with no hand-maintained state |
| **W4** | `record_write` byte-preserving writes, `record_log`, derived interaction history | write-read-back is byte-identical outside the patched span |
| **W5** | The human surface: record table, grouping, related-records panel, problem banner, drill-down, cell edit | the banner names excluded records and the drill-down lists them |
| **W6** | Saved views (D10) and `record_view_import` (O-1) | a `.base` file imports, with every untranslatable expression reported verbatim |

**Performance targets: none stated, deliberately.**

Revision 2 carried latency and memory numbers derived from a ~794 ns/record figure that
measured **expression evaluation only** — not stored-field retrieval, not JSON decode, not
index traversal. Presenting them as targets invited meeting a number derived from the wrong
measurement while the thing an operator actually feels stayed slow.

D16's spike establishes real numbers from measurement. Until it reports, this ADR states no
latency or memory target, and none may be quoted from it.

The one budget that **does** hold is inherited and unchanged: ADR-067's < 64 MB steady-state
RSS for the index, in this same process. Record work lives inside it.

---

## 3. Alternatives considered and rejected

### 3.1 Implement an Obsidian `.base` evaluator

**Rejected.** Explored on 2026-08-24 and abandoned on two grounds.

*Legal ambiguity, stated as such and not as advice:* the `.base` expression language has no
formal grammar, the evaluator is closed-source, and the documentation repository carries no
licence file. This ADR does not assert a legal conclusion; it records that the question was
unresolved and that a commercial product should not rest on it.

*The engineering argument is independently sufficient.* The documentation demonstrably drifts
from runtime behaviour (date subtraction returns a Duration where the docs say milliseconds;
documented object literals are rejected by the parser), the format broke in five consecutive
releases, and **zero third-party implementations exist** — so there is no conformance suite to
test against. A faithful implementation would be wrong in ways we could not detect, and
silently producing a plausible-but-different answer is exactly what this ADR exists to prevent.

### 3.2 Adopt an existing open-source project as the foundation

**Rejected — none is usable.** SiYuan is architecturally the closest (Go kernel, files on
disk, query layer) and is **AGPL**. `zk` is Go with Markdown+YAML and SQLite, and is **GPL
and requires CGo**. Anytype is Go but its licence permits commercial use only on
vendor-controlled networks. AppFlowy is Rust and Dart, not Go. LeafWiki (MIT, Go,
Markdown-on-disk, pure-Go index) satisfies every constraint but has no query layer — the thing
being built here. Its architecture is worth copying; its code cannot be.

### 3.3 Store derived values in frontmatter for speed

**Rejected.** See D9. It is the single most common source of wrong data in the research
corpus, and an agent cannot tell a stale derived value from a fact.

### 3.4 Model every conceptual link as a relation

**Rejected.** See D6. Notion's own hiring guidance is a flat table with an enum, and treating
that as a degraded model would produce a worse fit for a common shape.

### 3.6 A dedicated aggregation store (the D16 fallback)

**Not taken now; named so the fallback is not a silent choice later.** If the D16 RSS bound
cannot be met, records need a store that aggregates natively. `modernc.org/sqlite` is already
linked into the binary (via WhatsApp) and supports FTS5, JSON functions and generated columns
CGo-free, so the engineering cost is low.

The cost is architectural, and it is not ours to pay quietly: it **overturns ADR-067's
rejected alternative A2** and **amends the CLAUDE.md rule** that *"SQLite is isolated to
WhatsApp session storage only."* A2's stated reason was "no gain over scorch" — assessed for
*search*, where it was correct. For *records* the gain is aggregation, which scorch provably
cannot do at all, so the premise does not transfer. That is a real argument, and it deserves
its own ADR rather than an implementation note.

### 3.5 A visual query builder

**Deferred, not rejected.** Agents author queries; a builder serves a user who cannot write
one. Notion maintains both a GUI filter builder and a raw form, and complex expressions become
uneditable in the GUI — a two-representation problem worth avoiding until there is a reason.

---

## 4. Consequences

### 4.1 Gained

- A CRM-shaped question is one call, not a JavaScript loop with a regex in it.
- A schema violation is a named, per-record, fixable report — not an empty result set.
- Renaming a record cannot break references to it.
- Derived values cannot go stale, because they are not stored.
- The vault remains a plain set of Markdown notes; uninstalling Omnipus loses the index and
  nothing else.

### 4.2 Cost

- **Byte-preserving frontmatter writes are genuinely bespoke** (D14). No Go library does this.
- **The expression layer's null and type semantics are the highest-risk component**, and the
  risk is specific: to make filters behave over real-world frontmatter, comparison operators
  must be overloaded with `func(any, any) bool`, and the moment that happens the expression
  engine's type checker stops protecting those operators. This was hit during research — a
  first-attempt overload made `3 > 2` evaluate to **false**, with nothing reporting an error.
  **Mitigation is mandatory and is a deliverable, not test coverage:** an exhaustive comparison
  truth table (every type pair × every operator × absent × type mismatch) written from this
  specification **before** the comparators exist.
- Two schema surfaces now exist in the product (agent tool policies, and record schemas).
  They are unrelated and could be confused; naming must keep them apart.
- Sub-records and time-bounded facts are **deferred** (D11, D12). Records are one flat map
  per file for now, which is the same wall the incumbents hit; we have simply not promised
  otherwise.

### 4.3 Explicitly out of scope

- **Obsidian `.base` compatibility** — see §3.1. If interop is wanted later it arrives as a
  one-way importer or over MCP, and either is a separate ADR.
- **`.canvas`** — descoped by founder direction 2026-08-24.
- **Board, calendar, gallery and map views** — one view type (table) until a real case appears.
- **Charts** — the numeric layer is second-class even in Notion, whose charts cannot display
  rollups at all.
- **Multi-currency conversion** — pending O-2.
- **Inbound capture** from mail and calendar — high value, separate feature, separate ADR.

---

## 5. Open questions

| ID | Question | Why it matters | Owner |
|---|---|---|---|
| ~~O-1~~ | **RESOLVED 2026-08-25 — one-way importer.** `record_view_import` reads a `.base` file and translates the filters, order and grouping it recognises into a native view. Anything it cannot translate is **reported by name**, never silently dropped or approximated. It is a one-shot translation, not a live read path: we do not take on tracking a format that broke five times in eight weeks. Lands in W6. | Founder |
| ~~O-2~~ | **RESOLVED 2026-08-25 — correct totals with honest gaps.** `money` is amount (integer minor units) + ISO-4217 currency + declared scale, with exact decimal arithmetic. Sums within one currency are exact; sums **across** currencies are refused with the currencies listed. **No FX conversion, no rate table, no periods, no ledger semantics, no amount audit trail.** Those would change the record model and belong to their own ADR. | Founder |
| ~~O-3~~ | **RESOLVED 2026-08-25 — structured JSON only, no query language.** `record_query` takes a typed filter object; every field name, enum value and relation target is validated against the schema before evaluation, so a typo is a rejection naming the valid options rather than an empty result. **No text query language and no parser** — Notion's ~91% token saving is real but it buys a parser we would own, and D13's whole premise is that a malformed query must fail loudly. Revisit only if transcript token cost becomes a measured problem. | Architect |
| ~~O-4~~ | **RESOLVED 2026-08-25 — in the vault.** Schemas live at `<vault>/.omnipus-vault/records/<type>.yaml`, beside the notes they describe. They travel with the operator's data, diff in the same git history as the notes, and survive uninstalling Omnipus — which D8's no-lock-in rule requires. Accepted cost: we write into the operator's folder, and the vault may not be a git repo, so D2's "lives in git" claim is **conditional on the operator's own setup** and must not be stated as a guarantee. | Architect |
| ~~O-5~~ | **RESOLVED 2026-08-25 — one rule: the target is missing.** A relation whose target cannot be resolved is reported as missing, whether it was deleted, moved out of the vault, or on an unmounted drive. We deliberately do **not** distinguish the causes: we do not control the filesystem, an operator can delete a note in Finder at any time, and a system that claimed to tell "deleted" from "unreachable" would be guessing. The report names the record and the unresolvable target; deciding what happened is the operator's. | Founder |
| ~~O-6~~ | **RESOLVED 2026-08-25 — vault-wide, one definition per vault.** A record type means the same thing everywhere its vault is mounted. Schemas live in the vault (O-4) and travel with it, so a per-workspace override would separate a record from the definition that validates it and make "is this record valid?" a question with two answers. Accepted cost: two teams sharing a vault cannot extend a type independently — they change it for both, in a file they can both see and diff. | Founder |

---

## 6. What this ADR deliberately does not claim

- It does not claim the research figures were independently reproduced by the author. Vault
  measurements were; the Notion and Obsidian behavioural claims are cited findings from
  documentation, forums, plugin sources and one decompiled binary, read rather than executed.
- It does not claim performance targets. ADR-067 sets index-level targets; this ADR adds
  record-level query targets only when W2 has something measurable, and **that is a gap** —
  a claim with no threshold cannot be falsified.
- It does not claim the byte-splice write approach is proven at scale. It is proven possible;
  D14 states the acceptance criterion that would prove it.
