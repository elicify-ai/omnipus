# ADR-20261006 — Session core with an agent address book: reuse one standing session, one archive and the existing execution paths

**Correction, 2026-10-07 — spec correction round 1:** the saved-chat migration exception is withdrawn: **greenfield everywhere, no migration**. Stop now **discards undelivered webchat/channel input**, rather than holding it for recovery actions. `/clear` is refused in **all helper/subagent/delegate sessions**, native and external; only main and extra chats keep it. These founder decisions replace the contradictory earlier prescriptions. Admin still has his default-workspace main. The dated decision log below records all eight R1 answers; historical drafts are not current authority.

| Header | Value |
|---|---|
| Status | Decided rules recorded; specification in review after correction round 1. Design only, not implementation, runtime verification or landing approval. |
| Date | 2026-10-06; correction completed 2026-10-08 using founder decisions through 2026-10-07 22:10. |
| Decider | Daniel Piatkowski; later founder answers override earlier answers and review recommendations. |
| Author | Architect. |
| Code baseline | `c6837a42dcfb503fb34a0cea86cd3ccc451e7b9b`, true-merged by `250b72caeb5c615ebd37f043a23a12c7befd9e2f`; no production changes in this design task. |
| Specification | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/specs/session-core-spec.md` — requirement/contract identifiers, acceptance oracles and the **single normative DELETE inventory**. |
| Founder record | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/CONTINUATION-20261005.md`::2026-10-06/07 founder entries; original and R1 answers in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/session-core-adr-20261007/QUESTIONS.md`. |
| R1 review input | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/session-core-grill-r1-20261007/session-core-spec-review.md`, reviewed `4adad865286223a4bafa3abf771b0f09b6a9c80f`; compare with `0e1fececc5df91c140e21fd27cf03bfd0ee3c8dc`, which already contains foreign Inbox/status-only and #1221 amendments. |
| Frontend counterpart | **ADR-20261007 — Agent-first navigation and agent identity**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/ADR-20261007-agent-first-navigation-and-agent-identity.md`, `work/adr-frontend-navigation-20261007` at `554d21ffd`, **Proposed**. Read from that Git object; not merged into this worktree. Layout/identity choices remain there. D5/D12 require joint sidebar/main-backend integration and landing. |

## Context

Mia needs one reliable main conversation in each workspace. Clicking her opens it; **+ New chat** on her row deliberately opens an extra chat. A heartbeat uses the main. A task assigned to her normally runs as its real child, but the task still owns its goal, run and result. Asking `@Jim` requests Jim's answer; it does not replace Mia. This is a change to existing paths, not another coordinator or chat product (founder record; specification FR-002/017/026/029).

The simplicity rule is **“avoid designing parallel systems or reinventing the wheel — use what we have.”** Source code wins over dated documentation. Primary grounding: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/AS-IS-architecture.md` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/plugin-extensibility-assessment.md`. Existing ADRs are cited by title below. GitNexus MCP is unavailable; direct reads and controlled caller searches ground this design, not invented graph results.

### Existing mechanisms and limits

The specification's E-keys expand to exact existing `file::symbol` citations. They identify adaptations, **not claims of implemented target behavior**.

| Existing path | Current limitation and required adaptation | Source key |
|---|---|---|
| Standing heartbeat session / protection | Random heartbeat identity and enable-coupled lifecycle become a computed membership-owned main. | E-MAIN |
| Shared session store / day partitions / model window | Useful day logic exists, but production has separate content histories and full model-archive reads. Integrate into one archive and bounded window; do not activate a second store. | E-STORE/E-RETENTION |
| Steering FIFO / session runner / ordinary admission | Competing worker inbox, manual fallback and previous-execution wait obscure settlement ownership. Keep one runner and identity fences. | E-QUEUE/E-STOP |
| Report inbox / acknowledgement / wake | Child delivery requires a real edge; wake eligibility and cap exemptions are currently coupled. Adapt classification without widening preserved exemptions. | E-INBOX |
| Existing delegate launcher / policy | Omitted and explicit self-target checks differ. Normalize before ordinary authorization; keep depth, memory admission and operator Deny. | E-DELEGATE/E-POLICY |
| Task executor / cron modes / run store | Modes exist. Main parentage, fixed recipients and CONTINUE identity boundaries need adaptation, not another executor. | E-TASK/E-SCHEDULE |
| Message tool / connector ownership | Parameters lack a request-bound reply selector. Direct guest egress cannot borrow another owner's connector. Mature the existing source-owner return path. | E-ADDRESS/E-MESSAGE |
| External worker drivers | Current Input discards text; Resume starts fresh. Real interrupt/native resume is required; `/clear` is not a context-reset workaround. | E-CLI |
| Agent deletion / entity state | Entity can disappear before cleanup fails; current existence-first retry cannot finish it. Extend the same cascade with durable original-scope evidence. Mail cleanup intents are in-memory and are not sufficient after restart. | E-DELETE |
| Goal display / streamed turn identity | Canonical keyed goals coexist with a latest-goal scalar. Turn/message identity exists, but a universal run-to-goal carrier does not. Stamp the association at the producer. | E-GOAL |
| Navigation / commands / Activity / approvals | Reuse existing screens and one approval ID; current mention/clear actions and span-only Activity are insufficient. | E-NAV/E-COMMAND/E-ACTIVITY/E-APPROVAL |

## Decision

### D1 — One main per eligible workspace/agent pair

Mature the existing standing-session path into `main-session-<workspaceid>-<agentid>`. Validate the persisted pair and immutable `agent_id`, not just an ID match. Eligible native chat-target agents have one main per membership; workers and other hidden system agents have none. **Admin has one main in the default workspace only**, is displayed like a main colleague and is messageable/@-addressable there. His standalone/no-team operator role, fixed tools and self-helper restrictions otherwise remain (founder 17:15/17:25; FR-002/045; E-MAIN).

Pin/protect main independently of heartbeat enabled. Heartbeat runs in it; Stop, idle and revival do not mint another main. Membership removal immediately hides it from UI/channel/task addressing and admits no new work through that membership. Already-authorized runs settle and keep outcomes without waking the hidden main; re-add reveals the retained pair until normal retention (original Q5=A; FR-003/007).

Contract names remain **Session.type = main**, required current-format type; immutable **Session.agent_id**; computed **Session.protected** and read-only **WorkspaceMemberConfig.main_session_id**. Delete stored **heartbeat.session_id** and mutable handover **active_agent_id**. Main is server-created, not in client-create enums. Validate computed-ID bounds from the existing workspace/agent bounds rather than assuming UUID/128-character IDs. Expose Admin through existing Session list/detail, not fake team membership (C-MAIN; DEL-01/07, DEL-F07/09–13).

### D2 — One append-only archive, two views and a bounded model window

Use one content entry format for chat/model/both, UTC day files, and a `(file, byte position)` model-start mark. Append and model steps read the required bounded window, not lifetime history. Acceptance order governs append order even when source timestamps are delayed. Midnight does not split a conversation or invalidate tool-call/result groups; full result references and session-scoped recall remain (FR-004–006; E-STORE).

Clear, interrupted-turn corrections and view changes append effects or move metadata; they never rewrite prior retained bytes. No new compaction-summary writer: idle recap writes agent memory. A supported compaction entry tag does not imply a writer. Existing whole-tool/turn protections and provider-valid replay remain (Q-R2-8; FR-006/036).

Keep `retention.session_days`, default 90 days by file modification age, including disabled retention. Old main content expires despite identity protection. If the marked day expires, advance to the first retained complete message/tool group and tell the agent older history expired; if nothing remains, use an empty window under the same ID (original Q2=A; FR-007; DEL-09–12).

### D3 — One ordinary FIFO and the existing report inbox

The existing session runner owns ordinary intake **through settlement on every entry path**. Ready human/channel/authorized-parent/peer instructions enter at safe boundaries together as separate messages. At natural completion, ready input starts the next turn immediately. Delete the five-second waiting room, manual queue, competing worker inbox and bypass dispatch; retain execution ID, generation, boot epoch, message and trusted sender/destination protections (FR-008/009/023; DEL-02–04/22/24).

Acceptance saves message plus authenticated provenance together, or neither. A transcript save, intake acceptance and model consumption are distinct claims. No successful receipt for a failed save/admission (FR-008).

Original Q1=A fixes ordinary defaults: **65,536 UTF-8 bytes per message; 60 admissions/minute per trusted sender+target; 200 waiting ordinary items; one aggregate key `session_messaging.steer_aggregate_body = 1,048,576 bytes`, AND existing model-budget fit, whichever is stricter**. Wire existing `steer_body`/`steer_rate` and the aggregate setting at boot/reload/intake (#1216). Preserve the current estimator and model budget; CLI exemption is not a fit guarantee. Refuse before acceptance, never shorten accepted text (FR-010/011; E-LIMIT).

All accepted helper report kinds use the current inbox, dedupe, acknowledgement and consumption identity, and can wake an idle **non-stopped** parent. Live work consumes at a safe boundary; a stopped parent does not revive for a report. Expanding wake eligibility does **not** expand current rate/count exemptions. #1211 adds truthful child rejection/retry information and parent refused-arrival visibility, not automatic retry. Full result/envelope/context limits remain accepted; no new defer, overflow, pagination or summary pipeline (FR-012/013/020/021; E-INBOX).

In-scope #1214 requires truthful queued/applied/superseded/refused control disposition; text refused cannot be delivered and internal stale-generation terms cannot leak as the explanation. #1211/#1214 still require QA reproduction, not a source-read “fixed” claim (FR-013/023).

### D4 — Native self-delegation uses ordinary delegation

Eligible native MAIN and WORKER agents may self-delegate without a self-edge. Normalize an omitted target to the caller before the same authorization check as explicit self. Operator Deny still wins; other-agent execution keeps workspace delegation checks. Use ordinary context, requested-skill/receiver rules, memory admission and normal nested depth, default 3. No self-only hop, quota, spend controller or special memory pipeline (FR-014/015; E-DELEGATE/E-POLICY).

Main and extra chats keep role-allowed tools; delegation is a prompt priority, not a ban or guaranteed model compliance. External CLI workers **permanently cannot create Omnipus helpers**; their internal harness is not tracked Omnipus delegation. Admin/system tools are not broadened (FR-015/016). Parsed-but-unforwarded helper snapshot behavior remains the separate #1212 lane, not a side fix.

### D5 — Tasks retain their executor and outcome ownership

| Work | Existing derived mode / conversation | Requirement |
|---|---|---|
| MAIN assignee, any timing | MAIN: fresh real child of the assignee's main for each actual run; **MAIN beats CONTINUE** | FR-017/018 |
| One-time worker | ISOLATED: fresh independent conversation | FR-017 |
| Recurring worker | CONTINUE: same compatible segment, new run identity each occurrence | FR-017; R1-Q3 |
| Scheduled “run isolated (one session per execution)” checked | ISOLATED: fresh independent conversation for MAIN or worker | FR-017 |
| Heartbeat | Main itself, same intake | FR-002/009 |

Assignee determines the mode; only scheduled isolation is selectable. Keep Calendar `once/config.at_ms` and current RRULE/anchor/time-zone timing. Delete task `every/every_ms` and `recurring.cron_expr` compatibility, not heartbeat's internal cron engine. No new scheduler, outside-payload event bus or Schedules screen (original Q4=A; C-TIMING; DEL-19).

**CONTINUE identity boundary:** an authorized worker, workspace or external-runtime change applies on the next run and starts a new conversation segment. Keep series and earlier run links; never change the old owner or reuse an incompatible native conversation. Active runs retain captured identity. This is existing task/session selection plus run-history adaptation, not handover (R1-Q3=A; C-TASK; FR-017/039).

Authorized future MAIN task launch under a stopped main is task-origin-only, ordered against the existing Stop/cascade fence and does not wake the main model. Keep task claim, task-owned goal, attempts, limits, plan gates and authoritative outcome. MAIN is not hands-on work in a coordinator turn and does not use delegate defaults (Q-R2-1=A; FR-018).

Capture recipients at actual run start: starter MAIN's main, **not creator**, plus MAIN assignee's main; deduplicate. Missing/hidden captured destinations do not acquire guessed substitutes. Every actual done/failed/stopped run carries its authoritative reason and full stored result plus a brief engine header; no skipped/per-retry final notice or launch-time model injection. Keep task/run results inspectable when delivery fails. Use existing report delivery and its limits; no fake child edge for ISOLATED/CONTINUE (FR-019–021; E-TASK/E-INBOX).

Activity shows relevant agent task/scheduler runs through the existing panel, including ones not started by the current chat. Show one MAIN row, actual running/queued/waiting and available token data; missing is not zero. Visibility does not grant tree Stop over independent runs (FR-033).

### D6 — One Stop; discard undelivered human input

| Action | Effect through the existing path | Requirement |
|---|---|---|
| First Stop/Esc/`/stop` | Current turn only; real helpers may continue; existing three-second escalation window | FR-022 |
| Second activation within that window, or `/cancel` | Current real downward tree including MAIN task children; not independent monitored runs or future recurrence | FR-022 |
| `/stop-redirect <instruction>` | Stop selected execution and continue the same root/helper conversation with new instruction | FR-023 |
| Stop wins before webchat/channel input reaches the agent | Discard that pending input, report **discarded/not delivered**; no held state or recovery action | FR-024; R1-Q5 |
| Helper/subagent report arrives for stopped parent | Retain in existing report inbox for the next active turn after legitimate continuation; report cannot revive parent | FR-012/024 |
| Gateway cuts an execution | Show Interrupted; no boot replay of old waiting input; task interrupted failure and plan recovery remain | FR-025 |

Discard ordering is the existing control/consumption fence, not timestamps. A step that already consumed an input is not reported to have forgotten it. Archived input bytes remain; append disposition/view effects. Superseded parent controls never return after revival. There is **no held-message release/discard state, transition, button, client action or contract** (founder 21:50; C-INPUT/C-CONTROL).

Keep selected-execution protection against late force/detach/completion/redirect callbacks. Viewer disconnect or navigation never stops work. Native plain Stop retains background shells; external CLI Stop may kill its subprocesses before native resume, a disclosed exception. Show **Stopped · N helpers still running** from actual activity rather than historical child count (FR-022/023/033/043).

### D7 — Addressed peers and request-bound source-owner replies

Every @/peer/cross-workspace address is the explicit `(workspace_id, agent_id)` pair; default @ chooses the current workspace, another is selected explicitly. Resolve the exact pair to its computed main, including Admin's default pair. Workers/other system agents are not targets. No bare agent address, hash/address-map database or recipient-workspace fallback (FR-026/045; E-ADDRESS).

Eligible main peers may communicate, including cross-workspace, without a delegation edge. The receiver gets only the addressed request/material, keeps its own permissions, and gains no sender context, Stop or approval authority. The owner sees request and guest answer; guest author identity survives live/replay. An answer wakes an eligible idle owner once; silence produces no empty bubble (FR-026/027).

**R1-Q2=A:** expose an admitted request identity to the receiving model and a `reply_to` selector on the existing message tool. The server resolves that selector only within the authenticated receiving session and bound request. The **source owner's existing return adapter** performs connector/webchat delivery with the captured instance/chat/thread/source session; the guest remains the answer's author. A model cannot supply a foreign connector destination to turn this into general send permission. Recheck source ownership/binding before egress; forged, wrong-session, stale or rebound correlation visibly refuses. C-REPLY specifies the concrete shapes and tests; no new messaging service or connector permission transfer (E-MESSAGE/E-ADDRESS; FR-027/028).

Mixed-source answers address each sender individually. Missing usable correlation visibly refuses; no guessed destination or broadcast (original Q3=B). Channels still share the configured pair's main context/control for now. Correct egress is not per-person privacy isolation; #1206 remains later work (FR-028).

### D8 — Navigation, clear and session-aware commands

Agent row click and `/switch-agent <agentname>` navigate to the selected main, never hand over the current owner. Agent-row **+ New chat** is the sole extra-chat UI creation entry. `/sessions` retains existing search, replacing `/resume`; `/agents`, `/new` and named deprecated aliases are deleted. Preserve existing connect/reconnect roster refresh and team picker eligibility (FR-029/031; DEL-05–07, DEL-F01/02).

**Clear only in main and extra chats.** At a safe boundary it moves the context/display start, adds a marker, preserves pending input, same identity and retained search/recall. It does not stop or erase the in-flight step. Every helper/subagent/delegate chat, including MAIN task children and native/external workers, refuses with an explanation through palette and typed/server execution; owner role or stale menu does not bypass session classification (founder 22:10; FR-030/031; C-CONTROL).

| Existing helper/worker capability | Current decision |
|---|---|
| Navigation; help/status/stop/cancel/stop-redirect/sessions/workspace/tasks/recall; existing read-only model/skill/channel lists | Permitted within current authorization |
| clear | **Refused in all helper/subagent/delegate sessions; main and extra chats only** |
| remember/retrospective/goal/loop, model switching, config mutation | Refused in helper/worker context |
| new/agents/list/show/switch/check/channel/start and old resume | Removed everywhere; no hidden aliases |

Typing in a legitimate existing worker/task chat steers its live run or continues a finished conversation without rewriting/rerunning the completed task. Explicit task Rerun remains separate. Workers still cannot be fresh-chat/@ targets. External follow-up uses real native-conversation resume; no successful no-op or silent fresh run (FR-032/043).

### D9 — Idle recap and deletion are different operations

Keep default-on auto recap, default 30-minute activity timeout, including main chats. Any turn/message resets actual activity; recap does not overlap execution/settlement. Repeated idle episodes can recap again. Keep idle/bootstrap and **joined** agent-recorded retrospective; delete lazy/explicit/session_close/ack. Helper memory behavior and retained conversation identity stay unchanged (FR-036; E-RECAP; DEL-08, DEL-F20).

Agent deletion uses one authorized owned-data cascade, preserving default/locked/system/active-plan guards. UI warns accurately and confirms twice; API/tool retain existing single approval. Owned sessions/memory are removed; guest answers elsewhere remain labelled **deleted agent** and never become a runnable identity (original Q6=B; FR-037).

**R1-Q7=A:** before removing the entity, durably capture the deletion's original identity/revision/CreatedAt and exact cleanup targets/reference versions in the same protected mutation/storage boundary. Preserve that original author identity on guest entries so same-ID recreation cannot relabel old guest history as the new agent. The same DELETE/tool operation retries that remaining scope after entity removal/restart. Current authorization is checked; original scope cannot widen to new/recreated data. Retire/isolate old owned roots before recreation can reuse their names, or retain the guard until that isolation succeeds. Never delete “everything currently owned by this ID” on retry. Record step completion and partial errors; retain retry evidence until complete. C-DELETE specifies request, result and existing-screen recovery. No generic cleanup daemon/service (E-DELETE; FR-037/039).

### D10 — Goal indicators belong to their own run

Thinking and goal-setup error indicators join the exact producing turn/message/run's `goal_id` to canonical `goalPills`; unknown association uses neutral text. No latest-goal scalar, arbitrary map choice or `_default` goal key. Capture association at the task/goal/turn producer and carry it through existing live/snapshot/history/replay contracts. Goal changes do not relabel a different run or earlier message; delayed frames only update their matching keyed data. Source inspection establishes that the universal carrier needs wiring, **not that it already exists** (R1-Q8=A; C-GOAL; FR-039; DEL-F39–41).

### D11 — Main attention, task-family merge and foreign Inbox delivery

**Attention:** computed read-only `Session.needs_attention`, true/false on valid mains including default Admin, omitted on non-main. Sources are only that main's pending structured user question, pending approval, or unseen goal ending `met` (finished), `rounds_exhausted`/`other` (failed). `stopped_by_user`, other chats/helpers, generic unread/task/plan activity do not light it. Use a bounded pair-wide goal-seen mark in existing main metadata. One successful explicit foreground open acknowledges observed goal attention for everyone, never unresolved asks/approvals or newer racing outcomes. Existing attach/snapshots/frames and unopened-main query projection must actually update it; source failure is visible unknown/error, not false. Structured-user-question prompt belongs to prometheus-prompt-engineer (original Q7=A; C-ATTENTION; FR-047; E-ATTENTION/E-PROMPT).

**Merge:** canonical create_task/update_task/list_tasks gain optional explicit `workspace_id`; omitted means own workspace. Consolidate delete callers into existing delete_task. Delete all four duplicate `*_in_workspace` tools and their implementations/registrations/policies/default/prompt/caller aliases. Move Ava/Admin/other callers and reuse validation/store/audit; no new executor or cross-workspace plans. **R1-Q1=C:** canonical tools use shipped defaults; saved settings for deleted names are dropped in both layers, not converted or strict-merged. Canonical-name policies still resolve through the existing global ceiling and tightening agent overrides; canonical operator Deny wins (FR-046; DEL-21/23).

**Foreign creation is delivery only:** assigned task in the target workspace's existing team board **Inbox**, `status=inbox`, `workspace_id`, `agent_id`. Return task identity/status/resolved pair and created/not-started guidance, no run/session/claim/activated goal/trigger/dispatch or mode. Foreign run/start/re-run/assign-for-execution refuses before writes. Receiver chooses local pickup inside its workspace under its policy; creator can send an explicit-pair message, not borrow source-workspace execution authority. Creator is not automatically starter, parent or completion recipient (founder 18:40/19:00; FR-048).

Foreign creator may read only status of tasks it created there, explicitly scoped to destination plus `CreatedByAgentID`/`CreatedByAgent`. No mixed human/agent Owner/CreatedBy predicate, unrelated-task visibility, private creator wire field, mutation or operator REST bypass (founder 18:55; FR-049; C-FOREIGN-TASK).

### D12 — Constraints and scope

Root `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/CLAUDE.md` governs single pure-Go binary/embedded SPA, Linux/macOS/Windows, degradation, <10 MB security-feature overhead, file-based storage and ecosystem conventions. Do not keep every main's archive/model runner hot; preserve session-scoped tools/browser/recall and lock order. These are implementation proof obligations, not measured design results (FR-040).

Architect specifies shapes; **backend-lead alone edits/regenerates contracts before consumers**. No handwritten boundary types, extra policy layer, fail-closed per-agent backfill or restored retired surfaces (FR-001/038/040).

Parent Auto/grants inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221): per-agent Auto off-switch is removed there, not implemented here. MAIN task child uses its main parent, not starter extra chat. Tool policy/receiver authority and unattended Ask refusal remain. One native approval ID/modal names the acting helper/run; external harness does not gain native-popup enforcement (FR-034).

| Issue disposition | Boundary |
|---|---|
| In scope | [#1211](https://github.com/elicify-ai/omnipus/issues/1211), [#1214](https://github.com/elicify-ai/omnipus/issues/1214), [#1216](https://github.com/elicify-ai/omnipus/issues/1216); no issue is fixed by writing this ADR |
| Separate/cross-reference only | [#1212](https://github.com/elicify-ai/omnipus/issues/1212), [#1213](https://github.com/elicify-ai/omnipus/issues/1213), [#1215](https://github.com/elicify-ai/omnipus/issues/1215), [#1217](https://github.com/elicify-ai/omnipus/issues/1217), [#1206](https://github.com/elicify-ai/omnipus/issues/1206), approval dependency #1221 |
| Excluded | #1198 waiting-input restart reconstruction/live descendant-stop retry; outside-payload events, changed helper memory, new result pipelines, generic cleanup service or CLI server protocol |
| Greenfield | **No saved-chat migration/importer, conversion, backfill or compatibility reader.** The 17:15 exception is withdrawn; DEL-09–11/21 are strict deletion obligations. |

### New things and why nothing existing fits

This is the **only** justification table; the spec contains shapes/tests, not a duplicate. Each row extends an existing mechanism.

| Necessary extension | Existing candidate is insufficient because | Existing boundary / requirement |
|---|---|---|
| Entry view membership/day-byte mark and original author stamp | Separate model file/line skip cannot express one archive/bounded seek; agent_id alone cannot distinguish a deleted author from same-ID recreation. Reuse existing immutable CreatedAt for the stamp. | E-STORE/E-DELETE; FR-004–007/037 |
| Request-bound `reply_to` and trusted source/guest attribution | Child-only return and content/channel/chat arguments do not bind another owner's connector return to an admitted guest request. | E-INBOX/E-MESSAGE/E-ADDRESS; C-REPLY; FR-027/028 |
| Aggregate ordinary byte setting and discarded-input receipt | Count alone does not bound bytes; current receipts cannot state Stop-discard. No held/recovery action is added. | E-LIMIT/E-QUEUE; C-INPUT; FR-010/011/024 |
| Captured recipients, scheduled isolation and CONTINUE segment identity | Completion-time lookup can change recipients; old session reuse ignores changed execution identity. | E-TASK/E-SCHEDULE; C-TASK; FR-017/019 |
| Main-only attention / bounded shared seen mark | Outcomes have identity but no shared seen writer; pending-only snapshots cannot acknowledge goal attention. | E-ATTENTION; C-ATTENTION; FR-047 |
| Optional task workspace / retired-key drop | Ordinary parameters omit destination; duplicate tools are not a second canonical family or permission source. | E-TASK-TARGET/E-POLICY; C-TASK-TOOLS; FR-046/048/049 |
| Durable deletion scope / continuation projection | Existence-first retry and in-memory mail intents cannot finish original owned cleanup after restart. | E-DELETE; C-DELETE; FR-037 |
| Run-to-goal association on current carriers | A latest scalar cannot identify the goal of multiple/delayed runs; existing goal status alone does not bind the producing message. | E-GOAL; C-GOAL; FR-039 |

### Dated decision log — one current architectural record

This log records authority and supersession. The specification references it instead of copying interview history. R1 IDs are distinct from the original core questions.

| Date/time / record | Decision and supersession | Normative location |
|---|---|---|
| 2026-10-06 Q20 and queue clarification | Delete waiting room; one FIFO/runner, separate ready messages at boundaries/next turn; keep report inbox and authenticated authority. | D3; FR-008/009/012 |
| 2026-10-06 Q22/Q24/Q25; 2026-10-07 main/worker clarification | Main per eligible pair, extra chats permitted; task modes supersede earlier “every event in main/every worker occurrence fresh” formulations. | D1/D5/D8 |
| 2026-10-06 Q29; 2026-10-07 13:50/14:00 | Keep joined; idle recap remains memory; no new archive summary writer. | D2/D9 |
| 2026-10-07 native self/depth and Q-R2-2/4 | Ordinary native self-delegation without self-edge, normal default depth 3; external permanently excluded. No self-only nesting ban. | D4 |
| 2026-10-07 10:25/10:35; Q-R2-1/3/5/7/10 | Guest-answer wake, fixed task recipients, stopped-main scheduled-child exception, main-parent approval, CLI subprocess exception/real resume, unchanged result limits, existing worker input. | D5–D8/D12 |
| 2026-10-07 12:35/12:44; Q-R2-9 | Navigation replaces handover; clear is safe-point context/display reset, not new chat. Worker capability table is later narrowed by 22:10. | D8 |
| 2026-10-07 original Q3=B/Q6=B, 15:10 | Unaddressed replies refuse; UI-only double deletion confirmation. | D7/D9 |
| 2026-10-07 original Q2=A/Q5=A, 15:20 | Expired mark uses first retained complete group/notice; membership hides immediately and authorized work settles. | D1/D2 |
| 2026-10-07 original Q1=A/Q4=A, 15:55 | Approved ordinary limits/one aggregate; keep once/at_ms, delete old recurring forms. | D3/D5 |
| 2026-10-07 17:15/17:25 | Admin default main; cross-workspace collaboration; explicit workspace+agent addresses. Saved-chat migration was granted here but **withdrawn at 21:40**. | D1/D7/D12 |
| 2026-10-07 original Q7=A/Q8=MERGE, 17:45 | Shared main goal-seen/mapping; merge canonical task family. Later task rules narrow foreign operation and persisted-key treatment. | D11 |
| 2026-10-07 18:05, #1221 | Delete per-agent Auto off-switch separately; helpers inherit parent; MAIN child sources main parent. | D12; FR-034 |
| 2026-10-07 18:40/18:55/19:00 | Foreign tasks are existing assigned team board Inbox delivery only; creator status-only read, receiver-local execution; no new inbox/status. | D11; FR-048/049 |
| 2026-10-07 R1-Q1=C, 21:30 / MAJ-001 | Shipped merged-tool defaults; drop deleted-name saved policies, no permission conversion. | D11; C-TASK-TOOLS; DEL-21/23 |
| 2026-10-07 R1-Q2=A, 21:30 / MAJ-002 | Source owner returns correlated guest reply, guest author; no connector transfer/general send grant. | D7; C-REPLY |
| 2026-10-07 R1-Q3=A, 21:30 / MAJ-003 | New CONTINUE segment on worker/workspace/runtime change; prior run links/owners retained. | D5; C-TASK |
| 2026-10-07 R1-Q4=B, 21:30; extended 22:10 / MAJ-004 | Refuse clear in **all** helper/subagent/delegate sessions; main/extra only, every entry path. | D8; FR-030/031 |
| 2026-10-07 R1-Q6, 21:40 / MAJ-006 | **NO MIGRATION**, withdraw 17:15 saved-chat exception; remove import requirements/cases/data/docs. | D12; FR-038; DEL-09–11 |
| 2026-10-07 R1-Q7=A, 21:40 / MAJ-007 | Same authorized cascade retries original leftovers after entity removal/restart; no recreated/guest data deletion. | D9; C-DELETE |
| 2026-10-07 R1-Q5, 21:50 / MAJ-005 | Stop discards undelivered webchat/channel input; helper reports wait for legitimate continuation. Delete held-state/recovery actions entirely. | D6; C-INPUT; FR-024 |
| 2026-10-07 R1-Q8=A, 21:50 / MAJ-008 | Indicator's own run goal; neutral if unknown. No scalar/latest selection. | D10; C-GOAL; DEL-F39/40 |
| 2026-10-07 MIN-001/MIN-002/OBS-001 correction | Add scoped unaddressed-turn/streamer/exited removals; correct Admin amendment; compiler/source proof for erased aliases, behavioral proof for live branches. | DEL-24–26; amended role ADR; spec proof classes |

### Earlier decisions amended by title and clause

Corrections are dated 2026-10-07 and limited to this feature; unrelated decisions remain. The complete source/caller deletion inventory is **only** in the specification::Explicit DELETE Requirements. ADR references here are IDs, not copied rows.

| Existing decision / clause | This amendment |
|---|---|
| **Context paging — sliding-window + recall replaces reactive compaction**::D11/D14; **Context overflow — the sliding window extended mid-turn, tool results emptied with a recall mark, and a per-result cap at the door**::6.3/6.5/18.1/18.2 | D2 replaces separate-history/full-read/destructive writers; recall/tool structure remains. DEL-09–12. |
| **Workspace-scoped heartbeat config + global memory settings UI**::A1 F-02/F-04/A2/D7 | Membership-owned heartbeat-independent computed main replaces random enable-owned standing session. DEL-01. |
| **Unified Slash-Command + Skill Menu (skill-as-command, partitioned palette)**::D7–D9; **Tool manifest tier redesign: ToolSearch, a search-only third tier, switch_agent, and a cached catalog boundary**::D4/5.1 | D8 actions/capabilities and D1 immutable owner replace handover/old commands. Loaded tools remain session/agent scoped. DEL-05–07, DEL-F01/02/09–13. |
| **Built-in agent configuration, skills, and visual file reading**::Who talks to whom / Staff | D4 native self/ordinary depth; D8 existing worker input; Admin has default main but unchanged standalone/tool role. Its dated no-main sentence is corrected in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/ADR-090-built-in-agents-skills-and-visual-reading.md`::Amended 2026-10-07. |
| **A sub-agent is a session steered by another session**::D1/D3/D5; **An open conversation must keep the ability to delegate**::D2/D6/D7 | D3/D5/D6 retain real MAIN child versus independent modes, report wake without stopped revival, task-only stopped-parent exception, Interrupted and selected execution. DEL-02–04/13/17/22/24. |
| **PlanSupervisor — a System Agent that adjudicates and corrects running plans**::D14 | Starter/creator/recipient/parent are distinct; fixed run recipients do not remove plan supervision. |
| **Remove the main sentinel agent**::What was deliberately KEPT; **Entity / config separation — per-entity files for agents**::D6 rule 5 | Owned-data deletion/retry is D9; team hide is D1. Protected entity identity remains separate from agent-writable data; no sentinel agent or broad retry-by-name deletion. |
| **The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume**::D-G; **CLI Minimization: a thin one-shot task-runner over the engine**::D4 | New intake/visible limits and worker existing-chat input; restart reconstruction/live stop retry stay deferred. D6/D8/D12, DEL-20. |
| **Shell permission modes: Ask / Auto / God Mode; drop the block list; one rule format**::D1/D10 | D12 sources MAIN child from main; #1221 removes agent Auto switch separately. Two policy layers, unattended refusal and external exception remain. |
| **A channel belongs to one (workspace, agent) pair, in both directions**::handoff example/4a | Delete handoff, preserve connector ownership. D7 request-bound source-owner return retains guest author without moving permission. |
| **Calendar Recurrence Redesign (Recurring Tasks are Calendar Events)**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/specs/calendar-recurrence-redesign-spec.md`::D1/D8/US5 | Delete old recurring preservation/firing/editor promises. Current US1 one-time once/at_ms and RRULE/common engine remain. DEL-19. |

Preserve session-scoped recall/tool/browser state; actual external runtime/workspace/native IDs; grants and one-directional lock/cache order; other-agent trust/depth; human-versus-agent assignment gates; task series/runs/goals/plan ownership; connector instance provenance; receiver-gated skills; two-layer policy; fixed Admin/system tools; viewer-independent streaming; chronological reply segmentation; root-question/goal ownership; and atomic authenticated acceptance (FR-008/015/018/034/039/040). These are not excuses for retaining scoped legacy aliases.

## Consequences

| Kind | Consequence / evidence basis |
|---|---|
| Positive | Computed standing main, one archive and one intake owner remove competing identity/history/wait mechanisms. **Inferred design effect**, not measured runtime results; D1–D3. |
| Positive | Existing task/report/message/deletion paths keep their owners while making parentage, replies and recovery explicit; D5/D7/D9. |
| Negative, accepted | Stop discards pending web/channel input; already-performed side effects are not undone. Waiting input can also be lost across restart under #1198. D6/D12. |
| Negative, accepted | MAIN task may inherit more permissive main Auto than starter extra chat; target policy/unattended rules still apply, #1221 dependency. D12. |
| Negative, accepted | External Stop may kill subprocesses; native approval does not govern its harness; real CLI resume remains to be demonstrated. D6/D8. |
| Negative, accepted | Shared channel/group context is not per-person privacy; full results can exceed existing delivery/context limits. D3/D7. |
| Negative | Cross-tree contract/source/UI adaptation and deletion proof are substantial. Compiler/source absence alone cannot prove behavior. FR-001/038/039 and spec acceptance oracles. |
| Neutral | Ordinary helper memory, task/plan outcome owners, one approval modal, existing Calendar/Activity/recall and infrastructure remain. Delegation priority may be ignored by a model; accepted reports can spend tokens waking parents. D3–D5/D12. |

### Alternatives considered

| Alternative | Why rejected / authority |
|---|---|
| New address-book database, lifetime main runner, second content store or queue | Existing identity/store/runner suffices; simplicity rule, D1–D3. |
| Second scheduler/task executor or all tasks inside main turn | Existing modes/executor plus real parentage preserve task-owned results; D5. |
| Fake child edges for peers/independent tasks; general guest connector send permission | Confuses reporting with steering/ownership; source-owner request-bound adapter is enough, D7. |
| Held-message release/discard controls | Founder replaced the design with automatic Stop discard; D6/R1-Q5. |
| Saved-chat importer or retired-policy permission conversion | Founder withdrew migration and chose greenfield retired-key drop; D11/D12/R1-Q1/Q6. |
| New cleanup service or fresh deletion by agent name on retry | Same cascade with original durable scope is sufficient; name reuse would delete new data, D9/R1-Q7. |
| Latest-goal scalar or arbitrary keyed-map selection | Can describe another run; exact producer association/neutral unknown chosen, D10/R1-Q8. |
| New CLI server/harness protocol, summary/overflow pipeline or spend controller | Not founder-selected; current driver/report/admission boundaries retained, D3/D4/D8/D12. |

### Affected components and delivery

| Component | Required work / specification reference |
|---|---|
| Session/window/retention | D1/D2; FR-002–008; strict DEL-09–12 |
| Engine/intake/controls/report/CLI | D3/D4/D6/D8; FR-009–016/022–025/030–032/043; DEL-02–04/20/22/24–26 |
| Task/cron/config/catalog | D5/D11; FR-017–021/046/048/049; DEL-19/21/23 |
| Gateway/contracts/ownership/deletion | C-MAIN/C-INPUT/C-REPLY/C-TASK/C-DELETE/C-GOAL/C-ATTENTION; FR-001/026–028/037/045/047 |
| Existing frontend surfaces | Navigation, commands, Activity/approvals, own-run indicators, Calendar and deletion recovery; FR-029–035/039; DEL-F01–43. Counterpart owns visual design. |
| User documentation | Specification DOC-001–DOC-009 names absolute destination pages and exact TODOs. Implementing leads update them in the behavior change; docs-verifier audits. No public-doc update is claimed by this ADR. |

All original core Q1–Q8 and R1-Q1–R1-Q8 are answered. Any genuinely new decision goes to the question record, not an invented implementation assumption. No new open question is identified here. This author **does not run grill-spec**; team-lead controls the next review. Exact-candidate implementation/reachability, tester plus independent-validator UAT with `openrouter` + `deepseek/deepseek-v4.1-flash`, matching docs, joint navigation integration, project gates and founder landing approval remain required (FR-041/042; root Definition of Done).

**Code correct and tested:** not claimed — design documentation only.

**Reachable by a user/agent:** specified entry paths and recovery, not delivered production behavior.

skills: omnipus-shared-rules, plan-spec, gitnexus-exploring, ux-heuristics-review, omnipus-design-system

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Corrections follow founder answers, not review preferences | Founder record::21:30/21:40/21:50/22:10; question record::Correction round 1; R1 report::MAJ-001–008 | Verified instruction, high confidence |
| Current review comparison includes later foreign-task/#1221 work | R1 reviewed SHA `4adad8652`; pre-correction SHA `0e1fececc`; `git diff --name-only 4adad8652..0e1fececc -- pkg src contracts`, exit 0, no production paths | Verified bounded diff, high confidence |
| Reuse seams exist but require adaptation | Specification::Existing Codebase Context; E-MESSAGE Parameters/Execute, E-DELETE existence-first delete, E-GOAL turn/stream/goal carriers; direct reads saved under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/build/session-core-evidence/correction-r1/` | Verified source; impact Inferred, target runtime untested |
| Admin stale amendment is corrected | Commit `89a6e4f5ee67fb0e9ace3389a0dde9836a2f9e38`; role ADR::Amended 2026-10-07 | Verified dated correction, high confidence |
| One inventory and appropriate proof are required | Specification::Explicit DELETE Requirements/proof classes; ADR contains references, not a second inventory | Verified artifact structure; implementation pending |
| **Self-check** | Reread correction diff against eight R1 answers and 22:10; check single inventory, withdrawn migration/held actions, unchanged foreign Inbox/#1221 boundaries, source-owner reply, identity segments, deletion recovery and own-run goal association. No production/gate/landing success or new review is claimed. Final document-check receipts are in the correction hand-off. | Document verification only; runtime Unknown |
