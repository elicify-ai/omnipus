# UAT Report — ADR-057 Batch 2: Communication Tools (steer / respond / inbox / inbox_ack / message_parent)

**Tester:** UAT-2 (one of four parallel UAT agents)
**Target:** `https://omnipus-uat-swimlane.fly.dev` (commit `acd0d0af`)
**Date:** 2026-08-03
**Scope:** `delegate(action="steer")`, `delegate(action="respond")`, `delegate(action="inbox")`, `delegate(action="inbox_ack")` (incl. `since_cursor`/`max`), `message_parent` (child→parent), plus the "Always Allow survives across sibling delegations" regression check.
**Plan followed:** `docs/internal/uat/uat-plan-delegation-tools-v2-2026-07-31.md` (TC-11 through TC-14, TC-11b/c/d, TC-12b/c, TC-13b/c, TC-14b/d, EC-11), reassigned per this run's task brief (steer treated as "Communicate" category, not Suite D).

## Verdict

**FAIL — one confirmed CRITICAL defect (`respond`/`message_parent(wait=true)` parking), one confirmed MAJOR defect (`inbox_ack` false-success count), everything else in scope PASSES with real, REST-verified evidence.** The headline "Always Allow survives across sibling delegations" regression check **PASSES**.

| Result | Count |
|---|---|
| PASS | 11 |
| FAIL | 3 (1 CRITICAL, 2 same-root MAJOR/CRITICAL cluster counted separately below) |
| BLOCKED | 0 (all ambiguous results were resolved by retesting on a quiet box, per coordinator instruction) |

---

## Methodology & Limitations (read first)

- **The Playwright MCP browser in this devpod is shared across all four concurrent UAT agents** (same browser process/cookie jar/tabs) — confirmed directly: the onboarding wizard advanced through steps 1→2→3 and a login form filled itself in without any input from me, and both tabs bounced to `/#/login` with 401s on `/auth/validate` for ~20s before recovering. **I abandoned browser-driven testing entirely after this was confirmed** (both by my own observation and independently by two siblings) and switched to a from-scratch REST + raw WebSocket harness (Python `websockets`, own bearer token via `POST /auth/login`, own cookie-free HTTP client) that is **genuinely isolated per agent** — no findings in this report rest on browser observation.
- **How I drove the system under test:** `delegate`/`steer`/`respond`/`inbox`/`inbox_ack`/`message_parent` are agent-side tools, not REST endpoints — there is no way to invoke them directly. I drove Jim through a dedicated WS chat session (`session_01KZ4VRKCP1CDYNEV8S28M40XT`, mine alone, created via `MessageFrame{agent_id:"jim"}`) with precise, unambiguous instructions ("call delegate(action=..., ...) now, do not ask me for confirmation"), then **verified every claimed effect independently via `GET /api/v1/sessions/{id}` with my own bearer token** — never trusting a tool's return string alone. All session/task ids cited below are ones I created in this run.
- **System-wide capacity gate encountered and resolved.** For roughly 10 minutes mid-run, every chat turn (including a brand-new session with zero history) was rejected with "I'm at capacity right now." The coordinator confirmed via live gateway logs this was `al.admission.TryAdmit` saturated at `active:4/soft_cap:4` — four concurrent UAT agents each holding a session scope, not a product bug. I paused, waited for load to clear, and **re-ran the affected tests (steer A/B differentiation, respond parking) on a quiet box** to separate genuine defects from this confound. Both re-runs are reported below with the confound explicitly noted where it matters.
- **One session (`1d66cb92-a4a2-4b65-ba95-4426a351e99c`) never recovered** even after load cleared — see the TC-14 secondary finding. This is reported as a standalone, lower-confidence observation, clearly separated from the confirmed PASS.
- A WS `MessageFrame` that omits `agent_id` on a follow-up (continuing an existing session) silently reroutes to Mia instead of the session's real agent (`pkg/gateway/websocket.go:1246-1270` computes `targetAgentID` before checking whether the session already has one; `pkg/agent/loop.go:6492-6586`'s `resolveMessageRoute` never consults the session's persisted `ActiveAgentID`). **Confirmed NOT reachable through the real SPA** — `src/store/chat.ts` always resends `agent_id: activeAgentId` on every outgoing frame — so this is a latent code-path issue only exploitable by a raw WS client like mine, not a live product bug. Noted for completeness, not counted in the PASS/FAIL tally.
- A peripheral bug in `delegate(action="status")`'s `session_id` resolution (misleading "No subagent found with task ID: ..." error, confirmed via code read of `pkg/tools/delegate.go:1996-2097`) is **Monitor category, UAT-1's lane** — flagged to the coordinator, not scored here.

---

## BDD Traceability

| Test | Description | Result | Evidence |
|---|---|---|---|
| TC-14 | steer — mid-run instruction injection | **PASS** | A/B differentiation test, see below |
| TC-14 (secondary) | steer effect verifiability under load | **OBSERVATION** (not scored FAIL — see below) | session `1d66cb92` |
| TC-14b | steer — empty text | **PASS** | clear rejection |
| TC-14d | steer — on completed session | **PASS** | clear rejection |
| EC-11 | two steers in quick succession | **PASS** | both applied, no crash |
| EC-10 | steer+cancel race | **NOT RUN** | `cancel` is UAT-3's tool, out of scope |
| TC-13 | respond — answer a question | **FAIL (CRITICAL)** | reproduced twice, see below |
| TC-13b | respond — invalid correlation_id | **PASS** | clear error |
| TC-13c | respond — no question pending | **PASS** | clear error |
| TC-11 | inbox — drain child messages | **PASS** | real messages, correct attribution/ordering |
| TC-11b | inbox — `max` param | **PASS** | exactly 1 returned |
| TC-11c | inbox — `since_cursor` incremental drain | **PASS** | no dupes, no gaps |
| TC-11d | inbox — empty (no messages) | **PASS** | `messages: null` |
| TC-12 | inbox_ack — acknowledge messages | **PASS** (main case) | acked messages excluded from later drain |
| TC-12b | inbox_ack — invalid message_ids | **FAIL (MAJOR)** | false "Acknowledged 1" for a nonexistent id |
| TC-12c | inbox_ack — mixed valid+invalid | **FAIL (same bug)** | false "Acknowledged 2" when only 1 was real |
| message_parent | child→parent attribution | **PASS** | tested indirectly via TC-11/TC-13, correct attribution confirmed |
| Always Allow regression | grant survives across sibling delegations | **PASS** | see dedicated section |

---

## FAIL #1 (CRITICAL): `message_parent(kind="question", wait=true)` does not park the session; `respond()` correctly refuses, leaving the child permanently stuck

**What the tool documents:** `message_parent`'s own schema description (returned live by `load_tool`) says: *"wait=true parks THIS session in needs_input awaiting an answer (native sessions only)."*

**What actually happens (reproduced twice — once under heavy load, once on a quiet box, per coordinator instruction to rule out the capacity confound):**

Trial 2 (clean run, quiet box, async dispatch): I delegated to `worker` with the explicit instruction *"Ask your parent ... using message_parent with wait=true. Do not attempt to answer it yourself or search for it — just ask and wait."* Child session `61783e3d-dc0d-4f3f-998c-599cb376ef0a`:

1. `worker-turn-270` calls `message_parent(kind="question", text="What is the magic number for UAT2_TC13B?", wait=true)`, then its own assistant text says *"this session is parked until an answer comes back."*
2. ~20 seconds later, **in direct contradiction of both its own statement and my explicit instruction**, the same session runs a **new** turn (`worker-turn-271`) with **13 more tool calls**: `recall_memory` ×2, `bash` (grep/pwd/ls) ×5, `list_directory`, `bash` (git log/status/branch) ×2, etc. — actively searching for the answer on its own.
3. I confirmed via `delegate(action="inbox", session_id=<child>)` that the question message and its `correlation_id` (`6c3f59c4-2ece-4f37-88f9-b090f8143e1c`) were correctly delivered to Jim's inbox with correct attribution (`sender_identity: "worker"`, matching `session_id`).
4. I called `delegate(action="respond", session_id=<child>, correlation_id="6c3f59c4-...", text="42")`. Result: **`"delegate: respond: session 61783e3d-... is not parked on correlation_id \"6c3f59c4-...\""`** — a clean, correct rejection, because the session's real lifecycle state genuinely was not `needs_input` (matches the tool's own fail-closed contract at `pkg/tools/delegate.go` — `rec.State == session.LifecycleNeedsInput` check).
5. **Effect confirmed via REST:** the child never received "42". Its final message: *"I've searched everywhere I can reach for 'UAT2_TC13B' and 'magic number' ... I can't answer this question from anything available to me ... Could you tell me where the magic number ... is supposed to be defined?"* The session remains `status: "active"` indefinitely — genuinely stuck, with **no way to unblock it via `respond()`** because it never actually parked.

Trial 1 (under load, `async=false`) reproduced the identical shape independently: the sync delegate call returned early with *"I'm parked awaiting a reply"* while the child kept running a new turn in the background, and `respond()` again rejected with "not parked."

**Why this is CRITICAL:** `respond()` is documented to "resolve the child's blocking question and unblock that child." In both trials it never could, because the upstream parking mechanism it depends on (`message_parent(wait=true)`) does not actually pause the session's turn loop — the session simply keeps running a fresh turn instead of blocking. This is a genuine contract violation of a documented, security-relevant primitive (`authority: "owner_required"` questions are meant to force a real pause pending owner input), not a capacity artifact — confirmed by reproducing it on a quiet box.

**`respond()`'s own behavior was correct in every trial** — it never returned a false "success" for an unparked session; it always failed closed with a specific, accurate error message. The defect is entirely in the parking mechanism upstream, not in `respond()` itself.

---

## FAIL #2 (MAJOR): `inbox_ack` reports a false success count for nonexistent message IDs

**TC-12b:** `delegate(action="inbox_ack", session_id=<real child>, message_ids=["fake-id-12345"])` — a completely fabricated message ID that never existed — returned:

```
"Acknowledged 1 message(s)."
```

Per the plan's own pass criterion ("Clear error or graceful no-op. No crash.") this is **neither** — it's a **false success claim**. The count implies something real was acknowledged; nothing was.

**TC-12c** confirmed the pattern: `inbox_ack(message_ids=["2c865e37-...(real, unacked)", "fake-id-99999"])` returned `"Acknowledged 2 message(s)."` I then verified the actual effect via a follow-up `inbox()` call: the real message ("4") **was** correctly excluded going forward (so the valid half of the batch genuinely worked), but the reported count (2) does not match reality (only 1 was real).

**Root cause (behavior-inferred, not yet code-confirmed):** the reported count appears to equal `len(message_ids)` requested, not the count actually found/acked — the tool never validates existence before counting, silently masking not-found IDs as if they succeeded.

**Why MAJOR, not CRITICAL:** no data loss, no stuck state, no security boundary crossed — real acks do work correctly. But this is squarely the "success string that isn't evidence" defect class this release exists to eliminate: a caller (human or agent) acting on the returned count would believe a message was acknowledged when it never existed. Matches the ENV-06 rubric's P1 "Wrong result returned."

---

## PASS: TC-14 (steer) — differentiation test, real content verified

Dispatched two identical long-running children from the same Jim turn (`delegate(action=run, async=true, launch_profile=utility, task="sleep 20 && echo CONTROL_{A,B}_DONE")`):
- **A** (`session 5073b52e-4ca4-4119-b854-e4d228124f64`) — steered with `delegate(action="steer", session_id=A, text="Before finishing, also output the exact word STEERED_MARKER_CTRL on its own line")`.
- **B** (`session fb478363-c24c-401b-a72b-e60d3abe7fc1`) — left completely untouched, as a control.

After capacity cleared, `GET /api/v1/sessions/{id}` (my own bearer token) shows:
- **A's final message:** `"STEERED_MARKER_CTRL\n\nThe command output was:\n\nCONTROL_A_DONE"` — the steered instruction landed verbatim.
- **B's final message:** `"The command completed successfully. Output:\n\n\`\`\`\nCONTROL_B_DONE\n\`\`\`\n..."` — no marker, normal completion.

This is a genuine differentiation test (two identical setups, one treatment difference, two different real outputs) — it proves `steer` reaches the **specific named child** and injects real content, not a hardcoded or no-op response.

**TC-14b** (empty text): `delegate(action="steer", ..., text="")` → `"text is required and must be a non-empty string"`. Clean rejection.

**TC-14d** (steer on lifecycle-terminal session): re-steering the now-`completed` session A → `"delegate: steer: session 5073b52e-... is terminal (completed) and cannot be steered"`. Clean rejection.

**EC-11** (two steers in quick succession, same turn): dispatched a fresh child (`session 88c5e001-a93d-4792-88a8-5f0edbe6858b`, `sleep 15 && echo EC11_DONE`), then in one Jim turn issued `steer(text="Output the word FIRST_STEER...")` immediately followed by `steer(text="Output the word SECOND_STEER instead...")`. Final content (REST-verified): `"FIRST_STEER\nSECOND_STEER"` — **both** were queued and applied, no crash, session reached terminal state cleanly. Documented actual behavior: both-queued, not last-wins.

**EC-10** (steer+cancel race) was not run — `cancel` belongs to UAT-3's Control-category lane per this run's scope split.

### TC-14 secondary observation (not scored as a confirmed defect — reported for completeness)

The **first** TC-14 test child (`session 1d66cb92-a4a2-4b65-ba95-4426a351e99c`, steered with `"STEERED_MARKER_TC14"`) never recovered even after system load cleared — unlike A/B above. `delegate(action="peek")` reported `state: "completed"` (checked twice, ~15 minutes apart) while `GET /sessions/1d66cb92-...` stayed frozen at 2 messages (no final turn ever recorded) the entire time, and steering it now correctly errors "terminal, cannot be steered" (so it will never get another chance to run). This is a live example of a lifecycle store reporting "completed" with no corresponding transcript effect — genuinely worth investigating — but because the A/B control test (dispatched later, under the same load) recovered cleanly once capacity freed up, I cannot rule out that this specific instance was an artifact of the exact moment of peak saturation (an orphaned-span reconciliation race, not a `steer`-specific defect). **Reporting as an observation, not a scored FAIL**, since I could not reproduce it on demand.

---

## PASS: TC-11 / TC-11b / TC-11c / TC-11d (inbox)

- **TC-11:** `delegate(action="inbox", session_id=<specialist child>)` returned real messages, each with `kind`, `message_id`, `sender_identity` (correctly `"worker"`), `session_id` (correctly matching the child), and monotonically increasing `created_at` timestamps (question at `22:43:54.864`, progress at `22:43:56.958`). Confirms `message_parent` → inbox attribution end-to-end.
- **TC-11b (`max`):** `inbox(session_id=..., max=1)` on a child that had sent 5 progress messages returned exactly 1 (`text:"1"`, `pct:20`), `has_more:true`, `next_cursor:"4"`.
- **TC-11c (`since_cursor`):** `inbox(session_id=..., since_cursor="4")` returned exactly the 4 remaining messages (`text: "2","3","4","5"`) — zero duplication of the already-drained message 1, zero gaps.
- **TC-11d (empty):** a utility-profile child (no checkpoint messaging) → `inbox()` returned `{"has_more":false,"messages":null,"next_cursor":"11"}` — no error, no crash. (Note: `next_cursor` reflects the shared per-owner JSONL file's global entry count across all children, not a per-child counter — consistent with the one-file-per-owner-key architecture, not a bug.)

## PASS: TC-12 main scenario (inbox_ack real messages)

`inbox_ack(session_id=<child>, message_ids=[<3 real ids>])` → `"Acknowledged 3 message(s)."`, then a subsequent `inbox()` with no `since_cursor` correctly excluded all 3, returning only the 2 remaining unacked messages. Real state change confirmed via REST re-check, not just the return string.

## PASS: TC-13b / TC-13c (respond edge cases)

- **TC-13b:** `respond(session_id=<same broken child>, correlation_id="fake-corr-id-99999", text="x")` → `"... is not parked on correlation_id \"fake-corr-id-99999\""`. Clear, specific, no crash.
- **TC-13c:** `respond(session_id=<unrelated, already-completed control session B>, correlation_id=B, text="x")` → same "not parked" error family, no side effects on the target session.

---

## PASS (highest-value check): "Always Allow" survives across sibling delegations

This is the fix landed this session (`recordGrantOnDelegationParent`, `pkg/gateway/rest_tool_registry.go:557-587`) — previously the grant was filed under the acting child's session id (which never recurs), so the UI toasted success but the user was re-prompted forever.

**Test:**
1. Confirmed via `GET /api/v1/agents/worker/tools` that `run_task` is configured `ask` for the `worker` agent (not a global default — checked the agent's own effective policy).
2. Created two real tasks (`TASK1`, `TASK2`) via `create_task` (policy `allow`, no prompt).
3. Delegated **CHILD1** (`session a38e03dd-72f8-49cd-8dcc-9748e7289b76`) to call `run_task(task_id=TASK1)`. Caught the live approval via a fresh WS connection's `session_state.pending_approvals` push: `{approval_id: "e9afc9f8-9ce6-4e57-9439-c4c4f5dda499", session_id: "a38e03dd-...", tool_name: "run_task", agent_id: "worker"}`.
4. Resolved via **REST**, `POST /api/v1/tool-approvals/e9afc9f8-... {"action":"always"}` → `200 {"status":"ok"}`.
5. Verified CHILD1's `run_task` actually executed past the gate (`GET /sessions/a38e03dd-...` shows a real `run_task` tool_call, `status:"error"` only because TASK1 was already `done` — i.e. it reached real execution, was not blocked).
6. Delegated **CHILD2** (`session 703b6ec3-1cd9-4ae9-bad1-07a3e8507521` — a **different, brand-new sibling delegation** under the same parent Jim session) to call `run_task(task_id=TASK2)`.
7. Opened a fresh WS connection immediately after dispatch: `session_state.pending_approvals: []` — **no new prompt**.
8. `GET /sessions/703b6ec3-...` confirms `run_task` tool_call `status:"success"` with **no approval interruption anywhere** in the transcript.

**Conclusion:** the grant recorded during CHILD1's approval correctly carried over to CHILD2 — a distinct session, a distinct delegation — without re-prompting. This is exactly the previously-broken scenario (grant filed under a non-recurring child id) now fixed. Effect confirmed via REST at every step, never via the approval endpoint's return string alone.

---

## Shortcut/no-op detection summary (per the QA brief's explicit ask)

- **No hardcoded or no-op response was found in this lane's happy paths.** The steer A/B differentiation test is the clearest proof: identical setup, one variable (steered vs not), two genuinely different real outputs.
- **One confirmed "success string that isn't evidence" defect was found:** `inbox_ack` reporting `"Acknowledged N message(s)"` where N counts *requested* ids, not *actually-acked* ids — a fake id silently inflates the success count. This is precisely the defect class this UAT round was designed to catch, and it would have been invisible if I had trusted the return string instead of re-querying `inbox()` afterward.
- **`respond()` never produced a false success** — every rejection was a specific, accurate error tied to real state. The defect there is upstream (`message_parent(wait=true)` not parking), not in `respond()`'s own logic.
- **`delegate(action="peek")` reporting `state:"completed"` for a session whose transcript never received a final message** (the TC-14 secondary observation) is a borderline case of exactly this pattern — reported honestly as unconfirmed/unreproduced rather than inflated to a scored FAIL.

## Gaps

- EC-10 (steer+cancel race) not run — requires `cancel`, out of this lane's scope (UAT-3).
- The TC-14 secondary "peek says completed but transcript frozen" observation on session `1d66cb92` could not be reproduced on demand; flagging for follow-up investigation rather than scoring.
- 3P (external-CLI) agent behavior for any of these tools was not tested — no 3P agent configured in this workspace.

## Issues found (for tracking)

1. **CRITICAL** — `message_parent(kind="question", wait=true)` does not park the session in `needs_input`; the session continues running a new turn instead. Consequence: `delegate(action="respond")` can never unblock such a child (correctly fails closed, but the child is permanently stuck). Reproduced twice, once with the capacity confound ruled out.
2. **MAJOR** — `delegate(action="inbox_ack")` reports `"Acknowledged N message(s)"` using the count of *requested* message_ids rather than the count *actually found and acknowledged*, producing a false-success result for nonexistent IDs.
3. **Peripheral, not scored (UAT-1's lane, Monitor category)** — `delegate(action="status")` with a valid `session_id` intermittently returns a misleading `"No subagent found with task ID: <stale id>"` error (`pkg/tools/delegate.go` ~line 2029) instead of a session-aware error; flagged to the coordinator for UAT-1.
3b. **Peripheral, not scored, not reachable via SPA** — WS `MessageFrame` omitting `agent_id` on a follow-up reroutes to the default agent (Mia) instead of the session's real agent; confirmed the real SPA always resends `agent_id`, so not a live product bug.
