# Vault records — specification

- **Implements:** [ADR-068](../architecture/ADR-068-vault-records-typed-record-layer.md) **revision 8** (2026-08-28)
- **Builds on:** [ADR-067](../architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md) — `pkg/knowledge` is **reused, never duplicated**
- **Date:** 2026-08-25; **revised 2026-08-28** to ADR-068 revision 8
- **Branch:** `feat/library-improvements`
- **Status:** Draft revision 6. Revision 2 followed round-2 review (BLOCK: 8 critical, 21 major); revision 3 realigned to ADR-068 revision 5; revision 4 realigned to ADR-068 revision 6 — a sixth tool (`vault_configure`), two named blast-radius criteria, a specified freshness token, a platform posture, a W0 wave, and byte budgets. Revision 5 followed grill pass 1 (BLOCK: 19 critical, 41 major, 17 minor) and three operator rulings that delete work. **Revision 6 follows grill pass 2 (BLOCK: 11 critical, 42 major, 14 minor — `vault-records-spec-2026-08-25-review-round6.md`) and one further operator ruling: Unicode case folding is REQUIRED.**


**What revision 6 changes, in one paragraph.** Revision 5's rulings were right; **the deletions they
required were not finished, and two new mechanisms did not work as claimed.** Revision 6 finishes
both. **(1) The case-folding mechanism is replaced.** Revision 5 asserted that `strings.ToLower`
gives *"full-Unicode case insensitivity for free"*. It does not — Go's standard library performs
**simple** folding, and its two candidate functions fail in **opposite** directions, so `straße`
never matched `STRASSE` under either. The mechanism is now **`golang.org/x/text/cases.Fold()`**,
full Unicode folding, with six literal language pairs pinned as **AC-8.9** — including the Turkish
`İstanbul`/`istanbul` pair asserted as **NOT equal**, with the reason recorded so it is not "fixed"
later (FR-011a). **(2) Seven surviving SQL-side evaluations are removed and the ruling is now
MECHANICALLY ENFORCED**, which it was not: revision 5 deleted both artifacts that could detect one
(test 39 and AC-8.8) while depending on the property entirely. **AC-8.10 / §7 test 39a** inspects
every emitted statement against a named narrowing allow-list, and it is W1's first deliverable.
**(3) FR-064's bound is respecified**, because as written it was unimplementable — it counted a
compiled `WHERE` clause ruling R-A deletes — and it becomes **two** bounds, B1 (50,000 narrowed
candidates, genuinely pre-retrieval) and B2 (10,000 survivors, a streaming abort), with A-14's claim
**correspondingly weakened and said so**. **(4) FR-064a's SQLite pushdown is deleted**; the
aggregate path is a Go stream that retrieves up to 50,000 candidates, and the cost is stated rather
than recorded as free. **(5) Pass 1's C-1 is closed properly** — four normative places, including a
mandatory acceptance criterion, still compared against `ManifestEntry.Hash`, the mechanism FR-020c
spends nine bullets disproving. **(6) FR-004a's test is respecified**: revision 5's grep-based test
**could never pass**, because the first thing it red-lights is `pkg/records/doc.go:12` — the D0
statement itself — and the "verified clean" claim behind it is **withdrawn**, while the underlying
claim (no domain vocabulary in declared enums, types, seeded schemas or default config) is
**confirmed by reading all 44 hits**. Beyond those: `<>`'s absent-side semantics are ruled (they were
specified in two directions), sorting is assigned to Go (it was assigned to nobody and then assumed
of SQLite four times), the filter tree gains the bound it never had, twenty-five unscheduled tests
are scheduled, §4.2's worked total is corrected (it was **arithmetically impossible**), the money
deletion inventory gains the wire-contract and SPA surfaces it omitted, and all seven of the review's
"unasked questions" are answered rather than left open.

**Where review round 6 is WRONG or incomplete, said here rather than silently ignored.**

| Round-6 claim | Verdict |
|---|---|
| **C-1's proposed FIX** — *"state the mechanism as `strings.EqualFold` … and DELETE `straße`/`STRASSE` from test 53"* | **The DIAGNOSIS is upheld and was verified by execution; the REMEDY is REJECTED with evidence.** `EqualFold` is also **simple** folding: `EqualFold("straße","STRASSE")` is **false**. Adopting it would have kept the flagship test case unsatisfiable and deleted the very row that discriminates. **`cases.Fold()` returns true**, and the German pair **stays** in test 53 as the cell that catches a regression to any stdlib call. Executed, both ways, before writing this |
| **C-8's proposed new edge-case row** — *"add: enum value differing by a FULL-case-folding pair (`ß`/`ss`) — does NOT resolve"* | **REJECTED, and it follows from the above.** Under `cases.Fold()` it **DOES** resolve. The row is added with the **opposite** expectation and with the reason, so the correct behaviour is pinned rather than the workaround |
| **M-36** — *"`FR-039a` is referenced and never defined"* | **HALF UPHELD.** It is undefined **in this document**, and the bare citation was unresolvable from here — that is a real defect and it is fixed. But the citation is **not dangling**: FR-039a is `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md:1918`, and `pkg/knowledge/manifest.go:62-64` cites it the same bare way. The defect is two documents sharing an `FR-nnn` namespace; the fix is to qualify it, not to invent a local FR-039a |
| **M-26's counts** — *"288 in the specification and 119 in the ADR"* | **The POINT is upheld emphatically; the FIGURES do not reproduce exactly.** Over R-F's own word list this document counts **313** and ADR-068 **135**; over the unambiguous subset, **243** and **90**. The command is published in R-F so a third reviewer does not derive a fourth number. Either way revision 5's *"roughly thirty"* was wrong by an order of magnitude, which is the finding |
| **M-34** — *"twenty-six tests named in §6 do not appear in §7"* | **UPHELD at TWENTY-FIVE.** The twenty-sixth, `TestFilter_CaseFoldIsUnicodeNotASCII`, is test 53 under its old name; it is repointed rather than scheduled |
| **M-20** — *"the three files [referencing `maxMoneyScale`] are `decimal_string_bounds_test.go`, `money_test.go` and `schema_declared_keys_test.go`"* | **UPHELD AND EXTENDED.** Those three are right and `schema_declared_keys_test.go` was genuinely missing from §10a. The review **also missed three**: `pkg/records/schema.go:678`, `pkg/records/value.go:324,467,471-472` (an executable bound check, not a comment), and `compare_oracle.go`'s `crossCurrencyProblem` |
| **C-9's contract inventory** | **UPHELD AND EXTENDED**, and one trap is added: a case-insensitive grep for `currency` matches **con·currency**, so seven hits in `PerformanceSettings*.yaml` and `openapi.yaml` are agent-concurrency settings and are **explicitly out of scope** — named in §10a so the next person does not delete them |
| The brief's *"promoting `x/text` to direct adds nothing to the binary"* | **The CONCLUSION stands — Hard Constraint #1 is satisfied, no new module and no CGo — and the CLAIM is refined by measurement.** The `cases` subpackage is not currently linked and costs **≈274 KiB** (280,352 bytes, measured). Stated in FR-011a because "adds nothing" would not survive a measurement |

**What revision 5 changes, in one paragraph.** Three operator rulings land first because they remove
material rather than add it. **(1) There is no `money` type.** The requirement is *"a precise decimal
datatype and also an integer 64 datatype"*, not a currency-carrying money value — so `money` is
deleted from the type system, `R-6` is **retired** with its defeats, acceptance criteria and tests,
and every cross-currency behaviour goes with it. **(2) `number` is replaced by two types the author
chooses between — `integer` (int64) and `decimal` (exact, arbitrary precision)** — so the index
column type is decided by the schema and never inferred from the first value seen. **(3)
Case-insensitive matching is a FEATURE**: SQLite's ASCII-folding `LIKE` and `COLLATE NOCASE` stop
being defects and become the implementation, `R-10` is rewritten around them, and the prohibition on
`LIKE` (and its acceptance criterion AC-8.7 and test 39) is deleted. Beyond the rulings, revision 5
closes the grill's criticals: a **tenth** SQLite violation (join fan-out, §8.1b) that made every
count and total over a filtered multi-value list wrong; a defeat for **R-7** (dates), which had
none; **De Morgan normalisation before SQL emission**, without which the R-2 defeat only covers
leaves; a per-defeat mutation obligation (AC-8.4a); a re-derived freshness ordering, with the part
that is still not designed carried as a **stated open risk** rather than a fourth assumed mechanism;
and the ten normative contradictions, nine vacuous acceptance criteria and four wrong citations the
review names.

**Nine operator rulings landed mid-revision and several DELETE work this revision had already
done. They are recorded here in full, because a reader meeting a withdrawn requirement needs to
find the ruling that withdrew it.**

| # | Ruling | What it deleted |
|---|---|---|
| **R-A** | **Comparisons are decided in OUR OWN CODE. SQLite only narrows candidates.** SQLite answers *"which notes are of this type?"* and hands back candidates; our own tested comparator applies R-1..R-13 to them. **SQLite decides nothing.** | **The largest deletion in the revision.** All eleven SQLite defeats §8.1 had grown to; FR-023a (De Morgan before emission); FR-028a's `EXISTS` semi-join; FR-011a's fold **column**; FR-021d's epoch storage; AC-8.4a, AC-8.7, AC-8.8. FR-021 **reverts** to Go evaluation. D16.2b is reversed. **The join-fan-out finding, R-7's missing defeat, the insufficient defeats and AC-8.4's escape hatch all become N/A rather than fixed** |
| **R-B** | **SQL operator names and semantics, not our invented ones** — `=`, `<>`, `<`, `<=`, `>`, `>=`, `LIKE`, `IN`, `IS NULL`, `IS NOT NULL` | `eq`/`lt`/`lte`/`gt`/`gte`/`contains`/`is_absent` (`pkg/records/filter.go:83-93`). R-9, R-10 and R-13 are restated; R-13 narrows to the ordering operators alone |
| **R-C** | **Unsupported SQL is REFUSED, naming what IS supported** | — (adds FR-022c) |
| **R-D** | **Case-insensitive matching is a FEATURE** | The `LIKE` prohibition, AC-8.7, test 39, and R-10's case-sensitivity. FR-011's exact-case enum treatment is **resolved against it**: enum equality is case-insensitive too |
| **R-E** | **Enum ordering follows SQLite's — lexical. Custom order is a value prefix** | R-5's ordinal column, its `NULLS LAST` requirement, its rebuild obligation, and §4.1.6's "must declare its values in order" refusal. **ADR-068 D4's title is now wrong in its second clause** and is corrected in ADR revision 7 |
| **R-F** | **No hardcoded domain vocabulary, anywhere** — *"an empty database, all capabilities but nothing predefined"* | Nothing in code (verified clean); it makes every illustrative name in this document **unmistakably an illustration**, and makes the rule testable (FR-004a) |
| **R-G** | **Bad values reported in the answer AND in a vault health view**, and the two must agree | — (adds FR-025a) |
| **R-H** | **Strict ISO dates; reject the rest.** `2026-9-1` and `03/04/2026` are bad values with the fix named; ambiguous formats are never guessed | FR-021d's storage requirement, replaced by a parsing requirement |
| **(money)** | **No money type** — a precise decimal and an int64 | Recorded in the paragraph above; §10a lists the code and contracts it makes dead |

**Where the review is wrong, and it is said here rather than silently ignored.**

| Review claim | Verdict |
|---|---|
| *"Property type count changes from seven"* (the ruling's framing) | **Arithmetically false, and the spec says so rather than restating a wrong number.** Seven were declared: `text`, `enum`, `relation`, `date`, `number`, `money`, `person`. Deleting `money` and splitting `number` into `integer` and `decimal` is −2 +2. The count stays **seven**; the **membership** changed. Restating "six" or "eight" anywhere would be a new defect. |
| M-2 — *"FR-110 still lists revision 5's seven doc comments"* | **Upheld**, and corrected to thirteen (FR-110, FR-110a, FR-110b, SC-016). |
| C-9 — *"§4.1.2 refuses a cross-currency total while §4.2 renders one"* | **Dissolved, not adjudicated.** Under ruling (1) there is no currency, so neither artifact has a subject. Both are rewritten; the contradiction cannot recur because the concept is gone. |
| C-7 — *"`COLLATE NOCASE` defeats R-10"* | **Inverted by ruling (3).** `NOCASE` is now the specified implementation for text and enum columns. The half of C-7 that survives is real and is kept: `NOCASE` on a **relation-identifier** column collides two distinct ids, so identifier columns stay `BINARY` (§8.1, R-8 row). |
| M-37 — *"the receipts were taken on `sqlite3` 3.51.0; the shipped engine reports **3.53.3**"* | **The concern is upheld; the number is REJECTED with evidence.** `modernc.org/sqlite v1.46.1` (`go.mod:64`) was opened through the real Go driver and asked `select sqlite_version()`. It answers **`3.51.2`**, not 3.53.3 — a **patch** ahead of the 3.51.0 CLI the receipts were taken on, not two minor versions. The review's figure does not reproduce. The **standard** behind the finding is right and is adopted anyway: §8.1 now names both engines and both versions in place, and FR-020i adds a test asserting the linked version, because affinity and collation behaviour *is* version-sensitive and a silent engine bump must not silently move the semantics. |
| C-7's second half — *"a `NOCASE` relation-id column makes `rec_ABC` and `rec_abc` the same key"* | **Upheld and load-bearing**, and it is why ruling (3) is applied per column rather than per database. See §8.1's R-8 row. |
| C-3 I-1 — *"`SELECT id FROM t WHERE NOT (a=1 OR b=2)` returns **ZERO rows**"* | **REJECTED — the cell does not reproduce.** Over the review's own fixture it returns **one row** (`id 2`), not zero: `NOT(NULL OR TRUE)` is `FALSE` and `NOT(NULL OR NULL)` is `NULL`, so rows 3, 4 and 5 drop while row 2 survives. The **finding is still correct and still CRITICAL** — the correct De Morgan answer is **four** rows (2, 3, 4, 5), so three rows are silently dropped — but the receipt is restated at its true value in §8.1b, because a wrong number in a document about wrong numbers is not a small thing. |
| M-38 — *"`repairAndValidateToolPolicyCoverage` is at `pkg/gateway/gateway.go:968`… It also emits **two** WARNs, not one"* | **Citation upheld, WARN count REJECTED.** The path correction is right (§0 correction 3 is fixed). The count is not two and is not fixed: it is **`1 + N`**, where the 1 is `gateway.go:975` and `N` is one per repaired agent from `pkg/config/validate.go:576`, and it is **0** when nothing needed repair. §0 now states the shape rather than a number. |

**What revision 3 changed, in one paragraph.** The agent surface is `vault_*` tools split by
**blast radius**, not nine `record_*` tools and not the nine `knowledge_*` tools shipping today
(§4.1). Storage is resolved: bleve keeps text, a derived SQLite properties index holds typed
properties and relations, and the two indexes must agree or the answer is not complete
(FR-020..FR-020g). Retrieval is specified for the first time — BM25 rather than the TF-IDF the
code actually uses, fielded indexing, RRF fusion, retry-only expansion, one tokenizer
(FR-110..FR-117). The response the model reads is specified as mechanism rather than
presentation, with a literal worked example (§4.2). Schema and view authoring become ordinary
writes (FR-016..FR-019a).

**What revision 4 changes, in one paragraph.** The surface is **six** tools, not five: schema and
saved-view authoring leave `vault_edit` for a control plane of its own, **`vault_configure`**
(ADR-068 D15.6), so that *"edit the notes, but do not redefine what a note is"* becomes an
expressible policy — it was not, and revision 3 said it was. The split criterion is now **two
named criteria**, C-A (cascade in bytes) and C-B (cascade in meaning), because one criterion was
being read two ways; ADR-068 D23.3's claim that it *"generalises with no special-casing"* is
**withdrawn**. Creating a **new** record type moves from tier 4 to tier 5 under C-B: a new schema
file retroactively converts every pre-existing note already declaring that type into a validated
record, which is a cascade the agent never named. The freshness token FR-020c required is now
**specified** against `ManifestEntry.Hash` *(superseded — see FR-020c and revision 6's C-2 correction)* rather than assumed to exist, per note rather than per
index, with a residual hole stated. Records are **unavailable and say so by name** wherever
SQLite cannot build. Response budgets move from **tokens to bytes**, because a token cap cannot be
enforced without naming a tokenizer. A **W0 wave** ships the bleve corruption fix ahead of, and
independently of, everything else. `trash`'s W4/W5 split is resolved by the ADR rather than read
into it.

**Four places where this spec was RIGHT and ADR-068 revision 5 was wrong, kept and now vindicated
by revision 6.** They are listed because the temptation on a sync is to assume the ADR is the
authority in every disagreement, and here it was not. (a) The unsatisfiable token budget versus
page-size cap — FR-127a; revision 6 changes the unit to bytes and D22.7 now says so. (b) The
unnamed token unit — FR-127b and A-8; revision 6 concedes the point in the same words. (c) The
stale FR-021 premise: this spec had already moved FR-021 to the properties index while ADR-068
D21.5 was still arguing from Go-side evaluation; revision 6 re-derives D21.5 on the corrected
basis and **cites this spec's note as the thing it got right**. (d) The `trash` W4/W5 ambiguity —
A-9; revision 6's D20 adopts exactly the reading A-9 proposed.

**FRs whose meaning changed, and what cited them.** Nothing below was renumbered silently; each
carries the same note in place.

| FR | Was | Is | Cited by |
|---|---|---|---|
| FR-020 | BLOCKED on the D16 spike | typed properties in a SQLite properties index | §6, §7 regression |
| FR-020a | the bleve-mapping no-op guard | derived and disposable: rebuild yields identical results *(the guard survives as **FR-020d**)* | §6 |
| FR-021 | filtering evaluated **in Go** over the candidate set | evaluated **in the properties index** over typed columns | ADR-068 D21.5, the storage spike C-3, §6 |
| FR-070 | nine `record_*` tools | five `vault_*` tools *(superseded by revision 4: **six**)* | §6, §7 test 18, SC-011 |
| FR-072 | compact text for `record_schema` | compact text for **every** result of all five *(superseded by revision 4: **all six**)* | §6, §7 |
| FR-073 | a `record_explain` **tool** | an `explain` **flag** on `vault_find` | §6, §7 |
| FR-100 | an agent tool `record_view_import` | an **operator/CLI** one-shot | §6, §7 test 20, SC-010 |
| SC-005 | "1,000 distinct identifiers **and zero sequence gaps**" | 1,000 distinct identifiers; **gaps permitted, repeats fail** | contradicted FR-038 and ADR-068 D7.1 — a defect, not a wording change |

**FRs whose meaning changed in revision 4 (ADR-068 revision 6).** Same rule: nothing renumbered
silently, each carries the note in place.

| FR | Was | Is | Cited by |
|---|---|---|---|
| FR-016 | new record type is an op of `vault_edit` — *"changes no existing note's meaning"* | an op of **`vault_configure`**; the premise was **false** under ADR-068 D1 — a new schema file converts every pre-existing note declaring that type | ADR-068 D15.6, D23.3; §4.1.6, US-12, §7 test 37 |
| FR-017 | change/delete existing type is an op of `vault_restructure` | an op of **`vault_configure`** | ADR-068 D15.6; §4.1.5, §4.1.6 |
| FR-018 | saved view is an op of `vault_edit` | an op of **`vault_configure`** | ADR-068 D15.6 |
| FR-020c | the two indexes *"carry the same freshness token"* — a token that **did not exist** | a **per-note** `source_hash` compared against `ManifestEntry.Hash` *(superseded by revision 5's FR-020c and corrected throughout in revision 6: the comparison side is the **bleve document's stored `source_hash`**, not the manifest — see C-2)*, with a named residual hole | ADR-068 D16.5; §7 test 32, SC-015 |
| FR-070 | **five** tools | **six** tools — `vault_configure` added | §6, §7 test 18, SC-011 |
| FR-078 | catalog `102 → 98` | catalog **`98 → 95`**; 102 was a miscount, corrected in ADR-068 D15.0 and re-counted here | §3, SC-011 |
| FR-127 | budgets in **tokens** (~50–80/hit, 1,000 default, 4,000 cap) | budgets in **BYTES** (~200–320/hit, ~4,000 default, 16,000 cap) | ADR-068 D22.7; §3, §4.2, A-8 |
| FR-128 | five descriptions ≈ 750 tokens | **six** ≈ 900 tokens | ADR-068 D22.8 |

FR-015 keeps its meaning and **gains a citation**: ADR-068 D23.3 rests on it to place an
existing-record-type change in a cascading tier. *(Revision 4: that tier is now `vault_configure`,
not `vault_restructure` — the citation stands, the destination moved.)*

**FR-021 does NOT change in revision 4, and the reason is worth stating.** Revision 3 moved it to
the properties index ahead of the ADR; ADR-068 revision 6 has now caught up (its D16.2c) and
**re-derives D21.5's tokenizer hazard on this spec's own note** — rank fusion stays in Go, so the
hazard survives, narrowed to the ranking pass. FR-021 and FR-116 already say exactly that and are
left byte-identical.

---

## 0. What already exists, and is reused

Verified against the tree at time of writing. This specification adds **no second
implementation** of any of these.

| Surface | Lines | Reused for |
|---|---|---|
| `pkg/knowledge/scope.go` | 390 | workspace scoping (`Scope.WorkspaceID`, `.Roots`, `.Contains`, `.Truncated`) — FR-060 |
| `pkg/knowledge/author.go` | 1,183 | scalar splice only — FR-040. `SetProperty(key, value string)` (`author.go:766`) takes strings and rejects line breaks; **there is no list writer, no body-replace and no delete**, so FR-040a, FR-047 and FR-048 add them |
| `pkg/knowledge/index.go` | 1,277 | bleve keeps **text** and gains real **fields** — FR-111. `indexDoc` is a closed 5-field struct (`index.go:583-589`) and the mapping is closed (`Dynamic=false`), so per-property *fields* are impossible; per-property **terms** in a `[]string` field are not, and that is how record candidates are selected (ADR-068 D16.1 S-1) |
| `modernc.org/sqlite v1.46.1` (`go.mod:64`) | — | the **derived properties index** — FR-020. Already a direct dependency, linked into the binary today for WhatsApp and Matrix session storage |
| `pkg/coreagent/core.go:357-483` | **98** names | the static builtin catalog the six `vault_*` names join and the nine `knowledge_*` names leave — FR-070a, FR-078. *(Revision 3 said 102. Re-counted over the composite literal at `core.go:358-482`: **98 quoted identifiers, 98 unique, 9 of them `knowledge_*`**. ADR-068 D15.0 corrects the same miscount.)* |
| `pkg/knowledge/search.go` | 603 | result caps (`SearchDefaultTopN`=20, `SearchMaxTopN`=100) as the precedent for FR-063 |
| `pkg/knowledge/tools.go` | 1,135 | tool registration shape and rate limiter |
| `contracts/components/schemas/Knowledge*.yaml` | 13 files | the contract-first pattern FR-090 follows |

**Three corrections restated so this spec cannot re-introduce them. The first is itself
corrected in revision 3:**

1. ~~There is **no SQLite**.~~ **Superseded by ADR-068 D16.2.** ADR-067 rejected SQLite for
   **search** (its A2), and for search that was right — scorch is better at text. For **records**
   the gain is aggregation, which scorch cannot do at all, so the premise does not transfer. A
   derived, disposable SQLite properties index is now part of the design, and it **widens the
   CLAUDE.md house rule that "SQLite is isolated to WhatsApp session storage only"** — deliberately,
   recorded in ADR-068 D16.4, not discovered in a diff. What remains true and load-bearing: **notes
   are the sole source of truth**, and nothing may live in SQLite that cannot be rebuilt from
   Markdown (FR-020a).
2. Tool names contain **no dots** — `vault_find`, not `vault.find`. A §7 invariant test asserts
   zero dots across builtin tool names. *(The audit **event** names `vault.edit` and
   `vault.restructure` carry a dot deliberately and are not tool names — FR-071.)* **Revision 5
   removes a wrong rationale that was attached to this correction.** An earlier draft said dots
   *"are already stripped at the provider boundary — `SanitizeToolName` replaces `.` and `:` with
   `_`"*. Verified: `SanitizeToolName` (`pkg/tools/registry.go:569`) is
   `strings.ReplaceAll(name, ".", "_")` — **it replaces dots only. Colons are not replaced**,
   despite its own doc comment at `:567-568` claiming both. The requirement is unaffected; the
   citation was repeating a false comment, which is the failure this document is about.
3. **CORRECTED, revision 5 — the citation was wrong and the conclusion was too strong.**
   `repairAndValidateToolPolicyCoverage` is defined at **`pkg/gateway/gateway.go:968`**, not in
   `pkg/config/validate.go` (which names it only in a doc comment at `:489` and `:494`, so a reader
   auditing the claim there finds prose and no call). It has two production call sites —
   `gateway.go:2061` (boot) and `gateway.go:3483` (hot reload). What it does, precisely:
   - It calls `config.RepairIncompleteToolPolicyCoverage` (`pkg/config/validate.go:525`) **first**,
     which backfills every gap to `deny` (`validate.go:565`) — **so a forgotten tool ships silently
     denied, with the feature dead and a log line as the only signal. That half of the original
     correction stands and is the half that matters to FR-080/FR-081.**
   - It then hard-validates the remainder, and **boot DOES abort** on any residual gap the repair
     could not close (`gateway.go:2065-2068`); a reload rejects the new config and stays degraded
     (`gateway.go:3486-3492`). *"Boot does not abort" was too strong: **backfill-then-continue is
     the normal path; abort is the residual backstop.***
   - **The WARN count is not a fixed number**, and revision 4's "one WARN" was wrong in the same way
     the grill's proposed "two" is: it is **`1 + N`** — one at `gateway.go:975`, plus one per
     repaired agent at `pkg/config/validate.go:576` — and **`0`** when nothing needed repair.

   Seeding is therefore protected by being written down and tested (FR-080, FR-081), not by a boot
   abort that the repair pass has already made unreachable for the gaps this specification creates.

**The nine `knowledge_*` names retire into the six. This mapping is the migration:**

| Retired | Replaced by |
|---|---|
| `knowledge_search` | `vault_find` (`words`) |
| `knowledge_graph` | `vault_find` (`near` + `hops`) |
| `knowledge_tasks` | `vault_find` (`kind: task`) |
| `knowledge_create` | `vault_edit` (`create`) |
| `knowledge_link` | `vault_edit` (`link`) |
| `knowledge_set_property` | `vault_edit` (`set_property`, now scalar **and list**) |
| `knowledge_append_section` | `vault_edit` (`append_section`) |
| `knowledge_move` | `vault_restructure` (`move`) |
| `knowledge_rename` | `vault_restructure` (`rename`) |
| — *(no equivalent today)* | `vault_read`, `vault_describe`, `vault_configure` |

Two of those rows are the point of the exercise. `knowledge_move` and `knowledge_rename` leave the
tier their siblings stay in, which is what makes FR-083 expressible. And the last row is the gap:
**there is no tool that reads a note today**, so an agent must leave the audited `knowledge_*`
boundary and drop to `read_file` to read a note it just found (FR-074).

**`check_integrity` inherits more than revision 3 said, and it inherits it UNBOUNDED.**
`knowledge_graph`'s five operations are `links`, `backlinks`, `unresolved`, `orphans`,
`neighborhood` (`pkg/knowledge/tools.go:643-647`). Only `neighborhood` is clamped —
`MaxNeighborhoodHops = 3` / `MaxNeighborhoodNodes = 500` (`pkg/knowledge/graph.go:36-38`,
applied at `graph.go:307-315`, read at `tools.go:812-826`). The **whole-vault** sweeps are
`resp.Links = toGraphLinks(g.Unresolved())` and `resp.Nodes = g.Orphans()`
(`tools.go:809-811`) — **no clamp at all**. Those sweeps cover **ordinary wikilinks across the
whole vault**, and most notes in a vault are not records, so a broken-link report has no home in
the record-typed half of `check_integrity`. FR-075 and FR-075a therefore widen it and bound it.

---

## 1. Existing codebase context

GitNexus was not consulted for this spec; the surfaces above were read directly. Recorded as a
gap: an impact analysis on `pkg/knowledge/index.go` before W2 would be prudent, since record
fields are added to a mapping that search already depends on.

| Symbol | Role | Note |
|---|---|---|
| `knowledge.Index` | **extended** | gains real fields (FR-111), an index-format version bump (FR-020d G1), a persisted-mapping assertion (G2) and an explicit `ScoringModel` (FR-110). Blast radius: every existing index is rebuilt once, which FR-020e requires anyway for the corrupt-segment fix |
| *(new)* properties index | **added** | derived, disposable SQLite (FR-020, FR-020a), carrying a per-row `source_hash` (FR-020c). Its **write path is unmeasured** — the spike measured neither the two-index write path, nor concurrent queries, nor any non-macOS platform (its §6.1). W1 measures it before anything is built on it. **It does not exist on every target**: `modernc.org/sqlite` cannot build on `linux/mipsle`, `netbsd/*` or `freebsd/arm` (`pkg/gateway/channel_matrix.go:20-28`), of which only `linux/mipsle` ships (`Makefile:210`, `:234`) — FR-020h |
| `knowledge.Scope` | **called** | unchanged; all **six** `vault_*` tools resolve through it (`Scope.WorkspaceID`, `.Roots`, `.Contains`, `.Truncated`) |
| `knowledge.author` splice | **called and extended** | scalar path reused unchanged; a list-valued splice (FR-040a), a body-replace (FR-047) and a trash convention (FR-048) are new work |
| `knowledge.Manifest` | **read by the INDEXER, NOT on the query path** | **CORRECTED, revision 6 (review round 6, C-2) — this row survived VERBATIM from the commit grill pass 1 reviewed, which is why pass 1's C-1 was still open two revisions later.** `ManifestEntry.Hash` (`pkg/knowledge/manifest.go:64`) is the hex SHA-256 of a note's contents, keyed by collection-relative path in `Manifest.Entries` (`manifest.go:82`), readable by `Manifest.Get` (`manifest.go:175`). **It is the value the INDEXER writes into both indexes. It is NOT read at query time and MUST NOT be** — `Index` holds no manifest field, `LoadManifest` has two call sites and both are inside `SyncWith`/`CheckDrift`, and `Manifest` has **no mutex**, so a query-path read is a data race against a concurrent indexer (FR-020c's nine bullets establish all three by citation). *(Revision 5's row said "read on the query path" and "the new work is the lookup and the column, not the value". Both were wrong, and an implementer obeying them built the race that §7 test 62 was added to hunt.)* |
| *(new)* `indexDoc.source_hash` | **added — this is the new work** | A **stored field on the bleve document** (`Store: true`), plus its mapping entry and stored-field retrieval on the search path. **The query-time comparison is SQLite row `source_hash` versus the BLEVE DOCUMENT's stored `source_hash`** — two derived indexes compared against each other, neither of them the manifest. Cost: 64 bytes of hex per document, one stored-field fetch per returned hit. Carried by **A-13** |
| `checkRetrievalRate` | **does NOT cover writes** | consulted at three sites only — `pkg/knowledge/tools.go:610`, `:749` and `pkg/knowledge/authoring_tools.go:1330` — all reads. `AuthoringDeps.RateLimiter` (`authoring_tools.go:136`) is documented as bounding `knowledge_tasks`, which is a read (`:133`). No write `Execute` consults it, so FR-067's write-path limiter is **new work**, not inheritance |
| `AgentLoop.resolveToolPolicyAtExec` | **constrains the design** | `pkg/agent/loop.go:12418` takes a tool **name** and no arguments, which is why the write surface splits by blast radius rather than by noun (FR-070b, FR-070c) |
| `config.RepairIncompleteToolPolicyCoverage` | **not called, but must be defeated** | FR-081 asserts zero *repaired* pairs, not zero gaps after repair |
| `KnowledgePanel.tsx` / `useKnowledgeIndexStore` | **extended** | today the panel renders index progress from live WS frames only (`KnowledgePanel.tsx:226`); FR-020f adds the snapshot a late-joining client needs |

---

## 2. User stories

### US-1 — Declare what a record is (P0)

An operator or an agent declares a record type once, in a file, and every record of that type
is then validated against it. Without this, nothing else in the spec can fail loudly, because
there is no statement of what "correct" means.

**Why P0:** every other story depends on a schema existing.

**Independent test:** write a schema, write a conforming record and a non-conforming one, and
confirm validation accepts one and names the fault in the other.

1. **Given** no schema exists, **When** a note declares `type: company`, **Then** the note is
   an ordinary note and no error is raised.
2. **Given** a `company` schema requiring `name`, **When** a record omits `name`, **Then**
   validation reports that record, the missing property, and that it is required.
3. **Given** a schema declaring `status` as an enum, **When** a record holds a value outside
   the declared set, **Then** validation reports the record, the offending value, and the
   permitted values.
4. **Given** a schema declaring `segment` as scalar, **When** a record holds a list, **Then**
   validation reports an arity violation naming the expected shape.
5. **Given** a schema file with no `schema_version`, **When** it is loaded, **Then** it is
   rejected and no records of that type are validated against it.

### US-2 — Ask a question and get a trustworthy answer (P0)

An agent filters and groups records with `vault_find` and receives, in one response, both the
records and an account of anything the query could not include. It cannot receive a total without also
receiving the caveats attached to it.

**Why P0:** this is the requirement the whole ADR exists to serve.

**Independent test:** query a corpus containing deliberately malformed records; confirm the
response names them rather than silently omitting them.

1. **Given** a corpus where every record is valid, **When** a filter runs, **Then** matching
   records are returned and completeness is reported as true.
2. **Given** a corpus where some records hold a non-numeric value in a numeric property,
   **When** an aggregate runs over that property, **Then** the response reports incompleteness,
   names the offending records, and states the reason.
3. **REPLACED, revision 5** *(was the cross-currency refusal; `money` is deleted — FR-014)*.
   **Given** a `decimal` property holding a value with more than 100 decimal places, **When** it is
   written, **Then** it is refused naming the bound and the value's own scale, and it is **not**
   rounded to fit (FR-013).
4. **Given** a filter naming a property that does not exist in the schema, **When** it runs,
   **Then** the query is rejected with the valid property names listed — it does not return
   zero records.
5. **Given** a candidate set exceeding the materialisation cap, **When** the query runs,
   **Then** it is refused with a narrowing instruction, and no partial answer is returned.
6. **Given** a query grouping by a multi-value property, **When** a record holds two values,
   **Then** that record appears under both groups.

### US-3 — Follow a relation without maintaining it (P0)

An agent asks a question that crosses from one record type to another, and the reverse
direction exists without anyone having written it down.

**Why P0:** this is the capability whose absence costs five manual steps in the incumbent.

**Independent test:** create two record types and a relation, and query the inverse without
ever writing an inverse property.

1. **Given** a deal related to a company, **When** the company's related deals are requested,
   **Then** they are returned, and no inverse property exists in any file.
2. **Given** a relation pointing at a note that does not exist, **When** validation runs,
   **Then** the dangling relation is reported with the offending record and target.
3. **Given** a relation pointing at a note that exists but is not the declared target type,
   **When** validation runs, **Then** it is reported as a type violation, not silently accepted.
4. **Given** a record referenced by a relation is renamed, **When** the inverse is queried
   again, **Then** the same records are returned.
5. **Given** a query requesting three relation hops, **When** it runs, **Then** it is refused
   with the hop limit stated.

### US-4 — Correct a record without damaging the file (P0)

An agent writes a property. Everything it was not asked to change is left exactly as it was.

**Why P0:** the vault is simultaneously a human's working notes. A writer that reformats
degrades it invisibly.

**Independent test:** write one property into a file rich in comments and unusual formatting;
diff the result.

1. **Given** a file with comments, blank lines and mixed quoting, **When** one property is
   written, **Then** the file is byte-identical outside the patched span.
2. **Given** a write whose value violates the schema, **When** it is attempted, **Then** it is
   rejected, the expected shape is named, and the file is unmodified.
3. **Given** a stale version token, **When** a write is attempted, **Then** it is refused and
   the refusal is audited.
4. **Given** a successful write, **When** it completes, **Then** an audit entry records agent,
   workspace, record and outcome.

### US-5 — Records stay findable when things move (P1)

Identity survives renames, and two records can never share an identity.

**Why P1:** important but only observable once relations exist.

1. **Given** a new record, **When** it is created, **Then** it receives an identifier unique
   within its type.
2. **Given** two processes creating records concurrently, **When** both complete, **Then** no
   two records share an identifier.
3. **Given** two records that somehow share an identifier, **When** validation runs, **Then**
   both paths are reported and neither is silently preferred.
4. **Given** the sequence file is deleted, **When** the vault is opened, **Then** allocation
   resumes above the highest existing identifier.

### US-6 — Interaction history nobody maintains (P1)

Contact history is derived from what is already written, not from a field somebody has to
remember to update.

1. **Given** a dated note mentioning a record, **When** history is requested, **Then** that
   date appears.
2. **Given** a mention inside an **unchecked** to-do, **When** history is requested, **Then**
   it does **not** count as contact.
3. **Given** a mention inside a **completed** task, **When** history is requested, **Then** it
   does count.
4. **Given** a mention inside an embed, a quote, or a code block, **When** history is
   requested, **Then** it does not count.

### US-7 — Bring existing base files across (P2) — operator/CLI, not an agent tool

A one-shot importer translates what it recognises and names what it does not. **An operator runs
it and reads the report**; it is not an agent tool (FR-100, FR-103).

**Why P2:** valuable, but nothing else depends on it. And mount-in-place already covers the
common case with no import code at all — `.obsidian/` is detected and never written
(`pkg/knowledge/detect.go:100`), so pointing Omnipus at an existing Obsidian vault works today.

1. **Given** a `.base` file using only supported filters, **When** it is imported, **Then** an
   equivalent native view is produced.
2. **Given** a `.base` file containing an expression the importer does not support, **When** it
   is imported, **Then** the view is produced without that clause and the untranslated
   expression is reported verbatim.
3. **Given** a `.base` file referencing a property absent from any schema, **When** it is
   imported, **Then** the import is reported as incomplete and names the property.

### US-8 — Only the records I am entitled to (P0)

**Why P0:** ADR-067 was blocked on precisely this for a weaker primitive.

1. **Given** a vault mounted only into workspace B, **When** an agent in workspace A queries,
   **Then** zero records are returned.
2. **Given** the same, **When** the agent inspects the response, **Then** it cannot distinguish
   the scoping from the vault being empty.

### US-9 — Read a note through the front door (P0)

An agent that has found a note can read it, and can obtain the version token a write requires,
without leaving the audited tool boundary and without sending a write it expects to fail.

**Why P0:** it is the gap that forces every other tool's guarantees to be abandoned mid-task. An
agent that drops to `read_file` to read a note is outside the scope check, outside the audit
trail, and outside the typed view of frontmatter.

**Independent test:** find a note, read it, edit it — with zero failed writes in between.

1. **Given** a note found by `vault_find`, **When** it is read, **Then** the response carries a
   version token an immediately following `vault_edit` accepts.
2. **Given** a note with a heading, **When** one section is requested, **Then** only that section
   is returned and the token still covers the whole file.
3. **Given** a note whose frontmatter violates its schema, **When** it is read, **Then** it reads
   successfully and the violation is named per property.

### US-10 — A search that ranks properly and admits what it cannot find (P0)

**Why P0:** a wrong answer costs more than no answer, and today a search over frontmatter
returns the note and reports complete while having no representation of the field at all.

1. **Given** a corpus, **When** a query runs, **Then** ranking uses BM25 over weighted fields,
   not TF-IDF over one flattened body.
2. **Given** a query with no matches, **When** it runs, **Then** the nearest indexed terms are
   reported and the query is **not** silently broadened.
3. **Given** a note with `status: prospect` in frontmatter, **When** a field query on `status`
   runs, **Then** it is expressible at all — which it is not today.
4. **Given** the same filter run under two ranking configurations, **When** both run, **Then**
   the same set of records matches; only the order differs.

### US-11 — A response the model can act on (P0)

**Why P0:** result delivery moved accuracy 93.1% → 55.2% in the cited study — the same
magnitude as replacing the retriever. Rendering is mechanism.

1. **Given** a partial answer, **When** it is rendered, **Then** the incompleteness is on the
   **first** line, not below the rows.
2. **Given** an excluded record, **When** it is reported, **Then** the fix is in the same line as
   the exclusion.
3. **Given** a joined value, **When** it is rendered, **Then** it is visibly borrowed and not
   merged into the row's own columns.
4. **Given** any response, **When** it ends, **Then** it ends in at least one call the caller can
   make next, with arguments filled in.

### US-12 — An agent manages the vault's own schema (P1)

An agent with write-enabled tools creates a record type, edits a saved view, and changes an
existing type — governed by ordinary tool policy, with no bespoke approval flow.

**Why P1:** the mechanism must be complete enough that a vault's conventions can be expressed
without asking us to change code (ADR-068 D0).

1. **Given** an agent with `vault_configure`, **When** it creates a new record type, **Then** it
   succeeds, **and the response names how many pre-existing notes already declaring that type
   have just become validated records** — because they have, and nothing else in the change says
   so (ADR-068 D15.6).
2. **Given** an agent with `vault_edit` but **without** `vault_configure`, **When** it creates or
   changes any record type or saved view, **Then** it is refused, naming `vault_configure` as the
   tool that would allow it. *(Revision 3 put creation in `vault_edit`, so this posture was not
   expressible at all — the defect ADR-068 D15.6 corrects.)*
3. **Given** an operator who sets `vault_configure: allow`, **When** an agent changes a type,
   **Then** it succeeds — a conservative default is not a prohibition (FR-019a).
4. **Given** an agent with `vault_configure: allow` and `vault_restructure: deny`, **When** it
   edits a schema and then attempts a rename, **Then** the first succeeds and the second is
   refused. The three write tiers are independently settable (FR-083).
5. **Given** any of these, **When** the agent tries to mount a folder, **Then** the only path is
   `request_mount`, unchanged.

### US-13 — Index state a late arrival can still see (P1)

A human who opens the collection panel **after** indexing finished sees the same truth as one who
was watching while it ran.

**Why P1:** for any fast index this is the normal case, not the edge case, and today it renders
as "no progress has arrived" for a fully indexed collection.

1. **Given** an index that completed before the panel was opened, **When** the panel mounts,
   **Then** it renders the completed state, not an empty one.
2. **Given** the same collection, **When** an agent queries it, **Then** the freshness the
   response header reports and the state the panel shows agree.

### Edge cases

| Condition | Expected |
|---|---|
| Property absent entirely | distinct from any value; `is absent` matches it; a negative filter includes it unless excluded |
| Enum value differing only in ASCII case | **REVERSED, revision 5 (operator ruling 3):** it **resolves** to the declared value and is conforming. `Won` is the declared value `won`; it **sorts by `won`'s folded sort key** (revision 6, M-15/M-16 — revision 5 said "`won`'s ordinal", the thing R-E deletes) and renders as the file spells it. It does **not** create a second de-facto value, which is what D4 actually forbids (FR-011) |
| Enum value differing only in **non-ASCII** case (`Ätä` vs `ätä`) | **CORRECTED, revision 6 — revision 5's row stated the OPPOSITE of the requirement.** It **resolves** to the declared value, **unconditionally**. The fold is in the comparator (`cases.Fold()`, FR-011a), not in SQLite, so there is no build in which it does not resolve and no column it depends on. *(Revision 5 said it "does NOT resolve" and gated it on the Go-side fold column FR-011a itself withdraws; DS-1's `ÄKTIV` row, SC-002d, R-5, R-10 and §7 test 53 all required the opposite. An implementer following the old row built a conditional behaviour on a column that does not exist.)* |
| Enum or text value differing by a **FULL**-case-folding pair (`straße` vs `STRASSE`, `ﬁle` vs `file`) | **resolves — and only because the mechanism is `cases.Fold()`.** Under `strings.ToLower` or `strings.EqualFold` — revision 5's mechanism and review round 6's proposed remedy respectively — **both of these are `false`** (executed; see FR-011a's table). This row exists so that a later "simplification" back to a stdlib call is caught by a test rather than by a user (§7 test 53) |
| Enum or text value differing by the **Turkish dotted `İ`** (`İstanbul` vs `istanbul`) | **does NOT resolve, and MUST NOT.** They are different letters; `cases.Fold()` keeps them distinct while `strings.ToLower` collapses them. Asserted as a **negative** case (AC-8.9, §7 test 53, DS-1) so that the correct answer cannot be mistaken for a folding gap |
| Record type declared in two schema files | both rejected, both paths named |
| Relation to a record in another workspace's vault | invisible; treated as dangling within scope |
| `integer` property holding a value above int64 | rejected at write, naming the bound (FR-012). Never `CAST` — a `CAST('9223372036854775808' AS INTEGER)` **saturates silently at int64 max** with no error (§8.1 receipt) |
| `decimal` property with more than 100 decimal places | rejected at write, naming the bound and the value's scale (FR-013); never rounded |
| Vault is not a git repository | schemas still work; no version history is claimed |
| Windows, two processes allocating IDs | collision possible (`fileutil.WithFlock` is a documented no-op — `pkg/fileutil/flock_windows.go`). **NOT healed by reconcile** — reconcile heals a lost counter, it cannot un-write two records that already share an identifier. Detected and reported by `vault_describe check_integrity`, which names both files and refuses to choose. Accepted, inherited from ADR-054 §5, not introduced here |
| Two indexes at different generations | the answer is reported **incomplete**, naming staleness (FR-020c) — never rendered as a complete answer |
| A `many` property compared with `=` | **REVERSED, revision 5 (ruling R-B):** `=` matches an element exactly, `IN` matches any of a list, `LIKE` matches by pattern. Only the **ordering** operators stay undefined and refused (R-13) |
| An anchor matching twice in a body-replace | refused, both line numbers named, file unmodified (FR-047) |
| A trashed note with inbound links | trashed anyway; the links are **not** repaired and the count is reported (FR-048) |

---

## 3. Behavioural contract

- When a query cannot include a record, the system **names that record and the reason**.
- When a numeric value exceeds its declared type's bound — an `integer` outside int64, a `decimal`
  above 100 places — the system **refuses and names the bound**. It never truncates, rounds or
  saturates. *(Replaces the cross-currency bullet, deleted with `money` — FR-014.)*
- When text, an enum value or a relation **path** is matched, the match is **case-insensitive**
  (operator ruling, revision 5). Record **identifiers** are matched **byte-exactly by the
  comparator** — the reasoning is in **FR-011a and in R-8's own row in §8's rule table**. *(Revision
  6, review round 6, M-6: revision 5 sent the reader to "§8.1's R-8 row" from five places and
  **§8.1's table has no R-8 row** — it has R-1, R-2/R-3, R-5, R-7, R-9/R-10, R-11, R-12 and join
  fan-out, and ADR-068 D16.6's table has none either. A reader sent to find the reasoning found
  nothing. All five references are repointed; nothing was "different about that column", because
  under ruling R-A no column decides anything.)*
- When a filter names an unknown property or enum value, the system **rejects the query** and
  lists the valid names.
- When a bound is exceeded, the system **refuses** and states which bound and how to narrow.
- When a write violates the schema, the system **rejects it** and leaves the file untouched.
- When a write succeeds, the file is **byte-identical outside the patched span**.
- When a relation target is missing or mistyped, the system **reports it** at validation.
- When a record is out of workspace scope, the system returns **empty, not an error**.
- When an operation would write bytes into a file the caller did not name (**C-A**), it is
  **only** reachable through `vault_restructure`, and the cascade is reported in counts.
- When an operation would change what already-existing files **mean** — their validity, their
  type, or how a query renders them — without writing them (**C-B**), it is **only** reachable
  through `vault_configure`, and the count of records whose validity changes is reported.
- When a record's two indexes have seen different bytes of the same note, the answer naming that
  record is **incomplete**, with staleness given as the reason.
- When the build has no SQLite, every typed-filter, join, grouping or aggregation call **refuses
  by name**, stating the platform. It never returns an empty result.
- When the two indexes disagree, the answer is **incomplete**, and says so first.
- When a result is rendered for the model, it is **compact text projected from the wire object**,
  completeness first and next actions last.
- When a query finds nothing, the system reports the **vocabulary it holds** and stops.

### Explicit non-behaviours

- The system must **not** return a partial result set that looks complete — that is the defect
  this specification exists to remove.
- The system must **not** write derived values into frontmatter, because a reader cannot then
  distinguish a stale derived value from a fact.
- The system must **not** auto-create an enum value on write, because that is how one column
  comes to hold `Won`, `won` and `Closed Won`. **Case-folding is the opposite of that and is
  required (revision 5):** resolving `Won` **to** the declared `won` collapses two spellings into
  one value; auto-creating `Closed Won` invents a second. D4 forbids the second, not the first.
- The system must **not** re-serialise a file it was asked to make one edit to.
- The system must **not** silently widen an `integer` to a `decimal`, or round a `decimal` to fit a
  bound. Either is a change to a number nobody asked for (FR-012, FR-013). *(Replaces "must not
  convert between currencies", deleted with `money`.)*
- The system must **not** use `CAST(? AS INTEGER)` to admit a numeric string on the SQL side: it
  **saturates silently at int64 max** rather than erroring — verified, §8.1b. Range checking is
  Go-side, before emission.
- The system must **not** implement a text query language or parser (ADR-068 O-3).
- The system must **not** read `.base` files at query time — the importer is one-shot
  (ADR-068 O-1).
- The system must **not** treat an absent property as `false`.
- The system must **not** expand or reformulate a query on the caller's behalf (FR-114).
- The system must **not** report an answer as complete when a returned record's two indexes have
  seen different bytes (FR-020c).
- The system must **not** return an empty result on a build where the properties index cannot
  exist; it must refuse by name (FR-020h).
- The system must **not** enforce a response budget in tokens, because no tokenizer we own is the
  one that would make the number mean what it says (FR-127, ADR-068 D22.7).
- The system must **not** run an unbounded whole-vault sweep from `check_integrity` (FR-075a).
- The system must **not** offer a second mounting path; `request_mount` stays the only one
  (FR-019).
- The system must **not** register the `.base` importer as an agent tool (FR-103).
- The system must **not** let a policy default read as a prohibition (FR-019a).
- ~~The system must **not** use `LIKE` in the compiled filter path, because it is case-insensitive
  for ASCII and would silently change R-10 (AC-8.7).~~ **DELETED, revision 5 (operator ruling 3).**
  Case-insensitive matching is the desired behaviour, so the property `LIKE` was banned for is no
  longer a defect. `LIKE` is permitted. **AC-8.7 and §7 test 39 (`TestFilter_NoLikeInCompiledPath`)
  are deleted with it, not reworded.** **CORRECTED, revision 6 (review round 6, m-3): revision 5
  went on to say *"§8.1 picks `instr()` over a folded column instead … for the compiled filter
  path"*. **§8.1 picks nothing, and there is no compiled filter path** — ruling R-A deleted both,
  in the same revision. What survives, restated: **`LIKE` is a comparator operator now** (FR-022b),
  evaluated in Go with its own escaping rule (FR-011a's metacharacter clause), and **no `LIKE`
  appears in emitted SQL at all** — which is not a preference any more but a **property, asserted
  by AC-8.10 and §7 test 39a**. The prohibition on `LIKE` as a *filter vocabulary* is what ruling
  R-D deleted; the absence of `LIKE` from *emitted SQL* is what C-5 restored, and they are
  different statements about different layers.
- The system must **not** claim a folding guarantee stronger than the one it delivers, and
  **"Unicode-aware" is not a guarantee** — revision 5 used it as though it meant full case folding
  and it does not (FR-011b's three-level vocabulary). Anything resting on SQLite (`COLLATE NOCASE`,
  `LIKE`, `lower()`) is **ASCII-only**, verified over fourteen non-ASCII pairs in §8.1b; anything
  resting on `strings.ToLower` or `strings.EqualFold` is **simple** folding, which is weaker than
  what FR-011a requires and, between those two functions, not even self-consistent. Nothing rests on
  a derived fold column: **there is no fold column** (FR-011a withdrew it), and no requirement,
  criterion or test may reintroduce a dependency on one.

### Machine-verifiable constraints

| Constraint | Value |
|---|---|
| Results per page | default 50, max 200; a clamp is reported in the response |
| Candidate **evaluation** bound (B1) | **50,000 narrowed candidates**, counted before retrieval over the narrowing predicates only (`type`, `path` prefix, `kind`); above it any typed query is refused naming the scope/kind remedy (FR-064). **This is the bound A-14's cost claim rests on** |
| Candidate **materialisation** bound (B2) | **10,000 survivors**; a **row-returning** query aborts and is refused above it (FR-064). **It is an abort during evaluation, not a pre-retrieval count** — revision 5 said the latter and no mechanism could produce it (C-4). **An aggregate-only query is exempt from B2** and is bounded by B1 (FR-064a) |
| Filter tree | **64 leaves, depth 8**; refused above either, naming which (FR-023c). **NEW, revision 6** — it was the only unbounded input, and it is the one that multiplies against the candidate bound |
| Relation hops | 2; a third is refused |
| Record layer availability | every SQLite-capable build. `linux/mipsle` is the one shipped **target** without it — **two binaries**, `omnipus-linux-mipsle` and `omnipus-lite-linux-mipsle` — and both **refuse by name** (FR-020h). *(Revision 5, m-3: "one shipped binary" was one target and two binaries.)* |
| Supported records per vault | 50,000 records — **and this is now reconciled with the candidate cap rather than left to collide with it (C-14).** 50,000 is the population the vault may hold; 10,000 is the population a **row-returning** query may materialise. They were set for different reasons and never checked against each other, which made ADR-068 §1.2's own motivating question — a pipeline total over more deals than the cap — unanswerable by construction. FR-064a resolves it with an aggregate-only path that returns no rows. **Note:** the index counts segments, not records, so this is an unknown larger document count; the segment ratio MUST be measured at W2 and recorded here |
| Peak RSS | ADR-067's < 64 MB steady state is inherited as a **TARGET, not a property** — it was measured for bleve alone, and SQLite's page cache, `GROUP BY`/`ORDER BY` temp b-trees and connection state are unmeasured (ADR-068 D16.4 item 4). W1 measures both indexes, idle and at the cap, on Linux **and** macOS. **No record-specific latency target is stated** |
| Rate limit | **new work for the write path** — `checkRetrievalRate` covers reads only (§1); 429 carries `Retry-After` (FR-067) |
| Numeric arithmetic | **REPLACES "Money arithmetic", revision 5.** `integer` is int64, bound-checked in Go and refused outside it (FR-012). `decimal` is exact and arbitrary-precision over `math/big`, declared scale bounded at **100** (`pkg/records/decimal.go:48`), refused above it and never rounded (FR-013). **No binary floating point anywhere in the parse, storage, comparison, ordering or aggregation path** — asserted by `pkg/records/decimal_no_float_test.go` |
| Case sensitivity | **matching is case-INSENSITIVE** for text, enum values, relation **paths**, `LIKE` and `IN` (operator ruling). **The mechanism is `golang.org/x/text/cases.Fold()` — full Unicode case folding, in the Go comparator** (FR-011a). Nothing rests on SQLite and **there is no fold column**; both were withdrawn under ruling R-A. `strings.ToLower` and `strings.EqualFold` are **forbidden** here (§7 test 53a). Record **identifiers** are matched **byte-exactly by the comparator**, with no folding applied — not by a column collation (R-8, restated). *(Revision 5 said "ASCII-only where it rests on SQLite", "Unicode requires the Go-side fold column" and "on a `BINARY` column". All three named mechanisms that this design does not have.)* |
| SQLite engine | `modernc.org/sqlite v1.46.1` (`go.mod:64`), which reports `sqlite_version()` = **3.51.2** — verified through the driver, not assumed. A test asserts the linked version (FR-020i), because affinity and collation behaviour is version-sensitive |
| Agent tool count | exactly **6** `vault_*` names; **0** `knowledge_*` names; catalog **98 → 95** |
| Tool description budget | ~150 tokens each. **This is a budget for the description PROSE only and it is not the tool surface's standing cost** — see FR-079 and FR-128, corrected in revision 5: the whole JSON parameter schema, every parameter description included, is sent on every request (`pkg/tools/registry.go:557-560`), so the ~900-token figure was computed against the wrong denominator |
| Response budget | **BYTES, not tokens**: ~200–320 bytes/hit, ~4,000 default, **16,000 hard cap**; `minimal` ~80 bytes/hit. **The cap is allocated, not first-come** — mandatory header and `NEXT` are reserved first, then problems to their own clamp, then rows (FR-127c). Scoped to `vault_find`, `vault_describe` and `vault_configure`; `vault_read` has its own budget (FR-072a) |
| Index freshness | **per note**: the properties row's `source_hash` versus **the bleve document's STORED `source_hash`** (FR-020c). *(Revision 6, C-2: revision 5's row said "versus `ManifestEntry.Hash`" — the mechanism FR-020c spends nine bullets disproving, and a query-path read of a mutex-less `Manifest`.)* A mismatch, a missing stored field or an empty hash forces `complete: false` for that record |
| Scoring model | BM25, set explicitly. TF-IDF is the library default and is a defect (FR-110) |
| Embeddings | none, permanently (FR-117) |

---

## 4. Functional requirements

### A note on every example in this document, before the requirements begin

> **R-F — NO HARDCODED DOMAIN VOCABULARY, ANYWHERE. NEW, revision 5, operator ruling, and it
> governs how the whole of the rest of this document must be read.**
>
> **`company`, `deal`, `meeting`, `status`, `stage`, `arr`, `open`, `won`, `lost`,
> `prospect`, `Acme`, `Northwind`, `CO-0142`, `DEAL-0117` — every one of these is an ILLUSTRATION
> of what a vault MIGHT define. NONE of them is anything the product ships, seeds, defaults to,
> validates against, or knows the name of.**
>
> **`person` was REMOVED from that list in revision 6 (review round 6, M-24), because it is FALSE
> of `person` and the error was load-bearing.** FR-004 ships `person` as one of the **seven property
> TYPES**, and ADR-068 D0 carried the same list with the same error. An implementer following R-F
> literally would have deleted a shipped type. **The two axes must not be confused, and they are
> now stated separately:**
>
> | Axis | Whose vocabulary | Examples | Shipped? |
> |---|---|---|---|
> | **Property TYPE names** | **OURS** | `text`, `enum`, `relation`, `date`, `integer`, `decimal`, **`person`** | **YES — all seven, FR-004** |
> | **Record type names, property names, enum values, identifier prefixes** | **THE VAULT'S** | `company`, `deal`, `status`, `won`, `CO-0142` | **NO — none, ever, FR-004a** |
>
> `meeting` has the mirror problem and it is fixed the same way: R-F called it an illustration while
> §4.1.6's refusal table called the same string contract. It is an **illustration**; §4.1.6 is
> corrected (M-25).
>
> **HOW OFTEN THE ILLUSTRATIONS APPEAR — the real figure, because revision 5's was wrong by an
> order of magnitude and that understatement was doing work.** Revision 5 said *"roughly thirty
> times below and fourteen times in ADR-068"*; ADR-068 D0 said *"fourteen times [in the ADR] and
> thirty-three times [in the specification]"*. **Counted whole-word, case-insensitively, over the
> list above:** **313 in this specification and 135 in ADR-068.** Reproducible:
>
> ```
> grep -oiwE 'company|companies|deal|deals|meeting|meetings|status|stage|arr|open|won|lost|prospect|Acme|Northwind|CO-0142|DEAL-0117' <file> | wc -l
> ```
>
> Restricting to the terms with no ordinary-English reading (dropping `status`, `stage` and `open`):
> **243 and 90.** *(Review round 6 reported 288 and 119 over a slightly different word set; the
> figures differ in the third digit and agree completely on the point — the stated counts were
> understated roughly tenfold. Both counts are given here with the command that produces them so
> the next reviewer does not have to re-derive a third number.)*
>
> **What that number means, said plainly rather than absorbed.** R-F's own justification is *"a rule
> that is stated once and then quietly undermined by every example is a rule that will be broken by
> someone acting in good faith"*. At **313** occurrences an implementer meets the vocabulary
> **twenty times** for every time they meet this boxed disclaimer. **A marker at the front is doing
> very little work, and saying otherwise would be the same kind of unchecked claim this rule
> exists to catch.** The structural answer — replacing the running examples with an obviously
> non-domain vocabulary (`widget`/`gizmo`, or an abstract `type_a`/`prop_1`) — would discharge R-F
> **by construction** instead of by disclaimer, which is exactly the argument this document makes
> for preferring a Go comparator over nine SQL defeats, turned on its own prose.
>
> **DECISION, revision 6: the replacement is ACCEPTED IN PRINCIPLE and DEFERRED, with the reason
> and the schedule stated rather than left as silence.** It is a ~450-occurrence mechanical edit
> across two documents whose cross-references, refusal strings, worked example (§4.2) and datasets
> all quote the vocabulary; done in the same pass as eleven CRITICAL semantic corrections it would
> make both sets of changes unreviewable, and a botched find-and-replace inside a normative refusal
> string is a worse defect than the one it fixes. **It is scheduled as its own task with its own
> review, as W0's second deliverable** (§11), with a stated exit criterion: the counts above,
> re-run, return **zero** outside a single explicitly-marked illustrative appendix. **Until then
> R-F is discharged by the markers (M-25), which is the weaker instrument, and this paragraph is
> the record that we know it is weaker.**
>
> The operator's framing, which is sharper than anything this document had: **"a generic vault
> system where enums like that must very clearly be defined by agents and not hard coded — like an
> empty database, all capabilities but nothing predefined."**
>
> **This REINFORCES ADR-068 D0 rather than changing it** — D0 already says *"we ship mechanism, the
> vault ships convention"* — and the ADR's revision 7 strengthens D0 with the operator's wording.
> **What is new is that it is now testable:** **FR-004a** requires it, and a test asserts it.
>
> - **FR-004a** **NEW, revision 5; TEST RESPECIFIED AND THE \"VERIFIED CLEAN\" CLAIM WITHDRAWN,
>   revision 6 (review round 6, C-10).** The shipped binary MUST contain **no domain vocabulary**:
>   no seeded record type, no seeded enum value, no seeded property name, no seeded view, no seeded
>   identifier prefix. A fresh install has **zero** record types, and a vault with no
>   `.omnipus-vault/records/` directory is a working vault of ordinary notes (FR-005).
>   - **WITHDRAWN: *"Verified at revision time: the code is already clean — zero domain vocabulary
>     outside tests in `pkg/records` and `pkg/knowledge`."*** It does not reproduce as stated, and
>     it was the kind of unchecked assertion this document exists to remove. **Re-executed for
>     revision 6:** `grep -rInwiE 'company|deal|contact|lead|opportunity|pipeline|prospect|stage|arr|crm'`
>     over non-test `.go` files in those two packages returns **44 hits**.
>   - **AND THE UNDERLYING CLAIM IS NEVERTHELESS TRUE, which is why the fix is to the TEST and not
>     to the code.** Every one of the 44 was read. **Zero are declared enum values, type names,
>     seeded schemas or default config.** They are: prose in **doc comments** (`pkg/records/doc.go:12`,
>     `invalidate.go:139-141`, `schema.go:1073-1074`, `value.go:304-311`, `money.go:240`), the word
>     **`stage`** used 19 times in `pkg/knowledge` for ordinary ADR-067 implementation stages
>     (`index.go:1`, `scan.go:1`, `tools.go:32`, …), **`arr`** inside a YAML illustration in
>     `frontmatter.go:22`, and one local variable `lead` in `pkg/knowledge/rename.go:710` meaning
>     *leading whitespace*. **The code ships no domain vocabulary. The GREP was the wrong
>     instrument.**
>   - **The most consequential hit is the one that proves the test unpassable:** `pkg/records/doc.go:12`
>     reads *"declares NO record types of its own — no company, no contact, no deal, no…"*. **That is
>     the D0 statement itself.** A test written to revision 5's words red-lights the build on the
>     sentence asserting the opposite of hardcoding — and a test that fails on day one for a reason
>     nobody accepts is a test that gets weakened until it asserts nothing.
>   - **THE TEST, RESPECIFIED to what the requirement actually cares about, which is decidable:**
>     a test asserts that **`coreagent.SeedConfig`, the default config (`pkg/config/defaults.go`),
>     and the shipped schema and saved-view directories contain ZERO record types, ZERO enum values,
>     ZERO property names and ZERO identifier prefixes** — over **declared identifiers and literal
>     data, never over comments**. The subject is **seeded data**, which is what R-F is about;
>     `.omnipus-vault/records/` on a fresh install is empty, and that is assertable exactly.
>   - **The denylist, if a lexical check is kept alongside it, is NARROWED to terms with no
>     plausible non-domain use** — `crm`, `opportunity`, `prospect`, `deal`, `company` — and
>     **`stage`, `lead`, `arr`, `pipeline` and `contact` are DROPPED, deliberately, with the reason
>     recorded here so nobody re-adds them:** they are ordinary English and ordinary programming
>     vocabulary, they produce false positives **structurally rather than incidentally**, and the
>     count grows every time someone writes `stage` in a comment. A denylist that must be weakened
>     to stay green is worse than no denylist.
>   - **Scope, stated in both directions (M-27):** the lexical check runs over **declared
>     identifiers and string literals in non-test `.go` files, `pkg/config/defaults.go`, and the
>     shipped `.omnipus-vault/` seed directories**. **Excluded by path:** `*_test.go`, `testdata/`,
>     and **comments and doc comments in any file**. *(Revision 5 said the fixture exclusion was
>     "narrow and named rather than a general 'except where inconvenient'" and then named no path —
>     which is precisely the claim that needs the name.)*
> - Test fixtures MAY use domain vocabulary, and DS-1..DS-3 do, under the path exclusion above.

### Schema and types

- **FR-001** The system MUST load record-type schemas from `<vault>/.omnipus-vault/records/<type>.yaml`.
- **FR-002** The system MUST reject a schema without `schema_version`.
- **FR-003** The system MUST reject two schema files declaring the same record type, naming both paths.
- **FR-004** **MEANING CHANGED, revision 5 (operator ruling).** The system MUST support exactly these **seven** property types: `text`, `enum`, `relation`, `date`, `integer`, `decimal`, `person`. *Previously: `text`, `enum`, `relation`, `date`, `number`, `money`, `person`. **`money` is deleted from the type system** — the operator's requirement is "a precise decimal datatype and also an integer 64 datatype", not a currency-carrying value; and **`number` is split into `integer` and `decimal`**, because one numeric type forces the index to infer a column type from the first value it sees. **The count is unchanged at seven; the membership changed** — −1 (`money`) −1 (`number`) +2 (`integer`, `decimal`). Anywhere this document previously said "seven property types" it still says seven, and it is not a stale figure.*
- **FR-004b** **NEW, Draft 7 (UAT case C-7). `person`'s `to:` is OPTIONAL, and that is the ONLY thing that distinguishes `person` from `relation`.** A `relation` property MUST declare `to:` and MUST be refused without it. A `person` property MAY declare `to:`; **when it does, the target type is checked exactly as a relation's is (FR-034), and when it does not, only the link SHAPE is validated** — the value must be a quoted wikilink (FR-030, D5.1) and a bare name is a reported finding, never a silent accept. In every other respect `person` **is** a relation: it compares by target identity under **R-8**, its path side folds and its identifier side is byte-exact, and it is refused for the ordering operators.
  - **Why this had to be ruled: the UAT was written to mark case C-7 *Blocked* if the product had no way to say which record type a person resolves to**, and ADR-068 revision 8 pointed three ways at once — D3's prose said *"a relation to a **person record**"* (which reads as a shipped `person` record type, the thing **D0 forbids**), D0's own table listed `person` among **our** seven property TYPES, and **D2's canonical schema example writes `owner: { type: person }` with no `to:`** while `relation` refuses without one. A tester could not build a fixture guaranteed to pass; an implementer could not know whether to demand `to:`.
  - **The ruling is what the code already does, and it is cited rather than asserted.** `pkg/records/schema.go:Property.finalize` requires `To` in its `case TypeRelation:` arm **and in no other**; its sibling check reads *"`to`/`inverse` are only meaningful on a relation or person, not on a %s"* — permitted on `person`, demanded only of `relation`. `pkg/records/schema.go:TypePerson`'s doc comment already states it: *"With no `to:` declared, only the link shape is validated."* `pkg/records/compare_oracle.go`'s authority table carries *"person — ADR-068 D3 defines person as a relation, so it inherits R-8."*
  - **Neither alternative reading is taken, and the reason is D0 in both directions.** Making `to:` **required** deletes shipped, tested behaviour that lets a vault say *"this is a person link"* before it has decided what its people are called. Resolving an absent `to:` against a **vault-declared convention** — a default target type the product looks up — would mean the product knowing a name for the operator's people, which is the hardcoded-vocabulary failure **D0.1** exists to prevent. **Absent `to:` means the target type is simply not checked**, which costs nothing and was already built.
  - **AC-4b.1** — `{ type: person }` with no `to:` is **accepted**, and a value that is a bare name rather than a wikilink is reported with the reason (`pkg/records/validate.go`'s `FindingNotAWikilink`). **AC-4b.2** — `{ type: person, to: <a declared type> }` is accepted and its target type IS checked. **AC-4b.3** — `{ type: relation }` with no `to:` is **refused**, naming `to:`. **AC-4b.4** — the three criteria above are asserted against **one** shared fixture, so a change that makes `person` behave like `relation` fails AC-4b.1 rather than passing everything.
  - **ADR-068 D3's prose is corrected in revision 9 (new D3.4); D2's canonical example is NOT** — it was right as written, and it is annotated to say so rather than "fixed" into the inconsistency it was accused of.
- **FR-005** The system MUST treat a note whose `type` matches no schema as an ordinary note, without error.
- **FR-005a** **NEW, Draft 7 (UAT case C-8.4). An unknown schema key MUST be REFUSED BY NAME, and the permitted set MUST be named with it. THE BEHAVIOUR IS ALREADY SHIPPED AND CORRECT; what was missing was a requirement, so an implementer had nothing to build to and a tester had nothing to assert.** A schema file's top level, its `identity` block, a property declaration and an enum value's long form each have a **closed key set**. A key outside that set MUST be rejected, naming the key **and** listing what the declaration does carry. It MUST NOT be dropped in silence — *"an author who writes something meaningful must never be told nothing when we throw it away"* is the same rule FR-006 applies to arity, generalised.
  - **A key we publish but do not act on MUST be refused with ITS OWN reason**, never called "unknown": the operator wrote a key from our own contract, and calling it unrecognised is a second error on top of the first.
  - **The version check MUST run BEFORE the unknown-key check, and this ordering is load-bearing.** A key this release does not know, inside a `schema_version` this release does not know, is an **unsupported version** — reporting `unknown key "x"` about a version-2 file sends the operator to fix the wrong thing.
  - **A property-level unknown key is reported as `schema_bad_property`, not `schema_unknown_key`**, because the property it belongs to is the thing the operator has to go and fix.
  - **Already built, and cited rather than described:** `pkg/records/schema.go:checkDeclaredKeys`; the four key sets `schemaFileKeys` / `identityDeclKeys` / `propertyDeclKeys` / `enumValueDeclKeys`; the `RejectUnknownKey` rejection code. Three guards hold it in `pkg/records/schema_declared_keys_test.go`: `TestSchema_EveryContractPropertyKeyIsHandled` — which reads `contracts/components/schemas/PropertyDef.yaml` **itself** rather than a transcription of it, so a key added to the contract without being handled fails the build; the *"no contract key is silently ignored"* subtest, which parses each key present and absent and fails if the two produce an identical property; and `TestSchema_RefusedKeysCarryAReason`, whose invariant is *"a refused key is refused in words the operator can act on"*.
  - **AC-5a.1** — a schema declaring `{ type: text, colour: blue }` is refused, and the message contains both `colour` **and** the permitted property keys. **AC-5a.2** — a schema declaring an unknown key under `schema_version: 2` is refused as an **unsupported version**, and the message does **not** name the key. **AC-5a.3** — the guards above are on the required-checks list; they are the reason this requirement does not need new code.
- **FR-005b** **NEW, Draft 7 (UAT case C-8.4), and it REVERSES ADR-068 revision 8's `unit` ruling with evidence. `unit` IS a schema key on `integer` and `decimal`, it is OPAQUE, and the product never interprets it.**
  - **What revision 8 ruled, and why it does not survive.** ADR-068 revision 8 wrote *"`unit` is NOT a schema key"*, on the ground that *"no FR, no wire schema and no test anywhere defines a `unit` key."* **All three halves are false**, checked by reading: `contracts/components/schemas/PropertyDef.yaml` declares a `unit` property with its own description; `pkg/records/schema.go`'s `propertyDeclKeys` carries `"unit": {kind: declKeyParsed}`; `Property.Unit` is a real field and `Property.finalize` refuses it on a non-numeric type with *"`unit` is only meaningful on an integer or a decimal"*; and `schema_declared_keys_test.go` asserts for `unit`, along with every other contract key, that it is **not silently ignored**. **And ADR-068 D3's own `decimal` row, in the same document, names `unit` as the mechanism that closes the `exercise: 60 minutes` failure** — revision 8 deleted in D2 a key D3 was simultaneously requiring. Corrected in ADR revision 9.
  - **The requirement.** `unit` is valid on `integer` and `decimal` and on **nothing else**. Its value is **opaque text**: it MUST NOT affect comparison, ordering, grouping, aggregation, validation, storage or narrowing. It is carried and rendered. **No code branch anywhere may read its value** — only whether it is present and whether its property is numeric.
  - **`unit` is declared per PROPERTY and never per VALUE, and that is what makes the money hazard structurally unreachable rather than merely forbidden.** Every value of one property shares one unit by construction, so a `SUM` **cannot** add GBP to JPY: there is no per-value currency for it to mix. This is the same move the design makes everywhere else — remove the failure's precondition rather than police its symptom.
  - **`scale` is NOT a property-level key** and MUST fall through to FR-005a's unknown-key rejection. FR-013's decimal bound is enforced **per value** at parse time, at `maxDecimalScale` (100, `pkg/records/decimal.go`). *(ADR-068 revision 8's canonical example wrote `arr: { type: decimal, scale: 2 }` and called `scale` `unit`'s replacement. The two are unrelated and `scale` is not a key a property may carry; the example is corrected in revision 9.)*
  - **AC-5b.1** — `{ type: decimal, unit: GBP }` is **ACCEPTED**, and **no** response anywhere in the product contains "currency", "ISO-4217" or "minor units", and nothing treats `GBP` as other than three characters. *This is UAT C-8.4 and C-8.5 together, and it is the criterion that distinguishes "the product ships a unit field" from "the product ships money".* **AC-5b.2** — `{ type: text, unit: minutes }` is refused, naming `unit` and the two types it is meaningful on. **AC-5b.3** — an aggregate over a `unit`-bearing property produces a number with no unit-aware behaviour of any kind; the unit is rendered beside it, never folded into it.
- **FR-006** Each property MUST declare arity, and the system MUST reject a value of the wrong arity.
- **FR-007** The system MUST distinguish an absent property from every value of that property.
- **FR-008** **DISAMBIGUATED, revision 6 (review round 6, C-7) — the phrase "a negative filter" was read two ways and the two readings contradict each other.** A **negated filter TREE** — `{not: {...}}` — MUST include records where the property is absent, unless the query excludes them explicitly; this follows from R-2 by construction, because a comparison over an absent side is a real `false` and `NOT(false)` is `true`, at any depth. **A `<>` LEAF is NOT a "negative filter" in this sense and does NOT include absent records** (R-2, §4.1.2's filter table, DS-4 rows E and E2). *Revision 5 stated only the first sentence, and §4.1.2's filter table plus DS-4 row E read it as covering the `<>` leaf — the single most consequential cell in the truth table, specified in two directions at once.*
- **FR-009** Property types MUST be scoped to their record type; the same name in two types is unrelated.
- **FR-010** **MEANING REVERSED, revision 5 (operator ruling R-E).** An enum MUST declare its permitted values as a **set**. **Sorting is lexical, not by declared position — and it is computed in Go.** *(Revision 6, review round 6, survivor 6: revision 5 said "follows SQLite's own ordering". The ORDER is the same order SQLite's `BINARY` collation would produce; the COMPUTATION is ours, because ruling R-A leaves SQLite deciding nothing. The sort key is the folded form — see R-5 clause (c) and M-8's resolution.)* An author who wants a domain order **prefixes the values**: `1-lead`, `2-qualified`, `3-proposal`, `4-won`. *Previously: "MUST declare its values in order, and sorting MUST follow declared position, not lexical order."*
  - **What this deletes:** the derived ordinal column, its `NULLS LAST` requirement, the schema bookkeeping that kept it in step with the file, and the rebuild obligation nobody had written (the grill's m-7 — an enum **reorder** changed the derived ordinal for every record of the type, and no FR required the reindex; the grill's m-8 — the cascade block reported "0 records lost validity" while silently reordering every existing report).
  - **The cost is real and is accepted rather than glossed.** ADR-068 §1.4 cites the `1-Pending…7-DoNotContact` prefix hack as a **documented failure** of the incumbents, and this ruling adopts it as the mechanism. **The trade is visibility:** the prefix is in the operator's own file and does exactly what it looks like it does, where a derived ordinal was a second source of truth for the order, invisible in the vault, and drifted on every reorder. A convention the operator can see and change beats a mechanism they cannot.
  - **ADR-068 D4's title — "Enums are closed and ordered; ordering is data, not spelling" — is now wrong in its second clause** and is corrected in ADR revision 7. **Closed** is unchanged and is the half D4's evidence supports.
  - **The refusal string changes.** `enum property 'status' must declare its values in order` is deleted from §4.1.6; declaring an enum in any order is now valid.
- **FR-011** **MEANING CHANGED, revision 5 (operator ruling 3).** The system MUST **resolve** an enum value to a declared value **case-insensitively**, and MUST reject a value that resolves to none of them, listing the permitted values. *Previously the resolution was exact-case, which made `Won` a rejection. Under ruling 3 it resolves to `won`.* **Two consequences are normative:** the value it resolves to supplies the **sort key** (FR-010, R-5), so ordering is unaffected by spelling *(revision 6, review round 6, M-15: revision 5 said "ordinal", which FR-010 deletes twelve lines above)*; and the **file's own spelling is what renders**, because the note is the source of truth and this system does not rewrite a file it was not asked to change (FR-046's sibling rule). *(This does not weaken D4: D4 forbids **auto-creating** a second value, and folding does the opposite — it collapses two spellings into one. §7 test 2 and DS-1's `Active` row are corrected, not merely re-labelled.)*
- **FR-011a** **NEW, revision 5 (operator rulings R-D and R-A). MECHANISM CORRECTED, revision 6 — revision 5's mechanism did not do what revision 5 said it did, and this was established by execution, not by argument.** **Case-insensitive matching is a property of the comparator, in Go, over Unicode — not a collation, and not a derived column.** Every `=`, `<>`, `LIKE` and `IN` comparison on `text`, on an `enum` label and on a relation **path** MUST fold both sides with **one shared Go function**, and that function MUST be **`golang.org/x/text/cases.Fold()`** — Unicode **FULL** case folding. **`strings.ToLower` and `strings.EqualFold` are both FORBIDDEN for this purpose**, and a test asserts neither appears in the comparator (§7 test 53a).
  - **What revision 5 claimed, and why it was false.** Revision 5 said *"`strings.ToLower` is Unicode-aware, so this is full-Unicode case insensitivity for free"*. **Unicode-aware is not the same property as full case folding, and the Go standard library implements only SIMPLE folding.** Worse, its two candidate methods fail in **opposite** directions, so there is no safe standard-library default. Executed on this machine, Go 1.26.6, against `golang.org/x/text v0.41.0`:

    | Pair | `strings.ToLower` equal | `strings.EqualFold` | **`cases.Fold()`** | Correct answer |
    |---|---|---|---|---|
    | `straße` / `STRASSE` (German) | false | false | **true** | **true** — `ß` full-folds to `ss` |
    | `σίσυφος` / `ΣΊΣΥΦΟΣ` (Greek final sigma) | false | true | **true** | **true** — `ς` and `σ` fold together |
    | `müller` / `MÜLLER` (German umlaut) | true | true | **true** | true |
    | `łódź` / `ŁÓDŹ` (Polish) | true | true | **true** | true |
    | `istanbul` / `İSTANBUL` (Turkish dotted I) | true | false | **false** | **false** — see below |
    | `ﬁle` / `file` (ligature) | false | false | **true** | true |

    The two stdlib columns **disagree with each other** on rows 2 and 5. `cases.Fold()` is the only column that is right in every row.
  - **The Turkish row is a REQUIREMENT, not a defect, and it is stated here so nobody "fixes" it later.** `İ` (U+0130, Latin capital I with dot above) and plain `i` are **different letters**. Unicode full case folding maps `İ` to `i` + U+0307 (combining dot above), which is not equal to plain `i`, so `cases.Fold()` keeps them distinct. `strings.ToLower` collapses them, and that collapse is the classic **Turkish-I bug** — the one where a Turkish user's `İSTANBUL` silently matches an unrelated `istanbul`. **A future contributor who observes that `ToLower` "handles Turkish" and `cases.Fold()` does not has the sign backwards.** `cases.Fold()` is also **locale-free and deterministic**, which is the property a vault with no declared locale needs: the same two strings compare the same way on every machine, in every timezone, under every `LANG`.
  - **Hard Constraint #1 is satisfied, and the measured cost is given rather than waved at.** `golang.org/x/text v0.41.0` is **already a dependency** — `go.mod:167`, currently marked `// indirect` — and **several of its subpackages already link into the shipped binary today** (`golang.org/x/text/language`, `/transform`, `/runes`, `/unicode/norm`, `/encoding/*`, plus the vendored copies under `vendor/golang.org/x/text/`; enumerated with `go list -deps -tags goolm,stdjson ./cmd/omnipus/`). Promoting it to a **direct** requirement therefore adds **no new module, no new runtime dependency, no CGo and no external C library**. **What it does add, measured:** the `cases` subpackage itself is not currently linked, and adding it costs **≈274 KiB of binary** (280,352 bytes — two otherwise-identical programs built `CGO_ENABLED=0`, one importing `x/text/cases` and one not). That is read-only table data, demand-paged, not heap, so Hard Constraint #3's 10 MB RSS budget is not materially touched. **The number is stated because "adds nothing to the binary" would not survive a measurement, and an unverified size claim is exactly the failure class this document exists to remove.**
  - **`cases.Fold()` is stateless and concurrency-safe, and this is load-bearing.** A `cases.Caser` in general *"may be stateful and should therefore not be shared between goroutines"* (`golang.org/x/text@v0.41.0/cases/cases.go:35-36`), but `Fold` is documented as the exception: *"The returned Caser is stateless and safe to use concurrently by multiple goroutines"* (`cases.go:86-87`). The comparator MAY therefore hold **one package-level `cases.Fold()` value** and use it from every query goroutine. **An implementer who assumes the general Caser rule applies will construct one per comparison and pay for it; an implementer who assumes a general Caser is shareable will build a data race in some other part of the codebase.** Both mistakes are avoided by citing the exception here.
  - **Folding changes rune count, and `LIKE`'s `_` must be specified against that.** `straße` is 6 runes and folds to `strasse`, which is **7**; `ﬁle` is 3 runes and folds to 4; `İ` is 1 rune and folds to 2. So `_` — SQL's exactly-one-character wildcard — **cannot mean the same thing before and after folding**. **The rule:** `LIKE` folds the subject and the pattern's **literal segments**, never its metacharacters (`%`, `_`, and the `\` escape), and **`_` then matches exactly one character of the FOLDED subject**. `'straße' LIKE 'stra_e'` is therefore **false** (the folded subject has two characters where the pattern has one) and `'straße' LIKE 'stra__e'` is **true**. This is stated rather than discovered because it is the kind of behaviour a test writer will otherwise read off whatever the implementation happened to do. See FR-022b for `LIKE`'s anchoring, and DS-4 for the cases.
  - **`IN` folds, and this is now decided rather than left silent.** `IN` is set membership evaluated as a disjunction of `=`, and `=` folds, so **every element of an `IN` list is folded and so is the subject**. Revision 5 left this to inference and the truth table would have been filled in by guessing.
  - **Enum equality folds by the SAME function**, so FR-011's resolution of a written value to a declared value is `cases.Fold()` on both sides — not a second, weaker rule. Consequence: `STRASSE` resolves to a declared `straße`, and `ÄKTIV` resolves to a declared `äktiv`, **unconditionally and with no SQLite involvement** (DS-1).
  - **Ordering does NOT use the fold. See M-8's resolution in R-5.** Folding is for *matching*. The sort key is specified separately in FR-010/R-5, because folding is not order-preserving — `"Won" < "lost"` is **true** on raw bytes and **false** on folded bytes (executed), so a comparator that reused the fold as its sort key would silently reorder every result.
  - **Record IDENTIFIERS are excluded and are matched exactly.** Folding `CO-0142` and `co-0142` into one key would make two legitimately distinct targets indistinguishable. Identifiers are minted by us in one canonical form, so nothing is lost. Relation **paths** fold; relation **ids** do not (R-8). *(R-8 is now a comparator rule, not a column collation — see R-8 as restated in §8.)*
  - **The fold is not a fourth notion of a term** and MUST NOT be confused with FR-116's tokenizer question: it is a character-level transform that splits nothing, stems nothing and removes no stopword.
  - **The derived `<col>_fold` SQLite column is WITHDRAWN**, and nothing in this document may depend on one. *(An earlier draft of revision 5 specified one. Under ruling R-A there is nothing in SQLite to compare it against, and it cost one extra TEXT column per foldable property for a strictly worse — ASCII-only — fold.)*
  - **The cost is stated:** one `cases.Fold()` call per operand per comparison, over a candidate set FR-064 caps at 10,000. `cases.Fold()` is **idempotent** — `fold(fold(x)) == fold(x)`, executed over all six pairs above — so the record side MAY be folded **once per candidate** and reused across every leaf of the filter tree (FR-011c). Unmeasured; carried by A-14, whose worst case is bounded by FR-023c's leaf-count cap.
- **FR-011b** **NEW, revision 5; VOCABULARY EXTENDED, revision 6.** There are **three** distinct guarantees here and a surface MUST name the one it delivers, never the stronger one:
  1. **ASCII case folding** — what `COLLATE NOCASE`, `LIKE` and `lower()` inside SQLite deliver (verified, §8.1). Two of fourteen pairs.
  2. **Simple Unicode case folding** — what `strings.ToLower` and `strings.EqualFold` deliver. It is **not** full folding and it is **not** self-consistent between those two functions (the table above).
  3. **Full Unicode case folding** — what `cases.Fold()` delivers, and what this system ships.
  Where any surface delivers (1) or (2) — a fallback path, a SQLite-side narrowing that happens to compare text, a future optimisation — it MUST be documented and reported as **ASCII-case-insensitive** or **simple-case-insensitive** respectively, never as "case-insensitive" without qualification and never as "Unicode-aware", which revision 5 used as though it meant (3) and it does not. A guarantee that silently holds for one alphabet, or for one folding depth, is the failure class this document exists to remove.
- **FR-011c** **NEW, revision 6 (closes review round 6 M-31).** An implementation MAY fold the **record side** of a comparison **once per candidate** and reuse the folded form across every leaf of the filter tree; it MUST NOT fold once and then render the folded form. **The folded form is never rendered, never written to a note, and never returned on the wire** — the file's own spelling is what renders (FR-011, FR-046's sibling rule). This permission is stated because without it an implementer either pays FR-011a's per-comparison cost as specified — candidates × leaves folds, which is the multiplication A-14 is exposed to — or reintroduces a persisted derived fold column, which FR-011a withdrew. A per-candidate in-memory fold is neither.
#### The two numeric types (revision 5, operator ruling)

An author **chooses** `integer` or `decimal` in the schema. **There is no inference from the first
value seen**, and this is the point of the split rather than a side effect of it: with one `number`
type the properties index would have to decide a column type from data, so the same property could
land in an INTEGER column in one vault and a TEXT column in another, and `2` and `2.0` would compare
differently depending on which note happened to be indexed first. The schema decides; the column
follows the schema.

- **FR-012** **MEANING CHANGED, revision 5 — this FR was `money`; `money` is deleted.** An `integer` property MUST hold a signed 64-bit integer, and the accepted range MUST be exactly **−9,223,372,036,854,775,808 to 9,223,372,036,854,775,807** inclusive. A value outside that range, or a value carrying a fractional part, MUST be **refused with the bound named** — never truncated, never rounded, and never widened to `decimal` on the system's initiative. *(SQLite's INTEGER storage class is exactly int64 and stores the whole range losslessly, so the storage bound and the type bound are the same number and there is no second limit to reconcile. What SQLite does **not** do is refuse an overflow: scalar `9223372036854775807 + 1` silently becomes a REAL — see §8.1b's arithmetic receipt.)* *(Revision 6, review round 6, M-38: revision 5 cited "§8.1a, R-11a" — the receipts are in **§8.1b**, not §8.1a, and **no `R-11a` exists anywhere in this document**. It was a label for a receipt, not a rule.)*
- **FR-013** **MEANING CHANGED, revision 5.** A `decimal` property MUST be **exact and arbitrary-precision**, and no value of it may pass through a binary floating-point representation anywhere in the parse, storage, comparison, ordering or aggregation path. *Previously: "Money arithmetic MUST be exact decimal" — the guarantee survives its subject.*
  - **The declared-scale bound is `maxDecimalScale = 100`** (`pkg/records/decimal.go:48`) — **100 digits after the point**, and the choice is deliberate and generous. The retired `money` type bounded scale at `maxMoneyScale = 12` (`decimal.go:166`); **that bound is deleted with money and MUST NOT be inherited by `decimal`.** Twelve places is a currency-shaped limit and this type is not currency-shaped: a `decimal` property is as likely to hold a scientific measurement, a ratio, or a coordinate as a price. 100 is what the existing parser already enforces and already has a property test over the full symmetric range (`pkg/records/decimal_string_bounds_test.go`, which sweeps every scale in `[-100, +100]`), so adopting it costs nothing and removes a bound nobody could justify.
  - **A value whose scale exceeds 100 MUST be refused, naming the bound and the value's own scale.** It MUST NOT be rounded to fit. Rounding to satisfy a bound is a silent change to a number, which is the failure class this document exists to remove.
  - `pkg/records/decimal.go` (588 lines, `math/big`-based, with `pkg/records/decimal_no_float_test.go` asserting no binary float appears anywhere in the path) **is the implementation and survives money's removal unchanged.** It is the valuable half of the retired work.
- **FR-014** **RETIRED, revision 5 — `money` is deleted, so cross-currency summation has no subject.** *Previously: "The system MUST refuse to sum money across currencies and MUST list the currencies present." **The number is retired rather than reused**, so that a reader meeting `FR-014` in an older document, a commit message or a test name finds this entry and not a different requirement wearing the same identifier. Everything that depended on it goes with it: US-2 scenario 3, the behavioural-contract cross-currency bullet, the `Cross-currency total` refusal string in §4.1.2, §4.2's `GBP only` total, **R-6** and its §8.1 defeat row, `TestMoney_RefusesCrossCurrencySum` (§7 test 4), and the `CurrenciesPresent` field. All are removed in this revision, not left as dead references.*

- **FR-015a** **NEW, revision 5 — FR-016's mandated count block was not computable as specified, and FR-015 contradicted AC-C1 on whether it was synchronous.** Three things are decided here:
  1. **The population is findable, and the FR that makes it findable is named.** Before a schema exists, notes declaring `type: X` are ordinary notes (FR-005) and the properties index holds records, so the population `create_record_type` must count is precisely the one the properties index does not hold. **FR-111 MUST index the `type` frontmatter key for EVERY note, record or not**, and this is stated as a requirement rather than assumed — revision 4 rested FR-016's computability on an unstated property of a different FR.
  2. **Fielded values MUST be `Store: true`** for the property keys and values FR-111 adds, so naming the notes that newly fail validation is a query rather than N file reads. *(This is the same `Store: true` decision FR-020c's freshness field needs, taken once for both.)*
  3. **Revalidation is SYNCHRONOUS AND BOUNDED, and FR-015's wording loses.** FR-015 said a schema change *"MUST … trigger revalidation"*, which reads asynchronous, while AC-C1 requires the response itself to carry the results. **AC-C1 wins**, because a cascade the caller does not see reported is the invisible cascade `vault_configure` exists to make visible. **The bound is 10,000 notes converted**, above which the call is **refused** naming the count and the remedy (narrow the type, or run the conversion from the CLI) — the pattern FR-075a uses for `check_integrity`. *Revision 4 had no bound at all: a vault with 100,000 notes declaring one type would validate 100,000 notes inside one tool call, and FR-075a's cap applies to `check_integrity` only.*
- **FR-015** A change to a schema file MUST invalidate affected records and revalidate them **synchronously, within FR-015a's bound**, reporting the result in the response. Schemas live under a directory the scanner does not walk, so no manifest entry or mtime exists for them; the system MUST track them explicitly rather than inheriting note-scanning behaviour. *(Unchanged in meaning. **Cited by ADR-068 D23.3** as the reason an existing-record-type change sits in a cascading tier: it retroactively reinterprets every record of that type. **Revision 4: that tier is `vault_configure`, not `vault_restructure`** — the reasoning is unchanged, the destination moved, and the same reasoning now also carries **creating a new type**, per FR-016.)*

### Schema and view authoring (ADR-068 D23, D15.6)

Schema and view authoring is **ordinary agent work, governed by ordinary tool policy**. An agent
with write-enabled tools manages the vault completely, and that explicitly includes creating and
changing record types and saved views. There is no bespoke approval flow, no
`request_schema_change`, and no UI-ratifies step.

**What it is NOT is an operation of `vault_edit`, and revision 3 had that wrong.** Policy resolves
on the tool **name** alone (FR-070c), so putting schema authoring inside `vault_edit` made the
posture *"this agent may edit notes freely, but may not redefine what a note is"* **inexpressible**
— while revision 3's own FR-083 told an operator that restricting `vault_restructure` protected
schemas. It did not: an agent holding `vault_edit: allow` could create arbitrary record types.
A control described as working while it does not is the failure class this document exists to
remove. **Every schema and view operation therefore lives in `vault_configure`** (ADR-068 D15.6).

**The two criteria, because one was being read two ways** (ADR-068 D15.1, revised). Either alone
puts an operation in a cascading tier:

> **C-A (bytes).** Does this operation write bytes into files the agent did not name?
> **C-B (meaning).** Does this operation change what already-existing files *mean* — their
> validity, their type, or how a query renders them — **without writing them**?

`vault_restructure` is C-A. `vault_configure` is C-B. `vault_edit` is neither, and its one
accepted exception is `.seq` (FR-036a).

**FR-070d — NEW, revision 5. Both criteria MUST be applied to every operation IN WRITING, and the
verdict recorded per operation.** ADR-068 D15.1 says revision 5's defect was *"switching readings
between two rows of the same table"* and that naming both criteria fixes it. **Revision 4 named both
criteria and then applied only C-A to `link`, to `create` and to `trash`** — the identical fault, one
layer up. The table below is that record, and it is normative. **Where an operation stays in a tier
its C-B answer argues against, the exception is STATED — as `.seq` is a stated exception to C-A —
never left as an omission.**

| Operation | Tool | C-A (bytes)? | C-B (meaning)? | Verdict |
|---|---|---|---|---|
| `set_property`, `append_section`, `replace_body` | `vault_edit` | no | no | tier 4, uncontested |
| `create` | `vault_edit` | **yes, `.seq`** | **YES — and revision 4 never asked** | **tier 4 by STATED EXCEPTION.** C-A is answered by FR-036a. **C-B is answered YES and the exception is new here:** FR-033 reports a relation whose target does not exist as a validation finding, so creating a note at a path an existing record links to **silences that finding on a note the agent never named** — its validity changes. That is C-B by the identical move that reclassified `create_record_type`. **It stays in tier 4 anyway**, and the ground is *bounded, monotone repair*: the only meaning-change a `create` can produce is turning a dangling relation into a resolved one, which is a **strict improvement in exactly one direction**, affects only notes that already named this path, and is undone by trashing the note. `create_record_type` is the opposite on all three counts — it can invalidate hundreds of notes, reaches notes that named nothing, and its cascade is invisible in the diff. **The exception is bounded by that reasoning and does not generalise**: an operation whose meaning-change can make an existing note *worse* is C-B and belongs in `vault_configure`. |
| `link` | `vault_edit` | no — writes the source only (FR-030) | **YES — and revision 4 answered only C-A** | **tier 4 by STATED EXCEPTION.** Revision 4's defence (*"it looks like a two-file operation and is a one-file operation"*) answers C-A and nothing else. C-B: FR-032 makes the inverse **derived**, so after the link `vault_find near="Acme"` and "the company's related deals" return a different answer for a note nobody named — that is *how a query renders an existing file*. **It stays in tier 4 on the same bounded-monotone-repair ground as `create`**, plus one that is specific to it: the derived inverse is **a view of the source file**, not a state of the target, so the target's meaning is unchanged and only its *rendering under a derived query* moves. **An operator who wants to withhold this withholds `vault_edit`**, and FR-079 requires `vault_edit`'s description to say that linking changes what queries return about the target. |
| `rename`, `move` | `vault_restructure` | **YES** — inbound wikilinks in N notes are rewritten | yes, consequentially | tier 5, uncontested. These are the genuine C-A cases. |
| `trash` | `vault_restructure` | **NO** — it moves **one** file and FR-048 explicitly does **not** repair inbound links, so it writes bytes into no file the caller did not name | **YES** — it breaks N existing notes' relations without writing them | **PLACEMENT CONFIRMED IN TIER 5, and revision 4's reasoning REPLACED.** Revision 4 said *"Recoverability and blast radius are different axes"*, which answers a question nobody asked and never applies either criterion. Under the criteria as written, **`trash` is C-B and FR-070b would send it to `vault_configure`.** It stays in `vault_restructure`, and the ground is stated rather than assumed: **`vault_configure` is the CONTROL PLANE — it writes `.omnipus-vault/` and nothing else** (see FR-070e), and `trash` moves a **note**. Putting a note-destroying operation behind the schema tool would mean an operator who grants type authoring also grants deletion, which is a worse posture than the one being fixed. **So the tier is not "C-A only"** — revision 4's framing of `vault_restructure` as *"the C-A tier"* is withdrawn: it is **the tier for operations that reach beyond one named note**, of which C-A is the common but not the only shape. |
| every `vault_configure` op | `vault_configure` | no — one file, inside `.omnipus-vault/` | **YES** | tier 6, uncontested |

**FR-070e — NEW, revision 5. A mechanical criterion is adopted ALONGSIDE the two semantic ones,
because the semantic pair has now mis-decided three operations across two revisions.** *(This is
the grill's O-1, and it is right.)* **Writing into `.omnipus-vault/` is `vault_configure`; writing a
note is not.** It is one path-shaped rule, testable in CI over the emitted write path, and it decides
`link`, `create` and `trash` without argument — the three the semantic criteria mis-decided.

**CORRECTED, revision 6 (review round 6, M-42) — revision 5 said the rule *"has exactly one
exception"* and the count was WRONG, on the fourth operation, in the rule adopted BECAUSE the
semantic criteria mis-decided the first three.** FR-048 puts trashed notes at
**`<vault>/.omnipus-vault/trash/<timestamp>/<path>`**, and `trash` is a **`vault_restructure`**
operation; FR-048a's `restore` reads out of the same directory and writes back into the vault. So
`vault_restructure` writes into `.omnipus-vault/` **on every trash**, §4.1.5's *"`vault_configure`
writes **only** `.omnipus-vault/`"* is false as written, and
`TestTools_ConfigureWritesOnlyVaultControlPlane` (test 68) would have met it on the first run.

**The rule is still the right rule — trash genuinely belongs where it is — and it is restated so
the mechanical test is EXPRESSIBLE:**

> **The rule governs the CONTROL PLANE, which is `.omnipus-vault/records/` and
> `.omnipus-vault/views/`.** Writing into either is `vault_configure`. Two paths inside
> `.omnipus-vault/` are **named non-control-plane** and are written by other tools:
>
> | Path | Written by | Why it is not control plane |
> |---|---|---|
> | `.omnipus-vault/records/<type>.seq` | `vault_edit` (FR-036a) | An identifier counter is bookkeeping about records, not a definition of one. **It is inside `records/` and is the one genuinely awkward case** — m-9's note stands |
> | `.omnipus-vault/trash/**` | `vault_restructure` (FR-048, FR-048a) | Trash is note storage that happens to live out of the way. It defines nothing and changes no note's validity |
>
> **Two named exceptions, not one**, and the test asserts exactly this partition. **The semantic criteria are not replaced**
— they remain the reviewer's tool for a *new* operation, where the mechanical rule can only classify
something already designed — but where the two disagree, **the mechanical rule decides, and the
disagreement is recorded in the table above.** *(m-9 is noted and accepted in the same breath:
because `.seq` sits inside the control-plane directory, `vault_edit: allow` + `vault_configure: deny`
still permits a write into `.omnipus-vault/records/`. It is a monotonic counter with no readable
content — FR-036a's whole argument — and the alternative is a second exception directory, which is
worse.)*

- **FR-016** **MEANING CHANGED, revision 4 (ADR-068 D15.6).** Creating a **new** record type MUST be an operation of **`vault_configure`**, and the response MUST name the count of pre-existing notes that have just become validated records as a result. *Previously: an operation of `vault_edit`, on the stated ground that a new schema file changes no existing note's meaning.* **That ground was false under ADR-068 D1**: a note becomes a record by declaring `type:` in frontmatter, and a note whose declared type matches no schema is an ordinary note (FR-005). Writing `.omnipus-vault/records/company.yaml` therefore converts **every pre-existing note already carrying `type: company`** into an indexed, queryable, validated record — and any of them missing a `required: true` property becomes a validation finding nobody asked for. That is **C-B**, and it is invisible in the diff: one new twelve-line YAML file, hundreds of notes changed in meaning.
- **FR-017** **MEANING CHANGED, revision 4.** Changing or deleting an **existing** record type MUST be an operation of **`vault_configure`**, because FR-015 makes it reinterpret every existing record of that type. *Previously: `vault_restructure`.*
- **FR-018** **MEANING CHANGED, revision 4.** Creating or editing a **saved view** MUST be an operation of **`vault_configure`**. *Previously: `vault_edit`.* A view writes no note, but it changes what a query returns, which is **C-B in its weakest form** — and a view is part of the same control plane as the schema, so an operator granting one grants the other knowingly.
- **FR-018a** `vault_configure` MUST NOT declare an `expect_version` **parameter**, and a test MUST assert its absence (ADR-068 D15.5c, AC-15.5d). A single-file content hash cannot honestly guard a change whose blast radius is every note declaring the type. Its safety is policy, plus the audit entry (FR-077), plus `check_integrity` (FR-075) — never optimistic concurrency it cannot honour. **REVISION 5 ADDS THE HALF THAT WAS MISSING:** removing the parameter removed the protection a token *can* give and put **nothing** in its place, leaving a check-then-write race on the file that defines the type system. **FR-043a supplies an internal `O_EXCL`/content-hash CAS that guards THE FILE and explicitly not the cascade** — which is honest, is invisible to the caller, and is not the thing this FR rejects.
- **FR-019** The system MUST NOT add any agent-callable mount operation. Mounting a folder stays `request_mount` (`pkg/coreagent/core.go:367`), seeded `ask` everywhere (ADR-063 FR-7.2). A second mount path would route around a control ADR-063 deliberately placed.
- **FR-019a** No policy default may be presented as a prohibition. `vault_restructure` **and `vault_configure`** are seeded `ask` across the base roster, and an operator MAY set either to `allow` — including for schema changes. A conservative default is not a ban (ADR-068 D23.5).

### Index and query

**UNBLOCKED, revision 3.** ADR-068 D16 is resolved (spike report:
`../design/adr068-storage-spike-2026-08-25.md`). The design is **two indexes**: bleve keeps
text; a **derived, disposable properties index in pure-Go SQLite** holds typed properties,
relations and derived child tables. `modernc.org/sqlite v1.46.1` is already a direct dependency
(`go.mod:64`), linked into the binary today for WhatsApp and Matrix session storage — so this is
no new runtime dependency, no CGo, and Hard Constraints #1 and #2 hold.

- **FR-020** **MEANING CHANGED, revision 3 — was BLOCKED, is now specified.** Typed properties, relations and their derived child tables MUST be persisted in a **SQLite properties index**, and a query MUST select candidates by record type and property without materialising documents that cannot match. *Cited by §6 traceability and the §7 regression block; both are restated.*
- **FR-020a** **MEANING CHANGED, revision 3.** The properties index MUST be **derived and disposable**: deleting it and reopening the vault MUST rebuild it from the notes, and MUST yield **identical query results**. Nothing may exist in SQLite that is not reconstructible from Markdown. *(Previously this FR was the bleve-mapping no-op guard; that guard survives as FR-020d.)*
- **FR-020b** **REWRITTEN, revision 5 — the guarantee survives, its subject changed.** A **`decimal`** value MUST never be converted to a binary floating-point number anywhere in the parse, storage, comparison, ordering or aggregation path, and an **`integer`** MUST never be widened to a float. *Previously about `money`, storing integer minor units with currency and scale in their own columns (ADR-068 O-2). `money` is deleted (FR-014) and O-2 is superseded; **the no-binary-float rule is the part worth keeping** and it is the one this project already enforces — `pkg/records/decimal.go` is `math/big`-based with zero `float64` in executable code, asserted by `pkg/records/decimal_no_float_test.go`.* **One consequence is easy to miss:** SQLite's `total()` returns a REAL `0.0` where `SUM()` returns NULL over an empty set, so **`total()` is not an acceptable substitute** — though under ruling R-A neither is emitted, since aggregation is Go-side.
- **FR-020c** **MEANING CHANGED, revision 4 (ADR-068 D16.5) — the token is SPECIFIED, and it is per-note.** *Previously this FR said the two indexes carry **the same freshness token**, which read as a description of something that existed. **It did not exist.** `manifestVersion` (`pkg/knowledge/manifest.go:48`) is a struct-schema constant a human bumps when the recorded shape changes (`manifest.go:45-47`); a mismatch discards the manifest and rebuilds (`manifest.go:113-115`). No query result carries anything of the kind — `IndexHit` is exactly `Path`/`Kind`/`Score`/`Offset`/`Segment` (`pkg/knowledge/index.go:159-173`). **`VersionToken` is `pkg/knowledge/version.go:101`** — `type VersionToken string` — a per-note compare-and-swap token on the **authoring** path, consumed by `checkVersion` (`author.go:667`) and never attached to a read; `NoteContentVersion` (`pkg/knowledge/author.go:322-324`) is the helper that computes one. **Revision 4 cited `author.go:309-323` for `VersionToken` and that was wrong twice over** — wrong file for the type, and wrong lines for the helper (`:306-321` is its doc comment). The distinction between the authoring CAS token and the freshness token is the whole of this FR's argument, so citing the wrong symbol weakened exactly the point being made.* The requirement is now:
  - **The token VALUE is the note's content SHA-256** — the same value `ManifestEntry.Hash` (`pkg/knowledge/manifest.go:64`) already holds, written per note on every index and keyed by collection-relative path in `Manifest.Entries` (`manifest.go:82`). Not an integer, not a generation counter. *(Revision 5 corrects the citation for `Manifest.Get`: it is `manifest.go:175`, not `:174`, which is its doc comment. The value claim is unchanged and was the one part of revision 4's FR-020c that checked out.)*
  - **Every record row MUST carry a `source_hash` column**, written in the same transaction as the row, holding the hash the indexer computed for that note in that pass.
  - **Per note, deliberately, not per index.** A whole-index generation would report *every* answer stale while any agent writes anywhere in the vault, which turns the problem channel into noise — FR-121's verdict stops being read at all. Per-note flags only the notes actually mid-write.
  - **The comparison is per RETURNED HIT, not per candidate:** for every hit `vault_find` is about to return, compare the row's `source_hash` against the hash the text index holds for that path. **Equal** → the two indexes have seen the same bytes. **Unequal, or the entry is missing, or its hash is empty** → the record enters `problems` with staleness as the reason and the response is `complete: false`. Bounded by the page size (FR-063).
  - **WHERE the comparison side comes from is respecified in revision 5, because revision 4 asserted a mechanism that does not exist.** Revision 4 said the cost is *"one in-memory map lookup per hit"* against *"the live `Manifest`"*. **There is no live `Manifest`.** Verified: `Index` (`pkg/knowledge/index.go:350`) holds `idx`/`dir`/`blevePath`/`manifestPath`/`root`/`mu`/`regKey` and **no manifest field**; `LoadManifest` (`manifest.go:101`) has exactly **two** production call sites, `index.go:751` inside `SyncWith` and `drift.go:218` inside `CheckDrift`, both of which bind it to a function-local and drop it on return; and `Manifest` (`manifest.go:74`) has **no mutex** — `Get` (`:175`), `Put` (`:181`) and `Remove` (`:189`) are bare map operations over `Entries` (`:82`). So the budgeted lookup is neither in memory at query time nor safe to share with a concurrent indexer. **This was the fourth wrong storage assumption in this document's history, and it was inside the FR written to fix the third.**
  - **The mechanism is therefore respecified, and it removes the manifest from the query path rather than putting it there.** **The hash rides on the bleve document as a stored field.** FR-111 already reopens `indexDoc` — a closed five-field struct (`pkg/knowledge/index.go:583-589`) — to add title, name, headings, property keys and property values, so adding a sixth field `source_hash` with `Store: true` and retrieving it on search is **the same work already scheduled for W2**, not new machinery. The comparison then reads two values that both arrive with the hit: the SQLite row's `source_hash` and the bleve document's. **No shared mutable state, no lock discipline, no manifest parse, and the thing being compared is the thing the requirement is about** — whether the two indexes have seen the same bytes — rather than a proxy for it through a third store.
    - **What must be built, named as new work:** the `source_hash` field on `indexDoc` and its mapping entry; `Store: true` on it; retrieval of stored fields on the search path (`IndexHit` gains the value, or the search request asks for the field); and the SQLite-side column. **The cost:** 64 bytes of hex per document in the bleve index, and a stored-field fetch per returned hit. **Nobody has measured either** — it joins A-12.
    - **The manifest-based variant is recorded as the alternative and NOT taken**: it would need a `*Manifest` cached on `Index`, a `sync.RWMutex` on `Manifest` that does not exist today, and a lock order against `Index.mu` that nobody has designed. It is strictly more work than the stored field and gets a weaker answer.
  - **A-13 (LIVE): the stored-field mechanism is designed, not verified.** Whether bleve returns a stored field cheaply enough on this hit path, and whether `Store: true` on a 64-byte field at 100,000 documents is acceptable against FR-020e's rebuild, are **open**. They are answerable by W2's fielded-indexing work, which touches the same struct. **This is carried as a stated open risk rather than closed with a fifth assumed mechanism** — a reader must not take FR-020c's freshness comparison as a settled property until W2 reports.
  - **Write ordering, RE-DERIVED in revision 5, because revision 4's made the reachable failure undetectable.** Revision 4 specified bleve → SQLite → manifest, manifest last, and claimed both directions were caught. Trace it: a failure **after bleve and before SQLite** leaves the SQLite row and the manifest **both at the old hash**, so they compare **equal** and the answer is reported `COMPLETE: yes` over a stale row. That is the reachable case, and it was the undetected one; the two cases revision 4 described are each unreachable under its own ordering. **The ordering is now: SQLite row (with the new hash) FIRST, then the bleve document, then the manifest.** Under it every partial failure is detected:

    | Failure point | SQLite row | bleve / manifest | Compare | Detected? |
    |---|---|---|---|---|
    | before the SQLite write | old | old | equal | **not a divergence** — nothing was written |
    | SQLite write fails, later steps proceed | old | new | differ | **yes** |
    | after SQLite, before bleve | new | old | differ | **yes** |
    | after bleve, before manifest | new | new (bleve) | equal | **yes for the comparison that matters** — the two indexes agree; the manifest lags and `SyncWith` re-indexes on a missing entry |
    | all three complete | new | new | equal | correct |

    **The cost of putting the writer that can fail first is false positives, and that is the direction to err in.** A record flagged "possibly stale" when SQLite is actually *ahead* of bleve is a caveat on a correct answer; a record reported fresh when it is stale is the failure this document exists to remove. **The reason string MUST therefore say "the two indexes disagree", not "the properties index is stale"** — the comparison establishes disagreement, not which side is behind, and claiming the second would be a precision the mechanism does not have.
  - **A TRASHED record's row is NOT an integrity finding, and this had to be ruled (revision 6, review round 6, unasked question 7).** A trashed note has no live manifest entry, so its properties row is orphaned and FR-020c flags it and `check_integrity` reports it as an orphaned row. **But trash is a normal, expected state with a 30-day retention** — so every trashed record would generate a **permanent** integrity finding until purge, which is 30 days of noise in the exact channel FR-121's verdict depends on being read. **Ruling: the indexer DELETES a record's properties row when its note moves to `.omnipus-vault/trash/`, in the same pass that notices the move.** The row is derived and disposable (FR-020a); there is nothing to preserve. A `restore` re-indexes and the row returns. **An orphaned row is therefore a genuine integrity finding again** — it means a note vanished without going through `trash`, which is exactly the condition the finding is for. *(The alternative — keeping the row and suppressing the finding — was rejected: a suppression rule is a second thing to get wrong, and it would make `check_integrity`'s output depend on a state the reader cannot see.)*
  - **An empty hash is unknown freshness and MUST be flagged, never assumed fresh.** `ManifestEntry.Hash` is deliberately empty for attachments (`manifest.go:62-65`, because **ADR-067's** FR-039a forbids opening one and hashing is opening — `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md:1918`, and the code comment at `manifest.go:62-64` cites it by that bare number too. *(Revision 6, review round 6, M-36: the review is **right that this document defines no FR-039a** and the bare citation was unresolvable from here — and **wrong to imply the citation is dangling**. It resolves, in the ADR-067 specification's own FR namespace, where FR-039a reads "the indexer MUST NOT open an attachment". Two documents sharing an `FR-nnn` namespace is the real defect; the fix is to qualify the namespace at every cross-document citation, not to invent a local FR-039a.)*). Records are notes, so this should not arise; the rule is stated for the case where it does.
  - **A flagged record MUST be re-queued** for re-indexing, so a divergence does not stay flagged. **BOUNDED, revision 5:** the re-queue MUST be **deduplicated and debounced** — at most one outstanding re-index request per note path, and a per-note cooldown of **60 seconds** between requeues. A note under active editing diverges on every query, and an unbounded re-queue turns every query of it into an indexing job.
  - **A row with no manifest entry at all** — the note was deleted, the row orphaned — MUST be flagged and MUST be reported by `check_integrity` as an orphaned row (FR-075).
- **FR-020c1** **The residual hole is named and accepted for W1.** FR-020c detects divergence **for records the query returned**. It does **not** detect a record that is stale *and excluded from the result by a stale predicate* — if the properties index holds `status: prospect` for a note whose file now says `status: churned`, a query for `status = churned` never returns that row and so never compares its hash. Closing it means hashing the whole candidate population rather than the returned page, which is the cost FR-064's cap exists to avoid. It is bounded by re-indexing on the next sync and by `check_integrity`'s explicit sweep. **The documentation MUST state that a completeness verdict covers what the query returned, not what it did not** — an operator who is not told this will read `COMPLETE: yes` as a stronger claim than it is. **THE OBLIGATION NOW HAS A HOME, revision 6 (review round 6, M-29): revision 5 required this of "the documentation" and no FR assigned it to a surface — not `vault_find`'s tool description (FR-079's ~150-token budget is already spoken for), not `vault_describe`, not the response header. AC-F4 states a SECOND unstated-exception carve-out (scope) in the same spirit and put it nowhere either. Both go in ONE place, named here and in AC-F4:** the **`COMPLETE:` line's own reference documentation** — the operator-facing page that explains what the verdict means — carries **both** exceptions verbatim, and `vault_find`'s tool description carries a **one-clause pointer** to it (*"completeness covers what was returned; see the reference for the two exceptions"*), which is what the token budget can afford. §7 test 71's sibling asserts that the reference page contains both clauses, so the obligation is checkable rather than aspirational.
- **FR-020d** An index created before record support exists MUST NOT be opened and queried for fields it cannot hold. Two guards are required, and the second exists because the first depends on a human remembering: **(G1)** an index **format version** that forces a rebuild, and **(G2)** an assertion that the **persisted mapping** actually contains the fields the query planner will use. `bleve.OpenUsing` takes no mapping argument and the mapping is persisted in the store, so re-running the same code against an existing index silently returns zero hits with `err == nil` — the spike **reproduced** this (1 hit on a fresh index, 0 and no error on an existing one).
- **FR-020e** Existing bleve indexes MUST be **rebuilt**, not opened, on upgrade. `bleve/v2 v2.6.1` (`go.mod:12`) and `zapx/v17 v17.2.3` (`go.mod:107`) fix the segment corruption the spike found at 100,000 documents, but **segments already written stay corrupt** — a version bump alone does not discharge it. *(Revision 4, ADR-068 D20: this ships in **W0**, ahead of and **independently of** everything else in this specification. The panic is `slice bounds out of range` **unrecovered** through `indexImpl.Search`, which in the gateway is a process crash, and it reproduces with the shipping mapping and no record fields at all — it is an ADR-067 defect this work merely tripped over. Revision 3 left it inside the record waves, behind schema files and a new storage engine. Hard Constraint #7 does not admit a stylistic reason for leaving a live crash in the field behind a design wave.)*
- **FR-020f** **Index state MUST be queryable, not only broadcast.** The system MUST expose a request-response path returning the current indexing state of a collection — phase, indexed count, total, and the freshness generations FR-020c compares — in **the same shape a live progress frame carries**. Today progress is live-only: `KnowledgePanel.tsx:226` reads exclusively from `useKnowledgeIndexStore`, which is populated only by incoming `knowledge_index_progress` WS frames, and no REST endpoint returns index state. (`index_state` at `pkg/gateway/rest_knowledge.go:405` is a coarse string riding on a **search** payload — `ready` / `not_built` / `unavailable`, used at `rest_knowledge.go:534-535,620` — not a queryable snapshot, and it carries no phase or counts.) The consequence is that a client which connects **after** an index finished renders "no progress has arrived" for a completed index, which is what a human sees for any fast index. The frames are emitted correctly at both edges — a leading frame before `syncWith` runs (`pkg/gateway/knowledge_lifecycle.go:836`) and an unthrottled terminal `progress.flush` (`pkg/knowledge/index.go:829`) — so **this is not an emission defect and MUST NOT be fixed by changing emission** (ADR-068 §2.6 investigated the emission theory and did not confirm it).
- **FR-020i** **NEW, revision 5.** A test MUST assert the **linked SQLite version** by opening a database through `modernc.org/sqlite` and reading `sqlite_version()`. The expected value at this revision is **`3.51.2`** for the pin at `go.mod:64` (`modernc.org/sqlite v1.46.1`), verified by execution. Affinity, collation and `NULLS LAST` behaviour are version-sensitive, and §8.1's whole defeat table is a set of claims about one engine's semantics; a driver bump that changes them must fail a test rather than change the product's answers quietly. *(The test asserts the version it was written against and is expected to be updated deliberately on a bump, together with re-running §8.1b's receipts. That is the point: the update is the review trigger.)*
- **FR-020j** **NEW, revision 5 — "index generation" was used in five normative places and defined in none, and FR-020c explicitly rejects the concept it was reaching for.** The concept is defined here, **narrowly**, and its relationship to FR-020c is stated so the two cannot be confused:
  - **An `index_epoch` is a monotonic per-collection integer, stored beside the index, incremented ONLY on a structural change**: a full rebuild (FR-020a, FR-020e), a schema create/edit/delete (FR-015, FR-017), or a note leaving the index (FR-048's trash). **An ordinary note re-index does NOT bump it.**
  - **Its ONLY uses are cursor validity (§4.1.2's stale-cursor refusal), trash's index removal (FR-048), and FR-020g's agreement check.** It is a stability token for *paging and structure*, not a freshness signal.
  - **It MUST NOT be used to decide `complete`.** FR-020c's argument against a per-index generation is about **reporting**: a whole-index counter would mark every answer stale while any agent writes anywhere. That argument is about freshness and does not reach cursor validity, where an epoch is the right primitive precisely because it changes rarely.
  - **`manifestVersion` is not this** (`pkg/knowledge/manifest.go:48`, a struct-schema constant a human bumps), and neither is anything else that exists today. **This is new work.**
- **FR-020g** The agent-facing and human-facing views of index state MUST NOT diverge. `vault_find`'s header (FR-020c) and the snapshot FR-020f requires MUST be computed from **one** source; a test asserts the two agree for the same collection at the same generation.
- **FR-020h** **NEW, revision 4 (ADR-068 D16.2a). On a build without SQLite, the record layer MUST REFUSE BY NAME. It MUST NOT return an empty result, and it MUST NOT fail to build.** `modernc.org/sqlite` cannot compile on three targets, each already documented in the tree: `linux/mipsle` softfloat (`pkg/gateway/channel_matrix.go:20-22`), `netbsd/*` (`channel_matrix.go:23-25`) and `freebsd/arm` (`channel_matrix.go:26-28`). Both existing consumers are gated against exactly that set — `pkg/channels/whatsapp_native/whatsapp_native.go:1` and `channel_matrix.go:1`. Three consequences are normative:
  - **`-tags lite` KEEPS records.** `lite` drops whatsmeow only; Matrix is not `lite`-gated, and `make build-lite` builds `$(GO_BUILD_TAGS),lite` = `goolm,stdjson,lite` (`Makefile:205-213`), so SQLite is still linked. This MUST be stated in the build documentation, because a reasonable reader assumes the opposite.
  - **Exactly one SHIPPED binary lacks SQLite: `linux/mipsle`**, the only Makefile target built with `GO_BUILD_TAGS_NO_GOOLM` (`Makefile:210`, `:234`). `netbsd` and `freebsd/arm` are not shipped by `make build-all`; the exposure there is a person compiling from source.
  - **The degradation is a build-tagged stub that registers and refuses**, following `pkg/channels/whatsapp_native/whatsapp_native_stub.go`. `vault_read` and the plain-text half of `vault_find` keep working, because they resolve through bleve. Typed filters, relation joins, grouping and aggregation MUST each return a refusal naming the platform. **An empty result here would be the exact failure this specification exists to remove**, arriving from the build system.
- **FR-021** **REVERTED to its original meaning, revision 5 (operator ruling R-A). The spec was right the first time.** Filtering, grouping, joining and aggregation MUST be evaluated **in Go**, by the comparator that implements §8's R-1..R-13, over the candidate set the properties index returns. **The properties index NARROWS; the comparator DECIDES.** *Revision 3 moved this into the properties index ahead of ADR-068, and ADR-068 D16.2b then made "the properties index answers every typed predicate" normative. Both are now reversed. §8.1 records the eleven SQLite-semantics defeats this deletes and why deleting the class beats mitigating it.* **Three consequences are normative:**
  - **What SQLite still answers:** set-membership over indexed columns — `type = 'deal'`, `path` prefix within scope, `kind = 'task'`, the relation child table's `rec_id` join for **reachability**. None of these is a comparison governed by R-1..R-13. FR-020's "select candidates without materialising documents that cannot match" is unchanged.
  - **What moves back to Go:** every operator in FR-022b's vocabulary, every grouping, every aggregate, **every SORT**, and the `many`-arity fan-out. **Sorting is named explicitly, revision 6 (review round 6, M-7): revision 5's list omitted it**, and four other places then assumed SQLite did it (FR-021b's *"at `ORDER BY` time"*, FR-066b's `GROUP BY` result set, SC-007's temp-b-tree budget, and R-5 / FR-010 / §4.1.2's *"SQLite's own ordering"*). `sort` is a first-class `vault_find` parameter and **ordering IS comparison** — it is governed by R-1, R-4, R-5, R-7 and R-13 like every other comparison, and an omission from this list is how a ruling gets quietly half-applied. **FR-066b's one-page-in-memory rule therefore binds harder, not softer** — a Go pass over a candidate stream must stream, and it is asserted by measured peak RSS at the cap, not by inspection.
  - **The tokenizer hazard (FR-116) widens back to its original scope.** ADR-068 D21.5 was re-derived in revision 6 on the narrow ground that only *rank fusion* stayed in Go. With membership in Go too, bleve selects with one notion of a term while Go both ranks **and matches** with another. FR-116 is unchanged in text and stronger in consequence.
  - **The cost is stated: a query matching very many records is slower, and the cost scales with candidates rather than results.** FR-064's 10,000-candidate cap is the bound, and it was set for exactly this reason (FR-066a). **Nobody has measured the Go path over the two-index design — A-14 carries it, and W1 measures it.**
- **FR-021a** Every value the properties index holds MUST be **typed at write time** against the record's schema. A value that does not conform MUST be stored as non-conforming and flagged, never coerced into the typed column, so that R-4 can report it rather than compare it.
- **FR-021b** **NEW, revision 5 — FR-021a as written collides R-4 with R-2/R-3 and this closes it.** "Never in the typed column" leaves **NULL** in that column, and NULL is the **absence** representation (FR-007), so a non-conforming value and an absent property become indistinguishable in storage. Every typed property MUST therefore carry a **conformance flag column** with three distinguishable states — **present-and-conforming**, **present-and-non-conforming**, **absent** — and that flag MUST be consulted **at comparison time and at SORT time — both of which are in Go** (FR-021, R-5), not only when the problem list is built. *(Revision 6, review round 6, survivor 3: revision 5 said "at `ORDER BY` time", which named a SQL clause this design does not emit.)* A non-conforming value MUST NOT be ordered as though it were absent, and an absent value MUST NOT be reported as a problem.
- **FR-021c** **NEW, revision 5.** Parsing a value into its typed column MUST happen **in Go, before insert** — never by a SQL function on the way in. Two of SQLite's parsers return **NULL with no error** on malformed input (`unixepoch('not-a-date')` and `unixepoch('2026-8-26')`, both verified, §8.1b *(revision 6, M-38: "§8.1a §E" named a lettered subsection §8.1a has never had)*), and one **saturates silently** (`CAST('9223372036854775808' AS INTEGER)` → int64 max). Any of the three would write an FR-021b non-conformance into the storage cell reserved for absence, defeating FR-021b in the same transaction that FR-021b exists to protect.
- **FR-021d** **REWRITTEN, revision 5 (operator rulings R-A and R-H). The storage requirement is withdrawn; a PARSING requirement replaces it.** *An earlier draft of this revision required a signed integer epoch column, because R-7 had no defeat and SQLite's TEXT date ordering is wrong four ways. Under ruling R-A no date comparison reaches SQLite, so the storage form is not load-bearing and the requirement is withdrawn.* **What replaces it is strict ISO-8601 parsing, in Go, with everything else reported as a bad value:**
  - **Accepted: `YYYY-MM-DD`** (a day) **and `YYYY-MM-DDThh:mm[:ss[.sss]]` with an explicit `Z` or `±hh:mm` offset** (an instant). Zero-padding is mandatory.
  - **Rejected and reported as a bad value with the fix named** (FR-021b, R-G): `2026-9-1` — *"month and day must be zero-padded — write 2026-09-01"*; `03/04/2026` — *"the date format is ambiguous and will not be guessed; write 2026-04-03 or 2026-03-04"*; a bare `hh:mm`; a date-time with no offset.
  - **No normalisation of ambiguous formats, ever.** `03/04` cannot be resolved without knowing a locale we do not have, and a system that guessed would produce a wrong answer with no error channel — the exact failure this document exists to remove. **A date we cannot parse is a reported problem, not a best effort.**
  - **A day and an instant compare directly** (R-7): a `date` with no time is the instant at `00:00:00Z` of that day for ordering, and equality between a day and an instant is `true` exactly when the instant falls within the day. *(That asymmetry is deliberate and is stated because it is the kind of thing two implementers decide differently: "is `2026-09-01` equal to `2026-09-01T14:30Z`?" — yes, because the coarser value is the question being asked.)*
  - **Parsing is never delegated to SQL** (FR-021c): `unixepoch('not-a-date')` and `unixepoch('2026-8-26')` both return **NULL with no error**, which would write a parse failure into the storage cell reserved for absence.
- **FR-022** **`vault_find`** MUST accept a structured filter object; the system MUST NOT accept a text query language (ADR-068 O-3, unchanged in substance — only the tool name moved).
- **FR-022a** **NEW, revision 5.** A `LIKE` predicate whose value is the **empty string** or **`'%'` alone** MUST be refused, naming `IS NOT NULL` as the operator that was probably meant. A pattern that matches every value is a whole-table result returned as though it were a filtered one — **and the justification is now ENGINE-INDEPENDENT (revision 6, m-2): a pattern of `''` or `'%'` matches every value, which is true of `LIKE` in any implementation.** *(Revision 5 justified the rule with a SQLite receipt — `instr('abc','')` and `instr('','')` both return `1` — for a rule now enforced in Go by the comparator. The rule was right; its evidence pointed at the wrong engine.)* FR-022d's empty-`IN` refusal is its sibling (M-13).
- **FR-022b** **NEW, revision 5 (operator ruling R-B). The filter's operator vocabulary is SQL's, not ours.** The permitted operators are exactly **`=`, `<>`, `<`, `<=`, `>`, `>=`, `LIKE`, `IN`, `IS NULL`, `IS NOT NULL`**, and each carries **SQL's own meaning**. *Revision 4 invented `eq` / `lt` / `lte` / `gt` / `gte` / `contains` / `is_absent` (`pkg/records/filter.go:83-93`); every one of them is replaced.*
  - **The filter remains a STRUCTURED OBJECT. There is no parser, no text query language and no `WHERE`-clause string.** Only the operator vocabulary changes: `{property: "tags", op: "LIKE", value: "vend%"}`. **ADR-068 O-3 is AMENDED, not overturned** — both halves of its resolution (structured JSON; no parser) still hold, and the amendment is recorded in the ADR rather than left to read as a contradiction.
  - **The argument is retrieval accuracy, not style.** Our vocabulary has appeared in a model's training data **zero times**; SQL's has appeared an enormous number of times. A model choosing from a name it has never seen is guessing; a model choosing `LIKE` is recalling. **That is the same class of argument as FR-072's compact-text rule** — the response format moved accuracy 93.1% → 55.2% in the cited study — applied to the request side.
  - **`%` and `_` are `LIKE`'s wildcards and mean what they mean in SQL**, with `\` as the escape character. **This is the reason `contains` is gone rather than renamed:** `contains` had one behaviour and `LIKE` has two — exact when the pattern has no wildcard, partial when it does — and the caller chooses, which is what revision 4's R-9/R-13 argument was trying and failing to give them.
  - **A WILDCARD-FREE `LIKE` IS EXACTLY `=`. STATED AS AN EQUIVALENCE IN DRAFT 7 (UAT case F-2.4), because "anchored" was stated and the equivalence was not, and the equivalence is what a test writer and a tool description both need.** `label LIKE 'Bracken'` selects **exactly** the records `label = 'Bracken'` selects, and in **every** respect — the same case-insensitive fold (FR-011a), the same element-wise behaviour on a `many` property (R-9), the same `false` when the property is absent (R-2), the same `false` when the value does not conform to its declared type (R-4). There is **no** case in which the two disagree.
    - **Why this is the answer, recorded so nobody "helpfully" makes it substring later.** In standard SQL, `x LIKE 'v'` with no wildcard **is** `x = 'v'`. That is the entire point of ruling R-B's adoption of SQL's vocabulary: **the model already knows this**, and a `LIKE` that quietly meant substring would be the one place our vocabulary diverged from the one it was chosen to borrow — and it would diverge **silently**, returning a superset that looks like a working answer. `contains` was deleted precisely so the caller could choose; giving the wildcard-free form `contains`'s old meaning would restore the ambiguity under a new name.
    - **"Wildcard-free" is decided AFTER unescaping, and that is not a pedantic distinction.** `\%` and `\_` are **literals**, not wildcards. `label LIKE 'a\%b'` has no active wildcard and is therefore equivalent to `label = 'a%b'`. **An implementation that fast-paths on `strings.ContainsAny(pattern, "%_")` gets this wrong**, classifying an escaped literal as a wildcard; the test must be for an **unescaped** `%` or `_`. *(A general pattern matcher with no fast path is correct either way. The rule is written for the optimisation someone will add.)*
    - **UAT F-2.4 asks the tester to record what a bare `LIKE` does and then check that the tool's own description says the same thing.** §4.1.2's operator table is that description, and it MUST carry this clause — it did not, which is the actual defect the case found: the rule was in FR-022b and the surface a caller reads was silent.
    - **AC-8.11** — `'Bracken' LIKE 'Bracken'` is **true**; `'Brackens' LIKE 'Bracken'` is **false**; `'bracken' LIKE 'BRACKEN'` is **true**; `'a%b' LIKE 'a\%b'` is **true** and `'axb' LIKE 'a\%b'` is **false**. **And the discriminating criterion:** for a corpus with no wildcards anywhere, the result set of every `LIKE` leaf is **byte-identical** to the same leaf rewritten with `=`. A substring implementation fails the second and fifth cells and the criterion.
  - **`LIKE` IS ANCHORED — it matches the WHOLE value, as SQL's does. NEW, revision 6 (review round 6, M-14), and it is the single most likely implementation divergence in this FR.** `'vendors' LIKE 'vendor'` is **false**; `'vendors' LIKE 'vendor%'` is **true**. Revision 5 said `%` and `_` mean what they mean in SQL and **never said the match was whole-value**, while R-9's phrasing — *"`LIKE` matches an element **by pattern**"* — reads like substring matching to anyone who has just deleted a `contains` operator. **A wildcard-free pattern is therefore an exact (folded) match, which is the property FR-022a's empty-pattern refusal depends on.** DS-4 carries `'vendors' LIKE 'vendor'` → false as a literal case.
  - **How folding composes with the pattern — NEW, revision 6 (M-14), because it is not free.** The fold is applied to the **subject** and to the pattern's **literal segments only**, never to `%`, `_` or the `\` escape. **Folding the escape character's operand would change what it escapes, and folding can change rune count** (FR-011a: `straße` → `strasse`, 6 runes → 7), so `_`'s "exactly one character" is counted **against the folded subject** — `'straße' LIKE 'stra_e'` is **false**, `'straße' LIKE 'stra__e'` is **true**. Both are DS-4 cases. A test writer who is not told this reads the answer off the implementation.
  - **Matching is case-insensitive** (FR-011a, ruling R-D) for `=`, `<>`, `LIKE` **and `IN`** on text and enum labels. *(Revision 6 adds `IN` explicitly — it is set membership over `=`, so it folds; revision 5 left it to inference and the truth table would have been filled in by guessing.)*
  - **`IS NULL` replaces `is_absent`** and keeps its exemption: it is the one operator absence does not make `false`, and it is exempt from FR-008's inclusion rule. `IS NOT NULL` is its complement and shares the exemption. **`<>` does NOT** — see R-2 and C-7.
- **FR-022d** **NEW, revision 6 (review round 6, M-13). The wire shape of the three operators that do not take a scalar `value`, because the leaf is `{property, op, value}` and three of the ten do not fit it.**
  - **`IS NULL` and `IS NOT NULL`: `value` MUST be ABSENT.** A present `value` — including JSON `null` — is **refused**, naming the operator: *"`IS NULL` takes no value"*. **`null` is not permitted as a spelling of "no value"**, and the reason is specific to this system: `null` is indistinguishable from *"the caller sent JSON null as the thing to compare against"*, in a design whose **central distinction is absence** (FR-007). Accepting it would put the ambiguity inside the operator that exists to resolve it.
  - **`IN`: `value` MUST be a NON-EMPTY array.** An **empty array is refused**, naming the operator and the remedy. **CONFIRMED AND STRENGTHENED IN DRAFT 7 (UAT case F-3.4).** The UAT case reads *"Refused **or** returns nothing, with the response saying which"* — i.e. it was written to accept either behaviour, so the tester would have recorded whatever shipped. **This requirement already ruled, and the ruling stands: it is REFUSED, and "returns nothing" is not an acceptable outcome.** `x IN ()` is a syntax error in most SQL engines, and an empty list is almost always a caller bug — an unfilled variable, a filter built from an empty selection — not an intent to match nothing. **What was actually missing was the refusal STRING**, which §4.1.2's refusal table carried for the empty `LIKE` pattern and not for this; it is added there, so the two siblings are now visible in the same place a tester looks. **A single-element array is valid and means the same as `=`** (UAT F-3.3). It is `LIKE ''`'s sibling (FR-022a): an empty `IN` list matches nothing, so it silently returns zero records for a query the caller believes selects something — the silent-empty-result failure FR-022c prohibits, arriving through a different door. A **single-element array is valid** and means the same as `=`.
  - **These three shapes MUST be expressed in `RecordQueryRequest`** (FR-090, Hard Constraint #8) rather than enforced only in Go, so that a malformed leaf is refused at the contract edge by the generated Zod validator as well as by the handler.
- **FR-022e** **NEW, Draft 7 (UAT case F-5.5). A QUERY LITERAL IS INTERPRETED IN THE PROPERTY'S DECLARED TYPE, and a literal that cannot be so interpreted is REFUSED — not `false`.** This closes the one gap that made **R-1** unevaluable in a query: R-1 rules on values of *"different **declared** types", and a record value gets its declared type from the schema while **a literal has no schema**. Nothing said how a literal acquires one, so `{property: "label", op: ">", value: 5}` had no defined answer.
  - **The rule, in two steps.** (1) **Interpretation.** The literal is interpreted **in the declared type of the property it is compared against** — a JSON string against a `date` is parsed by FR-021d's strict ISO-8601 grammar, against an `enum` it is resolved by FR-011's case-insensitive membership, against `relation`/`person` it is read as a wikilink or an identifier (R-8), against `integer`/`decimal` it is parsed as a number. Once interpreted it **is** a value of that declared type, and **R-12 then holds exactly as written**: every rule applies identically whether the value came from a literal or from a record. (2) **Refusal.** A literal that **cannot** be interpreted in that declared type — a JSON number against `text` or `enum`, a JSON string against `integer`/`decimal` that is not a number, a string against `date` that is not ISO-8601, a non-link against `relation`/`person` — is **REFUSED at query validation, before any candidate is retrieved**, in FR-024's pattern: naming the property, its declared type, the literal as supplied, and the form that would have worked.
  - **Why refusal and not `false`, which is the harder half.** `false` for every record is **indistinguishable from "no matches"**, and telling a caller "no records" when the truth is "your query cannot mean anything" is the exact silent-empty-result failure FR-022a, FR-022d and FR-024 all exist to remove — arriving through a fourth door. It is also knowable **before** any work: the property's declared type is in the schema and the literal is in the request. A refusal is cheap, early and unambiguous; a `false` is late, expensive and a lie.
  - **THIS CORRECTS THE OPERATOR'S OWN PROPOSED RULING, and the correction is narrow.** The proposal was that *"a `text` property compared against `2` treats `2` as the text `\"2\"`"*. **Step (1) above is exactly that instinct and it is adopted; step (2) is where it is rejected.** Retyping a JSON `2` into the text `"2"` is a **coercion**, and R-1's own words are *"Never an error, never a **coercion**"*. **JSON already distinguishes `2` from `"2"`** — a caller who means the text `"2"` has a way to say so and did not use it — so silently retyping the number is the product deciding the caller meant something other than what they wrote. It would also make `label > 5` return the records whose label sorts after `"5"` lexically, which is a plausible-looking answer to a query that was a mistake.
  - **R-1's `"3" > 2` example survives untouched, and its scope is now stated.** R-1 governs **values**, and after this requirement a filter leaf can no longer construct two values of different declared types — the interpretation step unifies them or the refusal step stops the query. **R-1's cross-type cells are therefore reached only by the comparator's own truth table (AC-8.1's type × type axis), which is where R-11's totality obligation lives.** The comparator stays **total and never panics** over every type pair; the query layer simply never hands it a mismatched pair. *That is a stronger position than either alternative: the refusal is at the layer that can name a remedy, and the comparator's totality is preserved as the backstop it was always meant to be.*
  - **This is checked against R-12 and does not contradict it.** R-12 requires that *"every rule above applies **identically** whether the value came from a query literal or from a record"* — it is a rule about **the rules**, not a licence to retype the literal. Its own recorded violation is SQLite's comparison affinity, where `3 = '3'` answers differently by provenance; the fix for that was **one comparison path**, which this requirement leaves untouched. After interpretation there is one value and one path, so R-12 is satisfied by construction.
  - **AC-22e.1** — `{property: <a text property>, op: ">", value: 5}` is **REFUSED**, and the message names the property, `text`, the literal `5`, and that a quoted string was expected. It does **not** return an empty result and does **not** return every record. *(This is UAT F-5.5's entire requirement: "it must not silently return everything or nothing.")* **AC-22e.2** — `{property: <a date property>, op: ">=", value: "2026-04-01"}` is accepted and compares as an instant; `value: "last April"` is refused naming the ISO-8601 form. **AC-22e.3** — `{property: <a decimal property>, op: "=", value: "3.0"}` and `value: 3` select the same records (R-1 makes `integer` and `decimal` one comparison type; interpretation makes the string and the number one value). **AC-22e.4** — no refusal under this requirement is ever reported as a **record problem**: a record problem is a fact about the vault (R-4), and this is a fact about the query.
- **FR-022c** **NEW, revision 5 (operator ruling R-C). An unsupported SQL construct MUST be REFUSED, naming what IS supported.** A model fluent in SQL will reach for `JOIN`, a subquery, `COALESCE`, `CASE`, `BETWEEN`, `EXISTS`, `GROUP_CONCAT`, a function call, or an operator outside FR-022b's list.

  **SCOPED, revision 6 (review round 6, M-12) — as revision 5 wrote it, this requirement could not be satisfied without the parser ADR-068 O-3 forbids, and its likely implementation committed the exact failure it prohibits.** The filter is a structured object of `{property, op, value}` with `op` drawn from a **closed ten-member enum**. **`JOIN`, `EXISTS`, `GROUP_CONCAT`, `BETWEEN` and an unknown operator arrive in `op` and are refusable by name — those work.** **A subquery, `COALESCE`, `CASE` and a function call have nowhere to arrive.** A model reaching for one puts it in `value` — `{"property":"amount","op":">","value":"(SELECT max(amount) FROM …)"}` — or in `property`. Detecting SQL inside a value string requires **recognising SQL**, which is the parser O-3 forbids and which FR-022 and FR-022b both reaffirm.

  **What FR-022c requires, restated to what the structure can actually express:**
  - **An `op` outside the ten-member enum MUST be refused by name**, listing the ten. This covers `JOIN`, `BETWEEN`, `EXISTS`, `GROUP_CONCAT`, `LIKE ANY`, `~`, and anything else a model puts in the operator position.
  - **A parameter name the request schema does not declare MUST be refused by name**, listing the accepted parameters. This covers `where:`, `sql:`, `having:` and their kin.
  - **A `property` the schema does not declare MUST be refused by FR-024**, listing the valid property names. This is where a `COALESCE(...)` or `CASE ...` in the property position lands, and **the refusal names the wrong problem** ("unknown property") — which is imperfect and is stated rather than papered over: it is non-silent, it lists real alternatives, and closing the gap properly would need the parser.
  - **A SQL fragment smuggled inside `value` is treated as a TEXT LITERAL**, and this is now specified rather than left to emerge. Against a typed property, R-1 makes the comparison `false` and **R-4 puts the record in `PROBLEMS` with the offending value named**. **That is a real, reachable, non-silent answer and it needs no parser.** *(Under revision 5 the same input returned **zero records with no problem reported** — the silent empty result FR-022c explicitly forbids, produced by the requirement's own default path.)*
  - **In every case the refusal lists the supported operators and names the parameter that does the job** (`join` for a relation, `group_by` for grouping, `aggregate` for a total, two `>=`/`<=` leaves for a range) — and **never a parse error, never a silent empty result, and never a partial evaluation with the unsupported clause dropped.** *(Dropping the clause is the failure mode that matters: it returns a plausible answer to a different question.)*
  - **"A subquery" and "a function call" are DELETED from this requirement's refusable list, from SC-002c and from §7 test 52**, except in the `op`- and `property`-position forms above. **A requirement that cannot be met is worse than an absent one**, because the test written to it gets weakened until it passes.
- **FR-023** Every property name, enum value and relation target in a query MUST be validated against the schema **before** evaluation.
- ~~**FR-023a**~~ **WITHDRAWN before it shipped, revision 5 (operator ruling R-A).** *It required De Morgan normalisation to the leaves before SQL emission, because `(x IS NULL OR x <> ?)` is a leaf rewrite while the filter grammar is a tree, and `NOT (subtree)` in SQL silently drops every NULL-bearing row. **Under ruling R-A no SQL is emitted for a comparison**, and a Go evaluator returning a real `bool` gets this right by construction: R-2 makes a comparison with an absent side `false`, so `NOT(false)` is `true` and FR-008's absent rows are included — at any depth of the tree, with no normalisation pass to forget.* **The requirement is retained in one narrow form as FR-023b**, because "it is right by construction" is a claim that deserves a test.
- **FR-023b** **NEW, revision 5.** The comparator MUST evaluate `not` over an **arbitrarily nested** filter tree, and a test MUST assert FR-008's inclusion rule over a **compound** negation — `{not: {all: [...]}}` and `{not: {any: [...]}}` — not only over a negated leaf. *The truth table's negation cells were all leaf-shaped; a tri-state bug in a tree walker would pass every one of them.*
- **FR-023c** **NEW, revision 6 (review round 6, M-30). The filter tree MUST be bounded, and it is the one input that was not.** Every other input to a query is bounded — page size, relation hops, group levels, candidates, sweep size, findings per category, response bytes — while the filter, **the one input whose cost MULTIPLIES against the candidate bound**, was unbounded, and FR-023b requires arbitrary nesting explicitly. A filter tree MUST be refused above **64 leaves** or **depth 8**, naming which bound it exceeded and the count it reached, in FR-024's pattern. **The two numbers are chosen to make A-14's worst case a defined quantity rather than an open one** — 50,000 candidates × 64 leaves = 3.2M comparisons, each with at most one `cases.Fold()` per operand and, under FR-011c, at most one fold per candidate rather than per leaf. They are not measured, and W1 measures at the bound. Both bounds appear in the constraint table so the measurement has a stated worst case to measure.
- **FR-024** A query naming an unknown property or enum value MUST be rejected with valid names listed, and MUST NOT return an empty result set. **Scope MUST be resolved before this rejection**, so the valid-names list never reveals schemas outside the caller's workspace — otherwise the error channel defeats FR-062.
- **FR-025** Every query response MUST carry a completeness verdict and a problem list as **required** fields.
- **FR-025b** **NEW, revision 6 (review round 6, unasked question 6). The health view IS workspace-scoped, and the question was real.** FR-025a's health view lists *"every bad value vault-wide"*, and FR-060's scope rule is written for **the six tools** — the health view is a **UI surface**, not a tool, so **US-8's guarantee had a hole exactly the width of that distinction**, in the story the whole document calls P0 and which ADR-067 was blocked on. **Ruling: every surface that renders record data resolves through `knowledge.Scope` — tool or not — and the health view is no exception.** It renders findings for the workspace the viewer is in, "vault-wide" means "across every collection **in scope**", and an out-of-scope finding is invisible rather than redacted (FR-062's rule, unchanged). §7 test 58 — which already asserts FR-025a's two surfaces agree — gains a scoped fixture, so agreement is asserted **within** a scope rather than across all of them.
- **FR-025c** **NEW, Draft 7 (UAT case N-2). THE HEALTH VIEW HAS A NAMED ROUTE.** FR-025a specifies the health view's content and FR-025b its scoping, and nothing said where in the SPA it lives — so a tester was told to open *"the health view"* with no address to navigate to, and two implementers would have put it in two places.
  - **The route is `/library/health`**, file `src/routes/_app/library.health.tsx`, workspace-scoped by the **same `?workspace=` search parameter `src/routes/_app/library.tsx` already validates** (`librarySearchSchema`) — so `/library/health?workspace=<id>` is the address, and FR-025b's scoping is carried by a parameter that already exists rather than a second scoping mechanism.
  - **`/library` becomes a LAYOUT route and the explorer moves to `library.index.tsx`.** This is the exact shape `src/routes/_app/agents.tsx` (a layout with `<Outlet />`) plus `agents.index.tsx` plus `agents.$agentId.tsx` already has in this tree; it is a precedent, not a new pattern. The explorer's behaviour, its unsaved-edits blocker and its `pagehide` handoff move **unchanged** — a route split that alters the explorer is a defect, not a refactor.
  - **Why under `/library` and not under `/workspaces/$workspaceId/`.** The health view is a property of a **vault**, and vaults are what `/library` is the screen for; a workspace shows the vaults mounted into it, which is what the `?workspace=` parameter already expresses. Putting it under a workspace would make "vault-wide" mean "this workspace's slice" **by route structure** — which happens to be FR-025b's answer, but arrived at by accident rather than by the scope resolution FR-025b actually mandates.
  - **Any sibling W6 record screen uses the same pattern** — a child of the `/library` layout, workspace-scoped by the same parameter. That is stated so the second screen does not start a third convention; it does **not** create a requirement for a screen no FR names.
  - **AC-25c.1** — `/library/health?workspace=<A>` renders findings for workspace A's vaults only, and an out-of-scope finding is **invisible, not redacted** (FR-025b, FR-062). **AC-25c.2** — the route is reachable from the Library screen's own navigation, not only by typing the address: a screen a tester has to hunt for is a screen an operator never opens. **AC-25c.3** — `/library` with no child still renders the explorer exactly as it does today, asserted by the existing `-library.test.tsx` suite passing unchanged.
- **FR-025a** **NEW, revision 5 (operator ruling R-G). Bad values MUST be reported on BOTH surfaces, and the two MUST agree.** One surface is not a substitute for the other, and the reasoning for each is different:
  - **In the answer** — every `vault_find` response carries the skipped/excluded block (FR-025, FR-026), **so an agent never reports a total as complete when it is not.** This surface exists for the machine, at the moment of the wrong answer.
  - **In a vault health view** — the human UI MUST carry a **problems screen listing every bad value vault-wide**, grouped by record type and by reason, each row naming the note path, the property, the offending value and the fix (FR-123's inline-fix rule applies unchanged). **This surface exists for the person, so they can clear a vault in one sitting** rather than meeting its problems one query at a time, which is how a vault stays broken.
  - **The two MUST be computed from one source and MUST agree.** A test asserts that the set of problems a `vault_find` reports for a scope is a **subset** of what the health view reports for that scope, and that a record absent from the health view never appears in a query's problem list. *(Subset, not equality, and the asymmetry is deliberate: FR-020c1's residual hole means a query compares only what it returned, so the health view is the broader of the two by construction. Requiring equality would be requiring the query to do the sweep.)*
  - **The health view is bounded by the same rules as `check_integrity`** (FR-075a): 500 findings per category, clamp reported with the would-be count, 100,000-note sweep limit with the scoped remedy.
  - **AC-G1** — a vault with a bad value in a property no query has touched appears in the health view. **AC-G2** — a bad value cleared in the vault disappears from both surfaces after re-index, with no separate acknowledgement step: **the note is the source of truth and the health view holds no state of its own.**
- **FR-026** A record excluded from an aggregate MUST be named in the problem list with the reason. **CLAMPED, revision 5 — as revision 4 worded it, this promise and FR-127's byte cap were not simultaneously satisfiable.** A query over 10,000 candidates of which 3,000 are non-conforming produces roughly 180 KB of problem lines against a 16,000-byte cap, and no clamp was specified anywhere. The requirement is now: **the problem list is clamped at 20 named records, and when it clamps it MUST carry a "showing N of M" line naming the total** — the pattern FR-075a already uses for `check_integrity`. **The clamp line is mandatory and is not itself clampable**, because "3,000 records were excluded, 20 named" is a *different and more alarming* verdict than "20 records were excluded", and collapsing the two is the silent-truncation failure this document exists to remove. The budget allocation that makes this fit is FR-127c.
- **FR-027** Grouping MUST support two levels.
- **FR-028** Grouping by a multi-value property MUST place a record in every group it belongs to.
- **FR-028a** **REWRITTEN, revision 5 (operator ruling R-A). The requirement survives; its mechanism is deleted.** **Every aggregate over a filtered set MUST be computed over DISTINCT RECORDS**, and a record MUST contribute to a `count`, `sum`, `min` or `max` **exactly once**, however many of its values match.
  - *This was grill pass 1's worst finding, and the SQL form of it is now unreachable. Verified for the record, because the ruling's justification rests on it: a record carrying both `vendor` and `vendors` joins **twice**, so `COUNT(*)` returns **2** and `SUM(amount)` returns **200** where truth is **1** and **100** — quiet, plausible, and wrong by a factor that varies per record. It reached `count`, `sum`, `min`, `max`, the `join` parameter and `group_by`. The specified defeats (`EXISTS` semi-join, `COUNT(DISTINCT)`, a de-duplicated subquery) are **withdrawn with the SQL evaluation path** — and one of them was itself a trap: `SUM(DISTINCT)` deduplicates on **value**, not row identity, so two distinct records sharing an amount collapse into one. It returned **100** against a truth of 200 while the naive join returned 300. It errs in the direction that looks conservative, which is the harder wrong answer to catch in review.*
  - **In Go this is a property of the aggregation pass, not a query shape**: the candidate set is a set of records, each visited once. **The requirement is retained anyway, and a test with it**, because "records are visited once" is exactly the kind of thing that stops being true when someone iterates the relation child rows instead of the records.
  - **`group_by` keeps both halves.** Grouping by a multi-value property **must** fan out — FR-028 requires a record under every group it belongs to — and each group's aggregate MUST still count that record **once within that group**.
  - **AC-8.4b's mutation:** remove the de-duplication from the aggregate pass. **The truth-table case that must die:** a record gains a **second** matching value of a `many` property, and `count` and `sum` MUST be unchanged.
- **FR-029** Grouping by a relation MUST be supported.

### Relations and identity

- **FR-030** A relation MUST be stored on disk as a quoted wikilink.
- **FR-031** The index MUST resolve a relation to the target's record identifier.
- **FR-032** The inverse of a relation MUST be derived and MUST NOT be stored in any file.
- **FR-033** A relation whose target does not exist MUST be reported by validation.
- **FR-034** A relation whose target is not of the declared type MUST be reported by validation.
- **FR-035** Relation cardinality MUST be declared and enforced.
- **FR-036** Every record MUST carry an identifier unique within its type, minted on creation.
- **FR-036b** **NEW, revision 6 (review round 6, m-11). The identifier PREFIX has a source, and it was named nowhere.** FR-036 requires an identifier and never said where its prefix comes from; the id-prefix block appeared only in ADR-068 D2's example and in §4.1.1's response spec, so a reader could not tell whether the prefix was schema data, derived from the type name, or ours. **It is schema data**: a record-type schema MAY declare `id_prefix`, and the minted identifier is `<id_prefix>-<counter>` (`CO-0142` in this document's illustrations). **If the schema declares none, the identifier is the counter alone** — the system MUST NOT derive a prefix from the type name, because that would be the product inventing vocabulary, which R-F forbids. The prefix is **not** an identity: two types may declare the same prefix and FR-036's uniqueness is still within-type. `vault_configure` writes it; `vault_describe` renders it.
- **FR-037** Identifier allocation MUST be mutually exclusive within a process and, on POSIX, across processes.
- **FR-038a** **NEW, revision 6 (review round 6, unasked question 1). A RESTORED record keeps its old identifier, and that is NOT a duplicate.** FR-038 forbids lowering the counter and SC-005a asserts a new identifier lands above a deleted one — so a note trashed at `CO-0142`, followed by allocations past 142, followed by **`restore`** (FR-048a), puts `CO-0142` back **below** the counter. **Nothing in revision 5 said whether that was a FR-039 duplicate-identifier finding or the intended behaviour, and both readings are defensible from the text.** **Ruling: it is intended, and it is not a finding** — the identifier was never reissued (FR-038 guarantees exactly that), so the restored record is the *same* record returning, not a second one wearing its name. **What IS a finding is the case FR-039 already covers**: two *live* records sharing an identifier, which `restore` can produce only if a note was created at the trashed record's own path in the interval. **`restore` MUST therefore check for a live record holding that identifier and REFUSE, naming both paths**, rather than restoring into a collision. The counter is never lowered in either case.
- **FR-038** On open, the allocator MUST raise its counter to at least the highest existing identifier and MUST NEVER lower it. Reconciling *to* the maximum guarantees reuse after the highest record is deleted, which makes an existing relation resolve to a different record.
- **FR-039** A duplicate identifier MUST be a hard validation error naming both paths.
- **FR-036a** **NEW, revision 4 (ADR-068 D15.1).** `vault_edit`'s one-file rule has exactly **one** accepted exception: `create` also writes `.omnipus-vault/records/.seq` under a cross-process flock, because FR-036 requires an identifier. `.seq` is accepted because it is a monotonic counter with no readable content, no meaning to any query and no recoverable state to lose — corrupting it costs skipped identifiers, which FR-038 already permits, never a wrong answer. **It is the only exception**; adding a second is a decision for another ADR, not for whoever writes the next tool. *(Revision 3 stated the tier rule as "writes only the file named in `path`", which was false on the first record ever created.)*

### Writes

- **FR-040** A write MUST reuse `pkg/knowledge/author.go`'s splice for scalar values; the system MUST NOT re-serialise the document.
- **FR-040a** The system MUST add a list-valued splice, preserving the source's existing list style. `SetProperty` is scalar-only and cannot satisfy FR-045 or `many: true`.
- **FR-040b** A scalar write targeting a key whose current value spans multiple lines MUST be refused and the file left unmodified, because the existing key-splice removes continuation lines and would silently delete the value.
- **FR-041** A write MUST leave the file byte-identical outside the patched span.
- **FR-042** A write violating the schema MUST be rejected with the expected shape named, leaving the file unmodified.
- **FR-043** **SCOPED, revision 5.** A **`vault_edit`** write against an existing file MUST carry ADR-067 D14's version token; a stale token MUST be refused and the refusal audited. *Previously unqualified, which contradicted FR-018a (which exempts `vault_configure`) and §4.1.5's own parameter table. **The two exceptions are named here rather than left implicit:** `vault_configure` (FR-018a, AC-C3) and `vault_restructure` (§4.1.5, AC-X3, ADR-068 AC-15.5d) declare no `expect_version`, on the same ground — a single-file token cannot honestly guard a cascade. `vault_edit`'s `create` is a third case and needs no token, there being no prior version to compare against.*
- **FR-043a** **NEW, revision 5 — `vault_configure` had NO concurrency control at all, and the refusal it relied on is a check-then-write race.** FR-018a's argument for removing `expect_version` is sound as far as it goes, but it removed the protection a token *can* give and put nothing in its place: **two agents concurrently issuing `create_record_type company` both pass the "already declared" check and both write `.omnipus-vault/records/company.yaml` — a silent lost update on the file that defines the type system, with no error, no detection, and an audit trail showing two successful writes and no anomaly.** FR-003's duplicate-type protection does not help, because `create_record_type` writes one path per type so the collision is *within* one file rather than across two; and FR-037's cross-process flock covers `.seq` only. The requirement is:
  - **`create_record_type` MUST create its file with `O_EXCL`.** A second concurrent create loses the race and receives FR-016's "already declared" refusal, which is the correct answer and is now produced by the filesystem rather than by a check that can be overtaken.
  - **`edit_record_type`, `delete_record_type`, `write_view` and `delete_view` MUST use a content-hash compare-and-swap over the file they write**, refusing on mismatch with the current hash named. **This is documented as guarding THE FILE and explicitly NOT the cascade** — which is honest, and is not the thing FR-018a rejects. FR-018a rejects a token that *claims* to guard a change to every note declaring the type; a CAS that says "this schema file changed under you" claims exactly what it delivers.
  - **It is not exposed as a caller parameter.** The hash is read and re-checked inside the write, so no `expect_version` appears in the tool schema (AC-C3 stands) and no caller has to hold one.
  - **AC-C7** — two concurrent `create_record_type` calls for one type produce **one** file and **one** refusal, never two writes. **AC-C8** — an `edit_record_type` whose file changed between read and write is refused naming the current state, and the file is unmodified.
- **FR-044** Every mutating `vault_*` tool MUST emit an audit entry per ADR-067 D19, named per FR-077.
- **FR-045** Relations MUST be modified through distinct add, remove and replace operations; replace MUST be named explicitly.
- **FR-046** Derived values MUST NOT be written into frontmatter.

**Two primitives in the tool tables do not exist and are not relabellings** (ADR-068 D14.1).
Every shipped `NoteEdit` constructor is additive — `SetProperty` (`pkg/knowledge/author.go:766`),
`AppendSection` (`author.go:799`), `AppendSectionAt` (`author.go:813`), `AddWikilink`
(`pkg/knowledge/authoring_tools.go:1103`), `AppendSectionOnce` (`authoring_tools.go:1129`) —
and there is **no body-replace and no delete primitive among the `NoteEdit` constructors**. **SCOPED, revision 6 (review round 6, M-45): revision 5 said *"anywhere in the package"*, and that is too strong.** `(*Writer).WriteNote` (`pkg/knowledge/version.go:499`) takes a `WriteRequest` and writes note content **wholesale**. It has **zero non-test callers**, verified — so FR-047's practical conclusion survives intact — but the premise as stated was false, and a reader who finds `WriteNote` cannot tell from revision 5's text whether it was considered. **It was, and it is NOT what FR-047 asks for:** it cannot address an anchor or a line range, and it **re-serialises**, which FR-040 and FR-041 forbid. A body-replace that preserves everything outside the patched span is genuinely new work. The `NoteEdit`
type is `func(src []byte) ([]byte, error)` (`author.go:540`), general enough to express anything;
`EditNote` (`author.go:620`) is a **harness that applies additive edits in order**, not an
editor. Its name invites exactly the misreading these two FRs exist to prevent.

- **FR-047** **Body-replace** MUST address its target by **anchor text or line range**, and its ambiguity rule is part of this specification rather than the implementer's choice: **an anchor matching more than once is REFUSED, naming every match with its line number, and the file is left unmodified.** A line range outside the file is likewise refused. Body-replace is an operation of `vault_edit` (one named file).
- **FR-047a** **NEW, Draft 7 (UAT case I-8.3). Templates have a location and a format, and BOTH ARE ALREADY BUILT — this requirement writes down `pkg/knowledge/template.go` rather than designing anything.** `vault_describe` reports the vault's templates (§4.1.1) and `vault_edit.create` consumes one, and no requirement said where they live or what they are, so a tester could not create a fixture and an implementer would have invented a second location.
  - **Location.** A collection's templates are **ordinary files in the marker directory** — `<vault>/.omnipus-vault/templates/` by default, or wherever the collection's marker names, resolved by `pkg/knowledge/marker.go` (`DefaultTemplatesDirName`, `Marker.TemplatesDir`, `TemplatesPath`). This is the same `.omnipus-vault/` marker directory that already holds `records/` (FR-001), saved views and `trash/` (FR-048), so templates need no new home and no new configuration key. A collection with **no** templates directory is **not an error** — it is the ordinary state of a vault Omnipus has never written to, and it lists as empty.
  - **Only top-level regular files are templates.** A subdirectory is not descended and **a symlink is skipped rather than followed** — a template symlinked at `/etc/passwd` must not become a note's contents. `pkg/knowledge/template.go:ListTemplates`.
  - **Format: a template IS a note.** Ordinary Markdown with optional YAML frontmatter, stored verbatim. There is no template header, no manifest and no registry; the file's name is the template's name.
  - **Expansion is a single left-to-right textual pass over a CLOSED four-token set** — `{{title}}`, `{{date}}` (ISO 8601 `2006-01-02`), `{{time}}` (24-hour `15:04`), `{{datetime}}` (RFC 3339) — and **nothing else**. There is no expression language, no conditional, no include and no shell-out. Three properties are normative because each is a decision, not an implementation detail:
    - **An unknown `{{whatever}}` is left EXACTLY as written**, never blanked. Blanking silently deletes something the operator typed, which is the same class of failure as a write that quietly loses a note.
    - **Templater syntax (`<% tp.file.title %>`), shell syntax (`$(id)`, `${HOME}`) and anything else instruction-shaped stays LITERAL.** Omnipus does not own those syntaxes and must not half-implement them.
    - **Expansion never re-scans what it substituted.** A note titled `{{date}}` produces the literal text `{{date}}` in the body, not today's date. A single pass makes that true by construction rather than by a recursion limit somebody later raises.
  - **A template name is resolved through the same containment gate as every other path** (`pkg/knowledge/contain.go`'s `ResolveContained`), not a second template-specific check: `../../../.ssh/id_rsa` and `/etc/passwd` are **refused**, never clamped, because a template read that silently became a read of some other file would put that file's contents into a note. The two sentinels are distinct on purpose — `ErrTemplateNotFound` ("pick another template") and `ErrTemplateNameRefused` ("that name is not inside the templates directory") need different operator advice.
  - **CITATION HYGIENE, and it is the M-36 defect again.** `pkg/knowledge/template.go` cites *"ADR-067 D12, FR-100..FR-102"*. **Those are the ADR-067 specification's FR numbers, not this document's** — `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` — and **this document's own FR-100..FR-103 are the `.base` importer**, an unrelated subject. Two documents share one `FR-nnn` namespace, exactly as they do for FR-039a. **Every citation of the template requirements in this document MUST be qualified as `ADR-067 spec FR-100..FR-102`**, and a bare `FR-100` here always means the `.base` importer.
  - **AC-47a.1** — `vault_describe` lists a template placed at `<vault>/.omnipus-vault/templates/specimen.md`, and lists nothing for a collection with no templates directory (empty, not an error). **AC-47a.2** — `vault_edit.create` with that template produces a note whose body is the template's, with the four tokens expanded and every other `{{…}}` **byte-identical to the template**. **AC-47a.3** — a template containing `<% tp.date %>` and `$(whoami)` produces those strings verbatim in the created note. **AC-47a.4** — `template: "../records/company.yaml"` is **refused** under `ErrTemplateNameRefused`, and the created note does not exist. **AC-47a.5** — a symlink in the templates directory is absent from the list.
- **FR-048** **Trash** MUST have a soft-delete convention, and it MUST answer three questions the current code answers for nothing: **where a trashed note goes** — `<vault>/.omnipus-vault/trash/<RFC3339 timestamp>/<original relative path>`, preserving the path so a restore is unambiguous; **what happens to inbound links** — they are **not** repaired, because there is nothing to repair them to, and the response MUST name the count and list the linking notes (FR-124's borrowed-value rule does not apply; these are real inbound links); and **when the index forgets it** — immediately on trash, in both indexes, in the same generation bump. Trash is an operation of `vault_restructure`. *(**Sequencing RESOLVED, revision 4.** Revision 3 read ADR-068 D20's two listings as convention-in-W4, operation-in-W5 and flagged the reading as A-9. **ADR-068 revision 6's D20 adopts exactly that split in the ADR's own words** — "the convention is W4 design work, the operation is W5" — naming the alternative it avoids: shipping part of a tier-5 tool before W5 defines the tool or seeds its policy. A-9 is closed in this spec's favour.)*
> **THE TRASH CONVENTION IS SETTLED, NOT PENDING — AND SAYING SO IS THE CORRECTION DRAFT 7 MAKES
> (UAT case J-3).** §11's W4 row read *"the trash convention … **written down and reviewed** before
> any tool exposes it"*, which any reasonable reader takes as *"it has not been written down yet"*.
> **It has.** Marking it pending would have **deleted six ruled behaviours**, which is a regression
> dressed as honesty. The six questions and where each is answered, so a tester and an implementer
> stop reassembling them from five places:
>
> | Question | Answer | Where |
> |---|---|---|
> | Where does a trashed note go? | `<vault>/.omnipus-vault/trash/<timestamp>/<original relative path>`, path preserved so a restore is unambiguous. The timestamp is **colon-free** (`20260826T120000Z`) because RFC 3339's colons are illegal in a Windows path component | **FR-048**, **FR-048a** |
> | What happens to inbound links? | **Not repaired** — there is nothing to repair them to — and the response MUST name the **count** and **list the linking notes** | **FR-048** |
> | When does the index forget it? | **Immediately**, in both indexes, in the same generation bump. The properties row is **deleted**, so a trashed record is not a permanent integrity finding for 30 days | **FR-048**, and the ruling under the `index_epoch` bullet |
> | Retention? | Purged after **30 days**; the trash directory's size is reported by `vault_describe` | **FR-048a** |
> | Restore semantics? | A `vault_restructure` operation. The record **keeps its old identifier and that is not a duplicate** (it was never reissued); `restore` **refuses** if a live record already holds that identifier, naming both paths; the reconstructed destination is resolved through `Root.ResolveContained` and refused on escape, `..`, an out-of-root symlink or an out-of-scope collection, **and the refusal is audited** | **FR-038a**, **FR-048a**, **FR-048b** |
> | Trashing the same path twice? | A **second** timestamped copy, and the response **says so**, naming both timestamps | **FR-048a** |
>
> **What W4 actually owes is the assembly and its review** — one document a person can read end to
> end — **not the rulings**, which are above and are normative now. §11's W4 row is reworded to say
> that. **UAT case J-3's seven steps each map to a row of this table**, so J-3 is runnable against
> the specification as it stands rather than resting on two refusal strings.

- **FR-048b** **NEW, revision 6 (review round 6, STRIDE — the ONE row the review marked NOT ADDRESSED, and it is a path-traversal hole in a write path).** FR-048a's `restore` takes a trashed path and restores it *"to its original relative location"*, **reconstructed from the trash directory layout** — and nothing said the reconstructed destination was validated. A note trashed from a collection that has since been unmounted, or a **hand-edited** trash path (the trash directory is ordinary files in the operator's own vault, editable by anything), restores **wherever the path says**. **The requirement:** the reconstructed destination MUST be resolved through `Root.ResolveContained` (`pkg/knowledge/contain.go`, the existing containment primitive — FR-060's scope check is not a substitute and does not run here) and MUST be **refused** if it escapes the collection root, contains `..` after normalisation, resolves through a symlink out of the root, or names a collection not currently in scope. **The refusal names the trashed path and the destination it declined to write**, and it is audited (FR-077) like every other refused write. **A restore is a WRITE the caller does not fully name**, which is precisely the C-A class `vault_restructure` exists to contain, and it was the one operation in that tier with no containment assertion. §7 test 68's sibling asserts it with a hand-edited trash path as the fixture.
- **FR-048a** **NEW, revision 5 — a soft-delete with no restore is a hard delete.** FR-048 justifies the trash path by *"preserving the path so a restore is unambiguous"*, and **no tool in §4.1 has a restore operation**. Three requirements:
  - **`restore` is an operation of `vault_restructure`**, taking the trashed path and restoring it to its original relative location. It is C-B by the reasoning of FR-070d (it revalidates relations on notes nobody named) and C-A-adjacent by nothing, but **FR-070e's mechanical rule decides it: it writes a note, so it is not `vault_configure`** — and it belongs with `trash` for the same reason `trash` does.
  - **Retention and purge:** trashed notes are purged after **30 days**, matching the session-retention default's shape, and the trash directory's size is reported by `vault_describe`. Without a purge the trash grows without bound and the "soft" in soft-delete costs disk forever.
  - **The timestamp directory MUST be colon-free.** FR-048 specifies `<vault>/.omnipus-vault/trash/<RFC3339 timestamp>/…`, and **RFC3339 contains colons, which are illegal in a Windows path component** — the form is `20260826T120000Z`. *(The specification considers Windows elsewhere, in the edge-case table's flock no-op row; this path would simply have failed there.)*
  - **A second `trash` of the same path produces a second timestamped copy and MUST say so** — `Deals/Acme.md was already trashed at 20260825T091500Z; this copy is at 20260826T120000Z` — rather than silently accumulating.
- **FR-049** Rename and move MUST report their cascade in counts (§4.1.5).
- **FR-049a** **NEW, revision 5 — nine shipping tools are retired in one wave with no deprecation window, no feature flag and no rollback.** FR-070a retires `knowledge_*` at W5, and any user-authored skill, prompt or seeded policy naming one breaks at that moment; ADR-068 §4.2's only mitigation is *"the migration is cheap now"*, which is a reason to do it, not a plan for doing it. The requirement: **a boot-time scan MUST report every skill file, agent prompt and seeded policy entry naming a retired `knowledge_*` tool, by path, and the report being EMPTY is a W5 exit criterion.** An alias period is deliberately **not** specified — an alias for a tool with different semantics is a second failure mode — but shipping the retirement without knowing what it breaks is not acceptable either, and the scan is what makes the difference.
- **FR-049b** **NEW, revision 5 — there are no observability requirements anywhere in this document, and the properties index is a new store that can diverge silently.** The requirement: a counter for **flagged-stale hits** (FR-020c — the rate A-7 says nobody knows), a counter for **index-rebuild events**, a counter for **candidate-cap refusals** (FR-064), a counter for **comparator-rejected records** (R-4/FR-021b), and a **WARN naming the platform** on a SQLite-less refusal (FR-020h). **Without these an operator discovers divergence only by running a query and reading a problem row**, which is not an operational posture. FR-020f's human panel snapshot is a UI affordance and is not a substitute.
- **FR-049c** **NEW, revision 5.** FR-020e forces a rebuild of every existing bleve index at upgrade, and **no availability contract was stated for it**. During a rebuild the system MUST report **`INDEX: rebuilding, N of M`** in every `vault_find` header and MUST return `complete: false`; it MUST NOT block, and it MUST NOT return an empty success. A 100,000-document rebuild is minutes, not seconds, and a gateway that silently answers "0 records" during it commits the failure this document exists to remove, at the worst possible moment. *(Revision 6, m-5: revision 5's closing sentence here was about ADR-067 D10 rewriting inbound wikilinks on rename and the count being reported. **That belongs to FR-049 and has nothing to do with rebuild availability**, which is this FR's entire subject. It is moved out.)*

### Interaction history

- **FR-050** A mention of a record in a dated note MUST be treated as an interaction.
- **FR-051** A mention inside an unchecked to-do MUST NOT count.
- **FR-052** A mention inside a completed task MUST count.
- **FR-053** A mention inside an embed, quote or code block MUST NOT count.

### Scope and bounds

- **FR-060** Every one of the **six** `vault_*` tools MUST resolve through the calling agent's workspace scope: agent → workspace → `AllowedMountRoots(home, workspaceID)` → records within those roots. Records are a **stronger** read primitive than search — typed fields, relations and aggregation across them — so the boundary ADR-067 D7 drew applies with no exception.
- **FR-061** An out-of-scope record MUST yield an empty result, never a permission error.
- **FR-062** The scoped-out case MUST be indistinguishable from an empty vault.
- **FR-062a** When scope resolution is itself incomplete — ADR-067's `Scope.Truncated()` — the query MUST report incompleteness with that reason. A query that could not resolve every mount MUST NOT report success, or a whole mounted folder can go missing while the answer claims to be complete.
- **FR-063** Page size MUST default to 50 and cap at 200; a clamp MUST be reported.
- **FR-064** **SCOPED, revision 5.** A **row-returning** query whose candidate set exceeds 10,000 records MUST be refused with a narrowing instruction. **"Candidate" is defined, because it is the most consequential bound in the document and revision 4 never said what it counted — and revision 6 fixes the UNIT, which revision 5 also left ambiguous (review round 6, m-9).** **BOTH bounds count RECORDS, never rows**, and the distinction is not pedantic: with `many` arity and the relation child table, **a single record produces several rows**, so a cap stated in rows silently tightens by the average arity of the query's `many` properties — a 10,000-row cap over a corpus averaging two tags is a 5,000-record cap that nobody chose. The population B1 counts is the **distinct record population the narrowing predicates select**; the population B2 counts is the **distinct records the comparator has accepted**. The constraint table says "records" in both rows and means it. **RESPECIFIED, revision 6 (review round 6, C-4) — as revision 5 wrote it, this bound was UNIMPLEMENTABLE, and it is the load-bearing bound of the whole design.** Revision 5 said the count was *"a `COUNT(*)` over the compiled `WHERE` clause, taken before any row is materialised"*. **Ruling R-A deletes the compiled `WHERE` clause.** SQLite narrows by type, path prefix and kind; "the rows surviving the filter" is a quantity **only the Go comparator can produce, and it produces it by evaluating candidates — which is materialising them**. So the requirement, its acceptance criterion (FR-066a), its test (47) and its refusal string were all written for a mechanism the ruling removed. If a cap can only be enforced *after* the work it bounds, it bounds nothing.

  **What replaces it: TWO bounds, because one number was being asked to do two different jobs.** They protect different resources, they fire at different moments, and each names a remedy that actually reduces the number it fired on.

  | | **B1 — the EVALUATION bound** | **B2 — the MATERIALISATION bound** |
  |---|---|---|
  | **What it counts** | the **narrowed** candidate population: `COUNT(*)` over the narrowing predicates **only** — `type`, `path` prefix within scope, `kind` (AC-8.10's allow-list, and nothing else) | the **survivors**: records the comparator has accepted |
  | **Value** | **50,000** — deliberately the supported vault size, not a second independent number to reconcile | **10,000** |
  | **When** | **before any candidate is retrieved.** One index-bound aggregate; genuinely cheap and genuinely pre-retrieval | **during evaluation**, as a streaming abort |
  | **What it protects** | **work** — it is what makes A-14's cost claim true rather than hopeful | **memory** — it is what FR-066b's one-page rule rests on |
  | **Remedy named in the refusal** | narrow the **scope** (a collection or path prefix), or the **kind** — the dimensions the count actually ranges over | add or tighten a **filter**, or use the aggregate-only path (FR-064a) |

  **Two things are now said plainly that revision 5 said the opposite of.** *(i)* **B2 is NOT "before any row is materialised"** — it cannot be, and FR-066a, §7 test 47 and the refusal string are rewritten to say so. *(ii)* **A-14's bound is correspondingly weaker than revision 5 claimed**: the Go path's worst case is **50,000 candidate evaluations × the filter's leaf count** (FR-023c caps the second factor), not 10,000 evaluations. That is the number W1 must measure, and §11's W1 row already says 50,000 — the wave plan was right and revision 5's reasoning was not.

  **The cost of B1 is stated rather than hidden: it can refuse a query whose filter would have selected three records.** A vault with 60,000 notes of one type refuses every row-returning query over that type until the caller narrows by scope or kind, even though the answer was small. That is a real product cost and it is accepted deliberately — the alternative is a bound that is enforced after the work, which is not a bound. **The refusal must therefore NOT say "add a filter on status"**, because under B1 adding a filter does not change the number that fired; a refusal whose remedy does not work is the failure class this document exists to remove.
- **FR-064a** **NEW, revision 5 — this closes the collision between the supported-vault size and the candidate cap.** An **aggregate-only** query — one that requests `aggregate` and/or `group_by` and returns **no rows** — is **exempt from FR-064** and is bounded instead at the supported vault size (50,000 records, the constraint table). *Rationale: the constraint table supports 50,000 records per vault while FR-064 refuses above 10,000 and FR-066 forbids an aggregate over a refused set, so **a vault with 10,001 deals could never obtain a total over its deals, by design, with no escape hatch** — and the refusal's own remedy ("add a filter on status") means running seven queries and adding them by hand, which is precisely the five-manual-steps cost ADR-068 §1.2 cites as the thing this system removes. The two bounds were set for different reasons and never checked against each other.*
  - **Why this is not "raising the cap", which FR-066a rightly forbids.** FR-064 exists because **materialising** rows is what costs memory (FR-066b's one-page rule). **CORRECTED, revision 6 (review round 6, C-3) — revision 5's stated mechanism was a SQLite pushdown, which ruling R-A forbids, and §8.1 said the opposite about the same requirement 980 lines later.** An aggregate-only query **materialises one RESULT row**, and that is the property that matters. **It does NOT avoid retrieving candidates.** The `COUNT`/`SUM` is a **Go scan over a streamed candidate set** — the same comparator, the same de-duplication, the same R-1..R-13 — accumulating into one result row and holding no page of rows. *(Revision 5: *"the `COUNT`/`SUM` is pushed entirely into SQLite and no candidate is retrieved"*. §8.1 in the same revision: *"FR-064a's aggregate-only exemption … is now a **Go** scan over a candidate stream **rather than a SQLite pushdown**"*. Two normative statements, opposite mechanisms, one requirement — and the SQL one was mandated for **the single query shape that returns no rows, so no reader can sanity-check the total against anything**. It is also the shape where the deleted defect lives: FR-028a's own receipt is a `SUM` over a join with a `many` property returning **200 where truth is 100**.)*

  - **The cost is now stated honestly, because A-14 was told this exemption was free.** The aggregate-only path **retrieves and evaluates up to 50,000 candidates** — FR-064's B1 ceiling, and five times the row-returning path's B2 cap. It buys exemption from **materialisation**, not from **work**. A-14 carries the measurement; §11's W1 row already requires it *"at the 10,000-record cap **and at FR-064a's 50,000**"*.
  - **The de-duplication is not optional and is asserted here, not assumed.** Because the candidate stream includes the relation child table's `many` fan-out, a record with two matching elements arrives twice. **SC-002a's record-counted-once property applies to this path in full**, and §7 test 59 asserts the **VALUE** of the aggregate against a hand-computed fixture total — not merely that it returns `COMPLETE: yes`. **The bound that was load-bearing is not being relaxed;** a query shape that does not materialise pages is being permitted, at a work cost that is named.
  - **It is still bounded, and the bound is still a refusal.** Above 50,000 candidate records the aggregate is refused naming the count and the supported size. And **FR-066b still applies**: an aggregate-only query MUST hold no more than the result row in memory, asserted by measured peak RSS at the bound, not by inspection.
  - **The response says which path it took.** An aggregate-only response carries `ROWS: none — aggregate-only over N records` in its header, so a caller cannot mistake an exempted total for a paged one.
  - **AC-F8** — **STRENGTHENED, revision 6 (review round 6, V-4): as revision 5 wrote it, a join-fan-out-inflated `sum` passed.** A vault of 24,000 records returns a `sum` over all of them with no rows and `COMPLETE: yes` — **and the `sum` EQUALS a hand-computed fixture total, and the fixture MUST contain at least one record whose `many` property holds two matching elements**, so that a missing de-duplication produces a wrong number rather than a passing test. *(Revision 5 asserted nothing about the value, over the one query shape whose answer no returned row can corroborate, on the one path still specified as a SQLite pushdown. The two defects were mutually concealing.)* The same query with `select` or `limit` present is **refused** by FR-064's B2.
- **FR-065** A query requesting more than two relation hops MUST be refused.
- **FR-065a** **NEW, revision 6 (review round 6, unasked question 4). The JOIN side is bounded, and it was not.** FR-064 caps **candidates**; `join` borrows columns through a relation, and **no bound mentioned the join at all**. A 9,000-candidate query joining a `many` relation with 20 targets each materialises **180,000 borrowed values** while passing every bound in the document. **The rule:** the total number of borrowed values a query may materialise is capped at **50,000** — the same ceiling as FR-064's B1, deliberately, so there is one number to reason about — counted as `candidates × joined properties × per-record target arity`, **enforced as a streaming abort** during the join pass, in FR-064's B2 pattern and with the same honesty about when it fires. The refusal names the join, the arity that blew the bound, and the remedy (drop a `join`, or narrow first). **W1 measures it alongside A-14's other numbers.**
- **FR-066** No aggregate MUST be returned over a candidate set refused **under FR-064**. *(FR-064a's exempt path is not a refused set; it is a different query shape with its own bound.)*
- **FR-066a** **RESTATED, revision 6 (C-4) — split to match FR-064's two bounds, because "a HARD precondition, counted BEFORE retrieval" was true of neither number as revision 5 defined it.** **B1 (50,000 narrowed candidates) IS a hard precondition counted BEFORE retrieval**, and it is the one A-14's cost claim rests on. **B2 (10,000 survivors) is a hard ABORT during evaluation** — it stops the query, it is not advisory, and it is not pre-retrieval. Neither** is a politeness limit and neither MUST be relaxed on the ground that the properties index is assumed to make it cheap. *(ADR-068 D16.3a, condition C-3, upheld against revision 5's relaxation. Nobody has measured the two-index path; relaxing a bound is something to do **after** a measurement shows it is no longer load-bearing, never **because** a new store is assumed to make it so. If W1 shows the path is comfortable at 50,000, the cap is revisited in a revision that cites the number.)*
- **FR-066b** **The query path MUST hold no more than one page of rows in memory at once**, and this MUST be asserted by **measured peak RSS at the cap**, not by code inspection. *(ADR-068 D16.3a, condition C-2. **Revision 6 (review round 6, survivor 4) deletes revision 5's illustration, which described a SQL `GROUP BY` — no `GROUP BY` is emitted (AC-8.10).** The rule is unchanged and binds **harder** under ruling R-A, not softer: grouping is now a **Go** pass, so the thing that must not be materialised whole is the Go grouping structure over up to 50,000 evaluated candidates. **The group KEY set is bounded** — FR-029's two group levels, over an `enum`'s closed value set or a `text` property's distinct values — but **the distinct-value count of a `text` group key is NOT bounded by anything in this document**, and grouping 50,000 records by a free-text property builds a map with up to 50,000 entries. That is a real, unbounded structure and it is carried by A-14 with the rest of the Go path's cost.)*
- **FR-067b** **NEW, revision 6 (review round 6, O-4). The per-agent rate is not a bound on the cost, and the observation is upheld.** FR-067a's 3-per-minute limiter is a good answer to the wrong quantity: it is **per agent**, and `check_integrity`'s sweep is over up to 100,000 notes, so **three of those a minute per agent is not a bounded cost** — and a workspace with six agents multiplies it. **The requirement:** `check_integrity` additionally carries a **per-collection cooldown**, default **60 seconds**, evaluated **before** the per-agent limiter and **independent of which agent asks**. Inside the cooldown the call returns the **previous sweep's result with its timestamp and an explicit `stale: <age>` marker**, never a refusal and never a fresh sweep — a cached integrity report is useful and honest; a 429 on an orientation call is not. The cooldown is operator-configurable and is reported in `vault_describe`'s own output so its effect is visible rather than mysterious. *(`vault_describe check_integrity` remains the highest-risk operation in the surface, and this is stated as a mitigation, not a closure.)*
- **FR-067a** **NEW, revision 5 — the reads are not rate-limited and the most expensive operation in the document is seeded `allow`.** FR-070c is normative: *"the seeded default for a tool MUST be chosen for the **WIDEST** operation it grants."* FR-080a seeds `vault_describe: allow` on the ground that *"a prompt in front of a read that `read_file` already permits protects nothing"* — **and that is false for `vault_describe`'s widest operation.** `check_integrity` sweeps up to 100,000 notes resolving every wikilink in the vault; `read_file` permits reading **one file**. So the justification does not apply to the operation FR-070c says the default must be chosen for, and `vault_describe` has **no rate limit** — FR-067 enumerates the three write tools only, and §0 establishes `checkRetrievalRate` is not inherited by anything in this specification. **Two changes, and the second is chosen over seeding `ask`:**
  - **`vault_find` and `vault_describe` come under FR-067's limiter**, with their own bounds: `vault_find` at 30 calls/minute per agent, `check_integrity` at **3 calls/minute per agent**, both returning 429 with `Retry-After`. A 10,000-candidate two-index query is not cheap either.
  - **`vault_describe` stays seeded `allow`, and the justification is REPLACED rather than repeated.** Seeding it `ask` would put a prompt in front of the orientation call §4.1.1 calls *"the mandatory cheap first call"* — an agent that has not made it is guessing at property names, which is the failure FR-024 exists to prevent — and training an operator to click through that prompt is how the prompts that matter stop being read. **The widest operation is bounded by the limiter instead of by a prompt**, which is the control that actually applies to a sweep. *(The alternative — splitting `check_integrity` onto its own seventh tool — is rejected: it buys one policy lever at the cost of a permanent catalog entry and its standing schema cost, which is the trade D15.2 rejects elsewhere.)*
  - **This is a genuine amendment to FR-070c's application, not a loophole in it**, and it is marked as one: the rule is that the default must be chosen *for* the widest operation, and here the widest operation is made safe by a different control. **An operation whose cost cannot be bounded by a limiter still takes the prompt.**
- **FR-067** **NEW, revision 4 (ADR-068 D15.5b); WIDENED to reads in revision 5 (FR-067a).** `vault_edit`, `vault_restructure` and `vault_configure` MUST be rate-limited, returning **429 with `Retry-After`** on breach. **This is new work, not inheritance.** Revision 3's constraint table said the limiter was "shared with ADR-067's knowledge limiter"; it is not. `knowledgeRESTLimiter` (`pkg/gateway/rest_knowledge.go:90`) is consulted at exactly one place, `rest_knowledge.go:691`, on the **REST** path — no agent tool touches it. The agent-tool limiter `checkRetrievalRate` has three call sites, all reads (`pkg/knowledge/tools.go:610`, `:749`, `pkg/knowledge/authoring_tools.go:1330`). No write `Execute` consults either.

### Tools, policy, contracts

- **FR-070** **MEANING CHANGED AGAIN, revision 4 (ADR-068 D15.3, D15.6).** The system MUST expose exactly **six** tools: `vault_describe`, `vault_find`, `vault_read`, `vault_edit`, `vault_restructure`, **`vault_configure`**. *Revision 3 said five; revision 2 said nine `record_*`. The sixth is the control plane (FR-016..FR-018a) and it is a **correction**, not an addition — without it the schema-authoring policy lever does not exist. Cited by §6 traceability, §7 test 18 and SC-011; all three are restated below. The nine `record_*` names were never implemented, so nothing but this document referenced them.*
- **FR-070a** The six tools MUST also **replace** the nine shipping `knowledge_*` tools — `knowledge_search`, `knowledge_graph`, `knowledge_tasks`, `knowledge_create`, `knowledge_link`, `knowledge_set_property`, `knowledge_append_section`, `knowledge_move`, `knowledge_rename` (`pkg/coreagent/core.go:475-482`). After W5 no `knowledge_*` name remains in `allStaticToolNames` (`pkg/coreagent/core.go:357`), in the global ceiling (`pkg/config/defaults.go:637-646`), or in any seeded per-agent map.
- **FR-070b** **The tool boundary is the policy boundary. REVISED, revision 4: there are TWO criteria, not one.** `vault_edit` MUST write **only** the file named in its `path` argument, plus `.seq` on `create` (FR-036a). `vault_restructure` is the only tool permitted to **write bytes into** a file the caller did not name (**C-A**). `vault_configure` is the only tool permitted to **change what existing files mean** — their validity, their type, or how a query renders them — **without writing them** (**C-B**). A reviewer decides an operation's tool by asking both questions in order. *Revision 3 stated one criterion and, following ADR-068 revision 5, applied it under two incompatible readings — bytes to keep `link` in the per-file tier, meaning to push a schema change into the cascading one — and then presented the result as evidence the criterion generalises. **ADR-068 D23.3 withdraws that claim in revision 6.** A criterion that needs two readings is two criteria; the design is not weaker for having two, the document was weaker for pretending it had one.* Additive-versus-destructive remains explicitly **not** a criterion — `set_property` overwrites and stays in `vault_edit`.
- **FR-070c** An operation enum **within** one tool is permitted and carries no policy meaning, and **the seeded default for a tool MUST be chosen for the WIDEST operation it grants, not the most common one**. Policy resolves on the tool **name** only — `resolveToolPolicyAtExec(ts *turnState, toolName string, …) string` (`pkg/agent/loop.go:12418`) takes no arguments and no operation discriminator — so every operation reachable from one tool MUST be equally acceptable to an operator who granted that tool. *(Revision 4: this is why FR-016..FR-018 move out of `vault_edit`. It is also why `vault_edit`'s description must name `replace_body` — its widest remaining operation — rather than `set_property`, its most common one; see FR-079.)*
- **FR-071** Tool names MUST contain no dots. *(Audit event names are not tool names: FR-077's `vault.edit` / `vault.restructure` carry a dot deliberately and are out of this requirement's scope.)*
- **FR-072** **MEANING WIDENED, revision 3 (ADR-068 D22.1); extended to six in revision 4.** **Every** result from **all six** tools MUST be rendered to the model as compact text, never as a JSON document. *Previously: `record_schema` alone. Cited by §6 and §7 test list.* **Revision 5 narrows one over-broad consequence:** "never a JSON document" is a rule about the **envelope**, not about the bytes. `vault_read` returns a note's body, and a note may legitimately contain a fenced JSON code block; §7 test 25's "asserts no JSON document is emitted" MUST NOT be applied to `vault_read`'s body content, or the test forbids reading an ordinary note.
- **FR-072a** **NEW, revision 5.** FR-127's budget is scoped to `vault_find`, `vault_describe` and `vault_configure`. **`vault_read` has its own budget and its own truncation contract**, because a note is not a result set: its `max_bytes` default of 40,000 (§4.1.3) is **2.5× FR-127's 16,000-byte hard cap**, and revision 4 stated both without scoping either, so the two read as a contradiction. `vault_read`'s contract is: **`max_bytes` bounds the note content**, truncation is stated in the header and never silent, and the version token, the typed-frontmatter block and the refusal strings are **outside** the bound and always emitted. A note larger than `max_bytes` is truncated with `section` named as the remedy — never refused, because refusing to read a large note leaves the agent with `read_file` and no scope check (US-9).
- **FR-073** **MEANING CHANGED, revision 3 (ADR-068 D15.3).** `explain` MUST be a **boolean argument of `vault_find`**, not a tool. With `explain: true` the system MUST report what the query would select, which properties it could not evaluate and why, and MUST NOT evaluate the query. *Previously: a `record_explain` tool. Cited by §6 and §7.*
- **FR-074** `vault_read` MUST return the note's **version token** in every successful response. Obtaining a token MUST NOT require sending a write that is expected to fail. *(Today the only source of a token is `*ConflictError` from a refused `EditNote` — `pkg/knowledge/author.go:701`; `EditNoteRequest.ExpectVersion` (`author.go:566`) refuses an empty token too.)*
- **FR-075** **WIDENED, revision 4 (ADR-068 D15.3).** `vault_describe` MUST accept an integrity sweep (`check_integrity: true`) reporting **duplicate identifiers** (FR-039), **unresolvable and mistyped relations** (FR-033, FR-034), **unresolved ordinary wikilinks and orphan notes across the whole vault**, and **rows in the properties index with no note behind them** (FR-020c), and MUST name every offending path. *The wikilink and orphan half is not optional and not a nicety: `knowledge_graph`'s `unresolved` and `orphans` cover ordinary wikilinks vault-wide (`pkg/knowledge/tools.go:809-811`), **most notes in a vault are not records**, and FR-070a retires that tool — so without this widening a vault-wide broken-link report would have no home in the new surface at all.*
- **FR-075a** **BOUNDED, revision 4 (ADR-068 D15.5b).** `check_integrity` MUST NOT be argument-free-and-unbounded. It MUST accept an **optional scope** — a record type, or a collection — and MUST enforce: **500 findings per category** (reported as clamped, with the count that would have been returned and the scope argument that narrows it) and **100,000 notes swept** (refused, naming the collection, with the scoped form as the remedy). Unscoped is permitted and is the common case; it is the *unbounded* part that is not. *Revision 3 called this "an argument-free whole-vault integrity sweep" and gave it no bound, which would have made it the most expensive operation in this specification and the only one with no stated limit — in a document whose motivating evidence is unbounded operations returning silently wrong answers. The unboundedness is **inherited** from `knowledge_graph`'s uncapped sweeps, not introduced here; inheriting it into a tool advertised as bounded would be worse than shipping one.*
- **FR-076** `vault_find` MUST accept `near` (a note) with `hops` (1..2) and MUST **compose** it with `words`, `type` and `filter` in a single call. A `near` query MUST NOT bypass, weaken or replace any filter supplied alongside it.
- **FR-076a** **NEW, revision 4 (ADR-068 D15.3). `kind: task` MUST be served from an INDEXED checkbox row, not a walk.** Revision 3 listed `kind: task` in `vault_find`'s parameter table and specified no mechanism, which was an absorption claim with nothing behind it. Two facts make the mechanism non-obvious and both are verified: `indexDoc.Kind` only ever holds `note` or `attachment` (`pkg/knowledge/scan.go:45,48`), so a `kind` filter over today's index selects nothing; and `knowledge_tasks` is not a record query at all — it walks the collection with `WalkContained` (`pkg/knowledge/authoring_tools.go:1398`), reads each file (**`:1434`, `ReadNoteContent`** — *revision 6, review round 6, M-43: revision 5 cited `:1420` for both the read and the clamp; `:1420` is `if len(files) > TasksMaxFiles {`, the head of the clamp block, so `:1420-1425` is right for the clamp and wrong for the read*), matches `^[ \t]*[-*+][ \t]+\[([ xX])\][ \t]*(.*)$` per line, bounds itself at `TasksMaxFiles = 5000` (`:1246`, clamp reported at `:1420-1425`), and returns **many rows per file**. Therefore:
  - **A checkbox MUST be indexed as its own row** in the properties index, carrying `path`, `line`, `status` (`open` / `done`), `text` and the `source_hash` FR-020c requires — the same shape every other row has, so freshness, bounds, pagination and rendering all apply unchanged.
  - **FR-124's one-row-is-one-real-file rule is AMENDED, narrowly and explicitly:** a row is one real *thing at a path* — a note, or a checkbox line within one. A task row MUST render with its line number so a reader cannot mistake it for the note. *(This is a genuine amendment and is marked as one rather than absorbed silently.)*
  - **The regex walk MUST NOT survive.** `TasksMaxFiles = 5000` is a bound on *reading*, and it exists only because the walk re-reads every file on every call. FR-063..FR-066 replace it.
  - **The cost is stated:** checkbox extraction joins the indexing pass, so indexing does slightly more work per note. That is the trade — a per-index cost paid once against a per-query cost paid every time, over a corpus FR-075a caps at 100,000 notes. **Nobody has measured that indexing cost** (ADR-068 §6); W2 measures it.
- **FR-077a** **NEW, Draft 7 (UAT cases I-6.4 and K-5.3). THE AUDIT LOG MUST REACH A HUMAN, AND THE SURFACE IT LANDS ON ALREADY EXISTS.** FR-077 and AC-C5 require audit entries and no requirement put them in front of a person, which left a MUST whose satisfaction nobody could observe — and two UAT cases (I-6.4, K-5.3) instruct the tester to *"check the audit log surface in the UI (Settings → Security, or wherever audit entries surface)"* with no document to confirm that guess against.
  - **The audit log is NOT machine-only for this release, and the surface is named: Settings → Security → Audit Log**, rendered by `src/components/settings/AuditLogViewer.tsx`, opened from `src/components/settings/SecuritySection.tsx`'s Audit Log control. It is a **shipped, live surface** — this requirement adds no screen and no route.
  - **The requirement:** every entry FR-077 emits — `vault.edit`, `vault.restructure`, `vault.configure` — MUST be **visible and filterable in that existing viewer**, carrying its operation, agent, workspace, target path and outcome. **No new audit UI is built for records**; a second audit surface would be a second thing to keep in step, and the existing one is where an operator already looks.
  - **REFUSALS ARE AUDITED, and this is the half that was easy to lose.** A **refused** mutating call emits an audit entry exactly as an accepted one does. AC-C5 says *"including on refusal"* for `vault_configure` and FR-048b says it for a refused restore; **the rule is general and is stated once here** rather than re-derived per tool. A refused write is the most security-relevant thing the write path does — a stale token, an out-of-scope path, a hand-edited trash destination — and an audit log that records only successes records the wrong half. *(UAT K-5.3 asserts this for every `vault_configure` call "including the refused ones"; UAT I-6.4 asserts it for the stale-token refusal.)*
  - **AC-77a.1** — after one accepted and one refused `vault_edit`, **two** entries are visible in Settings → Security → Audit Log, distinguishable by outcome. **AC-77a.2** — an entry names the agent, the workspace and the target path, so an operator can answer "which agent changed this note" without reading a file on disk. **AC-77a.3** — no new route, component or navigation entry is added for record auditing; the assertion is that the existing viewer renders the new event names.
- **FR-077** Every mutating call MUST emit an audit entry named for its tool — `vault.edit`, `vault.restructure` **or `vault.configure`** — carrying the operation, agent, workspace, path and outcome. The operation appears in the audit record because it is known **after** the call; it MUST NOT be presented anywhere as a policy lever, because FR-070c establishes it is not one.
- **FR-078** **CORRECTED, revision 4.** The **catalog size** MUST fall: after W5 the static builtin catalog contains **six** `vault_*` names and **zero** `knowledge_*` names, taking `allStaticToolNames` from **98** entries to **95**. *Revision 3 said 102 → 98. **102 was a miscount**, corrected in ADR-068 D15.0 and re-counted independently here: the composite literal at `pkg/coreagent/core.go:358-482` holds **98 quoted identifiers, 98 unique, 9 of them `knowledge_*`**. So 98 − 9 + 6 = **95**. The comparison that matters is against the shape this replaces: nine `record_*` **alongside** nine `knowledge_*` would have been 107, i.e. eighteen definitions on one subsystem.*
- **FR-079** **CORRECTED, revision 5 — the claim its cost argument rested on is FALSE, verified in code.** Each of the **six** tool **descriptions** MUST fit a budget of ~150 tokens, and each MUST **name the widest operation it grants** (FR-070c) rather than the most common one — an operator reading `vault_edit` will not infer `replace_body` from the name. **What must NOT be repeated is revision 4's justification**: *"Operation detail belongs in parameter descriptions and error messages, which are paid only when used."*
  - **Parameter descriptions are paid on EVERY request.** `ToolsToProviderDefs` (`pkg/tools/registry.go:542`) builds `providers.ToolFunctionDefinition{Name, Description, Parameters}` at `:557-560`, where `Parameters` is **the whole schema map, verbatim**, sourced from `ToolToSchema` (`pkg/tools/base.go:431-440`) which passes `tool.Parameters()` through untouched. It is rebuilt per LLM request at `pkg/agent/loop.go:8346`. **There is no per-parameter lazy loading anywhere.** Only **error messages** are genuinely paid on use.
  - **The one nuance that survives:** tool-def compression (`buildCompressedToolDefs`, `pkg/agent/tool_manifest.go:141`, ending in the same `ToolsToProviderDefs` at `:176`) gates **which tools** are sent, not the schema fidelity of a sent tool. So "paid only when used" is true at **whole-tool** granularity under compression and false at **parameter** granularity, always.
  - **The arithmetic is therefore re-derived against the right denominator.** The standing per-turn cost of a `vault_*` tool is **description prose + the entire JSON parameter schema**, and `vault_find` alone declares **fifteen parameters plus a recursive filter-tree schema** (§4.1.2). **The ~900-token figure in FR-128 counts descriptions only and is an undercount of the real standing cost by an unknown multiple.** The correct figure is not stated here because it has not been measured — **measuring the serialised six-tool definition set, in bytes, is a W5 obligation and an exit criterion**, and until it exists neither this specification nor ADR-068 D15.0/D22.8 may argue "six tools are affordable" from the 900.
  - **The requirement that survives the correction is stronger, not weaker:** push detail into **error messages**, which really are paid on use, and keep parameter schemas terse — because they are not.
- **FR-080** Every one of the **six** `vault_*` tools MUST have an explicit, literal, wildcard-free policy entry for **every** seeded agent — all ten `coreagent.SeedConfig` creates (`mia`, `jim`, `ava`, `ray`, `worker`, `planner`, `explorer`, `researcher`, `judge`, `plansupervisor`), not only the four base agents. `worker`'s map is sparse, and in a sparse map **absence grants**.
- **FR-080a** **EXTENDED, revision 4.** The seeded posture MUST be: `vault_describe` / `vault_find` / `vault_read` **allow** for Mia, Jim, Ava and Ray and **deny** for workers and the rest; `vault_edit` **ask** for Mia and Ray, **allow** for Jim and Ava, **deny** elsewhere; `vault_restructure` **ask** for all four base agents and **deny** elsewhere; **`vault_configure` ask for all four base agents and deny elsewhere**. Reads are `allow` roster-wide because a prompt in front of a read that `read_file` already permits protects nothing and trains the operator to click through the prompts that do.
- **FR-080b** **NEW, revision 4; TRACED, revision 6 (review round 6, M-37) — it appeared in no §6 row and no §7 test, and it is the only seeded-policy requirement with no assertion behind it, while being a security posture with a stated cost. It joins the FR-080 traceability row and §7 test 17.** Workers are **`deny` on all six**, reads included, and the reasoning MUST be recorded rather than assumed: the reads-are-`allow` argument turns on `allow` versus `ask` and is about **prompting**, not **granting**. `deny` removes the capability instead of removing a prompt. Workers are delegation-only executors whose task comes from a parent that has already done the vault reading, so granting them the vault surface widens what a delegated sub-turn can reach for no gain in what it can accomplish. **The cost is real and is accepted:** a worker that genuinely needs a note reaches for `read_file`, and that read leaves the audited boundary. The remedy is the one available for every seed — an operator flips it. This is a default, not a wall.
- **FR-083** **WIDENED, revision 4.** `vault_edit`, `vault_restructure` and **`vault_configure`** MUST be **independently settable**, and a test MUST prove **all three** policies are independently settable — specifically that an operator can permit editing while forbidding restructuring **and** forbid schema authoring while permitting both. **This fixes a live defect:** today `knowledge_rename` and `knowledge_move` sit in the same `ask` bucket as `knowledge_append_section` (`pkg/coreagent/core.go:800-808`, `pkg/config/defaults.go:637-646`), despite the first two rewriting inbound wikilinks across the whole vault and the third appending to one file. **And revision 4 states the defect more strongly, because it is worse than revision 3 said:** the same-`ask`-bucket claim holds for **Mia** (`pkg/coreagent/core.go:1056-1058`) and **Ray** (`:1149-1151`) only. For **Ava** (`:976-978`), **Jim** (`:1296-1298`) and the **global ceiling** (`pkg/config/defaults.go:644-646`) all three are **`allow`** — vault-wide restructuring outright permitted with no prompt at all. So the defect is not "the prompt does not distinguish"; it is **"for two of the four base agents there is no prompt"**, and FR-080a is therefore a **tightening** for Ava and Jim, not a re-labelling.
- **FR-084** Every retired `knowledge_*` name MUST be removed from the catalog (`pkg/coreagent/core.go:475-482`), from the global ceiling (`pkg/config/defaults.go:637-646`) and from every per-agent seed in the same change. A name left behind in a seed map is a policy entry for a tool that no longer exists, which is a coverage gap wearing a valid-looking entry.
- **FR-081** A test MUST assert **zero repaired pairs** on a fresh install — not zero gaps after repair.
- **FR-082** The global tool-policy ceiling for every `vault_*` tool MUST be stated explicitly in the seed (`pkg/config/defaults.go`). Repair backfills a *missing agent entry* to `deny`; what can silently grant is the **global ceiling**, which the seed sets per tool. Revision 1's rationale ("absence grants in a sparse map") named the wrong mechanism.
- **FR-090** Every wire type MUST be defined in `contracts/` before Go or TS code exists: the record-model types `RecordSchema`, `RecordType`, `PropertyDef`, `RecordQueryRequest`, `RecordQueryResponse`, `RecordWriteRequest`, `RelationWriteRequest`, `ViewDef`, `ValidationReport`, **plus the tool envelopes** `VaultDescribeResponse`, `VaultFindRequest`/`VaultFindResponse`, `VaultReadResponse` (carrying the version token FR-074 requires), `VaultEditRequest`, `VaultRestructureRequest`, **`VaultConfigureRequest`** (revision 4, ADR-068 D19), and the index-state snapshot FR-020f requires — which MUST reuse the schema of the existing `knowledge_index_progress` frame rather than declaring a parallel one.
- **FR-091** The completeness verdict and problem list MUST be required fields in the response schema. A client MUST NOT be able to receive records without also receiving the completeness verdict.
- **FR-092** FR-120's compact rendering MUST NOT weaken FR-090. The wire type stays contract-defined, generated into `pkg/api/generated/` and `src/lib/api/generated/`, and verified by `make verify-contracts`; the text the model reads is a **projection of that validated object** at the tool-result boundary. These are two surfaces and only one of them changes.

### Retrieval and ranking (ADR-068 D21)

- **FR-110** **The scoring model MUST be set to BM25 explicitly.** `DefaultScoringModel = TFIDFScoring` (`bleve_index_api@v1.4.1/indexing_options.go:37`) and `ScoringModel` is assigned **nowhere** in `pkg/` — a repository-wide grep returns zero assignments — so every vault search, every memory-room recall and every long-term-memory query ranks with TF-IDF today. **THIRTEEN locations claim otherwise, not seven — CORRECTED, revision 5.** *(Revision 4 carried revision 5-of-the-ADR's count of seven; ADR-068 D21.1 revision 6 enumerated thirteen and stated in place that both seven and the round-5 review's twelve were undercounts. This spec did not sync, and FR-110 was absent from §0's list of FRs whose meaning changed. Re-verified independently for this revision: `ScoringModel` returns **zero** matches across every `.go` file in the tree, and `bleve_index_api v1.4.1` is the version actually in the build (`go.mod:90`), with `bleve/v2@v2.6.1/index_impl.go:710` and `search/scorer/scorer_term.go:154` as the consumption points that fall back to the default.)* The thirteen are ADR-068 D21.1's enumerated table: `pkg/knowledge/index.go:164`, `index.go:1062`; `pkg/memrooms/index/index.go:19`, `:67`, `:249`, `:250`, `:267`; `pkg/agent/memory.go:301`, `:620`, `:674`, `:746`; `pkg/agent/retro_bm25.go:14`, `:24`. The last two matter beyond hygiene: `retro_bm25.go` hand-rolls BM25 with `k1=1.2, b=0.75` **specifically so retrospective ranking is commensurate with bleve's**, and bleve is not producing BM25 scores at all. The fix is one line and it **changes rankings**, so it ships inside W2 with the fielded-indexing work it affects — not as a detached one-liner.
- **FR-110a** **CORRECTED to thirteen, revision 5.** The stale claims above MUST be corrected in the same change. A comment asserting a scoring model the code does not set is how this defect survived.
- **FR-110b** **NEW, revision 5 — one of the thirteen is not a doc comment and MUST NOT be swept up with them.** `pkg/agent/memory.go:301` is a runtime `logger.WarnCF` that reaches an operator reading logs: *"roomIndex: failed to open bleve index; BM25 disabled for room"*. It tells them a fallback happened and implies BM25 was what they had. **They never had it.** Fixing a stale doc comment is hygiene; **fixing operator-facing output is a user-visible correction and MUST be described that way in the changelog**, not folded into a documentation sweep. FR-110a's wording ("doc comments") does not reach a log string, which is why this is its own requirement.
- **FR-111** The index MUST hold **distinct fields** — title, name, headings, property keys, property values, body — rather than one flattened body. **Frontmatter MUST be stripped from the body field.** There is no frontmatter-stripping step in the indexing path today, so `status: prospect` enters the body as the loose tokens `status` and `prospect`; a search for "prospect" then returns the note and reports `Complete: true`, which is the system confidently answering a question it cannot represent. `indexDoc` is a closed five-field struct (`pkg/knowledge/index.go:583-589`), so this is green field — there is nothing to migrate away from.
- **FR-112** **CONDITIONAL, revision 5 — this was a MUST that FR-113 may forbid, which is not a requirement.** Ranking MUST fuse four signals with **Reciprocal Rank Fusion** **if and only if FR-113's comparison clears its stated threshold; otherwise the default ranking is plain BM25F and this FR does not ship.** *(Revision 4 stated FR-112 unconditionally and FR-113 as a gate that could forbid it, so the two requirements contradicted each other in the ordinary case where the fusion does not win. The conditionality now lives in FR-112's own text, where an implementer reads it.)* The signals are: BM25 over weighted fields (BM25F-style, title and name above body), exact/prefix name match, recency, and backlink degree. RRF operates on **ranks, not scores**, so no cross-signal normalisation is required — normalising a BM25 score against a recency score against a degree count is a tuning problem with no principled answer, and RRF removes the question instead of answering it badly.
- **FR-113** **MEASURABLE, revision 5 — revision 4 stated a ship/no-ship gate with no metric, no k, no threshold and no judge, which made "a fusion that does not beat plain BM25 MUST NOT ship" unfalsifiable.** The fusion MUST ship behind a measured comparison against plain BM25F, and the comparison is now specified in full so it can fail:
  - **Metric: nDCG@10.** Chosen over MRR because the vault case is "give me the right handful", not "give me the one right answer", and nDCG rewards getting a good set into the visible page rather than only the top hit.
  - **Corpus: the §7 fixture corpus**, which MUST hold at least 500 notes and at least 4 record types, and MUST be committed rather than generated per run.
  - **Query set: 30 queries**, written **before** either ranking is run and committed alongside the corpus, each with a graded relevance judgement (0/1/2) over the union of both rankings' top 20. Written by a human, reviewed by a second.
  - **Threshold: mean nDCG@10 must improve by at least 0.03 absolute**, and the improvement must hold at the **10th percentile** of the per-query distribution — i.e. the fusion must not win on average by badly damaging a minority of queries.
  - **Decision: the architect records the verdict**, with the run and the per-query table attached. A result inside the threshold is a **no-ship** for FR-112, not a judgement call.
  - **Holdout scenario 11's human judgement over 20 queries is a DIFFERENT and weaker criterion and is not this one.** It stays in §9 as a sanity check; it MUST NOT be substituted for this measurement, and §9 is explicitly not for use during development.
  - **SC-018 and §7 test 28 are rewritten against this**, because as revision 4 wrote them they recorded a comparison without asserting anything about it.
- **FR-114** Query expansion (RM3 / pseudo-relevance feedback) MUST NOT run by default. It MAY run **only on an explicit retry**. Two independent reasons, either sufficient: PRF assumes first-pass precision and amplifies error when it is absent; and silently expanding a query answers a question nobody asked, which is the failure this specification exists to remove.
- **FR-115** On zero hits the system MUST surface **near-miss vocabulary from the index** — `nearest indexed terms: prospect, prospects, prospecting` — and MUST NOT reformulate on the caller's behalf. See §4.2's zero-hit example.
- **FR-116** Before any Go-side ranking ships, the system MUST **either** thread one shared token function through the ranking path **or** record an explicit decision that the Go pass is deliberately unstemmed, stating why and what it costs. **Silence is the choice being made today.** Three notions of a term coexist: `bm25Tokenize` (`pkg/utils/bm25.go:216`) splits on whitespace and trims **edge** ASCII punctuation, so `"don't"` survives whole; `retroTokenize` (`pkg/agent/retro_bm25.go:71`) splits on every non-letter/non-number rune, so `"don't"` becomes `["don","t"]`; bleve's `en` analyzer applies Unicode segmentation, Porter stemming and stopword removal. This is harmless only while the three never rank the same corpus. Its symptom when they do is the worst kind: a document that matched during selection scores as though the query term were absent — no error, just a quietly wrong ranking. The shared function threads through `NewBM25Engine` (`pkg/utils/bm25.go:72`; existing caller `pkg/tools/search_tool.go:41`).
- **FR-117** **No embeddings, permanently.** This is not a footprint concession. A 5,000-note vault is roughly 0.5–3M corpus tokens, an order of magnitude inside the regime where an agentic loop over good lexical ranking wins: Claude Code dropped its vector index because agentic lexical search retrieved code better; *"Is Grep All You Need?"* (arXiv 2605.15184) found inline grep beat inline vector retrieval on **every** harness-model pair tested, by up to 86.2% vs 62.9%; *"BM25 Wins at Scale"* (arXiv 2607.26497) puts the crossover far above this corpus size. We would choose lexical retrieval here with an unlimited budget. *(Cited findings, not independently reproduced by us; the vault-token estimate is ours.)*

### The response the model reads (ADR-068 D22)

The evidence for treating this as mechanism rather than presentation: changing result delivery
from inline to file-based collapsed accuracy from **93.1% to 55.2%** (arXiv 2605.15184) — as
large a swing as replacing the retriever. A system that finds the right notes and renders them
badly is a worse retrieval system.

- **FR-120** A tool result rendered for the model MUST be compact text. It MUST be produced **from** the validated wire object (FR-090) at the tool-result boundary — a projection of the contract, never a parallel hand-written structure, and never a replacement for the wire type (Hard Constraint #8).
- **FR-121** The completeness verdict MUST be the **first line** of every response. It MUST NOT appear only after the rows: a model that has read 40 rows has already begun composing an answer, and a caveat arriving after them competes with a conclusion rather than preventing it.
- **FR-122** The **query as executed** MUST be echoed in the header, including any clamp, coercion or default the system applied. A caller MUST be able to see that its `limit: 500` ran as 200.
- **FR-123** An exclusion MUST be named **with its fix**, inline — `CO-0052: arr is '50k' where a number is required — write 50000`. `3 records excluded` alone fails this requirement.
- **FR-124** A value pulled through a relation MUST be rendered as **borrowed** — `company [[Acme]]: active` — and MUST NOT be merged into the row's own columns. The row is one real file; blurring that is how an agent comes to believe a property exists on a note that does not have it.
- **FR-125** Every total MUST state its scope — `sum(arr) = 673,000.00 over 12 of 12 rows`. A bare number MUST never be returned. *(Revision 5: the example loses its currency clause with `money` — FR-014.)*
- **FR-125a** **NEW, revision 5.** An aggregate MUST be computed over the **full evaluated set**, never over the rendered page, and the scope line MUST say which set it covers. *Revision 4's §4.2 example reported `over 5 of 12 rows` against a header stating 17 selected and 14 evaluated — a page-scoped number labelled a total, which is a wrong answer to the question ADR-068 §1.2 motivates. Nothing in revision 4 stated which set an aggregate covered.* Where the two differ — a clamp, a cursor, a budget truncation — the scope line names the **evaluated** count and the header names the rendered one, and a test asserts they are computed from different numbers.
- **FR-126** Every response MUST end in **addressable next actions**: at least one concrete call the caller can make next, with its arguments filled in. In an agentic loop each response is a prompt for the next call; a response that terminates in data terminates the loop.
- **FR-127** **MEANING CHANGED, revision 4 (ADR-068 D22.7): the unit is BYTES, not tokens.** Budgets: **~200–320 bytes per hit**, **~4,000 bytes** per response by default, **16,000 bytes hard cap**; `detail: minimal` renders **~80 bytes per hit**. Truncation to meet a budget MUST be stated in the header (FR-121's line), never applied silently. *Previously ~50–80 tokens/hit, ~1,000 default, 4,000 hard cap. **The figures are the same intent at a conservative ~4 bytes/token; only the enforceable unit changed.** The reason is FR-116: three notions of a term already coexist in this tree, none of them the serving model's, so a token cap enforced with any tokenizer we own would be wrong by an unknown margin in an unknown direction on every provider, and would silently change meaning whenever a provider changed tokenizer. Bytes are exact, provider-independent, and enforceable at the point of truncation. This is a budget for **rendering**, not an accounting of what the model is billed.*
- **FR-127a** **The response budget and the page-size cap still conflict, and the budget still wins.** *(Revision 3 raised this against ADR-068 revision 5's token figures; revision 6 changed the unit but not the arithmetic, so the requirement stands, restated in bytes.)* FR-063 permits a page of **200 results** while FR-127 caps a response at **16,000 bytes**; at ~200–320 bytes per hit a 200-row page is **40,000–64,000 bytes**, so the two bounds are not simultaneously satisfiable — and the default page of 50 reaches the cap at the top of the per-hit range. The system MUST therefore degrade in a stated order and **report every step of it in the header**: render at `standard` until the budget is reached, then drop the remaining rows to `minimal`, then stop and page. `limit` is a bound on rows **selected**, never a promise about rows **rendered**. A response that silently returns fewer rows than the budget forced is the truncation failure this specification exists to remove, committed by the renderer instead of the index. **The ladder as revision 4 wrote it did not close, and revision 5 says so:** it is written entirely about **rows** and never mentions problems, totals or the header, every one of which is a MUST (FR-121, FR-122, FR-125, FR-025, FR-026, FR-126); and at `minimal`'s ~80 bytes/hit a 200-row page is **16,000 bytes — the entire cap, with zero bytes left for any of them.** FR-127c fixes it by reserving before it renders.
- **FR-127c** **NEW, revision 5. The byte budget is ALLOCATED IN A STATED ORDER, not consumed first-come.** Rendering MUST reserve in this order and MUST NOT begin the next block until the previous one fits:
  1. **Mandatory header** — `COMPLETE:` (FR-121), `QUERY:` (FR-122), `INDEX:` (FR-020c). **Reserved and never truncated.** Budget: ~600 bytes.
  2. **`NEXT`** (FR-126). **Reserved and never truncated**, because a response that terminates in data terminates the loop — dropping `NEXT` to fit rows would sacrifice the block whose whole job is recovery. Budget: ~400 bytes.
  3. **`TOTALS`** (FR-125), if an aggregate was requested. Reserved. Budget: ~200 bytes per aggregate.
  4. **`PROBLEMS`**, up to FR-026's 20-record clamp plus the mandatory "showing N of M" line. Budget: ~1,600 bytes.
  5. **Rows**, at `standard` until the remaining budget is reached, then the remainder at `minimal`, then stop and page — the ladder FR-127a states, now running against **what is left** rather than against the whole cap.

  **Every step that fires MUST be named in the header line**, so the caller can tell a short page from a short answer. At the 16,000-byte cap this leaves roughly 13,200 bytes for rows: ~41 rows at `standard`'s top-of-range 320 bytes, or ~165 at `minimal`. **The default page of 50 therefore fits at `standard` only at the low end of the per-hit range**, which is why the ladder exists and why `limit: 200` is a selection bound the renderer will not honour — both now arithmetically demonstrated rather than asserted.
- **FR-127b** **RESOLVED, revision 4.** The unit is **bytes of the rendered UTF-8 response**, it is named here, and it is the unit the tests measure. *Revision 3 required only that the unit be named, and flagged A-8 because ADR-068 revision 5 left it unnamed — budgets enforced against an unnamed unit are decorative. **ADR-068 revision 6 resolves it in this spec's favour** and for this spec's stated reason. A-8 is closed.* One consequence is normative and easy to lose: **FR-079's and FR-128's ~150-token description budget stays in TOKENS**, because it is a design guideline for a human writing prose and is never enforced at runtime. Runtime enforcement is bytes; authoring guidance is tokens; a test MUST NOT conflate them.
- **FR-128** **CORRECTED, revision 5 — the number is right for what it measures and wrong for what it was used to argue.** Each tool description MUST fit ~150 tokens (FR-079). **Six** tools at that budget is **~900 tokens of DESCRIPTION PROSE** — and that is **not** the surface's standing per-turn cost, because the whole parameter schema ships with it on every request (FR-079's correction, verified at `pkg/tools/registry.go:557-560`). The ~900 is a **floor**. The comparison against ~2,700 for nine `record_*` plus nine `knowledge_*` is still directionally right — eighteen prose budgets against six — but **it MUST NOT be quoted as the affordability argument** until W5 measures the serialised definition set. *(ADR-068 D15.6 cites "~750 for the surface as a whole", which is the **five**-tool figure and is stale as well as measuring the wrong thing; the ADR's revision 7 corrects both.)* *(The sixth tool's whole standing price is those ~150 tokens — ADR-068 D22.8. Tool **count** costs selection accuracy; tool **descriptions** cost tokens on every turn forever, for every agent holding the tool, whether or not it is ever called. `vault_configure` is selected only when an agent is authoring a type or a view, an intent distinct enough that it competes with nothing — the tools that hurt selection are near-synonyms.)*

### Import

- **FR-100** **MEANING CHANGED, revision 3 (ADR-068 D15.4).** The saved-view importer MUST be an **operator/CLI one-shot**, NOT an agent tool. It MUST translate the filters, order and grouping it recognises from a `.base` file into a native view. *Previously an agent tool named `record_view_import`. Cited by §6, §7 test 20 and SC-010; all restated. The resolution of ADR-068 O-1 is unchanged — only the delivery surface moves, because FR-101's verbatim report exists to be read and judged by a human, which is a UI act with a person in it by definition.*
- **FR-101** An expression it cannot translate MUST be reported verbatim, and MUST NOT be approximated or silently dropped.
- **FR-102** The importer MUST be one-shot; `.base` files MUST NOT be read on the query path.
- **FR-103** The importer MUST NOT appear in `allStaticToolNames` and MUST NOT hold a tool-policy entry. A permanent slot in the static catalog is paid by every agent on every turn; a one-shot translation run by a human is not worth that price.

---

## 4.1 Tool specifications — normative

The **six** tools below are the **whole** agent-facing surface of the vault. Parameters are
normative: a name not listed here does not exist, and an argument the schema does not declare is
rejected with the accepted argument names listed (the same posture FR-024 takes for an unknown
property).

Every response is compact text (FR-072). Every response begins with its completeness verdict
(FR-121) and ends with next actions (FR-126). Errors are returned as a **refusal the model can
act on**, never as an empty success.

### 4.1.1 `vault_describe` — READ, orientation and integrity

*(**R-F marker.** Every record-type, property and value name below is an **illustration of what a
vault might define** — see R-F. The product ships none of them. **What a test asserts is the SHAPE
and the remedy clause**, against a fixture schema the test itself declares — not these particular
words. The **property TYPE** names — `text`, `enum`, `relation`, `date`, `integer`, `decimal`,
`person` — are the exception: those are ours and are shipped, FR-004.)*

*(Marker added revision 6, review round 6, M-25 — §4.1.2 carried it and this section did not, which left four normative tables reading as though their vocabulary were contract.)*

The mandatory cheap first call. An agent that has not called it is guessing at property names,
and a guessed property name is the failure FR-024 exists to prevent.

| Parameter | Type | Default | Meaning |
|---|---|---|---|
| `collection` | string | all in scope | Narrow to one collection. Unknown name → refusal listing the collections in scope. |
| `record_type` | string | — | Return the full property table for one type only. |
| `include` | list of `types \| views \| templates \| index` | all | Trim the response. |
| `check_integrity` | bool | `false` | Run the integrity sweep (FR-075). Scoped by `collection` / `record_type` when either is given (FR-075a). |
| `detail` | `minimal \| standard` | `standard` | `minimal` omits property descriptions and enum value lists. |

**Response sections, in order:** index freshness → collections → record types (name, label, **id
prefix — see FR-036b**, property table: name, **type rendered DISTINCTLY for `integer` and
`decimal`**, arity, required, **enum values, as declared — the set is UNORDERED and a reader MUST
NOT infer a sort order from the response's own sequence**) → saved
views → templates → integrity findings when requested.

**Integrity findings** are grouped by kind and every finding names a path. **Five categories, each
capped at 500 findings (FR-075a):**

```
INTEGRITY: 6 findings (scope: whole vault, 4,182 notes swept)
  duplicate id   CO-0142 — Companies/Acme.md and Companies/Acme (old).md; neither is preferred
  unresolved     DEAL-0091 company -> [[Acme Corp.]] — no note resolves; nearest: Companies/Acme Ltd.md
  wrong type     DEAL-0104 company -> [[Q3 planning]] is a note of type meeting, expected company
  broken link    Notes/2026-08-14.md -> [[Q2 retro]] — no note resolves (ordinary wikilink, not a relation)
  orphan         Notes/scratch.md — no note links to it and it links to none
  orphan row     properties index holds DEAL-0221 at Deals/old.md; no note exists at that path
```

**Normative clamp and refusal wording:**

| Condition | Message |
|---|---|
| Category clamped (FR-075a) | `broken link: showing 500 of 1,842 — narrow with collection=<name> or record_type=<name>` |
| Sweep too large (FR-075a) | `collection 'Archive' holds 214,900 notes; the sweep limit is 100,000 — run check_integrity with record_type=<name>, or on a narrower collection` |
| No properties index on this build (FR-020h) | `typed integrity checks are unavailable on linux/mipsle: this build has no properties index. Duplicate identifiers, relation targets and orphan rows cannot be checked here; wikilink and orphan checks still run` |

- **AC-D1** — `check_integrity` on a vault with a duplicate identifier names **both** paths and
  states that neither is preferred (FR-039, ADR-068 D7.1).
- **AC-D2** — `vault_describe` with an unknown `record_type` is refused with the declared type
  names listed; it does not return an empty description.
- **AC-D3** — the response never contains a JSON object (FR-072).
- **AC-D4** — an unscoped `check_integrity` over a collection above the sweep limit is **refused**, naming the collection and the scoped remedy; it never returns a partial sweep presented as whole (FR-075a).
- **AC-D5** — a vault with a broken **ordinary wikilink** and no records at all still produces a finding. This is the capability `knowledge_graph`'s retirement would otherwise have lost (FR-075).
- **AC-D6** — on a build without SQLite, `check_integrity` states which categories it could not run **by name**, and does not report zero findings for them (FR-020h).

### 4.1.2 `vault_find` — READ, the one retrieval path

Absorbs `record_query`, `record_explain`, `knowledge_search`, `knowledge_tasks` and
link-neighbourhood traversal. There is **no second retrieval tool**.

| Parameter | Type | Default | Meaning |
|---|---|---|---|
| `words` | string | — | Free text, ranked per FR-112. |
| `type` | string | — | Record type. Unknown → refusal listing declared types. |
| `kind` | `note \| record \| task \| attachment` | `note` | `task` replaces `knowledge_tasks`. |
| `filter` | object | — | Structured only (FR-022). Shape below. |
| `view` | string | — | A saved view, applied first; `filter` refines it. |
| `near` | string | — | A note path or wikilink. |
| `hops` | int 1..2 | `1` | Only with `near`; 3+ is refused (FR-065). |
| `join` | list of relation names | — | Borrowed columns (FR-124). |
| `group_by` | list, max 2 | — | Two levels (FR-027). |
| `sort` | list of `{property, direction}` | relevance | **CHANGED, revision 5 (ruling R-E); MECHANISM CORRECTED, revision 6.** An enum sorts **lexically** — the same order SQLite's `BINARY` collation would produce, **computed in Go by the comparator** (R-5, FR-021), not by an emitted `ORDER BY`. The sort key is the **folded** form, so `Won`/`won`/`WON` sort together as they group together (R-5 clause (c)); ties on the folded key break on raw bytes, so the order is deterministic (O-5). A domain order is expressed by prefixing the declared values (`1-lead`, `2-qualified`) — FR-010. *(Revision 5 said "lexically, like SQLite", which read as a delegation.)* |
| `select` | list of property names | schema order | Columns to render. |
| `aggregate` | list of `{op, property}`; `op` ∈ `count, sum, min, max` | — | Scoped totals only (FR-123). |
| `explain` | bool | `false` | Report the plan; evaluate nothing (FR-073). |
| `limit` | int | `50` | Clamped at 200, clamp reported (FR-063). |
| `cursor` | opaque string | — | An unhonourable cursor is an error, never a silent restart. |
| `detail` | `minimal \| standard` | `standard` | `minimal` ≈ 80 bytes/hit (FR-127). |

**Filter shape — REWRITTEN, revision 5 (operator ruling R-B).** A tree of `{all: [...]}`,
`{any: [...]}`, `{not: {...}}` over leaves `{property, op, value}`, with `op` drawn from **SQL's own
vocabulary**:

| `op` | Meaning | Notes |
|---|---|---|
| `=` | equal | case-insensitive on text and enum labels (FR-011a); element-wise on a `many` property (R-9) |
| `<>` | not equal | **CORRECTED, revision 6 (C-7): a `<>` leaf does NOT match an absent property.** R-2 governs it — an absent side makes every operator except `IS NULL`/`IS NOT NULL` `false`, which is SQL's own semantics and therefore ruling R-B's. To include absent records, use `{not: {p, "=", v}}` (a negative **tree**, R-2-correct at any depth) or `{any: [{p, "<>", v}, {p, "IS NULL"}]}`. *(Revision 5's row said the opposite and cited FR-008, whose phrase "a negative filter" was ambiguous between a leaf and a tree.)* |
| `<` `<=` `>` `>=` | ordered comparison | undefined against a `many` property and reported as a problem (R-13) |
| `LIKE` | pattern match, **anchored to the whole value** | `%` and `_` are SQL's wildcards, `\` escapes. Case-insensitive. **A pattern with no unescaped wildcard is exactly `=`** — `LIKE 'Bracken'` selects what `= 'Bracken'` selects, never a substring (FR-022b; **ADDED in Draft 7, UAT F-2.4 — this table is the description a caller reads and it was silent on the one case the tester was told to record**). An empty pattern or a bare `%` is refused (FR-022a) |
| `IN` | membership in a supplied list | `value` is a **non-empty** list; **an empty list is REFUSED** (FR-022d). Case-insensitive, being `=` over a set. A single-element list means `=` |
| `IS NULL` | the property is absent | the one operator absence does not make `false`; exempt from FR-008 |
| `IS NOT NULL` | the property has a value | an empty string, an empty list and a zero are values (R-3) |

*Previously `is, is_not, lt, lte, gt, gte, contains, is_absent, is_present` — names we invented,
which have appeared in a model's training data zero times. **The filter is still a structured object
and there is still no parser and no text query language** (FR-022, ADR-068 O-3, amended not
overturned); only the operator spelling changes. Any other SQL construct — `JOIN`, a subquery,
`COALESCE`, `CASE`, `BETWEEN`, a function call — is **refused naming the supported set and the
parameter that does the job** (FR-022c), never parsed and never silently dropped.*

**Normative refusal wording.** These strings are contract, not illustration; a test asserts them.

*(**R-F marker.** Every record-type, property and value name below is an **illustration of what a
vault might define** — see R-F. The product ships none of them. **What a test asserts is the SHAPE
and the remedy clause**, against a fixture schema the test itself declares — not these particular
words. The **property TYPE** names — `text`, `enum`, `relation`, `date`, `integer`, `decimal`,
`person` — are the exception: those are ours and are shipped, FR-004.)*

| Condition | Message |
|---|---|
| Unknown property | `unknown property 'ownr' on record type 'company'; declared: name, status, segment, owner, website, arr` |
| Unknown enum value | `'Wonn' is not a value of deal.status; permitted: lost, open, won` *(revision 5: "in order" is dropped — R-E makes ordering lexical, so the list is simply the declared set, and `'Won'` is no longer a refusal because R-D resolves it case-insensitively to `won`)* |
| Ordering operator on a `many` property (R-13) | `segment holds many values; ordering comparisons are not defined over a list — use =, IN or LIKE` |
| Unsupported SQL construct (FR-022c) | `'BETWEEN' is not a supported operator; supported: =, <>, <, <=, >, >=, LIKE, IN, IS NULL, IS NOT NULL — express a range as two leaves, >= and <=` |
| Unsupported SQL construct, parameter remedy (FR-022c) | `'JOIN' is not expressible in a filter; use the join parameter to borrow columns through a relation, or group_by to group by one` |
| Empty `LIKE` pattern (FR-022a) | `a LIKE pattern of '' or '%' matches every record; use IS NOT NULL if that is what you meant` |
| **Empty `IN` list (FR-022d)** — **ADDED in Draft 7 (UAT F-3.4)**; the requirement existed and its refusal string did not, in the one table a tester reads to know what to expect | `IN was given an empty list, which can match nothing; supply at least one value, or use IS NULL to select records with no value` |
| **A literal that cannot be interpreted in the property's declared type (FR-022e)** — **ADDED in Draft 7 (UAT F-5.5)** | `label is declared text and the value 5 is a number; compare it against a quoted string, or use IS NULL / IS NOT NULL` |
| Third hop | `hops=3 exceeds the limit of 2; run a second vault_find from one of these results` |
| Candidate cap **B2** (survivors), row-returning query | **CORRECTED, revision 6 (C-4) — revision 5's string named a count no bound produces and a remedy that does not reduce it.** `this query matched more than 10,000 records; the limit is 10,000 — add or tighten a filter, or ask for a total instead (an aggregate-only query returns no rows and is exempt — FR-064a)`. **The string does NOT quote an exact survivor total, because the count aborts at the cap and does not continue to a true total — quoting one would be a number nobody computed.** |
| Candidate cap **B1** (evaluation), any typed query | **NEW, revision 6 (C-4).** `this query would evaluate 60,412 candidate records of type <T>; the limit is 50,000 — narrow the scope to a collection or path, or narrow the kind`. **This count IS exact and IS taken before retrieval** (one index-bound aggregate over the narrowing predicates), so quoting it is honest. **It does not name "add a filter" as the remedy**, because a filter does not change what B1 counts |
| ~~old candidate-cap string~~ | ~~`this query selects 24,180 records; the limit is 10,000 — add a filter on status (7 values) or a narrower type. An aggregate-only query (no select, no limit) is exempt` |
| Aggregate over a refused set | `no total is returned over a refused candidate set` |
| Bad date format (FR-021d) | `closed_on is '03/04/2026'; the date format is ambiguous and will not be guessed — write 2026-04-03 or 2026-03-04` |
| Unpadded date (FR-021d) | `closed_on is '2026-9-1'; month and day must be zero-padded — write 2026-09-01` |
| Integer out of range (FR-012) | `headcount is 9223372036854775808; an integer property holds at most 9223372036854775807 — declare the property as decimal if the value is genuinely larger` |
| Decimal scale exceeded (FR-013) | `ratio has 140 decimal places; a decimal property carries at most 100. The value is not rounded to fit — shorten it` |
| Stale cursor | `that cursor was issued against index_epoch 8802; the index is now at 8814 — re-run the query` |
| Stale record (FR-020c) | `DEAL-0117: the properties index and the note on disk disagree (indexed 3f9a…, on disk 71c4…) — this record is being re-indexed; re-run to confirm` |
| No manifest entry (FR-020c) | `DEAL-0221: no indexed note at Deals/old.md — the row is orphaned; run vault_describe check_integrity` |
| Typed query on a SQLite-less build (FR-020h) | `typed filters are unavailable on linux/mipsle: this build has no properties index. Plain-word search and vault_read still work` |

- **AC-F1** — every refusal above names the remedy in the same string. A refusal that states only
  what went wrong fails this criterion.
- **AC-F2** — `near` composed with `words` and `filter` returns the intersection. A test asserts a
  record inside the hop radius but failing the filter is **absent**, and one matching the filter
  but outside the radius is **absent** (FR-076).
- **AC-F3** — **STRENGTHENED, revision 5 — as worded it passed for a constant-returning stub.** `explain: true` MUST (a) return a plan naming **every property the query touches and the index each will be answered from**, so a stub returns the wrong thing rather than nothing; (b) be **unchanged by a corpus mutation** between two identical calls, over a mutation **chosen to change the plan if evaluation were happening** — a mutation that happens not to change the plan proves nothing and the fixture must exclude that case; and (c) perform **zero** candidate retrievals, asserted at the store boundary by the same counter AC-8.4 uses. *(Revision 4 asserted only (b), unqualified.)* **Nothing may differ between the two calls, `index_epoch` included.** *(Revision 6, m-8: revision 5 said "only `index_epoch` may differ". **`explain` EVALUATES NOTHING**, so it should not observe an epoch at all — the epoch belongs to the response header FR-020c places it in, and a plan is not a result. **Ruling:** an `explain` response carries the plan and the request's identity fields and **not** `index_epoch`; two `explain` calls over an unchanged schema are byte-identical. This also settles unasked question 5 — see §7 test 86.)*
- **AC-F4** — **REWORDED, revision 5 — "and no other signal" was unsatisfiable.** An out-of-scope vault yields `COMPLETE: yes — 0 records` and **no signal distinguishing scoping from an empty vault** (FR-062). *Revision 4's "no other signal" contradicted FR-122's mandatory `QUERY:` echo and FR-126's mandatory `NEXT` block, both of which §4.2's own zero-hit example carries.* **And the carve-out ADR-068 D13.1 records MUST be stated here rather than left to be discovered:** workspace scoping is the **one exception** to §3's headline *"names what it excluded"* guarantee — a caller can receive `complete: true` over zero records while records exist. That is deliberate (FR-062 requires it) and it means the completeness verdict is honest about everything **except** scope. D13.1's own reasoning applies: an unstated exception to a headline guarantee is how a guarantee stops being believed. **WHERE IT IS STATED, revision 6 (M-29): the `COMPLETE:` line's own reference documentation, alongside FR-020c1's exception, with a one-clause pointer from `vault_find`'s tool description.** Two exceptions, one place, one assertion — because two carve-outs each filed nowhere is how a reader concludes there are none.
- **AC-F5** — **CORRECTED, revision 6 (review round 6, C-2 / V-3). As revision 5 wrote it, this MANDATORY criterion tested the mechanism FR-020c disproved: it passed only if the implementation IGNORED FR-020c and failed if it obeyed it.** A record whose row `source_hash` differs from **the bleve document's stored `source_hash`** is returned **with**
  `COMPLETE: no`, named in `PROBLEMS` with staleness as the reason, in **both** divergence
  directions (FR-020c). A record with no manifest entry is flagged the same way.
- **AC-F6** — on a build without a properties index, a typed filter is **refused by name**; a test
  asserts the response is not an empty success (FR-020h).
- **AC-F7** — `kind: task` returns rows carrying `path`, `line`, `status` and `text`, each
  rendered with its line number, and no collection walk occurs (FR-076a).

### 4.1.3 `vault_read` — READ, a note or one section of one

*(**R-F marker.** Every record-type, property and value name below is an **illustration of what a
vault might define** — see R-F. The product ships none of them. **What a test asserts is the SHAPE
and the remedy clause**, against a fixture schema the test itself declares — not these particular
words. The **property TYPE** names — `text`, `enum`, `relation`, `date`, `integer`, `decimal`,
`person` — are the exception: those are ours and are shipped, FR-004.)*

*(Marker added revision 6, review round 6, M-25 — §4.1.2 carried it and this section did not, which left four normative tables reading as though their vocabulary were contract.)*

| Parameter | Type | Default | Meaning |
|---|---|---|---|
| `path` | string | required | Note path within scope. |
| `section` | string | — | One heading. Unknown → refusal listing the headings present. |
| `include` | list of `frontmatter \| body \| links \| backlinks` | all | |
| `max_bytes` | int | 40,000 | Truncation is reported in the header, never silent. |

**Response:** version token first, then typed frontmatter (parsed per the record's schema, with
any non-conforming value flagged in place), then body or section, then links and backlinks
inline.

Refusals: `no section '## Pricing' in Deals/Acme.md; headings: ## Summary, ## Terms, ## Notes`.

- **AC-R1** — a successful `vault_read` carries a version token that `vault_edit` accepts
  unchanged (FR-074).
- **AC-R2** — no path exists by which an agent must send a failing write to obtain a token: a test
  drives read → edit with zero intervening failed writes.
- **AC-R3** — a note whose frontmatter violates its schema still **reads**, with the violation
  named per property. Reading is never blocked by a validation finding.

### 4.1.4 `vault_edit` — WRITE, one named file

*(**R-F marker.** Every record-type, property and value name below is an **illustration of what a
vault might define** — see R-F. The product ships none of them. **What a test asserts is the SHAPE
and the remedy clause**, against a fixture schema the test itself declares — not these particular
words. The **property TYPE** names — `text`, `enum`, `relation`, `date`, `integer`, `decimal`,
`person` — are the exception: those are ours and are shipped, FR-004.)*

*(Marker added revision 6, review round 6, M-25 — §4.1.2 carried it and this section did not, which left four normative tables reading as though their vocabulary were contract.)*

Writes **only** `path` (FR-070b). Every op **on an existing file** requires `expect_version`; **`create` takes none** and FR-043 exempts it, because there is no version of a file that does not yet exist. *(Revision 6, m-13.)*

| `op` | Additional parameters | Notes |
|---|---|---|
| `create` | `path`, `frontmatter` (map), `body` | Mints the identifier (FR-036) when `type` is a declared record type. |
| `set_property` | `path`, `property`, `value` (scalar **or list**), `expect_version` | Closes today's scalar-only limit (`SetProperty(key, value string)`, `pkg/knowledge/author.go:766`). |
| `append_section` | `path`, `heading`, `body`, `level` (default 2), `once` (bool), `expect_version` | Maps to `AppendSectionAt` / `AppendSectionOnce`. |
| `replace_body` | `path`, `anchor` **or** `line_range`, `body`, `expect_version` | **Unbuilt primitive** — FR-047. |
| `link` | `path`, `target`, `alias`, `section`, `relation` (property name), `expect_version` | Writes the **source** only; the inverse is derived (FR-032). |

*(Revision 4: `write_view` and `create_record_type` are **removed from this tool**. Both are
`vault_configure` operations — FR-016, FR-018. Revision 3 placed them here on the ground that a
new schema file reinterprets nothing, which is false under ADR-068 D1; see FR-016.)*

`link` belongs to this tier because a relation is stored once, on the source (FR-030). Linking
`Deal.md` to `[[Acme]]` writes `Deal.md` and never touches `Acme.md` — it looks like a two-file
operation and is a one-file operation.

**Normative refusal wording:**

| Condition | Message |
|---|---|
| Stale token (FR-043) | `Deals/Acme.md changed since you read it; you have v1:ab12…, current is v1:cd34… — vault_read it again and re-apply` |
| Multi-line clobber (FR-040b) | `segment currently spans 3 lines; a scalar write would delete them — no change made. Send a list value instead` |
| Ambiguous anchor (FR-047) | `anchor "## Pricing" appears twice in Deals/Acme.md (lines 14 and 88) — no change made; give a unique anchor or a line_range` |
| Schema violation (FR-042) | `deal.status holds one value; got a list of 2 — send a single value, or declare many: true in .omnipus-vault/records/deal.yaml` |
| Cascading op sent here (C-A) | `rename cascades to notes you did not name; use vault_restructure` |
| Schema or view op sent here (C-B) | `create_record_type changes what existing notes mean; use vault_configure` |

- **AC-E1** — after any successful `vault_edit`, **exactly one file has a changed mtime and hash — except on a `create` of a record, where exactly TWO do** (the note and `.seq`, FR-036a), and on no operation more than two. *(Revision 6, m-6: revision 5 stated the one-file rule unqualified and repaired it in AC-E2a two bullets later, while §7 test 49 asserts "exactly **two** paths change on a **record** `create`" and "exactly ONE on a non-record create". The exception is folded into AC-E1's own text, because an unqualified criterion is what a test gets written to.)*
  This is FR-070b as a test, and it is the tier's definition.
- **AC-E2** — `create_record_type` or `write_view` sent to `vault_edit` is refused, naming
  `vault_configure` (FR-016, FR-018). An agent holding `vault_edit: allow` and
  `vault_configure: deny` **cannot** create a record type — the posture revision 3 claimed and
  did not deliver.
- **AC-E2a** — the one accepted multi-file case is asserted rather than assumed: after a `create`,
  exactly **two** paths changed — the note and `.omnipus-vault/records/.seq` — and no third
  (FR-036a). AC-E1's one-file assertion is read with this exception, not against it.
- **AC-E3** — an ambiguous `replace_body` anchor leaves the file byte-identical.

### 4.1.5 `vault_restructure` — WRITE, cascades

*(**R-F marker.** Every record-type, property and value name below is an **illustration of what a
vault might define** — see R-F. The product ships none of them. **What a test asserts is the SHAPE
and the remedy clause**, against a fixture schema the test itself declares — not these particular
words. The **property TYPE** names — `text`, `enum`, `relation`, `date`, `integer`, `decimal`,
`person` — are the exception: those are ours and are shipped, FR-004.)*

*(Marker added revision 6, review round 6, M-25 — §4.1.2 carried it and this section did not, which left four normative tables reading as though their vocabulary were contract.)*

The only tool permitted to change a file the caller did not name.

| `op` | Additional parameters | Cascade |
|---|---|---|
| `rename` | `path`, `new_name` | Inbound wikilinks in N notes are rewritten (ADR-067 D10). |
| `move` | `path`, `dest` | Same. |
| `trash` | `path` | Inbound links **cannot** be repaired — FR-048. |

> **`expect_version` is REMOVED from all three, revision 5 — the table and AC-X3 said opposite
> things and the table was wrong.** Revision 4 declared `expect_version` on every row above while
> AC-X3 in the same section asserted *"`vault_restructure` declares **no** `expect_version` on any
> cascading op that cannot honour it"* — and **every** operation of `vault_restructure` is a
> cascading operation, that being the tier's definition. ADR-068 AC-15.5d sides with AC-X3
> (*"a test asserts no `expect_version` parameter is declared on either"*), and today's shipping
> code already behaves that way: neither `RenameTool.Parameters`
> (`pkg/knowledge/authoring_tools.go:852-871`) nor `MoveTool.Parameters` (`:904-927`) declares the
> field. **The table is corrected to match.** A single-file content hash cannot honestly guard a
> change whose blast radius is N notes, and a compare-and-swap that guards one of the things it
> affects is worse than none, because it reads as a guarantee.
>
> **FR-043 is scoped in the same change**, because as revision 4 wrote it — *"A write MUST carry
> ADR-067 D14's version token; a stale token MUST be refused"*, unqualified — it directly
> contradicted FR-018a's exemption for `vault_configure` and now this table too.

*(Revision 4: `edit_record_type` and `delete_record_type` are **removed from this tool** and are
`vault_configure` operations — FR-017. They cascade in **meaning** (C-B), not in bytes (C-A), and
ADR-068 D23.4 records the asymmetry: a schema change writes **one** file, so reverting that file
undoes it, which is not true of trash. Grouping the two would have implied a severity schema
authoring does not have.)*

**Trash stays in this tier, and revision 5 replaces the reason.** *Revision 4 said "recoverability
and blast radius are different axes, and only the second one decides the tier", which answers a
question nobody asked and never applies either criterion to `trash` at all.* Under C-A and C-B as
written, **`trash` is C-B, not C-A** — it moves one file and FR-048 explicitly does not repair
inbound links, so it writes bytes into no file the caller did not name, while breaking N notes'
relations without writing them. **It stays here because FR-070e's mechanical rule decides it: it
writes a note, and `vault_configure` writes only `.omnipus-vault/`.** Putting a note-destroying
operation behind the schema tool would mean an operator granting type authoring also grants
deletion. **So this tier is not "the C-A tier"** — that framing is withdrawn; it is the tier for
operations reaching beyond one named note, of which C-A is the common but not the only shape. The
full per-operation record is FR-070d.

**Normative refusal wording — NEW, revision 5.** *§4.1.5 had no refusal table at all, while §7 test
37 asserted that every schema and view op is refused by `vault_restructure` naming `vault_configure`
— a behaviour this section never specified.*

| Condition | Message |
|---|---|
| A schema or view op sent here (C-B) | `create_record_type changes what existing notes mean; use vault_configure` |
| A one-file note edit sent here | `set_property writes one note; use vault_edit` |
| Version token supplied (§4.1.5, AC-X3) | `vault_restructure takes no expect_version: a single-file token cannot guard a rename that rewrites inbound links in notes you did not name. Re-read with vault_read and re-send` |
| `restore` of a path not in the trash (FR-048a) | `no trashed note at Deals/Acme.md; vault_describe reports the trash contents` |
| Trash of an already-trashed path (FR-048a) | `Deals/Acme.md was already trashed at 20260825T091500Z; this copy is at 20260826T120000Z` |

Every response MUST state the cascade in counts before the next-actions block:

```
CASCADE: 7 notes rewritten (inbound wikilinks), 1 note moved
```

- **AC-X1** — a `trash` response names the count of now-unrepairable inbound links and lists
  the linking notes up to the page limit (FR-048).
- **AC-X2** — an operator policy of `vault_edit: allow` + `vault_restructure: deny` permits
  `set_property` and refuses `rename` in the same session (FR-083).
- **AC-X3** — `vault_restructure` declares **no** `expect_version` on any cascading op that cannot
  honour it, and its description says so (FR-018a's sibling rule, ADR-068 AC-15.5d). *Today
  `knowledge_rename` and `knowledge_move` take no `expect_version` and return none — verified:
  neither `RenameTool.Parameters` (`pkg/knowledge/authoring_tools.go:852-871`) nor
  `MoveTool.Parameters` (`:904-927`) declares the field — while the shipped parameter description
  at `authoring_tools.go:413-422` tells the model that "every knowledge tool that touches a note
  returns one as `version`". That is **false for the two highest-blast-radius tools in the set**.
  It is pre-existing and adjacent; it is named here so it is not lost.*

### 4.1.6 `vault_configure` — WRITE, cascades in meaning

**New in revision 4 (ADR-068 D15.6).** The control plane: record types and saved views. It is the
only tool that changes what already-existing notes **mean**, and it is a separate tool for exactly
one reason — policy resolves on the tool **name** (FR-070c), so a control plane folded into
`vault_edit` cannot be granted or withheld on its own.

**It writes one file** — a schema or a view — **and reinterprets many notes.** That is C-B, and it
is the opposite shape to `vault_restructure`, which writes many files and reinterprets none.

| `op` | Additional parameters | Cascade in meaning |
|---|---|---|
| `create_record_type` | `type`, `definition` | Every pre-existing note already declaring `type:` becomes a validated record (FR-016). |
| `edit_record_type` | `type`, `definition` | Every existing record of that type is revalidated (FR-015, FR-017). |
| `delete_record_type` | `type` | Every record of that type reverts to an ordinary note (FR-005, FR-017). |
| `write_view` | `view` (name), `definition` | Changes what a saved query returns; changes no note (FR-018). |
| `delete_view` | `view` | Same, in reverse. |

**No `expect_version` parameter exists on this tool, on any operation** (FR-018a). A single-file
content hash cannot honestly guard a change whose blast radius is every note declaring the type,
and a compare-and-swap that guards one of the things it affects is worse than none, because it
reads as a guarantee. Safety here is policy (FR-080a), plus the audit entry (FR-077), plus
`check_integrity` (FR-075).

**Every response MUST state the cascade in meaning, in counts, before the next-actions block.**
This is the requirement that makes C-B visible at all — the file diff shows one small YAML file
and nothing else:

```
CASCADE (meaning): 47 notes now match record type 'meeting'
  41 validate clean
   6 newly reported: required property 'date' is absent
  0 records lost validity
```

**Normative refusal and report wording. CORRECTED, revision 6 (review round 6, M-25) — revision 5
said *"These strings are contract, not illustration; a test asserts them"* over a table containing
`company`, `deal`, `meeting` and `person`, which is the exact opposite of what §4.1.2's own marker
says in the same words.** The **shape and the remedy clause** are contract; the **vocabulary is
illustration**.

*(**R-F marker.** Every record-type, property and value name below is an **illustration of what a
vault might define** — see R-F. The product ships none of them. **What a test asserts is the SHAPE
and the remedy clause**, against a fixture schema the test itself declares — not these particular
words. The **property TYPE** names — `text`, `enum`, `relation`, `date`, `integer`, `decimal`,
`person` — are the exception: those are ours and are shipped, FR-004.)*

**And this table now HAS an acceptance criterion, which it did not (M-41).** **AC-C9** — every
refusal and report string below names the remedy in the same string, asserted by §7 test 37 as
AC-F1 asserts §4.1.2's equivalent table. *(Revision 5 said "a test asserts them" and no test in §7
did: test 37 asserted tier placement and AC-C1's conversion count, and nothing asserted these ten
strings at all.)*

| Condition | Message |
|---|---|
| Type already exists (FR-016) | `record type 'company' is already declared in .omnipus-vault/records/company.yaml; use op=edit_record_type to change it` |
| Type does not exist (FR-017) | `no record type 'compnay' is declared; declared types: company, deal, meeting, person` |
| Schema missing `schema_version` (FR-002) | `schema for 'company' has no schema_version; add schema_version: 1` |
| Two files declare one type (FR-003) | `record type 'deal' is declared in .omnipus-vault/records/deal.yaml and .omnipus-vault/records/deals.yaml; delete one` |
| Unknown property type (FR-004) | `property 'closed' declares type 'boolean'; permitted: text, enum, relation, date, integer, decimal, person` *(revision 5: `number` and `money` are gone, `integer` and `decimal` are in — FR-004)* |
| ~~Enum with no declared order (FR-010)~~ | **DELETED, revision 5 (operator ruling R-E).** Declaring an enum in any order is now valid; ordering is lexical and a domain order is expressed by prefixing the values. |
| Integer property declaring a scale (FR-012) | `property 'headcount' declares type integer and scale 2; an integer property has no scale — declare it as decimal` |
| A cascading-in-bytes op sent here (C-A) | `rename writes notes you did not name; use vault_restructure` |
| A one-file note edit sent here | `set_property writes one note; use vault_edit` |
| Version token supplied (FR-018a) | `vault_configure takes no expect_version: a single-file token cannot guard a change to every note declaring this type. Re-read with vault_describe and re-send` |
| No properties index on this build (FR-020h) | `record types cannot be declared on linux/mipsle: this build has no properties index. The schema file would be written and never enforced` |

- **AC-C1** — **creating a new record type on a vault that already contains notes declaring that
  type reports the count of notes converted, and names the ones that newly fail validation.** A
  response that reports only "type created" fails this criterion. *This is the acceptance test for
  the defect ADR-068 D15.6 corrects: an operator whose vault already uses `type: meeting` as a
  personal convention, whose agent then authors a `meeting` schema, must not discover the
  conversion from a validation report over hundreds of notes they never asked to be validated.*
- **AC-C2** — an agent holding `vault_edit: allow` and `vault_configure: deny` **cannot** create,
  change or delete a record type or a saved view, by any route. This is the posture revision 3
  described and did not deliver (FR-083).
- **AC-C3** — `vault_configure` declares no `expect_version` parameter; a test asserts its absence
  in the tool schema, not merely its absence from the documentation (FR-018a, ADR-068 AC-15.5d).
- **AC-C4** — `delete_record_type` reports how many records revert to ordinary notes, and a
  subsequent `vault_find type=<deleted>` is **refused** naming the declared types, never returning
  an empty result (FR-024).
- **AC-C5** — every `vault_configure` call emits a `vault.configure` audit entry carrying the
  operation, agent, workspace, target and outcome, including on refusal (FR-077).
- **AC-C6** — the tool description names its **widest** operation, `delete_record_type`, not its
  most common one (FR-079, FR-070c).

---

## 4.2 The `vault_find` response — a literal worked example, normative

The ADR states the rules; this is the artifact. A test diffs against this shape, not against a
description of it.

> **Everything named in this example — `deal`, `company`, `status`, `arr`, `open`, `Acme`,
> `DEAL-0117` — is an ILLUSTRATION of what a vault might define (R-F). The product ships none of
> it.** What the test diffs against is the **rendered shape** over a fixture the test declares, not
> these names.

**The call.** *"Open deals of this type above 50,000, and anything within two link-hops of one
record that mentions a given word."*

```json
{
  "words": "pricing",
  "type": "deal",
  "filter": { "all": [ { "property": "status", "op": "=",  "value": "open" },
                       { "property": "arr",    "op": ">=", "value": "50000" } ] },
  "near": "Companies/Acme Ltd.md",
  "hops": 2,
  "join": ["company"],
  "sort": [ { "property": "arr", "direction": "desc" } ],
  "aggregate": [ { "op": "sum", "property": "arr" } ],
  "limit": 50
}
```

**The response the model reads.** Every line below is required by a numbered FR, named in the
annotation table that follows.

```
COMPLETE: no — 3 of 17 selected records could not be evaluated; 12 of 14 shown (more: cursor c2FnZTI)
QUERY: type=deal  words="pricing"  filter=(status = 'open' AND arr >= 50000)  near=[[Acme Ltd]] hops=2  join=company  sort=arr desc  limit=50
INDEX: 12 of 12 returned records agree across both indexes (source_hash matched); index_epoch 8814

DEAL-0117  Acme renewal FY27       status open   arr 180,000.00   company [[Acme Ltd]]: status active
DEAL-0155  Acme data migration     status open   arr 120,000.00   company [[Acme Ltd]]: status active
DEAL-0121  Acme expansion EU       status open   arr  95,000.00   company [[Acme Ltd]]: status active
DEAL-0161  Acme training package   status open   arr  88,000.00   company [[Acme Ltd]]: status active
DEAL-0134  Acme platform add-on    status open   arr  70,000.00   company [[Acme Ltd]]: status active
DEAL-0140  Acme support uplift     status open   arr  62,000.00   company [[Acme Ltd]]: status active
DEAL-0102  Northwind pricing pilot status open   arr  58,000.00   company [[Northwind]]: status prospect
… 5 more rows (57,000.00 · 56,000.00 · 55,000.00 · 54,000.00 · 53,000.00)

TOTALS: sum(arr) = 1,051,000.00 over 14 of 14 evaluated rows (12 shown)

PROBLEMS (3)
  DEAL-0052  arr is '50k' where a decimal is required — write 50000
  DEAL-0088  company is text "Acme Ltd", not a relation — write company: "[[Acme Ltd]]"
  DEAL-0093  status is 'Wonn'; deal.status permits: lost, open, won — write 'won'

NEXT
  page       vault_find cursor="c2FnZTI"
  narrow     vault_find type=deal filter=(status = 'open' AND arr >= 100000)
  widen      vault_find near="Companies/Acme Ltd.md" hops=2 words="pricing" type=<any>
  fix        vault_read path="Deals/DEAL-0052.md"   then vault_edit set_property arr=50000
```

**THE FIXTURE IS PUBLISHED, revision 6 (review round 6, M-40), because a normative worked example
whose arithmetic cannot be checked is not normative.** §7 test 25 *"diffs against §4.2's literal
example"*, and §4.2's annotation table certifies it line by line — so the numbers have to be
derivable, not asserted. The fourteen evaluated `arr` values, in the `desc` order the query asks
for:

| # | Record | `arr` | State |
|---|---|---|---|
| 1–7 | `DEAL-0117`, `-0155`, `-0121`, `-0161`, `-0134`, `-0140`, `-0102` | 180,000 · 120,000 · 95,000 · 88,000 · 70,000 · 62,000 · 58,000 | **rendered in full above** — subtotal **673,000** |
| 8–12 | five more | 57,000 · 56,000 · 55,000 · 54,000 · 53,000 | **shown but elided** by the byte budget's row cap — subtotal **275,000** |
| 13–14 | two more | 52,000 · 51,000 | **evaluated, NOT shown** — beyond the response byte budget (FR-127) — subtotal **103,000** |
| 15–17 | `DEAL-0052`, `-0088`, `-0093` | — | **selected but NOT evaluable** — the three in `PROBLEMS` |

**17 selected · 3 unevaluable · 14 evaluated · 12 shown.** `sum` over the **evaluated** set is
`673,000 + 275,000 + 103,000` = **1,051,000.00**; over the **shown** set it would be **948,000.00**.

**Two defects in revision 5's example are fixed by that table, and both were real.**
1. **M-40 — the total was arithmetically impossible.** The seven rendered rows sum to exactly
   673,000, revision 5 then said *"… 5 more rows"* under a filter of `arr >= 50000`, and gave the
   12-row total as **673,000**. The five hidden rows must contribute at least 250,000, so the stated
   total was **less than the rows it claimed to cover**. Nothing in the document could catch it,
   because the fixture was never written down.
2. **M-39 — `TOTALS` was page-scoped, which is the exact defect FR-125a exists to remove.** FR-125a:
   *"An aggregate MUST be computed over the **full evaluated set**, never over the rendered page …
   and a test asserts [the two counts] are computed from different numbers."* Revision 5's header
   said 14 evaluated and its total said `over 12 of 12 rows` — **the same number twice**, so the
   test FR-125a mandates could not have failed. **They now differ (14 vs 12, 1,051,000 vs 948,000),
   which is what makes the assertion capable of failing.**

**m-10 — the rendering rule for a `decimal`, stated rather than inherited from money.** Revision 5
rendered `arr` as `180,000.00` — two fixed decimal places on a `decimal` property with **no declared
scale**, which is currency-shaped formatting that outlived the currency. **The rule:** a `decimal`
renders at its property's **declared `scale`** where the schema declares one, and otherwise **at the
value's own scale as written in the note** (FR-046's render-what-the-file-says principle). This
example's fixture schema declares `arr: { type: decimal, scale: 2 }`, which is what makes
`180,000.00` correct here — **and it is a property of this fixture, not of `decimal`.** Thousands
separators are a rendering choice of the compact-text projection (FR-072) and never part of a
stored or compared value.

| Line | Required by | Why it is there and not elsewhere |
|---|---|---|
| `COMPLETE:` first | FR-121 | The verdict precedes the evidence, so no conclusion forms before the caveat arrives. |
| `QUERY:` echo | FR-122 | Shows the query **as executed** — a clamp or default is visible without a second call. |
| `INDEX:` freshness | FR-020c | Freshness is **per returned record**, not per index (ADR-068 D16.5): each row's `source_hash` is compared against **the bleve document's stored `source_hash`** (corrected, revision 6, C-2 — revision 5's annotation named `ManifestEntry.Hash`, which FR-020c removes from the query path). A record that fails moves to `PROBLEMS` and the verdict becomes `no`. The count is an assertion, not decoration — and per FR-020c1 it covers **what the query returned, not what it did not**. |
| Rows | FR-127 | ~200–320 **bytes** each; the row count shown is what the budget allowed, and the shortfall is in the header. |
| `company [[Acme Ltd]]: status active` | FR-124 | Borrowed, visibly. It is not a `deal` property and must never render as one. |
| `TOTALS:` | FR-125, **FR-125a** | **CORRECTED TWICE, revision 6 (review round 6, M-39 and M-40).** Scoped in the same sentence as the number — `over 14 of 14 evaluated rows (12 shown)` — so a reader can see the total covers every EVALUATED row. *Revision 4's line read `sum(arr) = GBP 465,000.00 over 5 of 12 rows — GBP only; 7 rows are USD and are not included`, which was the grill's C-9: §4.1.2's refusal table said a cross-currency total returns **nothing** while this artifact returned **one**, and both were labelled contract with a test behind them. Under operator ruling 1 the contradiction is **dissolved rather than adjudicated** — there is no currency, so neither artifact has a subject.* **The scope clause survives and is the load-bearing half:** a total that does not say what it covers is a bare number, which FR-125 forbids. **And it MUST be computed over the full evaluated set, never the rendered page** (FR-125a) — revision 4's example scoped its total to 12 shown rows while the header said 14 were evaluated, which is a page-scoped number labelled a total. |
| `PROBLEMS` | FR-025, FR-026, FR-123 | Each line is one record, one reason, one fix. |
| `NEXT` | FR-126 | Four addressable calls; the loop continues without the model inventing arguments. |

**`detail: minimal` renders the same query at ~80 bytes per hit** (FR-127) — header and problem
count survive the trim; columns and joins do not:

```
COMPLETE: no — 3 excluded (re-run with detail=standard to see them); 12 of 14 shown
QUERY: type=deal words="pricing" near=[[Acme Ltd]] hops=2 limit=50
DEAL-0117  Acme renewal FY27
DEAL-0121  Acme expansion EU
DEAL-0134  Acme platform add-on
… 9 more
NEXT  vault_find cursor="c2FnZTI"  |  vault_find detail=standard
```

**A zero-hit response never renders as an empty success** (FR-115):

```
COMPLETE: yes — 0 records matched
QUERY: type=company words="prospekt"
NEAREST INDEXED TERMS: prospect (412), prospects (37), prospecting (9)
NEXT  vault_find type=company words="prospect"  |  vault_describe record_type=company
```

The system reports the vocabulary it holds and **stops there** — it does not expand the query on
the caller's behalf (FR-114). A user who searched for one thing and received results for a
broader thing has been given a wrong answer with no error channel.

- **AC-P1** — a response whose `COMPLETE` line reads `no` and whose `PROBLEMS` block is empty is a defect: the reason is either named or the verdict is wrong. **PROMOTED TO A TEST, revision 5:** this was an invariant with no test in §7 asserting it (the grill's m-10). It is now asserted **over every response every test in §7 produces**, by a shared helper every `vault_find` test calls — the one place it is cheap to enforce and the one place it will actually catch a regression.
- **AC-P2** — the four blocks appear in the order `header → rows → totals → problems → next`, and
  a test asserts the order rather than the presence.
- **AC-P3** — **REWRITTEN, revision 5 — both halves were unusable.** *"The same wire object rendered twice is byte-identical"* asserts that a pure function is deterministic, which cannot fail; *"every fact in the text is present in the wire object"* is untestable as worded, there being no enumeration of "facts". The criterion is now **mechanical and falsifiable**: **every field the renderer reads MUST be reachable from the generated wire type** (`pkg/api/generated/`), asserted by a test that renders from a **zero-valued** wire object and asserts the output contains no literal that is not either a constant of the renderer or a field of that type. A renderer that reaches around the contract — a second lookup, a store call, a computed value not on the wire — produces a literal with no source and fails. *(This is FR-092 as a test, and it is the assertion Hard Constraint #8 actually needs.)*
- **AC-P4** — the response is measured in **bytes of rendered UTF-8** and the measurement in the
  test is the same unit the implementation enforces (FR-127b). A test that counts tokens fails
  this criterion even if it passes.
- **AC-P5** — a `vault_configure` response renders its cascade-in-meaning counts in the same
  block position `vault_restructure` renders its cascade-in-bytes counts: after the rows, before
  `NEXT` (§4.1.6, AC-P2's order).

---

## 5. Success criteria

- **SC-001** The two-hop question from ADR-068 §1.2 is answered by one `vault_find` call with no hand-maintained state and no regular expression.
- **SC-002** For a corpus of 63 records where 22 hold non-numeric values in a `decimal` property, an aggregate names all 22 and returns no combined figure. **And revision 5 adds the second half (ruling R-G):** the same 22 appear in the vault health view, and a test asserts the two surfaces agree (FR-025a).
- **SC-002a** **NEW, revision 5.** A record carrying **two** matching values of a `many` property contributes **once** to `count` and once to `sum` (FR-028a). *This is grill pass 1's worst finding as a success criterion: the SQL form returned 2 and 200 where truth was 1 and 100.*
- **SC-002b** **NEW, revision 5.** `{not: {any: [...]}}` over a corpus with absent values returns the absent records (FR-008, FR-023b) — asserted over a **compound** negation, not only a negated leaf.
- **SC-002c** **NEW, revision 5 (ruling R-B).** A filter using `=`, `<>`, `LIKE`, `IN`, `IS NULL` and `IS NOT NULL` evaluates correctly; a filter naming `BETWEEN`, `JOIN`, `COALESCE` or `CASE` is **refused naming the supported set and the parameter that does the job**, and never returns an empty result (FR-022b, FR-022c).
- **SC-002d** **NEW, revision 5; STRENGTHENED, revision 6.** All four of the following hold, and the fourth is a **negative**: `LIKE 'äcm%'` matches `ÄCME`; `= 'straße'` matches `STRASSE`; `= 'σίσυφος'` matches `ΣΊΣΥΦΟΣ`; and `= 'istanbul'` does **NOT** match `İSTANBUL`. Full Unicode case folding via `cases.Fold()`, in the comparator (FR-011a). *Revision 5's single `äcm%`/`ÄCME` cell passed under `strings.ToLower` and under a hypothetical SQLite implementation alike, so it discriminated nothing beyond ASCII; the four cells above are the smallest set that separates full folding from simple folding, from ASCII folding, and from an over-eager fold.*
- **SC-002e** **NEW, revision 5 (ruling R-F).** A fresh install has **zero** record types, zero seeded enum values and zero seeded property names, and a denylist test finds no domain vocabulary in any non-test file of the record packages (FR-004a).
- **SC-003** A query filtering on a mistyped property name is rejected with valid names listed; zero such queries return an empty result set.
- **SC-004** Writing one property into a 200-line note leaves the file byte-identical outside the patched span, across a 50-file fixture corpus.
- **SC-005** **CORRECTED, revision 3.** 1,000 records created concurrently across two POSIX processes yield 1,000 **distinct** identifiers. **Gaps are permitted; a repeat is a failure.** *Revision 2 demanded "zero sequence gaps", which directly contradicts FR-038 and ADR-068 D7.1: deleting a record burns its identifier permanently, so a gap is the correct outcome and reconciling to max to close it would make an existing relation resolve to a different record. This was a specification defect, not a wording preference.*
- **SC-005a** Deleting the highest-numbered record and creating a new one yields an identifier **above** the deleted one, never equal to it.
- **SC-006** An agent in workspace A retrieves zero records from a vault mounted only into workspace B, and cannot distinguish it from an empty vault.
- **SC-007** **CORRECTED, revision 4.** *(No latency target is stated. The spike measured the **bleve-plus-Go design this specification did not take**; quoting its numbers as targets for the two-index design would repeat revision 1's error in a new costume. Targets arrive when W1 has the SQLite path measured.)* **The 64 MB budget is a TARGET, not an inherited property.** Revision 3 wrote that ADR-067's < 64 MB steady-state RSS holds and "the properties index lives inside it". That budget was measured for **bleve alone** — idle 12.9–15.1 MB, 23.6–24.0 MB streamed at the cap (spike §5.1, §5.3) — and the two-index design keeps all of it and adds costs on **both** sides. **CORRECTED, revision 6 (review round 6, survivor 5): revision 5 charged the budget for "SQLite's temp b-trees for `GROUP BY`/`ORDER BY`", and under ruling R-A neither clause is emitted (AC-8.10), so no such b-tree exists.** What the budget actually carries, none of it measured: **on the SQLite side**, the page cache, the connection state, and whatever the narrowing select's `LIMIT`/`OFFSET` costs; **on the Go side — and this is the part revision 5 lost when it deleted the SQL clauses without re-charging their replacements** — the candidate stream's working set, the **grouping map** (FR-066b: up to 50,000 entries for a free-text group key), the **sort buffer** for a full ordering pass, and the per-candidate folded forms FR-011c permits. **The cost did not vanish with the SQL clauses; it moved across the boundary, and it moved to the side nobody has measured.** **W1 exit criterion:** both indexes, idle and at the 10,000-record cap, inside 64 MB, measured on **Linux as well as macOS** (ADR-068 D16.4 item 4).
- **SC-009** Zero tool-policy pairs are repaired on a fresh install, across all ten seeded agents.
- **SC-010** A `.base` file containing one unsupported expression imports — **via the operator/CLI one-shot** — with that expression reported verbatim and the rest translated.
- **SC-011** **CORRECTED, revision 4.** The static builtin catalog contains exactly **six** `vault_*` names and zero `knowledge_*` names, and `allStaticToolNames` has **95** entries. *(Revision 3 said five and 98. 98 is the count **today**, before any change — re-counted at `pkg/coreagent/core.go:358-482`: 98 quoted identifiers, 98 unique, 9 `knowledge_*`. After the swap: 98 − 9 + 6 = 95.)*
- **SC-012** **WIDENED, revision 4.** All three write policies are independently settable, proven by test in one session: `vault_edit: allow` + `vault_restructure: deny` permits a property write and refuses a rename; `vault_edit: allow` + `vault_configure: deny` permits a property write and refuses `create_record_type`.
- **SC-008** **RESTORED, revision 5 — it was missing with no annotation, in a document whose §0 states "Nothing below was renumbered silently".** *(The grill's M-41. It had simply fallen out between revisions and nothing recorded it, which is exactly the silent-renumbering this document forbids itself.)* **A write that violates the schema leaves the file byte-identical and the response names the expected shape** — FR-042, US-4 scenario 2, `TestWrite_SchemaViolationLeavesFileUnmodified`. It is a distinct criterion from SC-004 (which tests a *successful* write's byte-preservation) and from SC-003 (which tests a *query* rejection); it is the write-side rejection path and nothing else covered it.
- **SC-013** **CORRECTED, revision 5 — it is SC-005's sibling and it was false as written.** Every successful `vault_edit` changes **exactly one file on disk, or exactly two on `create`** — the note and `.omnipus-vault/records/.seq` (FR-036a). *Revision 4 said "exactly one file", which is false for `create` and is contradicted by AC-E2a in the same document, which asserts **two**. **AC-E1 was amended for this exception and SC-013 was not**, which is the same defect SC-005 carried: a criterion left behind when the requirement it summarised was corrected.* **And a second case revision 4 missed:** `create` of a note whose `type` is **not** a declared record type mints no identifier (§4.1.4) and therefore changes **one** path, so AC-E2a's "exactly two" is scoped to record creates and the one-file case is asserted alongside it.
- **SC-014** Deleting the properties index and reopening the vault reproduces byte-identical query results for a 30-query fixture suite.
- **SC-015** **CORRECTED, revision 4 — it tests DIVERGENCE, not rebuild, and in both directions.** A record row is written; the note is then modified and re-indexed into **bleve only** (the SQLite write suppressed); a `vault_find` returning that record reports `COMPLETE: no` with the record named and staleness given as the reason. The symmetric case — SQLite updated, bleve not — is asserted the same way. *Revision 3's criterion could be satisfied by a generation counter that does not exist; ADR-068 revision 5's W1 exit criterion was "delete the properties index and reopen rebuilds it with identical results", **which would have passed with the mitigation entirely absent**. SC-014 keeps the rebuild criterion because it tests a different property — FR-020a's disposability.*
- **SC-016** **REWRITTEN, revision 5 — asserting an assignment exists is not a behavioural test, and the count was stale.** *Revision 4 asserted that `ScoringModel` is set, over **seven** stale doc comments. A source-level assertion passes if the assignment is in dead code, applied to a different index than the one queried, or overwritten later — and seven is the wrong number.* The criterion is now: **BM25 ranking is asserted BEHAVIOURALLY**, over a fixture corpus in which BM25 and TF-IDF demonstrably differ (a term-saturation or length-normalisation case), by asserting the **BM25 ordering** rather than the presence of an assignment; **and all THIRTEEN stale claims are corrected in the same change** (FR-110, FR-110a), with `pkg/agent/memory.go:301`'s operator-facing WARN carrying its own changelog entry (FR-110b).
- **SC-017** A field query on a frontmatter property key returns the records holding it — a query that is not expressible at all today.
- **SC-018** **REWRITTEN, revision 5 — "the comparison is recorded" is not a criterion.** The FR-112 fusion is compared against plain BM25F on the committed fixture corpus and the committed 30-query set, measured as **nDCG@10**; **the fusion ships as the default if and only if mean nDCG@10 improves by ≥ 0.03 absolute AND the improvement holds at the 10th percentile of the per-query distribution** (FR-113). A result inside the threshold is a **no-ship for FR-112**, recorded with the per-query table. Separately, AC-8.6's set-invariance is asserted **over a fixture in which the two orders demonstrably differ** — a run where they do not differ fails, because it proves the fixture did not exercise the change.
- **SC-019** An agent obtains a version token via `vault_read` and completes a write with zero failed writes in between.
- **SC-020** A client mounting the collection panel after indexing completed renders the completed state, and it matches the freshness `vault_find` reports for the same collection.
- **SC-021** A `vault_find` response over a partial answer places its completeness verdict on line 1, and a test asserts block order `header → rows → totals → problems → next`.
- **SC-022** **NEW, revision 4.** Creating a record type on a vault holding 47 notes that already declare that type reports **47** converted and names the 6 that newly fail validation. A response reading only "type created" fails (AC-C1).
- **SC-023** **NEW, revision 4.** On a build without the properties index, every typed filter, join, grouping and aggregation call **refuses by name**, and zero of them return an empty success (FR-020h, AC-F6).
- **SC-024** **REWRITTEN, revision 5 (operator ruling R-A).** The truth table of §8 runs against **the comparator the product uses**, driven through the real path — schema → filter object → candidate set → comparator — and **each of AC-8.4b's TWELVE named rule mutations, plus its thirteenth aggregate mutation, makes it fail**, reported as a mutation table naming the mutation, the cell it killed and the run. *(Revision 6, review round 6, M-11: revision 5 stated **six** mutations as sufficient for **twelve** live rules. Six is not a threshold that can be argued down from — it left seven rules with no mutation at all.)* *Revision 4 required the compiled SQL path and named **two** mutations for **nine** defeats — and a compiler missing seven of the nine passed both of them. Under ruling R-A there is no compiler; the two mutations it named are gone with it (one was the `IS NULL` arm of a SQL negation, the other swapped `instr()` for `LIKE`, which ruling R-D now permits anyway).* **The no-post-filter assertion of AC-8.4 is part of this criterion**: a row count at the candidate boundary equals rendered rows plus problem rows plus comparator rejections, so a second filtering pass silently correcting the first is observable.
- **SC-025** **NEW, revision 4.** Response budgets are asserted in **bytes of rendered UTF-8**, by the same measurement the implementation enforces (FR-127b, AC-P4).
- **SC-026** **NEW, revision 4.** An unscoped `check_integrity` over a collection above 100,000 notes is refused naming the collection and the scoped remedy; a category above 500 findings reports the clamp with the count that would have been returned (FR-075a).

---

## 6. Traceability

| FR | Story | Scenario | Test |
|---|---|---|---|
| FR-001..003 | US-1 | 1.5 | `TestSchema_LoadAndReject` |
| FR-004, 004a, 009 | US-1 | 1.2 | `TestSchema_TypesAreScopedToRecordType`; `TestSchema_NoDomainVocabularyInBinary` |
| FR-006 | US-1 | 1.4 | `TestValidate_ArityViolationIsReported` |
| FR-007, 008 | US-2 | edge | `TestFilter_AbsentIsDistinctAndIncludedByNegation` |
| FR-010, 011, 011a, 011b | US-1 | 1.3 | `TestEnum_OrdersLexicallyAndResolvesCaseInsensitively`; `TestFilter_CaseFoldIsFullUnicode`; `TestFilter_ComparatorDoesNotUseStdlibFolding`; **FR-011c** joins this row (revision 6) |
| FR-012, FR-013 | US-2 | 2.3 | `TestNumeric_IntegerBoundAndDecimalScaleRefused` *(FR-014 is **retired** — `money` is deleted)* |
| FR-015, 015a, 016..019a | US-12 | 12.1–12.5 | `TestSchemaAuthoring_LivesInVaultConfigure` — *and revision 5 closes M-23's gap here: FR-015 had no row and no test, while being new mechanism (schemas live outside the scanner's walk, so change-tracking must be built)* |
| FR-018a | US-12 | 12.4 | `TestConfigure_DeclaresNoVersionToken` |
| FR-020, 020a, 020b, 021 | US-2 | — | `TestPropsIndex_RebuildIsResultIdentical`; `TestIndex_PropsRoundTripsExactDecimal` — **a `decimal` value survives the index unchanged; a `float64` path fails it** *(revision 5: the test's subject was a money value)* |
| FR-020c, 020c1, 020g, 020i, 020j | US-13 | 13.2 | `TestIndexes_SourceHashDivergenceIsIncomplete`; `TestFreshness_ConcurrentQueryDuringSync`; `TestFreshness_EveryPartialWriteFailurePointIsDetectable`; `TestStorage_LinkedSQLiteVersionIsAsserted` |
| FR-020h | — | — | `TestRecords_RefuseByNameWithoutSQLite` |
| FR-020d, 020e | — | — | `TestIndex_StaleFormatIsRebuiltNotOpened`; `TestIndex_PersistedMappingAsserted` |
| FR-020f | US-13 | 13.1 | `TestIndexState_SnapshotMatchesLiveFrame` |
| FR-022, 022a, 022b, 022c, 023, 023b, 024 | US-2 | 2.4 | `TestQuery_UnknownPropertyIsRejectedNotEmpty`; `TestFilter_SQLOperatorVocabularyAndRefusals`; `TestFilter_CompoundNegationIncludesAbsent` |
| FR-025, 025a, 026 | US-2 | 2.2 | `TestQuery_ProblemsAreNamedNotDropped`; `TestProblems_QueryAndHealthViewAgree`; `TestQuery_ProblemListClampsWithCount` |
| FR-027..029, 028a | US-2 | 2.6 | `TestGroup_MultiValueAppearsInEveryGroup`; `TestAggregate_ManyValuePropertyCountsRecordOnce` |
| FR-030..035 | US-3 | 3.1–3.4 | `TestRelation_InverseIsDerivedAndSurvivesRename` |
| FR-036..039 | US-5 | 5.1–5.4 | `TestID_ConcurrentAllocationIsCollisionFree` |
| FR-040..042 | US-4 | 4.1, 4.2 | `TestWrite_ByteIdenticalOutsidePatchedSpan` |
| FR-043, 043a, 044 | US-4 | 4.3, 4.4 | `TestWrite_StaleTokenRefusedAndAudited`; `TestConfigure_ConcurrentCreateRecordTypeIsExclusive` |
| FR-045 | US-4 | — | `TestRelate_ReplaceMustBeNamed` |
| FR-046 | — | — | `TestDerived_NeverWrittenToFrontmatter` |
| FR-050..053 | US-6 | 6.1–6.4 | `TestInteraction_ExclusionRules` |
| FR-060..062 | US-8 | 8.1, 8.2 | `TestScope_CrossWorkspaceReturnsEmpty` |
| FR-063..066b, 064a | US-2 | 2.5 | `TestBounds_RefusalNotTruncation`; `TestBounds_CandidateCountedBeforeRetrieval`; `TestBounds_PeakRSSAtCap`; `TestBounds_AggregateOnlyIsExemptFromCandidateCap` |
| FR-067, 067a | — | — | `TestWritePath_RateLimited`; `TestReadPath_RateLimited` |
| FR-036a | US-1 | — | `TestCreate_WritesNoteAndSeqAndNothingElse` |
| FR-047 | US-4 | — | `TestReplaceBody_AmbiguousAnchorIsRefused` |
| FR-048, 048a, 049, 049a, 049b, 049c | US-4 | — | `TestTrash_ConventionAndUnrepairableLinksReported`; `TestTrash_RestoreRetentionAndWindowsSafePath`; `TestMigration_NoRetiredKnowledgeToolNamesRemain`; `TestObservability_CountersAndDegradedRebuildHeader` |
| FR-070, 070a, 078 | — | — | `TestTools_ExactlySixVaultToolsAndNoKnowledgeNames` |
| FR-070b..070e, 071 | — | — | `TestTools_EditWritesOnlyNamedFile`; `TestTools_NamesHaveNoDots`; `TestTools_ConfigureWritesOnlyVaultControlPlane` — *FR-070e's mechanical criterion is CI-testable over the emitted write path, which is why it was adopted* |
| FR-072, 072a, 120..128, 125a, 127c | US-11 | 11.1–11.4 | `TestRender_CompactTextContract`; `TestRender_BudgetIsMeasuredInBytes`; `TestRender_BudgetAllocationOrder` |
| FR-073 | US-2 | — | `TestFind_ExplainEvaluatesNothing` |
| FR-074 | US-9 | 9.1–9.3 | `TestRead_ReturnsUsableVersionToken` |
| FR-075 | US-5 | 5.3 | `TestDescribe_CheckIntegrityNamesBothPaths`; `TestDescribe_ReportsOrdinaryBrokenWikilinks` |
| FR-075a | — | — | `TestDescribe_CheckIntegrityIsBounded` |
| FR-076 | US-3 | — | `TestFind_NearComposesWithFilters` |
| FR-076a | US-10 | — | `TestFind_TasksAreIndexedRowsNotAWalk` |
| FR-077 | US-4 | 4.4 | `TestAudit_VaultEditAndRestructureCarryOperation` |
| FR-064, FR-064a, FR-066b, SC-007 | US-2 | 2.5 | `BenchmarkRecords_AtFiftyThousand` (§7 test 21); `BenchmarkBounds_PeakRSSAtCap` (§7 test 88) — **NEW ROW, revision 6 (review round 6, m-7).** Both are **benchmarks with a W1 write-back obligation**, and neither had a §6 row, so the obligation was traced only from §7 and would have been discharged by whoever read §7 rather than by the requirement's owner |
| FR-079, 128 | — | — | `TestTools_DescriptionBudgetIsReviewedNotEnforced` *(revision 6, M-35 — §6 named `TestTools_DescriptionTokenBudget`, whose name mandates runtime enforcement of a budget FR-127b says is NEVER enforced at runtime; §7 test 38 wins)* |
| FR-080, 080a, 081, 082 | — | — | `TestToolPolicy_ZeroRepairedPairsOnFreshInstall` |
| FR-083, 084 | US-12 | 12.2, 12.4 | `TestToolPolicy_ThreeWriteTiersAreIndependent` |
| FR-110, 110a, 110b | US-10 | 10.1 | `TestSearch_RankingIsBM25Behaviourally` |
| FR-111 | US-10 | 10.3 | `TestIndex_FieldedAndFrontmatterStripped` |
| FR-112, 113 | US-10 | 10.4 | `TestRank_FusionMeetsNDCGThreshold` |
| FR-114, 115 | US-10 | 10.2 | `TestSearch_ZeroHitsReportsVocabularyNotExpansion` |
| FR-116 | — | — | `TestTokenizer_OneNotionOfATerm` |
| FR-117 | — | — | `TestRetrieval_NoEmbeddingDependency` |
| FR-090, 091 | — | — | `TestContract_CompletenessFieldsAreRequired` |
| FR-100..103 | US-7 | 7.1–7.3 | `TestImport_UntranslatedExpressionIsReported`; `TestImport_NotRegisteredAsAgentTool` |

**Rows added in revision 5 to close M-23 — nine FRs had no row and no test, and two of them mattered a great deal.**

| FR | Story | Scenario | Test |
|---|---|---|---|
| FR-005 | US-1 | 1.1 | `TestSchema_UndeclaredTypeIsAnOrdinaryNote` — *US-1.1 was also an orphaned scenario (M-25); this closes both* |
| FR-019 | — | 12.5 | `TestTools_NoAgentCallableMountOperation` |
| FR-021a, 021b, 021c, 021d | US-2 | 2.2 | `TestValue_NonConformingIsFlaggedNotAbsent`; `TestDate_StrictISOAndAmbiguousRefused` — **FR-021a is the whole of the R-4 defeat and had no test at all** |
| FR-040a, 040b | US-4 | 4.1 | `TestWrite_ListSpliceAndMultiLineClobberRefused` |
| FR-062a | US-8 | 8.1 | `TestScope_TruncatedResolutionReportsIncomplete` — **a P0-scope guarantee that had no test, no AC and no SC**: a whole mounted folder can go missing while the answer claims to be complete |
| FR-092 | — | — | `TestRender_TextIsAProjectionOfTheWireObject` |
| FR-004a | US-1 | — | `TestSchema_NoDomainVocabularyInBinary` |
| FR-025a | US-2 | 2.2 | `TestProblems_QueryAndHealthViewAgree` |
| **FR-200..FR-212** (= R-1..R-13) | US-2 | — | `TestComparisonTruthTable` — **the rules are PROMOTED to numbered requirements as FR-200..FR-212** (M-24), one per rule, so that the highest-risk item in the document appears in this matrix rather than being traced to "see §8". AC-8.1..AC-8.6 and AC-8.8 trace here too |

**Orphaned acceptance scenarios closed (M-25):** US-1.1 above; **US-2.1** (all-valid corpus ⇒ complete true) → `TestQuery_AllValidCorpusReportsComplete`; **US-3.5** (third hop refused) → `TestFind_ThirdHopRefused`, which revision 4 mapped under US-2.5 and which is FR-065's refusal, not FR-064's.

---

## 7. Test plan

Order is unit → integration → e2e; within a level, dependencies first.

| # | Test | Level | Traces |
|---|---|---|---|
| 1 | `TestSchema_LoadAndReject` | unit | FR-001..003 |
| ~~2~~ | ~~`TestEnum_OrderedAndClosed`~~ | ~~unit~~ | **DELETED, revision 6 (review round 6, M-18)** — a duplicate of test 56 whose **name asserts the withdrawn requirement**, and §6's traceability row already names only test 56. Deleted rather than renamed, on the same ground as tests 36 and 39: a test named for a deleted semantics is a test that gets written to its name |
| 3 | `TestValidate_ArityViolationIsReported` | unit | FR-006 |
| 4 | `TestNumeric_IntegerBoundAndDecimalScaleRefused` | unit | FR-012, FR-013 — **REPLACES `TestMoney_RefusesCrossCurrencySum`, deleted with `money` (operator ruling 1).** int64+1 is refused naming the bound and is never `CAST`; a 101-place decimal is refused naming the bound and its own scale, and is never rounded |
| 5 | `TestFilter_AbsentIsDistinctAndIncludedByNegation` | unit | FR-007, 008 |
| 6 | `TestComparisonTruthTable` | unit | **see §8** |
| 7 | `TestQuery_UnknownPropertyIsRejectedNotEmpty` | unit | FR-024 |
| 8 | `TestQuery_ProblemsAreNamedNotDropped` | integration | FR-025, 026 |
| 9 | `TestGroup_MultiValueAppearsInEveryGroup` | integration | FR-028 |
| 10 | `TestRelation_InverseIsDerivedAndSurvivesRename` | integration | FR-030..034 |
| 11 | `TestID_ConcurrentAllocationIsCollisionFree` | integration | FR-036..039 |
| 12 | `TestWrite_ByteIdenticalOutsidePatchedSpan` | integration | FR-040, 041 |
| 13 | `TestWrite_StaleTokenRefusedAndAudited` | integration | FR-043, 044 |
| 14 | `TestInteraction_ExclusionRules` | integration | FR-050..053 |
| 15 | `TestScope_CrossWorkspaceReturnsEmpty` | integration | FR-060..062 |
| 16 | `TestBounds_RefusalNotTruncation` | integration | FR-063..066 |
| 17 | `TestToolPolicy_ZeroRepairedPairsOnFreshInstall` | integration | FR-081 |
| 18 | `TestTools_ExactlySixVaultToolsAndNoKnowledgeNames` | unit | FR-070, 070a, 071, 078 — **REPLACES revision 3's five-tool test, which replaced revision 2's nine-tool test.** Asserts the **six** names are registered, that none contains a dot, that **no `knowledge_*` name survives** in the catalog, the global ceiling or any seed, and that `allStaticToolNames` has **95** entries. A name-only check would duplicate an existing green assertion over 35 tools and pass today with zero vault tools, so registration is asserted first |
| 19 | `TestContract_CompletenessFieldsAreRequired` | unit | FR-091 |
| 20 | `TestImport_UntranslatedExpressionIsReported` | integration | FR-100..103 |
| 21 | `BenchmarkRecords_AtFiftyThousand` | **benchmark, not a test** | **RECLASSIFIED, revision 5.** A test with no assertion cannot fail; revision 4 carried a benchmark labelled a test with *"no threshold until W1 measures the SQLite path"*. It moves to a benchmark, and **W1's exit criterion is that it produces a number which is then written back into SC-007 as a threshold** — the follow-up is the requirement, not the run |
| 22 | `TestTools_EditWritesOnlyNamedFile` | integration | FR-070b — snapshots every file's hash, runs each `vault_edit` op, asserts exactly one changed |
| 23 | `TestRead_ReturnsUsableVersionToken` | integration | FR-074 — read → edit with zero intervening failed writes |
| 24 | `TestFind_NearComposesWithFilters` | integration | FR-076 — both negative directions, per AC-F2 |
| 25 | `TestRender_CompactTextContract` | unit | FR-120..127 — diffs against §4.2's literal example, asserts block order, and asserts no JSON document is emitted |
| 26 | `TestSearch_RankingIsBM25Behaviourally` | integration | FR-110, SC-016 — **REWRITTEN, revision 5.** Asserting the assignment exists is not a behavioural test: it passes if the assignment sits in dead code, is applied to a different index than the one queried, or is overwritten. A fixture corpus in which BM25 and TF-IDF demonstrably differ (term saturation, length normalisation), asserting the **BM25 ordering** |
| 27 | `TestIndex_FieldedAndFrontmatterStripped` | integration | FR-111 — a field query on a property key returns records; the body field does **not** contain frontmatter tokens |
| 28 | `TestRank_FusionMeetsNDCGThreshold` | integration | FR-113, SC-018, AC-8.6 — **REWRITTEN, revision 5; revision 4's version could not fail.** AC-8.6's set-equality is guaranteed by the architecture (membership is the comparator's, ranking is a separate pass), and *"records the comparison"* asserts nothing. Now: measures **nDCG@10** over the committed corpus and 30-query set, **asserts the ≥ 0.03 threshold and the 10th-percentile condition**, and asserts set-equality **over a fixture whose two orders demonstrably differ** — a run in which they do not differ FAILS |
| 29 | `TestSearch_ZeroHitsReportsVocabularyNotExpansion` | integration | FR-114, FR-115 |
| 30 | `TestTokenizer_OneNotionOfATerm` | unit | FR-116 — asserts one shared function, or fails with the recorded decision as the only permitted alternative |
| 31 | `TestPropsIndex_RebuildIsResultIdentical` | integration | FR-020a, SC-014 |
| 31a | `TestQuery_FilteringHappensInGoNotSQLite` | integration | **FR-021 — NEW, revision 6 (review round 6, m-1).** §6's FR-020/021 row traced FR-021 to `TestPropsIndex_RebuildIsResultIdentical` and `TestIndex_PropsRoundTripsExactDecimal`, which assert rebuild-identity and decimal round-tripping and **neither touches FR-021's Go-evaluation requirement — the most-changed FR in revision 5**. This test asserts the requirement itself: with the comparator stubbed to reject everything, a filtered query returns **zero** records (the comparator decides), while the same query's narrowing SELECT still returns its candidates (SQLite still narrows). It is the behavioural pair to test 39a's structural check |
| 32 | `TestIndexes_SourceHashDivergenceIsIncomplete` | integration | FR-020c — **tests divergence, not rebuild, in BOTH directions.** Writes a row, re-indexes the modified note into bleve only (SQLite write suppressed), asserts the returning `vault_find` reports `complete: false` with the record named and staleness as the reason; then the symmetric case. A rebuild-only criterion would pass with the mitigation absent. Also asserts an orphaned row (no manifest entry) and an empty hash are both flagged, never assumed fresh |
| 33 | `TestIndexState_SnapshotMatchesLiveFrame` | integration | FR-020f, FR-020g — a client that never received a frame renders the same state as one that did |
| 34 | `TestReplaceBody_AmbiguousAnchorIsRefused` | unit | FR-047 — both line numbers named, file byte-identical |
| 35 | `TestTrash_ConventionAndUnrepairableLinksReported` | integration | FR-048 |
| ~~36~~ | ~~`TestToolPolicy_EditAndRestructureAreIndependent`~~ | — | **DELETED, revision 5.** The superseded two-tier version of test 41; both traced to FR-083 and SC-012 and §6 listed only 41 |
| 37 | `TestSchemaAuthoring_LivesInVaultConfigure` | integration | FR-016, FR-017, FR-018 — **REPLACES revision 3's `TestSchemaAuthoring_TierPlacement`, which asserted the wrong placement.** Every schema and view op is refused by `vault_edit` **and** `vault_restructure`, naming `vault_configure`, and accepted by `vault_configure`. Creating a new type over a vault already holding notes of that type asserts the conversion count and the newly-failing records (AC-C1) |
| 38 | `TestTools_DescriptionBudgetIsReviewedNotEnforced` | unit | FR-079, FR-127b, FR-128 — **REWRITTEN, revision 5.** FR-127b says the ~150-token description budget is *"never enforced at runtime"* and that *"a test MUST NOT conflate"* it with the byte budget; revision 4's test enforced it at runtime, with one of the three tokenizers FR-116 says disagree, none of which is the serving model's — the exact defect A-8 closed for the response budget. The test now asserts what **is** enforceable: that each of the six descriptions exists, is non-empty, and **names its widest operation** (FR-070c). The ~150-token budget becomes a review checklist item at W5. **Separately, the test MUST measure and RECORD the serialised six-tool definition set in bytes** — description plus full parameter schema — because FR-079's correction shows the ~900-token figure counts the wrong thing |
| ~~39~~ | ~~`TestFilter_NoLikeInCompiledPath`~~ | — | **DELETED, revision 5 (operator rulings R-D and R-A).** It asserted zero `LIKE` to protect a case-sensitivity that is no longer wanted, in a compiled filter path that no longer exists. AC-8.7 is deleted with it |
| 40 | `TestConfigure_DeclaresNoVersionToken` | unit | FR-018a, AC-C3 — asserts `expect_version` is absent from the **tool schema**, not merely from the prose |
| 41 | `TestToolPolicy_ThreeWriteTiersAreIndependent` | integration | FR-083, SC-012 — `vault_edit` / `vault_restructure` / `vault_configure` set independently; the `edit: allow, configure: deny` case asserts `create_record_type` is refused |
| 42 | `TestRecords_RefuseByNameWithoutSQLite` | unit | FR-020h, SC-023 — build-tagged; every typed call refuses naming the platform, and none returns an empty success. **CI GATE NAMED, revision 5:** a build-tagged test with no gate may never run — the documented "27% of the SPA suite never ran while CI was green" pattern. **The job is `go-test-nosqlite`, the tag combination is `goolm,stdjson,nosqlite`, the make target is `make test-nosqlite`, and it is added to the required-checks list.** `make build-all`'s `linux/mipsle` target is a **build**, not a test run, and does not discharge this |
| 43 | `TestDescribe_CheckIntegrityIsBounded` | integration | FR-075a, SC-026 — the 500-per-category clamp is reported with the would-be count; the 100,000-note sweep is refused naming the scoped remedy |
| 44 | `TestDescribe_ReportsOrdinaryBrokenWikilinks` | integration | FR-075 — a vault with **no records at all** still produces broken-link and orphan findings. This is the capability `knowledge_graph`'s retirement would otherwise lose |
| 45 | `TestFind_TasksAreIndexedRowsNotAWalk` | integration | FR-076a — task rows carry `path`/`line`/`status`/`text`, render with the line number, and **no collection walk occurs**; a fixture with 6,000 files (above `TasksMaxFiles = 5000`) returns tasks from all of them |
| 46 | `TestRender_BudgetIsMeasuredInBytes` | unit | FR-127, FR-127b, AC-P4 — the assertion counts bytes of rendered UTF-8, and the same function the renderer enforces with |
| 47 | `TestBounds_CandidateCountedBeforeRetrieval` | integration | FR-066a — the count happens before any document is retrieved; a 24,000-candidate query is refused without materialising one row |
| 48 | `TestWritePath_RateLimited` | integration | FR-067 — `vault_edit`, `vault_restructure` and `vault_configure` each 429 with `Retry-After`. **Asserts the limiter is reached at all**, because today no write `Execute` consults one |
| 49 | `TestCreate_WritesNoteAndSeqAndNothingElse` | integration | FR-036a, SC-013 — exactly **two** paths change on a **record** `create`; a third is a failure. **And exactly ONE on a non-record create**, which revision 4's AC-E2a asserted wrongly as two |
| 50 | `TestFilter_CompoundNegationIncludesAbsent` | unit | FR-008, FR-023b, SC-002b — **NEW, revision 5.** `{not: {all: […]}}` and `{not: {any: […]}}` over a corpus with absent values. Every negation cell in revision 4's table was leaf-shaped, so a tri-state bug in a tree walker would have passed all of them |
| 51 | `TestAggregate_ManyValuePropertyCountsRecordOnce` | integration | FR-028a, SC-002a — **NEW, revision 5.** A record gains a **second** matching value of a `many` property; `count` and `sum` are unchanged. *Grill pass 1's worst finding, as a test* |
| 52 | `TestFilter_SQLOperatorVocabularyAndRefusals` | unit | FR-022b, FR-022c, SC-002c — **NEW, revision 5 (ruling R-B).** All ten operators evaluate; `BETWEEN`, `JOIN`, `COALESCE`, `CASE`, a subquery and a function call are each refused **naming the supported set and the parameter that does the job**, never parsed, never silently dropped, never an empty result |
| 53 | `TestFilter_CaseFoldIsFullUnicode` | unit | FR-011a, FR-011b, SC-002d, AC-8.9 — **RENAMED AND CORRECTED, revision 6.** Asserts AC-8.9's six literal pairs, including the two **negatives**. `= 'straße'` matches `STRASSE` — which is **true under `cases.Fold()` and FALSE under both `strings.ToLower` and `strings.EqualFold`**, so this cell is what makes the test discriminating rather than decorative. **The fixture MUST include non-ASCII pairs**, and MUST include at least one full-folding pair and the Turkish negative — an ASCII-only fixture passes over any of the three wrong mechanisms, which is the whole point. *(Revision 5 named this `…IsUnicodeNotASCII` and cited "Go's folding" for a cell Go's stdlib folding gets wrong.)* |
| 53a | `TestFilter_ComparatorDoesNotUseStdlibFolding` | unit | FR-011a, FR-011b — **NEW, revision 6.** A source-level assertion over the comparator package: **no `strings.ToLower`, `strings.ToUpper`, `strings.EqualFold` or `unicode.ToLower` appears in any comparison path**, and the only folding call is `cases.Fold()`. This is a **guard against a plausible future simplification**, not against a present bug: every one of those calls looks like a harmless tidy-up, three of AC-8.9's six pairs change answer when one is substituted, and the change is invisible in any ASCII fixture. Compare §7 test 42's treatment: name the file set the check covers, so that adding a new comparison file cannot silently escape it |
| 54 | `TestSchema_NoDomainVocabularyInBinary` | unit | FR-004a, SC-002e — **NEW, revision 5 (ruling R-F).** A denylist over every non-test file of the record packages; fixtures excluded by path |
| 55 | `TestDate_StrictISOAndAmbiguousRefused` | unit | FR-021d, R-7 — **NEW, revision 5 (ruling R-H).** `2026-09-01` and `2026-09-01T14:30Z` parse; `2026-9-1` and `03/04/2026` are reported as bad values **with the fix named**; a day and an instant compare per R-7 |
| 56 | `TestEnum_OrdersLexicallyAndResolvesCaseInsensitively` | unit | FR-010, FR-011, R-5 — **NEW, revision 5 (rulings R-E and R-D).** Ordering is lexical and no ordinal column exists; `Won` resolves to `won`; a prefixed set (`1-lead`, `2-qualified`) orders in the author's intended order |
| 57 | `TestConfigure_ConcurrentCreateRecordTypeIsExclusive` | integration | FR-043a, AC-C7, AC-C8 — **NEW, revision 5.** Two concurrent `create_record_type` calls for one type produce one file and one refusal; an `edit_record_type` whose file moved under it is refused with the file unmodified |
| 58 | `TestProblems_QueryAndHealthViewAgree` | integration | FR-025a, AC-G1, AC-G2 — **NEW, revision 5 (ruling R-G).** A bad value in a property no query has touched appears in the health view; a query's problem set is a subset of the health view's for the same scope; clearing the note removes it from both after re-index, with no acknowledgement state |
| 59 | `TestBounds_AggregateOnlyIsExemptFromCandidateCap` | integration | FR-064a, AC-F8 — **NEW, revision 5.** 24,000 records return a `sum` with no rows and `COMPLETE: yes`; the same query with `select` or `limit` is refused |
| 60 | `TestRender_BudgetAllocationOrder` | unit | FR-127c — **NEW, revision 5.** Header and `NEXT` survive a response that must truncate; problems clamp at 20 with the "showing N of M" line; rows degrade last. **A 200-row page at `minimal` MUST NOT consume the whole cap** |
| 61 | `TestQuery_ProblemListClampsWithCount` | integration | FR-026 — **NEW, revision 5.** 3,000 excluded records produce 20 named plus a mandatory count line, not 180 KB and not a silent 20 |
| 62 | `TestFreshness_ConcurrentQueryDuringSync` | integration | FR-020c, A-13 — **NEW, revision 5.** `vault_find` runs concurrently with `SyncWith` under `-race`. *Revision 4's mechanism read a manifest that is not on the query path and has no mutex; the respecified mechanism reads a stored bleve field, and this test is what proves the race is gone rather than moved* |
| 63 | `TestFreshness_EveryPartialWriteFailurePointIsDetectable` | integration | FR-020c — **NEW, revision 5.** Table-driven over each failure point of the SQLite → bleve → manifest ordering, **stating for each whether it is detectable**. Revision 4's ordering made the reachable failure undetectable and its criterion tested an unreachable one |
| 64 | `TestTrash_RestoreRetentionAndWindowsSafePath` | integration | FR-048a — **NEW, revision 5.** A trashed note restores to its original path; the trash directory name contains **no colon**; a second trash of one path produces a second copy **and says so**; purge removes a 31-day-old entry |
| 65 | `TestMigration_NoRetiredKnowledgeToolNamesRemain` | integration | FR-049a — **NEW, revision 5.** The boot-time scan reports every skill, prompt and seeded policy naming a retired `knowledge_*` tool; the report being empty is W5's exit criterion |
| 39a | `TestQuery_NoComparisonIsDelegatedToSQL` | integration | **AC-8.10, ruling R-A — NEW, revision 6 (review round 6, C-5). THE control that makes R-A a property rather than an intention, and the highest-priority new test in this revision.** A query-boundary recorder captures every SQL statement the properties index executes for a corpus exercising all ten operators, `group_by`, `aggregate`, `sort` and `join`, and **fails on any comparison operator, `LIKE`, `IN`, `GROUP BY`, `ORDER BY`, aggregate function or `COLLATE` outside AC-8.10's named narrowing allow-list**. It replaces the deleted test 39 at the **store boundary** rather than in a compiler, so it survives the compiler's deletion and cannot be satisfied by a bypassed comparator. **CI treatment follows test 42's template**: name the job, the tag combination (`goolm,stdjson`) and the make target, and put it on the required-checks list |
| 89 | `TestStorage_IdentifierColumnDoesNotFold` | unit | **R-8 — NEW, revision 6 (review round 6, M-5).** Two records whose identifiers differ only in case (`CO-0142` / `co-0142`) coexist, are returned separately by the narrowing select, and compare **unequal** in the comparator. **Replaces the deleted AC-8.8 at the layer that now decides.** If a `BINARY` collation is declared on the narrowing column as a storage note, this test is what asserts it, and it asserts the OUTCOME (two distinct records survive), not the DDL |
| 66 | `TestScope_TruncatedResolutionReportsIncomplete` | integration | FR-062a — **SCHEDULED, revision 6 (review round 6, M-34).** It is the **P0 scope guarantee** revision 5 itself described as having had *"no test, no AC and no SC"*, and it then left it in §6 with no §7 schedule |
| 67 | `TestValue_NonConformingIsFlaggedNotAbsent` | unit | FR-021a, FR-021b — **SCHEDULED, revision 6 (M-34).** *"FR-021a is the whole of the R-4 defeat and had no test at all"* — and still had no schedule |
| 68 | `TestTools_ConfigureWritesOnlyVaultControlPlane` | integration | FR-070e, **FR-070e's two named exceptions (M-42)** — **SCHEDULED, revision 6 (M-34).** The mechanical rule adopted *because* the semantic criteria mis-decided three operations |
| 69 | `TestStorage_LinkedSQLiteVersionIsAsserted` | unit | FR-020i — **SCHEDULED, revision 6 (M-34)** |
| 70 | `TestObservability_CountersAndDegradedRebuildHeader` | integration | FR-049b — **SCHEDULED, revision 6 (M-34)** |
| 71 | `TestRender_TextIsAProjectionOfTheWireObject` | unit | AC-P3 — **SCHEDULED, revision 6 (M-34).** One of the rewritten ACs pass 1 asked for, left unscheduled |
| 72 | `TestReadPath_RateLimited` | integration | FR-067a — **SCHEDULED, revision 6 (M-34)** |
| 73 | `TestIndex_PropsRoundTripsExactDecimal` | unit | FR-013, FR-020b — **SCHEDULED, revision 6 (M-34)** |
| 74 | `TestQuery_AllValidCorpusReportsComplete` | integration | US-2.1 — **SCHEDULED, revision 6 (M-34)** |
| 75 | `TestFind_ThirdHopRefused` | integration | FR-065, US-3.5 — **SCHEDULED, revision 6 (M-34)** |
| 76 | `TestSchema_UndeclaredTypeIsAnOrdinaryNote` | unit | FR-005, US-1.1 — **SCHEDULED, revision 6 (M-34)** |
| 77 | `TestSchema_TypesAreScopedToRecordType` | unit | FR-009 — **SCHEDULED, revision 6 (M-34)** |
| 78 | `TestTools_NamesHaveNoDots` | unit | §0 correction 2 — **SCHEDULED, revision 6 (M-34)** |
| 79 | `TestTools_NoAgentCallableMountOperation` | integration | FR-019, US-12.5 — **SCHEDULED, revision 6 (M-34)** |
| 80 | `TestAudit_VaultEditAndRestructureCarryOperation` | integration | FR-071, FR-077 — **SCHEDULED, revision 6 (M-34)** |
| 81 | `TestDerived_NeverWrittenToFrontmatter` | unit | §3 non-behaviours — **SCHEDULED, revision 6 (M-34)** |
| 82 | `TestDescribe_CheckIntegrityNamesBothPaths` | integration | FR-039, FR-075, US-5.3 — **SCHEDULED, revision 6 (M-34)** |
| 83 | `TestWrite_ListSpliceAndMultiLineClobberRefused` | unit | FR-040a, FR-047 — **SCHEDULED, revision 6 (M-34)** |
| 84 | `TestRelate_ReplaceMustBeNamed` | unit | FR-030..FR-035 — **SCHEDULED, revision 6 (M-34)** |
| 85 | `TestRetrieval_NoEmbeddingDependency` | unit | FR-117 — **SCHEDULED, revision 6 (M-34)** |
| 86 | `TestFind_ExplainEvaluatesNothing` | integration | FR-073, AC-F3 — **SCHEDULED, revision 6 (M-34)**, and it carries **unasked question 5**: `explain: true` on a SQLite-less build. **RULED: `explain` ANSWERS** — it evaluates nothing, so there is nothing for FR-020h to refuse, and refusing a plan request would deny the caller the one response that explains why their typed query is unavailable. The plan MUST name the unavailability. AC-D6 and AC-F6 gain this case |
| 87 | `TestImport_NotRegisteredAsAgentTool` | unit | FR-103 — **SCHEDULED, revision 6 (M-34)** |
| 88 | `BenchmarkBounds_PeakRSSAtCap` | **benchmark** | SC-007, FR-066b — **SCHEDULED, revision 6 (M-34)**, and **reclassified**: §6 named it `TestBounds_PeakRSSAtCap`, but a peak-RSS measurement is a benchmark with a W1 write-back obligation, exactly as test 21 was reclassified. Measured at **both** bounds — B2's 10,000 and FR-064a's 50,000 |

**M-34 — TWENTY-FIVE tests were named in §6 and scheduled nowhere, and they are scheduled above.**
§6 named 86 distinct identifiers; §7 scheduled 73; twenty-five of §6's were absent, **including
several the revision itself added to close pass-1 findings**. §7 is the ordered implementation list
(*"Order is unit → integration → e2e; within a level, dependencies first"*), so **a test in §6 and
not in §7 has a requirement and no schedule** — which is how a P0 guarantee comes to have a
traceability row and no build step. *(Review round 6 counted 26; the twenty-sixth,
`TestFilter_CaseFoldIsUnicodeNotASCII`, is test 53 under its old name and is repointed rather than
added — see M-35's sibling below.)*

**M-35 — §6 and §7 named DIFFERENT tests for the same requirement, with OPPOSITE semantics, and §7
wins.** §6's FR-079/FR-128 row named **`TestTools_DescriptionTokenBudget`**; §7 test 38 is
**`TestTools_DescriptionBudgetIsReviewedNotEnforced`**, and its entire point is that the ~150-token
budget *"becomes a review checklist item at W5"* because FR-127b says it is *"never enforced at
runtime"* and *"a test MUST NOT conflate"* the two units. **A test implemented to §6's name enforces
the budget at runtime — the exact defect test 38 was rewritten to remove.** §6 takes test 38's name.
The same correction is applied to §6's `TestFilter_CaseFoldIsUnicodeNotASCII`, which is now test
53's `TestFilter_CaseFoldIsFullUnicode` (C-1).


### Test datasets

*(**R-F marker.** Every record-type, property and value name below is an **illustration of what a
vault might define** — see R-F. The product ships none of them. **What a test asserts is the SHAPE
and the remedy clause**, against a fixture schema the test itself declares — not these particular
words. The **property TYPE** names — `text`, `enum`, `relation`, `date`, `integer`, `decimal`,
`person` — are the exception: those are ours and are shipped, FR-004.)*

**DS-1 — property values.** Traces to FR-006, 007, 010, 011, 011a, 012, 013, 021d.
*(Every type and value name here is a fixture, not product vocabulary — R-F.)*

| Value | Property type | Expected | Traces |
|---|---|---|---|
| `active` | enum(prospect,active) | accepted | 1.3 |
| `Active` | enum(prospect,active) | **ACCEPTED — resolves to `active`** *(REVERSED, revision 5, ruling R-D. Was "rejected — case-exact")* | 1.3 |
| `ÄKTIV` | enum(äktiv,passiv) | **accepted — resolves to `äktiv`.** **This row is the one that fails over any SQLite-side fold**, and it is in the dataset for that reason (FR-011a) | edge |
| `STRASSE` | enum(straße,gasse) | **accepted — resolves to `straße`** (FR-011a). **NEW, revision 6.** Fails under `strings.ToLower` *and* under `strings.EqualFold`; passes only under `cases.Fold()`. Verified by execution, not asserted | edge |
| `ΣΊΣΥΦΟΣ` | enum(σίσυφος,άλλος) | **accepted — resolves to `σίσυφος`** (FR-011a). **NEW, revision 6.** Greek final sigma: fails under `strings.ToLower`, passes under `EqualFold` and under `cases.Fold()`. It is in the dataset because it is the pair on which the two stdlib functions **disagree with each other**, which is why neither is a safe default | edge |
| `ŁÓDŹ` | enum(łódź,gdańsk) | **accepted — resolves to `łódź`** (FR-011a). **NEW, revision 6.** Polish: the control row — all three mechanisms get it right, so a fixture containing only this pair proves nothing | edge |
| `İSTANBUL` | enum(istanbul,ankara) | **REJECTED — resolves to NOTHING, and this is CORRECT** (FR-011a, AC-8.9). **NEW, revision 6, and it is a NEGATIVE row.** Turkish dotted `İ` and plain `i` are different letters; `cases.Fold()` keeps them distinct, `strings.ToLower` collapses them. **The refusal names the permitted values, as every enum refusal does** — a reader who reads this as a folding gap has the sign backwards, and the reason is recorded in FR-011a so it is not "fixed" later | edge |
| `Actve` | enum(prospect,active) | **rejected** — resolves to nothing; permitted values listed | 1.3 |
| absent | enum | absent, not a value | 1.4 |
| `[a, b]` | enum scalar | **rejected** — arity | 1.4 |
| `""` | text | accepted, distinct from absent | edge |
| `349.98` | decimal | accepted, exact | 2.3 |
| 100 zeros after the point | decimal | accepted — at the bound (`maxDecimalScale`, `pkg/records/decimal.go:48`) | edge |
| 101 places after the point | decimal | **rejected** naming the bound and the value's own scale; **never rounded** | edge |
| `9223372036854775807` | integer | accepted — int64 max | edge |
| `9223372036854775808` | integer | **rejected** naming the bound; **never `CAST`** (SQLite would saturate silently) | edge |
| `-9223372036854775808` | integer | accepted — int64 min | edge |
| `3.5` | integer | **rejected** — a fractional part; the remedy names `decimal` | edge |
| `PLACEHOLDER — unknown` | decimal | **rejected**, record named | 2.2 |
| 2^53+1 | decimal | accepted, exact — the value a binary float would silently round | edge |
| `2026-09-01` | date | accepted — a day | edge |
| `2026-09-01T14:30Z` | date | accepted — an instant; **equal to the day above** per R-7 | edge |
| `2026-9-1` | date | **rejected** — *"month and day must be zero-padded"* | edge |
| `03/04/2026` | date | **rejected** — *"ambiguous and will not be guessed"* | edge |
| `2026-13-45` | date | rejected | edge |

**DS-4 — multi-value (`many`) properties. NEW, revision 5.** Traces to FR-028, FR-028a, R-9, R-13,
SC-002a. **There was no dataset for `many` arity at all**, which is how the join-fan-out defect
survived every review until grill pass 1.

| Record | `tags` (many) | Expectation |
|---|---|---|
| A | `[vendor]` | matches `= 'vendor'`; contributes **1** to `count` and its own value to `sum` |
| B | `[vendor, vendors]` | matches `IN ('vendor','vendors')` and contributes **1** to `count`, **not 2** — the fan-out case |
| C | `[Vendor]` | matches `= 'vendor'` — element matching is case-insensitive (R-9, R-10) |
| D | `[]` | an empty list is a **value**, not absence (R-3): `IS NULL` is false, `IS NOT NULL` is true |
| E | absent | **CORRECTED, revision 6 (C-7).** `IS NULL` is **true**; `IS NOT NULL` is **false**; a `<>` leaf **EXCLUDES** it (R-2 — an absent side is `false` for every operator but the two null tests). *(Revision 5's row said `<>` includes it, contradicting R-2, from which `TestComparisonTruthTable` generates its cells.)* |
| E2 | absent | **NEW, revision 6, and it is the pair to row E.** `{not: {tags, "=", "vendor"}}` **INCLUDES** it — `NOT(false)` is `true`, at any depth. **Rows E and E2 differ in outcome over the same record**, which is the whole point: a tree walker and a leaf evaluator can disagree, and this dataset is what stops them (DS-5 carries the compound form) |
| B, F | both worth the same amount | `sum` is the sum of both — the case `SUM(DISTINCT)` gets wrong |
| any | `< 'vendor'` | **refused** — ordering is undefined over a list (R-13), with `=`/`IN`/`LIKE` named |
| G | `[vendors]` | **`LIKE 'vendor'` is FALSE** — `LIKE` is **anchored**, whole-value, as SQL's is; `LIKE 'vendor%'` is true. **NEW, revision 6 (M-14)** — the single most likely implementation divergence, and R-9's *"matches an element by pattern"* reads like substring matching |
| H | `[straße]` | **`LIKE 'stra_e'` is FALSE; `LIKE 'stra__e'` is TRUE.** **NEW, revision 6 (M-14).** Folding changes rune count (`straße` → `strasse`, 6 → 7), and `_` counts characters of the **folded** subject (FR-011a) |
| I | `[a%b]` | **`LIKE 'a\\%b'` matches; `LIKE 'a%b'` also matches (`%` is a wildcard here).** **NEW, revision 6 (M-14)** — the escape character is never folded, so `\\` still escapes after folding |
| J | any | **`IN []` is REFUSED**, naming the operator and the remedy (FR-022d). **NEW, revision 6 (M-13)** — an empty `IN` list matches nothing and is `LIKE ''`'s sibling |
| K | any | **`IS NULL` with a `value` present — including JSON `null` — is REFUSED** (FR-022d). **NEW, revision 6 (M-13)** |

**DS-5 — absence across a nested boolean tree. NEW, revision 5.** Traces to FR-008, FR-023b,
SC-002b. **Revision 4 had no dataset for NULL across a tree**, so every negation case it tested was
leaf-shaped.

| Record | `a` | `b` | `{not:{all:[a=1,b=2]}}` | `{not:{any:[a=1,b=2]}}` |
|---|---|---|---|---|
| 1 | 1 | 2 | excluded | excluded |
| 2 | 9 | 9 | **included** | **included** |
| 3 | absent | 2 | **included** | excluded |
| 4 | 1 | absent | **included** | excluded |
| 5 | absent | absent | **included** | **included** |

*Rows 3, 4 and 5 are the ones a tri-state evaluator drops. Under SQL's `NOT` the first column
returns row 2 alone and the second returns row 2 alone; under R-2's `false`-for-absent rule and a
real `bool`, the expectations above hold.*

**DS-6 — concurrency. NEW, revision 5.** Traces to FR-037, FR-043a, FR-020c, A-13. Two processes
creating records concurrently (test 11); two agents calling `create_record_type` for one type
concurrently (test 57); a `vault_find` running concurrently with `SyncWith` under `-race`
(test 62). **Revision 4 had no concurrency dataset**, and holdout scenario 6 — which covers
concurrent writes — is explicitly not for use during development.

**DS-2 — relation targets.** Traces to FR-030..034.

| Target | Expected |
|---|---|
| existing record, correct type | resolves |
| existing note, no `type` | reported: not a record |
| existing record, wrong type | reported: type violation |
| non-existent | reported: dangling |
| record in another workspace's vault | invisible; dangling within scope |
| ambiguous basename matching two notes | reported: ambiguous, both paths named |

**DS-3 — write corpus.** Traces to FR-041. 50 real notes including: leading comments, comments
between properties, blank lines inside frontmatter, single- and double-quoted scalars,
unquoted scalars, a multi-line block scalar, CRLF line endings, and
a file whose frontmatter is the entire file.

### Regression

The record layer is new capability, but **FR-111 and FR-110 modify `pkg/knowledge/index.go`**,
which search already depends on, and **FR-110 changes rankings across three subsystems**.
Existing `pkg/knowledge` tests MUST pass unchanged. Seam tests:

- `TestIndex_StaleFormatIsRebuiltNotOpened` — an index written at the previous format version
  is rebuilt, not opened. **This is the seam that matters**: `openOrCreateBleve` calls
  `bleve.OpenUsing` and never re-applies the mapping, so without the version bump an upgraded
  install would query a field that does not exist and report `complete: true`. The spike
  **reproduced** exactly this: 1 hit on a fresh index, 0 hits and `err == nil` on an existing one.
- `TestIndex_PersistedMappingAsserted` — the G2 guard (FR-020d). It exists because G1 depends on
  a developer remembering to bump a constant, and this project has a documented history of guards
  that pass with the thing they guard deleted.
- **`TestKnowledgeSearch_RankingChangeIsIntentionalAndBounded`** — **REPLACES revision 2's
  `TestKnowledgeSearch_ScoringUnchangedByPropsField`, which is now impossible to satisfy.** FR-110
  changes scores by design: BM25's saturation and length normalisation are exactly what TF-IDF
  lacks. The honest assertion is therefore not "scores are identical" but: the **matched set** is
  unchanged (AC-8.6), the ranking change is recorded against a fixture corpus, and the memory-room
  and retrospective rankers (`pkg/agent/retro_bm25.go`) are re-checked for the comparability claim
  they make — which was false while bleve produced TF-IDF.
- `TestIndex_RebuildWithRecordFields` — a rebuilt index returns identical ranked results to a
  freshly built one at the same generation.

---

## 8. The comparison oracle — the highest-risk item, specified

ADR-068 §4.2 names the risk: to filter over real frontmatter, comparison operators must accept
`any`, and the expression engine's type checker then stops protecting them. During research a
first-attempt overload made `3 > 2` evaluate to **false**, with nothing reporting an error.

`TestComparisonTruthTable` is a first-class deliverable. Revision 1 demanded it be written
"from the specification" while the specification defined roughly **three of ~648 cells** — so
in practice the other 645 would have been read off the implementation, which is exactly the
failure the test exists to prevent.

**The oracle is these rules. Every cell is generated from them, and the rules — not the cells —
are what a human reviews.**

> **THE PROMOTION IS PERFORMED HERE, revision 6 (review round 6, M-32) — revision 5 ANNOUNCED it in
> §6 and in §11's W2 row and then defined no `FR-2xx` anywhere, so §6 still traced the highest-risk
> item in the document to a table by prose reference, which is the defect the promotion was written
> to close.** The mapping is **one-to-one and closes no gap**, so the retired rule keeps its number
> rather than everything below it shifting:
>
> | FR | Rule | | FR | Rule | | FR | Rule |
> |---|---|---|---|---|---|---|---|
> | **FR-200** | R-1 | | **FR-205** | **R-6 — RETIRED** (money) | | **FR-210** | R-11 |
> | **FR-201** | R-2 | | **FR-206** | R-7 | | **FR-211** | R-12 |
> | **FR-202** | R-3 | | **FR-207** | R-8 | | **FR-212** | R-13 |
> | **FR-203** | R-4 | | **FR-208** | R-9 | | | |
> | **FR-204** | R-5 | | **FR-209** | R-10 | | | |
>
> **`FR-205` is retired alongside `R-6` and MUST NOT be reused** — the same treatment FR-014 gets,
> and for the same reason: an older test name or commit that says `FR-205` must resolve here rather
> than to a different requirement wearing the number. **Thirteen numbers, thirteen declared rules,
> twelve live** (M-33). Both identifiers are valid in citations; `R-n` stays the readable name and
> `FR-2nn` is what §6's traceability matrix and §11's wave table carry.

| # | Rule |
|---|---|
| **R-1** | **`text` and `enum` are ONE declared type for comparison purposes, and `integer` and `decimal` are ONE; every other pair is different. RULED, revision 6 (review round 6, unasked question 2) — R-1 explicitly unified `integer` and `decimal` and said nothing about `text`/`enum`, while AC-8.1 generates the cell and no rule supplied its value, so the truth table had a cell an implementer would fill in by guessing.** The ground: an `enum` value **is** text that happens to be drawn from a closed set, it folds with the same function (FR-011a), it sorts with the same lexical rule (R-5), and refusing `text = enum` would make a filter break the moment an author tightened a `text` property into an `enum` — a schema change that should narrow what validates, not what compares. **A `text` value that is not a declared member of the `enum` still compares** (it is a value, not a non-conformance on the text side); the enum side's own non-membership is R-4's business, unchanged. A comparison between values of **different declared types** is `false`. Never an error, never a coercion. `"3" > 2` is false because one is text and one is a number. **`integer` and `decimal` are ONE declared type for this rule** *(revision 5)*: an author chooses the storage, not a distinct comparison domain, so `3 = 3.0` is **true** and an `integer` compares with a `decimal` numerically. R-1 separates text from numbers, not int64 from arbitrary precision. |
| **R-2** | A comparison where **either side is absent** is `false`, for every operator except `IS NULL`. **This is the rule that makes FR-008 right by construction rather than by a compiler pass** *(revision 5)*: because a comparison over an absent side is a real `false`, `NOT(false)` is `true`, so a negative filter includes the absent records **at any depth of the filter tree**. SQL's three-valued `NOT(NULL) = NULL` is what made this need a leaf rewrite *and* a normalisation pass, neither of which the comparator needs — see §8.1. **`<>` IS GOVERNED BY THIS RULE AND IS NOT AN EXCEPTION — ruled explicitly, revision 6 (review round 6, C-7), because three normative places said the opposite.** A `<>` leaf over an absent side is **`false`**, so the record does **NOT** match. **The exception list is `IS NULL` and `IS NOT NULL`, and nothing else.** The reasoning is not a preference: ruling **R-B** adopts *"SQL operator names and **semantics**, not our invented ones"*, and in SQL `x <> 'v'` over a NULL `x` yields NULL and the row is excluded. Exempting `<>` while leaving `<`, `<=`, `>`, `>=` governed would be exactly the invented semantics R-B forbids, and there is no principle that separates them. **The capability is not lost, it moves one level up:** to ask *"which records are not `v`, including those that never said"* — the spec's own motivating *"which days did I not meditate?"* — write `{not: {p, "=", v}}`, which is R-2-correct at any depth because `NOT(false)` is `true`; or write it out as `{any: [{p, "<>", v}, {p, "IS NULL"}]}`. **`<>` is a leaf; `not` is a tree. The document conflated them and FR-008's phrase "a negative filter" carried the ambiguity** — FR-008 is corrected to name the tree form. |
| **R-3** | `IS NULL` is `true` exactly when the property has no value, and `false` otherwise; `IS NOT NULL` is its complement. An empty string, an empty list and a zero are **values**, not absence. *(Revision 5: the operator is named in SQL's vocabulary per ruling R-B; the rule is unchanged.)* |
| **R-4** | A value present but **not conforming to its declared type** does not compare. It is `false` for every operator **and** the record is added to the query's problem list. Silence here is the defect. |
| **R-5** | **REVERSED, revision 5 (operator rulings R-E and R-D).** `enum` is a **closed set**, and it **orders lexically, computed in Go — not by declared position, and not by SQLite.** A domain order is expressed by **prefixing the values** (`1-lead`, `2-qualified`). Equality resolves a value **case-insensitively** (full Unicode, in Go — FR-011a) to a declared value; a value resolving to none of them is a reported problem. **The ordering rule is stated in full, revision 6 (review round 6, M-7 and M-8), because revision 5 assigned sorting to nobody and then described it four times as SQLite's.** *(a)* **Sorting happens in Go**, in the comparator, like every other comparison — FR-021's list of what moves back to Go omitted it, which is how *"SQLite's own ordering"* survived the ruling that deleted SQLite's role. *(b)* **The order is byte-lexical over the value string** — the same order SQLite's `BINARY` collation would produce, computed by us. *(c)* **The sort KEY is the FOLDED form** (`cases.Fold()`), not the raw bytes, and this is a decision, not an inherited default: byte-lexical order over raw values puts every capitalised value before every lowercase one (`"Won" < "lost"` is **true** on raw bytes and **false** folded — executed), so a corpus that FR-011 deliberately permits to hold `Won`, `won` and `WON` as **one** value would render them in **three** separate places in a sorted result while `group_by` collapsed them into **one** group. Sorting on the folded key makes ordering, equality and grouping agree — which is the only combination a reader can reason about. *(d)* **Ties on the folded key are broken by raw byte order**, so the order is total and **deterministic across runs** (O-5); an implementation that leaves ties to Go's map iteration order breaks SC-014's byte-identical-across-rebuild assertion. *(e)* **What renders is the file's own spelling**, never the sort key (FR-011, FR-011c). *(Two changes: "compares by declared position" is withdrawn by R-E, deleting the ordinal column and its bookkeeping; "equality is exact-case" is withdrawn by R-D, because resolving `Won` **to** `won` collapses two spellings into one value rather than creating a second, which is the thing D4 actually forbids.)* |
| ~~**R-6**~~ | **RETIRED, revision 5 (operator ruling 1) — `money` is deleted from the type system, so this rule has no subject.** *Was: "`money` compares only within one currency. Across currencies every operator is `false` and the query reports the currencies present."* **The number is retired rather than reused**, so an older test name, commit or review that says "R-6" resolves here rather than to a different rule wearing the number. Removed with it: R-6's §8.1 defeat row, every R-6 cell of AC-8.1's generated table, `TestMoney_RefusesCrossCurrencySum`, and the "cross-currency `MIN`/`MAX`/`AVG`/`ORDER BY`" gap the grill raised as C-3 I-4 and C-10 — all four had `money` as their only subject. **The rule count is therefore THIRTEEN declared rules (R-1..R-13) of which one is retired — TWELVE LIVE.** *(Revision 6, review round 6, M-33: revision 5 wrote "twelve declared rules of which one is retired, not thirteen", which implies eleven live and was wrong while correcting a count.)* §8.1 and AC-8.1 are restated accordingly. |
| **R-7** | **REVISED, revision 5 — the rule is unchanged, its storage is now specified, because it had no defeat and SQLite violates it four ways (§8.1b).** `date` compares as an instant. A date and a date-time are the same declared type and compare directly. **CORRECTED, revision 6 (review round 6, C-6): the storage MUST is DELETED.** Revision 5's *"a `date` MUST be stored as a signed integer epoch with a declared precision, never as text"* was withdrawn by **FR-021d** in the same revision (*"the storage form is not load-bearing and the requirement is withdrawn"*) and by §8.1's deletion table, and it pointed the reader at an §8.1 row that no longer contains a storage specification. A MUST in the rule table, contradicted by the row it cites, in the one table this document says *"the rules — not the cells — are what a human reviews"*. **What replaces it:** a `date` value MUST be **parsed in Go** per FR-021d's strict ISO-8601 grammar (ruling R-H), and a value that does not parse is a **reported problem under R-4**, not a comparison. **Storage form is unconstrained** — nothing compares dates in SQLite, so nothing depends on how the narrowing column spells them. |
| **R-8** | `relation` compares by **target identity**, never by display text. Two links resolving to the same record are equal regardless of spelling. **RESTATED, revision 6 (review round 6, M-5) — revision 5 specified this as two SQLite columns with two collations, and under ruling R-A no collation decides anything.** The split is real and survives; the mechanism is the comparator, not the column. **The path/name side folds** with `cases.Fold()` (FR-011a) — which also removes a real macOS-vs-Linux divergence in wikilink resolution. **The identifier side is compared BYTE-EXACTLY by the comparator, with no folding applied**, because folding would make `CO-0142` and `co-0142` one key and two legitimately distinct targets could not coexist. *(Revision 5 said "matched exactly, on a `BINARY` column", and enforced it with AC-8.8, a `BINARY`-collation assertion over emitted DDL — which revision 5 then **deleted** as having SQL-side comparison as its only subject. The rule survived and its enforcement did not. It is enforced now by AC-8.9's sibling in AC-8.4b's mutation set — mutation 8, which folds the identifier side and must make a cell fail.)* **If a `BINARY` collation is still wanted on the narrowing column** — and it costs nothing to declare one — that is a **storage note with no semantic weight**, stated here as such: the narrowing SELECT must not silently collide two identifiers before the comparator ever sees them. It is asserted by §7 test 89, not by AC-8.8. |
| **R-9** | **RESTATED IN SQL'S TERMS, revision 5 (operator ruling R-B).** Against a `many` property, **`=` matches an element exactly** and **`IN` matches any element of a list**; **`LIKE` matches an element by pattern.** *(Was: "`contains` on a list is whole-element membership. It is never substring matching." The old rule existed because one invented operator had to serve two meanings; SQL's vocabulary already separates them and the caller chooses.)* Element matching follows R-10's case rule, so a list holding `Vendor` matches `= 'vendor'`. |
| **R-10** | **REVERSED AND RESTATED, revision 5 (operator rulings R-D and R-B).** On text, **`=` is exact and `LIKE` is patterned, `%` and `_` meaning what they mean in SQL** — and **both are case-INSENSITIVE**. *(Was: "`contains` on text is substring matching, case-sensitive." Both halves change: the operator is now SQL's, and the case-sensitivity is reversed by ruling R-D.)* **The insensitivity is full Unicode, because it is performed in Go by the comparator** (FR-011a). It would have been **ASCII-only** if delegated to SQLite — `COLLATE NOCASE`, `LIKE` and `lower()` fold `A`/`a` and fold **nothing** outside ASCII, verified over fourteen non-ASCII pairs (§8.1) — which is a second, independent reason the comparator decides (ruling R-A). |
| **R-11** | **WIDENED, revision 5; DETERMINISM ADDED, revision 6 (review round 6, O-5).** **Comparison and ordering MUST be DETERMINISTIC across runs for the same inputs** — same corpus, same query, byte-identical result, every time and in every process. This is stated because **SC-014 asserts byte-identical results across a rebuild** and **Go map iteration order is the classic way that stops being true**: a grouping pass, a problem list or a tie in the sort that is emitted in map order will differ run to run while every rule above still holds. **Every ordered output — rows, groups, problems, `NEXT` entries — MUST have a total order specified to the last tie-break** (R-5 clause (d) gives the sort's: folded key, then raw bytes). Determinism is a property of the whole comparator, not of any one rule, which is why it sits here. ** Comparison is **total and never panics**: every type pair × every operator yields a boolean or a reported problem. There is no third outcome. **"Third outcome" is not a synonym for "error", and treating it as one was the defect:** four of SQLite's third outcomes are *silent* — `1/0` → NULL, scalar int64 overflow → REAL, `unixepoch('bad')` → NULL, `SUM` over an empty set → NULL — while `SUM` overflow *is* a loud error for the same arithmetic. All five are enumerated in §8.1's R-11 row and in **§8.1b**. *(Revision 6, review round 6, M-38: revision 5 said "with their defeats" — the defeats are withdrawn under R-A — and pointed at "§8.1a §D", which is neither the section holding the receipts (that is §8.1b, §8.1a now holds rulings R-B/R-C/R-E) nor a subsection that has ever existed.)* |
| **R-12** | Every rule above applies **identically** whether the value came from a query literal or from a record. **RESTATED, revision 6 (review round 6, M-10): SQLite violates this rule, and the violation is UNREACHABLE because there is one comparison path — it does not have, and no longer needs, a "defeat" (§8.1's R-12 row, which is a reason, not a defeat; §8.1's deletion table marks R-12 N/A).** The violation, recorded because it is why one path matters: comparison affinity converts a TEXT operand only when the other side is a typed column, so `3 = '3'` is **false** between two literals and **true** between a column and a literal — identical values, identical operator, opposite answers depending on provenance. |
| **R-13** | **NARROWED, revision 5 (operator ruling R-B) — most of what it refused now has a defined answer.** Against a `many` property, **`=`, `<>`, `IN`, `LIKE`, `IS NULL` and `IS NOT NULL` are defined**, and they mean what R-9 says: element-wise, with the record matching if **any** element matches. **Only the ORDERING operators — `<`, `<=`, `>`, `>=` — remain undefined**, and they are reported as a problem naming the remedy: *"`segment` holds many values; ordering comparisons are not defined over a list — use `=`, `IN` or `LIKE`"*. *Originally added 2026-08-25 because `segment != vendor` had no defined answer; **it has one now**, in a vocabulary the caller already knows, which is the better resolution of the same gap. The refusal survives only where the question is genuinely undefined: "is this list greater than `vendor`?" has no answer in any vocabulary.* **The reasoning that survives:** an agent that gets a helpful answer to a malformed query never learns the schema, and the refusal names the fix exactly as FR-024 does for an unknown property. |

**AC-8.9** **NEW, revision 6 (the operator's requirement, and it is written as literal cells so it
cannot be softened into a description).** The comparator's fold is asserted over these **six literal
pairs**, each with the stated expectation and each with the reason recorded, so that a later change
of mechanism fails a test rather than a user:

| Case | Left | Right | Expected | Why this pair is in the set |
|---|---|---|---|---|
| **AC-8.9a** | `straße` | `STRASSE` | **MATCH** | German `ß`. **Full** folding. `false` under `strings.ToLower`; `false` under `strings.EqualFold`. This is the cell that fails if anyone substitutes a stdlib call |
| **AC-8.9b** | `σίσυφος` | `ΣΊΣΥΦΟΣ` | **MATCH** | Greek final sigma. `false` under `ToLower`, `true` under `EqualFold` — **the two stdlib functions disagree**, which is why neither is a permitted default |
| **AC-8.9c** | `müller` | `MÜLLER` | **MATCH** | German umlaut. All mechanisms agree; included as the ordinary case so the set is not all edge |
| **AC-8.9d** | `łódź` | `ŁÓDŹ` | **MATCH** | Polish. The control row — a fixture containing only rows like this one discriminates nothing |
| **AC-8.9e** | `istanbul` | `İSTANBUL` | **MUST NOT MATCH** | **Turkish dotted `İ` and plain `i` are different letters.** `true` under `ToLower` — which is the classic **Turkish-I bug**, not a feature. `cases.Fold()` maps `İ` to `i` + U+0307 and keeps them apart. **This cell is asserted as a NEGATIVE with the reason in the assertion message**, because otherwise the next person to read it will "fix" it |
| **AC-8.9f** | `ﬁle` | `file` | **MATCH** | Ligature. `false` under both stdlib functions. Not a language case — it is here because it is the second independent witness that simple folding is not enough |

**AC-8.9 fails if the comparator produces the same six answers as `strings.ToLower`, or the same six
as `strings.EqualFold`** — that is the discriminating property, and stating it this way means the
criterion cannot be satisfied by an implementation that folds nothing at all in a fixture that
happens to be ASCII. Every expectation above was **executed** against `golang.org/x/text v0.41.0` on
Go 1.26.6 before it was written down; none was reasoned to.

**AC-8.1** — the generated table covers every declared type × every declared type × every
operator, plus absent and non-conforming on both sides, and every expected value traces to a
numbered rule above. **Revision 5 adds three obligations to the generator, each closing a hole the
grill found:** ~~every cell is generated in **both provenances** — literal-vs-literal and
column-vs-literal — because R-12's row shows the two disagree~~ **— DELETED, revision 6 (review
round 6, M-9 / V-1). There is ONE comparison path and therefore ONE provenance** (§8.1's deletion
table marks R-12's literal-vs-column affinity asymmetry **N/A**; ADR-068 D16.6: *"One comparator
means one provenance"*). Keeping the axis **doubled the size of the generated table while every
added cell asserted the same value as its twin** — growth in size with no growth in discriminating
power, which is worse than a vacuous criterion because it costs real runtime. *(R-12 itself stays
live and is mutated — AC-8.4b mutation 12 — because a comparator **could** still be written to
treat the two sides differently; what is deleted is generating the whole table twice to look for
it.)* The two axes that ARE live are kept: the type axis is the **seven types
of FR-004** (`money` cells are deleted, `integer` and `decimal` cells are added, and `integer × decimal`
is asserted to compare numerically per R-1); and **`many` arity is a third axis**, so the fan-out
case of §8.1b is generated rather than remembered.

**AC-8.2** — a comparator change that requires editing a **rule** is a specification change and
must be argued as one. A change that only regenerates cells is an implementation detail.

**AC-8.3** — `3 > 2` is `true`. Stated explicitly because it is the case that actually failed.

### 8.1 Where the rules are ENFORCED — and why SQLite does not enforce them

> **REWRITTEN IN FULL, revision 5, by operator ruling. This section previously specified how to
> defeat SQLite's semantics rule by rule. It no longer does, because SQLite no longer decides
> anything.** The ruling: **the properties index NARROWS CANDIDATES; our own tested comparator
> DECIDES.** SQLite answers *"which notes are `type: task`?"* and hands back candidate rows; the
> comparator in `pkg/records/compare_oracle.go` then applies R-1..R-13 to those candidates. **No
> comparison is delegated to SQL.**
>
> **What this deletes, named so the deletion is auditable rather than a quiet shrinkage.** Revision
> 4 listed nine SQLite violations at seven defeat sites; this revision had grown that to ten
> violations at eleven sites. **All of them are now NOT APPLICABLE rather than defeated**, and the
> requirements written to defeat them are withdrawn with them:
>
> | Was | Status under the ruling |
> |---|---|
> | R-1's declared-type guard + `CHECK(typeof(...))` backstop | **N/A** — affinity cannot decide a comparison SQL never performs |
> | R-2/R-3's `(x IS NULL OR x <> ?)` leaf rewrite | **N/A** — and see the paragraph below, which is the strongest single argument for the ruling |
> | **FR-023a**, De Morgan normalisation before emission | **WITHDRAWN.** It existed to stop `NOT (subtree)` dropping NULL-bearing rows in SQL. A Go evaluator returning a real `bool` gets this right by construction |
> | R-5's ordinal column and `NULLS LAST` | **N/A** — and separately deleted by ruling R-E, below |
> | R-7's integer-epoch storage (**FR-021d**) | **WITHDRAWN as a storage requirement.** Dates are parsed and compared in Go. Ruling R-H replaces it with a parsing rule |
> | R-9/R-10's `instr()` and the **FR-011a** fold column | **WITHDRAWN.** Folding in the comparator delivers what SQLite cannot deliver at all. *(Revision 6 corrects this row's stated reason: it said "Go's `strings.ToLower` is Unicode-aware", which is true and irrelevant — `ToLower` performs **simple** folding and fails `straße`/`STRASSE`. The mechanism is `golang.org/x/text/cases.Fold()`, FR-011a. The withdrawal itself is unaffected.)* |
> | R-11's five third outcomes | **N/A for four of five.** No SQL division, no SQL `SUM`, no SQL `unixepoch`, no SQL `CAST`. Only "a store error must not escape as a third outcome" survives |
> | R-12's literal-vs-column affinity asymmetry | **N/A** — there is one comparison path and one provenance |
> | **FR-028a**, the `EXISTS` semi-join against join fan-out | **WITHDRAWN.** Counting distinct records is what a Go loop does; it was a defect of aggregating in SQL |
> | **AC-8.4a** (a mutation per defeat), **AC-8.7** (zero `LIKE`), **AC-8.8** (BINARY id column) | **DELETED.** AC-8.7 was already deleted by ruling R-D; the other two had defeats as their only subject |
>
> **This is a stronger position than eleven deliberate defeats, and it is worth saying why rather
> than only that it is simpler.** Every one of those defeats was a line of a query compiler nobody
> had written, and **nine of the ten violations failed in the quiet direction** — a compiler that
> forgot one produced a passing test suite and a wrong product. A-11 existed precisely to say that a
> *specified* defeat and a *verified* defeat are different things. **The ruling removes the class
> rather than mitigating it**: there is no compiler to forget a line, because the comparator that
> decides is the one that already exists, is already tested, and is the subject of §8's rule table.

**The receipts are KEPT, and their status changes from "what we must defeat" to "why we do not
delegate".** They were executed against the `sqlite3` CLI 3.51.0 and re-executed identically
through `modernc.org/sqlite v1.46.1`, which reports `sqlite_version()` = **3.51.2** — verified by
opening a database through the driver. *(The grill's M-37 said the shipped engine reports 3.53.3
and the receipts were two minor versions stale. That figure does not reproduce; see the header
table. FR-020i still asserts the linked version, because a driver bump must be a review trigger.)*

| Rule | What SQLite does by default | Why that is now a reason, not a task |
|---|---|---|
| **R-1** | `SELECT '3' > 2` → **`1`**. An `INTEGER`-affinity column holding `'3abc'` keeps `typeof = 'text'` and still answers `'3abc' > 2` → **`1`**. `STRICT` coerces `'3'` and rejects only `'3abc'`; `CHECK(typeof(n) IN ('integer','null'))` rejects both | SQLite has **affinity, not types**. R-1 is a statement about declared types, which SQLite does not have, so no configuration of SQLite can express it |
| **R-2 / R-3** | `NOT (a=1 AND b=2)` over a 5-row fixture returns **1 row**; the NULL-safe rewrite returns **4**. `NOT (a=1 OR b=2)` returns **1 row** (*the grill said zero; it does not reproduce — the correct answer is 4, so **three** rows are dropped, not four*). `NOT (a>5)` drops the NULL row. `typeof(instr(NULL,'x')>0)` is **null**, so `instr` drops it too | **This is the sharpest one and it is the clearest argument for the ruling.** SQL's `NOT` is three-valued: `NOT(NULL)` is `NULL`, which is not `TRUE`, so absent rows fall out of every negation. **R-2 says a comparison with an absent side is `false`; a Go comparator returning a real `bool` therefore makes `NOT(false)` = `true`, and FR-008's "which days did I not meditate?" returns the days with no entry — by construction, with no line anyone can forget.** In SQL the correct behaviour needed a leaf rewrite *and* a normalisation pass, and nothing reported the absence of either |
| **R-5** | `ORDER BY` over TEXT is lexical: `lead, proposal, qualified, won` | **No longer a violation at all** — ruling R-E adopts exactly this ordering. See below |
| **R-7** | Two spellings of one instant compare unequal and order anyway; fractional seconds invert (`Z` sorts after `.`, **even in an all-UTC corpus**); the `T`-vs-space separator reorders; any non-UTC offset breaks ordering. `unixepoch('not-a-date')` and `unixepoch('2026-8-26')` both return **NULL, with no error** | Comparison is Go-side over parsed instants, so none of it applies. The `unixepoch` NULL is why **parsing is never delegated either** (FR-021c) — a parse failure that returns NULL is indistinguishable from absence |
| **R-9 / R-10** | `'ACME' LIKE '%acme%'` → **`1`**; `'vendors,partner' LIKE '%vendor%'` → **`1`** | Ruling R-D makes case-insensitivity **desired**, and ruling R-B adopts `LIKE`'s own semantics as the vocabulary. What SQLite folds is nonetheless **ASCII-only** — see the Unicode receipt below, which is why the fold is Go's |
| **R-11** | `9223372036854775807 + 1` → **REAL**, silently; `1/0` → **NULL**, silently; `SUM` over an empty set → **NULL**; `SUM` **overflow** → a hard `integer overflow` error. `CAST('9223372036854775808' AS INTEGER)` → **`9223372036854775807`**, saturated silently | Five outcomes for one arithmetic, four of them silent. **None is reachable**: no arithmetic is emitted. The one that survives into the design is the rule that a **store** error (a corrupt index, a closed database) must be caught at the boundary and rendered as a problem row, never escape as a third outcome |
| **R-12** | `3 = '3'` is **0** between literals and **1** between a column and a literal; `'2' > 3` is **1** and **0** respectively; a `BLOB`-affinity column restores literal behaviour | Provenance changes the answer **only when SQL performs the comparison**. One comparator, one provenance, rule satisfied |
| *(join fan-out)* | A record carrying both `vendor` and `vendors` joins **twice**: `COUNT(*)` → **2** and `SUM(amount)` → **200** where truth is **1** and **100**. `SUM(DISTINCT)` returns **100** against a truth of 200 — wrong in the *conservative-looking* direction, which is the harder wrong answer to catch | Aggregation is Go-side over a de-duplicated record set. **This was the worst finding of grill pass 1 and the ruling removes it rather than defeating it** |

**The Unicode receipt, kept because it bounds ruling R-D.** Across fourteen upper/lower pairs
(`A/a`, `Z/z`, `Ä/ä`, `É/é`, `Ñ/ñ`, `Ø/ø`, `Ç/ç`, `Å/å`, `Σ/σ`, `Д/д`, `İ/i`, `Ł/ł`, `Ż/ż`, `Ć/ć`)
**`COLLATE NOCASE`, `LIKE` and `lower()` each folded the two ASCII pairs and ZERO of the twelve
non-ASCII pairs**; `lower()` returned every non-ASCII input byte-for-byte unchanged
(`hex('Ä')` = `hex(lower('Ä'))` = `C384`). `PRAGMA compile_options` carries no `ENABLE_ICU` and
`icu_load_collation` does not exist, so **there is no Unicode-aware option inside SQLite here at
all.** **Case-insensitive matching is therefore something our comparator can deliver and SQLite
cannot** — a second, independent argument for the ruling, arriving from a direction nobody was
looking in.

**The receipt's second half, added in revision 6, because the first half alone led revision 5 to the
wrong remedy.** Having established that SQLite folds ASCII only, revision 5 concluded that Go's
`strings.ToLower` was *"Unicode-aware, so this is full-Unicode case insensitivity for free"* and
stopped there. **That conclusion was not executed, and it is false.** Executed on Go 1.26.6 over
the same class of pairs: `strings.ToLower("straße") == strings.ToLower("STRASSE")` is **false**, and
`strings.EqualFold("straße","STRASSE")` is **false** too; `strings.ToLower("ΣΟΦΟΣ")` is `σοφοσ`, not
`σοφος`, so the Greek pair is **false** under `ToLower` and **true** under `EqualFold` — the two
functions disagree; and `strings.ToLower` collapses Turkish `İ` onto `i`, which is a **wrong match**,
not a missing one. Go's standard library implements **simple** case folding; `ß`→`ss` and `ﬁ`→`fi`
are **full** case folding, which it does not perform. `golang.org/x/text/cases.Fold()` performs
full folding and gets all six of AC-8.9's pairs right, verified against `golang.org/x/text v0.41.0`.
**This is the same failure the receipt above catches in SQLite, committed one layer up in Go** —
which is why the mechanism is now named as a specific function in FR-011a rather than as a property
("Unicode-aware") that no API guarantees.

**What the properties index is still for, so the ruling does not read as "delete SQLite".** It
narrows. `type = 'deal'`, `path` prefix within scope, `kind = 'task'`, the relation child table's
`rec_id` join for *reachability* — these are set-membership questions over indexed columns, they are
what an index is good at, and **none of them is a comparison governed by R-1..R-13.** FR-020's
"select candidates by record type and property without materialising documents that cannot match"
survives exactly as written. What moves out of SQLite is **evaluation**, not selection.

**The cost, stated honestly rather than discovered later.** Filtering, grouping and totals now run
in Go over the candidate set, so **a query matching very many records is slower than one pushing
predicates into SQLite**, and the cost scales with candidates rather than with results. Two things
bound it and neither is a hope: FR-064's 10,000-candidate cap is a **refusal**, not a politeness
limit, and it was set for exactly this reason; and a vault is a few thousand notes, not a database.
**Nobody has measured the Go path over the two-index design** — W1 measures it, and FR-064a's
aggregate-only exemption is the one place the cap is relaxed, which is now a **Go** scan over a
candidate stream rather than a SQLite pushdown. *(A-14 records this as live.)*

**Three consequences for other requirements, stated in place rather than left to be discovered:**

- **FR-021 reverts to its original meaning** — evaluation in Go over the candidate set. Revision 3
  moved it into the properties index ahead of ADR-068 D16.2b; that move is now reversed. **The
  spec was right the first time and is restored, not corrected.**
- **D21.5's tokenizer hazard returns to its original grounding.** Revision 6 of the ADR re-derived
  it narrowly, on the ground that only *rank fusion* stayed in Go. With membership back in Go the
  hazard is wider again: bleve selects with one notion of a term while Go both ranks and *matches*
  with another. **FR-116 is unchanged in text and stronger in consequence** — and note that
  ruling R-D's case-fold is **not** a fourth notion of a term: it is a character-level transform
  that splits nothing and stems nothing.
- **AC-8.4 reverts too.** Revision 4 required the truth table to run against the compiled SQL path
  *because* the product would not use a Go comparator. It will. See AC-8.4, restated below.

### 8.1a — Rulings R-B, R-C, R-E: the operator vocabulary, the refusal, and enum ordering

**R-B — the filter's operators are SQL's, because SQL's are the ones the model already knows.**
Revision 4 invented `eq` / `lt` / `lte` / `gt` / `gte` / `contains` / `is_absent`
(`pkg/records/filter.go:83-93`). They are replaced by **`=`, `<>`, `<`, `<=`, `>`, `>=`, `LIKE`,
`IN`, `IS NULL`, `IS NOT NULL`** — see FR-022b.

- **The filter remains a STRUCTURED OBJECT. There is no parser and no WHERE-clause string.** Only
  the operator vocabulary changes: `{property: "tags", op: "LIKE", value: "vend%"}`. ADR-068 O-3
  is **amended, not overturned** — its resolution was "structured JSON only, no query language",
  and both halves still hold; what changes is the spelling of the operators inside the JSON.
- **The argument is accuracy, not style.** Our vocabulary has appeared in a model's training data
  **zero times**. SQL's has appeared an enormous number of times. A model choosing an operator
  from a name it has never seen is guessing; a model choosing `LIKE` is recalling.
- **It also settles the list-matching question without inventing a convention.** Revision 4 needed
  R-9 ("whole-element membership") and R-10 ("substring") as two distinct rules with a bespoke
  operator serving both, and needed R-13 to refuse `=` against a `many` property because
  membership-versus-equality had no defined answer. **In SQL's vocabulary the distinction is
  already drawn and already known: `=` is exact, `LIKE` with `%` is partial, `IN` is
  membership.** R-9, R-10 and R-13 are restated in those terms below.

**R-C — an unsupported SQL construct is REFUSED, naming what IS supported.** A model fluent in SQL
will reach for `JOIN`, a subquery, `COALESCE`, `CASE`, `BETWEEN`, `GROUP_CONCAT`. Every one of them
MUST be refused in FR-024's pattern — the refusal names the supported operators and the parameter
that does the job (`join` for a relation, `group_by` for grouping, `aggregate` for a total) — and
**never a parse error and never a silent empty result** (FR-022c).

**R-E — enum ordering is lexical. Custom order is expressed by prefixing the values.**
*(Revision 6: the ruling's own phrasing was "follows SQLite's own ordering", and revision 5
propagated that wording into FR-010, R-5 and §4.1.2 as though SQLite performed the sort. **The
ruling settles WHICH ORDER, not WHO COMPUTES IT** — ruling R-A settles that, and the answer is the
Go comparator. The order is byte-lexical, i.e. what SQLite's `BINARY` collation would have produced,
over the folded sort key.)* An author who wants `lead < qualified < proposal < won` writes `1-lead`,
`2-qualified`, `3-proposal`, `4-won`. See FR-010's revision.

- **This deletes the R-5 ordinal column, its `NULLS LAST` requirement, its schema bookkeeping and
  its rebuild obligation** (the grill's m-7: an enum reorder previously invalidated the derived
  ordinal for every record of the type, and no FR required the rebuild).
- **ADR-068 D4's title — *"Enums are closed and ordered; ordering is data, not spelling"* — is now
  wrong in its second clause and is corrected in ADR revision 7.** Closed is unchanged and is the
  half D4's evidence actually supports. Ordered-by-declaration is withdrawn.
- **The cost is real and is accepted rather than glossed:** §1.4 of ADR-068 cites the
  `1-Pending…7-DoNotContact` prefix hack as a *documented failure* of the incumbents, and this
  ruling adopts it as the mechanism. The trade is that the prefix is **visible, in the operator's
  own file, and does exactly what it appears to do** — where a derived ordinal column was
  invisible, was a second source of truth for the order, and drifted from the schema file on every
  reorder. A convention the operator can see and change beats a mechanism they cannot.

### 8.1b — The executed receipts

Run against the **`sqlite3` CLI 3.51.0**; re-executed identically through **`modernc.org/sqlite
v1.46.1`** (`sqlite_version()` = **3.51.2**). They are retained because they are the evidence for
the ruling in §8.1, not because anything below must be defeated.

**Negation and NULL.** Fixture `(1,a=1,b=2)`, `(2,9,9)`, `(3,NULL,2)`, `(4,1,NULL)`, `(5,NULL,NULL)`.
`NOT (a=1 AND b=2)` → **`2`**. `NOT (a=1 OR b=2)` → **`2`**. The NULL-safe form
`(a IS NULL OR a<>1) OR (b IS NULL OR b<>2)` → **`2,3,4,5`**. `NOT (a>5)` → `1,4`; guarded →
`1,3,4,5`. `typeof(instr(NULL,'x')>0)` → **null**; `NOT (instr(note,'x')>0)` → `2`, guarded →
`2,3,5`. `instr('abc','')` → **1** and `instr('','')` → **1**.

**Affinity.** `INSERT INTO ti(n INTEGER) VALUES ('3'),('3abc')` → `typeof` `integer` and **`text`**;
`'3abc' > 2` → **1**. `STRICT` accepts `'3'` (coercing) and rejects `'3abc'`;
`CHECK(typeof(n) IN ('integer','null'))` rejects both. Literal-vs-column: `3='3'` → 0 / 1;
`3>'2'` → 0 / 1; `'2'>3` → 1 / 0; over `BLOB` affinity both → 0.

**Arithmetic.** `9223372036854775807 + 1` → `9.22337203685478e+18`, `typeof` **real**. `1/0` → NULL.
`SUM` over int64-max plus 1 → **`Runtime error: integer overflow`**. `SUM` over an empty set → NULL;
`total()` over the same → `0.0`, `typeof` **real**. `0.1 + 0.2 = 0.3` → **0**.
`CAST('9223372036854775808' AS INTEGER)` → **`9223372036854775807`**, saturated silently. An INTEGER
column stores `9223372036854775807` and `-9223372036854775808` losslessly.

**Dates as TEXT.** `'2026-08-27T00:00:00+02:00' = '2026-08-26T22:00:00Z'` → **0**, while
`unixepoch()` gives **1787781600** for both; `>` between them → **1**.
`'…09:00:00Z' < '…09:00:00.500Z'` → **0**. `unixepoch('not-a-date')` → NULL;
`unixepoch('2026-8-26')` → **NULL**.

**Case and collation.** `'ACME' = 'acme'` → **0**; `… COLLATE NOCASE` → **1**;
`'ACME' LIKE '%cm%'` → **1**; `instr('ACME','acme')` → **0**; `'abc' GLOB 'A*'` → **0**. Over a
`TEXT COLLATE NOCASE` column holding `ACME`/`acme`/`Acme`: `WHERE name='acme'` → all three,
`COUNT(DISTINCT name)` → **1**, `GROUP BY name` → `ACME|3`. **`LIKE` ignores column collation
entirely** — same answer on a `NOCASE` and a `BINARY` column, while `=` differed. The fourteen-pair
Unicode sweep is in §8.1 above.

**NULL ordering.** `ORDER BY ord ASC` → `NULL, 1, 2, 3`; `ASC NULLS LAST` → `1, 2, 3, NULL`.
`NULLS LAST` is supported in 3.51.x; `DESC` already sorts NULL last, so a `DESC` test alone would
not prove support.

**Join fan-out.** `COUNT(*)` → **2** and `SUM(r.amount)` → **200** where truth is 1 and 100.
Adding a third record also worth 100 and also tagged: `SUM(DISTINCT r.amount)` → **100**,
naive join → **300**, truth **200**.

- **AC-8.4** — **REVERTED, revision 5 (ruling R-A).** The truth table runs against **the comparator
  the product uses**, driven through the real path — schema → filter object → candidate set →
  comparator. *Revision 4 required it to run against compiled SQL, on ADR-068 D16.2b's ground that
  "the properties index answers every typed predicate". It does not, and the comparator is the
  product's decision surface again.* **Two obligations survive the revert unchanged, because they
  were never about SQL:**
  - **No post-filter escape.** A row count taken at the **candidate boundary** MUST equal the
    rendered row count plus the problem rows plus the count the comparator rejected, and the
    comparator's rejections MUST be attributable per record. An implementation that filters twice
    in two places, with the second silently correcting the first, fails this.
  - **Mutation-checked.** Mutating the comparator MUST make the table fail — and **AC-8.4b**
    replaces the deleted AC-8.4a with a mutation per **live rule**. **EXTENDED FROM SIX TO TWELVE,
    revision 6 (review round 6, M-11).** Revision 5 named six mutations for **twelve live rules**,
    and a comparator that got R-1, R-5, R-7, R-8, R-9, R-12 or R-13 wrong passed every one of them —
    the same "insufficient defeats" shape grill pass 1 found in the SQL design, reincarnated as
    insufficient mutations, and asserted as sufficient by SC-024. **One mutation per live rule,
    twelve in total, each naming the cell it MUST kill**, reported as a mutation table rather than
    a pass. *(A-11's exit is that table.)*

    | # | Rule | Mutation | A cell it MUST kill |
    |---|---|---|---|
    | 1 | R-1 | compare across declared types instead of returning `false` | `integer 3` vs `text '3'` → must stop being `false` |
    | 2 | R-2 | remove the absent-side rule | absent `<>` `'v'` → must stop being `false` (DS-4 row E) |
    | 3 | R-3 | make an empty list or empty string count as absence | DS-4 row D's `IS NULL` → must stop being `false` |
    | 4 | R-4 | let a non-conforming value compare instead of being reported | DS-1's `2026-9-1` date row |
    | 5 | R-5 | sort on the raw value instead of the folded key, **or** restore declared-position ordering | `Won` / `lost` ordering (M-8's pair) |
    | 6 | R-7 | accept a lenient date format (`2026-9-1`, `03/04/2026`) | DS-1's two rejected date rows |
    | 7 | R-8 | fold the **identifier** side | `CO-0142` vs `co-0142` → must stop being unequal |
    | 8 | R-9 | match a `many` property whole-list instead of element-wise | DS-4 row A's `= 'vendor'` |
    | 9 | R-10 | substitute `strings.ToLower` or `strings.EqualFold` for `cases.Fold()` | **AC-8.9a** (`straße`/`STRASSE`) and **AC-8.9b** (Greek final sigma) — this mutation is the one that looks most like a tidy-up |
    | 10 | R-10 (`LIKE`) | remove `LIKE`'s wildcard handling, or fold its metacharacters | `'stra_e'` / `'stra__e'` against `straße` (FR-011a's rune-count case) |
    | 11 | R-11 | remove the totality guard so a type pair panics or returns a third outcome | any `date` × `relation` cell |
    | 12 | R-12 / R-13 | make a literal-sourced value compare differently from a record-sourced one, **or** answer an ordering operator over a `many` property instead of refusing | DS-4's `< 'vendor'` refusal |

    Plus the aggregate mutation, which is not a comparator rule and is stated separately because it
    kills a different artifact: **13 — remove the `many` fan-out de-duplication in the aggregate
    pass** (SC-002a, DS-4 row B, and the `COUNT`=2 / `SUM`=200 receipt).
- **AC-8.10** — **NEW, revision 6 (review round 6, C-5). The one control that makes ruling R-A a
  PROPERTY rather than an intention.** Revision 5's headline is that *"a single surviving SQL-side
  comparison reopens every violation"* — and it is right — and revision 5 then deleted **both** of
  the only artifacts that could ever detect one (§7 test 39, `TestFilter_NoLikeInCompiledPath`,
  which inspected the emitted filter path; and AC-8.8, a `BINARY`-collation assertion over emitted
  DDL), on the correct-sounding ground that SQL-side comparison was their only subject. **It was
  their subject because it is the property the design now depends on entirely.** Review round 6
  found **seven** surviving SQL-side evaluations in the revision whose headline is this ruling, and
  nothing in the document would have caught an eighth.

  **The assertion:** a **query-boundary recorder** captures every SQL statement the properties index
  executes for a corpus exercising all ten operators, `group_by`, `aggregate`, `sort` and `join`. It
  fails if **any captured statement contains a comparison operator, `LIKE`, `IN`, `GROUP BY`,
  `ORDER BY`, an aggregate function, or `COLLATE`** — outside a **named allow-list of narrowing
  predicates**:

  | Allowed in emitted SQL | Why it is narrowing and not deciding |
  |---|---|
  | `type = ?` | selects the candidate population by record type; no property value is compared |
  | `path LIKE ? ` with a caller-independent, escaped prefix | workspace/collection scope (FR-060); the pattern is built by us from a resolved root, never from caller text |
  | `kind = ?` | note-kind narrowing, same argument as `type` |
  | the relation child table's `rec_id` join predicate | assembles a record's `many` values into one row set; the fan-out is de-duplicated in Go (SC-002a) |
  | `LIMIT` / `OFFSET` on the **narrowing** select | paging of candidates, not of results |

  **Adding a statement to that allow-list is a specification change requiring the argument AC-8.2
  demands** — not a test edit. The recorder is at the **store boundary**, not inside a compiler, so
  it survives the deletion of the compiler and cannot be satisfied by a comparator that is simply
  bypassed. §7 test 39a implements it, and it is named in W1's exit criteria so that it exists
  before there is anything to catch. **Treat §7 test 42's CI discipline as the template**: name the
  job, the tag combination and the make target, so the check cannot be quietly not-run.
- **AC-8.5** — the table is run twice: once against a freshly built properties index and once after
  a delete-and-rebuild (FR-020a). Identical results both times. **Under the ruling this now tests
  what it always claimed to:** the candidate set is index-derived, so a rebuild that changes
  candidates changes results even though the comparator is untouched.
- **AC-8.6** — **membership is invariant under ranking.** **REWRITTEN, revision 5: as revision 4
  worded it, this could not fail.** Membership is the comparator's and ranking is a separate pass,
  so set-equality is guaranteed by the architecture and asserting it tests nothing. The assertion
  that *can* fail: **a corpus is constructed in which the two rankings return different orders** (a
  term-saturation or length-normalisation case, so BM25 and TF-IDF demonstrably differ), the order
  difference is asserted to be **non-empty**, and only then is set-equality asserted over it. **A
  run in which the two orders are identical FAILS**, because it proves the fixture did not exercise
  the ranking change.
- ~~**AC-8.7**~~ — **DELETED** (ruling R-D): it asserted zero `LIKE` in the compiled filter path, to
  protect a case-sensitivity no longer wanted. §7 test 39 is deleted with it.
- ~~**AC-8.4a**, **AC-8.8**~~ — **DELETED** (ruling R-A): a mutation per SQL defeat, and a
  `BINARY`-collation assertion over emitted DDL. Both had SQL-side comparison as their only subject.


## 9. Holdout evaluation scenarios

**Not for use during development** — with the two cross-references named below, which are
deliberate. *(Revision 6, m-14: revision 5's blanket "Not referenced in §6 or §7" was false.
Scenario 14 is promoted into §7 as test 50, scenario 11 is referenced by FR-113 and SC-018, and
scenario 15's pairs are the source of AC-8.9. All three cross-references are correct and useful;
the blanket statement was the defect. The rule that survives is the one that matters: **a holdout
scenario is never a substitute for the fixture test that shares its subject**, and where one is
promoted the holdout stays in place alongside it.)*

1. Point the system at a real vault of at least 500 notes with no schema; confirm nothing
   breaks and no errors are raised.
2. Declare a schema matching an existing note type; confirm the count of conforming records
   matches a hand count.
3. Ask for all records related to one record, and verify the answer against a manual search.
4. Corrupt one record's typed property; confirm the next query names that record.
5. Rename a heavily-referenced record; confirm every relation still resolves.
6. Run two agents writing different properties of the same record concurrently; confirm both
   land and neither file is damaged.
7. Import a real `.base` file containing an unsupported expression; confirm the translated
   view is correct and the untranslated clause is reported verbatim.
8. Ask a question no incumbent can express — *"notes mentioning pricing within 2 hops of
   `[[Acme]]`"* — and verify the answer against a manual traversal.
9. Delete the properties index on a real vault mid-session; confirm the next query rebuilds it
   and returns the same answer as before the deletion.
10. Open the collection panel on a vault whose index completed before the browser connected;
    confirm it shows the completed state rather than "no progress".
11. Run the same corpus through plain BM25 and through the FR-112 fusion; have a human judge the
    top 10 for 20 real queries. **This is a SANITY CHECK, not the ship gate, and revision 5 marks it
    as such because revision 4 left the two readable as the same thing.** FR-113's gate is the
    committed 30-query set measured as **nDCG@10** against a stated threshold; **this holdout MUST
    NOT be substituted for it**, and §9 is explicitly not for use during development.
12. On a real vault that already uses an undeclared convention such as `type: meeting`, declare a
    schema for that type and confirm the response names the count of notes just converted and
    every one that newly fails validation — before the operator discovers it from a validation
    report they did not ask for (AC-C1).
13. Build for `linux/mipsle` and confirm the binary builds, the gateway boots, `vault_read` and
    plain-word `vault_find` work, and every typed filter refuses **naming the platform** rather
    than returning an empty result (FR-020h).
14. Take the twenty most natural questions a person would ask this vault, phrase each as a
    **negative** — "which X have I not done", "which records have no owner" — and confirm every
    one returns the absent rows. This is the R-2 failure in the form a user would meet it, and it
    is the case SQLite's defaults get backwards (§8.1). **A FIXTURE VERSION IS PROMOTED INTO §7 as
    test 50, revision 5** — this is the strongest verification instrument in the document and it was
    sitting in the section marked "not for use during development", where it could not catch
    anything before release. The holdout stays as well; a twenty-question human pass over a real
    vault is not the same evidence as a fixture.
15. **NEW, revision 5; CORRECTED, revision 6 — revision 5's wording asserted an outcome that is
    WRONG for one of the four languages it named.** On a vault whose values are not English,
    confirm each of the following **specific** outcomes, because "confirm case matches across
    non-ASCII letters" is not a decidable instruction and three of revision 5's four languages
    behave differently from each other:
    - **German** — `straße` and `STRASSE` MUST match. This is **full** case folding (`ß`→`ss`) and it
      is the pair that fails under `strings.ToLower` and under `strings.EqualFold` alike.
    - **Polish** — `łódź` and `ŁÓDŹ` MUST match. The control: every candidate mechanism gets this
      right, so a Polish-only corpus proves nothing.
    - **Greek** — `σίσυφος` and `ΣΊΣΥΦΟΣ` MUST match. Final sigma; the pair on which the two Go
      stdlib functions **disagree with each other**.
    - **Turkish** — `istanbul` and `İSTANBUL` MUST **NOT** match, and a tester who reports this as a
      bug should be sent to FR-011a. They are different letters. **This is the row revision 5 got
      backwards**: it promised that all four languages would match across case, which would have
      made the Turkish-I bug a passing result.
    **This scenario fails silently under any SQLite-side case fold and under both Go stdlib folds**,
    and no English-language corpus can detect it (FR-011a, AC-8.9, §8.1's Unicode receipt).
16. **NEW, revision 5.** Ask for a total over a vault holding more records than the 10,000-record
    candidate cap, and confirm the aggregate-only path answers it rather than refusing (FR-064a).
    **This is ADR-068 §1.2's own motivating question at a scale the spec claims to support**, and
    under revision 4's bounds it was unanswerable by construction.

---

## 10. Ambiguity warnings — unresolved

| # | Ambiguous | Likely agent assumption | Needs |
|---|---|---|---|
| ~~A-1~~ | **RESOLVED — one rule.** ADR-068 O-5: an unresolvable target is reported as missing; the cause is not guessed at. |
| ~~A-2~~ | **RESOLVED — vault-wide.** ADR-068 O-6. FR-024's valid-names list is therefore vault-scoped, which is the same boundary the records themselves sit behind. |
| ~~A-3~~ | **RESOLVED — not ours to decide.** ADR-068 D0: the product ships mechanism, the vault ships convention. **Revision 3: `record_log` is deleted, not relocated** (ADR-068 D15.4). Interaction history is derived from mentions (FR-050..053), so a dedicated logging tool served only the residual case of an interaction with no note behind it — and that case is served by creating a note, which `vault_edit` already does. |
| ~~A-4~~ | **RESOLVED — deferred.** ADR-068 D11 descoped; no FR needed. The edge-case table's sub-record rows are removed. |
| ~~A-5~~ | **RESOLVED — deferred.** ADR-068 D12 descoped; likewise. |

| ~~A-6~~ | **RESOLVED 2026-08-28 — the spike reported and D16 is decided.** Two indexes: bleve keeps text, a derived SQLite properties index holds typed properties and relations. FR-020..FR-020h are specified above and W1 is unblocked. ADR-068 **overrides its own spike's recommendation** (its D16.3): the spike said bleve passes, and on the question D16 gated on — memory — it does. **CORRECTED, revision 4: the override is taken on CAPABILITY ALONE.** Revision 3 wrote "on latency and capability"; ADR-068 revision 6 **withdraws the latency argument outright** as unevidenced — revision 5 had quoted the spike's *"a dedicated aggregation store would be substantially quicker"* as evidence, and that sentence sits inside the spike's own section headed *"What this does not say"*. Nobody has benchmarked SQLite here. The capability argument — joins, `OR`, `GROUP BY`, aggregates, without our writing a general expression evaluator — carries the decision alone, and it is sufficient. **A reader must not quote latency from D16.3.** |
| **A-7** | **LIVE. The two-index write path is unmeasured.** One note, two index updates: ordering, crash consistency and what a torn write leaves behind are all undefined. FR-020a says a rebuild fixes any divergence, which is true and is not a substitute for knowing how often divergence happens. **This ADR's own history is three revisions of assuming exactly this kind of thing.** W1 must measure it — write throughput, concurrent queries, property counts above 10 per record, and at least one non-macOS platform (the spike measured none of these; its §6.1). |
| ~~A-8~~ | **RESOLVED 2026-08-28, in this spec's favour — the unit is BYTES.** Revision 3 raised it: budgets of ~50–80/hit, 1,000 default and 4,000 cap are meaningless without naming the counter, and the likely assumption is the serving model's tokenizer, which varies per provider. **ADR-068 revision 6's D22.7 concedes the point in the same words** and changes the unit: *"a token cap needs a tokenizer to enforce it, and D21.5 is a decision about three tokenizers that disagree — none of which is the model's."* FR-127 is now ~200–320 bytes/hit, ~4,000 default, 16,000 cap — the same intent at a conservative ~4 bytes/token. FR-127b names the unit and binds the tests to it. **One carve-out survives:** FR-079/FR-128's ~150-token description budget stays in tokens, because it is authoring guidance for a human, never a runtime check. |
| ~~A-9~~ | **RESOLVED 2026-08-28, in this spec's favour.** Revision 3 read ADR-068 D20's two listings of `trash` as: the **primitive and its convention** land in W4, the **`vault_restructure` operation** exposing it lands in W5, and said that if the ADR meant otherwise the spec should be corrected rather than reconciled. **ADR-068 revision 6's D20 adopts exactly that split in its own words** — *"the convention is W4 design work, the operation is W5"* — and names the failure it avoids: shipping part of a tier-5 tool before W5 defines the tool or seeds its policy. FR-048 is unchanged and is now the ADR's reading too. |
| **A-10** | **PARTLY RESOLVED, revision 4, and narrowed.** The half that was a real defect is fixed: `create_record_type` no longer sits inside `vault_edit` at all — it is `vault_configure` (FR-016), so granting `vault_edit` no longer silently grants the type system. **The residual is genuine and stays LIVE:** granting `vault_edit` still grants `replace_body` as well as `set_property`, and an operator reading the tool name will not infer that. FR-070c and FR-079 now require every tool description to name its **widest** operation rather than its most common one — `replace_body` for `vault_edit`, `delete_record_type` for `vault_configure`, `trash` for `vault_restructure`. **Needs:** a review that the six shipped descriptions actually do this, at W5, not an assertion here that they will. |
| ~~A-11~~ | **RESOLVED 2026-08-28 by operator ruling R-A, and resolved by DELETING THE CLASS rather than mitigating it.** *Revision 4 raised it: the nine SQLite-semantics defeats are specified and none is verified; every one is a line of a query compiler nobody has written; eight of nine fail in the quiet direction, so the absence of a defeat produces a passing test suite and a wrong product. Grill pass 1 then found that **zero of the seven specified defeats were sufficient as written**, plus a tenth violation (join fan-out) that made every count and total over a filtered multi-value list wrong. That is two rounds of a specified defeat differing from a verified one, which is exactly what A-11 predicted.* **The ruling removes the compiler: SQLite narrows candidates, our own tested comparator decides (§8.1).** There is no line to forget because there is no query compiler. **What survives as an obligation, and it is smaller and different:** AC-8.4b's **six** comparator mutations, reported as a mutation table at W2 rather than a pass. **What does NOT survive:** the eleven defeats, AC-8.4a, AC-8.7, AC-8.8 and FR-023a/FR-028a's SQL mechanisms — all withdrawn, listed in §8.1's deletion table so the shrinkage is auditable.
| **A-13** | **LIVE, new in revision 5. FR-020c's freshness mechanism is designed, not verified — and it is the FOURTH mechanism this FR has had.** The three before it were each asserted rather than checked: a shared freshness token that did not exist; `manifestVersion`, which is a struct-schema constant; and a *"live `Manifest`"* query-path lookup, where `Index` holds no manifest field, `LoadManifest` has two call sites both inside `SyncWith`/`CheckDrift`, and `Manifest` has no mutex. **Revision 5 puts the hash on the bleve document as a stored field instead**, which is real work on a struct FR-111 already reopens — but whether bleve returns a stored field cheaply on this hit path, and whether `Store: true` on a 64-byte field at 100,000 documents is acceptable against FR-020e's rebuild, are **open**. **Needs:** W2's fielded-indexing work to answer both, and test 62's `-race` run to prove the concurrency question is gone rather than moved. **Carried as a stated open risk deliberately: a known gap is worth more than a fifth assumed mechanism.** **TWO ADDITIONS, revision 6 (review round 6, M-28 and C-2).** *(a)* **A wave-ordering defect, stated rather than papered over:** §11 puts `FR-020..FR-020c1` in **W1** and *"whether the stored-field freshness mechanism holds"* in **W2**, so **W1 ships a freshness comparison whose two open questions are answered a wave later** — and if the answer is no, W1's completeness verdict was built on a mechanism being replaced. ADR-068's W2 row concedes the same thing (*"or it does not and is replaced before W3"*). **RESOLVED by stating W1's FALLBACK explicitly rather than by moving the wave:** until the stored-field mechanism is verified in W2, **W1 reports every record `complete: false`, with staleness-unknown named as the reason**. That is honest and non-silent, it is what a reader would want if the mechanism turns out not to hold, and it is visibly uncomfortable — which is the correct pressure on W2. *(b)* **The fourth mechanism was NOT the only one in the document.** Four normative places still specified the **third**: §1's reuse table (**verbatim from the commit grill pass 1 reviewed**), the constraint table, **AC-F5** — a mandatory acceptance criterion — and §4.2's worked annotation. An implementer obeying AC-F5 built a data race on a mutex-less `Manifest`, which §7 test 62 was added **this revision** to hunt. All four are corrected in revision 6 (C-2). **A-13's own lesson was live in the document that states it, and this is the record of that.** |
| **A-14** | **LIVE, new in revision 5 (operator ruling R-A); RE-DERIVED AND WEAKENED, revision 6 (review round 6, A-14 / C-3 / C-4 / M-30). Nobody has measured the GO evaluation path over the two-index design, and revision 5's reasoning about its BOUND was wrong in three places.** Filtering, grouping, aggregation **and sorting** move back to Go (FR-021), so cost scales with **candidates** rather than with results, and the Unicode case fold (FR-011a) adds a `cases.Fold()` per operand per comparison. **What revision 5 got wrong, corrected here rather than left implicit:** *(i)* it said the cap was *"counted before any row is materialised"* over a compiled `WHERE` clause **that ruling R-A deletes**, so the bound it leaned on was unimplementable — FR-064 now carries **two** bounds and only **B1 (50,000 narrowed candidates)** is genuinely pre-retrieval; *(ii)* it recorded FR-064a's aggregate exemption as free on the strength of a **SQLite pushdown that does not exist**, so the true worst case is **50,000 candidate evaluations, not 10,000 — five times the number A-14 asked W1 to measure**; *(iii)* **nothing bounded comparisons per candidate** — the filter tree was the one unbounded input, and it multiplies against the candidate bound. **The bound is now a real refusal rather than a hope, and it is a WEAKER claim than revision 5 made:** worst case is bounded at **50,000 candidates × FR-023c's 64-leaf cap = 3.2M comparisons**, with FR-011c permitting one fold per candidate rather than per leaf. **Needs:** W1 to measure the Go path at **both** bounds — B2's 10,000 and B1/FR-064a's 50,000 — at FR-023c's leaf cap, with peak RSS (FR-066b), on Linux as well as macOS. **AND — carried as a STATED OPEN RISK rather than closed:** the Go-side **grouping map** over a free-text group key is bounded only by the candidate count (FR-066b), and no measurement exists for it; if W1 shows 3.2M comparisons or a 50,000-entry grouping map is not affordable, **the caps come down and FR-064a's exemption is re-argued** — it does not get absorbed. *This is the cost the ruling accepts, stated up front rather than discovered.* |
| **A-12** | **LIVE, new in revision 4. The properties-index write path is now carrying a second obligation nobody has measured.** FR-020c adds a `source_hash` column written in the same transaction as every record row, and FR-076a adds a checkbox row per task line. A-7 already flags the two-index write path as unmeasured; these widen what is unmeasured rather than narrowing it. **Needs:** W1 and W2 to measure the write path **with** `source_hash` and task rows present, not a bare record write. |

**A-4 and A-5 were specification defects and are now resolved by descoping the decisions that
caused them** (ADR-068 D11, D12). **A-6, revision 2's live blocker, is resolved — W1 is
unblocked**, with its rationale corrected in revision 4 to capability alone.

**Revision 5's closing count.** A-8 and A-9 closed in revision 4, both the way this spec argued.
**A-11 closes here, and it closes by deletion rather than by verification** — the operator ruling
that moves comparison out of SQLite removes the class of failure A-11 named, and it does so after
grill pass 1 established that zero of the seven specified defeats were sufficient and that a tenth
violation existed. A-10 stays halved. **Five items are live — A-7, A-10, A-12, A-13, A-14 — and
none of them blocks W1**: A-7, A-12 and A-14 are measurements W1 and W2 perform, A-10's residual is
a description review at W5, and A-13 is answered by the same W2 work that reopens `indexDoc`.

**The one thing a reader should carry away from this table is now A-13, and it replaces A-11's
lesson with a sharper one.** A-11 said a specified defeat and a verified defeat are different
things. **A-13 says something worse: FR-020c has now had four mechanisms, and the first three were
each described in normative text as though they already worked.** The fourth is designed rather
than described, its unbuilt parts are named, and its open questions are written down here instead
of being resolved by confidence. **That is the difference this document is trying to hold onto, and
it is easier to lose on a revision that is mostly deleting things than on one that is adding
them.**

---

## 10a. Code and contracts made dead by revision 5 — marked for deletion, not deleted here

**This specification does not delete code.** The surfaces below become dead when W1 lands, and they
are enumerated so the deletion is a scheduled task with a reviewer rather than something a future
reader trips over. **Verified against the tree at revision time** — every path, line count and
symbol below was read, not recalled. **Revision 6 re-executed the whole section against the tree and
found four defects in it; they are marked in place.**

> **READ THIS FIRST, because it changes how the whole section is executed (review round 6, M-23 /
> M-46).** **`pkg/records` has ZERO production importers.** `grep -rln '"…/pkg/records"' --include='*.go'`
> returns exactly two files, and both are its own external tests
> (`pkg/records/external_enum_ordering_test.go`, `pkg/records/external_property_test.go`).
> **Three consequences, and each is load-bearing:**
> 1. **Every deletion below is near-zero-risk** — and, crucially, **the compiler will not find the
>    callers for you**, because there are none. Whoever executes this list cannot lean on a build
>    break to tell them they missed something; the list is the only instrument.
> 2. **It strengthens ruling R-A.** §8.1's *"the comparator that decides is the one that already
>    exists, is already tested"* is **verified true** — `pkg/records/compare_oracle.go` evaluates
>    comparisons in Go today and `pkg/records` emits no SQL at all. R-A **restores what exists**
>    rather than commissioning something new, and saying so with this evidence makes the argument
>    stronger than it currently reads.
> 3. **And it is a real caveat, stated rather than swallowed:** the comparator R-A relies on has
>    **never run in production**. "Already tested" is true; "already proven in service" is not, and
>    the document should not be read as claiming the second.

**Operator ruling 1 — `money` is deleted from the type system (FR-014).** `pkg/records` holds a
real, tested money implementation; it is not a stub.

| Surface | Size | Disposition |
|---|---|---|
| `pkg/records/money.go` | 281 lines | **DELETE.** `SumMoney` (`:161`), `CurrenciesPresent` (`:195`), the ISO-4217 handling and the `maxMoneyScale` check at `:109-110` |
| `pkg/records/money_test.go` | 767 lines | **DELETE** |
| `pkg/records/money_refusal_test.go` | 394 lines | **DELETE** |
| `pkg/records/money_parse_paths_test.go` | 115 lines | **DELETE** |
| `const maxMoneyScale = 12` | `pkg/records/decimal.go:166` | **DELETE.** *(It is defined in `decimal.go`, not `money.go` — worth stating, because deleting `money.go` alone leaves it behind, and inheriting **12** as `decimal`'s bound is exactly what FR-013 forbids.)* |
| `TypeMoney` | `pkg/records/schema.go:78`, referenced at `schema.go:77,88,257`; `value.go:67,150,390,456,498`; `compare_oracle.go:309,371,432,467` | **DELETE**, with `TypeNumber` (`schema.go:76`) **REPLACED** by `TypeInteger` and `TypeDecimal` (FR-004) |
| `contracts/components/schemas/RecordMoney.yaml` | — | **DELETE**, with its references at `contracts/openapi.yaml:279-280`, `RecordValue.yaml:75-76` (`$ref` and `x-go-type`), the `money` enum member in `RecordValue.yaml:32` and `RecordPropertyValue.yaml:44`, `RecordAggregateResult.yaml:64`'s currencies field, `RecordFilter.yaml:71-72,111`'s money clauses, and `PropertyDef.yaml:60,118,120` |
| Generated | `pkg/api/generated/openapi_types.gen.go:3558,3572,3849,3863` | **REGENERATED**, never hand-edited — `scripts/gen-contracts.sh`, committed in the same atomic commit as the spec change (Hard Constraint #8, step 4) |
| **`contracts/components/schemas/RecordProblem.yaml`** | `:38-39` (enum members), `:67-69` (prose) | **DELETE the enum members `cross_currency` and `money_scale_mismatch`, and their prose.** **NEW, revision 6 (review round 6, C-9), and these are the HIGHEST-VALUE deletions in the whole section.** They are **not comments**: each generates a Go constant, a TypeScript union member and a **runtime Zod validator the SPA edge uses to accept or drop payloads**. They are live machine-readable residue of the requirement FR-014 retired |
| `contracts/components/schemas/PropertyDef.yaml` | `:4` (type list prose), `:45` (enum member `money`), `:60` (its description), `:117-120` (`money_scale`) | **DELETE** the `money` enum member, its description and the whole `money_scale` property. **The `:4` prose line must be rewritten, not deleted** — it enumerates the seven types and is where `number` → `integer` + `decimal` also lands (M-22) |
| `contracts/components/schemas/PropertyDef.yaml`, continued — **THREE ROWS ADDED IN DRAFT 7 (UAT C-8), because this file is a **validator**, not documentation, and it is stale by two revisions in the exact place UAT case C-8 tests** | the `type` enum still lists **`number`** and **`money`**; the `text` description still names the **deleted** operator vocabulary (*"`contains` (case-sensitive substring matching)"*, *"`is_absent`"*, *"there is no `is_present` operator"*) — every clause of which ruling **R-B** and **R-D** reverse; the `values` description asserts *"The closed, **ordered** value set … **Order here IS the sort order** (FR-010)"*, which **R-5**/**FR-010** reverse to lexical-on-the-folded-key | **REWRITE all three.** The `type` enum becomes the seven of FR-004. The `text` description states `=`, `<>`, `<`, `<=`, `>`, `>=`, `LIKE`, `IN`, `IS NULL`, `IS NOT NULL`, case-**insensitive**, `LIKE` anchored. The `values` description states that order is **lexical on the folded key** and that a domain order is a value **prefix**. *A contract that still describes `contains` is a contract that will be regenerated into TS types and a Zod validator asserting a vocabulary the product refuses* |
| `contracts/components/schemas/PropertyDef.yaml`, `unit:` | the description reads *"Unit of measure for a **\"number\"** property"* | **REWRITE, do NOT delete.** `unit` **survives** — FR-005b reverses ADR-068 revision 8's *"`unit` is NOT a schema key"* with evidence, one item of which is **this very declaration**. The description becomes: valid on `integer` and `decimal`; **opaque text the product never interprets**; declared **per property, never per value**. **ADR-068 revision 8's claim that no wire schema defines a `unit` key is corrected in ADR revision 9 against this line** |
| `contracts/components/schemas/PropertyDef.yaml`, `person` | *"a relation to a **person record**"* — the same D0-violating phrasing ADR-068 D3 carried | **REWRITE** to *"a relation to whatever record type the vault uses for people; `to:` is **optional**"* (**FR-004b**). **This file's `to:` description says *"Present only when type is `relation` or `person`"*, which is already correct and must not be tightened into a requirement** |
| `contracts/components/schemas/RecordValue.yaml` | `:7`, `:31` (`number`), `:32` (`money`), `:37`, `:68-69`, `:74-76` | **EDIT.** Delete the `money` enum member, the `money` property with its `$ref`/`x-go-type`, and the money clause of `:68-69`. Replace the `number` enum member with `integer` and `decimal` (M-22). **The decimal-string rule at `:7` and `:69` SURVIVES and must be re-pointed at `decimal`** — it is the FR-020b guarantee and deleting it with money would delete the valuable half |
| `contracts/components/schemas/RecordPropertyValue.yaml` | `:43` (`number`), `:44` (`money`), `:49` (example) | **EDIT**, same two changes |
| `contracts/components/schemas/RecordFilter.yaml` | `:71-73`, `:111` | **EDIT.** `:71-72` also carries **two other stale statements** that outlived their requirements and are not money: *"on an enum it compares DECLARED POSITION, not spelling (FR-010)"* — **reversed by ruling R-E** — and the `R-6` citation, a **retired rule**. Three corrections in four lines |
| `contracts/components/schemas/RecordAggregateResult.yaml` | `:34` (prose), **`:61-70`** (`currencies_present`, the whole property) | **DELETE** the property; **EDIT** `:34`. *(Revision 6, review round 6, M-21: revision 5 cited `:64` for this field. **`:64` is a line inside its description; the field is declared at `:61`.** A deletion list is executed literally, and §10a's own opening sentence claims every line was read. It also missed the file's **second** money reference at `:34`.)* |
| `contracts/components/schemas/RecordSort.yaml` | `:10` | **EDIT** — deletes a money sentence, and the surrounding sort prose must be checked against R-5's Go-side lexical ordering while it is open |
| `contracts/components/schemas/RecordGroup.yaml` | `:64` | **EDIT** |
| **`src/lib/api/generated/schemas.ts`** | **18 hits**, incl. `:319-320` and `:3875-3876` | **REGENERATED.** **NEW, revision 6 (C-9) — §10a listed no SPA surface at all.** This file carries the **runtime Zod** union with both `RecordProblem` members, a `RecordMoney` object schema and a `currency` regex. It is a **validator**, not a type: leaving it means the SPA keeps accepting payloads describing a concept that no longer exists |
| **`src/lib/api/generated/openapi-types.ts`** | **35 hits** | **REGENERATED.** Same commit |
| **`pkg/api/generated/openapi_types.gen.go`** | **40 hits**, not four | **REGENERATED.** *(Revision 6: revision 5's row named four lines. The file carries forty.)* |

**FALSE POSITIVES, named so nobody deletes them.** A case-insensitive grep for `currency` matches
**con·currency**: `contracts/components/schemas/PerformanceSettings.yaml:3,5`,
`PerformanceSettingsUpdate.yaml:13` and `contracts/openapi.yaml:2906,2929,5944,7992` are **agent
concurrency settings and have nothing to do with money**. `TokenBudgetStatus.yaml:6` ("no money
caps") is prose about token budgets. **None of the seven is in scope.** They are listed because the
next person to run this grep will find them, and an unexplained hit in a deletion list gets deleted.

**M-22 — the `number` → `integer` + `decimal` contract change is scheduled HERE, revision 6, because
revision 5 scheduled it nowhere.** `contracts/` today declares a `number` property-type enum member
(`RecordValue.yaml:31`, `RecordPropertyValue.yaml:43`) and **no `integer` and no `decimal` property
type anywhere**. FR-004's change touches the same three schema files as the money deletion and
carries the identical Hard-Constraint-#8 obligation, so it belongs in the same atomic commit or it
will be discovered by `make verify-contracts` failing on somebody else's branch.

**W1 exit criterion for every contract row above (Hard Constraint #8):** the spec change and the
regenerated `pkg/api/generated/` **and** `src/lib/api/generated/` trees land in **one atomic
commit**, and **`make verify-contracts` exits 0**. Regeneration is `scripts/gen-contracts.sh`;
generated files are never hand-edited.

**`pkg/records/decimal.go` (588 lines) SURVIVES INTACT and is the valuable core.** It is
`math/big`-based (`decimal.go:10`, `Decimal` = `unscaled *big.Int` + `scale int32` at `:36-38`),
`maxDecimalScale = 100` (`:48`) becomes `decimal`'s declared bound under FR-013, and
`float64`/`float32` appear on exactly two lines of the file, **both comments** (`:19`, `:462`) —
zero binary float in executable code, guarded by `decimal_no_float_test.go`. Its test suite
(`decimal_test.go`, `decimal_cmp_test.go`, `decimal_scale_gap_test.go`,
`decimal_string_bounds_test.go`, `decimal_no_float_test.go`) survives with it.

**CORRECTED, revision 6 (review round 6, M-20) — revision 5 said *"three of those files reference
`maxMoneyScale`"* and that is wrong; **one** of the five does. Following revision 5's instruction
left a reference to a deleted constant in a file nobody was told about.** Re-executed
(`grep -rn maxMoneyScale pkg/records/`), **every** surviving reference, by path:

| File | Lines | Action |
|---|---|---|
| `pkg/records/decimal.go` | `:108`, `:161`, **`:166` (the `const` itself)** | **DELETE the const; edit the two comments.** Already scheduled above |
| `pkg/records/decimal_string_bounds_test.go` | `:431` | **EDIT** — the only one of the five surviving decimal test files that references it |
| **`pkg/records/schema_declared_keys_test.go`** | `:24` | **EDIT. NEW, revision 6 — this file was in NEITHER the delete list nor the edit list.** Its comment describes a defect where a wire `maximum: 12` matching `maxMoneyScale` *"made it look verified while nothing read it"* |
| **`pkg/records/schema.go`** | `:678` | **EDIT. NEW, revision 6** — non-test production file, missed entirely |
| **`pkg/records/value.go`** | `:324`, `:467`, `:471-472` | **EDIT/DELETE. NEW, revision 6** — `:471` is an executable bound check, not a comment. It goes with `parseMoneyValue` (`:335`), `parseMoneyScalar` (`:350`), `parseMoneyMapping` (`:395`), `unknownMoneyKeys` (`:518`), `renderMoneyMapping` (`:564`) and `moneyValueError` (`:572`), **none of which revision 5 listed** |
| `pkg/records/money.go`, `money_test.go` | many | already **DELETE** above |
| `pkg/records/compare_oracle.go` | `crossCurrencyProblem` (`:365`) | **DELETE. NEW, revision 6** — a money symbol outside `money.go`, in the file the ruling makes load-bearing |

**Operator ruling R-B — the invented operator vocabulary is replaced (FR-022b).**
`pkg/records/filter.go:83-93` declares `OpEqual`/`OpLess`/`OpLessOrEqual`/`OpGreater`/
`OpGreaterOrEqual`/`OpContains`/`OpIsAbsent`. All seven are **REPLACED** by the ten SQL operators,
and `filter_test.go`, `filter_r13_validate_test.go` and `compare_truthtable_test.go` move with them.
**`compare_truthtable_test.go` (1,236 lines) is regenerated rather than edited** — it is generated
from the rules (AC-8.2), and the rules changed.

**Operator ruling R-E — the enum ordinal is deleted (FR-010).** **ENUMERATED, revision 6 (review
round 6, M-19): revision 5's row read *"**Whatever** holds declared position in …"* and was the only
disposition in §10a with no symbols, in a section whose whole purpose is that the deletion be a
scheduled task rather than a discovery. The symbols exist and are findable:**

| Symbol | Location | Disposition |
|---|---|---|
| `EnumValue.Position` (field) | `pkg/records/schema.go:138`, documented `:120-137` | **DELETE the field.** Its doc comment is 18 lines explaining why nothing may read it — all of it dead |
| `(*Property).EnumPosition` | `pkg/records/schema.go:220`, documented `:198-219` | **DELETE** |
| `(Comparator).enumOrdering` | `pkg/records/compare_oracle.go:509`, documented `:486-508` | **DELETE.** This is the enum-ordering comparison branch; under R-E an enum orders lexically like any text, so R-5's ordering path is the ordinary one |
| `SortByEnumOrder` | `pkg/records/filter.go:454` | **DELETE** |
| `enumValueNotDeclaredProblem` | `pkg/records/compare_oracle.go` (called `:514`) | **KEEP** — a value that resolves to no declared member is still FR-011 non-conformance under R-E. **Named here so it is not deleted along with its caller** |
| `pkg/records/enum_position_authority_test.go` | 230 lines | **DELETE** |
| `pkg/records/external_enum_ordering_test.go` | 373 lines | **DELETE** |
| `pkg/records/schema.go:29` | comment *"FR-010 an enum declares its values IN ORDER; sorting follows position"* | **EDIT** — it states the reversed requirement in the file's header |

**These two test files are the ones most likely to be "fixed" into passing rather than deleted**,
so the deletion is named here, and **AC-8.4b mutation 5 is the guard**: a comparator that restores
declared-position ordering must fail a named cell.

**Nothing above is deleted by this document.** Each is a W1 or W2 task with the FR that killed it
cited in the commit.

## 11. Wave plan

**NEW, revision 5.** *The specification referenced W0–W6 eleven times and contained no wave plan,
so the wave assignment of any individual FR was unreviewable and the document was not implementable
without ADR-068 open alongside it (the grill's M-29).* Waves are ADR-068 D20's; this table maps
this specification's requirements onto them.

| Wave | This spec's requirements | Exit criterion additions from revision 5 |
|---|---|---|
| **W0** | FR-020e; **the R-F vocabulary replacement (revision 6)** | *(unchanged — the F-0 index rebuild, independent of everything else.)* **ADDED, revision 6 (review round 6, M-26 / O-2): the R-F vocabulary replacement is W0's second deliverable, with its own review.** Exit criterion: `grep -oiwE 'company\|companies\|deal\|deals\|meeting\|meetings\|status\|stage\|arr\|open\|won\|lost\|prospect\|Acme\|Northwind\|CO-0142\|DEAL-0117'` over both documents returns **zero** outside one explicitly-marked illustrative appendix. It is W0 because it touches no code, blocks nothing, and gets harder every revision it is deferred |
| **W1** | FR-001..FR-011c, FR-012..FR-013, FR-015, FR-015a, FR-020, FR-020a..FR-020c1, FR-020h..FR-020j, FR-021, FR-021a..FR-021d, FR-036..FR-039 (incl. **FR-036b**, **FR-038a**), FR-060..FR-062a, FR-075, FR-075a | **A-14's measurement**: the Go evaluation path at the 10,000-record cap and at FR-064a's 50,000, peak RSS, Linux **and** macOS. FR-020i's version assertion. **The `go-test-nosqlite` CI job exists and is a required check** **REVISION 6 ADDITIONS.** **(a) `TestQuery_NoComparisonIsDelegatedToSQL` (test 39a / AC-8.10) MUST EXIST AND PASS BEFORE ANYTHING ELSE IN W1 IS ACCEPTED** — it is the control that makes ruling R-A a property rather than an intention (C-5), and it must exist before there is anything for it to catch. **(b) A-14's measurement is taken at BOTH bounds** — B2's 10,000 and B1/FR-064a's 50,000 — **at FR-023c's 64-leaf cap**, with peak RSS. **(c) W1's freshness FALLBACK is stated and shipped**: until W2 verifies the stored-field mechanism, every record reports `complete: false` with staleness-unknown as the reason (M-28). **(d) The `cases.Fold()` dependency is promoted to DIRECT in `go.mod`** in the same commit as the comparator, with the measured ≈274 KiB binary delta recorded in the commit message (FR-011a). **(e) Every `contracts/` row of §10a lands atomically with both regenerated trees and `make verify-contracts` exiting 0** (C-9, M-22) |
| **W2** | FR-004a, FR-022..FR-029 (incl. **FR-022d**, **FR-023c**), FR-063..FR-067b (incl. **FR-065a**), FR-073, FR-076, FR-076a, FR-110..FR-117, R-1..R-13 as FR-200..FR-212 | **AC-8.4b's TWELVE-mutation table plus the thirteenth aggregate mutation, reported as an artifact** *(revision 6, M-11: revision 5 said six, for twelve live rules)*. FR-113's nDCG@10 run with its per-query table and its ship/no-ship verdict. **A-13's answer**: whether the stored-field freshness mechanism holds. FR-049b's counters exist |
| **W3** | FR-030..FR-035, FR-065, FR-074 | *(unchanged)* |
| **W4** | FR-040..FR-048 (incl. **FR-047a**), FR-050..FR-053 | **REWORDED, Draft 7 — the old wording read as "the trash convention is still design work", and it is NOT (see the note under FR-048).** The convention is **already normative in this document**; W4's deliverable is to **assemble FR-048 / FR-048a / FR-048b / FR-038a / §4.1.5's refusal rows into one reviewable convention document, and to have that document reviewed, before W5 exposes the operation**. Nothing about the trash convention is open. **Plus FR-047a's template location and format**, which `create` consumes in this wave |
| **W5** | FR-016..FR-019a, FR-043a, FR-048a's `restore` operation, FR-049a, FR-070..FR-084, FR-090..FR-092, FR-127..FR-128 | **FR-049a's migration scan reports EMPTY.** FR-079's serialised six-tool definition set is **measured in bytes** and written back into FR-128. A-10's description review |
| **W6** | FR-020f, FR-025a's health view, **FR-025b**, FR-100..FR-103 | FR-025a's two surfaces agree **within one workspace scope** (FR-025b), asserted by test 58 |
