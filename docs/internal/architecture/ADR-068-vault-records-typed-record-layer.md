# ADR-068 — Vault records: a typed record layer with relations

- **Status:** Proposed (2026-08-28) — **revision 6**, after a fifth adversarial review (BLOCK: 8 critical, 25 major, 3 minor — `ADR-068-vault-records-typed-record-layer-review-round5.md`). Revision 5's own headline numbers were wrong: the catalog is **98 tools, not 102**, and the two-index staleness mitigation named a **freshness token that does not exist**. Both are repaired below, the second by specifying the mechanism rather than asserting it again. The agent tool surface grows from five `vault_*` tools to **six** — the control plane gets its own policy lever (D15.6). D16's latency argument is **withdrawn as unevidenced**; the capability argument, which is the one that survives, now carries the decision alone.
  - *Revision 5:* three-agent design council; D16 resolved to a two-index design; nine `record_*` tools cut to five `vault_*`; D21, D22, D23 added.
  - *Revision 4 and earlier:* proposed 2026-08-25 after three adversarial reviews (BLOCK each time: 7, 8, then 10 critical). Revision 1 made three false claims about existing code (D14, D16, D18); all three are corrected in place and the corrections are marked. D16 had been wrong three times and was deliberately left unresolved behind a measured spike.
- **Verification standard (revision 5's, restated because revision 5 breached it):** every claim about our own code below cites `file:symbol` or `file:line` and was read at revision time. Revision 5 declared this standard and then broke it twice, in its two load-bearing decisions. **The rule for revision 6 is narrower and harder: a mechanism this ADR relies on either exists and is cited, or is specified as NEW WORK with what it costs — never named as though it already worked.** The freshness token (D16.4) is the test case: it is now specified against the per-file hash the manifest already stores, with the part that must be built named as such.
- **Where this revision disagrees with its review.** Three of the review's claims did not survive checking and are **rejected with evidence**, in place: the spec-is-stale claim (C-4's tail — the spec is at revision 3 and already carries the changes), the "drop `and Matrix`" half of M-24 (Matrix genuinely uses SQLite; the defect was the missing citation), and the BM25 undercount arithmetic (M-23 says twelve; the enumerable count is **thirteen**). Each is marked **REJECTED** where it arises. A review is evidence, not an oracle, and complying with a wrong finding would be the same failure as ignoring a right one.
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

ADR-067 owns: the index engine, full-text search, the link graph, the note reader and preview.
**This ADR owns: record types, the property type system, relations as typed edges, views over
records, retrieval and ranking (D21), and the agent tool surface over all of it.**

**Revision 5 moves the tool boundary.** ADR-067's nine `knowledge_*` tools are **subsumed**, not
sat beside: this ADR now owns **five `vault_*` tools** that replace both the nine `record_*`
tools revision 4 proposed and the nine `knowledge_*` tools that ship today (D15). ADR-067 keeps
the *engine*; it stops owning the *tool surface*.

Where they meet — the index — revision 5 resolves D16 to a **two-index design**: bleve keeps
text, and a derived, disposable properties index in pure-Go SQLite holds typed properties and
relations. Notes remain the sole source of truth.

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
  never **where an interaction is stored** (a convention). An interaction is created by
  `vault_edit` as a record of a vault-declared type; the product does not impose a shape.
  *(Revision 5: this bullet named `record_log`, which D15.4 deletes.)*
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
`vault_describe`'s `check_integrity`**, never silently rendered as a distinct group of one.

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
  `vault_describe`'s `check_integrity` with the offending record and the reason. It is never silently treated as
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
  What we ship instead is honest detection: `vault_describe`'s `check_integrity` reports duplicate identifiers,
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
- **A duplicate ID is a hard validation error**, reported by `vault_describe`'s `check_integrity` with both
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

`vault_find` returns records **and** a problem report in the same response. There is no call
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

**D14.1 — `edit` and `delete` are UNBUILT PRIMITIVES and each needs its own FR.**

*New in revision 5; framing corrected in revision 6.* D15.3 lists `vault_edit` and
`vault_restructure` operations in compact tables, and a reader skimming those tables will
reasonably assume they are relabellings of things that exist. **Two of them are not.**

> **Revision 6:** revision 5's version of this paragraph warned against misreading a table
> row — `replace_body` — that its own table did not contain. `vault_edit`'s operation list read
> *"create, set_property, append_section, link"*. So a primitive this ADR requires appeared in no
> tool at all, and the warning pointed at nothing. `replace_body` is now listed in D15.3. The
> other unbuilt primitive, `trash`, **was** in the table all along.

Verified against the tree:

| Exists today | Where |
|---|---|
| `SetProperty(key, value string)` | `pkg/knowledge/author.go:766` |
| `AppendSection(heading, body)` | `pkg/knowledge/author.go:799` |
| `AppendSectionAt(level, heading, body)` | `pkg/knowledge/author.go:813` |
| `AddWikilink(target, alias, section)` | `pkg/knowledge/authoring_tools.go:1103` |
| `AppendSectionOnce(level, heading, body)` | `pkg/knowledge/authoring_tools.go:1129` |

**Every one of these exported `NoteEdit` constructors is additive.** There is **no body-replace
and no delete/trash primitive anywhere in the package.**

*Precision, because this ADR's own standard demands it:* the `NoteEdit` **type** is
`func(src []byte) ([]byte, error)` (`pkg/knowledge/author.go:540`), which is general enough to
express anything. It is the **shipped constructors** that are uniformly additive. `EditNote`
(`pkg/knowledge/author.go:620`) is **a harness that applies those additive edits in order**, not
an editor — the name invites exactly the misreading this paragraph exists to prevent.

So two operations in D15.3 are new work with real design content:

1. **Body-replace** needs **anchor-text or line-range addressing, with its own ambiguity rules.**
   *What happens when the anchor text appears twice?* Refusing and naming both matches is
   probably right — it is the D13 posture — but it is a decision, not an implementation detail,
   and it must be made in an FR rather than by whoever writes the function.
2. **Trash** needs a **soft-delete convention that does not exist**: where a trashed note goes,
   what happens to the inbound links D15.3 notes cannot be repaired, and whether the index
   forgets it immediately.

**Neither may be listed as a sub-bullet of an existing tool as though it were a relabelling.**
D20 places both in W4 with FRs of their own. This ADR has three prior revisions of assuming
capabilities existed; these two are named so revision 5 does not add a fourth.

### D15 — The agent tool surface: five `vault_*` tools, subsuming `knowledge_*`

**Revised 2026-08-28 (revision 5).** Revision 4 specified nine `record_*` tools. That is
withdrawn. The surface is **five `vault_*` tools**, and they **also replace the nine
`knowledge_*` tools that ship today**. All are contract-first (D19).

**D15.0 — Why the count is a design constraint and not bookkeeping.**

> **CORRECTED in revision 6. Revision 5 said 102. The catalog is 98.** The error was a grep that
> swept in policy *values* (`"ask"`, `"allow"`, `"deny"`) alongside tool *names*. It was quoted
> three times as a counted-not-estimated figure, inside the decision that makes tool count the
> load-bearing constraint, in the revision whose standard is "read at revision time". Every
> figure derived from it was therefore also wrong, and they are re-derived below rather than
> patched.

The static builtin catalog is **98 tools** — `allStaticToolNames`
(`pkg/coreagent/core.go:357-482`). Counted at revision time by stripping trailing `//` comments
from the composite literal and taking unique quoted identifiers: **98 entries, 98 unique**. It
matches `Sandbox.ToolPolicies` in `pkg/config/defaults.go` **entry for entry with no diff** —
the one-for-one global ceiling that `TestCatalog_MatchesGlobalCeilingEntryForEntry` asserts. Two
independently-derived counts agreeing is the reason to believe this one and not the last one.

Nine of the 98 are already `knowledge_*`: `knowledge_search`, `knowledge_graph`,
`knowledge_tasks`, `knowledge_create`, `knowledge_link`, `knowledge_set_property`,
`knowledge_append_section`, `knowledge_move`, `knowledge_rename`.

**The arithmetic, re-derived honestly:**

| | Count | Working |
|---|---|---|
| Catalog today | **98** | counted, cross-checked against the global ceiling |
| Revision 4's shape — nine `record_*` **alongside** the nine `knowledge_*` | **107** | 98 + 9. Roughly **18 definitions on one subsystem** |
| Revision 6's shape — six `vault_*` **replacing** the nine `knowledge_*` | **95** | 98 − 9 + 6 |

So the change is **107 → 95**, a difference of twelve definitions, and the subsystem's own
surface goes **18 → 6**. Revision 5 stated the gain as "98 rather than 111", which quoted
*today's* catalog size as the achievement — the after-number and the before-number had been
swapped by the same original error.

**These absolutes go stale on the next tool that ships.** Rather than re-verify prose by hand
every revision, W1 adds a test asserting `len(coreagent.AllStaticToolNames())` against a named
constant, so a catalog change that invalidates this decision's premise fails a build instead of
rotting quietly in a document. *(The constant is the guard; the ratio 18 → 6 is what the
argument actually rests on, and it does not move.)*

The published evidence on tool-selection accuracy is consistent and unkind: selection accuracy
holds around 50 tools (84–95%) and collapses by 200 (41%); Block cut one server from 30 tools to
2, and GitHub Copilot from 40 to 13, both with *measured improvement*. We are already in the
band where every added definition is paid for on every turn by every agent.

**18 → 5 is the decision.** Not 18 → 14, and not "add nine now and consolidate later" — a tool
name in a seeded policy map is a compatibility surface, and the cheapest moment to not have 18
of them is before any exist.

**D15.1 — The split criterion is BLAST RADIUS. This is the load-bearing idea of the whole
tool surface.**

The tempting criterion is *additive vs destructive*. It is wrong, and it is worth saying why,
because it is the split a reviewer will reach for: `set_property` **overwrites** a value and is
still perfectly safe. Destructiveness is not the axis.

> **REVISED in revision 6. Revision 5 stated one criterion and then applied it under two
> incompatible readings** — *bytes* to keep `link` in the per-file tier, *meaning* to push a
> schema change into the cascading tier — and D23.4 conceded both readings without choosing.
> Revision 5 then presented the result as evidence the criterion generalises "with no
> special-casing", which was unearned: it required switching readings between two rows of the
> same table. **A criterion that needs two readings is two criteria, and the honest fix is to
> name both.**

There are **two** criteria, applied in order. Either one alone puts an operation in the
cascading tier.

> **C-A (bytes).** Does this operation write bytes into files the agent did not name?
>
> **C-B (meaning).** Does this operation change what already-existing files *mean* — their
> validity, their type, or how a query renders them — without writing them?

A `set_property` on `Acme.md` changes `Acme.md`: neither. A rename of `Acme.md` rewrites inbound
wikilinks in N other notes the agent never mentioned and may never have read: **C-A**. Editing
`company.yaml` writes one file and re-validates every note that declares `type: company`:
**C-B**. Those are different kinds of act, and an operator reasoning about risk cares about
exactly that difference.

**Why C-B is a real criterion and not a rescue.** The failure C-B catches is the one this whole
ADR is about. A note that was fine yesterday is reported as a validation finding today, and
nothing in its own file changed — so a human diffing the note sees nothing and a human diffing
the vault sees one small YAML edit. That is precisely §1.3's *silence*, arriving from the
control plane instead of from the query engine.

**C-A has exactly one accepted exception, and it is named rather than absorbed.** Revision 5
wrote that every tier-4 operation *"writes only the file named in `path` — that is the tier's
whole definition and it is checkable at review time."* That is **false as stated**, and it would
have failed on the first record ever created: D7.1 requires every `create` to allocate an
identifier, which writes `.omnipus-vault/records/.seq` under a cross-process flock. So:

> **The tier-4 rule is: only the *note* the agent named, plus `.seq`.** `.seq` is accepted
> because it is a monotonic counter with no readable content, no meaning to any query, and no
> recoverable state to lose — corrupting it costs skipped identifiers (D7.1 says gaps are fine),
> never a wrong answer. **It is the only exception, and adding a second one is a decision for
> another ADR, not for whoever writes the next tool.**

That softening is deliberately kept narrow, because the same softening applied one notch wider
would let a schema file in — and a schema file is the thing C-B exists to keep out.

**D15.2 — Why the split must exist at all: policy resolves on the NAME only.**

This is the constraint that decides the shape, and it is verified rather than assumed:

```go
func (al *AgentLoop) resolveToolPolicyAtExec(
	ts *turnState,
	toolName string,
	filterTimePolicyMap map[string]string,
) string
```

— `pkg/agent/loop.go:12418`. It takes a **tool name**. It does not take the call's arguments
and it does not take an operation discriminator. There is no seam anywhere in that path where a
policy could say *allow `vault_write` when `op=set_property`, ask when `op=trash`*.

Therefore a single consolidated `vault_write` carrying an eight-operation enum could only be
**allowed or denied as a whole**. An operator wanting "this agent may correct a property but may
not restructure the vault" would have no lever at all.

**So consolidating by NOUN conflicts with Hard Constraint #6; consolidating by RISK does not.**
Grouping every vault mutation under one name would make the policy entry meaningless while
still satisfying the letter of the coverage rule — an explicit entry that no longer expresses
an enforceable decision. Grouping by blast radius keeps the entry meaningful:

> **The tool boundary becomes the policy boundary.**

**Record this as a design win, not a limitation.** Hard Constraint #6 acted as a *forcing
function*: it refused the shape that would have been convenient (one write tool, one policy
entry, an enum doing the real work) and pushed us to a surface where the thing an operator
grants is the thing an operator actually means. The constraint made the design better. That is
worth writing down, because the reflex when a constraint blocks a shape is to record it as a
cost.

**D15.3 — The six.**

> **Revision 5 said five. Revision 6 says six**, and the sixth is the answer to the review's
> sharpest structural finding — see D15.6 for why the control plane earns its own name.

| # | Tool | Tier | Does |
|---|---|---|---|
| 1 | `vault_describe` | READ | orientation + integrity |
| 2 | `vault_find` | READ | the one retrieval path |
| 3 | `vault_read` | READ | a note, or one section of one |
| 4 | `vault_edit` | WRITE — one named note (+ `.seq`) | create, set property, append section, replace body, link |
| 5 | `vault_restructure` | WRITE — **cascades in bytes** (C-A) | rename, move, trash |
| 6 | `vault_configure` | WRITE — **cascades in meaning** (C-B) | author and change record types and saved views |

**1. `vault_describe` (READ).** Collections in scope, record types with their typed properties
and enum values, saved views, templates, and index freshness. **Compact text, not JSON** —
Notion measured a ~91% context-token reduction moving their AI surface from JSON schema objects
to a compact textual schema; revision 4 already made this call for `record_schema` and D22 now
extends it to every result.

It is the **mandatory cheap first call**: an agent that has not called it is guessing at
property names, and a guessed property name is the failure D13 exists to prevent.

It also carries **`check_integrity`** — an argument-free whole-vault health sweep reporting
duplicate identifiers (D7.1), unresolved relations (D5.1), and orphans. This absorbs
`record_schema` and the vault-wide sweep half of `record_validate`.

**2. `vault_find` (READ). The ONE retrieval path.** Plain words, typed filters, saved views,
relation joins, `kind:'task'`, and `near: <path>` with `hops` for "within N link steps". It
absorbs `record_query`, `record_explain` (now an `explain: true` flag rather than a tool),
`knowledge_search`, `knowledge_tasks`, and link-neighbourhood traversal.

**`near` COMPOSES with filters, and that composition is the capability worth having.** It makes

> *"notes mentioning pricing within 2 hops of `[[Acme]]`"*

one call. **No system surveyed in this ADR's research can express that query** — not Dataview,
not Obsidian Bases, not Notion, not Tana, not mdbase. Each has text search *or* graph
traversal; none composes them. *(Claim scope: this is a statement about the corpus §1 surveyed,
not a universal claim about every tool that exists.)*

**3. `vault_read` (READ).** A full note or one named section: parsed typed frontmatter, body,
version token, and links + backlinks inline.

**This does not exist today in any form.** That absence is not cosmetic — it is why an agent
must **leave the audited `knowledge_*` boundary entirely**, dropping to `read_file`, to read a
note it just found through `knowledge_search`. And because ADR-067's write path requires a
version token (`EditNoteRequest.ExpectVersion`, `pkg/knowledge/author.go:552-560`, where empty is
refused too), with no read that returns one an agent's only way to *obtain* a version token is
to **send a write it knows will fail** and harvest the token from the `*ConflictError`. A
deliberately-failed write as the supported way to read a value is a design defect, and closing
it is most of this tool's justification.

**4. `vault_edit` (WRITE — one named note).** `create`, `set_property`, `append_section`,
**`replace_body`**, `link`.

> **Revision 6 corrections, both from the round-5 review.** (a) **`replace_body` is now listed.**
> Revision 5's D14.1 warned a reader against misreading D15.3's tables as relabellings and named
> body-replace as one of two unbuilt primitives — while D15.3's table **did not contain
> body-replace at all**. A required primitive appearing in no tool's operation set is a worse
> defect than the misreading D14.1 was guarding against. It writes one named note, so tier 4 is
> right. (b) **Schema and view authoring have moved out of this tool** to `vault_configure`
> (D15.6); revision 5 put them here and that was wrong under C-B.

Every operation writes **only the note named in `path`**, plus `.seq` on `create` — the single
accepted exception D15.1 names and bounds. That is the tier's whole definition and it is
checkable at review time.

`set_property` takes **scalar and list** values — today's scalar-only limit (`SetProperty(key,
value string)`, `pkg/knowledge/author.go:766`) is closed here, per D14's list-splice
requirement.

**`create` takes an optional `template`.** `vault_describe` reports the vault's templates, and a
tool that lists templates nothing can consume is a dead end. `ListTemplates`
(`pkg/knowledge/template.go:213`) has **no non-test caller anywhere in the tree** today —
verified — so this is the first thing that ever uses it. The parameter mirrors
`knowledge_create`'s (`pkg/knowledge/authoring_tools.go:453`).

**Why `link` belongs in this tier and not the next:** a link is stored **once, on the SOURCE
note** (D5.1) and the inverse is derived (D5). Linking `Deal.md` to `[[Acme]]` writes
`Deal.md` and **never touches `Acme.md`**. It looks like a two-file operation and is a one-file
operation. Had we stored both sides — Notion's mistake, quoted in D5 — `link` would have
belonged in tier 5.

**5. `vault_restructure` (WRITE — cascades in bytes).** `rename`, `move`, `trash`. *(Revision 5
also put "changing an existing record type" here; revision 6 moves every schema and view
operation to `vault_configure` — D15.6.)*

**Trash stays in this tier even when it is a soft delete, and the reasoning generalises.** The
objection is natural: *a recoverable bin makes trash safe, so it belongs with the ordinary
edits.* It does not, because:

> A recoverable bin fixes the **trashed note's own recoverability**. It does nothing whatsoever
> for the **N other notes whose links just broke**.

Rename at least *repairs* inbound links (ADR-067 D10 rewrites them). Trash has **nothing to
repair them to**. So trash is, if anything, the worse cascade of the two.

**Recoverability and blast radius are different axes.** Conflating them is how a "safe because
undoable" argument smuggles a vault-wide operation into a per-file tier.

**D15.6 — The sixth tool: `vault_configure`, the control plane.**

*New in revision 6, and it is a correction rather than an addition.*

**The defect.** Revision 5 placed schema and view authoring inside `vault_edit`, alongside
`set_property` on one note. The consequence is that the posture

> *"this agent may edit notes freely, but may not author or change the vault's type system"*

was **inexpressible** — because policy resolves on the tool name alone (D15.2), and both live
under one name. That is word-for-word the complaint D15.2 and D18 level at today's `knowledge_*`
surface. **Revision 5 reproduced its own criticised defect at a different seam**, and the review
was right to call it the sharpest structural finding.

Worse, it made D23.3's closing promise false. Revision 5 told an operator that *"an operator who
wants to protect schemas sets `vault_restructure` restrictively"* — while an agent holding
`vault_edit: allow` (Jim and Ava, in D18's own seed) could create arbitrary new record types.
**A document that tells an operator a control works when it does not is the failure this ADR
exists to prevent**, committed in the decision claiming the criterion generalises.

**The decision.** Schema files and view files get their own tool:

| Operation | Tool | Criterion |
|---|---|---|
| Create a **new** record type | `vault_configure` | **C-B** — see below |
| Change an **existing** record type | `vault_configure` | **C-B** (FR-015) |
| Delete a record type | `vault_configure` | **C-B**, more of it |
| Create or change a **saved view / base** | `vault_configure` | **C-B**, weakest form — it changes what queries return, not what notes are |

**Why "create a NEW record type" is C-B, which revision 5 got backwards.** Revision 5's stated
grounds were: *"A new schema file. Nothing that already exists is reinterpreted — no note changes
meaning."* **That is false under this ADR's own D1.** A note becomes a record **by declaring
`type:` in frontmatter**, and D1 says a note with an *unrecognised* type is an ordinary note,
silently and by design. So writing `.omnipus-vault/records/company.yaml` converts **every
pre-existing note already carrying `type: company`** from an ordinary note into a validated
record — indexed, queryable, and, if it lacks a `required: true` property, newly reported as a
validation finding. Notes nobody named changed meaning. That is C-B exactly.

The failure mode is worth stating concretely because it is not obvious: an operator whose vault
already uses `type: meeting` as a personal convention, whose agent then authors a `meeting`
schema, gets a validation report over hundreds of notes they never asked to be validated — and
nothing in the diff explains it beyond one new twelve-line YAML file.

**Justifying a sixth tool against D15.0's count argument.** D15.0's case is that tool *count*
costs selection accuracy, so the sixth definition must earn its place. It does:

- **The catalog goes 98 → 95, not 98 → 94.** One definition against a twelve-definition
  reduction. The evidence D15.0 cites is about catalogs of 50 and 200; nothing in it turns on 94
  versus 95.
- **D22.8's ~150-token description budget puts the standing cost at ~150 tokens per agent
  per turn**, against ~750 for the surface as a whole. It is the cheapest lever this ADR buys.
- **It is the only lever that expresses a real posture.** D15.2's whole thesis is that a tool
  boundary is worth having exactly when an operator would want to grant one side and not the
  other. "Edit the notes, do not redefine what a note *is*" is that, unambiguously — and it is
  the posture most operators will actually want for a delegated worker.
- **Selection accuracy is not harmed by a tool that is rarely reached for.** `vault_configure` is
  selected when an agent is authoring a type or a view, which is a distinct enough intent that
  it competes with nothing. The tools that hurt selection are near-synonyms; this is not one.

**It does NOT contradict D23.2's operator ruling.** The operator said an agent with write-enabled
tools *"should be able to manage the vault completely… that includes creating and changing
bases."* It still can: `vault_configure` is an ordinary tool governed by ordinary policy, with no
approval flow, no `request_schema_change`, and no UI-ratifies step. **What changed is that the
operator now has a switch they did not have — which is more control, not less.** D23.5 continues
to apply: an operator may absolutely set it to `allow`.

**D15.4 — Retired from the agent surface entirely.**

| Retired | Why |
|---|---|
| `record_view_import` | → an **operator/CLI one-shot**. FR-101 requires every untranslatable expression to be reported **verbatim for a human to read and judge**. That is a UI act with a human in it by definition, and it should not cost a permanent slot in a 98-tool catalog that every agent pays for on every turn. O-1's resolution stands; only its delivery surface changes. |
| `record_log` | → **deleted, not relocated.** D17 already makes interaction history **derived** from mentions. A dedicated logging tool therefore serves only the residual case of an interaction with no note behind it — at permanent catalog cost, and in tension with D9's rule that derived values are computed rather than written. |

### D15.5 — Scope, bounds and refusal (closing ADR-067's C-4 for records)

ADR-067's own review was blocked partly because multi-vault search crossed the workspace
isolation boundary; it closed that with D7's workspace scoping and a negative test, now
implemented as `pkg/knowledge/scope.go` (`Scope.WorkspaceID`, `.Roots`, `.Contains`,
`.Truncated`). Records are a **stronger** read primitive — typed fields, relations, and
aggregation across them — so the same boundary applies with no exception.

**D15.5a — Workspace scoping.** Every one of the five `vault_*` tools resolves through the calling agent's
workspace: agent → workspace → `AllowedMountRoots(home, workspaceID)` → records within those
roots. A record in a vault mounted only into another workspace is **not visible**, and the
out-of-scope answer is an **empty result, not a permission error** — mirroring ADR-067
FR-052/FR-053, so the error channel cannot be used to probe for records the caller may not see.

**AC-15.5a** — an agent in workspace A gets zero records from a vault mounted only in
workspace B, and cannot distinguish that from the vault being empty.

**D15.5b — Every bound is stated and every breach is a refusal.** §1.3 of this ADR cites
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

**D15.5c — Writes carry ADR-067's version token and are audited.** Revision 1's write path
bypassed both. A record write takes the same opaque content-hash version token ADR-067 D14
defines; a stale token is **refused and the refusal is audited** (ADR-067 AC-14.2). Every
mutating `vault_*` tool emits an audit entry per ADR-067 D19 — **`vault.edit` and
`vault.restructure`, each carrying its operation** — with agent, workspace, record ID and
outcome. *(Revision 5: the audit event carries the operation even though the tool policy cannot
read it (D15.2). Policy resolves before the call on the name; the audit record is written after
it, where the operation is known. Naming them apart keeps the audit log readable without
implying a policy lever that does not exist.)*

**`vault_read` supplies the version token** a write needs (D15.3), which is what makes the
compare-and-swap contract usable rather than a failed-write ritual.

**AC-15.5c** — a write with a stale version token is refused, and an audit entry exists for
the refusal.

### D16 — **RESOLVED 2026-08-28: a two-index design.** bleve keeps text; a derived SQLite index holds properties

**This decision was wrong three times, in three consecutive reviews, and each error was a
property of the code that was assumed rather than measured.** Recording that honestly was more
useful than a fourth guess — so revision 4 took none, and gated the decision behind a spike.
**The spike has reported** (`../design/adr068-storage-spike-2026-08-25.md`). The table of prior
errors is kept below because it is the reason the spike existed.

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

**D16.1 — What the spike measured.**

| # | Question | Answer |
|---|---|---|
| **S-1** | How are record candidates selected? | **An indexed field works.** A `record_type` keyword field selects exactly its population in **0.10–5.07 ms over a 100,000-document index**, cost tracking the *hit count* rather than index size. A `[]string` `record_props` field carries runtime-named `name\x1fvalue` terms **even with `Dynamic=false`**, narrowing the largest type from 25,000 to 5,018 in ~3 ms. Revision 2's "per-property fields are impossible" was true and its conclusion did not follow. |
| **S-2** | How does an existing index acquire a new field? | **It does not on its own — the silent no-op was REPRODUCED and is real.** `bleve.OpenUsing` takes no mapping argument; the mapping is persisted in the store. Same code, same document, same query: **1 hit on a fresh index, 0 hits and `err=nil` on an existing one.** Two guards close it (G1 format version, G2 persisted-mapping assertion); G2 catches the case G1 misses when a developer forgets to bump the constant. |
| **S-3** | What does Go-side evaluation cost? | **Expression evaluation is 0.5–0.9% of the true per-record cost.** Retrieval and decode are the other ~99%. The ~794 ns/record figure revision 2 quoted as a sizing basis is off by roughly **two orders of magnitude** against a measured total of 33,000–48,000 ns/record. |
| **F-0** | *(not asked for — found anyway)* | **bleve v2.6.0's pinned `zapx/v17 v17.1.2` writes corrupt segments at 100,000 documents**, panicking an unrecovered `slice bounds out of range` through `indexImpl.Search`. Reproduced with the **shipping mapping and no record fields at all** — an ADR-067 defect ADR-068 merely tripped over. |

**F-0 is already fixed on this branch and that is verified, not assumed:** `go.mod:12` now pins
`github.com/blevesearch/bleve/v2 v2.6.1` and `go.mod:107` pins
`github.com/blevesearch/zapx/v17 v17.2.3`, both above the v17.1.4 that carries upstream's fix
(`blevesearch/zapx` PR #409). The spike's condition C-1 is discharged. Its second half —
**force a rebuild of existing indexes**, because segments already written stay corrupt — is
**not** discharged by the version bump and is carried into D20 as work.

**D16.2 — The decision: two indexes.**

- **bleve keeps TEXT.** It already does this, it does it well, and D21 improves its ranking
  rather than replacing it.
- **A properties index in pure-Go SQLite** holds typed properties, relations, and derived child
  tables.

**On the dependency question, which is the one a reader will check first:**
`modernc.org/sqlite v1.46.1` is a **direct dependency already** — `go.mod:64`, in the main
require block, not the indirect one. So this is **no new runtime dependency, no CGo, and the
single-binary constraint (Hard Constraint #1, #2) is preserved.**

**It is linked into the binary today by two channels, and revision 6 cites both** — revision 5
asserted this without a citation, in the one decision where its own verification standard mattered
most:

- **WhatsApp**, via `whatsmeow`: `pkg/channels/whatsapp_native/whatsapp_native.go:1`.
- **Matrix**, directly: `pkg/channels/matrix/matrix.go:31` imports `_ "modernc.org/sqlite"`,
  `matrix.go:43` names the driver `sqliteDriver = "sqlite"`, and `matrix.go:343` opens it with
  `sql.Open(sqliteDriver, connStr)` for the E2EE crypto store.

> **REJECTED, in part — round-5 review M-24.** The review's fix was *"cite it or drop `and
> Matrix`"*, on the grounds that `CLAUDE.md:62` says whatsmeow only and `channel_matrix.go:21-23`
> mentions SQLite only in a build-unavailability context. **The claim is true and the citation is
> the fix; dropping it would have made the ADR less accurate, not more.** `CLAUDE.md:62` is the
> stale document here — which D16.4 already schedules for correction. This is worth marking
> because "an uncited claim" and "a false claim" are different defects with different remedies,
> and revision 5 committed the first, not the second.

**D16.2a — Platform posture. Hard Constraint #4, addressed rather than assumed.**

*New in revision 6. Revision 5 said nothing about this, which was the review's C-3 and is a real
gap: making the properties index depend on SQLite means the record layer cannot exist wherever
SQLite cannot build, and "it doesn't compile" is not an answer an operator can act on.*

`modernc.org/sqlite` **cannot build on three targets**, and the repo already documents each:

| Target | Evidence |
|---|---|
| `linux/mipsle` (softfloat) | `pkg/gateway/channel_matrix.go:20-22` — *"modernc.org/sqlite/modernc.org/libc also lacks a working build path for our mipsle + softfloat target"* |
| `netbsd/*` | `channel_matrix.go:23-25` — *"modernc.org/sqlite v1.46.1 fails to compile due to broken generated mutex code on NetBSD"* |
| `freebsd/arm` | `channel_matrix.go:26-28` — *"modernc.org/libc v1.67.6 fails to compile due to broken generated 32-bit FreeBSD code"* |

Both existing consumers are gated against exactly that set:
`whatsapp_native.go:1` is `//go:build !lite && !mipsle && !netbsd && !(freebsd && arm)`;
`channel_matrix.go:1` is `//go:build !mipsle && !netbsd && !(freebsd && arm) && (goolm || cgo)`.

**Three consequences, each stated so it can be argued with:**

1. **`-tags lite` KEEPS records.** The `lite` tag drops whatsmeow only. Matrix is not `lite`-gated,
   and `make build-lite` builds with `$(GO_BUILD_TAGS),lite` = `goolm,stdjson,lite`
   (`Makefile:205-213`), so SQLite is still linked. *A reasonable reader assumes the opposite —
   `lite` sounds like it drops the heavy dependency — so it is worth stating outright.*
2. **Exactly one SHIPPED binary lacks SQLite: `linux/mipsle`.** It is the only Makefile target
   built with `GO_BUILD_TAGS_NO_GOOLM` (`Makefile:210`, `:234`), which drops Matrix as well as
   WhatsApp. `netbsd` and `freebsd/arm` are **not shipped at all** by `make build-all` — they are
   source-buildable targets, so the exposure there is a person compiling from source, not a
   release artifact.
3. **On a target without SQLite, records are UNAVAILABLE and say so.** The record layer degrades
   the way ADR-067's channels already do: a build-tagged stub registers, the gateway boots, and
   **every `vault_*` call that needs the properties index returns a refusal naming the platform**
   — never an empty result, which would be D13's headline failure. `vault_read` and the plain-text
   half of `vault_find` keep working, because they resolve through bleve; typed filters, relation
   joins, grouping and aggregation do not. The precedent is
   `pkg/channels/whatsapp_native/whatsapp_native_stub.go`, whose header states the same posture
   for the same reason: *"REST callers use this to know native WhatsApp is unavailable on this
   build/arch and must NOT offer it."*

**This is a product decision, and it is being made here rather than discovered in a build
failure:** records are a feature of the SQLite-capable builds. Putting them on `linux/mipsle` —
a softfloat router-class target — would mean a second storage backend for one embedded platform,
which is a worse trade than an honest refusal. **W1 owns the stub and the refusal string.**

**The properties index is DERIVED and DISPOSABLE.** Delete it and it rebuilds from the notes.
**Notes remain the sole source of truth** — this is D8's no-lock-in rule and D9's
never-store-derived-values rule applied to the store itself. Nothing is in SQLite that is not
reconstructible from Markdown. *(This is also what makes D16.2a survivable: a vault carried from
a SQLite-capable machine to `linux/mipsle` loses a capability, never data.)*

What it buys that Go-over-bleve does not: **joins, `OR`, `GROUP BY` and aggregates, without our
writing a general expression evaluator.** It answers §3.6 without a separate aggregation store,
because it *is* the aggregation store, folded into the design rather than bolted beside it.

**D16.2b — Which store answers a filter. One sentence, because revision 5 never gave it.**

> **The properties index answers every typed predicate — membership, filtering, joining,
> grouping, aggregation. bleve answers text relevance, and nothing else.**

D21.2's fielded indexing (*"property keys, property values… as distinct fields"*) exists so that
**free-text search over frontmatter works** — so that searching the word `prospect` finds a note
whose frontmatter says `status: prospect`, which today it does only by accident, as loose body
prose (D21.2). It is **not** a second typed-filter path, and `vault_find` never consults it for
one. Revision 5 decided both in the same revision without either referencing the other, which
left three generations able to disagree instead of two.

**D16.2c — This resolves against FR-021, and the spec has already moved.**

Revision 5's D16.2 contradicted the implementing spec's FR-021 (*"filtering, grouping and
aggregation MUST be evaluated in Go over the retrieved candidate set"*) without noticing, while
D21.5 built its tokenizer-hazard argument on that same requirement.

> **REJECTED, in part — round-5 review C-4's tail.** The review states *"`spec:274-275` still
> marks FR-020/FR-020a **BLOCKED on ADR-068 D16's spike**"*, *"`spec:20` still asserts
> per-property fields are impossible"*, and *"No wave owns updating the spec."* **All three were
> true of the commit the review read (`b4a957e1`) and are false of the tree now.**
> `vault-records-spec-2026-08-25.md` is at **Draft revision 3**, realigned to ADR-068 revision 5
> after it committed: FR-020 and FR-020a are unblocked and respecified (`spec:460-461`), FR-021 is
> **explicitly marked MEANING CHANGED** to *"evaluated in the properties index, over typed
> columns"* (`spec:468`), and the spec carries a whole "FRs whose meaning changed, and what cited
> them" table (`spec:20-25`) built for exactly this. The review was reading a stale target, which
> is a normal hazard of a moving branch and not a fault in the review.

So the resolution is **FR-021 changes, and it already has.** What remains for this ADR is to say
so, and to record the one thing the spec's own note gets right that revision 5 missed: **the
tokenizer hazard survives the change.** D21.5 is re-derived on that basis below.

**D16.3 — This overrides the spike's stated recommendation. The grounds, stated plainly.**

The spike recommends **(a) bleve** and says §3.6's store is "not needed on these numbers". This
ADR takes the other option, and pretending otherwise would be the exact dishonesty D13 exists
to prevent. So:

- **The spike answered the question D16 asked, and on that question bleve passes.** D16 gated on
  **memory**. Streamed evaluation at the 10,000-record cap measures 23.6–24.0 MB peak RSS,
  comfortably inside ADR-067's 64 MB budget. That result is accepted, not disputed.
- **We reopen it on grounds the spike itself named as outside its scope**, quoting its §6.1:
  *"~460 ms of CPU for a 10,000-record query is not fast. It fits the memory budget, which is
  what D16 gated on, but a dedicated aggregation store would be substantially quicker. If
  interactive latency later becomes the binding constraint rather than memory, §3.6 should be
  reconsidered on that basis — it is not reopened here because latency is not what D16 asked
  about."* We are reconsidering it on exactly that basis.
- **S-3 is the argument for the override, not against it.** Decode is ~99% of the cost because
  every candidate is retrieved as JSON and unmarshalled into `map[string]any` before Go can
  filter it. A typed properties index does not pay that cost at all: the filter, the join, the
  group and the aggregate happen inside the store, over typed columns, and only the surviving
  rows are materialised. The measurement that vindicates bleve on memory is the same
  measurement that identifies where the time goes.
- **Capability, not only speed.** FR-021 and §3.6 want joins, `OR`, `GROUP BY` and aggregates.
  Over bleve these are all Go-side, which means we own an expression engine — and §4.2 already
  records that the expression layer's null and type semantics are *the highest-risk component
  in this ADR*, with a first-attempt comparator overload making `3 > 2` evaluate to **false**.
  Choosing the option that requires less of that code is a correctness argument, not a
  convenience one.
- **The cap stops being load-bearing for memory.** The spike's C-3 notes FR-064's 10,000-record
  cap is what keeps the design inside ADR-067's budget. With aggregation pushed into the store,
  the cap becomes a politeness limit again rather than the thing preventing a breach.

**D16.4 — What this costs, named rather than skipped.**

1. **It widens the CLAUDE.md house rule that *"SQLite is isolated to WhatsApp session storage
   only."*** That widening is **deliberate and is recorded here** as the decision that makes it.
   It also overturns **ADR-067's rejected alternative A2**, whose stated reason was "no gain
   over scorch" — assessed for *search*, where it was correct. For *records* the gain is
   aggregation, which scorch cannot do at all, so the premise does not transfer. §3.6 always
   said this deserved an explicit decision rather than an implementation note; this is it.
2. **Two indexes can disagree, and that is a new failure mode of exactly the class this ADR
   exists to prevent.** A text index and a properties index that have seen different generations
   of the same note produce a confidently wrong answer. **Mitigation is mandatory and structural,
   not test coverage:** the properties index carries the same freshness token the text index
   does, `vault_find` compares them, and a mismatch sets `complete: false` (D13) naming staleness
   as the reason. An answer computed across two indexes at different generations must never be
   reported as complete.
3. **The spike did not measure the write path, concurrent queries, property counts above 10 per
   record, or any non-macOS platform** (its §6.1). The two-index write path — one note, two index
   updates — is precisely the unmeasured area, and D20 places it where it gets measured rather
   than assumed. **This ADR has been wrong three times by assuming exactly this kind of thing.**

**Unchanged by this resolution.** The record model (D1–D15), the tool surface, scoping and the
write path do not depend on the storage outcome and remain valid, as revision 4 predicted.

### D17 — Interaction history is derived, not maintained

An interaction is a record of whatever type the vault declares for the purpose — the product
does not define one (D0). It is created by **`vault_edit`** like any other record and related to
the records involved; **the product does not impose a file location, a naming scheme, or a
shape.**

> **Revision 5 deleted the dedicated `record_log` tool** (D15.4). The reasoning is D17's own: if
> interaction history is **derived** from mentions, a dedicated logging tool serves only the
> residual case of an interaction with no note behind it — and that case is served by creating a
> note, which `vault_edit` already does. A permanent slot in a 98-tool catalog is too high a
> price for a verb that is a special case of `create`.

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

**AC-18.1** — a test enumerates the **five `vault_*` tools** and asserts an explicit, literal,
wildcard-free policy entry for **every seeded agent**, not only the four base agents. Coverage
runs over all ten agents `coreagent.SeedConfig` creates (`mia`, `jim`, `ava`, `ray`, `worker`,
`planner`, `explorer`, `researcher`, `judge`, `plansupervisor`). `worker`'s map is sparse, and
in a sparse map **absence grants** — so `worker` needs explicit entries or it silently receives
tools it should not have.

**AC-18.2** — a test asserts the WARN-backfill path is never reached for these tools on a
fresh install, by asserting zero repaired pairs rather than zero gaps after repair.

**Seed posture, revised 2026-08-28 to the D15.1 blast-radius tiers:**

| Tool | Tier | Mia | Jim | Ava | Ray | worker / others |
|---|---|---|---|---|---|---|
| `vault_describe`, `vault_find`, `vault_read` | READ | allow | allow | allow | allow | `deny` |
| `vault_edit` | WRITE — one named file | **ask** | allow | allow | **ask** | `deny` |
| `vault_restructure` | WRITE — **cascades** | **ask** | **ask** | **ask** | **ask** | `deny` |

**Reads are `allow` roster-wide, and the existing reasoning is kept unchanged:** a prompt in
front of a read that another tool already permits is a prompt that protects nothing. An agent
denied `vault_read` still has `read_file`. The prompt would train the operator to click through
without buying a single unit of safety.

**`vault_restructure` gets its own line, and that is the point of splitting it out.** It
defaults more restrictively than `vault_edit` so that **an operator can forbid restructuring
while permitting edits** — a posture that is simply inexpressible today, and would have stayed
inexpressible under a consolidated `vault_write` (D15.2).

**This FIXES a live defect, stated as such.** Today `knowledge_rename` and `knowledge_move` sit
in the **same `ask` bucket** as `knowledge_append_section`, despite the first two rewriting
inbound wikilinks across the whole vault and the third appending to one file. An operator who
grants "vault writes" today grants vault-wide restructuring in the same gesture, with nothing in
the tool surface signalling the difference. The tiering is not new taxonomy for its own sake; it
is the missing distinction.

**Workers stay `deny` on all five** — unchanged from revision 4, and now explicitly including
the read tools.

### D19 — Contract-first wire types (Hard Constraint #8)

Every type crossing the gateway/SPA boundary is defined in `contracts/` before any Go or TS
code: `RecordSchema`, `RecordType`, `PropertyDef`, `RecordQueryRequest`, `RecordQueryResponse`
(carrying `complete` and `problems`), `RecordWriteRequest`, `RelationWriteRequest`,
`ViewDef`, `ValidationReport`.

`RecordQueryResponse.complete` and `.problems` are **required fields**, not optional. D13's
guarantee has to be structural: a client cannot receive records without also receiving the
completeness verdict.

**Revision 5 additions.** The five-tool surface adds `VaultDescribeResponse`,
`VaultFindRequest` / `VaultFindResponse`, `VaultReadResponse` (carrying the version token
`vault_edit` requires — D15.3), `VaultEditRequest` and `VaultRestructureRequest`. The
`Record*` schemas above remain the record-model types those requests carry; only the
tool-shaped envelopes are renamed.

**D22 does not weaken this decision, and the distinction matters.** D22.1 makes tool results
**compact text to the model**. The **wire type is unchanged**: still contract-defined, still
generated into `pkg/api/generated/` and `src/lib/api/generated/`, still verified by
`make verify-contracts`. The compact rendering is produced **from** the validated wire object at
the tool-result boundary — it is a projection of the contract, never a replacement for it, and
never a hand-written parallel struct (Hard Constraint #8).

### D20 — Sequencing

**Revised 2026-08-28. Operator directive, explicit: NO quick wins — build the full
best-in-class system.** The sequence below is therefore ordered for **the whole thing**, not for
cheap partial value. There is deliberately **no "quick wins" wave**: the TF-IDF correction
(D21.1) and the index rebuild F-0 requires (D16.1) ride along **inside the waves they belong
to**, because a one-line fix shipped outside the design it belongs to is how a system acquires
changes nobody can explain later.

| Wave | Delivers | Exit criterion |
|---|---|---|
| **W1** | Schema files, the seven types, arity/presence/scope, validation. The SQLite properties index (D16.2) with its freshness token, its rebuild-from-notes path, and the two-index staleness check (D16.4 item 2). `vault_describe` including `check_integrity`. | a schema violation is reported per record with the expected shape named; deleting the properties index and reopening rebuilds it with identical query results |
| **W2** | Fielded indexing (D21.2), the **`ScoringModel` correction** (D21.1), BM25F weighting + RRF (D21.3), the shared tokenizer resolution (D21.5). The **index rebuild** F-0 requires (D16.1). `vault_find` — plain words, typed filters, grouping, the problem report. | a query over a typed corpus returns records + a populated `problems` array; a type mismatch is never a silent empty result; a field query on a property key is possible at all, which it is not today |
| **W3** | Relations, inverses, relation grouping; `near` + `hops` and its **composition with filters** (D15.3). `vault_read`. | the §1.2 two-hop question is one call with no hand-maintained state; "notes mentioning pricing within 2 hops of `[[Acme]]`" is one call |
| **W4** | `vault_edit`: byte-preserving writes, the **list-valued splice** (D14), and the two **unbuilt primitives** — body-replace and trash — each with its own FR (D14.1). Derived interaction history (D17). Saved-view and record-type authoring (D23). | write-read-back is byte-identical outside the patched span; a body-replace whose anchor is ambiguous is refused, naming both matches |
| **W5** | `vault_restructure`: rename, move, trash, existing-record-type change. The D18 policy seeding and its two ACs. | an operator can forbid restructuring while permitting edits, and a test proves the two policies are independently settable |
| **W6** | The human surface: record table, grouping, related-records panel, problem banner, drill-down, cell edit. The **operator/CLI** saved-view importer (D15.4, O-1). | the banner names excluded records and the drill-down lists them; a `.base` file imports with every untranslatable expression reported verbatim |

**Performance targets: none stated, deliberately — and revision 5 does not change this.**

Revision 2 carried latency and memory numbers derived from a ~794 ns/record figure that
measured **expression evaluation only**. D16.1 now records the measured reality: that phase is
**0.5–0.9% of true cost**, so the numbers were off by roughly two orders of magnitude.

The spike produced real numbers, but they measure the **bleve-plus-Go design this ADR did not
take** (D16.3). Quoting them as targets for the two-index design would repeat the original
error in a new costume. Targets arrive when W1 has the SQLite path measured.

The one budget that **does** hold is inherited and unchanged: ADR-067's < 64 MB steady-state
RSS for the index, in this same process. Record work lives inside it, and the properties index
is inside it too.

### D21 — Retrieval: lexical ranking done properly, and no embeddings, permanently

**New in revision 5.** ADR-067 shipped retrieval; nobody had audited what it actually ranks
with. Doing so found a defect and a structural gap.

**D21.1 — A DEFECT: vault search is scoring TF-IDF, not BM25.**

Verified, and this one *did* survive checking:

- `const DefaultScoringModel = TFIDFScoring` —
  `bleve_index_api@v1.4.1/indexing_options.go:37`.
- **`ScoringModel` is set NOWHERE in `pkg/`.** A repository-wide grep returns zero
  assignments. The default therefore stands everywhere.

Meanwhile the code says otherwise, in at least seven doc comments across three packages:

| Location | Claims |
|---|---|
| `pkg/knowledge/index.go:164` | *"Score is the BM25 score of the file's BEST segment."* |
| `pkg/knowledge/index.go:1062` | *"Search runs a BM25 query…"* |
| `pkg/memrooms/index/index.go:19` | *"Recall: BM25 ranking via **bleve's default BM25 scorer**"* — the default is TF-IDF |
| `pkg/memrooms/index/index.go:249-250` | *"executes a BM25 full-text query… ordered by descending BM25 score"* |
| `pkg/agent/memory.go:620` | *"searches the specified room scope for query using bleve BM25 (FR-7.4)"* |
| `pkg/agent/retro_bm25.go:14` | retro BM25 parameters *"match bleve's defaults"* |
| `pkg/agent/retro_bm25.go:24` | the retro ranker exists to match *"the BM25 similarity ranking bleve provides for long-term memories"* |

The last two are the ones that matter beyond documentation hygiene: `retro_bm25.go` builds a
**cross-subsystem score-comparability argument on a false premise.** It hand-rolls BM25 with
`k1=1.2, b=0.75` specifically so retrospective ranking is commensurate with bleve's — and
bleve is not producing BM25 scores at all.

**The fix is one line.** It is not cosmetic: **it changes rankings**, because BM25's saturation
and length normalisation are exactly what TF-IDF lacks. It therefore ships inside W2 with the
fielded-indexing work whose ranking it affects, not as a detached one-liner.

**D21.2 — The index has no fields worth ranking.**

`indexDoc` is a closed five-field struct — `Path`, `Name`, `Kind`, `Offset`, `Body`
(`pkg/knowledge/index.go:583-589`). And **frontmatter is not stripped**: there is no
frontmatter-stripping step anywhere in the indexing path, so YAML flows into `Body` as prose.

The consequence is concrete. Given `status: prospect` in frontmatter, the index holds the loose
tokens `status` and `prospect` in the body text. **No field query is possible** — yet a search
for "prospect" returns the note and reports `Complete: true`. The system is confidently
answering a question it cannot represent.

Fielded indexing — title, name, headings, property keys, property values, body as **distinct
fields** — is therefore **green field**. There is nothing to migrate away from, which makes this
cheaper than it sounds and is a reason to do it properly now rather than incrementally.

**D21.3 — BM25F-style field weighting, fused with Reciprocal Rank Fusion.**

Ranking combines four signals:

1. **BM25 over weighted fields** (BM25F-style — title and name weigh more than body);
2. **exact / prefix name match** — the "I know what it's called" case, which pure BM25 ranks
   poorly;
3. **recency**;
4. **link-graph backlink degree** — a note many notes point at is more likely the canonical one.

They are combined with **Reciprocal Rank Fusion**, which **operates on ranks rather than
scores, so no score normalisation is required.** That is the property worth having: normalising
a BM25 score against a recency score against a degree count is a tuning problem with no
principled answer, and RRF removes the question rather than answering it badly.

> **This specific signal mix is OUR COMPOSITION, not a benchmarked whole.** BM25F and RRF are
> each well-established individually; *this combination, on vault data, is not something we have
> measured or found measured.* It is a reasoned starting point and must be treated as one — W2
> should ship it behind a comparison against plain BM25, not assume the fusion helps.

**D21.4 — Query expansion (RM3 / pseudo-relevance feedback) only on retry, never by default.**

Two independent reasons, and either alone is sufficient:

1. **PRF assumes first-pass precision and amplifies error when it is absent.** It takes the top
   results on faith and expands the query with their terms. If the first pass was wrong, the
   second pass is confidently wronger.
2. **Silently expanding a query answers a question nobody asked**, which breaks the D13 honesty
   contract directly. A user who searched for one thing and received results for a broader thing
   has been given a wrong answer with no error channel — §1.3's failure mode, reintroduced by us.

**On zero hits, surface near-miss VOCABULARY instead of guessing:** *"no matches; nearest indexed
terms: `prospect`, `prospecting`, `prospects`"* — and let the agent reformulate. That respects
the agentic loop (D22) instead of pre-empting it, and it tells the truth about why the query
failed.

**D21.5 — A tokenizer hazard: dormant today, live the moment Go-side ranking ships.**

**Three notions of "a term" coexist in this codebase right now:**

| Tokenizer | Rule | `"don't"` becomes |
|---|---|---|
| `pkg/utils/bm25.go:216` — `bm25Tokenize` | `strings.Fields` (whitespace), then `strings.Trim` of **edge** ASCII punctuation `.,;:!?"'()/\-_` | `["don't"]` — the apostrophe is interior, so it survives |
| `pkg/agent/retro_bm25.go:71` — `retroTokenize` | `strings.FieldsFunc` splitting on every non-letter, non-number **rune** | `["don","t"]` — split at the apostrophe |
| bleve's `en` analyzer | Unicode segmentation + **Porter stemming** + stopword removal | a stemmed form, and `"don't"` may be dropped as a stopword entirely |

*(The brief that prompted this decision stated the split without saying which tokenizer did
which; the table above is from reading both functions. `bm25Tokenize` is the one that keeps the
contraction whole, because it trims edges only.)*

This is **harmless today** only because the three never rank the same corpus. **FR-021 already
requires Go-side evaluation over the candidate set** — so bleve selects candidates with one
notion of a term and Go ranks them with another. The mismatch **will** become live, and its
symptom is the worst kind: a document that matched during selection scores as though the query
term were absent. No error, just a ranking that is quietly wrong.

**Required before Go-side ranking ships (W2):** either

- **one shared token function**, threaded through the `NewBM25Engine` call site
  (`pkg/utils/bm25.go:72`; the existing caller is `pkg/tools/search_tool.go:41`); or
- **an explicit, documented decision that the Go pass is deliberately unstemmed**, stating why
  and what it costs.

Either is acceptable. **Silence is not** — that is the choice being made by default today.

**D21.6 — Why no embeddings is correct, with evidence.**

The constraint against embeddings is usually defended as a footprint decision. It does not need
to be, and defending it that way concedes a point that the evidence does not require:

- **Claude Code dropped its vector index** — agentic lexical search retrieved code better.
- ***"Is Grep All You Need?"*** (arXiv 2605.15184) — inline grep beat inline vector retrieval on
  **every harness-model pair tested**, by up to **86.2% vs 62.9%** at the widest.
- ***"BM25 Wins at Scale"*** (arXiv 2607.26497) — file-system agentic exploration wins below
  ~10M corpus tokens and BM25 wins above it, with graph-RAG **construction** costing up to
  ~102B tokens.

**A 5,000-note vault is roughly 0.5–3M corpus tokens.** That sits *permanently* in the regime
where the agentic loop over good lexical ranking wins — not near a boundary we might cross, but
an order of magnitude inside it.

> **The constraint is the winning position, not a compromise.** We are not doing lexical
> retrieval because we cannot afford embeddings. We would choose lexical retrieval at this scale
> with an unlimited budget.

*(Scope: these are cited findings, consistent with §6's standing caveat that this ADR does not
claim to have independently reproduced published research. The vault-token estimate is ours.)*

### D22 — The response the model reads is part of the retrieval design

**New in revision 5,** and it records the single most surprising finding put to the council:

> Changing result delivery from **inline** to **file-based** collapsed accuracy from **93.1% to
> 55.2%** (arXiv 2605.15184) — **as large a swing as replacing the retriever entirely.**

That reframes the response format from presentation to mechanism. A retrieval system that finds
the right notes and renders them badly is a worse retrieval system. Accordingly:

**D22.1 — Compact text, never JSON, to the model.** Revision 4 made this call for
`record_schema` alone (Notion's measured ~91% context-token reduction); it now applies to
**every result from all five tools**.

> **This does not touch the wire.** The type crossing the gateway/SPA boundary **remains a
> contract-defined JSON schema per Hard Constraint #8** — `RecordQueryResponse` and the rest of
> D19 are unchanged, generated, and validated. What changes is the **rendering inside the
> tool-result content block the model reads.** These are two different surfaces and this ADR
> changes exactly one of them.

**D22.2 — Completeness FIRST, in the header, not last.** D13 requires the completeness verdict;
D22 fixes where it goes. **The model must not have to read to the bottom of a table to learn the
answer was partial** — a model that has read 40 rows has already begun composing an answer, and
a caveat arriving after them is a correction competing with a conclusion.

**D22.3 — Exclusions named inline, WITH the fix.** Not *"3 records excluded"* but
*"`CO-0052`: value is `'50k'` where a number is required."* The reader must be able to act
without a second call.

**D22.4 — Joined values are marked as borrowed.** A value pulled through a relation renders as
`company [[Acme]]: …`, **never merged into the row's own columns.** The row is still one real
file, and blurring that is how an agent comes to believe a property exists on a note that does
not have it.

**D22.5 — Totals state their scope.** *"2 matched, GBP only"* — **never a bare number.** This is
D13's cross-currency refusal (O-2) carried into the rendering, where it is actually read.

**D22.6 — Every response ends in addressable next actions.** In an agentic loop **each response
is a prompt for the next call.** A response that terminates in data terminates the loop; one
that ends in *"narrow by `status`, or `near: [[Acme]] hops:2`"* continues it.

**D22.7 — Token budget.** ~50–80 tokens per hit; ~1,000 tokens default; **hard cap 4,000, with
truncation stated in the header** (D22.2, and D15.5b's rule that every breach is reported). A
`minimal` mode at ~20 tokens/hit for wide scans.

**D22.8 — Tool DESCRIPTIONS are the binding constraint, not tool count.**

This is the part that is easy to get backwards. Tool *count* costs selection accuracy (D15.0).
Tool *descriptions* cost **tokens on every turn, forever**, for every agent that has the tool —
whether or not it is ever called.

**Budget ~150 tokens per tool description.** Push operation detail down into **parameter
descriptions and error messages**, which are paid only when relevant: an agent learns the
`set_property` arity rule from the error it gets, not from a paragraph every other agent carries
on every turn. Learn-on-demand, not learn-in-advance.

*(Five tools at ~150 tokens is ~750 tokens of permanent context. Eighteen would have been
~2,700.)*

### D23 — Schema and view authoring are ordinary writes; mounting is not

**New in revision 5, and corrected mid-drafting after an operator ruling.** The correction is
recorded because the first framing was wrong in an instructive way.

**D23.1 — Mounting: reuse `request_mount`, do not reinvent it.**

The operator-approval pattern **already exists** and is verified:

```go
// request_mount (ADR-063 FR-7.2): seeded "ask" everywhere — the whole
// point is that the operator approves each folder.
"request_mount",
```

— `pkg/coreagent/core.go:365-367`. It is in the static catalog, seeded `ask` **everywhere**.

**Mounting a folder therefore stays `request_mount`, unchanged.** This ADR specifies **no new
agent-callable mount**, and no `vault_*` tool mounts anything. Adding a second mount path would
give the vault surface a way around a control ADR-063 deliberately placed.

**D23.2 — Schema and view authoring: ordinary writes, governed by ordinary tool policy.**

An earlier draft of this decision proposed a bespoke *propose-and-approve* flow for schema
changes, by analogy with `request_mount`. **That was wrong, and the operator corrected it
directly:**

> *"i was talking about mounting, what is scheme change? the agents should be able to manage the
> vault completely when they have the write enabled tools, that includes creating and changing
> bases"*

So: **an agent with write-enabled tools manages the vault completely — and that explicitly
includes creating and changing record types and creating and changing saved views (bases).**
There is no separate approval mechanism, no `request_schema_change`, and no UI-ratifies step.
These are writes, and writes are already governed.

**D23.3 — Placement: the control plane is its own tool. REWRITTEN in revision 6.**

> **Revision 5's version of this table was wrong in two of its four rows and its closing
> sentence.** It put new-type creation in `vault_edit` on the false premise that nothing existing
> is reinterpreted (refuted in D15.6 against this ADR's own D1), and it then told an operator
> that `vault_restructure` protects schemas — a control that, as revision 5 shipped it, did not
> exist. Both are corrected here rather than softened.

**Every schema and view operation lives in `vault_configure` (D15.6).** The placement table is
there, not duplicated here.

**What revision 5 claimed and revision 6 withdraws.** Revision 5 presented this placement as
*"evidence the criterion is the right one… classified them correctly with no special-casing and
no new rule."* **That claim is withdrawn.** It was not one criterion generalising; it was two
readings of one sentence, chosen per row to reach the desired tier (D15.1). The criterion did
need a new rule, and D15.1 now states it as **C-B** — a second, named criterion, presented as
such.

This is worth recording rather than deleting, because the failure is instructive: a criterion
that appears to generalise effortlessly is exactly the kind of claim that should attract more
scrutiny, not less. **The design is not weaker for having two criteria. The document was weaker
for pretending it had one.**

The practical consequence, stated correctly this time: **an operator who wants to protect the
vault's type system sets `vault_configure` restrictively.** It is its own lever, independent of
both `vault_edit` and `vault_restructure`, which is the point of splitting it out.

**D23.4 — One honest asymmetry between the two cascading tiers.** `vault_configure` cascades in
**meaning** (C-B); `vault_restructure` cascades in **bytes** (C-A). A schema change also **writes
only one file**, so reverting that file undoes it — which is not true of trash. So schema
authoring is the *less* destructive of the two, and it has its own tool partly for that reason:
grouping it with trash would have implied a severity it does not have.

It is still a cascading tier, not a per-file one: **notes the agent never named change status**,
which is C-B, and D15.3 already establishes (for trash) that **recoverability and blast radius
are different axes**. The asymmetry is recorded so nobody later reads `vault_configure` and
`vault_restructure` as interchangeable.

**D23.5 — Dropped from the earlier draft: "a schema change must never be `allow` for anyone."**
That was over-reach. **An operator may absolutely grant it.** The *default* is conservative
(D18's `ask` across the roster for `vault_restructure`), but the decision is the operator's,
consistent with how every other write policy in this system works. A default is not a
prohibition, and this ADR should not smuggle one in as if it were.

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

## 2.6 A defect alleged during this council — investigated and **NOT CONFIRMED**

*New in revision 5.* The council was asked to record, as a found defect, that vault **indexing
progress is emitted but throttled into invisibility**: that `DefaultProgressInterval = 200ms`
plus a coalescer that sets `lastAt` at construction means **an index completing in under 200 ms
emits ZERO frames**, matching a manual observation that the UI reported no indexing progress.
The proposed fix was **edge-triggered emission at both ends** — a first frame immediately on
phase entry, and a terminal frame regardless of throttle — landed inside `progressCoalescer` so
every consumer inherits it.

**The two cited facts are true. The conclusion drawn from them is not, and the proposed fix is
already implemented at both ends.**

| Claim | Verified? | What the code actually does |
|---|---|---|
| `DefaultProgressInterval = 200 * time.Millisecond` | **True** | `pkg/knowledge/index.go:242` |
| The coalescer sets `lastAt: now()` at construction, commented *"The clock starts now, so the first update waits a whole interval"* | **True** | `pkg/knowledge/index.go:301-304` |
| A leading-edge frame is missing | **FALSE** | `pkg/gateway/knowledge_lifecycle.go:836` emits a frame **immediately on `BeginIndexing`, before `syncWith` runs.** The `lastAt: now()` line is *deliberate and correct*: the leading edge is emitted one line earlier by the caller, and firing the coalescer too would duplicate it in the same millisecond — which is precisely what its comment says. |
| A terminal frame is missing | **FALSE** | `progress.flush(stats.Indexed + stats.Unchanged)` — `pkg/knowledge/index.go:829`. **`flush` bypasses the interval check entirely** (`index.go:325-330`). `SyncOptions.ProgressInterval`'s own doc comment (`index.go:228-230`) states the contract: *"The final call of a run ignores it, so a run shorter than one interval still reports its result exactly once rather than not at all."* `tracker.Finish` (`knowledge_lifecycle.go:845`) emits again after it. |
| *"An index completing in under 200 ms emits ZERO frames"* | **FALSE for any non-empty collection** | It emits a leading frame **and** a terminal frame. |

**The only genuine zero-frame cases are both deliberate and documented:**

1. **An empty collection** (`total == 0`). `firstIndex` guards the leading frame with
   `if total > 0`, and its comment states the intent: *"BeginIndexing(0) returns the tracker to
   idle rather than entering a phase whose only rendering would be '0 of 0', so an empty
   collection deliberately never produces an indexing frame at all."* `flush(0)` then also
   returns early on its `indexed == c.lastN` guard.
2. **An incremental / reconcile run.** The running report is wired into `firstIndex` only —
   *"the running report is wired here and not in reconcile: an incremental run has no measured
   denominator, and a count that rises against no total is not progress, it is a number nobody
   can read."*

Either is a far likelier explanation of the manual observation than a throttle bug.

**Why this is recorded rather than quietly dropped.** This ADR's D16 table exists because three
consecutive revisions asserted properties of code that nobody had read. **Writing an unverified
defect into the revision whose whole subject is verified retrieval would have been that same
failure, committed while correcting it.** The finding is kept as a finding of *no defect*.

**What is left over, and it is small:** if an operator should be told that an empty collection
indexed successfully, that is a **product decision about empty-state rendering**, not a
throttling fix, and it belongs in W6's human surface. No change is made to `progressCoalescer`.

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

**Reinforced 2026-08-28 by explicit operator direction**, which settles the priority the two
arguments above only imply:

> *"the tools must be really really good optimised for our vault even if that means to sacrifice
> obsidian support; I'd rather have an obsidian import functionality than tools that cannot be
> best in class."*

That is decisive for revision 5's shape. D15's five-tool surface, D21's fielded index, and
D22's compact rendering are all designed against *our* vault, and several of them — `near`
composed with filters (D15.3), property-key field queries (D21.2) — are not expressible in
`.base` at all. A compatibility constraint would have forbidden the best parts of this design.

**And the import story is already shipped, which removes the usual cost of this choice.**
Mount-in-place already covers Obsidian and generic Markdown + frontmatter vaults with **no
import code whatsoever**: `.obsidian/` is *detected and never written*
(`pkg/knowledge/detect.go:100` — *"HasObsidianMarker reports a `.obsidian/` DIRECTORY at the
root"*; `detect.go:134-135` further refuses to follow a symlink named `.obsidian`). An operator
points Omnipus at an existing Obsidian vault and it works, without conversion and without
Omnipus writing into Obsidian's own directory.

So the launch import story is **mounting, and it exists**. The highest-value importer still worth
building is therefore the narrow one: the **one-shot saved-view importer** (D15.4, O-1) — which
is exactly where revision 5 puts it, as an operator/CLI tool rather than a permanent agent tool.

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

### 3.6 A dedicated aggregation store (the D16 fallback) — **TAKEN in revision 5**

> **Superseded 2026-08-28.** This alternative is **no longer an alternative** — D16.2 adopts it,
> in the two-index form (bleve keeps text; a derived, disposable SQLite index holds properties
> and relations). The section is kept because its argument is the one D16.3 relies on, and
> because this ADR's convention is to leave the reasoning visible rather than rewrite it once
> the answer is known. **The two conditions it set are both now discharged explicitly:** the
> ADR-067 A2 overturn and the CLAUDE.md widening are recorded in **D16.4**, in this ADR, rather
> than deferred to another one — the "own ADR" this section asked for is the revision you are
> reading.

**The original text, unchanged:**

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
- The vault remains a plain set of Markdown notes; uninstalling Omnipus loses the indexes and
  nothing else.

*Added in revision 5:*

- **The vault subsystem costs 6 tool definitions instead of 18** — a catalog of **95 rather than
  107** (D15.0), in a regime where selection accuracy is already the binding constraint.
  *(Revision 5 printed this as "98 rather than 111", which was doubly wrong: 98 is the count
  today, and 111 came from the same miscount. Working: 98 today, 98 + 9 = 107 under revision 4's
  shape, 98 − 9 + 6 = 95 under this one.)*
- **The tool boundary is the policy boundary** (D15.2). An operator can permit editing while
  forbidding restructuring — a posture that is inexpressible today (D18).
- **An agent can read a note through an audited tool**, and obtain a version token without
  sending a write it knows will fail (D15.3).
- **"Notes mentioning X within 2 hops of `[[Y]]`" is one call** — a query no system in this
  ADR's research corpus can express (D15.3).
- **Search will rank with BM25 rather than TF-IDF**, and over real fields rather than
  frontmatter flattened into prose (D21.1, D21.2).
- **Joins, `OR`, `GROUP BY` and aggregates come from a store that already does them**, so the
  expression engine §4.2 names as this ADR's highest-risk component shrinks (D16.2).

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

*Added in revision 5:*

- **A second index exists, and two indexes can disagree** (D16.4). This is a genuinely new
  failure mode of exactly the class §1.3 catalogues, and it is why the freshness-token
  comparison is structural rather than a test.
- **The CLAUDE.md rule isolating SQLite to WhatsApp session storage is widened**, and ADR-067's
  A2 is overturned (D16.4). Recorded here rather than discovered in a diff.
- **This ADR decides against its own spike's recommendation** (D16.3). The grounds are the
  spike's own out-of-scope note, but a future reader must be able to see that the override
  happened and judge it — hence D16.3 rather than a quiet adoption.
- **Nine `knowledge_*` tool names are retired.** Any prompt, skill or seeded policy referencing
  them must move. The migration is cheap now and gets more expensive with every vault that
  ships.
- **Two operations in D15.3 are unbuilt primitives, not relabellings** (D14.1): body-replace
  needs anchor-ambiguity rules, trash needs a soft-delete convention. Both are real design work.
- **The D21.3 signal mix is unmeasured on vault data** and is flagged in place as our own
  composition rather than a benchmarked result.

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
| ~~O-1~~ | **RESOLVED 2026-08-25 — one-way importer.** `record_view_import` reads a `.base` file and translates the filters, order and grouping it recognises into a native view. Anything it cannot translate is **reported by name**, never silently dropped or approximated. It is a one-shot translation, not a live read path: we do not take on tracking a format that broke five times in eight weeks. Lands in W6. **Revision 5: the resolution stands; the delivery surface moves from an agent tool to an operator/CLI one-shot (D15.4), because FR-101's verbatim report exists to be read and judged by a human.** | Founder |
| ~~O-2~~ | **RESOLVED 2026-08-25 — correct totals with honest gaps.** `money` is amount (integer minor units) + ISO-4217 currency + declared scale, with exact decimal arithmetic. Sums within one currency are exact; sums **across** currencies are refused with the currencies listed. **No FX conversion, no rate table, no periods, no ledger semantics, no amount audit trail.** Those would change the record model and belong to their own ADR. | Founder |
| ~~O-3~~ | **RESOLVED 2026-08-25 — structured JSON only, no query language.** `record_query` takes a typed filter object; every field name, enum value and relation target is validated against the schema before evaluation, so a typo is a rejection naming the valid options rather than an empty result. **No text query language and no parser** — Notion's ~91% token saving is real but it buys a parser we would own, and D13's whole premise is that a malformed query must fail loudly. Revisit only if transcript token cost becomes a measured problem. **Revision 5: unchanged, and note it governs the query INPUT only — D22.1's compact-text rule is about the RESPONSE the model reads, and the two do not conflict.** The tool is now `vault_find`. | Architect |
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

*Added in revision 5:*

- **It does not claim the D21.3 ranking composition is benchmarked.** BM25F and Reciprocal Rank
  Fusion are each established; *this* mix of four signals over vault data is **our composition**,
  and D21.3 says so in place. W2 must compare it against plain BM25 rather than assume it helps.
- **It does not claim the two-index design is measured.** The spike measured the design this ADR
  did **not** take (D16.3). The two-index write path, concurrent queries and every non-macOS
  platform are unmeasured, exactly as the spike's own §6.1 records for its own numbers. D20
  places that measurement in W1, before anything is built on top of it.
- **It does not claim the tool-count research transfers exactly.** The 50-tool / 200-tool
  accuracy figures and the Block and Copilot consolidations are cited findings about other
  systems' catalogs. The direction is clear and consistent across sources; the specific
  thresholds are not ours and were not reproduced here.
- **It does not claim `vault_find`'s composed `near` + filter query is unprecedented in
  general** — only that **no system in this ADR's surveyed corpus** (Dataview, Obsidian Bases,
  Notion, Tana, mdbase) can express it.
- **It makes no claim about indexing-progress emission beyond what §2.6 verified**, where an
  alleged defect was investigated and **not confirmed**.
