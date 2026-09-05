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
  parts freely. `write_view` remains the raw escape hatch **for the legacy
  shape** (D6, 2026-09-05).

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

**Amended 2026-09-05 by D6.** "The raw path covers the tail" above meant, and
now says explicitly, the **legacy view shape** — `layout`, `filter`,
`grouping`, `properties`, `aggregates`, `sort`, `limit`, `label`,
`property_config`, `formulas`. It has never meant the kind/part vocabulary.
`write_view` refuses a `definition` carrying `kind` or `parts`; those are
`create_view`'s to write, because the eligibility rules that give them meaning
run only there. Writing `kind: trend` on a type with no number is not an
uncovered composition — it is the closed set asserted through a door with no
check on it, which is what the UAT observed an agent do. See D6 in §9.

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

`delete_view` is unchanged. `write_view`'s tool description gains one line
steering agents to `create_view` for the common cases — and, per **D6**
(2026-09-05), `write_view` now **refuses** a `definition` carrying `kind` or
`parts`, naming `op=create_view` and why. Its remaining surface is the legacy
shape, which is also the shape hand-edited files and imported `.base` views
already have.

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
- D6. RULED 2026-09-05: **the gate belongs to the tool, not to one of its
  ops** — mechanical enforcement of §3's "skippable by no agent".

  **Evidence.** The UAT transcript
  (`docs/internal/specs/uat-findings-view-kinds-2026-09-05.md`, D1). Asked for
  a `trend` on a record type with no number property, the tester agent was
  refused twice by `create_view` — correctly, naming the missing number — then
  called `write_view` **ten** times until one succeeded, and reported "Saved —
  uat-task-trend is live and queryable". The file landed. The server returned
  `kind: trend`, `refusal: None`, `problems: []`, an empty figures row, an
  empty chart and 131 rows of table. A directed follow-up wrote `kind: tiles`
  bound to an enum — the exact binding D5 rejected for the record — and it was
  accepted and served the same way. Every G1 gate lived in
  `knowledge_configure_create_view.go`; `write_view`, the same tool one
  argument away, called none of them.

  The design's safety property is not "create_view refuses"; it is "an agent
  cannot author an impossible view". As shipped, a refusal was a speed bump:
  this model treated ten consecutive rejections as a puzzle to solve rather
  than an answer, and the tool let it win.

  **The ruling, in two halves.**

  (a) **Authoring.** `op=write_view` refuses any `definition` carrying `kind`
  or `parts`. The composer is the sole author of the kind/part vocabulary; the
  refusal names `op=create_view` and states why. `write_view` keeps the
  **legacy shape** — layout / filter / grouping / properties / aggregates /
  sort / limit / label / property_config / formulas — which is the actual
  escape-hatch tail and the shape hand-edited files and imported `.base` views
  have. The gate is on the VOCABULARY, not on the impossibility: refusing only
  impossible requests would leave two authors of one closed set, disagreeing
  the first time either was extended — the drift D5's "ONE shared eligibility
  helper" exists to prevent. §2.3's "raw escape hatch" line carries the same
  amendment.

  (b) **Serving.** The view endpoint never serves a vacuous or ineligible part
  silently. A `figures`/`chart`/`crosstab`/`tiles`/`columns`/`calendar` part
  whose bindings fail the **same** G1 checks the composer runs gets a recorded
  problem (`view_part_ineligible`) naming the failed requirement, `complete:
  false`, and no empty-but-clean rendering. The rule is not restated: the
  renderer's `knowledge.ViewPartBindingRefusal` delegates to the composer's own
  `gateG1RequireImage` / `gateG1RequireChoice` / `gateG1RequireDate`, so the
  two are one decision made once. This matters beyond `write_view`: a
  parts-bearing file that got in by any path — hand-edited, imported, written
  by a binary older than this ruling — is caught at read time.

  **Scope of (b).** Files that DECLARE a `parts:` stack. A legacy view carries
  a `layout:` and no parts, and `EffectiveParts` synthesises one part from it
  (a `columns` part with no `choice:`, a `tiles` part with no `image:`)
  because a file written before this design had none to give. Judging those
  would put a problem on all 69 of the founder's imported views — the renderer
  reporting the format's own history as a fault. §4's promise that they "load
  unchanged" holds.

- D7. RULED 2026-09-05: **G2 belongs to the ENGINE, not to the surfaces that
  read it** — `knowledge_find` totals a number-with-unit once per unit value,
  and the view endpoint stops compensating for the fact that it did not.

  **Evidence.** A deliberately-skipped failing test,
  `pkg/records/knowledgefind/unit_aggregate_g2_test.go`, written when the
  renderer half of G2 landed and kept rather than deleted so the defect stayed
  visible. Un-skipped on 2026-09-05, its observed red was:

  ```
  --- FAIL: TestFind_SumDoesNotCrossUnits_G2
      sum(amount) answered "300.50" — 100.50 SGD + 200.00 EUR is a figure in
      no currency; G2 admits no combined total
  ```

  `aggregate` reduced a column with no idea `PropertyDef.unit_property`
  existed. The gate lived in the composer and in the view renderer; the tool an
  agent calls directly had none. The interim protection was a wall around one
  door in a building with two: the view endpoint refused to route a view total
  through the engine at all, which left the gap reachable through
  `knowledge_find` itself and through a saved view's legacy `aggregates:` key.

  The skip named four questions the engine could not answer on its own. All
  four are ruled here.

  **D7.1 — The answer format: one total per unit value.** `VaultFindTotal`
  gains optional `unit` and `unit_property`, the same two fields with the same
  meanings `ViewUnitTotal` already carries (contract-first, Constraint #8). ONE
  requested aggregate yields **N totals**, one per distinct unit value, each
  with its own value, its own scope and its own unit. No field anywhere can
  hold a combined figure. A number with **no** companion unit still yields
  exactly one entry with `unit` absent — unchanged in shape, value and rendered
  bytes, because existing agents read that line.

  Which summaries are split is the **closed list already written for the view
  endpoint**, moved into the engine and delegated to from there
  (`knowledgefind.AggregateCrossesNoUnits`): `count`, `empty`, `filled` and
  `unique` answer with a COUNT, which is dimensionless and crosses no unit;
  every other summary either combines values (`sum`, `avg`, `median`,
  `stddev`, `range`) or selects one as representative (`min`, `max`,
  `earliest`, `latest`), and both answer in the unit's own domain.

  Rendered, in the compact text the model reads — **one line per total**, the
  unit printed with the figure:

  ```
  TOTALS: sum(amount) = 150.00 SGD over 2 of 5 evaluated rows (5 shown); 1 row(s) carry no amount and are not included; in SGD only — one total per unit value, never combined across units (G2); 1 row excluded from every total (G3), still shown: 1 row has no confirmed currency value
  TOTALS: sum(amount) = 200.00 EUR over 1 of 5 evaluated rows (5 shown); …
  ```

  The lines are deliberately NOT merged into one per-property sentence.
  Merging would make the renderer decide which half of each scope clause is
  shared between units, and a scope re-derived by a later layer is precisely
  what FR-125 forbids.

  **D7.2 — The memory bound, re-stated for N partitions.** B3 (FR-151) bounds
  what a population-class summary HOLDS. Partitioning does not make a value
  cost more, so the **single column budget is shared across every partition of
  one aggregate** rather than handed out per partition: the stated guarantee is
  re-stated for N partitions, not multiplied by unit cardinality. The buffer is
  therefore the caller's (`reduceAggregateSet` declares it; `scanColumn` takes
  it), and `columnBufferMaxValues` became a `var` for the same reason
  `maxViewResultRows` is one — the two designs are indistinguishable below the
  bound, so proving the shared one needs a corpus that crosses the bound in
  total while no single partition does.

  The **number of partitions** gets its own separate bound,
  `unitPartitionMax = 64`, which **refuses rather than degrading**. A unit
  column with more than 64 distinct values is not a unit column, and 64 totals
  already exceed what the 4 kB response budget can render or a reader can use.
  Past it the answer is a refused total naming the bound and the remedy — never
  a truncated list of totals, which would look complete.

  **D7.3 — The untyped case: refuse, mirroring the view endpoint exactly.** A
  query naming no `type` refuses to total any property **any** in-scope schema
  pairs with a companion unit; the rows are still shown and the refusal names
  the declaring types and the fix. A property no schema pairs with a unit keeps
  its unit-less total — with no declaration there are no units to cross
  (declared, never inferred, §5). The **reason** is one implementation
  (`knowledgefind.UntypedUnitTotalReason`) called by both surfaces; the
  **remedy** is deliberately per-surface, because declaring `type:` in a view
  file and adding `type=` to a request are different acts on different
  artefacts and one sentence fitting both would name neither.

  **D7.4 — The scope clause is emitted per unit.** Each per-unit total carries
  FR-125's clause over **its own** row subset with the **whole** evaluated set
  as the denominator (`over 2 of 5 evaluated rows (5 shown)`), followed by the
  G2 statement and the G3 exclusion count. The exclusion is repeated on every
  unit's line rather than stated once at the end: a reader who lifts one line
  out of the answer must lift the caveat with it.

  **G4 was probed, not assumed, and needed no change.** `parse()` already
  refuses a summary the property's declared type does not define (FR-155 /
  `opDefinedForType`), so `sum` over a text column holding "1200" and "3400"
  never reaches the reducer — 4,600 is unreachable at this layer, and an
  undeclared name in an untyped query resolves in the text domain by rule and
  meets the same gate. A regression test now says so, because the gate lives in
  a different file from the rule it serves.

  **Consequence for the view endpoint (b).** The interim compensation is
  **removed**, not kept as defence in depth. Now that the engine answers per
  unit, dropping a legacy `aggregates:` total would hide a CORRECT figure
  behind a refusal saying it could not be computed — the same "panel that omits
  a number the chat will state" failure that made the endpoint surface those
  totals in the first place. Its position-pairing invariant went with it: one
  declared aggregate is no longer one total.
