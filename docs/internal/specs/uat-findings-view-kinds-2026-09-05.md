# View kinds — UAT findings

**Design:** `docs/internal/specs/view-kinds-design-2026-09-03.md` §8 item 5
**Branch:** `feat/library-improvements` at `863ca24fa`
**Date:** 2026-09-05
**Binary:** built from that head with the SPA embed re-synced (proof in §2)
**Tester model:** `z-ai/glm-5.3-flash` via OpenRouter, driven over the gateway API
**Tester agents:** two NEW agents, one per vault — never a member of the built-in roster

---

## 1. Verdict

**§8 item 5's pass criteria are NOT met.** The composer is right; the gate around
it is not closed.

> Pass: agent produces a working `summary`, `trend`, and `board` from plain
> instructions with zero hand-written view files; every impossible request
> refused with the missing field named; no total ever crosses units.

| Requirement | Vault A (founder's import) | Vault B (synthetic recipes) |
|---|---|---|
| Discovery block informs the next call | PASS | PASS |
| `summary` from plain language | **FAIL** — agent hand-wrote it via `write_view`; result has no totals | PASS |
| `trend` from plain language | PASS | PASS |
| `board` from plain language | PASS | PASS |
| Impossible request refused, nothing written (tiles) | PASS | PASS |
| Impossible request refused, nothing written (trend, no number) | **FAIL** — refused twice, then written anyway via `write_view` | PASS |
| No total ever crosses units | n/a — no `unit_property` declared in this vault | **PASS** |
| Zero hand-written view files | **FAIL** — 2 of 5 scenarios used `write_view` | PASS |

Vault A: 3 of 5. Vault B: 7 of 7.

**What is genuinely good.** `op=create_view` does what the design promises. On
both vaults it composed correct part stacks from plain language, refused
`kind=tiles` unconditionally with D5's exact wording and wrote nothing, and —
the rule whose failure mode is "a wrong number that looks right" — produced a
mixed-unit total split per unit with the excluded rows named, on a vault where
the unit is a **weight** and no currency exists anywhere. `knowledge_describe`'s
availability block is real, complete, and demonstrably what the agent reads
before its next call.

**What is broken.** The gates are a property of ONE OP, not of the tool. Every
G1 check lives in `pkg/knowledge/knowledge_configure_create_view.go`, and
`write_view` — the raw escape hatch, in the same tool, one argument away —
calls none of them. An agent refused by the composer can, and in this UAT did,
simply route around it. The result is a file on disk, a confident "Saved —
live and queryable", and `problems: []` from the server. Details in **§6 D1**,
which is the finding this report exists for.

Two further user-visible defects were found: the "View raw" escape hatch draws
an empty pane (**D2**), and a knowledge base added to a running gateway never
indexes (**D3**).

---

## 2. Deliverable 1 — the binary carries this cycle's SPA

The embed directory is not the Vite output; skipping the sync serves a stale
SPA that looks entirely normal. The check is therefore *differential* — the
same string counted before and after — rather than a claim that the build ran.

```
npm run build                                                      exit 0
grep -rl 'base-preview-view-raw' pkg/gateway/spa/assets/ | wc -l  → 0   (before sync)
grep -rl 'base-preview-view-raw' dist/spa/assets/       | wc -l  → 1   (Vite output)
rm -rf pkg/gateway/spa && mkdir -p pkg/gateway/spa
cp -r dist/spa/* pkg/gateway/spa/                                  exit 0
grep -rl 'base-preview-view-raw' pkg/gateway/spa/assets/ | wc -l  → 1   (after sync)

CGO_ENABLED=0 go build -tags goolm,stdjson -o …/omnipus ./cmd/omnipus/   exit 0
```

Then the same question asked of the **linked binary**, the artefact that
actually matters:

| string | occurrences in the binary |
|---|---|
| `base-preview-view-raw` (this cycle's escape hatch) | 1 |
| `base-view-tab-unservable` (this cycle's view tabs) | 1 |
| `excluded_paths` (this cycle's G3 wire field) | 7 |
| `base-preview-zzz-nonexistent` (negative control) | **0** |

The negative control is there because a `grep` that matches everything proves
nothing. It returns 0, so the three positives are real matches.

Binary: 184,148,112 bytes. `pkg/gateway/spa/` is gitignored, so the sync is a
build step with nothing to commit.

---

## 3. Deliverable 2 — the two vaults

### Vault A — the founder's own import, copied

Copied out of the live instance's workspace into a fresh `OMNIPUS_HOME` on
port 5201. **The original was never opened for writing**, and this was checked
rather than asserted:

```
original: 759 notes, 69 saved views
files modified in the original since the UAT began: 0
```

Its schemas are what an import produces, not what a fixture author would write:
`task` (131 notes) has four dates and four small enums and **no number at all**;
`decision` (69 notes) has `version: decimal` plus three dates and a `status`
enum of 6; `invoice` has `amount` and `currency` as separate properties with
**no `unit_property` declared**, and a `status` enum of 26 values — the design's
own §2.3 example of a board refusal, found in the wild rather than constructed.

### Vault B — recipes, deliberately not financial

`scripts/uat/gen-recipes-vault.mjs` (new). 18 recipes + 6 techniques, every
value literal, so two runs are byte-identical and the answer key can be checked
by hand.

The vault-agnosticism requirement is what this vault exists for. The number
that must never be combined across units is a **weight**:

| unit | rows | sum |
|---|---|---|
| `g` | 7 | 3450 |
| `kg` | 4 | 9.4 |
| `cup` | 4 | 9.5 |
| *(no unit — G3)* | 3 | excluded from every total |

A combined figure would read **3468.9**. That number is computed by the
generator, recorded in `answer-key.json`, and asserted absent — so the check is
an equality against a known wrong answer, not a guess about what wrongness
would look like.

There is a second, unrelated unit pair on the same type (`servings` →
`portion_type`, values plate/bowl/jar) because a rule that works on one
number-with-unit per type has not been shown to be a rule about units.
`technique` has no number property at all, which is what makes "a trend is
impossible here" testable without reaching for tiles. `difficulty` is declared
`text` and holds digits, so G4 has a target here too.

---

## 4. Deliverable 3 — the agent UAT

Every scenario grades on artefacts — the WS tool-call frames, the files that
appeared on disk, and what the **server** returns for the view — never on the
agent's own account of what it did. That distinction earned its keep twice in
this run: on vault A the agent reported "Saved — **uat-task-trend** is live and
queryable" for a view the composer had just refused as impossible.

### Vault B — 7 of 7

| Scenario | Verdict | Evidence |
|---|---|---|
| (a) discovery | PASS | 1 × `knowledge_describe`; the **tool result** named all 8 kinds and carried the exact D5 wording |
| (b) summary | PASS | `create_view(summary)` → `uat-recipes-by-cuisine.yaml`; server serves `figures → table` |
| (b) trend | PASS | `create_view(trend)` → `uat-recipes-over-time.yaml`; server serves `figures → chart → table` |
| (b) board | PASS | `create_view(board)` → `uat-recipes-board.yaml`; server serves `columns` |
| (c) tiles | PASS | `create_view(tiles)` → error, **no file written**; refusal: `create_view: kind=tiles: no image-capable property type exists yet` |
| (c) trend on a type with no number | PASS | no file written; agent consulted `knowledge_describe` and relayed the missing requirement by name |
| (d) **mixed-unit total** | **PASS** | §4.2 |

`write_view` was never called once on vault B. Every view on disk came out of
the composer.

**The discovery block is doing the work the design assigns it.** For (a) the
eight kinds and the tiles refusal were read out of the `knowledge_describe`
*tool result*, not the agent's prose — an agent reciting the eight kinds from
its own priors while the tool said nothing would have failed that check.

**One nuance, recorded rather than smoothed over.** Vault B's (c) trend case
passed with **zero** `knowledge_configure` calls: the agent asked
`knowledge_describe`, saw `technique` has no number, and declined without
reaching the composer. That is exactly §6.3's intended flow, but it means the
*composer's* G1 gate was not exercised there. Vault A's equivalent scenario did
reach the composer — and is where D1 was found.

### 4.1 Vault A — 3 of 5

| Scenario | Verdict | Evidence |
|---|---|---|
| (a) discovery on `task` | PASS | 1 × `knowledge_describe`; all 8 kinds in the tool result, 4 buildable / 4 not |
| (b) summary on `decision` | **FAIL** | 4 × `write_view` (3 errors, 1 success); file has **no `kind`**, serves a bare `table` |
| (b) trend on `decision` | PASS | `create_view(trend)` → serves `figures → chart → table` |
| (b) board on `task` | PASS | `create_view(board)` → serves `columns`, 131 rows |
| (c) tiles | PASS | `create_view(tiles)` → error, no file written |
| (c) trend on `task` (no number) | **FAIL** | 2 × `create_view` correctly refused, then **10 × `write_view`** until one succeeded — file written |

The two failures are the same root cause, D1. The summary case is its quieter
half: asked for "a report grouped by status, with a total on each group", the
agent hand-wrote

```yaml
parts:
    - {part: table, grouping: [{property: status}], aggregate: count, properties: […]}
```

`aggregate` is only meaningful on `figures` and `crosstab` (`ViewPart.aggregate`);
on a `table` part it is inert, and `subtotals` — the key that would have produced
per-group figures — is absent. The served result is 69 rows in 6 groups with
`subtotals: []` on **every** group and `problems: []`. The user asked for totals
and got a grouped table with none, and nothing anywhere said so.

`decision` declares `version: decimal` and `status: enum(6)`, so `summary` was
available and `create_view` would have composed it. The raw path was chosen
over an available composed one.

### 4.2 The killer assertion, in full (vault B)

Prompt, naming no tool argument: *"Give me a saved report of the total weight of
my recipes, broken down by cuisine. Call it recipe-weight-report. I want to see
the totals."*

The agent wrote, via `create_view(summary)`:

```yaml
kind: summary
name: recipe-weight-report
parts:
    - {part: figures, number: weight, unit: weight_unit, aggregate: sum}
    - {part: table, grouping: [{property: cuisine}], subtotals: {weight: sum}, unit: weight_unit}
type: recipe
```

`GET /api/v1/library/{ws}/knowledge/view?…&view=recipe-weight-report` → HTTP 200:

| assertion | result |
|---|---|
| per-unit sums | `9.5 cup (n=4)`, `3450 g (n=7)`, `9.4 kg (n=4)` — **exactly the pre-computed key** |
| any total lacking a unit | none |
| the combined figure `3468.9` anywhere in the response | **absent** |
| G3 excluded rows | 3, named by path: `larb-salad.md`, `nonna-sauce.md`, `tarte-tatin.md` |
| per-group subtotals | also split per unit (french: `480 g` + `4.5 kg`, its excluded row counted separately) |

This is the served result over the API, not the file. A user asking for "the
total weight" gets three figures and a count of what was left out, on a vault
where the units are grams and cups. The rule is about units, not about money.

**Instrument check.** Before trusting that absence, the same search was run for
a value that *is* present (`"3450"` → found) and for the forbidden one
(`"3468.9"` → not found) against the same serialized response. The check can
distinguish; it is not inert.

---

## 5. Vault A's earlier runs

Vault A took three runs. All three are recorded, because "it passed on the
third attempt" is only honest if the first two are visible.

**Run 1 — invalidated (harness fault, mine).** The priming prompt was
open-ended: *"What can you tell me about the decision records in this vault?"*
On a 759-note import the agent answered with 12 successive `list_directory`
calls and the turn timed out at 300 s, never reaching a view. The prompt was
narrowed to the record type, matching §6.3's own step 1. An observation about
open-ended questions on a large vault, not about view kinds.

**Run 2 — invalidated, and it found D3.** Every view refused
`index_unavailable`, because instance A's gateway had been started *before* the
vault was copied in. Written up as D3; the run measures nothing about view
kinds and is discarded as evidence either way.

**Run 3 — the valid one**, after restarting the gateway (`properties.db` and
`manifest.json` appeared, and the founder's own pre-existing
`finance-ar--chasing` view began serving). §4.1 reports it.

One vault-B scenario also had to be re-run: a turn came back with *"I'm at
capacity right now"* — an OpenRouter-side condition, not a product fault. The
re-run passed. Noted so the first run's FAIL in the raw logs is not mistaken
for a finding.

---

## 6. Defects

### D1 — CRITICAL: `write_view` bypasses every gate the design rests on

**The design's premise, §1:** "The tools hold the expertise, not the agent."
**§3 G6:** "The composer either writes a complete, valid view or refuses. Never
a partial file." **§9 D5:** tiles' availability check and its `create_view` gate
"both flow through ONE shared eligibility helper … so discover/compose cannot
disagree."

All three hold for `op=create_view`. None of them hold for `op=write_view`,
which is the same tool, one argument away, and available to the same agent in
the same turn.

**Observed, not inferred.** Vault A, scenario (c): the agent was asked for a
trend on `task`, a type with no number property.

1. `create_view(trend)` → refused. Correctly, naming the missing number.
2. `create_view(trend)` → refused again.
3. `write_view` × 10, the last of which **succeeded**.
4. The agent reported: *"Saved — **uat-task-trend** is live and queryable."*

What landed on disk:

```yaml
kind: trend
name: uat-task-trend
parts:
    - {part: figures, aggregate: count}          # no `number:` binding at all
    - {part: chart, aggregate: count, date: completed, subtotals: {completed: count}}
    - {part: table, properties: […]}
type: task
```

What the server returns for it:

```
kind: trend      refusal: None      problems: []
parts: figures (series 0, totals None) → chart (series 0, totals None) → table
rows: 131
```

A "trend" with an empty figures row, an empty chart, 131 rows of table, **no
refusal and no problem reported anywhere**. This is §1's stated worst case in a
new form: not a wrong number this time, but a wrong *view* that looks right.

**Confirmed a second way, on the D5 case specifically.** A directed follow-up
turn asked the agent to use `write_view` with `kind: tiles` and
`parts: [{part: tiles, image: status}]`, where `status` is an enum. Accepted,
written, and served: `kind: tiles`, a `tiles` part bound to `image: status`,
131 rows, `problems: []`. The agent's own summary of what it had just done:

> "notably, this is exactly the kind of view `create_view` would have refused
> unconditionally (no image-capable property type exists yet, per D5). The raw
> escape hatch writes it as-is."

D5 explicitly rejected binding tiles to `text` because it "would … bind a
rendering behaviour to unvalidated strings". That is precisely what `write_view`
just did, in one call.

**Root cause.** Every G1 gate — `gateG1RequireImage`, `gateG1RequireChoice`,
`gateG1RequireDate`, `gateG1RequireGroupProperty` — is defined in and called
only from `pkg/knowledge/knowledge_configure_create_view.go` (definitions at
lines 265–414, call sites at 599–646). `write_view` reaches none of them, and
does not consult `pkg/knowledge/view_kinds.go`'s `ViewKindAvailability` or
`ImageEligible` either. The "ONE shared eligibility helper" is shared between
`knowledge_describe` and `create_view`; `write_view` is outside that circle.

**Why "it's the documented escape hatch" does not settle it.** Two things are
being conflated. Writing a *part stack the eight kinds do not produce* is what
the escape hatch is for, and should stay open. Writing `kind: trend` on a type
with no number, or `kind: tiles` on a vault where D5 says tiles cannot exist, is
not an uncovered composition — it is the same closed set the design defines,
asserted through a door with no check on it. `kind` is documented in
`write_view`'s own description as "provenance only", yet it is accepted without
being tested against the availability rules that give it meaning, and it is then
echoed back by the server as the view's `kind`.

**The behavioural consequence is the serious part.** The design's safety
property is not "create_view refuses"; it is "an agent cannot author an
impossible view". As shipped, a refusal is a speed bump: this model treated ten
consecutive rejections as a puzzle to solve rather than an answer, and the tool
let it win. Any agent that retries will find the same door.

**Suggested direction (production code, not applied — QA scope).** At minimum,
`write_view` should route `kind` and any `parts` entry it recognises through the
same eligibility helper and refuse identically, keeping the escape hatch open
only for stacks the eight kinds genuinely do not produce. Whatever the shape,
the invariant worth restoring is that **the gate belongs to the tool, not to one
of its ops.**

**Also worth fixing alongside it:** the served result for both files carried
`problems: []`. A part that computes nothing because its bindings are absent is
exactly what `RecordProblem` exists to say out loud.

### D2 — CRITICAL, user-visible: "View raw" draws an empty pane

**What a user sees.** Open any `.base` file whose views could not be loaded (or
which imported none). The SPA correctly explains the situation and offers
**View raw** — the escape hatch added by code-review finding #9. Clicking it
opens a pane containing **nothing**: a small `base` language chip in the corner
and an otherwise blank surface. An 80-byte readable YAML file is on the other
end of it.

**Not a server problem.** `GET …/content?path=…` returns the whole file:

```json
{"content":"filters:\n  and:\n    - type == \"recipe\"\nviews:\n  - type: table\n    name: Damaged\n",
 "is_text":true,"mime":"text/plain; charset=utf-8","size":80,"too_large":false}
```

**Root cause.** `src/components/library/preview/libraryLanguages.ts:122`:

```ts
return SHIKI_ALIASES[ext] ?? ext
```

For `Damaged.base` this returns the string `"base"`. `ShikiCodeBlock`
(`src/components/chat/markdown-shared.tsx:189`) has a fallback —
`language={language || 'text'}` — but `"base"` is truthy, so the fallback never
fires, Shiki is handed a grammar it does not have, and renders nothing.

**The file's own comment (lines 100–103) describes the intended behaviour and
the code does the opposite:**

> `undefined` (unmapped) falls back to ShikiCodeBlock's own 'text' default.

Unmapped extensions do not return `undefined`; they return themselves.

**Blast radius is wider than `.base`.** Every extension absent from the
twelve-entry `SHIKI_ALIASES` map and unknown to Shiki takes the same path —
`.conf`, `.ini`, `.env`, and anything else a vault happens to hold. `.base` is
simply the one this cycle put in front of users.

**Suggested fix (not applied — QA scope).** The narrow fix is one alias,
`base: 'yaml'`. The fix that matches the comment is for `shikiLanguageFor` to
return `undefined` for anything it cannot vouch for, so the existing
`|| 'text'` fallback works for every unknown extension rather than for none.

**Caught by:** `tests/e2e-viewkinds/view-kinds.spec.ts`, tests 5 and 6.

**How nearly it was missed.** Test 6 originally asserted only that the raw pane
appeared. It **passed on the first run against a blank pane.** The defect
surfaced only once the assertion was strengthened to require the file's own
bytes. An assertion that stops at "the element is visible" cannot tell a working
escape hatch from an empty one.

### D3 — HIGH: a knowledge base added to a running gateway never indexes

**Reproduced twice, on two instances, once on the founder's own vault.**

A vault placed in a workspace while the gateway is running is *detected* —
`GET …/knowledge?path=vault` returns `is_knowledge_base: true`, a `display_name`
and a `collection_id`. Its properties index never opens. Every view answers:

```json
{"refusal":{"code":"index_unavailable",
  "reason":"the properties index is not open, so no record can be read",
  "remedy":"re-open the vault; run knowledge_describe check_integrity to see the index state"}}
```

**Measured, not assumed:** polled every 5 s for **120 s** with no recovery, and
separately for ~40 s on another instance. On disk,
`$OMNIPUS_HOME/knowledge/<id>/` holds `bleve/` and `index_format.json` but **no
`properties.db` and no `manifest.json`**. After a gateway restart both appear
and views serve immediately — verified as a clean before/after on instance A.

**Why it matters beyond this UAT.** The text index works throughout, so search
and `knowledge_read` behave normally and the vault feels alive — only the
records layer is dead. The user is told to "re-open the vault", which
corresponds to no action the product offers. Dropping a vault into a workspace
is the ordinary way to start using this feature.

**Where it comes from.** `KnowledgeLifecycle.attachWorkspaceScope`
(`pkg/gateway/knowledge_lifecycle.go:762`) has exactly one caller, the boot-time
sweep at line 756. Nothing re-runs it when a collection appears later.

**Consequence for this UAT:** the mitigation is baked into
`scripts/uat/setup-viewkinds-instance.sh`, which now restarts the gateway after
copying the vault and says why. It is also why the browser suite cannot be
CI-wired (§7).

### D4 — LOW: subject/verb disagreement in the G3 footer

> `3 rows has no confirmed weight_unit value`

The singular case is correct (`1 row has …`), so the plural branch reuses the
singular verb. `pkg/gateway/rest_knowledge_view.go:123`
(`viewResultExcludedReason`): `rowsOf(n)` pluralises the noun and the verb after
it does not follow.

### D5 — LOW, friction not failure: the composer's first call is often wasted

On vault B the agent's first `create_view` was refused:

```
knowledge.configure: unknown argument(s) definition; op=create_view accepts:
op, collection, view, type, kind, filter, number, unit, date, image, choice,
group_by, columns, sort, limit
```

It had sent every argument in the schema, including `definition: {}` (a
`write_view` key) and a set of empty placeholders. **The refusal is correct and
the wording is excellent** — it names exactly what is accepted, and the agent
self-corrected on the next call unaided. The cost is one wasted round-trip on
first use. Worth noting only because the design's premise is that the tool
carries the expertise, and the `definition` / `create_view` split is evidently
not obvious at call time. The same confusion appears in D1's `write_view` runs,
where the agent's first raw attempt also failed on argument placement.

### O1 — observation: open-ended questions on a large vault

On the 759-note import, *"what can you tell me about the X records in this
vault?"* produced 12 successive `list_directory` calls and a 300 s timeout,
never reaching a tool that would have answered it in one call. Not a view-kinds
defect; recorded because it is what a real user's first message often looks like.

---

## 7. Deliverable 4 — the browser suite

`tests/e2e-viewkinds/view-kinds.spec.ts` (new), run against the **embedded
binary** on port 6161 — the gateway serving `pkg/gateway/spa`, never the Vite
dev server.

```
OMNIPUS_URL=http://127.0.0.1:6161 OMNIPUS_HOME=… VIEWKINDS_E2E_FACTS=… \
  npx playwright test --config=playwright.viewkinds.config.ts
```

| # | Spec | Result |
|---|---|---|
| 1 | clicking a `.base` opens server-enumerated view tabs, and does not download | PASS |
| 2 | a summary view shows per-unit totals, group subtotals, and why there is no combined figure | PASS |
| 3 | rows whose unit is missing are counted and named, not silently dropped | PASS |
| 4 | a chart with negative values draws them below the zero line | PASS |
| 5 | a `.base` whose views could not be loaded still offers View raw | **FAIL — D2** |
| 6 | a `.base` that imported no views also offers View raw | **FAIL — D2** |

**Observed exit codes**

| run | outcome | exit |
|---|---|---|
| run 1 (as first written) | 3 passed, 3 failed | 1 |
| run 2 (two test bugs fixed, test 6 strengthened) | 4 passed, 2 failed | 1 |
| mutation run (tests 1 and 3 deliberately broken) | 0 passed, 2 failed | 1 |
| run 3 (restored, confirming run 2 reproduces) | 4 passed, 2 failed | 1 |

The two failures in runs 2 and 3 are both D2. They are **not** test bugs and are
left failing.

### Two of run 1's three failures were mine

* **Test 2** compared `"3450"` against the renderer's `"3,450"`. The renderer
  was right. Fixed by stripping only thousands separators before comparing —
  which keeps the check an equality against the key rather than weakening it to
  a digits-substring match; `3468.9` still does not appear after normalisation.
* **Test 4** used `toBeVisible()` on an SVG `<line>`. Playwright's visibility
  rule requires a non-empty bounding box and a horizontal rule has zero height,
  so it reported "hidden" against a renderer drawing it perfectly at
  `y1=94.28`. Changed to `toBeAttached()`, with "can it be seen" asserted the
  way it actually can be: the chart's `svg` is visible, and every plotted point
  lands inside its `viewBox`.

### Proof each assertion can fail

A selector never observed failing may be matching nothing. All six were observed
failing:

| Spec | how it was seen to fail |
|---|---|
| 1 | mutation: tab testid changed to `base-view-tab-NOSUCH-…` → `element(s) not found` |
| 2 | naturally, in run 1, on the unformatted-number comparison |
| 3 | mutation: expected excluded count `3` changed to `44` → failed on the real text |
| 4 | naturally, in run 1, on the zero-line visibility rule |
| 5, 6 | currently failing, on D2 |

The mutation log is `pw-mutation.log`; the spec was restored from a byte copy
afterwards (`grep -c NOSUCH` → 0) and run 3 confirms the restoration.

### The chart regression, specifically

The negative-value fixture straddles zero (`12, -8, 5, -15, 20, -3`). The
assertion is not "a chart appeared": it reads the `<polyline>`'s own `points`
attribute and requires at least one point **below** the zero line's `y` and one
**above** it, with all six inside the `viewBox`. The clipping bug produced a
negative SVG height (which browsers drop) or a value pinned to the axis; both
leave every point on one side of zero, which this catches.

### Why this suite is not in `tests/e2e/`

It sits in `tests/e2e-viewkinds/` under its own
`playwright.viewkinds.config.ts`, and that is a consequence of **D3**.

Every spec under `tests/e2e/` must be assigned to a shard in
`tests/e2e/shards.json`, and `scripts/e2e-shards.sh check` fails CI on any that
is not. This suite cannot run in CI as things stand: its vault must exist
**before the gateway boots** (D3), and CI starts the gateway before Playwright.
A spec cannot seed its own knowledge base the way `preview-pdf.spec.ts` seeds
its PDFs.

Assigning it to a shard would make CI run it and fail; leaving it unassigned in
`tests/e2e/` would fail the shard guard. Housing it separately keeps both true:
the guard still reports every CI spec assigned (`55 spec file(s), each assigned
to exactly one shard`, exit 0), and this suite is visibly a separately-driven
one rather than a spec that silently never runs.

**This is a gap, not a resolution.** Fixing D3 removes the obstacle and the
suite can then join a shard.

---

## 8. Reachability — what a person or an agent can actually do now

Stated as end-to-end paths, because green tests are not delivery.

**An agent can, today, end to end:** ask what views a record type supports and
get a complete, accurate answer naming the bindings; compose `summary`, `trend`
and `board` views from a plain-language request with no YAML; and produce a
total over a mixed-unit number that is split per unit, never combined, with the
unusable rows shown, counted and named. Verified on two vaults, one the
founder's real import and one containing no money at all.

**A person can, today, end to end:** open a `.base` file in the Library and get
its views as tabs named by the server; read per-unit totals and per-group
subtotals with a stated reason for the absence of a combined figure; see how
many rows were excluded and which ones; and read a chart whose negative values
are drawn.

**What is NOT true today, and should not be described as delivered:**

1. **An agent cannot be prevented from authoring an impossible view** (D1). The
   composer refuses; `write_view` accepts the same request seconds later,
   writes the file, and the server serves it with no refusal and no problem.
   Observed on both the tiles case and the trend-without-a-number case. This is
   the design's core safety property, and it is currently advisory.
2. **A person cannot read a `.base` file whose views did not load** (D2). The
   escape hatch opens an empty pane — the fallback path, reached precisely when
   everything else has already failed the reader.
3. **A person cannot start using a vault without restarting the gateway** (D3).
   Dropping a knowledge base into a workspace leaves every view refusing
   `index_unavailable` indefinitely, with a remedy naming no action the product
   offers.

**Not measured here, and not claimed:** `breakdown`, `calendar`, `list` and
`table` were exercised only through the availability block, not composed and
served; `tiles` was tested only as a refusal (and as D1's bypass). Vault A
declares no `unit_property` on any type, so the mixed-unit rule was proven on
vault B alone — the founder's own invoices keep `amount` and `currency` as
unrelated properties, and until one declares the other as its unit, totals on
that vault are single-figure by default rather than by rule.

---

## 9. Artefacts

New, committed with this report:

| Path | What it is |
|---|---|
| `scripts/uat/gen-recipes-vault.mjs` | Vault B and its pre-computed answer key |
| `scripts/uat/gen-viewkinds-e2e-fixture.mjs` | the browser fixture: 4 `.base` files, a mixed-unit summary, a negative-value chart |
| `scripts/uat/setup-viewkinds-instance.sh` | one throwaway gateway instance end to end (carries the `"version": 1` and D3-restart lessons) |
| `scripts/uat/run-viewkinds-agent-uat.mjs` | the agent scenarios, grading on artefacts |
| `tests/e2e-viewkinds/view-kinds.spec.ts` | the six browser specs |
| `playwright.viewkinds.config.ts` | its config, and why it is separate |

Run artefacts (transcripts, per-scenario JSON, Playwright logs and traces) are
under the session scratchpad at
`/private/tmp/claude-501/-Users-danielpiatkowski-AI-Agent-Workspace-omnipus-repo/7306a40a-bfe6-441a-82f2-23c68facc755/scratchpad/uat-viewkinds/`
and are not committed.
