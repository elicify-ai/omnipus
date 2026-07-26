# UAT Report — Plan/Board/Graph, Goals, Loops, Lifecycle (2026-07-26)

Branch `feature/plan-swimlane-board`. Five human-impersonating testers, three isolated gateways,
real LLM. Plan: [`uat-plan-plan-swimlane-2026-07-26.md`](uat-plan-plan-swimlane-2026-07-26.md).

**Status: COMPLETE — all five testers reported. Everything below is either verified by me directly or attributed to the tester
who observed it.**

---

## 0. Two environment defects found and fixed mid-run (NOT product bugs)

Recorded first because both produced convincing-looking false findings.

**E-1 · `mkauth.mjs` dropped the session cookie → "chat is completely dead" (reported S1).**
`/auth/login` sets `omnipus-session` (HttpOnly) **and** `csrf`; the harness kept only `csrf`. The
SPA authenticates the WS via that cookie and post-Wave-1 deliberately sends no `{type:'auth'}`
frame (`src/lib/ws.ts:721`); the server tries the cookie first and only then demands a frame
(`pkg/gateway/websocket.go`). No cookie → demanded a frame that never comes → infinite reconnect.
REST was unaffected (bearer token), which is why *only* chat looked broken. **Fixed** — mkauth now
carries every login cookie and warns if `omnipus-session` is absent. **Verified after fix:** Mia
replied `PINEAPPLE`, socket opened once and stayed open, zero errors. Two testers independently
confirmed the withdrawal.

**E-2 · QA Runner absent from every picker.** `useWorkspaceTeamIds` filters selects to
`core_team ∪ delegation edges`; the agent was created without workspace-team membership. Testers
were forced onto deny-by-default agents, confounding tool-permission failures with engine
failures. **Fixed** — added to `core_team` on all three gateways; owner dropdown now lists it
(verified live).

---

## 0b. RELEASE BLOCKER — the Execute approval gate is enforced on nothing (VERIFIED BY ME)

**Plan member tasks execute themselves while the plan is still `draft` and was never approved.**

Tester repro (`PRIYA-GATE-never-executed`, Execute never clicked):
```
t+0s   plan.state=draft  approved_at=null | task.status=next  started_at=null
t+20s  plan.state=draft  approved_at=null | task.status=done  result="LEAKED…"
FINAL  plan {"state":"draft","approved_at":null,"progress":1}
```
The board renders `Draft · 1/1` with a full progress bar **and still offers an Execute button**.
Two other plans reproduced it independently.

**Verified by me in the code, not inferred:**
- `TaskExecutor.CheckQueuedTasks` (`pkg/agent/task_executor.go:1803`) lists `Filter{Status: next}`
  and dispatches. It skips only human-only tasks (`AgentID == ""`) and unmet `blocked_by` deps —
  **it never reads `PlanID` and never consults the parent plan's state.**
- `ExecuteTask` has no plan gate either (grepped its body for `PlanID`/`plan.`/`approved`: empty).
- Driven by `pkg/heartbeat/task_drain.go:112` on a ticker, so it fires unattended.

**Why this is a blocker.** The Execute confirm dialog promises: *"This starts autonomous
execution … Its member tasks will run — and be judged and retried — **without further
approval**."* Approval is the control gate for autonomous agent execution, and nothing enforces
it. It also means the FR-084 criteria gate, the DoD gate and `plan.Lint` are all bypassed for the
work that actually runs. Reachable through the UI by dragging a plan member Inbox → Next.

**Same root cause, second symptom (S2): Stop does not stop remaining members.** `PRIYA-D8-race`
was stopped at 10:36; member `PRIYA-D8-t2` was `next` at that moment and the heartbeat started it
*after* the stop, running it to completion — final state `failed`/`stopped_by_user` with progress
**2/2**, i.e. "Cancelled" over a full bar. Stop's own fan-out correctly cancels `in_progress`
members; the leak is that a terminal plan's `next` members are never removed from the queue.

---

## 1. Confirmed by me at the code level

| # | Finding | Sev | Evidence |
|---|---|---|---|
| **1** | **Multi-goal support is dead on arrival.** `GoalStatusFrame` is never constructed with `GoalId` — `grep "GoalId:" pkg/ --include=*.go` excluding tests returns **empty**, though the field exists in the contract and TS type. Every goal lands in the `_default` bucket, so `GoalPillTray`'s documented "one pill per goal-id" can never fire. Observed: goal → amend → clear → second goal left exactly one pill and no history. | S3 | Alex #11, verified by me |
| **2** | **`/goal clear` exists** (`pkg/agent/goal_loop.go`, verbs `clear`/`stop`/`cancel` — `goal_loop_test.go:196`). This **downgrades Sam's E5 "no way to clear a goal"**: the mechanism is real, it is a slash command rather than a button. The `TaskDetailPanel` "Stop/Clear goal loop" control is task-scoped (`isRunning && attempt_count > 0`) and does not apply to a chat goal. | — | verified by me |
| **3** | **Clearing a goal reports `failed`.** `/goal clear` replies "Goal cleared (cleared by user)" while the WS frame carries `state:"failed"`, so the pill flips to a red ✗ **failed** badge for a deliberate, successful action — and lingers. | S3 | Alex #12 |

---

## 2. Plan lifecycle — §2 matrix verdict (Marcus)

| Plan state | Spec | Verdict |
|---|---|---|
| `draft` → Execute | ✔ | verified-correct |
| `running`/`approved` → Stop | ✔ | verified-correct, **both** sub-states observed |
| `failed` + `stopped_by_user` → Restart | ✔ | verified-correct (copy deviates: button says "Play", dialog says "Restart") |
| `done` → none | ✔ | **verified-correct** — plan reached `done` 10 s after Execute with a fully-capable owner; zero action buttons offered |
| `failed` other reason → none | ✔ | **verified at BOTH layers** |

**All five matrix rows are now verified.** D5 was reached on re-run once QA Runner was usable.

**D4 — the highest-value case — PASSED.** A genuinely-failed plan
(`failed_reason: judge_rounds_exhausted`) offers no Execute/Stop/Play/Restart, the ⋯ menu holds
only "Clear", and `POST /api/v1/plans/{id}/restart` returns **409** naming the reason. US-9
Acceptance 2 holds in UI *and* API. The UI also correctly distinguishes **Cancelled** (orange,
user-stopped) from **Failed** (red, genuine).

---

## 3. Product findings

### Blocking / high

| # | Finding | Sev |
|---|---|---|
| **4** | **`plan_phase: awaiting_owner_correction` is terminal in practice — no UI escape.** *(Corrected: the tester's original "no plan can reach `done`" was too broad and was retracted on re-run — the happy path DOES complete. What stands is the parking state.)* Five of six plans sit there; the only offered action is Stop, and the screen never says why. The sole explanation is a hover-only `title` naming three corrections ("append a tail, supersede a done member, targeted-retry a frozen member") that **have no corresponding controls** — dead on touch, and dead-ended on desktop. | S2 |
| **4b** | **A `check` DoD criterion runs in a DIFFERENT working directory than the task it verifies — VERIFIED BY ME.** `pkg/tools/shell.go:508-511` re-roots `baseDir` only when `TurnWorkspaceDir(ctx)` is set, and that is set at exactly ONE production site (`pkg/agent/loop.go:6432`, the agent's turn ctx). The judge's check call (`pkg/agent/judge.go:534-536`) builds `callCtx` and adds **only** `tools.WithAgentID` — it never propagates the workspace dir, so the check falls back to the fixed agent dir while the work landed in `workspaces/<id>/work/`. Measured: file present on disk, `test -f <file>` → `exit_code:1`, `policy_denied:false`, `timed_out:false` — a genuine `unmet` on work that was actually done. Any `check` using a relative path against workspace output is guaranteed to fail, burning attempts and pushing plans toward `judge_rounds_exhausted`. **`check` criteria are the deterministic half of the DoD story, so this undercuts the feature's main selling point.** One-step repro for a maintainer: make a criterion's command `pwd`. | S2 |
| **5** | **Executing a plan whose members sit in `Inbox` silently does nothing.** `dispatchReadyMembers` (`pkg/agent/plan_engine.go:914`) only dispatches `next`; nothing promotes `inbox`. Plan reads "Running" with 0 progress indefinitely. Moving the task to **Next** dispatched it in ~2 s. No UI signal that manual triage is required. | S2 |
| **6** | **Plan creation fails silently from the form's default state.** `owner_agent_id` is server-required but unmarked and unvalidated client-side; the error toast renders **underneath the slide-over footer** (measured: `coveredBySomethingElse: true`). User sees "Saving…" ~7 s then nothing. | S2 |
| **7** | **Goal textarea allows 4000 chars; server caps at 2000.** No counter, no inline error. | S2 |
| **8** | **The API creates unnamed plans.** `title: "   \t  "` → **HTTP 201**; `""` correctly 400s, so the check is `== ""` rather than trimmed. Produces an untitled, unfindable, unfilterable plan chip. SPA blocks it client-side, so any non-SPA caller — including an agent tool — can create one. | S2 |
| **9** | **Graph silently shows a subset, with no indication.** Sam: scope card "4 tasks", Board 4, List 4, **Graph 1**. Alex: 3 unlinked tasks dropped entirely. **CORRECTED after I read the code — both testers framed this as an unimplemented "unlinked tray"; it is not.** `GraphView.tsx:292-296` states unlinked tasks are **deliberately** not rendered ("they live on the Board/List"), and an *all*-unlinked workspace correctly shows `GraphEmptyState`. The real defects are narrower: (a) when SOME tasks are linked, the unlinked ones vanish with **no count and no hint** — only the all-empty case is handled; (b) `taskGraph.ts:137` still promises an `"N unlinked tasks" tray` that was deliberately dropped, so the doc comment misleads the next reader. Downgraded from S2 — this is a missing affordance plus a stale comment, not an unfinished feature. | S3 |

### Medium / low

*(Finding 9 above is S3 and belongs here; it is left in place so the Graph findings read together.)*

| # | Finding | Sev |
|---|---|---|
| **10** | One Create click sends **4 identical POSTs** — `queryClient.ts:37` retries mutations on any non-401/403/404, so a deterministic 400 is retried on a non-idempotent create, stretching the error to ~7 s. | S3 |
| **11** | Silent truncation on paste: title 413→200, goal 5000→4000, no notice. | S3 |
| **12** | A 200-char unbroken title (exactly the UI's own `maxLength`) pushes STATUS/TAGS/AGENT/UPDATED/Actions **off-screen with no horizontal scroll** — per-row actions unreachable for every row. | S3 |
| **13** | **Bidi override (U+202E) stored and rendered raw** — `ALEX-C4-‮EXE.txt` displays as `ALEX-C4-txt.EXE`. Extension-spoofing primitive in a shared workspace. | S3 |
| **14** | Graph auto-fits ~12 dependency-free plan members to an illegible zoom — the "field of disconnected dots" the design says it avoids; legible only after 4 zoom-ins. | S3 |
| **15** | Natural-language goal-setting **falsely confirms**: agent replies "Goal set: …" (after calling *Set todos*) while no goal exists. | S3 |
| **16** | Reloading Chat starts a new empty session while `meta.json` still holds a live goal (`goal_rounds_used 1/20`); no session history in the sidebar, so no route back to a round-burning goal. | S3 |
| **17** | Quick-create inside a plan-scoped board lands the task **unplanned** (`plan_id: null`) — intended (`WorkspaceTasksTab.tsx:366`) but the UI says nothing; the work appears to vanish. Recovery ("Move to plan…") works. | S3 |
| **18** | A failed plan never renders **why** — `failed_reason` is on the wire, never shown; only action is "Clear". | S3 |
| **19** | NUL (U+0000) and ANSI ESC accepted verbatim in titles; ESC colourises any terminal/log echo. | S4 |
| **19b** | **"Create & Run" with Agent = Unassigned parks a task in `In Progress` forever.** `SAM-T1` sat at `in_progress` with `agent_id: null` for the whole session, doing nothing and saying nothing. Same shape as the plan-owner problem (finding 6): the form lets you start work with nobody assigned to do it. Related, from a second tester: tasks PATCHed to `in_progress` are picked up within one cycle and **two of three landed back in `next`** rather than staying `in_progress` — so the lane is doubly unreliable. Worth checking together. | S3 |
| **20** | Internal spec IDs leak into user copy: `(soft tier)`, `(D5)`, `(re-run, G-3)`; a DoD row asks a non-developer for `go test ./... -run TestX`; an unlabeled `0` input (aria-label only). | S4 |
| **21** | `active loops 5/16` — global telemetry — is rendered inside a personal goal card. | S4 |
| **22** | Play/Restart vocabulary inconsistent: button + confirm say "Play", dialog title + toast say "Restart". | S4 |
| **23** | Collapsed goal pill truncates mid-word (expanded card is verbatim — verified programmatically). | S4 |
| **24** | Composer agent selection resets to Mia on every load. | S4 |

---

## 4. Security: no XSS. The boundary is real.

Alex fired `<script>`, `<img src=x onerror>`, `<svg onload>`, `javascript:` hrefs and SQL-ish
strings through **plan title, plan goal, task title, task prompt, chat message and `/goal`
condition**, then hard-reloaded to force the stored-render path.

**Zero dialogs, zero injected nodes** across Board, List, Graph, plan band, chat thread and the
amendment diff — `{"scriptWithXSS":0,"imgSrcX":0,"svgOnload":0,"jsHref":0,"literalScriptVisible":true,"rawOnerrorInHTML":false}`.
React escaping holds on every reachable surface. **No S1.**

Bidi (#13) and NUL/ESC (#19) are stored raw — a display/log concern, not injection.

---

## 5. What works well

- **Confirm dialogs are excellent** — accurate, jargon-free, and they match what actually happens
  ("marks it Cancelled" → badge reads Cancelled).
- **Live updates are real** — lanes and badges move without refresh; observed across testers.
- **Plan attribution is exact** under 4 concurrent plans — no cross-talk.
- **Reload mid-execution** preserves state and re-syncs cleanly.
- **Client-side validation, where it exists, is good** — empty title gives a correct inline error
  with no request fired.
- **Goal amendment (E3) is genuinely good** — a comprehensible `[added]`/`[dropped]` diff with an
  explicit confirm step.
- **Server-side caps are clean** — specific 400s, no 500s, no crash at 50,000 chars.

---

## 5b. Performance — all budgets PASS by wide margins (Chen, own gateway)

50 tasks / 3 plans / 80 graph edges, seeded via REST so no LLM tokens were burned. Every figure
is a median of ≥3 samples. **Zero console/pageerror/network errors across every run.**

| Scenario | Measured | Budget | Verdict |
|---|---|---|---|
| Cold load → interactive, 50 tasks | **542 ms** | <3 s | PASS 5.5× |
| Board→List / List→Graph / Graph→Board | **71 / 215 / 121 ms** | <500 ms | PASS |
| Board scroll, 50 tasks | p50 **16.7 ms**, 0 frames >33 ms | no jank | PASS (locked 60 fps) |
| Graph 50 nodes initial render | **342 ms** (+13 ms for all 80 edges) | <2 s | PASS 5.8× |
| 12 tasks updating at once | p50 16.7 ms, UI interactive in 162 ms | responsive | PASS |
| Memory, 10-min / 180 cycles | heap **13.638 MB flat, slope 0** | no growth | PASS |

**My published baselines were wrong and are withdrawn.** The 2,348 ms cold load and ~1,040 ms
view switches in the plan's §1.5 were `waitUntil:'networkidle'` plus a fixed `waitForTimeout` —
instrument time, not app time. Reproduced as a control: `networkidle` reads 953 ms where real
time-to-interactive is 542 ms, and the switch figures were ~1,000 ms of `waitForTimeout` over
~50 ms of real work. Use the table above.

**Graph correctness also verified:** linear chain (3n/2e), fan-out-then-join (5n/6e, textbook
ranks), plan-scope filter (2+6+72 = 80 = the All-mode total, i.e. **zero cross-plan leakage**),
node click opens the right task, zoom-out clamps at `minZoom` without blanking, and **0 node
overlaps** at 50 nodes.

**Chen disproved its own leak finding rather than reporting it.** An apparent +949 DOM
nodes/cycle turned out to be its own `page.evaluate` + `Performance.getMetrics` instrumentation
inflating the CDP counter; a controlled 3-arm A/B showed `dLiveDom = 0`, `dHeapMB = 0`. No leak.

### Graph findings (Chen)

| # | Finding | Sev |
|---|---|---|
| **25** | **The graph never re-fits when the plan-scope filter changes the node set.** `fitView` is mount-only (`GraphView.tsx:317`) and the re-seed effect (`:198-201`) calls `setNodes`/`setEdges` without re-fitting. Scope 50→3 leaves scale 0.23 (3 nodes filling 0.5 % of canvas); manual fit then scoping back to 50 leaves scale 1.40 with **48 of 50 nodes clipped off-screen**, no cue but the minimap. | S2 |
| **26** | At 50 nodes auto-fit renders labels at **3.22 px** — unreadable. Cause is vertical budget: 310 px (34 %) of a 900 px viewport is permanent chrome (tab bar + Plans band + view switcher), leaving 590 px of canvas. Layout itself is clean. | S3 |
| **27** | **The board has one shared vertical scroller, not per-lane scrollers** (`scrollHeight 4089 / clientHeight 583`). Inbox's 34 cards set the height, so past ~1,200 px the Next/Done/Failed columns are blank while their headers still read 3/10/3. | S3 |
| **28** | **The `Blocked` lane is unreachable.** A task with an unmet `blocked_by` stays `inbox`; PATCHing it to `next` while its blocker is still `inbox` returns **200**. Setting `blocked` directly correctly 400s ("derived side-state") — but nothing ever derives it. `Task.yaml` says `blocked` is "set automatically". Across 52 tasks with 42 dependency edges the lane held **0** all session. | S2 |
| **29** | ~~Plan vs task agent validation is inconsistent~~ — **WITHDRAWN.** This was the QA-Runner-not-in-`core_team` environment defect (E-2), not a product inconsistency. Re-verified after the fix: `POST /api/v1/tasks` with `agent_id: <QA Runner>` now returns **201** (was 400). | — |

### WITHDRAWN — "live updates are poll-driven, not pushed" (reported S2)

Originally measured at 13.66 s median with zero `task_status_changed` frames and **7 socket closes
(1006)**, concluding freshness was bounded by `refetchInterval: 15_000`.

**This was the same harness cookie defect as E-1**, not a product bug — that run predated the fix
and its WebSocket was dying repeatedly, so it measured the poll fallback. The emitter exists
(`pkg/gateway/websocket.go:3415,3424`).

**Re-run on the fixed environment — the numbers are unambiguous:**

| Measure | Broken socket | Fixed |
|---|---|---|
| Graph update latency (median, n=6) | 13,659 ms | **289 ms** |
| Board update latency (median, n=9) | — | **190 ms** |
| UI settle, 12 concurrent updates | 14,496 ms | **1,177 ms** |
| UI interactive during flood | 162 ms | **100 ms** |
| WS frames during 12-task flood | 0 | **12** — 1:1, no amplification or duplicates |
| Frame p95 / max | 16.9 / 91 ms | **20.2 / 56.6 ms**, none >100 ms |

**Socket stability exit proof — 183 s soak:** `socketsOpened: 1, closes: [], errors: 0`, with 6
`pong` frames confirming the heartbeat now completes. Previously it died roughly every 13–15 s, so
a 183 s hold would have caught ~12 closures. Push latency stayed flat (183–209 ms) from t=41 s to
t=183 s — no late-soak degradation.

Latency decomposes as ~75 ms HTTP + ~56 ms emit→deliver + ~158 ms frame→paint. The graph's extra
~99 ms over the board is dagre re-layout of 50 nodes plus the React Flow re-seed — expected, not a
defect. **G5 now passes on the case the budget actually cares about**: 12 discrete frames in one
~1.2 s burst rather than a single 14.5 s poll cycle.

## 5c. Races and loops — the lifecycle held up (Priya)

Everything she attacked deliberately **passed**; the failures she found were elsewhere.

| # | Scenario | Result |
|---|---|---|
| **D8** | Stop immediately after Execute — 4 methods incl. `Promise.all` at 0 ms gap | **PASS, no wedge.** Every attempt ended in a legal state with an action offered. The 0 ms race was cleanly refused: `400 plan is "draft"; only a running or approved plan can be stopped` — exactly the required "cleanly refuse" branch |
| **D7** | Rapid double-click Execute/Stop/Play | **PASS, idempotent** — exactly one POST each time; the confirm button disables while pending and unmounts on settle |
| **D9** | Restart twice | **PASS** — two concurrent restarts → `409` + `409` with a precise message, state unchanged |
| **F1/F3/F4** | Loop chips, stop mid-loop, survive reload | **PASS** — `attempt N/M` chips render, Stop halts the loop, chips survive a hard refresh |
| **A5** | Live board updates | **PASS** with a healthy socket — 0–2 s lag driven by real `plan_status`/task frames |
| **A4** | Drag between lanes | **mouse PASS** (one PATCH, survives reload — no silent revert); **keyboard FAIL**, see below |

### Additional findings

| # | Finding | Sev |
|---|---|---|
| **30** | **Keyboard drag-and-drop is a no-op.** Cards advertise "Enter to open, Space to move"; Space lifts and the live region announces correctly, but ArrowRight/ArrowDown never change the target column and the drop announces "was moved to the Inbox column". Zero network calls, status unchanged after reload. Reproduced twice. **A keyboard-only user cannot move a card at all.** | S3 (S2 for keyboard-only users) |
| **31** | **A stuck plan is indistinguishable from a working one.** `awaiting_owner_correction` renders as plain "Running" on the Board — `planPhaseChip()` has the right copy but is only wired into `WorkspaceGraphTab.tsx`, never the Plans band. Four plans sat "Running" indefinitely. (Same surface as finding 4.) | S2 |
| **32** | **Contract drift:** `Plan.yaml` allows `awaiting_owner_correction`, but `PlanStatusFrame.yaml` (and generated `_asyncapi-zod-schemas.generated.ts:629`) allows only `dispatching\|judging\|synthesizing\|idle`, while `gateway.go:2624` emits `PlanPhase` unfiltered — so a plan entering that phase pushes a frame the SPA's zod edge drops. Latent Constraint #8 violation. | S4 |

**Also independently confirmed by Priya:** the WS cookie defect (B3 — "REST worked perfectly all
session while the socket died ~20 times with zero frames in either direction"), which corroborates
E-1 above from a third tester, and the long-title layout collapse (finding 12).

## 6. Measurement honesty

Only one timing figure is currently trustworthy: **cold load to `/board` = 2,385 ms** (Sam,
`networkidle`, §4 method) — corroborated by my own 2,348/2,131 ms baselines. Sam's view-switch
numbers are **invalid** (a fixed `waitForTimeout` sat inside the measurement window) and are
excluded. Chen's Group-G numbers are still outstanding.

---

## 7. The pattern worth fixing beyond the individual bugs

Findings 6, 8, 9, 15, 17 and 18 share one shape: **the system has the right information and
discards it.** The 400's message is good and never shown; `failed_reason` is on the wire and never
rendered; the graph knows about unlinked tasks and drops them; the agent says "Goal set" having
set nothing. As the first-time tester put it — *"this build has a habit of swallowing errors it
already has good messages for."*
