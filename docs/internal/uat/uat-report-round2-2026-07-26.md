# UAT Round 2 — consolidated report (2026-07-26)

Branch `feature/plan-swimlane-board`, build from `5d77f26a`. Five parallel testers, three
isolated gateways (`:8080` / `:8081` / `:8082`), each with a seeded **QA Runner** agent
(83/83 tool policies `allow`) added to the workspace `core_team`.

Round 1 report: `uat-report-plan-swimlane-2026-07-26.md`. Test plan:
`uat-plan-plan-swimlane-2026-07-26.md`.

## 0. Environment — both round-1 setup defects closed before dispatch

Round 1 was partly invalidated by two harness defects of our own making. Both were fixed
and **verified before** any tester started, so round-2 findings are about the product:

| Round-1 defect | Status |
|---|---|
| `mkauth.mjs` persisted only `csrf`, dropping the HttpOnly `omnipus-session` cookie → false "chat is dead" S1 | Fixed; both cookies confirmed present on all three gateways |
| QA Runner never added to `core_team` → filtered out of every picker, forcing testers onto deny-by-default agents | Fixed; `PUT 200` on all three, agent selectable |

Pre-flight on each gateway: chat replies, **0 WS closures**, no disconnect banner, board
lanes render, view toggle works, zero console/page/network errors.

## 1. Headline: the Execute gate holds — but a laundering path bypasses it

### 1.1 The gate itself is sound

Two testers attacked it independently and could not break it by any dispatch route.

- **11 routes** attacked (Marcus), incl. a **25-minute / ~25-heartbeat soak** on round 1's
  exact repro — which in round 1 ran to `done`. Held. Positive control confirmed the
  pipeline was live: restarting a stopped plan dispatched all 3 members within 10 s.
- **7 further routes** (Priya) incl. the completion cascade, a stopped plan with a member
  dragged to `next` *after* the stop (209 s), and a genuinely **paused** plan
  (`state:running` + `paused_reason:"owner_disabled"`, 200 s). All held.
- Board drag Inbox→In Progress and Next→In Progress both `409`; card snaps back and
  survives reload. `POST /tasks` with `status:"in_progress"` + `plan_id` persists `inbox`.
- `PUT /plans/{id}` `{state:running}` → `400 state cannot be set via PUT`.

The **paused** sub-case has no REST trigger (pausing is engine-internal), so it is covered
by test rather than observation at both layers — `TestCheckQueuedTasks_PausedRunningPlanMemberNotDispatched`
(asserts the provider is never called) and `TestHandleTaskPatch_InProgress_PausedRunningPlanMember_DoesNotLaunch`
(409). Both run and pass. Priya additionally induced a real paused plan and confirmed the 409.

### 1.2 S1 — deleting a plan launders its unapproved members into auto-running tasks

Found by one tester only, after every direct route failed. **Two clicks: ⋯ → Clear.**

Causal chain (verified in code, not inferred):
1. `handlePlanDelete` (`pkg/gateway/rest_plans.go:836-842`) detaches members with
   `task.Patch{PlanID: &empty}` and changes **nothing else** — a `next` member stays `next`.
2. `requirePlanExecuting` (`pkg/agent/task_executor.go:2004-2007`) returns `nil` when
   `PlanID == ""`. Correct in isolation: standalone tasks are legitimately runnable.
3. The ~60 s heartbeat drain (`CheckQueuedTasks`, `Filter{Status: StatusNext}`) then runs it.

| Variant | Observed |
|---|---|
| **A — never-executed draft plan.** Execute never clicked, progress 0 | Member ran itself at +~90 s (`started_at 13:32:24`, `done 13:32:28`). Over the API: `DELETE` → 204, member ran at t+40 s |
| **B — plan the user explicitly STOPPED.** Direct stop path verified to hold for 209 s | After Clear, **2 of 3 members executed themselves** (13:53:24, 13:54:24). Cancelled work came back to life |

The confirm dialog was actively misleading — *"Member tasks are not deleted, but lose their
plan grouping"* — never mentioning that they start running.

**Fix:** non-terminal members detach to `inbox` (terminal `done`/`failed` keep status), in the
same striped-lock RMW that clears `plan_id`; dialog copy corrected.

## 2. Round-1 findings verified fixed

Each re-checked by the tester who filed it.

| Area | Result |
|---|---|
| Goal cap 4000-vs-2000 | **Fixed.** `maxlength=2000`; live counter, amber at limit + ring. Server: 2000 → 201, 2001 → 400 |
| 4× POST per Create click | **Fixed.** 400 → **1** POST (was 4). 408/429/500 still retry by design |
| Goal-clear red "failed" tombstone | **Fixed.** Pill disappears cleanly; `meta.json` has zero `goal_*` keys |
| Silent plan-create failure | **Fixed.** Required-field marker + inline "Owner agent is required", instant, zero network |
| Long unbroken title (task card, List row) | **Fixed.** `overflow-wrap:anywhere`, `line-clamp:2`, no page-level h-scroll on Board/List/Graph |
| Graph re-fit on node-set change | **Fixed** both directions; camera now **bit-identical** across unrelated edits |
| Graph min-zoom floor | **Fixed.** Node title **3.22 px → 10.4 px** (3.23×), legible at native res |
| Failed plan showed only "Failed" | **Fixed.** `Why: judge rounds exhausted` always visible |
| `awaiting_owner_correction` undiscoverable | **Fixed.** Always-visible chip + plain-language explanation + explicit next step |
| Quick-create in plan scope landed unplanned, silently | **Fixed.** Hint shown; recovery works |
| Restart copy disagreed across surfaces | **Fixed.** aria / dialog / button / toast all agree |
| Blocked lane unreachable | **Fixed.** `inbox→next` with unmet blockers → 200 with derived `status:"blocked"` |
| Keyboard drag was a complete no-op | **Fixed.** Announcements track columns; `PATCH 200`; survives reload |

**Security held:** 13 XSS payloads × 6 entry points × pre/post-reload — **zero** dialogs,
beacons or injected nodes; `javascript:` never becomes an anchor; stored render escapes.

**No performance regression.** Cold load 592 ms (budget 3 s), view switches 74–197 ms,
board scroll 60 fps locked (0 frames >33 ms), heap flat over 20 cycles. The tester
controlled for host load rather than reporting a spike: a raw API GET with zero SPA code
inflated ~2.5× at load-avg 15 vs 9.6, so the spread is the box, not the build.

## 3. Open findings

| Sev | Finding | Status |
|---|---|---|
| **S1** | Plan delete launders unapproved members into auto-running tasks (§1.2) | Fix in progress |
| **S2** | Plan members attached to a plan sit in **Inbox** forever while the card reports "Running 0/N". Root cause: `dispatchReadyMembers` only takes `next`; nothing promotes `inbox`→`next`. Hit by 2 testers on the naive happy path; one sat 225 s, the other 14 min | Fix in progress |
| **S2** | Plans mostly park at `awaiting_owner_correction` rather than reaching `done` (2 of 11 completed). A plan with all members `done` and progress 1/1 parked 15+ min | Open — judge behaviour |
| **S3** | Invisible codepoints (ZWSP/ZWNJ/ZWJ/word-joiner/BOM/soft-hyphen/U+2800) bypassed title validation → blank plans, now reachable from the form | **Fixed** — solved as a class (`unicode.Cf` + U+2800), not a 7-codepoint blacklist |
| **S3** | Drag refusal says "refresh and try again" — which can never help; the specific server reason is discarded | Fix in progress |
| **S3** | Drag live region announces "was moved" on a **refused** move (a11y: opposite of the truth) | Fix in progress |
| **S3** | Bidi override `U+202E` renders raw on 5 surfaces — extension-spoofing primitive | Open |
| **S3** | A **paused** plan is indistinguishable from a running one; wire carries `paused_reason`, UI discards it | Open |
| **S3** | Chat sessions persist (`GET /sessions` returns them) but the SPA always opens fresh; `/resume` restores the transcript but **not** the goal pill/round counter — a live, token-burning loop you cannot see | Open |
| **S3** | Task form says criteria are optional; Execute hard-refuses with 400 for missing criteria | Open — contradiction |
| **S3** | Plan card title bleed (2183 px past a 196 px card) | **Fixed** |
| **S4** | Graph banner stated a false reason ("no dependencies"; the real rule is plan membership); all-unlinked empty state claimed "No tasks yet" while the tile read "3 tasks" | **Fixed** (copy only; logic intended) |
| **S4** | Stop double-click now fires 2 POSTs (409 + 200); round 1 measured 1 | Open — minor click-guard regression |
| **S4** | `POST /tasks` with `status:"in_progress"` returns 201 and silently persists `inbox` | Open — fails safe, silently |
| **S4** | NUL and ESC stored verbatim | Open |
| **S4** | 500 on non-idempotent create is retried 4× (duplicate-create risk) | Open |

## 4. Not reached / honest gaps

- **F3 mid-loop stop** — 6 attempts burned in <20 s, so the loop had already capped; what
  was tested is "stop an already-capped plan", not a mid-loop stop.
- **`approved`-state Stop button** — the window is ~7 s in the UI; verified at the API layer only.
- One tester deleted another tester's plan via a `.first()` selector hitting the wrong ⋯
  menu. Cross-tester interference on a shared gateway; recorded for transparency.

## 5. Note on CI

The e2e gate reported "48 failed" across 5 shards on this commit. That was **infrastructure,
not code**: a Playwright browser-revision mismatch meant no browser ever launched (every
test failed in 4–6 ms, including retries). Every other gate — gofmt, go-build, go-vet,
golangci-lint, verify-contracts, typecheck, vitest, **go-test** — was green. Fixed in
`deploy/ci-worker/runci.sh` (install via the repo-local playwright, add the separately
downloaded `chromium-headless-shell`, and assert the resolved revisions exist on disk,
because installing the wrong revision also exits 0). Documented as a third false-signal
trap in `deploy/ci-worker/CLAUDE.md`.
