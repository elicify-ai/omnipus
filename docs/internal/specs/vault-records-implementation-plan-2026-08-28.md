# Vault records — implementation plan

**Branch:** `feat/library-improvements` · **Authority:** ADR-068 revision 8, spec Draft 6
**Directive:** no deferrals, no shortcuts, no postponement. Ship-to-production standard.
**Concurrency ceiling:** 6 agents per stage.

---

## Baseline, measured

| | |
|---|---|
| `pkg/records` | 13,760 lines, 11 non-test files, **0 consumers** |
| `vault_*` tools | 0 |
| `knowledge_*` tools | 9 (to retire in S5) |
| SQLite properties index | does not exist |
| `money` (to delete) | 1,557 lines |
| Invented operators (to replace) | 7 |

The type core is built and tested. Nothing consumes it, and three operator rulings
(no money, SQL operator names, lexical enum order) mean part of it must be **unbuilt**
before anything is built on it.

---

## Stage 1 — Unbuild and foundations · 6 agents

Every agent owns a disjoint file set. No agent waits on another.

| # | Owns | Delivers | Exit |
|---|---|---|---|
| **1** | `pkg/knowledge/index.go`, `manifest.go` | **W0** — forced rebuild of indexes written under the corrupt segment format | an index built under the old format is rebuilt on upgrade, not opened; the search that panicked returns results |
| **2** | `pkg/records/schema.go`, `value.go`, `decimal.go`, `money*.go`, `contracts/` | **Type refactor** — delete `money` entirely; split `number` into `integer` + `decimal`; enum ordering becomes lexical; Unicode folding via `x/text/cases.Fold()` | type count is seven; no `money`, `cross_currency` or `money_scale_mismatch` anywhere including generated wire types; the six folding pairs pass incl. the Turkish negative |
| **3** | `pkg/records/filter.go`, `compare_oracle.go`, `compare_*_test.go` | **Operator refactor** — the seven invented operators become SQL's `=`, `<>`, `<`, `<=`, `>`, `>=`, `LIKE`, `IN`, `IS NULL`, `IS NOT NULL`; unsupported SQL refused naming the supported set | truth table regenerates and passes; a `JOIN`/`COALESCE` attempt is refused by name, never an empty result |
| **4** | `pkg/records/propindex/**` (new) | **SQLite properties index** — schema, write path, `source_hash`, rebuild-from-notes | delete the index, reopen, identical query results; both indexes measured inside 64 MB on Linux and macOS |
| **5** | `pkg/records/stub_*.go`, `pkg/coreagent/core.go`, `CLAUDE.md`, ADR-067 | **Platform posture + house rules** — build-tagged stub that refuses by name on SQLite-less targets; catalog-count assertion; the house-rule edits W1 owes | a SQLite-less build refuses by name and never returns empty; `CLAUDE.md` no longer says SQLite is isolated to WhatsApp |
| **6** | `docs/internal/specs/uat-*` | **UAT plan** — every feature and edge case, written for a human tester | covers all six tools, all seven types, every refusal path, and the failure modes UAT found last round |

**Gate:** `gofmt` clean · `go build` · `go vet` · `pkg/records` + `pkg/knowledge` tests · lint · `verify-contracts`.

---

## Stage 2 — Retrieval and the comparator · 6 agents

| # | Delivers | Exit |
|---|---|---|
| **1** | `ScoringModel = bm25` + the thirteen false BM25 doc/log corrections | no `.go` file attributes BM25 to bleve while `ScoringModel` is unset |
| **2** | Fielded indexing (title, name, headings, property keys, property values, body) + the freshness stored field | a field query on a property key is possible at all — it is not today |
| **3** | BM25F weighting + RRF fusion (BM25 + exact-name + recency + backlink degree) | clears its nDCG@10 threshold against plain BM25 **or does not ship** |
| **4** | `vault_find` — words, typed filters, grouping, `kind: task`, problem report | a type mismatch is never a silent empty result |
| **5** | **The Go comparator that decides every comparison** + AC-8.10's emitted-SQL guard | the guard fails on any comparison operator, `LIKE`, `IN`, `GROUP BY`, `ORDER BY`, aggregate or `COLLATE` outside the narrowing allow-list |
| **6** | `vault_describe` incl. `check_integrity` and its bounds | unknown property refused naming the valid ones |

**Gate:** as Stage 1, plus the six-mutation table produced **as an artifact**, not a pass.

---

## Stage 3 — Relations, reads, writes · 6 agents

| # | Delivers |
|---|---|
| **1** | Relations, inverses, relation grouping |
| **2** | `near` + `hops`, and its composition with filters |
| **3** | `vault_read` — full note, sections, version token, links+backlinks inline |
| **4** | `vault_edit` — byte-preserving writes, list-valued splice, `create`'s template argument |
| **5** | `replace_body` — anchor-addressed, ambiguity refused naming both matches |
| **6** | The **trash convention** — where a trashed note goes, what happens to inbound links, whether the index forgets it. Written and reviewed **before** any tool exposes it |

---

## Stage 4 — Control plane and surface · 6 agents

| # | Delivers |
|---|---|
| **1** | `vault_restructure` — rename, move, trash operation |
| **2** | `vault_configure` — record-type and saved-view authoring |
| **3** | D18 policy seeding + ACs — all three policies independently settable |
| **4** | **Retire the nine `knowledge_*` names** — catalog, global ceiling, all five seed maps, every skill and prompt |
| **5** | Record table UI — grouping, related-records panel, problem banner, drill-down, cell edit |
| **6** | Index-state snapshot (the live-only defect) + operator/CLI saved-view importer |

**Exit:** no `knowledge_*` name anywhere; the catalog assertion reads 95.

---

## Stage 5 — Review · 2 agents, distinct scopes

Two `/code-review large` passes over the **entire branch diff**, deliberately non-overlapping:

- **Scope A — correctness and data integrity:** the comparator, the two indexes and their divergence, the write path, byte preservation, refusal paths, the thirteen rules.
- **Scope B — surface and safety:** tool contracts and descriptions, policy seeding and tiers, wire types, the UI, error copy, and everything an agent or operator can reach.

## Stage 6 — Fix · up to 6 agents

One agent per finding cluster, file-disjoint, each mutation-verifying its own fix.

## Stage 7 — UAT execution · up to 6 agents

Parallel testers driving **Playwright against the real UI**, each on its own gateway
instance and port. They impersonate human testers: they look, they judge, they report what
they see. Instructions carried forward from the last round, which earned them:

- verify your own console capture before reporting "no errors"
- *"I restarted and it worked"* is a **failure**, not a pass
- assume every failure is real; never write one off as flakiness
- do not diagnose, do not work around, do not read source to explain what you saw

## Stage 8 — Fix, then CI to green

Parallel fixers, then full CI. Iterate until every job passes. **Real defects, not flakiness.**

---

## Standing rules for every agent

1. **Mutation-verify every test — and when the test is a GUARD, mutate PRODUCTION code, not a fixture.**
   Break the thing it guards, watch it fail, restore, confirm green. **A guard can pass every fixture
   and still be blind to the exact bug it exists to catch**, because fixtures are written to match the
   author's mental model and are therefore minimal — real code is not.
   *Worked example, Stage 1.* The caller guard for the properties-index refusal
   (`pkg/records/propindex_caller_guard_test.go`) first asked *"does the error name appear in any
   return of the enclosing function"*. Nine synthetic fixtures passed. Mutating a **real** call site
   — `pkg/records/propindex/sqlite.go`'s `Open`, guard clause changed to `return nil, nil`, the exact
   FR-020h bug — left the guard **silent**: `Open` reuses `err` for its later `sql.Open` failure, so a
   legitimate return forty lines below covered the swallowed branch. No fixture would ever carry that
   incidental reuse. The rule is now branch-scoped.
   **Cheapest check: apply the mutation to a copy of a real caller, never only to a fixture you wrote.**
   *(This belongs in `docs/internal/false-green-patterns.md`, which is on the release lineage and not on
   this branch — fold it in when the two meet.)*
2. **A pattern-matching guard must be probed ADVERSARIALLY, not just fixtured — start with whitespace.**
   Extending the fixture set cannot find this class, because a fixture and the guard that passes it
   were written by the same person in the same sitting, in the same style. **Fixtures carry the
   spacing their author happened to type; real code carries whatever was typed.**
   *Worked example, Stage 2.* The AC-8.10 emitted-SQL guard
   (`pkg/records/propindex/sqlgate_test.go`) exists to prove no comparison reaches SQLite. Its
   comparison-operator regex spelled the operators as `\s=\s`, `\s<\s`, `\s>\s` — **whitespace
   required on both sides**. So every one of these was reported CLEAN by the control whose entire
   purpose is to catch them:
   `WHERE p.v_text=?` · `WHERE p.v_num>?` · `WHERE p.v_num<10` · `ON p.note_id=n.note_id AND p.v_text='x'`
   Twelve deliberate violation fixtures passed, 100% green, while the hole was open. It was found by
   feeding the guard hostile inputs, not by adding a thirteenth fixture.
   **Cheapest check: take each fixture the guard already catches and re-run it with the whitespace
   removed, the operator spelled differently, and the construct nested one level down.** Two further
   shapes no fixture covered and this probe found immediately: a correlated subquery hiding the
   comparison inside `EXISTS (SELECT 1 … AND p.v_text = ?)`, and a scalar function doing the
   comparing (`WHERE instr(n.path, ?) > 0`). Both are how a determined implementer gets a predicate
   past a regex **without meaning to**.
   Fix pattern: canonicalise before matching (`normaliseOperators`) rather than enumerating spellings
   — an allow-list benefits equally, so `record_type=?` is recognised as the legitimate narrowing
   predicate it is instead of flagged for its spacing. Then pin the unspaced forms as fixtures so
   removing the canonicalisation fails loudly rather than silently reopening the blind spot.
3. **A null result means nothing until the instrument is shown to have the POWER to detect the effect.**
   This is the same failure as rules 1 and 2 and it is the worst-behaved member of the family,
   because **its output is indistinguishable from a careful negative finding.** A collision that
   shrinks a corpus at least leaves a wrong count somewhere. A blind instrument returns a clean,
   plausible, publishable zero — and a reader has no way to tell "we measured this and it does not
   help" from "this measurement could not have said anything else."
   *Worked example, Stage 2 (D21.3 ranking).* The BM25F field-weighting row of the ablation came back
   **byte-identical to the plain-BM25 baseline on all 60 committed queries.** Read as a result, that
   is a clean null: field weighting does not help. It was not a result. Every note in the generated
   corpus repeated its own title inside its own body, so **title and body agreed on every document**,
   and re-weighting two fields that agree cannot reorder anything. The corpus was structurally
   incapable of expressing the effect the row claimed to measure. Had it been reported, we would have
   shipped without field weighting on the strength of a measurement that could not have said
   otherwise.
   A second instance in the same file, same session: the exact/prefix **name signal** compared the
   whole query phrase against the note name, so it fired on **0 of 60** real multi-term queries. Its
   ablation row was likewise identical to the row without it.
   **The trap underneath: a whole-metric power check does not cover a per-signal blind spot.** There
   WAS a headroom guard (`TestRank_EvalHasHeadroom`, asserting the baseline is neither at ceiling nor
   at floor) and it passed throughout. Aggregate headroom says the eval can detect *something*; it
   says nothing about whether the corpus can express *this signal*.
   **Cheapest check: before trusting any null, plant a positive and confirm the instrument detects
   it** — construct the case the signal exists to decide, and assert the outcome changes. If it does
   not, the signal is broken or the corpus is blind, and either way the null is uninformative.
   Written as a test per signal, not once per suite.
4. **Capture exit codes without a pipe** — `cmd > log 2>&1; echo "exit=$?"`.
   Corollary, learned in Stage 2: **`go vet` is not a build check.** It printed
   `import cycle not allowed in test` and exited **0**. Only `go test` proves a test binary links.
5. **Commit COMPILING states incrementally.** This machine has slept mid-write five times today and
   killed four agents at the research→write boundary. In a shared worktree an uncommitted broken
   build is not a private state — it is a stop-the-world event for every agent whose tests import the
   package, and they cannot tell whose edit caused it without asking.
   **When someone else's uncommitted work breaks your verification, do not guess at their
   half-written symbols and do not edit their files.** Verify against your own committed HEAD in a
   detached worktree — `git worktree add --detach /tmp/verify HEAD` — which contains only committed
   work, so their untracked files do not exist there. Mutate freely, `git checkout -- .` to restore.
   State the SHA in any mutation table produced this way: it describes the version tested.
6. **Never run the full Go suite** — it OOMs. Build tags `goolm,stdjson` are mandatory.
7. **Author as the human**, no agent co-author trailer — the CLA gate hard-fails on it.
8. **No deferrals.** A finding is fixed or it is a stated open risk with a reason. Never "later".
9. **A guard that cries wolf gets disabled — so pin the false positives too.** Over-tightening is the
   obvious over-correction to rule 1 and is its own defect: the same Stage 1 guard's first
   branch-scoped rule flagged `err := Require(...); return nil, err`, which propagates correctly.
   Every guard needs **negative** fixtures — correct shapes asserted NOT to fire — or the next person
   it trips spuriously will delete it rather than debug it.
