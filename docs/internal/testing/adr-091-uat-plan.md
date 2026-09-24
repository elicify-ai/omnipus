# ADR-091 — UAT plan: steered sessions (delegation) and visibility

**What this covers.** ADR-091 removed the old "sub-agent" special case. A delegated
worker is now an ordinary session that another session steers. This plan checks the
behaviour a person actually sees: work gets delegated, results come back, Stop
works, queues are visible, and nothing leaks between sessions or accounts.

**Who runs it.** Written for a human tester clicking through the Omnipus web UI.
It is *executed* by parallel Claude browser agents, one per lane, each in its own
isolated browser. Every lane is self-contained: it does its own setup, shares no
state with any other lane, and can run at any time in any order.

**How to judge.** Every scenario ends with a PASS/FAIL line a non-developer can
apply without reading code. If you cannot tell whether it passed, that is a FAIL —
report what you saw.

---

## Where the expected results come from

Every number and state name below is taken from product documentation or the wire
contract, not from reading the implementation. If the product disagrees with this
plan, **that is a finding** — report it rather than adjusting the plan.

| Expectation | Source |
|---|---|
| Delegation depth caps at **3** levels with nothing configured | `docs/settings.md` — "How deep delegation may go" |
| Over-cap chain is **refused**; the extra work never starts | same |
| At the concurrency cap a delegation is **queued with its place in line**, never refused | `docs/settings.md` — "How many agents run at once" |
| Concurrency floor is **2** when memory cannot be measured | same |
| A delegated session's lifetime cap is **30 minutes**, and a follow-up does **not** restart it | `docs/settings.md` — "How long a child may run" |
| The status words a row may show: `queued`, `running`, `needs_input`, `paused`, `completed`, `failed`, `cancelled`, `timed_out` | `contracts/components/schemas/SubagentStateFrame.yaml` |
| Side panel holds at most **50** pending updates | `docs/settings.md` — "Safety stops that are not settings" |

---

## Rules for every lane

1. **Own account, own browser.** Do not reuse another lane's login, agents, or chats.
   Name everything with your lane letter (`uat-a-worker`, `uat-b-chat`) so two lanes
   can never collide.
2. **Never judge from a spinner.** Wait for a terminal state or an explicit timeout.
   If nothing changes for 3 minutes, capture a screenshot and call it FAIL — a hang
   is exactly the class of bug this ADR set out to fix.
3. **Screenshot on every FAIL**, plus the moments named in each scenario. Name them
   `lane-<letter>-<scenario>-<moment>.png`.
4. **Report what you saw, not what you expected.** "The row never appeared" is a
   result. "Probably fine" is not.
5. **Do not fix anything.** No code changes, no config edits, no retries-until-green.
   One clean run, then report.
6. If a step's UI element is not where this plan says, **look for it once**, then
   report the discrepancy and continue if you can.

**Useful element handles for the browser agents** (the human-readable description is
what a tester looks for; the handle is for automation):

| What a tester sees | Handle |
|---|---|
| The activity strip above the message box | `activity-bar` |
| One worker's row in that strip | `activity-row` |
| The status text on a row | `activity-row-status-line` |
| The expand/collapse control on a row | `activity-row-toggle` |
| The "open this worker's session" control | `activity-row-open` |

---

## Lane P — Pre-flight (run once; ~10 min)

Not a pass/fail lane. It establishes that the build is testable at all. If P fails,
**stop and report** — every other lane's result would be meaningless.

1. Open the Omnipus URL and sign in.
2. Go to **Settings → Performance**. Record what `max_parallel_agents` shows
   (a number, or "automatic — bounded by available memory") and the delegation depth
   and timeout values. **Lanes E and B need these numbers** — publish them to the
   other lanes before they start.
3. Create two agents: one named `uat-p-boss`, one named `uat-p-worker`. Give
   `uat-p-boss` permission to delegate to `uat-p-worker` (Team/roster settings).
4. Open a new chat with `uat-p-boss`, send "hello", confirm a normal reply arrives.
5. Screenshot: the Performance tab, and the working chat.

**Stop condition:** if you cannot sign in, cannot create an agent, or a plain chat
does not reply, report and stop.

---

## Lane A — Delegation works, and stays contained (~20 min)

### A1 — A delegated job comes back with a real answer

**Setup.** Agents `uat-a-boss` and `uat-a-worker`, boss may delegate to worker.

**Steps.**
1. Open a chat with `uat-a-boss`.
2. Send: `Ask uat-a-worker to write one sentence about the colour blue, then tell me exactly what it said.`
3. Watch the activity strip above the message box.
4. Wait for the boss's final reply.

**Expected.**
- A row appears in the activity strip for the worker.
- The row ends at `completed`.
- The boss's reply contains a real sentence about the colour blue — actual content,
  not a placeholder, not "I have delegated the task", not an apology.

**Screenshots.** (a) the moment the worker row first appears; (b) the final reply
with the row visible.

**PASS** if the boss's answer contains the worker's actual sentence.
**FAIL** if the boss replies without the content, if the row never reaches
`completed`, or if nothing happens for 3 minutes.

### A2 — The worker's internal chatter stays out of the human's chat

**Setup.** Continue in the same chat as A1 (this scenario deliberately reuses it).

**Steps.**
1. Send: `Ask uat-a-worker to count from 1 to 5, thinking out loud at each step, then give me only the final total.`
2. Read the **main chat transcript** carefully, top to bottom.

**Expected.**
- The main chat shows the boss's final answer.
- The main chat does **not** show the worker's step-by-step thinking, its tool calls,
  or its intermediate messages.
- That detail is reachable only by expanding the worker's row or opening its session.

**Screenshots.** (a) the full main transcript after the reply; (b) the expanded row
showing the detail *is* available there.

**PASS** if the human's chat contains only the final answer.
**FAIL** if any of the worker's internal steps appear in the main chat. This is a
containment failure — report it as high severity.

---

## Lane B — Nesting and the depth limit (~25 min)

### B1 — Three levels deep still returns an answer

**Setup.** Three agents: `uat-b-l1`, `uat-b-l2`, `uat-b-l3`. `l1` may delegate to
`l2`; `l2` may delegate to `l3`.

**Steps.**
1. Open a chat with `uat-b-l1`.
2. Send: `Ask uat-b-l2 to ask uat-b-l3 for one word that rhymes with "cat", and tell me the word.`
3. Watch the activity strip. Expand rows as they appear.
4. Wait for the top-level reply.

**Expected.**
- A row appears for `uat-b-l2`; expanding it (or opening its session) shows a child
  row for `uat-b-l3`.
- Every level ends at `completed`.
- The top-level chat receives an actual rhyming word.

**Screenshots.** (a) both levels visible; (b) the final reply.

**PASS** if the word arrives in the top-level chat and no level is left spinning.
**FAIL** if any level stays `running` for more than 3 minutes after its child
finished, or the answer never reaches the top. *This is the single most important
scenario in the plan — a chain that silently never finishes is the defect this ADR
exists to fix.*

### B2 — Too deep is refused, clearly, before any work starts

**Setup.** Read the depth cap from Lane P (default is **3**). Create one more level
than the cap: with a cap of 3, create `uat-b-d1` … `uat-b-d4`, each permitted to
delegate to the next.

**Steps.**
1. Open a chat with `uat-b-d1`.
2. Send an instruction that forces the full chain: `Ask uat-b-d2 to ask uat-b-d3 to ask uat-b-d4 for the word "done", and report back what you get.`
3. Watch the activity strip and read the final reply.

**Expected.**
- Levels within the cap run normally.
- The hop past the cap is **refused** with a message that names depth as the reason.
- No row ever appears for the over-cap level — it must never start, not start and
  get cut off.

**Screenshots.** (a) the activity strip at the moment of refusal; (b) the message
explaining it.

**PASS** if the refusal is explicit, mentions depth, and no over-cap worker started.
**FAIL** if it fails silently, hangs, gives an unrelated error, or the over-cap
worker visibly starts and is then killed.

---

## Lane C — Stop, and what it reaches (~20 min)

### C1 — Stop on a running worker takes effect at once, and reaches its children

**Setup.** Agents `uat-c-boss`, `uat-c-worker`, `uat-c-sub`; boss→worker→sub.

**Steps.**
1. Open a chat with `uat-c-boss`.
2. Send: `Ask uat-c-worker to ask uat-c-sub to count slowly from 1 to 500, reporting every number.`
3. Wait until you can see both the worker and its child running.
4. Press **Stop**.
5. Watch both rows for 60 seconds.

**Expected.**
- Both rows leave `running` promptly and settle on `cancelled` (or `interrupted` on
  the row's detail).
- No row is still `running` or `queued` 60 seconds later.
- The chat is usable again immediately.

**Screenshots.** (a) both rows running, before Stop; (b) both rows 60s after Stop.

**PASS** if every level stops and nothing is left running.
**FAIL** if any level keeps running, if a child outlives its parent, or if Stop
appears to do nothing.

### C2 — Stop on a worker that never started still cancels it

**Setup.** Same agents. You need a worker sitting in `queued` — easiest right after
C1's Stop, or by starting several workers at once (see Lane E).

**Steps.**
1. Start enough delegations that at least one row shows `queued`.
2. Without waiting for it to start, press **Stop**.
3. Watch the queued row for 60 seconds.

**Expected.**
- The queued row becomes `cancelled` promptly.
- It does **not** stay `queued`, and it does **not** start running afterwards.

**Screenshots.** (a) the row showing `queued` before Stop; (b) the same row 60s after.

**PASS** if the queued worker reaches `cancelled` without ever running.
**FAIL** if it stays `queued`, or starts running after Stop. *This was a real
defect — a queued worker used to stay stuck until the server restarted.*

---

## Lane D — Reviving, and running out of steps (~25 min)

### D1 — A stopped worker resumes with the NEW instruction, not the old one

**Setup.** Agents `uat-d-boss`, `uat-d-worker`.

**Steps.**
1. Open a chat with `uat-d-boss`.
2. Send: `Ask uat-d-worker to write a long essay about the history of the bicycle.`
3. Once the worker row shows `running`, press **Stop**.
4. Now send: `Never mind the bicycle. Ask uat-d-worker instead for a single word: the capital of France.`
5. Wait for the reply.

**Expected.**
- The reply answers the **new** question — the capital of France.
- The reply is not a bicycle essay, and does not mix the two.

**Screenshots.** (a) the moment of Stop; (b) the final reply.

**PASS** if the answer is "Paris" (or a sentence containing it).
**FAIL** if any bicycle content comes back. *A revived worker used to re-run its
original instruction and report that as a genuine answer — confidently answering
the question you had just cancelled.*

### D2 — A worker that runs out of steps reports back instead of hanging

**Setup.** Agents `uat-d2-boss`, `uat-d2-worker`. The worker needs tools available
so it can burn steps.

**Steps.**
1. Open a chat with `uat-d2-boss`.
2. Send an instruction that will exhaust the worker's tool budget: `Ask uat-d2-worker to repeatedly list the files in the workspace, one directory at a time, until it has explored absolutely everything, and then summarise.`
3. Watch the worker row and the boss's chat for up to 5 minutes.

**Expected.**
- If the worker hits its limit, the boss is **told** — a message or a row state
  change appears.
- The boss's chat does not sit on a spinner with no explanation.

**Screenshots.** (a) the row at the moment it stops progressing; (b) the boss's chat
at that moment.

**PASS** if the boss's chat shows an outcome — success, or a clear "the worker hit
its limit".
**FAIL** if the boss's chat spins indefinitely with no message. *This used to hang
the whole chain silently, all the way up to the human's chat.*

**Note.** The worker may simply complete before hitting the limit. That is a valid
result — record "limit not reached" rather than forcing it.

---

## Lane E — The queue is visible and honest (~20 min)

**Setup.** Read the concurrency cap from Lane P. If it shows "automatic", set
`performance.max_parallel_agents` to **2** in Settings → Performance for this lane,
and **record that you changed it**. Create agents `uat-e-boss` and four workers
`uat-e-w1` … `uat-e-w4`.

### E1 — Over the cap means queued with a position, never refused

**Steps.**
1. Open a chat with `uat-e-boss`.
2. Send: `Ask all four of uat-e-w1, uat-e-w2, uat-e-w3 and uat-e-w4 to each write one sentence about the sea. Start them all now.`
3. Immediately look at the activity strip.

**Expected.**
- Four rows appear — **all four**, not two.
- Up to the cap show `running`; the rest show `queued`.
- Queued rows indicate their place in line.
- **No delegation is refused or silently dropped.**

**Screenshots.** (a) the strip immediately after sending, with running and queued
rows both visible; (b) the strip once all four have completed.

**PASS** if every requested worker is represented, extras are queued rather than
refused, and all four eventually complete.
**FAIL** if fewer than four rows appear, if any delegation is refused with a
capacity error, or if a queued row never starts once a slot frees.

### E2 — Queued work starts in order as slots free

**Steps.** From E1, watch the queued rows as running ones complete.

**Expected.** Queued rows become `running` as slots free, in the order they queued.

**PASS** if every queued worker eventually runs and completes.
**FAIL** if a queued worker is skipped, or stays queued after slots have freed.

**Clean-up.** Restore `max_parallel_agents` to what Lane P recorded.

---

## Lane F — The side panel tells the truth (~20 min)

### F1 — A worker is visible the moment it is queued, not only when it runs

**Setup.** Agents `uat-f-boss`, workers `uat-f-w1` … `uat-f-w3`. Cap set low enough
to force queuing (see Lane E).

**Steps.**
1. Open a chat with `uat-f-boss`.
2. Ask it to start all three workers at once.
3. Look at the activity strip **immediately**, before anything completes.

**Expected.** The strip is visible and shows a row for **every** worker, including
those still `queued`.

**Screenshots.** The strip within a few seconds of sending.

**PASS** if queued workers are visible straight away.
**FAIL** if the strip stays hidden until something starts running, or queued workers
are missing. *A queued delegation used to be completely invisible.*

### F2 — The rows survive a page reload

**Steps.**
1. With workers still running or queued from F1, reload the browser page.
2. Wait for the chat to finish loading.

**Expected.** The activity strip returns with the same workers and sensible states.
Nothing that existed before the reload has vanished.

**Screenshots.** (a) before reload; (b) after reload.

**PASS** if the same workers are present after reload.
**FAIL** if rows disappear, or a row appears **only** after reload and was missing
before — both directions are bugs.

### F3 — A very long task description still shows a row

**Setup.** Agents `uat-f3-boss`, `uat-f3-worker`.

**Steps.**
1. Open a chat with `uat-f3-boss`.
2. Send a delegation with a very long task description — at least 300 characters of
   real instruction text, with no short title. For example, ask it to delegate a task
   described in one long run-on paragraph.
3. Look at the activity strip.

**Expected.**
- A row appears. Its label may be shortened with an ellipsis — that is correct.
- A row appears **immediately**, not only after a page reload.

**Screenshots.** (a) the strip right after sending; (b) the strip after a reload.

**PASS** if the row is present both before and after reload.
**FAIL** if no row appears until you reload. *A long description used to make the
row vanish entirely for the whole run, then appear on reload.*

---

## Lane G — Skills and restart recovery (~25 min)

### G1 — Asking for a skill the target does not have fails the call

**Setup.** Agents `uat-g-boss` and `uat-g-worker`. Ensure `uat-g-worker` does **not**
have some named skill that exists in the workspace.

**Steps.**
1. Open a chat with `uat-g-boss`.
2. Instruct it to delegate while requiring that skill: `Ask uat-g-worker to do this task and require it to use the <name> skill.`
3. Read the outcome.

**Expected.**
- The delegation **fails** with a message saying the skill is not available to the
  target.
- The worker does **not** quietly run without the skill.

**Screenshots.** The failure message.

**PASS** if the call fails and says why.
**FAIL** if the worker runs anyway. *The tool promised "the whole call fails instead
of silently proceeding" while doing exactly the opposite.*

**Note.** If the UI gives no way to request a skill, record "not reachable from the
UI" — that is a legitimate finding about the feature's reachability.

### G2 — moved

Server-restart recovery now lives in **Lane I** alongside the other interruption
cases, so one agent owns every "something went away mid-flight" scenario.

---

## Lane H — One account never sees another's work (~20 min)

**Setup.** **Two separate accounts** in two isolated browsers — call them Account 1
and Account 2. This lane exists to prove isolation, so never share a browser profile,
cookie jar, or login between the two.

### H1 — Approval prompts stay with their owner

**Steps.**
1. In Account 1, start a delegation whose worker will ask for approval (a tool that
   needs confirmation, or a question back to the user).
2. While Account 1 shows the prompt, switch to Account 2 and open its own chat.
3. Look at Account 2's screen carefully: chat, activity strip, notifications.

**Expected.** Account 2 shows **nothing** about Account 1's worker — no prompt, no
row, no notification.

**Screenshots.** (a) Account 1 showing the prompt; (b) Account 2's full screen at the
same moment.

**PASS** if Account 2 shows no trace of Account 1's activity.
**FAIL** if anything from Account 1 appears in Account 2. Report as **critical** and
stop the lane — a cross-account leak outranks every other result in this plan.

### H2 — Worker rows stay with their owner

**Steps.**
1. In Account 1, start two delegations and leave them running.
2. In Account 2, open its own chat and inspect the activity strip.

**Expected.** Account 2's strip is empty, or shows only Account 2's own work.

**PASS** if no cross-account rows appear.
**FAIL** if any of Account 1's workers are visible to Account 2. Critical.

---

## Lane I — Recovery from interruptions (~35 min)

The question every scenario here asks: **when something goes away mid-flight, does
the work come back, or does it die quietly?** A worker left `running` forever with
nothing behind it is the worst outcome — worse than an honest failure, because
nobody knows to retry it.

Run this lane **last**, or on a dedicated instance: I1 and I4 disturb the whole
server.

**I3 (provider failure) is deliberately excluded from this run** — see below.

### I1 — The server restarts while workers are running

**Setup.** Agents `uat-i-boss`, `uat-i-worker`. **Requires someone who can restart
the Omnipus server** — coordinate first, never restart a shared server unannounced.

**Steps.**
1. Start a long delegation: `Ask uat-i-worker to write a detailed 10-paragraph report on the history of printing.`
2. Wait until the worker row shows `running`.
3. Have the server restarted.
4. Reload the page, reopen the chat, watch for 3 minutes.

**Expected.** Each worker either **resumes and finishes**, or is clearly marked
`failed` / `cancelled` with a visible reason. Nothing sits at `running` forever.

**Screenshots.** (a) running before restart; (b) 30 seconds after reload; (c) 3
minutes after reload.

**PASS** if every worker resumes or is honestly reported.
**FAIL** if a row is stuck `running` with nothing behind it, or a worker vanishes
with no trace. A vanished worker is the more serious of the two.

### I2 — The tester's internet connection drops

Tests the **browser's** connection, not the server's. The work runs on the server,
so losing the browser should not lose the work.

**Setup.** Agents `uat-i2-boss`, `uat-i2-worker`.

**Steps.**
1. Start a delegation that takes a minute or two.
2. With the worker `running`, **disconnect the browser's network** (turn off Wi-Fi,
   or use the browser's offline mode). Keep the tab open.
3. Wait 60 seconds.
4. Reconnect. Do **not** reload yet — watch for 30 seconds first.
5. If nothing recovers on its own, reload and look again.

**Expected.**
- While offline, the UI may show a disconnected or reconnecting indicator. That is
  correct.
- After reconnecting, the activity strip shows the worker again with an up-to-date
  state — whether or not you reloaded.
- The work **continued on the server** while you were offline: a worker that would
  have finished during that minute shows `completed`, not `running`.

**Screenshots.** (a) running before disconnect; (b) the UI while offline; (c) 30s
after reconnect, before any reload; (d) after reload.

**PASS** if the worker's true state is visible after reconnecting and the work was
not interrupted by the browser going away.
**FAIL** if the work stopped because the browser disconnected, if the strip is empty
after reconnecting, or if the UI still shows `running` for something that actually
finished.

### I3 — removed: provider-failure recovery is deferred

**Do not test provider failure in this run.** The scenario was written, then pulled
before anyone ran it.

Checking what the product actually does revealed that a delegated worker **cannot**
retry a provider rate limit: the retry's enabling condition has had no production
writer since the sub-turn mechanism was deleted. The code, its retry limit and its
tests all still exist — the tests pass only because they hand-build a state that
production no longer produces.

So the scenario would fail for a cause already known and already filed
(**issue #857**). Running it would spend a tester's time to rediscover a logged bug,
and — worse — would put a FAIL in this report that looks like an ADR-091 regression
when it is a separate defect with its own fix.

**Restore this scenario once #857 is fixed**, and run it then. The behaviour is worth
testing; it just cannot pass today.

### I4 — A nested worker survives an interruption

The hardest case: an interruption two levels down, where the recovery has to travel
back up a chain.

**Setup.** Agents `uat-i4-l1`, `uat-i4-l2`, `uat-i4-l3`; l1→l2→l3.

**Steps.**
1. From a chat with `uat-i4-l1`, start a job that reaches all three levels and takes
   a few minutes.
2. Once the deepest worker is `running`, interrupt — restart the server (I1) or drop
   the provider (I3), whichever you can arrange.
3. Reload and watch all three levels for 5 minutes.

**Expected.** Every level reaches a sensible state. Either the chain resumes and the
answer arrives at the top, or each level reports a clear failure — including the
**top-level chat**, which must not be left waiting on a child that will never reply.

**Screenshots.** (a) all three running; (b) 1 minute after; (c) 5 minutes after,
including the top-level chat.

**PASS** if the top-level chat ends up with an answer or an explanation.
**FAIL** if the top-level chat waits forever while a lower level is already dead.
*This is the exact shape of the bug this ADR set out to fix — a chain where one
level dies and its ancestors wait for ever.*

---

## Lane assignment summary

| Lane | Covers | Needs | Time |
|---|---|---|---|
| P | Pre-flight, record settings | 1 account | 10 min |
| A | Delegation end-to-end, containment | 1 account, 2 agents | 20 min |
| B | Three-level nesting, depth limit | 1 account, 7 agents | 25 min |
| C | Stop on running and on queued | 1 account, 3 agents | 20 min |
| D | Revive with a new instruction, step limit | 1 account, 4 agents | 25 min |
| E | Concurrency queue and ordering | 1 account, 5 agents, settings change | 20 min |
| F | Panel visibility, reload, long label | 1 account, 5 agents | 20 min |
| G | Skill refusal | 1 account, 2 agents | 10 min |
| H | Cross-account isolation | **2 accounts, 2 browsers** | 20 min |
| I | Recovery: restart, network drop, nested interruption | restart access | 25 min |

Lanes A–I are independent. Lane P should complete first because B and E need the
numbers it records. **Lane I restarts the server — run it last, or against a
dedicated instance**, or it will corrupt every other lane's result.

---

## Known limitations — do not report these as bugs

1. **The worker's detail is behind a click.** Its internal steps living in the
   expanded row rather than the main chat is the intended design (see A2).
2. **Shortened labels.** A long task shown with an ellipsis is correct. A *missing*
   row is the bug.
3. **`interrupted` vs `cancelled`.** A stopped worker may read `cancelled` on the row
   and `interrupted` in the detail. Both mean stopped.
4. **A worker may finish before hitting its step limit** (D2). Record "limit not
   reached" rather than forcing it.
5. **Queue position may not update every second.** Slightly stale ordering is not a
   bug; a queued worker that never starts is.
6. **The 30-minute lifetime cap covers the whole life of a delegated session.** A
   follow-up that wakes it does **not** restart the clock — a worker woken repeatedly
   over 30 minutes may still time out. That is intended.
7. **Provider-failure recovery is not in this run.** It is deferred to issue #857;
   see I3. Do not test it and do not report it.
8. **Timings in this plan are guidance**, except the explicit 3-minute no-progress
   rule, which is a real FAIL condition.

---

## Reporting

Per lane, report:

- Lane letter, account used, and when you ran it.
- Per scenario: **PASS / FAIL / BLOCKED**, and for anything not PASS, exactly what you
  saw with the screenshot filename.
- Anything you could not test and why. **"Blocked" is a legitimate result** — a
  fabricated PASS is not.
- Anything surprising that no scenario asked about.

Do not fix anything. Do not retry until green. One clean run, then report.
