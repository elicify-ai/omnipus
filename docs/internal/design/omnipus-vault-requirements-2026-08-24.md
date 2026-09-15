# Omnipus Vault — requirements, tailored from evidence

**Date:** 2026-08-24
**Status:** Proposed requirements — pre-ADR
**Decision already taken:** we are NOT aiming for Obsidian `.base` compatibility. Interop, if
wanted later, arrives via MCP or a one-way importer.

## Why this document exists

We need a structured-data layer over notes — master data (CRM: companies, contacts, deals) and
numeric/accounting values. Rather than reimplement someone else's undocumented DSL, we researched
what the two incumbents actually do, and measured our own vault to find out what is really needed.

Three research passes (Notion data layer; Obsidian Bases + Dataview; Go ecosystem/foundation) plus
direct measurement of the founder's 751-note vault.

## 1. The evidence that drives every requirement below

All figures measured directly from the real vault on 2026-08-24.

### 1.1 The subscription total is unanswerable today

63 subscription records carry a `cost`:

```
41 parse as a number
22 DO NOT — they hold "PLACEHOLDER — cost unknown" and similar

currencies present:  USD 26 · EUR 8 · SGD 6 · AUD 1
billing cycles:      monthly 18 · annual 12 · quarterly 1 · free 1 · on-demand 1
                     + 8 distinct spellings of "PLACEHOLDER"
```

Four independent defects stack in one query:

1. **35% of records silently drop out** of any numeric aggregate.
2. **Four currencies are summed as if they were one number.**
3. **Monthly and annual amounts are added without normalisation.**
4. **Float arithmetic is already wrong**: summing the 41 numeric values gives
   `5875.6799999999985` where exact decimal gives `5875.68`.

### 1.2 The failure is SILENT — and the two tools fail in two different silent ways

**Dataview** implements `sum()` as a left-to-right `reduce(array, "+")` and registers
`string + *` as concatenation. Applying those semantics to the real 63 values returns:

```
type : STRING (1,211 characters)
value: "238.4PLACEHOLDER — current subscription cost not found in sweep14.986645PLACEHOLDER — …"
```

**Obsidian Bases** — read out of the decompiled 1.12.7 binary — fails the *opposite* way, and it
is worse. `sum()` folds only values that are actual numbers, **silently dropping** the rest; the
built-in `Sum` summary is hardcoded as `values.sum().round(2)`; and `mean()` divides that sum by
the **full list length**, not by the count of numeric values. On the same 63 records:

```
Bases would display    Sum: 5875.68     ← 22 records silently missing
                   Average:   93.26     ← divided by 63, not 41
honest average                143.31
                              → Bases understates the average by 34.9%
```

**The Bases failure is the more dangerous of the two.** A 1,211-character string is obviously
broken and someone will investigate. A subscription average of 93.26 looks entirely plausible,
sits in a dashboard, and is wrong by more than a third. Neither number carries any warning, and
both silently add USD, EUR, SGD and AUD together.

The honest answer is one line: *"cannot total: 22 of 63 records have a non-numeric cost;
4 currencies present."*

**This contrast is the design brief.**

### 1.3 A relational model already exists, with no integrity

**1,175 wikilink foreign keys** across the vault: `up` (654), `owner` (365), `decided_by` (53),
`vendor` (41), `project` (35), `company` (9), and others. Entity types are already declared:
`task` 121, `note` 108, `reference` 87, `decision` 69, `company` 52, `subscription` 42.

But: **exactly one `slug` property exists in 751 notes.** Identity is the filename, so renaming a
company breaks every reference to it. And **63 notes carry `PLACEHOLDER` text in typed fields**,
including where a relation should be.

### 1.4 Views are simpler than feared

18 `.base` files, **69 views, every single one `type: table`**. No cards, no list, no map, no
`summaries` section. 7 distinct functions. 21 formula expressions, 20 under 90 characters.
**75 of 101 note embeds are `![[X.base#View]]`** — Base views embedded in dashboards.

## 2. What the incumbents get wrong — converged findings

| | Notion | Obsidian (Bases + Dataview) |
|---|---|---|
| Money type | ❌ IEEE-754 double + a column-level currency *sticker*; API returns a bare unitless number | ❌ IEEE-754 double, no currency concept at all |
| Decimal exactness | ❌ proven from published function outputs | ❌ `1.10` and `1.1` are the same value; trailing zeros cannot be stored |
| Validation | ❌ none — and automations have **no reject action**, so it cannot be bolted on | ❌ none |
| Aggregation | ⚠️ *"always requires a Relation"* (their own certified consultants) | ⚠️ Dataview groups and sums properly; Bases gained cross-note rollups in 1.9.7 but they are **documented as not refreshing**, and cost an O(rows × vault) backlink scan |
| Write-back | ⚠️ API destroys data (below) | ⚠️ **Bases table cells DO write frontmatter** (via `processFrontMatter`, with undo/redo). Dataview is read-only — verified by source grep: one write call in the whole codebase, for task checkboxes |
| Silent failure | ❌ **the system's defining characteristic** | ❌ string-concat sum; nulls error; `FLATTEN` on `[]` drops rows |

Specific findings worth carrying into design review:

- **Notion's relation API destroys data on an ordinary write.** Reads return 25 items, writes
  replace the whole list, and there is no append/detach. Read-modify-write deletes items 26..N and
  returns `200 OK`. A sync vendor documents engineering around exactly this.
- **Notion documents its own trap**: *"a loop that just follows `next_cursor` until `has_more` is
  false looks like it finished, but on a 25,000-row database it quietly returns 10,000 rows."*
- **Notion relations are materialized arrays.** Their engineering, quoted publicly: *"we store
  relations as lists of page IDs, on the relation property itself"* — which is precisely why the
  reverse side caps at 10,000 and silently stops reflecting.
- **Obsidian frontmatter links are not real links.** A link written in YAML (`company: "[[Acme]]"`)
  is visible to Dataview but invisible to the graph, to outgoing-links, and to rename-refactoring.
  Only body inline fields (`company:: [[Acme]]`) are rename-safe. This is a trap for any
  CRM built on frontmatter.
- **Broken links never unify.** `[[Acme Corp]]` and `[[Acme Corp.]]` group separately forever.
- **Dataview is feature-frozen** — no functional commits in ~16 months; the maintainer's stated
  successor is a different project. Community pain reports cluster at 4,000–6,000 notes.
- **Bases has no relation property type at all** — no picker, no referential integrity. Requested
  2025-06-29, unanswered, absent from the roadmap.
- **Bases `groupBy` accepts one property only.** The founder's own CRM design note specifies
  *"grouped by `company` then `jurisdiction`"*; the delivered `CRM.base` has `groupBy: company`
  alone, because the second level is inexpressible.
- **Bases infers types from value SHAPE at read time**, and frontmatter key lookup is
  **case-insensitive** — so `Amount` and `amount` collide and which wins depends on key order,
  `id: 2026-01-01` silently becomes a Date, and a numeric string is *never* coerced to a number.
- **Bases `Duration.days` is evaluated relative to "now"**, so `duration("1M").days` returns
  28, 29, 30 or 31 depending on when it is read. Duration comparisons are not deterministic.
- **The `.base` format broke in five consecutive releases** in eight weeks, two of them
  unannounced. Stable for the thirteen months since — but the lesson stands for our own format.

## 3. Requirements

### R1 — Money is a first-class type
Integer minor units + ISO-4217 code + scale. Exchange rate and rate-date as first-class fields.
Never a bare float with a currency label beside it. Both incumbents fail here; this is where we
can be plainly better, and §1.1 proves the founder's own data already needs it.

### R2 — Complete, or explicitly incomplete. Never silently partial.
Every aggregate either answers fully or states what it could not include, in the payload, in a
field the caller cannot miss. §1.2 is the anti-requirement: a 1,211-character "total" is worse
than no total, because an agent will act on it and narrate it confidently.

### R3 — Validation on write, with defaults
Required, unique, closed enums, type checks, referential checks. Rejected writes are how an agent
learns the schema. Notion structurally cannot do this (no reject action anywhere in its automation
model); we can from day one.

### R4 — Store each relation once; derive the inverse
No materialized reverse arrays. One edge, one place, index computes the inverse. Avoids Notion's
10,000 cap, its sync lag, and its silent divergence.

### R5 — Stable IDs, independent of filename
`DEAL-142` style: monotonic, never reused, quotable by both agent and human, survives renames.
Today the vault has one slug in 751 notes and 1,175 filename-based references.

### R6 — Compute at query time; never persist a derived value
Into frontmatter where an agent cannot distinguish a stale rollup from a fact. Every Notion
pathology (depth caps, `unsupported` rollups, recompute storms) traces to persisting computed
values as schema columns.

### R7 — append / remove / set as distinct operations
Not read-modify-write. `set` should be the one you have to ask for.

### R8 — Schema as a reviewable file in git
Notion added schema audit logging in 2026: Enterprise-only, 365-day retention, and it does not
capture previous values. `git log` is better, free, and already ours.

### R9 — Aggregation without requiring a relation
Sum any property across any query result. Notion's hard ceiling; do not inherit it.

### R10 — Non-destructive write-back
Agents will edit constantly. Comments, key order, blank lines and quoting must survive. No Go
library does this — every one re-encodes, and re-encoding is lossy. Requires a byte-splice
approach; genuinely bespoke.

### R11 — Relations reference IDs, not display names
Follows from §2's broken-link finding and R5. A reference to a deleted or renamed target must be
detectable, not silently forked into a new group.

### R12 — Type errors are reported per record, not swallowed
The 22 PLACEHOLDER records must be nameable by a query — "which records block this total?" is a
question the system must answer.

## 4. Stack

No foundation exists to build on. The closest projects each fail decisively: **SiYuan** (Go, files,
query layer — **AGPL**), **zk** (Go, Markdown+YAML — **GPL and CGo**), **Anytype** (Go, but its
licence permits commercial use only on vendor-controlled networks), **AppFlowy** (Rust/Dart, not
Go). **LeafWiki** (MIT, Go, Markdown-on-disk, pure-Go index) satisfies every constraint but has no
query layer — copy its architecture, not its code.

Recommended components:

| Layer | Choice | Notes |
|---|---|---|
| Expression evaluation | `expr-lang/expr` | MIT, **zero dependencies**, +2.18 MB measured |
| Index & aggregation | `modernc.org/sqlite` | **already a dependency** (v1.46.1); FTS5 + JSON + generated columns verified CGo-free |
| Markdown | `goldmark` (pin v1.8.x) | v2 is RC; ecosystem still v1 |
| YAML surgery | `goccy/go-yaml` | only library exposing byte offsets, which R10 needs |

Two decisions worth recording:

- **SQLite alone, not SQLite + Bleve.** Bleve cannot aggregate at all — facets are counters, with
  no sum/avg/min/max — and its counting fails silently (measured: 135 of 500 documents vanished
  from a date facet without appearing in `Missing` or `Other`). A second derived index over one
  source of truth is two things that can silently disagree. Bleve stays in `pkg/memory`.
- **We already carry an archived dependency.** `gopkg.in/yaml.v3` was formally marked unmaintained
  in April 2025 and is in our `go.mod` at v3.0.1. Needs migrating regardless of this project.

A measurement that simplifies the design: a compiled `expr` filter over **100,000 notes evaluates
in 79 ms**. No query planner is needed for predicates. The index exists to avoid re-reading files
and to compute aggregates — not to make filtering feasible.

## 5. The biggest implementation risk

To make filters behave over real frontmatter (missing keys, `PLACEHOLDER` strings where numbers
belong), the comparison operators must be overloaded with `func(any, any) bool`. **The moment that
happens, `expr`'s type checker stops protecting those operators**, and a faulty comparator returns
a plausible boolean with no error anywhere.

This was hit during research: a first-attempt overload made `3 > 2` evaluate to **false**, and
nothing complained.

That is this project's signature failure mode — a control reporting success while doing nothing —
landing in the component users trust most. Mitigation is not subtle, only mandatory: an exhaustive
comparison truth table (every type pair × every operator × nil × missing key × type mismatch)
written from the specification **before** the comparators exist. Budget it as a deliverable, not as
test coverage added afterwards.

## 6. Open decisions

1. **`.base` reading — drop entirely, or keep as a one-way importer?** 18 files and 75 embedded
   views go dark on day one otherwise.
2. **Accounting ambition** — "correct totals and honest gaps" (R1 + R2), or real bookkeeping
   (multi-currency revaluation, periods, audit trail)? This materially changes the data model and
   is far cheaper to decide now than to retrofit.
3. **Query surface for agents.** Notion's own answer to LLM consumers was to ship SQL plus a
   compact text schema, reporting ~91% context-token reduction versus their JSON filter trees.
   Worth deciding early whether our agent-facing surface is SQL-like, expression-like, or both.

## 7. What is NOT verified

- Notion and Obsidian behaviour is from research agents' cited sources (primary-source labelled
  where available), not independently reproduced here.
- The Dataview `sum()` failure modes are derived from reading its source, not from executing
  Dataview. §1.2 applies *its documented semantics* to our real data — the arithmetic is ours and
  is reproducible; the claim that Dataview would behave identically is inference.
- Binary sizes, the 79 ms benchmark, and the Bleve facet loss are the Go survey agent's
  measurements on this machine, not re-run independently.
- Vault figures in §1 are measured directly and are reproducible from the vault.
