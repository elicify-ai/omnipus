# View Kinds — agent-authored views without manual scripting

Status: DESIGN — awaiting founder ratification
Date: 2026-09-03
Branch: feat/library-improvements
Author: agent session 7306a40a (with founder, over 2026-09-02/03)
Supersedes: nothing. Extends the saved-view model in
`vault-records-implementation-plan-2026-08-28.md` and the rendering rules in
the wireframe `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/vault-ui-wireframe.html`.

---

## 1. Problem

A saved view today is a single flat layout (`layout: table`) plus filter,
grouping, columns. Two consequences:

1. **Nothing composes.** A financial report is figures + grouped table with
   subtotals + an aging cross-table. A performance report is figures + chart +
   worst-offenders table. Neither fits "one layout". All 69 views in the
   founder's imported vault are flat tables that total nothing.
2. **The agent must be a vault expert.** The only authoring path is
   `knowledge_configure op=write_view`, which takes a raw `definition` map.
   The agent must know the filter tree shape, the grouping contract, and every
   correctness rule by heart. The worst rule to forget — never sum across
   units/currencies — fails silently: the result is a wrong number that looks
   right, not an error.

Founder rulings this design encodes:

- **Agent-first.** Humans edit views only as raw text in the edit pane. No
  designer/builder UI. The agent is the primary author.
- **The tools hold the expertise, not the agent.** (Same principle as the
  records layer: "tools should deterministically maintain the vault … without
  the agent much thinking about it.")
- **Vault-agnostic.** No rule may mention a domain ("invoice", "currency").
  Rules speak only in field kinds. Money is merely "a number with a unit";
  grams, hours and euros obey the same law.
- **Closed set.** The agent picks from named view kinds; it does not compose
  parts freely. `write_view` remains the raw escape hatch.

## 2. The model

Three layers. Each is a closed enumeration.

### 2.1 Field kinds (already exist in the records layer)

text · enum · relation · date · integer · decimal · person · checkbox —
the records layer's actual closed set — plus **number-with-unit** (the §5
addition). Any may be single or many.

**Correction (2026-09-03):** this section originally listed a "file/image"
kind and claimed it already existed in the records layer. It does not — no
such property type exists in `records.PropertyTypes`. See the tiles row in
§2.3 and D5 in §9 for how that is handled.

"Number-with-unit" is the one addition: a record type may declare that a
number property has a companion unit property (§5). This is **declared in the
record type, never inferred** — pairing any number with any nearby enum would
work on invoices and be wrong the first time a record holds two amounts.

### 2.2 Parts (internal vocabulary — renderer + composer only)

| Part | Draws | Requires |
|---|---|---|
| table | rows × columns | — |
| list | name + one detail | — |
| tiles | grid with images | a file/image property |
| columns | status columns (board) | an enum property |
| calendar | month grid | a date property |
| figures | headline numbers row | a number property |
| chart | line/bar over time | a date + a number |
| crosstab | rows × columns, aggregated cells | two group properties + a number |

Parts are **not** exposed to the agent as an authoring surface. They are the
units the composer emits and the renderer consumes.

### 2.3 View kinds (the agent-facing closed set)

| Kind | Stack (in order) | Offered only when the collection has |
|---|---|---|
| `table` | table | anything |
| `list` | list | anything |
| `tiles` | tiles | an image-capable property — **none exists yet, so tiles is currently never available** (D5) |
| `board` | columns | an enum property with ≤ 8 values |
| `calendar` | calendar | a date property |
| `summary` | figures → table (grouped, subtotals) | a number property |
| `trend` | figures → chart → table | a date + a number |
| `breakdown` | figures → crosstab | two groupable properties + a number |

Eight kinds. A financial report is `summary`. A software-metrics report is
`trend`. Aged receivables is `breakdown`. None are features; they are what the
kinds produce when the fields happen to be money or metrics.

Deliberately out (recorded so they aren't re-litigated): **map** (no
coordinate data, adds a dependency), **formulas/computed properties**
(inventing a formula language is a standing commitment; revisit on demonstrated
need), **free part composition** (untestable; the raw path covers the tail).

## 3. Gate rules (the whole of the tool's "judgement")

G1. A kind is offered **only** when the collection has the properties it
    requires. A refusal names the missing property and lists candidate
    properties if any near-miss exists (e.g. "board needs an enum with ≤ 8
    values; `status` has 26").
G2. Numbers total (sum/avg/min/max/count). A number-with-unit totals **once
    per unit value, never across units**. No combined figure is ever emitted;
    the renderer states why in a footer line.
G3. A row whose unit is missing/unconfirmed (import placeholder) is shown,
    **excluded from every total**, and counted separately.
G4. Text is never totalled, even when it parses as a number. The refusal
    offers the existing property-conversion path.
G5. Grouping is one property (Obsidian parity). Two-way grouping is exactly
    what `breakdown` is for.
G6. The composer either writes a complete, valid view or refuses. Never a
    partial file.

All six live in the composer and renderer — testable once, skippable by no
agent.

## 4. File format change

`SavedView` gains a part stack while staying back-compatible:

```yaml
name: unpaid--by-client
type: invoice                 # unchanged: the collection
kind: summary                 # NEW: which of the 8 kinds authored this
layout: table                 # legacy field, retained; = first table part
parts:                        # NEW: ordered stack the renderer walks
  - part: figures
    number: amount
    unit: currency            # names the companion property (must match §5)
    aggregate: sum
  - part: table
    grouping: [{property: client, direction: asc}]
    subtotals: {amount: sum}  # per-group, per-unit under G2
filter: {...}                 # unchanged, shared by all parts
properties: [...]             # unchanged: columns for table-ish parts
```

- A file with no `parts` is read as today: one part, `layout`. **All 69
  existing views load unchanged.**
- `kind` is provenance + re-edit affordance; the renderer walks `parts` only.
- SavedView is a generated wire type, so this is a **contract-first change**
  (Constraint #8): schema in `contracts/`, regen, then code. Same for the new
  tool argument shapes (§6) if they cross the wire.

## 5. Record-type change: declaring a unit

```yaml
properties:
  amount:
    type: decimal
    unit_property: currency   # NEW, optional; must name a sibling enum/text
```

Validation: `unit_property` must exist on the same type. Rendering: the pair
draws as one value ("12,480.00 SGD"); the unit property loses its own row.
Totals: G2/G3 apply. Where a number and a unit-like enum coexist undeclared,
`knowledge_describe` may *mention* the candidate pairing; nothing acts on it.

## 6. Tool surface (the actual "how does the agent know")

### 6.1 `knowledge_configure` gains `op: create_view` (composer path)

Arguments (flat, no nested definition):

```
op:          create_view
view:        unpaid--by-client        # name, same rules as write_view
kind:        summary                  # one of the 8
type:        invoice                  # collection (or scope, as write_view)
filter:      {...}                    # optional, same shape as today
number:      amount                   # kind-dependent bindings, validated
group_by:    client
date:        due_date                 # trend/calendar/breakdown as applicable
image:       cover                    # tiles
choice:      status                   # board
columns:     [file.name, client, due_date, status, amount]   # optional
sort/limit:  as today                 # optional
```

Behaviour: validate against §3 gates → assemble the part stack for the kind →
write the file → answer with the same cascade block `write_view` uses, plus
the assembled stack so the agent can read back what it built. Any gate failure
refuses with the G1 wording and writes nothing.

`write_view` (raw definition) and `delete_view` are unchanged. `write_view`'s
tool description gains one line steering agents to `create_view` for the
common cases.

### 6.2 `knowledge_describe` on a record type gains an "available views" block

For each of the 8 kinds: **available** (with which properties satisfy it) or
**unavailable** (naming the missing requirement). Example line:

```
views you can create here: table, list, summary (number: amount, unit: currency),
board — NO (no enum with ≤ 8 values; `status` has 26), trend — NO (no
number tracked over a date), ...
```

This is the discovery path: the agent asks, it does not remember. The same
block appears in the tool's own schema description in compressed form (the
8 kinds + their requirements), because the tool description is what is in
front of the agent at call time.

### 6.3 What the agent's flow looks like

1. `knowledge_describe type=invoice` → sees `summary` is available and which
   bindings to use.
2. `knowledge_configure op=create_view kind=summary ...` → done, or a refusal
   that names exactly what to fix.

No YAML authored by the agent on the normal path.

## 7. Renderer

The SPA's base/view surface (wireframe, sections "A base is a set of views",
"The accounting view", "One set of records, five shapes") walks `parts` in
order inside one view frame. New renderers: figures, chart, crosstab,
per-group subtotal rows, the G2 per-unit total footer and the G3 excluded
counter. Existing table/list/tiles/board/calendar behaviour per the wireframe.
Chart is the only genuinely new visual component; everything else is table
furniture.

## 8. Delivery order

1. **Contracts + records**: SavedView `kind`/`parts`, `unit_property`, regen.
2. **Composer + gates** (`create_view`) with unit tests per gate — G1–G6 each
   provably refusing and passing.
3. **Describe block** (6.2) — parallel with 2.
4. **Renderer parts** (7) — parallel with 2–3 once 1 lands.
5. **UAT**: fresh agent, GLM flash via API, on TWO vaults — the founder's
   import AND a synthetic non-financial vault (recipes or tickets) built by
   the corpus generator. Pass: agent produces a working `summary`, `trend`,
   and `board` from plain instructions with zero hand-written view files;
   every impossible request refused with the missing field named; no total
   ever crosses units.

## 9. Open decisions for ratification

- D1. The 8-kind set and the closed-set rule (recommend: as specified).
- D2. Formulas stay out for now (recommend: out).
- D3. `unit_property` as the money/unit mechanism (recommend: yes).
- D5. RULED 2026-09-03: no file/image property type exists, so `tiles` ships
  gated off — its availability check and its `create_view` gate both flow
  through ONE shared eligibility helper (in `pkg/knowledge/view_kinds.go`)
  that currently returns not-eligible for every type, with the refusal
  "no image-capable property type exists yet". Binding tiles to `text`
  (option a) was rejected: it would make tiles available on every vault and
  bind a rendering behaviour to unvalidated strings. When an image/file
  property type lands in the records layer (its own small design), enabling
  tiles is a one-helper change and discover/compose cannot disagree.
  Acceptance fixtures assert 7-of-8 available plus tiles-unavailable-with-
  this-exact-reason.
- D4. Naming of the 8 kinds as the agent sees them (`summary`/`trend`/
  `breakdown` vs alternatives) — pure naming, but frozen once shipped.
