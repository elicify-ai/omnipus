# Spec Grill #2 — Findings Report

**Spec under review**: `docs/internal/specs/adr-057-session-unification-spec.md` v2 (2957 lines, commit `883f1efc`)
**Source ADR**: `docs/internal/architecture/ADR-057-session-parent-child-parity.md` v4 (737 lines, Accepted)
**Prior review**: `docs/internal/specs/adr-057-session-unification-spec-review.md` (grill #1, verdict BLOCK)
**Reviewed**: 2026-08-03, branch `feature/plan-swimlane-board`
**Mode**: `plan-spec`
**Grill**: #2 of 2 — FINAL gate before parallel implementation waves

> **Note on filename**: this report is written to `…-review-2.md` rather than overwriting
> `…-review.md`, because v2's changelog cites grill #1's review by path as the record of what
> was corrected. Destroying it would break that citation.

---

## 1. Executive Summary

v2's corrections to the six CRITICAL findings are, on the merits, **real**: C-1's two-key
`InheritFrom`, C-2's log-record-plus-delta, C-4's unforced-flush assertion, C-5's field-group
isolation, C-6's release-between-shards protocol and M-9's both-halves-in-one-run are each a
genuine fix that fails against a broken implementation. AC-1…AC-22 are machine-verified
byte-identical to the ADR. The structural counts all check out. The M-7 rebuttal is correct
against the tree.

The problem is that v2 fixed **the findings** rather than **the defect classes**, and the same
three classes recur in the corrected material. The ownership table's disjointness claim is true
and independently verified — but disjointness was never the failure mode. **Exhaustiveness was**,
and it is still false: ten more files that FRs require changing have no owner, six of them are
hard compile breaks the moment W15 or W13 lands. The new nested-listing scope (US-19, 4 FRs,
6 BDDs, 5 tests, unit U24) is specified with a requirement — "each layer MUST bound its own cost
rather than loading the full set and slicing" — that is **not implementable** against a store
whose list method sorts an unordered map and a loop method that k-way-merges and de-duplicates
N stores. And W3's conversion boundary names five transcript writers when the tree has twenty,
three of them in unowned files — which means the "silent create" the P0 gate story exists to
eliminate survives the migration in the task-executor, goal-loop and hand-off paths.

**Findings: 4 CRITICAL, 12 MAJOR, 6 MINOR, 3 OBSERVATION.**

**Verdict: BLOCK.**

---

## 2. Regression check on grill #1 (attack priority 1)

Each claimed correction was re-derived against the tree before being accepted.

| # | Claim | Verdict | Evidence |
|---|---|---|---|
| **C-1** | `InheritFrom` two-key op; BDD-88 asserts absent-before | **FIXED, genuinely** | `pkg/security/approvalgrants.go:112-131` verified: one `sessionID` for source (`:118`) and destination (`:122`); silent `return` at `:119-120`. A single-key re-key cannot satisfy BDD-88, and BDD-89/#84 make the empty-source branch loud. FR-079 is the right tripwire |
| **C-2** | Test #7 now asserts the WARN record + counter delta | **FIXED, genuinely** | `pkg/agent/turn.go:1296-1298` verified: `abandonedWritesSuppressed.Add(1); return` with **no log call**. The doc comment at `:1289-1291` claims "Debug-level logging is emitted when the no-op fires" — false for this branch (only the `transcriptStore == nil` branch at `:1300-1302` logs). The log record is genuinely new, so #7 **does** fail pre-change. Counter confirmed at 7 sites (`turn.go:866,1097,1172,1226,1297,1496`; `loop.go:7596`) |
| **C-3** | Binding rule 4 + 11-row bounds table + #91 mutation gate | **PARTIALLY fixed** — see **M2-5**. The eleven enumerated gates are properly bounded; **two new gates added in v2 (#106, #104) are unbounded**, absent from the table and outside #91's scope. SC-003's corrected `rg` still cannot return zero — see **m2-4** |
| **C-4** | FR-083 / BDD-93 / #89 unforced flush + negative control in #20 | **FIXED, genuinely** | BDD-93 forbids every external trigger *and* a test-driven tick, so it is the one scenario a never-started flusher goroutine fails. Dataset row 9 and the two-sided row 7 close the SIGKILL hole |
| **C-5** | FR-084 field-group-only cache mutation | **FIXED, genuinely** | `pkg/session/unified.go:797` verified: `us.metaCache[sessionID] = meta.Clone()` is a whole-document refresh, doc'd at `:780`. #90 fails if any targeted writer keeps that shape |
| **C-6** | FR-082 release-then-acquire; #88 instrumented lock order | **Fixed in the requirement, not in the mechanism** — see **M2-6**. `createSessionLocked`'s literal (`:448-460`) confirmed to carry no `Owner`, so the two-session read is real and FR-082's protocol is correct. The *test* needs a production seam nobody is required to build |
| **M-1** | 5 unowned files given owners; 4 pagination layers assigned | **NOT fixed** — see **C2-1**. The five named files got owners; exhaustiveness was never re-derived |
| **M-2/M-3** | U20 split; U7/U9 declare U17 | **Fixed for the named units, class recurs** — see **C2-4** (U17→U6, U19→U8, U14→U8) |
| **M-4** | Rule 5: new tests in new `_adr057_test.go` files | **Fixed for Go.** Silent on the SPA test files the change breaks — see **M2-9** |
| **M-5** | `readMetaLocked` signature frozen | **Fixed.** Verified `readMetaLocked` (`:764-774`) already errors via `readUnifiedMeta` on a missing `meta.json`, so U2's existence predicate holds both pre- and post-U5 |
| **M-6…M-8, M-10…M-14, m-2…m-5, o-1, o-2** | various | **Fixed as claimed** (spot-checked; no residual) |
| **m-1** | Rebutted and inverted to `:572-575` | **Rebuttal is CORRECT, application is INCOMPLETE** — see **m2-1**. Verified `lifecycle.go:571` is the `AgentID` clause's closing brace and the `ParentAgentID` block is exactly `:572-575`. But FR-022 (line 2482) and BDD-22 (line 1204) **still say `:571-575`** |
| **M-9** | BDD-36/#56 assert both files in one run | **FIXED, genuinely** — the child-count-non-zero half is real and in the same run; SC-004 mirrors it |

---

## 3. Findings

### CRITICAL

---

#### C2-1 — The ownership table is disjoint but still not exhaustive: ten unowned files, six of them hard compile breaks

**Lens**: Incompleteness / Inoperability · **Section**: Ownership table (lines 785-812), "Disjointness proof" (line 812), M-1 resolution row (line 45)

The disjointness claim was verified independently and **is true**: every path in the table appears
in exactly one row, with the declared `U4→U5→U6` chain on `unified.go` as the sole exception. The
wave DAG was also verified acyclic with no unit depending on a later wave *as declared*.

That is not the property that matters. Grill #1's M-1 was an **exhaustiveness** failure, and v2
closed it by adding owners for the five files the review happened to name. Re-deriving it from the
FRs and from the symbols W13/W15/W16/W23/W24 change produces ten more:

| File | Why it must change | Compile break? | Owner |
|---|---|---|---|
| `pkg/session/retention_sweep.go` | Uses `us.mu` **3×** (`:136`, `:141`, +1) and `metaCache`/`cacheLoadFailures` **2×**; it is the **only** file besides `unified.go` with `UnifiedStore` methods. FR-048 deletes `us.mu`. FR-050, SC-038 and Ambiguity item 13 name `RetentionSweep` by name as needing index-order shard acquisition | **YES** | **none** |
| `pkg/commands/runtime.go` | `AgentLoopInterface` declares `InterruptSession(sessionID, hint string) ([]string, error)` at `:39`. FR-041 collapses that method into a scope-taking entry point | **YES** | **none** |
| `pkg/sysagent/tools/deps.go` | `:197` declares `ListSessions func() ([]*session.UnifiedMeta, []error)`, wired to `agentLoop.ListAllSessions`. FR-092/cross-unit-request changes that signature to `(limit, offset int, parentSessionID string)` | **YES** | **none** |
| `pkg/gateway/gateway.go` | `:1902` `ListSessions: agentLoop.ListAllSessions` (the wiring for the field above); `:3109` `store.ListSessions()` | **YES** | **none** |
| `pkg/gateway/rest_stats.go` | `:92` `a.agentLoop.ListAllSessions()` | **YES** | **none** |
| `pkg/agent/goal_triggers.go` | `:468` `store.ListSessions()` | **YES** | **none** |
| `pkg/agent/goal_loop.go` | `:770` `store.ListSessions()`; also `:739`, `:823` `AppendTranscript` (see C2-3) | **YES** | **none** |
| `pkg/config/config.go` (+ `pkg/config/defaults.go`) | FR-067 (promoted to **MUST** by operator decision 2) requires a **new** flush-interval config key defaulting to 5 s; #105 and SC-048 gate it. No `pkg/config/**` row exists | no, but unowned | **none** |
| `pkg/agent/task_executor.go` | **7** non-test `AppendTranscript` sites (`:554, :687, :718, :1084, :1170, :1278, :1989`) — see C2-3 | no | **none** |
| `pkg/tools/handoff.go` | `:205`, `:386` `AppendTranscript` — see C2-3 | no | **none** |
| `pkg/agent/runner/driver_claude.go`, `driver_codex.go`, `driver_opencode.go` | FR-029 / AC-17(c) — see M2-2 | no | **none** |

Under Rules 1–3 an implementing agent has **no one to request these from**, and Rule 3 (`git add`
only what you own) leaves the change either unmade or made by whoever hits the compile error
first — in a shared working tree where this session has already observed agents reverting each
other. Every one of the six compile breaks lands the moment U4 (Wave B) or U8 (Wave E) merges,
i.e. mid-wave, with three to five other agents writing concurrently.

**Fix**: Do not hand-patch this list. Re-derive ownership **from the symbols the spec changes**,
not from a review's file list: for each of `us.mu`, `metaCache`, `cacheLoadFailures`,
`ListSessions`, `ListAllSessions`, `InterruptSession*`, `InterruptBySessionKey*`,
`AppendTranscript`, `IsDelegateChildEntry`, `Inherit`, `fetchSessions`, run the non-test
reference enumeration and require every file that appears to hold exactly one owner. State the
enumeration commands in the spec so the check is reproducible in review. Add the missing rows
(candidates: `retention_sweep.go` → the U4→U5→U6 chain; `pkg/commands/**` → U8;
`deps.go`/`gateway.go`/`rest_stats.go` → U9 or a new gateway-wiring unit; `goal_loop.go`/
`goal_triggers.go`/`task_executor.go` → U9 or a new unit; `pkg/config/**` → U6;
`pkg/agent/runner/**` → U22, whose current scope is empty — see M2-3).

---

#### C2-2 — US-19's pagination requirement is infeasible as written at the two backend layers, and its own test forbids the only implementation that works

**Lens**: Infeasibility · **Section**: US-19 (line 608), FR-091, FR-092, BDD-102 (line 1877), BDD-103, BDD-106, #100, #98, #99, #108, SC-043

BDD-102's binding clause is:

> **But** it does **not** load the whole set and slice it in memory — the point of the requirement
> is that the cost is bounded at every layer, not only at the last one.

FR-092 repeats it: each of the four layers "MUST bound its own cost rather than loading the full
set and slicing". Verified against the two backend layers:

**Store layer** (`UnifiedStore.ListSessions`, `pkg/session/unified.go:1247-1293`). The result is
built by iterating `us.metaCache` — a **Go map**, i.e. unordered — into a slice, then
`slices.SortFunc` by `UpdatedAt` descending (`:1283-1291`). There is no on-disk or in-memory index
ordered by recency, and the reconcile pass is an `os.ReadDir` over the whole base directory
(`:1251`). A recency-ordered page therefore **requires** materialising and sorting every session.
Bounding the cost needs a new ordered index; **no FR requires one, no unit is scoped to build one,
and no dataset covers its maintenance.**

**Loop layer** (`AgentLoop.ListAllSessions`, `pkg/agent/loop.go:5046-5090`). It merges the shared
store with **every** legacy per-agent store, de-duplicates by session id against `sharedIDs`, and
then sorts the union by `UpdatedAt` (`:5086-5088`). Bounded-cost pagination over that shape is a
k-way merge with a per-store cursor and a cross-store dedup set — a genuinely new algorithm. The
spec gives it one cross-unit-request line ("paginated `ListAllSessions(limit, offset int,
parentSessionID string)`") and nothing else: no ordering contract across stores, no cursor
stability rule, no statement of what happens when a legacy store errors mid-page.

Two more FR-091 obligations have the same shape:

- **`child_count` per root row.** The session store has no parent index — `ParentSessionID` is a
  brand-new `meta.json` field (FR-008). Counting children for a page of roots means reading every
  session's meta, i.e. O(all sessions) per page. U13's parent index is over **lifecycle records**,
  not sessions, and no FR bridges them.
- **BDD-106 / #108, orphan-as-root.** Deciding that a child's `ParentSessionID` "no longer
  resolves" requires resolving every candidate's parent — again O(all sessions).

So #100 (`TestSessionListLayers_EachHonoursPaging`, "none loads-all-then-slices") is either
impossible to pass or will be written to assert only `len(rows) <= limit`, which the forbidden
implementation satisfies exactly. That is a vacuous test in brand-new scope, i.e. the C-3 class
in the material added to fix C-3's siblings.

**Fix**: Either (a) add the missing FR — a recency-ordered session index maintained inside the
store's write paths (the `lifecycle_index.go` precedent U13 is already building), with an owner,
a wave slot, and a dataset covering insert/update/delete/orphan; plus a stated cross-store
ordering and cursor contract for `ListAllSessions`; **or** (b) relax FR-092/BDD-102 to bound the
cost only at the boundary layers, explicitly accept an O(N) in-process sort at the store and loop
layers, state that as the design, and rewrite #100's assertion to something checkable. Do not
ship the current wording, which mandates a property no scoped unit can deliver.

---

#### C2-3 — W3's conversion boundary is 5 of 20 real `AppendTranscript` call sites, and the lenient primitive survives — so the P0 story's own governing constraint outlives the migration

**Lens**: Incompleteness / Incorrectness · **Section**: US-1, FR-001, FR-002 (line 2453), Symbols table (line 205), AC-1

FR-002 enumerates the writers to convert: `turn.go:1130, :1208, :1270, :1325`,
`websocket.go:4256`, plus `external_dispatch.go` and `approval_transcript.go` (the latter two are
themselves wrong — see M2-3). The Symbols table states the store change as
"`AppendTranscript` … **modified** — gains a strict **sibling** (W3)", i.e. the lenient
silent-create primitive **remains**.

Enumerated against the tree, non-test `AppendTranscript(` call sites:

| File | Sites | In FR-002? | Owner |
|---|---|---|---|
| `pkg/agent/turn.go` | `:1130, :1208, :1270, :1325` | yes | U3 |
| `pkg/gateway/websocket.go` | `:4256` **and `:1642`** | only `:4256` | U11 |
| `pkg/agent/task_executor.go` | `:554, :687, :718, :1084, :1170, :1278, :1989` | **no** | **none** |
| `pkg/agent/loop.go` | `:5923, :6107` | **no** | U9 |
| `pkg/agent/goal_loop.go` | `:739, :823` | **no** | **none** |
| `pkg/agent/cancel.go` | `:354` | **no** | U15 |
| `pkg/tools/handoff.go` | `:205, :386` | **no** | **none** |

**Fifteen non-test sites keep the primitive whose behaviour the spec's opening section calls "the
canonical mechanism" of silent failure** — `fileutil.AppendJSONL`'s `MkdirAll`
(`pkg/fileutil/file.go:207-210`) creating the directory, the meta read failing, `slog.Warn`, and
`return nil` (`pkg/session/unified.go:819-823`). Three of the files have no owner at all.

This directly contradicts AC-1's governing text, which is written as a property of
**`AppendTranscript`**, not of a sibling: *"`AppendTranscript` against a UUID with no `meta.json`
returns a non-nil error and creates **no** directory."* Under the sibling design AC-1 is satisfied
only for the five converted callers; a task-executor or `/goal` write against an unminted session
still silently creates. US-1's own rationale — "until that changes, every acceptance criterion in
this document is measured against a primitive that reports success for a lost write" — applies
verbatim to the fifteen.

It also matters concretely for this change: `task_executor.go` is the ADR-053 dispatch path that
this ADR's delegation work sits next to, and `goal_loop.go:739/:823` writes goal verdict
transcripts against a session id resolved elsewhere.

**Fix**: State the W3 conversion boundary explicitly and completely, the way FR-090 does for W20.
Either (a) make `AppendTranscript` itself strict and enumerate all 20 call sites with owners and
per-site error handling (this is what AC-1's wording requires), or (b) keep the sibling, state in
an FR that the lenient primitive is retained **only** for the enumerated legacy call sites, list
them, give each an owner, and add a static gate asserting no **new** caller of the lenient form —
with a positive lower bound per binding rule 4. Add `pkg/agent/task_executor.go`,
`pkg/agent/goal_loop.go`, `pkg/tools/handoff.go` and `pkg/gateway/websocket.go:1642` to the
ownership table either way.

---

#### C2-4 — Three wave-order/undeclared-dependency violations of exactly the M-2/M-3 shape the correction declared closed

**Lens**: Inconsistency / Ordering · **Section**: Ownership table, Integration order (lines 816-850), cross-unit requests (lines 866-886), FR-033, FR-064, FR-041

1. **U17 (Wave A) owes two store-dependent behaviours to units in Waves C–D.**
   FR-064 requires a forced synchronous stats flush on "the child `CloseSession` teardown", and
   FR-033 requires `CloseSession` to clear the child's **`metaCache` entry**. `CloseSession` is
   `pkg/agent/session_end.go:32`, owned exclusively by **U17**, whose `Depends on` column reads
   **"—"** and which lands in **Wave A**. The flush API is U6's (`W24`, **Wave D**); the
   `metaCache` surface is U4/U5's (**Waves B–C**). Verified: `CloseSession`'s body
   (`session_end.go:32-80`) touches `forgetSession`, `approvalGrants.ClearSession`, the recap
   claim and the idle ticker — **it holds no store reference at all**. There is no cross-unit
   request row for either. BDD-67's fourth example and #22 are assigned to **U6 alone**, which
   must not touch `session_end.go` (Rule 1).

2. **U19 (Wave C) owns the file that U8 breaks in Wave E.**
   `pkg/agent/session_messaging_wire.go:166` is
   `dt.SetCancelHooks(al.InterruptBySessionKey, al.InterruptBySessionKeyHard)`. FR-041 (W13, **U8,
   Wave E**) collapses all four interrupt entry points into one taking a mandatory
   `InterruptScope`. U19's work items are W17 / W7b / W6b — **W13 is not among them**, U8 is not
   in its `Depends on`, and there is no cross-unit request row. U19 finishes two waves before the
   signature it depends on changes.

3. **U14 (Wave B) holds the func-typed fields with those same signatures.**
   `pkg/tools/delegate.go:350-354` documents `cancelSoft`/`cancelHard` as holding
   `AgentLoop.InterruptBySessionKey` / `…Hard`. Same omission: U14 lists neither W13 nor U8.

Additionally, `pkg/commands/runtime.go:34-39` declares the interface `AgentLoop` must satisfy, and
`pkg/commands/cmd_cancel_test.go:61` implements a stub of it — both unowned (see C2-1). FR-041 is
therefore a four-file signature change with **one** file owned.

**Fix**: Add `W13` to U19's and U14's work items with `U8` in their `Depends on`, and move both to
a wave after U8 (or split the hook-wiring line item out of U19 into a Wave-F unit). Add
`pkg/commands/**` to U8. For U17: either add a cross-unit request row and a dependency on U6
(moving U17's teardown line items to Wave E), or split U17 into `U17a` (approval store + registry
signatures, Wave A) and `U17b` (`session_end.go` teardown wiring, after U6). Record the flush and
`metaCache`-eviction entry points as frozen contracts from U6/U4 the way `readMetaLocked` is.

---

### MAJOR

---

#### M2-1 — FR-095 has no defined behaviour on a default install, where the resolver it cites falls back to the knob it forbids

**Lens**: Incompleteness / Infeasibility · **Section**: FR-095 (line 2594), operator decision 4, BDD-75, SC-030, #110, AC-10

The M-7 rebuttal is **verified correct**: `getSubTurnConfig` (`pkg/agent/subturn.go:64-69`) reads
`cfg.MaxConcurrent` **unclamped** when `> 0`, and `clampParallelExplicit`
(`pkg/config/config.go:459-468`) caps only the `EffectiveMaxParallelAgents()` fallback at 16. Setting
`agents.defaults.subturn.max_concurrent = 24` does satisfy AC-10's 24/25 topology literally. That
finding is closed.

What the correction did not close is the **unset** case, which is the shipped default:

```go
// pkg/agent/subturn.go:64-69   (the resolver FR-095 cites by line)
maxConcurrent := cfg.MaxConcurrent
if maxConcurrent <= 0 {
    maxConcurrent = al.cfg.Performance.EffectiveMaxParallelAgents()   // <- clamped ≤ 16
}
```

`grep SubTurn pkg/config/defaults.go` returns **nothing** — no seeded default. So on a fresh
install `MaxConcurrent == 0` and the resolver FR-095 names takes the branch FR-095 forbids
("MUST NOT be sourced from `Performance.EffectiveMaxParallelAgents()`"). An implementer has three
readings and the spec picks none:

- call `getSubTurnConfig()` → violates FR-095 on every default install, and #110 goes red;
- read `cfg.Agents.Defaults.SubTurn.MaxConcurrent` directly → the cap is **0** on a default
  install. Is that "refuse everything" or "no gate"? W17's whole purpose is preventing an ungated
  root fan-out from becoming a self-inflicted DoS; "no gate" silently restores the defect, and
  every visible test (#63, #110, SC-030) is run with the key explicitly set to 24, so **none of
  them exercises the default**;
- seed a default → no FR says so, and no value is stated.

Note also that the reused knob's existing semantics are a **per-parent-turn in-turn fan-out**
semaphore (`subturn.go:1051` creates it on the *child*; the guard is `:607` on
`parentTS.concurrencySem`), whereas W17's gate is a **process-global root-level** admission cap.
Reusing the value is fine; the spec should say that the two scopes now share one number and that
this is intentional.

**Fix**: State the default explicitly in FR-095 — either seed `subturn.max_concurrent` in
`pkg/config/defaults.go` (needs an owner, see C2-1) or define the gate's behaviour when the key is
`≤ 0` (e.g. "fall back to `EffectiveMaxParallelAgents()` for the *root* gate only, and #110
asserts the unclamped path only when the key is set"). Add a BDD row and a dataset row for the
unset case; today zero coverage exists for the configuration every install ships with.

---

#### M2-2 — FR-029 / AC-17(c) has no implementation site: the 3P runner spawns without `Setpgid`, and the package is unowned and unmentioned

**Lens**: Incompleteness / Infeasibility · **Section**: FR-029, US-18 AS-4, BDD-86, #68a, AC-17(c), Integration Boundaries → "External CLI (3P) subagents"

FR-029: *"A 3P child's process **group** MUST die with the child."* #68a asserts "Real PIDs in the
child's process group, all gone".

Verified: the external-CLI subprocesses are started in `pkg/agent/runner/` —
`driver_claude.go:147`, `driver_codex.go:121`, `driver_opencode.go:87`, all
`exec.CommandContext(runCtx, binary, args...)` followed by `cmd.Start()`. A tree-wide grep of
`pkg/agent/runner/` for `SysProcAttr`, `Setpgid`, `cmd.Process.Kill` or `Signal(` returns **zero
matches**. `exec.CommandContext` kills only the direct child on context cancel, so today a 3P
child's own subprocess tree survives — which is exactly what FR-029 says must not happen, i.e.
this is real work, not an assertion of existing behaviour.

`pkg/agent/runner/` does not appear anywhere in the ownership table. FR-029 is filed under W9,
assigned to **U14** (`pkg/tools/delegate.go`, `list_jobs_sources.go`, `message_parent.go`) and
**U16** (`pkg/tools/shell.go`, `session.go`) — neither can touch the drivers. The in-house
precedent that would be copied (`pkg/sandbox/hardened_exec_linux.go:39-41` sets
`cmd.SysProcAttr.Setpgid = true`; `hardened_exec_cancel_unix.go` does the group kill) is not
cited by the spec at all.

**Fix**: Add `pkg/agent/runner/driver_claude.go`, `driver_codex.go`, `driver_opencode.go` (and
whichever file will own the group-kill helper) to a unit — U22 is the natural home given its
current scope is empty (M2-3). Cite `pkg/sandbox/hardened_exec_linux.go:39-41` and
`hardened_exec_cancel_unix.go` as the pattern. State the Windows behaviour (the sandbox package
has a separate path) or scope FR-029 POSIX-only the way the store's cross-process assertions are
scoped.

---

#### M2-3 — U22, the unit created to close M-1, has a factually wrong work statement and as scoped has nothing to do

**Lens**: Incorrectness · **Section**: FR-002, ownership table U22 row (line 808), W3 coverage map

U22 is new in v2, created to give `external_dispatch.go` and `approval_transcript.go` an owner.
Its work item is "W3 (`external_dispatch.go:463`, `:550-555`, `:562-564`;
`approval_transcript.go:179`, `:183`)" — convert them to the strict primitive. Verified against
the tree:

- `grep AppendTranscript pkg/agent/external_dispatch.go pkg/agent/approval_transcript.go` matches
  **only two doc comments** (`external_dispatch.go:581`, `approval_transcript.go:166`). **Neither
  file contains a single `AppendTranscript` call.**
- `external_dispatch.go:463` and `:562-564` are `childTS.appendIntermediateAssistantTranscript(...)`
  and `:550-555` is `childTS.appendToolCallTranscript(...)` — i.e. calls to **U3's** `turn.go`
  writers (`turn.go:1208`, `:1130`). Once U3 converts those, external_dispatch inherits the strict
  behaviour with no edit.
- `approval_transcript.go:179` is a **guard condition**
  (`ts.transcriptStore == nil || ts.transcriptSessionID == ""`) and `:183` passes
  `ts.transcriptStore, ts.transcriptSessionID` into `mutateToolCallInTranscript` — a
  **read-modify-write** on an existing entry, not an append. `AppendTranscriptStrict` is not
  applicable to it.

So the correction assigned an owner from the review's file list without checking what the files
need. Two consequences: U22 is a no-op unit occupying a wave slot, and the one genuine hazard on
that path — a `mutateToolCallInTranscript` against a session with no `meta.json`, which today
silently finds nothing and reports nothing — is covered by **no requirement at all**.

**Fix**: Delete U22's W3 line item, or replace it with the real requirement: an FR stating that the
transcript **mutate** path (`mutateToolCallInTranscript`, `pkg/agent/approval_transcript.go:188+`)
surfaces "session not found" and "entry not found" as a counter plus a WARN rather than a silent
`false`, with a BDD scenario and a test. Repurpose U22 to own `pkg/agent/runner/**` (M2-2) and/or
the unowned files in C2-1. Correct FR-002's citation list.

---

#### M2-4 — #107 asserts a hardware property that 64 shards make false by construction

**Lens**: Infeasibility · **Section**: FR-052 (line 2530), #107, SC-016, dataset "Sharding concurrency slope"

FR-052 and SC-016 now require throughput to be **"still rising from 64 to 128 concurrent
sessions, proving no fixed cap above the tested N"**, tested by #107
(`TestStoreSharding_ThroughputRisesPast64Sessions`, Cross-process).

The design is 64 FNV-keyed shards (FR-048). At N=128 the pigeonhole gives ≥2 sessions per shard
even under a perfect hash, so lock contention at least doubles relative to N=64 — and the work
inside each shard is fsync-bound (`WriteFileAtomic` does a file `Sync()` at
`pkg/fileutil/file.go:97` **and** a parent-directory `Sync()` at `:121`). On the CI runner (16 GB,
finite IOPS) throughput at 128 concurrent fsync-bound session creates will plateau or fall, not
rise. The spec's own edge case concedes the mechanism — *"Two sessions collide on an FNV-32a hash
mod 64 … they contend on one shard; correctness is unaffected and throughput is bounded by the
filesystem"* — and dataset row 5 records shard collision as "documented, not a failure". #107
asserts the opposite of both.

This also breaks binding rule 3's own principle: a "must keep rising" assertion is a
machine-specific claim wearing a slope's clothing.

**Fix**: FR-052 is a **design** property ("no fixed concurrency cap in the code"), not a throughput
promise. Assert it as one: a static/structural check that no constant bounds concurrent session
writers, plus the existing 2N-vs-N slope at a box-saturating N (dataset rows 3–4). If a
quantitative statement is wanted, state it as "throughput at 128 is not *worse* than at 64 by more
than the shard-collision factor", with the factor derived, not "still rising".

---

#### M2-5 — Two negative gates added in v2 have no positive lower bound, and #91's mutation check is scoped to the original eleven

**Lens**: Test coverage / Infeasibility · **Section**: binding rule 4 (line 109), "Negative-gate positive lower bounds" (lines 117-130), FR-085, #91, #104, #106, SC-035

Binding rule 4 and FR-085 are correctly stated as universal ("**every** negative, exclusion or
static gate"). The enforcement is not universal:

- The bounds table has exactly **eleven** rows.
- SC-035 and #91 (`TestNegativeGates_AssertPositiveLowerBounds`) are both scoped to
  "each of the **eleven** gates".

v2 added at least two more gates of the same shape, and neither is bounded anywhere:

| Test | Negative assertion | Vacuous-pass mode | Bound stated? |
|---|---|---|---|
| **#106** `TestParentageWalk_NeverReadsOwnerScopeIDOrParentAgentID` (FR-023) | "Static gate over the walk's code path: **zero** reads of either field" | passes if it locates zero code, or if "the walk's code path" resolves to nothing | **no** — and "the walk's code path" is not defined for a static analyser (which files? which functions? `verifyCallerOwnsSession` only, or its transitive callees?) |
| **#104** `TestMigrateLegacy_BytesUnchanged` (FR-060) | "also asserts **no** pre-split fused reader exists" | passes if its search for a fused reader finds nothing because the search is wrong | **no** |

#106 is the sole coverage of FR-023, which is one of the two **security** properties of the
parentage design (the other being the ancestor walk itself) — the ADR's stated reason for
forbidding `OwnerScopeID` is that a task dispatch puts a **plan id** in it
(`pkg/agent/task_executor.go:202-208`), so a walk over it mistakes a plan id for a session id. An
unbounded gate leaves that unenforced while reporting green: precisely C-3's finding, inside C-3's
own correction.

Separately, **#91 itself is of doubtful feasibility and is hazardous here.** As described
("mutation-style: rename the target, assert the gate goes red") a Go test must mutate source files
and re-invoke `go test` for each of eleven gates. That is 11 rebuilds of packages including
`pkg/gateway` — which this repository's CLAUDE.md forbids running locally at all (OOM) and defers
to CI — and it mutates files in a **shared working tree** where other units are writing
concurrently, with no stated isolation (temp copy? `git stash`? worktree?). No FR, dataset or unit
note specifies the mechanism, and SC-035 scores it 11-of-11 pass/fail.

**Fix**: (a) Add #106 and #104 to the bounds table with concrete counts (#106: assert it located
≥ K statements in the walk *and* ≥ 1 read of `ParentDurableKey`, and enumerate the exact files or
symbol set that constitute "the walk's code path"; #104: assert ≥ 1 located `migrateLegacy`
golden and ≥ 1 located candidate reader site). (b) Make the bounds table generated-by-rule rather
than a closed list — state "every row of the TDD plan whose assertion is an exclusion appears
here", and make #91 iterate that set. (c) Specify #91's mechanism: run mutations in a
`t.TempDir()` copy of the tree or in a `git worktree`, never in place, and scope the rebuild to
the gate's own package. If that is not affordable, downgrade #91 to a documented review gate the
way FR-072's semantic half was downgraded, and say so.

---

#### M2-6 — Three v2 tests require production seams that no requirement obliges anyone to build, and one of them contradicts binding rules 1–2

**Lens**: Infeasibility · **Section**: #88 / FR-082 / SC-038, #92 / FR-086 / SC-041, #103 / FR-058, binding rules 1–2

| Test | Needs | Required by an FR? |
|---|---|---|
| **#88** `TestCreateSessionWithID_NeverHoldsTwoSessionShards` | "an instrumented lock wrapper recording every `sessionLock` acquire and release in order" (BDD-92), reaching into production acquisition | **no** — FR-048 specifies the shard pool copying `lifecycle_lock.go:17-39`, which exposes no hook |
| **#92** `TestListSessions_ConcurrentDeleteConsistency` | a `DeleteSession` "interleaved so it **lands between** the reconcile pass and the snapshot" (BDD-95), across "100 interleavings" (SC-041) | **no** — deterministic interleaving needs a test-only barrier between FR-051's two phases |
| **#103** `TestMetaCache_HitCostsZeroDiskReads` | "Instrumented FS counter" proving zero reads on a cache hit | **no** — the store has an injectable *write* seam (`writeFileAtomicFn`, used at `unified.go:793`) but no read seam |

#88 additionally sits in direct tension with the spec's own binding rules: rule 1 disallows
"a spy, fake, or mock", and rule 2 requires assertions to land "on observable artefacts, not on
invocation" — yet #88's entire assertion **is** an invocation-order recording. The exemption is
correct on the merits (Go's race detector genuinely is not a lock-order checker, as C-6 argued)
but it is unstated, so a reviewer applying the rules literally must reject the only test that
covers SC-038.

**Fix**: Add an FR per seam, owned and waved: (a) `sessionLock` acquisition goes through a
package-level indirection U4 owns, defaulting to the direct path, overridable in-package for #88;
(b) FR-051's reconcile→snapshot boundary exposes an in-package test barrier (or #92 is rewritten
as a stress loop with a stated iteration count and the deterministic claim dropped from SC-041);
(c) `readUnifiedMeta`'s file reads go through a `readFileFn` var mirroring `writeFileAtomicFn`.
Amend binding rules 1–2 with the explicit, narrow exception for lock-order and cache-cost
instrumentation, so the exemption is a rule rather than an oversight.

---

#### M2-7 — FR-086's stated consistency model is stricter than the implementation it describes, and nothing implements the difference

**Lens**: Incorrectness · **Section**: FR-086 (line 2584), BDD-95, US-11 AS-6, #92, SC-041

FR-086 states the post-striping model as: *"a best-effort point-in-time snapshot that MAY omit a
session deleted during the call, **MUST NOT return a session whose directory was already absent
when the call began**…"*.

The second clause is not a property of the design being described. `ListSessions` snapshots
`us.metaCache` (`unified.go:1283-1286`), and the reconcile pass only **adds** entries for
out-of-band directories (`:1258-1280`) — it never removes a cache entry whose directory has
vanished. A session directory deleted **out of band** (not through `DeleteSession`, which is the
`RetentionSweep` / operator-`rm` / crashed-deploy case the spec's own AC-19 cares about) therefore
stays in `metaCache` and **is** returned, forever. That is true today and stays true after
striping unless reconcile learns to prune — which no FR requires and no unit is scoped for.

So FR-086 as written mandates a behaviour change disguised as a consistency statement, #92 will
either fail or be written not to exercise it, and SC-041's "consistent with the stated model" is
unfalsifiable.

**Fix**: Either add the pruning requirement explicitly ("the reconcile pass MUST evict a cached
entry whose directory no longer exists, under that session's shard"), with a dataset row and its
interaction with `cacheLoadFailures` (Ambiguity item 8 preserves the "excluded for the process
lifetime" limitation, which pruning must not disturb) — or weaken FR-086's second clause to match
the design ("MAY return a session whose directory was removed out of band; the cache is
authoritative between reconciles") and say so in the Behavioral Contract's error-flow bullet,
which currently repeats the strict wording verbatim (line 662).

---

#### M2-8 — #29's positive lower bound K is asserted but never derived, and its WS arm reduces FR-012 to a single site

**Lens**: Ambiguity · **Section**: "Negative-gate positive lower bounds" #29 row (line 125), "Eight sites, seven predicates" (lines 131-147), FR-014, FR-015, FR-016, AC-2

The C-3 correction's stated purpose for the bounds is *"K is written down in this spec so drift is
visible in code review"*. #29's row reads:

> **K = 10 post-change**: 7 role-B predicate reads + **3 pre-arm key reads**, **plus** every
> WS-payload stamping site … which the test MUST also count and assert ≥ 1.

The 7 is derived rigorously (the "Eight sites, seven predicates" table, verified: eight distinct
enclosing functions today, seven after W13 collapses `InterruptSession`/`InterruptSessionHard`).
**The 3 is never derived anywhere.** FR-016 names **five** pre-arm sites —
`cancel_prearm.go:338`, `:355`, `:602`; `subturn.go:585`, `:1147`. Verified individually, they are
not all reads of the id: `:338` is inside `preArmKeyForScope(sessionID string, …)` (a
**parameter**), `:355` reads `ts.transcriptSessionID`, `:602` calls `preArmKeysForTurn(ts)` (a
**call**), `subturn.go:585` reads `parentTS.transcriptSessionID`, `:1147` consumes a
precomputed slice. So "reads" could plausibly be 2, 3 or 5 depending on what the enumerator
counts — and the spec never defines "read" for this gate (identifier occurrence? field selector?
AST `SelectorExpr` on a `turnState` receiver?).

A reviewer therefore cannot tell a working gate from a broken one, which is exactly the state C-3
was raised to end. And the "≥ 1 WS-payload stamping site" arm is weak enough to be inert: FR-012
requires **every** session-scoped frame's `session_id` to come from `routingSessionID`, and a gate
satisfied by one stamping site cannot see nineteen minus one.

**Fix**: Define "read" precisely (recommend: an AST `SelectorExpr` whose selector is
`routingSessionID`, in non-test Go, excluding the field's own declaration and assignment).
Enumerate the expected post-change reads **by site** the way the "Eight sites, seven predicates"
table does, and set K to that count. Replace "≥ 1 WS-stamping site" with the exact stamping-site
count the W5 audit artefact (FR-089) produces, and make #29 depend on that artefact.

---

#### M2-9 — FR-091's roots-only response silently removes every child session from per-session usage accounting, and breaks four SPA test files nobody owns

**Lens**: Incompleteness / Incorrectness · **Section**: FR-091, US-19, Assumptions (line 2931), Regression Test Requirements, ownership table U12/U24, Rule 5

FR-091 makes `GET /api/v1/sessions` return **root sessions only**. The Assumptions section
acknowledges this as "a breaking change to the `GET /api/v1/sessions` response shape". The
regression analysis stops there. Verified consumers:

- **`src/components/screens/UsageScreen.tsx:282`** — `fetchSessions(undefined, undefined,
  { includeVerifier: true })` backs the "By session" tab. Under D1 a large share of token spend
  moves into delegated child sessions; roots-only means that spend **silently disappears** from
  per-session accounting, and ADR-052's SC-014 (verifier LLM spend auditable per session) is
  materially weakened. This is not in the "behaviours a reviewer will otherwise mistake for
  breakage" dataset, not in Regression Test Requirements, and `UsageScreen.tsx` **has no owner**.
- **`src/components/layout/Sidebar.test.tsx:241`** and **`src/components/search/SearchModal.test.tsx:158`**
  both assert `expect(vi.mocked(fetchSessions)).toHaveBeenCalledWith()` — literally "called with
  no arguments". FR-092/FR-093/FR-094 change all three call sites to pass paging arguments, so
  both assertions break.
- **`src/lib/__adr052__sessionVisibilityParams.test.ts`** pins `fetchSessions`'s exact query-string
  construction across six cases; adding `limit`/`offset`/`parent_session_id` touches it.

W22/U21 is Go-only ("the 12 named `*_test.go` files"), Rule 5 routes **new** tests to new files
and says nothing about **existing** SPA tests that must be inverted, and no unit owns
`Sidebar.test.tsx`, `SearchModal.test.tsx`, `UsageScreen.test.tsx` or the ADR-052 params test.
SC-034 nonetheless requires `npx vitest run` to exit 0.

**Fix**: (a) Add an FR covering usage accounting under roots-only listing — either UsageScreen
opts into a flat/all-sessions mode (a `flat=true` or `include_subordinate=true` parameter on the
same endpoint) or per-session usage moves to an endpoint that is not the tree listing. Add a
regression-dataset row ("per-session token accounting for a delegated child: previously present →
must remain present"). (b) Extend W22/FR-072's deliberate-inversion discipline to the SPA suite,
name the four files, and give them an owner (U12 for the api/lib tests, U24 for the component
tests). (c) Add `src/components/screens/UsageScreen.tsx` to the ownership table.

---

#### M2-10 — The `GET /api/v1/sessions` response is already a discriminated `oneOf`; reshaping it collides with ADR-034's inline-hosting rule and no unit is told

**Lens**: Incompleteness · **Section**: FR-091, FR-092, U10 row, Integration Boundaries → REST, Assumptions

`listSessions` (`pkg/gateway/rest.go:800-811`) returns **either** a bare `[]gen.Session` **or**
`gen.ListSessions200JSONResponseBody1{Sessions, PartialErrors}` — an existing generated
discriminated response. FR-091 adds `child_count` to each row and a paging envelope with a next
cursor; FR-092 adds `limit`/`offset`/`parent_session_id` parameters.

This project's CLAUDE.md records a hard, precedent-backed constraint on exactly this shape:
`oneOf` + discriminator wrappers **must be hosted inline in `openapi.yaml`** over internal refs,
because oapi-codegen inlines external file refs inside a `oneOf` as anonymous structs and emits
non-compiling `As*` accessors (ADR-034, precedent `AgentCreateRequest`). U10's row says only
"W16e (pagination + `parent_session_id` filter + `child_count`)"; neither the row, nor the
Integration Boundaries section, nor the Assumptions mention the existing `oneOf`, the partial-error
variant's fate under paging, or the inline-hosting requirement.

Also unaddressed: how `partial_errors` composes with a paged response (is a page with partial
errors still a page? does the cursor survive a store that errored?), and how `include_verifier`
interacts with roots-only.

**Fix**: Add the response-shape decision to FR-091 explicitly — name the new envelope, state that
it is hosted inline per ADR-034, state what happens to the `partial_errors` variant, and state the
`include_verifier` × roots-only interaction. Add a dataset row for "one legacy per-agent store
errors mid-page".

---

#### M2-11 — FR-064's `DeleteSession` flush-then-delete ordering contradicts FR-086's deletion window, and the two are tested by different units

**Lens**: Inconsistency · **Section**: FR-064, Edge Cases (line 686), FR-086, BDD-95, #22, #92

Edge Cases resolves the mid-flush delete as **flush-then-delete**: *"the flush completes under the
session's shard before the directory is removed, so a concurrent flusher tick cannot recreate a
`stats.json` in a deleted directory. A tick that finds the session gone drops its dirty entry
without writing."*

That is coherent for the flusher. It is not reconciled with `ListSessions`: FR-086 permits a
result that "MAY omit a session deleted during the call", while the flush-then-delete sequence
means `DeleteSession` now holds the session's shard for the duration of an fsync-bearing
`stats.json` write **before** removing the directory — widening exactly the window BDD-95
interleaves into. The two requirements are owned by the same unit chain (U6) but tested by
different tests (#22 vs #92) with no shared dataset, and neither states the ordering relative to
the `metaCache` eviction FR-033 also requires at teardown.

**Fix**: State the full `DeleteSession` sequence once, as an ordered list (acquire shard → flush
dirty stats → remove directory → evict `metaCache` → clear dirty-set entry → release), and
reference it from FR-064, FR-086 and FR-033. Add a dataset row covering a flusher tick that
arrives at each of the five points.

---

#### M2-12 — `pkg/session/retention_sweep.go` is named by FR-050 and SC-038 but owned by nobody, and it is the only other file holding `us.mu` and `metaCache`

**Lens**: Incompleteness · **Section**: FR-050, SC-038, Ambiguity item 13, Edge Cases (line 684), ownership table

Called out separately from C2-1 because it is the one unowned file the spec **names in a success
criterion**. SC-038 requires an instrumented wrapper to record "`ClearAll`/`RetentionSweep`
acquiring all 64 shards in strictly ascending index order"; Ambiguity item 13 accepts the
resulting store stall by name, citing `pkg/session/retention_sweep.go:35`. Verified:
`RetentionSweep` is `retention_sweep.go:25`, takes `us.mu` at `:136`/`:141` (3 references total)
and touches `metaCache`/`cacheLoadFailures` twice. `ClearAll` is `unified.go:1437` (the U4→U5→U6
chain ✓). So half of the pair SC-038 asserts on lives in a file no unit may write.

**Fix**: Assign `pkg/session/retention_sweep.go` to the `U4→U5→U6` chain (it is `UnifiedStore`
internals in all but filename) and add its shard-order conversion to U4's W15 line item.

---

### MINOR

**m2-1 — The m-1 correction was applied to one of the three places the changelog says it was
applied to.** The rebuttal is right (verified: `lifecycle.go:571` is the `AgentID` clause's closing
brace; the `ParentAgentID` block is exactly `:572-575`), and US-4 AS-5 (line 338) now reads
`:572-575`. But **FR-022 (line 2482) and BDD-22 (line 1204) still read `:571-575`**, while test
#12's bound (line 2159) and AC-13 (line 2678) read `:572-575`. The requirement and the test that
enforces it point at different line ranges. *Fix*: apply the correction to lines 1204 and 2482.

**m2-2 — The Regression Test Requirements table names a test that the C-1 correction deleted.**
Line 2409 maps "Approval-grant delegation inheritance" to
`TestApprovalGrants_InheritKeyedToChildSession` — the pre-rename name. The TDD plan's #25 is now
`TestApprovalGrants_InheritFromTwoKeys`, and the changelog's ID-stability note explicitly records
the rename "because the old name described the vacuous assertion". *Fix*: update the regression row
to `#25` / `TestApprovalGrants_InheritFromTwoKeys` (and #84/#85).

**m2-3 — The completeness check under-counts its own BDD scenarios.** Line 2796 asserts
"**106 BDD scenarios** (BDD-01 … BDD-107 … contiguous)". Machine count: **107** scenario headings,
ids 01–107 with no gaps — which matches the changelog header's "87 → **107**" and contradicts the
check. A structural check that is itself wrong is worse than none. *Fix*: 107.

**m2-4 — SC-003's corrected `rg` still cannot return zero, for a smaller reason than v1's.**
The `--glob '*.go' --glob '!*_test.go'` fix is right and closes the "matches this spec" problem.
But the name survives in comments that **FR-037 does not list for rewrite**:
`pkg/agent/verifier_adjudication.go:394` and `pkg/tools/inspect_session.go:170` (FR-037 lists only
`daypartition.go:268-307`/`:311-332` and `replay.go:41-45`/`:271-297`). Both are doc/inline
comments adjacent to deleted code, so a careful implementer removes them — but "careful" is not a
requirement. *Fix*: add both to FR-037's list.

**m2-5 — `pkg/gateway/websocket.go:1642` is a non-test `AppendTranscript` call in a U11-owned file
that FR-002 does not name.** Subset of C2-3, listed separately because the file *is* owned, so it
is a pure enumeration miss rather than an ownership gap.

**m2-6 — FR-096 is defined out of order, after FR-095 (lines 2593-2594).** Cosmetic, but this
section is the one an implementing agent reads linearly.

---

### OBSERVATION

**o2-1 — The disjointness claim is true and was verified independently; the spec should say what it
actually proves.** Extracting every path from the 24 ownership rows yields zero collisions, with
the declared `unified.go` chain as the sole exception, exactly as claimed. The wave graph is
acyclic and no unit depends on a later wave as declared. The claim is simply answering a question
nobody asked: two units writing the *same* file was never the observed failure — a file with *no*
unit was. Consider replacing "Disjointness proof" with "Ownership derivation", stating the symbol
enumeration that produced the table and the commands to re-run it.

**o2-2 — The verbatim-AC and structural-count claims are all true.** Machine-diffed: AC-1…AC-22 are
byte-identical to ADR-057 v4, 22 of 22, zero differences. FR-001…FR-096 are all defined and all
appear in the traceability matrix (96/96, no orphans in either direction). 112 test rows (#1…#111
plus #68a, no gaps). 49 success criteria (SC-001…SC-049, no gaps). All twelve W22 gate test files
exist. `SESSION_SCOPED_FRAME_TYPES` is exactly 19 members, and BDD-16/98/99's classification
arithmetic (6 + 5 + 2 pinned, 6 deferred to the W5 audit) sums correctly.

**o2-3 — The M-7 rebuttal is correct and worth keeping as written.** `getSubTurnConfig`
(`pkg/agent/subturn.go:64-69`) reads `agents.defaults.subturn.max_concurrent` unclamped when > 0;
only the fallback passes through `clampParallelExplicit` (`pkg/config/config.go:459-468`, cap 16).
AC-10's 24/25 topology runs literally with no ADR amendment. The residual gap is the *unset* case
(M2-1), not the rebuttal.

---

## 4. Structural Integrity Results (`plan-spec` mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** | 19 stories |
| Every acceptance scenario has ≥1 BDD scenario | **PASS** | |
| Every BDD scenario has a `Traces to:` back-reference | **PASS** | BDD-01…BDD-107, machine-verified contiguous |
| Every BDD scenario has a corresponding test | **PASS** | |
| Every FR appears in the traceability matrix | **PASS** | 96/96, machine-verified, no orphans either direction |
| Matrix rows test what their FR claims | **PASS with exceptions** | M-11's seven rows are genuinely repaired (#103–#107, #109). New exceptions: FR-023→#106 and FR-060→#104 are unbounded gates (M2-5); FR-052→#107 is unpassable (M2-4) |
| Test datasets cover boundary/edge/error | **PASS** | 9 datasets; the throttle dataset's two-sided row 7 and new row 9/10 are the strongest additions in v2 |
| Regression impact explicitly addressed | **FAIL** | M2-9 — the roots-only response shape breaks UsageScreen's per-session accounting and four SPA test files, none named |
| Success criteria measurable, no subjective language | **PASS with exceptions** | SC-016's "still rising" is unmeasurable-as-true (M2-4); SC-041's "consistent with the stated model" is unfalsifiable while FR-086 is stricter than the design (M2-7); SC-003 still cannot return zero (m2-4) |
| **File-ownership table is disjoint** | **PASS** | independently verified |
| **File-ownership table is exhaustive** | **FAIL** | C2-1 — 10 unowned files, 6 hard compile breaks |
| **Wave ordering honours stated dependencies** | **PASS as declared** | acyclic, no forward references |
| **Wave ordering honours *actual* dependencies** | **FAIL** | C2-4 — U17→U6/U4, U19→U8, U14→U8 |
| Hard ordering W15→W23→W24 honoured | **PASS** | U4(B) → U5(C) → U6(D) |
| Hard ordering 6 (W11 shim dance) | **PASS** | The shim keeps the intermediate tree compiling (`IsDelegateChildEntry` has exactly 4 non-test call sites, all U18-owned: `rest.go:826`, `replay.go:298`, `verifier_adjudication.go:406`, `inspect_session.go:172`); #58's ≥60 `ParentSpawnCallID` bound (measured 73 across 9 files) plus the zero-reference clause does fail if the shim survives U18 |
| U10 before U9/U11/U12/U23/U24 | **PASS** | |
| U21 last, own commit, no collision with tests-first | **PASS** | Rule 5 resolves M-4 cleanly for Go; **fails for the SPA suite** (M2-9) |
| All 24 ADR work items covered | **PASS on paper** | W3, W16, W9 are nominally covered but under-scoped (C2-3, C2-2, M2-2) |

---

## 5. Test Coverage Assessment

**What v2 genuinely improved.** The four throttle tests are no longer jointly satisfiable by a dead
flusher — BDD-93/#89/dataset row 9 is a real, single-purpose assertion and the negative control
inside #20 closes the "unchanged means never written" hole. #56's merge of both transcript halves
into one run closes M-9 properly. #84/#85 make the `InheritFrom` re-key untestable-by-fixture
(dataset row 5 forbidding `parentSid == childSid` is the right kind of rule). #90 is the correct
shape for the cache-clobber property. #111 promotes the H-7 tampering hazard into the visible plan
without leaking the holdout, and a machine check confirms no `H-` identifier appears in the visible
plan.

**What is still missing or unusable.**

1. **No coverage of the default configuration for W17** (M2-1) — every test sets the cap explicitly.
2. **#107 cannot pass** (M2-4); FR-052 is therefore unenforced in practice.
3. **#91's mechanism is unspecified and hazardous** (M2-5); SC-035 depends entirely on it.
4. **#88, #92, #103 need production seams no FR requires** (M2-6).
5. **#100 is either impossible or vacuous** (C2-2) — it is the only enforcement of FR-092.
6. **#106 and #104 are unbounded negative gates** (M2-5).
7. **The transcript *mutate* path has zero coverage** (M2-3) — `mutateToolCallInTranscript`
   against a session with no `meta.json` silently returns `false` today and no requirement changes
   it.
8. **Fifteen `AppendTranscript` call sites keep the lenient primitive with no test asserting that
   is intentional** (C2-3).
9. **No SPA regression coverage for the response-shape change** (M2-9).
10. **`ListAllSessions` partial-error behaviour under paging is untested and unspecified**
    (M2-10) — `loop.go:5046-5090` returns `[]error` alongside the rows, and `rest.go:800-811`
    switches response variants on it.

---

## 6. STRIDE Threat Summary (delta from grill #1)

| Component | Threat | Status in v2 |
|---|---|---|
| Child id colliding with an existing session directory | **Tampering** — adopting another session's transcript/meta/owner/stats | **Closed properly.** FR-096 / BDD-107 / #111 / SC-049 promote H-7's property into the visible plan; `os.MkdirAll` at `unified.go:463` verified idempotent-and-silent, so the requirement is load-bearing |
| Pending-approval registry | **Repudiation / availability** | **Closed.** FR-080 (entry carries the acting session id) + FR-081 (resolve by approval id) + #86/#87 cover both halves incl. the interactive approve round trip |
| Approval grants | **Elevation of privilege** | **Closed.** Two-key `InheritFrom`, absent-before assertion, self-delegation union (dataset rows 3–4), loud empty-source branch |
| Ancestor walk / parentage edge | **Elevation of privilege** (sibling/cousin reach) | **Weakened.** The positive coverage is good (9-row topology dataset, #50–#52), but the **negative** guarantee — never infer parentage from `OwnerScopeID`/`ParentAgentID`, where a task dispatch puts a *plan id* in `OwnerScopeID` (`task_executor.go:202-208`) — rests solely on #106, an unbounded gate over an undefined "walk's code path" (M2-5) |
| 3P child process tree | **Elevation of privilege / resource leak** — a surviving foreign process tree | **NOT addressed in any implementable form.** No `Setpgid` anywhere in `pkg/agent/runner/`; the package is unowned (M2-2) |
| Root-level delegation fan-out | **DoS** | **Addressed only when the operator sets the key.** Default install has no defined gate (M2-1) |
| Session store lock order | **DoS** (deadlock/stall) | **Addressed** by FR-050/FR-082 — but half the asserted surface (`RetentionSweep`) is unowned (M2-12) and the assertion needs an unspecified seam (M2-6) |
| Per-session usage accounting | **Information loss** (not an attack, but an audit regression) | **NOT addressed** — roots-only listing silently drops child spend from the Usage screen (M2-9) |

---

## 7. Unasked Questions

1. **Does `AppendTranscript` itself become strict, or does the lenient form survive at 15 call
   sites?** AC-1 is written as a property of `AppendTranscript`; the Symbols table says "gains a
   strict sibling". These are different migrations. (C2-3)
2. **What is the root-delegation cap on a fresh install**, where `subturn.max_concurrent` is unset
   and the cited resolver falls back to the knob FR-095 forbids? (M2-1)
3. **What maintains the recency order that pagination slices?** No FR creates an index; the store
   sorts a map and the loop sorts a merged union. (C2-2)
4. **How is `child_count` computed without an O(all sessions) scan per page?** (C2-2)
5. **Who owns `pkg/session/retention_sweep.go`, `pkg/commands/runtime.go`,
   `pkg/sysagent/tools/deps.go`, `pkg/gateway/gateway.go`, `pkg/gateway/rest_stats.go`,
   `pkg/agent/goal_loop.go`, `pkg/agent/goal_triggers.go`, `pkg/agent/task_executor.go`,
   `pkg/tools/handoff.go`, `pkg/config/config.go`, `pkg/agent/runner/**`?** (C2-1)
6. **Which unit adds the store-flush and `metaCache`-eviction calls to `session_end.go`**, given
   U17 owns the file in Wave A and U6 creates the API in Wave D? (C2-4)
7. **What does `#91` actually execute?** Mutating the tree in place is unsafe in a shared working
   tree; rebuilding `pkg/gateway` eleven times is forbidden locally by this repo's own rules.
   (M2-5)
8. **Where does per-session token accounting live once the list returns roots only?** (M2-9)
9. **What happens to `partial_errors` under pagination**, and does a cursor survive a legacy store
   that errored mid-page? (M2-10)
10. **Is "read" in #29's K an identifier occurrence, a selector expression, or a dataflow use** —
    and is the pre-arm component 2, 3 or 5? (M2-8)
11. **Does the reconcile pass prune cache entries for directories deleted out of band?** FR-086
    requires the outcome; nothing requires the mechanism. (M2-7)
12. **What is the strict contract for the transcript *mutate* path** (`mutateToolCallInTranscript`),
    which silently returns `false` today for a session that does not exist? (M2-3)

---

## 8. Verdict

**BLOCK** — 4 CRITICAL.

v2 is a materially better document than v1, and its treatment of the six CRITICAL findings is
honest and technically correct — each fix was re-derived here against the tree and each genuinely
fails against a broken implementation. The `[grill …]` / `[operator …]` annotation discipline, the
"Silent-today claims, re-verified" table and the m-1 rebuttal-with-evidence are the right
instincts, and the AC-verbatim and structural-completeness claims are all true under machine check.

It is nevertheless not safe to dispatch to parallel waves, for one structural reason: the
corrections were derived **from grill #1's findings** rather than from the tree. The ownership
table gained owners for the five files the review named and was then declared exhaustive; ten more
were never looked for, six of which stop the build the moment U4 or U8 merges. W3's writer list was
carried forward unchanged and is 5 of 20. The genuinely new scope — US-19's nested listing, which
is the largest single body of work v2 adds and goes to six owners across four layers — was
specified without checking that its central requirement is achievable against a map-backed store
and a merge-and-dedup loop layer; it is not.

**Fix order** (dependencies noted):

1. **C2-1 first, and mechanically.** Re-derive ownership from the symbol enumerations, not from
   this review's file list — otherwise grill #3 finds the eleventh file. This also resolves M2-12,
   part of M2-2, and part of C2-3.
2. **C2-2** — decide whether pagination gets a real index (new FR, owner, wave, dataset) or whether
   FR-092/BDD-102 relax to bound only the boundary layers. This changes U6/U9/U13's scope and
   possibly the wave graph, so it must settle before dispatch.
3. **C2-3** — state the W3 conversion boundary completely, the way FR-090 states W20's.
4. **C2-4** — re-run the dependency derivation over *signature changes*, not just *file writes*;
   U17/U19/U14 all own files whose signatures other units change.
5. **M2-1** (a one-line operator answer: what is the cap when the key is unset), **M2-4**
   (rewrite #107 as a design assertion), **M2-5** (bound #106/#104; specify or downgrade #91),
   **M2-6** (three seam FRs), **M2-9** (usage accounting + SPA test ownership) — all spec-text
   changes that can land in one revision pass.
6. The MINORs are mechanical; m2-1 in particular is a one-character edit in two places that
   currently leaves FR-022 and its own gate test pointing at different line ranges.

---

**Review written to**: `docs/internal/specs/adr-057-session-unification-spec-review-2.md`

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/adr-057-session-unification-spec.md docs/internal/specs/adr-057-session-unification-spec-review-2.md
```
