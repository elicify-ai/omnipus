# Vault records specification — adversarial review, round 2

- **Spec under review:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements/docs/internal/specs/vault-records-spec-2026-08-25.md`
- **Inherited from:** `ADR-068-vault-records-typed-record-layer.md` revision 2
- **Tree verified against:** worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements`, branch `feat/library-improvements`
- **Mode:** plan-spec (FR/SC identifiers, traceability matrix, numbered test plan)
- **Date:** 2026-08-25

---

## Executive summary

The specification contracts behaviour that the code it names as reusable cannot produce,
and its single highest-blast-radius requirement (FR-020, modifying ADR-067's bleve index)
is both infeasible as written and untested. **8 critical, 21 major, 9 minor, 5 observations.**

Verdict: **BLOCK.**

The three headline problems, all verified against the tree rather than inferred:

1. **FR-020 cannot be implemented as written.** `buildIndexMapping()`
   (`pkg/knowledge/index.go:531`) sets `doc.Dynamic = false`, `m.IndexDynamic = false`,
   `m.StoreDynamic = false`, and `indexDoc` (`index.go:583`) is a fixed five-field struct.
   Operator-declared property names are runtime data from schema files. No FR covers
   building the mapping from schemas, and bleve freezes the mapping at index-create time,
   which `openOrCreateBleve` (`index.go:446`) confirms — an existing index is opened with
   `bleve.OpenUsing(path, cfg)` and never re-mapped.

2. **FR-040's claimed reuse does not cover what FR-045 and D11 require.**
   `pkg/knowledge/author.go` exposes exactly one property writer, `SetProperty(key, value
   string)` (`author.go:766`), and `authorEncodeScalar` (`author.go:1048`) **refuses any
   value containing a newline**. There is no list writer and no nested-map writer. Worse,
   `fmFindKey`/`fmSpliceKey` (`author.go:934`, `author.go:901`) *delete every continuation
   line* of the key being written, so a scalar write over a sub-record list silently
   destroys the list — the precise inverse of the byte-preservation guarantee US-4 exists
   to give.

3. **The spec requires exactly eight tools and then requires a ninth.** FR-070 enumerates
   eight and does not include `record_view_import`; FR-100, FR-101 and FR-102 all mandate
   `record_view_import`, as do US-7, SC-010 and test 20. Inherited unresolved from ADR-068
   D15 vs O-1.

The spec's self-reported defects A-4 and A-5 are confirmed and are worse than stated: §3's
edge-case table already **contracts behaviour** for sub-records and for temporal properties
(three rows), so the missing FRs are not an omission of scope — they are behaviour promised
with no requirement, no test and no owner.

---

## 1. Findings

### CRITICAL

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| **C-1** | Inconsistency | FR-070 vs FR-100..102, US-7, SC-010, test 20 | FR-070: *"MUST expose exactly eight tools"* and the list omits `record_view_import`. FR-100/101/102 require `record_view_import`. Both cannot hold. Inherited from ADR-068 D15 (eight-row table) vs O-1's resolution, which added the importer without amending D15. | Amend FR-070 to nine tools naming `record_view_import`, or state that the importer is a CLI/one-shot path and not an agent tool — and then delete FR-100..102's tool framing. Whichever is chosen must also be corrected in ADR-068 D15. |
| **C-2** | Infeasibility | FR-020, §0 index row, §1 blast-radius row | Record properties cannot become bleve stored fields under the existing mapping. Verified: `buildIndexMapping()` sets `doc.Dynamic=false; m.IndexDynamic=false; m.StoreDynamic=false` and `indexDoc` is a closed struct `{Path,Name,Kind,Offset,Body}`. Property names come from `<vault>/.omnipus-vault/records/<type>.yaml` at runtime. The spec asserts index.go is *"reused"* for FR-020 with no requirement describing how a runtime-variable field set enters a compile-time mapping. | Add an FR specifying the mechanism: (a) a per-collection mapping built from the loaded schemas at index-create time, or (b) a single stored `omni_record_props` JSON blob field with all filtering in Go, or (c) `StoreDynamic` enabled with a named cost. Option (b) is the only one that does not make the mapping a function of operator data; if (a) is chosen, C-3 must be answered in the same FR. |
| **C-3** | Incorrectness / Inoperability | FR-020, §7 Regression | bleve persists the index mapping in `index_meta.json` at create. `openOrCreateBleve` calls `bleve.NewUsing(path, buildIndexMapping(), …)` **only when the directory does not exist**; otherwise `bleve.OpenUsing(path, cfg)`. Adding record fields to `buildIndexMapping()` therefore has **zero effect on every already-created index**. `manifestVersion` (`pkg/knowledge/manifest.go:48`) versions the *manifest*, not the bleve mapping — bumping it forces a re-parse into the **old** mapping. Consequence on any upgraded install: records index into fields that do not exist, queries return nothing, and `complete: true` is reported. That is the exact silent-wrong-answer failure class the ADR exists to remove. | Add an FR: an index-mapping version, persisted, checked on open; a mismatch forces a full index **recreate** (delete the directory, not just the manifest), reported to the operator. Add a test that opens an index created by the pre-record mapping and asserts a recreate occurs. Nothing in §7 currently touches this path. |
| **C-4** | Incorrectness | §3 *"no binary floating point anywhere in the path"*, FR-013, FR-020, DS-1 row 10 | Bleve's only numeric field type is float64 (`bleve.NewNumericFieldMapping`; the existing `indexDoc.Offset` is `float64`). FR-020 mandates record properties be **stored fields in that index**. So money amounts and numbers must round-trip through float64, contradicting §3's *"exact decimal; no binary floating point anywhere in the path"* and FR-013. DS-1's row **`2^53+1` → "accepted, exact"** is unachievable through a bleve numeric field — float64 cannot represent it. | Decide the representation explicitly: store money and number as *keyword strings* (exact, but no bleve numeric range query — all range filtering then happens in Go per FR-021, which is consistent), and state that in an FR. Then either delete the `2^53+1` DS-1 row or keep it and add the FR that makes it true. |
| **C-5** | Infeasibility | FR-040, FR-045, FR-006, DS-3, ADR-068 D11 | `author.go`'s reusable write surface is `SetProperty(key, value string)` plus `AppendSection`. `authorEncodeScalar` returns `ErrInvalidProperty` for any value containing `\r` or `\n` — its own comment: *"A newline in a value is REFUSED rather than folded into a block scalar."* There is no primitive for a YAML list or a nested map. FR-045 (relation `add`/`remove`/`replace` on `many: true`), FR-006 (arity, i.e. list values), and DS-3's *"nested sub-records"* all require exactly that. Additionally `fmFindKey` consumes continuation lines (indented lines and `- ` block-sequence items) and `fmSpliceKey` replaces the whole consumed range with one scalar line — writing a scalar to a key that currently holds a sub-record list **destroys the list without reporting it**. | Either (a) add an FR specifying a new multi-line splice primitive in `author.go` with its own byte-preservation acceptance criterion and its own tests, and correct §0's reuse table to say author.go is *extended*, not merely called; or (b) descope `many:` relations and sub-records from this spec entirely (which is the honest reading of A-4). Do not leave FR-040 asserting reuse that does not exist — this is the same class of false-claim-about-existing-code that put ADR-068 revision 1 into BLOCK. |
| **C-6** | Incompleteness | FR-001, FR-020, §7 Regression | Schemas live at `<vault>/.omnipus-vault/records/<type>.yaml`. `scanSkippedDirNames` (`pkg/knowledge/scan.go:67`) **never descends into `.omnipus-vault`** — it is a collection marker. Schema files therefore have no manifest entry, no size/mtime record, and no hash. No FR covers *"the schema changed, so the index must be rebuilt."* Adding a property to a schema leaves every existing record indexed without it; queries filtering on the new property return zero records and report `complete: true`. | Add an FR: schema files are hashed on open; a change to any schema invalidates the record portion of the index and triggers a reindex, and a query issued while that reindex is outstanding must set `complete: false` with the reason. Add the corresponding test to §7 — none of the 21 covers it. |
| **C-7** | Incompleteness / Inconsistency | A-4, A-5, §3 edge table, ADR-068 D11/D12/D20 W6 | The spec correctly self-reports that D11 (sub-records) and D12 (temporal facts) carry no FR. The defect is larger than stated: §3's edge table **already contracts three behaviours** for them — *"Sub-record list containing one malformed entry → that entry reported; the rest of the record remains valid"*, *"Temporal property queried with no `as of` → the currently-valid value"*, *"Temporal property with overlapping validity ranges → reported as a validation error"* — and ADR-068 D20's W6 exit criterion is *"who did we know at Acme in 2023 is answerable"*. Behaviour is therefore promised, in a normative table, with no requirement, no test and no implementation obligation. ADR-068 §4.2 additionally records that both features have **no prior art**, which makes them the two least-specified and highest-risk items in the design. | Choose one and do it in this document: (a) write FR-110..FR-119 for sub-records and FR-120..FR-129 for temporal facts, with tests, and accept the schedule; or (b) **cut both**, delete the three edge-table rows, delete the DS-3 "nested sub-records" corpus entry, and record the descope against ADR-068 D11/D12 and W6. Option (b) also resolves half of C-5. |
| **C-8** | Incorrectness | FR-036, FR-038, US-5.4, ADR-068 D7 | ADR-068 D7 states the sequence is *"monotonic per type and **never reused**, so ID count is not record count."* FR-038 and US-5.4 require the allocator to reconcile its counter to `max(existing id)` on open. If the highest-numbered record is deleted, the next open allocates that same ID again. A relation pointing at the deleted `CO-0142` — or a commit message, or a prose reference, all of which D7 cites as the reason for a readable ID — then silently resolves to a **different company**. This is a silent record merge through the join key, which D7.1 itself names as the failure the allocator exists to prevent. | Add an FR: the reconcile floor is `max(highest existing id, last recorded sequence)` and the sequence file is the authority when present; a rebuild from records alone (sequence file lost) MUST be reported as a lossy recovery, not performed silently. State explicitly whether ID reuse after deletion is permitted; if it is, D7's "never reused" must be withdrawn and every relation-to-deleted-record path re-examined. |

### MAJOR

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| **M-1** | Infeasibility | §3 perf table, SC-007, test 21 | The performance targets are unfalsifiable. Every target is stated *"at 50,000 records"*, but the actual cost driver is the **candidate set**, which FR-064 caps at 10,000 and refuses beyond. A query whose filter matches 50 records meets `< 150 ms` trivially; one matching 20,000 is refused and never measured. The benchmark query's selectivity — the only variable that matters — is unspecified. ADR-068 D16's own supporting figure (~794 ns/record) puts Go evaluation of a full 10,000-record candidate set at ~8 ms, i.e. under 2% of the 400 ms budget, so the budget is entirely retrieval and stored-field fetch, which nothing bounds. | Restate every target as a function of candidate-set size at a **named selectivity** — e.g. *"filter matching 10,000 of 50,000 records (the cap), p95 < 400 ms"* — so the number can fail. Specify the fixture generator's distribution in DS terms. |
| **M-2** | Inconsistency | §3 RSS bound | §3 permits `< 96 MB` peak RSS for aggregation at the candidate cap. ADR-067's own budget is **`< 64 MB` steady-state RSS at 100,000 notes** with the index open. These run in one process. The spec adds 1.5× the entire existing index budget on top of it and never reconciles the two, nor mentions Hard Constraint #3's minimal-footprint posture. | Restate the record bound as *incremental over ADR-067's steady state* and give the combined ceiling. If the combined figure exceeds what ADR-067 promised operators, that is a change to ADR-067's contract and belongs in an ADR, not a spec table. |
| **M-3** | Incorrectness | §3 bounds, FR-064, ADR-068 D16 | Bounds are stated in **records**; the index counts **segments**. `segmentDocID(relPath, ordinal)` (`index.go:594`) means a note over `IndexSegmentSize` produces several documents, and the manifest records `Segments` per file precisely because *"deleting a file from the index means deleting every document it produced."* A 50,000-record vault is therefore an unknown, larger number of bleve documents, and a 10,000-record candidate set is an unknown, larger number of hits. Nothing in the spec says the record layer de-duplicates segments, and no test asserts that a multi-segment record appears once. | Add an FR: record retrieval de-duplicates by note path/record id before the candidate cap is applied, so the cap counts records, not segments. Add a DS row with a record whose body exceeds `IndexSegmentSize` and a test asserting it counts once. |
| **M-4** | Structural | §6 vs §7 | Four tests named in the traceability matrix do not appear in the test plan: `TestSchema_TypesAreScopedToRecordType` (FR-004, 009), `TestIndex_NoSecondStore` (FR-020, 021), `TestRelate_ReplaceMustBeNamed` (FR-045), `TestDerived_NeverWrittenToFrontmatter` (FR-046). Two tests in §7's Regression block (`TestKnowledgeSearch_UnaffectedByRecordFields`, `TestIndex_RebuildWithRecordFields`) appear in neither §6 nor the numbered plan. §6 names 23 tests; §7 plans 21. | Reconcile the two lists. Every test in §6 must have a §7 row with a level; every §7 row must trace to at least one FR. |
| **M-5** | Structural | FR-005, §6, US-1.1 | **FR-005 appears in no traceability row.** *"The system MUST treat a note whose `type` matches no schema as an ordinary note, without error"* is the requirement that keeps the feature inert on a real vault, it is US-1's first scenario, and it has no test. §9 holdout 1 exercises it but §9 is explicitly excluded from development. | Add FR-005 to §6 with a test — a corpus of notes with no schema, asserting zero errors raised and zero records reported. |
| **M-6** | Test quality | FR-020, §6 row 7 | FR-020's only mapped test is `TestIndex_NoSecondStore`, annotated *"asserts no sqlite import in the record path."* That test is already green today (no SQLite appears anywhere in `pkg/knowledge`) and would remain green if FR-020 were never implemented. **The single highest-blast-radius requirement in the spec has no test that can fail for the reason the requirement exists.** | Add a test that indexes a typed record, retrieves it, and asserts the property values came back **from the index** — with a mutation check (change the stored value, confirm the assertion dies) per the project's test-integrity standard. |
| **M-7** | Test quality / Duplication | FR-071, §0 correction 2, test 18 | `pkg/sysagent/tools/contract_test.go:71` already asserts `!strings.Contains(name, ".")` across all 35 sysagent tools, and `TestRegistry_AllSysagentToolsCategory` is green today. As §0 describes it — *"a §7 invariant test asserts zero dots across builtin tool names"* — `TestTools_NamesHaveNoDots` **passes today with zero record tools implemented**. It cannot fail for the record layer. | Either scope the test to the eight/nine `record_*` names *and* assert the expected set membership (so a missing tool is red), or delete it and cite the existing test. FR-071 alone does not warrant a new test. |
| **M-8** | Test quality | §7 Regression | `TestKnowledgeSearch_UnaffectedByRecordFields` cannot fail. Every field in `buildIndexMapping()` sets `IncludeInAll = false` — its own comment explains that the composite `_all` field is deliberately off and *"we query the real fields explicitly"* — and bleve's IDF is per-field. Adding new fields therefore cannot change `body`/`name` ranking by construction. The seam that actually breaks is the mapping freeze (C-3), which this test does not touch. | Replace it with a test that opens a **pre-existing index directory created under the old mapping** and asserts the documented upgrade behaviour (recreate, or explicit refusal). That is the seam that can fail. |
| **M-9** | Incorrectness | FR-082, ADR-068 D18 | FR-082's rationale — *"because absence grants in a sparse map"* — is only true when the **global ceiling** grants. Verified: `pkg/config/defaults.go:637` seeds `"knowledge_search": "allow"` globally, and `pkg/coreagent/core.go:763` states the mechanism precisely: *"the global ceiling now seeds … 'allow' … so an absent entry here would silently inherit that 'allow'."* Meanwhile `RepairIncompleteToolPolicyCoverage` (`pkg/config/validate.go:525`) backfills an uncovered pair to **`deny`**. So absence grants only via the ceiling, never via the repair. **The spec never states the global `sandbox.tool_policies` seed for the eight record tools** — the value that determines whether FR-082's premise even holds. | State the global ceiling posture for each record tool as seeded data, alongside the per-agent table. Then restate FR-082's rationale as *"absence inherits the global ceiling, which for these tools is X."* |
| **M-10** | Inconsistency | FR-063, §0 search.go row | §0 cites `SearchDefaultTopN = 20` / `SearchMaxTopN = 100` (verified at `search.go:69-70`) as *"the precedent for FR-063"*, and FR-063 then sets **50 / 200**. Two agent-facing retrieval surfaces in the same package with different caps, and the divergence from the cited precedent is unexplained. | Either adopt 20/100 or justify 50/200 with the measured reason (record rows are smaller than search hits with excerpts, presumably). An unexplained divergence from a named precedent is how two conventions become permanent. |
| **M-11** | Incompleteness | DS-3, FR-041 | DS-3 puts **a BOM** in the 50-file write corpus. There is **zero BOM handling in `pkg/knowledge`** (grep for `BOM`/`feff` returns nothing), and `fmParse` (`author.go:874`) requires the note's first line to be exactly `---` after `TrimRight(" \t\r")`. A UTF-8 BOM defeats that test, `block.present` is false, and `SetProperty` then **prepends a second frontmatter block above the existing one**. The corpus contains a case whose correct behaviour no FR states, and whose current behaviour corrupts the file. | Add an FR stating the expected behaviour for a BOM'd note — strip-and-preserve, or refuse the write naming the reason. Refusing is the safer default and is consistent with `authorEncodeScalar`'s posture on newlines. |
| **M-12** | Ambiguity | §3 rate-limit row, FR-070 | *"Rate limit — shared with ADR-067's knowledge limiter"* names one thing where two exist: `knowledgeRESTLimiter` (`pkg/gateway/rest_knowledge.go:90`, a package-level var keyed by **workspace**) and the per-`KnowledgeTools` `RetrievalRateLimiter` (`pkg/knowledge/tools.go:60`, keyed by **agent**, and whose own comment says it *"bounds retrieval"*). Which one governs a record tool, and whether the four **mutating** record tools count against a *retrieval* limiter, is unstated. `429`/`Retry-After` is an HTTP shape and does not describe a tool-call refusal at all. | Name the limiter, the key, and the refusal shape for the tool path separately from the REST path. |
| **M-13** | Test quality | Test 11, SC-005, §3 edge table | `TestID_ConcurrentAllocationIsCollisionFree` is planned as **integration**, but SC-005 requires *"1,000 records created concurrently across two POSIX processes"* and §3's edge table says Windows can collide because flock is a no-op. A same-process goroutine test would pass while proving nothing about the cross-process half — which is the half ADR-068 D7.1 leans on `pkg/entity/store_crossprocess_test.go` for. | State the mechanism: re-exec the test binary as real OS processes, `//go:build !windows`, mirroring `pkg/entity/store_crossprocess_test.go`. Add a second, separate assertion for the in-process striped-mutex half so the two guarantees fail independently. |
| **M-14** | Insecurity (Information Disclosure) | FR-023, FR-024, FR-062 | FR-024 requires an unknown-property rejection to **list the valid property names**, and FR-023 requires schema validation *"before evaluation"*. FR-062 requires the out-of-scope case to be *"indistinguishable from an empty vault"*. If schema validation runs before scope resolution — which is what "before evaluation" reads as — an agent outside the vault's workspace can enumerate the full schema (every property name and every enum value) through the **error channel**, which is precisely the probing channel FR-062 exists to close. A-2 (schema vault-wide or per-workspace) being unresolved makes this worse, not moot. | Add an FR fixing the order: workspace scope resolves **first**; if it yields no vault, the response is empty with no schema information, before any query validation runs. Add the negative test to §7 — `TestScope_CrossWorkspaceReturnsEmpty` as described tests the success path only. |
| **M-15** | Insecurity (DoS) | FR-064, FR-066 | FR-064 refuses a candidate set above 10,000. Determining that the set exceeds 10,000 requires **executing the retrieval**. A filter matching all 50,000 records costs a full index traversal before the refusal is issued, and nothing rate-limits the refusal itself. An agent in a loop issuing deliberately broad filters gets 50,000-document traversals at the rate limiter's ceiling. | Add an FR bounding the *cost of discovering the bound was breached*: request `size = cap + 1` and stop, or use bleve's total-hits count before materialising. State that a refusal counts against the rate limit. |
| **M-16** | Insecurity (Tampering) | FR-001, FR-011, §4 | The schema is a plain YAML file in the operator's vault. Agents in this product hold `bash` and filesystem tools. Nothing in the spec prevents an agent from widening an enum, dropping `required: true`, or changing a property's type by editing `.omnipus-vault/records/<type>.yaml` directly — bypassing FR-011's entire purpose, which is that *"an agent writing `Won` where the schema says `won` must be corrected."* `record_view_write` is policy-gated; a direct file write is not. | Either state that this is accepted (the vault is the operator's, and `bash` is already the trust boundary) with the reasoning written down, or add an FR requiring schema mutations to be audited the way FR-044 audits record mutations. Silence here reads as an oversight. |
| **M-17** | Inoperability | whole spec | Nothing in the spec covers the state *"the index has not caught up with the vault."* There is no FR requiring `complete: false` while a reindex is outstanding, no health signal, no metric, no log requirement, and no way for a caller to distinguish *"no records match"* from *"the record layer has indexed nothing yet."* That is the same silent-wrong-answer shape the ADR was written to remove, one layer up. | Add an FR: a query issued against a collection whose index is stale or rebuilding sets `complete: false` with the reason and an estimated readiness. Add an operator-visible indexing state. |
| **M-18** | Inoperability | FR-020, C-3 | The mapping change is a **one-way door** with no rollback. Once an index is recreated carrying record fields, reverting the binary leaves a mapping containing fields the old code does not write — and the old code will not detect that. No feature flag, no staged rollout, no downgrade path is specified for a change whose blast radius §1 itself describes as *"everything that opens the index."* | Add an FR for the downgrade path (old binary detects a newer mapping version and recreates, or refuses to open with a named error), and state whether the record layer ships behind a flag. |
| **M-19** | Incompleteness | FR-050..053, FR-070, A-3 | `record_log` is one of the eight tools in FR-070 and has **no functional requirement**. The "Interaction history" FR block (FR-050..053) specifies only *derived* history — mentions in dated notes. ADR-068 D17 says `record_log` **creates an interaction record**. A-3 flags the note-vs-append question as open, but the deeper problem is that the tool has no requirement at all, so §6 maps FR-050..053 to `TestInteraction_ExclusionRules` and `record_log` itself is untested. | Add FR-054 (or similar) specifying what `record_log` writes, where, and how it interacts with the derived path — including whether a logged interaction and a harvested mention of the same event double-count. That double-count question is not raised anywhere. |
| **M-20** | Ambiguity | FR-041, US-4.1, SC-004 | *"byte-identical outside the patched span"* — **"the patched span" is never defined**, and the definition is load-bearing. `fmFindKey` consumes and `fmSpliceKey` deletes every continuation line of the target key. Is a deleted five-line sub-record block "inside the patched span" (so its destruction is compliant) or outside it (so the write must be refused)? Two competent engineers will answer differently, and one of the answers is silent data loss. | Define the span explicitly, in bytes, in the FR: *"the contiguous byte range from the start of the target key's line to the end of the last line YAML attaches to it."* Then add the FR that says a write which would *shrink* that span from a structured value to a scalar is **refused**, not performed. |
| **M-21** | Ambiguity / Incompleteness | FR-030..FR-034, DS-2 row 6, §0 | FR-031 requires the index to resolve a relation wikilink to the target's record ID, and DS-2 requires *"ambiguous basename matching two notes"* to be reported with both paths. Wikilink resolution is `pkg/knowledge/links.go` (27 KB, present in the tree), which §0's reuse table **does not name**, and no FR says which resolver is used or whether resolution runs under `Scope.Contains`. FR-060 scopes *"every record tool"* — the indexer is not a tool. | Add `links.go` to §0's reuse table with the specific resolver symbol, and add an FR requiring index-time relation resolution to run inside the collection's scope. Without it, index-time resolution is the one path that can legitimately cross a workspace boundary. |

### MINOR

| ID | Lens | Section | Finding | Fix |
|---|---|---|---|---|
| m-1 | Structural | §7 test 6 | `TestComparisonTruthTable` traces to *"see §8"* and appears in **no §6 row** — the spec's own highest-risk deliverable is outside the traceability matrix. | Give the truth table an FR (e.g. FR-015: comparison semantics across all type pairs) and a §6 row. |
| m-2 | Incompleteness | FR-072 | *"compact textual schema, not a JSON schema object"* has no format definition, no size target, and no test in §7 (FR-070..073 map only to `TestTools_NamesHaveNoDots`). The ~91% token figure is Notion's, measured on their surface, not a target for ours. | Define the format by example and add a test asserting the round-trip names every property, type, enum value and required flag. |
| m-3 | Infeasibility | FR-073 | *"report what a query would return … without evaluating it"* is self-contradictory as literal text — knowing what it would return requires evaluating it. The intent is presumably a plan plus an unevaluable-property list. | Restate: *"reports the properties the query would evaluate, which of them are unresolvable against the schema or absent from the index, and an estimated candidate-set size, without materialising records."* |
| m-4 | Ambiguity | FR-012, §3 money row, ADR-068 O-2 | O-2 resolves money as *"amount (integer minor units) + ISO-4217 currency + declared scale"*. FR-012 says *"amount, ISO-4217 currency and declared scale"* — dropping "integer minor units", the part that makes FR-013's exactness achievable. | Restore "integer minor units" to FR-012. |
| m-5 | Ambiguity | §3 vs FR-025 | The response fields are called *"completeness verdict and problem list"* in §3/FR-025/FR-091 and `complete`/`problems` in ADR-068 D13. Contract-first (FR-090) means these are literal field names. | Use the wire names throughout. |
| m-6 | Incompleteness | FR-063, FR-025 | FR-063 requires a page-size clamp to be *"reported"*, but no FR ties a clamp to `complete: false`. ADR-068 D15.1b does: *"`complete: false` is set for **every** one of these."* The spec drops that. | Add: every bound breach — clamp, refusal, truncation — sets `complete: false` with a reason. |
| m-7 | Incompleteness | FR-044, `record_query` | Only *mutating* tools are audited. `record_query` is the strongest read primitive the product has (typed cross-record aggregation with relation traversal) and is unaudited. ADR-067 has the same posture for search, so this is inherited rather than introduced. | Note the decision explicitly rather than leaving it implicit, or audit read tools at a coarser grain. |
| m-8 | Incompleteness | FR-081, §0 correction 3 | FR-081 requires *"zero repaired pairs on a fresh install."* `repairAndValidateToolPolicyCoverage` is a `pkg/gateway` helper; a test calling `config.RepairIncompleteToolPolicyCoverage` directly proves the function, not the boot posture. The spec does not say which. | State that the test must run the real boot-path helper against the real `coreagent.SeedConfig` output. |
| m-9 | Process | §1 | The spec records that GitNexus was not consulted and calls it a gap. `CLAUDE.md` mandates `impact` before editing any symbol, and FR-020 edits `buildIndexMapping`, whose blast radius §1 itself calls *"everything that opens the index."* | Run `impact({target: "buildIndexMapping", direction: "upstream"})` before W2 and record the result in §1 rather than the gap. |

### OBSERVATIONS

| ID | Lens | Finding |
|---|---|---|
| O-1 | Overcomplexity | `record_explain` (FR-073) exists to prevent a confident wrong answer, but FR-023/FR-024 already reject an invalid query **before evaluation** with the valid names listed. Its remaining job is a cost estimate. That may not earn a separate tool in the eight-tool budget; a `dry_run: true` flag on `record_query` would deliver the same value with one fewer tool to policy-seed across ten agents. |
| O-2 | Overcomplexity | The spec introduces seven property types, arity, absence semantics, temporal validity, sub-records, two-level grouping, relation traversal, a view format, a `.base` importer and nine tools — all before a single record exists in the product. W1–W3 (schema, typed query, relations) would answer ADR-068 §1.2's motivating question. W6's contents (D11, D12, views, importer) are the four items with the least prior art and, per A-4/A-5, the least specification. |
| O-3 | Overcomplexity | Two schema surfaces now exist (tool policies, record schemas), which ADR-068 §4.2 already flags. The spec adds a third representation of the record schema in FR-072's compact text format. Three renderings of one truth is where drift starts. |
| O-4 | Positive | `.omnipus-vault` is **already** in `scanSkippedDirNames` (`scan.go:69`) as a collection marker, so FR-001's schema location does not pollute the note index. The spec did not claim this; it happens to be true and is worth stating in §0 so a later reader does not "fix" it. |
| O-5 | Positive | FR-030's *"stored on disk as a quoted wikilink"* is already satisfied by `authorEncodeScalar`, which quotes `[[…]]` precisely because *"under-quoting turns a wikilink `[[Note]]` into a YAML flow sequence and loses the value."* The reuse claim holds for this one case. |

---

## 2. Structural integrity results

| Check | Result | Evidence |
|---|---|---|
| Every user story has at least one acceptance scenario | **PASS** | US-1..US-8 all carry numbered scenarios |
| Every scenario traces to an FR | **FAIL** | US-1.1 → FR-005, which appears in no §6 row (M-5) |
| Every FR appears in the traceability matrix | **FAIL** | FR-005 absent (M-5) |
| Every named test appears in the test plan | **FAIL** | 4 of 23 missing (M-4) |
| Every planned test traces to an FR | **FAIL** | Test 6 traces to *"see §8"*, not an FR (m-1) |
| Test datasets cover boundaries, edges, errors | **PARTIAL** | DS-1/DS-2 strong; DS-3 includes a BOM case with no FR (M-11) and a `2^53+1` case that is unachievable (C-4). No dataset for candidate-set size, the actual perf variable (M-1) |
| Regression impact explicitly addressed | **PARTIAL** | Named, but the two seam tests cannot fail (M-8) and the real seam (mapping freeze) is untouched (C-3) |
| Success criteria measurable, no subjective language | **PARTIAL** | SC-001..SC-010 are all measurable in form; SC-007/SC-008 are unfalsifiable in substance (M-1) |
| Every tool in FR-070 has a requirement | **FAIL** | `record_log` has none (M-19); `record_view_import` is required but not in FR-070 (C-1) |

---

## 3. Test coverage assessment

**Can the 21 planned tests fail?** Assessed individually. Four cannot fail for the reason
they are listed, and they are not the marginal ones:

| Test | Can it fail? | Why |
|---|---|---|
| 18 `TestTools_NamesHaveNoDots` | **No** | Duplicates an already-green assertion over 35 existing tools; passes with zero record tools (M-7) |
| `TestIndex_NoSecondStore` (§6 only) | **No** | Asserts absence of a SQLite import that has never existed in `pkg/knowledge` (M-6) |
| `TestKnowledgeSearch_UnaffectedByRecordFields` (§7 regression) | **No** | `IncludeInAll=false` on every field makes non-interference structural (M-8) |
| `TestIndex_RebuildWithRecordFields` (§7 regression) | **Unclear** | Cannot be written meaningfully until C-3 decides what a rebuild *is* |
| 21 `TestRecords_PerfAtFiftyThousand` | **Not reliably** | Passes or fails on unspecified query selectivity (M-1) |
| 11 `TestID_ConcurrentAllocationIsCollisionFree` | **Only if cross-process** | Planned as "integration"; SC-005 needs two OS processes (M-13) |
| 6 `TestComparisonTruthTable` | **Yes — and it is the strongest item in the spec** | §8's "derived from this document, never from running the implementation" is exactly right; keep it verbatim |
| 12 `TestWrite_ByteIdenticalOutsidePatchedSpan` | **Yes, but under-specified** | Cannot be written until "patched span" is defined (M-20), and the corpus contains a BOM case with no expected value (M-11) |
| 1–5, 7–10, 13–17, 19, 20 | **Yes** | These are well-formed |

**Missing test levels.** There is no test at any level for: FR-005 (inert on an unschema'd
vault), FR-020's actual mechanism, schema-change invalidation (C-6), index-mapping upgrade
(C-3), segment de-duplication (M-3), `record_log` (M-19), `record_explain` (m-3),
`record_schema`'s output format (m-2), or the scope-before-validation ordering (M-14).

**Missing negative tests.** US-2 has five negative scenarios and one positive; that balance
is good. US-4 has no test for a write that would collapse a structured value to a scalar
(M-20). US-8 has no test for the error-channel probe (M-14).

**Missing concurrency tests.** Test 11 covers ID allocation. Nothing covers two agents
writing **different properties of the same record** concurrently — which §9 holdout 6
exercises but §9 is explicitly excluded from development. `author.go`'s header records that
the last time this was assumed rather than tested, *"twelve concurrent writers all told they
had succeeded with one surviving on disk."*

---

## 4. STRIDE summary

| Component | Threat | Finding |
|---|---|---|
| `record_query` error channel | **Information disclosure** | FR-024 lists valid schema names on rejection; ordering vs FR-062's scope check unspecified — schema enumeration from outside the workspace (M-14) |
| `record_query` candidate evaluation | **Denial of service** | Discovering the 10,000 breach costs a full retrieval; refusals not stated to count against the limiter (M-15) |
| Schema files in the vault | **Tampering** | Agent-writable via `bash`/filesystem tools; no audit requirement for schema mutation (M-16) |
| Record writes | **Repudiation** | Covered — FR-043/FR-044 require version token + audit including the refusal (grounded: `NoteContentVersion` exists at `author.go:322`) |
| Record reads | **Repudiation** | `record_query` unaudited (m-7) |
| Relation resolution at index time | **Elevation of privilege** | FR-060 scopes *tools*; the indexer is not a tool, and no FR scopes index-time wikilink resolution (M-21) |
| ID allocator | **Spoofing (identity confusion)** | Reconcile-to-max reuses a deleted record's ID; a relation then resolves to a different record (C-8) |
| Rate limiting | **Denial of service** | Which limiter, which key, and whether mutations count is unstated (M-12) |

---

## 5. Unasked questions

1. **What is the global `sandbox.tool_policies` seed for the eight (nine) record tools?**
   FR-080 requires an explicit entry *"for every seeded agent"* but never states the ceiling,
   which is what actually determines whether a sparse map grants or denies (M-9).
2. **What happens to the record layer when the index is stale or rebuilding?** No FR, no
   signal, no `complete: false` requirement (M-17).
3. **What invalidates the index when a schema changes?** Schemas live in a directory the
   scanner never walks (C-6).
4. **Does a logged interaction and a harvested mention of the same event double-count?**
   FR-050..053 and `record_log` are specified independently and never reconciled (M-19).
5. **Is ID reuse after deletion permitted?** D7 says never reused; FR-038 makes it inevitable
   (C-8).
6. **Which resolver turns a wikilink into a record ID, and does it run inside scope?** (M-21)
7. **What is the downgrade path once an index carries record fields?** (M-18)
8. **How many bleve documents is a 50,000-record vault?** Every bound is stated in records;
   the index counts segments (M-3).
9. **At what selectivity are the p95 targets measured?** (M-1)
10. **Is the `< 96 MB` record budget on top of, or inclusive of, ADR-067's `< 64 MB`?** (M-2)

---

## 6. Verdict

**BLOCK.**

C-1 through C-8 must be resolved before implementation begins. C-2, C-3 and C-5 are the
same category of defect that put ADR-068 revision 1 into BLOCK — a specification asserting
that existing code does something it does not do — and this draft's §0 reuse table is where
they entered. The line counts in that table are all exactly right (390 / 1,183 / 1,277 /
603 / 1,135 / 13, every one verified); what is wrong is the claim about **capability**, not
about size. Verifying that a file exists at the stated length is not verifying that it does
the stated job.

The strongest thing in the document is §8. `TestComparisonTruthTable`, written from the
specification before the comparators exist, with the rule that *"one that requires editing
the table is a specification change and must be argued as one"*, is the correct treatment of
the highest-risk item and should survive the revision unchanged.

