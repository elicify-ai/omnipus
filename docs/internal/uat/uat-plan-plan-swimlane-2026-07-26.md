# UAT Plan — Plan/Board/Graph, Goals, Loops, Lifecycle (2026-07-26)

Branch `feature/plan-swimlane-board`. Exploratory, human-impersonating UAT driven through the
SPA in a real browser. Every scenario below has been checked as **reachable** against a live
gateway before being written down — see §1.

**Read [`harness/GUIDE.md`](harness/GUIDE.md) first.** It documents hash routing, the
workspace-as-project IA, the 7-tab bar, and selector conventions. This plan does not repeat it.

---

## 1. Environment (pre-verified — do not re-provision)

| Thing | Value |
|---|---|
| Base URL (in-pod) | `http://127.0.0.1:8080` |
| Base URL (external) | `https://pod-omnipus.fly.dev` — verified HTTP 200 |
| Auth state | `<SCRATCH>/uat/auth.json` (admin/admin123, token + CSRF cookie) |
| Workspace | `01KYEYSTKC539ZRSQ2NH561EH8` — "My Workspace" |
| Provider | OpenRouter, `google/gemini-2.5-flash` — **real LLM, real tokens; be economical** |
| Seeded roster | mia, jim, ava, ray, judge, worker, planner, explorer, researcher |

**Test agent — "QA Runner"** (`9f58b3d5-0c43-4822-8f16-54b5d44bade9`): a Main agent seeded with
**83/83 tool policies set to `allow`**, explicitly enumerated and wildcard-free (Hard Constraint
#6 forbids wildcards for static builtin tools). Verified on disk. **Use QA Runner for any
scenario that must not be blocked by tool policy.** If a scenario fails with a permission/denial
error while acting as QA Runner, that is a **bug** — report it, do not work around it.

### Why this matters for scenario reachability
The seeded core agents are deny-by-default (`denyAllThenOverride`). A test that fails because
Mia lacks `bash` is testing the seed, not the feature. QA Runner exists so every scenario below
is genuinely executable end-to-end.

---

## 1.5 Verified UI facts — read before writing selectors

I drove the live gateway before writing this plan. **`harness/GUIDE.md` describes an older IA
(a 7-tab bar with Board/List/Graph as tabs). That is out of date.** What is actually there:

**Top tab bar** (`role="tab"`, real `<a href>` — deep-linkable):
`My Workspace` (button) · `Chat` · **`Tasks`** (→ `/board`) · `Calendar` · `Team`

**Board / List / Graph are NOT tabs and NOT links.** They are
`<button role="radio">` in a radio group inside the Tasks view:

```js
await page.getByRole('radio', { name: 'Graph' }).click();   // works
await page.getByRole('link',  { name: 'Graph' }).click();   // finds nothing
```

**Consequence — deep-linking `/workspaces/<id>/list` or `/graph` lands on the Board.** The view
selector is client-side only and never updates the URL; all three views stay on
`…/board`. Route files for `/list` and `/graph` exist in the source but are not what the current
IA uses. This is a real finding (**S4** — no shareable/bookmarkable Graph URL, and browser Back
does not undo a view switch) but it is **not** a broken route — do not re-report it as S2.

**Confirmed working** (measured, zero console/pageerror/network errors):

| View | How to reach | Observed |
|---|---|---|
| Board | default | 6 lanes: **Inbox · Next · In Progress · Blocked · Done · Failed** |
| List | radio | table — STATUS / TAGS / AGENT / UPDATED / Actions |
| Graph | radio | empty-state: "a task on the Board and its dependencies will graph here — laid out left to right" |
| Calendar | tab | month view |
| Team | tab | "Team & delegation" |

~~Cold board load 2.35 s; each view switch ~1.0-1.1 s.~~ **WITHDRAWN — these were my own
measurement artifacts.** They used `waitUntil:'networkidle'` plus a fixed `waitForTimeout`,
i.e. instrument time, not app time. Correct figures (median of >=3, own gateway, 50 tasks):
**cold load to interactive 542 ms**, view switches **71-215 ms**. Do not quote the old numbers.

### RESOLVED — the WS 1006 / "chat is dead" question (was flagged here as an open question)

**It was a HARNESS BUG, not a product defect. Fixed. Do not re-report it.**

`/api/v1/auth/login` sets **two** cookies — `omnipus-session` (HttpOnly) and `csrf` — but
`harness/mkauth.mjs` persisted only `csrf`. The SPA's WS client authenticates via the
`omnipus-session` cookie the browser auto-attaches on the upgrade request and, post-Wave-1,
deliberately sends **no** `{type:'auth'}` frame (`src/lib/ws.ts:721`). Server-side,
`authenticateWS` (`pkg/gateway/websocket.go`) tries `ResolveUserFromCookie` **first** and only
falls through to the legacy frame path when that fails. With the cookie dropped, that fallback
demanded a frame the SPA will never send → socket closed → infinite reconnect → chat dead.
REST was unaffected because it uses the localStorage bearer token, which is why *only* chat
looked broken.

`mkauth.mjs` now carries over every login cookie (and warns loudly if `omnipus-session` is
missing). **Verified after the fix:** sent "Reply with exactly one word: PINEAPPLE" → Mia
replied `PINEAPPLE`, no disconnect banner, clean WS handshake, zero console/pageerror/network
errors.

A stale/flickering 1006 banner may still appear briefly during reconnects while the app remains
fully functional — one tester confirmed plan create/execute/stop/restart, refetches and live
lane movement all succeed while it is displayed. Treat a *transient* banner as **S4 cosmetic**;
only a banner that coincides with actually-broken behaviour is worth reporting.

### RESOLVED — "QA Runner" was missing from every agent picker

Also an environment error, now fixed. `useWorkspaceTeamIds` filters the owner/agent selects to
`core_team ∪ delegation edges`; QA Runner was created but never added to the workspace team, so
it was filtered out everywhere. **It is now in `core_team` on all three gateways** and appears
in the pickers. Use it — that is the whole point of its 83/83 allow policies.

### Known engine behaviour that will otherwise look like a bug

- **Plan execution only dispatches members in `Next`.** A member created into `Inbox` is never
  dispatched: `dispatchReadyMembers` (`pkg/agent/plan_engine.go:914`) only picks up `next`, and
  nothing promotes `inbox`. The plan sits at `running` with 0 progress and no hint. Move the
  task to **Next** and it dispatches in ~2 s. (Reported separately as an S2 — the UI gives no
  signal that manual triage is required — but know it so you don't chase it twice.)
- **Quick-create inside a plan-scoped board lands the task UNPLANNED** (`plan_id: null`), by
  design (`WorkspaceTasksTab.tsx:366`). Use the task detail panel's "Move to plan…" to attach it.
- **Selecting a plan card auto-switches Board → Graph**, deliberately
  (`WorkspaceTasksTab.tsx:104-121`), and deselecting does **not** switch back.

**Lane names in scenario A3 are the six above** — use those, not the raw status strings.

---

## 2. Plan lifecycle — the authoritative button matrix

From `planActionFor()` (`src/components/workspaces/PlanActionButton.tsx`, ADR-052 §6.8). This is
the spec the UI must obey; deviations are bugs.

| Plan state | Offered action | Button |
|---|---|---|
| `draft` | `execute` | **Execute** |
| `running` or `approved` | `stop` | **Stop** (confirm dialog: "Stop this plan?") |
| `failed` **and** `failed_reason === 'stopped_by_user'` | `play` | **Restart** |
| `done` | *(none)* | no button |
| `failed` for any **other** reason | *(none)* | no button — **US-9 Acceptance 2** |

**The last row is the highest-value edge case in this plan.** A genuinely-failed plan must NOT
offer Restart; only a user-stopped one may. Getting this backwards silently invites users to
re-run broken work.

API surface: `POST /api/v1/plans/{id}/approve` · `/restart` · `/stop` · `/{action}`.

---

## 3. Scenarios

Severity: **S1** data loss / security / unusable · **S2** feature broken · **S3** wrong-but-
recoverable · **S4** polish.

### A. Board (swimlane) — `/#/workspaces/<id>/board`

| # | Scenario | Expected | Sev if broken |
|---|---|---|---|
| A1 | Load board with zero tasks | Empty state is explanatory, not a blank void or spinner-forever | S3 |
| A2 | Create a plan that yields ≥5 tasks; open board | Tasks appear in correct lanes; lane counts match card counts | S2 |
| A3 | Lane semantics | Observed lanes include `in_progress`, `blocked`, `done`/`completed`, `failed`. Verify each card's lane matches its status badge | S2 |
| A4 | Drag a card between lanes (if supported) | Either it persists after reload, or dragging is visibly disabled. **Silent revert = S2** | S2 |
| A5 | Board during live execution | Cards move lanes without a manual refresh (WS-driven) | S2 |
| A6 | Board with ~50 tasks | Scrolls smoothly; no layout thrash. Record interaction latency (§4) | S3 |
| A7 | Deep-link straight to `/board` on a cold load | Renders without first visiting Chat | S3 |

### B. Graph (task DAG) — `/#/workspaces/<id>/graph`

React Flow canvas. **Screenshot every graph state** — most graph bugs are visual.

| # | Scenario | Expected | Sev |
|---|---|---|---|
| B1 | Graph with a linear 3-task plan | Three nodes, two edges, correct direction | S2 |
| B2 | Graph with a branching plan (fan-out then join) | Edges match real dependencies; no crossing/overlap that hides structure | S2 |
| B3 | Orphan tasks (no deps) | Handled per `taskGraph.orphanCollapse` — collapsed or clearly grouped, never scattered | S3 |
| B4 | Plan-scope filter | Selecting one plan shows only its subgraph (`taskGraph.planScope`) | S2 |
| B5 | Pan + zoom | Smooth; nodes stay legible; no blank canvas after zoom-out | S3 |
| B6 | Node click | Opens detail/slide-over for the right task | S2 |
| B7 | Graph with ~50 nodes | Renders in reasonable time; measure (§4). Note if the browser locks up | S2 |
| B8 | Live updates while running | Node states change without manual refresh | S3 |

### C. Plans — create, execute, edge cases

| # | Scenario | Expected | Sev |
|---|---|---|---|
| C1 | Create plan via CreatePlanSlideOver | Validation is clear; plan lands in `draft` | S2 |
| C2 | Submit empty / whitespace-only goal | Rejected with a helpful message — **not** a 500 and not a silently-created empty plan | S2 |
| C3 | Very long goal (~5000 chars) | Accepted or rejected gracefully; no truncation-without-telling and no layout break | S3 |
| C4 | Goal with emoji / RTL / newlines / `<script>` | Stored and rendered safely; **no HTML injection** | S1 if injection |
| C5 | Execute a draft plan | Button reads **Execute**; state → running/approved; tasks appear | S2 |
| C6 | PlansFilterBand | Filters actually narrow the list; clearing restores | S3 |
| C7 | Two plans at once | Both progress; no cross-talk in board/graph attribution | S2 |
| C8 | Reload mid-execution | State survives; UI re-syncs rather than showing stale/frozen data | S2 |

### D. Pause / stop / restart — the lifecycle matrix

| # | Scenario | Expected | Sev |
|---|---|---|---|
| D1 | Stop a running plan | Confirm dialog appears; on confirm → "Plan stopped"; state `failed` + `stopped_by_user` | S2 |
| D2 | Stopped plan offers **Restart** | Per matrix row 3 | S2 |
| D3 | Restart a stopped plan | "Plan restarted"; execution resumes | S2 |
| D4 | **A genuinely-failed plan offers NO Restart** | Per US-9 Acceptance 2. **If Restart appears here it is S2** — it invites re-running broken work | S2 |
| D5 | `done` plan offers no action | No button | S3 |
| D6 | Cancel the Stop confirm dialog | Plan keeps running; nothing changed | S2 |
| D7 | Double-click Execute/Stop rapidly | Idempotent; no duplicate runs, no stuck "Stopping…" | S2 |
| D8 | Stop immediately after Execute (race) | Either cleanly stops or cleanly refuses — **never a wedged plan with no available action** | S1 if wedged |
| D9 | Restart twice in a row | Second is either a no-op or a clean second run | S3 |

### E. Goals

Components: `GoalIndicator`, `GoalPillTray`, `GoalEchoCard`, `GoalAmendmentDiff`,
`GoalThreadTailCards`.

| # | Scenario | Expected | Sev |
|---|---|---|---|
| E1 | Set a goal in chat | GoalIndicator/PillTray reflects it | S2 |
| E2 | Goal echo card | Restates the goal accurately, no truncation mid-word | S3 |
| E3 | Amend an active goal | GoalAmendmentDiff shows a comprehensible before/after | S2 |
| E4 | Goal + plan together | Goal context visibly influences the plan; no contradiction between the two surfaces | S3 |
| E5 | Clear/complete a goal | Indicator clears; no ghost pill left behind | S3 |
| E6 | Multiple sequential goals | Tray shows correct active vs historical | S3 |

### F. Loops

`TaskCard.goalLoopStatus` is the surface.

| # | Scenario | Expected | Sev |
|---|---|---|---|
| F1 | Task that loops/retries | Loop status is visible on the card, with attempt count | S2 |
| F2 | Loop hitting its cap | Terminates with a clear terminal state — **does not spin forever** | S1 if infinite |
| F3 | Loop + stop | Stopping mid-loop actually halts it | S2 |
| F4 | Loop status after reload | Survives a refresh | S3 |

### G. UI performance (§4 for method)

| # | Scenario | Budget |
|---|---|---|
| G1 | Cold load to interactive | < 3 s |
| G2 | Tab switch (Chat→Board→Graph→List) | < 500 ms each |
| G3 | Board with 50 tasks — scroll | No visible jank |
| G4 | Graph with 50 nodes — initial render | < 2 s |
| G5 | Live execution, 10+ tasks updating | UI stays responsive; no WS-flood freeze |
| G6 | Memory over a 10-minute session | No unbounded growth (leak indicator) |

### H. Usability (judgement-based — report opinions, clearly labelled)

| # | Prompt |
|---|---|
| H1 | As a first-time user, is it obvious how to create and run a plan? |
| H2 | When a plan fails, does the UI explain **why** and what to do next? |
| H3 | Are Board / Graph / List distinct enough to justify three views? |
| H4 | Is the Stop-vs-Restart distinction discoverable, or did you have to guess? |
| H5 | Any dead-end state with no obvious next action? |
| H6 | Anything you clicked expecting X and got Y? |

---

## 4. Performance measurement method

Do not eyeball it. In the harness script:

```js
const t0 = Date.now();
await page.goto(url, { waitUntil: 'networkidle' });
const load = Date.now() - t0;

const nav = await page.evaluate(() => {
  const n = performance.getEntriesByType('navigation')[0];
  return n ? { domContentLoaded: n.domContentLoadedEventEnd, loadEvent: n.loadEventEnd } : null;
});
const mem = await page.evaluate(() => performance.memory?.usedJSHeapSize ?? null);
```

Report actual numbers, not "felt fast". A budget miss is **S3** unless the UI is unusable (**S2**).

---

## 5. Reporting contract

For every finding:
1. **What I did** — exact steps, reproducible.
2. **What I expected** vs **what happened**.
3. **Screenshot** — filename.
4. **Severity** S1–S4, with reasoning.
5. **Console/network errors** — from `finish()`'s JSON.

Rules:
- **Do not fix anything.** Report only.
- Separate **facts** (observed) from **opinions** (usability). Label opinions as such.
- A scenario you could not run is a finding too — say why (blocked, missing UI, unclear).
- **Do not report a scenario as passed if you did not actually execute it.** An honest "not
  reached" is worth more than a fabricated pass.
- WS reconnect warnings in the console are **expected noise**, not bugs.
