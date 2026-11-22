`Status: In review`

`ADR: Export & Import, end to end (bundles, wizards, sidebar page), epic elicify-ai/omnipus#1094 — revision 3` (draft at `/workspace/logs/1094/adr-export-import-e2e.md`; on promotion it becomes amendments A12+ to [Export and import packages](../architecture/ADR-export-import-packages.md)). Base S1 spec: [export-import-s1-spec.md](export-import-s1-spec.md).

# Feature Specification: Export & Import — slice B1 (page, both wizards, one agent)

**Created**: 2026-11-21 · **Revision 2**: 2026-11-21 (after spec review round 1, `/workspace/logs/1094/b1-spec-review-1.md`, verdict REVISE) · **Revision 3**: 2026-11-21 (after spec review round 2, `/workspace/logs/1094/b1-spec-review-2.md`, verdict REVISE) · **Revision 4**: 2026-11-21 (after spec review round 3, `/workspace/logs/1094/b1-spec-review-3.md`, verdict REVISE) · **Revision 5**: 2026-11-21 (wording and fixture fixes from spec review round 4, `/workspace/logs/1094/b1-spec-review-4.md`, verdict PASS; no design change)
**Decisions in force**: Q1 = A (cancel call in B1, lifecycle made safe) and Q2 = A (self-hosted only; the desktop app is a separate founder question), both by the chief on 2026-11-21.
**Input**: founder requirements (Discord #team-a, 2026-11-21, message 1573688799848960202 and the earlier wizard cards; relayed by the chief, treated as given); ADR revision 3 at `/workspace/logs/1094/adr-export-import-e2e.md` (§D-E3.1, D-E4 layer 3, D-E5.1 single-agent row, D-E6, D-E7 row B1). No interview-me file exists for this slice: the founder's Discord requirement receipt and the ADR's §0.2 founder decisions are the interview record.
**Repo destination on promotion**: `docs/internal/specs/export-import-b1-spec.md` (this draft is written to `/workspace/logs/1094/b1-spec.md` because the task is read-only on the repo).
**Epic**: #1094 (Stage: Spec)
**Riders**: none confirmed. Checked open issues touching the same files: #1947 (empty `AGENT.md` exported but not created on import) touches `pkg/omnpkg/export.go` but needs a product decision and would change bundle content, which B1 must not do — **not** taken as a rider; #1945 (CLI `writeAtomic`) and #1946 (REST create drops voice) touch other files. Chief to confirm with the architect.
**Agent tool field table**: no agent-managed object gains or changes a property. Bundles are a person-only operation in B1 (decision and reasons in "Agent tool parity" below).

| Object | Property / action | User can set (B1) | Agent can set | Reason if different |
|---|---|---|---|---|
| Agent | every existing field | unchanged | unchanged (create_agent / update_agent, and tool permissions through set_agent_permissions, as today) | — (B1 changes no agent field; the inherited S1a field-coverage test and its explicit refusals stay in force; B1 does not claim every field is writable by every tool) |
| Bundle export (operation) | export one custom agent to a file | yes (wizard, Quick Export) | **no** | A bundle is a file handed to the person's browser; an agent has no safe destination for it (writing it into a workspace would put pasted secrets from prompts in reach of every agent with file access). Founder scoped the feature as a UI page. Revisit only with a founder decision. |
| Bundle import (operation) | add an agent from a file | yes (wizard) | **no** | An agent that could import would add agents (with their tool permissions) from any file it can read — a privilege path the create_agent tool's own checks do not cover as a whole. The create rules themselves are shared with create_agent already (S1a field-coverage test). |

---

## Review response 4

| Finding | Resolution | Where |
|---|---|---|
| R4-MIN-001 request counts without the `csrf_missing` exception | Summaries now say at most one package request per action, zero on client `csrf_missing`, counted per "Counting requests" (recovery read apart); applies to apply, explicit Cancel, reload and leaving. Cookie gate unchanged. | test 26, FR-025, FR-026 |
| R4-MIN-002 summaries promised eventual removal | Both summaries now say removal is attempted again at a later upload or repeated cancel, and the files stay if removal keeps failing. | Behavioral Contract, Edge Cases |
| R4-MIN-003 "exactly one wins" | Replaced with a reference to the checkpoint-based interleavings of "Cancel and apply at the same moment"; no exclusive-or or success-count assertion. | Edge Cases |
| R4-MIN-004 tied creation times in D5-6 | First "Plan store cap" row and D5-6 now use distinct fake-clock times (P*n* at T + *n* s), all unexpired when P33 is created, so P1 is uniquely oldest. | "Plan store cap", D5-6, test 10 |
| R4-MIN-005 cancel-413 copy contradicted the cancel evidence table | The 413 known-refusal text applies to **apply** only; a 413 on cancel stays under "any other answer" → Uncertain screen. | UI text table |

## Review response 3

| Finding | Resolution | Where |
|---|---|---|
| R3-MAJ-001 missing-cookie case required a request the client must refuse | Requests are now counted **per package URL and method**, separately from the wizard's recovery read `GET /api/v1/state`. Client `csrf_missing` (no CSRF cookie) → **zero** package requests; the existing cookie-presence gate in `http.ts::requestOnce` stays exactly as it is and the test must not fake a server `csrf_missing` answer. Server CSRF 403, 403 `planted_cookie_cleared`, network failure at dispatch and 5xx → exactly **one** package request, never an automatic second. After recovery a deliberate click sends one package request (for `csrf_missing`, the first). `csrf_missing` is now a row of the apply outcome classes, the record transitions (back to `previewed`), the cancel evidence table and the session-refresh scenario. Positive control for other callers kept. | "Wizard-owned recovery" and "Counting requests", "Apply outcome classes", "Import record transitions", "What a cancel answer proves", scenario "Session refresh on apply is not replayed", D4-11a/11b, FR-020, FR-024a, SC-002b, test 19 |
| R3-MIN-001 retention wording left in older sections | "Can be imported until" everywhere (US-4.1, Preview BDD, Preview screen). Impact Assessment now names the orphan scan inside the upload `Sweep`, not boot. Clean-up-failure and unknown-upload texts now promise an **attempt** ("Omnipus will try to remove its temporary files at a later upload"), not deletion; "Retention, in plain terms", US-7.3 and DOC-001 say removal can keep failing while a disk problem lasts. Review response 1 is labelled historical, and its boot-scan clause is marked superseded. | Impact Assessment, US-4.1, US-7.3, Preview BDD, UI text table, cancel tables, reload table, "Retention, in plain terms", DOC-001, Review response 1 note |
| R3-MIN-002 rules keyed on "after Import was selected" | Every such rule is now keyed on the **current record phase**: no cancel and the Outcome-unknown screen only while the record is `apply_sent` (Import sent, no authoritative answer); after a known refusal the record is `previewed` and a reload or leaving cancels the unused upload once. Rewritten: US-5.6, US-5.10, Behavioral Contract, Edge Cases, Qualitative Prohibitions. 413 on apply (refused before `BeginApply`) now has its transition (back to `previewed`, Import available) and UI text. The cancel evidence table now holds the session-refusal row (explicit Cancel stays on the step; reload goes to Uncertain), matching FR-026 and "Cancel from the wizard". | "Import record transitions", "What a cancel answer proves", US-5.6, US-5.10, Behavioral Contract, Edge Cases, Qualitative Prohibitions, UI text table, "Record after a known refusal", D4-11c |
| R3-MIN-003 expiry dataset impossible with its fixture | D5-7 now uses an explicit fake clock: P1 at T, P2…P32 at T+10 min, P33 at T+30:00 → P1 404, P2…P33 200. New D5-8 with three expired plans. Because every review lives exactly 30 minutes, "expired first" is observable only as "no unexpired review is lost while an expired one is held"; FR-008 says so. "Plan store cap" is now an outline over D5-6…8. | Dataset D5, scenario "Plan store cap", FR-008, test 10 |
| Review 3 dispositions of Review response 2 (4 partial) | R2-MAJ-001 → closed by R3-MAJ-001 row; R2-MAJ-003 → R3-MIN-001 row; R2-MIN-001 → R3-MIN-002 and R3-MAJ-001 rows; the collective round-1 closure → closed by this table as a whole. | as above |

## Review response 1

Historical record of revision 2. Where a row here conflicts with a later section, the later "Review response" tables and the body of the spec win.

| Finding | Resolution | Where |
|---|---|---|
| MAJ-001 automatic retries erase the unknown-outcome warning | Upload, apply and cancel are sent with automatic retry switched off per call (the app-wide mutation default of up to 3 retries for network and 5xx failures does not apply). Only a person's **Try again** after a positively identified pre-write refusal (`import_in_progress`) resends. An "unknown outcome" is defined for every answer that is not a parsed success or a listed coded refusal: network failure, timeout, proxy 5xx/502/503/504 without a package code, an unreadable or truncated 200 body. Never shown as "nothing added". A lost upload answer gets its own text and is not resent. | FR-024a, FR-020, "Apply outcome classes", Dataset D4, D2-14, tests 19, 24, 26, 26a |
| MAJ-002 reload during apply treated as abandoning an unused upload | Minimal per-tab record in browser session storage (non-secret: staging ID, phase, workspace ID and name, and after success the result summary). Phase `apply_sent` is written **before** the apply request leaves. A reload never cancels an upload whose phase is `apply_sent`; it shows the "may have finished" screen. Without a record, the cancel's own answer is the evidence (204 = unused and now cancelled; 409 = an import is running; 404 = gone, outcome unknown). A Result address without a stored result claims nothing and returns to the landing. ADR D-E6 B1 reload row narrowed accordingly. | FR-025, FR-025a, US-5.6/5.9, "Reload mid-import", Dataset D3, test 28 |
| MAJ-003 cancel lacks concurrency and deletion-failure contract | One registry operation under the registry's single lock: check `ready`, remove the folder, drop the entry — the same lock `BeginApply` takes, so either the cancel or the apply wins, never both. `receiving`, `applying`, `needs-cleanup` → 409 `staging_busy`, folder and journal untouched. Folder removal failure → 500 `staging_cleanup_failed`: the staging becomes `cancelled` (can never be applied, slot freed) and the folder is removed by the next upload's sweep or, after a restart, by the existing orphan scan (a ready staging has no journal). **Superseded in revision 3 and 4:** that orphan scan runs only inside the next upload's sweep, never at start-up, and removal is an attempt that can fail again (R2-MAJ-003, R3-MIN-001). Explicit **Cancel** shows its outcome; navigation clean-up stays silent. Contract lists all responses. | FR-010, FR-026, "API and Data", cancel BDD, tests 5, 13 |
| MAJ-004 review says connector assignments are not included | Wording corrected to what export and import actually do: the connector **names** (IDs) travel inside the agent's settings; connector settings and secrets do not; assignments are **not restored** on import. New reason value `not_restored`. Export content unchanged. Proof: decode a real exported bundle and compare with the review. | `left_out` rules table, FR-004, D1-2, test 3a, DOC-001 |
| MAJ-005 Result cannot tell Main from worker | After a successful apply, the Result looks the agent up by `created[0].new_id` through the existing agent lookup (`GET /api/v1/agents/{id}`). **Open chat** only when that lookup says type `Main` and activation is `active`. Loading shows the result immediately and a busy placeholder for the chat action; a failed lookup keeps the result and replaces the chat action with "Open it from the Team." No parsing of the activation text. | FR-023, US-4.4, "Import succeeds" outline, Dataset D4, test 27 |
| MAJ-006 "never reads secrets" has no detecting test | Added a no-open test around the review create, read and refusal routes, reusing the existing open watcher with its positive control (a deliberate `auth.json` open must be seen). Honest platform scope: the watcher is Linux-only (inotify); other platforms keep the output scans only, and the Linux CI leg owns the no-open proof. Added a decoded-bundle test (payload **and** manifest) for the B1 flow, with both free-text carriers (SOUL.md, AGENT.md) as positive controls and runtime values separated from allowed variable names. SC-002 split. | Security section, FR-005, FR-005a, Dataset D6, tests 4, 9a, 11, 11a, SC-002, SC-002a |
| MIN-001 Quick Export not edition-gated | Quick Export also requires identity mode `local`; tested for local, platform, unknown. | FR-027, Quick Export outline |
| MIN-002 download not checked against a lost plan | Download re-reads the review from the server immediately before calling the export; 404 → Choose with the expired notice. Stated limit: the export itself does not know the review, so content is as of download. | FR-019, US-2.8, "Expired review" |
| MIN-003 `cli_args` fixture type wrong | Fixture is a string (`"--token=CANARY_ARG --verbose"`), seeded through the typed agent store. | D1-4, D6-8 |
| MIN-004 "three files of at most 1 MiB" overstated | Corrected: prompt and instructions are bounded at 1 MiB each; the agent's settings record is not bounded by the export (inherited S1a limitation, documented, not fixed in B1). The three-1-MiB agent is a benchmark fixture, not a maximum. | Edge cases, SC-005, DOC-001 |
| MIN-005 local test commands wrong | Commands now use `heavy`, `CGO_ENABLED=0`, `-tags goolm,stdjson`, `-p 1`, one named test; Vitest one file; plus typecheck and touched-file design-system checks. Full suites stay in CI. | TDD "Local execution" |
| OBS-001 "invalidate once" | Means once per Result effect, using the existing helper; the `agent_created` frame may also refresh — both are fine. No global deduplication. | FR-023, "Lists refresh after import" |
| Review note on B1-UAT-09 | Manual two-tab row is a smoke check only; the serialisation proof is the automated controlled-overlap test (13a). | UAT table, test 13a |
| Review note on DOC-005 | DOC-005 now replaces the CLI page's "There is no screen for this…" sentence and the "No separate screen" table cell. | DOC-005 |
| Stale Q1/Q2 wording | Removed; both decided. | Open Questions → Decisions |

---

## Review response 2

Record of revision 3. Where a row conflicts with a later section, "Review response 3" and the body of the spec win.

| Finding | Resolution | Where |
|---|---|---|
| R2-MAJ-001 hidden transport retries can resend apply | All three replay layers are named and switched off for the four package calls only: query mutation retry (`retry: false`), CSRF recovery (`csrfRetry: false`, existing switch) and the planted-cookie toast's stored **Retry** (new not-wire-format switch `plantedCookieToast: false`, default on for every other caller). The wizard owns recovery: a CSRF 403, client `csrf_missing` or 403 `planted_cookie_cleared` is a known pre-write refusal, the record returns to `previewed`, and the person deliberately selects Import again. Go test 13c proves both middleware refusals leave the staging ready and add no agent; Vitest drives the **real** `request()` with a mocked `fetch` and asserts the dispatch count and that no toast action is created. | "No automatic resend — all three replay layers", Symbols/Impact (`http.ts`), FR-024a, scenarios "Session refresh on apply is not replayed" and "Session refresh refusals happen before the import starts (server)", tests 13c, 19, 26, SC-002b |
| R2-MAJ-002 404/409 cancel answers read as proof | One evidence rule for every cancel answer: 204 and 500 `staging_cleanup_failed` prove a cancel; 409, 404, lost or unreadable answers prove nothing (404 also covers an import already finished). Those land on the **Uncertain** screen ("could not confirm… may have been imported… check the Team") with Open Team or the workspace hint, and Start over. Applied to explicit Cancel and reload alike; silent navigation clean-up ignores the answer. | "What a cancel answer proves", Reload table (new rows for another tab's import and for cleanup failure), "Cancel from the wizard", FR-025, FR-026, test 28 |
| R2-MAJ-003 "removed on its own after 30 minutes" not true | Retention stated as the code does it: 30 minutes is import eligibility (and slot use) only; temporary files are removed by the next upload's clean-up (or a successful cancel), with no timer and no start-up clean-up in B1. Every message, US-5.11, US-7.3, FR-010, the cancel lifecycle, the clarification and DOC-001 corrected. The restart scenario is renamed to name its upload trigger, and a new scenario proves a restart alone removes nothing. | Cancel lifecycle, "Retention, in plain terms", UI text table, scenarios "Folder left after a restart is removed by the next upload" and "A restart alone removes nothing", FR-010, DOC-001 |
| R2-MAJ-004 race oracle forbade a legitimate apply clean-up | The exclusive-or assertion and "loser" wording are removed. Oracles per observation point: cancel first → 204, apply 404, no agent; apply paused after `BeginApply` → cancel 409 with folder, journal and state unchanged **at that checkpoint**; on resume the apply ends normally (success with `FinishApply`'s own folder removal, or rollback); cancel after done → 404; cancel after `AbortApply` → 204. | API "Consequence" list, US-7.4, Behavioral Contract, scenario outline "Cancel and apply at the same moment", tests 5a, 13a |
| R2-MIN-001 no record transition after a known refusal | "Import record transitions" table: 200 → `done`; refusals that leave the upload ready (`import_in_progress`, `workspace_not_found`, `default_workspace_missing`, `name_suffix_exhausted`, 400, 422, CSRF/planted 403) → back to `previewed` with a deliberate Import again; 404 `staging_not_found` and 500 `import_rolled_back` → cleared, File; `import_rollback_incomplete` → cleared, restart text; unknown → stays `apply_sent`. Proofs added: refusal then reload (one cancel, File), refusal then leave (one silent cancel), changed workspace then resubmit (one new apply with the new `workspace_id`). | "Import record transitions", US-5.12, scenario outline "Record after a known refusal", FR-024, FR-025a, FR-026, tests 26, 28 |
| R2-MIN-002 UAT-09 points at the wrong test; Reachability omits UAT-14 | UAT-09 now names `stagingD13OneApplyAtATime` (existing registry proof) and new route test 13b `TestPackageApply_TwoStagingsSerialised` (S1's apply paused after `BeginApply`; S2's apply answers 409 `import_in_progress`; on release S1 finishes and S2's deliberate Import again succeeds). 13a is only the same-staging cancel/apply test. Reachability requires B1-UAT-01…14. | UAT table, test 13b, scenario "Two uploads are imported one at a time (server)", Reachability |
| Round-1 partials (MAJ-001, MAJ-002, MAJ-003, MAJ-005 as listed by review 2) | Closed by the rows above: replay layers (MAJ-001), cancel evidence and record transitions (MAJ-002), retention and race oracles (MAJ-003); MAJ-005's lookup flow is unchanged and its Result screen is now reached only from a parsed 200 per the transition table. | as above |

## Overview

Omnipus can already pack one custom agent into a file and add it on another install, but only from the command line or the REST API (S1a, S1b). People who use the app cannot see or use it.

Slice B1 gives this feature its own place in the app: a new sidebar entry **Export & Import** opens a page with two step-by-step guides ("wizards"). The **Export** wizard lets the person pick one of their own agents, shows an honest review of what goes into the file — contents, size, what is left out, which secrets must be typed again, and a clear warning that text they wrote is copied as it is — and then downloads the file, which the app calls a **bundle** (file ending `.omnpkg`). The **Import** wizard takes a bundle file, shows a preview of what will be added and any warnings, adds the agent as a new agent into a workspace the person chooses, and shows the result. A **Quick Export** button on an agent's edit sheet opens the same Export wizard with that agent already chosen.

B1 deliberately uses today's one-agent engine: one agent per bundle, "Add as new" only, no change to the bundle format, and the existing all-or-nothing import. The page shows only what exists today (agents). Workspaces, skills, connectors, memory and chat history, the other scopes (Everything, One workspace, Pick items), Replace/Merge and progress for large bundles arrive with later slices (B2–B9) on this same page. The only new server pieces are a read-only **review** call (the "export plan") and a call that cancels an unused upload (Q1 = A). The page is offered only in the self-hosted edition (Q2 = A).

---

## Existing Codebase Context

GitNexus was not available in this session; context comes from `git show`/`git archive` of `origin/release/v0.1.1` at `616261783` with grep and read. Code wins over this table.

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `pkg/omnpkg/export.go::Export` | calls (unchanged) | Packs exactly one agent into a zstd tar in memory; refuses 0 or >1 IDs, built-ins, blank prompt, bad files. |
| `pkg/omnpkg/export.go::collectAgentFiles`, `::buildPackage`, `::cleanRecordForExport` | calls (reuse from a new plan function in the same package) | Same capture and cleaning the plan must apply so plan and export refuse identically. |
| `pkg/omnpkg/import_map.go::MapAgentForImport` | read only (oracle source for import warnings) | Skills bound by name or dropped, connector assignments dropped, runtime settings removed. |
| `pkg/omnpkg/staging.go::StagingRegistry` (`Release`, `BeginApply`, `State`, `Sweep`, `slotsUsedLocked`) | extends (new ready-only cancel; new `cancelled` state) | `Release` today drops any state and frees the entry before the folder delete; it is **not** reused for cancel. `BeginApply`, `Sweep` and every state change take the one registry mutex. |
| `src/lib/queryClient.ts::queryClient`, `::shouldRetryMutation` | overridden per call | App-wide mutation default retries network and 5xx failures up to 3 times; package mutations switch it off. |
| `src/lib/api/agents.ts::fetchAgent` | calls | `GET /api/v1/agents/{id}`; source of the imported agent's type on Result. |
| `src/store/chat/slices/frames.ts` (`agent_created` frame) | unchanged | Already calls `invalidateAfterAgentCreate` when the server announces the new agent. |
| `pkg/gateway/packages_t5_openwatch_linux_test.go` / `_other_test.go` (`newOpenWatch`) | reuses | inotify open watcher with probe (positive control); non-Linux stub asserts nothing. |
| `pkg/gateway/rest_packages.go::registerPackageRoutes` | modifies | Registers package routes inside the local-mode block of `rest.go::registerAdditionalEndpoints`. |
| `pkg/gateway/rest_packages.go::packageState` | extends | Gains the in-memory plan store. |
| `pkg/gateway/rest_packages_export.go::packageExportSource`, `::packageFileName` | calls | Store edge and download-name rule the plan reuses. |
| `pkg/gateway/rest_packages_apply.go::handlePackageImportApply` | modifies (method dispatch only) | Owns the `/api/v1/packages/import/` prefix; today answers non-POST with 405. |
| `src/components/layout/Sidebar.tsx::ASSET_ITEMS` | modifies | Assets section: Skills & Tools, Connectors, then the Library button. |
| `src/lib/api/identityMode.ts::currentIdentityMode` | calls | `local` / `platform` / `null` (null = not local, fail closed). |
| `src/components/agents/AgentProfile.tsx::AgentProfile` | modifies (≤ 5 lines: import + render of an extracted component) | 2,874 lines, grandfathered in `scripts/budgets/files.txt` (3,211): may only shrink in spirit, so all Quick Export logic lives in a new file. |
| `src/lib/api/agents.ts::fetchAgents`, `::invalidateAfterAgentCreate` | calls | Agent list for Choose; refresh of agent, team and sidebar lists after an import. |
| `src/lib/api/workspaces.ts` (`fetchWorkspaces`) | calls | Workspace picker on Confirm (`is_default` marks the default). |
| `src/lib/api/http.ts::request`, `::requestOnce`, `::withCsrfRetry`, `::raisePlantedCookieToast`, `RequestOptions` | modifies (one new not-wire-format option) and calls | JSON calls with CSRF; a raw-response variant is needed for the binary download (pattern: `src/lib/api/mail.ts` returns `res.blob()`). Today a CSRF 403 is re-sent once by `withCsrfRetry` unless `csrfRetry: false`, and a planted-cookie 403 raises a toast whose **Retry** re-sends the original request. B1 adds `RequestOptions.plantedCookieToast?: boolean` (default `true`); `false` skips the toast and its stored retry and returns the error to the caller. |
| `src/routes/_app/library.tsx` | pattern | Zod `validateSearch` URL state for a page route under `_app`. |
| `src/routes/_app/workspaces.$workspaceId.team.tsx` | navigates to | Team is a panel of the workspace chat (`?panel=team`). |
| `tests/e2e/shards.json` | modifies | Every new spec must be assigned to exactly one shard (`scripts/e2e-shards.sh check`). |

**Name collision to avoid:** `pkg/gateway/packages_b1_*_test.go` already exist and mean an older S1a CHECK batch, not this slice. New Go tests use the stem `packages_exportplan_*` and `packages_stagingcancel_*`.

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| `registerPackageRoutes` | LOW | route tests `packages_t5_wired_red_test.go`, auth negative tests | CLI `pkg/app/internal/pkgcmd` (uses the same routes; unchanged paths) |
| `handlePackageImportApply` dispatch | MEDIUM | `packages_t5_apply_red_test.go`, `packages_s1b_*` apply tests; a DELETE branch must not change any POST outcome | CLI `omnipus import` |
| `StagingRegistry` new cancel method and `cancelled` state | MEDIUM | `staging_state_machine_test.go`, `Sweep` (gains the `cancelled` case), `slotsUsedLocked` (does not count `cancelled`) | upload slot accounting (`too_many_staged_imports`), orphan scan inside the upload `Sweep` (unchanged; removes journal-less folders, including those left by a restart; boot recovery `RecoverImports` skips them) |
| `packageState` (+plan store) | LOW | none outside `rest_packages*.go` | — |
| `omnpkg` new plan function (no change to `Export`) | LOW | none (new) | — ; `Export` and its tests stay byte-for-byte unchanged |
| `Sidebar.tsx` Assets list | MEDIUM | Sidebar Vitest files, AppShell tests, `tests/e2e/accessibility.spec.ts`, sidebar e2e specs that count Assets entries | `docs/using-omnipus-ui.md` |
| `AgentProfile.tsx` header | LOW–MEDIUM | `AgentProfile.test.tsx` (header snapshot / button counts) | Team panel e2e specs |
| `http.ts` `RequestOptions.plantedCookieToast` (default `true`) | LOW | existing `http.test.ts` planted-cookie and CSRF tests (must stay green unchanged: default behaviour is unchanged) | every API module (none pass the new option except `packages.ts`) |
| `contracts/openapi.yaml` (+3 operations, +2 schemas) | LOW | `make verify-contracts`, generated Go and TS types | `scripts/check-no-handwritten-wire-types.sh` |

No HIGH or CRITICAL row.

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Export (`POST /packages/export` → `omnpkg.Export` → audit → bytes) | Download step calls it unchanged. |
| Upload (`POST /packages/import` → staging → checks → preview) | Import File step calls it unchanged. |
| Apply (`POST /packages/import/{id}/apply`, mode `new`, all-or-nothing, pending record, commit rename, reload, `activation`) | Import Confirm step calls it unchanged (ADR D-E5.1 single-agent row). |
| Start-up recovery (`rest_packages_recovery.go`) | Untouched; B1 shows `import_rollback_incomplete` text that points to a restart. |
| Agent created invalidation (`invalidateAfterAgentCreate`) | Result step triggers it so sidebar, Team and pickers show the new agent. |

---

## Contract Changes (contract-first — Hard Constraint #8)

| Schema / operation | New / Changed | File | Notes |
|---|---|---|---|
| `PackageExportPlanRequest` | new | `contracts/components/schemas/PackageExportPlanRequest.yaml` | `scope` (string enum, B1: `agents` only), `agent_ids` (array of string). Both required, `additionalProperties: false`. A count other than one is refused by the server (400 `export_selection_invalid`), not by the schema, so the B2 extension (several agents, `roots`, `exclude`, other scopes) is additive. |
| `PackageExportPlan` | new | `contracts/components/schemas/PackageExportPlan.yaml` | Response of plan create and plan read; fields below. |
| `POST /packages/export/plan` (`createPackageExportPlan`) | new | `contracts/openapi.yaml` | 201 `PackageExportPlan`; 400, 401, 404, 405, 413, 422, 503 as listed in "API and Data". Tag `Settings` like the other package operations. |
| `GET /packages/export/plan/{plan_id}` (`getPackageExportPlan`) | new | `contracts/openapi.yaml` | 200 `PackageExportPlan`; 401, 404 `plan_not_found`, 405, 503. |
| `DELETE /packages/import/{staging_id}` (`cancelPackageImport`) | new | `contracts/openapi.yaml` | 204 no body; 401; 404 `staging_not_found`; 405; 409 `staging_busy`; 500 `staging_cleanup_failed` (cancelled, folder removal pending); 503. |
| `PackageExportRequest`, `PackageImportPreview`, `PackageImportApplyRequest`, `PackageImportResult`, `PackageManifest` | **unchanged** | existing files | B1 changes no bundle format and no existing wire shape. |
| `ErrorResponse` | unchanged | existing | New codes carried in `code`: `plan_not_found`, `staging_busy`, `staging_cleanup_failed`. |
| `Agent` (`GET /agents/{id}`) | unchanged | existing | Read on Result for the imported agent's `type`. |

**`PackageExportPlan` fields** (all required unless marked optional; `additionalProperties: false` on every object):

| Field | Type | Meaning |
|---|---|---|
| `plan_id` | string (UUID v4) | Handle for re-reading this review. |
| `created_at`, `expires_at` | date-time (UTC) | `expires_at` = `created_at` + 30 minutes. |
| `scope` | string, enum `agents` | Echo of the request. |
| `elements` | array (exactly 1 item in B1) | Each: `type` (string, `agent`), `source_id`, `name`, `kind` (enum `main`, `worker`, `external_worker`), `files` (array of `{path, size}`; `size` int64 bytes), `bytes` (int64, sum of its file sizes). |
| `left_out` | array | Each: `type` (open string: `skill`, `connector`, `agent_memory`, `session`, `work_files`, `team`, `agent_setting`), `reason` (open string; B1 values: `type_not_exportable`, `host_path`, `secret_value`, `not_carried`, `not_restored`), `message` (plain English, never a value), `items` (optional array of names: skill names, connector IDs, environment variable names). Open strings because later slices add values (ADR D-E1.2). |
| `needs_secrets` | array | Same item shape as `PackageManifest.needs_secrets` (`name`, `used_by`, `kind`). |
| `disclosures` | array | Each `{code, message}`; B1 always carries exactly one: `free_text_copied`. |
| `size_bytes` | int64 | Byte length of the bundle as it would be built at plan time. |
| `file_name` | string | Download name the export will use (same rule as the export's `Content-Disposition`). |

`plan_digest` (ADR D-E3.1) is **not** in B1: nothing in B1 checks it, and an unchecked digest would look like a binding it is not. B2 adds it with the export job that verifies it (additive change).

Regenerate with `scripts/gen-contracts.sh`; `make verify-contracts` must pass; the SPA uses the generated Zod schemas (`src/lib/api/generated/schemas.ts`) for every response it parses; no hand-written wire type (`scripts/check-no-handwritten-wire-types.sh`).

## API and Data

| Route | Auth | Behaviour |
|---|---|---|
| `POST /api/v1/packages/export/plan` | local mode only; `withAuth`; refused while developer bypass is on (503); 1 MiB JSON body limit (413) | Runs the same capture, cleaning and refusals as export for the one agent, builds the bundle in memory to measure it, discards the bytes, stores the plan for 30 minutes, returns 201. Writes nothing to the data folder, writes no audit entry, uses no staging slot. Errors: 400 `export_selection_invalid` (agent count ≠ 1) or invalid JSON / unknown scope; 404 `agent_not_found`; 422 `core_agent_not_exportable`, `agent_prompt_missing`, `element_not_creatable`, `agent_file_unreadable` (with `details.file`, `details.check`), `external_worker_not_supported_yet` if still applicable on the release; 405 other methods. |
| `GET /api/v1/packages/export/plan/{plan_id}` | same | Returns the stored plan unchanged while held; 404 `plan_not_found` for an unknown, malformed or expired ID. Does not re-capture. |
| `DELETE /api/v1/packages/import/{staging_id}` | same | See "Cancel lifecycle" below. |
| `POST /packages/export`, `POST /packages/import`, `POST /packages/import/{id}/apply` | unchanged | Used by the wizards as they are. |

**Plan store** (in memory, per gateway process): at most 32 plans; adding the 33rd first drops expired plans, then the oldest by `created_at`. Plans are not kept across a gateway restart (a restart answers `plan_not_found`). A plan holds only what its response shows (no bundle bytes).

**Cancel lifecycle** (one registry operation, under the same single registry lock that `BeginApply`, `Reserve`, `Sweep` and every other state change take; no separate state check before it):

| Staging state when the lock is taken | Action inside the lock | Answer | After |
|---|---|---|---|
| `ready` (expired or not) | remove the folder; on success drop the entry | 204 | slot free, folder gone, ID unknown (`staging_not_found` from then on) |
| `ready`, folder removal fails | entry moves to new state `cancelled` | 500 `staging_cleanup_failed` | can never be applied (`BeginApply` accepts only `ready`), does **not** count as a slot. Removal is **attempted again** only when something triggers it: the next upload's `Sweep` (which drops the entry when removal succeeds), or a second cancel on the `cancelled` entry (204 on success, 500 again on failure). After a gateway restart the in-memory entry is gone and the journal-less folder is an orphan; the existing orphan scan, which runs **inside the next upload's `Sweep`, not at start-up**, attempts its removal. Nothing removes it on a timer or at boot, and removal is not promised to succeed while the filesystem error persists. |
| `receiving`, `applying`, `needs-cleanup` | nothing | 409 `staging_busy` | folder, journal and state unchanged |
| `done` or unknown ID | nothing | 404 `staging_not_found` | — |

Consequence, stated at observation points (the lock is held only for each state change, not for the whole import):
- Cancel takes the lock while S is `ready` → 204; a later apply on S gets 404 `staging_not_found`; no agent is created.
- Apply's `BeginApply` has moved S to `applying` and the import is still running → a cancel gets 409 `staging_busy` and changes nothing (folder, journal, state).
- After the import finished (`FinishApply` sets `done` and deletes the folder as part of the unchanged apply lifecycle) → a cancel gets 404 `staging_not_found`.
- If the apply was refused before writing (the unchanged `AbortApply` returns S to `ready`) → a later cancel gets 204.
Cancel never deletes an `applying` or `needs-cleanup` folder or its journal; the apply's own clean-up of its staging is unchanged and expected. Cancel is not audited (no data enters or leaves).

**Retention, in plain terms** (what B1 promises, and no more): an upload can be imported for 30 minutes; after that it is no longer usable and no longer counts as one of the five places. Its temporary files stay on disk until a removal succeeds: removal is attempted by the next upload's clean-up (or a cancel); while a disk problem lasts, each attempt can fail and the files stay until a later attempt succeeds. Omnipus does not delete them on a timer or at start-up in B1.

**`left_out` rules for one agent** (observable; values are never emitted):

| Condition in the stored agent | `left_out` row (`type` / `reason`) | `items` | Message (exact) |
|---|---|---|---|
| Always | `agent_memory` / `type_not_exportable` | — | "This agent's memory is not included in this version." |
| Always | `session` / `type_not_exportable` | — | "This agent's chats are not included." |
| Always | `work_files` / `type_not_exportable` | — | "Workspace files are not included." |
| Always | `team` / `not_carried` | — | "Team membership and delegation lines are not included. On import the agent joins the workspace you choose, with no delegation lines." |
| The agent names one or more skills | `skill` / `type_not_exportable` | the skill names | "Skills are not included. The agent keeps their names; the other install uses a skill with the same name if it has one." |
| The agent is assigned one or more connectors | `connector` / `not_restored` | the connector IDs | "Connector names travel inside the agent's settings, but no connector settings or secrets are included, and the assignments are not restored on import. Assign connectors again after import." |
| External worker with a program path | `agent_setting` / `host_path` | — | "The program path is not included. The other install uses its own setting for this tool." |
| External worker with environment overrides | `agent_setting` / `secret_value` | the variable names | "Environment override values are not included. Enter them again after import." |
| External worker with extra CLI arguments | `agent_setting` / `secret_value` | — | "Extra command-line arguments are not included. Enter them again after import." |

Order of rows: the table order. A row whose condition is false is absent. The skill and connector rows describe names that **do** travel inside the agent's settings record (export keeps them; import binds same-named skills and drops connector assignments, S1 Field mapping); B1 does not change export content to make any row truer.

**`disclosures`** (always, exactly this text, ADR D-E4 layer 3): `free_text_copied` — "Text you wrote (prompts and instructions) is copied exactly as it is. If you pasted a password or key into it, it goes with the bundle."

**No automatic resend — all three replay layers.** For the upload (`POST /packages/import`), the apply (`POST /packages/import/{id}/apply`), the cancel (`DELETE /packages/import/{id}`) and the export download (`POST /packages/export`, which writes an audit entry), each of these must be switched off **for these calls only** (other callers keep their recovery):

| Replay layer (existing code) | What it does today | B1 rule for the four package calls |
|---|---|---|
| Query mutation retry (`queryClient.ts::shouldRetryMutation`) | up to 3 retries on network failure and 5xx | `retry: false` on each package mutation |
| CSRF recovery (`http.ts::withCsrfRetry`, used by `requestOnce`) | on a CSRF 403, re-mints the cookie and re-sends once | `RequestOptions.csrfRetry: false` (existing switch) |
| Planted-cookie toast (`http.ts::request` → `raisePlantedCookieToast`) | puts a **Retry** action in a global toast that re-sends the original request later, with its result going nowhere | new transport switch `RequestOptions.plantedCookieToast: false` (in-browser option, `// not-wire-format`, default `true` so every other caller is unchanged): no toast and no stored retry closure for this call; the error is returned to the wizard |

The export download and the upload use the same `request()` path (or a raw-response variant built on `requestOnce` with the same two switches); no package call may use a transport path that bypasses these switches.

**Wizard-owned recovery.** Instead of the transport recovering on its own, the wizard treats these answers as **known pre-write refusals** (the request was rejected by middleware before the package handler ran, so the staging is still `ready`): CSRF 403 (the existing CSRF-failure body), 403 `planted_cookie_cleared`, and client `csrf_missing` (the existing gate in `http.ts::requestOnce` throws it **before** any request leaves when no CSRF cookie is present; B1 keeps that gate unchanged). The wizard shows "Your session needed refreshing. Nothing was imported. Select Import again." (or "…Select Download again." / "…Choose the file again."), refreshes the session with one safe read (`GET /api/v1/state`), and waits for the person's deliberate action. A Go test proves the "staging still ready, no agent" fact for both server rejections on the apply route (test 13c).

**Counting requests.** Package requests are counted by package URL and HTTP method; the wizard's recovery read `GET /api/v1/state` is counted separately and never counts as a package request. Per deliberate action: server CSRF 403, 403 `planted_cookie_cleared`, network failure at dispatch and 5xx → exactly one package request and no automatic second; client `csrf_missing` → zero package requests. After recovery, the person's next deliberate action sends exactly one package request (for `csrf_missing`, the first).

**Apply outcome classes** (the browser sorts every apply answer into exactly one):

| Class | Answers | Meaning shown |
|---|---|---|
| Added | 200 with a body that parses against `PackageImportResult` | the Result |
| Not added, known | client `csrf_missing` (no request sent); 400; 403 CSRF failure; 403 `planted_cookie_cleared`; 404 `staging_not_found`, `workspace_not_found`; 409 `import_in_progress`, `name_suffix_exhausted`, `default_workspace_missing`; 413; 422 (any package code); 500 `import_rolled_back` | the mapped text; "nothing was added" only where the code says so; next step and record per "Import record transitions" |
| Failed, clean-up pending | 500 `import_rollback_incomplete` | restart text |
| **Unknown** | network failure or abort; timeout; any 5xx without one of the codes above (including proxy 502/503/504 bodies, and the uncoded developer-bypass 503 — conservative on purpose, since its body carries no `code`); 200 whose body is unreadable, truncated or fails schema parsing; any other status | "The connection dropped before Omnipus answered…" text; **Import** is not offered again for this upload; link to the chosen workspace's Team |

An unknown upload answer (same causes, on `POST /packages/import`) shows: "Omnipus did not confirm the upload. Choose the file again. If the upload did arrive, it can no longer be used after 30 minutes, and Omnipus will try to remove its temporary files at a later upload." and is not resent.

**Per-tab import record** (browser session storage, key `omnipus.exportImport.import`, one record per tab; no bundle bytes, no password, no secret names): `staging_id`, `phase` (`previewed` → `apply_sent` → `done`), `workspace_id`, `workspace_name`, `written_at`, and after success `result` (final name, `new_id`, workspace name, activation). `phase: apply_sent` is written **before** the apply request is sent. The record is cleared by **Import another**, **Done**, a successful **Cancel** (204), and when older than 2 hours. It is evidence for reloads only; it is never sent to the server.

**Import record transitions** (every apply answer class sets the record and the next deliberate action; nothing is reset on an ambiguous answer):

| Apply answer | Upload state it proves | Record phase after | Screen and next deliberate action |
|---|---|---|---|
| 200 parsed | used | `done` (+ result summary) | Result |
| 409 `import_in_progress`, 403 CSRF, 403 `planted_cookie_cleared`, 413 (apply body too large) | still `ready` (refused before `BeginApply` or by middleware) | back to `previewed` | Confirm with the message (session text after one recovery read `GET /api/v1/state` for the two 403s); **Import** available again |
| client `csrf_missing` (no CSRF cookie: the existing transport gate refuses **before** any request leaves) | still `ready` (nothing was sent) | back to `previewed` (it was set to `apply_sent` just before the call) | Confirm with the session text after one recovery read `GET /api/v1/state`; the next deliberate **Import** sends the **first** apply request |
| 404 `workspace_not_found`, 409 `default_workspace_missing`, 409 `name_suffix_exhausted`, 400, 422 | still `ready` (refused in preparation; the unchanged apply calls `AbortApply`) | back to `previewed` | Confirm with the message; the workspace list is refreshed for `workspace_not_found`; **Import** available again after the person changes the workspace (or renames an agent for the suffix case) |
| 404 `staging_not_found` | not usable (expired, used or cancelled) | cleared | File step with the expired-or-used text |
| 500 `import_rolled_back` | not usable for another apply (treated as consumed) | cleared | Confirm area shows "The import failed and was undone. Nothing was added."; **Start over** goes to File |
| 500 `import_rollback_incomplete` | kept for clean-up (`needs-cleanup`) | cleared | restart text; **Start over** |
| Unknown class | unknown | stays `apply_sent` | Outcome-unknown screen; no Import, no cancel |

A 500 `import_rolled_back` is treated as "choose the file again" without asserting the staging's exact state; the wizard does not try a second apply on it.

**What a cancel answer proves** (one rule for explicit **Cancel**, reload, and the error map; silent navigation clean-up ignores the answer because the person has left):

| Cancel answer | Proves | Screen |
|---|---|---|
| 204 | the upload was unused and is now cancelled | landing (explicit Cancel) or File with the reload notice (reload); record cleared |
| 500 `staging_cleanup_failed` | cancelled (can no longer be imported); files not yet removed | same as 204, plus "The upload was cancelled. Omnipus will try to remove its temporary files at a later upload." |
| 409 `staging_busy` | the upload is being received, being imported, or kept after a failed import — **not** proof of a successful import | **Uncertain** screen |
| 404 `staging_not_found` | the upload is gone: it expired, was cancelled, **or was imported** (`done` also answers 404) | **Uncertain** screen |
| 403 CSRF failure, 403 `planted_cookie_cleared`, or client `csrf_missing` (no cancel request sent) | the cancel did not run; nothing about whether the upload was used elsewhere | explicit **Cancel**: stays on the step with "Your session needed refreshing. Select Cancel again.", record unchanged, nothing re-sent; reload: **Uncertain** screen, no second cancel; navigation clean-up: ignored |
| lost, unreadable, or any other answer not listed above | nothing | **Uncertain** screen |

**Uncertain screen** (reached from explicit Cancel or a reload): "Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again." with **Open Team** when the record names a workspace, otherwise "check the Team of the workspace you chose", and **Start over** (clears the record; sends nothing). The Outcome-unknown screen after an apply uses its own text but the same two actions.

---

## User Stories & Acceptance Criteria

### User Story 1 — Find Export & Import in the sidebar (Priority: P1)

A person who runs Omnipus on their own machine wants one obvious place to move agents between installs, so they do not have to know the command line.

**Why this priority**: without an entry point the wizards are unreachable (Definition of Done), but it is useless without US-2 and US-4.

**Independent Test**: sign in on a self-hosted install, open the sidebar, select **Export & Import**, see the page with **Start export** and **Start import**.

**Acceptance Scenarios**:

1. **Given** a signed-in person on the self-hosted edition, **When** they open the sidebar, **Then** the Assets section shows **Export & Import** after **Connectors** and before **Library**.
2. **Given** the sidebar, **When** the person selects **Export & Import**, **Then** the Export & Import page opens with a heading, **Start export** and **Start import**, and the entry shows as the current page.
3. **Given** the desktop or hosted edition (omnipus.ai sign-in), **When** the person opens the sidebar, **Then** no Export & Import entry is shown.
4. **Given** the desktop or hosted edition, **When** the person opens the page's address directly, **Then** the page says Export & Import is not available in this edition and offers no wizard.

### User Story 2 — Export one agent as a bundle with an honest review (Priority: P0)

A person wants to save one of their own agents as a bundle file and know exactly what is in it before it leaves the app — contents, size, what stays behind, which secrets must be typed again, and that text they wrote travels as it is.

**Why this priority**: the founder's core export promise; the review is the user's only safeguard against sharing a pasted secret.

**Independent Test**: with one custom agent, run Start export → choose it → Review → Download, and get a `.omnpkg` file whose agent imports elsewhere.

**Acceptance Scenarios**:

1. **Given** the person has custom agents, **When** they start an export, **Then** the first step lists only their own agents (no built-in agents), one choice at a time, with a note that a bundle holds one agent in this version.
2. **Given** no agent is chosen, **When** the person looks at the step, **Then** **Review** is unavailable and a hint says to choose an agent.
3. **Given** an agent is chosen, **When** the person selects **Review**, **Then** the review shows the agent's name and kind, each file with a plain label and size, the bundle size, everything left out with reasons, any secrets to enter again, the "text you wrote is copied as it is" notice, and a fixed statement of what is never included.
4. **Given** the review, **When** the person selects **Download**, **Then** the browser saves a file named after the agent ending in `.omnpkg`, and the step shows the saved name and its actual size.
5. **Given** the person has no custom agents, **When** they start an export, **Then** an empty state explains that built-in agents are on every install and are not exported, and nothing can be reviewed.
6. **Given** the chosen agent cannot be exported (blank prompt, unreadable or too large file, a worker without a description), **When** the person selects **Review**, **Then** a plain-language message names the reason and the person stays on the choose step.
7. **Given** the review is open, **When** the person reloads the page within 30 minutes, **Then** the same review is shown again.
8. **Given** the review is older than 30 minutes, was dropped, or the server restarted, **When** the person reloads or selects Download, **Then** the app asks the server for the review first and, when the server no longer holds it, takes them back to choose (the agent still ticked when it came from the address) with a notice that the review expired. (The download itself is not tied to the review: its content is the agent as it is at download time.)

### User Story 3 — Quick Export from the agent's edit sheet (Priority: P1)

A person looking at one agent wants to export it without hunting for it in a list.

**Why this priority**: founder requirement; a shortcut into US-2.

**Independent Test**: open a custom agent's edit sheet, select **Export**, land on the export wizard's choose step with that agent ticked.

**Acceptance Scenarios**:

1. **Given** the edit sheet of a custom agent, **When** the person selects **Export**, **Then** the sheet closes and the Export & Import page opens on the export choose step with that agent already chosen.
2. **Given** the edit sheet of a built-in agent, or any agent's sheet in the desktop or hosted edition, **When** it is shown, **Then** there is no **Export** button.
3. **Given** an address that pre-chooses an agent that no longer exists, is built in, or is not an agent, **When** the page opens, **Then** nothing is ticked and a notice says why.

### User Story 4 — Import a bundle as a new agent (Priority: P0)

A person with a bundle file wants to see what it would add, choose the workspace, add it without changing anything that already exists, and see the outcome.

**Why this priority**: the other half of the founder's core promise.

**Independent Test**: Start import → choose a valid bundle → preview → confirm into a workspace → result names the new agent, which appears in that workspace's Team.

**Acceptance Scenarios**:

1. **Given** the import wizard, **When** the person chooses a bundle file, **Then** the app checks it and shows a preview: the agent that will be added with the name it will get, items skipped, warnings in plain words, secrets to enter again, and until when the upload can be imported.
2. **Given** an agent with the same name already exists, **When** the preview is shown, **Then** it shows the new name (for example "Writer (imported)") and says the existing agent is not changed.
3. **Given** the preview, **When** the person continues, **Then** the confirm step states that the agent is added as new and existing agents are never changed, and offers a workspace choice with the default workspace pre-chosen.
4. **Given** the confirm step, **When** the person selects **Import**, **Then** the result shows the new agent's final name and workspace, whether it is active yet, any warnings and secrets, and a link to that workspace's Team; a link to the agent's chat appears only when the app has confirmed the new agent is a Main agent and it is active (if that check fails, the result stays and says to open the agent from the Team).
5. **Given** a successful import, **When** the result is shown, **Then** the sidebar and Team lists show the new agent without a manual reload.
6. **Given** the install has no default workspace, **When** the confirm step opens, **Then** no workspace is pre-chosen and **Import** is unavailable until one is chosen.

### User Story 5 — Problems are explained and recoverable (Priority: P1)

A person who picks the wrong file, waits too long, reloads, or collides with another import wants a clear message and a way forward, never a silent failure or a half-imported agent.

**Why this priority**: the founder's "honest" requirement; these are the realistic failure paths.

**Independent Test**: each row of Dataset D2/D4 produces its listed message and next step.

**Acceptance Scenarios**:

1. **Given** a file that is not a bundle, **When** the person chooses it, **Then** the message says it is not an Omnipus bundle and they stay on the file step.
2. **Given** a bundle made by a newer Omnipus, **When** the person chooses it, **Then** the message says to update this install first.
3. **Given** a file over 64 MB, **When** the person chooses it, **Then** it is refused before uploading, with the limit named.
4. **Given** the upload has expired, **When** the person selects **Import**, **Then** they return to the file step with a notice to choose the file again, and nothing was added.
5. **Given** another import is running, **When** the person selects **Import**, **Then** a message says so and offers **Try again**, and they stay on the confirm step.
6. **Given** the page is reloaded on the preview or confirm step **while no import is in flight for it** (Import not yet selected, or selected and refused with a known answer that returned the record to `previewed`), **When** it opens again, **Then** the earlier upload is cancelled, the file step shows a notice to choose the file again, and that upload no longer counts against the upload limit.
7. **Given** the import failed and was undone, **When** the result arrives, **Then** the message says nothing was added.
8. **Given** **Import** was selected and no clear answer arrives (connection lost, timeout, gateway or proxy error, unreadable answer), **When** the wizard handles it, **Then** it sends nothing again by itself, says the import may have finished, links to the chosen workspace's Team, and does not offer **Import** again for that upload.
9. **Given** the result step, **When** the person reloads in the same tab, **Then** the page shows the same result summary again without importing again; **given** a result address opened without that tab's record, **Then** the page claims nothing and shows the landing.
10. **Given** **Import** was sent and no authoritative answer has arrived (record phase `apply_sent`: still waiting, or an unknown outcome), **When** the page is reloaded before a result is shown, **Then** the page does not cancel the upload and does not ask for the file again; it says the import may have finished and links to the chosen workspace's Team.
11. **Given** the choose-file step, **When** the upload's answer is lost, **Then** the app does not resend it and says the upload was not confirmed, that the file should be chosen again, and that an upload that did arrive can no longer be used after 30 minutes, and Omnipus will try to remove its temporary files at a later upload.
12. **Given** a known refusal of **Import** that leaves the upload usable (another import running, a removed workspace, no default workspace, every "(imported N)" name taken, a refused agent rule, or a session refresh), **When** the message is shown, **Then** the person stays on Confirm, can correct the choice, and only a deliberate **Import** sends again; a reload or leaving the wizard after such a refusal treats the upload as unused and cancels it once.

### User Story 6 — A review call the wizard and scripts can rely on (Priority: P0)

The export wizard (and any script) needs the server, not the browser, to say what a bundle will contain and leave out, using the same rules as the export itself, without changing anything.

**Why this priority**: the founder requires the review to be authoritative; the wizard's review step is built on it.

**Independent Test**: call the review for each Dataset D1 agent and compare with the oracle table; confirm nothing on disk or in the audit log changed.

**Acceptance Scenarios**:

1. **Given** a custom agent, **When** a signed-in caller asks for its review, **Then** the answer lists the agent, its files and sizes, the left-out rows of the rules table, secrets to enter again, the free-text notice and the bundle size, and gives a review ID valid for 30 minutes.
2. **Given** an agent the export would refuse, **When** its review is asked for, **Then** the answer is the same refusal the export gives.
3. **Given** a review ID, **When** it is read back within 30 minutes, **Then** the same review is returned; after that, or after a restart, the answer is "review not found".
4. **Given** any review request, **When** it completes, **Then** no file in the data folder changed and no audit entry was written.
5. **Given** an external worker with stored runtime values, **When** its review is returned, **Then** no runtime value appears anywhere in the answer, only names.
6. **Given** a caller who is not signed in, or developer bypass mode, or the desktop or hosted edition, **When** they call the review, **Then** it is refused (not signed in; bypass on; route absent).

### User Story 7 — An unused upload can be cancelled (Priority: P2)

A person who leaves or restarts the import wizard should not block later imports for 30 minutes with abandoned uploads.

**Why this priority**: today at most five uploads are kept for 30 minutes each; five reloads would block importing.

**Independent Test**: upload, cancel, then upload five more times: all five succeed.

**Acceptance Scenarios**:

1. **Given** a checked upload waiting for confirmation, **When** the person selects **Cancel** (or leaves the wizard, or reloads on preview/confirm), **Then** the upload is discarded and its slot is free.
2. **Given** an upload being imported right now, or still being received, or kept for clean-up after a failed import, **When** a cancel arrives for it, **Then** the cancel is refused and the upload, its files and its recovery record are unaffected.
3. **Given** the person selects **Cancel** and the temporary files cannot be removed, **When** the answer arrives, **Then** the upload can no longer be imported, it no longer blocks new uploads, the person is told Omnipus will try to remove its temporary files at a later upload, and the next upload's clean-up attempts that removal again (it can fail again while the disk problem lasts) (nothing removes them on a timer or at start-up).
4. **Given** a cancel and an **Import** for the same upload arrive at the same moment, **When** both are handled, **Then** if the cancel is handled first the upload is cancelled and the import reports it expired or used, with no agent added; if the import has started first the cancel is refused and changes nothing, and the import finishes in its usual way (including its own clean-up of the upload); a cancel after a finished import reports the upload as gone.

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Sidebar entry | — (rendered with the Assets list) | — | — | hidden while the edition is unknown (fail closed) | entry visible, current-page style on `/export-import` |
| Export & Import page (landing) | — | — | platform edition: "Export & Import is not available in this edition." | — | heading "Export & Import", two cards: **Start export** ("Save one of your agents as a bundle file."), **Start import** ("Add an agent from a bundle file.") |
| Export · Choose agent | skeleton list while agents load | "You have no agents of your own yet. Built-in agents are on every install and are not exported." | agent list failed: query error state with **Retry**; plan refusal: inline error above **Review** | filter shows no match: "No agent matches." | radio list (name, kind label Main / Worker / External worker, description), filter field, note "A bundle holds one agent in this version.", **Review** |
| Export · Review | "Preparing the review…" (spinner, announced) | — | plan read 404 → back to Choose with notice; network error with **Retry** | — | agent name + kind; "What goes into the bundle" table (Settings `agent.json`, Prompt `SOUL.md`, Instructions `AGENT.md` with sizes); "About N KB"; "Left out" list; "Enter again after import" list (if any); free-text callout (warning tone); "Never included" statement; **Back**, **Download** |
| Export · Download | "Checking the review…" then "Creating the bundle…" | — | mapped error text + **Back to review**; review no longer held on the server (re-read just before the download) → Choose notice | — | "Saved <file name> (<size>)." **Export another**, **Done** |
| Import · Choose file | "Checking the bundle…" (spinner, announced) after a file is chosen | file button "Choose a bundle file" with hint "Bundle files end in .omnpkg" | mapped error text (role alert), stays on step | — | moves to Preview |
| Import · Preview | — | — | — | skipped items / warnings shown in their own groups | "Will be added": source name → final name (+ "renamed: an agent with this name exists; it is not changed"); "Skipped"; "Warnings"; "Enter again after import"; "Can be imported until <time>"; **Cancel**, **Continue** |
| Import · Confirm | workspace list loading: select disabled with spinner | no active workspace: "Create a workspace first." with no **Import** | mapped apply error inline; workspace list error with **Retry** | no default workspace: nothing pre-chosen | "Added as new. Existing agents are never changed."; workspace select; **Back**, **Import** (busy state while applying) |
| Import · Result | chat action shows a small busy placeholder while the new agent's type is looked up; the rest of the result is shown at once | — | type lookup failed: result kept, chat action replaced by "Open it from the Team." | activation pending: activation message shown in a notice, no chat action | "<final name> was added to <workspace>." warnings, secrets, **Open Team**, **Open chat** (only when the lookup says Main and activation is active), **Import another**; after a same-tab reload: the stored summary under "Import finished" |
| Import · Outcome unknown | — | — | — | — | "The connection dropped before Omnipus answered. The import may have finished: check the workspace Team before importing again." **Open Team** (chosen workspace), **Start over** (clears the tab record; never re-sends the apply) |
| Import · Uncertain (after a cancel answer that proves nothing) | — | — | — | — | "Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again." **Open Team** (recorded workspace) or the hint "check the Team of the workspace you chose" when there is no record; **Start over** (clears the tab record; sends nothing) |
| Quick Export button (agent edit sheet) | — | not rendered for built-in agents, or when the identity mode is not `local` | — | — | outline button **Export** in the header row |
| Stepper (both wizards) | — | — | — | — | ordered step labels; current step marked; "Step n of m" for screen readers |

## User Journey

**Export.** Sidebar → **Export & Import** (US-1.1, 1.2) → **Start export** → Choose agent (US-2.1, 2.2, 2.5) → **Review** (US-2.3, 2.6; server review US-6.1) → **Download** (US-2.4) → **Done** returns to the landing. Shortcut: agent edit sheet → **Export** (US-3.1) lands on Choose with the agent ticked. Reload/expiry: US-2.7, 2.8.

**Import.** Sidebar → **Export & Import** → **Start import** → Choose file (US-4.1, US-5.1–5.3) → Preview (US-4.1, 4.2) → **Continue** → Confirm (US-4.3, 4.6) → **Import** → Result (US-4.4, 4.5; failures US-5.4, 5.5, 5.7, 5.8). **Cancel** on Preview or Confirm discards the upload (US-7.1, 7.3) and returns to the landing. Reloads: US-5.6, 5.9, 5.10.

Back navigation: Review → Choose keeps the chosen agent; Download → Review; Confirm → Preview; Preview **Cancel** → landing; Result never undoes.

**Address (URL) state** — validated search parameters of `/export-import`:

| Parameter | Values (B1) | Unknown or invalid value |
|---|---|---|
| `wizard` | `export`, `import` | dropped → landing |
| `step` | export: `choose`, `review`, `download`; import: `file`, `preview`, `confirm`, `result` | dropped → first step of the wizard |
| `scope` | `agents` | dropped → `agents` |
| `preselect` | `agent:<id>` | other types or malformed → ignored with notice (US-3.3) |
| `plan_id` | string | → Choose with expired notice when the server does not hold it |
| `staging_id` | string | on Preview/Confirm after a reload: decided by the per-tab import record and the cancel's answer ("Reload mid-import"); never treated as proof of anything by itself |

A password or bundle content is never placed in the address or browser storage. B1 adds `export_id` and `import_id` to nothing (they arrive in B2).

## Accessibility and Keyboard

- Stepper is an ordered list; the current step has `aria-current="step"`; a visually hidden "Step n of m" text accompanies it.
- On every step change, focus moves to that step's heading (a heading focusable by script only), so screen readers announce the new step.
- Busy states ("Preparing the review…", "Checking the bundle…", "Creating the bundle…", applying) are announced in a polite live region; errors use `role="alert"`.
- Agent choice is a radio group: arrow keys move, Space selects; the filter field is above the group and does not trap focus.
- The file chooser is a catalogued **Button** that opens a visually hidden file input; Enter and Space open the system picker; the chosen file name is announced.
- All actions reachable by Tab in reading order; **Back** precedes the primary action; Escape does not leave the wizard (no dialog).
- The one focus ring rule applies (no per-component `focus-visible:*` utilities).
- Narrow screens (≤ 640 px wide): single column, tables become stacked label/value rows, touch targets meet the design-system size rule (rule 12).
- No emoji in any label, notice or message (brand guidelines). Icons are Phosphor icons with `aria-hidden` next to text.

## Design-System Components

Catalog checked first (`design-system/catalog.json`, 70 entries).

| Need | Component |
|---|---|
| Page title | `page-header` |
| Landing entries | `card` + `Button` |
| Step indicator | **new shared composite `Stepper`** (see below) |
| Agent choice | `radio-group` (+ `search-field` for the filter) |
| Files table, preview lists | `table`, `list-group` |
| Notices (free text, expiry, pending activation) | `callout` |
| Errors | `error-state` / `query-error-state` (lists), `FormError` (inline) |
| Empty states | `empty` / `collection-state` |
| Workspace choice | `select` |
| Busy | `spinner`, `skeleton` |
| Kind labels | `badge` |
| Quick Export | `Button` (outline, small) |

**Stepper — new composite.** No catalogued component shows wizard steps, and the onboarding wizard already hand-builds one (`src/routes/onboarding.tsx`, "Step {step} of {totalSteps}"), so with two more wizards this is a recurring job (design-system rule 14). Build `src/components/ui/stepper.tsx` as a **composite**, presentational only (labels, current index; no navigation logic), published through the four-part contract (rule 9: `src/index.ts` export, `catalog.json` entry, `@source` line in `src/styles/library.css`, `design-system/manifests/stepper.json` with all eleven verification kinds) plus a story. Migrating onboarding to it is **out of scope** (record as a follow-up).

The file input is the one raw element (`<input type="file">`, hidden), matching `SkillBrowser.tsx`; it is not a control the lock script bans.

## Security and User Promises

- New routes reuse the existing package guard: local mode only, `withAuth`, developer bypass refused, with negative auth tests per route (ADR D-E3.1). This touches gateway auth wiring → **security-lead on-demand review**.
- The review never opens `credentials.json`, `master.key`, `cli.token`, `auth.json` or `config.json`; it uses the same store edge as export (S1a FR-013). **Proof is two separate instruments:** (a) a no-open test around the review create, read and refusal routes using the existing inotify open watcher, whose positive control (a deliberate `auth.json` open) must be detected — this runs on the Linux CI leg only; on macOS and Windows the watcher is a stub and asserts nothing, so the no-open promise is certified on Linux and relied on by shared code elsewhere; (b) output scans of review bodies, errors and logs, and of the downloaded bundle's decoded payload **and** manifest, for the D6 canaries. Removed values never appear in the review answer — names only.
- Guaranteed never included (shown on the Review as fixed text, true for B1's agent carriers): "Never included: passwords, API keys and tokens stored in Omnipus, provider keys, channel tokens, this install's settings file, and an external worker's program path, environment values and extra arguments."
- Not guaranteed, disclosed: free text (prompt, instructions) — the `free_text_copied` notice on every Review, ADR D-E4 layer 3.
- Import keeps S1a's all-or-nothing guarantee (ADR D-E5.1 single-agent row); B1 adds no write path.
- Export stays audited exactly as today; the review is not audited (it releases no bytes); the cancel is not audited (no data enters or leaves).
- No automatic resend of upload, apply, cancel or export (see "No automatic resend"); an ambiguous import outcome is never presented as "nothing added" or as "finished".
- No agent tool is added (table in the header); `docs/security.md` and `docs/tools.md` need no change.

---

## Behavioral Contract

Primary flows:
- When a signed-in person on the self-hosted edition opens the sidebar, the system shows an Export & Import entry that opens the Export & Import page.
- When the person chooses one of their own agents and asks for a review, the system shows what the bundle contains, its size, what is left out, secrets to enter again, the free-text notice and what is never included.
- When the person downloads from a current review, the system saves a bundle file named after the agent.
- When the person selects Export on a custom agent's edit sheet, the system opens the export wizard with that agent chosen.
- When the person chooses a bundle file, the system checks it and shows what would be added, what is skipped and any warnings.
- When the person confirms an import into a workspace, the system adds the agent as a new agent there and shows its final name, workspace and status; existing agents are not changed.

Error flows:
- When the chosen agent cannot be exported, the system says why and stays on the choose step.
- When a review is no longer held, the system returns to the choose step with a notice.
- When the file is not a bundle, too large, damaged or from a newer Omnipus, the system says so and stays on the file step.
- When the upload expired before confirmation, the system returns to the file step and adds nothing.
- When another import is running, the system says so and offers to try again.
- When an import fails, the system says it was undone and nothing was added, or that clean-up needs a restart.
- When no clear answer arrives after Import (lost connection, timeout, gateway or proxy error, unreadable answer), the system sends nothing again by itself, says the import may have finished and to check the Team first, and does not offer Import again for that upload.
- When the page is reloaded while this tab's record is `apply_sent` (Import sent, no authoritative answer), the system does not cancel the upload and says the import may have finished; when the record is `previewed` (Import never sent, or refused with a known answer), a reload cancels the unused upload once.
- When a cancel cannot remove the temporary files, the system still makes the upload unusable and frees its place, and attempts removal again at a later upload or a repeated cancel; the files stay if removal keeps failing.
- When a cancel and an import meet on the same upload, the system handles them one state change at a time: a cancel before the import starts discards the upload and the import is refused; a cancel while the import runs is refused and changes nothing; the import's own clean-up of its upload is unchanged.
- When the type of a newly imported agent cannot be confirmed, the system keeps the result and sends the person to the Team instead of offering a chat.

Boundary conditions:
- When the person has no agents of their own, the system shows an empty state and no review is possible.
- When no agent is chosen, the system does not allow a review.
- When a file is larger than 64 MB, the system refuses it before uploading.
- When the install has no default workspace, the system pre-chooses no workspace.
- When the edition is desktop or hosted, the system shows no entry, no Quick Export button, and the page says it is unavailable.

## Edge Cases

- Empty selection → Review unavailable, hint shown (US-2.2).
- Built-in agent → never listed, no Quick Export button; reached by address → `core_agent_not_exportable` text, nothing ticked (US-3.2, 3.3).
- Agent deleted between Review and Download → `agent_not_found` text on Download, **Back to choose**.
- Review dropped on the server (restart, eviction) while the Review stays open → the re-read before Download answers 404 → Choose with the expired notice (MIN-002).
- Reload in the same tab while the record is `apply_sent` → Outcome-unknown screen, no cancel, no apply (MAJ-002). Reload after a known refusal (record `previewed`) → one cancel, read by "What a cancel answer proves". A second tab has no record: its one cancel's answer decides, and 404, 409 or a lost answer → Uncertain screen (R2-MAJ-002).
- Apply commits but its answer is lost → one request only, Outcome-unknown screen (MAJ-001).
- Upload answer lost → one request only; at most one slot used until it expires (MAJ-001).
- Cancel races apply → handled per the checkpoint-based interleavings in "Cancel and apply at the same moment" (cancel before apply, cancel while apply is paused, normal apply completion, cancel after a preparation refusal); no exclusive-or or success-count assertion (MAJ-003, R2-MAJ-004). Cancel cannot remove the folder → `staging_cleanup_failed`, slot freed, removal attempted again at a later upload or a repeated cancel; the files stay if removal keeps failing.
- Imported agent's type lookup fails → result kept, "Open it from the Team." (MAJ-005).
- Agent edited between Review and Download → the download holds the content at download time; the Download step shows the actual size (may differ from "About N KB"). Accepted for B1 (ADR D-E3.2: content inside reviewed elements is taken as of capture).
- Huge bundle: on export, the prompt and instructions are bounded at 1 MiB each (a larger file is `agent_file_unreadable` / too large); the agent's settings record is **not** bounded by the export (inherited S1a limitation: the store reads it whole). B1 does not change this; DOC-001 states it. On import, > 64 MiB is refused in the browser, and 413 from the server if sent anyway; the server's 256 MiB unpacked limit stays.
- Wrong file (text, zip, empty) → `not_a_package` text.
- Duplicate name → preview shows the suffixed name; 99 suffixes taken → `name_suffix_exhausted` text on Confirm.
- Expired upload → File step notice (US-5.4).
- Concurrent import (two tabs) → `import_in_progress` text with **Try again** (US-5.5).
- Bundle from a newer format version → `package_too_new`; newer element schema → `element_schema_too_new`.
- Bundle with an element type this install does not know (from a later Omnipus that still writes format 1) → shown under "Skipped" (S1b behaviour).
- Five uploads already kept → `too_many_staged_imports` text naming the 30-minute expiry.
- Workspace deleted between Confirm opening and Import → `workspace_not_found` text; workspace list refreshes.
- Gateway restarted while on Review → `plan_not_found` → Choose with notice.
- Two reviews of the same agent in two tabs → two independent plans; both valid.
- 33rd concurrent review → oldest dropped; reading it gives `plan_not_found`.
- Audit log cannot be written at download → `audit_failed` text: "The export was stopped because the audit log could not be written. Nothing was downloaded."
- Agent name with characters outside letters, digits, space, dot, dash, underscore (e.g. "Écrivain ✍") → file name uses underscores for them (existing export rule); the review shows the same `file_name`.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not change the bundle format, manifest version or any existing package wire shape, because B1 must stay on today's engine (ADR D-E7).
- The system must not offer more than one agent per bundle, other scopes, Replace or Merge in B1, because the engine does not support them yet; they must not be shown as disabled teasers either ("show only what exists today").
- The system must not show workspace, skill or connector Quick Export buttons in B1, because those slices have not landed.
- The system must not place bundle bytes, review details beyond IDs, or a password in the address bar or browser storage.
- The system must not compute what is left out in the browser, because the review must be authoritative (founder requirement); the browser only displays the server's review.
- The system must not echo any removed value (program path, argument, environment value, credential) in the review, an error or a log line.
- The system must not add an agent tool for export or import (agent tool table).
- The system must not write an audit entry, a staging folder or any data-folder file for a review.
- The system must not undo or re-run an import when the Result step is reloaded.
- The system must not use emoji in labels, notices or messages.
- The system must not invent an import outcome after a lost or unreadable response; it must say the outcome is unknown.
- The system must not resend an upload, apply, cancel or export by itself (no inherited automatic retry), because a resend can duplicate an import, reserve extra upload places, or replace an unknown-outcome warning with a misleading "expired" message.
- The system must not cancel an upload while this tab's record for it is `apply_sent` (Import sent and no authoritative answer; a known refusal returns the record to `previewed`, after which the unused upload may be cancelled once), and must never delete an upload that is being received, being imported, or kept for clean-up, because that folder may hold the recovery record.
- The system must not claim "Import finished" from the address alone.
- The system must not decide whether to offer a chat from the activation text or from an invented field in the import result.
- The system must not say connector assignments are excluded from the bundle: their names travel; only their settings and secrets do not, and import does not restore them.

### Machine-Verifiable Constraints

**Error Codes / Messages** (`ErrorResponse`, `contracts/components/schemas/ErrorResponse.yaml`):

| Condition | Status | `code` |
|---|---|---|
| Review: `agent_ids` count ≠ 1 | 400 | `export_selection_invalid` |
| Review: invalid JSON, unknown property, `scope` ≠ `agents` | 400 | (none or `invalid_request` per existing jsonErr) |
| Review: JSON body > 1 MiB | 413 | — |
| Review: agent unknown or invalid ID | 404 | `agent_not_found` |
| Review: export refusal | 422 | the export's code (`core_agent_not_exportable`, `agent_prompt_missing`, `element_not_creatable`, `agent_file_unreadable`, `external_worker_not_supported_yet`) |
| Review read: unknown / expired / malformed ID | 404 | `plan_not_found` |
| Any new route without sign-in | 401 | — |
| Any new route with developer bypass on | 503 | — |
| Any new route in platform auth mode | 404 (not registered) | — |
| Wrong method on `/export/plan` (not POST) or `/export/plan/{id}` (not GET) | 405 | — |
| Cancel: unknown or used staging | 404 | `staging_not_found` |
| Cancel: staging receiving, applying or needs clean-up | 409 | `staging_busy` |
| Cancel: folder could not be removed (staging now cancelled) | 500 | `staging_cleanup_failed` |
| Cancel: method other than DELETE on `/import/{id}` (and other than POST on `/import/{id}/apply`) | 405 | — |

**UI text for codes** (exact; client map, unknown code → the server's `error` text):

| Code | Text |
|---|---|
| `not_a_package` | "This file is not an Omnipus bundle." |
| `package_too_new` | "This bundle was made by a newer Omnipus. Update this install to import it." |
| `element_schema_too_new` | "Part of this bundle was made by a newer Omnipus. Update this install to import it." |
| `package_too_large` | "This bundle is too large for this version (most is 64 MB, 256 MB unpacked)." |
| `checksum_mismatch`, `manifest_mismatch` | "This bundle is damaged: a file does not match its record. Export it again." |
| `unsafe_path`, `unsafe_entry_type` | "This bundle contains something unsafe and was refused." |
| `no_supported_element`, `element_type_not_supported` | "This bundle contains nothing this version can import." |
| `too_many_agents` | "This bundle holds more than one agent. This version imports one agent per bundle." |
| `invalid_element` | "The agent in this bundle is not valid." |
| `element_not_creatable` (import steps) | "The agent in this bundle breaks a rule for new agents" + ": <field>" when `field` is present |
| `element_not_creatable` (export steps) | "This agent breaks a rule for agents and cannot be exported" + ": <field>" when `field` is present |
| `external_worker_not_supported_yet` | "Third-party workers cannot be exported or imported yet." |
| `too_many_staged_imports` | "Too many imports are waiting. Each one expires 30 minutes after upload; finish or cancel one, then try again." |
| `staging_not_found` | "This upload expired or was already used. Choose the file again." |
| `staging_cleanup_failed` (explicit Cancel or reload) | "The upload was cancelled. Omnipus will try to remove its temporary files at a later upload." |
| cancel answer 404, 409, lost or unreadable (explicit Cancel or reload) — Uncertain screen | "Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again." |
| unknown upload answer | "Omnipus did not confirm the upload. Choose the file again. If the upload did arrive, it can no longer be used after 30 minutes, and Omnipus will try to remove its temporary files at a later upload." |
| CSRF 403, client `csrf_missing`, 403 `planted_cookie_cleared` (any package call) | "Your session needed refreshing. Nothing was imported. Select Import again." (apply); "Your session needed refreshing. Nothing was downloaded. Select Download again." (export); "Your session needed refreshing. Choose the file again." (upload); "Your session needed refreshing. Select Cancel again." (explicit cancel) |
| `import_in_progress` | "Another import is running. Try again in a moment." |
| 413 on apply (request too large; no code) | "Omnipus refused the request as too large. Nothing was imported. Select Import again." (a 413 on cancel is not a known refusal: it falls under "any other answer" in "What a cancel answer proves" → Uncertain screen) |
| `workspace_not_found` | "That workspace no longer exists. Choose another." |
| `default_workspace_missing` | "Choose a workspace to import into." |
| `name_suffix_exhausted` | "Every \"(imported N)\" name is taken. Rename one of the existing agents, then try again." |
| `import_rolled_back` | "The import failed and was undone. Nothing was added." |
| `import_rollback_incomplete` | "The import failed and could not be fully undone. Restart Omnipus to finish the clean-up." |
| `core_agent_not_exportable` | "Built-in agents are on every install and cannot be exported." |
| `agent_prompt_missing` | "This agent has no prompt, so it cannot be exported." |
| `agent_file_unreadable` | "A file of this agent could not be read safely (<file>)." + " It is over 1 MB." when `details.check` = `too_large` |
| `agent_not_found` | "This agent no longer exists." |
| `export_selection_invalid` | "Choose exactly one agent." |
| `plan_not_found` | "The review expired. Check your choice and review again." |
| `audit_failed` | "The export was stopped because the audit log could not be written. Nothing was downloaded." |
| unknown apply outcome (see "Apply outcome classes") | "The connection dropped before Omnipus answered. The import may have finished: check the workspace Team before importing again." |
| network failure on a read (agent list, workspace list, review read) | "Omnipus could not be reached. Check the connection and try again." |

**Performance Bounds**: a review of the benchmark fixture agent (prompt and instructions of 1 MiB each, a settings record of about 1 MiB; a fixture, not a maximum) MUST answer within 5 s in the handler test on CI hardware; the review MUST hold no bundle bytes after it answers (plan store holds only the response).

**Scope Boundaries**: the plan store MUST hold at most 32 plans; plan TTL MUST be exactly 30 minutes from `created_at` (held while now < `expires_at`); the browser MUST refuse files whose size is > 67,108,864 bytes before upload; exactly 67,108,864 bytes is sent.

## Integration Boundaries

### Gateway package routes (existing)

- **Data in / out**: agent ID in, bundle bytes out (export); bundle bytes in, preview out (upload); `{mode: new, workspace_id}` in, result out (apply).
- **Contract**: `PackageExportRequest`, `PackageImportPreview`, `PackageImportApplyRequest`, `PackageImportResult` (unchanged).
- **On failure**: coded `ErrorResponse`; mapped to UI text above; apply answers sorted by "Apply outcome classes"; no automatic resend.
- **Development**: real gateway in Go handler tests and Playwright; Vitest for wizard steps mocks the package API module with responses built from the generated schemas, **except** the transport tests (19 and the replay rows of 26), which run the real `request()` from `src/lib/api/http.ts` over a mocked global `fetch` so CSRF recovery and the planted-cookie toast are exercised for real.

### Review route (new)

- **Data in / out**: `PackageExportPlanRequest` in, `PackageExportPlan` out.
- **Contract**: new schema files above.
- **On failure**: coded errors table.
- **Development**: real store edge (`packageExportSource`) over a temp home in Go tests.

### Agent and workspace lists

- **Data in / out**: `GET /api/v1/agents` (name, type, locked, description), `GET /api/v1/workspaces` (`is_default`, status), `GET /api/v1/agents/{id}` (type of the imported agent on Result).
- **On failure**: list failures show a query error state with **Retry**; the wizard cannot proceed without them. A failed single-agent lookup on Result never hides the result; only the chat action is replaced.
- **Development**: mocked in Vitest; real in Playwright.

### Browser download and file picker

- **Data in / out**: a Blob saved under the server's `Content-Disposition` name; a `File` read from the picker.
- **On failure**: a download the browser blocks is outside the app's knowledge; the Download step still shows the file name and offers **Download again**.
- **Development**: Playwright `download` event and `setInputFiles`.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | Download is not bound to the review (no `plan_digest` until B2); content may change between Review and Download. | Bind it anyway by adding `plan_id` to the export request. | Resolved: do **not** bind in B1 (ADR keeps export unchanged); show actual size after download. Residual risk accepted for one agent. |
| 2 | Cancel route pulled forward from B2. | Skip it and let slots expire. | Resolved: Q1 = A (chief); lifecycle per "Cancel lifecycle". |
| 3 | Desktop edition (platform sign-in) has no package routes today, so B1 hides the page there too. | Enable it for desktop. | Resolved: Q2 = A (chief); desktop is a separate founder question. |
| 7 | What the browser can know after Import when no clear answer arrives. | Assume nothing was added, or retry. | Resolved: Unknown outcome class; never resend; never claim "nothing added" or "finished" without evidence (MAJ-001/002). |
| 8 | How the Result knows Main from worker. | Read activation text, or add a field to the package result. | Resolved: existing agent lookup by `new_id` (MAJ-005); no wire change. |
| 4 | Agents with identical names in the Choose list. | Show only names. | Resolved: show description and kind; when two names are equal, also show the first 8 characters of the agent ID. |
| 5 | Where Quick Export sits in the edit sheet. | Add it to a tab. | Resolved: header badge row, right end, before the description text. |
| 6 | Whether the scope step and mode step appear with a single option. | Show them with one option. | Resolved by founder rule "show only what exists today": hidden in B1; they appear when B2 / B8 add choices. |

---

## BDD Scenarios

### Feature: Sidebar entry and page

#### Scenario: Sidebar shows the entry on the self-hosted edition
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path
- **Given** the last `/state` answer said `identity.mode` is `local`
- **When** the sidebar renders
- **Then** the Assets section lists "Skills & Tools", "Connectors", "Export & Import", then the "Library" button, in that order

#### Scenario: Entry opens the page
**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path
- **Given** the sidebar is open on the self-hosted edition
- **When** the person selects "Export & Import"
- **Then** the address is `/export-import`
- **And** the page shows the heading "Export & Import", "Start export" and "Start import"
- **And** the entry has the current-page state

#### Scenario Outline: Entry hidden unless the edition is known to be local
**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Edge Case
- **Given** the remembered identity mode is `<mode>`
- **When** the sidebar renders
- **Then** the "Export & Import" entry is `<shown>`

| mode | shown |
|---|---|
| local | shown |
| platform | absent |
| null (unknown) | absent |

#### Scenario: Page unavailable in the platform edition
**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Error Path
- **Given** the identity mode is `platform`
- **When** the person opens `/export-import` directly
- **Then** the page says "Export & Import is not available in this edition."
- **And** no request to `/api/v1/packages/` is made

### Feature: Export wizard

#### Scenario: Choose lists only the person's own agents
**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path
- **Given** agents Mia (core, locked), Planner (Subagent, locked), Judge (system), "Writer" (Main), "Helper" (Subagent, not locked) and "Coder" (subagent_3p)
- **When** the export Choose step renders
- **Then** it lists Coder, Helper, Writer (sorted by name) with labels External worker, Worker, Main
- **And** it shows "A bundle holds one agent in this version."
- **And** choosing one agent un-chooses any other

#### Scenario: Review needs a choice
**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Edge Case
- **Given** the Choose step with no agent chosen
- **When** the person looks at the actions
- **Then** "Review" is disabled and the hint "Choose an agent to continue." is shown

#### Scenario: Review shows the server's review
**Traces to**: User Story 2, Acceptance Scenario 3; User Story 6, Acceptance Scenario 1
**Category**: Happy Path
- **Given** "Writer" (Main) with SOUL.md 2,000 bytes, AGENT.md 500 bytes, skills `research` and `Summarize`, connector `github`
- **When** the person chooses Writer and selects "Review"
- **Then** the review lists Settings (agent.json), Prompt (SOUL.md, 2,000 bytes shown as "2.0 KB"), Instructions (AGENT.md, 500 bytes shown as "500 B")
- **And** it shows "About" followed by the plan's `size_bytes`
- **And** "Left out" lists, in order, the memory, chats, workspace files, team, skill (items research, Summarize) and connector (item github) rows with their exact messages
- **And** the free-text notice and the "Never included" statement are shown
- **And** the address holds `step=review` and `plan_id=<id>`

#### Scenario: Download saves the bundle
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path
- **Given** a current review of "Writer"
- **When** the person selects "Download"
- **Then** the browser receives a file named `Writer.omnpkg`
- **And** the step shows "Saved Writer.omnpkg (<actual size>)."

#### Scenario: No own agents
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case
- **Given** only built-in agents exist
- **When** the Choose step renders
- **Then** it shows "You have no agents of your own yet. Built-in agents are on every install and are not exported."
- **And** no "Review" action is available

#### Scenario Outline: Export refusals at review time
**Traces to**: User Story 2, Acceptance Scenario 6; User Story 6, Acceptance Scenario 2
**Category**: Error Path
- **Given** the chosen agent has `<condition>`
- **When** the person selects "Review"
- **Then** the Choose step shows `<text>` and stays on Choose

| condition | text |
|---|---|
| blank SOUL.md | This agent has no prompt, so it cannot be exported. |
| SOUL.md of 1,048,577 bytes | A file of this agent could not be read safely (SOUL.md). It is over 1 MB. |
| SOUL.md is a link | A file of this agent could not be read safely (SOUL.md). |
| a worker with an empty description | This agent breaks a rule for agents and cannot be exported: description |

#### Scenario: Review survives a reload
**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Alternate Path
- **Given** the Review step of plan P, created 10 minutes ago
- **When** the page is reloaded
- **Then** the Review step shows plan P's content, read from the server, not rebuilt

#### Scenario Outline: Expired review
**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Error Path
- **Given** the Review step of plan P and `<event>`
- **When** `<action>`
- **Then** the Choose step opens with "The review expired. Check your choice and review again."
- **And** the agent is ticked only when `preselect` is in the address

| event | action |
|---|---|
| 31 minutes have passed | the page is reloaded |
| the gateway restarted | the page is reloaded |
| the gateway restarted while the Review stayed open | the person selects "Download" (the review re-read answers 404; no export request is sent) |
| the review was evicted (33rd review elsewhere) while the Review stayed open | the person selects "Download" |
| `expires_at` has passed in the browser clock | the person selects "Download" (no request sent) |

Stated limit: the review re-read and the export are two requests, and the export does not know the review; a review can expire between them. The download then still holds the agent as it is at that moment — the same content rule as any download.

#### Scenario: Agent deleted before download
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path
- **Given** a current review of "Writer" and Writer is then deleted
- **When** the person selects "Download"
- **Then** the step shows "This agent no longer exists." and "Back to review"
- **And** no file is saved

#### Scenario: Audit failure stops the download
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path
- **Given** the export answers 500 `audit_failed`
- **When** the person selects "Download"
- **Then** the step shows "The export was stopped because the audit log could not be written. Nothing was downloaded."

### Feature: Quick Export

#### Scenario: Quick Export opens the wizard with the agent chosen
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path
- **Given** the edit sheet of custom agent "Writer" (id W)
- **When** the person selects "Export"
- **Then** the sheet closes
- **And** the address is `/export-import?wizard=export&step=choose&scope=agents&preselect=agent:W`
- **And** "Writer" is chosen and "Review" is enabled

#### Scenario Outline: No Quick Export for built-in agents
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Edge Case
- **Given** the identity mode is `<mode>` and the edit sheet of an agent with type `<type>` and locked `<locked>`
- **When** the sheet header renders
- **Then** the "Export" button is `<shown>`

| mode | type | locked | shown |
|---|---|---|---|
| local | Main | false | shown |
| local | Subagent | false | shown |
| local | subagent_3p | false | shown |
| local | core | true | absent |
| local | Subagent | true | absent |
| local | system | false | absent |
| platform | Main | false | absent |
| null (unknown) | Main | false | absent |

#### Scenario Outline: Unusable preselect
**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Error Path
- **Given** the address `preselect=<value>`
- **When** the Choose step renders
- **Then** nothing is chosen and the notice `<notice>` is shown

| value | notice |
|---|---|
| agent:does-not-exist | The agent chosen from its page no longer exists. |
| agent:mia | Built-in agents are on every install and cannot be exported. |
| skill:research | This kind of item cannot be exported yet. |
| agent: (empty id) | This kind of item cannot be exported yet. |

### Feature: Import wizard

#### Scenario: Preview after a valid bundle
**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path
- **Given** the Import File step and a valid bundle of agent "Writer" on an install without a "Writer"
- **When** the person chooses the file
- **Then** "Checking the bundle…" is announced
- **And** the Preview shows "Writer" under "Will be added" with final name "Writer"
- **And** it shows "Can be imported until <expires_at in local time>"
- **And** the address holds `step=preview` and `staging_id=<id>`

#### Scenario: Preview of a name clash
**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** an install with an agent "Writer"
- **When** the person chooses a bundle of "Writer"
- **Then** the Preview shows "Writer → Writer (imported)" with "renamed: an agent with this name exists; it is not changed"

#### Scenario: Preview warnings and secrets
**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Alternate Path
- **Given** a bundle whose preview carries warnings `skill_dropped` (items x), `mcp_binding_dropped` (items github) and needs_secrets `API_TOKEN`
- **When** the Preview renders
- **Then** each warning shows the server's message and its items
- **And** "Enter again after import" lists API_TOKEN

#### Scenario: Confirm states the mode and pre-chooses the default workspace
**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Happy Path
- **Given** active workspaces "General" (default) and "Marketing"
- **When** the person selects "Continue" on the Preview
- **Then** the Confirm step shows "Added as new. Existing agents are never changed."
- **And** the workspace choice shows "General" chosen

#### Scenario: Import succeeds
**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Happy Path
- **Given** the Confirm step with "Marketing" chosen
- **When** the person selects "Import"
- **Then** the apply request carries mode `new` and the Marketing workspace ID
- **And** the Result shows "Writer (imported) was added to Marketing."
- **And** "Open Team" links to Marketing's Team panel

#### Scenario Outline: Chat action decided by the agent lookup
**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** an apply answer that is valid against the unchanged `PackageImportResult` (created type `agent`, `new_id` N, activation `<activation>`)
- **And** `GET /api/v1/agents/N` answers `<lookup>`
- **When** the Result renders
- **Then** the chat action is `<chat>`
- **And** the rest of the result (name, workspace, warnings, secrets, Open Team) is shown in every row

| activation | lookup | chat |
|---|---|---|
| active | 200, type Main | "Open chat" opens the agent's main chat in that workspace, as its sidebar row does |
| active | 200, type Subagent | absent |
| active | 200, type subagent_3p | absent |
| pending | (not called) | absent |
| active | still loading | busy placeholder, no link yet |
| active | 404 or network failure | "Open it from the Team." in place of the action |

#### Scenario: Pending activation
**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** the apply answer has activation `pending` and an activation message
- **When** the Result renders
- **Then** the activation message is shown in a notice and "Open chat" is absent

#### Scenario: Lists refresh after import
**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Happy Path
- **Given** a successful apply
- **When** the Result renders
- **Then** the Result effect calls the existing `invalidateAfterAgentCreate` helper once (the server's `agent_created` frame may refresh the same lists again; that is allowed and not deduplicated)

#### Scenario: No default workspace
**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Edge Case
- **Given** active workspaces without `is_default`
- **When** the Confirm step opens
- **Then** no workspace is chosen and "Import" is disabled until one is chosen

#### Scenario: No active workspace
**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Edge Case
- **Given** no active workspace
- **When** the Confirm step opens
- **Then** it shows "Create a workspace first." and no "Import" action

### Feature: Problems and recovery

#### Scenario Outline: Upload refusals
**Traces to**: User Story 5, Acceptance Scenarios 1, 2
**Category**: Error Path
- **Given** the Import File step
- **When** the person chooses `<file>`
- **Then** the step shows `<text>` and stays on the File step

| file | text |
|---|---|
| notes.txt | This file is not an Omnipus bundle. |
| empty.omnpkg (0 bytes) | This file is not an Omnipus bundle. |
| bundle with format_version 2 | This bundle was made by a newer Omnipus. Update this install to import it. |
| bundle with agent schema 2 | Part of this bundle was made by a newer Omnipus. Update this install to import it. |
| bundle with one changed byte in SOUL.md | This bundle is damaged: a file does not match its record. Export it again. |
| bundle with two agents | This bundle holds more than one agent. This version imports one agent per bundle. |
| sixth upload while five are kept | Too many imports are waiting. Each one expires 30 minutes after upload; finish or cancel one, then try again. |

#### Scenario Outline: Size check before upload
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case
- **Given** the Import File step
- **When** the person chooses a file of `<bytes>` bytes
- **Then** the upload is `<sent>`

| bytes | sent |
|---|---|
| 67,108,864 | sent |
| 67,108,865 | not sent; text "This bundle is too large for this version (most is 64 MB, 256 MB unpacked)." |

#### Scenario: Expired upload at confirm
**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Error Path
- **Given** the Confirm step and the apply answers 404 `staging_not_found`
- **When** the person selects "Import"
- **Then** the File step opens with "This upload expired or was already used. Choose the file again."
- **And** no agent was added

#### Scenario: Another import is running
**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Error Path
- **Given** the apply answers 409 `import_in_progress`
- **When** the person selects "Import"
- **Then** the Confirm step shows "Another import is running. Try again in a moment." and "Try again"
- **And** "Try again" sends the same apply once more

#### Scenario Outline: Other apply failures
**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Error Path
- **Given** the apply answers `<status>` `<code>`
- **When** the person selects "Import"
- **Then** the Confirm step shows `<text>`

| status | code | text |
|---|---|---|
| 500 | import_rolled_back | The import failed and was undone. Nothing was added. |
| 500 | import_rollback_incomplete | The import failed and could not be fully undone. Restart Omnipus to finish the clean-up. |
| 409 | name_suffix_exhausted | Every "(imported N)" name is taken. Rename one of the existing agents, then try again. |
| 404 | workspace_not_found | That workspace no longer exists. Choose another. |
| 422 | element_not_creatable (field model.primary) | The agent in this bundle breaks a rule for new agents: model.primary |

#### Scenario Outline: Unknown apply outcome, never resent
**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Error Path
- **Given** the app runs with its production query defaults and the apply answer is `<answer>`
- **When** the person selects "Import"
- **Then** exactly one apply request was sent
- **And** the Outcome-unknown screen shows "The connection dropped before Omnipus answered. The import may have finished: check the workspace Team before importing again." with "Open Team" for the chosen workspace
- **And** "Import" is not offered again for this upload and no cancel is sent for it

| answer |
|---|
| the server commits the import, then the connection is cut before the answer arrives |
| network error before any answer |
| 502 from a proxy, HTML body |
| 504 from a proxy, empty body |
| 503 without a package code |
| 500 with body `{"error":"internal server error"}` (no code) |
| 200 with a truncated JSON body |
| 200 with a body that fails `PackageImportResult` schema parsing |

#### Scenario: Unknown upload answer is not resent
**Traces to**: User Story 5, Acceptance Scenario 11
**Category**: Error Path
- **Given** the app runs with its production query defaults and the upload's connection is cut after the server accepted it
- **When** the person chooses a file
- **Then** exactly one upload request was sent
- **And** the File step shows "Omnipus did not confirm the upload. Choose the file again. If the upload did arrive, it can no longer be used after 30 minutes, and Omnipus will try to remove its temporary files at a later upload."

#### Scenario: Try again only after a proven pre-write refusal
**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** the first apply answered 409 `import_in_progress`
- **When** the answer is handled
- **Then** the tab record's phase is back to `previewed` and no request is sent until the person acts
- **When** the person selects "Try again"
- **Then** exactly one second apply request is sent with the same body, and the tab record's phase is `apply_sent` before it leaves

#### Scenario Outline: Record after a known refusal
**Traces to**: User Story 5, Acceptance Scenario 12
**Category**: Alternate Path
- **Given** the Confirm step with staging S and the apply answers `<answer>`
- **When** the person then does `<action>`
- **Then** `<outcome>`

| answer | action | outcome |
|---|---|---|
| 404 workspace_not_found | chooses another workspace W2 and selects Import | record `previewed` after the refusal; exactly one new apply with `workspace_id` W2; record `apply_sent` before it leaves |
| 409 import_in_progress | reloads the page | exactly one cancel for S; File step with the reload notice; no apply |
| 409 name_suffix_exhausted | navigates to another page | exactly one silent cancel for S; no apply |
| 422 element_not_creatable | reloads the page | exactly one cancel for S; no apply |
| 413 (apply body too large) | selects Import | record `previewed` after the refusal; exactly one new apply |
| 500 import_rolled_back | selects Start over | record cleared; File step; no cancel, no apply |
| 404 staging_not_found | (none) | record cleared; File step with the expired-or-used text; no cancel |

#### Scenario Outline: Session refresh on apply is not replayed
**Traces to**: User Story 5, Acceptance Scenario 12
**Category**: Error Path
- **Given** the real browser transport (`request()` over a mocked `fetch`, requests counted by URL and method) and the session condition `<condition>`
- **When** the person selects "Import"
- **Then** `<first>` requests `POST /api/v1/packages/import/S/apply` reach `fetch`
- **And** exactly one recovery read `GET /api/v1/state` is sent (counted separately)
- **And** no toast with a Retry action is created
- **And** the Confirm step shows "Your session needed refreshing. Nothing was imported. Select Import again." and the record phase is `previewed`
- **When** the person selects "Import" again (session now valid)
- **Then** exactly one more apply request reaches `fetch`

| condition | first |
|---|---|
| server answers 403 with the CSRF-failure body ("csrf token mismatch") | 1 |
| server answers 403 `planted_cookie_cleared` | 1 |
| no CSRF cookie in the browser (client `csrf_missing`; the real gate, no faked answer) | 0 |

#### Scenario: Session refresh refusals happen before the import starts (server)
**Traces to**: User Story 5, Acceptance Scenario 12
**Category**: Error Path
- **Given** ready staging S and an apply request that has a mismatched CSRF token, or carries a duplicated reserved cookie name that the planted-cookie guard refuses
- **When** it is sent through the registered route
- **Then** the answer is 403 and no agent was added
- **And** S is still `ready`: a following valid apply on S answers 200

#### Scenario Outline: Reload mid-import
**Traces to**: User Story 5, Acceptance Scenarios 6, 9, 10
**Category**: Alternate Path
- **Given** the import wizard address with `step=<step>` and `staging_id` S, the tab record `<record>`, and the server state `<server>`
- **When** the page is reloaded
- **Then** `<outcome>`
- **And** no apply request is sent

| step | record | server | outcome |
|---|---|---|---|
| preview | phase `previewed`, S | S ready | one cancel for S → 204; File step with "The page was reloaded. Choose the file again."; record cleared |
| confirm | phase `previewed`, S | S ready | same as above |
| confirm | phase `apply_sent`, S | S applying (apply still running) | no cancel sent; Outcome-unknown screen with "Open Team" for the recorded workspace |
| confirm | phase `apply_sent`, S | S done (committed, answer lost) | no cancel sent; Outcome-unknown screen |
| confirm | none (new tab opened with this address) | S ready | one cancel → 204; File step with the reload notice |
| confirm | none | S ready, folder removal fails | one cancel → 500 `staging_cleanup_failed`; File step with the reload notice and "The upload was cancelled. Omnipus will try to remove its temporary files at a later upload."; record cleared |
| confirm | none | S applying | one cancel → 409 `staging_busy`; Uncertain screen without a workspace link ("check the Team of the workspace you chose") |
| confirm | none | S done (another tab imported it) or unknown | one cancel → 404; Uncertain screen without a workspace link |
| confirm | phase `previewed`, S | S done (another tab imported it) | one cancel → 404; Uncertain screen with "Open Team" for the recorded workspace |
| confirm | none or `previewed` | cancel answer lost or unreadable | Uncertain screen; no second cancel |
| result | phase `done` with result summary | any | the stored summary under "Import finished"; no request to `/packages/` |
| result | none | any | landing; no completion claim; no request to `/packages/` |

### Feature: Review call (server)

#### Scenario Outline: Review contents per agent kind
**Traces to**: User Story 6, Acceptance Scenarios 1, 5
**Category**: Happy Path
- **Given** the stored agent `<agent>` (Dataset D1)
- **When** an authenticated caller sends `POST /api/v1/packages/export/plan` with scope `agents` and its ID
- **Then** the answer is 201 with `kind` `<kind>`, file paths `<files>`, `left_out` rows `<left_out>`, `needs_secrets` `<secrets>`
- **And** `disclosures` is exactly `[free_text_copied]` with the exact message
- **And** `expires_at` − `created_at` = 30 minutes

| agent | kind | files | left_out (type/reason, in order) | secrets |
|---|---|---|---|---|
| D1-1 Main with AGENT.md, no skills/connectors | main | agent.json, SOUL.md, AGENT.md | agent_memory, session, work_files, team | none |
| D1-2 Main without AGENT.md, skills [research, Summarize], connector [github] | main | agent.json, SOUL.md | agent_memory, session, work_files, team, skill [research, Summarize], connector [github] | none |
| D1-3 native worker | worker | agent.json, SOUL.md | agent_memory, session, work_files, team | none |
| D1-4 external worker with cli_path, cli_args (a string), env_overrides {API_TOKEN, REGION}, seeded through the typed agent store | external_worker | agent.json (+ AGENT.md if present; never SOUL.md) | agent_memory, session, work_files, team, agent_setting/host_path, agent_setting/secret_value [API_TOKEN, REGION], agent_setting/secret_value | API_TOKEN, REGION (kind executor_env) |

#### Scenario: Review size matches the export
**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a review of D1-1 with `size_bytes` N
- **When** the same agent is exported unchanged
- **Then** the export body length differs from N by at most 256 bytes
- **And** the export's file name equals the review's `file_name`

#### Scenario: Review wording matches the real bundle
**Traces to**: User Story 6, Acceptance Scenario 1; User Story 2, Acceptance Scenario 3
**Category**: Happy Path
- **Given** D1-2 (skills research and Summarize, connector github) and its review
- **When** the agent is exported and the bundle is decoded independently (zstd → tar → `agent.json`)
- **Then** the decoded settings record still lists skills research, Summarize and connector github — matching the review's "names travel" wording
- **And** the decoded payload contains no connector definition (no `command`, `url`, `env`, `headers` of github)
- **And** after importing that bundle, `GET /api/v1/agents/{new_id}` shows no connector assignment, and the result carries warning `mcp_binding_dropped` with item github

#### Scenario: Review read-back and expiry
**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** a review P created at time T (fake clock)
- **When** `GET /api/v1/packages/export/plan/P` is sent at `<time>`
- **Then** the answer is `<result>`

| time | result |
|---|---|
| T + 29 min 59 s | 200, body equal to the create answer |
| T + 30 min | 404 plan_not_found |
| after a gateway restart | 404 plan_not_found |
| any time, ID `not-a-uuid` | 404 plan_not_found |

#### Scenario Outline: Plan store cap
**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Edge Case
- **Given** a fake clock and reviews created at `<created>`
- **When** a 33rd review P33 is created at `<when>`
- **Then** `<gone>` give 404 plan_not_found and `<kept>` give 200

| created | when | gone | kept |
|---|---|---|---|
| P*n* at T + *n* s for n = 1…32 (distinct times, none expired) | T + 1 min | P1 (uniquely oldest, evicted) | P2…P33 |
| P1 at T; P2…P32 at T + 10 min | T + 30:00 (P1 expired) | P1 | P2…P33 |
| P1…P3 at T; P4…P32 at T + 10 min | T + 30:00 (P1…P3 expired) | P1…P3 | P4…P33 |

#### Scenario: Review changes nothing
**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Happy Path
- **Given** a snapshot of every file under the test home (paths, sizes, hashes) and the audit log length
- **When** a review is created and read back
- **Then** the snapshot and the audit log length are unchanged and no staging slot is used

#### Scenario: Review never carries removed values
**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Error Path
- **Given** D1-4 whose cli_path, cli_args and env override values, plus the credential store, master key, CLI token, a provider key and a channel token in the home, contain canary strings (Dataset D6)
- **When** its review is created, read back, and its refusal variants are triggered
- **Then** no D6-1…D6-9 canary appears in any response body, header or log line captured during the test
- **And** the allowed names API_TOKEN and REGION do appear (names are not values)
- **And** a control run that plants D6-9's value into a review body through a test seam makes this scanner fail

#### Scenario: Review never opens secret files (Linux)
**Traces to**: User Story 6, Acceptance Scenarios 4, 5
**Category**: Error Path
- **Given** the open watcher on `credentials.json`, `master.key`, `cli.token`, `auth.json` and `config.json` of the test home, and a deliberate open of `auth.json` that the watcher reports (positive control)
- **When** a review is created for D1-1 and D1-4, read back, and refused for D1-7 and `mia`
- **Then** the watcher reports no open of any watched file during those requests
- **And** a control run with a test seam that opens `credentials.json` inside the review handler (output unchanged) makes the test fail
- **And** on macOS and Windows the test runs the output assertions only and states that the no-open half is not asserted there

#### Scenario: Downloaded bundle carries no secret, but does carry free text
**Traces to**: User Story 2, Acceptance Scenarios 3, 4; User Story 6, Acceptance Scenario 5
**Category**: Error Path
- **Given** D1-4 plus a Main agent whose SOUL.md contains `CANARY_PROMPT` and AGENT.md contains `CANARY_INSTRUCTIONS`, and the D6 secrets in the home
- **When** both agents are reviewed and then exported through the B1 flow, and each bundle is decoded (payload and `manifest.json`)
- **Then** no D6-1…D6-9 canary appears in any decoded payload entry or in the manifest
- **And** `CANARY_PROMPT` and `CANARY_INSTRUCTIONS` appear in the decoded payload (positive controls) and in neither review answer
- **And** the manifest lists API_TOKEN and REGION as `needs_secrets` names only

#### Scenario Outline: Review request validation
**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Error Path
- **Given** an authenticated caller
- **When** they send `<request>`
- **Then** the answer is `<status>` `<code>`

| request | status | code |
|---|---|---|
| agent_ids [] | 400 | export_selection_invalid |
| agent_ids [A, B] | 400 | export_selection_invalid |
| scope "everything" | 400 | — |
| unknown property "roots" | 400 | — |
| body not JSON | 400 | — |
| body 1 MiB + 1 byte | 413 | — |
| agent_ids ["../x"] | 404 | agent_not_found |
| agent_ids [unknown UUID] | 404 | agent_not_found |
| agent_ids ["mia"] | 422 | core_agent_not_exportable |
| GET on /export/plan | 405 | — |
| POST on /export/plan/P | 405 | — |

#### Scenario Outline: Review route guard
**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Error Path
- **Given** `<setup>`
- **When** the review create, the review read and the cancel are called
- **Then** each answers `<status>`

| setup | status |
|---|---|
| no session cookie and no bearer token | 401 |
| developer bypass mode on | 503 |
| platform auth mode (routes not registered) | 404 |

### Feature: Cancel an upload

#### Scenario: Cancel frees the slot
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path
- **Given** five ready uploads S1…S5
- **When** `DELETE /api/v1/packages/import/S1` is sent
- **Then** the answer is 204, S1's folder is gone, and a new upload answers 201

#### Scenario Outline: Cancel refusals
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Error Path
- **Given** staging S in state `<state>`
- **When** `DELETE /api/v1/packages/import/S` is sent
- **Then** the answer is `<status>` `<code>` and S's state and folder are `<after>`

| state | status | code | after |
|---|---|---|---|
| ready, past expiry, not swept | 204 | — | removed; entry gone |
| receiving (upload still streaming) | 409 | staging_busy | unchanged |
| applying | 409 | staging_busy | unchanged, journal file byte-identical |
| needs-cleanup | 409 | staging_busy | unchanged, journal file byte-identical |
| done | 404 | staging_not_found | unchanged |
| unknown ID | 404 | staging_not_found | — |

#### Scenario: Folder removal fails
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Error Path
- **Given** ready staging S whose folder cannot be removed (test seam makes the removal fail once)
- **When** `DELETE /api/v1/packages/import/S` is sent
- **Then** the answer is 500 `staging_cleanup_failed`
- **And** an apply on S answers 404 `staging_not_found`
- **And** S does not count as a slot (five new uploads answer 201)
- **And** after the removal seam is cleared, the next upload's sweep removes S's folder and S is unknown

#### Scenario: Folder left after a restart is removed by the next upload
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Edge Case
- **Given** a cancelled staging whose folder is still on disk and the gateway restarts
- **When** a bundle is uploaded after the restart (its sweep runs the orphan scan)
- **Then** the folder is removed (no journal) and no `unfinished_import_found` warning is shown

#### Scenario: A restart alone removes nothing
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Edge Case
- **Given** a cancelled staging whose folder is still on disk
- **When** the gateway restarts, boot recovery runs, and the fake clock moves 31 minutes with no upload
- **Then** the folder is still on disk and no warning is shown (pins the B1 retention promise: removal only at a later upload)

#### Scenario Outline: Cancel and apply at the same moment
**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Edge Case
- **Given** ready staging S and the test seam `<seam>`
- **When** `<sequence>`
- **Then** `<observed>`

| seam | sequence | observed |
|---|---|---|
| none | cancel, then apply on S | cancel 204; apply 404 `staging_not_found`; no agent added; S's folder gone |
| apply paused right after `BeginApply` | apply starts; at the pause a cancel is sent | cancel 409 `staging_busy`; **at that checkpoint** S is `applying`, its folder exists and its journal (if any) is byte-identical to before the cancel |
| (continues the row above) | the pause is released | the apply ends in its normal way: 200 with the agent added and S retired by `FinishApply` (folder removed by the apply, as today), or a coded rollback; the cancel had no effect on either |
| none | apply completes (200), then cancel | cancel 404 `staging_not_found` |
| none | apply refused in preparation (404 `workspace_not_found`, `AbortApply`), then cancel | cancel 204; S's folder gone; no agent added |

#### Scenario: Two uploads are imported one at a time (server)
**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Edge Case
- **Given** ready stagings S1 and S2 and the apply of S1 paused right after `BeginApply`
- **When** an apply on S2 is sent
- **Then** it answers 409 `import_in_progress` and S2 is still `ready`
- **When** S1's pause is released and S1's apply answers 200, and the apply on S2 is sent again
- **Then** it answers 200

#### Scenario Outline: Cancel from the wizard
**Traces to**: User Story 7, Acceptance Scenarios 1, 3
**Category**: Happy Path
- **Given** the Preview step with staging S
- **When** the person selects "Cancel" and the cancel answers `<answer>`
- **Then** `<shown>`
- **And** exactly one cancel request was sent

| answer | shown |
|---|---|
| 204 | landing page, no message; tab record cleared |
| 500 staging_cleanup_failed | landing page with "The upload was cancelled. Omnipus will try to remove its temporary files at a later upload."; tab record cleared |
| 404 staging_not_found (it had expired, or another tab imported it) | Uncertain screen ("Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again.") with "Open Team" for the recorded workspace and "Start over" |
| 409 staging_busy | Uncertain screen, same text and actions |
| network failure or unreadable answer | Uncertain screen, same text and actions; no second cancel |
| 403 CSRF failure | stays on Preview with "Your session needed refreshing. Select Cancel again."; no automatic second cancel |

#### Scenario: Leaving the wizard cleans up silently
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Alternate Path
- **Given** the Preview or Confirm step with tab record phase `previewed`
- **When** the person navigates to another page of the app
- **Then** one cancel for S is sent and no message is shown whatever its answer
- **And** with record phase `apply_sent` no cancel is sent

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit (Go) | `pkg/omnpkg` plan function, left-out rules, staging cancel | rules in isolation, no HTTP |
| Integration (Go) | `pkg/gateway` handlers over a temp home with real stores | wire, status codes, guards, no-write, canaries |
| Unit (Vitest) | each wizard step, Stepper, Quick Export button, Sidebar entry, route search validation, error-text map, API client | rendering and behaviour per step with mocked responses built from generated schemas |
| E2E (Playwright) | one spec: sidebar → export → download → import → result; agent sheet → Quick Export | real gateway and browser download, reachability |

### Hands-on UAT rows (written at spec time)

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| B1-UAT-01 | US-1.1, 1.2 | Sign in (self-hosted), open sidebar, select Export & Import | Entry after Connectors, before Library; page with Start export / Start import | Sidebar, route | open page from a fresh browser | yes (E2E) |
| B1-UAT-02 | US-2.1, 2.3 | Create custom agent with a prompt, skill and connector; Start export; choose it; Review | Only own agents listed; review shows files with sizes, About size, left-out rows incl. skill and connector names, free-text notice, never-included text | Export Choose/Review, plan route | compare with spec's left-out table | yes |
| B1-UAT-03 | US-2.4 | Download from the review | File `<name>.omnpkg` saved; size shown | Download | file present in downloads | yes (E2E) |
| B1-UAT-04 | US-4.1–4.5 | Start import; choose the file from row 3 on the same install; Continue; pick a non-default workspace; Import | Preview shows "(imported)" rename; result names the workspace; agent appears in that Team and sidebar without reload | Import wizard, apply | open Team of chosen workspace | yes (E2E) |
| B1-UAT-05 | US-4.4 | Import the row-3 file on a second, clean install | Added with original name; Main agent chat opens from "Open chat" and answers with the same prompt behaviour | apply, activation | chat with the agent once | no (needs second install and a model) |
| B1-UAT-06 | US-3.1, 3.2 | Open a custom agent's edit sheet → Export; open Mia's edit sheet | Wizard on Choose with the agent ticked; Mia's sheet has no Export | AgentProfile, Quick Export | both sheets | yes (E2E) |
| B1-UAT-07 | US-2.8 | Review, wait 31 min (or restart gateway), reload | Back on Choose with expired notice | plan store | restart path | partly (restart path) |
| B1-UAT-08 | US-5.1–5.3 | Import a .txt, a .zip renamed .omnpkg, a 70 MB file | Not-a-bundle text twice; size text without upload (no network request in dev tools) | Import File | dev-tools network tab | yes (Vitest + E2E row) |
| B1-UAT-09 | US-5.5 | Two tabs, two uploads of the same file, Import in both close together (smoke only) | Each tab ends in exactly one of: a result, "Another import is running" (Try again then adds "(imported 2)"), or "expired or used". Two sequential successes are a valid outcome; this row cannot prove serialisation — the existing registry test `stagingD13OneApplyAtATime` and the new route test 13b `TestPackageApply_TwoStagingsSerialised` do (13a covers only cancel and apply on the same upload) | apply concurrency | both tabs | no (timing) |
| B1-UAT-10 | US-5.6, US-7 | Upload, reload on Preview, repeat 6 times | Every reload returns to File with notice; the 6th upload still succeeds | cancel route | slot count | yes |
| B1-UAT-14 | US-5.8, 5.10 | Dev tools: throttle to offline right after selecting Import (or block the apply response); then reload | Outcome-unknown screen; network tab shows one apply request, no cancel; Team shows whether the agent was added; no second agent appears | apply outcome, reload | network tab | yes (Vitest + E2E route interception) |
| B1-UAT-11 | US-1.3, 1.4 | Desktop edition build: open sidebar, open /export-import | No entry; page says unavailable | edition gating | desktop build | no (needs desktop build) |
| B1-UAT-12 | US-2.3 | Agent whose prompt contains `sk-test-...`; Review | Free-text notice visible; review does not claim the key is removed | Review copy | read notice | no (judgement of wording) |
| B1-UAT-13 | a11y | Keyboard only: both wizards end to end; screen reader announces step headings and busy/errors | All steps reachable; focus on step heading after each step | both wizards | keyboard run | partly (axe in E2E) |

Model use: no row needs a paid model except B1-UAT-05; use the founder-consented UAT model per root `CLAUDE.md`, or skip the chat check with a scripted model.

### Test Implementation Order

Names are placeholders; qa-lead names the real tests in RED.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestPlanExport_LeftOutRulesPerKind` (`pkg/omnpkg/plan_test.go`) | Unit | Review contents per agent kind | D1-1…D1-4 left_out, files, kind, needs_secrets |
| 2 | `TestPlanExport_RefusalsEqualExport` | Unit | Export refusals at review time; Review request validation | every D1 refusal row: plan error code == Export error code |
| 3 | `TestPlanExport_SizeMatchesExport` | Unit | Review size matches the export | ≤ 256 bytes difference, same file name |
| 3a | `TestPlanExport_WordingMatchesDecodedBundle` | Unit | Review wording matches the real bundle | independent zstd/tar decode of `agent.json`; skills and connector names present, no connector definition |
| 4 | `TestPlanExport_NoValuesInPlan` | Unit | Review never carries removed values | D6-1…9 absent in marshalled plan; names present; planted-value control fails the scanner |
| 5 | `TestStagingRegistry_CancelReadyOnly` | Unit | Cancel refusals; Folder removal fails; Folder left after a restart is removed by the next upload; A restart alone removes nothing | state table incl. receiving and expired-ready; failed removal → `cancelled`, not a slot, refused by `BeginApply`, removed by the next `Sweep`; a fresh registry plus `RecoverImports` and a 31-minute fake-clock move with no `Sweep` leaves the folder; a `Sweep` then removes it |
| 5a | `TestStagingRegistry_CancelApplyInterleavings` | Unit | Cancel and apply at the same moment | registry-level: cancel then `BeginApply` (404); `BeginApply` then cancel (409, folder and journal unchanged at that point) then `FinishApply` (done, folder removed by `FinishApply`) then cancel (404); `BeginApply`, `AbortApply`, cancel (204). No exclusive-or assertion |
| 6 | `TestExportPlanHandler_CreateAndRead` (`pkg/gateway/packages_exportplan_test.go`) | Integration | Review contents per agent kind; Review read-back and expiry | 201 body vs schema, GET equality, fake-clock TTL boundary |
| 7 | `TestExportPlanHandler_RequestValidation` | Integration | Review request validation | 400/404/405/413/422 table |
| 8 | `TestExportPlanHandler_Guards` | Integration | Review route guard | 401, 503, 404 in platform mode — for create, read, cancel |
| 9 | `TestExportPlanHandler_WritesNothing` | Integration | Review changes nothing | home snapshot, audit length, staging slots |
| 9a | `TestExportPlanHandler_NoSecretFileOpens` | Integration | Review never opens secret files (Linux) | existing `newOpenWatch`; positive control (`requireProbeSeen`); planted-open control; non-Linux stub documented |
| 10 | `TestExportPlanHandler_StoreCap` | Integration | Plan store cap | fake clock; D5-6 (distinct creation times; 33rd evicts the uniquely oldest P1), D5-7 and D5-8 (expired plans dropped, no unexpired plan lost) |
| 11 | `TestExportPlanHandler_Canaries` | Integration | Review never carries removed values | bodies, headers, captured slog output |
| 11a | `TestExportPlanFlow_DecodedBundleCanaries` | Integration | Downloaded bundle carries no secret, but does carry free text | decode payload and manifest after plan → export; SOUL.md and AGENT.md positive controls; maps the inherited `exportOmitsStructuredSecrets` checks into B1 evidence |
| 12 | `TestExportPlanHandler_BenchmarkFixtureWithin5s` | Integration | (performance bound) | benchmark fixture, ≤ 5 s |
| 13 | `TestStagingCancelHandler_*` (`packages_stagingcancel_test.go`) | Integration | Cancel frees the slot; Cancel refusals; Folder removal fails; Folder left after a restart is removed by the next upload; A restart alone removes nothing | through the registered route: 204/404/405/409/500, journal byte-identical for applying and needs-cleanup, 6th upload succeeds |
| 13a | `TestStagingCancelHandler_InterleavedWithApply` | Integration | Cancel and apply at the same moment | registered routes; a test seam pauses the apply right after `BeginApply` (channel handshake, no sleeps); cancel 409 asserted with folder, journal and state compared **at the pause**; after release the apply's normal outcome is asserted (200 + agent + folder removed by `FinishApply`), then cancel 404; cancel-first row: 204, apply 404, no agent; `AbortApply` row: apply 404 `workspace_not_found`, cancel 204 |
| 13b | `TestPackageApply_TwoStagingsSerialised` | Integration | Two uploads are imported one at a time (server) | registered routes; S1's apply paused after `BeginApply`; S2's apply 409 `import_in_progress` and S2 still `ready`; release; S1 200; S2's second apply 200 |
| 13c | `TestPackageApply_SessionRefusalsBeforeHandler` | Integration | Session refresh refusals happen before the import starts (server) | full middleware chain of the registered route; CSRF-mismatch apply → 403, planted reserved-cookie apply → 403; both: no agent, staging `ready`, then a valid apply 200 |
| 14 | `TestPackageRoutes_ExistingWireUnchanged` | Integration | (regression) | export/upload/apply golden responses unchanged |
| 15 | `stepper.test.tsx` | Unit | (Stepper, all wizard scenarios) | ordered list, aria-current, "Step n of m" |
| 16 | `Sidebar.exportImport.test.tsx` | Unit | Sidebar shows the entry…; Entry hidden unless… | order, mode table |
| 17 | `-export-import.test.tsx` (route) | Unit | Page unavailable in the platform edition; URL table | search validation, platform notice, no package request |
| 18 | `packageErrorText.test.ts` | Unit | all error outlines | every code in the UI text table → exact text; unknown → server text |
| 19 | `packages.api.test.ts` | Unit | Download saves the bundle; Upload refusals; Unknown apply outcome, never resent; Unknown upload answer is not resent | client: generated schemas, CSRF header, binary upload, blob + Content-Disposition name; mutations built with the **production** `queryClient` defaults and `retry: false` per call; runs the **real** `request()` / `requestOnce()` from `http.ts` over a mocked global `fetch` (the transport is not mocked); requests are counted per package URL and method, separately from the recovery read `GET /api/v1/state`; for each of upload, apply, cancel and export: server CSRF 403, 403 `planted_cookie_cleared`, network error at dispatch and 5xx → exactly one package request and never an automatic second; client `csrf_missing` (no CSRF cookie in `document.cookie`) → **zero** package requests through the real, unchanged cookie-presence gate (the test must not fake a server `csrf_missing` answer); a deliberate action after recovery → exactly one package request (the first, for `csrf_missing`); no toast with an action is created (toast module spied); a control case without the switches shows today's CSRF re-send and the planted-cookie toast still happen for other callers; apply outcome classifier table |
| 20 | `ExportChooseStep.test.tsx` | Unit | Choose lists only…; Review needs a choice; No own agents; Unusable preselect; Export refusals at review time | filter, sort, duplicate names with ID prefix |
| 21 | `ExportReviewStep.test.tsx` | Unit | Review shows the server's review; Review survives a reload; Expired review | labels, sizes formatting, notices, plan read |
| 22 | `ExportDownloadStep.test.tsx` | Unit | Download saves the bundle; Agent deleted before download; Audit failure…; Expired review (download row) | |
| 23 | `QuickExportButton.test.tsx` | Unit | Quick Export opens…; No Quick Export for built-in agents | navigation target, sheet close callback |
| 24 | `ImportFileStep.test.tsx` | Unit | Upload refusals; Size check before upload | no request above limit |
| 25 | `ImportPreviewStep.test.tsx` | Unit | Preview after a valid bundle; Preview of a name clash; Preview warnings and secrets; Cancel from the wizard | |
| 26 | `ImportConfirmStep.test.tsx` | Unit | Confirm states the mode…; No default workspace; No active workspace; Another import is running; Try again only after a proven pre-write refusal; Record after a known refusal (Confirm rows); Session refresh on apply is not replayed; Other apply failures; Unknown apply outcome, never resent; Expired upload at confirm | apply body exactness; tab record phase `apply_sent` written before the request leaves; at most one apply request per click (zero on client `csrf_missing`), counted per "Counting requests" with the recovery read apart |
| 26a | `ImportFileStep.unknownUpload.test.tsx` | Unit | Unknown upload answer is not resent | one upload request, text |
| 27 | `ImportResultStep.test.tsx` | Unit | Import succeeds; Chat action decided by the agent lookup; Pending activation; Lists refresh after import; Reload mid-import (result rows) | responses valid against the unchanged generated schemas; no kind field invented in mocks |
| 28 | `ExportImportWizard.reload.test.tsx` | Unit | Reload mid-import (all rows); Leaving the wizard cleans up silently; Cancel from the wizard; Record after a known refusal (reload and leave rows) | per-tab record × server answer table; never an apply request; cancel count 0 or 1 |
| 29 | `tests/e2e/export-import-b1.spec.ts` | E2E | Entry opens the page; Review shows…; Download saves the bundle; Import succeeds; Preview of a name clash; Quick Export opens…; No Quick Export for built-in agents | one test: sidebar → export a seeded custom agent → real download → import that file → "(imported)" result → Team shows it → Mia sheet has no Export → custom agent sheet Export lands on Choose ticked; axe check on landing and each step (no serious/critical) |

RED order: 1–14 (Go) and 15–28 (Vitest) may be written in parallel by the one test author; 29 last. Execution: RED tests fail for the right reason on the landing branch; GREEN makes them pass; CHECK (different model from developer and test author) audits; the full suites run once at the cut (CI).

**Local execution** (one named test or one test file per run, through the slot runner; full suites stay in CI):

```bash
heavy env CGO_ENABLED=0 go test -tags goolm,stdjson -p 1 -run '^TestExportPlanHandler_CreateAndRead$' ./pkg/gateway/
heavy env CGO_ENABLED=0 go test -tags goolm,stdjson -p 1 -run '^TestStagingRegistry_CancelReadyOnly$' ./pkg/omnpkg/
heavy npx vitest run src/components/export-import/ImportConfirmStep.test.tsx
heavy npm run typecheck
heavy npx playwright test tests/e2e/export-import-b1.spec.ts
```

Before each push also: the design-system gates from `.claude/skills/omnipus-design-system/SKILL.md` §13 for the touched files (controls lock, focus rule, Stepper's four-part publication), `make verify-contracts` after regeneration, and `scripts/e2e-shards.sh check`. A fresh worktree needs the SPA embed stub before `pkg/gateway` compiles (root `CLAUDE.md`). `heavy go test` on the no-open test (9a) must run on Linux to assert anything.

### Test Datasets

#### Dataset D1: Agents for the review

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Main "Writer", SOUL.md 2,000 B, AGENT.md 500 B | happy | 201; main; 3 files; 4 fixed left_out rows | Review contents per agent kind | |
| 2 | Main, no AGENT.md, skills [research, Summarize], tools.mcp.servers [github] | happy | 201; 2 files; + skill row [research, Summarize] + connector row [github] | Review contents per agent kind | skill case kept |
| 3 | native worker with description | happy | 201; worker | Review contents per agent kind | |
| 4 | external worker, cli_path `/opt/x/CANARY_CLI_PATH/claude`, cli_args `"--token=CANARY_ARG --verbose"` (a string, as `ExecutorConfig.CLIArgs` is), env_overrides {API_TOKEN: CANARY_ENV, REGION: eu}; seeded through the typed agent store, not raw JSON | happy + security | 201; external_worker; no SOUL.md; host_path row; secret_value rows; needs_secrets API_TOKEN, REGION | Review contents per agent kind; Review never carries removed values | |
| 5 | Main, SOUL.md exactly 1,048,576 B | boundary max | 201 | Export refusals at review time | |
| 6 | Main, SOUL.md 1,048,577 B | boundary max+1 | 422 agent_file_unreadable, check too_large | Export refusals at review time | |
| 7 | Main, SOUL.md empty | boundary zero | 422 agent_prompt_missing | Export refusals at review time | |
| 8 | Main, SOUL.md only whitespace and newlines | edge | 422 agent_prompt_missing | Export refusals at review time | |
| 9 | Main, SOUL.md symlink | security | 422 agent_file_unreadable, check link | Export refusals at review time | |
| 10 | worker, empty description | error | 422 element_not_creatable, field description | Export refusals at review time | |
| 11 | `mia` | built-in | 422 core_agent_not_exportable | Review request validation | |
| 12 | `planner` (locked Subagent) | built-in | 422 core_agent_not_exportable | Review request validation | |
| 13 | unknown UUID | missing | 404 agent_not_found | Review request validation | |
| 14 | `../x` | invalid ID | 404 agent_not_found | Review request validation | |
| 15 | `[]` | empty | 400 export_selection_invalid | Review request validation | |
| 16 | `[A, B]` | max+1 | 400 export_selection_invalid | Review request validation | |
| 17 | name "Écrivain ✍" | unicode | 201; file_name `_crivain _.omnpkg` | Review size matches the export | same rule as export |
| 18 | name ".." | special | 201; file_name `agent.omnpkg` | Review size matches the export | trim leaves empty |

#### Dataset D2: Import files

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | bundle exported from D1-1 on the same install | happy | Preview; renamed "Writer (imported)" | Preview of a name clash | |
| 2 | same bundle on a clean install | happy | Preview; name unchanged | Preview after a valid bundle | |
| 3 | `notes.txt` (text) | wrong type | not_a_package text | Upload refusals | |
| 4 | 0-byte file | empty | not_a_package text | Upload refusals | |
| 5 | bundle, manifest format_version 2 | newer version | package_too_new text | Upload refusals | |
| 6 | bundle, element schema 2 | newer schema | element_schema_too_new text | Upload refusals | |
| 7 | bundle, one byte changed in SOUL.md | damaged | checksum_mismatch text | Upload refusals | |
| 8 | bundle with two agents | max+1 | too_many_agents text | Upload refusals | |
| 9 | bundle with agent + unknown element type `skill` (v1) | forward compat | Preview lists it under "Skipped" | Preview warnings and secrets | S1b |
| 10 | file of 67,108,864 bytes (random) | boundary max | sent; server answers not_a_package | Size check before upload | |
| 11 | file of 67,108,865 bytes | boundary max+1 | not sent; size text | Size check before upload | |
| 12 | sixth upload with five kept | capacity | too_many_staged_imports text | Upload refusals | |
| 13 | bundle of D1-4 | secrets | Preview "Enter again" API_TOKEN, REGION; warning runtime_settings_removed | Preview warnings and secrets | |
| 14 | valid bundle; upload connection cut after the server accepted it | lost answer | one upload request; "did not confirm the upload" text; slot count rises by at most 1 | Unknown upload answer is not resent | |

#### Dataset D3: Address parameters

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `/export-import` | none | landing | Entry opens the page | |
| 2 | `?wizard=export` | partial | Choose | URL table | |
| 3 | `?wizard=export&step=bogus` | invalid | Choose | URL table | |
| 4 | `?wizard=nope` | invalid | landing | URL table | |
| 5 | `?wizard=export&preselect=agent:W` | happy | W chosen | Quick Export opens… | |
| 6 | `?wizard=export&preselect=agent:` | malformed | notice "cannot be exported yet" | Unusable preselect | |
| 7 | `?wizard=export&preselect=skill:x` | unsupported type | notice | Unusable preselect | |
| 8 | `?wizard=export&step=review&plan_id=expired` | expired | Choose + expired notice | Expired review | |
| 9 | `?wizard=import&step=result` with a tab record `done` | reload | stored summary under "Import finished", no package request | Reload mid-import | |
| 9a | `?wizard=import&step=result` without a tab record | forged / new tab | landing, no completion claim, no package request | Reload mid-import | |
| 10 | `?wizard=import&step=confirm&staging_id=S`, record `previewed` | reload | cancel S once; File + notice | Reload mid-import | |
| 10a | same, record `apply_sent` | reload during or after apply | no cancel, Outcome-unknown screen | Reload mid-import | |
| 10b | same, no record | new tab | one cancel; 204/500 → File + notice; 404/409/lost → Uncertain screen | Reload mid-import | |
| 10c | same, record `previewed`, another tab already imported S | reload | one cancel → 404; Uncertain screen with Open Team | Reload mid-import | R2-MAJ-002 |
| 11 | `?scope=everything` | future value | treated as `agents` | URL table | |

#### Dataset D4: Apply outcomes

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 200 activation active, Main | happy | result + Open chat | Import succeeds | |
| 2 | 200 activation active, worker | happy | result, activation message, no Open chat | Import succeeds | |
| 3 | 200 activation pending | partial | pending notice | Pending activation | |
| 4 | 200 with warning publish_not_durable | partial | warning shown | Import succeeds | |
| 5 | 404 staging_not_found | expired | File + notice | Expired upload at confirm | |
| 6 | 409 import_in_progress | concurrency | Try again; record `previewed` | Another import is running; Try again only after a proven pre-write refusal | |
| 7 | 404 workspace_not_found | race | text, list refresh; record `previewed`; Import after a new choice | Other apply failures; Record after a known refusal | |
| 8 | 409 name_suffix_exhausted | max | text; record `previewed` | Other apply failures; Record after a known refusal | |
| 9 | 422 element_not_creatable field model.primary | error | text with field; record `previewed` | Other apply failures; Record after a known refusal | |
| 10 | 500 import_rolled_back | failure | nothing added text; record cleared; Start over → File | Other apply failures; Record after a known refusal | |
| 11 | 500 import_rollback_incomplete | failure | restart text; record cleared | Other apply failures | |
| 11a | 403 CSRF failure body; 403 planted_cookie_cleared | session refresh | one apply request, one recovery read, no toast; session text; record `previewed` | Session refresh on apply is not replayed | R2-MAJ-001 |
| 11b | no CSRF cookie (client `csrf_missing`) | local refusal | **zero** apply requests, one recovery read; session text; record `previewed`; next click sends the first apply | Session refresh on apply is not replayed | R3-MAJ-001 |
| 11c | 413 (no code) | refused before `BeginApply` | too-large text; record `previewed`; Import available | Record after a known refusal | R3-MIN-002 |
| 12 | network error | lost answer | Outcome-unknown screen, one request, Import hidden | Unknown apply outcome, never resent | |
| 13 | commit then connection cut | lost answer after commit | Outcome-unknown screen, one request; Team later shows the agent | Unknown apply outcome, never resent | |
| 14 | 502 / 504 proxy, 503 without code, 500 without code | gateway/proxy error | Outcome-unknown screen | Unknown apply outcome, never resent | |
| 15 | 200 truncated or schema-invalid body | unreadable success | Outcome-unknown screen | Unknown apply outcome, never resent | |

#### Dataset D5: Plan store timing and capacity

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | read at T + 0 | min | 200 | Review read-back and expiry | |
| 2 | read at T + 29:59 | max−1 s | 200 | Review read-back and expiry | |
| 3 | read at T + 30:00 | max | 404 | Review read-back and expiry | held while now < expires_at |
| 4 | read after restart | lifecycle | 404 | Review read-back and expiry | |
| 5 | 32 plans | max | all readable | Plan store cap | |
| 6 | fake clock: P*n* created at T + *n* s (n = 1…32); P33 at T + 1 min (none expired) | max+1 | P1 404 (uniquely oldest); P2…P33 200 | Plan store cap | distinct `created_at`, no tie |
| 7 | fake clock: P1 created at T; P2…P32 at T + 10 min; P33 created at T + 30:00 (P1 expired, P2…P32 not) | eviction order | P1 404; P2…P33 200 | Plan store cap | expired first: no unexpired review is lost |
| 8 | fake clock: P1…P3 at T; P4…P32 at T + 10 min; P33 at T + 30:00 (three expired) | several expired | P1…P3 404; P4…P33 200 | Plan store cap | equal lifetimes: the oldest expire first |

#### Dataset D6: Canary values (must never appear in any review answer or log)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `CANARY_CRED_VAULT` in credentials store | security | absent | Review never carries removed values | |
| 2 | `CANARY_MASTER_KEY` master key file | security | absent | same | |
| 3 | `CANARY_CLI_TOKEN` | security | absent | same | |
| 4 | `CANARY_AUTH_JSON` in auth.json | security | absent | same | |
| 5 | `CANARY_PROVIDER_KEY` provider key in config.json | security | absent | same | |
| 6 | `CANARY_CHANNEL_TOKEN` channel token | security | absent | same | |
| 7 | `CANARY_CLI_PATH` in cli_path | security | absent | same | |
| 8 | `--token=CANARY_ARG --verbose` (string) in cli_args | security | absent | same | |
| 9 | `CANARY_ENV` env override value | security | absent (name API_TOKEN present) | same | |
| 10 | `CANARY_PROMPT` in SOUL.md | positive control | **present** in the decoded bundle payload, absent from the review answer | Downloaded bundle carries no secret, but does carry free text | proves the decoded-payload scanner can see bundle text |
| 11 | `CANARY_INSTRUCTIONS` in AGENT.md | positive control | **present** in the decoded payload, absent from the review answer | same | second free-text carrier |
| 12 | deliberate `auth.json` open by the test | positive control | **seen** by the open watcher (Linux) | Review never opens secret files (Linux) | proves the watcher can detect an open |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Export wire, refusals, audit-before-bytes | `packages_t5_export_red_test.go`, `export_*_test.go` | Yes: `TestPackageRoutes_ExistingWireUnchanged` | plan reuses capture code; Export unchanged |
| Upload preview and slots | `packages_t5_import_red_test.go`, `staging_*_test.go` | Covered by 5, 13; plus `Sweep` regression rows for the new `cancelled` state | cancel must not change slot rules otherwise |
| Apply all-or-nothing, recovery | `packages_t5_apply_red_test.go`, `packages_t5_crash_red_test.go`, `packages_t5_recovery*_test.go` | No (untouched), but the POST dispatch must still route identically | DELETE branch only |
| External worker packaging | `packages_s1b_*` | No | |
| CLI commands | `pkg/app/internal/pkgcmd` tests | No | same routes |
| Sidebar Assets order and focus trap | Sidebar/AppShell tests, `tests/e2e/accessibility.spec.ts` | Update expectations for one more entry | count-based assertions |
| Agent edit sheet header | `AgentProfile.test.tsx` | Update for the new button | |
| Route tree | generated `routeTree.gen.ts` | regenerate | |
| Contracts | `make verify-contracts`, handwritten-wire guard | must pass | |
| E2E shard plan | `scripts/e2e-shards.sh check` | add the new spec to one non-`llm-*` shard | |

---

## Functional Requirements

- **FR-001**: System MUST provide `POST /api/v1/packages/export/plan` accepting `PackageExportPlanRequest` and answering 201 `PackageExportPlan` for exactly one exportable agent; a count other than one MUST be refused with 400 `export_selection_invalid`.
- **FR-002**: The review MUST apply the same capture, cleaning and refusals as `POST /packages/export` and MUST return the same error code and status for the same agent state.
- **FR-003**: The review MUST carry `plan_id`, `created_at`, `expires_at` (= `created_at` + 30 min), `scope`, one element (`source_id`, `name`, `kind`, files with sizes, `bytes`), `left_out`, `needs_secrets`, `disclosures` (exactly `free_text_copied` with the exact text), `size_bytes` (within 256 bytes of the bundle exported from the same unchanged agent) and `file_name` (same as the export's download name).
- **FR-004**: `left_out` MUST follow the rules table exactly (rows, order, messages, items) and MUST NOT contain any removed value.
- **FR-005**: No review answer, error or log line, and no decoded payload entry or manifest of a bundle downloaded through the B1 flow, MUST contain any Dataset D6-1…9 canary; the free-text controls D6-10/11 MUST be present in the decoded payload and absent from the review.
- **FR-005a**: The review create, read and refusal paths MUST NOT open `credentials.json`, `master.key`, `cli.token`, `auth.json` or `config.json`; asserted on Linux with the open watcher and its positive control; other platforms rely on the shared code path (stated).
- **FR-006**: A review MUST NOT write any file under the data folder, any audit entry, or use a staging slot.
- **FR-007**: `GET /api/v1/packages/export/plan/{plan_id}` MUST return the stored review unchanged while now < `expires_at` and 404 `plan_not_found` otherwise, including after a gateway restart and for a malformed ID.
- **FR-008**: The plan store MUST hold at most 32 reviews, evicting expired reviews first, then the oldest. Because every review lives exactly 30 minutes, this is observable as: creating a review never makes an unexpired review unreadable while an expired one is held (Dataset D5 rows 6–8).
- **FR-009**: The new routes MUST be registered only in local auth mode, MUST require authentication (401) and MUST refuse developer bypass (503); wrong methods MUST answer 405.
- **FR-010**: `DELETE /api/v1/packages/import/{staging_id}` MUST follow the Cancel lifecycle table: one state change under the registry's single lock; ready (expired or not) → folder removed, entry dropped, 204; removal failure → state `cancelled` (never applicable, not a slot), 500 `staging_cleanup_failed`, with removal attempted again only by the next upload's `Sweep` or a repeated cancel (after a restart, by the orphan scan inside the next upload's `Sweep`; nothing on a timer or at boot); `receiving`, `applying`, `needs-cleanup` → 409 `staging_busy` with folder, journal and state unchanged by the cancel; unknown or done → 404 `staging_not_found`; other methods → 405. The apply's own `FinishApply` and `AbortApply` behaviour is unchanged.
- **FR-011**: Existing export, upload and apply request and response shapes and outcomes MUST stay unchanged.
- **FR-012**: The sidebar MUST show "Export & Import" in Assets after "Connectors" and before "Library" only when the remembered identity mode is `local`, and MUST mark it current on `/export-import`.
- **FR-013**: The route `/export-import` MUST validate its search parameters per the address table, falling back as listed for unknown values.
- **FR-014**: The landing MUST show the heading and the two entries; when the identity mode is not `local` the route MUST show the unavailable notice and MUST make no `/api/v1/packages/` request.
- **FR-015**: Both wizards MUST show a Stepper: export "Choose agent · Review · Download", import "Choose file · Preview · Confirm · Result"; no scope or mode step in B1.
- **FR-016**: Export Choose MUST list only agents that are not locked and not of type `core` or `system`, sorted by name (case-insensitive), single choice, with kind label, description, a filter, the one-agent note, the ID prefix for duplicate names, the empty state, and Review disabled until one is chosen.
- **FR-017**: `preselect=agent:<id>` MUST choose that agent when listed; otherwise nothing is chosen and the notice from the preselect table is shown.
- **FR-018**: Export Review MUST display only server-provided review content (files with plain labels and sizes, "About" size, left-out messages and items, secrets, the free-text notice) plus the fixed "Never included" text; it MUST re-read the review by `plan_id` on reload and go to Choose with the expired notice on 404 or when `expires_at` has passed.
- **FR-019**: Download MUST first re-read the review from the server (404 → Choose with the expired notice, no export request), then call the existing export for the reviewed agent once, without automatic retry, save the response under its `Content-Disposition` file name, and show the saved name and actual byte size; errors MUST use the UI text table.
- **FR-020**: Import File MUST refuse files over 67,108,864 bytes without sending them, upload others unchanged with all replay layers switched off (at most one upload request per file choice: one normally, zero when the client refuses with `csrf_missing`), announce the busy state, map refusals to the UI text table, and show the unknown-upload text for an unknown answer.
- **FR-021**: Import Preview MUST show will-add with final names and rename explanation, skipped elements, warnings (server message and items), secrets and the local expiry time, with Cancel and Continue.
- **FR-022**: Import Confirm MUST show "Added as new. Existing agents are never changed.", a workspace choice of active workspaces with the default pre-chosen (none when there is no default), and MUST send `mode: "new"` with an explicit `workspace_id`.
- **FR-023**: Import Result MUST show the final name and workspace, activation state (message when pending), warnings and secrets, Open Team and Import another at once; it MUST look up `created[0].new_id` with the existing agent lookup when activation is `active`, and show Open chat only when that lookup returns type `Main`; while loading it shows a placeholder; on lookup failure it keeps the result and shows "Open it from the Team."; it MUST NOT infer the kind from activation text; it MUST call the existing `invalidateAfterAgentCreate` once per Result effect (an additional refresh from the `agent_created` frame is allowed).
- **FR-024**: Apply answers MUST be sorted by the "Apply outcome classes" table and handled per Dataset D4 (texts, next step) and MUST set the tab record and next action per the "Import record transitions" table: known refusals that leave the upload usable return the record to `previewed` and allow only a deliberate **Import** (labelled **Try again** for `import_in_progress`); 404 `staging_not_found`, 500 `import_rolled_back` and `import_rollback_incomplete` clear it; an unknown outcome keeps `apply_sent`.
- **FR-024a**: Upload, apply, cancel and export requests MUST be sent with all three replay layers switched off for that call: query mutation retry (`retry: false`), CSRF recovery (`csrfRetry: false`) and the planted-cookie toast retry (new `plantedCookieToast: false`), so at most one package request reaches the network per deliberate action (counted by package URL and method, separately from the recovery read `GET /api/v1/state`): exactly one for server refusals, network failures and 5xx, and zero when the existing client cookie-presence gate refuses with `csrf_missing` (that gate MUST stay unchanged); a CSRF 403, client `csrf_missing` or 403 `planted_cookie_cleared` MUST be shown as a session-refresh refusal, MUST return the record to `previewed`, and MUST send nothing again until the person acts; other callers keep today's behaviour; an Unknown apply outcome MUST show the Outcome-unknown screen, MUST NOT be described as "nothing added" or "finished", and MUST NOT offer Import again or send a cancel for that upload.
- **FR-025**: Reloads MUST follow the "Reload mid-import" table: with phase `apply_sent` no cancel and the Outcome-unknown screen; with phase `previewed` or no record, one cancel attempt (at most one package request; zero on client `csrf_missing`, per "Counting requests") whose answer is read only by the "What a cancel answer proves" table (204 or 500 `staging_cleanup_failed` → File with the reload notice; 404, 409, lost or unreadable → Uncertain screen, never File with a choose-again claim); Result with a `done` record → the stored summary; Result without a record → landing with no claim. No reload ever sends an apply.
- **FR-025a**: The wizard MUST keep the per-tab import record in session storage with exactly the listed non-secret fields, MUST write phase `apply_sent` before the apply request is sent, MUST move it per the "Import record transitions" table (back to `previewed` after a known refusal that leaves the upload usable; `done` after a parsed 200; cleared after `staging_not_found`, `import_rolled_back` or `import_rollback_incomplete`; unchanged for an unknown outcome), and MUST clear it on Import another, Done, Start over, a 204 or 500 `staging_cleanup_failed` cancel, or age over 2 hours.
- **FR-026**: Explicit **Cancel** MUST make one cancel attempt (at most one package request; zero on client `csrf_missing`, per "Counting requests") and read its answer by the "What a cancel answer proves" table (204 → landing; 500 `staging_cleanup_failed` → landing with the later-upload text; 404, 409, lost or unreadable → Uncertain screen; CSRF 403 → stay with the session text, nothing re-sent); leaving the wizard (navigation) with phase `previewed` — including after a known refusal — MUST make one silent best-effort cancel attempt (at most one package request; zero on client `csrf_missing`) and ignore its answer; with phase `apply_sent` no cancel is sent.
- **FR-027**: The agent edit sheet MUST show an "Export" button only when the remembered identity mode is `local` and the agent is eligible under FR-016's rule; selecting it MUST close the sheet and open `/export-import?wizard=export&step=choose&scope=agents&preselect=agent:<id>`.
- **FR-028**: Every new screen MUST use catalogued components, the new `Stepper` composite published through the four-part contract, no emoji, focus on the step heading after each step change, `role="alert"` for errors and a polite live region for busy states.
- **FR-029**: The client error-text map MUST cover every code in the UI text table with the exact text and fall back to the server's `error` text for unknown codes.
- **FR-030**: User-facing documentation MUST be updated per DOC-001…DOC-006 in the same change.
- **FR-031**: The system MUST NOT add an agent tool for export or import.

## Success Criteria

- **SC-001**: The E2E spec exports a seeded custom agent through the sidebar, imports the downloaded file on the same install, and the new agent "<name> (imported)" appears in the chosen workspace's Team; pass/fail.
- **SC-002**: 0 of 9 D6 secret canaries appear in any review answer, error body, captured log line, decoded bundle payload entry or manifest; D6-10 and D6-11 appear in the decoded payload; the planted-value control makes the scanner fail.
- **SC-002a**: On the Linux CI leg, the no-open test sees 0 opens of the five watched files during review requests, sees the D6-12 probe, and fails when the planted-open seam is on.
- **SC-002b**: In tests 19, 26 and 26a, through the real transport under the production query defaults and counting by package URL and method (recovery reads counted separately): every upload, apply, cancel and export case with network error, 5xx, CSRF 403 or 403 `planted_cookie_cleared` shows exactly one package request; every client `csrf_missing` case shows zero package requests; no case shows an automatic second package request or a toast with a Retry action.
- **SC-003**: For every D1 happy row, `|size_bytes − exported length| ≤ 256`.
- **SC-004**: For every D1 refusal row, the review and the export return the same status and `code`.
- **SC-005**: Review handler answers within 5 s for the benchmark fixture agent on CI (a fixture, not a promised maximum).
- **SC-006**: axe reports 0 serious or critical violations on the landing and each wizard step in the E2E spec.
- **SC-007**: `make verify-contracts`, the handwritten-wire guard, the design-system gates and `scripts/e2e-shards.sh check` pass at the cut.
- **SC-008**: Every row of the UI text table is asserted by an exact-string test.

---

## Reachability

- **Tool registration**: none — no new agent tool (FR-031). No policy entry is needed in `pkg/config` defaults or any agent config (Hard Constraint #6 not engaged).
- **Routes**: `registerPackageRoutes` (called only from the local-mode block of `rest.go::registerAdditionalEndpoints`) registers `/api/v1/packages/export/plan` and `/api/v1/packages/export/plan/` with `adminWrap` and the 1 MiB JSON limit; the existing `/api/v1/packages/import/` handler dispatches DELETE on `/import/{id}` to the cancel; POST on `/import/{id}/apply` is unchanged. Check: `grep -n 'packages/export/plan' pkg/gateway/rest_packages.go` returns the registrations.
- **Screens**: `src/routes/_app/export-import.tsx` (route, in `routeTree.gen.ts` after regeneration); sidebar entry in `Sidebar.tsx` Assets; components under `src/components/export-import/` (landing, `ExportWizard`, `ImportWizard`, one file per step, `QuickExportButton`); `src/components/ui/stepper.tsx` (catalogued); `AgentProfile.tsx` renders `QuickExportButton`.
- **Client**: `src/lib/api/packages.ts` (new) using generated types and Zod schemas.
- **Test plan execution**: Go and Vitest tests written in RED, made green in GREEN, audited in CHECK; the E2E spec runs in its assigned shard in the single full CI run at the cut; UAT rows B1-UAT-01…14 run by uat-tester and an independent uat-validator on the cut commit (hands-on pipeline). The spec is reachable when B1-UAT-01, 03, 04 and 06 pass on the cut commit.

## User-facing documentation

| TODO | UI / behaviour change | User-facing page | Matching update |
|---|---|---|---|
| DOC-001 | New page and both wizards | `docs/export-import.md` (new) | What a bundle is (`.omnpkg`); how to export one agent (steps, what the review shows, what is left out and why, the free-text warning, never-included list); **connector and skill names travel inside the agent's settings, connector settings and secrets do not, and connector assignments are not restored on import**; Quick Export; how to import (preview, rename on clash, workspace choice, result, pending activation, secrets to enter again, the chat link only for Main agents); cancel (what Cancel does; if Omnipus cannot confirm a cancel, check the Team before importing again); **retention: an upload can be imported for 30 minutes; its temporary files stay on disk until a later upload's clean-up (or a cancel) removes them; removal is an attempt that can fail while a disk problem lasts (there is no timed or start-up removal in this version)**; session refresh ("Select Import again" is safe: nothing was imported); **what "The import may have finished" means and what to do (check the Team before importing again; the app never re-sends an import by itself)**; reload behaviour; limits (one agent per bundle, Add as new only, 64 MB, 30-minute review and 30-minute import window for an upload, five waiting uploads, newer bundles refused, the agent's settings record is not size-limited on export); available in the self-hosted edition only; troubleshooting table with the UI texts. |
| DOC-002 | Agents can be exported from the app | `docs/agents.md` | Replace "It is an API only, with no screen in the app" with a pointer to the page; add Quick Export on the edit sheet; "How to move an agent to another install" leads with the page, CLI second; add the review and cancel endpoints to the "Without the app" API line. |
| DOC-003 | New sidebar entry | `docs/using-omnipus-ui.md` | Assets row and the mermaid "links to" list include Export & Import. |
| DOC-004 | Settings note points to bundles | `docs/settings.md` (Backup removal note) | "Use the Export & Import page (or the command line) to move agents between installations." |
| DOC-005 | CLI page no longer says there is no screen | `docs/using-omnipus-cli.md` (How to move an agent; comparison table) | **Replace** "There is no screen for this, and no agent can do it for you." with: the same can be done from Export & Import in the app (self-hosted edition); no agent can do it for you. **Replace** the table cell "No separate screen." with "Export & Import page". State that a bundle is the same package file. |
| DOC-006 | New page in the index | `docs/README.md` | Link to `export-import.md`. |

Word rule for docs: the user word is **bundle**; "package" appears only where the CLI command or API path says it.

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | Documentation TODO(s) |
|---|---|---|---|---|
| FR-001 | US-6 | Review contents per agent kind; Review request validation | 1, 6, 7 | DOC-002 |
| FR-002 | US-6, US-2 | Export refusals at review time; Review request validation | 2, 7 | DOC-001 |
| FR-003 | US-6 | Review contents per agent kind; Review size matches the export | 1, 3, 6 | DOC-001 |
| FR-004 | US-6, US-2 | Review contents per agent kind; Review shows the server's review; Review wording matches the real bundle | 1, 3a, 6, 21 | DOC-001 |
| FR-005 | US-6, US-2 | Review never carries removed values; Downloaded bundle carries no secret, but does carry free text | 4, 11, 11a | DOC-001 |
| FR-005a | US-6 | Review never opens secret files (Linux) | 9a | DOC-001 (never-included list) |
| FR-006 | US-6 | Review changes nothing | 9 | none — not user-visible (no data change to describe) |
| FR-007 | US-6, US-2 | Review read-back and expiry; Review survives a reload | 6, 21 | DOC-001 |
| FR-008 | US-6 | Plan store cap | 10 | none — internal capacity, invisible except as an expired review (covered by DOC-001's expiry text) |
| FR-009 | US-6, US-1 | Review route guard | 8 | DOC-001 (edition note) |
| FR-010 | US-7 | Cancel frees the slot; Cancel refusals; Folder removal fails; Folder left after a restart is removed by the next upload; A restart alone removes nothing; Cancel and apply at the same moment | 5, 5a, 13, 13a | DOC-001, DOC-002 |
| FR-011 | US-4 | Import succeeds | 14 | none — unchanged behaviour |
| FR-012 | US-1 | Sidebar shows the entry…; Entry opens the page; Entry hidden unless… | 16, 29 | DOC-003 |
| FR-013 | US-2, US-3, US-5 | Unusable preselect; Reload mid-import; Expired review | 17, 28 | DOC-001 |
| FR-014 | US-1 | Entry opens the page; Page unavailable in the platform edition | 17, 29 | DOC-001 |
| FR-015 | US-2, US-4 | Choose lists only…; Confirm states the mode… | 15, 20, 26 | DOC-001 |
| FR-016 | US-2 | Choose lists only…; Review needs a choice; No own agents | 20 | DOC-001 |
| FR-017 | US-3 | Quick Export opens…; Unusable preselect | 20, 23 | DOC-001 |
| FR-018 | US-2 | Review shows the server's review; Review survives a reload; Expired review | 21 | DOC-001 |
| FR-019 | US-2 | Download saves the bundle; Agent deleted before download; Audit failure…; Expired review (download rows) | 19, 22, 29 | DOC-001 |
| FR-020 | US-4, US-5 | Preview after a valid bundle; Upload refusals; Size check before upload; Unknown upload answer is not resent | 19, 24, 26a | DOC-001 |
| FR-021 | US-4 | Preview after a valid bundle; Preview of a name clash; Preview warnings and secrets | 25 | DOC-001 |
| FR-022 | US-4 | Confirm states the mode…; No default workspace; No active workspace | 26 | DOC-001 |
| FR-023 | US-4 | Import succeeds; Chat action decided by the agent lookup; Pending activation; Lists refresh after import | 27, 29 | DOC-001 |
| FR-024 | US-5 | Expired upload at confirm; Another import is running; Try again only after a proven pre-write refusal; Other apply failures; Record after a known refusal; Two uploads are imported one at a time (server) | 13b, 26, 28 | DOC-001 |
| FR-024a | US-5 | Unknown apply outcome, never resent; Unknown upload answer is not resent; Session refresh on apply is not replayed; Session refresh refusals happen before the import starts (server) | 13c, 19, 26, 26a | DOC-001 (troubleshooting) |
| FR-025 | US-5 | Reload mid-import | 27, 28 | DOC-001 |
| FR-025a | US-5 | Reload mid-import; Try again only after a proven pre-write refusal; Record after a known refusal | 26, 28 | none — internal browser record; its visible effect is covered by FR-025's DOC-001 text |
| FR-026 | US-7, US-5 | Cancel from the wizard; Leaving the wizard cleans up silently; Reload mid-import; Record after a known refusal | 25, 28 | DOC-001 |
| FR-027 | US-3 | Quick Export opens…; No Quick Export for built-in agents (incl. mode rows) | 23, 29 | DOC-002 |
| FR-028 | all UI stories | all UI scenarios | 15, 29 (axe) | none — accessibility is not a documented behaviour change |
| FR-029 | US-5 | all error outlines | 18 | DOC-001 (troubleshooting table) |
| FR-030 | all | — | docs-verifier audit | DOC-001…006 |
| FR-031 | — | — | review check: `grep -rn 'omnpkg\|packages/export' pkg/sysagent pkg/tools pkg/coreagent` returns nothing | none — no user-visible effect |

Every BDD scenario above appears in at least one row.

---

## Decisions (formerly open questions)

- **Q1 = A** (chief, 2026-11-21): the cancel call ships in B1, with the lifecycle in "Cancel lifecycle" (atomic under the registry lock, no journal ever deleted, defined clean-up failure).
- **Q2 = A** (chief, 2026-11-21): self-hosted edition only. Whether the desktop app gets Export & Import is a separate founder question tracked outside this spec; B1 only verifies that the page and routes are absent there (B1-UAT-11).
- No new founder question arises from review round 1.

---

## Assumptions

- The founder's requirement receipt is as relayed in the dispatch (treated as given); no interview-me file exists for this slice.
- "Show only what exists today" means: one-agent wording, no scope step, no mode step, no disabled placeholders for later scopes.
- Built-in agents are exactly those with `locked: true` or type `core`/`system` in the agent list; the server stays the authority (refusal on review).
- The existing export rules (S1a/S1b, bundle content, audit) are correct and unchanged; their tests remain the oracle for export content.
- The plan store lives in the gateway process, like the staging registry; one account per install, so plans are not partitioned by user.
- Holdout evaluation scenarios (global plan-spec skill) are omitted: the repo template replaces them with spec-time UAT rows validated independently on the cut commit.
- Onboarding's hand-built step indicator is not migrated to `Stepper` in B1 (follow-up issue to file).

## Clarifications

### 2026-11-21

- Q: Does B1 need `plan_digest`? -> A: No; nothing verifies it before B2's export job (ADR D-E3.1 row, adjusted; this spec).
- Q: Should the download be bound to the review? -> A: No in B1; the ADR keeps the existing export unchanged; the Download step shows the actual size (Ambiguity 1).
- Q: Which steps appear? -> A: Founder rule "show only what exists today": Choose agent · Review · Download, and Choose file · Preview · Confirm · Result (ADR D-E6 adjusted for B1).
- Q: Agent tool for bundles? -> A: None (agent tool table; root `CLAUDE.md` parity rule satisfied by writing the difference and reason down).
- Q: Where is the left-out list computed? -> A: On the server, in the review (founder: "review step lists contents, size, what is left out"; ADR FR-3).
- Q: Q1, Q2 -> A: both A (chief, 2026-11-21).

### 2026-11-21 (revision 2, after spec review round 1)

- Q: Which evidence survives a reload during apply? -> A: a per-tab session-storage record with phase `apply_sent` written before dispatch; without it, the cancel's answer (MAJ-002). ADR D-E6's B1 reload row ("reload shows 'Import finished'") is narrowed: a Result reload shows the stored summary only when the tab recorded a success; otherwise it claims nothing. To be recorded as an amendment when the ADR is promoted.
- Q: Cancel deletion failure? -> A: `cancelled` state, 500 `staging_cleanup_failed`, slot freed, removal attempted by the next upload's sweep (its orphan scan covers folders left by a restart) or a repeated cancel; no timed or boot removal (MAJ-003; corrected in revision 3, R2-MAJ-003).
- Q: Connector wording? -> A: names travel, settings and secrets do not, assignments not restored (MAJ-004); export content unchanged.
- Q: Plan validity at Download? -> A: re-read the review just before the export; the export stays unbound (MIN-002).

### 2026-11-21 (revision 3, after spec review round 2)

- Q: Which browser layers can re-send a package call? -> A: three (mutation retry, CSRF recovery, planted-cookie toast Retry); all off for the four package calls, the last through the new `plantedCookieToast: false` option; the wizard owns the session-refresh message (R2-MAJ-001).
- Q: What does a cancel answer prove? -> A: only 204 and 500 `staging_cleanup_failed` prove a cancel; 404, 409 and lost answers lead to the Uncertain screen, because 404 also means "already imported" (R2-MAJ-002).
- Q: When are temporary upload files removed? -> A: at a later upload's sweep or a successful cancel; 30 minutes is the import window only; no timer and no boot removal in B1 (R2-MAJ-003).
- Q: What may a race test assert? -> A: answers and state at each observation point; the apply's own `FinishApply` folder removal is expected, so no exclusive-or oracle (R2-MAJ-004).
- Q: Where does the record go after a known refusal? -> A: back to `previewed`; a reload or leaving then cancels once; only a deliberate Import re-sends (R2-MIN-001).

### 2026-11-21 (revision 4, after spec review round 3)

- Q: How are requests counted when the browser has no CSRF cookie? -> A: by package URL and method, recovery reads apart; client `csrf_missing` sends zero package requests (gate unchanged); server refusals, network failures and 5xx send one, never two (R3-MAJ-001).
- Q: What decides whether a reload cancels? -> A: the current record phase: `apply_sent` → never cancel; `previewed` (including after a known refusal and 413) → one cancel (R3-MIN-002).
- Q: What may a clean-up failure message promise? -> A: an attempt at a later upload, not deletion (R3-MIN-001).
- Q: How is "expired first" tested? -> A: explicit fake-clock fixtures D5-7 and D5-8 (R3-MIN-003).
