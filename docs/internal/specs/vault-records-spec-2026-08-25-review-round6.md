# Grill review — vault-records specification, PASS 2

- **Target:** `docs/internal/specs/vault-records-spec-2026-08-25.md` — Draft revision 5 (2,174 lines)
- **Authority read first:** `docs/internal/architecture/ADR-068-vault-records-typed-record-layer.md` — revision 7 (2,755 lines)
- **Prior review:** `vault-records-spec-2026-08-25-review.md` (pass 1: BLOCK — 19 CRITICAL, 41 MAJOR, 17 MINOR)
- **Date:** 2026-08-26 · **Mode:** `plan-spec`
- **Method:** every SQLite claim re-executed against `sqlite3` 3.51.0; the linked engine version re-verified through `modernc.org/sqlite` itself; every Go case-folding claim executed; every code citation checked against the tree at `03e71909`.

---

## Verdict: **BLOCK**

| Severity | Count |
|---|---|
| CRITICAL | 11 |
| MAJOR | 31 |
| MINOR | 14 |
| OBSERVATION | 5 |

---

## The question the brief asked first: did a SQL-side comparison survive?

**Yes. Seven of them, in normative text, and one of the seven is mandated in so many words.**

The ruling is enforced by prose alone. **No acceptance criterion and no test anywhere in the
document asserts that SQLite performs no comparison** — and the only two artifacts that ever
inspected emitted SQL or DDL (§7 test 39 `TestFilter_NoLikeInCompiledPath`, and AC-8.8's
`BINARY`-collation assertion over emitted DDL) were both **deleted in this revision**, as having
"SQL-side comparison as their only subject". The revision removed its own instrumentation for the
property it now depends on entirely.

| # | Surviving SQL-side evaluation | Where | Severity |
|---|---|---|---|
| 1 | *"the `COUNT`/`SUM` is **pushed entirely into SQLite** and no candidate is retrieved"* | **FR-064a** (`:897`) | C-3 |
| 2 | The candidate count is *"a `COUNT(*)` over **the compiled `WHERE` clause**, taken before any row is materialised"* | **FR-064** (`:895`), §7 test 47 | C-4 |
| 3 | The conformance flag *"MUST be consulted at comparison time **and at `ORDER BY` time**"* | **FR-021b** (`:791`) | M-7 |
| 4 | *"a **`GROUP BY`** over 10,000 rows still returns a result set that must not be materialised whole"* | **FR-066b** (`:904`) | M-7 |
| 5 | The RSS budget still charges *"SQLite's page cache, its temp b-trees for **`GROUP BY`/`ORDER BY`**, and its connection state"* | **SC-007** (`:1484`) | M-7 |
| 6 | *"it **orders lexically — SQLite's own ordering**"* / *"an enum sorts lexically, **like SQLite**"* | **R-5** (`:1779`), FR-010 (`:636`), §4.1.2 `sort` row (`:1079`) | M-7 |
| 7 | The relation-identifier side *"is matched exactly, **on a `BINARY` column**"* | **R-8** (`:1782`) — and its only test was deleted | M-5 |

Items 1 and 2 are the serious ones. Item 1 **reopens grill pass 1's worst finding by name**: a SQL
`COUNT`/`SUM` over a candidate set that includes the relation child table is exactly the join
fan-out that returned 2 and 200 where truth was 1 and 100. Item 2 makes the document's single most
consequential bound — the one A-14 leans on as *"a refusal, not a hope"* — unimplementable as
specified.

And the document contradicts itself on item 1 within its own §8.1: *"FR-064a's aggregate-only
exemption is the one place the cap is relaxed, which is now a **Go** scan over a candidate stream
**rather than a SQLite pushdown**"* (`:1879-1880`). Two normative statements, opposite mechanisms,
one requirement.

---

## Executive summary

Revision 5 is a genuine improvement and the deletions were the right call. The receipts reproduce:
I re-executed every SQLite claim in §8.1/§8.1b against `sqlite3` 3.51.0 and **every one holds**,
including the corrected `NOT (a=1 OR b=2)` → one row that pass 1 got wrong. I opened a database
through `modernc.org/sqlite v1.46.1` (`go.mod:64`) and it reports `sqlite_version()` = **3.51.2** —
the spec's rejection of pass 1's 3.53.3 figure is **correct**. The type-count arithmetic is
**correct** (−`money` −`number` +`integer` +`decimal` = seven). `allStaticToolNames` is **98
identifiers, 98 unique, 9 of them `knowledge_*`** — counted; 98 − 9 + 6 = 95 is right.
`resolveToolPolicyAtExec` really is at `loop.go:12418`. The citation discipline is markedly better
than pass 1 found.

What went wrong is what the brief anticipated: **the deletions were not finished.** Seven SQL-side
evaluations survive. Three normative places still require the withdrawn Go-side fold column and one
of them states the **opposite outcome** from the requirement that replaced it. The §8 rule table —
promoted to numbered requirements — still mandates the withdrawn integer-epoch date storage.
Pass 1's C-1 is **not closed**: §1's reuse table retains verbatim the sentence C-1 flagged, and four
normative places including a mandatory AC still compare against `ManifestEntry.Hash`, the mechanism
FR-020c spends nine bullets disproving.

Two failures are worse than leftovers, because they are new assertions made with the same confidence
the document exists to eliminate:

- **FR-011a's mechanism cannot deliver what FR-011a promises.** The spec correctly established by
  execution that SQLite folds ASCII only, then asserted that `strings.ToLower` gives *"full-Unicode
  case insensitivity for free"*. **I executed it.** It does not. `strings.ToLower` is *simple* case
  folding, not full: the spec's own named test case — §7 test 53's `= 'straße'` matches `STRASSE` —
  is **false**, under `strings.ToLower` and under `strings.EqualFold` alike. Greek final sigma is
  **false**. Turkish dotless i is wrong. **Three of the four languages holdout scenario 15 names
  fail.** The document caught this exact failure class in SQLite and then committed it in Go.

- **FR-004a's denylist test cannot pass, and the "verified clean" claim behind it does not
  reproduce.** Both documents state *"the code is already clean — zero domain vocabulary outside
  tests in `pkg/records` and `pkg/knowledge`"*. There are **49 whole-word hits in non-test files**,
  including `pkg/records/doc.go:12` — which is the D0 statement itself. A test written to FR-004a's
  words red-lights the build on the day it lands.

---

## 1. CRITICAL findings

### C-1 — FR-011a's mechanism cannot deliver full-Unicode case insensitivity. Verified by execution.

**Lens:** Incorrectness · **Sections:** FR-011a, R-10, SC-002d, §7 test 53, §9 scenario 15, DS-1

FR-011a (`:642`): *"**`strings.ToLower` is Unicode-aware**, so this is full-Unicode case
insensitivity for free, in the place the ruling asks for it."* §7 test 53 (`:1638`) makes it
normative: *"`LIKE 'äcm%'` matches `ÄCME`; **`= 'straße'` matches `STRASSE` per Go's folding**."*

Executed on this machine, Go stdlib:

| Pair | `ToLower(a)==ToLower(b)` | `EqualFold(a,b)` |
|---|---|---|
| `straße` / `STRASSE` | **false** | **false** |
| `ΣΟΦΟΣ` / `σοφος` (Greek final sigma) | **false** | true |
| `İstanbul` / `istanbul` | true | **false** |
| `äcme` / `ÄCME` | true | true |
| `ŁÓDŹ` / `łódź` | true | true |

`strings.ToLower("STRASSE")` is `strasse`; `strings.ToLower("straße")` is `straße`. Go's stdlib
implements **simple** case folding; `ß → ss` is **full** case folding, which neither `ToLower` nor
`EqualFold` performs. `strings.ToLower("ΣΟΦΟΣ")` is `σοφοσ`, not `σοφος`.

**Three consequences:**

1. **§7 test 53 is a test that cannot pass as written.** It is one of the revision's flagship new
   tests and its fixture requirement (*"MUST include non-ASCII pairs"*) is the right instinct.
2. **Holdout scenario 15 names "German, Polish, Greek or Turkish".** Under `strings.ToLower`, Polish
   works and **German, Greek and Turkish do not**. The scenario the spec added to catch a
   silent-ASCII-only fold would catch a silent simple-folding-only fold — after release.
3. **FR-011b's vocabulary has no word for what is actually being shipped.** It requires any surface
   delivering only ASCII folding to say so. This surface is neither ASCII-only nor full-Unicode; it
   is *simple Unicode folding*, and the document has no term for it and makes the stronger claim.

**Fix.** State the mechanism as **`strings.EqualFold`** for `=`/`<>` (it is simple case folding, and
it fixes Greek final sigma and Kelvin-sign-class pairs that `ToLower` gets wrong), state explicitly
that **full case folding — `ß`/`ss`, `ﬁ`/`fi` — is NOT performed and why**, and **delete `straße`
/ `STRASSE` from test 53** or move it to an explicit negative case asserting it does *not* match.
Note that `EqualFold` breaks Turkish `İ`/`i` where `ToLower` happens to work, so a per-operator
decision is required and must be written down. Add `İstanbul`/`istanbul`, `ΣΟΦΟΣ`/`σοφος` and
`straße`/`STRASSE` to DS-1 with their **verified** expectations, so the bound is in the dataset
rather than in a claim. `LIKE`'s fold is a third question — a pattern matcher cannot use
`EqualFold` directly — and is unaddressed.

### C-2 — Pass 1's C-1 is NOT closed. Two contradictory freshness mechanisms are both normative.

**Lens:** Inconsistency · **Sections:** §1 table (`:232`), FR-020c, constraint table (`:584`), AC-F5, §4.2 annotation, test 32

FR-020c (`:751-754`) spends nine bullets establishing that the manifest is **not** on the query
path — `Index` holds no manifest field, `LoadManifest` has two call sites both inside
`SyncWith`/`CheckDrift`, `Manifest` has no mutex — and concludes: *"The mechanism is therefore
respecified, and it **removes the manifest from the query path** rather than putting it there. **The
hash rides on the bleve document as a stored field.**"*

**Four other normative places still specify the disproven mechanism**, and one of them is the exact
sentence pass 1's C-1 named:

| Place | Text | Status |
|---|---|---|
| §1 reuse table (`:232`) | `knowledge.Manifest` — **"read on the query path"** … *"The new work is the lookup and the column, not the value"* | **verbatim unchanged from the commit pass 1 reviewed** |
| Constraint table (`:584`) | *"the properties row's `source_hash` versus **`ManifestEntry.Hash`**"* | contradicts FR-020c |
| **AC-F5** (`:1141`) | *"a record whose row `source_hash` differs from **`ManifestEntry.Hash`**"* | a mandatory AC that tests the wrong side |
| §4.2 annotation (`:1424`) | *"each row's `source_hash` is compared against **`ManifestEntry.Hash`**"* | the normative worked example |

An implementer following AC-F5 builds the mechanism FR-020c proves does not work, against a
`Manifest` with no mutex, on the query path, under concurrent `SyncWith`. That is a data race, and
test 62 (`-race`, added in this revision to catch exactly this) traces to FR-020c — so the race is
built by AC-F5 and hunted by test 62.

**This is A-13's own lesson, live in the document that states it.** A-13 says FR-020c has had four
mechanisms and *"the first three were each described in normative text as though they already
worked"*. The fourth is now described in one place and the third in four.

**Fix.** Rewrite §1's `knowledge.Manifest` row to **"read by the indexer, NOT on the query path"**
and delete *"The new work is the lookup and the column, not the value"*. Rewrite the constraint
table, AC-F5 and §4.2's annotation to compare the SQLite row's `source_hash` against **the bleve
document's stored `source_hash`**. Add `indexDoc.source_hash` to §1's *"what must be built"* list.

### C-3 — FR-064a mandates a SQLite-side aggregate, reopening join fan-out by name.

**Lens:** Inconsistency / Incorrectness · **Sections:** FR-064a, FR-021, FR-028a, §8.1, SC-002a, test 59

FR-064a (`:897`): *"An aggregate-only query materialises **one result row**: the `COUNT`/`SUM` is
**pushed entirely into SQLite** and no candidate is retrieved."*

FR-021 (`:787`): *"**What moves back to Go:** every operator in FR-022b's vocabulary, every
grouping, **every aggregate**, and the `many`-arity fan-out."*

§8.1 (`:1879`): *"FR-064a's aggregate-only exemption … is now a **Go** scan over a candidate stream
**rather than a SQLite pushdown**."*

FR-064a is the only requirement in the document that still commands SQL to compute a value, and it
commands it for **the one query shape with no row-level check on the answer** — no rows are
returned, so no reader can sanity-check the total. It is also the shape where the deleted defect
lives: FR-028a's own receipt is a `SUM` over a join with a `many` property returning **200 where
truth is 100**, and FR-064a's exempt path is the aggregate over the largest candidate populations
in the system (up to 50,000). SC-002a and test 51 assert the record-counted-once property; **test 59
asserts FR-064a's exempt path returns `COMPLETE: yes`** and asserts nothing about de-duplication.

**Fix.** Delete the pushdown sentence. Restate FR-064a's rationale in the terms §8.1 already uses:
the aggregate-only path is exempt from FR-064 because it **streams** candidates through the Go
aggregation pass and materialises one result row, not because it avoids retrieving them. Then state
the cost honestly — it retrieves up to 50,000 candidates — and hand it to A-14, which currently
believes the exemption is free. Add FR-028a's de-duplication assertion to test 59.

### C-4 — FR-064's candidate count is unimplementable: there is no compiled `WHERE` clause.

**Lens:** Infeasibility · **Sections:** FR-064, FR-066a, §7 test 47, A-14, §5 refusal table

FR-064 (`:895`): *"the candidate set is **the rows surviving the filter**, before paging, joins,
grouping and ranking … It is counted by a **`COUNT(*)` over the compiled `WHERE` clause**, taken
**before any row is materialised** (§7 test 47)."*

Under R-A the filter is never compiled to SQL. SQLite narrows by type, path prefix and kind only.
So "the rows surviving the filter" is a quantity **only the Go comparator can produce**, and the Go
comparator produces it by evaluating candidates — which is materialising them. The requirement, its
acceptance criterion (FR-066a: *"a HARD precondition, counted BEFORE retrieval"*), its test (47:
*"a 24,000-candidate query is refused without materialising one row"*) and its refusal string
(`:1123`: *"this query selects 24,180 records"*) are all written for a mechanism the ruling deleted.

**This is the load-bearing bound of the whole design.** §8.1's honest cost paragraph, A-14, FR-066a
and FR-066b all rest on *"FR-064's 10,000-candidate cap is a **refusal**, not a politeness limit,
and it was set for exactly this reason"*. If the cap can only be enforced *after* doing the work it
bounds, it does not bound anything — the Go path can be handed an arbitrary population and the only
protection is that it refuses after paying.

**Fix.** Decide and write down which of these the design takes, because they are materially
different products:
- **(a) A pre-filter narrowing count.** `COUNT(*)` over what SQLite *can* answer — type, path,
  kind — refusing above the cap. Honest, cheap, and enforceable before retrieval, but it refuses
  queries whose filter would have selected three records out of a large type. Say so.
- **(b) A streaming abort.** The comparator counts survivors as it evaluates and aborts at 10,000.
  Enforceable, but **not** "before any row is materialised" — FR-066a, test 47 and the refusal
  string must all be rewritten, and A-14's bound becomes "cost is capped at 10,000 *evaluations*",
  which is a different and weaker claim.

Whichever is chosen, restate FR-064's definition of "candidate" to match it and rewrite test 47.

### C-5 — Nothing asserts that SQLite performs no comparison. The instrumentation was deleted.

**Lens:** Incompleteness · **Sections:** §8.1's deletion table, AC-8.4, AC-8.4b, SC-024, §7 tests 39/42, §6

The revision's central claim is that *"a single surviving SQL-side comparison reopens every
violation"* — and it is right. The document then removes every mechanical check that could detect
one:

- **§7 test 39** (`TestFilter_NoLikeInCompiledPath`) — deleted; it inspected the emitted filter path.
- **AC-8.8** — deleted; *"a `BINARY`-collation assertion over emitted DDL"*.
- **AC-8.4a** — deleted; a mutation per defeat.

What replaces them: AC-8.4's *"no post-filter escape"* (a row-count identity that detects double
filtering, not SQL-side filtering) and AC-8.4b's six comparator mutations (which mutate Go and say
nothing about SQL). **Neither can fail if the whole filter is compiled to SQL and the comparator is
bypassed** — the row counts still balance and the comparator's mutations are unreached.

Seven surviving SQL-side evaluations are listed at the head of this review. Each got into a
revision *whose headline is this ruling*, and nothing in the document would catch an eighth.

**Fix.** Add a required test — the analogue of the deleted test 39, at the store boundary rather
than in the compiler:

> **`TestQuery_NoComparisonIsDelegatedToSQL`** — a query-boundary recorder captures every SQL
> statement the properties index executes for a corpus exercising all ten operators, `group_by`,
> `aggregate`, `sort` and `join`. It asserts that **no captured statement contains any comparison
> operator, `LIKE`, `IN`, `GROUP BY`, `ORDER BY`, an aggregate function, or `COLLATE`** outside a
> named allow-list of narrowing predicates (`type = ?`, `path LIKE ?` for prefix scope, `kind = ?`,
> the relation child table's `rec_id` join). Adding a statement to that allow-list is a
> specification change requiring the argument AC-8.2 demands.

This is the one control that makes R-A a property rather than an intention, and it is cheap.

### C-6 — R-7 still MANDATES the storage FR-021d withdrew, and cites a row that no longer contains it.

**Lens:** Inconsistency · **Sections:** §8 R-7 (`:1781`), FR-021d (`:793`), §8.1 deletion table (`:1823`), §6

The §8 rule table is the highest-risk artifact in the document and §6 promotes it to numbered
requirements. R-7 reads, in full and unstruck:

> *"**A `date` MUST be stored as a signed integer epoch with a declared precision, never as text**
> — see §8.1's R-7 row."*

FR-021d (`:793`) withdraws precisely this: *"the storage form is not load-bearing and the
requirement is withdrawn."* §8.1's deletion table (`:1823`) agrees: *"R-7's integer-epoch storage
(**FR-021d**) — **WITHDRAWN as a storage requirement**."* And §8.1's R-7 row, which R-7 points the
reader at for the storage specification, no longer contains one.

A MUST in the rule table, pointing at a row that contradicts it, in the one table the document says
*"the rules — not the cells — are what a human reviews"*.

**Fix.** Replace R-7's second sentence with the parsing rule: *"A `date` value MUST be parsed in Go
per FR-021d's strict ISO-8601 grammar; a value that does not parse is a reported problem (R-4), not
a comparison. Storage form is unconstrained."*

### C-7 — `<>` and R-2 specify opposite answers for an absent property.

**Lens:** Ambiguity / Inconsistency · **Sections:** §4.1.2 filter table (`:1094`), R-2 (`:1776`), FR-008, DS-4 row E, DS-5

| Artifact | Says |
|---|---|
| **R-2** (`:1776`) | *"A comparison where **either side is absent** is `false`, **for every operator except `IS NULL`**."* |
| **§4.1.2 filter table** (`:1094`) | *"`<>` not equal — **includes records where the property is absent**, unless excluded (FR-008)"* |
| **DS-4 row E** (`:1691`) | *"absent — `IS NULL` is true; **a `<>` filter includes it** (FR-008)"* |

`<>` is a leaf operator. Under R-2 it evaluates to `false` over an absent side, so the record does
**not** match. Under the filter table and DS-4 it does. Both are normative and
`TestComparisonTruthTable` generates its cells **from R-2**, while DS-4 is the dataset the same test
suite runs.

R-2's own explanation is about `{not: {...}}` — *"`NOT(false)` is `true`, so a negative filter
includes the absent records"* — which is correct and is what DS-5 tests. **`<>` is not `not`.** The
document conflates a negated *tree* with a not-equal *leaf*, and FR-008's phrase "a negative filter"
is ambiguous between them. This is the single most consequential cell in the truth table, and the
spec's own motivating question (*"which days did I not meditate?"*) can be phrased either way.

**Fix.** Rule on it explicitly and in one place. Either:
- **(a) `<>` is R-2-governed** — absent yields `false`, and the way to include absent records is
  `{any: [{p, "<>", v}, {p, "IS NULL"}]}` or `{not: {p, "=", v}}`. Then correct the filter table and
  DS-4 row E, and add a DS-4 row asserting `<>` **excludes** absent. This is SQL's semantics and is
  the more defensible answer given R-B.
- **(b) `<>` is exempt from R-2 like `IS NULL`** — then amend R-2's exception list to name both, say
  so in R-3, and explain why `<` `>` `<=` `>=` are not also exempt.

Whichever, add the chosen case to DS-5 as a compound too, since a tree walker and a leaf evaluator
can disagree.

### C-8 — Three normative places require the WITHDRAWN fold column, and one states the opposite outcome from FR-011a.

**Lens:** Inconsistency · **Sections:** edge-case table (`:477`), §3 non-behaviours (`:565`), constraint table (`:579`), FR-011a, R-10, SC-002d, DS-1

FR-011a (`:643`) withdraws the derived `<col>_fold` SQLite column in as many words: *"**Withdrawn**
— under ruling R-A there is nothing to compare it against."* §8.1's deletion table (`:1824`)
confirms: *"R-9/R-10's `instr()` and the **FR-011a** fold column — **WITHDRAWN**."*

Three places still depend on it, and the first **inverts the requirement**:

| Place | Text | Problem |
|---|---|---|
| **Edge-case table** (`:477`) | *Enum value differing only in **non-ASCII** case (`Ätä` vs `ätä`) — **does NOT resolve** … this depends on the Go-side **fold column** being built … a build without the fold column reports these as **non-conforming**"* | **States the opposite of FR-011a, R-10, SC-002d, test 53 and DS-1's `ÄKTIV` row**, all of which require non-ASCII case to resolve unconditionally |
| §3 non-behaviours (`:565`) | *"Unicode folding requires the Go-side **fold column** FR-011a specifies"* | FR-011a specifies a function, not a column |
| Constraint table (`:579`) | *"**ASCII-only where it rests on SQLite**; Unicode requires the Go-side **fold column** (FR-011a)"* | nothing rests on SQLite any more |

DS-1 (`:1661`) is unambiguous the other way: *"`ÄKTIV` — **accepted — resolves to `äktiv`**"*. An
implementer reading the edge-case table builds a conditional behaviour gated on a column that does
not exist; an implementer reading DS-1 builds unconditional Go folding. Both are following
normative text.

**Fix.** Rewrite the edge-case row to *"resolves to the declared value, unconditionally — the fold
is in the comparator (FR-011a), not in SQLite"*, and add the row that is actually load-bearing now:
*"Enum value differing by a **full**-case-folding pair (`ß`/`ss`) — **does NOT resolve**; see C-1."*
Delete "fold column" from `:565` and `:579`.

### C-9 — §10a's money inventory is incomplete. Two live wire-enum members survive it.

**Lens:** Incompleteness · **Sections:** §10a (`:2112-2131`), FR-014, Hard Constraint #8

§10a claims to be exhaustive so *"the deletion is a scheduled task with a reviewer rather than
something a future reader trips over"*, and asserts *"Verified against the tree at revision time —
every path, line count and symbol below was read, not recalled."* Grepped against the tree:

| Surviving money/currency surface | §10a lists it? |
|---|---|
| **`contracts/components/schemas/RecordProblem.yaml:38-39`** — the enum members **`cross_currency`** and **`money_scale_mismatch`**, plus their prose at `:67-69` | **NO** |
| `RecordAggregate.yaml:27-28`, `RecordSort.yaml:10`, `RecordGroup.yaml:64` | **NO** |
| `src/lib/api/generated/schemas.ts:319-320`, `:3875-3876` — the generated **runtime Zod** union carrying both members, plus a `RecordMoney` object schema and a `currency` regex (18 hits in the file) | **NO** — §10a's "Generated" row names only four lines of the **Go** file |
| `src/lib/api/generated/openapi-types.ts` (31 hits) | **NO** |
| `pkg/api/generated/openapi_types.gen.go` | listed as **four lines**; the file carries far more |

`cross_currency` and `money_scale_mismatch` are **not comments**. They are live wire-contract enum
members that generate a Go constant, a TypeScript union member and a **runtime Zod validator** that
the SPA edge uses to accept or drop payloads. They are the machine-readable residue of the requirement
FR-014 retired, and under Hard Constraint #8 they cannot be removed by hand — the spec change and
the regenerated artifacts must land in one atomic commit, which is a task nobody has been given.

**Fix.** Extend §10a with a `contracts/` and a `src/lib/api/generated/` row enumerating every hit,
name `RecordProblem.yaml`'s two enum members explicitly as the highest-value deletions, and add the
regeneration of **both** generated trees (Go and TS) to the W1 task with `make verify-contracts` as
its exit criterion. Also add the **`number` → `integer` + `decimal`** contract change, which §10a
does not mention at all: `contracts/` currently declares a `number` property-type enum member and
**no `integer` and no `decimal` anywhere**, so FR-004's change has no contract task scheduled.

### C-10 — FR-004a's test cannot pass, and its "verified clean" claim does not reproduce.

**Lens:** Incorrectness / Infeasibility · **Sections:** FR-004a (`:612-620`), §7 test 54, SC-002e, ADR D0 (`:157-160`)

FR-004a requires *"a test asserts that **no** CRM or other domain term appears in **any non-test
file** of the record packages, over a denylist including at least `company`, `deal`, `contact`,
`lead`, `opportunity`, `pipeline`, `prospect`, `stage`, `arr`, `crm`"*, and both documents state:
*"**Verified at revision time: the code is already clean** — zero domain vocabulary outside tests in
`pkg/records` and `pkg/knowledge`."*

Grepped whole-word, case-insensitively, over non-test `.go` files in those two packages:
**49 hits**, including —

- **`pkg/records/doc.go:12`** — the sentence *"we ship mechanism, the vault ships convention"*
  illustrated with `deal`/`company`. **The D0 statement itself trips FR-004a's denylist.**
- `pkg/knowledge/` — `stage` (19 hits, ordinary pipeline-stage naming), `lead` (a local variable),
  `contact`, `arr` as a whole word.

`stage`, `lead`, `arr`, `pipeline` and `contact` are ordinary English and ordinary programming
vocabulary. A denylist over them produces false positives structurally, not incidentally, and it
grows every time someone writes `stage` in a comment. The test as specified is both **failing on
day one** and **destined to be weakened into uselessness** by whoever has to make it green.

**Fix.** Three changes: (a) **withdraw the "verified clean" claim** from both documents — it does
not reproduce and it is the kind of unchecked assertion this document exists to remove; (b) narrow
the denylist to terms with **no plausible non-domain use** (`crm`, `opportunity`, `prospect`,
`deal`, `company`) and drop `stage`/`lead`/`arr`/`pipeline`/`contact` explicitly, with the reason
recorded so nobody re-adds them; (c) scope the test to what the requirement actually cares about —
**seeded data**: assert that `SeedConfig`, the default config, and the shipped schema/view
directories contain zero record types, zero enum values, zero property names and zero identifier
prefixes. That is FR-004a's real subject and it is decidable. Add `pkg/records/doc.go`'s prose to
the excluded-by-path list or rewrite it in neutral terms.

### C-11 — ADR-068 §4 retains revision 6's reversed position verbatim, marked "restated" and not restated.

**Lens:** Inconsistency · **Sections:** ADR-068 §4 Consequences (`:2610-2622`), D16.2b, D16.6, AC-16.6

The bullet carries a revision-7 note — *"**this is now a REASON, not a cost** … The bullet is kept,
**restated**, because the reasoning is what justifies the ruling"* — and the body was then left
unchanged. It still reads:

> *"Each is defeatable and D16.6 gives the defeat, but each must be **deliberately defeated in the
> query compiler** — the correct behaviour is never the default. This is the largest single
> correctness cost of D16 … **The truth table therefore runs against the real compiled query path
> (AC-8.4), not a Go comparator: after D16.2b the product does not use one for filtering, and a
> table that passes over an unused comparator proves nothing.**"*

Every clause is the reversed position. It names the reversed D16.2b as live authority, mandates the
deleted compiled-SQL truth-table target, and asserts the product does not use a Go comparator —
against D16.2b as reversed, D16.6 as rewritten, AC-16.6 as revised, and the spec's AC-8.4. It also
carries a **money orphan** the revision claims to have swept (*"`SUM` adds **USD to JPY** without
complaint"*) and lists lexical enum ordering as a violation (*"enums order lexically, so `stage >=
qualified` drops `proposal`"*) when D4 as revised makes lexical ordering **the specification**.

The ADR is the authority document. A reader who implements from §4 Consequences builds a SQL query
compiler.

**Fix.** Restate the bullet as the note promises: keep the receipts as reasons, delete *"must be
deliberately defeated in the query compiler"*, delete the whole final sentence and replace it with
*"The truth table therefore runs against the comparator the product uses (AC-16.6)."* Delete the
USD/JPY clause. Move the enum-ordering clause out of the violation list into D4's own record.

---

## 2. The six verification items from the brief — verdicts

| # | Item | Verdict |
|---|---|---|
| 1 | Comparisons in Go; SQLite only narrows | **NOT CLEAN — 7 survivors, 2 CRITICAL.** See the table at the head of this review, C-3, C-4, C-5, C-6, M-5, M-7 |
| 2 | SQL operator vocabulary, structured object, no parser | **MOSTLY CLEAN — one unimplementable requirement.** Vocabulary is consistent everywhere; R-9/R-10 restated correctly; O-3's amendment is coherent in both documents. **FR-022c cannot be satisfied without the parser O-3 forbids** — M-12 |
| 3 | Enum ordering lexical; ordinal bookkeeping gone | **MOSTLY CLEAN — D4's title IS fixed in ADR revision 7.** Four vestiges survive: M-17, M-18, M-19, M-20 |
| 4 | Money deleted entirely; type count stays seven | **ARITHMETIC CORRECT, INVENTORY INCOMPLETE.** −1 −1 +2 = seven verified. Two **live wire-enum members** survive §10a — C-9 |
| 5 | Case-insensitivity is a Go-only feature | **DIAGNOSIS CORRECT, MECHANISM WRONG.** SQLite's ASCII-only fold is verified and correctly stated. `strings.ToLower` cannot deliver what is claimed — **C-1**. FR-011's enum treatment is resolved (M-17 is a vestige, not a contradiction) |
| 6 | No hardcoded domain vocabulary | **NOT ACHIEVED.** The test cannot pass (C-10); four normative tables carry no marker and one declares CRM strings as contract (M-16); `person` is simultaneously illustration and shipped type (M-14); both documents' own counts are wrong by ~10× (M-15) |

---

## 3. MAJOR findings

### Item 1 — remaining SQL-side leftovers

**M-5 — R-8 still mandates a `BINARY` SQLite column and its only test was deleted.**
R-8 (`:1782`): the identifier side *"is matched **exactly, on a `BINARY` column**"*. Under R-A,
relation identity is compared by the Go comparator, where "exactly" is a byte comparison and no
collation exists. Meanwhile AC-8.8 — *"a `BINARY`-collation assertion over emitted DDL"* — was
**deleted** (`:2013`) as having SQL-side comparison as its only subject. So the rule survives and its
enforcement does not, and the mechanism it names belongs to the layer that no longer decides.
**Fix:** restate R-8 as *"the identifier side is compared byte-exactly by the comparator; no folding
is applied"*, and if a `BINARY` collation is still wanted on the narrowing column, say that
separately as a storage note with its own assertion.

**M-6 — `§8.1`'s R-8 row does not exist, and five places cite it as authority.**
§8.1's rewritten table (`:1845-1854`) has rows for R-1, R-2/R-3, R-5, R-7, R-9/R-10, R-11, R-12 and
join fan-out. **There is no R-8 row.** ADR-068 D16.6's table has none either. Cited as though it
does: the header table (`:50`, `:52`), §3's behavioural contract (`:499` — *"see §8.1's R-8 row for
why that one column is different"*), the constraint table (`:579`), and FR-011a (`:644`). A reader
sent to find the reasoning finds nothing. **Fix:** either add the R-8 row or move its content into
FR-011a and repoint all five references.

**M-7 — Sorting is never assigned to Go or to SQL, and four artifacts assume SQL.**
FR-021 (`:787`) enumerates *"what moves back to Go: every operator …, every grouping, every
aggregate, and the `many`-arity fan-out"* — **sorting is absent from the list**. Four places then
assume SQLite does it: FR-021b's *"at `ORDER BY` time"*, FR-066b's `GROUP BY` result set, SC-007's
SQLite `GROUP BY`/`ORDER BY` temp-b-tree budget, and R-5 / FR-010 / §4.1.2's *"SQLite's own
ordering"*. `sort` is a first-class `vault_find` parameter and ordering **is** comparison — it is
governed by R-1, R-4, R-5, R-7 and R-13. **Fix:** add sorting to FR-021's list explicitly, restate
R-5 and FR-010 as *"lexical byte order over the value string — the same order SQLite's `BINARY`
collation produces, computed in Go"*, delete the `ORDER BY` phrasing from FR-021b, and remove the
SQLite `GROUP BY`/`ORDER BY` temp-b-tree line from SC-007's budget or explain what still produces one.

**M-8 — Case-INSENSITIVE matching plus case-SENSITIVE ordering is unresolved.**
Matching folds case (FR-011a); ordering is *"lexical"* (R-5) with no case rule stated. Byte-lexical
order puts every capitalised value before every lowercase one — `Won` sorts before `lost` (`W`=0x57
< `l`=0x6C) — so a corpus that FR-011 deliberately allows to hold `Won`, `won` and `WON` as one
value renders them in three separate places in a sorted result, while `group_by` collapses them into
one group. Nothing in the document says which. **Fix:** state that ordering uses the **folded** form
as its key with the file's spelling rendered (mirroring FR-011's render-what-the-file-says rule), and
add a DS-1 row.

**M-9 — AC-8.1 still requires both provenances, a distinction the design deleted.**
AC-8.1 (`:1792`): *"every cell is generated in **both provenances** — literal-vs-literal and
column-vs-literal — because R-12's row shows the two disagree."* §8.1's deletion table (`:1826`):
*"R-12's literal-vs-column affinity asymmetry — **N/A** — there is one comparison path and one
provenance."* ADR D16.6 agrees: *"One comparator means one provenance."* AC-8.1 therefore **doubles
the size of the generated truth table** to exercise a distinction that cannot exist. This is the
brief's *"an AC written for a defeat that no longer exists"*. **Fix:** delete the both-provenances
clause; keep the seven-type axis and the `many`-arity axis, both of which are live.

**M-10 — R-12's rule text still claims it "has its own defeat".**
R-12 (`:1786`): *"**This rule is itself violated by SQLite and now has its own defeat (§8.1, R-12
row)**."* §8.1's R-12 row is a *reason*, not a defeat, and the deletion table marks R-12 N/A.
**Fix:** restate as *"SQLite violates this rule; it is unreachable because there is one comparison
path — §8.1, R-12 row."*

**M-11 — AC-8.4b's six mutations cover six of the twelve live rules, and SC-024 asserts they suffice.**
The six (`:1994-1997`) mutate R-2, R-4, R-11, R-10's fold, `LIKE`'s wildcards and the aggregate
de-duplication. **Nothing mutates R-1 (different declared types), R-5 (enum resolution and
ordering), R-7 (dates), R-8 (relation identity), R-9 (element-wise `many` matching), R-12 or R-13.**
A comparator that gets any of those seven wrong passes all six mutations, and SC-024 (`:1501`) states
the criterion as *"**each** of AC-8.4b's six named mutations makes it fail"* — six of twelve is the
same "insufficient defeats" shape pass 1 found, reincarnated as insufficient mutations. **Fix:** one
mutation per live rule, twelve in total, each naming the cell it must kill; renumber SC-024 and W2's
exit criterion to twelve.

### Item 2 — the operator vocabulary

The vocabulary change is done well. `=`/`<>`/`<`/`<=`/`>`/`>=`/`LIKE`/`IN`/`IS NULL`/`IS NOT NULL`
is used consistently in FR-022b, §4.1.2's filter table and refusal table, R-9, R-10, R-13, DS-4,
SC-002c, §7 test 52, §4.2's worked example and §10a's replacement note. The old vocabulary survives
only inside explicit *"previously"* clauses. O-3's amendment is recorded in ADR-068 (`:2670`) in the
terms the spec claims, and both halves of its resolution (structured JSON, no parser) are restated.
The `contains`-becomes-`LIKE` argument — one operator with two behaviours, caller chooses — is sound
and `LIKE`'s wildcard-free-means-exact semantics are correctly described. One requirement does not work:

**M-12 — FR-022c cannot be satisfied without the parser O-3 forbids, and its likely implementation commits the failure it prohibits.**
FR-022c (`:807`) requires that `JOIN`, **a subquery**, `COALESCE`, `CASE`, `BETWEEN`, `EXISTS`,
`GROUP_CONCAT` and *"a function call"* each be **refused naming the supported set** — *"never a
parse error, never a silent empty result, and never a partial evaluation"*. SC-002c and §7 test 52
assert it.

But the filter is a structured object of `{property, op, value}` with `op` drawn from a closed
ten-member enum. `BETWEEN` and `JOIN` arrive as an unknown `op` and are refusable by name — those two
work. **A subquery, `COALESCE`, `CASE` and a function call have nowhere to arrive.** A model reaching
for them puts them in `value` (`{"property":"arr","op":">","value":"(SELECT max(arr) FROM deals)"}`)
or in `property`. Then:

- if `property`, FR-024 refuses it as an unknown property — acceptable, though the message names the
  wrong problem;
- if `value`, the comparator treats it as a text literal, R-1 makes the comparison `false` against a
  numeric property, and the query returns **zero records** — **the silent empty result FR-022c
  explicitly forbids**, produced by the requirement's own default path.

Detecting SQL inside a value string requires recognising SQL, which is the parser ADR-068 O-3 forbids
and which FR-022 and FR-022b both reaffirm.

**Fix:** scope FR-022c to what the structure can express — **an `op` outside the ten-member enum, and
a parameter name the schema does not declare** — and say plainly that a SQL fragment smuggled inside
a `value` is treated as a text literal, which for a typed property makes the comparison `false` under
R-1 and **puts the record in `PROBLEMS` under R-4 with the value named**. That is a real, reachable,
non-silent answer and it needs no parser. Then delete "a subquery" and "a function call" from
FR-022c's list, from SC-002c and from test 52, or restate them as `op`-position cases.

**M-13 — `IS NULL` / `IS NOT NULL` and `IN` have no wire shape.**
The filter leaf is `{property, op, value}`. `IS NULL` and `IS NOT NULL` take no value; `IN` takes a
list. Nothing says whether `value` is omitted, `null`, or an empty string for the first pair, and
`null` is indistinguishable from "the caller sent JSON null" — which matters for a system whose
central distinction is absence. FR-090 lists `RecordQueryRequest` as a contract type but no schema is
sketched. **Fix:** state that `value` MUST be **absent** for `IS NULL`/`IS NOT NULL` and that a
present `value` is refused naming the operator; state that `IN` requires a non-empty array and that
an empty array is refused (it matches nothing and is the `LIKE ''` failure in another costume,
FR-022a's sibling case).

**M-14 — `LIKE`'s anchoring, escaping and fold interaction are unspecified.**
FR-022b says `%`/`_` are wildcards with `\` escaping and that matching is case-insensitive. It does
not say the match is **whole-value** (SQL's `LIKE` is anchored: `'vendors' LIKE 'vendor'` is
**false**), which is the single most likely implementation divergence given R-9's phrasing
*"`LIKE` matches an element **by pattern**"*. Nor does it say how the fold composes with the pattern —
folding the pattern folds the escape character's operand and can change what `_` matches for
multi-byte runes. **Fix:** state anchoring explicitly with the `'vendors' LIKE 'vendor'` → false
example in DS-4, and specify that folding is applied to literal segments of the pattern and to the
subject, never to the metacharacters.

### Item 3 — enum ordering

**ADR-068 D4's title IS corrected in revision 7** — it now reads *"Enums are closed. Ordering is
SQLite's, and a domain order is a value prefix."* The spec's claim that it "is corrected in ADR
revision 7" is accurate. The ordinal column, its `NULLS LAST` requirement, the rebuild obligation and
§4.1.6's refusal string are all deleted, and §10a schedules the two guard test files. Four vestiges:

**M-15 — FR-011 still says the resolved value "supplies the ordinal".**
FR-011 (`:641`): *"the value it resolves to supplies the **ordinal** (FR-010, R-5), so ordering is
unaffected by spelling."* FR-010, twelve lines above, deletes the ordinal. **Fix:** *"the value it
resolves to supplies the **sort key**"*.

**M-16 — the edge-case table still says `Won` "sorts by `won`'s ordinal".**
`:476`. Same fix.

**M-17 — §4.1.1's response still returns "enum values in declared order".**
`:1030`. §4.1.2's refusal table (`:1117`) explicitly records the opposite: *"revision 5: 'in order'
is dropped — R-E makes ordering lexical, so the list is simply the declared set"*. Two normative
descriptions of the same response field. **Fix:** *"enum values, as declared"* — and say that the set
is unordered, so a reader does not infer a sort order from the response's own sequence.

**M-18 — §7 test 2 `TestEnum_OrderedAndClosed` survives alongside test 56 with the deleted semantics in its name.**
Test 2 (`:1587`) traces FR-010 and FR-011; test 56 (`:1641`)
`TestEnum_OrdersLexicallyAndResolvesCaseInsensitively` traces FR-010, FR-011 and R-5. §6's
traceability row names **only** test 56. Test 2 is a duplicate whose name asserts the withdrawn
requirement, and it is the kind of test that gets written to its name. **Fix:** delete test 2, as
tests 36 and 39 were deleted, with the same annotation.

**M-19 — §10a's R-E row is the only disposition with no symbols.**
Every other row in §10a is line-precise. The R-E row (`:2150`) reads *"**Whatever** holds declared
position in `pkg/records/schema.go` and [two test files] is dead"*. The symbols exist and are
findable — the schema-side position fields, the enum-ordering comparison branch in
`compare_oracle.go`, and the enum-order sort helper — and the section's whole purpose is that the
deletion be a scheduled task rather than a discovery. **Fix:** enumerate them as the money row does.

### Item 4 — the money deletion

**The arithmetic is right and I checked it.** Seven were declared (`text`, `enum`, `relation`,
`date`, `number`, `money`, `person`); −`money` −`number` +`integer` +`decimal` = **seven**. Every
prose occurrence of "seven property types" is therefore correct and not stale. `R-6` is retired
rather than reused, with the reason stated, and every dependant is named: US-2 scenario 3, the
behavioural-contract bullet, §4.1.2's refusal string, §4.2's total, the `CurrenciesPresent` field
and `TestMoney_RefusesCrossCurrencySum`. All are gone from the live text; the surviving mentions are
inside explicit *"previously"* or *"retired"* clauses, which is the correct treatment. `decimal.go`
survives with `maxDecimalScale = 100` and the *"must not inherit 12"* warning is exactly right.
The residue is in the inventory, not the prose:

**M-20 — §10a misidentifies which decimal test files reference `maxMoneyScale`, and misses one file entirely.**
§10a (`:2139`): *"**Three of those files** reference `maxMoneyScale` in comments or fixtures and need
those references removed."* Of the five decimal test files it lists, **one** references
`maxMoneyScale`. The three files in the package that do are `decimal_string_bounds_test.go`,
`money_test.go` (already scheduled for deletion) and **`schema_declared_keys_test.go`** — which is
**not in §10a at all**, in neither the delete list nor the edit list. Following §10a's instruction
leaves a reference to a deleted constant in a file nobody was told about. **Fix:** name the three
files by path and add `schema_declared_keys_test.go` as an edit.

**M-21 — `RecordAggregateResult.yaml:64` is a wrong citation, in a revision whose header counts four wrong citations it corrected.**
The currencies field is at `:61`. Minor in isolation; material because §10a's opening sentence is
*"Verified against the tree at revision time — every path, line count and symbol below was read, not
recalled"*, and because a deletion list is executed literally.

**M-22 — the `number` → `integer` + `decimal` contract change has no scheduled task.**
`contracts/` currently declares a `number` property-type enum member and **no `integer` and no
`decimal` anywhere**. §10a schedules only the money deletions. FR-004's change touches the same three
schema files as the money deletion and requires the same atomic regeneration under Hard Constraint
#8. **Fix:** add it to §10a with the enum members named, and to W1's exit criterion.

**M-23 — `pkg/records` is not imported by any production code, and the document never says so.**
The package is standalone: `money.go`, `decimal.go`, `schema.go`, `value.go`, `compare_oracle.go`
and `filter.go` are reachable only from their own tests. This is **good news for §10a** — the
deletions are near-zero-risk — and it is also the single most useful piece of context for whoever
executes them, because it means the compiler will not find the callers for them. **Fix:** state it in
§10a's opening. It also strengthens the ruling: `compare_oracle.go` already evaluates comparisons in
Go and emits no SQL, so R-A restores what exists rather than building something new — the spec's
*"the comparator that decides is the one that already exists, is already tested"* (`:1835`) is
**verified true**, and saying so with the evidence would make the argument stronger than it currently
reads.

### Item 6 — no hardcoded domain vocabulary

Beyond C-10 (the test cannot pass; the "verified clean" claim does not reproduce):

**M-24 — `person` is simultaneously an R-F illustration and a shipped property type.**
R-F's box (`:597`) names *"`company`, `deal`, `meeting`, **`person`**, `status`, `stage`, `arr`,
`open`, `won`, `lost`, `prospect`, `Acme`, `Northwind`, `CO-0142`, `DEAL-0117`"* and says **"NONE of
them is anything the product ships"**. FR-004, thirty lines later: *"The system MUST support exactly
these seven property types: `text`, `enum`, `relation`, `date`, `integer`, `decimal`, **`person`**."*
ADR-068 D0 (`:148`) carries the same list with the same error. `meeting` has the mirror problem: R-F
calls it an illustration, §4.1.6's refusal table calls the same string contract. An implementer
following R-F removes the `person` type. **Fix:** remove `person` from the R-F list in both documents
and add a sentence distinguishing the two axes — *"property TYPE names (`text`…`person`) are ours and
are shipped; record type names, property names and enum values are the vault's and are not"*.

**M-25 — four normative tables carry no R-F marker and one declares CRM strings as contract.**
§4.1.2's refusal table carries the marker (`:1110-1112`) and does it well — *"what a test asserts is
the SHAPE and the remedy clause, against a fixture schema the test itself declares"*. **§4.1.1,
§4.1.3, §4.1.4, §4.1.5 and §4.1.6 do not**, and §4.1.6's (`:1323`) says the opposite in the same
words the marked one negates: *"**These strings are contract, not illustration; a test asserts
them**"* — over a table containing `company`, `deal`, `meeting` and `person`. §4.1.1's integrity
example block, §4.1.4's schema-violation refusal, DS-1 and DS-2 are likewise unmarked. **Fix:**
propagate §4.1.2's marker verbatim to all five, and rewrite §4.1.6's sentence to §4.1.2's form.

**M-26 — both documents state occurrence counts that are wrong by roughly an order of magnitude.**
R-F (`:600`): *"They appear **roughly thirty times** below and **fourteen times** in ADR-068."*
ADR-068 D0 (`:141`): *"this ADR uses CRM vocabulary **fourteen times** and the implementing
specification **thirty-three times**."* Counted whole-word, case-insensitively, over the vocabulary
both documents name: **288 in the specification and 119 in the ADR** — `company` 36, `deal` 43,
`deals` 17, `Acme` 51, `won` 25, `arr` 23 in the spec alone. The rule's own justification is *"a rule
that is stated once and then quietly undermined by every example is a rule that will be broken by
someone acting in good faith"*, and the figure that quantifies the undermining is understated
tenfold. **Fix:** state the real counts, or state none. And treat the real number as an argument for
**changing the examples**, not only for marking them: at 288 occurrences a boxed note at the front is
doing very little work. A neutral example vocabulary — `widget`/`gizmo`, or an abstract
`type_a`/`prop_1` — would discharge R-F structurally instead of by disclaimer, and the operator's
framing ("an empty database, all capabilities but nothing predefined") argues for exactly that.

**M-27 — FR-004a's fixture carve-out is stated but never located.**
FR-004a: *"Fixtures are excluded from FR-004a's denylist **by path**, and the exclusion is narrow and
named rather than a general 'except where inconvenient'."* No path is named. The exclusion is the
part that decides whether the test is meaningful or theatre, and *"narrow and named"* is precisely
the claim that needs the name. **Fix:** name the paths (e.g. `*_test.go` and `testdata/`), and state
what is **not** excluded — non-test `.go`, seeded config, shipped schema and view directories.

---

## 4. Vacuous acceptance criteria — re-audit against the new design

Pass 1 named nine. **Seven are genuinely closed**, and several of the repairs are better than the
finding asked for: AC-F3 now requires the plan to name every property and the index answering it,
a mutation chosen to change the plan, and zero candidate retrievals at the store boundary
(m-11 closed properly). AC-8.6 now fails when the two rankings do not differ, which is the correct
inversion. AC-P3 is rewritten from *"a pure function is deterministic"* into a real
reach-around-the-contract detector. AC-P1 is promoted into a shared helper every `vault_find` test
calls. Test 21 is reclassified as a benchmark with a W1 write-back obligation. SC-016 and SC-018 are
rewritten behaviourally. **This is the strongest part of the revision.**

Two survive, and the deletions created three new ones:

| ID | Criterion | Why it cannot fail, or fails against nothing |
|---|---|---|
| **V-1** | **AC-8.1's both-provenances axis** | Generates cells for a distinction the design deleted (M-9). Not vacuous — *worse*: it doubles the table and every added cell asserts the same value, so the table's size grows while its discriminating power does not |
| **V-2** | **AC-8.4b / SC-024's six mutations** | Six mutations for twelve live rules; a comparator wrong on R-1, R-5, R-7, R-8, R-9, R-12 or R-13 passes every one (M-11). SC-024 states the six as sufficient |
| **V-3** | **AC-F5** | Asserts divergence against `ManifestEntry.Hash`, which FR-020c's respecified mechanism does not consult. It passes only if the implementation ignores FR-020c, and fails if it obeys it (C-2) |
| **V-4** | **AC-F8 / §7 test 59** | Asserts an aggregate-only query over 24,000 records returns a `sum` with `COMPLETE: yes`. It asserts **nothing about the value**, over the one query shape whose answer no row can corroborate, on the one path still specified as a SQLite pushdown (C-3). A join-fan-out-inflated `sum` passes it |
| **V-5** | **AC-8.4's "no post-filter escape"** | The row-count identity (candidates = rendered + problems + comparator rejections) detects **double filtering**. It is stated as the control that makes the ruling checkable, and it is satisfied identically whether the single filter ran in Go or in SQL (C-5) |

**AC-D6 and AC-F6 deserve a note the other way.** They are build-tagged, and §7 test 42 does the
thing this project's own `false-green-patterns.md` demands: it **names the CI job
(`go-test-nosqlite`), the tag combination, the make target, and requires it on the required-checks
list**, and explicitly rules out `make build-all`'s `linux/mipsle` target as discharging it. That is
the correct treatment of a build-tagged test and it should be the template for the missing test in
C-5.

---

## 5. Open risks — A-13 and A-14

### A-13 (freshness, fourth mechanism) — **specified this time, and that is a real improvement.**

Unlike its three predecessors, the fourth mechanism is *mechanism*: a `source_hash` field on
`indexDoc` with `Store: true`, its mapping entry, stored-field retrieval on the search path, and the
SQLite column — each named as **new work** with its cost (64 bytes of hex per document, one
stored-field fetch per returned hit) and its unknowns stated rather than assumed. The
manifest-based alternative is recorded and explicitly **not taken**, with the reason. The write
ordering is re-derived with a five-row detectability table and the "err toward false positives"
direction argued. The reason string is constrained to what the comparison establishes
(*"the two indexes disagree"*, not *"the properties index is stale"*). The re-queue is bounded and
debounced. **This is what the other three should have looked like.**

Three things stop it being closed:

**M-28 — the mechanism is a W1 requirement whose viability is a W2 exit criterion.**
§11's wave table puts `FR-020..FR-020c1` in **W1** and *"A-13's answer: whether the stored-field
freshness mechanism holds"* in **W2**. So W1 ships a freshness comparison whose two open questions
are answered a wave later, and if the answer is no, W1's completeness verdict was built on a
mechanism being replaced. ADR-068's W2 row says *"A-13 is answered — the stored-field freshness
mechanism holds, **or it does not and is replaced before W3**"*, which concedes the same thing.
**Fix:** either move FR-020c to W2 with the fielded-indexing work that reopens `indexDoc` — which is
where the ADR says the answer comes from — or state W1's fallback explicitly (report every record
`complete: false` until W2 lands, which is honest and non-silent).

**C-2 above** — four normative places still specify the third mechanism, so the fourth is not the
only one in the document.

**M-29 — FR-020c1's residual is well named but its documentation obligation has no home.**
FR-020c1 requires *"The documentation MUST state that a completeness verdict covers what the query
returned, not what it did not"*. No FR assigns that to a surface — not the tool description
(FR-079's budget is ~150 tokens and already spoken for), not `vault_describe`, not the response
header. AC-F4 states a **different** unstated-exception carve-out (scope) in the same spirit and puts
it nowhere either. **Fix:** put both exceptions in one place — the `COMPLETE:` line's own
documentation and `vault_find`'s tool description — and name that place in FR-020c1 and AC-F4.

### A-14 (Go evaluation path, unmeasured) — **the cost is stated but NOT bounded.**

A-14 is honest about what is unknown and correct that FR-064/FR-066a were set for this reason. But
its bound does not hold as specified:

- **The cap cannot be enforced where A-14 needs it** (C-4). If the candidate count can only be taken
  after Go evaluation, the 10,000 cap bounds the *refusal*, not the *work*.
- **FR-064a raises the bound to 50,000 for one query shape** and A-14 records this as free, because
  FR-064a claims a SQLite pushdown (C-3). Corrected, the Go path's worst case is **50,000 candidate
  evaluations**, not 10,000 — five times the number A-14 asks W1 to measure. §11's W1 row does say
  *"at the 10,000-record cap **and at FR-064a's 50,000**"*, so the wave plan is right and A-14's own
  reasoning is not.
- **Nothing bounds comparisons per candidate.** The cap counts *records*. The filter is an
  arbitrarily nested tree (FR-023b requires arbitrary nesting explicitly), so the work is candidates
  × leaves × (a `strings.ToLower` per operand, per FR-011a). A 40-leaf filter over 50,000 candidates
  is 4M folds, and nothing in the document caps filter tree size, depth or leaf count.

**M-30 — no bound on filter tree size, depth or leaf count.** Every other input is bounded — page
size, hops, group levels (2), candidates, sweep size, findings per category, response bytes. The
filter, the one input whose cost multiplies against the candidate cap, is unbounded. **Fix:** state a
maximum leaf count and maximum depth, refuse above them in FR-024's pattern, and add both to the
constraint table so A-14's measurement has a defined worst case to measure.

**M-31 — the fold is specified per comparison rather than per candidate.**
FR-011a: *"one `strings.ToLower` per operand per comparison"*. The record side of a comparison is
the same value on every evaluation, so folding it once at candidate load (or storing a folded form
in the row) is the obvious implementation and changes the cost by the leaf count. The document
neither permits nor forbids it. **Fix:** say that the record side MAY be folded once per candidate
and that the folded form is never rendered (FR-046's sibling rule) — otherwise an implementer either
pays the cost the spec names or silently reintroduces the derived column FR-011a withdrew.

---

## 6. Traceability after the mass deletion

The document forbids itself silent renumbering (*"Nothing below was renumbered silently"*) and
restored SC-008 on exactly that ground. Measured against its own standard:

**M-32 — `FR-200..FR-212` are cited as requirements in two places and defined nowhere.**
§6 (`:1574`): *"the rules are **PROMOTED to numbered requirements as FR-200..FR-212** (M-24), one per
rule, so that the highest-risk item in the document appears in this matrix rather than being traced
to 'see §8'."* §11's W2 row lists *"R-1..R-13 as FR-200..FR-212"* as wave content. **No FR-2xx is
defined anywhere in the document.** The promotion is announced and not performed, so §6 still traces
the highest-risk item to a table by prose reference, which is the defect the promotion was written to
close. And the mapping is ambiguous: `FR-200..FR-212` is thirteen numbers for **thirteen declared
rules of which one (R-6) is retired**, and nothing says whether FR-205 is R-6-retired or whether the
numbering closes the gap. **Fix:** either define the thirteen FRs in §8's table (one column, `FR-200
= R-1` … `FR-212 = R-13`, with FR-205 marked retired alongside R-6), or withdraw the promotion and
trace §6's row to `TestComparisonTruthTable` and the rule IDs directly. The first is better and costs
one table column.

**M-33 — §8 states the rule count wrongly while correcting a count.**
`:1780`: *"**The rule count is therefore twelve declared rules of which one is retired**, not
thirteen."* Thirteen rules are declared (R-1..R-13); one is retired; **twelve are live**. As written
it implies eleven live. **Fix:** *"thirteen declared rules of which one is retired — twelve live."*

**M-34 — twenty-six tests named in §6 do not appear in §7's test plan.**
§6 names 86 distinct test identifiers; §7 schedules 67. Twenty-six of §6's are absent from §7,
including several the revision itself added to close pass-1 findings:
`TestScope_TruncatedResolutionReportsIncomplete` (FR-062a — the P0 scope guarantee revision 5
describes as having had *"no test, no AC and no SC"*), `TestValue_NonConformingIsFlaggedNotAbsent`
(*"FR-021a is the whole of the R-4 defeat and had no test at all"*),
`TestTools_ConfigureWritesOnlyVaultControlPlane` (FR-070e's mechanical rule — the rule adopted
*because* the semantic criteria mis-decided three operations),
`TestStorage_LinkedSQLiteVersionIsAsserted` (FR-020i),
`TestObservability_CountersAndDegradedRebuildHeader` (FR-049b),
`TestRender_TextIsAProjectionOfTheWireObject` (AC-P3, the rewritten one),
`TestReadPath_RateLimited` (FR-067a), `TestIndex_PropsRoundTripsExactDecimal`,
`TestQuery_AllValidCorpusReportsComplete`, `TestFind_ThirdHopRefused`,
`TestSchema_UndeclaredTypeIsAnOrdinaryNote`. §7 is the ordered implementation list (*"Order is unit →
integration → e2e; within a level, dependencies first"*), so a test in §6 and not §7 has a
requirement and no schedule. **Fix:** add the twenty-six to §7, or state explicitly that §6 is the
authority and §7 is a subset — but then §7 is not a test plan.

**M-35 — §6 and §7 name different tests for the same requirement, with opposite semantics.**
§6's FR-079/FR-128 row names **`TestTools_DescriptionTokenBudget`**. §7 test 38 is
**`TestTools_DescriptionBudgetIsReviewedNotEnforced`**, and its entire point is that the ~150-token
budget *"becomes a review checklist item at W5"* because FR-127b says it is *"never enforced at
runtime"* and *"a test MUST NOT conflate"* the two units. A test implemented to §6's name enforces
the budget at runtime — the exact defect test 38 was rewritten to remove. **Fix:** §6 takes test 38's
name.

**M-36 — `FR-039a` is referenced and never defined.**
FR-020c (`:767`): *"`ManifestEntry.Hash` is deliberately empty for attachments … because **FR-039a**
forbids opening one and hashing is opening"*. There is no FR-039a. FR-039 is the duplicate-identifier
rule. **Fix:** cite the real source of the don't-open-attachments rule, or define FR-039a.

**M-37 — FR-080b has no traceability row.**
`FR-080, 080a, 081, 082` are covered; **FR-080b** — workers `deny` on all six, with the
reads-are-`allow`-is-about-prompting-not-granting reasoning — appears in no §6 row and no §7 test.
It is a security posture with a stated cost, and it is the only seeded-policy requirement with no
assertion behind it. **Fix:** add it to the FR-080 row and to test 17.

**M-38 — §8.1a was repurposed and every reference to it now points at the wrong section.**
§8.1a used to hold the executed receipts; in revision 5 it holds *"Rulings R-B, R-C, R-E"* and the
receipts moved to **§8.1b**. Ten references were not repointed, and two of them name subsections that
have never existed in the new §8.1a:

| Reference | Points at | Actually in |
|---|---|---|
| `:19` *"a tenth SQLite violation (join fan-out, §8.1a)"* | rulings | §8.1b |
| `:53` *"the receipt is restated at its true value in §8.1a"* | rulings | §8.1b |
| `:536` *"saturates silently at int64 max … verified, §8.1a"* | rulings | §8.1b |
| `:565` *"fourteen non-ASCII pairs in §8.1a"* | rulings | §8.1 |
| **`:657` FR-012** *"see **§8.1a, R-11a**"* | **no `R-11a` exists anywhere in the document** | §8.1b |
| **`:792` FR-021c** *"both verified, **§8.1a §E**"* | **§8.1a has no lettered subsections** | §8.1b |
| **`:1785` R-11** *"enumerated with their defeats in §8.1's R-11 row and **§8.1a §D**"* | **same** | §8.1b |
| `:1781` R-7 *"SQLite violates it four ways (§8.1a)"* | rulings | §8.1b |
| `:1796` AC-8.1 *"the fan-out case of §8.1a"* | rulings | §8.1b |

R-11's reference also still says *"enumerated with their **defeats**"* — the defeats are withdrawn.
**Fix:** repoint all ten to §8.1b (or §8.1 for the Unicode receipt), delete `R-11a`, delete the
`§D`/`§E` subsection references, and delete "defeats" from R-11's sentence.

**M-39 — §4.2's `TOTALS` is still page-scoped, which is the defect FR-125a was written to remove.**
The header reads *"3 of 17 selected records could not be evaluated; **12 of 14 shown**"* — 14
evaluated. The total reads **`sum(arr) = 673,000.00 over 12 of 12 rows`**. FR-125a: *"An aggregate
MUST be computed over the **full evaluated set**, never over the rendered page, and the scope line
MUST say which set it covers … the scope line names the **evaluated** count and the header names the
rendered one, **and a test asserts they are computed from different numbers**." The example's two
numbers are the same number, and §7 test 25 *"diffs against §4.2's literal example"*. The annotation
table (`:1427`) even describes the original defect in the correct terms and then leaves it in place.
**Fix:** `sum(arr) = <total> over 14 of 14 evaluated rows (12 shown)`.

**M-40 — §4.2's total is arithmetically impossible.**
The seven displayed rows are 180,000 + 120,000 + 95,000 + 88,000 + 70,000 + 62,000 + 58,000 =
**673,000**, and the example then says *"… 5 more rows"* under a filter of `arr >= 50000`. The five
hidden rows must contribute **at least 250,000**, so a 12-row total cannot be 673,000. §4.2 is
labelled *"a literal worked example, normative"* with *"A test diffs against this shape"*, and it is
the artifact the whole of §4.2's annotation table certifies line by line. **Fix:** recompute against
the fixture the test will actually use, and — since M-39 requires the total to cover 14 evaluated
rows, two of which are not shown — publish the fixture's per-row values so the number is checkable.

**M-41 — §4.1.6's normative refusal table has no acceptance criterion or test asserting its strings.**
It says *"These strings are contract, not illustration; a test asserts them"*. §7 test 37 asserts
tier placement and AC-C1's conversion count; no test in §7 asserts §4.1.6's ten refusal strings.
§4.1.2's equivalent table is covered by AC-F1 (*"every refusal above names the remedy in the same
string"*). **Fix:** give §4.1.6 an AC-F1 sibling (AC-C9) and add it to test 37.

**M-42 — FR-070e's mechanical rule has a second, unstated exception: `trash`.**
FR-070e (`:712-725`): *"**Writing into `.omnipus-vault/` is `vault_configure`**; writing a note is
not … **It has exactly one exception**, and it is the one already stated: `.seq` (FR-036a)."*
§4.1.5 (`:1251`): *"`vault_configure` writes **only** `.omnipus-vault/`."* But FR-048 puts trashed
notes at **`<vault>/.omnipus-vault/trash/<timestamp>/<path>`**, and `trash` is a `vault_restructure`
operation; FR-048a's `restore` reads out of the same directory and writes back into the vault. So
`vault_restructure` writes into `.omnipus-vault/` on every trash, and the rule adopted *because the
semantic criteria mis-decided three operations* mis-states its own exception count on the fourth.
The rule is still the right rule — trash genuinely belongs where it is — but *"exactly one
exception"* is false and `TestTools_ConfigureWritesOnlyVaultControlPlane` will meet it.
**Fix:** state the exception properly: the rule governs the **control plane**
(`.omnipus-vault/records/`, `.omnipus-vault/views/`), with `.seq` (written by `vault_edit`) and
`.omnipus-vault/trash/` (written by `vault_restructure`) as the two named non-control-plane paths
inside the directory. Then the mechanical test is expressible.
