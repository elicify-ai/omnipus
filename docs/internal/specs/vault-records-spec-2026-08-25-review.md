# Vault records specification — adversarial review, grill pass 1 of 2

- **Target:** `docs/internal/specs/vault-records-spec-2026-08-25.md`, Draft revision 4, 1,513 lines
- **Authority read:** `docs/internal/architecture/ADR-068-vault-records-typed-record-layer.md` revision 6 (at `b442a920`, which exists and is titled *"ADR-068 rev6 — two citations were off by a line"*)
- **Date:** 2026-08-28
- **Mode:** `plan-spec` (FR/SC identifiers, acceptance scenarios, traceability matrix, TDD plan)
- **Method:** every code citation re-read against the tree at
  `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements`; every SQLite
  semantics claim re-executed against `sqlite3` **and** against `modernc.org/sqlite v1.46.1`
  through the real Go driver.

---

## Executive summary

Nineteen CRITICAL and forty-one MAJOR findings. **Verdict: BLOCK.**

The revision-4 sync did close A-8 and A-9 as claimed, and the arithmetic the spec stakes itself
on is right — 98 → 95, ten seeded agents, thirteen contract files, five graph operations, every
line count. That is real and is recorded so it is not re-litigated.

But the failure mode this review was asked to hunt is present and it is present in the newest
work. **FR-020c — the freshness token, written specifically to replace a mechanism that was
asserted without existing — rests on two assertions about our code that are both false.** The
`Manifest` is not on the query path (no `Index` field holds one; `LoadManifest` has exactly two
call sites, both inside `SyncWith` and `CheckDrift`), and `Manifest` has no mutex, so the per-hit
lookup FR-020c budgets as "one in-memory map lookup" is neither in memory at query time nor safe
to share with the indexer. And the write ordering FR-020c mandates makes one of the two
divergence directions it claims to catch **undetectable**.

On A-11 the answer is worse than the spec's own warning. Of the seven defeat sites specified,
**zero are sufficient as written**. R-7 has no defeat at all and is violated four independent
ways. Thirteen further violations were found by execution that neither the spec nor the ADR
lists, twelve of them quiet — including one (join fan-out) that makes **every count and every
total over a filtered multi-value list silently wrong**. AC-8.4/SC-024, the control the spec
leans on, names two mutations for nine defeats and does not forbid the one implementation shape
that would let it pass while the SQL is broken.

Nine acceptance criteria are vacuous — they pass whether or not the requirement holds. Two pairs
of normative artifacts specify opposite behaviour for the same input. And the spec's own
supported scale (50,000 records) and its candidate cap (10,000) make ADR-068 §1.2's motivating
question — a CRM pipeline total — unanswerable by construction.

| Severity | Count |
|---|---|
| CRITICAL | 19 |
| MAJOR | 41 |
| MINOR | 17 |
| OBSERVATION | 8 |

---

## 1. CRITICAL findings

### C-1 — `Manifest` is not on the query path, and `Manifest.Get` is not concurrency-safe. FR-020c's central mechanism is asserted, not verified.

**Lens:** Incorrectness · **Section:** §1 table, FR-020c, FR-020g, AC-F5, SC-015, test 32

§1's table classifies `knowledge.Manifest` as **"read on the query path"** and concludes *"The
new work is the lookup and the column, not the value."* ADR-068 D16.5 makes the same claim in
its own words: *"a query-path lookup from `IndexHit.Path` into the live `Manifest` … That is the
cheaper of the two options and it needs no wire change."* Both are false, verified:

- The `Index` struct holds `idx/dir/blevePath/manifestPath/root/mu/regKey`. **No manifest
  field.** `LoadManifest` has exactly two call sites in the package — `pkg/knowledge/index.go:751`
  (inside `SyncWith`) and `pkg/knowledge/drift.go:218` (inside `CheckDrift`). Both bind it to a
  function-local and drop it on return. `Search`, `SearchFiltered` and `searchRaw` never
  reference it.
- `Manifest` has **no mutex** — `grep 'sync\.'` over `manifest.go` returns zero hits. `Get`, `Put`
  and `Remove` are bare map operations.

So the "live `Manifest`" does not exist. Satisfying FR-020c means either parsing `manifest.json`
on every query, or introducing a cached instance shared between the query path and a concurrent
indexer under a lock discipline that does not exist and is not designed. Either is real work with
a cost and a correctness question; neither is the free map lookup the design budgets for.

**This is the fourth wrong storage assumption in this document's history, and it is inside the FR
written to fix the third one.**

**Fix:** state what holds the manifest at query time, who owns its lifetime, and what guards it
against the indexer. Add an FR for it. Re-cost FR-020c against a per-query parse if no cache is
designed, and re-check FR-020c's "cost is one in-memory map lookup per hit" against that.
Add a `-race` test that runs `vault_find` concurrently with `SyncWith`.

---

### C-2 — FR-020c's own write ordering makes one of the two divergence directions undetectable. The "both directions are caught" claim is false.

**Lens:** Incorrectness · **Section:** FR-020c, AC-F5, SC-015, test 32, ADR-068 D16.5

FR-020c specifies the ordering **bleve document → SQLite row (with hash) → manifest entry,
manifest last**, and then claims:

> Both partial-failure directions are then caught by the one comparison — SQLite committed but
> bleve/manifest not, the manifest holds the *previous* hash and differs from the row's; bleve
> committed but SQLite not, the row holds the previous hash while the manifest holds the new one.

Trace it against the stated ordering:

| Failure point | bleve | SQLite `source_hash` | manifest `Hash` | Comparison | Detected? |
|---|---|---|---|---|---|
| after bleve, before SQLite | **new** | old | old (never written) | **equal** | **NO** |
| after SQLite, before manifest | new | **new** | old | differ | yes |

The second described case — *"bleve committed but SQLite not, the row holds the previous hash
while the manifest holds the new one"* — **requires the manifest to have been written despite the
SQLite step failing**, which the stated ordering forbids: the manifest is last. And the first
described case, *"SQLite committed but bleve … not"*, is unreachable under an ordering that puts
bleve first.

The reachable, real failure — bleve updated, SQLite not — leaves both hashes at the previous
value, compares **equal**, and is reported `COMPLETE: yes` while the properties index holds stale
values for that note. That is exactly the silent-wrong-answer class the specification exists to
remove, produced by the mitigation written to remove it.

Consequently **SC-015 and test 32 ask for a test of a case the ordering makes unreachable**
("SQLite updated, bleve not" — bleve is first), and do not test the case that is reachable and
undetected.

**Fix:** either (a) write the manifest entry **before** the SQLite row so the reachable failure
leaves them unequal, and re-derive both directions; or (b) keep manifest-last and add a second
token (e.g. a bleve-side per-document hash) so the bleve↔SQLite divergence is directly
observable rather than inferred through the manifest. Rewrite SC-015 and test 32 against
whichever ordering is chosen, and state explicitly which failure points are detectable and which
are not.

---

### C-3 — Of the seven specified defeats, ZERO are sufficient as written. Verified by execution.

**Lens:** Incorrectness / Incompleteness · **Section:** §8.1, AC-8.1..8.7, A-11

Every claimed defeat *site* is real and correctly diagnosed — all seven premises reproduced
exactly, including the `200.0` REAL typing. But every defeat as specified has a hole. Verified on
`sqlite3` and through `modernc.org/sqlite v1.46.1`:

**I-1 — the flagship R-2 defeat is a leaf rewrite; the problem is tree-shaped.** `(x IS NULL OR
x <> ?)` covers a leaf `=`. It does nothing for `NOT` over a subtree, which is the normal case for
the `{not: {all: [...]}}` filter grammar §4.1.2 declares:

```sql
SELECT id FROM t WHERE NOT (a=1 AND b=2);  -- returns only id 2; ids 3,4,5 (a or b NULL) DROPPED
SELECT id FROM t WHERE NOT (a=1 OR  b=2);  -- returns ZERO rows
-- correct only if De Morgan is pushed to every leaf first:
WHERE (a IS NULL OR a<>1) OR (b IS NULL OR b<>2);   -- 2,3,4,5
```

The natural implementation — compile the subtree, wrap it in `NOT (...)` — silently drops every
NULL-bearing row. **The spec never requires negation normalisation before emission.**

Two further holes in the same defeat: it covers only `=`/`<>` (`WHERE NOT (x>5)` drops the NULL
row; `WHERE (x IS NULL OR NOT (x>5))` is correct), and it breaks when the *needle* is absent
(`x <> NULL` is NULL, so a genuine value is dropped — violating R-12).

**I-2 — the R-1 defeat's "one typed column per declared property" does no work.** SQLite has
affinity, not types:

```sql
CREATE TABLE ti(n INTEGER); INSERT INTO ti VALUES ('3'),('3abc');
SELECT n, typeof(n), n>2 FROM ti;
-- 3    | integer | 1     (silently coerced)
-- 3abc | text    | 1     STILL TEXT, and '3abc' > 2 is TRUE
```

The **Go-side declared-type guard is the entire load-bearing half**, and the spec presents the
column as co-equal. `STRICT` tables exist in both builds and are not mentioned; a
`CHECK(typeof(n) IN ('integer','null'))` also works. Neither is specified.

**I-3 — `instr(col,?) > 0` reopens R-2 through the back door.**

```sql
SELECT typeof(instr(NULL,'x')>0);              -- null
SELECT id FROM t WHERE NOT (instr(note,'x')>0); -- the NULL-note row is DROPPED
SELECT instr('abc',''), instr('','');           -- 1 | 1  -- empty needle matches every row
```

`instr` needs the identical `IS NULL OR` wrapper, and empty-needle behaviour is unspecified.

**I-4 — the R-6 defeat guards `SUM` only.** `MIN`, `MAX`, `AVG`, `ORDER BY` and `GROUP BY` cross
currencies freely, and **ignore `scale` entirely**: `10000` minor units is `$100.00` or `¥10000`
depending on scale, so cross-scale `MIN`/`MAX` is meaningless even within one currency.

**I-5 — the R-5 ordinal defeat is silent on NULLs.** SQLite sorts NULL **first** ascending, so a
non-conforming enum value — the thing R-4 says must be reported as a problem — **heads page one**.
`NULLS LAST` is required and unstated.

**I-6 — the R-11 defeat catches errors, and three of the four third outcomes are not errors.**
`1/0` → NULL (no error, scans as nil through the driver); scalar integer overflow → silently REAL
(`9223372036854775807 + 1` → `9.22e+18`, `typeof=real` — exact-decimal money degrading to float);
`unixepoch('bad')` → NULL; `SUM` over an empty set → NULL. Asymmetrically, `SUM` overflow **is**
loud while scalar `+` is silent — same logical operation, three outcomes.

**I-7 — the R-4 defeat creates an R-2/R-3 collision.** "Never in the typed column" leaves NULL
there, which is the absence representation. Non-conforming and absent become indistinguishable in
storage unless the flag is consulted at every comparison *and* every `ORDER BY`.

**Fix:** rewrite §8.1's right-hand column so each defeat is a complete specification, not a
gesture: negation normalisation to leaves before emission; the Go-side type guard named as the
primary mechanism with `STRICT`/`CHECK` as the column-level backstop; `instr` wrapped for NULL and
specified for the empty needle; every aggregate and every ordering over money gated on one
currency *and* one scale; `NULLS LAST` plus problem-row exclusion from ordered sets; a
non-error-third-outcome list for R-11; and a presence flag consulted at comparison and ordering
time for R-4.

---

### C-4 — There is a tenth violation, and it makes every count and total over a filtered list wrong. Join fan-out.

**Lens:** Incorrectness · **Section:** §8.1 R-9 row, FR-028, FR-029, `join`/`aggregate`/`group_by`

The spec's R-9 defeat is *"a child-table equality join, not a string operation."* That is correct
for a single-value predicate and **silently wrong the moment two child rows match one parent** —
which is the entire point of a `many` property:

```sql
-- record 1 (amount=100) carries tags 'vendor' AND 'vendors'
SELECT COUNT(*)      FROM rec r JOIN tags t ON t.rec_id=r.id
  WHERE t.val IN ('vendor','vendors');   -- 2     truth: 1 record
SELECT SUM(r.amount) FROM rec r JOIN tags t ON t.rec_id=r.id
  WHERE t.val IN ('vendor','vendors');   -- 200   truth: 100
-- correct
SELECT COUNT(DISTINCT r.id) …            -- 1
SELECT SUM(amount) FROM rec r WHERE EXISTS(SELECT 1 FROM tags t …);  -- 100
```

Quiet, plausible, and wrong by a factor that varies per record. It reaches `aggregate`
(`count`, `sum`, `min`, `max`), the `join` parameter, and `group_by`. ADR-068 §4.2's own
"not native SQL" table names the adjacent case — *"D10 multi-value grouping … needs an unnest;
the naive `GROUP BY` produces Obsidian's 'Finance Business' defect"* — and **the spec carries
none of that table**: §8.1 addresses only the nine comparison rules, and no FR, AC or test in the
document covers unnest, `DISTINCT`, or `EXISTS`-vs-`JOIN` semantics.

**Fix:** add an FR requiring every aggregate over a filtered set to be computed over distinct
parent rows (`EXISTS` subquery or `COUNT(DISTINCT)`), add it to §8.1 as a tenth defeat, and add a
test asserting `count` and `sum` are unchanged when a record gains a second matching value of a
`many` property. Import ADR-068 §4.2's four-row "not native SQL" table into the spec — it is
currently referenced by neither §8 nor §8.1.

---

### C-5 — R-12 is violated by SQLite's affinity rules, and the violation contradicts the spec's own headline receipt.

**Lens:** Incorrectness · **Section:** §8 R-12, §8.1, AC-8.3

R-12: *"Every rule above applies identically whether the value came from a query literal or from
a record."* Verified false:

```sql
CREATE TABLE t(n INTEGER); INSERT INTO t VALUES (3);
SELECT (3 = '3'),           (SELECT n = '3' FROM t);   -- 0 | 1
SELECT (3 > '2'),           (SELECT n > '2' FROM t);   -- 0 | 1
SELECT ('2' > 3),           (SELECT '2' > n FROM t);   -- 1 | 0
```

Identical values, identical operator, **opposite answers** depending on operand provenance —
comparison affinity converts the TEXT operand only when the other side is a typed column. A
`BLOB`-affinity column restores literal behaviour, so the answer also depends on the DDL.

This directly undercuts the document's flagship receipt. §8.1 states `SELECT '3' > 2;` → `1` and
draws the R-1 conclusion from it — but against a *column* the same comparison returns `1` for a
different reason and against a `BLOB` column returns `0`. **R-12 is listed among the thirteen
rules, is not among the nine the spec says SQLite contradicts, and is contradicted.**

**Fix:** add R-12 to §8.1's table with its own defeat (normalise the literal side through the same
declared-type guard as the column side, before emission), and add the literal-vs-column asymmetry
as generated cells in AC-8.1's table.

---

### C-6 — R-7 (dates) has no defeat listed, and SQLite violates it four independent ways.

**Lens:** Incompleteness · **Section:** §8 R-7, §8.1

R-7 says *"`date` compares as an instant. A date and a date-time are the same declared type and
compare directly."* Under TEXT storage that is false:

```sql
-- same instant (both epoch 1787781600)
SELECT ('2026-08-27T00:00:00+02:00' = '2026-08-26T22:00:00Z');  -- 0  NOT equal
SELECT ('2026-08-27T00:00:00+02:00' > '2026-08-26T22:00:00Z');  -- 1  ordered anyway
SELECT ('2026-08-26T09:00:00Z' < '2026-08-26T09:00:00.500Z');   -- 0  fractional seconds invert
SELECT ('2026-08-26T09:00:00Z' < '2026-08-26 09:00');           -- 0  separator reorders
```

`ORDER BY ts` over four mixed forms returned `3,4,2,1`; correct instant order is `3,1,2,4`.
Timezone offsets, fractional seconds, separator choice and equality of equal instants are all
wrong, quietly. **§8.1 lists no defeat for R-7 and the rule is not among the nine.**

Integer-epoch storage fixes it — and introduces **M-12**: `unixepoch('not-a-date')` and
`unixepoch('2026-8-26')` both return NULL with no error, so an R-4 non-conforming date collapses
into the R-2/R-3 absence representation. R-4, R-2 and R-3 become mutually indistinguishable in
storage.

**Fix:** specify date storage explicitly (integer epoch with a declared precision is the only
form that satisfies R-7), add R-7 to §8.1 with that defeat, and add an FR that a date failing to
parse is stored flagged rather than as NULL — otherwise FR-021a's guarantee does not hold for the
`date` type.

---

### C-7 — `COLLATE NOCASE` defeats R-10 with no `LIKE` anywhere. AC-8.7 is not sufficient.

**Lens:** Insecurity / Incorrectness · **Section:** AC-8.7, FR-020 schema, test 39

AC-8.7 forbids `LIKE` in the compiled filter path. The DDL is the unguarded surface:

```sql
CREATE TABLE nc(id INTEGER, name TEXT COLLATE NOCASE);
INSERT INTO nc VALUES (1,'ACME'),(2,'acme'),(3,'Acme');
SELECT id FROM nc WHERE name='acme';        -- 1,2,3  all three
SELECT COUNT(DISTINCT name) FROM nc;        -- 1      (three distinct values under R-10)
SELECT name,COUNT(*) FROM nc GROUP BY name; -- ACME|3 -- facet counts silently collapse
```

`instr()` on that same column stays byte-wise, so `=` and `contains` **disagree about identity on
one column** — an internal inconsistency independent of the collation. And `GLOB` is not `LIKE`,
so it slips past an AC that names only `LIKE`.

The same mechanism breaks R-8: a relation-id column declared `COLLATE NOCASE` makes `rec_ABC` and
`rec_abc` the same key (this one is loud — `UNIQUE constraint failed`), so two legitimately
distinct targets cannot coexist.

**Fix:** widen AC-8.7 from "zero `LIKE`" to a schema assertion — no column in the properties index
declares a non-`BINARY` collation, and no index does either — plus "zero `GLOB`". Assert it over
the emitted DDL, not only over the filter path.

---

### C-8 — AC-8.4 / SC-024 does not force the nine defeats and does not exclude the one shape that would let it pass over broken SQL.

**Lens:** Infeasibility / Incorrectness · **Section:** AC-8.4, SC-024, A-11

A-11's entire mitigation is AC-8.4 mutation-checked. Two problems:

1. **Two mutations for nine defeats.** SC-024 names exactly two — removing the `IS NULL` arm of a
   negation, and swapping `instr()` for `LIKE`. A compiler with **no declared-type guard** (R-1),
   **no ordinal column** (R-5), **no separate money columns** (R-6), and **no flagged
   non-conforming storage** (R-4) passes both named mutations. Seven of the nine defeats have no
   mutation named, and A-11's stated exit ("W2 to report the mutation run") is satisfied by a
   two-mutation run.
2. **AC-8.4 does not forbid a Go post-filter.** It requires the table to run *"schema → filter
   object → compiled query → store"*. An implementation that emits sloppy SQL and corrects the
   result set in Go satisfies that path description and passes every cell — while the SQL is
   wrong, and while silently violating FR-066b (a Go post-filter must materialise rows the SQL
   should have excluded). This is the same defect §8 identifies for the Go comparator, one layer
   down.

**Fix:** enumerate a mutation per defeat (nine minimum, thirteen after C-4/C-5/C-6) and require
each to fail the table. Add an assertion that **the store returns exactly the rows the answer
contains** — a row count taken at the driver boundary equal to the rendered row count plus the
problem rows — so a Go post-filter is observable. Assert the emitted SQL text for at least the
negation and `contains` cases.

---

### C-9 — Two normative artifacts specify opposite behaviour for a cross-currency total.

**Lens:** Inconsistency · **Section:** §4.1.2 refusal table vs §4.2 worked example, FR-014, FR-125, US-2.3

§4.1.2's refusal table — *"These strings are contract, not illustration; a test asserts them"*:

| Cross-currency total | `2 currencies present (GBP, USD); no combined total is returned` |

§4.2's worked example — *"A test diffs against this shape"* — over the same GBP+USD corpus:

```
TOTALS: sum(arr) = GBP 465,000.00 over 5 of 12 rows — GBP only; 7 rows are USD and are not included
```

One refuses and returns nothing. The other returns a total. US-2 scenario 3 sides with the
refusal (*"no combined total is returned; the currencies present are listed instead"*); FR-125's
own example string is byte-identical to §4.2's. **Two documents both labelled contract, both
backed by a test, specifying different outputs for the same input.**

**Fix:** choose. If a per-currency total is the behaviour, delete the refusal row and amend US-2.3
and FR-014 to say "no *combined* total, but a per-currency total is returned". If refusal is the
behaviour, rewrite §4.2's TOTALS line — and note that FR-125's illustrative string then has to
change too.

---

### C-10 — R-6 and `sort` on a money property are not simultaneously satisfiable, and the worked example implements an unspecified third behaviour.

**Lens:** Infeasibility · **Section:** §8 R-6, §4.1.2 `sort`, §4.2

R-6: *"`money` compares only within one currency. Across currencies every operator is `false`."*
A total order requires every pair to compare. So `sort: [{property: arr, direction: desc}]` over a
mixed-currency column has no defined result under R-6.

§4.2 invokes exactly that and produces: all GBP rows descending, then all USD rows descending
(180k, 95k, 70k, 62k, 58k GBP, then 120k, 88k USD). That is a **currency-partitioned sort** —
neither a global numeric sort (120k USD would be second) nor a refusal. No FR specifies it, no AC
covers it, and the partition order (why GBP before USD?) is undefined.

Separately verified: `ORDER BY` over money crosses currency **and scale** with no complaint
(`10000` USD scale 2 ties with `10000` JPY scale 0).

**Fix:** add an FR for ordering a money property — either refuse unless the query constrains to
one currency and one scale, or specify the partition rule and its ordering — and make §4.2
consistent with it.

---

### C-11 — The problem list is unbounded, and FR-026's naming guarantee collides with FR-127's byte cap with no stated resolution.

**Lens:** Infeasibility / Inconsistency · **Section:** FR-025, FR-026, FR-123, FR-127, FR-127a

FR-026: *"A record excluded from an aggregate MUST be named in the problem list with the reason."*
FR-123 requires each line to carry its fix inline. FR-127 caps a response at **16,000 bytes**.
FR-064 permits a candidate set of **10,000**.

A query over 10,000 candidates of which 3,000 are non-conforming produces roughly 180 KB of
problem lines. **The spec's headline promise and its hard cap are not simultaneously satisfiable,
and no clamp on the problem list is specified anywhere.** FR-127a's degradation ladder is written
entirely about *rows* (`standard` → `minimal` → stop and page); it never mentions problems,
totals, or the header.

Worse, the ladder does not close even for rows. FR-063 permits 200 rows; at `minimal`
(~80 bytes/hit) that is 16,000 bytes — **the entire cap, with zero bytes left for the mandatory
`COMPLETE:`, `QUERY:`, `INDEX:`, `TOTALS:`, `PROBLEMS` and `NEXT` blocks**, every one of which is
a MUST (FR-121, FR-122, FR-020c, FR-125, FR-025, FR-126). The default page of 50 at the top of the
`standard` range (320 bytes) is 16,000 bytes on its own.

**Fix:** state the budget's allocation order explicitly — mandatory header and `NEXT` are reserved
and counted first, then problems up to a stated cap with a "showing N of M" clamp line (the
pattern FR-075a already uses for `check_integrity`), then rows. Add a clamp for the problem list
with its own FR and AC. Re-derive FR-127a's ladder against the reserved budget.

---

### C-12 — "Index generation" is used in five normative places and is defined nowhere. FR-020c explicitly rejects it as a concept.

**Lens:** Incompleteness / Inconsistency · **Section:** FR-020c, FR-020g, FR-048, §4.1.2, §4.2, AC-F3, §7 regression

FR-020c: *"Not an integer, **not a generation counter**"*, and *"Per note, deliberately, not per
index."* Yet the document then requires an index generation in five places:

| Site | Text |
|---|---|
| Edge-case table | *"Two indexes at different **generations**"* |
| §4.1.2 refusal | `that cursor was issued against index generation 8802; the index is now at 8814` |
| §4.2 header | `collection gen 8814` — inside the artifact a test diffs against |
| FR-048 | trash forgets the note *"in the same **generation bump**"* |
| FR-020g / AC-F3 / regression | *"the same collection at the same **generation**"* |

**No FR defines what a generation is, what increments it, where it is stored, or whether it is
per-collection or per-index.** `manifestVersion` is not it (verified: `manifest.go:48`, a
struct-schema constant a human bumps). This is the same shape as the defect FR-020c was written
to fix — a mechanism named in normative text with nothing behind it — reintroduced two paragraphs
below the correction.

**Fix:** either add an FR specifying the generation counter (and reconcile it with FR-020c's
argument against per-index staleness, which concerns *reporting*, not *cursor validity*), or
remove every reference and specify cursor invalidation, trash-index-removal and FR-020g's
agreement check against something that exists.

---

### C-13 — AC-X3 contradicts §4.1.5's own normative parameter table.

**Lens:** Inconsistency · **Section:** §4.1.5, AC-X3, FR-018a, FR-043

§4.1.5's table declares `expect_version` on **all three** `vault_restructure` operations:

| `rename` | `path`, `new_name`, `expect_version` |
| `move` | `path`, `dest`, `expect_version` |
| `trash` | `path`, `expect_version` |

AC-X3 in the same section: *"`vault_restructure` declares **no** `expect_version` on any cascading
op that cannot honour it."* Every operation of `vault_restructure` **is** a cascading operation —
that is the tier's definition. So AC-X3 says the parameter is absent from all three and the table
declares it on all three.

ADR-068 AC-15.5d sides with AC-X3: *"a test asserts no `expect_version` parameter is declared on
either"* (`vault_restructure` and `vault_configure`).

Compounding it: **FR-043 is unqualified** — *"A write MUST carry ADR-067 D14's version token; a
stale token MUST be refused"* — and `vault_configure` operations are writes. FR-018a exempts
`vault_configure` without amending FR-043, so FR-043 and FR-018a contradict each other directly.

**Fix:** delete `expect_version` from §4.1.5's table (matching AC-X3 and AC-15.5d), and amend
FR-043 to scope itself to `vault_edit` explicitly, citing FR-018a and AC-X3 as the exceptions.

---

### C-14 — The candidate cap makes the ADR's own motivating question unanswerable at the spec's own supported scale.

**Lens:** Infeasibility · **Section:** FR-064, FR-066, FR-066a, constraint table, ADR-068 §1.2

The constraint table supports **50,000 records per vault**. FR-064 refuses any query whose
candidate set exceeds **10,000**. FR-066 forbids returning an aggregate over a refused set.
FR-066a forbids relaxing the cap.

Therefore **a vault with 10,001 deals can never obtain a total over its deals — by design, with
no escape hatch.** The refusal's remedy (`add a filter on status (7 values)`) means running seven
queries and adding the results by hand, which is precisely the "five manual steps in the
incumbent" ADR-068 §1.2 cites as the cost this system removes.

The two bounds were set for different reasons — 50,000 from the spike's memory measurement,
10,000 from the spike's C-3 condition — and never checked against each other.

**Fix:** either lower the supported-records figure to the cap, or specify an aggregate-only path
that does not materialise candidates (a `COUNT`/`SUM` pushed entirely into SQLite, returning no
rows, exempt from FR-064), with its own FR, bound and AC. Do not simply raise the cap — FR-066a is
right that a measurement must come first.

---

### C-15 — FR-016's mandated count block is not computable as specified, and FR-015 contradicts AC-C1 on whether it is synchronous.

**Lens:** Infeasibility / Inconsistency · **Section:** FR-015, FR-016, AC-C1, SC-022, §4.1.6

`create_record_type` must report the count of notes converted **and name those that newly fail
validation** (AC-C1, SC-022: "reports 47 converted and names the 6 that newly fail"). Three
obstacles, none addressed:

1. **The population is precisely the one the properties index does not hold.** Before the schema
   exists, notes declaring `type: company` are ordinary notes (FR-005), and the properties index
   holds records. Finding them requires the bleve property-key/value fields FR-111 adds — and
   FR-111 never states whether those fields are populated for **non-record** notes. The count's
   computability rests on an unstated property of a different FR.
2. **Naming the newly-failing notes requires validating each one**, which requires their full
   frontmatter. Whether the fielded index stores retrievable values (bleve `Store: true`) or only
   indexes them is unspecified. If not stored, this is N file reads.
3. **FR-015 says a schema change "MUST … trigger revalidation"** — the word "trigger" reads
   asynchronous. AC-C1 requires the *response* to carry the results. **Direct contradiction.** And
   there is no bound: a vault with 100,000 notes declaring `type: meeting` validates 100,000 notes
   inside one tool call. FR-075a's 100,000-note cap applies to `check_integrity` only.

**Fix:** state whether property fields are indexed for non-record notes and whether values are
stored; specify revalidation as synchronous-with-a-bound or asynchronous-with-a-follow-up-call and
amend whichever of FR-015/AC-C1 loses; add a cap and a clamp message to `create_record_type` in
the pattern of FR-075a.

---

### C-16 — `vault_configure` has no concurrency control at all, and the refusal it relies on is a check-then-write race.

**Lens:** Insecurity (Tampering) / Incorrectness · **Section:** FR-018a, FR-003, §4.1.6, AC-C3

FR-018a forbids `expect_version` on `vault_configure`, on the ground that a single-file token
cannot guard a cascade. The argument is sound as far as it goes — but it removes the protection
the token *can* provide, and nothing replaces it:

- Two agents concurrently issuing `create_record_type company` both pass the "already declared"
  check and both write `.omnipus-vault/records/company.yaml`. **Silent lost update on the file
  that defines the type system**, with no error and no detection.
- FR-003's duplicate-type protection does not help: `create_record_type` writes one path per type,
  so the collision is *within* one file, not across two.
- FR-037's cross-process flock covers `.seq` only.

"Safety here is policy, plus the audit entry, plus `check_integrity`" — none of which prevents or
detects a lost update. Audit will show two successful writes and no anomaly.

**Fix:** add an FR requiring the schema/view write to be atomic and guarded (an `O_EXCL` create for
`create_record_type`; a content-hash CAS for `edit_record_type` that guards *the file*, documented
as guarding the file and explicitly **not** the cascade — which is honest and is not the thing
FR-018a rejects). Or take the flock. Add an AC for concurrent `create_record_type`.

---

### C-17 — `link` and `create` are C-B operations kept in tier 4 by asking only C-A. This is the exact failure the two criteria were introduced to stop.

**Lens:** Inconsistency · **Section:** FR-070b, §4.1.4, C-A/C-B, ADR-068 D15.1

FR-070b: *"A reviewer decides an operation's tool by asking **both** questions in order."*
C-B: *"Does this operation change what already-existing files **mean** — their validity, their
type, or how a query renders them — without writing them?"*

**`link`.** §4.1.4 defends it with a bytes argument only: *"Linking `Deal.md` to `[[Acme]]` writes
`Deal.md` and never touches `Acme.md` — it looks like a two-file operation and is a one-file
operation."* That answers C-A. Apply C-B: FR-032 makes the inverse **derived**, so after the link
`vault_find near="Acme"` and "the company's related deals" return a different answer for a note
nobody named. That is *how a query renders an existing file*. **C-B says `link` cascades.**

**`create`.** FR-033 reports a relation whose target does not exist as a validation finding.
Creating a note at a path an existing record links to **silences that finding on a note the agent
never named** — its validity changes. That is C-B, by the identical move the spec used to
reclassify `create_record_type` (a new file retroactively reinterpreting existing notes).

ADR-068 D15.1 says revision 5's defect was *"switching readings between two rows of the same
table"* and that naming both criteria fixes it. **Revision 4 names both criteria and still applies
only C-A to `link` and to `create`.**

**Fix:** apply both criteria to every row of §4.1.4 and §4.1.5 in writing, and record the verdict
per operation. If `link` and `create` stay in tier 4, the argument must be a stated exception to
C-B (as `.seq` is a stated exception to C-A), not an omission.

---

### C-18 — `trash` is placed in the C-A tier by assertion; under the two criteria it is C-B.

**Lens:** Inconsistency · **Section:** §4.1.5, FR-048, C-A/C-B

`vault_restructure` is defined as the C-A tier — *"the only tool permitted to write bytes into a
file the caller did not name."* `trash` moves **one** file and, per FR-048, explicitly **does not
repair inbound links**. It therefore writes bytes into no file the caller did not name. It is not
C-A.

What it does is break N existing notes' relations without writing them — **C-B**, which by
FR-070b makes it a `vault_configure` operation.

§4.1.5 addresses the placement only obliquely (*"Recoverability and blast radius are different
axes"*), which answers a question nobody asked; the criteria are never applied to `trash` at all.

**Fix:** apply C-A and C-B to `trash` explicitly and place it accordingly, or state why the
criteria do not decide it and what does. Note that the same reasoning applies to `rename` and
`move` in the opposite direction — those genuinely are C-A — so the tier is not homogeneous.

---

### C-19 — FR-080a seeds `vault_describe: allow` on a justification that is false for its widest operation, contradicting FR-070c.

**Lens:** Insecurity (DoS / Information disclosure) / Inconsistency · **Section:** FR-070c, FR-075, FR-075a, FR-080a

FR-070c is normative: *"the seeded default for a tool MUST be chosen for the **WIDEST** operation
it grants, not the most common one."*

FR-080a seeds `vault_describe: allow` for all four base agents, justified as: *"a prompt in front
of a read that `read_file` already permits protects nothing."*

`vault_describe`'s widest operation is `check_integrity` — a sweep of up to **100,000 notes**
resolving every wikilink in the vault. `read_file` does not permit that; it permits reading one
file. **The justification is false for the operation FR-070c says the default must be chosen
for.** And unlike the three write tools, `vault_describe` has **no rate limit** — FR-067
enumerates `vault_edit`, `vault_restructure` and `vault_configure` only, and §1 establishes that
`checkRetrievalRate` is not inherited by anything in this specification.

So the most expensive operation in the document is seeded `allow`, unprompted and unlimited, for
four agents, on a stated rationale that does not apply to it.

**Fix:** either apply FR-070c honestly (seed `vault_describe: ask`, or split `check_integrity`
onto its own tool), or bring `vault_describe` under FR-067's limiter with its own bound and AC.
Add a rate limit for `vault_find` while you are there — a 10,000-candidate query is not cheap
either.

---

## 2. MAJOR findings

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| **M-1** | Inconsistency | SC-013 vs FR-036a, AC-E2a | **SC-013 is the SC-005 sibling.** *"Every successful `vault_edit` changes exactly one file on disk"* — false for `create`, which FR-036a requires to write `.seq` as well, and which AC-E2a asserts changes **two** paths. AC-E1 was amended for this; SC-013 was not. | Amend SC-013 to "exactly one file, or exactly two on `create` (the note and `.seq`)", citing FR-036a. |
| **M-2** | Incorrectness | FR-110, FR-110a, SC-016 | **The doc-comment count was not synced to ADR revision 6, and the ADR corrected it explicitly.** ADR-068 D21.1 revision 6 enumerates **thirteen** locations and states *"Revision 5 listed seven. The round-5 review said twelve. **Both are undercounts**"*, with a numbered table. FR-110 still lists exactly revision 5's seven; SC-016 says *"the seven stale doc comments"*. ADR W2's exit criterion says "thirteen documentation corrections". The spec's §0 tables claim to enumerate every FR whose meaning changed in revision 6; FR-110 is absent from them. | Sync FR-110/FR-110a/SC-016 to thirteen and cite D21.1's table. |
| **M-3** | Incompleteness | FR-110a | **ADR D21.1 row 8 is not a doc comment.** `pkg/agent/memory.go:301` is a runtime `logger.WarnCF` reaching an operator: *"BM25 disabled for room"*, implying BM25 was ever in use. The ADR flags it as *"a user-visible correction … described that way in the changelog"*. FR-110a says only *"the stale doc comments above MUST be corrected"*, which does not reach a log string. | Add the operator-facing string as its own requirement with its own changelog obligation. |
| **M-4** | Incompleteness / Incorrectness | §3 first bullet, FR-025, AC-F4 | **ADR-068 D13.1 is new in revision 6 and the spec does not carry it at all.** D13.1 records that workspace scoping is the **one exception** to "names what it excluded" — a caller can receive `complete: true` over zero records while records exist. §3's first behavioural bullet states the guarantee unconditionally; FR-025 makes the problem list required; AC-F4 mandates `COMPLETE: yes — 0 records`. The spec never reconciles them. D13.1's own rationale is that *"an unstated exception to a headline guarantee is how a guarantee stops being believed"*. | Import D13.1 as an explicit carve-out on §3's bullet and on FR-025, and cite it from AC-F4 and FR-062. |
| **M-5** | Incorrectness (verified in code) | FR-079, FR-128, constraint table | **"Parameter descriptions … are paid only when used" is false.** The full JSON schema, including every property description, is sent on every request: `pkg/tools/registry.go:532` and `:560` build `providers.ToolFunctionDefinition{Name, Description, Parameters}` where `Parameters` is the whole schema map. Only **error messages** are paid on use. The ~900-token standing cost therefore omits six parameter schemas — `vault_find` alone has 15 parameters plus a recursive filter-tree schema. The design's own cost argument (D15.0, D22.8: "tool descriptions are the binding constraint") is computed against the wrong denominator. | Correct FR-079's claim, re-derive the standing cost including parameter schemas, and re-check D15.0's six-tools-are-affordable argument against the real figure. |
| **M-6** | Inconsistency | FR-127b vs §7 test 38 | **FR-127b says the ~150-token description budget is *"never enforced at runtime"* and that *"a test MUST NOT conflate them"*. Test 38 `TestTools_DescriptionTokenBudget` enforces it at runtime.** Whatever tokenizer it uses is one of the three FR-116 says disagree, none of which is the serving model's — the exact defect A-8 closed for the response budget. | Either delete test 38 and make the description budget a review checklist item, or name the tokenizer it is measured with and accept the caveat in writing. |
| **M-7** | Inconsistency | §4.1.2 `kind` default vs §4.2 | **The declared default `kind: note` contradicts the normative worked example.** §4.2 passes `type: deal` with no `kind` and returns deal records. If the default is `note`, either the example is wrong or `kind` is not a filter in the way the table implies. §7 test 25 diffs against this example. | Specify `kind`'s interaction with `type` — most likely `type` implies `kind: record` — and fix whichever artifact is wrong. |
| **M-8** | Ambiguity | FR-064, FR-066a, test 47 | **"Candidate" is never defined**, and it is the single most consequential bound in the document. Records of the named type before the filter, or rows surviving the filter? Test 47's *"refused without materialising one row"* implies a `COUNT(*)` with predicates applied — which is the query, doubling its cost on every call, unmeasured and unmentioned by A-7 or A-12. | Define "candidate" precisely, state where the count is taken, and add its cost to A-12's measurement obligation. |
| **M-9** | Ambiguity | §4.1.2 | **A `filter` with no `type` has no schema to validate against.** `type` is optional; FR-009 scopes property types to their record type; FR-023 requires validation against "the schema"; FR-024's refusal string presumes a type (`unknown property 'ownr' on record type 'company'`). Untyped filter semantics are unspecified. So is `vault_find` with no arguments at all, and the interaction between `view` and a conflicting `type`. | Specify: require `type` whenever `filter` names a property, or define cross-type property resolution and amend FR-024's message. |
| **M-10** | Incorrectness | §4.2, FR-125 | **The worked example's total is page-scoped and labelled a total.** The header says 17 selected / 14 evaluated / 12 shown; `TOTALS` covers "5 of 12 rows" — i.e. it excludes the two rows behind the cursor. No FR states whether aggregates are computed over the result set or the page. A page-scoped total is a wrong answer to the pipeline question §1.2 motivates. | Add an FR: aggregates are computed over the full evaluated set, never the rendered page, and the scope line says which. Fix §4.2. |
| **M-11** | Inconsistency | FR-072, FR-127, §4.1.3 | **`vault_read`'s `max_bytes` default of 40,000 is 2.5× FR-127's 16,000-byte hard cap.** FR-072 extends compact-text rendering to all six tools; FR-127 and the constraint table state the budget without scoping it to `vault_find`. | Scope FR-127 explicitly to the tools it governs, and give `vault_read` its own stated budget and truncation contract. |
| **M-12** | Inconsistency | §4.1.1 | **"Five categories, each capped at 500" — the example immediately below lists six** (duplicate id, unresolved, wrong type, broken link, orphan, orphan row). At 500 each and ~80 bytes a finding, a full sweep is 240,000 bytes against a 16,000-byte cap (see C-11). | Correct the count and specify how `check_integrity`'s output is budgeted. |
| **M-13** | Incompleteness | FR-048 | **A soft-delete with no restore is a hard delete.** FR-048 justifies the trash path by *"preserving the path so a restore is unambiguous"* — and no tool in §4.1 has a restore operation. There is also no retention policy, no purge, and no quota: the trash grows without bound. | Add a `restore` operation (and decide its tier — it is C-B by C-17's reasoning) plus a retention/purge FR, or drop the restore justification and call it a delete. |
| **M-14** | Infeasibility | FR-048 | **The trash path is invalid on Windows.** `<vault>/.omnipus-vault/trash/<RFC3339 timestamp>/…` — RFC3339 contains colons, which are illegal in Windows path components. The spec considers Windows elsewhere (the flock no-op in the edge-case table). | Use a colon-free timestamp form (`20260826T120000Z`) and say so. |
| **M-15** | Incompleteness | FR-020h | **The SQLite-less posture covers reads only.** FR-020h, AC-D6, AC-F6 and SC-023 specify `vault_find`, `vault_describe` and `vault_read`. Nothing states what `vault_edit`, `vault_restructure` or `vault_configure` do on `linux/mipsle` — whether `create` mints an identifier, whether FR-042's schema validation on write still runs, whether FR-039's duplicate detection is possible. §4.1.6 has one refusal string; §4.1.4 and §4.1.5 have none. | Specify all six tools' behaviour on a SQLite-less build, and extend SC-023 to cover the write tools. |
| **M-16** | Ambiguity | FR-036, FR-036a, SC-005a, §4.1.1 | **Is `.seq` one counter or one per type?** FR-036 requires uniqueness *within its type*; the examples use per-type prefixes (`CO-0142`, `DEAL-0091`) with independently low numbers, implying per-type counters. FR-036a and AC-E2a name a single file `.omnipus-vault/records/.seq`, implying one. AC-E2a's "exactly two paths" fails if there is one file per type and a create touches a per-type file. | State the format and whether it is one file holding a map or one file per type; reconcile AC-E2a. |
| **M-17** | Incompleteness | §4.1.4, AC-E2a | **AC-E2a is wrong for non-record creates.** `create` mints an identifier *"when `type` is a declared record type"* — so creating an ordinary note changes **one** path, and AC-E2a asserts exactly two. | Scope AC-E2a to record creates and add the one-file case. |
| **M-18** | Incompleteness | §4.1.5, §7 test 37 | **Test 37 asserts a behaviour §4.1.5 never specifies.** It requires every schema/view op to be *"refused by `vault_edit` **and** `vault_restructure`, naming `vault_configure`"*. §4.1.4 has the refusal strings; §4.1.5 has **no refusal table at all**. | Add a refusal table to §4.1.5 covering C-B ops and one-file note edits. |
| **M-19** | Overcomplexity / Inconsistency | FR-018, AC-C6, FR-070c | **A seventh tier is being argued away.** FR-018 concedes a saved view is *"C-B in its weakest form"* and justifies bundling it with type authoring by *"an operator granting one grants the other knowingly"* — a convenience argument, which is exactly what D15.2 rejects for a consolidated `vault_write`. Under FR-070c the seeded default must be chosen for `delete_record_type`, and AC-C6 confirms the description must advertise it. So an agent that needs only to save a view must be granted a tool whose stated widest power is reverting every record of a type to an ordinary note. | Either split views onto their own tool (or into `vault_edit`, with the C-B consequence stated), or replace the convenience argument with a criterion-based one. |
| **M-20** | Inconsistency | FR-113, FR-112, §7 test 28, AC-8.6, SC-018 | **Test 28 is vacuous with respect to what FR-113 gates on.** AC-8.6 asserts the same filter returns the same **set** under both rankings — which is guaranteed by the architecture: FR-021 puts membership in SQLite and FR-112's fusion is a Go-side ranking pass. The assertion cannot fail. Beyond that, test 28 only *"records the comparison"*. **FR-113 states no metric, no k, no threshold and no significance test**, so *"a fusion that does not beat plain BM25 MUST NOT ship"* is unfalsifiable. Holdout scenario 11 uses a human judge over 20 queries — a different, unstated criterion. And FR-112 is a MUST that FR-113 may forbid. | Name the metric (nDCG@10 or MRR), the corpus, the query set, the margin, and who decides. Make FR-112 conditional on FR-113 in its own text. |
| **M-21** | Infeasibility | SC-016, §7 test 26 | **Asserting an assignment exists is not a behavioural test.** Test 26 *"asserts the assignment exists; a doc-comment claim is not evidence"* — but a source-level assertion passes if the assignment is in dead code, applied to a different index than the one queried, or overwritten. Verified today: `ScoringModel` is assigned **nowhere** in the repo and `DefaultScoringModel = TFIDFScoring` (`bleve_index_api@v1.4.1/indexing_options.go:37`), so every bleve search in the tree ranks TF-IDF. | Add a behavioural assertion: a fixture corpus where BM25 and TF-IDF demonstrably differ (term saturation or length normalisation), asserting the BM25 ordering. |
| **M-22** | Infeasibility | §7 test 42, SC-023, AC-F6, AC-D6 | **A build-tagged test with no CI gate may never run.** Test 42 is *"build-tagged"*. The project's gates run `-tags goolm,stdjson`; nothing in the spec requires CI to build the SQLite-less tag combination, and `make build-all`'s `linux/mipsle` target is a **build**, not a test run. This is the documented "27% of the SPA suite never ran while CI was green" pattern. | Name the CI job, the tag combination and the make target that executes test 42, and add it to the gate list. |
| **M-23** | Incompleteness | §6 traceability | **Nine FRs have no test.** FR-005, FR-015, FR-019, FR-021a, FR-040a, FR-040b, FR-062a, FR-092 have no row in §6 and no test in §7. Two matter a great deal: **FR-021a is the entire R-4 defeat** (one of the nine A-11 flags), and **FR-015** is new mechanism — schemas live outside the scanner's walk, so an explicit change-tracking path must be built, with zero coverage. **FR-062a** (scope truncation ⇒ incomplete) is a P0-scope guarantee with no test, no AC and no SC. | Add rows and tests for all nine. |
| **M-24** | Incompleteness | §6, §8 | **The highest-risk item in the document has no FR.** R-1..R-13 are not functional requirements, appear in no traceability row, and AC-8.1..AC-8.7 appear in neither §6 nor the SC list except via SC-024. §7 test 6's Traces column reads *"see §8"*. | Promote the thirteen rules to numbered FRs and put AC-8.1..8.7 in the matrix. |
| **M-25** | Incompleteness | §6 | **Three acceptance scenarios are orphaned:** US-1.1 (no schema ⇒ ordinary note, no error), US-2.1 (all-valid corpus ⇒ complete true), US-3.5 (third hop refused). None appears in the Scenario column. US-3.5 in particular is the FR-065 refusal, mapped instead under US-2.5. | Add rows. |
| **M-26** | Incompleteness | §7 test 21 | **`TestRecords_PerfAtFiftyThousand` has no oracle** — *"no threshold until W1 measures the SQLite path"*. A test with no assertion cannot fail; it is a benchmark labelled as a test in the plan. | Move it to a benchmark, or gate it behind W1 with the threshold written in as a required follow-up. |
| **M-27** | Inconsistency | §7 tests 36 and 41 | **Two tests for one requirement, with divergent scope.** Test 36 (`…EditAndRestructureAreIndependent`) and test 41 (`…ThreeWriteTiersAreIndependent`) both trace to FR-083 and SC-012; §6 lists only the latter. Test 36 is the superseded two-tier version. | Delete test 36. |
| **M-28** | Inconsistency | §6 FR-077 row | **The audit test name omits the third tool.** `TestAudit_VaultEditAndRestructureCarryOperation` covers FR-077, which revision 4 widened to include `vault.configure`. AC-C5 covers it separately; the traced test does not. | Rename and widen. |
| **M-29** | Incompleteness | throughout | **The spec references W0–W6 eleven times and contains no wave plan.** FR-020e ships "in W0"; FR-070a completes "after W5"; A-10's residual is "a description review at W5"; SC-007 is "the W1 exit criterion"; FR-076a's cost is "measured at W2". None of W0–W6 is defined anywhere in the document, and §6 has no wave column. The spec is not implementable without the ADR open alongside it, and the wave assignment of individual FRs is therefore unreviewable. | Add a wave table (or a wave column to §6) mapping every FR to its wave, and reconcile it against ADR-068 D20. |
| **M-30** | Insecurity (DoS) | FR-067 | **Reads are not rate-limited and the spec says the read limiter is not inherited.** §1 establishes `checkRetrievalRate` has three call sites, none reachable from this specification's surface, and FR-067 covers the three write tools only. `vault_describe check_integrity` (100,000 notes) and `vault_find` (10,000 candidates, two-index) are both unlimited. | Extend FR-067 to `vault_find` and `vault_describe` with their own bounds and a test. |
| **M-31** | Inoperability | FR-070a, FR-084 | **Nine shipping tools are retired in one change with no deprecation window, no feature flag and no rollback.** Any user-authored skill, prompt or seeded policy naming `knowledge_search` breaks at W5. ADR-068 §4.2's only mitigation is *"the migration is cheap now"*. | Add a migration FR: an alias period, or a boot-time scan reporting every prompt/skill naming a retired tool, with the report as an exit criterion. |
| **M-32** | Inoperability | throughout | **No observability requirements whatsoever.** No metric, no log line, no alert, no health check for the properties index; no counter for FR-020c's stale-flag rate (the number A-7 says nobody knows); no way for an operator to see divergence except by running a query and reading a problem row. FR-020f adds a human panel snapshot and nothing else. | Add an operability FR: a counter for flagged-stale hits, a counter for index-rebuild events, and a log line naming the platform on a SQLite-less refusal. |
| **M-33** | Incompleteness | FR-020c | **The re-queue has no debounce.** *"A flagged record MUST be re-queued for re-indexing"* — a note under active editing flags on every query and re-queues on every query. | Bound the re-queue (per-note cooldown or a dedupe set) with a stated policy. |
| **M-34** | Incompleteness | FR-020e, W0 | **A forced rebuild of every existing index at upgrade has no availability contract.** No statement of how long a 100,000-document rebuild takes, whether the gateway serves during it, whether search returns "not built" or blocks, or what the user sees. | Add an FR for degraded-mode behaviour during rebuild and a bound on it. |
| **M-35** | Ambiguity | FR-072, §4.2, AC-P3, FR-127 | **"Compact text" is defined only by prohibition.** FR-072 forbids "a JSON document"; §4.2 gives one example of one query. There is no grammar, so two engineers build different renderers and test 25's "diffs against §4.2's literal example" covers one shape. Worse, §4.2's columns are **right-aligned to data-dependent widths**, so padding — and therefore the byte count FR-127 enforces and AC-P3 asserts byte-identical — varies with the page's contents. | Specify the rendering grammar (field order, separator, alignment rule, escaping) or drop alignment and use a fixed separator. |
| **M-36** | Ambiguity | AC-F4 vs FR-122, FR-126 | **AC-F4's "and no other signal" is unsatisfiable.** It mandates `COMPLETE: yes — 0 records` *"and no other signal"*, while FR-122 requires the `QUERY:` echo and FR-126 requires a `NEXT` block on every response. §4.2's zero-hit example carries both. | Reword AC-F4 to "no signal distinguishing scoping from an empty vault". |
| **M-37** | Incorrectness | §8.1, ADR-068 D16.6 | **The receipts were taken on the wrong engine.** Both documents say *"verified by direct execution against `sqlite3` 3.51.0"*. The engine that will actually run is `modernc.org/sqlite v1.46.1`, which reports `sqlite_version()` = **3.53.3** — two minor versions ahead. Every claim re-executed through the driver reproduced, so no conclusion changes; but the verification standard the document sets for itself was met against a proxy, not the artifact. | Re-state the receipts as taken against `modernc.org/sqlite v1.46.1` (3.53.3) and add a test asserting the linked version, since collation and affinity behaviour are version-sensitive. |
| **M-38** | Incorrectness (citation) | §0 correction 3 | **`repairAndValidateToolPolicyCoverage` is at `pkg/gateway/gateway.go:968`, not `pkg/config/validate.go`.** `validate.go` names it only in a doc comment (`:489`, `:494`), so a reader auditing the claim there finds a comment and no call. The *conclusion* is correct and important — repair runs first, backfills to `deny`, boot does not abort, contradicting root CLAUDE.md — and this is one of three corrections §0 exists to make permanent. It also emits **two** WARNs, not one. | Correct the citation and the WARN count. |
| **M-39** | Incorrectness (citation) | FR-020c | **`VersionToken` is at `pkg/knowledge/version.go:101`, not `author.go:309-323`** (which is `NoteContentVersion`). FR-020c's argument turns on distinguishing the authoring-path CAS token from the freshness token, so the wrong symbol weakens the distinction being drawn. | Correct. |
| **M-40** | Incorrectness (citation) | FR-076a | **`authoring_tools.go:1420` is the `TasksMaxFiles` clamp, not the per-file read.** The read is `:1434` (`ReadNoteContent`). FR-076a's argument is that `knowledge_tasks` walks and reads rather than querying an index — the read site is the load-bearing citation and it is the wrong one. (The clamp citation `:1420-1425` in the same sentence is correct.) | Correct to `:1434`. |
| **M-41** | Inconsistency | SC list | **SC-008 is missing with no annotation**, in a document whose §0 states *"Nothing below was renumbered silently"* and tracks every changed identifier in place. | Either restore SC-008 or record its retirement in §0's table. |

---

## 3. MINOR findings

| ID | Section | Finding |
|---|---|---|
| m-1 | FR-020c | `FR-039a` is cited as though it belongs to this spec; it is ADR-067's (`adr-067-knowledge-base-and-preview-spec.md:1918`). |
| m-2 | §1 table, FR-020c | `manifest.go:174` is `Get`'s doc comment; the function is `:175`. Same off-by-one at `detect.go:100` (field at `:101`) and `manifest.go:62-65` (comment is `61-63`). All point at the right symbol. |
| m-3 | FR-020h | *"Exactly one SHIPPED binary lacks SQLite"* — one *target*, two binaries (`omnipus-linux-mipsle` and `omnipus-lite-linux-mipsle`). And the causal mechanism is the `!mipsle` GOARCH constraint at `channel_matrix.go:1`, not `GO_BUILD_TAGS_NO_GOOLM`, which merely coincides. |
| m-4 | FR-071 | Dots are already stripped at the provider boundary — `SanitizeToolName` replaces `.` and `:` with `_` (`pkg/tools/registry.go`). The invariant test is still worth having; the stated rationale is weaker than it reads. |
| m-5 | §4.1.2 | `hops` is declared `int 1..2` in the parameter table **and** refused at 3 by FR-065 — two layers, and the spec does not say which produces the normative message. |
| m-6 | FR-011, edge cases | *"MUST reject an enum value outside the declared set"* reads as a write-time guarantee, but the premise (US-4) is that humans edit the same files in Obsidian. Only R-4/FR-021a's flagging path is reachable for externally-authored violations. |
| m-7 | FR-010, FR-020a | An enum reorder changes the derived ordinal column, so `edit_record_type` requires a properties-index rebuild for that type. No FR requires it, and FR-020a's "rebuild yields identical results" holds only for an unchanged schema. |
| m-8 | §4.1.6 cascade block | The mandated block reports validity counts only. An enum reorder changes the **ordering** of every existing report while reporting *"0 records lost validity"* — C-B's "how a query renders them" is not covered by the count. |
| m-9 | FR-036a | `.seq` lives inside `.omnipus-vault/records/`, the `vault_configure` control-plane directory, so `vault_edit: allow` + `vault_configure: deny` still grants writes into it. |
| m-10 | AC-P1 | *"a response whose COMPLETE line reads no and whose PROBLEMS block is empty is a defect"* is an invariant, not a test — no single test in §7 asserts it. |
| m-11 | AC-F3 | *"a corpus mutation between two identical `explain` calls changes nothing"* passes for a constant-returning stub, and also passes when `explain` does evaluate but the mutation happens not to change the plan. |
| m-12 | AC-P3 | *"the same wire object rendered twice is byte-identical"* asserts determinism of a pure function. The substantive half — *"every fact in the text is present in the wire object"* — is untestable as worded. |
| m-13 | §4.1.4 | `replace_body`'s `body` and `create`'s `body`/`frontmatter` have no size bound, while `vault_read` has `max_bytes`. |
| m-14 | FR-047 | Ambiguity is specified for `anchor`; nothing is specified for an `anchor` that matches **zero** times, or for `anchor` and `line_range` supplied together. |
| m-15 | §4.1.2 | `cursor` interaction with FR-064 is unspecified: if the corpus grows past 10,000 between pages, page 2 is refused mid-pagination. |
| m-16 | ADR-068 D15.6 | The ADR says the six-tool description cost is *"~750 for the surface as a whole"* — the five-tool figure. FR-128's 900 is right; the cited authority is stale. |
| m-17 | §7 test 25 | *"asserts no JSON document is emitted"* is not applicable to `vault_read`, whose body may legitimately contain a JSON code block. |

---

## 4. OBSERVATIONS

| ID | Finding |
|---|---|
| O-1 | **The C-A/C-B split is a semantic judgement where a mechanical one is available.** Every `vault_configure` operation writes exactly one file, inside `.omnipus-vault/`. "Writing `.omnipus-vault/` is `vault_configure`" is one path-shaped criterion, testable in CI, and it would have caught `.seq` (currently a named exception) and would settle `trash` and `link` without argument. Worth costing against the two-criteria design before W5. |
| O-2 | **FR-112's four-signal fusion is speculative under FR-113's own gate.** BM25F alone is the specified fallback. Shipping BM25F in W2 and adding fusion in a later wave, when the measurement exists, removes a large unmeasured component from the critical path. |
| O-3 | **Four independent response-size controls** — `limit`, `detail`, FR-127's byte budget, and FR-127a's degradation ladder — govern one dimension. The ladder exists because `limit` and the budget conflict (C-11); resolving that conflict may remove the need for the ladder. |
| O-4 | **`explain` largely duplicates the refusal channel.** FR-024's refusals already list valid names; FR-073's `explain` reports what a query would select and why properties could not be evaluated. The overlap is worth checking before it becomes two code paths. |
| O-5 | **§9 holdout scenario 14** (twenty natural questions phrased as negatives) is the strongest verification instrument in the document, and it is in the section marked *"Not for use during development"*. Consider promoting a fixture version of it into §7 — it is the R-2 failure in the form a user meets it. |
| O-6 | Recorded so revision 5 is not re-litigated: **every number the spec stakes its arithmetic on checks out exactly** — 98 quoted identifiers / 98 unique / 9 `knowledge_*` → 95 (and including comment lines yields 102, which is precisely where the old miscount came from); ten seeded agents; thirteen `Knowledge*.yaml` contracts; five `knowledge_graph` operations; `TasksMaxFiles = 5000`; `SearchDefaultTopN`/`MaxTopN` 20/100; `MaxNeighborhoodHops`/`Nodes` 3/500; all five `pkg/knowledge` line counts; all three `go.mod` pins; `modernc.org/sqlite` genuinely direct. |
| O-7 | Recorded likewise: **the per-agent policy claim in FR-083 is exactly right** — Mia (`core.go:1056-1058`) and Ray (`:1149-1151`) are `ask, ask, ask`; Ava (`:976-978`), Jim (`:1296-1298`) and the global ceiling (`defaults.go:644-646`) are `allow, allow, allow`. So FR-080a is a tightening for Ava and Jim, as the spec says. |
| O-8 | Recorded likewise: `resolveToolPolicyAtExec`'s signature at `pkg/agent/loop.go:12418` is **exact**, including the line number — the constraint the whole tool surface is derived from is verified. |

---

## 5. Structural integrity — `plan-spec` checks

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** (13 stories, all covered) |
| Every acceptance scenario reaches the traceability matrix | **FAIL** — US-1.1, US-2.1, US-3.5 orphaned (M-25) |
| Every FR appears in the traceability matrix | **FAIL** — nine FRs absent (M-23) |
| Every test in §7 traces to a requirement | **FAIL** — test 6 traces to "§8" (no FR); test 21 has no oracle; test 39 traces to an AC with no matrix row (M-24, M-26) |
| Every requirement has a test | **FAIL** — see M-23 |
| Success criteria are measurable and non-subjective | **FAIL** — FR-113/SC-018 has no metric or threshold (M-20); SC-013 is false (M-1); SC-016 is a source assertion (M-21); SC-008 missing (M-41) |
| Test datasets cover boundaries, edges, errors | **PARTIAL** — DS-1/DS-2/DS-3 are good on values, relations and write corpora. **No dataset for dates** (C-6), **none for multi-value/`many` properties** (C-4), **none for NULL/absence across a nested boolean tree** (C-3 I-1a), and **none for concurrency**. |
| Regression impact explicitly addressed | **PASS** — §7's Regression block is genuinely strong, and `TestKnowledgeSearch_RankingChangeIsIntentionalAndBounded` correctly refuses an impossible assertion |
| Non-functional requirements stated | **PARTIAL** — memory is a stated target with a W1 exit criterion; **no latency target anywhere, deliberately**; no throughput, no availability contract for the rebuild (M-34) |
| Rollback / operability addressed | **FAIL** — M-31, M-32, M-34 |

---

## 6. Test-coverage gap analysis

**Missing levels.** FR-020c's freshness comparison is specified as an integration test (32) only.
Given C-1, it needs a **race test** running `vault_find` concurrently with `SyncWith`, and given
C-2 a **table-driven test over every partial-write failure point** stating for each whether it is
detectable.

**Missing negative tests.** For every specified refusal string §4.1.x declares, only the
`vault_find` set has an AC (AC-F1). §4.1.6's ten refusal strings have no test row; §4.1.5 has no
refusal table at all (M-18).

**Missing boundary tests.** No test at the 10,000 candidate boundary itself (test 47 uses 24,000);
none at the 200-row page cap against the 16,000-byte budget (C-11 says they collide); none at the
500-findings-per-category clamp against the budget (M-12); none at the 100,000-note sweep boundary
against `create_record_type`'s unbounded validation (C-15).

**Missing concurrency tests.** Test 11 covers identifier allocation. Nothing covers: concurrent
`create_record_type` on one type (C-16); concurrent `vault_edit` on one note (holdout 6 covers it,
and holdouts are explicitly not for development); a query concurrent with an index sync (C-1).

**Missing idempotency tests.** `create_record_type` twice, `link` twice (the second is
`AppendSectionOnce`-shaped and unspecified), `trash` twice on one path (the timestamp directory
differs, so the second succeeds and produces two trash copies — unspecified).

**Regression blind spot.** §7's Regression block covers `pkg/knowledge/index.go`. **FR-110's
`ScoringModel` change alters rankings in `pkg/memrooms` and `pkg/agent` too** — the spec says so
(FR-110: "every memory-room recall and every long-term-memory query") and then names seam tests
for `pkg/knowledge` only. `retro_bm25.go`'s comparability claim is mentioned in prose and has no
test.

---

## 7. STRIDE summary

| Component | Threat | Status |
|---|---|---|
| `vault_find` | Information disclosure across workspaces | **Addressed** — FR-060/061/062, FR-024's scope-before-rejection ordering. But D13.1's admission that this makes `complete: true` dishonest is missing (M-4) |
| `vault_find` | DoS | **Gap** — no rate limit (M-30); candidate `COUNT(*)` cost unmeasured (M-8) |
| `vault_describe check_integrity` | DoS | **Gap (severe)** — 100,000-note sweep, seeded `allow`, unlimited (C-19) |
| `vault_configure` | Tampering — lost update | **Gap** — no CAS, no flock, check-then-write race (C-16) |
| `vault_configure` | DoS | **Gap** — unbounded synchronous revalidation (C-15) |
| `vault_configure` | EoP | **Addressed** — separate tool, separate policy, `deny` for workers (FR-080b), audited (AC-C5) |
| `vault_edit` | Tampering | **Addressed** — `expect_version` CAS, byte-preserving splice, audited |
| `vault_edit` | DoS | **Gap** — no size bound on `body`/`frontmatter` (m-13) |
| `vault_restructure trash` | Repudiation / data loss | **Gap** — no restore, no retention, no quota (M-13); invalid path on Windows (M-14) |
| Properties index | Tampering (integrity) | **Partial** — `source_hash` per row, but the mechanism is broken in one direction (C-2) and rests on an absent manifest (C-1) |
| Properties index | Spoofing (collation) | **Gap** — a `NOCASE` column silently changes identity for `=`, `DISTINCT` and `GROUP BY` (C-7) |
| All six | Repudiation | **Addressed** — FR-044, FR-077, AC-C5 |
| Mount surface | EoP | **Addressed** — FR-019, no second path |

---

## 8. Unasked questions

1. **What holds the `Manifest` at query time, and what guards it against the indexer?** (C-1)
2. **Which partial-write failure points are detectable, one by one?** (C-2)
3. **Is a Go post-filter permitted anywhere in the query path?** If yes, FR-066b and AC-8.4 both
   need rewording. If no, say so and assert it. (C-8)
4. **Are property fields indexed for notes that are not records?** FR-016's count depends on it and
   FR-111 does not say. (C-15)
5. **Are the fielded values *stored* retrievably in bleve, or index-only?** AC-C1's
   "names the ones that newly fail validation" depends on it.
6. **Are aggregates computed over the page or the result set?** (M-10)
7. **What does `vault_find` do with a `filter` and no `type`?** With no arguments at all? (M-9)
8. **What is an "index generation"?** (C-12)
9. **Is `.seq` one file or one per type?** (M-16)
10. **What do the three write tools do on `linux/mipsle`?** (M-15)
11. **What happens on a second `trash` of the same path?** Two timestamped copies, or a refusal?
12. **How does an operator discover that the properties index has diverged, without running a
    query?** (M-32)
13. **What does the gateway serve during the W0 forced rebuild of a 100,000-document index?** (M-34)
14. **Who runs the mutation testing AC-8.4 requires, against what mutation set, and what is the
    artifact that proves it ran?** A-11 says "W2 to report the mutation run" and names no report
    format, no owner and no mutation list beyond two.
15. **Does `vault_read`'s `path` resolve symlinks before the scope check?** Inherited from ADR-067,
    never restated, and `vault_read` is a new front door.

---

## Verdict

**BLOCK.**

Nineteen CRITICAL findings, of which four (C-1, C-2, C-3, C-4) are the failure mode the spec's own
§0 says it exists to stop: a property of the code or of the storage engine **assumed rather than
verified**, inside the work added to fix the previous instance of it.

The specific answers to the five questions this review was asked:

1. **Are the nine defeats sufficient and testable?** No. **Zero of seven are sufficient as
   written** (C-3). There is a tenth violation and it is the worst one — join fan-out silently
   inflates every count and total over a filtered multi-value list (C-4). R-7 has no defeat and is
   violated four ways (C-6). R-12 is violated by affinity and contradicts the spec's own headline
   receipt (C-5). `COLLATE NOCASE` defeats R-10 without `LIKE` (C-7). Twelve further quiet
   violations are listed above.
2. **Does AC-8.4/SC-024 force the truth through the compiled path?** No — two mutations for nine
   defeats, and nothing excludes a Go post-filter that would let the table pass over broken SQL
   (C-8).
3. **Is the freshness token real and complete?** The token value is real (`ManifestEntry.Hash` is
   exactly what the spec says it is). **The mechanism around it is not**: the manifest is not on
   the query path and is not concurrency-safe (C-1), and the specified write ordering makes one of
   the two claimed directions undetectable (C-2). The residual hole FR-020c1 names is therefore
   **not** the only one.
4. **Does C-A/C-B classify every operation?** No. `link` and `create` are C-B and are kept in tier
   4 by asking only C-A — the exact fault ADR-068 D15.1 says naming both criteria fixes (C-17).
   `trash` is C-B and is placed in the C-A tier by assertion (C-18). A seventh tier is being argued
   away (M-19). `vault_describe` violates the spec's own FR-070c (C-19). FR-016's count block is
   not computable as specified (C-15).
5. **Internal contradictions?** Ten, listed as CRITICAL. The sharpest are two pairs of normative
   artifacts specifying opposite behaviour for the same input (C-9, C-13), and the problem list
   colliding with the byte cap so that the document's headline promise and its hard bound are not
   simultaneously satisfiable (C-11). SC-013 is SC-005's sibling and is false as written (M-1).

Review written to:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements/docs/internal/specs/vault-records-spec-2026-08-25-review.md`

To address these findings, run:

```
/plan-spec --revise /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements/docs/internal/specs/vault-records-spec-2026-08-25.md /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements/docs/internal/specs/vault-records-spec-2026-08-25-review.md
```
