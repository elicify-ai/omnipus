# Vault records — specification

- **Implements:** [ADR-068](../architecture/ADR-068-vault-records-typed-record-layer.md) **revision 7** (2026-08-28)
- **Builds on:** [ADR-067](../architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md) — `pkg/knowledge` is **reused, never duplicated**
- **Date:** 2026-08-25; **revised 2026-08-28** to ADR-068 revision 7
- **Branch:** `feat/library-improvements`
- **Status:** Draft revision 5. Revision 2 followed round-2 review (BLOCK: 8 critical, 21 major); revision 3 realigned to ADR-068 revision 5; revision 4 realigned to ADR-068 revision 6 — a sixth tool (`vault_configure`), two named blast-radius criteria, a specified freshness token, a platform posture, a W0 wave, and byte budgets. **Revision 5 follows grill pass 1 (BLOCK: 19 critical, 41 major, 17 minor — `vault-records-spec-2026-08-25-review.md`) and three operator rulings that delete work.**

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
closes the grill's criticals: a **tenth** SQLite violation (join fan-out, §8.1a) that made every
count and total over a filtered multi-value list wrong; a defeat for **R-7** (dates), which had
none; **De Morgan normalisation before SQL emission**, without which the R-2 defeat only covers
leaves; a per-defeat mutation obligation (AC-8.4a); a re-derived freshness ordering, with the part
that is still not designed carried as a **stated open risk** rather than a fourth assumed mechanism;
and the ten normative contradictions, nine vacuous acceptance criteria and four wrong citations the
review names.

**Where the review is wrong, and it is said here rather than silently ignored.**

| Review claim | Verdict |
|---|---|
| *"Property type count changes from seven"* (the ruling's framing) | **Arithmetically false, and the spec says so rather than restating a wrong number.** Seven were declared: `text`, `enum`, `relation`, `date`, `number`, `money`, `person`. Deleting `money` and splitting `number` into `integer` and `decimal` is −2 +2. The count stays **seven**; the **membership** changed. Restating "six" or "eight" anywhere would be a new defect. |
| M-2 — *"FR-110 still lists revision 5's seven doc comments"* | **Upheld**, and corrected to thirteen (FR-110, FR-110a, FR-110b, SC-016). |
| C-9 — *"§4.1.2 refuses a cross-currency total while §4.2 renders one"* | **Dissolved, not adjudicated.** Under ruling (1) there is no currency, so neither artifact has a subject. Both are rewritten; the contradiction cannot recur because the concept is gone. |
| C-7 — *"`COLLATE NOCASE` defeats R-10"* | **Inverted by ruling (3).** `NOCASE` is now the specified implementation for text and enum columns. The half of C-7 that survives is real and is kept: `NOCASE` on a **relation-identifier** column collides two distinct ids, so identifier columns stay `BINARY` (§8.1, R-8 row). |
| M-37 — *"the receipts were taken on `sqlite3` 3.51.0; the shipped engine reports **3.53.3**"* | **The concern is upheld; the number is REJECTED with evidence.** `modernc.org/sqlite v1.46.1` (`go.mod:64`) was opened through the real Go driver and asked `select sqlite_version()`. It answers **`3.51.2`**, not 3.53.3 — a **patch** ahead of the 3.51.0 CLI the receipts were taken on, not two minor versions. The review's figure does not reproduce. The **standard** behind the finding is right and is adopted anyway: §8.1 now names both engines and both versions in place, and FR-020i adds a test asserting the linked version, because affinity and collation behaviour *is* version-sensitive and a silent engine bump must not silently move the semantics. |
| C-7's second half — *"a `NOCASE` relation-id column makes `rec_ABC` and `rec_abc` the same key"* | **Upheld and load-bearing**, and it is why ruling (3) is applied per column rather than per database. See §8.1's R-8 row. |
| C-3 I-1 — *"`SELECT id FROM t WHERE NOT (a=1 OR b=2)` returns **ZERO rows**"* | **REJECTED — the cell does not reproduce.** Over the review's own fixture it returns **one row** (`id 2`), not zero: `NOT(NULL OR TRUE)` is `FALSE` and `NOT(NULL OR NULL)` is `NULL`, so rows 3, 4 and 5 drop while row 2 survives. The **finding is still correct and still CRITICAL** — the correct De Morgan answer is **four** rows (2, 3, 4, 5), so three rows are silently dropped — but the receipt is restated at its true value in §8.1a, because a wrong number in a document about wrong numbers is not a small thing. |
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
**specified** against `ManifestEntry.Hash` rather than assumed to exist, per note rather than per
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
| FR-020c | the two indexes *"carry the same freshness token"* — a token that **did not exist** | a **per-note** `source_hash` compared against `ManifestEntry.Hash` (`pkg/knowledge/manifest.go:64`), with a named residual hole | ADR-068 D16.5; §7 test 32, SC-015 |
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
| `knowledge.Manifest` | **read on the query path** | `ManifestEntry.Hash` (`pkg/knowledge/manifest.go:64`) is the hex SHA-256 of a note's contents, keyed by collection-relative path in `Manifest.Entries` (`manifest.go:82`) and already readable by `Manifest.Get` (`manifest.go:174`). FR-020c makes it the freshness token. **The new work is the lookup and the column, not the value** |
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
| Enum value differing only in ASCII case | **REVERSED, revision 5 (operator ruling 3):** it **resolves** to the declared value and is conforming. `Won` is the declared value `won`; it sorts by `won`'s ordinal and renders as the file spells it. It does **not** create a second de-facto value, which is what D4 actually forbids (FR-011) |
| Enum value differing only in **non-ASCII** case (`Ätä` vs `ätä`) | **does NOT resolve** — stock SQLite folds no non-ASCII case, so this depends on the Go-side fold column being built. Specified in §8.1, R-10/R-5 rows; a build without the fold column reports these as non-conforming rather than matching them (FR-011a) |
| Record type declared in two schema files | both rejected, both paths named |
| Relation to a record in another workspace's vault | invisible; treated as dangling within scope |
| `integer` property holding a value above int64 | rejected at write, naming the bound (FR-012). Never `CAST` — a `CAST('9223372036854775808' AS INTEGER)` **saturates silently at int64 max** with no error (§8.1 receipt) |
| `decimal` property with more than 100 decimal places | rejected at write, naming the bound and the value's scale (FR-013); never rounded |
| Vault is not a git repository | schemas still work; no version history is claimed |
| Windows, two processes allocating IDs | collision possible (`fileutil.WithFlock` is a documented no-op — `pkg/fileutil/flock_windows.go`). **NOT healed by reconcile** — reconcile heals a lost counter, it cannot un-write two records that already share an identifier. Detected and reported by `vault_describe check_integrity`, which names both files and refuses to choose. Accepted, inherited from ADR-054 §5, not introduced here |
| Two indexes at different generations | the answer is reported **incomplete**, naming staleness (FR-020c) — never rendered as a complete answer |
| A `many` property compared with `=` | refused, naming `contains` (R-13) — never silently treated as membership |
| An anchor matching twice in a body-replace | refused, both line numbers named, file unmodified (FR-047) |
| A trashed note with inbound links | trashed anyway; the links are **not** repaired and the count is reported (FR-048) |

---

## 3. Behavioural contract

- When a query cannot include a record, the system **names that record and the reason**.
- When a numeric value exceeds its declared type's bound — an `integer` outside int64, a `decimal`
  above 100 places — the system **refuses and names the bound**. It never truncates, rounds or
  saturates. *(Replaces the cross-currency bullet, deleted with `money` — FR-014.)*
- When text, an enum value or a relation **path** is matched, the match is **case-insensitive**
  (operator ruling, revision 5). Record **identifiers** are matched exactly — see §8.1's R-8 row for
  why that one column is different.
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
  **saturates silently at int64 max** rather than erroring — verified, §8.1a. Range checking is
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
  are deleted with it, not reworded.** The compiler still does not *use* `LIKE` — §8.1 picks
  `instr()` over a folded column instead, for a reason that has nothing to do with case (`LIKE`
  needs `%`/`_` escaping on caller-supplied text, and an unescaped `%` in a needle is a wildcard
  nobody asked for) — but that is an implementation preference, not a prohibition, and **no test
  asserts the absence of `LIKE`.**
- The system must **not** claim its case-insensitive matching is Unicode-aware. It is **ASCII-only**
  wherever it rests on SQLite (`COLLATE NOCASE`, `LIKE`, `lower()`), verified over fourteen
  non-ASCII pairs in §8.1a; Unicode folding requires the Go-side fold column FR-011a specifies.

### Machine-verifiable constraints

| Constraint | Value |
|---|---|
| Results per page | default 50, max 200; a clamp is reported in the response |
| Candidate set materialised | 10,000 records; beyond this a **row-returning** query is refused (FR-064). **An aggregate-only query is exempt** and is bounded separately at the supported-vault size (FR-064a) |
| Relation hops | 2; a third is refused |
| Record layer availability | every SQLite-capable build. `linux/mipsle` is the one shipped **target** without it — **two binaries**, `omnipus-linux-mipsle` and `omnipus-lite-linux-mipsle` — and both **refuse by name** (FR-020h). *(Revision 5, m-3: "one shipped binary" was one target and two binaries.)* |
| Supported records per vault | 50,000 records — **and this is now reconciled with the candidate cap rather than left to collide with it (C-14).** 50,000 is the population the vault may hold; 10,000 is the population a **row-returning** query may materialise. They were set for different reasons and never checked against each other, which made ADR-068 §1.2's own motivating question — a pipeline total over more deals than the cap — unanswerable by construction. FR-064a resolves it with an aggregate-only path that returns no rows. **Note:** the index counts segments, not records, so this is an unknown larger document count; the segment ratio MUST be measured at W2 and recorded here |
| Peak RSS | ADR-067's < 64 MB steady state is inherited as a **TARGET, not a property** — it was measured for bleve alone, and SQLite's page cache, `GROUP BY`/`ORDER BY` temp b-trees and connection state are unmeasured (ADR-068 D16.4 item 4). W1 measures both indexes, idle and at the cap, on Linux **and** macOS. **No record-specific latency target is stated** |
| Rate limit | **new work for the write path** — `checkRetrievalRate` covers reads only (§1); 429 carries `Retry-After` (FR-067) |
| Numeric arithmetic | **REPLACES "Money arithmetic", revision 5.** `integer` is int64, bound-checked in Go and refused outside it (FR-012). `decimal` is exact and arbitrary-precision over `math/big`, declared scale bounded at **100** (`pkg/records/decimal.go:48`), refused above it and never rounded (FR-013). **No binary floating point anywhere in the parse, storage, comparison, ordering or aggregation path** — asserted by `pkg/records/decimal_no_float_test.go` |
| Case sensitivity | **matching is case-INSENSITIVE** for text, enum values and relation paths (operator ruling). **ASCII-only where it rests on SQLite**; Unicode requires the Go-side fold column (FR-011a). Record identifiers are matched **exactly**, on a `BINARY` column (§8.1, R-8) |
| SQLite engine | `modernc.org/sqlite v1.46.1` (`go.mod:64`), which reports `sqlite_version()` = **3.51.2** — verified through the driver, not assumed. A test asserts the linked version (FR-020i), because affinity and collation behaviour is version-sensitive |
| Agent tool count | exactly **6** `vault_*` names; **0** `knowledge_*` names; catalog **98 → 95** |
| Tool description budget | ~150 tokens each. **This is a budget for the description PROSE only and it is not the tool surface's standing cost** — see FR-079 and FR-128, corrected in revision 5: the whole JSON parameter schema, every parameter description included, is sent on every request (`pkg/tools/registry.go:557-560`), so the ~900-token figure was computed against the wrong denominator |
| Response budget | **BYTES, not tokens**: ~200–320 bytes/hit, ~4,000 default, **16,000 hard cap**; `minimal` ~80 bytes/hit. **The cap is allocated, not first-come** — mandatory header and `NEXT` are reserved first, then problems to their own clamp, then rows (FR-127c). Scoped to `vault_find`, `vault_describe` and `vault_configure`; `vault_read` has its own budget (FR-072a) |
| Index freshness | **per note**: the properties row's `source_hash` versus `ManifestEntry.Hash`; a mismatch, a missing entry or an empty hash forces `complete: false` for that record |
| Scoring model | BM25, set explicitly. TF-IDF is the library default and is a defect (FR-110) |
| Embeddings | none, permanently (FR-117) |

---

## 4. Functional requirements

### Schema and types

- **FR-001** The system MUST load record-type schemas from `<vault>/.omnipus-vault/records/<type>.yaml`.
- **FR-002** The system MUST reject a schema without `schema_version`.
- **FR-003** The system MUST reject two schema files declaring the same record type, naming both paths.
- **FR-004** **MEANING CHANGED, revision 5 (operator ruling).** The system MUST support exactly these **seven** property types: `text`, `enum`, `relation`, `date`, `integer`, `decimal`, `person`. *Previously: `text`, `enum`, `relation`, `date`, `number`, `money`, `person`. **`money` is deleted from the type system** — the operator's requirement is "a precise decimal datatype and also an integer 64 datatype", not a currency-carrying value; and **`number` is split into `integer` and `decimal`**, because one numeric type forces the index to infer a column type from the first value it sees. **The count is unchanged at seven; the membership changed** — −1 (`money`) −1 (`number`) +2 (`integer`, `decimal`). Anywhere this document previously said "seven property types" it still says seven, and it is not a stale figure.*
- **FR-005** The system MUST treat a note whose `type` matches no schema as an ordinary note, without error.
- **FR-006** Each property MUST declare arity, and the system MUST reject a value of the wrong arity.
- **FR-007** The system MUST distinguish an absent property from every value of that property.
- **FR-008** A negative filter MUST include records where the property is absent, unless the query excludes them explicitly.
- **FR-009** Property types MUST be scoped to their record type; the same name in two types is unrelated.
- **FR-010** An enum MUST declare its values in order, and sorting MUST follow declared position, not lexical order.
- **FR-011** **MEANING CHANGED, revision 5 (operator ruling 3).** The system MUST **resolve** an enum value to a declared value **case-insensitively**, and MUST reject a value that resolves to none of them, listing the permitted values. *Previously the resolution was exact-case, which made `Won` a rejection. Under ruling 3 it resolves to `won`.* **Two consequences are normative:** the value it resolves to supplies the **ordinal** (FR-010, R-5), so ordering is unaffected by spelling; and the **file's own spelling is what renders**, because the note is the source of truth and this system does not rewrite a file it was not asked to change (FR-046's sibling rule). *(This does not weaken D4: D4 forbids **auto-creating** a second value, and folding does the opposite — it collapses two spellings into one. §7 test 2 and DS-1's `Active` row are corrected, not merely re-labelled.)*
- **FR-011a** **NEW, revision 5. Case-insensitive matching MUST be implemented by a Go-computed fold column, not by SQLite's collation, and the reason is a measurement rather than a preference.** Every text, enum-label and relation-path column MUST carry a derived `<col>_fold` column holding the value case-folded by **one Go function** (`strings.ToLower`, which is Unicode-aware), and every equality and `contains` predicate on those properties MUST compare the fold column against a needle folded by that same function.
  - **Why not `COLLATE NOCASE`, `LIKE` or `lower()`:** all three fold **ASCII only**. Executed over fourteen upper/lower pairs, each folded the two ASCII pairs and **zero** of the twelve non-ASCII ones; `lower()` returned every non-ASCII input byte-for-byte unchanged (§8.1a §H). The engine carries no `ENABLE_ICU` and `icu_load_collation` does not exist, so **there is no Unicode-aware option inside SQLite at all here**. A vault in German, Polish, Greek or Turkish would get case-insensitive matching for its ASCII words and silent case-sensitivity for the rest — which is the "works until it doesn't, with no error" shape this document exists to remove.
  - `COLLATE NOCASE` on those columns is **permitted and is no longer a defect** (it makes the ASCII case work even if a predicate escapes the fold path), but it is **not** the mechanism the requirement rests on.
  - **The fold column is derived and disposable** like everything else in the properties index (FR-020a): it is recomputed on rebuild and never written to a note.
  - **The identifier column is excluded, deliberately** — see R-8 and AC-8.8.
  - **The cost is stated:** one extra TEXT column per foldable property, roughly doubling the properties index's text bytes, and one `strings.ToLower` per value per index pass. **Nobody has measured it** — it joins A-12's W1/W2 measurement obligation.
- **FR-011b** **NEW, revision 5.** A build that does not carry the fold column MUST NOT present its matching as Unicode-insensitive. Documentation, tool descriptions and the `explain` output MUST state **ASCII-case-insensitive** where that is what is delivered.
#### The two numeric types (revision 5, operator ruling)

An author **chooses** `integer` or `decimal` in the schema. **There is no inference from the first
value seen**, and this is the point of the split rather than a side effect of it: with one `number`
type the properties index would have to decide a column type from data, so the same property could
land in an INTEGER column in one vault and a TEXT column in another, and `2` and `2.0` would compare
differently depending on which note happened to be indexed first. The schema decides; the column
follows the schema.

- **FR-012** **MEANING CHANGED, revision 5 — this FR was `money`; `money` is deleted.** An `integer` property MUST hold a signed 64-bit integer, and the accepted range MUST be exactly **−9,223,372,036,854,775,808 to 9,223,372,036,854,775,807** inclusive. A value outside that range, or a value carrying a fractional part, MUST be **refused with the bound named** — never truncated, never rounded, and never widened to `decimal` on the system's initiative. *(SQLite's INTEGER storage class is exactly int64 and stores the whole range losslessly, so the storage bound and the type bound are the same number and there is no second limit to reconcile. What SQLite does **not** do is refuse an overflow: scalar `9223372036854775807 + 1` silently becomes a REAL — see §8.1a, R-11a.)*
- **FR-013** **MEANING CHANGED, revision 5.** A `decimal` property MUST be **exact and arbitrary-precision**, and no value of it may pass through a binary floating-point representation anywhere in the parse, storage, comparison, ordering or aggregation path. *Previously: "Money arithmetic MUST be exact decimal" — the guarantee survives its subject.*
  - **The declared-scale bound is `maxDecimalScale = 100`** (`pkg/records/decimal.go:48`) — **100 digits after the point**, and the choice is deliberate and generous. The retired `money` type bounded scale at `maxMoneyScale = 12` (`decimal.go:166`); **that bound is deleted with money and MUST NOT be inherited by `decimal`.** Twelve places is a currency-shaped limit and this type is not currency-shaped: a `decimal` property is as likely to hold a scientific measurement, a ratio, or a coordinate as a price. 100 is what the existing parser already enforces and already has a property test over the full symmetric range (`pkg/records/decimal_string_bounds_test.go`, which sweeps every scale in `[-100, +100]`), so adopting it costs nothing and removes a bound nobody could justify.
  - **A value whose scale exceeds 100 MUST be refused, naming the bound and the value's own scale.** It MUST NOT be rounded to fit. Rounding to satisfy a bound is a silent change to a number, which is the failure class this document exists to remove.
  - `pkg/records/decimal.go` (588 lines, `math/big`-based, with `pkg/records/decimal_no_float_test.go` asserting no binary float appears anywhere in the path) **is the implementation and survives money's removal unchanged.** It is the valuable half of the retired work.
- **FR-014** **RETIRED, revision 5 — `money` is deleted, so cross-currency summation has no subject.** *Previously: "The system MUST refuse to sum money across currencies and MUST list the currencies present." **The number is retired rather than reused**, so that a reader meeting `FR-014` in an older document, a commit message or a test name finds this entry and not a different requirement wearing the same identifier. Everything that depended on it goes with it: US-2 scenario 3, the behavioural-contract cross-currency bullet, the `Cross-currency total` refusal string in §4.1.2, §4.2's `GBP only` total, **R-6** and its §8.1 defeat row, `TestMoney_RefusesCrossCurrencySum` (§7 test 4), and the `CurrenciesPresent` field. All are removed in this revision, not left as dead references.*

- **FR-015** A change to a schema file MUST invalidate affected records and trigger revalidation. Schemas live under a directory the scanner does not walk, so no manifest entry or mtime exists for them; the system MUST track them explicitly rather than inheriting note-scanning behaviour. *(Unchanged in meaning. **Cited by ADR-068 D23.3** as the reason an existing-record-type change sits in a cascading tier: it retroactively reinterprets every record of that type. **Revision 4: that tier is `vault_configure`, not `vault_restructure`** — the reasoning is unchanged, the destination moved, and the same reasoning now also carries **creating a new type**, per FR-016.)*

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

- **FR-016** **MEANING CHANGED, revision 4 (ADR-068 D15.6).** Creating a **new** record type MUST be an operation of **`vault_configure`**, and the response MUST name the count of pre-existing notes that have just become validated records as a result. *Previously: an operation of `vault_edit`, on the stated ground that a new schema file changes no existing note's meaning.* **That ground was false under ADR-068 D1**: a note becomes a record by declaring `type:` in frontmatter, and a note whose declared type matches no schema is an ordinary note (FR-005). Writing `.omnipus-vault/records/company.yaml` therefore converts **every pre-existing note already carrying `type: company`** into an indexed, queryable, validated record — and any of them missing a `required: true` property becomes a validation finding nobody asked for. That is **C-B**, and it is invisible in the diff: one new twelve-line YAML file, hundreds of notes changed in meaning.
- **FR-017** **MEANING CHANGED, revision 4.** Changing or deleting an **existing** record type MUST be an operation of **`vault_configure`**, because FR-015 makes it reinterpret every existing record of that type. *Previously: `vault_restructure`.*
- **FR-018** **MEANING CHANGED, revision 4.** Creating or editing a **saved view** MUST be an operation of **`vault_configure`**. *Previously: `vault_edit`.* A view writes no note, but it changes what a query returns, which is **C-B in its weakest form** — and a view is part of the same control plane as the schema, so an operator granting one grants the other knowingly.
- **FR-018a** `vault_configure` MUST NOT declare an `expect_version` parameter, and a test MUST assert its absence (ADR-068 D15.5c, AC-15.5d). A single-file content hash cannot honestly guard a change whose blast radius is every note declaring the type. Its safety is policy, plus the audit entry (FR-077), plus `check_integrity` (FR-075) — never optimistic concurrency it cannot honour.
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
- **FR-020b** Money MUST never be converted to a binary floating-point number anywhere in the storage or retrieval path. Amount is stored as **integer minor units** with currency and declared scale in their own columns (ADR-068 O-2).
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
  - **An empty hash is unknown freshness and MUST be flagged, never assumed fresh.** `ManifestEntry.Hash` is deliberately empty for attachments (`manifest.go:62-65`, because FR-039a forbids opening one and hashing is opening). Records are notes, so this should not arise; the rule is stated for the case where it does.
  - **A flagged record MUST be re-queued** for re-indexing, so a divergence does not stay flagged. **BOUNDED, revision 5:** the re-queue MUST be **deduplicated and debounced** — at most one outstanding re-index request per note path, and a per-note cooldown of **60 seconds** between requeues. A note under active editing diverges on every query, and an unbounded re-queue turns every query of it into an indexing job.
  - **A row with no manifest entry at all** — the note was deleted, the row orphaned — MUST be flagged and MUST be reported by `check_integrity` as an orphaned row (FR-075).
- **FR-020c1** **The residual hole is named and accepted for W1.** FR-020c detects divergence **for records the query returned**. It does **not** detect a record that is stale *and excluded from the result by a stale predicate* — if the properties index holds `status: prospect` for a note whose file now says `status: churned`, a query for `status = churned` never returns that row and so never compares its hash. Closing it means hashing the whole candidate population rather than the returned page, which is the cost FR-064's cap exists to avoid. It is bounded by re-indexing on the next sync and by `check_integrity`'s explicit sweep. **The documentation MUST state that a completeness verdict covers what the query returned, not what it did not** — an operator who is not told this will read `COMPLETE: yes` as a stronger claim than it is.
- **FR-020d** An index created before record support exists MUST NOT be opened and queried for fields it cannot hold. Two guards are required, and the second exists because the first depends on a human remembering: **(G1)** an index **format version** that forces a rebuild, and **(G2)** an assertion that the **persisted mapping** actually contains the fields the query planner will use. `bleve.OpenUsing` takes no mapping argument and the mapping is persisted in the store, so re-running the same code against an existing index silently returns zero hits with `err == nil` — the spike **reproduced** this (1 hit on a fresh index, 0 and no error on an existing one).
- **FR-020e** Existing bleve indexes MUST be **rebuilt**, not opened, on upgrade. `bleve/v2 v2.6.1` (`go.mod:12`) and `zapx/v17 v17.2.3` (`go.mod:107`) fix the segment corruption the spike found at 100,000 documents, but **segments already written stay corrupt** — a version bump alone does not discharge it. *(Revision 4, ADR-068 D20: this ships in **W0**, ahead of and **independently of** everything else in this specification. The panic is `slice bounds out of range` **unrecovered** through `indexImpl.Search`, which in the gateway is a process crash, and it reproduces with the shipping mapping and no record fields at all — it is an ADR-067 defect this work merely tripped over. Revision 3 left it inside the record waves, behind schema files and a new storage engine. Hard Constraint #7 does not admit a stylistic reason for leaving a live crash in the field behind a design wave.)*
- **FR-020f** **Index state MUST be queryable, not only broadcast.** The system MUST expose a request-response path returning the current indexing state of a collection — phase, indexed count, total, and the freshness generations FR-020c compares — in **the same shape a live progress frame carries**. Today progress is live-only: `KnowledgePanel.tsx:226` reads exclusively from `useKnowledgeIndexStore`, which is populated only by incoming `knowledge_index_progress` WS frames, and no REST endpoint returns index state. (`index_state` at `pkg/gateway/rest_knowledge.go:405` is a coarse string riding on a **search** payload — `ready` / `not_built` / `unavailable`, used at `rest_knowledge.go:534-535,620` — not a queryable snapshot, and it carries no phase or counts.) The consequence is that a client which connects **after** an index finished renders "no progress has arrived" for a completed index, which is what a human sees for any fast index. The frames are emitted correctly at both edges — a leading frame before `syncWith` runs (`pkg/gateway/knowledge_lifecycle.go:836`) and an unthrottled terminal `progress.flush` (`pkg/knowledge/index.go:829`) — so **this is not an emission defect and MUST NOT be fixed by changing emission** (ADR-068 §2.6 investigated the emission theory and did not confirm it).
- **FR-020i** **NEW, revision 5.** A test MUST assert the **linked SQLite version** by opening a database through `modernc.org/sqlite` and reading `sqlite_version()`. The expected value at this revision is **`3.51.2`** for the pin at `go.mod:64` (`modernc.org/sqlite v1.46.1`), verified by execution. Affinity, collation and `NULLS LAST` behaviour are version-sensitive, and §8.1's whole defeat table is a set of claims about one engine's semantics; a driver bump that changes them must fail a test rather than change the product's answers quietly. *(The test asserts the version it was written against and is expected to be updated deliberately on a bump, together with re-running §8.1a's receipts. That is the point: the update is the review trigger.)*
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
- **FR-021** **MEANING CHANGED, revision 3 (ADR-068 D16.2/D16.3).** Filtering, grouping, joining and aggregation MUST be evaluated **in the properties index**, over typed columns, so that only surviving rows are materialised. *Previously: "evaluated in Go over the retrieved candidate set". **Cited by:** ADR-068 D21.5 (which builds its tokenizer-hazard argument on Go-side evaluation), the storage spike's C-3, and §6 of this spec. The tokenizer hazard **survives the change**: FR-112's rank fusion is still Go-side, so bleve still selects with one notion of a term while Go ranks with another — see FR-116.* What remains in Go is rank fusion (FR-112), rendering (FR-120) and the problem report (FR-025) — not membership.
- **FR-021a** Every value the properties index holds MUST be **typed at write time** against the record's schema. A value that does not conform MUST be stored as non-conforming and flagged, never coerced into the typed column, so that R-4 can report it rather than compare it.
- **FR-021b** **NEW, revision 5 — FR-021a as written collides R-4 with R-2/R-3 and this closes it.** "Never in the typed column" leaves **NULL** in that column, and NULL is the **absence** representation (FR-007), so a non-conforming value and an absent property become indistinguishable in storage. Every typed property MUST therefore carry a **conformance flag column** with three distinguishable states — **present-and-conforming**, **present-and-non-conforming**, **absent** — and that flag MUST be consulted **at comparison time and at `ORDER BY` time**, not only when the problem list is built. A non-conforming value MUST NOT be ordered as though it were absent, and an absent value MUST NOT be reported as a problem.
- **FR-021c** **NEW, revision 5.** Parsing a value into its typed column MUST happen **in Go, before insert** — never by a SQL function on the way in. Two of SQLite's parsers return **NULL with no error** on malformed input (`unixepoch('not-a-date')` and `unixepoch('2026-8-26')`, both verified, §8.1a §E), and one **saturates silently** (`CAST('9223372036854775808' AS INTEGER)` → int64 max). Any of the three would write an FR-021b non-conformance into the storage cell reserved for absence, defeating FR-021b in the same transaction that FR-021b exists to protect.
- **FR-021d** **NEW, revision 5. A `date` MUST be stored as a signed integer epoch, with precision declared on the property** (`seconds` by default; `milliseconds` permitted), and all comparison and ordering MUST run on that integer column. The raw text is retained for rendering and for FR-021b's problem line only. *Rationale, verified in §8.1a §E: under TEXT storage two spellings of the same instant compare unequal and order anyway; fractional seconds invert the order (`Z` sorts after `.`) **even in an all-UTC corpus**; the `T`-vs-space separator reorders; and any non-UTC offset breaks ordering outright. R-7 had no defeat at all before this revision.*
- **FR-022** **`vault_find`** MUST accept a structured filter object; the system MUST NOT accept a text query language (ADR-068 O-3, unchanged in substance — only the tool name moved).
- **FR-022a** **NEW, revision 5.** A `contains` predicate whose value is the **empty string** MUST be refused, naming `is_present` as the operator that was probably meant. `instr('abc','')` and `instr('','')` both return **1** (§8.1a §B), so an empty needle matches **every row including the empty ones** — a whole-table result returned as though it were a filtered one.
- **FR-023** Every property name, enum value and relation target in a query MUST be validated against the schema **before** evaluation.
- **FR-023a** **NEW, revision 5. Negation MUST be pushed to the leaves by De Morgan normalisation BEFORE any SQL is emitted, and the compiler MUST NOT emit a `NOT` over a compound expression at all.** *(This is the hole under §8.1's flagship R-2 defeat: `(x IS NULL OR x <> ?)` is a **leaf** rewrite and the filter grammar §4.1.2 declares is a **tree**, whose normal shape is `{not: {all: [...]}}`. The natural implementation — compile the subtree, wrap it in `NOT (...)` — silently drops every NULL-bearing row however correct each leaf is. Verified: over §8.1a §B's fixture, `NOT (a=1 AND b=2)` returns **one** row where the normalised form returns **four**.)* Normalisation is `NOT(all) → any(NOT …)`, `NOT(any) → all(NOT …)`, `NOT(NOT x) → x`, applied recursively until every `not` sits on a leaf, at which point the leaf rewrite of §8.1's R-2 row applies — **including for the ordered operators**, since `NOT (x > 5)` drops the NULL row exactly as `NOT (x = 5)` does. **AC-8.4a requires a mutation that removes the normalisation pass and a truth-table cell over a compound negation that dies when it does.**
- **FR-024** A query naming an unknown property or enum value MUST be rejected with valid names listed, and MUST NOT return an empty result set. **Scope MUST be resolved before this rejection**, so the valid-names list never reveals schemas outside the caller's workspace — otherwise the error channel defeats FR-062.
- **FR-025** Every query response MUST carry a completeness verdict and a problem list as **required** fields.
- **FR-026** A record excluded from an aggregate MUST be named in the problem list with the reason.
- **FR-027** Grouping MUST support two levels.
- **FR-028** Grouping by a multi-value property MUST place a record in every group it belongs to.
- **FR-028a** **NEW, revision 5 — the tenth SQLite violation, and the one that made every count and total over a filtered list wrong.** **Every aggregate over a filtered set MUST be computed over DISTINCT PARENT ROWS.** The mandated mechanism is a **semi-join** — `... FROM rec r WHERE EXISTS (SELECT 1 FROM <child> t WHERE t.rec_id = r.id AND <predicate>)` — or an aggregate over an explicitly de-duplicated subquery (`SELECT DISTINCT r.id, r.<col> FROM …`). A **plain join with the predicate in the `WHERE` clause is FORBIDDEN as the aggregate's source.**
  - **Why, verified (§8.1a):** the R-9 defeat is a child-table equality join, which is right for membership and wrong for arithmetic. A record carrying both `vendor` and `vendors` joins **twice**, so `COUNT(*)` returns **2** and `SUM(amount)` returns **200** where the truth is **1** and **100**. Quiet, plausible, and wrong by a factor that varies per record. It reaches `count`, `sum`, `min`, `max`, the `join` parameter and `group_by`.
  - **Why `EXISTS` and not `COUNT(DISTINCT)`, decided rather than assumed:** `COUNT(DISTINCT r.id)` fixes `count` and has **no working analogue for `sum`**. `SUM(DISTINCT)` deduplicates on **value**, not on row identity, so two genuinely distinct records sharing an amount collapse into one — it returned **100** against a truth of 200 while the naive join returned 300 (both verified). It errs in the direction that *looks* conservative, which makes it the harder wrong answer to catch in review. `EXISTS` is one shape that is correct for every aggregate, present and future.
  - **`group_by` is the exception and needs both halves.** Grouping by a multi-value property **must** fan out, because FR-028 requires a record under every group it belongs to. The requirement there is: **fan out for membership, then compute each group's aggregate over distinct parent rows within that group.** A `GROUP BY` whose counts are taken over the fanned-out rows produces the defect FR-028 exists to avoid, arriving from the other side.
  - **AC-8.4a requires a mutation that replaces the semi-join with a plain join, and a truth-table case that dies when it does:** a record gains a **second** matching value of a `many` property, and `count` and `sum` MUST be unchanged.
- **FR-029** Grouping by a relation MUST be supported.

### Relations and identity

- **FR-030** A relation MUST be stored on disk as a quoted wikilink.
- **FR-031** The index MUST resolve a relation to the target's record identifier.
- **FR-032** The inverse of a relation MUST be derived and MUST NOT be stored in any file.
- **FR-033** A relation whose target does not exist MUST be reported by validation.
- **FR-034** A relation whose target is not of the declared type MUST be reported by validation.
- **FR-035** Relation cardinality MUST be declared and enforced.
- **FR-036** Every record MUST carry an identifier unique within its type, minted on creation.
- **FR-037** Identifier allocation MUST be mutually exclusive within a process and, on POSIX, across processes.
- **FR-038** On open, the allocator MUST raise its counter to at least the highest existing identifier and MUST NEVER lower it. Reconciling *to* the maximum guarantees reuse after the highest record is deleted, which makes an existing relation resolve to a different record.
- **FR-039** A duplicate identifier MUST be a hard validation error naming both paths.
- **FR-036a** **NEW, revision 4 (ADR-068 D15.1).** `vault_edit`'s one-file rule has exactly **one** accepted exception: `create` also writes `.omnipus-vault/records/.seq` under a cross-process flock, because FR-036 requires an identifier. `.seq` is accepted because it is a monotonic counter with no readable content, no meaning to any query and no recoverable state to lose — corrupting it costs skipped identifiers, which FR-038 already permits, never a wrong answer. **It is the only exception**; adding a second is a decision for another ADR, not for whoever writes the next tool. *(Revision 3 stated the tier rule as "writes only the file named in `path`", which was false on the first record ever created.)*

### Writes

- **FR-040** A write MUST reuse `pkg/knowledge/author.go`'s splice for scalar values; the system MUST NOT re-serialise the document.
- **FR-040a** The system MUST add a list-valued splice, preserving the source's existing list style. `SetProperty` is scalar-only and cannot satisfy FR-045 or `many: true`.
- **FR-040b** A scalar write targeting a key whose current value spans multiple lines MUST be refused and the file left unmodified, because the existing key-splice removes continuation lines and would silently delete the value.
- **FR-041** A write MUST leave the file byte-identical outside the patched span.
- **FR-042** A write violating the schema MUST be rejected with the expected shape named, leaving the file unmodified.
- **FR-043** A write MUST carry ADR-067 D14's version token; a stale token MUST be refused and the refusal audited.
- **FR-044** Every mutating `vault_*` tool MUST emit an audit entry per ADR-067 D19, named per FR-077.
- **FR-045** Relations MUST be modified through distinct add, remove and replace operations; replace MUST be named explicitly.
- **FR-046** Derived values MUST NOT be written into frontmatter.

**Two primitives in the tool tables do not exist and are not relabellings** (ADR-068 D14.1).
Every shipped `NoteEdit` constructor is additive — `SetProperty` (`pkg/knowledge/author.go:766`),
`AppendSection` (`author.go:799`), `AppendSectionAt` (`author.go:813`), `AddWikilink`
(`pkg/knowledge/authoring_tools.go:1103`), `AppendSectionOnce` (`authoring_tools.go:1129`) —
and there is **no body-replace and no delete primitive anywhere in the package**. The `NoteEdit`
type is `func(src []byte) ([]byte, error)` (`author.go:540`), general enough to express anything;
`EditNote` (`author.go:620`) is a **harness that applies additive edits in order**, not an
editor. Its name invites exactly the misreading these two FRs exist to prevent.

- **FR-047** **Body-replace** MUST address its target by **anchor text or line range**, and its ambiguity rule is part of this specification rather than the implementer's choice: **an anchor matching more than once is REFUSED, naming every match with its line number, and the file is left unmodified.** A line range outside the file is likewise refused. Body-replace is an operation of `vault_edit` (one named file).
- **FR-048** **Trash** MUST have a soft-delete convention, and it MUST answer three questions the current code answers for nothing: **where a trashed note goes** — `<vault>/.omnipus-vault/trash/<RFC3339 timestamp>/<original relative path>`, preserving the path so a restore is unambiguous; **what happens to inbound links** — they are **not** repaired, because there is nothing to repair them to, and the response MUST name the count and list the linking notes (FR-124's borrowed-value rule does not apply; these are real inbound links); and **when the index forgets it** — immediately on trash, in both indexes, in the same generation bump. Trash is an operation of `vault_restructure`. *(**Sequencing RESOLVED, revision 4.** Revision 3 read ADR-068 D20's two listings as convention-in-W4, operation-in-W5 and flagged the reading as A-9. **ADR-068 revision 6's D20 adopts exactly that split in the ADR's own words** — "the convention is W4 design work, the operation is W5" — naming the alternative it avoids: shipping part of a tier-5 tool before W5 defines the tool or seeds its policy. A-9 is closed in this spec's favour.)*
- **FR-049** Rename and move MUST report their cascade in counts (§4.1.5). ADR-067 D10 already rewrites inbound wikilinks on rename; what is new is that the count is **reported**, so an operator reading an audit entry can see how far a rename reached.

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
- **FR-064** **SCOPED, revision 5.** A **row-returning** query whose candidate set exceeds 10,000 records MUST be refused with a narrowing instruction. **"Candidate" is now defined, because it is the most consequential bound in the document and revision 4 never said what it counted:** the candidate set is **the rows surviving the filter, before paging, joins, grouping and ranking** — i.e. the population the query would page through, not the population of the record type. It is counted by a `COUNT(*)` over the compiled `WHERE` clause, **taken before any row is materialised** (§7 test 47), and its cost is one extra index-bound aggregate per call. **That cost is real and is unmeasured** — it joins A-12's obligation, which revision 4 did not name it under.
- **FR-064a** **NEW, revision 5 — this closes the collision between the supported-vault size and the candidate cap.** An **aggregate-only** query — one that requests `aggregate` and/or `group_by` and returns **no rows** — is **exempt from FR-064** and is bounded instead at the supported vault size (50,000 records, the constraint table). *Rationale: the constraint table supports 50,000 records per vault while FR-064 refuses above 10,000 and FR-066 forbids an aggregate over a refused set, so **a vault with 10,001 deals could never obtain a total over its deals, by design, with no escape hatch** — and the refusal's own remedy ("add a filter on status") means running seven queries and adding them by hand, which is precisely the five-manual-steps cost ADR-068 §1.2 cites as the thing this system removes. The two bounds were set for different reasons and never checked against each other.*
  - **Why this is not "raising the cap", which FR-066a rightly forbids.** FR-064 exists because **materialising** rows is what costs memory (FR-066b's one-page rule). An aggregate-only query materialises **one result row**: the `COUNT`/`SUM` is pushed entirely into SQLite and no candidate is retrieved. The bound that was load-bearing is not being relaxed; a query shape that does not trip it is being permitted.
  - **It is still bounded, and the bound is still a refusal.** Above 50,000 candidate records the aggregate is refused naming the count and the supported size. And **FR-066b still applies**: an aggregate-only query MUST hold no more than the result row in memory, asserted by measured peak RSS at the bound, not by inspection.
  - **The response says which path it took.** An aggregate-only response carries `ROWS: none — aggregate-only over N records` in its header, so a caller cannot mistake an exempted total for a paged one.
  - **AC-F8** — a vault of 24,000 deals returns a `sum` over all of them, with no rows and `COMPLETE: yes`; the same query with `select` or `limit` present is **refused** by FR-064.
- **FR-065** A query requesting more than two relation hops MUST be refused.
- **FR-066** No aggregate MUST be returned over a candidate set refused **under FR-064**. *(FR-064a's exempt path is not a refused set; it is a different query shape with its own bound.)*
- **FR-066a** **The 10,000-record candidate cap in FR-064 is a HARD precondition, counted BEFORE retrieval.** It is not a politeness limit and MUST NOT be relaxed on the ground that the properties index is assumed to make it cheap. *(ADR-068 D16.3a, condition C-3, upheld against revision 5's relaxation. Nobody has measured the two-index path; relaxing a bound is something to do **after** a measurement shows it is no longer load-bearing, never **because** a new store is assumed to make it so. If W1 shows the path is comfortable at 50,000, the cap is revisited in a revision that cites the number.)*
- **FR-066b** **The query path MUST hold no more than one page of rows in memory at once**, and this MUST be asserted by **measured peak RSS at the cap**, not by code inspection. *(ADR-068 D16.3a, condition C-2. Pushing predicates into SQLite changes where rows are selected, not whether surviving rows are streamed to the renderer: a `GROUP BY` over 10,000 rows still returns a result set that must not be materialised whole.)*
- **FR-067** **NEW, revision 4 (ADR-068 D15.5b).** `vault_edit`, `vault_restructure` and `vault_configure` MUST be rate-limited, returning **429 with `Retry-After`** on breach. **This is new work, not inheritance.** Revision 3's constraint table said the limiter was "shared with ADR-067's knowledge limiter"; it is not. `knowledgeRESTLimiter` (`pkg/gateway/rest_knowledge.go:90`) is consulted at exactly one place, `rest_knowledge.go:691`, on the **REST** path — no agent tool touches it. The agent-tool limiter `checkRetrievalRate` has three call sites, all reads (`pkg/knowledge/tools.go:610`, `:749`, `pkg/knowledge/authoring_tools.go:1330`). No write `Execute` consults either.

### Tools, policy, contracts

- **FR-070** **MEANING CHANGED AGAIN, revision 4 (ADR-068 D15.3, D15.6).** The system MUST expose exactly **six** tools: `vault_describe`, `vault_find`, `vault_read`, `vault_edit`, `vault_restructure`, **`vault_configure`**. *Revision 3 said five; revision 2 said nine `record_*`. The sixth is the control plane (FR-016..FR-018a) and it is a **correction**, not an addition — without it the schema-authoring policy lever does not exist. Cited by §6 traceability, §7 test 18 and SC-011; all three are restated below. The nine `record_*` names were never implemented, so nothing but this document referenced them.*
- **FR-070a** The six tools MUST also **replace** the nine shipping `knowledge_*` tools — `knowledge_search`, `knowledge_graph`, `knowledge_tasks`, `knowledge_create`, `knowledge_link`, `knowledge_set_property`, `knowledge_append_section`, `knowledge_move`, `knowledge_rename` (`pkg/coreagent/core.go:475-482`). After W5 no `knowledge_*` name remains in `allStaticToolNames` (`pkg/coreagent/core.go:357`), in the global ceiling (`pkg/config/defaults.go:637-646`), or in any seeded per-agent map.
- **FR-070b** **The tool boundary is the policy boundary. REVISED, revision 4: there are TWO criteria, not one.** `vault_edit` MUST write **only** the file named in its `path` argument, plus `.seq` on `create` (FR-036a). `vault_restructure` is the only tool permitted to **write bytes into** a file the caller did not name (**C-A**). `vault_configure` is the only tool permitted to **change what existing files mean** — their validity, their type, or how a query renders them — **without writing them** (**C-B**). A reviewer decides an operation's tool by asking both questions in order. *Revision 3 stated one criterion and, following ADR-068 revision 5, applied it under two incompatible readings — bytes to keep `link` in the per-file tier, meaning to push a schema change into the cascading one — and then presented the result as evidence the criterion generalises. **ADR-068 D23.3 withdraws that claim in revision 6.** A criterion that needs two readings is two criteria; the design is not weaker for having two, the document was weaker for pretending it had one.* Additive-versus-destructive remains explicitly **not** a criterion — `set_property` overwrites and stays in `vault_edit`.
- **FR-070c** An operation enum **within** one tool is permitted and carries no policy meaning, and **the seeded default for a tool MUST be chosen for the WIDEST operation it grants, not the most common one**. Policy resolves on the tool **name** only — `resolveToolPolicyAtExec(ts *turnState, toolName string, …) string` (`pkg/agent/loop.go:12418`) takes no arguments and no operation discriminator — so every operation reachable from one tool MUST be equally acceptable to an operator who granted that tool. *(Revision 4: this is why FR-016..FR-018 move out of `vault_edit`. It is also why `vault_edit`'s description must name `replace_body` — its widest remaining operation — rather than `set_property`, its most common one; see FR-079.)*
- **FR-071** Tool names MUST contain no dots. *(Audit event names are not tool names: FR-077's `vault.edit` / `vault.restructure` carry a dot deliberately and are out of this requirement's scope.)*
- **FR-072** **MEANING WIDENED, revision 3 (ADR-068 D22.1); extended to six in revision 4.** **Every** result from **all six** tools MUST be rendered to the model as compact text, never as a JSON document. *Previously: `record_schema` alone. Cited by §6 and §7 test list.*
- **FR-073** **MEANING CHANGED, revision 3 (ADR-068 D15.3).** `explain` MUST be a **boolean argument of `vault_find`**, not a tool. With `explain: true` the system MUST report what the query would select, which properties it could not evaluate and why, and MUST NOT evaluate the query. *Previously: a `record_explain` tool. Cited by §6 and §7.*
- **FR-074** `vault_read` MUST return the note's **version token** in every successful response. Obtaining a token MUST NOT require sending a write that is expected to fail. *(Today the only source of a token is `*ConflictError` from a refused `EditNote` — `pkg/knowledge/author.go:701`; `EditNoteRequest.ExpectVersion` (`author.go:566`) refuses an empty token too.)*
- **FR-075** **WIDENED, revision 4 (ADR-068 D15.3).** `vault_describe` MUST accept an integrity sweep (`check_integrity: true`) reporting **duplicate identifiers** (FR-039), **unresolvable and mistyped relations** (FR-033, FR-034), **unresolved ordinary wikilinks and orphan notes across the whole vault**, and **rows in the properties index with no note behind them** (FR-020c), and MUST name every offending path. *The wikilink and orphan half is not optional and not a nicety: `knowledge_graph`'s `unresolved` and `orphans` cover ordinary wikilinks vault-wide (`pkg/knowledge/tools.go:809-811`), **most notes in a vault are not records**, and FR-070a retires that tool — so without this widening a vault-wide broken-link report would have no home in the new surface at all.*
- **FR-075a** **BOUNDED, revision 4 (ADR-068 D15.5b).** `check_integrity` MUST NOT be argument-free-and-unbounded. It MUST accept an **optional scope** — a record type, or a collection — and MUST enforce: **500 findings per category** (reported as clamped, with the count that would have been returned and the scope argument that narrows it) and **100,000 notes swept** (refused, naming the collection, with the scoped form as the remedy). Unscoped is permitted and is the common case; it is the *unbounded* part that is not. *Revision 3 called this "an argument-free whole-vault integrity sweep" and gave it no bound, which would have made it the most expensive operation in this specification and the only one with no stated limit — in a document whose motivating evidence is unbounded operations returning silently wrong answers. The unboundedness is **inherited** from `knowledge_graph`'s uncapped sweeps, not introduced here; inheriting it into a tool advertised as bounded would be worse than shipping one.*
- **FR-076** `vault_find` MUST accept `near` (a note) with `hops` (1..2) and MUST **compose** it with `words`, `type` and `filter` in a single call. A `near` query MUST NOT bypass, weaken or replace any filter supplied alongside it.
- **FR-076a** **NEW, revision 4 (ADR-068 D15.3). `kind: task` MUST be served from an INDEXED checkbox row, not a walk.** Revision 3 listed `kind: task` in `vault_find`'s parameter table and specified no mechanism, which was an absorption claim with nothing behind it. Two facts make the mechanism non-obvious and both are verified: `indexDoc.Kind` only ever holds `note` or `attachment` (`pkg/knowledge/scan.go:45,48`), so a `kind` filter over today's index selects nothing; and `knowledge_tasks` is not a record query at all — it walks the collection with `WalkContained` (`pkg/knowledge/authoring_tools.go:1398`), reads each file (`:1420`), matches `^[ \t]*[-*+][ \t]+\[([ xX])\][ \t]*(.*)$` per line, bounds itself at `TasksMaxFiles = 5000` (`:1246`, clamp reported at `:1420-1425`), and returns **many rows per file**. Therefore:
  - **A checkbox MUST be indexed as its own row** in the properties index, carrying `path`, `line`, `status` (`open` / `done`), `text` and the `source_hash` FR-020c requires — the same shape every other row has, so freshness, bounds, pagination and rendering all apply unchanged.
  - **FR-124's one-row-is-one-real-file rule is AMENDED, narrowly and explicitly:** a row is one real *thing at a path* — a note, or a checkbox line within one. A task row MUST render with its line number so a reader cannot mistake it for the note. *(This is a genuine amendment and is marked as one rather than absorbed silently.)*
  - **The regex walk MUST NOT survive.** `TasksMaxFiles = 5000` is a bound on *reading*, and it exists only because the walk re-reads every file on every call. FR-063..FR-066 replace it.
  - **The cost is stated:** checkbox extraction joins the indexing pass, so indexing does slightly more work per note. That is the trade — a per-index cost paid once against a per-query cost paid every time, over a corpus FR-075a caps at 100,000 notes. **Nobody has measured that indexing cost** (ADR-068 §6); W2 measures it.
- **FR-077** Every mutating call MUST emit an audit entry named for its tool — `vault.edit`, `vault.restructure` **or `vault.configure`** — carrying the operation, agent, workspace, path and outcome. The operation appears in the audit record because it is known **after** the call; it MUST NOT be presented anywhere as a policy lever, because FR-070c establishes it is not one.
- **FR-078** **CORRECTED, revision 4.** The **catalog size** MUST fall: after W5 the static builtin catalog contains **six** `vault_*` names and **zero** `knowledge_*` names, taking `allStaticToolNames` from **98** entries to **95**. *Revision 3 said 102 → 98. **102 was a miscount**, corrected in ADR-068 D15.0 and re-counted independently here: the composite literal at `pkg/coreagent/core.go:358-482` holds **98 quoted identifiers, 98 unique, 9 of them `knowledge_*`**. So 98 − 9 + 6 = **95**. The comparison that matters is against the shape this replaces: nine `record_*` **alongside** nine `knowledge_*` would have been 107, i.e. eighteen definitions on one subsystem.*
- **FR-079** Each of the **six** tool **descriptions** MUST fit a budget of ~150 tokens, and each MUST **name the widest operation it grants** (FR-070c) rather than the most common one — an operator reading `vault_edit` will not infer `replace_body` from the name. Operation detail belongs in **parameter descriptions and error messages**, which are paid only when used; a tool description is paid on every turn by every agent that holds the tool, whether or not it is called.
- **FR-080** Every one of the **six** `vault_*` tools MUST have an explicit, literal, wildcard-free policy entry for **every** seeded agent — all ten `coreagent.SeedConfig` creates (`mia`, `jim`, `ava`, `ray`, `worker`, `planner`, `explorer`, `researcher`, `judge`, `plansupervisor`), not only the four base agents. `worker`'s map is sparse, and in a sparse map **absence grants**.
- **FR-080a** **EXTENDED, revision 4.** The seeded posture MUST be: `vault_describe` / `vault_find` / `vault_read` **allow** for Mia, Jim, Ava and Ray and **deny** for workers and the rest; `vault_edit` **ask** for Mia and Ray, **allow** for Jim and Ava, **deny** elsewhere; `vault_restructure` **ask** for all four base agents and **deny** elsewhere; **`vault_configure` ask for all four base agents and deny elsewhere**. Reads are `allow` roster-wide because a prompt in front of a read that `read_file` already permits protects nothing and trains the operator to click through the prompts that do.
- **FR-080b** **NEW, revision 4.** Workers are **`deny` on all six**, reads included, and the reasoning MUST be recorded rather than assumed: the reads-are-`allow` argument turns on `allow` versus `ask` and is about **prompting**, not **granting**. `deny` removes the capability instead of removing a prompt. Workers are delegation-only executors whose task comes from a parent that has already done the vault reading, so granting them the vault surface widens what a delegated sub-turn can reach for no gain in what it can accomplish. **The cost is real and is accepted:** a worker that genuinely needs a note reaches for `read_file`, and that read leaves the audited boundary. The remedy is the one available for every seed — an operator flips it. This is a default, not a wall.
- **FR-083** **WIDENED, revision 4.** `vault_edit`, `vault_restructure` and **`vault_configure`** MUST be **independently settable**, and a test MUST prove **all three** policies are independently settable — specifically that an operator can permit editing while forbidding restructuring **and** forbid schema authoring while permitting both. **This fixes a live defect:** today `knowledge_rename` and `knowledge_move` sit in the same `ask` bucket as `knowledge_append_section` (`pkg/coreagent/core.go:800-808`, `pkg/config/defaults.go:637-646`), despite the first two rewriting inbound wikilinks across the whole vault and the third appending to one file. **And revision 4 states the defect more strongly, because it is worse than revision 3 said:** the same-`ask`-bucket claim holds for **Mia** (`pkg/coreagent/core.go:1056-1058`) and **Ray** (`:1149-1151`) only. For **Ava** (`:976-978`), **Jim** (`:1296-1298`) and the **global ceiling** (`pkg/config/defaults.go:644-646`) all three are **`allow`** — vault-wide restructuring outright permitted with no prompt at all. So the defect is not "the prompt does not distinguish"; it is **"for two of the four base agents there is no prompt"**, and FR-080a is therefore a **tightening** for Ava and Jim, not a re-labelling.
- **FR-084** Every retired `knowledge_*` name MUST be removed from the catalog (`pkg/coreagent/core.go:475-482`), from the global ceiling (`pkg/config/defaults.go:637-646`) and from every per-agent seed in the same change. A name left behind in a seed map is a policy entry for a tool that no longer exists, which is a coverage gap wearing a valid-looking entry.
- **FR-081** A test MUST assert **zero repaired pairs** on a fresh install — not zero gaps after repair.
- **FR-082** The global tool-policy ceiling for every `vault_*` tool MUST be stated explicitly in the seed (`pkg/config/defaults.go`). Repair backfills a *missing agent entry* to `deny`; what can silently grant is the **global ceiling**, which the seed sets per tool. Revision 1's rationale ("absence grants in a sparse map") named the wrong mechanism.
- **FR-090** Every wire type MUST be defined in `contracts/` before Go or TS code exists: the record-model types `RecordSchema`, `RecordType`, `PropertyDef`, `RecordQueryRequest`, `RecordQueryResponse`, `RecordWriteRequest`, `RelationWriteRequest`, `ViewDef`, `ValidationReport`, **plus the tool envelopes** `VaultDescribeResponse`, `VaultFindRequest`/`VaultFindResponse`, `VaultReadResponse` (carrying the version token FR-074 requires), `VaultEditRequest`, `VaultRestructureRequest`, **`VaultConfigureRequest`** (revision 4, ADR-068 D19), and the index-state snapshot FR-020f requires — which MUST reuse the schema of the existing `knowledge_index_progress` frame rather than declaring a parallel one.
- **FR-091** The completeness verdict and problem list MUST be required fields in the response schema. A client MUST NOT be able to receive records without also receiving the completeness verdict.
- **FR-092** FR-120's compact rendering MUST NOT weaken FR-090. The wire type stays contract-defined, generated into `pkg/api/generated/` and `src/lib/api/generated/`, and verified by `make verify-contracts`; the text the model reads is a **projection of that validated object** at the tool-result boundary. These are two surfaces and only one of them changes.

### Retrieval and ranking (ADR-068 D21)

- **FR-110** **The scoring model MUST be set to BM25 explicitly.** `DefaultScoringModel = TFIDFScoring` (`bleve_index_api@v1.4.1/indexing_options.go:37`) and `ScoringModel` is assigned **nowhere** in `pkg/` — a repository-wide grep returns zero assignments — so every vault search, every memory-room recall and every long-term-memory query ranks with TF-IDF today. Seven doc comments across three packages claim otherwise (`pkg/knowledge/index.go:164`, `index.go:1062`, `pkg/memrooms/index/index.go:19`, `index.go:249-250`, `pkg/agent/memory.go:620`, `pkg/agent/retro_bm25.go:14`, `retro_bm25.go:24`). The last two matter beyond hygiene: `retro_bm25.go` hand-rolls BM25 with `k1=1.2, b=0.75` **specifically so retrospective ranking is commensurate with bleve's**, and bleve is not producing BM25 scores at all. The fix is one line and it **changes rankings**, so it ships inside W2 with the fielded-indexing work it affects — not as a detached one-liner.
- **FR-110a** The stale doc comments above MUST be corrected in the same change. A comment asserting a scoring model the code does not set is how this defect survived.
- **FR-111** The index MUST hold **distinct fields** — title, name, headings, property keys, property values, body — rather than one flattened body. **Frontmatter MUST be stripped from the body field.** There is no frontmatter-stripping step in the indexing path today, so `status: prospect` enters the body as the loose tokens `status` and `prospect`; a search for "prospect" then returns the note and reports `Complete: true`, which is the system confidently answering a question it cannot represent. `indexDoc` is a closed five-field struct (`pkg/knowledge/index.go:583-589`), so this is green field — there is nothing to migrate away from.
- **FR-112** Ranking MUST fuse four signals with **Reciprocal Rank Fusion**: BM25 over weighted fields (BM25F-style, title and name above body), exact/prefix name match, recency, and backlink degree. RRF operates on **ranks, not scores**, so no cross-signal normalisation is required — normalising a BM25 score against a recency score against a degree count is a tuning problem with no principled answer, and RRF removes the question instead of answering it badly.
- **FR-113** **The fusion MUST ship behind a measured comparison against plain BM25.** This signal mix is our own composition, not a benchmarked result: BM25F and RRF are each well established individually; *this* combination over vault data has not been measured by us and we have not found it measured. W2 MUST report the comparison; a fusion that does not beat plain BM25 on the fixture corpus MUST NOT ship as the default.
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
- **FR-125** Every total MUST state its scope — `sum(arr) = GBP 465,000.00 over 5 of 12 rows — GBP only`. A bare number MUST never be returned.
- **FR-126** Every response MUST end in **addressable next actions**: at least one concrete call the caller can make next, with its arguments filled in. In an agentic loop each response is a prompt for the next call; a response that terminates in data terminates the loop.
- **FR-127** **MEANING CHANGED, revision 4 (ADR-068 D22.7): the unit is BYTES, not tokens.** Budgets: **~200–320 bytes per hit**, **~4,000 bytes** per response by default, **16,000 bytes hard cap**; `detail: minimal` renders **~80 bytes per hit**. Truncation to meet a budget MUST be stated in the header (FR-121's line), never applied silently. *Previously ~50–80 tokens/hit, ~1,000 default, 4,000 hard cap. **The figures are the same intent at a conservative ~4 bytes/token; only the enforceable unit changed.** The reason is FR-116: three notions of a term already coexist in this tree, none of them the serving model's, so a token cap enforced with any tokenizer we own would be wrong by an unknown margin in an unknown direction on every provider, and would silently change meaning whenever a provider changed tokenizer. Bytes are exact, provider-independent, and enforceable at the point of truncation. This is a budget for **rendering**, not an accounting of what the model is billed.*
- **FR-127a** **The response budget and the page-size cap still conflict, and the budget still wins.** *(Revision 3 raised this against ADR-068 revision 5's token figures; revision 6 changed the unit but not the arithmetic, so the requirement stands, restated in bytes.)* FR-063 permits a page of **200 results** while FR-127 caps a response at **16,000 bytes**; at ~200–320 bytes per hit a 200-row page is **40,000–64,000 bytes**, so the two bounds are not simultaneously satisfiable — and the default page of 50 reaches the cap at the top of the per-hit range. The system MUST therefore degrade in a stated order and **report every step of it in the header**: render at `standard` until the budget is reached, then drop the remaining rows to `minimal`, then stop and page. `limit` is a bound on rows **selected**, never a promise about rows **rendered**. A response that silently returns fewer rows than the budget forced is the truncation failure this specification exists to remove, committed by the renderer instead of the index.
- **FR-127b** **RESOLVED, revision 4.** The unit is **bytes of the rendered UTF-8 response**, it is named here, and it is the unit the tests measure. *Revision 3 required only that the unit be named, and flagged A-8 because ADR-068 revision 5 left it unnamed — budgets enforced against an unnamed unit are decorative. **ADR-068 revision 6 resolves it in this spec's favour** and for this spec's stated reason. A-8 is closed.* One consequence is normative and easy to lose: **FR-079's and FR-128's ~150-token description budget stays in TOKENS**, because it is a design guideline for a human writing prose and is never enforced at runtime. Runtime enforcement is bytes; authoring guidance is tokens; a test MUST NOT conflate them.
- **FR-128** Each tool description MUST fit ~150 tokens (FR-079). **Six** tools at that budget is **~900 tokens** of permanent per-turn context; the nine `record_*` plus nine `knowledge_*` surface this replaces would have been ~2,700. *(The sixth tool's whole standing price is those ~150 tokens — ADR-068 D22.8. Tool **count** costs selection accuracy; tool **descriptions** cost tokens on every turn forever, for every agent holding the tool, whether or not it is ever called. `vault_configure` is selected only when an agent is authoring a type or a view, an intent distinct enough that it competes with nothing — the tools that hurt selection are near-synonyms.)*

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

The mandatory cheap first call. An agent that has not called it is guessing at property names,
and a guessed property name is the failure FR-024 exists to prevent.

| Parameter | Type | Default | Meaning |
|---|---|---|---|
| `collection` | string | all in scope | Narrow to one collection. Unknown name → refusal listing the collections in scope. |
| `record_type` | string | — | Return the full property table for one type only. |
| `include` | list of `types \| views \| templates \| index` | all | Trim the response. |
| `check_integrity` | bool | `false` | Run the integrity sweep (FR-075). Scoped by `collection` / `record_type` when either is given (FR-075a). |
| `detail` | `minimal \| standard` | `standard` | `minimal` omits property descriptions and enum value lists. |

**Response sections, in order:** index freshness → collections → record types (name, label, id
prefix, property table: name, type, arity, required, enum values in declared order) → saved
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
| `sort` | list of `{property, direction}` | relevance | Enum sorts by declared position (FR-010). |
| `select` | list of property names | schema order | Columns to render. |
| `aggregate` | list of `{op, property}`; `op` ∈ `count, sum, min, max` | — | Scoped totals only (FR-123). |
| `explain` | bool | `false` | Report the plan; evaluate nothing (FR-073). |
| `limit` | int | `50` | Clamped at 200, clamp reported (FR-063). |
| `cursor` | opaque string | — | An unhonourable cursor is an error, never a silent restart. |
| `detail` | `minimal \| standard` | `standard` | `minimal` ≈ 80 bytes/hit (FR-127). |

**Filter shape** — a tree of `{all: [...]}`, `{any: [...]}`, `{not: {...}}` over leaves
`{property, op, value}`, with `op` ∈ `is, is_not, lt, lte, gt, gte, contains, is_absent,
is_present`. No text query language exists and none is accepted (FR-022).

**Normative refusal wording.** These strings are contract, not illustration; a test asserts them.

| Condition | Message |
|---|---|
| Unknown property | `unknown property 'ownr' on record type 'company'; declared: name, status, segment, owner, website, arr` |
| Unknown enum value | `'Won' is not a value of deal.status; permitted, in order: open, won, lost` |
| Equality on a `many` property (R-13) | `segment holds many values; use contains` |
| Third hop | `hops=3 exceeds the limit of 2; run a second vault_find from one of these results` |
| Candidate cap | `this query selects 24,180 records; the limit is 10,000 — add a filter on status (7 values) or a narrower type` |
| Aggregate over a refused set | `no total is returned over a refused candidate set` |
| Cross-currency total | `2 currencies present (GBP, USD); no combined total is returned` |
| Stale cursor | `that cursor was issued against index generation 8802; the index is now at 8814 — re-run the query` |
| Stale record (FR-020c) | `DEAL-0117: the properties index and the note on disk disagree (indexed 3f9a…, on disk 71c4…) — this record is being re-indexed; re-run to confirm` |
| No manifest entry (FR-020c) | `DEAL-0221: no indexed note at Deals/old.md — the row is orphaned; run vault_describe check_integrity` |
| Typed query on a SQLite-less build (FR-020h) | `typed filters are unavailable on linux/mipsle: this build has no properties index. Plain-word search and vault_read still work` |

- **AC-F1** — every refusal above names the remedy in the same string. A refusal that states only
  what went wrong fails this criterion.
- **AC-F2** — `near` composed with `words` and `filter` returns the intersection. A test asserts a
  record inside the hop radius but failing the filter is **absent**, and one matching the filter
  but outside the radius is **absent** (FR-076).
- **AC-F3** — `explain: true` performs no evaluation: a corpus mutation between two identical
  `explain` calls changes nothing in the response except index generation.
- **AC-F4** — an out-of-scope vault yields `COMPLETE: yes — 0 records` and no other signal
  (FR-062).
- **AC-F5** — a record whose row `source_hash` differs from `ManifestEntry.Hash` is returned **with**
  `COMPLETE: no`, named in `PROBLEMS` with staleness as the reason, in **both** divergence
  directions (FR-020c). A record with no manifest entry is flagged the same way.
- **AC-F6** — on a build without a properties index, a typed filter is **refused by name**; a test
  asserts the response is not an empty success (FR-020h).
- **AC-F7** — `kind: task` returns rows carrying `path`, `line`, `status` and `text`, each
  rendered with its line number, and no collection walk occurs (FR-076a).

### 4.1.3 `vault_read` — READ, a note or one section of one

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

Writes **only** `path` (FR-070b). Every op on an existing file requires `expect_version`.

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

- **AC-E1** — after any successful `vault_edit`, exactly one file has a changed mtime and hash.
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

The only tool permitted to change a file the caller did not name.

| `op` | Additional parameters | Cascade |
|---|---|---|
| `rename` | `path`, `new_name`, `expect_version` | Inbound wikilinks in N notes are rewritten (ADR-067 D10). |
| `move` | `path`, `dest`, `expect_version` | Same. |
| `trash` | `path`, `expect_version` | Inbound links **cannot** be repaired — FR-048. |

*(Revision 4: `edit_record_type` and `delete_record_type` are **removed from this tool** and are
`vault_configure` operations — FR-017. They cascade in **meaning** (C-B), not in bytes (C-A), and
ADR-068 D23.4 records the asymmetry: a schema change writes **one** file, so reverting that file
undoes it, which is not true of trash. Grouping the two would have implied a severity schema
authoring does not have.)*

**Trash stays in this tier even though it is a soft delete.** A recoverable bin fixes the trashed
note's own recoverability; it does nothing for the N notes whose links just broke. Recoverability
and blast radius are different axes, and only the second one decides the tier.

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

**Normative refusal and report wording.** These strings are contract, not illustration; a test
asserts them.

| Condition | Message |
|---|---|
| Type already exists (FR-016) | `record type 'company' is already declared in .omnipus-vault/records/company.yaml; use op=edit_record_type to change it` |
| Type does not exist (FR-017) | `no record type 'compnay' is declared; declared types: company, deal, meeting, person` |
| Schema missing `schema_version` (FR-002) | `schema for 'company' has no schema_version; add schema_version: 1` |
| Two files declare one type (FR-003) | `record type 'deal' is declared in .omnipus-vault/records/deal.yaml and .omnipus-vault/records/deals.yaml; delete one` |
| Unknown property type (FR-004) | `property 'closed' declares type 'boolean'; permitted: text, enum, relation, date, number, money, person` |
| Enum with no declared order (FR-010) | `enum property 'status' must declare its values in order; sorting follows declared position, not spelling` |
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

**The call.** *"Open Acme deals over £50k, and anything within two link-hops of Acme that
mentions pricing."*

```json
{
  "words": "pricing",
  "type": "deal",
  "filter": { "all": [ { "property": "status", "op": "is", "value": "open" },
                       { "property": "arr", "op": "gte", "value": 50000 } ] },
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
QUERY: type=deal  words="pricing"  filter=(status is open AND arr >= 50000)  near=[[Acme Ltd]] hops=2  join=company  sort=arr desc  limit=50
INDEX: 12 of 12 returned records verified fresh (source_hash matched); collection gen 8814

DEAL-0117  Acme renewal FY27       status open   arr GBP 180,000.00   company [[Acme Ltd]]: status active
DEAL-0121  Acme expansion EU       status open   arr GBP  95,000.00   company [[Acme Ltd]]: status active
DEAL-0134  Acme platform add-on    status open   arr GBP  70,000.00   company [[Acme Ltd]]: status active
DEAL-0140  Acme support uplift     status open   arr GBP  62,000.00   company [[Acme Ltd]]: status active
DEAL-0102  Northwind pricing pilot status open   arr GBP  58,000.00   company [[Northwind]]: status prospect
DEAL-0155  Acme data migration     status open   arr USD 120,000.00   company [[Acme Ltd]]: status active
DEAL-0161  Acme training package   status open   arr USD  88,000.00   company [[Acme Ltd]]: status active
… 5 more rows

TOTALS: sum(arr) = GBP 465,000.00 over 5 of 12 rows — GBP only; 7 rows are USD and are not included

PROBLEMS (3)
  DEAL-0052  arr is '50k' where a number is required — write 50000
  DEAL-0088  company is text "Acme Ltd", not a relation — write company: "[[Acme Ltd]]"
  DEAL-0093  status is 'Won'; deal.status permits, in order: open, won, lost — write 'won'

NEXT
  page       vault_find cursor="c2FnZTI"
  narrow     vault_find type=deal filter=(status is open AND currency is GBP)
  widen      vault_find near="Companies/Acme Ltd.md" hops=2 words="pricing" type=<any>
  fix        vault_read path="Deals/DEAL-0052.md"   then vault_edit set_property arr=50000
```

| Line | Required by | Why it is there and not elsewhere |
|---|---|---|
| `COMPLETE:` first | FR-121 | The verdict precedes the evidence, so no conclusion forms before the caveat arrives. |
| `QUERY:` echo | FR-122 | Shows the query **as executed** — a clamp or default is visible without a second call. |
| `INDEX:` freshness | FR-020c | Freshness is **per returned record**, not per index (ADR-068 D16.5): each row's `source_hash` is compared against `ManifestEntry.Hash`. A record that fails moves to `PROBLEMS` and the verdict becomes `no`. The count is an assertion, not decoration — and per FR-020c1 it covers **what the query returned, not what it did not**. |
| Rows | FR-127 | ~200–320 **bytes** each; the row count shown is what the budget allowed, and the shortfall is in the header. |
| `company [[Acme Ltd]]: status active` | FR-124 | Borrowed, visibly. It is not a `deal` property and must never render as one. |
| `TOTALS:` | FR-125, FR-014 | Scoped in the same sentence as the number. The USD rows are counted and excluded, not dropped. |
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

- **AC-P1** — a response whose `COMPLETE` line reads `no` and whose `PROBLEMS` block is empty is
  a defect: the reason is either named or the verdict is wrong.
- **AC-P2** — the four blocks appear in the order `header → rows → totals → problems → next`, and
  a test asserts the order rather than the presence.
- **AC-P3** — rendering is a **projection**: the same wire object rendered twice is
  byte-identical, and every fact in the text is present in the wire object (FR-120).
- **AC-P4** — the response is measured in **bytes of rendered UTF-8** and the measurement in the
  test is the same unit the implementation enforces (FR-127b). A test that counts tokens fails
  this criterion even if it passes.
- **AC-P5** — a `vault_configure` response renders its cascade-in-meaning counts in the same
  block position `vault_restructure` renders its cascade-in-bytes counts: after the rows, before
  `NEXT` (§4.1.6, AC-P2's order).

---

## 5. Success criteria

- **SC-001** The two-hop question from ADR-068 §1.2 is answered by one `vault_find` call with no hand-maintained state and no regular expression.
- **SC-002** For a corpus of 63 records where 22 hold non-numeric values in a numeric property, an aggregate names all 22 and returns no combined figure.
- **SC-003** A query filtering on a mistyped property name is rejected with valid names listed; zero such queries return an empty result set.
- **SC-004** Writing one property into a 200-line note leaves the file byte-identical outside the patched span, across a 50-file fixture corpus.
- **SC-005** **CORRECTED, revision 3.** 1,000 records created concurrently across two POSIX processes yield 1,000 **distinct** identifiers. **Gaps are permitted; a repeat is a failure.** *Revision 2 demanded "zero sequence gaps", which directly contradicts FR-038 and ADR-068 D7.1: deleting a record burns its identifier permanently, so a gap is the correct outcome and reconciling to max to close it would make an existing relation resolve to a different record. This was a specification defect, not a wording preference.*
- **SC-005a** Deleting the highest-numbered record and creating a new one yields an identifier **above** the deleted one, never equal to it.
- **SC-006** An agent in workspace A retrieves zero records from a vault mounted only into workspace B, and cannot distinguish it from an empty vault.
- **SC-007** **CORRECTED, revision 4.** *(No latency target is stated. The spike measured the **bleve-plus-Go design this specification did not take**; quoting its numbers as targets for the two-index design would repeat revision 1's error in a new costume. Targets arrive when W1 has the SQLite path measured.)* **The 64 MB budget is a TARGET, not an inherited property.** Revision 3 wrote that ADR-067's < 64 MB steady-state RSS holds and "the properties index lives inside it". That budget was measured for **bleve alone** — idle 12.9–15.1 MB, 23.6–24.0 MB streamed at the cap (spike §5.1, §5.3) — and the two-index design keeps all of it and **adds** SQLite's page cache, its temp b-trees for `GROUP BY`/`ORDER BY`, and its connection state, none of which is measured. **W1 exit criterion:** both indexes, idle and at the 10,000-record cap, inside 64 MB, measured on **Linux as well as macOS** (ADR-068 D16.4 item 4).
- **SC-009** Zero tool-policy pairs are repaired on a fresh install, across all ten seeded agents.
- **SC-010** A `.base` file containing one unsupported expression imports — **via the operator/CLI one-shot** — with that expression reported verbatim and the rest translated.
- **SC-011** **CORRECTED, revision 4.** The static builtin catalog contains exactly **six** `vault_*` names and zero `knowledge_*` names, and `allStaticToolNames` has **95** entries. *(Revision 3 said five and 98. 98 is the count **today**, before any change — re-counted at `pkg/coreagent/core.go:358-482`: 98 quoted identifiers, 98 unique, 9 `knowledge_*`. After the swap: 98 − 9 + 6 = 95.)*
- **SC-012** **WIDENED, revision 4.** All three write policies are independently settable, proven by test in one session: `vault_edit: allow` + `vault_restructure: deny` permits a property write and refuses a rename; `vault_edit: allow` + `vault_configure: deny` permits a property write and refuses `create_record_type`.
- **SC-013** Every successful `vault_edit` changes exactly one file on disk.
- **SC-014** Deleting the properties index and reopening the vault reproduces byte-identical query results for a 30-query fixture suite.
- **SC-015** **CORRECTED, revision 4 — it tests DIVERGENCE, not rebuild, and in both directions.** A record row is written; the note is then modified and re-indexed into **bleve only** (the SQLite write suppressed); a `vault_find` returning that record reports `COMPLETE: no` with the record named and staleness given as the reason. The symmetric case — SQLite updated, bleve not — is asserted the same way. *Revision 3's criterion could be satisfied by a generation counter that does not exist; ADR-068 revision 5's W1 exit criterion was "delete the properties index and reopen rebuilds it with identical results", **which would have passed with the mitigation entirely absent**. SC-014 keeps the rebuild criterion because it tests a different property — FR-020a's disposability.*
- **SC-016** `ScoringModel` is asserted to be BM25 by test, and the seven stale doc comments naming BM25 are corrected in the same change.
- **SC-017** A field query on a frontmatter property key returns the records holding it — a query that is not expressible at all today.
- **SC-018** The FR-112 fusion is compared against plain BM25 on the fixture corpus and the comparison is recorded; the same filter returns the same **set** under both (AC-8.6).
- **SC-019** An agent obtains a version token via `vault_read` and completes a write with zero failed writes in between.
- **SC-020** A client mounting the collection panel after indexing completed renders the completed state, and it matches the freshness `vault_find` reports for the same collection.
- **SC-021** A `vault_find` response over a partial answer places its completeness verdict on line 1, and a test asserts block order `header → rows → totals → problems → next`.
- **SC-022** **NEW, revision 4.** Creating a record type on a vault holding 47 notes that already declare that type reports **47** converted and names the 6 that newly fail validation. A response reading only "type created" fails (AC-C1).
- **SC-023** **NEW, revision 4.** On a build without the properties index, every typed filter, join, grouping and aggregation call **refuses by name**, and zero of them return an empty success (FR-020h, AC-F6).
- **SC-024** **NEW, revision 4.** The truth table of §8 runs against the **compiled query path** — schema → filter object → compiled query → store — and a mutation to the compiler (removing the `IS NULL` arm of a negation, or swapping `instr()` for `LIKE`) makes it fail (AC-8.4, AC-8.7).
- **SC-025** **NEW, revision 4.** Response budgets are asserted in **bytes of rendered UTF-8**, by the same measurement the implementation enforces (FR-127b, AC-P4).
- **SC-026** **NEW, revision 4.** An unscoped `check_integrity` over a collection above 100,000 notes is refused naming the collection and the scoped remedy; a category above 500 findings reports the clamp with the count that would have been returned (FR-075a).

---

## 6. Traceability

| FR | Story | Scenario | Test |
|---|---|---|---|
| FR-001..003 | US-1 | 1.5 | `TestSchema_LoadAndReject` |
| FR-004, 009 | US-1 | 1.2 | `TestSchema_TypesAreScopedToRecordType` |
| FR-006 | US-1 | 1.4 | `TestValidate_ArityViolationIsReported` |
| FR-007, 008 | US-2 | edge | `TestFilter_AbsentIsDistinctAndIncludedByNegation` |
| FR-010, 011 | US-1 | 1.3 | `TestEnum_OrderedAndClosed` |
| FR-012..014 | US-2 | 2.3 | `TestMoney_RefusesCrossCurrencySum` |
| FR-016..019a | US-12 | 12.1–12.5 | `TestSchemaAuthoring_LivesInVaultConfigure` |
| FR-018a | US-12 | 12.4 | `TestConfigure_DeclaresNoVersionToken` |
| FR-020, 020a, 021 | US-2 | — | `TestPropsIndex_RebuildIsResultIdentical`; `TestIndex_PropsRoundTripsExactDecimal` — a money value survives the index unchanged; a float64 path fails it |
| FR-020c, 020c1, 020g | US-13 | 13.2 | `TestIndexes_SourceHashDivergenceIsIncomplete` |
| FR-020h | — | — | `TestRecords_RefuseByNameWithoutSQLite` |
| FR-020d, 020e | — | — | `TestIndex_StaleFormatIsRebuiltNotOpened`; `TestIndex_PersistedMappingAsserted` |
| FR-020f | US-13 | 13.1 | `TestIndexState_SnapshotMatchesLiveFrame` |
| FR-022..024 | US-2 | 2.4 | `TestQuery_UnknownPropertyIsRejectedNotEmpty` |
| FR-025, 026 | US-2 | 2.2 | `TestQuery_ProblemsAreNamedNotDropped` |
| FR-027..029 | US-2 | 2.6 | `TestGroup_MultiValueAppearsInEveryGroup` |
| FR-030..035 | US-3 | 3.1–3.4 | `TestRelation_InverseIsDerivedAndSurvivesRename` |
| FR-036..039 | US-5 | 5.1–5.4 | `TestID_ConcurrentAllocationIsCollisionFree` |
| FR-040..042 | US-4 | 4.1, 4.2 | `TestWrite_ByteIdenticalOutsidePatchedSpan` |
| FR-043, 044 | US-4 | 4.3, 4.4 | `TestWrite_StaleTokenRefusedAndAudited` |
| FR-045 | US-4 | — | `TestRelate_ReplaceMustBeNamed` |
| FR-046 | — | — | `TestDerived_NeverWrittenToFrontmatter` |
| FR-050..053 | US-6 | 6.1–6.4 | `TestInteraction_ExclusionRules` |
| FR-060..062 | US-8 | 8.1, 8.2 | `TestScope_CrossWorkspaceReturnsEmpty` |
| FR-063..066b | US-2 | 2.5 | `TestBounds_RefusalNotTruncation`; `TestBounds_CandidateCountedBeforeRetrieval`; `TestBounds_PeakRSSAtCap` |
| FR-067 | — | — | `TestWritePath_RateLimited` |
| FR-036a | US-1 | — | `TestCreate_WritesNoteAndSeqAndNothingElse` |
| FR-047 | US-4 | — | `TestReplaceBody_AmbiguousAnchorIsRefused` |
| FR-048, 049 | US-4 | — | `TestTrash_ConventionAndUnrepairableLinksReported` |
| FR-070, 070a, 078 | — | — | `TestTools_ExactlySixVaultToolsAndNoKnowledgeNames` |
| FR-070b, 070c, 071 | — | — | `TestTools_EditWritesOnlyNamedFile`; `TestTools_NamesHaveNoDots` |
| FR-072, 120..128 | US-11 | 11.1–11.4 | `TestRender_CompactTextContract`; `TestRender_BudgetIsMeasuredInBytes` |
| FR-073 | US-2 | — | `TestFind_ExplainEvaluatesNothing` |
| FR-074 | US-9 | 9.1–9.3 | `TestRead_ReturnsUsableVersionToken` |
| FR-075 | US-5 | 5.3 | `TestDescribe_CheckIntegrityNamesBothPaths`; `TestDescribe_ReportsOrdinaryBrokenWikilinks` |
| FR-075a | — | — | `TestDescribe_CheckIntegrityIsBounded` |
| FR-076 | US-3 | — | `TestFind_NearComposesWithFilters` |
| FR-076a | US-10 | — | `TestFind_TasksAreIndexedRowsNotAWalk` |
| FR-077 | US-4 | 4.4 | `TestAudit_VaultEditAndRestructureCarryOperation` |
| FR-079, 128 | — | — | `TestTools_DescriptionTokenBudget` |
| FR-080, 080a, 081, 082 | — | — | `TestToolPolicy_ZeroRepairedPairsOnFreshInstall` |
| FR-083, 084 | US-12 | 12.2, 12.4 | `TestToolPolicy_ThreeWriteTiersAreIndependent` |
| FR-110, 110a | US-10 | 10.1 | `TestSearch_ScoringModelIsBM25` |
| FR-111 | US-10 | 10.3 | `TestIndex_FieldedAndFrontmatterStripped` |
| FR-112, 113 | US-10 | 10.4 | `TestRank_FusionComparedAgainstPlainBM25` |
| FR-114, 115 | US-10 | 10.2 | `TestSearch_ZeroHitsReportsVocabularyNotExpansion` |
| FR-116 | — | — | `TestTokenizer_OneNotionOfATerm` |
| FR-117 | — | — | `TestRetrieval_NoEmbeddingDependency` |
| FR-090, 091 | — | — | `TestContract_CompletenessFieldsAreRequired` |
| FR-100..103 | US-7 | 7.1–7.3 | `TestImport_UntranslatedExpressionIsReported`; `TestImport_NotRegisteredAsAgentTool` |

---

## 7. Test plan

Order is unit → integration → e2e; within a level, dependencies first.

| # | Test | Level | Traces |
|---|---|---|---|
| 1 | `TestSchema_LoadAndReject` | unit | FR-001..003 |
| 2 | `TestEnum_OrderedAndClosed` | unit | FR-010, 011 |
| 3 | `TestValidate_ArityViolationIsReported` | unit | FR-006 |
| 4 | `TestMoney_RefusesCrossCurrencySum` | unit | FR-012..014 |
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
| 21 | `TestRecords_PerfAtFiftyThousand` | e2e | *(no threshold until W1 measures the SQLite path — SC-007)* |
| 22 | `TestTools_EditWritesOnlyNamedFile` | integration | FR-070b — snapshots every file's hash, runs each `vault_edit` op, asserts exactly one changed |
| 23 | `TestRead_ReturnsUsableVersionToken` | integration | FR-074 — read → edit with zero intervening failed writes |
| 24 | `TestFind_NearComposesWithFilters` | integration | FR-076 — both negative directions, per AC-F2 |
| 25 | `TestRender_CompactTextContract` | unit | FR-120..127 — diffs against §4.2's literal example, asserts block order, and asserts no JSON document is emitted |
| 26 | `TestSearch_ScoringModelIsBM25` | unit | FR-110 — asserts the assignment exists; a doc-comment claim is not evidence |
| 27 | `TestIndex_FieldedAndFrontmatterStripped` | integration | FR-111 — a field query on a property key returns records; the body field does **not** contain frontmatter tokens |
| 28 | `TestRank_FusionComparedAgainstPlainBM25` | integration | FR-113, AC-8.6 — records the comparison and asserts set-equality under both rankings |
| 29 | `TestSearch_ZeroHitsReportsVocabularyNotExpansion` | integration | FR-114, FR-115 |
| 30 | `TestTokenizer_OneNotionOfATerm` | unit | FR-116 — asserts one shared function, or fails with the recorded decision as the only permitted alternative |
| 31 | `TestPropsIndex_RebuildIsResultIdentical` | integration | FR-020a, SC-014 |
| 32 | `TestIndexes_SourceHashDivergenceIsIncomplete` | integration | FR-020c — **tests divergence, not rebuild, in BOTH directions.** Writes a row, re-indexes the modified note into bleve only (SQLite write suppressed), asserts the returning `vault_find` reports `complete: false` with the record named and staleness as the reason; then the symmetric case. A rebuild-only criterion would pass with the mitigation absent. Also asserts an orphaned row (no manifest entry) and an empty hash are both flagged, never assumed fresh |
| 33 | `TestIndexState_SnapshotMatchesLiveFrame` | integration | FR-020f, FR-020g — a client that never received a frame renders the same state as one that did |
| 34 | `TestReplaceBody_AmbiguousAnchorIsRefused` | unit | FR-047 — both line numbers named, file byte-identical |
| 35 | `TestTrash_ConventionAndUnrepairableLinksReported` | integration | FR-048 |
| 36 | `TestToolPolicy_EditAndRestructureAreIndependent` | integration | FR-083, SC-012 |
| 37 | `TestSchemaAuthoring_LivesInVaultConfigure` | integration | FR-016, FR-017, FR-018 — **REPLACES revision 3's `TestSchemaAuthoring_TierPlacement`, which asserted the wrong placement.** Every schema and view op is refused by `vault_edit` **and** `vault_restructure`, naming `vault_configure`, and accepted by `vault_configure`. Creating a new type over a vault already holding notes of that type asserts the conversion count and the newly-failing records (AC-C1) |
| 38 | `TestTools_DescriptionTokenBudget` | unit | FR-079, FR-128 |
| 39 | `TestFilter_NoLikeInCompiledPath` | unit | AC-8.7 — asserts **zero** occurrences of `LIKE` in the compiled filter path. R-10's case-sensitivity is one careless operator away from being lost and nothing else would report it |
| 40 | `TestConfigure_DeclaresNoVersionToken` | unit | FR-018a, AC-C3 — asserts `expect_version` is absent from the **tool schema**, not merely from the prose |
| 41 | `TestToolPolicy_ThreeWriteTiersAreIndependent` | integration | FR-083, SC-012 — `vault_edit` / `vault_restructure` / `vault_configure` set independently; the `edit: allow, configure: deny` case asserts `create_record_type` is refused |
| 42 | `TestRecords_RefuseByNameWithoutSQLite` | unit | FR-020h, SC-023 — build-tagged; every typed call refuses naming the platform, and none returns an empty success |
| 43 | `TestDescribe_CheckIntegrityIsBounded` | integration | FR-075a, SC-026 — the 500-per-category clamp is reported with the would-be count; the 100,000-note sweep is refused naming the scoped remedy |
| 44 | `TestDescribe_ReportsOrdinaryBrokenWikilinks` | integration | FR-075 — a vault with **no records at all** still produces broken-link and orphan findings. This is the capability `knowledge_graph`'s retirement would otherwise lose |
| 45 | `TestFind_TasksAreIndexedRowsNotAWalk` | integration | FR-076a — task rows carry `path`/`line`/`status`/`text`, render with the line number, and **no collection walk occurs**; a fixture with 6,000 files (above `TasksMaxFiles = 5000`) returns tasks from all of them |
| 46 | `TestRender_BudgetIsMeasuredInBytes` | unit | FR-127, FR-127b, AC-P4 — the assertion counts bytes of rendered UTF-8, and the same function the renderer enforces with |
| 47 | `TestBounds_CandidateCountedBeforeRetrieval` | integration | FR-066a — the count happens before any document is retrieved; a 24,000-candidate query is refused without materialising one row |
| 48 | `TestWritePath_RateLimited` | integration | FR-067 — `vault_edit`, `vault_restructure` and `vault_configure` each 429 with `Retry-After`. **Asserts the limiter is reached at all**, because today no write `Execute` consults one |
| 49 | `TestCreate_WritesNoteAndSeqAndNothingElse` | integration | FR-036a — exactly two paths change on `create`; a third is a failure |

### Test datasets

**DS-1 — property values.** Traces to FR-006, 007, 011, 012.

| Value | Property type | Expected | Traces |
|---|---|---|---|
| `active` | enum(prospect,active) | accepted | 1.3 |
| `Active` | enum(prospect,active) | **rejected** — case-exact | edge |
| absent | enum | absent, not a value | 1.4 |
| `[a, b]` | enum scalar | **rejected** — arity | 1.4 |
| `""` | text | accepted, distinct from absent | edge |
| `349.98` + `SGD` | money | accepted | 2.3 |
| `349.98`, no currency | money | **rejected** | edge |
| `PLACEHOLDER — unknown` | number | **rejected**, record named | 2.2 |
| `2026-13-45` | date | rejected | edge |
| 2^53+1 | number | accepted, exact | edge |

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

| # | Rule |
|---|---|
| **R-1** | A comparison between values of **different declared types** is `false`. Never an error, never a coercion. `"3" > 2` is false because one is text and one is a number. **`integer` and `decimal` are ONE declared type for this rule** *(revision 5)*: an author chooses the storage, not a distinct comparison domain, so `3 = 3.0` is **true** and an `integer` compares with a `decimal` numerically. R-1 separates text from numbers, not int64 from arbitrary precision. |
| **R-2** | A comparison where **either side is absent** is `false`, for every operator except `is absent`. |
| **R-3** | `is absent` is `true` exactly when the property has no value, and `false` otherwise. An empty string, an empty list and a zero are values, not absence. |
| **R-4** | A value present but **not conforming to its declared type** does not compare. It is `false` for every operator **and** the record is added to the query's problem list. Silence here is the defect. |
| **R-5** | **REVISED, revision 5.** `enum` compares by **declared position**, not by spelling: ordering follows the schema's order. Equality resolves a value **case-insensitively** to a declared value; the value it resolves to supplies the ordinal. *(Was "equality is exact-case against the declared set." Operator ruling 3. Resolving `Won` **to** `won` collapses two spellings into one value — it does not create a second, which is the thing D4 forbids. See §8.1's R-5 row for the ASCII-only caveat.)* |
| ~~**R-6**~~ | **RETIRED, revision 5 (operator ruling 1) — `money` is deleted from the type system, so this rule has no subject.** *Was: "`money` compares only within one currency. Across currencies every operator is `false` and the query reports the currencies present."* **The number is retired rather than reused**, so an older test name, commit or review that says "R-6" resolves here rather than to a different rule wearing the number. Removed with it: R-6's §8.1 defeat row, every R-6 cell of AC-8.1's generated table, `TestMoney_RefusesCrossCurrencySum`, and the "cross-currency `MIN`/`MAX`/`AVG`/`ORDER BY`" gap the grill raised as C-3 I-4 and C-10 — all four had `money` as their only subject. **The rule count is therefore twelve declared rules of which one is retired**, not thirteen; §8.1 and AC-8.1 are restated accordingly. |
| **R-7** | **REVISED, revision 5 — the rule is unchanged, its storage is now specified, because it had no defeat and SQLite violates it four ways (§8.1a).** `date` compares as an instant. A date and a date-time are the same declared type and compare directly. **A `date` MUST be stored as a signed integer epoch with a declared precision, never as text** — see §8.1's R-7 row. |
| **R-8** | `relation` compares by **target identity**, never by display text. Two links resolving to the same record are equal regardless of spelling. **Revision 5 splits this into two columns with two collations, deliberately:** the **path/name** side resolves **case-insensitively** (operator ruling 3, and it removes a real macOS-vs-Linux divergence in wikilink resolution); the **identifier** side is matched **exactly, on a `BINARY` column**, because folding it would make `CO-0142` and `co-0142` one key and two legitimately distinct targets could not coexist — a `UNIQUE constraint failed`, which is loud, but it is a data-loss refusal for a case nobody chose. |
| **R-9** | `contains` on a list is **whole-element** membership. It is never substring matching. **Element equality follows R-10's case rule**, so a list holding `Vendor` contains `vendor`. |
| **R-10** | **REVERSED, revision 5 (operator ruling 3).** `contains` on text is **substring** matching, **case-INSENSITIVE**. *(Was "case-sensitive.")* Equality (`is`, `is_not`) on text is likewise case-insensitive. **The insensitivity is ASCII-only wherever it is delegated to SQLite** — `COLLATE NOCASE`, `LIKE` and `lower()` fold `A`/`a` and fold **nothing** outside ASCII, verified over fourteen non-ASCII pairs (§8.1a §H) — so full Unicode folding requires the Go-computed fold column FR-011a specifies, and **a build without that column MUST NOT claim Unicode-insensitive matching.** |
| **R-11** | **WIDENED, revision 5.** Comparison is **total and never panics**: every type pair × every operator yields a boolean or a reported problem. There is no third outcome. **"Third outcome" is not a synonym for "error", and treating it as one was the defect:** four of SQLite's third outcomes are *silent* — `1/0` → NULL, scalar int64 overflow → REAL, `unixepoch('bad')` → NULL, `SUM` over an empty set → NULL — while `SUM` overflow *is* a loud error for the same arithmetic. All five are enumerated with their defeats in §8.1's R-11 row and §8.1a §D. |
| **R-12** | Every rule above applies **identically** whether the value came from a query literal or from a record. **This rule is itself violated by SQLite and now has its own defeat (§8.1, R-12 row):** comparison affinity converts a TEXT operand only when the other side is a typed column, so `3 = '3'` is **false** between two literals and **true** between a column and a literal — identical values, identical operator, opposite answers depending on provenance. |
| **R-13** | Against a **`many` property, only `contains` and `is absent` are defined.** Equality and ordering (`=`, `!=`, `<`, `<=`, `>`, `>=`) are **not defined** and are reported as a problem naming the remedy — "`segment` holds many values; use `contains`". They are NOT silently treated as membership. *Added 2026-08-25 after the type-system agent surfaced the gap rather than routing around it: `segment != vendor` had no defined answer.* **Why refuse rather than help:** treating `=` as membership is the implicit coercion this design removes everywhere else, and an agent that gets a helpful answer to a malformed query never learns the schema. The refusal names the fix, exactly as FR-024 does for an unknown property. |

**AC-8.1** — the generated table covers every declared type × every declared type × every
operator, plus absent and non-conforming on both sides, and every expected value traces to a
numbered rule above. **Revision 5 adds three obligations to the generator, each closing a hole the
grill found:** every cell is generated in **both provenances** — literal-vs-literal and
column-vs-literal — because R-12's row shows the two disagree; the type axis is the **seven types
of FR-004** (`money` cells are deleted, `integer` and `decimal` cells are added, and `integer × decimal`
is asserted to compare numerically per R-1); and **`many` arity is a third axis**, so the fan-out
case of §8.1a is generated rather than remembered.

**AC-8.2** — a comparator change that requires editing a **rule** is a specification change and
must be argued as one. A change that only regenerates cells is an implementation detail.

**AC-8.3** — `3 > 2` is `true`. Stated explicitly because it is the case that actually failed.

### 8.1 What the storage decision changes here — assessed rule by rule

**Twelve rules are live; R-6 is retired with `money` (operator ruling 1); three of the twelve —
R-5, R-10 and R-11 — change in revision 5, and R-7 and R-12 gain defeats they never had.** The
`vault_*` tool surface and the retrieval decisions still touch none of them, and the reasons are
worth stating rather than assumed:

- **The tool surface changes who calls the comparator, not what it decides.** `vault_find`
  replaces `record_query` as the caller; the filter object it accepts (FR-022) is the same
  structured shape, validated against the schema before evaluation (FR-023). A rule about whether
  `"3" > 2` holds cannot be affected by the name of the tool that asked.
- **Retrieval decides ORDER; the rules decide MEMBERSHIP.** BM25-vs-TF-IDF (FR-110), fielded
  indexing (FR-111) and RRF fusion (FR-112) rank records that already matched. A ranking change
  that altered which records matched would be a defect, and **AC-8.6 asserts it does not.**
- **The tokenizer question (FR-116) still does not reach R-10, and revision 5 has to say why more
  carefully than revision 4 did.** `contains` on text is substring matching over the property
  value, not over the analysed text index. Stemming, stopwords and Unicode segmentation are
  properties of the ranking path; they must not leak into comparison, or `contains "running"` would
  match `run` and the rule would have silently changed. **Case-folding is not a fourth notion of a
  term and must not be mistaken for one:** FR-011a's fold is `strings.ToLower` over the whole value,
  a character-level transform that splits nothing and stems nothing. It is the *only* analysis
  permitted on the comparison path, and FR-116's shared-token-function obligation is unaffected by
  it.
- **R-13 gains a second reason to exist.** It was written because `segment != vendor` had no
  defined answer. Under FR-021 a `many` property lives in a child table, where SQL equality
  against a join **silently behaves as membership** — precisely the implicit coercion R-13 refuses.

**What DOES change is the surface the rules are enforced on, and this is the important part.**
FR-021 moves membership from a Go comparator into SQLite. **SQLite's default semantics contradict
TEN of the twelve live rules**, and every defeat below is a line of a query compiler nobody has
written. None is defeated by choosing SQLite carefully.

> **Corrections in revision 5, and one of them is a count going UP after a review said it should.**
> (a) Revision 4 said "nine of the thirteen" at seven defeat sites. **R-6 is retired with `money`**
> (−1 rule, −1 site) and **R-7, R-12 and a tenth violation the document never had — join fan-out —
> are added** (+3 rules, +3 sites). Net: **ten violated rules of twelve live ones, across eleven
> defeat sites.** (b) `LIKE`'s prohibition and AC-8.7 are **deleted**, not reworded, under operator
> ruling 3. (c) The receipts are re-taken and the engine is named: they were run on the **`sqlite3`
> CLI 3.51.0**, and the engine that will actually run is **`modernc.org/sqlite v1.46.1`, which
> reports `sqlite_version()` = 3.51.2** — verified by opening a database through the driver, not
> assumed. Every claim below re-executed identically on both. *(The grill said the shipped engine
> reports 3.53.3 and that the receipts were two minor versions stale; that figure does not
> reproduce — see the header table. The standard behind the finding is adopted regardless: FR-020i
> asserts the linked version, because affinity and collation are version-sensitive.)*

**Nine of the ten fail in the QUIET direction** — a wrong answer that looks exactly like a right
one, with no error, no empty result and no `complete: false`. Only one arm of R-11 announces
itself, and only by escaping as an error the caller did not ask for; **its other four arms are
silent too, which is the part revision 4 had backwards.** That asymmetry is why this is a
first-class risk rather than an implementation detail.

| Rule | SQLite's own behaviour | What this specification requires instead |
|---|---|---|
| **R-1** | Comparison across storage classes never errors and never yields false: INTEGER and REAL sort **before** TEXT, so `'3' > 2` is **true** | **Two mechanisms, and the Go one is PRIMARY.** *(Revision 5: revision 4 presented "one typed column per declared property" as co-equal, and it does no work on its own — SQLite has affinity, not types. `CREATE TABLE ti(n INTEGER); INSERT INTO ti VALUES ('3abc');` leaves `typeof(n)='text'`, and `'3abc' > 2` is still **true**.)* **(1) The declared-type guard in the compiler, before emission** — a type mismatch is decided in Go and the predicate is never emitted. **(2) A column-level backstop, because (1) is a line someone can forget:** every typed column carries `CHECK(typeof(col) IN ('<class>','null'))`. **`CHECK` is specified in preference to a `STRICT` table** and the reason is a receipt: `STRICT` still **coerces** a losslessly-convertible `'3'` to `3` and only rejects `'3abc'`, so it is the weaker guard; `CHECK(typeof(...))` rejects both. Verified, §8.1a §C |
| **R-2 / R-3** | `x = 'y'` over NULL yields NULL, and negation over NULL yields NULL — so a negative filter **drops** absent rows | **THE LEAF REWRITE IS NOT SUFFICIENT AND REVISION 4 STOPPED THERE.** Two requirements, and the second is new: **(1)** a negated leaf compiles to `(x IS NULL OR x <> ?)`, always, with no operator-level opt-out — and the same wrapper for **every** operator, not only `=`/`<>`: `NOT (x > 5)` drops the NULL row and `(x IS NULL OR NOT (x > 5))` does not. **(2) FR-023a: negation MUST be pushed to the leaves by De Morgan normalisation BEFORE any SQL is emitted.** The filter grammar §4.1.2 declares is a tree, and `{not: {all: [...]}}` is its normal shape; the natural implementation — compile the subtree, wrap it in `NOT (...)` — silently drops every NULL-bearing row no matter how correct each leaf is. See §8.1a §B for the executed receipt and FR-023a for the requirement |
| **R-4** | A non-conforming value is stored as whatever it parsed to and compares silently | FR-021a: non-conforming values are stored flagged, never in the typed column, and every query touching that property emits a problem row. **Plus FR-021b, revision 5:** "never in the typed column" leaves NULL there, which is the **absence** representation — so non-conforming and absent become indistinguishable in storage, colliding R-4 with R-2/R-3. A **separate presence/conformance flag column** is required, and it MUST be consulted at comparison time **and at `ORDER BY` time** |
| **R-5** | Enums order lexically | The declared ordinal is stored in its own column and `ORDER BY` uses it (FR-010). **Two additions in revision 5.** **(a) `NULLS LAST` is mandatory** — SQLite sorts NULL **first** ascending, so a value with no ordinal (i.e. a non-conforming enum, the thing R-4 says must be *reported*) **heads page one**. `NULLS LAST` is supported in this engine (verified, §8.1a §F); the portable `ORDER BY (col IS NULL), col` form is an acceptable equivalent. **(b) Enum resolution is case-insensitive** (R-5 as revised) — it MUST run against the Go-side fold (FR-011a), not `COLLATE NOCASE`, because `COLLATE NOCASE` folds no non-ASCII, so `Ätä` would not resolve to `ätä` and would be reported non-conforming |
| ~~**R-6**~~ | *(retired — `money` is deleted, operator ruling 1)* | *(no defeat: no subject)* |
| **R-7** | **NEW ROW, revision 5 — R-7 had NO defeat and SQLite violates it four independent ways.** Under TEXT storage: two spellings of the **same instant** compare unequal and order anyway (`'2026-08-27T00:00:00+02:00'` vs `'2026-08-26T22:00:00Z'`, both epoch `1787781600` — `=` is **0**, `>` is **1**); fractional seconds invert (`'…09:00:00Z' < '…09:00:00.500Z'` is **0**, because `Z` (0x5A) sorts after `.` (0x2E) — **and this one survives an all-UTC corpus**, so "we always store UTC" is not a defence); the `T`-vs-space separator reorders; and any non-UTC offset breaks ordering outright | **`date` MUST be stored as a signed integer epoch, with the precision declared on the property** (`seconds` default, `milliseconds` permitted), **never as text**, and comparison and ordering run on the integer column. Text is retained **only** as the raw value for rendering and for R-4's problem line. **This introduces one new hazard and FR-021c closes it:** `unixepoch()` returns **NULL** with no error for both `'not-a-date'` and the merely non-zero-padded `'2026-8-26'`, which would collapse an R-4 non-conformance into the R-2/R-3 absence representation. **Date parsing is therefore Go-side, before insert**, and a value that fails to parse is written to the flag column of FR-021b, never as a NULL epoch |
| **R-9** | The natural SQL spelling of list `contains` is `LIKE '%vendor%'`, which matches `vendors` — substring where the rule says element | `contains` on a list is a **child-table equality join**, not a string operation — **and see §8.1a's tenth violation, which is what that join costs if the aggregate is not written for it** |
| **R-10** | `LIKE` is substring **and ASCII-case-insensitive**, so `'ACME' LIKE '%acme%'` is **true** | **REVERSED, revision 5 (operator ruling 3): that is now the DESIRED behaviour, and `LIKE`'s prohibition is deleted.** The requirement is `contains` and equality on text match **case-insensitively**. The mechanism is **`instr(col_fold, ?) > 0` against the Go-computed fold column (FR-011a), with the needle folded by the same Go function**, and the choice is made on two grounds that are *not* about case: **(1)** SQLite's own folding is **ASCII-only** — `COLLATE NOCASE`, `LIKE` and `lower()` all fold `A`/`a` and fold **nothing** outside ASCII, verified over fourteen pairs (§8.1a §H), and `lower()` returns non-ASCII input byte-for-byte unchanged, so it is not a workaround but the same limitation wearing a different hat; Go's `strings.ToLower` **is** Unicode-aware, so the fold must happen in Go or not at all. **(2)** `LIKE` would require escaping `%` and `_` in caller-supplied text, and an unescaped `%` in a needle is a wildcard nobody asked for. **`instr()` was specified in revision 4 *because* it is case-sensitive; that reason is gone, and it survives on the new reasons, applied to a different column.** `COLLATE NOCASE` on the text columns is **permitted and correct** for the ASCII case and is no longer a defect (this reverses the grill's C-7 for text columns) — but it is **not sufficient**, and FR-011a's column is what the requirement rests on |
| **R-8** | A relation-**id** column declared `COLLATE NOCASE` makes `CO-0142` and `co-0142` one key — loud (`UNIQUE constraint failed`) but a refusal for a case nobody chose | **The fold is applied per column, not per database.** Relation **path/name** columns fold (they resolve case-insensitively, which also removes a real macOS-vs-Linux wikilink divergence); the relation **identifier** column is `BINARY` and matched exactly. Identifiers are minted by us in one canonical form (`CO-0142`), so nothing is lost by not folding them. *(This is the surviving half of the grill's C-7 and it is upheld.)* |
| **R-11** | **WIDENED, revision 5 — revision 4 caught only errors, and four of the five third outcomes are NOT errors.** Verified (§8.1a §D): `1/0` → **NULL**, silently, scanning as nil through the driver; scalar int64 overflow → **REAL** (`9223372036854775807 + 1` → `9.22337203685478e+18`, `typeof=real`), silently degrading an exact integer to a float; `unixepoch('bad')` → **NULL**, silently; `SUM` over an empty set → **NULL**, silently; and asymmetrically `SUM` **overflow** → a hard `integer overflow` error that aborts the statement. Same arithmetic, opposite failure modes | **Five outcomes, five defeats, enumerated because "catch the error" reaches only one of them.** *(1)* No division is emitted at all — the filter grammar has no division operator and MUST NOT gain one without amending this row. *(2)* Integer range is checked **in Go before emission** (FR-012) and **`CAST(? AS INTEGER)` is forbidden**, because it **saturates silently at int64 max** rather than erroring (verified). *(3)* Date parsing is Go-side (R-7's row). *(4)* An aggregate over an empty set renders as an explicit `0 records` scope line (FR-125), never as a bare NULL — and `total()` is **not** an acceptable substitute for `SUM()` here, because it returns a REAL `0.0` and would breach FR-013's no-binary-float rule. *(5)* Store errors are caught at the query boundary and rendered as problem rows. **A test asserts each of the five separately** (AC-8.4a) |
| **R-12** | **NEW ROW, revision 5 — R-12 was listed among the rules, was not among the violations, and is violated.** Comparison affinity converts a TEXT operand **only when the other side is a typed column**: `SELECT 3 = '3'` → **0**, but the same comparison against an INTEGER column → **1**; `SELECT '2' > 3` → **1**, against the column → **0**. Identical values, identical operator, **opposite answers depending on operand provenance** — and a `BLOB`-affinity column restores literal behaviour, so the answer also depends on the DDL. **This undercuts §8.1a's own headline receipt:** `SELECT '3' > 2` → `1` is true, and against a column the same comparison returns `1` for a *different* reason, and against a `BLOB` column returns `0` | The **query literal is normalised through the same declared-type guard as the column side, before emission** (R-1's mechanism (1), applied symmetrically). A literal that does not conform to the property's declared type is refused by FR-024's path, never emitted and never compared. **AC-8.1's generated table MUST include the literal-vs-column asymmetry as generated cells**, so the table exercises both provenances rather than assuming they agree |

### 8.1a — The tenth violation, and the executed receipts

**The tenth violation is join fan-out, and it is the worst one in this document.** The R-9 defeat —
*"a child-table equality join, not a string operation"* — is correct for a single-value predicate and
**silently wrong the moment two child rows match one parent**, which is the entire point of a `many`
property. Verified:

```sql
-- record 1 (amount 100) carries tags 'vendor' AND 'vendors'; record 2 (amount 50) carries 'other'
SELECT COUNT(*)      FROM rec r JOIN tags t ON t.rec_id=r.id WHERE t.val IN ('vendor','vendors');  -- 2    truth: 1
SELECT SUM(r.amount) FROM rec r JOIN tags t ON t.rec_id=r.id WHERE t.val IN ('vendor','vendors');  -- 200  truth: 100
```

**Every count and every total over a filtered multi-value list is wrong**, quietly, by a factor that
varies per record. It reaches `aggregate` (`count`, `sum`, `min`, `max`), the `join` parameter and
`group_by`. **The defeat is `EXISTS`, and the alternatives were tested rather than assumed:**

```sql
-- add record 3, ALSO worth 100, ALSO tagged 'vendor'.  Truth is now 2 records, 200.
SELECT SUM(DISTINCT r.amount) FROM rec r JOIN tags t …;   -- 100  WRONG, and wrong the OTHER way
SELECT SUM(r.amount)          FROM rec r JOIN tags t …;   -- 300  WRONG
SELECT COUNT(DISTINCT r.id)   FROM rec r JOIN tags t …;   --   2  correct for COUNT
SELECT COUNT(*), SUM(amount)  FROM rec r WHERE EXISTS(SELECT 1 FROM tags t WHERE t.rec_id=r.id AND t.val IN (…));
                                                          -- 2|200  correct for BOTH
```

**`EXISTS` is chosen over `COUNT(DISTINCT)` because `COUNT(DISTINCT)` does not generalise.** It fixes
`count` and has no working analogue for `sum`: `SUM(DISTINCT)` deduplicates on **value**, not on row
identity, so two genuinely distinct records that happen to share an amount collapse into one. It
returned **100** where truth is 200 — and it errs in the *conservative-looking* direction, which
makes it the harder wrong answer to catch in review than the naive join's 300. `EXISTS` is one shape
that is correct for `count`, `sum`, `min`, `max` and any future aggregate, so it is the requirement
(**FR-028a**). `SELECT ... FROM (SELECT DISTINCT r.id, r.amount FROM …)` is an accepted equivalent.

**`group_by` is the one case `EXISTS` does not cover**, because grouping by a multi-value property
*must* fan out — FR-028 requires a record to appear under every group it belongs to. There the
requirement is the opposite one: the fan-out is intended for **membership**, and each group's
**aggregate** must still be computed over distinct parent rows within that group. Both halves are in
FR-028a.

**The executed receipts.** Run against the **`sqlite3` CLI 3.51.0**; every claim re-executed
identically through **`modernc.org/sqlite v1.46.1`**, which reports `sqlite_version()` = **3.51.2**.

**§A — case folding.** `SELECT id FROM nc WHERE name='acme'` over a `TEXT COLLATE NOCASE` column
holding `ACME`, `acme`, `Acme` → **all three**; `COUNT(DISTINCT name)` → **1**; `GROUP BY name` →
`ACME|3`. `'ACME' = 'acme'` → **0**; `… COLLATE NOCASE` → **1**. `'ACME' LIKE '%cm%'` → **1**.
`instr('ACME','acme')` → **0**. `'abc' GLOB 'A*'` → **0**. **`LIKE` ignores column collation
entirely** — it returned the same answer on a `NOCASE` column and a `BINARY` one, while `=` differed
between them, so the two operators disagree about identity on one column unless the fold is made
explicit.

**§B — negation and NULL.** Fixture: `(1,a=1,b=2)`, `(2,9,9)`, `(3,NULL,2)`, `(4,1,NULL)`,
`(5,NULL,NULL)`. `NOT (a=1 AND b=2)` → **`2`** — rows 3, 4, 5 dropped. `NOT (a=1 OR b=2)` → **`2`**;
**the grill said this returns ZERO rows and it does not** — the correct De Morgan answer is `2,3,4,5`,
so **three** rows are silently dropped, not four, and the finding is CRITICAL at its true value.
`NOT (a>5)` → `1,4`; guarded → `1,3,4,5`. `typeof(instr(NULL,'x')>0)` → **null**, so
`NOT (instr(note,'x')>0)` → `2` while the guarded form → `2,3,5` — **`instr` needs the identical
`IS NULL OR` wrapper.** `instr('abc','')` → **1** and `instr('','')` → **1**, so an **empty needle
matches every row**: FR-022a refuses an empty `contains` value rather than returning the whole table.

**§C — affinity.** `INSERT INTO ti(n INTEGER) VALUES ('3'),('3abc')` → `typeof` is `integer` and
**`text`**; `'3abc' > 2` is **1**. `STRICT` accepts `'3'` (coercing it) and rejects `'3abc'`;
`CHECK(typeof(n) IN ('integer','null'))` rejects **both**. Literal-vs-column: `3='3'` → 0 / column
→ 1; `3>'2'` → 0 / column → 1; `'2'>3` → 1 / column → 0; over a `BLOB`-affinity column both → 0.

**§D — arithmetic.** `9223372036854775807 + 1` → `9.22337203685478e+18`, `typeof` **real**.
`1/0` → NULL. `1.0/0.0` → NULL. `SUM` over int64-max plus 1 → **`Runtime error: integer overflow`**.
`SUM` over an empty set → NULL (`typeof` null); `total()` over the same → `0.0`, `typeof` **real**.
`0.1 + 0.2 = 0.3` → **0**. `CAST('9223372036854775808' AS INTEGER)` → **`9223372036854775807`**,
`typeof` integer — **saturated silently, no error, no NULL.** An INTEGER column stores
`9223372036854775807` and `-9223372036854775808` losslessly and reads them back equal.

**§E — dates as TEXT.** `'2026-08-27T00:00:00+02:00' = '2026-08-26T22:00:00Z'` → **0** while
`unixepoch()` gives **1787781600** for both; `>` between them → **1**. `'…09:00:00Z' <
'…09:00:00.500Z'` → **0**. `'…09:00:00Z' < '2026-08-26 09:00'` → **0**. `unixepoch('not-a-date')` →
NULL; `unixepoch('2026-8-26')` → **NULL**. An all-UTC four-row table ordered by `ts` versus by
`unixepoch(ts,'subsec')` disagreed on two adjacent pairs.

**§F — NULL ordering.** `ORDER BY ord ASC` → `NULL, 1, 2, 3`; `ASC NULLS LAST` → `1, 2, 3, NULL`.
**`NULLS LAST` is supported in 3.51.x.** `DESC` already sorts NULL last, so a `DESC NULLS LAST` test
alone would **not** prove support — the ascending case is the one that must be asserted.

**§H — the Unicode limit, stated because it bounds operator ruling 3.** Across fourteen
upper/lower pairs (`A/a`, `Z/z`, `Ä/ä`, `É/é`, `Ñ/ñ`, `Ø/ø`, `Ç/ç`, `Å/å`, `Σ/σ`, `Д/д`, `İ/i`,
`Ł/ł`, `Ż/ż`, `Ć/ć`): **`COLLATE NOCASE`, `LIKE` and `lower()` each folded the two ASCII pairs and
ZERO of the twelve non-ASCII pairs.** `lower()` returned every non-ASCII input **byte-for-byte
unchanged** (`hex('Ä')` = `hex(lower('Ä'))` = `C384`). Confirmed structurally as well as
behaviourally: `PRAGMA compile_options` carries no `ENABLE_ICU`, and `icu_load_collation` does not
exist. **There is no Unicode-aware option inside SQLite here at all**, which is why FR-011a puts the
fold in Go.

**The R-2 case is the sharpest, and it is worth naming as a product failure rather than a SQL
quirk. FR-008 exists so that "which days did I not meditate?" returns the days with no entry.
Under SQLite's default negation it returns the wrong rows** — three of four dropped in §B's
fixture — with the omitted rows being exactly the rows asked about. That is the silent-failure
class this whole specification exists to remove, reintroduced by the engine chosen to implement
it. **And the defeat is not the one line revision 4 said it was:** the leaf rewrite is necessary
and insufficient, because nothing in revision 4 required negation to reach the leaves at all.

- **AC-8.4** — the truth table runs against the **real query path** — schema → filter object →
  compiled query → store — not against a Go comparator in isolation. A truth table that passes
  over a comparator the product does not use proves nothing about the product, and after ADR-068
  D16.2b (*"the properties index answers every typed predicate"*) the product does not use one for
  filtering. **This is the criterion that separates the eleven defeats being verified from their
  being believed**, and ADR-068 restates it as AC-16.6 for that reason.
  - **AC-8.4 alone is not sufficient, and revision 5 says so rather than leaning on it.** It
    describes a **path**, and an implementation that emits sloppy SQL and corrects the result set
    in Go satisfies that description, passes every cell, and is wrong — the same defect §8
    identifies for the Go comparator, one layer down. **So AC-8.4 additionally forbids the Go
    post-filter, observably:** a row count taken **at the driver boundary** MUST equal the rendered
    row count plus the problem rows. A Go pass that discards rows the SQL should have excluded
    makes those two numbers differ, and the criterion fails. *(This also enforces FR-066b, which
    the post-filter would violate by materialising rows the SQL should never have returned.)*
  - **The emitted SQL text is asserted, not only its results**, for at least the negation and
    `contains` cases — because those are the two where a correct result can be reached by an
    incorrect query over a fixture too small to distinguish them.
- **AC-8.4a** — **NEW, revision 5. One mutation PER defeat, not two for all of them.** SC-024 named
  exactly two mutations — removing the `IS NULL` arm of a negation, and swapping `instr()` for
  `LIKE` — and **a compiler with no declared-type guard (R-1), no De Morgan pass (R-2), no ordinal
  column (R-5), no epoch dates (R-7), no `EXISTS` aggregate (R-9 fan-out), no flagged
  non-conforming storage (R-4) and no literal normalisation (R-12) passes both.** The `LIKE`
  mutation is now meaningless as well, since `LIKE` is permitted. The obligation is therefore: **a
  named mutation for every defeat in §8.1's table and for the fan-out defeat — eleven minimum —
  each of which MUST make the truth table fail**, plus the five separate R-11 mutations enumerated
  in its row. The artifact is a **mutation report listing each mutation, the cell it killed, and
  the run that produced it**; A-11's exit is that report, not a pass.
- **AC-8.5** — the table is run twice: once against a freshly built properties index and once
  after a delete-and-rebuild (FR-020a). Identical results both times.
- **AC-8.6** — **membership is invariant under ranking.** The same filter run with plain BM25 and
  with the FR-112 fusion returns the same **set** of records, differing only in order. **REWRITTEN,
  revision 5: as revision 4 worded it, this could not fail.** FR-021 puts membership in SQLite and
  FR-112's fusion is a Go-side ranking pass over an already-selected set, so set-equality is
  guaranteed by the architecture and asserting it tests nothing. The assertion that *can* fail, and
  is the one meant: **a corpus is constructed in which the two rankings return different orders**
  (a term-saturation or length-normalisation case, so BM25 and TF-IDF demonstrably differ), the
  order difference is asserted to be non-empty, **and then** set-equality is asserted over it. A
  run in which the two orders are identical **fails the criterion**, because it proves the fixture
  did not exercise the ranking change.
- ~~**AC-8.7**~~ — **DELETED, revision 5 (operator ruling 3).** It asserted zero occurrences of
  `LIKE` in the compiled filter path, to protect a case-sensitivity that is no longer wanted. §7
  test 39 (`TestFilter_NoLikeInCompiledPath`) is deleted with it. **Its schema half is retained and
  relocated** to **AC-8.8**, because that half was about identity, not case.
- **AC-8.8** — **NEW, revision 5 (the surviving half of the grill's C-7).** A test asserts over the
  **emitted DDL**, not the filter path, that the relation-**identifier** column declares no
  non-`BINARY` collation and neither does any index over it. Folding identifiers makes `CO-0142`
  and `co-0142` one key. Text, enum-label and relation-path columns MAY declare `NOCASE` and this
  criterion does not touch them.

## 9. Holdout evaluation scenarios

**Not for use during development.** Not referenced in §6 or §7.

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
    top 10 for 20 real queries. If the fusion does not win, FR-113 says it does not ship.
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
    is the case SQLite's defaults get backwards (§8.1).

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
| **A-11** | **LIVE, new in revision 4. The nine SQLite-semantics defeats are specified and none is verified.** §8.1 gives the defeat for each of the nine rules SQLite's defaults contradict; ADR-068 D16.6 gives the same list. **Every one of them is a line of a query compiler nobody has written**, and eight of the nine fail in the quiet direction, so the absence of a defeat produces a passing test suite and a wrong product. AC-8.4/SC-024 is the control, and it is only a control if it is **mutation-checked** against the real compiled path. **Needs:** W2 to report the mutation run, not the pass. |
| **A-12** | **LIVE, new in revision 4. The properties-index write path is now carrying a second obligation nobody has measured.** FR-020c adds a `source_hash` column written in the same transaction as every record row, and FR-076a adds a checkbox row per task line. A-7 already flags the two-index write path as unmeasured; these widen what is unmeasured rather than narrowing it. **Needs:** W1 and W2 to measure the write path **with** `source_hash` and task rows present, not a bare record write. |

**A-4 and A-5 were specification defects and are now resolved by descoping the decisions that
caused them** (ADR-068 D11, D12). **A-6, revision 2's live blocker, is resolved — W1 is
unblocked**, with its rationale corrected in revision 4 to capability alone.

**Revision 4's closing count. A-8 and A-9 are both closed, and both closed the way this spec
argued** — ADR-068 revision 6 adopted this document's reading in each case rather than the other
way round. A-10 is halved. Four items stay live and **none of them blocks W1**: A-7 and A-12 are
measurement W1 and W2 perform, A-10's residual is a description review at W5, and A-11 is a
verification obligation on W2 that already has its acceptance criterion written (AC-8.4, SC-024).
The one thing a reader should carry away from this table is A-11: **a specified defeat and a
verified defeat are different things, and eight of the nine failures look identical to success.**
