# Feature Specification: ADR-057 — Unify delegate sub-turns onto the own-session execution path

**Created**: 2026-08-03
**Status**: Draft
**Input**: [ADR-057 v4](../architecture/ADR-057-session-parent-child-parity.md) (Accepted, commit `7d7def6f`), shaped by [ADR-057 review](../architecture/ADR-057-session-parent-child-parity-review.md) (adversarial red-team, verdict BLOCK on v2). This spec is the implementation layer for ADR-057 v4's decisions D1–D12 and work items W1–W24. **ADR-057's acceptance criteria AC-1 … AC-22 are carried forward verbatim in §"Acceptance Criteria (verbatim from ADR-057 v4 §10)" and are non-negotiable.**

**Branch**: `feature/plan-swimlane-board`. **Greenfield** — no migration, no back-compat, for chats or config files (ADR-057 v4 operator decision 1).

**Version**: v3 (2026-08-03). Supersedes v2 (commit `883f1efc`), which superseded v1 (commit `478b85b5`).

> **v3 changelog.** v2 was reviewed by [grill #2](adr-057-session-unification-spec-review-2.md) — **verdict BLOCK, 4 CRITICAL / 12 MAJOR / 6 MINOR / 3 OBSERVATION**. All 22 numbered findings are resolved here; **nothing is deferred, nothing is filed as a follow-up issue.** Grill #2 independently re-derived every v2 correction against the tree and confirmed all six CRITICAL fixes are real — **those are not touched here.** Corrections forced by this review are marked `[grill2 C2-n / M2-n / m2-n]`.
>
> **The structural lesson v3 acts on.** Grill #2's verdict was not "v2 got things wrong" — it was that v2 **fixed the findings rather than the defect classes**, deriving its corrections from grill #1's file list instead of from the tree. So every correction below was derived by **enumerating the tree**, and the enumeration commands are now stated in the document so a reviewer can re-run them (see "Ownership derivation"). Three of the four CRITICALs are direct consequences of that one habit.
>
> **CRITICAL corrections:**
> - **C2-1 — the ownership table was disjoint but not exhaustive.** Ten files that FRs require changing had no owner; six were hard compile breaks. Ownership is now derived from **symbol enumerations, not a review's file list**, and the commands are printed in the spec. New owners: `retention_sweep.go` → the U4→U5→U6 chain; `pkg/commands/**` → U8; `deps.go`/`gateway.go`/`rest_stats.go` → new **U25**; `task_executor.go`/`goal_loop.go`/`goal_triggers.go` → new **U26**; `pkg/config/**` → new **U28**; `pkg/agent/runner/**` and `handoff.go` → **U22** (re-scoped, see M2-3); `UsageScreen.tsx` + 4 SPA test files → U12/U24.
> - **C2-2 — US-19's "MUST NOT load the whole set and slice" was infeasible.** `ListSessions` sorts an unordered `metaCache` map (`pkg/session/unified.go:1247-1293`, verified) and `ListAllSessions` k-way-merges N stores (`pkg/agent/loop.go:5046-5090`, verified); neither can produce a recency-ordered page without materialising everything, and no FR created an index. **Resolved by restating the requirement, not by adding an index** — because `metaCache` is already resident-everything (FR-058 asserts a cache hit costs **zero** disk reads), so the store-layer sort touches no disk and there is no I/O cost to eliminate. FR-092 now bounds cost at the **boundary** layers (REST payload, SPA render) and explicitly accepts the in-process sort over resident data at the store and loop layers. New **FR-097** (in-memory parent index for `child_count` and orphan detection) and **FR-098** (cross-store ordering + cursor + partial-error contract) supply the two mechanisms US-19 needed and no FR provided. **#100 is rewritten into three checkable assertions.**
> - **C2-3 — FR-002 named 5 transcript writers; the tree has 22.** Enumerated: `task_executor.go` (7), `turn.go` (4), `handoff.go` (3), `websocket.go` (2), `loop.go` (2), `goal_loop.go` (2), `unified.go` (1), `cancel.go` (1) = **22 non-test matches of `AppendTranscript(`**, of which **20 are invocations** and 2 are declarations (the store method at `unified.go:802`, the interface method at `handoff.go:70`). *(This reconciles v3's 22 with grill #2's 20 — the review counted invocations, the enumeration command counts matches. Both numbers are now stated with their scope.)* **Resolved the way AC-1's frozen wording requires: `AppendTranscript` itself becomes strict** and the lenient silent-create branch (`unified.go:819-823`) is deleted. There is no lenient sibling left to leak. Verified this is a far smaller change than it appears: **all 20 invocation sites already capture or check the returned error**, so no call site fails to compile — the change is in runtime behaviour at sites that already have an error branch.
> - **C2-4 — three undeclared wave-order violations.** Re-derived over **signature changes**, not file writes. U17 is split into **U17a** (approvals, Wave A) and **U17b** (`session_end.go` teardown, Wave E, after U6). U14 → Wave F and U19 → Wave G, both now declaring U8. **Correction to the review:** FR-041's compile-breaking surface is **5 files, not 4** — `steering.go` (U8, definitions), `cancel.go:390`/`:465` (U15, calls), `session_messaging_wire.go:166` (U19, method values), `delegate.go:363-364`/`:572-578` (U14, func types), `pkg/commands/runtime.go:39` (interface decl) + `cmd_cancel_test.go:61` (stub). The other ~25 references across 10 files are **doc comments only** and are handled as owned doc-rot (FR-100), which is why U3/U7/U9/U15/U17a/U22 do **not** need to move waves.
>
> **MAJOR corrections:** M2-1 the shipped default is seeded (**U28**) *and* covered (BDD-108, #112) — v2 had zero coverage for the configuration every install ships with; M2-2 FR-029 gets real implementation sites in `pkg/agent/runner/` with the in-house `Setpgid` precedent cited and POSIX scoping stated; M2-3 **U22 is re-scoped** (verified: zero `AppendTranscript` calls in either of its v2 files) and the real hazard it missed becomes **FR-099** (the transcript *mutate* path); M2-4 #107 is rewritten as a design assertion (a "must keep rising" claim is false by construction at 128 sessions over 64 shards); M2-5 binding rule 4 is made **generative** rather than a closed list of eleven, and #91's mechanism is specified; M2-6 three seam FRs (**FR-101…FR-103**) plus a narrow, stated exception to binding rules 1–2; M2-7 FR-086 is aligned with the design **and** the prune is required, citing the in-tree precedent at `unified.go:1474-1487` whose own comment names this exact failure ("leaving ListSessions to resurrect sessions that are gone from disk"); M2-8 #29's K is derived **by site**; M2-9 usage accounting gets **FR-104** and the four SPA test files get owners; M2-10 the existing `oneOf` and ADR-034's inline-hosting rule are addressed; M2-11 `DeleteSession`'s full ordered sequence is stated once.
>
> **MINOR corrections:** m2-1 the m-1 rebuttal is now applied to **all three** places (BDD-22 line 1204 and FR-022 were still `:571-575`); m2-2 the regression row names the renamed test; m2-3 the completeness check's BDD count was wrong — v2 claimed 106 while containing **107**; v3 adds seven and the check now reads **114**, produced by command rather than by hand (machine-verified: 114 headings, 114 unique ids, 01–114, zero gaps, zero duplicates); m2-4 FR-037 gains the two leftover comment sites **and** — found while verifying — FR-035's helper citation is widened to `:814-832` to include the doc comment that also names the predicate; m2-5 `websocket.go:1642` is enumerated; m2-6 FR-095/FR-096 are reordered.
>
> **Rebutted:** none. Every grill-#2 finding was confirmed against the tree. Two were found **understated** (C2-3's writer count, and M2-4's `rest.go` doc-comment site) and one **overstated in scope but not in kind** (C2-4's FR-041 file count is 5 compile breaks + 25 comment references, not 4 uniform breaks).
>
> **ID stability.** AC-1 … AC-22 remain byte-identical to ADR-057 v4 — **unchanged by this revision**. No existing FR/BDD/SC/test/unit ID was renumbered or reused for a different subject. Additions only: **FR-097…FR-104**, **BDD-108…BDD-114**, **SC-050…SC-056**, tests **#112…#118**, units **U25/U26/U28** plus the **U17 → U17a/U17b** split. Two IDs changed **meaning** and are called out where they appear: **FR-002** (the primitive itself becomes strict, so there is no sibling) and **FR-092** (bounded at the boundary layers, not at all four).
>
> **Counts.** v2 → v3: user stories 19 → **19**; FRs 96 → **104**; BDD scenarios 107 → **114**; success criteria 49 → **56**; test entries 112 → **119**; work units 24 → **28**; datasets 9 → **10**; waves 7 → **8**.

> **AMENDMENT (2026-08-04, operator-authorised, commit `536b7340` + its follow-up fix).** FR-095's core premise no longer holds, and every place in this spec that restates it as fact is now **superseded** — marked inline as `[AMENDED 2026-08-04]` at each occurrence rather than silently rewritten, per this project's rule that spec amendments must be authorised and their rationale recorded (never a quiet edit).
>
> **What broke the premise.** FR-095 (and the M-7/grill-M-7 rebuttal it rests on, the "Where a finding and a decision pulled against each other" note, US-15's body text, BDD-75/BDD-108, SC-050, test #112, and the Ambiguity-table row 4) all assert, as a *fact about the code*, that `agents.defaults.subturn.max_concurrent` is honoured **unclamped** while `Performance.EffectiveMaxParallelAgents()` is **hard-capped at 16** by `clampParallelExplicit` — two genuinely different numbers, which is why FR-095 could pin the root-delegation gate to the former and forbid the latter, and why the seeded default of **16** (U28, `pkg/config/defaults.go`) was argued to "change no shipped behaviour" (v3's own Assumptions note, `[grill2 M2-1]`): 16 was supposedly already what the fallback capped out at anyway, on capable hardware. Commit `536b7340` ("size agent concurrency from available memory, remove the ceiling of 16") removed `clampParallelExplicit`'s ceiling entirely — it now only floors an explicit value at 1 (`pkg/config/config.go`) — so `Performance.EffectiveMaxParallelAgents()` is no longer capped at 16 at all. The "two knobs, only one clamped" distinction FR-095 pins its whole contract to is gone, and the 16==16 coincidence the seed's "changes no shipped behaviour" claim depended on no longer holds: with the seed unchanged, root-level `delegate()` fan-out stayed hardcoded at 16 while an operator's own `max_parallel_agents` (now the documented "SINGLE authority for agent concurrency" per `PerformanceSettings.yaml`) could be set to anything — a control that moved, persisted and governed **nothing** for this one dispatch path (the ADR-037 anti-pattern this project bans by name).
>
> **The operator's direction (this amendment).** Consolidate: `Performance.EffectiveMaxParallelAgents()` becomes the **sole, central authority** for agent concurrency, full stop — including root-level delegation admission. `agents.defaults.subturn.max_concurrent` is **redefined** from "the root-delegation cap, sourced independently and forbidden from ever equalling the fallback" to an **optional per-delegation override**: `== 0` (unset — the new shipped default; the U28 seed of 16 is **removed**, `DefaultSubTurnMaxConcurrent` no longer exists) resolves LIVE to `cfg.Performance.EffectiveMaxParallelAgents()`; `> 0` is an explicit, deliberate operator override honoured exactly as configured (may legitimately differ from the central value — this is what "per-delegation override" means, not a second default); `< 0` remains a genuine configuration error (`ErrRootDelegationCapMisconfigured`, unchanged). `RootDelegationAdmission`'s cap is also no longer boot-frozen: it resolves live via a `resolveCap` closure (mirroring `AdmissionController.resolveCap`, the session-admission gate this same 2026-08-04 consolidation already made live), so an operator's `PUT /api/v1/performance` write reaches root-level delegation with no restart.
>
> **IDs amended (marked `[AMENDED 2026-08-04]` at each site, text retained alongside for the historical record rather than deleted):** **FR-095** (body + the "unset case" and coverage bullets); **SC-050**; **BDD-75** (the `clampParallelExplicit`-at-16 clause), **BDD-108** (the "seeded default of 16" clause); test **#110** (still passes unmodified — an explicit override still diverges from the central value by design, so the assertion "resolved cap ≠ EffectiveMaxParallelAgents()" still holds when the two are deliberately set apart) and test **#112**, now `TestRootDelegationCap_DefaultInstallInheritsCentralValue` (deliberately inverted — see `pkg/agent/admission_adr057_test.go`); the US-15 body text and its M-7 rebuttal note (line ~623); the earlier "Where a finding and a decision pulled against each other" v2-era note (line ~77); Ambiguity-table row **4**; and the two Assumptions-section bullets citing the `clampParallelExplicit`-at-16 tripwire and the "seed changes no shipped behaviour" claim. **AC-10's 24/25 topology is unaffected** — it is satisfied more simply now: setting the CENTRAL `max_parallel_agents = 24` (or `subturn.max_concurrent = 24` as an explicit override, still supported) both produce a 24-cap either way, since neither path is clamped any more.
>
> Full implementation detail: `pkg/config/config.go` (`SubTurnConfig.MaxConcurrent` doc comment), `pkg/config/defaults.go`, `pkg/agent/admission.go` (`ResolveRootDelegationCap`, `RootDelegationAdmission`), `pkg/agent/loop.go` (`NewAgentLoop`'s wiring). Regression coverage: `pkg/agent/wiring_adr057_fix_test.go::TestWiring_RootDelegationFanOut_BoundedByCentralValueNot16` (proves the 17th root-level delegation, which the pre-fix hardcoded-16 seed would have refused, is admitted once the central value is 40).

### Grill #2 finding resolution

| # | Severity | Finding | Resolution | Where |
|---|---|---|---|---|
| C2-1 | CRITICAL | Ownership disjoint but not exhaustive; 10 unowned files, 6 compile breaks | **Corrected** — re-derived from symbol enumerations; U25/U26/U28 added; commands printed | Ownership derivation, ownership table |
| C2-2 | CRITICAL | US-19's bounded-cost requirement infeasible; #100 impossible or vacuous | **Corrected** — FR-092 restated to bound the boundary layers; FR-097/FR-098 add the missing mechanisms; #100 rewritten | FR-092, FR-097, FR-098, #100, BDD-102, SC-043 |
| C2-3 | CRITICAL | 22 transcript writers, 5 named; lenient primitive survives | **Corrected** — `AppendTranscript` itself becomes strict; all 22 enumerated with owners | FR-002, Symbols table, ownership table |
| C2-4 | CRITICAL | Three undeclared wave-order violations | **Corrected** — U17 split; U14→F, U19→G; `pkg/commands/**`→U8; derived over signatures | Ownership table, Integration order, FR-041, FR-100 |
| M2-1 | MAJOR | No seeded default for `subturn.max_concurrent`; shipped config has zero coverage | **Corrected** — U28 seeds it; BDD-108/#112 cover the unset case | FR-095, U28, BDD-108, #112, SC-050 |
| M2-2 | MAJOR | FR-029 has no implementation site; `pkg/agent/runner/` unowned | **Corrected** — U22 owns the drivers; precedent cited; POSIX scoped | FR-029, U22, BDD-86, #68a |
| M2-3 | MAJOR | U22 has nothing to do; mutate-path hazard uncovered | **Corrected** — U22 re-scoped; FR-099 covers the mutate path | U22, FR-099, BDD-109, #113 |
| M2-4 | MAJOR | #107 false by construction at 64 shards | **Corrected** — restated as a design assertion | FR-052, #107, SC-016 |
| M2-5 | MAJOR | #106/#104 unbounded; #91 scoped to a fixed eleven; mechanism unspecified | **Corrected** — bounds table made generative; #91 mechanism specified | Rule 4, bounds table, FR-085, #91, SC-035 |
| M2-6 | MAJOR | #88/#92/#103 need seams no FR requires | **Corrected** — FR-101/FR-102/FR-103; rules 1–2 gain a narrow stated exception | FR-101…FR-103, rules 1–2 |
| M2-7 | MAJOR | FR-086 stricter than the design; nothing implements the difference | **Corrected** — prune required, in-tree precedent cited | FR-086, BDD-110, #114, SC-051 |
| M2-8 | MAJOR | #29's K never derived; WS arm inert | **Corrected** — "read" defined; K derived by site | "Three reads, five sites", #29, FR-016 |
| M2-9 | MAJOR | Roots-only drops child spend; 4 SPA test files unowned | **Corrected** — FR-104; files owned by U12/U24 | FR-104, BDD-111, #115, ownership table |
| M2-10 | MAJOR | Existing `oneOf` + ADR-034 inline-hosting unaddressed | **Corrected** — response shape decided and stated | FR-091, Integration Boundaries → REST |
| M2-11 | MAJOR | FR-064 vs FR-086 ordering contradiction | **Corrected** — one ordered sequence stated once | Edge Cases, FR-064, FR-086, FR-033 |
| M2-12 | MAJOR | `retention_sweep.go` named in SC-038 but unowned | **Corrected** — assigned to the U4→U5→U6 chain | Ownership table, FR-050 |
| m2-1 | MINOR | m-1 correction applied to 1 of 3 places | **Corrected** — BDD-22 and FR-022 now `:572-575` | BDD-22, FR-022 |
| m2-2 | MINOR | Regression row names a deleted test | **Corrected** — `#25` / `TestApprovalGrants_InheritFromTwoKeys` | Regression Test Requirements |
| m2-3 | MINOR | Completeness check under-counts its own BDDs | **Corrected** — 107, machine-verified | Completeness check |
| m2-4 | MINOR | SC-003's `rg` still cannot return zero | **Corrected + widened** — FR-037 gains 2 sites; FR-035's helper citation widened to `:814-832` | FR-035, FR-037, SC-003 |
| m2-5 | MINOR | `websocket.go:1642` unnamed | **Corrected** — enumerated | FR-002 |
| m2-6 | MINOR | FR-096 defined before FR-095 | **Corrected** — reordered | FR section |
| o2-1 | OBS | Disjointness answers a question nobody asked | **Accepted** — replaced with "Ownership derivation" + commands | Ownership derivation |
| o2-2 | OBS | Verbatim-AC and structural claims all true | **Acknowledged** — re-verified in v3; AC-1…AC-22 untouched | — |
| o2-3 | OBS | M-7 rebuttal correct and worth keeping | **Kept verbatim** | changelog, FR-095 |

> **v2 changelog.** v1 was reviewed by [grill #1](adr-057-session-unification-spec-review.md) — **verdict BLOCK, 6 CRITICAL / 14 MAJOR / 5 MINOR / 2 OBSERVATION**. All 20 numbered findings are resolved here; **nothing is deferred**. In parallel the operator resolved all 12 items in the Ambiguity Self-Audit. The two classes of change are marked distinctly throughout — `[grill C-n / M-n / m-n]` for a correction forced by the review, `[operator n]` for a settled decision.
>
> **Grill-driven corrections (what was actually wrong):**
> - **C-1** — FR-031 ("`Inherit`'s first argument MUST become the child's own session id") was, implemented literally, a **silent no-op**: `Inherit` uses one `sessionID` for both the source lookup and the destination write (`pkg/security/approvalgrants.go:112-129`, verified), so re-keying it to the child makes the source lookup miss and return at `:118-120`. FR-031 is rewritten as an explicit **source/destination pair** (`InheritFrom`), FR-079 makes the empty-source branch loud, and BDD-31/32/88/89 assert the grant was **not** present under the child key before the spawn.
> - **C-2** — FR-003's premise was **factually false**. `pkg/agent/turn.go:1297` already does `abandonedWritesSuppressed.Add(1)`; the counter is declared at `:25`, exported at `:44`, incremented at **seven** sites, and there is already a passing test (`pkg/agent/turn_test.go:221`). Test #7 was green against the unmodified tree — inside the P0 story that gates every other measurement. FR-003, US-1 AS-4, BDD-04 and test #7 now target the **log record** (the genuinely new artefact) plus a counter **delta**. Every other "is silent today" claim was re-verified against the tree; see "Silent-today claims, re-verified".
> - **C-3** — eleven tests were negative/static gates that pass when their **search finds nothing**. A **fourth binding rule** now requires every such gate to prove its search is live by first asserting a stated positive lower bound; FR-085 makes it a requirement, the bounds are enumerated in "Negative-gate positive lower bounds", and SC-003's `rg` invocation is corrected (as written it matched this spec and the ADR themselves and could **never** return zero).
> - **C-4** — the W24 throttle could be entirely non-functional with tests #20/#21/#22/#74 all green, because nothing asserted the **unforced** periodic flush. FR-083, BDD-93, test #89, SC-036, a negative control inside #20, and a two-sided rewrite of throttle-dataset row 7.
> - **C-5** — W23+W24 relocate Alternative F's clobber from the **file** to `metaCache` (`writeMetaLocked` ends with a whole-document `us.metaCache[sessionID] = meta.Clone()`, `pkg/session/unified.go:798`, verified). FR-084, BDD-94, test #90, SC-037.
> - **C-6** — copying the parent's `Owner` is a **two-session** operation that FR-050 forbade outright. FR-082 defines the protocol (read under the parent's shard, **release**, then create under the child's), FR-050 gains the matching exception, and test #88 asserts acquisition order via an instrumented lock wrapper rather than relying on `-race`.
> - **M-1…M-5, M-13** — the ownership table was neither exhaustive nor acyclic. It is **re-derived**: five previously unowned files get owners (three new units U22/U23/U24), U20 is split so the cascade wiring sits with its hook, U7 declares U17, U2's existence predicate is frozen as a contract, the child-terminal `CloseSession` call site is assigned, and every unit's new tests move to **new** files so U21's exclusive 12 no longer collide with "tests first".
> - **M-6…M-12, M-14** and the five MINORs are each corrected in place; see the finding-resolution table below.
> - **m-1 is REBUTTED with evidence and inverted.** The review said AC-13's `lifecycle.go:572-575` was one line past the block. Verified: `:571` is the closing brace of the `AgentID` clause and the `ParentAgentID` comment block is exactly `:572-575`. **The ADR is right and this spec was wrong** — FR-022, BDD-22 and US-4 AS-5 are corrected from `:571-575` to `:572-575`.
> - **New defect found while verifying (not in the grill):** the spec said "the **seven** role-B predicates" while citing **eight** sites in **eight** distinct functions. Both numbers are now stated with their scope — eight reads pre-change, seven post-change (W13 collapses `InterruptSession`/`InterruptSessionHard` into one).
>
> **Operator decisions (settled — not re-litigated):** subordinate sessions are listed **nested under their parent**, which is real UI work and gets its own user story (US-19), its own units (U24) and its own FRs/BDDs/tests rather than a filter flag [1]; flush interval 5 s as a **MUST** config key [2]; AC-10's budget becomes a **slope** assertion [3]; the root-delegation cap **reuses `agents.defaults.subturn.max_concurrent`** [4]; `delegate cancel` becomes `ScopeSubtree` rooted at the child [5]; a refused delegation is a tool error plus `slog.Error` [6]; items 7–12 stand as the agent defaults [7–12].
>
> **Where a finding and a decision pulled against each other:** grill **M-7** argued AC-10 is unsatisfiable because operator decision [4] reuses a cap the UAT recorded as 16, so "refuses the 25th" cannot run. **Both are honoured.** Verified (at the time): `getSubTurnConfig` (`pkg/agent/subturn.go:64-69`) reads `agents.defaults.subturn.max_concurrent` **unclamped** when it is > 0, and only falls back to `Performance.EffectiveMaxParallelAgents()` — the value `clampParallelExplicit` caps at 16 (`pkg/config/config.go:459-468`) — when it is ≤ 0. Setting `subturn.max_concurrent = 24` therefore satisfies AC-10's 24/25 topology **literally**, with no ADR amendment and no second knob. FR-095 pins the cap to that key and forbids sourcing it from the clamped one. `[AMENDED 2026-08-04]` Commit `536b7340` removed `clampParallelExplicit`'s ceiling — `EffectiveMaxParallelAgents()` is no longer capped at 16, which invalidates the "only one is clamped" distinction this note's verification rested on. AC-10's 24/25 topology still runs as written (setting either knob to 24 still produces a 24 cap), but FR-095 itself is corrected — see the top-of-file AMENDMENT note and FR-095's own `[AMENDED 2026-08-04]` entry.
>
> **ID stability.** AC-1 … AC-22 are byte-identical to ADR-057 v4 (machine-diffed, zero differences). **No existing FR/BDD/SC/test/unit ID was renumbered or reused for a different subject.** Additions only: **FR-079…FR-096**, **BDD-88…BDD-107**, **SC-035…SC-049**, tests **#84…#111**, units **U22/U23/U24**, and **US-19**. Two IDs changed **meaning** rather than number and are called out where they appear: **FR-031** (now a two-key operation) and **FR-047** (now a concrete static gate rather than an untestable meta-requirement). Three tests were renamed because their old names described the vacuous assertion: #7, #25, #31, #56. New BDD scenarios are placed **with the user story they trace to**, so numeric order and document order diverge; the set remains contiguous.
>
> **Counts.** v1 → v2: user stories 18 → **19**; FRs 78 → **96**; BDD scenarios 87 → **107**; success criteria 34 → **49**; test entries 84 → **112**; work units 21 → **24**; datasets 6 → **9**; waves 5 → **7**.
>
> **One finding is also a security promotion.** The review's STRIDE table noted that a child id colliding with an existing session directory — a tampering hazard, live today because `os.MkdirAll` at `pkg/session/unified.go:463` is idempotent and silent — had its **only** coverage inside the post-implementation evaluation set, which the implementing agent never sees. Its *property* is promoted to FR-096 / BDD-107 / #111 / SC-049; the evaluation scenario itself stays where it was, and **no evaluation-set identifier appears anywhere in the visible plan** (machine-checked).

### Grill #1 finding resolution

| # | Severity | Finding | Resolution | Where |
|---|---|---|---|---|
| C-1 | CRITICAL | FR-031 re-keys `Inherit` into a silent no-op | **Corrected** — two-key `InheritFrom` | FR-031, FR-079, BDD-88, BDD-89, #84, #85, SC-039 |
| C-2 | CRITICAL | FR-003/BDD-04/#7 target an already-counted path | **Corrected** — assert the log record + counter delta | FR-003, US-1 AS-4, BDD-04, #7, "Silent-today claims" |
| C-3 | CRITICAL | 11 negative gates pass on an empty search | **Corrected** — binding rule 4 + stated bounds | Rule 4, FR-085, BDD-97, #91, SC-003, SC-035 |
| C-4 | CRITICAL | W24 throttle can be dead with 4 tests green | **Corrected** — unforced-flush requirement + negative control | FR-083, BDD-93, #89, #20, dataset row 7, SC-036 |
| C-5 | CRITICAL | Clobber relocated from file to `metaCache`, untested | **Corrected** — field-group-only mutation | FR-084, BDD-94, #90, SC-037 |
| C-6 | CRITICAL | Owner copy is a two-session op FR-050 forbids | **Corrected** — explicit protocol + FR-050 exception | FR-082, FR-050, BDD-92, #88, SC-038 |
| M-1 | MAJOR | 5 unowned files + 2 unassigned pagination layers | **Corrected** — U22/U23 added, all layers assigned | Ownership table, FR-092 |
| M-2 | MAJOR | U20 in Wave A depends on U18 in Wave C | **Corrected** — U20 split; cascade wiring is U18's W18b | Ownership table, #62 |
| M-3 | MAJOR | U7's undeclared dependency on U17 | **Corrected** — declared for U7 and U9 | Ownership table, cross-unit requests |
| M-4 | MAJOR | U21's 12 files collide with 8 units' tests | **Corrected** — Rule 5: new tests go in new files | Rules 5–6, TDD plan |
| M-5 | MAJOR | U2's error path defined by concurrent U5 | **Corrected** — `readMetaLocked` signature frozen | Ownership table, cross-unit requests |
| M-6 | MAJOR | No FR re-keys the approval **registry** | **Corrected** — FR-080/FR-081 + approve round-trip | FR-080, FR-081, BDD-90, BDD-91, #86, #87, SC-040 |
| M-7 | MAJOR | AC-10 internally contradictory | **Corrected + partly rebutted** — slope [3]; the cap **is** reachable at 24 (evidence above) | FR-095, SC-044, #72, #102 |
| M-8 | MAJOR | BDD-16 false for ≥5 of 19 rows | **Corrected** — split into three classes (a)/(b)/(c) | BDD-16, BDD-98, BDD-99, FR-089, SC-006 |
| M-9 | MAJOR | BDD-36/#56 satisfied by total child-transcript loss | **Corrected** — both halves in one run | BDD-36, #56, SC-004 |
| M-10 | MAJOR | FR-045 eviction has no policy or bound | **Corrected** — trigger + bound + measurable test | FR-045, FR-087, BDD-52, #93, SC-042 |
| M-11 | MAJOR | 7 traceability rows don't test their FR | **Corrected** — real tests added; fabricated AC column blanked | #103–#107, FR-047, matrix |
| M-12 | MAJOR | FR-030 tested only at depth 1 in the visible plan | **Corrected** — depth-3 scenario added to the visible plan | BDD-100, #97, SC-046 |
| M-13 | MAJOR | No unit owns the child-terminal `CloseSession` | **Corrected** — U7 calls, U17 owns the entry point | FR-088, BDD-96, #94, ownership table |
| M-14 | MAJOR | FR-051's reconcile/snapshot stale-read window | **Corrected** — consistency model stated + tested | FR-086, BDD-95, #92, SC-041 |
| m-1 | MINOR | AC-13 cites the wrong line | **REBUTTED with file:line evidence — inverted.** `:572-575` is correct; the **spec** was off by one | Citation corrections, FR-022, BDD-22 |
| m-2 | MINOR | BDD-65 has no "file exists" precondition | **Corrected** — prior forced flush required | BDD-65, SC-023 |
| m-3 | MINOR | #81 claims an unenforceable property | **Corrected** — presence + marker comment; semantics to review | #81, FR-072 |
| m-4 | MINOR | Wave collision inside one Go package | **Corrected** — Rule 6, unit-prefixed helpers | Rules |
| m-5 | MINOR | FR-067 is the only `SHOULD` | **Corrected** — promoted to MUST [operator 2] | FR-067, #105, SC-048 |
| o-1 | OBS | "21 units / 5 waves" overstates parallelism | **Accepted** — true critical path stated, 7 waves | Integration order |
| o-2 | OBS | W20's named types enforced at one site | **Corrected** — conversion boundary made explicit | FR-090 |

---

## The governing constraint: silent failure

Read this before anything else in this document.

> Almost every failure in this migration is **success-shaped**: a predicate returns "nothing to do" and every caller proceeds happily.

The canonical mechanism, verified 2026-08-03 against the live tree:

```go
// pkg/session/unified.go:819-823   (inside AppendTranscript)
meta, err := us.readMetaLocked(sessionID)
if err != nil {
    slog.Warn("unified_store: could not update meta stats", "session_id", sessionID, "error", err)
    return nil
}
```

The line before it (`pkg/session/unified.go:814`) calls `fileutil.AppendJSONL`, which begins with `os.MkdirAll(dir, 0o700)` (`pkg/fileutil/file.go:207-210`). So an append against a session id that **does not exist** creates the directory, writes the line, fails the meta read, logs a WARN, and **returns `nil`**. Its read counterpart is symmetric: `ReadTranscript` returns `[]TranscriptEntry{}, nil` on `os.IsNotExist` (`pkg/session/unified.go:1192-1194`).

This is a silent **create**, not a silent drop. An assertion of the form *"the append succeeded"* can therefore never fail, which is why a green test suite currently proves almost nothing about this migration.

The project has been burned by exactly this shape before, and the code says so:

```go
// pkg/agent/plan_engine.go:3937-3944
// Both MUST be REAL, store-backed sessions. A derived or composed id
// ("plan:<id>") is forbidden and is the defect this replaces: nothing in the
// tree ever CREATED that session, so processSystemMessage's transcript
// resolution (which resolves by GetMeta against a real store) dropped it, the
// turn ran with an empty transcriptSessionID, and RequestCancelForSession —
// which matches on exactly that value — found nothing to cancel. Every test
// of that cascade passed anyway, because the fake canceller records the string
// it was handed and returns success.
```

**Four rules bind every test in this spec, without exception:**

1. **Every acceptance criterion is verified against REAL store-backed state and a REAL registered turn.** A spy, fake, or mock that records the argument it was handed and returns success is **disallowed**. Where a test needs a store, it gets a real `UnifiedStore` rooted at a `t.TempDir()`. Where it needs a turn, it gets a turn registered in `activeTurnStates`.
2. **Assertions land on observable artefacts, not on invocation.** Files on disk and their bytes; process IDs that are gone; registry entries that no longer resolve; SPA store buckets. Never "the flush function was called".

> **The one narrow exception to rules 1–2, stated as a rule rather than left as an oversight.** `[grill2 M2-6]` Three properties in this spec are **about** ordering or cost and therefore have no observable artefact to land on: **lock-acquisition order** (#88 / SC-038), **deterministic interleaving at a stated boundary** (#92 / SC-041), and **cache-hit disk-read cost** (#103 / FR-058). Go's race detector is not a lock-order checker and a correct cache hit leaves no trace, so for these three — **and only these three** — a test MAY assert on instrumented invocation, provided the instrumentation is a **production seam required by an FR** (FR-101, FR-102, FR-103) rather than a test-only substitute for the thing under test. A reviewer applying rules 1–2 literally would otherwise have to reject the only tests covering three success criteria. Every other test in this suite is bound by rules 1–2 without exception.
3. **Cross-process and store-level guarantees copy the shape of `pkg/entity/store_crossprocess_test.go`** — which re-execs the test binary as real OS processes (`//go:build !windows`, verified present). Performance properties assert a **slope** (doubling concurrency must not double wall-clock), never a machine-specific constant.
4. **Every negative, exclusion or static gate MUST first prove its search is live.** `[grill C-3]` Rules 1–3 address spies, invocation assertions and cross-process guarantees. They say nothing about the third failure shape this migration is full of: a test whose assertion is *"the search returned zero results"*, which is green whenever the search **itself** is broken — a typo'd pattern, a renamed symbol, a fixture that is never compiled, a drifted file path, a parser that silently returns no nodes. Such a test MUST, in the same run and before its zero-assertion, assert a **stated positive lower bound** — that it located at least K of the occurrences it is supposed to be scanning, where K is written down in this spec so drift is visible in code review. A gate that cannot state a positive lower bound is not a gate and MUST NOT be counted as coverage. This rule is FR-085; the bounds are in "Negative-gate positive lower bounds" below.
>
> **Rule 4 is GENERATIVE, not a list of eleven.** `[grill2 M2-5]` v2 stated rule 4 universally ("every negative, exclusion or static gate") but **enforced** it against a closed list of eleven rows, and then added two more gates of exactly that shape — #106 and #104 — which sat outside both the bounds table and #91's scope. #106 is the **sole** coverage of FR-023, one of the two security properties of the parentage design, so an unbounded gate left it unenforced while reporting green: C-3's own finding, inside C-3's correction. The membership rule is therefore stated as a **predicate**, not an enumeration: **every row of the TDD plan whose assertion is an exclusion, a zero-count, a "no such thing exists", or a compile-must-fail appears in the bounds table below, and #91 iterates that table rather than a hardcoded list.** Adding a gate without adding its bound is a spec defect a reviewer must reject. The table currently has **thirteen** rows.

**Corollary — distinct ids everywhere.** `pkg/agent/message_parent_real_context_test.go:16-17` already records that its fixture *"happens to make `ToolTranscriptSessionID`"* equal the seeded id, i.e. an existing test would **not** catch a divergence introduced here. Every test written for this spec MUST construct parent and child ids as distinct, non-equal values and assert on **which one** was used.

### Negative-gate positive lower bounds

`[grill C-3; grill2 M2-5]` Every gate below satisfies rule 4's membership predicate. Each row states the positive assertion that MUST run **first**, in the same test. Counts marked *(verified 2026-08-03)* were measured on `feature/plan-swimlane-board` and are the review anchor: if the measured count drifts, the gate is stale and the reviewer must re-derive it, not relax it. **This table is the input to #91** — #91 iterates it, so a gate added to the TDD plan without a row here is not covered by the mutation check either.

| Test | Gate | Required positive lower bound (assert first, in the same run) |
|---|---|---|
| #3 `TestSessionIDTypes_DoNotInterconvert` | compile-fail fixture | The fixture file MUST exist and MUST be located by path; `go build` on it MUST **fail** with a type error naming both types. A missing/unreadable fixture is a **test failure**, never a pass |
| #9 `TestCacheMu_NoFilesystemInCriticalSection` | AST gate over `cacheMu` regions | MUST locate **≥ 3** `cacheMu` critical sections (`cacheMu` does not exist today — `grep -c cacheMu pkg/session/unified.go` = **0**, verified; W15 creates it, and FR-048 requires it to guard `metaCache` reads, `metaCache` writes and `cacheLoadFailures`). Zero located is a failure |
| #12 `TestLifecycleDocComments_NoSharedParentChildClaim` | doc-truth grep | MUST locate **all 3** comment blocks by anchor text before asserting content: `pkg/session/lifecycle.go:225-228`, `:572-575`, `pkg/tools/list_jobs_sources.go:311-315` (all three verified present). Locating fewer than 3 is a failure |
| #17 `TestMetaWriters_WriterIsolationByteLevel` | byte-comparison of "other" files | MUST assert all **4** files exist with **non-zero** length and a **distinct** content hash before any "unchanged" assertion |
| #19 `TestMetaDocComments_NoSingleFunnelClaim` | doc-truth grep | MUST locate **both** blocks (`pkg/session/unified.go:776-785` — verified: `writeMetaLocked`'s doc comment opens at `:776` and the "single invalidation/update point" sentence is at `:780` — and `:166-181`) before asserting content |
| #27 `TestInterruptScope_RequiredByCompiler` | compile-fail fixture | As #3 |
| #29 `TestRoutingSessionID_ConsumerSetIsClosed` | enumerate reads, assert none outside the set | MUST assert it enumerated **≥ K** reads before asserting none is outside the set. **K = 10 post-change = 7 role-B predicate reads + 3 pre-arm reads**, each derived **by site** in "Eight sites, seven predicates" and "Three reads, five sites" below, **plus** the WS-payload stamping sites, for which the bound is **the exact count in the W5 audit artefact (FR-089)** — not "≥ 1". `[grill2 M2-8]` "Read" is defined precisely below; a gate whose enumerator counts identifier occurrences instead of selector expressions will not reproduce K and must be treated as broken, not relaxed. Enumerating zero is the D2 safety property silently unenforced |
| #58 `TestIsDelegateChildEntry_ZeroNonTestReferences` | grep-for-zero | MUST first assert **≥ 60** non-test **Go** references to `ParentSpawnCallID` (measured: **73** across 9 non-test Go files, verified 2026-08-03), proving the file set and the search both work, before asserting zero for `IsDelegateChildEntry`. **The search MUST be restricted to Go source** — see SC-003 |
| #81 `TestGateTestsInvertedNotDeleted` | file presence | MUST assert all **12** files are present **and** each contains the marker comment `// ADR-057-W22-inverted`. Scope is presence + marker only (`[grill m-3]`) |
| #82 `TestW22CommitContainsOnlyTests` | commit-shape gate | MUST first resolve the W22 commit by its marker and assert it exists and its file list is **non-empty**; "commit not found" is a **failure**, not a pass |
| #83 `TestAllFixturesUseDistinctParentChildIDs` | fixture discovery | MUST assert it discovered **≥ 20** fixtures constructing a parent/child id pair before asserting all are distinct. Discovering zero is a failure |
| **#104** `TestMigrateLegacy_BytesUnchanged` | golden bytes + "no fused reader exists" | `[grill2 M2-5]` MUST first assert (a) it located **≥ 1** `migrateLegacy` golden fixture and that fixture is **non-empty**, and (b) it located the **2** symbols whose output it is pinning — `migrateLegacy` and `writeUnifiedMetaDirect` (`pkg/session/unified.go:1515`, verified present) — before asserting either the byte equality or the zero-fused-readers clause. A search that finds neither symbol currently passes both clauses |
| **#106** `TestParentageWalk_NeverReadsOwnerScopeIDOrParentAgentID` | zero reads of two fields over the walk | `[grill2 M2-5]` **"The walk's code path" is defined as an explicit symbol set, not a phrase:** `verifyCallerOwnsSession` and `callerOwnerKey` (`pkg/tools/delegate.go:1973-1979`, `:1966-1968`) plus their transitive callees **within `pkg/tools` and `pkg/session`**, and `LifecycleFilter.matches` (`pkg/session/lifecycle.go:565+`). The test MUST first assert (a) it resolved **all 3** named symbols, (b) it walked **≥ 1** transitive callee edge, and (c) it located **≥ 1** read of `ParentDurableKey` inside that set — proving the walk it is scanning is the real one — before asserting **zero** reads of `OwnerScopeID` or `ParentAgentID`. This gate is the sole coverage of FR-023, a security property: unbounded, it reports green over an empty symbol set |

### Eight sites, seven predicates

`[found while verifying; not a grill finding]` v1 said "the **seven** role-B predicates" while citing **eight** line numbers. Both are right, at different times, and the ambiguity mattered because it sets K for test #29. Verified 2026-08-03, each citation resolved to its enclosing function:

| Site | Enclosing function |
|---|---|
| `pkg/agent/steering.go:429` | `collectDescendantTurnIDs` |
| `pkg/agent/steering.go:459` | `InterruptSession` |
| `pkg/agent/steering.go:519` | `InterruptSessionHard` |
| `pkg/agent/steering.go:745` | `sessionTurnsStillAlive` |
| `pkg/agent/steering.go:787` | `hasLiveCriticalDelegate` |
| `pkg/agent/turn.go:524` | `GetActiveTurnHookForSession` |
| `pkg/agent/turn.go:564` | `resolveSessionIDByChannelChat` |
| `pkg/agent/turn.go:607` | `getActiveRootTurnStateForSession` |

**Eight distinct functions today. Seven after W13**, which collapses `InterruptSession` and `InterruptSessionHard` into one entry point (FR-041), removing one read. Wherever this spec says "seven role-B predicates" it means the **post-change** set; FR-015's eight citations are the **pre-change** sites to re-base.

### Three reads, five sites — and what "read" means

`[grill2 M2-8]` v2 set #29's K at "7 role-B + **3** pre-arm key reads". The 7 was derived rigorously above; **the 3 was never derived anywhere**, while FR-016 names **five** pre-arm sites. A reviewer could not tell a working gate from a broken one — which is exactly the state binding rule 4 exists to end.

**Definition (normative, for #29 and every gate that counts "reads").** A **read** is an AST `SelectorExpr` whose selector identifier is the field name, appearing in non-test Go source, **excluding** the field's own declaration and excluding the left-hand side of an assignment to it. Comments, string literals, parameter identifiers that merely *carry* the value, and calls to helpers that take the value are **not** reads. An enumerator counting identifier occurrences or grep hits will not reproduce K.

Verified 2026-08-03, each of FR-016's five citations resolved:

| Site | What is actually there | A read? |
|---|---|---|
| `pkg/agent/cancel_prearm.go:354` | `if ts.transcriptSessionID != "" {` | **yes** — selector on the turn state |
| `pkg/agent/cancel_prearm.go:355` | `keys = append(keys, "s:"+ts.transcriptSessionID)` | **yes** |
| `pkg/agent/subturn.go:585` | `pendingSpawnKeys(parentTS.transcriptSessionID, parentTS.channel, parentTS.chatID)` | **yes** |
| `pkg/agent/cancel_prearm.go:338` | `return "s:" + sessionID` — inside `preArmKeyForScope(sessionID string, …)` | no — a **parameter**, fed from `:355` |
| `pkg/agent/cancel_prearm.go:602` | `al.cancelPreArm.consume(time.Now(), preArmKeysForTurn(ts)...)` | no — a **call** taking the whole turn state |
| `pkg/agent/subturn.go:1147` | `al.cancelPreArm.clearPendingSpawn(pendingSpawnKeysForThisCall...)` | no — consumes a **precomputed slice** |

**Three reads across five sites.** K's pre-arm component is **3** and the number is now derived rather than asserted. The two counts are different things and both are load-bearing: **FR-016's five sites** are what W4 must re-base (three directly, two transitively); **#29's three** are what the gate must enumerate. A gate that enumerates 5 is as broken as one that enumerates 0 — it is counting the wrong construct.

**The WS-payload arm is no longer "≥ 1".** `[grill2 M2-8]` FR-012 requires **every** session-scoped frame's `session_id` to come from `routingSessionID`; a gate satisfied by one stamping site cannot see nineteen minus one. #29's WS bound is **the exact stamping-site count recorded in the W5 audit artefact** (FR-089), and #29 MUST read that artefact rather than hardcoding a number — which also makes the artefact load-bearing instead of decorative.

---

## Citation corrections (verified 2026-08-03, `feature/plan-swimlane-board`)

ADR-057 v4 demands citation accuracy as the floor (finding m-5). Every ADR citation this spec depends on was re-opened. The corrections below are what this spec uses. **No ADR *decision* changes as a result — these are pointer fixes.**

| Source says | Verified actual | Impact |
|---|---|---|
| ADR: `unified.go:1194-1196` — `ReadTranscript` silent-empty | `pkg/session/unified.go:1192-1194` (`if os.IsNotExist(err) { return []TranscriptEntry{}, nil }`) | none — same construct, off by 2 |
| ADR: `websocket.go:4254` — "streamed transcript write" | `:4254` is the `ParentSpawnCallID: parentSpawnCallID,` stamp; the `AppendTranscript` call is `pkg/gateway/websocket.go:4256` | W3 must convert `:4256`; W11's provenance retention concerns `:4254` |
| ADR: `session_messaging_wire.go:141-143`, `normalization.go:247-254`, `media/tempdir.go:33-51` (no package prefix) | `pkg/agent/session_messaging_wire.go:141-143` (NOT `pkg/gateway/`), `pkg/tools/normalization.go:247-254`, `pkg/media/tempdir.go:33-51` — all three line ranges exact | file-ownership assignment only |
| **v1 spec: `lifecycle.go:571-575`** (FR-022, BDD-22, US-4 AS-5) vs **AC-13: `:572-575`** | **`:572-575` is correct.** Verified: `matches` opens at `:565`; `:566-568` is the `WorkspaceID` clause; `:569-571` is the `AgentID` clause, whose closing `}` is `:571`; the `ParentAgentID` comment block is exactly `:572-575` | `[grill m-1 — REBUTTED and inverted]` The review asserted AC-13 was one line past the block. It is not: **the spec was**. FR-022/BDD-22/US-4 AS-5 corrected to `:572-575`; AC-13's governing text stands unamended |
| **v1 spec + ADR W3: `turn.go:1296-1299` "is entirely silent today"** | **False.** `pkg/agent/turn.go:1295-1298` reads `if ts.abandoned.Load() { abandonedWritesSuppressed.Add(1); return }`. The counter is declared `:25`, documented `:21-24` as backing `omnipus_abandoned_writes_suppressed_total`, exported `:44`, and incremented at **seven** sites (`turn.go:866`, `:1097`, `:1172`, `:1226`, `:1297`, `:1496`; `loop.go:7596`). A passing test already exists (`pkg/agent/turn_test.go:221`) | `[grill C-2]` **Only the log line is missing.** FR-003, US-1 AS-4, BDD-04 and test #7 rewritten to assert the WARN record and a counter **delta** |
| v1 spec: `approvalgrants.go:112-123` | `pkg/security/approvalgrants.go:112-129` — the function body runs to `:129`; the silent `return` on an empty source set is `:118-120` | `[grill C-1]` the silent branch is inside the cited range and is what FR-031 must not trip |
| v1 spec: `writeMetaLocked` doc comment `:780-785` | The doc comment opens at `:776`; `:780` is the "single invalidation/update point for every mutation path" sentence; the func signature is `:786`. The whole-document cache refresh `us.metaCache[sessionID] = meta.Clone()` is `:798` | `[grill C-5]` FR-059/#19 must locate `:776-785`; FR-084 targets `:798` |
| v1 spec: "the **seven** role-B predicates" + eight citations | Eight distinct functions pre-change, seven post-W13 | see "Eight sites, seven predicates" |

### Silent-today claims, re-verified

`[grill C-2]` C-2 proved that at least one "X is silent today" claim propagated from the ADR into three places in this spec without being re-checked. Every remaining claim of that shape was re-opened against the tree on 2026-08-03. **Each row below is the evidence a reviewer should demand before accepting the requirement built on it.**

| Claim | Verdict | Evidence |
|---|---|---|
| `AppendTranscript` returns `nil` after a failed meta read | **TRUE** | `pkg/session/unified.go:819-823` — `slog.Warn(...)` then `return nil` |
| `fileutil.AppendJSONL` `MkdirAll`s first, so the append is a silent **create** | **TRUE** | `pkg/fileutil/file.go:207-210` |
| `ReadTranscript` returns `[]TranscriptEntry{}, nil` on `IsNotExist` | **TRUE** | `pkg/session/unified.go:1192-1194` |
| `Inherit` returns silently when the parent holds no grants | **TRUE** | `pkg/security/approvalgrants.go:118-120`; documented as intended at `:110-111` |
| `ts.abandoned` suppression is silent | **FALSE — already counted** | `pkg/agent/turn.go:1297`; see the row above |
| `createSessionLocked` constructs `UnifiedMeta` with **no** `Owner` field | **TRUE** | `pkg/session/unified.go:448-460` — the literal sets `ID`, `AgentID`, `AgentIDs`, `ActiveAgentID`, `Status`, `Channel`, `CreatedAt`, `UpdatedAt`, `Type` only |
| `createSessionLocked` does `os.MkdirAll` with no existence check | **TRUE** | `:463` — idempotent and silent; this is the child-id-collision hazard (FR-096) |
| `writeMetaLocked` refreshes the **whole** cache entry | **TRUE** | `:798` `us.metaCache[sessionID] = meta.Clone()` |
| `ListSessions` runs entirely under `us.mu.Lock()` and says why | **TRUE** | `:1240-1246` doc comment, `:1248` `us.mu.Lock()` |
| `cancelAllPendingForSession` matches by **exact** session-id equality | **TRUE** | `pkg/gateway/approvals.go:419`; the field is `:85`, set at `:213`/`:232` |
| `CloseSession` has **no** child-turn-terminal call site | **TRUE** | defined `pkg/agent/session_end.go:32`; every non-test call site is `pkg/gateway/websocket.go:1038` ("explicit"), `pkg/agent/loop.go:1048`/`:1064` ("idle"), `pkg/agent/session_end.go:865` ("bootstrap") |
| `RateLimitPayload` has no `SessionID` field | **TRUE** | `pkg/agent/events.go:525-533` — `Scope`, `Resource`, `PolicyRule`, `RetryAfterSeconds`, `AgentID`, `ChatID`, `Tool` |
| `replay_done` is absent from the `WsFrameType` enum on both sides | **TRUE** | tree-wide it appears **only** at `src/store/chat.ts:1238`; zero hits in `contracts/`, `pkg/api/generated/`, `pkg/` |
| `AdmissionController` does not gate subagent spawn | **TRUE** | `pkg/agent/admission.go:12-18`, verbatim: *"Subagent spawn and task-executor dispatch paths are NOT gated"* |
| `concurrencySem` is set only on a child | **TRUE** | `pkg/agent/subturn.go:1051` is the only assignment; the acquire guard is `:607` |
| There is **no pagination at any layer** | **TRUE** | `pkg/session/unified.go:1247` (no params), `pkg/agent/loop.go:5046` (no params), `pkg/gateway/rest.go:758-812` (reads only `agent_id`, `type`, `include_verifier`), `src/lib/api.ts:1379-1388` (`fetchSessions(agentId?, type?, opts?)` — no limit/offset) |
| Sidebar shows only the 9 most recent by recency | **TRUE** | `src/components/layout/Sidebar.tsx:456-457` — `const maxVisible = 9` / `.slice(0, maxVisible)` |
| `SearchModal` renders the session list unvirtualized | **TRUE** | `src/components/search/SearchModal.tsx:363` fetches the full list, `:687` `groups.map(...)` renders it with no windowing |
| `SESSION_SCOPED_FRAME_TYPES` has exactly 19 members | **TRUE** | `src/store/chat.ts:1236-1249`, counted |
| `subturn.go:916` passes the **parent's** transcript id to `Inherit` | **TRUE** | `al.ApprovalGrants().Inherit(parentTS.transcriptSessionID, parentTS.agentID, agent.ID)` |
| `loop.go:8617` reads the grant under `ts.transcriptSessionID` | **TRUE** | `approved := al.ApprovalGrants().IsAllowed(ts.transcriptSessionID, ts.agentID, toolName)`; the 300 s fallthrough is `:8630-8631` |

Everything else this spec cites was re-verified exact, including: `pkg/session/unified.go:161` (single `sync.RWMutex`), `:405-418`/`:410`, `:415-416`, `:439-440`, `:448-460` (the `UnifiedMeta` literal — **no `Owner` field**), `:463`, `:466`, `:472`, `:582`, `:586`, `:614`, `:764`, `:786`, `:810-811`, `:819-823`, `:824-847`, `:848`, `:1247`, `:1388`, `:1397`, `:1494`, `:182`, `:192`; `pkg/fileutil/file.go:97`/`:121` (file and parent-directory `Sync()`); `pkg/session/lifecycle_lock.go:17`/`:29-31`/`:35-39`; `pkg/session/message_inbox.go:139`; `pkg/entity/lock.go:12`; `pkg/session/lifecycle.go:543-563` (exactly five filter fields) and `:572-575` (`matches` refusing `ParentDurableKey` — **corrected from `:571-575`**, see the table above); `pkg/session/daypartition.go:209-223` (`SessionStats`, **9** fields) and **9** `Goal*` + **9** `Loop*` fields in `SessionMeta`, `:332-334` (`IsDelegateChildEntry`); the four filter sites `pkg/gateway/replay.go:298`, `pkg/gateway/rest.go:826` (helper `:823-832`), `pkg/agent/verifier_adjudication.go:406`, `pkg/tools/inspect_session.go:172`; `pkg/agent/subturn.go:916`, `:1020`, `:1032`, `:1034`, `:1051`; `pkg/tools/delegate.go:1105`, `:1106`, `:1117-1122`, `:1123`, `:1966-1968`, `:1973-1979`; `pkg/agent/turn.go:1130`/`:1208`/`:1270`/`:1325`; `pkg/agent/loop.go:6844-6848`; `pkg/agent/cancel.go:233-234`, `:462`, `:487`; `pkg/agent/steering.go:425`/`:449`/`:511`/`:611`/`:665`/`:738`/`:780`; `pkg/agent/admission.go:12-18`; `pkg/security/approvalgrants.go:112-129` (**corrected from `:112-123`**); `src/store/chat.ts:1236-1249` (**19** `SESSION_SCOPED_FRAME_TYPES`, counted) and `:2883-2885`. All twelve `*_test.go` files named by W22 exist.

**`producing_session_id` (W5) is genuinely new**: `rg -c producing_session_id contracts/ src/ pkg/` returns zero matches tree-wide.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Site | Role in this change |
|---|---|---|
| `UnifiedStore.AppendTranscript` | `pkg/session/unified.go:802` | **becomes strict** (W3) — the silent-create branch at `:819-823` is **deleted**, not preserved behind a sibling `[grill2 C2-3]`; the counter path becomes in-memory (W24) |
| `UnifiedStore.createSessionLocked` | `pkg/session/unified.go:441` | **extended** — exported exact-id wrapper (W1); must copy parent `Owner` |
| `UnifiedStore.mu` | `pkg/session/unified.go:161` | **replaced** — 64-shard `sessionLock` + narrow `cacheMu` (W15) |
| `UnifiedStore.writeMetaLocked` | `pkg/session/unified.go:786` | **replaced** — four targeted writers (W23) |
| `UnifiedStore.readMetaLocked` / `readUnifiedMeta` | `:764` / `:1494` | **extended** — compose four files (W23) |
| `UnifiedStore.ListSessions` | `pkg/session/unified.go:1247` | **modified** — per-session reconcile, `cacheMu.RLock` snapshot (W15); paginated (W16) |
| `UnifiedStore.Close` | `pkg/session/unified.go:1388` | **extended** — gains a flush hook that does not exist today (W24) |
| `SessionMeta` | `pkg/session/daypartition.go:76-185` | **extended + split** — `ParentSessionID` (W2); persistence splits four ways (W23) |
| `SessionStats` | `pkg/session/daypartition.go:209-223` | **relocated** — becomes `stats.json` (W23) |
| `TranscriptEntry.IsDelegateChildEntry` | `pkg/session/daypartition.go:332-334` | **deleted** (W11) |
| `LifecycleFilter` / `matches` | `pkg/session/lifecycle.go:543-563` / `:565+` | **extended** — `ParentDurableKey` field + clause + parent index (W6) |
| `lifecycleStripedLock` | `pkg/session/lifecycle_lock.go:17-39` | **pattern source** — copied verbatim for `sessionLock` (W15) |
| `turnState.transcriptSessionID` | `pkg/agent/turn.go:225` | **split** — role A stays; roles B/C move to `routingSessionID` (W4) |
| `spawnSubTurn` | `pkg/agent/subturn.go` | **modified** — mints a real session, drops `NoHistory` (W1) |
| `AgentLoop.InterruptSession` / `InterruptBySessionKey` (+`Hard`) | `pkg/agent/steering.go:449`/`:611`/`:511`/`:665` | **collapsed** into one scoped entry point (W13) |
| `collectDescendantTurnIDs`, `sessionTurnsStillAlive`, `hasLiveCriticalDelegate` | `pkg/agent/steering.go:425`/`:738`/`:780` | **re-based** onto `routingSessionID` (W4) |
| `RequestCancel` | `pkg/agent/cancel.go` | **modified** — subtree computed once, durable walk added (W8) |
| `ApprovalGrantStore.Inherit` | `pkg/security/approvalgrants.go:112` | **re-keyed** to the child session (W10) |
| `verifyCallerOwnsSession` / `callerOwnerKey` | `pkg/tools/delegate.go:1973-1979` / `:1966-1968` | **replaced** — ancestor-chain walk (W12) |
| `AdmissionController` | `pkg/agent/admission.go:12-18` | **extended** — gates root-level delegation (W17) |
| `SessionUploadsDir` | `pkg/media/tempdir.go:33-51` | **cascade** — child dirs reachable by parent delete (W18) |
| `SESSION_SCOPED_FRAME_TYPES` | `src/store/chat.ts:1236-1249` | **audited** — all 19 types against the routing rule (W5) |
| `handleFrame` bucketing | `src/store/chat.ts:2883-2885` | **contract anchor** — the bucket key that D2 exists to protect |

### Impact Assessment

Blast radius measured by the ADR's own enumeration command (`rg -n "transcriptSessionID" --glob '!*_test.go' pkg/` → 116 refs / 18 files; `ToolTranscriptSessionID(` → 19 call sites), plus ~430 references across ~71 test files.

| Symbol modified | Risk | d=1 dependents | d=2 dependents |
|---|---|---|---|
| `turnState.transcriptSessionID` | **CRITICAL** | 4 transcript writers, 7 subtree predicates, 6 WS payload stampers, pre-arm keys, grants, uploads, manifest, audit | the whole cancel ladder, ADR-045 watchdog, SPA span/step correlation |
| `UnifiedStore.mu` | **HIGH** | every `UnifiedStore` method | every session-writing subsystem; latency of unrelated sessions |
| `writeMetaLocked` | **HIGH** | `createSessionLocked`, `SetMeta` (31 call sites), `AppendTranscript`, `SwitchAgent` | goal/loop state machines, boot sweep, REST session payloads |
| `IsDelegateChildEntry` | **MEDIUM** | 4 read boundaries (5 effective, `rest.go` helper serves 2 handlers) | every rendered historical chat |
| `InterruptSession` family | **HIGH** | `RequestCancel`, `delegate action=cancel`, channel `/stop` | every cancellation path |
| `ApprovalGrantStore.Inherit` | **MEDIUM** | `spawnSubTurn`, `loop.go:8617`/`:8630-8631` | 300 s approval timeout inside every delegated child |
| `LifecycleFilter` | **MEDIUM** | `List`, `list_jobs` sources | durable cancel walk, `delegate action=inbox` |

**Risk note that is not a code-graph fact:** the impact figures above are counted from `rg`, not from a graph query. GitNexus `impact` on a struct *field* under-reports by default (it excludes `ACCESSES`); any implementer re-checking these numbers must pass `relationTypes` including `ACCESSES`, and must still treat the result as "what could behave differently", not "how many places must I edit".

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Delegation spawn (`delegate.run` → `spawnSubTurn` → child turn) | The subject. Gains a real session create; identity, routing, grants, manifest all re-key |
| Chat Stop / `RequestCancel` PHASE A→B→C | Must keep reaching descendants after the id split (D2/D4) |
| ADR-045 orphan watchdog | Fire predicate condition 2 (`hasLiveCriticalDelegate`) depends on the shared id |
| Cancel pre-arm latch | Marker set/cleared under the parent's identity; preserved verbatim by `routingSessionID` |
| WS frame delivery → SPA bucketing | `session_id → bucket → parent_call_id → span → call_id → step`; hop 1 breaks without D2 |
| Transcript replay / cold load / verifier window / `inspect_session` | All four stop filtering (D6) |
| Session store write path (create, append, meta) | Striped, split, and throttled (D10/D11/D12) |
| Boot sweep / crash recovery | Must reconcile a child's lifecycle record across a deploy (AC-19) |

### Cluster Placement

Spans four clusters: **agent execution** (`pkg/agent`), **session storage** (`pkg/session`, `pkg/fileutil`, `pkg/media`), **tool surface** (`pkg/tools`, `pkg/security`), and **gateway/SPA boundary** (`pkg/gateway`, `contracts/`, `src/`). The storage cluster (D10/D11/D12 → W15/W23/W24) is separable from the identity cluster and is the one place a hard internal ordering applies.

---

## User Stories & Acceptance Criteria

### User Story 1 — A lost transcript write fails loudly (Priority: P0)

An engineer verifying any other story in this spec needs the transcript primitive to be honest. Today `AppendTranscript` against an unknown session id creates an orphan directory, writes the line, and returns `nil` (`pkg/session/unified.go:814` → `pkg/fileutil/file.go:207-210`, then `:819-823`). Until that changes, every acceptance criterion in this document is measured against a primitive that reports success for a lost write.

> `[grill C-2]` **v1's Acceptance Scenario 4 was measuring nothing.** It asserted that the `ts.abandoned` suppression "emits a counted, logged signal rather than returning silently" — but the count already exists (`turn.go:1297`), so the test written from it was green against the unmodified tree, **inside the P0 story this spec designates as the gate for every other measurement**. AS-4 now names the log record as the new artefact and the counter as a delta. AS-6 is new: it makes binding rule 4 a property of this story, so the gate story also gates the *gates*.

**Why this priority**: It is the gate. ADR-057 §10 consequence 3 states it directly — "AC-1 comes first and gates the rest." Landing any other work item first means measuring it with a broken instrument.

**Independent Test**: Call the strict primitive with a freshly generated UUID against a real `UnifiedStore` on `t.TempDir()`. Assert a non-nil error and assert `os.Stat` on the would-be directory returns `IsNotExist`. No other work item need exist.

**Acceptance Scenarios**:

1. **Given** a real `UnifiedStore` with no session `X`, **When** `AppendTranscriptStrict(X, entry)` is called, **Then** a non-nil error is returned **and** no directory `<baseDir>/X` exists on disk.
2. **Given** a real `UnifiedStore` with an existing session `Y`, **When** `AppendTranscriptStrict(Y, entry)` is called, **Then** it returns nil and `transcript.jsonl` grows by exactly one line.
3. **Given** a turn whose transcript store is wired and whose session id does not resolve, **When** any of the four `pkg/agent/turn.go` writers runs, **Then** the error is surfaced as a counter increment and a WARN naming the session id.
4. **Given** a turn marked `ts.abandoned`, **When** a transcript write is suppressed, **Then** a WARN naming the session id and the suppression reason is emitted **and** `AbandonedWritesSuppressed()` increases by exactly one across the call. `[grill C-2]` **The counter already exists and already increments** — `pkg/agent/turn.go:1297`, declared `:25`, exported `:44`, seven increment sites, existing coverage at `pkg/agent/turn_test.go:221`. The new artefact is the **log record**; the counter is asserted as a **delta**, never as mere existence.
5. **Given** the compiled tree, **When** a distinct-type check runs, **Then** `SessionID` and `RoutingSessionID` are separate named types that do not interconvert implicitly, and the compile-fail fixture is proven present before its failure is asserted (binding rule 4).
6. **Given** the merged tree, **When** any negative, exclusion or static gate in this spec's suite runs, **Then** it first asserts its stated positive lower bound and fails if its search located fewer occurrences than that bound. `[grill C-3]`

---

### User Story 2 — A delegated child owns a real, store-backed session (Priority: P0)

A delegated child today carries two identity namespaces: its own `childID` (`pkg/agent/subturn.go:1020`) and the parent's `transcriptSessionID` (`:1034`), with `UnifiedStore.NewSession` never called for it (`pkg/tools/delegate.go:1248`). This story makes the child's own id its session id, its `sessionKey` and its `transcriptSessionID` — one namespace.

**Why this priority**: It is the ADR's central decision (D1) and the precondition for drill-down, per-child retention, `#564`, and the elimination of the #576/#577 defect class.

**Independent Test**: Run one delegation against a real store; assert `<baseDir>/<childID>/meta.json` exists and `GET /api/v1/sessions/{childID}` returns 200 with non-empty messages. Today that endpoint 404s.

**Acceptance Scenarios**:

1. **Given** a parent chat session and a delegation, **When** the child turn spawns, **Then** a session directory named exactly `childID` exists with a `meta.json`, created via the exact-id path (`pkg/session/unified.go:441`, precedent caller `:582`).
2. **Given** a parent session whose `meta.Owner` is a non-empty principal, **When** the child spawns, **Then** the child's `meta.Owner` equals the parent's verbatim, and `WithSessionOwner` installs inside the child turn (`pkg/agent/loop.go:6844-6848` guards on `meta.Owner != ""`).
2b. **Given** that same owner copy, **When** its lock acquisitions are recorded, **Then** the parent's shard is taken, the `Owner` is read, the parent's shard is **released**, and only then is the child's shard taken — two session shards are never held simultaneously. `[grill C-6]` Verified: `createSessionLocked` (`pkg/session/unified.go:441-478`) constructs `UnifiedMeta` with **no `Owner` field** (`:448-460`), so the value can only come from reading the **parent's** meta inside the operation that creates the **child's** session. Under W15 that is one operation touching two shards, which v1's own FR-050 flatly forbade. Honouring the prohibition by taking both anyway would acquire in **hash** order (`shard(child)`, `shard(parent)`), inverting against `ClearAll`/`RetentionSweep`'s **index**-order acquisition — the exact defect ADR-057 names as R-19. Releasing between the two accepts a benign TOCTOU on a field that is immutable after creation.
3. **Given** the child's `processOptions`, **When** they are constructed, **Then** `NoHistory` is absent (today `true` at `pkg/agent/subturn.go:1032`) and `TranscriptSessionID == childID`.
4. **Given** a child session's meta, **When** it is read, **Then** `ParentSessionID` names the direct parent and the session type is the subordinate value.
5. **Given** a child turn, **When** `steer`/`respond`/`cancel`/`peek`/`inbox`/`follow_up` are invoked, **Then** each takes the same single id it takes today, because `delegateSessionID == sessionKey == transcriptSessionID`.

---

### User Story 3 — Client routing survives the identity split (Priority: P0)

The SPA buckets frames **strictly** by the frame's own `session_id`, with no chat check (`src/store/chat.ts:2883-2885`). `tool_call_start`/`tool_call_result` sit in `SESSION_SCOPED_FRAME_TYPES` alongside `subagent_start`/`subagent_end` (19 types, `:1236-1249`). Without an explicit routing key, a delegation's span lands in bucket `<chatSid>` while its steps land in `<childID>` — on the first delegation, on the live connection.

**Why this priority**: A 100 %-reproducible break of the primary delegation UI on the happy path, whose only signal is a dev-only diagnostic (`logDiagnostic('chatAttachStepSpanIndexMiss')`, `src/store/chat.ts:1959`).

**Independent Test**: Drive one delegation through the real gateway with a real WS client; assert the SPA store's `<chatSid>` bucket contains the span and its steps, and that the miss diagnostic never fires.

**Acceptance Scenarios**:

1. **Given** a root turn, **When** `routingSessionID` is read, **Then** it equals the turn's own session id, making root behaviour byte-identical to today.
2. **Given** a child turn, **When** `routingSessionID` is read, **Then** it equals the parent's verbatim, and the pre-arm latch keys set at `pkg/agent/subturn.go:585` are the ones cleared at `:1147`.
3. **Given** a delegation on a live connection, **When** `subagent_start`, `tool_call_start`, `tool_call_result` and `subagent_end` arrive, **Then** all four file into the `<chatSid>` bucket and the span/step correlation resolves.
4. **Given** any session-scoped frame produced by a child, **When** it crosses the wire, **Then** `session_id` is the routing key and `producing_session_id` is present and equal to the child's own id.
5. **Given** the non-test tree, **When** a consumer-set test enumerates reads of `routingSessionID`, **Then** every read is inside the closed set (WS payload stamping, the seven role-B predicates, pre-arm keys) and the test fails on any read outside it.

---

### User Story 4 — The parent→child edge is durable and queryable (Priority: P0)

A Stop must find a child that is no longer in memory. `OwnerScopeID` cannot serve: it is `""` for every direct child of a chat turn (`pkg/tools/delegate.go:1117-1122`; stated as contract at `pkg/session/lifecycle.go:141-143` and `:229`). `ParentDurableKey` is stamped unconditionally (`pkg/tools/delegate.go:1106`) and becomes a genuine strict-direct-parent edge under D1 — but `LifecycleFilter` has exactly five fields and `matches` explicitly refuses to match on it (`pkg/session/lifecycle.go:543-563`, `:572-575` — **line range corrected in v2, see Citation corrections**), and `List` has no index.

**Why this priority**: Under D3 the lifecycle record becomes the **only** durable cancel edge. A missing store means a Stop cancels nothing, with no error.

**Independent Test**: Persist three real lifecycle records at depths 1–3, then query children-of-X by `ParentDurableKey` and assert exactly the direct children come back, in one file read.

**Acceptance Scenarios**:

1. **Given** lifecycle records for a chat, its child and its grandchild, **When** `List` is called with `ParentDurableKey` set to the chat id, **Then** exactly the direct child is returned — not the grandchild, not a sibling.
2. **Given** a session with N descendants, **When** the walk runs, **Then** its cost is O(descendants) via the parent index, not O(all sessions ever) per depth level (`pkg/session/lifecycle.go:617-636` is a full-directory scan plus full parse today).
3. **Given** a `DelegateTool` with no lifecycle store wired (`pkg/agent/session_messaging_wire.go:141-143` makes it optional today), **When** a delegation is attempted, **Then** it is **refused** with an operator-visible error, never a silent skip.
4. **Given** `tools.delegate.require_parent_agent_id=false`, **When** a child is minted with a blank `ParentAgentID`, **Then** the child is still reachable by the `ParentDurableKey` walk and a Stop cancels it.
5. **Given** the merged tree, **When** the three doc comments at `pkg/session/lifecycle.go:225-228`, `:572-575` and `pkg/tools/list_jobs_sources.go:311-315` are read, **Then** none of them describes `ParentDurableKey` as shared between parent and children — **and** the gate asserts all three blocks were **located** before asserting their content (binding rule 4).

---

### User Story 5 — A Stop reaches the whole subtree, live and durable (Priority: P0)

Five shipped safety mechanisms were built specifically to exploit the shared transcript id and say so in their doc comments. Without the routing key they all return "nothing to do" and every caller proceeds: the escalation ladder (`pkg/agent/steering.go:730-733`), the ADR-045 interlock (`pkg/agent/orphan_watch.go:280-287`), the pre-arm latch (`pkg/agent/cancel_prearm.go:385-389`), background-shell reaping (`pkg/agent/cancel.go:233-234`), and the `turn_canceled` audit descendant list.

**Why this priority**: An un-hard-aborted child "retries with a fresh, uncanceled context and keeps running — invisibly, for as long as its own task takes" (`pkg/agent/steering.go:730-733`).

**Independent Test**: Register a real root that finishes gracefully and a real `Critical:true` child that does not; issue a real Stop; assert PHASE B hard-abort and PHASE C detach both fire against the child (`pkg/agent/cancel.go:462`, `:487`).

**Acceptance Scenarios**:

1. **Given** a real root turn and a real live child turn, **When** a Stop is issued, **Then** PHASE A computes the live subtree once and PHASE B and PHASE C consume that set rather than re-scanning.
2. **Given** a Stop that reached a child, **When** the `turn_canceled` audit entry is read, **Then** `descendants_canceled` (`pkg/agent/cancel.go:376`) is non-empty and names the child.
2b. **Given** a chat with descendants at depths 1, 2 **and 3**, **When** a Stop is issued on the chat, **Then** `descendants_canceled` names **all three** — not merely the depth-1 child. `[grill M-12]` FR-030 says "every descendant"; v1 asserted that property only in the post-implementation evaluation set, and **that cannot serve as acceptance evidence for an FR** because the implementing agent never sees it. This scenario is deliberately part of the visible plan.
3. **Given** a live `Critical:true` async delegate and an orphaned root, **When** the ADR-045 watchdog evaluates its fire predicate, **Then** it does **not** fire, and it does fire once the delegate finishes.
4. **Given** a child that started a background `bash`, **When** a chat-level Stop is issued, **Then** the real PID is gone; **and** a sibling's background shell survives.
5. **Given** a `delegate action=cancel` on that child, **When** it executes, **Then** the child's background shells are killed (today `InterruptBySessionKey` never calls `KillBackgroundSessions` at all — the only non-test call site tree-wide is `pkg/agent/cancel.go:234`).
6. **Given** a Stop, **When** the durable walk runs, **Then** each descendant's lifecycle record transitions to `cancelled` (today `pkg/agent/cancel.go:428` transitions exactly one).

---

### User Story 6 — Approvals inherit to the child and are torn down with it (Priority: P0)

`Inherit` writes under `{sessionID, agentID}` (`pkg/security/approvalgrants.go:112-129`), written at spawn with the parent's transcript id (`pkg/agent/subturn.go:916`) and read inside the child with the child's (`pkg/agent/loop.go:8617`, `:8630-8631`). Under D1 without a decision, every inherited grant misses, the child falls through to `CheckGrantOrRequestApproval` and blocks on a human for up to 300 s per tool call — with the delegate span hidden from the thread unless verbose chat is on (`src/lib/toolVisibility.ts:218-223`). The symptom is a delegation that hangs for five minutes with no prompt and no explanation.

> `[grill C-1]` **The obvious fix is the bug.** v1's FR-031 said *"`Inherit`'s first argument MUST become the child's own session id"*. Verified: `Inherit(sessionID, parentAgentID, childAgentID)` uses **one** `sessionID` for **both** the source lookup (`grants[{sessionID, parentAgentID}]`, `:118`) and the destination write (`grants[{sessionID, childAgentID}]`, `:122`). Passing the child's id makes the **source** lookup miss — the parent's grants live under the parent's key — so `!ok` fires at `:118-120` and the function returns having done nothing. That path is a **documented** silent no-op (`:110-111`: *"No-op on … or when the parent currently holds no grants for this session"*). The child then falls through exactly as if nothing had been inherited: `IsAllowed(ts.transcriptSessionID = childID, …)` returns false at `loop.go:8617`, `CheckGrantOrRequestApproval` blocks at `:8630-8631`, and the delegation hangs for 300 s with its span hidden. **That is, verbatim, the failure this story exists to prevent.** The re-key is a two-key operation and this spec now says so (FR-031, `InheritFrom`). A test that seeds the grant under the child key to begin with passes green while production hangs — which is why AS-1 now requires the "absent before" assertion.

> `[grill M-6]` **The grant store and the pending-approval registry are different stores and v1 only re-keyed one.** `pkg/gateway/approvals.go` carries its own `SessionID` (`:85`, set at `:213`/`:232`) and matches by **exact equality** at `:419`. v1's FR-032 required `cancelAllPendingForSession` to run over the descendant set — which only makes sense if each descendant's entries carry that descendant's own id, and **no requirement said so**. Worse, `tool_approval_required` is in `SESSION_SCOPED_FRAME_TYPES` (`src/store/chat.ts:1240`), so under FR-012 its `session_id` becomes the **routing** key while the registry entry is keyed by the **child** — and v1 specified no route back. AS-5 and AS-6 close both halves.

**Why this priority**: The failure direction is safe but the availability impact is severe and invisible.

**Independent Test**: With a standing grant on the parent, run one delegation and assert the child executes the granted tool with no approval prompt and no wait — having first asserted the grant was **not** resolvable under the child's key before the spawn.

**Acceptance Scenarios**:

1. **Given** a standing grant on the parent for tool `T`, and parent/child session ids that are **distinct**, **When** a child executes `T`, **Then** no approval prompt is raised and no 300 s wait occurs — **and** a pre-spawn lookup under `{childSessionID, childAgentID}` returned absent, proving the grant arrived by inheritance rather than by fixture.
2. **Given** the grant now keyed `{childSessionID, childAgentID}`, **When** the child session terminates, **Then** the grant set is gone — the grant does not outlive the child.
3. **Given** a pending approval inside a child, **When** a chat-level Stop is issued, **Then** the registry entry is gone, its timer is stopped, and the child's goroutine unblocks.
4. **Given** a terminated child turn, **When** teardown runs, **Then** `CloseSession` has run for the child: its grant set, `loadedTools` bucket and `recallSpans` entries are gone (today no call site exists on any child/delegate path — verified: the only non-test callers are `websocket.go:1038`, `loop.go:1048`/`:1064`, `session_end.go:865`).
5. **Given** a child raising a real approval request, **When** the pending-approval registry entry is inspected, **Then** its `SessionID` is the **child's own** session id, not the chat's. `[grill M-6]`
6. **Given** that pending approval and a client that **approves** it, **When** the approve response arrives carrying the routing `session_id`, **Then** it resolves to the child's entry **by approval id** and the child's tool call proceeds — the round trip does not depend on the frame's `session_id` matching the registry's. `[grill M-6]`
7. **Given** a spawn where the parent genuinely holds no grants, **When** `InheritFrom` runs, **Then** the no-op is logged and counted rather than returning silently, so a future re-key cannot regress into C-1 again. `[grill C-1]`

---

### User Story 7 — The transcript visibility filter is deleted outright (Priority: P1)

Under D1 a child's entries are written to the child's own `transcript.jsonl`, so the content-based predicate `IsDelegateChildEntry() { return e.ParentSpawnCallID != "" }` (`pkg/session/daypartition.go:332-334`) has nothing to match for any session created after cutover. Greenfield removes the reason to carry it at four sites (five effective read boundaries — `pkg/gateway/rest.go`'s helper serves both `getSession` and `getSessionMessages`).

**Why this priority**: High value, low risk under greenfield, but strictly downstream of D1 landing; deleting the filter before D1 would un-hide narration with nothing gained.

**Independent Test**: Assert `IsDelegateChildEntry` has zero non-test references and that after one delegation the **parent's** `transcript.jsonl` contains no child entry **while the child's own `transcript.jsonl` gained exactly the expected non-zero count** — both asserted on the files, in one run.

> `[grill M-9]` **v1's flagship AC-18(b) assertion was satisfied by total child-transcript loss.** "The parent's file contains no entry produced by the child" is trivially true of a child that wrote **nothing, anywhere** — and that is the *expected* outcome of this spec's own error handling: FR-002 surfaces a transcript-write failure as a counter increment plus a WARN, not a hard failure. If the child's session mint is broken (the C-6 owner copy, a shard deadlock, a `CreateSessionWithID` bug), every child write errors, gets WARN-logged, the turn proceeds — and the test goes green. v1's positive counterpart existed (AS-3 / BDD-37 / test #57) but sat in a **different test in a different unit**, so a partial implementation passed the flagship and deferred the counterpart. AS-2 now requires both halves in the same run.

**Acceptance Scenarios**:

1. **Given** the merged tree, **When** a repo-wide reference check runs, **Then** `IsDelegateChildEntry` has zero references outside tests and none of the four read boundaries filters on `ParentSpawnCallID` — **and** the check first proves its search is live by locating the ≥ 60 non-test Go references to `ParentSpawnCallID` (binding rule 4; measured 73).
2. **Given** one delegation with distinct parent and child ids, **When** both `transcript.jsonl` files are read from disk **in the same run**, **Then** the parent's contains zero entries produced by the child **and** `<baseDir>/<childID>/transcript.jsonl` contains exactly the expected non-zero entry count with the expected content. `[grill M-9]`
3. **Given** the child's own session, **When** `inspect_session` and `GET /api/v1/sessions/{childID}` are called, **Then** both return the full transcript, unfiltered.
4. **Given** a child's own transcript entries, **When** they are read, **Then** `ParentSpawnCallID` is still stamped as provenance and is read by the drill-down surface.
5. **Given** an adjudication window, **When** the verifier renders it (`pkg/agent/verifier_adjudication.go:403`), **Then** it receives the adjudicated session's own entries and nothing else.
6. **Given** a **pre-cutover** session that ran a delegation, **When** it is rendered, **Then** previously-hidden delegate narration appears as top-level bubbles — **accepted**, bounded to pre-cutover sessions (R-16).

---

### User Story 8 — Ownership is an ancestor-chain walk, not subtree-wide equality (Priority: P1)

`callerOwnerKey` returns `ToolTranscriptSessionID(ctx)` (`pkg/tools/delegate.go:1966-1968`) and is compared for equality against `rec.ParentDurableKey` (`:1973-1979`). Because every descendant inherits the root chat's transcript id today, the gate is chat-subtree-wide: a parent can address its grandchildren, **and a child can address its siblings and cousins**.

**Why this priority**: Closing the sibling/cousin leak is a genuine security improvement, but it depends on D1 and D3 having landed.

**Independent Test**: At depth 3, assert a sibling cannot `cancel`/`steer`/`peek` another sibling, while the root chat still can reach a grandchild.

**Acceptance Scenarios**:

1. **Given** a chat with two children B and C, **When** B attempts any of the six gated actions against C, **Then** the action is rejected.
2. **Given** a chat, its child B and B's child D, **When** the chat issues a gated action against D, **Then** it is permitted (root-over-subtree preserved).
3. **Given** a delegation chain deeper than the configured max delegation depth, **When** the walk runs, **Then** it terminates at the bound and rejects rather than looping.
4. **Given** all six gated call sites (`pkg/tools/delegate.go:2010`, `:2107`, `:2159`, `:2321`, `:2459`, `:2592`), **When** each is exercised, **Then** each uses the walk — none retains equality.

---

### User Story 9 — One interrupt entry point with an explicit scope (Priority: P1)

`InterruptSession(sessionID, hint)` (`pkg/agent/steering.go:449`) and `InterruptBySessionKey(sessionKey, hint)` (`:611`) have identical Go signatures and differ only in cascade semantics. Today they are distinguishable because they take ids from different namespaces. After D1 they take the same id — recreating the confusion class this ADR eliminates, on the cancel path, in the code `#577` just fixed. The hazard is already flagged by name at `pkg/tools/delegate.go:556-561`.

**Why this priority**: Prevents the migration from regenerating the defect it exists to remove.

**Independent Test**: Assert the compiler rejects an interrupt call that does not name a scope, and that `Interrupt(childB, ScopeSubtree)` leaves parent A and sibling C running.

**Acceptance Scenarios**:

1. **Given** the collapsed API, **When** any caller invokes an interrupt, **Then** it must supply an explicit `InterruptScope` — the compiler enforces it.
2. **Given** a chat A with children B and C, and B with its own child D, **When** `Interrupt(B, ScopeSubtree)` runs, **Then** B and D are cancelled and A and C keep running.
3. **Given** the same tree, **When** `Interrupt(chat, ScopeSubtree)` runs, **Then** all three depths are reached.
4. **Given** `pkg/agent/interrupt_by_session_key_test.go:9-19,232`, **When** the change lands, **Then** the test is **deliberately inverted** to assert the new invariant — not deleted.

---

### User Story 10 — Delegate status and activity stop returning silent emptiness (Priority: P1)

`delegateStatusExtra` calls `recentActivityLines(task.SessionID, …)` (`pkg/tools/delegate.go:1823`) with a documented silent-nil path (`:1844-1851`), reading the parent's transcript and finding nothing. Separately, `executeSync` registers no `DelegateTaskState` at all (`:1507`; only `executeAsync` does, `:1280`/`:1315`), so `status`'s activity snapshot is already absent for every synchronous delegation. And nothing anywhere deletes from `t.tasks` or `t.sessionIndex` — both grow for the process lifetime.

**Why this priority**: Observability regression that would otherwise be attributed to this migration; also a genuine unbounded-growth defect.

**Independent Test**: Call `delegate action=status` after a **sync** delegation and assert a non-empty activity snapshot.

**Acceptance Scenarios**:

1. **Given** a completed synchronous delegation, **When** `delegate action=status` is called, **Then** a non-empty activity snapshot is returned.
2. **Given** a completed asynchronous delegation, **When** `delegate action=status` is called, **Then** a non-empty activity snapshot is returned.
3. **Given** a delegation whose activity genuinely is empty, **When** `recentActivityLines` returns nothing, **Then** the empty path is logged rather than returning silently.
4. **Given** a stated retention bound `C` and `N ≫ C` completed delegations whose tasks have reached a terminal state and whose last `status` read is older than the stated TTL `T`, **When** the eviction pass has run, **Then** `len(t.tasks) ≤ C` **and** `len(t.sessionIndex) ≤ C`. `[grill M-10]` v1 said only "do not retain all N", which is satisfied by deleting exactly one entry, and left "reaped" undefined so two implementers would build two different things. The trigger, the TTL and the bound are now all named (FR-045, FR-087).

---

### User Story 11 — Concurrent sessions stop serialising on one store-global lock (Priority: P1)

`UnifiedStore` has a single non-striped `sync.RWMutex` (`pkg/session/unified.go:161`). `NewSession` takes the **write** lock (`:415-416`) and holds it through `os.MkdirAll` (`:463`), `writeMetaLocked` (`:466`) and a second `WriteFileAtomic` (`:472`) — each `WriteFileAtomic` doing a file `Sync()` (`pkg/fileutil/file.go:97`) **and** a parent-directory `Sync()` (`:121`). `AppendTranscript` takes the same write lock on **every streamed line** (`:810-811`), and `ListSessions` takes it too (`:1247`). After D1 every delegation is an fsync-bound session create behind that lock.

**Why this priority**: D1 is what makes this load-bearing. Without it, a 24-way fan-out serialises 24 fsync-bound creates and stalls token streaming in every other session in the store.

**Independent Test**: N goroutines each create a session and append to their own session against a real on-disk store; assert wall-clock is close to single-session time and that doubling N does not double the time.

**Acceptance Scenarios**:

1. **Given** N concurrent writers on N distinct sessions against a real on-disk store, **When** they create and append, **Then** the slope holds: doubling N does not double wall-clock, measured against the pre-change store as the baseline it must beat.
2. **Given** an in-flight `NewSession` on session A, **When** `ListSessions` is called, **Then** it does not block on A.
3. **Given** a streaming append loop on session A, **When** session B is created, **Then** A's inter-token latency is unaffected.
4. **Given** concurrent create/append/`SetMeta`/`ListSessions`/`DeleteSession` on overlapping and disjoint ids, **When** run under `-race`, **Then** the run is clean; `ClearAll`/`RetentionSweep` interleaved with per-session writes neither deadlock nor drop a session.
5. **Given** the merged tree, **When** `cacheMu` critical sections are inspected, **Then** none contains an `os.*` or `fileutil.*` call, and lock order is only ever `sessionLock(id)` → `cacheMu` — **and** the gate first asserts it located ≥ 3 such sections (binding rule 4; `cacheMu` does not exist today, so a gate that finds zero is finding a bug, not a pass).
6. **Given** `ListSessions` running concurrently with `DeleteSession` on a session the reconcile pass just installed, **When** both complete, **Then** the result honours the **stated** consistency model — a best-effort snapshot that MAY omit a session deleted during the call and MUST NOT return a session whose directory was already gone before the call began, and MUST NOT panic, deadlock or return a partially-composed meta. `[grill M-14]` v1 split `ListSessions` into per-session reconcile plus a `cacheMu.RLock` snapshot (FR-051) and stated no consistency model anywhere — not in the FRs, not in the Behavioral Contract, not in Edge Cases. Today's whole-method `us.mu.Lock()` (`pkg/session/unified.go:1240-1248`) makes the question moot and its doc comment says exactly why; splitting it makes the question real.
7. **Given** a multi-session operation (the `CreateSessionWithID` owner copy), **When** its lock acquisitions are recorded by an instrumented lock wrapper, **Then** at no point are two session shards held simultaneously, and `ClearAll`/`RetentionSweep` acquire in **index** order. `[grill C-6]` `-race` is not a lock-order checker; it detects a lock-order inversion only if that particular run happens to deadlock.

---

### User Story 12 — `meta.json` splits into four files, one per writer family (Priority: P1)

`SessionMeta` is one document holding identity+lifecycle (`pkg/session/daypartition.go:77-104`), an embedded `SessionStats` (`:85`; 9 fields, type at `:209-223`), **9** `Goal*` fields and **9** `Loop*` fields. `writeMetaLocked` marshals the **whole** document on every mutation (`pkg/session/unified.go:786-799`). Today a `/loop` tick rewrites the goal state machine, a `/goal` judge round rewrites `LoopJobID`, and a single streamed token rewrites both.

**Why this priority**: It is what makes the D12 throttle safe (Alternative F is rejected precisely because a flusher over a fused document either clobbers or re-serialises).

**Independent Test**: After a create plus one `/goal set`, one `/loop` start and one transcript append, assert the session directory holds exactly `meta.json`, `stats.json`, `goal.json`, `loop.json`, each containing only its own group's fields.

**Acceptance Scenarios**:

1. **Given** a session exercised on all four write paths, **When** its directory is listed, **Then** four files exist and each contains only its own group's fields.
2. **Given** a `/loop` tick, **When** it completes, **Then** `goal.json`'s bytes are unchanged; symmetrically for a `/goal` round against `loop.json`; and a transcript append leaves both unchanged.
3. **Given** a session directory with `meta.json` only, **When** it is loaded, **Then** it loads successfully with zero-valued stats/goal/loop.
4. **Given** a session directory with **no** `meta.json`, **When** it is loaded, **Then** `readUnifiedMeta` returns an error and `GET /api/v1/sessions/{id}` 404s.
5. **Given** a present but truncated/corrupt `goal.json`, **When** it is loaded, **Then** an error surfaces for that group rather than silently composing a zero goal.
6. **Given** the same logical state before and after the split, **When** `UnifiedMeta` is marshalled and every REST/WS payload is rendered, **Then** the bytes are identical and `make verify-contracts` is unaffected.
7. **Given** the merged tree, **When** `writeMetaLocked`'s (`:776-785`) and `metaCache`'s (`:166-181`) doc comments are read, **Then** neither asserts a single whole-document write funnel — **and** the gate first asserts both blocks were located (binding rule 4).
8. **Given** K transcript appends with no flush, followed by a `/goal set` and a `Status` transition, **When** a flush is then forced, **Then** `stats.json` equals K's exact deltas — the goal writer and the status writer each mutated **only their own field group** in the cached `*UnifiedMeta` and did **not** replace the cache entry wholesale. `[grill C-5]` This is the missing negative case for AC-22(b). This spec rejects Alternative F because a flusher over a fused document "would clobber goal/loop/status or re-serialise everything" — true of the **file**, false of the **cache**, and W24 puts the counters in the cache (FR-061). `writeMetaLocked` today ends with `us.metaCache[sessionID] = meta.Clone()` (`pkg/session/unified.go:798`), a whole-document refresh documented at `:780` as "the single invalidation/update point for every mutation path". If any of the four new targeted writers keeps that shape — the obvious translation — a `/goal` round replaces the cache entry with a meta composed from **disk**, discarding every unflushed in-memory `Stats.*` delta. Counters silently go backwards and the only v1 test of AC-22(b) (#21) is single-goroutine, single-writer-family, and cannot see it.

---

### User Story 13 — The per-token counter write is throttled; every event-driven write stays immediate (Priority: P1)

`AppendTranscript` bumps `Stats.*` (`pkg/session/unified.go:824-846`) and `UpdatedAt` (`:847`) then rewrites the whole meta document (`:848`) **once per streamed transcript line** — a marshal, a `WithFlock`, an fsync, a rename and a directory fsync per token. The system already treats that write as expendable: it returns `nil` when the meta read fails (`:819-823`) and `nil` when the meta write fails (`:848-856`).

**Why this priority**: Directly downstream of US-12; must not land without it.

**Independent Test**: Burst appends within one flush interval against a real store; assert `stats.json`'s mtime and bytes do not change while `transcript.jsonl` grows by exactly one line per append — **then, in the same test, wait past the interval and assert `stats.json` becomes current**, so "unchanged" cannot mean "never written".

> `[grill C-4]` **The load-bearing property of W24 had zero coverage.** Trace v1's four throttle tests against a store whose periodic flusher goroutine is **never started** and where only the forced flush points work: #20 asserts `stats.json` is *unchanged* during a burst — satisfied by never writing it at all ✅; #21 drives the flush explicitly (AC-22 permits an injected fake clock), so the production wiring is never exercised ✅; #22 tests exactly the four forced paths that still work ✅; #74's dataset row 7 expected "behind by ≤ K", which a store that flushed **nothing** satisfies exactly ✅. Four green tests, dead feature. Under the real production shape — a long-lived gateway that never calls `Close` — a broken flusher means `stats.json` is stale for the entire process lifetime. AS-7 asserts the unforced path directly.

**Acceptance Scenarios**:

1. **Given** `stats.json` already on disk from a prior forced flush with known bytes and mtime, **When** a burst of K appends occurs inside one flush interval, **Then** `stats.json`'s mtime and content hash are unchanged and `transcript.jsonl` has exactly one new line per append. `[grill m-2]` The precondition is explicit: under W23 `stats.json` is written lazily, so for a fresh session whose only activity is transcript appends it may not exist when the burst starts, and "unchanged" over a non-existent path is undefined and would be implemented three different ways.
2. **Given** the flush interval has elapsed, **When** `stats.json` is read, **Then** it matches the counters implied by the appended entries **exactly** — no lost or double-counted delta.
3. **Given** each forced flush point in turn — a `SetMeta` carrying `Status`, `DeleteSession` (`:1397`), `UnifiedStore.Close` (`:1388`), and the child `CloseSession` — **When** it fires, **Then** `stats.json` is current and re-opening the store reads back the exact counters.
4. **Given** a `/goal` round, a `/loop` tick, a `Status` transition and a `Title` change, **When** each call returns, **Then** its value is on disk **immediately**, with no flush interval elapsed.
5. **Given** two sessions where B streamed most recently, **When** `ListSessions` is called with no flush in between, **Then** B sorts ahead of A.
6. **Given** the process is killed mid-interval **after a run spanning ≥ 2 flush intervals**, **When** the store is re-opened, **Then** the counters are behind by at most that interval's appends, the flushed prefix is **non-zero**, and the transcript is complete. `[grill C-4]` The two-sided bound matters: "behind by ≤ K" alone is satisfied by a store that flushed nothing.
7. **Given** a single append and **no other action whatsoever** — the store is never closed, no `SetMeta` runs, no `DeleteSession`, no `CloseSession`, no test-driven tick — **When** more than one flush interval elapses on the real (or advanced fake) clock, **Then** `stats.json` on disk is current. `[grill C-4]` This is the only assertion in the story that fails when the periodic flusher is never started, and it is therefore the only one that proves W24 exists.

---

### User Story 14 — A child session is inspectable, and the session list scales (Priority: P2)

`GET /api/v1/sessions/{childID}` 404s today (`pkg/gateway/rest.go:834-844`, no `UnifiedMeta`). The ActivityPanel is not a usable fallback: `subagent_message`/`subagent_state` have **zero Go emitters** and are absent from the `WsFrameType` enum in contracts, Go and TS. Meanwhile there is no pagination at any layer and the sidebar shows only the 9 most recent by recency — so 24 child sessions evict the parent chat.

**Why this priority**: Required to make hidden delegations inspectable at all, but not on the correctness-critical path.

**Independent Test**: Without verbose chat enabled, open `GET /api/v1/sessions/{childID}` for a hidden delegation and render it; assert the transcript is populated.

**Acceptance Scenarios**:

1. **Given** a hidden delegation and verbose chat **disabled**, **When** the drill-down surface is opened by child id, **Then** it is reachable and populated using only `GET /api/v1/sessions/{childID}`.
2. **Given** a store with more sessions than one page, **When** `GET /api/v1/sessions` is called with paging parameters, **Then** each of the four named layers honours them: `UnifiedStore.ListSessions` (`pkg/session/unified.go:1247`, U6), `AgentLoop.ListAllSessions` (`pkg/agent/loop.go:5046`, U9), `restAPI.listSessions` (`pkg/gateway/rest.go:758-812`, U18) and `fetchSessions` (`src/lib/api.ts:1379-1388`, U12). `[grill M-1]` v1 assigned W16 to U12 + U18 only, leaving the store and loop layers with no owner, so FR-068 could not be delivered as scoped. Verified: **none of the four takes a limit or offset today.**
3. **Given** a 24-way fan-out under one parent chat, **When** the sidebar renders, **Then** the parent chat is still shown.
4. **Given** the drill-down view, **When** it filters, **Then** it filters on `producing_session_id`, and a static gate asserts zero non-test references to `subagent_message`/`subagent_state` in `src/`. `[grill M-11]` v1's FR-047 ("no requirement MAY depend on…") was a property over all requirements that a single E2E test cannot establish; it is restated as something a gate can actually check.

---

### User Story 15 — Root-level delegation fan-out is admission-gated (Priority: P2)

`AdmissionController` gates inbound user-message dispatch only and says so verbatim: *"Subagent spawn and task-executor dispatch paths are NOT gated"* (`pkg/agent/admission.go:12-18`). `turnState.concurrencySem` is set **only** on a child (`pkg/agent/subturn.go:1051`), so nested delegation is gated and root-level delegation is not — matching the live "24 parallel against a cap of 16" observation in `docs/internal/uat/max-parallel-concurrency-gap-2026-07-31.md` §G1.

**Why this priority**: Required **by this ADR** — D1 turns every delegation into an fsync-bound session create, so an ungated root fan-out becomes a self-inflicted DoS. Not required for correctness of any other story.

**Independent Test**: With `agents.defaults.subturn.max_concurrent` set to N, attempt N+1 concurrent root-level delegations and assert the N+1th is refused rather than queued behind the store lock.

> `[operator 4]` **The cap reuses the existing per-agent knob; no new config key.** `[grill M-7 — corrected, and partly rebutted]` The review argued AC-10's "refuses **the 25th**" (implying a cap of 24) is unrunnable if the cap is the existing `maxConcurrent`, because the UAT recorded that as **16**. Verified (at the time), and the review's premise did not hold then: **there were two knobs, and only one was clamped.** `getSubTurnConfig` (`pkg/agent/subturn.go:64-69`) reads `agents.defaults.subturn.max_concurrent` (`pkg/config/config.go:1304`) **unclamped** whenever it is > 0, and falls back to `Performance.EffectiveMaxParallelAgents()` — at the time, a value `clampParallelExplicit` hard-capped at 16 (`pkg/config/config.go:459-468`) — only when it is ≤ 0. The 16 the UAT observed was that fallback. Setting `subturn.max_concurrent = 24` still satisfies AC-10's 24/25 topology **literally**, so AC-10 needs no amendment. `[AMENDED 2026-08-04]` Commit `536b7340` removed `clampParallelExplicit`'s ceiling — `EffectiveMaxParallelAgents()` is no longer capped at 16, so the "no second config key" framing now means something different: the field is a genuine per-delegation OVERRIDE of a central value that itself carries no ceiling, not a workaround for a clamp elsewhere. See FR-095's own `[AMENDED 2026-08-04]` entry and the top-of-file AMENDMENT note.

> `[operator 6]` **"Operator-visible" means a tool error plus `slog.Error`.** Mirroring the shape already in the tree at `pkg/tools/delegate.go:1150-1159` — an `slog.Error` naming the ids, then an `ErrorResult(...)` returned to the calling agent. **No separate user-facing notification.**

**Acceptance Scenarios**:

1. **Given** `agents.defaults.subturn.max_concurrent = N` with N in flight, **When** the N+1th concurrent root-level delegation is attempted, **Then** it is **refused**, not queued — asserted at N = 24 so AC-10's stated topology runs as written.
2. **Given** the gate is in effect, **When** a nested (child-level) delegation runs, **Then** its existing `concurrencySem` behaviour is unchanged (`pkg/agent/subturn.go:607`, `:1051`).
3. **Given** the refusal, **When** it surfaces, **Then** it is an `ErrorResult` naming the cap returned to the calling agent **and** an `slog.Error` record, matching `pkg/tools/delegate.go:1150-1159`'s shape.
4. **Given** the cap is resolved with `agents.defaults.subturn.max_concurrent` set explicitly (e.g. 24), **When** its source is inspected, **Then** it came from that field, honoured exactly as configured — never silently coerced towards the central value. `[grill M-7]` `[AMENDED 2026-08-04: originally this scenario also asserted "and not from Performance.EffectiveMaxParallelAgents(), so an operator-set 24 is honoured rather than clamped to 16" — clampParallelExplicit no longer clamps EffectiveMaxParallelAgents() at all (commit 536b7340), and the UNSET case now DOES resolve through EffectiveMaxParallelAgents() by design (see FR-095's amendment). The scenario is narrowed to the case that still holds: an EXPLICIT override is never coerced.]`

---

### User Story 16 — Child upload directories are reachable by cascade-delete (Priority: P2)

Tool-media uploads resolve their directory from `ToolTranscriptSessionID(ctx)` (`pkg/tools/normalization.go:247-248` → `pkg/media/tempdir.go:33-51`) and use `CleanupPolicyForgetOnly` (`pkg/tools/normalization.go:254`), which is immune to the TTL cleaner. Today parent and children share one directory; after D1 each child gets its own, and nothing deletes it.

**Why this priority**: A silent disk leak, not a correctness break.

**Independent Test**: Run a delegation that uploads media, delete the parent session, assert `<home>/uploads/<childID>/` is gone.

**Acceptance Scenarios**:

1. **Given** a parent session with a descendant that uploaded media, **When** the parent session is deleted, **Then** `<home>/uploads/<childID>/` is removed for **every** descendant.
2. **Given** a child id that is path-unsafe, **When** the uploads directory is resolved, **Then** the existing `("", false)` rejection (`pkg/media/tempdir.go:34-44`) still applies.

---

### User Story 17 — The suite's encoding of the old contract is inverted deliberately, in its own commit (Priority: P0)

Roughly 71 test files and ~430 references touch the shared-transcript-id value (128 `transcriptSessionID` refs across 43 test files alone). Twelve named files pin the current contract explicitly and all twelve exist today. The suite **is** the specification of the current contract; quietly deleting a gate test converts a contract change into an untracked behaviour change.

**Why this priority**: P0 because it is the difference between "the contract changed" and "the behaviour regressed" being distinguishable under bisection — and because R-4/R-5's failures are the same silent shape #576–#588 were.

**Independent Test**: `git log` shows W22's inversions as a commit containing only `*_test.go` changes, with no behaviour file in the same commit.

**Acceptance Scenarios**:

1. **Given** each of the twelve named gate tests, **When** the change lands, **Then** each asserts the **new** invariant — none is deleted and none is left asserting the old one.
2. **Given** the commit history, **When** it is inspected, **Then** W22's test inversions are a single commit containing no behaviour-file change.
3. **Given** every test written or inverted for this spec, **When** it constructs parent and child ids, **Then** they are distinct, non-equal values and the assertion names which one was used.

---

### User Story 18 — Consequential semantics that change are pinned by assertion, not assumed (Priority: P1)

Five behaviours change as a **consequence** of D1–D8 rather than as a target of them. ADR-057 names each and requires each to be asserted: `follow_up` warm resume now sees the previous generation's history (R-11); ADR-053 D15's per-child message ceiling becomes per-direct-parent, so a chat's aggregate is (children × ceiling); ADR-053 D16's inbox routing moves from the chat to the immediate parent, and producer and consumer must move together or `delegate action=inbox` returns a clean, empty success payload forever; a 3P child's own sub-delegations are outside the session graph, so the process group is the only cancellation boundary; and a deploy landing mid-delegation must not leave an orphan directory.

**Why this priority**: Each is a silent-failure candidate — an empty inbox, a ceiling that is quietly 3× wider, a surviving foreign process tree, a transcript in a directory nothing knows about. None blocks another story, but each would ship undetected.

**Independent Test**: Build a depth-3 tree and assert inbox drain, message ceiling, `follow_up` resume, 3P process-group death and restart reconciliation independently of one another.

**Acceptance Scenarios**:

1. **Given** a completed child, **When** `follow_up` resumes it, **Then** generation N's history is visible in generation N+1's first assembled message list — the intended behaviour, not a leak (R-11, AC-11).
2. **Given** a chat with C children each at the per-child message ceiling, **When** the aggregate is measured, **Then** it equals (C × ceiling), enforced per **direct parent** at depth 3 (ADR-053 D15, AC-15).
3. **Given** a depth-3 tree, **When** the grandchild calls `message_parent`, **Then** its direct parent's `delegate action=inbox` drains it and no other node's does (ADR-053 D16, AC-16).
4. **Given** an external-CLI (3P) child running its own subprocess tree, **When** the child is cancelled, **Then** its process group dies and the subtree dies with it (D3 gap 5, AC-17c).
5. **Given** a parent turn mid-delegation, **When** the process restarts, **Then** the boot sweep reconciles the child's lifecycle record and no transcript write lands in an orphan directory (AC-19).

---

### User Story 19 — Child sessions are listed nested under their parent (Priority: P2)

`[operator 1]` ADR-057 §9's one remaining open question — hidden-with-a-flag (the `verifier` precedent) or nested under the parent — is **resolved as nested**. Children appear as an expandable tree under their parent, not as a hidden class behind `?include_subordinate=true`.

**This is real UI and API work and is scoped as such, not as a filter flag.** The `verifier` precedent it is deliberately *not* following was cheap because hiding a session needs one `continue` in a loop (`pkg/gateway/rest.go:783-785`). Nesting needs hierarchy at every layer that currently has none. Verified 2026-08-03:

- **There is no pagination anywhere.** `UnifiedStore.ListSessions` (`pkg/session/unified.go:1247`), `AgentLoop.ListAllSessions` (`pkg/agent/loop.go:5046`), `restAPI.listSessions` (`pkg/gateway/rest.go:758-812`, which reads only `agent_id`, `type`, `include_verifier`) and `fetchSessions` (`src/lib/api.ts:1379-1388`) all return or request **everything**. A tree cannot be paginated by slicing a flat list — a page boundary that lands between a parent and its children renders orphans — so pagination and hierarchy must be designed together.
- **The sidebar is a hard-truncated recency list.** `src/components/layout/Sidebar.tsx:456-457`: `const maxVisible = 9` then `.slice(0, maxVisible)`. A 24-way fan-out evicts the parent chat with no hierarchy to fall back on.
- **`SearchModal` renders the full list unvirtualized.** It fetches every session (`src/components/search/SearchModal.tsx:363`) and renders `groups.map(...)` (`:687`) with no windowing. Under D1 the session count becomes (chats + every delegated child, at every depth) — the same list, an order of magnitude longer.

**Design that makes hierarchy and pagination coherent**: the list endpoint paginates over **root** sessions (`ParentSessionID == ""`) and returns a `child_count` per row; children are fetched on expand via the same endpoint filtered by `parent_session_id`, itself paginated. The client assembles the tree; no layer ever has to hold the whole forest.

> `[grill2 C2-2]` **Where the cost is actually bounded — and where it deliberately is not.** v2 required all four layers to "bound their own cost rather than loading the full set and slicing", which is **not implementable** at the two backend layers: `ListSessions` ranges an unordered `metaCache` map and sorts (`pkg/session/unified.go:1283-1291`), and `ListAllSessions` k-way-merges and de-duplicates N stores before sorting (`pkg/agent/loop.go:5046-5090`). Neither can produce a recency-ordered page without materialising everything, and no v2 FR created an index. **This spec does not add one**, because there is nothing to gain: `metaCache` already holds every session's composed meta — FR-058 asserts a cache hit costs **zero** disk reads — so the sort runs over resident data and touches no disk. The bounded-cost obligation therefore sits where unbounded cost is genuinely paid: the **REST payload** (serialise ≤ `limit` rows) and the **SPA render** (virtualized, viewport-bounded). At the store and loop layers the obligation is *correctness and stability* of the window, plus zero per-session disk reads on a warm cache. Two mechanisms US-19 needed and v2 never required are added: **FR-097** (an in-memory parent index, so `child_count` and orphan detection are O(1) per row rather than O(all sessions) per page) and **FR-098** (cross-store ordering, cursor stability and mid-page store-error behaviour). See FR-092.

**Why this priority**: P2 — it is the difference between "delegations are inspectable" and "the session list is unusable at 24-way fan-out", but no correctness-critical story depends on it. It is called out separately from US-14 because it is a distinct body of work with its own owner (U24) and its own contract change.

**Independent Test**: Create one parent chat with 24 children, then list; assert the first page contains the parent with `child_count == 24` and **zero** children inline, and that expanding it returns the 24 in pages.

**Acceptance Scenarios**:

1. **Given** a parent chat with 24 child sessions, **When** `GET /api/v1/sessions` is called with default paging, **Then** the response contains the parent with `child_count == 24` and contains **no** subordinate session as a top-level row.
2. **Given** that parent, **When** `GET /api/v1/sessions?parent_session_id=<parentID>` is called with paging parameters, **Then** exactly its **direct** children are returned, a page at a time, ordered by recency — not its grandchildren.
3. **Given** a depth-3 tree, **When** each level is expanded in turn, **Then** each expansion returns only that node's direct children, and the total number of requests is O(expanded nodes), not O(all sessions).
4. **Given** the sidebar with 24 children under one parent, **When** it renders, **Then** the parent chat is present, its children are **collapsed** by default behind an expand affordance, and the `maxVisible` budget is spent on **root** sessions so a fan-out cannot evict a chat.
5. **Given** `SearchModal` open against a store with more sessions than fit one viewport, **When** results render, **Then** matching children appear nested under their parent (with the parent shown for context even when only the child matched) and the list is **virtualized**.
6. **Given** a child whose parent has been deleted, **When** the list renders, **Then** the orphan is shown as a root-level row rather than silently omitted — a session that exists and is not reachable in the tree is the R-7 shape again.

---

## Behavioral Contract

**Primary flows**

- When a delegation is dispatched, the system creates a store-backed session whose id is exactly the child id, whose `meta.Owner` is the parent's, and whose `ParentSessionID` names the direct parent.
- When a child turn writes a transcript entry, the entry lands in the child's own `transcript.jsonl` and never in the parent's file.
- When any session-scoped WS frame leaves the gateway, its `session_id` is the routing key inherited from the root of the chat tree, and `producing_session_id` is present iff it differs.
- When a Stop is issued on a chat, the live subtree is computed once from the routing key and hard-abort, detach, background-shell kill, pending-approval cancel and lifecycle transition all apply to that whole set.
- When a `delegate action=cancel` targets child B, exactly B and B's own descendants are cancelled — never the parent, never a sibling.
- When a gated delegate action (`inbox`, `steer`, `respond`, `cancel`, `follow_up`, `peek`) is invoked, it is permitted iff the caller is an **ancestor** of the target within the configured depth bound.
- When a transcript is read at any of the four read boundaries, it is returned unfiltered.
- When a session writes any of identity, statistics, goal state or loop state, it writes exactly one of the four files and leaves the other three byte-unchanged, **and mutates only its own field group in the cached `*UnifiedMeta`** — never replacing the cache entry wholesale.
- When a transcript line is appended, the transcript write is immediate and the counter update is in memory only.
- When more than one flush interval elapses with a session's counters dirty and **no** external trigger of any kind, the periodic flusher writes that session's `stats.json`.
- When the session list is requested, roots are returned a page at a time with a `child_count`; a node's direct children are returned only when explicitly requested by `parent_session_id`, also a page at a time.
- When a delegated child inherits approval grants, the grants are **read** under `{parentRoutingOrSessionID, parentAgentID}` and **written** under `{childSessionID, childAgentID}` — two distinct keys in one operation.
- When a child raises an approval, the pending-registry entry carries the **child's own** session id, and the approve/deny response resolves by **approval id**.

**Error flows**

- When a transcript append targets a session id with no `meta.json`, the call returns a non-nil error and creates no directory; the caller surfaces it as a counter increment and a WARN.
- When a delegation is attempted with no lifecycle store wired, the delegation is refused with an operator-visible error.
- When the ancestor walk exceeds the configured max delegation depth, the action is rejected.
- When `meta.json` is absent, the session load returns an error and the REST surface 404s.
- When `goal.json` / `loop.json` / `stats.json` is present but corrupt, the load surfaces an error for **that group**.
- When the root-level delegation admission cap is reached, the next root-level delegation is refused with a tool error returned to the calling agent **and** an `slog.Error` record — the shape at `pkg/tools/delegate.go:1150-1159`. No separate user-facing notification `[operator 6]`.
- When a grant inheritance finds no grants under the **source** key, the no-op is logged and counted rather than returning silently.
- When `ListSessions` runs concurrently with `DeleteSession`, the result MAY omit a session deleted during the call and MUST NOT include one whose directory was already gone when the call began — **the latter because the reconcile pass prunes cache entries for vanished directories (FR-097a), not because the cache happens to be right**; it never panics, deadlocks or returns a partially-composed meta. `[grill2 M2-7]`
- When the transcript **mutate** path cannot find the session or the target entry, it emits a counter increment and a WARN rather than returning a bare `false` (FR-099).
- When a legacy per-agent store errors during a paged `ListAllSessions` merge, the page still returns its rows with `partial_errors` populated and a valid `next_cursor` (FR-098).

**Boundary conditions**

- When a turn is a **root** turn, `routingSessionID` equals its own session id and every downstream behaviour is byte-identical to today.
- When a delegation is a **self-delegation**, the child is still a distinct session with a distinct id; the grant `Inherit` is a same-agent, different-session union.
- When `goal.json` / `loop.json` / `stats.json` are **absent**, they compose as the zero value and the load succeeds.
- When a session's last activity was a stream that never reached a flush point, its `UpdatedAt` on reload is stale by at most one flush interval; within a live process, ordering is exact.
- When `require_parent_agent_id=false` blanks `ParentAgentID`, `ParentDurableKey` is still stamped and the walk still reaches the child.

---

## Edge Cases

- **A child spawns while the parent's Stop is already in flight.** Expected: the pre-arm latch, keyed on the parent identity the child inherits verbatim as `routingSessionID`, is consumed by the child — not expired (`pkg/agent/cancel_prearm.go:338`, `:355`, `:385-389`; markers at `pkg/agent/subturn.go:585`, `:1147`).
- **A process restart lands mid-delegation.** Expected: the boot sweep reconciles the child's lifecycle record and no transcript write lands in an orphan directory (AC-19).
- **`follow_up` warm resume reuses `childID` verbatim** for the next generation (`pkg/agent/subturn.go:1115-1135`). With `NoHistory: false` and a real session behind that id, generation N+1 loads generation N's history. Expected: **that is intended** — a corrective follow-up should see what it is correcting (R-11).
- **An external-CLI (3P) child's own sub-delegations never reach the lifecycle mint.** Expected: out of the session graph by construction; the boundary is the 3P child's own process-group kill, and the subtree dies with it.
- **A grandchild's `message_parent` output.** Expected: `ParentDurableKey` now names the **immediate** parent, so it routes to the direct parent's inbox — producer (`pkg/tools/message_parent.go:640`) and consumer (`pkg/tools/delegate.go:2024`, `:2200`) must move together or `delegate action=inbox` returns a clean, empty success payload forever (AC-16).
- **The ADR-053 D15 per-child message ceiling.** Expected: it becomes per-direct-parent instead of per-chat-subtree, so a chat's aggregate is (children × ceiling) rather than one shared pool — **asserted, not assumed** (AC-15).
- **A channel `/stop` arrives when only a surviving child is alive.** Expected: `resolveSessionIDByChannelChat` (`pkg/agent/turn.go:557-583`) returns the **routing** id, so the Stop cancels the tree — not just the child.
- **Two sessions collide on an FNV-32a hash mod 64.** Expected: they contend on one shard; correctness is unaffected and throughput is bounded by the filesystem and the admission gate, never by the shard count.
- **`ClearAll` / `RetentionSweep` run while per-session writes are in flight.** Expected: every shard is taken **in index order** (never hash order); no deadlock, no dropped session.
- **A `SetMeta` carrying `Status` lands between a counter bump and a flush.** Expected: structurally unrepresentable as a clobber **at the file layer** — the flusher owns `stats.json`, which no other writer touches. **At the cache layer it is entirely representable and MUST be prevented by construction** (FR-084): the `Status` writer mutates only `meta.json`'s field group inside the cached `*UnifiedMeta` and MUST NOT replace the entry, and a `readMetaLocked` cache-miss compose MUST NOT overwrite an entry marked dirty. `[grill C-5]` v1 asserted the file-layer property and stopped there, while W24 moved the counters into the cache — one layer up from where the guarantee was proved.
- **A child's `stats.json` deltas when the child is `DeleteSession`d mid-flush.** Expected: **flush-then-delete**, per the single ordered sequence stated in FR-064 — acquire shard → flush dirty stats → remove directory → evict `metaCache` → drop dirty-set entry → release. A concurrent flusher tick cannot recreate a `stats.json` in a deleted directory: before step 3 it flushes, after step 3 it finds the session gone and drops its dirty entry without writing. `[grill2 M2-11]` v2 resolved the flusher's half here and left the `metaCache`-eviction ordering FR-033 also requires unstated, while FR-086 described the same window from the reader's side — three requirements over one operation with no agreed order, tested by two different units (#22, #92) with no shared dataset.
- **A session directory removed out of band** — by `RetentionSweep`, an operator `rm`, or a crashed deploy — while its `metaCache` entry survives. Expected: the next `ListSessions` reconcile pass **prunes** it (FR-097a) and it stops being listed. Verified: today the reconcile pass only **adds** entries (`pkg/session/unified.go:1251-1280`) and never removes one, so such a session is returned forever. The codebase already names this failure in `ClearAll`'s own prune loop (`:1474-1487`): *"leaving ListSessions to resurrect sessions that are gone from disk."* `[grill2 M2-7]`
- **A page boundary falls between a parent and its children.** Expected: unrepresentable — the list endpoint paginates over **roots only**, and children are a separate, separately-paginated request keyed by `parent_session_id` (US-19). Slicing a flat mixed list is the design this explicitly rejects.
- **A child whose parent session was deleted.** Expected: rendered as a **root-level row**, not omitted. A session that exists on disk and is unreachable in the UI is R-7's shape with a different surface.
- **A pre-cutover session that ran a delegation is rendered.** Expected: previously-hidden narration appears; accepted and bounded (R-16). Tool-call and error entries were never filtered anyway — only three writers stamp `ParentSpawnCallID` (`pkg/agent/turn.go:1204`, `:1268`, `pkg/gateway/websocket.go:4254`), while `appendToolCallTranscript` (`pkg/agent/turn.go:1123-1129`) and `appendErrorTranscript` (`:1314-1324`) do not.
- **`HydrateAgentHistoryFromTranscript` on reload.** Expected: the parent agent's LLM context **stops** absorbing delegate narration (`pkg/agent/attach_hydrate.go:34-42`, zero filter references, run at `pkg/gateway/websocket.go:2577` and `pkg/agent/loop.go:6204`). This is a behaviour change to the parent's own context and reviewers must see it coming.
- **A child hands off.** Expected: impossible — `hand_off` is structurally excluded from a child registry (`pkg/agent/subturn.go:988` → `registry.go:667-669`), so `sessionActiveAgent` correctly returns `""` and the delegate target is stamped.
- **A child's `recallSpans` cleanup key.** Expected: `forgetSession` matches via `key == sessionID || strings.HasSuffix(key, ":session:"+sessionID)` (`pkg/agent/loop.go:11497-11500`); a child's `sessionKey` is a bare UUID, so the first arm matches — provided something actually calls `CloseSession`.

---

## Explicit Non-Behaviors

- The system MUST NOT use `routingSessionID` as a session-store key, a transcript write target, an ownership predicate, a steering-queue scope, an approval-grant key, an uploads-directory key, a tool-manifest bucket, a lifecycle-record field, or an audit `session_id`. This exclusion list is enforced by test (AC-2).
- The system MUST NOT infer parentage from `OwnerScopeID` — it is `""` for every direct child of a chat turn, and a task dispatch puts a **plan id** in it (`pkg/agent/task_executor.go:202-208`, `:224-233`), so a walk over it would mistake a plan id for a session id.
- The system MUST NOT infer parentage from `ParentAgentID` — it is an agent config id, so two chats where the same agent delegates are indistinguishable.
- The system MUST NOT throttle any event-driven `SetMeta` path (goal, loop, status, title, owner, workspace). They are control flow, not display: a judge round reads back `GoalRoundsUsed`/`GoalMaxRounds` to decide whether to continue, `/loop stop` needs `LoopJobID` to find the cron job, and `boot_sweep.go:321` transitions `Status` for crash recovery. Throttling any of them reintroduces the ADR-037 anti-pattern this project bans — a control that reports success and changes nothing.
- The system MUST NOT change plan cancellation. `StopPlan` (`pkg/agent/plan_engine.go:2044-2135`) already builds an explicit `[]string` under `planDecisionMu` and calls `RequestCancelForSession` once per id (`:2330-2385`). No change.
- The system MUST NOT unify `turnState.concurrencySem`, `TaskExecutor.dispatchSema` and `TaskExecutor.maxConcurrent`. That cut is ratified; the single exception is W17's root-level gate. D12's *write-cadence* throttle shares a word with this and nothing else.
- The system MUST NOT mint the child's `UnifiedMeta` lazily on first drill-down. Between spawn and first drill-down the child would write into a directory with no meta — invisible to `ListSessions`, to replay and to `GET /api/v1/sessions/{id}` — while every write returns `nil`. That is R-7 reborn and it makes AC-1 unassertable.
- The system MUST NOT keep the counters in the fused `meta.json` while throttling them (Alternative F). The flusher would clobber goal/loop/status or re-serialise everything under a lock shared with all 31 event-path call sites.
- The system MUST NOT change `UnifiedMeta`'s in-memory shape or its marshalled JSON. **No `contracts/` change and no regeneration are required by D11/W23.** (**W5, W2 and W16 *do* require the Constraint #8 pipeline** — they are different work items, and AC-21(e)'s "byte-identical, `verify-contracts` unaffected" is scoped to the **split**, not to those three. A reviewer seeing a `contracts/` diff in this change set should check it belongs to W5/W2/W16 and not to W23.)
- The system MUST NOT re-add a transcript visibility filter anywhere, including in frontend code. AC-18(b) asserts the property on the **file**, so a re-added filter cannot satisfy it.
- The system MUST NOT rely on `subagent_message` / `subagent_state` frames. They have zero Go emitters, are absent from the `WsFrameType` enum in contracts, Go and TS, and their structs are dead declarations (`pkg/api/generated/asyncapi_types.gen.go:496`, `:521`).
- The system MUST NOT touch `migrateLegacy` / `writeUnifiedMetaDirect` (`pkg/session/unified.go:1515`) — they handle a *different* legacy (PartitionStore → UnifiedStore) and are out of scope.
- The system MUST NOT provide a reader for a pre-split fused `meta.json`. Greenfield: a fresh install writes four files from `createSessionLocked` onward and never encounters the old shape.

---

## Integration Boundaries

### Gateway ↔ SPA (WebSocket frames)

- **Data in**: session-scoped WS frames emitted by `pkg/agent` payload types.
- **Data out**: `session_id` (routing key, always present on session-scoped frames) and a new **optional** `producing_session_id`, present iff it differs from `session_id`.
- **Contract**: `contracts/asyncapi.yaml` + `contracts/components/schemas/`. Constraint #8's 5-step pipeline is mandatory: schema → reference → `scripts/gen-contracts.sh` → commit the generated diff in the **same** commit → write the consumer against the generated type only. Hand-written wire types are lint-caught.
- **On failure**: the SPA edge validates every incoming payload against the generated zod schema; a failure drops the frame, increments a counter, and shows a dev-mode toast. No production crash.
- **Development**: real gateway. AC-3 explicitly forbids satisfying this boundary with a mocked socket — the assertion is on the SPA store's bucket membership on a live connection.
- **Known pre-existing strain (not caused here, must not be attributed here)**: `RateLimitPayload` has no `SessionID` field at all (`pkg/agent/events.go:525-533`) and its `session_id` is reconstructed from the connection's chat→session map (`pkg/gateway/websocket.go:3461` → `sessionIDForChat`, `:3022`), so a reconstructed `""` is dropped in production; and `'replay_done'` is in `SESSION_SCOPED_FRAME_TYPES` but absent from the `WsFrameType` enum on both sides.

### Gateway ↔ SPA (REST)

- **Data in**: `GET /api/v1/sessions` (list, now paginated), `GET /api/v1/sessions/{id}` (detail — must resolve for a child id).
- **Data out**: session list/detail wire shape (`pkg/gateway/rest.go:608-665`) plus the new subordinate session type and `ParentSessionID`.
- **Contract**: `contracts/openapi.yaml`. The `verifier` session type is the working precedent: it required a store enum, an OpenAPI enum and an SPA change (`pkg/gateway/rest.go:783-785` + `?include_verifier=true`).
- **The response is already a discriminated `oneOf`, and reshaping it is constrained by ADR-034** `[grill2 M2-10]`: `listSessions` (`pkg/gateway/rest.go:795-811`) returns **either** a bare `[]gen.Session` **or** `gen.ListSessions200JSONResponseBody1{Sessions, PartialErrors}`, with generated union accessors at `pkg/api/generated/openapi_types.gen.go:14816-14862`. This project's CLAUDE.md records a hard, precedent-backed rule for exactly this shape: `oneOf` + discriminator wrappers **must be hosted inline in `openapi.yaml`** over internal refs, because oapi-codegen inlines external file refs inside a `oneOf` as anonymous structs and emits non-compiling `As*` accessors (precedent `AgentCreateRequest`). **FR-091 resolves it by collapsing the union to one named `SessionPage` schema**, hosted inline, carrying `partial_errors` as an optional field — removing the discriminator rather than deepening it. U10 owns the change; U18 owns the handler.
- **On failure**: `GET /api/v1/sessions/{id}` 404s when no `UnifiedMeta` resolves (`pkg/gateway/rest.go:834-844` ← `pkg/agent/loop.go:5012-5039`). Under D11 that 404 is the **required** behaviour for a directory with no `meta.json` (AC-21c).
- **Development**: real gateway binary, not the Vite dev server.

### Agent loop ↔ session store

- **Data in**: exact session id + transcript entry / meta patch.
- **Data out**: `error` (now non-nil for an unknown session on the strict path).
- **Contract**: `AppendTranscriptStrict` returns a non-nil error and creates no directory for an unresolvable id. `CreateSessionWithID` creates with the exact supplied id and copies the parent's `Owner`.
- **On failure**: caller surfaces a counter increment plus a WARN naming the session id. `ts.abandoned` suppression is counted and logged, not silent.
- **Development**: real `UnifiedStore` on `t.TempDir()`. Fakes are disallowed for every AC.

### Agent loop ↔ lifecycle store

- **Data in**: `LifecycleRecord` with `ParentDurableKey` stamped unconditionally (`pkg/tools/delegate.go:1106`, `:1173`).
- **Data out**: `List(LifecycleFilter{ParentDurableKey: X})` → the direct children of X, via a secondary parent index.
- **Contract**: the index is maintained **inside `Persist`**, under the existing 64-shard striped lock (`pkg/session/lifecycle_lock.go:19-31`; precedent `pkg/session/message_inbox.go:135-139`).
- **On failure**: **fail closed.** No lifecycle store wired → delegation refused (mirroring the existing fail-closed posture at `pkg/tools/delegate.go:1150-1157`). Never a silent skip.
- **Development**: real on-disk lifecycle store.

### Agent loop ↔ background process manager

- **Data in**: `ProcessSession.OwnerSessionID`, stamped from the owning session (`pkg/tools/shell.go:571-572` → `:1035`).
- **Data out**: `KillAllForSession` matches on it (`pkg/tools/session.go:455`).
- **Contract**: the stamp becomes the child's **own** id; kill cascades over the descendant set; `delegate action=cancel` kills that child's shells.
- **On failure**: a 3P child's process **group** must die with the child — asserted, because its own sub-delegations are outside the Omnipus tool surface.
- **Development**: real processes with real PIDs; assert the PID is gone.

### External CLI (3P) subagents

- **Data in**: task prompt; the child runs inside a foreign CLI's process tree.
- **Data out**: transcript entries via `pkg/agent/external_dispatch.go:463`, `:550-555`, `:562-564` — **which are calls to U3's `turn.go` writers** (`childTS.appendIntermediateAssistantTranscript`, `childTS.appendToolCallTranscript`), not `AppendTranscript` call sites. `[grill2 M2-3]` Verified: `external_dispatch.go` contains **zero** `AppendTranscript` calls; it inherits strictness from U3's conversion with no edit of its own.
- **Contract**: out of the lifecycle session graph by construction. The cancellation boundary is the process group.
- **Implementation sites (FR-029)** `[grill2 M2-2]`: `pkg/agent/runner/driver_claude.go:147`, `driver_codex.go:121`, `driver_opencode.go:87` — each `exec.CommandContext(runCtx, binary, args...)` + `cmd.Start()`, with **no** `SysProcAttr` anywhere in the package today (`rg 'SysProcAttr|Setpgid|cmd\.Process\.Kill|Signal\(' pkg/agent/runner/` → zero matches, verified). Pattern to copy: `pkg/sandbox/hardened_exec_linux.go:39-41` (sets `Setpgid`) and `pkg/sandbox/hardened_exec_cancel_unix.go` (group kill). Owner **U22**; POSIX-only, with a no-op Windows path.
- **On failure**: if the process group survives, the subtree survives — AC-17(c) asserts it does not.
- **Development**: real subprocess, real PIDs, POSIX only.

---

## Work Unit Decomposition & File Ownership

> **This table is a safety mechanism, not bureaucracy.** This repository is a **shared working tree** and this session has already observed concurrent agents silently reverting each other's edits. A unit that writes a file it does not own can destroy another unit's work with no error and no conflict marker.

> **v2: this table was re-derived from scratch.** `[grill M-1…M-5, M-13]` v1's table was not exhaustive (five files that FRs require changing had **no owner**, and two of FR-068's four pagination layers had no unit), not acyclic across waves (U20 sat two waves before the unit that installs the hook it depends on), and not consistent with its own TDD plan (U21 exclusively owned twelve `*_test.go` files that eight other units' tests naturally belong in, while those units were told to write tests **first** and U21 to land **last**). Three units are new — **U22** (turn-adjacent transcript writers), **U23** (`pkg/agent/events.go` payloads), **U24** (SPA session hierarchy, operator decision 1) — and every dependency below is declared.

**Rules**

1. **A file has exactly one owner.** Where a file appears against a *chain* (`U4→U5→U6`), the chain is the owner and its members **must never run concurrently**.
2. **A unit that needs a change in a file it does not own must request it from the owner** — it must not make the edit itself.
3. **`git add` only the files your unit owns.** Run `git status --short` first, every time.
4. **Generated artefacts** (`pkg/api/generated/`, `src/lib/api/generated/`) are owned solely by **U10**. No other unit regenerates or edits them.
5. **Every unit's new tests go in NEW files named `<subject>_adr057_test.go`.** `[grill M-4]` U21 touches **only** the twelve enumerated `*_test.go` files and nothing else — its scope is inversion of the existing gate tests, not authorship of new ones. No unit may add a test to a U21-owned file, and U21 may not add a test to any other file. Without this rule the "write tests before the implementation" instruction and "U21 lands last in its own commit" are mutually exclusive for every test that naturally belongs in `subturn_test.go`, `steering_test.go`, `cancel_subagent_cascade_test.go`, `interrupt_by_session_key_test.go` or `approval_grant_delegation_test.go` — which is eight units' worth.
6. **Every new file added to an existing Go package MUST prefix its unexported package-level helpers with its unit id.** `[grill m-4]` U2 creates `pkg/session/unified_api.go` while U5 rewrites `pkg/session/unified.go` — same package, same wave. A package-level `sessionDir` or `metaPath` introduced independently by both is a compile break that neither unit owns and neither will see until integration. Use `u2SessionDir`, `u6FlushTick`, etc. Exported API is unaffected.
7. **A frozen contract is a promise, not a suggestion.** Where the table below says a signature is frozen, its owner MUST NOT change it inside this change set. Changing it is a cross-unit request, not a refactor.

### Ownership table

| Unit | Work items | Files owned (exclusive write) | Depends on | Must NOT touch |
|---|---|---|---|---|
| **U1** Named ID types | W20 | NEW `pkg/session/ids.go` | — | any existing file |
| **U2** Strict store API | W3 (store half), W1 (store half) | NEW `pkg/session/unified_api.go` (`AppendTranscriptStrict`, `CreateSessionWithID`) | U1, U4 · **frozen contract from U5** | `pkg/session/unified.go` — request lock-helper changes from U4 |
| **U3** Turn-local identity | W3 (4 writers), W4 (`turnState.routingSessionID` + 3 resolvers) | `pkg/agent/turn.go` | U1, U2 | `pkg/agent/subturn.go`, `pkg/agent/steering.go`, `pkg/agent/loop.go`, `pkg/agent/events.go` |
| **U4** Store striping | W15, **W15b (`RetentionSweep` shard-order conversion, FR-050/SC-038)**, **FR-097 parent-index surface**, **FR-101 lock seam** | `pkg/session/unified.go` (chain 1/3: lock + cache surface), **`pkg/session/retention_sweep.go`** (chain 1/3), NEW `pkg/session/unified_lock.go` | U1 | `pkg/session/daypartition.go` |
| **U5** meta split + parent fields + predicate deletion | W23, W2 (store half), W11a (delete `IsDelegateChildEntry`), **W3a (`AppendTranscript` becomes strict, FR-002)**, **FR-103 read seam** | `pkg/session/unified.go` (chain 2/3), `pkg/session/retention_sweep.go` (chain 2/3), `pkg/session/daypartition.go`, NEW `pkg/session/unified_meta_files.go` | **U4** | anything in `pkg/agent`, `pkg/gateway`, `pkg/tools`. **MUST NOT change `readMetaLocked`'s signature** — frozen for U2 |
| **U6** Stats throttle + store pagination | W24, **W16a (store layer, FR-092/FR-097 consumer)**, **FR-097a prune**, **FR-102 barrier** | `pkg/session/unified.go` (chain 3/3), `pkg/session/retention_sweep.go` (chain 3/3), NEW `pkg/session/unified_stats_flush.go` | **U5**, **U28** | `pkg/session/daypartition.go` (U5 is done with it; do not re-edit), `pkg/config/**` — **U28 owns the keys; U6 only reads them** |
| **U7** Delegation spawn | W1 (agent half), W4 (subturn half), W10a (`InheritFrom` **call site**), W10d (**child-terminal `CloseSession` call site**), W21c (payload assignment), FR-100 doc-rot in `subturn.go` | `pkg/agent/subturn.go` | U2, U3, U5, **U17a**, **U17b** | `pkg/agent/turn.go`, `pkg/agent/loop.go`, `pkg/agent/events.go`, `pkg/security/approvalgrants.go`, `pkg/agent/session_end.go` |
| **U8** Steering + interrupt | W4 (role-B predicates), W13 (scope collapse), **W13b (`pkg/commands` interface + stub)** | `pkg/agent/steering.go`, **`pkg/commands/runtime.go`**, **`pkg/commands/cmd_cancel_test.go`** | U3 | `pkg/agent/cancel.go`, `pkg/agent/subturn.go` |
| **U9** Loop payload stamping + loop pagination | W4 (WS payload stamping), W10b (grant read re-key), **W16b (loop layer, FR-098)**, FR-100 doc-rot in `loop.go` | `pkg/agent/loop.go` | U3, U6, U10, U17a, U23 | `pkg/agent/turn.go`, `pkg/agent/subturn.go`, `pkg/agent/events.go` |
| **U10** Contracts + regeneration | W5a, **W2b (OpenAPI enum)**, **W16e (pagination + `parent_session_id` filter + `child_count`)** | `contracts/**`, `pkg/api/generated/**`, `src/lib/api/generated/**` | — | any hand-written Go/TS consumer |
| **U11** Gateway WS | W3 (streamed write, `pkg/gateway/websocket.go:4256`), W5b (frame stamping), W10c (WS-side teardown) | `pkg/gateway/websocket.go` | U2, U10, U17, U23 | `pkg/gateway/rest.go`, `pkg/gateway/replay.go`, `pkg/gateway/approvals.go` |
| **U12** SPA store + API client | W5c, W19b (drill-down), W2c (SPA enum), **W16d (client paging + tree assembly)**, **W16h (`flat=true` usage accounting, FR-104)** | `src/store/chat.ts`, `src/lib/api.ts`, `src/routes/_app/sessions.$sessionId.tsx`, **`src/components/screens/UsageScreen.tsx`**, **`src/components/screens/UsageScreen.test.tsx`**, **`src/lib/__adr052__sessionVisibilityParams.test.ts`** | U10 | `src/lib/api/generated/**`, `src/components/layout/Sidebar.tsx`, `src/components/search/SearchModal.tsx` and **their** tests — **U24 owns those** |
| **U13** Lifecycle edge + index | W6 | `pkg/session/lifecycle.go`, NEW `pkg/session/lifecycle_index.go` | — | `pkg/session/lifecycle_lock.go` (read-only precedent), `pkg/session/unified.go` |
| **U14** Delegate tool + inbox producer | W7a (refuse), W9a (cancel kills shells), W12 (ancestor walk), W14 (status/leak/eviction), W21b (`DelegateTaskState.SessionID`), W6 doc-rot in `list_jobs_sources.go`, **W12b (`message_parent.go:640` producer)**, **W13c (consume U8's collapsed signature at `delegate.go:363-364`, `:572-578`)**, FR-100 doc-rot in `delegate.go` | `pkg/tools/delegate.go`, `pkg/tools/list_jobs_sources.go`, **`pkg/tools/message_parent.go`** | U13, **U8** | `pkg/tools/shell.go`, `pkg/tools/session.go`, `pkg/tools/inspect_session.go`, `pkg/tools/handoff.go` — **U22 owns it** |
| **U15** Cancel orchestration | W8, W4 (pre-arm keys) | `pkg/agent/cancel.go`, `pkg/agent/cancel_prearm.go`, `pkg/agent/orphan_watch.go` | U8, U13, U16 | `pkg/agent/steering.go` |
| **U16** Background shells | W9b | `pkg/tools/shell.go`, `pkg/tools/session.go`, **`pkg/tools/shell_process_unix.go`** | — | `pkg/tools/delegate.go`, `pkg/agent/runner/**` — **U22 owns the 3P drivers** |
| **U17a** Approvals | W10 (grant store **signature**, pending registry re-key, tool manifest), FR-100 doc-rot in `approvals.go` | `pkg/security/approvalgrants.go`, `pkg/gateway/approvals.go`, `pkg/agent/tool_manifest.go` | — | `pkg/agent/loop.go`, `pkg/agent/subturn.go`, **`pkg/agent/session_end.go` — U17b owns it** |
| **U17b** Session teardown | **W10e** (`CloseSession` **entry point** + the store-flush and `metaCache`-eviction FR-033/FR-064 require at teardown) | **`pkg/agent/session_end.go`** | **U6**, U17a | `pkg/agent/subturn.go` — **the child-terminal call site is U7's line item W10d** |
| **U18** Read boundaries + REST | W11b (4 filter sites), **W16c (REST layer + nested listing)**, W19a (drill-down endpoint), **W18b (uploads cascade wiring)** | `pkg/gateway/replay.go`, `pkg/gateway/rest.go`, `pkg/agent/verifier_adjudication.go`, `pkg/tools/inspect_session.go` | U5, U9, U10, U13, U20 | `pkg/session/daypartition.go` — **the predicate deletion is U5's line item W11a** |
| **U19** Admission + wiring + boot sweep | W17 (**reads U28's seeded cap, FR-095**), W7b (fail-closed wiring), **W6b (boot-sweep reconcile, FR-078)**, **W13d (re-wire `SetCancelHooks` at `session_messaging_wire.go:166` onto U8's collapsed signature)**, FR-100 doc-rot in `session_messaging_wire.go` | `pkg/agent/admission.go`, `pkg/agent/session_messaging_wire.go`, **`pkg/agent/boot_sweep.go`** | U13, U14, **U8**, **U28** | `pkg/agent/subturn.go`, `pkg/config/**` — **U28 owns the seed** |
| **U20** Uploads primitive | **W18a** (primitive only) | `pkg/tools/normalization.go`, `pkg/media/tempdir.go`, `pkg/media/store.go` | — | `pkg/gateway/rest.go` — **the cascade wiring is U18's line item W18b**; U20 only exposes `RemoveSessionUploadsTree(ids []string) error` |
| **U21** Test inversions | W22 | **exactly** the 12 named `*_test.go` files, and nothing else | all behaviour units | **any non-test file; any test file not in the twelve** |
| **U22** 3P process group + mutate path + hand-off writers | **RE-SCOPED** `[grill2 M2-3]`: **W9c (FR-029 `Setpgid` + group kill)**, **W3b (FR-099 mutate-path strictness)**, **W3c (`handoff.go:205`, `:386` writers)**, FR-100 doc-rot in `external_dispatch.go` | **NEW owner:** `pkg/agent/runner/driver_claude.go`, `pkg/agent/runner/driver_codex.go`, `pkg/agent/runner/driver_opencode.go`, NEW `pkg/agent/runner/procgroup_unix.go`, NEW `pkg/agent/runner/procgroup_windows.go`, **`pkg/tools/handoff.go`**; retained: `pkg/agent/external_dispatch.go`, `pkg/agent/approval_transcript.go` | U1, U2, U5 | `pkg/agent/turn.go`, `pkg/agent/subturn.go`, `pkg/tools/delegate.go` |
| **U23** Event payload types | **W21a** (`SubTurnSpawnPayload.SessionID` `events.go:441`, `SubTurnEndPayload.SessionID`), W5d (`ProducingSessionID` on session-scoped payloads) | **NEW owner:** `pkg/agent/events.go` | U1, U10 | every consumer of these types — U7/U9/U11 assign them |
| **U24** SPA session hierarchy | **W16f (sidebar tree), W16g (search tree)** — operator decision 1; **W22b (invert the two SPA component tests)** | **NEW owner:** `src/components/layout/Sidebar.tsx`, `src/components/search/SearchModal.tsx`, NEW `src/components/sessions/SessionTree.tsx`, **`src/components/layout/Sidebar.test.tsx`**, **`src/components/layout/Sidebar.focus-trap.test.tsx`**, **`src/components/layout/Sidebar.m5.test.tsx`**, **`src/components/search/SearchModal.test.tsx`** | U10, U12 | `src/store/chat.ts`, `src/lib/api.ts` — request shape changes from U12 |
| **U25** List-consumer wiring | **NEW** `[grill2 C2-1]` **W16i** — consume U9's paginated `ListAllSessions` signature | **NEW owner:** `pkg/sysagent/tools/deps.go`, **`pkg/sysagent/tools/diag.go`**, `pkg/gateway/gateway.go`, `pkg/gateway/rest_stats.go` | U6, U9 | `pkg/gateway/rest.go`, `pkg/gateway/websocket.go`, `pkg/agent/loop.go` |
| **U26** Task/goal-path store consumers | **NEW** `[grill2 C2-1, C2-3]` **W3d** (9 `AppendTranscript` sites), **W16j** (consume paginated `ListSessions`) | **NEW owner:** `pkg/agent/task_executor.go`, `pkg/agent/goal_loop.go`, `pkg/agent/goal_triggers.go` | U2, U5, U6 | `pkg/agent/turn.go`, `pkg/agent/loop.go`, `pkg/agent/subturn.go` |
| **U28** Config keys + seeded defaults | **NEW** `[grill2 C2-1, M2-1]` **W24b** (flush-interval key, FR-067), **W17b** (seed `subturn.max_concurrent`, FR-095) | **NEW owner:** `pkg/config/config.go`, `pkg/config/defaults.go` | — | every reader of these keys — U6 and U19 consume them |

> **There is no U27.** The number is skipped deliberately so that no reader mistakes a three-unit addition for a four-unit one; U25/U26/U28 are the three new units and the count is 28 rows for 27 units plus the U17a/U17b split. *(Unit total: 28 — U1…U16, U17a, U17b, U18…U26, U28.)*

### Ownership derivation

`[grill2 C2-1, o2-1]` **v2 proved the wrong property.** Its "Disjointness proof" was true and was independently re-verified by grill #2 — but two units writing the same file was never the observed failure mode. **A file with *no* unit was**, and v2 closed grill #1's exhaustiveness finding by adding owners for the five files that review happened to name, then declared the table exhaustive. Ten more were never looked for; six stopped the build the moment U4 or U8 merged.

So ownership is derived **from the symbols this spec changes**, not from any review's file list, and the commands are printed here so the check is reproducible in review rather than asserted:

```bash
# For each symbol this spec modifies, every non-test file that references it
# must appear in exactly one ownership row.
for sym in 'us\.mu' 'metaCache' 'cacheLoadFailures' \
           'ListSessions' 'ListAllSessions' \
           'InterruptSession\b' 'InterruptSessionHard\b' \
           'InterruptBySessionKey\b' 'InterruptBySessionKeyHard\b' \
           'AppendTranscript\(' 'IsDelegateChildEntry' 'ParentSpawnCallID' \
           'Inherit\(' 'InheritFrom\(' 'fetchSessions' \
           'mutateToolCallInTranscript' 'SysProcAttr' 'Setpgid'; do
  echo "== $sym"; rg -l "$sym" --glob '*.go' --glob '!*_test.go'
done
rg -l 'fetchSessions' src/ --glob '*.ts' --glob '*.tsx'
```

**Both properties are asserted, and both were machine-checked** (see the completeness check for the exact commands run):

- **Disjoint** — every path appears in exactly one row, with two **declared** chain exceptions under Rule 1: `pkg/session/unified.go` and `pkg/session/retention_sweep.go` are both owned by the **U4→U5→U6 chain**, whose members occupy three consecutive waves and never run concurrently.
- **Exhaustive** — every file the enumeration above returns has an owner or an explicit out-of-scope reason. The ten v2 left unowned are now assigned: `retention_sweep.go` → the U4→U5→U6 chain `[grill2 M2-12 — the one unowned file v2 named in a success criterion, SC-038]`; `pkg/commands/runtime.go` + `cmd_cancel_test.go` → U8; `deps.go`, `gateway.go`, `rest_stats.go` → U25; `goal_triggers.go`, `goal_loop.go`, `task_executor.go` → U26; `pkg/config/**` → U28; `pkg/agent/runner/**` and `handoff.go` → U22; plus `UsageScreen.tsx` and the four SPA test files → U12/U24 `[grill2 M2-9]`.

> **The mechanical derivation found an eleventh file the review did not.** `pkg/sysagent/tools/diag.go:212` calls `metas, errs := t.deps.ListSessions()` through the `deps.ListSessions` func field at `deps.go:197` — so FR-092's signature change makes it a **hard compile break**, and it appeared in neither v2's table nor grill #2's list of ten. It is assigned to **U25** alongside the `deps.go` field it consumes. This is precisely grill #2's stated prediction — *"re-derive ownership mechanically, otherwise grill #3 finds the eleventh file"* — and it is recorded here as evidence that the derivation, not the review, is now the source of the table. `pkg/tools/shell_process_unix.go:14` (`SysProcAttr{Setpgid: true}`) was likewise unowned and is assigned to **U16**, whose background-shell subsystem it belongs to; it is also the nearest in-house precedent for FR-029.

**Explicitly out of scope, with reasons** (these appear in the enumeration but are read-only precedent or unrelated subsystems, and no unit writes them): `pkg/session/lifecycle_lock.go`, `pkg/session/message_inbox.go`, `pkg/entity/lock.go` (striping/index precedents); `pkg/sandbox/hardened_exec*.go`, `pkg/sandbox/spawn_bg*.go` (the `Setpgid` precedent for FR-029); `pkg/fileutil/file.go` (the `Sync()` and `MkdirAll` behaviour this spec reasons about but does not change); `pkg/daemon/daemon*.go` and `pkg/tools/browser/cdppipe/allocator.go` (unrelated `SysProcAttr` uses); `pkg/session/usage.go` (a **comment-only** mention of `ListSessions` at `:193`; it declares no `UnifiedStore` method — verified, unlike `retention_sweep.go`, which declares one and was a genuine gap).

**`retention_sweep.go` is `UnifiedStore` internals in all but filename** — verified: it declares exactly one method (`func (us *UnifiedStore) RetentionSweep`, `:25`), takes `us.mu` at `:136`/`:141` and touches `metaCache` at `:127`/`:131`/`:139` — five references in total, and it is the **only** file besides `unified.go` holding either. FR-048 deletes `us.mu`; FR-050, SC-038 and Ambiguity item 13 name `RetentionSweep` by name. Putting it anywhere but the chain would split one lock surface across two owners.

### Integration order

```
Wave A  (parallel, no interdependencies)
  U1  types            U10 contracts+regen    U13 lifecycle index
  U16 shells           U17a approvals         U20 uploads primitive
  U28 config keys

Wave B  (parallel)                            [needs Wave A]
  U4  striping            (needs U1)
  U23 event payloads      (needs U1,U10)
  U12 SPA store+client    (needs U10)

Wave C  (parallel)                            [needs Wave B]
  U5  meta split          (needs U4)   ← SERIAL after U4, same files
  U2  strict store API    (needs U1,U4 | U5 same-wave: FROZEN CONTRACT, not an ordering edge)
  U24 SPA hierarchy       (needs U10,U12)

Wave D  (parallel)                            [needs Wave C]
  U6  stats throttle      (needs U5,U28)  ← SERIAL after U5, same files
  U3  turn.go             (needs U1,U2)
  U11 gateway WS          (needs U2,U10,U17a,U23)
  U22 3P group + mutate   (needs U1,U2,U5)

Wave E  (parallel)                            [needs Wave D]
  U8  steering+commands   (needs U3)
  U9  loop.go             (needs U3,U6,U10,U17a,U23)
  U17b session teardown   (needs U6,U17a)

Wave F  (parallel)                            [needs Wave E]
  U7  subturn             (needs U2,U3,U5,U17a,U17b)
  U14 delegate tool       (needs U13,U8)      ← MOVED from Wave B [grill2 C2-4]
  U15 cancel              (needs U8,U13,U16)
  U18 read boundaries+REST(needs U5,U9,U10,U13,U20)
  U25 list-consumer wiring(needs U6,U9)
  U26 task/goal consumers (needs U2,U5,U6)

Wave G  (parallel)                            [needs Wave F]
  U19 admission+wiring    (needs U13,U14,U8,U28)  ← MOVED from Wave C [grill2 C2-4]

Wave H  (own commit, no behaviour files)
  U21 test inversions
```

**The true critical path is six to seven sequential steps, not "28 units in 8 waves".** `[grill o-1 — accepted; recounted in v3]` The storage cluster is a forced serial chain on two files (`U4 → U5 → U6`, three waves by construction) and the read/REST cluster is gated behind it twice over (`U4 → U5 → U2 → U3 → U9 → U18` is six). The longest path is now `U1 → U4 → U5 → U3 → U8 → U14 → U19` — **seven** — created by C2-4's correction, which is the honest cost of making the interrupt-collapse dependency explicit rather than discovering it as a mid-wave compile break. Twenty-eight units buys parallel **breadth**, not a shorter path. If an implementer chooses to merge U4/U5/U6 into one agent taking three commits in the stated order, that is compliant with Rule 1 and loses nothing — the ownership overhead exists to stop *concurrent* writes, and a chain is not concurrent.

**What moved in v3, and why** `[grill2 C2-4]`:

| Unit | v2 wave | v3 wave | Reason |
|---|---|---|---|
| **U14** | B | **F** | Owns `delegate.go:363-364` / `:572-578`, whose func types **are** FR-041's collapsing signature. U8 (Wave E) changes it. v2 declared neither the work item nor the dependency |
| **U19** | C | **G** | Owns `session_messaging_wire.go:166`, `SetCancelHooks(al.InterruptBySessionKey, al.InterruptBySessionKeyHard)` — same signature. Also now needs U14 (unchanged) and U28 |
| **U17** → **U17a** | A | A | Approvals half has no store dependency; unchanged |
| **U17** → **U17b** | A | **E** | FR-064 requires a forced flush and FR-033 a `metaCache` eviction on the child `CloseSession` teardown. Verified: `CloseSession` (`pkg/agent/session_end.go:32-80`) touches `forgetSession`, `approvalGrants.ClearSession`, the recap claim and the idle ticker — **it holds no store reference at all**. The flush API is U6's (Wave D); v2 put the whole of U17 in Wave A with `Depends on: —` |
| **U22** | D | D | Re-scoped, not re-waved; its new work (`pkg/agent/runner/**`, `handoff.go`) needs only U2/U5 |

**Hard orderings (violating any of these is a defect, not a preference):**

1. **U4 → U5 → U6** (`W15 → W23 → W24`), Waves B → C → D. W15 must land before W23 because the split's four targeted writers each take a per-session shard; writing them against the old store-global mutex means four lock acquisitions where there was one — strictly worse than today. W23 must land before W24 because throttling counters that still live in the fused document is **Alternative F**, which is rejected. **Do not land W24 without W23.**
2. **U2 (AC-1's primitive) before any acceptance measurement.** ADR-057 §10: until `AppendTranscript` fails loudly, a green suite is not evidence. U2 is Wave C; every unit whose tests measure an acceptance criterion is Wave D or later.
3. **U10 (contracts) before U9, U11, U12, U23, U24.** Constraint #8: schema first, generated types only, one atomic commit. U10 is Wave A.
4. **U5 before U18.** Deleting the four filter sites before the child owns its own file un-hides narration with nothing gained. U5 is Wave C, U18 is Wave F.
5. **U21 last, in its own commit.** Bisection must be able to distinguish "the contract changed" from "the behaviour regressed".
5a. **U8 (W13) before U14, U15 and U19.** `[grill2 C2-4]` FR-041's compile-breaking surface is five files with five owners; three of them are not U8. Landing U14 or U19 before U8 leaves the tree uncompilable mid-wave. U8 is Wave E; U14/U15 are Wave F; U19 is Wave G.
5b. **U28 (config keys) before U6 and U19.** `[grill2 M2-1, C2-1]` FR-067's flush interval and FR-095's seeded root cap are both **MUST** config keys with no v2 owner. U28 is Wave A; U6 is Wave D; U19 is Wave G.
5c. **U6 before U17b.** `[grill2 C2-4]` The teardown flush FR-064 requires at `CloseSession` does not exist until U6 builds it.
6. **W11's two halves must land in the same integration window.** `[grill §5, W11]` U5 deletes `IsDelegateChildEntry` (Wave C) and U18 deletes its four call sites (Wave F). **The intermediate tree does not compile.** This is enforced, not hoped: U5 MUST land the deletion behind a `//lint:ignore` deprecation shim that keeps the method compiling but always returns `false`, and U18's commit MUST remove both the shim and the call sites. Test #58's positive lower bound (see Rule 4) fails if the shim survives U18.
7. **U6 → U9 → U18 → U12/U24 for pagination.** `[grill M-1]` FR-068/FR-092 requires four layers; each now has exactly one owner, and they only work in this order because each calls the one below it.

**Cross-unit requests (a unit needing a change it does not own):**

| Requesting unit | Needs | From owner |
|---|---|---|
| U2 | a `lockSession(id)` helper on `UnifiedStore` | U4 |
| U2 | **frozen:** `readMetaLocked(sessionID string) (*UnifiedMeta, error)` keeps its exact signature; W23 may change only its internals. `AppendTranscriptStrict`'s existence predicate is "`readMetaLocked` returned a non-nil error" `[grill M-5]` | U5 |
| U7 | **the two-key `InheritFrom(srcSessionID, srcAgentID, dstSessionID, dstAgentID)` signature** `[grill C-1, M-3]` | U17 |
| U9 | **the two-key grant read** — `IsAllowed` under the child's own session key `[grill M-3]` | U17 |
| U7 | the child-terminal `CloseSession(sessionID, trigger string)` entry point (`pkg/agent/session_end.go:32`), unchanged signature, new trigger value `[grill M-13]` | U17 |
| U7 | the exported exact-id create wrapper `CreateSessionWithID` | U2 |
| U7 / U9 / U11 | `ProducingSessionID` on the session-scoped payload structs | U23 |
| U18 | `IsDelegateChildEntry` removed from `daypartition.go` (and its shim, see hard ordering 6) | U5 |
| U18 | `RemoveSessionUploadsTree(ids []string) error` primitive `[grill M-2]` | U20 |
| U18 | paginated `ListAllSessions(limit, offset int, parentSessionID string)` | U9 |
| U18 | the descendant walk over the parent index | U13 |
| U9 | paginated `ListSessions(limit, offset int, parentSessionID string)` | U6 |
| U14 | `KillBackgroundSessions` reachable from the `delegate cancel` path | U16 |
| U9 / U11 / U23 | the `producing_session_id` generated type | U10 |
| U12 | the paginated + `parent_session_id`-filterable list contract | U10 |
| U24 | the tree-assembly helper and `child_count`-bearing `Session` type from the API client | U12 |
| U15 | the descendant-set accessor computed in PHASE A | U8 |
| U22 | `AppendTranscriptStrict` | U2 |
| **U14 / U15 / U19** | **frozen:** the collapsed `Interrupt(sessionID, hint string, scope InterruptScope) ([]string, error)` signature, published once by U8 and unchanged for the rest of the change set (Rule 7) `[grill2 C2-4]` | **U8** |
| **U17b** | **frozen:** the forced-flush entry point `FlushSessionStats(sessionID string) error` and the cache-eviction entry point `EvictSessionMeta(sessionID string)`, both taking the session's shard internally, signatures fixed before U17b lands `[grill2 C2-4]` | **U6** |
| **U7** | the child-terminal `CloseSession(sessionID, trigger string)` **behaviour** now also flushing stats and evicting `metaCache` — the signature is unchanged and frozen | **U17b** |
| **U6 / U19** | the flush-interval key and the seeded `agents.defaults.subturn.max_concurrent` default `[grill2 M2-1]` | **U28** |
| **U6** | the `metaCache`-adjacent parent index and its `cacheMu` guard (FR-097) | **U4** |
| **U25** | the paginated `ListAllSessions(limit, offset int, parentSessionID string, flat bool)` signature `[grill2 C2-1]` | **U9** |
| **U26** | the strict `AppendTranscript` and the paginated `ListSessions` | **U5**, **U6** |
| **U24** | the four SPA component-test inversions are U24's own; the `api.ts`/ADR-052 params tests are U12's `[grill2 M2-9]` | **U12** |
| **U12** | the `flat=true` list contract backing per-session usage accounting (FR-104) | **U10**, **U18** |

---

## BDD Scenarios

### Feature: Session parent/child unification (ADR-057)

#### Background

- **Given** a real `UnifiedStore` rooted at a temporary directory on the real filesystem
- **And** a real lifecycle store wired to the delegate tool
- **And** parent and child session ids constructed as **distinct, non-equal** values

---

#### BDD-01 — Scenario: Transcript append to an unknown session fails loudly and creates nothing

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Error Path

- **Given** a real `UnifiedStore` containing no session with id `X`
- **When** `AppendTranscriptStrict(X, entry)` is called
- **Then** a non-nil error is returned
- **And** `os.Stat(<baseDir>/X)` reports the path does not exist
- **But** no WARN-and-return-nil path is taken

#### BDD-02 — Scenario: Transcript append to an existing session appends exactly one line

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a real session `Y` created through the store
- **When** `AppendTranscriptStrict(Y, entry)` is called
- **Then** nil is returned
- **And** `<baseDir>/Y/transcript.jsonl` contains exactly one more line than before

#### BDD-03 — Scenario Outline: Each turn-level transcript writer surfaces an unresolvable session id

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path

- **Given** a registered turn whose transcript store is wired and whose session id does not resolve
- **When** the writer at `<site>` runs
- **Then** an error counter is incremented and a WARN naming the session id is emitted

**Examples**:

| site | writer |
|---|---|
| `pkg/agent/turn.go:1130` | tool-call transcript |
| `pkg/agent/turn.go:1208` | intermediate assistant |
| `pkg/agent/turn.go:1270` | final assistant |
| `pkg/agent/turn.go:1325` | error transcript |
| `pkg/gateway/websocket.go:4256` | streamed assistant |

#### BDD-04 — Scenario: An abandoned turn's suppressed write is counted, not silent

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a registered turn with `ts.abandoned` set, and `AbandonedWritesSuppressed()` sampled immediately beforehand
- **When** a transcript write is attempted
- **Then** the write is suppressed and no entry lands in the transcript
- **And** a **WARN log record** is emitted naming the session id and the suppression reason
- **And** `AbandonedWritesSuppressed()` has increased by **exactly one** across the call
- **But** the assertion is on the log record and the counter **delta** — never on the counter's existence, which `[grill C-2]` verified is already satisfied today at `pkg/agent/turn.go:1297` and already covered by `pkg/agent/turn_test.go:221`

#### BDD-109 — Scenario: A transcript mutate against a missing session is counted and logged, not silently false

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error

- **Given** a real `UnifiedStore` and a turn whose `transcriptStore` is wired
- **And** a session id for which no `meta.json` exists, and a `callID` that therefore cannot resolve
- **When** `replaceToolCallInTranscript` runs and reaches `mutateToolCallInTranscript` (`pkg/agent/approval_transcript.go:188+`)
- **Then** a WARN naming the session id and the call id is emitted **and** a counter increases by exactly one
- **And** the same holds for the "session exists but the entry does not" case, which MUST be distinguishable in the log record
- **But** today it returns a bare `false` with no signal `[grill2 M2-3]` — the read-modify-write twin of AC-1's silent append, and the one genuine hazard on the path v2 assigned U22 a factually empty work item for

---

#### BDD-05 — Scenario: Routing and session ids are distinct types

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the compile-fail fixture file is present and locatable by path (asserted first; absence is a failure, not a pass)
- **When** `go build` runs over the fixture, in which a `RoutingSessionID` is assigned to a `SessionID` without conversion
- **Then** the build fails with a type error naming **both** type names
- **But** a build that succeeds, or a fixture that could not be found, fails the test

#### BDD-97 — Scenario Outline: Every negative gate proves its search is live before asserting zero

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Error Path

- **Given** the merged tree and the gate `<gate>` with stated positive lower bound `<K>`
- **When** the gate runs
- **Then** it first asserts it located at least `<K>` occurrences of `<positive_target>`
- **And** only then asserts its exclusion property
- **But** a run in which the search located fewer than `<K>` **fails**, and never reports the exclusion as satisfied

**Examples**: **every** row of "Negative-gate positive lower bounds" — currently **thirteen**: #3, #9, #12, #17, #19, #27, #29, #58, #81, #82, #83, **#104**, **#106**. `[grill2 M2-5]` The example set is **generated by rule 4's membership predicate**, not fixed: any TDD-plan row whose assertion is an exclusion, a zero-count, a "no such thing exists" or a compile-must-fail belongs here, and #91 iterates this table. v2 listed eleven and then added #104 and #106 outside it — one of which (#106) is the sole coverage of FR-023, a security property.

---

#### BDD-06 — Scenario: A delegation creates a store-backed session with the exact child id

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a parent chat session and a registered parent turn
- **When** one delegation is dispatched
- **Then** `<baseDir>/<childID>/meta.json` exists on disk
- **And** the session id inside it equals `childID` exactly

#### BDD-07 — Scenario: The child inherits the parent's session owner

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a parent session whose `meta.Owner` is a non-empty principal
- **When** a child spawns and executes `system.workspace.create`
- **Then** the created entity's owner is non-empty and equals the parent's owner

#### BDD-08 — Scenario: A child with no inherited owner does not silently disable ownership stamping

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Error Path

- **Given** a parent session whose `meta.Owner` is empty
- **When** a child spawns
- **Then** the absence is observable in logs rather than only manifesting as an unstamped entity later

#### BDD-09 — Scenario: The child's process options carry no `NoHistory` flag

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a delegation about to spawn
- **When** the child's `processOptions` are constructed
- **Then** `NoHistory` is absent
- **And** `TranscriptSessionID` equals `childID`

#### BDD-10 — Scenario: The child's meta names its direct parent and a subordinate type

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a delegation at depth 2 (a grandchild)
- **When** the grandchild's meta is read
- **Then** `ParentSessionID` equals the depth-1 child's id, not the chat's
- **And** the session type is the subordinate value

#### BDD-11 — Scenario Outline: Per-delegation controls take the same single id as today

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** a running child session
- **When** `<action>` is invoked with the child's id
- **Then** it resolves and acts on that child

**Examples**:

| action |
|---|
| `steer` |
| `respond` |
| `cancel` |
| `peek` |
| `inbox` |
| `follow_up` |

---

#### BDD-12 — Scenario: A root turn's routing id equals its own session id

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a root chat turn with session id `S`
- **When** `routingSessionID` is read
- **Then** it equals `S`
- **And** every session-scoped frame it emits carries `session_id == S` with no `producing_session_id`

#### BDD-13 — Scenario: A child inherits the routing id verbatim and the pre-arm latch still matches

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a parent turn that sets a pending-spawn pre-arm marker
- **When** the child spawns and later clears the marker
- **Then** the keys cleared are exactly the keys set
- **And** the child's `routingSessionID` equals the parent's

#### BDD-14 — Scenario: Span and steps land in the same SPA bucket on the live connection

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a real gateway and a real WS client subscribed to chat session `S`
- **When** one delegation runs to completion
- **Then** the SPA store's `S` bucket contains the subagent span **and** its tool-call steps
- **And** `spanByParentCallId` resolves for that span
- **But** `logDiagnostic('chatAttachStepSpanIndexMiss')` never fires

#### BDD-15 — Scenario: Span and steps still correlate after a reconnect

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a delegation in progress
- **When** the browser reconnects mid-delegation and replay completes
- **Then** the span and its steps are in the same bucket and correlate

> `[grill M-8]` **v1's BDD-16 asserted one property across all 19 types, and it is false for at least five of them.** Its Given was "a child turn emitting frame type X" and its Then was "`producing_session_id` equals the child's own id" — but `replay_message`/`replay_done` are emitted by the gateway replay path, not by a turn; `session_started`/`session_close_ack` are chat-lifecycle frames, not turn output; and `rate_limit` has **no `SessionID` field at all** (`pkg/agent/events.go:525-533`, verified: `Scope`, `Resource`, `PolicyRule`, `RetryAfterSeconds`, `AgentID`, `ChatID`, `Tool`), which directly contradicts this spec's own dataset row 5 expecting `producing_session_id` **absent**. SC-006 ("all 19 round-trip both ids") was therefore unachievable as literally written while being scored pass/fail. The outline is split into three classes. **The classification itself is the W5 audit's deliverable** (FR-089) — this spec pins only the rows it verified, and requires the remaining ones to be classified and committed rather than guessed here.

#### BDD-16 — Scenario Outline: Class (a) — frames a child turn genuinely emits carry both ids

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a child turn emitting frame type `<frame_type>`, with the child's id distinct from the routing key
- **When** the frame crosses the wire
- **Then** `session_id` equals the routing key
- **And** `producing_session_id` is present and equals the child's own id

**Examples** (verified child-turn-produced):

| frame_type |
|---|
| `token` |
| `done` |
| `tool_call_start` |
| `tool_call_result` |
| `tool_approval_required` |
| `media` |

#### BDD-98 — Scenario Outline: Class (b) — root- or gateway-produced frames omit `producing_session_id`

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** frame type `<frame_type>`, produced by the routing session itself or by the gateway rather than by a child turn
- **When** the frame crosses the wire
- **Then** `session_id` equals the routing key
- **And** `producing_session_id` is **absent** — because FR-013 requires it present *iff* it differs

**Examples** (each row's class is fixed by the W5 audit artefact required by FR-089; the four below are the ones this spec verified):

| frame_type | why class (b) |
|---|---|
| `replay_message` | emitted by the gateway replay path, not by a turn |
| `session_started` | chat-session lifecycle, not turn output |
| `session_close_ack` | chat-session lifecycle, not turn output |
| `subagent_start` / `subagent_end` | emitted by the **parent** about the child (`pkg/agent/subturn.go`); FR-017 pins their `SessionID` to the routing key, so producing == routing |

#### BDD-99 — Scenario Outline: Class (c) — the two known-broken types assert the audited, documented gap

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Edge Case

- **Given** frame type `<frame_type>`, a pre-existing strain this change **exposes** and does not cause
- **When** the frame crosses the wire
- **Then** the **documented** behaviour is asserted exactly as recorded, `producing_session_id` is absent, and the gap is present in the W5 audit artefact
- **But** no requirement in this spec depends on either type carrying a producing id `[operator 11 — audit, document, do not fix]`

**Examples**:

| frame_type | verified gap |
|---|---|
| `rate_limit` | `RateLimitPayload` has **no `SessionID` field** (`pkg/agent/events.go:525-533`); its `session_id` is reconstructed from the connection's chat→session map (`pkg/gateway/websocket.go:3461` → `sessionIDForChat`, `:3022`), and a reconstructed `""` is dropped in production |
| `replay_done` | in `SESSION_SCOPED_FRAME_TYPES` (`src/store/chat.ts:1238`) but absent from the `WsFrameType` enum on both sides — verified: tree-wide it appears **only** at that one line, with zero hits in `contracts/`, `pkg/api/generated/` and `pkg/` |

**Remaining types** — `agent_switched`, `task_status_changed`, `system_overload`, `cancel_stage`, `goal_status`, `loop_status` — MUST each be assigned to (a), (b) or (c) by the W5 audit and asserted per its class (FR-089). **This spec does not guess them**; assigning them by inspection here is exactly the unverified-claim habit `[grill C-2]` caught.

#### BDD-17 — Scenario: A read of the routing id outside the closed consumer set fails the build gate

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path

- **Given** the non-test tree
- **When** the consumer-set test enumerates every read of `routingSessionID`
- **Then** it fails if any read appears outside WS payload stamping, the seven role-B predicates, or the pre-arm keys

---

#### BDD-18 — Scenario: Children-of-X returns direct children only

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** persisted lifecycle records for chat `A`, child `B`, grandchild `D` and sibling `C`
- **When** `List(LifecycleFilter{ParentDurableKey: A})` is called
- **Then** exactly `B` and `C` are returned
- **But** `D` is not

#### BDD-19 — Scenario: The parent index makes the walk cost proportional to descendants

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a store containing many unrelated sessions and one small subtree
- **When** the descendant walk runs over that subtree
- **Then** its file-read count scales with the subtree size, not with the total session count

#### BDD-20 — Scenario: Delegation is refused when no lifecycle store is wired

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Error Path

- **Given** a delegate tool with no lifecycle store
- **When** a delegation is attempted
- **Then** it is refused with an operator-visible error
- **But** no child session is created and no success payload is returned

#### BDD-21 — Scenario: A child minted without a parent agent id is still cancellable

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case

- **Given** `tools.delegate.require_parent_agent_id=false`
- **When** a child is minted with a blank `ParentAgentID` and a Stop is issued on the chat
- **Then** the child is reached by the `ParentDurableKey` walk and cancelled

#### BDD-22 — Scenario: The three parentage doc comments no longer assert shared keys

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the merged tree
- **When** `pkg/session/lifecycle.go:225-228`, `:572-575` and `pkg/tools/list_jobs_sources.go:311-315` are read
- **Then** none describes `ParentDurableKey` as shared between a parent and its children

---

#### BDD-23 — Scenario: A Stop hard-aborts a live child in PHASE B

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a registered root turn that finishes gracefully and a registered `Critical:true` child turn that does not
- **When** a real Stop is issued on the chat
- **Then** PHASE B's hard abort fires against the child

#### BDD-24 — Scenario: A Stop detaches a surviving child in PHASE C

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the same tree with the child surviving the hard abort window
- **When** PHASE C's window elapses
- **Then** the detach fires against the child

#### BDD-25 — Scenario: The cancel audit entry names the descendants it reached

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a Stop that reached one child
- **When** the `turn_canceled` audit entry is read
- **Then** `descendants_canceled` is non-empty and contains the child's turn id

#### BDD-100 — Scenario: The cancel audit names every descendant at depth 3

**Traces to**: User Story 5, Acceptance Scenario 2b
**Category**: Edge Case

- **Given** a chat with live descendants at depths 1, 2 and 3 (the same tree BDD-30 builds)
- **When** a Stop is issued on the chat and the `turn_canceled` audit entry is read
- **Then** `descendants_canceled` (`pkg/agent/cancel.go:376`) contains **all three** turn ids
- **But** v1 asserted this depth only in the post-implementation evaluation set `[grill M-12]` — FR-030 requires "every descendant", and a scenario the implementing agent never sees cannot serve as acceptance evidence for an FR

#### BDD-26 — Scenario: The orphan watchdog defers while a critical delegate is alive

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case

- **Given** an orphaned root turn and a live `Critical:true` async delegate
- **When** the ADR-045 watchdog evaluates its fire predicate
- **Then** it does not fire

#### BDD-27 — Scenario: The orphan watchdog fires once the delegate finishes

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the same orphaned root
- **When** the critical delegate completes
- **Then** the watchdog fires and reaps the root

#### BDD-28 — Scenario: A chat Stop kills a child's background shell but not a sibling's

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Happy Path

- **Given** child `B` and sibling `C` each running a real background `bash` process
- **When** a chat-level Stop is issued on `B`'s chat with `C` under a different chat
- **Then** `B`'s real PID is gone
- **But** `C`'s process is still alive

#### BDD-29 — Scenario: `delegate action=cancel` kills that child's background shells

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Happy Path

- **Given** child `B` running a real background `bash` process
- **When** `delegate action=cancel` targets `B`
- **Then** `B`'s real PID is gone

#### BDD-30 — Scenario: Every descendant's lifecycle record transitions to cancelled

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Happy Path

- **Given** a chat with children at depths 1, 2 and 3
- **When** a Stop is issued on the chat
- **Then** each descendant's persisted lifecycle record reads `cancelled`

---

#### BDD-31 — Scenario: A child executes a parent-granted tool with no prompt

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a standing approval grant on the parent for tool `T`, with `parentSessionID != childSessionID`
- **And** a pre-spawn lookup of `{childSessionID, childAgentID}` that returns **absent**
- **When** a delegated child executes `T`
- **Then** the tool runs immediately
- **But** no approval prompt is raised and no wait occurs

#### BDD-88 — Scenario: The grant is read under the parent's key and written under the child's

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `parentSessionID != childSessionID` and `parentAgentID` possibly equal to `childAgentID` (self-delegation is a same-agent, different-session union)
- **And** a grant recorded **only** under `{parentSessionID, parentAgentID}`, verified absent under `{childSessionID, childAgentID}`
- **When** `InheritFrom(parentSessionID, parentAgentID, childSessionID, childAgentID)` runs
- **Then** the grant resolves under `{childSessionID, childAgentID}`
- **And** it still resolves under `{parentSessionID, parentAgentID}` — inheritance is a copy, not a move
- **But** a single-key `Inherit` cannot satisfy this scenario `[grill C-1]`: passing the child's id for both source and destination makes the source lookup miss at `pkg/security/approvalgrants.go:118`, the function returns at `:119`, and the child hangs 300 s at `pkg/agent/loop.go:8630-8631`

#### BDD-89 — Scenario: An inheritance with no source grants is logged and counted

**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Error Path

- **Given** a parent that holds **no** grants for its session
- **When** `InheritFrom` runs at spawn
- **Then** a log record names the source key, the destination key and "no grants to inherit"
- **And** a counter increments across the call
- **But** the function does not return silently as `Inherit` does today (`pkg/security/approvalgrants.go:118-120`, documented as intended at `:110-111`) — this is the tripwire that stops a future re-key from regressing into C-1 unnoticed

#### BDD-32 — Scenario: A child's inherited grant does not outlive the child

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a child holding an inherited grant keyed to its own session
- **When** the child session terminates
- **Then** the grant set for that session no longer exists
- **And** the **parent's** grant set under its own key is untouched

#### BDD-33 — Scenario: A chat Stop cancels a pending approval inside a child

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a pending approval request raised inside a child
- **When** a chat-level Stop is issued
- **Then** the registry entry is gone, its timer is stopped, and the child's goroutine unblocks
- **And** the cancellation ran over the **descendant set**, not a single id — `cancelAllPendingForSession` matches by exact equality on `SessionID` (`pkg/gateway/approvals.go:419`), so a chat id alone would match nothing once entries carry the child's id

#### BDD-90 — Scenario: A pending-approval registry entry carries the acting session's id

**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Happy Path

- **Given** a child turn raising a real approval request, with the child's id distinct from the chat's
- **When** the registry entry is inspected (`pkg/gateway/approvals.go:85`, set at `:213`/`:232`)
- **Then** its `SessionID` is the **child's own** session id
- **But** it is not the chat's routing id `[grill M-6]` — v1's FR-032 presupposed this without any requirement stating it

#### BDD-91 — Scenario: A client approves a child's request and the round trip resolves by approval id

**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Happy Path

- **Given** that pending entry, and a `tool_approval_required` frame whose `session_id` is the **routing** key (it is in `SESSION_SCOPED_FRAME_TYPES`, `src/store/chat.ts:1240`, so FR-012 applies)
- **When** the client responds **approve**
- **Then** the response resolves to the child's entry **by approval id**
- **And** the child's tool call proceeds without a prompt or a timeout
- **But** the resolution does **not** depend on the frame's `session_id` matching the registry's, so the routing-key change cannot break it `[grill M-6]` — v1 covered only the cancel path and never the interactive approve round trip

#### BDD-34 — Scenario: Child teardown evicts grants, loaded tools and recall spans

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a child that loaded tools and recorded recall spans
- **When** the child turn reaches a terminal state
- **Then** its grant set, `loadedTools` bucket, `metaCache` entry and `recallSpans` entries are all gone

#### BDD-96 — Scenario Outline: `CloseSession` fires from the child-turn terminal path on every terminal state

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a child turn that reaches terminal state `<terminal_state>`
- **When** the turn's terminal path runs
- **Then** `CloseSession(childID, "delegate_terminal")` has been invoked for that child
- **And** `forgetSession`'s first arm matches (`key == sessionID`, `pkg/agent/loop.go:11497-11500`), because a child's `sessionKey` is a bare UUID

**Examples**:

| terminal_state |
|---|
| completed |
| cancelled |
| failed |
| abandoned |

> `[grill M-13]` **No unit owned this call site in v1 and no call site exists in the tree.** Verified: `CloseSession` is defined at `pkg/agent/session_end.go:32` and its only non-test callers are `pkg/gateway/websocket.go:1038` (explicit user close), `pkg/agent/loop.go:1048`/`:1064` (idle sweep) and `pkg/agent/session_end.go:865` (bootstrap) — **none is a child-turn terminal**. v1 assigned "W10 (teardown call site)" to U11, whose file is the *user* session-close path. The child's terminal path is in `pkg/agent/subturn.go`; **U7 now owns the call**, U17 owns the entry point, and the cross-unit request is recorded. This spec's own Edge Cases already flagged the risk ("provided something actually calls `CloseSession`") and then left it unassigned.

---

#### BDD-35 — Scenario: The delegate-child predicate has no non-test references

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the merged tree
- **And** a first assertion that the search located **≥ 60** non-test **Go** references to `ParentSpawnCallID` (measured 73 across 9 files; binding rule 4)
- **When** a Go-source-only reference check runs for `IsDelegateChildEntry`
- **Then** zero references exist outside tests
- **And** none of the four read boundaries filters on `ParentSpawnCallID`
- **But** the search MUST be scoped to `*.go` — an unscoped `rg` matches this spec, the ADR and the review, and can therefore **never** return zero

#### BDD-36 — Scenario: The child's transcript is complete and the parent's contains none of it

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Happy Path

- **Given** one completed delegation with `parentID != childID`, producing a known non-zero number `N` of child transcript entries
- **When** **both** `transcript.jsonl` files are read directly from disk **in the same run**
- **Then** the parent's contains zero entries produced by the child
- **And** `<baseDir>/<childID>/transcript.jsonl` contains exactly `N` entries with the expected content
- **But** the first assertion alone is **not** sufficient `[grill M-9]`: it is satisfied by a child that wrote nothing anywhere, which is the expected outcome when the session mint is broken and FR-002 downgrades every write failure to a counter plus a WARN

#### BDD-37 — Scenario Outline: Each read boundary returns the child's transcript unfiltered

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a completed child session with narration, a final report and a tool call
- **When** `<boundary>` reads that session
- **Then** all of its entries are returned

**Examples**:

| boundary |
|---|
| `GET /api/v1/sessions/{childID}` |
| `GET /api/v1/sessions/{childID}/messages` |
| `inspect_session` |
| live-reconnect replay |

#### BDD-38 — Scenario: Child entries retain spawn-call provenance

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a child session's own transcript entries
- **When** they are read
- **Then** `ParentSpawnCallID` is populated on the entries that carried it before
- **And** the drill-down surface reads it

#### BDD-39 — Scenario: The verifier window sees only the adjudicated session's entries

**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Happy Path

- **Given** a session under adjudication that spawned a delegation
- **When** the verifier renders its window
- **Then** it receives that session's own entries and nothing else

#### BDD-40 — Scenario: A pre-cutover session shows previously-hidden delegate narration

**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Edge Case

- **Given** a session file written before the cutover containing entries with `ParentSpawnCallID` set
- **When** it is rendered
- **Then** those entries appear as top-level bubbles
- **And** this is recorded as the accepted, bounded consequence R-16

---

#### BDD-41 — Scenario: A sibling cannot address another sibling

**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Error Path

- **Given** chat `A` with children `B` and `C`
- **When** `B` invokes a gated delegate action against `C`
- **Then** the action is rejected with an ownership error

#### BDD-42 — Scenario: The root chat can still address a grandchild

**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path

- **Given** chat `A`, child `B` and grandchild `D`
- **When** `A` invokes a gated delegate action against `D`
- **Then** the action is permitted

#### BDD-43 — Scenario: The ancestor walk terminates at the configured depth bound

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a chain longer than the configured max delegation depth
- **When** the walk runs from the deepest record
- **Then** it stops at the bound and rejects
- **But** it does not loop or scan the whole store

#### BDD-44 — Scenario Outline: Every gated action uses the walk

**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Happy Path

- **Given** chat `A`, child `B` and grandchild `D`
- **When** `A` invokes `<action>` against `D`
- **Then** it is permitted, and when `B`'s sibling invokes the same action it is rejected

**Examples**:

| action | site |
|---|---|
| `inbox` | `pkg/tools/delegate.go:2010` |
| `steer` | `:2107` |
| `respond` | `:2159` |
| `cancel` | `:2321` |
| `follow_up` | `:2459` |
| `peek` | `:2592` |

---

#### BDD-45 — Scenario: An interrupt without an explicit scope does not compile

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Error Path

- **Given** the collapsed interrupt API
- **When** a caller omits the `InterruptScope` argument
- **Then** compilation fails

#### BDD-46 — Scenario: Subtree-scoped interrupt at a child spares parent and sibling

**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Happy Path

- **Given** chat `A` with children `B` and `C`, and `B` with child `D`
- **When** `Interrupt(B, ScopeSubtree)` runs
- **Then** `B` and `D` are cancelled
- **But** `A` and `C` keep running

#### BDD-47 — Scenario: Subtree-scoped interrupt at the chat reaches all depths

**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the same tree
- **When** `Interrupt(A, ScopeSubtree)` runs
- **Then** `A`, `B`, `C` and `D` are all cancelled

#### BDD-48 — Scenario: The two-namespace gate test asserts the new invariant

**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Happy Path

- **Given** `pkg/agent/interrupt_by_session_key_test.go`
- **When** the change lands
- **Then** the test exists and asserts the scoped-interrupt invariant
- **But** it has not been deleted

---

#### BDD-114 — Scenario: No doc comment survives describing the retired four-entry-point interrupt API

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Edge Case

- **Given** the merged tree after W13
- **When** a doc-truth gate greps non-test Go for `InterruptSession`, `InterruptSessionHard`, `InterruptBySessionKey` and `InterruptBySessionKeyHard`
- **Then** it first asserts it located **≥ 1** reference to the **new** collapsed entry point (proving the search and the file set both work)
- **And** then asserts **zero** surviving references to the four retired names, in code **or** comments
- **But** v2 left ~25 such references across eight files (`cancel_prearm.go`, `external_dispatch.go`, `loop.go`, `orphan_watch.go`, `subturn.go`, `turn.go`, `config.go`, `approvals.go`) `[grill2 C2-4]` — not compile breaks, which is why their owners need no dependency on U8, but exactly the doc-rot FR-022/FR-037/FR-059 already gate elsewhere

---

#### BDD-49 — Scenario: A synchronous delegation reports a non-empty activity snapshot

**Traces to**: User Story 10, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a completed synchronous delegation
- **When** `delegate action=status` is called for it
- **Then** the activity snapshot is non-empty

#### BDD-50 — Scenario: An asynchronous delegation reports a non-empty activity snapshot

**Traces to**: User Story 10, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a completed asynchronous delegation
- **When** `delegate action=status` is called for it
- **Then** the activity snapshot is non-empty

#### BDD-51 — Scenario: A genuinely empty activity path logs

**Traces to**: User Story 10, Acceptance Scenario 3
**Category**: Error Path

- **Given** a delegation whose session has no recent activity lines
- **When** `recentActivityLines` returns nothing
- **Then** a log line records the empty result

#### BDD-52 — Scenario: Delegate task maps do not grow without bound

**Traces to**: User Story 10, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a stated retention bound `C` and TTL `T`, and `N ≫ C` delegations that have reached a terminal state with their last `status` read older than `T`
- **When** the eviction pass has run
- **Then** `len(t.tasks) ≤ C` **and** `len(t.sessionIndex) ≤ C`
- **And** a task still within `T` of its last `status` read is **retained**, so eviction cannot break `delegate action=status`
- **But** "fewer than N" is explicitly **not** the assertion `[grill M-10]` — it is satisfied by deleting one entry, and with no bound stated the requirement cannot fail

---

#### BDD-53 — Scenario: Concurrent writes to different sessions do not serialise

**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a real on-disk store and N goroutines each creating and appending to its own session
- **When** the run completes at N and again at 2N
- **Then** wall-clock at 2N is materially less than double the wall-clock at N
- **And** the same measurement against the pre-change store is the baseline this must beat

#### BDD-54 — Scenario: Listing does not block on an unrelated in-flight create

**Traces to**: User Story 11, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a `NewSession` in flight on session `A`
- **When** `ListSessions` is called
- **Then** it returns without waiting for `A`'s fsyncs

#### BDD-55 — Scenario: A session create does not stall another session's token stream

**Traces to**: User Story 11, Acceptance Scenario 3
**Category**: Happy Path

- **Given** session `A` streaming transcript appends continuously
- **When** session `B` is created
- **Then** `A`'s inter-token interval stays within its pre-change distribution

#### BDD-56 — Scenario: Mixed concurrent store operations are race-clean

**Traces to**: User Story 11, Acceptance Scenario 4
**Category**: Edge Case

- **Given** concurrent create, append, `SetMeta`, `ListSessions` and `DeleteSession` on overlapping and disjoint ids
- **When** the suite runs under `-race`
- **Then** the run is clean
- **And** `ClearAll` / `RetentionSweep` interleaved with per-session writes neither deadlock nor drop a session

#### BDD-57 — Scenario: No cache critical section performs filesystem work

**Traces to**: User Story 11, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the merged tree
- **And** a first assertion that the gate located **≥ 3** `cacheMu` critical sections (binding rule 4; `cacheMu` does not exist today — `grep -c cacheMu pkg/session/unified.go` = 0 — so W15 must create them and a gate that finds zero has found a bug)
- **When** every located `cacheMu` critical section is inspected
- **Then** none contains an `os.*` or `fileutil.*` call
- **And** no code path takes `cacheMu` before a session shard

#### BDD-92 — Scenario: The parent-`Owner` copy never holds two session shards at once

**Traces to**: User Story 11, Acceptance Scenario 7 · User Story 2, Acceptance Scenario 2b
**Category**: Edge Case

- **Given** an instrumented lock wrapper recording every `sessionLock` acquire and release in order
- **When** `CreateSessionWithID(childID, …)` copies the parent's `Owner`
- **Then** the recorded sequence is `acquire(shard(parent)) … release(shard(parent)) … acquire(shard(child))`
- **And** at no instant are two session shards held simultaneously
- **And** `ClearAll` / `RetentionSweep` acquire all 64 in **index** order, recorded and asserted
- **But** the assertion is on the recorded **order**, not on a `-race` run `[grill C-6]` — Go's race detector is not a lock-order checker and reports nothing for an inversion that does not happen to deadlock in that run

#### BDD-95 — Scenario: Listing concurrent with a delete honours the stated consistency model

**Traces to**: User Story 11, Acceptance Scenario 6
**Category**: Edge Case

- **Given** a store where `ListSessions` reconciles per-session under each session's shard and then snapshots under `cacheMu.RLock` (FR-051)
- **When** a `DeleteSession` runs concurrently, interleaved so it lands between the reconcile pass and the snapshot
- **Then** the result MAY omit the deleted session
- **And** the result MUST NOT contain a session whose directory was already absent when the call began
- **And** no call panics, deadlocks, or returns a partially-composed meta
- **But** v1 stated **no** consistency model for `ListSessions` after striping `[grill M-14]` — today the whole method runs under `us.mu.Lock()` and the doc comment at `pkg/session/unified.go:1240-1246` says exactly why ("an RLock cannot be upgraded to a Lock without risking deadlock"), so splitting it creates a window that must be specified rather than discovered

---

#### BDD-110 — Scenario: A directory deleted out of band stops being listed

**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a real `UnifiedStore` with sessions `A` and `B` both loaded into `metaCache`
- **And** `B`'s directory removed **out of band** — by `os.RemoveAll` directly, not through `DeleteSession`
- **When** `ListSessions` runs
- **Then** the reconcile pass **prunes** `B`'s cache entry under `B`'s shard and updates the parent index (FR-097a)
- **And** the returned set contains `A` and **not** `B`
- **And** a session excluded by `cacheLoadFailures` (a construction-time load failure, Ambiguity item 8) is **not** disturbed — it stays excluded for the process lifetime, which is a different set from "directory is gone"
- **But** today the reconcile pass only **adds** entries (`pkg/session/unified.go:1251-1280`) and `B` is returned forever `[grill2 M2-7]`. The in-house precedent is `ClearAll`'s stat-and-prune loop (`:1483-1487`), whose own comment names this exact outcome: *"leaving ListSessions to resurrect sessions that are gone from disk"*

---

#### BDD-58 — Scenario: A fully exercised session directory holds exactly four meta files

**Traces to**: User Story 12, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a session created, then given one `/goal set`, one `/loop` start and one transcript append
- **When** the session directory is listed
- **Then** `meta.json`, `stats.json`, `goal.json` and `loop.json` all exist
- **And** each file contains only its own group's fields

#### BDD-59 — Scenario Outline: Each writer family leaves the other families' bytes untouched

**Traces to**: User Story 12, Acceptance Scenario 2
**Category**: Happy Path

- **Given** all four files present with known contents
- **When** `<operation>` runs
- **Then** `<unchanged_files>` are byte-identical afterwards

**Examples**:

| operation | unchanged_files |
|---|---|
| `/loop` tick | `goal.json`, `meta.json` |
| `/goal` judge round | `loop.json`, `meta.json` |
| transcript append | `goal.json`, `loop.json` |
| status transition | `goal.json`, `loop.json`, `stats.json` |

#### BDD-60 — Scenario: A directory with only `meta.json` loads with zero-valued groups

**Traces to**: User Story 12, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a session directory containing `meta.json` and nothing else
- **When** it is loaded
- **Then** the load succeeds with zero-valued stats, goal and loop

#### BDD-61 — Scenario: A directory with no `meta.json` is an error, not an empty session

**Traces to**: User Story 12, Acceptance Scenario 4
**Category**: Error Path

- **Given** a session directory containing `stats.json` but no `meta.json`
- **When** it is loaded
- **Then** `readUnifiedMeta` returns an error
- **And** `GET /api/v1/sessions/{id}` returns 404

#### BDD-62 — Scenario: A corrupt group file surfaces an error for that group

**Traces to**: User Story 12, Acceptance Scenario 5
**Category**: Error Path

- **Given** a session directory with a present but truncated `goal.json`
- **When** it is loaded
- **Then** an error surfaces for the goal group
- **But** the load does not silently compose a zero-valued goal

#### BDD-63 — Scenario: The wire representation is byte-identical across the split

**Traces to**: User Story 12, Acceptance Scenario 6
**Category**: Happy Path

- **Given** the same logical session state before and after the split
- **When** `UnifiedMeta` is marshalled and the REST session payload is rendered
- **Then** the bytes are identical
- **And** `make verify-contracts` exits 0 with no regeneration

#### BDD-64 — Scenario: The write-funnel doc comments no longer claim a single funnel

**Traces to**: User Story 12, Acceptance Scenario 7
**Category**: Happy Path

- **Given** the merged tree
- **And** a first assertion that **both** comment blocks were located by anchor text (binding rule 4)
- **When** `pkg/session/unified.go:776-785` and `:166-181` are read
- **Then** neither asserts that every mutation path funnels through one whole-document write

#### BDD-94 — Scenario: Interleaved writer families do not clobber unflushed counters in the cache

**Traces to**: User Story 12, Acceptance Scenario 8
**Category**: Edge Case

- **Given** a session with K transcript appends recorded and **no** flush, so K's `Stats.*` deltas exist only in the cached `*UnifiedMeta`
- **When** a `/goal set` round and a `Status` transition both run — each of which today would refresh the whole cache entry via `writeMetaLocked`'s trailing `us.metaCache[sessionID] = meta.Clone()` (`pkg/session/unified.go:798`)
- **And** a flush is then forced
- **Then** `stats.json` equals K's exact deltas — zero lost, zero double-counted
- **And** `goal.json` and `meta.json` each carry their own writer's value
- **But** neither targeted writer replaced the cache entry wholesale, and a `readMetaLocked` cache-miss compose did not overwrite an entry marked dirty `[grill C-5]`

---

#### BDD-65 — Scenario: A burst of appends does not touch the stats file

**Traces to**: User Story 13, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a session whose `stats.json` **already exists on disk from a prior forced flush**, with its mtime and content hash recorded `[grill m-2]`
- **And** a flush interval that has just started
- **When** K transcript appends occur inside that interval
- **Then** `stats.json`'s mtime and content hash are unchanged
- **And** `transcript.jsonl` has exactly K new lines
- **And** — as a **negative control in the same test** — after the interval elapses with no other action, `stats.json` **does** become current, so "unchanged" can never be satisfied by "never written" `[grill C-4]`

#### BDD-93 — Scenario: The periodic flusher converges with no external trigger at all

**Traces to**: User Story 13, Acceptance Scenario 7
**Category**: Happy Path

- **Given** a running store with one session and one transcript append recorded
- **And** **no** other action whatsoever — the store is never closed, no `SetMeta` runs, no `DeleteSession`, no `CloseSession`, and the test drives no manual tick
- **When** more than one flush interval elapses on the real clock (or on an injected fake advanced without invoking the flush directly)
- **Then** `stats.json` on disk is current
- **But** this is the **only** scenario in US-13 that fails when the periodic flusher goroutine is never started `[grill C-4]` — BDD-65 (unchanged bytes), BDD-66 (test-driven flush), BDD-67 (forced points) and BDD-70 (bounded loss) are **all** satisfiable by a store that has flushed nothing

#### BDD-66 — Scenario: The stats file matches the counters exactly after the interval

**Traces to**: User Story 13, Acceptance Scenario 2
**Category**: Happy Path

- **Given** K appends with known token, cost and tool-call deltas
- **When** the flush interval elapses
- **Then** `stats.json` on disk equals the sum of those deltas exactly

#### BDD-67 — Scenario Outline: Each forced flush point leaves the stats file current

**Traces to**: User Story 13, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** pending in-memory counter deltas and no elapsed flush interval
- **When** `<flush_point>` fires
- **Then** `stats.json` is current, and re-opening the store reads back the exact counters

**Examples**:

| flush_point |
|---|
| `SetMeta` carrying a `Status` patch |
| `DeleteSession` |
| `UnifiedStore.Close` |
| child `CloseSession` teardown |

#### BDD-68 — Scenario Outline: Event-driven writes are on disk immediately

**Traces to**: User Story 13, Acceptance Scenario 4
**Category**: Happy Path

- **Given** no flush interval has elapsed
- **When** `<event>` completes
- **Then** `<field>` is readable from disk immediately

**Examples**:

| event | field | file |
|---|---|---|
| `/goal` judge round | `GoalRoundsUsed` | `goal.json` |
| `/loop` tick | `LoopRunCount` | `loop.json` |
| status transition | `Status` | `meta.json` |
| title change | `Title` | `meta.json` |

#### BDD-69 — Scenario: Recency ordering is exact within a live process

**Traces to**: User Story 13, Acceptance Scenario 5
**Category**: Happy Path

- **Given** session `A` streamed, then session `B` streamed, with no flush in between
- **When** `ListSessions` is called
- **Then** `B` sorts ahead of `A`

#### BDD-70 — Scenario: An ungraceful kill loses at most one interval of counters

**Traces to**: User Story 13, Acceptance Scenario 6
**Category**: Edge Case

- **Given** a session streamed continuously across **≥ 2** flush intervals, then left mid-interval with pending counter deltas
- **When** the process is killed without a graceful shutdown and the store is re-opened
- **Then** the counters are behind by at most that interval's appends
- **And** the flushed prefix is **non-zero** — the two-sided bound, so a store that flushed nothing fails `[grill C-4]`
- **And** `transcript.jsonl` is complete

---

#### BDD-71 — Scenario: A hidden delegation is inspectable without verbose chat

**Traces to**: User Story 14, Acceptance Scenario 1
**Category**: Happy Path

- **Given** verbose chat disabled and a completed delegation hidden from the thread
- **When** the drill-down surface is opened by child id
- **Then** the child's transcript is displayed using only `GET /api/v1/sessions/{childID}`

#### BDD-72 — Scenario: The session list paginates

**Traces to**: User Story 14, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a store with more sessions than one page
- **When** `GET /api/v1/sessions` is called with paging parameters
- **Then** a bounded page is returned with a means to fetch the next

#### BDD-102 — Scenario Outline: Every one of the four layers honours the paging parameters

**Traces to**: User Story 14, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a store holding more sessions than one page
- **When** `<layer>` is invoked with a limit smaller than the total
- **Then** it returns at most that many rows and reports how to fetch the next
- **But** it does **not** load the whole set and slice it in memory — the point of the requirement is that the cost is bounded at every layer, not only at the last one

**Examples** (each verified today to take **no** limit or offset):

| layer | site | owner |
|---|---|---|
| store | `UnifiedStore.ListSessions`, `pkg/session/unified.go:1247` | U6 |
| loop | `AgentLoop.ListAllSessions`, `pkg/agent/loop.go:5046` | U9 |
| REST | `restAPI.listSessions`, `pkg/gateway/rest.go:758-812` (reads only `agent_id`, `type`, `include_verifier`) | U18 |
| client | `fetchSessions`, `src/lib/api.ts:1379-1388` | U12 |

#### BDD-73 — Scenario: A wide fan-out does not evict the parent chat from the sidebar

**Traces to**: User Story 14, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a parent chat with 24 child sessions created after it
- **When** the sidebar renders
- **Then** the parent chat is still visible

#### BDD-74 — Scenario: Drill-down filters on the producing session

**Traces to**: User Story 14, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the drill-down view for a child
- **When** it filters frames
- **Then** it filters on `producing_session_id`
- **But** it does not depend on `subagent_message` or `subagent_state`

---

#### BDD-75 — Scenario: The root-delegation cap refuses rather than queues

**Traces to**: User Story 15, Acceptance Scenario 1
**Category**: Error Path

- **Given** `agents.defaults.subturn.max_concurrent = 24` with 24 root-level delegations in flight
- **When** the **25th** root-level delegation is attempted
- **Then** it is refused
- **And** the cap was resolved from `agents.defaults.subturn.max_concurrent` (`pkg/agent/subturn.go:64-69`) directly, as the explicit per-delegation override it is set to here `[operator 4, grill M-7]` `[AMENDED 2026-08-04: originally this clause read "...not from Performance.EffectiveMaxParallelAgents(), which clampParallelExplicit would have capped at 16 (pkg/config/config.go:459-468) and which would make AC-10's stated topology unrunnable". Commit 536b7340 removed clampParallelExplicit's ceiling — EffectiveMaxParallelAgents() is no longer capped at 16 at all, so that clause is no longer true. The scenario's own premise (an explicit 24 override) still resolves and passes unclamped either way — see the top-of-file AMENDMENT note for the full rationale.]`
- **But** it is not queued behind the session-store lock

#### BDD-76 — Scenario: Nested delegation gating is unchanged

**Traces to**: User Story 15, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the root-level gate in effect
- **When** a child-level delegation runs
- **Then** its existing `concurrencySem` behaviour is unchanged

#### BDD-77 — Scenario: The refusal is operator-visible

**Traces to**: User Story 15, Acceptance Scenario 3
**Category**: Error Path

- **Given** a refused root-level delegation
- **When** the refusal surfaces
- **Then** an `ErrorResult` naming the cap is returned to the calling agent
- **And** an `slog.Error` record is emitted naming the cap, the delegating agent and the target — the shape already in the tree at `pkg/tools/delegate.go:1150-1159`
- **But** no separate user-facing notification is required `[operator 6]`

---

#### BDD-108 — Scenario: The root-delegation cap is defined on a default install, with no operator override

**Traces to**: User Story 15, Acceptance Scenario 1
**Category**: Edge Case

`[AMENDED 2026-08-04]` This scenario's original text (below, retained for the historical record) asserted the resolved value is a **seeded 16, independent of `Performance.EffectiveMaxParallelAgents()`, and forbidden from ever passing through it.** Commit 536b7340 invalidated the premise that made that a safe, no-op default (see the top-of-file AMENDMENT note): the seed is **removed**, and the corrected scenario is —

- **Given** a **fresh install** with `agents.defaults.subturn.max_concurrent` **not set by any operator**
- **When** the root-delegation admission gate resolves its cap
- **Then** the resolved value IS `Performance.EffectiveMaxParallelAgents()` — the SAME central, UI-configurable authority every other concurrency gate resolves to, not a fixed number
- **And** the gate is **active** at that value — the (central-value + 1)th concurrent root delegation is refused, not admitted
- **And** a configured value of `< 0` is surfaced as a boot-time configuration error, never as "no gate"; a value of exactly `0` is the valid "unset" case above, not an error

Original v3 text (superseded): *"Then the resolved value is the seeded default of 16 from `pkg/config/defaults.go` (U28) — And the gate is active — the 17th concurrent root delegation is refused, not admitted — And the resolution did not pass through `Performance.EffectiveMaxParallelAgents()` (FR-095) — And a configured value of ≤ 0 is surfaced as a boot-time configuration error, never as 'no gate' — But every v2 test set the key explicitly to 24 `[grill2 M2-1]`, so the configuration every install ships with had zero coverage — and with no seed, `MaxConcurrent == 0` sent the resolver down exactly the branch FR-095 forbids (`pkg/agent/subturn.go:64-69`, verified; `rg SubTurn pkg/config/defaults.go` returns nothing)."*

---

#### BDD-78 — Scenario: Deleting a parent removes every descendant's uploads directory

**Traces to**: User Story 16, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a parent session with descendants at depths 1 and 2 that each uploaded media
- **When** the parent session is deleted
- **Then** `<home>/uploads/<id>/` is gone for every descendant

#### BDD-79 — Scenario: A path-unsafe session id is still rejected

**Traces to**: User Story 16, Acceptance Scenario 2
**Category**: Error Path

- **Given** a session id containing `..` or a path separator
- **When** the uploads directory is resolved
- **Then** the resolver returns no directory and the caller falls back to the ephemeral temp dir

---

#### BDD-80 — Scenario: Every named gate test asserts the new invariant

**Traces to**: User Story 17, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the twelve gate test files named by W22
- **When** the change lands
- **Then** all twelve exist
- **And** each carries the marker comment `// ADR-057-W22-inverted`
- **But** the automated gate asserts **presence plus marker only** `[grill m-3]` — a Go test can verify that another file exists and contains a token; it cannot verify that another test's assertions encode a semantic invariant. "Each asserts the new invariant" is a **review-gate** obligation (FR-072), recorded as such rather than claimed as automated coverage

#### BDD-81 — Scenario: Test inversions land in their own commit

**Traces to**: User Story 17, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the commit history for this change
- **And** a first assertion that the W22 commit was **resolved** and its file list is non-empty (binding rule 4 — "commit not found" is a failure, not a pass)
- **When** the W22 commit is inspected
- **Then** it contains only `*_test.go` files

#### BDD-82 — Scenario: Every test uses distinct parent and child ids

**Traces to**: User Story 17, Acceptance Scenario 3
**Category**: Edge Case

- **Given** any test written or inverted for this spec
- **And** a first assertion that the discovery pass found **≥ 20** fixtures constructing a parent/child id pair (binding rule 4)
- **When** each constructs the parent and child session ids
- **Then** the two values are not equal
- **And** the assertion names which of the two was used

---

#### BDD-83 — Scenario: `follow_up` generation N+1 sees generation N's history

**Traces to**: User Story 18, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** a completed child session that produced assistant output in generation N
- **When** `follow_up` resumes the same `childID` for generation N+1
- **Then** generation N's messages appear in generation N+1's first assembled message list

#### BDD-84 — Scenario: The per-child message ceiling is enforced per direct parent

**Traces to**: User Story 18, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a depth-3 tree where each parent has C children
- **When** each child sends messages up to the per-child ceiling
- **Then** each direct-parent relationship enforces the ceiling independently
- **And** the chat's aggregate equals (C × ceiling), not one shared pool

#### BDD-85 — Scenario: A grandchild's `message_parent` is drained only by its direct parent

**Traces to**: User Story 18, Acceptance Scenario 3
**Category**: Happy Path

- **Given** chat `A`, child `B` and grandchild `D`
- **When** `D` calls `message_parent`
- **Then** `B`'s `delegate action=inbox` drains the message
- **But** `A`'s `delegate action=inbox` does not, and does not return a clean empty success in place of it

#### BDD-86 — Scenario: A 3P child's process group dies with the child

**Traces to**: User Story 18, Acceptance Scenario 4
**Category**: Edge Case

- **Given** an external-CLI child that has spawned its own subprocess tree
- **When** the child is cancelled
- **Then** every PID in the child's process group is gone

#### BDD-87 — Scenario: A restart mid-delegation leaves no orphan directory

**Traces to**: User Story 18, Acceptance Scenario 5
**Category**: Edge Case

- **Given** a parent turn mid-delegation with a persisted child lifecycle record
- **When** the process restarts and the boot sweep runs (`pkg/agent/boot_sweep.go`, owned by U19)
- **Then** the child's lifecycle record is reconciled to a terminal state
- **And** a transcript write attempted against the un-minted child id returns a **non-nil error** from `AppendTranscriptStrict` and creates no directory — asserted positively, not as "no orphan directory was found", which is satisfied by a run that wrote nothing anywhere `[grill C-3, §4 item 8]`

---

#### BDD-107 — Scenario: A child id colliding with an existing session directory fails loudly

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Error Path

- **Given** an existing session directory whose name equals the `childID` the next delegation will mint, containing a `meta.json`, a non-empty `transcript.jsonl` and a distinct `Owner`
- **When** `CreateSessionWithID(childID, …)` runs
- **Then** it returns a non-nil error naming the collision
- **And** the pre-existing directory's `transcript.jsonl`, `meta.json` and `stats.json` are byte-unchanged
- **But** the child does **not** silently adopt that session — `createSessionLocked` calls `os.MkdirAll` (`pkg/session/unified.go:463`), which is idempotent and silent, so without this requirement the adoption is the default behaviour

---

#### BDD-101 — Scenario: The session list returns roots with a child count, never children inline

**Traces to**: User Story 19, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a parent chat with 24 child sessions created after it
- **When** `GET /api/v1/sessions` is called with default paging
- **Then** the parent appears with `child_count == 24`
- **And** zero subordinate sessions appear as top-level rows
- **But** the children are not omitted from the system — they remain reachable, a page at a time, via `parent_session_id`

#### BDD-112 — Scenario: `child_count` and orphan detection cost O(1) per row, not O(all sessions)

**Traces to**: User Story 19, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a store holding `M` sessions of which `R` are roots, with `R ≪ M`
- **When** a page of `n` roots is listed
- **Then** each row's `child_count` and each candidate's orphan status are resolved from the **in-memory parent index** (FR-097) in O(1) per row
- **And** the total work attributable to `child_count` and orphan detection is O(n), **not** O(M)
- **And** the index is updated on create, meta write, delete, eviction and reconcile — asserted by mutating each path and re-reading the count
- **But** v2 required `child_count` per root and orphan-as-root with **no parent index anywhere** `[grill2 C2-2]`: `ParentSessionID` is a brand-new field (FR-008), and U13's parent index is over **lifecycle records**, not sessions, with no FR bridging them — so both obligations were O(all sessions) per page by construction

---

#### BDD-103 — Scenario: Expanding a node returns its direct children only, a page at a time

**Traces to**: User Story 19, Acceptance Scenarios 2 and 3
**Category**: Happy Path

- **Given** a depth-3 tree beneath one chat
- **When** `GET /api/v1/sessions?parent_session_id=<nodeID>` is called with paging parameters at each level in turn
- **Then** each call returns exactly that node's **direct** children, bounded by the page size, ordered by recency
- **And** no call returns a grandchild
- **And** the total request count is O(expanded nodes), not O(all sessions)

#### BDD-113 — Scenario: A legacy store that errors mid-merge yields a partial page, not a broken cursor

**Traces to**: User Story 19, Acceptance Scenario 2
**Category**: Error

- **Given** a shared store plus two legacy per-agent stores, one of which fails to list
- **When** a paged `ListAllSessions` merge runs
- **Then** the page returns the rows the healthy stores supplied
- **And** `partial_errors` names the failing store (sanitized), and the response is still HTTP 200
- **And** `next_cursor` is present and valid — the failure does **not** halt paging or invalidate the cursor
- **And** requesting the next page returns the following window with **no duplicated and no skipped row** relative to the first
- **And** rows with equal `UpdatedAt` are ordered by session id, so repeated calls cannot reorder them (FR-098)
- **But** v2 gave this layer one cross-unit-request line and stated no ordering, no cursor stability rule and no mid-page error behaviour `[grill2 C2-2, M2-10]`, while `pkg/agent/loop.go:5070-5075` already returns `[]error` alongside the rows and `rest.go:795-811` already switches response variants on it

---

#### BDD-104 — Scenario: The sidebar spends its budget on roots and collapses children

**Traces to**: User Story 19, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a parent chat with 24 children created after it
- **When** the sidebar renders
- **Then** the parent chat is present
- **And** its children are collapsed behind an expand affordance and are not counted against the visible-root budget
- **But** the current behaviour — `maxVisible = 9` applied to a flat recency list (`src/components/layout/Sidebar.tsx:456-457`) — evicts the parent, which is the R-9 symptom this story exists to remove

#### BDD-105 — Scenario: Search results nest matching children under their parent and virtualize

**Traces to**: User Story 19, Acceptance Scenario 5
**Category**: Edge Case

- **Given** `SearchModal` open against a store with far more sessions than fit one viewport, where only a **child** matches the query
- **When** results render
- **Then** the matching child appears nested under its parent, with the parent shown for context even though it did not match
- **And** the list is virtualized — the rendered node count is bounded by the viewport, not by the result count
- **But** today the component fetches the full list (`src/components/search/SearchModal.tsx:363`) and renders `groups.map(...)` unwindowed (`:687`), which under D1 grows by every delegated child at every depth

#### BDD-106 — Scenario: An orphaned child is listed as a root, not dropped

**Traces to**: User Story 19, Acceptance Scenario 6
**Category**: Error Path

- **Given** a child session whose parent session has been deleted, so its `ParentSessionID` names a session that no longer resolves
- **When** the session list renders
- **Then** the orphan appears as a **root-level** row
- **But** it is not silently omitted — a session that exists on disk and is unreachable in the UI is R-7's shape with a different surface

#### BDD-111 — Scenario: Per-session token accounting survives roots-only listing

**Traces to**: User Story 19, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a parent chat that delegated to 3 children, where the children consumed the majority of the tokens
- **When** the Usage screen's "By session" tab loads
- **Then** it requests `GET /api/v1/sessions?flat=true&include_verifier=true` (FR-104)
- **And** the response contains **both** the parent and all 3 children as top-level rows
- **And** the sum of per-session token totals equals the pre-change total for the same logical state — **zero spend disappears**
- **And** the default (non-`flat`) listing still returns roots only, so the tree surfaces are unaffected
- **And** supplying `flat=true` together with `parent_session_id` is a 400
- **But** under v2's roots-only response this spend silently vanished from per-session accounting `[grill2 M2-9]`, materially weakening ADR-052's SC-014 — an audit regression with no requirement, no dataset row and no owner, in a screen (`src/components/screens/UsageScreen.tsx:282`) that appeared in no ownership row

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| **Unit** | One function or type against a real `UnifiedStore` / real lifecycle store on `t.TempDir()` | Validates the primitive in isolation. **No spies, no fakes** — the store is real, the files are real |
| **Integration** | Two or more subsystems in one process: a registered turn + a real store; the cancel ladder + real turns; the gateway + a real WS client | Validates that the id actually threaded through, rather than that a function was called |
| **Cross-process** | The test binary re-exec'd as real OS processes, copying `pkg/entity/store_crossprocess_test.go` | The only honest way to assert a durability or lock guarantee that spans processes |
| **E2E** | Real gateway binary + real SPA store + real delegation | Validates the user-visible property (bucket membership, drill-down, sidebar) |

**Build tags are mandatory.** Every Go invocation carries `-tags goolm,stdjson` (prefer `make test`). Without them `pkg/channels/matrix` will not compile and the gateway package fails to build — a missing-tag error that reads like a real failure but is not.

**Do not run the full Go suite in the dev pod.** CI is the authority. At most one narrowly scoped local test: `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/...`. The full suite (especially `pkg/gateway`) OOM-kills this environment.

### Test Implementation Order

Write these before the implementation code. Order is by dependency; the Unit tier for a unit's own primitive comes before any Integration test that consumes it.

> **Every test below goes in a NEW file named `<subject>_adr057_test.go`** (ownership Rule 5). `[grill M-4]` v1 gave U21 exclusive ownership of twelve `*_test.go` files while assigning eight other units tests that naturally live in exactly those files (`subturn_test.go`, `steering_test.go`, `interrupt_by_session_key_test.go`, `cancel_subagent_cascade_test.go`, `cancel_orphan_delegate_test.go`, `orphan_watch_test.go`, `approval_grant_delegation_test.go`) — and simultaneously required those units to write tests **first** and U21 to land **last, in a commit containing no behaviour files**. Those two constraints are mutually exclusive for every such test. New files resolve it: U21 touches only the twelve, everyone else touches only their own new file.
>
> **The `Level` column is a claim about what the test can detect, not a label.** A test marked Integration that constructs its store with a fake is a Unit test that lies. Binding rules 1–4 apply to every row.

| # | Test name | Level | Unit | Traces to BDD | What it verifies |
|---|---|---|---|---|---|
| 1 | `TestAppendTranscriptStrict_UnknownSession_ErrorsAndCreatesNothing` | Unit | U2 | BDD-01 | Non-nil error **and** `os.Stat` IsNotExist on the would-be dir |
| 2 | `TestAppendTranscriptStrict_KnownSession_AppendsExactlyOneLine` | Unit | U2 | BDD-02 | Line count delta of exactly 1 |
| 3 | `TestSessionIDTypes_DoNotInterconvert` | Unit | U1 | BDD-05 | Fixture **located** first (absence = failure), then `go build` on it MUST fail naming both types |
| 4 | `TestCreateSessionWithID_UsesExactIDAndCopiesOwner` | Unit | U2 | BDD-06, BDD-07 | Directory name == supplied id; `meta.Owner` copied from the parent (verified absent from the `UnifiedMeta` literal at `unified.go:448-460`) |
| 5 | `TestTurnTranscriptWriters_SurfaceUnresolvableSession` | Unit | U3 | BDD-03 | 4 writers → counter + WARN each |
| 6 | `TestWebsocketStreamedWrite_SurfacesUnresolvableSession` | Unit | U11 | BDD-03 | `pkg/gateway/websocket.go:4256` |
| 7 | `TestAbandonedTurn_SuppressionIsLoggedAndCountedDelta` | Unit | U3 | BDD-04 | **Renamed.** Asserts the WARN **record** (the new artefact) plus a `AbandonedWritesSuppressed()` **delta of exactly 1**. `[grill C-2]` The old name/assertion was green against the unmodified tree — `turn.go:1297` already increments and `turn_test.go:221` already covers it |
| 8 | `TestStripedSessionLock_ShardIsolation` | Unit | U4 | BDD-57 | Distinct ids map to distinct shards; `Get` is stable per key |
| 9 | `TestCacheMu_NoFilesystemInCriticalSection` | Unit | U4 | BDD-57, BDD-97 | AST gate; asserts **≥ 3 `cacheMu` regions located** before asserting the exclusion |
| 10 | `TestLifecycleFilter_ParentDurableKey_DirectChildrenOnly` | Unit | U13 | BDD-18 | Depth-1 only; grandchild excluded |
| 11 | `TestLifecycleParentIndex_MaintainedInsidePersist` | Unit | U13 | BDD-19 | Index updated under the striped lock; one file read per query |
| 12 | `TestLifecycleDocComments_NoSharedParentChildClaim` | Unit | U13, U14 | BDD-22, BDD-97 | Doc-truth gate; asserts **all 3 blocks located** (`lifecycle.go:225-228`, `:572-575`, `list_jobs_sources.go:311-315`) before asserting content |
| 13 | `TestReadUnifiedMeta_ComposesFourFiles` | Unit | U5 | BDD-58 | All four read and composed |
| 14 | `TestReadUnifiedMeta_MissingGroupFilesAreZeroValue` | Unit | U5 | BDD-60 | Success with zero stats/goal/loop |
| 15 | `TestReadUnifiedMeta_MissingMetaJSONIsError` | Unit | U5 | BDD-61 | Error, not empty session — asymmetry asserted in both directions |
| 16 | `TestReadUnifiedMeta_CorruptGroupFileErrors` | Unit | U5 | BDD-62 | Truncated `goal.json` → error for that group |
| 17 | `TestMetaWriters_WriterIsolationByteLevel` | Unit | U5 | BDD-59, BDD-97 | Asserts all 4 files exist, non-zero, with distinct hashes **first**; then each op leaves the others byte-identical |
| 18 | `TestUnifiedMetaMarshal_ByteIdenticalAcrossSplit` | Unit | U5 | BDD-63 | Golden-bytes comparison |
| 19 | `TestMetaDocComments_NoSingleFunnelClaim` | Unit | U5 | BDD-64, BDD-97 | Doc-truth gate; both blocks (`unified.go:776-785`, `:166-181`) **located first** |
| 20 | `TestStatsThrottle_NoFileWriteWithinInterval` | Unit | U6 | BDD-65 | Pre-existing `stats.json` from a forced flush; mtime + content hash unchanged; transcript grows; **negative control in the same test**: after the interval it *does* become current `[grill C-4, m-2]` |
| 21 | `TestStatsThrottle_ExactCountersAfterInterval` | Unit | U6 | BDD-66 | Sum equality, no lost/double delta |
| 22 | `TestStatsThrottle_ForcedFlushPoints` | Unit | U6 | BDD-67 | 4 flush points, each independently |
| 23 | `TestEventWrites_NotThrottled` | Unit | U6 | BDD-68 | goal/loop/status/title on disk immediately |
| 24 | `TestListSessions_RecencyExactInProcess` | Unit | U6 | BDD-69 | In-memory `UpdatedAt` bump orders correctly |
| 25 | `TestApprovalGrants_InheritFromTwoKeys` | Unit | U17 | BDD-31, BDD-32, BDD-88 | **Renamed and rewritten `[grill C-1]`.** Distinct source/destination session ids; asserts the grant was **absent** under the destination key beforehand, resolves under it after, and **still** resolves under the source (copy, not move) |
| 26 | `TestSessionUploadsDir_RejectsUnsafeID` | Unit | U20 | BDD-79 | Existing `("", false)` contract preserved |
| 27 | `TestInterruptScope_RequiredByCompiler` | Unit | U8 | BDD-45, BDD-97 | Fixture **located** first (absence = failure), then `go build` MUST fail |
| 28 | `TestRoutingSessionID_RootEqualsOwnSessionID` | Unit | U3 | BDD-12 | Root behaviour byte-identical |
| 29 | `TestRoutingSessionID_ConsumerSetIsClosed` | Unit | U3 | BDD-17, BDD-97 | Asserts it enumerated **≥ 10** reads (7 role-B + 3 pre-arm, each derived by site) **plus exactly the WS-stamping count recorded in the W5 audit artefact** (FR-089), before asserting none is outside the set. "Read" = AST `SelectorExpr`, defined normatively in "Three reads, five sites" `[grill2 M2-8]` — an enumerator counting identifier occurrences will not reproduce K and is broken, not lenient. `[grill C-3]` A silently empty enumeration leaves the entire D2 safety property unenforced while reporting green |
| 30 | `TestRecentActivityLines_LogsEmptyPath` | Unit | U14 | BDD-51 | Empty path logged |
| 31 | `TestDelegateTaskMaps_BoundedAfterNCompletions` | Unit | U14 | BDD-52 | **Renamed `[grill M-10]`.** N ≫ C terminal tasks past TTL → `len(t.tasks) ≤ C` and `len(t.sessionIndex) ≤ C`; a task within TTL is retained |
| 32 | `TestSpawnSubTurn_ChildOwnsRealSession` | Integration | U7 | BDD-06 | `meta.json` exists at `<baseDir>/<childID>` after a real spawn. File: `pkg/agent/subturn_adr057_test.go` (NOT `subturn_test.go` — U21's) |
| 33 | `TestSpawnSubTurn_NoHistoryFlagRemoved` | Integration | U7 | BDD-09 | Options carry no `NoHistory`; `TranscriptSessionID == childID` |
| 34 | `TestSpawnSubTurn_OwnerInheritedAndInstalled` | Integration | U7, U9 | BDD-07, BDD-08 | `WithSessionOwner` installs; entity stamped with parent's owner |
| 35 | `TestChildMeta_ParentSessionIDAndSubordinateType` | Integration | U5, U7 | BDD-10 | Depth-2 names depth-1, not the chat |
| 36 | `TestDelegateControls_ResolveByChildID` | Integration | U7, U14 | BDD-11 | All six actions resolve on the same id |
| 37 | `TestPreArmLatch_KeysSetAndClearedMatch` | Integration | U15 | BDD-13 | Same keys set and cleared; child consumes a pre-arrival Stop |
| 38 | `TestCancel_PhaseB_HardAbortsLiveChild` | Integration | U15 | BDD-23 | Real root + real critical child + real Stop |
| 39 | `TestCancel_PhaseC_DetachesSurvivingChild` | Integration | U15 | BDD-24 | Detach fires |
| 40 | `TestCancel_AuditNamesDescendants` | Integration | U15 | BDD-25 | `descendants_canceled` non-empty and names the child |
| 41 | `TestOrphanWatchdog_DefersWhileCriticalDelegateAlive` | Integration | U15 | BDD-26 | Fire predicate condition 2 holds |
| 42 | `TestOrphanWatchdog_FiresAfterDelegateFinishes` | Integration | U15 | BDD-27 | Reaps once clear |
| 43 | `TestCancel_KillsChildShellsNotSiblings` | Integration | U15, U16 | BDD-28 | Real PIDs; sibling survives |
| 44 | `TestDelegateCancel_KillsThatChildsShells` | Integration | U14, U16 | BDD-29 | Real PID gone via the delegate path |
| 45 | `TestCancel_TransitionsEveryDescendantLifecycleRecord` | Integration | U15, U13 | BDD-30 | Depth 3, all records `cancelled` |
| 46 | `TestPendingApproval_CancelledByChatStop` | Integration | U17, U11 | BDD-33 | Registry entry gone, timer stopped, goroutine unblocked |
| 47 | `TestChildCloseSession_EvictsGrantsToolsAndRecallSpans` | Integration | U17 | BDD-34 | All three evicted |
| 48 | `TestDelegationRefusedWithoutLifecycleStore` | Integration | U14, U19 | BDD-20 | Operator-visible refusal, no child session created |
| 49 | `TestChildReachableWithBlankParentAgentID` | Integration | U13, U14 | BDD-21 | Walk still reaches it; Stop cancels it |
| 50 | `TestOwnershipWalk_SiblingRejectedAncestorAllowed` | Integration | U14 | BDD-41, BDD-42 | Sibling rejected, root-over-grandchild allowed |
| 51 | `TestOwnershipWalk_DepthBounded` | Integration | U14 | BDD-43 | Terminates at the bound |
| 52 | `TestOwnershipWalk_AllSixGatedActions` | Integration | U14 | BDD-44 | Six sites, both directions |
| 53 | `TestInterrupt_SubtreeAtChildSparesParentAndSibling` | Integration | U8 | BDD-46 | The new-invariant assertion, in `pkg/agent/steering_adr057_test.go`. U21 separately inverts `interrupt_by_session_key_test.go` `[grill M-4]` |
| 54 | `TestInterrupt_SubtreeAtChatReachesAllDepths` | Integration | U8 | BDD-47 | Three depths |
| 55 | `TestDelegateStatus_SyncAndAsyncSnapshotsNonEmpty` | Integration | U14 | BDD-49, BDD-50 | `executeSync` now registers state |
| 56 | `TestChildTranscriptCompleteAndParentClean` | Integration | U7, U18 | BDD-36 | **Renamed and merged `[grill M-9]`.** One run, both files: parent gains **zero** child entries **and** `<baseDir>/<childID>/transcript.jsonl` gains exactly N with expected content. The parent-only half is satisfied by a child that wrote nothing |
| 57 | `TestReadBoundaries_ReturnChildTranscriptUnfiltered` | Integration | U18 | BDD-37 | All four boundaries |
| 58 | `TestIsDelegateChildEntry_ZeroNonTestReferences` | Integration | U5, U18 | BDD-35, BDD-97 | Go-source-only gate; asserts **≥ 60** non-test Go `ParentSpawnCallID` references first (measured 73). Also fails if U5's compile shim survived U18 (hard ordering 6) |
| 59 | `TestChildEntries_RetainParentSpawnCallID` | Integration | U7, U18 | BDD-38 | Provenance retained with a named reader |
| 60 | `TestVerifierWindow_OwnSessionEntriesOnly` | Integration | U18 | BDD-39 | Adjudicated session only |
| 61 | `TestPreCutoverSession_ShowsPreviouslyHiddenNarration` | Integration | U18 | BDD-40 | R-16 asserted as the accepted outcome, not as a bug |
| 62 | `TestUploadsCascadeDeleteAcrossDescendants` | Integration | U20, U18 | BDD-78 | Depths 1 and 2 both removed |
| 63 | `TestRootDelegationAdmission_RefusesNotQueues` | Integration | U19 | BDD-75, BDD-77 | N+1 refused, operator-visible |
| 64 | `TestNestedDelegationGating_Unchanged` | Integration | U19 | BDD-76 | `concurrencySem` behaviour preserved |
| 65 | `TestBootSweep_ReconcilesChildAcrossRestart` | Integration | U13, U19 | BDD-87 | Record reconciled to terminal **and** a write against the un-minted child id returns a **non-nil error** — asserted positively, not as "no orphan dir found" |
| 66 | `TestMessageParent_DrainedByDirectParentAtDepth3` | Integration | U14 | BDD-85 | Producer and consumer agree (AC-16) |
| 67 | `TestPerChildMessageCeiling_IsPerDirectParent` | Integration | U14 | BDD-84 | Aggregate is (children × ceiling), asserted (AC-15) |
| 68 | `TestFollowUpResume_SeesPreviousGeneration` | Integration | U7 | BDD-83 | Intended behaviour pinned (AC-11) |
| 68a | `TestExternalCLIChild_ProcessGroupDies` | Integration | U14, U16 | BDD-86 | Real PIDs in the child's process group, all gone (AC-17c) |
| 69 | `TestCrossProcess_ConcurrentSessionWritesDoNotLoseUpdates` | Cross-process | U4 | BDD-53 | Re-execs the binary as real OS processes |
| 70 | `TestStoreSharding_SlopeNotDoubling` | Cross-process | U4 | BDD-53 | Asserts the **slope** at N and 2N against a pre-change baseline; no machine constant |
| 71 | `TestListSessions_DoesNotBlockOnUnrelatedCreate` | Integration | U4 | BDD-54 | Real fsyncs in flight |
| 72 | `TestStreamingUnaffectedByForeignSessionCreate` | Integration | U4 | BDD-55 | **Concrete assertion `[grill M-7, operator 3]`:** A's median inter-token interval during B's create is within the **slope** bound of its interval with no concurrent create, baselined on the pre-change store. No millisecond constant |
| 73 | `TestStoreConcurrency_RaceClean` | Integration | U4 | BDD-56 | `-race`; `ClearAll`/`RetentionSweep` interleaved |
| 74 | `TestStatsThrottle_UngracefulKillBoundedLoss` | Cross-process | U6 | BDD-70 | Real SIGKILL, real re-open |
| 75 | `TestFrameContract_BothIDsRoundTrip` | E2E | U10, U11, U12 | BDD-16 | All 19 session-scoped types |
| 76 | `TestDelegationSpanAndStepsShareBucket_LiveConnection` | E2E | U11, U12 | BDD-14 | Real gateway; SPA store bucket membership; miss diagnostic never fires |
| 77 | `TestDelegationSpanAndStepsShareBucket_AfterReconnect` | E2E | U11, U12 | BDD-15 | Reconnect case |
| 78 | `TestDrillDownReachableWithoutVerboseChat` | E2E | U12, U18 | BDD-71, BDD-74 | Only `GET /api/v1/sessions/{childID}` |
| 79 | `TestSessionListPaginates` | E2E | U12, U18 | BDD-72 | All four layers |
| 80 | `TestSidebarRetainsParentUnderWideFanOut` | E2E | U12 | BDD-73 | 24 children, parent still shown |
| 81 | `TestGateTestsInvertedNotDeleted` | Unit | U21 | BDD-80, BDD-97 | **Scoped `[grill m-3]`:** all twelve present **and** each carries `// ADR-057-W22-inverted`. Semantic-invariant verification is a review gate (FR-072), not this test |
| 82 | `TestW22CommitContainsOnlyTests` | Unit | U21 | BDD-81, BDD-97 | Resolves the commit and asserts a **non-empty** file list first; "commit not found" is a failure |
| 83 | `TestAllFixturesUseDistinctParentChildIDs` | Unit | U21 | BDD-82, BDD-97 | Asserts **≥ 20 fixtures discovered** first, then all pairs distinct. Closes the `message_parent_real_context_test.go:16-17` hole |

#### Tests added in v2

`[grill C-1…C-6, M-6…M-14; operator 1–4]` Each row names the finding it closes. Numbering continues from #83; no existing number was reused.

| # | Test name | Level | Unit | Traces to BDD | What it verifies | Closes |
|---|---|---|---|---|---|---|
| 84 | `TestApprovalGrants_InheritFrom_SourceMissIsNotSilent` | Unit | U17 | BDD-89 | Empty source set → log record naming both keys + counter increment; **not** a bare `return` | C-1 |
| 85 | `TestApprovalGrants_InheritFrom_SelfDelegationUnion` | Unit | U17 | BDD-88 | Same agent, **different** sessions: union at the destination, source untouched (dataset rows 3–4) | C-1 |
| 86 | `TestApprovalRegistry_EntryCarriesActingSessionID` | Integration | U17, U11 | BDD-90 | A child's pending entry's `SessionID` is the child's, not the chat's (`approvals.go:85`) | M-6 |
| 87 | `TestApprovalRoundTrip_ChildApprovedResolvesByApprovalID` | Integration | U17, U11, U12 | BDD-91 | Client **approves**; resolution is by approval id, so the routing-key change cannot break it | M-6 |
| 88 | `TestCreateSessionWithID_NeverHoldsTwoSessionShards` | Unit | U2, U4 | BDD-92 | Instrumented lock wrapper records acquire/release order; parent shard released before child's; `ClearAll` in index order | C-6 |
| 89 | `TestStatsThrottle_UnforcedFlushConverges` | Unit | U6 | BDD-93 | One append, **no** other action, > 1 interval → `stats.json` current. The only test that fails on a dead flusher | C-4 |
| 90 | `TestStatsCache_FieldGroupIsolationUnderInterleavedWriters` | Unit | U6 | BDD-94 | K appends unflushed + `/goal set` + `Status` → forced flush yields exactly K's deltas; no wholesale cache replace | C-5 |
| 91 | `TestNegativeGates_AssertPositiveLowerBounds` | Unit | U21 | BDD-97 | Meta-gate: **iterates the bounds table** (currently **13** rows, not a hardcoded eleven) and asserts each gate goes **red** when its search target is deliberately broken. **Mechanism specified** `[grill2 M2-5]`: mutations are applied to a `t.TempDir()` **copy** of only the gate's own package — never in place, because this is a shared working tree — and each re-invocation is scoped to that package with `-run '^<GateTest>$'`. `pkg/gateway` gates are **excluded from local execution** and run in CI only (CLAUDE.md forbids local `pkg/gateway` suites; 13 in-place rebuilds would OOM the dev pod). If the temp-copy approach proves unaffordable at implementation time, #91 downgrades to a recorded **review gate** the way FR-072's semantic half did — and that downgrade must be written into SC-035, not decided silently in a test file | C-3 |
| 92 | `TestListSessions_ConcurrentDeleteConsistency` | Integration | U6 | BDD-95 | Delete interleaved between reconcile and snapshot; stated model honoured; no panic/deadlock/partial meta | M-14 |
| 93 | `TestDelegateTaskMaps_RetainWithinTTL` | Unit | U14 | BDD-52 | The complement of #31: a terminal task **within** TTL is **not** evicted, so eviction cannot break `action=status` | M-10 |
| 94 | `TestChildCloseSession_FiresOnEveryTerminalState` | Integration | U7, U17 | BDD-96 | completed / cancelled / failed / abandoned each invoke `CloseSession` from the child-turn terminal path | M-13 |
| 95 | `TestFrameContract_ProducingIDAbsentForClassB` | E2E | U10, U11, U12, U23 | BDD-98 | Root- and gateway-produced types omit `producing_session_id` | M-8 |
| 96 | `TestFrameContract_DocumentedGapsAssertedForClassC` | E2E | U10, U11, U23 | BDD-99 | `rate_limit` and `replay_done` behave exactly as documented; the W5 audit artefact records both | M-8, operator 11 |
| 97 | `TestCancel_AuditNamesEveryDescendantAtDepth3` | Integration | U15, U13 | BDD-100 | `descendants_canceled` contains all three ids — **non-holdout** | M-12 |
| 98 | `TestSessionList_RootsOnlyWithChildCount` | Integration | U18 | BDD-101 | 1 parent + 24 children → 1 row, `child_count == 24`, zero children inline | operator 1 |
| 99 | `TestSessionList_ExpandReturnsDirectChildrenPaged` | Integration | U18, U13 | BDD-103 | `?parent_session_id=…&limit=n` returns direct children only, a page at a time, at every depth | operator 1 |
| 100 | `TestSessionListLayers_EachHonoursPaging` | Integration | U6, U9, U18, U12 | BDD-102 | **Rewritten `[grill2 C2-2]`** into three checkable assertions, replacing "none loads-all-then-slices" — which was either impossible to satisfy or would have been written as `len(rows) <= limit`, which the forbidden implementation satisfies exactly. (a) **Window correctness + stability:** each of the four layers returns exactly the requested window of the recency-ordered sequence, and two identical calls with no intervening write return byte-identical windows (catches `limit` ignored and non-deterministic map ordering). (b) **Boundary cost is O(page):** the REST response body carries exactly ≤ `limit` rows and its serialised size scales with `limit`, not with total session count, measured at two population sizes. (c) **Zero per-session disk reads** on a warm-cache paged call, via FR-103's read seam (shared counter with #103) — catches an implementation that re-reads meta files per page | M-1 |
| 101 | `TestSidebarTree_ParentSurvivesWideFanOut` | E2E | U24 | BDD-104 | 24 children, parent present, children collapsed, root budget unspent on children | operator 1 |
| 102 | `TestSearchModalTree_NestedAndVirtualized` | E2E | U24 | BDD-105 | Child-only match shows its parent for context; rendered node count bounded by viewport | operator 1 |
| 103 | `TestMetaCache_HitCostsZeroDiskReads` | Unit | U5 | BDD-58 | Instrumented FS counter: a `GetMeta`/`ListSessions` cache hit performs **zero** reads after the split | M-11 (FR-058) |
| 104 | `TestMigrateLegacy_BytesUnchanged` | Unit | U5 | BDD-61, BDD-97 | Golden-bytes gate on `migrateLegacy`/`writeUnifiedMetaDirect` output; also asserts no pre-split fused reader exists. **Bounded `[grill2 M2-5]`:** MUST first assert ≥ 1 located, **non-empty** golden fixture and that **both** named symbols resolved, before either clause — otherwise the "no fused reader" half passes whenever the search is wrong | M-11 (FR-060) |
| 105 | `TestFlushInterval_ConfigKeyDefaultAndOverride` | Unit | U6 | BDD-93 | The key exists, defaults to **5 s**, and a non-default value is honoured end to end | M-11 (FR-067), operator 2 |
| 106 | `TestParentageWalk_NeverReadsOwnerScopeIDOrParentAgentID` | Unit | U13, U14 | BDD-18, BDD-21, BDD-97 | Static gate: **zero** reads of `OwnerScopeID` or `ParentAgentID`. **Bounded and scoped `[grill2 M2-5]`:** "the walk's code path" is the explicit symbol set `verifyCallerOwnsSession` + `callerOwnerKey` + `LifecycleFilter.matches` and their transitive callees within `pkg/tools` and `pkg/session`; the test MUST first assert it resolved all **3** symbols, walked **≥ 1** callee edge and located **≥ 1** `ParentDurableKey` read. This is the **sole** coverage of FR-023, a security property — unbounded, it reported green over an empty symbol set | M-11 (FR-023) |
| 107 | `TestStoreSharding_NoFixedConcurrencyCapInDesign` | Cross-process + Unit | U4 | BDD-53, BDD-97 | **Renamed and rewritten `[grill2 M2-4]`.** The v2 assertion ("throughput still rises 64 → 128") is **false by construction**: at N=128 over 64 shards the pigeonhole gives ≥ 2 sessions per shard, so contention at least doubles while the work inside each shard is fsync-bound (`pkg/fileutil/file.go:97` **and** `:121`) — the spec's own edge case and dataset row 5 both concede it. Now: (a) a **static gate** asserting no constant in the store's write path bounds concurrent session writers (no semaphore, no worker pool, no fixed-size gating channel over `NewSession`/`AppendTranscript`/`SetMeta`), with a positive lower bound of ≥ 1 located write-path function; (b) the existing 2N-vs-N slope at a box-saturating N (dataset rows 3–4). No machine-specific throughput promise | M-11 (FR-052) |
| 108 | `TestOrphanSession_ListedAsRoot` | Integration | U18 | BDD-106 | A child whose parent was deleted appears as a root row, not omitted | operator 1 |
| 109 | `TestDrillDown_NoSubagentMessageOrStateReferences` | Unit | U12, U24 | BDD-74, BDD-97 | Static gate over `src/`: ≥ 1 `producing_session_id` reference located first, then **zero** non-test references to `subagent_message`/`subagent_state` | M-11 (FR-047) |
| 110 | `TestRootDelegationCap_SourcedFromSubTurnMaxConcurrent` | Unit | U19 | BDD-75 | The resolved cap came from `agents.defaults.subturn.max_concurrent` (unclamped) and **not** from `Performance.EffectiveMaxParallelAgents()`; an operator-set 24 survives | operator 4, M-7 |
| 111 | `TestCreateSessionWithID_RejectsCollidingDirectory` | Unit | U2 | BDD-107 | A pre-existing directory at the target id → loud failure; the child never adopts its transcript/meta/owner/stats | STRIDE: tampering |

#### Tests added in v3

`[grill2 C2-1…C2-4, M2-1, M2-3, M2-7, M2-9]` Each row names the finding it closes. Numbering continues from #111; no existing number was reused.

| # | Test name | Level | Unit | Traces to BDD | What it verifies | Closes |
|---|---|---|---|---|---|---|
| 112 | `TestRootDelegationCap_DefaultInstallInheritsCentralValue` `[AMENDED 2026-08-04, renamed from TestRootDelegationCap_DefaultInstallIsGatedAt16 — deliberately inverted, see top-of-file AMENDMENT note]` | Unit | U19, U28 | BDD-108 | **No key set anywhere**: the resolved cap now IS `EffectiveMaxParallelAgents()` (the central authority), the gate is **active** at that value (the next-past-cap admission refused). A `< 0` configured value is a boot error; `0` is the valid unset case, not an error. Original (superseded) assertion: "the resolved cap is the seeded 16 ... and resolution did not pass through `EffectiveMaxParallelAgents()`" | M2-1 |
| 113 | `TestTranscriptMutate_MissingSessionIsLoggedAndCounted` | Unit | U22 | BDD-109 | `mutateToolCallInTranscript` against (a) a session with no `meta.json` and (b) an existing session with an unresolvable `callID` → WARN + counter delta each, distinguishable in the record; never a bare `false` | M2-3 |
| 114 | `TestListSessions_PrunesOutOfBandDeletedDirectory` | Unit | U6 | BDD-110 | `os.RemoveAll` a session directory behind the store's back → the next `ListSessions` drops it from the result **and** from `metaCache` and the parent index; a `cacheLoadFailures` exclusion is left undisturbed | M2-7 |
| 115 | `TestUsageAccounting_FlatListingRetainsChildSpend` | E2E | U12, U18 | BDD-111 | Parent + 3 children where children hold most of the spend: `flat=true` returns all 4 rows and the per-session total equals the pre-change total; default listing still returns roots only; `flat=true` + `parent_session_id` → 400 | M2-9 |
| 116 | `TestSessionParentIndex_ChildCountIsConstantPerRow` | Unit | U4, U6 | BDD-112 | `child_count` and orphan status resolve from the in-memory parent index in O(1) per row; the index is correct after create, meta write, delete, eviction and reconcile — each mutated and re-read | C2-2 |
| 117 | `TestListAllSessions_PartialErrorPagingContract` | Integration | U9, U18 | BDD-113 | One legacy store errors mid-merge → rows still returned, `partial_errors` populated, HTTP 200, `next_cursor` valid; the next page neither duplicates nor skips a row; equal `UpdatedAt` ties break on session id across repeated calls | C2-2, M2-10 |
| 118 | `TestInterruptAPI_NoStaleDocReferences` | Unit | U8 | BDD-114, BDD-97 | Doc-truth gate: ≥ 1 reference to the collapsed entry point located **first**, then zero surviving references — code or comment — to the four retired names across non-test Go | C2-4 |

**Test count**: 119 entries — #1 … #118 plus #68a. v2 had 112; v1 had 84.

### Test Datasets

#### Dataset: `AppendTranscriptStrict` session-id resolution

| # | Input session id | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `""` | Empty | Error from `validateSessionID` (pre-existing, `pkg/session/unified.go:803-805`) | BDD-01 | Already loud today; must stay loud |
| 2 | Fresh UUID, never created | Missing entity | Non-nil error **and no directory created** | BDD-01 | The core R-7 case |
| 3 | Existing session id | Valid representative | nil; one new line | BDD-02 | Happy path |
| 4 | Existing session **directory** with no `meta.json` | Corrupted state | Non-nil error | BDD-01, BDD-61 | The D11 asymmetry meets W3 |
| 5 | `"../escape"` | Injection | Error from `validateSessionID` | BDD-01 | Path traversal |
| 6 | `".hidden"` | Special name | Error from `validateSessionID` | BDD-01 | Leading-dot reject |
| 7 | Id of a session deleted between resolve and append | Race: create/delete | Non-nil error, no re-creation | BDD-01 | Must not resurrect the directory |

#### Dataset: `readUnifiedMeta` file-composition matrix

| # | Files present | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | all four | Valid representative | Composed meta, all groups populated | BDD-58 | Happy path |
| 2 | `meta.json` only | Min | Success; stats/goal/loop zero-valued | BDD-60 | A session that never ran a goal |
| 3 | `meta.json` + `stats.json` | Partial | Success; goal/loop zero-valued | BDD-60 | Common streaming session |
| 4 | no `meta.json`, others present | Missing required | **Error**; REST 404 | BDD-61 | Inverting this re-opens R-7 |
| 5 | none | Empty | Error | BDD-61 | "This session does not exist" |
| 6 | all four, `goal.json` truncated mid-object | Corrupted payload | Error for the goal group | BDD-62 | Not a silent zero goal |
| 7 | all four, `stats.json` truncated | Corrupted payload | Error for the stats group | BDD-62 | Same rule, different group |
| 8 | all four, `loop.json` = `{}` | Empty object | Success; zero-valued loop | BDD-60 | Valid empty vs corrupt is distinguishable |
| 9 | `meta.json` + a stale extra file | Unexpected content | Success; extra ignored | BDD-58 | Forward tolerance |

#### Dataset: `UpdatedAt` composition and recency

| # | `meta.json` UpdatedAt | `stats.json` UpdatedAt | Boundary type | Expected composed value | Traces to |
|---|---|---|---|---|---|
| 1 | `T` | absent | Missing group | `T` | BDD-60 |
| 2 | `T` | `T+5s` | Later in stats | `T+5s` | BDD-69 |
| 3 | `T+5s` | `T` | Later in meta | `T+5s` | BDD-69 |
| 4 | `T` | `T` | Equal | `T` | BDD-69 |
| 5 | zero time | `T` | Epoch / zero | `T` | BDD-69 |

#### Dataset: Ownership-walk topology

| # | Caller | Target | Boundary type | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | chat `A` | child `B` | Direct parent | Permit | BDD-44 |
| 2 | chat `A` | grandchild `D` | Ancestor, depth 2 | Permit | BDD-42 |
| 3 | child `B` | sibling `C` | Sibling | **Reject** | BDD-41 |
| 4 | grandchild `D` | cousin `E` | Cousin | **Reject** | BDD-41 |
| 5 | child `B` | own child `D` | Direct parent | Permit | BDD-44 |
| 6 | child `D` | ancestor `A` | Inverted direction | **Reject** | BDD-41 |
| 7 | node at depth `maxDepth+1` | root | Max + 1 | **Reject** at the bound | BDD-43 |
| 8 | caller with empty owner key | any | Empty | **Reject** (existing behaviour, `pkg/tools/delegate.go:1975`) | BDD-41 |
| 9 | target with empty `ParentDurableKey` | — | Empty | **Reject** (existing behaviour) | BDD-41 |

#### Dataset: Stats-throttle timing

> Every "unchanged" row below requires `stats.json` to **already exist from a prior forced flush** `[grill m-2]`, and every "unchanged" assertion is paired with the negative control in BDD-65. Rows 1–3 and 7 were all trivially satisfiable in v1 by a store that never wrote `stats.json` at all `[grill C-4]`.

| # | Appends | Elapsed vs flush interval | Boundary type | Expected `stats.json` | Traces to |
|---|---|---|---|---|---|
| 1 | 0 | 0 | Zero | unchanged (file pre-exists) | BDD-65 |
| 2 | 1 | < interval | One | unchanged (file pre-exists) | BDD-65 |
| 3 | 1000 | < interval | Very large burst | unchanged (file pre-exists) | BDD-65 |
| 4 | 1 | ≥ interval | Min above bound | exact counters | BDD-66 |
| 5 | 1000 | ≥ interval | Large + bound | exact counters, no double-count | BDD-66 |
| 6 | K | forced flush before interval | Alternate trigger | exact counters | BDD-67 |
| 7 | K spread over **≥ 2** intervals | SIGKILL mid-interval | Resource loss | **two-sided:** shortfall ≥ 0 **and** ≤ the final interval's appends, **and** the flushed prefix is strictly **> 0** | BDD-70 |
| 7a | K | SIGKILL **within** the first interval | Min resource loss | shortfall MAY be all K; transcript complete | BDD-70 |
| 8 | K on session A, 0 on session B | ≥ interval | Dirty-set selectivity | only A's file rewritten | BDD-59 |
| 9 | 1, then **no action of any kind** | > interval, real/advanced clock | **Unforced convergence** | current — this row fails iff the periodic flusher was never started | BDD-93 |
| 10 | K, then a `/goal set` **and** a `Status` transition, then a forced flush | any | Cross-writer-family interleave | exactly K's deltas; `goal.json` and `meta.json` each carry their own writer's value | BDD-94 |

#### Dataset: Sharding concurrency slope

| # | N concurrent sessions | Boundary type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 1 | Min | baseline wall-clock `T1` | BDD-53 | Establishes the unit |
| 2 | 2 | Small | `< 2 × T1` | BDD-53 | Slope, not a constant |
| 3 | N (box-saturating) | Representative | `TN` recorded | BDD-53 | N chosen to saturate, not fixed by design |
| 4 | 2N | Max | `< 2 × TN` | BDD-53 | **The assertion.** Doubling must not double |
| 5 | N, all colliding on one shard by construction | Adversarial | serialises — documented, not a failure | BDD-53 | FNV collision behaviour is expected |
| 6 | N, pre-change store | Regression baseline | must be beaten by rows 3–4 | BDD-53 | Same box, same filesystem |

#### Dataset: WS frame identity stamping

| # | Producer | `session_id` | `producing_session_id` | Boundary type | Traces to |
|---|---|---|---|---|---|
| 1 | root turn | own id | absent | Root | BDD-12 |
| 2 | depth-1 child | chat id | child id | Direct child | BDD-16 |
| 3 | depth-3 grandchild | chat id | grandchild id | Deep nesting | BDD-16 |
| 4 | self-delegation | chat id | child id | Same agent, different session | BDD-16 |
| 5 | `rate_limit` (no `SessionID` field, `events.go:525-533`) | reconstructed | absent | Pre-existing strain — **class (c)** | BDD-99 |
| 6 | `replay_done` (absent from `WsFrameType` enum; only occurrence tree-wide is `chat.ts:1238`) | routing id | absent | Pre-existing gap — **class (c)** | BDD-99 |
| 7 | gateway replay (`replay_message`) | routing id | absent | Not turn-produced — **class (b)** | BDD-98 |
| 8 | chat lifecycle (`session_started`, `session_close_ack`) | routing id | absent | Not turn-produced — **class (b)** | BDD-98 |
| 9 | parent-emitted span frames (`subagent_start`, `subagent_end`) | routing id | absent | Producer **is** the routing session — **class (b)** | BDD-98 |
| 10 | each of `agent_switched`, `task_status_changed`, `system_overload`, `cancel_stage`, `goal_status`, `loop_status` | per its assigned class | per its assigned class | **Unclassified pending the W5 audit** — FR-089 requires each to be assigned to (a)/(b)/(c) and committed; this spec deliberately does not guess | BDD-16 / BDD-98 / BDD-99 |

#### Dataset: Approval-grant inheritance keys

`[grill C-1]` The whole point is that source and destination differ. A row where they coincide is the trap FR-031 fell into.

| # | Source key | Destination key | Boundary type | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | `{parentSid, parentAgent}` holding `{T}` | `{childSid, childAgent}` empty | Valid representative | `T` resolves under the destination **and** still under the source | BDD-88 |
| 2 | `{parentSid, parentAgent}` **empty** | `{childSid, childAgent}` empty | Missing source | no-op, **logged and counted** | BDD-89 |
| 3 | `{parentSid, agentX}` holding `{T}` | `{childSid, agentX}` — self-delegation | Same agent, different session | union under the destination; source untouched | BDD-88 |
| 4 | `{parentSid, parentAgent}` holding `{T}` | `{childSid, childAgent}` already holding `{U}` | Pre-existing destination | destination holds `{T, U}` — union, not replace (`approvalgrants.go:123-128`) | BDD-88 |
| 5 | `parentSid == childSid` | — | **Degenerate / forbidden** | test fixture MUST NOT construct this; a fixture whose two ids coincide cannot distinguish a working re-key from C-1's no-op | BDD-88, FR-074 |
| 6 | empty `srcSessionID` or empty agent id | — | Empty | no-op, logged and counted (existing guard, `approvalgrants.go:113-115`) | BDD-89 |

#### Dataset: Session-list hierarchy and paging

`[operator 1]`

| # | Store contents | Request | Boundary type | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | 1 chat, 0 children | default paging | Min | 1 row, `child_count == 0` | BDD-101 |
| 2 | 1 chat, 24 children | default paging | Representative fan-out | 1 row, `child_count == 24`, zero children inline | BDD-101 |
| 3 | 1 chat, 24 children | `?parent_session_id=<chat>&limit=10` | Page boundary | exactly 10 direct children + a next cursor | BDD-103 |
| 4 | depth-3 tree | `?parent_session_id=<depth-1 child>` | Nesting | that node's direct children only; zero grandchildren | BDD-103 |
| 5 | more roots than one page | `?limit=<n>` | Max | at most n roots; no page boundary ever splits a parent from its children, because children are never inline | BDD-102 |
| 6 | a child whose parent was deleted | default paging | Orphan / corrupted state | orphan appears as a **root** row | BDD-106 |
| 7 | 0 sessions | default paging | Empty | empty page, HTTP 200, not 404 | BDD-102 |
| 8 | `?parent_session_id=<id that does not exist>` | — | Missing entity | empty page, HTTP 200 — "no children" is not "no such session"; a 404 here would make an orphan indistinguishable from a childless node | BDD-103 |

#### Dataset: Session parent index

`[grill2 C2-2]` FR-097's index is the mechanism `child_count` and orphan detection need. Every row is a mutation of `metaCache` that MUST keep the index correct.

| # | Store mutation | Boundary type | Expected index state | Traces to |
|---|---|---|---|---|
| 1 | Create a root (`ParentSessionID == ""`) | Valid representative | root registered; `child_count(root) == 0` | BDD-112 |
| 2 | Create a child naming that root | Direct child | `child_count(root) == 1`; child not a root | BDD-112, BDD-101 |
| 3 | Create 24 children of one root | Representative fan-out | `child_count(root) == 24`, resolved O(1) | BDD-101, BDD-112 |
| 4 | `SetMeta` on a child that does **not** touch `ParentSessionID` | No-op for the index | counts unchanged | BDD-112 |
| 5 | `DeleteSession` on a child | Removal | `child_count(parent)` decremented; child gone from the index | BDD-112, FR-064 |
| 6 | `DeleteSession` on the **parent** | Orphan creation | each former child is now a **root**; `child_count` of the deleted id no longer resolves | BDD-106, BDD-112 |
| 7 | Directory removed **out of band**, then `ListSessions` | Prune | entry evicted and index updated in the same pass | BDD-110, FR-097a |
| 8 | `CloseSession` evicts a child's `metaCache` entry | Eviction ≠ deletion | the session still exists on disk, so it stays in the index; only the cached meta is dropped | FR-033, Ambiguity 14 |
| 9 | Reconcile discovers an out-of-band **new** directory | Late arrival | added to the index with its parent link, or as a root if `ParentSessionID == ""` | BDD-112 |
| 10 | A child whose `ParentSessionID` names a never-existing id | Corrupted state | treated as an **orphan root**, not dropped and not a dangling edge | BDD-106 |

### Regression Test Requirements

**This change MODIFIES existing behaviour.** The suite is the current contract's specification: ~430 references across ~71 test files touch the shared transcript id (128 `transcriptSessionID` refs across 43 test files alone).

**Behaviours that MUST be preserved exactly:**

| Existing behaviour | Existing test / anchor | New regression test | Why |
|---|---|---|---|
| Root turn WS routing is byte-identical | `pkg/gateway/replay_test.go:1549` | `TestRoutingSessionID_RootEqualsOwnSessionID` | Root is the overwhelming majority of traffic; a root regression is not acceptable collateral |
| Pre-arm latch "inherits verbatim" invariant | `pkg/agent/cancel_async_delegate_repro_test.go` | `TestPreArmLatch_KeysSetAndClearedMatch` | `pkg/agent/cancel_prearm.go:385-389` states the correctness argument explicitly |
| Chat-wide Stop reaches every live descendant | `pkg/agent/cancel_subagent_cascade_test.go:51-101`, `pkg/gateway/cancel_subagent_cascade_test.go:5` | `TestCancel_PhaseB…`, `TestCancel_PhaseC…` | This is what ADR-053's FR-6a amendment was protecting |
| Cancel isolation between unrelated sessions | `pkg/agent/cancel_session_isolation_test.go:12` | `TestCancel_KillsChildShellsNotSiblings` | A broader cascade must not become a wider blast radius |
| ADR-045 watchdog does not reap live critical work | `pkg/agent/orphan_watch_test.go:14,223-229` | `TestOrphanWatchdog_DefersWhileCriticalDelegateAlive` | Silent failure mode; the interlock's whole purpose |
| Orphan-delegate cancellation | `pkg/agent/cancel_orphan_delegate_test.go:57-79` | covered by #38–#42 | Same ladder |
| Steering delivery to a child at its next tool boundary | `pkg/agent/steering_test.go:1693,1765-1811,1865` | `TestDelegateControls_ResolveByChildID` | INV-3; a steer must still land |
| Approval-grant delegation inheritance | `pkg/agent/approval_grant_delegation_test.go:19,229` | **#25 `TestApprovalGrants_InheritFromTwoKeys`** (+ #84, #85) | Availability: the 300 s invisible block. `[grill2 m2-2]` v2 left the **pre-rename** name here after C-1 renamed it "because the old name described the vacuous assertion" — the regression table pointed at a test that no longer exists |
| Per-session token accounting includes delegated children | `src/components/screens/UsageScreen.test.tsx:346-349` | **#115 `TestUsageAccounting_FlatListingRetainsChildSpend`** | `[grill2 M2-9]` Roots-only listing would silently drop child spend from the Usage screen. ADR-052 SC-014 depends on it |
| Subagent transcript nesting on the wire | `pkg/agent/subturn_transcript_nesting_test.go:9-10,93-94` | `TestFrameContract_BothIDsRoundTrip` | Span nesting key is a **different** field from the transcript one |
| Plan cancellation | `StopPlan` (`pkg/agent/plan_engine.go:2044-2135`) | no new test; **assert unchanged** | D9: explicitly out of scope |
| `list_jobs` attribution by `ParentAgentID` | existing `list_jobs` tests | `TestLifecycleFilter_ParentDurableKey_DirectChildrenOnly` (negative half) | A different axis; must not start filtering by session |
| `migrateLegacy` / `writeUnifiedMetaDirect` | existing migration tests | no new test; **assert unchanged** | Different legacy, out of scope |
| `make verify-contracts` clean | CI gate | `TestUnifiedMetaMarshal_ByteIdenticalAcrossSplit` | D11 must not drift the contract |

**Tests that MUST be deliberately inverted, never deleted (W22, U21, own commit):**

| File | Anchor | New assertion |
|---|---|---|
| `pkg/agent/subturn_test.go` | `TestSubTurnInheritsTranscriptSessionID` at `:2095`, equality at `:2143-2145` | The child's transcript session id is its **own**; the **routing** id is inherited |
| `pkg/agent/interrupt_by_session_key_test.go` | `:9-19`, `:232` | One scoped entry point; `ScopeSubtree` at a child spares parent and sibling |
| `pkg/agent/approval_grant_delegation_test.go` | `:19`, `:229` | Grant keyed to the child session |
| `pkg/agent/cancel_orphan_delegate_test.go` | `:57-79` | Cascade via the routing key |
| `pkg/agent/cancel_subagent_cascade_test.go` | `:51-101` | Same |
| `pkg/agent/cancel_session_isolation_test.go` | `:12` | Same |
| `pkg/agent/orphan_watch_test.go` | `:14`, `:223-229` | Interlock via the routing key |
| `pkg/agent/steering_test.go` | `:1693`, `:1765-1811`, `:1865` | Predicates re-based |
| `pkg/agent/subturn_transcript_nesting_test.go` | `:9-10`, `:93-94` | Nesting survives on the span key, not the transcript key |
| `pkg/agent/cancel_async_delegate_repro_test.go` | whole file | Pre-arm race still closed |
| `pkg/gateway/cancel_subagent_cascade_test.go` | `:5` | Gateway-side cascade |
| `pkg/gateway/replay_test.go` | `:1549` | Replay returns unfiltered entries |

**SPA tests that MUST be deliberately inverted, never deleted** `[grill2 M2-9]`. W22/U21's scope is Go-only ("the twelve named `*_test.go` files"), and Rule 5 routes **new** tests to new files while saying nothing about **existing SPA** tests the change breaks — yet SC-034 requires `npx vitest run` to exit 0. Four files assert the current `fetchSessions` call shape and break the moment FR-092/FR-093/FR-094/FR-104 land. They are inverted by the unit that owns the component, in that unit's own commit (not U21's):

| File | Anchor | New assertion | Owner |
|---|---|---|---|
| `src/components/layout/Sidebar.test.tsx` | `:241`, `:244` — `toHaveBeenCalledWith()` (literally "no arguments") | called **with** paging arguments; roots-only; children collapsed | **U24** |
| `src/components/search/SearchModal.test.tsx` | `:158`, `:161` — same | called with paging arguments; results nested and virtualized | **U24** |
| `src/components/screens/UsageScreen.test.tsx` | `:346-349` — `toHaveBeenCalledWith(undefined, undefined, { includeVerifier: true })` | called with `flat: true` **and** `includeVerifier: true`; child rows present (FR-104) | **U12** |
| `src/lib/__adr052__sessionVisibilityParams.test.ts` | 6 cases pinning the exact query string | `limit`/`offset`/`parent_session_id`/`flat` added; `include_verifier`'s existing opt-in semantics preserved unchanged | **U12** |

`src/components/layout/Sidebar.focus-trap.test.tsx:73` and `Sidebar.m5.test.tsx:60` mock `fetchSessions` arity-agnostically (`() => Promise.resolve([])`) and do **not** break — they are listed in U24's owned set so no other unit edits them, and they need no inversion.

**Regression dataset — behaviours a reviewer will otherwise mistake for breakage:**

| # | Input | Previous behaviour | Must now produce | Traces to |
|---|---|---|---|---|
| 1 | Pre-cutover session with delegate narration | narration hidden | narration **visible** | BDD-40 (accepted, R-16) |
| 2 | Parent reload after a delegation | parent's LLM context absorbed delegate narration | context **excludes** it | Regression: hydration (m-4) |
| 3 | `delegate action=cancel` on child B | cancels B's turn only, leaves B's children and shells | cancels B's **subtree** and kills B's shells | BDD-29, BDD-46 (R-13) |
| 4 | Grandchild `message_parent` | routed to the chat's inbox | routed to the **direct parent's** inbox | Regression: ADR-053 D16 (AC-16) |
| 5 | Per-child message ceiling in a chat | one shared pool | (children × ceiling) | Regression: ADR-053 D15 (AC-15) |
| 6 | Audit `session_id` for a child's action | the chat id | the **acting** session id | Regression: audit attribution |
| 7 | Child's loaded-tool manifest | inherited the parent's bucket | starts empty | Regression: token/latency cost per delegation |
| 8 | `follow_up` generation N+1 | no history | sees generation N's history | Regression: TDD #68 → AC-11 (intended, R-11) |

---

## Functional Requirements

### Strict transcript primitive and named types (W3, W20)

- **FR-001**: The system MUST provide `AppendTranscriptStrict`, which returns a non-nil error for a session id with no `meta.json` and MUST NOT create any directory for it.
- **FR-002**: `UnifiedStore.AppendTranscript` (`pkg/session/unified.go:802`) MUST **itself become strict** — the `slog.Warn` + `return nil` branch after a failed meta read (`:819-823`) is **deleted**, and the method returns that error. **No lenient form survives anywhere in the tree.** `[grill2 C2-3 — MEANING CHANGED from v2.]` v2 kept the lenient primitive and gave it "a strict **sibling**", converting five named call sites. That contradicts **AC-1's frozen governing text**, which states the property of `AppendTranscript` itself: *"`AppendTranscript` against a UUID with no `meta.json` returns a non-nil error and creates **no** directory."* Under a sibling design AC-1 holds only for the converted callers, and a `task_executor`, `/goal` or `hand_off` write against an unminted session still silently creates — the exact mechanism US-1's opening section calls "the canonical mechanism" of silent failure. `AppendTranscriptStrict` (FR-001) is retained as the explicitly-named entry point on `unified_api.go` for callers converted in W3; it is a **name**, not a second behaviour.

  **The conversion boundary, enumerated completely** (the way FR-090 states W20's). Command: `rg -n 'AppendTranscript\(' --glob '*.go' --glob '!*_test.go'` → **22 matches**, of which **20 are invocations** and **2 are declarations** (the store method at `unified.go:802`; the `TranscriptAppender` interface method at `pkg/tools/handoff.go:70`). *(v2 named 5. Grill #2 said 20 — it counted invocations; this spec's command counts matches. Both numbers are stated with their scope so the enumeration is reproducible.)* Every invocation site MUST surface the now-possible error as a counter increment plus a WARN naming the session id:

  | File | Invocation sites | Count | Owner |
  |---|---|---|---|
  | `pkg/agent/task_executor.go` | `:554`, `:687`, `:718`, `:1084`, `:1170`, `:1278`, `:1989` | 7 | **U26** |
  | `pkg/agent/turn.go` | `:1130`, `:1208`, `:1270`, `:1325` | 4 | U3 |
  | `pkg/tools/handoff.go` | `:205`, `:386` (+ the interface decl at `:70`) | 2 | **U22** |
  | `pkg/gateway/websocket.go` | `:4256`, **`:1642`** `[grill2 m2-5]` | 2 | U11 |
  | `pkg/agent/loop.go` | `:5923`, `:6107` | 2 | U9 |
  | `pkg/agent/goal_loop.go` | `:739`, `:823` | 2 | **U26** |
  | `pkg/agent/cancel.go` | `:354` | 1 | U15 |
  | `pkg/session/unified.go` | — (the method **definition**) | 0 | U4→U5→U6 chain |

  **Verified: all 20 invocation sites already capture or check the returned error** (`if err := …; err != nil` or `appendErr := …`), so making the primitive strict breaks **no** call site's compilation. The change is in runtime behaviour at sites that already have an error branch — which is why this is the smaller migration as well as the correct one. `pkg/agent/external_dispatch.go` and `pkg/agent/approval_transcript.go`, which v2 listed here, contain **zero** `AppendTranscript` calls and are correctly absent — see FR-099 and M2-3.
- **FR-003**: The `ts.abandoned` write suppression (`pkg/agent/turn.go:1295-1298`) MUST emit a **WARN naming the session id and the suppression reason**. The existing `abandonedWritesSuppressed` counter is **retained unchanged** — it already increments here and at six other sites, is declared at `turn.go:25`, documented at `:21-24` as backing `omnipus_abandoned_writes_suppressed_total`, and exported at `:44`. Any test of this requirement MUST assert the **log record** and a counter **delta** across the call, never the counter's existence. `[grill C-2 — v1's premise that this path is "entirely silent" was false, and the test written from it was green against the unmodified tree]`
- **FR-004**: The system MUST define `SessionID` and `RoutingSessionID` as distinct named types that do not implicitly interconvert.

### Child owns a real session (W1, W2)

- **FR-005**: Every delegated child MUST have a store-backed session created with the exact `childID`, via an exported wrapper over the existing exact-id primitive (`pkg/session/unified.go:441`).
- **FR-006**: The child's session meta MUST carry the parent's `Owner` verbatim, so `WithSessionOwner` installs inside the child turn (`pkg/agent/loop.go:6844-6848`).
- **FR-007**: The child's `processOptions` MUST NOT set `NoHistory` (today `true` at `pkg/agent/subturn.go:1032`), and `TranscriptSessionID` MUST equal `childID`.
- **FR-008**: `SessionMeta` MUST carry `ParentSessionID` naming the **direct** parent, and `UnifiedSessionType` MUST gain a subordinate value.
- **FR-009**: For a child, `delegateSessionID == sessionKey == transcriptSessionID` MUST hold, so `steer`/`respond`/`cancel`/`peek`/`inbox`/`follow_up` take the same single id they take today.
- **FR-010**: The child session MUST be minted into the **same** shared `*session.UnifiedStore` the delegate tool holds (`pkg/agent/loop.go:1727-1728`).

### Routing key (W4, W5, W21)

- **FR-011**: `turnState` MUST carry `routingSessionID`, inherited verbatim from the parent; for a root turn it MUST equal the turn's own session id.
- **FR-012**: Every session-scoped WS frame's `session_id` MUST be stamped from `routingSessionID`.
- **FR-013**: The system MUST add an optional `producing_session_id` to session-scoped frames, present **iff** it differs from `session_id`.
- **FR-014**: `routingSessionID` MUST NOT be read outside the closed consumer set (WS payload stamping, the seven role-B predicates, pre-arm keys), and a test MUST fail the build on any read outside it.
- **FR-015**: The seven role-B predicates (`pkg/agent/steering.go:429`, `:459`, `:519`, `:745`, `:787`; `pkg/agent/turn.go:524`, `:564`, `:607`) MUST re-base onto `routingSessionID`.
- **FR-016**: The pre-arm latch keys MUST re-base onto `routingSessionID`, preserving the "inherits verbatim" invariant literally, across **five sites of which three are direct reads**: direct — `pkg/agent/cancel_prearm.go:354`, `:355`, `pkg/agent/subturn.go:585`; transitive — `pkg/agent/cancel_prearm.go:338` (a parameter fed from `:355`), `:602` (a call taking the turn state), `pkg/agent/subturn.go:1147` (consumes a precomputed slice). `[grill2 M2-8]` The three-vs-five distinction is normative, not editorial: **#29's K counts the three reads; this requirement re-bases all five sites.** See "Three reads, five sites" for the derivation and the definition of "read".
- **FR-017**: `SubTurnSpawnPayload.SessionID` and `SubTurnEndPayload.SessionID` MUST be pinned to `routingSessionID` with a regression test, and `DelegateTaskState.SessionID` (`pkg/tools/delegate.go:1303`) MUST be re-pointed deliberately.
- **FR-018**: All **19** `SESSION_SCOPED_FRAME_TYPES` MUST be audited against the routing rule, and the contract change MUST follow Constraint #8's 5-step pipeline in one atomic commit.

### Durable parent→child edge (W6, W7)

- **FR-019**: `LifecycleFilter` MUST gain a `ParentDurableKey` field and a corresponding `matches` clause.
- **FR-020**: A secondary parent index MUST be maintained inside `Persist`, under the existing 64-shard striped lock, so "children of X" is one file read and a transitive walk is O(descendants).
- **FR-021**: Delegation MUST be refused with an operator-visible error when no lifecycle store is wired (`pkg/agent/session_messaging_wire.go:141-143`), mirroring the existing fail-closed posture at `pkg/tools/delegate.go:1150-1157`.
- **FR-022**: The three doc comments at `pkg/session/lifecycle.go:225-228`, `:572-575` and `pkg/tools/list_jobs_sources.go:311-315` MUST be rewritten so none describes `ParentDurableKey` as shared parent↔child. `[grill2 m2-1 — line range corrected from `:571-575`; v2's changelog claimed this correction had been applied here and to BDD-22, and it had not. All three sites now read `:572-575`, matching #12's bound and AC-13.]`
- **FR-023**: The system MUST NOT use `OwnerScopeID` or `ParentAgentID` as the parentage edge, asserted by a **static gate** (#106) rather than only by positive tests of the correct edge. `[grill M-11 — v1 mapped this to #10/#49, neither of which asserts the negative]` **"The walk's code path" is an explicit symbol set, not a phrase** `[grill2 M2-5]`: `verifyCallerOwnsSession` (`pkg/tools/delegate.go:1973-1979`), `callerOwnerKey` (`:1966-1968`) and `LifecycleFilter.matches` (`pkg/session/lifecycle.go:565+`), plus their transitive callees within `pkg/tools` and `pkg/session`. #106 MUST assert it resolved all three symbols, walked ≥ 1 callee edge and located ≥ 1 `ParentDurableKey` read before asserting zero — see the bounds table. This is the sole coverage of a **security** property (a task dispatch puts a *plan id* in `OwnerScopeID`, `pkg/agent/task_executor.go:202-208`, so a walk over it mistakes a plan id for a session id); an unbounded gate leaves it unenforced while reporting green.

### Cancellation (W8, W9)

- **FR-024** `[SUPERSEDED 2026-08-04 — gate + chain-reaction cancellation, see below]`: ~~`RequestCancel` MUST compute the live subtree once in PHASE A and thread it through PHASE B (`pkg/agent/cancel.go:462`) and PHASE C (`:487`) rather than re-scanning.~~ This original text is kept, struck through, because it is the requirement that shipped the bug this supersession fixes — not because "no re-scanning" was ever a correct reading of the ADR's actual intent (it was written to bound the escalation gates' cost, not to freeze the tree). Live UAT (2026-08-03/04) found that a sub-turn registering into `al.activeTurnStates` **after** PHASE A had already run — the common shape being a `delegate async=true` spawn whose delegating parent turn finishes gracefully within milliseconds of the Stop while the backgrounded `spawnSubTurn` goroutine keeps running and registers its child moments later — was invisible to PHASE B/C's escalation gates for the turn's entire remaining lifetime: `al.liveTurnStatesAmong(descendants)` could only ever report on turn ids that were already in the frozen PHASE-A snapshot, so a late child that the frozen list never named could run to completion entirely uncancelled even though the Stop that should have reached it had already begun.
  Presented with two candidate fixes — keep the descendant list continuously updated as new turns start, or make cancellation a **chain reaction** where a parent's cancellation cannot conclude until its own children's has — the operator rejected updating-the-list-in-place as not going far enough and chose the chain reaction as the stronger design: *"either the list needs to be updated when something new starts, or it must be done like a chain reaction: each parent cancels first his children before its cancellation concludes."*
  **Recursion alone was then found insufficient and revised, before this landed, into gate + recursion.** Recursion (re-scanning, or re-arming a latch for a spawn already known to be imminent) fixes the ORDER cancellation reaches existing/imminent descendants — it cannot stop a BRAND NEW child from being born after cancellation has begun. `pkg/agent/subturn.go`'s `spawnSubTurn` constructs the child's context as `context.WithTimeout(context.Background(), timeout)` — deliberately **not** derived from the parent's own context, so a `Critical` async delegate can outlive its parent's graceful finish — so Go's ordinary context-cancellation propagation gives `spawnSubTurn` no signal at all that its own parent is being cancelled; re-parenting that context was considered and rejected (it would break exactly the async-outlives-parent behaviour it exists for). There was also, before this fix, no "cancelling" gate anywhere on the spawn path at all. Without one, a parent whose own "cancel my children" step had already run could still spawn a brand-new child a moment later, born free of anything the cascade had already resolved — recursion has nothing to catch until that child registers or is at least known to be imminent (`pendingSpawns`), and a spawn that hasn't even been dispatched yet is neither.
  **The complete contract, in force — gate first, recursion second:**
  1. **THE GATE** (closes the race by construction): `Interrupt`/`InterruptSessionHard` (`pkg/agent/steering.go`) mark every RESOLVED TARGET — the anchor and every currently-known live descendant — as `cancelling` (`turnState.cancelling`, `pkg/agent/turn.go`, via `markTurnsCancelling`) as the FIRST thing either function does, before firing any interrupt signal. `spawnSubTurn` (`pkg/agent/subturn.go`), before creating any new child, walks `parentTS`'s own ancestor chain via the existing `parentTurnState` pointer, checking `cancelling` at every level; a hit refuses the spawn outright (`ErrSessionCancelling`) before any session, workspace, or transcript state is created for it. Because `Interrupt`/`InterruptSessionHard` are the shared backbone BOTH `RequestCancel` (chat-wide Stop) and `delegate action=cancel` (per-delegate cascade) call, one change gates both surfaces. Never explicitly cleared: each `turnState` is a fresh object per turn generation, so there is nothing to reset — a later, unrelated message in the same session constructs a brand-new root `turnState` (`parentTurnState==nil`, `cancelling`'s zero value `false`), which the ancestor walk never reaches; no TTL, no registry, no possibility of permanently "bricking" a session's ability to delegate.
  2. **THE RECURSION** (closes the residual ordering gap the gate cannot, on its own, resolve for what already exists): PHASE B and PHASE C (`pkg/agent/cancel.go`) each **re-derive the live descendant set FRESH** — calling `al.collectDescendantTurnIDs(sessionID)` again at the checkpoint, not threading the PHASE-A snapshot through — before deciding whether anything remains to escalate. `collectDescendantTurnIDs` is a flat `Range` match on `routingSessionID` (never a graph walk), so re-deriving it at each checkpoint carries no cycle or unbounded-recursion hazard; a child that registered (or whose spawn attempt WON the narrow race against the gate's own mark, an inherently vanishingly-small window between two atomic operations) is picked up the first time a checkpoint runs after it registers. When a checkpoint finds a delegate spawn still in flight for the identity being escalated (`hasPendingDescendantSpawn`, backed by the existing `pendingSpawns` marker `pkg/tools/delegate.go`'s `executeAsync` already sets via `MarkPendingDelegateSpawn`) but nothing yet registered to re-scan, it **arms a chain-reaction cancel latch** (`armChainReactionCancelLatch`) under the SAME identity key `cancel_prearm.go` already uses for "a cancel arrived before any turn registered." The instant the pending child actually registers — however much later, including after every scheduled checkpoint has already fired — `registerActiveTurn`'s existing `consumePreArmedCancel` call finds the armed latch and **re-invokes `RequestCancel` for it**, giving that child (and thus, inductively, its own descendants) a full, fresh PHASE A/B/C cycle of its own — which re-applies the SAME gate (step 1) to ITS OWN children, closing the race inductively at arbitrary depth. This is bounded and cannot livelock: it is driven entirely by REAL, already depth/concurrency-limited turn registrations, never by an unbounded fixed-point loop against a hypothetically-still-growing tree.
  3. What the original rule was protecting — that cancellation must never reach a parent or a sibling, and that `descendants_canceled` must accurately report what was reached — still holds: every re-scan, every chain-reaction latch, and the gate's own ancestor walk are all scoped to the SAME chat tree (the ancestor walk via `parentTurnState`, the re-scan/latch via `routingSessionID`), which by construction can only ever match members of that one chat's own tree (a sibling chat, or any turn outside this tree, has a different `routingSessionID`/is not in the `parentTurnState` chain, and is structurally excluded). `descendants_canceled` on the ORIGINAL turn's own `turn_canceled` audit event is intentionally **not** mutated to reflect later chain-reaction catches — it still reports the PHASE-A snapshot captured at the moment that specific cancel activated (recomputing it fresh at `Finish()` time was tried and reverted: `runTurn`'s cleanup order runs `clearActiveTurn` before `Finish()`, so by the time the `SetOnCancelFinish` callback fires, the reporting turn's own entry — and, in the common single-descendant case, therefore the whole visible tree — has already been removed from `al.activeTurnStates`, making a "fresh" re-scan at that exact point under-report, not correct, reality). Instead, each recursively-caught descendant produces its OWN separate, accurate `turn_canceled` audit event via its OWN recursive `RequestCancel` call — the full audit TRAIL (potentially several events) reflects reality; no single event's `descendants_canceled` field is asked to retroactively become a promise about the whole cascade's eventual reach.
  4. `cancelHardAbortDelay`/`cancelDetachDelay` (`pkg/agent/cancel.go`) replace the previous inline `3*time.Second`/`5*time.Second` literals as test-shrinkable package vars, mirroring `cancel_prearm.go`'s existing `cancelPreArmTTL`/`turnSettleGrace` pattern.
  Regression coverage, all in NEW file `pkg/agent/cancel_chain_reaction_test.go`: `TestRequestCancel_LateChildRegisteredDuringEscalation_IsStillHardAborted` and `TestRequestCancel_ChainReactionLatch_CatchesChildRegisteringAfterLastCheckpoint` cover the recursion half (step 2). `TestSpawnSubTurn_RefusedWhileParentIsCancelling` and `TestSpawnSubTurn_RefusedWhileAncestorIsCancelling` cover the gate half (step 1) — the required proof that "a child whose creation is attempted DURING its parent's cancellation must not end up running," verified to FAIL when the gate's enforcement in `spawnSubTurn` is disabled (recursion alone) and PASS with it restored. Both existing BDD-23/BDD-24 tests (`TestU15Cancel_PhaseB_HardAbortsLiveChild`, `TestU15Cancel_PhaseC_DetachesSurvivingChild`) and the async-delegate pre-arm repro (`TestRepro_AsyncDelegateCancel_ArmsBeforeChildRegisters`) remain green unmodified — they encode behaviour that is still correct under the new contract.
- **FR-025**: The durable descendant walk MUST run once per Stop, on its own goroutine, off the escalation path.
- **FR-026**: Each descendant's lifecycle record MUST transition to `cancelled` (today `pkg/agent/cancel.go:428` transitions exactly one).
- **FR-027**: `ProcessSession.OwnerSessionID` MUST be stamped from the child's own id, and `KillBackgroundSessions` MUST cascade over the descendant set.
- **FR-028**: `delegate action=cancel` MUST kill that child's background shells (today no such call exists on that path).
- **FR-029**: A 3P child's process **group** MUST die with the child. `[grill2 M2-2]` **This is new work with named sites, not an assertion of existing behaviour.** Verified: the external-CLI subprocesses are started at `pkg/agent/runner/driver_claude.go:147`, `driver_codex.go:121` and `driver_opencode.go:87`, each `exec.CommandContext(runCtx, binary, args...)` followed by `cmd.Start()`, and `rg 'SysProcAttr|Setpgid|cmd\.Process\.Kill|Signal\(' pkg/agent/runner/` returns **zero matches** — so `exec.CommandContext` kills only the direct child on context cancel and a 3P child's own subprocess tree survives today. Each of the three drivers MUST set `cmd.SysProcAttr.Setpgid = true` and cancellation MUST signal the **process group**, following the in-house precedents at **`pkg/tools/shell_process_unix.go:14`** — `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`, the closest analogue since it is this repo's own background-shell path and is owned by U16 — and `pkg/sandbox/hardened_exec_linux.go:39-41` / `pkg/sandbox/hardened_exec_cancel_unix.go` (the group kill). The group-kill helper lands in NEW `pkg/agent/runner/procgroup_unix.go` with a no-op `procgroup_windows.go`, and **FR-029 is scoped POSIX-only** — matching how the store's cross-process assertions are scoped (see Assumptions) — because `Setpgid` has no Windows equivalent in this codebase. Owner: **U22**; `pkg/agent/runner/**` appeared in no v2 ownership row, so this requirement had no implementation site at all.
- **FR-030**: The `turn_canceled` audit entry's `descendants_canceled` (`pkg/agent/cancel.go:376`) MUST remain non-empty and name **every** descendant the Stop reached, asserted at **depth 3** by BDD-100 / #97 in the visible plan. `[grill M-12 — v1 required "every descendant" but tested only depth 1 where the implementing agent could see it]`

### Approvals and session teardown (W10)

- **FR-031**: `ApprovalGrantStore` MUST expose a **two-key** inheritance operation, `InheritFrom(srcSessionID, srcAgentID, dstSessionID, dstAgentID string)`, which reads the grant set under `{srcSessionID, srcAgentID}` and unions it into `{dstSessionID, dstAgentID}`. At spawn it MUST be called with the **parent's** routing/session id as the source and the **child's own** session id as the destination. The single-key `Inherit` MUST be removed, not merely re-parameterised. `[grill C-1 — MEANING CHANGED from v1.]` v1 said only "`Inherit`'s first argument MUST become the child's own session id", which is a **silent no-op**: `Inherit` uses one `sessionID` for both the source lookup (`pkg/security/approvalgrants.go:118`) and the destination write (`:122`), so a child-keyed call misses the source, returns at `:119`, and the child blocks 300 s at `pkg/agent/loop.go:8630-8631` — the exact failure US-6 exists to prevent.
- **FR-032**: `cancelAllPendingForSession` (`pkg/gateway/approvals.go:403-419`) MUST run over the descendant set, not a single id. It matches by **exact equality** on `SessionID` (`:419`), so this requirement is only meaningful together with FR-080.
- **FR-033**: A child session MUST receive a `CloseSession` on child-turn terminal, clearing its grant set, `loadedTools` bucket, `metaCache` entry and `recallSpans` entries. See FR-088 for the call site and its owner — **no such call site exists in the tree today** (verified: the only non-test callers of `pkg/agent/session_end.go:32` are `websocket.go:1038`, `loop.go:1048`/`:1064`, `session_end.go:865`, none of which is a child-turn terminal).

### Transcript visibility (W11)

- **FR-034**: `IsDelegateChildEntry()` MUST be deleted and MUST have zero references outside tests.
- **FR-035**: All four filter sites MUST be deleted — `pkg/gateway/replay.go:298`, `pkg/gateway/rest.go:826` (including the `filterDelegateChildEntries` helper at **`:814-832`** — the func body is `:823-832` and its **doc comment is `:814-822`**, which itself names `IsDelegateChildEntry` twice at `:815` and `:818` — and both callers `:851`/`:887`), `pkg/agent/verifier_adjudication.go:406`, `pkg/tools/inspect_session.go:172`. `[grill2 m2-4, widened]` v2 cited only `:823-832`; deleting exactly that range leaves a dangling doc comment that keeps SC-003's `rg` non-zero. Found while verifying m2-4, which named only the other two sites.
- **FR-036**: `TranscriptEntry.ParentSpawnCallID` MUST be retained as provenance on the child's own entries and MUST have a named reader (the drill-down surface).
- **FR-037**: The comment blocks that exist only to defend the filter MUST be rewritten or removed in the same change: `pkg/session/daypartition.go:268-307`, `:311-332`; `pkg/gateway/replay.go:41-45` and `:271-297`; **`pkg/agent/verifier_adjudication.go:394`** and **`pkg/tools/inspect_session.go:170`** `[grill2 m2-4]`. The last two are inline comments adjacent to deleted code that name `IsDelegateChildEntry`; v2 did not list them, so a careful implementer would remove them and a literal one would not — and SC-003's `rg` cannot return zero while either survives. "Careful" is not a requirement; the list is.
- **FR-038**: No read boundary — backend or frontend — MAY reintroduce a transcript visibility filter.

### Ownership and interrupt (W12, W13)

- **FR-039**: `verifyCallerOwnsSession` MUST walk the `ParentDurableKey` chain upward from the target toward the caller, bounded by the configured max delegation depth, at all six gated call sites.
- **FR-040**: The sibling/cousin reach MUST be removed; root-over-subtree reach MUST be preserved.
- **FR-041**: `InterruptSession`, `InterruptSessionHard`, `InterruptBySessionKey` and `InterruptBySessionKeyHard` (`pkg/agent/steering.go:449`, `:511`, `:611`, `:665`) MUST collapse into one entry point taking a mandatory explicit `InterruptScope ∈ {ScopeSubtree, ScopeSelfOnly}`. **`InterruptGraceful` (`:397`) and `InterruptHard` (`:866`) are out of scope** — they take no session id and are process-wide.

  **The compile-breaking surface is five files, each with a declared owner** `[grill2 C2-4 — derived over the signature, not over file writes]`:

  | File | What breaks | Owner | Wave |
  |---|---|---|---|
  | `pkg/agent/steering.go` | the four definitions | **U8** | E |
  | `pkg/agent/cancel.go` | calls at `:390`, `:465` | **U15** | F |
  | `pkg/tools/delegate.go` | func-typed fields `:363-364`; `SetCancelHooks` params `:572-578` | **U14** | F |
  | `pkg/agent/session_messaging_wire.go` | method values at `:166` | **U19** | G |
  | `pkg/commands/runtime.go` | `AgentLoopInterface` method decl `:39` | **U8** | E |
  | `pkg/commands/cmd_cancel_test.go` | stub implementation `:61` | **U8** | E |

  Enumeration command: `rg -n 'InterruptSession\b|InterruptSessionHard\b|InterruptBySessionKey\b|InterruptBySessionKeyHard\b' --glob '*.go' --glob '!*_test.go' -l` → **13 files**. Verified line by line: the **other eight** (`cancel_prearm.go`, `external_dispatch.go`, `loop.go`, `orphan_watch.go`, `subturn.go`, `turn.go`, `config.go`, `approvals.go`) contain **doc comments only** — ~25 references naming the retired symbols, zero call sites. Those are doc-rot, covered by **FR-100**, and are the reason U3/U7/U9/U17a/U22 do **not** acquire a wave dependency on U8. A file-level dependency derivation would have moved five units unnecessarily and still missed `pkg/commands/**`.
- **FR-042**: `Interrupt(child, ScopeSubtree)` MUST reach that child's own descendants and MUST NOT reach the parent or a sibling.

### Delegate observability (W14, W19)

- **FR-043**: `recentActivityLines` MUST read the delegate session id and MUST log its empty path.
- **FR-044**: `executeSync` MUST register a `DelegateTaskState` (today only `executeAsync` does).
- **FR-045**: `t.tasks` and `t.sessionIndex` MUST have a deletion path with a **named trigger**: an entry is deleted when its task has reached a terminal state **and** its last `status` read is older than a stated TTL `T`. Eviction MUST run without an external caller (on the same cadence as, or driven by, the delegate tool's own bookkeeping). `[grill M-10 — v1 said only "MUST have a deletion path", leaving "reaped" undefined so two implementers build two different things]`
- **FR-046**: The drill-down surface (`GET /api/v1/sessions/{childID}` → `<ChatScreen />`) MUST be the stated inspection surface for hidden delegations and MUST work with verbose chat disabled.
- **FR-047**: The drill-down surface MUST filter on `producing_session_id`, and a static gate MUST assert **zero** non-test references to `subagent_message` or `subagent_state` in `src/`, having first asserted it located ≥ 1 reference to `producing_session_id` there (binding rule 4). `[grill M-11 — MEANING CHANGED from v1.]` v1's "no requirement MAY depend on…" is a property quantified over all requirements; a single E2E test cannot establish it, so the matrix row claiming coverage was false. Restated as something a gate can actually check.

### Session store: striping (W15)

- **FR-048**: `UnifiedStore.mu` MUST be replaced by (a) a 64-shard FNV-keyed `sync.Mutex` pool keyed by session id, copying `pkg/session/lifecycle_lock.go:17-39`'s shape, and (b) a narrow `cacheMu sync.RWMutex` guarding only `metaCache` (`:182`) and `cacheLoadFailures` (`:192`).
- **FR-049**: `cacheMu` MUST NEVER be held across an `os.*` or `fileutil.*` call.
- **FR-050**: Lock order MUST be one-directional: `sessionLock(id)` → `cacheMu`. Two session shards MUST NOT be held at once, with exactly two exceptions: (a) `ClearAll`/`RetentionSweep`, which MUST take every shard **in index order**; and (b) **none other** — in particular the parent-`Owner` copy MUST satisfy the prohibition by **releasing** the parent's shard before taking the child's, per FR-082, rather than by being exempted. `[grill C-6 — v1's blanket prohibition forbade the change's own most-used new operation]`
- **FR-051**: `ListSessions` MUST reconcile per-session under that session's shard and snapshot under `cacheMu.RLock`, and MUST NOT take a store-global write lock.
- **FR-052**: The design MUST NOT impose a fixed concurrency cap. This is a **design** property, asserted **structurally**: a static gate proving no constant in the store's write path bounds the number of concurrent session writers (no semaphore, no worker pool, no fixed-size channel gating `NewSession`/`AppendTranscript`/`SetMeta`), plus the existing 2N-vs-N slope at a box-saturating N (dataset rows 3–4). `[grill2 M2-4 — MEANING CHANGED from v2.]` v2 asserted it as a **throughput promise** — "still rising from 64 to 128" (#107) — which **64 shards make false by construction**: at N=128 the pigeonhole gives ≥ 2 sessions per shard under a perfect hash, so contention at least doubles, and the work inside each shard is fsync-bound (`WriteFileAtomic` does a file `Sync()` at `pkg/fileutil/file.go:97` **and** a parent-directory `Sync()` at `:121`). On a finite-IOPS CI runner throughput at 128 concurrent creates plateaus or falls. The spec's own edge case concedes the mechanism ("they contend on one shard; … throughput is bounded by the filesystem") and dataset row 5 records shard collision as "documented, not a failure" — #107 asserted the opposite of both, and would also have violated binding rule 3, being a machine-specific claim wearing a slope's clothing.

### Session store: file split (W23)

- **FR-053**: `meta.json` MUST split into four files — `meta.json` (identity + lifecycle + `Type` + `ParentSessionID`), `stats.json` (`SessionStats` + its own `UpdatedAt`), `goal.json` (the 9 `Goal*` fields), `loop.json` (the 9 `Loop*` fields).
- **FR-054**: `writeMetaLocked` MUST be replaced by four targeted writers, each taking its session's shard.
- **FR-055**: `readUnifiedMeta` MUST compose all four; a missing `stats.json`/`goal.json`/`loop.json` MUST compose as the zero value and MUST NOT be an error; a missing `meta.json` MUST be an error.
- **FR-056**: A present-but-corrupt group file MUST surface an error for that group rather than composing a zero value.
- **FR-057**: `UnifiedMeta`'s in-memory shape and marshalled JSON MUST be unchanged; no `contracts/` change and no regeneration are required by this work item.
- **FR-058**: `metaCache` MUST continue to hold one composed `*UnifiedMeta` clone per session, so `GetMeta` and `ListSessions` cost nothing extra — asserted by an instrumented filesystem counter showing **zero** disk reads on a cache hit after the split (#103). `[grill M-11 — v1 mapped this to #13, which never touches the cache]`
- **FR-059**: The doc comments at `pkg/session/unified.go:780-785` and `:166-181` MUST be rewritten, as neither single-funnel claim remains true.
- **FR-060**: The system MUST NOT provide a reader for a pre-split fused `meta.json`, and MUST NOT modify `migrateLegacy`/`writeUnifiedMetaDirect` (`:1515`) — the latter asserted by a golden-bytes gate on their output (#104), **which MUST first assert its positive lower bound: ≥ 1 located non-empty golden fixture and both named symbols resolved** `[grill2 M2-5]`. Without that bound the "no pre-split fused reader exists" clause passes whenever the search is wrong. `[grill M-11 — v1 mapped this to #15, which tests neither clause]`

### Session store: counter throttle (W24)

- **FR-061**: `AppendTranscript`'s `Stats.*` and `UpdatedAt` bumps MUST become in-memory mutations of the cached meta under `cacheMu`, with no file write.
- **FR-062**: The transcript append itself MUST stay immediate and unthrottled.
- **FR-063**: A per-store periodic flusher MUST write only `stats.json`, only for dirty sessions, each write taking that session's shard.
- **FR-064**: Forced synchronous flushes MUST occur on a `SetMeta` carrying `Status`, on `DeleteSession`, on `UnifiedStore.Close` (which has no flush hook today), and on the child `CloseSession` teardown (**U17b**, see FR-088 and the ownership table). **`DeleteSession`'s full ordered sequence is stated once, here, and is referenced by FR-033, FR-086 and FR-096** `[grill2 M2-11]`: (1) acquire `sessionLock(id)`; (2) flush the session's dirty stats under that shard; (3) `os.RemoveAll` the directory; (4) evict the `metaCache` entry under `cacheMu`; (5) drop the dirty-set entry under `cacheMu`; (6) release. A flusher tick arriving at any of the six points either finds the session dirty and flushes it (before 3) or finds it gone and drops its dirty entry without writing (after 3) — it can never recreate a `stats.json` in a deleted directory. v2 resolved the flusher's half in Edge Cases and left the `metaCache`-eviction ordering FR-033 also requires unstated, while FR-086 described the deletion window from the reader's side with no shared sequence — three requirements, one operation, no agreed order.
- **FR-065**: Event-driven `SetMeta` paths (goal, loop, status, title, owner, workspace) MUST NOT be throttled.
- **FR-066**: `UpdatedAt` MUST compose as the later of `meta.json`'s and `stats.json`'s on load.
- **FR-067**: The flush interval **MUST** be a config key with a default of **5 seconds** `[operator 2]`, tunable from measurement. `[grill m-5 — promoted from the spec's only `SHOULD`, which SC-034's gate list did not cover and which was therefore unenforced in either direction]` A test MUST assert the key exists, defaults to 5 s, and that a non-default value is honoured end to end (#105). **The key and its seeded default are declared in `pkg/config/config.go` and `pkg/config/defaults.go`, owned by U28** `[grill2 C2-1]` — v2 required a new MUST config key while no ownership row covered `pkg/config/**` at all, so the requirement had no implementation site. U6 **reads** the key; it does not own the config package.

### Scale and hygiene (W16, W17, W18)

- **FR-068**: `GET /api/v1/sessions` MUST paginate through all four layers — see FR-092 for the layer-by-layer breakdown and owners — and the sidebar MUST spend its visible-root budget on **root** sessions so a wide fan-out cannot evict the parent chat. `[operator 1]` The sidebar treatment is nesting (FR-093), not a hide-filter.
- **FR-069**: Root-level delegation MUST be admission-gated, refusing rather than queueing when the cap is reached. "Operator-visible" means an `ErrorResult` returned to the calling agent **and** an `slog.Error` record, mirroring `pkg/tools/delegate.go:1150-1159`; no separate user-facing notification is required `[operator 6]`. `turnState.concurrencySem` is set only on a child today (`pkg/agent/subturn.go:1051`, the sole assignment; guard at `:607`), which is why root-level fan-out is ungated.
- **FR-070**: Nested delegation's existing `concurrencySem` gating MUST be unchanged.
- **FR-071**: A child's uploads directory MUST be reachable by the parent session's cascade-delete, for every descendant.

### Process (W22)

- **FR-072**: Every test encoding the current contract MUST be deliberately inverted to assert the new invariant; none MAY be quietly deleted. **The automated portion is presence plus the `// ADR-057-W22-inverted` marker (#81); "asserts the new invariant" is a human review gate.** `[grill m-3 — a Go test can verify a file exists and contains a token; it cannot verify that another test's assertions encode a semantic invariant]`
- **FR-073**: The test inversions MUST land as their own commit, containing no behaviour-file change.
- **FR-074**: Every test written or inverted for this spec MUST construct parent and child ids as distinct, non-equal values and MUST assert which one was used.

### Consequential semantics (US-18)

- **FR-075**: `follow_up` warm resume MUST load generation N's history into generation N+1 — this is intended behaviour, not a leak, and MUST be pinned by test.
- **FR-076**: The ADR-053 D15 per-child message ceiling MUST be enforced per **direct parent**, making a chat's aggregate (children × ceiling); the change MUST be asserted, not assumed.
- **FR-077**: The ADR-053 D16 inbox producer (`pkg/tools/message_parent.go:640`) and consumers (`pkg/tools/delegate.go:2024`, `:2200`) MUST both key on the immediate parent's `ParentDurableKey` and MUST change together.
- **FR-078**: The boot sweep (`pkg/agent/boot_sweep.go`, owned by U19) MUST reconcile an in-flight child's lifecycle record across a process restart, and a transcript write against an un-minted child id MUST return a **non-nil error** — asserted positively, not as "no orphan directory was found".

### Added in v2 (W3, W10, W15, W16, W20, W23, W24 and operator decision 1)

`[grill C-1…C-6, M-1, M-6, M-8, M-10, M-13, M-14; operator 1, 2, 4, 6; o-2]`

- **FR-079**: `InheritFrom` MUST log and count the branch where the **source** key holds no grants, naming both keys. `[grill C-1]` Today this is a documented silent `return` (`pkg/security/approvalgrants.go:118-120`, doc at `:110-111`). Without a signal here, any future re-key can regress into C-1 and every test stays green.
- **FR-080**: A pending-approval registry entry's `SessionID` (`pkg/gateway/approvals.go:85`, set at `:213`/`:232`) MUST be the **acting** session id — the child's own, not the chat's. `[grill M-6]` FR-032 presupposed this and no v1 requirement stated it.
- **FR-081**: An approve/deny response MUST resolve to its pending entry **by approval id**, never by session id. `[grill M-6]` `tool_approval_required` is in `SESSION_SCOPED_FRAME_TYPES` (`src/store/chat.ts:1240`), so FR-012 makes its `session_id` the **routing** key while FR-080 keys the registry entry by the **child** — resolving by session id would break the round trip on the first delegated approval.
- **FR-082**: The parent-`Owner` copy inside `CreateSessionWithID` MUST read the parent's meta under `sessionLock(parent)`, **release** that shard, and only then create the child under `sessionLock(child)`. Two session shards MUST NOT be held simultaneously. The resulting TOCTOU on `Owner` is accepted and documented: the field is immutable after session creation. `[grill C-6]` `createSessionLocked` builds `UnifiedMeta` with no `Owner` (`pkg/session/unified.go:448-460`), so the value can only come from the parent — one operation, two shards. Acquiring both would use **hash** order, inverting against `ClearAll`/`RetentionSweep`'s **index** order (R-19).
- **FR-083**: The periodic flusher MUST make a dirty session's `stats.json` current **without any external trigger** — no `Close`, no `SetMeta`, no `DeleteSession`, no `CloseSession`, no test-driven tick. `[grill C-4]` Every other W24 requirement is satisfiable by a store whose flusher goroutine was never started; under the production shape (a long-lived gateway that never calls `Close`) that means `stats.json` is stale for the process lifetime.
- **FR-084**: Each of W23's four targeted writers MUST update **only its own field group** within the cached `*UnifiedMeta` and MUST NOT replace the cache entry wholesale; and a `readMetaLocked` cache-miss compose MUST NOT overwrite an entry marked dirty. `[grill C-5]` `writeMetaLocked` ends today with a whole-document `us.metaCache[sessionID] = meta.Clone()` (`pkg/session/unified.go:798`), documented at `:780` as "the single invalidation/update point for every mutation path". The obvious per-writer translation of that shape discards every unflushed `Stats.*` delta — Alternative F's clobber, one layer up from the file the spec proved it away on.
- **FR-085**: Every negative, exclusion or static gate MUST assert a **stated positive lower bound** before its zero-assertion, and MUST fail if its search located fewer occurrences than that bound. The bounds are enumerated in "Negative-gate positive lower bounds" and MUST be restated in the test's own comment so drift is visible in review. `[grill C-3]`
- **FR-086**: `ListSessions`'s consistency model after striping MUST be stated in code and honoured: a **best-effort point-in-time snapshot** that MAY omit a session deleted during the call, MUST NOT panic, deadlock or return a partially-composed meta, and MUST NOT return a session whose directory was already absent when the call began — **which requires the reconcile pass to prune, and FR-097a makes that a requirement rather than an assumption.** `[grill M-14; grill2 M2-7 — the second clause was stricter than the design it claimed to describe.]` Verified: `ListSessions` snapshots `us.metaCache` and its reconcile pass only **adds** entries for out-of-band directories (`pkg/session/unified.go:1251-1280`) — it never removes a cache entry whose directory has vanished, so a directory deleted **out of band** (`RetentionSweep`, an operator `rm`, a crashed deploy — exactly AC-19's cases) stays cached and is returned forever. The codebase already knows this failure by name: `ClearAll` carries a targeted stat-and-prune loop at `:1483-1487` whose comment (`:1474-1482`) says removing it leaves *"ListSessions to resurrect sessions that are gone from disk."* That loop is the in-house precedent FR-097a generalises. Today the whole method runs under `us.mu.Lock()` and `:1240-1246` documents why; FR-051's split creates a window that must be specified rather than discovered.
- **FR-087**: `t.tasks` and `t.sessionIndex` MUST each be bounded by a stated constant `C`, enforced after eviction. A task within its TTL MUST be retained so eviction cannot break `delegate action=status`. `[grill M-10]`
- **FR-088**: The child-turn terminal path in `pkg/agent/subturn.go` (**owned by U7**) MUST invoke `CloseSession(childID, "delegate_terminal")` on each of the four terminal states — completed, cancelled, failed, abandoned — using the entry point at `pkg/agent/session_end.go:32` (**owned by U17**, signature unchanged). `[grill M-13]` No such call site exists in the tree, and v1 assigned "W10 (teardown call site)" to U11, whose file is the *user* session-close path.
- **FR-089**: The W5 audit MUST produce a **committed classification artefact** assigning each of the 19 `SESSION_SCOPED_FRAME_TYPES` to exactly one of class **(a)** child-turn-produced → both ids, **(b)** root/gateway-produced → `producing_session_id` absent, **(c)** documented pre-existing gap. Every type MUST be asserted per its class, and a gate MUST fail if any of the 19 is unclassified. `[grill M-8]` A single outline asserting one property across all 19 is false for at least five of them.
- **FR-090**: W20's conversion boundary MUST be stated and MUST be complete within it: **every field and parameter in `turnState`, `processOptions` and the `UnifiedStore` public API** carries `SessionID` or `RoutingSessionID` rather than a bare `string`. References outside that boundary are explicitly out of scope for this change. `[grill o-2]` With 116 non-test `transcriptSessionID` references across 18 files, a partial conversion is the likely outcome; test #3 proves the types do not interconvert, not that they are used.
- **FR-091**: `GET /api/v1/sessions` MUST return **root** sessions (`ParentSessionID == ""`) only, each carrying a `child_count`, and MUST accept a `parent_session_id` filter returning exactly that node's **direct** children. A session whose `ParentSessionID` names a session that no longer resolves MUST be returned as a **root**. `[operator 1]` This is the resolution of ADR-057 §9's R-9 open question: **nested under parent**, explicitly **not** the `verifier` hidden-with-a-flag precedent.

  **Response shape — decided, not left to the implementer** `[grill2 M2-10]`. `listSessions` (`pkg/gateway/rest.go:795-811`) **already** returns a discriminated union: a bare `[]gen.Session` when there are no partial errors, otherwise `gen.ListSessions200JSONResponseBody1{Sessions, PartialErrors}` — with generated `As*`/`From*`/`Merge*` accessors at `pkg/api/generated/openapi_types.gen.go:14816-14862`. Adding `child_count` and a paging envelope therefore **reshapes an existing `oneOf`**, which this project constrains by hard precedent: **`oneOf` + discriminator wrappers MUST be hosted INLINE in `openapi.yaml` over internal `#/components/schemas/…` refs** (ADR-034; oapi-codegen inlines external file refs inside a `oneOf` as anonymous structs and emits non-compiling `As*` accessors; precedent `AgentCreateRequest`). The decisions:
  - The paged envelope is a **new named schema `SessionPage`** `{sessions: [Session], next_cursor?: string, partial_errors?: [string]}`, hosted **inline in `openapi.yaml`** per ADR-034, replacing the two-variant `oneOf` with **one** shape. The bare-array variant is retired — greenfield permits it (operator decision 1) and one shape removes the discriminator problem rather than deepening it.
  - **`partial_errors` composes with paging**: a page whose merge hit a failing legacy store is **still a page** — it returns its rows, populates `partial_errors`, and **still returns `next_cursor`**. A store that errored contributes no rows and does not halt the cursor (see FR-098).
  - **`include_verifier` × roots-only**: `include_verifier` continues to gate *inclusion of verifier sessions*, orthogonally to hierarchy. A verifier session with no parent is a root; one with a parent is reachable only via `parent_session_id`. Absent the flag, verifier sessions are excluded from both surfaces and are **not** counted in `child_count`.
- **FR-092**: Pagination MUST be implemented at each of the four layers, each with exactly one owner: `UnifiedStore.ListSessions` (`pkg/session/unified.go:1247`, **U6**), `AgentLoop.ListAllSessions` (`pkg/agent/loop.go:5046`, **U9**), `restAPI.listSessions` (`pkg/gateway/rest.go:758-812`, **U18**), `fetchSessions` (`src/lib/api.ts:1379-1388`, **U12**). Verified: **none of the four takes a limit or offset today.** `[grill M-1]`

  **Where cost MUST be bounded, and where an in-process sort is the accepted design** `[grill2 C2-2 — MEANING CHANGED from v2.]` v2 required that each of the four layers "bound its own cost rather than loading the full set and slicing". That is **not implementable** at the two backend layers and its own test (#100) was therefore either impossible or vacuous. Verified: `UnifiedStore.ListSessions` builds its result by ranging `us.metaCache` — a Go **map**, i.e. unordered — then `slices.SortFunc` by `UpdatedAt` descending (`:1283-1291`); there is no recency-ordered index on disk or in memory, and none was required by any FR. `AgentLoop.ListAllSessions` merges the shared store with **every** legacy per-agent store, de-duplicates against `sharedIDs`, then sorts the union (`pkg/agent/loop.go:5046-5090`). A recency-ordered page from either shape **requires** materialising and sorting the whole set.

  The requirement is restated to match the design, and the design is stated:
  - **Store and loop layers: an in-process sort over resident data is the design, explicitly accepted.** `metaCache` already holds every session's composed meta — that is its purpose, and FR-058 asserts a cache hit costs **zero** disk reads. So the sort touches **no disk**: there is no I/O cost to eliminate, and adding a persisted recency index would be a large new mechanism buying nothing. What these layers MUST guarantee instead is (a) they return exactly the requested window of the recency-ordered sequence, (b) the window is **stable** across repeated calls with no intervening write, and (c) a paged call performs **zero per-session disk reads** against a warm cache (instrumented via FR-103's read seam, shared with #103). An implementation that ignores `limit`, orders non-deterministically, or re-reads meta files per page fails (c) — and those are the failure modes that actually matter here.
  - **Boundary layers: cost MUST be O(page), not O(N).** `restAPI.listSessions` MUST serialise at most `limit` rows — response body size scales with `limit`, not with total session count — and `fetchSessions` MUST request and hold at most one page, with `SearchModal`'s rendered node count bounded by the viewport (FR-094). These are the layers where unbounded cost is genuinely paid: JSON serialisation and DOM nodes.

  The two mechanisms US-19 needed and no v2 FR provided are **FR-097** (parent index, for `child_count` and orphan detection) and **FR-098** (cross-store ordering and cursor contract).
- **FR-093**: The sidebar MUST render the session tree — roots at the top level with an expand affordance, children collapsed by default — and its visible budget (`maxVisible`, `src/components/layout/Sidebar.tsx:456-457`) MUST apply to **roots**, so a wide fan-out cannot evict a parent chat. `[operator 1]`
- **FR-094**: `SearchModal` MUST nest matching children under their parent (showing the parent for context even when only a child matched) and MUST render the list **virtualized**. `[operator 1]` It currently fetches every session (`src/components/search/SearchModal.tsx:363`) and renders `groups.map(...)` unwindowed (`:687`); under D1 that list grows by every delegated child at every depth.
- **FR-095** `[AMENDED 2026-08-04 — see the top-of-file AMENDMENT note for full rationale]`: ~~The root-delegation cap MUST be sourced from `agents.defaults.subturn.max_concurrent` (`pkg/config/config.go:1343`, type at `:1301-1302`, resolved at `pkg/agent/subturn.go:64-69`) and MUST NOT be sourced from `Performance.EffectiveMaxParallelAgents()`.~~ **Superseded.** Commit `536b7340` removed `clampParallelExplicit`'s ceiling, so `Performance.EffectiveMaxParallelAgents()` is no longer hard-capped at 16 — the "two knobs, only one clamped" distinction this requirement pinned its contract to no longer exists in the code. **Corrected requirement:** the root-delegation cap MUST be sourced from `Performance.EffectiveMaxParallelAgents()` — the single, central, UI-configurable authority for agent concurrency — whenever `agents.defaults.subturn.max_concurrent` is unset (`<= 0`... precisely, `== 0`; see below). `agents.defaults.subturn.max_concurrent`, when set to a positive value, remains an accepted **explicit per-delegation override** (operator decision 4 is not revoked, only its default posture is): honoured exactly as configured, may legitimately diverge from the central value. `[operator 4; grill M-7]` No new config key is introduced. Original (superseded) text: *"...MUST NOT be sourced from Performance.EffectiveMaxParallelAgents(). No new config key is introduced. The distinction is load-bearing: the former is honoured unclamped when > 0, the latter is hard-capped at 16 by clampParallelExplicit (pkg/config/config.go:459-468) — which is the 16 the UAT observed, and which would make AC-10's 'refuses the 25th' unrunnable."*

  **The unset case — the shipped default — `[AMENDED 2026-08-04]` is now DEFINED, LIVE-RESOLVED and covered**, in place of v3's "seeded, covered" resolution below (retained for the record). Verified: `subturn.go`'s `if maxConcurrent <= 0 { maxConcurrent = al.cfg.Performance.EffectiveMaxParallelAgents() }` fallback is exactly where FR-095 (as corrected above) now WANTS the root gate to land on a fresh install — this is no longer "the branch FR-095 forbids". The corrected resolution:
  - **U28's seed is REMOVED.** `agents.defaults.subturn.max_concurrent` stays at its Go zero value (0, "unset") on a fresh install; `DefaultSubTurnMaxConcurrent` no longer exists. A fixed 16 seed would (once `clampParallelExplicit`'s ceiling was gone) be a SECOND, independently-sized cap silently disagreeing with an operator's own `max_parallel_agents` — exactly the ADR-037 anti-pattern this project bans.
  - **The root gate reads the key directly** (unchanged) and resolves it LIVE: `== 0` → `Performance.EffectiveMaxParallelAgents()`, live-resolved on every check (via `RootDelegationAdmission`'s `resolveCap` closure, mirroring `AdmissionController`); `> 0` → the explicit override, honoured exactly as configured; `< 0` → a **configuration error surfaced at boot** — never "no gate". "No gate" silently restores the ungated root fan-out W17 exists to prevent, which remains the ADR-037 anti-pattern this project bans; the corrected error boundary is `< 0`, not `<= 0`.
  - **Two scopes still share one number, intentionally, and the spec still says so.** The key's existing semantics are a **per-parent-turn, in-turn fan-out** semaphore (`subturn.go:1051` creates it on the *child*; the guard is `:607` on `parentTS.concurrencySem`). W17's gate is a **process-global root-level** admission cap. Reusing the value is operator decision 4; that the two scopes are different and deliberately share one knob is recorded here so it is not later mistaken for a bug.
  - Coverage: **BDD-108 / #112 / SC-050** exercise the **unset** case explicitly — all three amended, see their own `[AMENDED 2026-08-04]` notes.

  Original v3 resolution (superseded): *"U28 MUST seed `agents.defaults.subturn.max_concurrent = 16` in `pkg/config/defaults.go`. 16 matches what `clampParallelExplicit` already caps the fallback at, so the seeded default changes no shipped behaviour — it only makes the value explicit, keeps the root gate off the forbidden branch, and gives an operator one number to raise. The root gate MUST read the key directly, not via `getSubTurnConfig()`, and MUST treat a value ≤ 0 as a configuration error surfaced at boot — never as 'no gate'."*
- **FR-096**: `CreateSessionWithID` MUST detect a child id that collides with an existing session directory and MUST fail loudly rather than adopting it. Under no circumstance may a child silently inherit a pre-existing session's transcript, meta, owner or stats. `[grill §6 STRIDE note]` `createSessionLocked` calls `os.MkdirAll` (`pkg/session/unified.go:463`), which is **idempotent and silent**, and FR-005 said nothing about an existing directory — so a child adopting another session's transcript, meta, owner and stats was the **default** behaviour, with no requirement anywhere obliging an implementer to defend against it.

### Added in v3 (grill #2)

`[grill2 C2-2, M2-1, M2-3, M2-6, M2-7, M2-9, C2-4]` Each requirement below exists because grill #2 found a property the spec **asserted or relied on** with nothing obliging anyone to build it.

- **FR-097**: The store MUST maintain an **in-memory parent index** alongside `metaCache`, guarded by the same `cacheMu`, mapping `parentSessionID → set(childSessionID)` plus the derived `child_count` per session. It MUST be updated in every place `metaCache` is mutated — create, meta write, delete, eviction, reconcile — so `child_count` for a page of roots and orphan detection are **O(1) per row** rather than O(all sessions) per page. `[grill2 C2-2]` FR-091 requires a `child_count` per root and FR-106-style orphan detection; the session store has **no** parent index (`ParentSessionID` is a brand-new field, FR-008) and U13's parent index is over **lifecycle records**, not sessions, with no FR bridging them. Owner: **U4** creates the index surface (it owns the lock + cache surface); **U6** consumes it for listing. Dataset: "Session parent index".
  - **FR-097a**: The `ListSessions` reconcile pass MUST **evict** a cached entry whose session directory no longer exists, under that session's shard, and MUST update the parent index accordingly. `[grill2 M2-7]` This is the mechanism FR-086's second clause requires and v2 left unimplemented. The in-house precedent is `ClearAll`'s stat-and-prune loop (`pkg/session/unified.go:1483-1487`), whose own comment names this failure: *"leaving ListSessions to resurrect sessions that are gone from disk."* Pruning MUST NOT disturb `cacheLoadFailures`' documented limitation (a session that failed to load at construction stays excluded for the process lifetime — Ambiguity item 8): a pruned entry is one whose **directory** is gone, which is a different set.
- **FR-098**: `ListAllSessions` MUST define its **cross-store ordering and cursor contract**: (a) the merged sequence is ordered by `UpdatedAt` descending with the **session id as a stable tiebreak**, so equal timestamps cannot reorder between calls; (b) paging is **offset-based over that merged sequence**, and a cursor remains valid for the duration of a client's expansion even if a store's contents change — a shifted window is acceptable, a duplicated or skipped row within one page is not; (c) a legacy per-agent store that **errors mid-merge** contributes zero rows, appends to `partial_errors`, and **does not halt the page or invalidate the cursor** (today it already `continue`s and appends to `errs`, `pkg/agent/loop.go:5070-5075`). `[grill2 C2-2, M2-10]` v2 gave this layer one cross-unit-request line and no ordering, stability or error contract at all. Owner: **U9**.
- **FR-099**: The transcript **mutate** path — `mutateToolCallInTranscript` (`pkg/agent/approval_transcript.go:188+`), reached from `replaceToolCallInTranscript` (`:176-186`) and `external_dispatch.go`'s result updater — MUST surface "session not found" and "entry not found" as a **counter increment plus a WARN**, rather than the bare `false` it returns today. `[grill2 M2-3]` This is the one genuine hazard on U22's original path, and v2 covered it with **no requirement at all**: verified, `grep AppendTranscript pkg/agent/external_dispatch.go pkg/agent/approval_transcript.go` matches **only two doc comments** (`external_dispatch.go:581`, `approval_transcript.go:166`) — **neither file contains a single `AppendTranscript` call**, so U22's v2 work item was factually empty while the real read-modify-write silent-failure went unrequired. It is the same success-shaped failure as AC-1's, on the mutation side. Owner: **U22**.
- **FR-100**: Every doc comment naming a symbol this change retires MUST be rewritten or removed in the same change set. `[grill2 C2-4]` FR-041 leaves **~25 references across eight files** (`cancel_prearm.go`, `external_dispatch.go`, `loop.go`, `orphan_watch.go`, `subturn.go`, `turn.go`, `config.go`, `approvals.go`) naming `InterruptSession`/`InterruptSessionHard`/`InterruptBySessionKey`/`InterruptBySessionKeyHard` in prose. These are **not** compile breaks — which is why their owners do not acquire a wave dependency on U8 — but this spec already treats stale comments as defects worth a gate (FR-022, FR-037, FR-059, tests #12/#19), and leaving 25 comments describing a four-entry-point API after collapsing it to one is exactly the doc-rot those gates exist to prevent. Each file's existing owner performs the rewrite in its own commit. Enumeration command is stated in FR-041.
- **FR-101**: `sessionLock` acquisition and release MUST go through a **package-level indirection** that U4 owns, defaulting to the direct path and overridable in-package, so lock **order** is observable to a test. `[grill2 M2-6]` #88 is the sole coverage of SC-038 and BDD-92 requires "an instrumented lock wrapper recording every acquire and release in order" — a production seam **no v2 requirement obliged anyone to build** (FR-048 specifies the shard pool copying `lifecycle_lock.go:17-39`, which exposes no hook). Go's race detector is not a lock-order checker, so there is no alternative. Owner: **U4**.
- **FR-102**: FR-051's **reconcile → snapshot boundary** MUST expose an in-package test barrier allowing a `DeleteSession` to be interleaved deterministically between the two phases. `[grill2 M2-6]` BDD-95/#92 require exactly that interleaving across "100 interleavings" (SC-041) and v2 required no mechanism to produce it. Owner: **U6**. If the barrier is judged too invasive at implementation time, the fallback is stated rather than discovered: #92 becomes a stress loop with a declared iteration count and SC-041 drops the word "deterministic" — **that choice must be recorded in the ADR, not made silently in a test file.**
- **FR-103**: `readUnifiedMeta`'s file reads MUST go through a package-level `readFileFn` var mirroring the existing injectable write seam `writeFileAtomicFn` (`pkg/session/unified.go:793`). `[grill2 M2-6]` #103 asserts "zero disk reads on a cache hit" and FR-092's bounded-cost clause (c) reuses the same counter; the store has an injectable **write** seam today and **no read seam**, so neither assertion was constructible. Owner: **U5**.
- **FR-104**: Per-session token accounting MUST remain complete under roots-only listing. `GET /api/v1/sessions` MUST accept **`flat=true`**, which returns every session — roots and subordinates — as a flat, paged list with `child_count` still populated; `UsageScreen`'s "By session" tab MUST use it. `[grill2 M2-9]` `src/components/screens/UsageScreen.tsx:282` calls `fetchSessions(undefined, undefined, { includeVerifier: true })` to back that tab. Under D1 a large share of token spend moves into delegated child sessions, so roots-only would make that spend **silently disappear** from per-session accounting and materially weaken ADR-052's SC-014 (verifier LLM spend auditable per session). v2's Assumptions acknowledged the response-shape break and the regression analysis stopped there — an audit regression with no requirement, no dataset row and no owner. `flat=true` is orthogonal to `parent_session_id`; supplying both is a 400.

---

## Success Criteria

- **SC-001**: `AppendTranscriptStrict` against a UUID with no `meta.json` returns a non-nil error in 100 % of trials and creates zero directories, verified by `os.Stat`.
- **SC-002**: After one delegation, `<store>/<childID>/meta.json` exists on disk and `GET /api/v1/sessions/{childID}` returns HTTP 200 with a non-empty `messages` array.
- **SC-003**: `rg -n "IsDelegateChildEntry" --glob '*.go' --glob '!*_test.go'` returns zero matches, **and** `rg -c "ParentSpawnCallID" --glob '*.go' --glob '!*_test.go'` returns ≥ 60 across ≥ 8 files (measured 73 across 9, 2026-08-03). `[grill C-3]` **Two corrections.** (a) v1's invocation had **no `*.go` glob**, so it matched this spec, the ADR and the review — it could never return zero and was therefore not a criterion at all. (b) The positive control proves the search works; without it, deleting `daypartition.go` satisfies the criterion.
- **SC-004**: After one delegation, in the **same run**: the parent session's `transcript.jsonl` contains zero entries produced by the child **and** `<baseDir>/<childID>/transcript.jsonl` contains exactly the expected non-zero entry count, both measured by reading the files. `[grill M-9]` The first clause alone is satisfied by a child that wrote nothing anywhere.
- **SC-005**: In the live-connection E2E, the SPA store's chat bucket contains both the subagent span and 100 % of its tool-call steps, and `chatAttachStepSpanIndexMiss` fires zero times.
- **SC-006**: All 19 `SESSION_SCOPED_FRAME_TYPES` are classified into exactly one of (a) child-turn-produced, (b) root/gateway-produced, (c) documented pre-existing gap; the classification is committed as an artefact; and each type is asserted per its class — zero types unclassified, zero types asserted against a class they do not belong to. `[grill M-8]` v1's "all 19 round-trip both ids" is **false for at least five** and was scored pass/fail.
- **SC-007**: `List(LifecycleFilter{ParentDurableKey: X})` returns exactly the direct children of X — zero grandchildren, zero siblings — at depths 1, 2 and 3.
- **SC-008**: A chat-level Stop against a live `Critical:true` child produces a PHASE B hard abort and a PHASE C detach against that child, and `descendants_canceled` has length ≥ 1 naming it.
- **SC-009**: A chat-level Stop leaves zero live PIDs among the subtree's background shells and ≥ 1 live PID for an unrelated sibling chat's shell.
- **SC-010**: A `delegate action=cancel` on child B leaves zero live PIDs for B's shells while parent A and sibling C remain in a running state.
- **SC-011**: With a standing parent grant, a delegated child's granted tool call completes with zero approval prompts and elapsed time under the approval timeout by at least two orders of magnitude.
- **SC-012**: After a child terminates, lookups for its grant set, `loadedTools` bucket and `recallSpans` entries all return absent.
- **SC-013**: A sibling's attempt at each of the six gated actions against another sibling returns an ownership error in 6 of 6 cases; the root chat's attempt against a grandchild succeeds in 6 of 6.
- **SC-014**: The interrupt API exposes exactly one entry point (plus its `Hard` variant) and the scope argument is non-optional, proven by a compile-fail fixture.
- **SC-015**: `delegate action=status` returns a non-empty activity snapshot for both a sync and an async delegation.
- **SC-016**: Wall-clock for 2N concurrent single-session writers is less than 2× the wall-clock for N, on the same box and filesystem, with the pre-change store's measurement recorded as the baseline that must be beaten — **and** a static gate reports **zero** constants bounding the number of concurrent session writers in the store's write path (FR-052), having first located ≥ 1 write-path function. `[grill M-11; grill2 M2-4 — the v2 clause "throughput still rises from 64 to 128" is unmeasurable-as-true: 64 shards guarantee ≥ 2 sessions per shard at N=128, and the work inside a shard is fsync-bound. FR-052 is a design property and is now asserted as one.]`
- **SC-017**: A `-race` run over concurrent create/append/`SetMeta`/`ListSessions`/`DeleteSession` on overlapping and disjoint ids reports zero data races and completes without deadlock.
- **SC-018**: Zero `cacheMu` critical sections contain an `os.*` or `fileutil.*` call, verified by a static gate.
- **SC-019**: After a create plus one `/goal set`, one `/loop` start and one transcript append, the session directory contains exactly the four expected files and zero fields appear in more than one of them.
- **SC-020**: A `/loop` tick leaves `goal.json` byte-identical, a `/goal` round leaves `loop.json` byte-identical, and a transcript append leaves both byte-identical — 3 of 3.
- **SC-021**: `readUnifiedMeta` returns success for a directory with only `meta.json` and an error for a directory with no `meta.json`, in both directions, with `GET /api/v1/sessions/{id}` returning 404 in the latter case.
- **SC-022**: `UnifiedMeta`'s marshalled JSON and the REST session payload are byte-identical pre- and post-split for the same logical state, and `make verify-contracts` exits 0.
- **SC-023**: With `stats.json` pre-existing from a forced flush, a burst of K appends inside one flush interval leaves its mtime and content hash unchanged and `transcript.jsonl` gains exactly K lines — **and, in the same test, `stats.json` becomes current once the interval elapses**, so "unchanged" cannot be satisfied by "never written". `[grill C-4, m-2]`
- **SC-024**: After the flush interval elapses, `stats.json`'s counters equal the exact sum of the appended entries' deltas — zero lost and zero double-counted.
- **SC-025**: Each of the four forced flush points independently leaves `stats.json` current, verified by re-opening the store and comparing counters exactly.
- **SC-026**: `GoalRoundsUsed`, `LoopRunCount`, `Status` and `Title` are each readable from disk immediately after their call returns, with zero flush interval elapsed — 4 of 4.
- **SC-027**: After a run spanning ≥ 2 flush intervals, a SIGKILL mid-interval and a re-open: the counter shortfall is at most the final interval's appends, **the flushed prefix is strictly greater than zero**, and `transcript.jsonl` is complete. `[grill C-4]` The one-sided bound alone is satisfied by a store that flushed nothing.
- **SC-028**: With verbose chat disabled, the drill-down surface renders a hidden delegation's transcript using only `GET /api/v1/sessions/{childID}`.
- **SC-029**: With 24 child sessions created after a parent chat, the sidebar lists that parent chat as a root with an expand affordance, the 24 children collapsed beneath it, and zero children counted against the visible-root budget. `[operator 1]`
- **SC-030**: With `agents.defaults.subturn.max_concurrent = 24` and 24 in flight, the **25th** root-level delegation is refused with an `ErrorResult` naming the cap plus an `slog.Error` record, and zero are queued; the resolved cap is asserted to have come from that key and not from the clamped `Performance.EffectiveMaxParallelAgents()`. `[operator 4, 6; grill M-7]`
- **SC-031**: Deleting a parent session removes `<home>/uploads/<id>/` for 100 % of its descendants.
- **SC-032**: All twelve named gate test files exist and each carries the `// ADR-057-W22-inverted` marker; zero are deleted. Whether each *asserts* the new invariant is a recorded **review-gate** sign-off, not an automated criterion. `[grill m-3]`
- **SC-033**: The W22 commit's file list contains zero non-`_test.go` files.
- **SC-034**: `gofmt -l . | wc -l` is 0, `golangci-lint run --build-tags=goolm,stdjson` exits 0, `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 ./...` exits 0 in CI, `govulncheck ./...` reports 0 vulnerabilities, `npm run typecheck` and `npx vitest run` exit 0, and `make verify-contracts` exits 0.
- **SC-035**: **Every** row of the "Negative-gate positive lower bounds" table — currently **13**, and generated by rule 4's membership predicate rather than fixed at eleven — fails **red** when its search target is deliberately renamed (mutation check, #91, run against a `t.TempDir()` copy scoped to the gate's own package, `pkg/gateway` gates in CI only), and each asserts its stated positive lower bound before its zero-assertion — 13 of 13. `[grill C-3; grill2 M2-5 — v2 scored this "11 of 11" while two gates of the same shape (#104, #106) had been added outside the list, one of them the sole coverage of a security property.]` If #91's mutation mechanism is downgraded to a review gate per FR-085, this criterion is scored as a recorded sign-off and that fact is stated here rather than left implicit.
- **SC-036**: With one append and **no** other action — store never closed, no `SetMeta`, no `DeleteSession`, no `CloseSession`, no test-driven tick — `stats.json` on disk is current after one flush interval elapses, in 100 % of trials. `[grill C-4]`
- **SC-037**: After K unflushed appends followed by a `/goal set` and a `Status` transition and then a forced flush, `stats.json` equals K's exact deltas — zero lost, zero double-counted — while `goal.json` and `meta.json` each carry their own writer's value. `[grill C-5]`
- **SC-038**: An instrumented lock wrapper over `CreateSessionWithID` records zero instants at which two session shards are held simultaneously, and records `ClearAll`/`RetentionSweep` acquiring all 64 shards in strictly ascending index order. `[grill C-6]`
- **SC-039**: With distinct parent and child session ids, the inherited grant is **absent** under `{childSessionID, childAgentID}` before the spawn and **present** after, while remaining present under `{parentSessionID, parentAgentID}` — 3 of 3 assertions in one run. `[grill C-1]`
- **SC-040**: A child's pending-approval registry entry carries the child's own session id in 100 % of trials, and a client **approve** on a routing-keyed frame resolves to that entry by approval id and completes the child's tool call. `[grill M-6]`
- **SC-041**: A `ListSessions` interleaved with `DeleteSession` **at FR-102's reconcile→snapshot barrier** returns a result consistent with the stated model — the deleted session MAY be omitted and MUST NOT be a partially-composed meta — with zero panics, zero deadlocks and zero partially-composed metas across 100 interleavings. `[grill M-14; grill2 M2-6, M2-7 — "consistent with the stated model" was unfalsifiable while FR-086's second clause was stricter than the design; FR-097a supplies the prune that makes it true, and FR-102 supplies the barrier that makes "interleaved" reproducible. If FR-102's barrier is not built, this criterion becomes a stress loop with a declared iteration count and the word "deterministic" is struck — recorded, not decided in a test file.]`
- **SC-042**: After N ≫ C terminal delegations past TTL, `len(t.tasks) ≤ C` and `len(t.sessionIndex) ≤ C`; a terminal task within TTL is still present and `delegate action=status` still returns for it. `[grill M-10]`
- **SC-043**: With one parent chat and 24 children, `GET /api/v1/sessions` returns the parent with `child_count == 24` and zero children inline; `?parent_session_id=<parent>&limit=10` returns exactly 10 direct children and a next cursor; **the REST response carries ≤ `limit` rows with body size scaling on `limit` and not on total session count; the SPA holds at most one page; each of the four layers returns exactly the requested window of the recency-ordered sequence, stably across repeated calls; and a warm-cache paged call performs zero per-session disk reads.** `[operator 1; grill M-1; grill2 C2-2 — v2's "each of the four layers bounds its own cost" was not achievable at the store and loop layers and its test was therefore unwritable. The obligation is now split: O(page) at the boundary layers, correctness-stability-and-zero-disk-reads at the backend layers.]`
- **SC-044**: While a 24-way root fan-out runs, a second session's inter-token interval satisfies the **slope** assertion against the pre-change baseline on the same box (no millisecond constant), and the 25th root delegation is refused. `[operator 3, 4; grill M-7]`
- **SC-045**: The W5 classification artefact assigns all 19 session-scoped frame types to (a), (b) or (c), with zero unclassified, and the gate fails if a type is added to `SESSION_SCOPED_FRAME_TYPES` without a class. `[grill M-8]`
- **SC-046**: A Stop on a chat with descendants at depths 1, 2 and 3 produces a `turn_canceled` audit entry whose `descendants_canceled` contains all three turn ids. `[grill M-12]`
- **SC-047**: `CloseSession` is invoked from the child-turn terminal path for each of the four terminal states — 4 of 4 — and after each, the child's grant set, `loadedTools` bucket, `metaCache` entry and `recallSpans` entries are absent. `[grill M-13]`
- **SC-048**: The flush-interval config key exists, resolves to **5 s** with no operator override, and a non-default value is honoured end to end. `[operator 2; grill m-5]`
- **SC-049**: A `CreateSessionWithID` against an id whose directory already exists fails loudly in 100 % of trials, and the pre-existing directory's `transcript.jsonl`, `meta.json` and `stats.json` are byte-unchanged afterwards. `[grill §6 STRIDE note]`
- **SC-050** `[AMENDED 2026-08-04]`: On a **fresh install with no operator override**, the root-delegation cap resolves to `Performance.EffectiveMaxParallelAgents()` — the central authority — and the gate is active at that value (the next-past-cap concurrent root delegation is refused) — asserted with the key absent from config, not set to a value. A configured `< 0` aborts boot with a named error; `0` is the valid unset case. `[grill2 M2-1]` Original (superseded — see top-of-file AMENDMENT note): *"the root-delegation cap resolves to the seeded 16 ... and the resolution did not pass through `Performance.EffectiveMaxParallelAgents()`... A configured ≤ 0 aborts boot with a named error."*
- **SC-051**: A session directory removed **out of band** is absent from the next `ListSessions` result and from `metaCache` and the parent index, in 100 % of trials; a `cacheLoadFailures` exclusion is unaffected. `[grill2 M2-7]`
- **SC-052**: `mutateToolCallInTranscript` against (a) a missing session and (b) a missing entry each produce exactly one WARN and one counter increment, distinguishable by record — zero bare `false` returns on either path. `[grill2 M2-3]`
- **SC-053**: `GET /api/v1/sessions?flat=true` returns every session including subordinates, and the sum of per-session token totals equals the pre-change total for the same logical state — zero spend unaccounted. `flat=true` with `parent_session_id` returns 400. `[grill2 M2-9]`
- **SC-054**: `child_count` and orphan status for a page of `n` roots resolve in work proportional to `n`, not to total session count, across all ten "Session parent index" dataset rows — 10 of 10. `[grill2 C2-2]`
- **SC-055**: With one legacy per-agent store failing mid-merge, a paged `ListAllSessions` returns its healthy rows, a populated `partial_errors`, HTTP 200 and a valid `next_cursor`; consecutive pages neither duplicate nor skip a row, and equal-`UpdatedAt` rows keep a stable session-id tiebreak order across repeated calls. `[grill2 C2-2, M2-10]`
- **SC-056**: Every file returned by the ownership-derivation enumeration (see "Ownership derivation") appears in **exactly one** ownership row — zero unowned, zero doubly-owned, with the two declared `U4→U5→U6` chain files as the only exception; and the wave graph is acyclic with zero units depending on a later wave. Both checked mechanically, with the commands recorded in the completeness check. `[grill2 C2-1, C2-4]`

---

## Acceptance Criteria (verbatim from ADR-057 v4 §10)

> These are carried forward **unchanged and non-negotiable**. Where this spec's Functional Requirements, BDD scenarios or Success Criteria appear to differ in wording, **the ADR text below governs**.

**The governing fact.** Almost every failure in this migration is *success-shaped*: a predicate returns "nothing to do" and every caller proceeds happily. This project's precedent is `plan_engine.go:3937-3944` — a derived `plan:<id>` id that cancelled nothing in production for months while every test passed, because the fake canceller recorded the string it was handed and returned success.

**Three consequences.**

1. **Every criterion below is verified against real store-backed state and real registered turns. A spy or mock that records its argument and returns success is disallowed, without exception.**
2. **The v4 storage criteria (AC-20/21/22) are held to that same bar, and they need it most.** Their failure modes are the quietest in this document: a counter that is 300 tokens light is indistinguishable from a correct one, a re-serialised store is only slower, and a re-added filter still returns a valid response. So AC-20 asserts a **slope** (doubling concurrency must not double wall-clock) rather than a call count; AC-21 asserts on the **session directory's files and their bytes**, not on the composed struct that would look identical either way; and AC-22 asserts on **`stats.json`'s mtime and contents** across a real interval, not on whether a flush function was invoked. The precedent for why this matters is the same one below: a fake that records the string it was handed and returns success proved nothing for months. `pkg/entity/store_crossprocess_test.go` — which re-execs the test binary as real OS processes — is the in-house shape to copy.
3. **AC-1 comes first and gates the rest.** Until `AppendTranscript` fails loudly, a green suite is not evidence: today it `MkdirAll`s the directory, writes the line, fails `readMetaLocked`, logs `slog.Warn("unified_store: could not update meta stats")` and **returns `nil`** (`pkg/session/unified.go:814-823`); `ReadTranscript` on a missing path returns `[]TranscriptEntry{}, nil` (`:1194-1196`). It is a silent **create**, not a silent drop — so an assertion of the form "the append succeeded" can never fail.

| AC | Risk | Criterion |
|---|---|---|
| **AC-1** | R-7 | `AppendTranscript` against a UUID with no `meta.json` returns a non-nil error and creates **no** directory. Each of the four `turn.go` writers plus `websocket.go:4254` surfaces that error (counter + WARN). Then: after one delegation, `<store>/<childID>/meta.json` exists and `GET /api/v1/sessions/{childID}` returns 200 with non-empty messages |
| **AC-2** | R-10, D2 | A test enumerates every read of `routingSessionID` in the non-test tree and fails if it appears outside the closed consumer set (WS payload stamping + the seven role-B predicates + pre-arm keys). Separately: after one delegation, `system.workspace.create` inside the child stamps a non-empty owner equal to the parent's (`WithSessionOwner` installed, `loop.go:6844-6848`) |
| **AC-3** | R-3 | **Client-side bucket membership on the LIVE connection** — not frame delivery, and not after a reconnect. Drive one delegation through the real gateway; assert the SPA store's `<chatSid>` bucket contains the span **and** its steps, `spanByParentCallId` resolves, and `logDiagnostic('chatAttachStepSpanIndexMiss')` never fires. Repeat with a reconnect as a second case. A `producing_session_id` round-trip test covers all 19 session-scoped frame types |
| **AC-4** | R-4, R-6 | A real registered root that finishes gracefully + a real registered `Critical:true` child that does not + a real Stop → assert PHASE B hard-abort **and** PHASE C detach both fire against the child, and the `turn_canceled` audit entry's `descendants_canceled` (`cancel.go:376`) is non-empty and names the child. Separately, the pre-arm race (`cancel_async_delegate_repro_test.go`): a Stop arriving before the child registers is consumed by the child, not expired |
| **AC-5** | R-4 | A live `Critical:true` async delegate + an orphaned root → the ADR-045 watchdog does **not** fire (`hasLiveCriticalDelegate` returns true through `routingSessionID`), and does fire once the delegate finishes |
| **AC-6** | R-2 | A child starts a background `bash`; a chat-level Stop kills it (real PID gone). A `delegate action=cancel` on that child also kills it. A sibling's background shell survives both |
| **AC-7** | R-5 | With a standing grant on the parent, a delegated child executes the granted tool with **no** approval prompt and no 300 s wait. With a pending approval inside a child, a chat-level Stop cancels it (registry entry gone, timer stopped, the child's goroutine unblocks). After the child terminates, its grant set, `loadedTools` bucket and `recallSpans` entries are gone |
| **AC-8** | R-13 | `Interrupt(childB, ScopeSubtree)` cancels B and B's own children, and leaves parent A and sibling C running (the inverted `interrupt_by_session_key_test.go` assertion). `Interrupt(chat, ScopeSubtree)` reaches all three depths |
| **AC-9** | R-1 | `delegate action=status` returns a non-empty activity snapshot for a **sync** delegation (today `executeSync` registers no `DelegateTaskState` at all) and for an async one; the empty path logs |
| **AC-10** | R-8, R-9 | **A concurrency scenario, explicitly.** A 24-way root fan-out while a second session streams tokens: assert the second session's inter-token latency stays within a stated budget, and that W17's gate refuses the 25th rather than queueing it behind the store lock. Assert `GET /api/v1/sessions` paginates and the sidebar still shows the parent chat |
| **AC-11** | R-11 | `follow_up` on a completed child resumes with generation N's history visible in generation N+1's first assembled message list |
| **AC-12** | R-12 | Deleting a parent session removes `<home>/uploads/<childID>/` for every descendant |
| **AC-13** | R-14 | A doc-truth test (or review gate) asserting that `lifecycle.go:225-228`, `:572-575` and `list_jobs_sources.go:311-315` no longer describe `ParentDurableKey` as shared parent↔child |
| **AC-14** | R-15 | The drill-down surface is reachable and populated for a hidden delegation **without** verbose chat enabled, using only `GET /api/v1/sessions/{childID}`. No criterion depends on `subagent_message`/`subagent_state`, which have no emitter |
| **AC-15** | ADR-053 D15 | The per-child message ceiling is enforced per direct parent at depth 3, and a chat's aggregate is (children × ceiling) — asserted, not assumed |
| **AC-16** | ADR-053 D16 | At depth 3, `message_parent` from the grandchild is drained by its **direct parent's** `delegate action=inbox` and by nobody else; producer (`message_parent.go:640`) and consumer (`delegate.go:2024`, `:2200`) agree |
| **AC-17** | D3 gaps | Negative paths: (a) delegate with the lifecycle store unwired → the delegation is **refused** with an operator-visible error, never a silent skip (W7); (b) delegate with `require_parent_agent_id=false` → the child is still reachable by the `ParentDurableKey` walk and a Stop cancels it; (c) a 3P child's own subprocess tree dies with the child's process group |
| **AC-18** | R-16, D6 | **Rewritten in v4 for greenfield — the pre-cutover invariant v3 asserted here is deliberately abandoned.** (a) A repo-wide assertion that `IsDelegateChildEntry` has **zero** references outside tests, and that none of the four read boundaries filters on `ParentSpawnCallID`. (b) After one delegation, the **parent's** `transcript.jsonl` contains no child entry at all — asserted on the file, structurally, not on a rendered response, so the property cannot be satisfied by a filter someone re-adds. (c) On the child's own session, `inspect_session` and `GET /api/v1/sessions/{childID}` return the full transcript. (d) `TranscriptEntry.ParentSpawnCallID` is still stamped on the child's own entries and is read by W19's drill-down. (e) The verifier's window (`verifier_adjudication.go:403`) receives the adjudicated session's own entries and nothing else |
| **AC-19** | migration | A session **in flight** across a deploy: the parent's turn is mid-delegation when the process restarts. Assert the boot sweep reconciles the child's lifecycle record and no transcript write lands in an orphan directory |
| **AC-20** | R-8, R-19 | **D10 sharding — measured against a real on-disk store, never a mock or an in-memory fake.** (a) **Concurrent writes to DIFFERENT sessions do not serialise:** N goroutines each create a session and append transcript lines to their own session concurrently; assert wall-clock completion is close to the *single*-session time, not N× it, on the same box and filesystem — with the same test run against the pre-change store as the baseline it must beat. N is chosen to saturate the box, not fixed by the design (operator: "as many as the box allows"); the assertion is on the **slope** — doubling N must not double the time — so the criterion does not encode a machine-specific constant. (b) `ListSessions` concurrent with an in-flight `NewSession` on an unrelated session does not block on it. (c) Streaming appends to session A are not delayed by a session create for session B (the specific R-8 regression). (d) A lock-order assertion: a race-detector run (`-race`) over concurrent create/append/`SetMeta`/`ListSessions`/`DeleteSession` on overlapping and disjoint ids is clean, and `ClearAll`/`RetentionSweep` interleaved with per-session writes neither deadlocks nor drops a session. (e) Static/review gate: no `cacheMu` critical section contains an `os.*` or `fileutil.*` call |
| **AC-21** | R-17, D11 | **The file split — asserted on the directory, not on the in-memory struct.** (a) After a create plus one `/goal set`, one `/loop` start and one transcript append, the session directory contains `meta.json`, `stats.json`, `goal.json`, `loop.json`, and each file contains **only** its own group's fields. (b) **Writer isolation, byte-level:** a `/loop` tick leaves `goal.json`'s bytes unchanged; a `/goal` round leaves `loop.json`'s unchanged; a transcript append leaves both unchanged. (c) **Composition:** a session directory with `meta.json` only loads successfully with zero-valued stats/goal/loop; a directory with **no** `meta.json` returns an error from `readUnifiedMeta` and 404s through `GET /api/v1/sessions/{id}` — the asymmetry is asserted in both directions, because inverting it re-opens R-7. (d) **Partial-write:** with `goal.json` present but truncated/corrupt, the load surfaces an error for that group rather than silently composing a zero goal. (e) `UnifiedMeta`'s marshalled JSON and every REST/WS payload are byte-identical to pre-split for the same logical state (no contract drift; `make verify-contracts` unaffected). (f) Doc-truth gate, as AC-13: `writeMetaLocked`'s (`:780-785`) and `metaCache`'s (`:166-181`) comments no longer assert a single whole-document write funnel |
| **AC-22** | R-18, D12 | **The throttle — asserted against real store-backed state, with a real clock or an injected fake, never a spy that records its argument.** (a) During a burst of appends within one flush interval, `stats.json`'s **mtime and bytes do not change**, while `transcript.jsonl` grows by exactly one line per append — proving the transcript stayed immediate and only the counters were deferred. (b) After the interval elapses, `stats.json` on disk matches the counters implied by the appended entries **exactly** (no lost or double-counted delta). (c) **Forced flush points each verified independently:** a `SetMeta` with a `Status` patch, `DeleteSession`, and `UnifiedStore.Close` each leave `stats.json` current; re-opening the store reads back the exact counters. (d) **Event-driven writes are provably not throttled:** a `/goal` round's `GoalRoundsUsed`, a `/loop` tick's `LoopRunCount`, a `Status` transition and a `Title` change are each on disk **immediately** after the call returns, with no flush interval elapsed. (e) **Ordering:** `ListSessions` returns a session that just streamed ahead of one that streamed earlier, with no flush in between (the in-memory `UpdatedAt` bump, `:1289-1290`). (f) **The accepted loss is bounded and asserted:** kill the process mid-interval and re-open; the counters are behind by at most the interval's appends and the transcript is complete — asserted, so the loss window is a measured property rather than a hope |

**m-5's warning applies to the whole suite:** `pkg/agent/message_parent_real_context_test.go:16-17` already notes its fixture *"happens to make `ToolTranscriptSessionID`"* equal the seeded id — i.e. an existing test would **not** catch a divergence introduced here. Every criterion above must construct the parent and child ids as *distinct values* and assert on which one was used.

> **Two AC citations differ from the verified tree** (see §"Citation corrections"): AC-1's `websocket.go:4254` is the `ParentSpawnCallID` stamp — the `AppendTranscript` call to convert is `:4256`; and consequence 3's `ReadTranscript` reference `:1194-1196` is verified at `:1192-1194`. Neither changes the criterion.

---

## Traceability Matrix

| Requirement | User Story | Work item(s) | BDD Scenario(s) | Test(s) | ADR AC |
|---|---|---|---|---|---|
| FR-001 | US-1 | W3 | BDD-01 | #1 | AC-1 |
| FR-002 | US-1 | W3 | BDD-02, BDD-03 | #2, #5, #6 | AC-1 |
| FR-003 | US-1 | W3 | BDD-04 | #7 | AC-1 |
| FR-085 | US-1 | W22 | BDD-97 | #91, #3, #9, #12, #17, #19, #27, #29, #58, #81, #82, #83 | AC-1, AC-2 |
| FR-090 | US-1 | W20 | BDD-05 | #3 | AC-2 |
| FR-004 | US-1 | W20 | BDD-05 | #3 | AC-2 |
| FR-005 | US-2 | W1 | BDD-06 | #4, #32 | AC-1 |
| FR-006 | US-2 | W1 | BDD-07, BDD-08 | #4, #34 | AC-2 |
| FR-082 | US-2, US-11 | W1, W15 | BDD-92 | #88 | AC-20 |
| FR-007 | US-2 | W1 | BDD-09 | #33 | AC-11 |
| FR-008 | US-2 | W2 | BDD-10 | #35 | AC-14 |
| FR-009 | US-2 | W1 | BDD-11 | #36 | AC-8 |
| FR-010 | US-2 | W1 | BDD-06 | #32 | AC-1 |
| FR-011 | US-3 | W4 | BDD-12, BDD-13 | #28, #37 | AC-2 |
| FR-012 | US-3 | W4, W5 | BDD-14, BDD-16 | #75, #76 | AC-3 |
| FR-013 | US-3 | W5 | BDD-16, BDD-98, BDD-99 | #75, #95, #96 | AC-3 |
| FR-014 | US-3 | W4 | BDD-17 | #29 | AC-2 |
| FR-015 | US-3, US-5 | W4 | BDD-23, BDD-24, BDD-26, BDD-27 | #38, #39, #41, #42 | AC-4, AC-5 |
| FR-016 | US-3 | W4 | BDD-13 | #37 | AC-4 |
| FR-017 | US-3 | W21 | BDD-14, BDD-15 | #76, #77 | AC-3 |
| FR-018 | US-3 | W5 | BDD-16, BDD-98, BDD-99 | #75, #95, #96 | AC-3 |
| FR-089 | US-3 | W5 | BDD-16, BDD-98, BDD-99 | #75, #95, #96 | AC-3 |
| FR-019 | US-4 | W6 | BDD-18 | #10 | AC-17 |
| FR-020 | US-4 | W6 | BDD-19 | #11 | AC-17 |
| FR-021 | US-4 | W7 | BDD-20 | #48 | AC-17 |
| FR-022 | US-4 | W6 | BDD-22 | #12 | AC-13 |
| FR-023 | US-4 | W6 | BDD-18, BDD-21 | #10, #49, **#106** | AC-17 |
| FR-024 | US-5 | W8 | BDD-23, BDD-24 | #38, #39 | AC-4 |
| FR-025 | US-5 | W8 | BDD-30 | #45 | AC-4 |
| FR-026 | US-5 | W8 | BDD-30 | #45 | AC-4 |
| FR-027 | US-5 | W9 | BDD-28 | #43 | AC-6 |
| FR-028 | US-5 | W9 | BDD-29 | #44 | AC-6 |
| FR-029 | US-5, US-18 | W9 | BDD-86 | #68a | AC-17 |
| FR-030 | US-5 | W8 | BDD-25, BDD-100 | #40, #97 | AC-4 |
| FR-031 | US-6 | W10 | BDD-31, BDD-32, BDD-88 | #25, #85 | AC-7 |
| FR-079 | US-6 | W10 | BDD-89 | #84 | AC-7 |
| FR-032 | US-6 | W10 | BDD-33 | #46 | AC-7 |
| FR-080 | US-6 | W10 | BDD-90 | #86 | AC-7 |
| FR-081 | US-6 | W10 | BDD-91 | #87 | AC-7 |
| FR-033 | US-6 | W10 | BDD-34 | #47 | AC-7 |
| FR-088 | US-6 | W10 | BDD-96 | #94 | AC-7 |
| FR-034 | US-7 | W11 | BDD-35, BDD-40 | #58, #61 | AC-18, R-16 |
| FR-035 | US-7 | W11 | BDD-35, BDD-37, BDD-39 | #57, #58, #60 | AC-18 |
| FR-036 | US-7 | W11 | BDD-38 | #59 | AC-18 |
| FR-037 | US-7 | W11 | BDD-35 | #58 | AC-18 |
| FR-038 | US-7 | W11 | BDD-36 | #56 | AC-18 |
| FR-039 | US-8 | W12 | BDD-42, BDD-44 | #50, #52 | AC-8 |
| FR-040 | US-8 | W12 | BDD-41, BDD-43 | #50, #51 | AC-8 |
| FR-041 | US-9 | W13 | BDD-45, BDD-48 | #27, #53 | AC-8 |
| FR-042 | US-9 | W13 | BDD-46, BDD-47 | #53, #54 | AC-8 |
| FR-043 | US-10 | W14 | BDD-51 | #30 | AC-9 |
| FR-044 | US-10 | W14 | BDD-49, BDD-50 | #55 | AC-9 |
| FR-045 | US-10 | W14 | BDD-52 | #31, #93 | AC-9 |
| FR-087 | US-10 | W14 | BDD-52 | #31, #93 | AC-9 |
| FR-046 | US-14 | W19 | BDD-71, BDD-74 | #78 | AC-14 |
| FR-047 | US-14 | W19 | BDD-74, BDD-97 | #78, **#109** | AC-14 |
| FR-048 | US-11 | W15 | BDD-53, BDD-55, BDD-57 | #8, #69, #70, #72 | AC-20 |
| FR-049 | US-11 | W15 | BDD-57 | #9 | AC-20 |
| FR-050 | US-11 | W15 | BDD-56, BDD-57 | #9, #73 | AC-20 |
| FR-051 | US-11 | W15 | BDD-54 | #71 | AC-20 |
| FR-086 | US-11 | W15 | BDD-95 | #92 | AC-20 |
| FR-052 | US-11 | W15 | BDD-53 | #70, **#107** | AC-20 |
| FR-053 | US-12 | W23 | BDD-58 | #13 | AC-21 |
| FR-054 | US-12 | W23 | BDD-59 | #17 | AC-21 |
| FR-055 | US-12 | W23 | BDD-60, BDD-61 | #14, #15 | AC-21 |
| FR-056 | US-12 | W23 | BDD-62 | #16 | AC-21 |
| FR-057 | US-12 | W23 | BDD-63 | #18 | AC-21 |
| FR-058 | US-12 | W23 | BDD-58 | #13, **#103** | AC-21 |
| FR-059 | US-12 | W23 | BDD-64 | #19 | AC-21 |
| FR-060 | US-12 | W23 | BDD-61 | #15, **#104** | AC-21 |
| FR-061 | US-13 | W24 | BDD-65 | #20 | AC-22 |
| FR-084 | US-12, US-13 | W23, W24 | BDD-94 | #90 | AC-22 |
| FR-062 | US-13 | W24 | BDD-65 | #20 | AC-22 |
| FR-063 | US-13 | W24 | BDD-66, BDD-70, BDD-93 | #21, #74, #89 | AC-22 |
| FR-083 | US-13 | W24 | BDD-93 | #89 | AC-22 |
| FR-064 | US-13 | W24 | BDD-67 | #22 | AC-22 |
| FR-065 | US-13 | W24 | BDD-68 | #23 | AC-22 |
| FR-066 | US-13 | W24 | BDD-69 | #24 | AC-22 |
| FR-067 | US-13 | W24 | BDD-93 | **#105** | AC-22 |
| FR-068 | US-14 | W16 | BDD-72, BDD-73, BDD-102 | #79, #80, #100 | AC-10 |
| FR-092 | US-14, US-19 | W16 | BDD-102 | #100 | AC-10 |
| FR-069 | US-15 | W17 | BDD-75, BDD-77 | #63 | AC-10 |
| FR-095 | US-15 | W17 | BDD-75 | #63, **#110** | AC-10 |
| FR-070 | US-15 | W17 | BDD-76 | #64 | AC-10 |
| FR-071 | US-16 | W18 | BDD-78, BDD-79 | #26, #62 | AC-12 |
| FR-072 | US-17 | W22 | BDD-80, BDD-97 | #81, #91 | — (process item; W22 has no ADR AC) |
| FR-073 | US-17 | W22 | BDD-81, BDD-97 | #82 | — (process item; W22 has no ADR AC) |
| FR-074 | US-17 | W22 | BDD-82 | #83 | all (m-5) |
| FR-075 | US-18 | W1 | BDD-83 | #68 | AC-11 |
| FR-076 | US-18 | W12 | BDD-84 | #67 | AC-15 |
| FR-077 | US-18 | W12, W14 | BDD-85 | #66 | AC-16 |
| FR-078 | US-18 | W6, W17 | BDD-87 | #65 | AC-19 |
| FR-096 | US-2 | W1 | BDD-107 | #111 | AC-1 |
| FR-091 | US-19 | W16 | BDD-101, BDD-103, BDD-106 | #98, #99, #108 | AC-10 |
| FR-093 | US-19 | W16 | BDD-104 | #101 | AC-10 |
| FR-094 | US-19 | W16 | BDD-105 | #102 | AC-10 |
| FR-097 | US-19 | W16 | BDD-112, BDD-101, BDD-106 | #116, #98, #108 | AC-10 |
| FR-097a | US-11, US-19 | W15, W16 | BDD-110, BDD-95 | #114, #92 | AC-20 |
| FR-098 | US-19 | W16 | BDD-113, BDD-103 | #117, #99 | AC-10 |
| FR-099 | US-1 | W3 | BDD-109 | #113 | AC-1 |
| FR-100 | US-9 | W13 | BDD-114 | #118 | AC-8 |
| FR-101 | US-2, US-11 | W15 | BDD-92 | #88 | AC-20 |
| FR-102 | US-11 | W15 | BDD-95 | #92 | AC-20 |
| FR-103 | US-12, US-19 | W23, W16 | BDD-58, BDD-102 | #103, #100 | AC-21 |
| FR-104 | US-19 | W16 | BDD-111 | #115 | AC-10 |

**Completeness check (v3)**: **104 FRs** (FR-001 … FR-104, plus the sub-requirement FR-097a), every row carrying at least one BDD scenario and at least one test. **114 BDD scenarios** (BDD-01 … BDD-114, machine-verified contiguous: 114 headings, 114 unique ids, min 01, max 114, zero gaps, zero duplicates), every one of which appears in at least one row. **19 user stories**, each with ≥ 1 acceptance scenario. **56 success criteria** (SC-001 … SC-056). **119 test entries** (#1 … #118 plus #68a). Every ADR acceptance criterion AC-1 … AC-22 is referenced by at least one row, and AC-1 … AC-22 remain **byte-identical** to ADR-057 v4.

> `[grill2 m2-3]` v2's check asserted "**106** BDD scenarios" while the document contained **107** — and its own changelog header said 107. A structural check that is itself wrong is worse than none, so v3's counts were produced by command rather than by hand. **Commands run, for re-verification:**
>
> ```bash
> S=docs/internal/specs/adr-057-session-unification-spec.md
> # BDD: count, uniqueness, contiguity
> grep -o '^#### BDD-[0-9][0-9]*' $S | sed 's/.*BDD-//' | sort -n > /tmp/bdd
> wc -l < /tmp/bdd; sort -nu /tmp/bdd | wc -l; head -1 /tmp/bdd; tail -1 /tmp/bdd
> sort -n /tmp/bdd | awk 'NR==1{p=$1;next}{if($1!=p+1)print "GAP/DUPE "p" -> "$1; p=$1}'
> # FRs and SCs
> grep -c '^- \*\*FR-[0-9]*\*\*' $S
> grep -c '^- \*\*SC-[0-9]*\*\*' $S
> # Ownership: disjoint AND exhaustive + wave DAG acyclic (SC-056)
> #   Parse the ownership table, extract every path, cross-check against the symbol
> #   enumeration in "Ownership derivation"; then parse the wave block and check
> #   every declared dependency points at a STRICTLY earlier wave.
> ```
>
> **Result of the ownership / wave-DAG check, run 2026-08-03 on `feature/plan-swimlane-board`:**
>
> | Property | Result | Detail |
> |---|---|---|
> | **Disjoint** | **PASS** | 74 distinct paths, 28 units, **0 collisions**. The only multi-owner paths are `pkg/session/unified.go` and `pkg/session/retention_sweep.go`, both the declared `U4→U5→U6` chain (Rule 1) |
> | **Exhaustive** | **PASS** | 57 files returned by the enumeration; **0 unowned**; 17 explicitly out of scope with stated reasons |
> | **Wave DAG** | **PASS** | 28 units placed across waves A–H; **0 forward and 0 unknown dependencies**; acyclic by construction. The one same-wave edge (U2↔U5, Wave C) is a declared **frozen contract** under Rule 7, not an ordering edge |
>
> **This check is what found the eleventh unowned file** — `pkg/sysagent/tools/diag.go` — *after* grill #2's ten had already been assigned. v2 described its table as "machine-checked" without stating what was checked; v3 states the property, the command, and the result.

**Two columns now legitimately read `—`.** `[grill M-11]` FR-072 and FR-073 are **process** requirements (W22's commit shape). W22 has **no** acceptance criterion in ADR-057 §10, and v1 filled the column with **AC-8**, which is the interrupt-scope criterion and has nothing to do with commit shape. A fabricated mapping is worse than a blank one: it makes a structural completeness check pass while the row means nothing. The column is now blank with a stated reason.

**BDD numbering is by user story, not by document position.** Scenarios added in v2 are placed with the story they trace to, so BDD-88 … BDD-105 appear throughout the document rather than appended at the end. Contiguity of the **set** is what the check above asserts.

### Work-item coverage map (W1 … W24 — nothing deferred)

| W | Summary | User Story | Work Unit | FRs |
|---|---|---|---|---|
| W1 | Exact-id session create; copy parent `Owner`; delete `NoHistory` | US-2, US-18 | U2 (store), U7 (agent) | FR-005…FR-007, FR-009, FR-010, FR-075 |
| W2 | `SessionMeta.ParentSessionID` + subordinate `UnifiedSessionType` + OpenAPI + SPA | US-2, US-14, **US-19** | U5 (store), U10 (contract), U12 (SPA) | FR-008, **FR-091** (its consumer) |
| W3 | **`AppendTranscript` itself becomes strict** + all 20 invocation sites surface the error + the mutate path | US-1 | U2, **U5** (W3a, the primitive), U3, U11, **U22** (W3b/W3c), **U26** (W3d) | FR-001…FR-003, **FR-099** |
| W4 | `turnState.routingSessionID`; re-base 7 predicates + pre-arm keys | US-3 | U3, U7, U8, U9, U15 | FR-011, FR-014…FR-016 |
| W5 | WS contract: routing key + `producing_session_id`; **classify** all 19 frame types | US-3 | U10, U11, U12, **U23** | FR-012, FR-013, FR-018, **FR-089** |
| W6 | `LifecycleFilter.ParentDurableKey` + parent index + 3 doc rewrites + boot sweep | US-4, US-18 | U13, U14, **U19** (`boot_sweep.go`) | FR-019, FR-020, FR-022, FR-023, FR-078 |
| W7 | Refuse delegation with no lifecycle store | US-4 | U14, U19 | FR-021 |
| W8 | Subtree computed once in PHASE A; durable walk; per-descendant transitions | US-5 | U15 | FR-024…FR-026, FR-030 |
| W9 | Shell ownership + cascade kill + `delegate cancel` kills shells + **3P process group (W9c, real sites in `pkg/agent/runner/`)** | US-5, US-18 | U14, U16, **U22** (W9c) | FR-027…FR-029 |
| W10 | Grants re-keyed **two-key**; pending-registry re-key + approve round-trip; child `CloseSession` **call site** + **the teardown flush/eviction it must perform (W10e)** | US-6 | U7 (call sites, W10d), U9 (grant read), U11 (WS), **U17a** (signatures), **U17b** (teardown, W10e) | FR-031…FR-033, **FR-079, FR-080, FR-081, FR-088** |
| W11 | Delete `IsDelegateChildEntry` + 4 filter sites + 3 comment blocks | US-7 | U5 (predicate), U18 (sites) | FR-034…FR-038 |
| W12 | Ancestor-chain ownership walk at 6 call sites; inbox producer+consumer move together | US-8, US-18 | U14 (incl. `message_parent.go`) | FR-039, FR-040, FR-076, FR-077 |
| W13 | One interrupt entry point with explicit `InterruptScope`; **5 compile-breaking files across 4 owners + ~25 doc-comment sites** | US-9 | U8 (+ `pkg/commands/**`), **U14** (W13c), **U15**, **U19** (W13d) | FR-041, FR-042, **FR-100** |
| W14 | `recentActivityLines` fix; `executeSync` registers state; **bounded** map eviction | US-10 | U14 | FR-043…FR-045, **FR-087** |
| W15 | Stripe `UnifiedStore.mu`; narrow `cacheMu`; one-directional lock order **with the stated two-session protocol**; `ListSessions` consistency model **+ the prune it requires**; **`retention_sweep.go` shard-order conversion (W15b)**; **three test seams** | US-11 | U4 (+ U2 for the two-session protocol), U6 (FR-097a, FR-102), U5 (FR-103) | FR-048…FR-052, **FR-082, FR-086, FR-097a, FR-101, FR-102, FR-103** |
| W16 | Pagination through all four **owned** layers; **nested-under-parent listing**, sidebar tree, search tree, **parent index, cross-store cursor contract, flat usage listing** | US-14, **US-19** | **U6** (store), **U9** (loop), U18 (REST), U12 (client + W16h), **U24** (sidebar+search), U10 (contract), **U4** (index surface), **U25** (W16i consumers), **U26** (W16j consumers) | FR-068, **FR-091, FR-092, FR-093, FR-094, FR-097, FR-097a, FR-098, FR-104** |
| W17 | Root-level delegation admission gate, cap from `subturn.max_concurrent`, **seeded default** | US-15, US-18 | U19, **U28** (W17b seed) | FR-069, FR-070, FR-078, **FR-095** |
| W18 | Child uploads directory reachable by cascade-delete — **W18a primitive (U20), W18b wiring (U18)** | US-16 | U20, U18 | FR-071 |
| W19 | Drill-down surface as the stated inspection surface | US-14 | U12, U18, U24 | FR-046, FR-047 |
| W20 | Named ID types (`SessionID`, `RoutingSessionID`) **+ a stated conversion boundary** | US-1 | U1 | FR-004, **FR-090** |
| W21 | Pin `SubTurn*Payload.SessionID`; re-point `DelegateTaskState.SessionID` | US-3 | **U23** (types), U7 (assignment), U14 (`DelegateTaskState`) | FR-017 |
| W22 | Deliberately invert the 12 gate tests, in their own commit; **enforce binding rule 4 across the suite** | US-17, **US-1** | U21 | FR-072…FR-074, **FR-085** |
| W23 | Split `meta.json` into four files + 2 doc rewrites + **field-group-only cache mutation** | US-12 | U5 | FR-053…FR-060, **FR-084** |
| W24 | Throttle the counter path; forced flush points **with one stated ordered `DeleteSession` sequence**; **unforced periodic flush**; event writes immediate; **flush-interval config key (W24b)** | US-13 | U6, **U17b** (teardown flush), **U28** (W24b key) | FR-061…FR-067, **FR-083** |

**Hardest to place, stated honestly:**

- **W2** is split three ways (store field, OpenAPI enum, SPA enum) across three units. Its ADR justification also *narrowed* in v4 — with the filter deleted, the "filter discriminator" rationale is gone and only R-9 (listing) and W19 (drill-down) remain. **It now has a named consumer** `[grill §5, W2]`: v1 left `ParentSessionID` with no requirement that anything **read** it, so W2 could have shipped as a write-only field with every test green. **FR-091 is that consumer** — the nested listing filters and groups on it, and BDD-101/103/106 fail if it is unread.
- **W17** sits between US-15 and US-18: it is a concurrency gate, but its acceptance evidence (`docs/internal/uat/max-parallel-concurrency-gap-2026-07-31.md` §G1) is a UAT observation rather than a code-derived requirement, and it is required *by* this ADR rather than *of* it. Its cap value is settled by operator decision 4 and pinned by FR-095.
- **W22** is a process requirement, not a behaviour. It gets a P0 user story because the commit shape is the only thing that keeps bisection honest, and because ~430 references make "quietly delete the failing test" the path of least resistance. **It has no ADR acceptance criterion**, and FR-072/FR-073's AC column reads `—` rather than borrowing AC-8 as v1 did `[grill M-11]`. v2 also gives it FR-085 (binding rule 4), because "the suite is the specification" is worthless if eleven of its gates pass on an empty search.
- **W11's predicate deletion vs its call sites** land in two different units (U5 owns `daypartition.go`, U18 owns the four call sites) purely for file-ownership safety, and now **three waves apart** (C and F). v1 flagged that they "must land in the same integration window" and provided no mechanism `[grill §5, W11]`. **Hard ordering 6 is that mechanism**: U5 lands a deprecation shim that keeps the method compiling and always returns `false`; U18's commit removes the shim and the call sites together; test #58's positive lower bound fails if the shim survives.

---

## Ambiguity Resolution (operator, 2026-08-03)

> All twelve items in v1's Ambiguity Self-Audit are **resolved**. Items 1–6 were put to the operator explicitly; 7–12 are agent defaults the operator reviewed and did not override. **None of these is open. Do not re-litigate them.**

| # | What was ambiguous | **DECISION** | Landed in |
|---|---|---|---|
| **1** | **R-9 listing policy** — ADR §9's one remaining open question. Subordinate sessions hidden by default with an opt-in flag (the `verifier` precedent, `pkg/gateway/rest.go:783-785` + `?include_verifier=true`), or shown **nested under their parent**? | **NESTED UNDER PARENT.** The operator **overrode** the agent's stated default (copy the `verifier` precedent). Children appear as an expandable tree; the sidebar, `SearchModal` and pagination must all learn hierarchy. This is **real UI and API work**, scoped as such — not a filter flag | **US-19** (6 acceptance scenarios), FR-091…FR-094, BDD-101…BDD-106, tests #98–#102, #108, SC-029, SC-043, dataset "Session-list hierarchy and paging", **new unit U24**, W16 rewritten |
| **2** | **D12 flush interval default** | **5 seconds, exposed as a config key** — and **MUST**, not `SHOULD` (which also closes `[grill m-5]`, the spec's only `SHOULD`, uncovered by SC-034's gate list and therefore unenforced in either direction) | FR-067, #105, SC-048 |
| **3** | **AC-10's "stated budget"** for inter-token latency, which is stated nowhere | **Slope assertion**, as AC-20 already uses, baselined on the **pre-change store**. No machine-specific millisecond constant. Recorded as the operative reading of AC-10(a); AC-10's text is unamended | #72 (given a concrete assertion), SC-044 |
| **4** | **Root-level delegation cap value (W17)** — the UAT observed "24 parallel against a cap of 16"; the ADR names no new cap | **Reuse the existing per-agent `maxConcurrent`. No new config knob.** Specifically `agents.defaults.subturn.max_concurrent`, honoured **unclamped** when explicitly set (an override); when unset, resolves to `Performance.EffectiveMaxParallelAgents()` — the central authority, no longer capped at 16 by `clampParallelExplicit` as of commit `536b7340` `[AMENDED 2026-08-04]`. AC-10's 24/25 topology still runs **as written** either way — see the changelog's note on M-7 and FR-095's amendment | FR-095, BDD-75, #63, **#110**, SC-030, SC-044 |
| **5** | **`InterruptScope` at existing call sites** — D8 makes the scope mandatory but does not say which scope each of today's callers gets | **`ScopeSubtree` rooted at the child** for `delegate action=cancel`. **R-13's behaviour change is INTENDED and confirmed** — cancelling a child cancels that child **and its descendants**, where today it cancels one turn and leaves the grandchildren running | FR-041, FR-042, BDD-46, BDD-47, regression dataset row 3 |
| **6** | **What "operator-visible" means** for a refused delegation (W7, W17) | **Tool error + `slog.Error`**, mirroring `pkg/tools/delegate.go:1150-1159`. **No separate user-facing notification** | FR-021, FR-069, BDD-77, BDD-20 |
| 7 | **Corrupt-group-file blast radius (FR-056)** | Per-**group** error; the session stays listable and deletable rather than becoming a permanently stuck row *(agent default accepted)* | FR-056, BDD-62, #16 |
| 8 | **`cacheLoadFailures` after striping** | **Preserve the documented accepted limitation exactly** — a session that fails to load at construction stays excluded for the process lifetime (`pkg/session/unified.go:184-192`). Changing it is out of scope *(agent default accepted)* | FR-048, Assumptions |
| 9 | **Widen `ParentSpawnCallID` stamping** to tool-call and error entries? | **No.** Leave the three existing writers alone (`pkg/agent/turn.go:1204`, `:1268`, `pkg/gateway/websocket.go:4254`); widening is unrequested scope. The field's new job is provenance for W19's drill-down *(agent default accepted)* | FR-036, BDD-38, Edge Cases |
| 10 | **Audit-query impact** — ADR §3.1 marks "no aggregation consumer groups audit entries by chat session" as **[INFERRED]**: not verified, only unfound | **Proceed on the inference, keep it flagged.** It remains `[INFERRED]` and is called out here so a reviewer can challenge it rather than inherit it silently *(agent default accepted)* | regression dataset row 6, Assumptions |
| 11 | **`replay_done` / `rate_limit` frame gaps** | **Audit per W5, document, do NOT fix.** Both are pre-existing strains this change *exposes* — verified: `RateLimitPayload` has no `SessionID` field (`pkg/agent/events.go:525-533`) and `replay_done` appears tree-wide only at `src/store/chat.ts:1238` *(agent default accepted)* | **BDD-99 (class c)**, FR-089, #96, Assumptions |
| 12 | **`metaCache` × D12 dirty set shape** | **Separate dirty-session set guarded by `cacheMu`; the cache entry stays a plain clone** *(agent default accepted)*. **Note `[grill C-5]`:** this settles *where the dirty set lives* and does **not** settle the harder question one layer up — that the shared mutable `*UnifiedMeta` is itself the fused document, so a wholesale cache replace by any targeted writer discards unflushed deltas. That is FR-084 | FR-084, BDD-94, #90, SC-037 |

### Newly surfaced during the v2 correction pass — agent default applied, flagged for the operator

> These are **not** blockers and none belongs to the 20 grill findings. They are recorded rather than silently decided, in keeping with this spec's own standard.

| # | Question | Agent default applied | Why it is defensible |
|---|---|---|---|
| 13 | **What bounds the number of `sessionLock` shards held by `ClearAll`?** Holding all 64 while `RetentionSweep` walks the whole tree (`pkg/session/retention_sweep.go:35`) is a full-store stall | Accept the stall; assert **no deadlock and no dropped session** (AC-20(d), FR-050) and do not add a batching scheme | The operation is already store-global today under one write lock, so 64-in-index-order is not a regression. Batching would reintroduce the lock-order question this design just closed |
| 14 | **Is `metaCache` bounded?** W24 makes it the authoritative counter home; a long-lived gateway with repeated 24-way fan-outs now caches a `*UnifiedMeta` per **child** session for the process lifetime | Evict a child's `metaCache` entry at `CloseSession` (already required by FR-033/FR-088) and treat that as the bound | The eviction point already exists in this change set, so the growth D1 introduces is retired by the teardown D1 also introduces. A general cache bound is a separate concern that predates this ADR |
| 15 | **Does `producing_session_id` survive replay?** BDD-15 asserts span/step correlation after a reconnect but nothing says whether replayed frames carry it or reconstruct it | Replayed frames **carry** it, persisted per entry; they do not reconstruct it | Reconstruction would need the parentage edge at render time, which is precisely the derived-value habit ADR-057 exists to remove |
| 16 | **`follow_up` at generation N+2** — `follow_up` reuses `childID` verbatim (`pkg/agent/subturn.go:1115-1135`), so generation N+2 runs against a session that already has two generations of history | Covered by FR-075/BDD-83's property, which is generation-agnostic; no per-generation cap is introduced | The requirement is "generation N is visible to N+1", which composes. If a context-budget problem emerges it is `windowTrim`'s (ADR-028), not this ADR's |

---

## Evaluation Scenarios (Holdout)

> **HOLDOUT — post-implementation evaluation only.** These MUST NOT be visible to the implementing agent during development. They are deliberately absent from the TDD plan, the datasets and the traceability matrix, and no FR or BDD scenario references them.

### H-1 — A four-deep chain cancels completely from the root
- **Setup**: chat `A` → child `B` → grandchild `C` → great-grandchild `D`, all live, `D` running a real background `bash`.
- **Action**: a single chat-level Stop on `A`.
- **Expected outcome**: all four turns reach a cancelled state, all four lifecycle records read `cancelled`, `D`'s real PID is gone, and the `turn_canceled` audit entry names three descendants.
- **Category**: Happy Path

### H-2 — Two unrelated chats delegating concurrently stay isolated
- **Setup**: chats `P` and `Q`, each with two live children, run by the **same** agent id.
- **Action**: Stop `P`.
- **Expected outcome**: `P`'s two children are cancelled; `Q`'s two children are untouched and complete normally. Nothing in `Q` appears in `P`'s audit descendant list.
- **Category**: Happy Path

### H-3 — A delegation that streams heavily leaves an exact, complete record
- **Setup**: one child streams ≥ 500 assistant tokens across ≥ 50 transcript entries with a mix of models.
- **Action**: let the child complete, then close the store gracefully and re-open it.
- **Expected outcome**: `transcript.jsonl` has exactly the entries appended; `stats.json`'s `TokensTotal`, `Cost`, `ToolCalls`, `MessageCount` and per-model `ByModel` breakdown match the entries exactly; `goal.json` and `loop.json` are absent or zero-valued.
- **Category**: Happy Path

### H-4 — A delegation attempted with a read-only session directory
- **Setup**: make the sessions base directory read-only immediately before a delegation.
- **Action**: dispatch one delegation.
- **Expected outcome**: the session create fails with a non-nil, operator-visible error; the delegation is refused; **no** turn runs against a session that does not exist; and no transcript line is written anywhere.
- **Category**: Error

### H-5 — Ownership walk against a lifecycle record whose parent record was deleted
- **Setup**: a depth-3 tree; delete the depth-2 record from the lifecycle store out of band.
- **Action**: the root chat invokes `delegate action=cancel` on the depth-3 child.
- **Expected outcome**: the walk terminates on the broken link and **rejects** the action with an ownership error; it does not fall through to permit, and it does not scan the whole store looking for a path.
- **Category**: Error

### H-6 — Two processes writing the same session's stats concurrently
- **Setup**: two OS processes (re-exec'd test binaries) open stores rooted at the same directory and both stream into the same session id.
- **Action**: let both flush.
- **Expected outcome**: on POSIX, `stats.json` is a valid, parseable document with no interleaved bytes and no lost file; the documented Windows limitation (no cross-process locking, `pkg/fileutil/flock_windows.go`) is stated rather than silently assumed away.
- **Category**: Edge Case

### H-7 — A child whose id collides with an existing session directory
> **v2 note (holdout-side only):** this scenario's *property* was promoted to FR-096 / BDD-107 / #111 so the implementing agent is obliged to defend against it. The scenario below stays a holdout and is **not** referenced anywhere in the visible plan.
- **Setup**: pre-create a session directory whose name equals the `childID` the next delegation will mint.
- **Action**: dispatch that delegation.
- **Expected outcome**: the collision is detected and surfaced — the delegation either fails loudly or mints a fresh id; under no circumstance does the child silently adopt the pre-existing session's transcript, meta, owner or stats.
- **Category**: Edge Case

---

## Assumptions

- The `#576–#588` fix wave is already an ancestor of this branch (`0ee87fbe` reachable from the ADR commits), so W22's test inversions do not collide with concurrent edits. ADR-057 v4 operator decision 2 removed the sequencing gate; this remains an integration consideration, not a blocker.
- Greenfield holds: no session written before the cutover needs a reader for a fused `meta.json`, and no config migration is required.
- CI is the authority for Go test and build results. This spec assumes no implementer runs the full Go suite in the dev pod.
- `UnifiedMeta` is not a wire type in its own right — the REST/WS payloads derive from it — so W23 requires no `contracts/` change. W5 does, and follows Constraint #8's 5-step pipeline.
- The 64-shard constant is chosen to match the in-house precedent (`pkg/session/lifecycle_lock.go:17`, `pkg/entity/lock.go:12`), not to bound throughput.
- Windows has no cross-process file locking anywhere in the file-store family (`pkg/fileutil/flock_windows.go` is a no-op). W15's cross-process assertions are POSIX-only, matching `pkg/entity/store_crossprocess_test.go`'s `//go:build !windows` gate. This is an accepted, documented limitation, not a gap this spec closes.
- The **W5 audit artefact** (FR-089) is produced before U11/U23 stamp anything, so no frame type is stamped against a class nobody assigned.
- `agents.defaults.subturn.max_concurrent` is honoured **unclamped** when > 0 (`pkg/agent/subturn.go:64-69`). `[AMENDED 2026-08-04]` The predicted failure mode ("if a future change routes it through `clampParallelExplicit`") did not literally occur — instead, commit `536b7340` removed `clampParallelExplicit`'s ceiling ENTIRELY, so `Performance.EffectiveMaxParallelAgents()` (the OTHER path) is no longer capped at 16 either. That is a different route to the same outcome this tripwire was watching for: FR-095's contract (see its own `[AMENDED 2026-08-04]` note) depended on the two paths staying numerically distinct, and they no longer reliably are unless an operator has set `subturn.max_concurrent` explicitly. #110 (`TestRootDelegationCap_SourcedFromSubTurnMaxConcurrent`) still passes — it deliberately sets the two paths to different values (24 vs 8) precisely so an explicit override is provably distinguished from the central value, which remains true post-amendment.
- The listing design (US-19) paginates over **roots** and fetches children on expand. This is a **breaking change to the `GET /api/v1/sessions` response shape**, which greenfield permits (ADR-057 v4 operator decision 1) and which travels through Constraint #8's pipeline as part of W16 — **not** as part of W23, whose AC-21(e) "byte-identical, `verify-contracts` unaffected" is scoped to the file split alone. **The break has three named consumers, not zero** `[grill2 M2-9, M2-10]`: `Sidebar.tsx:172`, `SearchModal.tsx:363` and `UsageScreen.tsx:282` all call `fetchSessions()`, and four SPA test files assert its exact call shape — all now owned and listed under "SPA tests that MUST be deliberately inverted". The existing two-variant `oneOf` collapses to one inline-hosted `SessionPage` schema per ADR-034.
- **The store-layer sort is over resident data and this is a design decision, not an oversight** `[grill2 C2-2]`. `metaCache` holds every session's composed meta and FR-058 asserts a cache hit costs zero disk reads, so `ListSessions`'s O(N log N) sort touches no filesystem. No persisted recency index is introduced. If the session count ever grows to where an in-process sort of resident metadata is the bottleneck, the resident cache itself is the prior constraint to revisit — not the sort.
- **The 3P process-group requirement (FR-029) is POSIX-only.** `Setpgid` and process-group signalling have no equivalent in this codebase on Windows; `pkg/agent/runner/procgroup_windows.go` is a documented no-op, matching how W15's cross-process assertions are scoped and how `pkg/fileutil/flock_windows.go` is treated. AC-17(c) is asserted on POSIX.
- ~~**The seeded `agents.defaults.subturn.max_concurrent = 16` changes no shipped behaviour**~~ `[AMENDED 2026-08-04 — see the top-of-file AMENDMENT note]` **This premise no longer holds and the seed has been REMOVED.** The claim depended on 16 already being what `clampParallelExplicit` capped `Performance.EffectiveMaxParallelAgents()`'s fallback at — commit `536b7340` removed that ceiling, so the seed became a second, independent, lower cap the instant an operator's central `max_parallel_agents` diverged from 16 (verified: this environment's auto-detected value is 486, not 16). Corrected: `agents.defaults.subturn.max_concurrent` is left UNSET (Go zero value) on a fresh install; both consumers (`getSubTurnConfig`, `ResolveRootDelegationCap`) resolve it live to `Performance.EffectiveMaxParallelAgents()` instead. Original (superseded) text: *"16 is already what `clampParallelExplicit` caps the fallback at, so the seed makes the effective value explicit rather than altering it. It exists so the resolver never takes FR-095's forbidden branch and so an operator has one number to raise."* `[grill2 M2-1]`
- **Out of scope**: plan cancellation (D9), throttle unification (ratified cut, W17 excepted), `migrateLegacy`/`writeUnifiedMetaDirect`, and any fix to the pre-existing `rate_limit`/`replay_done` frame gaps (operator decision 11 — audit and document, do not fix).

## Clarifications

### 2026-08-03

- Q: Should the transcript visibility filter be scoped or deleted? → A: **Deleted** at all four sites. Operator decision 1 (greenfield) lifted the no-migration constraint v3 was designing around. Historical chats surfacing previously-hidden delegate narration is accepted (R-16).
- Q: Does this wait for the `#576–#588` wave to close? → A: **No.** Operator decision 2 removed the gate; this is bug resolution and simplification, and the wave already landed.
- Q: Strict direct-parent ownership, or an ancestor walk? → A: **Ancestor-chain walk**, depth-bounded (operator decision 3). It preserves root-over-subtree control and removes the sibling/cousin leak — better than both options v2 offered.
- Q: Stripe the store lock, or move the fsyncs out of it? → A: **Stripe it** — 64 shards on the in-house pattern, plus a narrow cache-only mutex, one-directional lock order, no fixed concurrency cap (operator decision 4).
- Q: How many files does `meta.json` become? → A: **Four** — identity/lifecycle, statistics, goal, loop. The boundary is the *writer*, not the reader (operator decision 5).
- Q: Which writes get throttled? → A: **Only the per-token counter path.** Every event-driven write (goal, loop, status, title, owner, workspace) stays immediate, because they are control flow, not display (operator decision 6).
- Q: Does throttle unification come along? → A: **No**, ratified unchanged, with the single exception of the ungated root-level fan-out (W17). D12's write-cadence throttle is unrelated despite the shared word (operator decision 7).

### 2026-08-03 (v2, post-grill-#1)

- Q: Are subordinate sessions hidden behind a flag, or nested under their parent? → A: **Nested under their parent** (operator decision 1). The `verifier` hidden-with-a-flag precedent is deliberately **not** followed. Scoped honestly as real UI and API work: US-19, FR-091…FR-094, unit U24, and a `GET /api/v1/sessions` response-shape change.
- Q: What is `Inherit`'s new signature? → A: **`InheritFrom(srcSessionID, srcAgentID, dstSessionID, dstAgentID)`** — a two-key operation. Re-keying the single-key form is a silent no-op and is the failure US-6 exists to prevent (`[grill C-1]`).
- Q: When a child's transcript write fails, does the child turn continue? → A: **Yes** — FR-002's counter-plus-WARN, unchanged. That is precisely why AC-18(b)'s assertion is not sufficient alone and why BDD-36 now asserts the child's file too (`[grill M-9]`).
- Q: What is `ListSessions`'s consistency model after striping? → A: **Best-effort point-in-time snapshot**, stated in FR-086 and asserted by BDD-95.
- Q: Who calls `CloseSession` for a child, and on which terminal states? → A: **U7, from `pkg/agent/subturn.go`'s terminal path, on completed / cancelled / failed / abandoned** (FR-088). No such call site exists in the tree today.
- Q: What happens to a child's `stats.json` deltas when the child is `DeleteSession`d mid-flush? → A: **Flush-then-delete**; a flusher tick that finds the session gone drops its dirty entry without writing (Edge Cases).
- Q: How does the SPA route an approve/deny response back to a child's pending entry when the frame's `session_id` is the chat's? → A: **By approval id** (FR-081). Resolving by session id breaks on the first delegated approval.
- Q: Does `producing_session_id` survive replay? → A: **Carried, not reconstructed** (Ambiguity Resolution item 15).
- Q: Does `ParentSessionID` have a reader, or is W2 a write-only field? → A: **FR-091 is its reader.** Without US-19 it had none, and W2 could have shipped write-only with every test green (`[grill §5, W2]`).
- Q: Does `CreateSessionWithID` fail or succeed when the directory already exists? → A: **Fails loudly** (FR-096). `os.MkdirAll` at `pkg/session/unified.go:463` is idempotent and silent, so adoption was the default; the property is promoted out of holdout H-7 while H-7's scenario stays a holdout.

### 2026-08-03 (v3, post-grill-#2)

- Q: Does `AppendTranscript` itself become strict, or does the lenient form survive behind a sibling? → A: **`AppendTranscript` itself becomes strict** (FR-002). AC-1's frozen text states the property of `AppendTranscript`, so a sibling satisfies AC-1 only for converted callers. Verified all 20 invocation sites already check the error, so this is also the smaller change (`[grill2 C2-3]`).
- Q: What maintains the recency order that pagination slices, and how is `child_count` computed without an O(all sessions) scan? → A: **Nothing maintains a persisted order, and none is added.** `metaCache` is resident-everything, so the sort costs no disk; bounded cost is required at the REST and SPA layers only (FR-092). `child_count` and orphan detection come from a new **in-memory parent index** (FR-097) (`[grill2 C2-2]`).
- Q: What is the root-delegation cap on a fresh install? → A: `[AMENDED 2026-08-04]` **`Performance.EffectiveMaxParallelAgents()` — the central, UI-configurable authority — not a seeded number.** Original v3 answer (superseded, see top-of-file AMENDMENT note): *"16, seeded explicitly in `pkg/config/defaults.go` by U28 (FR-095). It matches today's effective value, so nothing changes behaviourally — but the resolver stops taking the branch FR-095 forbids, and the case now has coverage (BDD-108, #112)"* `[grill2 M2-1]`. That premise depended on `clampParallelExplicit` capping `EffectiveMaxParallelAgents()`'s fallback at 16 too — commit `536b7340` removed that ceiling, so "nothing changes behaviourally" stopped being true and the seed was removed. The case still has coverage (BDD-108, #112, SC-050 — all amended).
- Q: Where does per-session token accounting live once the list returns roots only? → A: **`GET /api/v1/sessions?flat=true`** (FR-104), used by UsageScreen's "By session" tab. Roots-only would have silently dropped child spend (`[grill2 M2-9]`).
- Q: Does the reconcile pass prune cache entries for directories deleted out of band? → A: **Yes, and it is now required** (FR-097a), following `ClearAll`'s existing stat-and-prune loop at `pkg/session/unified.go:1483-1487`. FR-086's second clause was previously stricter than the design it described (`[grill2 M2-7]`).
- Q: What is the strict contract for the transcript *mutate* path? → A: **Counter + WARN, never a bare `false`** (FR-099) — the read-modify-write twin of AC-1 (`[grill2 M2-3]`).
- Q: What does `#91` actually execute, given a shared working tree and a local `pkg/gateway` ban? → A: **Mutations against a `t.TempDir()` copy, scoped to the gate's own package; `pkg/gateway` gates run in CI only.** If unaffordable, #91 downgrades to a recorded review gate and SC-035 says so (`[grill2 M2-5]`).
- Q: Is "read" in #29's K an identifier occurrence, a selector expression, or a dataflow use — and is the pre-arm component 2, 3 or 5? → A: **An AST `SelectorExpr`; the answer is 3 reads across 5 re-based sites**, derived by site in "Three reads, five sites" (`[grill2 M2-8]`).
- Q: What happens to `partial_errors` under pagination, and does a cursor survive a store that errored mid-page? → A: **The page still returns, `partial_errors` is populated, and the cursor stays valid** (FR-098). The two-variant `oneOf` collapses to one inline `SessionPage` schema per ADR-034 (`[grill2 M2-10]`).
- Q: Which unit adds the store-flush and `metaCache`-eviction calls to `session_end.go`? → A: **U17b**, split out of U17 and moved to Wave E behind U6. `CloseSession` holds no store reference today (`[grill2 C2-4]`).
- Q: Do all units owning a file that mentions the retired interrupt API need to wait for U8? → A: **No — only the five with compile breaks.** The other ~25 references are doc comments, handled by FR-100 in each file's existing owner's commit (`[grill2 C2-4]`).
