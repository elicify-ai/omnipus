# UAT Report — Batch 3: ADR-057 Cancel/Control Surface (UAT-3)

**Verdict: MIXED — core cancel mechanics (soft/hard cancel, over-reach isolation, background-bash-kill, chat-wide Stop) all PASS with independently-verified evidence; `follow_up` on a terminal session is CRITICALLY BROKEN (reports success, does nothing); the single highest-priority claim in scope — cascade-to-grandchild — is BLOCKED (untestable in this workspace's configuration, not confirmed either way).**

**Counts:** 8 PASS · 2 FAIL (CRITICAL) · 1 BLOCKED (untestable precondition)

| # | Test | Verdict | Severity |
|---|------|---------|----------|
| TC-15 | Soft cancel (`hard=false`) | PASS | — |
| TC-15b | Hard cancel (`hard=true`) | PASS | — |
| TC-15c | Cancel on already-terminal session | PASS | — |
| Brief #3 | No over-reach (sibling + parent untouched) | PASS | — |
| Brief #1 | Cancel cascades to grandchild | **BLOCKED** | — (see below) |
| Brief #2 | Cancel kills child's background bash | PASS | — |
| Brief #4 | Chat-wide Stop reaches delegated sub-turn | PASS (1 caveat) | caveat: MINOR |
| TC-16 | `follow_up` warm resume of completed session | **FAIL** | **CRITICAL (P1)** |
| TC-16b | `follow_up` on cancelled/failed session | **FAIL** | **CRITICAL (P1)** |
| TC-16c | `follow_up` on running session | PASS | — |

Target: `https://omnipus-uat-swimlane.fly.dev` (commit `acd0d0af`). Tester account: `uat1tester` (shared UAT account, onboarded by a sibling — see Methodology). Workspace: `01KZ4TWYRZ4CVVS81PP2XJ51QS`. All session IDs below were created by me during this run.

---

## Methodology & Limitations (read this first)

The coordinator confirmed mid-run that **the Playwright MCP browser is a single shared instance across all four UAT agents** — same process, same cookie jar, same tabs. I observed this directly and unprompted before the coordinator's message: while filling the onboarding wizard, the username field I had typed was overwritten mid-flow by a sibling's input ("uat1tester"), and the wizard silently reset to step 1. I reported this to the coordinator (`main`) as soon as I saw it, and disclosed the credential the working account ended up with.

**Consequence for methodology:** after that discovery, I performed **zero** functional verification through the shared browser. Instead I built an isolated test harness in my own process:

- A Python WebSocket client (`/tmp/.../scratchpad/wsclient.py`) that authenticates with my own bearer token (`POST /api/v1/auth/login` as `uat1tester`) and speaks the chat WS protocol directly (`wss://.../api/v1/chat/ws`) — `auth`/`message`/`cancel`/`attach_session` frames per `contracts/asyncapi.yaml`. This is genuinely isolated: my own TCP connection, my own token, my own session IDs.
- Independent verification via `curl` against REST (`GET /api/v1/sessions/{id}`) with the same token, for every claimed effect.
- A dedicated script (`ws_stop_test.py`) that sends a blocking delegate instruction to Jim and fires a raw `cancel` WS frame mid-turn — this **is** the literal wire-level mechanism the UI's "Stop" button invokes (confirmed against `CancelFrame`'s schema/description), so testing it this way is at least as rigorous as clicking the shared button, and avoids any risk of measuring a sibling's browser activity instead of my own.

**Every PASS/FAIL below rests on REST or WS evidence I captured myself, scoped to session IDs I created in this run**, never on a UI observation from the shared browser. The only browser interaction I performed was the unavoidable one-time onboarding step before I had a token (documented above), which produced no test evidence used in this report.

One process-note unrelated to my findings: an earlier coordinator broadcast referenced "your earlier login guesses (seven attempts across seven usernames)" — that was not something I did; I performed exactly one onboarding attempt (username "admin") before the shared wizard was overwritten by a sibling. I flagged this discrepancy to the coordinator; it doesn't affect any finding here.

---

## Detailed Findings

### TC-15 — Soft cancel (`hard=false`) — PASS

**Setup:** Dispatched `delegate(target_agent_id="worker", async, launch_profile="specialist", task="sleep 180 && echo LONG_RUN_COMPLETE_SOFT2")` → child session `6ca2f478-81a8-4c80-a58b-1a0f837cee5d`. Confirmed actively running: REST message count grew 32 → 72 in 13s (worker was polling its own background bash in a loop).

**Action:** `delegate(action="cancel", session_id="6ca2f478...", hard=false)` at 22:28:25 UTC.

**Tool's own claim:** `"Session 6ca2f478... cooperatively cancelled; a checkpoint flush is expected within 5s, after which a hard cancel backstop fires if it has not stopped on its own."`

**Independent proof of effect (not just the toast):**
- `GET /sessions/{id}` polled 6× over 15s post-cancel: message count frozen at **99**, `updated_at` frozen at `22:28:29.37Z` (≈4s after the cancel call — consistent with the documented grace window, not instant, not never).
- Final transcript message, written by the child itself: *"A background bash session (fcae9f45) was canceled. No output was produced before it was terminated."*
- Session never reached `LONG_RUN_COMPLETE_SOFT2` (would have needed 180s; cancel fired at ~40s in).

### TC-15b — Hard cancel (`hard=true`) — PASS

**Setup:** Same round, child `725f1b73-3aed-4762-a21c-c027b1cf990b`, confirmed running (242 messages, actively growing).

**Action:** `delegate(action="cancel", session_id="725f1b73...", hard=true)` at 22:29:35 UTC. Tool's claim: `"Session 725f1b73... hard-cancelled immediately."` — this is the **exact phrasing the brief warned is not itself evidence**, so I did not accept it at face value.

**Independent proof:**
- REST polled 8× over 15s: message count frozen at **261**, `updated_at` frozen at `22:29:37.5Z` (≈2s after the call — faster than the soft-cancel case, consistent with "no grace window").
- Final transcript message (child's own): *"A background bash session (7592cbb5) was canceled. No output was produced."*

Hard cancel is measurably faster than soft cancel (2s vs 4s to freeze) and both genuinely stop real work, not just change a label.

### TC-15c — Cancel on an already-terminal session — PASS

Tested twice:
1. Against a session that finished **naturally**: `"delegate: cancel: session 2c9a6431... is already terminal (completed) — nothing to cancel"`.
2. Against a session that had been **cancelled** moments earlier (double-cancel, `08f44a1a...`): `"delegate: cancel: session 08f44a1a... is already terminal (cancelled) — nothing to cancel"`.

Both are clear, specific, non-crashing errors — and notably the tool **correctly differentiates the terminal reason** ("completed" vs "cancelled"), which is good evidence the cancel-path reads real durable state rather than a canned string.

### Brief item #3 — No over-reach: sibling and parent untouched — PASS

**Setup:** Dispatched two siblings from the same parent turn: `SIBA = 40fcbdf7-0c5d-412f-949b-93a7f796fbe2`, `SIBB = d213f241-04dc-49db-afda-d61a02606e0e` (both `sleep 60`, both confirmed actively producing output: 21 and 22 messages respectively after 4s).

**Action:** `delegate(action="cancel", session_id=SIBA, hard=true)` **only**.

**Independent proof, polled every 5s for 40s:**

| t | SIBA (cancelled) | SIBB (untouched) |
|---|---|---|
| +15s | 31 (frozen) | 58 |
| +20s | 31 | 64 |
| +25s | 31 | 70 |
| +30s | 31 | 75 |
| +35s | 31 | 81 (flatlined — natural completion) |

SIBB's final message: *"The background session `67ca3416` completed successfully (exit code 0)"* — it finished **on its own**, never touched by SIBA's cancel. SIBA's final message: *"background session `a95668f2` was canceled. No output was produced before it was stopped."*

Parent (Jim's own session) survived every cancel action in this entire run — I made dozens of further successful calls on the same parent session after each cancel, throughout the whole test session, with no degradation.

### Brief item #1 — Cancel cascades to a grandchild — **BLOCKED**

This is the core ADR-057 claim the brief calls out as most important, and I could not construct the precondition needed to test it: **a real 2-level delegation chain**. I made four independent, escalating attempts, all denied — and I verified the denials are real policy enforcement, not soul-level politeness:

1. `worker → worker` (self-delegation): denied — *"the system does not permit an agent to delegate to itself."*
2. `worker → explorer`: denied — *"the policy in this workspace does not permit me to delegate to agent 'explorer' (or any other agent)... You cannot delegate to other agents in this workspace — complete the task yourself."* I suspected this might be a soul-level refusal rather than a hard policy block, so I re-ran it explicitly instructing the worker to attempt the tool call anyway and report the raw result. The tool call **was** attempted, and came back as a structured, real policy denial: `{"error":"delegation_denied","policy":"trust_set","reason":"delegation to agent \"explorer\" is not permitted in this workspace", ...}`. This confirms it's a genuine `trust_set` enforcement, not a refusal.
3. `jim → planner` (trying a different candidate "child" that itself has known delegation behavior): denied at Jim's own level with the same structured `delegation_denied`/`trust_set` error.
4. `jim → ava → worker`: Jim **can** delegate to Ava (accepted, no denial) — but Ava's own transcript shows: *"The `delegate` tool is denied by my agent policy — I cannot load it."* Ava has no delegate tool at all.

**Conclusion:** in this workspace's current configuration, Jim can reach `worker` and `ava` directly, but **neither of those has any further outbound delegation capability** (worker: `trust_set` denies all further edges; ava: tool itself not granted by policy). There is no agent reachable from Jim's `delegate` tool that can itself delegate again. This means the cascade-to-grandchild behavior — arguably the single most important claim in this release — is **untestable from the Control surface alone in this environment**, and I am not reporting it as PASS or FAIL. This should be escalated: either the test workspace needs a delegation edge added (e.g., `worker → explorer`) so a real chain exists, or the cascade behavior needs to be exercised via a different path (e.g., the plan/task engine, which is UAT-4's territory and may spawn `planner→explorer/researcher` through a different mechanism than the ad-hoc chat `delegate` tool).

### Brief item #2 — Cancel kills the child's background bash — PASS (rigorously verified)

This is the test the brief specifically asked for external, non-self-reported proof on, so I built exactly that.

**Setup:** Dispatched a child (`08f44a1a-a485-4d3f-b29d-0b1ebe166198`) told to run `bash(run_in_background=true, command="python3 -m http.server 18831 --bind 127.0.0.1")` and immediately report back without waiting.

**Before-cancel check**, run from **Jim's own** `bash` tool (a different process than the child's, connecting over loopback): `curl -s -o /dev/null -w "HTTPCODE=%{http_code}" http://127.0.0.1:18831/` → **`HTTPCODE=200`** — server genuinely up and serving.

**Action:** `delegate(action="cancel", session_id="08f44a1a...", hard=true)` → `"Session 08f44a1a... hard-cancelled immediately."`

**After-cancel check**, same curl from Jim, ~14s later: **`HTTPCODE=000 EXIT=7`** (`curl` error 7 = "failed to connect", i.e. connection refused). The port is genuinely freed — the process is gone, not just relabeled.

This is corroborated by the self-reports in the TC-15/15b sessions above, both of which independently mentioned their own background bash session being "canceled" with "no output produced" — consistent across soft cancel, hard cancel, and this dedicated port test.

**Coverage gap I did not close:** I verified bg-bash-kill via a *direct* `delegate(cancel)` call, and separately verified chat-wide Stop kills a *foreground* bash call (see below). I did not specifically combine the two (a child with its own `run_in_background=true` job, killed via a **chat-wide Stop** on the parent rather than a direct child-cancel). Given the shared mechanism evidence from both halves, I'd expect this to also work, but I did not prove it directly — flagging as a residual gap, not a finding.

### Brief item #4 — Chat-wide Stop reaches delegated sub-turns — PASS (with one caveat)

I could not click the actual shared-browser Stop button without risking contamination from a sibling's session (see Methodology). Instead I drove the identical wire mechanism directly: a raw `cancel` WS frame against Jim's own session_id, fired while Jim's turn was synchronously blocked on a delegation (`wait=true`).

**Sequence observed** (all from a single, clean WS connection, timestamps relative to message send):
- T+2.3s: Jim calls `delegate(target_agent_id="worker", wait=true, task="sleep 90 && echo CHATSTOP_CLEAN2_DONE_UAT3")`. `subagent_start` opens a span. Child session `2cddbae6-7989-4bb0-bd88-82ee83cc33a7`.
- T+6.2s: child starts its foreground `bash` call (`sleep 90...`, `timeout_seconds:120`).
- T+8.2s: **I send `{"type":"cancel","session_id":"<jim's session>"}`** — the literal Stop-button frame.
- T+8.2s: server replies `{"type":"cancel_stage","stage":"graceful"}`.
- T+11.2s: server escalates — `{"type":"cancel_stage","stage":"hard"}` (3s grace window, observed at the wire level).
- T+12.2s: `subagent_end` (span closes) and `done` (Jim's turn ends).

**Independent proof the child actually stopped** (not just that Jim's turn ended):
- `GET /sessions/{child_id}` polled 6× over 24s: message count frozen at **1**, `updated_at` frozen at `22:46:43.5Z` — the child's transcript never advanced past its opening line; the 90s sleep never completed and was never read back.
- A follow-up `delegate(action="cancel", session_id=<child>)` probe (the same durable-state check that correctly distinguished "completed" vs "cancelled" earlier) returned: **`"...is already terminal (cancelled) — nothing to cancel"`** — the durable record is correctly `cancelled`.
- Externally, from Jim's own `bash` tool: `ps aux | grep -i CHATSTOP_CLEAN2` and `ps aux | grep "sleep 90"` both returned **nothing** — no orphaned OS process survived the Stop.

**Caveat (MINOR, but worth fixing):** the `subagent_end` WS frame for this span reported **`"status":"success"`** — not `cancelled`/`interrupted` as the schema's own enum would suggest for this outcome (`SubagentEndFrame.status` includes `cancelled`/`interrupted`, and `reason` explicitly has a `parent_cancelled` value for exactly this case). The durable backend state is correctly `cancelled` and the process is genuinely dead, but the **live signal a user would see in the chat thread while watching this happen says "success."** A user driving a real Stop click on a real delegation would see a green/successful completion badge for work that was actually killed — this is a truthfulness gap in the live UI signal, not a functional cancel failure. I'd rate this MINOR/MAJOR (not a blocker) since the actual control works and the correct state is recoverable via the durable record — but it directly touches the "reports success while doing nothing" defect class this release is supposed to have eliminated, just at the reporting layer rather than the control layer.

### TC-16 — `follow_up` warm resume of a completed session — **FAIL, CRITICAL (P1)**

This is the one place I found the exact defect class the brief describes: **a control that reports success while doing nothing.**

**Test 1:** Child `d213f241...` completed naturally ("background session `67ca3416` completed successfully"). Called `delegate(action="follow_up", session_id="d213f241...", task="Now also return the exact string GOODBYE_UAT3_FOLLOWUP")`. Result: `"Delegated task 'UAT3_SIBB' for: ... session_id: d213f241-04dc-49db-afda-d61a02606e0e) — running in background..."` — reports success, correctly preserves the *same* session_id (matching the documented "warm resume" contract shape). Polled REST every 3s for 20s: **message count never moved past 81. No new content, no `GOODBYE_UAT3_FOLLOWUP` anywhere.**

**Test 2 (independent re-run, to rule out a one-off):** Fresh child `d18a5a07...` (short `sleep 5` task, completed in ~10s). This time I additionally opened a **live `attach_session` connection directly to the child's own session_id** *before* calling `follow_up`, and kept it open for **60 seconds** past the follow_up call specifically to catch any live re-invocation frames (not just replay-then-done). Result: the connection received the replay of the *original* turn, one `done` frame for that replay, and then **nothing else for the full 60-second window** — no new `tool_call_start`, no new assistant message, no second generation of any kind. `follow_up` again reported "success" with the same session_id and again did nothing.

Both runs are consistent and reproducible: `delegate(action="follow_up")` on a **completed** session returns a well-formed success description but never actually re-invokes the agent. This is a CRITICAL/P1 finding — functionally identical in shape to the "cancel that doesn't cancel" defect class the whole review exists to catch, except here it's "resume that doesn't resume."

### TC-16b — `follow_up` on a cancelled/failed session — **FAIL, CRITICAL (P1)**

Same pattern. Session `40fcbdf7...` (SIBA, hard-cancelled earlier). `delegate(action="follow_up", session_id="40fcbdf7...", task="Try again: return the exact string RETRY_UAT3_SIBA")` → reports success. Polled REST every 4s for 24s: **message count frozen at 31**, last message still the original cancellation note from the earlier test. `RETRY_UAT3_SIBA` never appears. Identical silent no-op as TC-16.

### TC-16c — `follow_up` on a running session — PASS

For contrast: `delegate(action="follow_up", session_id="44672881...", task="Also return RUNNING_FOLLOWUP_TEST")` while that session was genuinely still running returned a **correct, specific rejection**: `"delegate: follow_up: session 44672881... is not terminal (state=running) — follow_up only resumes a finished session."` This confirms the precondition check itself reads accurate, real-time durable state — the bug in TC-16/16b is specifically in the terminal-case execution path (the validation passes, a "success" description is generated, but the actual resume/re-invoke never happens), not in the server's ability to tell running from terminal.

---

## Cross-cutting observations (adjacent to my scope, flagging because they affected my methodology)

- **`delegate(action="status")` appears broken for anything dispatched in a prior turn.** I relied on it briefly as a diagnostic and it consistently returned `"No subagent found with task ID: delegate-N"` (or `"No subagents found for this conversation"` for the no-id list-all case) for sessions that **REST confirms exist and are `active`**, and that the `cancel` action **correctly finds and reports on** (with the right terminal-reason) moments later. This looks like `status` reads from a per-turn, non-durable registry rather than the durable `SessionLifecycleRecord` its own schema doc says it wraps. This is squarely Monitor-category (UAT-1's scope), but I flag it because it forced me to abandon the "documented" verification path and build REST+cancel-probe verification instead — and because it's a second instance, at the reporting layer, of results being disconnected from durable truth.
- **A `message` WS frame without an explicit `agent_id`, sent against an existing session whose active agent is Jim, appears to silently reroute to the workspace's default agent (Mia)** rather than continuing with the session's established agent. Observed once (not exhaustively verified — outside my scope to chase further), noted here only because it cost me a wasted test round and is worth someone confirming deliberately.

## Shortcut/defect summary (explicit, per the review's mandate)

- **Real defect, CRITICAL:** `delegate(action="follow_up")` on a terminal (completed OR cancelled) session is a no-op that reports success. Caught by: REST message-count-freeze over 20-24s (two sessions) and a 60-second live WS `attach_session` drain that received zero new frames after the follow_up call. This is not "it didn't crash" — I asserted on the actual absence of the requested new content (`GOODBYE_UAT3_FOLLOWUP` / `RETRY_UAT3_SIBA`) and on the complete absence of any new turn activity at the wire level.
- **Reporting-layer defect, MINOR:** a chat-wide-Stop-terminated delegation's `subagent_end` frame says `status:"success"` while the durable record and the OS process both correctly show `cancelled`/dead.
- **Everything else I tested — soft cancel, hard cancel, cancel-on-terminal, sibling isolation, background-bash-kill, and chat-wide Stop's actual effect — is real.** I did not accept any of the tool's own "cancelled" claims at face value; each PASS above is backed by an independent REST poll showing genuine work-stoppage (message count freezing before natural completion), and the background-bash-kill and chat-wide-Stop claims are additionally backed by external, out-of-band checks (a port probe from a separate process, and a `ps aux` scan) that could not be satisfied by a tool merely returning a friendly string.

## Why not "all green"

Two of ten tests are hard FAILs, and I'm not softening that: `follow_up` on a terminal session is broken in a way indistinguishable from the exact defect class this UAT round exists to catch. The one BLOCKED item (cascade-to-grandchild) is the most important claim in my brief and I was not able to confirm it either way — that gap should be treated as open risk, not as an implicit pass, until someone (with either an adjusted test-workspace trust graph, or a different construction path) actually exercises a real 2-level chain and checks whether cancelling the middle session stops the grandchild.

## Evidence artifacts

- WS/REST test harness: `/tmp/claude-1000/-home-dev-omnipus3/9a5cc9d5-94c8-4246-b11e-938e082e3387/scratchpad/wsclient.py`, `ws_stop_test.py`
- Raw frame logs referenced above: `round1.log`, `round2.log`, `round3.log`, `softcancel2.log`, `followup_completed.log`, `attach_child3.log`, `chatstop_clean2.log` (same scratchpad directory)
- All session IDs cited above are real, created by me in this run, and independently queryable via `GET /api/v1/sessions/{id}` with a bearer token for `uat1tester` for as long as the box stays up.
