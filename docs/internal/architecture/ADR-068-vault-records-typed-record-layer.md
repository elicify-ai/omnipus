# ADR-068 — Vault records: a typed record layer with relations

- **Status:** Proposed (2026-08-28) — **revision 7**, after grill pass 1 over the implementing specification (BLOCK: 19 critical, 41 major, 17 minor) and **nine operator rulings**. **Revision 7 is mostly a DELETION, and that is the headline.** The largest ruling reverses D16.2b: **the properties index NARROWS CANDIDATES; our own tested comparator DECIDES.** SQLite evaluates no comparison. That makes D16.6's nine violations **not applicable rather than defeated** — a stronger and far simpler position than nine deliberate defeats, **none of which was verified and zero of the seven the spec had specified turned out to be sufficient.** Also deleted: the **`money` type** in full (D3, O-2 — the requirement is a precise decimal and an int64, not a currency-carrying value); **enum ordering by declared position** (D4's second clause, replaced by SQLite's own lexical order with a value prefix for domain order); and our **invented filter-operator vocabulary**, replaced by SQL's (O-3, amended). Added: `number` splits into **`integer` and `decimal`**; **case-insensitive matching is a feature**, and it is a comparator rule because SQLite folds no non-ASCII at all; **no hardcoded domain vocabulary anywhere** (D0, strengthened with the operator's "empty database, all capabilities, nothing predefined"); and the corrections grill pass 1 earned — a re-derived freshness ordering with its residual carried as a **stated open risk**, four wrong code citations, and a tool-cost argument computed against the wrong denominator.
  - *Revision 6:* after a fifth adversarial review (BLOCK: 8 critical, 25 major, 3 minor — `ADR-068-vault-records-typed-record-layer-review-round5.md`). Revision 5's own headline numbers were wrong: the catalog is **98 tools, not 102**, and the two-index staleness mitigation named a **freshness token that does not exist**. Both are repaired below, the second by specifying the mechanism rather than asserting it again. The agent tool surface grows from five `vault_*` tools to **six** — the control plane gets its own policy lever (D15.6). D16's latency argument is **withdrawn as unevidenced**; the capability argument, which is the one that survives, now carries the decision alone. And **D16.6 is new**: SQLite's default semantics contradict **nine of the thirteen** comparison rules the spec's §8 oracle defines, eight of them silently — the strongest single consideration bearing on D16, which revision 5 omitted entirely while the implementing spec carried it.
  - *Revision 5:* three-agent design council; D16 resolved to a two-index design; nine `record_*` tools cut to five `vault_*`; D21, D22, D23 added.
  - *Revision 4 and earlier:* proposed 2026-08-25 after three adversarial reviews (BLOCK each time: 7, 8, then 10 critical). Revision 1 made three false claims about existing code (D14, D16, D18); all three are corrected in place and the corrections are marked. D16 had been wrong three times and was deliberately left unresolved behind a measured spike.
- **Verification standard (revision 5's, restated because revision 5 breached it):** every claim about our own code below cites `file:symbol` or `file:line` and was read at revision time. Revision 5 declared this standard and then broke it twice, in its two load-bearing decisions. **The rule for revision 6 is narrower and harder: a mechanism this ADR relies on either exists and is cited, or is specified as NEW WORK with what it costs — never named as though it already worked.** The freshness token (D16.4) is the test case: it is now specified against the per-file hash the manifest already stores, with the part that must be built named as such.
- **Where this revision disagrees with its review.** Three of revision 6's review claims did not survive checking and are **rejected with evidence**, in place: the spec-is-stale claim (C-4's tail), the "drop `and Matrix`" half of M-24 (Matrix genuinely uses SQLite; the defect was the missing citation), and the BM25 undercount arithmetic (M-23 says twelve; the enumerable count is **thirteen**). **Revision 7 rejects three more, from grill pass 1, and each was re-executed rather than argued:**
  - *"The shipped engine reports `sqlite_version()` = **3.53.3**, two minor versions ahead of the 3.51.0 the receipts were taken on."* **It reports `3.51.2`** — verified by opening a database through `modernc.org/sqlite v1.46.1` (`go.mod:64`) and asking. A patch, not two minors. **The standard behind the finding is adopted anyway**: the engine and its version are now named in place, and a test asserts the linked version, because affinity and collation are version-sensitive.
  - *"`SELECT id FROM t WHERE NOT (a=1 OR b=2)` returns **ZERO rows**."* It returns **one** (`id 2`). The finding is still CRITICAL at its true value — the correct answer is **four** rows, so three are silently dropped — but a wrong number inside a document about wrong numbers is not a small thing.
  - *"`repairAndValidateToolPolicyCoverage` emits **two** WARNs, not one."* It emits **`1 + N`** — one at `pkg/gateway/gateway.go:975`, plus one per repaired agent at `pkg/config/validate.go:576` — and **zero** when nothing needed repair. The **citation** half of the same finding is upheld: the function is at `gateway.go:968`, not in `pkg/config/validate.go`.

  A review is evidence, not an oracle, and complying with a wrong finding would be the same failure as ignoring a right one.
- **What revision 7 deletes, listed once so the shrinkage is auditable.** D16.6's nine defeats (superseded — the violations remain, as reasons); the `money` type, `RecordMoney` and cross-currency refusal (O-2, superseded); D4's declared-position ordering (superseded by ruling R-E); our filter-operator vocabulary (O-3, amended); D16.2b's "the properties index answers every typed predicate" (reversed). **Every deletion is marked at its own decision rather than only here.**
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
sat beside: this ADR now owns **six `vault_*` tools** that replace both the nine `record_*`
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

**D0.1 — STRENGTHENED, revision 7, operator ruling, in the operator's own words because they are
sharper than anything this decision had:**

> **"We should not have any hardcoded CRM enums at all. We create a generic vault system where
> enums like that must very clearly be defined by agents and not hard coded — like an empty
> database, all capabilities but nothing predefined."**

**This does not change D0; it makes D0 TESTABLE, and it says why that was necessary.** D0 already
forbade shipped record types. What it did not do was reckon with its own prose: **this ADR uses CRM
vocabulary fourteen times and the implementing specification thirty-three times**, always as
illustration, and an implementer skimming forty-odd mentions of "deal", "company", "stage" and
"arr" could reasonably conclude the product knows what those are. **A rule that is stated once and
then quietly undermined by every example is a rule that will be broken by someone acting in good
faith.** So:

- **Every occurrence of `company`, `deal`, `contact`, `lead`, `meeting`, `person`, `status`,
  `stage`, `arr`, `open`, `won`, `lost`, `prospect`, `Acme`, `Northwind`, `CO-0142`, `DEAL-0117`
  and their kin, in this ADR and in the specification, is an ILLUSTRATION OF WHAT A VAULT MIGHT
  DEFINE.** None is shipped, seeded, defaulted to, validated against, or known by name to any
  compiled artifact. **The spec states this in a boxed note before its requirements begin, and
  every table of illustrative strings carries the same marker.**
- **A fresh install has ZERO record types**, zero seeded enum values, zero seeded property names,
  zero seeded views and zero seeded identifier prefixes. A vault with no `.omnipus-vault/records/`
  directory is a working vault of ordinary notes (D1's premise, unchanged).
- **A denylist test asserts it** over every non-test file of the record packages — spec **FR-004a**
  and its test 54. **Verified at revision time: the code is already clean.** Zero domain vocabulary
  outside tests in `pkg/records` and `pkg/knowledge`. **The test exists to keep it that way**, not
  to repair something — and it is worth having precisely because the documentation drifted from the
  rule while the code did not.
- Test fixtures may use domain vocabulary, and they do. **Fixtures are excluded by path**, narrowly
  and by name, rather than by a general "except where inconvenient".

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
| `integer` | a signed 64-bit whole number, bound-checked and refused outside int64 | a count silently widened to a float, or a large identifier silently truncated |
| `decimal` | an exact, arbitrary-precision number, `unit` declared as metadata rather than glued into the property name | `exercise: 60 minutes` — text to one engine, duration to another, sortable in neither. And every quantity that a binary float rounds without saying so |
| `person` | a relation to a person record, distinct from a name typed as text | the same concept modelled both ways in one vault |

> **REVISED IN REVISION 7 BY OPERATOR RULING, and the count did not change even though two types
> did.** `money` — *"amount + ISO-4217 currency + scale, as **one** value"* — is **DELETED**, and
> `number` is **SPLIT** into `integer` and `decimal`. The requirement, verbatim: *"we do not need a
> real money type with currency, only a precise decimal datatype and also an integer 64 datatype."*
>
> **The arithmetic is worth stating because it is easy to get wrong in the other direction:**
> −1 (`money`) −1 (`number`) +2 = **still seven types**. What changed is the membership, not the
> count. This heading stays "Seven property types" and is not stale.
>
> **Why the split, rather than one `number` type:** with one numeric type the index must infer a
> column type from the first value it sees, so the same property could be an integer column in one
> vault and a text column in another, and `2` versus `2.0` would compare differently depending on
> which note was indexed first. **The author chooses; the storage follows the schema.** For
> comparison purposes the two are **one declared type** (spec R-1): `3 = 3.0` is true, and an
> `integer` compares with a `decimal` numerically. The split decides storage and bounds, not a
> comparison domain.
>
> **`decimal`'s precision bound is 100 places, and it is deliberately generous.** The retired
> `money` type bounded scale at **12** (`maxMoneyScale`, `pkg/records/decimal.go:166`) — a
> currency-shaped limit for a type that is not currency-shaped. **That bound is deleted with money
> and must not be inherited.** `decimal` takes the parser's own `maxDecimalScale = 100`
> (`decimal.go:48`), which is already enforced and already has a property test sweeping every scale
> in `[-100, +100]`. A value above it is **refused naming the bound and its own scale, never
> rounded** — rounding to satisfy a bound is a silent change to a number.
>
> **`pkg/records/decimal.go` (588 lines) survives money's removal intact and is the valuable core:**
> `math/big`-based, `Decimal` = `unscaled *big.Int` + `scale int32`, with `float64`/`float32`
> appearing on exactly two lines of the file, **both comments**. `pkg/records/money.go` (281 lines)
> and its three test files (1,276 lines) become dead — the specification's §10a enumerates them,
> with `RecordMoney.yaml` and its contract references, as scheduled deletions.
>
> **What goes with `money`, so nothing is left dangling:** O-2's cross-currency refusal, D16.6's
> R-6 row, D13's cross-currency sentence, D22.5's example, the spec's R-6 rule, FR-012/013/014 in
> their old meanings, `CurrenciesPresent`, ISO-4217 handling and every currency-first/amount-first
> parsing path. **Multi-currency conversion, previously "pending O-2", is now out of scope
> permanently** — there is no money type to convert.

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

### D4 — Enums are closed. Ordering is SQLite's, and a domain order is a value prefix.

> **TITLE AND SECOND CLAUSE REVISED IN REVISION 7 BY OPERATOR RULING.** The heading was *"Enums are
> closed and ordered; ordering is data, not spelling"*. **The "ordered" half is withdrawn**; the
> ruling: *"the enum ordering is following SQLite standard; if we need different ordering we need
> to prefix the content."* **"Closed" is unchanged and is the half D4's evidence actually
> supports.**

An enum declares its permitted values as a **set**. Writing a value outside the set is
**rejected**, with the permitted values named in the error. **Sorting an enum column sorts
lexically — SQLite's own ordering.** An author who wants a domain order writes it into the values:
`1-lead`, `2-qualified`, `3-proposal`, `4-won`.

**Why closed:** an agent writing an invented value must be corrected, not silently create a second
de-facto value. Notion's multi-select auto-creates an option on any typo; the observable result in
real vaults is three spellings of one state in one column.

**Case-folding is NOT the thing "closed" forbids, and revision 7 says so explicitly because the
spec previously read it that way.** Under the case-insensitivity ruling, a value differing only in
case **resolves to** the declared value — collapsing two spellings into **one**. Auto-creating a
new option invents a **second**. D4 forbids the second. The specification's revision 4 had enum
equality as exact-case, which rejected a value D4 has no quarrel with.

**What the ordering ruling deletes:** a derived ordinal column, the schema bookkeeping that kept it
in step with the file, the `NULLS LAST` requirement that came with it (SQLite sorts NULL first
ascending, so a value with no ordinal headed page one), and an unwritten rebuild obligation — an
enum **reorder** changed the derived ordinal for every record of the type, and no requirement
anywhere said the index had to be rebuilt for it.

**The cost is real and is accepted rather than glossed.** §1.4 of this ADR cites the
`1-Pending…7-DoNotContact` prefix hack as a **documented failure** of the incumbents, and this
ruling adopts it as the mechanism. **The trade is visibility.** The prefix sits in the operator's
own file and does exactly what it looks like it does. A derived ordinal was a second source of
truth for the order, invisible in the vault, drifting from the schema file on every reorder, and
capable of changing the ordering of every existing report while the cascade block reported *"0
records lost validity"*. **A convention the operator can see and change beats a mechanism they
cannot see and we have to maintain** — and the incumbents' version of this hack was bad because
their column types were bound vault-wide and unfixable, not because a prefix is a bad way to say
"this comes first".

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

**AC-7.3** *(new in revision 6)* — **the accepted Windows risk gets the acceptance criterion its
mitigation always needed.** D7.1 accepts collisions and says *"what we ship instead is honest
detection"* — and then AC-7.1 tests POSIX allocation and AC-7.2 tests the high-water mark, so
**nothing tested the detection path at all**. An accepted risk whose entire mitigation is
untested is an accepted risk with no mitigation. So: a vault seeded with two records sharing an
identifier produces a `check_integrity` finding **naming both paths**, and no query silently
prefers one over the other. This is testable on every platform, including the one where the
collision cannot be prevented — which is the point.

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

**D13.1 — There is exactly ONE exception to "names what it excluded", and it is stated here
rather than buried in the decision that makes it.** *(New in revision 6.)*

**Workspace scoping (D15.5a) is a silent exclusion.** A vault mounted only into another workspace
returns an **empty result, not a permission error**, and by design the caller *cannot distinguish
it from the vault being empty* — that is AC-15.5a, word for word. So a caller can receive
`complete: true` over zero records while records exist that were deliberately withheld: a
confidently wrong answer, which is §1.3's failure class and D13's own prohibition.

**The security argument wins, and it is not close.** Naming an excluded record — even as an
opaque count — turns the error channel into a probing oracle: an agent in workspace A could
enumerate what exists in workspace B by watching the exclusion count move. ADR-067's
FR-052/FR-053 settled this for search and the reasoning transfers unchanged. **Honesty about
what a query excluded is a rule about the caller's own scope. It was never a rule about the
boundary of that scope, and it cannot be one without dissolving the boundary.**

Recorded because an unstated exception to a headline guarantee is how a guarantee stops being
believed — and because a reader who finds this collision themselves, in a document that claims
*"there is no call shape that returns records alone"*, will reasonably wonder what else was not
mentioned.

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

### D15 — The agent tool surface: six `vault_*` tools, subsuming `knowledge_*`

**Revised 2026-08-28 (revision 5).** Revision 4 specified nine `record_*` tools. That is
withdrawn. The surface is **six `vault_*` tools** (five in revision 5; D15.6 adds the sixth),
and they **also replace the nine
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

**18 → 6 is the decision.** Not 18 → 14, and not "add nine now and consolidate later" — a tool
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

It also carries **`check_integrity`** — a whole-vault health sweep. This absorbs `record_schema`
and the vault-wide sweep half of `record_validate`.

**`check_integrity` reports, and revision 6 widens the list:** duplicate identifiers (D7.1),
unresolved and mistyped **relations** (D5.1), **unresolved ordinary wikilinks and orphan notes**,
and rows in the properties index with no note behind them (D16.5).

> **The wikilink half is new in revision 6, and it closes a capability loss revision 5 did not
> notice.** `knowledge_graph`'s five operations are `links`, `backlinks`, `unresolved`, `orphans`,
> `neighborhood` (`pkg/knowledge/tools.go:643-647`). Revision 5 mapped `neighborhood` onto
> `vault_find`'s `near`/`hops` and `links`/`backlinks` onto `vault_read`'s inline links — but
> `unresolved` and `orphans` today cover **ordinary wikilinks across the whole vault**, and
> revision 5's `check_integrity` covered only typed *relations*. **Most notes in a vault are not
> records**, so a vault-wide broken-link report would have had no home in the new surface at all.
> Naming it here is cheaper than discovering it after the nine `knowledge_*` names are gone.

**`check_integrity` is BOUNDED, and revision 6 states the bounds** — revision 5 called it *"an
argument-free whole-vault health sweep"* and D15.5b's bounds table omitted it entirely, which
would have made it the most expensive operation in the ADR and the only one with no stated limit,
in a document whose §1.3 headline evidence is unbounded operations returning silently wrong
answers.

> **Partial correction to the round-5 review (M-5).** The review says *"Today's nearest equivalent
> is bounded: `knowledge_graph` clamps `hops ≤ 3` and `max_nodes ≤ 500`."* **Those clamps apply to
> `neighborhood` only** — `pkg/knowledge/tools.go:812-826` reads them inside `case
> GraphOpNeighborhood`, and `MaxNeighborhoodHops = 3` / `MaxNeighborhoodNodes = 500` live in
> `pkg/knowledge/graph.go:36-38`, applied in `graph.go:307-315`. The whole-vault sweeps are
> `resp.Links = toGraphLinks(g.Unresolved())` and `resp.Nodes = g.Orphans()`
> (`tools.go:809-811`) — **no clamp at all**. So this is a **pre-existing unbounded surface being
> inherited**, not a new one being introduced. The finding stands regardless: inheriting an
> unbounded sweep into a tool this ADR advertises as bounded would be worse than shipping one.

`check_integrity` therefore takes **an optional scope** — a record type, or a collection — and
its bounds are in D15.5b's table with everything else. Unscoped is permitted and is the common
case; it is the *unbounded* part that is not.

**2. `vault_find` (READ). The ONE retrieval path.** Plain words, typed filters, saved views,
relation joins, `kind: task`, and `near: <path>` with `hops` for "within N link steps". It
absorbs `record_query`, `record_explain` (now an `explain: true` flag rather than a tool),
`knowledge_search`, `knowledge_tasks`, and link-neighbourhood traversal.

**`kind: task` — the mechanism, because revision 5 promised the absorption and specified
nothing.** *(New in revision 6; the review's largest unbacked-claim finding, and it was right.)*

The gap is real: this ADR's own D16 table states that `indexDoc.Kind` only ever holds `note` or
`attachment` (`pkg/knowledge/scan.go:45,48`), so a `kind` filter over today's index selects
nothing. And what `knowledge_tasks` actually does is not a record query at all — it walks the
collection with `WalkContained` (`pkg/knowledge/authoring_tools.go:1398`), reads each file
(`:1420`), matches `^[ \t]*[-*+][ \t]+\[([ xX])\][ \t]*(.*)$` per line, bounds itself at
`TasksMaxFiles = 5000` (`:1246`, clamp reported at `:1420-1425`), and returns **many rows per
file** — `path` / `line` / `status` / `text` per checkbox. **That does not fit D22.4's "the row is
still one real file" model**, which is the model the whole response format is built on.

Two options were open. **A checkbox is indexed as its own row, in the properties index.**

- A task row carries `path`, `line`, `status` (`open` / `done`), `text`, and the `source_hash`
  D16.5 requires — the same shape every other row has, so the freshness comparison, the bounds,
  the pagination and the rendering all apply unchanged.
- **D22.4's rule is amended, narrowly and explicitly**: a row is one real *thing at a path* — a
  note, or a checkbox line within one. A task row renders with its line number, so a reader is
  never able to mistake it for the note. **That is a genuine amendment to D22.4 and is marked as
  one rather than absorbed silently.**
- **The regex walk does not survive.** `TasksMaxFiles = 5000` is a bound on *reading*, and it
  exists because the walk re-reads every file on every call. An indexed task disappears from the
  query path entirely, and D15.5b's ordinary bounds replace that one.
- **Cost, stated:** checkbox extraction joins the indexing pass, so indexing does slightly more
  work per note. That is the trade — a per-index cost paid once for a per-query cost paid every
  time, over a corpus D15.5b caps at 100,000 notes.

The rejected option was *"`vault_find` keeps a whole-collection regex walk for `kind: task`"*,
which would have needed its own bound, its own cap, its own rendering, and its own
completeness story — four exceptions to make one tool absorb another, which is not absorption.

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

**D15.5a — Workspace scoping.** Every one of the six `vault_*` tools resolves through the calling agent's
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
| Candidate set counted before retrieval | 10,000 records | **refused** with a narrowing instruction naming the filter that would help. Hard, per the spike's C-3 (D16.3a) |
| Relation hops per query | 2 | refused; deeper traversal is a follow-up query, not an implicit walk |
| Aggregation over a refused set | — | never partial. No total is returned at all |
| **`check_integrity` findings** *(new)* | 500 per category | reported as clamped, with the count that would have been returned and the scope argument that narrows it |
| **`check_integrity` notes swept** *(new)* | 100,000 | refused, naming the collection; the scoped form is the remedy |
| Rate limit | **new work — see below** | 429 with `Retry-After` |

**The rate-limit row is CORRECTED in revision 6, and the correction is the interesting part.**
Revision 5 wrote *"shared with ADR-067's `knowledgeRESTLimiter`"*, which reads as inheritance
from a shipped control. It is not:

- `knowledgeRESTLimiter` (`pkg/gateway/rest_knowledge.go:90`) is consulted at exactly one place —
  `rest_knowledge.go:691` — on the **REST** path. No agent tool touches it.
- The agent-tool path uses `checkRetrievalRate`, and a repository grep finds **three** call sites:
  `pkg/knowledge/tools.go:610` and `:749` (the two read tools), and
  `pkg/knowledge/authoring_tools.go:1330` (`knowledge_tasks`, which is a read).
- **The six authoring WRITE tools call neither.** `AuthoringDeps.RateLimiter` is declared
  (`authoring_tools.go:136`) and defaulted (`:157-159`), and its own doc comment says what it is
  for: *"RateLimiter bounds `knowledge_tasks`, which is a read"* (`:133`). No write `Execute`
  consults it.

So `vault_edit`, `vault_restructure` and `vault_configure` inherit **no** rate limit from the
surface revision 5 named. **A write-path limiter is new work, owned by W5 with the policy
seeding.** *(This is a third instance of the same habit: a control named as inherited, which on
reading turns out not to cover the case. It is worth counting them.)*

`complete: false` (D13) is set for **every** one of these, with the reason and the remedy. A
caller that ignores `problems` still cannot mistake a bounded answer for a whole one, because
`complete` is a required field (D19).

**D15.5c — Writes carry ADR-067's version token and are audited.** Revision 1's write path
bypassed both. A record write takes the same opaque content-hash version token ADR-067 D14
defines; a stale token is **refused and the refusal is audited** (ADR-067 AC-14.2). Every
mutating tool emits an audit entry per ADR-067 D19 — **`vault.edit`, `vault.restructure` and
`vault.configure`, each carrying its operation** — with agent, workspace, record ID and
outcome. *(Revision 5: the audit event carries the operation even though the tool policy cannot
read it (D15.2). Policy resolves before the call on the name; the audit record is written after
it, where the operation is known. Naming them apart keeps the audit log readable without
implying a policy lever that does not exist.)*

**The compare-and-swap contract covers `vault_edit` ONLY, and revision 6 says so instead of
implying otherwise.** Revision 5 wrote *"Every mutating `vault_*` tool… takes the same opaque
content-hash version token"*, which is not achievable and was not true of the tools being
replaced:

- **Today `knowledge_rename` and `knowledge_move` take no `expect_version` and return none.**
  Verified: neither `RenameTool.Parameters` (`pkg/knowledge/authoring_tools.go:852-871`) nor
  `MoveTool.Parameters` (`:904-927`) declares the field.
- **More fundamentally, a single-file content hash cannot guard a cascade.** A rename rewrites
  inbound links in N files; a token over the renamed file says nothing about the other N−1. A CAS
  that guards one of the files it writes is worse than no CAS, because it reads as a guarantee.

So, stated plainly:

> **`vault_edit` is compare-and-swap. `vault_restructure` and `vault_configure` are NOT.**
> A cascading write takes no version token, and its safety comes from being a **tier-5 policy
> decision an operator made deliberately**, plus the audit entry, plus `check_integrity` — not
> from optimistic concurrency it cannot honestly offer.

**Related, and worth an issue of its own because it is shipped and live:** the parameter
description at `pkg/knowledge/authoring_tools.go:413-422` tells the model that *"every knowledge
tool that touches a note returns one as `version`"*. That is **false for `knowledge_rename` and
`knowledge_move`** — the two highest-blast-radius tools in the set. The model is being handed a
false invariant about exactly the tools where it matters most. It is pre-existing and adjacent, so
it is not fixed by this ADR; it is named so it is not lost.

**AC-15.5d** — `vault_restructure`'s and `vault_configure`'s tool descriptions state that they
take no version token, and a test asserts no `expect_version` parameter is declared on either. A
capability that is deliberately absent should be absent in the schema too, not merely undocumented.

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

**And the store that answers every typed predicate does not answer it the way this ADR's rules
require.** See **D16.6** — SQLite's defaults contradict nine of the thirteen comparison rules,
eight of them silently. That is a property of the decision made here, so it is recorded under it.

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

> ### The latency argument is WITHDRAWN. Revision 6.
>
> Revision 5 justified this override primarily on **speed**, quoting the spike's §6.1 — *"a
> dedicated aggregation store would be substantially quicker"* — and arguing from S-3 that a
> typed store *"does not pay that cost at all"*.
>
> **That sentence sits inside the spike's section headed "What this does not say", immediately
> alongside "Not measured: concurrent queries; the record write path; … any non-macOS
> platform."** Revision 5 quoted the spike's own disclaimer of an unmeasured opinion **as
> evidence**, and then built the decision on it. The claim that SQLite is faster here is a
> performance claim about a store nobody has benchmarked, made against a design that *was*
> benchmarked. **This ADR has been wrong three times by asserting an unmeasured property of a
> store. Doing it a fourth time, in the decision that catalogues the first three, is not a risk
> worth taking for an argument the decision does not need.**
>
> **So: the override is a CAPABILITY decision. The latency argument carries no weight in it and
> must not be quoted from it.** If interactive latency later matters, it will be measured then.

- **Capability is the whole argument, and it is sufficient on its own.** FR-021 and §3.6 want
  joins, `OR`, `GROUP BY` and aggregates. Over bleve these are all Go-side, which means we own an
  expression evaluator — and §4.2 already records that the expression layer's null and type
  semantics are *the highest-risk component in this ADR*, with a first-attempt comparator overload
  making `3 > 2` evaluate to **false**. Choosing the option that requires less of that code is a
  correctness argument, and correctness is what this ADR gates on everywhere else.
- **S-3 explains where the time goes; it does not license a claim about where the time would go
  instead.** Decode is ~99% of the cost because every candidate is retrieved as JSON and
  unmarshalled into `map[string]any` before Go can filter it. That is a measured fact about the
  bleve path. What a typed store costs to answer the same query is **unmeasured**, and W1
  measures it. *(Revision 5 wrote "does not pay that cost at all" — a prediction stated as an
  observation.)*

**D16.3a — The spike set three conditions. All three are answered by name.**

*New in revision 6. Revision 5 discharged C-1 and left the other two unaddressed, while
reversing one of them in passing — so the cap was simultaneously load-bearing (D15.5b) and a
politeness limit (D16.3).*

| Condition | Status |
|---|---|
| **C-1** — bump `zapx/v17` to ≥ v17.1.4 **and force a rebuild of existing indexes** | **Half discharged, half scheduled.** The pins are in (`go.mod:12`, `go.mod:107`). The rebuild is not, and revision 6 **splits it out of the wave sequence** — see D20. |
| **C-2** — *"Evaluate streamed, never materialised"* (3–6× lower peak RSS, no time penalty) | **CARRIED FORWARD, and it still applies.** Revision 5 never mentioned it. Pushing predicates into SQLite changes *where* rows are selected, not whether the surviving rows are streamed to the renderer: a `GROUP BY` over 10,000 rows still returns a result set that must not be materialised whole. **W1 exit criterion:** the query path holds no more than one page of rows in memory at once, asserted by peak RSS at the cap, not by inspection. |
| **C-3** — *"Enforce FR-064's 10,000-record cap as a hard precondition, and count candidates before retrieving anything"* | **UPHELD. Revision 5's relaxation is withdrawn.** |

**On C-3 specifically.** Revision 5 wrote that *"the cap becomes a politeness limit again rather
than the thing preventing a breach"* — while **D15.5b continued to specify a hard refusal at
10,000**, and the spec's FR-064 continued to require one (`spec:531`). Two decisions in one
revision, contradicting each other, on the same unmeasured premise the latency argument rested
on.

> **The cap stays a hard precondition, counted before retrieval, exactly as the spike wrote it.**
> Relaxing a bound is a thing to do *after* a measurement shows it is no longer load-bearing,
> never *because* a new store is assumed to make it so. If W1's measurements show the two-index
> path is comfortable at 50,000 records, the cap can be revisited then, in a revision that cites
> the number.

This also removes a live inconsistency: D15.5b, D16.3 and FR-064 now say the same thing.

**D16.4 — What this costs, named rather than skipped.**

1. **It widens the CLAUDE.md house rule that *"SQLite is isolated to WhatsApp session storage
   only."*** That widening is **deliberate and is recorded here** as the decision that makes it.

   > **Revision 6: recording it is not delivering it, and revision 5 only recorded it.** Root
   > `CLAUDE.md:66` still reads *"SQLite is isolated to WhatsApp session storage only"* and
   > `:62` still describes `modernc.org/sqlite` as *"pure-Go SQLite **for whatsmeow**"* — both
   > already inaccurate today, since Matrix uses it too (D16.2). **That file is loaded by every
   > agent on every session in this repository, and an ADR cannot amend it by describing the
   > amendment.** The predictable outcome is an agent reading `CLAUDE.md`, finding the
   > properties index, and treating it as a violation of a live house rule.
   >
   > **Both edits are now W1 deliverables with an exit criterion**: `CLAUDE.md:66`'s sentence
   > becomes *"SQLite is used for WhatsApp and Matrix session storage and for the vault's derived
   > properties index (ADR-068 D16); it is not a general application store"*, `:62`'s parenthetical
   > drops "for whatsmeow", and **ADR-067's §A2 row gains a superseded-by note** pointing here.
   > It is the cheapest item in this ADR and the one most likely to be forgotten, which is why it
   > has an exit criterion rather than a mention.

   It also overturns **ADR-067's rejected alternative A2** — *"A second SQLite user against the
   house rule, for no gain over scorch"* (`ADR-067…md:776`). That reason was assessed for
   *search*, where it was correct. For *records* the gain is aggregation, which scorch cannot do
   at all, so the premise does not transfer. §3.6 always
   said this deserved an explicit decision rather than an implementation note; this is it.
2. **Two indexes can disagree, and that is a new failure mode of exactly the class this ADR
   exists to prevent.** A text index and a properties index that have seen different generations
   of the same note produce a confidently wrong answer. **The mitigation is specified in D16.5,
   below** — revision 5 named a mechanism instead of specifying one, and that is corrected there
   rather than repeated here.
3. **The spike did not measure the write path, concurrent queries, property counts above 10 per
   record, or any non-macOS platform** (its §6.1). The two-index write path — one note, two index
   updates — is precisely the unmeasured area, and D20 places it where it gets measured rather
   than assumed. **This ADR has been wrong three times by assuming exactly this kind of thing.**
4. **The 64 MB budget is INHERITED, and it is UNVERIFIED for this design.** *(New in revision 6.)*
   D20 says the one budget that holds is ADR-067's < 64 MB steady-state RSS, *"and the properties
   index is inside it too."* That budget was measured for **bleve alone** — idle 12.9–15.1 MB,
   23.6–24.0 MB streamed at the cap (spike §5.1, §5.3). The two-index design keeps all of that
   and adds SQLite's page cache, its temp b-trees for `GROUP BY`/`ORDER BY`, and its connection
   state. **None of that is measured.** Asserting the inherited budget over an unmeasured store
   is the same move the latency argument made, and it is withdrawn the same way: the budget is
   the **target**, not a property this ADR claims. **W1 exit criterion:** both indexes, idle and
   at the 10,000-record cap, inside 64 MB — measured, on Linux as well as macOS.

### D16.5 — The freshness token, SPECIFIED. This is the finding revision 5 got most wrong.

*New in revision 6.*

**What revision 5 said, and why it was the worst sentence in the document.** D16.4 item 2 read:
*"the properties index carries **the same freshness token the text index does**, `vault_find`
compares them, and a mismatch sets `complete: false`"* — followed by *"Mitigation is mandatory and
**structural, not test coverage**."*

**The text index has no freshness token.** Verified at revision time, three ways:

- `manifestVersion` (`pkg/knowledge/manifest.go:48`) is a **struct-schema constant** — `= 1`,
  bumped by a human when the recorded *shape* changes (`manifest.go:45-47`). It never increments
  per build, and a mismatch discards the manifest and rebuilds (`manifest.go:113-115`).
- No query result carries anything of the kind. `IndexHit` is exactly
  `Path`/`Kind`/`Score`/`Offset`/`Segment` (`pkg/knowledge/index.go:159-173`).
- `VersionToken` (`pkg/knowledge/author.go:309-323`) is a **per-note compare-and-swap token on
  the authoring path** — `ComputeVersionToken(src)`, consumed by `checkVersion` at
  `author.go:667`. It is never attached to a read.

So the mitigation for the new failure mode was **neither structural nor tested** — it was a
sentence. Written into the revision whose declared subject is not doing that, three paragraphs
below a table cataloguing three previous instances of doing exactly that. **This is recorded at
this length because the pattern is the actual risk, not the individual error.**

**The specification. What follows is NEW WORK, and is written as a design, not a description.**

**The token is the per-note content hash the manifest already stores.**
`ManifestEntry.Hash` (`pkg/knowledge/manifest.go:64`) is *"the hex SHA-256 of the file's
contents"*, written per note on every index, keyed by collection-relative path in
`Manifest.Entries` (`manifest.go:82`), and already readable by path via `Manifest.Get`
(`manifest.go:174`). It exists, it is per-note, and it is exactly the value that changes when a
note changes.

| Question the review asked | Answer |
|---|---|
| **What is the token?** | The note's content SHA-256 — `ManifestEntry.Hash`. Not an integer, not a generation counter. |
| **Per-index or per-note?** | **Per-note, deliberately.** A whole-index generation would report *every* answer stale while any agent is writing anywhere in the vault, which trains D13's problem channel into noise — the failure D22.2 warns about, arriving from the other side. Per-note flags only the notes actually mid-write. |
| **Where is it stored on the SQLite side?** | One `source_hash` column per record row, written in the same transaction as the row, holding the hash the indexer computed for that note in that pass. |
| **What does the comparison do?** | For every hit `vault_find` is about to return, it compares the row's `source_hash` against `Manifest.Get(path).Hash`. **Equal → the two indexes have seen the same bytes.** Unequal, or the manifest entry is missing, or its hash is empty → the record goes into `problems` with staleness as the reason, and `complete: false`. |
| **What does it cost?** | One map lookup per returned hit, against an in-memory map, bounded by the page size (max 200, D15.5b). Not per candidate — per *hit*. |
| **When is it written relative to the index commit?** | Notes are the source of truth, so ordering is chosen to make the failure detectable rather than to make it impossible: **bleve document → SQLite row (with hash) → manifest entry**, manifest last. The manifest is already written last today and already re-indexes on a missing entry. |
| **What happens on partial write failure?** | **Both directions are caught by the one comparison, which is why it is worth having.** SQLite committed, bleve/manifest not: the manifest still holds the *previous* hash, so it differs from the row's — flagged. bleve committed, SQLite not: the row still holds the previous hash while the manifest holds the new one — flagged. A row with no manifest entry at all (note deleted, row orphaned) — flagged, and `check_integrity` reports it as an orphaned row. |
| **What about attachments?** | `ManifestEntry.Hash` is deliberately **empty for attachments** (`manifest.go:62-65`: FR-039a forbids opening one, and hashing is opening). Records are notes, so this does not arise — but the rule is written for the case anyway: **an empty hash is unknown freshness, which is flagged, never assumed fresh.** |

**What must be BUILT, named so it is not mistaken for something that exists:**

1. A `source_hash` column on every record row, and the write-path change that populates it.
2. A query-path lookup from `IndexHit.Path` into the live `Manifest`. *(`IndexHit` itself need not
   gain a field — `Manifest.Get` already takes the relative path the hit carries. That is the
   cheaper of the two options and it needs no wire change.)*
3. The `problems` entry, its reason string, and its `complete: false`.
4. A re-queue of any note whose two hashes disagree, so a flagged record does not stay flagged.

**AC-16.5 — the acceptance criterion tests DIVERGENCE, not rebuild.** W1's exit criterion in
revision 5 was *"deleting the properties index and reopening rebuilds it with identical query
results"* — which tests **rebuild**, and **would have passed with the mitigation entirely
absent**. Replaced by:

> A record row is written, the note is then modified and re-indexed into bleve **only** (the
> SQLite write suppressed), and a `vault_find` returning that record reports `complete: false`
> with the record named and staleness given as the reason. The symmetric case — SQLite updated,
> bleve not — is asserted the same way. *(The rebuild criterion is kept as well; it tests a
> different property, FR-020a's disposability.)*

**The residual risk, stated because it is not closed.** This detects divergence per returned hit.
It does **not** detect a record that is stale *and excluded from the result by a stale predicate*
— if the properties index holds `status: prospect` for a note whose file now says `status:
churned`, a query for `status = churned` never returns that row and so never compares its hash.
**That is a real hole and it is accepted for W1**, because closing it means comparing hashes over
the whole candidate population rather than the returned page, which is the cost the cap exists to
avoid. It is bounded by the reconcile: the note is re-indexed on the next sync, and
`check_integrity` sweeps for it explicitly. **An operator should know that a query's completeness
verdict covers what it returned, not what it did not.**

**Unchanged by this resolution.** The record model (D1–D15), the tool surface, scoping and the
write path do not depend on the storage outcome and remain valid, as revision 4 predicted.


### D16.6 — SQLite's DEFAULT semantics contradict ten of the comparison rules. **REVISION 7: that is why SQLite does not decide a comparison, rather than a list of things to defeat.**

> **REWRITTEN IN FULL, revision 7, by operator ruling — and this is the single largest change in
> the revision.** Revision 6 listed nine violations and, beside each, a **defeat**: a line of a
> query compiler that would make SQL behave the way the comparison oracle requires. **That entire
> right-hand column is withdrawn.** The ruling: **the properties index NARROWS CANDIDATES; our own
> tested comparator DECIDES.** SQLite answers *"which notes declare this type?"* and hands back
> candidate rows; the comparator applies R-1..R-13 to them. **No comparison is delegated.**
>
> **This reverses D16.2b**, whose one sentence was *"the properties index answers every typed
> predicate"*, and it restores the implementing spec's FR-021 to its original meaning — evaluation
> in Go. **The spec had it right before revision 3 moved it, and it is restored rather than
> corrected.**

**Why the ruling is right, and it is worth arguing rather than asserting, because revision 6 argued
the opposite.** Revision 6's position was that each contradiction is defeatable, all nine defeats
are known, and the shape of the work is therefore clear. **Two things happened to that position:**

1. **Grill pass 1 checked the defeats. Zero of the seven specified were sufficient as written**, and
   it found a **tenth** violation nobody had — **join fan-out**, which made `COUNT` and `SUM` over a
   filtered multi-value list return **2 and 200 where truth was 1 and 100**, silently, by a factor
   varying per record. Every count and every total over a filtered list of a `many` property was
   wrong.
2. **The pattern, not the individual errors, is the finding.** Revision 6 itself wrote — about a
   different mechanism, two decisions above — *"a mechanism this ADR relies on either exists and is
   cited, or is specified as NEW WORK with what it costs, never named as though it already
   worked."* **Nine defeats named in a normative table, none of them written, eight failing in the
   quiet direction, is the same shape.** A-11 in the specification said so in as many words: *"a
   specified defeat and a verified defeat are different things, and eight of the nine failures look
   identical to success."*

**The ruling removes the class rather than mitigating it.** There is no query compiler to forget a
line, because the thing that decides is the comparator that already exists, is already tested, and
is the subject of the spec's §8 rule table. **Nine deliberate defeats, each of which must be
written, none of which was verified, is a worse position than one comparator that is.**

**The receipts are KEPT, and their status changes.** They were *"what we must defeat"*; they are now
*"why we do not delegate"*. Taken by direct execution against the **`sqlite3` CLI 3.51.0** and
re-executed identically through **`modernc.org/sqlite v1.46.1`**, which reports `sqlite_version()`
= **3.51.2** — verified by opening a database through the driver, not read from documentation and
not assumed. *(Grill pass 1 said the shipped engine reports 3.53.3 and that the receipts were two
minor versions stale. That does not reproduce; see the status block. The **standard** behind the
finding is adopted: the engine and version are named here, and a test asserts the linked version,
because affinity and collation behaviour is version-sensitive and a driver bump must be a review
trigger rather than a silent change of meaning.)*

| Rule | What SQLite does by default | Why that is now a REASON, not a task |
|---|---|---|
| **R-1** | `SELECT '3' > 2;` → **`1` (true)**. Storage-class ordering puts INTEGER/REAL before TEXT, so *any* text value outranks *any* number. And an `INTEGER`-affinity column holding `'3abc'` keeps `typeof = 'text'` and still answers `'3abc' > 2` → **`1`** | **SQLite has affinity, not types.** R-1 is a statement about *declared* types, which SQLite does not have — so no configuration of it can express R-1. Revision 6's defeat put "one typed column per declared property" beside a Go-side guard as co-equal halves; **the column half does no work**, which grill pass 1 established and which the receipt above shows directly |
| **R-2 / R-3** | Over a row with `status` NULL: `WHERE NOT (status = 'done')` → **0 rows**. Over a five-row fixture, `NOT (a=1 AND b=2)` returns **1 row** where the NULL-safe rewrite returns **4** | **This is the sharpest one and it is the clearest argument for the ruling.** SQL's `NOT` is three-valued: `NOT(NULL)` is `NULL`, which is not `TRUE`, so absent rows fall out of every negation. **In Go it is right by construction:** R-2 makes a comparison with an absent side `false`, so `NOT(false)` is `true` and D3.2's *"which days did I not meditate?"* returns the days with no entry — at any depth of the filter tree. **In SQL it needed a leaf rewrite AND a normalisation pass, and revision 6 specified only the first.** `(x IS NULL OR x <> ?)` covers a leaf; the grammar is a tree, and the natural implementation — compile the subtree, wrap it in `NOT (...)` — drops every NULL-bearing row however correct each leaf is |
| **R-4** | A value that parsed to something else is stored as whatever it parsed to and compares silently | Unreachable: a non-conforming value never reaches a comparison, because the comparator holds the conformance flag and R-4 is one of its rules. *(One thing grill pass 1 found survives into the design: "never in the typed column" leaves NULL there, and NULL is the **absence** representation — so a **separate** conformance flag is required, consulted at comparison **and ordering** time. Spec FR-021b)* |
| **R-5** | `ORDER BY` over a TEXT column is lexical: declared `lead < qualified < proposal < won` sorts as `lead, proposal, qualified, won` | **No longer a violation at all.** D4 as revised adopts exactly this ordering; a domain order is a value prefix. **The row that was the most-cited example of SQLite getting it wrong is now the specification** |
| ~~**R-6**~~ | *(retired — `money` is deleted, D3)* | *(no reason: no subject)* |
| **R-7** | Two spellings of one instant compare unequal and order anyway (`'2026-08-27T00:00:00+02:00'` vs `'2026-08-26T22:00:00Z'`, both epoch `1787781600`: `=` → **0**, `>` → **1**); fractional seconds invert (`Z` at 0x5A sorts after `.` at 0x2E) **even in an all-UTC corpus**; the `T`-vs-space separator reorders; any non-UTC offset breaks ordering. `unixepoch('not-a-date')` **and** `unixepoch('2026-8-26')` both return **NULL, with no error** | **Revision 6 listed no defeat for R-7 at all and it is violated four ways** — grill pass 1's C-6. Under the ruling, comparison is Go-side over parsed instants and none of it applies. **The `unixepoch` NULL is why parsing is never delegated either:** a parse failure returning NULL is indistinguishable from absence, which would collapse R-4 into R-2/R-3 in storage |
| **R-9 / R-10** | `SELECT 'ACME' LIKE '%acme%';` → **`1`**. `SELECT 'vendors,partner' LIKE '%vendor%';` → **`1`** | **Half of this is now DESIRED** — case-insensitivity is a feature by operator ruling — and the operator vocabulary adopts `LIKE`'s own semantics (O-3, amended). **But SQLite's folding is ASCII-ONLY**, and that is a second, independent argument for the ruling arriving from a direction nobody was looking in — see the Unicode receipt below |
| **R-11** | `9223372036854775807 + 1` → **REAL**, silently. `1/0` → **NULL**, silently. `unixepoch('bad')` → **NULL**. `SUM` over an empty set → **NULL**. But `SUM` **overflow** → a hard `integer overflow` that aborts the statement. And `CAST('9223372036854775808' AS INTEGER)` → **`9223372036854775807`**, saturated silently | **Five outcomes for one arithmetic, four of them silent.** Revision 6 said *"a SQL error is a third outcome"* and specified catching errors — **which reaches exactly one of the five.** None is reachable now: no arithmetic is emitted, no `CAST` is used to admit a numeric string (range-checking is Go-side, before emission), no date is parsed by SQL. **What survives into the design is narrow and real:** a **store** error — a corrupt index, a closed database — must be caught at the boundary and rendered as a problem row |
| **R-12** | `SELECT 3 = '3'` → **0** between literals; the same comparison against an INTEGER column → **1**. `'2' > 3` → **1** and **0** respectively. A `BLOB`-affinity column restores literal behaviour | **Revision 6 listed R-12 among the rules, did not list it among the violations, and it is violated** — grill pass 1's C-5. Comparison affinity converts a TEXT operand **only when the other side is a typed column**, so identical values and an identical operator give **opposite answers depending on operand provenance**, and the answer also depends on the DDL. **One comparator means one provenance**, and the rule is satisfied by there being nothing to disagree |
| *(join fan-out — the tenth)* | A record carrying two matching values of a `many` property joins **twice**: `COUNT(*)` → **2**, `SUM(amount)` → **200**, where truth is **1** and **100** | **The worst finding of grill pass 1 and it was not in revision 6 at all.** Aggregation is now Go-side over a set of records, each visited once. **Worth recording because the obvious fix was itself a trap:** `SUM(DISTINCT)` deduplicates on **value**, not row identity, so two distinct records sharing an amount collapse into one — it returned **100** against a truth of 200 while the naive join returned 300. It errs in the direction that *looks* conservative, which makes it the harder wrong answer to catch in review |

**The Unicode receipt, because it decides where the case fold lives.** Across fourteen upper/lower
pairs (`A/a`, `Z/z`, `Ä/ä`, `É/é`, `Ñ/ñ`, `Ø/ø`, `Ç/ç`, `Å/å`, `Σ/σ`, `Д/д`, `İ/i`, `Ł/ł`, `Ż/ż`,
`Ć/ć`), **`COLLATE NOCASE`, `LIKE` and `lower()` each folded the two ASCII pairs and ZERO of the
twelve non-ASCII pairs.** `lower()` returned every non-ASCII input **byte-for-byte unchanged**
(`hex('Ä')` = `hex(lower('Ä'))` = `C384`), so it is not a workaround for `NOCASE` — it is the same
limitation wearing a different hat. Confirmed structurally as well as behaviourally: `PRAGMA
compile_options` carries no `ENABLE_ICU`, `icu_load_collation` does not exist, and the binary
carries `OMIT_LOAD_EXTENSION`. **There is no Unicode-aware option inside SQLite here at all.**

**Go's `strings.ToLower` is Unicode-aware.** So the case-insensitive matching the operator asked for
is something the comparator can deliver correctly and SQLite cannot deliver at all. **A vault in
German, Polish, Greek or Turkish would have received case-insensitive matching for its ASCII words
and silent case-sensitivity for the rest** — a guarantee holding for one alphabet, which is exactly
the failure class §1.3 catalogues.

**What the properties index is still FOR, so this does not read as "delete SQLite".** It **narrows**:
`type = 'deal'`, `path` prefix within scope, `kind = 'task'`, the relation child table's `rec_id`
join for **reachability**. These are set-membership questions over indexed columns, they are what an
index is good at, and **none is a comparison governed by R-1..R-13.** D16.2's gain stands: the
general `func(any, any) bool` comparator over an expression engine does not need to be written, and
a query does not materialise documents that cannot match.

**The cost, stated up front rather than discovered.** Filtering, grouping and totals run in Go over
the candidate set, so **a query matching very many records is slower than one pushing predicates
down, and the cost scales with candidates rather than results.** Two things bound it and neither is
a hope: **D16.3a's condition C-3 — the 10,000-candidate cap — is a refusal, not a politeness
limit**, and it was set for exactly this reason; and a vault is a few thousand notes, not a
database. **Nobody has measured the Go path over the two-index design.** W1 measures it, at the cap
and at the aggregate-only path's higher bound, with peak RSS, on Linux as well as macOS. The
implementing spec carries it as **A-14**.

**AC-16.6 — REVISED.** The truth table must run against **the comparator the product uses**, driven
through the real path: *schema → filter object → candidate set → comparator*. This is spec
**AC-8.4**, and revision 6 required the opposite — the compiled SQL path — on D16.2b's ground that
the product would not use a Go comparator. **It will.** Two obligations survive the reversal
unchanged, because neither was ever about SQL:

- **No post-filter escape.** A row count taken at the **candidate boundary** must equal rendered
  rows plus problem rows plus comparator rejections, attributable per record. An implementation
  that filters in two places, the second silently correcting the first, fails.
- **Mutation-checked, per rule.** Spec **AC-8.4b** names **six** comparator mutations — remove R-2's
  absent-side rule; remove R-4's non-conforming rule; remove R-11's totality guard; remove the case
  fold; remove `LIKE`'s wildcard handling; remove the `many` de-duplication in the aggregate pass —
  **each of which must kill a named cell**, reported as a mutation table rather than a pass.
  *(Revision 6's SC-024 named **two** mutations for **nine** defeats, and a compiler missing seven
  of the nine passed both. That is deleted with the defeats.)*

**Cross-reference, not duplication.** The normative rules are the spec's §8 rule table; the
rule-by-rule assessment and the executed receipts are its §8.1 and §8.1b; the acceptance criteria
are AC-8.1..AC-8.6, also §8. **Section references, not line numbers, deliberately** — the spec
moves, and this ADR's verification standard is worth less if its citations rot. **This ADR states
the decision and the risk; the spec states the cells and the tests.** Where the two disagree the
spec is the implementing document and is corrected there.

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

**AC-18.1** — a test enumerates the **six `vault_*` tools** and asserts an explicit, literal,
wildcard-free policy entry for **every seeded agent**, not only the four base agents. Coverage
runs over all ten agents `coreagent.SeedConfig` creates (`mia`, `jim`, `ava`, `ray`, `worker`,
`planner`, `explorer`, `researcher`, `judge`, `plansupervisor`). `worker`'s map is sparse, and
in a sparse map **absence grants** — so `worker` needs explicit entries or it silently receives
tools it should not have.

**AC-18.2** — a test asserts the WARN-backfill path is never reached for these tools on a
fresh install, by asserting zero repaired pairs rather than zero gaps after repair.

**Seed posture, revised 2026-08-28 to D15.1's two criteria:**

| Tool | Tier | Mia | Jim | Ava | Ray | worker / others |
|---|---|---|---|---|---|---|
| `vault_describe`, `vault_find`, `vault_read` | READ | allow | allow | allow | allow | `deny` |
| `vault_edit` | WRITE — one named note | **ask** | allow | allow | **ask** | `deny` |
| `vault_restructure` | WRITE — cascades in **bytes** | **ask** | **ask** | **ask** | **ask** | `deny` |
| `vault_configure` | WRITE — cascades in **meaning** | **ask** | **ask** | **ask** | **ask** | `deny` |

**Reads are `allow` roster-wide, and the existing reasoning is kept unchanged:** a prompt in
front of a read that another tool already permits is a prompt that protects nothing. An agent
denied `vault_read` still has `read_file`. The prompt would train the operator to click through
without buying a single unit of safety.

**Why `worker` is `deny` on the reads too, when that argument seems to cut the other way.**
*(New in revision 6; the review's M-22 is a fair challenge and deserves an answer rather than a
restatement.)* The read argument is *"a prompt buys nothing when another tool already permits
the read"*. It turns on `allow` versus `ask`, and it is an argument about **prompting**, not
about **granting**. `deny` is a different act: it removes the capability rather than removing a
prompt in front of it.

The seed is therefore **deliberate and narrow**: workers are delegation-only executors whose task
comes from a parent that has already done the vault reading. Granting them the vault surface
widens what a delegated sub-turn can reach for no gain in what it can accomplish. **The honest
cost is exactly what the review identifies:** a worker that genuinely needs a note reaches for
`read_file` and the read leaves the audited boundary. That is a real loss, it is accepted, and
the remedy is the same one available for every seed in this table — **an operator flips it.**
*(What we do **not** claim is that a worker has no `read_file`. It does. The boundary is a
default, not a wall.)*

**`vault_restructure` gets its own line, and that is the point of splitting it out.** It
defaults more restrictively than `vault_edit` so that **an operator can forbid restructuring
while permitting edits** — a posture that is simply inexpressible today, and would have stayed
inexpressible under a consolidated `vault_write` (D15.2).

**This FIXES a live defect, and revision 6 states it more strongly because it is worse than
revision 5 said.** Revision 5 wrote that `knowledge_rename` and `knowledge_move` sit *"in the same
`ask` bucket as `knowledge_append_section`"*. That is true for **Mia** (`pkg/coreagent/core.go:1056-1058`)
and **Ray** (`:1149-1151`) only. For **Ava** (`:976-978`), **Jim** (`:1296-1298`) and the **global
ceiling** (`pkg/config/defaults.go:644-646`), all three are **`allow`** — vault-wide restructuring
outright permitted, with no prompt at all.

So the defect is not "the prompt does not distinguish"; it is **"for two of the four base agents
there is no prompt."** The new table is therefore a **tightening** for Ava and Jim, not a
re-labelling — a stronger and more useful thing to say than revision 5 said. An operator who
grants "vault writes" today grants vault-wide restructuring in the same gesture, and for half the
roster grants it silently.

**Workers stay `deny` on all six** — unchanged from revision 4, and now explicitly including
the read tools and the control plane.

### D19 — Contract-first wire types (Hard Constraint #8)

Every type crossing the gateway/SPA boundary is defined in `contracts/` before any Go or TS
code: `RecordSchema`, `RecordType`, `PropertyDef`, `RecordQueryRequest`, `RecordQueryResponse`
(carrying `complete` and `problems`), `RecordWriteRequest`, `RelationWriteRequest`,
`ViewDef`, `ValidationReport`.

`RecordQueryResponse.complete` and `.problems` are **required fields**, not optional. D13's
guarantee has to be structural: a client cannot receive records without also receiving the
completeness verdict.

**Revision 5 additions, extended in revision 6.** The six-tool surface adds
`VaultDescribeResponse`, `VaultFindRequest` / `VaultFindResponse`, `VaultReadResponse` (carrying
the version token `vault_edit` requires — D15.3), `VaultEditRequest`, `VaultRestructureRequest`
and **`VaultConfigureRequest`** (D15.6). The `Record*` schemas above remain the record-model types
those requests carry; only the tool-shaped envelopes are renamed.

**Revision 6 adds one more, and it is not a tool envelope: the index-state snapshot §2.7 requires.**
It **MUST reuse the schema of the existing `knowledge_index_progress` frame** rather than declare
a parallel one — the live frame and the hydration response are the same information at two
delivery times, and two schemas for one thing is how the agent-facing and human-facing views come
to disagree.

**D22 does not weaken this decision, and the distinction matters.** D22.1 makes tool results
**compact text to the model**. The **wire type is unchanged**: still contract-defined, still
generated into `pkg/api/generated/` and `src/lib/api/generated/`, still verified by
`make verify-contracts`. The compact rendering is produced **from** the validated wire object at
the tool-result boundary — it is a projection of the contract, never a replacement for it, and
never a hand-written parallel struct (Hard Constraint #8).

### D20 — Sequencing

**Revised 2026-08-28. Operator directive, explicit: NO quick wins — build the full
best-in-class system.** The sequence below is therefore ordered for **the whole thing**, not for
cheap partial value. There is deliberately no "quick wins" wave: the TF-IDF correction (D21.1)
rides along inside the wave whose ranking it affects, because a one-line fix shipped outside the
design it belongs to is how a system acquires changes nobody can explain later.

**W0 — the ONE deliberate exception to that directive, and revision 6 makes it.**

| Wave | Delivers | Exit criterion |
|---|---|---|
| **W0** — ships **independently of ADR-068, ahead of W1** | **The F-0 index rebuild.** `bleve/v2 v2.6.1` (`go.mod:12`) and `zapx/v17 v17.2.3` (`go.mod:107`) are pinned, but **segments already written stay corrupt**: a search over a 100,000-document index panics `slice bounds out of range` **unrecovered through `indexImpl.Search`**, which in the gateway is a process crash. A forced rebuild of existing indexes is the other half of the fix. | an index built under `zapx v17.1.2` at 100,000 documents is rebuilt on upgrade rather than opened, and the search that panicked returns results |

**Why this one is carved out, when the directive says not to.** The no-quick-wins reasoning is
about coherence: a change shipped away from its design is a change nobody can later explain. That
argument fits the `ScoringModel` one-liner, which alters ranking and belongs with the ranking
work. **It does not fit a repair to an operator's crashing index.** The spike says so in as many
words — *"Blocking, and independent of records — today's index is corrupt at 100,000 documents.
**Do not let this wait on ADR-068.**"* — and **Hard Constraint #7** does not admit a stylistic
reason for leaving a live crash in the field behind a design wave. Revision 5 placed it in W2,
behind schema files and a new storage engine. That was wrong.

*(The version pins are not the fix on their own, and revision 5's D16.1 already said so; what it
did not do is act on it.)*

| Wave | Delivers | Exit criterion |
|---|---|---|
| **W1** | Schema files, the seven types, arity/presence/scope, validation. The SQLite properties index (D16.2) with **`source_hash` and the divergence check specified in D16.5**, its rebuild-from-notes path, and the **platform stub and refusal** (D16.2a). `vault_describe` including `check_integrity` and its bounds. **The catalog-count assertion (D15.0).** **Updating `CLAUDE.md` and ADR-067 §A2 (D16.4 item 1).** | **AC-16.5** — a record whose two indexes disagree is reported `complete: false` and named, in **both** divergence directions; deleting the properties index and reopening rebuilds it with identical query results; **both indexes, idle and at the cap, measured inside 64 MB on Linux and macOS**; a query on a SQLite-less build refuses by name and never returns empty; `CLAUDE.md:66` no longer says SQLite is isolated to WhatsApp |
| **W2** | Fielded indexing (D21.2), the **`ScoringModel` correction and the thirteen documentation corrections** (D21.1), BM25F weighting + RRF (D21.3), the tokenizer resolution (D21.5). `vault_find` — plain words, typed filters, grouping, `kind: task`, the problem report. | a query over a typed corpus returns records + a populated `problems` array; a type mismatch is never a silent empty result; a field query on a property key is possible at all, which it is not today; **no `.go` file in the tree attributes BM25 to bleve while `ScoringModel` is unset** |
| **W3** | Relations, inverses, relation grouping; `near` + `hops` and its **composition with filters** (D15.3). `vault_read`. | the §1.2 two-hop question is one call with no hand-maintained state; "notes mentioning pricing within 2 hops of `[[Acme]]`" is one call |
| **W4** | `vault_edit`: byte-preserving writes, the **list-valued splice** (D14), `create`'s `template` argument, and the two **unbuilt primitives** — `replace_body` and the **trash CONVENTION** (where a trashed note goes, what happens to inbound links, whether the index forgets it immediately) — each with its own FR (D14.1). Derived interaction history (D17). | write-read-back is byte-identical outside the patched span; a `replace_body` whose anchor is ambiguous is refused, naming both matches; the trash convention is written down and reviewed before any tool exposes it |
| **W5** | `vault_restructure`: rename, move, and the trash **operation**. `vault_configure`: record-type and saved-view authoring (D15.6, D23). The D18 policy seeding and its ACs. **The write-path rate limiter (D15.5b).** **Retiring the nine `knowledge_*` names** — from `allStaticToolNames` (`pkg/coreagent/core.go:357-482`), from the global ceiling (`pkg/config/defaults.go`), from all five seed maps that carry them, and from every skill and prompt that names one. | an operator can forbid restructuring while permitting edits **and forbid schema authoring while permitting both**, with a test proving all three policies are independently settable; **after this wave no `knowledge_*` name exists anywhere in the catalog or any seed map, and the catalog assertion reads 95** |
| **W6** | The human surface: record table, grouping, related-records panel, problem banner, drill-down, cell edit. **The index-state snapshot (§2.7).** The **operator/CLI** saved-view importer (D15.4, O-1). | the banner names excluded records and the drill-down lists them; **a client connecting after an index completed renders its real state rather than "no progress has arrived"**; a `.base` file imports with every untranslatable expression reported verbatim |

**Two scheduling defects revision 5 carried, both fixed above:**

- **`trash` was in two waves** — W4 as an "unbuilt primitive" and W5 as a `vault_restructure`
  operation — which would have shipped part of a tier-5 tool before W5 defined the tool or seeded
  its policy. Split: **the convention is W4 design work, the operation is W5.**
- **No wave retired the nine `knowledge_*` tools.** §4.2 named the cost; nothing scheduled it.
  Between W1 and an unowned removal the catalog would have carried **fifteen** vault tools — worse
  than the 107 D15.0 rejects. It is now W5's, with the count as the exit criterion. *(The
  implementing spec already requires this — FR-070a and FR-084 — so this is the ADR catching up to
  its own spec, not new scope.)*

**Performance targets: none stated, deliberately — and revision 6 does not change this.**

Revision 2 carried latency and memory numbers derived from a ~794 ns/record figure that
measured **expression evaluation only**. D16.1 now records the measured reality: that phase is
**0.5–0.9% of true cost**, so the numbers were off by roughly two orders of magnitude.

The spike produced real numbers, but they measure the **bleve-plus-Go design this ADR did not
take** (D16.3). Quoting them as targets for the two-index design would repeat the original
error in a new costume. Targets arrive when W1 has the SQLite path measured.

**On the 64 MB budget, revision 6 is more careful than revision 5 was.** ADR-067's < 64 MB
steady-state RSS is **inherited as a target and is not yet a property of this design** — it was
measured for bleve alone, and the two-index design adds an unmeasured store beside it (D16.4 item
4). W1 measures it; until then it is what we are aiming at, not what we are claiming.

### D21 — Retrieval: lexical ranking done properly, and no embeddings, permanently

**New in revision 5.** ADR-067 shipped retrieval; nobody had audited what it actually ranks
with. Doing so found a defect and a structural gap.

**D21.1 — A DEFECT: vault search is scoring TF-IDF, not BM25.**

Verified, and this one *did* survive checking:

- `const DefaultScoringModel = TFIDFScoring` —
  `bleve_index_api@v1.4.1/indexing_options.go:37`.
- **`ScoringModel` is set NOWHERE in `pkg/`.** A repository-wide grep returns zero
  assignments. The default therefore stands everywhere.

Meanwhile the code says otherwise in **thirteen places** across three packages. *(Revision 5
listed seven. The round-5 review said twelve. **Both are undercounts** — the enumerable set is
thirteen, and it is enumerated here so the number is checkable rather than asserted. Method:
every non-test `BM25` occurrence under `pkg/` that attributes the scoring to **bleve**; the
hand-rolled engines in `pkg/utils/bm25.go` and `pkg/agent/retro_bm25.go`'s own arithmetic really
are BM25 and are excluded, as is `pkg/tools/memory.go:94`, whose `SearchRetros` is satisfied by
the hand-rolled retro ranker and is therefore **true**.)*

| # | Location | Claims |
|---|---|---|
| 1 | `pkg/knowledge/index.go:164` | *"Score is the BM25 score of the file's BEST segment."* |
| 2 | `pkg/knowledge/index.go:1062` | *"Search runs a BM25 query…"* |
| 3 | `pkg/memrooms/index/index.go:19` | *"Recall: BM25 ranking via **bleve's default BM25 scorer**"* — the default is TF-IDF |
| 4 | `pkg/memrooms/index/index.go:67` | *"Score is the BM25 relevance score"* — on a struct documented as *"a single recall result from the bleve index"* |
| 5 | `pkg/memrooms/index/index.go:249` | *"executes a BM25 full-text query against the index"* |
| 6 | `pkg/memrooms/index/index.go:250` | *"ordered by descending BM25 score"* |
| 7 | `pkg/memrooms/index/index.go:267` | *"BM25 over the text fields explicitly (title, body, tags)"* |
| **8** | **`pkg/agent/memory.go:301`** | **a runtime WARN: `"roomIndex: failed to open bleve index; BM25 disabled for room"`** |
| 9 | `pkg/agent/memory.go:620` | *"searches the specified room scope for query using bleve BM25 (FR-7.4)"* |
| 10 | `pkg/agent/memory.go:674` | *"BM25 path (FR-7.4)"* |
| 11 | `pkg/agent/memory.go:746` | *"Sort by BM25 score descending"* — of bleve's scores |
| 12 | `pkg/agent/retro_bm25.go:14` | retro BM25 parameters *"match bleve's defaults"* |
| 13 | `pkg/agent/retro_bm25.go:24` | the retro ranker exists to match *"the BM25 similarity ranking bleve provides for long-term memories"* |

**Row 8 is a different kind of defect from the other twelve, and revision 6 flags it as such.**
It is not a comment. It is `logger.WarnCF` output that **reaches an operator reading logs**, and
it tells them a fallback happened — *"BM25 disabled"* — implying that BM25 was what they had.
They never did. Fixing a stale doc comment is hygiene; **fixing operator-facing output is a
user-visible correction**, and it should be described that way in the changelog rather than
folded into a documentation sweep.

Rows 12 and 13 are the ones that matter beyond hygiene: `retro_bm25.go` builds a
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

This is **harmless today** only because the three never rank the same corpus.

> **RE-DERIVED in revision 6, because the requirement this argument rested on changed in the same
> revision that made it.** Revision 5 grounded the hazard in FR-021 — *"FR-021 already requires
> Go-side evaluation over the candidate set"* — while D16.2, three decisions earlier in the same
> revision, pushed filtering, grouping and aggregation into the properties index. Neither
> referenced the other. **If the Go-side pass had vanished, so had this argument.**

**It did not vanish, and the hazard survives — but the reason is now a different one and is worth
stating exactly.** What moved into SQLite is **membership**: which records match. What stays in
Go is **ranking** — D21.3's four-signal RRF fusion, the rendering, and the problem report. So:

> **bleve selects text candidates with one notion of a term, and Go's rank fusion scores them
> with another.** A document bleve matched on a stemmed form is scored by a Go tokenizer that
> never produces that form, so it ranks as though the query term were absent.

The symptom is unchanged and is still the worst kind: **no error, just a ranking that is quietly
wrong.** What changed is the surface — the hazard is now confined to the ranking pass rather than
spanning the whole filter path, which makes it *narrower* and no less real. *(The implementing
spec records the same conclusion at FR-021's meaning-change note and carries it as FR-116.)*

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

**Restated at the design target, which revision 5 did not use.** Revision 5 argued from *"a
5,000-note vault is roughly 0.5–3M corpus tokens"* and concluded that this sits *"permanently"*
in the agentic-loop regime, *"an order of magnitude inside it"*. **ADR-067's stated scale is
100,000 documents** — the spike builds exactly that corpus for exactly that reason — which is
~10–60M corpus tokens: **across** the ~10M boundary the cited paper draws, not an order of
magnitude inside it.

**The conclusion survives the correction; two of its words do not.** Both sides of that boundary
are lexical — below it the agentic file-system loop wins, above it BM25 wins — so *"no
embeddings"* holds at every size this product targets, and holds for a better reason than
revision 5 gave: **we cross the boundary and the answer does not change.** What must go is
"permanently" and "an order of magnitude inside it". Overstating a correct conclusion is how a
reader stops trusting the correct ones.

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
the right notes and renders them badly is a worse retrieval system.

> **D22.0 — What the two citations support, and what they do not. New in revision 6, because
> revision 5 used both beyond their warrant and §6 caveated neither.**
>
> - **The 93.1% → 55.2% finding is about WHERE results are put** — inline in the conversation
>   versus written to a file the model must open. It establishes that **delivery is mechanism, not
>   presentation**, and that is a large and well-evidenced claim. It says **nothing about
>   compact text versus JSON**, which is a question about *format* at the same delivery site.
>   Revision 5 used it to justify D22.1. **That is an inference, and it is now labelled one.**
> - **Notion's ~91% reduction is a measurement over a SCHEMA surface** — a structured, bounded,
>   highly repetitive object where JSON's key repetition is most of the payload. Revision 4
>   correctly cited it for `record_schema`. Revision 5 extended it to *"every result from all five
>   tools"*, including record rows carrying unbounded property values and free-text excerpts,
>   **where the compression ratio has no particular reason to transfer.**
>
> **What survives, and it is enough to keep every decision below:** delivery format is part of
> retrieval design (finding 1, directly); a schema rendered as compact text costs far fewer
> tokens than the same schema as JSON (finding 2, directly); and **every specific rule in D22.2
> through D22.6 stands on its own reasoning**, which is about what a model can act on, not about
> a measured ratio. **The decisions are kept. The warrant is corrected.**

Accordingly:

**D22.1 — Compact text, never JSON, to the model.** Revision 4 made this call for
`record_schema` alone (Notion's measured ~91% context-token reduction, which is a schema
measurement and is load-bearing here); it now applies to **every result from all six tools**,
**as a reasoned extension rather than a measured one** (D22.0). The saving on a schema is
evidenced; the saving on a page of record rows is expected and unmeasured, and if it turns out to
be small the rules in D22.2–D22.6 are why the format stays anyway.

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
`company [[Acme]]: …`, **never merged into the row's own columns.** Blurring that is how an agent
comes to believe a property exists on a note that does not have it.

> **Amended in revision 6.** Revision 5 wrote *"the row is still one real file"*. That holds for
> notes and records; it does **not** hold for `kind: task`, where many rows come from one file
> (D15.3). The rule is therefore: **a row is one real thing at a path** — a note, or a checkbox
> line within one — and a task row always renders its line number so the distinction is visible
> rather than inferred. The property being protected is unchanged: a reader must never take a
> borrowed or derived value for one the file itself contains.

**D22.5 — Totals state their scope.** *"2 matched, GBP only"* — **never a bare number.** This is
D13's cross-currency refusal (O-2) carried into the rendering, where it is actually read.

**D22.6 — Every response ends in addressable next actions.** In an agentic loop **each response
is a prompt for the next call.** A response that terminates in data terminates the loop; one
that ends in *"narrow by `status`, or `near: [[Acme]] hops:2`"* continues it.

**D22.7 — Response budget, expressed in BYTES.** ~200–320 bytes per hit; ~4,000 bytes default;
**hard cap 16,000 bytes, with truncation stated in the header** (D22.2, and D15.5b's rule that
every breach is reported). A `minimal` mode at ~80 bytes/hit for wide scans.

> **Changed from tokens to bytes in revision 6, and the reason is D21.5.** Revision 5 set a
> *"hard cap 4,000 tokens"*. **A token cap needs a tokenizer to enforce it, and D21.5 is a
> decision about three tokenizers that disagree** — none of which is the model's, which is the
> only one that would make the number mean what it says. Enforcing a token budget with any
> tokenizer we own would produce a cap that is wrong by an unknown margin in an unknown
> direction, on every provider, and would silently change meaning when a provider changed
> tokenizer.
>
> **Bytes are exact, provider-independent, and enforceable at the point of truncation.** The
> figures above are the token figures at a conservative ~4 bytes/token so the intent is
> unchanged; they are a budget for *rendering*, not an accounting of what the model is billed.
> *(Nothing else in D22 depends on the unit. D22.8's ~150-token description budget is a design
> guideline for a human writing prose, not a runtime check, so it stays in tokens.)*

**D22.8 — Tool DESCRIPTIONS are the binding constraint, not tool count.**

This is the part that is easy to get backwards. Tool *count* costs selection accuracy (D15.0).
Tool *descriptions* cost **tokens on every turn, forever**, for every agent that has the tool —
whether or not it is ever called.

**Budget ~150 tokens per tool description.** Push operation detail down into **parameter
descriptions and error messages**, which are paid only when relevant: an agent learns the
`set_property` arity rule from the error it gets, not from a paragraph every other agent carries
on every turn. Learn-on-demand, not learn-in-advance.

*(Six tools at ~150 tokens is ~900 tokens of permanent context. Eighteen would have been ~2,700.
The sixth tool D15.6 adds costs ~150 of those, which is the whole of its standing price.)*

### D23 — Schema and view authoring are ordinary writes; mounting is not

**New in revision 5, and corrected mid-drafting after an operator ruling.** The correction is
recorded because the first framing was wrong in an instructive way.

> **Revision 6 keeps D23's ruling and changes only where the writes live.** They are still
> **ordinary writes governed by ordinary tool policy** — no approval flow, no
> `request_schema_change`, no UI-ratifies step. What changed is that they are governed by
> **their own** policy entry (`vault_configure`, D15.6) rather than sharing `vault_edit`'s. That
> is more operator control, not a new gate on the agent.

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

---

## 2.7 Indexing progress: the alleged defect is NOT CONFIRMED; a different, real one is

*New in revision 5; completed in revision 6.* **This is a verification note, not a decision** —
revision 5 numbered it §2.6, so a reader scanning decisions read it as one.

**The shape of this section matters.** Revision 5 refuted a hypothesis correctly and then
**guessed at a replacement**, in the section whose entire thesis is not guessing. Revision 6 keeps
the refutation, deletes the guess, and puts the **verified** cause in its place. The alleged
defect is still NOT CONFIRMED. The symptom the operator saw is **REAL**, and its cause is a third
mechanism revision 5 never considered.

### 2.7.1 The alleged defect — NOT CONFIRMED

The council was asked to record, as a found defect, that vault **indexing
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

> **REMOVED in revision 6.** Revision 5 closed this table with *"Either is a far likelier
> explanation of the manual observation than a throttle bug."* **That was an unverified causal
> claim, asserted in the paragraph immediately above one condemning unverified causal claims.**
> Neither case is in fact the explanation — see below — but the objection stands even if it had
> been: a section whose standard is "verified or recorded as unverified" does not get to end on
> "far likelier".

### 2.7.2 The real cause — VERIFIED

**Progress is live-only. There is no snapshot, no replay, and no persistence at any layer.**
Every step of the delivery path was read at revision time:

| Layer | What it does | Evidence |
|---|---|---|
| Gateway broadcast | non-blocking send to **currently connected** sockets; a client with a full buffer is skipped and the frame is dropped | `pkg/gateway/knowledge_lifecycle.go:1114`, drop at `:1131-1136` |
| Server-side store | **none.** Nothing retains the last frame for a later reader | — |
| REST | **no endpoint returns index state.** The four knowledge routes are `""`, `search`, `graph`, `outline` | `pkg/gateway/rest_knowledge.go:141-167` |
| SPA store | `useKnowledgeIndexStore` is **unpersisted**, and is written from exactly one place: the `knowledge_index_progress` WS case | `src/store/knowledgeIndex.ts:43-50`; sole writer `src/store/chat.ts:5075` |
| Render with no entry | `index_status_unknown` → *"No indexing progress has arrived since you opened it."* | `KnowledgePanel.tsx:138`, `KnowledgeEmptyState.tsx:402` |

**The consequence is worse than a transient gap, and this is the part revision 5 missed
entirely: a collection indexed once at boot and never touched again renders
`index_status_unknown` PERMANENTLY, for every client that connects afterwards.** Not until the
next frame — there is no next frame. Every browser that opens the Library after a quiet index
sees "no progress has arrived" about an index that finished successfully hours ago.

`KnowledgePanel.tsx:37-39` states the intended contract — *"the gateway emits progress on mount
and on each drift check, so a browser that opens the Library between two of those events
legitimately has no news, and absence of news is not news."* That reasoning is sound for a
**momentary** gap. It does not hold when the gap is unbounded, and "mount" there means the
*collection* mounting, not the browser's.

**The fix is a hydration, not an emission change.** The snapshot already exists and is simply
never exposed: `knowledge.SharedProgressTracker(c.root)` (`knowledge_lifecycle.go:762`), already
converted into exactly the frame shape a client consumes at `:836`. What is missing is a
request-response path that returns it.

**On the no-polling rule, which is the objection a reader will raise:** the contracts forbid
*polling* for index state. **One-shot hydration on connect or on mount is a different thing** —
it is bounded, it happens once, and it exists precisely so the live channel does not have to be
replayed. Reading the prohibition as forbidding hydration is what left the panel with no way to
learn anything at all.

**The decision: expose a connect-time or mount-time snapshot, in the SAME shape the live frame
carries** (one source for both, so the agent-facing and human-facing views cannot drift).
**Owner: W6, the human surface.** The implementing spec already carries it as **FR-020f**, with
the same verified cause and the same explicit instruction that this **must not** be fixed by
changing emission.

**Emission itself is untouched.** `progressCoalescer` is correct, its leading edge is correct,
and its terminal flush is correct — §2.7.1 verified all three, and nothing in this section
revises that.

**Why this is recorded at length rather than quietly dropped.** This ADR's D16 table exists
because three consecutive revisions asserted properties of code that nobody had read. Revision 5
avoided that failure in the first half of this section and committed it in the second. **The
finding is kept, and so is the record of the guess**, because a section that quietly replaced its
own wrong conclusion would teach a future reader nothing.

**What remains genuinely open, and it is small:** whether an operator should be told that an
*empty* collection indexed successfully. That is a product decision about empty-state rendering,
not a throttling fix, and it belongs with W6's snapshot work.

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

That is decisive for the shape from revision 5 onward. D15's six-tool surface, D21's fielded index, and
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

### 3.5 A visual query builder

**Deferred, not rejected.** Agents author queries; a builder serves a user who cannot write
one. Notion maintains both a GUI filter builder and a raw form, and complex expressions become
uneditable in the GUI — a two-representation problem worth avoiding until there is a reason.

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
- **An operator can forbid schema authoring while permitting note edits** (D15.6) — the posture
  revision 5 promised and did not deliver.
- **Joins, `OR`, `GROUP BY` and aggregates come from a store that already does them**, so the
  general expression **evaluator** §4.2 names as this ADR's highest-risk component does not need
  to be written (D16.2).

  > **Narrowed in revision 6.** Revision 5 claimed the risk *"shrinks"*, and D16.2 claimed the
  > store buys this *"without our writing a query engine"*. **Both overstate it**, and the four
  > places they overstate it are named in §4.2's cost list rather than left for a reader to find:
  > four of this ADR's own semantics are **not native SQL**, so a translation layer that carries
  > them is real code with real correctness risk. What genuinely disappears is the **general
  > `func(any, any) bool` comparator overload** — the specific thing that made `3 > 2` evaluate
  > false. That is the claim, and it is smaller and true.
  >
  > **D16.6 narrows it once more, and further.** Beyond the four non-native semantics, SQLite's
  > defaults **actively contradict** nine of the thirteen comparison rules. The comparator
  > overload genuinely disappears; the semantics it was carrying do not — they move into the
  > query compiler, where the same wrong answers are reachable by a different route.

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

*Added in revision 6:*

- **SQLite's DEFAULT semantics contradict NINE of the thirteen comparison rules, and eight of the
  nine fail silently** (D16.6). `'3' > 2` is **true** in SQL; `NOT (status = 'done')` **drops**
  every absent row, so FR-008's *"which days did I not meditate?"* returns **zero**; `'ACME' LIKE
  '%acme%'` is **true**; enums order lexically, so `stage >= qualified` drops `proposal`; and
  `SUM` adds USD to JPY without complaint. Each is defeatable and D16.6 gives the defeat, but each
  must be **deliberately** defeated in the query compiler — the correct behaviour is never the
  default. **This is the largest single correctness cost of D16** and it was absent from revision
  5 entirely, carried only by the implementing spec (its §8.1). The truth table therefore
  runs against the **real compiled query path** (AC-8.4), not a Go comparator: after D16.2b the
  product does not use one for filtering, and a table that passes over an unused comparator proves
  nothing.
- **Four of this ADR's semantics are NOT native SQL, and the translation layer that carries them
  is the risk D16.2 moved rather than removed.** Named so they are costed:

  | Semantic | Why SQL does not give it |
  |---|---|
  | **D3.2** — a negative filter *includes* absent records by default | SQL's `<>` **excludes** `NULL`. Every negative predicate must be emitted as `(x <> v OR x IS NULL)`, and forgetting it reproduces §1.3's checkbox failure exactly |
  | **D4** — enums sort by **declared position**, not spelling | needs an ordinal join or a generated ordinal column, maintained in step with the schema file |
  | **D10** — multi-value grouping puts one record under **both** groups | needs an unnest; the naive `GROUP BY` produces Obsidian's "Finance Business" defect, which D10 exists to avoid |
  | **O-2** — cross-currency sums are **refused**, not summed | no aggregate expresses "refuse"; the refusal must be decided before the SQL is emitted |

  **The `3 > 2` failure class does not disappear; it changes address** — from a Go comparator to
  a SQL generator, where the symptom is the same (a wrong answer, no error) and the test surface
  is the same exhaustive truth table §4.2 already requires. That table is still a deliverable, and
  it now covers emitted SQL as well as Go comparisons.
- **A sixth tool exists** (D15.6). One more definition in the catalog and ~150 more tokens of
  standing context per agent that has it, bought deliberately — see D15.6 for the trade.
- **The write path gets a rate limiter it does not have today** (D15.5b). Revision 5 recorded this
  as inherited; it is new work.
- **`vault_restructure` and `vault_configure` offer no compare-and-swap** (D15.5c), because a
  single-file token cannot honestly guard a cascade. Their safety is policy plus audit plus
  `check_integrity`.

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
- **It makes no claim about indexing-progress emission beyond what §2.7.1 verified**, where an
  alleged defect was investigated and **not confirmed**.

*Added in revision 6:*

- **It does not claim the response-format research transfers exactly (D22.0).** The inline-versus-
  file finding is about *where* results are delivered and is used here to argue that *how* they are
  rendered is mechanism — a reasoned extension, not a measured one. Notion's ~91% is a **schema**
  measurement, load-bearing for `vault_describe` and inferred for everything else. §6 omitted D22
  entirely in revision 5, which was the one decision resting on a single un-corroborated finding.
- **It does not claim SQLite is faster than bleve-plus-Go for these queries.** The latency argument
  is withdrawn (D16.3). Nobody has measured the two-index path.
- **It does not claim the < 64 MB budget holds for the two-index design.** It is inherited as a
  target and measured in W1 (D16.4 item 4).
- **It does not claim the freshness comparison catches every divergence.** It compares returned
  hits; a record excluded by a stale predicate is not compared (D16.5's residual risk).
- **It does not claim the record layer works on every target Omnipus builds for.** `linux/mipsle`
  is the one shipped binary without SQLite, and records refuse by name there (D16.2a).
- **It does not claim `pkg/knowledge`'s task extraction has been designed.** D15.3 specifies
  indexed checkbox rows; nobody has built or measured the indexing cost of that (W2).
- **It does not claim SQLite's defaults implement this ADR's comparison semantics — they
  contradict nine of the thirteen rules** (D16.6), verified by execution rather than asserted.
  The defeats are known and specified; **none of them is verified until the truth table runs
  against the compiled query path** (AC-8.4). Until W1 produces that run, "the nine are defeated"
  is a plan, not a property, and this ADR does not claim otherwise.
