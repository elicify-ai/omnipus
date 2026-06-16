# Grill-Spec Review — Spec-1: Workspace Scoping Key & `project → workspace` Rename

> **ROUND 3 (re-review) is at the top. ROUND 2 and ROUND 1 are retained verbatim below their dividers.**

---

# ═══ ROUND 3 (re-review) ═══

- **Reviewed spec:** `docs/internal/specs/v01-spec1-workspace-rename-spec.md` (Rev 3)
- **Detected mode:** `plan-spec`.
- **Reviewer mindset:** adversarial, read-only. Every claim re-grounded against the **live tree** via direct `git grep` — not the round-2 prose.

## Executive Summary (Round 3) — **GATE C: PASS**

All four round-2 survivors are **CLOSED**, grounded against the live tree:

- **NEW-C-1 (guard false-positive on GCP `project_id`) → CLOSED.** The exclusion set `pkg/auth/` · `pkg/providers/antigravity_provider.go` · `cmd/omnipus/internal/auth/` is **exactly complete**. I enumerated every live `\b(ProjectID|project_id)\b` site in `pkg/`+`cmd/` (non-test, non-archive): the only matches outside the three excluded paths are genuine workspace-rename targets (`pkg/gateway/rest*.go`, `pkg/boardtask`, `pkg/session/daypartition.go`, `pkg/sysagent/tools/*`, `pkg/agent/project_linker_hook.go`, plus regenerated `pkg/api/generated/`). The three excluded files contain only `AuthCredential.ProjectID` / `cred.ProjectID` (Google-Cloud project) — verified at `pkg/auth/store.go:23`, `pkg/auth/oauth.go:452-453`, `antigravity_provider.go:594-625`, `cmd/omnipus/internal/auth/helpers.go:120,415-416,454`. **No other unrelated `project_id` survives in live `pkg/`.** AC-3 (assert `AuthCredential.ProjectID` not flagged) is the correct guard regression. The guard can now reach 0 on a correct branch.
- **NEW-C-2 (agent-tool surface) → CLOSED (operator chose RENAME).** FR-1.11 + §2 + US-2(d) + SC-7 correctly inventory the surface and **acknowledge the breaking tool-contract change**: tool names (`tools/project.go`, 25 occ in-file / 47 `system.project.*` string occ across non-archive `.go`), the LLM `project_id`→`workspace_id` param (`task.go`/`pin.go` schemas + descriptions), `confirmation.go`/`ratelimit.go`/`rbac.go` keys (verified: 4+4+4 entries), `projectsDir` (`project.go:37`), and `boardtask.go:56`/`daypartition.go:81`/`project_linker_hook.go`/`project_session_links.go`. The breaking-change posture is explicit ("agents/skills/configs calling the old tool name or param break"). **Residual gaps are documentation-only and self-healed by the US-2(d) guard** (see NEW-m2): `prompt.go`, `loop.go:3857`, `sysagent_test.go` carry literal `system.project.*` and are caught by a `git grep system\.project` guard even though §2 doesn't name them. The FR-1.11 claim of per-agent policy seeds in `coreagent`/`config` is **ungrounded — no such seeds exist** (NEW-m1), but this is harmless (nothing to rename there).
- **NEW-M1 (counts) → CLOSED.** §2 and US-2(b) replaced the brittle exact numbers with "all sites (≈19–20 calls + 2 defs)"; live tree shows **20** `canAccess`/`denyIfNoAccess` def+call lines in non-test `pkg/gateway/*.go` (= ~18 calls + 2 defs) — matches. The `projectsDir` Join site (`tools/project.go:37`) is now explicitly listed in §2 and the guard.
- **NEW-M2 (SEC-2 test scoping) → CLOSED.** FR-1.9 is now precise and matches the live files: DELETE `canAccess`+`denyIfNoAccess` (defs at `rest_projects.go:75,90`) and `TestCanAccess_Table` (`tenancy_regression_test.go:49`); REWRITE the cross-owner-404 behaviour tests (`tenancy_regression_test.go`: `TestTenancy_MultiUser_CrossOwnerProject_Returns404:221`, `…CrossOwnerBoardTask_Returns404:412`, `TestBoardTask_CrossOwnerMilestoneFK_MilestoneNotFound:170`) to no-denial; EXCLUDE `rest_patch_ownership_test.go` (verified all `TestPatchOwnership_*` = agent-RBAC `patchAgentOwnership`, a distinct control). SC-5 wording is now consistent (no "test count preserved" trap).

- **CRITICAL:** 0
- **MAJOR:** 0
- **MINOR:** 2 (NEW-m1 ungrounded `coreagent`/`config` seed claim; NEW-m2 three un-listed but guard-caught `system.project.*` sites incl. the LLM-facing prompt)

**Verdict (Round 3): PASS (GATE C).** Every round-2 BLOCK/REVISE-grade defect is closed and grounded. The two remaining items are MINOR and do not block: one is a harmless over-statement (renaming seeds that don't exist), the other is an inventory omission that the spec's own US-2(d)/SC-7 guard mechanically catches at implementation time. Recommend folding both into the spec for cleanliness but they are not gates.

## Round-3 Findings

| ID | Severity | Lens | Section | Finding | Fix |
|----|----------|------|---------|---------|-----|
| **NEW-m1** | MINOR | Incorrectness (ungrounded claim) | FR-1.11, §2 row, SC-7 | FR-1.11 asserts the rename must move "**every per-agent policy seed** referencing them (`pkg/coreagent/` + `pkg/config/`)" and SC-7 says "per-agent policy seeds reference `system.workspace.*`". Grounding: there are **zero** `system.project.*` references in `pkg/coreagent/**` or `pkg/config/**`. Custom agents seed `system.*: deny` via a wildcard, not by enumerating `system.project.*`. The claim is harmless (nothing to rename) but ungrounded — the exact NEW-M1 defect class (asserting a code fact that isn't true). | Drop the `coreagent`/`config` policy-seed clause from FR-1.11/SC-7, or reword to "if any per-agent seed enumerates `system.project.*` it moves too (none do today)." Keeps SC-7 from asserting a post-condition that is vacuously true and confusing. |
| **NEW-m2** | MINOR | Incompleteness | FR-1.11, §2 inventory | Three live `system.project.*` sites are not named in §2/FR-1.11: (i) **`pkg/sysagent/prompt.go:63,118`** — the **agent system-prompt text** literally lists `system.project.delete` and `system.project.{create,update,delete,list}`; if not renamed the LLM is instructed to call a tool that no longer exists (functional, not cosmetic). (ii) **`pkg/agent/loop.go:3857`** — comment naming `system.project.create`. (iii) **`pkg/sysagent/sysagent_test.go:178,229,473,496,679-680`** — 6 assertions on `system.project.*` that break on rename (regression coverage, must update). Also the `task.go` tool **descriptions** (`:39,:124`) tell the LLM to "call `system.project.list` first to get the `project_id`". **Mitigant:** all carry the literal `system.project.` token, so the US-2(d) guard (`no system.project.* remains`) catches every one — the omission self-heals at implementation. | For completeness add `pkg/sysagent/prompt.go` (system-prompt strings — flag as LLM-facing, same class as tool descriptions), `pkg/agent/loop.go:3857`, and `pkg/sysagent/sysagent_test.go` to the FR-1.11 inventory, and note the `task.go` descriptions explicitly. Confirm US-2(d) does NOT exclude `*_test.go` (so the `sysagent_test.go` assertions are forced to update, not silently left stale) — or call them out in the Regression section. |

## Round-3 closure ledger

| Round-2 item | Round-3 status | Grounding |
|---|---|---|
| NEW-C-1 (GCP guard false-positive) | ✅ CLOSED | Exclusion set exactly = the 3 GCP files; no other stray `project_id` in live `pkg/`/`cmd/`. AC-3 added. |
| NEW-C-2 (agent-tool surface) | ✅ CLOSED | FR-1.11 RENAME w/ breaking-change ack; tool names+param+policy tables+storage+structs inventoried. Residual sites guard-caught (→ NEW-m2). |
| NEW-M1 (counts) | ✅ CLOSED | "all sites (≈19–20 calls + 2 defs)"; live = 20 lines. `projectsDir` Join listed. |
| NEW-M2 (SEC-2 tests) | ✅ CLOSED | DELETE canAccess+TestCanAccess_Table; REWRITE 3 cross-owner-404 tests; EXCLUDE rest_patch_ownership (agent-RBAC). All files verified live. |

## Round-3 Verdict

**PASS — GATE C cleared.**

The spec is grounded, internally consistent, and its completeness guard (US-2 a–d) is now sound — it returns 0 only on a correctly + fully renamed tree, and it does not false-positive on the GCP OAuth `project_id`. The owner-gate removal (FR-1.9) is precise, auditable, and correctly scoped away from the agent-RBAC control. The two remaining MINOR items are non-blocking cleanups (one ungrounded seed claim, one inventory omission the guard already enforces).

Spec is ready for task decomposition. Run:

```
/taskify docs/internal/specs/v01-spec1-workspace-rename-spec.md
```

---

# ═══ ROUND 2 (re-review) ═══

- **Reviewed spec:** `docs/internal/specs/v01-spec1-workspace-rename-spec.md` (Rev 2)
- **Source ADR:** `docs/internal/architecture/ADR-019-v01-workspaces-foundation.md` — amended with FR-1.7 (operator decision C-1 STRIP) + risk R7. **ADR amendment verified present and sound** (lines 30, 58, 204): C-1 recorded, R7 documents the gate removal as a deliberate, auditable security-posture reduction with the `Owner` field retained for reversibility. Good.
- **Detected mode:** `plan-spec`.
- **Reviewer mindset:** adversarial, read-only. Re-grounded against the live tree via direct `git grep` (not the round-1 prose).

## Executive Summary (Round 2)

The three round-1 CRITICALs are **partially** closed. The owner-gate contradiction (C-1) is genuinely resolved — the spec is now internally consistent (FR-1.9 + US-4 + BDD #3/#5 + the ADR amendment all say "strip the gate," the security-control removal is explicit and auditable, and the "behaviour-preserving" claim is correctly scoped to "except the gate"). **C-1 → CLOSED.** The seed correction (C-3 seed half) is correct and grounded ("Main"→"My Workspace", now owner-stamped, 409 delete-protection retained). The double-seed race (M-1), disk-key/`validateMilestoneFK` (M-3), legacy-migration drop (M-4) and CI-authority (M-6) fixes are all grounded and adequate.

**But two round-1 CRITICAL-class defects survive in a new form, and the inventory is still wrong** — re-grounding the live tree turned up symbols the corrected §2 still omits:

1. **The "token-precise" guard (C-2 fix) is still unsound** — it will fail *forever* on legitimate code. `\b(ProjectID|project_id)\b` over `pkg/` matches the **GCP/Antigravity OAuth credential field** `AuthCredential.ProjectID` / `json:"project_id"` in `pkg/auth/store.go:23`, `pkg/auth/oauth.go:452-453`, `pkg/providers/antigravity_provider.go:594-625`, and `cmd/omnipus/internal/auth/helpers.go:120-454` — 11+ occurrences across 4 files that are **completely unrelated** to the workspace key and must NOT be renamed. The spec's only guard exclusions are `_archive/`, `*BRD*`, and test *fixtures* — none of which cover these. The round-2 guard as written can never reach 0. This is the same defect class as round-1 C-2 (a guard that doesn't model the real token namespace), re-introduced. **NEW-C-1.**

2. **The §2 inventory is still materially incomplete** — it lists only `rest_projects.go`/`rest_board.go`/`rest_milestones.go` + SPA, but the workspace `project_id` symbol also lives in an entire **agent-tool surface the spec never mentions**: `pkg/sysagent/tools/project.go` (the `system.project.create/update/list/delete` tools), `task.go`, `pin.go`, `project_session_links.go`, plus `pkg/boardtask/boardtask.go:56` (on-disk task struct), `pkg/session/daypartition.go:81`, `pkg/agent/project_linker_hook.go`. The `system.project.*` tool **names** and the `project_id` **tool parameter** are LLM-facing and are referenced by per-agent policy/RBAC/rate-limit/confirmation tables (`pkg/sysagent/confirmation.go`, `ratelimit.go`, `rbac.go`). The spec gives no decision on whether the tool names/params rename — yet the guard `\bproject_id\b` will fail on the tool params if they don't. **NEW-C-2.**

3. **The corrected counts are themselves not grounded.** "23 canAccess/denyIfNoAccess sites" is unverified prose carried from round 1: the live tree has **18 non-test call lines** (one of which is the call *inside* `denyIfNoAccess`) + 2 function definitions = ~19-20 production touch points, not 23. "~5 `filepath.Join("projects")` sites" undercounts: there are 4 *non-test* sites — and one is `pkg/sysagent/tools/project.go:37 projectsDir`, which §2 does not list. C-3's inventory half is "corrected" to a different set of wrong numbers. **NEW-M (inventory still false).** (SC-5/the guard enforce *zero remaining*, so the wrong count is not fatal — but it is the exact defect round-1 C-3 raised, recurring.)

- **CRITICAL:** 2 (NEW-C-1 unsound guard, NEW-C-2 missing tool surface)
- **MAJOR:** 2 (NEW-M1 inventory counts still wrong; NEW-M2 SEC-2 test scope under-specified — `rest_patch_ownership_test.go` is agent-RBAC, NOT the #406 workspace gate, and must be excluded from the "rewrite to no-denial" instruction or it will be wrongly gutted)
- **MINOR:** 1
- **Round-1 closures:** C-1 ✅, C-3(seed) ✅, M-1 ✅, M-3 ✅, M-4 ✅, M-6 ✅; C-3(inventory) ⚠️ re-opened as NEW-M1; C-2 ⚠️ re-opened as NEW-C-1.

**Verdict (Round 2): REVISE.** C-1 is closed and the spec's *security posture* is now correct and auditable — the BLOCK-grade owner-gate contradiction is gone, so this is no longer a BLOCK. But the completeness guard (NEW-C-1) still cannot pass on the real tree, and the rename's blast radius (NEW-C-2: `system.project.*` tools + policy tables) is undefined. Both must be pinned down before implementation, because the guard *is* the spec's correctness gate and it currently fails on day one. These are bounded, mechanical fixes (add an explicit OAuth-field exclusion + a tool-surface decision + re-derive counts), hence REVISE not BLOCK.

## Round-2 Findings

| ID | Severity | Lens | Section | Finding | Fix |
|----|----------|------|---------|---------|-----|
| **NEW-C-1** | CRITICAL | Infeasibility / Incompleteness | US-2 (a), FR-1.8, SC-4, Test #8 | The token guard `\b(ProjectID\|project_id)\b` over live `pkg/` matches the **GCP OAuth credential field** (`AuthCredential.ProjectID`, `json:"project_id"`): `pkg/auth/store.go:23`, `pkg/auth/oauth.go:452-453`, `pkg/providers/antigravity_provider.go:594-625`, `cmd/omnipus/internal/auth/helpers.go:120-454` (11+ hits, 4 files). These are unrelated to the workspace key and must never be renamed. The guard's exclusion set (`_archive/`, BRD, fixtures) does not cover them, so check (a) can never reach 0 → the spec's own primary completeness gate fails on a correct branch. | Add an **explicit symbol-level exclusion** to US-2(a)/FR-1.8/SC-4: exclude the OAuth credential `ProjectID`/`project_id` field by path (`pkg/auth/`, `pkg/providers/`, `cmd/omnipus/internal/auth/`) OR by qualifying the guard to the workspace symbol only (e.g. grep for `project_id` as a *board/session/milestone* JSON tag and `ProjectID` only in `pkg/gateway`, `pkg/boardtask`, `pkg/session`, `pkg/sysagent/tools`, `src/`). State the exact command + exclusion list so the guard returns 0 on a correctly-renamed tree. |
| **NEW-C-2** | CRITICAL | Incompleteness | §2 inventory, FR-1.4, US-2, scope | §2 omits the **agent-tool surface**: `pkg/sysagent/tools/project.go` defines `system.project.create/update/list/delete` (Name() strings, tool descriptions, the `project_id` parameter) plus `task.go`/`pin.go`/`project_session_links.go`; these tool names are keyed in `pkg/sysagent/confirmation.go`, `ratelimit.go`, `rbac.go` (per-agent policy/RBAC). Also missing from §2: `pkg/boardtask/boardtask.go:56` (on-disk task `project_id`), `pkg/session/daypartition.go:81`, `pkg/agent/project_linker_hook.go`. The spec gives **no decision** on whether `system.project.*` tool names + the `project_id` tool-parameter rename. If they do: blast radius includes LLM-facing tool descriptions, per-agent `ToolPolicyCfg` seeds, and the `system.*` policy tables — a far larger change than §2 implies. If they don't: the guard `\bproject_id\b` fails on the tool params (NEW-C-1). | Add to §2 + FR-1.4 an explicit decision: rename `system.project.*` → `system.workspace.*` and the `project_id` tool param → `workspace_id` (and enumerate the policy/RBAC/ratelimit/confirmation tables + agent-seed policy keys that must move with them), **or** explicitly scope the tool names OUT (keep `system.project.*`) and exclude them from the guard with a stated rationale. Either way the `boardtask.go`/`daypartition.go`/`project_linker_hook.go` on-disk + linker symbols must be listed in the inventory and covered by FR-1.4. |
| **NEW-M1** | MAJOR | Incorrectness | §2 inventory, FR-1.9, SC-5 | The "corrected" counts are still wrong. Live tree: **18 non-test `canAccess`/`denyIfNoAccess` call lines** (incl. the call inside `denyIfNoAccess`) + 2 func defs ≈ 19-20 touch points, **not 23**. `filepath.Join("projects")`: **4 non-test sites**, one being `pkg/sysagent/tools/project.go:37 projectsDir` which §2 omits — "~5" is approximately right by luck but lists the wrong files. The "216 occ / 23 files" figure also can't be reconciled (24 `.go` files carry the token before excluding the 4 OAuth files). Same defect class as round-1 C-3, recurring. | Re-derive every §2 count from `git grep` on the live tree (`canAccess\|denyIfNoAccess` call sites; `filepath.Join(...,"projects")`; `\b(ProjectID\|project_id)\b` minus the OAuth exclusion) and cite the actual file list. Since SC-5/the guard enforce *zero remaining*, prefer replacing the brittle counts with "all sites in {enumerated files}". |
| **NEW-M2** | MAJOR | Inconsistency / Insecurity | FR-1.9, US-4 AC-3, Test #5, SC-5 | "Rewrite the round-1 SEC-2/#406 tests to assert no-denial" is under-scoped. The #406 denial assertions span **multiple files** (`tenancy_regression_test.go`: `TestTenancy_MultiUser_CrossOwnerProject_Returns404`, `TestTenancy_MultiUser_CrossOwnerBoardTask_Returns404`, `TestBoardTask_CrossOwnerMilestoneFK_MilestoneNotFound`, `TestCanAccess_Table`; plus refs in `rest_board_test.go`/`rest_milestones_test.go`/`regression_board_bugs_test.go`). Two traps: (i) `TestCanAccess_Table` tests the `canAccess` *function* — once the function is deleted (FR-1.9), this test cannot be "rewritten to assert no-denial," it must be **deleted**, contradicting SC-5's "test count is preserved." (ii) `rest_patch_ownership_test.go` is **agent-PATCH RBAC**, a *different* control from the #406 workspace owner gate — it must NOT be swept into the "no-denial" rewrite or a real RBAC control gets gutted. | Enumerate the exact #406 workspace-gate tests in FR-1.9/Test #5 (the 3 cross-owner `Returns404`/`MilestoneFK` tests → rewrite to assert 200; `TestCanAccess_Table` → delete with the function). Drop SC-5's "test count preserved" or reword to "the cross-owner-denial assertions are converted to no-denial; the `canAccess` unit test is removed with the function." Explicitly exclude agent-ownership RBAC (`rest_patch_ownership_test.go`, `patchAgentOwnership`) from the gate removal. |
| **NEW-m1** | MINOR | Ambiguity | US-2 (c), US-7 | Guard check (c) "no `/projects` route or `projectId` param in `src/`" is sound for routes, but `src/` also carries non-route `Project*` identifiers from generated types (`gen.Project`, `ProjectStatus`) and component/dir names (`src/components/projects/`, `ProjectDetailScreen`). The guard text doesn't say whether component/dir renames are in scope or whether only the route + `project_id` token are gated. 36 `src/` files reference `project`. | Clarify (c): gate the route literal `/projects`, the `projectId` route param, and the `project_id`/`workspace_id` data field; state whether `src/components/projects/` dir + `Project*` component names are renamed (recommended for consistency) or out of guard scope. |

## Round-2 Structural / Coverage notes

- **C-1 internal consistency: CLOSED.** No remaining "behaviour-preserving" contradiction — §1 explicitly carves out the gate, NB list says "must not retain any canAccess/denyIfNoAccess … nor leave round-1 SEC-2 tests asserting the old denial," and the ADR amendment (R7) makes the removal auditable, not silent. The security-control removal is explicit. ✅
- **M-1 (race): CLOSED in spec form** — FR-1.6 requires lock-safe seed + Test #2 (`-race`). NOTE for implementation (not a spec defect): the round-1 finding was that `ensureInboxProject` is a *separate-process* race (two `gateway` processes against one data dir, `gateway.go:1422`, before the listener binds). A Go `-race` test exercises *goroutine* races, not multi-process — the `-race` test validates an in-process lock but does not prove the cross-process invariant H7 describes. The mechanism (`O_EXCL`/flock sentinel) must be the cross-process kind; the test as specced (#2 `-race`) is necessary but not sufficient for H7. Carry as an implementation note.
- **M-3: CLOSED** — FR-1.4 covers on-disk `project_id` rename; `validateMilestoneFK` cross-owner `canAccess` (verified live at `rest_board.go:1123`) is removed under FR-1.9. Grounded.
- **M-4: CLOSED** — FR-1.10 drops the `agent_ids→core_team` lazy migration (verified live at `rest_projects.go:152-162`). Greenfield rationale sound.
- **M-6: CLOSED** — SC-2/regression now defer to CI as authority; local = scoped tests.

## Round-2 Verdict

**REVISE.**

C-1 (the BLOCK-grade owner-gate contradiction) is closed and the security posture is now consistent and auditable. The remaining defects are bounded and mechanical but real: the completeness guard cannot pass on the live tree (NEW-C-1, OAuth `project_id` collision) and the rename's blast radius is undefined (NEW-C-2, `system.project.*` tool surface + policy tables). Fix the guard exclusions, decide the tool-name scope, re-derive the §2 counts, and pin the SEC-2 test list (NEW-M2), then re-run.

```
/plan-spec --revise docs/internal/specs/v01-spec1-workspace-rename-spec.md docs/internal/specs/v01-spec1-workspace-rename-spec-review.md
```

---

# ═══ ROUND 1 (retained) ═══

- **Reviewed spec:** `docs/internal/specs/v01-spec1-workspace-rename-spec.md`
- **Source ADR:** `docs/internal/architecture/ADR-019-v01-workspaces-foundation.md` (FR-1, R1)
- **Detected mode:** `plan-spec` (US/AC, BDD Given/When/Then, FR-x, SC-x, Traceability Matrix all present)
- **Reviewer mindset:** adversarial, read-only. Grounded against the live codebase (`pkg/gateway/rest_projects.go`, `rest_board.go`, `rest_milestones.go`, `gateway.go`, `contracts/`).

---

## Executive Summary

This spec describes the highest-risk unit of the v0.1.0 foundation: the atomic
`project → workspace` rename. The intent is sound and the structure is mostly
complete, but the spec is **wrong on several load-bearing facts about the code it
is renaming**, and those errors flow straight into its requirements and tests.
The most damaging: the spec asserts the rename is "behaviour-preserving" and that
"`owner` is attribution only," but the live code uses `owner` as an **enforced
access gate** (`caller.canAccess` / `denyIfNoAccess`, SEC-2/#406) — so a
behaviour-preserving rename must preserve that gate, directly contradicting
non-behavior NB-2 and FR-1.7. Separately, the grep-guard (Test #6) as specified
will **not** catch the partial-rename it is supposed to catch, the seed name in
the code is "Main" (not "My Workspace") and lives behind a delete-protected
"Inbox" concept the spec never mentions, and the rename inventory counts in §2
do not match the codebase.

- **CRITICAL:** 3
- **MAJOR:** 6
- **MINOR:** 4
- **OBSERVATION:** 3

**Verdict: BLOCK.** The owner-gate contradiction (C-1) is a correctness defect
that would either silently strip a security control or invalidate the
"behaviour-preserving" claim; the grep-guard gap (C-2) defeats the spec's own
primary completeness check; the inventory/seed-state mismatch (C-3) means the
implementer is working from a false map.

---

## Findings Table

| ID | Severity | Lens | Section | Finding | Recommended Fix |
|----|----------|------|---------|---------|-----------------|
| C-1 | CRITICAL | Incorrectness / Insecurity / Inconsistency | FR-1.7, NB-2, US-3 AC-2, Test #2 | Spec claims `owner` is "attribution only … no access gate" and US-5 demands behaviour preservation. The live code (`rest_projects.go:75-95,499`) uses `owner` as an **enforced gate**: `canAccess` returns `!MultiUser` for empty-owner resources and `owner==username` otherwise; `denyIfNoAccess` returns 404 (SEC-2/#406). A behaviour-preserving rename MUST keep this gate — but FR-1.7/NB-2 instruct the implementer that owner is inert, inviting them to drop or weaken it. The two requirements are mutually contradictory. | Reword FR-1.7 to: "preserve the existing `owner` access-gate semantics (`canAccess`/`denyIfNoAccess`, SEC-2/#406) verbatim under the new symbol names; this rename adds no new access control and removes none." Add a regression test asserting cross-owner 404 still fires post-rename. Remove "owner is attribution only" framing or scope it to *the seeded default's owner field value*, not the gate. |
| C-2 | CRITICAL | Infeasibility / Incompleteness | US-1, FR-1.8, Test #6, BDD "No project symbol survives" | The grep-guard is the spec's primary completeness gate, but it is under-specified and unsound. (a) US-1/FR-1.8 say "no `project`/`Project` substring" yet Test #6 only greps three tokens (`project_id`/`ProjectID`/`"projects/"`) — narrower than the stated requirement. (b) The bare-substring reading is infeasible: `pkg/audit/argshash.go`, schedule contracts, and `src/` contain legitimate "projection"/"projected"/"project" English (1119 hits in `src/` alone). (c) `"projects/"` matches **0** occurrences today — the storage path appears as `filepath.Join(home, "projects", …)` (3 sites in `rest_projects.go`) and in comments, never the literal `"projects/"`. The guard as written would pass a branch that never renamed the storage directory. | Pin the guard to an **explicit token allow/deny list**, not a substring. Specify exactly: regex `\bProjectID\b`, `"project_id"` (json tag/string), `filepath.Join\([^,]+,\s*"projects"`, route literal `/api/v1/projects`, and `Project\b` as a Go type identifier (e.g. `gen.Project`, `storedProject`). List the precise exclusions (`docs/internal/_archive`, `BRD/`, `*_test` fixtures, and the English words `projection`/`projected`/`projecting`). State the exact command and that it must return 0. |
| C-3 | CRITICAL | Incorrectness / Incompleteness | §2 inventory, FR-1.4, FR-1.6, Test #1 | The rename inventory and seed description are factually wrong against the code. (a) §2 says "143 occ. across 36 `pkg/` files"; actual `ProjectID`/`project_id` is **216 occurrences across 23 `.go` files** (15 non-test). (b) §2 says "34 `projects/` storage paths"; the literal does not exist — storage is 3 `filepath.Join(…, "projects", …)` sites plus comments. (c) The seed is **not** named "My Workspace": `ensureInboxProject` (`rest_projects.go:382-409`) creates `Name:"Main"`, `IsDefault:true`, with no `owner` set, and it is a delete-protected "Inbox" (returns 409 on delete, `:757-759`; sorted first, `:508-513`). The spec never mentions the Inbox/delete-protection/owner-empty realities, so US-3 AC-2 ("owner equals the username") is **false today** — the seed sets no owner. | Re-derive §2 counts from the actual tree and cite the real call sites. State that the seed must be **renamed Inbox→default-workspace AND its name changed "Main"→"My Workspace"** in the same change. Resolve US-3 AC-2: either the seed must now set `owner=username` (a behaviour *change*, must be called out and tested) or AC-2 is wrong and must be dropped. Mention the delete-protection 409 path explicitly as behaviour to preserve. |
| M-1 | MAJOR | Incompleteness | Test #1, H7, Edge Cases | Double-seed race (H7) is named as a holdout but **not** tested in the TDD plan, and the current `ensureInboxProject` is **not** concurrency-safe: it list-then-writes with no lock, and is called once at boot (`gateway.go:1422`) before the listener starts. H7 ("two rapid boots") is a *separate-process* race against a shared data dir with no file lock — the code as written can produce two defaults. The spec asserts the property ("no double-seed race") without a mechanism or test to enforce it. | Either (a) state that single-process-at-boot is the only supported invariant and downgrade H7 to "not supported / undefined" with rationale, or (b) add FR + Test for an atomic seed (e.g. `O_EXCL` create or flock on a sentinel) and a concurrent-boot test. Do not leave an asserted-but-unenforced safety property. |
| M-2 | MAJOR | Inconsistency | US-3, Test #1, contracts | Field-name drift: US-3/BDD say the workspace is "marked default," but the contract field is `is_default` (`Project.yaml:67`) surfaced as `IsDefault *bool`. The spec must name the actual wire field so the implementer/tester does not invent a `default` field. Also confirm whether `is_default` is renamed (it is *not* a `project_id`, so it likely stays) — the spec is silent. | Specify: `is_default` field name is retained (not renamed; it is not a project-scoped key). State that the seeded default has `is_default=true`. Align US-3 AC wording to `is_default`. |
| M-3 | MAJOR | Incompleteness | FR-1.4, §2, Test #3 | The on-disk **task/milestone files already contain `project_id`** (`rest_board.go:186,303,525`; `validateMilestoneFK(…, projectID, …)` at `rest_board.go:1117`). Under "greenfield" the spec assumes no existing data, but a fresh install that *creates tasks then reboots* writes `workspace_id` files — fine — yet the spec never states whether the on-disk JSON key for tasks/milestones changes `project_id→workspace_id` (it must, for consistency with the renamed contracts) and whether `validateMilestoneFK`'s cross-entity ownership check (#406) is preserved. Test #3 ("behaviour preserved") does not mention the milestone-FK ownership path. | Add to FR-1.4 explicit mention of the on-disk task/milestone JSON key rename and the `validateMilestoneFK` signature/behaviour preservation (the #406 ownership check is security-relevant — see C-1). Extend Test #3 to assert milestone-FK ownership enforcement is unchanged. |
| M-4 | MAJOR | Incompleteness / Inoperability | NB-1, Edge Cases, H5 | "Greenfield — never read pre-existing `projects/` data" collides with an existing **lazy migration** in `readProjectFile` (`rest_projects.go:152-162`: `agent_ids → core_team`). After the rename, a `workspaces/` reader inherits or drops that migration. The spec does not say whether the lazy-migration code is ported, dropped, or what happens if a `projects/` dir exists on disk from a prior build (H5 reseeds, but a stale `projects/` dir is orphaned silently). | State explicitly: the `agent_ids→core_team` lazy migration is [ported / dropped] under the new reader, and a pre-existing `projects/` directory is [ignored / logged / errored]. Add a holdout or note for the orphaned-`projects/`-dir case so it isn't a silent surprise. |
| M-5 | MAJOR | Inconsistency | §2, FR-1.4, Test #6 | The spec scopes the grep guard / rename to `pkg/` + `src/`, but the rename also touches `cmd/`, generated files (`pkg/api/generated/`, `src/lib/api/generated/` — regenerated, correct), and **comments/doc-strings** that the guard's token list must decide on (e.g. `rest_projects.go:35,137,171` comments say "projects/"). SC-4 greps only `pkg/`+`src/`; a stray `project_id` in `cmd/` or a config default would pass. | Extend the guard scope to all non-archive Go/TS source roots (`pkg/`, `src/`, `cmd/`, `internal/`, `contracts/`), or explicitly justify the `pkg/+src/` boundary and assert no scope-key symbols live elsewhere (verify `cmd/` first). |
| M-6 | MAJOR | Infeasibility | SC-2, Test #4, CLAUDE.md build rules | SC-2 prescribes `CGO_ENABLED=0 go build -tags goolm,stdjson ./...` and Test #4 runs `make verify-contracts` as an "Integration (CI)" test. CLAUDE.md is explicit that the **full Go suite must not be run locally** (OOM on the devpod) and that CI is the authority. The spec frames these as developer-runnable gates without noting they are CI-gated. A junior implementer following SC-2 verbatim may OOM the pod. | Annotate SC-2/Test #4/#6 as **CI-verified gates** (push-and-read-checks), per CLAUDE.md "CI is the authority." For local confidence, specify the narrow `-run '^TestName$' -p 1` form for the seed/behaviour unit tests only. |
| m-1 | MINOR | Ambiguity | US-1 independent test | "no `project`/`Project`/`project_id` in non-archived, non-comment code paths (allowed: history/`_archive`, unrelated words)" — "unrelated words" is undefined and "non-comment" conflicts with the guard which (per M-5) must decide comments explicitly. | Define "unrelated words" as the enumerated allow-list from C-2; state whether comments are in or out of guard scope. |
| m-2 | MINOR | Ambiguity | §1, NB-3 | "tasks/memory/calendar/connections continue to work exactly as today" — but **memory/calendar/connections are not yet keyed by project_id** in the code surveyed (only Session, Milestone, BoardTask carry `project_id`). The spec overstates the current coupling. | Scope the rename claim to the entities that actually carry the key today (Session, Milestone, BoardTask*); note memory/calendar/connections gain the key in Specs 2–6, not here. |
| m-3 | MINOR | Inconsistency | Traceability Matrix | FR-1.7 traces to Test #2 (`TestWorkspace_OwnerIsAttributionOnly`) which, per C-1, encodes a *false* property. The matrix is internally consistent but points at a wrong test. | After fixing C-1, retarget FR-1.7's test to "owner-gate preserved" rather than "attribution only." |
| m-4 | MINOR | Overcomplexity | Holdout H6 | H6 (custom skill hitting old `/projects` API "fails cleanly, documented breaking change") implies a documentation deliverable not listed in scope/FRs. Either it's in scope (then it needs an FR) or it's noise. | Add a one-line FR for the breaking-change note, or drop H6. |
| O-1 | OBSERVATION | Inoperability | §4 | No mention of a CHANGELOG/release-note entry for the `/projects → /workspaces` breaking API change, despite v0.1.0 being a release. | Consider noting the breaking-change changelog line (release-drafter). |
| O-2 | OBSERVATION | Cross-spec | §2 flows, Assumptions | Confirmed cross-spec coupling: `workspace_id` is consumed by Specs 2–6 (Session/Milestone/BoardTask already; memory/calendar/connections later). This is correctly flagged for Phase-3.5 — good. Recommend the Phase-3.5 pass also reconcile the **field name** (`workspace_id`), the **storage dir** (`workspaces/`), and the **owner-gate** decision (C-1) as the three shared invariants. | None — carry to Phase-3.5 checklist. |
| O-3 | OBSERVATION | Overcomplexity | — | The "full atomic rename, one regen" decision is the right call (avoids a half-renamed contract drift window). No over-engineering detected in the approach itself. | None. |

---

## Structural Integrity Results (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | PASS (US-1…US-5 each have AC) |
| Every acceptance scenario has ≥1 BDD scenario | PASS |
| Every BDD scenario has `Traces to:` | PASS |
| Every BDD scenario has a corresponding TDD test | PARTIAL — H7 double-seed race named but no test (M-1) |
| Every FR appears in traceability matrix | PASS (FR-1.1…1.8 mapped) |
| Every BDD scenario appears in matrix | PASS |
| Test datasets cover boundary/edge/error | PARTIAL — no concurrency dataset for H7 (M-1); "boot twice" covers stability |
| Regression impact explicitly addressed | PASS in form, but rests on the false "behaviour-preserving" premise (C-1) and missing milestone-FK coverage (M-3) |
| Success criteria measurable, no subjective language | PARTIAL — SC-4 measurable but unsound as written (C-2); SC-2/SC-4 mis-state local runnability (M-6) |

---

## Test Coverage Assessment

- **Negative/error tests:** drift-fails-closed (#5) present and correct in shape — good. Missing: cross-owner 404 regression (C-1), milestone-FK ownership (M-3), orphaned-`projects/`-dir (M-4).
- **Boundary:** "boot twice → same id" (stability) covered. "empty dir → exactly one" covered. Zero/two-workspace edges asserted in prose, tested via #1.
- **Concurrency:** **gap** — H7 race asserted, untested, and the underlying code is not lock-safe (M-1).
- **Idempotency:** seed idempotency is the existing `ensureInboxProject` contract; #1 should assert second-call no-op (currently only asserts "one default exists").
- **Regression blind spots:** the spec ports `rest_projects_test.go → rest_workspaces_test.go` "unchanged in logic" — good intent, but the owner-gate tests (#406 regression coverage, commit `253a4d8`) must survive and are not called out (C-1/M-3).

---

## STRIDE Threat Summary

| Component | Threat | Notes |
|---|---|---|
| Workspace REST handler (renamed) | **Elevation of Privilege / Info Disclosure** | The `owner`/`canAccess` gate (SEC-2/#406) is mischaracterized as inert. A rename that drops it re-opens cross-owner enumeration the codebase explicitly closed (commit `3cecbb3`, `253a4d8`). C-1. |
| Milestone-FK validation | **Elevation of Privilege** | `validateMilestoneFK` carries an ownership check (#406). Not in Test #3 scope. M-3. |
| Seed (`ensureInboxProject`) | **DoS (data integrity)** | No-lock list-then-write; concurrent boot could double-seed (H7). M-1. |
| Contract-regen pipeline | **Tampering (drift)** | Drift-fails-closed (#5) correctly fails the build on hand-edited generated files. Adequate. |

---

## Unasked Questions (for the spec author to resolve)

1. **Owner gate:** Is the rename behaviour-preserving including the `owner` access gate, or is the gate being removed? It cannot be both "behaviour-preserving" (US-5) and "owner is attribution only / no gate" (FR-1.7). (C-1)
2. **Seed name + owner:** The seed is "Main" with no owner today. Does the renamed seed set `owner=username` (a behaviour change → must be tested) or not (→ US-3 AC-2 is false)? (C-3)
3. **Inbox/delete-protection:** Does "My Workspace" inherit the Inbox's `is_default` delete-protection (409)? Spec is silent; the code enforces it. (C-3, M-2)
4. **On-disk task/milestone key:** Does the persisted task/milestone JSON key flip `project_id→workspace_id`? (Must, for contract consistency.) (M-3)
5. **Lazy migration:** Is `agent_ids→core_team` ported to the workspace reader or dropped? (M-4)
6. **Grep guard exact tokens & scope:** What is the precise token list and root set, given "projects/" literally matches nothing today and `cmd/` is excluded? (C-2, M-5)
7. **Concurrency contract:** Is concurrent boot supported (lock) or explicitly out of scope? (M-1)

---

## Verdict

**BLOCK.**

Three CRITICAL findings (C-1 owner-gate contradiction, C-2 unsound grep-guard,
C-3 false inventory/seed state) each independently prevent a correct
implementation. C-1 is the most serious: as written, the spec instructs the
implementer to treat a live security control as inert during a "behaviour-preserving"
rename. Fix the factual grounding in §2, resolve the owner-gate semantics, and
make the grep-guard a concrete, sound token list before this proceeds.

Address the findings, then re-run:

```
/grill-spec docs/internal/specs/v01-spec1-workspace-rename-spec.md
```
