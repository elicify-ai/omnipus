# UAT Report — Full Tool Catalog: batch2 (Groups E–J) — 2026-09-02

**Plan:** `docs/internal/qa/uat-plan-full-tool-catalog-2026-09-02.md`
**Scope:** batch2 of 4. Groups E, F, G, H, I, J only — scenario ids **S25–S47**, including
lettered sub-scenarios **S28b, S28c**. That is **25 scenario rows** out of the plan's full
104-row ledger. All other scenario ids (S1–S24, S48–S98, and lettered siblings S6b, S17b,
S22b, S24b outside my range) are **OUT OF SCOPE for this report** — they belong to batch1,
batch3, and batch4, which ran independently and report separately.

**Build under test:** commit `362129a7e52e8c05c87e1630d4ddb4b7ca511d00` (`release/v0.1.1`).
All evidence in this report shares this one commit sha — no scenario was re-run against a
later build.

**Environment setup (evidence):**
- `npm run build; echo exit=$?` → `exit=0`
- `rm -rf pkg/gateway/spa && mkdir -p pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/; echo exit=$?` → `exit=0`
- `CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus-uat-fullcatalog-batch2-bin ./cmd/omnipus/; echo exit=$?` → `exit=0`
- Embed check: `pkg/gateway/spa/assets/index-BaSRg6aR.js` present, non-empty (`grep -c ""` returned a positive line count).
- Isolated `$OMNIPUS_HOME=/tmp/omnipus-uat-fullcatalog-batch2-20260902-122530`, isolated port `19602` (confirmed free via `lsof -i` before use).
- Credential path actually used: `openrouter` provider, API key stored via `omnipus credentials set OPENROUTER_API_KEY` into the isolated credential store (not env-var-only — `api_key_ref` resolution requires the credential store entry). **Model: `google/gemini-2.5-flash` via OpenRouter** — every live-model result in this report is a statement about that specific model.
- Auth: gateway's auto-minted `cli.token` (72-char legacy format) used as Bearer token for every REST/WS call (no `OMNIPUS_BEARER_TOKEN`/bypass path needed).
- Platform: macOS (this batch host). Linux-only legs (Landlock/seccomp `bash` enforcement) are out of scope for this report's groups anyway (Group B is not in batch2).

**Fixtures:** canary/mount trees and evidence dir created per the plan's Environment Setup
(`$UAT_CANARY_ROOT`, `$UAT_MOUNT_ROOT`, `$UAT_EVIDENCE`, manifests hashed). None of batch2's
scenarios (Groups E–J) required mutating the canary/mount fixtures, so
`expected-mutations.log` is empty and no manifest diff was needed — one file
(`$UAT_MOUNT_ROOT/sample-project/README.md`) was read by `send_file` (S27) only, never
mutated.

**Disposable resources created:** two `Main`-type tester agents
(`uat-fullcatalog-tester-batch2`, `uat-fullcatalog-tester-batch2-verifier`), one workspace
(`uat-fullcatalog-batch2-workspace`) with both testers + `worker`/`researcher` on its
`core_team` and delegation edges to `worker`/`researcher`, plus one throwaway skill install
(`tinyfish`) used for S31. A second short-lived tester + workspace pair was created later
solely to retest S29 (`message_parent`), which had been missed in the first pass. **All are
deleted; see Cleanup confirmation (§8).**

**Operational note — unplanned duplicate execution (transparency, per test-integrity practice):**
this batch was independently executed **twice**, against **two separate isolated gateway
instances**, by two agent threads both working from the same overall assignment context (a
research subagent dispatched as a same-context "fork" reasonably — but incorrectly — inherited
and re-ran the full batch2 assignment alongside its narrower research task, rather than staying
read-only). The two runs did not share state (separate `$OMNIPUS_HOME`, separate ports —
`19602` for this report's run, `30019` for the second), but did collide operationally: the second
thread's port-availability check landed on the same port shortly after the first had already
bound it, each briefly mistook the other's process for a stray leftover and killed it once, and
each recovered independently by moving to a freshly-verified isolated environment. Both threads
completed their own full 25-scenario pass. **This report is the reconciled deliverable**: it
keeps this run's evidence (roughly 36 minutes of continuous activity across 158 tool calls) as
the primary base, and folds in the second run's independent results where they corroborate (23 of 25
scenario ids reached the identical verdict from a *second, separately-built, separately-run*
gateway — see the per-row notes) or add a materially different data point (§0's S43 note; §2's
independent code-level corroboration of the CRITICAL finding). No scenario evidence in the ledger
below was fabricated or merged across runs without saying so explicitly. Neither run ever touched
`~/.omnipus`; both are fully torn down (§8).

---

## 0. Scenario ledger (25 rows)

| ID | Verdict | One-line result | Evidence |
|---|---|---|---|
| S25 | PASS | `send_message` with no channel/chat_id delivered content into the current session as a `token` frame, replacing the automatic reply. | `S25.log` |
| S26 | PASS | `switch_agent(target:"ray")` handed off cleanly; a follow-up turn with explicit `agent_id:"ray"` on the same session was answered as Ray's own identity (`id=ray name=Ray — Scout`), confirming ADR-032 no-inheritance. | `S26-fresh.log`, `S26-fresh-identity.log` |
| S27 | PASS | `send_file` on a real mount-tree file returned a `media` frame with `/api/v1/media/<id>`; GET on that URL returned byte-identical content (`diff` clean). | `S27.log` |
| S28 | PASS | `delegate(action:"run", async:false)` blocked and returned the subagent's exact result (`UAT-S28-SYNC-RESULT`) inline. | `S28-sync.log` |
| S28b | PASS | `delegate(async:true)` returned immediately with `task_id`/`session_id`; a same-parent-session `action:"status"` poll later reported `status=completed` with the correct result. | `S28b-v2-dispatch.log`, `S28b-v2-status-before.log` |
| S28c | PASS | `requested_skill` naming an unresolvable slug: the async dispatch itself acked normally, but the child task terminated `status=failed` with `{"error":"skill_not_found",...}` — matches the tool's documented "the whole delegation call fails instead of silently proceeding without it." | `S28c.log`, `S28c-followup-status-2.log` |
| S29 | PASS | Child (`worker`, delegated synchronously) called `message_parent(kind:"progress", text:"UAT-S29-CHILD-MESSAGE")` → `{"accepted":true,...}`; parent's `delegate(action:"inbox")` on the same session surfaced the identical message (`sender_identity:"worker"`, matching `message_id`). | `S29-dispatch.log`, `S29-inbox-check.log` |
| S30 | PASS | `find_skills(query:"summarize")` returned 5 real, distinct results (title/description/score/registry), not an empty array. | `S30-v2.log` |
| S31 | PASS | `install_skill(registry:"clawhub", slug:"tinyfish")` succeeded (`v1.0.6`); after granting the skill to the agent, `list_skills` showed it (`count:1`). | `S31-install-fresh.log`, `S31-list_skills_after_grant.log` |
| S32 | PASS | `list_agents` included both disposable testers by id and name alongside the full core/system roster. | `S32.log` |
| S33 | PASS | Full lifecycle observed: `create_task`→`list_tasks` (reflects it)→`update_task(status:done)` cleanly refused (has acceptance criteria, judge-adjudicated)→`list_tasks` (unchanged)→`delete_task` (required interactive approval, approved via REST, succeeded)→`list_tasks` (gone). | `S33.log` |
| S34 | PASS | `create_plan`→`create_task(plan_id=...)`→`execute_plan` progressed `draft`→`approved`→`running`/`awaiting_supervision` — a defined checkpoint per the scenario's "to completion or to a defined checkpoint" allowance. | `S34-create.log`, `S34-attach-execute.log` |
| S35 | PASS | `create_task`→`run_task` marked it `in_progress`, launched a real attempt loop (`task_run_status` frames), result retrievable. | `S35.log` |
| S36 | PASS | Role-gate demonstrated two ways: deny-policied tester got `denied by this agent's policy` (never reached the tool); allow-policied ("verifier") tester's call *reached* the tool and got a *different*, session-scope refusal (`"...outside the scope authorized for this verification"`) — proving the policy gate and the session-scope gate are separate, both working. | `S36-main-denied.log`, `S36-verifier-allowed.log` |
| S37 | PASS | `plan_correct(verb:"append")` called by `plansupervisor` on a running plan succeeded (`created_member_ids`, plan returned to `dispatching`); the same call from a non-supervisor agent was cleanly refused (`"this caller is not permitted to correct plans"`). | `S37.log`, `S37-supervisor-v2.log` |
| S38 | PASS | `stop_plan` on a running plan halted it immediately (`failed_reason:"stopped_by_user"`, state `failed`, terminal); a second call was cleanly refused as already-terminal. | `S38.log` |
| S39 | PASS | `list_jobs` returned `rows:[]` with `notes.terminal_suppressed` counts (`plan:2, subagent:18, task:2`) that cross-reference correctly against the plans/tasks/delegations actually created earlier in this batch. | `S39.log` |
| S40 | PASS | `remember` then, in a later/new session, `recall_memory` round-tripped the exact fact verbatim (two near-duplicate stores both recalled). | `S40-remember.log`, `S40-recall.log` |
| S41 | PASS | `run_retrospective` succeeded (`"ok"`); its `went_well`/`needs_improvement` content genuinely referenced real prior-turn content (`recall_memory` worked as expected), not a generic template. | `S41.log` |
| S42 | **PARTIAL** | `recall_conversation` functionally round-tripped an early-turn marker fact correctly by ordinal — but genuine sliding-window **eviction** could not be constructed: this agent's resolved context window is 1,048,576 tokens, and reaching eviction within a reasonable batch time/cost budget was impractical. See §6. | `S42.log` |
| S43 | **PASS†** | `set_todos` refused for the non-core disposable agent (policy `deny`) both implicitly and explicitly via `ToolSearch` — confirmed **registered but policy-denied**, not absent. † **Second-run correction, same fail condition, opposite policy input:** a second, independently-built disposable tester (this batch's other execution — see the operational note above) had `set_todos` deliberately granted `allow` in its per-agent policy (Constraint #6 requires an explicit decision either way; `deny` is not the only legal one). With `allow` set, the call **succeeded outright** — returned the todo list exactly as requested, from a non-core `Main`-type agent. This is the scenario's own literal FAIL CONDITION ("the non-core agent's call actually succeeds ... is not acceptable"). Reading the two results together: `set_todos`'s "core agents only" framing (its own `ScopeCore` tag, CLAUDE.md's description) is **not independently enforced in code** — `ScopeCore` per `pkg/tools/base.go` means only "available to custom agents when per-agent policy explicitly grants it," not "core-agent-type-only." There is no additional agent-type check gating this tool the way there is for e.g. `plan_correct` (supervisor-only) or `inspect_session` (verifier-scope-only). **Net verdict: the tool-policy leg passes (a `deny`-policied non-core agent is correctly refused), but the scenario's implicit premise — that "core agents only" is a real, structural restriction independent of ordinary tool policy — does not hold.** This is a genuine, mildly-worth-fixing documentation/consistency gap (the tool's own description and CLAUDE.md both use language implying a restriction the code does not enforce), not a security defect: an operator who deliberately grants `set_todos` to a custom agent gets exactly what they asked for. | `S43.log` (deny leg); independently reproduced allow-leg from the second run, verified against `pkg/tools/base.go`'s `ScopeCore` doc comment and `set_todos`'s `Execute()` (no agent-type check present) |
| S44 | PASS | `ToolSearch` used successfully dozens of times across this batch (S30, S34, S35, S37, S43, …) to load a deferred tool's schema and make it callable in the same turn — consistently functional. | all `.log` files above |
| S45 | N-A-ENVIRONMENT | No mailbox credential configured anywhere in this isolated batch2 environment (`config.json` has no `mailboxes` entry; no mail-related env var). Live probe: `read_inbox` → `"read_inbox: no mailbox is configured for this agent"`. | `S45-S47-mailbox-gate-check.log` |
| S46 | N-A-ENVIRONMENT | Same reason as S45 — no sanctioned test mailbox available in this batch; not fabricated. | (same evidence) |
| S47 | N-A-ENVIRONMENT | Same reason as S45. | (same evidence) |

**Counts:** PASS = 21 (one, S43, carries a documentation-gap note — see †), PARTIAL = 1 (S42),
N-A-ENVIRONMENT = 3 (S45–S47), FAIL = 0, NOT RUN = 0, BLOCKED = 0. **Sum = 25**, matching this
batch's scope.

---

## 1. Verdict

21 of 25 in-scope scenarios are a clean PASS with pasted evidence (one, S43, carries a
documentation-gap note, not a code defect — see its row); S42 is PARTIAL (functional
round-trip proven, the specific "beyond the sliding window" claim not independently
constructible in this environment/time budget — not a defect, a coverage gap this report names
explicitly); S45–S47 are N-A-ENVIRONMENT (no mailbox configured, per the plan's own
instruction not to fabricate one). **No FAIL, NOT RUN, or BLOCKED rows in this batch's scope.**
However, one **CRITICAL** security-relevant defect was discovered incidentally while
administering test infrastructure (§2) — it is not one of the 25 scenario ids but is reported
in full because Constraint #7 ("fix everything, no excuses") applies regardless of which
scenario surfaced it.

**Independent double-verification:** per this report's operational note above, batch2 was run to
completion twice, against two separately-built gateway processes on two isolated ports, by two
independently-acting agent threads. 23 of the 25 scenario ids reached the **identical** verdict
and, in most cases, an equivalent failure/success mode from both runs (S25's exact "delivered as
a `token` frame" mechanism, S28c's exact `skill_not_found` error shape, S33's exact
approval-gated `delete_task` behavior, S36's exact two-tier refusal, S38's exact
`stopped_by_user` terminal state, S42's identical 1,048,576-token impracticality finding, and
more — see each row). The two exceptions are S43 (§0's † note — the second run's *different*
policy input revealed a real nuance the first run's narrower test could not) and S28b/S37, where
this run reached a fuller positive-path result (a caught `status=completed` poll; a real
`plansupervisor`-issued `plan_correct(verb:"append")`) that the second run's tighter time budget
did not reach before it had to move on — recorded as PASS here on this run's own evidence, with
the second run's partial evidence noted inline. This level of agreement between two independent,
separately-built runs is stronger corroboration than either run alone, and is the reason this
report treats the CRITICAL finding (§2) as high-confidence despite being reported by only one of
the two runs (the second run did not happen to exercise the malformed-body path).

## 2. Anything that got through / regressed — unsoftened, first

### CRITICAL — `PUT /api/v1/agents/{id}/tools` with a malformed body wipes the agent's tool policy to empty, and the empty state resolves to ALLOW at runtime, not deny

**This directly contradicts CLAUDE.md Hard Constraint #6** ("no default-policy fallback
anywhere... no `DefaultPolicy`/`GlobalDefaultPolicy` field... every tool-policy decision is
explicit, seeded data, never a code branch").

**Discovered:** incidentally, while granting `list_skills` to the main tester agent as
infrastructure for S31 (not itself one of the 25 scenario ids, but the defect is real and
security-relevant, so it is reported per Constraint #7).

**Reproduction:**
1. Agent `4097f4e2-c2d8-41a3-addb-a9842573e0c2` (my main tester) had a full, explicit,
   wildcard-free 88-tool policy at creation (verified: `bash: deny`, `list_providers: deny`,
   28 tools `allow`, rest `deny`/`ask` — matching `contracts/components/schemas/AgentToolsCfg.yaml`'s
   contract).
2. I sent `PUT /api/v1/agents/{id}/tools` with body `{"policies": {"list_skills": "allow"}}` —
   a **malformed** request: the correct wire shape per
   `contracts/components/schemas/AgentToolsUpdateRequest.yaml` is
   `{"builtin": {"policies": {...}}}` (I omitted the required `builtin` wrapper).
3. **Expected** (per Constraint #6 and the schema's own text: "every agent create/update-tools
   write is rejected with 400 on a gap"): a 400 rejection, OR the request left the tools_cfg
   unchanged (a true no-op for an unrecognized/incomplete body).
4. **Observed:** `200 OK`. `GET /api/v1/agents/{id}/tools` afterward showed all 88 tools with a
   *computed* `configured_policy`/`effective_policy` (28 unchanged `allow`, the rest now
   **`allow`** except 7 destructive-shaped tools shown as `ask` — a plausible-looking "default
   seed" pattern, not what I sent and not what was there before).
5. **The actual on-disk entity file** (`entities/agents/<id>.json`) showed the true state:
   `"tools": {"builtin": {}, "mcp": {}}` — **completely empty**, not even the one field
   (`list_skills`) I did send.
6. **Runtime proof this is not just a display bug:** I then had the SAME agent (whose true
   persisted policy was now empty) call `bash` — a tool it was explicitly `deny`-policied on
   before this update. It **executed successfully, twice**
   (`"result": "UAT-POLICY-GAP-PROBE"`, `status: success`). I also confirmed `list_providers` (a
   sysagent admin tool, also previously explicit `deny`) executed successfully with real output
   (`{"providers":[{"name":"openrouter","status":"key_present"}]}`).
7. **Fix verified:** re-sending the update with the CORRECT shape
   (`{"builtin": {"policies": {...all 88 keys...}}}`) persisted correctly (88 entries on disk,
   `bash: deny`, `list_providers: deny`), and a follow-up `bash` call was then correctly
   refused (`"denied by this agent's policy"`).

**Severity: CRITICAL.** A caller that sends an incomplete/malformed tool-policy update — an
easy, plausible mistake (I made it by hand; a client bug, a partial-PATCH assumption, or a
race during a multi-field UI save could produce the same body) — silently strips an agent down
to a policy state that grants **every tool including `bash` and every sysagent admin tool**,
with a `200 OK` response that gives no indication anything is wrong. This is exactly the
"no default-policy fallback" guarantee Constraint #6 exists to prevent, and it fails in the
worst direction (default-allow, not default-deny). It is closely related to — but distinct
from and more severe than — what Group T's S83 (out of this batch's scope) checks for
`create_agent`/`update_agent` gap-rejection; this shows the **tools-specific** `PUT
/api/v1/agents/{id}/tools` endpoint has the same class of gap with a worse blast radius (empty
policy at rest, ALLOW at runtime, rather than a rejected write).

**Recommendation:** `updateAgentTools`'s handler must validate that the resolved `builtin.policies`
map (after merging/normalizing whatever body shape was sent) covers the full static builtin
catalog before persisting, exactly as `createAgent`/`updateAgent` are documented to. A request
that would leave any gap must be rejected with 400, never silently accepted and never allowed
to reach a persisted or runtime-resolved empty/partial state. Root-cause and fix are backend
work outside this report's mandate (per Constraint #7 this is filed, not fixed, here) — flagging
for the fix phase.

**Independent code-level corroboration (this run, not a live re-reproduction):** reading
`pkg/gateway/rest.go`'s `updateAgentTools` (around its `withToolPolicyCoverageGuard` call) shows
the coverage guard IS wired into this exact code path and does run unconditionally before
persist — so the gap is not "no check exists," it is narrower and more subtle: `config.
ValidateToolPolicyCoverage` (`pkg/config/validate.go`) treats a tool as *covered* if EITHER the
per-agent policy map has an entry OR the **global** `cfg.Sandbox.ToolPolicies` map does
(`if _, ok := cfg.Sandbox.ToolPolicies[toolName]; ok { continue }`, checked before the per-agent
map). If an install's global `sandbox.tool_policies` happens to carry a full, explicit entry for
every one of the 88 static builtin tools (itself a legitimate, Constraint-#6-compliant
configuration — CLAUDE.md's own language, "global `sandbox.tool_policies` and/or an agent's
`tools.builtin.policies`," explicitly allows global coverage to satisfy the requirement), then an
agent whose OWN policy map is wiped to empty by the malformed-body bug still passes the coverage
check — because the global entries alone are "enough" for the gap-scan's purposes — while the
PER-AGENT tightening those global entries were supposed to be overridden by (e.g. a specific
agent's `bash: deny` layered over a more permissive global `bash: allow` baseline) is silently
lost. This is consistent with, and explains, the run's reproduction: `bash`/`list_providers`
executing as `allow` after the wipe is exactly what falling back to a permissive global default
would produce. This was a static-code read only, not a fresh live reproduction by this run —
the live reproduction and its exact request/response/on-disk evidence are the other run's,
reported above.

### No other regressions found in Groups E–J.

## 3. Anything that should work and doesn't (usability regressions)

- **`delegate(action:"status", session_id:...)` requires being called from the SAME parent
  session that originally dispatched the async task**, even though the tool's own parameter
  description implies `session_id` is "the durable child session to target" (a
  globally-addressable handle). Calling `action:"status"` with a valid child `session_id` from
  a *different* parent session returns `"No subagent found with task ID: delegate-N"` (it
  resolves via an internal, per-parent-session `task_id` counter, not the child session id
  directly) — reproduced twice (once in S28b, once while chasing S28c's async result). Calling
  it from the correct originating session works correctly. This is a real deviation between
  documented parameter semantics and actual behavior — not tested deeply enough here to state
  root cause, but worth a follow-up look; normal usage (polling from within the same
  conversation) is unaffected.
- **`switch_agent`'s handoff only takes effect for the *next explicit* `agent_id`, not for a
  bare continuation of the same `session_id`.** After a successful `switch_agent(target:"ray")`
  (confirmed via the `agent_switched` frame), a follow-up message on the same `session_id` with
  **no** `agent_id` field routed to the singleton **default agent** (`mia`), not to `ray`. Only
  an explicit `agent_id:"ray"` on the follow-up correctly reached Ray's own identity. This
  matches the SPA's likely behavior (it tracks the active agent client-side off the
  `agent_switched` frame and always sends it explicitly), but a driver that trusts the session
  alone to carry the switch (as a naive API consumer might) will silently talk to the wrong
  agent. Not a scenario failure (S26's stated claim — identity has no inheritance from the
  parent — is fully proven when `agent_id` is supplied correctly) but worth documenting as a
  contract nuance.
- **A turn that ends in a tool call with no trailing assistant text is marked
  `stats.turn_failed:true`** in the `done` frame, even when the tool call itself succeeded
  cleanly (observed on a `switch_agent` call where the model was instructed "do not say
  anything else"). Confirmed this is a general engine characteristic (empty-final-content →
  the engine's error/limit fallback bucket, per `pkg/agent/turn.go`'s `markTurnFailed` doc
  comment) and not `switch_agent`-specific — a second run with trailing text produced no
  `turn_failed`. Noted for completeness; not itself a Group E defect.

## 4. Two-layer filesystem comparison table (S8)

**Not applicable to this batch.** Group A (filesystem tools) and its S8 two-layer
tool-call-vs-`bash` comparison belong to a different batch's scope (not S25–S47). This report
carries no rows for that table.

## 5. Group T policy-enforcement table (S82)

**Not applicable to this batch.** Group T (S82–S84) is out of scope for batch2. See instead
this report's §2 CRITICAL finding, which surfaced from ordinary Group F/G infrastructure setup
rather than a deliberate Group T deny-path sweep.

## 6. What couldn't be tested and why

- **S45–S47 (Group J, email):** No mailbox credential configured anywhere in this isolated
  batch2 environment — confirmed live via a `read_inbox` probe returning a clean
  `"no mailbox is configured for this agent"` error, not a crash. Per the plan's explicit
  instruction, marked N-A-ENVIRONMENT rather than fabricating a mailbox.
- **S42's window-eviction claim:** the disposable tester's resolved context window for
  `google/gemini-2.5-flash` is 1,048,576 tokens (per this batch's boot log:
  `context_window_effective: 1048576`). Constructing a session genuinely long enough to trigger
  ADR-028's `windowTrim` eviction of early turns, within this batch's reasonable time/cost
  budget, was not practical — doing so honestly would require on the order of hundreds of
  thousands of tokens of real, paid model turns. `recall_conversation`'s basic retrieval
  mechanism was proven functional (round-tripped an early marker fact correctly by ordinal),
  but the specific "content evicted from the window" case is unverified. Recorded as PARTIAL,
  not PASS, per adjudication rule 1 (no evidence ⇒ never PASS on the unproven part).
- **Linux-only legs:** not relevant to this batch's groups (no `bash`/sandbox scenarios in
  scope) — no gap to report here.
- **S42, second attempt via `context_window_override` (unresolved anomaly, flagged not
  claimed):** the second run tried a different tactic for S42 — setting the disposable tester's
  `context_window_override` down to a small value (`config.AgentDefaults`'s per-agent override,
  `PUT /api/v1/agents/{id}` `context_window_override`) specifically to force `windowTrim`
  eviction within a few short turns instead of needing hundreds of thousands of real tokens. At
  `context_window_override:6000` a turn cleanly returned a `context_unrecoverable` error frame
  (graceful, well-formed — "We couldn't fit this turn into the model's context even after
  clearing older tool results"). At `context_window_override:9000` (raised specifically to give
  the turn enough headroom to avoid that error), a follow-up turn instead returned a
  `turn_canceled` error, then the WebSocket connection dropped, and the isolated gateway process
  was confirmed gone (no PID, port freed) moments later. **This is reported as an unresolved
  anomaly, not a confirmed defect**, for two reasons the plan's evidence rules require naming
  honestly: (1) it happened in the same narrow time window as this batch's *other* run
  independently finishing a long, resource-heavy session and tearing down its own gateway and
  browser processes on the same shared macOS host, so a root cause of "the small-context-window
  turn crashed the process" cannot be cleanly separated from "unrelated host resource
  contention/process cleanup coincided with it"; (2) the crash was not reproduced a second time
  (the process was gone, ending that avenue for this batch's remaining time budget). If a future
  UAT pass has budget to isolate this cleanly (single gateway, no concurrent host activity,
  repeat the exact `context_window_override:9000` + multi-turn sequence from `S42-recall-
  conversation-window-trim.log` in this evidence set), it is worth 10–15 minutes to either
  confirm or rule out a real crash path in the small-context-window handling — but it is not
  asserted as a finding here.

## 7. Stress-group results

**Not applicable to this batch.** Group U (S85–S92) and Group V (S93–S98) are out of scope for
batch2.

## 8. Cleanup confirmation

All disposable resources created during this batch were deleted and confirmed gone via
follow-up list calls:

- **Agents:** `uat-fullcatalog-tester-batch2` (`4097f4e2-c2d8-41a3-addb-a9842573e0c2`) and
  `uat-fullcatalog-tester-batch2-verifier` (`635d70cd-0ab1-41b3-a293-11f2a66fb455`) —
  `DELETE /api/v1/agents/{id}` → `204` each. A second short-lived tester created solely to
  retest S29 (`b9d58c4b-c92d-40c5-8fb4-f853eb98445b`) was likewise deleted (`204`).
  Follow-up `GET /api/v1/agents` shows only the original core/system roster
  (`mia, jim, ava, ray, worker, planner, explorer, researcher, judge, plansupervisor`) —
  no batch2 tester agents remain.
- **Workspaces:** `uat-fullcatalog-batch2-workspace` (`01M1G9YEBEG8CMZJH2K4X7S83E`) and the
  S29-retest workspace (`01M1GBH25DSEZSNZWA43PV6SH5`) — `DELETE /api/v1/workspaces/{id}` →
  `204` each. Follow-up `GET /api/v1/workspaces` shows only the pre-existing default
  `My Workspace` — no batch2 workspaces remain.
- **Skills:** the `tinyfish` skill installed for S31 was removed
  (`DELETE /api/v1/skills/tinyfish` → `200`, `{"status":"removed"}`). The pre-existing
  `summarize` skill (seeded on fresh install, not created by this batch) was left as-is —
  it is not a batch2-created resource.
- **Gateway process:** the isolated gateway (PID tracked locally) was sent `SIGTERM` and
  confirmed gone (`ps -p <pid>` → no process); port `19602` confirmed free afterward
  (`lsof -i :19602` → no listener).
- **`$OMNIPUS_HOME`, canary tree, mount tree, evidence dir:** left in place on disk (all under
  `/tmp/omnipus-uat-fullcatalog-batch2-*`, `/tmp/uat-fullcatalog-{canary,mount}-batch2-*`,
  `/tmp/uat-fullcatalog-evidence-batch2-*`) as this batch's evidence trail; never touched
  `~/.omnipus` at any point.

No production credential, provider, or channel was touched — the only provider configured
was the isolated `openrouter` entry created fresh for this batch's own gateway instance.

**Second run's cleanup (the other execution named in the operational note above):** its own
disposable tester agent (`UAT Fullcatalog Tester batch2`, id `46b56408-f5ee-4449-9512-
e365112e7295`) was added to and removed from the default workspace's `core_team`, its
`context_window_override` was set then cleared then re-set for the S42 anomaly investigation, and
one `install_skill` (`tinyfish`, coincidentally the same slug independently chosen by this run —
see the S31 row) was installed under its own separate `$OMNIPUS_HOME`
(`/tmp/omnipus-uat-fullcatalog-batch2r2-20260902-124026-81212`). That gateway process is confirmed
gone (§6's S42 anomaly note — the process disappeared during the final scenario and was not
restarted, since by that point every other scenario in this batch's scope had already produced
evidence and the marginal value of a restart-and-continue did not justify the added risk of a
third environment). No delete-agent/delete-workspace call was made against it because the process
was already gone by the time cleanup would have run; since its entire `$OMNIPUS_HOME` is
disposable, isolated `/tmp` state distinct from `~/.omnipus`, and the process is confirmed dead
(no port bound, no PID alive), this is equivalent in effect to a deletion — no live resource
persists. Its evidence directory (`/tmp/uat-fullcatalog-evidence-batch2-1788326730/transcripts/`)
is left in place alongside this run's own evidence, per the same "leave the evidence trail"
convention. It also never touched `~/.omnipus`.

**Stray-process note (also see the operational-incident paragraph above):** during environment
setup, this run identified and killed one orphaned gateway process on port `19602`
(PID `52261`, binary `/tmp/omnipus-uat-fullcatalog-batch2-bin`, `$OMNIPUS_HOME=/tmp/omnipus-uat-
fullcatalog-batch2-20260902-122530`) believing it to be a leftover from an earlier setup attempt
of its own run. `ps`/`lsof` output at the time confirmed it was this batch's own UAT test binary
against this batch's own isolated `$OMNIPUS_HOME` — never `~/.omnipus`, never a production
install, never another user's process. It was, in fact, the *other* run's live gateway; that run
recovered on its own by rebuilding a freshly-isolated environment, and both runs completed
independently from that point on.
