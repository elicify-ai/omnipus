# Adversarial Review: The Omnipus system agent specification — round 3

**Correction: the Skill draft proposed asking the founder to choose recap frequency. That was wrong because _Session core with an agent address book: reuse one standing session, one archive and the existing execution paths_::D9 already requires repeated idle episodes to recap again. Correct is a technical adaptation finding, R3-MAJ-010, not a new founder question.**

**Verdict: REVISE — 0 CRITICAL, 13 MAJOR, 2 MINOR and 2 OBSERVATIONS.** The added scope has incompatible conversation boundaries, unowned intake/authorization seams, incomplete room recovery and incomplete workspace-memory isolation. The earlier secret/approval corrections remain specified; expanding channel configuration exposes one residual in the shared-write correction.

| Review header | Value |
|---|---|
| Document reviewed | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md` |
| Mode / round | **SPEC MODE — founder-approved EXTRA round 3 of 4**; independent architect, not the spec author |
| Date / receipt lane | 2026-10-13 / `grill-osa-spec-r3` |
| Branch / reviewed commit | `work/omnipus-agent-design-20261012` / `3404ca905aabf41eb2f7654fcff1907b774bd2d5` |
| Spec SHA-256 | `65e710c6ccbf62e6d72ef96392bd6aa3e183128baed6887f085176e8095c458f` |
| ADR SHA-256 | `1ee85ab835be37e53f50f82aca334d079572291033deefc1e3145142973f657b` |
| Authority | **The Omnipus system agent: one technical partner, workflows as skills, a narrow ask list and a secure secret field**, including OSA-14–16 and dated amendments; later founder answers win |
| Round exception | Founder answers::After spec fix round 2 explicitly adds routing/group scope and two further reviews. This overrides the skill's normal two-round stop; it does not authorize another ADR-mode grill |
| Method | Complete spec, both prior reports, founder records, architectural grounding, relevant ADRs, current contracts, UI guidance/user docs and first-hand source reads. The Skill returned candidate findings for this same round; this report independently verifies and corrects them |
| Instrument limits | GitNexus MCP is not exposed. CLI status says this checkout is unindexed; branch-scoped change detection rejects a stale sibling index. Direct source reads/search and scoped Git diff are the fallback. No graph-derived impact, runtime or visual PASS is claimed |
| Write boundary | Only this report is written and committed. No spec, ADR, contract, production, test or user-doc edits; no builds, product tests, servers, stash or push |

## Executive summary

All twelve lenses were applied, including frontend contracts, screen states, keyboard journeys, component reuse and documentation. **160 functional requirements, 202 declared behaviour-driven scenarios and 139 planned test rows** resolve in the inventory. That is reference completeness, not proof that the tests catch the failures below. K1–K35 contains **34 entries**: K31 was deliberately removed with self hand-off, not accidentally omitted.

The room design correctly removes per-member working sessions and self hand-off. Reuse is feasible, but not unchanged: receiver Ask happens before capture, busy requests can enter a live turn, ordinary replies wake the source's agent owner, and existing request records do not contain durable fan-out/answer settlement. Those adaptations need explicit ownership and acceptance before implementation planning.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 13 |
| MINOR | 2 |
| OBSERVATION | 2 |
| **Total** | **17** |

**Certainty:** quoted document contradictions and source mechanisms are **Verified (high confidence)**. Future failure scenarios are **Inferred**, not executed incidents; confidence is stated per finding. Evidence IDs below resolve to absolute `file::symbol` citations in the final evidence table. Proposed test/component names remain plans, not existing implementations.

**Decisions preserved:** no default agent; mandatory workspace binding; main-chat mode as the connection default; global identity links; steering off by default; group admission and paused notice; user-only rooms; two-agent minimum; main-session room execution; reduced room commands; durable scoped secret Submit; versioned replacement; authenticated no-password card; instruction-only Go; accepted R-A1–R-A4. **GC-5's unlimited replies and FQ-11/FQ-12's accepted risks are not reopened.**

## Findings

### CRITICAL findings

None. The security findings concern incomplete integration obligations under explicit denial/isolation requirements, not a specification that deliberately grants a new bypass. This is not a security acceptance verdict on a future implementation.

### MAJOR findings

#### R3-MAJ-001 — Shared-main mode cannot also leave the web main unchanged

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 3 Inconsistency; 1 Ambiguity; 4 Infeasibility |
| Affected section | FR-147, FR-180–188; US-18/19; BDD-16.7, BDD-19.1; K19/K28 |
| Failure scenario | Anna's connection uses the default `main` mode. Her entitled `/clear`, `/stop`, `/goal` or `/model` changes the same session the web main uses. BDD-19.1 nevertheless requires the web chat and other channel conversations to stay unchanged. If several groups share that main, stopping or resuming one also has no separately defined group execution to control |
| Evidence | FR-147 explicitly selects the target agent's main chat. BDD-19.1 says **“the web chat and other channel conversations are unchanged.”** Current bound intake obtains the pair's main and stamps its ID; its source comment acknowledges shared context/control. The issue's conversation-only requirement and founder GC-6/GQ-3 coexist with the main-default decision; neither silently creates a second control context. E02, E04, E12 |
| Certainty | Verified contradiction/mechanism, high confidence; inferred shared-control effect, high confidence |
| Recommendation | Add the normative session/control scope table for direct chats, channel groups and threads in both modes. Reconcile the incompatible guarantees through Q-R3-1; then make command, Stop/resume, status, tests and docs use that one table. Do not implement hidden per-channel control state over a supposedly shared main to make the example appear isolated |

#### R3-MAJ-002 — The routing change misses capture before the routing ladder

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 3 Code consistency; 7 Reachability; 11 Testability |
| Affected section | §2, §6.4, FR-140–147; WU-14; tests 98/101/102/106 |
| Failure scenario | tg-1's main agent is Mia and Anna's rule targets Jim. A resolver test returns Jim, but real inbound handling already captured Anna into Mia's main and set explicit `agent_id: mia` before the resolver. Alternatively, changing the capture owner to Jim lets intake pass but the final connector return check rejects Jim's answer because tg-1 is owned by Mia |
| Evidence | `admitAndDispatch` calls `admitBoundConnectorInputCtx` first. That function takes the configured pair, creates its main, stores the source-owner pair and stamps explicit routing metadata. `resolveMessageRoute` returns the explicit agent before the ladder; `checkReturnRoute` separately compares current instance ownership with captured ownership. These earlier/later seams are absent from §2's US-16 adaptation inventory. E04 |
| Certainty | Verified source/plan gap, high confidence; inferred wrong destination/refused reply, high confidence |
| Recommendation | Give WU-14 ownership of the actual adapter → admission → capture → session selection → reply chain, not just the resolver. Resolve the destination and conversation mode before preparing the receiver session/capture; retain the connection's source ownership separately from the responding agent. Preserve final rebind/deletion checks. Prove Anna → Jim → original Anna chat through real intake and egress, with two same-type instances and a rebind-before-send refusal. State how the pre-ladder binding/availability check relates to the selected rule target |

#### R3-MAJ-003 — The proposed per-person key contradicts linked cross-channel conversations

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 1 Ambiguity; 3 Inconsistency; 6 Isolation |
| Affected section | FR-147, BDD-16.12; K19/K22; test 102 |
| Failure scenario | Linked Telegram and Slack IDs normalize to Anna, but the reused `per-channel-peer` key still retains the platform, yielding two conversations where BDD-16.12 requires one. Conversely, retaining only type + peer can merge two same-type instances or the same agent across workspaces if the new owning scope is not added |
| Evidence | FR-147 names **“the existing per-channel-peer scope and identity_links.”** BDD-16.12 requires both connections to land in the same per-person conversation. `BuildAgentPeerSessionKey` applies the identity link and then retains channel for that scope; its inputs have no workspace or instance field, and group peers follow a different always-per-peer branch. E05 |
| Certainty | Verified mismatch/key inputs, high confidence; inferred collision/isolation consequence, high confidence |
| Recommendation | After Q-R3-2, specify the canonical owning tuple, trusted normalized sender identity, connection isolation, explicit-link behavior and group/thread keys. Cross-linking never crosses the workspace + agent boundary. Add independent expected-key/session fixtures for linked cross-platform people, two same-type connections, same agent in two workspaces, unlinked identical raw IDs, and groups/threads. Rewrite BDD-16.12 to name its workspace and agent explicitly |

#### R3-MAJ-004 — The steering flag does not own every earlier command path

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 6 Security; 2 Incompleteness; 11 Testability |
| Affected section | FR-185–188, K32; tests 122/123/132/133; WU-14/16 |
| Failure scenario | Ben has steering off. Command-handler tests reject `/model` and `/clear`, but his `/stop`, `/stop-redirect` or `/cancel` is intercepted before that handler. A memory/goal command can also be rewritten before ordinary dispatch. Two entries for Anna in the allow list and person table can disagree about steering, with no specified authoritative result |
| Evidence | `BaseChannel.HandleMessage` performs the allow check and immediately calls `interceptSessionControl`; that interceptor dispatches Stop, redirect and cancel before message publication. `handleCommand` applies skill, memory, goal and loop hooks before the executor. The spec defines the flag but not these admission seams or duplicate-entry semantics. Current empty allow lists admit everyone, so “individually listed” cannot be inferred merely from ordinary admission. E06 |
| Certainty | Verified source/ownership gap, high confidence; inferred bypass under a handler-only implementation, high confidence |
| Recommendation | Name one server-owned participant entitlement lookup before every control interceptor, prompt rewrite and execution path. Use adapter-authenticated IDs, not text/display labels; define duplicate/conflicting entry validation and empty/wildcard admission versus individual steering/resume entitlement. Cover real adapter negatives for Stop, redirect, cancel, memory, goal and loop, including group-only participants. Flag revocation must affect the next command without restart; rejected commands must not reach the model |

#### R3-MAJ-005 — Channel revisions omit the new routing and authority fields

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 2 Concurrency; 3 Inconsistency; 6 Security |
| Affected section | §6.6, FR-085/088/140/150/185; BDD-11.9; tests 75/88 |
| Failure scenario | The operator disables Anna's steering or changes Anna → Jim in Configure. The channel revision remains unchanged because the projection hashes only binding, enabled state and credential names. An already-confirmed helper plan then overwrites that edit without conflict |
| Evidence | §6.6's channel projection is **“instance, platform, binding pair, enabled, reference names.”** FR-140/150 and K19/K32 add admission, person rules, mode and steering fields; the bindings live outside the instance entry. The shared-lock correction alone serializes the stale overwrite but does not detect its changed meaning. E01, E03 |
| Certainty | Verified omitted projection fields, high confidence; inferred stale overwrite, high confidence |
| Recommendation | Include all semantically editable connection fields and its ordered instance-scoped bindings in the secret-free revision: admission/allow list, person rules, steering flags, mode and relevant per-instance settings. Derive Settings and tool tokens from the same canonical projection under the one boundary. Extend tool-versus-Settings contention to each of these field changes, not only the main binding or credentials. This is a residual of R2-MAJ-003 exposed by the new scope, not a new workflow lock system |

#### R3-MAJ-006 — Room reuse needs explicit admission, reply and failure transitions

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 3 Code consistency; 4 Infeasibility; 8 UI states |
| Affected section | §0.9, FR-199–201/166/169/202; K27/K33/K34; BDD-17.11/14/15; WU-15 |
| Failure scenario | A busy member consumes a room request inside its current turn, although the room says queued until that turn settles. A receiver's Ask waits before a ledger capture exists, so it cannot be represented or cancelled by simply scanning pending captures. A denied admission, failed append, provider failure, stopped receiver or restart leaves a member permanently labelled queued/working. An ordinary reply also tries to wake the room's nonexistent agent owner |
| Evidence | `AdmitRequest` starts **or joins** ordinary intake; `trySteerIntoLiveTurn` routes live input to steering. `requestReceiverApproval` waits before capture. `admitRequest`'s actual held flag means stopped receiver, not pending Ask. `replyIntoConversation` appends only to the source and calls `wakeSourceOwner`; it does not create the responder-main room-answer mirror. K27 has no refused/failed/interrupted/unavailable state. E07, E08 |
| Certainty | Verified mismatched reuse claim/state gaps, high confidence; inferred stuck/early execution or owner-wake error, high confidence |
| Recommendation | Specify the per-message/per-member state machine, including pre-capture approval, receiver stopped, denied/refused, capacity/storage/provider failure and restart interruption; unknown is not working. Preserve the founder's busy-member behavior through an explicit room-origin intake rule in the existing queue, not a second runner. Add a room-origin reply branch that preserves captured authority, publishes the room answer, mirrors its room entry in the responding main exactly once, and fans out resolved mentions instead of waking an agent owner. Define state correlation when a member has several outstanding messages; restore it from durable facts in the existing snapshot protocol |

#### R3-MAJ-007 — Partial fan-out Retry has no durable per-member duplicate guard

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 2 Recovery/idempotency; 8 UI states; 11 False-green risk |
| Affected section | §6.3 Room view; FR-166/200/201/203; SC-017/019; tests 113/119/128/129 |
| Failure scenario | One room send reaches Mia, then the gateway exits before Jim is admitted or before the sender sees the acknowledgment. Retry admits Mia again with a new request ID, duplicating work/cost; refusing the whole Retry instead loses Jim. A repeated answer occurrence can append/fan out again because settlement is only in memory |
| Evidence | The room UI offers “not confirmed + Retry,” and SC-019 requires exactly-once visible entries. `admitRequest` mints a new UUID per invocation; `replyIntoConversation` mints another for each append. `Ledger.Resolve` rejects discarded requests but does not record durable answered settlement; `settleRequest` clears process-memory sets. Existing web retry handling reads an in-memory accepted-message cache, not a durable room-message/member fan-out receipt. E07–E09 |
| Certainty | Verified missing invariant/source behavior, high confidence; inferred duplicate/lost partial delivery, high confidence |
| Recommendation | Define stable room-message/member delivery identities and durable progress in the existing room/request records. A Retry reconciles the original send and admits only members not already admitted. Deduplicate the same answer occurrence and its mention fan-out, not every distinct answer to a request. Preserve the existing no-automatic-replay rule for already-consumed interrupted work: expose interruption and an explicit new-answer action rather than replaying tool effects at boot. Fault-cut tests must cover room append, 0/1/several member captures, receiver append, answer append before acknowledgment, and answer-to-mention fan-out. Do not promise exactly-once external tool side effects |

#### R3-MAJ-008 — Pause has no execution fence or pre-pause provenance after Resume

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 2 Concurrency; 6 Authority; 11 Testability |
| Affected section | FR-162/170/203/204; K34/K35; BDD-17.6/17/20; test 115 |
| Failure scenario | `/pause` cancels a snapshot of pending requests while another fan-out admission or Ask approval completes afterward. Pause then Resume lets an older running answer appear current and its mentions start a fresh exchange, violating the decided “after pause, no forwarding” rule. Removing a member or deleting the source room leaves outstanding delivery disposition unspecified |
| Evidence | FR-170 defines the desired behavior, but K34 records only room origin/ID and K35 only a boolean paused state. Neither identifies which pre-pause requests remain suppressed after Resume. Current admission waits/rechecks and ledger discard are separate operations; delivery marking and queue consumption are also distinct. E07–E09 |
| Certainty | Verified missing ordering/provenance contract, high confidence; inferred late admission/refan-out, high confidence |
| Recommendation | State the atomic ordering boundary between room admission, approval completion, model consumption and Pause. Retain a durable room generation or equivalent per-request pause provenance: an answer to work running when Pause won remains after-pause even after Resume and never forwards its mentions. Cancel every not-yet-consumed revoked room request through the existing ledger/queue; never interrupt unrelated or already-running main work. Define membership/deletion races: no new delivery to removed members or deleted rooms; retain already-authorized work's settled outcome in its main and report unavailable source delivery honestly. Add deterministic Pause/admit, Pause/approval, Pause/dequeue, Pause–Resume–late-answer and membership/deletion fixtures |

#### R3-MAJ-009 — Workspace memory cannot be selected through an agent-wide mutable pointer

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 6 Isolation; 2 Concurrency; 3 Code consistency |
| Affected section | FR-194–197; BDD-20.3/4; test 126; WU-17 |
| Failure scenario | Concurrent Mia@Sales and Mia@Marketing calls each set the shared MemoryStore's workspace pointer, then read/write later. Sales uses Marketing's pointer. Even after scoping new notes and last-session, `recall_memory` can still append Marketing retrospectives, or the cached bootstrap prompt can inject memories from the previous active workspace |
| Evidence | `MemoryStoreAdapter.SetWorkspaceID` mutates one store and a separate `SearchEntriesInRoom` uses it. `RecallMemoryTool.Execute` sets the pointer, searches, then calls `SearchRetros`; that adapter reads agent-wide retrospectives. `GetMemoryContext` reads agent-private last-session and notes; the context builder calls it while constructing cached workspace prompts without passing immutable scope to the memory operation. Locks protect individual pointer access, not the set-then-use operation. E10 |
| Certainty | Verified current scope mechanisms and omitted adaptation inventory, high confidence; inferred cross-workspace interleaving/disclosure, high confidence |
| Recommendation | Give every memory operation an immutable trusted workspace + agent scope, or a pair-bound store. Inventory ordinary private saves, retrospective writes/search, bootstrap prompt reads/cache invalidation, recap success and fallback, automatic-note writes and carry-forward. An old agent-wide room must not be the fallback for workspace-owned data. Add deterministic same-agent concurrent-workspace tests through real tools/context building, with identical query terms and distinct sentinel facts. Test private/shared/both recall and provider-failure fallback, not just sequential new-note creation. This extends the known recap leak (#1197), not a side implementation by this reviewer |

#### R3-MAJ-010 — The reused recap pipeline is lifetime-once, contrary to the existing repeated-idle decision

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 3 ADR/code consistency; 4 Feasibility; 11 Testability |
| Affected section | §0.7, FR-191/194/196; BDD-20.3/7; tests 124/126; WU-17 |
| Failure scenario | Anna's ongoing conversation gets its first idle recap. She later changes the outcome or preference in the same conversation. The next idle period does not refresh the recap or automatic notes; the main's “latest” summary remains stale. Restart still skips it because an earlier retrospective file exists |
| Evidence | **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**::D9 already says repeated idle episodes can recap again. Current `CloseSession` claims the session ID permanently; `agentSessionHasRetro` treats any existing file as recapped. `persistResponse` explicitly retains the lifetime claim. The spec assumes reuse supplies current summaries and does not own these gates. E11 |
| Certainty | Verified decision/source mismatch, high confidence; inferred stale awareness, high confidence |
| Recommendation | Implement the already-decided repeated-idle behavior through the existing recap path, keyed to transcript progress rather than lifetime session ID. Keep unchanged-transcript deduplication and idle/non-overlap controls; stale completion must not overwrite a newer summary. Persist/recover a conversation's recap progress and its automatic-note outcome without another summarization path. Clarify “no extra model calls”: awareness list construction and note extraction add none; they reuse each legitimate idle recap. Test a second idle episode after new activity, unchanged activity, restart and out-of-order completion. **No founder question: frequency is already decided** |

#### R3-MAJ-011 — Display-name mention resolution can address the wrong member

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 1 Ambiguity; 5 Contract semantics; 11 Testability |
| Affected section | FR-164/165, K25; D7; test 112 |
| Failure scenario | Two members have the same case-insensitive display name, or a member is renamed after picker selection. The picker submits a stable ID but the server re-resolves the text to a different member. Multi-word names and a member named “all” also have no unique interpretation |
| Evidence | FR-164 matches display names case-insensitively; K25 carries IDs but says the server re-resolves from text. D7 only has distinct single-word names. Current `preparePersistence` trims a renamed name and rejects empty names; it does not make this spec's mention namespace unique. E01, E13 |
| Certainty | Verified undefined namespace/text-ID semantics, high confidence; inferred misaddressing, high confidence |
| Recommendation | Define unambiguous mention tokens and text/ID consistency checks. Preserve and validate the picker-selected stable ID; do not silently resolve it to a different member. Ambiguous raw text refuses or opens the existing picker, with a specific message. Specify multi-word/Unicode names, duplicate names, reserved `@all`, edits after selection and rename-before-send. Extend D7 with independent expected recipient IDs and zero unintended admissions |

#### R3-MAJ-012 — “Proportional inputs” is not a reproducible room-cost oracle

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 1 Ambiguity; 4 Measurability; 11 Testability |
| Affected section | FR-171; SC-017; BDD-17.5/7; test 114; K23 |
| Failure scenario | One producing main turn uses two rooms and a web input. One implementation counts messages, another bytes and another estimated tokens; all comply with “in proportion to its room-originated inputs” but show different room costs. Unknown price can also appear as zero. A room-only test passes all of them |
| Evidence | FR-171 defines no input unit, denominator, accounting boundary, rounding, persistence or unknown-cost behavior. Existing `AddTurnStats` and `appendAssistantTranscriptImpl` record producing-turn totals, not a room allocation. Cost visibility is the retained mitigation for accepted R-A4, so misleading totals defeat a stated user control. E14 |
| Certainty | Verified missing allocation/oracle and current accounting shape, high confidence; inferred misleading cost, high confidence |
| Recommendation | State one allocation formula using the existing accounting inputs, when its weights are fixed, what belongs in the denominator, rounding and durable attribution. Label allocated estimates and unknown prices honestly; do not count the same spend twice. Add a mixed two-room/web fixture with independently calculated totals, plus interrupted/failed work, duplicate answer delivery and unknown price. Do not introduce a reply/loop cap or reopen GC-5 |

#### R3-MAJ-013 — Added scope is missing write and live/replay contract carriers

| Field | Review |
|---|---|
| Severity / lens | **MAJOR** — 5 Contract-first gaps; 7 Reachability; 8 UI states |
| Affected section | K18–K35, §7.2, §15 WU-0; FR-152/158/169/174/201/202/204 |
| Failure scenario | The hidden-conversation toggle calls the described PATCH “existing update route,” but the existing operation is PUT rename with required title. A room request/answer has room metadata in REST Message yet its live or replay frame lacks it, so the main displays ordinary expanded traffic or drops an ad-hoc field. A team mute is refused because K24 prohibits editing the team room, while K23 stores mute only for team rooms although `/mute` works in every room. Room attention has no specified server-derived source/read representation |
| Evidence | K29 names Session and PATCH, not an update-request schema; the actual operation is `renameSession`/`SessionRenameRequest`. K26/K33/K35 name transcript/Message metadata but not its UserMessageFrame, TokenFrame, ReplayMessageFrame and catch-up/snapshot projections; current publication builds those generated frames separately. K23/K24 contain the mute restrictions above. Current Session requires `agent_id`, while K23 rooms omit it; ordinary `needs_attention` is explicitly main-only. WU-0 still owns K1–K17, and validation tests 62–64 cover old secret/agent/binding payloads, not the added room/routing writes. E08, E15 |
| Certainty | Verified contract inventory/transport mismatch, high confidence; inferred unreachable toggle/inconsistent rendering or state, high confidence |
| Recommendation | Complete the endpoint/method/request/response/frame map before consumers: visibility update, all-room mute with a narrow team-mute exception, conditional room versus agent ownership, derived room attention, per-member state recovery, and every live/history/replay carrier for room refs/addressees/after-pause. Preserve required ownership for ordinary sessions; do not make all agent ownership optional to admit rooms. Apply the new room branch before ordinary agent execution/control lookup. Name nil-safe bounded arrays, safe refusals and schema mirrors. Assign K18–K35 to contract preparation and real malformed-payload/consumer tests in both inbound-validation modes, with positive controls. Follow schema → reference → generation → atomic generated commit → consumer; backend-lead alone edits/regenerates |

### MINOR findings

#### R3-MIN-001 — Superseded scope and Stop wording can misdirect delivery

| Field | Review |
|---|---|
| Severity / lens | **MINOR** — 3 Inconsistency; 2 Documentation/delivery completeness |
| Affected section | §1, §4, US-17 opening, §6.3, BDD-20.1, SC-017, §14/15/19 |
| Failure scenario | A planner follows the out-of-scope row or Landing table and omits S4; QA implements Stop-all or three awareness lines including rooms; user docs tell someone to use `/stop` in a room where it is expressly refused |
| Evidence | §1 still says group chats are out of scope; §4 says Stop stops every member's work. BDD-20.1 lists rooms despite FR-190's non-room boundary. §6.3 describes a zero-agent team room although FR-204 hides it below two. §14/§19 and SC-017 retain Stop wording; the Landing row says S1–S3, excluding S4. Later §0.9/0.10 and FR-170/190/204 resolve the intent. E01, E02 |
| Certainty | Verified contradiction set, high confidence; inferred delivery/documentation confusion, medium confidence |
| Recommendation | Sweep all normative examples, success criteria, state rows and doc TODOs against the latest decisions. Use Pause, running work untouched, no forwarding of after-pause answers, no team room below two, non-room awareness and **S1–S4** in the one landing. Make paused/read-only states explicit in the room screen table. Preserve the one complete landing; do not introduce new stages or resurrect removed room-worker/self-hand-off concepts |

#### R3-MIN-002 — The exact Omnipus tool set contradicts the new awareness grant

| Field | Review |
|---|---|
| Severity / lens | **MINOR** — 3 Inconsistency; 7 Reachability |
| Affected section | §6.5, FR-007/198; test 3; WU-2/17 |
| Failure scenario | The grant author builds from the “exact supporting set” plus “everything else Deny” and omits `read_conversation`, while the awareness grant table/test expects Omnipus to ride Allow. Independently authored inventory and execution tests then disagree |
| Evidence | §6.5's grant row and FR-198 include Omnipus; its exact-name list omits `read_conversation`. Current role inventories start ungranted names at Deny and custom constructors use explicit overrides; a ceiling/catalogue entry alone is not an executable grant. E01, E16 |
| Certainty | Verified contradictory grant sources, high confidence; inferred seed mismatch, high confidence |
| Recommendation | Reconcile the exact set, role inventory, custom-main/worker rule and test oracle. Include `select_secret_version` and `read_conversation` in the reachability table with actual executable dispatch proof. Keep sparse tightening and all hidden/external exclusions; do not add fail-closed load-time backfill or a resolver exception |

### Observations — non-gating

#### R3-OBS-001 — Remove the unowned awareness inspector wire surface

| Field | Review |
|---|---|
| Severity / lens | **OBSERVATION** — 12 Overcomplexity; 7 Reachability |
| Scenario / evidence | K30 introduces `AwarenessEntry` **“for the web inspector of what the agent sees”**, but no story, screen, operation, transport consumer, test or documentation TODO owns that inspector. The approved capability is context list + read-only tool + digest, not another screen. E01, E02 |
| Certainty | Verified unowned consumer in the complete spec, high confidence; need for a new inspector Unknown |
| Recommendation | Remove this wire surface if the entry is internal context data only. If an already-approved consumer exists, name that consumer, transport, owner and acceptance instead. Do not commission a speculative inspector. Group membership audit shapes should be specified with their actual existing audit consumer, not bundled with an unrelated list |

#### R3-OBS-002 — Extend component and composite-keyboard acceptance to the new scope

| Field | Review |
|---|---|
| Severity / lens | **OBSERVATION** — 9 Accessibility/keyboard; 10 Design-system reuse |
| Scenario / evidence | The generic all-surface keyboard/state obligations remain, but §6.8's mapping and BDD-12.14's explicit journeys stop at the earlier scope. The new mention picker, membership dialog, hidden-conversation disclosure and person-entry steering switch could pass primitive checks without a usable composed journey. Tests 119/132/139 name the surfaces but not their focus/error/announcement oracles. Current catalogue already includes Command, Dialog, Popover/Sheet, Switch, DisclosureRow, AgentIcon and state controls. E01, E17 |
| Certainty | Verified handoff omission, high confidence; inferred primitive-only blind spot, medium confidence. No rendered inaccessible control is asserted |
| Recommendation | Extend the mapping and real-shell UAT rows using existing catalogue jobs. Name initial/return focus for participant/squad dialogs, arrow/Enter/Escape behavior for mentions, keyboard membership/mute/steering edits, expansion of collapsed traffic and announcements for paused/failed/unknown states. Keep touch/reflow/reduced-motion evidence separate from kit/manifest/Storybook compliance. This requests execution of existing accessibility obligations, not a visual redesign or new control family |

## Structural integrity and twelve-lens coverage

| Spec-mode check | Result | Evidence / limitation |
|---|---|---|
| Status / ADR / authority | Present | Draft; founder exception and new scope explicitly recorded; no ADR-mode re-grill |
| Contract-first order | Present, expanded inventory incomplete | Five-step order remains; R3-MAJ-013 and R3-OBS-001 |
| API/data ownership | Partial | Source-owner intake, room admission/reply/settlement and scope keys need R3-MAJ-002/003/006–009 |
| Screen states / journeys | Present, truth incomplete | Old states retained; failure/recovery/paused/read-only room states need R3-MAJ-006–008/013 and R3-MIN-001 |
| Accessibility / keyboard | Generic obligations present | Added composite evidence ownership needs R3-OBS-002; no browser/visual PASS |
| Component reuse / brand | Old mapping retained | Existing primitives verified; extend mapping, not visual design, R3-OBS-002 |
| Security / user promises | Partial | Accepted risks retained; entitlement, revision and workspace isolation gaps remain |
| BDD / traceability | Complete for references | 160 FR definitions = 160 matrix rows; 202 scenarios assigned; 139 planned rows. Adequacy remains partial |
| Reachability | Planned, not demonstrated | New screens/tools have intended homes; real intake, transport and exact grant issues remain |
| User-facing docs | Specific TODOs present | Same-change pages, new room page and memory/connector updates named. Stop and scope wording must be corrected |
| Work-unit sizes/order | Mostly coherent | Added WU-14–17 Feature; one complete landing retained. Contract ownership and omitted S4 need correction |

| Lens | Applied result |
|---|---|
| 1 Ambiguity | R3-MAJ-001/003/011/012: conversation scope, canonical keys, mentions and cost |
| 2 Incompleteness | R3-MAJ-004–009/013: entry paths, revisions, state machines, recovery, Pause and all memory paths |
| 3 Inconsistency / ADR / AS-IS | R3-MAJ-001–006/009/010; R3-MIN-001/002. Code wins over dated architecture text; latest founder answers win over older design clauses |
| 4 Infeasibility | R3-MAJ-001/006/010/012: impossible isolation promise, unchanged-reuse claim, lifetime recap and unmeasurable allocation |
| 5 Contract-first | R3-MAJ-011/013; K1–K35 assessment below |
| 6 Security / user promises | R3-MAJ-004/005/008/009; existing authority, retained settled outcomes and no third tool-policy layer preserved |
| 7 Reachability | R3-MAJ-002/013, R3-MIN-002, R3-OBS-001; executable registry separate from metadata; actual proposed paths reviewed |
| 8 UI states / journey | R3-MAJ-006–008/013; truthful partial member delivery, retry, late answers and reconnect |
| 9 Accessibility / keyboard | R3-OBS-002; generic obligations checked, new composite evidence ownership not inferred from primitives |
| 10 Design-system / brand | R3-OBS-002; catalogue and Sovereign Deep rules checked; no raw-control or visual redesign proposed |
| 11 Testability / false greens | R3-MAJ-002–013 and test-gap table; reference inventory is not behavioural proof |
| 12 Overcomplexity | R3-OBS-001; reuse corrections below remove speculative inspector/parallel machinery, not decided capabilities |

### UX heuristic check — several issues to address

| Heuristic | Relevant issue |
|---|---|
| H1 Visibility of status | Refused, interrupted, paused, read-only and partial room delivery must not look working; cost must be reproducible |
| H3 User control | Pause must stop future forwarding while preserving running main work; Retry must not duplicate already-admitted work |
| H4 Consistency | Shared main and isolated channel control cannot be simultaneously promised; Room Stop wording must go |
| H5 Error prevention | Stable mentions, individual entitlement and shared revision checks prevent wrong recipients/authority |
| H7 Keyboard operation | Added picker, disclosure, membership and steering controls need real-shell evidence |
| H9/H10 Recovery/help | Correct room command refusal links, observable failures and updated user instructions |

| Priority action | Findings |
|---|---|
| Resolve conversation boundaries and own the real routing/authorization chain | R3-MAJ-001–005 |
| Specify room admission, partial recovery and Pause ordering without another execution engine | R3-MAJ-006–008/013 |
| Bind every memory read/write to its workspace and use the already-decided recap lifecycle | R3-MAJ-009/010 |

## Round-1 and round-2 correction verification

**Closed at definition level** means the obligation and named planned proof remain in the spec. It does not mean implemented, run, mutation-proven or landed. The new room/channel/awareness findings are not evidence that an earlier secret correction disappeared.

| Round-1 finding | Current disposition | Verified text / residual |
|---|---|---|
| CRIT-001 private workspace boundary | Retained | FR-065/076, D1/D6, BDD-7.5/6, tests 34/71 use trusted workspace + agent + variable |
| MAJ-001 staging lifecycle | Retained with round-2 correction | FR-063–067/072/086: durable Submit, versioned storage, selection distinction and truthful completed effects |
| MAJ-002 unbound named-agent channels | Retained; new path integration incomplete | FR-121/127–129, D3, BDD-13.1/8 still refuse unbound; R3-MAJ-002 owns actual pre-ladder intake |
| MAJ-003 submission-unknown recovery | Retained with round-2 correction | K4/K5, FR-079/087, BDD-6.24, test 86: readable state and receipt in same atomic store publication |
| MAJ-004 wire/validation omissions | Earlier corrections retained | K8 and §7.2 preserve semantics with validation on/off and generated mirrors; added-scope residual is R3-MAJ-013 |
| MAJ-005 no-default caller oracle | Retained | §6.4 covers sixteen former getter callers plus secondary fallback/boot/browser replacements |
| MAJ-006 meaningful coverage | Earlier ownership corrections retained | Sentinel positive/nonempty control, singleton, all-native dispatch, readiness and inherited control owners remain. New scope still needs the tests below |
| MAJ-007 God Mode and ambiguous routing | Retained | BDD-3.4/7/8 distinguish clarification/secret from confirmations; BDD-2.2 must ask, 2.5 nested case is separate |
| MAJ-008 states/keyboard | Retained | §6.3, FR-109/112 and old real-shell journeys remain; new-scope extensions noted separately |
| MAJ-009 ordinary/custom execution grants | Earlier correction retained | §6.5 executable boot/reload wiring and test 65. New `read_conversation` exact-set mismatch is R3-MIN-002 |
| MAJ-010 private inputs/masking | Retained with round-2 corrections | Byte/NUL/name rules, per-child scope, protected transitive layers and full masking rebuild ownership remain |
| MAJ-011 shared writes | **Partial after scope expansion** | Lock-order/revision correction retained; new channel fields excluded from revision, R3-MAJ-005 |
| MAJ-012 picker/self-row | Retained | FR-104 eligible nonmembers only; FR-111 and BDD-12.12 preserve graph editing/removal/refused next helper |
| MAJ-013 secret endpoint trust | Retained | §6.7/FR-083/084 retain account/session/recipient checks, bounds, zero-write refusals and security-lead gate |
| MIN-001 warm default browser | Retained | FR-126/BDD-13.11/test 79 |
| MIN-002 kit mapping | Retained for earlier scope | §6.8 and static/composite separation; extend it for added scope, R3-OBS-002 |
| OBS-001 landing bundles | Earlier correction retained; table needs sweep | One complete landing, grants committed with permission split; S4 missing in Landing row, R3-MIN-001 |

| Round-2 finding | Current disposition | Verified correction / residual |
|---|---|---|
| R2-MAJ-001 private selector and Stop truth | Retained | FR-064/067/086, §6.5/6.7, BDD-6.19–23/7.11 and tests 84/85/87 |
| R2-MAJ-002 Submit common commit fact | Retained | FR-087 and BDD-6.24/test 86: same atomic publication contains value-free receipt; recovery/reference-only idempotent resume specified |
| R2-MAJ-003 shared synchronization/projection | **Partial after expansion** | Explicit config-lock → loop-lock order and four-resource tokens now specified; channel projection incomplete, R3-MAJ-005 |
| R2-MAJ-004 gateway environment overpromise | Retained | FR-062/076 distinguish private per-child values from retained integration resolution; docs TODO includes the distinction |
| R2-MAJ-005 masking rebuilds | Retained | FR-089/BDD-6.25/tests 89/90 include boot, reload, Settings, sign-in, cloning and serialized complete-set registration |
| R2-MAJ-006 unsafe independent landing | Retained | S1–S4 are preparation stages, not independent landings; permission split moves with WU-2 grants. Fix S4 omission rather than reopen staged landing |
| R2-MAJ-007 protected environment layers | Retained | D6/FR-076 include prefixes, setup/document layers and post-composition injection; test 91 exercises real composers |
| R2-MIN-001 inherited control owners | Retained | §9.5 now explicitly assigns listing-only, shared-skill, multi-consumer failed-reload/next-boot, policy-reload and other target verbs |
| R2-OBS-001 composite evidence | Retained | §13 keeps real screen keyboard/touch/reduced-motion evidence separate from catalogue/Storybook proof |

## Contract completeness — K1–K35

This is a specification shape audit, not a claim that future schemas already exist. Backend-lead alone owns editing and regeneration.

| Entry | Assessment |
|---|---|
| K1 | Secret request identities, display bounds and owner pair retained; real generated parser/dispatch remains required |
| K2 | Frozen effective recipient/private pair retained; display is not authorization |
| K3 | Value-free terminal notification retained; durable state comes from receipt/state read, not delivery of this frame |
| K4 | Bounded never-null pending/terminal snapshot array retained; preserve existing full-snapshot/catch-up ordering |
| K5 | POST submit + GET state, exclusivity, safe errors and references-only recovery retained |
| K6 | Stored/selected/superseded name listing retained; align FR-073's in-use flag with the derived state/consumer fields during generation |
| K7 | Root/acting helper approval fields retain ordinary session/workspace ownership and Deny-only reconnect stub |
| K8 | New-session nonblank agent rule and workflow target metadata retained; rooms with existing IDs need their non-agent dispatch branch |
| K9 | Intentional server kickoff into existing Omnipus main retained; client mint-only protection stays |
| K10 | Pairing request/session correlation retained; chat consumer and subscription recovery remain acceptance obligations |
| K11 | Agent default-field removal retained; preparation only until consumers are complete |
| K12 | Update default removal/Omnipus Ask refusal retained; required permission split stays with grants |
| K13 | Tools-write Ask refusal retained; no new resolution layer |
| K14 | Required bound write versus truthful invalid legacy read retained; explicit read/write schema distinction remains necessary |
| K15 | Loud routing degraded status retained; distinguish routing repair from missing credentials/platform failure |
| K16 | Explicit agent on ordinary session creation retained; singleton Omnipus proof remains |
| K17 | Old default config ignored with warning; no guessed routing, permission migration or hidden default |
| K18 | Person/group/guild/team/account match shape named; define typed trusted identities, duplicate/conflict handling and admission interaction, R3-MAJ-004 |
| K19 | Main/per-person mode named; canonical keys/control boundaries and full channel revision need R3-MAJ-001/003/005 |
| K20 | Stranger status named; define count unit (events, as BDD-16.3), reset/lifetime and bounded rate-limit bookkeeping |
| K21 | Derived reachability endpoint named; never store a second allow-list source. Interpret admitted group/all-main rules truthfully |
| K22 | Instance binding match is config data, not a second rule store; global scope route retirement retained; connection session identity needs R3-MAJ-003 |
| K23 | Room member metadata named; current required agent ownership/type versus new kind must be reconciled. All-room mute/cost/derived attention need precise shape, R3-MAJ-012/013 |
| K24 | Creation/membership/deletion routes named; user-only trust, safe errors, validation, minimum-size/read-only lifecycle and team mute exception need complete operation shapes |
| K25 | Stable-ID mentions plus text resolution conflict needs R3-MAJ-011; never let a forged ID/changed label select an unintended member |
| K26 | Author and resolved addressees named; carry through the actual live/history/replay schema projections, R3-MAJ-013 |
| K27 | Request-correlated member frame/snapshot named; complete failure/unknown/multiple-outstanding semantics and sequencing need R3-MAJ-006/013 |
| K28 | Group stopped status named, but a ChannelEntry aggregate is not one independently scoped conversation. Define the keyed per-conversation read/update/status carrier after Q-R3-1 |
| K29 | Visibility response field alone does not define the PATCH request/operation; current rename requires title, R3-MAJ-013 |
| K30 | Unowned inspector surface, R3-OBS-001; audit shapes belong with their real consumer |
| K31 | **Intentionally removed** with GC-7 self hand-off; do not restore |
| K32 | Individual steering default false named; allow-list strings → typed entries, conflict/identity/revocation semantics and real validation required |
| K33 | Collapsed room ref named; request and answer must retain the same proven correlation across publication, replay, history and main mirror |
| K34 | Ledger room-origin tag named; durable fan-out settlement/cancellation/generation provenance needs R3-MAJ-007/008 |
| K35 | Pause/origin/read-only/after-pause fields named; expose/update them through defined operations and frames, not ad-hoc payloads |
| Across entries | Expand WU-0/§7.2 beyond K1–K17 and the old three malformed-payload tests. Preserve generated Go/TypeScript/Zod/inbound mirrors and inline union rule from **Discriminated-union create contract for agents** |

## Room reuse — what exists, what must change

```text
User room message / captured agent answer
              |
              v
Persist room entry + stable per-member delivery progress   [new]
              |
              v
Resolve mentions + current same-workspace membership       [new]
              |
              v
Receiver policy / approval + existing request capture      [reuse + adaptation]
              |
              v
Member's own main intake / model-consumption fence         [reuse + room-origin rule]
              |
              v
answer_request -> captured room reply                      [reuse + room-origin branch]
              |
              +--> room transcript/publication             [reuse + projections]
              +--> collapsed answer mirror in own main      [new narrow projection]
              +--> named-member fan-out, unless suppressed  [new]
```

| Existing mechanism | Boundary to preserve / required adaptation |
|---|---|
| Request ledger and receiver main | Keep captured receiver/source authority, append-before-model ordering and own-main execution; add stable room delivery/progress, not another message store |
| Receiver policy/Ask | Reuse current two-layer policy and membership check; represent approval before a capture exists and its cancellation/refusal |
| FIFO/intake | No per-room worker session or replacement engine. Name the room-origin busy behavior and the actual consumption fence |
| Reply/source binding | Keep authenticated responder, request correlation and final source check. A user-owned room has no agent to wake; selected mentions are the next recipients |
| Guest reply publication | Preserve responder identity and existing sequence/replay protocol; add necessary room metadata carriers and the main answer mirror |
| Pause | Room-owned send suppression and pending-request discard only. Never agent-wide Stop or cancellation of already-running/unrelated main work |
| Attention/cost | Derive from actual request/decision/turn facts. Neither can be guessed from an active room tab or missing data |
| Memory awareness | Rooms already reach addressed mains; no extra room-working sessions, self hand-off or awareness injection for the same traffic |

## Test coverage assessment — planned, not executed

| Missing/insufficient category | Required falsifiable proof |
|---|---|
| Real routing chain | Adapter admission → resolved target/mode → capture/session → reply egress; target differs from main; two same-type connections; membership/rebind changes |
| Scope/control modes | Direct/group/thread fixtures under both modes; model/clear/Stop/resume outcomes compare canonical sessions, configuration and unaffected conversations |
| Steering bypass negatives | Real early Stop/redirect/cancel and memory/goal/loop rewrite paths; off flag, group-only, duplicates and revocation; zero control effects and model delivery |
| Shared revisions | Settings changes only person rule, allow list, steering or mode while an approved helper retains old token; no silent overwrite |
| Partial room fan-out | Crash before/after each member admission; lost sender acknowledgment; unfinished members only on Retry; independent capture/model-start counts |
| Reply settlement | Same answer occurrence resent after append-before-ack; no duplicate main/room entry or mention delivery; legitimate distinct follow-up preserved |
| Pause/control concurrency | Approval/admission/dequeue racing Pause; Pause–Resume–old answer; deletion/member removal; running unrelated main remains untouched |
| Complete member states | Deny/rejected approval/capacity/storage/provider errors, stopped main, interrupted restart and unknown snapshot; truthful partial member failure, not eternal working |
| Memory isolation | Deterministic parallel workspace operations through real tools/context/retrospective/fallback/cache paths; populated distinct sentinels, not only sequential new-note tests |
| Recap freshness | Second idle after new input, unchanged input, restart and old completion after new recap; no turn-start summarization call |
| Cost | Independent mathematical expected totals for two rooms + web inputs, failed/interrupted work, dedupe and unknown price |
| Boundary datasets | Duplicate/multi-word/renamed/reserved mentions; room minimum/exact size and rate bounds; 500/501 rules; 20/21 context entries and 8,192-byte boundary; UTF-8 and identity normalization |
| New contracts | Valid positive controls plus malformed/null/unknown-field/oversize routing, steering, room/member/visibility writes; actual outbound frame union parsing and live/replay convergence |
| New UI composites | Room partial/error/paused/read-only states; membership/mentions/disclosure/steering keyboard, focus and screen-reader journeys; touch/narrow reflow and reduced motion |
| Engine acceptance | Exact-commit hands-on tester + independent validator for WU-14–17 as well as earlier engine/WebSocket units; fixed UAT provider/model; whole-feature five-reviewer gate |

The inventory scanner's controls cover full and abbreviated ranges, a known-present scenario and a deliberately absent scenario. It establishes only that declared references resolve. A source-only resolver test, mocked command executor, newly constructed memory store or room-only cost fixture cannot detect the whole-path defects identified here. Red-before-green, meaningful mutation controls, race/platform CI and actual reachability remain implementation obligations. **No product test, build, mutation, browser or live-model acceptance run was performed.**

## STRIDE threat summary

STRIDE groups identity spoofing, state tampering, missing action attribution, disclosure, resource exhaustion and privilege escalation. These are design obligations, not penetration-test results.

| Flow | Main unresolved threat | Existing controls / disposition |
|---|---|---|
| Channel intake/return | Wrong target or borrowed return ownership | Workspace-bound explicit routes, loud refusal and source-owner egress retained; R3-MAJ-002/003 |
| Channel commands/resume | Individual entitlement bypass or controls affecting shared web main | Steering off by default and server authority retained; R3-MAJ-001/004 |
| Shared channel config | Stale approved plan restores changed admission/steering/routing | One write boundary retained; revision must cover new fields, R3-MAJ-005 |
| Room fan-out/replies | Duplicate work/spend, pending approval not cancelled, stale post-Pause forwarding | User-only membership, captured sender/receiver and no agent approval authority retained; R3-MAJ-006–008 |
| Awareness/memory | Cross-workspace retrospective/bootstrap/concurrent scope disclosure | Same-workspace requirement retained; all memory paths must implement it, R3-MAJ-009 |
| Summaries/cost | Stale awareness and misleading visibility | Existing repeated-idle decision and turn accounting reused; R3-MAJ-010/012 |
| Unlimited replies | Unbounded conversational spend | **Founder-accepted R-A4**, not a new finding. Pause and honest cost remain the decided controls; no loop limit requested |
| Secret/management workflow | Accepted instruction injection/read-to-web/executable-selection exposure | R-A1–R-A3 preserved. Earlier remaining Ask/consent/recipient/receipt/masking protections must still be implemented and independently reviewed |

## Reachability, documentation and delivery

| Capability | Definition-level assessment |
|---|---|
| Seven management/secret tools plus `read_conversation` | All eight proposed new names have registration/policy intent in §6.5. Catalogue/executable and role/custom paths are distinct; current pre-implementation absence is expected. Exact-set correction R3-MIN-002; real selection/read dispatch proofs still required |
| Person routing and steering | Configure panel and derived agent-editor list named; actual source intake/reply and entitlement boundaries need R3-MAJ-002/004; hidden sidebar visibility write needs R3-MAJ-013 |
| Rooms | Sidebar, header, picker, membership UI, Add participant and collapsed main entries named. A user-owned room needs its own intake dispatch branch, not a fake agent or per-member session. Complete state/transport/recovery remains open |
| Agent awareness | Per-turn list + scoped read tool + recap outcomes named. New web inspector is not an approved entry point, R3-OBS-001 |
| User documentation | Specific existing connector, memory, settings, UI, security and tool pages plus a new room page named. These must land with the behavior and pass docs-verifier. Fix Stop/room scope wording; do not postpone new-scope docs to the final sweep |
| Work units | WU-14/15/16/17 correctly Feature; backend/frontend/prompt ownership and exact-commit UAT requirement retained. Add all expanded contracts to preparation; make the Landing row include S4. No independently incomplete protected/reachable stage may land |
| Simplification | Keep one request/reply path, existing ledger/queue/snapshot/audit/memory owners. Delete speculative inspector and stale per-member-session/self-hand-off remnants. Do not add a workflow engine, second vault, room runner or third tool-policy layer |

**Code correct and tested:** not claimed — specification and source review only.

**Reachable by a user or agent:** planned paths assessed; not demonstrated at runtime.

## Questions for the founder — NEW only

These are two unaddressed interactions between the newly combined scope choices. Recommendations are not decisions. Team-lead interviews before the fix; answer, for example, `Q-R3-1 B, Q-R3-2 A`. Do not re-ask GC-5, FQ-11/FQ-12 or recap frequency.

### Q-R3-1 — What can channel steering do when messages share the web main?

**Context:** A connection's default mode sends messages into the agent's actual web main session. Steering is separately decided to be conversation-only, and the spec promises that the web main remains unchanged. Those cannot both hold for `/clear`, `/stop`, `/goal` or a session model change. The same boundary determines what a stopped channel group is when several sources share that main.

**Impact:** Determines whether an entitled channel participant can control the shared web main, and whether isolated steering requires a separate channel conversation. No hidden second control context will be built over shared history.

| Option | Choice |
|---|---|
| A | In main mode, explicitly accept and label that conversation steering affects the shared main and every source using it. Separate per-person/group conversations remain isolated when selected. Update the web-unchanged acceptance promise accordingly |
| B | Keep main mode for ordinary messages, but refuse mutating channel steering while it shares the main; explain that isolated control requires a separate channel conversation. Keep steering functional in separately scoped channel conversations and define the group/thread scope table accordingly |

**Recommendation: B.** It preserves the shared-main default without giving a channel participant unintended control of the web conversation. The explicit refusal and available separate-conversation mode must be visible in help and Configure.

### Q-R3-2 — Do explicitly linked people share one conversation across connections?

**Context:** Global identity links are decided to stay. The spec additionally says linked Telegram and Slack identities share one per-person conversation, but names a key mechanism that keeps channels separate. Identity links can identify the same person without necessarily merging their conversations; the founder records do not choose between those two effects.

**Impact:** Determines whether history and conversation settings follow that person across connected accounts/platforms, or remain separate for each connection. Neither option may merge different workspaces or agents.

| Option | Choice |
|---|---|
| A | Explicitly linked identities share one per-person conversation across connections/platforms within the same workspace + agent pair. Unlinked identities stay connection-scoped; the connection of each message remains recorded for its reply |
| B | Every connection keeps its own per-person conversation, even for explicitly linked people. Links identify the person for matching/display, not merged conversation history or controls |

**Recommendation: A.** It matches BDD-16.12's explicit use case. Make the merge deliberate through the existing links and keep workspace + agent ownership non-negotiable; use connection-scoped keys for unlinked people.

## Verdict rationale and next action

**REVISE** follows from thirteen MAJOR defects in requirements that otherwise explicitly retain the decided boundaries. The two founder questions resolve newly combined conversation semantics; the other findings are technical corrections, not invitations to reopen accepted risks. Prior corrections are mostly retained at definition level, with the channel revision projection the remaining inherited residual.

| Next action | Owner |
|---|---|
| Interview only Q-R3-1/Q-R3-2; preserve all other authoritative answers | team-lead / founder |
| Correct the whole spec, including actual intake/authorization seams, room state/recovery/Pause, scoped memory, contracts and test oracles | spec author; architectural contract-shape owner as authorized |
| Keep accepted risks recorded while reviewing remaining authorization, scope and consent boundaries | security-lead, read-only reviewer |
| Run founder-approved EXTRA round 4 on the corrected whole spec, then its interview/fix. Do not infer another ADR grill or an unlimited review loop | team-lead / independent reviewer |
| Plan/implement only after the commissioned review/fix flow, with one complete protected/reachable landing, same-change docs and exact-commit UAT | team-lead / implementing leads |

This is **round 3 of 4 under the founder's added-scope exception**, not completion of the feature and not permission to land it. The report commit is delivered separately; no push is authorized.

skills: omnipus-shared-rules, grill-spec, gitnexus-exploring, ux-heuristics-review, jev-use:jev-use, github-cli, commit-messages

## Evidence table — grill-osa-spec-r3

| Claim | Evidence | Certainty |
|---|---|---|
| E01 — Whole input and prior correction obligations read | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md`::§0–20, all FR/BDD/K/test/trace/work-unit tables; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec-review.md`::all findings; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec-review-round2.md`::all findings and inheritance tables. First-hand complete reads; fingerprints above | Verified, high confidence |
| E02 — Latest founder decisions and round exception | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-system-agent-decisions-20261012.md`::D1–D27; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-adr-founder-answers-20261012.md`::all answer sections, especially After spec fix round 2 / GC-8–10 / FQ-RM-1–2 / GQ-3; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/channel-person-routing-decisions-20261013.md`::R1–R5 / PR-1–4 | Verified, high confidence |
| E03 — Architectural source hierarchy and shared-write correction | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md`::complete ADR including OSA-14–16 / dated precedence / accepted risks; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/AS-IS-architecture.md`::complete dated walkthrough; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/plugin-extensibility-assessment.md`::complete assessment; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/deps.go`::Deps lock-order contract. Current source wins over dated descriptions | Verified reads, high confidence |
| E04 — Real channel capture precedes routing; source ownership is independent | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop.go`::admitAndDispatch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/connector_intake.go`::boundConnectorPair / admitBoundConnectorInputCtx; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_inbound.go`::resolveMessageRoute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/routing/route.go`::ResolveRoute / filterBindings; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/channels/return_route.go`::checkReturnRoute | Verified source, high confidence |
| E05 — Linked per-channel keys retain channel; workspace/instance absent from key inputs | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/routing/session_key.go`::SessionKeyParams / BuildAgentPeerSessionKey / resolveLinkedPeerID | Verified source, high confidence |
| E06 — Earlier controls and rewrites must enforce entitlement | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/channels/base.go`::HandleMessage / IsAllowedSender; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/channels/controlparse.go`::interceptSessionControl / DispatchStopIfRecognized / DispatchRedirectIfRecognized; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_slash.go`::handleCommand / applyMemoryCommandPrompt / buildCommandsRuntime; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/commands/cmd_model.go`::modelHandler | Verified source, high confidence |
| E07 — Admission/Ask/held/live intake are not the specified room states unchanged | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/address_router.go`::RequestAdmission / AdmitRequest / admitRequest / requestReceiverApproval / recheckReceiverAdmission; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/session_worker.go`::enqueueOrRetry / trySteerIntoLiveTurn; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/request_reminder.go`::markRequestDelivered / receiverStopped / deliverHeldRequests | Verified source, high confidence |
| E08 — Reply append, owner wake and generated publication are separate | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/address_router.go`::Reply / replyIntoConversation / wakeSourceOwner / DiscardRequest; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/address_deps.go`::PublishGuestReply / PublishUserEntry; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/addressing/addressing.go`::Source / Capture.Validate | Verified source, high confidence |
| E09 — Ledger delivery/discard is durable, answered tracking/web retry is not room settlement | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/addressing/ledger.go`::Put / Resolve / Discard / MarkDelivered / Undelivered; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/request_reminder.go`::settleRequest; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/ws_client_message_dedupe.go`::answerRetriedMessage / releaseEvictedSessions | Verified source, high confidence; future recovery failure Inferred |
| E10 — Agent-shared memory scope and retrospective/bootstrap paths | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/memory.go`::SetWorkspaceID / rooms / resolveWriteRoom / SearchEntriesInScope / AppendRetro / WriteLastSession / ReadLastSession / GetMemoryContext; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/memory_adapter.go`::SetWorkspaceID / SearchEntriesInRoom / SearchRetros; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/memory.go`::RecallMemoryTool.Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/context.go`::buildSystemPromptForShelf / BuildSystemPromptWithCacheForWorkspace | Verified source, high confidence; concurrent outcome Inferred |
| E11 — Repeated idle already decided; existing claim/retro gates remain lifetime-once | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D9 (also D1/D3/D6/D7 preserve settled work/intake/source authority); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/session_end.go`::CloseSession / persistResponse / writeHeuristicFallbackRetroWithCount / BootstrapRecapPass / agentSessionHasRetro. `rg -n 'claimedCloseSessions'` in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent`, exit 0 → production definition and LoadOrStore; no production release of the lifetime claim in matched uses | Verified source/decision, high confidence |
| E12 — Linked issue requirements checked, not assumed from titles | `gh issue view <892,955,1068,1197,1277> --json number,title,state,body,url`, each exit 0 → all OPEN; #892 authorized resume/no automatic wake; #955 per-conversation control/web unchanged; #1068 peer/group security requirements; #1197 workspace recap leak; #1277 separate boot-degradation dependency. Latest founder answers supersede older proposed issue mechanics | Verified issue reads, high confidence |
| E13 — Rename does not define the room mention namespace | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_agents_update.go`::restAPIUpdateAgentFlow.preparePersistence; E01 FR-164/K25/D7 | Verified source/spec, high confidence |
| E14 — Existing accounting records producing-turn totals | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/turn.go`::AddTurnStats; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/turn_transcript.go`::appendAssistantTranscriptImpl; E01 FR-171/test 114 | Verified source/spec, high confidence |
| E15 — New live/update obligations compared with actual current contracts | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/openapi.yaml`::renameSession; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/asyncapi.yaml`::MessageFrame; shared schemas `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/Session.yaml`::ownership/attention; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionRenameRequest.yaml`::title; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/ChannelRouting.yaml`::properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/UserMessageFrame.yaml`::properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/TokenFrame.yaml`::properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/ReplayMessageFrame.yaml`::properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionStateFrame.yaml`::properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionSnapshotFrame.yaml`::snapshot protocol. E08 publication source read; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_inbound_validate.go`::decodeAndValidate documentation; **Discriminated-union create contract for agents** at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-034-agent-create-discriminated-union.md`::Decision | Verified contract/source reads, high confidence; no generation/validation run |
| E16 — Role grants, metadata and executable access are different | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent/role_policies_adr090.go`::ADR090RolePolicyInventory / adr090SparseRolePolicies; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent/seed.go`::denyAllThenOverride / NewCustomAgentToolsCfg; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/general_builtin_catalog.go`::GeneralBuiltinMetadata; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/central_builtin_registry.go`::buildCentralBuiltinRegistry. `rg -l AskUserQuestion` over the three tool-policy/catalogue roots, exit 0 → seed/default/catalogue/executable control; same roots new request/select/read tool names, exit 1 → expected pre-implementation absence | Verified source/search, high confidence |
| E17 — UI authority/catalogue/user-doc baseline read | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/requirements.md`::R34/R35; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/design-system/catalog.json`::entries (controlled JSON inventory printed existing Switch/Command/Dialog/DisclosureRow and old controls); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/design/design-system-definition.md`::D4–D8; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/brand/brand-guidelines.md`::complete guide; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/ui/CLAUDE.md`::control/publication rules. User docs `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/connectors.md`::complete page; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/memory.md`::complete page; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/tools.md`::complete page; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/security.md`::saved secrets / filtering limits; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/using-omnipus-ui.md`::chat / Stop / clear / retry / sessions | Verified reads, high confidence; rendered quality Unknown |
| E18 — Reference instrument detects ranges and missing controls | Inline `python3 -I` controlled document inventory, exit 0 → `instrument_controls 7 / 7 PASS`; `functional_requirements 160 matrix_rows 160 missing [] unknown []`; `bdd_scenarios 202 test_rows 139 trace_missing [] test_missing [] unknown_tests []`; contract IDs list has 34 rows, K31 absent intentionally. This is document analysis only. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/false-green-patterns.md`::complete guidance read before reporting the inventory | Verified static analysis, high confidence; not product-test proof |
| E19 — Limits and typed triage acknowledged honestly | `gitnexus status`, exit 0 → `Repository not indexed`; `gitnexus detect-changes --scope all --branch work/omnipus-agent-design-20261012`, exit 1 → branch not indexed, sibling 3582 commits behind. `jev-use judge` three-question batch via inline Python, child exit 0 → entitlement MAJOR, memory MAJOR, recap founder question NO; no escalation. Source/decision verification, not Jev, grounds findings | Verified instrument receipts, high confidence; graph impact unavailable |
| E20 — Mandatory project gates and accepted policy model checked | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/CLAUDE.md`::Hard Constraints #6/#8 / Definition of Done / retired surfaces / review sizes / human authorship; **Two-layer tool-policy model: the reconciled global ceiling IS the default; remove the fail-closed per-agent backfill** at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-077-two-layer-tool-policy-remove-fail-closed-backfill.md`::D1/D3–6; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/compositor.go`::effectiveToolPolicyWith; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config/security.go`::RegisterSensitiveValues | Verified reads, high confidence |
| E21 — Baseline and human author/committer verified before writing | `git rev-parse HEAD`, exit 0 → `3404ca905aabf41eb2f7654fcff1907b774bd2d5`; `git status --short` and staged diff empty; `git config user.email`, exit 0 → `10800669+daniel-piatkowski-ai@users.noreply.github.com`; `git var GIT_AUTHOR_IDENT` / `GIT_COMMITTER_IDENT`, each exit 0 → Daniel Piatkowski, same email. Git hook path inspected: sample hooks only. `shasum -a 256` recheck, exit 0 → fingerprints above unchanged | Verified pre-publication receipt, high confidence |
| **Self-check** | Full staged report diff re-read at `/Users/danielpiatkowski/.claude/projects/-Users-danielpiatkowski-AI-Agent-Workspace-omnipus-wt-omnipus-agent-design/aef77da4-bf7c-4709-ae0e-226ed4ce01c7/tool-results/bq4p85klm.txt` and the publication-row/path correction diff checked against all done-criteria. Inline `python3 -I` report audit, exit 0 → `report_self_check=PASS; counts=0/13/2/2; lenses=12; contract_rows=35_including_removed_K31; r1_dispositions=17; r2_dispositions=9; new_questions=2; input_hashes_unchanged=true; human_identity=true; staged_scope=report_only`. Verified every finding's scenario/evidence/severity/certainty/recommendation, decided risks, two genuinely new questions, both inheritance tables, 34 active contract entries plus removed K31 and no implementation success claim. `git diff --cached --check`, exit 0; only this report staged. Final commit author/committer/scope receipt accompanies dispatch; no push | Verified report/source self-check, high confidence; runtime acceptance Unknown |
